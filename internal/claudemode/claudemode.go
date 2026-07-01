package claudemode

const UserAgent = "claude-cli/2.1.89 (external, sdk-cli)"

const Context1MBeta = "context-1m-2025-08-07"

const BaseAnthropicBeta = "claude-code-20250219,interleaved-thinking-2025-05-14,context-management-2025-06-27,prompt-caching-scope-2026-01-05"

func AnthropicBeta(context1M bool) string {
	if context1M {
		return BaseAnthropicBeta + "," + Context1MBeta
	}
	return BaseAnthropicBeta
}
