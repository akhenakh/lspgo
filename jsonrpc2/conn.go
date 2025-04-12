package jsonrpc2

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sync"
)

// Conn manages reading/writing JSON-RPC messages via a Stream.
type Conn struct {
	stream *Stream
	mu     sync.Mutex // Protects concurrent writes
	closed bool
}

// NewConn creates a new connection manager.
func NewConn(stream *Stream) *Conn {
	return &Conn{
		stream: stream,
	}
}

// Read decodes the next message from the stream.
// It blocks until a message is received or an error occurs.
// Handles context cancellation during the read operation if the underlying stream supports it implicitly (less likely)
// or explicitly checks context before/after blocking read. The primary use here is to unblock Run loop.
func (c *Conn) Read(ctx context.Context) (interface{}, error) {
	// Check context before blocking read
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}

	// Read raw bytes
	jsonData, err := c.stream.ReadMessage()
	if err != nil {
		c.mu.Lock()
		c.closed = true // Assume fatal error or EOF closes connection
		c.mu.Unlock()
		return nil, err // e.g., io.EOF, format errors
	}

	// Determine message type (Request, Response, or Notification)
	// We need to partially decode to find "method" and "id" fields.
	var base struct {
		JSONRPC string          `json:"jsonrpc"`
		ID      json.RawMessage `json:"id"`
		Method  string          `json:"method"`
	}
	if err := json.Unmarshal(jsonData, &base); err != nil {
		return nil, NewError(ParseError, fmt.Sprintf("failed to parse base message: %v", err))
	}

	if base.Method != "" {
		if len(base.ID) > 0 && string(base.ID) != "null" {
			// It's a Request
			var req RequestMessage
			if err := json.Unmarshal(jsonData, &req); err != nil {
				return nil, NewError(ParseError, fmt.Sprintf("failed to parse request message: %v", err))
			}
			return &req, nil
		}
		// It's a Notification
		var ntf NotificationMessage
		if err := json.Unmarshal(jsonData, &ntf); err != nil {
			return nil, NewError(ParseError, fmt.Sprintf("failed to parse notification message: %v", err))
		}
		return &ntf, nil
	}

	// It must be a Response (we don't expect to *receive* responses in a server context,
	// but a generic Conn should handle it). Could also be an error during parsing.
	if len(base.ID) > 0 && string(base.ID) != "null" {
		var resp ResponseMessage
		if err := json.Unmarshal(jsonData, &resp); err != nil {
			return nil, NewError(ParseError, fmt.Sprintf("failed to parse response message: %v", err))
		}
		return &resp, nil
	}

	// Invalid message structure
	return nil, NewError(InvalidRequest, "message is not a valid request, notification, or response")

}

// Write encodes and sends a message (Request, Response, Notification) to the stream.
// It is safe for concurrent use. Handles context cancellation before writing.
func (c *Conn) Write(ctx context.Context, msg interface{}) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.closed {
		return io.ErrClosedPipe
	}

	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	return c.stream.WriteMessage(msg)
}

// Close closes the underlying stream.
func (c *Conn) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.closed {
		return nil // Already closed
	}
	c.closed = true

	// Use the Stream's Close method which handles the original source
	return c.stream.Close()
}
