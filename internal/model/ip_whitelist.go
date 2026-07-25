package model

import (
	"fmt"
	"net"
	"strings"
)

const (
	// IPWhitelistMaxEntries prevents a malformed setting from turning every
	// request into an unexpectedly expensive matcher operation.
	IPWhitelistMaxEntries = 256
	IPWhitelistMaxBytes   = 8192
)

// ParseIPWhitelist parses exact IP addresses and CIDR networks. Entries may
// be separated by commas, whitespace, semicolons, or newlines.
func ParseIPWhitelist(value string) ([]*net.IPNet, error) {
	if len(value) > IPWhitelistMaxBytes {
		return nil, fmt.Errorf("ip whitelist is too long (maximum %d bytes)", IPWhitelistMaxBytes)
	}

	parts := strings.FieldsFunc(value, func(r rune) bool {
		switch r {
		case ',', '，', ';', '\n', '\r', '\t', ' ':
			return true
		default:
			return false
		}
	})
	if len(parts) > IPWhitelistMaxEntries {
		return nil, fmt.Errorf("ip whitelist has too many entries (maximum %d)", IPWhitelistMaxEntries)
	}

	networks := make([]*net.IPNet, 0, len(parts))
	seen := make(map[string]struct{}, len(parts))
	for _, raw := range parts {
		entry := strings.TrimSpace(raw)
		if entry == "" {
			continue
		}
		entry = strings.TrimPrefix(entry, "[")
		entry = strings.TrimSuffix(entry, "]")

		var network *net.IPNet
		if strings.Contains(entry, "/") {
			_, parsed, err := net.ParseCIDR(entry)
			if err != nil {
				return nil, fmt.Errorf("invalid IP whitelist entry %q", raw)
			}
			network = parsed
		} else {
			ip := net.ParseIP(entry)
			if ip == nil {
				return nil, fmt.Errorf("invalid IP whitelist entry %q", raw)
			}
			bits := 128
			if ip4 := ip.To4(); ip4 != nil {
				ip = ip4
				bits = 32
			}
			network = &net.IPNet{IP: ip, Mask: net.CIDRMask(bits, bits)}
		}

		canonical := network.String()
		if _, ok := seen[canonical]; ok {
			continue
		}
		seen[canonical] = struct{}{}
		networks = append(networks, network)
	}
	return networks, nil
}

// NormalizeIPWhitelist validates and canonicalizes a whitelist for storage.
func NormalizeIPWhitelist(value string) (string, error) {
	networks, err := ParseIPWhitelist(value)
	if err != nil {
		return "", err
	}
	entries := make([]string, 0, len(networks))
	for _, network := range networks {
		entries = append(entries, network.String())
	}
	return strings.Join(entries, ","), nil
}

// IPWhitelistContains reports whether ip is covered by at least one entry.
func IPWhitelistContains(ip string, whitelist string) (bool, error) {
	parsedIP := net.ParseIP(strings.Trim(strings.TrimSpace(ip), "[]"))
	if parsedIP == nil {
		return false, nil
	}
	networks, err := ParseIPWhitelist(whitelist)
	if err != nil {
		return false, err
	}
	for _, network := range networks {
		if network.Contains(parsedIP) {
			return true, nil
		}
		// Normalize IPv4-mapped addresses so an IPv4 client can match a
		// whitelist entry represented in the other address family.
		if ip4 := parsedIP.To4(); ip4 != nil && network.Contains(ip4) {
			return true, nil
		}
	}
	return false, nil
}
