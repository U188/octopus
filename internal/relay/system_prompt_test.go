package relay

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	dbmodel "github.com/U188/octopus/internal/model"
	"github.com/U188/octopus/internal/op"
	"github.com/U188/octopus/internal/transformer/inbound"
	transformerModel "github.com/U188/octopus/internal/transformer/model"
	"github.com/U188/octopus/internal/transformer/outbound"
	"github.com/gin-gonic/gin"
	"net/http/httptest"
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
				if len(messages) != 2 || messages[0].(map[string]any)["content"] != "managed" || messages[1].(map[string]any)["role"] != "user" {
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
			systemPromptMode: dbmodel.SystemPromptModeOverride,
			systemPrompt:     "managed",
		},
		channel: &dbmodel.Channel{
			Type:          outbound.OutboundTypeOpenAIChat,
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
