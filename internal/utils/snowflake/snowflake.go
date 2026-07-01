package snowflake

import (
	"sync"
	"time"
)

var (
	sfMutex    sync.Mutex
	sfLastTime int64
)

// GenerateID 生成唯一ID
// 基于毫秒时间戳，当同一毫秒内调用时等待到下一毫秒
func GenerateID() int64 {
	sfMutex.Lock()
	defer sfMutex.Unlock()

	now := time.Now().UnixMilli()

	if now <= sfLastTime {
		sfLastTime++
		return sfLastTime
	}

	sfLastTime = now
	return now
}
