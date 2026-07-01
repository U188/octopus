package claudemode

import (
	"strings"
	"testing"
)

func TestAnthropicBetaUsesExplicitContext1MFlag(t *testing.T) {
	if got := AnthropicBeta(false); containsBeta(got, Context1MBeta) {
		t.Fatalf("expected context-1m beta to be omitted by default, got %q", got)
	}
	if got := AnthropicBeta(true); !containsBeta(got, Context1MBeta) {
		t.Fatalf("expected context-1m beta when enabled, got %q", got)
	}
}

func containsBeta(header string, beta string) bool {
	for _, item := range strings.Split(header, ",") {
		if item == beta {
			return true
		}
	}
	return false
}
