package handlers

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"

	"github.com/GameplanePanel/gameplane/api/internal/auth"
	"github.com/GameplanePanel/gameplane/api/internal/httperr"
	"github.com/GameplanePanel/gameplane/api/internal/kube"
	"github.com/GameplanePanel/gameplane/api/internal/scope"
	"github.com/go-chi/chi/v5"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

func (h *captureHandler) captureNodeEnable(w http.ResponseWriter, r *http.Request) {
	h.captureNodeOptIn(w, r, true)
}
func (h *captureHandler) captureNodeDisable(w http.ResponseWriter, r *http.Request) {
	h.captureNodeOptIn(w, r, false)
}

func (h *captureHandler) captureNodeOptIn(w http.ResponseWriter, req *http.Request, enabled bool) {
	k, ok := resolveCluster(w, req, h.reg)
	if !ok {
		return
	}
	ns, ok := resolveNS(w, req)
	if !ok {
		return
	}
	name := chi.URLParam(req, "name")
	u := auth.UserFromContext(req.Context())
	if u == nil || (u.Role != "admin" && !u.Can("*", true, scope.RequestedCluster(req), "*")) {
		if h.auditWriteOrFail(w, req, req.Method, req.URL.Path, name, "node_diagnostics_admin_required", http.StatusForbidden) {
			httperr.WriteCode(w, req, http.StatusForbidden, errors.New("node diagnostic exception requires administrator privileges"))
		}
		return
	}
	gs, err := k.GetServer(req.Context(), ns, name)
	if err != nil {
		httperr.Write(w, req, err)
		return
	}
	if gs.GetDeletionTimestamp() != nil {
		httperr.WriteCode(w, req, http.StatusConflict, errors.New("server is terminating"))
		return
	}
	reason := "node_diagnostics_opt_out"
	if enabled {
		reason = "node_diagnostics_opt_in_host_privileges"
	}
	// Record intent synchronously before granting host capabilities. A failed
	// audit write prevents the exception; Kubernetes status remains separate.
	if !h.auditWriteOrFail(w, req, req.Method, req.URL.Path, name+":"+string(gs.GetUID()), reason, http.StatusOK) {
		return
	}
	patch, err := json.Marshal(map[string]any{"metadata": map[string]any{"uid": string(gs.GetUID()), "resourceVersion": gs.GetResourceVersion()}, "spec": map[string]any{"capture": map[string]any{"nodeDiagnostic": enabled}}})
	if err != nil {
		httperr.Write(w, req, err)
		return
	}
	_, err = k.Dynamic.Resource(kube.GVRs["servers"]).Namespace(ns).Patch(req.Context(), name, types.MergePatchType, patch, metav1.PatchOptions{})
	if err != nil {
		if h.auditWriteOrFail(w, req, req.Method, req.URL.Path, name, "node_diagnostics_update_failed", http.StatusInternalServerError) {
			httperr.Write(w, req, err)
		}
		return
	}
	if !enabled {
		if err := h.stopActiveCaptures(req.Context(), k, ns, name); err != nil {
			httperr.Write(w, req, err)
			return
		}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]bool{"nodeDiagnostic": enabled})
}

func (h *captureHandler) proxyNodeCaptureDownload(w http.ResponseWriter, req *http.Request, gs *kube.GameServer, nc *kube.NetworkCapture) int {
	bound, err := captureTarget(scope.RequestedCluster(req), gs, nc)
	if err != nil {
		http.NotFound(w, req)
		return http.StatusNotFound
	}
	name, ns, _, err := kube.NodeCaptureService(nc)
	if err != nil {
		httperr.Write(w, req, err)
		return http.StatusInternalServerError
	}
	host, ok := newValidatedHost(name, ns)
	if !ok || h.tlsClient == nil {
		http.Error(w, "capture artifacts unavailable", http.StatusServiceUnavailable)
		return http.StatusServiceUnavailable
	}
	u := url.URL{Scheme: "https", Host: string(host), Path: "/v1/targets/" + bound.UID + "/captures/" + bound.Capture + "/uids/" + bound.CaptureUID + "/file"}
	up, err := http.NewRequestWithContext(req.Context(), http.MethodGet, u.String(), nil)
	if err != nil {
		httperr.Write(w, req, err)
		return http.StatusInternalServerError
	}
	copyProxyHeaders(up.Header, req.Header)
	resp, err := h.tlsClient.Do(up)
	if err != nil {
		return writeUpstreamError(w, req, err)
	}
	defer func() { _ = resp.Body.Close() }()
	return streamCaptureResponse(w, req, resp, nc.Name)
}
