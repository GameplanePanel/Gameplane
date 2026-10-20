package handlers

import (
	"net/http"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kubefake "k8s.io/client-go/kubernetes/fake"

	"github.com/GameplanePanel/gameplane/api/internal/kube"
)

// TestDestinations_Upsert_RotatesPasswordOnExisting confirms the
// AlreadyExists → patch branch.
func TestDestinations_Upsert_RotatesPasswordOnExisting(t *testing.T) {
	pre := newDestSecret("gameplane-games", "default")
	k := &kube.Client{
		Dynamic: fakeKubeClient().Dynamic,
		Typed:   kubefake.NewClientset(pre),
	}
	r := mountDestRouter(k)
	body := map[string]any{"name": "default", "url": "s3:rotated", "password": "newpw"}
	rr := do(t, r, "POST", "/backup-destinations/", body)
	if rr.Code != 200 {
		t.Fatalf("rotate: got %d %s", rr.Code, rr.Body)
	}
}

// TestDestinations_Upsert_ConflictsWithNonGameplaneSecret returns a clear
// error when a same-named, unlabelled Secret already exists.
func TestDestinations_Upsert_ConflictsWithNonGameplaneSecret(t *testing.T) {
	stranger := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "default",
			Namespace: "gameplane-games",
		},
	}
	k := &kube.Client{
		Dynamic: fakeKubeClient().Dynamic,
		Typed:   kubefake.NewClientset(stranger),
	}
	r := mountDestRouter(k)
	body := map[string]any{"name": "default", "url": "s3:x", "password": "p"}
	rr := do(t, r, "POST", "/backup-destinations/", body)
	if rr.Code == http.StatusOK {
		t.Fatalf("expected non-200 when a non-Gameplane Secret blocks the name")
	}
}

// TestDestinations_Upsert_MissingPassword exercises the required-fields
// branch.
func TestDestinations_Upsert_MissingPassword(t *testing.T) {
	k := &kube.Client{
		Dynamic: fakeKubeClient().Dynamic,
		Typed:   kubefake.NewClientset(),
	}
	r := mountDestRouter(k)
	body := map[string]any{"name": "x", "url": "s3:x"}
	rr := do(t, r, "POST", "/backup-destinations/", body)
	if rr.Code == http.StatusOK {
		t.Fatal("missing password should fail")
	}
}
