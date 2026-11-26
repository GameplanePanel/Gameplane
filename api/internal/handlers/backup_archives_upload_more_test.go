package handlers

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	dynamicfake "k8s.io/client-go/dynamic/fake"

	"github.com/GameplanePanel/gameplane/api/internal/audit"
	"github.com/GameplanePanel/gameplane/api/internal/db"
	"github.com/GameplanePanel/gameplane/api/internal/gatewayprotocol"
	"github.com/GameplanePanel/gameplane/api/internal/kube"
	"github.com/GameplanePanel/gameplane/api/internal/scope"
)

// uploadMoreGateway stands in for the gateway's RestoreUploadStream. err wins.
// Otherwise it reads the body, ignoring its error, and answers with status.
// Ignoring the error lets a test reach the overflow check after the read.
type uploadMoreGateway struct {
	err    error
	status int
}

func (g uploadMoreGateway) RestoreUploadStream(_ context.Context, _ string, _ gatewayprotocol.RestoreUploadTarget, body io.Reader, _ int64) (*http.Response, error) {
	if g.err != nil {
		return nil, g.err
	}
	_, _ = io.ReadAll(body)
	return &http.Response{StatusCode: g.status, Body: io.NopCloser(strings.NewReader(""))}, nil
}

// uploadMoreRemoteHandler builds a handler whose "remote" cluster is remote.
// A nil uploads leaves the gateway unconfigured.
func uploadMoreRemoteHandler(t *testing.T, store *db.Store, remote *kube.Client, uploads restoreUploadGateway) *backupArchiveHandler {
	t.Helper()
	reg := kube.NewRegistry(scope.DefaultCluster)
	reg.Set(scope.DefaultCluster, fakeRestoreClient())
	reg.Set("remote", remote)
	h := newBackupArchiveHandler(reg, audit.New(store), BackupArchiveConfig{MaxConcurrentExports: 3})
	h.pollInterval = 2 * time.Millisecond
	h.startTimeout = 2 * time.Second
	h.uploads = uploads
	return h
}

// uploadMoreRemoteClient is the remote cluster for an upload: GameServer
// "alpha" and a create that reports the receiver as Receiving.
func uploadMoreRemoteClient() *kube.Client {
	remote := fakeRestoreClient(newUploadServerFixture(nil))
	remote.Dynamic.(*dynamicfake.FakeDynamicClient).PrependReactor("create", "restores", restoreCreateReactor("Receiving", "rx-alpha", "", nil))
	return remote
}

var uploadMoreBoom = errors.New("disk gone")

// uploadMoreFailingReader fails every read with uploadMoreBoom.
type uploadMoreFailingReader struct{}

func (uploadMoreFailingReader) Read([]byte) (int, error) {
	return 0, uploadMoreBoom
}

func TestRestoreUploadUnknownClusterIsRefusedWithoutAudit(t *testing.T) {
	t.Parallel()
	store := backupMoreStore(t)
	h := newUploadTestHandler(t, store, fakeRestoreClient(newUploadServerFixture(nil)))
	rr := doUpload(t, newUploadTestRouter(h), validUploadQuery+"&cluster=nope")
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status %d, want 400; body=%s", rr.Code, rr.Body)
	}
	if rows := readBackupAuditRows(t, store); len(rows) != 0 {
		t.Fatalf("audit rows %+v, want none for an unknown cluster", rows)
	}
}

func TestRestoreUploadForbiddenNamespaceIs403WithoutAudit(t *testing.T) {
	t.Parallel()
	store := backupMoreStore(t)
	h := newUploadTestHandler(t, store, fakeRestoreClient(newUploadServerFixture(nil)))
	rr := doUpload(t, newUploadTestRouter(h), validUploadQuery+"&namespace=elsewhere")
	if rr.Code != http.StatusForbidden {
		t.Fatalf("status %d, want 403; body=%s", rr.Code, rr.Body)
	}
	if rows := readBackupAuditRows(t, store); len(rows) != 0 {
		t.Fatalf("audit rows %+v, want none for a forbidden namespace", rows)
	}
}

func TestRestoreUploadInvalidServerNameIs400(t *testing.T) {
	t.Parallel()
	store := backupMoreStore(t)
	h := newUploadTestHandler(t, store, fakeRestoreClient(newUploadServerFixture(nil)))
	rr := doUpload(t, newUploadTestRouter(h), "server=Bad_Name&format=tar.gz&fileName=a.tar.gz")
	expectBackupRefusal(t, rr, http.StatusBadRequest, "server.restore.upload.bad_request")
	rows := readBackupAuditRows(t, store)
	if len(rows) != 1 || rows[0].Status != http.StatusBadRequest {
		t.Fatalf("audit rows %+v, want one 400 refusal", rows)
	}
}

