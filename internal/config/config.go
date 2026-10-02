package config

import (
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

const (
	DefaultStreamTokens = 256
	MinStreamTokens     = 16
	MaxStreamTokens     = 32768
)

type Target struct {
	BaseURL      string
	APIKey       string
	Model        string
	Timeout      time.Duration
	StreamTokens int
	Headers      http.Header
}

func (t *Target) Normalize() error {
	t.BaseURL = strings.TrimRight(strings.TrimSpace(t.BaseURL), "/")
	if t.APIKey == "" {
		t.APIKey = os.Getenv("COMPATPROBE_API_KEY")
	}
	if t.Timeout == 0 {
		t.Timeout = 60 * time.Second
	}
	if t.StreamTokens == 0 {
		t.StreamTokens = DefaultStreamTokens
	}
	u, err := url.Parse(t.BaseURL)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return fmt.Errorf("base URL must be an absolute http(s) URL")
	}
	if u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return fmt.Errorf("base URL must not contain user info, query parameters, or a fragment")
	}
	if t.Model == "" {
		return fmt.Errorf("model is required")
	}
	if t.Timeout <= 0 {
		return fmt.Errorf("timeout must be positive")
	}
	if err := ValidateStreamTokens(t.StreamTokens); err != nil {
		return err
	}
	return nil
}

func ValidateStreamTokens(value int) error {
	if value < MinStreamTokens || value > MaxStreamTokens {
		return fmt.Errorf("stream tokens must be between %d and %d", MinStreamTokens, MaxStreamTokens)
	}
	return nil
}

func (t Target) URL(path string) string {
	base := t.BaseURL
	if strings.HasSuffix(base, "/v1") && strings.HasPrefix(path, "/v1/") {
		return base + strings.TrimPrefix(path, "/v1")
	}
	return base + path
}
