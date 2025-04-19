package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/akhenakh/lspgo/jsonrpc2"
	"github.com/akhenakh/lspgo/protocol"
)

var (
	languageToolURL     = getEnv("LANGUAGETOOL_URL", "http://localhost:8081/v2/check") // Default local URL
	languageToolTimeout = 10 * time.Second
	// TODO: Make language configurable (e.g., via init options or env var)
	defaultLanguage = "en-US"
)

// Structs for LanguageTool API Response
// See: https://languagetool.org/http-api/swagger-ui/#!/default/post_check
type LanguageToolResponse struct {
	Software SoftwareInfo `json:"software"`
	Language LanguageInfo `json:"language"`
	Matches  []Match      `json:"matches"`
}

type SoftwareInfo struct {
	Name       string `json:"name"`
	Version    string `json:"version"`
	BuildDate  string `json:"buildDate"`
	APIVersion int    `json:"apiVersion"`
	Status     string `json:"status"`
}

type LanguageInfo struct {
	Name             string               `json:"name"`
	Code             string               `json:"code"`
	DetectedLanguage DetectedLanguageInfo `json:"detectedLanguage"`
}

type DetectedLanguageInfo struct {
	Name       string  `json:"name"`
	Code       string  `json:"code"`
	Confidence float64 `json:"confidence"`
}

type Match struct {
	Message      string        `json:"message"`
	ShortMessage string        `json:"shortMessage"`
	Replacements []Replacement `json:"replacements"`
	Offset       int           `json:"offset"` // Byte offset
	Length       int           `json:"length"` // Byte length
	Context      ContextInfo   `json:"context"`
	Sentence     string        `json:"sentence"`
	Type         TypeInfo      `json:"type"`
	Rule         RuleInfo      `json:"rule"`
	// IgnoreForIncompleteSentence bool `json:"ignoreForIncompleteSentence"`
	// ContextForSureMatch int `json:"contextForSureMatch"`
}

type Replacement struct {
	Value string `json:"value"`
}

type ContextInfo struct {
	Text   string `json:"text"`
	Offset int    `json:"offset"` // Offset within context text
	Length int    `json:"length"` // Length within context text
}

type TypeInfo struct {
	TypeName string `json:"typeName"`
}

type RuleInfo struct {
	ID          string       `json:"id"`
	Description string       `json:"description"`
	IssueType   string       `json:"issueType"`
	Category    CategoryInfo `json:"category"`
	// IsPremium   bool `json:"isPremium"`
}

