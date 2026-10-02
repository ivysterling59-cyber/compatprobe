package openai

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"
)

func TestSSEParser(t *testing.T) {
	input := ": keepalive\r\nid: 7\r\nevent: message\r\nretry: 1000\r\ndata: {\"a\":\r\ndata: 1}\r\n\r\ndata: [DONE]\n\n"
	p := NewSSEParser(strings.NewReader(input))
	ev, err := p.Next()
	if err != nil {
		t.Fatal(err)
	}
	if ev.ID != "7" || ev.Event != "message" || ev.Data != "{\"a\":\n1}" || ev.Retry == nil || *ev.Retry != 1000 {
		t.Fatalf("event = %#v", ev)
	}
	ev, err = p.Next()
	if err != nil || ev.Data != "[DONE]" {
		t.Fatalf("done event = %#v, %v", ev, err)
	}
	if _, err = p.Next(); !errors.Is(err, io.EOF) {
		t.Fatalf("want EOF, got %v", err)
	}
}

func TestSSEMalformedAndTruncated(t *testing.T) {
	ev, err := NewSSEParser(strings.NewReader("data: partial")).Next()
	if ev.Data != "partial" || !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("got %#v, %v", ev, err)
	}
}

func TestSSEIgnoresInvalidRetry(t *testing.T) {
	ev, err := NewSSEParser(strings.NewReader("retry: nope\ndata: ok\n\n")).Next()
	if err != nil || ev.Data != "ok" || ev.Retry != nil {
		t.Fatalf("event=%#v err=%v", ev, err)
	}
}

func TestSSERejectsInvalidUTF8(t *testing.T) {
	_, err := NewSSEParser(bytes.NewReader([]byte{'d', 'a', 't', 'a', ':', ' ', 0xff, '\n', '\n'})).Next()
	if err == nil || !strings.Contains(err.Error(), "invalid UTF-8") {
		t.Fatalf("err=%v", err)
	}
}

func TestSSEIgnoresUnknownField(t *testing.T) {
	p := NewSSEParser(strings.NewReader("extension: value\ndata: ok\n\n"))
	ev, err := p.Next()
	if err != nil || ev.Data != "ok" {
		t.Fatalf("event=%#v err=%v", ev, err)
	}
}

func TestSSEBoundaryCases(t *testing.T) {
	tests := []struct{ name, input, want string }{
		{"LF empty lines and comments", "\n: one\n: two\n\ndata: ok\n\n", "ok"},
		{"CRLF", ": comment\r\n\r\ndata: ok\r\n\r\n", "ok"},
		{"UTF-8", "data: 你好，世界🌍\n\n", "你好，世界🌍"},
		{"multi-line", "data: first\ndata:\ndata: third\n\n", "first\n\nthird"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ev, err := NewSSEParser(strings.NewReader(tt.input)).Next()
			if err != nil || ev.Data != tt.want {
				t.Fatalf("event=%#v err=%v", ev, err)
			}
		})
	}
}

func TestSSELargeEventBeyondScannerLimit(t *testing.T) {
	data := strings.Repeat("x", 128<<10)
	ev, err := NewSSEParser(strings.NewReader("data: " + data + "\n\n")).Next()
	if err != nil || ev.Data != data {
		t.Fatalf("len=%d err=%v", len(ev.Data), err)
	}
}

func TestSSERejectsOverOneMiBLine(t *testing.T) {
	_, err := NewSSEParser(strings.NewReader("data: " + strings.Repeat("x", (1<<20)+1) + "\n\n")).Next()
	if err == nil || !strings.Contains(err.Error(), "exceeds 1 MiB") {
		t.Fatalf("err=%v", err)
	}
}

func TestSSERejectsOverFourMiBMultiLineEvent(t *testing.T) {
	line := strings.Repeat("x", 512<<10)
	var b strings.Builder
	for i := 0; i < 9; i++ {
		b.WriteString("data: ")
		b.WriteString(line)
		b.WriteByte('\n')
	}
	_, err := NewSSEParser(strings.NewReader(b.String())).Next()
	if err == nil || !strings.Contains(err.Error(), "event exceeds 4 MiB") {
		t.Fatalf("err=%v", err)
	}
}

func TestSSETruncatedFinalEventCRLF(t *testing.T) {
	ev, err := NewSSEParser(strings.NewReader("data: 最后一个事件\r\n")).Next()
	if ev.Data != "最后一个事件" || !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("event=%#v err=%v", ev, err)
	}
}
