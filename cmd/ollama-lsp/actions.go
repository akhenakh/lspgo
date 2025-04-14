package main

import (
	"context"
	"fmt"
	"log"
	"strings"

	"github.com/akhenakh/lspgo/jsonrpc2"
	"github.com/akhenakh/lspgo/protocol"
)

// executeContinueAction handles the "continue" action.
func executeContinueAction(ctx context.Context, conn *jsonrpc2.Conn, args OllamaActionArgs, docItem protocol.TextDocumentItem) error {
	content := docItem.Text
	docVersion := docItem.Version

	textBeforeCursor := getTextBeforePosition(content, args.Position)
	prompt := fmt.Sprintf(`You are an expert coding assistant. Continue the following code snippet directly without any preamble or explanation.
Respond ONLY with the code that should come next.

Code Snippet:
%s`, textBeforeCursor)

	ollamaResult, err := callOllama(ctx, prompt)
	if err != nil {
		errMsg := fmt.Sprintf("Ollama 'continue' request failed: %v", err)
		log.Println(errMsg)
		showNotification(ctx, conn, protocol.Error, errMsg)
		return nil // Error handled via notification
	}

	log.Printf("Ollama response received for action 'continue'")

	err = applyOllamaContinuation(ctx, conn, args.URI, docVersion, args.Position, ollamaResult)
	if err != nil {
		log.Printf("Error applying Ollama continuation edit: %v", err)
		showNotification(ctx, conn, protocol.Error, fmt.Sprintf("Failed to apply edit: %v", err))
	} else {
		log.Printf("Successfully requested 'workspace/applyEdit' for continuation")
		showNotification(ctx, conn, protocol.Info, "Ollama continuation applied.")
	}
	return nil // Edit application outcome handled via notification
}

