package sse

import (
"io"
"strings"
"testing"
)

func TestNext_SingleEvent(t *testing.T) {
	stream := "data: hello\n\n"
	r := NewReader(strings.NewReader(stream))

	ev, err := r.Next()
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	if ev.Data != "hello" {
		t.Errorf("Data = %q, want %q", ev.Data, "hello")
	}
	if ev.Type != "" {
		t.Errorf("Type = %q, want empty", ev.Type)
	}
}

func TestNext_EventWithType(t *testing.T) {
	stream := "event: update\ndata: {\"v\":1}\n\n"
	r := NewReader(strings.NewReader(stream))

	ev, err := r.Next()
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	if ev.Type != "update" {
		t.Errorf("Type = %q, want %q", ev.Type, "update")
	}
	if ev.Data != `{"v":1}` {
		t.Errorf("Data = %q, want %q", ev.Data, `{"v":1}`)
	}
}

func TestNext_MultiLineData(t *testing.T) {
	stream := "data: line1\ndata: line2\ndata: line3\n\n"
	r := NewReader(strings.NewReader(stream))

	ev, err := r.Next()
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	want := "line1\nline2\nline3"
	if ev.Data != want {
		t.Errorf("Data = %q, want %q", ev.Data, want)
	}
}

func TestNext_MultipleEvents(t *testing.T) {
	stream := "data: first\n\ndata: second\n\n"
	r := NewReader(strings.NewReader(stream))

	ev1, err := r.Next()
	if err != nil {
		t.Fatalf("Next 1: %v", err)
	}
	if ev1.Data != "first" {
		t.Errorf("ev1.Data = %q, want %q", ev1.Data, "first")
	}

	ev2, err := r.Next()
	if err != nil {
		t.Fatalf("Next 2: %v", err)
	}
	if ev2.Data != "second" {
		t.Errorf("ev2.Data = %q, want %q", ev2.Data, "second")
	}
}

func TestNext_EOF(t *testing.T) {
	r := NewReader(strings.NewReader(""))

	_, err := r.Next()
	if err != io.EOF {
		t.Fatalf("got %v, want io.EOF", err)
	}
}

func TestNext_CommentLinesSkipped(t *testing.T) {
	stream := ": this is a comment\ndata: real\n\n"
	r := NewReader(strings.NewReader(stream))

	ev, err := r.Next()
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	if ev.Data != "real" {
		t.Errorf("Data = %q, want %q", ev.Data, "real")
	}
}

func TestNext_EmptyDataField(t *testing.T) {
	stream := "data:\n\n"
	r := NewReader(strings.NewReader(stream))

	ev, err := r.Next()
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	if ev.Data != "" {
		t.Errorf("Data = %q, want empty", ev.Data)
	}
}

func TestNext_NoSpaceAfterColon(t *testing.T) {
	stream := "data:no-space\n\n"
	r := NewReader(strings.NewReader(stream))

	ev, err := r.Next()
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	if ev.Data != "no-space" {
		t.Errorf("Data = %q, want %q", ev.Data, "no-space")
	}
}

func TestNext_UnknownFieldsIgnored(t *testing.T) {
	stream := "id: 123\nretry: 5000\ndata: value\n\n"
	r := NewReader(strings.NewReader(stream))

	ev, err := r.Next()
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	if ev.Data != "value" {
		t.Errorf("Data = %q, want %q", ev.Data, "value")
	}
}

func TestNext_TrailingDataWithoutNewline(t *testing.T) {
	// Stream ends with data but no trailing blank line
	stream := "data: orphan"
	r := NewReader(strings.NewReader(stream))

	ev, err := r.Next()
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	if ev.Data != "orphan" {
		t.Errorf("Data = %q, want %q", ev.Data, "orphan")
	}
}

func TestNext_MultipleBlankLines(t *testing.T) {
	stream := "\n\n\ndata: after-blanks\n\n"
	r := NewReader(strings.NewReader(stream))

	ev, err := r.Next()
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	if ev.Data != "after-blanks" {
		t.Errorf("Data = %q, want %q", ev.Data, "after-blanks")
	}
}
