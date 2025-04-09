package protocol

import "encoding/json"

// ClientInfo information about the client.
type ClientInfo struct {
	Name    string `json:"name"`
	Version string `json:"version,omitempty"`
}

// InitializeParams parameters for the initialize request.
type InitializeParams struct {
	ProcessID             *int               `json:"processId,omitempty"` // Pointer to allow null
	ClientInfo            *ClientInfo        `json:"clientInfo,omitempty"`
	RootURI               *DocumentURI       `json:"rootUri,omitempty"` // Can be null
	InitializationOptions json.RawMessage    `json:"initializationOptions,omitempty"`
	Capabilities          ClientCapabilities `json:"capabilities"`
	Trace                 string             `json:"trace,omitempty"` // off, messages, verbose
	WorkspaceFolders      []WorkspaceFolder  `json:"workspaceFolders,omitempty"`
}

// WorkspaceFolder information.
type WorkspaceFolder struct {
	URI  string `json:"uri"`
	Name string `json:"name"`
}

// ClientCapabilities defines the capabilities provided by the client.
// NOTE: This is heavily truncated for brevity. A real implementation needs
// many more fields based on the LSP spec.
type ClientCapabilities struct {
	Workspace    *WorkspaceClientCapabilities    `json:"workspace,omitempty"`
	TextDocument *TextDocumentClientCapabilities `json:"textDocument,omitempty"`
	// Experimental features can be added here using json.RawMessage or specific structs
}

// WorkspaceClientCapabilities workspace specific client capabilities.
type WorkspaceClientCapabilities struct {
	ApplyEdit bool `json:"applyEdit,omitempty"`
	// ... many more fields (didChangeConfiguration, workspaceFolders, etc.)
}

// TextDocumentClientCapabilities text document specific client capabilities.
// NOTE: Truncated. Add capabilities like completion, hover, definition etc. as needed.
type TextDocumentClientCapabilities struct {
	Synchronization *TextDocumentSyncClientCapabilities `json:"synchronization,omitempty"`
	Completion      *CompletionClientCapabilities       `json:"completion,omitempty"`
	Hover           *HoverClientCapabilities            `json:"hover,omitempty"`
	// ... many more fields (definition, references, formatting, etc.)
}

// TextDocumentSyncClientCapabilities capabilities for text document synchronization.
type TextDocumentSyncClientCapabilities struct {
	DidSave bool `json:"didSave,omitempty"` // Notify on save
}

// CompletionClientCapabilities capabilities specific to completion requests.
type CompletionClientCapabilities struct {
	DynamicRegistration bool `json:"dynamicRegistration,omitempty"`
	CompletionItem      *struct {
		SnippetSupport bool `json:"snippetSupport,omitempty"`
	} `json:"completionItem,omitempty"`
	// ... many more fields
}

// HoverClientCapabilities capabilities specific to hover requests.
type HoverClientCapabilities struct {
	DynamicRegistration bool         `json:"dynamicRegistration,omitempty"`
	ContentFormat       []MarkupKind `json:"contentFormat,omitempty"`
}

// MarkupKind describes the content type that a client supports in various
// result literals like `Hover`, `ParameterInformation` or `CompletionItem`.
type MarkupKind string

const (
	PlainText MarkupKind = "plaintext"
	Markdown  MarkupKind = "markdown"
)

// InitializeResult result of the initialize request.
type InitializeResult struct {
	Capabilities ServerCapabilities `json:"capabilities"`
	ServerInfo   *ServerInfo        `json:"serverInfo,omitempty"`
}

// ServerInfo information about the server.
type ServerInfo struct {
	Name    string `json:"name"`
	Version string `json:"version,omitempty"`
}

// ServerCapabilities defines the capabilities provided by the server.
// NOTE: This is heavily truncated. Fill based on features implemented.
type ServerCapabilities struct {
	TextDocumentSync   *TextDocumentSyncOptions `json:"textDocumentSync,omitempty"` // Can be options or number
	CompletionProvider *CompletionOptions       `json:"completionProvider,omitempty"`
	HoverProvider      *HoverOptions            `json:"hoverProvider,omitempty"`      // Can be bool or options
	DefinitionProvider *DefinitionOptions       `json:"definitionProvider,omitempty"` // Can be bool or options
	// ... many more capabilities (references, formatting, codeAction, etc.)
}

// TextDocumentSyncOptions defines how text documents are synced.
type TextDocumentSyncOptions struct {
	OpenClose bool                 `json:"openClose,omitempty"` // DidOpen/DidClose notifications
	Change    TextDocumentSyncKind `json:"change,omitempty"`    // Kind of change notifications
	// WillSave, WillSaveWaitUntil, Save options...
}

// TextDocumentSyncKind defines the type of sync notifications.
type TextDocumentSyncKind int // Use int; LSP spec uses numbers 0, 1, 2

const (
	// None documents should not be synced at all.
	SyncNone TextDocumentSyncKind = 0
	// Full documents are synced by sending the full content on change.
	SyncFull TextDocumentSyncKind = 1
	// Incremental documents are synced by sending incremental changes.
	SyncIncremental TextDocumentSyncKind = 2
)

// CompletionOptions server options for completion requests.
type CompletionOptions struct {
	ResolveProvider   bool     `json:"resolveProvider,omitempty"` // Server resolves additional info on demand
	TriggerCharacters []string `json:"triggerCharacters,omitempty"`
}

// WorkDoneProgressOptions options for work done progress reporting.
type WorkDoneProgressOptions struct {
	WorkDoneProgress bool `json:"workDoneProgress,omitempty"`
}

// DefinitionOptions server options for definition requests.
type DefinitionOptions struct {
	WorkDoneProgressOptions
}

// InitializedParams parameters for the initialized notification. Empty struct.
type InitializedParams struct{}

// LogMessageParams parameters for window/logMessage notification.
type LogMessageParams struct {
	Type    MessageType `json:"type"`
	Message string      `json:"message"`
}

// MessageType for log messages (error, warning, info, log).
type MessageType int

const (
	Error   MessageType = 1
	Warning MessageType = 2
	Info    MessageType = 3
	Log     MessageType = 4
)

// ShowMessageParams parameters for window/showMessage notification.
type ShowMessageParams struct {
	Type    MessageType `json:"type"`
	Message string      `json:"message"`
}

// ShowMessageRequestParams parameters for window/showMessageRequest request.
type ShowMessageRequestParams struct {
	Type    MessageType         `json:"type"`
	Message string              `json:"message"`
	Actions []MessageActionItem `json:"actions,omitempty"`
}

// MessageActionItem used in ShowMessageRequestParams.
type MessageActionItem struct {
	Title string `json:"title"`
}

// ShutdownParams parameters for the shutdown request. Empty struct.
type ShutdownParams struct{}

// ExitParams parameters for the exit notification. Empty struct.
type ExitParams struct{}
