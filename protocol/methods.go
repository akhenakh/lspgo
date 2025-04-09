package protocol

// Defines constants for common LSP method names.

const (
	// Text Document Synchronization
	MethodTextDocumentDidOpen   = "textDocument/didOpen"
	MethodTextDocumentDidChange = "textDocument/didChange"
	MethodTextDocumentDidSave   = "textDocument/didSave"
	MethodTextDocumentDidClose  = "textDocument/didClose"

	// Language Features
	MethodTextDocumentHover      = "textDocument/hover"
	MethodTextDocumentCompletion = "textDocument/completion"
	MethodCompletionItemResolve  = "completionItem/resolve"
	MethodTextDocumentDefinition = "textDocument/definition"
	MethodTextDocumentCodeAction = "textDocument/codeAction"
	MethodCodeActionResolve      = "codeAction/resolve"
	// Add other language features as needed... (e.g., references, rename, formatting)

	// Workspace Features
	MethodWorkspaceExecuteCommand = "workspace/executeCommand"
	MethodWorkspaceApplyEdit      = "workspace/applyEdit"

	// Add other workspace features as needed... (e.g., didChangeConfiguration, workspaceFolders)

	// Window Features
	MethodWindowShowMessage        = "window/showMessage"
	MethodWindowShowMessageRequest = "window/showMessageRequest"
	MethodWindowLogMessage         = "window/logMessage"

	// Diagnostics
	MethodTextDocumentPublishDiagnostics = "textDocument/publishDiagnostics"

	// General Lifecycle
	MethodInitialize    = "initialize"
	MethodInitialized   = "initialized"
	MethodShutdown      = "shutdown"
	MethodExit          = "exit"
	MethodCancelRequest = "$/cancelRequest" // Notification to cancel a request
	MethodProgress      = "$/progress"      // Notification for progress updates
)
