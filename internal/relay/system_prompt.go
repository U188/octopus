package relay

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	dbmodel "github.com/U188/octopus/internal/model"
	"github.com/U188/octopus/internal/transformer/outbound"
)

func (ra *relayAttempt) finalizeOutboundRequest(req *http.Request) error {
	if err := ra.applySystemPrompt(req); err != nil {
		return fmt.Errorf("system prompt rewrite failed: %w", err)
	}
	if ra.metrics != nil {
		if body, err := readOutboundRequestBody(req); err == nil {
			modelName := ra.requestModel
			if ra.internalRequest != nil {
				modelName = ra.internalRequest.Model
			}
			ra.metrics.SetTransportRequestPayload(body, modelName)
		}
	}
	return nil
}

func (ra *relayAttempt) applySystemPrompt(req *http.Request) error {
	if req == nil || ra == nil || ra.relayRequest == nil {
		return nil
	}
	mode := ra.systemPromptMode
	if mode == "" {
		mode = dbmodel.SystemPromptModeOff
	}
	if mode == dbmodel.SystemPromptModeOff {
		return nil
	}
	if err := dbmodel.ValidateSystemPromptConfig(mode, ra.systemPrompt); err != nil {
		return err
	}
	body, err := readOutboundRequestBody(req)
	if err != nil {
		return err
	}
	if ra.channel == nil {
		return fmt.Errorf("missing channel")
	}
	rewritten, changed, err := rewriteSystemPromptBody(body, ra.channel.Type, ra.channel.CodexMode, mode, ra.systemPrompt)
	if err != nil {
		return err
	}
	if changed {
		resetRequestBody(req, rewritten)
	}
	return nil
}

func (ra *relayAttempt) rewriteSystemPromptBody(body []byte) ([]byte, error) {
	mode := ra.systemPromptMode
	if mode == "" {
		mode = dbmodel.SystemPromptModeOff
	}
	if mode == dbmodel.SystemPromptModeOff {
		return body, nil
	}
	if err := dbmodel.ValidateSystemPromptConfig(mode, ra.systemPrompt); err != nil {
		return nil, err
	}
	if ra.channel == nil {
		return nil, fmt.Errorf("missing channel")
	}
	rewritten, _, err := rewriteSystemPromptBody(body, ra.channel.Type, ra.channel.CodexMode, mode, ra.systemPrompt)
	return rewritten, err
}

