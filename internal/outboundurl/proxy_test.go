package outboundurl

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"testing"
	"time"
)

func TestSOCKSProxyDialHonorsContextAndHandshakeTimeout(t *testing.T) {
	previousTimeout := socksProxyDialTimeout
	socksProxyDialTimeout = 80 * time.Millisecond
	t.Cleanup(func() { socksProxyDialTimeout = previousTimeout })

	for _, testCase := range []struct {
		name           string
		contextTimeout time.Duration
		maximumElapsed time.Duration
	}{
		{name: "caller deadline", contextTimeout: 20 * time.Millisecond, maximumElapsed: 70 * time.Millisecond},
		{name: "handshake deadline", maximumElapsed: 150 * time.Millisecond},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			proxyAddress, connectionClosed := startHangingSOCKS5Server(t)
			proxyURL, err := url.Parse("socks5://" + proxyAddress)
			if err != nil {
				t.Fatalf("parse proxy URL: %v", err)
			}
			transport := http.DefaultTransport.(*http.Transport).Clone()
			if err := ConfigureProxyTransport(transport, proxyURL); err != nil {
				t.Fatalf("configure SOCKS transport: %v", err)
			}

			ctx := context.Background()
			var cancel context.CancelFunc
			if testCase.contextTimeout > 0 {
				ctx, cancel = context.WithTimeout(ctx, testCase.contextTimeout)
				defer cancel()
			}
			started := time.Now()
			_, dialErr := transport.DialContext(ctx, "tcp", "example.com:80")
			elapsed := time.Since(started)
			if !errors.Is(dialErr, context.DeadlineExceeded) {
				t.Fatalf("SOCKS dial error = %v, want context deadline exceeded", dialErr)
			}
			if !errors.Is(dialErr, ErrProxyDialTimeout) {
				t.Fatalf("SOCKS dial error = %v, want proxy dial timeout", dialErr)
			}
			if elapsed > testCase.maximumElapsed {
				t.Fatalf("SOCKS dial took %s, want at most %s", elapsed, testCase.maximumElapsed)
			}
			select {
			case readErr := <-connectionClosed:
				if !errors.Is(readErr, io.EOF) {
					t.Fatalf("SOCKS connection after timeout = %v, want EOF", readErr)
				}
			case <-time.After(200 * time.Millisecond):
				t.Fatal("SOCKS handshake connection remained open after timeout")
			}
		})
	}
}

func TestConfigureProxyTransportNormalizesProxySchemeCase(t *testing.T) {
	proxyURL, err := url.Parse("HTTP://proxy.example:8080")
	if err != nil {
		t.Fatalf("parse proxy URL: %v", err)
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	if err := ConfigureProxyTransport(transport, proxyURL); err != nil {
		t.Fatalf("configure HTTP proxy: %v", err)
	}
	requestURL, err := url.Parse("https://example.com")
	if err != nil {
		t.Fatalf("parse request URL: %v", err)
	}
	configured, err := transport.Proxy(&http.Request{URL: requestURL})
	if err != nil {
		t.Fatalf("resolve configured HTTP proxy: %v", err)
	}
	if configured == nil || configured.Scheme != "http" || configured.Host != "proxy.example:8080" {
		t.Fatalf("configured HTTP proxy = %v, want normalized proxy URL", configured)
	}
}

func startHangingSOCKS5Server(t *testing.T) (string, <-chan error) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen for SOCKS proxy: %v", err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	connectionClosed := make(chan error, 1)
	go func() {
		conn, acceptErr := listener.Accept()
		if acceptErr != nil {
			connectionClosed <- acceptErr
			return
		}
		defer conn.Close()
		reader := bufio.NewReader(conn)
		header := make([]byte, 2)
		if _, readErr := io.ReadFull(reader, header); readErr != nil {
			connectionClosed <- readErr
			return
		}
		methods := make([]byte, int(header[1]))
		if _, readErr := io.ReadFull(reader, methods); readErr != nil {
			connectionClosed <- readErr
			return
		}
		_, readErr := reader.ReadByte()
		connectionClosed <- readErr
	}()
	return listener.Addr().String(), connectionClosed
}
