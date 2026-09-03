package compat

import (
	"strings"

	"github.com/U188/octopus/internal/transformer/model"
)

// PatchOpenAIRequest applies both tool-call pairing repairs before OpenAI Chat
// Completions / Responses wire conversion. OpenAI is as strict as Anthropic
// here: an assistant `tool_calls` turn must be followed by one tool message per
// call ID, and a tool message must refer to a call that was actually made.
// Cross-protocol routing (Anthropic client → OpenAI upstream) and history
// truncation both produce violations the caller cannot see. OpenAI tolerates
// repeated roles and so has no alternation step, which lets both repairs run
// together here.
func PatchOpenAIRequest(req *model.InternalLLMRequest) {
	RepairToolResultBindings(req)
	RepairUnansweredToolCalls(req)
}

// RepairToolResultBindings rewrites tool messages that no upstream can bind
// into user messages. Run this BEFORE EnforceMessageAlternation: the rewrite
// changes a message's effective role, and only alternation can re-merge the
// same-role run it may create.
func RepairToolResultBindings(req *model.InternalLLMRequest) {
	if req == nil || len(req.Messages) == 0 {
		return
	}
	req.Messages = RepairToolResults(req.Messages)
}

// RepairUnansweredToolCalls appends a synthetic empty result for every assistant
// tool call left unanswered. Run this AFTER EnforceMessageAlternation so the
// synthetic result lands adjacent to its own call: the Anthropic and Gemini
// converters group a tool result with the user turn that follows it, which is
// the shape their APIs expect.
func RepairUnansweredToolCalls(req *model.InternalLLMRequest) {
	if req == nil || len(req.Messages) == 0 {
		return
	}
	req.Messages = FixOrphanedToolCalls(req.Messages)
}

// FixOrphanedToolCalls inserts empty tool_result messages for assistant
// tool_use blocks that are not answered before the next assistant turn.
func FixOrphanedToolCalls(messages []model.Message) []model.Message {
	if len(messages) == 0 {
		return messages
	}

	out := make([]model.Message, 0, len(messages))
	for i := 0; i < len(messages); i++ {
		msg := messages[i]
		out = append(out, msg)
		if msg.Role != "assistant" || len(msg.ToolCalls) == 0 {
			continue
		}

		answered := answeredToolCallIDsBeforeNextAssistant(messages, i+1)
		for _, toolCall := range msg.ToolCalls {
			id := strings.TrimSpace(toolCall.ID)
			if id == "" {
				continue
			}
			if _, ok := answered[id]; ok {
				continue
			}
			out = append(out, emptyToolResult(toolCall))
		}
	}
	return out
}

// RepairToolResults rewrites tool messages that no upstream can bind into user
// messages. Three cases qualify: the ToolCallID matches no preceding assistant
// tool call (the client truncated history and kept a result whose originating
// turn is gone), the message carries no ID at all, or a result for the same ID
// was already emitted (a duplicate binding). Converting to user text preserves
// the tool output as context instead of discarding it.
func RepairToolResults(messages []model.Message) []model.Message {
	available := make(map[string]int)
	out := make([]model.Message, 0, len(messages))
	repaired := false
	for _, msg := range messages {
		if msg.Role == "assistant" {
			for _, toolCall := range msg.ToolCalls {
				if id := strings.TrimSpace(toolCall.ID); id != "" {
					available[id]++
				}
			}
			out = append(out, msg)
			continue
		}
		if msg.Role != "tool" {
			out = append(out, msg)
			continue
		}
		id := toolCallIDOf(msg)
		if id != "" && available[id] > 0 {
			available[id]--
			out = append(out, msg)
			continue
		}
		out = append(out, toolResultAsUserMessage(msg))
		repaired = true
	}
	if !repaired {
		return messages
	}
	return out
}

func toolCallIDOf(msg model.Message) string {
	if msg.ToolCallID == nil {
		return ""
	}
	return strings.TrimSpace(*msg.ToolCallID)
}

// toolResultAsUserMessage converts an unbindable tool result into a user turn,
// labelling it with the tool name when one is known so the model can still tell
// what produced the text. The result is re-normalized because a tool result may
// legitimately carry empty content, which is valid for a tool message but not
// for a user one.
func toolResultAsUserMessage(msg model.Message) model.Message {
	converted := msg
	converted.Role = "user"
	converted.ToolCallID = nil
	converted.ToolCallName = nil
	converted.ToolCallIsError = nil
	converted.MessageIndex = nil

	label := ""
	if msg.ToolCallName != nil {
		label = strings.TrimSpace(*msg.ToolCallName)
	}
	if label != "" && converted.Content.Content != nil {
		text := "Tool result (" + label + "): " + *converted.Content.Content
		converted.Content = model.MessageContent{Content: &text}
	}
	converted.Normalize()
	return converted
}

func answeredToolCallIDsBeforeNextAssistant(messages []model.Message, start int) map[string]struct{} {
	answered := make(map[string]struct{})
	for i := start; i < len(messages); i++ {
		msg := messages[i]
		if msg.Role == "assistant" {
			break
		}
		if msg.Role != "tool" || msg.ToolCallID == nil {
			continue
		}
		id := strings.TrimSpace(*msg.ToolCallID)
		if id != "" {
			answered[id] = struct{}{}
		}
	}
	return answered
}

func emptyToolResult(toolCall model.ToolCall) model.Message {
	id := toolCall.ID
	name := toolCall.Function.Name
	content := ""
	return model.Message{
		Role:         "tool",
		Content:      model.MessageContent{Content: &content},
		ToolCallID:   &id,
		ToolCallName: &name,
	}
}
