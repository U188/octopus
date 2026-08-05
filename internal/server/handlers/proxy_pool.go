package handlers

import (
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/U188/octopus/internal/model"
	"github.com/U188/octopus/internal/op"
	"github.com/U188/octopus/internal/server/middleware"
	"github.com/U188/octopus/internal/server/resp"
	"github.com/U188/octopus/internal/server/router"
	"github.com/gin-gonic/gin"
)

func init() {
	router.NewGroupRouter("/api/v1/proxy-pool").
		Use(middleware.Auth()).
		AddRoute(router.NewRoute("/list", http.MethodGet).Handle(listProxyConfigurations)).
		AddRoute(router.NewRoute("/references/:id", http.MethodGet).Handle(listProxyConfigurationReferences)).
		AddRoute(router.NewRoute("/delete/:id", http.MethodDelete).Handle(deleteProxyConfiguration))

	router.NewGroupRouter("/api/v1/proxy-pool").
		Use(middleware.Auth()).
		Use(middleware.RequireJSON()).
		AddRoute(router.NewRoute("/create", http.MethodPost).Handle(createProxyConfiguration)).
		AddRoute(router.NewRoute("/update", http.MethodPost).Handle(updateProxyConfiguration)).
		AddRoute(router.NewRoute("/test", http.MethodPost).Handle(testProxyConfiguration))
}

func listProxyConfigurations(c *gin.Context) {
	items, err := op.ProxyConfigurationList(c.Request.Context())
	if err != nil {
		resp.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	resp.Success(c, items)
}

func listProxyConfigurationReferences(c *gin.Context) {
	idNum, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		resp.InvalidParam(c)
		return
	}
	items, err := op.ProxyConfigurationReferences(idNum, c.Request.Context())
	if err != nil {
		resp.Error(c, http.StatusBadRequest, err.Error())
		return
	}
	resp.Success(c, items)
}

func createProxyConfiguration(c *gin.Context) {
	type proxyConfigurationCreateRequest struct {
		Name    string `json:"name" binding:"required"`
		URL     string `json:"url" binding:"required"`
		Enabled *bool  `json:"enabled,omitempty"`
		Remark  string `json:"remark,omitempty"`
	}

	var req proxyConfigurationCreateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.InvalidJSON(c)
		return
	}
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	item := model.ProxyConfiguration{
		Name:    req.Name,
		URL:     req.URL,
		Enabled: enabled,
		Remark:  req.Remark,
	}
	if err := op.ProxyConfigurationCreate(&item, c.Request.Context()); err != nil {
		recordAuditFailure(c, "proxy_pool.create", map[string]any{
			"name": item.Name,
		}, err)
		resp.Error(c, http.StatusBadRequest, err.Error())
		return
	}
	recordAuditSuccess(c, "proxy_pool.create", map[string]any{
		"id":      item.ID,
		"name":    item.Name,
		"enabled": item.Enabled,
	})
	resp.Success(c, item)
}

func updateProxyConfiguration(c *gin.Context) {
	var req model.ProxyConfigurationUpdateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.InvalidJSON(c)
		return
	}
	item, err := op.ProxyConfigurationUpdate(&req, c.Request.Context())
	if err != nil {
		recordAuditFailure(c, "proxy_pool.update", map[string]any{
			"id": req.ID,
		}, err)
		resp.Error(c, http.StatusBadRequest, err.Error())
		return
	}
	recordAuditSuccess(c, "proxy_pool.update", map[string]any{
		"id":      item.ID,
		"name":    item.Name,
		"enabled": item.Enabled,
	})
	resp.Success(c, item)
}

func deleteProxyConfiguration(c *gin.Context) {
	idNum, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		resp.InvalidParam(c)
		return
	}
	if err := op.ProxyConfigurationDelete(idNum, c.Request.Context()); err != nil {
		recordAuditFailure(c, "proxy_pool.delete", map[string]any{
			"id": idNum,
		}, err)
		resp.Error(c, http.StatusBadRequest, err.Error())
		return
	}
	recordAuditSuccess(c, "proxy_pool.delete", map[string]any{
		"id": idNum,
	})
	resp.Success(c, nil)
}

func testProxyConfiguration(c *gin.Context) {
	var req model.ProxyTestRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.InvalidJSON(c)
		return
	}
	action := proxyTestAuditAction(req)
	detail := proxyTestAuditDetail(req)
	result, err := op.ProxyConfigurationTest(req, c.Request.Context())
	if err != nil {
		recordAuditFailure(c, action, detail, errors.New("proxy connectivity test request failed"))
		resp.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	detail["status_code"] = result.StatusCode
	detail["duration_ms"] = result.DurationMS
	if result.Success {
		recordAuditSuccess(c, action, detail)
	} else {
		recordAuditFailure(c, action, detail, errors.New("proxy connectivity test failed"))
	}
	resp.Success(c, result)
}

func proxyTestAuditAction(req model.ProxyTestRequest) string {
	if req.UseSystemProxy {
		return "system_proxy.test"
	}
	return "proxy_pool.test"
}

func proxyTestAuditDetail(req model.ProxyTestRequest) map[string]any {
	source := "draft"
	if req.UseSystemProxy {
		source = "system"
	} else if req.ProxyConfigID != nil && *req.ProxyConfigID > 0 {
		source = "pool"
	}
	targetHost := "www.google.com"
	if parsed, err := url.Parse(strings.TrimSpace(req.URL)); err == nil && strings.TrimSpace(parsed.Hostname()) != "" {
		targetHost = strings.TrimSpace(parsed.Hostname())
	}
	detail := map[string]any{
		"source":      source,
		"target_host": targetHost,
	}
	if req.ProxyConfigID != nil && *req.ProxyConfigID > 0 {
		detail["proxy_config_id"] = *req.ProxyConfigID
	}
	return detail
}
