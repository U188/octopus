package relay

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/U188/octopus/internal/claudemode"
	"github.com/U188/octopus/internal/op"
	transformerModel "github.com/U188/octopus/internal/transformer/model"
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

	synthesized := !ra.isNativeClaudeCodeRequest(req)
	// Preserve the client's Claude Code identity when present. Rewriting the
	// session id / Accept / User-Agent is a major source of drift vs direct
	// Claude Code -> upstream requests and can break multi-turn continuity.
	existingSessionID := firstNonEmptyHeader(req.Header, "X-Claude-Code-Session-Id", "x-claude-code-session-id")
	if synthesized {
		existingSessionID = ""
	}
	sessionID := existingSessionID
	if sessionID == "" {
		sessionID = uuid.NewString()
	}
	clientAnthropicBeta := firstNonEmptyHeader(req.Header, "anthropic-beta", "Anthropic-Beta")
	clientAccept := firstNonEmptyHeader(req.Header, "Accept", "accept")
	clientUserAgent := firstNonEmptyHeader(req.Header, "User-Agent", "user-agent")
	clientApp := firstNonEmptyHeader(req.Header, "X-App", "x-app")
	if synthesized {
		clientAccept = ""
		clientUserAgent = ""
		clientApp = ""
	}
	modelName := ra.normalizeClaudeAnthropicBody(req, sessionID, synthesized)
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
	if synthesized {
		removeSynthesizedClientIdentityHeaders(req.Header)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", clientAccept)
	if synthesized || firstNonEmptyHeader(req.Header, "Anthropic-Version", "anthropic-version") == "" {
		req.Header.Set("Anthropic-Version", "2023-06-01")
	}
	// Prefer the client's beta list when present. For non-Claude clients that hit
	// a Claude mode channel without sending anthropic-beta themselves, synthesize
	// the Claude Code baseline and force the 1M-context beta: Claude-mode reseller
	// upstreams reject requests without it (HTTP 400), matching the test path.
	if synthesized {
		req.Header.Set("anthropic-beta", claudemode.MergeAnthropicBeta(true, clientAnthropicBeta))
	} else if strings.TrimSpace(clientAnthropicBeta) != "" {
		req.Header.Set("anthropic-beta", claudemode.MergeAnthropicBeta(context1M, clientAnthropicBeta))
	} else {
		req.Header.Set("anthropic-beta", claudemode.AnthropicBeta(true))
	}
	if synthesized || firstNonEmptyHeader(req.Header, "Anthropic-Dangerous-Direct-Browser-Access") == "" {
		req.Header.Set("Anthropic-Dangerous-Direct-Browser-Access", "true")
	}
	req.Header.Set("User-Agent", clientUserAgent)
	req.Header.Set("X-App", clientApp)
	req.Header.Set("X-Claude-Code-Session-Id", sessionID)
	setClaudeStainlessHeader(req.Header, "X-Stainless-Retry-Count", "0", synthesized)
	setClaudeStainlessHeader(req.Header, "X-Stainless-Timeout", "600", synthesized)
	setClaudeStainlessHeader(req.Header, "X-Stainless-Lang", "js", synthesized)
	setClaudeStainlessHeader(req.Header, "X-Stainless-Package-Version", claudemode.StainlessPackageVersion, synthesized)
	setClaudeStainlessHeader(req.Header, "X-Stainless-OS", claudemode.StainlessOS(), synthesized)
	setClaudeStainlessHeader(req.Header, "X-Stainless-Arch", claudemode.StainlessArch(), synthesized)
	setClaudeStainlessHeader(req.Header, "X-Stainless-Runtime", claudemode.StainlessRuntime, synthesized)
	setClaudeStainlessHeader(req.Header, "X-Stainless-Runtime-Version", claudemode.StainlessRuntimeVersion, synthesized)
	req.Header.Set("X-API-Key", ra.usedKey.ChannelKey)
}

func (ra *relayAttempt) isNativeClaudeCodeRequest(req *http.Request) bool {
	if ra == nil || ra.internalRequest == nil || req == nil {
		return false
	}
	if ra.internalRequest.RawAPIFormat != transformerModel.APIFormatAnthropicMessage {
		return false
	}
	userAgent := strings.ToLower(firstNonEmptyHeader(req.Header, "User-Agent", "user-agent"))
	return strings.HasPrefix(userAgent, "claude-cli/")
}

