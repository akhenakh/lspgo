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

	// The lspgo server library should automatically determine capabilities
	// based on registered handlers, including executeCommandProvider if
	// workspace/executeCommand is registered.

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
	return nil
}

func handleDidChange(ctx context.Context, params *protocol.DidChangeTextDocumentParams) error {
	if len(params.ContentChanges) == 0 {
		return nil
	}
	// Assuming full sync, the first change contains the whole text
	fullText := params.ContentChanges[0].Text

	docMu.Lock()
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
	return nil
}

func handleDidClose(ctx context.Context, params *protocol.DidCloseTextDocumentParams) error {
	docMu.Lock()
	delete(documents, params.TextDocument.URI)
	docMu.Unlock()
	log.Printf("Document Closed: %s", params.TextDocument.URI)
	return nil
}

// handleCodeAction function provides available actions
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

	// --- Action 1: Continue ---
	continueArgs := OllamaActionArgs{
		Action:   "continue",
		URI:      uri,
		Position: params.Range.Start,
	}
	continueCmdArgs, _ := json.Marshal(continueArgs)

	actions = append(actions, protocol.CodeAction{
		Title: "Ollama: Continue...",
		Kind:  protocol.RefactorInline, // Suggests inline code generation
		Command: &protocol.Command{
			Title:     "Ollama: Continue...",
			Command:   "ollama/executeAction",
			Arguments: []json.RawMessage{continueCmdArgs},
		},
	})

	// --- Action 2: Explain Selection (if there is a selection) ---
	if params.Range.Start != params.Range.End {
		explainArgs := OllamaActionArgs{
			Action: "explain",
			URI:    uri,
			Range:  &params.Range,
		}
		explainCmdArgs, _ := json.Marshal(explainArgs)

		actions = append(actions, protocol.CodeAction{
			Title: "Ollama: Explain selection with diagnostics...",
			Kind:  protocol.Source, // Source actions are often for analysis/refactoring without direct code change
			Command: &protocol.Command{
				Title:     "Ollama: Explain selection with diagnostics...",
				Command:   "ollama/executeAction",
				Arguments: []json.RawMessage{explainCmdArgs},
			},
		})
	}

	// --- Action 3: Prompt (Current Line) ---
	promptArgs := OllamaActionArgs{
		Action:   "prompt",
		URI:      uri,
		Position: params.Range.Start, // Use start of selection/cursor position
	}
	promptCmdArgs, _ := json.Marshal(promptArgs)

	actions = append(actions, protocol.CodeAction{
		Title: "Ollama: Use current line as prompt...",
		Kind:  protocol.Source, // Similar to explain, source-level action
		Command: &protocol.Command{
			Title:     "Ollama: Use current line as prompt...",
			Command:   "ollama/executeAction",
			Arguments: []json.RawMessage{promptCmdArgs},
		},
	})

	log.Printf("Offering %d code actions for %s", len(actions), uri)
	return actions, nil
}

// Define a structure for parsing the JSON response from Ollama for explanations
type ExplanationItem struct {
	LineNumber  int    `json:"line"`
	Explanation string `json:"explanation"`
}

