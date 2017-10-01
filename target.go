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
	Close()
}

// RPCTarget is a target for an RPC call.
type RPCTarget struct {
	s          *Session
	copyBuffer []byte
	totalRead  int64
	writer     *io.PipeWriter
}

// NewRPCTarget creates and returns a new target for the RPC call.
func NewRPCTarget(copyBuffer []byte, s *Session) *RPCTarget {
	reader, writer := io.Pipe()
	r := &RPCTarget{
		s:          s,
		copyBuffer: copyBuffer,
		writer:     writer,
	}

	go func() {
		method, err := s.rpc.ReadMethodCall(reader)
		defer reader.Close()
		if err != nil {
			return
		}
		data, err := r.s.rpc.Dispatch(method)
		if err != nil {
			s.handleError(err)
		} else {
			s.SendControlFrame(data)
		}
	}()

	return r
}

// Pull implements Target.Pull. It pipes the RPC call from the socket to the
// RPC goroutine (kicked off in NewRPCTarget)
func (r *RPCTarget) Pull(header ws.Header, socket *Socket, frame *io.LimitedReader) (err error) {
	if r.totalRead+header.Length > socket.config.FrameSizeLimit {
		socket.WriteFrame(ws.NewCloseFrame(ws.StatusMessageTooBig, ""))
		socket.Close()
		return io.EOF
	}

	var reader io.Reader
	if header.Masked {
		reader = NewMasked(frame, 0, header.Mask)
	} else {
		reader = frame
	}

	n, err := io.CopyBuffer(r.writer, reader, r.copyBuffer)
	if err != nil {
		return err
	}
	r.totalRead += n

	if header.Fin {
		r.writer.Close()
	}

	return nil
}

// Close implements Target.Close.
func (r *RPCTarget) Close() { r.writer.Close() }

// ConnectionTarget is a target to write out the data to an external connection.
type ConnectionTarget struct{ c *Connection }

// Pull implements Target.Pull. It copies the frame to the target connection.
func (c *ConnectionTarget) Pull(header ws.Header, _ *Socket, frame *io.LimitedReader) (err error) {
	c.c.socket.CopyData(header, frame)
	return
}

// Close implements Target.Close.
func (c *ConnectionTarget) Close() {}
