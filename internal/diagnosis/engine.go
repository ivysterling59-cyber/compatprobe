package diagnosis

import (
	"fmt"
	"strings"

	"github.com/ivysterling59-cyber/compatprobe/internal/core"
)

func Evaluate(probes []core.ProbeResult) []core.Diagnosis {
	byName := map[string]core.ProbeResult{}
	for _, p := range probes {
		byName[p.Name] = p
	}
	var out []core.Diagnosis
	stream, streamOK := byName["stream_completion"]
	basic := byName["basic_chat_completion"]
	framing := byName["sse_framing"]
	valid := integer(framing.Facts["valid_sse_frames"])
	invalid := integer(framing.Facts["invalid_sse_frames"])
	finish := boolFact(byName["finish_semantics"].Facts, "finish_reason_seen")
	done := boolFact(byName["finish_semantics"].Facts, "done_seen")
	if streamOK && basic.Status == core.Pass && valid > 0 && stream.Status == core.Fail && !finish && !done && interruption(stream.Error) {
		out = append(out, core.Diagnosis{Code: "STREAM_TRANSPORT_INTERRUPTION", Severity: core.SeverityFailure, Confidence: core.High, Summary: "Streaming connection terminated before protocol completion.", Evidence: []string{"non-stream request completed successfully", fmt.Sprintf("%d valid SSE events received", valid), "no normal completion marker observed", "transport terminated unexpectedly"}, Suggestions: []string{"Inspect reverse proxy and upstream timeout settings.", "Compare the endpoint without network intermediaries.", "Check server and proxy logs at the recorded termination time."}})
	}
	if invalid >= 2 || (invalid > 0 && valid == 0) {
		out = append(out, core.Diagnosis{Code: "MALFORMED_SSE", Severity: core.SeverityFailure, Confidence: core.High, Summary: "The endpoint returned data using an invalid or incompatible SSE framing format.", Evidence: []string{fmt.Sprintf("%d invalid SSE frames observed", invalid), fmt.Sprintf("%d valid SSE frames observed", valid)}, Suggestions: []string{"Validate SSE fields, frame boundaries, and JSON payloads.", "Ensure each event is separated by a blank line."}})
	}
	if boolFact(stream.Facts, "body_is_json") && !boolFact(stream.Facts, "content_type_is_event_stream") && integer(stream.Facts["http_status"]) >= 200 && integer(stream.Facts["http_status"]) < 300 {
		out = append(out, core.Diagnosis{Code: "STREAM_NOT_SSE", Severity: core.SeverityFailure, Confidence: core.High, Summary: "The endpoint appears to ignore or mishandle streaming mode.", Evidence: []string{"stream=true returned HTTP 2xx", "Content-Type was not text/event-stream", "response body was valid JSON"}, Suggestions: []string{"Verify that the compatibility layer forwards the stream parameter.", "Configure the endpoint to return SSE for streaming requests."}})
	}
	return out
}

func interruption(e *core.ProbeError) bool {
	if e == nil {
		return false
	}
	switch e.Kind {
	case "unexpected_eof", "cancellation":
		return true
	}
	s := strings.ToLower(e.Message)
	for _, x := range []string{"eof", "reset", "cancel", "closed", "abort", "truncat"} {
		if strings.Contains(s, x) {
			return true
		}
	}
	return false
}
func boolFact(m map[string]any, k string) bool { v, _ := m[k].(bool); return v }
func integer(v any) int {
	switch x := v.(type) {
	case int:
		return x
	case int64:
		return int(x)
	case float64:
		return int(x)
	}
	return 0
}