type ExplanationResponse struct {
	Explanations []ExplanationItem `json:"explanations"`
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
		showNotification(ctx, conn, protocol.Error, errMsg)
		return nil, fmt.Errorf(errMsg)
	}
	content := docItem.Text
	docVersion := docItem.Version

	// Show "Thinking..." message
	showNotification(ctx, conn, protocol.Info, fmt.Sprintf("Ollama (%s) is thinking...", args.Action))

	var prompt string
	var err error

	switch args.Action {
	case "continue":
		textBeforeCursor := getTextBeforePosition(content, args.Position)
		prompt = fmt.Sprintf(`You are an expert coding assistant. Continue the following code snippet directly without any preamble or explanation.
Respond ONLY with the code that should come next.

Code Snippet:
%s`, textBeforeCursor)

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
			return nil, nil
		}

		// Prefix lines with numbers before sending to Ollama
		numberedSelectedText := addLineNumbers(selectedText)

		// request JSON response with line numbers and explanations,
		// using the line-numbered code as input.
		prompt = fmt.Sprintf(`You are an expert coding assistant. Analyze the following code, where each line is prefixed with its line number (relative to the selection, starting from 0). Provide explanations for notable lines.
Format your response strictly as a JSON object containing only an "explanations" array. Each item in the array should have a "line" number (use the number from the input prefix) and an "explanation" string. Respond ONLY with the JSON object.

Example Input Code:
0: const x = 10;
1:
2: function greet(name) {
3:   console.log("Hello, " + name);
4: }

Example JSON Output:
{
  "explanations": [
    { "line": 0, "explanation": "This line initializes the constant 'x' to 10." },
    { "line": 3, "explanation": "This line logs a greeting message to the console." }
  ]
}

Selected Code with Line Numbers:
%s`, numberedSelectedText)

	case "prompt":
		lineNum := args.Position.Line
		currentLine, err := getCurrentLine(content, lineNum)
		if err != nil {
			errMsg := fmt.Sprintf("Failed to get current line: %v", err)
			log.Println(errMsg)
			showNotification(ctx, conn, protocol.Error, errMsg)
			return nil, nil
		}

		currentLine = strings.TrimSpace(currentLine)
		// Get text *before* the start of the prompt line
		textBeforePromptLine := getTextBeforePosition(content, protocol.Position{
			Line:      lineNum,
			Character: 0,
		})
		textBeforePromptLine = strings.TrimSuffix(textBeforePromptLine, "\n")

		if currentLine == "" {
			showNotification(ctx, conn, protocol.Warning, "Current line is empty. Please type a prompt/instruction first.")
			return nil, nil
		}

		// Use the current line as instructions for what to generate next.
		prompt = fmt.Sprintf(`You are an expert coding assistant. Use the INSTRUCTION below to modify or generate code based on the CODE SNIPPET.
Respond ONLY with the resulting code, without any preamble or explanation.

INSTRUCTION:
%s

CODE SNIPPET (Code before the instruction line):
%s`, currentLine, textBeforePromptLine)

		showNotification(ctx, conn, protocol.Info, fmt.Sprintf("Ollama processing prompt: %s...",
			currentLine[:min(30, len(currentLine))]))

	default:
		errMsg := fmt.Sprintf("Unknown action '%s' in command arguments", args.Action)
		log.Println(errMsg)
		showNotification(ctx, conn, protocol.Error, errMsg)
		return nil, fmt.Errorf(errMsg)
	}

	// Call Ollama API
	ollamaResult, err := callOllama(ctx, prompt)
	if err != nil {
		errMsg := fmt.Sprintf("Ollama request failed: %v", err)
		log.Println(errMsg)
		showNotification(ctx, conn, protocol.Error, errMsg)
		// Return nil here, error already shown to user via notification
		return nil, nil
	}

	log.Printf("Ollama response received for action '%s'", args.Action)

	// --- Apply Result Based on Action ---
	switch args.Action {
	case "continue":
		err = applyOllamaContinuation(ctx, conn, args.URI, docVersion, args.Position, ollamaResult)
		if err != nil {
			log.Printf("Error applying Ollama continuation edit: %v", err)
			showNotification(ctx, conn, protocol.Error, fmt.Sprintf("Failed to apply edit: %v", err))
		} else {
			log.Printf("Successfully requested 'workspace/applyEdit' for continuation")
			showNotification(ctx, conn, protocol.Info, "Ollama continuation applied.")
		}

	case "explain":
		// New handling for explain action - parse JSON and create diagnostics
		explanations, err := parseExplanationResponse(ollamaResult)
		if err != nil {
			log.Printf("Error parsing explanation response: %v", err)
			// Check if the raw result looks like an explanation itself
			if len(strings.TrimSpace(ollamaResult)) > 0 && !strings.Contains(ollamaResult, `"explanations"`) {
				log.Printf("Explanation response did not contain expected JSON, showing raw response.")
				messageToShow := fmt.Sprintf("Ollama Explanation:\n---\n%s\n---", ollamaResult)
				showNotification(ctx, conn, protocol.Info, messageToShow)
			} else {
				// Proper JSON parsing error
				showNotification(ctx, conn, protocol.Error, fmt.Sprintf("Failed to parse explanation: %v. Raw response:\n%s", err, ollamaResult))
			}
			return nil, nil // Don't proceed if parsing failed
		}

		// Get the original selected text again (needed for line length calculation)
		// We could potentially pass this down instead of re-calculating
		selectedText, err := getTextInRange(content, *args.Range)
		if err != nil {
			errMsg := fmt.Sprintf("Failed to get text in range for diagnostics: %v", err)
			log.Println(errMsg)
			showNotification(ctx, conn, protocol.Error, errMsg)
			return nil, fmt.Errorf(errMsg) // Internal error, okay to return error here
		}

		// Split the *original* selected text into lines for proper line length calculations
		selectedLines := strings.Split(selectedText, "\n")

		// Create diagnostics from explanations
		diagnostics := []protocol.Diagnostic{}
		for _, item := range explanations {
			// The 'line' number from the JSON should correspond to the prefixed line number
			// in the prompt, which is the relative line number within the selection.
			relativeLineNum := item.LineNumber

			// Skip invalid line numbers relative to the selection
			if relativeLineNum < 0 || relativeLineNum >= len(selectedLines) {
				log.Printf("Warning: Explanation received for invalid relative line %d (selection has %d lines)", relativeLineNum, len(selectedLines))
				continue
			}

			// Calculate the actual line number in the document
			// args.Range.Start.Line is the first line of the selection
			actualDocLine := int(args.Range.Start.Line) + relativeLineNum
			// Calculate the length of the corresponding original line within the selection
			lineLength := uint(len(selectedLines[relativeLineNum]))

			// Create diagnostic for this line
			diagnostic := protocol.Diagnostic{
				Range: protocol.Range{
					Start: protocol.Position{
						Line:      uint(actualDocLine),
						Character: 0, // Place diagnostic on the whole line
					},
					End: protocol.Position{
						Line:      uint(actualDocLine),
						Character: lineLength, // Cover the whole line
					},
				},
				Severity: protocol.SeverityInfo, // Use Info level for explanations
				Source:   "ollama-lsp (explain)",
				Message:  item.Explanation,
			}

			diagnostics = append(diagnostics, diagnostic)
		}

		// Publish diagnostics to the editor
		// Note: This will overwrite any previous diagnostics from this source for this file.
		sendDiagnostics(ctx, conn, args.URI, diagnostics)

		// Show a notification that diagnostics have been published
		showNotification(ctx, conn, protocol.Info, fmt.Sprintf("Explanation published %d diagnostics in editor", len(diagnostics)))

	case "prompt":
		lineNum := args.Position.Line // The line where the prompt instruction was
		originalLine, err := getCurrentLine(content, lineNum)
		if err != nil {
			errMsg := fmt.Sprintf("Failed to get original current line for replacement: %v", err)
			log.Println(errMsg)
			showNotification(ctx, conn, protocol.Error, errMsg)
			return nil, nil
		}

		err = applyOllamaLineReplacement(ctx, conn, args.URI, docVersion, lineNum, originalLine, ollamaResult)
		if err != nil {
			log.Printf("Error applying Ollama line replacement: %v", err)
			showNotification(ctx, conn, protocol.Error, fmt.Sprintf("Failed to apply edit: %v", err))
		} else {
			log.Printf("Successfully requested 'workspace/applyEdit' for line replacement")
			showNotification(ctx, conn, protocol.Info, "Ollama prompt result applied.")
		}

	default:
		// Should have been caught earlier, but defensively handle here too
		errMsg := fmt.Sprintf("Unknown action '%s' in command arguments", args.Action)
		log.Println(errMsg)
		showNotification(ctx, conn, protocol.Error, errMsg)
		return nil, fmt.Errorf(errMsg)
	}

	// Return nil error signifies the command execution logic finished (even if Ollama failed and showed a notification)
	return nil, nil
}

