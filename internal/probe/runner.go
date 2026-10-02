package probe

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"strings"
	"syscall"
	"time"

	"github.com/ivysterling59-cyber/compatprobe/internal/config"
	"github.com/ivysterling59-cyber/compatprobe/internal/core"
	openai "github.com/ivysterling59-cyber/compatprobe/internal/protocol/openai"
	"github.com/ivysterling59-cyber/compatprobe/internal/redact"
	"github.com/ivysterling59-cyber/compatprobe/internal/transport"
)

const maxBody = 4 << 20

type RunResult struct {
	Probes   []core.ProbeResult
	Timeline []core.TimelineEvent
}

type Runner struct {
	Target config.Target
	Client *transport.Client
}

func New(target config.Target) *Runner {
	return &Runner{Target: target, Client: transport.New(target.Timeout)}
}

func (r *Runner) Run(ctx context.Context) RunResult {
	defer r.Client.CloseIdleConnections()
	if r.Target.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, r.Target.Timeout)
		defer cancel()
	}
	models, mt := r.models(ctx)
	if ctx.Err() != nil || (models.Error != nil && IsNetworkKind(models.Error.Kind)) {
		return earlyRun(models, mt)
	}
	basic, bt := r.basic(ctx)
	if ctx.Err() != nil {
		stream, framing, finish := skippedStream(ctx.Err())
		mt = placeTimeline(mt, 0, "authentication_models")
		bt = placeTimeline(bt, models.Duration, "basic_chat_completion")
		return RunResult{Probes: []core.ProbeResult{models, basic, stream, framing, finish}, Timeline: append(mt, bt...)}
	}
	stream, framing, finish, st := r.stream(ctx)
	mt = placeTimeline(mt, 0, "authentication_models")
	bt = placeTimeline(bt, models.Duration, "basic_chat_completion")
	st = placeTimeline(st, models.Duration+basic.Duration, "stream_completion")
	return RunResult{Probes: []core.ProbeResult{models, basic, stream, framing, finish}, Timeline: append(append(mt, bt...), st...)}
}

func earlyRun(models core.ProbeResult, timeline []core.TimelineEvent) RunResult {
	reason := "network target unavailable"
	if models.Error != nil {
		reason = models.Error.Kind
	}
	basic := core.ProbeResult{Name: "basic_chat_completion", Status: core.Skip, Facts: map[string]any{"reason": "not run after " + reason}}
	stream, framing, finish := skippedStream(errors.New(reason))
	return RunResult{Probes: []core.ProbeResult{models, basic, stream, framing, finish}, Timeline: placeTimeline(timeline, 0, "authentication_models")}
}

func skippedStream(reason error) (core.ProbeResult, core.ProbeResult, core.ProbeResult) {
	facts := func() map[string]any { return map[string]any{"reason": "not run after " + reason.Error()} }
	return core.ProbeResult{Name: "stream_completion", Status: core.Skip, Facts: facts()}, core.ProbeResult{Name: "sse_framing", Status: core.Skip, Facts: facts()}, core.ProbeResult{Name: "finish_semantics", Status: core.Skip, Facts: facts()}
}

func placeTimeline(events []core.TimelineEvent, offset time.Duration, request string) []core.TimelineEvent {
	for i := range events {
		events[i].Timestamp += offset
		if events[i].Metadata == nil {
			events[i].Metadata = map[string]string{}
		}
		events[i].Metadata["request"] = request
	}
	return events
}

func (r *Runner) request(ctx context.Context, method, path string, body any) (*http.Response, *transport.Observation, error) {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, nil, err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, r.Target.URL(path), rd)
	if err != nil {
		return nil, nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if r.Target.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+r.Target.APIKey)
	}
	for k, values := range r.Target.Headers {
		for _, value := range values {
			req.Header.Add(k, value)
		}
	}
	return r.Client.Do(ctx, req)
}

