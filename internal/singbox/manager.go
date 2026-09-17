package singbox

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Node struct {
	Key        string
	Protocol   string
	ConfigJSON string
	Display    string
}

type Status struct {
	Enabled    bool      `json:"enabled"`
	Available  bool      `json:"available"`
	Running    bool      `json:"running"`
	Version    string    `json:"version"`
	BinaryPath string    `json:"binary_path"`
	NodeCount  int       `json:"node_count"`
	StartedAt  time.Time `json:"started_at,omitempty"`
	LastError  string    `json:"last_error"`
}

type managedProcess struct {
	cmd        *exec.Cmd
	done       chan struct{}
	waitErr    error
	configPath string
}

type Manager struct {
	mu         sync.RWMutex
	reloadMu   sync.Mutex
	enabled    bool
	binaryPath string
	dataDir    string
	version    string
	lastError  string
	startedAt  time.Time
	endpoints  map[string]string
	displays   map[string]string
	process    *managedProcess
	generation uint64
	desired    []Node
	restarts   int
}

var Default = &Manager{endpoints: make(map[string]string), displays: make(map[string]string)}

func (m *Manager) Configure(enabled bool, binaryPath, dataDir string) {
	if absolute, err := filepath.Abs(dataDir); err == nil {
		dataDir = absolute
	}
	m.mu.Lock()
	m.enabled = enabled
	m.binaryPath = strings.TrimSpace(binaryPath)
	if m.binaryPath == "" {
		m.binaryPath = "sing-box"
	}
	m.dataDir = dataDir
	m.version = ""
	m.mu.Unlock()
}

// Probe starts an isolated runtime for candidate validation without replacing
// the process serving committed proxy nodes.
func (m *Manager) Probe(ctx context.Context, nodes []Node) (map[string]string, func(), error) {
	if !m.Enabled() || len(nodes) == 0 {
		return map[string]string{}, func() {}, nil
	}
	binary, err := m.resolveBinary()
	if err != nil {
		return nil, func() {}, err
	}
	m.mu.RLock()
	dataDir := m.dataDir
	m.mu.RUnlock()
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return nil, func() {}, err
	}
	probeDir, err := os.MkdirTemp(dataDir, "probe-")
	if err != nil {
		return nil, func() {}, err
	}
	probe := &Manager{}
	probe.Configure(true, binary, probeDir)
	cleanup := func() {
		_ = probe.Stop()
		_ = os.RemoveAll(probeDir)
	}
	endpoints, err := probe.Reload(ctx, nodes)
	if err != nil {
		cleanup()
		return nil, func() {}, err
	}
	return endpoints, cleanup, nil
}

func (m *Manager) Resolve(nodeKey string) (string, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	endpoint, ok := m.endpoints[nodeKey]
	return endpoint, ok
}

func (m *Manager) Enabled() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.enabled
}

func (m *Manager) DisplayForEndpoint(endpoint string) (string, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for key, candidate := range m.endpoints {
		if candidate == endpoint {
			display := m.displays[key]
			return display, display != ""
		}
	}
	return "", false
}

func (m *Manager) Status(ctx context.Context) Status {
	m.mu.RLock()
	status := Status{
		Enabled: m.enabled, Running: m.process != nil, Version: m.version,
		BinaryPath: m.binaryPath, NodeCount: len(m.endpoints), StartedAt: m.startedAt, LastError: m.lastError,
	}
	m.mu.RUnlock()
	if !status.Enabled {
		return status
	}
	resolvedBinary := ""
	if resolved, err := m.resolveBinary(); err == nil {
		status.Available = true
		resolvedBinary = resolved
		status.BinaryPath = resolved
	}
	if status.Version == "" && status.Available {
		probeCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
		defer cancel()
		if output, err := exec.CommandContext(probeCtx, resolvedBinary, "version").Output(); err == nil {
			line := strings.TrimSpace(strings.SplitN(string(output), "\n", 2)[0])
			status.Version = line
			m.mu.Lock()
			m.version = line
			m.mu.Unlock()
		}
	}
	return status
}

