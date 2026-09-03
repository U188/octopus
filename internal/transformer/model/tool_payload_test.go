package model

import (
	"encoding/json"
	"strings"
	"testing"
)

// A message left with zero content parts must not keep a non-nil empty slice:
// that defeats `omitzero` on Message.Content and marshals as `"content": null`,
// which every upstream we target rejects.
func TestNormalizeNeverEmitsNullContent(t *testing.T) {
	cases := map[string]Message{
		"empty text parts with tool calls": {
			Role:      "assistant",
			Content:   MessageContent{MultipleContent: []MessageContentPart{{Type: "text", Text: ptrStr("")}}},
			ToolCalls: []ToolCall{{ID: "call_a", Function: FunctionCall{Name: "a", Arguments: "{}"}}},
		},
		"caller-supplied empty slice": {
			Role:      "assistant",
			Content:   MessageContent{MultipleContent: []MessageContentPart{}},
			ToolCalls: []ToolCall{{ID: "call_a", Function: FunctionCall{Name: "a", Arguments: "{}"}}},
		},
		"tool result with empty text part": {
			Role:       "tool",
			ToolCallID: ptrStr("call_a"),
			Content:    MessageContent{MultipleContent: []MessageContentPart{{Type: "text", Text: ptrStr("")}}},
		},
	}

	for name, msg := range cases {
		t.Run(name, func(t *testing.T) {
			msg.Normalize()
			body, err := json.Marshal(msg)
			if err != nil {
				t.Fatalf("marshal failed: %v", err)
			}
			if strings.Contains(string(body), `"content":null`) {
				t.Fatalf("normalized message still marshals a null content: %s", body)
			}
		})
	}
}

// A tool message's tool_call_id counts as payload, so the placeholder branch is
// skipped — but OpenAI Chat still requires a content field on every tool
// message. Normalize supplies an empty string.
func TestNormalizeGivesToolMessageExplicitContent(t *testing.T) {
	msg := Message{Role: "tool", ToolCallID: ptrStr("call_a")}
	msg.Normalize()

	if msg.Content.Content == nil {
		t.Fatalf("expected explicit content on tool message, got %+v", msg.Content)
	}
	if *msg.Content.Content != "" {
		t.Fatalf("expected empty string content, got %q", *msg.Content.Content)
	}
}

// Empty and null tool arguments are both unusable: Anthropic's tool_use.input
// and Gemini's functionCall.args are objects, and OpenAI-compatible upstreams
// unmarshal the string.
func TestNormalizeCanonicalisesEmptyToolArguments(t *testing.T) {
	msg := Message{
		Role: "assistant",
		ToolCalls: []ToolCall{
			{ID: "call_a", Function: FunctionCall{Name: "a", Arguments: ""}},
			{ID: "call_b", Function: FunctionCall{Name: "b", Arguments: "null"}},
			{ID: "call_c", Function: FunctionCall{Name: "c", Arguments: "  "}},
			{ID: "call_d", Function: FunctionCall{Name: "d", Arguments: `{"x":1}`}},
		},
	}
	msg.Normalize()

	for i, want := range []string{"{}", "{}", "{}", `{"x":1}`} {
		if got := msg.ToolCalls[i].Function.Arguments; got != want {
			t.Fatalf("tool call %d: expected arguments %q, got %q", i, want, got)
		}
	}
}

func TestNormalizeToolArguments(t *testing.T) {
	cases := map[string]string{
		"":           "{}",
		"   ":        "{}",
		"null":       "{}",
		" null ":     "{}",
		`{"x":1}`:    `{"x":1}`,
		`{"broken":`: `{"broken":`,
	}
	for input, want := range cases {
		if got := NormalizeToolArguments(input); got != want {
			t.Fatalf("NormalizeToolArguments(%q) = %q, want %q", input, got, want)
		}
	}
}

// Dropping every part of a turn must not leave it empty: the flatten pass
// re-normalizes so the message still carries a placeholder. Gemini rejects a
// Content whose parts array is empty; OpenAI rejects a null content.
func TestFlattenUnsupportedBlocksRenormalizesEmptiedMessages(t *testing.T) {
	for _, provider := range []AlternationProvider{AlternationProviderOpenAI, AlternationProviderGemini} {
		t.Run(string(provider), func(t *testing.T) {
			r := InternalLLMRequest{Messages: []Message{{
				Role: "assistant",
				Content: MessageContent{MultipleContent: []MessageContentPart{{
					Type:          "server_tool_use",
					ServerToolUse: &ServerToolUseBlock{ID: "srv_1", Name: "web_search"},
				}}},
			}}}
			r.FlattenUnsupportedBlocks(provider)

			msg := r.Messages[0]
			if msg.Content.MultipleContent != nil {
				t.Fatalf("expected parts to be cleared, got %+v", msg.Content.MultipleContent)
			}
			if msg.Content.Content == nil || *msg.Content.Content == "" {
				t.Fatalf("expected placeholder content after flatten, got %+v", msg.Content)
			}
			body, err := json.Marshal(msg)
			if err != nil {
				t.Fatalf("marshal failed: %v", err)
			}
			if strings.Contains(string(body), `"content":null`) {
				t.Fatalf("flattened message marshals a null content: %s", body)
			}
		})
	}
}

// Gemini natively supports documents, so its flatten pass must not collapse
// them into text hints the way the OpenAI one does.
func TestFlattenUnsupportedBlocksKeepsGeminiDocuments(t *testing.T) {
	r := InternalLLMRequest{Messages: []Message{{
		Role: "user",
		Content: MessageContent{MultipleContent: []MessageContentPart{{
			Type:     "document",
			Document: &DocumentSource{Type: "text", Text: "body"},
		}}},
	}}}
	r.FlattenUnsupportedBlocks(AlternationProviderGemini)

	parts := r.Messages[0].Content.MultipleContent
	if len(parts) != 1 || parts[0].Type != "document" {
		t.Fatalf("expected the document part to survive, got %+v", parts)
	}
}
