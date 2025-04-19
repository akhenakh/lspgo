package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"

	"github.com/akhenakh/lspgo/jsonrpc2"
	"github.com/akhenakh/lspgo/protocol"
)

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

// --- Execute Command Handling ---

// handleExecuteCommand main entry point for workspace/executeCommand
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
		protocol.ShowNotification(ctx, conn, protocol.Error, errMsg)
		// Return nil error, user was notified
		return nil, nil
	}

	// Show "Thinking..." message
	protocol.ShowNotification(ctx, conn, protocol.Info, fmt.Sprintf("Ollama (%s) is thinking...", args.Action))

	// Dispatch to action-specific handlers
	var err error
	switch args.Action {
	case "continue":
		err = executeContinueAction(ctx, conn, args, docItem)
	case "explain":
		err = executeExplainAction(ctx, conn, args, docItem)
	case "prompt":
		err = executePromptAction(ctx, conn, args, docItem)
	default:
		errMsg := fmt.Sprintf("Unknown action '%s' in command arguments", args.Action)
		log.Println(errMsg)
		protocol.ShowNotification(ctx, conn, protocol.Error, errMsg)
		// Return nil error, user was notified
		err = nil
	}

	// Log any internal errors from the action handlers (rare)
	if err != nil {
		log.Printf("Error during action execution '%s': %v", args.Action, err)
		// Optionally notify the user about the internal error, though sub-functions
		// should generally handle user-facing notifications.
		// showNotification(ctx, conn, protocol.Error, fmt.Sprintf("Internal error during %s: %v", args.Action, err))
	}

	// Return nil error signifies the command execution logic finished
	// User feedback (success/failure) is handled via notifications within action handlers.
	return nil, nil
}
