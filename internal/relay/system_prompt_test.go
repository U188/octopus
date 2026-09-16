package relay

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	dbmodel "github.com/U188/octopus/internal/model"
	"github.com/U188/octopus/internal/op"
	"github.com/U188/octopus/internal/transformer/inbound"
	transformerModel "github.com/U188/octopus/internal/transformer/model"
	"github.com/U188/octopus/internal/transformer/outbound"
	"github.com/coder/websocket"
	"github.com/gin-gonic/gin"
)

func TestRewriteSystemPromptBody(t *testing.T) {
	tests := []struct {
		name        string
		channelType outbound.OutboundType
		codexMode   bool
		mode        dbmodel.SystemPromptMode
		body        string
		check       func(t *testing.T, payload map[string]any)
	}{
		{
			name:        "chat override removes all client instructions",
			channelType: outbound.OutboundTypeOpenAIChat,
			mode:        dbmodel.SystemPromptModeOverride,
			body:        `{"model":"gpt","messages":[{"role":"system","content":"old"},{"role":"user","content":"hi"},{"role":"developer","content":"late"}]}`,
			check: func(t *testing.T, payload map[string]any) {
				messages := payload["messages"].([]any)
				if len(messages) != 2 || messages[0].(map[string]any)["role"] != "system" || messages[0].(map[string]any)["content"] != "managed" || messages[1].(map[string]any)["role"] != "user" {
					t.Fatalf("unexpected chat messages: %#v", messages)
				}
			},
		},
		{
			name:        "anthropic append preserves blocks",
			channelType: outbound.OutboundTypeAnthropic,
			mode:        dbmodel.SystemPromptModeAppend,
			body:        `{"system":[{"type":"text","text":"old","cache_control":{"type":"ephemeral"}}],"messages":[]}`,
			check: func(t *testing.T, payload map[string]any) {
				system := payload["system"].([]any)
				if len(system) != 2 || system[0].(map[string]any)["cache_control"] == nil || system[1].(map[string]any)["text"] != "managed" {
					t.Fatalf("unexpected anthropic system: %#v", system)
				}
			},
		},
		{
			name:        "responses append follows developer item",
			channelType: outbound.OutboundTypeOpenAIResponse,
			mode:        dbmodel.SystemPromptModeAppend,
			body:        `{"instructions":"top","input":[{"type":"message","role":"developer","content":[{"type":"input_text","text":"old"}]},{"type":"message","role":"user","content":[{"type":"input_text","text":"hi"}]}]}`,
			check: func(t *testing.T, payload map[string]any) {
				input := payload["input"].([]any)
				if len(input) != 3 || input[1].(map[string]any)["role"] != "developer" || input[2].(map[string]any)["role"] != "user" {
					t.Fatalf("unexpected responses input: %#v", input)
				}
			},
		},
		{
			name:        "codex override preserves tools and string input",
			channelType: outbound.OutboundTypeOpenAIResponse,
			codexMode:   true,
			mode:        dbmodel.SystemPromptModeOverride,
			body:        `{"instructions":"old","input":"hi","tools":[{"type":"function","name":"f"}]}`,
			check: func(t *testing.T, payload map[string]any) {
				if _, ok := payload["instructions"]; ok {
					t.Fatalf("instructions were not removed: %#v", payload)
				}
				if payload["tools"] == nil {
					t.Fatalf("tools were removed: %#v", payload)
				}
				input := payload["input"].([]any)
				if len(input) != 2 || input[0].(map[string]any)["role"] != "developer" || input[1].(map[string]any)["role"] != "user" {
					t.Fatalf("unexpected codex input: %#v", input)
				}
			},
		},
		{
			name:        "gemini prepend preserves existing parts",
			channelType: outbound.OutboundTypeGemini,
			mode:        dbmodel.SystemPromptModePrepend,
			body:        `{"systemInstruction":{"parts":[{"text":"old"}]},"contents":[]}`,
			check: func(t *testing.T, payload map[string]any) {
				parts := payload["systemInstruction"].(map[string]any)["parts"].([]any)
				if len(parts) != 2 || parts[0].(map[string]any)["text"] != "managed" || parts[1].(map[string]any)["text"] != "old" {
					t.Fatalf("unexpected gemini parts: %#v", parts)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rewritten, changed, err := rewriteSystemPromptBody([]byte(tt.body), tt.channelType, tt.codexMode, tt.mode, "managed")
			if err != nil {
				t.Fatalf("rewriteSystemPromptBody() error = %v", err)
			}
			if !changed {
				t.Fatal("rewriteSystemPromptBody() did not report a change")
			}
			var payload map[string]any
			if err := json.Unmarshal(rewritten, &payload); err != nil {
				t.Fatalf("unmarshal rewritten body: %v", err)
			}
			tt.check(t, payload)
		})
	}
}

func TestSanitizeOutboundPayloadRewritesKnownFingerprints(t *testing.T) {
	body := []byte(`{"messages":[{"role":"system","content":"You are Claude Code, Anthropic's official CLI for Claude. cc_entrypoint=cli;"}]}`)
	got, changed, err := sanitizeOutboundPayload(body)
	if err != nil || !changed {
		t.Fatalf("sanitizeOutboundPayload() changed=%t err=%v", changed, err)
	}
	text := string(got)
	if strings.Contains(text, "official CLI for Claude") || strings.Contains(text, "cc_entrypoint=") || !strings.Contains(text, "official CLI tool for Claude") {
		t.Fatalf("fingerprint was not minimally rewritten: %s", text)
	}
}

func TestSanitizeOutboundPayloadLeavesNonPromptFieldsUntouched(t *testing.T) {
	body := []byte(`{"model":"11128","metadata":"You are Claude Code, Anthropic's official CLI for Claude","messages":[{"role":"user","content":"11128 cc_entrypoint=cli;"},{"role":"system","name":"11128","content":"ordinary"}]}`)
	got, changed, err := sanitizeOutboundPayload(body)
	if err != nil || changed {
		t.Fatalf("non-prompt payload changed=%t err=%v got=%s", changed, err, got)
	}
	if string(got) != string(body) {
		t.Fatalf("non-prompt fields were modified: %s", got)
	}
}

func TestSanitizeOutboundPayloadDoesNotConsumeFollowingText(t *testing.T) {
	body := []byte(`{"system":"cc_mode=cli keep this sentence"}`)
	got, changed, err := sanitizeOutboundPayload(body)
	if err != nil || !changed || !strings.Contains(string(got), "keep this sentence") {
		t.Fatalf("following text was consumed: changed=%t err=%v got=%s", changed, err, got)
	}
}

func TestSanitizeOutboundPayloadAppliesCustomLiteralRules(t *testing.T) {
	body := []byte(`{"messages":[{"role":"system","content":"remove-me old-value keep"},{"role":"user","content":"remove-me old-value"}]}`)
	got, changed, err := sanitizeOutboundPayloadWithRules(body, "remove-me\nold-value => new-value")
	if err != nil || !changed {
		t.Fatalf("custom rules changed=%t err=%v", changed, err)
	}
	var payload map[string]any
	if err := json.Unmarshal(got, &payload); err != nil {
		t.Fatal(err)
	}
	messages := payload["messages"].([]any)
	if content := messages[0].(map[string]any)["content"]; content != "new-value keep" {
		t.Fatalf("system content = %q", content)
	}
	if content := messages[1].(map[string]any)["content"]; content != "remove-me old-value" {
		t.Fatalf("user content was modified: %q", content)
	}
}

func TestScopedTextRulesPreserveToolsAndOtherFields(t *testing.T) {
	body := []byte(`{"model":"old","metadata":{"content":"old"},"messages":[{"role":"system","content":"old"},{"role":"user","content":"old"},{"role":"assistant","content":"old","reasoning_content":"try writing the file again","tool_calls":[{"id":"call_old","type":"function","function":{"name":"bash","arguments":"{\"command\":\"echo old\"}"}}]},{"role":"assistant","content":"old"},{"role":"tool","content":"old"}],"tools":[{"type":"function","function":{"name":"old","parameters":{"content":"old"}}}]}`)
	got, changed, err := rewriteOutboundPayload(body, false, "", "[content] old => new\n[reasoning_content] * => 今天天气真好", true)
	if err != nil || !changed {
		t.Fatalf("scoped rules changed=%t err=%v", changed, err)
	}
	var before, after map[string]any
	if err := json.Unmarshal(body, &before); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(got, &after); err != nil {
		t.Fatal(err)
	}
	messages := before["messages"].([]any)
	messages[1].(map[string]any)["content"] = "new"
	messages[2].(map[string]any)["content"] = "new"
	messages[2].(map[string]any)["reasoning_content"] = "今天天气真好"
	messages[3].(map[string]any)["content"] = "new"
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("unexpected fields modified: %s", got)
	}
}

