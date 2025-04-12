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
	"time"

	"github.com/akhenakh/lspgo/jsonrpc2"
	"github.com/akhenakh/lspgo/protocol"
)

// Server represents an LSP server.
type Server struct {
	conn         *jsonrpc2.Conn
	handlers     map[string]*typedHandler // Value is now pointer
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
		handlers: make(map[string]*typedHandler), // Store pointers
		logger:   log.New(os.Stderr, "lsp: ", log.LstdFlags),
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
	// These handlers should match the expected signatures
	s.Register(protocol.MethodInitialize, s.handleInitialize)   // func(ctx, params) (result, error)
	s.Register(protocol.MethodInitialized, s.handleInitialized) // func(ctx, params) error
	s.Register(protocol.MethodShutdown, s.handleShutdown)       // func(ctx) error
	s.Register(protocol.MethodExit, s.handleExit)               // func(ctx)
	s.Register(protocol.MethodCancelRequest, s.handleCancel)    // Example: func(ctx, params)
	s.Register(protocol.MethodProgress, s.handleProgress)       // Example: func(ctx, params)
}

// Register associates a handler function with an LSP method name.
// The handler func must match the expected signature patterns (see handler.go).
func (s *Server) Register(method string, handlerFunc any) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.handlers[method]; exists {
		return fmt.Errorf("handler already registered for method: %s", method)
	}

	// Validate and get metadata about the handler signature
	paramType, takesConn, takesParams, err := validateHandlerFunc(handlerFunc)
	if err != nil {
		return fmt.Errorf("invalid handler for method %s: %w", method, err)
	}

	// Store the handler along with its metadata
	s.handlers[method] = &typedHandler{
		h:           handlerFunc,
		paramType:   paramType,
		takesConn:   takesConn,
		takesParams: takesParams,
	}
	s.logger.Printf("Registered handler for method: %s (takesConn: %v, takesParams: %v, paramType: %v)",
		method, takesConn, takesParams, paramType)
	return nil
}

// Run starts the server's main loop, reading and processing messages.
// It blocks until the connection is closed or the server exits.
func (s *Server) Run(ctx context.Context) error {
	s.logger.Println("Server starting listener loop...")
	defer s.logger.Println("Server listener loop stopped.")

	// Create a done channel to signal when we're exiting
	done := make(chan struct{})
	defer close(done)

	// Set up a goroutine to handle clean context cancellation
	go func() {
		select {
		case <-ctx.Done():
			s.logger.Printf("Context cancelled, initiating shutdown: %v", ctx.Err())
			// Try to close the connection gracefully
			s.conn.Close() //nolint:errcheck
		case <-done:
			// Normal exit through return - no action needed
		}
	}()

	for {
		// Check context before blocking read
		select {
		case <-ctx.Done():
			s.logger.Printf("Context cancelled, exiting run loop: %v", ctx.Err())
			return ctx.Err()
		default:
			// Continue to read message
		}

		// Read one message
		msg, err := s.conn.Read(ctx) // Pass context for cancellation during read
		if err != nil {
			// Determine if the error is fatal or recoverable
			if err == io.EOF || err == io.ErrClosedPipe || err == context.Canceled || err == context.DeadlineExceeded {
				// Expected closure or cancellation
				s.logger.Printf("Connection closed or context cancelled, exiting run loop: %v", err)

				// If we're in shutdown state, this is expected - return nil
				if s.currentState() == stateShutdown {
					return nil
				}

				// Check state: if not shutdown gracefully, maybe log an error?
				s.logger.Println("Client closed connection unexpectedly or context cancelled before shutdown.")
				// Consider specific error types? For now, just return the original error.
				if err == io.EOF {
					return io.ErrUnexpectedEOF // Indicate unclean shutdown
				}
				return err
			}

			// Log other read errors (e.g., JSON parsing errors within Read)
			s.logger.Printf("Error reading message: %v", err)

			// Try to send error response if possible (e.g., if it was a jsonrpc2 format error)
			if jsonErr, ok := err.(*jsonrpc2.ErrorObject); ok {
				// We don't have an ID here. Cannot send a proper response.
				// Log and continue? Or is it fatal? Likely fatal.
				s.logger.Printf("Fatal JSON-RPC format error: %v", jsonErr)
			}
			// For other errors (network, etc.), assume fatal.
			return fmt.Errorf("fatal error reading message: %w", err)
		}

		// Process the message in a separate goroutine for concurrency
		s.pendingReqs.Add(1)
		go func(m any) {
			defer s.pendingReqs.Done()
			// Create a per-message context if needed, inheriting from the main one
			// msgCtx, cancel := context.WithTimeout(ctx, 30*time.Second) // Example timeout
			// defer cancel()
			s.handleMessage(ctx, m) // Pass original context for now
		}(msg)
	}
}

