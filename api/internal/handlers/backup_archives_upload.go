package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/util/validation"

	"github.com/GameplanePanel/gameplane/api/internal/gatewayprotocol"
	"github.com/GameplanePanel/gameplane/api/internal/httperr"
	"github.com/GameplanePanel/gameplane/api/internal/kube"
	"github.com/GameplanePanel/gameplane/api/internal/messages"
	"github.com/GameplanePanel/gameplane/api/internal/workerconn"
)

// Archive uploads and restore confirmation (spec 041). An upload creates an
// upload Restore, waits for the operator to start the archive receiver,
// streams the body to it, and answers 202 once the receiver has the whole
// file. The operator checks the archive and waits; the client then confirms
// through the confirm route, and the operator runs the restore.

const (
	// restoreUploadDefaultMaxBytes caps one upload when no limit is configured (20 GiB).
	restoreUploadDefaultMaxBytes int64 = 20 << 30
	// restoreUploadFileNameMax mirrors the operator's RestoreUpload.fileName limit.
	restoreUploadFileNameMax = 255

	restorePhasePending   = "Pending"
	restorePhaseFailed    = "Failed"
	restorePhaseSucceeded = "Succeeded"

	backupPhasePending = "Pending"
	backupPhaseRunning = "Running"

	// restoreWipeCompletedAnnotation matches the operator's WipeCompletedAnnotation.
	restoreWipeCompletedAnnotation = "gameplane.local/wipe-data-completed"

	// restoreNoStagingClassPrefix is the operator's Failed message prefix
	// when no staging storage class is configured or defaulted.
	restoreNoStagingClassPrefix = "NoStagingStorageClass"

	// uploadClientClosedStatus is the audit status for a client that went away first.
	uploadClientClosedStatus = 499
)

// errUploadOverflow reports a client body longer than its declared size.
var errUploadOverflow = errors.New("restore upload is longer than its declared size")

// uploadBodyReader yields exactly the declared number of bytes from the
// client body. Bytes past the declared size set overflowed and fail the read,
// so an over-long upload is aborted instead of being silently truncated.
type uploadBodyReader struct {
	r          io.Reader
	remaining  int64
	overflowed bool
}

// Read implements io.Reader for the declared bytes only.
func (b *uploadBodyReader) Read(p []byte) (int, error) {
	if b.remaining <= 0 {
		var probe [1]byte
		n, err := io.ReadFull(b.r, probe[:])
		if n > 0 {
			b.overflowed = true
			return 0, errUploadOverflow
		}
		if errors.Is(err, io.EOF) {
			return 0, io.EOF
		}
		return 0, err
	}
	if int64(len(p)) > b.remaining {
		p = p[:b.remaining]
	}
	n, err := b.r.Read(p)
	b.remaining -= int64(n)
	if errors.Is(err, io.EOF) && b.remaining > 0 {
		return n, io.ErrUnexpectedEOF
	}
	return n, err
}

// defaultRestoreUploadURL builds the receiver URL for an upload.
func defaultRestoreUploadURL(host backupExportHost, uid string) *url.URL {
	return &url.URL{Scheme: "https", Host: string(host), Path: "/upload/" + uid}
}

