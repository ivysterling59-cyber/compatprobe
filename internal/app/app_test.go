package app

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ivysterling59-cyber/compatprobe/testserver"
)

func TestCLICheckHelpIsSuccessfulAndDoesNotRunProbes(t *testing.T) {
	for _, flag := range []string{"--help", "-h"} {
		t.Run(flag, func(t *testing.T) {
			secret := "help-only-fake-credential"
			var stdout, stderr bytes.Buffer
			code := App{Stdout: &stdout, Stderr: &stderr}.Run([]string{"check", "--api-key", secret, "--header", "Cookie:" + secret, flag})
			if code != 0 || stderr.Len() != 0 {
				t.Fatalf("help code=%d stderr=%q", code, stderr.String())
			}
			for _, want := range []string{"compatprobe check", "-base-url", "-model", "-timeout", "shared by all probes", "-stream-tokens", "-json", "-output", "-verbose", "-header", "max_completion_tokens", "16-32768", "default 256", "COMPATPROBE_API_KEY"} {
				if !strings.Contains(stdout.String(), want) {
					t.Fatalf("help omitted %q: %s", want, stdout.String())
				}
			}
			if strings.Contains(stdout.String(), secret) {
				t.Fatal("help exposed a parsed credential")
			}
		})
	}
}

type progressBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (b *progressBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.Write(p)
}

func (b *progressBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.String()
}

func TestCLIVerboseReportsLiveProgressOnStderrOnly(t *testing.T) {
	var stdout bytes.Buffer
	var stderr progressBuffer
	seenBeforeResponse := make(chan bool, 1)
	handler := testserver.Handler(testserver.Config{})
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/models" {
			seenBeforeResponse <- strings.Contains(stderr.String(), "authentication_models START")
		}
		handler.ServeHTTP(w, r)
	}))
	defer s.Close()
	code := App{Version: "test", Stdout: &stdout, Stderr: &stderr}.Run([]string{"check", "--base-url", s.URL, "--model", "m", "--verbose", "--json"})
	if code != 0 || !json.Valid(stdout.Bytes()) {
		t.Fatalf("code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	if !<-seenBeforeResponse {
		t.Fatal("progress was not emitted before the request completed")
	}
	progress := stderr.String()
	previous := -1
	for _, want := range []string{"authentication_models START", "authentication_models PASS", "basic_chat_completion START", "basic_chat_completion PASS", "stream_completion START", "stream_completion PASS", "sse_framing PASS", "finish_semantics PASS"} {
		index := strings.Index(progress, want)
		if index <= previous {
			t.Fatalf("missing or out-of-order progress %q: %s", want, progress)
		}
		previous = index
	}
	if !strings.Contains(progress, "elapsed=") || !strings.Contains(progress, "duration=") || !strings.Contains(progress, "overall timeout=1m0s") {
		t.Fatalf("progress omitted actual timing or timeout budget: %s", progress)
	}
}

