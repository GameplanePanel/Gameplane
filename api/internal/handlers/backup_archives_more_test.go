package handlers

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	clienttesting "k8s.io/client-go/testing"

	"github.com/GameplanePanel/gameplane/api/internal/audit"
	"github.com/GameplanePanel/gameplane/api/internal/db"
	"github.com/GameplanePanel/gameplane/api/internal/gatewayprotocol"
	"github.com/GameplanePanel/gameplane/api/internal/kube"
	"github.com/GameplanePanel/gameplane/api/internal/scope"
)

// backupMoreStore opens a file-backed store private to one test. The older
// handler tests share a named in-memory database, which parallel tests must not use.
func backupMoreStore(t *testing.T) *db.Store {
	t.Helper()
	store, err := db.Open(t.Context(), "sqlite", "file:"+filepath.Join(t.TempDir(), "audit.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.Migrate(t.Context()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return store
}

// backupMoreDropAudit makes every later audit write fail, so the audit-failure
// branches of the handlers run.
func backupMoreDropAudit(t *testing.T, store *db.Store) {
	t.Helper()
	if _, err := store.DB.ExecContext(t.Context(), `DROP TABLE audit_events`); err != nil {
		t.Fatalf("drop audit_events: %v", err)
	}
}

// backupMoreFail returns a reactor that fails the matched action with err.
func backupMoreFail(err error) clienttesting.ReactionFunc {
	return func(clienttesting.Action) (bool, runtime.Object, error) {
		return true, nil, err
	}
}

// backupMoreExportCreate plays the operator's part on a BackupExport create. It
// names the export, sets its UID, worker Service, phase and message, then runs
// onCreate. The object is then stored by the default tracker.
func backupMoreExportCreate(service, phase, message string, onCreate func()) clienttesting.ReactionFunc {
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
		_ = unstructured.SetNestedField(u.Object, service, "status", "serviceName")
		if phase != "" {
			_ = unstructured.SetNestedField(u.Object, phase, "status", "phase")
		}
		if message != "" {
			_ = unstructured.SetNestedField(u.Object, message, "status", "message")
		}
		if onCreate != nil {
			onCreate()
		}
		return false, nil, nil
	}
}

// backupMoreGameServer is GameServer "alpha". deleting adds a deletionTimestamp.
func backupMoreGameServer(deleting bool) *unstructured.Unstructured {
	meta := map[string]any{"name": "alpha", "namespace": scope.DefaultNamespace, "uid": "server-uid"}
	if deleting {
		meta["deletionTimestamp"] = "2026-01-02T03:04:05Z"
	}
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "gameplane.local/v1alpha1",
		"kind":       "GameServer",
		"metadata":   meta,
		"spec":       map[string]any{"templateRef": map[string]any{"name": "minecraft"}},
	}}
}

// backupMoreForServer is a Backup of the named GameServer in the given phase.
func backupMoreForServer(name, server, phase string) *unstructured.Unstructured {
	u := newBackupFixture(name, phase, "")
	_ = unstructured.SetNestedField(u.Object, server, "spec", "serverRef", "name")
	return u
}

// backupMoreRemoteClient is the remote cluster: a Succeeded Backup "b1", the
// given objects, and an export create that reports Ready.
func backupMoreRemoteClient(objs ...runtime.Object) *kube.Client {
	k := fakeBackupClient(append([]runtime.Object{newBackupFixture("b1", "Succeeded", "")}, objs...)...)
	k.Dynamic.(*dynamicfake.FakeDynamicClient).PrependReactor("create", "backupexports", backupMoreExportCreate("bk-worker", "Ready", "", nil))
	return k
}

// backupMoreRemoteHandler builds a handler whose "remote" cluster is remote.
// A nil gateway leaves the gateway unconfigured.
func backupMoreRemoteHandler(t *testing.T, store *db.Store, remote *kube.Client, gateway backupExportGateway) *backupArchiveHandler {
	t.Helper()
	reg := kube.NewRegistry(scope.DefaultCluster)
	reg.Set(scope.DefaultCluster, fakeBackupClient())
	reg.Set("remote", remote)
	h := newBackupArchiveHandler(reg, audit.New(store), BackupArchiveConfig{MaxConcurrentExports: 3})
	h.pollInterval = 2 * time.Millisecond
	h.gateway = gateway
	return h
}

