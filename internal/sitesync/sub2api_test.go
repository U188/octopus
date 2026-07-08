package sitesync

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/U188/octopus/internal/model"
)

func TestSyncSub2APIUsernamePasswordLogin(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		switch r.URL.Path {
		case "/api/v1/auth/login":
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("decode login body failed: %v", err)
			}
			if body["email"] != "user@example.com" || body["password"] != "secret" {
				t.Fatalf("unexpected login body: %#v", body)
			}
			_, _ = w.Write([]byte(`{"code":0,"data":{"access_token":"sub2-session-token","refresh_token":"sub2-refresh-token","expires_in":3600,"token_type":"Bearer"}}`))
		case "/api/v1/keys":
			if r.Header.Get("Authorization") != "Bearer sub2-session-token" {
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = w.Write([]byte(`{"message":"unauthorized"}`))
				return
			}
			_, _ = w.Write([]byte(`{"code":0,"data":{"items":[{"id":11,"name":"managed-key","key":"sub2-user-key","group_id":7,"group_name":"VIP 7","enabled":true}]}}`))
		case "/api/v1/groups/available":
			if r.Header.Get("Authorization") != "Bearer sub2-session-token" {
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = w.Write([]byte(`{"message":"unauthorized"}`))
				return
			}
			_, _ = w.Write([]byte(`{"code":0,"data":{"groups":[{"id":7,"name":"vip"}]}}`))
		case "/api/v1/models":
			if r.Header.Get("Authorization") != "Bearer sub2-user-key" {
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = w.Write([]byte(`{"error":"unauthorized"}`))
				return
			}
			_, _ = w.Write([]byte(`{"code":0,"data":{"items":[{"id":"gpt-4o-mini"}]}}`))
		case "/api/v1/auth/me":
			if r.Header.Get("Authorization") != "Bearer sub2-session-token" {
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = w.Write([]byte(`{"message":"unauthorized"}`))
				return
			}
			_, _ = w.Write([]byte(`{"code":0,"data":{"balance":12.5}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	snapshot, err := syncSub2API(context.Background(), &model.Site{
		BaseURL:  server.URL,
		Platform: model.SitePlatformSub2API,
	}, &model.SiteAccount{
		CredentialType: model.SiteCredentialTypeUsernamePassword,
		Username:       "user@example.com",
		Password:       "secret",
	})
	if err != nil {
		t.Fatalf("syncSub2API returned error: %v", err)
	}
	if snapshot.accessToken != "sub2-session-token" || snapshot.refreshToken != "sub2-refresh-token" || snapshot.tokenExpiresAt <= 0 {
		t.Fatalf("expected login credentials in snapshot, got access=%q refresh=%q expires=%d", snapshot.accessToken, snapshot.refreshToken, snapshot.tokenExpiresAt)
	}
	if snapshot.balance != 12.5 {
		t.Fatalf("expected balance 12.5, got %v", snapshot.balance)
	}
	if len(snapshot.tokens) != 1 || snapshot.tokens[0].Token != "sub2-user-key" {
		t.Fatalf("expected managed token synced after login, got %+v", snapshot.tokens)
	}
}

func TestSyncSub2APIUsernamePasswordLoginRequiresAccessTokenWhen2FA(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":0,"data":{"requires_2fa":true,"temp_token":"tmp","user_email_masked":"u***@example.com"}}`))
	}))
	defer server.Close()

	_, err := syncSub2API(context.Background(), &model.Site{
		BaseURL:  server.URL,
		Platform: model.SitePlatformSub2API,
	}, &model.SiteAccount{
		CredentialType: model.SiteCredentialTypeUsernamePassword,
		Username:       "user@example.com",
		Password:       "secret",
	})
	if err == nil {
		t.Fatalf("expected 2FA login to fail")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "2fa") || !strings.Contains(strings.ToLower(err.Error()), "access token") {
		t.Fatalf("expected 2FA access token hint, got %v", err)
	}
}

