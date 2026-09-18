package op

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/U188/octopus/internal/db"
	"github.com/U188/octopus/internal/model"
	"github.com/U188/octopus/internal/singbox"
)

type ProxyModelProbeRequest struct {
	URL    string `json:"url"`
	Model  string `json:"model"`
	APIKey string `json:"api_key"`
}

type ProxyModelProbeResult struct {
	Status     model.ProxyTestHealthStatus `json:"status"`
	StatusCode int                         `json:"status_code"`
	DurationMS int64                       `json:"duration_ms"`
	Message    string                      `json:"message"`
}

var (
	proxyRuntimeDataDir   = "data"
	proxyRuntimeDataDirMu sync.RWMutex
	proxyRuntimeReloadMu  sync.Mutex
)

func ProxyRuntimeInit(ctx context.Context, dataDir string) error {
	proxyRuntimeDataDirMu.Lock()
	proxyRuntimeDataDir = dataDir
	proxyRuntimeDataDirMu.Unlock()
	enabled, err := SettingGetBool(model.SettingKeySingBoxEnabled)
	if err != nil {
		enabled = true
	}
	path, err := SettingGetString(model.SettingKeySingBoxPath)
	if err != nil || path == "" {
		path = "sing-box"
	}
	singbox.Default.Configure(enabled, path, filepath.Join(dataDir, "sing-box"))
	return ProxyRuntimeReload(ctx)
}

func ProxyRuntimeApplySettings(ctx context.Context) error {
	enabled, err := SettingGetBool(model.SettingKeySingBoxEnabled)
	if err != nil {
		enabled = true
	}
	path, err := SettingGetString(model.SettingKeySingBoxPath)
	if err != nil || path == "" {
		path = "sing-box"
	}
	proxyRuntimeDataDirMu.RLock()
	dataDir := proxyRuntimeDataDir
	proxyRuntimeDataDirMu.RUnlock()
	return ProxyRuntimeConfigure(ctx, enabled, path, dataDir)
}

func ProxyRuntimeConfigure(ctx context.Context, enabled bool, binaryPath, dataDir string) error {
	singbox.Default.Configure(enabled, binaryPath, filepath.Join(dataDir, "sing-box"))
	return ProxyRuntimeReload(ctx)
}

func ProxyRuntimeReload(ctx context.Context) error {
	proxyRuntimeReloadMu.Lock()
	defer proxyRuntimeReloadMu.Unlock()
	nodes, err := proxySingBoxNodes(ctx, 0, nil)
	if err != nil {
		return err
	}
	now := time.Now()
	endpoints, err := singbox.Default.Reload(ctx, nodes)
	if !singbox.Default.Enabled() {
		_ = db.GetDB().WithContext(ctx).Model(&model.ProxySubscriptionNode{}).
			Where("runtime_type = ? AND active = ?", model.ProxyNodeRuntimeSingBox, true).
			Updates(map[string]any{"conversion_status": model.ProxyNodeConversionPending, "last_error": "sing-box is disabled", "last_checked_at": now}).Error
		proxySubscriptionNodeCache.Clear()
		return nil
	}
	if err != nil {
		status := singbox.Default.Status(context.Background())
		if status.Running {
			// Reload validates a new config before swapping processes. Keep the
			// committed node health when the previous runtime is still serving.
			return err
		}
		_ = db.GetDB().WithContext(ctx).Model(&model.ProxySubscriptionNode{}).
			Where("runtime_type = ? AND active = ?", model.ProxyNodeRuntimeSingBox, true).
			Updates(map[string]any{"conversion_status": model.ProxyNodeConversionFailed, "last_error": truncateProxySubscriptionMessage(err.Error()), "last_checked_at": now}).Error
		proxySubscriptionNodeCache.Clear()
		return err
	}
	var persisted []model.ProxySubscriptionNode
	if err := db.GetDB().WithContext(ctx).Where("runtime_type = ? AND active = ?", model.ProxyNodeRuntimeSingBox, true).Find(&persisted).Error; err != nil {
		return err
	}
	for _, node := range persisted {
		status := model.ProxyNodeConversionFailed
		message := "sing-box endpoint unavailable"
		if endpoints[node.NodeKey] != "" {
			status = model.ProxyNodeConversionReady
			message = ""
		}
		if err := db.GetDB().WithContext(ctx).Model(&model.ProxySubscriptionNode{}).Where("id = ?", node.ID).
			Updates(map[string]any{"conversion_status": status, "last_error": message}).Error; err != nil {
			return err
		}
	}
	proxySubscriptionNodeCache.Clear()
	return nil
}