func (r *Runner) models(ctx context.Context) (core.ProbeResult, []core.TimelineEvent) {
	start := time.Now()
	p := core.ProbeResult{Name: "authentication_models", Facts: map[string]any{}}
	resp, obs, err := r.request(ctx, http.MethodGet, "/v1/models", nil)
	if err != nil {
		p.Status = core.Fail
		p.Error = probeError(err)
		p.Duration = time.Since(start)
		return p, timeline(obs)
	}
	defer resp.Body.Close()
	p.Facts["http_status"] = resp.StatusCode
	if resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusMethodNotAllowed || resp.StatusCode == http.StatusNotImplemented {
		p.Status = core.Warn
		p.Facts["models_supported"] = false
		p.Duration = time.Since(start)
		return p, timeline(obs)
	}
	b, readErr := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		p.Status = core.Fail
		p.Facts["authentication_failed"] = true
		p.Error = &core.ProbeError{Kind: "authentication", Message: "authentication was rejected"}
		p.Duration = time.Since(start)
		return p, timeline(obs)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		p.Status = core.Fail
		p.Error = httpProbeError("models", resp.StatusCode)
		p.Duration = time.Since(start)
		return p, timeline(obs)
	}
	if readErr != nil {
		p.Status = core.Fail
		p.Error = probeError(readErr)
		p.Duration = time.Since(start)
		return p, timeline(obs)
	}
	var value any
	jsonOK := json.Unmarshal(b, &value) == nil
	p.Facts["json_valid"] = jsonOK
	if resp.StatusCode >= 200 && resp.StatusCode < 300 && jsonOK {
		p.Status = core.Pass
	} else {
		p.Status = core.Fail
		p.Error = &core.ProbeError{Kind: "protocol", Message: fmt.Sprintf("models returned HTTP %d or invalid JSON", resp.StatusCode)}
	}
	p.Duration = time.Since(start)
	return p, timeline(obs)
}

