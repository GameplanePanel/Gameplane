package handlers

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"

	"github.com/GameplanePanel/gameplane/api/internal/audit"
	"github.com/GameplanePanel/gameplane/api/internal/gatewayprotocol"
	"github.com/GameplanePanel/gameplane/api/internal/httperr"
	"github.com/GameplanePanel/gameplane/api/internal/kube"
	"github.com/GameplanePanel/gameplane/api/internal/messages"
	"github.com/GameplanePanel/gameplane/api/internal/scope"
	"github.com/GameplanePanel/gameplane/api/internal/ws"
)

// Backup archive downloads (spec 041). A download creates a BackupExport,
// waits for the operator to start the export worker, streams the archive
// from it, and deletes the export when the request ends.
const (
	backupArchiveFormatTarGz = "tar.gz"
	backupArchiveFormatZip   = "zip"

	// backupArchiveAgentPort is the port the export worker Service listens on.
	backupArchiveAgentPort = 9443
	// backupArchiveDefaultMaxExports is used when the configured cap is unset.
	backupArchiveDefaultMaxExports = 3

	backupArchivePollInterval      = time.Second
	backupArchiveStartTimeout      = 120 * time.Second
	backupArchiveDeleteTimeout     = 10 * time.Second
	backupArchiveCorrectionTimeout = 5 * time.Second
)

// BackupArchiveConfig holds the cluster-wide settings for backup archive
// downloads.
type BackupArchiveConfig struct {
	// GatewayNamespace is the central namespace holding enrolled gateway
	// credentials. Remote clusters are reached through it.
	GatewayNamespace string
	// MaxConcurrentExports caps active BackupExports per namespace (Helm
	// backups.maxConcurrentExports). Values below 1 select the default.
	MaxConcurrentExports int
	// AgentCABundle, AgentClientCert and AgentClientKey authenticate the
	// API to export workers in the local cluster over mTLS.
	AgentCABundle   string
	AgentClientCert string
	AgentClientKey  string
	// MaxUploadBytes caps one archive upload (Helm backups.maxUploadSize).
	// Values below 1 select the default.
	MaxUploadBytes int64
}

// backupExportGateway is the cluster gateway call for remote archive streams.
type backupExportGateway interface {
	BackupExportDownload(ctx context.Context, target gatewayprotocol.BackupExportTarget, headers http.Header) (*http.Response, error)
}

// restoreUploadGateway is the cluster gateway call that streams an uploaded
// archive to a remote cluster's receiver.
type restoreUploadGateway interface {
	RestoreUploadStream(ctx context.Context, cluster string, target gatewayprotocol.RestoreUploadTarget, body io.Reader, contentLength int64) (*http.Response, error)
}

// backupExportHost is an in-cluster "host:port" authority that can only be
// produced by newBackupExportHost, so the export worker address is always
// built from validated DNS-1123 labels.
type backupExportHost string

// newBackupExportHost builds the export worker's in-cluster Service name
// (<service>.<ns>.svc.cluster.local:9443), or reports ok=false when either
// part is not a DNS-1123 label.
func newBackupExportHost(service, ns string) (backupExportHost, bool) {
	if !isDNS1123Label(service) || !isDNS1123Label(ns) {
		return "", false
	}
	return backupExportHost(fmt.Sprintf("%s.%s.svc.cluster.local:%d", service, ns, backupArchiveAgentPort)), true
}

// backupArchiveHandler serves GET /backups/{name}:download.
type backupArchiveHandler struct {
	reg       *kube.Registry
	auditor   *audit.Auditor
	cfg       BackupArchiveConfig
	tlsClient *http.Client
	gateway   backupExportGateway
	// uploads streams archive uploads to remote clusters (spec 041).
	uploads restoreUploadGateway
	// exportURL builds the worker URL for an export. Tests replace it to
	// point at a stand-in server.
	exportURL func(host backupExportHost, uid string) *url.URL
	// uploadURL builds the receiver URL for an upload. Tests replace it.
	uploadURL    func(host backupExportHost, uid string) *url.URL
	pollInterval time.Duration
	startTimeout time.Duration
}

