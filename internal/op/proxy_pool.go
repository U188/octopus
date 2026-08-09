package op

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/U188/octopus/internal/db"
	"github.com/U188/octopus/internal/model"
	"github.com/U188/octopus/internal/outboundurl"
	"github.com/U188/octopus/internal/utils/cache"
	"gorm.io/gorm"
)

const (
	defaultProxyTestURL     = "https://www.google.com/generate_204"
	proxyTestAttemptCount   = 3
	proxyTestAttemptTimeout = 20 * time.Second
	proxyTestAttemptDelay   = 250 * time.Millisecond
)

var proxyConfigurationCache = cache.New[int, model.ProxyConfiguration](16)

func ProxyConfigurationList(ctx context.Context) ([]model.ProxyConfiguration, error) {
	var items []model.ProxyConfiguration
	if err := db.GetDB().WithContext(ctx).Order("id ASC").Find(&items).Error; err != nil {
		return nil, err
	}
	counts, err := ProxyConfigurationReferenceCounts(ctx)
	if err != nil {
		return nil, err
	}
	type nodeCountRow struct {
		ProxyConfigurationID int
		NodeCount            int
		HealthyNodeCount     int
		AvailableNodeCount   int
		QuarantinedNodeCount int
	}
	var nodeCounts []nodeCountRow
	now := time.Now()
	if err := db.GetDB().WithContext(ctx).Model(&model.ProxySubscriptionNode{}).
		Select(`proxy_configuration_id,
			count(*) as node_count,
			sum(case when health_status = ? then 1 else 0 end) as healthy_node_count,
			sum(case when health_status = ? and (quarantined_until is null or quarantined_until <= ?) then 1 else 0 end) as available_node_count,
			sum(case when quarantined_until > ? then 1 else 0 end) as quarantined_node_count`,
			model.ProxyTestHealthHealthy, model.ProxyTestHealthHealthy, now, now).
		Where("active = ?", true).
		Group("proxy_configuration_id").Scan(&nodeCounts).Error; err != nil {
		return nil, err
	}
	nodeCountMap := make(map[int]nodeCountRow, len(nodeCounts))
	for _, row := range nodeCounts {
		nodeCountMap[row.ProxyConfigurationID] = row
	}
	for i := range items {
		if items[i].Type == "" {
			items[i].Type = model.ProxyConfigurationTypeSingle
		}
		items[i].ReferenceCount = counts[items[i].ID]
		items[i].NodeCount = nodeCountMap[items[i].ID].NodeCount
		items[i].HealthyNodeCount = nodeCountMap[items[i].ID].HealthyNodeCount
		items[i].AvailableNodeCount = nodeCountMap[items[i].ID].AvailableNodeCount
		items[i].QuarantinedNodeCount = nodeCountMap[items[i].ID].QuarantinedNodeCount
	}
	return items, nil
}

func ProxyConfigurationGet(id int, ctx context.Context) (*model.ProxyConfiguration, error) {
	var item model.ProxyConfiguration
	if err := db.GetDB().WithContext(ctx).First(&item, id).Error; err != nil {
		return nil, err
	}
	return &item, nil
}

func ProxyConfigurationCreate(item *model.ProxyConfiguration, ctx context.Context) error {
	if item == nil {
		return fmt.Errorf("proxy configuration is nil")
	}
	if err := item.Validate(); err != nil {
		return err
	}
	if item.Type == model.ProxyConfigurationTypeSubscription {
		if err := outboundurl.ValidateHTTPURL(item.URL); err != nil {
			return fmt.Errorf("invalid subscription url: %w", err)
		}
	}
	if err := db.GetDB().WithContext(ctx).Create(item).Error; err != nil {
		return err
	}
	// A freshly created configuration can reuse an ID after an import or a
	// test-database reset. Drop any stale round-robin state before first use.
	forgetProxySubscriptionState(item.ID)
	proxyConfigurationCache.Set(item.ID, *item)
	return nil
}

