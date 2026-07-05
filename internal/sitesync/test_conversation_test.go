package sitesync

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/U188/octopus/internal/codexmode"
	"github.com/U188/octopus/internal/model"
)

func TestBuildTestConversationRequestCompletesV1BaseURL(t *testing.T) {
	siteRecord := &model.Site{
		Platform: model.SitePlatformAPI,
		BaseURL:  "https://example.com",
	}
	token := model.SiteToken{Token: "sk-test"}

	tests := []struct {
		name     string
		mode     TestConversationMode
		client   TestConversationClient
		expected string
	}{
		{
			name:     "openai chat appends v1",
			mode:     TestConversationModeOpenAIChat,
			expected: "https://example.com/v1/chat/completions",
		},
		{
			name:     "openai responses appends v1",
			mode:     TestConversationModeOpenAIResponse,
			expected: "https://example.com/v1/responses",
		},
		{
			name:     "openai images appends v1",
			mode:     TestConversationModeOpenAIImage,
			expected: "https://example.com/v1/images/generations",
		},
		{
			name:     "codex responses appends v1",
			mode:     TestConversationModeOpenAIResponse,
			client:   TestConversationClientCodex,
			expected: "https://example.com/v1/responses",
		},
		{
			name:     "anthropic messages appends v1",
			mode:     TestConversationModeAnthropic,
			expected: "https://example.com/v1/messages",
		},
		{
			name:     "claude messages appends v1 beta",
			mode:     TestConversationModeAnthropic,
			client:   TestConversationClientClaude,
			expected: "https://example.com/v1/messages?beta=true",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			requestURL, _, _ := buildTestConversationRequest(siteRecord, token, "gpt-4o", tt.mode, "hi", tt.client, false)
			if requestURL != tt.expected {
				t.Fatalf("expected %q, got %q", tt.expected, requestURL)
			}
		})
	}
}

func TestBuildTestConversationRequestUsesRouteOverrideVerbatim(t *testing.T) {
	siteRecord := &model.Site{
		Platform: model.SitePlatformAPI,
		BaseURL:  "https://example.com",
		RouteBaseURLs: []model.SiteRouteBaseURL{
			{RouteType: model.SiteModelRouteTypeOpenAIResponse, BaseURL: "https://gateway.example.com/openai/v1"},
			{RouteType: model.SiteModelRouteTypeAnthropic, BaseURL: "https://gateway.example.com/anthropic/v1"},
		},
	}
	token := model.SiteToken{Token: "sk-test"}

	responseURL, _, _ := buildTestConversationRequest(siteRecord, token, "gpt-4o", TestConversationModeOpenAIResponse, "hi", TestConversationClientDefault, false)
	if responseURL != "https://gateway.example.com/openai/v1/responses" {
		t.Fatalf("expected route override response URL, got %q", responseURL)
	}

	siteRecord.RouteBaseURLs = append(siteRecord.RouteBaseURLs, model.SiteRouteBaseURL{RouteType: model.SiteModelRouteTypeOpenAIImage, BaseURL: "https://gateway.example.com/openai/v1"})
	imageURL, imageBody, _ := buildTestConversationRequest(siteRecord, token, "gpt-image-2", TestConversationModeOpenAIImage, "draw a cube", TestConversationClientDefault, false)
	if imageURL != "https://gateway.example.com/openai/v1/images/generations" {
		t.Fatalf("expected route override image URL, got %q", imageURL)
	}
	if imageBody["prompt"] != "draw a cube" || imageBody["model"] != "gpt-image-2" {
		t.Fatalf("unexpected image request body: %#v", imageBody)
	}

	anthropicURL, _, _ := buildTestConversationRequest(siteRecord, token, "claude-sonnet-4", TestConversationModeAnthropic, "hi", TestConversationClientDefault, false)
	if anthropicURL != "https://gateway.example.com/anthropic/v1/messages" {
		t.Fatalf("expected route override anthropic URL, got %q", anthropicURL)
	}

	claudeURL, _, _ := buildTestConversationRequest(siteRecord, token, "claude-sonnet-4", TestConversationModeAnthropic, "hi", TestConversationClientClaude, false)
	if claudeURL != "https://gateway.example.com/anthropic/v1/messages?beta=true" {
		t.Fatalf("expected route override claude URL, got %q", claudeURL)
	}
}

