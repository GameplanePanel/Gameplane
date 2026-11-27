package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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

const (
	testRestoreUID  = "66666666-7777-8888-9999-000000000000"
	testRestoreName = "alpha-restore-fixed"
	testUploadBody  = "ARCHIVE-BYTES"
)

// fakeRestoreUploadGateway stands in for the cluster gateway on remote clusters.
type fakeRestoreUploadGateway struct {
	target        *gatewayprotocol.RestoreUploadTarget
	cluster       string
	body          string
	contentLength int64
	status        int
}

func (f *fakeRestoreUploadGateway) RestoreUploadStream(_ context.Context, cluster string, target gatewayprotocol.RestoreUploadTarget, body io.Reader, contentLength int64) (*http.Response, error) {
	f.target = &target
	f.cluster = cluster
	f.contentLength = contentLength
	got, err := io.ReadAll(body)
	if err != nil {
		return nil, err
	}
	f.body = string(got)
	status := f.status
	if status == 0 {
		status = http.StatusOK
	}
	return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(""))}, nil
}

// fakeRestoreClient builds a kube.Client whose dynamic client knows Backups,
// Restores, GameServers and BackupExports.
func fakeRestoreClient(objs ...runtime.Object) *kube.Client {
	scm := runtime.NewScheme()
	gvkr := map[schema.GroupVersionResource]string{
		kube.GVRBackup:       "BackupList",
		kube.GVRRestore:      "RestoreList",
		kube.GVRBackupExport: "BackupExportList",
		kube.GVRs["servers"]: "GameServerList",
	}
	dyn := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(scm, gvkr, objs...)
	return &kube.Client{Dynamic: dyn, Typed: kubefake.NewClientset()}
}

// newUploadServerFixture builds GameServer "alpha" with an optional annotation map.
func newUploadServerFixture(annotations map[string]any) *unstructured.Unstructured {
	meta := map[string]any{"name": "alpha", "namespace": scope.DefaultNamespace, "uid": "server-uid"}
	if annotations != nil {
		meta["annotations"] = annotations
	}
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "gameplane.local/v1alpha1",
		"kind":       "GameServer",
		"metadata":   meta,
		"spec":       map[string]any{"templateRef": map[string]any{"name": "minecraft"}},
	}}
}

// newUploadRestoreFixture builds an upload Restore for server "alpha".
func newUploadRestoreFixture(name, phase string, generation int64, confirmed bool) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "gameplane.local/v1alpha1",
		"kind":       "Restore",
		"metadata":   map[string]any{"name": name, "namespace": scope.DefaultNamespace, "uid": testRestoreUID, "generation": generation},
		"spec": map[string]any{
			"serverRef": map[string]any{"name": "alpha"},
			"upload":    map[string]any{"fileName": "a.tar.gz", "format": "tar.gz", "declaredBytes": int64(len(testUploadBody)), "confirmed": confirmed},
		},
		"status": map[string]any{"phase": phase},
	}}
}

// restoreCreateReactor plays the operator's part on create: it assigns the
// name from generateName and a UID, and sets the phase, receiver Service and
// failure message. onCreate, when set, runs after the object is stored.
func restoreCreateReactor(phase, receiver, message string, onCreate func()) clienttesting.ReactionFunc {
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
		u.SetUID(types.UID(testRestoreUID))
		if phase != "" {
			_ = unstructured.SetNestedField(u.Object, phase, "status", "phase")
		}
		if receiver != "" {
			_ = unstructured.SetNestedField(u.Object, receiver, "status", "upload", "receiverService")
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

func newUploadTestHandler(t *testing.T, store *db.Store, k *kube.Client) *backupArchiveHandler {
	t.Helper()
	reg := kube.NewRegistry(scope.DefaultCluster)
	reg.Set(scope.DefaultCluster, k)
	h := newBackupArchiveHandler(reg, audit.New(store), BackupArchiveConfig{MaxConcurrentExports: 3})
	h.pollInterval = 2 * time.Millisecond
	h.startTimeout = 2 * time.Second
	return h
}

func newUploadTestRouter(h *backupArchiveHandler) *chi.Mux {
	r := chi.NewRouter()
	r.Post("/restores:upload", h.upload)
	r.Post("/restores/{name}:confirm", h.confirm)
	return r
}

// uploadRequest builds a POST with a raw body. A non-negative contentLength
// overrides the length the body would advertise.
func uploadRequest(t *testing.T, path string, body []byte, contentLength int64) *http.Request {
	t.Helper()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, path, bytes.NewReader(body))
	req.Header.Set("Accept", "application/problem+json")
	if contentLength >= 0 {
		req.ContentLength = contentLength
	}
	return req
}

// doUpload sends an upload request with a valid archive body.
func doUpload(t *testing.T, h http.Handler, query string) *httptest.ResponseRecorder {
	t.Helper()
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, uploadRequest(t, "/restores:upload?"+query, []byte(testUploadBody), int64(len(testUploadBody))))
	return rr
}