// upload handles POST /restores:upload?server=&format=&fileName=[&template=].
// The body is the raw archive; Content-Length is required and must not
// exceed the configured maximum.
func (h *backupArchiveHandler) upload(w http.ResponseWriter, req *http.Request) {
	k, ok := resolveCluster(w, req, h.reg)
	if !ok {
		return
	}
	ns, ok := resolveNS(w, req)
	if !ok {
		return
	}
	q := req.URL.Query()
	server := strings.TrimSpace(q.Get("server"))
	format := q.Get("format")
	fileName := q.Get("fileName")
	template := strings.TrimSpace(q.Get("template"))
	const auditPath = "/restores:upload"
	size := max(req.ContentLength, 0)
	target := fmt.Sprintf("%s:%s:%d", server, fileName, size)

	if req.ContentLength <= 0 {
		h.refuseMessage(w, req, auditPath, target, "length_required", messages.New("server.restore.upload.length_required", nil))
		return
	}
	maxBytes := h.cfg.MaxUploadBytes
	if maxBytes < 1 {
		maxBytes = restoreUploadDefaultMaxBytes
	}
	if req.ContentLength > maxBytes {
		h.refuseMessage(w, req, auditPath, target, "too_large", messages.New("server.restore.upload.too_large", nil))
		return
	}
	if reason := validateUploadRequest(server, format, fileName, template); reason != "" {
		h.refuseMessage(w, req, auditPath, target, "bad_request", messages.New("server.restore.upload.bad_request", map[string]any{"message": reason}))
		return
	}

	gs, err := k.GetGameServer(req.Context(), ns, server)
	if err != nil {
		if apierrors.IsNotFound(err) {
			h.refuseCode(w, req, auditPath, target, "not_found", http.StatusNotFound, errors.New("not found"))
			return
		}
		h.refuseCode(w, req, auditPath, target, "error", http.StatusInternalServerError, err)
		return
	}
	if gs.DeletionTimestamp != nil {
		h.refuseCode(w, req, auditPath, target, "not_found", http.StatusNotFound, errors.New("not found"))
		return
	}
	busy, err := h.serverBusy(req.Context(), k, ns, gs)
	if err != nil {
		h.refuseCode(w, req, auditPath, target, "error", http.StatusInternalServerError, err)
		return
	}
	if busy {
		h.refuseMessage(w, req, auditPath, target, "server_busy", messages.New("server.restore.upload.server_busy", nil))
		return
	}

	// The intent row is written before the Restore exists. A later failure
	// appends a correction row with the real outcome.
	if !h.auditOrFail(w, req, auditPath, target, "", http.StatusAccepted) {
		return
	}
	restoreName, restoreUID, err := kube.CreateUploadRestore(req.Context(), k.Dynamic, ns, server, fileName, format, req.ContentLength, template)
	if err != nil {
		h.correct(req, auditPath, target, "create_failed", http.StatusInternalServerError)
		httperr.Write(w, req, err)
		return
	}
	delivered := false
	// Cleanup must survive a client disconnect, so it runs on a context
	// detached from the request's cancellation.
	cleanupCtx := context.WithoutCancel(req.Context())
	defer func() {
		if !delivered {
			h.deleteRestore(cleanupCtx, k, ns, restoreName)
		}
	}()

	receiver, reason, msg, err := h.awaitReceiving(req.Context(), k, ns, restoreName)
	if err != nil {
		h.correct(req, auditPath, target, "error", http.StatusInternalServerError)
		httperr.Write(w, req, err)
		return
	}
	if receiver == "" {
		h.uploadFailed(w, req, auditPath, target, reason, msg)
		return
	}

	body := &uploadBodyReader{r: req.Body, remaining: req.ContentLength}
	reason, msg = h.sendUpload(req, ns, gs, restoreName, string(restoreUID), receiver, req.ContentLength, body)
	if reason != "" {
		h.uploadFailed(w, req, auditPath, target, reason, msg)
		return
	}
	delivered = true
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	_ = json.NewEncoder(w).Encode(map[string]string{"restore": restoreName})
}

// validateUploadRequest returns the reason the upload's query values are
// refused, or "" when they are valid.
func validateUploadRequest(server, format, fileName, template string) string {
	switch {
	case !isDNS1123Label(server):
		return "the server name is not valid"
	case format != backupArchiveFormatTarGz && format != backupArchiveFormatZip:
		return "format must be tar.gz or zip"
	case fileName == "" || len(fileName) > restoreUploadFileNameMax:
		return "the file name must be 1 to 255 characters"
	case strings.ContainsAny(fileName, "/\\\x00") || fileName == "." || fileName == "..":
		return "the file name must not contain path separators"
	case template != "" && len(validation.IsDNS1123Subdomain(template)) != 0:
		return "the template name is not valid"
	}
	return ""
}

