package op

import (
	"sync"
	"testing"

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
