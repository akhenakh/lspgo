package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/akhenakh/lspgo/jsonrpc2"
	"github.com/akhenakh/lspgo/protocol"
	"github.com/akhenakh/lspgo/server"
)

var (
	ollamaBaseURL = getEnv("OLLAMA_HOST", "http://localhost:11434")
	ollamaModel   = getEnv("OLLAMA_MODEL", "qwen2.5-coder:latest") // Make sure this model is pulled in Ollama
	ollamaTimeout = 30 * time.Second
)

func getEnv(key, fallback string) string {
	if value, ok := os.LookupEnv(key); ok {
		return value
	}
	return fallback
}

var (
	documents     = make(map[protocol.DocumentURI]protocol.TextDocumentItem)
	nextRequestID atomic.Int64 // Counter for outgoing request IDs
	docMu         sync.RWMutex
)

func main() {
	ctx := context.Background()
	// Example: Configure logger format
	logger := log.New(os.Stderr, "[ollama-lsp] ", log.LstdFlags|log.Lshortfile)

	lspServer := server.NewServer(server.WithLogger(logger))

	// Register handlers
	mustRegister(lspServer, "textDocument/didOpen", handleDidOpen)
	mustRegister(lspServer, "textDocument/didChange", handleDidChange)
	mustRegister(lspServer, "textDocument/didClose", handleDidClose) // Good practice
	mustRegister(lspServer, "textDocument/codeAction", handleCodeAction)
	mustRegister(lspServer, "workspace/executeCommand", handleExecuteCommand)

	log.Println("Starting Ollama LSP server...")
	log.Printf("Using Ollama URL: %s, Model: %s", ollamaBaseURL, ollamaModel)

	if err := lspServer.Run(ctx); err != nil {
		logger.Fatalf("Server error: %v", err)
	}
	logger.Println("Server stopped.")
}

func mustRegister(s *server.Server, method string, handler any) {
	if err := s.Register(method, handler); err != nil {
		log.Fatalf("Failed to register handler for %s: %v", method, err)
	}
}

// Function to get next request ID
func getNextRequestID() json.RawMessage {
	id := nextRequestID.Add(1)
	// JSON-RPC IDs can be numbers or strings. Let's use strings for safety.
	return json.RawMessage(strconv.FormatInt(id, 10))
}

// Define a structure for parsing the JSON response from Ollama for explanations
type ExplanationItem struct {
	LineNumber  int    `json:"line"`
	Explanation string `json:"explanation"`
}

type ExplanationResponse struct {
	Explanations []ExplanationItem `json:"explanations"`
}

// sendApplyEditRequest sends the workspace/applyEdit request to the client.
func sendApplyEditRequest(ctx context.Context, conn *jsonrpc2.Conn, label string, edit protocol.WorkspaceEdit) error {
	applyParams := protocol.ApplyWorkspaceEditParams{
		Label: label, // Undo label
		Edit:  edit,
	}

	rawParams, err := json.Marshal(applyParams)
	if err != nil {
		return fmt.Errorf("failed to marshal applyEdit params: %w", err)
	}

	request := &jsonrpc2.RequestMessage{
		JSONRPC: jsonrpc2.Version,
		ID:      getNextRequestID(), // Generate a unique ID for the request
		Method:  protocol.MethodWorkspaceApplyEdit,
		Params:  rawParams,
	}

	log.Printf("<-- Request (to client): Method=%s, ID=%s, Label=%s", request.Method, string(request.ID), label)
	if err := conn.Write(ctx, request); err != nil {
		return fmt.Errorf("failed to send workspace/applyEdit request: %w", err)
	}
	// Note: We are *not* waiting for the client's response here.
	return nil
}
