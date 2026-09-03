package openai

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"

	"github.com/U188/octopus/internal/transformer/model"
)

// responsesWireInput runs an internal request through the Responses outbound and
// returns the decoded `input` array actually sent upstream.
func responsesWireInput(t *testing.T, messages []model.Message) []map[string]any {
	t.Helper()
	maxTokens := int64(100)
	out := &ResponseOutbound{}
	httpReq, err := out.TransformRequest(context.Background(), &model.InternalLLMRequest{
		Model:               "gpt-5",
		RawAPIFormat:        model.APIFormatOpenAIResponse,
		Messages:            messages,
		MaxCompletionTokens: &maxTokens,
	}, "https://example.com/v1", "key")
	if err != nil {
		t.Fatalf("outbound transform failed: %v", err)
	}
	body, err := io.ReadAll(httpReq.Body)
	if err != nil {
		t.Fatalf("failed to read request body: %v", err)
	}
	var payload struct {
		Input []map[string]any `json:"input"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("failed to unmarshal request body: %v\n%s", err, body)
	}
	return payload.Input
}

func itemOfType(t *testing.T, items []map[string]any, itemType string) map[string]any {
	t.Helper()
	for _, item := range items {
		if item["type"] == itemType {
			return item
		}
	}
	t.Fatalf("no %q item in %+v", itemType, items)
	return nil
}

// A function_call's arguments are unmarshalled by the upstream, so an empty
// string is rejected as malformed.
func TestResponsesFunctionCallArgumentsNeverEmpty(t *testing.T) {
	for name, arguments := range map[string]string{
		"empty": "",
		"null":  "null",
	} {
		t.Run(name, func(t *testing.T) {
			items := responsesWireInput(t, []model.Message{
				{Role: "user", Content: model.MessageContent{Content: ptrString("go")}},
				{Role: "assistant", ToolCalls: []model.ToolCall{
					{ID: "call_a", Type: "function", Function: model.FunctionCall{Name: "a", Arguments: arguments}},
				}},
				{Role: "tool", ToolCallID: ptrString("call_a"), Content: model.MessageContent{Content: ptrString("ok")}},
			})

			call := itemOfType(t, items, "function_call")
			if call["arguments"] != "{}" {
				t.Fatalf("expected arguments %q, got %#v", "{}", call["arguments"])
			}
		})
	}
}

// An unanswered function_call must gain a function_call_output: the Responses
// API rejects a call with no output in the same input array.
func TestResponsesUnansweredCallGetsOutput(t *testing.T) {
	items := responsesWireInput(t, []model.Message{
		{Role: "user", Content: model.MessageContent{Content: ptrString("go")}},
		{Role: "assistant", ToolCalls: []model.ToolCall{
			{ID: "call_a", Type: "function", Function: model.FunctionCall{Name: "a", Arguments: "{}"}},
		}},
		{Role: "user", Content: model.MessageContent{Content: ptrString("never mind")}},
	})

	output := itemOfType(t, items, "function_call_output")
	if output["call_id"] != "call_a" {
		t.Fatalf("expected the synthetic output to bind call_a, got %+v", output)
	}
}

// A function_call_output whose call is gone cannot bind; it becomes user text.
func TestResponsesOrphanedOutputBecomesUserMessage(t *testing.T) {
	items := responsesWireInput(t, []model.Message{
		{
			Role:         "tool",
			ToolCallID:   ptrString("call_gone"),
			ToolCallName: ptrString("lookup"),
			Content:      model.MessageContent{Content: ptrString("stale output")},
		},
		{Role: "user", Content: model.MessageContent{Content: ptrString("continue")}},
	})

	body, err := json.Marshal(items)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}
	if strings.Contains(string(body), "function_call_output") {
		t.Fatalf("orphaned output was forwarded verbatim: %s", body)
	}
	if !strings.Contains(string(body), "stale output") {
		t.Fatalf("expected the tool output to survive as text: %s", body)
	}
}

func ptrString(v string) *string { return &v }
