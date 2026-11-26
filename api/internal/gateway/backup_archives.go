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
	upstream, err := http.NewRequestWithContext(ctx, http.MethodGet, upstreamURL, nil)
	if err != nil {
		http.Error(w, "backup export unavailable", http.StatusBadGateway)
		return
	}
	resp, err := transport.RoundTrip(upstream)
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

// copyFlushing copies the worker's body to the client, flushing after every
// chunk so long archives reach the client as they are produced. It returns a
// non-nil error only when the worker's body fails before io.EOF; client write
// or flush failures mean the client is gone and return nil.
func copyFlushing(w http.ResponseWriter, body io.Reader) error {
	rc := http.NewResponseController(w)
	buf := make([]byte, 32<<10)
	for {
		n, readErr := body.Read(buf)
		if n > 0 {
			if _, writeErr := w.Write(buf[:n]); writeErr != nil {
				return nil
			}
			if flushErr := rc.Flush(); flushErr != nil {
				return nil
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
