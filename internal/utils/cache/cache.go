// This implementation is based on and modified from https://github.com/fanjindong/go-cache
package cache

import (
	"fmt"

	"github.com/cespare/xxhash/v2"
)

func keyToString[K comparable](key K) string {
	return fmt.Sprintf("%v", key)
}

type Cache[K comparable, V any] interface {
	Set(k K, v V)
	Get(k K) (V, bool)
	GetOrSet(k K, v V) (V, bool)
	Update(k K, fn func(old V, ok bool) V) V
	UpdateExisting(k K, fn func(old V) V) (V, bool)
	GetAll() map[K]V
	Del(keys ...K) int
	Exists(keys ...K) bool
	Len() int
	Clear()
}

func New[K comparable, V any](shards int) Cache[K, V] {
	if shards <= 0 {
		shards = 1024
	}

	c := &cache[K, V]{
		shards:    make([]*shard[K, V], shards),
		shardMask: uint64(shards - 1),
	}
	for i := 0; i < shards; i++ {
		c.shards[i] = &shard[K, V]{hashmap: map[K]V{}}
	}

	return c
}

type cache[K comparable, V any] struct {
	shards    []*shard[K, V]
	shardMask uint64
}

func (c *cache[K, V]) Set(k K, v V) {
	hashedKey := xxhash.Sum64String(keyToString(k))
	shard := c.getShard(hashedKey)
	shard.set(k, v)
}

func (c *cache[K, V]) Get(k K) (V, bool) {
	hashedKey := xxhash.Sum64String(keyToString(k))
	shard := c.getShard(hashedKey)
	return shard.get(k)
}

func (c *cache[K, V]) GetOrSet(k K, v V) (V, bool) {
	hashedKey := xxhash.Sum64String(keyToString(k))
	shard := c.getShard(hashedKey)
	return shard.getOrSet(k, v)
}

func (c *cache[K, V]) Update(k K, fn func(old V, ok bool) V) V {
	hashedKey := xxhash.Sum64String(keyToString(k))
	shard := c.getShard(hashedKey)
	return shard.update(k, fn)
}

// UpdateExisting 仅当 k 存在时在锁内应用 fn 并写回，返回新值与是否命中。
// 与 Update 的 upsert 语义不同：条目不存在时不写入，用于避免在并发删除窗口中重建已删除的条目。
func (c *cache[K, V]) UpdateExisting(k K, fn func(old V) V) (V, bool) {
	hashedKey := xxhash.Sum64String(keyToString(k))
	shard := c.getShard(hashedKey)
	return shard.updateExisting(k, fn)
}

func (c *cache[K, V]) GetAll() map[K]V {
	result := make(map[K]V)
	for _, shard := range c.shards {
		shardData := shard.getAll()
		for k, v := range shardData {
			result[k] = v
		}
	}
	return result
}

func (c *cache[K, V]) Del(ks ...K) int {
	var count int
	for _, k := range ks {
		hashedKey := xxhash.Sum64String(keyToString(k))
		shard := c.getShard(hashedKey)
		count += shard.del(k)
	}
	return count
}

func (c *cache[K, V]) Exists(ks ...K) bool {
	for _, k := range ks {
		if _, found := c.Get(k); !found {
			return false
		}
	}
	return true
}

func (c *cache[K, V]) Len() int {
	var count int
	for _, shard := range c.shards {
		count += shard.len()
	}
	return count
}

func (c *cache[K, V]) getShard(hashedKey uint64) (shard *shard[K, V]) {
	return c.shards[hashedKey&c.shardMask]
}

func (c *cache[K, V]) Clear() {
	for _, s := range c.shards {
		s.clear()
	}
}
