package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"

	"github.com/akhenakh/lspgo/jsonrpc2"
	"github.com/akhenakh/lspgo/protocol"
)

type ollamaRequest struct {
	Model  string `json:"model"`
	Prompt string `json:"prompt"`
	Stream bool   `json:"stream"`           // Keep false for simple request/response
	Format string `json:"format,omitempty"` // Request JSON format if needed
}

type ollamaResponse struct {
	Response string `json:"response"`
	Done     bool   `json:"done"`
}

func callOllama(ctx context.Context, prompt string) (string, error) {
	apiURL := ollamaBaseURL + "/api/generate"

	requestPayload := ollamaRequest{
		Model:  ollamaModel,
		Prompt: prompt,
		Stream: false,
	}

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

	log.Printf("Ollama Raw Response Body: %s", string(bodyBytes))

	var ollamaResp ollamaResponse
	if err := json.Unmarshal(bodyBytes, &ollamaResp); err != nil {
		if !strings.HasPrefix(strings.TrimSpace(string(bodyBytes)), "{") {
			log.Printf("Ollama response is not JSON, returning raw body as response string.")
			return strings.TrimSpace(string(bodyBytes)), nil
		}
		return "", fmt.Errorf("failed to decode ollama JSON response: %w. Body: %s", err, string(bodyBytes))
	}

	if !ollamaResp.Done {
		log.Printf("Warning: Ollama response 'done' field is false.")
	}

	return strings.TrimSpace(ollamaResp.Response), nil
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
		return nil // Not an error, just nothing to apply
	}

	edit := protocol.TextEdit{
		Range:   protocol.Range{Start: position, End: position},
		NewText: textToInsert,
	}
	workspaceEdit := createWorkspaceEdit(uri, version, []protocol.TextEdit{edit})
	return sendApplyEditRequest(ctx, conn, "Ollama Continuation", workspaceEdit)
}

// applyOllamaLineReplacement sends a workspace/applyEdit request to replace a line with new text.
func applyOllamaLineReplacement(ctx context.Context, conn *jsonrpc2.Conn, uri protocol.DocumentURI, version int,
	lineNum uint, oldLine string, textToInsert string) error {

	textToInsert = cleanOllamaCodeResult(textToInsert)
	if textToInsert == "" {
		log.Println("Ollama returned empty result after cleaning, not applying edit.")
		showNotification(ctx, conn, protocol.Warning, "Ollama returned empty result.")
		return nil // Not an error, just nothing to apply
	}

	// Calculate the range to replace the entire line content
	// Use the length of the original line content *excluding* the newline character itself
	// This ensures the replacement happens correctly whether the line had a newline or not (EOF case)
	originalContentLength := uint(len(strings.TrimSuffix(oldLine, "\n")))
	replaceRange := protocol.Range{
		Start: protocol.Position{Line: lineNum, Character: 0},
		End:   protocol.Position{Line: lineNum, Character: originalContentLength},
	}

	edit := protocol.TextEdit{
		Range:   replaceRange,
		NewText: textToInsert,
	}
	workspaceEdit := createWorkspaceEdit(uri, version, []protocol.TextEdit{edit})
	return sendApplyEditRequest(ctx, conn, "Ollama Prompt Response", workspaceEdit)
}

// cleanOllamaCodeResult removes common markdown artifacts from Ollama's code output.
func cleanOllamaCodeResult(rawResult string) string {
	trimmed := strings.TrimSpace(rawResult)
	lines := strings.Split(trimmed, "\n")
	if len(lines) > 0 && strings.HasPrefix(lines[0], "```") {
		if len(lines) > 1 {
			lines = lines[1:]
		} else {
			return ""
		}
		trimmed = strings.TrimSpace(strings.Join(lines, "\n"))
	}
	trimmed = strings.TrimSuffix(trimmed, "```")
	return strings.TrimSpace(trimmed)
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