// addLineNumbers takes a block of text and prefixes each line with its number.
func addLineNumbers(text string) string {
	lines := strings.Split(text, "\n")
	var builder strings.Builder
	for i, line := range lines {
		// Don't add newline for the very last line if it's empty (common after split)
		if i == len(lines)-1 && line == "" {
			continue
		}
		builder.WriteString(strconv.Itoa(i))
		builder.WriteString(": ")
		builder.WriteString(line)
		// Add newline except for the last actual line of content
		if i < len(lines)-1 {
			// Check if the next line is the empty one from split, if so don't add newline yet
			isLastRealLine := (i == len(lines)-2 && lines[len(lines)-1] == "")
			if !isLastRealLine {
				builder.WriteString("\n")
			}
		}

	}
	return builder.String()
}

// Function to parse JSON explanation response from Ollama
func parseExplanationResponse(response string) ([]ExplanationItem, error) {
	// Try to extract JSON from the response (in case the model adds extra text)
	jsonStart := strings.Index(response, "{")
	jsonEnd := strings.LastIndex(response, "}")

	// Be a bit more lenient: allow starting with `[` if it's just the array
	if jsonStart == -1 || response[jsonStart] != '{' {
		jsonStart = strings.Index(response, "[")
		jsonEnd = strings.LastIndex(response, "]")
		// If we found brackets, wrap them in the expected structure
		if jsonStart != -1 && jsonEnd != -1 && jsonEnd > jsonStart {
			response = fmt.Sprintf(`{ "explanations": %s }`, response[jsonStart:jsonEnd+1])
			jsonStart = 0 // Reset start index after wrapping
			jsonEnd = len(response) - 1
		} else {
			jsonStart = -1 // Reset if bracket finding didn't work
		}
	}

	if jsonStart == -1 || jsonEnd == -1 || jsonEnd <= jsonStart {
		return nil, fmt.Errorf("could not find valid JSON object or array in response")
	}

	jsonStr := response[jsonStart : jsonEnd+1]
	log.Printf("Attempting to parse JSON: %s", jsonStr) // Log the extracted JSON

	// Try to parse the JSON
	var result ExplanationResponse
	// Use a decoder for potentially better error messages
	decoder := json.NewDecoder(strings.NewReader(jsonStr))
	decoder.DisallowUnknownFields() // Help catch malformed JSON structure

	if err := decoder.Decode(&result); err != nil {
		// Try unmarshalling without DisallowUnknownFields for flexibility
		if errRetry := json.Unmarshal([]byte(jsonStr), &result); errRetry != nil {
			return nil, fmt.Errorf("invalid JSON format: %w (strict parse failed: %v)", errRetry, err)
		}
		log.Printf("Warning: Parsed explanation JSON with unknown fields allowed.")
	}

	// Basic validation of content
	if result.Explanations == nil {
		// Might be valid empty JSON `{}` or `{"explanations": null}`
		log.Printf("Parsed explanation JSON, but 'explanations' field is null or missing.")
		return []ExplanationItem{}, nil // Return empty slice, not error
	}

	return result.Explanations, nil
}

