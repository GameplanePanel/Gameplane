//go:build envtest

package controller

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/manager"

	gameplanev1alpha1 "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
	"github.com/GameplanePanel/gameplane/operator/internal/modsrc"
)

type slowIndexFetcher struct {
	modsrc.Fetcher
	calls *atomic.Int32
}

func (f slowIndexFetcher) Index(ctx context.Context) ([]gameplanev1alpha1.ModuleEntry, []string, error) {
	f.calls.Add(1)
	// LastSync uses whole seconds on the wire. Cross that boundary so an
	// accidental status-triggered reconcile cannot hide behind an equal timestamp.
	select {
	case <-time.After(1100 * time.Millisecond):
	case <-ctx.Done():
		return nil, nil, ctx.Err()
	}
	return f.Fetcher.Index(ctx)
}

func TestModuleSourceStatusDoesNotTriggerAnotherIndex(t *testing.T) {
	fake := newFakeOCI()
	fake.putBundle("local/test/game", "1.0.0", fixtureBundle("game", "1.0.0", "Game"))
	name := uniqueName("refresh")
	var calls atomic.Int32
	startMgr(t, "gameplane-system", func(mgr manager.Manager) error {
		return (&ModuleSourceReconciler{
			Client: mgr.GetClient(), Scheme: mgr.GetScheme(),
			NewFetcher: func(ctx context.Context, src *gameplanev1alpha1.ModuleSource) (modsrc.Fetcher, error) {
				f, err := fakeOCIFetcher(fake)(ctx, src)
				if src.Name == name && err == nil {
					return slowIndexFetcher{Fetcher: f, calls: &calls}, nil
				}
				return f, err
			},
		}).SetupWithManager(mgr)
	})
	src := &gameplanev1alpha1.ModuleSource{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec: gameplanev1alpha1.ModuleSourceSpec{
			Type:            gameplanev1alpha1.ModuleSourceTypeOCI,
			OCI:             &gameplanev1alpha1.OCISourceSpec{URL: "local/test", Modules: []gameplanev1alpha1.ModuleRef{{Name: "game"}}},
			RefreshInterval: metav1.Duration{Duration: time.Hour},
		},
	}
	if err := k8sClient.Create(t.Context(), src); err != nil {
		t.Fatal(err)
	}
	deleteCleanup(t, src)
	eventually(t, func() (bool, string) {
		return getModuleSource(t, name).Status.LastSync != nil, "waiting for first index"
	})
	consistently(t, 3*time.Second, func() (bool, string) {
		return calls.Load() == 1, fmt.Sprintf("indexed %d times before refresh was due", calls.Load())
	})
	patchSourceAnnotation(t, name, "gameplane.local/test-nudge", "1")
	eventually(t, func() (bool, string) {
		return calls.Load() == 2, "annotation change did not trigger an index"
	})
}