func TestScopedContentRulesForResponsesAndGeminiTextBlocks(t *testing.T) {
	for _, body := range []string{
		`{"instructions":"old","input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"old","metadata":"old"},{"type":"input_image","image_url":"old"}]},{"type":"function_call","name":"old","arguments":"old"}]}`,
		`{"systemInstruction":{"parts":[{"text":"old"}]},"contents":[{"role":"model","parts":[{"text":"old"},{"functionCall":{"name":"old","args":{"text":"old"}}}]}]}`,
		`{"input":"old"}`,
	} {
		got, changed, err := rewriteOutboundPayload([]byte(body), false, "", "[content] old => new", true)
		if err != nil || !changed || !strings.Contains(string(got), "new") {
			t.Fatalf("text blocks changed=%t err=%v got=%s", changed, err, got)
		}
		if strings.Contains(string(got), `"name":"new"`) || strings.Contains(string(got), `"instructions":"new"`) || strings.Contains(string(got), `"image_url":"new"`) {
			t.Fatalf("non-content fields were modified: %s", got)
		}
	}
}

func TestFingerprintAndConversationRulesRemainIndependent(t *testing.T) {
	body := []byte(`{"messages":[{"role":"system","content":"old"},{"role":"user","content":"old"},{"role":"assistant","content":"old","reasoning_content":"old"}]}`)
	got, changed, err := rewriteOutboundPayload(body, true, "old => system-new", "[content] old => content-new\n[reasoning_content] * => weather", true)
	if err != nil || !changed {
		t.Fatalf("rewrite changed=%t err=%v", changed, err)
	}
	var payload struct {
		Messages []struct {
			Content          string `json:"content"`
			ReasoningContent string `json:"reasoning_content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(got, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Messages[0].Content != "system-new" || payload.Messages[1].Content != "content-new" ||
		payload.Messages[2].Content != "content-new" || payload.Messages[2].ReasoningContent != "weather" {
		t.Fatalf("rules crossed boundaries: %s", got)
	}
}

func TestReasoningRulesCoverProviderWireFormats(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{name: "responses", body: `{"input":[{"type":"reasoning","summary":[{"type":"summary_text","text":"old"}]}]}`},
		{name: "anthropic", body: `{"messages":[{"role":"assistant","content":[{"type":"thinking","thinking":"old"}]}]}`},
		{name: "gemini", body: `{"contents":[{"role":"model","parts":[{"thought":true,"text":"old"}]}]}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, changed, err := rewriteOutboundPayload([]byte(tt.body), false, "", "[reasoning_content] * => weather", true)
			if err != nil || !changed || !strings.Contains(string(got), `"weather"`) || strings.Contains(string(got), `"old"`) {
				t.Fatalf("reasoning rewrite changed=%t err=%v body=%s", changed, err, got)
			}
		})
	}
}

