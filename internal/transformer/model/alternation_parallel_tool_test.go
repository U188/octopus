package model

import "testing"

// 回归：OpenAI 并行 tool calls 产生的连续 tool 消息各带不同 ToolCallID，
// 物理合并只能保留第一个 ID，其余结果会错位或被上游 400。
// 期望：保持消息独立，并共享同一个合成 MessageIndex 供 outbound 归并。
func TestEnforceAlternationKeepsParallelToolResults(t *testing.T) {
	msgs := []Message{
		{Role: "user", Content: MessageContent{Content: strPtr("check weather in two cities")}},
		{Role: "assistant", ToolCalls: []ToolCall{
			{ID: "call_a", Type: "function", Function: FunctionCall{Name: "weather", Arguments: `{"city":"a"}`}},
			{ID: "call_b", Type: "function", Function: FunctionCall{Name: "weather", Arguments: `{"city":"b"}`}},
		}},
		{Role: "tool", ToolCallID: strPtr("call_a"), Content: MessageContent{Content: strPtr("sunny")}},
		{Role: "tool", ToolCallID: strPtr("call_b"), Content: MessageContent{Content: strPtr("rainy")}},
	}

	for _, provider := range []AlternationProvider{AlternationProviderAnthropic, AlternationProviderGemini} {
		out := EnforceAlternation(msgs, provider)
		var tools []Message
		for _, m := range out {
			if m.Role == "tool" {
				tools = append(tools, m)
			}
		}
		if len(tools) != 2 {
			t.Fatalf("provider %s: expected 2 independent tool messages, got %d (out=%+v)", provider, len(tools), out)
		}
		gotIDs := map[string]bool{}
		for _, tm := range tools {
			if tm.ToolCallID == nil {
				t.Fatalf("provider %s: tool message lost its ToolCallID", provider)
			}
			gotIDs[*tm.ToolCallID] = true
			if tm.MessageIndex == nil {
				t.Fatalf("provider %s: tool message missing synthetic MessageIndex", provider)
			}
		}
		if !gotIDs["call_a"] || !gotIDs["call_b"] {
			t.Fatalf("provider %s: expected both call IDs preserved, got %v", provider, gotIDs)
		}
		if *tools[0].MessageIndex != *tools[1].MessageIndex {
			t.Fatalf("provider %s: parallel tool results must share one MessageIndex, got %d vs %d",
				provider, *tools[0].MessageIndex, *tools[1].MessageIndex)
		}
		if *tools[0].MessageIndex >= 0 {
			t.Fatalf("provider %s: synthetic index must be negative to avoid colliding with inbound indexes, got %d",
				provider, *tools[0].MessageIndex)
		}
	}
}

// 同一 ToolCallID 的连续 tool 消息仍走合并路径（保持既有行为）。
func TestEnforceAlternationStillMergesSameToolCallID(t *testing.T) {
	msgs := []Message{
		{Role: "user", Content: MessageContent{Content: strPtr("hi")}},
		{Role: "assistant", Content: MessageContent{Content: strPtr("calling")}},
		{Role: "tool", ToolCallID: strPtr("call_a"), Content: MessageContent{Content: strPtr("part 1")}},
		{Role: "tool", ToolCallID: strPtr("call_a"), Content: MessageContent{Content: strPtr("part 2")}},
	}
	out := EnforceAlternation(msgs, AlternationProviderAnthropic)
	toolCount := 0
	for _, m := range out {
		if m.Role == "tool" {
			toolCount++
		}
	}
	if toolCount != 1 {
		t.Fatalf("same-ID tool run should still merge into one message, got %d", toolCount)
	}
}