// uploadFailed records the failure as a correction row and writes the
// client response. A nil message means the client went away first.
func (h *backupArchiveHandler) uploadFailed(w http.ResponseWriter, req *http.Request, auditPath, target, reason string, msg *httperr.Message) {
	if msg == nil {
		h.correct(req, auditPath, target, reason, uploadClientClosedStatus)
		return
	}
	h.correct(req, auditPath, target, reason, msg.Status)
	httperr.WriteMessage(w, req, msg)
}

// restoreUploadState is the part of a Restore an upload waits on.
type restoreUploadState struct {
	phase    string
	message  string
	receiver string
}

// loadRestoreUploadState reads the Restore's phase, failure message and
// receiver Service. It returns nil, nil when the Restore does not exist.
func loadRestoreUploadState(ctx context.Context, k *kube.Client, ns, name string) (*restoreUploadState, error) {
	u, err := k.Dynamic.Resource(kube.GVRRestore).Namespace(ns).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("get restore %s: %w", name, err)
	}
	st := &restoreUploadState{}
	st.phase, _, _ = unstructured.NestedString(u.Object, "status", "phase")
	st.message, _, _ = unstructured.NestedString(u.Object, "status", "message")
	st.receiver, _, _ = unstructured.NestedString(u.Object, "status", "upload", "receiverService")
	return st, nil
}

// awaitReceiving polls the upload Restore until the operator reports
// Receiving with a receiver Service. It returns that Service name, or ""
// with a reason and the client-facing message. A "" name with a nil message
// and nil error means the client disconnected first. Polling respects ctx.
func (h *backupArchiveHandler) awaitReceiving(ctx context.Context, k *kube.Client, ns, name string) (string, string, *httperr.Message, error) {
	deadline := time.NewTimer(h.startTimeout)
	defer deadline.Stop()
	ticker := time.NewTicker(h.pollInterval)
	defer ticker.Stop()
	for {
		st, err := loadRestoreUploadState(ctx, k, ns, name)
		if err != nil {
			if ctx.Err() != nil {
				return "", "client_disconnected", nil, nil
			}
			return "", "", nil, err
		}
		if st == nil {
			return "", "", nil, fmt.Errorf("restore %s disappeared", name)
		}
		switch {
		case st.phase == string(kube.RestorePhaseReceiving) && st.receiver != "":
			return st.receiver, "", nil, nil
		case st.phase == restorePhaseFailed:
			if strings.HasPrefix(st.message, restoreNoStagingClassPrefix) {
				return "", "no_storage_class", messages.New("server.restore.upload.no_storage_class", nil), nil
			}
			slog.Warn("restore upload: restore failed", "restore", name, "message", st.message)
			return "", "upload_failed", messages.New("server.restore.upload.failed", nil), nil
		case st.phase == "" || st.phase == restorePhasePending || st.phase == string(kube.RestorePhaseReceiving):
			// Still preparing the receiver; keep polling.
		default:
			slog.Warn("restore upload: restore left the upload phases", "restore", name, "phase", st.phase)
			return "", "upload_failed", messages.New("server.restore.upload.failed", nil), nil
		}
		select {
		case <-ctx.Done():
			return "", "client_disconnected", nil, nil
		case <-deadline.C:
			slog.Warn("restore upload: receiver not ready in time", "restore", name)
			return "", "start_timeout", messages.New("server.restore.upload.failed", nil), nil
		case <-ticker.C:
		}
	}
}