func TestRestoreUploadGameServerLookupErrorIs500(t *testing.T) {
	t.Parallel()
	store := backupMoreStore(t)
	k := fakeRestoreClient(newUploadServerFixture(nil))
	k.Dynamic.(*dynamicfake.FakeDynamicClient).PrependReactor("get", "gameservers", backupMoreFail(errors.New("apiserver down")))
	h := newUploadTestHandler(t, store, k)
	rr := doUpload(t, newUploadTestRouter(h), validUploadQuery)
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("status %d, want 500; body=%s", rr.Code, rr.Body)
	}
	rows := readBackupAuditRows(t, store)
	if len(rows) != 1 || rows[0].Status != http.StatusInternalServerError {
		t.Fatalf("audit rows %+v, want one 500 refusal", rows)
	}
}

func TestRestoreUploadGameServerDeletingIs404(t *testing.T) {
	t.Parallel()
	store := backupMoreStore(t)
	h := newUploadTestHandler(t, store, fakeRestoreClient(backupMoreGameServer(true)))
	rr := doUpload(t, newUploadTestRouter(h), validUploadQuery)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status %d, want 404; body=%s", rr.Code, rr.Body)
	}
	rows := readBackupAuditRows(t, store)
	if len(rows) != 1 || rows[0].Status != http.StatusNotFound {
		t.Fatalf("audit rows %+v, want one 404 refusal", rows)
	}
}

func TestRestoreUploadBusyCheckListErrorIs500(t *testing.T) {
	t.Parallel()
	for _, resource := range []string{"backups", "restores"} {
		t.Run(resource, func(t *testing.T) {
			t.Parallel()
			store := backupMoreStore(t)
			k := fakeRestoreClient(newUploadServerFixture(nil))
			k.Dynamic.(*dynamicfake.FakeDynamicClient).PrependReactor("list", resource, backupMoreFail(errors.New("apiserver down")))
			h := newUploadTestHandler(t, store, k)
			rr := doUpload(t, newUploadTestRouter(h), validUploadQuery)
			if rr.Code != http.StatusInternalServerError {
				t.Fatalf("status %d, want 500; body=%s", rr.Code, rr.Body)
			}
		})
	}
}

func TestRestoreUploadBusyWhileOperationOpenIs409(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		other func() runtime.Object
	}{
		{name: "backup not reconciled", other: func() runtime.Object { return backupMoreForServer("b2", "alpha", "") }},
		{name: "backup pending", other: func() runtime.Object { return backupMoreForServer("b2", "alpha", "Pending") }},
		{name: "restore receiving", other: func() runtime.Object { return newUploadRestoreFixture("r-other", "Receiving", 1, false) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			store := backupMoreStore(t)
			k := fakeRestoreClient(newUploadServerFixture(nil), tc.other())
			h := newUploadTestHandler(t, store, k)
			rr := doUpload(t, newUploadTestRouter(h), validUploadQuery)
			expectBackupRefusal(t, rr, http.StatusConflict, "server.restore.upload.server_busy")
		})
	}
}

func TestRestoreUploadOtherServerWorkDoesNotBlockIs500AfterCreate(t *testing.T) {
	t.Parallel()
	store := backupMoreStore(t)
	k := fakeRestoreClient(newUploadServerFixture(nil), backupMoreForServer("beta-b", "beta", "Running"))
	k.Dynamic.(*dynamicfake.FakeDynamicClient).PrependReactor("create", "restores", backupMoreFail(errors.New("quota exceeded")))
	h := newUploadTestHandler(t, store, k)
	rr := doUpload(t, newUploadTestRouter(h), validUploadQuery)
	// Reaching the create (and its 500) shows the busy check ignored beta's Backup.
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("status %d, want 500 from the create; body=%s", rr.Code, rr.Body)
	}
	rows := readBackupAuditRows(t, store)
	if len(rows) != 2 || rows[0].Status != http.StatusAccepted || rows[1].Status != http.StatusInternalServerError {
		t.Fatalf("audit rows %+v, want intent 202 then 500 correction", rows)
	}
}

