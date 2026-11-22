// Package sse provides a lightweight Server-Sent Events (SSE) reader.
package sse

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"strings"
)

// MaxEventBytes is the maximum size of the joined data of a single event.
const MaxEventBytes = 1 << 20

// ErrEventTooLarge is returned when an event (or a single line) exceeds the size limit.
// The Reader must not be used after it returns this error.
var ErrEventTooLarge = errors.New("sse: event too large")

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
	size := 0

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
			size += len(value)
			if len(data) > 0 {
				size++
			}
			if size > MaxEventBytes {
				return Event{}, ErrEventTooLarge
			}
			data = append(data, value)
		}
	}

	if err := r.scanner.Err(); err != nil {
		if errors.Is(err, bufio.ErrTooLong) {
			return Event{}, fmt.Errorf("%w: %w", ErrEventTooLarge, err)
		}
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
