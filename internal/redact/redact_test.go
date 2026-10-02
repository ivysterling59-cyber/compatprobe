package redact

import (
	"strings"
	"testing"
)

func TestStringRedactsSecrets(t *testing.T) {
	secret := "sk-supersecret123"
	got := String("Authorization: Bearer " + secret + "\nCookie: abc=123\nerror for " + secret)
	if strings.Contains(got, secret) || strings.Contains(got, "abc=123") {
		t.Fatalf("secret leaked: %s", got)
	}
}

func TestURLAndArbitrarySecret(t *testing.T) {
	secret := "unstructured-value"
	got := URL("https://user:pass@example.test/v1?api_key=" + secret + "&region=us")
	if strings.Contains(got, secret) || strings.Contains(got, "user") || !strings.Contains(got, "region=us") {
		t.Fatalf("URL not safely redacted: %s", got)
	}
	if got := Secrets("failure: "+secret, secret); strings.Contains(got, secret) {
		t.Fatalf("arbitrary secret leaked: %s", got)
	}
}

func TestTokenLikeCustomHeadersAreRedacted(t *testing.T) {
	for _, name := range []string{"X-Access-Token", "Client-Secret", "Session-ID", "custom_key"} {
		if got := Header(name, "plain-value"); got != Replacement {
			t.Fatalf("%s=%q", name, got)
		}
	}
}
