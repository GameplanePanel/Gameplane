package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	kubefake "k8s.io/client-go/kubernetes/fake"
	clienttesting "k8s.io/client-go/testing"

	"github.com/GameplanePanel/gameplane/api/internal/audit"
	"github.com/GameplanePanel/gameplane/api/internal/db"
	"github.com/GameplanePanel/gameplane/api/internal/gatewayprotocol"
	"github.com/GameplanePanel/gameplane/api/internal/kube"
	"github.com/GameplanePanel/gameplane/api/internal/scope"
)

const testBackupExportUID = "11111111-2222-3333-4444-555555555555"

// fakeBackupExportGateway stands in for the cluster gateway on remote clusters.
type fakeBackupExportGateway struct {
	target *gatewayprotocol.BackupExportTarget
	body   string
}

func (f *fakeBackupExportGateway) BackupExportDownload(_ context.Context, target gatewayprotocol.BackupExportTarget, _ http.Header) (*http.Response, error) {
	f.target = &target
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": {"application/gzip"}},
		Body:       io.NopCloser(strings.NewReader(f.body)),
	}, nil
}

// fakeBackupClient builds a kube.Client whose dynamic client knows Backups,
// BackupExports and GameServers.
func fakeBackupClient(objs ...runtime.Object) *kube.Client {
	scm := runtime.NewScheme()
	gvkr := map[schema.GroupVersionResource]string{
		kube.GVRBackup:       "BackupList",
		kube.GVRBackupExport: "BackupExportList",
		kube.GVRs["servers"]: "GameServerList",
	}
	dyn := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(scm, gvkr, objs...)
	return &kube.Client{Dynamic: dyn, Typed: kubefake.NewClientset()}
}

// newBackupFixture builds a Backup for server "alpha" with the given phase and strategy.
func newBackupFixture(name, phase, strategy string) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "gameplane.local/v1alpha1",
		"kind":       "Backup",
		"metadata":   map[string]any{"name": name, "namespace": scope.DefaultNamespace, "uid": "backup-uid-1"},
		"spec":       map[string]any{"serverRef": map[string]any{"name": "alpha"}, "strategy": strategy},
		"status": map[string]any{
			"phase":          phase,
			"size":           "1234",
			"completionTime": "2026-01-02T03:04:05Z",
		},
	}}
}

// newExportFixture builds a BackupExport in the given phase (empty means not yet reconciled).
func newExportFixture(name, phase string) *unstructured.Unstructured {
	status := map[string]any{}
	if phase != "" {
		status["phase"] = phase
	}
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "gameplane.local/v1alpha1",
		"kind":       "BackupExport",
		"metadata":   map[string]any{"name": name, "namespace": scope.DefaultNamespace},
		"spec":       map[string]any{"backupRef": map[string]any{"name": "b1"}, "backupUID": "backup-uid-1", "format": "tar.gz"},
		"status":     status,
	}}
}

// exportReactor plays the operator's part on create: it assigns the name from
// generateName and a UID, sets the worker Service, and sets the phase and
// message. The object is then stored by the default tracker.
func exportReactor(phase, message string) clienttesting.ReactionFunc {
	return func(action clienttesting.Action) (bool, runtime.Object, error) {
		create, ok := action.(clienttesting.CreateAction)
		if !ok {
			return false, nil, nil
		}
		u, ok := create.GetObject().(*unstructured.Unstructured)
		if !ok {
			return false, nil, nil
		}
		if u.GetName() == "" {
			u.SetName(u.GetGenerateName() + "fixed")
		}
		u.SetUID(types.UID(testBackupExportUID))
		_ = unstructured.SetNestedField(u.Object, "bk-worker", "status", "serviceName")
		if phase != "" {
			_ = unstructured.SetNestedField(u.Object, phase, "status", "phase")
		}
		if message != "" {
			_ = unstructured.SetNestedField(u.Object, message, "status", "message")
		}
		return false, nil, nil
	}
}

func newBackupTestHandler(t *testing.T, store *db.Store, k *kube.Client, maxExports int) *backupArchiveHandler {
	t.Helper()
	reg := kube.NewRegistry(scope.DefaultCluster)
	reg.Set(scope.DefaultCluster, k)
	h := newBackupArchiveHandler(reg, audit.New(store), BackupArchiveConfig{MaxConcurrentExports: maxExports})
	h.pollInterval = 2 * time.Millisecond
	h.startTimeout = 2 * time.Second
	return h
}

func newBackupTestRouter(h *backupArchiveHandler) *chi.Mux {
	r := chi.NewRouter()
	r.Get("/backups/{name}:download", h.download)
	return r
}

type backupAuditRow struct {
	Path, Target string
	Status       int
}

