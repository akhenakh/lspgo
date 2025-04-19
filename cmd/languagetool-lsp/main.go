package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/akhenakh/lspgo/protocol"
	"github.com/akhenakh/lspgo/server"
)

var (
	// Store open documents in memory
	// Key: Document URI, Value: Full document item including text and version
	documents = make(map[protocol.DocumentURI]protocol.TextDocumentItem)
	docMu     sync.RWMutex // Protects access to the documents map
)

func getEnv(key, fallback string) string {
	if value, ok := os.LookupEnv(key); ok {
		return value
	}
	return fallback
}

// offsetLengthToRange converts a byte offset and length within content
// to an LSP Range (0-based line and UTF-16 character).
// This is complex due to UTF-8 vs UTF-16 LSP positioning.
// We'll approximate using UTF-8 character counts for simplicity here.
// A production-ready version would need proper UTF-16 counting.
func offsetLengthToRange(content string, byteOffset, byteLength int) (protocol.Range, error) {
	if byteOffset < 0 || byteLength < 0 || byteOffset+byteLength > len(content) {
		return protocol.Range{}, fmt.Errorf("offset/length (%d, %d) out of bounds for content length %d", byteOffset, byteLength, len(content))
	}

	startLine, startChar := -1, -1
	endLine, endChar := -1, -1
	currentByteOffset := 0
	currentLine := 0
	currentCharInLine := 0 // Using rune count as proxy for UTF-16

	// Iterate through runes to handle multi-byte characters correctly
	for i, r := range content {
		// Found start position
		if startLine == -1 && currentByteOffset >= byteOffset {
			startLine = currentLine
			// Calculate character position *before* this rune
			lineStartByteOffset := currentByteOffset - i // Estimate start byte of current line (approx)
			if currentLine > 0 {
				// More accurate: find previous newline
				lastNewline := strings.LastIndex(content[:i], "\n")
				if lastNewline != -1 {
					lineStartByteOffset = lastNewline + 1
				} else {
					lineStartByteOffset = 0 // First line
				}
			}

			// Count runes from start of line to start offset
			lineContentBeforeOffset := content[lineStartByteOffset:byteOffset]
			startChar = utf8.RuneCountInString(lineContentBeforeOffset)

		}

		// Found end position (position *after* the last character of the match)
		if endLine == -1 && currentByteOffset >= byteOffset+byteLength {
			endLine = currentLine
			// Calculate character position *before* this rune
			lineStartByteOffset := currentByteOffset - i // Estimate start byte of current line (approx)
			if currentLine > 0 {
				// More accurate: find previous newline
				lastNewline := strings.LastIndex(content[:i], "\n")
				if lastNewline != -1 {
					lineStartByteOffset = lastNewline + 1
				} else {
					lineStartByteOffset = 0 // First line
				}
			}

			// Count runes from start of line to end offset
			lineContentBeforeEndOffset := content[lineStartByteOffset : byteOffset+byteLength]
			endChar = utf8.RuneCountInString(lineContentBeforeEndOffset)

			// Break early once end is found
			break
		}

		// Advance position counters
		runeSize := utf8.RuneLen(r)
		if r == '\n' {
			currentLine++
			currentCharInLine = 0
		} else {
			currentCharInLine++
		}
		currentByteOffset += runeSize
	}

	// Handle case where the match extends to the very end of the file
	if startLine != -1 && endLine == -1 && currentByteOffset == byteOffset+byteLength {
		endLine = currentLine
		// Count runes from start of line to end offset (which is end of content)
		lineStartByteOffset := 0
		if currentLine > 0 {
			lastNewline := strings.LastIndex(content, "\n")
			if lastNewline != -1 {
				lineStartByteOffset = lastNewline + 1
			}
		}
		lineContentBeforeEndOffset := content[lineStartByteOffset : byteOffset+byteLength]
		endChar = utf8.RuneCountInString(lineContentBeforeEndOffset)
	}

	if startLine == -1 || endLine == -1 {
		log.Printf("Failed to calculate range for offset=%d, length=%d. ContentLen=%d. Found: startL=%d, endL=%d", byteOffset, byteLength, len(content), startLine, endLine)
		// Fallback: return range covering the whole document or a specific line?
		// For now, return an error or a zero-range? Let's return an error.
		return protocol.Range{}, fmt.Errorf("failed to map offset/length (%d, %d) to line/character", byteOffset, byteLength)
	}

	return protocol.Range{
		Start: protocol.Position{Line: uint(startLine), Character: uint(startChar)},
		End:   protocol.Position{Line: uint(endLine), Character: uint(endChar)},
	}, nil
}

func main() {
	ctx := context.Background()
	logger := log.New(os.Stderr, "[languagetool-lsp] ", log.LstdFlags|log.Lshortfile)

	srv := server.NewServer(
		server.WithLogger(logger),
	)

	// Register handlers with signatures accepting the connection
	// (assuming the server framework supports this via reflection)
	mustRegister(srv, protocol.MethodTextDocumentDidOpen, handleDidOpen)
	mustRegister(srv, protocol.MethodTextDocumentDidChange, handleDidChange)
	// mustRegister(srv, protocol.MethodTextDocumentDidSave, handleDidSave) // Optional
	mustRegister(srv, protocol.MethodTextDocumentDidClose, handleDidClose)

	// The default handlers for initialize, shutdown, exit etc. are already
	// registered by server.NewServer(). We only need to add our specific ones.

	log.Println("Starting LanguageTool LSP server...")
	log.Printf("Using LanguageTool API URL: %s", languageToolURL)

	if err := srv.Run(ctx); err != nil {
		logger.Fatalf("Server error: %v", err)
	}
	logger.Println("Server stopped.")
}

func mustRegister(s *server.Server, method string, handler any) {
	if err := s.Register(method, handler); err != nil {
		log.Fatalf("Failed to register handler for %s: %v", method, err)
	}
}
