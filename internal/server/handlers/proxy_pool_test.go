package handlers

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/U188/octopus/internal/model"
)

func TestProxyTestAuditMetadataOmitsProxyURLAndCredentials(t *testing.T) {
	req := model.ProxyTestRequest{
		ProxyURL:       "socks5://user:password@proxy.example:1080",
		UseSystemProxy: true,
		URL:            "https://api.example.com/v1/models?token=secret",
	}
	if action := proxyTestAuditAction(req); action != "system_proxy.test" {
		t.Fatalf("expected system proxy audit action, got %q", action)
	}
	detail := proxyTestAuditDetail(req)
	encoded, err := json.Marshal(detail)
	if err != nil {
		t.Fatal(err)
	}
	text := string(encoded)
	if strings.Contains(text, "password") || strings.Contains(text, "proxy.example") || strings.Contains(text, "token") || strings.Contains(text, "secret") {
		t.Fatalf("audit detail leaked proxy or target credentials: %s", text)
	}
	if detail["target_host"] != "api.example.com" || detail["source"] != "system" {
		t.Fatalf("unexpected audit detail: %#v", detail)
	}
}

func TestProxyTestAuditMetadataIncludesPoolConfigurationID(t *testing.T) {
	id := 42
	req := model.ProxyTestRequest{ProxyConfigID: &id}
	if action := proxyTestAuditAction(req); action != "proxy_pool.test" {
		t.Fatalf("expected proxy pool audit action, got %q", action)
	}
	detail := proxyTestAuditDetail(req)
	if detail["source"] != "pool" || detail["proxy_config_id"] != id || detail["target_host"] != "www.google.com" {
		t.Fatalf("unexpected audit detail: %#v", detail)
	}
}