func (m *Manager) Reload(ctx context.Context, nodes []Node) (map[string]string, error) {
	return m.reload(ctx, nodes, true)
}

func (m *Manager) reload(ctx context.Context, nodes []Node, resetRestarts bool) (map[string]string, error) {
	m.reloadMu.Lock()
	defer m.reloadMu.Unlock()

	m.mu.RLock()
	enabled := m.enabled
	dataDir := m.dataDir
	m.mu.RUnlock()
	if !enabled {
		_ = m.stopCurrent(3 * time.Second)
		m.mu.Lock()
		m.lastError = ""
		m.mu.Unlock()
		return map[string]string{}, nil
	}
	if len(nodes) == 0 {
		_ = m.stopCurrent(3 * time.Second)
		m.mu.Lock()
		m.endpoints = make(map[string]string)
		m.displays = make(map[string]string)
		m.lastError = ""
		m.mu.Unlock()
		return map[string]string{}, nil
	}
	binary, err := m.resolveBinary()
	if err != nil {
		m.setError(err)
		return nil, err
	}
	config, endpoints, listeners, err := generateConfig(nodes)
	if err != nil {
		m.setError(err)
		return nil, err
	}
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return nil, err
	}
	m.mu.Lock()
	m.generation++
	generation := m.generation
	m.mu.Unlock()
	configPath := filepath.Join(dataDir, fmt.Sprintf("config-%d.json", generation))
	configBytes, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(configPath, configBytes, 0o600); err != nil {
		return nil, err
	}
	checkCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	check := exec.CommandContext(checkCtx, binary, "check", "-c", configPath)
	checkOutput, checkErr := check.CombinedOutput()
	cancel()
	if checkErr != nil {
		_ = os.Remove(configPath)
		err = fmt.Errorf("sing-box config check failed: %s", boundedMessage(checkOutput, checkErr))
		m.setError(err)
		return nil, err
	}

	cmd := exec.Command(binary, "run", "-c", configPath)
	cmd.Dir = dataDir
	cmd.Stdout = nil
	cmd.Stderr = nil
	if err := cmd.Start(); err != nil {
		_ = os.Remove(configPath)
		m.setError(err)
		return nil, fmt.Errorf("start sing-box: %w", err)
	}
	next := &managedProcess{cmd: cmd, done: make(chan struct{}), configPath: configPath}
	go func() {
		next.waitErr = cmd.Wait()
		close(next.done)
	}()
	if err := waitReady(ctx, listeners, next.done, 12*time.Second); err != nil {
		_ = stopProcess(next, 2*time.Second)
		_ = os.Remove(configPath)
		m.setError(err)
		return nil, err
	}

	m.mu.Lock()
	previous := m.process
	m.process = next
	m.endpoints = endpoints
	m.displays = make(map[string]string, len(nodes))
	for _, node := range nodes {
		m.displays[node.Key] = node.Display
	}
	m.desired = append([]Node(nil), nodes...)
	if resetRestarts {
		m.restarts = 0
	}
	m.startedAt = time.Now()
	m.lastError = ""
	m.mu.Unlock()
	if previous != nil {
		_ = stopProcess(previous, 5*time.Second)
	}
	m.removeOldConfigs(configPath)
	go m.watch(next)
	return cloneEndpoints(endpoints), nil
}

func (m *Manager) Stop() error {
	m.reloadMu.Lock()
	defer m.reloadMu.Unlock()
	m.mu.Lock()
	m.generation++
	m.desired = nil
	m.mu.Unlock()
	return m.stopCurrent(5 * time.Second)
}

func (m *Manager) stopCurrent(grace time.Duration) error {
	m.mu.Lock()
	process := m.process
	m.process = nil
	m.endpoints = make(map[string]string)
	m.displays = make(map[string]string)
	m.startedAt = time.Time{}
	m.mu.Unlock()
	if process == nil {
		return nil
	}
	err := stopProcess(process, grace)
	_ = os.Remove(process.configPath)
	return err
}

