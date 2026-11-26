package kube

import (
	"context"
	"errors"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	k8stesting "k8s.io/client-go/testing"
)

var errGapAPIServer = errors.New("apiserver refused")

func TestGapCreateBackupExportReportsApiserverRejection(t *testing.T) {
	dyn := newBackupExportFake()
	dyn.PrependReactor("create", "backupexports", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, errGapAPIServer
	})
	c := &Client{Dynamic: dyn}
	exp, err := c.CreateBackupExport(context.Background(), "games", "bk", "bk-uid", "zip", "prod", nil)
	if exp != nil || !errors.Is(err, errGapAPIServer) || !strings.Contains(err.Error(), "create backup export for bk") {
		t.Fatalf("exp=%v err=%v", exp, err)
	}
}

func TestGapCreateBackupExportRejectsUnreadableCreateResponse(t *testing.T) {
	dyn := newBackupExportFake()
	dyn.PrependReactor("create", "backupexports", func(action k8stesting.Action) (bool, runtime.Object, error) {
		obj := action.(k8stesting.CreateAction).GetObject().(*unstructured.Unstructured)
		obj.SetName("generated")
		// A phase that is not a string cannot be read back into the mirror type.
		obj.Object["status"] = map[string]any{"phase": int64(5)}
		return true, obj, nil
	})
	c := &Client{Dynamic: dyn}
	exp, err := c.CreateBackupExport(context.Background(), "games", "bk", "bk-uid", "zip", "prod", nil)
	if exp != nil || err == nil || !strings.Contains(err.Error(), "convert from unstructured") {
		t.Fatalf("exp=%v err=%v", exp, err)
	}
}

func TestGapGetBackupExportReportsLookupAndDecodeFailures(t *testing.T) {
	dyn := newBackupExportFake()
	dyn.PrependReactor("get", "backupexports", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, errGapAPIServer
	})
	c := &Client{Dynamic: dyn}
	if exp, err := c.GetBackupExport(context.Background(), "games", "ready"); exp != nil || !errors.Is(err, errGapAPIServer) {
		t.Fatalf("lookup exp=%v err=%v", exp, err)
	}

	dyn = newBackupExportFake()
	dyn.PrependReactor("get", "backupexports", func(k8stesting.Action) (bool, runtime.Object, error) {
		obj := backupExportObject("ready", "Ready")
		obj.Object["status"] = map[string]any{"phase": int64(5)}
		return true, obj, nil
	})
	c = &Client{Dynamic: dyn}
	if exp, err := c.GetBackupExport(context.Background(), "games", "ready"); exp != nil || err == nil || !strings.Contains(err.Error(), "convert from unstructured") {
		t.Fatalf("decode exp=%v err=%v", exp, err)
	}
}

func TestGapGetBackupReportsLookupAndDecodeFailures(t *testing.T) {
	dyn := newBackupExportFake()
	dyn.PrependReactor("get", "backups", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, errGapAPIServer
	})
	c := &Client{Dynamic: dyn}
	if b, err := c.GetBackup(context.Background(), "games", "bk"); b != nil || !errors.Is(err, errGapAPIServer) {
		t.Fatalf("lookup backup=%v err=%v", b, err)
	}

	dyn = newBackupExportFake()
	dyn.PrependReactor("get", "backups", func(k8stesting.Action) (bool, runtime.Object, error) {
		obj := &unstructured.Unstructured{Object: map[string]any{
			"apiVersion": "gameplane.local/v1alpha1",
			"kind":       "Backup",
			"metadata":   map[string]any{"name": "bk", "namespace": "games"},
			// serverRef must be an object; a bare string cannot decode.
			"spec": map[string]any{"serverRef": "alpha"},
		}}
		return true, obj, nil
	})
	c = &Client{Dynamic: dyn}
	if b, err := c.GetBackup(context.Background(), "games", "bk"); b != nil || err == nil || !strings.Contains(err.Error(), "convert from unstructured") {
		t.Fatalf("decode backup=%v err=%v", b, err)
	}
}

func TestGapDeleteBackupExportPropagatesNonNotFoundErrors(t *testing.T) {
	dyn := newBackupExportFake(backupExportObject("ready", "Ready"))
	dyn.PrependReactor("delete", "backupexports", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, errGapAPIServer
	})
	c := &Client{Dynamic: dyn}
	err := c.DeleteBackupExport(context.Background(), "games", "ready")
	if !errors.Is(err, errGapAPIServer) || !strings.Contains(err.Error(), "delete backup export ready") {
		t.Fatalf("err=%v", err)
	}
}

func TestGapCountActiveBackupExportsReportsListFailures(t *testing.T) {
	dyn := newBackupExportFake()
	dyn.PrependReactor("list", "backupexports", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, errGapAPIServer
	})
	c := &Client{Dynamic: dyn}
	n, err := c.CountActiveBackupExports(context.Background(), "games")
	if n != 0 || !errors.Is(err, errGapAPIServer) || !strings.Contains(err.Error(), "list backup exports in games") {
		t.Fatalf("n=%d err=%v", n, err)
	}
}

func TestGapCountActiveBackupExportsRejectsUndecodableItem(t *testing.T) {
	bad := backupExportObject("broken", "")
	bad.Object["status"] = map[string]any{"phase": int64(5)}
	dyn := newBackupExportFake(backupExportObject("ready", "Ready"), bad)
	c := &Client{Dynamic: dyn}
	n, err := c.CountActiveBackupExports(context.Background(), "games")
	if n != 0 || err == nil || !strings.Contains(err.Error(), "convert from unstructured") {
		t.Fatalf("n=%d err=%v", n, err)
	}
}
