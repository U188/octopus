package codexmode

import "testing"

func TestCapturedCodexCLIHeaders(t *testing.T) {
	if UserAgent != "codex_sdk_go/0.144.3 (Debian 12.0.0; x86_64) Konsole/221203 (codex_exec; 0.144.3)" {
		t.Fatalf("unexpected codex user agent: %q", UserAgent)
	}
	if Originator != "codex_sdk_go" {
		t.Fatalf("unexpected codex originator: %q", Originator)
	}
	if BetaFeatures != "remote_compaction_v2" {
		t.Fatalf("unexpected codex beta features: %q", BetaFeatures)
	}
	if Sandbox != "seccomp" {
		t.Fatalf("unexpected codex sandbox: %q", Sandbox)
	}
	if ResponsesLiteHeader != "X-OpenAI-Internal-Codex-Responses-Lite" || ResponsesLiteHeaderValue != "true" {
		t.Fatalf("unexpected codex responses lite header: %q=%q", ResponsesLiteHeader, ResponsesLiteHeaderValue)
	}
}
