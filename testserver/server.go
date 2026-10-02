package testserver

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

type Mode string

const (
	Normal            Mode = "normal"
	MalformedSSE      Mode = "malformed_sse"
	MalformedField    Mode = "malformed_field"
	PrematureClose    Mode = "premature_close"
	MissingCompletion Mode = "missing_completion"
	MissingDone       Mode = "missing_done"
	MissingFinish     Mode = "missing_finish"
	WrongContentType  Mode = "wrong_content_type"
)

type Config struct {
	Mode                                       Mode
	TTFBDelay, ChunkInterval, TerminationDelay time.Duration
	ModelsStatus, BasicStatus, StreamStatus    int
	StreamContentType                          string
	CaptureRequest                             func(map[string]any)
}

func Handler(cfg Config) http.Handler {
	if cfg.Mode == "" {
		cfg.Mode = Normal
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/models", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", 405)
			return
		}
		if cfg.ModelsStatus != 0 {
			http.Error(w, http.StatusText(cfg.ModelsStatus), cfg.ModelsStatus)
			return
		}
		writeJSON(w, map[string]any{"object": "list", "data": []map[string]any{{"id": "test-model", "object": "model", "owned_by": "compatprobe"}}})
	})
	mux.HandleFunc("/v1/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", 405)
			return
		}
		var payload map[string]any
		if json.NewDecoder(r.Body).Decode(&payload) != nil {
			http.Error(w, "invalid JSON", 400)
			return
		}
		if cfg.CaptureRequest != nil {
			cfg.CaptureRequest(payload)
		}
		reqStream, _ := payload["stream"].(bool)
		if !reqStream {
			if cfg.BasicStatus != 0 {
				http.Error(w, http.StatusText(cfg.BasicStatus), cfg.BasicStatus)
				return
			}
			writeJSON(w, map[string]any{"id": "chatcmpl-test", "object": "chat.completion", "choices": []any{map[string]any{"index": 0, "message": map[string]any{"role": "assistant", "content": "OK"}, "finish_reason": "stop"}}})
			return
		}
		if cfg.TTFBDelay > 0 {
			time.Sleep(cfg.TTFBDelay)
		}
		if cfg.StreamStatus != 0 {
			http.Error(w, http.StatusText(cfg.StreamStatus), cfg.StreamStatus)
			return
		}
		if cfg.Mode == WrongContentType {
			writeJSON(w, map[string]any{"choices": []any{map[string]any{"message": map[string]string{"content": "not streamed"}}}})
			return
		}
		contentType := cfg.StreamContentType
		if contentType == "" {
			contentType = "text/event-stream"
		}
		w.Header().Set("Content-Type", contentType)
		w.Header().Set("Cache-Control", "no-cache")
		flusher, _ := w.(http.Flusher)
		count := 3
		if cfg.Mode == PrematureClose {
			count = 10
		}
		for i := 1; i <= count; i++ {
			fmt.Fprintf(w, "data: {\"id\":\"chunk-%d\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"%d\"},\"finish_reason\":null}]}\n\n", i, i)
			if flusher != nil {
				flusher.Flush()
			}
			if cfg.ChunkInterval > 0 {
				time.Sleep(cfg.ChunkInterval)
			}
		}
		switch cfg.Mode {
		case MalformedSSE:
			fmt.Fprint(w, "data: not-json-one\n\ndata: not-json-two\n\n")
		case MalformedField:
			w.Write([]byte{'d', 'a', 't', 'a', ':', ' ', 0xff, '\n', '\n'})
		case PrematureClose:
			fmt.Fprint(w, "data: {\"truncated\":")
			if flusher != nil {
				flusher.Flush()
			}
		case MissingCompletion:
		case MissingDone:
			fmt.Fprint(w, "data: {\"id\":\"final\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n")
		case MissingFinish:
			fmt.Fprint(w, "data: [DONE]\n\n")
		default:
			fmt.Fprint(w, "data: {\"id\":\"final\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
		}
		if cfg.TerminationDelay > 0 {
			time.Sleep(cfg.TerminationDelay)
		}
	})
	return mux
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}
