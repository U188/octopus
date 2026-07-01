package handlers

import (
	"context"
	"net/http"
	"strconv"
	"strings"

	"github.com/U188/octopus/internal/apperror"
	"github.com/U188/octopus/internal/model"
	"github.com/U188/octopus/internal/op"
	"github.com/U188/octopus/internal/server/auth"
	"github.com/U188/octopus/internal/server/middleware"
	"github.com/U188/octopus/internal/server/resp"
	"github.com/U188/octopus/internal/server/router"
	"github.com/gin-gonic/gin"
	"github.com/samber/lo"
)

func init() {
	router.NewGroupRouter("/api/v1/apikey").
		Use(middleware.Auth()).
		Use(middleware.RequireJSON()).
		AddRoute(
			router.NewRoute("/create", http.MethodPost).
				Handle(createAPIKey),
		).
		AddRoute(
			router.NewRoute("/list", http.MethodGet).
				Handle(listAPIKey),
		).
		AddRoute(
			router.NewRoute("/update", http.MethodPost).
				Handle(updateAPIKey),
		).
		AddRoute(
			router.NewRoute("/delete/:id", http.MethodDelete).
				Handle(deleteAPIKey),
		)
	router.NewGroupRouter("/api/v1/apikey").
		Use(middleware.APIKeyAuth()).
		AddRoute(
			router.NewRoute("/stats", http.MethodGet).
				Handle(getStatsAPIKeyById),
		).
		AddRoute(
			router.NewRoute("/login", http.MethodGet).
				Handle(loginAPIKey),
		)
}

func createAPIKey(c *gin.Context) {
	var req model.APIKey
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.InvalidJSON(c)
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		resp.ErrorWithAppError(c, http.StatusBadRequest, apperror.New(apperror.CodeCommonInvalidParam, "name is required").WithStatus(http.StatusBadRequest))
		return
	}
	if req.MaxRPM < 0 {
		resp.ErrorWithAppError(c, http.StatusBadRequest, apperror.New(apperror.CodeCommonInvalidParam, "max_rpm must be non-negative").WithStatus(http.StatusBadRequest))
		return
	}
	if apiKeyNameExists(req.Name, 0, c.Request.Context()) {
		resp.ErrorWithAppError(c, http.StatusConflict, apperror.New(apperror.CodeCommonInvalidParam, "name already exists").WithStatus(http.StatusConflict))
		return
	}
	req.APIKey = strings.TrimSpace(req.APIKey)
	if req.APIKey == "" {
		for range 5 {
			candidate := auth.GenerateAPIKey()
			if _, err := op.APIKeyGetByAPIKey(candidate, c.Request.Context()); err != nil {
				req.APIKey = candidate
				break
			}
		}
		if req.APIKey == "" {
			resp.Error(c, http.StatusInternalServerError, "failed to generate unique API key")
			return
		}
	} else if _, err := op.APIKeyGetByAPIKey(req.APIKey, c.Request.Context()); err == nil {
		resp.ErrorWithAppError(c, http.StatusConflict, apperror.New(apperror.CodeCommonInvalidParam, "api_key already exists").WithStatus(http.StatusConflict))
		return
	}
	if err := op.APIKeyCreate(&req, c.Request.Context()); err != nil {
		resp.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	resp.Success(c, req)
}

func listAPIKey(c *gin.Context) {
	apiKeys, err := op.APIKeyList(c.Request.Context())
	if err != nil {
		resp.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	resp.Success(c, apiKeys)
}

func updateAPIKey(c *gin.Context) {
	var req model.APIKey
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.InvalidJSON(c)
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		resp.ErrorWithAppError(c, http.StatusBadRequest, apperror.New(apperror.CodeCommonInvalidParam, "name is required").WithStatus(http.StatusBadRequest))
		return
	}
	if req.MaxRPM < 0 {
		resp.ErrorWithAppError(c, http.StatusBadRequest, apperror.New(apperror.CodeCommonInvalidParam, "max_rpm must be non-negative").WithStatus(http.StatusBadRequest))
		return
	}
	if apiKeyNameExists(req.Name, req.ID, c.Request.Context()) {
		resp.ErrorWithAppError(c, http.StatusConflict, apperror.New(apperror.CodeCommonInvalidParam, "name already exists").WithStatus(http.StatusConflict))
		return
	}
	if err := op.APIKeyUpdate(&req, c.Request.Context()); err != nil {
		resp.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	resp.Success(c, req)
}

func deleteAPIKey(c *gin.Context) {
	id := c.Param("id")
	idNum, err := strconv.Atoi(id)
	if err != nil {
		resp.InvalidParam(c)
		return
	}
	if err := op.APIKeyDelete(idNum, c.Request.Context()); err != nil {
		resp.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	resp.Success(c, nil)
}

func getStatsAPIKeyById(c *gin.Context) {
	id := c.GetInt("api_key_id")
	stats := op.StatsAPIKeyGet(id)
	info, err := op.APIKeyGet(id, c.Request.Context())
	if err != nil {
		resp.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	models, err := op.GroupListModel(c.Request.Context())
	if err != nil {
		resp.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	var modelsString string
	if info.SupportedModels == "" {
		modelsString = strings.Join(models, ", ")
	} else {
		supportedModels := lo.Map(strings.Split(info.SupportedModels, ","), func(s string, _ int) string {
			return strings.TrimSpace(s)
		})
		models = lo.Filter(models, func(m string, _ int) bool {
			return lo.Contains(supportedModels, m)
		})
		modelsString = strings.Join(models, ", ")
	}
	info.SupportedModels = modelsString
	resp.Success(c, map[string]any{
		"stats": stats,
		"info":  info,
	})
}

func loginAPIKey(c *gin.Context) {
	resp.Success(c, nil)
}

func apiKeyNameExists(name string, exceptID int, ctx context.Context) bool {
	apiKeys, err := op.APIKeyList(ctx)
	if err != nil {
		return false
	}
	for _, apiKey := range apiKeys {
		if apiKey.ID != exceptID && strings.EqualFold(strings.TrimSpace(apiKey.Name), name) {
			return true
		}
	}
	return false
}
