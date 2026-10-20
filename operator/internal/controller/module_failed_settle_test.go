package controller

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	gameplanev1alpha1 "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
	"github.com/GameplanePanel/gameplane/operator/internal/modsrc"
	"github.com/GameplanePanel/gameplane/operator/internal/verify"
)

// settleFetcher serves one fixed bundle for every Pull.
type settleFetcher struct{ bundle *modsrc.Bundle }

func (f settleFetcher) Index(context.Context) ([]gameplanev1alpha1.ModuleEntry, []string, error) {
	return nil, nil, nil
}

func (f settleFetcher) Pull(context.Context, string, string) (*modsrc.Bundle, error) {
	return f.bundle, nil
}

// TestModuleReconcile_FailedModuleSettles is the F-258 regression: a Module
// that fails for the same cause at the same generation must not rewrite its
// status on every reconcile. Before the fix, each reconcile flipped it
// Failed → Pulling → Failed (two status writes, each re-queueing the Module
// through its own watch), so its resourceVersion never settled. It must also
// still reach Ready once the cause is fixed.
func TestModuleReconcile_FailedModuleSettles(t *testing.T) {
	ctx := context.Background()
	const bundleDigest = "sha256:mc-1.0.0"

	src := &gameplanev1alpha1.ModuleSource{ObjectMeta: metav1.ObjectMeta{Name: "src"}}
	src.Status.Modules = []gameplanev1alpha1.ModuleEntry{{
		Name:          "mc",
		Reference:     "local/test/mc",
		Versions:      []string{"1.0.0"},
		LatestVersion: "1.0.0",
	}}
	mod := &gameplanev1alpha1.Module{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "mc",
			Generation: 1,
			Finalizers: []string{gameplanev1alpha1.ModuleFinalizer},
		},
		Spec: gameplanev1alpha1.ModuleSpec{
			Source: corev1.LocalObjectReference{Name: "src"},
			Name:   "mc",
			Digest: "sha256:wrong",
		},
	}

	s := scrapeScheme(t)
	cl := fake.NewClientBuilder().WithScheme(s).
		WithObjects(src, mod).
		WithStatusSubresource(&gameplanev1alpha1.Module{}, &gameplanev1alpha1.ModuleSource{}).
		Build()
	r := &ModuleReconciler{
		Client: cl,
		Scheme: s,
		NewFetcher: func(context.Context, *gameplanev1alpha1.ModuleSource) (modsrc.Fetcher, error) {
			return settleFetcher{bundle: &modsrc.Bundle{
				Digest: bundleDigest,
				TemplateYAML: []byte("apiVersion: gameplane.local/v1alpha1\nkind: GameTemplate\n" +
					"spec:\n  displayName: MC\n  game: mc\n  version: 1.0.0\n  image: ghcr.io/test/mc:1.0.0\n"),
			}}, nil
		},
		NewVerifier: func(context.Context, *gameplanev1alpha1.ModuleSource) (verify.Verifier, error) {
			return verify.Nop{}, nil
		},
	}
	req := ctrl.Request{NamespacedName: types.NamespacedName{Name: "mc"}}
	get := func() *gameplanev1alpha1.Module {
		t.Helper()
		got := &gameplanev1alpha1.Module{}
		if err := cl.Get(ctx, req.NamespacedName, got); err != nil {
			t.Fatalf("get module: %v", err)
		}
		return got
	}

	// First reconcile: the digest pin doesn't match, so the Module fails.
	res, err := r.Reconcile(ctx, req)
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if res.RequeueAfter <= 0 {
		t.Errorf("failed reconcile result = %+v, want a paced RequeueAfter", res)
	}
	failed := get()
	if failed.Status.Phase != gameplanev1alpha1.ModulePhaseFailed {
		t.Fatalf("phase = %s, want Failed", failed.Status.Phase)
	}

	// Retries for the same cause at the same generation write nothing.
	for i := range 3 {
		if _, err := r.Reconcile(ctx, req); err != nil {
			t.Fatalf("retry %d: %v", i, err)
		}
		got := get()
		if got.ResourceVersion != failed.ResourceVersion {
			t.Fatalf("retry %d rewrote status: resourceVersion %s -> %s (phase %s)",
				i, failed.ResourceVersion, got.ResourceVersion, got.Status.Phase)
		}
	}

	// Fix the cause: pin the digest the bundle actually has.
	fixed := get()
	fixed.Spec.Digest = bundleDigest
	fixed.Generation = 2
	if err := cl.Update(ctx, fixed); err != nil {
		t.Fatalf("update module: %v", err)
	}
	if _, err := r.Reconcile(ctx, req); err != nil {
		t.Fatalf("reconcile after fix: %v", err)
	}
	ready := get()
	if ready.Status.Phase != gameplanev1alpha1.ModulePhaseReady {
		t.Fatalf("phase after fix = %s (lastError %q), want Ready", ready.Status.Phase, ready.Status.LastError)
	}
	if ready.Status.LastError != "" {
		t.Errorf("lastError after fix = %q, want empty", ready.Status.LastError)
	}
}
