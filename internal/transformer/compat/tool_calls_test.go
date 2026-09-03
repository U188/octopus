package compat

import (
	"testing"

	"github.com/U188/octopus/internal/transformer/model"
)

func TestFixOrphanedToolCallsInsertsMissingResults(t *testing.T) {
	followup := "next"
	messages := []model.Message{
		{
			Role: "assistant",
			ToolCalls: []model.ToolCall{
				{ID: "call_a", Function: model.FunctionCall{Name: "lookup"}},
				{ID: "call_b", Function: model.FunctionCall{Name: "search"}},
			},
		},
		{
			Role:       "tool",
			ToolCallID: stringPtr("call_a"),
			Content:    model.MessageContent{Content: stringPtr("ok")},
		},
		{Role: "user", Content: model.MessageContent{Content: &followup}},
	}

	got := FixOrphanedToolCalls(messages)
	if len(got) != 4 {
		t.Fatalf("expected one synthetic tool result, got %d messages: %+v", len(got), got)
	}
	if got[1].Role != "tool" || got[1].ToolCallID == nil || *got[1].ToolCallID != "call_b" {
		t.Fatalf("unexpected synthetic tool result: %+v", got[1])
	}
	if got[1].Content.Content == nil || *got[1].Content.Content != "" {
		t.Fatalf("expected empty synthetic content, got %+v", got[1].Content)
	}
	if got[2].Role != "tool" || got[2].ToolCallID == nil || *got[2].ToolCallID != "call_a" {
		t.Fatalf("existing tool result was not preserved after synthetic result: %+v", got[2])
	}
}

func TestFixOrphanedToolCallsStopsAtNextAssistant(t *testing.T) {
	messages := []model.Message{
		{
			Role:      "assistant",
			ToolCalls: []model.ToolCall{{ID: "call_a", Function: model.FunctionCall{Name: "lookup"}}},
		},
		{
			Role:      "assistant",
			ToolCalls: []model.ToolCall{{ID: "call_b", Function: model.FunctionCall{Name: "search"}}},
		},
		{
			Role:       "tool",
			ToolCallID: stringPtr("call_a"),
		},
	}

	got := FixOrphanedToolCalls(messages)
	if len(got) != 5 {
		t.Fatalf("expected both assistant turns to be patched independently, got %+v", got)
	}
	if got[1].ToolCallID == nil || *got[1].ToolCallID != "call_a" {
		t.Fatalf("first assistant was not patched before next assistant: %+v", got)
	}
	if got[3].ToolCallID == nil || *got[3].ToolCallID != "call_b" {
		t.Fatalf("second assistant was not patched: %+v", got)
	}
}

// A tool result whose originating assistant call is absent (truncated history)
// cannot bind on any upstream. It becomes a user turn so the output survives.
func TestRepairToolResultsDowngradesOrphanedResult(t *testing.T) {
	messages := []model.Message{
		{
			Role:         "tool",
			ToolCallID:   stringPtr("call_gone"),
			ToolCallName: stringPtr("lookup"),
			Content:      model.MessageContent{Content: stringPtr("stale output")},
		},
		{Role: "user", Content: model.MessageContent{Content: stringPtr("continue")}},
	}

	got := RepairToolResults(messages)
	if len(got) != 2 {
		t.Fatalf("expected message count to be preserved, got %+v", got)
	}
	if got[0].Role != "user" {
		t.Fatalf("expected orphaned tool result to become a user message, got %+v", got[0])
	}
	if got[0].ToolCallID != nil {
		t.Fatalf("downgraded message must not keep a tool_call_id: %+v", got[0])
	}
	if got[0].Content.Content == nil || *got[0].Content.Content != "Tool result (lookup): stale output" {
		t.Fatalf("expected labelled tool output, got %+v", got[0].Content)
	}
}

// A tool result whose call exists earlier in the conversation is left alone.
func TestRepairToolResultsKeepsBoundResult(t *testing.T) {
	messages := []model.Message{
		{
			Role:      "assistant",
			ToolCalls: []model.ToolCall{{ID: "call_a", Function: model.FunctionCall{Name: "lookup"}}},
		},
		{
			Role:       "tool",
			ToolCallID: stringPtr("call_a"),
			Content:    model.MessageContent{Content: stringPtr("ok")},
		},
	}

	got := RepairToolResults(messages)
	if len(got) != 2 {
		t.Fatalf("expected no repair, got %+v", got)
	}
	if got[1].Role != "tool" || got[1].ToolCallID == nil || *got[1].ToolCallID != "call_a" {
		t.Fatalf("bound tool result was modified: %+v", got[1])
	}
}

