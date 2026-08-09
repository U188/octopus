package op

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	dbpkg "github.com/U188/octopus/internal/db"
	"github.com/U188/octopus/internal/model"
)

func newSequencedTestProxy(t *testing.T, statusCodes []int) (*httptest.Server, *atomic.Int64) {
	t.Helper()
	var requestCount atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		index := int(requestCount.Add(1)) - 1
		if index >= len(statusCodes) {
			index = len(statusCodes) - 1
		}
		w.WriteHeader(statusCodes[index])
	}))
	t.Cleanup(server.Close)
	return server, &requestCount
}

func TestResolveProxyTestURLsUsesStoredSystemProxy(t *testing.T) {
	previous, existed := settingCache.Get(model.SettingKeyProxyURL)
	settingCache.Set(model.SettingKeyProxyURL, "SOCKS5://User:Pass@Proxy.Example:1080")
	defer func() {
		if existed {
			settingCache.Set(model.SettingKeyProxyURL, previous)
			return
		}
		settingCache.Del(model.SettingKeyProxyURL)
	}()

	actual, err := resolveProxyTestURLs(model.ProxyTestRequest{UseSystemProxy: true}, context.Background())
	if err != nil {
		t.Fatalf("resolve stored system proxy: %v", err)
	}
	if len(actual) != 1 || actual[0] != "socks5://User:Pass@proxy.example:1080" {
		t.Fatalf("expected normalized stored system proxy, got %#v", actual)
	}
}

func TestResolveProxyTestURLsUsesDraftURL(t *testing.T) {
	actual, err := resolveProxyTestURLs(model.ProxyTestRequest{ProxyURL: " http://Proxy.Example:8080 "}, context.Background())
	if err != nil {
		t.Fatalf("resolve draft proxy: %v", err)
	}
	if len(actual) != 1 || actual[0] != "http://proxy.example:8080" {
		t.Fatalf("expected normalized draft proxy, got %#v", actual)
	}
}

func TestProxyConfigurationTestHealthyAfterThreeSuccessfulAttempts(t *testing.T) {
	proxyServer, requestCount := newSequencedTestProxy(t, []int{http.StatusNoContent, http.StatusNoContent, http.StatusNoContent})
	result, err := ProxyConfigurationTest(model.ProxyTestRequest{
		ProxyURL: proxyServer.URL,
		URL:      "http://example.com/health",
	}, context.Background())
	if err != nil {
		t.Fatalf("test proxy configuration: %v", err)
	}
	if result.HealthStatus != model.ProxyTestHealthHealthy || !result.Success {
		t.Fatalf("expected healthy result, got %+v", result)
	}
	if result.AttemptCount != proxyTestAttemptCount || result.SuccessCount != proxyTestAttemptCount {
		t.Fatalf("expected %d/%d successful attempts, got %+v", proxyTestAttemptCount, proxyTestAttemptCount, result)
	}
	if requestCount.Load() != proxyTestAttemptCount || len(result.Attempts) != proxyTestAttemptCount {
		t.Fatalf("expected three independent requests, count=%d attempts=%d", requestCount.Load(), len(result.Attempts))
	}
}

func TestProxyConfigurationTestReportsDegradedPartialSuccess(t *testing.T) {
	proxyServer, _ := newSequencedTestProxy(t, []int{http.StatusNoContent, http.StatusTooManyRequests, http.StatusNoContent})
	result, err := ProxyConfigurationTest(model.ProxyTestRequest{
		ProxyURL: proxyServer.URL,
		URL:      "http://example.com/health",
	}, context.Background())
	if err != nil {
		t.Fatalf("test proxy configuration: %v", err)
	}
	if result.HealthStatus != model.ProxyTestHealthDegraded || !result.Success {
		t.Fatalf("expected degraded reachable result, got %+v", result)
	}
	if result.SuccessCount != 2 || result.AttemptCount != proxyTestAttemptCount {
		t.Fatalf("expected 2/3 successful attempts, got %+v", result)
	}
	if result.Attempts[1].Success || result.Attempts[1].StatusCode != http.StatusTooManyRequests {
		t.Fatalf("expected the second attempt to record HTTP 429, got %+v", result.Attempts[1])
	}
}

