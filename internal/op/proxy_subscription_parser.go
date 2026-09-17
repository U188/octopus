package op

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"

	"github.com/U188/octopus/internal/model"
	"go.yaml.in/yaml/v3"
)

type parsedProxySubscriptionNode struct {
	URL          string
	NodeKey      string
	Name         string
	Protocol     string
	RuntimeType  model.ProxyNodeRuntimeType
	ConfigJSON   string
	DisplayValue string
}

type clashSubscription struct {
	Proxies []map[string]any `yaml:"proxies"`
	Legacy  []map[string]any `yaml:"Proxy"`
}

var encryptedProxyProtocols = map[string]bool{
	"vmess": true, "vless": true, "trojan": true, "shadowsocks": true,
	"hysteria2": true, "tuic": true, "anytls": true,
}

func parseProxySubscriptionNodes(content string) ([]parsedProxySubscriptionNode, error) {
	content = strings.TrimSpace(content)
	if content == "" {
		return nil, fmt.Errorf("subscription contains no valid proxy nodes")
	}
	if extracted, ok, err := extractProxyListJSON(content); err != nil {
		return nil, err
	} else if ok {
		content = extracted
	}

	if nodes := parseClashSubscription(content); len(nodes) > 0 {
		return validateParsedProxyNodes(nodes)
	}
	if decoded, ok := decodeSubscriptionBase64(content); ok {
		if nodes := parseClashSubscription(decoded); len(nodes) > 0 {
			return validateParsedProxyNodes(nodes)
		}
		content = decoded
	}

	nodes := make([]parsedProxySubscriptionNode, 0)
	for _, rawLine := range strings.Split(content, "\n") {
		line := strings.TrimSpace(rawLine)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}
		if node, ok := parseEncryptedProxyLink(line); ok {
			nodes = append(nodes, node)
			continue
		}
		if comment := strings.IndexAny(line, " \t#;"); comment >= 0 {
			line = strings.TrimSpace(line[:comment])
		}
		normalized, err := model.NormalizeProxyURL(line)
		if err != nil {
			continue
		}
		parsed, _ := url.Parse(normalized)
		protocol := strings.ToLower(parsed.Scheme)
		if protocol == "socks" {
			protocol = "socks5"
		}
		nodes = append(nodes, parsedProxySubscriptionNode{
			URL: normalized, NodeKey: hashProxyNode(normalized), Name: parsed.Host,
			Protocol: protocol, RuntimeType: model.ProxyNodeRuntimeDirect, DisplayValue: redactProxyNodeURL(normalized),
		})
	}
	return validateParsedProxyNodes(nodes)
}

func validateParsedProxyNodes(nodes []parsedProxySubscriptionNode) ([]parsedProxySubscriptionNode, error) {
	nodes = dedupeParsedProxyNodes(nodes)
	if len(nodes) == 0 {
		return nil, fmt.Errorf("subscription contains no valid proxy nodes")
	}
	if len(nodes) > proxySubscriptionMaxNodes {
		return nil, fmt.Errorf("subscription exceeds %d proxy nodes", proxySubscriptionMaxNodes)
	}
	return nodes, nil
}

