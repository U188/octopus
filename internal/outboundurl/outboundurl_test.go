package outboundurl

import (
	"net/http"
	"net/netip"
	"testing"
)

type closeIdleConnectionsSpy struct {
	closed bool
}

func (s *closeIdleConnectionsSpy) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, nil
}

func (s *closeIdleConnectionsSpy) CloseIdleConnections() {
	s.closed = true
}

func TestForbiddenIPClassification(t *testing.T) {
	for _, raw := range []string{"127.0.0.1", "10.0.0.1", "169.254.169.254", "100.64.0.1", "::1"} {
		if !isForbiddenIP(netip.MustParseAddr(raw)) {
			t.Fatalf("expected %s to be forbidden", raw)
		}
	}
	for _, raw := range []string{"1.1.1.1", "8.8.8.8", "2606:4700:4700::1111"} {
		if isForbiddenIP(netip.MustParseAddr(raw)) {
			t.Fatalf("expected %s to be public", raw)
		}
	}
}

func TestValidateHTTPURLSyntax(t *testing.T) {
	if err := ValidateHTTPURL("ftp://example.com/file"); err == nil {
		t.Fatal("expected non-http scheme to fail")
	}
	if err := ValidateHTTPURL("https://user:pass@example.com"); err == nil {
		t.Fatal("expected embedded credentials to fail")
	}
	if err := ValidateHTTPURL("https://example.com"); err != nil {
		t.Fatalf("expected public HTTPS URL to pass: %v", err)
	}
}

func TestGoTestRuntimeIsDetectedWithoutExecutableNameCheck(t *testing.T) {
	if !runningUnderGoTest() {
		t.Fatal("expected Go test runtime to be detected from registered test flags")
	}
}

func TestWrapTransportForwardsCloseIdleConnections(t *testing.T) {
	base := &closeIdleConnectionsSpy{}
	wrapped := WrapTransport(base)
	closer, ok := wrapped.(interface{ CloseIdleConnections() })
	if !ok {
		t.Fatalf("wrapped transport %T does not expose CloseIdleConnections", wrapped)
	}
	closer.CloseIdleConnections()
	if !base.closed {
		t.Fatal("wrapped transport did not close idle connections on the base transport")
	}
}