func TestSyncSub2APIUsesManagedKeyAndAPIModelEndpoint(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		switch r.URL.Path {
		case "/api/v1/keys":
			if r.Header.Get("Authorization") != "Bearer sub2-session-token" {
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = w.Write([]byte(`{"message":"unauthorized"}`))
				return
			}
			_, _ = w.Write([]byte(`{"code":0,"data":{"items":[{"id":11,"name":"managed-key","key":"sub2-user-key","group_id":7,"group_name":"VIP 7","enabled":true}]}}`))
		case "/api/v1/groups/available":
			if r.Header.Get("Authorization") != "Bearer sub2-session-token" {
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = w.Write([]byte(`{"message":"unauthorized"}`))
				return
			}
			_, _ = w.Write([]byte(`{"code":0,"data":{"groups":[{"id":7,"name":"vip"}]}}`))
		case "/v1/models":
			http.NotFound(w, r)
		case "/api/v1/models":
			if r.Header.Get("Authorization") != "Bearer sub2-user-key" {
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = w.Write([]byte(`{"error":"unauthorized"}`))
				return
			}
			_, _ = w.Write([]byte(`{"code":0,"data":{"items":[{"id":"gpt-4o-mini"},{"name":"claude-3-5-sonnet"}]}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	snapshot, err := syncSub2API(context.Background(), &model.Site{
		BaseURL:  server.URL,
		Platform: model.SitePlatformSub2API,
	}, &model.SiteAccount{
		CredentialType: model.SiteCredentialTypeAccessToken,
		AccessToken:    "Bearer sub2-session-token",
	})
	if err != nil {
		t.Fatalf("syncSub2API returned error: %v", err)
	}
	if len(snapshot.tokens) != 1 {
		t.Fatalf("expected one managed token, got %+v", snapshot.tokens)
	}
	if snapshot.tokens[0].Token != "sub2-user-key" || snapshot.tokens[0].GroupKey != "7" {
		t.Fatalf("expected managed token with group 7, got %+v", snapshot.tokens[0])
	}
	if len(snapshot.groups) != 1 || snapshot.groups[0].GroupKey != "7" || snapshot.groups[0].Name != "vip" {
		t.Fatalf("expected parsed group 7/vip, got %+v", snapshot.groups)
	}
	if len(snapshot.models) != 2 {
		t.Fatalf("expected models discovered from /api/v1/models, got %+v", snapshot.models)
	}
}

func TestSyncSub2APIReturnsMissingKeySnapshotWhenKeyListIsEmpty(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		switch r.URL.Path {
		case "/api/v1/keys", "/api/v1/api-keys":
			_, _ = w.Write([]byte(`{"code":0,"data":[]}`))
		case "/api/v1/groups/available", "/api/v1/groups", "/api/v1/group":
			_, _ = w.Write([]byte(`{"code":0,"data":[{"id":7,"name":"vip"}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	snapshot, err := syncSub2API(context.Background(), &model.Site{
		BaseURL:  server.URL,
		Platform: model.SitePlatformSub2API,
	}, &model.SiteAccount{
		CredentialType: model.SiteCredentialTypeAccessToken,
		AccessToken:    "sub2-session-token",
	})
	if err == nil {
		t.Fatalf("expected syncSub2API to report missing key")
	}
	if snapshot == nil {
		t.Fatalf("expected missing key snapshot")
	}
	if snapshot.status != model.SiteExecutionStatusFailed {
		t.Fatalf("expected failed snapshot status, got %+v", snapshot)
	}
	if len(snapshot.groupResults) != 1 || snapshot.groupResults[0].Status != siteGroupSyncStatusMissingKey {
		t.Fatalf("expected missing key group result, got %+v", snapshot.groupResults)
	}
	if !strings.Contains(strings.ToLower(err.Error()), "missing") && !strings.Contains(err.Error(), "缺少") && !strings.Contains(err.Error(), "没有可用 Key") {
		t.Fatalf("expected missing key error, got %v", err)
	}
}

func TestFetchSub2APITokensReturnsEnvelopeError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":401,"message":"token expired","data":null}`))
	}))
	defer server.Close()

	_, err := fetchSub2APITokens(context.Background(), &model.Site{BaseURL: server.URL}, &model.SiteAccount{}, "expired-token")
	if err == nil {
		t.Fatalf("expected envelope error")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "expired") {
		t.Fatalf("expected token expired error, got %v", err)
	}
}

func TestBuildSub2APIModelEndpointURLsIncludesAntigravityV1(t *testing.T) {
	endpoints := buildSub2APIModelEndpointURLs(&model.Site{BaseURL: "https://example.com"})
	for _, endpoint := range endpoints {
		if endpoint == "https://example.com/antigravity/v1/models" {
			return
		}
	}
	t.Fatalf("expected antigravity v1 models endpoint, got %+v", endpoints)
}

func TestParseSub2APIModelNamesReturnsEnvelopeError(t *testing.T) {
	_, err := parseSub2APIModelNames(map[string]any{
		"code":    float64(401),
		"message": "expired key",
	})
	if err == nil {
		t.Fatalf("expected envelope error")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "expired") {
		t.Fatalf("expected expired key error, got %v", err)
	}
}
