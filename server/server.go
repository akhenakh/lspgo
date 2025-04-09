package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"sync"
	"sync/atomic" // For atomic state checks

	"github.com/akhenakh/lspgo/jsonrpc2"
	"github.com/akhenakh/lspgo/protocol"
)

// Server represents an LSP server.
type Server struct {
	conn         *jsonrpc2.Conn
	handlers     map[string]*typedHandler
	mu           sync.RWMutex
	state        atomic.Value // Stores serverState (uninitialized, initializing, running, shutdown)
	shutdownOnce sync.Once
	pendingReqs  sync.WaitGroup
	logger       *log.Logger
	initParams   *protocol.InitializeParams // Store params from client
	initResult   *protocol.InitializeResult // Store result we sent
}

// serverState represents the lifecycle state of the server.
type serverState int

const (
	stateUninitialized serverState = iota
	stateInitializing
	stateRunning
	stateShutdown
)

// NewServer creates a new LSP server instance.
// It typically communicates over stdin/stdout.
func NewServer(opts ...Option) *Server {
	s := &Server{
		handlers: make(map[string]*typedHandler),
		// Use os.Stdin/os.Stdout by default
		logger: log.New(os.Stderr, "lsp: ", log.LstdFlags),
	}
	s.state.Store(stateUninitialized)

	// Apply options
	options := defaultOptions()
	for _, opt := range opts {
		opt(options)
	}
	s.logger = options.logger

	// Setup connection using the configured stream
	stream := jsonrpc2.NewStream(options.stream)
	s.conn = jsonrpc2.NewConn(stream)

	// Register standard handlers
	s.registerDefaultHandlers()

	return s
}

// registerDefaultHandlers registers handlers for required LSP methods.
func (s *Server) registerDefaultHandlers() {
	// Use Register method to ensure validation
	s.Register("initialize", s.handleInitialize)
	s.Register("initialized", s.handleInitialized)
	s.Register("shutdown", s.handleShutdown)
	s.Register("exit", s.handleExit)
	// Example: Log unsupported methods
	// s.DefaultHandler = func(...) { log/return method not found }
}

// Register associates a handler function with an LSP method name.
// The handler func must match the expected signature patterns (see handler.go).
// paramExample is an instance of the expected parameter type (e.g., protocol.InitializeParams{})
// used for type reflection. Use nil if the method has no parameters.
func (s *Server) Register(method string, handlerFunc interface{}) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.handlers[method]; exists {
		return fmt.Errorf("handler already registered for method: %s", method)
	}

	paramType, err := validateHandlerFunc(handlerFunc)
	if err != nil {
		return fmt.Errorf("invalid handler for method %s: %w", method, err)
	}

	s.handlers[method] = &typedHandler{h: handlerFunc, paramType: paramType}
	s.logger.Printf("Registered handler for method: %s", method)
	return nil
}

// Run starts the server's main loop, reading and processing messages.
// It blocks until the connection is closed or the server exits.
func (s *Server) Run(ctx context.Context) error {
	s.logger.Println("Server starting...")
	defer s.logger.Println("Server stopped.")
	defer s.conn.Close() // Ensure connection is closed on exit

	for {
		select {
		case <-ctx.Done():
			return ctx.Err() // Context cancelled or deadline exceeded
		default:
			// Continue processing messages
		}

		// Read one message
		msg, err := s.conn.Read(ctx) // Pass context for cancellation during read
		if err != nil {
			if err == io.EOF {
				s.logger.Println("Client disconnected.")
				// Check state: if not shutdown gracefully, maybe log an error?
				if s.currentState() != stateShutdown {
					s.logger.Println("Client closed connection unexpectedly.")
					return fmt.Errorf("client closed connection unexpectedly: %w", io.ErrUnexpectedEOF)
				}
				return nil // Graceful exit after shutdown sequence
			}
			// Log other read errors and attempt to continue? Or is it fatal?
			// A parsing error might allow recovery, network error likely fatal.
			// For simplicity, treat most read errors as fatal for now.
			s.logger.Printf("Error reading message: %v", err)
			// Try sending error response if it was a request ID parse issue? Complex.
			return fmt.Errorf("failed to read message: %w", err)
		}

		// Process the message in a separate goroutine for concurrency,
		// but manage lifecycle carefully (e.g., shutdown).
		// Add to waitgroup *before* launching goroutine.
		s.pendingReqs.Add(1)
		go func(m interface{}) {
			defer s.pendingReqs.Done()
			s.handleMessage(ctx, m)
		}(msg)

		// Special handling for exit notification - it should cause Run to return.
		// We check state inside handleMessage, but might need signal here too.
		// The exit handler should coordinate this, perhaps by cancelling the context.
	}
}