func ProxyConfigurationUpdate(req *model.ProxyConfigurationUpdateRequest, ctx context.Context) (*model.ProxyConfiguration, error) {
	if req == nil {
		return nil, fmt.Errorf("proxy update request is nil")
	}
	lock := proxySubscriptionLock(req.ID)
	lock.Lock()
	defer lock.Unlock()
	var existing model.ProxyConfiguration
	if err := db.GetDB().WithContext(ctx).First(&existing, req.ID).Error; err != nil {
		return nil, fmt.Errorf("proxy configuration not found")
	}
	merged := existing
	var selectFields []string
	updates := model.ProxyConfiguration{ID: req.ID}
	if req.Name != nil {
		merged.Name = *req.Name
		selectFields = append(selectFields, "name")
	}
	if req.URL != nil {
		merged.URL = *req.URL
		selectFields = append(selectFields, "url")
	}
	if req.Enabled != nil {
		merged.Enabled = *req.Enabled
		selectFields = append(selectFields, "enabled")
	}
	if req.Remark != nil {
		merged.Remark = *req.Remark
		selectFields = append(selectFields, "remark")
	}
	if req.RefreshIntervalMinutes != nil {
		merged.RefreshIntervalMinutes = *req.RefreshIntervalMinutes
		selectFields = append(selectFields, "refresh_interval_minutes")
	}
	if len(selectFields) > 0 {
		if err := merged.Validate(); err != nil {
			return nil, err
		}
		if merged.Type == model.ProxyConfigurationTypeSubscription {
			if err := outboundurl.ValidateHTTPURL(merged.URL); err != nil {
				return nil, fmt.Errorf("invalid subscription url: %w", err)
			}
		}
	}
	if req.Name != nil {
		updates.Name = merged.Name
	}
	if req.URL != nil {
		updates.URL = merged.URL
	}
	if req.Enabled != nil {
		updates.Enabled = merged.Enabled
	}
	if req.Remark != nil {
		updates.Remark = merged.Remark
	}
	if req.RefreshIntervalMinutes != nil {
		updates.RefreshIntervalMinutes = merged.RefreshIntervalMinutes
	}
	resetSubscription := req.URL != nil && existing.Type == model.ProxyConfigurationTypeSubscription && merged.URL != existing.URL
	if resetSubscription {
		if err := db.GetDB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			if len(selectFields) > 0 {
				if err := tx.Model(&model.ProxyConfiguration{}).Where("id = ?", req.ID).Select(selectFields).Updates(&updates).Error; err != nil {
					return err
				}
			}
			return tx.Model(&model.ProxyConfiguration{}).Where("id = ?", req.ID).Updates(map[string]any{
				"last_sync_at":      nil,
				"last_sync_status":  model.ProxySubscriptionSyncIdle,
				"last_sync_message": "",
			}).Error
		}); err != nil {
			return nil, fmt.Errorf("failed to update proxy subscription: %w", err)
		}
		invalidateProxySubscriptionCache(req.ID)
	} else if len(selectFields) > 0 {
		if err := db.GetDB().WithContext(ctx).Model(&model.ProxyConfiguration{}).Where("id = ?", req.ID).Select(selectFields).Updates(&updates).Error; err != nil {
			return nil, fmt.Errorf("failed to update proxy configuration: %w", err)
		}
	}
	item, err := ProxyConfigurationGet(req.ID, ctx)
	if err != nil {
		return nil, err
	}
	proxyConfigurationCache.Set(item.ID, *item)
	return item, nil
}

func ProxyConfigurationDelete(id int, ctx context.Context) error {
	lock := proxySubscriptionLock(id)
	lock.Lock()
	defer lock.Unlock()
	if _, err := ProxyConfigurationGet(id, ctx); err != nil {
		return fmt.Errorf("proxy configuration not found")
	}
	count, err := ProxyConfigurationReferenceCount(id, ctx)
	if err != nil {
		return err
	}
	if count > 0 {
		return fmt.Errorf("proxy configuration is still referenced")
	}
	if err := db.GetDB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("proxy_configuration_id = ?", id).Delete(&model.ProxySubscriptionNode{}).Error; err != nil {
			return err
		}
		return tx.Delete(&model.ProxyConfiguration{}, id).Error
	}); err != nil {
		return err
	}
	proxyConfigurationCache.Del(id)
	forgetProxySubscriptionState(id)
	return nil
}

func ProxyConfigurationReferenceCount(id int, ctx context.Context) (int, error) {
	counts, err := ProxyConfigurationReferenceCounts(ctx)
	if err != nil {
		return 0, err
	}
	return counts[id], nil
}

