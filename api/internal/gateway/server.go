// Package gateway exposes a private mTLS-only gateway to verified local agents.
package gateway

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"strings"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/GameplanePanel/gameplane/api/internal/gatewayprotocol"
	"github.com/GameplanePanel/gameplane/api/internal/kube"
)

// Config restricts a gateway to one cluster and an explicit namespace allowlist.
type Config struct {
	Cluster                        string
	Namespaces                     []string
	PeerURI                        string
	TLS                            TLSFiles
	AgentTLS                       TLSFiles
	MaxRequestDuration             time.Duration
	CaptureEnabled                 bool
	CaptureDefaultRetentionSeconds int64
	CaptureMaxRetentionSeconds     int64
	CaptureDefaultMaxDurationSecs  int64
	CaptureDefaultMaxSizeBytes     int64
}

type httpTransport = http.Transport

func newHTTPTransport(tlsConfig *tls.Config) *httpTransport {
	return &http.Transport{TLSClientConfig: tlsConfig, DisableKeepAlives: true,
		DialContext:         (&net.Dialer{Timeout: 10 * time.Second}).DialContext,
		TLSHandshakeTimeout: 10 * time.Second, ResponseHeaderTimeout: 30 * time.Second}
}

// NewHandler creates a gateway with no browser sessions or administrative routes.
func NewHandler(cfg Config, client *kube.Client) (http.Handler, error) {
	if client == nil || cfg.Cluster == "" || cfg.PeerURI == "" || len(cfg.Namespaces) == 0 {
		return nil, errors.New("gateway cluster, namespaces, peer identity, and Kubernetes client required")
	}
	if cfg.MaxRequestDuration < 0 {
		return nil, errors.New("gateway request duration must not be negative")
	}
	if cfg.CaptureDefaultRetentionSeconds == 0 {
		cfg.CaptureDefaultRetentionSeconds = 86400
	}
	if cfg.CaptureMaxRetentionSeconds == 0 {
		cfg.CaptureMaxRetentionSeconds = 604800
	}
	if cfg.CaptureDefaultMaxDurationSecs == 0 {
		cfg.CaptureDefaultMaxDurationSecs = 300
	}
	if cfg.CaptureDefaultMaxSizeBytes == 0 {
		cfg.CaptureDefaultMaxSizeBytes = 943718400
	}
	if cfg.CaptureDefaultRetentionSeconds < 60 || cfg.CaptureDefaultRetentionSeconds > cfg.CaptureMaxRetentionSeconds || cfg.CaptureMaxRetentionSeconds > 604800 || cfg.CaptureDefaultMaxDurationSecs < 1 || cfg.CaptureDefaultMaxDurationSecs > 3600 || cfg.CaptureDefaultMaxSizeBytes < 1 {
		return nil, errors.New("invalid gateway capture defaults or limits")
	}
	return &handler{cfg: cfg, client: client, transport: agentTransport}, nil
}

type handler struct {
	cfg       Config
	client    *kube.Client
	transport func(TLSFiles) (*http.Transport, error)
}

