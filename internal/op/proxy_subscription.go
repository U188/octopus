package op

import (
	"bufio"
	"context"
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

var proxyRuntimeQuarantine = 10 * time.Minute

type proxySubscriptionCandidate struct {
	URL              string
	QuarantinedUntil *time.Time
}

var (
	proxySubscriptionNodeCache = cache.New[int, []proxySubscriptionCandidate](16)
	proxySubscriptionCounters  sync.Map
	proxySubscriptionSyncLocks sync.Map
	proxySubscriptionHealthURL = defaultProxyTestURL
)

type proxySubscriptionCounterKey struct {
	ConfigID int
	Scope    string
}

func ProxySubscriptionNodes(configID int, ctx context.Context) ([]model.ProxySubscriptionNode, error) {
	var nodes []model.ProxySubscriptionNode
	err := db.GetDB().WithContext(ctx).
		Where("proxy_configuration_id = ?", configID).
		Order("active DESC, health_status ASC, latency_ms ASC, id ASC").
		Find(&nodes).Error
	return nodes, err
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

// ProxyURLsForConfigStable returns the current latency-ordered healthy
// candidates without advancing a round-robin cursor. Subscription refreshes,
// health changes, or quarantine may still change which node is preferred.
func ProxyURLsForConfigStable(id int, ctx context.Context) ([]string, error) {
	return proxyURLsForConfig(id, "", false, ctx)
}

func proxyURLsForConfig(id int, scope string, rotate bool, ctx context.Context) ([]string, error) {
	item, err := proxyConfigurationForUse(id, ctx)
	if err != nil {
		return nil, err
	}
	if item.Type != model.ProxyConfigurationTypeSubscription {
		return []string{item.URL}, nil
	}
	urls, err := healthyProxySubscriptionURLs(id, ctx)
	if err != nil {
		return nil, err
	}
	if len(urls) == 0 {
		return nil, fmt.Errorf("proxy subscription has no available healthy nodes")
	}
	start := 0
	// A one-node subscription cannot rotate. Avoid allocating a permanent
	// cursor for it; this matters for installations that define many small
	// per-site subscriptions and also keeps invalidation work proportional to
	// actual rotation state.
	if rotate && len(urls) > 1 {
		counterKey := proxySubscriptionCounterKey{ConfigID: id, Scope: scope}
		counterValue, _ := proxySubscriptionCounters.LoadOrStore(counterKey, &atomic.Uint64{})
		counter := counterValue.(*atomic.Uint64)
		// Apply the modulo while the value is still uint64. Converting the
		// unbounded request counter to int first can produce a negative index
		// after the counter exceeds MaxInt on 32-bit (and eventually 64-bit)
		// builds.
		sequence := counter.Add(1)
		start = int((sequence - 1) % uint64(len(urls)))
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

func healthyProxySubscriptionURLs(id int, ctx context.Context) ([]string, error) {
	candidates, ok := proxySubscriptionNodeCache.Get(id)
	if !ok {
		if err := db.GetDB().WithContext(ctx).Model(&model.ProxySubscriptionNode{}).
			Select("url, quarantined_until").
			Where("proxy_configuration_id = ? AND active = ? AND health_status = ?", id, true, model.ProxyTestHealthHealthy).
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
		if candidateURL == "" {
			continue
		}
		if candidate.QuarantinedUntil != nil && candidate.QuarantinedUntil.After(now) {
			continue
		}
		urls = append(urls, candidateURL)
	}
	return urls, nil
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
}

func clearAllProxySubscriptionCounters() {
	proxySubscriptionCounters.Range(func(key, _ any) bool {
		proxySubscriptionCounters.Delete(key)
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
	normalizedURL, err := model.NormalizeProxyURL(proxyURL)
	if err != nil {
		return err
	}
	now := time.Now()
	quarantinedUntil := now.Add(proxyRuntimeQuarantine)
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

	urls, err := fetchProxySubscription(ctx, item.URL)
	if err != nil {
		markProxySubscriptionSyncFailed(configID, err, ctx)
		return model.ProxySubscriptionSyncResult{}, err
	}

	nodes, err := testProxySubscriptionNodes(ctx, configID, urls)
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
	if result.HealthyCount == 0 {
		syncErr := fmt.Errorf("subscription sync found no healthy nodes: %d degraded, %d failed", result.DegradedCount, result.FailedCount)
		markProxySubscriptionSyncFailed(configID, syncErr, ctx)
		return result, syncErr
	}

	message := fmt.Sprintf("fetched %d nodes: %d healthy, %d degraded, %d failed", result.FetchedCount, result.HealthyCount, result.DegradedCount, result.FailedCount)
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
					"active", "health_status", "latency_ms", "last_checked_at", "last_error",
					"runtime_failure_count", "quarantined_until", "last_runtime_failure_at", "last_runtime_error", "updated_at",
				}),
			}).Create(&node).Error; err != nil {
				return err
			}
		}
		return tx.Model(&model.ProxyConfiguration{}).Where("id = ?", configID).Updates(map[string]any{
			"last_sync_at":      syncedAt,
			"last_sync_status":  model.ProxySubscriptionSyncSuccess,
			"last_sync_message": message,
		}).Error
	}); err != nil {
		markProxySubscriptionSyncFailed(configID, err, ctx)
		return model.ProxySubscriptionSyncResult{}, fmt.Errorf("save proxy subscription: %w", err)
	}

	proxyConfigurationCache.Del(configID)
	invalidateProxySubscriptionCache(configID)
	return result, nil
}

func fetchProxySubscription(ctx context.Context, rawURL string) ([]string, error) {
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
	return parseProxySubscription(string(data))
}

func parseProxySubscription(content string) ([]string, error) {
	seen := make(map[string]struct{})
	urls := make([]string, 0)
	scanner := bufio.NewScanner(strings.NewReader(content))
	scanner.Buffer(make([]byte, 4096), 64*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}
		if comment := strings.IndexAny(line, " \t#;"); comment >= 0 {
			line = strings.TrimSpace(line[:comment])
		}
		normalized, err := model.NormalizeProxyURL(line)
		if err != nil {
			continue
		}
		if _, exists := seen[normalized]; exists {
			continue
		}
		if len(urls) >= proxySubscriptionMaxNodes {
			return nil, fmt.Errorf("subscription exceeds %d proxy nodes", proxySubscriptionMaxNodes)
		}
		seen[normalized] = struct{}{}
		urls = append(urls, normalized)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("parse subscription: %w", err)
	}
	if len(urls) == 0 {
		return nil, fmt.Errorf("subscription contains no valid proxy nodes")
	}
	sort.Strings(urls)
	return urls, nil
}

func testProxySubscriptionNodes(ctx context.Context, configID int, urls []string) ([]model.ProxySubscriptionNode, error) {
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
				result := testNormalizedProxyURL(ctx, urls[index], proxySubscriptionHealthURL)
				nodes[index] = model.ProxySubscriptionNode{
					ProxyConfigurationID: configID,
					URL:                  urls[index],
					Active:               true,
					HealthStatus:         result.HealthStatus,
					LatencyMS:            result.AverageDurationMS,
					LastError:            truncateProxySubscriptionMessage(result.Message),
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
