package handlers

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/GameplanePanel/gameplane/api/internal/auth"
	"github.com/GameplanePanel/gameplane/api/internal/kube"
	"github.com/GameplanePanel/gameplane/api/internal/scope"
)

func mountLifecycleRouter(k *kube.Client) *chi.Mux {
	r := chi.NewRouter()
	reg := kube.NewRegistry(scope.DefaultCluster)
	reg.Set(scope.DefaultCluster, k)
	MountLifecycle(r, reg)
	return r
}

func newServerObj(ns, name string) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "gameplane.local/v1alpha1",
		"kind":       "GameServer",
		"metadata":   map[string]any{"name": name, "namespace": ns},
		"spec":       map[string]any{"templateRef": map[string]any{"name": "minecraft"}},
	}}
}

func TestLifecycle_StartStop(t *testing.T) {
	k := fakeKubeClient(lifecycleSuspendServer())
	r := mountLifecycleRouter(k)

	for _, verb := range []string{"start", "stop"} {
		rr := do(t, r, "POST", "/servers/alpha:"+verb, nil)
		if rr.Code != 202 {
			t.Fatalf("%s: %d %s", verb, rr.Code, rr.Body)
		}
	}
}

func TestLifecycle_Restart(t *testing.T) {
	k := fakeKubeClient(newServerObj("gameplane-games", "alpha"))
	r := mountLifecycleRouter(k)
	rr := do(t, r, "POST", "/servers/alpha:restart", nil)
	if rr.Code != 202 {
		t.Fatalf("got %d %s", rr.Code, rr.Body)
	}

	obj, err := k.Dynamic.Resource(kube.GVRs["servers"]).
		Namespace("gameplane-games").Get(t.Context(), "alpha", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if obj.GetAnnotations()[restartRequestedAnnotation] == "" {
		t.Errorf("expected restart-requested annotation, got %v", obj.GetAnnotations())
	}
	// The operator owns the recycle; the handler must not touch spec.suspend.
	if suspend, _, _ := unstructured.NestedBool(obj.Object, "spec", "suspend"); suspend {
		t.Error("restart must not set spec.suspend")
	}
}

func TestLifecycle_Wake(t *testing.T) {
	k := fakeKubeClient(newServerObj("gameplane-games", "alpha"))
	r := mountLifecycleRouter(k)
	rr := do(t, r, "POST", "/servers/alpha:wake", nil)
	if rr.Code != 202 {
		t.Fatalf("got %d %s", rr.Code, rr.Body)
	}

	obj, err := k.Dynamic.Resource(kube.GVRs["servers"]).
		Namespace("gameplane-games").Get(t.Context(), "alpha", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if obj.GetAnnotations()[idleWakeRequestedAnnotation] == "" {
		t.Errorf("expected idle-wake-requested annotation, got %v", obj.GetAnnotations())
	}
	// The operator owns the sleep marker and spec.suspend belongs to the user.
	// Waking must stamp a request and nothing else — clearing suspend here
	// would resume a server its owner had deliberately stopped.
	if suspend, found, _ := unstructured.NestedBool(obj.Object, "spec", "suspend"); found && suspend {
		t.Error("wake must not touch spec.suspend")
	}
}

func TestLifecycle_StartUnknown(t *testing.T) {
	k := fakeKubeClient()
	r := mountLifecycleRouter(k)
	rr := do(t, r, "POST", "/servers/missing:start", nil)
	if rr.Code == 202 {
		t.Fatal("missing server should not patch successfully")
	}
}

func TestLifecycle_WipeData(t *testing.T) {
	k := fakeKubeClient(newServerObj("gameplane-games", "alpha"))
	r := mountLifecycleRouter(k)

	t.Run("requires matching confirmation", func(t *testing.T) {
		rr := doWithUser(t, r, "POST", "/servers/alpha:wipe-data", map[string]any{"confirm": "wrong"}, testAdminUser())
		if rr.Code != 400 {
			t.Fatalf("got %d %s", rr.Code, rr.Body)
		}
	})

	t.Run("suspends and stamps the wipe annotation", func(t *testing.T) {
		rr := doWithUser(t, r, "POST", "/servers/alpha:wipe-data", map[string]any{"confirm": "alpha"}, testAdminUser())
		if rr.Code != 202 {
			t.Fatalf("got %d %s", rr.Code, rr.Body)
		}
		obj, err := k.Dynamic.Resource(kube.GVRs["servers"]).
			Namespace("gameplane-games").Get(t.Context(), "alpha", metav1.GetOptions{})
		if err != nil {
			t.Fatalf("get: %v", err)
		}
		suspend, _, _ := unstructured.NestedBool(obj.Object, "spec", "suspend")
		if !suspend {
			t.Error("expected suspend=true after wipe request")
		}
		ann := obj.GetAnnotations()
		if ann[wipeRequestedAnnotation] == "" {
			t.Errorf("expected wipe-requested annotation, got %v", ann)
		}
	})
}

func TestLifecycle_Clone(t *testing.T) {
	k := fakeKubeClient(newServerObj("gameplane-games", "alpha"))
	r := mountLifecycleRouter(k)

	t.Run("happy path", func(t *testing.T) {
		rr := do(t, r, "POST", "/servers/alpha:clone", map[string]any{"newName": "beta"})
		if rr.Code != 200 {
			t.Fatalf("got %d %s", rr.Code, rr.Body)
		}
	})

	t.Run("missing newName", func(t *testing.T) {
		rr := do(t, r, "POST", "/servers/alpha:clone", map[string]any{})
		if rr.Code != 400 {
			t.Fatalf("got %d", rr.Code)
		}
	})

	t.Run("bad json", func(t *testing.T) {
		req := httptest.NewRequestWithContext(t.Context(), "POST", "/servers/alpha:clone", strings.NewReader("bogus"))
		rr := httptest.NewRecorder()
		r.ServeHTTP(rr, req)
		if rr.Code != 400 {
			t.Fatalf("got %d", rr.Code)
		}
	})

	t.Run("source missing", func(t *testing.T) {
		rr := do(t, r, "POST", "/servers/ghost:clone", map[string]any{"newName": "z"})
		if rr.Code == 200 {
			t.Fatal("ghost source should not clone")
		}
	})
}

// TestLifecycle_CloneRestampsOwner verifies a clone is stamped with the
// caller as owner and carries none of the source's server-managed metadata
// (owner/lifecycle annotations, finalizers, ownerReferences).
func TestLifecycle_CloneRestampsOwner(t *testing.T) {
	src := newServerObj("gameplane-games", "alpha")
	src.SetAnnotations(map[string]string{
		ownerIDAnnotation:       "1",
		ownerAnnotation:         "alice",
		wipeRequestedAnnotation: "true",
	})
	src.SetFinalizers([]string{"gameplane.local/cleanup"})
	src.SetOwnerReferences([]metav1.OwnerReference{{
		APIVersion: "gameplane.local/v1alpha1", Kind: "GameServer", Name: "owner", UID: "uid-1",
	}})
	k := fakeKubeClient(src)
	r := mountLifecycleRouter(k)

	body, _ := json.Marshal(map[string]any{"newName": "beta"})
	req := httptest.NewRequestWithContext(t.Context(), "POST", "/servers/alpha:clone", bytes.NewReader(body))
	req = req.WithContext(auth.WithUser(req.Context(), &auth.User{ID: 2, Username: "bob"}))
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != 200 {
		t.Fatalf("clone: %d %s", rr.Code, rr.Body)
	}

	clone, err := k.Dynamic.Resource(kube.GVRs["servers"]).
		Namespace("gameplane-games").Get(t.Context(), "beta", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get clone: %v", err)
	}
	ann := clone.GetAnnotations()
	if ann[ownerAnnotation] != "bob" || ann[ownerIDAnnotation] != "2" {
		t.Errorf("clone owner = %v, want bob/2", ann)
	}
	if _, ok := ann[wipeRequestedAnnotation]; ok {
		t.Errorf("clone carried source lifecycle annotation: %v", ann)
	}
	if len(clone.GetFinalizers()) != 0 {
		t.Errorf("clone carried source finalizers: %v", clone.GetFinalizers())
	}
	if len(clone.GetOwnerReferences()) != 0 {
		t.Errorf("clone carried source ownerReferences: %v", clone.GetOwnerReferences())
	}
}
