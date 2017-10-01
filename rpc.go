package wsplice

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"io"

	"github.com/gobwas/ws"
)

type methodMap map[string]func(json.RawMessage) (interface{}, error)

// RPC implements a simple bidirectional RPC server.
type RPC struct {
	config  *Config
	methods methodMap
}

// ReadMethodCall reads a Method off the reader, returning an error or the
// unmarshaled method call.
func (r *RPC) ReadMethodCall(sr io.Reader) (Method, error) {
	var method Method
	if err := json.NewDecoder(sr).Decode(&method); err != nil {
		return Method{}, BadJSON
	}

	return method, nil
}

// Dispatch sends a method call to the correct handler, return the packet to
// reply with, or an error.
func (r *RPC) Dispatch(method Method) (v interface{}, err error) {
	// Currently, we don't care about replies at all.
	if method.Type == "reply" {
		return nil, nil
	}
	handler := r.methods[method.Method]
	reply := Reply{ID: method.ID, Type: "reply"}
	if handler == nil {
		reply.Error = UnknownMethod.ResponseError()
	} else {
		reply.Result, err = handler(method.Params)
	}

	if rerr, ok := err.(*ResponseError); ok {
		reply.Error = rerr
		err = nil
	}

	return reply, err
}

// dialConnection executes a dial connection command. It errors if the host to
// dial to is not in the allowed list of hostnames.
func (s *Session) dialConnection(cmd ConnectCommand) (net.Conn, error) {
	targetUrl, err := url.Parse(cmd.URL)
	if err != nil {
		return nil, InvalidURL
	}

	if len(s.config.HostnameAllowlist) > 0 {
		var allowed bool
		for _, hostname := range s.config.HostnameAllowlist {
			if strings.ToLower(hostname) == strings.ToLower(targetUrl.Hostname()) {
				allowed = true
				break
			}
		}
		if !allowed {
			return nil, InvalidHostname.WithPath("url")
		}
	}

	headers := http.Header{}
	for key, value := range cmd.Headers {
		headers.Set(key, value)
	}

	// Set the dial timeout to what the socket asks for, up to a maximum of
	// the global dial timeout. Default to 10 seconds.
	var timeout time.Duration
	if cmd.Timeout > 0 {
		timeout = time.Millisecond * time.Duration(cmd.Timeout)
	}
	if timeout == 0 || (s.config.DialTimeout > 0 && timeout > s.config.DialTimeout) {
		timeout = s.config.DialTimeout
	}
	if timeout == 0 {
		timeout = 10 * time.Second
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	conn, _, err := ws.Dialer{Protocol: cmd.Subprotocols}.Dial(ctx, cmd.URL, headers)
	return conn, err
}

// insertConnection adds the net.Conn, as a websocket connection, into the
// connection list and returns the index it was inserted it.
func (s *Session) insertConnection(conn net.Conn) (index int) {
	cnx := &Connection{
		session: s,
		socket:  NewSocket(conn, s.config),
		config:  s.config,
	}

	s.connectionsMu.Lock()
	defer s.connectionsMu.Unlock()

	defer func() {
		cnx.index = index
		go cnx.Start()
	}()

	for i, cnx := range s.connections {
		if cnx == nil {
			index = i
			s.connections[i] = cnx
			return index
		}
	}

	index = len(s.connections)
	s.connections = append(s.connections, cnx)
	return index
}

func (s *Session) connect(params json.RawMessage) (interface{}, error) {
	var parsed ConnectCommand
	if err := json.Unmarshal(params, &parsed); err != nil {
		return nil, BadJSON
	}

	conn, err := s.dialConnection(parsed)
	if err != nil {
		return nil, &ResponseError{Code: DialError, Message: err.Error(), Path: "url"}
	}

	index := s.insertConnection(conn)
	return ConnectResponse{index}, nil
}

func (s *Session) terminate(params json.RawMessage) (interface{}, error) {
	var parsed TerminateCommand
	if err := json.Unmarshal(params, &parsed); err != nil {
		return nil, BadJSON
	}

	s.RemoveConnection(parsed.Index)
	return TerminateResponse{}, nil
}
