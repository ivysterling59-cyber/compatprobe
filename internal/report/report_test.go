package report

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/ivysterling59-cyber/compatprobe/internal/core"
)

func TestTerminalIncludesStreamTerminationEvidence(t *testing.T) {
	for _, tc := range []struct {
		name     string
		cleanEOF bool
		done     bool
		err      string
	}{
		{"completed", true, true, ""},
		{"deadline after finish_reason", false, false, "context deadline exceeded"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := New("test", "https://example.test/v1", "m", nil, []core.ProbeResult{
				{Name: "stream_completion", Facts: map[string]any{"requested_max_completion_tokens": 256, "http_status": 200, "content_type": "text/event-stream; charset=utf-8", "stream_duration_ms": 37, "chunks": 275, "clean_eof": tc.cleanEOF, "transport_error": tc.err}},
				{Name: "sse_framing", Facts: map[string]any{"valid_sse_frames": 273, "invalid_sse_frames": 0}},
				{Name: "finish_semantics", Facts: map[string]any{"finish_reason_seen": true, "done_seen": tc.done}},
			}, nil)
			r.CheckTimeout = 120 * time.Second
			termination := tc.err
			if termination == "" {
				termination = "none"
			}
			out := string(Terminal(r))
			for _, want := range []string{"HTTP status: 200", "Content-Type: text/event-stream; charset=utf-8", "Observed duration: 37 ms", "Observed SSE events/chunks: 275", "Valid SSE frames: 273", "Invalid SSE frames: 0", "finish_reason seen: true", fmt.Sprintf("[DONE] seen: %t", tc.done), fmt.Sprintf("Clean EOF: %t", tc.cleanEOF), "Transport error: " + termination, "Overall check timeout: 2m0s (shared by all probes)"} {
				if !strings.Contains(out, want) {
					t.Fatalf("terminal omitted %q: %s", want, out)
				}
			}
		})
	}
}

func TestTerminalMissingFactsAreNotInvented(t *testing.T) {
	r := New("test", "https://example.test/v1", "m", nil, []core.ProbeResult{{Name: "stream_completion", Status: core.Skip, Facts: map[string]any{"chunks": nil}}}, nil)
	out := string(Terminal(r))
	for _, label := range []string{"HTTP status", "Observed duration", "Observed SSE events/chunks", "Valid SSE frames", "Invalid SSE frames", "finish_reason seen", "[DONE] seen", "Clean EOF", "Transport error"} {
		if !strings.Contains(out, label+": not observed") {
			t.Fatalf("terminal omitted missing fact %q: %s", label, out)
		}
	}
	if strings.Contains(out, "<nil>") || strings.Contains(out, ": false") || strings.Contains(out, ": 0") {
		t.Fatalf("terminal invented observations: %s", out)
	}
}

func sample(secret string) Report {
	return New("test", "https://example.test/v1?token="+secret, "model", []core.TimelineEvent{{Name: "request_error", Timestamp: time.Millisecond, Metadata: map[string]string{"error": "Bearer " + secret}}}, []core.ProbeResult{{Name: "basic_chat_completion", Status: core.Fail, Duration: time.Second, Error: &core.ProbeError{Kind: "transport", Message: "Authorization: Bearer " + secret}}}, []core.Diagnosis{{Code: "TEST", Confidence: core.High, Summary: "failed for " + secret}}, secret)
}
func TestReportsAreGeneratedAndRedacted(t *testing.T) {
	secret := "arbitrary-api-credential"
	r := sample(secret)
	for name, out := range map[string][]byte{"markdown": Markdown(r), "terminal": Terminal(r)} {
		if bytes.Contains(out, []byte(secret)) {
			t.Fatalf("%s leaked secret: %s", name, out)
		}
	}
	j, err := JSON(r)
	if err != nil {
		t.Fatal(err)
	}
	if !json.Valid(j) {
		t.Fatalf("invalid JSON: %s", j)
	}
	if bytes.Contains(j, []byte(secret)) {
		t.Fatalf("JSON leaked secret: %s", j)
	}
}

func TestReportsRedactHeadersBodiesURLAndAllOutputPaths(t *testing.T) {
	secrets := []string{"plain-api-key", "authorization-value", "cookie-value", "set-cookie-value", "query-token", "body-token", "quoted\"slash\\newline\nsecret"}
	r := New("test", "https://user:pass@example.test/v1?access_token=query-token&region=cn", "model", []core.TimelineEvent{{Name: "response_headers", Metadata: map[string]string{"Authorization": "authorization-value", "Cookie": "cookie-value", "Set-Cookie": "set-cookie-value", "body": "body-token"}}}, []core.ProbeResult{{Name: "stream_completion", Status: core.Fail, Facts: map[string]any{"content_type": "text/event-stream", "response_body": "body-token", "transport_error": "transport failed: body-token"}, Error: &core.ProbeError{Kind: "authentication", Message: "Bearer authorization-value"}}}, nil, secrets...)
	j, err := JSON(r)
	if err != nil {
		t.Fatal(err)
	}
	outputs := map[string][]byte{"json": j, "markdown": Markdown(r), "terminal": Terminal(r)}
	for name, out := range outputs {
		for _, secret := range append(secrets, "user:pass") {
			if bytes.Contains(out, []byte(secret)) {
				t.Fatalf("%s leaked %q: %s", name, secret, out)
			}
			encoded, _ := json.Marshal(secret)
			if len(encoded) > 2 && bytes.Contains(out, encoded[1:len(encoded)-1]) {
				t.Fatalf("%s leaked JSON-escaped %q: %s", name, secret, out)
			}
		}
	}
}