// currentState safely gets the current server state.
func (s *Server) currentState() serverState {
	state, _ := s.state.Load().(serverState)
	return state
}

// handleMessage dispatches incoming messages to appropriate handlers.
func (s *Server) handleMessage(ctx context.Context, msg interface{}) {
	switch m := msg.(type) {
	case *jsonrpc2.RequestMessage:
		s.handleRequest(ctx, m)
	case *jsonrpc2.NotificationMessage:
		s.handleNotification(ctx, m)
	case *jsonrpc2.ResponseMessage:
		// LSP servers typically don't receive responses (they send them)
		// unless they are also acting as a client for some reason.
		s.logger.Printf("Received unexpected Response: ID=%s", string(m.ID))
	default:
		// Should not happen if jsonrpc2.Conn.Read works correctly
		s.logger.Printf("Received unknown message type: %T", msg)
	}
}

// handleRequest handles an incoming request message.
func (s *Server) handleRequest(ctx context.Context, req *jsonrpc2.RequestMessage) {
	method := req.Method
	// Use a shorter log format for less noise
	s.logger.Printf("--> Request: Method=%s, ID=%s", method, string(req.ID))

	// State checks
	currentState := s.currentState()
	if currentState == stateShutdown {
		s.logger.Printf("Rejecting request %s ID=%s during shutdown.", method, string(req.ID))
		errResp := jsonrpc2.NewError(jsonrpc2.InvalidRequest, "server is shutting down")
		s.sendResponse(ctx, req.ID, nil, errResp)
		return
	}
	if currentState == stateUninitialized && method != protocol.MethodInitialize {
		s.logger.Printf("Rejecting request %s ID=%s before initialization.", method, string(req.ID))
		errResp := jsonrpc2.NewError(jsonrpc2.ServerNotInitialized, "server not initialized")
		s.sendResponse(ctx, req.ID, nil, errResp)
		return
	}
	if currentState == stateInitializing && method != protocol.MethodInitialize {
		// Should not happen if initialize is handled synchronously, but check anyway
		s.logger.Printf("Rejecting request %s ID=%s during initialization.", method, string(req.ID))
		errResp := jsonrpc2.NewError(jsonrpc2.ServerNotInitialized, "server is initializing")
		s.sendResponse(ctx, req.ID, nil, errResp)
		return
	}

	s.mu.RLock()
	handler, found := s.handlers[method]
	s.mu.RUnlock()

	if !found {
		s.logger.Printf("No handler found for request method: %s ID=%s", method, string(req.ID))
		errResp := jsonrpc2.NewError(jsonrpc2.MethodNotFound, fmt.Sprintf("method not found: %s", method))
		s.sendResponse(ctx, req.ID, nil, errResp)
		return
	}

	// Invoke the handler - Pass conn and the params RawMessage directly
	// The invoke method now correctly takes *jsonrpc2.Conn and json.RawMessage
	result, err := handler.invoke(ctx, s.conn, req.Params)

	// Send the response
	var errResp *jsonrpc2.ErrorObject
	if err != nil {
		// Check if it's already a jsonrpc2 error
		if jsonErr, ok := err.(*jsonrpc2.ErrorObject); ok {
			errResp = jsonErr
		} else {
			// Wrap other errors as internal server errors
			errResp = jsonrpc2.NewError(jsonrpc2.InternalError, err.Error())
			// Log the Go error details for internal debugging
			s.logger.Printf("Internal handler error for method %s ID=%s: %v", method, string(req.ID), err)
		}
	}

	s.sendResponse(ctx, req.ID, result, errResp)
}