func (r *Runner) basic(ctx context.Context) (core.ProbeResult, []core.TimelineEvent) {
	start := time.Now()
	p := core.ProbeResult{Name: "basic_chat_completion", Facts: map[string]any{}}
	body := map[string]any{"model": r.Target.Model, "messages": []map[string]string{{"role": "user", "content": "Reply with exactly: OK"}}, "stream": false}
	resp, obs, err := r.request(ctx, http.MethodPost, "/v1/chat/completions", body)
	if err != nil {
		p.Status = core.Fail
		p.Error = probeError(err)
		p.Duration = time.Since(start)
		return p, timeline(obs)
	}
	defer resp.Body.Close()
	p.Facts["http_status"] = resp.StatusCode
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		p.Status = core.Fail
		p.Error = httpProbeError("basic completion", resp.StatusCode)
		p.Duration = time.Since(start)
		return p, timeline(obs)
	}
	b, readErr := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if readErr != nil {
		p.Status = core.Fail
		p.Error = probeError(readErr)
		p.Duration = time.Since(start)
		return p, timeline(obs)
	}
	var out struct {
		Choices []struct {
			Message struct {
				Content any `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	jsonOK := json.Unmarshal(b, &out) == nil
	contentOK := jsonOK && len(out.Choices) > 0 && meaningful(out.Choices[0].Message.Content)
	p.Facts["json_valid"], p.Facts["completion_content_seen"] = jsonOK, contentOK
	if resp.StatusCode >= 200 && resp.StatusCode < 300 && contentOK {
		p.Status = core.Pass
	} else {
		p.Status = core.Fail
		p.Error = &core.ProbeError{Kind: "protocol", Message: fmt.Sprintf("basic completion returned HTTP %d without usable completion content", resp.StatusCode)}
	}
	p.Duration = time.Since(start)
	return p, timeline(obs)
}

func (r *Runner) stream(ctx context.Context) (core.ProbeResult, core.ProbeResult, core.ProbeResult, []core.TimelineEvent) {
	start := time.Now()
	sp := core.ProbeResult{Name: "stream_completion", Facts: map[string]any{"requested_max_completion_tokens": r.Target.StreamTokens}}
	fp := core.ProbeResult{Name: "sse_framing", Facts: map[string]any{}}
	mp := core.ProbeResult{Name: "finish_semantics", Facts: map[string]any{}}
	body := map[string]any{
		"model":                 r.Target.Model,
		"messages":              []map[string]string{{"role": "user", "content": "Produce a long, simple sequence of consecutive integers starting at 1, separated by a single space. Continue until the output limit stops you. Output integers only."}},
		"stream":                true,
		"max_completion_tokens": r.Target.StreamTokens,
	}
	resp, obs, err := r.request(ctx, http.MethodPost, "/v1/chat/completions", body)
	if err != nil {
		sp.Status, fp.Status, mp.Status = core.Fail, core.Skip, core.Skip
		sp.Error = probeError(err)
		sp.Duration = time.Since(start)
		return sp, fp, mp, timeline(obs)
	}
	defer resp.Body.Close()
	sp.Facts["http_status"] = resp.StatusCode
	sp.Facts["ttfb_ms"] = obs.TTFBDuration().Milliseconds()
	mediaType, _, mediaErr := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	isSSE := mediaErr == nil && strings.EqualFold(mediaType, "text/event-stream")
	sp.Facts["content_type"] = resp.Header.Get("Content-Type")
	sp.Facts["content_type_is_event_stream"] = isSSE
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		sp.Status, fp.Status, mp.Status = core.Fail, core.Skip, core.Skip
		sp.Error = httpProbeError("stream", resp.StatusCode)
		sp.Duration = time.Since(start)
		return sp, fp, mp, timeline(obs)
	}
	if !isSSE {
		b, readErr := io.ReadAll(io.LimitReader(resp.Body, maxBody))
		var v any
		jsonBody := readErr == nil && json.Unmarshal(b, &v) == nil
		sp.Facts["body_is_json"] = jsonBody
		sp.Facts["clean_eof"] = readErr == nil
		sp.Status = core.Fail
		fp.Status = core.Skip
		mp.Status = core.Skip
		sp.Error = &core.ProbeError{Kind: "protocol", Message: "stream response is not text/event-stream"}
		sp.Duration = time.Since(start)
		return sp, fp, mp, timeline(obs)
	}
	parser := openai.NewSSEParser(resp.Body)
	valid, invalid, chunks := 0, 0, 0
	done, finish := false, false
	firstEvent := time.Duration(0)
	cleanEOF := false
	var terminalErr error
	for {
		ev, parseErr := parser.Next()
		if ev.Data != "" || ev.Event != "" || ev.ID != "" {
			chunks++
			if firstEvent == 0 {
				firstEvent = time.Since(start)
				obs.AddAt("first_sse_event", firstEvent, nil)
			}
		}
		if parseErr != nil {
			if errors.Is(parseErr, io.EOF) {
				cleanEOF = true
			} else if ctx.Err() != nil {
				terminalErr = ctx.Err()
			} else {
				invalid++
				terminalErr = parseErr
			}
			break
		}
		if ev.Data == "[DONE]" {
			done = true
			valid++
			continue
		}
		if ev.Data == "" {
			valid++
			continue
		}
		var chunk struct {
			Choices []struct {
				FinishReason *string `json:"finish_reason"`
			} `json:"choices"`
		}
		if json.Unmarshal([]byte(ev.Data), &chunk) != nil {
			invalid++
			continue
		}
		valid++
		for _, c := range chunk.Choices {
			if c.FinishReason != nil && *c.FinishReason != "" {
				finish = true
			}
		}
	}
	duration := time.Since(start)
	for _, p := range []*core.ProbeResult{&sp, &fp, &mp} {
		p.Duration = duration
	}
	sp.Facts["chunks"], sp.Facts["stream_duration_ms"], sp.Facts["time_to_first_event_ms"] = chunks, duration.Milliseconds(), firstEvent.Milliseconds()
	sp.Facts["clean_eof"], sp.Facts["transport_error"] = cleanEOF, errorString(terminalErr)
	fp.Facts["valid_sse_frames"], fp.Facts["invalid_sse_frames"] = valid, invalid
	mp.Facts["finish_reason_seen"], mp.Facts["done_seen"], mp.Facts["clean_eof"] = finish, done, cleanEOF
	if terminalErr != nil {
		sp.Status = core.Fail
		sp.Error = probeError(terminalErr)
	} else if done || finish {
		sp.Status = core.Pass
	} else {
		sp.Status = core.Warn
	}
	if invalid > 0 {
		fp.Status = core.Fail
		fp.Error = &core.ProbeError{Kind: "protocol", Message: "one or more SSE frames were invalid"}
	} else {
		fp.Status = core.Pass
	}
	if done && finish {
		mp.Status = core.Pass
	} else {
		mp.Status = core.Warn
	}
	return sp, fp, mp, timeline(obs)
}

func timeline(o *transport.Observation) []core.TimelineEvent {
	if o == nil {
		return nil
	}
	return o.TimelineSnapshot()
}
func meaningful(v any) bool {
	switch x := v.(type) {
	case string:
		return strings.TrimSpace(x) != ""
	case []any:
		return len(x) > 0
	default:
		return v != nil
	}
}
func errorString(err error) string {
	if err == nil {
		return ""
	}
	return redact.String(err.Error())
}
func probeError(err error) *core.ProbeError {
	kind, message := "transport", redact.String(err.Error())
	switch {
	case errors.Is(err, context.Canceled):
		kind, message = "cancellation", "request cancelled"
	case errors.Is(err, context.DeadlineExceeded):
		kind, message = "timeout", "request timed out; increase --timeout or inspect upstream latency"
	case errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, io.EOF):
		kind, message = "unexpected_eof", "connection ended before the response was complete"
	case errors.Is(err, syscall.ECONNREFUSED):
		kind, message = "connection_refused", "connection was refused; verify the host, port, and service availability"
	case errors.Is(err, syscall.ENETUNREACH), errors.Is(err, syscall.EHOSTUNREACH):
		kind, message = "network_unreachable", "network is unreachable; verify local connectivity, routing, VPN, and proxy settings"
	default:
		var dnsErr *net.DNSError
		if errors.As(err, &dnsErr) {
			kind, message = "dns", "DNS lookup failed; verify the endpoint hostname and DNS connectivity"
		} else if isTLSError(err) {
			kind, message = "tls", "TLS negotiation or certificate verification failed"
		} else if isConnectionRefused(err) {
			kind, message = "connection_refused", "connection was refused; verify the host, port, and service availability"
		} else if isNetworkUnreachable(err) {
			kind, message = "network_unreachable", "network is unreachable; verify local connectivity, routing, VPN, and proxy settings"
		} else {
			var netErr net.Error
			if errors.As(err, &netErr) && netErr.Timeout() {
				kind, message = "timeout", "request timed out; increase --timeout or inspect upstream latency"
			}
		}
	}
	return &core.ProbeError{Kind: kind, Message: message}
}

func isConnectionRefused(err error) bool {
	var errno syscall.Errno
	if errors.As(err, &errno) && errno == 10061 {
		return true
	}
	s := strings.ToLower(err.Error())
	return strings.Contains(s, "connection refused") || strings.Contains(s, "actively refused") || strings.Contains(s, "target machine actively refused")
}

func isNetworkUnreachable(err error) bool {
	var errno syscall.Errno
	if errors.As(err, &errno) && (errno == 10051 || errno == 10065) {
		return true
	}
	s := strings.ToLower(err.Error())
	return strings.Contains(s, "network is unreachable") || strings.Contains(s, "no route to host")
}

func isTLSError(err error) bool {
	s := strings.ToLower(err.Error())
	return strings.Contains(s, "tls") || strings.Contains(s, "x509") || strings.Contains(s, "certificate")
}

func httpProbeError(operation string, status int) *core.ProbeError {
	switch status {
	case http.StatusUnauthorized, http.StatusForbidden:
		return &core.ProbeError{Kind: "authentication", Message: fmt.Sprintf("%s authentication was rejected with HTTP %d; verify the API key and permissions", operation, status)}
	case http.StatusTooManyRequests:
		return &core.ProbeError{Kind: "rate_limit", Message: fmt.Sprintf("%s was rate limited with HTTP 429; retry later or inspect account limits", operation)}
	}
	if status >= 500 {
		return &core.ProbeError{Kind: "server", Message: fmt.Sprintf("%s returned HTTP %d; inspect endpoint and upstream logs", operation, status)}
	}
	return &core.ProbeError{Kind: "http_status", Message: fmt.Sprintf("%s returned unexpected HTTP %d", operation, status)}
}

func IsNetworkKind(kind string) bool {
	switch kind {
	case "transport", "dns", "tls", "connection_refused", "network_unreachable", "timeout", "cancellation", "unexpected_eof":
		return true
	default:
		return false
	}
}