func (m *Manager) watch(process *managedProcess) {
	<-process.done
	_ = os.Remove(process.configPath)
	m.mu.Lock()
	if m.process != process {
		m.mu.Unlock()
		return
	}
	m.process = nil
	m.endpoints = make(map[string]string)
	m.displays = make(map[string]string)
	if process.waitErr != nil {
		m.lastError = "sing-box exited unexpectedly: " + boundedMessage(nil, process.waitErr)
	} else {
		m.lastError = "sing-box exited unexpectedly"
	}
	m.restarts++
	restarts := m.restarts
	generation := m.generation
	desired := append([]Node(nil), m.desired...)
	enabled := m.enabled
	m.mu.Unlock()
	if !enabled || len(desired) == 0 || restarts > 5 {
		return
	}
	delay := time.Second << min(restarts-1, 5)
	go func() {
		timer := time.NewTimer(delay)
		defer timer.Stop()
		<-timer.C
		m.mu.RLock()
		stale := m.generation != generation || m.process != nil || !m.enabled
		m.mu.RUnlock()
		if stale {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_, _ = m.reload(ctx, desired, false)
	}()
}

func (m *Manager) resolveBinary() (string, error) {
	m.mu.RLock()
	path := m.binaryPath
	dataDir := m.dataDir
	m.mu.RUnlock()
	if path != "sing-box" {
		resolved, err := exec.LookPath(path)
		if err != nil {
			return "", fmt.Errorf("sing-box executable not found: %s", path)
		}
		return filepath.Abs(resolved)
	}
	if executable, err := os.Executable(); err == nil {
		if bundled, ok := bundledBinary(executable); ok {
			return bundled, nil
		}
	}
	if resolved, err := exec.LookPath(path); err == nil {
		return filepath.Abs(resolved)
	}
	fallback := filepath.Join(dataDir, "sing-box")
	if executableFile(fallback) {
		return filepath.Abs(fallback)
	}
	return "", fmt.Errorf("sing-box executable not found: %s", path)
}

func bundledBinary(executable string) (string, bool) {
	name := "sing-box"
	if strings.EqualFold(filepath.Ext(executable), ".exe") {
		name += ".exe"
	}
	path := filepath.Join(filepath.Dir(executable), "bin", name)
	return path, executableFile(path)
}

func executableFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir() && (strings.EqualFold(filepath.Ext(path), ".exe") || info.Mode()&0o111 != 0)
}

func (m *Manager) setError(err error) {
	m.mu.Lock()
	m.lastError = boundedMessage(nil, err)
	m.mu.Unlock()
}

func (m *Manager) removeOldConfigs(current string) {
	entries, _ := filepath.Glob(filepath.Join(filepath.Dir(current), "config-*.json"))
	for _, entry := range entries {
		if entry != current {
			_ = os.Remove(entry)
		}
	}
}

func stopProcess(process *managedProcess, grace time.Duration) error {
	if process == nil || process.cmd == nil || process.cmd.Process == nil {
		return nil
	}
	_ = process.cmd.Process.Signal(os.Interrupt)
	select {
	case <-process.done:
		return nil
	case <-time.After(grace):
		_ = process.cmd.Process.Kill()
		select {
		case <-process.done:
		case <-time.After(time.Second):
		}
		return nil
	}
}

func waitReady(ctx context.Context, listeners []string, exited <-chan struct{}, timeout time.Duration) error {
	deadline := time.NewTimer(timeout)
	ticker := time.NewTicker(100 * time.Millisecond)
	defer deadline.Stop()
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-exited:
			return fmt.Errorf("sing-box exited before ready")
		case <-deadline.C:
			return fmt.Errorf("sing-box did not become ready within %s", timeout)
		case <-ticker.C:
			ready := true
			for _, address := range listeners {
				connection, err := net.DialTimeout("tcp", address, 200*time.Millisecond)
				if err != nil {
					ready = false
					break
				}
				_ = connection.Close()
			}
			if ready {
				return nil
			}
		}
	}
}

