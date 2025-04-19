package protocol

import (
	"context"
	"encoding/json"
	"log"

	"github.com/akhenakh/lspgo/jsonrpc2"
)

func ShowNotification(ctx context.Context, conn *jsonrpc2.Conn, msgType MessageType, message string) {
	if conn == nil {
		log.Printf("Warning: Attempted to show notification with nil connection: %s", message)
		return
	}
	params := ShowMessageParams{
		Type:    msgType,
		Message: message,
	}
	// Use the server's Notify method if available and preferred, otherwise manual send.
	// For now, stick to manual send for direct comparison.
	rawParams, err := json.Marshal(params)
	if err != nil {
		log.Printf("Error marshalling showMessage params: %v", err)
		return
	}
	notification := &jsonrpc2.NotificationMessage{
		JSONRPC: jsonrpc2.Version,
		Method:  MethodWindowShowMessage,
		Params:  rawParams,
	}
	log.Printf("<-- Notification: Method=%s, Type=%d, Message=%s",
		notification.Method, msgType, message)
	if err := conn.Write(ctx, notification); err != nil {
		log.Printf("Error sending showMessage notification: %v", err)
	}
}

// SendDiagnostics sends diagnostics to the client.
func SendDiagnostics(ctx context.Context, conn *jsonrpc2.Conn, uri DocumentURI, diagnostics []Diagnostic) {
	if conn == nil {
		log.Printf("Warning: Attempted to send diagnostics with nil connection for URI: %s", uri)
		return
	}

	// Clear previous diagnostics for this file by sending an empty list if needed?
	// LSP typically expects the server to send the *full* set of current diagnostics.
	// If the API call fails, maybe we should clear? Or leave stale ones? Let's send what we have.

	params := PublishDiagnosticsParams{
		URI:         uri,
		Diagnostics: diagnostics,
		// Optionally include version if client supports it and it helps avoid race conditions
		// Version: docVersion, // Need to pass docVersion down or retrieve it here
	}

	rawParams, err := json.Marshal(params)
	if err != nil {
		log.Printf("Error marshalling diagnostics params for %s: %v", uri, err)
		return
	}

	notification := &jsonrpc2.NotificationMessage{
		JSONRPC: jsonrpc2.Version,
		Method:  MethodTextDocumentPublishDiagnostics,
		Params:  rawParams,
	}

	log.Printf("<-- Notification: Method=%s, URI=%s, Diagnostics=%d",
		notification.Method, uri, len(diagnostics))
	if err := conn.Write(ctx, notification); err != nil {
		log.Printf("Error sending diagnostics notification for %s: %v", uri, err)
	}
}
