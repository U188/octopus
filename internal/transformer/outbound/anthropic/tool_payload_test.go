package anthropic

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"

	"github.com/U188/octopus/internal/transformer/model"
)

// anthropicWireMessages runs an OpenAI-shaped internal request through the
// Anthropic outbound and returns the decoded `messages` array actually sent.
// OpenAI-shaped input is the interesting direction: separate tool messages have
// to be folded into user turns, which is where pairing repairs matter.
func anthropicWireMessages(t *testing.T, messages []model.Message) []map[string]any {
	t.Helper()
	maxTokens := int64(100)
	out := &MessageOutbound{}
	httpReq, err := out.TransformRequest(context.Background(), &model.InternalLLMRequest{
		Model:        "claude-opus-5",
		RawAPIFormat: model.APIFormatOpenAIChatCompletion,
		Messages:     messages,
		MaxTokens:    &maxTokens,
	}, "https://example.com", "key")
	if err != nil {
		t.Fatalf("outbound transform failed: %v", err)
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
	return payload.Messages
}

func blocksOf(t *testing.T, msg map[string]any) []map[string]any {
	t.Helper()
	raw, ok := msg["content"].([]any)
	if !ok {
		t.Fatalf("expected a content block array: %+v", msg)
	}
	blocks := make([]map[string]any, 0, len(raw))
	for _, item := range raw {
		block, ok := item.(map[string]any)
		if !ok {
			t.Fatalf("unexpected block shape: %#v", item)
		}
		blocks = append(blocks, block)
	}
	return blocks
}

// An empty arguments string must reach Anthropic as an empty object: tool_use
// input is typed as an object.
func TestAnthropicToolUseInputIsAlwaysObject(t *testing.T) {
	for name, arguments := range map[string]string{
		"empty":   "",
		"null":    "null",
		"invalid": `{"broken":`,
	} {
		t.Run(name, func(t *testing.T) {
			messages := anthropicWireMessages(t, []model.Message{
				{Role: "user", Content: model.MessageContent{Content: strPtr("go")}},
				{Role: "assistant", ToolCalls: []model.ToolCall{
					{ID: "call_a", Type: "function", Function: model.FunctionCall{Name: "a", Arguments: arguments}},
				}},
				{Role: "tool", ToolCallID: strPtr("call_a"), Content: model.MessageContent{Content: strPtr("ok")}},
			})

			blocks := blocksOf(t, messages[1])
			input, ok := blocks[0]["input"].(map[string]any)
			if !ok {
				t.Fatalf("expected tool_use input to be an object, got %#v", blocks[0]["input"])
			}
			if len(input) != 0 {
				t.Fatalf("expected an empty input object, got %+v", input)
			}
		})
	}
}

// A tool result whose originating call is gone cannot be sent as a tool_result:
// Anthropic rejects a tool_use_id it never issued. It becomes user text.
func TestAnthropicOrphanedToolResultBecomesUserText(t *testing.T) {
	messages := anthropicWireMessages(t, []model.Message{
		{
			Role:         "tool",
			ToolCallID:   strPtr("call_gone"),
			ToolCallName: strPtr("lookup"),
			Content:      model.MessageContent{Content: strPtr("stale output")},
		},
		{Role: "user", Content: model.MessageContent{Content: strPtr("continue")}},
	})

	body, err := json.Marshal(messages)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}
	if strings.Contains(string(body), "tool_result") {
		t.Fatalf("orphaned tool result was forwarded as a tool_result: %s", body)
	}
	if !strings.Contains(string(body), "stale output") {
		t.Fatalf("expected the tool output to survive as text: %s", body)
	}
}

// The synthetic result for an unanswered call must land in the same user turn
// that follows it, which is the shape Anthropic expects.
func TestAnthropicUnansweredToolCallGetsSyntheticResult(t *testing.T) {
	messages := anthropicWireMessages(t, []model.Message{
		{Role: "user", Content: model.MessageContent{Content: strPtr("go")}},
		{Role: "assistant", ToolCalls: []model.ToolCall{
			{ID: "call_a", Type: "function", Function: model.FunctionCall{Name: "a", Arguments: "{}"}},
		}},
		{Role: "user", Content: model.MessageContent{Content: strPtr("never mind")}},
	})

	blocks := blocksOf(t, messages[2])
	if blocks[0]["type"] != "tool_result" || blocks[0]["tool_use_id"] != "call_a" {
		t.Fatalf("expected a synthetic tool_result for call_a first, got %+v", blocks)
	}
	if blocks[1]["type"] != "text" {
		t.Fatalf("expected the user text to follow the synthetic result, got %+v", blocks)
	}
}

// Parallel tool calls answered by separate tool messages must collapse into one
// user turn carrying both tool_result blocks, keeping each bound to its own ID.
func TestAnthropicParallelToolResultsShareOneUserTurn(t *testing.T) {
	messages := anthropicWireMessages(t, []model.Message{
		{Role: "user", Content: model.MessageContent{Content: strPtr("go")}},
		{Role: "assistant", ToolCalls: []model.ToolCall{
			{ID: "call_a", Type: "function", Function: model.FunctionCall{Name: "a", Arguments: `{"x":1}`}},
			{ID: "call_b", Type: "function", Function: model.FunctionCall{Name: "b", Arguments: `{"x":2}`}},
		}},
		{Role: "tool", ToolCallID: strPtr("call_a"), Content: model.MessageContent{Content: strPtr("r1")}},
		{Role: "tool", ToolCallID: strPtr("call_b"), Content: model.MessageContent{Content: strPtr("r2")}},
	})

	if len(messages) != 3 {
		t.Fatalf("expected the two tool results to share one user turn, got %d messages", len(messages))
	}
	blocks := blocksOf(t, messages[2])
	if len(blocks) != 2 {
		t.Fatalf("expected two tool_result blocks, got %+v", blocks)
	}
	for i, wantID := range []string{"call_a", "call_b"} {
		if blocks[i]["tool_use_id"] != wantID {
			t.Fatalf("block %d bound to %#v, want %q", i, blocks[i]["tool_use_id"], wantID)
		}
	}
}

func strPtr(v string) *string { return &v }
