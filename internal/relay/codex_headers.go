package relay

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"time"

	"github.com/U188/octopus/internal/transformer/outbound"
	"github.com/google/uuid"
)

const codexExecUserAgent = "codex_exec/0.142.4 (Windows 10.0.19044; x86_64) unknown (codex_exec; 0.142.4)"
const codexRelayInstallationID = "00000000-0000-4000-8000-000000000001"

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

	sessionID := uuid.NewString()
	threadID := uuid.NewString()
	windowID := uuid.NewString()
	turnID := uuid.NewString()
	clientRequestID := uuid.NewString()
	turnMetadata := map[string]any{
		"originator":              "codex_exec",
		"client_request_id":       clientRequestID,
		"installation_id":         codexRelayInstallationID,
		"session_id":              sessionID,
		"thread_id":               threadID,
		"turn_id":                 turnID,
		"window_id":               windowID,
		"request_kind":            "turn",
		"thread_source":           "user",
		"sandbox":                 "windows_sandbox",
		"turn_started_at_unix_ms": time.Now().UnixMilli(),
		"timestamp":               time.Now().UTC().Format(time.RFC3339Nano),
	}
	turnMetadataJSON, _ := json.Marshal(turnMetadata)
	turnMetadataString := string(turnMetadataJSON)

	ra.normalizeCodexResponsesBody(req, sessionID, threadID, turnID, windowID, turnMetadataString)

	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", codexExecUserAgent)
	req.Header.Set("Originator", "codex_exec")
	req.Header.Set("X-Codex-Beta-Features", "remote_compaction_v2")
	req.Header.Set("Session-Id", sessionID)
	req.Header.Set("Thread-Id", threadID)
	req.Header.Set("X-Codex-Window-Id", windowID)
	req.Header.Set("X-Client-Request-Id", clientRequestID)
	req.Header.Set("X-Codex-Turn-Metadata", turnMetadataString)
	req.Header.Set("Authorization", "Bearer "+ra.usedKey.ChannelKey)
}

func (ra *relayAttempt) normalizeCodexResponsesBody(req *http.Request, sessionID, threadID, turnID, windowID, turnMetadata string) {
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
	if _, ok := payload["tool_choice"]; !ok && hasResponsesTools(payload["tools"]) {
		payload["tool_choice"] = "auto"
	}

	metadata, _ := payload["client_metadata"].(map[string]any)
	if metadata == nil {
		metadata = map[string]any{}
		payload["client_metadata"] = metadata
	}
	setDefaultMetadata(metadata, "session_id", sessionID)
	setDefaultMetadata(metadata, "thread_id", threadID)
	setDefaultMetadata(metadata, "turn_id", turnID)
	setDefaultMetadata(metadata, "x-codex-installation-id", codexRelayInstallationID)
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
