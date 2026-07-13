package relay

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"time"

	"github.com/U188/octopus/internal/codexmode"
	"github.com/U188/octopus/internal/transformer/outbound"
	"github.com/google/uuid"
)

func (ra *relayAttempt) shouldUseCodexResponseHeaders() bool {
	return ra != nil &&
		ra.channel != nil &&
		ra.channel.CodexMode &&
		ra.channel.Type == outbound.OutboundTypeOpenAIResponse
}

func (ra *relayAttempt) applyCodexResponseHeaders(req *http.Request) {
	if req == nil || !ra.shouldUseCodexResponseHeaders() {
		return
	}

	req.Header = http.Header{}

	sessionID := uuid.Must(uuid.NewV7()).String()
	threadID := sessionID
	windowID := sessionID + ":0"
	turnID := uuid.Must(uuid.NewV7()).String()
	clientRequestID := sessionID
	installationID := uuid.NewString()
	turnMetadata := map[string]any{
		"installation_id":         installationID,
		"session_id":              sessionID,
		"thread_id":               threadID,
		"turn_id":                 turnID,
		"window_id":               windowID,
		"request_kind":            "turn",
		"thread_source":           "user",
		"sandbox":                 codexmode.Sandbox,
		"turn_started_at_unix_ms": time.Now().UnixMilli(),
	}
	turnMetadataJSON, _ := json.Marshal(turnMetadata)
	turnMetadataString := string(turnMetadataJSON)

	ra.normalizeCodexResponsesBody(req, sessionID, threadID, turnID, windowID, installationID, turnMetadataString)

	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", codexmode.UserAgent)
	req.Header.Set("Originator", codexmode.Originator)
	req.Header.Set("X-Codex-Beta-Features", codexmode.BetaFeatures)
	req.Header.Set(codexmode.ResponsesLiteHeader, codexmode.ResponsesLiteHeaderValue)
	req.Header.Set("Session-Id", sessionID)
	req.Header.Set("Thread-Id", threadID)
	req.Header.Set("X-Codex-Window-Id", windowID)
	req.Header.Set("X-Client-Request-Id", clientRequestID)
	req.Header.Set("X-Codex-Turn-Metadata", turnMetadataString)
	req.Header.Set("Authorization", "Bearer "+ra.usedKey.ChannelKey)
}

func (ra *relayAttempt) normalizeCodexResponsesBody(req *http.Request, sessionID, threadID, turnID, windowID, installationID, turnMetadata string) {
	if req == nil || req.Body == nil {
		return
	}

	body, err := io.ReadAll(req.Body)
	if err != nil {
		return
	}
	if len(body) == 0 {
		req.Body = io.NopCloser(bytes.NewReader(body))
		return
	}

	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		resetRequestBody(req, body)
		return
	}

	if _, ok := payload["store"]; !ok {
		payload["store"] = false
	}
	if _, ok := payload["parallel_tool_calls"]; !ok {
		payload["parallel_tool_calls"] = true
	}
	if _, ok := payload["prompt_cache_key"]; !ok {
		payload["prompt_cache_key"] = sessionID
	}
	if _, ok := payload["text"]; !ok {
		payload["text"] = map[string]any{"verbosity": "low"}
	}
	ensureCodexResponsesReasoning(payload)
	ensureCodexResponsesInclude(payload)
	if _, ok := payload["tool_choice"]; !ok && hasResponsesTools(payload["tools"]) {
		payload["tool_choice"] = "auto"
	}
	normalizeCodexResponsesInput(payload)

	metadata, _ := payload["client_metadata"].(map[string]any)
	if metadata == nil {
		metadata = map[string]any{}
		payload["client_metadata"] = metadata
	}
	setDefaultMetadata(metadata, "session_id", sessionID)
	setDefaultMetadata(metadata, "thread_id", threadID)
	setDefaultMetadata(metadata, "turn_id", turnID)
	setDefaultMetadata(metadata, "x-codex-installation-id", installationID)
	setDefaultMetadata(metadata, "x-codex-turn-metadata", turnMetadata)
	setDefaultMetadata(metadata, "x-codex-window-id", windowID)

	// The Codex client shape does not send sampling temperature, and the Any
	// Codex endpoint rejects RikkaHub requests that include it.
	delete(payload, "temperature")

	normalized, err := json.Marshal(payload)
	if err != nil {
		resetRequestBody(req, body)
		return
	}
	resetRequestBody(req, normalized)
}

