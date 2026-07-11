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

	// Preserve the client's Claude Code identity when present. Rewriting the
	// session id / Accept / User-Agent is a major source of drift vs direct
	// Claude Code -> upstream requests and can break multi-turn continuity.
	existingSessionID := firstNonEmptyHeader(req.Header, "X-Claude-Code-Session-Id", "x-claude-code-session-id")
	sessionID := existingSessionID
	if sessionID == "" {
		sessionID = uuid.NewString()
	}
	clientAnthropicBeta := firstNonEmptyHeader(req.Header, "anthropic-beta", "Anthropic-Beta")
	clientAccept := firstNonEmptyHeader(req.Header, "Accept", "accept")
	clientUserAgent := firstNonEmptyHeader(req.Header, "User-Agent", "user-agent")
	clientApp := firstNonEmptyHeader(req.Header, "X-App", "x-app")
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

	// Keep Content-Type / Anthropic-Version defaults, but do not force
	// application/json Accept on streaming Claude Code clients.
	if clientAccept == "" {
		if ra.internalRequest != nil && ra.internalRequest.Stream != nil && *ra.internalRequest.Stream {
			clientAccept = "text/event-stream"
		} else {
			clientAccept = "application/json"
		}
	}
	if clientUserAgent == "" {
		clientUserAgent = claudeCodeUserAgent
	}
	if clientApp == "" {
		clientApp = "cli"
	}

	// Drop hop-by-hop / auth headers that must come from the channel key, then
	// set Claude Code defaults only for missing values so direct-client
	// requests stay byte-compatible where possible.
	req.Header.Del("Authorization")
	req.Header.Del("authorization")
	req.Header.Del("X-Api-Key")
	req.Header.Del("x-api-key")
	req.Header.Del("Api-Key")
	req.Header.Del("api-key")

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", clientAccept)
	if firstNonEmptyHeader(req.Header, "Anthropic-Version", "anthropic-version") == "" {
		req.Header.Set("Anthropic-Version", "2023-06-01")
	}
	// Prefer the client's beta list when present. For non-Claude clients that hit
	// a Claude mode channel without sending anthropic-beta themselves, synthesize
	// the Claude Code baseline and force the 1M-context beta: Claude-mode reseller
	// upstreams reject requests without it (HTTP 400), matching the test path.
	if strings.TrimSpace(clientAnthropicBeta) != "" {
		req.Header.Set("anthropic-beta", claudemode.MergeAnthropicBeta(context1M, clientAnthropicBeta))
	} else {
		req.Header.Set("anthropic-beta", claudemode.AnthropicBeta(true))
	}
	if firstNonEmptyHeader(req.Header, "Anthropic-Dangerous-Direct-Browser-Access") == "" {
		req.Header.Set("Anthropic-Dangerous-Direct-Browser-Access", "true")
	}
	req.Header.Set("User-Agent", clientUserAgent)
	req.Header.Set("X-App", clientApp)
	req.Header.Set("X-Claude-Code-Session-Id", sessionID)
	setHeaderIfMissing(req.Header, "X-Stainless-Retry-Count", "0")
	setHeaderIfMissing(req.Header, "X-Stainless-Timeout", "600")
	setHeaderIfMissing(req.Header, "X-Stainless-Lang", "js")
	setHeaderIfMissing(req.Header, "X-Stainless-Package-Version", claudemode.StainlessPackageVersion)
	setHeaderIfMissing(req.Header, "X-Stainless-OS", claudemode.StainlessOS())
	setHeaderIfMissing(req.Header, "X-Stainless-Arch", claudemode.StainlessArch())
	setHeaderIfMissing(req.Header, "X-Stainless-Runtime", claudemode.StainlessRuntime)
	setHeaderIfMissing(req.Header, "X-Stainless-Runtime-Version", claudemode.StainlessRuntimeVersion)
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

	changed := false
	if _, ok := payload["max_tokens"]; !ok {
		payload["max_tokens"] = float64(claudemode.DefaultMaxTokens)
		changed = true
	}
	if _, ok := payload["thinking"]; !ok {
		payload["thinking"] = map[string]any{
			"type":    "adaptive",
			"display": "omitted",
		}
		changed = true
	}
	if _, ok := payload["context_management"]; !ok {
		payload["context_management"] = map[string]any{
			"edits": []map[string]string{
				{"type": "clear_thinking_20251015", "keep": "all"},
			},
		}
		changed = true
	}
	if normalizeClaudeMetadata(payload, sessionID) {
		changed = true
	}
	if _, ok := payload["system"]; !ok {
		payload["system"] = []map[string]any{
			{"type": "text", "text": claudemode.BillingHeaderText},
			{"type": "text", "text": "You are a Claude agent, built on Anthropic's Claude Agent SDK.", "cache_control": map[string]string{"type": "ephemeral"}},
		}
		changed = true
	}
	if _, ok := payload["output_config"]; !ok {
		payload["output_config"] = map[string]any{"effort": "high"}
		changed = true
	}
	// A genuine Claude Code request always carries a tools array. Non-Claude
	// clients hitting a Claude mode channel send tool-less bodies, which
	// Claude-mode reseller upstreams reject with HTTP 503 "Service Unavailable".
	// Inject the canonical tool set only when absent so real Claude Code bodies
	// (which already carry tools) stay byte-identical and keep their cache
	// fingerprint.
	if !claudeBodyHasTools(payload["tools"]) {
		payload["tools"] = claudemode.Tools()
		changed = true
	}

	if _, ok := payload["thinking"]; ok {
		if _, has := payload["temperature"]; has {
			delete(payload, "temperature")
			changed = true
		}
		if _, has := payload["top_p"]; has {
			delete(payload, "top_p")
			changed = true
		}
		if _, has := payload["top_k"]; has {
			delete(payload, "top_k")
			changed = true
		}
	}

	// Avoid re-encoding a complete Claude Code body. Re-marshal reorders keys
	// and can change prompt-cache fingerprints relative to a direct client.
	if !changed {
		resetRequestBody(req, body)
		return modelName
	}

	normalized, err := json.Marshal(payload)
	if err != nil {
		resetRequestBody(req, body)
		return modelName
	}
	resetRequestBody(req, normalized)
	return modelName
}

func normalizeClaudeMetadata(payload map[string]any, sessionID string) bool {
	metadata, _ := payload["metadata"].(map[string]any)
	if metadata == nil {
		metadata = map[string]any{}
		payload["metadata"] = metadata
		metadata["user_id"] = claudeUserID(sessionID)
		return true
	}
	if _, ok := metadata["user_id"]; ok {
		return false
	}
	metadata["user_id"] = claudeUserID(sessionID)
	return true
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

// claudeBodyHasTools reports whether the request body already carries a
// non-empty tools array, in which case the client (a genuine Claude Code agent)
// must be left untouched.
func claudeBodyHasTools(value any) bool {
	switch tools := value.(type) {
	case []any:
		return len(tools) > 0
	case []map[string]any:
		return len(tools) > 0
	default:
		return false
	}
}

func firstNonEmptyHeader(header http.Header, keys ...string) string {
	if header == nil {
		return ""
	}
	for _, key := range keys {
		if value := strings.TrimSpace(header.Get(key)); value != "" {
			return value
		}
	}
	return ""
}

func setHeaderIfMissing(header http.Header, key string, value string) {
	if header == nil {
		return
	}
	if strings.TrimSpace(header.Get(key)) == "" {
		header.Set(key, value)
	}
}