// MountBackupArchives wires GET /backups/{name}:download and the archive
// upload routes (spec 041). The download is served through the protected
// router group, so the RBAC middleware maps it to backups:read. The upload
// and confirm routes are non-GET restores routes (backups:restore). The
// upload path "/restores:upload" has no first path segment "restores", so
// the rule in rbac.go does not match it until an explicit rule is added.
func MountBackupArchives(r chi.Router, reg *kube.Registry, auditor *audit.Auditor, cfg BackupArchiveConfig) {
	h := newBackupArchiveHandler(reg, auditor, cfg)
	r.Get("/backups/{name}:download", h.download)
	r.Post("/restores:upload", h.upload)
	r.Post("/restores/{name}:confirm", h.confirm)
}

func newBackupArchiveHandler(reg *kube.Registry, auditor *audit.Auditor, cfg BackupArchiveConfig) *backupArchiveHandler {
	if cfg.MaxConcurrentExports < 1 {
		cfg.MaxConcurrentExports = backupArchiveDefaultMaxExports
	}
	h := &backupArchiveHandler{
		reg:          reg,
		auditor:      auditor,
		cfg:          cfg,
		exportURL:    defaultBackupExportURL,
		uploadURL:    defaultRestoreUploadURL,
		pollInterval: backupArchivePollInterval,
		startTimeout: backupArchiveStartTimeout,
	}
	if cfg.AgentCABundle != "" && cfg.AgentClientCert != "" && cfg.AgentClientKey != "" {
		h.tlsClient = buildAgentHTTPClient(cfg.AgentCABundle, cfg.AgentClientCert, cfg.AgentClientKey)
	}
	if cfg.GatewayNamespace != "" {
		gw := ws.NewCaptureGatewayClient(reg, cfg.GatewayNamespace)
		h.gateway = gw
		h.uploads = gw
	}
	return h
}

func defaultBackupExportURL(host backupExportHost, uid string) *url.URL {
	return &url.URL{Scheme: "https", Host: string(host), Path: "/export/" + uid}
}

// backupSource is the part of a Backup the download needs.
type backupSource struct {
	backup     *kube.Backup
	completion time.Time
}

// download handles GET /backups/{name}:download?format=tar.gz|zip.
func (h *backupArchiveHandler) download(w http.ResponseWriter, req *http.Request) {
	k, ok := resolveCluster(w, req, h.reg)
	if !ok {
		return
	}
	name := chi.URLParam(req, "name")
	ns, ok := resolveNS(w, req)
	if !ok {
		return
	}
	format := req.URL.Query().Get("format")
	if format == "" {
		format = backupArchiveFormatTarGz
	}
	auditPath := fmt.Sprintf("/backups/%s:download", name)
	target := fmt.Sprintf("%s:%s", name, format)

	if format != backupArchiveFormatTarGz && format != backupArchiveFormatZip {
		h.refuseCode(w, req, auditPath, target, "invalid_format", http.StatusBadRequest, errors.New("format must be tar.gz or zip"))
		return
	}

	src, err := loadBackupSource(req.Context(), k, ns, name)
	if err != nil {
		h.refuseCode(w, req, auditPath, target, "error", http.StatusInternalServerError, err)
		return
	}
	if src == nil {
		h.refuseCode(w, req, auditPath, target, "not_found", http.StatusNotFound, errors.New("not found"))
		return
	}
	if src.backup.Status.Phase != backupPhaseSucceeded {
		h.refuseMessage(w, req, auditPath, target, "not_ready", messages.New("server.backup.download.not_ready", nil))
		return
	}
	if src.backup.Spec.Strategy == "volume-snapshot" {
		h.refuseMessage(w, req, auditPath, target, "snapshot_strategy", messages.New("server.backup.download.snapshot_strategy", nil))
		return
	}

	active, err := k.CountActiveBackupExports(req.Context(), ns)
	if err != nil {
		h.refuseCode(w, req, auditPath, target, "error", http.StatusInternalServerError, err)
		return
	}
	if active >= h.cfg.MaxConcurrentExports {
		h.refuseMessage(w, req, auditPath, target, "busy", messages.New("server.backup.download.busy", nil))
		return
	}

	// The intent row is written before the export exists. A later failure
	// appends a correction row with the real outcome.
	if !h.auditOrFail(w, req, auditPath, target, "", http.StatusOK) {
		return
	}

	// resolveCluster already validated the requested cluster, so RequestedCluster
	// names the cluster the export was made for.
	cluster := scope.RequestedCluster(req)
	export, err := k.CreateBackupExport(req.Context(), ns, name, string(src.backup.UID), format, cluster, nil)
	if err != nil {
		h.correct(req, auditPath, target, "create_failed", http.StatusInternalServerError)
		httperr.Write(w, req, err)
		return
	}
	defer h.deleteExport(req.Context(), k, ns, export.Name)

	ready, reason, msg, err := h.awaitExport(req.Context(), k, ns, export.Name)
	if err != nil {
		h.correct(req, auditPath, target, "error", http.StatusInternalServerError)
		httperr.Write(w, req, err)
		return
	}
	if ready == nil {
		if msg == nil {
			// The client went away while the export was starting.
			h.correct(req, auditPath, target, "client_disconnected", 499)
			return
		}
		h.correct(req, auditPath, target, reason, msg.Status)
		httperr.WriteMessage(w, req, msg)
		return
	}

	resp, status, err := h.openArchive(req, k, ns, src, ready)
	if err != nil {
		h.correct(req, auditPath, target, "open_failed", status)
		httperr.WriteCode(w, req, status, err)
		return
	}
	defer func() { _ = resp.Body.Close() }()

	h.stream(w, req, resp, src, format, auditPath, target)
}

