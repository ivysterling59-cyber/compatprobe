package probe

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/ivysterling59-cyber/compatprobe/internal/config"
	"github.com/ivysterling59-cyber/compatprobe/internal/core"
	"github.com/ivysterling59-cyber/compatprobe/internal/transport"
	"github.com/ivysterling59-cyber/compatprobe/testserver"
)

func runMode(t *testing.T, mode testserver.Mode) RunResult {
	t.Helper()
	s := httptest.NewServer(testserver.Handler(testserver.Config{Mode: mode}))
	defer s.Close()
	target := config.Target{BaseURL: s.URL, Model: "test-model", Timeout: time.Second}
	if err := target.Normalize(); err != nil {
		t.Fatal(err)
	}
	return New(target).Run(context.Background())
}

func TestNormalStreaming(t *testing.T) {
	r := runMode(t, testserver.Normal)
	for _, p := range r.Probes {
		if p.Status != core.Pass {
			t.Fatalf("%s = %s: %#v", p.Name, p.Status, p)
		}
	}
	if len(r.Timeline) == 0 {
		t.Fatal("missing timeline")
	}
	for i := 1; i < len(r.Timeline); i++ {
		if r.Timeline[i].Timestamp < r.Timeline[i-1].Timestamp {
			t.Fatalf("timeline is not chronological: %#v", r.Timeline)
		}
	}
}

func TestProgressMatchesObservedResultsOnEveryRunPath(t *testing.T) {
	for _, tc := range []struct {
		name        string
		config      testserver.Config
		cancelProbe string
	}{
		{name: "normal", config: testserver.Config{TTFBDelay: 20 * time.Millisecond}},
		{name: "warning", config: testserver.Config{Mode: testserver.MissingDone}},
		{name: "HTTP failure", config: testserver.Config{BasicStatus: http.StatusUnauthorized}},
		{name: "models cancellation", cancelProbe: "authentication_models"},
		{name: "basic cancellation", cancelProbe: "basic_chat_completion"},
		{name: "stream cancellation", cancelProbe: "stream_completion"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := httptest.NewServer(testserver.Handler(tc.config))
			defer s.Close()
			target := config.Target{BaseURL: s.URL, Model: "m", Timeout: time.Second}
			if err := target.Normalize(); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var events []Progress
			runner := New(target)
			runner.OnProgress = func(event Progress) {
				events = append(events, event)
				if event.Status == "" && event.Name == tc.cancelProbe {
					cancel()
				}
			}
			result := runner.Run(ctx)
			var completed []Progress
			started := make(map[string]bool)
			for _, event := range events {
				if event.Status == "" {
					if started[event.Name] {
						t.Fatalf("duplicate start: %#v", events)
					}
					started[event.Name] = true
				} else {
					completed = append(completed, event)
				}
			}
			if len(completed) != len(result.Probes) {
				t.Fatalf("incomplete final progress: %#v", events)
			}
			for i, p := range result.Probes {
				if got := completed[i]; got.Name != p.Name || got.Status != p.Status || got.Duration != p.Duration {
					t.Fatalf("progress differs from observed result: %#v versus %#v", got, p)
				}
				if p.Status == core.Skip && started[p.Name] {
					t.Fatalf("skipped probe was announced as started: %#v", events)
				}
			}
			if tc.name == "normal" && find(result.Probes, "stream_completion").Duration < 15*time.Millisecond {
				t.Fatal("progress duration did not include observed response latency")
			}
		})
	}
}

func TestSlowStreamRecordsDelays(t *testing.T) {
	s := httptest.NewServer(testserver.Handler(testserver.Config{Mode: testserver.Normal, TTFBDelay: 20 * time.Millisecond, ChunkInterval: 5 * time.Millisecond}))
	defer s.Close()
	target := config.Target{BaseURL: s.URL, Model: "m", Timeout: time.Second}
	if err := target.Normalize(); err != nil {
		t.Fatal(err)
	}
	stream, _, _, _ := New(target).stream(context.Background())
	if stream.Facts["time_to_first_event_ms"].(int64) < 15 {
		t.Fatalf("delay not observed: %#v", stream.Facts)
	}
}

