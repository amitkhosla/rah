package observability

import (
	"net/http"
	"strings"
	"testing"
)

func TestBuildRequestSnapshot(t *testing.T) {
	h := http.Header{
		"Content-Type":  []string{"application/json"},
		"Authorization": []string{"Bearer sk-secret"},
		"X-Request-Id":  []string{"abc123"},
	}
	got := BuildRequestSnapshot("POST", "/api/chat", "v=1", h)
	s := string(got)
	if !strings.Contains(s, `"method":"POST"`) {
		t.Errorf("missing method: %s", s)
	}
	if !strings.Contains(s, `"path":"/api/chat"`) {
		t.Errorf("missing path: %s", s)
	}
	if !strings.Contains(s, `"query":"v=1"`) {
		t.Errorf("missing query: %s", s)
	}
	if !strings.Contains(s, `"[REDACTED]"`) {
		t.Errorf("authorization not redacted: %s", s)
	}
	if strings.Contains(s, "sk-secret") {
		t.Errorf("secret leaked: %s", s)
	}
}

func TestBuildResponseSnapshot(t *testing.T) {
	h := http.Header{
		"Content-Type": []string{"application/json"},
		"Set-Cookie":   []string{"session=abc"},
	}
	got := BuildResponseSnapshot(200, h)
	s := string(got)
	if !strings.Contains(s, `"status":200`) {
		t.Errorf("missing status: %s", s)
	}
	if strings.Contains(s, "session=abc") {
		t.Errorf("cookie leaked: %s", s)
	}
}
