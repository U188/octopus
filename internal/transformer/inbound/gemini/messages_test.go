package gemini

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/U188/octopus/internal/transformer/model"
)

func TestTransformRequestUsesPathModelAndGeminiContents(t *testing.T) {
	in := &MessagesInbound{}
	ctx := WithRequestInfo(context.Background(), "gemini-2.5-flash", true)
	req, err := in.TransformRequest(ctx, []byte(`{
		"contents":[{"role":"user","parts":[{"text":"hello"}]}],
		"generationConfig":{"temperature":0.2,"maxOutputTokens":32}
	}`))
	if err != nil {
		t.Fatalf("TransformRequest failed: %v", err)
	}
	if req.Model != "gemini-2.5-flash" {
		t.Fatalf("Model = %q", req.Model)
	}
	if req.RawAPIFormat != model.APIFormatGeminiContents {
		t.Fatalf("RawAPIFormat = %q", req.RawAPIFormat)
	}
	if req.Stream == nil || !*req.Stream {
		t.Fatalf("expected stream request")
	}
	if len(req.Messages) != 1 || req.Messages[0].Role != "user" || req.Messages[0].Content.Content == nil || *req.Messages[0].Content.Content != "hello" {
		t.Fatalf("unexpected messages: %+v", req.Messages)
	}
	if req.MaxTokens == nil || *req.MaxTokens != 32 {
		t.Fatalf("MaxTokens = %v", req.MaxTokens)
	}
}

func TestTransformResponseReturnsGeminiCandidate(t *testing.T) {
	in := &MessagesInbound{}
	text := "ok"
	finish := "stop"
	body, err := in.TransformResponse(context.Background(), &model.InternalLLMResponse{
		ID:      "resp_1",
		Created: 1,
		Model:   "gemini-2.5-flash",
		Choices: []model.Choice{{
			Index:        0,
			Message:      &model.Message{Role: "assistant", Content: model.MessageContent{Content: &text}},
			FinishReason: &finish,
		}},
		Usage: &model.Usage{PromptTokens: 2, CompletionTokens: 1, TotalTokens: 3},
	})
	if err != nil {
		t.Fatalf("TransformResponse failed: %v", err)
	}
	var resp model.GeminiGenerateContentResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if resp.ResponseId != "resp_1" || resp.ModelVersion != "gemini-2.5-flash" {
		t.Fatalf("unexpected response metadata: %+v", resp)
	}
	if len(resp.Candidates) != 1 || resp.Candidates[0].Content == nil || len(resp.Candidates[0].Content.Parts) != 1 || resp.Candidates[0].Content.Parts[0].Text != "ok" {
		t.Fatalf("unexpected candidates: %+v", resp.Candidates)
	}
	if resp.Candidates[0].FinishReason == nil || *resp.Candidates[0].FinishReason != "STOP" {
		t.Fatalf("FinishReason = %v", resp.Candidates[0].FinishReason)
	}
	if resp.UsageMetadata == nil || resp.UsageMetadata.TotalTokenCount != 3 {
		t.Fatalf("UsageMetadata = %+v", resp.UsageMetadata)
	}
}

func TestTransformStreamReturnsGeminiSSE(t *testing.T) {
	in := &MessagesInbound{}
	text := "delta"
	body, err := in.TransformStream(context.Background(), &model.InternalLLMResponse{
		ID:    "resp_1",
		Model: "gemini-2.5-flash",
		Choices: []model.Choice{{
			Index: 0,
			Delta: &model.Message{Role: "assistant", Content: model.MessageContent{Content: &text}},
		}},
	})
	if err != nil {
		t.Fatalf("TransformStream failed: %v", err)
	}
	if !strings.HasPrefix(string(body), "data: ") || !strings.HasSuffix(string(body), "\n\n") {
		t.Fatalf("expected SSE data frame, got %q", body)
	}
}
