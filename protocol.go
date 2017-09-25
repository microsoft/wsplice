package wsplice

import (
	"encoding/json"
	"fmt"
	"strings"
)

type ErrorCode uint

const (
	BadJSON ErrorCode = 4000 + iota
	FrameTooShort
	FrameTooLong
	UnknownMethod
	UnknownConnection
	InvalidURL
	InvalidHostname
	DialError
)

func (e ErrorCode) Error() string {
	switch e {
	case BadJSON:
		return "Error parsing payload as JSON"
	case UnknownMethod:
		return "Unknown method name"
	case UnknownConnection:
			return "You are trying to send to a connection which does not exist"
	case FrameTooShort:
		return "The provided frame was too short, it must start with an socket index, or 0"
	case FrameTooLong:
		return "Maximum websocket frame length exceeded"
	case InvalidURL:
		return "Invalid URL provided"
	case InvalidHostname:
		return "You are not allowd to connect to that hostname"
	default:
		return fmt.Sprintf("Unknown error code %d", e)
	}
}

func (e ErrorCode) ResponseError() *ResponseError {
	return &ResponseError{Code: e, Message: e.Error()}
}

func (e ErrorCode) WithPath(path ...string) *ResponseError {
	r := e.ResponseError()
	r.Path = strings.Join(path, ".")
	return r
}

// ConnectCommand is sent on the controls socket when the client wants to
// initiate a new connection to a remote server.
type ConnectCommand struct {
	URL          string            `json:"url"`
	Headers      map[string]string `json:"headers"`
	Subprotocols []string          `json:"subprotocols"`
	Timeout      int               `json:"timeout"`
}

// ConnectResponse is sent back in response to a ConnectCommand
type ConnectResponse struct {
	Index int `json:"index"` // newly-allocated index to use to reference this socket
}

// A TerminateCommand is sent to signalClosed a socket, by its index.
type TerminateCommand struct {
	Index  int    `json:"index"`
	Code   int    `json:"code"`
	Reason string `json:"reason"`
}

// A TerminateResponse is sent in response to a TerminateCommand.
type TerminateResponse struct {
}

type SocketClosedCommand struct {
	Index  int    `json:"index"`
	Code   int    `json:"code"`
	Reason string `json:"reason"`
}

// Method is a generic RPC method call.
type Method struct {
	ID     int             `json:"id"`
	Type   string          `json:"type"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
}

// Reply is a generic RPC reply.
type Reply struct {
	ID     int            `json:"id"`
	Type   string         `json:"type"`
	Result interface{}    `json:"result,omitempty"`
	Error  *ResponseError `json:"error,omitempty"`
}

// A ResponseError can be included in method replies.
type ResponseError struct {
	Code    ErrorCode `json:"code"`
	Message string    `json:"message"`
	Path    string    `json:"path,omitempty"`
}

func (r ResponseError) Error() string { return r.Message }
