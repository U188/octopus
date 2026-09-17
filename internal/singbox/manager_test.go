package singbox

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestGenerateConfigCreatesIsolatedInboundPerNode(t *testing.T) {
	nodes := []Node{
		{Key: "one", Protocol: "vless", ConfigJSON: `{"server":"one.example","port":443,"uuid":"uuid-one","tls":true}`},
		{Key: "two", Protocol: "shadowsocks", ConfigJSON: `{"server":"two.example","port":8388,"method":"aes-128-gcm","password":"secret"}`},
	}
	config, endpoints, listeners, err := generateConfig(nodes)
	if err != nil {
		t.Fatalf("generate sing-box config: %v", err)
	}
	if len(endpoints) != 2 || len(listeners) != 2 || endpoints["one"] == endpoints["two"] {
		t.Fatalf("nodes did not receive isolated endpoints: endpoints=%v listeners=%v", endpoints, listeners)
	}
	encoded, err := json.Marshal(config)
	if err != nil {
		t.Fatalf("marshal generated config: %v", err)
	}
	var decoded struct {
		Inbounds  []map[string]any `json:"inbounds"`
		Outbounds []map[string]any `json:"outbounds"`
	}
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("decode generated config: %v", err)
	}
	if len(decoded.Inbounds) != 2 || len(decoded.Outbounds) != 3 {
		t.Fatalf("unexpected generated config shape: %+v", decoded)
	}
	if decoded.Outbounds[2]["type"] != "block" || config["route"].(map[string]any)["final"] != "block" {
		t.Fatalf("unmatched routes must not connect directly: %+v", config)
	}
}

func TestBuildOutboundRejectsMissingServer(t *testing.T) {
	if _, err := buildOutbound("vless", map[string]any{"port": 443, "uuid": "uuid"}); err == nil {
		t.Fatal("missing server unexpectedly accepted")
	}
}

