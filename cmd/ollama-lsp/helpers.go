package main

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/akhenakh/lspgo/protocol"
)

func getTextBeforePosition(content string, pos protocol.Position) string {
	lines := strings.SplitAfter(content, "\n")
	if int(pos.Line) >= len(lines) {
		return content
	}
	var builder strings.Builder
	for i := 0; i < int(pos.Line); i++ {
		builder.WriteString(lines[i])
	}
	lineContent := lines[pos.Line]
	charPos := int(pos.Character)
	if charPos > len(lineContent) {
		charPos = len(lineContent)
	}
	builder.WriteString(lineContent[:charPos])
	return builder.String()
}

func getTextInRange(content string, rng protocol.Range) (string, error) {
	lines := strings.Split(content, "\n")
	startLine, startChar := int(rng.Start.Line), int(rng.Start.Character)
	endLine, endChar := int(rng.End.Line), int(rng.End.Character)

	if startLine < 0 || startLine >= len(lines) || endLine < 0 || endLine >= len(lines) {
		return "", fmt.Errorf("invalid range: line numbers %d-%d out of bounds (0-%d)", startLine, endLine, len(lines)-1)
	}
	if startLine > endLine || (startLine == endLine && startChar > endChar) {
		return "", fmt.Errorf("invalid range: start %v is after end %v", rng.Start, rng.End)
	}
	startLineContent := lines[startLine]
	endLineContent := lines[endLine]
	if startChar > len(startLineContent) {
		startChar = len(startLineContent) // Clamp start char to end of line if needed
		// return "", fmt.Errorf("invalid range: start character %d out of bounds on line %d (len %d)", startChar, startLine, len(startLineContent))
	}
	if endChar > len(endLineContent) {
		endChar = len(endLineContent) // Clamp end char to end of line if needed
		// return "", fmt.Errorf("invalid range: end character %d out of bounds on line %d (len %d)", endChar, endLine, len(endLineContent))
	}
	// Re-check validity after clamping characters potentially reversed order on same line
	if startLine == endLine && startChar > endChar {
		return "", fmt.Errorf("invalid range: start char %d is after end char %d on the same line %d after clamping", startChar, endChar, startLine)
	}

	var builder strings.Builder
	if startLine == endLine {
		builder.WriteString(lines[startLine][startChar:endChar])
	} else {
		builder.WriteString(lines[startLine][startChar:])
		builder.WriteString("\n")
		for i := startLine + 1; i < endLine; i++ {
			builder.WriteString(lines[i])
			builder.WriteString("\n")
		}
		builder.WriteString(lines[endLine][:endChar])
	}
	return builder.String(), nil
}

func getCurrentLine(content string, lineNum uint) (string, error) {
	lines := strings.Split(content, "\n")
	if int(lineNum) >= len(lines) {
		return "", fmt.Errorf("line number %d is out of bounds (0-%d)", lineNum, len(lines)-1)
	}
	return lines[lineNum], nil
}

// addLineNumbers takes a block of text and prefixes each line with its number.
func addLineNumbers(text string) string {
	lines := strings.Split(text, "\n")
	var builder strings.Builder
	for i, line := range lines {
		// Don't add newline for the very last line if it's empty (common after split)
		if i == len(lines)-1 && line == "" {
			continue
		}
		builder.WriteString(strconv.Itoa(i))
		builder.WriteString(": ")
		builder.WriteString(line)
		// Add newline except for the last actual line of content
		if i < len(lines)-1 {
			// Check if the next line is the empty one from split, if so don't add newline yet
			isLastRealLine := (i == len(lines)-2 && lines[len(lines)-1] == "")
			if !isLastRealLine {
				builder.WriteString("\n")
			}
		}

	}
	return builder.String()
}

// createWorkspaceEdit simplifies the creation of a WorkspaceEdit with DocumentChanges.
func createWorkspaceEdit(uri protocol.DocumentURI, version int, edits []protocol.TextEdit) protocol.WorkspaceEdit {
	return protocol.WorkspaceEdit{
		DocumentChanges: []protocol.TextDocumentEdit{
			{
				TextDocument: protocol.VersionedTextDocumentIdentifier{
					TextDocumentIdentifier: protocol.TextDocumentIdentifier{URI: uri},
					Version:                version,
				},
				Edits: edits,
			},
		},
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
