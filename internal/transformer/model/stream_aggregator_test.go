package model

import "testing"

func TestMergeToolCallDeltaDoesNotDuplicateFunctionName(t *testing.T) {
	toolCalls := []ToolCall{{
		Index: 0,
		ID:    "call_1",
		Type:  "function",
		Function: FunctionCall{
			Name: "Write",
		},
	}}

	toolCalls = MergeToolCallDelta(toolCalls, ToolCall{
		Index: 0,
		Function: FunctionCall{
			Name:      "Write",
			Arguments: `{"file_path":`,
		},
	})
	toolCalls = MergeToolCallDelta(toolCalls, ToolCall{
		Index: 0,
		Function: FunctionCall{
			Name:      "Write",
			Arguments: `"a.txt"}`,
		},
	})

	if len(toolCalls) != 1 {
		t.Fatalf("expected one tool call, got %d", len(toolCalls))
	}
	if toolCalls[0].Function.Name != "Write" {
		t.Fatalf("function name duplicated: %q", toolCalls[0].Function.Name)
	}
	if toolCalls[0].Function.Arguments != `{"file_path":"a.txt"}` {
		t.Fatalf("arguments not merged: %q", toolCalls[0].Function.Arguments)
	}
}

func TestMergeToolCallDeltaSetsFunctionNameWhenMissing(t *testing.T) {
	toolCalls := []ToolCall{{Index: 0, Type: "function"}}

	toolCalls = MergeToolCallDelta(toolCalls, ToolCall{
		Index: 0,
		Function: FunctionCall{
			Name: "Search",
		},
	})

	if len(toolCalls) != 1 {
		t.Fatalf("expected one tool call, got %d", len(toolCalls))
	}
	if toolCalls[0].Function.Name != "Search" {
		t.Fatalf("function name not set: %q", toolCalls[0].Function.Name)
	}
}
