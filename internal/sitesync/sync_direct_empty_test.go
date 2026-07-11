package sitesync

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/U188/octopus/internal/model"
)

func TestSyncWithDirectTokenEmptyModelsIsSuccess(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/models":
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write([]byte("<html><head><title>Hlool API</title></head><body>ok</body></html>"))
		case "/v1/models":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"object":"list","data":[],"success":true}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	snapshot, err := syncWithDirectToken(context.Background(), &model.Site{
		Platform: model.SitePlatformAPI,
		BaseURL:  server.URL,
	}, &model.SiteAccount{
		Name:           "hlool",
		CredentialType: model.SiteCredentialTypeAPIKey,
		APIKey:         "sk-test",
		Enabled:        true,
		AutoSync:       true,
	}, "sk-test", "manual")
	if err != nil {
		t.Fatalf("expected empty models to succeed without error, got %v", err)
	}
	if snapshot == nil {
		t.Fatal("expected snapshot")
	}
	if snapshot.status != model.SiteExecutionStatusSuccess {
		t.Fatalf("expected success status for authoritative empty models, got %q", snapshot.status)
	}
	if len(snapshot.models) != 0 {
		t.Fatalf("expected zero models, got %+v", snapshot.models)
	}
	if !strings.Contains(snapshot.message, "没有可用模型") {
		t.Fatalf("expected explicit no-model message, got %q", snapshot.message)
	}
	if strings.Contains(snapshot.message, "同步失败") {
		t.Fatalf("empty models must not present as sync failure, got %q", snapshot.message)
	}
}