func TestCLIVerboseReportsSkippedProbesWithoutStartingThem(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var stdout, stderr bytes.Buffer
	code := App{Stdout: &stdout, Stderr: &stderr, Context: ctx}.Run([]string{"check", "--base-url", "http://127.0.0.1:1", "--model", "m", "--verbose", "--json"})
	if code != 3 || !json.Valid(stdout.Bytes()) {
		t.Fatalf("code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	for _, name := range []string{"basic_chat_completion", "stream_completion", "sse_framing", "finish_semantics"} {
		if !strings.Contains(stderr.String(), name+" SKIP") || strings.Contains(stderr.String(), name+" START") {
			t.Fatalf("skipped probe progress is misleading: %s", stderr.String())
		}
	}
}

func TestCLIJSONAndMarkdownDoNotLeakKey(t *testing.T) {
	secret := "arbitrary-api-credential"
	t.Setenv("COMPATPROBE_API_KEY", secret)
	s := httptest.NewServer(testserver.Handler(testserver.Config{}))
	defer s.Close()
	var stdout, stderr bytes.Buffer
	out := filepath.Join(t.TempDir(), "report.md")
	code := App{Version: "test", Stdout: &stdout, Stderr: &stderr}.Run([]string{"check", "--base-url", s.URL, "--model", "m", "--json", "--output", out})
	if code != 0 {
		t.Fatalf("code=%d stderr=%s", code, stderr.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("progress emitted without --verbose: %s", stderr.String())
	}
	md, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	combined := stdout.String() + stderr.String() + string(md)
	if strings.Contains(combined, secret) {
		t.Fatalf("key leaked: %s", combined)
	}
}

func TestCLIVerboseAndErrorPathsRedactSecrets(t *testing.T) {
	secret := "plain-custom-header-secret"
	s := httptest.NewServer(testserver.Handler(testserver.Config{BasicStatus: 401}))
	defer s.Close()
	var stdout, stderr bytes.Buffer
	code := App{Version: "test", Stdout: &stdout, Stderr: &stderr}.Run([]string{"check", "--base-url", s.URL, "--model", "m", "--api-key", secret, "--header", "Cookie:" + secret, "--verbose", "--json"})
	if code != 1 {
		t.Fatalf("code=%d stderr=%s", code, stderr.String())
	}
	if strings.Contains(stdout.String()+stderr.String(), secret) {
		t.Fatalf("secret leaked: stdout=%s stderr=%s", stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), "basic_chat_completion FAIL") || !json.Valid(stdout.Bytes()) {
		t.Fatalf("verbose failure status or JSON output incorrect: stdout=%s stderr=%s", stdout.String(), stderr.String())
	}
}

func TestCLIUsage(t *testing.T) {
	var out, err bytes.Buffer
	a := App{Version: "v", Stdout: &out, Stderr: &err}
	if code := a.Run([]string{"version"}); code != 0 || !strings.Contains(out.String(), "v") {
		t.Fatalf("version code=%d out=%q", code, out.String())
	}
	if code := a.Run([]string{"check", "--base-url", "bad", "--model", "m"}); code != 2 {
		t.Fatalf("usage code=%d", code)
	}
}

func TestCLIStreamTokensFlagParsed(t *testing.T) {
	captured := make(chan map[string]any, 1)
	s := httptest.NewServer(testserver.Handler(testserver.Config{CaptureRequest: func(payload map[string]any) {
		if stream, _ := payload["stream"].(bool); stream {
			captured <- payload
		}
	}}))
	defer s.Close()
	var stdout, stderr bytes.Buffer
	code := App{Version: "test", Stdout: &stdout, Stderr: &stderr}.Run([]string{"check", "--base-url", s.URL, "--model", "m", "--stream-tokens", "2048", "--json"})
	if code != 0 {
		t.Fatalf("code=%d stderr=%s", code, stderr.String())
	}
	payload := <-captured
	if payload["max_completion_tokens"] != float64(2048) {
		t.Fatalf("payload=%#v", payload)
	}
	if !bytes.Contains(stdout.Bytes(), []byte(`"requested_max_completion_tokens": 2048`)) {
		t.Fatalf("report missing completion-token budget: %s", stdout.String())
	}
}

func TestCLIMarkdownReproducesOverallTimeoutAndTokenBudget(t *testing.T) {
	s := httptest.NewServer(testserver.Handler(testserver.Config{}))
	defer s.Close()
	var stdout, stderr bytes.Buffer
	out := filepath.Join(t.TempDir(), "report.md")
	code := App{Version: "test", Stdout: &stdout, Stderr: &stderr}.Run([]string{"check", "--base-url", s.URL, "--model", "m", "--stream-tokens", "2048", "--timeout", "120s", "--json", "--output", out})
	if code != 0 {
		t.Fatalf("code=%d stderr=%s", code, stderr.String())
	}
	md, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Overall check timeout: `2m0s`", "--stream-tokens 2048 --timeout 2m0s"} {
		if !bytes.Contains(md, []byte(want)) {
			t.Fatalf("report omitted %q: %s", want, md)
		}
	}
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(stdout.Bytes(), &doc); err != nil {
		t.Fatal(err)
	}
	if len(doc) != 7 || string(doc["schema_version"]) != `"1"` {
		t.Fatalf("JSON schema changed: %s", stdout.String())
	}
}

func TestCLIRejectsInvalidStreamTokens(t *testing.T) {
	for _, value := range []string{"not-an-int", "-1", "0", "15", "32769"} {
		t.Run(value, func(t *testing.T) {
			var out, err bytes.Buffer
			code := App{Version: "test", Stdout: &out, Stderr: &err}.Run([]string{"check", "--base-url", "https://host", "--model", "m", "--stream-tokens", value})
			if code != 2 {
				t.Fatalf("code=%d", code)
			}
			if value != "not-an-int" && !strings.Contains(err.String(), "between 16 and 32768") {
				t.Fatalf("message=%q", err.String())
			}
		})
	}
}

func TestCLICancelsPromptly(t *testing.T) {
	started := make(chan struct{})
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { close(started); <-r.Context().Done() }))
	defer s.Close()
	ctx, cancel := context.WithCancel(context.Background())
	var stdout, stderr bytes.Buffer
	done := make(chan int, 1)
	go func() {
		done <- App{Version: "test", Stdout: &stdout, Stderr: &stderr, Context: ctx}.Run([]string{"check", "--base-url", s.URL, "--model", "m", "--timeout", "10s"})
	}()
	<-started
	cancel()
	select {
	case code := <-done:
		if code != 3 {
			t.Fatalf("code=%d output=%s", code, stdout.String())
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("CLI did not cancel promptly")
	}
}