func TestRestoreUploadAuditFailureIs500WithoutRestore(t *testing.T) {
	t.Parallel()
	store := backupMoreStore(t)
	k := fakeRestoreClient(newUploadServerFixture(nil))
	h := newUploadTestHandler(t, store, k)
	backupMoreDropAudit(t, store)
	rr := doUpload(t, newUploadTestRouter(h), validUploadQuery)
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("status %d, want 500 instead of the 202 intent; body=%s", rr.Code, rr.Body)
	}
	if uploadRestoreExists(t, k, testRestoreName) {
		t.Fatal("restore created although the intent row could not be written")
	}
}

func TestRestoreUploadCreateRestoreErrorIs500Audited(t *testing.T) {
	t.Parallel()
	store := backupMoreStore(t)
	k := fakeRestoreClient(newUploadServerFixture(nil))
	k.Dynamic.(*dynamicfake.FakeDynamicClient).PrependReactor("create", "restores", backupMoreFail(errors.New("quota exceeded")))
	h := newUploadTestHandler(t, store, k)
	rr := doUpload(t, newUploadTestRouter(h), validUploadQuery)
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("status %d, want 500; body=%s", rr.Code, rr.Body)
	}
	rows := readBackupAuditRows(t, store)
	if len(rows) != 2 || rows[1].Status != http.StatusInternalServerError {
		t.Fatalf("audit rows %+v, want intent then 500 correction", rows)
	}
}

func TestRestoreUploadRestoreLookupErrorIs500Audited(t *testing.T) {
	t.Parallel()
	store := backupMoreStore(t)
	k := fakeRestoreClient(newUploadServerFixture(nil))
	dyn := k.Dynamic.(*dynamicfake.FakeDynamicClient)
	dyn.PrependReactor("create", "restores", restoreCreateReactor("Pending", "", "", nil))
	dyn.PrependReactor("get", "restores", backupMoreFail(errors.New("apiserver down")))
	h := newUploadTestHandler(t, store, k)
	rr := doUpload(t, newUploadTestRouter(h), validUploadQuery)
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("status %d, want 500; body=%s", rr.Code, rr.Body)
	}
	rows := readBackupAuditRows(t, store)
	if len(rows) != 2 || rows[1].Status != http.StatusInternalServerError {
		t.Fatalf("audit rows %+v, want intent then 500 correction", rows)
	}
}

func TestRestoreUploadRestoreDisappearedIs500(t *testing.T) {
	t.Parallel()
	store := backupMoreStore(t)
	k := fakeRestoreClient(newUploadServerFixture(nil))
	dyn := k.Dynamic.(*dynamicfake.FakeDynamicClient)
	dyn.PrependReactor("create", "restores", restoreCreateReactor("Pending", "", "", nil))
	dyn.PrependReactor("get", "restores", backupMoreFail(apierrors.NewNotFound(kube.GVRRestore.GroupResource(), testRestoreName)))
	h := newUploadTestHandler(t, store, k)
	rr := doUpload(t, newUploadTestRouter(h), validUploadQuery)
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("status %d, want 500; body=%s", rr.Code, rr.Body)
	}
}

func TestRestoreUploadFailedRestoreIs502Audited(t *testing.T) {
	t.Parallel()
	store := backupMoreStore(t)
	k := fakeRestoreClient(newUploadServerFixture(nil))
	k.Dynamic.(*dynamicfake.FakeDynamicClient).PrependReactor("create", "restores", restoreCreateReactor("Failed", "", "image pull exploded", nil))
	h := newUploadTestHandler(t, store, k)
	rr := doUpload(t, newUploadTestRouter(h), validUploadQuery)
	expectBackupRefusal(t, rr, http.StatusBadGateway, "server.restore.upload.failed")
	rows := readBackupAuditRows(t, store)
	if len(rows) != 2 || rows[1].Status != http.StatusBadGateway {
		t.Fatalf("audit rows %+v, want intent then 502 correction", rows)
	}
	if uploadRestoreExists(t, k, testRestoreName) {
		t.Fatal("failed restore kept")
	}
}

func TestRestoreUploadUnexpectedPhaseIs502(t *testing.T) {
	t.Parallel()
	store := backupMoreStore(t)
	k := fakeRestoreClient(newUploadServerFixture(nil))
	k.Dynamic.(*dynamicfake.FakeDynamicClient).PrependReactor("create", "restores", restoreCreateReactor("Succeeded", "", "", nil))
	h := newUploadTestHandler(t, store, k)
	rr := doUpload(t, newUploadTestRouter(h), validUploadQuery)
	expectBackupRefusal(t, rr, http.StatusBadGateway, "server.restore.upload.failed")
}

