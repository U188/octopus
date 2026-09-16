package relay

import (
	"bytes"
	"encoding/json"
	"net/http"
	"regexp"
	"strings"

	dbmodel "github.com/U188/octopus/internal/model"
)

const degradedSystemPrompt = "You are a helpful assistant. Respond in the user's language, follow the user's instructions, and be direct and concise."

var outboundFingerprintReplacements = [][2]string{
	{"You are Claude Code, Anthropic's official CLI for Claude", "You are Claude Code, Anthropic's official CLI tool for Claude"},
	{"Main branch (you will usually use this for PRs)", "Default branch (you will usually use this for PRs)"},
	{"You are a coding agent running in the Codex CLI, a terminal-based coding assistant.", "You are a coding agent running in the Codex CLI tool, a terminal-based coding assistant."},
	{"To give feedback, users should report the issue at https://github.com/anthropics/claude-code/issues", "To provide feedback, users should report the issue at https://github.com/anthropics/claude-code/issues"},
	{"11128", "11-128"},
}

var (
	outboundFingerprintHeader = regexp.MustCompile(`(?i)\bx-anthropic-billing-header:[^;\s]+;?`)
	outboundFingerprintKV     = regexp.MustCompile(`(?i)\bcc_[a-z0-9_]+=[^;\s]+;?`)
	outboundFingerprintCode   = regexp.MustCompile(`\b11128\b`)
)

func sanitizeOutboundPayload(body []byte) ([]byte, bool, error) {
	return rewriteOutboundPayload(body, true, "", "", true)
}

func sanitizeOutboundPayloadWithRules(body []byte, customRules string) ([]byte, bool, error) {
	return rewriteOutboundPayload(body, true, customRules, "", true)
}

func rewriteOutboundPayload(body []byte, sanitizeFingerprints bool, fingerprintRules, conversationRules string, rewriteTopLevelInput bool) ([]byte, bool, error) {
	var value any
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return nil, false, err
	}
	var systemRules []customFingerprintRule
	if sanitizeFingerprints {
		var err error
		systemRules, err = parseCustomFingerprintRules(fingerprintRules)
		if err != nil {
			return nil, false, err
		}
	}
	messageRules, err := parseConversationRewriteRules(conversationRules)
	if err != nil {
		return nil, false, err
	}
	changed := sanitizeOutboundMessages(value, sanitizeFingerprints, systemRules, messageRules, rewriteTopLevelInput)
	if !changed {
		return body, false, nil
	}
	out, err := json.Marshal(value)
	return out, true, err
}

type customFingerprintRule struct {
	scope       string
	match       string
	replacement string
}

func sanitizeOutboundMessages(value any, sanitizeFingerprints bool, systemRules, conversationRules []customFingerprintRule, rewriteTopLevelInput bool) bool {
	payload, ok := value.(map[string]any)
	if !ok {
		return false
	}
	changed := false
	if sanitizeFingerprints {
		for _, key := range []string{"system", "instructions", "systemInstruction"} {
			item, exists := payload[key]
			if exists && sanitizeOutboundValue(&item, "system", systemRules, true) {
				payload[key] = item
				changed = true
			}
		}
	}
	for _, key := range []string{"messages", "input", "contents"} {
		if text, ok := payload[key].(string); ok && key == "input" && rewriteTopLevelInput {
			item := any(text)
			if sanitizeOutboundValue(&item, "content", conversationRules, false) {
				payload[key] = item
				changed = true
			}
			continue
		}
		messages, _ := payload[key].([]any)
		for _, raw := range messages {
			message, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			if key == "input" {
				if blockType, _ := message["type"].(string); blockType == "reasoning" {
					item := any(message)
					if sanitizeOutboundValue(&item, "reasoning_content", conversationRules, false) {
						changed = true
					}
					continue
				}
			}
			role, _ := message["role"].(string)
			scope := ""
			switch role {
			case "system", "developer":
				scope = "system"
			case "user", "assistant", "model":
				scope = "content"
			}
			if scope == "" || (scope == "system" && !sanitizeFingerprints) {
				continue
			}
			for _, field := range []string{"content", "parts", "reasoning_content"} {
				fieldScope := scope
				if field == "reasoning_content" {
					if role != "assistant" && role != "model" {
						continue
					}
					if signature, _ := message["reasoning_signature"].(string); signature != "" {
						continue
					}
					fieldScope = "reasoning_content"
				}
				item, exists := message[field]
				rules := conversationRules
				applyBuiltins := false
				if fieldScope == "system" {
					rules = systemRules
					applyBuiltins = true
				}
				if exists && sanitizeOutboundValue(&item, fieldScope, rules, applyBuiltins) {
					message[field] = item
					changed = true
				}
			}
		}
	}
	return changed
}

