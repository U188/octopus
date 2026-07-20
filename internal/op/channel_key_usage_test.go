package op

import (
	"sync"
	"testing"

	"github.com/U188/octopus/internal/model"
)

func setupChannelKeyUsageFixture(t *testing.T, channelID, keyID int) {
	t.Helper()
	key := model.ChannelKey{ID: keyID, ChannelID: channelID, ChannelKey: "sk-test", Enabled: true}
	ch := model.Channel{ID: channelID, Name: "usage-test", Enabled: true, Keys: []model.ChannelKey{key}}
	channelCache.Set(channelID, ch)
	channelKeyCache.Set(keyID, key)
	t.Cleanup(func() {
		channelCache.Del(channelID)
		channelKeyCache.Del(keyID)
		channelKeyCacheNeedUpdateLock.Lock()
		delete(channelKeyCacheNeedUpdate, keyID)
		channelKeyCacheNeedUpdateLock.Unlock()
	})
}

// 回归：并发请求各持旧快照整结构回写会互相覆盖丢计费；
// ChannelKeyAddUsage 必须在缓存锁内对当前值做增量，累计不丢。
func TestChannelKeyAddUsageConcurrentNoLostCost(t *testing.T) {
	const (
		channelID  = 910001
		keyID      = 910002
		goroutines = 50
		perG       = 200
		delta      = 0.5
	)
	setupChannelKeyUsageFixture(t, channelID, keyID)

	var wg sync.WaitGroup
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			for j := 0; j < perG; j++ {
				if err := ChannelKeyAddUsage(channelID, keyID, delta, 200, 1700000000); err != nil {
					t.Errorf("ChannelKeyAddUsage: %v", err)
					return
				}
			}
		}()
	}
	wg.Wait()

	want := float64(goroutines*perG) * delta
	got, ok := channelKeyCache.Get(keyID)
	if !ok {
		t.Fatal("key missing from channelKeyCache")
	}
	if got.TotalCost != want {
		t.Fatalf("channelKeyCache lost updates: got %v, want %v", got.TotalCost, want)
	}

	ch, ok := channelCache.Get(channelID)
	if !ok || len(ch.Keys) != 1 {
		t.Fatalf("channel missing or keys corrupted: ok=%v keys=%d", ok, len(ch.Keys))
	}
	if ch.Keys[0].TotalCost != want {
		t.Fatalf("Channel.Keys runtime view lost updates: got %v, want %v", ch.Keys[0].TotalCost, want)
	}
}

// 回归：ChannelKeyAddUsage / ChannelKeyUpdate 不得用旧 Channel 快照整体回写，
// 否则管理员并发禁用渠道会被热路径"复活"。
func TestChannelKeyUsageDoesNotResurrectDisabledChannel(t *testing.T) {
	const (
		channelID = 920001
		keyID     = 920002
	)
	setupChannelKeyUsageFixture(t, channelID, keyID)

	// 模拟管理员禁用渠道（直接改缓存中的权威状态）
	if _, ok := channelCache.UpdateExisting(channelID, func(ch model.Channel) model.Channel {
		ch.Enabled = false
		return ch
	}); !ok {
		t.Fatal("channel missing")
	}

	if err := ChannelKeyAddUsage(channelID, keyID, 1.0, 200, 1700000000); err != nil {
		t.Fatalf("ChannelKeyAddUsage: %v", err)
	}
	if ch, _ := channelCache.Get(channelID); ch.Enabled {
		t.Fatal("ChannelKeyAddUsage resurrected disabled channel")
	}

	key, _ := channelKeyCache.Get(keyID)
	if err := ChannelKeyUpdate(key); err != nil {
		t.Fatalf("ChannelKeyUpdate: %v", err)
	}
	if ch, _ := channelCache.Get(channelID); ch.Enabled {
		t.Fatal("ChannelKeyUpdate resurrected disabled channel")
	}
}

// 渠道或 Key 已被删除时不得凭空重建缓存条目（否则 SaveCache 会向 DB 写孤儿行）。
func TestChannelKeyAddUsageMissingEntries(t *testing.T) {
	if err := ChannelKeyAddUsage(930001, 930002, 1.0, 200, 1700000000); err == nil {
		t.Fatal("expected error for missing channel")
	}
	if channelKeyCache.Exists(930002) {
		t.Fatal("must not create key entry for missing channel")
	}

	const channelID = 930003
	channelCache.Set(channelID, model.Channel{ID: channelID, Name: "no-key", Enabled: true})
	t.Cleanup(func() { channelCache.Del(channelID) })
	if err := ChannelKeyAddUsage(channelID, 930004, 1.0, 200, 1700000000); err == nil {
		t.Fatal("expected error for missing key")
	}
	if channelKeyCache.Exists(930004) {
		t.Fatal("must not create missing key entry")
	}
}