func TestBrokenStreamingModes(t *testing.T) {
	tests := []struct {
		mode   testserver.Mode
		probe  string
		status core.Status
	}{{testserver.MalformedSSE, "sse_framing", core.Fail}, {testserver.MalformedField, "sse_framing", core.Fail}, {testserver.PrematureClose, "stream_completion", core.Fail}, {testserver.MissingCompletion, "finish_semantics", core.Warn}, {testserver.WrongContentType, "stream_completion", core.Fail}}
	for _, tt := range tests {
		t.Run(string(tt.mode), func(t *testing.T) {
			r := runMode(t, tt.mode)
			if got := find(r.Probes, tt.probe).Status; got != tt.status {
				t.Fatalf("%s = %s, want %s", tt.probe, got, tt.status)
			}
		})
	}
}

func TestMissingCompletionMarkersAreFactsNotFatal(t *testing.T) {
	for _, mode := range []testserver.Mode{testserver.MissingCompletion, testserver.MissingDone, testserver.MissingFinish} {
		t.Run(string(mode), func(t *testing.T) {
			r := runMode(t, mode)
			stream := find(r.Probes, "stream_completion")
			finish := find(r.Probes, "finish_semantics")
			if stream.Status == core.Fail {
				t.Fatalf("missing marker was fatal: %#v", stream)
			}
			if finish.Status != core.Warn {
				t.Fatalf("finish semantics=%s", finish.Status)
			}
		})
	}
}

func TestRunTimeoutIsOverallBudget(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { time.Sleep(100 * time.Millisecond) }))
	defer s.Close()
	target := config.Target{BaseURL: s.URL, Model: "m", Timeout: 20 * time.Millisecond}
	_ = target.Normalize()
	start := time.Now()
	result := New(target).Run(context.Background())
	if elapsed := time.Since(start); elapsed > 150*time.Millisecond {
		t.Fatalf("overall timeout took %s", elapsed)
	}
	if find(result.Probes, "basic_chat_completion").Status != core.Skip {
		t.Fatal("requests continued after overall timeout")
	}
}
func find(ps []core.ProbeResult, name string) core.ProbeResult {
	for _, p := range ps {
		if p.Name == name {
			return p
		}
	}
	return core.ProbeResult{}
}

func TestModelsUnsupportedStatusesAreWarnings(t *testing.T) {
	for _, status := range []int{404, 405, 501} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			s := httptest.NewServer(testserver.Handler(testserver.Config{ModelsStatus: status}))
			defer s.Close()
			target := config.Target{BaseURL: s.URL, Model: "m", Timeout: time.Second}
			if err := target.Normalize(); err != nil {
				t.Fatal(err)
			}
			result := New(target).Run(context.Background())
			if got := find(result.Probes, "authentication_models").Status; got != core.Warn {
				t.Fatalf("models=%s", got)
			}
			if got := find(result.Probes, "basic_chat_completion").Status; got != core.Pass {
				t.Fatalf("endpoint incorrectly failed: %s", got)
			}
		})
	}
}

func TestHTTPStatusClassification(t *testing.T) {
	tests := []struct {
		status int
		kind   string
	}{{401, "authentication"}, {403, "authentication"}, {429, "rate_limit"}, {500, "server"}, {503, "server"}}
	for _, tt := range tests {
		t.Run(http.StatusText(tt.status), func(t *testing.T) {
			s := httptest.NewServer(testserver.Handler(testserver.Config{BasicStatus: tt.status}))
			defer s.Close()
			target := config.Target{BaseURL: s.URL, Model: "m", Timeout: time.Second}
			_ = target.Normalize()
			basic, _ := New(target).basic(context.Background())
			if basic.Error == nil || basic.Error.Kind != tt.kind {
				t.Fatalf("error=%#v", basic.Error)
			}
			if !strings.Contains(basic.Error.Message, fmt.Sprint(tt.status)) {
				t.Fatalf("unhelpful message: %q", basic.Error.Message)
			}
		})
	}
}

func TestStreamContentTypeWithCharset(t *testing.T) {
	s := httptest.NewServer(testserver.Handler(testserver.Config{StreamContentType: "text/event-stream; charset=utf-8"}))
	defer s.Close()
	target := config.Target{BaseURL: s.URL, Model: "m", Timeout: time.Second}
	_ = target.Normalize()
	stream, _, _, _ := New(target).stream(context.Background())
	if stream.Status != core.Pass {
		t.Fatalf("stream=%#v", stream)
	}
}

