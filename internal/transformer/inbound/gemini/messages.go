package gemini

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/U188/octopus/internal/transformer/model"
)

type contextKey string

const (
	contextKeyModel  contextKey = "gemini_model"
	contextKeyStream contextKey = "gemini_stream"
)

type MessagesInbound struct {
	streamAggregator model.StreamAggregator
	storedResponse   *model.InternalLLMResponse
}

func WithRequestInfo(ctx context.Context, modelName string, stream bool) context.Context {
	ctx = context.WithValue(ctx, contextKeyModel, modelName)
	return context.WithValue(ctx, contextKeyStream, stream)
}

func (i *MessagesInbound) TransformRequest(ctx context.Context, body []byte) (*model.InternalLLMRequest, error) {
	var req model.GeminiGenerateContentRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return nil, err
	}

	modelName, _ := ctx.Value(contextKeyModel).(string)
	stream, _ := ctx.Value(contextKeyStream).(bool)
	out := &model.InternalLLMRequest{
		Model:               strings.TrimSpace(modelName),
		Stream:              boolPtr(stream),
		RawAPIFormat:        model.APIFormatGeminiContents,
		TransformerMetadata: map[string]string{},
	}

	if req.SystemInstruction != nil {
		text := geminiContentText(req.SystemInstruction)
		if text != "" {
			out.Messages = append(out.Messages, model.Message{
				Role:    "system",
				Content: model.MessageContent{Content: &text},
			})
		}
	}
	for _, content := range req.Contents {
		msg := geminiContentToMessage(content)
		if msg.Role == "" {
			continue
		}
		out.Messages = append(out.Messages, msg)
	}
	if req.GenerationConfig != nil {
		out.Temperature = req.GenerationConfig.Temperature
		out.TopP = req.GenerationConfig.TopP
		if req.GenerationConfig.MaxOutputTokens > 0 {
			maxTokens := int64(req.GenerationConfig.MaxOutputTokens)
			out.MaxTokens = &maxTokens
		}
		if len(req.GenerationConfig.StopSequences) > 0 {
			out.Stop = &model.Stop{MultipleStop: req.GenerationConfig.StopSequences}
		}
	}
	for _, tool := range req.Tools {
		if tool == nil {
			continue
		}
		for _, decl := range tool.FunctionDeclarations {
			if decl == nil || strings.TrimSpace(decl.Name) == "" {
				continue
			}
			params, _ := json.Marshal(decl.Parameters)
			out.Tools = append(out.Tools, model.Tool{
				Type: "function",
				Function: model.Function{
					Name:        decl.Name,
					Description: decl.Description,
					Parameters:  params,
				},
			})
		}
	}
	if req.ToolConfig != nil && req.ToolConfig.FunctionCallingConfig != nil {
		mode := strings.ToUpper(strings.TrimSpace(req.ToolConfig.FunctionCallingConfig.Mode))
		switch mode {
		case "NONE":
			choice := "none"
			out.ToolChoice = &model.ToolChoice{ToolChoice: &choice}
		case "ANY":
			if names := req.ToolConfig.FunctionCallingConfig.AllowedFunctionNames; len(names) == 1 {
				out.ToolChoice = &model.ToolChoice{NamedToolChoice: &model.NamedToolChoice{Type: "function", Name: &names[0]}}
			} else {
				choice := "required"
				out.ToolChoice = &model.ToolChoice{ToolChoice: &choice}
			}
		}
	}
	if out.Model == "" {
		return nil, fmt.Errorf("missing gemini model in request path")
	}
	return out, nil
}

func (i *MessagesInbound) TransformResponse(ctx context.Context, response *model.InternalLLMResponse) ([]byte, error) {
	i.storedResponse = response
	body, err := json.Marshal(internalToGeminiResponse(response, false))
	if err != nil {
		return nil, err
	}
	return body, nil
}

func (i *MessagesInbound) TransformStream(ctx context.Context, stream *model.InternalLLMResponse) ([]byte, error) {
	if stream == nil || stream.Object == "[DONE]" {
		return nil, nil
	}
	i.streamAggregator.Add(stream)
	body, err := json.Marshal(internalToGeminiResponse(stream, true))
	if err != nil {
		return nil, err
	}
	return []byte("data: " + string(body) + "\n\n"), nil
}

func (i *MessagesInbound) TransformStreamEvents(ctx context.Context, events []model.StreamEvent) ([]byte, error) {
	resp := model.InternalResponseFromStreamEvents(events)
	return i.TransformStream(ctx, resp)
}

func (i *MessagesInbound) GetInternalResponse(ctx context.Context) (*model.InternalLLMResponse, error) {
	if i.storedResponse != nil {
		return i.storedResponse, nil
	}
	return i.streamAggregator.BuildAndReset(), nil
}