// backupMoreWorker is a TLS stand-in for the export worker that answers with status and body.
func backupMoreWorker(t *testing.T, status int, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/gzip")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// backupMoreUseWorker points the handler's export client at srv.
func backupMoreUseWorker(h *backupArchiveHandler, srv *httptest.Server) {
	h.tlsClient = srv.Client()
	h.exportURL = func(_ backupExportHost, uid string) *url.URL {
		u, _ := url.Parse(srv.URL + "/export/" + uid)
		return u
	}
}

// backupMoreClosedServer returns the client and base URL of a TLS server that
// has already shut down, so dialling it fails without a timeout.
func backupMoreClosedServer(t *testing.T) (*http.Client, string) {
	t.Helper()
	srv := httptest.NewTLSServer(http.NotFoundHandler())
	client, base := srv.Client(), srv.URL
	srv.Close()
	return client, base
}

// backupMoreGateway stands in for the gateway's BackupExportDownload. err wins;
// otherwise it answers with status.
type backupMoreGateway struct {
	err    error
	status int
}

func (g backupMoreGateway) BackupExportDownload(_ context.Context, _ gatewayprotocol.BackupExportTarget, _ http.Header) (*http.Response, error) {
	if g.err != nil {
		return nil, g.err
	}
	return &http.Response{StatusCode: g.status, Body: io.NopCloser(strings.NewReader(""))}, nil
}

// backupMoreTimeoutErr is a transport error that reports a timeout.
type backupMoreTimeoutErr struct{}

func (backupMoreTimeoutErr) Error() string   { return "i/o timeout" }
func (backupMoreTimeoutErr) Timeout() bool   { return true }
func (backupMoreTimeoutErr) Temporary() bool { return true }

// backupMoreRoundTrip lets a test supply the transport of an http.Client.
type backupMoreRoundTrip func(*http.Request) (*http.Response, error)

func (f backupMoreRoundTrip) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func TestBackupDownloadUnknownClusterIsRefusedWithoutAudit(t *testing.T) {
	t.Parallel()
	store := backupMoreStore(t)
	h := newBackupTestHandler(t, store, fakeBackupClient(newBackupFixture("b1", "Succeeded", "")), 3)
	rr := doProblem(t, newBackupTestRouter(h), "/backups/b1:download?cluster=nope")
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status %d, want 400; body=%s", rr.Code, rr.Body)
	}
	if rows := readBackupAuditRows(t, store); len(rows) != 0 {
		t.Fatalf("audit rows %+v, want none for an unknown cluster", rows)
	}
}

func TestBackupDownloadForbiddenNamespaceIs403WithoutAudit(t *testing.T) {
	t.Parallel()
	store := backupMoreStore(t)
	h := newBackupTestHandler(t, store, fakeBackupClient(newBackupFixture("b1", "Succeeded", "")), 3)
	rr := doProblem(t, newBackupTestRouter(h), "/backups/b1:download?namespace=elsewhere")
	if rr.Code != http.StatusForbidden {
		t.Fatalf("status %d, want 403; body=%s", rr.Code, rr.Body)
	}
	if rows := readBackupAuditRows(t, store); len(rows) != 0 {
		t.Fatalf("audit rows %+v, want none for a forbidden namespace", rows)
	}
}

func TestBackupDownloadBackupLookupErrorIs500Audited(t *testing.T) {
	t.Parallel()
	store := backupMoreStore(t)
	k := fakeBackupClient(newBackupFixture("b1", "Succeeded", ""))
	k.Dynamic.(*dynamicfake.FakeDynamicClient).PrependReactor("get", "backups", backupMoreFail(errors.New("apiserver down")))
	h := newBackupTestHandler(t, store, k, 3)
	rr := do(t, newBackupTestRouter(h), http.MethodGet, "/backups/b1:download", nil)
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("status %d, want 500; body=%s", rr.Code, rr.Body)
	}
	rows := readBackupAuditRows(t, store)
	if len(rows) != 1 || rows[0].Status != http.StatusInternalServerError {
		t.Fatalf("audit rows %+v, want one 500 refusal", rows)
	}
}