func TestBuildTestConversationRequestResponsesDefaultsToStream(t *testing.T) {
	siteRecord := &model.Site{
		Platform: model.SitePlatformAPI,
		BaseURL:  "https://example.com",
	}
	token := model.SiteToken{Token: "sk-test"}

	requestURL, body, headers := buildTestConversationRequest(siteRecord, token, "gpt-5.5", TestConversationModeOpenAIResponse, "hi", TestConversationClientDefault, false)
	if requestURL != "https://example.com/v1/responses" {
		t.Fatalf("expected responses URL, got %q", requestURL)
	}
	if body["stream"] != true {
		t.Fatalf("expected stream=true for responses test conversation, got %#v", body["stream"])
	}
	if headers["Accept"] != "text/event-stream" {
		t.Fatalf("expected SSE accept header, got %q", headers["Accept"])
	}
}

func TestBuildTestConversationRequestCodexMatchesClientShape(t *testing.T) {
	siteRecord := &model.Site{
		Platform: model.SitePlatformAPI,
		BaseURL:  "https://example.com",
	}
	token := model.SiteToken{Token: "sk-test"}

	requestURL, body, headers := buildTestConversationRequest(siteRecord, token, "gpt-5.5", TestConversationModeOpenAIResponse, "hi", TestConversationClientCodex, false)
	if requestURL != "https://example.com/v1/responses" {
		t.Fatalf("expected codex responses URL, got %q", requestURL)
	}
	if headers["Accept"] != "text/event-stream" {
		t.Fatalf("expected codex stream accept header, got %q", headers["Accept"])
	}
	if headers["Originator"] != codexmode.Originator {
		t.Fatalf("expected codex originator, got %q", headers["Originator"])
	}
	if headers["User-Agent"] != codexmode.UserAgent {
		t.Fatalf("expected codex user agent, got %q", headers["User-Agent"])
	}
	if headers["X-Codex-Beta-Features"] != codexmode.BetaFeatures {
		t.Fatalf("expected codex beta features header, got %q", headers["X-Codex-Beta-Features"])
	}
	if body["store"] != false || body["stream"] != true {
		t.Fatalf("unexpected codex body flags: %#v", body)
	}
	if tools, ok := body["tools"].([]map[string]any); !ok || len(tools) == 0 {
		t.Fatalf("expected codex tool definitions, got %#v", body["tools"])
	}
	for _, tool := range body["tools"].([]map[string]any) {
		if tool["type"] == "tool_search" {
			t.Fatalf("codex test conversation must not include tool_search; some upstreams end the stream after codex.rate_limits")
		}
	}
	if body["tool_choice"] != "auto" {
		t.Fatalf("expected codex tool_choice auto, got %#v", body["tool_choice"])
	}
	if body["parallel_tool_calls"] != true {
		t.Fatalf("expected codex parallel tool calls, got %#v", body["parallel_tool_calls"])
	}
	if instructions, ok := body["instructions"].(string); !ok || !strings.Contains(instructions, "Do not call tools") {
		t.Fatalf("expected no-tools instruction, got %#v", body["instructions"])
	}
	if include, ok := body["include"].([]string); !ok || len(include) != 1 || include[0] != "reasoning.encrypted_content" {
		t.Fatalf("unexpected include field: %#v", body["include"])
	}
	input, ok := body["input"].([]map[string]any)
	if !ok || len(input) != 1 || input[0]["type"] != "message" || input[0]["role"] != "user" {
		t.Fatalf("unexpected codex input shape: %#v", body["input"])
	}
	metadata, ok := body["client_metadata"].(map[string]string)
	if !ok || metadata["session_id"] == "" || metadata["x-codex-turn-metadata"] == "" {
		t.Fatalf("unexpected codex client metadata: %#v", body["client_metadata"])
	}
}