func TestConfiguredTokenLimitReachesStreamingPayload(t *testing.T) {
	requests := make(chan map[string]any, 2)
	s := httptest.NewServer(testserver.Handler(testserver.Config{CaptureRequest: func(payload map[string]any) { requests <- payload }}))
	defer s.Close()
	target := config.Target{BaseURL: s.URL, Model: "m", Timeout: time.Second, StreamTokens: 2048}
	_ = target.Normalize()
	stream, _, _, _ := New(target).stream(context.Background())
	if stream.Status != core.Pass {
		t.Fatalf("stream=%#v", stream)
	}
	payload := <-requests
	if payload["max_completion_tokens"] != float64(2048) {
		t.Fatalf("payload=%#v", payload)
	}
	if payload["stream"] != true {
		t.Fatalf("not a streaming request: %#v", payload)
	}
	messages, ok := payload["messages"].([]any)
	if !ok || len(messages) != 1 {
		t.Fatalf("messages=%#v", payload["messages"])
	}
	message, ok := messages[0].(map[string]any)
	if !ok {
		t.Fatalf("message=%#v", messages[0])
	}
	prompt, _ := message["content"].(string)
	if !strings.Contains(prompt, "consecutive integers") || !strings.Contains(prompt, "until the output limit") {
		t.Fatalf("prompt does not request deliberate long simple output: %q", prompt)
	}
}

func TestDefaultStreamTokenLimitReachesPayload(t *testing.T) {
	requests := make(chan map[string]any, 1)
	s := httptest.NewServer(testserver.Handler(testserver.Config{CaptureRequest: func(payload map[string]any) { requests <- payload }}))
	defer s.Close()
	target := config.Target{BaseURL: s.URL, Model: "m", Timeout: time.Second}
	if err := target.Normalize(); err != nil {
		t.Fatal(err)
	}
	New(target).stream(context.Background())
	payload := <-requests
	if payload["max_completion_tokens"] != float64(config.DefaultStreamTokens) {
		t.Fatalf("payload=%#v", payload)
	}
}

func TestStreamDurationIsObservedNotInferredFromTokens(t *testing.T) {
	s := httptest.NewServer(testserver.Handler(testserver.Config{TerminationDelay: 30 * time.Millisecond}))
	defer s.Close()
	target := config.Target{BaseURL: s.URL, Model: "m", Timeout: time.Second, StreamTokens: 2048}
	_ = target.Normalize()
	stream, _, _, _ := New(target).stream(context.Background())
	duration, ok := stream.Facts["stream_duration_ms"].(int64)
	if !ok {
		t.Fatalf("duration type=%T", stream.Facts["stream_duration_ms"])
	}
	if duration < 20 || duration >= 1000 {
		t.Fatalf("duration=%dms, appears not to be observed wall time", duration)
	}
	if got := stream.Facts["requested_max_completion_tokens"]; got != 2048 {
		t.Fatalf("requested max_completion_tokens=%v", got)
	}
}

func TestNetworkErrorClassification(t *testing.T) {
	t.Run("connection refused", func(t *testing.T) {
		target := config.Target{BaseURL: "http://127.0.0.1:1", Model: "m", Timeout: time.Second}
		_ = target.Normalize()
		result := New(target).Run(context.Background())
		models := find(result.Probes, "authentication_models")
		if models.Error == nil || models.Error.Kind != "connection_refused" {
			t.Fatalf("error=%#v", models.Error)
		}
		if find(result.Probes, "basic_chat_completion").Status != core.Skip {
			t.Fatal("subsequent probe was not skipped")
		}
	})
	t.Run("TLS", func(t *testing.T) {
		s := httptest.NewTLSServer(testserver.Handler(testserver.Config{}))
		defer s.Close()
		target := config.Target{BaseURL: s.URL, Model: "m", Timeout: time.Second}
		_ = target.Normalize()
		result := New(target).Run(context.Background())
		if e := find(result.Probes, "authentication_models").Error; e == nil || e.Kind != "tls" {
			t.Fatalf("error=%#v", e)
		}
	})
	t.Run("timeout", func(t *testing.T) {
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { time.Sleep(200 * time.Millisecond) }))
		defer s.Close()
		target := config.Target{BaseURL: s.URL, Model: "m", Timeout: 20 * time.Millisecond}
		_ = target.Normalize()
		result := New(target).Run(context.Background())
		if e := find(result.Probes, "authentication_models").Error; e == nil || e.Kind != "timeout" {
			t.Fatalf("error=%#v", e)
		}
	})
	t.Run("cancellation", func(t *testing.T) {
		started := make(chan struct{})
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { close(started); <-r.Context().Done() }))
		defer s.Close()
		target := config.Target{BaseURL: s.URL, Model: "m", Timeout: time.Second}
		_ = target.Normalize()
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan RunResult, 1)
		go func() { done <- New(target).Run(ctx) }()
		<-started
		cancel()
		select {
		case result := <-done:
			if e := find(result.Probes, "authentication_models").Error; e == nil || e.Kind != "cancellation" {
				t.Fatalf("error=%#v", e)
			}
		case <-time.After(500 * time.Millisecond):
			t.Fatal("runner did not cancel promptly")
		}
	})
}

