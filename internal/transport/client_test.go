package transport

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestClientRecordsResponse(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Set-Cookie", "secret=1")
		w.WriteHeader(204)
	}))
	defer s.Close()
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, s.URL, nil)
	resp, obs, err := New(time.Second).Do(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if obs.StatusCode != 204 || len(obs.Timeline) == 0 {
		t.Fatalf("bad observation: %#v", obs)
	}
	if got := obs.ResponseHeaders.Get("Set-Cookie"); got != "[REDACTED]" {
		t.Fatalf("header leaked: %q", got)
	}
}
