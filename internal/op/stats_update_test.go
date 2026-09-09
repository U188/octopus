package op

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/U188/octopus/internal/model"
)

// 回归：统计更新原先是 Get→Add→Set，三处（channel/apikey/model）在高并发下互相覆盖少计。
// 改为缓存锁内原子读改写后，并发累计必须精确。
func TestStatsUpdateConcurrentNoLostCounts(t *testing.T) {
	const (
		id         = 940001
		goroutines = 50
		perG       = 200
	)
	t.Cleanup(func() {
		statsChannelCache.Del(id)
		statsAPIKeyCache.Del(id)
		statsModelCache.Del(id)
		statsChannelCacheNeedUpdateLock.Lock()
		delete(statsChannelCacheNeedUpdate, id)
		statsChannelCacheNeedUpdateLock.Unlock()
		statsAPIKeyCacheNeedUpdateLock.Lock()
		delete(statsAPIKeyCacheNeedUpdate, id)
		statsAPIKeyCacheNeedUpdateLock.Unlock()
		statsModelCacheNeedUpdateLock.Lock()
		delete(statsModelCacheNeedUpdate, id)
		statsModelCacheNeedUpdateLock.Unlock()
	})

	metrics := model.StatsMetrics{InputToken: 1, RequestSuccess: 1}

	var wg sync.WaitGroup
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			for j := 0; j < perG; j++ {
				_ = StatsChannelUpdate(id, metrics)
				_ = StatsAPIKeyUpdate(id, metrics)
				_ = StatsModelUpdate(model.StatsModel{ID: id, StatsMetrics: metrics})
			}
		}()
	}
	wg.Wait()

	want := int64(goroutines * perG)
	if got, _ := statsChannelCache.Get(id); got.InputToken != want || got.RequestSuccess != want {
		t.Fatalf("StatsChannelUpdate lost counts: got %+v, want %d", got.StatsMetrics, want)
	}
	if got, _ := statsAPIKeyCache.Get(id); got.InputToken != want || got.RequestSuccess != want {
		t.Fatalf("StatsAPIKeyUpdate lost counts: got %+v, want %d", got.StatsMetrics, want)
	}
	if got, _ := statsModelCache.Get(id); got.InputToken != want || got.RequestSuccess != want {
		t.Fatalf("StatsModelUpdate lost counts: got %+v, want %d", got.StatsMetrics, want)
	}
}

func TestAPIKeyQuotaDailyRequestsStopAndReset(t *testing.T) {
	const id = 940002
	cleanupStatsAPIKeyTest(t, id)
	key := model.APIKey{ID: id, MaxDailyRequests: 2}
	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.FixedZone("test", 8*60*60))

	if got := APIKeyQuotaCheck(key, true, now); got != APIKeyQuotaAllowed {
		t.Fatalf("first request status = %v", got)
	}
	if got := APIKeyQuotaCheck(key, true, now); got != APIKeyQuotaAllowed {
		t.Fatalf("second request status = %v", got)
	}
	if got := APIKeyQuotaCheck(key, true, now); got != APIKeyQuotaDailyRequestsExceeded {
		t.Fatalf("third request status = %v, want daily requests exceeded", got)
	}
	if got := APIKeyQuotaCheck(key, false, now); got != APIKeyQuotaDailyRequestsExceeded {
		t.Fatalf("non-counted request after limit = %v, want stopped key", got)
	}

	tomorrow := now.AddDate(0, 0, 1)
	if got := APIKeyQuotaCheck(key, true, tomorrow); got != APIKeyQuotaAllowed {
		t.Fatalf("first request next day status = %v", got)
	}
	stats, _ := statsAPIKeyCache.Get(id)
	if stats.DailyDate != "20260910" || stats.DailyRequestCount != 1 || stats.DailyCost != 0 {
		t.Fatalf("daily stats after reset = %+v", stats)
	}
}

func TestAPIKeyQuotaStopsAtExactCostLimit(t *testing.T) {
	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)

	const totalID = 940003
	cleanupStatsAPIKeyTest(t, totalID)
	if err := statsAPIKeyUpdateAt(totalID, model.StatsMetrics{InputCost: 4, OutputCost: 6}, now); err != nil {
		t.Fatal(err)
	}
	if got := APIKeyQuotaCheck(model.APIKey{ID: totalID, MaxCost: 10}, false, now); got != APIKeyQuotaTotalCostExceeded {
		t.Fatalf("total cost status = %v, want total cost exceeded", got)
	}

	const dailyID = 940004
	cleanupStatsAPIKeyTest(t, dailyID)
	if err := statsAPIKeyUpdateAt(dailyID, model.StatsMetrics{InputCost: 4, OutputCost: 6}, now); err != nil {
		t.Fatal(err)
	}
	key := model.APIKey{ID: dailyID, MaxCost: 100, MaxDailyCost: 10}
	if got := APIKeyQuotaCheck(key, false, now); got != APIKeyQuotaDailyCostExceeded {
		t.Fatalf("daily cost status = %v, want daily cost exceeded", got)
	}
	if got := APIKeyQuotaCheck(key, false, now.AddDate(0, 0, 1)); got != APIKeyQuotaAllowed {
		t.Fatalf("daily cost next day status = %v, want allowed", got)
	}
}

func TestAPIKeyQuotaDailyRequestsConcurrent(t *testing.T) {
	const (
		id    = 940005
		limit = 10
	)
	cleanupStatsAPIKeyTest(t, id)
	key := model.APIKey{ID: id, MaxDailyRequests: limit}
	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)

	var allowed atomic.Int64
	var wg sync.WaitGroup
	for range 100 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if APIKeyQuotaCheck(key, true, now) == APIKeyQuotaAllowed {
				allowed.Add(1)
			}
		}()
	}
	wg.Wait()

	if got := allowed.Load(); got != limit {
		t.Fatalf("allowed requests = %d, want %d", got, limit)
	}
	stats, _ := statsAPIKeyCache.Get(id)
	if stats.DailyRequestCount != limit {
		t.Fatalf("daily request count = %d, want %d", stats.DailyRequestCount, limit)
	}
}

func cleanupStatsAPIKeyTest(t *testing.T, id int) {
	t.Helper()
	statsAPIKeyCache.Del(id)
	statsAPIKeyCacheNeedUpdateLock.Lock()
	delete(statsAPIKeyCacheNeedUpdate, id)
	statsAPIKeyCacheNeedUpdateLock.Unlock()
	t.Cleanup(func() {
		statsAPIKeyCache.Del(id)
		statsAPIKeyCacheNeedUpdateLock.Lock()
		delete(statsAPIKeyCacheNeedUpdate, id)
		statsAPIKeyCacheNeedUpdateLock.Unlock()
	})
}
