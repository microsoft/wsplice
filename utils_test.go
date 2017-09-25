package wsplice

import (
	"encoding/binary"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"strings"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

var upgrader websocket.Upgrader

type EndToEndSuite struct {
	suite.Suite
	wspliceServer *httptest.Server
	servers       []*httptest.Server
}

func TestEndToEndSuite(t *testing.T) {
	suite.Run(t, new(EndToEndSuite))
}

func (e *EndToEndSuite) SetupTest() {
	e.wspliceServer = httptest.NewServer(&Server{
		Config: &Config{
			FrameSizeLimit:    1024 * 512,
			ReadTimeout:       time.Second,
			WriteTimeout:      time.Second,
			DialTimeout:       time.Second,
			HostnameAllowlist: []string{"127.0.0.1"},
		},
	})
}

func (e *EndToEndSuite) TearDownTest() {
	for _, s := range e.servers {
		s.Close()
	}
	e.servers = nil
	e.wspliceServer.Close()
}

func (e *EndToEndSuite) connectSocket() *websocket.Conn {
	cnx, _, err := websocket.DefaultDialer.Dial("ws:"+e.wspliceServer.URL[5:], nil)
	require.Nil(e.T(), err)
	return cnx
}

func (e *EndToEndSuite) expectRead(cnx *websocket.Conn, index int, expected string) {
	prefix := getIndexPrefix(index)
	_, b, err := cnx.ReadMessage()

	e.expectNoerr(err)
	require.Equal(e.T(), prefix, b[:len(prefix)], "Got invalid prefix in message:", string(b))
	actual := string(b[len(prefix):])

	if expected[0] == '{' {
		require.JSONEq(e.T(), expected, actual)
	} else {
		require.Equal(e.T(), expected, actual)
	}
}

func (e *EndToEndSuite) expectReadError(cnx *websocket.Conn) error {
	_, _, err := cnx.ReadMessage()
	require.NotNil(e.T(), err, "expected to read an error")
	return err
}

func (e *EndToEndSuite) write(cnx *websocket.Conn, index int, message string) {
	e.expectNoerr(cnx.WriteMessage(websocket.BinaryMessage, append(getIndexPrefix(index), []byte(message)...)))
}

func (e *EndToEndSuite) expectWriteError(cnx *websocket.Conn, index int, message string, expectedErr string) {
	err := cnx.WriteMessage(websocket.BinaryMessage, append(getIndexPrefix(index), []byte(message)...))
	if err == nil {
		require.NotNil(e.T(), err, "Expected to have errored with %q, but got nil", message)
	} else {
		require.Contains(e.T(), err.Error(), expectedErr)
	}
}

func (e *EndToEndSuite) expectNoerr(err error) {
	if err != nil {
		require.Nil(e.T(), err, err.Error())
	}
}

func (e *EndToEndSuite) makeServer(handler func(conn *websocket.Conn) error) (address string) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := upgrader.Upgrade(w, r, nil)
		require.Nil(e.T(), err)

		defer c.Close()
		e.Nil(handler(c))
	}))

	e.servers = append(e.servers, s)

	// replace http: with ws:
	return "ws:" + s.URL[5:]
}

func echo(c *websocket.Conn) error {
	mt, message, err := c.ReadMessage()
	if err != nil {
		return nil
	}

	return c.WriteMessage(mt, message)
}

func forever(fn func(conn *websocket.Conn) error) func(conn *websocket.Conn) error {
	return func(c *websocket.Conn) error {
		for {
			if err := fn(c); err != nil {
				return err
			}
		}
	}
}

func yell(c *websocket.Conn) error {
	mt, message, err := c.ReadMessage()
	if err != nil {
		return nil
	}

	updated := strings.ToUpper(string(message))
	return c.WriteMessage(mt, []byte(updated))
}

func getIndexPrefix(index int) []byte {
	prefix := make([]byte, 2)
	binary.BigEndian.PutUint16(prefix, uint16(index))
	return prefix
}