func geminiContentToMessage(content *model.GeminiContent) model.Message {
	if content == nil {
		return model.Message{}
	}
	role := "user"
	if content.Role == "model" {
		role = "assistant"
	} else if content.Role != "" {
		role = content.Role
	}

	msg := model.Message{Role: role}
	textParts := make([]model.MessageContentPart, 0)
	for _, part := range content.Parts {
		if part == nil {
			continue
		}
		if part.Text != "" {
			text := part.Text
			textParts = append(textParts, model.MessageContentPart{Type: "text", Text: &text})
		}
		if part.InlineData != nil {
			text := fmt.Sprintf("[inline_data:%s]", part.InlineData.MimeType)
			textParts = append(textParts, model.MessageContentPart{Type: "text", Text: &text})
		}
		if part.FileData != nil {
			text := fmt.Sprintf("[file_data:%s]", part.FileData.FileURI)
			textParts = append(textParts, model.MessageContentPart{Type: "text", Text: &text})
		}
		if part.FunctionCall != nil {
			args, _ := json.Marshal(part.FunctionCall.Args)
			msg.ToolCalls = append(msg.ToolCalls, model.ToolCall{
				ID:    part.FunctionCall.ID,
				Type:  "function",
				Index: len(msg.ToolCalls),
				Function: model.FunctionCall{
					Name:      part.FunctionCall.Name,
					Arguments: string(args),
				},
				ThoughtSignature: part.ThoughtSignature,
			})
		}
		if part.FunctionResponse != nil {
			role = "tool"
			msg.Role = role
			id := part.FunctionResponse.ID
			name := part.FunctionResponse.Name
			body, _ := json.Marshal(part.FunctionResponse.Response)
			text := string(body)
			msg.ToolCallID = &id
			msg.ToolCallName = &name
			msg.Content = model.MessageContent{Content: &text}
		}
	}
	if msg.Content.Content == nil && len(msg.Content.MultipleContent) == 0 && len(textParts) > 0 {
		if len(textParts) == 1 && textParts[0].Text != nil {
			msg.Content = model.MessageContent{Content: textParts[0].Text}
		} else {
			msg.Content = model.MessageContent{MultipleContent: textParts}
		}
	}
	return msg
}

func geminiContentText(content *model.GeminiContent) string {
	if content == nil {
		return ""
	}
	var parts []string
	for _, part := range content.Parts {
		if part != nil && part.Text != "" {
			parts = append(parts, part.Text)
		}
	}
	return strings.Join(parts, "\n")
}

func internalToGeminiResponse(response *model.InternalLLMResponse, stream bool) *model.GeminiGenerateContentResponse {
	out := &model.GeminiGenerateContentResponse{
		ResponseId:    responseID(response),
		CreateTime:    time.Unix(responseCreated(response), 0).UTC().Format(time.RFC3339),
		ModelVersion:  responseModel(response),
		UsageMetadata: usageToGemini(response),
	}
	for _, choice := range responseChoices(response) {
		candidate := &model.GeminiCandidate{Index: choice.Index}
		msg := choice.Message
		if stream {
			msg = choice.Delta
		}
		if msg != nil {
			candidate.Content = messageToGeminiContent(msg)
		}
		if choice.FinishReason != nil {
			reason := finishReasonToGemini(*choice.FinishReason)
			candidate.FinishReason = &reason
		}
		out.Candidates = append(out.Candidates, candidate)
	}
	return out
}

func responseChoices(response *model.InternalLLMResponse) []model.Choice {
	if response == nil {
		return nil
	}
	return response.Choices
}

func responseID(response *model.InternalLLMResponse) string {
	if response == nil || response.ID == "" {
		return fmt.Sprintf("octopus-%d", time.Now().UnixNano())
	}
	return response.ID
}

func responseCreated(response *model.InternalLLMResponse) int64 {
	if response == nil || response.Created == 0 {
		return time.Now().Unix()
	}
	return response.Created
}

func responseModel(response *model.InternalLLMResponse) string {
	if response == nil {
		return ""
	}
	return response.Model
}

func messageToGeminiContent(msg *model.Message) *model.GeminiContent {
	content := &model.GeminiContent{Role: "model"}
	if msg == nil {
		return content
	}
	for _, rb := range msg.ReasoningBlocks {
		if rb.Kind == model.ReasoningBlockKindThinking && (rb.Text != "" || rb.Signature != "") {
			content.Parts = append(content.Parts, &model.GeminiPart{Text: rb.Text, Thought: true, ThoughtSignature: rb.Signature})
		}
	}
	if msg.Content.Content != nil && *msg.Content.Content != "" {
		content.Parts = append(content.Parts, &model.GeminiPart{Text: *msg.Content.Content})
	}
	for _, part := range msg.Content.MultipleContent {
		if part.Text != nil && *part.Text != "" {
			content.Parts = append(content.Parts, &model.GeminiPart{Text: *part.Text})
		}
	}
	for _, tc := range msg.ToolCalls {
		args := map[string]interface{}{}
		_ = json.Unmarshal([]byte(tc.Function.Arguments), &args)
		content.Parts = append(content.Parts, &model.GeminiPart{
			FunctionCall: &model.GeminiFunctionCall{
				ID:   tc.ID,
				Name: tc.Function.Name,
				Args: args,
			},
			ThoughtSignature: tc.ThoughtSignature,
		})
	}
	if len(content.Parts) == 0 {
		return nil
	}
	return content
}

func usageToGemini(response *model.InternalLLMResponse) *model.GeminiUsageMetadata {
	if response == nil || response.Usage == nil {
		return nil
	}
	metadata := &model.GeminiUsageMetadata{
		PromptTokenCount:     int(response.Usage.PromptTokens),
		CandidatesTokenCount: int(response.Usage.CompletionTokens),
		TotalTokenCount:      int(response.Usage.TotalTokens),
	}
	if response.Usage.PromptTokensDetails != nil {
		metadata.CachedContentTokenCount = int(response.Usage.PromptTokensDetails.CachedTokens)
	}
	if response.Usage.CompletionTokensDetails != nil {
		metadata.ThoughtsTokenCount = int(response.Usage.CompletionTokensDetails.ReasoningTokens)
	}
	return metadata
}

func finishReasonToGemini(reason string) string {
	switch strings.ToLower(strings.TrimSpace(reason)) {
	case "stop":
		return "STOP"
	case "length":
		return "MAX_TOKENS"
	case "content_filter":
		return "SAFETY"
	case "tool_calls", "function_call":
		return "STOP"
	default:
		return strings.ToUpper(reason)
	}
}

func boolPtr(v bool) *bool { return &v }
