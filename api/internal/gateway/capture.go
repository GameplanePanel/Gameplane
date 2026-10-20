package gateway

import (
	"context"
	"errors"
	"net/http"
	"net/http/httputil"
	"time"

	"github.com/GameplanePanel/gameplane/api/internal/gatewayprotocol"
	"github.com/GameplanePanel/gameplane/api/internal/kube"
)

func (h *handler) serveCapture(w http.ResponseWriter, req *http.Request, target gatewayprotocol.CaptureTarget) {
	if req.URL.RawPath != "" || req.URL.RawQuery != "" || target.Cluster != h.cfg.Cluster || !h.namespaceAllowed(target.Namespace) || (req.Method != http.MethodGet && req.Method != http.MethodDelete) || req.Header.Get("Upgrade") != "" {
		http.NotFound(w, req)
		return
	}
	if req.ContentLength != 0 {
		http.Error(w, "capture file operation has no request body", http.StatusBadRequest)
		return
	}
	ctx, cancel := h.requestContext(req.Context(), req.TLS.PeerCertificates[0].NotAfter, req.Method == http.MethodGet)
	defer cancel()
	req = req.WithContext(ctx)
	lookupCtx, finishLookup := context.WithTimeout(ctx, operationTimeout)
	defer finishLookup()
	if err := h.verifyTarget(lookupCtx, target.Target); err != nil {
		http.NotFound(w, req)
		return
	}
	nc, err := h.client.GetNetworkCapture(lookupCtx, target.Namespace, target.Capture)
	finishLookup()
	if err != nil || !captureMatches(nc, target) {
		http.NotFound(w, req)
		return
	}
	// Empty phase is Pending before the first status update; absence of files
	// cannot make deletion safe while the operator may still start a writer.
	if nc.Status.Phase != kube.CapturePhaseCompleted && nc.Status.Phase != kube.CapturePhaseFailed && nc.Status.Phase != kube.CapturePhaseExpired {
		http.Error(w, "capture is still running", http.StatusConflict)
		return
	}
	if req.Method == http.MethodGet && (nc.Status.Phase != kube.CapturePhaseCompleted || h.captureExpired(nc)) {
		http.NotFound(w, req)
		return
	}
	transport, err := h.transport(h.cfg.AgentTLS)
	if err != nil {
		http.Error(w, "capture credentials unavailable", http.StatusServiceUnavailable)
		return
	}
	defer transport.CloseIdleConnections()
	host := target.Name + "-agent." + target.Namespace + ".svc.cluster.local:9091"
	proxy := &httputil.ReverseProxy{
		Transport: transport,
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.Out.URL.Scheme, pr.Out.URL.Host, pr.Out.Host = "https", host, host
			pr.Out.URL.Path = "/v1/targets/" + target.UID + "/captures/" + target.Capture + "/uids/" + target.CaptureUID + "/file"
			pr.Out.URL.RawPath, pr.Out.URL.RawQuery = "", ""
			clean := make(http.Header)
			for _, name := range []string{"Range", "If-Range", "If-None-Match", "If-Modified-Since"} {
				if value := pr.Out.Header.Get(name); value != "" {
					clean.Set(name, value)
				}
			}
			pr.Out.Header = clean
		},
		ModifyResponse: func(resp *http.Response) error {
			if resp.StatusCode >= 300 && resp.StatusCode < 400 && resp.StatusCode != http.StatusNotModified {
				return errors.New("capture redirects are not supported")
			}
			clean := make(http.Header)
			for _, name := range []string{"Content-Type", "Content-Disposition", "Content-Length", "Content-Range", "Accept-Ranges", "Last-Modified", "Etag"} {
				if value := resp.Header.Get(name); value != "" {
					clean.Set(name, value)
				}
			}
			resp.Header = clean
			return nil
		},
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, _ error) {
			http.Error(w, "capture sidecar unavailable", http.StatusBadGateway)
		},
	}
	proxy.ServeHTTP(w, req)
}

func captureMatches(nc *kube.NetworkCapture, target gatewayprotocol.CaptureTarget) bool {
	if nc == nil || string(nc.UID) != target.CaptureUID || nc.DeletionTimestamp != nil || nc.Spec.ServerRef.Name != target.Name {
		return false
	}
	for _, owner := range nc.OwnerReferences {
		if owner.APIVersion == "gameplane.local/v1alpha1" && owner.Kind == "GameServer" && owner.Name == target.Name && string(owner.UID) == target.UID {
			return true
		}
	}
	return false
}

func (h *handler) captureExpired(nc *kube.NetworkCapture) bool {
	if nc.Status.CompletionTime == nil {
		return true
	}
	ttl := h.cfg.CaptureDefaultRetentionSeconds
	if ttl <= 0 {
		ttl = 86400
	}
	if nc.Spec.TTLSecondsAfterFinished != nil && *nc.Spec.TTLSecondsAfterFinished > 0 {
		ttl = int64(*nc.Spec.TTLSecondsAfterFinished)
	}
	maxTTL := h.cfg.CaptureMaxRetentionSeconds
	if maxTTL <= 0 || maxTTL > 604800 {
		maxTTL = 604800
	}
	if ttl > maxTTL {
		ttl = maxTTL
	}
	return !time.Now().Before(nc.Status.CompletionTime.Add(time.Duration(ttl) * time.Second))
}
