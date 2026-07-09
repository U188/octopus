package claudemode

import (
	"runtime"
	"strings"
)

const UserAgent = "claude-cli/2.1.205 (external, sdk-cli)"

const StainlessPackageVersion = "0.94.0"

const StainlessRuntime = "node"

const StainlessRuntimeVersion = "v26.3.0"

const DefaultMaxTokens = 64000

const BillingHeaderText = "x-anthropic-billing-header: cc_version=2.1.205.61a; cc_entrypoint=sdk-cli;"

const Context1MBeta = "context-1m-2025-08-07"

const BaseAnthropicBeta = "claude-code-20250219,interleaved-thinking-2025-05-14,thinking-token-count-2026-05-13,context-management-2025-06-27,prompt-caching-scope-2026-01-05,mid-conversation-system-2026-04-07,effort-2025-11-24,fallback-credit-2026-06-01"

func AnthropicBeta(context1M bool) string {
	return MergeAnthropicBeta(context1M)
}

func MergeAnthropicBeta(context1M bool, extraValues ...string) string {
	seen := make(map[string]struct{})
	values := make([]string, 0)
	add := func(value string) {
		for _, part := range strings.Split(value, ",") {
			item := strings.TrimSpace(part)
			if item == "" {
				continue
			}
			if _, ok := seen[item]; ok {
				continue
			}
			seen[item] = struct{}{}
			values = append(values, item)
		}
	}
	add(BaseAnthropicBeta)
	if context1M {
		add(Context1MBeta)
	}
	for _, extra := range extraValues {
		add(extra)
	}
	return strings.Join(values, ",")
}

func StainlessOS() string {
	switch runtime.GOOS {
	case "darwin":
		return "MacOS"
	case "windows":
		return "Windows"
	case "linux":
		return "Linux"
	default:
		return runtime.GOOS
	}
}

func StainlessArch() string {
	switch runtime.GOARCH {
	case "amd64":
		return "x64"
	case "386":
		return "x86"
	default:
		return runtime.GOARCH
	}
}