const validUploadQuery = "server=alpha&format=tar.gz&fileName=a.tar.gz"

func uploadRestoreExists(t *testing.T, k *kube.Client, name string) bool {
	t.Helper()
	r, err := kube.GetRestore(t.Context(), k.Dynamic, scope.DefaultNamespace, name)
	if err != nil {
		t.Fatalf("get restore: %v", err)
	}
	return r != nil
}

func TestRestoreUploadRequiresContentLengthIs411(t *testing.T) {
	store := newTestStore(t)
	h := newUploadTestHandler(t, store, fakeRestoreClient(newUploadServerFixture(nil)))
	rr := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/restores:upload?"+validUploadQuery, nil)
	req.Header.Set("Accept", "application/problem+json")
	newUploadTestRouter(h).ServeHTTP(rr, req)
	expectBackupRefusal(t, rr, http.StatusLengthRequired, "server.restore.upload.length_required")
	rows := readBackupAuditRows(t, store)
	if len(rows) != 1 || rows[0].Path != "/restores:upload" || rows[0].Status != http.StatusLengthRequired {
		t.Fatalf("audit rows %+v", rows)
	}
}

func TestRestoreUploadOverMaxSizeIs413(t *testing.T) {
	store := newTestStore(t)
	h := newUploadTestHandler(t, store, fakeRestoreClient(newUploadServerFixture(nil)))
	h.cfg.MaxUploadBytes = 4
	rr := doUpload(t, newUploadTestRouter(h), validUploadQuery)
	expectBackupRefusal(t, rr, http.StatusRequestEntityTooLarge, "server.restore.upload.too_large")
}

func TestRestoreUploadRejectsBadQueryIs400(t *testing.T) {
	cases := map[string]string{
		"unknown format":      "server=alpha&format=rar&fileName=a.rar",
		"path separator":      "server=alpha&format=zip&fileName=..%2Fa.zip",
		"dot-dot file name":   "server=alpha&format=zip&fileName=..",
		"missing file name":   "server=alpha&format=zip",
		"missing server":      "format=zip&fileName=a.zip",
		"bad template name":   "server=alpha&format=zip&fileName=a.zip&template=Not_Valid",
		"over-long file name": "server=alpha&format=zip&fileName=" + strings.Repeat("a", 256),
	}
	for name, query := range cases {
		t.Run(name, func(t *testing.T) {
			store := newTestStore(t)
			h := newUploadTestHandler(t, store, fakeRestoreClient(newUploadServerFixture(nil)))
			rr := doUpload(t, newUploadTestRouter(h), query)
			expectBackupRefusal(t, rr, http.StatusBadRequest, "server.restore.upload.bad_request")
		})
	}
}

func TestRestoreUploadMissingServerIs404(t *testing.T) {
	store := newTestStore(t)
	h := newUploadTestHandler(t, store, fakeRestoreClient())
	rr := doUpload(t, newUploadTestRouter(h), validUploadQuery)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status %d, want 404; body=%s", rr.Code, rr.Body)
	}
}

func TestRestoreUploadBusyWhenBackupRunningIs409(t *testing.T) {
	store := newTestStore(t)
	k := fakeRestoreClient(newUploadServerFixture(nil), newBackupFixture("b1", "Running", ""))
	h := newUploadTestHandler(t, store, k)
	rr := doUpload(t, newUploadTestRouter(h), validUploadQuery)
	expectBackupRefusal(t, rr, http.StatusConflict, "server.restore.upload.server_busy")
}

func TestRestoreUploadBusyWhenRestoreActiveIs409(t *testing.T) {
	store := newTestStore(t)
	k := fakeRestoreClient(newUploadServerFixture(nil), newUploadRestoreFixture("other", "AwaitingConfirmation", 1, false))
	h := newUploadTestHandler(t, store, k)
	rr := doUpload(t, newUploadTestRouter(h), validUploadQuery)
	expectBackupRefusal(t, rr, http.StatusConflict, "server.restore.upload.server_busy")
}

