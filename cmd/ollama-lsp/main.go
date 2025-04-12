package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/akhenakh/lspgo/jsonrpc2"
	"github.com/akhenakh/lspgo/protocol"
	"github.com/akhenakh/lspgo/server"
)

var (
	ollamaBaseURL = getEnv("OLLAMA_BASE_URL", "http://localhost:11434")
	ollamaModel   = getEnv("OLLAMA_MODEL", "qwen2.5-coder:latest") // Make sure this model is pulled in Ollama
	ollamaTimeout = 30 * time.Second
)

func getEnv(key, fallback string) string {
	if value, ok := os.LookupEnv(key); ok {
		return value
	}
	return fallback
}

// --- Document Store ---

var (
	documents     = make(map[protocol.DocumentURI]protocol.TextDocumentItem)
	nextRequestID atomic.Int64 // Counter for outgoing request IDs
	docMu         sync.RWMutex
)

// --- Main Application ---

func main() {
	ctx := context.Background()
	// Example: Configure logger format
	logger := log.New(os.Stderr, "[ollama-lsp] ", log.LstdFlags|log.Lshortfile)

	lspServer := server.NewServer(server.WithLogger(logger))

	// Register handlers
	mustRegister(lspServer, "textDocument/didOpen", handleDidOpen)
	mustRegister(lspServer, "textDocument/didChange", handleDidChange)
	mustRegister(lspServer, "textDocument/didClose", handleDidClose) // Good practice
	mustRegister(lspServer, "textDocument/codeAction", handleCodeAction)
	mustRegister(lspServer, "workspace/executeCommand", handleExecuteCommand)

	// (Optional) Override default server capabilities in initialize if needed
	// Note: The default handlers in `server.go` already set basic capabilities.
	// We rely on the fact that the default `initialize` handler will be called
	// and we registered `handleExecuteCommand`, so the server should automatically
	// advertise the `executeCommandProvider` capability if the `lspgo` library supports that inference.
	// *Explicitly setting capabilities during initialize is generally more robust.*
	// We will modify the initialize handler later if needed, but let's try without first.
	// The `lspgo/server` needs adjustment to properly advertise commands registered later.
	// For now, let's assume the client might still send the command execution request.

	log.Printf("Starting Ollama LSP server...")
	log.Printf("Using Ollama URL: %s, Model: %s", ollamaBaseURL, ollamaModel)

	if err := lspServer.Run(ctx); err != nil {
		logger.Fatalf("Server error: %v", err)
	}
	logger.Println("Server stopped.")
}

func mustRegister(s *server.Server, method string, handler interface{}) {
	if err := s.Register(method, handler); err != nil {
		log.Fatalf("Failed to register handler for %s: %v", method, err)
	}
}

// Function to get next request ID
func getNextRequestID() json.RawMessage {
	id := nextRequestID.Add(1)
	// JSON-RPC IDs can be numbers or strings. Let's use strings for safety.
	return json.RawMessage(strconv.FormatInt(id, 10))
}

// --- LSP Handlers ---

func handleDidOpen(ctx context.Context, params *protocol.DidOpenTextDocumentParams) error {
	docMu.Lock()
	// Store the item itself, which includes URI, text, and version
	documents[params.TextDocument.URI] = params.TextDocument
	docMu.Unlock()
	log.Printf("Document Opened: %s (Version %d)", params.TextDocument.URI, params.TextDocument.Version)
	// sendDummyDiagnostics(ctx, conn, params.TextDocument.URI, params.TextDocument.Version) // Pass version if needed
	return nil
}

func handleDidChange(ctx context.Context, params *protocol.DidChangeTextDocumentParams) error {
	if len(params.ContentChanges) == 0 {
		return nil
	}
	fullText := params.ContentChanges[0].Text

	docMu.Lock()
	// Update text and version
	// It's safer to update the existing item if present, or create if missing (though didOpen should handle creation)
	item, ok := documents[params.TextDocument.URI]
	if !ok {
		// Should ideally not happen if didOpen was received, but handle defensively
		item = protocol.TextDocumentItem{
			URI:     params.TextDocument.URI,
			Version: params.TextDocument.Version, // Use the version from the change event
			Text:    fullText,
			// LanguageID might be missing here if we create it anew
		}
		log.Printf("Document Changed: %s (Version %d) - Created new entry", params.TextDocument.URI, params.TextDocument.Version)
	} else {
		item.Version = params.TextDocument.Version // Update version
		item.Text = fullText                       // Update text
		log.Printf("Document Changed: %s (Version %d) - Updated existing", params.TextDocument.URI, params.TextDocument.Version)
	}
	documents[params.TextDocument.URI] = item
	docMu.Unlock()

	// sendDummyDiagnostics(ctx, conn, params.TextDocument.URI, params.TextDocument.Version) // Pass version if needed
	return nil
}