func TestRestoreUploadReceiverNeverReadyTimesOutIs502(t *testing.T) {
	t.Parallel()
	store := backupMoreStore(t)
	k := fakeRestoreClient(newUploadServerFixture(nil))
	k.Dynamic.(*dynamicfake.FakeDynamicClient).PrependReactor("create", "restores", restoreCreateReactor("Pending", "", "", nil))
	h := newUploadTestHandler(t, store, k)
	h.startTimeout = 20 * time.Millisecond
	rr := doUpload(t, newUploadTestRouter(h), validUploadQuery)
	expectBackupRefusal(t, rr, http.StatusBadGateway, "server.restore.upload.failed")
	rows := readBackupAuditRows(t, store)
	if len(rows) != 2 || rows[1].Status != http.StatusBadGateway {
		t.Fatalf("audit rows %+v, want intent then 502 correction", rows)
	}
}

func TestRestoreUploadInvalidReceiverAddressIs502(t *testing.T) {
	t.Parallel()
	store := backupMoreStore(t)
	k := fakeRestoreClient(newUploadServerFixture(nil))
	k.Dynamic.(*dynamicfake.FakeDynamicClient).PrependReactor("create", "restores", restoreCreateReactor("Receiving", "Bad_Receiver", "", nil))
	h := newUploadTestHandler(t, store, k)
	h.tlsClient = &http.Client{}
	rr := doUpload(t, newUploadTestRouter(h), validUploadQuery)
	expectBackupRefusal(t, rr, http.StatusBadGateway, "server.restore.upload.failed")
}

func TestRestoreUploadWithoutMTLSIs502(t *testing.T) {
	t.Parallel()
	store := backupMoreStore(t)
	k := fakeRestoreClient(newUploadServerFixture(nil))
	k.Dynamic.(*dynamicfake.FakeDynamicClient).PrependReactor("create", "restores", restoreCreateReactor("Receiving", "rx-alpha", "", nil))
	h := newUploadTestHandler(t, store, k)
	rr := doUpload(t, newUploadTestRouter(h), validUploadQuery)
	expectBackupRefusal(t, rr, http.StatusBadGateway, "server.restore.upload.failed")
}

func TestRestoreUploadReceiverURLBuildFailureIs502(t *testing.T) {
	t.Parallel()
	store := backupMoreStore(t)
	k := fakeRestoreClient(newUploadServerFixture(nil))
	k.Dynamic.(*dynamicfake.FakeDynamicClient).PrependReactor("create", "restores", restoreCreateReactor("Receiving", "rx-alpha", "", nil))
	h := newUploadTestHandler(t, store, k)
	h.tlsClient = &http.Client{}
	h.uploadURL = func(_ backupExportHost, _ string) *url.URL {
		return &url.URL{Scheme: "1bad", Host: "receiver"}
	}
	rr := doUpload(t, newUploadTestRouter(h), validUploadQuery)
	expectBackupRefusal(t, rr, http.StatusBadGateway, "server.restore.upload.failed")
}

func TestRestoreUploadReceiverUnreachableIs502(t *testing.T) {
	t.Parallel()
	store := backupMoreStore(t)
	k := fakeRestoreClient(newUploadServerFixture(nil))
	k.Dynamic.(*dynamicfake.FakeDynamicClient).PrependReactor("create", "restores", restoreCreateReactor("Receiving", "rx-alpha", "", nil))
	client, base := backupMoreClosedServer(t)
	h := newUploadTestHandler(t, store, k)
	h.tlsClient = client
	h.uploadURL = func(_ backupExportHost, uid string) *url.URL {
		u, _ := url.Parse(base + "/upload/" + uid)
		return u
	}
	rr := doUpload(t, newUploadTestRouter(h), validUploadQuery)
	expectBackupRefusal(t, rr, http.StatusBadGateway, "server.restore.upload.failed")
}

func TestRestoreUploadClientGoneDuringSendIs499(t *testing.T) {
	t.Parallel()
	store := backupMoreStore(t)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	k := fakeRestoreClient(newUploadServerFixture(nil))
	// The receiver is ready, and the client leaves before the body is sent.
	k.Dynamic.(*dynamicfake.FakeDynamicClient).PrependReactor("create", "restores", restoreCreateReactor("Receiving", "rx-alpha", "", cancel))
	h := newUploadTestHandler(t, store, k)
	h.tlsClient = &http.Client{}
	rr := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(ctx, http.MethodPost, "/restores:upload?"+validUploadQuery, bytes.NewReader([]byte(testUploadBody)))
	newUploadTestRouter(h).ServeHTTP(rr, req)
	rows := readBackupAuditRows(t, store)
	if len(rows) != 2 || rows[1].Status != 499 {
		t.Fatalf("audit rows %+v, want intent then 499 correction", rows)
	}
	if uploadRestoreExists(t, k, testRestoreName) {
		t.Fatal("restore kept after the client disconnected")
	}
}

