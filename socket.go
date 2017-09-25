package wsplice

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"io"
	"net"
	"sync"
	"time"

	"github.com/gobwas/ws"
)

// Socket is a wrapper that provides useful utilities around a websocket net.Conn.
type Socket struct {
	Conn        net.Conn
	Reader      *bufio.Reader
	config      *Config
	buffer      []byte
	bytesBuffer bytes.Buffer
}

// NewSocket creates a new websocket.
func NewSocket(conn net.Conn, config *Config) *Socket {
	s := &Socket{
		Conn:   conn,
		config: config,
		Reader: bufio.NewReader(conn),
		buffer: make([]byte, copyBufferSize),
	}

	return s
}

// ReadNextFrame reads the next non-control or close frame off of the socket.
// Ping/pong frames are handled automatically.
func (s *Socket) ReadNextFrame() (header ws.Header, err error) {
	for {
		s.Conn.SetReadDeadline(time.Time{})
		if header, err = ws.ReadHeader(s.Reader); err != nil {
			return
		}

		switch header.OpCode {
		case ws.OpPing:
			s.Conn.SetWriteDeadline(time.Now().Add(s.config.WriteTimeout))
			s.WriteFrame(ws.NewPongFrame(nil))
		case ws.OpPong:
			// ignored
		default:
			return
		}
	}
}

// ReadNextWithBody returns the next non-control or close frame off the socket,
// joining fragmented messages bodies. This should only be used if you actually
// need to join fragmented messages, as it buffers data internally in memory.
func (s *Socket) ReadNextWithBody() (header ws.Header, r io.Reader, err error) {
	header, err = s.ReadNextFrame()
	if err != nil || header.OpCode == ws.OpClose {
		return header, io.LimitReader(s.Conn, header.Length), err
	}

	if header.Fin {
		return header, io.LimitReader(s.Reader, header.Length), nil
	}

	fc := NewFragmentCollector(header, s)
	return fc.Collect(s.config.FrameSizeLimit)
}

// CopyIndexedData copies data from the CountingReader to the socket.
func (s *Socket) CopyData(header ws.Header, r io.Reader) error {
	return s.CopyIndexedData(-1, header, r)
}

// CopyIndexedData copies data from the CountingReader to the socket.
func (s *Socket) WriteData(header ws.Header, b []byte) error {
	return s.WriteIndexedData(-1, header, b)
}

// CopyIndexedData copies data from the CountingReader to the socket, prefixing
// it with the index for the incoming socket.
func (s *Socket) CopyIndexedData(index int, header ws.Header, r io.Reader) (err error) {
	if index != -1 {
		header.Length += indexBytesSize
	}

	// We have a bytes buffer here that we use for constructing the index and
	// header. For small packets, we can for the buffer in memory entirely
	// instead of using io.Copy to save syscalls and network writes.
	rbuf := bytes.NewBuffer(s.buffer[:0])
	s.Conn.SetWriteDeadline(time.Now().Add(s.config.WriteTimeout))
	ws.WriteHeader(rbuf, header)

	if index != -1 {
		var indexBytes [indexBytesSize]byte
		binary.BigEndian.PutUint16(indexBytes[:], uint16(index))
		rbuf.Write(indexBytes[:])
	}

	if header.Length < int64(rbuf.Cap()-rbuf.Len()) {
		rbuf.ReadFrom(r)
		_, err = rbuf.WriteTo(s.Conn)
	} else {
		rbuf.WriteTo(s.Conn)
		_, err = io.CopyBuffer(s.Conn, r, s.buffer)
	}

	if disposable, ok := r.(Disposable); ok {
		disposable.Dispose()
	}

	return err
}

// CopyIndexedData writes data from the byte slice to the socket, prefixing
// it with the index for the incoming socket.
func (s *Socket) WriteIndexedData(index int, header ws.Header, b []byte) (err error) {
	header.Length = int64(len(b))
	if index != -1 {
		header.Length += indexBytesSize
	}

	s.Conn.SetWriteDeadline(time.Now().Add(s.config.WriteTimeout))
	ws.WriteHeader(s.Conn, header)

	if index != -1 {
		var indexBytes [indexBytesSize]byte
		binary.BigEndian.PutUint16(indexBytes[:], uint16(index))
		s.Conn.Write(indexBytes[:])
	}

	_, err = s.Conn.Write(b)

	return err
}

// WriteFrame writes a frame to the websocket.
func (s *Socket) WriteFrame(frame ws.Frame) error {
	s.Conn.SetWriteDeadline(time.Now().Add(s.config.WriteTimeout))
	return ws.WriteFrame(s.Conn, frame)
}

// Close closes the underlying connection.
func (s *Socket) Close() error {
	return s.Conn.Close()
}

var fragmentBufferPool = sync.Pool{New: func() interface{} { return &bytes.Buffer{} }}

// fragmentReader is a Disposable reader that returns the fragmentBuffer to
// the fragmentBufferPool
type fragmentReader struct {
	io.Reader
	fragmentBuffer *bytes.Buffer
}

func (f fragmentReader) Dispose() { f.fragmentBuffer.Reset(); fragmentBufferPool.Put(f.fragmentBuffer) }

// FragmentCollector is a structure that collects multiple fragmented websocket
// frames together into a single reader.
type FragmentCollector struct {
	header ws.Header
	socket *Socket
	buffer *bytes.Buffer
}

// NewFragmentCollector creates a collector on the socket. It assumes that the
// first header of a fragmented has just been read.
func NewFragmentCollector(header ws.Header, s *Socket) FragmentCollector {
	return FragmentCollector{header, s, fragmentBufferPool.Get().(*bytes.Buffer)}
}

// Collect returns a reader for the fragmented websocket frame.
func (f *FragmentCollector) Collect(maxSize int64) (header ws.Header, r io.Reader, err error) {
	header = f.header
	goto header

start:
	header, err = f.socket.ReadNextFrame()
	if err != nil {
		return header, nil, err
	}

header:
	if int64(f.buffer.Len())+header.Length > maxSize {
		f.socket.WriteFrame(ws.NewCloseFrame(ws.StatusMessageTooBig, ""))
		f.socket.Close()
		return header, r, io.EOF
	}

	var frameReader io.Reader
	if header.Masked {
		frameReader = NewMasked(&io.LimitedReader{R: f.socket.Reader, N: header.Length}, 0, header.Mask)
	} else {
		frameReader = io.LimitReader(f.socket.Reader, header.Length)
	}

	if header.OpCode == ws.OpClose {
		return header, frameReader, err
	}

	if !header.Fin {
		if _, err := f.buffer.ReadFrom(frameReader); err != nil {
			return header, nil, err
		}

		goto start
	}

	header = f.header
	header.Fin = true
	header.Length = int64(f.buffer.Len()) + header.Length
	return header, fragmentReader{io.MultiReader(f.buffer, frameReader), f.buffer}, err
}
