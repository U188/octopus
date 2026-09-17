// Command strip-proxy is a tiny reverse proxy that sits in front of Octopus and
// rewrites the two response headers that make embedding impossible.
//
// Why this exists: Octopus hardcodes `X-Frame-Options: DENY` and a CSP with
// `frame-ancestors 'none'`. ModelScope studios render the app inside an iframe,
// so the browser refuses to paint it and the studio page stays blank. The
// upstream Go source has no switch for this, so we strip/rewrite the headers on
// the way out instead of rebuilding the whole application.
package main

import (
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"strings"
)

func env(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}

// rewriteCSP replaces the frame-ancestors directive (or appends it when absent)
// with the supplied allow-list.
func rewriteCSP(csp, ancestors string) string {
	const dir = "frame-ancestors"
	i := strings.Index(csp, dir)
	if i < 0 {
		if csp == "" {
			return "default-src 'self'; frame-ancestors " + ancestors
		}
		return strings.TrimRight(csp, "; ") + "; frame-ancestors " + ancestors
	}
	// Find the end of the directive (next ';' or end of string).
	rest := csp[i+len(dir):]
	if j := strings.Index(rest, ";"); j >= 0 {
		return csp[:i] + dir + " " + ancestors + rest[j:]
	}
	return csp[:i] + dir + " " + ancestors
}

func main() {
	upstream := env("UPSTREAM", "http://127.0.0.1:8080")
	listen := env("LISTEN", ":7860")
	ancestors := env("FRAME_ANCESTORS",
		"https://www.modelscope.ai https://*.modelscope.ai https://*.modelscope.cn")

	u, err := url.Parse(upstream)
	if err != nil {
		log.Fatalf("strip-proxy: bad UPSTREAM %q: %v", upstream, err)
	}

	proxy := httputil.NewSingleHostReverseProxy(u)
	proxy.FlushInterval = -1 // stream SSE / chunked responses immediately

	// ModelScope terminates TLS in front of us, so the upstream must be told the
	// original request was HTTPS or it will skip HSTS / Secure cookies.
	origDirector := proxy.Director
	proxy.Director = func(r *http.Request) {
		origDirector(r)
		r.Header.Set("X-Forwarded-Proto", "https")
		if ip, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
			r.Header.Set("X-Real-IP", ip)
		}
	}

	proxy.ModifyResponse = func(r *http.Response) error {
		// X-Frame-Options has no wildcard syntax, so drop it entirely and let
		// CSP frame-ancestors do the access control.
		r.Header.Del("X-Frame-Options")
		r.Header.Set("Content-Security-Policy",
			rewriteCSP(r.Header.Get("Content-Security-Policy"), ancestors))
		return nil
	}

	log.Printf("strip-proxy: listening %s -> %s | frame-ancestors: %s", listen, upstream, ancestors)
	srv := &http.Server{Addr: listen, Handler: proxy}
	log.Fatal(srv.ListenAndServe())
}