// handleNotification handles an incoming notification message.
func (s *Server) handleNotification(ctx context.Context, n *jsonrpc2.NotificationMessage) {
	method := n.Method
	// Log notification methods that are common and less noisy only at debug level later?
	// For now, log all.
	s.logger.Printf("--> Notification: Method=%s", method)

	// State checks
	currentState := s.currentState()
	// Allow 'exit' even during shutdown
	if currentState == stateShutdown && method != protocol.MethodExit {
		s.logger.Printf("Ignoring notification %s during shutdown.", method)
		return
	}

	// Allow '$/cancelRequest' and '$/progress' even before 'initialized'
	isEarlyNotification := method == protocol.MethodCancelRequest || method == protocol.MethodProgress
	if currentState == stateUninitialized && !isEarlyNotification {
		s.logger.Printf("Ignoring notification %s before initialization.", method)
		return
	}

	// Special case: 'exit' notification terminates the server.
	// The handler itself calls os.Exit, no further processing here.
	if method == protocol.MethodExit {
		s.mu.RLock()
		handler, found := s.handlers[method]
		s.mu.RUnlock()
		if found {
			// Invoke exit handler directly - it doesn't return
			// It expects context, no params. Pass nil conn as exit shouldn't write.
			_, err := handler.invoke(ctx, nil, nil)
			if err != nil {
				s.logger.Printf("Error in exit handler: %v", err)
				// No need to return since we're exiting anyway
			}
			// The invoke will call the registered s.handleExit
		} else {
			s.logger.Println("No handler registered for exit, performing default exit(1)")
			s.conn.Close() // Try to close connection first
			os.Exit(1)     // Default exit if no handler was registered somehow
		}
		return // Exit handler terminates, don't continue
	}

	s.mu.RLock()
	handler, found := s.handlers[method]
	s.mu.RUnlock()

	if !found {
		// LSP spec: "Notifications unknown to the server are ignored."
		s.logger.Printf("No handler found for notification method: %s. Ignoring.", method)
		return
	}

	// Invoke the handler, ignore result/error (notifications don't have responses)
	// The invoke method now correctly takes *jsonrpc2.Conn and json.RawMessage
	_, err := handler.invoke(ctx, s.conn, n.Params)
	if err != nil {
		// Log handler errors for notifications, but don't send response
		s.logger.Printf("Handler error processing notification %s: %v", method, err)
	}
}

// sendResponse marshals and sends a JSON-RPC response.
func (s *Server) sendResponse(ctx context.Context, id json.RawMessage, result interface{}, respErr *jsonrpc2.ErrorObject) {
	// Ensure ID is valid before proceeding
	if len(id) == 0 || string(id) == "null" {
		s.logger.Printf("Attempted to send response for notification or invalid request ID. Ignoring.")
		return
	}

	response := &jsonrpc2.ResponseMessage{
		JSONRPC: jsonrpc2.Version,
		ID:      id, // Echo back the original request ID
	}

	// Set error if present
	if respErr != nil {
		response.Error = respErr
		// Don't log the full error here if it was already logged by the caller
	} else if result != nil {
		// Marshal result if non-nil and no error
		rawResult, err := json.Marshal(result)
		if err != nil {
			s.logger.Printf("Error marshalling result for ID %s: %v. Sending InternalError instead.", string(id), err)
			response.Error = jsonrpc2.NewError(jsonrpc2.InternalError, fmt.Sprintf("failed to marshal result: %v", err))
		} else {
			response.Result = rawResult
		}
	} else {
		// If result is nil and no error, LSP expects 'result: null'
		response.Result = json.RawMessage("null")
	}

	// Prepare log message
	logMsg := fmt.Sprintf("<-- Response: ID=%s", string(id))
	if response.Error != nil {
		logMsg += fmt.Sprintf(", Error=%d", response.Error.Code)
	} else {
		logMsg += ", Result=OK"
	}
	s.logger.Print(logMsg)

	// Send the response
	if err := s.conn.Write(ctx, response); err != nil {
		s.logger.Printf("Error writing response for ID %s: %v", string(id), err)
	}
}