func handleDidClose(ctx context.Context, params *protocol.DidCloseTextDocumentParams) error {
	docMu.Lock()
	delete(documents, params.TextDocument.URI)
	docMu.Unlock()
	log.Printf("Document Closed: %s", params.TextDocument.URI)
	return nil
}

// handleCodeAction provides actions based on cursor position or selection.
// Note: This handler needs access to `conn` to send notifications later if needed,
// but the core logic here just returns actions. The `lspgo` handler reflection
// supports including `conn` as the second argument.
// handleCodeAction function modification - Add a third action
func handleCodeAction(ctx context.Context, conn *jsonrpc2.Conn, params *protocol.CodeActionParams) ([]protocol.CodeAction, error) {
	uri := params.TextDocument.URI
	log.Printf("Code Action Request: %s Range: %v", uri, params.Range)

	docMu.RLock()
	_, ok := documents[uri]
	docMu.RUnlock()
	if !ok {
		log.Printf("Code Action: Document not found %s", uri)
		return nil, nil // No actions if document isn't open/tracked
	}

	var actions []protocol.CodeAction

	// --- Action 1: Continue --- (unchanged)
	continueArgs := OllamaActionArgs{
		Action:   "continue",
		URI:      uri,
		Position: params.Range.Start,
	}
	continueCmdArgs, _ := json.Marshal(continueArgs)

	actions = append(actions, protocol.CodeAction{
		Title: "Ollama: Continue...",
		Kind:  protocol.RefactorInline,
		Command: &protocol.Command{
			Title:     "Ollama: Continue...",
			Command:   "ollama/executeAction",
			Arguments: []json.RawMessage{continueCmdArgs},
		},
	})

	// --- Action 2: Explain Selection ---
	if params.Range.Start != params.Range.End {
		explainArgs := OllamaActionArgs{
			Action: "explain",
			URI:    uri,
			Range:  &params.Range,
		}
		explainCmdArgs, _ := json.Marshal(explainArgs)

		actions = append(actions, protocol.CodeAction{
			Title: "Ollama: Explain selection...",
			Kind:  protocol.RefactorInline,
			Command: &protocol.Command{
				Title:     "Ollama: Explain selection...",
				Command:   "ollama/executeAction",
				Arguments: []json.RawMessage{explainCmdArgs},
			},
		})
	}

	// --- Action 3: Prompt (Current Line) ---
	promptArgs := OllamaActionArgs{
		Action:   "prompt",
		URI:      uri,
		Position: params.Range.Start,
	}
	promptCmdArgs, _ := json.Marshal(promptArgs)

	actions = append(actions, protocol.CodeAction{
		Title: "Ollama: Use current line as prompt...",
		Kind:  protocol.Source, // Different kind for this action
		Command: &protocol.Command{
			Title:     "Ollama: Use current line as prompt...",
			Command:   "ollama/executeAction",
			Arguments: []json.RawMessage{promptCmdArgs},
		},
	})

	log.Printf("Offering %d code actions for %s", len(actions), uri)
	return actions, nil
}

// OllamaActionArgs defines the structure for arguments passed to our custom command
type OllamaActionArgs struct {
	Action   string               `json:"action"` // "continue" or "explain"
	URI      protocol.DocumentURI `json:"uri"`
	Position protocol.Position    `json:"position,omitempty"` // Used for "continue"
	Range    *protocol.Range      `json:"range,omitempty"`    // Used for "explain"
}