func generateConfig(nodes []Node) (map[string]any, map[string]string, []string, error) {
	inbounds := make([]map[string]any, 0, len(nodes))
	outbounds := make([]map[string]any, 0, len(nodes)+1)
	rules := make([]map[string]any, 0, len(nodes))
	endpoints := make(map[string]string, len(nodes))
	listeners := make([]string, 0, len(nodes))
	for index, node := range nodes {
		if strings.TrimSpace(node.Key) == "" {
			return nil, nil, nil, fmt.Errorf("sing-box node key is empty")
		}
		var raw map[string]any
		if err := json.Unmarshal([]byte(node.ConfigJSON), &raw); err != nil {
			return nil, nil, nil, fmt.Errorf("decode sing-box node %s: %w", node.Key, err)
		}
		outbound, err := buildOutbound(node.Protocol, raw)
		if err != nil {
			return nil, nil, nil, err
		}
		port, err := freeLoopbackPort()
		if err != nil {
			return nil, nil, nil, err
		}
		inTag := fmt.Sprintf("in-%d", index)
		outTag := fmt.Sprintf("out-%d", index)
		outbound["tag"] = outTag
		inbounds = append(inbounds, map[string]any{"type": "socks", "tag": inTag, "listen": "127.0.0.1", "listen_port": port})
		outbounds = append(outbounds, outbound)
		rules = append(rules, map[string]any{"inbound": []string{inTag}, "outbound": outTag})
		address := net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
		listeners = append(listeners, address)
		endpoints[node.Key] = "socks5://" + address
	}
	outbounds = append(outbounds, map[string]any{"type": "block", "tag": "block"})
	return map[string]any{
		"log": map[string]any{"level": "warn"}, "inbounds": inbounds, "outbounds": outbounds,
		"route": map[string]any{"rules": rules, "final": "block"},
	}, endpoints, listeners, nil
}

func buildOutbound(protocol string, raw map[string]any) (map[string]any, error) {
	protocol = strings.ToLower(protocol)
	server := strings.TrimSpace(stringValue(raw["server"]))
	serverPort := intValue(raw["port"])
	if server == "" || server == "<nil>" || serverPort < 1 || serverPort > 65535 {
		return nil, fmt.Errorf("invalid %s server address", protocol)
	}
	out := map[string]any{"type": protocol, "server": server, "server_port": serverPort}
	switch protocol {
	case "vmess":
		uuid := strings.TrimSpace(stringValue(raw["uuid"]))
		if uuid == "" {
			return nil, fmt.Errorf("vmess uuid is required")
		}
		out["uuid"], out["security"] = uuid, firstString(raw, "security", "cipher", "auto")
		out["alter_id"] = intValue(raw["alter_id"])
	case "vless":
		uuid := strings.TrimSpace(stringValue(raw["uuid"]))
		if uuid == "" {
			return nil, fmt.Errorf("vless uuid is required")
		}
		out["uuid"] = uuid
		copyString(out, raw, "flow", "flow")
	case "trojan", "hysteria2", "anytls":
		password := firstString(raw, "password", "password", "")
		if password == "" {
			return nil, fmt.Errorf("%s password is required", protocol)
		}
		out["password"] = password
		if protocol == "hysteria2" {
			copyBandwidth(out, raw, "up_mbps", "up", "up-mbps")
			copyBandwidth(out, raw, "down_mbps", "down", "down-mbps")
			if obfsType := strings.TrimSpace(stringValue(raw["obfs"])); obfsType != "" {
				out["obfs"] = map[string]any{"type": obfsType, "password": firstString(raw, "obfs-password", "obfs_password", "")}
			}
		}
	case "shadowsocks":
		method := firstString(raw, "method", "cipher", "")
		password := strings.TrimSpace(stringValue(raw["password"]))
		if method == "" || password == "" {
			return nil, fmt.Errorf("shadowsocks method and password are required")
		}
		out["type"], out["method"], out["password"] = "shadowsocks", method, password
		copyString(out, raw, "plugin", "plugin")
		copyString(out, raw, "plugin_opts", "plugin-opts")
	case "tuic":
		uuid, password := strings.TrimSpace(stringValue(raw["uuid"])), strings.TrimSpace(stringValue(raw["password"]))
		if uuid == "" || password == "" {
			return nil, fmt.Errorf("tuic uuid and password are required")
		}
		out["uuid"], out["password"] = uuid, password
		out["congestion_control"] = firstString(raw, "congestion-controller", "congestion_control", "bbr")
	default:
		return nil, fmt.Errorf("unsupported sing-box protocol: %s", protocol)
	}
	applyTLS(raw, out, protocol == "trojan" || protocol == "hysteria2" || protocol == "tuic" || protocol == "anytls")
	applyTransport(raw, out)
	return out, nil
}