// --- Standard Handlers ---

// handleInitialize: func(ctx context.Context, params *protocol.InitializeParams) (*protocol.InitializeResult, error)
func (s *Server) handleInitialize(ctx context.Context, params *protocol.InitializeParams) (*protocol.InitializeResult, error) {
	if !s.state.CompareAndSwap(stateUninitialized, stateInitializing) {
		currentState := s.currentState()
		errMsg := "server already initialized or is shutting down"
		s.logger.Printf("Initialize failed: %s (current state: %d)", errMsg, currentState)
		return nil, jsonrpc2.NewError(jsonrpc2.InvalidRequest, errMsg)
	}
	s.logger.Println("Handling initialize request...")
	s.initParams = params // Store client capabilities etc.

	// Log client info if available
	if params.ClientInfo != nil {
		s.logger.Printf("Client: %s %s", params.ClientInfo.Name, params.ClientInfo.Version)
	}

	// --- Server Capabilities ---
	// Determine capabilities based on registered handlers AND specific configurations.
	// This should ideally inspect the `s.handlers` map.
	serverCapabilities := s.determineServerCapabilities() // Extract to helper method

	result := &protocol.InitializeResult{
		Capabilities: serverCapabilities,
		ServerInfo: &protocol.ServerInfo{
			Name:    "Ollama-LSP-Go", // Specific name
			Version: "0.1.0",         // Example version
		},
	}
	s.initResult = result // Store server capabilities etc.

	// DO NOT transition to stateRunning yet. Wait for 'initialized' notification.
	s.logger.Println("Initialize successful, sending capabilities and waiting for 'initialized' notification.")
	return result, nil
}

// determineServerCapabilities inspects registered handlers to build the capabilities struct.
func (s *Server) determineServerCapabilities() protocol.ServerCapabilities {
	s.mu.RLock()
	defer s.mu.RUnlock()

	caps := protocol.ServerCapabilities{}

	// Text Document Sync: Check for didOpen, didChange, didClose handlers
	// Assuming full sync if didChange is registered. Needs more nuance for incremental.
	_, hasOpen := s.handlers[protocol.MethodTextDocumentDidOpen]
	_, hasChange := s.handlers[protocol.MethodTextDocumentDidChange]
	_, hasClose := s.handlers[protocol.MethodTextDocumentDidClose]
	_, hasSave := s.handlers[protocol.MethodTextDocumentDidSave] // Add if implementing save

	if hasOpen || hasChange || hasClose || hasSave {
		// Default to Full sync if Change is handled. This might need configuration.
		syncKind := protocol.SyncFull
		// TODO: Add config option or check handler signature for incremental support?
		caps.TextDocumentSync = &protocol.TextDocumentSyncOptions{
			OpenClose: hasOpen || hasClose,
			Change:    syncKind,
			// WillSave: ..., WillSaveWaitUntil: ..., Save: ... // Add based on registered handlers
		}
		// If textDocument/didSave is handled, advertise Save capability
		if hasSave {
			// Simple true for now, could be SaveOptions
			// caps.TextDocumentSync.Save = true // This field doesn't exist, need SaveOptions
			caps.TextDocumentSync.Save = &protocol.SaveOptions{IncludeText: false} // Or true if needed
		}
	}

	// Hover: Check for textDocument/hover
	if _, ok := s.handlers[protocol.MethodTextDocumentHover]; ok {
		// Can be bool or HoverOptions. Use options for potential future config.
		caps.HoverProvider = &protocol.HoverOptions{}
	}

	// Completion: Check for textDocument/completion
	if _, ok := s.handlers[protocol.MethodTextDocumentCompletion]; ok {
		// Add CompletionOptions based on implementation details
		caps.CompletionProvider = &protocol.CompletionOptions{
			// ResolveProvider: _, // Set if completionItem/resolve is handled
			// TriggerCharacters: []string{"."}, // Example
		}
		// Check for completionItem/resolve handler
		if _, okResolve := s.handlers[protocol.MethodCompletionItemResolve]; okResolve {
			caps.CompletionProvider.ResolveProvider = true
		}
	}

	// Definition: Check for textDocument/definition
	if _, ok := s.handlers[protocol.MethodTextDocumentDefinition]; ok {
		caps.DefinitionProvider = &protocol.DefinitionOptions{} // Can be bool or options
	}

	// Code Action: Check for textDocument/codeAction
	if _, ok := s.handlers[protocol.MethodTextDocumentCodeAction]; ok {
		// Advertise CodeActionOptions. Can be bool or options.
		opts := &protocol.CodeActionOptions{
			// Define specific kinds supported, if known
			// CodeActionKinds: []protocol.CodeActionKind{
			// 	protocol.QuickFix,
			// 	protocol.RefactorInline,
			// },
		}
		// Check if codeAction/resolve is implemented
		if _, okResolve := s.handlers[protocol.MethodCodeActionResolve]; okResolve {
			opts.ResolveProvider = true
		}
		caps.CodeActionProvider = opts
	}

	// Execute Command: Check for workspace/executeCommand
	if _, ok := s.handlers[protocol.MethodWorkspaceExecuteCommand]; ok {
		// Need to list the *commands* the server supports. This requires
		// knowing the command IDs used in handleExecuteCommand.
		// This info isn't easily available just from registration map keys.
		// The server implementation needs to provide this list.
		// For now, advertise basic support. A better way is needed.
		caps.ExecuteCommandProvider = &protocol.ExecuteCommandOptions{
			Commands: []string{
				// TODO: Dynamically discover or explicitly list commands
				"ollama/executeAction", // Hardcoding from main.go for now
			},
		}
	}

	// Add other capabilities based on registered handlers...
	// e.g., formatting, references, rename, diagnostics (pull model), etc.

	s.logger.Printf("Determined Server Capabilities: %+v", caps) // Log determined caps
	return caps
}