func TestBundledBinaryLookupAndOverride(t *testing.T) {
	dir := t.TempDir()
	executable := filepath.Join(dir, "octopus")
	path := filepath.Join(dir, "bin", "sing-box")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("fixture"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, ok := bundledBinary(executable); ok {
		t.Fatal("non-executable bundled binary accepted")
	}
	if err := os.Chmod(path, 0o755); err != nil {
		t.Fatal(err)
	}
	if found, ok := bundledBinary(executable); !ok || found != path {
		t.Fatalf("bundled binary not found: %s, %v", found, ok)
	}
	if found, ok := bundledBinary(filepath.Join(dir, "octopus.exe")); ok || found != filepath.Join(dir, "bin", "sing-box.exe") {
		t.Fatalf("incorrect Windows lookup: %s, %v", found, ok)
	}
	m := &Manager{}
	m.Configure(true, filepath.Join(dir, "missing-binary"), dir)
	if _, err := m.resolveBinary(); err == nil {
		t.Fatal("explicit missing executable silently fell back")
	}
}

func TestConfigureNormalizesRelativeDataDir(t *testing.T) {
	m := &Manager{}
	m.Configure(true, "sing-box", "data/sing-box")
	m.mu.RLock()
	dataDir := m.dataDir
	m.mu.RUnlock()
	if !filepath.IsAbs(dataDir) {
		t.Fatalf("data directory is not absolute: %q", dataDir)
	}
}

func TestGenerateConfigAcceptedBySingBoxBinary(t *testing.T) {
	binary := os.Getenv("SINGBOX_BIN")
	if binary == "" {
		t.Skip("SINGBOX_BIN not set")
	}
	config, _, _, err := generateConfig([]Node{
		{Key: "vmess", Protocol: "vmess", ConfigJSON: `{"server":"one.example","port":443,"uuid":"00000000-0000-4000-8000-000000000001"}`},
		{Key: "vless", Protocol: "vless", ConfigJSON: `{"server":"one.example","port":443,"uuid":"00000000-0000-4000-8000-000000000002"}`},
		{Key: "trojan", Protocol: "trojan", ConfigJSON: `{"server":"one.example","port":443,"password":"secret"}`},
		{Key: "shadowsocks", Protocol: "shadowsocks", ConfigJSON: `{"server":"one.example","port":8388,"method":"aes-128-gcm","password":"secret"}`},
		{Key: "hysteria2", Protocol: "hysteria2", ConfigJSON: `{"server":"one.example","port":443,"password":"secret"}`},
		{Key: "tuic", Protocol: "tuic", ConfigJSON: `{"server":"one.example","port":443,"uuid":"00000000-0000-4000-8000-000000000003","password":"secret"}`},
		{Key: "anytls", Protocol: "anytls", ConfigJSON: `{"server":"one.example","port":443,"password":"secret"}`},
	})
	if err != nil {
		t.Fatalf("generate config: %v", err)
	}
	path := filepath.Join(t.TempDir(), "config.json")
	data, err := json.Marshal(config)
	if err != nil {
		t.Fatalf("marshal config: %v", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if output, err := exec.CommandContext(ctx, binary, "check", "-c", path).CombinedOutput(); err != nil {
		t.Fatalf("sing-box rejected config: %v: %s", err, output)
	}
}

func TestManagerStartsAndStopsSingBoxBinary(t *testing.T) {
	binary := os.Getenv("SINGBOX_BIN")
	if binary == "" {
		t.Skip("SINGBOX_BIN not set")
	}
	binary, err := filepath.Abs(binary)
	if err != nil {
		t.Fatal(err)
	}
	t.Chdir(t.TempDir())
	m := &Manager{}
	m.Configure(true, binary, "data/sing-box")
	defer m.Stop()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	endpoints, err := m.Reload(ctx, []Node{{Key: "fixture", Protocol: "vless", ConfigJSON: `{"server":"one.example","port":443,"uuid":"uuid-one"}`}})
	if err != nil {
		t.Fatalf("start sing-box: %v", err)
	}
	if endpoint, ok := m.Resolve("fixture"); !ok || endpoint == "" || endpoints["fixture"] != endpoint {
		t.Fatalf("missing live endpoint: %+v", endpoints)
	}
	probeEndpoints, cleanup, err := m.Probe(ctx, []Node{{Key: "candidate", Protocol: "vless", ConfigJSON: `{"server":"two.example","port":443,"uuid":"uuid-two"}`}})
	if err != nil {
		t.Fatalf("start isolated probe: %v", err)
	}
	defer cleanup()
	if endpoint, ok := m.Resolve("fixture"); !ok || endpoint != endpoints["fixture"] || probeEndpoints["candidate"] == endpoint {
		t.Fatalf("probe replaced committed endpoint: live=%q probe=%v", endpoint, probeEndpoints)
	}
	if _, ok := m.Resolve("candidate"); ok {
		t.Fatal("uncommitted probe node is routable through live manager")
	}
	if _, _, err := m.Probe(ctx, []Node{{Key: "invalid", Protocol: "vless", ConfigJSON: `{}`}}); err == nil {
		t.Fatal("invalid probe was accepted")
	}
	if endpoint, ok := m.Resolve("fixture"); !ok || endpoint != endpoints["fixture"] {
		t.Fatalf("failed probe replaced committed endpoint: %q", endpoint)
	}
	cleanup()
	if dirs, err := filepath.Glob(filepath.Join("data", "sing-box", "probe-*")); err != nil || len(dirs) != 0 {
		t.Fatalf("probe directories were not removed: %v, %v", dirs, err)
	}
	if err := m.Stop(); err != nil {
		t.Fatalf("stop sing-box: %v", err)
	}
	if _, ok := m.Resolve("fixture"); ok {
		t.Fatal("stopped runtime still exposes endpoint")
	}
}

func TestBuildOutboundMapsWebSocketRealityOptions(t *testing.T) {
	outbound, err := buildOutbound("vless", map[string]any{
		"server": "edge.example", "port": 443, "uuid": "uuid", "security": "reality", "sni": "cdn.example",
		"network": "ws", "ws-opts": map[string]any{"path": "/gateway", "headers": map[string]any{"Host": "host.example"}},
		"reality-opts": map[string]any{"public-key": "public-key", "short-id": "12ab"}, "client-fingerprint": "chrome",
	})
	if err != nil {
		t.Fatalf("build vless Reality outbound: %v", err)
	}
	transport := outbound["transport"].(map[string]any)
	if transport["path"] != "/gateway" || transport["headers"].(map[string]string)["Host"] != "host.example" {
		t.Fatalf("websocket options were not mapped: %+v", transport)
	}
	tls := outbound["tls"].(map[string]any)
	reality := tls["reality"].(map[string]any)
	if reality["public_key"] != "public-key" || reality["short_id"] != "12ab" {
		t.Fatalf("Reality options were not mapped: %+v", tls)
	}
	if tls["utls"].(map[string]any)["fingerprint"] != "chrome" {
		t.Fatalf("uTLS fingerprint was not mapped: %+v", tls)
	}
}