func TestBackupDownloadBackupWithBadStatusIs500(t *testing.T) {
	t.Parallel()
	store := backupMoreStore(t)
	backup := newBackupFixture("b1", "Succeeded", "")
	// A non-string phase cannot be converted into the typed Backup.
	backup.Object["status"].(map[string]any)["phase"] = int64(7)
	h := newBackupTestHandler(t, store, fakeBackupClient(backup), 3)
	rr := do(t, newBackupTestRouter(h), http.MethodGet, "/backups/b1:download", nil)
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("status %d, want 500; body=%s", rr.Code, rr.Body)
	}
}

func TestBackupDownloadCountExportsErrorIs500(t *testing.T) {
	t.Parallel()
	store := backupMoreStore(t)
	k := fakeBackupClient(newBackupFixture("b1", "Succeeded", ""))
	k.Dynamic.(*dynamicfake.FakeDynamicClient).PrependReactor("list", "backupexports", backupMoreFail(errors.New("apiserver down")))
	h := newBackupTestHandler(t, store, k, 3)
	rr := do(t, newBackupTestRouter(h), http.MethodGet, "/backups/b1:download", nil)
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("status %d, want 500; body=%s", rr.Code, rr.Body)
	}
}

func TestBackupDownloadAuditFailureStopsBeforeExportIs500(t *testing.T) {
	t.Parallel()
	store := backupMoreStore(t)
	k := fakeBackupClient(newBackupFixture("b1", "Succeeded", ""))
	k.Dynamic.(*dynamicfake.FakeDynamicClient).PrependReactor("create", "backupexports", backupMoreExportCreate("bk-worker", "Ready", "", nil))
	h := newBackupTestHandler(t, store, k, 3)
	backupMoreDropAudit(t, store)
	rr := do(t, newBackupTestRouter(h), http.MethodGet, "/backups/b1:download", nil)
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("status %d, want 500; body=%s", rr.Code, rr.Body)
	}
	active, err := k.CountActiveBackupExports(t.Context(), scope.DefaultNamespace)
	if err != nil || active != 0 {
		t.Fatalf("active exports %d (err %v), want none created", active, err)
	}
}

func TestBackupDownloadRefusalAuditFailureIs500(t *testing.T) {
	t.Parallel()
	store := backupMoreStore(t)
	h := newBackupTestHandler(t, store, fakeBackupClient(newBackupFixture("b1", "Succeeded", "")), 3)
	backupMoreDropAudit(t, store)
	rr := do(t, newBackupTestRouter(h), http.MethodGet, "/backups/b1:download?format=rar", nil)
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("status %d, want 500 instead of the 400 refusal; body=%s", rr.Code, rr.Body)
	}
}

func TestBackupDownloadNotReadyAuditFailureIs500(t *testing.T) {
	t.Parallel()
	store := backupMoreStore(t)
	h := newBackupTestHandler(t, store, fakeBackupClient(newBackupFixture("b1", "Running", "")), 3)
	backupMoreDropAudit(t, store)
	rr := do(t, newBackupTestRouter(h), http.MethodGet, "/backups/b1:download", nil)
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("status %d, want 500 instead of the 409 refusal; body=%s", rr.Code, rr.Body)
	}
}

func TestBackupDownloadCreateExportErrorIs500Audited(t *testing.T) {
	t.Parallel()
	store := backupMoreStore(t)
	k := fakeBackupClient(newBackupFixture("b1", "Succeeded", ""))
	k.Dynamic.(*dynamicfake.FakeDynamicClient).PrependReactor("create", "backupexports", backupMoreFail(errors.New("quota exceeded")))
	h := newBackupTestHandler(t, store, k, 3)
	rr := do(t, newBackupTestRouter(h), http.MethodGet, "/backups/b1:download", nil)
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("status %d, want 500; body=%s", rr.Code, rr.Body)
	}
	rows := readBackupAuditRows(t, store)
	if len(rows) != 2 || rows[0].Status != http.StatusOK || rows[1].Status != http.StatusInternalServerError {
		t.Fatalf("audit rows %+v, want intent 200 then correction 500", rows)
	}
}

