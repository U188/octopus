package volcengine

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/U188/octopus/internal/transformer/model"
	"github.com/U188/octopus/internal/transformer/outbound/openai"
)

func TestConvertToResponsesInputEmptyItemsDoesNotPanic(t *testing.T) {
	got := convertToResponsesInput(openai.ResponsesInput{})
	if got.Text != nil || len(got.Items) != 0 || len(got.Raw) != 0 {
		t.Fatalf("expected empty input to stay empty, got %+v", got)
	}
}

func TestConvertToResponsesInputRawPassthrough(t *testing.T) {
	raw := json.RawMessage(`[{"type":"message","role":"user","content":"hi"}]`)
	got := convertToResponsesInput(openai.ResponsesInput{Raw: raw})
	if string(got.Raw) != string(raw) {
		t.Fatalf("raw input must pass through unchanged, got %s", got.Raw)
	}

	body, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}
	if string(body) != string(raw) {
		t.Fatalf("marshal must prefer raw, got %s", body)
	}
}

func TestConvertToResponsesInputRawTrailingAssistantPartial(t *testing.T) {
	raw := json.RawMessage(`[{"type":"message","role":"user","content":"hi"},{"type":"message","role":"assistant","content":"par"}]`)
	got := convertToResponsesInput(openai.ResponsesInput{Raw: raw})

	var elems []map[string]any
	if err := json.Unmarshal(got.Raw, &elems); err != nil {
		t.Fatalf("unmarshal patched raw failed: %v", err)
	}
	if len(elems) != 2 {
		t.Fatalf("expected 2 items, got %d", len(elems))
	}
	if partial, _ := elems[1]["partial"].(bool); !partial {
		t.Fatalf("trailing assistant item must be marked partial, got %+v", elems[1])
	}
	if _, ok := elems[0]["partial"]; ok {
		t.Fatalf("non-trailing item must stay untouched, got %+v", elems[0])
	}
}

func TestConvertToResponsesInputItemsTrailingAssistantPartial(t *testing.T) {
	items := []openai.ResponsesItem{
		{Type: "message", Role: "user"},
		{Type: "message", Role: "assistant"},
	}
	got := convertToResponsesInput(openai.ResponsesInput{Items: items})
	if len(got.Items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(got.Items))
	}
	if !got.Items[1].Partial {
		t.Fatalf("trailing assistant item must be partial")
	}
	if got.Items[0].Partial {
		t.Fatalf("first item must not be partial")
	}
}

func TestTransformRequestWithRawInputItemsDoesNotPanic(t *testing.T) {
	req := &model.InternalLLMRequest{Model: "doubao-seed-1-6-251015"}
	req.SetOpenAIRawInputItems(json.RawMessage(`[{"type":"message","role":"user","content":"hi"}]`))

	o := &ResponseOutbound{}
	httpReq, err := o.TransformRequest(context.Background(), req, "https://example.com/api/v3", "test-key")
	if err != nil {
		t.Fatalf("TransformRequest failed: %v", err)
	}

	var body struct {
		Input json.RawMessage `json:"input"`
	}
	if err := json.NewDecoder(httpReq.Body).Decode(&body); err != nil {
		t.Fatalf("decode request body failed: %v", err)
	}
	if !strings.Contains(string(body.Input), `"role":"user"`) {
		t.Fatalf("raw input items must be forwarded, got %s", body.Input)
	}
}