func ProxyConfigurationReferences(id int, ctx context.Context) ([]model.ProxyConfigurationReference, error) {
	if _, err := ProxyConfigurationGet(id, ctx); err != nil {
		return nil, fmt.Errorf("proxy configuration not found")
	}

	refs := make([]model.ProxyConfigurationReference, 0)

	var sites []model.Site
	if err := db.GetDB().WithContext(ctx).
		Where("proxy_mode = ? AND proxy_config_id = ?", model.ProxyUsageModePool, id).
		Order("id ASC").Find(&sites).Error; err != nil {
		return nil, err
	}
	for _, site := range sites {
		refs = append(refs, model.ProxyConfigurationReference{
			Type:         model.ProxyConfigurationReferenceTypeSite,
			SiteID:       site.ID,
			SiteName:     site.Name,
			SiteArchived: site.Archived,
		})
	}

	type accountRefRow struct {
		ID       int
		Name     string
		SiteID   int
		SiteName string
		Archived bool
	}
	var accountRows []accountRefRow
	if err := db.GetDB().WithContext(ctx).
		Table("site_accounts").
		Select("site_accounts.id, site_accounts.name, site_accounts.site_id, sites.name as site_name, sites.archived").
		Joins("LEFT JOIN sites ON sites.id = site_accounts.site_id").
		Where("site_accounts.proxy_mode = ? AND site_accounts.proxy_config_id = ?", model.ProxyUsageModePool, id).
		Order("site_accounts.id ASC").Scan(&accountRows).Error; err != nil {
		return nil, err
	}
	for _, row := range accountRows {
		refs = append(refs, model.ProxyConfigurationReference{
			Type:            model.ProxyConfigurationReferenceTypeSiteAccount,
			SiteID:          row.SiteID,
			SiteName:        row.SiteName,
			SiteArchived:    row.Archived,
			SiteAccountID:   row.ID,
			SiteAccountName: row.Name,
		})
	}

	var channels []model.Channel
	if err := db.GetDB().WithContext(ctx).
		Where("proxy_mode = ? AND proxy_config_id = ?", model.ProxyUsageModePool, id).
		Order("id ASC").Find(&channels).Error; err != nil {
		return nil, err
	}
	channelIDs := make([]int, 0, len(channels))
	for _, channel := range channels {
		channelIDs = append(channelIDs, channel.ID)
	}
	bindingMap, err := SiteChannelBindingMapByChannelIDs(channelIDs, ctx)
	if err != nil {
		return nil, err
	}
	for _, channel := range channels {
		ref := model.ProxyConfigurationReference{
			Type:        model.ProxyConfigurationReferenceTypeChannel,
			ChannelID:   channel.ID,
			ChannelName: channel.Name,
		}
		if binding, ok := bindingMap[channel.ID]; ok {
			ref.Type = model.ProxyConfigurationReferenceTypeManagedChannel
			ref.Managed = true
			ref.SiteID = binding.SiteID
			ref.SiteAccountID = binding.SiteAccountID
			ref.ManagedSource = &model.ManagedChannelSource{
				SiteID:          binding.SiteID,
				SiteAccountID:   binding.SiteAccountID,
				SiteUserGroupID: binding.SiteUserGroupID,
				GroupKey:        binding.GroupKey,
			}
		}
		refs = append(refs, ref)
	}

	return refs, nil
}

func ProxyConfigurationReferenceCounts(ctx context.Context) (map[int]int, error) {
	counts := make(map[int]int)
	if err := countProxyReferences(ctx, model.Site{}, counts); err != nil {
		return nil, err
	}
	if err := countProxyReferences(ctx, model.SiteAccount{}, counts); err != nil {
		return nil, err
	}
	if err := countManualChannelProxyReferences(ctx, counts); err != nil {
		return nil, err
	}
	return counts, nil
}

func countProxyReferences(ctx context.Context, table any, counts map[int]int) error {
	type row struct {
		ProxyConfigID int
		Count         int
	}
	var rows []row
	if err := db.GetDB().WithContext(ctx).Model(table).
		Select("proxy_config_id, count(*) as count").
		Where("proxy_mode = ? AND proxy_config_id IS NOT NULL", model.ProxyUsageModePool).
		Group("proxy_config_id").Scan(&rows).Error; err != nil {
		return err
	}
	for _, r := range rows {
		counts[r.ProxyConfigID] += r.Count
	}
	return nil
}

