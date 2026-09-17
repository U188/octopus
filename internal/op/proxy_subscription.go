package op

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/U188/octopus/internal/db"
	"github.com/U188/octopus/internal/model"
	"github.com/U188/octopus/internal/outboundurl"
	"github.com/U188/octopus/internal/singbox"
	"github.com/U188/octopus/internal/utils/cache"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	proxySubscriptionMaxBytes   = 1 << 20
	proxySubscriptionMaxNodes   = 2000
	proxySubscriptionWorkers    = 8
	proxySubscriptionFetchLimit = 30 * time.Second
	proxyRequestCandidateLimit  = 4
)

var proxyRuntimeQuarantine = time.Minute

type proxySubscriptionCandidate struct {
	URL              string
	NodeKey          string
	RuntimeType      model.ProxyNodeRuntimeType
	QuarantinedUntil *time.Time
	RuntimeFailures  int
}

var (
	proxySubscriptionNodeCache = cache.New[int, []proxySubscriptionCandidate](16)
	proxySubscriptionCounters  sync.Map
	proxySubscriptionSyncLocks sync.Map
	proxySubscriptionSticky    sync.Map
	proxySubscriptionHalfOpen  sync.Map
	proxySubscriptionHealthURL = defaultProxyTestURL
	proxyExitLookupURL         = "http://ip-api.com/json/?fields=status,countryCode,city,query"
)

type proxySubscriptionCounterKey struct {
	ConfigID int
	Scope    string
}

type proxySubscriptionStickyEntry struct {
	URL string
}

func ProxySubscriptionNodes(configID int, ctx context.Context) ([]model.ProxySubscriptionNodeView, error) {
	var nodes []model.ProxySubscriptionNode
	err := db.GetDB().WithContext(ctx).
		Where("proxy_configuration_id = ?", configID).
		Order("active DESC, health_status ASC, latency_ms ASC, id ASC").
		Find(&nodes).Error
	if err != nil {
		return nil, err
	}
	views := make([]model.ProxySubscriptionNodeView, 0, len(nodes))
	for _, node := range nodes {
		views = append(views, node.View())
	}
	return views, nil
}

func ProxyURLsForConfig(id int, ctx context.Context) ([]string, error) {
	return ProxyURLsForConfigScoped(id, "", ctx)
}

// ProxyURLsForConfigScoped returns latency-ordered healthy proxy candidates
// with a round-robin cursor isolated to the supplied request scope. Callers
// that share one proxy configuration can therefore rotate independently.
func ProxyURLsForConfigScoped(id int, scope string, ctx context.Context) ([]string, error) {
	return proxyURLsForConfig(id, strings.TrimSpace(scope), true, ctx)
}

func ProxyURLsForConfigPolicy(id int, scope string, forceRoundRobin bool, ctx context.Context) ([]string, error) {
	return proxyURLsForConfig(id, strings.TrimSpace(scope), forceRoundRobin, ctx)
}

func ProxyPolicyForConfig(id int, ctx context.Context) (model.ProxyConfigurationType, model.ProxySelectionStrategy, error) {
	item, err := proxyConfigurationForUse(id, ctx)
	if err != nil {
		return "", "", err
	}
	strategy := item.SelectionStrategy
	if strategy == "" {
		strategy = model.ProxySelectionLatency
	}
	return item.Type, strategy, nil
}

// ProxyURLsForConfigStable returns the current latency-ordered healthy
// candidates without advancing a round-robin cursor. Subscription refreshes,
// health changes, or quarantine may still change which node is preferred.
func ProxyURLsForConfigStable(id int, ctx context.Context) ([]string, error) {
	item, err := proxyConfigurationForUse(id, ctx)
	if err != nil {
		return nil, err
	}
	if item.Type != model.ProxyConfigurationTypeSubscription {
		return []string{item.URL}, nil
	}
	urls, err := healthyProxySubscriptionURLs(id, false, ctx)
	if err != nil {
		return nil, err
	}
	if len(urls) == 0 {
		return nil, fmt.Errorf("proxy subscription has no available healthy nodes")
	}
	if len(urls) > proxyRequestCandidateLimit {
		urls = urls[:proxyRequestCandidateLimit]
	}
	return urls, nil
}