func ProxyRuntimeStatus(ctx context.Context) singbox.Status {
	return singbox.Default.Status(ctx)
}

func ProxyRuntimeStop() error {
	return singbox.Default.Stop()
}

func ProxySubscriptionNodeRecover(nodeID int, ctx context.Context) error {
	var node model.ProxySubscriptionNode
	if err := db.GetDB().WithContext(ctx).First(&node, nodeID).Error; err != nil {
		return fmt.Errorf("proxy subscription node not found")
	}
	if err := db.GetDB().WithContext(ctx).Model(&node).Updates(map[string]any{
		"runtime_failure_count": 0, "quarantined_until": nil, "last_runtime_failure_at": nil,
		"last_runtime_error": "", "user_enabled": true,
	}).Error; err != nil {
		return err
	}
	invalidateProxySubscriptionCache(node.ProxyConfigurationID)
	if node.RuntimeType == model.ProxyNodeRuntimeSingBox {
		return ProxyRuntimeReload(ctx)
	}
	return nil
}

func ProxySubscriptionNodeSetEnabled(nodeID int, enabled bool, ctx context.Context) error {
	var node model.ProxySubscriptionNode
	if err := db.GetDB().WithContext(ctx).First(&node, nodeID).Error; err != nil {
		return fmt.Errorf("proxy subscription node not found")
	}
	updates := map[string]any{"user_enabled": enabled}
	if enabled {
		updates["quarantined_until"] = nil
		updates["last_runtime_error"] = ""
	}
	if err := db.GetDB().WithContext(ctx).Model(&node).Updates(updates).Error; err != nil {
		return err
	}
	invalidateProxySubscriptionCache(node.ProxyConfigurationID)
	if node.RuntimeType == model.ProxyNodeRuntimeSingBox {
		return ProxyRuntimeReload(ctx)
	}
	return nil
}

func ProxySubscriptionNodeTest(nodeID int, ctx context.Context) (model.ProxyTestResult, error) {
	var node model.ProxySubscriptionNode
	if err := db.GetDB().WithContext(ctx).First(&node, nodeID).Error; err != nil {
		return model.ProxyTestResult{}, fmt.Errorf("proxy subscription node not found")
	}
	var config model.ProxyConfiguration
	if err := db.GetDB().WithContext(ctx).First(&config, node.ProxyConfigurationID).Error; err != nil {
		return model.ProxyTestResult{}, fmt.Errorf("proxy configuration not found")
	}
	runtimeURL := node.URL
	if node.RuntimeType == model.ProxyNodeRuntimeSingBox {
		var ok bool
		runtimeURL, ok = singbox.Default.Resolve(node.NodeKey)
		if !ok {
			if err := ProxyRuntimeReload(ctx); err != nil {
				return model.ProxyTestResult{}, err
			}
			runtimeURL, ok = singbox.Default.Resolve(node.NodeKey)
			if !ok {
				return model.ProxyTestResult{}, fmt.Errorf("sing-box endpoint unavailable")
			}
		}
	}
	target := config.HealthCheckURL
	checks, err := testProxySubscriptionNodes(ctx, config.ID, []string{runtimeURL}, target)
	if err != nil {
		return model.ProxyTestResult{}, err
	}
	check := checks[0]
	result := model.ProxyTestResult{
		Success: check.HealthStatus == model.ProxyTestHealthHealthy, HealthStatus: check.HealthStatus,
		AverageDurationMS: check.LatencyMS, Message: check.LastError,
	}
	now := time.Now()
	updates := map[string]any{
		"health_status": result.HealthStatus, "latency_ms": result.AverageDurationMS,
		"last_checked_at": now, "last_error": truncateProxySubscriptionMessage(result.Message),
		"connectivity_checked": check.ConnectivityChecked, "connectivity_status": check.ConnectivityStatus,
		"connectivity_latency_ms": check.ConnectivityLatencyMS, "connectivity_last_error": check.ConnectivityLastError,
		"upstream_checked": check.UpstreamChecked, "upstream_url": check.UpstreamURL,
		"upstream_status": check.UpstreamStatus, "upstream_latency_ms": check.UpstreamLatencyMS,
		"upstream_last_error": check.UpstreamLastError,
	}
	if result.HealthStatus == model.ProxyTestHealthHealthy {
		updates["runtime_failure_count"] = 0
		updates["quarantined_until"] = nil
		updates["last_runtime_error"] = ""
		updates["exit_ip"] = check.ExitIP
		updates["exit_country"] = check.ExitCountry
		updates["exit_city"] = check.ExitCity
	}
	if err := db.GetDB().WithContext(ctx).Model(&node).Updates(updates).Error; err != nil {
		return model.ProxyTestResult{}, err
	}
	invalidateProxySubscriptionCache(node.ProxyConfigurationID)
	return result, nil
}

