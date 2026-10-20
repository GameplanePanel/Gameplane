package controller

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	gameplanev1alpha1 "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
)

func TestByCatalogName(t *testing.T) {
	entries := []gameplanev1alpha1.ModuleEntry{
		{Name: "alpha"},
		{Name: "beta"},
	}
	if got := byCatalogName(entries, "beta"); got == nil || got.Name != "beta" {
		t.Fatalf("got %+v", got)
	}
	if got := byCatalogName(entries, "missing"); got != nil {
		t.Fatalf("expected nil, got %+v", got)
	}
	if got := byCatalogName(nil, "x"); got != nil {
		t.Fatal("nil entries should return nil")
	}
}

func TestOwnedBy(t *testing.T) {
	owner := &gameplanev1alpha1.Module{
		ObjectMeta: metav1.ObjectMeta{
			UID:  types.UID("uid-1"),
			Name: "alpha",
		},
	}
	t.Run("matching ref", func(t *testing.T) {
		obj := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				OwnerReferences: []metav1.OwnerReference{
					{Kind: "Module", UID: "uid-1"},
				},
			},
		}
		if !ownedBy(obj, owner) {
			t.Fatal("expected match")
		}
	})
	t.Run("wrong UID", func(t *testing.T) {
		obj := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				OwnerReferences: []metav1.OwnerReference{
					{Kind: "Module", UID: "uid-other"},
				},
			},
		}
		if ownedBy(obj, owner) {
			t.Fatal("uid mismatch should not match")
		}
	})
	t.Run("wrong Kind", func(t *testing.T) {
		obj := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				OwnerReferences: []metav1.OwnerReference{
					{Kind: "Other", UID: "uid-1"},
				},
			},
		}
		if ownedBy(obj, owner) {
			t.Fatal("Kind mismatch should not match")
		}
	})
	t.Run("no owner refs", func(t *testing.T) {
		obj := &corev1.ConfigMap{}
		if ownedBy(obj, owner) {
			t.Fatal("empty refs should not match")
		}
	})
}

func TestOperatorTooOld(t *testing.T) {
	cases := []struct {
		name       string
		operator   string
		minVersion string
		want       bool
	}{
		{"older operator blocked", "0.1.0", "0.2.0", true},
		{"older operator blocked, v-prefixed", "v0.1.0", "v0.2.0", true},
		{"equal ok", "0.2.0", "0.2.0", false},
		{"newer operator ok", "1.0.0", "0.2.0", false},
		{"no requirement", "0.1.0", "", false},
		{"dev operator skips", "dev", "9.9.9", false},
		{"empty operator skips", "", "9.9.9", false},
		{"unparseable requirement skips", "0.1.0", "not-semver", false},
		{"unparseable operator skips", "weird", "0.2.0", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := &ModuleReconciler{OperatorVersion: tc.operator}
			if got := r.operatorTooOld(tc.minVersion); got != tc.want {
				t.Errorf("operatorTooOld(%q) with operator %q = %v, want %v",
					tc.minVersion, tc.operator, got, tc.want)
			}
		})
	}
}

func TestModuleReconciler_FetcherFor_DefaultPath(t *testing.T) {
	// When the NewFetcher hook is unset, fetcherFor must build a real
	// fetcher from the source spec. No PullSecretRef → no client call.
	r := &ModuleReconciler{}
	src := &gameplanev1alpha1.ModuleSource{
		Spec: gameplanev1alpha1.ModuleSourceSpec{
			Type: gameplanev1alpha1.ModuleSourceTypeOCI,
			OCI: &gameplanev1alpha1.OCISourceSpec{
				URL:     "ghcr.io/test/modules",
				Modules: []gameplanev1alpha1.ModuleRef{{Name: "minecraft-java"}},
			},
		},
	}
	f, err := r.fetcherFor(context.Background(), src)
	if err != nil {
		t.Fatalf("fetcherFor: %v", err)
	}
	if f == nil {
		t.Fatal("expected non-nil fetcher from default path")
	}
}