// currentState safely gets the current server state.
func (s *Server) currentState() serverState {
	return s.state.Load().(serverState)
}

// handleMessage dispatches incoming messages to appropriate handlers.
func (s *Server) handleMessage(ctx context.Context, msg interface{}) {
	// Determine message type (Request, Notification)
	switch m := msg.(type) {
	case *jsonrpc2.RequestMessage:
		s.handleRequest(ctx, m)
	case *jsonrpc2.NotificationMessage:
		s.handleNotification(ctx, m)
	default:
		// Should not happen if jsonrpc2.Conn.Read works correctly
		s.logger.Printf("Received unknown message type: %T", msg)
	}
}

// handleRequest handles an incoming request message.
func (s *Server) handleRequest(ctx context.Context, req *jsonrpc2.RequestMessage) {
	method := req.Method
	s.logger.Printf("Received request: Method=%s, ID=%s", method, string(req.ID))

	// State checks
	currentState := s.currentState()
	if currentState == stateShutdown {
		s.logger.Printf("Rejecting request %s during shutdown.", method)
		errResp := jsonrpc2.NewError(jsonrpc2.InvalidRequest, "server is shutting down")
		s.sendResponse(ctx, req.ID, nil, errResp)
		return
	}
	if currentState == stateUninitialized && method != "initialize" {
		s.logger.Printf("Rejecting request %s before initialization.", method)
		errResp := jsonrpc2.NewError(jsonrpc2.ServerNotInitialized, "server not initialized")
		s.sendResponse(ctx, req.ID, nil, errResp)
		return
	}
	if currentState == stateInitializing && method != "initialize" {
		// Should not happen if initialize is handled synchronously, but belt-and-suspenders
		s.logger.Printf("Rejecting request %s during initialization.", method)
		errResp := jsonrpc2.NewError(jsonrpc2.ServerNotInitialized, "server is initializing")
		s.sendResponse(ctx, req.ID, nil, errResp)
		return
	}

	s.mu.RLock()
	handler, found := s.handlers[method]
	s.mu.RUnlock()

	if !found {
		s.logger.Printf("No handler found for request method: %s", method)
		errResp := jsonrpc2.NewError(jsonrpc2.MethodNotFound, fmt.Sprintf("method not found: %s", method))
		s.sendResponse(ctx, req.ID, nil, errResp)
		return
	}

	// Invoke the handler
	result, err := handler.invoke(ctx, s.conn, req.Params) // Pass conn for potential notifications

	// Send the response
	var errResp *jsonrpc2.ErrorObject
	if err != nil {
		// Check if it's already a jsonrpc2 error
		if jsonErr, ok := err.(*jsonrpc2.ErrorObject); ok {
			errResp = jsonErr
		} else {
			// Wrap other errors as internal server errors
			errResp = jsonrpc2.NewError(jsonrpc2.InternalError, err.Error())
		}
		s.logger.Printf("Handler error for method %s: %v", method, err) // Log the original Go error
	}

	s.sendResponse(ctx, req.ID, result, errResp)
}