func TestProxyConfigurationTestRejectsNon2xxResponses(t *testing.T) {
	proxyServer, _ := newSequencedTestProxy(t, []int{http.StatusTooManyRequests, http.StatusBadGateway, http.StatusServiceUnavailable})
	result, err := ProxyConfigurationTest(model.ProxyTestRequest{
		ProxyURL: proxyServer.URL,
		URL:      "http://example.com/health",
	}, context.Background())
	if err != nil {
		t.Fatalf("test proxy configuration: %v", err)
	}
	if result.HealthStatus != model.ProxyTestHealthFailed || result.Success {
		t.Fatalf("expected failed result, got %+v", result)
	}
	if result.SuccessCount != 0 || result.AttemptCount != proxyTestAttemptCount {
		t.Fatalf("expected 0/3 successful attempts, got %+v", result)
	}
}

func TestProxyConfigurationTestChecksEverySubscriptionCandidate(t *testing.T) {
	initProxySubscriptionTestDB(t)
	workingProxy, workingRequests := newSequencedTestProxy(t, []int{http.StatusNoContent})
	failingProxy, failingRequests := newSequencedTestProxy(t, []int{http.StatusBadGateway})
	config := model.ProxyConfiguration{
		Name:                   "testable subscription",
		URL:                    "https://example.com/proxies.txt",
		Type:                   model.ProxyConfigurationTypeSubscription,
		Enabled:                true,
		RefreshIntervalMinutes: 30,
	}
	if err := ProxyConfigurationCreate(&config, context.Background()); err != nil {
		t.Fatalf("create proxy subscription: %v", err)
	}
	nodes := []model.ProxySubscriptionNode{
		{ProxyConfigurationID: config.ID, URL: workingProxy.URL, Active: true, HealthStatus: model.ProxyTestHealthHealthy, LatencyMS: 10},
		{ProxyConfigurationID: config.ID, URL: failingProxy.URL, Active: true, HealthStatus: model.ProxyTestHealthHealthy, LatencyMS: 20},
	}
	if err := dbpkg.GetDB().Create(&nodes).Error; err != nil {
		t.Fatalf("create proxy subscription nodes: %v", err)
	}

	result, err := ProxyConfigurationTest(model.ProxyTestRequest{
		ProxyConfigID: &config.ID,
		URL:           "http://example.com/health",
	}, context.Background())
	if err != nil {
		t.Fatalf("test proxy subscription: %v", err)
	}
	if result.HealthStatus != model.ProxyTestHealthDegraded || result.AttemptCount != 2 || result.SuccessCount != 1 {
		t.Fatalf("unexpected subscription test result: %+v", result)
	}
	if workingRequests.Load() != 1 || failingRequests.Load() != 1 {
		t.Fatalf("subscription candidates were not each tested once: working=%d failing=%d", workingRequests.Load(), failingRequests.Load())
	}
}

func TestProxyTestExpectedStatusForGoogleGenerate204(t *testing.T) {
	googleURL, err := url.Parse(defaultProxyTestURL)
	if err != nil {
		t.Fatalf("parse default test URL: %v", err)
	}
	if got := proxyTestExpectedStatus(googleURL); got != http.StatusNoContent {
		t.Fatalf("expected HTTP 204 for Google health endpoint, got %d", got)
	}
	if proxyTestStatusAccepted(http.StatusOK, http.StatusNoContent) {
		t.Fatal("expected HTTP 200 to fail the Google generate_204 check")
	}
	if !proxyTestStatusAccepted(http.StatusNoContent, http.StatusNoContent) {
		t.Fatal("expected HTTP 204 to pass the Google generate_204 check")
	}
}

func initProxySubscriptionTestDB(t *testing.T) {
	t.Helper()
	if dbpkg.GetDB() != nil {
		_ = dbpkg.Close()
	}
	if err := dbpkg.InitDB("sqlite", filepath.Join(t.TempDir(), "proxy-subscription.db"), false); err != nil {
		t.Fatalf("init proxy subscription test database: %v", err)
	}
	proxyConfigurationCache.Clear()
	proxySubscriptionNodeCache.Clear()
	proxySubscriptionCounters.Range(func(key, _ any) bool {
		proxySubscriptionCounters.Delete(key)
		return true
	})
	t.Cleanup(func() {
		proxyConfigurationCache.Clear()
		proxySubscriptionNodeCache.Clear()
		proxySubscriptionCounters.Range(func(key, _ any) bool {
			proxySubscriptionCounters.Delete(key)
			return true
		})
		_ = dbpkg.Close()
	})
}

