package openai

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"strconv"
	"strings"
	"unicode/utf8"
)

type SSEEvent struct {
	Event string
	Data  string
	ID    string
	Retry *int
}

type SSEParser struct {
	r        *bufio.Reader
	maxLine  int
	maxEvent int
}

func NewSSEParser(r io.Reader) *SSEParser {
	return &SSEParser{r: bufio.NewReader(r), maxLine: 1024 * 1024, maxEvent: 4 * 1024 * 1024}
}

func (p *SSEParser) Next() (SSEEvent, error) {
	var ev SSEEvent
	var data []string
	hasField := false
	for {
		line, eof, err := p.readLine()
		if err != nil {
			if errors.Is(err, io.EOF) && hasField {
				ev.Data = strings.Join(data, "\n")
				return ev, io.ErrUnexpectedEOF
			}
			return ev, err
		}
		if !utf8.ValidString(line) {
			return ev, errors.New("SSE stream contains invalid UTF-8")
		}
		if line == "" {
			if hasField {
				ev.Data = strings.Join(data, "\n")
				return ev, nil
			}
			if eof {
				return ev, io.EOF
			}
			continue
		}
		if strings.HasPrefix(line, ":") {
			if eof {
				return ev, io.EOF
			}
			continue
		}
		field, value, found := strings.Cut(line, ":")
		if !found {
			field, value = line, ""
		}
		if strings.HasPrefix(value, " ") {
			value = value[1:]
		}
		switch field {
		case "data":
			if dataSize(data)+len(value) > p.maxEvent {
				return ev, errors.New("SSE event exceeds 4 MiB")
			}
			data = append(data, value)
			hasField = true
		case "event":
			ev.Event = value
			hasField = true
		case "id":
			if !strings.ContainsRune(value, '\x00') {
				ev.ID = value
			}
			hasField = true
		case "retry":
			n, e := strconv.Atoi(value)
			if e == nil && n >= 0 {
				ev.Retry = &n
				hasField = true
			}
		default:
			// The SSE specification requires clients to ignore unknown fields.
		}
		if eof {
			if hasField {
				ev.Data = strings.Join(data, "\n")
				return ev, io.ErrUnexpectedEOF
			}
			return ev, io.EOF
		}
	}
}

func dataSize(lines []string) int {
	size := len(lines)
	for _, line := range lines {
		size += len(line)
	}
	return size
}

func (p *SSEParser) readLine() (string, bool, error) {
	var line []byte
	for {
		part, err := p.r.ReadSlice('\n')
		if len(line)+len(part) > p.maxLine+2 {
			return "", false, errors.New("SSE line exceeds 1 MiB")
		}
		line = append(line, part...)
		switch {
		case err == nil:
			line = bytes.TrimSuffix(line, []byte{'\n'})
			line = bytes.TrimSuffix(line, []byte{'\r'})
			return string(line), false, nil
		case errors.Is(err, bufio.ErrBufferFull):
			continue
		case errors.Is(err, io.EOF):
			if len(line) == 0 {
				return "", true, io.EOF
			}
			line = bytes.TrimSuffix(line, []byte{'\r'})
			return string(line), true, nil
		default:
			return "", false, err
		}
	}
}
