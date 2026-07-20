package gemini

import (
	"context"
	"encoding/json"
	"io"
	"testing"

	"github.com/U188/octopus/internal/transformer/model"
)

// 回归：assistant 历史消息可能携带数组形式内容（MultipleContent）；
// 原实现只读字符串字段，会重放出空 parts 的 model 轮并被 Gemini 400。
func TestTransformRequestKeepsAssistantMultipleContent(t *testing.T) {
	outbound := &MessagesOutbound{}
	req := &model.InternalLLMRequest{
		Model: "gemini-2.5-pro",
		Messages: []model.Message{
			{Role: "user", Content: model.MessageContent{Content: stringPtr("hi")}},
			{Role: "assistant", Content: model.MessageContent{MultipleContent: []model.MessageContentPart{
				{Type: "text", Text: stringPtr("answer part one")},
				{Type: "text", Text: stringPtr("answer part two")},
			}}},
			{Role: "user", Content: model.MessageContent{Content: stringPtr("continue")}},
		},
	}

	httpReq, err := outbound.TransformRequest(context.Background(), req, "https://example.com", "key")
	if err != nil {
		t.Fatalf("TransformRequest: %v", err)
	}
	body, err := io.ReadAll(httpReq.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}

	var payload struct {
		Contents []struct {
			Role  string `json:"role"`
			Parts []struct {
				Text string `json:"text"`
			} `json:"parts"`
		} `json:"contents"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("unmarshal request body: %v", err)
	}

	var modelParts []string
	for _, content := range payload.Contents {
		if content.Role != "model" {
			continue
		}
		for _, part := range content.Parts {
			if part.Text != "" {
				modelParts = append(modelParts, part.Text)
			}
		}
	}
	if len(modelParts) != 2 || modelParts[0] != "answer part one" || modelParts[1] != "answer part two" {
		t.Fatalf("assistant MultipleContent lost in replay, got parts %v (body=%s)", modelParts, body)
	}
}

// 回归：Gemini 的 candidatesTokenCount 不含思考 token；内部 usage 采用
// OpenAI 语义（completion 含 reasoning），换算时必须相加，否则输出计费少算。
func TestConvertGeminiUsageMetadataAddsThoughtsToCompletion(t *testing.T) {
	usage := convertGeminiUsageMetadata(&model.GeminiUsageMetadata{
		PromptTokenCount:     100,
		CandidatesTokenCount: 40,
		ThoughtsTokenCount:   60,
		TotalTokenCount:      200,
	})
	if usage == nil {
		t.Fatal("nil usage")
	}
	if usage.CompletionTokens != 100 {
		t.Fatalf("completion must include thoughts (40+60), got %d", usage.CompletionTokens)
	}
	if usage.CompletionTokensDetails == nil || usage.CompletionTokensDetails.ReasoningTokens != 60 {
		t.Fatalf("reasoning detail mismatch: %+v", usage.CompletionTokensDetails)
	}
	if usage.TotalTokens != 200 {
		t.Fatalf("total must stay as reported, got %d", usage.TotalTokens)
	}
}
