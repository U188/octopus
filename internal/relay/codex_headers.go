package relay

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
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
	ra.applyCodexResponseHeadersMode(req, true)
}

// applyCodexResponseHeadersPreservingBody adds Codex headers to the compact
// endpoint without injecting ordinary turn fields into its request body.
func (ra *relayAttempt) applyCodexResponseHeadersPreservingBody(req *http.Request) {
	ra.applyCodexResponseHeadersMode(req, false)
}

func (ra *relayAttempt) applyCodexResponseHeadersMode(req *http.Request, normalizeBody bool) {
	if req == nil || !ra.shouldUseCodexResponseHeaders() {
		return
	}

	req.Header = http.Header{}
	headers := codexmode.HeadersForProfile(ra.channel.CodexHeaderProfile)

	sourceHeaders := ra.clientRequestHeaders()
	sessionID := strings.TrimSpace(sourceHeaders.Get("Session-Id"))
	threadID := strings.TrimSpace(sourceHeaders.Get("Thread-Id"))
	windowID := strings.TrimSpace(sourceHeaders.Get("X-Codex-Window-Id"))
	clientRequestID := strings.TrimSpace(sourceHeaders.Get("X-Client-Request-Id"))
	turnMetadataString := strings.TrimSpace(sourceHeaders.Get("X-Codex-Turn-Metadata"))
	turnMetadataValues := parseCodexTurnMetadata(turnMetadataString)
	turnID := strings.TrimSpace(turnMetadataValues["turn_id"])
	installationID := strings.TrimSpace(turnMetadataValues["installation_id"])
	if installationID == "" {
		installationID = strings.TrimSpace(sourceHeaders.Get("X-Codex-Installation-Id"))
	}
	if sessionID == "" {
		sessionID = uuid.Must(uuid.NewV7()).String()
	}
	if threadID == "" {
		threadID = sessionID
	}
	if windowID == "" {
		windowID = sessionID + ":0"
	}
	if clientRequestID == "" {
		clientRequestID = sessionID
	}
	if turnID == "" {
		turnID = uuid.Must(uuid.NewV7()).String()
	}
	if installationID == "" {
		installationID = uuid.NewString()
	}
	if turnMetadataString == "" {
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
		turnMetadataString = string(turnMetadataJSON)
	}

	if normalizeBody {
		ra.normalizeCodexResponsesBody(req, sessionID, threadID, turnID, windowID, installationID, turnMetadataString)
		ra.applyKnownCodexCompactionCompatibility(req)
	}

	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", headers.UserAgent)
	req.Header.Set("Originator", headers.Originator)
	req.Header.Set("X-Codex-Beta-Features", headers.BetaFeatures)
	req.Header.Set(codexmode.ResponsesLiteHeader, headers.ResponsesLiteHeaderValue)
	req.Header.Set("Session-Id", sessionID)
	req.Header.Set("Thread-Id", threadID)
	req.Header.Set("X-Codex-Window-Id", windowID)
	req.Header.Set("X-Client-Request-Id", clientRequestID)
	req.Header.Set("X-Codex-Turn-Metadata", turnMetadataString)
	req.Header.Set("Authorization", "Bearer "+ra.usedKey.ChannelKey)
}

func (ra *relayAttempt) applyKnownCodexCompactionCompatibility(req *http.Request) {
	if req == nil || req.URL == nil || !isKnownCodexCompactionIncompatibleHost(req.URL.Hostname()) {
		return
	}
	body, err := readOutboundRequestBody(req)
	if err != nil {
		return
	}
	compatibleBody, changed, err := stripCodexCompactionItems(body)
	if err != nil || !changed {
		return
	}
	resetRequestBody(req, compatibleBody)
}

func isKnownCodexCompactionIncompatibleHost(host string) bool {
	host = strings.ToLower(strings.TrimSpace(host))
	for _, domain := range []string{"anyrouter.top", "agentrouter.org"} {
		if host == domain || strings.HasSuffix(host, "."+domain) {
			return true
		}
	}
	return false
}

func parseCodexTurnMetadata(raw string) map[string]string {
	values := make(map[string]string)
	if strings.TrimSpace(raw) == "" {
		return values
	}
	var metadata map[string]any
	if err := json.Unmarshal([]byte(raw), &metadata); err != nil {
		return values
	}
	for _, key := range []string{"installation_id", "session_id", "thread_id", "turn_id", "window_id"} {
		if value, ok := metadata[key].(string); ok {
			values[key] = strings.TrimSpace(value)
		}
	}
	return values
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
		payload["parallel_tool_calls"] = codexmode.ParallelToolCalls
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
	normalizeCodexResponsesEnvelope(payload)

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
	if _, ok := reasoning["context"]; !ok {
		reasoning["context"] = codexmode.ReasoningContext
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

// stripCodexCompactionItems removes opaque remote-compaction markers from a
// Responses request. Some Codex-compatible upstreams reject the marker even
// though they accept the surrounding Responses items. The caller only uses
// this after that exact upstream validation error, so compatible providers
// retain the normal remote-compaction path.
func stripCodexCompactionItems(body []byte) ([]byte, bool, error) {
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, false, err
	}
	input, ok := payload["input"].([]any)
	if !ok || len(input) == 0 {
		return nil, false, nil
	}
	filtered := make([]any, 0, len(input))
	removed := false
	for _, value := range input {
		item, ok := value.(map[string]any)
		if ok {
			if itemType, _ := item["type"].(string); itemType == "compaction" {
				removed = true
				continue
			}
		}
		filtered = append(filtered, value)
	}
	if !removed {
		return nil, false, nil
	}
	payload["input"] = filtered
	withoutCompaction, err := json.Marshal(payload)
	if err != nil {
		return nil, false, err
	}
	return withoutCompaction, true, nil
}

// Codex 0.144.5 represents developer instructions and tool declarations as
// input items instead of the legacy top-level instructions/tools fields.
func normalizeCodexResponsesEnvelope(payload map[string]any) {
	prefix := make([]any, 0, 2)

	if tools, ok := codexResponsesToolList(payload["tools"]); ok {
		delete(payload, "tools")
		if len(tools) > 0 {
			prefix = append(prefix, map[string]any{
				"type":  "additional_tools",
				"role":  "developer",
				"tools": tools,
			})
		}
	}

	if instructions, ok := payload["instructions"].(string); ok {
		delete(payload, "instructions")
		if instructions != "" {
			prefix = append(prefix, map[string]any{
				"type": "message",
				"role": "developer",
				"content": []any{
					map[string]any{"type": "input_text", "text": instructions},
				},
			})
		}
	}

	if len(prefix) == 0 {
		return
	}
	input := codexResponsesInputList(payload["input"])
	payload["input"] = append(prefix, input...)
}

func codexResponsesInputList(value any) []any {
	switch values := value.(type) {
	case nil:
		return nil
	case []any:
		return values
	case []map[string]any:
		items := make([]any, len(values))
		for i := range values {
			items[i] = values[i]
		}
		return items
	default:
		return []any{value}
	}
}

func codexResponsesToolList(value any) ([]any, bool) {
	switch tools := value.(type) {
	case []any:
		return tools, true
	case []map[string]any:
		items := make([]any, len(tools))
		for i := range tools {
			items[i] = tools[i]
		}
		return items, true
	default:
		return nil, false
	}
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
	tools, ok := codexResponsesToolList(value)
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
