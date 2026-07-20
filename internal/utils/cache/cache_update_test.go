package cache

import (
	"sync"
	"testing"
)

type testCounter struct {
	Count int
}

func TestUpdateConcurrentIncrement(t *testing.T) {
	const (
		goroutines = 100
		increments = 1000
	)

	c := New[string, testCounter](16)

	var wg sync.WaitGroup
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			for j := 0; j < increments; j++ {
				c.Update("counter", func(old testCounter, _ bool) testCounter {
					old.Count++
					return old
				})
			}
		}()
	}
	wg.Wait()

	got, ok := c.Get("counter")
	if !ok {
		t.Fatal("expected counter to exist after updates")
	}
	if want := goroutines * increments; got.Count != want {
		t.Fatalf("lost updates: got %d, want %d", got.Count, want)
	}
}

func TestUpdateMissingKey(t *testing.T) {
	c := New[string, testCounter](16)

	var (
		called bool
		gotOld testCounter
		gotOK  bool
	)
	newValue := c.Update("missing", func(old testCounter, ok bool) testCounter {
		called = true
		gotOld = old
		gotOK = ok
		return testCounter{Count: 42}
	})
	if !called {
		t.Fatal("fn was not called")
	}
	if gotOK {
		t.Fatal("expected ok == false for missing key")
	}
	if gotOld != (testCounter{}) {
		t.Fatalf("expected zero value old for missing key, got %+v", gotOld)
	}
	if newValue.Count != 42 {
		t.Fatalf("Update returned %+v, want Count=42", newValue)
	}

	stored, ok := c.Get("missing")
	if !ok || stored.Count != 42 {
		t.Fatalf("Get after Update = (%+v, %v), want (Count=42, true)", stored, ok)
	}

	// A second Update on the now-existing key must see ok == true and the stored value.
	c.Update("missing", func(old testCounter, ok bool) testCounter {
		if !ok {
			t.Error("expected ok == true for existing key")
		}
		if old.Count != 42 {
			t.Errorf("expected old.Count == 42, got %d", old.Count)
		}
		return old
	})
}

func TestUpdateExistingMissingKeyDoesNotInsert(t *testing.T) {
	c := New[string, testCounter](16)

	called := false
	got, ok := c.UpdateExisting("missing", func(old testCounter) testCounter {
		called = true
		return testCounter{Count: 42}
	})
	if called {
		t.Fatal("fn must not be called for missing key")
	}
	if ok {
		t.Fatal("expected ok == false for missing key")
	}
	if got != (testCounter{}) {
		t.Fatalf("expected zero value, got %+v", got)
	}
	if _, exists := c.Get("missing"); exists {
		t.Fatal("UpdateExisting must not insert a missing key")
	}
}

func TestUpdateExistingConcurrentIncrement(t *testing.T) {
	const (
		goroutines = 100
		increments = 1000
	)

	c := New[string, testCounter](16)
	c.Set("counter", testCounter{})

	var wg sync.WaitGroup
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			for j := 0; j < increments; j++ {
				if _, ok := c.UpdateExisting("counter", func(old testCounter) testCounter {
					old.Count++
					return old
				}); !ok {
					t.Error("expected existing key")
					return
				}
			}
		}()
	}
	wg.Wait()

	got, ok := c.Get("counter")
	if !ok {
		t.Fatal("expected counter to exist after updates")
	}
	if want := goroutines * increments; got.Count != want {
		t.Fatalf("lost updates: got %d, want %d", got.Count, want)
	}
}