// Function to send diagnostics to the client
func sendDiagnostics(ctx context.Context, conn *jsonrpc2.Conn, uri protocol.DocumentURI, diagnostics []protocol.Diagnostic) {
	params := protocol.PublishDiagnosticsParams{
		URI:         uri,
		Diagnostics: diagnostics,
		// Optionally include version if client supports it and it helps avoid race conditions
		// Version: docVersion, // Need to pass docVersion down or retrieve it here
	}

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

	log.Printf("<-- Notification: Method=%s, URI=%s, Diagnostics=%d",
		notification.Method, uri, len(diagnostics))
	if err := conn.Write(ctx, notification); err != nil {
		log.Printf("Error sending diagnostics notification: %v", err)
	}
}

// OllamaActionArgs defines the structure for arguments passed to our custom command
type OllamaActionArgs struct {
	Action   string               `json:"action"` // "continue", "explain", "prompt"
	URI      protocol.DocumentURI `json:"uri"`
	Position protocol.Position    `json:"position,omitempty"` // Used for "continue", "prompt" (cursor/line)
	Range    *protocol.Range      `json:"range,omitempty"`    // Used for "explain" (selection)
}

// applyOllamaContinuation sends a workspace/applyEdit request to insert the text.
func applyOllamaContinuation(ctx context.Context, conn *jsonrpc2.Conn, uri protocol.DocumentURI, version int, position protocol.Position, textToInsert string) error {
	// Clean up the result - Ollama might add backticks or language hints
	textToInsert = cleanOllamaCodeResult(textToInsert)
	if textToInsert == "" {
		log.Println("Ollama returned empty result after cleaning, not applying edit.")
		showNotification(ctx, conn, protocol.Warning, "Ollama returned empty result.")
		return nil
	}

	// Create the TextEdit
	edit := protocol.TextEdit{
		Range: protocol.Range{ // Zero-length range at the cursor for insertion
			Start: position,
			End:   position,
		},
		NewText: textToInsert,
	}

	// Create the WorkspaceEdit using preferred DocumentChanges
	workspaceEdit := protocol.WorkspaceEdit{
		DocumentChanges: []protocol.TextDocumentEdit{
			{
				TextDocument: protocol.VersionedTextDocumentIdentifier{
					TextDocumentIdentifier: protocol.TextDocumentIdentifier{URI: uri},
					Version:                version, // Use the tracked version
				},
				Edits: []protocol.TextEdit{edit},
			},
		},
	}

	// Send the edit request to the client
	return sendApplyEditRequest(ctx, conn, "Ollama Continuation", workspaceEdit)
}