func TestReasoningRulesPreserveSignedProviderBlocks(t *testing.T) {
	for _, body := range []string{
		`{"messages":[{"role":"assistant","reasoning_content":"old","reasoning_signature":"sig"}]}`,
		`{"messages":[{"role":"assistant","content":[{"type":"thinking","thinking":"old","signature":"sig"}]}]}`,
		`{"contents":[{"role":"model","parts":[{"thought":true,"text":"old","thoughtSignature":"sig"}]}]}`,
	} {
		got, changed, err := rewriteOutboundPayload([]byte(body), false, "", "[reasoning_content] * => weather", true)
		if err != nil || changed || string(got) != body {
			t.Fatalf("signed reasoning changed=%t err=%v body=%s", changed, err, got)
		}
	}
}

func TestConversationRulesSkipEmbeddingInput(t *testing.T) {
	body := []byte(`{"model":"embedding-model","input":"old"}`)
	ra := &relayAttempt{
		relayRequest: &relayRequest{conversationRewriteRules: "[content] old => new"},
		channel:      &dbmodel.Channel{Type: outbound.OutboundTypeOpenAIEmbedding},
	}
	got, changed, err := ra.rewriteConfiguredOutboundPayload(body, true)
	if err != nil || changed || string(got) != string(body) {
		t.Fatalf("embedding input changed=%t err=%v body=%s", changed, err, got)
	}
}