// handleNotification handles an incoming notification message.
func (s *Server) handleNotification(ctx context.Context, n *jsonrpc2.NotificationMessage) {
	method := n.Method
	s.logger.Printf("Received notification: Method=%s", method)

	// State checks
	currentState := s.currentState()
	if currentState == stateShutdown && method != "exit" {
		s.logger.Printf("Ignoring notification %s during shutdown.", method)
		return
	}
	// Allow notifications during initialization (e.g., $/progress)
	// but not before 'initialize' request is processed.
	// `initialized` notification specifically marks the end of init phase.
	if currentState == stateUninitialized {
		s.logger.Printf("Ignoring notification %s before initialization is complete.", method)
		return
	}
	// Special case: 'exit' notification terminates the server.
	if method == "exit" {
		s.handleExit(ctx, nil) // Assume ExitParams is empty or not needed for logic
		return
	}

	s.mu.RLock()
	handler, found := s.handlers[method]
	s.mu.RUnlock()

	if !found {
		// LSP says to ignore notifications for unknown methods
		s.logger.Printf("No handler found for notification method: %s. Ignoring.", method)
		return
	}

	// Invoke the handler, ignore result/error (notifications don't have responses)
	_, err := handler.invoke(ctx, s.conn, n.Params)
	if err != nil {
		// Log handler errors for notifications, but don't send response
		s.logger.Printf("Handler error for notification %s: %v", method, err)
	}
}

// sendResponse marshals and sends a JSON-RPC response.
func (s *Server) sendResponse(ctx context.Context, id json.RawMessage, result interface{}, respErr *jsonrpc2.ErrorObject) {
	response := &jsonrpc2.ResponseMessage{
		JSONRPC: jsonrpc2.Version,
		ID:      id, // Echo back the original request ID
	}

	if respErr != nil {
		response.Error = respErr
	} else {
		// Marshal result only if non-nil and no error
		if result != nil {
			// Ensure result is marshallable
			rawResult, err := json.Marshal(result)
			if err != nil {
				s.logger.Printf("Error marshalling result for ID %s: %v", string(id), err)
				response.Error = jsonrpc2.NewError(jsonrpc2.InternalError, fmt.Sprintf("failed to marshal result: %v", err))
			} else {
				response.Result = rawResult
			}
		} else {
			// If result is nil and no error, LSP expects 'result: null'
			response.Result = json.RawMessage("null")
		}
	}

	if err := s.conn.Write(ctx, response); err != nil {
		s.logger.Printf("Error writing response for ID %s: %v", string(id), err)
	} else {
		s.logger.Printf("Sent response: ID=%s, Error=%v", string(id), response.Error != nil)
	}
}

// --- Standard Handlers ---

func (s *Server) handleInitialize(ctx context.Context, params *protocol.InitializeParams) (*protocol.InitializeResult, error) {
	if !s.state.CompareAndSwap(stateUninitialized, stateInitializing) {
		currentState := s.currentState()
		if currentState == stateInitializing || currentState == stateRunning {
			return nil, jsonrpc2.NewError(jsonrpc2.InvalidRequest, "server already initialized")
		}
		return nil, jsonrpc2.NewError(jsonrpc2.InvalidRequest, "server is shutting down") // Should have been caught earlier
	}
	s.logger.Println("Handling initialize request...")
	s.initParams = params // Store client capabilities etc.

	// --- Server Capabilities ---
	// Define what your server *actually* supports here!
	// This is just a placeholder.
	serverCapabilities := protocol.ServerCapabilities{
		TextDocumentSync: &protocol.TextDocumentSyncOptions{
			OpenClose: true,              // Supports didOpen/didClose
			Change:    protocol.SyncFull, // Supports full document sync on change
			// Save: &protocol.SaveOptions{ IncludeText: true }, // If you handle didSave + want content
		},
		HoverProvider: &protocol.HoverOptions{}, // Basic hover support
		CompletionProvider: &protocol.CompletionOptions{
			TriggerCharacters: []string{"."}, // Example trigger
			// ResolveProvider: true, // If you implement completionItem/resolve
		},
		// DefinitionProvider: &protocol.DefinitionOptions{}, // If you implement goto definition
		// ... Add other capabilities your server implements
	}

	result := &protocol.InitializeResult{
		Capabilities: serverCapabilities,
		ServerInfo: &protocol.ServerInfo{
			Name:    "MyGoLSP",
			Version: "0.0.1",
		},
	}
	s.initResult = result // Store server capabilities etc.

	// DO NOT transition to stateRunning yet. Wait for 'initialized' notification.
	s.logger.Println("Initialize successful, waiting for 'initialized' notification.")
	return result, nil
}