func rewriteSystemPromptBody(body []byte, channelType outbound.OutboundType, codexMode bool, mode dbmodel.SystemPromptMode, prompt string) ([]byte, bool, error) {
	if channelType == outbound.OutboundTypeOpenAIEmbedding {
		return body, false, nil
	}
	var payload map[string]any
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	if err := decoder.Decode(&payload); err != nil {
		return nil, false, err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return nil, false, fmt.Errorf("request body must contain one JSON object")
	}

	var err error
	switch channelType {
	case outbound.OutboundTypeOpenAIChat:
		err = rewriteChatSystemPrompt(payload, mode, prompt)
	case outbound.OutboundTypeOpenAIResponse, outbound.OutboundTypeVolcengine:
		err = rewriteResponsesSystemPrompt(payload, codexMode, mode, prompt)
	case outbound.OutboundTypeAnthropic:
		err = rewriteAnthropicSystemPrompt(payload, mode, prompt)
	case outbound.OutboundTypeGemini:
		err = rewriteGeminiSystemPrompt(payload, mode, prompt)
	default:
		return body, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	rewritten, err := json.Marshal(payload)
	if err != nil {
		return nil, false, err
	}
	return rewritten, true, nil
}

func rewriteChatSystemPrompt(payload map[string]any, mode dbmodel.SystemPromptMode, prompt string) error {
	value, ok := payload["messages"]
	if !ok {
		return fmt.Errorf("chat request has no messages")
	}
	messages, ok := value.([]any)
	if !ok {
		return fmt.Errorf("chat messages must be an array")
	}
	customRole := "system"
	lastInstruction := -1
	for i, message := range messages {
		if !isInstructionMessage(message) {
			continue
		}
		lastInstruction = i
		if message.(map[string]any)["role"] == "developer" {
			customRole = "developer"
		}
	}
	custom := map[string]any{"role": customRole, "content": prompt}

	switch mode {
	case dbmodel.SystemPromptModePrepend:
		messages = insertJSONItem(messages, 0, custom)
	case dbmodel.SystemPromptModeAppend:
		messages = insertJSONItem(messages, lastInstruction+1, custom)
	case dbmodel.SystemPromptModeOverride:
		filtered := make([]any, 0, len(messages)+1)
		filtered = append(filtered, custom)
		for _, message := range messages {
			if !isInstructionMessage(message) {
				filtered = append(filtered, message)
			}
		}
		messages = filtered
	default:
		return fmt.Errorf("unsupported system prompt mode %q", mode)
	}
	payload["messages"] = messages
	return nil
}

func rewriteResponsesSystemPrompt(payload map[string]any, codexMode bool, mode dbmodel.SystemPromptMode, prompt string) error {
	instructions, hasInstructions, err := responseInstructions(payload)
	if err != nil {
		return err
	}
	input, inputIsArray := payload["input"].([]any)
	firstInstruction, lastInstruction := -1, -1
	if inputIsArray {
		for i, item := range input {
			if isResponsesInstructionItem(item) {
				if firstInstruction < 0 {
					firstInstruction = i
				}
				lastInstruction = i
			}
		}
	}
	custom := responsesDeveloperMessage(prompt)

	switch mode {
	case dbmodel.SystemPromptModePrepend:
		if hasInstructions {
			payload["instructions"] = joinPrompt(prompt, instructions)
		} else if firstInstruction >= 0 {
			payload["input"] = insertJSONItem(input, firstInstruction, custom)
		} else if codexMode {
			var err error
			input, err = responsesInputForInsertion(payload, input, inputIsArray)
			if err != nil {
				return err
			}
			payload["input"] = insertJSONItem(input, responsesStructuralPrefixEnd(input), custom)
		} else {
			payload["instructions"] = prompt
		}
	case dbmodel.SystemPromptModeAppend:
		if lastInstruction >= 0 {
			payload["input"] = insertJSONItem(input, lastInstruction+1, custom)
		} else if hasInstructions {
			payload["instructions"] = joinPrompt(instructions, prompt)
		} else if codexMode {
			var err error
			input, err = responsesInputForInsertion(payload, input, inputIsArray)
			if err != nil {
				return err
			}
			payload["input"] = insertJSONItem(input, responsesStructuralPrefixEnd(input), custom)
		} else {
			payload["instructions"] = prompt
		}
	case dbmodel.SystemPromptModeOverride:
		delete(payload, "instructions")
		if inputIsArray {
			filtered := make([]any, 0, len(input)+1)
			for _, item := range input {
				if !isResponsesInstructionItem(item) {
					filtered = append(filtered, item)
				}
			}
			input = filtered
		}
		if codexMode {
			var err error
			input, err = responsesInputForInsertion(payload, input, inputIsArray)
			if err != nil {
				return err
			}
			payload["input"] = insertJSONItem(input, responsesStructuralPrefixEnd(input), custom)
		} else {
			payload["instructions"] = prompt
			if inputIsArray {
				payload["input"] = input
			}
		}
	default:
		return fmt.Errorf("unsupported system prompt mode %q", mode)
	}
	return nil
}

func responsesInputForInsertion(payload map[string]any, input []any, inputIsArray bool) ([]any, error) {
	if inputIsArray {
		return input, nil
	}
	value, exists := payload["input"]
	if !exists || value == nil {
		return nil, nil
	}
	text, ok := value.(string)
	if !ok {
		return nil, fmt.Errorf("responses input must be a string or array")
	}
	return []any{
		map[string]any{
			"type": "message",
			"role": "user",
			"content": []any{
				map[string]any{"type": "input_text", "text": text},
			},
		},
	}, nil
}

func rewriteAnthropicSystemPrompt(payload map[string]any, mode dbmodel.SystemPromptMode, prompt string) error {
	if mode == dbmodel.SystemPromptModeOverride {
		payload["system"] = prompt
		return nil
	}
	value, exists := payload["system"]
	if !exists || value == nil {
		payload["system"] = prompt
		return nil
	}
	switch system := value.(type) {
	case string:
		if mode == dbmodel.SystemPromptModePrepend {
			payload["system"] = joinPrompt(prompt, system)
		} else {
			payload["system"] = joinPrompt(system, prompt)
		}
	case []any:
		part := map[string]any{"type": "text", "text": prompt}
		if mode == dbmodel.SystemPromptModePrepend {
			payload["system"] = insertJSONItem(system, 0, part)
		} else {
			payload["system"] = append(system, part)
		}
	default:
		return fmt.Errorf("anthropic system must be a string or array")
	}
	return nil
}

func rewriteGeminiSystemPrompt(payload map[string]any, mode dbmodel.SystemPromptMode, prompt string) error {
	part := map[string]any{"text": prompt}
	if mode == dbmodel.SystemPromptModeOverride {
		payload["systemInstruction"] = map[string]any{"parts": []any{part}}
		return nil
	}
	value, exists := payload["systemInstruction"]
	if !exists || value == nil {
		payload["systemInstruction"] = map[string]any{"parts": []any{part}}
		return nil
	}
	system, ok := value.(map[string]any)
	if !ok {
		return fmt.Errorf("gemini systemInstruction must be an object")
	}
	parts, ok := system["parts"].([]any)
	if !ok {
		return fmt.Errorf("gemini systemInstruction.parts must be an array")
	}
	if mode == dbmodel.SystemPromptModePrepend {
		system["parts"] = insertJSONItem(parts, 0, part)
	} else {
		system["parts"] = append(parts, part)
	}
	payload["systemInstruction"] = system
	return nil
}

func responseInstructions(payload map[string]any) (string, bool, error) {
	value, ok := payload["instructions"]
	if !ok || value == nil {
		return "", false, nil
	}
	instructions, ok := value.(string)
	if !ok {
		return "", false, fmt.Errorf("responses instructions must be a string")
	}
	return instructions, true, nil
}

func isInstructionMessage(value any) bool {
	message, ok := value.(map[string]any)
	if !ok {
		return false
	}
	role, _ := message["role"].(string)
	return role == "system" || role == "developer"
}

func isResponsesInstructionItem(value any) bool {
	item, ok := value.(map[string]any)
	if !ok {
		return false
	}
	itemType, _ := item["type"].(string)
	if itemType != "" && itemType != "message" {
		return false
	}
	return isInstructionMessage(item)
}

func responsesStructuralPrefixEnd(input []any) int {
	index := 0
	for index < len(input) {
		item, ok := input[index].(map[string]any)
		if !ok || item["type"] != "additional_tools" {
			break
		}
		index++
	}
	return index
}

func responsesDeveloperMessage(prompt string) map[string]any {
	return map[string]any{
		"type": "message",
		"role": "developer",
		"content": []any{
			map[string]any{"type": "input_text", "text": prompt},
		},
	}
}

func insertJSONItem(items []any, index int, value any) []any {
	if index < 0 {
		index = 0
	}
	if index > len(items) {
		index = len(items)
	}
	items = append(items, nil)
	copy(items[index+1:], items[index:])
	items[index] = value
	return items
}

func joinPrompt(first, second string) string {
	if first == "" {
		return second
	}
	if second == "" {
		return first
	}
	return strings.TrimRight(first, "\n") + "\n\n" + strings.TrimLeft(second, "\n")
}
