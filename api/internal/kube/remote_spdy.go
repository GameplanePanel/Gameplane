package kube

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"sync/atomic"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/remotecommand"
	clientspdy "k8s.io/client-go/transport/spdy"
	"k8s.io/streaming/pkg/httpstream"
	httpstreamspdy "k8s.io/streaming/pkg/httpstream/spdy"
)

// NewSPDYExecutor preserves the configured dial guard for pod streams. The
// client-go default SPDY factory ignores rest.Config.Dial, so standalone clients
// need an explicit upgrade transport. Unrestricted combined clients keep their
// existing transport construction.
func (c *Client) NewSPDYExecutor(method string, target *url.URL) (remotecommand.Executor, error) {
	if c.Config.Dial == nil {
		return remotecommand.NewSPDYExecutor(c.Config, method, target)
	}
	tlsConfig, err := rest.TLSConfigFor(c.Config)
	if err != nil {
		return nil, fmt.Errorf("configure guarded stream TLS: %w", err)
	}
	if tlsConfig == nil {
		// REST uses nil for system trust roots, but SPDY's custom-dial path
		// interprets nil as InsecureSkipVerify. Preserve system roots and
		// inferred hostname verification with an explicit verified default.
		tlsConfig = &tls.Config{MinVersion: tls.VersionTLS12}
	}
	// The SPDY implementation unwraps this transport for both TLS and dialing;
	// supply the raw HTTP transport so no wrapper can hide its guarded dialer.
	var connection atomic.Pointer[streamHandshakeConn]
	upgrade, err := httpstreamspdy.NewRoundTripperWithConfig(httpstreamspdy.RoundTripperConfig{
		UpgradeTransport: &http.Transport{
			DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
				conn, err := c.Config.Dial(ctx, network, address)
				if err != nil {
					return nil, err
				}
				bounded := &streamHandshakeConn{Conn: conn, remaining: remoteStreamHandshakeLimit}
				bounded.limited.Store(true)
				// The upstream manual HTTP header reader does not cancel reads
				// itself; close the connection when the operation context ends.
				bounded.stopCancel = context.AfterFunc(ctx, func() { _ = conn.Close() })
				connection.Store(bounded)
				return bounded, nil
			},
			TLSClientConfig: tlsConfig,
			Proxy:           c.Config.Proxy,
		},
		PingPeriod: 5 * time.Second,
	})
	if err != nil {
		return nil, fmt.Errorf("configure guarded stream transport: %w", err)
	}
	guarded := &guardedSPDYTransport{UpgradeRoundTripper: upgrade, connection: &connection}
	wrapped, err := rest.HTTPWrappersForConfig(c.Config, guarded)
	if err != nil {
		return nil, fmt.Errorf("configure guarded stream authentication: %w", err)
	}
	return remotecommand.NewSPDYExecutorRejectRedirects(wrapped, clientspdy.NewUpgraderForStreaming(guarded), method, target)
}

// Include TLS records in the initial budget: the upstream SPDY reader parses
// HTTP headers directly from its connection without a header-size limit. This
// bound is lifted once headers parse, leaving real interactive streams unlimited.
const remoteStreamHandshakeLimit = 1 << 20

type streamHandshakeConn struct {
	net.Conn
	remaining  int64
	limited    atomic.Bool
	stopCancel func() bool
}

func (c *streamHandshakeConn) Read(p []byte) (int, error) {
	if !c.limited.Load() || len(p) == 0 {
		return c.Conn.Read(p)
	}
	if int64(len(p)) > c.remaining+1 {
		p = p[:c.remaining+1]
	}
	n, err := c.Conn.Read(p)
	if int64(n) > c.remaining {
		_ = c.Close()
		return int(c.remaining), errRemoteResponseTooLarge
	}
	c.remaining -= int64(n)
	return n, err
}

func (c *streamHandshakeConn) Close() error {
	if c.stopCancel != nil {
		c.stopCancel()
	}
	return c.Conn.Close()
}

type guardedSPDYTransport struct {
	httpstream.UpgradeRoundTripper
	connection *atomic.Pointer[streamHandshakeConn]
}

func (t *guardedSPDYTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := t.UpgradeRoundTripper.RoundTrip(req)
	if conn := t.connection.Load(); conn != nil {
		if err != nil {
			_ = conn.Close()
		} else {
			conn.limited.Store(false)
		}
	}
	return resp, err
}

func (t *guardedSPDYTransport) NewConnection(resp *http.Response) (httpstream.Connection, error) {
	connection, err := t.UpgradeRoundTripper.NewConnection(resp)
	if err != nil {
		return nil, err
	}
	return &boundedSPDYConnection{Connection: connection}, nil
}

type boundedSPDYConnection struct{ httpstream.Connection }

func (c *boundedSPDYConnection) CreateStream(headers http.Header) (httpstream.Stream, error) {
	stream, err := c.Connection.CreateStream(headers)
	if err != nil {
		return nil, err
	}
	if headers.Get(corev1.StreamType) == corev1.StreamTypeError {
		return &boundedSPDYControlStream{Stream: stream, body: finiteRemoteBody{ReadCloser: stream, remaining: remoteVersionLimit}}, nil
	}
	return stream, nil
}

type boundedSPDYControlStream struct {
	httpstream.Stream
	body finiteRemoteBody
}

func (s *boundedSPDYControlStream) Read(p []byte) (int, error) { return s.body.Read(p) }
