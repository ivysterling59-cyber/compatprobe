package config

import (
	"testing"
	"time"
)

func TestTargetNormalizeAndURL(t *testing.T) {
	t.Setenv("COMPATPROBE_API_KEY", "secret")
	target := Target{BaseURL: "https://example.com/v1/", Model: "m"}
	if err := target.Normalize(); err != nil {
		t.Fatal(err)
	}
	if target.APIKey != "secret" || target.Timeout != 60*time.Second || target.StreamTokens != DefaultStreamTokens {
		t.Fatalf("defaults not applied: %#v", target)
	}
	if got := target.URL("/v1/models"); got != "https://example.com/v1/models" {
		t.Fatalf("URL = %q", got)
	}
}

func TestStreamTokensValidation(t *testing.T) {
	for _, value := range []int{MinStreamTokens, DefaultStreamTokens, 2048, MaxStreamTokens} {
		target := Target{BaseURL: "https://host", Model: "m", StreamTokens: value}
		if err := target.Normalize(); err != nil {
			t.Fatalf("%d rejected: %v", value, err)
		}
	}
	for _, value := range []int{-1, 1, MinStreamTokens - 1, MaxStreamTokens + 1} {
		target := Target{BaseURL: "https://host", Model: "m", StreamTokens: value}
		if err := target.Normalize(); err == nil {
			t.Fatalf("%d accepted", value)
		}
	}
}

func TestBaseURLVariantsDoNotDuplicateV1(t *testing.T) {
	tests := []struct{ base, want string }{
		{"https://host", "https://host/v1/models"},
		{"https://host/", "https://host/v1/models"},
		{"https://host/v1", "https://host/v1/models"},
		{"https://host/v1/", "https://host/v1/models"},
	}
	for _, tt := range tests {
		t.Run(tt.base, func(t *testing.T) {
			target := Target{BaseURL: tt.base, Model: "m"}
			if err := target.Normalize(); err != nil {
				t.Fatal(err)
			}
			if got := target.URL("/v1/models"); got != tt.want {
				t.Fatalf("URL=%q want %q", got, tt.want)
			}
		})
	}
}

func TestTargetRejectsCredentialBearingBaseURL(t *testing.T) {
	for _, raw := range []string{"https://user:pass@host/v1", "https://host/v1?token=secret", "https://host/v1#fragment"} {
		target := Target{BaseURL: raw, Model: "m"}
		if err := target.Normalize(); err == nil {
			t.Fatalf("accepted %q", raw)
		}
	}
}
