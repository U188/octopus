package relay

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/U188/octopus/internal/model"
	transformerModel "github.com/U188/octopus/internal/transformer/model"
	"github.com/U188/octopus/internal/utils/tokenizer"
)

// usage 完全缺失时，应估算实际传输输入和已生成的文本输出。
func TestSetInternalResponseFallbackWhenUsageMissing(t *testing.T) {
	m := &RelayMetrics{TransportInputTokens: intPtr(123)}
	text := "fallback output"
	m.SetInternalResponse(&transformerModel.InternalLLMResponse{
		Choices: []transformerModel.Choice{{
			Message: &transformerModel.Message{Content: transformerModel.MessageContent{Content: &text}},
		}},
	}, "test-model")

	if m.Stats.InputToken != 123 {
		t.Fatalf("input token: got %d want 123 (fallback)", m.Stats.InputToken)
	}
	if m.BillInputTokens == nil || *m.BillInputTokens != 123 {
		t.Fatalf("bill input tokens: got %v want 123", m.BillInputTokens)
	}
	wantOutput := tokenizer.CountTokens(text+"\n", "test-model")
	if m.Stats.OutputToken != int64(wantOutput) {
		t.Fatalf("output token: got %d want %d (fallback)", m.Stats.OutputToken, wantOutput)
	}
	if m.InputTokenSource != model.TokenCountSourceEstimated || m.OutputTokenSource != model.TokenCountSourceEstimated {
		t.Fatalf("unexpected token sources: input=%q output=%q", m.InputTokenSource, m.OutputTokenSource)
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
	if m.InputTokenSource != model.TokenCountSourceEstimated || m.OutputTokenSource != model.TokenCountSourceReported {
		t.Fatalf("unexpected token sources: input=%q output=%q", m.InputTokenSource, m.OutputTokenSource)
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
	if m.InputTokenSource != model.TokenCountSourceReported || m.OutputTokenSource != model.TokenCountSourceReported {
		t.Fatalf("unexpected token sources: input=%q output=%q", m.InputTokenSource, m.OutputTokenSource)
	}
}

// 仅缓存命中（input_tokens=0 但 cache_read>0）属于已上报输入，不应被估算覆盖，
// 聚合口径应计入缓存读取 Token。
func TestSetInternalResponseNoFallbackWhenCacheOnly(t *testing.T) {
	m := &RelayMetrics{TransportInputTokens: intPtr(999)}
	m.SetInternalResponse(&transformerModel.InternalLLMResponse{
		Usage: &transformerModel.Usage{PromptTokens: 0, CacheReadInputTokens: 40, CompletionTokens: 5},
	}, "test-model")

	if m.Stats.InputToken != 40 {
		t.Fatalf("input token: got %d want 40 (cache-only is reported input)", m.Stats.InputToken)
	}
	if m.InputTokenSource != model.TokenCountSourceReported {
		t.Fatalf("input token source: got %q want reported", m.InputTokenSource)
	}
}

func TestSetInternalResponseEstimatesZeroReportedOutputWhenContentExists(t *testing.T) {
	text := "the upstream returned content but zero output usage"
	m := &RelayMetrics{}
	m.SetInternalResponse(&transformerModel.InternalLLMResponse{
		Usage: &transformerModel.Usage{PromptTokens: 12},
		Choices: []transformerModel.Choice{{
			Message: &transformerModel.Message{Content: transformerModel.MessageContent{Content: &text}},
		}},
	}, "test-model")

	if m.Stats.OutputToken <= 0 || m.OutputTokenSource != model.TokenCountSourceEstimated {
		t.Fatalf("expected estimated output, got tokens=%d source=%q", m.Stats.OutputToken, m.OutputTokenSource)
	}
}

func TestSetInternalResponseEmbeddingOutputNotApplicable(t *testing.T) {
	m := &RelayMetrics{TransportInputTokens: intPtr(7)}
	m.SetInternalResponse(&transformerModel.InternalLLMResponse{
		EmbeddingData: []transformerModel.EmbeddingObject{{}},
	}, "embedding-model")

	if m.OutputTokenSource != model.TokenCountSourceNotApplicable || m.Stats.OutputToken != 0 {
		t.Fatalf("unexpected embedding output: tokens=%d source=%q", m.Stats.OutputToken, m.OutputTokenSource)
	}
}

func TestEstimateGeneratedOutputTokensCountsTextReasoningAndToolsOnly(t *testing.T) {
	text := "answer"
	reasoning := "reasoning"
	imageURL := "data:image/png;base64," + strings.Repeat("A", 10000)
	resp := &transformerModel.InternalLLMResponse{
		Choices: []transformerModel.Choice{{
			Message: &transformerModel.Message{
				Content: transformerModel.MessageContent{MultipleContent: []transformerModel.MessageContentPart{
					{Type: "text", Text: &text},
					{Type: "image_url", ImageURL: &transformerModel.ImageURL{URL: imageURL}},
				}},
				ReasoningContent: &reasoning,
				ToolCalls: []transformerModel.ToolCall{{
					Function: transformerModel.FunctionCall{Name: "lookup", Arguments: `{"q":"octopus"}`},
				}},
			},
		}},
	}
	want := tokenizer.CountTokens("answer\nreasoning\nlookup\n{\"q\":\"octopus\"}\n", "test-model")
	if got := estimateGeneratedOutputTokens(resp, "test-model"); got != want {
		t.Fatalf("estimated output tokens: got %d want %d", got, want)
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