func TestParseProxySubscriptionNormalizesDeduplicatesAndSkipsInvalidLines(t *testing.T) {
	urls, err := parseProxySubscription(`
# generated list
SOCKS5://Proxy.Example:1080
socks5://proxy.example:1080 # duplicate
http://Second.Example:8080
not-a-proxy
`)
	if err != nil {
		t.Fatalf("parse proxy subscription: %v", err)
	}
	want := []string{"http://second.example:8080", "socks5://proxy.example:1080"}
	if len(urls) != len(want) {
		t.Fatalf("parsed URLs = %#v, want %#v", urls, want)
	}
	for i := range want {
		if urls[i] != want[i] {
			t.Fatalf("parsed URLs = %#v, want %#v", urls, want)
		}
	}
}

func TestProxyURLsForConfigRotatesHealthyActiveNodes(t *testing.T) {
	initProxySubscriptionTestDB(t)
	ctx := context.Background()
	config := model.ProxyConfiguration{
		Name:                   "rotating subscription",
		URL:                    "https://example.com/proxies.txt",
		Type:                   model.ProxyConfigurationTypeSubscription,
		Enabled:                true,
		RefreshIntervalMinutes: 30,
	}
	if err := ProxyConfigurationCreate(&config, ctx); err != nil {
		t.Fatalf("create proxy subscription: %v", err)
	}
	nodes := []model.ProxySubscriptionNode{
		{ProxyConfigurationID: config.ID, URL: "socks5://127.0.0.1:1001", Active: true, HealthStatus: model.ProxyTestHealthHealthy, LatencyMS: 20},
		{ProxyConfigurationID: config.ID, URL: "socks5://127.0.0.1:1002", Active: true, HealthStatus: model.ProxyTestHealthHealthy, LatencyMS: 10},
		{ProxyConfigurationID: config.ID, URL: "socks5://127.0.0.1:1003", Active: true, HealthStatus: model.ProxyTestHealthFailed, LatencyMS: 1},
		{ProxyConfigurationID: config.ID, URL: "socks5://127.0.0.1:1004", Active: false, HealthStatus: model.ProxyTestHealthHealthy, LatencyMS: 1},
	}
	if err := dbpkg.GetDB().Create(&nodes).Error; err != nil {
		t.Fatalf("create subscription nodes: %v", err)
	}
	if err := dbpkg.GetDB().Model(&model.ProxySubscriptionNode{}).Where("id = ?", nodes[3].ID).Update("active", false).Error; err != nil {
		t.Fatalf("deactivate subscription node: %v", err)
	}

	first, err := ProxyURLsForConfig(config.ID, ctx)
	if err != nil {
		t.Fatalf("resolve first proxy rotation: %v", err)
	}
	second, err := ProxyURLsForConfig(config.ID, ctx)
	if err != nil {
		t.Fatalf("resolve second proxy rotation: %v", err)
	}
	if len(first) != 2 || len(second) != 2 {
		t.Fatalf("expected two healthy active nodes, first=%#v second=%#v", first, second)
	}
	if first[0] != "socks5://127.0.0.1:1002" || second[0] != "socks5://127.0.0.1:1001" {
		t.Fatalf("expected latency-ordered round robin, first=%#v second=%#v", first, second)
	}
}