func TestBackupDownloadExportLookupErrorIs500Audited(t *testing.T) {
	t.Parallel()
	store := backupMoreStore(t)
	k := fakeBackupClient(newBackupFixture("b1", "Succeeded", ""))
	dyn := k.Dynamic.(*dynamicfake.FakeDynamicClient)
	dyn.PrependReactor("create", "backupexports", backupMoreExportCreate("bk-worker", "Pending", "", nil))
	dyn.PrependReactor("get", "backupexports", backupMoreFail(errors.New("apiserver down")))
	h := newBackupTestHandler(t, store, k, 3)
	rr := do(t, newBackupTestRouter(h), http.MethodGet, "/backups/b1:download", nil)
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("status %d, want 500; body=%s", rr.Code, rr.Body)
	}
	rows := readBackupAuditRows(t, store)
	if len(rows) != 2 || rows[1].Status != http.StatusInternalServerError {
		t.Fatalf("audit rows %+v, want intent then 500 correction", rows)
	}
}

func TestBackupDownloadClientGoneWhileStartingIs499(t *testing.T) {
	t.Parallel()
	store := backupMoreStore(t)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	k := fakeBackupClient(newBackupFixture("b1", "Succeeded", ""))
	// The export is created with no phase yet, and the client leaves at once.
	k.Dynamic.(*dynamicfake.FakeDynamicClient).PrependReactor("create", "backupexports", backupMoreExportCreate("bk-worker", "", "", cancel))
	h := newBackupTestHandler(t, store, k, 3)
	h.startTimeout = time.Minute
	rr := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(ctx, http.MethodGet, "/backups/b1:download", nil)
	newBackupTestRouter(h).ServeHTTP(rr, req)
	rows := readBackupAuditRows(t, store)
	if len(rows) != 2 || rows[1].Status != 499 {
		t.Fatalf("audit rows %+v, want intent then 499 correction", rows)
	}
	if exp, err := k.GetBackupExport(t.Context(), scope.DefaultNamespace, "b1-export-fixed"); err != nil || exp != nil {
		t.Fatalf("export after disconnect: exp=%v err=%v, want deleted", exp, err)
	}
}

func TestBackupDownloadWorkerURLBuildFailureIs500(t *testing.T) {
	t.Parallel()
	store := backupMoreStore(t)
	k := fakeBackupClient(newBackupFixture("b1", "Succeeded", ""))
	k.Dynamic.(*dynamicfake.FakeDynamicClient).PrependReactor("create", "backupexports", backupMoreExportCreate("bk-worker", "Ready", "", nil))
	h := newBackupTestHandler(t, store, k, 3)
	h.tlsClient = &http.Client{}
	// A scheme that starts with a digit makes the request URL unparseable.
	h.exportURL = func(_ backupExportHost, _ string) *url.URL {
		return &url.URL{Scheme: "1bad", Host: "worker"}
	}
	rr := do(t, newBackupTestRouter(h), http.MethodGet, "/backups/b1:download", nil)
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("status %d, want 500; body=%s", rr.Code, rr.Body)
	}
	rows := readBackupAuditRows(t, store)
	if len(rows) != 2 || rows[1].Status != http.StatusInternalServerError {
		t.Fatalf("audit rows %+v, want intent then 500 correction", rows)
	}
}

func TestBackupDownloadWorkerUnreachableIs502(t *testing.T) {
	t.Parallel()
	store := backupMoreStore(t)
	k := fakeBackupClient(newBackupFixture("b1", "Succeeded", ""))
	k.Dynamic.(*dynamicfake.FakeDynamicClient).PrependReactor("create", "backupexports", backupMoreExportCreate("bk-worker", "Ready", "", nil))
	client, base := backupMoreClosedServer(t)
	h := newBackupTestHandler(t, store, k, 3)
	h.tlsClient = client
	h.exportURL = func(_ backupExportHost, uid string) *url.URL {
		u, _ := url.Parse(base + "/export/" + uid)
		return u
	}
	rr := do(t, newBackupTestRouter(h), http.MethodGet, "/backups/b1:download", nil)
	if rr.Code != http.StatusBadGateway {
		t.Fatalf("status %d, want 502; body=%s", rr.Code, rr.Body)
	}
	rows := readBackupAuditRows(t, store)
	if len(rows) != 2 || rows[1].Status != http.StatusBadGateway {
		t.Fatalf("audit rows %+v, want intent then 502 correction", rows)
	}
}

