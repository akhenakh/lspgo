package main

import (
	"context"
	"log"
	"sync"
	"time"

	"github.com/akhenakh/lspgo/jsonrpc2"
	"github.com/akhenakh/lspgo/protocol"
)

// Debounce logic
var (
	debounceTimers = make(map[protocol.DocumentURI]*time.Timer)
	debounceMu     sync.Mutex
	debounceDelay  = 500 * time.Millisecond // Adjust as needed
)

// handleDidOpen stores the document and triggers an initial check.
func handleDidOpen(ctx context.Context, conn *jsonrpc2.Conn, params *protocol.DidOpenTextDocumentParams) error {
	docMu.Lock()
	docItem := params.TextDocument // Store the full item
	documents[params.TextDocument.URI] = docItem
	docMu.Unlock()
	log.Printf("Document Opened: %s (Version: %d, LangID: %s)", docItem.URI, docItem.Version, docItem.LanguageID)

	// Trigger initial check asynchronously
	go checkDocumentAndSendDiagnostics(context.Background(), conn, docItem) // Use background context for async task
	return nil
}

// handleDidChange updates the document and triggers a debounced check.
func handleDidChange(ctx context.Context, conn *jsonrpc2.Conn, params *protocol.DidChangeTextDocumentParams) error {
	if len(params.ContentChanges) == 0 {
		return nil
	}
	// Assuming full sync (capability TextDocumentSyncKind.Full)
	fullText := params.ContentChanges[0].Text

	docMu.Lock()
	item, ok := documents[params.TextDocument.URI]
	if !ok {
		// Should not happen if didOpen was received, but handle defensively
		item = protocol.TextDocumentItem{
			URI:        params.TextDocument.URI,
			Version:    params.TextDocument.Version,
			Text:       fullText,
			LanguageID: "", // We don't get LanguageID in didChange
		}
		log.Printf("Document Changed: %s (Version %d) - Created new entry", params.TextDocument.URI, params.TextDocument.Version)
	} else {
		item.Version = params.TextDocument.Version
		item.Text = fullText
		log.Printf("Document Changed: %s (Version %d) - Updated existing", params.TextDocument.URI, params.TextDocument.Version)
	}
	documents[params.TextDocument.URI] = item
	currentDocItem := item // Capture current state for debounce closure
	docMu.Unlock()

	// --- Debounce Logic ---
	debounceMu.Lock()
	uri := params.TextDocument.URI
	if timer, exists := debounceTimers[uri]; exists {
		timer.Stop() // Cancel previous timer
	}
	// Create a new timer
	debounceTimers[uri] = time.AfterFunc(debounceDelay, func() {
		log.Printf("Debounce timer fired for %s", uri)
		// Remove timer from map *before* running check to avoid race if check is fast
		debounceMu.Lock()
		delete(debounceTimers, uri)
		debounceMu.Unlock()

		// Re-fetch the *latest* document state in case of rapid changes
		// Although currentDocItem *might* be slightly stale, it's often good enough
		// and avoids complex locking across the async call.
		// For max accuracy, re-fetch:
		// docMu.RLock()
		// latestDocItem, latestOk := documents[uri]
		// docMu.RUnlock()
		// if latestOk { go checkDocumentAndSendDiagnostics(context.Background(), conn, latestDocItem) }

		// Simpler: Use the state captured when the timer was set.
		go checkDocumentAndSendDiagnostics(context.Background(), conn, currentDocItem)

	})
	debounceMu.Unlock()
	// --- End Debounce Logic ---

	return nil
}

// handleDidSave could optionally trigger a check if desired (e.g., if didChange is too frequent).
// func handleDidSave(ctx context.Context, conn *jsonrpc2.Conn, params *protocol.DidSaveTextDocumentParams) error {
// 	docMu.RLock()
// 	docItem, ok := documents[params.TextDocument.URI]
// 	docMu.RUnlock()
// 	if !ok {
// 		log.Printf("Document Saved: %s - Not found in memory", params.TextDocument.URI)
// 		return nil
// 	}
//  log.Printf("Document Saved: %s", params.TextDocument.URI)
// 	// Optionally trigger check on save
// 	go checkDocumentAndSendDiagnostics(context.Background(), conn, docItem)
// 	return nil
// }

// handleDidClose removes the document from memory.
func handleDidClose(ctx context.Context, conn *jsonrpc2.Conn, params *protocol.DidCloseTextDocumentParams) error {
	uri := params.TextDocument.URI
	docMu.Lock()
	delete(documents, uri)
	docMu.Unlock()

	// Cancel any pending debounce timer for this document
	debounceMu.Lock()
	if timer, exists := debounceTimers[uri]; exists {
		timer.Stop()
		delete(debounceTimers, uri)
	}
	debounceMu.Unlock()

	log.Printf("Document Closed: %s", uri)

	// Clear diagnostics for the closed file
	go protocol.SendDiagnostics(context.Background(), conn, uri, []protocol.Diagnostic{})

	return nil
}
