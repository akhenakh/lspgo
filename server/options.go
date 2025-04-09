package server

import (
	"io"
	"log"
	"os"
)

// Option defines a function signature for configuring the Server.
type Option func(*options)

// options holds the configurable settings for a Server.
type options struct {
	stream io.ReadWriter // Default: os.Stdin/os.Stdout
	logger *log.Logger   // Default: log to os.Stderr
}

// defaultOptions returns the default server configuration.
func defaultOptions() *options {
	return &options{
		stream: ReadWriter{os.Stdin, os.Stdout}, // Combine stdin/stdout
		logger: log.New(os.Stderr, "lsp: ", log.LstdFlags|log.Lshortfile),
	}
}

// WithStream sets the input/output stream for the server connection.
func WithStream(rw io.ReadWriter) Option {
	return func(o *options) {
		o.stream = rw
	}
}

// WithLogger sets the logger used by the server.
func WithLogger(l *log.Logger) Option {
	return func(o *options) {
		o.logger = l
	}
}

// ReadWriter combines an io.Reader and io.Writer into an io.ReadWriter.
// Useful for using os.Stdin and os.Stdout together.
type ReadWriter struct {
	io.Reader
	io.Writer
}

// Close attempts to close the underlying streams if they support it.
// Primarily useful if the stream is something like a net.Conn.
// os.Stdin/Stdout don't typically need closing in this context.
func (rw ReadWriter) Close() error {
	var errR, errW error
	cR, okR := rw.Reader.(io.Closer)
	cW, okW := rw.Writer.(io.Closer)

	if okR {
		errR = cR.Close()
	}

	// Close the writer only if it's a closer AND it's different from the reader's closer
	// (or if the reader wasn't a closer).
	if okW && (!okR || cR != cW) {
		errW = cW.Close()
	}

	if errR != nil {
		return errR // Prioritize reader error
	}
	return errW // Return writer error if reader error was nil
}
