package relay

import (
	"bytes"
	"encoding/json"
	"net/http"
	"regexp"
	"strings"
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
	var value any
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return nil, false, err
	}
	changed := sanitizeOutboundValue(&value, false)
	if !changed {
		return body, false, nil
	}
	out, err := json.Marshal(value)
	return out, true, err
}

func sanitizeOutboundValue(value *any, promptContext bool) bool {
	switch v := (*value).(type) {
	case string:
		if !promptContext {
			return false
		}
		original := v
		if !strings.Contains(v, "You are Claude Code") &&
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
			if sanitizeOutboundValue(&item, promptContext) {
				v[i] = item
				changed = true
			}
		}
		return changed
	case map[string]any:
		changed := false
		role, _ := v["role"].(string)
		instructionMessage := role == "system" || role == "developer"
		for key, item := range v {
			childContext := key == "system" || key == "instructions" || key == "systemInstruction" ||
				(instructionMessage && key == "content") ||
				(promptContext && (key == "text" || key == "content" || key == "parts"))
			if sanitizeOutboundValue(&item, childContext) {
				v[key] = item
				changed = true
			}
		}
		return changed
	default:
		return false
	}
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