func TestBuildTestConversationRequestClaudeMatchesClientShape(t *testing.T) {
	siteRecord := &model.Site{
		Platform: model.SitePlatformAPI,
		BaseURL:  "https://example.com",
	}
	token := model.SiteToken{Token: "sk-test"}

	requestURL, body, headers := buildTestConversationRequest(siteRecord, token, "claude-sonnet-4-5-20250929", TestConversationModeAnthropic, "hi", TestConversationClientClaude, false)
	if requestURL != "https://example.com/v1/messages?beta=true" {
		t.Fatalf("expected claude messages URL, got %q", requestURL)
	}
	if headers["User-Agent"] != claudeTestConversationUserAgent {
		t.Fatalf("expected claude user agent, got %q", headers["User-Agent"])
	}
	if headers["X-App"] != "cli" || headers["X-Claude-Code-Session-Id"] == "" {
		t.Fatalf("expected claude client headers, got %#v", headers)
	}
	if !strings.Contains(headers["anthropic-beta"], claudeTestConversationBeta) || strings.Contains(headers["anthropic-beta"], "context-1m-2025-08-07") {
		t.Fatalf("expected claude beta header, got %q", headers["anthropic-beta"])
	}
	if body["stream"] != true || body["max_tokens"] != 32000 {
		t.Fatalf("unexpected claude body flags: %#v", body)
	}
	if thinking, ok := body["thinking"].(map[string]any); !ok || thinking["type"] != "enabled" || thinking["budget_tokens"] != 31999 {
		t.Fatalf("unexpected claude thinking: %#v", body["thinking"])
	}
	if _, ok := body["context_management"].(map[string]any); !ok {
		t.Fatalf("expected claude context management, got %#v", body["context_management"])
	}
	if metadata, ok := body["metadata"].(map[string]string); !ok || !strings.Contains(metadata["user_id"], "session_id") {
		t.Fatalf("unexpected claude metadata: %#v", body["metadata"])
	}

	_, _, contextHeaders := buildTestConversationRequest(siteRecord, token, "claude-opus-4-7", TestConversationModeAnthropic, "hi", TestConversationClientClaude, true)
	if !strings.Contains(contextHeaders["anthropic-beta"], "context-1m-2025-08-07") {
		t.Fatalf("expected explicit context 1m beta, got %q", contextHeaders["anthropic-beta"])
	}
}

