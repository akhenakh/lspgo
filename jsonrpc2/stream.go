package jsonrpc2

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
)

const (
	headerContentLength = "Content-Length"
	headerContentType   = "Content-Type" // Optional, often utf-8
	headerSeparator     = "\r\n"
)

// Stream handles reading and writing JSON-RPC messages over an io.ReadWriter.
type Stream struct {
	reader *bufio.Reader
	writer io.Writer
	source io.ReadWriter // Keep the original source
}

// NewStream creates a new Stream.
func NewStream(rw io.ReadWriter) *Stream {
	return &Stream{
		reader: bufio.NewReader(rw),
		writer: rw,
		source: rw,
	}
}

// Close closes the underlying source if it implements io.Closer.
func (s *Stream) Close() error {
	if closer, ok := s.source.(io.Closer); ok {
		return closer.Close()
	}
	return nil
}

// ReadMessage reads a single JSON-RPC message from the stream.
func (s *Stream) ReadMessage() ([]byte, error) {
	contentLength := -1
	// Read headers
	for {
		line, err := s.reader.ReadString('\n')
		if err != nil {
			// EOF or other read error during header read is critical
			return nil, fmt.Errorf("failed to read header line: %w", err)
		}

		line = strings.TrimSuffix(line, "\r\n") // Handle CRLF line endings

		// Empty line indicates end of headers
		if line == "" {
			break
		}

		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			// Malformed header, but try to continue reading headers
			// A robust server might log this and try to recover, but for simplicity,
			// we'll require valid headers. Alternatively, return error here.
			continue // Or: return nil, fmt.Errorf("malformed header line: %q", line)
		}

		headerName := strings.TrimSpace(parts[0])
		headerValue := strings.TrimSpace(parts[1])

		if strings.EqualFold(headerName, headerContentLength) {
			length, err := strconv.Atoi(headerValue)
			if err != nil {
				return nil, fmt.Errorf("invalid Content-Length: %q: %w", headerValue, err)
			}
			if length <= 0 {
				return nil, fmt.Errorf("invalid Content-Length: %d", length)
			}
			contentLength = length
		}
		// We can ignore Content-Type for now, assuming utf-8 json
	}

	if contentLength == -1 {
		return nil, fmt.Errorf("missing Content-Length header")
	}

	// Read the JSON content
	jsonData := make([]byte, contentLength)
	_, err := io.ReadFull(s.reader, jsonData)
	if err != nil {
		// EOF or error during content read
		return nil, fmt.Errorf("failed to read message content (expected %d bytes): %w", contentLength, err)
	}

	return jsonData, nil
}

// WriteMessage writes a JSON-RPC message to the stream.
// The msg parameter should be a struct marshallable to JSON (Request, Response, Notification).
func (s *Stream) WriteMessage(msg interface{}) error {
	jsonData, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("failed to marshal message: %w", err)
	}

	header := fmt.Sprintf("%s: %d%s%s",
		headerContentLength, len(jsonData), headerSeparator, headerSeparator) // Ends with \r\n\r\n

	// Write header and body together for atomicity (less chance of partial writes)
	var buf bytes.Buffer
	buf.WriteString(header)
	buf.Write(jsonData)

	_, err = s.writer.Write(buf.Bytes())
	if err != nil {
		return fmt.Errorf("failed to write message: %w", err)
	}
	// Flushing might be necessary depending on the underlying writer,
	// but typically Write handles it for os.Stdout, net.Conn etc.
	// if f, ok := s.writer.(interface{ Flush() error }); ok {
	//     if err := f.Flush(); err != nil {
	//         // Log or handle flush error
	//     }
	// }

	return nil
}
