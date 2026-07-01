package relay

import "testing"

func TestSupportedModelAllowedTrimsCSVItems(t *testing.T) {
	if !supportedModelAllowed("gpt-4, claude-3 ,  ds-chat", "claude-3") {
		t.Fatalf("expected model with surrounding spaces in CSV to be allowed")
	}
}

func TestSupportedModelAllowedEmptyListAllowsAll(t *testing.T) {
	if !supportedModelAllowed("  ", "any-model") {
		t.Fatalf("expected empty supported model list to allow all models")
	}
}

func TestSupportedModelAllowedRejectsMissingModel(t *testing.T) {
	if supportedModelAllowed("gpt-4,claude-3", "gemini") {
		t.Fatalf("expected missing model to be rejected")
	}
}
