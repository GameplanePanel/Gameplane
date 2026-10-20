package handlers

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"

	"github.com/GameplanePanel/gameplane/api/internal/audit"
	"github.com/GameplanePanel/gameplane/api/internal/httperr"
	"github.com/GameplanePanel/gameplane/api/internal/kube"
	"github.com/GameplanePanel/gameplane/api/internal/ws"
)

// CaptureConfig holds cluster-wide capture defaults and limits.
type CaptureConfig struct {
	// FeatureEnabled gates whether the network capture feature is turned
	// on cluster-wide (mirrors the operator's --capture-enabled flag,
	// operator/cmd/main.go). When false, POST .../:capture-enable
	// returns 501 rather than patching spec.capture.enabled — a
	// deliberate operator configuration choice, not a server failure.
	FeatureEnabled bool
	// GatewayNamespace is the central namespace containing enrolled gateway credentials.
	GatewayNamespace string

	DefaultRetentionSeconds int64
	MaxRetentionSeconds     int64
	DefaultMaxDurationSecs  int
	DefaultMaxSizeBytes     int64
}

// MountCapture wires capture endpoints: enable, disable, start, stop,
// list, get, download, and delete.
// cfg contains cluster-wide capture defaults and limits.
// The mTLS cert paths are used to authenticate to the capture sidecar's :9091 endpoint.
func MountCapture(r chi.Router, reg *kube.Registry, auditor *audit.Auditor, cfg CaptureConfig, agentCABundle, agentClientCert, agentClientKey string) {
	var tlsClient *http.Client
	if agentCABundle != "" && agentClientCert != "" && agentClientKey != "" {
		tlsClient = buildAgentHTTPClient(agentCABundle, agentClientCert, agentClientKey)
	}

	h := &captureHandler{
		reg:       reg,
		auditor:   auditor,
		cfg:       cfg,
		tlsClient: tlsClient,
	}
	if cfg.GatewayNamespace != "" {
		h.gateway = ws.NewCaptureGatewayClient(reg, cfg.GatewayNamespace)
	}
	r.Post("/servers/{name}:capture-enable", h.captureEnable)
	r.Post("/servers/{name}:capture-disable", h.captureDisable)
	r.Post("/servers/{name}:capture-start", h.captureStart)
	r.Post("/servers/{name}:capture-stop", h.captureStop)
	r.Get("/servers/{name}:captures", h.captureList)
	r.Get("/servers/{name}:capture", h.captureGet)
	r.Get("/servers/{name}:capture-file", h.captureDownload)
	r.Delete("/servers/{name}:capture", h.captureDelete)
}

type captureHandler struct {
	reg       *kube.Registry
	auditor   *audit.Auditor
	cfg       CaptureConfig
	tlsClient *http.Client
	gateway   captureGateway
}

// errCaptureNotFound is a sentinel distinguishing "no such capture, or it
// belongs to a different server" from a real Kubernetes API failure, so
// callers can tell a 404 apart from a 500 without string-matching errors.
var errCaptureNotFound = errors.New("capture not found")

type captureStartReq struct {
	Filter                string `json:"filter,omitempty"`
	MaxDurationSeconds    int    `json:"maxDurationSeconds"`
	MaxSizeBytes          int64  `json:"maxSizeBytes"`
	TTLSecondsAfterFinish int64  `json:"ttlSecondsAfterFinished,omitempty"`
}

type captureStartResp struct {
	CaptureID             string `json:"captureId"`
	Phase                 string `json:"phase"`
	ServerName            string `json:"serverName"`
	Filter                string `json:"filter"`
	MaxDurationSeconds    int    `json:"maxDurationSeconds"`
	MaxSizeBytes          int64  `json:"maxSizeBytes"`
	TTLSecondsAfterFinish int64  `json:"ttlSecondsAfterFinished"`
	CreatedAt             string `json:"createdAt"`
	StartedAt             string `json:"startedAt"`
	CompletedAt           string `json:"completedAt"`
	BytesWritten          int64  `json:"bytesWritten"`
	PacketsWritten        int64  `json:"packetsWritten"`
}

