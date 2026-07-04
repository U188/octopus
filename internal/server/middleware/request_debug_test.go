package middleware

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDebugRequestBodyRedactsJSONAndRestoresBody(t *testing.T) {
	body := `{"model":"claude","api_key":"sk-secret","messages":[{"content":"hello","access_token":"token-secret"}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	logged, omitted := debugRequestBody(req, 4096)
	if omitted != "" {
		t.Fatalf("unexpected omitted reason: %s", omitted)
	}
	if strings.Contains(logged, "sk-secret") || strings.Contains(logged, "token-secret") {
		t.Fatalf("expected sensitive JSON fields to be redacted, got %s", logged)
	}
	if !strings.Contains(logged, `"api_key":"[REDACTED]"`) || !strings.Contains(logged, `"access_token":"[REDACTED]"`) {
		t.Fatalf("expected redacted JSON body, got %s", logged)
	}

	restored, err := io.ReadAll(req.Body)
	if err != nil {
		t.Fatalf("read restored body: %v", err)
	}
	if string(restored) != body {
		t.Fatalf("body was not restored; got %q", string(restored))
	}
}

func TestDebugRequestBodySkipsLargeBodyWithoutConsuming(t *testing.T) {
	body := strings.Repeat("a", 32)
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Content-Type", "text/plain")

	logged, omitted := debugRequestBody(req, 8)
	if logged != "" || omitted != "too_large" {
		t.Fatalf("expected large body to be omitted, got logged=%q omitted=%q", logged, omitted)
	}

	restored, err := io.ReadAll(req.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if string(restored) != body {
		t.Fatalf("large body was consumed; got %q", string(restored))
	}
}

func TestRedactHeaders(t *testing.T) {
	headers := http.Header{}
	headers.Set("Authorization", "Bearer secret")
	headers.Set("X-Api-Key", "sk-secret")
	headers.Set("Content-Type", "application/json")

	redacted := redactHeaders(headers)
	if got := redacted["Authorization"]; len(got) != 1 || got[0] != "[REDACTED]" {
		t.Fatalf("Authorization not redacted: %#v", got)
	}
	if got := redacted["X-Api-Key"]; len(got) != 1 || got[0] != "[REDACTED]" {
		t.Fatalf("X-Api-Key not redacted: %#v", got)
	}
	if got := redacted["Content-Type"]; len(got) != 1 || got[0] != "application/json" {
		t.Fatalf("Content-Type should not be redacted: %#v", got)
	}
}

func TestDebugRequestBodyRedactsFormBody(t *testing.T) {
	body := "model=claude&api_key=sk-secret&prompt=hello"
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	logged, omitted := debugRequestBody(req, 4096)
	if omitted != "" {
		t.Fatalf("unexpected omitted reason: %s", omitted)
	}
	if strings.Contains(logged, "sk-secret") {
		t.Fatalf("expected form api_key to be redacted, got %s", logged)
	}
	if !strings.Contains(logged, "api_key=%5BREDACTED%5D") {
		t.Fatalf("expected redacted form body, got %s", logged)
	}
}

func TestRedactQuery(t *testing.T) {
	got := redactQuery("model=claude&key=sk-secret&prompt=hello")
	if strings.Contains(got, "sk-secret") {
		t.Fatalf("expected query key to be redacted, got %s", got)
	}
	if !strings.Contains(got, "key=%5BREDACTED%5D") {
		t.Fatalf("expected redacted query, got %s", got)
	}
}
