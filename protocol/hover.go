package protocol

// HoverParams parameters for textDocument/hover request.
// It embeds TextDocumentPositionParams for the standard text document and position fields.
type HoverParams struct {
	TextDocumentPositionParams
	// WorkDoneProgressParams // Optional for progress reporting - can be added if needed
}

// TextDocumentPositionParams parameters for requests identifying a text document and position.
// It's a common structure used by several requests like hover, definition, etc.
// Note: This struct was also hinted at in the main.go comments. It's good practice
// to define common parameter blocks like this separately.
type TextDocumentPositionParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
	Position     Position               `json:"position"`
}

// Hover result for textDocument/hover request.
type Hover struct {
	Contents MarkupContent `json:"contents"`
	Range    *Range        `json:"range,omitempty"` // Optional range the hover applies to
}

// MarkupContent represents structured content for display (like hover).
type MarkupContent struct {
	Kind  MarkupKind `json:"kind"` // "plaintext" or "markdown"
	Value string     `json:"value"`
}

// HoverOptions defines server capabilities for Hover.
// It's referenced in ServerCapabilities in general.go
// If only boolean support is needed, HoverProvider can be set to true.
// If options like workDoneProgress are needed, use *HoverOptions.
type HoverOptions struct {
	WorkDoneProgressOptions // Embed options if needed
}
