package kube

import (
	"context"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	k8stesting "k8s.io/client-go/testing"
)

func newBackupExportFake(objs ...runtime.Object) *dynamicfake.FakeDynamicClient {
	gvkr := map[schema.GroupVersionResource]string{
		GVRBackupExport: "BackupExportList",
		GVRBackup:       "BackupList",
	}
	return dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), gvkr, objs...)
}

func backupExportObject(name, phase string) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "gameplane.local/v1alpha1",
		"kind":       "BackupExport",
		"metadata":   map[string]any{"name": name, "namespace": "games", "uid": name + "-uid"},
		"spec":       map[string]any{"backupRef": map[string]any{"name": "bk"}, "backupUID": "bk-uid", "format": "zip"},
		"status":     map[string]any{"phase": phase, "serviceName": "svc-" + name},
	}}
}

func TestCreateBackupExportSetsGenerateNameAndNonControllingOwner(t *testing.T) {
	dyn := newBackupExportFake()
	var created *unstructured.Unstructured
	dyn.PrependReactor("create", "backupexports", func(action k8stesting.Action) (bool, runtime.Object, error) {
		obj := action.(k8stesting.CreateAction).GetObject().(*unstructured.Unstructured)
		created = obj.DeepCopy()
		obj.SetName("generated")
		return true, obj, nil
	})
	c := &Client{Dynamic: dyn}
	ttl := int32(600)
	longName := strings.Repeat("a", 60)
	exp, err := c.CreateBackupExport(context.Background(), "games", longName, "bk-uid", "tar.gz", "prod", &ttl)
	if err != nil {
		t.Fatal(err)
	}
	if got := created.GetAnnotations()[BackupExportClusterAnnotation]; got != "prod" {
		t.Fatalf("cluster annotation=%q, want prod", got)
	}
	if exp.Name != "generated" || exp.Spec.BackupUID != "bk-uid" || exp.Spec.Format != "tar.gz" || exp.Spec.BackupRef.Name != longName || exp.Spec.TTLSeconds == nil || *exp.Spec.TTLSeconds != 600 {
		t.Fatalf("unexpected export %+v", exp.Spec)
	}
	if got := created.GetGenerateName(); got != strings.Repeat("a", 40)+"-export-" {
		t.Fatalf("generateName=%q", got)
	}
	owners := created.GetOwnerReferences()
	if len(owners) != 1 || owners[0].Kind != "Backup" || owners[0].Name != longName || owners[0].UID != "bk-uid" {
		t.Fatalf("owners=%+v", owners)
	}
	if owners[0].Controller == nil || *owners[0].Controller || owners[0].BlockOwnerDeletion == nil || *owners[0].BlockOwnerDeletion {
		t.Fatalf("owner must be non-controlling and non-blocking: %+v", owners[0])
	}
}

func TestBackupExportGetDeleteAndCount(t *testing.T) {
	dyn := newBackupExportFake(
		backupExportObject("pending", ""),
		backupExportObject("ready", "Ready"),
		backupExportObject("working", "Pending"),
		backupExportObject("done", "Completed"),
		backupExportObject("failed", "Failed"),
		backupExportObject("expired", "Expired"),
	)
	c := &Client{Dynamic: dyn}
	ctx := context.Background()

	got, err := c.GetBackupExport(ctx, "games", "ready")
	if err != nil || got == nil || got.Status.Phase != BackupExportPhaseReady || got.Status.ServiceName != "svc-ready" || string(got.UID) != "ready-uid" {
		t.Fatalf("get: %+v %v", got, err)
	}
	missing, err := c.GetBackupExport(ctx, "games", "missing")
	if err != nil || missing != nil {
		t.Fatalf("missing export: %+v %v", missing, err)
	}

	n, err := c.CountActiveBackupExports(ctx, "games")
	if err != nil || n != 3 {
		t.Fatalf("active=%d err=%v want 3 (empty, Pending, Ready)", n, err)
	}

	if err := c.DeleteBackupExport(ctx, "games", "ready"); err != nil {
		t.Fatal(err)
	}
	if err := c.DeleteBackupExport(ctx, "games", "ready"); err != nil {
		t.Fatalf("second delete must ignore not found: %v", err)
	}
	n, err = c.CountActiveBackupExports(ctx, "games")
	if err != nil || n != 2 {
		t.Fatalf("active after delete=%d err=%v", n, err)
	}
}

func TestGetBackupReadsServerRefAndNotFound(t *testing.T) {
	bk := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "gameplane.local/v1alpha1",
		"kind":       "Backup",
		"metadata":   map[string]any{"name": "bk", "namespace": "games", "uid": "bk-uid"},
		"spec":       map[string]any{"serverRef": map[string]any{"name": "alpha"}, "strategy": "restic"},
		"status":     map[string]any{"phase": "Succeeded", "snapshotID": "snap"},
	}}
	c := &Client{Dynamic: newBackupExportFake(bk)}
	got, err := c.GetBackup(context.Background(), "games", "bk")
	if err != nil || got == nil || got.Spec.ServerRef.Name != "alpha" || got.Spec.Strategy != "restic" || got.Status.SnapshotID != "snap" || string(got.UID) != "bk-uid" {
		t.Fatalf("get backup: %+v %v", got, err)
	}
	missing, err := c.GetBackup(context.Background(), "games", "nope")
	if err != nil || missing != nil {
		t.Fatalf("missing backup: %+v %v", missing, err)
	}
}
