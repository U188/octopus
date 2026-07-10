package openai

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/U188/octopus/internal/transformer/model"
)

func TestConvertToInternalRequestPreservesRawInputItems(t *testing.T) {
	req := &ResponsesRequest{
		Model: "gpt-4o",
		Input: ResponsesInput{Items: []ResponsesItem{
			{Type: "input_text", Text: stringPtr("hello")},
		}},
	}

	internalReq, err := convertToInternalRequest(req)
	if err != nil {
		t.Fatalf("convertToInternalRequest failed: %v", err)
	}
	if len(internalReq.RawInputItems) == 0 {
		t.Fatalf("expected raw input items to be preserved")
	}

	var items []map[string]any
	if err := json.Unmarshal(internalReq.RawInputItems, &items); err != nil {
		t.Fatalf("unmarshal raw input items failed: %v", err)
	}
	if len(items) != 1 || items[0]["type"] != "input_text" {
		t.Fatalf("expected original raw input items to be kept, got %#v", items)
	}
	if internalReq.TransformOptions.ArrayInputs == nil || !*internalReq.TransformOptions.ArrayInputs {
		t.Fatalf("expected array input flag to stay true")
	}
}

func TestConvertToInternalRequestMarksPassthroughForUnsupportedToolType(t *testing.T) {
	req := &ResponsesRequest{
		Model: "gpt-4o",
		Input: ResponsesInput{Text: stringPtr("hello")},
		Tools: []ResponsesTool{{
			Type: "apply_patch",
		}},
	}

	internalReq, err := convertToInternalRequest(req)
	if err != nil {
		t.Fatalf("convertToInternalRequest failed: %v", err)
	}
	if !internalReq.HasOpenAIResponsesPassthrough() {
		t.Fatalf("expected unsupported responses tool to require passthrough")
	}
	if ext := internalReq.GetOpenAIExtensions(); !ext.ResponsesPassthroughRequired || ext.ResponsesPassthroughReason != "tool:apply_patch" {
		t.Fatalf("expected OpenAI extension passthrough view, got %#v", ext)
	}
}

func TestConvertToInternalRequestNormalizesCustomTool(t *testing.T) {
	req := &ResponsesRequest{
		Model: "gpt-5",
		Input: ResponsesInput{Items: []ResponsesItem{
			{Type: "custom_tool_call", CallID: "call_1", Name: "apply_patch", Input: stringPtr("*** Begin Patch")},
			{Type: "custom_tool_call_output", CallID: "call_1", Output: &ResponsesInput{Text: stringPtr("Done")}},
		}},
		Tools: []ResponsesTool{{
			Type:   "custom",
			Name:   "apply_patch",
			Format: json.RawMessage(`{"type":"grammar","syntax":"lark","definition":"start: /.+/"}`),
		}},
	}

	internalReq, err := convertToInternalRequest(req)
	if err != nil {
		t.Fatalf("convertToInternalRequest failed: %v", err)
	}
	if internalReq.HasOpenAIResponsesPassthrough() {
		t.Fatalf("expected custom tool to use the Chat compatibility path")
	}
	if len(internalReq.Tools) != 1 || internalReq.Tools[0].Type != "custom" || internalReq.Tools[0].Function.Name != "apply_patch" {
		t.Fatalf("unexpected normalized custom tool: %#v", internalReq.Tools)
	}
	if len(internalReq.Messages) != 2 || len(internalReq.Messages[0].ToolCalls) != 1 {
		t.Fatalf("unexpected normalized custom tool messages: %#v", internalReq.Messages)
	}
	if got := internalReq.Messages[0].ToolCalls[0].Function.Arguments; got != `{"input":"*** Begin Patch"}` {
		t.Fatalf("custom tool arguments = %q", got)
	}
}

