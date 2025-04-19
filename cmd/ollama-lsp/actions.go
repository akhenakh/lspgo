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
		protocol.ShowNotification(ctx, conn, protocol.Error, errMsg)
		return nil // Error handled via notification
	}

	log.Printf("Ollama response received for action 'continue'")

	// Apply the continuation edit
	err = applyOllamaContinuation(ctx, conn, args.URI, docVersion, args.Position, ollamaResult)
	if err != nil {
		log.Printf("Error applying Ollama continuation edit: %v", err)
		protocol.ShowNotification(ctx, conn, protocol.Error, fmt.Sprintf("Failed to apply edit: %v", err))
	} else {
		log.Printf("Successfully requested 'workspace/applyEdit' for continuation")
		protocol.ShowNotification(ctx, conn, protocol.Info, "Ollama continuation applied.")
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
		protocol.ShowNotification(ctx, conn, protocol.Error, "Internal error: Missing range for explain action.")
		return fmt.Errorf("range is required for 'explain' action") // Return internal error
	}

	selectedText, err := getTextInRange(content, *args.Range)
	if err != nil {
		errMsg := fmt.Sprintf("Failed to get text in range for 'explain': %v", err)
		log.Println(errMsg)
		protocol.ShowNotification(ctx, conn, protocol.Error, errMsg)
		return fmt.Errorf("failed to get text in range for 'explain': %w", err) // Return internal error
	}
	if selectedText == "" {
		protocol.ShowNotification(ctx, conn, protocol.Warning, "No text selected for 'explain'.")
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
		protocol.ShowNotification(ctx, conn, protocol.Error, errMsg)
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
			protocol.ShowNotification(ctx, conn, protocol.Info, messageToShow)
		} else {
			// Proper JSON parsing error
			protocol.ShowNotification(ctx, conn, protocol.Error, fmt.Sprintf("Failed to parse explanation: %v. Raw response:\n%s", err, ollamaResult))
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
	protocol.SendDiagnostics(ctx, conn, args.URI, diagnostics)

	protocol.ShowNotification(ctx, conn, protocol.Info, fmt.Sprintf("Explanation published %d diagnostics in editor", len(diagnostics)))
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
		protocol.ShowNotification(ctx, conn, protocol.Error, errMsg)
		return fmt.Errorf("failed to get current line %d: %w", lineNum, err) // Return internal error
	}

	trimmedCurrentLine := strings.TrimSpace(currentLine)
	if trimmedCurrentLine == "" {
		protocol.ShowNotification(ctx, conn, protocol.Warning, "Current line is empty. Please type a prompt/instruction first.")
		return nil // User action needed, not an error
	}

	// --- Get context *before* the instruction line ---
	// Use Character: 0 to get everything before the start of the line
	textBeforePromptLine := getTextBeforePosition(content, protocol.Position{Line: lineNum, Character: 0})
	// Remove the trailing newline that getTextBeforePosition might include from the previous line
	textBeforePromptLine = strings.TrimSuffix(textBeforePromptLine, "\n")
	// Ensure the context we check against later doesn't have leading/trailing whitespace issues
	trimmedContextForPrompt := strings.TrimSpace(textBeforePromptLine)
	// Use the potentially whitespace-preserved version in the prompt itself if needed,
	// but use the trimmed one for comparison later. Let's use the original in the prompt.
	// Note: Sending a lot of whitespace context might confuse the model less than trimmed.

	// Explicitly tell the model to ONLY generate the replacement for the instruction line
	// and NOT to repeat the context snippet.
	prompt := fmt.Sprintf(`You are an expert coding assistant. You are given an INSTRUCTION on a specific line in a file, and the CODE SNIPPET that comes *before* that instruction line.
Your task is to generate the code that should *replace* the INSTRUCTION line itself, based on the INSTRUCTION and using the CODE SNIPPET for context if needed.

Respond ONLY with the code meant for replacement.
Do NOT repeat any part of the original CODE SNIPPET in your output.
Do NOT add any preamble, explanation, markdown formatting, or comments about your process.

INSTRUCTION (This line will be replaced by your output):
%s

CODE SNIPPET (Context only - DO NOT INCLUDE THIS IN YOUR RESPONSE):
%s`, trimmedCurrentLine, textBeforePromptLine) // Send original context

	protocol.ShowNotification(ctx, conn, protocol.Info, fmt.Sprintf("Ollama processing prompt: %s...",
		trimmedCurrentLine[:min(30, len(trimmedCurrentLine))]))

	ollamaResult, err := callOllama(ctx, prompt)
	if err != nil {
		errMsg := fmt.Sprintf("Ollama 'prompt' request failed: %v", err)
		log.Println(errMsg)
		protocol.ShowNotification(ctx, conn, protocol.Error, errMsg)
		return nil // Error handled via notification
	}

	log.Printf("Ollama response received for action 'prompt'. Raw length: %d", len(ollamaResult))

	// --- Clean the result and remove potential context prefix ---
	cleanedResult := cleanOllamaCodeResult(ollamaResult) // Remove markdown, trim space
	log.Printf("Ollama response after initial cleaning. Length: %d", len(cleanedResult))

	finalReplacementText := cleanedResult // Start with the initially cleaned result

	// Check if the cleaned result starts with the context we sent (use the trimmed context for comparison)
	// Only attempt removal if the context isn't empty
	if len(trimmedContextForPrompt) > 0 {
		// Normalize the start of the cleaned result for comparison too
		trimmedResultStart := strings.TrimSpace(cleanedResult)

		if strings.HasPrefix(trimmedResultStart, trimmedContextForPrompt) {
			log.Printf("Attempting to remove potential context prefix from Ollama response.")

			// Find the *actual* text to remove from the *original* cleanedResult.
			// This is tricky because of potential whitespace differences.
			// Let's try removing the length of the matched trimmed context from the
			// beginning of the cleanedResult, AFTER trimming leading whitespace from it.
			// This assumes the generated code starts immediately after the context (possibly with whitespace).

			tempTrimmedResult := strings.TrimSpace(cleanedResult)
			if len(tempTrimmedResult) >= len(trimmedContextForPrompt) {
				potentialCodeStart := tempTrimmedResult[len(trimmedContextForPrompt):]
				// Now, find where this potential code start appears in the original cleanedResult
				// to preserve leading whitespace before the *actual* generated code.
				index := strings.Index(cleanedResult, potentialCodeStart)
				if index != -1 {
					finalReplacementText = cleanedResult[index:]
					log.Printf("Removed suspected context prefix. Final text length: %d", len(finalReplacementText))
				} else {
					// Fallback or warning: Couldn't reliably find the start after context
					log.Printf("Warning: Detected context prefix but couldn't reliably isolate generated code. Using potentially prefixed result.")
					// Keep finalReplacementText as cleanedResult in this uncertain case
				}
			} else {
				log.Printf("Warning: Result shorter than context after trimming, cannot remove prefix.")
			}
		} else {
			log.Printf("No context prefix detected in Ollama response based on trimmed comparison.")
		}
	}

	// Final trim space just in case the removal left some
	finalReplacementText = strings.TrimSpace(finalReplacementText)

	// Pass the original line content (including whitespace, but without trailing newline) for replacement calculation
	originalLineForReplacement, _ := getCurrentLine(content, lineNum) // We already checked for error above

	// Apply the line replacement edit using the potentially context-stripped result
	err = applyOllamaLineReplacement(ctx, conn, args.URI, docVersion, lineNum, originalLineForReplacement, finalReplacementText)
	if err != nil {
		log.Printf("Error applying Ollama line replacement: %v", err)
		protocol.ShowNotification(ctx, conn, protocol.Error, fmt.Sprintf("Failed to apply edit: %v", err))
	} else {
		log.Printf("Successfully requested 'workspace/applyEdit' for line replacement")
		protocol.ShowNotification(ctx, conn, protocol.Info, "Ollama prompt result applied.")
	}
	return nil // Edit application outcome handled via notification
}