// applyOllamaLineReplacement sends a workspace/applyEdit request to replace a line with new text.
func applyOllamaLineReplacement(ctx context.Context, conn *jsonrpc2.Conn, uri protocol.DocumentURI, version int,
	lineNum uint, oldLine string, textToInsert string) error {

	// Clean up the result
	textToInsert = cleanOllamaCodeResult(textToInsert)
	if textToInsert == "" {
		log.Println("Ollama returned empty result after cleaning, not applying edit.")
		showNotification(ctx, conn, protocol.Warning, "Ollama returned empty result.")
		return nil
	}

	// Create the TextEdit to replace the entire line
	edit := protocol.TextEdit{
		Range: protocol.Range{
			Start: protocol.Position{Line: lineNum, Character: 0},
			// Use the length of the original line content for the end character
			End: protocol.Position{Line: lineNum, Character: uint(len(strings.TrimSuffix(oldLine, "\n")))}, // Use original length
		},
		NewText: textToInsert,
	}

	// Create the WorkspaceEdit using preferred DocumentChanges
	workspaceEdit := protocol.WorkspaceEdit{
		DocumentChanges: []protocol.TextDocumentEdit{
			{
				TextDocument: protocol.VersionedTextDocumentIdentifier{
					TextDocumentIdentifier: protocol.TextDocumentIdentifier{URI: uri},
					Version:                version,
				},
				Edits: []protocol.TextEdit{edit},
			},
		},
	}

	// Send the edit request to the client
	return sendApplyEditRequest(ctx, conn, "Ollama Prompt Response", workspaceEdit)
}

