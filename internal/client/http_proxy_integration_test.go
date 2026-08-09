package client

import (
	"bufio"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"
	"time"
)

func TestHTTPProxyCONNECTToTLSUpstream(t *testing.T) {
	target := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(target.Close)

	connects := make(chan string, 1)
	proxyServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodConnect {
			http.Error(w, "CONNECT required", http.StatusMethodNotAllowed)
			return
		}
		upstream, err := net.Dial("tcp", req.Host)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		defer upstream.Close()
		clientConn, buffered, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Errorf("hijack CONNECT: %v", err)
			return
		}
		defer clientConn.Close()
		if _, err := buffered.WriteString("HTTP/1.1 200 Connection Established\r\n\r\n"); err != nil {
			t.Errorf("write CONNECT response: %v", err)
			return
		}
		if err := buffered.Flush(); err != nil {
			t.Errorf("flush CONNECT response: %v", err)
			return
		}
		connects <- req.Host
		copyDone := make(chan struct{}, 1)
		go func() {
			_, _ = io.Copy(upstream, clientConn)
			copyDone <- struct{}{}
		}()
		_, _ = io.Copy(clientConn, upstream)
		<-copyDone
	}))
	t.Cleanup(proxyServer.Close)

	proxyClient, err := GetHTTPClientCustomProxy(proxyServer.URL)
	if err != nil {
		t.Fatalf("create HTTP proxy client: %v", err)
	}
	transport, ok := proxyClient.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("HTTP proxy transport type = %T", proxyClient.Transport)
	}
	transport.TLSClientConfig = target.Client().Transport.(*http.Transport).TLSClientConfig.Clone()
	t.Cleanup(transport.CloseIdleConnections)

	req, err := http.NewRequest(http.MethodGet, target.URL, nil)
	if err != nil {
		t.Fatalf("create target request: %v", err)
	}
	req.Close = true
	resp, err := proxyClient.Do(req)
	if err != nil {
		t.Fatalf("request through HTTP CONNECT proxy: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("target status = %d, want %d", resp.StatusCode, http.StatusNoContent)
	}

	targetURL, _ := url.Parse(target.URL)
	select {
	case address := <-connects:
		if address != targetURL.Host {
			t.Fatalf("CONNECT target = %q, want %q", address, targetURL.Host)
		}
	case <-time.After(time.Second):
		t.Fatal("HTTP proxy did not receive CONNECT")
	}
}

func TestProxyPoolTraceRecordsConnectedProxyIP(t *testing.T) {
	target := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(target.Close)

	proxyServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodConnect {
			http.Error(w, "CONNECT required", http.StatusMethodNotAllowed)
			return
		}
		upstream, err := net.Dial("tcp", req.Host)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		defer upstream.Close()
		clientConn, buffered, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Errorf("hijack CONNECT: %v", err)
			return
		}
		defer clientConn.Close()
		if _, err := buffered.WriteString("HTTP/1.1 200 Connection Established\r\n\r\n"); err != nil {
			t.Errorf("write CONNECT response: %v", err)
			return
		}
		if err := buffered.Flush(); err != nil {
			t.Errorf("flush CONNECT response: %v", err)
			return
		}
		copyDone := make(chan struct{}, 1)
		go func() {
			_, _ = io.Copy(upstream, clientConn)
			copyDone <- struct{}{}
		}()
		_, _ = io.Copy(clientConn, upstream)
		<-copyDone
	}))
	t.Cleanup(proxyServer.Close)

	proxyClient, err := GetHTTPClientCustomProxyPool([]string{proxyServer.URL})
	if err != nil {
		t.Fatalf("create proxy pool client: %v", err)
	}
	poolTransport, ok := proxyClient.Transport.(*proxyFailoverTransport)
	if !ok || len(poolTransport.endpoints) != 1 {
		t.Fatalf("proxy pool transport = %T endpoints=%d", proxyClient.Transport, len(poolTransport.endpoints))
	}
	primary, ok := poolTransport.endpoints[0].primary.(*http.Transport)
	if !ok {
		t.Fatalf("primary proxy transport = %T", poolTransport.endpoints[0].primary)
	}
	primary.TLSClientConfig = target.Client().Transport.(*http.Transport).TLSClientConfig.Clone()
	t.Cleanup(proxyClient.CloseIdleConnections)

	trace := NewProxyTrace()
	req, err := http.NewRequestWithContext(WithProxyTrace(context.Background(), trace), http.MethodGet, target.URL, nil)
	if err != nil {
		t.Fatalf("create target request: %v", err)
	}
	req.Close = true
	resp, err := proxyClient.Do(req)
	if err != nil {
		t.Fatalf("request through traced proxy pool: %v", err)
	}
	resp.Body.Close()

	proxyURL, err := url.Parse(proxyServer.URL)
	if err != nil {
		t.Fatalf("parse proxy URL: %v", err)
	}
	wantIP := net.ParseIP(proxyURL.Hostname()).String()
	route := trace.Snapshot()
	if route.ProxyNode != proxyServer.URL || route.ProxyIP != wantIP {
		t.Fatalf("traced proxy route = %#v, want node=%q ip=%q", route, proxyServer.URL, wantIP)
	}
}

