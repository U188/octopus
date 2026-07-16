package codexmode

import "testing"

func TestCapturedCodexCLIHeaders(t *testing.T) {
	if UserAgent != "codex_cli_rs/0.144.5 (Windows 10.0.19044; x86_64) unknown (codex_cli_rs; 0.144.5)" {
		t.Fatalf("unexpected codex user agent: %q", UserAgent)
	}
	if Originator != "codex_cli_rs" {
		t.Fatalf("unexpected codex originator: %q", Originator)
	}
	if BetaFeatures != "remote_compaction_v2" {
		t.Fatalf("unexpected codex beta features: %q", BetaFeatures)
	}
	if Sandbox != "none" {
		t.Fatalf("unexpected codex sandbox: %q", Sandbox)
	}
	if ReasoningContext != "all_turns" || ParallelToolCalls {
		t.Fatalf("unexpected codex body defaults: context=%q parallel=%t", ReasoningContext, ParallelToolCalls)
	}
	if ResponsesLiteHeader != "X-OpenAI-Internal-Codex-Responses-Lite" || ResponsesLiteHeaderValue != "true" {
		t.Fatalf("unexpected codex responses lite header: %q=%q", ResponsesLiteHeader, ResponsesLiteHeaderValue)
	}
}