func TestProxyURLsForConfigStableAndScopedRotation(t *testing.T) {
	initProxySubscriptionTestDB(t)
	ctx := context.Background()
	config := model.ProxyConfiguration{
		Name:                   "stable scoped subscription",
		URL:                    "https://example.com/scoped.txt",
		Type:                   model.ProxyConfigurationTypeSubscription,
		Enabled:                true,
		RefreshIntervalMinutes: 30,
	}
	if err := ProxyConfigurationCreate(&config, ctx); err != nil {
		t.Fatalf("create proxy subscription: %v", err)
	}
	nodes := []model.ProxySubscriptionNode{
		{ProxyConfigurationID: config.ID, URL: "socks5://127.0.0.1:1101", Active: true, HealthStatus: model.ProxyTestHealthHealthy, LatencyMS: 10},
		{ProxyConfigurationID: config.ID, URL: "socks5://127.0.0.1:1102", Active: true, HealthStatus: model.ProxyTestHealthHealthy, LatencyMS: 20},
	}
	if err := dbpkg.GetDB().Create(&nodes).Error; err != nil {
		t.Fatalf("create subscription nodes: %v", err)
	}

	stableFirst, err := ProxyURLsForConfigStable(config.ID, ctx)
	if err != nil {
		t.Fatalf("resolve first stable candidates: %v", err)
	}
	stableSecond, err := ProxyURLsForConfigStable(config.ID, ctx)
	if err != nil {
		t.Fatalf("resolve second stable candidates: %v", err)
	}
	if stableFirst[0] != nodes[0].URL || stableSecond[0] != nodes[0].URL {
		t.Fatalf("stable candidates moved: first=%#v second=%#v", stableFirst, stableSecond)
	}

	siteFirst, err := ProxyURLsForConfigScoped(config.ID, "site:1", ctx)
	if err != nil {
		t.Fatalf("resolve first site candidates: %v", err)
	}
	otherSiteFirst, err := ProxyURLsForConfigScoped(config.ID, "site:2", ctx)
	if err != nil {
		t.Fatalf("resolve other site candidates: %v", err)
	}
	siteSecond, err := ProxyURLsForConfigScoped(config.ID, "site:1", ctx)
	if err != nil {
		t.Fatalf("resolve second site candidates: %v", err)
	}
	otherSiteSecond, err := ProxyURLsForConfigScoped(config.ID, "site:2", ctx)
	if err != nil {
		t.Fatalf("resolve second other-site candidates: %v", err)
	}
	if siteFirst[0] != nodes[0].URL || otherSiteFirst[0] != nodes[0].URL ||
		siteSecond[0] != nodes[1].URL || otherSiteSecond[0] != nodes[1].URL {
		t.Fatalf("scoped rotations were not independent: site=%#v/%#v other=%#v/%#v", siteFirst, siteSecond, otherSiteFirst, otherSiteSecond)
	}
}

func TestProxyURLsForConfigDoesNotAllocateCursorForSingleHealthyNode(t *testing.T) {
	initProxySubscriptionTestDB(t)
	ctx := context.Background()
	config := model.ProxyConfiguration{
		Name:                   "single-node-no-cursor",
		URL:                    "https://example.com/single-node.txt",
		Type:                   model.ProxyConfigurationTypeSubscription,
		Enabled:                true,
		RefreshIntervalMinutes: 30,
	}
	if err := ProxyConfigurationCreate(&config, ctx); err != nil {
		t.Fatalf("create proxy subscription: %v", err)
	}
	node := model.ProxySubscriptionNode{
		ProxyConfigurationID: config.ID,
		URL:                  "socks5://127.0.0.1:1161",
		Active:               true,
		HealthStatus:         model.ProxyTestHealthHealthy,
		LatencyMS:            10,
	}
	if err := dbpkg.GetDB().Create(&node).Error; err != nil {
		t.Fatalf("create subscription node: %v", err)
	}
	if _, err := ProxyURLsForConfigScoped(config.ID, "site:single", ctx); err != nil {
		t.Fatalf("resolve single-node subscription: %v", err)
	}
	if _, ok := proxySubscriptionCounters.Load(proxySubscriptionCounterKey{ConfigID: config.ID, Scope: "site:single"}); ok {
		t.Fatal("single-node subscription allocated a useless rotation cursor")
	}
}