// loadBackupSource fetches the Backup and the status fields the API needs.
// It returns nil, nil when the Backup does not exist. The typed kube.Backup
// does not carry completionTime, so the unstructured object is read as well.
func loadBackupSource(ctx context.Context, k *kube.Client, ns, name string) (*backupSource, error) {
	u, err := k.Dynamic.Resource(kube.GVRBackup).Namespace(ns).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("get backup %s: %w", name, err)
	}
	bk := &kube.Backup{}
	if err := runtime.DefaultUnstructuredConverter.FromUnstructured(u.Object, bk); err != nil {
		return nil, fmt.Errorf("get backup %s: convert from unstructured: %w", name, err)
	}
	src := &backupSource{backup: bk}
	if s, found, _ := unstructured.NestedString(u.Object, "status", "completionTime"); found {
		if t, perr := time.Parse(time.RFC3339, s); perr == nil {
			src.completion = t
		}
	}
	return src, nil
}

// awaitExport polls the export until the worker is Ready. It returns the
// Ready export, or nil with a reason and the client-facing message when the
// export cannot serve bytes. A nil export with a nil message and a nil error
// means the client disconnected first. Polling respects ctx.
func (h *backupArchiveHandler) awaitExport(ctx context.Context, k *kube.Client, ns, name string) (*kube.BackupExport, string, *httperr.Message, error) {
	deadline := time.NewTimer(h.startTimeout)
	defer deadline.Stop()
	ticker := time.NewTicker(h.pollInterval)
	defer ticker.Stop()
	for {
		exp, err := k.GetBackupExport(ctx, ns, name)
		if err != nil {
			if ctx.Err() != nil {
				return nil, "client_disconnected", nil, nil
			}
			return nil, "error", nil, err
		}
		if exp == nil {
			return nil, "error", nil, fmt.Errorf("backup export %s disappeared", name)
		}
		switch exp.Status.Phase {
		case kube.BackupExportPhaseReady:
			return exp, "", nil, nil
		case kube.BackupExportPhaseFailed, kube.BackupExportPhaseExpired:
			slog.Warn("backup download: export failed", "export", name, "message", exp.Status.Message)
			return nil, "export_failed", messages.New("server.backup.download.failed", nil), nil
		}
		select {
		case <-ctx.Done():
			return nil, "client_disconnected", nil, nil
		case <-deadline.C:
			return nil, "start_timeout", messages.New("server.backup.download.start_timeout", nil), nil
		case <-ticker.C:
		}
	}
}

