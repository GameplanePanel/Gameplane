package controller

import (
	"context"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	gameplanev1alpha1 "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
)

func TestRefreshInterval(t *testing.T) {
	t.Run("zero falls back to default 1h", func(t *testing.T) {
		src := &gameplanev1alpha1.ModuleSource{}
		if got := refreshInterval(src); got != defaultRefreshInterval {
			t.Fatalf("got %v want %v", got, defaultRefreshInterval)
		}
	})

	t.Run("below the floor clamps to minimum", func(t *testing.T) {
		src := &gameplanev1alpha1.ModuleSource{
			Spec: gameplanev1alpha1.ModuleSourceSpec{
				RefreshInterval: metav1.Duration{Duration: 10 * time.Second},
			},
		}
		if got := refreshInterval(src); got != minRefreshInterval {
			t.Fatalf("got %v want %v", got, minRefreshInterval)
		}
	})

	t.Run("normal value passes through", func(t *testing.T) {
		src := &gameplanev1alpha1.ModuleSource{
			Spec: gameplanev1alpha1.ModuleSourceSpec{
				RefreshInterval: metav1.Duration{Duration: 5 * time.Minute},
			},
		}
		if got := refreshInterval(src); got != 5*time.Minute {
			t.Fatalf("got %v want 5m", got)
		}
	})
}

func TestModuleSourceReconciler_FetcherFor_DefaultPath(t *testing.T) {
	r := &ModuleSourceReconciler{}
	src := &gameplanev1alpha1.ModuleSource{
		Spec: gameplanev1alpha1.ModuleSourceSpec{
			Type: gameplanev1alpha1.ModuleSourceTypeOCI,
			OCI: &gameplanev1alpha1.OCISourceSpec{
				URL:     "localhost:5001/modules",
				Modules: []gameplanev1alpha1.ModuleRef{{Name: "valheim"}},
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