func TestProxyURLsForConfigRotationIndexDoesNotOverflowInt(t *testing.T) {
	initProxySubscriptionTestDB(t)
	ctx := context.Background()
	config := model.ProxyConfiguration{
		Name:                   "portable counter subscription",
		URL:                    "https://example.com/portable-counter.txt",
		Type:                   model.ProxyConfigurationTypeSubscription,
		Enabled:                true,
		RefreshIntervalMinutes: 30,
	}
	if err := ProxyConfigurationCreate(&config, ctx); err != nil {
		t.Fatalf("create proxy subscription: %v", err)
	}
	nodes := []model.ProxySubscriptionNode{
		{ProxyConfigurationID: config.ID, URL: "socks5://127.0.0.1:1151", Active: true, HealthStatus: model.ProxyTestHealthHealthy, LatencyMS: 10},
		{ProxyConfigurationID: config.ID, URL: "socks5://127.0.0.1:1152", Active: true, HealthStatus: model.ProxyTestHealthHealthy, LatencyMS: 20},
	}
	if err := dbpkg.GetDB().Create(&nodes).Error; err != nil {
		t.Fatalf("create subscription nodes: %v", err)
	}

	key := proxySubscriptionCounterKey{ConfigID: config.ID, Scope: "portable"}
	counter := &atomic.Uint64{}
	// Force the next sequence past the host's MaxInt. The old int-first
	// calculation could yield a negative slice index on both 32-bit and
	// 64-bit builds.
	counter.Store(uint64(^uint(0)>>1) + 2)
	proxySubscriptionCounters.Store(key, counter)
	t.Cleanup(func() { proxySubscriptionCounters.Delete(key) })

	urls, err := ProxyURLsForConfigScoped(config.ID, key.Scope, ctx)
	if err != nil {
		t.Fatalf("resolve candidates after counter overflow: %v", err)
	}
	if len(urls) != 2 || (urls[0] != nodes[0].URL && urls[0] != nodes[1].URL) {
		t.Fatalf("unexpected candidates after counter overflow: %#v", urls)
	}
}

func TestProxyURLsForConfigLimitsPerRequestCandidates(t *testing.T) {
	initProxySubscriptionTestDB(t)
	ctx := context.Background()
	config := model.ProxyConfiguration{
		Name:                   "bounded subscription",
		URL:                    "https://example.com/bounded.txt",
		Type:                   model.ProxyConfigurationTypeSubscription,
		Enabled:                true,
		RefreshIntervalMinutes: 30,
	}
	if err := ProxyConfigurationCreate(&config, ctx); err != nil {
		t.Fatalf("create proxy subscription: %v", err)
	}
	nodes := make([]model.ProxySubscriptionNode, proxyRequestCandidateLimit+3)
	for i := range nodes {
		nodes[i] = model.ProxySubscriptionNode{
			ProxyConfigurationID: config.ID,
			URL:                  fmt.Sprintf("socks5://127.0.0.1:%d", 1200+i),
			Active:               true,
			HealthStatus:         model.ProxyTestHealthHealthy,
			LatencyMS:            int64(i + 1),
		}
	}
	if err := dbpkg.GetDB().Create(&nodes).Error; err != nil {
		t.Fatalf("create subscription nodes: %v", err)
	}
	first, err := ProxyURLsForConfig(config.ID, ctx)
	if err != nil {
		t.Fatalf("resolve bounded candidates: %v", err)
	}
	second, err := ProxyURLsForConfig(config.ID, ctx)
	if err != nil {
		t.Fatalf("resolve next bounded candidates: %v", err)
	}
	if len(first) != proxyRequestCandidateLimit || len(second) != proxyRequestCandidateLimit {
		t.Fatalf("candidate limits: first=%d second=%d", len(first), len(second))
	}
	if first[0] == second[0] {
		t.Fatalf("bounded candidates did not rotate: first=%#v second=%#v", first, second)
	}
}

