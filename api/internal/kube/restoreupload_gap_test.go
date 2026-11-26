package kube

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	k8stesting "k8s.io/client-go/testing"
)

func TestGapGetRestoreReportsLookupAndDecodeFailures(t *testing.T) {
	dyn := newRestoreFake()
	dyn.PrependReactor("get", "restores", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, errGapAPIServer
	})
	if rs, err := GetRestore(context.Background(), dyn, "games", "rs-one"); rs != nil || !errors.Is(err, errGapAPIServer) {
		t.Fatalf("lookup rs=%v err=%v", rs, err)
	}

	bad := uploadRestoreObject("rs-bad", "", 1)
	bad.Object["status"] = map[string]any{"phase": int64(5)}
	dyn = newRestoreFake(bad)
	if rs, err := GetRestore(context.Background(), dyn, "games", "rs-bad"); rs != nil || err == nil || !strings.Contains(err.Error(), "convert from unstructured") {
		t.Fatalf("decode rs=%v err=%v", rs, err)
	}
}

func TestGapCreateUploadRestoreReportsApiserverRejection(t *testing.T) {
	dyn := newRestoreFake()
	dyn.PrependReactor("create", "restores", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, errGapAPIServer
	})
	name, uid, err := CreateUploadRestore(context.Background(), dyn, "games", "alpha", "b.zip", "zip", 7, "")
	if name != "" || uid != "" || !errors.Is(err, errGapAPIServer) || !strings.Contains(err.Error(), "create upload restore for alpha") {
		t.Fatalf("name=%q uid=%q err=%v", name, uid, err)
	}
}

func TestGapConfirmUploadRestoreReportsGetAndDecodeFailures(t *testing.T) {
	dyn := newRestoreFake()
	dyn.PrependReactor("get", "restores", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, errGapAPIServer
	})
	if err := ConfirmUploadRestore(context.Background(), dyn, "games", "rs-one", 1); !errors.Is(err, errGapAPIServer) {
		t.Fatalf("get err=%v", err)
	}

	bad := uploadRestoreObject("rs-bad", RestorePhaseAwaitingConfirmation, 1)
	bad.Object["status"] = map[string]any{"phase": int64(5)}
	dyn = newRestoreFake(bad)
	if err := ConfirmUploadRestore(context.Background(), dyn, "games", "rs-bad", 1); err == nil || !strings.Contains(err.Error(), "convert from unstructured") {
		t.Fatalf("decode err=%v", err)
	}
}

// The confirm patch carries the resourceVersion that was read, so a concurrent
// write makes the apiserver refuse it instead of confirming a changed upload.
func TestGapConfirmUploadRestorePatchCarriesReadResourceVersion(t *testing.T) {
	rs := uploadRestoreObject("rs-one", RestorePhaseAwaitingConfirmation, 3)
	rs.SetResourceVersion("41")
	dyn := newRestoreFake(rs)
	var patchBody map[string]any
	dyn.PrependReactor("patch", "restores", func(action k8stesting.Action) (bool, runtime.Object, error) {
		if err := json.Unmarshal(action.(k8stesting.PatchAction).GetPatch(), &patchBody); err != nil {
			t.Errorf("patch is not JSON: %v", err)
		}
		return false, nil, nil
	})
	if err := ConfirmUploadRestore(context.Background(), dyn, "games", "rs-one", 3); err != nil {
		t.Fatal(err)
	}
	meta, _ := patchBody["metadata"].(map[string]any)
	if meta["resourceVersion"] != "41" {
		t.Fatalf("patch=%v, want resourceVersion 41", patchBody)
	}
}

func TestGapConfirmUploadRestoreReportsPatchFailure(t *testing.T) {
	dyn := newRestoreFake(uploadRestoreObject("rs-one", RestorePhaseAwaitingConfirmation, 1))
	dyn.PrependReactor("patch", "restores", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewConflict(GVRRestore.GroupResource(), "rs-one", errGapAPIServer)
	})
	err := ConfirmUploadRestore(context.Background(), dyn, "games", "rs-one", 1)
	if !apierrors.IsConflict(err) || !strings.Contains(err.Error(), "confirm restore rs-one: patch") {
		t.Fatalf("err=%v", err)
	}
}

func TestGapDeleteRestorePropagatesNonNotFoundErrors(t *testing.T) {
	dyn := newRestoreFake(uploadRestoreObject("rs-one", "Receiving", 1))
	dyn.PrependReactor("delete", "restores", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, errGapAPIServer
	})
	if err := DeleteRestore(context.Background(), dyn, "games", "rs-one"); !errors.Is(err, errGapAPIServer) || !strings.Contains(err.Error(), "delete restore rs-one") {
		t.Fatalf("err=%v", err)
	}
	// NotFound stays idempotent even when the reactor reports it.
	dyn.PrependReactor("delete", "restores", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewNotFound(GVRRestore.GroupResource(), "rs-one")
	})
	if err := DeleteRestore(context.Background(), dyn, "games", "rs-one"); err != nil {
		t.Fatalf("not found is not idempotent: %v", err)
	}
}
