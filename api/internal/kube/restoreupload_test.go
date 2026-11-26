package kube

import (
	"context"
	"strings"
	"testing"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	k8stesting "k8s.io/client-go/testing"
)

func newRestoreFake(objs ...runtime.Object) *dynamicfake.FakeDynamicClient {
	return dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), map[schema.GroupVersionResource]string{
		GVRRestore: "RestoreList",
	}, objs...)
}

func uploadRestoreObject(name, phase string, generation int64) *unstructured.Unstructured {
	status := map[string]any{"phase": phase}
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "gameplane.local/v1alpha1",
		"kind":       "Restore",
		"metadata":   map[string]any{"name": name, "namespace": "games", "uid": name + "-uid", "generation": generation},
		"spec": map[string]any{
			"serverRef": map[string]any{"name": "alpha"},
			"upload":    map[string]any{"fileName": "b.zip", "format": "zip", "declaredBytes": int64(7)},
		},
		"status": status,
	}}
}

func TestCreateUploadRestoreSetsGenerateNameAndUploadSpec(t *testing.T) {
	dyn := newRestoreFake()
	var created *unstructured.Unstructured
	dyn.PrependReactor("create", "restores", func(action k8stesting.Action) (bool, runtime.Object, error) {
		obj := action.(k8stesting.CreateAction).GetObject().(*unstructured.Unstructured)
		created = obj.DeepCopy()
		obj.SetName("generated")
		obj.SetUID("generated-uid")
		return true, obj, nil
	})
	name, uid, err := CreateUploadRestore(context.Background(), dyn, "games", strings.Repeat("a", 60), "b.tar.gz", "tar.gz", 42, "")
	if err != nil {
		t.Fatal(err)
	}
	if name != "generated" || uid != "generated-uid" {
		t.Fatalf("name=%q uid=%q", name, uid)
	}
	if got := created.GetGenerateName(); got != strings.Repeat("a", 40)+"-restore-" {
		t.Fatalf("generateName=%q", got)
	}
	spec, _ := created.Object["spec"].(map[string]any)
	if _, has := spec["backupRef"]; has {
		t.Fatal("upload restore carries a backupRef")
	}
	upload, _ := spec["upload"].(map[string]any)
	if upload["fileName"] != "b.tar.gz" || upload["format"] != "tar.gz" || upload["declaredBytes"] != int64(42) {
		t.Fatalf("upload=%v", upload)
	}
	if _, has := upload["confirmedTemplate"]; has {
		t.Fatal("empty template was written")
	}
	if sr, _ := spec["serverRef"].(map[string]any); sr["name"] != strings.Repeat("a", 60) {
		t.Fatalf("serverRef=%v", sr)
	}
}

func TestCreateUploadRestoreWritesPlainTemplate(t *testing.T) {
	dyn := newRestoreFake()
	var created *unstructured.Unstructured
	dyn.PrependReactor("create", "restores", func(action k8stesting.Action) (bool, runtime.Object, error) {
		created = action.(k8stesting.CreateAction).GetObject().(*unstructured.Unstructured).DeepCopy()
		return false, nil, nil
	})
	if _, _, err := CreateUploadRestore(context.Background(), dyn, "games", "alpha", "x.zip", "zip", 9, "minecraft"); err != nil {
		t.Fatal(err)
	}
	upload, _ := created.Object["spec"].(map[string]any)["upload"].(map[string]any)
	if upload["confirmedTemplate"] != "minecraft" {
		t.Fatalf("upload=%v", upload)
	}
}

func TestCreateUploadRestoreRejectsInvalidInputBeforeCalling(t *testing.T) {
	dyn := newRestoreFake()
	dyn.PrependReactor("create", "restores", func(k8stesting.Action) (bool, runtime.Object, error) {
		t.Fatal("invalid upload reached the apiserver")
		return true, nil, nil
	})
	for name, call := range map[string]func() error{
		"empty file name": func() error {
			_, _, err := CreateUploadRestore(context.Background(), dyn, "games", "alpha", "", "zip", 1, "")
			return err
		},
		"bad format": func() error {
			_, _, err := CreateUploadRestore(context.Background(), dyn, "games", "alpha", "x", "rar", 1, "")
			return err
		},
		"zero bytes": func() error {
			_, _, err := CreateUploadRestore(context.Background(), dyn, "games", "alpha", "x", "zip", 0, "")
			return err
		},
	} {
		t.Run(name, func(t *testing.T) {
			if err := call(); err == nil {
				t.Fatal("accepted invalid upload")
			}
		})
	}
}

func TestConfirmUploadRestoreRequiresAwaitingConfirmationAtGeneration(t *testing.T) {
	for _, tc := range []struct {
		name       string
		phase      string
		generation int64
		want       func(error) bool
	}{
		{"still receiving", "Receiving", 3, apierrors.IsConflict},
		{"generation moved", "AwaitingConfirmation", 4, apierrors.IsConflict},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dyn := newRestoreFake(uploadRestoreObject("rs", tc.phase, 3))
			err := ConfirmUploadRestore(context.Background(), dyn, "games", "rs", tc.generation)
			if err == nil || !tc.want(err) {
				t.Fatalf("err=%v", err)
			}
			got, err := GetRestore(context.Background(), dyn, "games", "rs")
			if err != nil || got == nil || got.Spec.Upload == nil || got.Spec.Upload.Confirmed {
				t.Fatalf("restore was changed by a refused confirm: %+v %v", got, err)
			}
		})
	}
}

func TestConfirmUploadRestoreSetsConfirmedOnce(t *testing.T) {
	dyn := newRestoreFake(uploadRestoreObject("rs", "AwaitingConfirmation", 3))
	if err := ConfirmUploadRestore(context.Background(), dyn, "games", "rs", 3); err != nil {
		t.Fatal(err)
	}
	got, err := GetRestore(context.Background(), dyn, "games", "rs")
	if err != nil || got == nil || got.Spec.Upload == nil || !got.Spec.Upload.Confirmed {
		t.Fatalf("not confirmed: %+v %v", got, err)
	}
	if got.Spec.Upload.FileName != "b.zip" || got.Spec.Upload.DeclaredBytes != 7 {
		t.Fatalf("other upload fields changed: %+v", got.Spec.Upload)
	}
}

func TestConfirmUploadRestoreMissingIsNotFound(t *testing.T) {
	dyn := newRestoreFake()
	if err := ConfirmUploadRestore(context.Background(), dyn, "games", "missing", 1); !apierrors.IsNotFound(err) {
		t.Fatalf("err=%v", err)
	}
}

func TestGetRestoreMissingIsNilAndDeleteRestoreIsIdempotent(t *testing.T) {
	dyn := newRestoreFake(uploadRestoreObject("rs", "Receiving", 1))
	if got, err := GetRestore(context.Background(), dyn, "games", "absent"); err != nil || got != nil {
		t.Fatalf("missing: got=%v err=%v", got, err)
	}
	if err := DeleteRestore(context.Background(), dyn, "games", "rs"); err != nil {
		t.Fatal(err)
	}
	if err := DeleteRestore(context.Background(), dyn, "games", "rs"); err != nil {
		t.Fatalf("second delete: %v", err)
	}
	if got, err := GetRestore(context.Background(), dyn, "games", "rs"); err != nil || got != nil {
		t.Fatalf("after delete: got=%v err=%v", got, err)
	}
}