// handleInitialized: func(ctx context.Context, params *protocol.InitializedParams) error
// Note: LSP spec says params can be null. Our generator made it a struct.
// Let's accept json.RawMessage and ignore content, or use a pointer *protocol.InitializedParams
// Changing the handler signature to use pointer.
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
	// Notifications have no return value / error should be nil if handled
	return nil
}

// handleShutdown: func(ctx context.Context) error
func (s *Server) handleShutdown(ctx context.Context) error {
	s.logger.Println("Handling shutdown request...")

	// Mark state as shutting down atomically and only once.
	s.shutdownOnce.Do(func() {
		// Attempt to transition from any valid pre-shutdown state
		if s.state.CompareAndSwap(stateRunning, stateShutdown) ||
			s.state.CompareAndSwap(stateInitializing, stateShutdown) ||
			s.state.CompareAndSwap(stateUninitialized, stateShutdown) {
			s.logger.Println("Server transitioning to shutdown state.")
			// Cancel any long-running background tasks here using a cancel func derived from main context
		} else {
			s.logger.Printf("Shutdown requested but already in state: %d", s.currentState())
		}
	})

	// Respond nil error *immediately* after setting state, as required by LSP spec.
	// The actual waiting happens before exit.
	return nil
}

// handleExit: func(ctx context.Context)
func (s *Server) handleExit(ctx context.Context) {
	s.logger.Println("Handling exit notification.")

	// Determine the state *before* waiting, as this decides the exit code.
	currentStateBeforeWait := s.currentState()
	exitCode := 1 // Default to 1 (error/unexpected exit)
	if currentStateBeforeWait == stateShutdown {
		exitCode = 0 // Graceful shutdown path was followed
		s.logger.Println("Shutdown completed, waiting for final pending tasks before clean exit.")
	} else {
		s.logger.Println("Exit called without prior successful shutdown. Waiting briefly for pending tasks before error exit.")
	}

	// Wait for any remaining pending requests (that were started before shutdown completed)
	// Use a reasonable timeout to prevent hanging indefinitely.
	waitCh := make(chan struct{})
	go func() {
		s.pendingReqs.Wait() // Wait for counter to reach zero
		close(waitCh)
	}()

	select {
	case <-waitCh:
		s.logger.Println("All pending tasks completed before exit.")
	case <-time.After(2 * time.Second): // Shorter timeout, exit should be quick
		s.logger.Println("Timed out waiting for pending tasks during exit - proceeding with exit anyway")
	}

	// Close connection before exiting
	s.logger.Printf("Closing connection and terminating process with code %d.", exitCode)
	if err := s.conn.Close(); err != nil {
		// Log error but proceed with exit
		s.logger.Printf("Error closing connection during exit: %v", err)
	}

	// Force exit. Using AfterFunc can be unreliable if the main goroutine exits first.
	os.Exit(exitCode)
}

