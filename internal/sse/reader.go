// Package sse provides a lightweight Server-Sent Events (SSE) reader.
package sse

import (
	"bufio"
	"io"
	"strings"
)

// Event represents a single SSE event.
type Event struct {
	Type string
	Data string
}

// Reader reads SSE events from an io.Reader.
type Reader struct {
	scanner *bufio.Scanner
}

// NewReader creates a new SSE stream reader.
func NewReader(r io.Reader) *Reader {
	return &Reader{scanner: bufio.NewScanner(r)}
}

// Next reads the next SSE event from the stream.
// Returns io.EOF when the stream ends.
func (r *Reader) Next() (Event, error) {
	var eventType string
	var data []string

	for r.scanner.Scan() {
		line := r.scanner.Text()

		// Empty line signals end of an event
		if line == "" {
			if len(data) > 0 {
				return Event{
					Type: eventType,
					Data: strings.Join(data, "\n"),
				}, nil
			}
			continue
		}

		if strings.HasPrefix(line, ":") {
			// Comment line — skip
			continue
		}

		field, value, _ := strings.Cut(line, ":")
		// Trim a single leading space from value per SSE spec
		value = strings.TrimPrefix(value, " ")

		switch field {
		case "event":
			eventType = value
		case "data":
			data = append(data, value)
		}
	}

	if err := r.scanner.Err(); err != nil {
		return Event{}, err
	}

	// If we have accumulated data when stream ends, return it before EOF
	if len(data) > 0 {
		return Event{
			Type: eventType,
			Data: strings.Join(data, "\n"),
		}, nil
	}

	return Event{}, io.EOF
}