func TestProxySubscriptionRuntimeFailureQuarantinesAndAutomaticallyRestoresNode(t *testing.T) {
	initProxySubscriptionTestDB(t)
	previousQuarantine := proxyRuntimeQuarantine
	// Leave enough room for the race detector and SQLite scheduling before the
	// first assertion; the expiry check below still waits only as long as needed.
	proxyRuntimeQuarantine = 2 * time.Second
	t.Cleanup(func() { proxyRuntimeQuarantine = previousQuarantine })
	ctx := context.Background()
	config := model.ProxyConfiguration{
		Name:                   "runtime quarantine subscription",
		URL:                    "https://example.com/quarantine.txt",
		Type:                   model.ProxyConfigurationTypeSubscription,
		Enabled:                true,
		RefreshIntervalMinutes: 30,
	}
	if err := ProxyConfigurationCreate(&config, ctx); err != nil {
		t.Fatalf("create proxy subscription: %v", err)
	}
	nodes := []model.ProxySubscriptionNode{
		{ProxyConfigurationID: config.ID, URL: "socks5://user:secret@127.0.0.1:1101", Active: true, HealthStatus: model.ProxyTestHealthHealthy, LatencyMS: 10},
		{ProxyConfigurationID: config.ID, URL: "socks5://127.0.0.1:1102", Active: true, HealthStatus: model.ProxyTestHealthHealthy, LatencyMS: 20},
	}
	if err := dbpkg.GetDB().Create(&nodes).Error; err != nil {
		t.Fatalf("create subscription nodes: %v", err)
	}
	if _, err := ProxyURLsForConfig(config.ID, ctx); err != nil {
		t.Fatalf("prime proxy candidates: %v", err)
	}
	failure := errors.New(`proxyconnect tcp: unexpected status: 429 Not Enough Bandwidth via socks5://user:secret@127.0.0.1:1101`)
	if err := ProxySubscriptionNodeReportFailure(config.ID, nodes[0].URL, failure, ctx); err != nil {
		t.Fatalf("report runtime proxy failure: %v", err)
	}

	available, err := ProxyURLsForConfig(config.ID, ctx)
	if err != nil {
		t.Fatalf("resolve candidates during quarantine: %v", err)
	}
	if len(available) != 1 || available[0] != nodes[1].URL {
		t.Fatalf("quarantined node remained available: %#v", available)
	}
	var quarantined model.ProxySubscriptionNode
	if err := dbpkg.GetDB().First(&quarantined, nodes[0].ID).Error; err != nil {
		t.Fatalf("read quarantined node: %v", err)
	}
	if quarantined.RuntimeFailureCount != 1 || quarantined.QuarantinedUntil == nil {
		t.Fatalf("runtime failure state not persisted: %+v", quarantined)
	}
	if strings.Contains(quarantined.LastRuntimeError, "secret") || strings.Contains(quarantined.LastRuntimeError, nodes[0].URL) {
		t.Fatalf("runtime failure leaked proxy credentials: %q", quarantined.LastRuntimeError)
	}
	configs, err := ProxyConfigurationList(ctx)
	if err != nil {
		t.Fatalf("list proxy configurations during quarantine: %v", err)
	}
	if len(configs) != 1 || configs[0].AvailableNodeCount != 1 || configs[0].QuarantinedNodeCount != 1 {
		t.Fatalf("unexpected quarantine list counts: %+v", configs)
	}

	if quarantined.QuarantinedUntil == nil {
		t.Fatal("quarantined node has no expiry")
	}
	if remaining := time.Until(*quarantined.QuarantinedUntil); remaining > 0 {
		time.Sleep(remaining + 20*time.Millisecond)
	}
	restored, err := ProxyURLsForConfig(config.ID, ctx)
	if err != nil {
		t.Fatalf("resolve candidates after quarantine: %v", err)
	}
	if len(restored) != 2 {
		t.Fatalf("node did not automatically return after quarantine: %#v", restored)
	}
	configs, err = ProxyConfigurationList(ctx)
	if err != nil {
		t.Fatalf("list proxy configurations after quarantine: %v", err)
	}
	if configs[0].AvailableNodeCount != 2 || configs[0].QuarantinedNodeCount != 0 {
		t.Fatalf("expired quarantine list counts were stale: %+v", configs[0])
	}
}

