package handlers

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"k8s.io/apimachinery/pkg/runtime"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	clienttesting "k8s.io/client-go/testing"
)

func TestBackupDownloadClientGoneDuringExportLookupIs499(t *testing.T) {
	t.Parallel()
	store := backupMoreStore(t)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	k := fakeBackupClient(newBackupFixture("b1", "Succeeded", ""))
	dyn := k.Dynamic.(*dynamicfake.FakeDynamicClient)
	dyn.PrependReactor("create", "backupexports", backupMoreExportCreate("bk-worker", "Pending", "", cancel))
	dyn.PrependReactor("get", "backupexports", backupMoreFail(errors.New("connection reset")))
	h := newBackupTestHandler(t, store, k, 3)
	rr := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(ctx, http.MethodGet, "/backups/b1:download", nil)
	newBackupTestRouter(h).ServeHTTP(rr, req)
	rows := readBackupAuditRows(t, store)
	if len(rows) != 2 || rows[1].Status != 499 {
		t.Fatalf("audit rows %+v, want intent then 499 correction", rows)
	}
}

func TestBackupDownloadCorrectionWriteFailureStillAnswers(t *testing.T) {
	t.Parallel()
	store := backupMoreStore(t)
	k := fakeBackupClient(newBackupFixture("b1", "Succeeded", ""))
	// The create drops the audit table, so the intent row exists but the
	// correction row cannot be written. The client still gets the 500.
	k.Dynamic.(*dynamicfake.FakeDynamicClient).PrependReactor("create", "backupexports",
		func(clienttesting.Action) (bool, runtime.Object, error) {
			backupMoreDropAudit(t, store)
			return true, nil, errors.New("quota exceeded")
		})
	h := newBackupTestHandler(t, store, k, 3)
	rr := do(t, newBackupTestRouter(h), http.MethodGet, "/backups/b1:download", nil)
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("status %d, want 500; body=%s", rr.Code, rr.Body)
	}
}

func TestRestoreUploadClientGoneDuringLookupIs499(t *testing.T) {
	t.Parallel()
	store := backupMoreStore(t)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	k := fakeRestoreClient(newUploadServerFixture(nil))
	dyn := k.Dynamic.(*dynamicfake.FakeDynamicClient)
	dyn.PrependReactor("create", "restores", restoreCreateReactor("Pending", "", "", cancel))
	dyn.PrependReactor("get", "restores", backupMoreFail(errors.New("connection reset")))
	h := newUploadTestHandler(t, store, k)
	rr := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(ctx, http.MethodPost, "/restores:upload?"+validUploadQuery, nil)
	req.ContentLength = int64(len(testUploadBody))
	newUploadTestRouter(h).ServeHTTP(rr, req)
	rows := readBackupAuditRows(t, store)
	if len(rows) != 2 || rows[1].Status != 499 {
		t.Fatalf("audit rows %+v, want intent then 499 correction", rows)
	}
	// Every get fails in this test, so check the recorded delete instead of
	// reading the Restore back.
	deleted := false
	for _, a := range dyn.Actions() {
		if a.GetVerb() == "delete" && a.GetResource().Resource == "restores" {
			deleted = true
		}
	}
	if !deleted {
		t.Fatal("restore not deleted after the client disconnected")
	}
}

func TestRestoreUploadCleanupDeleteFailureStillAnswers(t *testing.T) {
	t.Parallel()
	store := backupMoreStore(t)
	k := fakeRestoreClient(newUploadServerFixture(nil))
	dyn := k.Dynamic.(*dynamicfake.FakeDynamicClient)
	dyn.PrependReactor("create", "restores", restoreCreateReactor("Failed", "", "image pull exploded", nil))
	dyn.PrependReactor("delete", "restores", backupMoreFail(errors.New("apiserver down")))
	h := newUploadTestHandler(t, store, k)
	rr := doUpload(t, newUploadTestRouter(h), validUploadQuery)
	expectBackupRefusal(t, rr, http.StatusBadGateway, "server.restore.upload.failed")
	// The failed delete leaves the Restore in place for the operator to clear.
	if !uploadRestoreExists(t, k, testRestoreName) {
		t.Fatal("restore missing although its delete was refused")
	}
}