func (h *handler) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	if req.TLS == nil || checkPeer(*req.TLS, h.cfg.TLS.CA, h.cfg.PeerURI) != nil {
		http.Error(w, "central peer not authorized", http.StatusUnauthorized)
		return
	}
	if req.URL.Path == "/v1/capabilities" && req.Method == http.MethodGet {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"protocol":"v1","targetUID":true,"captureFiles":true,"captureEnabled":%t,"defaultRetentionSeconds":%d,"maxRetentionSeconds":%d,"defaultMaxDurationSeconds":%d,"defaultMaxSizeBytes":%d}`, h.cfg.CaptureEnabled, h.cfg.CaptureDefaultRetentionSeconds, h.cfg.CaptureMaxRetentionSeconds, h.cfg.CaptureDefaultMaxDurationSecs, h.cfg.CaptureDefaultMaxSizeBytes)
		return
	}
	if target, err := gatewayprotocol.ParseCapturePath(req.URL.Path); err == nil {
		h.serveCapture(w, req, target)
		return
	}
	target, agentPath, err := gatewayprotocol.ParsePath(req.URL.Path)
	if err != nil || req.URL.RawPath != "" || target.Cluster != h.cfg.Cluster || !h.namespaceAllowed(target.Namespace) {
		http.NotFound(w, req)
		return
	}
	maxBody, allowed := gatewayprotocol.Allowed(req.Method, agentPath)
	if !allowed {
		http.NotFound(w, req)
		return
	}
	upgrade := req.Header.Get("Upgrade")
	if gatewayprotocol.Streaming(agentPath) != strings.EqualFold(upgrade, "websocket") || (upgrade != "" && !strings.EqualFold(upgrade, "websocket")) {
		http.Error(w, "unsupported upgrade", http.StatusBadRequest)
		return
	}
	if req.ContentLength > maxBody {
		http.Error(w, "request too large", http.StatusRequestEntityTooLarge)
		return
	}
	longRunning := gatewayprotocol.LongRunningOperation(req.Method, agentPath)
	ctx, cancel := h.requestContext(req.Context(), req.TLS.PeerCertificates[0].NotAfter, longRunning)
	defer cancel()
	req = req.WithContext(ctx)
	if err := h.verifyTarget(ctx, target); err != nil {
		slog.Warn("gateway target unavailable", "cluster", target.Cluster, "namespace", target.Namespace, "server", target.Name, "err", err)
		http.NotFound(w, req)
		return
	}
	transport, err := h.transport(h.cfg.AgentTLS)
	if err != nil {
		http.Error(w, "agent credentials unavailable", http.StatusServiceUnavailable)
		return
	}
	defer transport.CloseIdleConnections()
	if longRunning {
		transport.ResponseHeaderTimeout = 0
	}
	req.Body = http.MaxBytesReader(w, req.Body, maxBody)
	host := target.Name + "-agent." + target.Namespace + ".svc.cluster.local:8090"
	proxy := &httputil.ReverseProxy{
		Transport: transport,
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.Out.URL.Scheme = "https"
			pr.Out.URL.Host = host
			pr.Out.URL.Path = "/v1/targets/" + target.UID + agentPath
			pr.Out.URL.RawPath = ""
			pr.Out.Host = host
			clean := make(http.Header)
			for _, key := range []string{"Accept", "Accept-Encoding", "Content-Type", "Content-Encoding", "Content-Disposition", "Range", "If-None-Match", "If-Modified-Since", "Connection", "Upgrade", "Sec-Websocket-Key", "Sec-Websocket-Version", "Sec-Websocket-Protocol"} {
				if values := pr.Out.Header.Values(key); len(values) > 0 {
					clean[key] = values
				}
			}
			pr.Out.Header = clean
		},
		ModifyResponse: func(resp *http.Response) error {
			if (resp.StatusCode >= 300 && resp.StatusCode <= 303) || resp.StatusCode == http.StatusTemporaryRedirect || resp.StatusCode == http.StatusPermanentRedirect {
				return errors.New("agent redirects are not supported")
			}
			for _, key := range []string{"Set-Cookie", "Www-Authenticate", "Proxy-Authenticate"} {
				resp.Header.Del(key)
			}
			return nil
		},
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, _ error) {
			http.Error(w, "agent unavailable; write outcome may be unknown", http.StatusBadGateway)
		},
	}
	proxy.ServeHTTP(w, req)
}

func (h *handler) namespaceAllowed(ns string) bool {
	for _, allowed := range h.cfg.Namespaces {
		if ns == allowed {
			return true
		}
	}
	return false
}

func (h *handler) verifyTarget(ctx context.Context, target gatewayprotocol.Target) error {
	ctx, cancel := context.WithTimeout(ctx, operationTimeout)
	defer cancel()
	server, err := h.client.GetServer(ctx, target.Namespace, target.Name)
	if err != nil {
		return fmt.Errorf("get server: %w", err)
	}
	if string(server.GetUID()) != target.UID || server.GetDeletionTimestamp() != nil {
		return errors.New("server identity changed")
	}
	service, err := h.client.Typed.CoreV1().Services(target.Namespace).Get(ctx, target.Name+"-agent", metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("get agent service: %w", err)
	}
	for _, owner := range service.OwnerReferences {
		if owner.Controller != nil && *owner.Controller && owner.APIVersion == "gameplane.local/v1alpha1" && owner.Kind == "GameServer" && owner.Name == target.Name && string(owner.UID) == target.UID {
			return nil
		}
	}
	return errors.New("agent service is not owned by target")
}