func TestCLIWindowsStyleUnicodeOutputPath(t *testing.T) {
	s := httptest.NewServer(testserver.Handler(testserver.Config{}))
	defer s.Close()
	var stdout, stderr bytes.Buffer
	out := filepath.Join(t.TempDir(), "报告 with spaces.md")
	code := App{Version: "test", Stdout: &stdout, Stderr: &stderr}.Run([]string{"check", "--base-url", s.URL + "/", "--model", "模型 name", "--output", out})
	if code != 0 {
		t.Fatalf("code=%d stderr=%s", code, stderr.String())
	}
	if _, err := os.Stat(out); err != nil {
		t.Fatal(err)
	}
}

func TestCLIRejectsTrailingArgumentsAndRedactsParseErrors(t *testing.T) {
	secret := "plain-unstructured-secret"
	for name, args := range map[string][]string{"trailing": {"check", "--base-url", "https://host", "--model", "m", "extra"}, "parse": {"check", "--api-key", secret, "--timeout", "not-a-duration"}} {
		t.Run(name, func(t *testing.T) {
			var out, err bytes.Buffer
			code := App{Version: "test", Stdout: &out, Stderr: &err}.Run(args)
			if code != 2 {
				t.Fatalf("code=%d", code)
			}
			if strings.Contains(out.String()+err.String(), secret) {
				t.Fatalf("secret leaked: %s", err.String())
			}
		})
	}
}

func TestJSONSchemaTopLevelAndFactTypesAreStable(t *testing.T) {
	s := httptest.NewServer(testserver.Handler(testserver.Config{}))
	defer s.Close()
	var stdout, stderr bytes.Buffer
	code := App{Version: "test", Stdout: &stdout, Stderr: &stderr}.Run([]string{"check", "--base-url", s.URL, "--model", "m", "--json"})
	if code != 0 {
		t.Fatalf("code=%d stderr=%s", code, stderr.String())
	}
	var doc struct {
		SchemaVersion string            `json:"schema_version"`
		Metadata      json.RawMessage   `json:"metadata"`
		Target        json.RawMessage   `json:"target"`
		Environment   json.RawMessage   `json:"environment"`
		Timeline      []json.RawMessage `json:"timeline"`
		Probes        []struct {
			Name       string                     `json:"name"`
			Status     string                     `json:"status"`
			DurationMS int64                      `json:"duration_ms"`
			Facts      map[string]json.RawMessage `json:"facts"`
		} `json:"probes"`
		Diagnoses []json.RawMessage `json:"diagnoses"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &doc); err != nil {
		t.Fatal(err)
	}
	if doc.SchemaVersion != "1" || doc.Metadata == nil || doc.Target == nil || doc.Environment == nil || doc.Timeline == nil || len(doc.Probes) != 5 || doc.Diagnoses == nil {
		t.Fatalf("unstable schema: %#v", doc)
	}
	for _, p := range doc.Probes {
		if p.Name == "stream_completion" {
			for _, key := range []string{"requested_max_completion_tokens", "chunks", "stream_duration_ms", "clean_eof", "content_type"} {
				if _, ok := p.Facts[key]; !ok {
					t.Fatalf("missing stable fact %q", key)
				}
			}
		}
	}
}