func ProxySubscriptionNodeModelProbe(nodeID int, input ProxyModelProbeRequest, ctx context.Context) (ProxyModelProbeResult, error) {
	input.URL = strings.TrimSpace(input.URL)
	input.Model = strings.TrimSpace(input.Model)
	if input.URL == "" || input.Model == "" || strings.TrimSpace(input.APIKey) == "" {
		return ProxyModelProbeResult{}, fmt.Errorf("model URL, model and API key are required")
	}
	if len(input.Model) > 191 || len(input.APIKey) > 4096 {
		return ProxyModelProbeResult{}, fmt.Errorf("model or API key is too long")
	}
	target, err := url.Parse(input.URL)
	if err != nil || (target.Scheme != "https" && target.Scheme != "http") || target.Hostname() == "" || target.User != nil || target.RawQuery != "" || target.Fragment != "" {
		return ProxyModelProbeResult{}, fmt.Errorf("model URL must be an http or https endpoint without credentials or query")
	}
	if err := proxyTestTargetHostSafe(target); err != nil {
		return ProxyModelProbeResult{}, fmt.Errorf("model URL host is not allowed")
	}
	var node model.ProxySubscriptionNode
	if err := db.GetDB().WithContext(ctx).First(&node, nodeID).Error; err != nil || !node.Active {
		return ProxyModelProbeResult{}, fmt.Errorf("active proxy subscription node not found")
	}
	proxyURL := node.URL
	if node.RuntimeType == model.ProxyNodeRuntimeSingBox {
		var found bool
		proxyURL, found = singbox.Default.Resolve(node.NodeKey)
		if !found {
			return ProxyModelProbeResult{}, fmt.Errorf("sing-box endpoint unavailable")
		}
	}
	client, err := newProxyTestHTTPClient(proxyURL)
	if err != nil {
		return ProxyModelProbeResult{}, err
	}
	client.Timeout = 30 * time.Second
	client.CheckRedirect = func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }
	body, err := json.Marshal(map[string]any{
		"model": input.Model, "messages": []map[string]string{{"role": "user", "content": "ping"}}, "max_tokens": 1, "stream": false,
	})
	if err != nil {
		return ProxyModelProbeResult{}, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, target.String(), bytes.NewReader(body))
	if err != nil {
		return ProxyModelProbeResult{}, err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+input.APIKey)
	request.Close = true
	started := time.Now()
	response, requestErr := client.Do(request)
	result := ProxyModelProbeResult{DurationMS: time.Since(started).Milliseconds(), Status: model.ProxyTestHealthFailed}
	if requestErr == nil {
		result.StatusCode = response.StatusCode
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
		_ = response.Body.Close()
		if response.StatusCode >= http.StatusOK && response.StatusCode < http.StatusMultipleChoices {
			result.Status = model.ProxyTestHealthHealthy
			result.Message = "model request succeeded"
		} else {
			result.Message = fmt.Sprintf("model request returned HTTP %d", response.StatusCode)
		}
	} else {
		result.Message = "model request failed to connect through proxy"
	}
	checkedAt := time.Now()
	if err := db.GetDB().WithContext(ctx).Model(&model.ProxySubscriptionNode{}).Where("id = ?", node.ID).Updates(map[string]any{
		"model_probe_url": sanitizedProxyUpstreamURL(target.String()), "model_probe_model": input.Model,
		"model_probe_status": result.Status, "model_probe_latency_ms": result.DurationMS,
		"model_probe_checked_at": checkedAt, "model_probe_last_error": result.Message,
	}).Error; err != nil {
		return result, err
	}
	return result, nil
}
