package snowflake

import (
	"testing"
	"time"
)

func TestAdvanceToPreventsRestartCollision(t *testing.T) {
	sfMutex.Lock()
	oldLastTime := sfLastTime
	sfLastTime = 0
	sfMutex.Unlock()
	t.Cleanup(func() {
		sfMutex.Lock()
		sfLastTime = oldLastTime
		sfMutex.Unlock()
	})

	floor := time.Now().UnixMilli() + 10_000
	AdvanceTo(floor)
	if got := GenerateID(); got != floor+1 {
		t.Fatalf("GenerateID() = %d, want %d", got, floor+1)
	}

	AdvanceTo(floor - 1)
	if got := GenerateID(); got != floor+2 {
		t.Fatalf("lower floor moved generator backwards: got %d, want %d", got, floor+2)
	}
}
