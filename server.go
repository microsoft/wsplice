package wsplice

import (
	"encoding/binary"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"sync"

	"io/ioutil"

	"github.com/Sirupsen/logrus"
	"github.com/gobwas/ws"
	"github.com/mixer/go-ext/msync"
	"github.com/satori/go.uuid"
)

const (
	// defaultParallelism is the number of concurrent socket operations to
	// do at once, in calls like broadcast.
	defaultParallelism = 16
	// indexBytesSize is the number of bytes at the start of socket frames
	// which hold the index number. Defaults to 2 bytes, holding a uint16
	indexBytesSize = 2
	// controlIndex is the magic index that refers to the wsplice control channel.
	controlIndex = 0xffff
	// copyBufferSize is the size of the byte rawBuffer to use for io.CopyBuffered
	// operations.
	copyBufferSize = 32 * 1024
)

type Server struct {
	Config *Config
}

func (s *Server) ServeHTTP(rw http.ResponseWriter, r *http.Request) {
	conn, _, _, err := ws.UpgradeHTTP(r, rw, r.Header)
	if err != nil {
		return // ws will have written out the error to the http response
	}

	s.ServeConn(conn)
}

func (s *Server) ServeConn(conn net.Conn) {
	session := &Session{
		Socket:          *NewSocket(conn, s.Config),
		id:              uuid.NewV4().String(),
		config:          s.Config,
		readCopyBuffer:  make([]byte, copyBufferSize),
		writeCopyBuffer: make([]byte, copyBufferSize),
		connections:     []*Connection{},
		rpc:             RPC{config: s.Config},
	}

	session.rpc.methods = methodMap{
		"connect":   session.connect,
		"terminate": session.terminate,
	}

	session.Start()
}

type Session struct {
	Socket
	socketSendMu sync.Mutex
	id           string
	config       *Config
	rpc          RPC

	readCopyBuffer  []byte
	writeCopyBuffer []byte

	connectionsMu sync.Mutex
	connections   []*Connection
}

func (s *Session) Start() {
	logrus.WithFields(logrus.Fields{"id": s.id}).Infof("created new session")
	defer logrus.WithFields(logrus.Fields{"id": s.id}).Infof("client session ended")
	defer s.Close()

	var (
		err         error
		header      ws.Header
		target      Target
		frameReader = &io.LimitedReader{R: s.Socket.Reader}
	)

	for {
		s.Reader.Discard(int(frameReader.N))

		if header, err = s.ReadNextFrame(); err != nil {
			s.closeAll(ws.StatusAbnormalClosure, "Invalid socket header")
			return
		}
		frameReader.N = header.Length

		if header.OpCode == ws.OpClose {
			s.dispatchClose(header, frameReader)
			return
		}

		if header.OpCode != ws.OpContinuation {
			target, err = s.createTarget(&header, frameReader)
			if err != nil {
				s.handleError(err)
				continue
			}
		}

		if err := target.Pull(header, &s.Socket, frameReader); err != nil {
			s.handleError(err)
		}
	}
}

// readOpNumber reads the operation number off the upcoming websocket frame.
func (s *Session) createTarget(header *ws.Header, frame io.Reader) (Target, error) {
	var opBytes [indexBytesSize]byte
	if _, err := io.ReadFull(frame, opBytes[:]); err != nil {
		return nil, err
	}

	ws.Cipher(opBytes[:], header.Mask, 0)
	header.Mask = shiftCipher(header.Mask, indexBytesSize)
	header.Length -= indexBytesSize
	index := int(binary.BigEndian.Uint16(opBytes[:]))

	if index == controlIndex {
		return &RPCTarget{s}, nil
	}

	cnx := s.GetConnection(index)
	if cnx == nil {
		return nil, UnknownConnection
	}

	return &ConnectionTarget{s.readCopyBuffer, cnx}, nil
}