func extractProxyListJSON(content string) (string, bool, error) {
	if !strings.HasPrefix(content, "{") && !strings.HasPrefix(content, "[") {
		return content, false, nil
	}
	var payload struct {
		Data struct {
			All struct {
				Dedup struct {
					Format2 string `json:"format2"`
					Nodes   []struct {
						Format2 string `json:"format2"`
					} `json:"nodes"`
				} `json:"dedup"`
			} `json:"all"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(content), &payload); err != nil {
		return "", true, fmt.Errorf("parse subscription JSON: %w", err)
	}
	value := strings.TrimSpace(payload.Data.All.Dedup.Format2)
	if value == "" {
		lines := make([]string, 0, len(payload.Data.All.Dedup.Nodes))
		for _, node := range payload.Data.All.Dedup.Nodes {
			if strings.TrimSpace(node.Format2) != "" {
				lines = append(lines, node.Format2)
			}
		}
		value = strings.Join(lines, "\n")
	}
	if value == "" {
		return "", true, fmt.Errorf("subscription JSON contains no supported proxy nodes")
	}
	return value, true, nil
}

func decodeSubscriptionBase64(content string) (string, bool) {
	compact := strings.Join(strings.Fields(content), "")
	if len(compact) < 8 {
		return "", false
	}
	for _, encoding := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
		decoded, err := encoding.DecodeString(compact)
		if err == nil && !strings.ContainsRune(string(decoded), '\x00') {
			text := strings.TrimSpace(string(decoded))
			if strings.Contains(text, "://") || strings.Contains(text, "proxies:") || strings.Contains(text, "Proxy:") {
				return text, true
			}
		}
	}
	return "", false
}

func parseClashSubscription(content string) []parsedProxySubscriptionNode {
	var document clashSubscription
	if err := yaml.Unmarshal([]byte(content), &document); err != nil {
		return nil
	}
	entries := append(document.Proxies, document.Legacy...)
	nodes := make([]parsedProxySubscriptionNode, 0, len(entries))
	for _, raw := range entries {
		protocol := strings.ToLower(strings.TrimSpace(valueString(raw["type"])))
		if protocol == "ss" {
			protocol = "shadowsocks"
		}
		server := strings.TrimSpace(valueString(raw["server"]))
		port := valueInt(raw["port"])
		if server == "" || port < 1 || port > 65535 {
			continue
		}
		name := strings.TrimSpace(valueString(raw["name"]))
		if name == "" {
			name = fmt.Sprintf("%s:%d", server, port)
		}
		if protocol == "http" || protocol == "socks5" || protocol == "socks" {
			scheme := protocol
			if scheme == "socks" {
				scheme = "socks5"
			}
			proxyURL := &url.URL{Scheme: scheme, Host: net.JoinHostPort(server, strconv.Itoa(port))}
			username := valueString(raw["username"])
			password := valueString(raw["password"])
			if username != "" {
				proxyURL.User = url.UserPassword(username, password)
			}
			normalized, err := model.NormalizeProxyURL(proxyURL.String())
			if err != nil {
				continue
			}
			nodes = append(nodes, parsedProxySubscriptionNode{URL: normalized, NodeKey: hashProxyNode(normalized), Name: name, Protocol: scheme, RuntimeType: model.ProxyNodeRuntimeDirect, DisplayValue: redactProxyNodeURL(normalized)})
			continue
		}
		if !encryptedProxyProtocols[protocol] {
			continue
		}
		raw["type"] = protocol
		raw["server"] = server
		raw["port"] = port
		raw["name"] = name
		if node, ok := newEncryptedParsedNode(protocol, name, server, port, raw); ok {
			nodes = append(nodes, node)
		}
	}
	return nodes
}

func parseEncryptedProxyLink(value string) (parsedProxySubscriptionNode, bool) {
	lower := strings.ToLower(value)
	switch {
	case strings.HasPrefix(lower, "vmess://"):
		encoded := strings.TrimSpace(value[len("vmess://"):])
		decoded, ok := decodeSubscriptionBase64(encoded)
		if !ok {
			for _, encoding := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
				data, err := encoding.DecodeString(encoded)
				if err == nil {
					decoded, ok = string(data), true
					break
				}
			}
		}
		if !ok {
			return parsedProxySubscriptionNode{}, false
		}
		var raw map[string]any
		if json.Unmarshal([]byte(decoded), &raw) != nil {
			return parsedProxySubscriptionNode{}, false
		}
		server := valueString(raw["add"])
		port := valueInt(raw["port"])
		if server == "" || port < 1 {
			return parsedProxySubscriptionNode{}, false
		}
		mapped := map[string]any{"type": "vmess", "name": valueString(raw["ps"]), "server": server, "port": port, "uuid": valueString(raw["id"]), "alter_id": valueInt(raw["aid"]), "security": valueString(raw["scy"]), "network": valueString(raw["net"]), "tls": valueString(raw["tls"]) == "tls", "server_name": valueString(raw["sni"]), "path": valueString(raw["path"]), "host": valueString(raw["host"])}
		return newEncryptedParsedNode("vmess", valueString(raw["ps"]), server, port, mapped)
	case strings.HasPrefix(lower, "ss://"):
		return parseShadowsocksLink(value)
	}
	parsed, err := url.Parse(value)
	if err != nil {
		return parsedProxySubscriptionNode{}, false
	}
	protocol := strings.ToLower(parsed.Scheme)
	if protocol == "hy2" {
		protocol = "hysteria2"
	}
	if !encryptedProxyProtocols[protocol] {
		return parsedProxySubscriptionNode{}, false
	}
	port, _ := strconv.Atoi(parsed.Port())
	if parsed.Hostname() == "" || port < 1 {
		return parsedProxySubscriptionNode{}, false
	}
	name, _ := url.PathUnescape(strings.TrimPrefix(parsed.Fragment, "#"))
	if parsed.User == nil {
		return parsedProxySubscriptionNode{}, false
	}
	credential := parsed.User.Username()
	password, _ := parsed.User.Password()
	raw := map[string]any{"type": protocol, "name": name, "server": parsed.Hostname(), "port": port}
	switch protocol {
	case "vless":
		raw["uuid"] = credential
	case "trojan", "hysteria2", "anytls":
		raw["password"] = credential
	case "tuic":
		raw["uuid"], raw["password"] = credential, password
	}
	for key, values := range parsed.Query() {
		if len(values) > 0 {
			raw[key] = values[0]
		}
	}
	return newEncryptedParsedNode(protocol, name, parsed.Hostname(), port, raw)
}

func parseShadowsocksLink(value string) (parsedProxySubscriptionNode, bool) {
	body := strings.TrimPrefix(value, "ss://")
	fragment := ""
	if index := strings.Index(body, "#"); index >= 0 {
		fragment, _ = url.PathUnescape(body[index+1:])
		body = body[:index]
	}
	var userInfo, address string
	if index := strings.LastIndex(body, "@"); index >= 0 {
		userInfo, address = body[:index], body[index+1:]
		if decoded, ok := decodeSimpleBase64(userInfo); ok {
			userInfo = decoded
		}
	} else if decoded, ok := decodeSimpleBase64(body); ok {
		if index := strings.LastIndex(decoded, "@"); index >= 0 {
			userInfo, address = decoded[:index], decoded[index+1:]
		}
	}
	parsedAddress, err := url.Parse("ss://" + address)
	if err != nil || parsedAddress.Hostname() == "" {
		return parsedProxySubscriptionNode{}, false
	}
	port, _ := strconv.Atoi(parsedAddress.Port())
	parts := strings.SplitN(userInfo, ":", 2)
	if len(parts) != 2 || port < 1 {
		return parsedProxySubscriptionNode{}, false
	}
	raw := map[string]any{"type": "shadowsocks", "name": fragment, "server": parsedAddress.Hostname(), "port": port, "method": parts[0], "password": parts[1]}
	return newEncryptedParsedNode("shadowsocks", fragment, parsedAddress.Hostname(), port, raw)
}

func newEncryptedParsedNode(protocol, name, server string, port int, raw map[string]any) (parsedProxySubscriptionNode, bool) {
	if strings.TrimSpace(server) == "" || port < 1 || port > 65535 {
		return parsedProxySubscriptionNode{}, false
	}
	if name == "" {
		name = fmt.Sprintf("%s:%d", server, port)
	}
	data, err := json.Marshal(raw)
	if err != nil {
		return parsedProxySubscriptionNode{}, false
	}
	key := hashProxyNode(string(data))
	return parsedProxySubscriptionNode{URL: "singbox://" + key, NodeKey: key, Name: name, Protocol: protocol, RuntimeType: model.ProxyNodeRuntimeSingBox, ConfigJSON: string(data), DisplayValue: protocol + "://" + net.JoinHostPort(server, strconv.Itoa(port))}, true
}

func decodeSimpleBase64(value string) (string, bool) {
	for _, encoding := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
		if data, err := encoding.DecodeString(value); err == nil {
			return string(data), true
		}
	}
	return "", false
}

func dedupeParsedProxyNodes(nodes []parsedProxySubscriptionNode) []parsedProxySubscriptionNode {
	seen := make(map[string]struct{}, len(nodes))
	out := make([]parsedProxySubscriptionNode, 0, len(nodes))
	for _, node := range nodes {
		if node.NodeKey == "" {
			node.NodeKey = hashProxyNode(node.URL)
		}
		if _, ok := seen[node.NodeKey]; ok {
			continue
		}
		seen[node.NodeKey] = struct{}{}
		out = append(out, node)
	}
	return out
}

func hashProxyNode(value string) string {
	digest := sha256.Sum256([]byte(value))
	return hex.EncodeToString(digest[:])
}

func valueString(value any) string {
	switch current := value.(type) {
	case string:
		return current
	case json.Number:
		return current.String()
	case float64:
		return strconv.FormatFloat(current, 'f', -1, 64)
	case int:
		return strconv.Itoa(current)
	default:
		return ""
	}
}

func valueInt(value any) int {
	parsed, _ := strconv.Atoi(valueString(value))
	return parsed
}

func redactProxyNodeURL(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	parsed.User = nil
	return parsed.String()
}