func countManualChannelProxyReferences(ctx context.Context, counts map[int]int) error {
	type row struct {
		ProxyConfigID int
		Count         int
	}
	var rows []row
	if err := db.GetDB().WithContext(ctx).Table("channels").
		Select("channels.proxy_config_id, count(*) as count").
		Where("channels.proxy_mode = ? AND channels.proxy_config_id IS NOT NULL", model.ProxyUsageModePool).
		Where("NOT EXISTS (SELECT 1 FROM site_channel_bindings WHERE site_channel_bindings.channel_id = channels.id)").
		Group("channels.proxy_config_id").Scan(&rows).Error; err != nil {
		return err
	}
	for _, r := range rows {
		counts[r.ProxyConfigID] += r.Count
	}
	return nil
}

func ProxyURLForConfig(id int, ctx context.Context) (string, error) {
	urls, err := ProxyURLsForConfigStable(id, ctx)
	if err != nil {
		return "", err
	}
	return urls[0], nil
}

func proxyConfigurationForUse(id int, ctx context.Context) (*model.ProxyConfiguration, error) {
	if cached, ok := proxyConfigurationCache.Get(id); ok {
		if !cached.Enabled {
			return nil, fmt.Errorf("proxy configuration is disabled")
		}
		return &cached, nil
	}
	item, err := ProxyConfigurationGet(id, ctx)
	if err != nil {
		proxyConfigurationCache.Del(id)
		return nil, fmt.Errorf("proxy configuration not found")
	}
	if item.Type == "" {
		item.Type = model.ProxyConfigurationTypeSingle
	}
	proxyConfigurationCache.Set(item.ID, *item)
	if !item.Enabled {
		return nil, fmt.Errorf("proxy configuration is disabled")
	}
	return item, nil
}

func proxyConfigurationRefreshCache(ctx context.Context) error {
	var items []model.ProxyConfiguration
	if err := db.GetDB().WithContext(ctx).Find(&items).Error; err != nil {
		return err
	}
	proxyConfigurationCache.Clear()
	proxySubscriptionNodeCache.Clear()
	clearAllProxySubscriptionCounters()
	for _, item := range items {
		proxyConfigurationCache.Set(item.ID, item)
	}
	return nil
}

func proxyTestTargetHostSafe(parsedTarget *url.URL) error {
	if parsedTarget == nil {
		return fmt.Errorf("test url is required")
	}
	host := strings.TrimSpace(parsedTarget.Hostname())
	if host == "" {
		host = strings.TrimSpace(parsedTarget.Host)
	}
	host = strings.Trim(strings.ToLower(host), ".")
	if host == "" {
		return fmt.Errorf("test url must have a host")
	}
	if host == "localhost" || strings.HasSuffix(host, ".local") {
		return fmt.Errorf("test url host is not allowed")
	}
	if ip := net.ParseIP(host); ip != nil {
		if proxyTestIPDisallowed(ip) {
			return fmt.Errorf("test url host is not allowed")
		}
		return nil
	}
	ips, err := net.LookupIP(host)
	if err != nil {
		return fmt.Errorf("resolve test url host: %w", err)
	}
	if len(ips) == 0 {
		return fmt.Errorf("test url host did not resolve")
	}
	for _, ip := range ips {
		if proxyTestIPDisallowed(ip) {
			return fmt.Errorf("test url host resolves to a disallowed address")
		}
	}
	return nil
}

func proxyTestIPDisallowed(ip net.IP) bool {
	if ip == nil {
		return true
	}
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsMulticast() || ip.IsUnspecified() {
		return true
	}
	if v4 := ip.To4(); v4 != nil {
		return v4[0] == 169 && v4[1] == 254
	}
	return ip.IsPrivate()
}

func newProxyTestHTTPClient(proxyURLStr string) (*http.Client, error) {
	return newProxyTestHTTPClientWithMode(proxyURLStr, false)
}

func newProxyTestHTTPClientWithMode(proxyURLStr string, http1Only bool) (*http.Client, error) {
	transport, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		return nil, fmt.Errorf("default transport is not *http.Transport")
	}
	cloned := transport.Clone()
	proxyURL, err := url.Parse(proxyURLStr)
	if err != nil {
		return nil, fmt.Errorf("invalid proxy url: %w", err)
	}
	if err := outboundurl.ConfigureProxyTransport(cloned, proxyURL); err != nil {
		return nil, err
	}
	if http1Only {
		outboundurl.ConfigureHTTP1Transport(cloned)
	}
	return &http.Client{
		Transport:     outboundurl.WrapTransport(cloned),
		CheckRedirect: outboundurl.CheckRedirect,
	}, nil
}