// sendUpload streams the declared bytes to the receiver, locally over mTLS
// or through the cluster gateway for a remote cluster. It returns an empty
// reason when the receiver answered 200, which means it has the whole file.
// Otherwise it returns the correction reason and the client message, or a
// nil message when the client went away.
func (h *backupArchiveHandler) sendUpload(req *http.Request, ns string, gs *kube.GameServer, restoreName, restoreUID, receiver string, size int64, body *uploadBodyReader) (string, *httperr.Message) {
	var (
		resp *http.Response
		err  error
	)
	if isRemoteCluster(req) {
		resp, err = h.sendRemoteUpload(req, ns, gs, restoreName, restoreUID, size, body)
	} else {
		resp, err = h.sendLocalUpload(req, ns, receiver, restoreUID, size, body)
	}
	if err != nil {
		if req.Context().Err() != nil {
			return "client_disconnected", nil
		}
		if body.overflowed {
			return "overflow", messages.New("server.restore.upload.bad_request", map[string]any{"message": "the body is longer than the declared size"})
		}
		slog.Warn("restore upload: archive receiver unavailable", "restore", restoreName, "err", err)
		return "upload_failed", messages.New("server.restore.upload.failed", nil)
	}
	defer func() { _ = resp.Body.Close() }()
	// The receiver may answer before the client has read past the declared
	// size, so an overlong body is refused even when the reply is 200.
	if body.overflowed {
		return "overflow", messages.New("server.restore.upload.bad_request", map[string]any{"message": "the body is longer than the declared size"})
	}
	if resp.StatusCode != http.StatusOK {
		slog.Warn("restore upload: archive receiver refused the upload", "restore", restoreName, "status", resp.StatusCode)
		return "worker_refused", messages.New("server.restore.upload.failed", nil)
	}
	return "", nil
}

// sendLocalUpload PUTs the body to the receiver Service in this cluster.
func (h *backupArchiveHandler) sendLocalUpload(req *http.Request, ns, receiver, restoreUID string, size int64, body io.Reader) (*http.Response, error) {
	if h.tlsClient == nil {
		return nil, errors.New("agent mTLS not configured")
	}
	host, ok := newBackupExportHost(receiver, ns)
	if !ok || restoreUID == "" || strings.ContainsAny(restoreUID, "/?#") {
		return nil, errors.New("restore upload has an invalid receiver address")
	}
	target := h.uploadURL(host, restoreUID).String()
	resp, err := workerconn.Do(req.Context(), h.tlsClient.Do, func(b io.Reader) (*http.Request, error) {
		upReq, err := http.NewRequestWithContext(req.Context(), http.MethodPut, target, b)
		if err != nil {
			return nil, fmt.Errorf("build restore upload request: %w", err)
		}
		upReq.ContentLength = size
		return upReq, nil
	}, body)
	if err != nil {
		return nil, fmt.Errorf("restore upload receiver: %w", err)
	}
	return resp, nil
}

// sendRemoteUpload streams the body through the cluster gateway.
func (h *backupArchiveHandler) sendRemoteUpload(req *http.Request, ns string, gs *kube.GameServer, restoreName, restoreUID string, size int64, body io.Reader) (*http.Response, error) {
	if h.uploads == nil {
		return nil, errors.New("agent gateway not configured")
	}
	cluster := strings.TrimSpace(req.URL.Query().Get("cluster"))
	target := gatewayprotocol.RestoreUploadTarget{
		Target: gatewayprotocol.Target{
			Cluster:   cluster,
			Namespace: ns,
			Name:      gs.Name,
			UID:       string(gs.UID),
		},
		Restore:    restoreName,
		RestoreUID: restoreUID,
	}
	resp, err := h.uploads.RestoreUploadStream(req.Context(), cluster, target, body, size)
	if err != nil {
		return nil, fmt.Errorf("restore upload gateway: %w", err)
	}
	return resp, nil
}

