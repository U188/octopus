package model

import "testing"

func TestNormalizeIPWhitelist(t *testing.T) {
	got, err := NormalizeIPWhitelist(" 192.0.2.10, 192.0.2.0/24\n2001:db8::1 ")
	if err != nil {
		t.Fatalf("NormalizeIPWhitelist returned error: %v", err)
	}
	if got != "192.0.2.10/32,192.0.2.0/24,2001:db8::1/128" {
		t.Fatalf("normalized whitelist = %q", got)
	}
}

func TestIPWhitelistContainsSupportsExactAndCIDR(t *testing.T) {
	allowed, err := IPWhitelistContains("192.0.2.42", "192.0.2.0/24,2001:db8::1")
	if err != nil || !allowed {
		t.Fatalf("expected IPv4 CIDR match, allowed=%t err=%v", allowed, err)
	}
	allowed, err = IPWhitelistContains("2001:db8::1", "192.0.2.0/24,2001:db8::1")
	if err != nil || !allowed {
		t.Fatalf("expected IPv6 exact match, allowed=%t err=%v", allowed, err)
	}
	allowed, err = IPWhitelistContains("198.51.100.1", "192.0.2.0/24")
	if err != nil || allowed {
		t.Fatalf("expected non-match, allowed=%t err=%v", allowed, err)
	}
}

func TestIPWhitelistRejectsInvalidEntries(t *testing.T) {
	if _, err := NormalizeIPWhitelist("not-an-ip"); err == nil {
		t.Fatal("expected invalid IP whitelist entry to fail")
	}
	if _, err := NormalizeIPWhitelist("*"); err == nil {
		t.Fatal("expected wildcard entry to fail")
	}
}