func TestLowLevelErrorClassification(t *testing.T) {
	tests := []struct {
		name string
		err  error
		kind string
	}{{"DNS", &net.DNSError{Err: "no such host", Name: "invalid.test"}, "dns"}, {"network unreachable", syscall.ENETUNREACH, "network_unreachable"}, {"unexpected EOF", io.ErrUnexpectedEOF, "unexpected_eof"}, {"EOF", io.EOF, "unexpected_eof"}}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := probeError(tt.err).Kind; got != tt.kind {
				t.Fatalf("kind=%q want %q", got, tt.kind)
			}
		})
	}
}

type trackingBody struct{ closed bool }

func (b *trackingBody) Read([]byte) (int, error) { return 0, io.EOF }
func (b *trackingBody) Close() error             { b.closed = true; return nil }

type trackingTransport struct {
	body   *trackingBody
	status int
}

func (t trackingTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return &http.Response{StatusCode: t.status, Status: fmt.Sprintf("%d status", t.status), Body: t.body, Header: make(http.Header)}, nil
}

func TestResponseBodyClosedOnEarlyHTTPStatusReturns(t *testing.T) {
	for _, status := range []int{401, 404, 429, 500} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			body := &trackingBody{}
			target := config.Target{BaseURL: "http://example.test", Model: "m", Timeout: time.Second}
			runner := New(target)
			runner.Client = &transport.Client{HTTP: &http.Client{Transport: trackingTransport{body: body, status: status}}}
			_, _ = runner.models(context.Background())
			if !body.closed {
				t.Fatal("models response body was not closed")
			}
		})
	}
}

func TestAllProbeResponseBodiesClose(t *testing.T) {
	tests := []struct {
		name   string
		status int
		run    func(*Runner)
	}{
		{"models success", 200, func(r *Runner) { r.models(context.Background()) }},
		{"basic status", 429, func(r *Runner) { r.basic(context.Background()) }},
		{"stream status", 503, func(r *Runner) { r.stream(context.Background()) }},
		{"stream wrong content type", 200, func(r *Runner) { r.stream(context.Background()) }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body := &trackingBody{}
			target := config.Target{BaseURL: "http://example.test", Model: "m", Timeout: time.Second}
			runner := New(target)
			headers := make(http.Header)
			if tt.name == "stream wrong content type" {
				headers.Set("Content-Type", "application/json")
			}
			runner.Client = &transport.Client{HTTP: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: tt.status, Status: fmt.Sprintf("%d status", tt.status), Body: body, Header: headers}, nil
			})}}
			tt.run(runner)
			if !body.closed {
				t.Fatal("body not closed")
			}
		})
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestStreamCancellationIsNotMalformedSSE(t *testing.T) {
	started := make(chan struct{})
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.(http.Flusher).Flush()
		close(started)
		<-r.Context().Done()
	}))
	defer s.Close()
	target := config.Target{BaseURL: s.URL, Model: "m", Timeout: time.Second}
	_ = target.Normalize()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan core.ProbeResult, 1)
	go func() { stream, _, _, _ := New(target).stream(ctx); done <- stream }()
	<-started
	cancel()
	select {
	case stream := <-done:
		if stream.Error == nil || stream.Error.Kind != "cancellation" {
			t.Fatalf("stream=%#v", stream)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("stream did not cancel")
	}
}
