package openai

import (
	"context"
	"encoding/json"
	"io"
	"testing"

	"github.com/U188/octopus/internal/transformer/model"
	outboundAnthropic "github.com/U188/octopus/internal/transformer/outbound/anthropic"
)

// The relay reuses one InternalLLMRequest across channel retries, and the
// outbound transformers mutate its Messages in place. A request that failed over
// from an Anthropic channel to a Chat channel therefore arrives already
// alternation-merged and already repaired; the second transform must still emit
// a valid body rather than compounding the repairs.
func TestChatRequestSurvivesAnthropicChannelFailover(t *testing.T) {
	maxTokens := int64(100)
	req := &model.InternalLLMRequest{
		Model:        "claude-opus-5",
		RawAPIFormat: model.APIFormatAnthropicMessage,
		MaxTokens:    &maxTokens,
		Messages: []model.Message{
			{Role: "user", Content: model.MessageContent{Content: ptrString("go")}},
			{Role: "assistant", ToolCalls: []model.ToolCall{
				{ID: "call_a", Type: "function", Function: model.FunctionCall{Name: "a", Arguments: ""}},
				{ID: "call_b", Type: "function", Function: model.FunctionCall{Name: "b", Arguments: "{}"}},
			}},
			{Role: "tool", ToolCallID: ptrString("call_a"), Content: model.MessageContent{Content: ptrString("r1")}},
			{Role: "user", Content: model.MessageContent{Content: ptrString("continue")}},
		},
	}

	anthropicOut := &outboundAnthropic.MessageOutbound{}
	if _, err := anthropicOut.TransformRequest(context.Background(), req, "https://example.com", "key"); err != nil {
		t.Fatalf("anthropic transform failed: %v", err)
	}

	chatOut := &ChatOutbound{}
	httpReq, err := chatOut.TransformRequest(context.Background(), req, "https://example.com/v1", "key")
	if err != nil {
		t.Fatalf("chat transform failed: %v", err)
	}
	body, err := io.ReadAll(httpReq.Body)
	if err != nil {
		t.Fatalf("failed to read request body: %v", err)
	}
	var payload struct {
		Messages []map[string]any `json:"messages"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("failed to unmarshal request body: %v\n%s", err, body)
	}

	assertNoNullContent(t, payload.Messages)

	// Every tool call must still be answered, and every tool message must still
	// bind to a call the assistant actually made.
	calls := make(map[string]int)
	for _, msg := range payload.Messages {
		if raw, ok := msg["tool_calls"].([]any); ok {
			for _, item := range raw {
				call := item.(map[string]any)
				calls[call["id"].(string)]++
			}
		}
	}
	answered := make(map[string]int)
	for _, msg := range payload.Messages {
		if msg["role"] != "tool" {
			continue
		}
		id, ok := msg["tool_call_id"].(string)
		if !ok {
			t.Fatalf("tool message without a tool_call_id survived: %+v", msg)
		}
		if calls[id] == 0 {
			t.Fatalf("tool message binds unknown call %q: %+v", id, payload.Messages)
		}
		answered[id]++
	}
	for id := range calls {
		if answered[id] != 1 {
			t.Fatalf("call %q has %d results, want exactly 1: %+v", id, answered[id], payload.Messages)
		}
	}
}
