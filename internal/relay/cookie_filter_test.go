package relay

import (
	"net/http"
	"testing"

	dbmodel "github.com/U188/octopus/internal/model"
)

// 回归：Cookie 属于敏感认证头，不得转发到上游；Set-Cookie 不得从上游回传下游。
func TestHopByHopFiltersCookies(t *testing.T) {
	for _, h := range []string{"cookie", "set-cookie"} {
		if !hopByHopHeaders[h] {
			t.Errorf("%q must be in hopByHopHeaders filter", h)
		}
	}
}

// 请求头复制：客户端 Cookie 不应出现在转发给上游的请求里。
func TestCopyProxyHeadersDropsCookie(t *testing.T) {
	src := http.Header{}
	src.Set("Cookie", "session=secret")
	src.Set("Content-Language", "en")

	dst := http.Header{}
	copyProxyHeaders(src, &dbmodel.Channel{}, dst)

	if got := dst.Get("Cookie"); got != "" {
		t.Fatalf("Cookie must not be forwarded upstream, got %q", got)
	}
	if got := dst.Get("Content-Language"); got != "en" {
		t.Fatalf("non-sensitive header must be forwarded, got %q", got)
	}
}

// 响应头复制：上游 Set-Cookie 不应回传给下游客户端。
func TestCopyProxyResponseHeadersDropsSetCookie(t *testing.T) {
	src := http.Header{}
	src.Add("Set-Cookie", "up=1")
	src.Set("Content-Type", "application/json")

	dst := http.Header{}
	copyProxyResponseHeaders(dst, src)

	if got := dst.Values("Set-Cookie"); len(got) != 0 {
		t.Fatalf("Set-Cookie must not be relayed downstream, got %v", got)
	}
	if got := dst.Get("Content-Type"); got != "application/json" {
		t.Fatalf("Content-Type must be relayed, got %q", got)
	}
}