// handleExecuteCommand runs the Ollama logic based on the chosen Code Action.
func handleExecuteCommand(ctx context.Context, conn *jsonrpc2.Conn, params *protocol.ExecuteCommandParams) (interface{}, error) {
	log.Printf("Execute Command Request: %s with %d args", params.Command, len(params.Arguments))

	if params.Command != "ollama/executeAction" {
		return nil, fmt.Errorf("unknown command: %s", params.Command)
	}

	if len(params.Arguments) != 1 {
		return nil, fmt.Errorf("expected 1 argument for command %s, got %d", params.Command, len(params.Arguments))
	}

	var args OllamaActionArgs
	if err := json.Unmarshal(params.Arguments[0], &args); err != nil {
		return nil, fmt.Errorf("failed to unmarshal command arguments: %w", err)
	}

	log.Printf("Executing action '%s' for %s", args.Action, args.URI)

	// Get document item (includes content and version)
	docMu.RLock()
	docItem, ok := documents[args.URI]
	docMu.RUnlock()
	if !ok {
		errMsg := fmt.Sprintf("Document %s not found for command %s", args.URI, params.Command)
		log.Println(errMsg)
		showNotification(ctx, conn, protocol.Error, errMsg) // Inform user via message still okay for error
		return nil, fmt.Errorf(errMsg)                      // Internal error for server log
	}
	content := docItem.Text       // Extract content for prompt generation
	docVersion := docItem.Version // Extract version for edit application

	// Show "Thinking..." message (still useful)
	showNotification(ctx, conn, protocol.Info, fmt.Sprintf("Ollama (%s) is thinking...", args.Action))

	var prompt string
	var err error

	switch args.Action {
	case "continue":
		textBeforeCursor := getTextBeforePosition(content, args.Position)
		// Improve prompt slightly for better code continuation
		prompt = fmt.Sprintf(`You are an expert coding assistant. Continue the following code snippet directly without any preamble or explanation.
Respond ONLY with the code that should come next.

Code Snippet:
%s`, textBeforeCursor) // Use the improved prompt

	case "explain":
		if args.Range == nil {
			return nil, fmt.Errorf("range is required for 'explain' action")
		}
		selectedText, err := getTextInRange(content, *args.Range)
		if err != nil {
			return nil, fmt.Errorf("failed to get text in range for 'explain': %w", err)
		}
		if selectedText == "" {
			showNotification(ctx, conn, protocol.Warning, "No text selected for 'explain'.")
			return nil, nil // Command succeeded, nothing to do.
		}
		// Improve prompt slightly
		prompt = fmt.Sprintf(`You are an expert coding assistant. Explain the following code or text clearly and concisely.

Code/Text Snippet:
%s

Explanation:`, selectedText)

	case "prompt":
		// Get current line text
		lineNum := args.Position.Line

		currentLine, err := getCurrentLine(content, lineNum)
		if err != nil {
			errMsg := fmt.Sprintf("Failed to get current line: %v", err)
			log.Println(errMsg)
			showNotification(ctx, conn, protocol.Error, errMsg)
			return nil, nil
		}

		currentLine = strings.TrimSpace(currentLine) // Trim whitespace for cleaner instructions

		// Get text before the current line
		textBeforeCursor := getTextBeforePosition(content, protocol.Position{
			Line:      lineNum,
			Character: 0,
		})
		// Remove potential trailing newline from textBeforeCursor for cleaner prompt formatting
		textBeforeCursor = strings.TrimSuffix(textBeforeCursor, "\n")

		if currentLine == "" { // Check after trimming
			showNotification(ctx, conn, protocol.Warning, "Current line is empty. Please type a prompt/instruction first.")
			return nil, nil
		}

		prompt = fmt.Sprintf(`You are an expert coding assistant. Continue the following code snippet directly without any preamble or explanation.
Respond ONLY with the code that should come next. %s.

Code Snippet:
%s`, currentLine, textBeforeCursor) // Use the improved prompt

		// Show "Thinking..." message
		showNotification(ctx, conn, protocol.Info, fmt.Sprintf("Ollama processing prompt: %s",
			currentLine[:min(30, len(currentLine))]+strings.Repeat(".", min(3, 30-min(30, len(currentLine))))))

	default:
		return nil, fmt.Errorf("unknown action '%s' in command arguments", args.Action)
	}

	// Call Ollama API
	ollamaResult, err := callOllama(ctx, prompt)
	if err != nil {
		errMsg := fmt.Sprintf("Ollama request failed: %v", err)
		log.Println(errMsg)
		showNotification(ctx, conn, protocol.Error, errMsg)
		return nil, nil // Command successful, underlying task failed
	}

	log.Printf("Ollama response received for action '%s'", args.Action)

	// --- Apply Result Based on Action ---
	switch args.Action {
	case "continue":
		// Apply the result as a workspace edit
		err = applyOllamaContinuation(ctx, conn, args.URI, docVersion, args.Position, ollamaResult)
		if err != nil {
			// Log error, maybe notify user
			log.Printf("Error applying Ollama continuation edit: %v", err)
			showNotification(ctx, conn, protocol.Error, fmt.Sprintf("Failed to apply edit: %v", err))
		} else {
			log.Printf("Successfully requested 'workspace/applyEdit' for continuation")
			// Maybe show a brief success notification? Optional.
			showNotification(ctx, conn, protocol.Info, "Ollama continuation applied.")
		}

	case "explain":
		// *** For explain, still show the result in a message window ***
		messageToShow := fmt.Sprintf("Ollama Explanation:\n---\n%s\n---", ollamaResult) // Add separators
		showNotification(ctx, conn, protocol.Info, messageToShow)

	case "prompt":
		lineNum := args.Position.Line
		// Apply the result as a workspace edit, replacing the current line
		// We need the original line text with original indentation/whitespace for replacement range calculation.
		originalLine, err := getCurrentLine(content, lineNum) // Get the original line again
		if err != nil {
			// This shouldn't happen if the first call succeeded, but handle defensively
			errMsg := fmt.Sprintf("Failed to get original current line for replacement: %v", err)
			log.Println(errMsg)
			showNotification(ctx, conn, protocol.Error, errMsg)
			return nil, nil
		}

		err = applyOllamaLineReplacement(ctx, conn, args.URI, docVersion, lineNum, originalLine, ollamaResult) // Use originalLine here
		if err != nil {
			// Log error, notify user
			log.Printf("Error applying Ollama line replacement: %v", err)
			showNotification(ctx, conn, protocol.Error, fmt.Sprintf("Failed to apply edit: %v", err))
		} else {
			log.Printf("Successfully requested 'workspace/applyEdit' for line replacement")
			showNotification(ctx, conn, protocol.Info, "Ollama prompt result applied.")
		}

	default:
		return nil, fmt.Errorf("unknown action '%s' in command arguments", args.Action)
	}

	// workspace/executeCommand usually returns null or a simple success indicator,
	// not the result of the action itself (which was handled above).
	return nil, nil
}