func TestRestoreUploadBusyWhenWipeRunningIs409(t *testing.T) {
	store := newTestStore(t)
	k := fakeRestoreClient(newUploadServerFixture(map[string]any{
		wipeRequestedAnnotation: "1700000000",
	}))
	h := newUploadTestHandler(t, store, k)
	rr := doUpload(t, newUploadTestRouter(h), validUploadQuery)
	expectBackupRefusal(t, rr, http.StatusConflict, "server.restore.upload.server_busy")
}

func TestRestoreUploadFinishedRestoreDoesNotBlockIs202(t *testing.T) {
	store := newTestStore(t)
	worker := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(worker.Close)
	k := fakeRestoreClient(newUploadServerFixture(nil), newUploadRestoreFixture("old", "Succeeded", 1, true))
	k.Dynamic.(*dynamicfake.FakeDynamicClient).PrependReactor("create", "restores", restoreCreateReactor("Receiving", "rx-alpha", "", nil))
	h := newUploadTestHandler(t, store, k)
	h.tlsClient = worker.Client()
	h.uploadURL = func(_ backupExportHost, uid string) *url.URL {
		u, _ := url.Parse(worker.URL + "/upload/" + uid)
		return u
	}
	rr := doUpload(t, newUploadTestRouter(h), validUploadQuery)
	if rr.Code != http.StatusAccepted {
		t.Fatalf("status %d, want 202; body=%s", rr.Code, rr.Body)
	}
}

func TestRestoreUploadNoStorageClassIs409AndDeletesRestore(t *testing.T) {
	store := newTestStore(t)
	k := fakeRestoreClient(newUploadServerFixture(nil))
	k.Dynamic.(*dynamicfake.FakeDynamicClient).PrependReactor("create", "restores",
		restoreCreateReactor("Failed", "", "NoStagingStorageClass: set the Helm value backups.uploadStagingStorageClass", nil))
	h := newUploadTestHandler(t, store, k)
	rr := doUpload(t, newUploadTestRouter(h), validUploadQuery)
	expectBackupRefusal(t, rr, http.StatusConflict, "server.restore.upload.no_storage_class")
	if uploadRestoreExists(t, k, testRestoreName) {
		t.Fatal("restore still exists after no storage class")
	}
	rows := readBackupAuditRows(t, store)
	if len(rows) != 2 || rows[0].Status != http.StatusAccepted || rows[1].Status != http.StatusConflict {
		t.Fatalf("audit rows %+v, want intent then 409 correction", rows)
	}
}

