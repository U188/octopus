package gemini

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"

	"github.com/U188/octopus/internal/transformer/model"
)

// geminiWireContents runs an internal request through the Gemini outbound and
// returns the decoded `contents` array actually sent upstream.
func geminiWireContents(t *testing.T, messages []model.Message) []map[string]any {
	t.Helper()
	maxTokens := int64(100)
	out := &MessagesOutbound{}
	httpReq, err := out.TransformRequest(context.Background(), &model.InternalLLMRequest{
		Model:        "gemini-3-pro",
		RawAPIFormat: model.APIFormatOpenAIChatCompletion,
		Messages:     messages,
		MaxTokens:    &maxTokens,
	}, "https://example.com/v1beta", "key")
	if err != nil {
		t.Fatalf("outbound transform failed: %v", err)
	}
	body, err := io.ReadAll(httpReq.Body)
	if err != nil {
		t.Fatalf("failed to read request body: %v", err)
	}
	var payload struct {
		Contents []map[string]any `json:"contents"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("failed to unmarshal request body: %v\n%s", err, body)
	}
	return payload.Contents
}

func partsOf(t *testing.T, content map[string]any) []map[string]any {
	t.Helper()
	raw, ok := content["parts"].([]any)
	if !ok {
		t.Fatalf("expected a parts array: %+v", content)
	}
	parts := make([]map[string]any, 0, len(raw))
	for _, item := range raw {
		part, ok := item.(map[string]any)
		if !ok {
			t.Fatalf("unexpected part shape: %#v", item)
		}
		parts = append(parts, part)
	}
	return parts
}

// Gemini types functionCall.args as an object. `args` carries omitempty, so a
// no-argument call must omit the key entirely rather than send `null`, which the
// API rejects. Unparseable arguments degrade to the same shape.
func TestGeminiFunctionCallArgsIsNeverNull(t *testing.T) {
	for name, arguments := range map[string]string{
		"empty":   "",
		"null":    "null",
		"invalid": `{"broken":`,
	} {
		t.Run(name, func(t *testing.T) {
			contents := geminiWireContents(t, []model.Message{
				{Role: "user", Content: model.MessageContent{Content: strPtr("go")}},
				{Role: "assistant", ToolCalls: []model.ToolCall{
					{ID: "call_a", Type: "function", Function: model.FunctionCall{Name: "a", Arguments: arguments}},
				}},
				{Role: "tool", ToolCallID: strPtr("call_a"), ToolCallName: strPtr("a"), Content: model.MessageContent{Content: strPtr("ok")}},
			})

			parts := partsOf(t, contents[1])
			call, ok := parts[0]["functionCall"].(map[string]any)
			if !ok {
				t.Fatalf("expected a functionCall part, got %+v", parts[0])
			}
			args, present := call["args"]
			if present && args == nil {
				t.Fatalf("functionCall.args must never be null: %+v", call)
			}
			if present {
				if object, ok := args.(map[string]any); !ok || len(object) != 0 {
					t.Fatalf("expected args to be absent or an empty object, got %#v", args)
				}
			}
		})
	}
}

// Real arguments must survive verbatim.
func TestGeminiFunctionCallArgsPassThrough(t *testing.T) {
	contents := geminiWireContents(t, []model.Message{
		{Role: "user", Content: model.MessageContent{Content: strPtr("go")}},
		{Role: "assistant", ToolCalls: []model.ToolCall{
			{ID: "call_a", Type: "function", Function: model.FunctionCall{Name: "a", Arguments: `{"path":"/tmp"}`}},
		}},
		{Role: "tool", ToolCallID: strPtr("call_a"), ToolCallName: strPtr("a"), Content: model.MessageContent{Content: strPtr("ok")}},
	})

	parts := partsOf(t, contents[1])
	call := parts[0]["functionCall"].(map[string]any)
	args, ok := call["args"].(map[string]any)
	if !ok {
		t.Fatalf("expected args object, got %#v", call["args"])
	}
	if args["path"] != "/tmp" {
		t.Fatalf("arguments were not forwarded verbatim: %+v", args)
	}
}

// Gemini rejects a Content whose parts array is empty. A turn holding only
// server-tool blocks loses all of them, so it must fall back to a placeholder
// rather than being emitted empty.
func TestGeminiServerToolOnlyTurnKeepsParts(t *testing.T) {
	contents := geminiWireContents(t, []model.Message{
		{
			Role: "assistant",
			Content: model.MessageContent{MultipleContent: []model.MessageContentPart{{
				Type:          "server_tool_use",
				ServerToolUse: &model.ServerToolUseBlock{ID: "srv_1", Name: "web_search"},
			}}},
		},
		{Role: "user", Content: model.MessageContent{Content: strPtr("go")}},
	})

	for i, content := range contents {
		if len(partsOf(t, content)) == 0 {
			t.Fatalf("content %d emitted an empty parts array: %+v", i, contents)
		}
	}
}

// A functionResponse whose name matches no prior functionCall is rejected with
// INVALID_ARGUMENT, so an unbindable tool result becomes plain user text.
func TestGeminiOrphanedToolResultBecomesUserText(t *testing.T) {
	contents := geminiWireContents(t, []model.Message{
		{
			Role:         "tool",
			ToolCallID:   strPtr("call_gone"),
			ToolCallName: strPtr("lookup"),
			Content:      model.MessageContent{Content: strPtr("stale output")},
		},
		{Role: "user", Content: model.MessageContent{Content: strPtr("continue")}},
	})

	body, err := json.Marshal(contents)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}
	if strings.Contains(string(body), "functionResponse") {
		t.Fatalf("orphaned tool result was forwarded as a functionResponse: %s", body)
	}
	if !strings.Contains(string(body), "stale output") {
		t.Fatalf("expected the tool output to survive as text: %s", body)
	}
}

// An unanswered functionCall gets a synthetic functionResponse so the turn is
// complete.
func TestGeminiUnansweredToolCallGetsSyntheticResponse(t *testing.T) {
	contents := geminiWireContents(t, []model.Message{
		{Role: "user", Content: model.MessageContent{Content: strPtr("go")}},
		{Role: "assistant", ToolCalls: []model.ToolCall{
			{ID: "call_a", Type: "function", Function: model.FunctionCall{Name: "a", Arguments: "{}"}},
		}},
		{Role: "user", Content: model.MessageContent{Content: strPtr("never mind")}},
	})

	body, err := json.Marshal(contents)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}
	if !strings.Contains(string(body), "functionResponse") {
		t.Fatalf("expected a synthetic functionResponse for call_a: %s", body)
	}
}

func strPtr(v string) *string { return &v }