func TestDegradedRetryDoesNotApplyConversationRulesTwice(t *testing.T) {
	ra := &relayAttempt{
		relayRequest: &relayRequest{
			systemPromptMode:         dbmodel.SystemPromptModeOverride,
			systemPrompt:             "managed",
			conversationRewriteRules: "[content] a => aa",
		},
		channel: &dbmodel.Channel{Type: outbound.OutboundTypeOpenAIChat},
	}
	req, err := http.NewRequest(http.MethodPost, "http://example.com", strings.NewReader(`{"messages":[{"role":"system","content":"original"},{"role":"user","content":"a"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if err := ra.finalizeOutboundRequest(req); err != nil {
		t.Fatal(err)
	}
	retry, err := ra.prepareDegradedRequest(req)
	if err != nil {
		t.Fatal(err)
	}
	body, err := readOutboundRequestBody(retry)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), `"content":"aaaa"`) || !strings.Contains(string(body), `"content":"aa"`) {
		t.Fatalf("conversation rules were applied twice: %s", body)
	}
}

func TestOutboundRequestAppliesReasoningRuleBeforeSending(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload struct {
			Messages []struct {
				ReasoningContent string `json:"reasoning_content"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Error(err)
		}
		if len(payload.Messages) != 1 || payload.Messages[0].ReasoningContent != "weather" {
			t.Errorf("upstream received unexpected reasoning: %#v", payload.Messages)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	ra := &relayAttempt{
		relayRequest: &relayRequest{
			systemPromptMode:                 dbmodel.SystemPromptModeOff,
			systemPromptSanitizeFingerprints: false,
			conversationRewriteRules:         "[reasoning_content] * => weather",
		},
		channel: &dbmodel.Channel{Type: outbound.OutboundTypeOpenAIChat, BaseUrls: []dbmodel.BaseUrl{{URL: server.URL}}},
	}
	req, err := http.NewRequest(http.MethodPost, server.URL, strings.NewReader(`{"messages":[{"role":"assistant","content":"","reasoning_content":"old"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if err := ra.finalizeOutboundRequest(req); err != nil {
		t.Fatal(err)
	}
	response, err := ra.sendRequest(req)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
}

func TestSanitizeOutboundPayloadLeavesNormalPayloadUntouched(t *testing.T) {
	body := []byte(`{"messages":[{"role":"user","content":"hello"}],"temperature":0.2}`)
	got, changed, err := sanitizeOutboundPayload(body)
	if err != nil || changed || string(got) != string(body) {
		t.Fatalf("normal payload changed=%t err=%v got=%s", changed, err, got)
	}
}

func TestIsSystemPromptContentBlocked(t *testing.T) {
	if !isSystemPromptContentBlocked(http.StatusBadRequest, []byte(`{"code":11128,"message":"blocked by security policy"}`)) {
		t.Fatal("expected content block to be detected")
	}
	if isSystemPromptContentBlocked(http.StatusBadRequest, []byte(`{"code":11101,"message":"invalid parameter"}`)) {
		t.Fatal("ordinary validation error must not trigger degraded retry")
	}
	if isSystemPromptContentBlocked(http.StatusBadRequest, []byte(`{"message":"invalid parameter 11128 chars"}`)) {
		t.Fatal("unrelated 11128 text must not trigger degraded retry")
	}
	if !isSystemPromptContentBlockedDetails(0, "11128", "") {
		t.Fatal("WebSocket error code 11128 must trigger degraded retry")
	}
}

func TestWSUpstreamReaderCapturesNumericPromptBlockCode(t *testing.T) {
	clientConn, serverConn := newTestWSConnPair(t)
	defer clientConn.Close(websocket.StatusNormalClosure, "")
	defer serverConn.Close(websocket.StatusNormalClosure, "")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	writeDone := make(chan error, 1)
	go func() {
		writeDone <- serverConn.Write(ctx, websocket.MessageText, []byte(`{"type":"error","status":400,"error":{"code":11128,"message":"blocked by security policy"}}`))
	}()

	reader := newWSUpstreamReader(&pooledConn{conn: clientConn}, 1, 1)
	if _, err := reader.ReadEvent(ctx); err == nil {
		t.Fatal("expected WebSocket error frame")
	}
	if err := <-writeDone; err != nil {
		t.Fatalf("write error frame: %v", err)
	}
	if reader.StatusCode() != http.StatusBadRequest || reader.errorCode != "11128" || !isSystemPromptContentBlockedDetails(reader.StatusCode(), reader.errorCode, reader.errorMsg) {
		t.Fatalf("blocked frame was not normalized: status=%d code=%q message=%q", reader.StatusCode(), reader.errorCode, reader.errorMsg)
	}
}

func TestSendRequestRetriesContentBlockOnceWithDegradedPrompt(t *testing.T) {
	var bodies []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		bodies = append(bodies, string(body))
		w.Header().Set("Content-Type", "application/json")
		if len(bodies) == 1 {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"code":11128,"message":"blocked by security policy"}`))
			return
		}
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()

	internalRequest := &transformerModel.InternalLLMRequest{Model: "upstream-model"}
	metrics := NewRelayMetrics(0, "group", nil, internalRequest)
	ra := &relayAttempt{
		relayRequest: &relayRequest{
			internalRequest:                  internalRequest,
			metrics:                          metrics,
			requestModel:                     "group",
			systemPromptMode:                 dbmodel.SystemPromptModeOverride,
			systemPrompt:                     "managed",
			systemPromptSanitizeFingerprints: true,
		},
		channel: &dbmodel.Channel{Type: outbound.OutboundTypeOpenAIChat, BaseUrls: []dbmodel.BaseUrl{{URL: server.URL}}},
	}
	req, err := http.NewRequest(http.MethodPost, server.URL, strings.NewReader(`{"messages":[{"role":"developer","content":"client"},{"role":"user","content":"hi"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if err := ra.finalizeOutboundRequest(req); err != nil {
		t.Fatalf("finalizeOutboundRequest: %v", err)
	}
	response, err := ra.sendRequest(req)
	if err != nil {
		t.Fatalf("sendRequest: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK || len(bodies) != 2 {
		t.Fatalf("status=%d requests=%d", response.StatusCode, len(bodies))
	}
	if !strings.Contains(bodies[0], `"role":"system"`) || !strings.Contains(bodies[0], "managed") {
		t.Fatalf("first request was not strict system override: %s", bodies[0])
	}
	if strings.Contains(bodies[1], "managed") || !strings.Contains(bodies[1], degradedSystemPrompt) {
		t.Fatalf("retry did not use degraded prompt: %s", bodies[1])
	}
	if !metrics.SystemPromptRetry || metrics.SystemPromptRetryReason != "content_blocked" || metrics.UpstreamRequestContent != bodies[1] {
		t.Fatalf("retry metrics not recorded: %+v", metrics)
	}
}

func TestRewriteSystemPromptModeMatrix(t *testing.T) {
	protocols := []struct {
		name        string
		channelType outbound.OutboundType
		body        string
	}{
		{name: "chat", channelType: outbound.OutboundTypeOpenAIChat, body: `{"messages":[{"role":"system","content":"old"},{"role":"user","content":"hi"}]}`},
		{name: "responses", channelType: outbound.OutboundTypeOpenAIResponse, body: `{"instructions":"old","input":"hi"}`},
		{name: "anthropic", channelType: outbound.OutboundTypeAnthropic, body: `{"system":"old","messages":[]}`},
		{name: "gemini", channelType: outbound.OutboundTypeGemini, body: `{"systemInstruction":{"parts":[{"text":"old"}]},"contents":[]}`},
	}
	modes := []dbmodel.SystemPromptMode{
		dbmodel.SystemPromptModePrepend,
		dbmodel.SystemPromptModeAppend,
		dbmodel.SystemPromptModeOverride,
	}

	for _, protocol := range protocols {
		for _, mode := range modes {
			t.Run(protocol.name+"/"+string(mode), func(t *testing.T) {
				rewritten, changed, err := rewriteSystemPromptBody([]byte(protocol.body), protocol.channelType, false, mode, "managed")
				if err != nil || !changed {
					t.Fatalf("rewrite failed: changed=%t err=%v", changed, err)
				}
				text := string(rewritten)
				oldIndex, managedIndex := strings.Index(text, "old"), strings.Index(text, "managed")
				switch mode {
				case dbmodel.SystemPromptModePrepend:
					if managedIndex < 0 || oldIndex < 0 || managedIndex > oldIndex {
						t.Fatalf("managed prompt was not prepended: %s", text)
					}
				case dbmodel.SystemPromptModeAppend:
					if managedIndex < 0 || oldIndex < 0 || managedIndex < oldIndex {
						t.Fatalf("managed prompt was not appended: %s", text)
					}
				case dbmodel.SystemPromptModeOverride:
					if managedIndex < 0 || oldIndex >= 0 {
						t.Fatalf("managed prompt did not override old prompt: %s", text)
					}
				}
			})
		}
	}
}

func TestRewriteChatSystemPromptUsesDeveloperRole(t *testing.T) {
	body := []byte(`{"messages":[{"role":"developer","content":"client"},{"role":"user","content":"hi"}]}`)
	rewritten, _, err := rewriteSystemPromptBody(body, outbound.OutboundTypeOpenAIChat, false, dbmodel.SystemPromptModeAppend, "managed")
	if err != nil {
		t.Fatalf("rewrite chat prompt: %v", err)
	}
	var payload struct {
		Messages []map[string]any `json:"messages"`
	}
	if err := json.Unmarshal(rewritten, &payload); err != nil {
		t.Fatalf("unmarshal rewritten chat: %v", err)
	}
	if len(payload.Messages) != 3 || payload.Messages[1]["role"] != "developer" || payload.Messages[1]["content"] != "managed" {
		t.Fatalf("managed prompt lost developer priority: %#v", payload.Messages)
	}
}

func TestRewriteSystemPromptPreservesLargeJSONNumber(t *testing.T) {
	body := []byte(`{"opaque_id":9007199254740993,"messages":[{"role":"user","content":"hi"}]}`)
	rewritten, _, err := rewriteSystemPromptBody(body, outbound.OutboundTypeOpenAIChat, false, dbmodel.SystemPromptModeAppend, "managed")
	if err != nil {
		t.Fatalf("rewrite chat prompt: %v", err)
	}
	if !strings.Contains(string(rewritten), `"opaque_id":9007199254740993`) {
		t.Fatalf("large JSON number changed: %s", rewritten)
	}
}

func TestRewriteSystemPromptRejectsMalformedEnabledBody(t *testing.T) {
	if _, _, err := rewriteSystemPromptBody([]byte(`not json`), outbound.OutboundTypeOpenAIChat, false, dbmodel.SystemPromptModeAppend, "managed"); err == nil {
		t.Fatal("expected malformed body to fail closed")
	}
}

func TestRewriteSystemPromptBodyOffLeavesMalformedBodyUntouched(t *testing.T) {
	body := []byte(`not json`)
	ra := &relayAttempt{relayRequest: &relayRequest{systemPromptMode: dbmodel.SystemPromptModeOff}}
	got, err := ra.rewriteSystemPromptBody(body)
	if err != nil || string(got) != string(body) {
		t.Fatalf("off mode changed body or returned error: got=%q err=%v", got, err)
	}
}

func TestFinalizeOutboundRequestWinsAfterParamOverride(t *testing.T) {
	req, err := http.NewRequest(http.MethodPost, "http://example.test/v1/chat/completions", strings.NewReader(`{"messages":[{"role":"system","content":"client"},{"role":"user","content":"hi"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	paramOverride := `{"messages":[{"role":"system","content":"channel"},{"role":"user","content":"hi"}]}`
	ra := &relayAttempt{
		relayRequest: &relayRequest{
			internalRequest:  &transformerModel.InternalLLMRequest{Model: "gpt"},
			metrics:          NewRelayMetrics(0, "gpt", nil, &transformerModel.InternalLLMRequest{Model: "gpt"}),
			systemPromptMode: dbmodel.SystemPromptModeOverride,
			systemPrompt:     "managed",
		},
		channel: &dbmodel.Channel{
			Type:          outbound.OutboundTypeOpenAIChat,
			BaseUrls:      []dbmodel.BaseUrl{{URL: "https://user:secret@example.test/v1/"}},
			ParamOverride: &paramOverride,
		},
	}
	if err := ra.applyParamOverride(req); err != nil {
		t.Fatalf("applyParamOverride() error = %v", err)
	}
	if err := ra.finalizeOutboundRequest(req); err != nil {
		t.Fatalf("finalizeOutboundRequest() error = %v", err)
	}
	body, err := io.ReadAll(req.Body)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), "channel") || !strings.Contains(string(body), "managed") {
		t.Fatalf("system prompt did not win after param override: %s", body)
	}
	if got := ra.metrics.UpstreamRequestContent; got == "" || !strings.Contains(got, "managed") || strings.Contains(got, "client") {
		t.Fatalf("metrics did not capture final upstream request: %s", got)
	}
	if got, want := ra.metrics.UpstreamBaseURL, "https://example.test/v1"; got != want {
		t.Fatalf("metrics base URL = %q, want %q", got, want)
	}
}

func TestBuildWSPassthroughRequestPayloadAppliesSystemPrompt(t *testing.T) {
	ra := &relayAttempt{
		relayRequest: &relayRequest{
			rawBody:          []byte(`{"model":"client","input":[{"type":"message","role":"developer","content":[{"type":"input_text","text":"client"}]}]}`),
			internalRequest:  &transformerModel.InternalLLMRequest{Model: "upstream"},
			systemPromptMode: dbmodel.SystemPromptModeOverride,
			systemPrompt:     "managed",
		},
		channel: &dbmodel.Channel{Type: outbound.OutboundTypeOpenAIResponse, CodexMode: true},
	}
	payload, err := ra.buildWSPassthroughRequestPayload()
	if err != nil {
		t.Fatalf("build WS passthrough payload: %v", err)
	}
	text := string(payload)
	if strings.Contains(text, "client") || !strings.Contains(text, "managed") || !strings.Contains(text, `"type":"response.create"`) {
		t.Fatalf("unexpected WS passthrough payload: %s", payload)
	}
}

func TestHandlerSendsHijackedSystemPromptToBaseURL(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx := setupRelayTestDB(t)

	var capturedBody []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var err error
		capturedBody, err = io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read upstream request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"chatcmpl_1","object":"chat.completion","created":1,"model":"upstream-model","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`))
	}))
	defer server.Close()

	paramOverride := `{"messages":[{"role":"system","content":"channel"},{"role":"user","content":"hi"}]}`
	channel := &dbmodel.Channel{
		Name:          "system-prompt-upstream",
		Type:          outbound.OutboundTypeOpenAIChat,
		Enabled:       true,
		BaseUrls:      []dbmodel.BaseUrl{{URL: server.URL + "/v1"}},
		Model:         "upstream-model",
		Keys:          []dbmodel.ChannelKey{{Enabled: true, ChannelKey: "test-key"}},
		ParamOverride: &paramOverride,
	}
	if err := op.ChannelCreate(channel, ctx); err != nil {
		t.Fatalf("ChannelCreate failed: %v", err)
	}
	group := &dbmodel.Group{
		Name:             "system-prompt-group",
		Mode:             dbmodel.GroupModeFailover,
		SystemPromptMode: dbmodel.SystemPromptModeOverride,
		SystemPrompt:     "managed",
	}
	if err := op.GroupCreate(group, ctx); err != nil {
		t.Fatalf("GroupCreate failed: %v", err)
	}
	if err := op.GroupItemAdd(&dbmodel.GroupItem{GroupID: group.ID, ChannelID: channel.ID, ModelName: "upstream-model", Priority: 1, Weight: 1}, ctx); err != nil {
		t.Fatalf("GroupItemAdd failed: %v", err)
	}

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"system-prompt-group","messages":[{"role":"system","content":"client"},{"role":"user","content":"hi"}]}`))
	c.Request.Header.Set("Content-Type", "application/json")
	Handler(inbound.InboundTypeOpenAIChat, c)

	if recorder.Code != http.StatusOK {
		t.Fatalf("relay failed: status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if strings.Contains(string(capturedBody), "client") || strings.Contains(string(capturedBody), "channel") || !strings.Contains(string(capturedBody), "managed") {
		t.Fatalf("base_url received the wrong prompt: %s", capturedBody)
	}
}
