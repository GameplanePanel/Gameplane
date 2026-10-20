package handlers

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/GameplanePanel/gameplane/api/internal/gatewayprotocol"
	"github.com/GameplanePanel/gameplane/api/internal/httperr"
	"github.com/GameplanePanel/gameplane/api/internal/kube"
	"github.com/GameplanePanel/gameplane/api/internal/ws"
)

type captureGateway interface {
	Capabilities(context.Context, string, string, string) (ws.CaptureGatewayCapabilities, error)
	Download(context.Context, gatewayprotocol.CaptureTarget, http.Header) (*http.Response, error)
	Delete(context.Context, gatewayprotocol.CaptureTarget) (*http.Response, error)
}

func (h *captureHandler) selectedCaptureConfig(req *http.Request, namespace, name string) (CaptureConfig, error) {
	if !isRemoteCluster(req) {
		return h.cfg, nil
	}
	if h.gateway == nil {
		return CaptureConfig{}, errors.New("agent gateway not configured")
	}
	caps, err := h.gateway.Capabilities(req.Context(), strings.TrimSpace(req.URL.Query().Get("cluster")), namespace, name)
	if err != nil {
		return CaptureConfig{}, err
	}
	cfg := CaptureConfig{FeatureEnabled: caps.Protocol == "v1" && caps.TargetUID && caps.CaptureEnabled && caps.CaptureFiles, DefaultRetentionSeconds: caps.DefaultRetentionSeconds, MaxRetentionSeconds: caps.MaxRetentionSeconds, DefaultMaxDurationSecs: int(caps.DefaultMaxDurationSecs), DefaultMaxSizeBytes: caps.DefaultMaxSizeBytes}
	if cfg.FeatureEnabled && (caps.DefaultMaxDurationSecs < 1 || caps.DefaultMaxDurationSecs > 3600 || cfg.DefaultMaxSizeBytes < 1 || cfg.DefaultRetentionSeconds < 60 || cfg.DefaultRetentionSeconds > cfg.MaxRetentionSeconds || cfg.MaxRetentionSeconds > 604800) {
		return CaptureConfig{}, errors.New("gateway capture limits unavailable")
	}
	return cfg, nil
}

func (h *captureHandler) requireCaptureConfig(w http.ResponseWriter, req *http.Request, namespace, name, path string) (CaptureConfig, bool) {
	cfg, err := h.selectedCaptureConfig(req, namespace, name)
	if err != nil {
		if h.auditWriteOrFail(w, req, http.MethodPost, path, name, "gateway_unavailable", http.StatusServiceUnavailable) {
			httperr.WriteCode(w, req, http.StatusServiceUnavailable, errors.New("capture availability unavailable for this cluster"))
		}
		return cfg, false
	}
	if !cfg.FeatureEnabled {
		if h.auditWriteOrFail(w, req, http.MethodPost, path, name, "feature_disabled", http.StatusNotImplemented) {
			httperr.WriteCode(w, req, http.StatusNotImplemented, errors.New("capture feature is disabled on this cluster"))
		}
		return cfg, false
	}
	return cfg, true
}

func (h *captureHandler) requireCaptureEnabled(w http.ResponseWriter, req *http.Request, namespace, name, path string) bool {
	_, ok := h.requireCaptureConfig(w, req, namespace, name, path)
	return ok
}

func captureTarget(cluster string, gs *kube.GameServer, nc *kube.NetworkCapture) (gatewayprotocol.CaptureTarget, error) {
	target := gatewayprotocol.CaptureTarget{Target: gatewayprotocol.Target{Cluster: cluster, Namespace: gs.Namespace, Name: gs.Name, UID: string(gs.UID)}, Capture: nc.Name, CaptureUID: string(nc.UID)}
	if _, err := gatewayprotocol.CapturePath(target); err != nil {
		return target, err
	}
	if gs.DeletionTimestamp != nil || nc.DeletionTimestamp != nil || nc.Spec.ServerRef.Name != gs.Name {
		return target, errCaptureNotFound
	}
	for _, owner := range nc.OwnerReferences {
		if owner.APIVersion == "gameplane.local/v1alpha1" && owner.Kind == "GameServer" && owner.Name == gs.Name && owner.UID == gs.UID {
			return target, nil
		}
	}
	return target, errCaptureNotFound
}