func proxyURLsForConfig(id int, scope string, rotate bool, ctx context.Context) ([]string, error) {
	item, err := proxyConfigurationForUse(id, ctx)
	if err != nil {
		return nil, err
	}
	if item.Type != model.ProxyConfigurationTypeSubscription {
		return []string{item.URL}, nil
	}
	urls, err := healthyProxySubscriptionURLs(id, true, ctx)
	if err != nil {
		return nil, err
	}
	if len(urls) == 0 {
		return nil, fmt.Errorf("proxy subscription has no available healthy nodes")
	}
	strategy := item.SelectionStrategy
	if strategy == "" {
		strategy = model.ProxySelectionLatency
	}
	if rotate {
		strategy = model.ProxySelectionRoundRobin
	}
	start := 0
	// A one-node subscription cannot rotate. Avoid allocating a permanent
	// cursor for it; this matters for installations that define many small
	// per-site subscriptions and also keeps invalidation work proportional to
	// actual rotation state.
	if strategy == model.ProxySelectionRoundRobin && len(urls) > 1 {
		counterKey := proxySubscriptionCounterKey{ConfigID: id, Scope: scope}
		counterValue, _ := proxySubscriptionCounters.LoadOrStore(counterKey, &atomic.Uint64{})
		counter := counterValue.(*atomic.Uint64)
		// Apply the modulo while the value is still uint64. Converting the
		// unbounded request counter to int first can produce a negative index
		// after the counter exceeds MaxInt on 32-bit (and eventually 64-bit)
		// builds.
		sequence := counter.Add(1)
		start = int((sequence - 1) % uint64(len(urls)))
	} else if strategy == model.ProxySelectionSticky && scope != "" {
		stickyKey := proxySubscriptionCounterKey{ConfigID: id, Scope: scope}
		if value, ok := proxySubscriptionSticky.Load(stickyKey); ok {
			stickyURL := value.(proxySubscriptionStickyEntry).URL
			found := false
			for index, candidateURL := range urls {
				if candidateURL == stickyURL {
					start = index
					found = true
					break
				}
			}
			if !found {
				proxySubscriptionSticky.Store(stickyKey, proxySubscriptionStickyEntry{URL: urls[0]})
			}
		} else {
			proxySubscriptionSticky.Store(stickyKey, proxySubscriptionStickyEntry{URL: urls[0]})
		}
	}
	limit := len(urls)
	if limit > proxyRequestCandidateLimit {
		limit = proxyRequestCandidateLimit
	}
	rotated := make([]string, 0, limit)
	for offset := 0; offset < limit; offset++ {
		rotated = append(rotated, urls[(start+offset)%len(urls)])
	}
	return rotated, nil
}

func healthyProxySubscriptionURLs(id int, claimHalfOpenLease bool, ctx context.Context) ([]string, error) {
	candidates, ok := proxySubscriptionNodeCache.Get(id)
	if !ok {
		if err := db.GetDB().WithContext(ctx).Model(&model.ProxySubscriptionNode{}).
			Select("url, node_key, runtime_type, quarantined_until, runtime_failure_count AS runtime_failures").
			Where("proxy_configuration_id = ? AND active = ? AND user_enabled = ? AND health_status = ? AND conversion_status IN ?", id, true, true, model.ProxyTestHealthHealthy, []model.ProxyNodeConversionStatus{model.ProxyNodeConversionDirect, model.ProxyNodeConversionReady}).
			Order("latency_ms ASC, id ASC").
			Scan(&candidates).Error; err != nil {
			return nil, err
		}
		proxySubscriptionNodeCache.Set(id, candidates)
	}
	now := time.Now()
	urls := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		candidateURL := strings.TrimSpace(candidate.URL)
		if candidate.RuntimeType == model.ProxyNodeRuntimeSingBox {
			runtimeURL, found := singbox.Default.Resolve(candidate.NodeKey)
			if !found {
				continue
			}
			candidateURL = runtimeURL
		}
		if candidateURL == "" {
			continue
		}
		if candidate.QuarantinedUntil != nil && candidate.QuarantinedUntil.After(now) {
			continue
		}
		if candidate.RuntimeFailures > 0 {
			if !claimHalfOpenLease || !claimProxyHalfOpen(id, candidate.URL, now) {
				if claimHalfOpenLease {
					continue
				}
			}
		}
		urls = append(urls, candidateURL)
	}
	return urls, nil
}

type proxyHalfOpenLease struct {
	ClaimedAt time.Time
}

