package diagnosis

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/ivysterling59-cyber/compatprobe/internal/config"
	"github.com/ivysterling59-cyber/compatprobe/internal/probe"
	"github.com/ivysterling59-cyber/compatprobe/testserver"
)

func diagnosesFor(t *testing.T, mode testserver.Mode) []string {
	t.Helper()
	s := httptest.NewServer(testserver.Handler(testserver.Config{Mode: mode}))
	defer s.Close()
	target := config.Target{BaseURL: s.URL, Model: "m", Timeout: time.Second}
	if err := target.Normalize(); err != nil {
		t.Fatal(err)
	}
	run := probe.New(target).Run(context.Background())
	ds := Evaluate(run.Probes)
	out := make([]string, len(ds))
	for i := range ds {
		out[i] = ds[i].Code
	}
	return out
}
func TestRules(t *testing.T) {
	tests := []struct {
		mode testserver.Mode
		code string
	}{{testserver.PrematureClose, "STREAM_TRANSPORT_INTERRUPTION"}, {testserver.MalformedSSE, "MALFORMED_SSE"}, {testserver.WrongContentType, "STREAM_NOT_SSE"}}
	for _, tt := range tests {
		t.Run(string(tt.mode), func(t *testing.T) {
			got := diagnosesFor(t, tt.mode)
			found := false
			for _, c := range got {
				if c == tt.code {
					found = true
				}
			}
			if !found {
				t.Fatalf("missing %s in %v", tt.code, got)
			}
		})
	}
}
