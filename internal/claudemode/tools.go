package claudemode

// canonicalToolNames mirrors the tool set a genuine Claude Code agent advertises
// on every request. Some upstream aggregators (notably reseller proxies) reject
// requests that do not look like a real agentic Claude Code call — a request that
// carries `thinking` but no `tools` array is answered with HTTP 503
// "Service Unavailable". Sending a representative tool list makes connectivity
// tests and synthesized probes indistinguishable from the real client.
//
// The schemas are intentionally minimal: empirically the upstream heuristic keys
// on the presence and count of tools, not their exact JSON Schema, so a compact
// list keeps the probe body small while still clearing the check.
var canonicalToolNames = []string{
	"Task", "Bash", "Glob", "Grep", "Read", "Edit", "Write",
	"NotebookEdit", "WebFetch", "WebSearch", "TodoWrite", "BashOutput",
	"KillShell", "ExitPlanMode", "Skill", "SlashCommand", "ListMcpResources",
	"ReadMcpResource", "Agent", "Monitor", "TaskCreate", "TaskUpdate",
	"SendMessage", "PushNotification",
}

// ToolNames returns the canonical Claude Code tool names.
func ToolNames() []string {
	out := make([]string, len(canonicalToolNames))
	copy(out, canonicalToolNames)
	return out
}

// Tools returns a Claude Code-shaped `tools` array suitable for embedding in a
// Messages API request body. Each entry is a valid Anthropic tool definition
// (name + description + object input schema).
func Tools() []map[string]any {
	tools := make([]map[string]any, 0, len(canonicalToolNames))
	for _, name := range canonicalToolNames {
		tools = append(tools, map[string]any{
			"name":        name,
			"description": "Claude Code " + name + " tool.",
			"input_schema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"input": map[string]any{
						"type":        "string",
						"description": "Tool input.",
					},
				},
				"required": []string{"input"},
			},
		})
	}
	return tools
}