func (h *captureHandler) remoteCaptureDownload(w http.ResponseWriter, req *http.Request, gs *kube.GameServer, nc *kube.NetworkCapture) int {
	if h.gateway == nil {
		http.Error(w, "agent gateway not configured", http.StatusServiceUnavailable)
		return http.StatusServiceUnavailable
	}
	target, err := captureTarget(strings.TrimSpace(req.URL.Query().Get("cluster")), gs, nc)
	if err != nil {
		http.NotFound(w, req)
		return http.StatusNotFound
	}
	resp, err := h.gateway.Download(req.Context(), target, req.Header)
	if err != nil {
		return writeUpstreamError(w, req, err)
	}
	defer func() { _ = resp.Body.Close() }()
	return streamCaptureResponse(w, req, resp, nc.Name)
}

func (h *captureHandler) remoteCaptureCleanup(req *http.Request, target gatewayprotocol.CaptureTarget) error {
	if h.gateway == nil {
		return errors.New("agent gateway not configured")
	}
	ctx, cancel := context.WithTimeout(req.Context(), 10*time.Second)
	defer cancel()
	resp, err := h.gateway.Delete(ctx, target)
	if err != nil {
		return fmt.Errorf("remote capture cleanup: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusGone {
		return fmt.Errorf("remote capture cleanup returned %d", resp.StatusCode)
	}
	return nil
}

// Only the operator can prove a capture never reached the sidecar. It persists
// the pod UID before starting, so a recorded pod disqualifies this exception
// even when a later stop reports never_started (for example after pod loss).
func captureNeverStarted(nc *kube.NetworkCapture) bool {
	if nc.Status.Phase != kube.CapturePhaseCompleted {
		return false
	}
	if _, recorded := nc.Annotations["gameplane.local/capture-pod-uid"]; recorded {
		return false
	}
	for _, condition := range nc.Status.Conditions {
		if condition.Type == "SidecarStopped" && condition.Status == metav1.ConditionTrue && condition.Reason == "never_started" && condition.ObservedGeneration == nc.Generation {
			return true
		}
	}
	return false
}

// Metadata stays readable during an outage. Explicit CR TTLs remain useful;
// only an authenticated selected-site probe may override them or supply a
// legacy default. Downloads enforce authoritative expiry at the gateway.
func (h *captureHandler) captureRetentionView(req *http.Request, namespace, name string) *captureHandler {
	if !isRemoteCluster(req) {
		return h
	}
	view := *h
	view.cfg.DefaultRetentionSeconds = 0
	view.cfg.MaxRetentionSeconds = 604800
	if h.gateway != nil {
		caps, err := h.gateway.Capabilities(req.Context(), strings.TrimSpace(req.URL.Query().Get("cluster")), namespace, name)
		if err == nil && caps.Protocol == "v1" && caps.TargetUID && caps.DefaultRetentionSeconds >= 60 && caps.MaxRetentionSeconds >= caps.DefaultRetentionSeconds && caps.MaxRetentionSeconds <= 604800 {
			view.cfg.DefaultRetentionSeconds = caps.DefaultRetentionSeconds
			view.cfg.MaxRetentionSeconds = caps.MaxRetentionSeconds
		}
	}
	return &view
}

func deleteCaptureWithUID(ctx context.Context, k *kube.Client, nc *kube.NetworkCapture) error {
	uid := nc.UID
	if err := k.Dynamic.Resource(kube.GVRNetworkCapture).Namespace(nc.Namespace).Delete(ctx, nc.Name, metav1.DeleteOptions{Preconditions: &metav1.Preconditions{UID: &uid}}); err != nil {
		return fmt.Errorf("delete bound capture: %w", err)
	}
	return nil
}

func streamCaptureResponse(w http.ResponseWriter, req *http.Request, resp *http.Response, captureID string) int {
	if resp.StatusCode == http.StatusNotModified {
		copyResponseHeaders(w.Header(), resp.Header)
		w.WriteHeader(resp.StatusCode)
		return resp.StatusCode
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		if resp.StatusCode == http.StatusRequestedRangeNotSatisfiable {
			for _, key := range []string{"Content-Range", "Accept-Ranges"} {
				if value := resp.Header.Get(key); value != "" {
					w.Header().Set(key, value)
				}
			}
		}
		httperr.WriteCode(w, req, resp.StatusCode, fmt.Errorf("capture sidecar returned %s", http.StatusText(resp.StatusCode)))
		return resp.StatusCode
	}
	copyResponseHeaders(w.Header(), resp.Header)
	w.Header().Set("Content-Type", "application/vnd.tcpdump.pcap")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=\"capture-%s.pcapng\"", captureID))
	w.WriteHeader(resp.StatusCode)
	if _, err := io.Copy(w, resp.Body); err != nil {
		slog.Warn("capture stream interrupted", "capture", captureID, "err", err)
		return http.StatusBadGateway
	}
	return resp.StatusCode
}
