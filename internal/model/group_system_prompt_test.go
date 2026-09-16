package model

import (
	"errors"
	"strings"
	"testing"
)

func TestValidateSystemPromptConfig(t *testing.T) {
	tests := []struct {
		name    string
		mode    SystemPromptMode
		prompt  string
		wantErr bool
	}{
		{name: "off allows empty", mode: SystemPromptModeOff},
		{name: "append requires prompt", mode: SystemPromptModeAppend, wantErr: true},
		{name: "override accepts prompt", mode: SystemPromptModeOverride, prompt: "managed"},
		{name: "reject unknown mode", mode: "unknown", prompt: "managed", wantErr: true},
		{name: "reject oversized prompt", mode: SystemPromptModePrepend, prompt: strings.Repeat("a", SystemPromptMaxBytes+1), wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateSystemPromptConfig(tt.mode, tt.prompt)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ValidateSystemPromptConfig() error = %v, wantErr %v", err, tt.wantErr)
			}
			if err != nil && !errors.Is(err, ErrInvalidSystemPromptConfig) {
				t.Fatalf("error %v does not wrap ErrInvalidSystemPromptConfig", err)
			}
		})
	}
}

func TestValidateSystemPromptFingerprintRules(t *testing.T) {
	tests := []struct {
		name    string
		rules   string
		wantErr bool
	}{
		{name: "empty"},
		{name: "delete and replace", rules: "remove me\nold => new"},
		{name: "optional system scope", rules: "[system] old => new"},
		{name: "conversation scope rejected", rules: "[content] old => new", wantErr: true},
		{name: "empty match", rules: "=> replacement", wantErr: true},
		{name: "too many", rules: strings.Repeat("rule\n", SystemPromptFingerprintRulesMaxCount+1), wantErr: true},
		{name: "too large", rules: strings.Repeat("a", SystemPromptFingerprintRulesMaxBytes+1), wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := ValidateSystemPromptFingerprintRules(tt.rules); (err != nil) != tt.wantErr {
				t.Fatalf("ValidateSystemPromptFingerprintRules() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestValidateConversationRewriteRules(t *testing.T) {
	tests := []struct {
		name    string
		rules   string
		wantErr bool
	}{
		{name: "empty"},
		{name: "content and reasoning", rules: "[content] old => new\n[reasoning_content] * => weather"},
		{name: "scope required", rules: "old => new", wantErr: true},
		{name: "system rejected", rules: "[system] old => new", wantErr: true},
		{name: "empty match", rules: "[content] => replacement", wantErr: true},
		{name: "too many", rules: strings.Repeat("[content] rule\n", ConversationRewriteRulesMaxCount+1), wantErr: true},
		{name: "too large", rules: strings.Repeat("a", ConversationRewriteRulesMaxBytes+1), wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := ValidateConversationRewriteRules(tt.rules); (err != nil) != tt.wantErr {
				t.Fatalf("ValidateConversationRewriteRules() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