func TestReportsSeparateCompletionTokenBudgetFromObservedDuration(t *testing.T) {
	r := New("test", "https://example.test/v1", "m", nil, []core.ProbeResult{{Name: "stream_completion", Status: core.Pass, Duration: 37 * time.Millisecond, Facts: map[string]any{"requested_max_completion_tokens": 2048, "stream_duration_ms": 37, "chunks": 12}}}, nil)
	for name, out := range map[string][]byte{"markdown": Markdown(r), "terminal": Terminal(r)} {
		if !bytes.Contains(out, []byte("2048")) || !bytes.Contains(out, []byte("37")) {
			t.Fatalf("%s omitted completion-token budget or observed duration: %s", name, out)
		}
	}
	jsonOut, err := JSON(r)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(jsonOut, []byte(`"requested_max_completion_tokens": 2048`)) || !bytes.Contains(jsonOut, []byte(`"stream_duration_ms": 37`)) {
		t.Fatalf("JSON conflated completion-token budget and duration: %s", jsonOut)
	}
}

func TestMarkdownIncludesStreamTerminationEvidence(t *testing.T) {
	for _, tc := range []struct {
		name     string
		cleanEOF bool
		done     bool
		err      string
	}{
		{"completed", true, true, ""},
		{"deadline after finish_reason", false, false, "context deadline exceeded"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := New("test", "https://example.test/v1", "m", nil, []core.ProbeResult{
				{Name: "stream_completion", Facts: map[string]any{"http_status": 200, "content_type": "text/event-stream; charset=utf-8", "clean_eof": tc.cleanEOF, "transport_error": tc.err}},
				{Name: "sse_framing", Facts: map[string]any{"valid_sse_frames": 273, "invalid_sse_frames": 0}},
				{Name: "finish_semantics", Facts: map[string]any{"finish_reason_seen": true, "done_seen": tc.done}},
			}, nil)
			termination := tc.err
			if termination == "" {
				termination = "none"
			}
			for _, want := range []string{
				"HTTP status: `200`", "Content-Type: `text/event-stream; charset=utf-8`",
				"Valid SSE frames: `273`", "Invalid SSE frames: `0`",
				"finish_reason seen: `true`", fmt.Sprintf("[DONE] seen: `%t`", tc.done),
				fmt.Sprintf("Clean EOF: `%t`", tc.cleanEOF), "Transport error: `" + termination + "`",
			} {
				if !bytes.Contains(Markdown(r), []byte(want)) {
					t.Fatalf("report omitted %q: %s", want, Markdown(r))
				}
			}
		})
	}
}

func TestMarkdownSkippedStreamDoesNotInventObservations(t *testing.T) {
	r := New("test", "https://example.test/v1", "m", nil, []core.ProbeResult{{Name: "stream_completion", Status: core.Skip}}, nil)
	out := Markdown(r)
	for _, want := range []string{"Clean EOF: not observed", "[DONE] seen: not observed", "Transport error: not observed"} {
		if !bytes.Contains(out, []byte(want)) {
			t.Fatalf("missing %q: %s", want, out)
		}
	}
	if bytes.Contains(out, []byte("<nil>")) || bytes.Contains(out, []byte("`false`")) {
		t.Fatalf("skipped stream reported invented facts: %s", out)
	}
}

func TestMarkdownReproductionQuotesArgumentsAndOmitsSecrets(t *testing.T) {
	for _, tc := range []struct {
		os, quotedModel string
	}{
		{"windows", "'model''s $name `literal`'"},
		{"linux", "'model'\"'\"'s $name `literal`'"},
	} {
		t.Run(tc.os, func(t *testing.T) {
			r := New("test", "https://user:password@example.test/v1", "model's $name `literal`", nil, []core.ProbeResult{{Name: "stream_completion", Facts: map[string]any{"requested_max_completion_tokens": 2048}}}, nil, "password")
			r.Environment.OS = tc.os
			out := Markdown(r)
			want := "compatprobe check --base-url 'https://example.test/v1' --model " + tc.quotedModel + " --stream-tokens 2048"
			if !bytes.Contains(out, []byte(want)) || bytes.Contains(out, []byte("password")) || bytes.Contains(out, []byte("--api-key")) {
				t.Fatalf("unsafe or incomplete reproduction: %s", out)
			}
		})
	}
}
