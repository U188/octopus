package op

import (
	"context"
	"testing"

	"github.com/U188/octopus/internal/model"
)

func TestResolveProxyTestURLUsesStoredSystemProxy(t *testing.T) {
	previous, existed := settingCache.Get(model.SettingKeyProxyURL)
	settingCache.Set(model.SettingKeyProxyURL, "SOCKS5://User:Pass@Proxy.Example:1080")
	defer func() {
		if existed {
			settingCache.Set(model.SettingKeyProxyURL, previous)
			return
		}
		settingCache.Del(model.SettingKeyProxyURL)
	}()

	actual, err := resolveProxyTestURL(model.ProxyTestRequest{UseSystemProxy: true}, context.Background())
	if err != nil {
		t.Fatalf("resolve stored system proxy: %v", err)
	}
	if actual != "socks5://User:Pass@proxy.example:1080" {
		t.Fatalf("expected normalized stored system proxy, got %q", actual)
	}
}

func TestResolveProxyTestURLUsesDraftURL(t *testing.T) {
	actual, err := resolveProxyTestURL(model.ProxyTestRequest{ProxyURL: " http://Proxy.Example:8080 "}, context.Background())
	if err != nil {
		t.Fatalf("resolve draft proxy: %v", err)
	}
	if actual != "http://proxy.example:8080" {
		t.Fatalf("expected normalized draft proxy, got %q", actual)
	}
}