func (s *Server) handleInitialized(ctx context.Context, params *protocol.InitializedParams) error {
	// Received 'initialized' from client. Now we can consider the server fully running.
	if s.state.CompareAndSwap(stateInitializing, stateRunning) {
		s.logger.Println("Server transitioned to running state.")
		// Start any background analysis tasks here if needed
		// s.startBackgroundTasks()
	} else {
		// Log if received in wrong state, but don't error out client
		s.logger.Printf("Received 'initialized' notification in unexpected state: %d", s.currentState())
	}
	// Notifications have no return value
	return nil
}

func (s *Server) handleShutdown(ctx context.Context) error {
	// According to spec, should return nil error immediately,
	// then wait for pending requests before receiving 'exit'.
	s.logger.Println("Handling shutdown request...")

	// Atomically transition to shutdown state only once.
	s.shutdownOnce.Do(func() {
		if s.state.CompareAndSwap(stateRunning, stateShutdown) || s.state.CompareAndSwap(stateInitializing, stateShutdown) || s.state.CompareAndSwap(stateUninitialized, stateShutdown) {
			s.logger.Println("Server transitioning to shutdown state.")
			// In a real server, you might cancel long-running background tasks here.
			// Do NOT close the connection yet. Wait for 'exit'.
		}
	})

	// Wait for all currently processing requests/notifications to complete.
	// New requests/notifications (except 'exit') will be rejected by state check.
	s.logger.Println("Waiting for pending requests to complete...")
	s.pendingReqs.Wait()
	s.logger.Println("All pending requests completed.")

	// Respond to shutdown request *after* pending work is done.
	// Spec is a bit ambiguous here, but responding after wait seems safer.
	// Shutdown itself has no result, only potential error (which we return as nil here).
	return nil
}

func (s *Server) handleExit(ctx context.Context, params *protocol.ExitParams) {
	s.logger.Println("Handling exit notification.")
	// Terminate the process based on whether shutdown was called.
	// If shutdown was called and returned successfully (nil error), exit code 0.
	// Otherwise, exit code 1.
	exitCode := 1
	if s.currentState() == stateShutdown {
		exitCode = 0
	} else {
		s.logger.Println("Exit called without prior shutdown request.")
	}

	s.logger.Printf("Exiting process with code %d.", exitCode)
	// Close connection *before* os.Exit to try and flush buffers if possible
	s.conn.Close()
	os.Exit(exitCode)
}

// Notify sends a notification to the client.
func (s *Server) Notify(ctx context.Context, method string, params interface{}) error {
	if s.currentState() != stateRunning {
		// Maybe allow during initialization too for $/progress?
		return fmt.Errorf("cannot send notification in state %d", s.currentState())
	}

	var rawParams json.RawMessage
	var err error
	if params != nil {
		rawParams, err = json.Marshal(params)
		if err != nil {
			return fmt.Errorf("failed to marshal notification params for %s: %w", method, err)
		}
	}

	notification := &jsonrpc2.NotificationMessage{
		JSONRPC: jsonrpc2.Version,
		Method:  method,
		Params:  rawParams,
	}

	if err := s.conn.Write(ctx, notification); err != nil {
		return fmt.Errorf("failed to write notification %s: %w", method, err)
	}
	s.logger.Printf("Sent notification: Method=%s", method)
	return nil
}