func ensureCodexResponsesReasoning(payload map[string]any) {
	reasoning, _ := payload["reasoning"].(map[string]any)
	if reasoning == nil {
		reasoning = map[string]any{}
		payload["reasoning"] = reasoning
	}
	if _, ok := reasoning["effort"]; !ok {
		reasoning["effort"] = "high"
	}
}

func ensureCodexResponsesInclude(payload map[string]any) {
	const encryptedReasoning = "reasoning.encrypted_content"
	include, ok := payload["include"]
	if !ok {
		payload["include"] = []any{encryptedReasoning}
		return
	}
	switch values := include.(type) {
	case []any:
		for _, value := range values {
			if value == encryptedReasoning {
				return
			}
		}
		payload["include"] = append(values, encryptedReasoning)
	case []string:
		for _, value := range values {
			if value == encryptedReasoning {
				return
			}
		}
		next := make([]any, 0, len(values)+1)
		for _, value := range values {
			next = append(next, value)
		}
		payload["include"] = append(next, encryptedReasoning)
	}
}

func normalizeCodexResponsesInput(payload map[string]any) {
	input, ok := payload["input"]
	if !ok {
		return
	}
	payload["input"] = normalizeCodexResponsesInputValue(input)
}

func normalizeCodexResponsesInputValue(value any) any {
	switch v := value.(type) {
	case string:
		return []any{map[string]any{
			"type": "message",
			"role": "user",
			"content": []any{
				map[string]any{"type": "input_text", "text": v},
			},
		}}
	case []any:
		items := make([]any, len(v))
		for i, item := range v {
			items[i] = normalizeCodexResponsesInputItem(item)
		}
		return items
	case []map[string]any:
		items := make([]any, len(v))
		for i := range v {
			items[i] = normalizeCodexResponsesInputItem(v[i])
		}
		return items
	default:
		return value
	}
}

func normalizeCodexResponsesInputItem(value any) any {
	item, ok := value.(map[string]any)
	if !ok {
		return value
	}
	if _, hasType := item["type"]; !hasType && item["role"] != nil {
		item["type"] = "message"
	}
	if item["type"] != "message" {
		return item
	}
	switch content := item["content"].(type) {
	case string:
		item["content"] = []any{map[string]any{"type": "input_text", "text": content}}
	case []any:
		for i, part := range content {
			content[i] = normalizeCodexResponsesContentPart(part)
		}
		item["content"] = content
	case []map[string]any:
		parts := make([]any, len(content))
		for i := range content {
			parts[i] = normalizeCodexResponsesContentPart(content[i])
		}
		item["content"] = parts
	}
	return item
}

func normalizeCodexResponsesContentPart(value any) any {
	part, ok := value.(map[string]any)
	if !ok {
		if text, ok := value.(string); ok {
			return map[string]any{"type": "input_text", "text": text}
		}
		return value
	}
	if _, hasType := part["type"]; !hasType {
		if _, hasText := part["text"]; hasText {
			part["type"] = "input_text"
		}
	}
	return part
}

func hasResponsesTools(value any) bool {
	tools, ok := value.([]any)
	return ok && len(tools) > 0
}

func setDefaultMetadata(metadata map[string]any, key, value string) {
	if _, ok := metadata[key]; !ok {
		metadata[key] = value
	}
}

func resetRequestBody(req *http.Request, body []byte) {
	req.Body = io.NopCloser(bytes.NewReader(body))
	req.ContentLength = int64(len(body))
	bodyCopy := append([]byte(nil), body...)
	req.GetBody = func() (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(bodyCopy)), nil
	}
}