func TestRestoreUploadLocalHappyPathStreamsAndAccepts(t *testing.T) {
	store := newTestStore(t)
	k := fakeRestoreClient(newUploadServerFixture(nil))
	k.Dynamic.(*dynamicfake.FakeDynamicClient).PrependReactor("create", "restores", restoreCreateReactor("Receiving", "rx-alpha", "", nil))

	var gotPath string
	var gotBody []byte
	var gotLength int64
	worker := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotLength = r.ContentLength
		gotBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(worker.Close)

	h := newUploadTestHandler(t, store, k)
	h.tlsClient = worker.Client()
	h.uploadURL = func(_ backupExportHost, uid string) *url.URL {
		u, _ := url.Parse(worker.URL + "/upload/" + uid)
		return u
	}

	rr := doUpload(t, newUploadTestRouter(h), validUploadQuery)
	if rr.Code != http.StatusAccepted {
		t.Fatalf("status %d, want 202; body=%s", rr.Code, rr.Body)
	}
	var out struct {
		Restore string `json:"restore"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil || out.Restore != testRestoreName {
		t.Fatalf("body %q, want restore %s", rr.Body, testRestoreName)
	}
	if gotPath != "/upload/"+testRestoreUID {
		t.Fatalf("receiver path %q", gotPath)
	}
	if gotLength != int64(len(testUploadBody)) || string(gotBody) != testUploadBody {
		t.Fatalf("receiver got length %d body %q", gotLength, gotBody)
	}
	if !uploadRestoreExists(t, k, testRestoreName) {
		t.Fatal("restore deleted on success")
	}
	rows := readBackupAuditRows(t, store)
	want := "alpha:a.tar.gz:13"
	if len(rows) != 1 || rows[0].Path != "/restores:upload" || rows[0].Target != want || rows[0].Status != http.StatusAccepted {
		t.Fatalf("audit rows %+v, want one intent row target %s", rows, want)
	}
}

func TestRestoreUploadRemoteRoutesThroughGateway(t *testing.T) {
	store := newTestStore(t)
	home := fakeRestoreClient()
	remote := fakeRestoreClient(newUploadServerFixture(nil))
	remote.Dynamic.(*dynamicfake.FakeDynamicClient).PrependReactor("create", "restores", restoreCreateReactor("Receiving", "rx-alpha", "", nil))

	reg := kube.NewRegistry(scope.DefaultCluster)
	reg.Set(scope.DefaultCluster, home)
	reg.Set("remote", remote)
	gateway := &fakeRestoreUploadGateway{}
	h := newBackupArchiveHandler(reg, audit.New(store), BackupArchiveConfig{MaxConcurrentExports: 3})
	h.pollInterval = 2 * time.Millisecond
	h.startTimeout = 2 * time.Second
	h.uploads = gateway

	rr := doUpload(t, newUploadTestRouter(h), validUploadQuery+"&cluster=remote")
	if rr.Code != http.StatusAccepted {
		t.Fatalf("status %d, want 202; body=%s", rr.Code, rr.Body)
	}
	want := gatewayprotocol.RestoreUploadTarget{
		Target:     gatewayprotocol.Target{Cluster: "remote", Namespace: scope.DefaultNamespace, Name: "alpha", UID: "server-uid"},
		Restore:    testRestoreName,
		RestoreUID: testRestoreUID,
	}
	if gateway.target == nil || *gateway.target != want {
		t.Fatalf("gateway target %+v, want %+v", gateway.target, want)
	}
	if gateway.cluster != "remote" || gateway.body != testUploadBody || gateway.contentLength != int64(len(testUploadBody)) {
		t.Fatalf("gateway cluster %q body %q length %d", gateway.cluster, gateway.body, gateway.contentLength)
	}
}

func TestRestoreUploadWorkerRefusalDeletesRestoreAndCorrectsAudit(t *testing.T) {
	store := newTestStore(t)
	k := fakeRestoreClient(newUploadServerFixture(nil))
	k.Dynamic.(*dynamicfake.FakeDynamicClient).PrependReactor("create", "restores", restoreCreateReactor("Receiving", "rx-alpha", "", nil))
	worker := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.ReadAll(r.Body)
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	t.Cleanup(worker.Close)
	h := newUploadTestHandler(t, store, k)
	h.tlsClient = worker.Client()
	h.uploadURL = func(_ backupExportHost, uid string) *url.URL {
		u, _ := url.Parse(worker.URL + "/upload/" + uid)
		return u
	}
	rr := doUpload(t, newUploadTestRouter(h), validUploadQuery)
	expectBackupRefusal(t, rr, http.StatusBadGateway, "server.restore.upload.failed")
	if uploadRestoreExists(t, k, testRestoreName) {
		t.Fatal("restore kept after the receiver refused the upload")
	}
	rows := readBackupAuditRows(t, store)
	if len(rows) != 2 || rows[0].Status != http.StatusAccepted || rows[1].Status != http.StatusBadGateway {
		t.Fatalf("audit rows %+v, want intent then 502 correction", rows)
	}
}

func TestRestoreUploadLongerThanDeclaredAbortsAndDeletesRestore(t *testing.T) {
	store := newTestStore(t)
	k := fakeRestoreClient(newUploadServerFixture(nil))
	k.Dynamic.(*dynamicfake.FakeDynamicClient).PrependReactor("create", "restores", restoreCreateReactor("Receiving", "rx-alpha", "", nil))
	worker := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(worker.Close)
	h := newUploadTestHandler(t, store, k)
	h.tlsClient = worker.Client()
	h.uploadURL = func(_ backupExportHost, uid string) *url.URL {
		u, _ := url.Parse(worker.URL + "/upload/" + uid)
		return u
	}
	rr := httptest.NewRecorder()
	// The body carries more bytes than the declared length.
	req := uploadRequest(t, "/restores:upload?"+validUploadQuery, []byte(testUploadBody+"-extra"), int64(len(testUploadBody)))
	newUploadTestRouter(h).ServeHTTP(rr, req)
	expectBackupRefusal(t, rr, http.StatusBadRequest, "server.restore.upload.bad_request")
	if uploadRestoreExists(t, k, testRestoreName) {
		t.Fatal("restore kept after an overlong upload")
	}
}

func TestRestoreUploadClientGoneWhileWaitingDeletesRestore(t *testing.T) {
	store := newTestStore(t)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	k := fakeRestoreClient(newUploadServerFixture(nil))
	// The operator never reports Receiving; the client goes away after the Restore is created.
	k.Dynamic.(*dynamicfake.FakeDynamicClient).PrependReactor("create", "restores", restoreCreateReactor("", "", "", cancel))
	h := newUploadTestHandler(t, store, k)
	h.startTimeout = time.Minute

	rr := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(ctx, http.MethodPost, "/restores:upload?"+validUploadQuery, bytes.NewReader([]byte(testUploadBody)))
	newUploadTestRouter(h).ServeHTTP(rr, req)
	if uploadRestoreExists(t, k, testRestoreName) {
		t.Fatal("restore kept after the client disconnected")
	}
	rows := readBackupAuditRows(t, store)
	if len(rows) != 2 || rows[1].Status != 499 {
		t.Fatalf("audit rows %+v, want intent then 499 correction", rows)
	}
}

// newConfirmFixtureClient builds a client holding one upload Restore.
func newConfirmFixtureClient(phase string, generation int64) *kube.Client {
	return fakeRestoreClient(newUploadRestoreFixture("alpha-restore-1", phase, generation, false))
}

func doConfirm(t *testing.T, h http.Handler, name, body string) *httptest.ResponseRecorder {
	t.Helper()
	rr := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/restores/"+name+":confirm", strings.NewReader(body))
	req.Header.Set("Accept", "application/problem+json")
	h.ServeHTTP(rr, req)
	return rr
}

func TestRestoreConfirmWrongPhaseIs409(t *testing.T) {
	store := newTestStore(t)
	h := newUploadTestHandler(t, store, newConfirmFixtureClient("Receiving", 1))
	rr := doConfirm(t, newUploadTestRouter(h), "alpha-restore-1", `{"generation":1}`)
	expectBackupRefusal(t, rr, http.StatusConflict, "server.restore.confirm.not_ready")
}

func TestRestoreConfirmWrongGenerationIs409(t *testing.T) {
	store := newTestStore(t)
	h := newUploadTestHandler(t, store, newConfirmFixtureClient("AwaitingConfirmation", 3))
	rr := doConfirm(t, newUploadTestRouter(h), "alpha-restore-1", `{"generation":2}`)
	expectBackupRefusal(t, rr, http.StatusConflict, "server.restore.confirm.not_ready")
}

func TestRestoreConfirmBadBodyIs400(t *testing.T) {
	store := newTestStore(t)
	h := newUploadTestHandler(t, store, newConfirmFixtureClient("AwaitingConfirmation", 3))
	rr := doConfirm(t, newUploadTestRouter(h), "alpha-restore-1", `{"generation":0}`)
	expectBackupRefusal(t, rr, http.StatusBadRequest, "server.restore.confirm.bad_request")
}

func TestRestoreConfirmSetsConfirmedAndAudits(t *testing.T) {
	store := newTestStore(t)
	k := newConfirmFixtureClient("AwaitingConfirmation", 3)
	h := newUploadTestHandler(t, store, k)
	rr := doConfirm(t, newUploadTestRouter(h), "alpha-restore-1", `{"generation":3}`)
	if rr.Code != http.StatusAccepted {
		t.Fatalf("status %d, want 202; body=%s", rr.Code, rr.Body)
	}
	u, err := k.Dynamic.Resource(kube.GVRRestore).Namespace(scope.DefaultNamespace).Get(t.Context(), "alpha-restore-1", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get restore: %v", err)
	}
	confirmed, found, _ := unstructured.NestedBool(u.Object, "spec", "upload", "confirmed")
	if !found || !confirmed {
		t.Fatalf("spec.upload.confirmed = %v (found %v), want true", confirmed, found)
	}
	rows := readBackupAuditRows(t, store)
	if len(rows) != 1 || rows[0].Path != "/restores/alpha-restore-1:confirm" || rows[0].Target != "alpha-restore-1" || rows[0].Status != http.StatusAccepted {
		t.Fatalf("audit rows %+v", rows)
	}
}

func TestRestoreConfirmRefusalIsAudited(t *testing.T) {
	store := newTestStore(t)
	h := newUploadTestHandler(t, store, newConfirmFixtureClient("Receiving", 1))
	_ = doConfirm(t, newUploadTestRouter(h), "alpha-restore-1", `{"generation":1}`)
	rows := readBackupAuditRows(t, store)
	if len(rows) != 1 || rows[0].Path != "/restores/alpha-restore-1:confirm" || rows[0].Status != http.StatusConflict {
		t.Fatalf("audit rows %+v, want one 409 refusal row", rows)
	}
}