// dispatchClose reads the upcoming signalClosed frame off the socket and broadcasts
// it to all listening connections.
func (s *Session) dispatchClose(header ws.Header, frame *io.LimitedReader) {
	if frame.N > s.config.FrameSizeLimit {
		s.closeAll(ws.StatusMessageTooBig, "")
		return
	}

	payload, err := ioutil.ReadAll(frame)
	if err != nil {
		s.closeAll(ws.StatusNormalClosure, "")
		return
	}

	ws.Cipher(payload, header.Mask, 0)
	s.broadcast(ws.NewFrame(ws.OpClose, true, payload))
}

// closeAll closes all sockets, including the original client, with the code
// and reason message.
func (s *Session) closeAll(code ws.StatusCode, reason string) {
	frame := ws.NewCloseFrame(code, reason)
	s.WriteFrame(frame)
	s.Socket.Close()

	s.broadcast(ws.NewCloseFrame(code, reason))
	s.connectionsMu.Lock()
	s.connections = nil
	s.connectionsMu.Unlock()
}

// broadcast sends the websocket frame out to all connections.
func (s *Session) broadcast(frame ws.Frame) {
	s.connectionsMu.Lock()
	msync.Parallel(len(s.connections), defaultParallelism, func(i int) {
		if s.connections[i] != nil {
			s.connections[i].socket.WriteFrame(frame)
		}
	})
	s.connectionsMu.Unlock()
}

// broadcastFatalError broadcasts an error that terminates the session. It
// sends a signalClosed frame to all connections, then closes those in turn.
func (s *Session) broadcastFatalError(err error) {
	frame := ws.NewCloseFrame(ws.StatusGoingAway, err.Error())
	s.Close()

	s.connectionsMu.Lock()
	msync.Parallel(len(s.connections), defaultParallelism, func(i int) {
		cnx := s.connections[i]
		if cnx != nil {
			cnx.Close(frame)
		}
	})
	s.connections = nil
	s.connectionsMu.Unlock()
}

// handleError notifies appropriate clients about the error.
func (s *Session) handleError(err error) {
	// We ignore net errors and EOF errors. Those come from the client
	// themselves, the next frame we read will error when this happens.
	if err == io.EOF {
		return
	}

	switch t := err.(type) {
	case net.Error:
	case *json.SyntaxError:
		s.issueWarning(BadJSON)
	case ErrorCode:
		s.issueWarning(t)
	default:
		logrus.WithError(err).Warn("An unexpected error occurred")
	}
}

// GetConnection returns the connection at the index, or nil.
func (s *Session) GetConnection(index int) *Connection {
	s.connectionsMu.Lock()
	defer s.connectionsMu.Unlock()

	if index >= len(s.connections) {
		return nil
	}

	return s.connections[index]
}

func (s *Session) RemoveConnection(index int) {
	s.connectionsMu.Lock()
	defer s.connectionsMu.Unlock()

	if index >= len(s.connections) || s.connections[index] == nil {
		return
	}

	s.connections[index].socket.Close()
	s.connections[index] = nil
}

// issueWarning calls a "warn" method on the remote client.
func (s *Session) issueWarning(code ErrorCode) { s.SendMethod("warn", code.ResponseError()) }

// SendMethod dispatches a method call to the client,
// and returns without waiting for a response.
func (s *Session) SendMethod(name string, params interface{}) {
	inner, err := json.Marshal(params)
	if err != nil {
		logrus.WithError(err).Warn("Error marshalling call params")
		return
	}

	s.SendControlFrame(Method{
		Type:   "method",
		Method: name,
		Params: inner,
	})
}

// SendControlFrame pushes a method to the socket, prefixing it with the control index.
func (s *Session) SendControlFrame(v interface{}) {
	data, err := json.Marshal(v)
	if err != nil {
		logrus.WithError(err).WithField("packet", data).Warn("Error marshalling method packet")
		return
	}

	s.WriteIndexedData(controlIndex, ws.Header{Fin: true, OpCode: ws.OpText}, data)
}

func (s *Session) Close() {
	s.closeAll(ws.StatusGoingAway, "")
}