func proxyTestExpectedStatus(target *url.URL) int {
	if target != nil && strings.EqualFold(target.Hostname(), "www.google.com") && target.EscapedPath() == "/generate_204" {
		return http.StatusNoContent
	}
	return 0
}

func proxyTestStatusAccepted(statusCode int, expectedStatus int) bool {
	if expectedStatus > 0 {
		return statusCode == expectedStatus
	}
	return statusCode >= http.StatusOK && statusCode < http.StatusMultipleChoices
}

func runProxyTestAttempt(ctx context.Context, proxyURL string, targetURL string, expectedStatus int, attempt int) model.ProxyTestAttemptResult {
	result := model.ProxyTestAttemptResult{Attempt: attempt}
	httpClient, err := newProxyTestHTTPClient(proxyURL)
	if err != nil {
		result.Message = err.Error()
		return result
	}
	httpClient.Timeout = proxyTestAttemptTimeout

	start := time.Now()
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, targetURL, nil)
	if err != nil {
		result.Message = err.Error()
		return result
	}
	httpReq.Close = true
	httpReq.Header.Set("User-Agent", "Octopus Proxy Pool Tester")
	resp, err := httpClient.Do(httpReq)
	if err != nil && outboundurl.IsTLSHandshakeFailure(err) {
		if http1Client, clientErr := newProxyTestHTTPClientWithMode(proxyURL, true); clientErr == nil {
			http1Client.Timeout = proxyTestAttemptTimeout
			fallbackReq := httpReq.Clone(ctx)
			fallbackReq.Close = true
			resp, err = http1Client.Do(fallbackReq)
		}
	}
	result.DurationMS = time.Since(start).Milliseconds()
	if err != nil {
		result.Message = err.Error()
		return result
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	result.StatusCode = resp.StatusCode
	if !proxyTestStatusAccepted(resp.StatusCode, expectedStatus) {
		if expectedStatus > 0 {
			result.Message = fmt.Sprintf("unexpected HTTP status: got %d, expected %d", resp.StatusCode, expectedStatus)
		} else {
			result.Message = fmt.Sprintf("unexpected HTTP status: %d", resp.StatusCode)
		}
		return result
	}
	result.Success = true
	result.Message = "proxy is reachable"
	return result
}