func TestProxySubscriptionSyncUpsertsAndDeactivatesMissingNodes(t *testing.T) {
	initProxySubscriptionTestDB(t)
	previousHealthURL := proxySubscriptionHealthURL
	proxySubscriptionHealthURL = "http://example.com/health"
	t.Cleanup(func() { proxySubscriptionHealthURL = previousHealthURL })
	proxyA, _ := newSequencedTestProxy(t, []int{http.StatusNoContent})
	proxyB, _ := newSequencedTestProxy(t, []int{http.StatusNoContent})
	var sourceContent atomic.Value
	sourceContent.Store(proxyA.URL + "\n")
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(sourceContent.Load().(string)))
	}))
	t.Cleanup(source.Close)

	ctx := context.Background()
	config := model.ProxyConfiguration{
		Name:                   "sync subscription",
		URL:                    source.URL,
		Type:                   model.ProxyConfigurationTypeSubscription,
		Enabled:                true,
		RefreshIntervalMinutes: 30,
	}
	if err := ProxyConfigurationCreate(&config, ctx); err != nil {
		t.Fatalf("create proxy subscription: %v", err)
	}
	first, err := ProxySubscriptionSync(config.ID, ctx)
	if err != nil {
		t.Fatalf("first subscription sync: %v", err)
	}
	if first.FetchedCount != 1 || first.HealthyCount != 1 {
		t.Fatalf("unexpected first sync result: %+v", first)
	}

	sourceContent.Store(proxyB.URL + "\n")
	second, err := ProxySubscriptionSync(config.ID, ctx)
	if err != nil {
		t.Fatalf("second subscription sync: %v", err)
	}
	if second.FetchedCount != 1 || second.HealthyCount != 1 {
		t.Fatalf("unexpected second sync result: %+v", second)
	}
	nodes, err := ProxySubscriptionNodes(config.ID, ctx)
	if err != nil {
		t.Fatalf("list subscription nodes: %v", err)
	}
	if len(nodes) != 2 {
		t.Fatalf("expected current and stale node records, got %+v", nodes)
	}
	activeByURL := make(map[string]bool, len(nodes))
	for _, node := range nodes {
		activeByURL[node.URL] = node.Active
	}
	if activeByURL[proxyA.URL] || !activeByURL[proxyB.URL] {
		t.Fatalf("expected old node inactive and new node active, got %#v", activeByURL)
	}
}

func TestProxySubscriptionSyncRetainsLastKnownGoodNodesWhenAllChecksFail(t *testing.T) {
	initProxySubscriptionTestDB(t)
	previousHealthURL := proxySubscriptionHealthURL
	proxySubscriptionHealthURL = "http://example.com/health"
	t.Cleanup(func() { proxySubscriptionHealthURL = previousHealthURL })
	healthyProxy, _ := newSequencedTestProxy(t, []int{http.StatusNoContent})
	failedProxy, _ := newSequencedTestProxy(t, []int{http.StatusBadGateway})
	var sourceContent atomic.Value
	sourceContent.Store(healthyProxy.URL + "\n")
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(sourceContent.Load().(string)))
	}))
	t.Cleanup(source.Close)

	ctx := context.Background()
	config := model.ProxyConfiguration{
		Name:                   "last known good subscription",
		URL:                    source.URL,
		Type:                   model.ProxyConfigurationTypeSubscription,
		Enabled:                true,
		RefreshIntervalMinutes: 30,
	}
	if err := ProxyConfigurationCreate(&config, ctx); err != nil {
		t.Fatalf("create proxy subscription: %v", err)
	}
	if _, err := ProxySubscriptionSync(config.ID, ctx); err != nil {
		t.Fatalf("initial subscription sync: %v", err)
	}
	sourceContent.Store(failedProxy.URL + "\n")
	if _, err := ProxySubscriptionSync(config.ID, ctx); err == nil {
		t.Fatal("all-failed subscription sync unexpectedly succeeded")
	}

	available, err := ProxyURLsForConfig(config.ID, ctx)
	if err != nil {
		t.Fatalf("resolve last known good nodes: %v", err)
	}
	if len(available) != 1 || available[0] != healthyProxy.URL {
		t.Fatalf("last known good nodes were replaced: %#v", available)
	}
	updated, err := ProxyConfigurationGet(config.ID, ctx)
	if err != nil {
		t.Fatalf("read failed sync status: %v", err)
	}
	if updated.LastSyncStatus != model.ProxySubscriptionSyncFailed {
		t.Fatalf("sync status = %q, want failed", updated.LastSyncStatus)
	}
}