// applyOllamaContinuation sends a workspace/applyEdit request to insert the text.
func applyOllamaContinuation(ctx context.Context, conn *jsonrpc2.Conn, uri protocol.DocumentURI, version int, position protocol.Position, textToInsert string) error {
	// Clean up the result - Ollama might add backticks or language hints
	textToInsert = cleanOllamaCodeResult(textToInsert)
	if textToInsert == "" {
		log.Println("Ollama returned empty result after cleaning, not applying edit.")
		return nil
	}

	// 1. Create the TextEdit
	edit := protocol.TextEdit{
		Range: protocol.Range{ // Zero-length range at the cursor for insertion
			Start: position,
			End:   position,
		},
		NewText: textToInsert,
	}

	// 2. Create the TextDocumentEdit (requires version)
	docEdit := protocol.TextDocumentEdit{
		TextDocument: protocol.VersionedTextDocumentIdentifier{
			TextDocumentIdentifier: protocol.TextDocumentIdentifier{URI: uri},
			Version:                version, // Use the tracked version
		},
		Edits: []protocol.TextEdit{edit},
	}

	// 3. Create the WorkspaceEdit
	workspaceEdit := protocol.WorkspaceEdit{
		// Use DocumentChanges (preferred) - assumes client supports it
		DocumentChanges: []protocol.TextDocumentEdit{docEdit},
		// Alternatively, use Changes (older clients):
		// Changes: map[protocol.DocumentURI][]protocol.TextEdit{
		// 	uri: {edit},
		// },
	}

	// 4. Create ApplyWorkspaceEditParams
	applyParams := protocol.ApplyWorkspaceEditParams{
		Label: "Ollama Continuation", // Undo label
		Edit:  workspaceEdit,
	}

	// 5. Marshal Params
	rawParams, err := json.Marshal(applyParams)
	if err != nil {
		return fmt.Errorf("failed to marshal applyEdit params: %w", err)
	}

	// 6. Create Request Message
	request := &jsonrpc2.RequestMessage{
		JSONRPC: jsonrpc2.Version,
		ID:      getNextRequestID(), // Generate a unique ID for the request
		Method:  protocol.MethodWorkspaceApplyEdit,
		Params:  rawParams,
	}

	// 7. Send the request TO THE CLIENT
	log.Printf("<-- Request (to client): Method=%s, ID=%s", request.Method, string(request.ID))
	if err := conn.Write(ctx, request); err != nil {
		return fmt.Errorf("failed to send workspace/applyEdit request: %w", err)
	}

	// Note: We are *not* waiting for the client's response here for simplicity.
	// A robust implementation might track the request ID and handle the
	// ApplyWorkspaceEditResponse when it arrives in the server's read loop.

	return nil
}

