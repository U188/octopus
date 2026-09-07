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
