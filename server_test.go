package wsplice

import (
	"crypto/rand"
	"strings"

	"github.com/stretchr/testify/require"
)

func (e *EndToEndSuite) TestErrorsSendingToNonexistentConnection() {
	cnx := e.connectSocket()
	e.write(cnx, 0, `{"hello":"world!"}`)
	e.expectRead(cnx, 0xffff, `{"id":0,"type":"method","method":"warn","params":{"code":4004,`+
		`"message":"You are trying to send to a connection which does not exist"}}`)
}

func (e *EndToEndSuite) TestPingPong() {
	url := e.makeServer(echo)
	cnx := e.connectSocket()

	e.write(cnx, 0xffff, `{"type":"method","method":"connect","params":{"url":"`+url+`"}}`)
	e.expectRead(cnx, 0xffff, `{"id":0,"type":"reply","result":{"index":0}}`)
	e.write(cnx, 0, `{"hello":"world!"}`)
	e.expectRead(cnx, 0, `{"hello":"world!"}`)

	e.servers[0].Close()
	e.expectRead(cnx, 0xffff, `{"id":0,"type":"method","method":"onSocketClosed","params":{"code":1001,"reason":"","index":0}}`)
}

func (e *EndToEndSuite) TestHandlesFragmentedMessages() {
	url := e.makeServer(echo)
	cnx := e.connectSocket()

	// Gorilla fragments messages at 64kb, a 400kb message will be split up.
	// Make sure it can ping and pong!
	data := make([]byte, 1024*400)
	_, err := rand.Read(data)
	require.Nil(e.T(), err)

	e.write(cnx, 0xffff, `{"type":"method","method":"connect","params":{"url":"`+url+`"}}`)
	e.expectRead(cnx, 0xffff, `{"id":0,"type":"reply","result":{"index":0}}`)
	e.write(cnx, 0, string(data))
	e.expectRead(cnx, 0, string(data))
}

func (e *EndToEndSuite) TestDisallowsHostsNotOnList() {
	e.makeServer(echo)
	cnx := e.connectSocket()
	e.write(cnx, 0xffff, `{"type":"method","method":"connect","params":{"url":"wss://example.com"}}`)
	e.expectRead(cnx, 0xffff, `{"id":0,"type":"reply","error":{"code":4007,"message":`+
		`"You are not allowd to connect to that hostname","path":"url"}}`)
}

func (e *EndToEndSuite) TestDisallowsTooLargeFrames() {
	e.makeServer(echo)
	cnx := e.connectSocket()
	e.expectWriteError(cnx, 0xffff, `{"method":"`+strings.Repeat("hello", 1024*1024)+`"}`, "write")
}

func (e *EndToEndSuite) TestMultiplexes() {
	url1 := e.makeServer(forever(echo))
	url2 := e.makeServer(forever(yell))
	cnx := e.connectSocket()

	e.write(cnx, 0xffff, `{"id":1,"type":"method","method":"connect","params":{"url":"`+url1+`"}}`)
	e.expectRead(cnx, 0xffff, `{"id":1,"type":"reply","result":{"index":0}}`)
	e.write(cnx, 0xffff, `{"id":2,"type":"method","method":"connect","params":{"url":"`+url2+`"}}`)
	e.expectRead(cnx, 0xffff, `{"id":2,"type":"reply","result":{"index":1}}`)

	e.write(cnx, 0, `{"hello":"world!"}`)
	e.expectRead(cnx, 0, `{"hello":"world!"}`)

	e.write(cnx, 1, `{"hello":"world!"}`)
	e.expectRead(cnx, 1, `{"HELLO":"WORLD!"}`)
}