func sanitizeOutboundValue(value *any, scope string, customRules []customFingerprintRule, applyBuiltins bool) bool {
	switch v := (*value).(type) {
	case string:
		original := v
		for _, replacement := range customRules {
			if replacement.scope != scope {
				continue
			}
			if replacement.match == "*" {
				v = replacement.replacement
			} else {
				v = strings.ReplaceAll(v, replacement.match, replacement.replacement)
			}
		}
		if !applyBuiltins {
			*value = v
			return original != v
		}
		if v == original && !strings.Contains(v, "You are Claude Code") &&
			!strings.Contains(v, "Main branch (") &&
			!strings.Contains(v, "You are a coding agent running in the Codex CLI") &&
			!strings.Contains(v, "https://github.com/anthropics/claude-code/issues") &&
			!outboundFingerprintCode.MatchString(v) && !outboundFingerprintHeader.MatchString(v) &&
			!outboundFingerprintKV.MatchString(v) {
			return false
		}
		for _, replacement := range outboundFingerprintReplacements {
			if replacement[0] == "11128" {
				v = outboundFingerprintCode.ReplaceAllString(v, replacement[1])
				continue
			}
			v = strings.ReplaceAll(v, replacement[0], replacement[1])
		}
		v = outboundFingerprintHeader.ReplaceAllString(v, "")
		v = outboundFingerprintKV.ReplaceAllString(v, "")
		v = strings.TrimSpace(v)
		*value = v
		return original != v
	case []any:
		changed := false
		for i := range v {
			item := any(v[i])
			if sanitizeOutboundValue(&item, scope, customRules, applyBuiltins) {
				v[i] = item
				changed = true
			}
		}
		return changed
	case map[string]any:
		if blockType, ok := v["type"].(string); ok {
			switch blockType {
			case "thinking":
				if signature, _ := v["signature"].(string); signature != "" {
					return false
				}
				return sanitizeOutboundMapField(v, "thinking", "reasoning_content", customRules)
			case "reasoning":
				return sanitizeOutboundMapField(v, "summary", "reasoning_content", customRules)
			case "summary_text":
				return sanitizeOutboundMapField(v, "text", "reasoning_content", customRules)
			case "text", "input_text", "output_text", "message":
			default:
				return false
			}
		}
		if thought, _ := v["thought"].(bool); thought {
			if signature, _ := v["thoughtSignature"].(string); signature != "" {
				return false
			}
			return sanitizeOutboundMapField(v, "text", "reasoning_content", customRules)
		}
		changed := false
		for _, key := range []string{"text", "content", "parts"} {
			item, exists := v[key]
			if exists && sanitizeOutboundValue(&item, scope, customRules, applyBuiltins) {
				v[key] = item
				changed = true
			}
		}
		return changed
	default:
		return false
	}
}

func sanitizeOutboundMapField(value map[string]any, key, scope string, rules []customFingerprintRule) bool {
	item, exists := value[key]
	if !exists || !sanitizeOutboundValue(&item, scope, rules, false) {
		return false
	}
	value[key] = item
	return true
}

func parseCustomFingerprintRules(raw string) ([]customFingerprintRule, error) {
	rules := make([]customFingerprintRule, 0)
	for _, rawLine := range strings.Split(raw, "\n") {
		line := strings.TrimSpace(rawLine)
		if line == "" {
			continue
		}
		match, replacement, err := dbmodel.ParseSystemPromptFingerprintRuleLine(line)
		if err != nil {
			return nil, err
		}
		rules = append(rules, customFingerprintRule{scope: "system", match: match, replacement: replacement})
	}
	return rules, nil
}

func parseConversationRewriteRules(raw string) ([]customFingerprintRule, error) {
	rules := make([]customFingerprintRule, 0)
	for _, rawLine := range strings.Split(raw, "\n") {
		line := strings.TrimSpace(rawLine)
		if line == "" {
			continue
		}
		scope, match, replacement, err := dbmodel.ParseConversationRewriteRuleLine(line)
		if err != nil {
			return nil, err
		}
		rules = append(rules, customFingerprintRule{scope: scope, match: match, replacement: replacement})
	}
	return rules, nil
}

func isSystemPromptContentBlocked(statusCode int, body []byte) bool {
	if statusCode != 400 {
		return false
	}
	message := string(body)
	if isSystemPromptContentBlockedDetails(statusCode, "", message) {
		return true
	}
	var value any
	if json.Unmarshal(body, &value) != nil {
		return false
	}
	return containsErrorCode11128(value)
}

func isSystemPromptContentBlockedDetails(status int, code, message string) bool {
	if strings.TrimSpace(code) == "11128" {
		return true
	}
	if status != http.StatusBadRequest {
		return false
	}
	message = strings.ToLower(message)
	for _, marker := range []string{"blocked by security policy", "unapproved channel", "illegal api invocation"} {
		if strings.Contains(message, marker) {
			return true
		}
	}
	return false
}

func containsErrorCode11128(value any) bool {
	switch v := value.(type) {
	case map[string]any:
		for key, child := range v {
			if strings.EqualFold(key, "code") {
				switch code := child.(type) {
				case string:
					if strings.TrimSpace(code) == "11128" {
						return true
					}
				case json.Number:
					if code.String() == "11128" {
						return true
					}
				case float64:
					if code == 11128 {
						return true
					}
				}
			}
			if containsErrorCode11128(child) {
				return true
			}
		}
	case []any:
		for _, child := range v {
			if containsErrorCode11128(child) {
				return true
			}
		}
	}
	return false
}