func TestRequestCodexTestConversationStreamParsesSSE(t *testing.T) {
	var capturedAccept string
	var capturedBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedAccept = r.Header.Get("Accept")
		if err := json.NewDecoder(r.Body).Decode(&capturedBody); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"type\":\"response.output_text.delta\",\"delta\":\"h\"}\n\n"))
		_, _ = w.Write([]byte("data: {\"type\":\"response.output_text.delta\",\"delta\":\"i\"}\n\n"))
		_, _ = w.Write([]byte("data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_1\",\"object\":\"response\",\"status\":\"completed\"}}\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer server.Close()

	siteRecord := &model.Site{
		Platform: model.SitePlatformAPI,
		BaseURL:  server.URL,
	}
	_, body, headers := buildTestConversationRequest(siteRecord, model.SiteToken{Token: "sk-test"}, "gpt-5.5", TestConversationModeOpenAIResponse, "hi", TestConversationClientCodex, false)

	payload, err := requestCodexTestConversationStream(context.Background(), siteRecord, server.URL, body, headers, nil, nil)
	if err != nil {
		t.Fatalf("request codex stream failed: %v", err)
	}
	if capturedAccept != "text/event-stream" {
		t.Fatalf("expected Accept text/event-stream, got %q", capturedAccept)
	}
	if capturedBody["stream"] != true {
		t.Fatalf("expected stream=true in request body, got %#v", capturedBody["stream"])
	}
	if payload["output_text"] != "hi" {
		t.Fatalf("expected parsed output text, got %#v", payload)
	}
	if payload["stream"] != true {
		t.Fatalf("expected stream marker in payload, got %#v", payload)
	}
}

func TestRequestClaudeTestConversationStreamParsesSSE(t *testing.T) {
	var capturedUserAgent string
	var capturedBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedUserAgent = r.Header.Get("User-Agent")
		if err := json.NewDecoder(r.Body).Decode(&capturedBody); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_1\",\"type\":\"message\",\"role\":\"assistant\",\"model\":\"claude-sonnet-4-5-20250929\",\"content\":[],\"usage\":{\"input_tokens\":1,\"output_tokens\":0}}}\n\n"))
		_, _ = w.Write([]byte("data: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n"))
		_, _ = w.Write([]byte("data: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"hi\"}}\n\n"))
		_, _ = w.Write([]byte("data: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\",\"stop_sequence\":null},\"usage\":{\"output_tokens\":1}}\n\n"))
		_, _ = w.Write([]byte("data: {\"type\":\"message_stop\"}\n\n"))
	}))
	defer server.Close()

	siteRecord := &model.Site{
		Platform: model.SitePlatformAPI,
		BaseURL:  server.URL,
	}
	_, body, headers := buildTestConversationRequest(siteRecord, model.SiteToken{Token: "sk-test"}, "claude-sonnet-4-5-20250929", TestConversationModeAnthropic, "hi", TestConversationClientClaude, false)

	payload, err := requestClaudeTestConversationStream(context.Background(), siteRecord, server.URL, body, headers, nil, nil)
	if err != nil {
		t.Fatalf("request claude stream failed: %v", err)
	}
	if capturedUserAgent != claudeTestConversationUserAgent {
		t.Fatalf("expected claude user agent, got %q", capturedUserAgent)
	}
	if capturedBody["stream"] != true {
		t.Fatalf("expected stream=true in request body, got %#v", capturedBody["stream"])
	}
	if reply := extractTestConversationReply(TestConversationModeAnthropic, payload); reply != "hi" {
		t.Fatalf("expected parsed reply, got %q payload=%#v", reply, payload)
	}
}

func TestParseCodexTestConversationSSEUsesContentPartDoneText(t *testing.T) {
	raw := strings.Join([]string{
		`data: {"type":"response.created","response":{"id":"resp_1","model":"gpt-5.5"}}`,
		"",
		`data: {"type":"response.content_part.done","part":{"type":"output_text","text":"hi there"}}`,
		"",
		`data: {"type":"response.completed","response":{"id":"resp_1","object":"response","status":"completed","output":[]}}`,
		"",
	}, "\n")

	payload, err := parseCodexTestConversationSSE(strings.NewReader(raw))
	if err != nil {
		t.Fatalf("parse sse failed: %v", err)
	}
	if payload["output_text"] != "hi there" {
		t.Fatalf("expected content_part.done text, got %#v", payload)
	}
}

func TestParseCodexTestConversationSSEUsesOutputItemDoneText(t *testing.T) {
	raw := strings.Join([]string{
		`data: {"type":"response.output_item.done","item":{"type":"message","content":[{"type":"output_text","text":"hello"}]}}`,
		"",
		`data: {"type":"response.completed","response":{"id":"resp_1","object":"response","status":"completed","output":[]}}`,
		"",
	}, "\n")

	payload, err := parseCodexTestConversationSSE(strings.NewReader(raw))
	if err != nil {
		t.Fatalf("parse sse failed: %v", err)
	}
	if payload["output_text"] != "hello" {
		t.Fatalf("expected output_item.done text, got %#v", payload)
	}
}