// serverBusy reports whether a wipe, a backup or a restore owns the
// GameServer (spec 041 FR-014). Non-terminal Backups and Restores count, and
// so does a wipe that has been requested and not completed.
func (h *backupArchiveHandler) serverBusy(ctx context.Context, k *kube.Client, ns string, gs *kube.GameServer) (bool, error) {
	if requested := gs.Annotations[wipeRequestedAnnotation]; requested != "" && requested != gs.Annotations[restoreWipeCompletedAnnotation] {
		return true, nil
	}
	backups, err := k.Dynamic.Resource(kube.GVRBackup).Namespace(ns).List(ctx, metav1.ListOptions{})
	if err != nil {
		return false, fmt.Errorf("list backups in %s: %w", ns, err)
	}
	for _, item := range backups.Items {
		if serverRefName(item.Object) != gs.Name {
			continue
		}
		phase, _, _ := unstructured.NestedString(item.Object, "status", "phase")
		if phase == "" || phase == backupPhasePending || phase == backupPhaseRunning {
			return true, nil
		}
	}
	restores, err := k.Dynamic.Resource(kube.GVRRestore).Namespace(ns).List(ctx, metav1.ListOptions{})
	if err != nil {
		return false, fmt.Errorf("list restores in %s: %w", ns, err)
	}
	for _, item := range restores.Items {
		if serverRefName(item.Object) != gs.Name {
			continue
		}
		phase, _, _ := unstructured.NestedString(item.Object, "status", "phase")
		if phase != restorePhaseSucceeded && phase != restorePhaseFailed {
			return true, nil
		}
	}
	return false, nil
}

// serverRefName returns the GameServer a Backup or Restore names.
func serverRefName(obj map[string]any) string {
	name, _, _ := unstructured.NestedString(obj, "spec", "serverRef", "name")
	return name
}

// deleteRestore removes an upload Restore once the request has ended. The
// caller passes a context detached from the request so a client disconnect
// does not skip it; this function only adds its own timeout.
func (h *backupArchiveHandler) deleteRestore(ctx context.Context, k *kube.Client, ns, name string) {
	cctx, cancel := context.WithTimeout(ctx, backupArchiveDeleteTimeout)
	defer cancel()
	if err := kube.DeleteRestore(cctx, k.Dynamic, ns, name); err != nil {
		slog.Warn("restore upload: delete restore failed", "restore", name, "err", err)
	}
}

// confirmRestoreRequest is the body of POST /restores/{name}:confirm.
type confirmRestoreRequest struct {
	Generation int64 `json:"generation"`
}

// confirm handles POST /restores/{name}:confirm with {"generation": N}. It
// succeeds only while the upload Restore waits in AwaitingConfirmation at
// that generation.
func (h *backupArchiveHandler) confirm(w http.ResponseWriter, req *http.Request) {
	k, ok := resolveCluster(w, req, h.reg)
	if !ok {
		return
	}
	name := chi.URLParam(req, "name")
	ns, ok := resolveNS(w, req)
	if !ok {
		return
	}
	auditPath := fmt.Sprintf("/restores/%s:confirm", name)

	var body confirmRestoreRequest
	if err := json.NewDecoder(io.LimitReader(req.Body, 1<<10)).Decode(&body); err != nil || body.Generation < 1 {
		h.refuseMessage(w, req, auditPath, name, "bad_request", messages.New("server.restore.confirm.bad_request", nil))
		return
	}
	r, err := kube.GetRestore(req.Context(), k.Dynamic, ns, name)
	if err != nil {
		h.refuseCode(w, req, auditPath, name, "error", http.StatusInternalServerError, err)
		return
	}
	if r == nil {
		h.refuseCode(w, req, auditPath, name, "not_found", http.StatusNotFound, errors.New("not found"))
		return
	}
	if r.Spec.Upload == nil || r.Status.Phase != kube.RestorePhaseAwaitingConfirmation || r.Generation != body.Generation {
		h.refuseMessage(w, req, auditPath, name, "not_ready", messages.New("server.restore.confirm.not_ready", nil))
		return
	}

	if !h.auditOrFail(w, req, auditPath, name, "", http.StatusAccepted) {
		return
	}
	if err := kube.ConfirmUploadRestore(req.Context(), k.Dynamic, ns, name, body.Generation); err != nil {
		if apierrors.IsConflict(err) {
			h.correct(req, auditPath, name, "not_ready", http.StatusConflict)
			httperr.WriteMessage(w, req, messages.New("server.restore.confirm.not_ready", nil))
			return
		}
		h.correct(req, auditPath, name, "error", http.StatusInternalServerError)
		httperr.Write(w, req, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	_ = json.NewEncoder(w).Encode(map[string]string{"restore": name})
}
