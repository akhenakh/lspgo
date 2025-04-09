package protocol

import "encoding/json"

// CodeActionParams parameters for textDocument/codeAction request.
type CodeActionParams struct {
	// The document in which the command was invoked.
	TextDocument TextDocumentIdentifier `json:"textDocument"`
	// The range for which the command was invoked.
	Range Range `json:"range"`
	// Context carrying additional information.
	Context CodeActionContext `json:"context"`
	// WorkDoneProgressParams // Optional for progress reporting
	// PartialResultParams // Optional for partial results
}

// CodeActionContext contains additional diagnostic information about the context in which
// a code action is run.
type CodeActionContext struct {
	// An array of diagnostics known on the client side overlapping the range.
	Diagnostics []Diagnostic `json:"diagnostics"`
	// Requested kinds of code actions to return. Actions not of this kind are filtered out by the client before being shown.
	// Empty array means return all actions.
	Only []CodeActionKind `json:"only,omitempty"`
	// The reason why code actions were requested.
	// Since LSP 3.17.0
	TriggerKind *CodeActionTriggerKind `json:"triggerKind,omitempty"`
}

// CodeActionTriggerKind how a code action was triggered.
// Since LSP 3.17.0
type CodeActionTriggerKind int

const (
	// Invoked manually by the user or by a command.
	CodeActionTriggerKindInvoked CodeActionTriggerKind = 1
	// Invoked automatically due to location changes.
	CodeActionTriggerKindAutomatic CodeActionTriggerKind = 2
)

// CodeActionKind defines kinds of code actions.
type CodeActionKind string

// Predefined CodeActionKinds
const (
	// Empty kind. Used to symbolize intermediate result literals.
	Empty CodeActionKind = ""
	// Base kind for quickfix actions: 'quickfix'.
	QuickFix CodeActionKind = "quickfix"
	// Base kind for refactoring actions: 'refactor'.
	Refactor CodeActionKind = "refactor"
	// Base kind for refactoring extraction actions: 'refactor.extract'.
	RefactorExtract CodeActionKind = "refactor.extract"
	// Base kind for refactoring inline actions: 'refactor.inline'.
	RefactorInline CodeActionKind = "refactor.inline"
	// Base kind for refactoring rewrite actions: 'refactor.rewrite'.
	RefactorRewrite CodeActionKind = "refactor.rewrite"
	// Base kind for source actions: `source`.
	Source CodeActionKind = "source"
	// Base kind for optimizing imports actions: `source.organizeImports`.
	SourceOrganizeImports CodeActionKind = "source.organizeImports"
	// Base kind for auto-fixing source actions: `source.fixAll`.
	SourceFixAll CodeActionKind = "source.fixAll"
	// Base kind for adding missing imports actions: `source.addMissingImports`. (LSP Extension)
	// SourceAddMissingImports CodeActionKind = "source.addMissingImports" // Example extension
)

// CodeAction represents a potential change that can be applied to a document.
// The result of a `textDocument/codeAction` request is an array of `Command` or `CodeAction` objects.
type CodeAction struct {
	// A short, human-readable, title for this code action.
	Title string `json:"title"`
	// The kind of the code action. Used to filter code actions.
	Kind CodeActionKind `json:"kind,omitempty"`
	// The diagnostics that this code action resolves.
	Diagnostics []Diagnostic `json:"diagnostics,omitempty"`
	// Marks this as a preferred action. Preferred actions are used by the `auto fix` command and can be targeted
	// by keybindings. A client should only mark one action per diagnostic source as `preferred`.
	IsPreferred bool `json:"isPreferred,omitempty"`
	// Marks that the code action cannot normally be applied because it is disabled.
	Disabled *CodeActionDisabled `json:"disabled,omitempty"`
	// The workspace edit this code action performs.
	Edit *WorkspaceEdit `json:"edit,omitempty"`
	// A command this code action executes. If a code action provides an edit and a command, first the edit is
	// executed and then the command.
	Command *Command `json:"command,omitempty"`
	// A data entry field that is preserved between a `textDocument/codeAction` and a `codeAction/resolve` request.
	// Since LSP 3.16.0
	Data json.RawMessage `json:"data,omitempty"`
}

// CodeActionDisabled marks a code action as disabled.
// Since LSP 3.16.0
type CodeActionDisabled struct {
	// Human readable reason why this code action is currently disabled.
	Reason string `json:"reason"`
}

// Command represents a reference to a command. Provides a title which will be used to represent a command in the UI.
// Commands are identified by a string identifier. The protocol defines specific commands like `applyWorkspaceEdit` and `showReferences`.
// Servers and clients can also define custom commands.
type Command struct {
	// Title of the command, like `save`.
	Title string `json:"title"`
	// The identifier of the actual command handler.
	Command string `json:"command"`
	// Arguments that the command handler should be invoked with.
	Arguments []json.RawMessage `json:"arguments,omitempty"` // Use RawMessage for flexibility
}

// CodeActionOptions defines server capabilities for CodeAction.
// It's referenced in ServerCapabilities in general.go
type CodeActionOptions struct {
	WorkDoneProgressOptions
	// CodeActionKinds that this server may return.
	// The list of kinds may be generic, such as `CodeActionKind.Refactor`, or the server
	// may list out every specific kind they provide.
	CodeActionKinds []CodeActionKind `json:"codeActionKinds,omitempty"`
	// The server provides support to resolve additional information for a code action.
	// Since LSP 3.16.0
	ResolveProvider bool `json:"resolveProvider,omitempty"`
}

// CodeActionRegistrationOptions options for dynamically registering code action support.
// type CodeActionRegistrationOptions struct {
// 	TextDocumentRegistrationOptions
// 	CodeActionOptions
// }