func TestParseCodexTestConversationSSEUsesCompletedOutputText(t *testing.T) {
	raw := strings.Join([]string{
		`data: {"type":"response.completed","response":{"id":"resp_1","object":"response","status":"completed","output":[{"type":"message","content":[{"type":"output_text","text":"completed text"}]}]}}`,
		"",
	}, "\n")

	payload, err := parseCodexTestConversationSSE(strings.NewReader(raw))
	if err != nil {
		t.Fatalf("parse sse failed: %v", err)
	}
	if payload["output_text"] != "completed text" {
		t.Fatalf("expected completed output text, got %#v", payload)
	}
	if eventTypes, ok := payload["stream_event_types"].([]string); !ok || len(eventTypes) != 1 || eventTypes[0] != "response.completed" {
		t.Fatalf("expected stream event types, got %#v", payload["stream_event_types"])
	}
}

func TestParseCodexTestConversationSSEEmitsTextDeltas(t *testing.T) {
	raw := strings.Join([]string{
		`data: {"type":"response.output_text.delta","delta":"he"}`,
		"",
		`data: {"type":"response.output_text.delta","delta":"llo"}`,
		"",
		`data: {"type":"response.completed","response":{"id":"resp_1","object":"response","status":"completed"}}`,
		"",
	}, "\n")
	var deltas []string
	payload, err := parseCodexTestConversationSSEWithEmit(strings.NewReader(raw), func(event TestConversationStreamEvent) error {
		if event.Type == "delta" {
			deltas = append(deltas, event.Delta)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("parse sse failed: %v", err)
	}
	if payload["output_text"] != "hello" {
		t.Fatalf("expected output text, got %#v", payload)
	}
	if strings.Join(deltas, "") != "hello" {
		t.Fatalf("expected emitted deltas, got %#v", deltas)
	}
}

func TestParseCodexTestConversationSSEPreservesFunctionCallOutput(t *testing.T) {
	raw := strings.Join([]string{
		`data: {"type":"response.created","response":{"id":"resp_1","model":"gpt-5.5"}}`,
		"",
		`data: {"type":"response.output_item.added","output_index":0,"item":{"id":"fc_1","type":"function_call","name":"shell_command","arguments":"","call_id":"call_1"}}`,
		"",
		`data: {"type":"response.function_call_arguments.delta","output_index":0,"item_id":"fc_1","delta":"{\"command\":\"date\"}"}`,
		"",
		`data: {"type":"response.function_call_arguments.done","output_index":0,"item_id":"fc_1","arguments":"{\"command\":\"date\"}"}`,
		"",
		`data: {"type":"response.output_item.done","output_index":0,"item":{"id":"fc_1","type":"function_call","name":"shell_command","arguments":"{\"command\":\"date\"}","call_id":"call_1"}}`,
		"",
		`data: {"type":"response.completed","response":{"id":"resp_1","object":"response","status":"completed","output":[]}}`,
		"",
	}, "\n")

	payload, err := parseCodexTestConversationSSE(strings.NewReader(raw))
	if err != nil {
		t.Fatalf("parse sse failed: %v", err)
	}
	reply := extractTestConversationReply(TestConversationModeOpenAIResponse, payload)
	if !strings.Contains(reply, "Tool call: shell_command") || !strings.Contains(reply, `"command":"date"`) {
		t.Fatalf("expected function call reply, got %q payload=%#v", reply, payload)
	}
	output, ok := payload["output"].([]any)
	if !ok || len(output) != 1 {
		t.Fatalf("expected preserved output item, got %#v", payload["output"])
	}
}

func TestParseCodexTestConversationSSEHandlesCRLFSeparators(t *testing.T) {
	raw := "data: {\"type\":\"response.output_text.delta\",\"delta\":\"hello\"}\r\n\r\n" +
		"data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_1\",\"object\":\"response\",\"status\":\"completed\"}}\r\n\r\n"

	payload, err := parseCodexTestConversationSSE(strings.NewReader(raw))
	if err != nil {
		t.Fatalf("parse sse failed: %v", err)
	}
	if payload["output_text"] != "hello" {
		t.Fatalf("expected output text from CRLF stream, got %#v", payload)
	}
}

func TestExtractTestConversationReplyHandlesWrappedResponsesPayload(t *testing.T) {
	payload := map[string]any{
		"message": "success",
		"data": map[string]any{
			"output": []any{
				map[string]any{
					"type": "message",
					"content": []any{
						map[string]any{"type": "output_text", "text": "wrapped text"},
					},
				},
			},
		},
	}

	if got := extractTestConversationReply(TestConversationModeOpenAIResponse, payload); got != "wrapped text" {
		t.Fatalf("expected wrapped text, got %q", got)
	}
}

func TestExtractTestConversationReplyPrefersStructuredResponsesOutput(t *testing.T) {
	payload := map[string]any{
		"output_text": "Hi!HowcanIhelp?",
		"output": []any{
			map[string]any{
				"type": "message",
				"content": []any{
					map[string]any{"type": "output_text", "text": "Hi! How can I help?"},
				},
			},
		},
	}

	if got := extractTestConversationReply(TestConversationModeOpenAIResponse, payload); got != "Hi! How can I help?" {
		t.Fatalf("expected structured output text, got %q", got)
	}
}

func TestExtractTestConversationReplyIgnoresEnvelopeStatusMessage(t *testing.T) {
	payload := map[string]any{
		"message": "success",
		"data": map[string]any{
			"message": "request completed",
		},
	}

	if got := extractTestConversationReply(TestConversationModeOpenAIResponse, payload); got != "No text content returned." {
		t.Fatalf("expected no text fallback, got %q", got)
	}
}

func TestExtractTestConversationReplyHandlesChatContentParts(t *testing.T) {
	payload := map[string]any{
		"choices": []any{
			map[string]any{
				"message": map[string]any{
					"content": []any{
						map[string]any{"type": "text", "text": "part text"},
					},
				},
			},
		},
	}

	if got := extractTestConversationReply(TestConversationModeOpenAIChat, payload); got != "part text" {
		t.Fatalf("expected chat content part text, got %q", got)
	}
}

func TestExtractTestConversationReplyHandlesImageGeneration(t *testing.T) {
	payload := map[string]any{
		"data": []any{
			map[string]any{"url": "https://cdn.example.com/image.png"},
			map[string]any{"b64_json": "abcdef"},
		},
	}

	got := extractTestConversationReply(TestConversationModeOpenAIImage, payload)
	if !strings.Contains(got, "Image 1: https://cdn.example.com/image.png") {
		t.Fatalf("expected image URL summary, got %q", got)
	}
	if !strings.Contains(got, "Image 2: b64_json returned (6 bytes)") {
		t.Fatalf("expected b64 summary, got %q", got)
	}
}

func TestExtractTestConversationImagesHandlesImageGeneration(t *testing.T) {
	payload := map[string]any{
		"data": []any{
			map[string]any{"url": "https://cdn.example.com/image.png", "revised_prompt": "clean prompt"},
			map[string]any{"b64_json": "abcdef", "output_format": "webp"},
		},
	}

	images := extractTestConversationImages(TestConversationModeOpenAIImage, payload)
	if len(images) != 2 {
		t.Fatalf("expected 2 images, got %#v", images)
	}
	if images[0].URL != "https://cdn.example.com/image.png" || images[0].RevisedPrompt != "clean prompt" {
		t.Fatalf("unexpected URL image: %#v", images[0])
	}
	if images[1].B64JSON != "abcdef" || images[1].MimeType != "image/webp" {
		t.Fatalf("unexpected b64 image: %#v", images[1])
	}

	wrapped := map[string]any{
		"data": map[string]any{
			"data": []any{
				map[string]any{"b64_json": "xyz", "mime_type": "image/jpeg"},
			},
		},
	}
	images = extractTestConversationImages(TestConversationModeOpenAIImage, wrapped)
	if len(images) != 1 || images[0].B64JSON != "xyz" || images[0].MimeType != "image/jpeg" {
		t.Fatalf("unexpected wrapped image: %#v", images)
	}

	if got := extractTestConversationImages(TestConversationModeOpenAIChat, payload); len(got) != 0 {
		t.Fatalf("expected non-image mode to return no images, got %#v", got)
	}
}