func TestBackupDownloadWorkerTimeoutIs504(t *testing.T) {
	t.Parallel()
	store := backupMoreStore(t)
	k := fakeBackupClient(newBackupFixture("b1", "Succeeded", ""))
	k.Dynamic.(*dynamicfake.FakeDynamicClient).PrependReactor("create", "backupexports", backupMoreExportCreate("bk-worker", "Ready", "", nil))
	h := newBackupTestHandler(t, store, k, 3)
	h.tlsClient = &http.Client{Transport: backupMoreRoundTrip(func(*http.Request) (*http.Response, error) {
		return nil, backupMoreTimeoutErr{}
	})}
	rr := do(t, newBackupTestRouter(h), http.MethodGet, "/backups/b1:download", nil)
	if rr.Code != http.StatusGatewayTimeout {
		t.Fatalf("status %d, want 504; body=%s", rr.Code, rr.Body)
	}
}

func TestBackupDownloadWorkerRefusalIs502(t *testing.T) {
	t.Parallel()
	store := backupMoreStore(t)
	k := fakeBackupClient(newBackupFixture("b1", "Succeeded", ""))
	k.Dynamic.(*dynamicfake.FakeDynamicClient).PrependReactor("create", "backupexports", backupMoreExportCreate("bk-worker", "Ready", "", nil))
	h := newBackupTestHandler(t, store, k, 3)
	backupMoreUseWorker(h, backupMoreWorker(t, http.StatusInternalServerError, "boom"))
	rr := do(t, newBackupTestRouter(h), http.MethodGet, "/backups/b1:download", nil)
	if rr.Code != http.StatusBadGateway {
		t.Fatalf("status %d, want 502; body=%s", rr.Code, rr.Body)
	}
	rows := readBackupAuditRows(t, store)
	if len(rows) != 2 || rows[1].Status != http.StatusBadGateway {
		t.Fatalf("audit rows %+v, want intent then 502 correction", rows)
	}
}

func TestBackupDownloadWithoutCompletionTimeStillStreams(t *testing.T) {
	t.Parallel()
	store := backupMoreStore(t)
	backup := newBackupFixture("b1", "Succeeded", "")
	unstructured.RemoveNestedField(backup.Object, "status", "completionTime")
	k := fakeBackupClient(backup)
	k.Dynamic.(*dynamicfake.FakeDynamicClient).PrependReactor("create", "backupexports", backupMoreExportCreate("bk-worker", "Ready", "", nil))
	h := newBackupTestHandler(t, store, k, 3)
	backupMoreUseWorker(h, backupMoreWorker(t, http.StatusOK, "ARCHIVE"))
	rr := do(t, newBackupTestRouter(h), http.MethodGet, "/backups/b1:download?format=tar.gz", nil)
	if rr.Code != http.StatusOK || rr.Body.String() != "ARCHIVE" {
		t.Fatalf("status %d body %q; want 200 ARCHIVE", rr.Code, rr.Body)
	}
	// Without a completion time the file name falls back to the current time.
	cd := rr.Header().Get("Content-Disposition")
	if !strings.HasPrefix(cd, `attachment; filename="alpha-`) || !strings.HasSuffix(cd, `.tar.gz"`) {
		t.Fatalf("Content-Disposition %q", cd)
	}
}

func TestBackupDownloadDeleteFailureStillStreams(t *testing.T) {
	t.Parallel()
	store := backupMoreStore(t)
	k := fakeBackupClient(newBackupFixture("b1", "Succeeded", ""))
	dyn := k.Dynamic.(*dynamicfake.FakeDynamicClient)
	dyn.PrependReactor("create", "backupexports", backupMoreExportCreate("bk-worker", "Ready", "", nil))
	dyn.PrependReactor("delete", "backupexports", backupMoreFail(errors.New("apiserver down")))
	h := newBackupTestHandler(t, store, k, 3)
	backupMoreUseWorker(h, backupMoreWorker(t, http.StatusOK, "ARCHIVE"))
	rr := do(t, newBackupTestRouter(h), http.MethodGet, "/backups/b1:download", nil)
	if rr.Code != http.StatusOK || rr.Body.String() != "ARCHIVE" {
		t.Fatalf("status %d body %q; a failed cleanup must not fail the download", rr.Code, rr.Body)
	}
}