func claimProxyHalfOpen(configID int, logicalURL string, now time.Time) bool {
	key := proxySubscriptionCounterKey{ConfigID: configID, Scope: logicalURL}
	lease := proxyHalfOpenLease{ClaimedAt: now}
	current, loaded := proxySubscriptionHalfOpen.LoadOrStore(key, lease)
	if !loaded {
		return true
	}
	existing, ok := current.(proxyHalfOpenLease)
	if ok && now.Sub(existing.ClaimedAt) < 30*time.Second {
		return false
	}
	return proxySubscriptionHalfOpen.CompareAndSwap(key, current, lease)
}

func releaseProxyHalfOpen(configID int, logicalURL string) {
	proxySubscriptionHalfOpen.Delete(proxySubscriptionCounterKey{ConfigID: configID, Scope: logicalURL})
}

func invalidateProxySubscriptionCache(id int) {
	proxySubscriptionNodeCache.Del(id)
	clearProxySubscriptionCounters(id)
}

func clearProxySubscriptionCounters(id int) {
	if id <= 0 {
		return
	}
	proxySubscriptionCounters.Range(func(key, _ any) bool {
		counterKey, ok := key.(proxySubscriptionCounterKey)
		if ok && counterKey.ConfigID == id {
			proxySubscriptionCounters.Delete(key)
		}
		return true
	})
	proxySubscriptionSticky.Range(func(key, _ any) bool {
		stickyKey, ok := key.(proxySubscriptionCounterKey)
		if ok && stickyKey.ConfigID == id {
			proxySubscriptionSticky.Delete(key)
		}
		return true
	})
}

func clearProxySubscriptionHalfOpen(id int) {
	proxySubscriptionHalfOpen.Range(func(key, _ any) bool {
		halfOpenKey, ok := key.(proxySubscriptionCounterKey)
		if ok && halfOpenKey.ConfigID == id {
			proxySubscriptionHalfOpen.Delete(key)
		}
		return true
	})
}

func clearAllProxySubscriptionCounters() {
	proxySubscriptionCounters.Range(func(key, _ any) bool {
		proxySubscriptionCounters.Delete(key)
		return true
	})
	proxySubscriptionSticky.Range(func(key, _ any) bool {
		proxySubscriptionSticky.Delete(key)
		return true
	})
	proxySubscriptionHalfOpen.Range(func(key, _ any) bool {
		proxySubscriptionHalfOpen.Delete(key)
		return true
	})
}

func forgetProxySubscriptionState(id int) {
	invalidateProxySubscriptionCache(id)
}

func proxySubscriptionLock(id int) *sync.Mutex {
	lockValue, _ := proxySubscriptionSyncLocks.LoadOrStore(id, &sync.Mutex{})
	return lockValue.(*sync.Mutex)
}

func ProxySubscriptionNodeReportFailure(configID int, proxyURL string, failure error, ctx context.Context) error {
	if configID <= 0 || failure == nil {
		return nil
	}
	normalizedURL := proxyLogicalURL(proxyURL)
	releaseProxyHalfOpen(configID, normalizedURL)
	now := time.Now()
	var node model.ProxySubscriptionNode
	if err := db.GetDB().WithContext(ctx).Where("proxy_configuration_id = ? AND url = ? AND active = ?", configID, normalizedURL, true).First(&node).Error; err != nil {
		return nil
	}
	backoff := proxyRuntimeQuarantine
	for i := 0; i < node.RuntimeFailureCount && backoff < 30*time.Minute; i++ {
		backoff *= 2
	}
	if backoff > 30*time.Minute {
		backoff = 30 * time.Minute
	}
	quarantinedUntil := now.Add(backoff)
	persistCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	result := db.GetDB().WithContext(persistCtx).Model(&model.ProxySubscriptionNode{}).
		Where("proxy_configuration_id = ? AND url = ? AND active = ?", configID, normalizedURL, true).
		Updates(map[string]any{
			"runtime_failure_count":   gorm.Expr("runtime_failure_count + ?", 1),
			"quarantined_until":       quarantinedUntil,
			"last_runtime_failure_at": now,
			"last_runtime_error":      sanitizeProxyRuntimeError(failure.Error(), normalizedURL),
		})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected > 0 {
		invalidateProxySubscriptionCache(configID)
	}
	return nil
}

