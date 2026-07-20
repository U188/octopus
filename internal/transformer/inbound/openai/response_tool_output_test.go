package openai

import (
	"context"
	"encoding/json"
	"testing"
)

// 回归：function_call_output/custom_tool_call_output 缺少 output 字段时
// 不应空指针 panic，而应作为空结果的 tool 消息处理。
func TestTransformRequestFunctionCallOutputWithoutOutput(t *testing.T) {
	body := []byte(`{
		"model": "gpt-4o",
		"input": [
			{"type": "function_call", "call_id": "call_1", "name": "get_weather", "arguments": "{}"},
			{"type": "function_call_output", "call_id": "call_1"}
		]
	}`)

	inbound := &ResponseInbound{}
	req, err := inbound.TransformRequest(context.Background(), body)
	if err != nil {
		t.Fatalf("TransformRequest failed: %v", err)
	}

	var toolMsg *struct {
		ToolCallID string
		Content    string
	}
	for _, msg := range req.Messages {
		if msg.Role != "tool" {
			continue
		}
		if msg.ToolCallID == nil {
			t.Fatalf("tool message missing tool_call_id: %+v", msg)
		}
		content := ""
		if msg.Content.Content != nil {
			content = *msg.Content.Content
		}
		toolMsg = &struct {
			ToolCallID string
			Content    string
		}{ToolCallID: *msg.ToolCallID, Content: content}
	}
	if toolMsg == nil {
		t.Fatalf("expected a tool message, got %+v", req.Messages)
	}
	if toolMsg.ToolCallID != "call_1" {
		t.Fatalf("expected tool_call_id call_1, got %q", toolMsg.ToolCallID)
	}
	if toolMsg.Content != "" {
		t.Fatalf("expected empty content for missing output, got %q", toolMsg.Content)
	}
}

// null output 与缺省等价，也不应 panic。
func TestTransformRequestFunctionCallOutputNullOutput(t *testing.T) {
	body := []byte(`{
		"model": "gpt-4o",
		"input": [
			{"type": "custom_tool_call_output", "call_id": "call_2", "output": null}
		]
	}`)

	inbound := &ResponseInbound{}
	req, err := inbound.TransformRequest(context.Background(), body)
	if err != nil {
		t.Fatalf("TransformRequest failed: %v", err)
	}
	raw, _ := json.Marshal(req.Messages)
	if req == nil || len(req.Messages) == 0 {
		t.Fatalf("expected messages, got %s", raw)
	}
}