func TestSOCKSProxyConnectsAndSupportsAlias(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(target.Close)
	proxyAddress := startSOCKS5Forwarder(t)

	proxyClient, err := GetHTTPClientCustomProxyPool([]string{"socks://" + proxyAddress})
	if err != nil {
		t.Fatalf("create SOCKS proxy client: %v", err)
	}
	trace := NewProxyTrace()
	req, err := http.NewRequestWithContext(WithProxyTrace(context.Background(), trace), http.MethodGet, target.URL, nil)
	if err != nil {
		t.Fatalf("create SOCKS target request: %v", err)
	}
	resp, err := proxyClient.Do(req)
	if err != nil {
		t.Fatalf("request through SOCKS proxy: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("target status = %d, want %d", resp.StatusCode, http.StatusNoContent)
	}
	proxyHost, _, err := net.SplitHostPort(proxyAddress)
	if err != nil {
		t.Fatalf("split SOCKS proxy address: %v", err)
	}
	wantProxyIP := net.ParseIP(proxyHost).String()
	route := trace.Snapshot()
	if route.ProxyNode != "socks://"+proxyAddress || route.ProxyIP != wantProxyIP {
		t.Fatalf("traced SOCKS route = %#v, want node=%q ip=%q", route, "socks://"+proxyAddress, wantProxyIP)
	}
	proxyClient.CloseIdleConnections()
}

func startSOCKS5Forwarder(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen for SOCKS proxy: %v", err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	go func() {
		for {
			conn, acceptErr := listener.Accept()
			if acceptErr != nil {
				return
			}
			go serveSOCKS5Connection(conn)
		}
	}()
	return listener.Addr().String()
}

func serveSOCKS5Connection(conn net.Conn) {
	defer conn.Close()
	reader := bufio.NewReader(conn)
	methods, err := readSOCKS5Greeting(reader)
	if err != nil || !containsByte(methods, 0) {
		return
	}
	if _, err := conn.Write([]byte{5, 0}); err != nil {
		return
	}
	target, err := readSOCKS5Target(reader)
	if err != nil {
		return
	}
	upstream, err := net.Dial("tcp", target)
	if err != nil {
		_, _ = conn.Write([]byte{5, 5, 0, 1, 0, 0, 0, 0, 0, 0})
		return
	}
	defer upstream.Close()
	if _, err := conn.Write([]byte{5, 0, 0, 1, 0, 0, 0, 0, 0, 0}); err != nil {
		return
	}
	copyDone := make(chan struct{}, 1)
	go func() {
		_, _ = io.Copy(upstream, reader)
		copyDone <- struct{}{}
	}()
	_, _ = io.Copy(conn, upstream)
	<-copyDone
}

func readSOCKS5Greeting(reader *bufio.Reader) ([]byte, error) {
	header := make([]byte, 2)
	if _, err := io.ReadFull(reader, header); err != nil {
		return nil, err
	}
	if header[0] != 5 {
		return nil, fmt.Errorf("SOCKS version = %d", header[0])
	}
	methods := make([]byte, int(header[1]))
	_, err := io.ReadFull(reader, methods)
	return methods, err
}

func readSOCKS5Target(reader *bufio.Reader) (string, error) {
	header := make([]byte, 4)
	if _, err := io.ReadFull(reader, header); err != nil {
		return "", err
	}
	if header[0] != 5 || header[1] != 1 {
		return "", fmt.Errorf("unsupported SOCKS request")
	}
	var host string
	switch header[3] {
	case 1:
		address := make([]byte, net.IPv4len)
		if _, err := io.ReadFull(reader, address); err != nil {
			return "", err
		}
		host = net.IP(address).String()
	case 3:
		length, err := reader.ReadByte()
		if err != nil {
			return "", err
		}
		address := make([]byte, int(length))
		if _, err := io.ReadFull(reader, address); err != nil {
			return "", err
		}
		host = string(address)
	case 4:
		address := make([]byte, net.IPv6len)
		if _, err := io.ReadFull(reader, address); err != nil {
			return "", err
		}
		host = net.IP(address).String()
	default:
		return "", fmt.Errorf("unsupported SOCKS address type: %d", header[3])
	}
	portBytes := make([]byte, 2)
	if _, err := io.ReadFull(reader, portBytes); err != nil {
		return "", err
	}
	return net.JoinHostPort(host, strconv.Itoa(int(binary.BigEndian.Uint16(portBytes)))), nil
}

func containsByte(values []byte, want byte) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
