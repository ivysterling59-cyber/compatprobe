package redact

import (
	"net/url"
	"regexp"
	"strings"
)

const Replacement = "[REDACTED]"

var (
	sensitiveHeader = regexp.MustCompile(`(?i)^(authorization|proxy-authorization|api-key|x-api-key|cookie|set-cookie|.*token.*|.*secret.*|.*session.*|.*[-_]key)$`)
	bearerPattern   = regexp.MustCompile(`(?i)\bBearer\s+[^\s,;"']+`)
	skPattern       = regexp.MustCompile(`\bsk-[A-Za-z0-9_-]{6,}\b`)
	headerPattern   = regexp.MustCompile(`(?im)^(Authorization|Proxy-Authorization|api-key|x-api-key|Cookie|Set-Cookie)\s*:\s*.*$`)
	sensitiveQuery  = regexp.MustCompile(`(?i)(api[-_]?key|access[-_]?token|auth|authorization|token|cookie|secret)`)
)

func Header(name, value string) string {
	if sensitiveHeader.MatchString(strings.TrimSpace(name)) {
		return Replacement
	}
	return String(value)
}

func Secrets(s string, secrets ...string) string {
	s = String(s)
	for _, secret := range secrets {
		if secret != "" {
			s = strings.ReplaceAll(s, secret, Replacement)
		}
	}
	return s
}

func URL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return String(raw)
	}
	q := u.Query()
	for key := range q {
		if sensitiveQuery.MatchString(key) {
			q.Set(key, Replacement)
		}
	}
	u.RawQuery = q.Encode()
	u.User = nil
	return String(u.String())
}

func String(s string) string {
	s = headerPattern.ReplaceAllStringFunc(s, func(line string) string {
		if i := strings.Index(line, ":"); i >= 0 {
			return line[:i+1] + " " + Replacement
		}
		return Replacement
	})
	s = bearerPattern.ReplaceAllString(s, "Bearer "+Replacement)
	return skPattern.ReplaceAllString(s, Replacement)
}

func Value(v any) any {
	switch x := v.(type) {
	case string:
		return String(x)
	case map[string]string:
		out := make(map[string]string, len(x))
		for k, v := range x {
			out[k] = Header(k, v)
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, v := range x {
			if sensitiveHeader.MatchString(k) {
				out[k] = Replacement
			} else {
				out[k] = Value(v)
			}
		}
		return out
	case []string:
		out := make([]string, len(x))
		for i := range x {
			out[i] = String(x[i])
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i := range x {
			out[i] = Value(x[i])
		}
		return out
	default:
		return v
	}
}