// sendApplyEditRequest sends the workspace/applyEdit request to the client.
func sendApplyEditRequest(ctx context.Context, conn *jsonrpc2.Conn, label string, edit protocol.WorkspaceEdit) error {
	applyParams := protocol.ApplyWorkspaceEditParams{
		Label: label, // Undo label
		Edit:  edit,
	}

	rawParams, err := json.Marshal(applyParams)
	if err != nil {
		return fmt.Errorf("failed to marshal applyEdit params: %w", err)
	}

	request := &jsonrpc2.RequestMessage{
		JSONRPC: jsonrpc2.Version,
		ID:      getNextRequestID(), // Generate a unique ID for the request
		Method:  protocol.MethodWorkspaceApplyEdit,
		Params:  rawParams,
	}

	log.Printf("<-- Request (to client): Method=%s, ID=%s, Label=%s", request.Method, string(request.ID), label)
	if err := conn.Write(ctx, request); err != nil {
		return fmt.Errorf("failed to send workspace/applyEdit request: %w", err)
	}

	// Note: We are *not* waiting for the client's response here for simplicity.
	// A robust implementation might track the request ID and handle the
	// ApplyWorkspaceEditResponse (which indicates success/failure/reason)
	// when it arrives back from the client in the server's read loop.
	return nil
}

// cleanOllamaCodeResult removes common markdown artifacts from Ollama's code output.
func cleanOllamaCodeResult(rawResult string) string {
	trimmed := strings.TrimSpace(rawResult)

	// Remove ```<lang>\n prefix and ``` suffix, handling potential variations
	lines := strings.Split(trimmed, "\n")
	if len(lines) > 0 && strings.HasPrefix(lines[0], "```") {
		// Check if there's content after the first line
		if len(lines) > 1 {
			lines = lines[1:] // Remove the first line (```lang or just ```)
		} else {
			// Only contains ```, result is empty
			return ""
		}
		// Rejoin the remaining lines and trim again
		trimmed = strings.TrimSpace(strings.Join(lines, "\n"))
	}

	// Remove trailing ``` if present
	trimmed = strings.TrimSuffix(trimmed, "```")

	return strings.TrimSpace(trimmed) // Trim again after removing fences
}

