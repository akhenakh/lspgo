package protocol

import "encoding/json"

// DidOpenTextDocumentParams parameters for textDocument/didOpen notification.
type DidOpenTextDocumentParams struct {
	TextDocument TextDocumentItem `json:"textDocument"`
}

// DidChangeTextDocumentParams parameters for textDocument/didChange notification.
type DidChangeTextDocumentParams struct {
	TextDocument   VersionedTextDocumentIdentifier  `json:"textDocument"`
	ContentChanges []TextDocumentContentChangeEvent `json:"contentChanges"` // For full sync, this is one element with the full text
}

// TextDocumentContentChangeEvent an event describing a change to a text document.
// If range and rangeLength are omitted, the new text is the full content of the document.
type TextDocumentContentChangeEvent struct {
	// The range of the document that changed. Left out if ChangedText is the full text.
	Range *Range `json:"range,omitempty"`
	// The length of the range that got replaced. Left out if ChangedText is the full text.
	RangeLength *uint `json:"rangeLength,omitempty"`
	// The new text of the document. If range and rangeLength are omitted,
	// this is the new text of the entire document.
	Text string `json:"text"`
}

// DidSaveTextDocumentParams parameters for textDocument/didSave notification.
type DidSaveTextDocumentParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
	Text         *string                `json:"text,omitempty"` // Optional text content if included by client capability
}

// DidCloseTextDocumentParams parameters for textDocument/didClose notification.
type DidCloseTextDocumentParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
}

// PublishDiagnosticsParams parameters for textDocument/publishDiagnostics notification
type PublishDiagnosticsParams struct {
	URI         DocumentURI  `json:"uri"`
	Version     *int         `json:"version,omitempty"` // Optional version number
	Diagnostics []Diagnostic `json:"diagnostics"`
}

// Diagnostic represents a diagnostic, such as a compiler error or warning.
type Diagnostic struct {
	Range    Range              `json:"range"`
	Severity DiagnosticSeverity `json:"severity,omitempty"`
	Code     json.RawMessage    `json:"code,omitempty"` // int | string
	Source   string             `json:"source,omitempty"`
	Message  string             `json:"message"`
	// RelatedInformation, Tags etc.
}

// DiagnosticSeverity severity level of a diagnostic.
type DiagnosticSeverity int

const (
	SeverityError   DiagnosticSeverity = 1
	SeverityWarning DiagnosticSeverity = 2
	SeverityInfo    DiagnosticSeverity = 3
	SeverityHint    DiagnosticSeverity = 4
)