func (ra *relayAttempt) normalizeClaudeAnthropicBody(req *http.Request, sessionID string, synthesized bool) string {
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
	if normalizeClaudeMetadata(payload, sessionID, synthesized) {
		changed = true
	}
	if synthesized {
		if normalizeSynthesizedClaudeSystem(payload) {
			changed = true
		}
	} else if _, ok := payload["system"]; !ok {
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
	hadClientTools := claudeBodyHasTools(payload["tools"])
	// A genuine Claude Code request carries the canonical tool set. Cross-protocol
	// clients may already have their own tools, so merge both sets; native
	// Anthropic bodies are only completed when tools are absent, preserving a
	// complete Claude Code body's byte-level cache fingerprint.
	if synthesized {
		if normalizeSynthesizedClaudeTools(payload) {
			changed = true
		}
	} else if !claudeBodyHasTools(payload["tools"]) {
		payload["tools"] = claudemode.Tools()
		changed = true
	}
	// Canonical tools are compatibility markers for upstream agentic gating.
	// Do not let the model call them when the original client declared no tools:
	// the client cannot execute those synthetic calls and would never receive a
	// normal text answer. Real client tools keep their original tool choice.
	if synthesized && !hadClientTools {
		if toolChoice, ok := payload["tool_choice"].(map[string]any); !ok || claudeJSONString(toolChoice["type"]) != "none" {
			payload["tool_choice"] = map[string]any{"type": "none"}
			changed = true
		}
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

func normalizeClaudeMetadata(payload map[string]any, sessionID string, forceUserID bool) bool {
	metadata, _ := payload["metadata"].(map[string]any)
	if metadata == nil {
		metadata = map[string]any{}
		payload["metadata"] = metadata
		metadata["user_id"] = claudeUserID(sessionID)
		return true
	}
	if _, ok := metadata["user_id"]; ok && !forceUserID {
		return false
	}
	userID := claudeUserID(sessionID)
	if current, ok := metadata["user_id"].(string); ok && current == userID {
		return false
	}
	metadata["user_id"] = userID
	return true
}

func normalizeSynthesizedClaudeSystem(payload map[string]any) bool {
	const agentPrompt = "You are a Claude agent, built on Anthropic's Claude Agent SDK."

	var existing []any
	switch system := payload["system"].(type) {
	case []any:
		existing = system
	case string:
		if strings.TrimSpace(system) != "" {
			existing = []any{map[string]any{"type": "text", "text": system}}
		}
	case nil:
	default:
		existing = []any{system}
	}

	hasBilling := claudeSystemContainsText(existing, claudemode.BillingHeaderText)
	hasAgent := claudeSystemContainsText(existing, agentPrompt)
	if hasBilling && hasAgent {
		return false
	}

	normalized := make([]any, 0, len(existing)+2)
	if !hasBilling {
		normalized = append(normalized, map[string]any{
			"type": "text",
			"text": claudemode.BillingHeaderText,
		})
	}
	if !hasAgent {
		normalized = append(normalized, map[string]any{
			"type":          "text",
			"text":          agentPrompt,
			"cache_control": map[string]string{"type": "ephemeral"},
		})
	}
	normalized = append(normalized, existing...)
	payload["system"] = normalized
	return true
}

func claudeSystemContainsText(system []any, target string) bool {
	for _, item := range system {
		block, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if text, ok := block["text"].(string); ok && strings.TrimSpace(text) == target {
			return true
		}
	}
	return false
}

func normalizeSynthesizedClaudeTools(payload map[string]any) bool {
	existing, _ := payload["tools"].([]any)
	canonical := claudemode.Tools()
	if claudeToolsContainCanonicalSet(existing, canonical) {
		return false
	}

	byName := make(map[string]any, len(existing))
	for _, tool := range existing {
		if name := claudeToolName(tool); name != "" {
			byName[name] = tool
		}
	}

	merged := make([]any, 0, len(canonical)+len(existing))
	used := make(map[string]struct{}, len(canonical))
	for _, tool := range canonical {
		name := claudeToolName(tool)
		if clientTool, ok := byName[name]; ok {
			merged = append(merged, clientTool)
		} else {
			merged = append(merged, tool)
		}
		used[name] = struct{}{}
	}
	for _, tool := range existing {
		name := claudeToolName(tool)
		if _, ok := used[name]; name != "" && ok {
			continue
		}
		merged = append(merged, tool)
	}
	payload["tools"] = merged
	return true
}

func claudeToolsContainCanonicalSet(existing []any, canonical []map[string]any) bool {
	names := make(map[string]struct{}, len(existing))
	for _, tool := range existing {
		if name := claudeToolName(tool); name != "" {
			names[name] = struct{}{}
		}
	}
	for _, tool := range canonical {
		if _, ok := names[claudeToolName(tool)]; !ok {
			return false
		}
	}
	return true
}

func claudeToolName(tool any) string {
	switch value := tool.(type) {
	case map[string]any:
		name, _ := value["name"].(string)
		return strings.TrimSpace(name)
	default:
		return ""
	}
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

func setClaudeStainlessHeader(header http.Header, key string, value string, force bool) {
	if force {
		header.Set(key, value)
		return
	}
	setHeaderIfMissing(header, key, value)
}

func removeSynthesizedClientIdentityHeaders(header http.Header) {
	for _, key := range []string{
		"Originator",
		"Session-Id",
		"Thread-Id",
		"X-Client-Request-Id",
		"X-Codex-Beta-Features",
		"X-Codex-Turn-Metadata",
		"X-Codex-Window-Id",
	} {
		header.Del(key)
	}
}