func TestRestoreUploadRemoteOverlongWithOKReplyIs400(t *testing.T) {
	t.Parallel()
	store := backupMoreStore(t)
	h := uploadMoreRemoteHandler(t, store, uploadMoreRemoteClient(), uploadMoreGateway{status: http.StatusOK})
	rr := httptest.NewRecorder()
	// The gateway accepts the bytes with a 200, but the body is longer than declared.
	req := uploadRequest(t, "/restores:upload?"+validUploadQuery+"&cluster=remote", []byte(testUploadBody+"-extra"), int64(len(testUploadBody)))
	newUploadTestRouter(h).ServeHTTP(rr, req)
	expectBackupRefusal(t, rr, http.StatusBadRequest, "server.restore.upload.bad_request")
}

func TestRestoreUploadRemoteGatewayFailuresMapStatus(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		uploads restoreUploadGateway
	}{
		{name: "gateway not configured", uploads: nil},
		{name: "gateway error", uploads: uploadMoreGateway{err: errors.New("tunnel down")}},
		{name: "receiver refused", uploads: uploadMoreGateway{status: http.StatusInternalServerError}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			store := backupMoreStore(t)
			h := uploadMoreRemoteHandler(t, store, uploadMoreRemoteClient(), tc.uploads)
			rr := doUpload(t, newUploadTestRouter(h), validUploadQuery+"&cluster=remote")
			expectBackupRefusal(t, rr, http.StatusBadGateway, "server.restore.upload.failed")
			rows := readBackupAuditRows(t, store)
			if len(rows) != 2 || rows[1].Status != http.StatusBadGateway {
				t.Fatalf("audit rows %+v, want intent then 502 correction", rows)
			}
		})
	}
}

func TestUploadBodyReaderYieldsDeclaredBytesOnly(t *testing.T) {
	t.Parallel()
	body := &uploadBodyReader{r: strings.NewReader("0123456789"), remaining: 4}
	got, err := io.ReadAll(body)
	if !errors.Is(err, errUploadOverflow) {
		t.Fatalf("err %v, want errUploadOverflow", err)
	}
	if string(got) != "0123" || !body.overflowed {
		t.Fatalf("read %q overflowed %v, want 0123 and overflow", got, body.overflowed)
	}
}

func TestUploadBodyReaderExactLengthIsEOF(t *testing.T) {
	t.Parallel()
	body := &uploadBodyReader{r: strings.NewReader("0123"), remaining: 4}
	got, err := io.ReadAll(body)
	if err != nil || string(got) != "0123" || body.overflowed {
		t.Fatalf("read %q err %v overflowed %v, want 0123 and no error", got, err, body.overflowed)
	}
}

func TestUploadBodyReaderShortBodyIsUnexpectedEOF(t *testing.T) {
	t.Parallel()
	body := &uploadBodyReader{r: strings.NewReader("01"), remaining: 4}
	got, err := io.ReadAll(body)
	if !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("err %v, want io.ErrUnexpectedEOF", err)
	}
	if string(got) != "01" {
		t.Fatalf("read %q, want 01", got)
	}
}

func TestUploadBodyReaderSourceErrorIsReturned(t *testing.T) {
	t.Parallel()
	src := io.MultiReader(strings.NewReader("0123"), uploadMoreFailingReader{})
	body := &uploadBodyReader{r: src, remaining: 4}
	got, err := io.ReadAll(body)
	if !errors.Is(err, uploadMoreBoom) {
		t.Fatalf("err %v, want the source error", err)
	}
	if string(got) != "0123" {
		t.Fatalf("read %q, want 0123", got)
	}
}

func TestRestoreConfirmBadJSONIs400(t *testing.T) {
	t.Parallel()
	store := backupMoreStore(t)
	h := newUploadTestHandler(t, store, newConfirmFixtureClient("AwaitingConfirmation", 3))
	rr := doConfirm(t, newUploadTestRouter(h), "alpha-restore-1", `{"generation":`)
	expectBackupRefusal(t, rr, http.StatusBadRequest, "server.restore.confirm.bad_request")
	rows := readBackupAuditRows(t, store)
	if len(rows) != 1 || rows[0].Status != http.StatusBadRequest {
		t.Fatalf("audit rows %+v, want one 400 refusal", rows)
	}
}

