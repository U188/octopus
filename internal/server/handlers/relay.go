package handlers

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/U188/octopus/internal/relay"
	"github.com/U188/octopus/internal/server/middleware"
	"github.com/U188/octopus/internal/server/router"
	"github.com/U188/octopus/internal/transformer/inbound"
	geminiInbound "github.com/U188/octopus/internal/transformer/inbound/gemini"
	"github.com/gin-gonic/gin"
)

func init() {
	router.NewGroupRouter("/v1").
		Use(middleware.APIKeyAuth()).
		Use(middleware.RequireJSON()).
		AddRoute(
			router.NewRoute("/chat/completions", http.MethodPost).
				Handle(chat),
		).
		AddRoute(
			router.NewRoute("/responses", http.MethodPost).
				Handle(response),
		).
		AddRoute(
			router.NewRoute("/responses/compact", http.MethodPost).
				Handle(responseCompact),
		).
		AddRoute(
			router.NewRoute("/messages", http.MethodPost).
				Handle(message),
		).
		AddRoute(
			router.NewRoute("/embeddings", http.MethodPost).
				Handle(embedding),
		)

	// WebSocket route for /v1/responses (no RequireJSON middleware)
	router.NewGroupRouter("/v1").
		Use(middleware.APIKeyAuth()).
		AddRoute(
			router.NewRoute("/responses", http.MethodGet).
				Handle(wsResponse),
		)

	for _, prefix := range []string{"/v1", "/v1beta"} {
		router.NewGroupRouter(prefix).
			Use(middleware.APIKeyAuth()).
			Use(middleware.RequireJSON()).
			AddRoute(
				router.NewRoute("/models/*model_method", http.MethodPost).
					Handle(geminiMessage),
			)
	}
}

func chat(c *gin.Context) {
	relay.Handler(inbound.InboundTypeOpenAIChat, c)
}
func response(c *gin.Context) {
	relay.Handler(inbound.InboundTypeOpenAIResponse, c)
}
func responseCompact(c *gin.Context) {
	relay.HandleResponsesCompact(c)
}
func message(c *gin.Context) {
	relay.Handler(inbound.InboundTypeAnthropic, c)
}
func embedding(c *gin.Context) {
	relay.Handler(inbound.InboundTypeOpenAIEmbedding, c)
}
func wsResponse(c *gin.Context) {
	relay.HandleWSResponse(c)
}

func geminiMessage(c *gin.Context) {
	modelName, stream, err := parseGeminiModelMethod(c.Param("model_method"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": gin.H{"message": err.Error()}})
		return
	}
	ctx := geminiInbound.WithRequestInfo(c.Request.Context(), modelName, stream)
	c.Request = c.Request.WithContext(ctx)
	relay.Handler(inbound.InboundTypeGemini, c)
}

func parseGeminiModelMethod(raw string) (string, bool, error) {
	value := strings.Trim(strings.TrimSpace(raw), "/")
	if value == "" {
		return "", false, fmt.Errorf("missing model")
	}
	var method string
	modelName, method, ok := strings.Cut(value, ":")
	if !ok {
		return "", false, fmt.Errorf("missing Gemini method")
	}
	switch method {
	case "generateContent":
		return modelName, false, nil
	case "streamGenerateContent":
		return modelName, true, nil
	default:
		return "", false, fmt.Errorf("unsupported Gemini method %q", method)
	}
}
