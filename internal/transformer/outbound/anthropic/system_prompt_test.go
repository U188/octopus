package anthropic

import (
	"testing"

	"github.com/U188/octopus/internal/transformer/model"
)

func sysStr(s string) *string { return &s }

// 回归：OpenAI 允许 system content 为数组形式（[{type:"text",...}]），
// 此时 Content.Content 为 nil；原实现只读字符串字段，会发出空 text 块被 400。
func TestConvertSystemPromptArrayContent(t *testing.T) {
	req := &model.InternalLLMRequest{
		Messages: []model.Message{
			{
				Role: "system",
				Content: model.MessageContent{MultipleContent: []model.MessageContentPart{
					{Type: "text", Text: sysStr("part one")},
					{Type: "text", Text: sysStr("part two")},
				}},
			},
			{Role: "user", Content: model.MessageContent{Content: sysStr("hi")}},
		},
	}

	prompt := convertSystemPrompt(req)
	if prompt == nil {
		t.Fatal("array-form system prompt must not be dropped")
	}
	if len(prompt.MultiplePrompts) != 2 {
		t.Fatalf("expected 2 system parts, got %+v", prompt.MultiplePrompts)
	}
	if prompt.MultiplePrompts[0].Text != "part one" || prompt.MultiplePrompts[1].Text != "part two" {
		t.Fatalf("system parts content mismatch: %+v", prompt.MultiplePrompts)
	}
	for _, part := range prompt.MultiplePrompts {
		if part.Text == "" {
			t.Fatal("must not emit empty text system blocks")
		}
	}
}

// 内容为空的 system 消息应整体省略 system 字段，而不是发空 text 块。
func TestConvertSystemPromptEmptyContentOmitted(t *testing.T) {
	req := &model.InternalLLMRequest{
		Messages: []model.Message{
			{Role: "system", Content: model.MessageContent{Content: sysStr("")}},
			{Role: "user", Content: model.MessageContent{Content: sysStr("hi")}},
		},
	}
	if prompt := convertSystemPrompt(req); prompt != nil {
		t.Fatalf("empty system content must omit the system field, got %+v", prompt)
	}
}

// 字符串形式 system 保持原行为。
func TestConvertSystemPromptStringContent(t *testing.T) {
	req := &model.InternalLLMRequest{
		Messages: []model.Message{
			{Role: "system", Content: model.MessageContent{Content: sysStr("be helpful")}},
		},
	}
	prompt := convertSystemPrompt(req)
	if prompt == nil || len(prompt.MultiplePrompts) != 1 || prompt.MultiplePrompts[0].Text != "be helpful" {
		t.Fatalf("string system prompt regression: %+v", prompt)
	}
}
