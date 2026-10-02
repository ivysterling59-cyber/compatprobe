package transport

import (
	"context"
	"crypto/tls"
	"net/http"
	"net/http/httptrace"
	"sync"
	"time"

	"github.com/ivysterling59-cyber/compatprobe/internal/core"
	"github.com/ivysterling59-cyber/compatprobe/internal/redact"
)

type Observation struct {
	StartedAt       time.Time
	Timeline        []core.TimelineEvent
	StatusCode      int
	ResponseHeaders http.Header
	TTFB            time.Duration
	Reused          bool
	WasIdle         bool
	mu              sync.Mutex
}

func (o *Observation) add(name string, start time.Time, metadata map[string]string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.Timeline = append(o.Timeline, core.TimelineEvent{Name: name, Timestamp: time.Since(start), Metadata: metadata})
}

func (o *Observation) AddAt(name string, timestamp time.Duration, metadata map[string]string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.Timeline = append(o.Timeline, core.TimelineEvent{Name: name, Timestamp: timestamp, Metadata: metadata})
}

func (o *Observation) TimelineSnapshot() []core.TimelineEvent {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]core.TimelineEvent(nil), o.Timeline...)
}

func (o *Observation) TTFBDuration() time.Duration {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.TTFB
}

func (o *Observation) snapshotHeaders(h http.Header) {
	o.ResponseHeaders = make(http.Header, len(h))
	for k, values := range h {
		for _, value := range values {
			o.ResponseHeaders.Add(k, redact.Header(k, value))
		}
	}
}

type Client struct{ HTTP *http.Client }

func (c *Client) CloseIdleConnections() { c.HTTP.CloseIdleConnections() }

func New(timeout time.Duration) *Client {
	tr := http.DefaultTransport.(*http.Transport).Clone()
	return &Client{HTTP: &http.Client{Transport: tr, Timeout: timeout}}
}

func (c *Client) Do(ctx context.Context, req *http.Request) (*http.Response, *Observation, error) {
	start := time.Now()
	o := &Observation{StartedAt: start}
	trace := &httptrace.ClientTrace{
		DNSStart: func(httptrace.DNSStartInfo) { o.add("dns_start", start, nil) },
		DNSDone: func(i httptrace.DNSDoneInfo) {
			m := map[string]string{}
			if i.Err != nil {
				m["error"] = redact.String(i.Err.Error())
			}
			o.add("dns_done", start, m)
		},
		ConnectStart: func(network, addr string) {
			o.add("tcp_connect_start", start, map[string]string{"network": network, "address": addr})
		},
		ConnectDone: func(network, addr string, err error) {
			m := map[string]string{"network": network, "address": addr}
			if err != nil {
				m["error"] = redact.String(err.Error())
			}
			o.add("tcp_connect_done", start, m)
		},
		TLSHandshakeStart: func() { o.add("tls_start", start, nil) },
		TLSHandshakeDone: func(cs tls.ConnectionState, err error) {
			m := map[string]string{"version": tlsVersion(cs.Version)}
			if err != nil {
				m["error"] = redact.String(err.Error())
			}
			o.add("tls_done", start, m)
		},
		GotConn: func(i httptrace.GotConnInfo) {
			o.mu.Lock()
			o.Reused, o.WasIdle = i.Reused, i.WasIdle
			o.mu.Unlock()
			o.add("connection_acquired", start, map[string]string{"reused": boolString(i.Reused), "was_idle": boolString(i.WasIdle)})
		},
		WroteRequest: func(i httptrace.WroteRequestInfo) {
			m := map[string]string{}
			if i.Err != nil {
				m["error"] = redact.String(i.Err.Error())
			}
			o.add("request_written", start, m)
		},
		GotFirstResponseByte: func() {
			o.mu.Lock()
			o.TTFB = time.Since(start)
			o.mu.Unlock()
			o.add("first_response_byte", start, nil)
		},
	}
	req = req.Clone(httptrace.WithClientTrace(ctx, trace))
	resp, err := c.HTTP.Do(req)
	if err != nil {
		if resp != nil && resp.Body != nil {
			resp.Body.Close()
		}
		o.add("request_error", start, map[string]string{"error": redact.String(err.Error())})
		return nil, o, err
	}
	o.StatusCode = resp.StatusCode
	o.snapshotHeaders(resp.Header)
	o.add("response_headers", start, map[string]string{"status": resp.Status})
	return resp, o, nil
}

func tlsVersion(v uint16) string {
	switch v {
	case tls.VersionTLS12:
		return "TLS 1.2"
	case tls.VersionTLS13:
		return "TLS 1.3"
	default:
		return "unknown"
	}
}
func boolString(v bool) string {
	if v {
		return "true"
	}
	return "false"
}