func applyTLS(raw, out map[string]any, force bool) {
	security := strings.ToLower(strings.TrimSpace(stringValue(raw["security"])))
	tlsEnabled := force || boolValue(raw["tls"]) || security == "tls" || security == "reality" || firstString(raw, "server_name", "sni", "") != "" || stringValue(raw["servername"]) != ""
	if !tlsEnabled {
		return
	}
	tls := map[string]any{"enabled": true}
	serverName := firstString(raw, "server_name", "sni", "")
	if serverName == "" {
		serverName = strings.TrimSpace(stringValue(raw["servername"]))
	}
	if serverName != "" {
		tls["server_name"] = serverName
	}
	if truthy(raw["skip-cert-verify"]) || truthy(raw["insecure"]) || truthy(raw["allowInsecure"]) || truthy(raw["allow_insecure"]) {
		tls["insecure"] = true
	}
	if fingerprint := firstNonEmpty(raw, "client-fingerprint", "fingerprint", "fp"); fingerprint != "" {
		tls["utls"] = map[string]any{"enabled": true, "fingerprint": fingerprint}
	}
	if alpn := stringSlice(raw["alpn"]); len(alpn) > 0 {
		tls["alpn"] = alpn
	}
	if security == "reality" || raw["reality-opts"] != nil || raw["reality_opts"] != nil || raw["pbk"] != nil {
		realityOptions := nestedMap(raw, "reality-opts", "reality_opts")
		publicKey := firstNonEmpty(raw, "pbk", "public_key")
		shortID := firstNonEmpty(raw, "sid", "short_id")
		if publicKey == "" {
			publicKey = firstNonEmpty(realityOptions, "public-key", "public_key")
		}
		if shortID == "" {
			shortID = firstNonEmpty(realityOptions, "short-id", "short_id")
		}
		if publicKey != "" {
			reality := map[string]any{"enabled": true, "public_key": publicKey}
			if shortID != "" {
				reality["short_id"] = shortID
			}
			tls["reality"] = reality
		}
	}
	out["tls"] = tls
}

