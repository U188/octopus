package inbound

import (
	"github.com/U188/octopus/internal/transformer/inbound/anthropic"
	"github.com/U188/octopus/internal/transformer/inbound/gemini"
	"github.com/U188/octopus/internal/transformer/inbound/openai"
	"github.com/U188/octopus/internal/transformer/model"
)

type InboundType int

const (
	InboundTypeOpenAIChat InboundType = iota
	InboundTypeOpenAIResponse
	InboundTypeAnthropic
	InboundTypeOpenAIEmbedding
	InboundTypeGemini
)

var inboundFactories = map[InboundType]func() model.Inbound{
	InboundTypeOpenAIChat:      func() model.Inbound { return &openai.ChatInbound{} },
	InboundTypeOpenAIResponse:  func() model.Inbound { return &openai.ResponseInbound{} },
	InboundTypeOpenAIEmbedding: func() model.Inbound { return &openai.EmbeddingInbound{} },
	InboundTypeAnthropic:       func() model.Inbound { return &anthropic.MessagesInbound{} },
	InboundTypeGemini:          func() model.Inbound { return &gemini.MessagesInbound{} },
}

func Get(inboundType InboundType) model.Inbound {
	if factory, ok := inboundFactories[inboundType]; ok {
		return factory()
	}
	return nil
}