type CategoryInfo struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// callLanguageTool sends text to the LT API and returns the parsed response.
func callLanguageTool(ctx context.Context, text string, language string) (*LanguageToolResponse, error) {
	if text == "" {
		return &LanguageToolResponse{Matches: []Match{}}, nil // No errors for empty text
	}

	// --- URL Normalization --- START
	apiURL := languageToolURL
	// Ensure the URL ends with /v2/check
	if !strings.HasSuffix(apiURL, "/check") {
		if strings.HasSuffix(apiURL, "/v2") {
			apiURL += "/check"
		} else if strings.HasSuffix(apiURL, "/v2/") {
			apiURL += "check"
		} else {
			// If it doesn't even end with /v2, add the whole thing.
			// Might be better to log a warning here, assuming the user provided something usable.
			apiURL = strings.TrimSuffix(apiURL, "/") + "/v2/check"
			log.Printf("Warning: LANGUAGETOOL_URL '%s' did not end with /v2/check. Appending it automatically -> '%s'", languageToolURL, apiURL)
		}
	}
	// --- URL Normalization --- END

	formData := url.Values{}
	formData.Set("text", text)
	formData.Set("language", language)
	// Add other parameters if needed (e.g., disabledRules, enabledRules)
	// formData.Set("disabledRules", "...")

	reqCtx, cancel := context.WithTimeout(ctx, languageToolTimeout)
	defer cancel()

	// Use the normalized apiURL here
	req, err := http.NewRequestWithContext(reqCtx, "POST", apiURL, strings.NewReader(formData.Encode()))
	if err != nil {
		return nil, fmt.Errorf("failed to create languagetool request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	// Log the actual URL being used
	log.Printf("Sending request to LanguageTool API: %s (Lang: %s, Size: %d bytes)", apiURL, language, len(text))

	// ... rest of the function remains the same ...

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		// Check for context deadline exceeded
		if reqCtx.Err() == context.DeadlineExceeded {
			return nil, fmt.Errorf("languagetool request timed out after %v", languageToolTimeout)
		}
		return nil, fmt.Errorf("languagetool request failed: %w", err)
	}
	defer resp.Body.Close()

	bodyBytes, readErr := io.ReadAll(resp.Body)
	if readErr != nil {
		return nil, fmt.Errorf("failed to read languagetool response body: %w", readErr)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("languagetool request failed with status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	log.Printf("LanguageTool Raw Response Body: %s", string(bodyBytes)) // Keep logging the raw response

	var ltResponse LanguageToolResponse
	if err := json.Unmarshal(bodyBytes, &ltResponse); err != nil {
		return nil, fmt.Errorf("failed to decode languagetool JSON response: %w. Body: %s", err, string(bodyBytes))
	}

	log.Printf("LanguageTool check successful, found %d matches.", len(ltResponse.Matches))
	return &ltResponse, nil
}

// convertMatchesToDiagnostics converts LanguageTool matches to LSP diagnostics.
func convertMatchesToDiagnostics(content string, matches []Match) []protocol.Diagnostic {
	diagnostics := make([]protocol.Diagnostic, 0, len(matches))

	for _, match := range matches {
		rng, err := offsetLengthToRange(content, match.Offset, match.Length)
		if err != nil {
			log.Printf("Error converting offset/length to range for match '%s': %v", match.Message, err)
			// Skip this diagnostic if range calculation fails
			continue
		}

		// Determine severity (heuristic)
		severity := protocol.SeverityWarning // Default to warning
		if strings.Contains(strings.ToLower(match.Rule.Category.ID), "error") ||
			strings.Contains(strings.ToLower(match.Rule.IssueType), "error") ||
			match.Rule.ID == "MORFOLOGIK_RULE_EN_US" { // Example: Spelling errors are often errors
			severity = protocol.SeverityError
		} else if match.Rule.Category.ID == "STYLE" || match.Rule.Category.ID == "TYPOGRAPHY" {
			// Use SeverityInfo instead of SeverityInformation
			severity = protocol.SeverityInfo // <<< FIXED HERE (was SeverityInformation)
		}
		// Could add more rules for hints (SeverityHint) etc.

		// Encode the string rule ID as a JSON string for the json.RawMessage field
		codeJSON, err := json.Marshal(match.Rule.ID)
		if err != nil {
			log.Printf("Error marshalling rule ID '%s' to JSON: %v", match.Rule.ID, err)
			// Assign a default or skip if marshalling fails? Let's assign null.
			codeJSON = json.RawMessage("null")
		}

		diagnostic := protocol.Diagnostic{
			Range:    rng,
			Severity: severity,
			// Assign the marshalled JSON string to the Code field
			Code:    json.RawMessage(codeJSON), // <<< FIXED HERE
			Source:  fmt.Sprintf("languagetool (%s)", match.Rule.Category.Name),
			Message: match.Message,
			// RelatedInformation, Tags etc. could be added if desired
		}
		diagnostics = append(diagnostics, diagnostic)
	}

	return diagnostics
}

// checkDocumentAndSendDiagnostics performs the core logic: call API, convert, send.
func checkDocumentAndSendDiagnostics(ctx context.Context, conn *jsonrpc2.Conn, docItem protocol.TextDocumentItem) {
	if conn == nil {
		log.Printf("Cannot check document %s: connection is nil", docItem.URI)
		return
	}
	// Determine language - simple approach for now
	lang := defaultLanguage
	// A more robust approach would check docItem.LanguageID or LT's detection
	// if docItem.LanguageID != "" { lang = mapLanguageID(docItem.LanguageID) }

	log.Printf("Checking document: %s (Version: %d, Lang: %s)", docItem.URI, docItem.Version, lang)

	ltResponse, err := callLanguageTool(ctx, docItem.Text, lang)
	if err != nil {
		errMsg := fmt.Sprintf("LanguageTool check failed for %s: %v", docItem.URI, err)
		log.Println(errMsg)
		// Show error to user?
		protocol.ShowNotification(ctx, conn, protocol.Error, errMsg)
		// Send empty diagnostics to clear previous errors from this server? Or keep stale ones?
		// Let's clear previous ones on error.
		protocol.SendDiagnostics(ctx, conn, docItem.URI, []protocol.Diagnostic{})
		return
	}

	diagnostics := convertMatchesToDiagnostics(docItem.Text, ltResponse.Matches)
	protocol.SendDiagnostics(ctx, conn, docItem.URI, diagnostics)
}