func TestResponseInboundRestoresCustomToolResponse(t *testing.T) {
	inbound := &ResponseInbound{}
	_, err := inbound.TransformRequest(context.Background(), []byte(`{
		"model":"gpt-5",
		"input":"fix it",
		"tools":[{"type":"custom","name":"apply_patch","format":{"type":"grammar"}}]
	}`))
	if err != nil {
		t.Fatalf("TransformRequest failed: %v", err)
	}

	response := &model.InternalLLMResponse{
		ID:    "chatcmpl_1",
		Model: "gpt-5",
		Choices: []model.Choice{{
			Message: &model.Message{
				Role: "assistant",
				ToolCalls: []model.ToolCall{{
					ID:   "call_1",
					Type: "function",
					Function: model.FunctionCall{
						Name:      "apply_patch",
						Arguments: `{"input":"*** Begin Patch"}`,
					},
				}},
			},
		}},
	}
	body, err := inbound.TransformResponse(context.Background(), response)
	if err != nil {
		t.Fatalf("TransformResponse failed: %v", err)
	}

	var got ResponsesResponse
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("unmarshal response failed: %v", err)
	}
	if len(got.Output) != 1 || got.Output[0].Type != "custom_tool_call" || got.Output[0].Input == nil || *got.Output[0].Input != "*** Begin Patch" {
		t.Fatalf("unexpected custom tool response: %#v", got.Output)
	}
	if got.Output[0].Arguments != "" {
		t.Fatalf("custom tool response leaked wrapped arguments: %#v", got.Output[0])
	}
}

func TestResponseInboundRestoresCustomToolStreamEvents(t *testing.T) {
	inbound := &ResponseInbound{}
	_, err := inbound.TransformRequest(context.Background(), []byte(`{
		"model":"gpt-5",
		"input":"fix it",
		"stream":true,
		"tools":[{"type":"custom","name":"apply_patch"}]
	}`))
	if err != nil {
		t.Fatalf("TransformRequest failed: %v", err)
	}

	finishReason := "tool_calls"
	chunks := []*model.InternalLLMResponse{
		{
			ID:    "chatcmpl_1",
			Model: "gpt-5",
			Choices: []model.Choice{{
				Delta: &model.Message{ToolCalls: []model.ToolCall{{
					ID:   "call_1",
					Type: "function",
					Function: model.FunctionCall{
						Name:      "apply_patch",
						Arguments: `{"input":"*** Begin`,
					},
				}}},
			}},
		},
		{
			ID:    "chatcmpl_1",
			Model: "gpt-5",
			Choices: []model.Choice{{
				Delta: &model.Message{ToolCalls: []model.ToolCall{{
					Index: 0,
					Function: model.FunctionCall{
						Arguments: ` Patch"}`,
					},
				}}},
			}},
		},
		{
			ID:    "chatcmpl_1",
			Model: "gpt-5",
			Choices: []model.Choice{{
				Delta:        &model.Message{},
				FinishReason: &finishReason,
			}},
		},
	}

	var output strings.Builder
	for _, chunk := range chunks {
		data, err := inbound.TransformStream(context.Background(), chunk)
		if err != nil {
			t.Fatalf("TransformStream failed: %v", err)
		}
		output.Write(data)
	}

	got := output.String()
	for _, expected := range []string{
		`"type":"custom_tool_call"`,
		`"type":"response.custom_tool_call_input.delta"`,
		`"type":"response.custom_tool_call_input.done"`,
		`"input":"*** Begin Patch"`,
	} {
		if !strings.Contains(got, expected) {
			t.Fatalf("stream output missing %q: %s", expected, got)
		}
	}
	if strings.Contains(got, `"type":"response.function_call_arguments.delta"`) ||
		strings.Contains(got, `"type":"response.function_call_arguments.done"`) {
		t.Fatalf("custom tool stream leaked function argument events: %s", got)
	}
}

