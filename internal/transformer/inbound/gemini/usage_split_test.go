package gemini

import (
	"testing"

	"github.com/U188/octopus/internal/transformer/model"
)

// 回归：内部 completion_tokens 含 reasoning（OpenAI 语义）；回放给 Gemini
// 客户端时必须从 candidatesTokenCount 中剥离思考 token，否则两个字段双计。
func TestUsageToGeminiSplitsThoughtsFromCandidates(t *testing.T) {
	metadata := usageToGemini(&model.InternalLLMResponse{
		Usage: &model.Usage{
			PromptTokens:     100,
			CompletionTokens: 100, // 含 60 reasoning
			TotalTokens:      200,
			CompletionTokensDetails: &model.CompletionTokensDetails{
				ReasoningTokens: 60,
			},
		},
	})
	if metadata == nil {
		t.Fatal("nil metadata")
	}
	if metadata.CandidatesTokenCount != 40 {
		t.Fatalf("candidates must exclude thoughts (100-60), got %d", metadata.CandidatesTokenCount)
	}
	if metadata.ThoughtsTokenCount != 60 {
		t.Fatalf("thoughts count mismatch, got %d", metadata.ThoughtsTokenCount)
	}
	if metadata.TotalTokenCount != 200 {
		t.Fatalf("total mismatch, got %d", metadata.TotalTokenCount)
	}
}
