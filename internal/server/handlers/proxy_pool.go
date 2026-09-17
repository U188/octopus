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
		AddRoute(router.NewRoute("/nodes/:id", http.MethodGet).Handle(listProxySubscriptionNodes)).
		AddRoute(router.NewRoute("/runtime", http.MethodGet).Handle(getProxyRuntimeStatus)).
		AddRoute(router.NewRoute("/sync/:id", http.MethodPost).Handle(syncProxySubscription)).
		AddRoute(router.NewRoute("/runtime/restart", http.MethodPost).Handle(restartProxyRuntime)).
		AddRoute(router.NewRoute("/node/:id/recover", http.MethodPost).Handle(recoverProxySubscriptionNode)).
		AddRoute(router.NewRoute("/node/:id/test", http.MethodPost).Handle(testProxySubscriptionNode)).
		AddRoute(router.NewRoute("/delete/:id", http.MethodDelete).Handle(deleteProxyConfiguration))

	router.NewGroupRouter("/api/v1/proxy-pool").
		Use(middleware.Auth()).
		Use(middleware.RequireJSON()).
		AddRoute(router.NewRoute("/create", http.MethodPost).Handle(createProxyConfiguration)).
		AddRoute(router.NewRoute("/update", http.MethodPost).Handle(updateProxyConfiguration)).
		AddRoute(router.NewRoute("/node/:id/enabled", http.MethodPost).Handle(setProxySubscriptionNodeEnabled)).
		AddRoute(router.NewRoute("/node/:id/model-probe", http.MethodPost).Handle(probeProxySubscriptionNodeModel)).
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

func listProxySubscriptionNodes(c *gin.Context) {
	idNum, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		resp.InvalidParam(c)
		return
	}
	item, err := op.ProxyConfigurationGet(idNum, c.Request.Context())
	if err != nil || item.Type != model.ProxyConfigurationTypeSubscription {
		resp.Error(c, http.StatusBadRequest, "proxy subscription not found")
		return
	}
	nodes, err := op.ProxySubscriptionNodes(idNum, c.Request.Context())
	if err != nil {
		resp.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	resp.Success(c, nodes)
}

func syncProxySubscription(c *gin.Context) {
	idNum, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		resp.InvalidParam(c)
		return
	}
	result, err := op.ProxySubscriptionSync(idNum, c.Request.Context())
	if err != nil {
		recordAuditFailure(c, "proxy_pool.subscription.sync", map[string]any{"id": idNum}, errors.New("proxy subscription sync failed"))
		resp.Error(c, http.StatusBadRequest, err.Error())
		return
	}
	recordAuditSuccess(c, "proxy_pool.subscription.sync", map[string]any{
		"id":             idNum,
		"fetched_count":  result.FetchedCount,
		"healthy_count":  result.HealthyCount,
		"degraded_count": result.DegradedCount,
		"failed_count":   result.FailedCount,
	})
	resp.Success(c, result)
}

func createProxyConfiguration(c *gin.Context) {
	type proxyConfigurationCreateRequest struct {
		Name                   string                       `json:"name" binding:"required"`
		URL                    string                       `json:"url" binding:"required"`
		Type                   model.ProxyConfigurationType `json:"type,omitempty"`
		Enabled                *bool                        `json:"enabled,omitempty"`
		Remark                 string                       `json:"remark,omitempty"`
		RefreshIntervalMinutes int                          `json:"refresh_interval_minutes,omitempty"`
		HealthCheckURL         string                       `json:"health_check_url,omitempty"`
		SelectionStrategy      model.ProxySelectionStrategy `json:"selection_strategy,omitempty"`
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
		Name:                   req.Name,
		URL:                    req.URL,
		Type:                   req.Type,
		Enabled:                enabled,
		Remark:                 req.Remark,
		RefreshIntervalMinutes: req.RefreshIntervalMinutes,
		HealthCheckURL:         req.HealthCheckURL,
		SelectionStrategy:      req.SelectionStrategy,
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
		"type":    item.Type,
		"enabled": item.Enabled,
	})
	resp.Success(c, item)
}

func getProxyRuntimeStatus(c *gin.Context) {
	resp.Success(c, op.ProxyRuntimeStatus(c.Request.Context()))
}

func restartProxyRuntime(c *gin.Context) {
	if err := op.ProxyRuntimeReload(c.Request.Context()); err != nil {
		resp.Error(c, http.StatusBadRequest, err.Error())
		return
	}
	recordAuditSuccess(c, "proxy_pool.runtime.restart", nil)
	resp.Success(c, op.ProxyRuntimeStatus(c.Request.Context()))
}

func recoverProxySubscriptionNode(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		resp.InvalidParam(c)
		return
	}
	if err := op.ProxySubscriptionNodeRecover(id, c.Request.Context()); err != nil {
		resp.Error(c, http.StatusBadRequest, err.Error())
		return
	}
	recordAuditSuccess(c, "proxy_pool.node.recover", map[string]any{"id": id})
	resp.Success(c, nil)
}

func testProxySubscriptionNode(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		resp.InvalidParam(c)
		return
	}
	result, err := op.ProxySubscriptionNodeTest(id, c.Request.Context())
	if err != nil {
		resp.Error(c, http.StatusBadRequest, err.Error())
		return
	}
	recordAuditSuccess(c, "proxy_pool.node.test", map[string]any{"id": id, "health_status": result.HealthStatus})
	resp.Success(c, result)
}

func probeProxySubscriptionNodeModel(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		resp.InvalidParam(c)
		return
	}
	var request op.ProxyModelProbeRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		resp.InvalidJSON(c)
		return
	}
	result, err := op.ProxySubscriptionNodeModelProbe(id, request, c.Request.Context())
	if err != nil {
		resp.Error(c, http.StatusBadRequest, err.Error())
		return
	}
	recordAuditSuccess(c, "proxy_pool.node.model_probe", map[string]any{"id": id, "model": request.Model, "status": result.Status})
	resp.Success(c, result)
}

func setProxySubscriptionNodeEnabled(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		resp.InvalidParam(c)
		return
	}
	var request struct {
		Enabled bool `json:"enabled"`
	}
	if err := c.ShouldBindJSON(&request); err != nil {
		resp.InvalidJSON(c)
		return
	}
	if err := op.ProxySubscriptionNodeSetEnabled(id, request.Enabled, c.Request.Context()); err != nil {
		resp.Error(c, http.StatusBadRequest, err.Error())
		return
	}
	recordAuditSuccess(c, "proxy_pool.node.enabled", map[string]any{"id": id, "enabled": request.Enabled})
	resp.Success(c, nil)
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
		"type":    item.Type,
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
	detail["health_status"] = result.HealthStatus
	detail["attempt_count"] = result.AttemptCount
	detail["success_count"] = result.SuccessCount
	if result.HealthStatus != model.ProxyTestHealthFailed {
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
