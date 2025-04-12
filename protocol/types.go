package protocol

// Position in a text document (zero-based).
type Position struct {
	Line      uint `json:"line"`
	Character uint `json:"character"`
}

// Range in a text document.
type Range struct {
	Start Position `json:"start"`
	End   Position `json:"end"`
}

// Location represents a location inside a resource, such as a line
// inside a text file.
type Location struct {
	URI   DocumentURI `json:"uri"`
	Range Range       `json:"range"`
}

// TextDocumentIdentifier identifies a text document.
type TextDocumentIdentifier struct {
	URI DocumentURI `json:"uri"`
}

// DocumentURI represents the URI of a document.
type DocumentURI string

// VersionedTextDocumentIdentifier identifies a specific version of a text document.
type VersionedTextDocumentIdentifier struct {
	TextDocumentIdentifier
	Version int `json:"version"` // Use int; null version is not typical in requests needing it
}

// TextDocumentItem represents a text document. Used in didOpen.
type TextDocumentItem struct {
	URI        DocumentURI `json:"uri"`
	LanguageID string      `json:"languageId"`
	Version    int         `json:"version"`
	Text       string      `json:"text"`
}

// TextEdit represents a textual change in a document.
type TextEdit struct {
	Range   Range  `json:"range"`
	NewText string `json:"newText"`
}

// TextDocumentEdit describes textual changes on a single text document.
// The text document is referred to by a VersionedTextDocumentIdentifier to allow clients
// to check the text document version before an edit is applied. An array of TextDocumentEdit
// can be part of a WorkspaceEdit's `documentChanges` field.
type TextDocumentEdit struct {
	TextDocument VersionedTextDocumentIdentifier `json:"textDocument"`
	// The edits to be applied.
	Edits []TextEdit `json:"edits"`
}

// WorkspaceEdit represents changes to many resources managed in the workspace.
// A workspace edit consists primarily of textual changes (`changes` or `documentChanges`),
// but can also include resource operations like creating, renaming, or deleting files
// (which are not fully defined in this basic example).
//
// Note: A server should prefer `documentChanges` over `changes` if the client supports
// versioned document edits (`workspace.workspaceEdit.documentChanges` capability).
// If the client supports `documentChanges`, the server should use `documentChanges` exclusively.
// If the client doesn't support `documentChanges`, the server should use `changes`.
type WorkspaceEdit struct {
	// Holds changes to existing resources. The key is the document URI and the value
	// is an array of edits for that document.
	// Deprecated: Clients support `documentChanges` field should ignore this field.
	Changes map[DocumentURI][]TextEdit `json:"changes,omitempty"`

	// An array of `TextDocumentEdit`s or resource operations (like create, rename, delete file).
	// Resource operations require the client capability `workspace.workspaceEdit.resourceOperations`
	// and are typically represented using different structs within this slice (e.g., CreateFile, RenameFile, DeleteFile).
	// For simplicity here, we only explicitly include `TextDocumentEdit`, which is the most common case.
	// A more complete implementation might use `[]interface{}` or custom marshalling.
	DocumentChanges []TextDocumentEdit `json:"documentChanges,omitempty"` // Simplified to focus on text edits

	// Optional metadata about the changes. Requires client capability
	// `workspace.workspaceEdit.changeAnnotationSupport`.
	// ChangeAnnotations map[string]ChangeAnnotation `json:"changeAnnotations,omitempty"` // Add if needed later
}

// // --- Placeholder definitions for completeness (if you need resource operations later) ---
//
// // CreateFile operation defined by LSP spec.
// type CreateFile struct {
// 	Kind string `json:"kind"` // always 'create'
// 	URI DocumentURI `json:"uri"`
// 	Options *CreateFileOptions `json:"options,omitempty"`
// 	AnnotationID *ChangeAnnotationIdentifier `json:"annotationId,omitempty"`
// }
// // RenameFile operation defined by LSP spec.
// type RenameFile struct {
// 	Kind string `json:"kind"` // always 'rename'
// 	OldURI DocumentURI `json:"oldUri"`
// 	NewURI DocumentURI `json:"newUri"`
// 	Options *RenameFileOptions `json:"options,omitempty"`
// 	AnnotationID *ChangeAnnotationIdentifier `json:"annotationId,omitempty"`
// }
// // DeleteFile operation defined by LSP spec.
// type DeleteFile struct {
// 	Kind string `json:"kind"` // always 'delete'
// 	URI DocumentURI `json:"uri"`
// 	Options *DeleteFileOptions `json:"options,omitempty"`
// 	AnnotationID *ChangeAnnotationIdentifier `json:"annotationId,omitempty"`
// }
// // Options for file operations (can be extended based on spec)
// type CreateFileOptions struct { Overwrite bool `json:"overwrite,omitempty"`; IgnoreIfExists bool `json:"ignoreIfExists,omitempty"` }
// type RenameFileOptions struct { Overwrite bool `json:"overwrite,omitempty"`; IgnoreIfExists bool `json:"ignoreIfExists,omitempty"` }
// type DeleteFileOptions struct { Recursive bool `json:"recursive,omitempty"`; IgnoreIfNotExists bool `json:"ignoreIfNotExists,omitempty"` }
// type ChangeAnnotationIdentifier string
// type ChangeAnnotation struct { // ... definition ... }