func waitProxyTestAttempt(ctx context.Context) bool {
	timer := time.NewTimer(proxyTestAttemptDelay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func resolveProxyTestURLs(req model.ProxyTestRequest, ctx context.Context) ([]string, error) {
	proxyURL := strings.TrimSpace(req.ProxyURL)
	if req.ProxyConfigID != nil && *req.ProxyConfigID > 0 {
		item, err := ProxyConfigurationGet(*req.ProxyConfigID, ctx)
		if err != nil {
			return nil, fmt.Errorf("proxy configuration not found")
		}
		if !item.Enabled {
			return nil, fmt.Errorf("proxy configuration is disabled")
		}
		if item.Type == model.ProxyConfigurationTypeSubscription {
			urls, resolveErr := ProxyURLsForConfig(item.ID, ctx)
			if resolveErr != nil {
				return nil, resolveErr
			}
			return urls, nil
		} else {
			proxyURL = item.URL
		}
	} else if req.UseSystemProxy && proxyURL == "" {
		storedProxyURL, err := SettingGetString(model.SettingKeyProxyURL)
		if err != nil {
			return nil, fmt.Errorf("system proxy setting not found")
		}
		proxyURL = storedProxyURL
	}
	if strings.TrimSpace(proxyURL) == "" {
		return nil, fmt.Errorf("proxy url is required")
	}
	normalized, err := model.NormalizeProxyURL(proxyURL)
	if err != nil {
		return nil, err
	}
	return []string{normalized}, nil
}

func ProxyConfigurationTest(req model.ProxyTestRequest, ctx context.Context) (model.ProxyTestResult, error) {
	targetURL := strings.TrimSpace(req.URL)
	if targetURL == "" {
		targetURL = defaultProxyTestURL
	}
	parsedTarget, err := url.Parse(targetURL)
	if err != nil || parsedTarget.Scheme == "" || parsedTarget.Host == "" || (parsedTarget.Scheme != "http" && parsedTarget.Scheme != "https") {
		return model.ProxyTestResult{HealthStatus: model.ProxyTestHealthFailed, Message: "test url must be a valid http or https url"}, nil
	}
	if err := proxyTestTargetHostSafe(parsedTarget); err != nil {
		return model.ProxyTestResult{HealthStatus: model.ProxyTestHealthFailed, Message: err.Error()}, nil
	}

	normalizedProxyURLs, err := resolveProxyTestURLs(req, ctx)
	if err != nil {
		return model.ProxyTestResult{HealthStatus: model.ProxyTestHealthFailed, Message: err.Error()}, nil
	}
	if len(normalizedProxyURLs) == 1 {
		return testNormalizedProxyURL(ctx, normalizedProxyURLs[0], targetURL), nil
	}
	return testNormalizedProxyURLs(ctx, normalizedProxyURLs, targetURL), nil
}

func testNormalizedProxyURL(ctx context.Context, normalizedProxyURL string, targetURL string) model.ProxyTestResult {
	startedAt := time.Now()
	attempts := make([]model.ProxyTestAttemptResult, 0, proxyTestAttemptCount)
	parsedTarget, _ := url.Parse(targetURL)
	expectedStatus := proxyTestExpectedStatus(parsedTarget)
	for attempt := 1; attempt <= proxyTestAttemptCount; attempt++ {
		attemptResult := runProxyTestAttempt(ctx, normalizedProxyURL, targetURL, expectedStatus, attempt)
		attempts = append(attempts, attemptResult)
		if attempt < proxyTestAttemptCount && !waitProxyTestAttempt(ctx) {
			break
		}
	}
	return summarizeProxyTestResult(startedAt, attempts, proxyTestAttemptCount)
}

func testNormalizedProxyURLs(ctx context.Context, normalizedProxyURLs []string, targetURL string) model.ProxyTestResult {
	startedAt := time.Now()
	attempts := make([]model.ProxyTestAttemptResult, 0, len(normalizedProxyURLs))
	parsedTarget, _ := url.Parse(targetURL)
	expectedStatus := proxyTestExpectedStatus(parsedTarget)
	for index, proxyURL := range normalizedProxyURLs {
		attempts = append(attempts, runProxyTestAttempt(ctx, proxyURL, targetURL, expectedStatus, index+1))
		if index+1 < len(normalizedProxyURLs) && !waitProxyTestAttempt(ctx) {
			break
		}
	}
	return summarizeProxyTestResult(startedAt, attempts, len(normalizedProxyURLs))
}

func summarizeProxyTestResult(startedAt time.Time, attempts []model.ProxyTestAttemptResult, expectedAttempts int) model.ProxyTestResult {
	successCount := 0
	statusCode := 0
	var attemptDurationTotal int64
	lastFailure := ""
	for _, attempt := range attempts {
		attemptDurationTotal += attempt.DurationMS
		if attempt.Success {
			successCount++
			statusCode = attempt.StatusCode
			continue
		}
		lastFailure = attempt.Message
		if statusCode == 0 && attempt.StatusCode != 0 {
			statusCode = attempt.StatusCode
		}
	}

	healthStatus := model.ProxyTestHealthFailed
	message := fmt.Sprintf("proxy check failed: %d/%d checks succeeded", successCount, len(attempts))
	if successCount == expectedAttempts && len(attempts) == expectedAttempts {
		healthStatus = model.ProxyTestHealthHealthy
		message = fmt.Sprintf("proxy is healthy: %d/%d checks succeeded", successCount, len(attempts))
	} else if successCount > 0 {
		healthStatus = model.ProxyTestHealthDegraded
		message = fmt.Sprintf("proxy is unstable: %d/%d checks succeeded", successCount, len(attempts))
	}
	if lastFailure != "" && healthStatus != model.ProxyTestHealthHealthy {
		message += "; last error: " + lastFailure
	}
	averageDurationMS := int64(0)
	if len(attempts) > 0 {
		averageDurationMS = attemptDurationTotal / int64(len(attempts))
	}
	return model.ProxyTestResult{
		Success:           healthStatus != model.ProxyTestHealthFailed,
		HealthStatus:      healthStatus,
		StatusCode:        statusCode,
		DurationMS:        time.Since(startedAt).Milliseconds(),
		AverageDurationMS: averageDurationMS,
		AttemptCount:      len(attempts),
		SuccessCount:      successCount,
		Attempts:          attempts,
		Message:           message,
	}
}
