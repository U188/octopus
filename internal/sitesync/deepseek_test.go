package sitesync

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/U188/octopus/internal/model"
)

func TestFetchDeepSeekBalance(t *testing.T) {
	var observedAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/user/balance" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		observedAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"is_available": true,
			"balance_infos": []map[string]any{
				{"currency": "CNY", "total_balance": "12.340000", "granted_balance": "2.340000", "topped_up_balance": "10.000000"},
				{"currency": "USD", "total_balance": "1.250000", "granted_balance": "0.000000", "topped_up_balance": "1.250000"},
			},
		})
	}))
	defer server.Close()

	site := &model.Site{Platform: model.SitePlatformDeepSeek, BaseURL: server.URL}
	account := &model.SiteAccount{Name: "deepseek", CredentialType: model.SiteCredentialTypeAPIKey, APIKey: "sk-test"}
	balance := fetchDeepSeekBalance(context.Background(), site, account, "sk-test")

	if observedAuth != "Bearer sk-test" {
		t.Fatalf("expected bearer api key, got %q", observedAuth)
	}
	if balance != 13.59 {
		t.Fatalf("expected balance 13.59, got %v", balance)
	}
}

func TestSyncDeepSeekOfficialIncludesBalance(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/models":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data": []map[string]any{
					{"id": "deepseek-v4-flash"},
					{"id": "deepseek-v4-pro"},
				},
			})
		case "/user/balance":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"is_available": true,
				"balance_infos": []map[string]any{
					{"currency": "CNY", "total_balance": "8.000000"},
				},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	site := &model.Site{Platform: model.SitePlatformDeepSeek, BaseURL: server.URL}
	account := &model.SiteAccount{Name: "deepseek", CredentialType: model.SiteCredentialTypeAPIKey, APIKey: "sk-test"}
	snapshot, err := syncOfficialPlatform(context.Background(), site, account)
	if err != nil {
		t.Fatalf("syncOfficialPlatform returned error: %v", err)
	}
	if snapshot.balance != 8 {
		t.Fatalf("expected balance 8, got %v", snapshot.balance)
	}
	if len(snapshot.models) != 2 {
		t.Fatalf("expected 2 models, got %d", len(snapshot.models))
	}
}
