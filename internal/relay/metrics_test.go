package relay

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	transformerModel "github.com/U188/octopus/internal/transformer/model"
)

// usage 完全缺失时，应使用 TransportInputTokens 兜底填充 input，output 保持 0。
func TestSetInternalResponseFallbackWhenUsageMissing(t *testing.T) {
	m := &RelayMetrics{TransportInputTokens: intPtr(123)}
	m.SetInternalResponse(&transformerModel.InternalLLMResponse{}, "test-model")

	if m.Stats.InputToken != 123 {
		t.Fatalf("input token: got %d want 123 (fallback)", m.Stats.InputToken)
	}
	if m.BillInputTokens == nil || *m.BillInputTokens != 123 {
		t.Fatalf("bill input tokens: got %v want 123", m.BillInputTokens)
	}
	if m.Stats.OutputToken != 0 {
		t.Fatalf("output token: got %d want 0", m.Stats.OutputToken)
	}
}

// usage 存在但输入侧全为 0（仅上报 output）时，input 兜底、output 保留。
func TestSetInternalResponseFallbackWhenInputZero(t *testing.T) {
	m := &RelayMetrics{TransportInputTokens: intPtr(50)}
	m.SetInternalResponse(&transformerModel.InternalLLMResponse{
		Usage: &transformerModel.Usage{PromptTokens: 0, CompletionTokens: 30},
	}, "test-model")

	if m.Stats.InputToken != 50 {
		t.Fatalf("input token: got %d want 50 (fallback)", m.Stats.InputToken)
	}
	if m.Stats.OutputToken != 30 {
		t.Fatalf("output token: got %d want 30 (preserved)", m.Stats.OutputToken)
	}
}

// 上游正常上报 input 时不触发兜底（保留真实值，而非估算值）。
func TestSetInternalResponseNoFallbackWhenInputReported(t *testing.T) {
	m := &RelayMetrics{TransportInputTokens: intPtr(999)}
	m.SetInternalResponse(&transformerModel.InternalLLMResponse{
		Usage: &transformerModel.Usage{PromptTokens: 12, CompletionTokens: 7},
	}, "test-model")

	if m.Stats.InputToken != 12 {
		t.Fatalf("input token: got %d want 12 (reported, not fallback)", m.Stats.InputToken)
	}
	if m.Stats.OutputToken != 7 {
		t.Fatalf("output token: got %d want 7", m.Stats.OutputToken)
	}
}

// 仅缓存命中（input_tokens=0 但 cache_read>0）属于已上报输入，不应被估算覆盖。
func TestSetInternalResponseNoFallbackWhenCacheOnly(t *testing.T) {
	m := &RelayMetrics{TransportInputTokens: intPtr(999)}
	m.SetInternalResponse(&transformerModel.InternalLLMResponse{
		Usage: &transformerModel.Usage{PromptTokens: 0, CacheReadInputTokens: 40, CompletionTokens: 5},
	}, "test-model")

	if m.Stats.InputToken != 0 {
		t.Fatalf("input token: got %d want 0 (cache-only is reported input)", m.Stats.InputToken)
	}
}

func TestSanitizedRequestHeadersForLog(t *testing.T) {
	headers := http.Header{}
	headers.Set("Authorization", "Bearer secret-token")
	headers.Set("Cookie", "session=secret")
	headers.Set("User-Agent", "codex_exec/0.1.0")
	headers.Set("Accept", "text/event-stream")
	headers.Add("X-Codex-Beta-Features", "responses")
	headers.Add("X-Codex-Beta-Features", "compact")
	headers.Set("X-Long", strings.Repeat("a", 2100))

	got := sanitizedRequestHeadersForLog(headers)
	if got == "" {
		t.Fatal("expected serialized headers")
	}

	var parsed map[string]interface{}
	if err := json.Unmarshal([]byte(got), &parsed); err != nil {
		t.Fatalf("invalid json: %v", err)
	}
	if parsed["Authorization"] != "[redacted]" {
		t.Fatalf("authorization: got %#v want redacted", parsed["Authorization"])
	}
	if parsed["Cookie"] != "[redacted]" {
		t.Fatalf("cookie: got %#v want redacted", parsed["Cookie"])
	}
	if parsed["User-Agent"] != "codex_exec/0.1.0" {
		t.Fatalf("user-agent: got %#v", parsed["User-Agent"])
	}
	if parsed["Accept"] != "text/event-stream" {
		t.Fatalf("accept: got %#v", parsed["Accept"])
	}
	if !strings.Contains(parsed["X-Long"].(string), "[truncated]") {
		t.Fatalf("long header was not truncated: %d", len(parsed["X-Long"].(string)))
	}
	values, ok := parsed["X-Codex-Beta-Features"].([]interface{})
	if !ok || len(values) != 2 {
		t.Fatalf("multi-value header: got %#v", parsed["X-Codex-Beta-Features"])
	}
}