func TestBackupDownloadRemoteWithoutGatewayIs503(t *testing.T) {
	t.Parallel()
	store := backupMoreStore(t)
	remote := backupMoreRemoteClient(backupMoreGameServer(false))
	h := backupMoreRemoteHandler(t, store, remote, nil)
	rr := do(t, newBackupTestRouter(h), http.MethodGet, "/backups/b1:download?cluster=remote", nil)
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("status %d, want 503; body=%s", rr.Code, rr.Body)
	}
	rows := readBackupAuditRows(t, store)
	if len(rows) != 2 || rows[1].Status != http.StatusServiceUnavailable {
		t.Fatalf("audit rows %+v, want intent then 503 correction", rows)
	}
}

func TestBackupDownloadRemoteGameServerMissingIs404(t *testing.T) {
	t.Parallel()
	store := backupMoreStore(t)
	remote := backupMoreRemoteClient()
	h := backupMoreRemoteHandler(t, store, remote, backupMoreGateway{status: http.StatusOK})
	rr := do(t, newBackupTestRouter(h), http.MethodGet, "/backups/b1:download?cluster=remote", nil)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status %d, want 404; body=%s", rr.Code, rr.Body)
	}
	rows := readBackupAuditRows(t, store)
	if len(rows) != 2 || rows[1].Status != http.StatusNotFound {
		t.Fatalf("audit rows %+v, want intent then 404 correction", rows)
	}
}

func TestBackupDownloadRemoteGameServerLookupErrorIs500(t *testing.T) {
	t.Parallel()
	store := backupMoreStore(t)
	remote := backupMoreRemoteClient(backupMoreGameServer(false))
	remote.Dynamic.(*dynamicfake.FakeDynamicClient).PrependReactor("get", "gameservers", backupMoreFail(errors.New("apiserver down")))
	h := backupMoreRemoteHandler(t, store, remote, backupMoreGateway{status: http.StatusOK})
	rr := do(t, newBackupTestRouter(h), http.MethodGet, "/backups/b1:download?cluster=remote", nil)
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("status %d, want 500; body=%s", rr.Code, rr.Body)
	}
}

func TestBackupDownloadRemoteGameServerDeletingIs404(t *testing.T) {
	t.Parallel()
	store := backupMoreStore(t)
	remote := backupMoreRemoteClient(backupMoreGameServer(true))
	h := backupMoreRemoteHandler(t, store, remote, backupMoreGateway{status: http.StatusOK})
	rr := do(t, newBackupTestRouter(h), http.MethodGet, "/backups/b1:download?cluster=remote", nil)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status %d, want 404; body=%s", rr.Code, rr.Body)
	}
}

func TestBackupDownloadRemoteGatewayFailuresMapStatus(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		gateway backupMoreGateway
		want    int
	}{
		{name: "gateway error", gateway: backupMoreGateway{err: errors.New("tunnel down")}, want: http.StatusBadGateway},
		{name: "gateway timeout", gateway: backupMoreGateway{err: backupMoreTimeoutErr{}}, want: http.StatusGatewayTimeout},
		{name: "worker refused", gateway: backupMoreGateway{status: http.StatusForbidden}, want: http.StatusBadGateway},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			store := backupMoreStore(t)
			remote := backupMoreRemoteClient(backupMoreGameServer(false))
			h := backupMoreRemoteHandler(t, store, remote, tc.gateway)
			rr := do(t, newBackupTestRouter(h), http.MethodGet, "/backups/b1:download?cluster=remote", nil)
			if rr.Code != tc.want {
				t.Fatalf("status %d, want %d; body=%s", rr.Code, tc.want, rr.Body)
			}
			rows := readBackupAuditRows(t, store)
			if len(rows) != 2 || rows[1].Status != tc.want {
				t.Fatalf("audit rows %+v, want correction %d", rows, tc.want)
			}
		})
	}
}

