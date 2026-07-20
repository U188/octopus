package volcengine

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/U188/octopus/internal/transformer/model"
	"github.com/U188/octopus/internal/transformer/outbound/openai"
)

var supportedReasoningEffortModel = map[string]bool{
	"doubao-seed-1-8-251228":      true,
	"doubao-seed-1-6-lite-251015": true,
	"doubao-seed-1-6-251015":      true,
}

type ResponseOutbound struct {
	inner openai.ResponseOutbound
}

func (o *ResponseOutbound) TransformRequest(ctx context.Context, request *model.InternalLLMRequest, baseUrl, key string) (*http.Request, error) {
	if request == nil {
		return nil, fmt.Errorf("request is nil")
	}

	request.NormalizeMessages()

	// Convert to Responses API request format
	openaiReq := openai.ConvertToResponsesRequest(request)
	openaiReq.Metadata = nil // volcengine not supported
	if _, ok := supportedReasoningEffortModel[request.Model]; !ok {
		openaiReq.Reasoning = nil
	}
	responsesReq := ResponsesRequest{
		ResponsesRequest: openaiReq,
		Input:            convertToResponsesInput(openaiReq.Input),
	}
	switch request.ReasoningEffort {
	case "minimal":
		responsesReq.Thinking.Type = ThinkingTypeDisabled
	case "low", "medium", "high":
		responsesReq.Thinking.Type = ThinkingTypeEnabled
	default:
	}

	body, err := json.Marshal(responsesReq)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal responses api request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	// Set headers
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+key)

	// Parse and set URL
	parsedUrl, err := url.Parse(strings.TrimSuffix(baseUrl, "/"))
	if err != nil {
		return nil, fmt.Errorf("failed to parse base url: %w", err)
	}
	parsedUrl.Path = parsedUrl.Path + "/responses"
	req.URL = parsedUrl
	req.Method = http.MethodPost

	return req, nil

}
func (o *ResponseOutbound) TransformResponse(ctx context.Context, response *http.Response) (*model.InternalLLMResponse, error) {
	return o.inner.TransformResponse(ctx, response)
}

func (o *ResponseOutbound) TransformStream(ctx context.Context, eventData []byte) (*model.InternalLLMResponse, error) {
	return o.inner.TransformStream(ctx, eventData)
}

type ResponsesRequest struct {
	*openai.ResponsesRequest
	Input    ResponsesInput `json:"input"`
	Thinking Thinking       `json:"thinking,omitzero"`
}

type ThinkingType string

const (
	ThinkingTypeAuto     ThinkingType = "auto"
	ThinkingTypeDisabled ThinkingType = "disabled"
	ThinkingTypeEnabled  ThinkingType = "enabled"
)

type Thinking struct {
	Type ThinkingType `json:"type"`
}

type ResponsesInput struct {
	Text  *string
	Items []ResponsesItem
	Raw   json.RawMessage
}

func (i ResponsesInput) MarshalJSON() ([]byte, error) {
	if len(i.Raw) > 0 {
		return json.Marshal(json.RawMessage(i.Raw))
	}
	if i.Text != nil {
		return json.Marshal(i.Text)
	}
	return json.Marshal(i.Items)
}

func (i *ResponsesInput) UnmarshalJSON(data []byte) error {
	var text string
	if err := json.Unmarshal(data, &text); err == nil {
		i.Text = &text
		return nil
	}
	var items []ResponsesItem
	if err := json.Unmarshal(data, &items); err == nil {
		i.Items = items
		return nil
	}
	return fmt.Errorf("invalid input format")
}

type ResponsesItem struct {
	openai.ResponsesItem
	Partial bool `json:"partial,omitempty"`
}

func convertToResponsesInput(input openai.ResponsesInput) ResponsesInput {
	result := ResponsesInput{}
	if input.Text != nil {
		result.Text = input.Text
		return result
	}
	// Raw is the authoritative carrier for Responses requests (see
	// openai.buildResponsesInput); pass it through instead of dropping it.
	if len(input.Raw) > 0 {
		result.Raw = markLastAssistantPartialRaw(input.Raw)
		return result
	}

	for _, item := range input.Items {
		result.Items = append(result.Items, ResponsesItem{ResponsesItem: item})
	}
	// If the role of the last message is the assistant, needs set partial.
	if last := len(result.Items) - 1; last >= 0 && result.Items[last].Role == "assistant" {
		result.Items[last].Partial = true
	}
	return result
}

// markLastAssistantPartialRaw sets "partial": true on the trailing assistant
// item of a raw input array, rewriting only that element so the rest of the
// payload stays byte-identical. Non-array or unparsable raw is returned as is.
func markLastAssistantPartialRaw(raw json.RawMessage) json.RawMessage {
	var elems []json.RawMessage
	if err := json.Unmarshal(raw, &elems); err != nil || len(elems) == 0 {
		return raw
	}
	var last map[string]json.RawMessage
	if err := json.Unmarshal(elems[len(elems)-1], &last); err != nil {
		return raw
	}
	var role string
	if roleRaw, ok := last["role"]; ok {
		_ = json.Unmarshal(roleRaw, &role)
	}
	if role != "assistant" {
		return raw
	}
	last["partial"] = json.RawMessage("true")
	patched, err := json.Marshal(last)
	if err != nil {
		return raw
	}
	elems[len(elems)-1] = patched
	out, err := json.Marshal(elems)
	if err != nil {
		return raw
	}
	return out
}