func TestRestoreConfirmMissingRestoreIs404(t *testing.T) {
	t.Parallel()
	store := backupMoreStore(t)
	h := newUploadTestHandler(t, store, fakeRestoreClient())
	rr := doConfirm(t, newUploadTestRouter(h), "gone", `{"generation":1}`)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status %d, want 404; body=%s", rr.Code, rr.Body)
	}
	rows := readBackupAuditRows(t, store)
	if len(rows) != 1 || rows[0].Path != "/restores/gone:confirm" || rows[0].Status != http.StatusNotFound {
		t.Fatalf("audit rows %+v, want one 404 refusal", rows)
	}
}

func TestRestoreConfirmLookupErrorIs500(t *testing.T) {
	t.Parallel()
	store := backupMoreStore(t)
	k := newConfirmFixtureClient("AwaitingConfirmation", 3)
	k.Dynamic.(*dynamicfake.FakeDynamicClient).PrependReactor("get", "restores", backupMoreFail(errors.New("apiserver down")))
	h := newUploadTestHandler(t, store, k)
	rr := doConfirm(t, newUploadTestRouter(h), "alpha-restore-1", `{"generation":3}`)
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("status %d, want 500; body=%s", rr.Code, rr.Body)
	}
}

func TestRestoreConfirmWithoutUploadSpecIs409(t *testing.T) {
	t.Parallel()
	store := backupMoreStore(t)
	restore := newUploadRestoreFixture("alpha-restore-1", "AwaitingConfirmation", 3, false)
	unstructured.RemoveNestedField(restore.Object, "spec", "upload")
	h := newUploadTestHandler(t, store, fakeRestoreClient(restore))
	rr := doConfirm(t, newUploadTestRouter(h), "alpha-restore-1", `{"generation":3}`)
	expectBackupRefusal(t, rr, http.StatusConflict, "server.restore.confirm.not_ready")
}

func TestRestoreConfirmAuditFailureIs500(t *testing.T) {
	t.Parallel()
	store := backupMoreStore(t)
	h := newUploadTestHandler(t, store, newConfirmFixtureClient("AwaitingConfirmation", 3))
	backupMoreDropAudit(t, store)
	rr := doConfirm(t, newUploadTestRouter(h), "alpha-restore-1", `{"generation":3}`)
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("status %d, want 500 instead of the 202; body=%s", rr.Code, rr.Body)
	}
}

func TestRestoreConfirmPatchConflictIs409Audited(t *testing.T) {
	t.Parallel()
	store := backupMoreStore(t)
	k := newConfirmFixtureClient("AwaitingConfirmation", 3)
	k.Dynamic.(*dynamicfake.FakeDynamicClient).PrependReactor("patch", "restores",
		backupMoreFail(apierrors.NewConflict(kube.GVRRestore.GroupResource(), "alpha-restore-1", errors.New("stale"))))
	h := newUploadTestHandler(t, store, k)
	rr := doConfirm(t, newUploadTestRouter(h), "alpha-restore-1", `{"generation":3}`)
	expectBackupRefusal(t, rr, http.StatusConflict, "server.restore.confirm.not_ready")
	rows := readBackupAuditRows(t, store)
	if len(rows) != 2 || rows[0].Status != http.StatusAccepted || rows[1].Status != http.StatusConflict {
		t.Fatalf("audit rows %+v, want intent 202 then 409 correction", rows)
	}
}

func TestRestoreConfirmPatchErrorIs500Audited(t *testing.T) {
	t.Parallel()
	store := backupMoreStore(t)
	k := newConfirmFixtureClient("AwaitingConfirmation", 3)
	k.Dynamic.(*dynamicfake.FakeDynamicClient).PrependReactor("patch", "restores", backupMoreFail(errors.New("apiserver down")))
	h := newUploadTestHandler(t, store, k)
	rr := doConfirm(t, newUploadTestRouter(h), "alpha-restore-1", `{"generation":3}`)
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("status %d, want 500; body=%s", rr.Code, rr.Body)
	}
	rows := readBackupAuditRows(t, store)
	if len(rows) != 2 || rows[1].Status != http.StatusInternalServerError {
		t.Fatalf("audit rows %+v, want intent then 500 correction", rows)
	}
}
