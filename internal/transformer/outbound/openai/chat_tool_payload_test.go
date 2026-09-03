package openai

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"

	inboundAnthropic "github.com/U188/octopus/internal/transformer/inbound/anthropic"
	"github.com/U188/octopus/internal/transformer/model"
)

// chatWireMessages runs an Anthropic client payload through the full
// Anthropic-inbound → Chat-outbound path and returns the decoded `messages`
// array from the request body actually sent upstream.
func chatWireMessages(t *testing.T, clientBody string) []map[string]any {
	t.Helper()
	in := &inboundAnthropic.MessagesInbound{}
	internalReq, err := in.TransformRequest(context.Background(), []byte(clientBody))
	if err != nil {
		t.Fatalf("inbound transform failed: %v", err)
	}
	if err := internalReq.Validate(); err != nil {
		t.Fatalf("validate failed: %v", err)
	}
	out := &ChatOutbound{}
	httpReq, err := out.TransformRequest(context.Background(), internalReq, "https://example.com/v1", "key")
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

func assertNoNullContent(t *testing.T, messages []map[string]any) {
	t.Helper()
	for i, msg := range messages {
		value, present := msg["content"]
		if present && value == nil {
			t.Fatalf("message %d emitted content:null, which OpenAI Chat rejects: %+v", i, msg)
		}
	}
}

func toolCallsOf(t *testing.T, msg map[string]any) []map[string]any {
	t.Helper()
	raw, ok := msg["tool_calls"].([]any)
	if !ok {
		t.Fatalf("expected tool_calls on message: %+v", msg)
	}
	calls := make([]map[string]any, 0, len(raw))
	for _, item := range raw {
		call, ok := item.(map[string]any)
		if !ok {
			t.Fatalf("unexpected tool_call shape: %#v", item)
		}
		calls = append(calls, call)
	}
	return calls
}

// A tool_use block with no `input` arrives as a nil json.RawMessage and used to
// emit `"arguments": ""`, which is not valid JSON and is rejected as a
// malformed tool payload.
func TestChatToolCallOmittedInputBecomesEmptyObject(t *testing.T) {
	messages := chatWireMessages(t, `{
		"model":"claude-opus-5","max_tokens":100,
		"messages":[
			{"role":"user","content":"ping"},
			{"role":"assistant","content":[{"type":"tool_use","id":"toolu_1","name":"noargs"}]},
			{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_1","content":"pong"}]}
		]}`)

	calls := toolCallsOf(t, messages[1])
	function, ok := calls[0]["function"].(map[string]any)
	if !ok {
		t.Fatalf("expected function object: %+v", calls[0])
	}
	if function["arguments"] != "{}" {
		t.Fatalf("expected arguments %q, got %#v", "{}", function["arguments"])
	}
}

// Request-side tool calls must not carry the streaming-only `index` field.
func TestChatToolCallsOmitStreamingIndex(t *testing.T) {
	messages := chatWireMessages(t, `{
		"model":"claude-opus-5","max_tokens":100,
		"messages":[
			{"role":"user","content":"go"},
			{"role":"assistant","content":[
				{"type":"tool_use","id":"toolu_1","name":"a","input":{"x":1}},
				{"type":"tool_use","id":"toolu_2","name":"b","input":{"x":2}}
			]},
			{"role":"user","content":[
				{"type":"tool_result","tool_use_id":"toolu_1","content":"r1"},
				{"type":"tool_result","tool_use_id":"toolu_2","content":"r2"}
			]}
		]}`)

	for _, call := range toolCallsOf(t, messages[1]) {
		if _, present := call["index"]; present {
			t.Fatalf("tool_call must not carry a request-side index: %+v", call)
		}
	}
}

// A tool_result whose content is an empty array, absent, or image-only has no
// text to forward. It must still emit a content field: OpenAI Chat requires one
// on every tool message, and `null` is not accepted.
func TestChatToolResultWithoutTextEmitsEmptyContent(t *testing.T) {
	cases := map[string]string{
		"empty array": `{"type":"tool_result","tool_use_id":"toolu_1","content":[]}`,
		"absent":      `{"type":"tool_result","tool_use_id":"toolu_1"}`,
		"image only":  `{"type":"tool_result","tool_use_id":"toolu_1","content":[{"type":"image","source":{"type":"base64","media_type":"image/png","data":"AAAA"}}]}`,
		"empty text":  `{"type":"tool_result","tool_use_id":"toolu_1","content":[{"type":"text","text":""}]}`,
	}

	for name, resultBlock := range cases {
		t.Run(name, func(t *testing.T) {
			messages := chatWireMessages(t, `{
				"model":"claude-opus-5","max_tokens":100,
				"messages":[
					{"role":"user","content":"go"},
					{"role":"assistant","content":[{"type":"tool_use","id":"toolu_1","name":"a","input":{}}]},
					{"role":"user","content":[`+resultBlock+`]}
				]}`)

			assertNoNullContent(t, messages)
			toolMsg := messages[len(messages)-1]
			if toolMsg["role"] != "tool" {
				t.Fatalf("expected trailing tool message, got %+v", toolMsg)
			}
			content, present := toolMsg["content"]
			if !present {
				t.Fatalf("tool message must carry a content field: %+v", toolMsg)
			}
			if content != "" {
				t.Fatalf("expected empty string content, got %#v", content)
			}
		})
	}
}

// An assistant turn whose only content was a server_tool_use block loses that
// block on the Chat path (OpenAI has no equivalent). The message must not be
// left with a null content.
func TestChatServerToolOnlyAssistantKeepsValidContent(t *testing.T) {
	messages := chatWireMessages(t, `{
		"model":"claude-opus-5","max_tokens":100,
		"messages":[
			{"role":"user","content":"search"},
			{"role":"assistant","content":[{"type":"server_tool_use","id":"srvtoolu_1","name":"web_search","input":{"query":"x"}}]},
			{"role":"user","content":"thanks"}
		]}`)

	assertNoNullContent(t, messages)
}

// An assistant tool call that the client never answered must gain a synthetic
// tool result: OpenAI Chat requires one tool message per call ID.
func TestChatUnansweredToolCallGetsSyntheticResult(t *testing.T) {
	messages := chatWireMessages(t, `{
		"model":"claude-opus-5","max_tokens":100,
		"messages":[
			{"role":"user","content":"go"},
			{"role":"assistant","content":[{"type":"tool_use","id":"toolu_1","name":"ls","input":{"path":"."}}]},
			{"role":"user","content":"never mind"}
		]}`)

	var found bool
	for _, msg := range messages {
		if msg["role"] == "tool" && msg["tool_call_id"] == "toolu_1" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected a synthetic tool result for toolu_1, got %+v", messages)
	}
	assertNoNullContent(t, messages)
}

// A tool result whose originating call is gone (truncated history) cannot bind
// upstream. It is downgraded to a user turn so the text survives.
func TestChatOrphanedToolResultBecomesUserMessage(t *testing.T) {
	messages := chatWireMessages(t, `{
		"model":"claude-opus-5","max_tokens":100,
		"messages":[
			{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_gone","content":"stale output"}]},
			{"role":"user","content":"continue"}
		]}`)

	for _, msg := range messages {
		if msg["role"] == "tool" {
			t.Fatalf("orphaned tool result was forwarded verbatim: %+v", messages)
		}
	}
	if !strings.Contains(messages[0]["content"].(string), "stale output") {
		t.Fatalf("expected the tool output to survive as user text, got %+v", messages[0])
	}
}

// One user turn can carry a bound tool result, an orphaned one, and text. The
// bound result must keep its binding while only the orphan is downgraded.
func TestChatMixedBoundAndOrphanedToolResults(t *testing.T) {
	messages := chatWireMessages(t, `{
		"model":"claude-opus-5","max_tokens":100,
		"messages":[
			{"role":"user","content":"go"},
			{"role":"assistant","content":[{"type":"tool_use","id":"toolu_1","name":"a","input":{}}]},
			{"role":"user","content":[
				{"type":"tool_result","tool_use_id":"toolu_1","content":"bound"},
				{"type":"tool_result","tool_use_id":"toolu_gone","content":"orphan"},
				{"type":"text","text":"now continue"}
			]}
		]}`)

	assertNoNullContent(t, messages)

	var toolIDs []string
	body, err := json.Marshal(messages)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}
	for _, msg := range messages {
		if msg["role"] != "tool" {
			continue
		}
		id, ok := msg["tool_call_id"].(string)
		if !ok {
			t.Fatalf("tool message without a tool_call_id: %+v", msg)
		}
		toolIDs = append(toolIDs, id)
	}
	if len(toolIDs) != 1 || toolIDs[0] != "toolu_1" {
		t.Fatalf("expected only the bound result to stay a tool message, got %v in %s", toolIDs, body)
	}
	if !strings.Contains(string(body), "orphan") {
		t.Fatalf("expected the orphaned output to survive as text: %s", body)
	}
	if !strings.Contains(string(body), "now continue") {
		t.Fatalf("expected the trailing user text to survive: %s", body)
	}
}

// Direct builder coverage: a caller assembling a request without going through
// Normalize still gets valid tool arguments and no index field.
func TestBuildChatCompletionsRequestNormalizesToolCalls(t *testing.T) {
	wire := buildChatCompletionsRequest(&model.InternalLLMRequest{
		Model: "gpt-4o",
		Messages: []model.Message{{
			Role: "assistant",
			ToolCalls: []model.ToolCall{{
				ID:       "call_1",
				Type:     "function",
				Index:    3,
				Function: model.FunctionCall{Name: "lookup", Arguments: ""},
			}},
		}},
	})

	body, err := json.Marshal(wire)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}
	if strings.Contains(string(body), `"index"`) {
		t.Fatalf("request body must not contain a tool call index: %s", body)
	}
	if !strings.Contains(string(body), `"arguments":"{}"`) {
		t.Fatalf("expected empty arguments to become {}: %s", body)
	}
}
