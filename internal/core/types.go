package core

import "time"

type Status string

const (
	Pass Status = "PASS"
	Warn Status = "WARN"
	Fail Status = "FAIL"
	Skip Status = "SKIP"
)

type Evidence struct {
	Kind    string `json:"kind"`
	Message string `json:"message"`
}

type ProbeError struct {
	Kind    string `json:"kind"`
	Message string `json:"message"`
}

type ProbeResult struct {
	Name     string         `json:"name"`
	Status   Status         `json:"status"`
	Duration time.Duration  `json:"-"`
	Facts    map[string]any `json:"facts,omitempty"`
	Evidence []Evidence     `json:"evidence,omitempty"`
	Error    *ProbeError    `json:"error,omitempty"`
}

func (p ProbeResult) MarshalJSON() ([]byte, error) {
	type alias ProbeResult
	return marshalJSON(struct {
		alias
		DurationMS int64 `json:"duration_ms"`
	}{alias: alias(p), DurationMS: p.Duration.Milliseconds()})
}

var marshalJSON = func(v any) ([]byte, error) { return jsonMarshal(v) }

type TimelineEvent struct {
	Name      string            `json:"name"`
	Timestamp time.Duration     `json:"-"`
	Metadata  map[string]string `json:"metadata,omitempty"`
}

func (e TimelineEvent) MarshalJSON() ([]byte, error) {
	type alias TimelineEvent
	return marshalJSON(struct {
		alias
		TimestampMS int64 `json:"timestamp_ms"`
	}{alias: alias(e), TimestampMS: e.Timestamp.Milliseconds()})
}

type Severity string
type Confidence string

const (
	SeverityWarning Severity   = "WARNING"
	SeverityFailure Severity   = "FAILURE"
	Low             Confidence = "LOW"
	Medium          Confidence = "MEDIUM"
	High            Confidence = "HIGH"
)

type Diagnosis struct {
	Code        string     `json:"code"`
	Severity    Severity   `json:"severity"`
	Confidence  Confidence `json:"confidence"`
	Summary     string     `json:"summary"`
	Evidence    []string   `json:"evidence"`
	Suggestions []string   `json:"suggestions"`
}
