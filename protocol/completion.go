package protocol

import "encoding/json"

// CompletionParams parameters for textDocument/completion request.
type CompletionParams struct {
	TextDocumentPositionParams
	// Context CompletionContext `json:"context,omitempty"` // Add if needed for trigger kind etc.
	// WorkDoneProgressParams
	// PartialResultParams
}

// CompletionList represents a list of completion items.
type CompletionList struct {
	// This list it not complete. Further typing should result in recomputing
	// this list.
	IsIncomplete bool `json:"isIncomplete"`
	// The completion items.
	Items []CompletionItem `json:"items"`
}

// CompletionItem represents a single completion suggestion.
type CompletionItem struct {
	// The label of this completion item. By default
	// also the text that is inserted when selecting
	// this completion.
	Label string `json:"label"`
	// The kind of this completion item. Based of the kind
	// an icon is chosen by the editor.
	Kind *CompletionItemKind `json:"kind,omitempty"` // Use pointer for optionality
	// A human-readable string with additional information
	// about this item, like type or symbol information.
	Detail string `json:"detail,omitempty"`
	// A human-readable string that represents a doc-comment.
	Documentation json.RawMessage `json:"documentation,omitempty"` // MarkupContent | string
	// A string that should be inserted into a document when selecting
	// this completion. When `falsy` the label is used.
	InsertText string `json:"insertText,omitempty"`
	// The format of the insert text. The format applies to both the `insertText` property
	// and the `newText` property of a provided `textEdit`.
	InsertTextFormat *InsertTextFormat `json:"insertTextFormat,omitempty"`
	// An edit which is applied to a document when selecting this completion. When an edit is provided the value of
	// `insertText` is ignored.
	//
	// *Note:* The range of the edit must be a single line range and it must contain the position at which completion
	// has been requested.
	TextEdit *TextEdit `json:"textEdit,omitempty"` // Often used for completions replacing existing text

	// Additional text edits that are applied when selecting this completion.
	// Edits must not overlap with the main edit nor with themselves.
	// AdditionalTextEdits []TextEdit `json:"additionalTextEdits,omitempty"`

	// ... other fields like preselect, sortText, filterText, commitCharacters, command etc.
}

// CompletionItemKind specifies the kind of completion item.
type CompletionItemKind int

// Defined kinds (subset)
const (
	Text          CompletionItemKind = 1
	Method        CompletionItemKind = 2
	Function      CompletionItemKind = 3
	Constructor   CompletionItemKind = 4
	Field         CompletionItemKind = 5
	Variable      CompletionItemKind = 6
	Class         CompletionItemKind = 7
	Interface     CompletionItemKind = 8
	Module        CompletionItemKind = 9
	Property      CompletionItemKind = 10
	Unit          CompletionItemKind = 11
	Value         CompletionItemKind = 12
	Enum          CompletionItemKind = 13
	Keyword       CompletionItemKind = 14
	Snippet       CompletionItemKind = 15
	Color         CompletionItemKind = 16
	File          CompletionItemKind = 17
	Reference     CompletionItemKind = 18
	Folder        CompletionItemKind = 19
	EnumMember    CompletionItemKind = 20
	Constant      CompletionItemKind = 21
	Struct        CompletionItemKind = 22
	Event         CompletionItemKind = 23
	Operator      CompletionItemKind = 24
	TypeParameter CompletionItemKind = 25
)

// InsertTextFormat defines whether the insert text is plaintext or a snippet.
type InsertTextFormat int

const (
	// PlainTextFormat the insert text is treated as plain text.
	PlainTextFormat InsertTextFormat = 1
	// SnippetFormat the insert text is treated as a snippet.
	SnippetFormat InsertTextFormat = 2
)

// CompletionOptions server options for completion requests.
// Already defined in general.go, ensure it's up-to-date if needed.
// type CompletionOptions struct {
// 	ResolveProvider   bool     `json:"resolveProvider,omitempty"`
// 	TriggerCharacters []string `json:"triggerCharacters,omitempty"`
// }