// backupPhaseSucceeded is the Backup phase a download requires; it mirrors
// operator/api/v1alpha1's BackupPhaseSucceeded.
const backupPhaseSucceeded = "Succeeded"

// openArchive opens the worker's archive stream, locally over mTLS or
// through the cluster gateway for a remote cluster. On error it returns the
// HTTP status to report.
func (h *backupArchiveHandler) openArchive(req *http.Request, k *kube.Client, ns string, src *backupSource, exp *kube.BackupExport) (*http.Response, int, error) {
	if isRemoteCluster(req) {
		return h.openRemote(req, k, ns, src, exp)
	}
	return h.openLocal(req, ns, exp)
}

func (h *backupArchiveHandler) openLocal(req *http.Request, ns string, exp *kube.BackupExport) (*http.Response, int, error) {
	if h.tlsClient == nil {
		return nil, http.StatusServiceUnavailable, errors.New("agent mTLS not configured")
	}
	uid := string(exp.UID)
	host, ok := newBackupExportHost(exp.Status.ServiceName, ns)
	if !ok || uid == "" || strings.ContainsAny(uid, "/?#") {
		return nil, http.StatusBadGateway, errors.New("backup export has an invalid worker address")
	}
	upReq, err := http.NewRequestWithContext(req.Context(), http.MethodGet, h.exportURL(host, uid).String(), nil)
	if err != nil {
		return nil, http.StatusInternalServerError, fmt.Errorf("build backup export request: %w", err)
	}
	resp, err := h.tlsClient.Do(upReq)
	if err != nil {
		return nil, backupUpstreamStatus(err), fmt.Errorf("backup archive worker is unreachable: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		_ = resp.Body.Close()
		return nil, http.StatusBadGateway, fmt.Errorf("backup archive worker returned %s", http.StatusText(resp.StatusCode))
	}
	return resp, http.StatusOK, nil
}

func (h *backupArchiveHandler) openRemote(req *http.Request, k *kube.Client, ns string, src *backupSource, exp *kube.BackupExport) (*http.Response, int, error) {
	if h.gateway == nil {
		return nil, http.StatusServiceUnavailable, errors.New("agent gateway not configured")
	}
	gs, err := k.GetGameServer(req.Context(), ns, src.backup.Spec.ServerRef.Name)
	if err != nil {
		if apierrors.IsNotFound(err) {
			return nil, http.StatusNotFound, errors.New("not found")
		}
		return nil, http.StatusInternalServerError, fmt.Errorf("load game server for backup export: %w", err)
	}
	if gs.DeletionTimestamp != nil {
		return nil, http.StatusNotFound, errors.New("not found")
	}
	target := gatewayprotocol.BackupExportTarget{
		Target: gatewayprotocol.Target{
			Cluster:   strings.TrimSpace(req.URL.Query().Get("cluster")),
			Namespace: gs.Namespace,
			Name:      gs.Name,
			UID:       string(gs.UID),
		},
		Export:    exp.Name,
		ExportUID: string(exp.UID),
	}
	resp, err := h.gateway.BackupExportDownload(req.Context(), target, req.Header)
	if err != nil {
		return nil, backupUpstreamStatus(err), fmt.Errorf("backup archive worker is unreachable: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		_ = resp.Body.Close()
		return nil, http.StatusBadGateway, fmt.Errorf("backup archive worker returned %s", http.StatusText(resp.StatusCode))
	}
	return resp, http.StatusOK, nil
}

// stream copies the archive to the client. The status line and headers are
// sent before the first byte, so a failure after that point cannot become an
// error response. It records the failure and aborts the connection, so the
// client sees a failed download rather than a truncated file (FR-006).
func (h *backupArchiveHandler) stream(w http.ResponseWriter, req *http.Request, resp *http.Response, src *backupSource, format, auditPath, target string) {
	completion := src.completion
	if completion.IsZero() {
		completion = time.Now()
	}
	// The content type comes from the format, not from the worker's header.
	w.Header().Set("Content-Type", backupArchiveContentType(format))
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s-%s.%s"`,
		src.backup.Spec.ServerRef.Name, completion.UTC().Format("20060102-150405"), format))
	if src.backup.Status.Size != nil {
		w.Header().Set("X-Gameplane-Estimated-Bytes", strconv.FormatInt(src.backup.Status.Size.Value(), 10))
	}
	w.WriteHeader(http.StatusOK)

	if _, err := io.Copy(flushingWriter{w: w, rc: http.NewResponseController(w)}, resp.Body); err != nil {
		slog.Warn("backup archive stream interrupted", "path", auditPath, "err", err)
		h.correct(req, auditPath, target, "stream_failed", http.StatusBadGateway)
		// net/http aborts the response without a clean chunked terminator.
		// chi's Recoverer re-panics http.ErrAbortHandler, so the abort reaches the server.
		panic(http.ErrAbortHandler)
	}
}

