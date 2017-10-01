package wsplice

import (
	"io"

	"github.com/gobwas/ws"
)

// Target is an "action" that the session takes. When the Session reads the
// start of a new frame/operation, it decides what target that hits and sets
// it internally. PushFrame can be called many times (for fragmented messages).
type Target interface {
	Pull(header ws.Header, socket *Socket, frame *io.LimitedReader) (err error)
}

// RPCTarget is a target for an RPC call.
type RPCTarget struct {
	s *Session
}

// Pull implements Target.Pull. It reads the full RPC call off of the socket
// and dispatches it on the session.
func (r *RPCTarget) Pull(header ws.Header, socket *Socket, frame *io.LimitedReader) (err error) {
	var reader io.Reader
	if !header.Fin {
		fc := NewFragmentCollector(header, socket)
		header, reader, err = fc.Collect(r.s.config.FrameSizeLimit)
		if err != nil {
			return err
		}
	} else if header.Masked {
		reader = NewMasked(frame, 0, header.Mask)
	} else {
		reader = frame
	}

	method, err := r.s.rpc.ReadMethodCall(reader)
	if err != nil {
		return err
	}

	go func() {
		data, err := r.s.rpc.Dispatch(method)
		if err != nil {
			r.s.handleError(err)
		} else {
			r.s.SendControlFrame(data)
		}
	}()

	return nil
}

// ConnectionTarget is a target to write out the data to an external connection.
type ConnectionTarget struct {
	copyBuffer []byte
	c          *Connection
}

// Pull implements Target.Pull. It copies the frame to the target connection.
func (c *ConnectionTarget) Pull(header ws.Header, _ *Socket, frame *io.LimitedReader) (err error) {
	c.c.socket.CopyData(header, frame)
	return
}