// captureStart creates a new NetworkCapture CR and returns it.
// POST /servers/{name}:capture-start
func (h *captureHandler) captureStart(w http.ResponseWriter, req *http.Request) {
	k, ok := resolveCluster(w, req, h.reg)
	if !ok {
		return
	}
	name := chi.URLParam(req, "name")
	ns, ok := resolveNS(w, req)
	if !ok {
		return
	}
	auditPath := fmt.Sprintf("/servers/%s:capture-start", name)
	cfg := h.cfg
	if isRemoteCluster(req) {
		cfg, ok = h.requireCaptureConfig(w, req, ns, name, auditPath)
		if !ok {
			return
		}
	}

	var body captureStartReq
	if err := json.NewDecoder(io.LimitReader(req.Body, 1<<16)).Decode(&body); err != nil {
		httperr.WriteCode(w, req, http.StatusBadRequest, errors.New("invalid request body"))
		return
	}

	if isRemoteCluster(req) {
		if body.MaxDurationSeconds == 0 {
			body.MaxDurationSeconds = cfg.DefaultMaxDurationSecs
		}
		if body.MaxSizeBytes == 0 {
			body.MaxSizeBytes = cfg.DefaultMaxSizeBytes
		}
	}
	// Validate duration range (FR-002). This ceiling is fixed by the
	// contract (rest-api.md), not by the cluster's configurable default —
	// the default only informs what a client might pre-fill.
	if body.MaxDurationSeconds < 1 || body.MaxDurationSeconds > 3600 {
		if !h.auditWriteOrFail(w, req, http.MethodPost, auditPath, name, "invalid_duration", http.StatusBadRequest) {
			return
		}
		httperr.WriteCode(w, req, http.StatusBadRequest, errors.New("maxDurationSeconds must be 1..3600"))
		return
	}

	// Validate size range (FR-002), capped by the cluster-configured max.
	if body.MaxSizeBytes < 1 || (cfg.DefaultMaxSizeBytes > 0 && body.MaxSizeBytes > cfg.DefaultMaxSizeBytes) {
		if !h.auditWriteOrFail(w, req, http.MethodPost, auditPath, name, "invalid_size", http.StatusBadRequest) {
			return
		}
		httperr.WriteCode(w, req, http.StatusBadRequest, fmt.Errorf("maxSizeBytes must be 1..%d", cfg.DefaultMaxSizeBytes))
		return
	}

	// Validate the filter expression (FR-003) before creating any CRD.
	if err := validateFilter(body.Filter); err != nil {
		if !h.auditWriteOrFail(w, req, http.MethodPost, auditPath, name, "invalid_filter", http.StatusBadRequest) {
			return
		}
		httperr.WriteCode(w, req, http.StatusBadRequest, err)
		return
	}

	// Check the server exists and has capture enabled.
	gs, err := k.GetGameServer(req.Context(), ns, name)
	if err != nil {
		if apierrors.IsNotFound(err) {
			if !h.auditWriteOrFail(w, req, http.MethodPost, auditPath, name, "server_not_found", http.StatusNotFound) {
				return
			}
			httperr.WriteCode(w, req, http.StatusNotFound, errors.New("not found"))
		} else {
			if !h.auditWriteOrFail(w, req, http.MethodPost, auditPath, name, "error", http.StatusInternalServerError) {
				return
			}
			httperr.Write(w, req, err)
		}
		return
	}

	if !isCaptureEnabled(gs) {
		if !h.auditWriteOrFail(w, req, http.MethodPost, auditPath, name, "capture_not_enabled", http.StatusBadRequest) {
			return
		}
		httperr.WriteCode(w, req, http.StatusBadRequest, errors.New("capture is not enabled on this server"))
		return
	}

	// Check no other capture is already Pending/Running (FR-012).
	active, err := h.hasActiveCapture(req.Context(), k, gs, ns, name)
	if err != nil {
		if !h.auditWriteOrFail(w, req, http.MethodPost, auditPath, name, "error", http.StatusInternalServerError) {
			return
		}
		httperr.Write(w, req, err)
		return
	}
	if active {
		if !h.auditWriteOrFail(w, req, http.MethodPost, auditPath, name, "capture_in_progress", http.StatusConflict) {
			return
		}
		httperr.WriteCode(w, req, http.StatusConflict, errors.New("capture already in progress on this server"))
		return
	}

	// Validate the TTL against the cluster max — never trust the client's
	// requested value directly. When the client omits ttlSecondsAfterFinished,
	// fall back to the GameServer's own RetentionSeconds override
	// (spec.capture.retentionSeconds) if one is set, otherwise the
	// cluster-wide default. Either way the result is still bounded by the
	// cluster maximum below: the per-server field is a per-server *default*,
	// not a per-server cap.
	ttl := body.TTLSecondsAfterFinish
	if ttl <= 0 {
		ttl = cfg.DefaultRetentionSeconds
		if gs.Spec.Capture != nil && gs.Spec.Capture.RetentionSeconds != nil {
			ttl = int64(*gs.Spec.Capture.RetentionSeconds)
		}
	}

	// Validate the lower bound (FR-007 / CRD minimum: 60 seconds).
	// This catches both client-supplied and per-server-override values
	// before they reach the operator, so the error is a 400 with actionable
	// feedback rather than a generic CRD admission failure.
	if ttl < 60 {
		if !h.auditWriteOrFail(w, req, http.MethodPost, auditPath, name, "ttl_too_small", http.StatusBadRequest) {
			return
		}
		httperr.WriteCode(w, req, http.StatusBadRequest,
			fmt.Errorf("ttlSecondsAfterFinished must be 60..%d", cfg.MaxRetentionSeconds))
		return
	}

	if ttl > cfg.MaxRetentionSeconds {
		if !h.auditWriteOrFail(w, req, http.MethodPost, auditPath, name, "ttl_exceeded", http.StatusBadRequest) {
			return
		}
		httperr.WriteCode(w, req, http.StatusBadRequest,
			fmt.Errorf("requested retention %ds exceeds cluster maximum %ds", ttl, cfg.MaxRetentionSeconds))
		return
	}

	// The NetworkCapture CRD stores this as int32 (kubebuilder
	// Maximum=604800 on Spec.TTLSecondsAfterFinished). cfg.MaxRetentionSeconds
	// is an operator-configurable int64 (GAMEPLANE_CAPTURE_MAX_RETENTION) that
	// isn't statically bounded to int32 range, so guard the narrowing
	// conversion below explicitly rather than relying on the comparison above
	// to have done it.
	if ttl < 0 || ttl > math.MaxInt32 {
		if !h.auditWriteOrFail(w, req, http.MethodPost, auditPath, name, "ttl_exceeded", http.StatusBadRequest) {
			return
		}
		httperr.WriteCode(w, req, http.StatusBadRequest,
			fmt.Errorf("ttlSecondsAfterFinished %d is out of range [0, %d]", ttl, int64(math.MaxInt32)))
		return
	}

	captureID := generateCaptureID()
	var filterPtr *string
	if body.Filter != "" {
		filterPtr = &body.Filter
	}
	maxDuration := &metav1.Duration{Duration: time.Duration(body.MaxDurationSeconds) * time.Second}
	maxSize := resource.NewQuantity(body.MaxSizeBytes, resource.BinarySI)
	ttl32 := int32(ttl)

	created, err := k.CreateNetworkCapture(req.Context(), ns, captureID, name, gs.UID, filterPtr, maxDuration, maxSize, &ttl32)
	if err != nil {
		if !h.auditWriteOrFail(w, req, http.MethodPost, auditPath, name, "create_failed", http.StatusInternalServerError) {
			return
		}
		httperr.Write(w, req, err)
		return
	}

	resp := captureStartResp{
		CaptureID:             created.Name,
		Phase:                 string(created.Status.Phase),
		ServerName:            name,
		Filter:                body.Filter,
		MaxDurationSeconds:    body.MaxDurationSeconds,
		MaxSizeBytes:          body.MaxSizeBytes,
		TTLSecondsAfterFinish: ttl,
		CreatedAt:             formatTime(created.CreationTimestamp.Time),
	}

	// Audit the successful creation before writing the response.
	if !h.auditWriteOrFail(w, req, http.MethodPost, auditPath, fmt.Sprintf("%s:%s", name, created.Name), "", http.StatusAccepted) {
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	_ = json.NewEncoder(w).Encode(resp)
}

// captureStatusResp is the canonical status.capture nested shape shared
// by the enable/disable responses (rest-api.md's "Enable Capture on
// GameServer" / "Disable Capture on GameServer" sections) — there is no
// flattened status.captureReady/status.captureMessage. ActiveCapture and
// LastCaptureTime marshal to a JSON null (not an omitted key) when unset,
// so they intentionally carry no `omitempty`.
type captureStatusResp struct {
	Ready           bool    `json:"ready"`
	ActiveCapture   *string `json:"activeCapture"`
	LastCaptureTime *string `json:"lastCaptureTime"`
	SidecarRestarts int32   `json:"sidecarRestarts"`
}

type captureToggleResp struct {
	Name   string `json:"name"`
	Status struct {
		Capture captureStatusResp `json:"capture"`
	} `json:"status"`
}

// captureEnable enables packet capture on a GameServer by patching
// spec.capture.enabled to true. The operator owns everything downstream
// of that field (rule 10 — the API is a UX layer): it is the one that
// injects the capture sidecar into the running pod via the
// pods/ephemeralcontainers subresource. This handler never touches a pod
// directly, and enabling an already-enabled server is a no-op success
// (the contract's precondition list carries no "already enabled" error).
// POST /servers/{name}:capture-enable
func (h *captureHandler) captureEnable(w http.ResponseWriter, req *http.Request) {
	k, ok := resolveCluster(w, req, h.reg)
	if !ok {
		return
	}
	name := chi.URLParam(req, "name")
	ns, ok := resolveNS(w, req)
	if !ok {
		return
	}
	auditPath := fmt.Sprintf("/servers/%s:capture-enable", name)

	// A cluster operator who has not turned the capture feature on
	// cluster-wide is a configuration state, not a server failure: 501,
	// mirroring clusterOps.enabled's identical shape
	// (cluster_actions.go's notEnabled).
	if !h.requireCaptureEnabled(w, req, ns, name, auditPath) {
		return
	}

	u, err := k.GetServer(req.Context(), ns, name)
	if err != nil {
		if apierrors.IsNotFound(err) {
			if !h.auditWriteOrFail(w, req, http.MethodPost, auditPath, name, "server_not_found", http.StatusNotFound) {
				return
			}
			httperr.WriteCode(w, req, http.StatusNotFound, errors.New("not found"))
		} else {
			if !h.auditWriteOrFail(w, req, http.MethodPost, auditPath, name, "error", http.StatusInternalServerError) {
				return
			}
			httperr.Write(w, req, err)
		}
		return
	}

	if u.GetDeletionTimestamp() != nil {
		if !h.auditWriteOrFail(w, req, http.MethodPost, auditPath, name, "terminating", http.StatusConflict) {
			return
		}
		httperr.WriteCode(w, req, http.StatusConflict, errors.New("server is terminating; cannot enable capture"))
		return
	}

	patched, err := h.patchCaptureEnabled(req.Context(), k, ns, name, true)
	if err != nil {
		if !h.auditWriteOrFail(w, req, http.MethodPost, auditPath, name, "patch_failed", http.StatusInternalServerError) {
			return
		}
		httperr.Write(w, req, err)
		return
	}

	if !h.auditWriteOrFail(w, req, http.MethodPost, auditPath, name, "", http.StatusOK) {
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(captureToggleResponse(name, patched))
}

// captureDisable disables packet capture on a GameServer: it patches
// spec.capture.enabled to false (blocking any new capture immediately)
// and stops every capture still Pending/Running for the server. It does
// NOT remove the already-injected ephemeral container — Kubernetes
// provides no API to delete an ephemeral container once injected (see
// rest-api.md's Disable response description, and this feature's
// central asymmetry). The container lingers, idle, in
// pod.status.ephemeralContainerStatuses until the pod is next recreated
// (next scheduling, rollout, or manual delete); the pre-provisioned
// capture emptyDir volume is never removed by disable either.
// POST /servers/{name}:capture-disable
func (h *captureHandler) captureDisable(w http.ResponseWriter, req *http.Request) {
	k, ok := resolveCluster(w, req, h.reg)
	if !ok {
		return
	}
	name := chi.URLParam(req, "name")
	ns, ok := resolveNS(w, req)
	if !ok {
		return
	}
	auditPath := fmt.Sprintf("/servers/%s:capture-disable", name)

	u, err := k.GetServer(req.Context(), ns, name)
	if err != nil {
		if apierrors.IsNotFound(err) {
			if !h.auditWriteOrFail(w, req, http.MethodPost, auditPath, name, "server_not_found", http.StatusNotFound) {
				return
			}
			httperr.WriteCode(w, req, http.StatusNotFound, errors.New("not found"))
		} else {
			if !h.auditWriteOrFail(w, req, http.MethodPost, auditPath, name, "error", http.StatusInternalServerError) {
				return
			}
			httperr.Write(w, req, err)
		}
		return
	}

	if enabled, _, _ := unstructured.NestedBool(u.Object, "spec", "capture", "enabled"); !enabled {
		if !h.auditWriteOrFail(w, req, http.MethodPost, auditPath, name, "already_disabled", http.StatusConflict) {
			return
		}
		httperr.WriteCode(w, req, http.StatusConflict, errors.New("capture is not enabled on this server"))
		return
	}

	// Request a stop of every capture still Pending/Running before
	// clearing the capability, reusing kube.StopNetworkCapture
	// (kube/capture.go). That only sets the stop-requested annotation; the
	// operator stops the sidecar and completes each capture (F-259).
	if err := h.stopActiveCaptures(req.Context(), k, ns, name); err != nil {
		if !h.auditWriteOrFail(w, req, http.MethodPost, auditPath, name, "stop_failed", http.StatusInternalServerError) {
			return
		}
		httperr.Write(w, req, err)
		return
	}

	patched, err := h.patchCaptureEnabled(req.Context(), k, ns, name, false)
	if err != nil {
		if !h.auditWriteOrFail(w, req, http.MethodPost, auditPath, name, "patch_failed", http.StatusInternalServerError) {
			return
		}
		httperr.Write(w, req, err)
		return
	}

	if !h.auditWriteOrFail(w, req, http.MethodPost, auditPath, name, "", http.StatusOK) {
		return
	}

	resp := captureToggleResponse(name, patched)
	// Disabling clears the capability immediately even though the
	// already-injected ephemeral container physically lingers until the
	// pod is recreated (see this handler's doc comment) — force
	// ready/activeCapture here rather than trusting status.capture,
	// which the operator has not necessarily reconciled to reflect that
	// yet (US2 acceptance scenario 4, rest-api.md's amended Disable
	// response description).
	resp.Status.Capture.Ready = false
	resp.Status.Capture.ActiveCapture = nil

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

// stopActiveCaptures requests a stop (kube.StopNetworkCapture's
// stop-requested annotation) of every Pending/Running NetworkCapture for
// the named server; the operator completes them asynchronously. It scans
// live captures rather than trusting status.capture.activeCapture alone,
// for the same eventual-consistency reason hasActiveCapture does below:
// that status field is maintained by the operator's reconciler and can
// lag behind the true CR state.
func (h *captureHandler) stopActiveCaptures(ctx context.Context, k *kube.Client, ns, name string) error {
	captures, err := k.ListNetworkCaptures(ctx, ns, name)
	if err != nil {
		return fmt.Errorf("list network captures for %s: %w", name, err)
	}
	for _, c := range captures {
		if c.Status.Phase != kube.CapturePhasePending && c.Status.Phase != kube.CapturePhaseRunning {
			continue
		}
		if _, err := k.StopNetworkCapture(ctx, ns, c.Name); err != nil {
			return fmt.Errorf("stop network capture %s: %w", c.Name, err)
		}
	}
	return nil
}

// patchCaptureEnabled merge-patches spec.capture.enabled and returns the
// apiserver's resulting object. The operator reconciles everything
// downstream of this field (rule 10) — this never touches a pod or the
// ephemeralcontainers subresource directly. Patching only "spec" (never
// submitting a "status" key) means the returned object's status is left
// untouched by the apiserver, so callers can read status.capture straight
// off the response.
func (h *captureHandler) patchCaptureEnabled(ctx context.Context, k *kube.Client, ns, name string, enabled bool) (*unstructured.Unstructured, error) {
	patch := map[string]any{
		"spec": map[string]any{
			"capture": map[string]any{
				"enabled": enabled,
			},
		},
	}
	patchBytes, err := json.Marshal(patch)
	if err != nil {
		return nil, fmt.Errorf("patch game server %s capture.enabled: marshal patch: %w", name, err)
	}
	u, err := k.Dynamic.Resource(kube.GVRs["servers"]).Namespace(ns).
		Patch(ctx, name, types.MergePatchType, patchBytes, metav1.PatchOptions{})
	if err != nil {
		return nil, fmt.Errorf("patch game server %s capture.enabled: %w", name, err)
	}
	return u, nil
}

// captureToggleResponse builds the enable/disable response body straight
// off the patched GameServer's raw unstructured status.capture (rather
// than through kube.GameServer's projection, which only carries the
// activeCapture subset earlier capture handlers needed) since this
// response's canonical shape needs the full
// ready/activeCapture/lastCaptureTime/sidecarRestarts set.
func captureToggleResponse(name string, u *unstructured.Unstructured) captureToggleResp {
	resp := captureToggleResp{Name: name}
	ready, _, _ := unstructured.NestedBool(u.Object, "status", "capture", "ready")
	resp.Status.Capture.Ready = ready
	if v, found, _ := unstructured.NestedString(u.Object, "status", "capture", "activeCapture"); found && v != "" {
		resp.Status.Capture.ActiveCapture = &v
	}
	if v, found, _ := unstructured.NestedString(u.Object, "status", "capture", "lastCaptureTime"); found && v != "" {
		resp.Status.Capture.LastCaptureTime = &v
	}
	if v, found, _ := unstructured.NestedInt64(u.Object, "status", "capture", "sidecarRestarts"); found {
		// status.capture.sidecarRestarts is int32 on the GameServer CRD type
		// (operator-authored, not client input), but NestedInt64 always widens
		// JSON numbers to int64. Clamp defensively rather than narrowing
		// blindly: this is a response builder with no request/writer to
		// return a 400 through, so an out-of-range value (a future operator
		// bug, an apiserver quirk) saturates instead of silently wrapping
		// into a negative restart count.
		switch {
		case v > math.MaxInt32:
			resp.Status.Capture.SidecarRestarts = math.MaxInt32
		case v < 0:
			resp.Status.Capture.SidecarRestarts = 0
		default:
			resp.Status.Capture.SidecarRestarts = int32(v)
		}
	}
	return resp
}

type captureStopReq struct {
	CaptureID string `json:"captureId"`
}

type captureStopResp struct {
	CaptureID      string `json:"captureId"`
	Phase          string `json:"phase"`
	ServerName     string `json:"serverName"`
	Filter         string `json:"filter"`
	CreatedAt      string `json:"createdAt"`
	StartedAt      string `json:"startedAt"`
	CompletedAt    string `json:"completedAt"`
	StoppingReason string `json:"stoppingReason,omitempty"`
	BytesWritten   int64  `json:"bytesWritten"`
	PacketsWritten int64  `json:"packetsWritten"`
}

// captureStop requests that a running capture be stopped (the operator
// completes it once the sidecar has stopped; see kube.StopNetworkCapture).
// POST /servers/{name}:capture-stop
func (h *captureHandler) captureStop(w http.ResponseWriter, req *http.Request) {
	k, ok := resolveCluster(w, req, h.reg)
	if !ok {
		return
	}
	name := chi.URLParam(req, "name")
	ns, ok := resolveNS(w, req)
	if !ok {
		return
	}
	auditPath := fmt.Sprintf("/servers/%s:capture-stop", name)

	var body captureStopReq
	if err := json.NewDecoder(io.LimitReader(req.Body, 1<<16)).Decode(&body); err != nil {
		httperr.WriteCode(w, req, http.StatusBadRequest, errors.New("invalid request body"))
		return
	}

	if body.CaptureID == "" {
		if !h.auditWriteOrFail(w, req, http.MethodPost, auditPath, name, "missing_id", http.StatusBadRequest) {
			return
		}
		httperr.WriteCode(w, req, http.StatusBadRequest, errors.New("captureId is required"))
		return
	}
	target := fmt.Sprintf("%s:%s", name, body.CaptureID)

	nc, err := h.resolveCapture(req.Context(), k, ns, name, body.CaptureID)
	if err != nil {
		if errors.Is(err, errCaptureNotFound) {
			if !h.auditWriteOrFail(w, req, http.MethodPost, auditPath, target, "not_found", http.StatusNotFound) {
				return
			}
			httperr.WriteCode(w, req, http.StatusNotFound, fmt.Errorf("capture '%s' not found", body.CaptureID))
		} else {
			if !h.auditWriteOrFail(w, req, http.MethodPost, auditPath, target, "error", http.StatusInternalServerError) {
				return
			}
			httperr.Write(w, req, err)
		}
		return
	}

	// A newly created CR has no phase until its first operator status write.
	if nc.Status.Phase != "" && nc.Status.Phase != kube.CapturePhasePending && nc.Status.Phase != kube.CapturePhaseRunning {
		if !h.auditWriteOrFail(w, req, http.MethodPost, auditPath, target, "not_running", http.StatusConflict) {
			return
		}
		httperr.WriteCode(w, req, http.StatusConflict, errors.New("capture is not running"))
		return
	}

	// Requests the stop only (F-259): the operator stops the sidecar and
	// then sets phase=Completed, so the response normally still reports
	// Pending/Running. A repeat stop while the operator has not caught up
	// yet is an idempotent no-op that returns 200 again.
	stopped, err := k.StopNetworkCapture(req.Context(), ns, body.CaptureID)
	if err != nil {
		if !h.auditWriteOrFail(w, req, http.MethodPost, auditPath, target, "stop_failed", http.StatusInternalServerError) {
			return
		}
		httperr.Write(w, req, err)
		return
	}

	resp := captureStopResp{
		CaptureID:      stopped.Name,
		Phase:          string(stopped.Status.Phase),
		ServerName:     name,
		Filter:         filterString(stopped.Spec.Filter),
		CreatedAt:      formatTime(stopped.CreationTimestamp.Time),
		StartedAt:      formatOptionalTime(stopped.Status.StartTime),
		CompletedAt:    formatOptionalTime(stopped.Status.CompletionTime),
		StoppingReason: "user_requested",
		BytesWritten:   quantityValue(stopped.Status.BytesWritten),
		PacketsWritten: stopped.Status.PacketsWritten,
	}

	if !h.auditWriteOrFail(w, req, http.MethodPost, auditPath, target, "", http.StatusOK) {
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

type captureListResp struct {
	Captures []captureItem `json:"captures"`
	Total    int           `json:"total"`
	Limit    int           `json:"limit"`
	Offset   int           `json:"offset"`
}

type captureItem struct {
	CaptureID      string `json:"captureId"`
	Phase          string `json:"phase"`
	ServerName     string `json:"serverName"`
	Filter         string `json:"filter"`
	CreatedAt      string `json:"createdAt"`
	StartedAt      string `json:"startedAt"`
	CompletedAt    string `json:"completedAt"`
	BytesWritten   int64  `json:"bytesWritten"`
	PacketsWritten int64  `json:"packetsWritten"`
	ExpiresAt      string `json:"expiresAt,omitempty"`
}

// captureList lists captures for a server.
// GET /servers/{name}:captures
func (h *captureHandler) captureList(w http.ResponseWriter, req *http.Request) {
	k, ok := resolveCluster(w, req, h.reg)
	if !ok {
		return
	}
	name := chi.URLParam(req, "name")
	ns, ok := resolveNS(w, req)
	if !ok {
		return
	}
	auditPath := fmt.Sprintf("/servers/%s:captures", name)

	if _, err := k.GetGameServer(req.Context(), ns, name); err != nil {
		if apierrors.IsNotFound(err) {
			if !h.auditWriteOrFail(w, req, http.MethodGet, auditPath, name, "server_not_found", http.StatusNotFound) {
				return
			}
			httperr.WriteCode(w, req, http.StatusNotFound, errors.New("not found"))
		} else {
			if !h.auditWriteOrFail(w, req, http.MethodGet, auditPath, name, "error", http.StatusInternalServerError) {
				return
			}
			httperr.Write(w, req, err)
		}
		return
	}

	captures, err := k.ListNetworkCaptures(req.Context(), ns, name)
	if err != nil {
		if !h.auditWriteOrFail(w, req, http.MethodGet, auditPath, name, "list_failed", http.StatusInternalServerError) {
			return
		}
		httperr.Write(w, req, err)
		return
	}

	// Audit the list before writing the response. research.md's Decision 4
	// (T010) rules list-auditing "RECOMMENDED" (audit-events.md §3.1 flags
	// it as a product decision outside that contract's scope, not a hard
	// FR-006 requirement) but implements it here per that recommendation.
	// shouldLog skips GET unconditionally, so this explicit call site is the
	// only way the row gets written at all — see auditWriteOrFail's doc
	// comment and the sibling GET routes in this file for the same pattern.
	if !h.auditWriteOrFail(w, req, http.MethodGet, auditPath, name, "", http.StatusOK) {
		return
	}

	view := h.captureRetentionView(req, ns, name)
	items := make([]captureItem, 0, len(captures))
	for _, c := range captures {
		if view.isExpired(c) {
			continue
		}
		items = append(items, captureItem{
			CaptureID:      c.Name,
			Phase:          string(c.Status.Phase),
			ServerName:     name,
			Filter:         filterString(c.Spec.Filter),
			CreatedAt:      formatTime(c.CreationTimestamp.Time),
			StartedAt:      formatOptionalTime(c.Status.StartTime),
			CompletedAt:    formatOptionalTime(c.Status.CompletionTime),
			BytesWritten:   quantityValue(c.Status.BytesWritten),
			PacketsWritten: c.Status.PacketsWritten,
			ExpiresAt:      view.expiresAt(c),
		})
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(captureListResp{
		Captures: items,
		Total:    len(items),
		Limit:    100,
		Offset:   0,
	})
}

type captureGetResp struct {
	CaptureID          string `json:"captureId"`
	Phase              string `json:"phase"`
	ServerName         string `json:"serverName"`
	Filter             string `json:"filter"`
	MaxDurationSeconds int    `json:"maxDurationSeconds"`
	MaxSizeBytes       int64  `json:"maxSizeBytes"`
	CreatedAt          string `json:"createdAt"`
	StartedAt          string `json:"startedAt"`
	CompletedAt        string `json:"completedAt"`
	BytesWritten       int64  `json:"bytesWritten"`
	PacketsWritten     int64  `json:"packetsWritten"`
	ExpiresAt          string `json:"expiresAt,omitempty"`
}

// captureGet gets a single capture by ID.
//
// Deliberately NOT audited. research.md's Decision 4 (T010) rules get-status
// out of FR-006's mandatory set — unlike list and download, which are
// audited explicitly elsewhere in this file — and this handler was reviewed
// to confirm it does not call the generic middleware's write path either
// (shouldLog excludes GET, so nothing would fire even if it tried). Do not
// add an explicit audit write here without re-litigating that ruling first.
// GET /servers/{name}:capture?id={id}
func (h *captureHandler) captureGet(w http.ResponseWriter, req *http.Request) {
	k, ok := resolveCluster(w, req, h.reg)
	if !ok {
		return
	}
	name := chi.URLParam(req, "name")
	ns, ok := resolveNS(w, req)
	if !ok {
		return
	}
	captureID := req.URL.Query().Get("id")

	if captureID == "" {
		httperr.WriteCode(w, req, http.StatusBadRequest, errors.New("id is required"))
		return
	}

	if _, err := k.GetGameServer(req.Context(), ns, name); err != nil {
		if apierrors.IsNotFound(err) {
			httperr.WriteCode(w, req, http.StatusNotFound, errors.New("not found"))
		} else {
			httperr.Write(w, req, err)
		}
		return
	}

	nc, err := h.resolveCapture(req.Context(), k, ns, name, captureID)
	if err != nil {
		if errors.Is(err, errCaptureNotFound) {
			httperr.WriteCode(w, req, http.StatusNotFound, fmt.Errorf("capture '%s' not found or has expired", captureID))
		} else {
			httperr.Write(w, req, err)
		}
		return
	}

	view := h.captureRetentionView(req, ns, name)
	if view.isExpired(*nc) {
		httperr.WriteCode(w, req, http.StatusNotFound, fmt.Errorf("capture '%s' not found or has expired", captureID))
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(captureGetResp{
		CaptureID:          nc.Name,
		Phase:              string(nc.Status.Phase),
		ServerName:         name,
		Filter:             filterString(nc.Spec.Filter),
		MaxDurationSeconds: maxDurationSecondsOf(nc.Spec.MaxDuration),
		MaxSizeBytes:       quantityValue(nc.Spec.MaxSize),
		CreatedAt:          formatTime(nc.CreationTimestamp.Time),
		StartedAt:          formatOptionalTime(nc.Status.StartTime),
		CompletedAt:        formatOptionalTime(nc.Status.CompletionTime),
		BytesWritten:       quantityValue(nc.Status.BytesWritten),
		PacketsWritten:     nc.Status.PacketsWritten,
		ExpiresAt:          view.expiresAt(*nc),
	})
}

type captureDeleteResp struct {
	Deleted   bool   `json:"deleted"`
	CaptureID string `json:"captureId"`
}

// captureDelete removes a capture CR. Remote files must acknowledge UID-bound
// cleanup first, retaining the CR for retry on an unavailable gateway. Local
// cleanup preserves its existing best-effort behavior.
// DELETE /servers/{name}:capture?id={id}
func (h *captureHandler) captureDelete(w http.ResponseWriter, req *http.Request) {
	k, ok := resolveCluster(w, req, h.reg)
	if !ok {
		return
	}
	name := chi.URLParam(req, "name")
	ns, ok := resolveNS(w, req)
	if !ok {
		return
	}
	captureID := req.URL.Query().Get("id")
	auditPath := fmt.Sprintf("/servers/%s:capture", name)
	target := fmt.Sprintf("%s:%s", name, captureID)

	if captureID == "" {
		if !h.auditWriteOrFail(w, req, http.MethodDelete, auditPath, target, "missing_id", http.StatusBadRequest) {
			return
		}
		httperr.WriteCode(w, req, http.StatusBadRequest, errors.New("id is required"))
		return
	}

	gs, err := k.GetGameServer(req.Context(), ns, name)
	if err != nil {
		if apierrors.IsNotFound(err) {
			if !h.auditWriteOrFail(w, req, http.MethodDelete, auditPath, target, "server_not_found", http.StatusNotFound) {
				return
			}
			httperr.WriteCode(w, req, http.StatusNotFound, errors.New("not found"))
		} else {
			if !h.auditWriteOrFail(w, req, http.MethodDelete, auditPath, target, "error", http.StatusInternalServerError) {
				return
			}
			httperr.Write(w, req, err)
		}
		return
	}

	nc, err := h.resolveCapture(req.Context(), k, ns, name, captureID)
	if err != nil {
		if errors.Is(err, errCaptureNotFound) {
			if !h.auditWriteOrFail(w, req, http.MethodDelete, auditPath, target, "not_found", http.StatusNotFound) {
				return
			}
			httperr.WriteCode(w, req, http.StatusNotFound, fmt.Errorf("capture '%s' not found", captureID))
		} else {
			if !h.auditWriteOrFail(w, req, http.MethodDelete, auditPath, target, "error", http.StatusInternalServerError) {
				return
			}
			httperr.Write(w, req, err)
		}
		return
	}

	// Require a known terminal phase. An empty phase is an unreconciled Pending
	// capture: the operator could already be starting its writer, even before
	// any file exists for a sidecar cleanup request to find.
	if nc.Status.Phase != kube.CapturePhaseCompleted && nc.Status.Phase != kube.CapturePhaseFailed && nc.Status.Phase != kube.CapturePhaseExpired {
		if !h.auditWriteOrFail(w, req, http.MethodDelete, auditPath, target, "capture_running", http.StatusConflict) {
			return
		}
		httperr.WriteCode(w, req, http.StatusConflict, errors.New("cannot delete a running capture; stop it first"))
		return
	}

	// Remote cleanup must succeed before CR deletion; both retain the identities
	// observed above so a same-name replacement is never touched. Local cleanup
	// keeps its existing best-effort behavior.
	var deleteErr error
	if isRemoteCluster(req) {
		bound, err := captureTarget(strings.TrimSpace(req.URL.Query().Get("cluster")), gs, nc)
		if err != nil {
			if h.auditWriteOrFail(w, req, http.MethodDelete, auditPath, target, "not_found", http.StatusNotFound) {
				http.NotFound(w, req)
			}
			return
		}
		if !captureNeverStarted(nc) {
			if err := h.remoteCaptureCleanup(req, bound); err != nil {
				if h.auditWriteOrFail(w, req, http.MethodDelete, auditPath, target, "cleanup_failed", http.StatusServiceUnavailable) {
					httperr.WriteCode(w, req, http.StatusServiceUnavailable, errors.New("capture file cleanup unavailable; capture retained for retry"))
				}
				return
			}
		}
		deleteErr = deleteCaptureWithUID(req.Context(), k, nc)
	} else {
		h.deleteSidecarCaptureFile(req.Context(), gs.Name, gs.Namespace, captureID)
		deleteErr = k.DeleteNetworkCapture(req.Context(), ns, captureID)
	}

	if deleteErr != nil {
		if !h.auditWriteOrFail(w, req, http.MethodDelete, auditPath, target, "delete_failed", http.StatusInternalServerError) {
			return
		}
		httperr.Write(w, req, deleteErr)
		return
	}

	// FR-006 names delete among the mandatory audited operations. DELETE is
	// a mutating method, so the generic Middleware's shouldLog also covers
	// this route — but every capture route in this file audits explicitly
	// via auditWriteOrFail regardless, both for consistency and because
	// only the handler knows the capture-scoped Target ("name:id") the
	// generic middleware cannot construct from the URL alone.
	if !h.auditWriteOrFail(w, req, http.MethodDelete, auditPath, target, "", http.StatusOK) {
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(captureDeleteResp{
		Deleted:   true,
		CaptureID: captureID,
	})
}

// captureDownload downloads the capture file from the sidecar.
// GET /servers/{name}:capture-file?id={id}
//
// Remote files travel through the registered gateway with immutable server
// and capture identities. The local legacy sidecar path remains supported.
func (h *captureHandler) captureDownload(w http.ResponseWriter, req *http.Request) {
	k, ok := resolveCluster(w, req, h.reg)
	if !ok {
		return
	}
	name := chi.URLParam(req, "name")
	ns, ok := resolveNS(w, req)
	if !ok {
		return
	}
	captureID := req.URL.Query().Get("id")
	auditPath := fmt.Sprintf("/servers/%s:capture-file", name)
	target := fmt.Sprintf("%s:%s", name, captureID)

	if captureID == "" {
		if !h.auditWriteOrFail(w, req, http.MethodGet, auditPath, target, "missing_id", http.StatusBadRequest) {
			return
		}
		httperr.WriteCode(w, req, http.StatusBadRequest, errors.New("id is required"))
		return
	}

	gs, err := k.GetGameServer(req.Context(), ns, name)
	if err != nil {
		if apierrors.IsNotFound(err) {
			if !h.auditWriteOrFail(w, req, http.MethodGet, auditPath, target, "server_not_found", http.StatusNotFound) {
				return
			}
			httperr.WriteCode(w, req, http.StatusNotFound, errors.New("not found"))
		} else {
			if !h.auditWriteOrFail(w, req, http.MethodGet, auditPath, target, "error", http.StatusInternalServerError) {
				return
			}
			httperr.Write(w, req, err)
		}
		return
	}

	// The upstream host is built from the resolved GameServer object's own
	// identity (gs.Name/gs.Namespace, populated from the Kubernetes API
	// response), not by re-reading the raw request values a second time —
	// see newValidatedHost below for why this matters for gosec's SSRF taint
	// analysis (G704).
	host, ok := newValidatedHost(gs.Name, gs.Namespace)
	if !ok {
		if !h.auditWriteOrFail(w, req, http.MethodGet, auditPath, target, "invalid_host", http.StatusBadRequest) {
			return
		}
		httperr.WriteCode(w, req, http.StatusBadRequest, errors.New("invalid namespace or pod name"))
		return
	}

	nc, err := h.resolveCapture(req.Context(), k, ns, name, captureID)
	if err != nil {
		if errors.Is(err, errCaptureNotFound) {
			if !h.auditWriteOrFail(w, req, http.MethodGet, auditPath, target, "not_found", http.StatusNotFound) {
				return
			}
			httperr.WriteCode(w, req, http.StatusNotFound, fmt.Errorf("capture '%s' not found or has expired", captureID))
		} else {
			if !h.auditWriteOrFail(w, req, http.MethodGet, auditPath, target, "error", http.StatusInternalServerError) {
				return
			}
			httperr.Write(w, req, err)
		}
		return
	}

	// Checked before the phase branch below: a Completed capture past its own
	// TTL is logically expired even while the operator's GC reconciler (which
	// runs on an interval, not instantaneously) hasn't yet flipped its phase
	// to Expired. Gating on wall-clock time here, not just phase, closes that
	// window — the capture is inaccessible the moment it crosses its
	// retention deadline, not merely after GC catches up.
	// The remote gateway applies its own site's retention defaults and maximum.
	if nc.Status.Phase == kube.CapturePhaseExpired || (!isRemoteCluster(req) && h.isExpired(*nc)) {
		if !h.auditWriteOrFail(w, req, http.MethodGet, auditPath, target, "expired", http.StatusNotFound) {
			return
		}
		httperr.WriteCode(w, req, http.StatusNotFound, fmt.Errorf("capture '%s' not found or has expired", captureID))
		return
	}

	if nc.Status.Phase != kube.CapturePhaseCompleted {
		if !h.auditWriteOrFail(w, req, http.MethodGet, auditPath, target, "not_completed", http.StatusConflict) {
			return
		}
		httperr.WriteCode(w, req, http.StatusConflict, errors.New("capture is still running or has failed"))
		return
	}

	// FR-006: the audit write happens BEFORE any response byte (including
	// WriteHeader) reaches the client — a failed audit write aborts the
	// download outright rather than letting an unaudited file stream
	// through to the caller. This first row necessarily records intent
	// (StatusOK), not the outcome: the sidecar hasn't been contacted yet.
	if !h.auditWriteOrFail(w, req, http.MethodGet, auditPath, target, "", http.StatusOK) {
		return
	}

	// Stream from sidecar over mTLS. If the proxy did not actually succeed,
	// correct the record above with a second row carrying the real status —
	// the response is already committed by then, so this can only append to
	// the trail, not gate it (that gate already happened above, per FR-006).
	var status int
	if isRemoteCluster(req) {
		status = h.remoteCaptureDownload(w, req, gs, nc)
	} else {
		status = h.proxyFileDownload(w, req, host, captureID)
	}
	if status < http.StatusOK || (status >= http.StatusMultipleChoices && status != http.StatusNotModified) {
		// A disconnect must not also erase the corrective audit outcome.
		auditCtx, cancel := context.WithTimeout(context.WithoutCancel(req.Context()), 5*time.Second)
		defer cancel()
		if err := h.auditor.WriteSync(auditCtx, http.MethodGet, auditPath, target, "download_failed", status); err != nil {
			slog.Error("capture download: corrective audit write failed", "capture", target, "status", status, "err", err)
		}
	}
}

// validatedHost is an in-cluster "host:port" authority that can only be
// produced by newValidatedHost. Threading this type through the URL
// construction below — instead of the raw namespace/name strings — makes
// "already validated as a DNS-1123 label" a fact carried by the value's
// type, not just something the reader has to trust happened earlier in the
// function.
type validatedHost string

// newValidatedHost builds the capture sidecar's in-cluster Service DNS name
// (<gs>-agent.<ns>.svc.cluster.local:9091), or reports ok=false if either
// name or ns is not a valid DNS-1123 label. Both must be validated before
// any URL is built from them, to prevent SSRF injection.
func newValidatedHost(name, ns string) (host validatedHost, ok bool) {
	if !isDNS1123Label(name) || !isDNS1123Label(ns) {
		return "", false
	}
	return validatedHost(fmt.Sprintf("%s-agent.%s.svc.cluster.local:9091", name, ns)), true
}

// proxyFileDownload streams the capture file from the sidecar's :9091
// endpoint. host must come from newValidatedHost — the DNS-1123 checks are
// not repeated here, since the type itself is the evidence they already ran.
// It returns the status actually written to the client, so the caller can
// correct the FR-006 audit row (written before the proxy ran, and so
// necessarily optimistic) with what really happened.
func (h *captureHandler) proxyFileDownload(w http.ResponseWriter, req *http.Request, host validatedHost, captureID string) int {
	if h.tlsClient == nil {
		httperr.WriteCode(w, req, http.StatusServiceUnavailable, errors.New("agent mTLS not configured"))
		return http.StatusServiceUnavailable
	}

	// Construct URL using url.URL (never string concatenation) from the
	// pre-validated host.
	u := &url.URL{
		Scheme: "https",
		Host:   string(host),
		Path:   fmt.Sprintf("/captures/%s/file", captureID),
	}

	// Build request to sidecar, propagating the caller's context so a
	// client disconnect cancels the upstream fetch.
	upReq, err := http.NewRequestWithContext(req.Context(), http.MethodGet, u.String(), nil)
	if err != nil {
		httperr.Write(w, req, err)
		return http.StatusInternalServerError
	}

	// Forward only headers the sidecar actually consumes — never the
	// caller's Cookie/Authorization/CSRF material.
	copyProxyHeaders(upReq.Header, req.Header)

	resp, err := h.tlsClient.Do(upReq)
	if err != nil {
		return writeUpstreamError(w, req, err)
	}
	defer func() { _ = resp.Body.Close() }()

	return streamCaptureResponse(w, req, resp, captureID)
}

// deleteSidecarCaptureFile calls the capture sidecar's DELETE /captures/{id}
// route directly, using the same newValidatedHost + h.tlsClient pattern
// proxyFileDownload uses for downloads, rather than routing the cleanup
// through the operator. It is called from captureDelete so that removing a
// NetworkCapture through the API also frees the bytes it occupies against
// the sidecar's volume budget immediately (F-187, F-261), instead of only
// once the pod is eventually recreated.
//
// It never reports failure to the caller: h.tlsClient being unset (agent
// mTLS not configured), an invalid host, an unreachable sidecar, or any
// non-2xx/404 response are all logged and swallowed. The CR is the
// authoritative record of what captures exist; losing this cleanup step
// only reproduces the pre-existing "file lingers until pod restart"
// staleness (still documented as a residual case in
// capture-sidecar/specs.md §8a), not data loss or an inconsistent CR.
// The sidecar's own DELETE handler already treats "file already absent" as
// success (204/404), so this call is safe to retry or to run twice for the
// same id.
func (h *captureHandler) deleteSidecarCaptureFile(ctx context.Context, serverName, namespace, captureID string) {
	if h.tlsClient == nil {
		return
	}
	host, ok := newValidatedHost(serverName, namespace)
	if !ok {
		slog.Warn("capture delete: invalid server/namespace for sidecar cleanup", "server", serverName, "namespace", namespace, "capture", captureID)
		return
	}

	u := &url.URL{
		Scheme: "https",
		Host:   string(host),
		Path:   fmt.Sprintf("/captures/%s", captureID),
	}
	upReq, err := http.NewRequestWithContext(ctx, http.MethodDelete, u.String(), nil)
	if err != nil {
		slog.Error("capture delete: build sidecar cleanup request failed", "capture", captureID, "err", err)
		return
	}

	resp, err := h.tlsClient.Do(upReq)
	if err != nil {
		slog.Warn("capture delete: sidecar unreachable; file will remain until pod restart", "capture", captureID, "err", err)
		return
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusNotFound {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
		slog.Warn("capture delete: sidecar refused file cleanup", "capture", captureID, "status", resp.StatusCode, "body", string(body))
	}
}

// writeUpstreamError maps transport-level failures to gateway statuses and
// returns the status it wrote.
func writeUpstreamError(w http.ResponseWriter, req *http.Request, err error) int {
	status := http.StatusBadGateway
	var nerr net.Error
	if errors.As(err, &nerr) && nerr.Timeout() {
		status = http.StatusGatewayTimeout
	}
	httperr.WriteCode(w, req, status, errors.New("capture sidecar is unreachable"))
	return status
}

// auditWriteOrFail records an audit event and reports whether the caller
// may go on to write its own response. Per FR-006 ("audit events MUST be
// written before the operation response is returned"), a failed audit
// write is not survivable here: this writes a generic 500 itself and
// returns false, and every call site must return immediately without
// writing anything else.
func (h *captureHandler) auditWriteOrFail(w http.ResponseWriter, req *http.Request, method, path, target, reason string, status int) bool {
	if err := h.auditor.WriteSync(req.Context(), method, path, target, reason, status); err != nil {
		httperr.WriteCode(w, req, http.StatusInternalServerError, errors.New("audit write failed"))
		return false
	}
	// This row is the request's audit record; stop audit.Middleware adding a
	// second, less detailed one for the same request.
	audit.MarkRecorded(req.Context())
	return true
}

// buildAgentHTTPClient constructs an HTTP client configured with mTLS for agent communication.
// It follows the same pattern as ws/dialer.go's agentTLSConfig, loading the CA bundle and
// client cert/key from files.
func buildAgentHTTPClient(caBundle, clientCert, clientKey string) *http.Client {
	tlsCfg := &tls.Config{}

	// Load and configure CA bundle for server verification
	if caBundle != "" {
		// caBundle is operator configuration (a CLI flag / mounted secret
		// path from api/cmd/main.go's --agent-ca-bundle), not user input;
		// filepath.Clean here mirrors ws/dialer.go's agentTLSConfig, which
		// loads the identical CA bundle for the same mTLS purpose.
		caCert, err := os.ReadFile(filepath.Clean(caBundle))
		if err != nil {
			slog.Error("failed to read CA bundle", "path", caBundle, "err", err)
			return nil
		}
		caCertPool := x509.NewCertPool()
		if !caCertPool.AppendCertsFromPEM(caCert) {
			slog.Error("failed to parse CA bundle", "path", caBundle)
			return nil
		}
		tlsCfg.RootCAs = caCertPool
	}

	// Load client certificate and key for mTLS
	if clientCert != "" && clientKey != "" {
		cert, err := tls.LoadX509KeyPair(clientCert, clientKey)
		if err != nil {
			slog.Error("failed to load client cert/key", "cert", clientCert, "key", clientKey, "err", err)
			return nil
		}
		tlsCfg.Certificates = []tls.Certificate{cert}
	}

	return &http.Client{
		Timeout: 0,
		Transport: &http.Transport{
			TLSClientConfig: tlsCfg,
		},
	}
}

// ---------- kube-layer wiring ----------

// isCaptureEnabled reports whether spec.capture.enabled is set on the GameServer.
func isCaptureEnabled(gs *kube.GameServer) bool {
	return gs.Spec.Capture != nil && gs.Spec.Capture.Enabled
}

// hasActiveCapture reports whether this GameServer already has a
// Pending/Running capture. It checks the fast path (status.capture.active
// Capture, maintained by the operator) but falls back to scanning the
// server's own NetworkCaptures — the status field is eventually consistent
// (populated by the operator's reconciler), so relying on it alone would
// let a second capture-start race in before that field catches up.
//
// This is a best-effort convenience check only, not the authoritative
// concurrency lock: two concurrent POSTs can both observe hasActiveCapture
// == false and both pass this gate before either has created its
// NetworkCapture CR, since there is no compare-and-swap between this read
// and CreateNetworkCapture below. The real lock — the one that guarantees
// exactly one Running capture per GameServer — lives in
// NetworkCaptureReconciler's Pending→Running transition
// (operator/internal/controller/networkcapture_controller.go), which lists
// every Pending/Running NetworkCapture for the server and keeps only the
// earliest-created one, failing the rest with reason
// "capture_already_in_progress". This check exists purely so the common
// case (no race) gets a fast, CR-free 409 instead of a create-then-fail
// round trip.
func (h *captureHandler) hasActiveCapture(ctx context.Context, k *kube.Client, gs *kube.GameServer, ns, name string) (bool, error) {
	if gs.Status.Capture != nil && gs.Status.Capture.ActiveCapture != nil && *gs.Status.Capture.ActiveCapture != "" {
		return true, nil
	}
	captures, err := k.ListNetworkCaptures(ctx, ns, name)
	if err != nil {
		return false, err
	}
	for _, c := range captures {
		if c.Status.Phase == kube.CapturePhasePending || c.Status.Phase == kube.CapturePhaseRunning {
			return true, nil
		}
	}
	return false, nil
}

// resolveCapture fetches a NetworkCapture by ID and verifies it belongs to
// the named server, returning errCaptureNotFound (wrapped, via errors.Is)
// for either "no such capture" or "belongs to a different server" — the
// contract requires both to 404 identically.
func (h *captureHandler) resolveCapture(ctx context.Context, k *kube.Client, ns, name, captureID string) (*kube.NetworkCapture, error) {
	nc, err := k.GetNetworkCapture(ctx, ns, captureID)
	if err != nil {
		return nil, err
	}
	if nc == nil || nc.Spec.ServerRef.Name != name {
		return nil, errCaptureNotFound
	}
	return nc, nil
}

// effectiveTTL resolves the retention window that applies to nc: its own
// TTLSecondsAfterFinished if set and > 0 (captureStart always sets this —
// see the per-server-override resolution there — but the field is optional
// on the wire type and may be zero/nil for legacy captures), falling back
// to the cluster-wide default otherwise. The result is clamped to the
// cluster-configured maximum (per FR-007), mirroring the operator's
// effectiveRetentionSeconds logic. This ensures the API's expiresAt
// timestamp never diverges from when the operator actually expires the
// capture.
func (h *captureHandler) effectiveTTL(nc kube.NetworkCapture) int64 {
	ttl := h.cfg.DefaultRetentionSeconds
	if nc.Spec.TTLSecondsAfterFinished != nil && *nc.Spec.TTLSecondsAfterFinished > 0 {
		ttl = int64(*nc.Spec.TTLSecondsAfterFinished)
	}
	if h.cfg.MaxRetentionSeconds > 0 && ttl > h.cfg.MaxRetentionSeconds {
		ttl = h.cfg.MaxRetentionSeconds
	}
	return ttl
}

// expiryDeadline returns when nc's retention window closes, or the zero
// Time if the capture has not completed yet (a Pending/Running capture has
// no expiry).
func (h *captureHandler) expiryDeadline(nc kube.NetworkCapture) time.Time {
	if nc.Status.CompletionTime == nil || h.effectiveTTL(nc) <= 0 {
		return time.Time{}
	}
	return nc.Status.CompletionTime.Add(time.Duration(h.effectiveTTL(nc)) * time.Second)
}

// expiresAt computes the display-only expiresAt field: completedAt plus
// the capture's own TTL (falling back to the cluster default retention
// when the capture didn't specify one). Empty until the capture has a
// completion time.
func (h *captureHandler) expiresAt(nc kube.NetworkCapture) string {
	deadline := h.expiryDeadline(nc)
	if deadline.IsZero() {
		return ""
	}
	return deadline.UTC().Format(time.RFC3339)
}

// isExpired reports whether nc is unreachable on expiry grounds — either
// because the operator's retention reconciler has already transitioned it
// to phase Expired, or because it is logically past its own retention
// deadline but that reconciler (which runs on an interval, not
// instantaneously) hasn't caught up yet. Checking wall-clock time directly
// here, rather than trusting phase alone, is what makes an expired capture
// inaccessible immediately instead of only after the next GC pass.
func (h *captureHandler) isExpired(nc kube.NetworkCapture) bool {
	if nc.Status.Phase == kube.CapturePhaseExpired {
		return true
	}
	deadline := h.expiryDeadline(nc)
	return !deadline.IsZero() && time.Now().UTC().After(deadline)
}

// generateCaptureID mints a "cap-<8 hex chars>" identifier, matching the
// shape used throughout the contract's examples. crypto/rand failing means
// the OS CSPRNG is broken, which every other ID generator in this codebase
// (auth/sessions.go, db/shares.go) also treats as unrecoverable.
func generateCaptureID() string {
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		panic("crypto/rand: " + err.Error())
	}
	return "cap-" + hex.EncodeToString(b)
}

// maxFilterLength mirrors NetworkCaptureSpec.Filter's own CRD validation
// (operator/api/v1alpha1/networkcapture_types.go: MaxLength=1024) so an
// over-length filter 400s at the API tier instead of failing CRD admission
// after being echoed back to the client.
const maxFilterLength = 1024

// filterAllowedChars is the character set a pcap-filter(7) expression can
// legitimately use: identifiers, numbers, whitespace, and the small
// punctuation set the grammar's operators are built from.
const filterAllowedChars = " ._-:/()[]<>=!&|"

// validateFilter performs the API-tier syntactic check FR-003 requires
// ("invalid filter expressions MUST be rejected before the capture
// starts"). It intentionally does not attempt a full pcap-filter(7)
// grammar/BPF compile: the only pure-Go compiler for that
// (packetcap/go-pcap/filter) has no tagged release (research.md's "Open
// Risks" #1 — only a pseudo-version resolves from the module proxy) and is
// not part of this module's dependency graph. Pulling in an unverifiable,
// untagged third-party dependency here — without the ability to build and
// confirm the change compiles — would repeat the exact fabricated/unverified
// API risk this rework exists to eliminate. Instead this rejects the
// shapes that can never be a valid pcap-filter expression: empty-after-
// validation is skipped (empty means "no filter", the default applies),
// over-length, and characters pcap-filter syntax never uses.
func validateFilter(filter string) error {
	if filter == "" {
		return nil
	}
	if len(filter) > maxFilterLength {
		return fmt.Errorf("invalid filter: exceeds maximum length of %d characters", maxFilterLength)
	}
	for _, r := range filter {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case strings.ContainsRune(filterAllowedChars, r):
		default:
			return fmt.Errorf("invalid filter: unexpected character %q", r)
		}
	}
	return nil
}

func filterString(f *string) string {
	if f == nil {
		return ""
	}
	return *f
}

func formatTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

func formatOptionalTime(t *metav1.Time) string {
	if t == nil {
		return ""
	}
	return formatTime(t.Time)
}

func quantityValue(q *resource.Quantity) int64 {
	if q == nil {
		return 0
	}
	return q.Value()
}

func maxDurationSecondsOf(d *metav1.Duration) int {
	if d == nil {
		return 0
	}
	return int(d.Seconds())
}

// ---------- SSRF-safe agent proxy helpers (mirrors ws/dialer.go; kept
// unexported and duplicated here since dialer.go's equivalents are
// unexported and this package cannot import them) ----------

// isDNS1123Label reports whether name is a valid DNS-1123 label (valid
// Kubernetes resource name component). Namespace and pod names flow into a
// constructed proxy URL, so both are validated against this rule before use
// to prevent SSRF injection. A label must consist of lower-case alphanumerics
// and hyphens, start and end with an alphanumeric, and be at most 63
// characters long.
func isDNS1123Label(name string) bool {
	if len(name) == 0 || len(name) > 63 {
		return false
	}
	for i, r := range name {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '-' {
			return false
		}
		if i == 0 || i == len(name)-1 {
			if r == '-' {
				return false
			}
		}
	}
	return true
}

// proxyHeaderAllowlist is the set of request headers forwarded from the
// dashboard-facing API to the in-cluster agent. Anything not in this
// set is dropped — most importantly Cookie, Authorization, and the CSRF
// header, which are user-session material the agent doesn't need.
var proxyHeaderAllowlist = map[string]bool{
	"Accept":            true,
	"Accept-Encoding":   true,
	"Range":             true,
	"If-None-Match":     true,
	"If-Modified-Since": true,
}

// responseHeaderDenylist strips hop-by-hop and auth-adjacent response
// headers from the agent before passing the body through to the browser.
var responseHeaderDenylist = map[string]bool{
	"Set-Cookie":         true,
	"Www-Authenticate":   true,
	"Proxy-Authenticate": true,
	"Connection":         true,
	"Keep-Alive":         true,
	"Transfer-Encoding":  true,
	"Upgrade":            true,
}

func copyProxyHeaders(dst, src http.Header) {
	for k, v := range src {
		if !proxyHeaderAllowlist[http.CanonicalHeaderKey(k)] {
			continue
		}
		dst[http.CanonicalHeaderKey(k)] = v
	}
}

func copyResponseHeaders(dst, src http.Header) {
	for k, v := range src {
		if responseHeaderDenylist[http.CanonicalHeaderKey(k)] {
			continue
		}
		dst[http.CanonicalHeaderKey(k)] = v
	}
}