// deleteExport removes the export once the request has ended. It runs on a
// context detached from the request so a client disconnect does not skip it.
func (h *backupArchiveHandler) deleteExport(ctx context.Context, k *kube.Client, ns, name string) {
	cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), backupArchiveDeleteTimeout)
	defer cancel()
	if err := k.DeleteBackupExport(cctx, ns, name); err != nil {
		slog.Warn("backup download: delete export failed", "export", name, "err", err)
	}
}

// refuseCode records a refused request and writes a status with a plain error.
func (h *backupArchiveHandler) refuseCode(w http.ResponseWriter, req *http.Request, auditPath, target, reason string, status int, err error) {
	if !h.auditOrFail(w, req, auditPath, target, reason, status) {
		return
	}
	httperr.WriteCode(w, req, status, err)
}

// refuseMessage records a refused request and writes a registry message.
func (h *backupArchiveHandler) refuseMessage(w http.ResponseWriter, req *http.Request, auditPath, target, reason string, m *httperr.Message) {
	if !h.auditOrFail(w, req, auditPath, target, reason, m.Status) {
		return
	}
	httperr.WriteMessage(w, req, m)
}

// auditOrFail writes an audit row carrying the request's method (GET for
// downloads, POST for uploads and confirms). A failed write is not survivable:
// it writes a generic 500 and reports false, and the caller must return.
func (h *backupArchiveHandler) auditOrFail(w http.ResponseWriter, req *http.Request, auditPath, target, reason string, status int) bool {
	if err := h.auditor.WriteSync(req.Context(), req.Method, auditPath, target, reason, status); err != nil {
		httperr.WriteCode(w, req, http.StatusInternalServerError, errors.New("audit write failed"))
		return false
	}
	audit.MarkRecorded(req.Context())
	return true
}

// correct appends the real outcome after the intent row. It runs on a
// context detached from the request, so a disconnect cannot drop it.
func (h *backupArchiveHandler) correct(req *http.Request, auditPath, target, reason string, status int) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(req.Context()), backupArchiveCorrectionTimeout)
	defer cancel()
	if err := h.auditor.WriteSync(ctx, req.Method, auditPath, target, reason, status); err != nil {
		slog.Error("backup download: audit correction failed", "path", auditPath, "target", target, "reason", reason, "err", err)
	}
}

// backupArchiveContentType maps a validated format to its media type.
func backupArchiveContentType(format string) string {
	if format == backupArchiveFormatZip {
		return "application/zip"
	}
	return "application/gzip"
}

// backupUpstreamStatus maps a transport failure to 504 for a timeout and 502 otherwise.
func backupUpstreamStatus(err error) int {
	var nerr net.Error
	if errors.As(err, &nerr) && nerr.Timeout() {
		return http.StatusGatewayTimeout
	}
	return http.StatusBadGateway
}

// flushingWriter flushes after every write so the client receives bytes as
// they are produced.
type flushingWriter struct {
	w  http.ResponseWriter
	rc *http.ResponseController
}

func (f flushingWriter) Write(p []byte) (int, error) {
	n, err := f.w.Write(p)
	if err == nil {
		// Flush is best effort: a writer without flush support still delivers the bytes.
		_ = f.rc.Flush()
	}
	return n, err
}
