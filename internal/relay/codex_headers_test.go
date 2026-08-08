package relay

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/U188/octopus/internal/model"
	"github.com/U188/octopus/internal/transformer/outbound"
	"github.com/gin-gonic/gin"
)

func TestCodexCompactHeadersPreserveClientIdentityAndBody(t *testing.T) {
	rawBody := `{"model":"gpt-5.6-sol","input":[{"type":"compaction","encrypted_content":"opaque"}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/responses/compact", nil)
	req.Header.Set("Session-Id", "client-session")
	req.Header.Set("Thread-Id", "client-thread")
	req.Header.Set("X-Codex-Window-Id", "client-window:3")
	req.Header.Set("X-Client-Request-Id", "client-request")
	req.Header.Set("X-Codex-Turn-Metadata", `{"installation_id":"install-1","session_id":"client-session","thread_id":"client-thread","turn_id":"turn-9","window_id":"client-window:3"}`)
	upstream := httptest.NewRequest(http.MethodPost, "/v1/responses/compact", strings.NewReader(rawBody))
	upstream.Header = req.Header.Clone()

	channel := &model.Channel{Name: "codex-compact", Type: outbound.OutboundTypeOpenAIResponse, CodexMode: true}
	ra := &relayAttempt{
		relayRequest: &relayRequest{c: &gin.Context{Request: req}},
		channel:      channel,
		usedKey:      model.ChannelKey{ChannelKey: "upstream-key"},
	}
	ra.applyCodexResponseHeadersPreservingBody(upstream)

	body, err := io.ReadAll(upstream.Body)
	if err != nil {
		t.Fatalf("read compact body: %v", err)
	}
	if string(body) != rawBody {
		t.Fatalf("compact body changed: got %s want %s", body, rawBody)
	}
	for key, want := range map[string]string{
		"Session-Id":            "client-session",
		"Thread-Id":             "client-thread",
		"X-Codex-Window-Id":     "client-window:3",
		"X-Client-Request-Id":   "client-request",
		"X-Codex-Turn-Metadata": `{"installation_id":"install-1","session_id":"client-session","thread_id":"client-thread","turn_id":"turn-9","window_id":"client-window:3"}`,
	} {
		if got := upstream.Header.Get(key); got != want {
			t.Fatalf("%s = %q, want %q", key, got, want)
		}
	}
	if got := upstream.Header.Get("X-Codex-Beta-Features"); got == "" {
		t.Fatal("expected Codex beta feature header")
	}
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("decode compact body: %v", err)
	}
	if _, ok := payload["store"]; ok {
		t.Fatalf("compact body unexpectedly received normal turn field: %#v", payload["store"])
	}
}

func TestCodexAnyRouterRequestStripsRemoteCompactionBeforeSend(t *testing.T) {
	rawBody := `{"model":"gpt-5.6-sol","input":[{"type":"compaction","id":"cmp_1","encrypted_content":"opaque"},{"type":"message","role":"user","content":[{"type":"input_text","text":"continue"}]}]}`
	clientRequest := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(rawBody))
	upstream := httptest.NewRequest(http.MethodPost, "https://anyrouter.top/v1/responses", strings.NewReader(rawBody))
	channel := &model.Channel{Name: "any/any/default-Response", Type: outbound.OutboundTypeOpenAIResponse, CodexMode: true}
	ra := &relayAttempt{
		relayRequest: &relayRequest{c: &gin.Context{Request: clientRequest}},
		channel:      channel,
		usedKey:      model.ChannelKey{ChannelKey: "upstream-key"},
	}

	ra.applyCodexResponseHeaders(upstream)
	body, err := io.ReadAll(upstream.Body)
	if err != nil {
		t.Fatalf("read upstream body: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("decode upstream body: %v", err)
	}
	items, ok := payload["input"].([]any)
	if !ok || len(items) != 1 {
		t.Fatalf("expected only the ordinary message after compatibility filtering, got %#v", payload["input"])
	}
	item, ok := items[0].(map[string]any)
	if !ok || item["type"] != "message" {
		t.Fatalf("expected ordinary message to remain, got %#v", items[0])
	}
}

func TestCodexOtherUpstreamPreservesRemoteCompactionOnFirstAttempt(t *testing.T) {
	rawBody := `{"model":"gpt-5.6-sol","input":[{"type":"compaction","encrypted_content":"opaque"}]}`
	clientRequest := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(rawBody))
	upstream := httptest.NewRequest(http.MethodPost, "https://api.example.com/v1/responses", strings.NewReader(rawBody))
	channel := &model.Channel{Name: "compatible", Type: outbound.OutboundTypeOpenAIResponse, CodexMode: true}
	ra := &relayAttempt{
		relayRequest: &relayRequest{c: &gin.Context{Request: clientRequest}},
		channel:      channel,
		usedKey:      model.ChannelKey{ChannelKey: "upstream-key"},
	}

	ra.applyCodexResponseHeaders(upstream)
	body, err := io.ReadAll(upstream.Body)
	if err != nil {
		t.Fatalf("read upstream body: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("decode upstream body: %v", err)
	}
	items, ok := payload["input"].([]any)
	if !ok || len(items) != 1 {
		t.Fatalf("expected compaction item to remain, got %#v", payload["input"])
	}
	item, ok := items[0].(map[string]any)
	if !ok || item["type"] != "compaction" {
		t.Fatalf("expected compaction item to remain, got %#v", items[0])
	}
}