type ollamaRequest struct {
	Model  string `json:"model"`
	Prompt string `json:"prompt"`
	Stream bool   `json:"stream"`           // Keep false for simple request/response
	Format string `json:"format,omitempty"` // Request JSON format if needed
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

	// If the prompt asks for JSON, tell the Ollama API to enforce it (if supported)
	if strings.Contains(prompt, "JSON object") || strings.Contains(prompt, `"explanations"`) {
		requestPayload.Format = "json"
		log.Println("Requesting JSON format from Ollama API")
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

	log.Printf("Sending request to Ollama API: %s (Model: %s, Format: %s)", apiURL, ollamaModel, requestPayload.Format)
	// Limit logging prompt length
	logPrompt := prompt
	if len(logPrompt) > 200 {
		logPrompt = logPrompt[:200] + "..."
	}
	log.Printf("Prompt: %s", logPrompt)

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("ollama request failed: %w", err)
	}
	defer resp.Body.Close()

	bodyBytes, readErr := io.ReadAll(resp.Body)
	if readErr != nil {
		return "", fmt.Errorf("failed to read ollama response body: %w", readErr)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("ollama request failed with status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	log.Printf("Ollama Raw Response Body: %s", string(bodyBytes)) // Log raw response

	var ollamaResp ollamaResponse
	if err := json.Unmarshal(bodyBytes, &ollamaResp); err != nil {
		// Check if the entire body might be the response string itself (some models might do this)
		if !strings.HasPrefix(strings.TrimSpace(string(bodyBytes)), "{") {
			log.Printf("Ollama response is not JSON, returning raw body as response string.")
			return strings.TrimSpace(string(bodyBytes)), nil
		}
		return "", fmt.Errorf("failed to decode ollama JSON response: %w. Body: %s", err, string(bodyBytes))
	}

	if !ollamaResp.Done {
		log.Printf("Warning: Ollama response 'done' field is false.")
	}

	// Return the 'response' field, which should contain the generated text or JSON string
	return strings.TrimSpace(ollamaResp.Response), nil
}

// --- Text Manipulation Helpers ---

// getTextBeforePosition extracts text from the start of the document up to the given position.
func getTextBeforePosition(content string, pos protocol.Position) string {
	lines := strings.SplitAfter(content, "\n") // Keep newlines
	if int(pos.Line) >= len(lines) {
		// Position is out of bounds (e.g., end of file), return everything
		return content
	}

	var builder strings.Builder
	for i := 0; i < int(pos.Line); i++ {
		builder.WriteString(lines[i])
	}

	// Add the part of the target line before the character position
	lineContent := lines[pos.Line]
	// Ensure character pos is within bounds
	charPos := int(pos.Character)
	if charPos > len(lineContent) {
		charPos = len(lineContent)
	}
	builder.WriteString(lineContent[:charPos])

	return builder.String()
}

// getTextInRange extracts text within the specified range.
// Returns an error if the range is invalid.
func getTextInRange(content string, rng protocol.Range) (string, error) {
	lines := strings.Split(content, "\n") // Use simple split for line indexing
	startLine, startChar := int(rng.Start.Line), int(rng.Start.Character)
	endLine, endChar := int(rng.End.Line), int(rng.End.Character)

	// Basic validation
	if startLine < 0 || startLine >= len(lines) || endLine < 0 || endLine >= len(lines) {
		return "", fmt.Errorf("invalid range: line numbers %d-%d out of bounds (0-%d)", startLine, endLine, len(lines)-1)
	}
	if startLine > endLine || (startLine == endLine && startChar > endChar) {
		return "", fmt.Errorf("invalid range: start %v is after end %v", rng.Start, rng.End)
	}
	startLineContent := lines[startLine]
	endLineContent := lines[endLine]
	if startChar > len(startLineContent) {
		return "", fmt.Errorf("invalid range: start character %d out of bounds on line %d (len %d)", startChar, startLine, len(startLineContent))
	}
	if endChar > len(endLineContent) {
		return "", fmt.Errorf("invalid range: end character %d out of bounds on line %d (len %d)", endChar, endLine, len(endLineContent))
	}

	var builder strings.Builder

	// If selection is within a single line
	if startLine == endLine {
		builder.WriteString(lines[startLine][startChar:endChar])
	} else {
		// First line (from startChar to end)
		builder.WriteString(lines[startLine][startChar:])
		builder.WriteString("\n")

		// Middle lines (full lines)
		for i := startLine + 1; i < endLine; i++ {
			builder.WriteString(lines[i])
			builder.WriteString("\n")
		}

		// Last line (from start to endChar)
		builder.WriteString(lines[endLine][:endChar])
	}

	return builder.String(), nil
}

// getCurrentLine extracts the text at the specified line number.
func getCurrentLine(content string, lineNum uint) (string, error) {
	lines := strings.Split(content, "\n")
	if int(lineNum) >= len(lines) {
		return "", fmt.Errorf("line number %d is out of bounds (0-%d)", lineNum, len(lines)-1)
	}
	return lines[lineNum], nil
}

// showNotification sends a window/showMessage notification to the client.
func showNotification(ctx context.Context, conn *jsonrpc2.Conn, msgType protocol.MessageType, message string) {
	params := protocol.ShowMessageParams{
		Type:    msgType,
		Message: message,
	}

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

	log.Printf("<-- Notification: Method=%s, Type=%d, Message=%s",
		notification.Method, msgType, message)

	// Use the context passed to the handler
	if err := conn.Write(ctx, notification); err != nil {
		log.Printf("Error sending showMessage notification: %v", err)
	}
}

// Helper for prompt logging
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