func ProxySubscriptionNodeReportSuccess(configID int, proxyURL string, ctx context.Context) error {
	if configID <= 0 {
		return nil
	}
	normalizedURL := proxyLogicalURL(proxyURL)
	releaseProxyHalfOpen(configID, normalizedURL)
	persistCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	result := db.GetDB().WithContext(persistCtx).Model(&model.ProxySubscriptionNode{}).
		Where("proxy_configuration_id = ? AND url = ? AND active = ? AND runtime_failure_count > 0", configID, normalizedURL, true).
		Updates(map[string]any{"runtime_failure_count": 0, "quarantined_until": nil, "last_runtime_error": ""})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected > 0 {
		invalidateProxySubscriptionCache(configID)
	}
	return nil
}

func proxyLogicalURL(runtimeURL string) string {
	normalizedURL, err := model.NormalizeProxyURL(runtimeURL)
	if err != nil {
		return runtimeURL
	}
	var node model.ProxySubscriptionNode
	if err := db.GetDB().Where("url = ?", normalizedURL).First(&node).Error; err == nil {
		return node.URL
	}
	var nodes []model.ProxySubscriptionNode
	if err := db.GetDB().Where("runtime_type = ? AND active = ?", model.ProxyNodeRuntimeSingBox, true).Find(&nodes).Error; err == nil {
		for _, candidate := range nodes {
			if endpoint, ok := singbox.Default.Resolve(candidate.NodeKey); ok && endpoint == normalizedURL {
				return candidate.URL
			}
		}
	}
	return normalizedURL
}

func ProxySubscriptionSync(configID int, ctx context.Context) (model.ProxySubscriptionSyncResult, error) {
	lock := proxySubscriptionLock(configID)
	lock.Lock()
	defer lock.Unlock()

	item, err := ProxyConfigurationGet(configID, ctx)
	if err != nil {
		return model.ProxySubscriptionSyncResult{}, fmt.Errorf("proxy configuration not found")
	}
	if item.Type != model.ProxyConfigurationTypeSubscription {
		return model.ProxySubscriptionSyncResult{}, fmt.Errorf("proxy configuration is not a subscription")
	}

	candidates, err := fetchProxySubscription(ctx, item.URL)
	if err != nil {
		markProxySubscriptionSyncFailed(configID, err, ctx)
		return model.ProxySubscriptionSyncResult{}, err
	}

	nodes, err := prepareProxySubscriptionNodes(ctx, configID, candidates, item.HealthCheckURL)
	if err != nil {
		markProxySubscriptionSyncFailed(configID, err, ctx)
		return model.ProxySubscriptionSyncResult{}, err
	}
	syncedAt := time.Now()
	result := model.ProxySubscriptionSyncResult{
		ProxyConfigurationID: configID,
		FetchedCount:         len(nodes),
		SyncedAt:             syncedAt,
	}
	for i := range nodes {
		nodes[i].LastCheckedAt = &syncedAt
		switch nodes[i].HealthStatus {
		case model.ProxyTestHealthHealthy:
			result.HealthyCount++
		case model.ProxyTestHealthDegraded:
			result.DegradedCount++
		default:
			result.FailedCount++
		}
	}
	message := fmt.Sprintf("fetched %d nodes: %d healthy, %d degraded, %d failed", result.FetchedCount, result.HealthyCount, result.DegradedCount, result.FailedCount)
	syncStatus := model.ProxySubscriptionSyncSuccess
	var syncErr error
	if result.HealthyCount == 0 {
		syncStatus = model.ProxySubscriptionSyncFailed
		syncErr = fmt.Errorf("subscription sync found no healthy nodes: %d degraded, %d failed", result.DegradedCount, result.FailedCount)
		message = syncErr.Error()
		var lastKnownGood int64
		if err := db.GetDB().WithContext(ctx).Model(&model.ProxySubscriptionNode{}).
			Where("proxy_configuration_id = ? AND active = ? AND user_enabled = ? AND health_status = ?", configID, true, true, model.ProxyTestHealthHealthy).
			Count(&lastKnownGood).Error; err != nil {
			return result, err
		}
		if lastKnownGood > 0 {
			markProxySubscriptionSyncFailed(configID, syncErr, ctx)
			return result, syncErr
		}
	}
	database := db.GetDB().WithContext(ctx)
	if err := database.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&model.ProxySubscriptionNode{}).
			Where("proxy_configuration_id = ?", configID).
			Update("active", false).Error; err != nil {
			return err
		}
		for i := range nodes {
			node := nodes[i]
			if err := tx.Clauses(clause.OnConflict{
				Columns: []clause.Column{{Name: "proxy_configuration_id"}, {Name: "url"}},
				DoUpdates: clause.AssignmentColumns([]string{
					"active", "node_key", "name", "display_address", "protocol", "runtime_type", "config_json", "conversion_status",
					"health_status", "latency_ms", "last_checked_at", "last_error", "exit_ip", "exit_country", "exit_city",
					"connectivity_checked", "connectivity_status", "connectivity_latency_ms", "connectivity_last_error",
					"upstream_checked", "upstream_url", "upstream_status", "upstream_latency_ms", "upstream_last_error",
					"runtime_failure_count", "quarantined_until", "last_runtime_failure_at", "last_runtime_error", "updated_at",
				}),
			}).Create(&node).Error; err != nil {
				return err
			}
		}
		return tx.Model(&model.ProxyConfiguration{}).Where("id = ?", configID).Updates(map[string]any{
			"last_sync_at":      syncedAt,
			"last_sync_status":  syncStatus,
			"last_sync_message": message,
		}).Error
	}); err != nil {
		markProxySubscriptionSyncFailed(configID, err, ctx)
		return model.ProxySubscriptionSyncResult{}, fmt.Errorf("save proxy subscription: %w", err)
	}
	proxyConfigurationCache.Del(configID)
	invalidateProxySubscriptionCache(configID)
	clearProxySubscriptionHalfOpen(configID)
	runtimeCtx, cancelRuntime := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	runtimeErr := ProxyRuntimeReload(runtimeCtx)
	cancelRuntime()
	if runtimeErr != nil {
		runtimeErr = fmt.Errorf("activate proxy subscription runtime: %w", runtimeErr)
		markProxySubscriptionSyncFailed(configID, runtimeErr, ctx)
		return result, runtimeErr
	}

	return result, syncErr
}