// A tool result that arrives before its own assistant call is still orphaned:
// the upstream sees messages in order and cannot bind forward.
func TestRepairToolResultsDowngradesResultBeforeCall(t *testing.T) {
	messages := []model.Message{
		{
			Role:       "tool",
			ToolCallID: stringPtr("call_a"),
			Content:    model.MessageContent{Content: stringPtr("early")},
		},
		{
			Role:      "assistant",
			ToolCalls: []model.ToolCall{{ID: "call_a", Function: model.FunctionCall{Name: "lookup"}}},
		},
	}

	got := RepairToolResults(messages)
	if got[0].Role != "user" {
		t.Fatalf("expected forward-referencing tool result to be downgraded, got %+v", got[0])
	}
}

// A tool message with no ID cannot be bound by any upstream either.
func TestRepairToolResultsDowngradesResultWithoutID(t *testing.T) {
	messages := []model.Message{
		{Role: "user", Content: model.MessageContent{Content: stringPtr("go")}},
		{Role: "tool", Content: model.MessageContent{Content: stringPtr("orphan")}},
	}

	got := RepairToolResults(messages)
	if got[1].Role != "user" {
		t.Fatalf("expected ID-less tool result to be downgraded, got %+v", got[1])
	}
}

// A second result for the same call ID is a duplicate binding, which the
// upstream rejects; only the first keeps its tool role.
func TestRepairToolResultsDowngradesDuplicateResult(t *testing.T) {
	messages := []model.Message{
		{
			Role:      "assistant",
			ToolCalls: []model.ToolCall{{ID: "call_a", Function: model.FunctionCall{Name: "lookup"}}},
		},
		{Role: "tool", ToolCallID: stringPtr("call_a"), Content: model.MessageContent{Content: stringPtr("first")}},
		{Role: "tool", ToolCallID: stringPtr("call_a"), Content: model.MessageContent{Content: stringPtr("second")}},
	}

	got := RepairToolResults(messages)
	if got[1].Role != "tool" {
		t.Fatalf("expected the first result to keep its tool role, got %+v", got[1])
	}
	if got[2].Role != "user" {
		t.Fatalf("expected the duplicate result to be downgraded, got %+v", got[2])
	}
}

// The downgraded message goes through Normalize, so an empty tool result does
// not become an empty user turn (which Anthropic rejects with a 400).
func TestRepairToolResultsNormalizesDowngradedEmptyResult(t *testing.T) {
	messages := []model.Message{
		{Role: "tool", ToolCallID: stringPtr("call_gone")},
	}

	got := RepairToolResults(messages)
	if got[0].Role != "user" {
		t.Fatalf("expected downgrade, got %+v", got[0])
	}
	if got[0].Content.Content == nil || *got[0].Content.Content == "" {
		t.Fatalf("expected placeholder content on downgraded empty result, got %+v", got[0].Content)
	}
}

// The relay reuses one InternalLLMRequest across channel retries, so every
// outbound transform re-runs the repairs on already-repaired messages. A second
// pass must be a no-op or the message list grows on each retry.
func TestToolCallRepairsAreIdempotentAcrossRetries(t *testing.T) {
	req := &model.InternalLLMRequest{Messages: []model.Message{
		{Role: "user", Content: model.MessageContent{Content: stringPtr("go")}},
		{Role: "assistant", ToolCalls: []model.ToolCall{
			{ID: "call_a", Function: model.FunctionCall{Name: "a"}},
			{ID: "call_b", Function: model.FunctionCall{Name: "b"}},
		}},
		{Role: "tool", ToolCallID: stringPtr("call_a"), Content: model.MessageContent{Content: stringPtr("r1")}},
		{Role: "tool", ToolCallID: stringPtr("call_gone"), Content: model.MessageContent{Content: stringPtr("stale")}},
		{Role: "user", Content: model.MessageContent{Content: stringPtr("continue")}},
	}}

	PatchOpenAIRequest(req)
	first := append([]model.Message(nil), req.Messages...)
	PatchOpenAIRequest(req)

	if len(req.Messages) != len(first) {
		t.Fatalf("second pass changed the message count: %d then %d", len(first), len(req.Messages))
	}
	for i := range first {
		if req.Messages[i].Role != first[i].Role {
			t.Fatalf("message %d role changed on the second pass: %q then %q", i, first[i].Role, req.Messages[i].Role)
		}
	}
}

func stringPtr(v string) *string {
	return &v
}
