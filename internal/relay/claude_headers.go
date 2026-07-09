package relay

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/U188/octopus/internal/claudemode"
	"github.com/U188/octopus/internal/op"
	"github.com/U188/octopus/internal/transformer/outbound"
	"github.com/google/uuid"
)

const claudeCodeUserAgent = claudemode.UserAgent
const claudeCodeAnthropicBeta = claudemode.BaseAnthropicBeta

func (ra *relayAttempt) shouldUseClaudeAnthropicMode() bool {
	return ra != nil &&
		ra.channel != nil &&
		ra.channel.ClaudeMode &&
		ra.channel.Type == outbound.OutboundTypeAnthropic
}

func (ra *relayAttempt) applyClaudeAnthropicMode(req *http.Request) {
	if req == nil || !ra.shouldUseClaudeAnthropicMode() {
		return
	}

	clientAnthropicBeta := req.Header.Get("anthropic-beta")
	sessionID := uuid.NewString()
	modelName := ra.normalizeClaudeAnthropicBody(req, sessionID)
	if strings.TrimSpace(modelName) == "" {
		modelName = firstClaudeModelName(ra.requestModel, ra.channel.Model)
	}
	context1M, _ := op.SiteModelContext1MForChannelModel(ra.channel.ID, modelName, ra.requestContext())
	if req.URL != nil {
		query := req.URL.Query()
		query.Set("beta", "true")
		req.URL.RawQuery = query.Encode()
	}

	req.Header = http.Header{}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Anthropic-Version", "2023-06-01")
	req.Header.Set("anthropic-beta", claudemode.MergeAnthropicBeta(context1M, clientAnthropicBeta))
	req.Header.Set("Anthropic-Dangerous-Direct-Browser-Access", "true")
	req.Header.Set("User-Agent", claudeCodeUserAgent)
	req.Header.Set("X-App", "cli")
	req.Header.Set("X-Claude-Code-Session-Id", sessionID)
	req.Header.Set("X-Stainless-Retry-Count", "0")
	req.Header.Set("X-Stainless-Timeout", "600")
	req.Header.Set("X-Stainless-Lang", "js")
	req.Header.Set("X-Stainless-Package-Version", claudemode.StainlessPackageVersion)
	req.Header.Set("X-Stainless-OS", claudemode.StainlessOS())
	req.Header.Set("X-Stainless-Arch", claudemode.StainlessArch())
	req.Header.Set("X-Stainless-Runtime", claudemode.StainlessRuntime)
	req.Header.Set("X-Stainless-Runtime-Version", claudemode.StainlessRuntimeVersion)
	req.Header.Set("X-API-Key", ra.usedKey.ChannelKey)
}

func (ra *relayAttempt) normalizeClaudeAnthropicBody(req *http.Request, sessionID string) string {
	if req == nil || req.Body == nil {
		return ""
	}

	body, err := readOutboundRequestBody(req)
	if err != nil || len(body) == 0 {
		return ""
	}

	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		resetRequestBody(req, body)
		return ""
	}
	modelName := claudeJSONString(payload["model"])

	if _, ok := payload["max_tokens"]; !ok {
		payload["max_tokens"] = float64(claudemode.DefaultMaxTokens)
	}
	if _, ok := payload["thinking"]; !ok {
		payload["thinking"] = map[string]any{
			"type":    "adaptive",
			"display": "omitted",
		}
	}
	if _, ok := payload["context_management"]; !ok {
		payload["context_management"] = map[string]any{
			"edits": []map[string]string{
				{"type": "clear_thinking_20251015", "keep": "all"},
			},
		}
	}
	normalizeClaudeMetadata(payload, sessionID)
	if _, ok := payload["system"]; !ok {
		payload["system"] = []map[string]any{
			{"type": "text", "text": claudemode.BillingHeaderText},
			{"type": "text", "text": "You are a Claude agent, built on Anthropic's Claude Agent SDK.", "cache_control": map[string]string{"type": "ephemeral"}},
		}
	}
	if _, ok := payload["output_config"]; !ok {
		payload["output_config"] = map[string]any{"effort": "high"}
	}

	if _, ok := payload["thinking"]; ok {
		delete(payload, "temperature")
		delete(payload, "top_p")
		delete(payload, "top_k")
	}

	normalized, err := json.Marshal(payload)
	if err != nil {
		resetRequestBody(req, body)
		return modelName
	}
	resetRequestBody(req, normalized)
	return modelName
}

func normalizeClaudeMetadata(payload map[string]any, sessionID string) {
	metadata, _ := payload["metadata"].(map[string]any)
	if metadata == nil {
		metadata = map[string]any{}
		payload["metadata"] = metadata
	}
	if _, ok := metadata["user_id"]; ok {
		return
	}
	metadata["user_id"] = claudeUserID(sessionID)
}

func claudeUserID(sessionID string) string {
	hash := sha256.Sum256([]byte(sessionID))
	payload := map[string]string{
		"device_id":    hex.EncodeToString(hash[:]),
		"account_uuid": "",
		"session_id":   sessionID,
	}
	data, _ := json.Marshal(payload)
	return string(data)
}

func firstClaudeModelName(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func claudeJSONString(value any) string {
	if s, ok := value.(string); ok {
		return strings.TrimSpace(s)
	}
	return ""
}