func readBackupAuditRows(t *testing.T, store *db.Store) []backupAuditRow {
	t.Helper()
	rows, err := store.DB.QueryContext(t.Context(), `SELECT path, target, status FROM audit_events ORDER BY id`)
	if err != nil {
		t.Fatalf("query audit rows: %v", err)
	}
	defer func() { _ = rows.Close() }()
	var out []backupAuditRow
	for rows.Next() {
		var r backupAuditRow
		if err := rows.Scan(&r.Path, &r.Target, &r.Status); err != nil {
			t.Fatalf("scan audit row: %v", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate audit rows: %v", err)
	}
	return out
}

func backupProblemID(t *testing.T, rr *httptest.ResponseRecorder) string {
	t.Helper()
	var p struct {
		MessageID string `json:"messageId"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &p); err != nil {
		t.Fatalf("decode problem %q: %v", rr.Body.String(), err)
	}
	return p.MessageID
}

// doProblem sends a GET that asks for application/problem+json, so the
// refusal body carries the registry messageId the assertions read.
func doProblem(t *testing.T, h http.Handler, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, path, nil)
	req.Header.Set("Accept", "application/problem+json")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

func expectBackupRefusal(t *testing.T, rr *httptest.ResponseRecorder, status int, id string) {
	t.Helper()
	if rr.Code != status || backupProblemID(t, rr) != id {
		t.Fatalf("status %d id %q, want %d %s; body=%s", rr.Code, backupProblemID(t, rr), status, id, rr.Body)
	}
}

func TestBackupDownloadRejectsUnknownFormat(t *testing.T) {
	store := newTestStore(t)
	k := fakeBackupClient(newBackupFixture("b1", "Succeeded", ""))
	h := newBackupTestHandler(t, store, k, 3)
	rr := do(t, newBackupTestRouter(h), http.MethodGet, "/backups/b1:download?format=rar", nil)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status %d, want 400; body=%s", rr.Code, rr.Body)
	}
	rows := readBackupAuditRows(t, store)
	if len(rows) != 1 || rows[0].Target != "b1:rar" || rows[0].Status != http.StatusBadRequest {
		t.Fatalf("audit rows %+v", rows)
	}
}

func TestBackupDownloadMissingBackupIs404(t *testing.T) {
	store := newTestStore(t)
	h := newBackupTestHandler(t, store, fakeBackupClient(), 3)
	rr := do(t, newBackupTestRouter(h), http.MethodGet, "/backups/nope:download", nil)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status %d, want 404; body=%s", rr.Code, rr.Body)
	}
}

func TestBackupDownloadNotFinishedIs409(t *testing.T) {
	store := newTestStore(t)
	h := newBackupTestHandler(t, store, fakeBackupClient(newBackupFixture("b1", "Running", "")), 3)
	rr := doProblem(t, newBackupTestRouter(h), "/backups/b1:download")
	expectBackupRefusal(t, rr, http.StatusConflict, "server.backup.download.not_ready")
}

func TestBackupDownloadSnapshotStrategyIs400(t *testing.T) {
	store := newTestStore(t)
	h := newBackupTestHandler(t, store, fakeBackupClient(newBackupFixture("b1", "Succeeded", "volume-snapshot")), 3)
	rr := doProblem(t, newBackupTestRouter(h), "/backups/b1:download")
	expectBackupRefusal(t, rr, http.StatusBadRequest, "server.backup.download.snapshot_strategy")
}

func TestBackupDownloadBusyIs429WhenCapReached(t *testing.T) {
	store := newTestStore(t)
	k := fakeBackupClient(
		newBackupFixture("b1", "Succeeded", ""),
		newExportFixture("e1", "Pending"),
		newExportFixture("e2", ""),
		newExportFixture("e3", "Ready"),
	)
	h := newBackupTestHandler(t, store, k, 3)
	rr := doProblem(t, newBackupTestRouter(h), "/backups/b1:download")
	expectBackupRefusal(t, rr, http.StatusTooManyRequests, "server.backup.download.busy")
}

func TestBackupDownloadExportFailedIs502AndDeletesExport(t *testing.T) {
	store := newTestStore(t)
	k := fakeBackupClient(newBackupFixture("b1", "Succeeded", ""))
	k.Dynamic.(*dynamicfake.FakeDynamicClient).PrependReactor("create", "backupexports", exportReactor("Failed", "node pool exhausted"))
	h := newBackupTestHandler(t, store, k, 3)
	rr := doProblem(t, newBackupTestRouter(h), "/backups/b1:download")
	expectBackupRefusal(t, rr, http.StatusBadGateway, "server.backup.download.failed")
	if exp, err := k.GetBackupExport(t.Context(), scope.DefaultNamespace, "b1-export-fixed"); err != nil || exp != nil {
		t.Fatalf("export after failure: exp=%v err=%v, want deleted", exp, err)
	}
	rows := readBackupAuditRows(t, store)
	if len(rows) != 2 || rows[0].Status != http.StatusOK || rows[1].Status != http.StatusBadGateway {
		t.Fatalf("audit rows %+v, want intent 200 then correction 502", rows)
	}
}

func TestBackupDownloadStartTimeoutIs504AndDeletesExport(t *testing.T) {
	store := newTestStore(t)
	k := fakeBackupClient(newBackupFixture("b1", "Succeeded", ""))
	k.Dynamic.(*dynamicfake.FakeDynamicClient).PrependReactor("create", "backupexports", exportReactor("", ""))
	h := newBackupTestHandler(t, store, k, 3)
	h.startTimeout = 20 * time.Millisecond
	rr := doProblem(t, newBackupTestRouter(h), "/backups/b1:download")
	expectBackupRefusal(t, rr, http.StatusGatewayTimeout, "server.backup.download.start_timeout")
	if exp, err := k.GetBackupExport(t.Context(), scope.DefaultNamespace, "b1-export-fixed"); err != nil || exp != nil {
		t.Fatalf("export after timeout: exp=%v err=%v, want deleted", exp, err)
	}
}

func TestBackupDownloadLocalStreamsArchiveAndAudits(t *testing.T) {
	store := newTestStore(t)
	k := fakeBackupClient(newBackupFixture("b1", "Succeeded", ""))
	k.Dynamic.(*dynamicfake.FakeDynamicClient).PrependReactor("create", "backupexports", exportReactor("Ready", ""))

	var gotPath string
	worker := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/gzip")
		_, _ = w.Write([]byte("ARCHIVE"))
	}))
	t.Cleanup(worker.Close)

	h := newBackupTestHandler(t, store, k, 3)
	h.tlsClient = worker.Client()
	h.exportURL = func(_ backupExportHost, uid string) *url.URL {
		u, _ := url.Parse(worker.URL + "/export/" + uid)
		return u
	}

	rr := do(t, newBackupTestRouter(h), http.MethodGet, "/backups/b1:download?format=tar.gz", nil)
	if rr.Code != http.StatusOK || rr.Body.String() != "ARCHIVE" {
		t.Fatalf("status %d body %q; want 200 ARCHIVE", rr.Code, rr.Body)
	}
	if gotPath != "/export/"+testBackupExportUID {
		t.Fatalf("worker path %q", gotPath)
	}
	if got := rr.Header().Get("Content-Type"); got != "application/gzip" {
		t.Fatalf("Content-Type %q", got)
	}
	if got := rr.Header().Get("Content-Disposition"); got != `attachment; filename="alpha-20260102-030405.tar.gz"` {
		t.Fatalf("Content-Disposition %q", got)
	}
	if got := rr.Header().Get("X-Gameplane-Estimated-Bytes"); got != "1234" {
		t.Fatalf("X-Gameplane-Estimated-Bytes %q", got)
	}
	if got := rr.Header().Get("Content-Length"); got != "" {
		t.Fatalf("Content-Length must be unset, got %q", got)
	}
	if exp, err := k.GetBackupExport(t.Context(), scope.DefaultNamespace, "b1-export-fixed"); err != nil || exp != nil {
		t.Fatalf("export after success: exp=%v err=%v, want deleted", exp, err)
	}
	rows := readBackupAuditRows(t, store)
	if len(rows) != 1 || rows[0].Path != "/backups/b1:download" || rows[0].Target != "b1:tar.gz" || rows[0].Status != http.StatusOK {
		t.Fatalf("audit rows %+v, want one intent row", rows)
	}
}

func TestBackupDownloadMidStreamFailureAbortsAndCorrectsAudit(t *testing.T) {
	store := newTestStore(t)
	k := fakeBackupClient(newBackupFixture("b1", "Succeeded", ""))
	k.Dynamic.(*dynamicfake.FakeDynamicClient).PrependReactor("create", "backupexports", exportReactor("Ready", ""))

	// The worker declares 100 bytes, sends 10, and ends. The client read then fails.
	worker := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Length", "100")
		w.Header().Set("Content-Type", "application/gzip")
		_, _ = w.Write([]byte("0123456789"))
	}))
	t.Cleanup(worker.Close)

	h := newBackupTestHandler(t, store, k, 3)
	h.tlsClient = worker.Client()
	h.exportURL = func(_ backupExportHost, uid string) *url.URL {
		u, _ := url.Parse(worker.URL + "/export/" + uid)
		return u
	}

	rr := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/backups/b1:download", nil)
	func() {
		defer func() {
			rec := recover()
			if err, ok := rec.(error); !ok || !errors.Is(err, http.ErrAbortHandler) {
				t.Fatalf("recovered %v, want http.ErrAbortHandler", rec)
			}
		}()
		newBackupTestRouter(h).ServeHTTP(rr, req)
	}()
	rows := readBackupAuditRows(t, store)
	if len(rows) != 2 || rows[0].Status != http.StatusOK || rows[1].Status != http.StatusBadGateway {
		t.Fatalf("audit rows %+v, want intent 200 then correction 502", rows)
	}
}

func TestBackupDownloadRemoteRoutesThroughGateway(t *testing.T) {
	store := newTestStore(t)
	home := fakeBackupClient()
	remote := fakeBackupClient(
		newBackupFixture("b1", "Succeeded", ""),
		&unstructured.Unstructured{Object: map[string]any{
			"apiVersion": "gameplane.local/v1alpha1",
			"kind":       "GameServer",
			"metadata":   map[string]any{"name": "alpha", "namespace": scope.DefaultNamespace, "uid": "server-uid"},
			"spec":       map[string]any{"templateRef": map[string]any{"name": "minecraft"}},
		}},
	)
	remote.Dynamic.(*dynamicfake.FakeDynamicClient).PrependReactor("create", "backupexports", exportReactor("Ready", ""))

	reg := kube.NewRegistry(scope.DefaultCluster)
	reg.Set(scope.DefaultCluster, home)
	reg.Set("remote", remote)
	gateway := &fakeBackupExportGateway{body: "REMOTE"}
	h := newBackupArchiveHandler(reg, audit.New(store), BackupArchiveConfig{MaxConcurrentExports: 3})
	h.pollInterval = 2 * time.Millisecond
	h.gateway = gateway

	rr := do(t, newBackupTestRouter(h), http.MethodGet, "/backups/b1:download?format=zip&cluster=remote", nil)
	if rr.Code != http.StatusOK || rr.Body.String() != "REMOTE" {
		t.Fatalf("status %d body %q", rr.Code, rr.Body)
	}
	if got := rr.Header().Get("Content-Type"); got != "application/zip" {
		t.Fatalf("Content-Type %q, want application/zip", got)
	}
	want := gatewayprotocol.BackupExportTarget{
		Target:    gatewayprotocol.Target{Cluster: "remote", Namespace: scope.DefaultNamespace, Name: "alpha", UID: "server-uid"},
		Export:    "b1-export-fixed",
		ExportUID: testBackupExportUID,
	}
	if gateway.target == nil || *gateway.target != want {
		t.Fatalf("gateway target %+v, want %+v", gateway.target, want)
	}
}

func TestBackupDownloadStampsClusterAnnotationOnExport(t *testing.T) {
	store := newTestStore(t)
	home := fakeBackupClient()
	remote := fakeBackupClient(
		newBackupFixture("b1", "Succeeded", ""),
		&unstructured.Unstructured{Object: map[string]any{
			"apiVersion": "gameplane.local/v1alpha1",
			"kind":       "GameServer",
			"metadata":   map[string]any{"name": "alpha", "namespace": scope.DefaultNamespace, "uid": "server-uid"},
			"spec":       map[string]any{"templateRef": map[string]any{"name": "minecraft"}},
		}},
	)
	dyn := remote.Dynamic.(*dynamicfake.FakeDynamicClient)
	dyn.PrependReactor("create", "backupexports", exportReactor("Ready", ""))
	// Recorder: observes the object as it is created and does not consume the
	// action, so the default tracker still stores it.
	var stamped string
	var sawCreate bool
	dyn.PrependReactor("create", "backupexports", func(action clienttesting.Action) (bool, runtime.Object, error) {
		create, ok := action.(clienttesting.CreateAction)
		if !ok {
			return false, nil, nil
		}
		if u, ok := create.GetObject().(*unstructured.Unstructured); ok {
			sawCreate = true
			stamped = u.GetAnnotations()[kube.BackupExportClusterAnnotation]
		}
		return false, nil, nil
	})

	reg := kube.NewRegistry(scope.DefaultCluster)
	reg.Set(scope.DefaultCluster, home)
	reg.Set("remote", remote)
	h := newBackupArchiveHandler(reg, audit.New(store), BackupArchiveConfig{MaxConcurrentExports: 3})
	h.pollInterval = 2 * time.Millisecond
	h.gateway = &fakeBackupExportGateway{body: "REMOTE"}

	rr := do(t, newBackupTestRouter(h), http.MethodGet, "/backups/b1:download?format=tar.gz&cluster=remote", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d body %q", rr.Code, rr.Body)
	}
	if !sawCreate {
		t.Fatal("BackupExport was never created on the remote cluster")
	}
	if stamped != "remote" {
		t.Fatalf("cluster annotation = %q, want %q", stamped, "remote")
	}
}
