package wsplice

import (
	"io"
	"io/ioutil"

	"github.com/gobwas/ws"
)

type Connection struct {
	index   int
	session *Session
	config  *Config
	socket  *Socket
}

// Start begins reading data from the connection, sending it to the Session.
func (c *Connection) Start() {
	defer c.socket.Close()

	for {
		header, r, err := c.socket.ReadNextWithBody()
		if err != nil {
			c.signalClosed(ws.StatusGoingAway, "")
			return
		}

		if header.OpCode == ws.OpClose {
			c.readClosed(r)
			return
		}

		c.session.CopyIndexedData(c.index, header, r)
	}
}

func (c *Connection) readClosed(r io.Reader) {
	data, err := ioutil.ReadAll(r)
	if err != nil {
		c.signalClosed(ws.StatusGoingAway, "")
		return
	}

	c.signalClosed(ws.ParseCloseFrameData(data))
}

func (c *Connection) signalClosed(code ws.StatusCode, reason string) {
	c.session.RemoveConnection(c.index)
	c.session.SendMethod("onSocketClosed", SocketClosedCommand{
		Index:  c.index,
		Code:   int(code),
		Reason: reason,
	})
}

func (c *Connection) Close(frame ws.Frame) {
	c.socket.WriteFrame(frame)
	c.socket.Close()
}