// executeExplainAction handles the "explain" action.
func executeExplainAction(ctx context.Context, conn *jsonrpc2.Conn, args OllamaActionArgs, docItem protocol.TextDocumentItem) error {
	content := docItem.Text
	// docVersion := docItem.Version // Not directly needed for diagnostics, but could be for version checks

	if args.Range == nil {
		// This should ideally be caught by client-side validation or codeAction logic
		log.Println("Error: Range is nil for 'explain' action")
		showNotification(ctx, conn, protocol.Error, "Internal error: Missing range for explain action.")
		return fmt.Errorf("range is required for 'explain' action") // Return internal error
	}

	selectedText, err := getTextInRange(content, *args.Range)
	if err != nil {
		errMsg := fmt.Sprintf("Failed to get text in range for 'explain': %v", err)
		log.Println(errMsg)
		showNotification(ctx, conn, protocol.Error, errMsg)
		return fmt.Errorf("failed to get text in range for 'explain': %w", err) // Return internal error
	}
	if selectedText == "" {
		showNotification(ctx, conn, protocol.Warning, "No text selected for 'explain'.")
		return nil // User action needed, not an error
	}

	numberedSelectedText := addLineNumbers(selectedText)
	prompt := fmt.Sprintf(`You are an expert coding assistant. Analyze the following code, where each line is prefixed with its line number (relative to the selection, starting from 0). Provide explanations for notable lines.
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

	ollamaResult, err := callOllama(ctx, prompt)
	if err != nil {
		errMsg := fmt.Sprintf("Ollama 'explain' request failed: %v", err)
		log.Println(errMsg)
		showNotification(ctx, conn, protocol.Error, errMsg)
		return nil // Error handled via notification
	}

	log.Printf("Ollama response received for action 'explain'")

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
		return nil // Parsing failure handled via notification
	}

	// Split the *original* selected text into lines for proper line length calculations
	// This text was already retrieved successfully earlier.
	selectedLines := strings.Split(selectedText, "\n")

	// Create diagnostics from explanations
	diagnostics := []protocol.Diagnostic{}
	for _, item := range explanations {
		relativeLineNum := item.LineNumber
		if relativeLineNum < 0 || relativeLineNum >= len(selectedLines) {
			log.Printf("Warning: Explanation received for invalid relative line %d (selection has %d lines)", relativeLineNum, len(selectedLines))
			continue
		}

		actualDocLine := int(args.Range.Start.Line) + relativeLineNum
		lineLength := uint(len(selectedLines[relativeLineNum]))

		diagnostics = append(diagnostics, protocol.Diagnostic{
			Range: protocol.Range{
				Start: protocol.Position{Line: uint(actualDocLine), Character: 0},
				End:   protocol.Position{Line: uint(actualDocLine), Character: lineLength},
			},
			Severity: protocol.SeverityInfo,
			Source:   "ollama-lsp (explain)",
			Message:  item.Explanation,
		})
	}

	// Publish diagnostics to the editor
	sendDiagnostics(ctx, conn, args.URI, diagnostics)

	showNotification(ctx, conn, protocol.Info, fmt.Sprintf("Explanation published %d diagnostics in editor", len(diagnostics)))
	return nil // Diagnostics published successfully
}

// executePromptAction handles the "prompt" action.
func executePromptAction(ctx context.Context, conn *jsonrpc2.Conn, args OllamaActionArgs, docItem protocol.TextDocumentItem) error {
	content := docItem.Text
	docVersion := docItem.Version
	lineNum := args.Position.Line // Line containing the instruction

	currentLine, err := getCurrentLine(content, lineNum)
	if err != nil {
		errMsg := fmt.Sprintf("Failed to get current line %d: %v", lineNum, err)
		log.Println(errMsg)
		showNotification(ctx, conn, protocol.Error, errMsg)
		return fmt.Errorf("failed to get current line %d: %w", lineNum, err) // Return internal error
	}

	trimmedCurrentLine := strings.TrimSpace(currentLine)
	if trimmedCurrentLine == "" {
		showNotification(ctx, conn, protocol.Warning, "Current line is empty. Please type a prompt/instruction first.")
		return nil // User action needed, not an error
	}

	textBeforePromptLine := getTextBeforePosition(content, protocol.Position{Line: lineNum, Character: 0})
	textBeforePromptLine = strings.TrimSuffix(textBeforePromptLine, "\n")

	prompt := fmt.Sprintf(`You are an expert coding assistant. Use the INSTRUCTION below to modify or generate code based on the CODE SNIPPET.
Respond ONLY with the resulting code, without any preamble or explanation.

INSTRUCTION:
%s

CODE SNIPPET (Code before the instruction line):
%s`, trimmedCurrentLine, textBeforePromptLine)

	showNotification(ctx, conn, protocol.Info, fmt.Sprintf("Ollama processing prompt: %s...",
		trimmedCurrentLine[:min(30, len(trimmedCurrentLine))]))

	ollamaResult, err := callOllama(ctx, prompt)
	if err != nil {
		errMsg := fmt.Sprintf("Ollama 'prompt' request failed: %v", err)
		log.Println(errMsg)
		showNotification(ctx, conn, protocol.Error, errMsg)
		return nil // Error handled via notification
	}

	log.Printf("Ollama response received for action 'prompt'")

	// Pass the original line content (including whitespace, but without trailing newline) for replacement calculation
	originalLineForReplacement, _ := getCurrentLine(content, lineNum) // We already checked for error above

	err = applyOllamaLineReplacement(ctx, conn, args.URI, docVersion, lineNum, originalLineForReplacement, ollamaResult)
	if err != nil {
		log.Printf("Error applying Ollama line replacement: %v", err)
		showNotification(ctx, conn, protocol.Error, fmt.Sprintf("Failed to apply edit: %v", err))
	} else {
		log.Printf("Successfully requested 'workspace/applyEdit' for line replacement")
		showNotification(ctx, conn, protocol.Info, "Ollama prompt result applied.")
	}
	return nil // Edit application outcome handled via notification
}