func TestNewBackupExportHostValidatesLabels(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		service string
		ns      string
		want    backupExportHost
		ok      bool
	}{
		{name: "valid", service: "svc", ns: "games", want: "svc.games.svc.cluster.local:9443", ok: true},
		{name: "uppercase service", service: "Bad", ns: "games"},
		{name: "underscore namespace", service: "svc", ns: "bad_ns"},
		{name: "empty service", service: "", ns: "games"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, ok := newBackupExportHost(tc.service, tc.ns)
			if ok != tc.ok || got != tc.want {
				t.Fatalf("newBackupExportHost(%q, %q) = %q, %v; want %q, %v", tc.service, tc.ns, got, ok, tc.want, tc.ok)
			}
		})
	}
}

func TestBackupUpstreamStatusMapsTimeouts(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		err  error
		want int
	}{
		{name: "refused", err: errors.New("connection refused"), want: http.StatusBadGateway},
		{name: "timeout", err: backupMoreTimeoutErr{}, want: http.StatusGatewayTimeout},
		{name: "wrapped timeout", err: fmt.Errorf("dial: %w", backupMoreTimeoutErr{}), want: http.StatusGatewayTimeout},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := backupUpstreamStatus(tc.err); got != tc.want {
				t.Fatalf("backupUpstreamStatus = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestNewBackupArchiveHandlerWiresOptionalClients(t *testing.T) {
	t.Parallel()
	store := backupMoreStore(t)
	reg := kube.NewRegistry(scope.DefaultCluster)
	h := newBackupArchiveHandler(reg, audit.New(store), BackupArchiveConfig{
		GatewayNamespace:     "gateway",
		MaxConcurrentExports: 0,
		AgentCABundle:        filepath.Join(t.TempDir(), "missing-ca.pem"),
		AgentClientCert:      "client.pem",
		AgentClientKey:       "client-key.pem",
	})
	if h.cfg.MaxConcurrentExports != backupArchiveDefaultMaxExports {
		t.Fatalf("MaxConcurrentExports %d, want default %d", h.cfg.MaxConcurrentExports, backupArchiveDefaultMaxExports)
	}
	if h.gateway == nil || h.uploads == nil {
		t.Fatal("gateway namespace set but the gateway clients were not wired")
	}
	if h.tlsClient != nil {
		t.Fatal("tlsClient set although the CA bundle cannot be read")
	}
}

func TestMountBackupArchivesRegistersRoutes(t *testing.T) {
	t.Parallel()
	store := backupMoreStore(t)
	reg := kube.NewRegistry(scope.DefaultCluster)
	reg.Set(scope.DefaultCluster, fakeBackupClient())
	r := chi.NewRouter()
	MountBackupArchives(r, reg, audit.New(store), BackupArchiveConfig{MaxConcurrentExports: 3})

	// The download route answers a missing Backup with the handler's 404.
	if rr := doProblem(t, r, "/backups/missing:download"); rr.Code != http.StatusNotFound {
		t.Fatalf("download route status %d, want 404; body=%s", rr.Code, rr.Body)
	}
	// The upload route refuses a body with no length with the handler's 411.
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/restores:upload?server=alpha", nil))
	if rr.Code != http.StatusLengthRequired {
		t.Fatalf("upload route status %d, want 411; body=%s", rr.Code, rr.Body)
	}
	// The confirm route rejects an empty body with the handler's 400.
	rr = httptest.NewRecorder()
	r.ServeHTTP(rr, httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/restores/x:confirm", nil))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("confirm route status %d, want 400; body=%s", rr.Code, rr.Body)
	}
}

func TestDefaultBackupURLs(t *testing.T) {
	t.Parallel()
	host := backupExportHost("svc.ns.svc.cluster.local:9443")
	if got := defaultBackupExportURL(host, "uid-1").String(); got != "https://svc.ns.svc.cluster.local:9443/export/uid-1" {
		t.Fatalf("export URL %q", got)
	}
	if got := defaultRestoreUploadURL(host, "uid-2").String(); got != "https://svc.ns.svc.cluster.local:9443/upload/uid-2" {
		t.Fatalf("upload URL %q", got)
	}
}
