package helper

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// ApplyParamOverride merges a JSON-object override into an outbound JSON request body.
// Empty overrides, nil bodies, and non-object request bodies are ignored.
func ApplyParamOverride(request *http.Request, paramOverride *string) error {
	if request == nil || request.Body == nil || paramOverride == nil || strings.TrimSpace(*paramOverride) == "" {
		return nil
	}

	body, err := io.ReadAll(request.Body)
	if err != nil {
		return fmt.Errorf("failed to read request body: %w", err)
	}

	restoreBody := func() {
		request.Body = io.NopCloser(bytes.NewReader(body))
		request.ContentLength = int64(len(body))
		request.GetBody = func() (io.ReadCloser, error) {
			return io.NopCloser(bytes.NewReader(body)), nil
		}
	}

	var bodyMap map[string]any
	if err := json.Unmarshal(body, &bodyMap); err != nil {
		restoreBody()
		return nil
	}

	var override map[string]any
	if err := json.Unmarshal([]byte(*paramOverride), &override); err != nil {
		restoreBody()
		return nil
	}

	for key, value := range override {
		bodyMap[key] = value
	}

	modifiedBody, err := json.Marshal(bodyMap)
	if err != nil {
		return fmt.Errorf("failed to marshal request body with param override: %w", err)
	}

	request.Body = io.NopCloser(bytes.NewReader(modifiedBody))
	request.ContentLength = int64(len(modifiedBody))
	request.GetBody = func() (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(modifiedBody)), nil
	}
	return nil
}

// ApplyResponsesToolDenylist removes unsupported OpenAI Responses tools from a
// JSON request body before it is sent to a channel with narrower capabilities.
func ApplyResponsesToolDenylist(request *http.Request, toolDenylist []string) error {
	_, _, err := ApplyResponsesToolDenylistWithReport(request, toolDenylist)
	return err
}

func ApplyResponsesToolDenylistWithReport(request *http.Request, toolDenylist []string) ([]string, bool, error) {
	if request == nil || request.Body == nil || len(toolDenylist) == 0 {
		return nil, false, nil
	}
	deny := make(map[string]struct{}, len(toolDenylist))
	for _, item := range toolDenylist {
		value := strings.ToLower(strings.TrimSpace(item))
		if value != "" {
			deny[value] = struct{}{}
		}
	}
	if len(deny) == 0 {
		return nil, false, nil
	}

	body, err := io.ReadAll(request.Body)
	if err != nil {
		return nil, false, fmt.Errorf("failed to read request body: %w", err)
	}
	restoreBody := func(next []byte) {
		request.Body = io.NopCloser(bytes.NewReader(next))
		request.ContentLength = int64(len(next))
		request.GetBody = func() (io.ReadCloser, error) {
			return io.NopCloser(bytes.NewReader(next)), nil
		}
	}

	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		restoreBody(body)
		return nil, false, nil
	}
	rawTools, ok := payload["tools"].([]any)
	if !ok || len(rawTools) == 0 {
		restoreBody(body)
		return nil, false, nil
	}

	filtered := make([]any, 0, len(rawTools))
	removedTools := make([]string, 0)
	removedSeen := make(map[string]struct{})
	for _, rawTool := range rawTools {
		toolMap, ok := rawTool.(map[string]any)
		if !ok {
			filtered = append(filtered, rawTool)
			continue
		}
		toolType, _ := toolMap["type"].(string)
		normalizedTool := strings.ToLower(strings.TrimSpace(toolType))
		if _, denyTool := deny[normalizedTool]; denyTool {
			if _, seen := removedSeen[normalizedTool]; !seen {
				removedSeen[normalizedTool] = struct{}{}
				removedTools = append(removedTools, normalizedTool)
			}
			continue
		}
		filtered = append(filtered, rawTool)
	}
	if len(removedTools) == 0 {
		restoreBody(body)
		return nil, false, nil
	}

	toolChoiceRemoved := false
	if len(filtered) == 0 {
		delete(payload, "tools")
		delete(payload, "tool_choice")
		toolChoiceRemoved = true
	} else {
		payload["tools"] = filtered
		if choice, ok := payload["tool_choice"].(map[string]any); ok {
			choiceType, _ := choice["type"].(string)
			if _, denyChoice := deny[strings.ToLower(strings.TrimSpace(choiceType))]; denyChoice {
				delete(payload, "tool_choice")
				toolChoiceRemoved = true
			}
		}
	}

	modifiedBody, err := json.Marshal(payload)
	if err != nil {
		return nil, false, fmt.Errorf("failed to marshal request body with responses tool denylist: %w", err)
	}
	restoreBody(modifiedBody)
	return removedTools, toolChoiceRemoved, nil
}