func fetchProxySubscription(ctx context.Context, rawURL string) ([]parsedProxySubscriptionNode, error) {
	if err := outboundurl.ValidateHTTPURL(rawURL); err != nil {
		return nil, fmt.Errorf("invalid subscription url: %w", err)
	}
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("parse subscription url: %w", err)
	}
	if err := outboundurl.ValidateHTTPURLContext(ctx, parsed); err != nil {
		return nil, fmt.Errorf("validate subscription url: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "text/plain")
	req.Header.Set("User-Agent", "Octopus Proxy Subscription Sync")
	resp, err := outboundurl.NewDirectClient(proxySubscriptionFetchLimit).Do(req)
	if err != nil {
		directErr := err
		storedProxyURL, settingErr := SettingGetString(model.SettingKeyProxyURL)
		if settingErr != nil || strings.TrimSpace(storedProxyURL) == "" {
			return nil, fmt.Errorf("fetch subscription directly: %w", directErr)
		}
		normalizedProxyURL, normalizeErr := model.NormalizeProxyURL(storedProxyURL)
		if normalizeErr != nil {
			return nil, fmt.Errorf("fetch subscription directly: %w", directErr)
		}
		proxyClient, clientErr := newProxyTestHTTPClient(normalizedProxyURL)
		if clientErr != nil {
			return nil, fmt.Errorf("fetch subscription directly: %w", directErr)
		}
		proxyClient.Timeout = proxySubscriptionFetchLimit
		retryReq := req.Clone(ctx)
		resp, err = proxyClient.Do(retryReq)
		if err != nil {
			return nil, fmt.Errorf("fetch subscription directly and through system proxy: %w", err)
		}
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("fetch subscription: unexpected HTTP status %d", resp.StatusCode)
	}
	limited := io.LimitReader(resp.Body, proxySubscriptionMaxBytes+1)
	data, err := io.ReadAll(limited)
	if err != nil {
		return nil, fmt.Errorf("read subscription: %w", err)
	}
	if len(data) > proxySubscriptionMaxBytes {
		return nil, fmt.Errorf("subscription exceeds %d bytes", proxySubscriptionMaxBytes)
	}
	return parseProxySubscriptionNodes(string(data))
}

func parseProxySubscription(content string) ([]string, error) {
	nodes, err := parseProxySubscriptionNodes(content)
	if err != nil {
		return nil, err
	}
	urls := make([]string, 0, len(nodes))
	for _, node := range nodes {
		urls = append(urls, node.URL)
	}
	sort.Strings(urls)
	return urls, nil
}

func prepareProxySubscriptionNodes(ctx context.Context, configID int, candidates []parsedProxySubscriptionNode, healthURL string) ([]model.ProxySubscriptionNode, error) {
	if healthURL == "" {
		var err error
		healthURL, err = proxyReferencedUpstreamURL(ctx, configID)
		if err != nil {
			return nil, err
		}
	}
	encrypted := make([]singbox.Node, 0)
	for _, candidate := range candidates {
		if candidate.RuntimeType == model.ProxyNodeRuntimeSingBox {
			encrypted = append(encrypted, singbox.Node{Key: candidate.NodeKey, Protocol: candidate.Protocol, ConfigJSON: candidate.ConfigJSON, Display: candidate.DisplayValue})
		}
	}
	converted := map[string]string{}
	var conversionErr error
	if len(encrypted) > 0 {
		var cleanup func()
		converted, cleanup, conversionErr = singbox.Default.Probe(ctx, encrypted)
		defer cleanup()
	}

	urls := make([]string, len(candidates))
	for index, candidate := range candidates {
		urls[index] = candidate.URL
		if candidate.RuntimeType == model.ProxyNodeRuntimeSingBox {
			urls[index] = converted[candidate.NodeKey]
		}
	}
	results, err := testProxySubscriptionNodes(ctx, configID, urls, healthURL)
	if err != nil {
		return nil, err
	}
	for index, candidate := range candidates {
		results[index].URL = candidate.URL
		results[index].NodeKey = candidate.NodeKey
		results[index].Name = candidate.Name
		results[index].DisplayAddress = candidate.DisplayValue
		results[index].Protocol = candidate.Protocol
		results[index].RuntimeType = candidate.RuntimeType
		results[index].ConfigJSON = candidate.ConfigJSON
		results[index].UserEnabled = true
		if candidate.RuntimeType == model.ProxyNodeRuntimeDirect {
			results[index].ConversionStatus = model.ProxyNodeConversionDirect
		} else if endpoint := converted[candidate.NodeKey]; endpoint != "" {
			results[index].ConversionStatus = model.ProxyNodeConversionReady
		} else if !singbox.Default.Enabled() {
			results[index].ConversionStatus = model.ProxyNodeConversionPending
			results[index].HealthStatus = model.ProxyTestHealthFailed
			results[index].LastError = "sing-box is disabled"
		} else {
			results[index].ConversionStatus = model.ProxyNodeConversionFailed
			results[index].HealthStatus = model.ProxyTestHealthFailed
			if conversionErr != nil {
				results[index].LastError = truncateProxySubscriptionMessage(conversionErr.Error())
			} else {
				results[index].LastError = "sing-box endpoint unavailable"
			}
		}
	}
	exitOwners := make(map[string]int)
	for index := range results {
		if results[index].HealthStatus != model.ProxyTestHealthHealthy || results[index].ExitIP == "" {
			continue
		}
		if owner, exists := exitOwners[results[index].ExitIP]; exists {
			results[index].HealthStatus = model.ProxyTestHealthFailed
			results[index].LastError = fmt.Sprintf("duplicate public exit IP with node %s", results[owner].Name)
			continue
		}
		exitOwners[results[index].ExitIP] = index
	}
	return results, nil
}

func proxySingBoxNodes(ctx context.Context, excludingConfigID int, current []singbox.Node) ([]singbox.Node, error) {
	var persisted []model.ProxySubscriptionNode
	query := db.GetDB().WithContext(ctx).
		Joins("JOIN proxy_configurations ON proxy_configurations.id = proxy_subscription_nodes.proxy_configuration_id AND proxy_configurations.enabled = ?", true).
		Where("proxy_subscription_nodes.runtime_type = ? AND proxy_subscription_nodes.active = ? AND proxy_subscription_nodes.user_enabled = ?", model.ProxyNodeRuntimeSingBox, true, true)
	if excludingConfigID > 0 {
		query = query.Where("proxy_subscription_nodes.proxy_configuration_id <> ?", excludingConfigID)
	}
	if err := query.Find(&persisted).Error; err != nil {
		return nil, err
	}
	nodes := make([]singbox.Node, 0, len(persisted)+len(current))
	seen := make(map[string]struct{}, len(persisted)+len(current))
	for _, node := range persisted {
		if node.NodeKey == "" || node.ConfigJSON == "" {
			continue
		}
		nodes = append(nodes, singbox.Node{Key: node.NodeKey, Protocol: node.Protocol, ConfigJSON: node.ConfigJSON, Display: node.DisplayAddress})
		seen[node.NodeKey] = struct{}{}
	}
	for _, node := range current {
		if _, ok := seen[node.Key]; ok {
			continue
		}
		nodes = append(nodes, node)
	}
	return nodes, nil
}

func proxyReferencedUpstreamURL(ctx context.Context, configID int) (string, error) {
	var site model.Site
	if err := db.GetDB().WithContext(ctx).Select("base_url").
		Where("proxy_mode = ? AND proxy_config_id = ? AND enabled = ?", model.ProxyUsageModePool, configID, true).
		Order("id ASC").Limit(1).Find(&site).Error; err != nil {
		return "", err
	}
	if site.BaseURL != "" {
		return site.BaseURL, nil
	}
	var channel model.Channel
	if err := db.GetDB().WithContext(ctx).Select("base_urls").
		Where("proxy_mode = ? AND proxy_config_id = ? AND enabled = ?", model.ProxyUsageModePool, configID, true).
		Order("id ASC").Limit(1).Find(&channel).Error; err != nil {
		return "", err
	}
	return channel.GetBaseUrl(), nil
}

func proxyUpstreamProbe(ctx context.Context, proxyURL, targetURL string) model.ProxyTestAttemptResult {
	parsed, err := url.Parse(targetURL)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Hostname() == "" {
		return model.ProxyTestAttemptResult{Message: "invalid upstream health URL"}
	}
	if err := proxyTestTargetHostSafe(parsed); err != nil {
		return model.ProxyTestAttemptResult{Message: "upstream health URL is not publicly reachable"}
	}
	result := runProxyTestAttempt(ctx, proxyURL, targetURL, 0, 1)
	if result.StatusCode == http.StatusUnauthorized || result.StatusCode == http.StatusForbidden || result.StatusCode == http.StatusNotFound {
		result.Success = true
		result.Message = "upstream responded; authentication or route required"
	}
	return result
}

func sanitizedProxyUpstreamURL(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return (&url.URL{Scheme: parsed.Scheme, Host: parsed.Host, Path: parsed.Path}).String()
}

func testProxySubscriptionNodes(ctx context.Context, configID int, urls []string, healthURL ...string) ([]model.ProxySubscriptionNode, error) {
	nodes := make([]model.ProxySubscriptionNode, len(urls))
	jobs := make(chan int)
	workers := proxySubscriptionWorkers
	if len(urls) < workers {
		workers = len(urls)
	}
	var wg sync.WaitGroup
	for worker := 0; worker < workers; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for index := range jobs {
				if strings.TrimSpace(urls[index]) == "" {
					nodes[index] = model.ProxySubscriptionNode{ProxyConfigurationID: configID, Active: true, HealthStatus: model.ProxyTestHealthFailed, UserEnabled: true, LastError: "proxy runtime endpoint unavailable"}
					continue
				}
				target := proxySubscriptionHealthURL
				customTarget := len(healthURL) > 0 && strings.TrimSpace(healthURL[0]) != "" && healthURL[0] != proxySubscriptionHealthURL
				if customTarget {
					target = strings.TrimSpace(healthURL[0])
				}
				var result model.ProxyTestResult
				if customTarget {
					attempts := make([]model.ProxyTestAttemptResult, 0, proxyTestAttemptCount)
					startedAt := time.Now()
					for attempt := 0; attempt < proxyTestAttemptCount; attempt++ {
						attempts = append(attempts, proxyUpstreamProbe(ctx, urls[index], target))
						if attempt+1 < proxyTestAttemptCount && !waitProxyTestAttempt(ctx) {
							break
						}
					}
					result = summarizeProxyTestResult(startedAt, attempts, proxyTestAttemptCount)
				} else {
					result = testNormalizedProxyURL(ctx, urls[index], target)
				}
				nodes[index] = model.ProxySubscriptionNode{
					ProxyConfigurationID:  configID,
					URL:                   urls[index],
					Active:                true,
					UserEnabled:           true,
					HealthStatus:          result.HealthStatus,
					LatencyMS:             result.AverageDurationMS,
					LastError:             truncateProxySubscriptionMessage(result.Message),
					ConnectivityChecked:   true,
					ConnectivityStatus:    result.HealthStatus,
					ConnectivityLatencyMS: result.AverageDurationMS,
					ConnectivityLastError: truncateProxySubscriptionMessage(result.Message),
				}
				if customTarget {
					nodes[index].ConnectivityChecked = false
					nodes[index].ConnectivityStatus = ""
					nodes[index].ConnectivityLatencyMS = 0
					nodes[index].ConnectivityLastError = ""
					nodes[index].UpstreamChecked = true
					nodes[index].UpstreamURL = sanitizedProxyUpstreamURL(target)
					nodes[index].UpstreamLatencyMS = result.AverageDurationMS
					nodes[index].UpstreamStatus = result.HealthStatus
					nodes[index].UpstreamLastError = nodes[index].LastError
				}
				if nodes[index].HealthStatus == model.ProxyTestHealthHealthy {
					nodes[index].ExitIP, nodes[index].ExitCountry, nodes[index].ExitCity = detectProxyExit(ctx, urls[index])
				}
			}
		}()
	}
	for index := range urls {
		select {
		case <-ctx.Done():
			close(jobs)
			wg.Wait()
			return nil, ctx.Err()
		case jobs <- index:
		}
	}
	close(jobs)
	wg.Wait()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return nodes, nil
}

