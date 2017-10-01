package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"time"

	"io"

	"github.com/Sirupsen/logrus"
	"github.com/gobwas/ws"
	"github.com/gobwas/ws/wsutil"
	"github.com/mixer/wsplice"
)

var (
	socketAddr    = "ws://127.0.0.1:3000"
	clients       = []int{32, 128, 512}
	payloadSizes  = []int{32, 128, 1024, 4098}
	payloads      = [][]byte{}
	benchDuration = time.Second * 5
)

func main() {
	for _, p := range payloadSizes {
		buf := &bytes.Buffer{}
		wsutil.WriteClientBinary(buf, make([]byte, p))
		payloads = append(payloads, buf.Bytes())
	}

	logrus.SetLevel(logrus.WarnLevel)

	server := &wsplice.Server{
		Config: &wsplice.Config{
			FrameSizeLimit:    1024 * 512,
			ReadTimeout:       time.Second,
			WriteTimeout:      time.Second,
			DialTimeout:       time.Second,
			HostnameAllowlist: []string{"127.0.0.1"},
		},
	}

	go func() {
		if err := http.ListenAndServe("127.0.0.1:3000", server); err != nil {
			log.Fatal("Could not set up wsplice listener", err)
		}
	}()

	time.Sleep(time.Millisecond * 50) // give the http server time to bind

	BenchmarkLatency()
	BenchmarkThroughput()
}

func BenchmarkLatency() {
	server := CreateEchoServer()

	for _, clientsCount := range clients {
		conns := RequestConnections(clientsCount, server)

		for _, payload := range payloads {
			var (
				latency, samples int64
				stopped          int64
				wg               sync.WaitGroup
			)
			for _, conn := range conns {
				wg.Add(1)
				go func(conn net.Conn) {
					buffer := make([]byte, 0, len(payload))
					for atomic.LoadInt64(&stopped) == 0 {
						start := time.Now()
						conn.Write(payload)
						io.ReadFull(conn, buffer)
						atomic.AddInt64(&latency, int64(time.Now().Sub(start)))
						atomic.AddInt64(&samples, 1)
						time.Sleep(time.Millisecond * 5)
					}
					wg.Done()
				}(conn)
			}

			time.Sleep(benchDuration)
			atomic.StoreInt64(&stopped, 1)
			wg.Wait()

			final := float64(latency/int64(time.Microsecond)) / float64(samples) / 2
			fmt.Printf("clients=%d, payload=%db, latency=%.1fus\n", clientsCount, len(payload), final)
		}

		for _, conn := range conns {
			conn.Close()
		}
	}
}

func BenchmarkThroughput() {
	var count int64
	server := CreateCountingServer(&count)

	for _, clientsCount := range clients {
		conns := RequestConnections(clientsCount, server)
		for _, payload := range payloads {
			var (
				stopped int64
				wg      sync.WaitGroup
			)
			for _, conn := range conns {
				wg.Add(1)
				go func(conn net.Conn) {
					for atomic.LoadInt64(&stopped) == 0 {
						conn.Write(payload)
					}
					wg.Done()
				}(conn)
			}

			time.Sleep(benchDuration)
			final := atomic.LoadInt64(&count)
			atomic.StoreInt64(&stopped, 1)
			bps := float64(final) / (float64(benchDuration) / float64(time.Second)) * 8
			fmt.Printf("clients=%d, payload=%db, throuput=%.1fmbps\n", clientsCount, len(payload), bps/1024/1024)
			wg.Wait()
			atomic.StoreInt64(&count, 0)
		}

		for _, conn := range conns {
			conn.Close()
		}
	}
}

func CreateCountingServer(count *int64) string {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, _, _, err := ws.UpgradeHTTP(r, w, r.Header)
		if err != nil {
			log.Fatal("error upgrading client socket", err)
		}
		defer conn.Close()

		buffer := make([]byte, payloadSizes[len(payloadSizes)-1])
		for {
			n, err := conn.Read(buffer)
			atomic.AddInt64(count, int64(n))
			if err != nil {
				return
			}
		}
	}))

	return "ws:" + s.URL[5:]
}

func CreateEchoServer() string {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, _, _, err := ws.UpgradeHTTP(r, w, r.Header)
		if err != nil {
			log.Fatal("error upgrading client socket", err)
		}
		defer conn.Close()

		buffer := make([]byte, payloadSizes[len(payloadSizes)-1])
		for {
			n, err := conn.Read(buffer)
			if err != nil {
				return
			}

			conn.Write(buffer[:n])
		}
	}))

	return "ws:" + s.URL[5:]
}

func RequestConnections(n int, target string) (conns []net.Conn) {
	req := append(
		[]byte{0xff, 0xff},
		[]byte(`{"id":1,"type":"method","method":"connect","params":{"url":"`+target+`"}}`)...,
	)

	for i := 0; i < n; i++ {
		conn, _, err := ws.Dial(context.Background(), socketAddr, nil)
		if err != nil {
			log.Fatal("Error provisioning connection", err)
		}

		if err := wsutil.WriteClientText(conn, req); err != nil {
			log.Fatal("Error writing connect request", conn)
		}

		b, err := ws.ReadFrame(conn)
		if err != nil {
			log.Fatal("Error reading client response", err)
		}

		var data wsplice.Reply
		if err := json.Unmarshal(b.Payload[2:], &data); err != nil {
			log.Fatal("Error unmarshaling client response", conn)
		}

		if data.Error != nil {
			log.Fatal("Unexpected error creating client", conn)
		}

		conns = append(conns, conn)
	}

	return conns
}
