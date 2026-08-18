package price

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestFetchPriceBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("User-Agent") == "" {
			t.Fatal("expected user agent")
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"openai":{"models":{}}}`))
	}))
	t.Cleanup(server.Close)

	body, err := fetchPriceBody(context.Background(), server.Client(), server.URL)
	if err != nil {
		t.Fatalf("fetch price body: %v", err)
	}
	if !strings.Contains(string(body), `"openai"`) {
		t.Fatalf("unexpected body: %s", body)
	}
}

func TestFetchPriceBodyRejectsNonOK(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "upstream unavailable", http.StatusBadGateway)
	}))
	t.Cleanup(server.Close)

	_, err := fetchPriceBody(context.Background(), server.Client(), server.URL)
	if err == nil || !strings.Contains(err.Error(), "502") {
		t.Fatalf("expected status error, got %v", err)
	}
}

func TestProviderIncludesXiaomi(t *testing.T) {
	for _, provider := range Provider {
		if provider == "xiaomi" {
			return
		}
	}
	t.Fatal("xiaomi provider is missing")
}

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
