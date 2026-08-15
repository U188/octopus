package price

import (
	"sync"
	"testing"
	"time"
)

func TestGetLastUpdateTimeConcurrent(t *testing.T) {
	llmPriceLock.RLock()
	previous := lastUpdateTime
	llmPriceLock.RUnlock()
	t.Cleanup(func() {
		llmPriceLock.Lock()
		lastUpdateTime = previous
		llmPriceLock.Unlock()
	})

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < 100; i++ {
			llmPriceLock.Lock()
			lastUpdateTime = time.Unix(int64(i), 0)
			llmPriceLock.Unlock()
		}
	}()
	go func() {
		defer wg.Done()
		for range 100 {
			_ = GetLastUpdateTime()
		}
	}()
	wg.Wait()
}