// cleanOllamaCodeResult removes common markdown artifacts from Ollama's code output.
func cleanOllamaCodeResult(rawResult string) string {
	trimmed := strings.TrimSpace(rawResult)
	// Remove ```<lang>\n prefix and ``` suffix
	if strings.HasPrefix(trimmed, "```") {
		lines := strings.SplitN(trimmed, "\n", 2)
		if len(lines) == 2 {
			trimmed = lines[1] // Skip the first line (```lang)
		}
	}
	if strings.HasSuffix(trimmed, "```") {
		trimmed = strings.TrimSuffix(trimmed, "```")
	}
	return strings.TrimSpace(trimmed) // Trim again after removing fences
}

type ollamaRequest struct {
	Model  string `json:"model"`
	Prompt string `json:"prompt"`
	Stream bool   `json:"stream"` // Keep false for simple request/response
}

type ollamaResponse struct {
	Response string `json:"response"`
	Done     bool   `json:"done"`
	// Add other fields if needed, e.g., context for follow-up
}

func callOllama(ctx context.Context, prompt string) (string, error) {
	apiURL := ollamaBaseURL + "/api/generate" // Standard Ollama API endpoint

	requestPayload := ollamaRequest{
		Model:  ollamaModel,
		Prompt: prompt,
		Stream: false,
	}

	jsonData, err := json.Marshal(requestPayload)
	if err != nil {
		return "", fmt.Errorf("failed to marshal ollama request: %w", err)
	}

	reqCtx, cancel := context.WithTimeout(ctx, ollamaTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, "POST", apiURL, bytes.NewBuffer(jsonData))
	if err != nil {
		return "", fmt.Errorf("failed to create ollama request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	log.Printf("Sending request to Ollama API: %s (Model: %s)", apiURL, ollamaModel)
	// log.Printf("Prompt snippet: %s...", prompt[:min(80, len(prompt))]) // Log prompt start

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("ollama request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("ollama request failed with status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	var ollamaResp ollamaResponse
	if err := json.NewDecoder(resp.Body).Decode(&ollamaResp); err != nil {
		return "", fmt.Errorf("failed to decode ollama response: %w", err)
	}

	if !ollamaResp.Done {
		log.Printf("Warning: Ollama response 'done' field is false.")
	}

	return strings.TrimSpace(ollamaResp.Response), nil
}

// --- Text Manipulation Helpers ---

// getTextBeforePosition extracts text from the start of the document up to the given position.
func getTextBeforePosition(content string, pos protocol.Position) string {
	lines := strings.Split(content, "\n")
	if int(pos.Line) >= len(lines) {
		// Position is out of bounds (e.g., end of file), return everything
		return content
	}

	var builder strings.Builder
	for i := 0; i < int(pos.Line); i++ {
		builder.WriteString(lines[i])
		builder.WriteString("\n")
	}

	// Add the part of the target line before the character position
	lineContent := lines[pos.Line]
	if int(pos.Character) > len(lineContent) {
		// Position is beyond the end of the line
		builder.WriteString(lineContent)
	} else {
		builder.WriteString(lineContent[:pos.Character])
	}

	return builder.String()
}

// getTextInRange extracts text within the specified range.
// Returns an error if the range is invalid.
func getTextInRange(content string, rng protocol.Range) (string, error) {
	lines := strings.Split(content, "\n") // Simple split, might not handle CRLF perfectly everywhere
	startLine, startChar := int(rng.Start.Line), int(rng.Start.Character)
	endLine, endChar := int(rng.End.Line), int(rng.End.Character)

	if startLine > endLine || (startLine == endLine && startChar > endChar) {
		return "", fmt.Errorf("invalid range: start %v is after end %v", rng.Start, rng.End)
	}
	if startLine < 0 || endLine >= len(lines) {
		return "", fmt.Errorf("invalid range: line numbers %d-%d out of bounds (0-%d)", startLine, endLine, len(lines)-1)
	}

	var builder strings.Builder

	for i := startLine; i <= endLine; i++ {
		lineContent := lines[i]
		lineStartChar := 0
		lineEndChar := len(lineContent)

		if i == startLine {
			if startChar > len(lineContent) {
				return "", fmt.Errorf("invalid range: start character %d out of bounds on line %d (len %d)", startChar, i, len(lineContent))
			}
			lineStartChar = startChar
		}
		if i == endLine {
			if endChar > len(lineContent) {
				return "", fmt.Errorf("invalid range: end character %d out of bounds on line %d (len %d)", endChar, i, len(lineContent))
			}
			lineEndChar = endChar
		}

		// Check for valid sub-range within the line
		if lineStartChar >= lineEndChar {
			// If it's the start line and also the end line, and start >= end, result is empty.
			// If it's a middle line, and somehow start >= end, skip (shouldn't happen with checks above).
			// If it's the end line and start >= end, result is empty for this line.
			if i == startLine && i == endLine {
				// Valid empty selection within a line
			} else if i == startLine {
				// Selection ends on a later line, take rest of start line
				builder.WriteString(lineContent[lineStartChar:])
			} else if i == endLine {
				// Selection starts on earlier line, take start of end line (until lineEndChar)
				builder.WriteString(lineContent[:lineEndChar])
			}
			// Otherwise (middle lines), this condition implies an issue or empty line selection.
		} else {
			builder.WriteString(lineContent[lineStartChar:lineEndChar])
		}

		// Add newline if not the last line of the selection
		if i < endLine {
			builder.WriteString("\n")
		}
	}

	return builder.String(), nil
}

// getTextAtLine extracts the text at the specified line number.
func getTextAtLine(content string, lineNum uint) (string, uint, error) {
	lines := strings.Split(content, "\n")
	if int(lineNum) >= len(lines) {
		return "", lineNum, fmt.Errorf("line number %d is out of bounds (0-%d)", lineNum, len(lines)-1)
	}

	return lines[lineNum], lineNum, nil
}

// getCurrentLine extracts the text at the specified line number.
func getCurrentLine(content string, lineNum uint) (string, error) {
	lines := strings.Split(content, "\n")
	if int(lineNum) >= len(lines) {
		return "", fmt.Errorf("line number %d is out of bounds (0-%d)", lineNum, len(lines)-1)
	}
	return lines[lineNum], nil
}

// applyOllamaLineReplacement sends a workspace/applyEdit request to replace a line with new text.
func applyOllamaLineReplacement(ctx context.Context, conn *jsonrpc2.Conn, uri protocol.DocumentURI, version int,
	lineNum uint, oldLine string, textToInsert string) error {

	// Clean up the result - Ollama might add backticks or language hints
	textToInsert = cleanOllamaCodeResult(textToInsert)
	if textToInsert == "" {
		log.Println("Ollama returned empty result after cleaning, not applying edit.")
		return nil
	}

	// Create the TextEdit to replace the entire line
	edit := protocol.TextEdit{
		Range: protocol.Range{
			Start: protocol.Position{Line: lineNum, Character: 0},
			End:   protocol.Position{Line: lineNum, Character: uint(len(oldLine))},
		},
		NewText: textToInsert,
	}

	// Create the document edit
	docEdit := protocol.TextDocumentEdit{
		TextDocument: protocol.VersionedTextDocumentIdentifier{
			TextDocumentIdentifier: protocol.TextDocumentIdentifier{URI: uri},
			Version:                version,
		},
		Edits: []protocol.TextEdit{edit},
	}

	// Create the WorkspaceEdit
	workspaceEdit := protocol.WorkspaceEdit{
		DocumentChanges: []protocol.TextDocumentEdit{docEdit},
	}

	// Create ApplyWorkspaceEditParams
	applyParams := protocol.ApplyWorkspaceEditParams{
		Label: "Ollama Prompt Response",
		Edit:  workspaceEdit,
	}

	// Marshal and send the request
	rawParams, err := json.Marshal(applyParams)
	if err != nil {
		return fmt.Errorf("failed to marshal applyEdit params: %w", err)
	}

	request := &jsonrpc2.RequestMessage{
		JSONRPC: jsonrpc2.Version,
		ID:      getNextRequestID(),
		Method:  protocol.MethodWorkspaceApplyEdit,
		Params:  rawParams,
	}

	log.Printf("<-- Request (to client): Method=%s, ID=%s", request.Method, string(request.ID))
	if err := conn.Write(ctx, request); err != nil {
		return fmt.Errorf("failed to send workspace/applyEdit request: %w", err)
	}

	return nil
}

// --- LSP Notification Helper ---

// showNotification sends a window/showMessage notification to the client.
func showNotification(ctx context.Context, conn *jsonrpc2.Conn, msgType protocol.MessageType, message string) {
	params := protocol.ShowMessageParams{
		Type:    msgType,
		Message: message,
	}
	// Use the server's Notify method which correctly wraps the params
	// We need the server instance here, or pass the conn to a method on the server
	// Let's try using the passed conn directly (assuming lspgo allows this)
	// Note: The `server.Notify` method exists, but isn't easily accessible from handlers without passing `s *Server`.
	// We rely on `conn.Write` being able to serialize `NotificationMessage` correctly.
	// This requires constructing the message manually.

	rawParams, err := json.Marshal(params)
	if err != nil {
		log.Printf("Error marshalling showMessage params: %v", err)
		return
	}

	notification := &jsonrpc2.NotificationMessage{
		JSONRPC: jsonrpc2.Version,
		Method:  protocol.MethodWindowShowMessage,
		Params:  rawParams,
	}

	// Use the context passed to the handler
	if err := conn.Write(ctx, notification); err != nil {
		log.Printf("Error sending showMessage notification: %v", err)
	} else {
		log.Printf("Sent showMessage notification (Type: %d)", msgType)
	}
}

// --- (Optional) Example Diagnostics ---

// sendDummyDiagnostics sends simple example diagnostics to the client.
// sendDummyDiagnostics sends simple example diagnostics to the client.
func sendDummyDiagnostics(ctx context.Context, conn *jsonrpc2.Conn, uri protocol.DocumentURI) {
	log.Printf("Sending dummy diagnostics for %s", uri)
	diagnostics := []protocol.Diagnostic{}

	// Example: Add a warning if document is empty (after getting content)
	docMu.RLock()
	docItem := documents[uri]
	content := docItem.Text // Get the text content from the TextDocumentItem
	docMu.RUnlock()

	if content == "" {
		diagnostics = append(diagnostics, protocol.Diagnostic{
			Range: protocol.Range{ // Range for the whole document (start)
				Start: protocol.Position{Line: 0, Character: 0},
				End:   protocol.Position{Line: 0, Character: 0},
			},
			Severity: protocol.SeverityWarning,
			Source:   "ollama-lsp",
			Message:  "Document is empty.",
		})
	} else if strings.Contains(strings.ToLower(content), "todo") {
		// Find first "TODO"
		lines := strings.Split(content, "\n")
		for i, line := range lines {
			if idx := strings.Index(strings.ToLower(line), "todo"); idx != -1 {
				diagnostics = append(diagnostics, protocol.Diagnostic{
					Range: protocol.Range{
						Start: protocol.Position{Line: uint(i), Character: uint(idx)},
						End:   protocol.Position{Line: uint(i), Character: uint(idx + 4)},
					},
					Severity: protocol.SeverityInfo,
					Source:   "ollama-lsp",
					Message:  "Found TODO item.",
				})
				break // Just show the first one
			}
		}
	}

	params := protocol.PublishDiagnosticsParams{
		URI:         uri,
		Diagnostics: diagnostics,
		// Version: optional document version
	}

	// --- Send Notification using conn.Write ---
	rawParams, err := json.Marshal(params)
	if err != nil {
		log.Printf("Error marshalling diagnostics params: %v", err)
		return
	}
	notification := &jsonrpc2.NotificationMessage{
		JSONRPC: jsonrpc2.Version,
		Method:  protocol.MethodTextDocumentPublishDiagnostics,
		Params:  rawParams,
	}
	if err := conn.Write(ctx, notification); err != nil {
		log.Printf("Error sending diagnostics notification: %v", err)
	}
}

// Helper for prompt logging
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