// handleCancel handles "$/cancelRequest" notifications.
// func(ctx context.Context, params *protocol.CancelParams)
// Note: protocol.CancelParams is just `{ id: number | string }`
// Need to add CancelParams to protocol package. For now, use RawMessage.
func (s *Server) handleCancel(ctx context.Context, params *json.RawMessage) {
	// TODO: Implement request cancellation logic.
	// This requires tracking ongoing requests and having a way to signal cancellation.
	// For now, just log it.
	var cancelParams struct {
		ID json.RawMessage `json:"id"`
	}
	if params != nil {
		if err := json.Unmarshal(*params, &cancelParams); err == nil {
			s.logger.Printf("Received cancellation request for ID: %s (Cancellation not implemented)", string(cancelParams.ID))
		} else {
			s.logger.Printf("Received malformed cancellation request: %v", err)
		}
	} else {
		s.logger.Printf("Received cancellation request with nil params")
	}
}

// handleProgress handles "$/progress" notifications.
// func(ctx context.Context, params *protocol.ProgressParams)
// Note: protocol.ProgressParams is `{ token: number | string; value: any; }`
// Need to add ProgressParams to protocol package. For now, use RawMessage.
func (s *Server) handleProgress(ctx context.Context, params *json.RawMessage) {
	// TODO: Handle progress updates from the client if the server initiated progress reporting.
	// For now, just log it.
	if params == nil {
		s.logger.Printf("Received progress notification with nil params")
		return
	}

	var progressParams struct {
		Token json.RawMessage `json:"token"`
		Value json.RawMessage `json:"value"`
	}

	if err := json.Unmarshal(*params, &progressParams); err != nil {
		s.logger.Printf("Received malformed progress notification: %v", err)
		return
	}

	s.logger.Printf("Received progress notification for Token: %s Value: %s (Progress handling not implemented)",
		string(progressParams.Token), string(progressParams.Value))
}

// Notify sends a notification to the client.
func (s *Server) Notify(ctx context.Context, method string, params interface{}) error {
	currentState := s.currentState()
	if currentState != stateRunning {
		// Allow some notifications during initialization? e.g., $/progress for server init tasks
		// Maybe allow stateInitializing as well?
		// For now, restrict to stateRunning for simplicity.
		s.logger.Printf("Attempted to send notification %s in wrong state: %d. Ignoring.", method, currentState)
		// Return nil because caller likely doesn't need to crash, but log the issue.
		// Or return an error? Let's return an error.
		return fmt.Errorf("cannot send notification %s while server state is %d", method, currentState)
	}

	var rawParams json.RawMessage
	var err error
	if params != nil {
		rawParams, err = json.Marshal(params)
		if err != nil {
			return fmt.Errorf("failed to marshal notification params for %s: %w", method, err)
		}
	} // If params is nil, rawParams remains nil, which is fine for JSON encoding

	notification := &jsonrpc2.NotificationMessage{
		JSONRPC: jsonrpc2.Version,
		Method:  method,
		Params:  rawParams, // Send null if params was nil or marshalled to null
	}

	// Log before sending
	s.logger.Printf("<-- Notification: Method=%s", method)

	if err := s.conn.Write(ctx, notification); err != nil {
		// Log write errors
		s.logger.Printf("Error writing notification %s: %v", method, err)
		return fmt.Errorf("failed to write notification %s: %w", method, err)
	}

	return nil
}
