package gateway

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"

	"k8s.io/apimachinery/pkg/util/validation"

	"github.com/GameplanePanel/gameplane/api/internal/gatewayprotocol"
	"github.com/GameplanePanel/gameplane/api/internal/kube"
	"github.com/GameplanePanel/gameplane/api/internal/workerconn"
)

// backupExportWorkerPort is the port the backup export worker listens on
// inside its ClusterIP Service (spec 041).
const backupExportWorkerPort = "9443"

// serveBackupExport streams one ready backup archive from its export worker.
// Every identity in the route is re-checked on each request: the GameServer,
// the BackupExport's UID, and the Backup it names, which must belong to the
// same GameServer and carry the UID the export pinned.
func (h *handler) serveBackupExport(w http.ResponseWriter, req *http.Request, target gatewayprotocol.BackupExportTarget) {
	if req.URL.RawPath != "" || req.URL.RawQuery != "" || target.Cluster != h.cfg.Cluster || !h.namespaceAllowed(target.Namespace) || req.Method != http.MethodGet || req.Header.Get("Upgrade") != "" {
		http.NotFound(w, req)
		return
	}
	if req.ContentLength != 0 {
		http.Error(w, "backup export download has no request body", http.StatusBadRequest)
		return
	}
	ctx, cancel := h.requestContext(req.Context(), req.TLS.PeerCertificates[0].NotAfter, true)
	defer cancel()
	req = req.WithContext(ctx)
	lookupCtx, finishLookup := context.WithTimeout(ctx, operationTimeout)
	defer finishLookup()
	if err := h.verifyTarget(lookupCtx, target.Target); err != nil {
		http.NotFound(w, req)
		return
	}
	export, err := h.client.GetBackupExport(lookupCtx, target.Namespace, target.Export)
	if err != nil || export == nil || string(export.UID) != target.ExportUID || export.DeletionTimestamp != nil {
		http.NotFound(w, req)
		return
	}
	backup, err := h.client.GetBackup(lookupCtx, target.Namespace, export.Spec.BackupRef.Name)
	finishLookup()
	if err != nil || !backupExportBelongs(backup, export, target) {
		http.NotFound(w, req)
		return
	}
	// An empty phase means the operator has not reconciled the export yet.
	if export.Status.Phase == "" || export.Status.Phase == kube.BackupExportPhasePending {
		http.Error(w, "backup export is still preparing", http.StatusConflict)
		return
	}
	if export.Status.Phase != kube.BackupExportPhaseReady || len(validation.IsDNS1123Label(export.Status.ServiceName)) != 0 {
		http.NotFound(w, req)
		return
	}
	transport, err := h.transport(h.cfg.AgentTLS)
	if err != nil {
		http.Error(w, "backup export credentials unavailable", http.StatusServiceUnavailable)
		return
	}
	defer transport.CloseIdleConnections()
	upstreamURL := "https://" + export.Status.ServiceName + "." + target.Namespace + ".svc.cluster.local:" + backupExportWorkerPort + "/export/" + target.ExportUID
	resp, err := workerconn.Do(ctx, transport.RoundTrip, func(rctx context.Context, _ io.Reader) (*http.Request, error) {
		return http.NewRequestWithContext(rctx, http.MethodGet, upstreamURL, nil)
	}, nil)
	if errors.Is(err, workerconn.ErrBuildRequest) {
		http.Error(w, "backup export unavailable", http.StatusBadGateway)
		return
	}
	if err != nil {
		http.Error(w, "backup export worker unavailable", http.StatusBadGateway)
		return
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 300 && resp.StatusCode < 400 {
		http.Error(w, "backup export redirects are not supported", http.StatusBadGateway)
		return
	}
	for _, name := range []string{"Content-Type", "Content-Disposition"} {
		if value := resp.Header.Get(name); value != "" {
			w.Header().Set(name, value)
		}
	}
	w.WriteHeader(resp.StatusCode)
	if err := copyFlushing(w, resp.Body); err != nil {
		if errors.Is(err, errClientGone) {
			// The client left mid-stream; there is nobody to tell.
			return
		}
		// The status line is already on the wire, so the response cannot be
		// turned into an error. Abort the connection so the client sees an
		// unexpected EOF rather than a clean end of a truncated archive.
		panic(http.ErrAbortHandler)
	}
}

// backupExportBelongs reports whether the Backup named by the export is the
// one the export pinned and is owned by the GameServer the route names.
func backupExportBelongs(backup *kube.Backup, export *kube.BackupExport, target gatewayprotocol.BackupExportTarget) bool {
	return backup != nil && backup.DeletionTimestamp == nil && string(backup.UID) == export.Spec.BackupUID && backup.Spec.ServerRef.Name == target.Name
}

// errClientGone reports that the client write or flush failed: the client
// has left, so the copy stops without aborting anything.
var errClientGone = errors.New("client went away")

// copyFlushing copies the worker's body to the client, flushing after every
// chunk so long archives reach the client as they are produced. It returns
// errClientGone when a client write or flush fails, and a non-nil error
// (other than errClientGone) when the worker's body fails before io.EOF.
func copyFlushing(w http.ResponseWriter, body io.Reader) error {
	rc := http.NewResponseController(w)
	buf := make([]byte, 32<<10)
	for {
		n, readErr := body.Read(buf)
		if n > 0 {
			if _, writeErr := w.Write(buf[:n]); writeErr != nil {
				return errClientGone
			}
			if flushErr := rc.Flush(); flushErr != nil {
				return errClientGone
			}
		}
		if readErr != nil {
			if errors.Is(readErr, io.EOF) {
				return nil
			}
			return fmt.Errorf("read backup export from worker: %w", readErr)
		}
	}
}

// restoreUploadWorkerPort is the port the archive receiver worker listens on
// inside its ClusterIP Service (spec 041).
const restoreUploadWorkerPort = "9443"

// restoreUploadDefaultMaxBytes caps one upload body when the gateway has no
// configured limit (spec 041: 20 GiB).
const restoreUploadDefaultMaxBytes int64 = 20 << 30

// restoreUploadReplyLimit bounds how much of the receiver's reply is relayed.
const restoreUploadReplyLimit = 4 << 10

// serveRestoreUpload streams one uploaded archive to its receiver worker. Every
// identity in the route is re-checked on each request: the GameServer, the
// Restore's UID, and the Restore itself, which must target the same GameServer,
// carry an upload, and be Receiving with a receiver Service. The body is
// streamed with the length the client declared; a failure before the worker
// answers is reported as 502, and the worker sees a truncated body and aborts.
func (h *handler) serveRestoreUpload(w http.ResponseWriter, req *http.Request, target gatewayprotocol.RestoreUploadTarget) {
	if req.URL.RawPath != "" || req.URL.RawQuery != "" || target.Cluster != h.cfg.Cluster || !h.namespaceAllowed(target.Namespace) || req.Method != http.MethodPut || req.Header.Get("Upgrade") != "" {
		http.NotFound(w, req)
		return
	}
	if req.ContentLength <= 0 {
		http.Error(w, "restore upload requires a Content-Length", http.StatusLengthRequired)
		return
	}
	maxBytes := h.cfg.BackupsMaxUploadBytes
	if maxBytes == 0 {
		maxBytes = restoreUploadDefaultMaxBytes
	}
	if req.ContentLength > maxBytes {
		http.Error(w, "restore upload too large", http.StatusRequestEntityTooLarge)
		return
	}
	ctx, cancel := h.requestContext(req.Context(), req.TLS.PeerCertificates[0].NotAfter, true)
	defer cancel()
	req = req.WithContext(ctx)
	lookupCtx, finishLookup := context.WithTimeout(ctx, operationTimeout)
	defer finishLookup()
	if err := h.verifyTarget(lookupCtx, target.Target); err != nil {
		http.NotFound(w, req)
		return
	}
	restore, err := kube.GetRestore(lookupCtx, h.client.Dynamic, target.Namespace, target.Restore)
	finishLookup()
	if err != nil {
		http.Error(w, "restore lookup unavailable", http.StatusServiceUnavailable)
		return
	}
	if !restoreUploadBelongs(restore, target) {
		http.NotFound(w, req)
		return
	}
	if restore.Status.Phase != kube.RestorePhaseReceiving {
		http.Error(w, "restore is not receiving an upload", http.StatusConflict)
		return
	}
	receiver := ""
	if restore.Status.Upload != nil {
		receiver = restore.Status.Upload.ReceiverService
	}
	if receiver == "" {
		http.Error(w, "restore upload receiver is not ready", http.StatusConflict)
		return
	}
	if len(validation.IsDNS1123Label(receiver)) != 0 {
		http.NotFound(w, req)
		return
	}
	transport, err := h.transport(h.cfg.AgentTLS)
	if err != nil {
		http.Error(w, "restore upload credentials unavailable", http.StatusServiceUnavailable)
		return
	}
	defer transport.CloseIdleConnections()
	upstreamURL := "https://" + receiver + "." + target.Namespace + ".svc:" + restoreUploadWorkerPort + "/upload/" + target.RestoreUID
	resp, err := workerconn.Do(ctx, transport.RoundTrip, func(rctx context.Context, b io.Reader) (*http.Request, error) {
		upstream, err := http.NewRequestWithContext(rctx, http.MethodPut, upstreamURL, b)
		if err != nil {
			return nil, fmt.Errorf("build restore upload request: %w", err)
		}
		upstream.ContentLength = req.ContentLength
		return upstream, nil
	}, req.Body)
	if errors.Is(err, workerconn.ErrBuildRequest) {
		http.Error(w, "restore upload unavailable", http.StatusBadGateway)
		return
	}
	if err != nil {
		http.Error(w, "restore upload worker unavailable; the archive was not confirmed", http.StatusBadGateway)
		return
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 300 && resp.StatusCode < 400 {
		http.Error(w, "restore upload redirects are not supported", http.StatusBadGateway)
		return
	}
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, io.LimitReader(resp.Body, restoreUploadReplyLimit))
}

// restoreUploadBelongs reports whether the Restore is the one the route names:
// the same UID, not being deleted, an upload restore, and targeting the
// GameServer the route names.
func restoreUploadBelongs(restore *kube.Restore, target gatewayprotocol.RestoreUploadTarget) bool {
	return restore != nil && restore.DeletionTimestamp == nil && string(restore.UID) == target.RestoreUID && restore.Spec.Upload != nil && restore.Spec.ServerRef.Name == target.Name
}
