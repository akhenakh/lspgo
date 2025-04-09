package jsonrpc2

import (
	"encoding/json"
	"fmt"
)

const Version = "2.0"

// RequestMessage represents a JSON-RPC request.
type RequestMessage struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"` // Can be string, number, or null. RawMessage handles this.
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"` // Use RawMessage to defer parsing
}

// ResponseMessage represents a JSON-RPC response.
type ResponseMessage struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"` // Must match request ID
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *ErrorObject    `json:"error,omitempty"`
}

// NotificationMessage represents a JSON-RPC notification.
type NotificationMessage struct {
	JSONRPC string          `json:"jsonrpc"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// ErrorObject represents a JSON-RPC error object.
type ErrorObject struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"` // Optional structured data
}

func (e *ErrorObject) Error() string {
	return fmt.Sprintf("jsonrpc2 error %d: %s", e.Code, e.Message)
}

// Error codes defined by JSON-RPC 2.0 spec.
const (
	ParseError     = -32700
	InvalidRequest = -32600
	MethodNotFound = -32601
	InvalidParams  = -32602
	InternalError  = -32603
	// -32000 to -32099 are reserved for implementation-defined server errors.
)

// LSP specific error codes (defined in LSP spec)
const (
	ServerNotInitialized = -32002
	RequestCancelled     = -32800
	ContentModified      = -32801
	// ... other LSP specific codes
)

// NewError creates a new ErrorObject.
func NewError(code int, message string) *ErrorObject {
	return &ErrorObject{Code: code, Message: message}
}
