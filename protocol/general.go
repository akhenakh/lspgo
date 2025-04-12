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
	// Window       *WindowClientCapabilities       `json:"window,omitempty"` // Added window capabilities
	// Experimental features can be added here using json.RawMessage or specific structs
}

// WorkspaceClientCapabilities workspace specific client capabilities.
type WorkspaceClientCapabilities struct {
	ApplyEdit bool `json:"applyEdit,omitempty"`
	// WorkspaceEdit *WorkspaceEditClientCapabilities `json:"workspaceEdit,omitempty"` // Added workspace edit capabilities
	// ... many more fields (didChangeConfiguration, workspaceFolders, etc.)
}

// TextDocumentClientCapabilities text document specific client capabilities.
// NOTE: Truncated. Add capabilities like completion, hover, definition etc. as needed.
type TextDocumentClientCapabilities struct {
	Synchronization *TextDocumentSyncClientCapabilities `json:"synchronization,omitempty"`
	Completion      *CompletionClientCapabilities       `json:"completion,omitempty"`
	Hover           *HoverClientCapabilities            `json:"hover,omitempty"`
	// Definition      *DefinitionClientCapabilities     `json:"definition,omitempty"` // Added definition capabilities placeholder
	CodeAction *CodeActionClientCapabilities `json:"codeAction,omitempty"` // <<< ADDED
	// ... many more fields (references, formatting, etc.)
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

// --- DefinitionClientCapabilities placeholder (can be expanded) ---
// type DefinitionClientCapabilities struct {
// 	DynamicRegistration bool `json:"dynamicRegistration,omitempty"`
// 	LinkSupport         bool `json:"linkSupport,omitempty"`
// }

// --- CodeActionClientCapabilities --- START ADDED SECTION ---

// CodeActionClientCapabilities capabilities specific to the `textDocument/codeAction` request.
type CodeActionClientCapabilities struct {
	// Whether code action supports dynamic registration.
	DynamicRegistration bool `json:"dynamicRegistration,omitempty"`
	// The client support code action literals of type CodeAction
	// object as a valid response of the `textDocument/codeAction` request.
	// Since LSP 3.8.0
	CodeActionLiteralSupport *CodeActionLiteralSupport `json:"codeActionLiteralSupport,omitempty"`
	// Whether the client supports `resolve` for code actions.
	// Since LSP 3.16.0
	ResolveSupport *CodeActionResolveSupport `json:"resolveSupport,omitempty"`
	// Whether code action supports the `isPreferred` property.
	// Since LSP 3.15.0
	IsPreferredSupport bool `json:"isPreferredSupport,omitempty"`
	// Whether code action supports the `disabled` property.
	// Since LSP 3.16.0
	DisabledSupport bool `json:"disabledSupport,omitempty"`
	// Whether the client supports data binding resolve for code actions.
	// Deprecated: Use `resolveSupport` instead.
	// DataSupport bool `json:"dataSupport,omitempty"`

	// Whether the client honors the change annotations in text edits and resource operations
	// returned via the `CodeAction#edit` property by the server.
	// Since LSP 3.16.0
	// HonorsChangeAnnotations bool `json:"honorsChangeAnnotations,omitempty"` // Could add if needed
}

// CodeActionLiteralSupport defines the code action kinds that the client supports for literals.
type CodeActionLiteralSupport struct {
	// The code action kind is supported with the following value set.
	CodeActionKind CodeActionKindCapability `json:"codeActionKind"`
}

// CodeActionKindCapability defines the supported CodeActionKinds.
type CodeActionKindCapability struct {
	// The code action kind values the client supports. When this
	// property exists the client also guarantees that it will
	// handle values outside its set gracefully and falls back
	// to a default value when unknown.
	ValueSet []CodeActionKind `json:"valueSet"`
}

// CodeActionResolveSupport defines the properties that a client can resolve lazily.
// Since LSP 3.16.0
type CodeActionResolveSupport struct {
	// The properties that a client can resolve lazily.
	Properties []string `json:"properties"` // e.g., ["edit", "command"]
}

// --- CodeActionClientCapabilities --- END ADDED SECTION ---

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
type ServerCapabilities struct {
	TextDocumentSync       *TextDocumentSyncOptions `json:"textDocumentSync,omitempty"` // Can be options or number
	CompletionProvider     *CompletionOptions       `json:"completionProvider,omitempty"`
	HoverProvider          *HoverOptions            `json:"hoverProvider,omitempty"`          // Can be bool or options
	DefinitionProvider     *DefinitionOptions       `json:"definitionProvider,omitempty"`     // Can be bool or options
	CodeActionProvider     *CodeActionOptions       `json:"codeActionProvider,omitempty"`     // Can be bool | CodeActionOptions
	ExecuteCommandProvider *ExecuteCommandOptions   `json:"executeCommandProvider,omitempty"` // Added this field
	// ... many more capabilities (references, formatting, codeAction, etc.)
}

// TextDocumentSyncOptions defines how text documents are synced.
type TextDocumentSyncOptions struct {
	OpenClose bool                 `json:"openClose,omitempty"` // DidOpen/DidClose notifications
	Change    TextDocumentSyncKind `json:"change,omitempty"`    // Kind of change notifications
	Save      *SaveOptions         `json:"save,omitempty"`      // Added this field
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

type SaveOptions struct {
	IncludeText bool `json:"includeText,omitempty"` // The client should include the document text in save notifications
}

// --- ExecuteCommandOptions placeholder ---
// Usually needed if CodeActions return Commands
// type ExecuteCommandOptions struct {
// 	WorkDoneProgressOptions
// 	Commands []string `json:"commands"` // List of command identifiers supported by the server
// }

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

// ExecuteCommandParams parameters for the workspace/executeCommand request.
type ExecuteCommandParams struct {
	// The identifier of the actual command handler.
	Command string `json:"command"`
	// Arguments that the command handler should be invoked with.
	Arguments []json.RawMessage `json:"arguments,omitempty"` // Use RawMessage for flexibility
	// WorkDoneProgressParams // Optional for progress reporting
}

// --- ExecuteCommandOptions placeholder ---
// Usually needed if CodeActions return Commands
type ExecuteCommandOptions struct {
	WorkDoneProgressOptions
	Commands []string `json:"commands"` // List of command identifiers supported by the server
}

// CancelParams parameters for the $/cancelRequest notification.
type CancelParams struct {
	// The request id to cancel.
	ID json.RawMessage `json:"id"` // number | string
}

// ProgressParams parameters for the $/progress notification.
type ProgressParams struct {
	// The progress token provided by the server.
	Token ProgressToken `json:"token"`
	// The progress data.
	Value json.RawMessage `json:"value"` // Type depends on the progress reporting kind
}

// ProgressToken is either a string or int.
type ProgressToken interface{} // Can use interface{} or define specific types if needed

// WorkDoneProgressBegin defines the start of a work done progress.
type WorkDoneProgressBegin struct {
	Kind string `json:"kind"` // always 'begin'
	// Mandatory title of the progress operation.
	Title string `json:"title"`
	// Controls if a cancel button should show to allow the user to cancel the
	// long running operation. Clients that don't support cancellation are allowed
	// to ignore the setting.
	Cancellable bool `json:"cancellable,omitempty"`
	// Optional progress percentage on start.
	Percentage *uint `json:"percentage,omitempty"` // Use pointer for optional 0
	// Optional, more detailed associated progress message.
	Message *string `json:"message,omitempty"`
}

// WorkDoneProgressReport defines updates for a work done progress.
type WorkDoneProgressReport struct {
	Kind string `json:"kind"` // always 'report'
	// Controls enablement state of a cancel button. This property is only valid if a cancel
	// button got requested in the `WorkDoneProgressBegin` payload.
	// Clients that don't support cancellation are allowed to ignore the setting.
	Cancellable *bool `json:"cancellable,omitempty"`
	// Optional progress percentage.
	Percentage *uint `json:"percentage,omitempty"`
	// Optional, more detailed associated progress message.
	Message *string `json:"message,omitempty"`
}

// WorkDoneProgressEnd defines the end of a work done progress.
type WorkDoneProgressEnd struct {
	Kind string `json:"kind"` // always 'end'
	// Optional, a final message indicating completion.
	Message *string `json:"message,omitempty"`
}

// ApplyWorkspaceEditParams parameters for `workspace/applyEdit` request.
type ApplyWorkspaceEditParams struct {
	// The edits to apply.
	Edit WorkspaceEdit `json:"edit"`
	// An optional label of the edit. This label is displayed in the user interface
	// for example as the undo label.
	Label string `json:"label,omitempty"`
}

// ApplyWorkspaceEditResponse result for `workspace/applyEdit` request.
type ApplyWorkspaceEditResponse struct {
	// Indicates whether the edit was applied or not.
	Applied bool `json:"applied"`
	// An optional textual description for why the edit was not applied.
	// This may be used by the server for diagnostic logging or to provide
	// a suitable error to the user.
	FailureReason string `json:"failureReason,omitempty"`

	// Depending on the client's capability `workspace.workspaceEdit.failedChange`
	// the client might return this field instead of `failureReason`.
	// The field is only valid if `applied` is `false`.
	// Since LSP 3.16.0
	FailedChange *uint32 `json:"failedChange,omitempty"`
}