func applyTransport(raw, out map[string]any) {
	network := strings.ToLower(strings.TrimSpace(stringValue(raw["network"])))
	if network == "" {
		network = strings.ToLower(strings.TrimSpace(stringValue(raw["type"])))
	}
	switch network {
	case "ws":
		transport := map[string]any{"type": "ws"}
		wsOptions := nestedMap(raw, "ws-opts", "ws_opts")
		path := firstNonEmpty(raw, "path")
		if path == "" {
			path = firstNonEmpty(wsOptions, "path")
		}
		if path != "" {
			transport["path"] = path
		}
		host := firstNonEmpty(raw, "host")
		if host == "" {
			host = firstNonEmpty(nestedMap(wsOptions, "headers"), "Host", "host")
		}
		if host != "" {
			transport["headers"] = map[string]string{"Host": host}
		}
		out["transport"] = transport
	case "grpc":
		transport := map[string]any{"type": "grpc"}
		serviceName := firstNonEmpty(raw, "service_name", "grpc-service-name", "serviceName")
		if serviceName == "" {
			serviceName = firstNonEmpty(nestedMap(raw, "grpc-opts", "grpc_opts"), "grpc-service-name", "service_name")
		}
		if serviceName != "" {
			transport["service_name"] = serviceName
		}
		out["transport"] = transport
	case "http", "h2":
		transport := map[string]any{"type": "http"}
		if path := firstNonEmpty(raw, "path"); path != "" {
			transport["path"] = path
		}
		if host := firstNonEmpty(raw, "host"); host != "" {
			transport["host"] = []string{host}
		}
		out["transport"] = transport
	case "httpupgrade":
		transport := map[string]any{"type": "httpupgrade"}
		if path := firstNonEmpty(raw, "path"); path != "" {
			transport["path"] = path
		}
		if host := firstNonEmpty(raw, "host"); host != "" {
			transport["host"] = host
		}
		out["transport"] = transport
	}
}

func nestedMap(raw map[string]any, keys ...string) map[string]any {
	for _, key := range keys {
		if value, ok := raw[key].(map[string]any); ok {
			return value
		}
		if value, ok := raw[key].(map[any]any); ok {
			converted := make(map[string]any, len(value))
			for nestedKey, nestedValue := range value {
				converted[fmt.Sprint(nestedKey)] = nestedValue
			}
			return converted
		}
	}
	return nil
}

func firstNonEmpty(raw map[string]any, keys ...string) string {
	for _, key := range keys {
		if value := strings.TrimSpace(stringValue(raw[key])); value != "" {
			return value
		}
	}
	return ""
}

func stringSlice(value any) []string {
	switch current := value.(type) {
	case string:
		parts := strings.FieldsFunc(current, func(r rune) bool { return r == ',' || r == ' ' })
		return parts
	case []any:
		values := make([]string, 0, len(current))
		for _, item := range current {
			if text := strings.TrimSpace(stringValue(item)); text != "" {
				values = append(values, text)
			}
		}
		return values
	case []string:
		return current
	default:
		return nil
	}
}

func copyBandwidth(out, raw map[string]any, destination string, keys ...string) {
	value := firstNonEmpty(raw, keys...)
	if value == "" {
		return
	}
	fields := strings.Fields(value)
	if parsed, err := strconv.Atoi(fields[0]); err == nil && parsed > 0 {
		out[destination] = parsed
	}
}

func freeLoopbackPort() (int, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer listener.Close()
	return listener.Addr().(*net.TCPAddr).Port, nil
}

func cloneEndpoints(source map[string]string) map[string]string {
	result := make(map[string]string, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}

func boundedMessage(output []byte, err error) string {
	message := strings.TrimSpace(string(output))
	if message == "" && err != nil {
		message = err.Error()
	}
	if len(message) > 500 {
		message = message[:500]
	}
	return message
}

func stringValue(value any) string {
	if value == nil {
		return ""
	}
	if text, ok := value.(string); ok {
		return text
	}
	return fmt.Sprint(value)
}

func intValue(value any) int {
	parsed, _ := strconv.Atoi(stringValue(value))
	return parsed
}

func boolValue(value any) bool {
	result, _ := strconv.ParseBool(stringValue(value))
	return result
}

func truthy(value any) bool {
	text := strings.ToLower(strings.TrimSpace(stringValue(value)))
	return text == "1" || text == "true" || text == "yes" || text == "on"
}

func firstString(raw map[string]any, first, second, fallback string) string {
	if value := stringValue(raw[first]); value != "" && value != "<nil>" {
		return value
	}
	if value := stringValue(raw[second]); value != "" && value != "<nil>" {
		return value
	}
	return fallback
}

func copyString(destination, source map[string]any, destinationKey, sourceKey string) {
	if value := stringValue(source[sourceKey]); value != "" && value != "<nil>" {
		destination[destinationKey] = value
	}
}
