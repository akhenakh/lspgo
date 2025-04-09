package main

import (
	"context"
	"fmt"
	"log"

	"github.com/akhenakh/lspgo/protocol"
	"github.com/akhenakh/lspgo/server"
)

func main() {
	ctx := context.Background()
	// Could add cancellation: ctx, cancel := context.WithCancel(context.Background())
	// defer cancel()

	// Create server instance (defaults to stdin/stdout)
	lspServer := server.NewServer()

	// Register handlers for the methods your server supports
	// (beyond the built-in initialize, shutdown, exit)
	err := lspServer.Register("textDocument/didOpen", handleDidOpen)
	if err != nil {
		log.Fatalf("Failed to register didOpen handler: %v", err)
	}
	err = lspServer.Register("textDocument/didChange", handleDidChange)
	if err != nil {
		log.Fatalf("Failed to register didChange handler: %v", err)
	}
	err = lspServer.Register("textDocument/hover", handleHover)
	if err != nil {
		log.Fatalf("Failed to register hover handler: %v", err)
	}
	// Add more handlers: completion, definition, diagnostics etc.

	log.Println("Starting LSP server...")
	// Run the server loop
	if err := lspServer.Run(ctx); err != nil {
		log.Fatalf("Server error: %v", err)
	}
	log.Println("Server stopped.")
}

// Example Handlers (Implement your actual logic here)

// handleDidOpen processes textDocument/didOpen notifications.
// The signature matches server.HandlerFunc indirectly via reflection.
// It expects context and the specific parameter type.
func handleDidOpen(ctx context.Context, params *protocol.DidOpenTextDocumentParams) error {
	log.Printf("Document Opened: %s (Version %d, Lang %s)", params.TextDocument.URI, params.TextDocument.Version, params.TextDocument.LanguageID)
	// TODO: Store document content, parse it, etc.
	// Example: Store in a map[protocol.DocumentURI]string protected by a mutex
	return nil // Notifications don't return results
}

// handleDidChange processes textDocument/didChange notifications.
func handleDidChange(ctx context.Context, params *protocol.DidChangeTextDocumentParams) error {
	log.Printf("Document Changed: %s (Version %d)", params.TextDocument.URI, params.TextDocument.Version)
	for _, change := range params.ContentChanges {
		// If change.Range is nil, it's a full document update
		if change.Range == nil {
			log.Printf("  Full content change (%d chars)", len(change.Text))
			// TODO: Update stored document content with change.Text
		} else {
			log.Printf("  Incremental change @ %v-%v: %q", change.Range.Start, change.Range.End, change.Text)
			// TODO: Apply incremental change to stored document content (more complex)
		}
	}
	// TODO: Re-parse/analyze the document after changes
	// Maybe trigger diagnostics: server.Notify(ctx, "textDocument/publishDiagnostics", ...)
	return nil
}

// handleHover processes textDocument/hover requests.
// It returns a result (*protocol.Hover) and an error.
func handleHover(ctx context.Context, params *protocol.HoverParams) (*protocol.Hover, error) {
	log.Printf("Hover Request: %s at (%d, %d)", params.TextDocument.URI, params.Position.Line, params.Position.Character)

	// --- Dummy Implementation ---
	// TODO: Replace with actual logic:
	// 1. Get document content for params.TextDocument.URI
	// 2. Find the token/symbol at params.Position
	// 3. Look up information about that symbol
	// 4. Format the information as Markdown or PlainText

	// Example: Return fixed hover content
	content := protocol.MarkupContent{
		Kind: protocol.Markdown, // Or protocol.PlainText
		Value: fmt.Sprintf("## Hover Info\n\nDocument: `%s`\nPosition: Line %d, Char %d\n\n*Provide real information here!*",
			params.TextDocument.URI,
			params.Position.Line,
			params.Position.Character),
	}

	// Determine the range of the symbol being hovered over (optional but good)
	// Dummy range for example:
	hoverRange := protocol.Range{
		Start: params.Position,
		End:   protocol.Position{Line: params.Position.Line, Character: params.Position.Character + 5}, // Example range
	}

	return &protocol.Hover{
		Contents: content,
		Range:    &hoverRange, // Optional: The range this hover applies to
	}, nil // No error
}