func detectProxyExit(ctx context.Context, proxyURL string) (string, string, string) {
	client, err := newProxyTestHTTPClient(proxyURL)
	if err != nil {
		return "", "", ""
	}
	client.Timeout = 8 * time.Second
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, proxyExitLookupURL, nil)
	if err != nil {
		return "", "", ""
	}
	response, err := client.Do(request)
	if err != nil {
		return "", "", ""
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return "", "", ""
	}
	var payload struct {
		Status      string `json:"status"`
		Query       string `json:"query"`
		CountryCode string `json:"countryCode"`
		City        string `json:"city"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 64<<10)).Decode(&payload); err != nil || payload.Status != "success" {
		return "", "", ""
	}
	return strings.TrimSpace(payload.Query), strings.ToUpper(strings.TrimSpace(payload.CountryCode)), strings.TrimSpace(payload.City)
}

func markProxySubscriptionSyncFailed(configID int, syncErr error, ctx context.Context) {
	now := time.Now()
	persistCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	_ = db.GetDB().WithContext(persistCtx).Model(&model.ProxyConfiguration{}).Where("id = ?", configID).Updates(map[string]any{
		"last_sync_at":      now,
		"last_sync_status":  model.ProxySubscriptionSyncFailed,
		"last_sync_message": truncateProxySubscriptionMessage(syncErr.Error()),
	}).Error
	proxyConfigurationCache.Del(configID)
}

func truncateProxySubscriptionMessage(message string) string {
	message = strings.TrimSpace(message)
	if len(message) <= 500 {
		return message
	}
	return message[:500]
}

func sanitizeProxyRuntimeError(message string, proxyURL string) string {
	message = strings.ReplaceAll(message, proxyURL, "[proxy]")
	if parsed, err := url.Parse(proxyURL); err == nil && parsed.User != nil {
		message = strings.ReplaceAll(message, parsed.User.String(), "[credentials]")
	}
	return truncateProxySubscriptionMessage(message)
}

func ProxySubscriptionsSyncDue(ctx context.Context, now time.Time) (int, error) {
	var items []model.ProxyConfiguration
	if err := db.GetDB().WithContext(ctx).
		Where("type = ? AND enabled = ?", model.ProxyConfigurationTypeSubscription, true).
		Order("id ASC").Find(&items).Error; err != nil {
		return 0, err
	}
	synced := 0
	var syncErrors []error
	for i := range items {
		interval := items[i].RefreshIntervalMinutes
		if interval <= 0 {
			interval = model.DefaultProxySubscriptionRefreshMinutes
		}
		if items[i].LastSyncAt != nil && items[i].LastSyncAt.Add(time.Duration(interval)*time.Minute).After(now) {
			continue
		}
		if _, err := ProxySubscriptionSync(items[i].ID, ctx); err != nil {
			syncErrors = append(syncErrors, fmt.Errorf("subscription %d: %w", items[i].ID, err))
			continue
		}
		synced++
	}
	return synced, errors.Join(syncErrors...)
}
