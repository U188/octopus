package codexmode

import "github.com/U188/octopus/internal/model"

const UserAgent = "codex_cli_rs/0.144.5 (Windows 10.0.19044; x86_64) unknown (codex_cli_rs; 0.144.5)"

const LocalUserAgent = "codex_cli_rs/0.146.0 (Debian 12.0.0; x86_64) Konsole/221203 (codex_exec; 0.146.0)"

const Originator = "codex_cli_rs"

const BetaFeatures = "remote_compaction_v2"

const Sandbox = "none"

const ReasoningContext = "all_turns"

const ParallelToolCalls = false

const ResponsesLiteHeader = "X-OpenAI-Internal-Codex-Responses-Lite"

const ResponsesLiteHeaderValue = "true"

type Headers struct {
	UserAgent                string
	Originator               string
	BetaFeatures             string
	ResponsesLiteHeaderValue string
}

func HeadersForProfile(profile model.CodexHeaderProfile) Headers {
	userAgent := UserAgent
	if profile.Normalize() == model.CodexHeaderProfileLocal {
		userAgent = LocalUserAgent
	}
	return Headers{
		UserAgent:                userAgent,
		Originator:               Originator,
		BetaFeatures:             BetaFeatures,
		ResponsesLiteHeaderValue: ResponsesLiteHeaderValue,
	}
}