func TestProxyConfigurationUpdateKeepsExistingNodesUntilNewSourceSyncs(t *testing.T) {
	initProxySubscriptionTestDB(t)
	ctx := context.Background()
	config := model.ProxyConfiguration{
		Name:                   "editable subscription",
		URL:                    "https://example.com/old.txt",
		Type:                   model.ProxyConfigurationTypeSubscription,
		Enabled:                true,
		RefreshIntervalMinutes: 30,
	}
	if err := ProxyConfigurationCreate(&config, ctx); err != nil {
		t.Fatalf("create proxy subscription: %v", err)
	}
	node := model.ProxySubscriptionNode{
		ProxyConfigurationID: config.ID,
		URL:                  "socks5://127.0.0.1:1301",
		Active:               true,
		HealthStatus:         model.ProxyTestHealthHealthy,
	}
	if err := dbpkg.GetDB().Create(&node).Error; err != nil {
		t.Fatalf("create existing node: %v", err)
	}
	newURL := "https://example.com/new.txt"
	updated, err := ProxyConfigurationUpdate(&model.ProxyConfigurationUpdateRequest{ID: config.ID, URL: &newURL}, ctx)
	if err != nil {
		t.Fatalf("update subscription source: %v", err)
	}
	if updated.URL != newURL || updated.LastSyncStatus != model.ProxySubscriptionSyncIdle || updated.LastSyncAt != nil {
		t.Fatalf("unexpected updated subscription: %+v", updated)
	}
	available, err := ProxyURLsForConfig(config.ID, ctx)
	if err != nil {
		t.Fatalf("resolve retained nodes: %v", err)
	}
	if len(available) != 1 || available[0] != node.URL {
		t.Fatalf("existing nodes were disabled before new source synced: %#v", available)
	}
}

func TestProxyConfigurationUpdateWaitsForInFlightSubscriptionSync(t *testing.T) {
	initProxySubscriptionTestDB(t)
	previousHealthURL := proxySubscriptionHealthURL
	proxySubscriptionHealthURL = "http://example.com/health"
	t.Cleanup(func() { proxySubscriptionHealthURL = previousHealthURL })
	requestStarted := make(chan struct{})
	releaseHealthCheck := make(chan struct{})
	var signalOnce sync.Once
	var releaseOnce sync.Once
	proxyServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		signalOnce.Do(func() { close(requestStarted) })
		<-releaseHealthCheck
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(proxyServer.Close)
	t.Cleanup(func() { releaseOnce.Do(func() { close(releaseHealthCheck) }) })
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(proxyServer.URL + "\n"))
	}))
	t.Cleanup(source.Close)

	ctx := context.Background()
	config := model.ProxyConfiguration{
		Name:                   "concurrent subscription update",
		URL:                    source.URL,
		Type:                   model.ProxyConfigurationTypeSubscription,
		Enabled:                true,
		RefreshIntervalMinutes: 30,
	}
	if err := ProxyConfigurationCreate(&config, ctx); err != nil {
		t.Fatalf("create proxy subscription: %v", err)
	}
	syncDone := make(chan error, 1)
	go func() {
		_, err := ProxySubscriptionSync(config.ID, ctx)
		syncDone <- err
	}()
	select {
	case <-requestStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("subscription sync did not reach health check")
	}

	newURL := "https://example.com/new-source.txt"
	updateDone := make(chan error, 1)
	go func() {
		_, err := ProxyConfigurationUpdate(&model.ProxyConfigurationUpdateRequest{ID: config.ID, URL: &newURL}, ctx)
		updateDone <- err
	}()
	select {
	case err := <-updateDone:
		t.Fatalf("subscription update bypassed in-flight sync lock: %v", err)
	case <-time.After(30 * time.Millisecond):
	}
	releaseOnce.Do(func() { close(releaseHealthCheck) })
	if err := <-syncDone; err != nil {
		t.Fatalf("in-flight subscription sync: %v", err)
	}
	if err := <-updateDone; err != nil {
		t.Fatalf("subscription update after sync: %v", err)
	}
	updated, err := ProxyConfigurationGet(config.ID, ctx)
	if err != nil {
		t.Fatalf("read subscription after concurrent update: %v", err)
	}
	if updated.URL != newURL || updated.LastSyncStatus != model.ProxySubscriptionSyncIdle || updated.LastSyncAt != nil {
		t.Fatalf("stale sync overwrote subscription update: %+v", updated)
	}
}