func TestConvertToInternalRequestMarksPassthroughForUnsupportedInputItem(t *testing.T) {
	req := &ResponsesRequest{
		Model: "gpt-4o",
		Input: ResponsesInput{Items: []ResponsesItem{{
			Type:   "apply_patch_call_output",
			CallID: "apc_123",
		}}},
	}

	internalReq, err := convertToInternalRequest(req)
	if err != nil {
		t.Fatalf("convertToInternalRequest failed: %v", err)
	}
	if !internalReq.HasOpenAIResponsesPassthrough() {
		t.Fatalf("expected unsupported responses input item to require passthrough")
	}
	if ext := internalReq.GetOpenAIExtensions(); !ext.ResponsesPassthroughRequired || ext.ResponsesPassthroughReason != "input:apply_patch_call_output" {
		t.Fatalf("expected OpenAI extension passthrough view, got %#v", ext)
	}
}

func TestConvertToInternalRequestDoesNotMarkPassthroughForSupportedFileAndAudioInputs(t *testing.T) {
	req := &ResponsesRequest{
		Model: "gpt-4o",
		Input: ResponsesInput{Items: []ResponsesItem{
			{
				Type: "message",
				Role: "user",
				Content: &ResponsesInput{Items: []ResponsesItem{
					{Type: "input_file", FileID: stringPtr("file_123")},
					{Type: "input_audio", InputAudio: &ResponsesInputAudio{Format: "wav", Data: "AAA="}},
				}},
			},
		}},
	}

	internalReq, err := convertToInternalRequest(req)
	if err != nil {
		t.Fatalf("convertToInternalRequest failed: %v", err)
	}
	if internalReq.HasOpenAIResponsesPassthrough() {
		t.Fatalf("expected supported file/audio inputs to stay normalized without passthrough")
	}
	if len(internalReq.Messages) != 1 || len(internalReq.Messages[0].Content.MultipleContent) != 2 {
		t.Fatalf("expected supported file/audio inputs to normalize into message content, got %#v", internalReq.Messages)
	}
	if internalReq.Messages[0].Content.MultipleContent[0].Type != "file" {
		t.Fatalf("expected file content part, got %#v", internalReq.Messages[0].Content.MultipleContent[0])
	}
	if internalReq.Messages[0].Content.MultipleContent[1].Type != "input_audio" {
		t.Fatalf("expected input_audio content part, got %#v", internalReq.Messages[0].Content.MultipleContent[1])
	}
}

func TestConvertToInternalRequestNormalizesTopLevelInputFile(t *testing.T) {
	req := &ResponsesRequest{
		Model: "gpt-4o",
		Input: ResponsesInput{Items: []ResponsesItem{{
			Type:     "input_file",
			FileID:   stringPtr("file_456"),
			Filename: stringPtr("notes.txt"),
		}}},
	}

	internalReq, err := convertToInternalRequest(req)
	if err != nil {
		t.Fatalf("convertToInternalRequest failed: %v", err)
	}
	if internalReq.HasOpenAIResponsesPassthrough() {
		t.Fatalf("expected top-level input_file to stay normalized without passthrough")
	}
	if len(internalReq.Messages) != 1 {
		t.Fatalf("expected one normalized message, got %#v", internalReq.Messages)
	}
	if internalReq.Messages[0].Role != "user" {
		t.Fatalf("expected top-level input_file to default to user role, got %#v", internalReq.Messages[0].Role)
	}
	if len(internalReq.Messages[0].Content.MultipleContent) != 1 || internalReq.Messages[0].Content.MultipleContent[0].Type != "file" {
		t.Fatalf("expected top-level input_file to become file content, got %#v", internalReq.Messages[0].Content)
	}
	if internalReq.Messages[0].Content.MultipleContent[0].File == nil || internalReq.Messages[0].Content.MultipleContent[0].File.FileID != "file_456" {
		t.Fatalf("expected normalized file reference to preserve file_id, got %#v", internalReq.Messages[0].Content.MultipleContent[0].File)
	}
}

func stringPtr(value string) *string {
	return &value
}
