package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/go-chi/chi/v5"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/validation/field"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	kubefake "k8s.io/client-go/kubernetes/fake"
	ktesting "k8s.io/client-go/testing"

	"github.com/GameplanePanel/gameplane/api/internal/auth"
	"github.com/GameplanePanel/gameplane/api/internal/kube"
	"github.com/GameplanePanel/gameplane/api/internal/rbac"
	"github.com/GameplanePanel/gameplane/api/internal/scope"
)

// The middleware sees the authorized snapshot; handlers see the live object.
// This models deletion/recreation between the authorization and handler reads.
type identitySnapshotFetcher struct{ server *unstructured.Unstructured }

func (f identitySnapshotFetcher) IDs() []string { return []string{scope.DefaultCluster} }
func (f identitySnapshotFetcher) GetServer(context.Context, string, string, string) (*unstructured.Unstructured, error) {
	return f.server.DeepCopy(), nil
}

func identityServer(uid types.UID) *unstructured.Unstructured {
	gs := newServerObj(scope.DefaultNamespace, "alpha")
	gs.SetUID(uid)
	gs.SetResourceVersion("10")
	gs.SetAnnotations(map[string]string{ownerIDAnnotation: "42"})
	return gs
}

func identityRouter(k *kube.Client, authorized *unstructured.Unstructured) http.Handler {
	reg := kube.NewRegistry(scope.DefaultCluster)
	reg.Set(scope.DefaultCluster, k)
	r := chi.NewRouter()
	r.Use(rbac.Middleware(identitySnapshotFetcher{authorized}))
	MountResources(r, reg, nil)
	MountLifecycle(r, reg)
	MountTunnelCredentials(r, reg)
	return r
}

func TestServerIdentity_ReplacedServerRejected(t *testing.T) {
	for _, tc := range []struct {
		method, suffix string
		body           any
	}{
		{"GET", "", nil},
		{"DELETE", "", nil},
		{"PUT", ":tunnel-credentials", putReq{Provider: "frp", Values: map[string]string{"token": "new"}}},
		{"GET", ":tunnel-credentials", nil},
		{"DELETE", ":tunnel-credentials", nil},
		{"POST", ":start", nil},
		{"POST", ":stop", nil},
		{"POST", ":restart", nil},
		{"POST", ":wake", nil},
		{"POST", ":clone", cloneReq{NewName: "copy"}},
		{"POST", ":wipe-data", wipeDataReq{Confirm: "alpha"}},
	} {
		t.Run(tc.method+tc.suffix, func(t *testing.T) {
			authorized, live := identityServer("authorized"), identityServer("replacement")
			k := fakeKubeClient(live)
			rr := doWithUser(t, identityRouter(k, authorized), tc.method, "/servers/alpha"+tc.suffix, tc.body, &auth.User{ID: 42})
			if rr.Code != http.StatusNotFound {
				t.Fatalf("got %d %s, want 404 for a replacement", rr.Code, rr.Body)
			}
			for _, a := range k.Dynamic.(*dynamicfake.FakeDynamicClient).Actions() {
				if a.GetVerb() != "get" {
					t.Fatalf("replacement request performed %s", a.GetVerb())
				}
			}
			if actions := k.Typed.(*kubefake.Clientset).Actions(); len(actions) != 0 {
				t.Fatalf("replacement request accessed Secrets: %v", actions)
			}
		})
	}
}

func TestServerIdentity_CurrentReadSucceeds(t *testing.T) {
	for _, tc := range []struct {
		name string
		user *auth.User
		uid  types.UID
	}{
		{"owner", &auth.User{ID: 42}, "authorized"},
		{"namespace-admin", testAdminUser(), "replacement"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			k := fakeKubeClient(identityServer(tc.uid))
			rr := doWithUser(t, identityRouter(k, identityServer("authorized")), "GET", "/servers/alpha", nil, tc.user)
			if rr.Code != http.StatusOK {
				t.Fatalf("got %d %s", rr.Code, rr.Body)
			}
			var got unstructured.Unstructured
			if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil || got.GetUID() != tc.uid {
				t.Fatalf("unexpected read: uid %q, error %v", got.GetUID(), err)
			}
		})
	}
}

func TestShareStart_ConditionsCheckedServerAndRejectsReplacement(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
	}{
		{"resource-version", apierrors.NewConflict(kube.GVRs["servers"].GroupResource(), "alpha", errors.New("recreated after read"))},
		{"uid", apierrors.NewInvalid(schema.GroupKind{Group: kube.GVRs["servers"].Group, Kind: "GameServer"}, "alpha", field.ErrorList{field.Invalid(field.NewPath("metadata", "uid"), "authorized", "immutable")})},
		{"deleted", apierrors.NewNotFound(kube.GVRs["servers"].GroupResource(), "alpha")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := newTestStore(t)
			ownerID := insertShareTestUser(t, store, "share-identity-"+tc.name)
			token, _, err := store.CreateShareLinkForServer(t.Context(), scope.DefaultCluster, scope.DefaultNamespace, "alpha", "authorized", ownerID, true, nil)
			if err != nil {
				t.Fatal(err)
			}
			k := fakeKubeClient(identityServer("authorized"))
			dyn := k.Dynamic.(*dynamicfake.FakeDynamicClient)
			patches := 0
			dyn.PrependReactor("patch", "gameservers", func(a ktesting.Action) (bool, runtime.Object, error) {
				patches++
				assertIdentityPatch(t, a, "authorized", "10")
				replacement := identityServer("replacement")
				replacement.SetResourceVersion("11")
				if err := dyn.Tracker().Update(kube.GVRs["servers"], replacement, scope.DefaultNamespace); err != nil {
					t.Fatal(err)
				}
				return true, nil, tc.err
			})
			reg := kube.NewRegistry(scope.DefaultCluster)
			reg.Set(scope.DefaultCluster, k)
			status, body := shareReq(t, mountSharesRouter(reg, store), "POST", "/shares/"+token+"/start", nil, nil, "203.0.113.201:1234")
			if status != http.StatusNotFound || !bytes.Equal(body, []byte("{\"error\":\"not found\"}\n")) || patches != 1 {
				t.Fatalf("got %d %s, patches %d", status, body, patches)
			}
			live, err := k.GetServer(t.Context(), scope.DefaultNamespace, "alpha")
			if err != nil || live.GetUID() != "replacement" || live.GetAnnotations()[idleWakeRequestedAnnotation] != "" {
				t.Fatalf("replacement modified: %v, error %v", live, err)
			}
		})
	}
}

func TestServerIdentity_CurrentLifecycleSucceeds(t *testing.T) {
	for _, suffix := range []string{":start", ":stop", ":restart", ":wake"} {
		t.Run(suffix, func(t *testing.T) {
			gs := identityServer("authorized")
			k := fakeKubeClient(gs)
			rr := doWithUser(t, identityRouter(k, gs), "POST", "/servers/alpha"+suffix, nil, &auth.User{ID: 42})
			if rr.Code != http.StatusAccepted {
				t.Fatalf("got %d %s", rr.Code, rr.Body)
			}
			live, err := k.Dynamic.Resource(kube.GVRs["servers"]).Namespace(scope.DefaultNamespace).Get(t.Context(), "alpha", metav1.GetOptions{})
			if err != nil {
				t.Fatal(err)
			}
			switch suffix {
			case ":start", ":stop":
				v, found, err := unstructured.NestedBool(live.Object, "spec", "suspend")
				if err != nil || !found || v != (suffix == ":stop") {
					t.Fatalf("unexpected suspend: %v %v %v", v, found, err)
				}
			case ":restart":
				if live.GetAnnotations()[restartRequestedAnnotation] == "" {
					t.Fatal("missing restart request")
				}
			case ":wake":
				if live.GetAnnotations()[idleWakeRequestedAnnotation] == "" {
					t.Fatal("missing wake request")
				}
			}
		})
	}
}

func TestServerIdentity_CurrentCloneAndDeleteSucceed(t *testing.T) {
	for _, tc := range []struct {
		method, suffix string
		body           any
		status         int
	}{
		{"POST", ":clone", cloneReq{NewName: "copy"}, http.StatusOK},
		{"DELETE", "", nil, http.StatusNoContent},
	} {
		t.Run(tc.method+tc.suffix, func(t *testing.T) {
			gs := identityServer("authorized")
			k := fakeKubeClient(gs)
			rr := doWithUser(t, identityRouter(k, gs), tc.method, "/servers/alpha"+tc.suffix, tc.body, &auth.User{ID: 42})
			if rr.Code != tc.status {
				t.Fatalf("got %d %s", rr.Code, rr.Body)
			}
			if tc.method == "POST" {
				cloned, err := k.GetServer(t.Context(), scope.DefaultNamespace, "copy")
				if err != nil || cloned.GetAnnotations()[ownerIDAnnotation] != "42" {
					t.Fatalf("clone failed: %v %v", cloned, err)
				}
			} else if _, err := k.GetServer(t.Context(), scope.DefaultNamespace, "alpha"); !apierrors.IsNotFound(err) {
				t.Fatalf("delete did not remove current server: %v", err)
			}
		})
	}
}

func TestServerIdentity_OwnerRetryAllowsSameUID(t *testing.T) {
	gs := identityServer("authorized")
	k := fakeKubeClient(gs)
	dyn := k.Dynamic.(*dynamicfake.FakeDynamicClient)
	patches := 0
	dyn.PrependReactor("patch", "gameservers", func(a ktesting.Action) (bool, runtime.Object, error) {
		patches++
		if patches == 1 {
			assertIdentityPatch(t, a, "authorized", "10")
			fresh := gs.DeepCopy()
			fresh.SetResourceVersion("11")
			if err := dyn.Tracker().Update(kube.GVRs["servers"], fresh, scope.DefaultNamespace); err != nil {
				t.Fatal(err)
			}
			return true, nil, apierrors.NewConflict(kube.GVRs["servers"].GroupResource(), "alpha", errors.New("status changed after read"))
		}
		assertIdentityPatch(t, a, "authorized", "11")
		return false, nil, nil
	})
	rr := doWithUser(t, identityRouter(k, gs), "POST", "/servers/alpha:wipe-data", wipeDataReq{Confirm: "alpha"}, &auth.User{ID: 42})
	if rr.Code != http.StatusAccepted || patches != 2 {
		t.Fatalf("got %d %s, patches %d", rr.Code, rr.Body, patches)
	}
}

func assertIdentityPatch(t *testing.T, a ktesting.Action, uid, rv string) {
	t.Helper()
	var patch map[string]any
	if err := json.Unmarshal(a.(ktesting.PatchAction).GetPatch(), &patch); err != nil {
		t.Fatal(err)
	}
	md, _ := patch["metadata"].(map[string]any)
	if md["uid"] != uid || md["resourceVersion"] != rv {
		t.Errorf("patch omitted identity preconditions: %s", a.(ktesting.PatchAction).GetPatch())
	}
}

func TestServerIdentity_LifecycleGrantChangeDoesNotRetry(t *testing.T) {
	for _, suffix := range []string{":start", ":stop", ":restart", ":wake"} {
		t.Run(suffix, func(t *testing.T) {
			gs := identityServer("authorized")
			k := fakeKubeClient(gs)
			calls := 0
			k.Dynamic.(*dynamicfake.FakeDynamicClient).PrependReactor("patch", "gameservers", func(a ktesting.Action) (bool, runtime.Object, error) {
				calls++
				assertIdentityPatch(t, a, "authorized", "10")
				// Status-only conflicts may retry start/stop, but an ownership
				// change must never carry the old grant into another attempt.
				changed := gs.DeepCopy()
				changed.SetResourceVersion("11")
				changed.SetAnnotations(map[string]string{ownerIDAnnotation: "99"})
				if err := k.Dynamic.(*dynamicfake.FakeDynamicClient).Tracker().Update(kube.GVRs["servers"], changed, scope.DefaultNamespace); err != nil {
					t.Fatal(err)
				}
				return true, nil, apierrors.NewConflict(kube.GVRs["servers"].GroupResource(), "alpha", errors.New("changed after read"))
			})
			rr := doWithUser(t, identityRouter(k, gs), "POST", "/servers/alpha"+suffix, nil, &auth.User{ID: 42})
			if rr.Code != http.StatusConflict || calls != 1 {
				t.Fatalf("got %d, patch calls %d", rr.Code, calls)
			}
		})
	}
}

func identitySecret() *corev1.Secret {
	return &corev1.Secret{ObjectMeta: metav1.ObjectMeta{
		Name: "alpha-tunnel-auth", Namespace: scope.DefaultNamespace,
		UID: "credential", ResourceVersion: "20",
		OwnerReferences: []metav1.OwnerReference{{Kind: "GameServer", Name: "alpha", UID: "authorized"}},
	}, Data: map[string][]byte{"token": []byte("old")}}
}

func TestServerIdentity_TunnelRotationPinsBothObjects(t *testing.T) {
	gs := identityServer("authorized")
	k := fakeKubeClient(gs)
	k.Typed = kubefake.NewClientset(identitySecret())
	k.Typed.(*kubefake.Clientset).PrependReactor("patch", "secrets", func(a ktesting.Action) (bool, runtime.Object, error) {
		assertIdentityPatch(t, a, "credential", "20")
		return false, nil, nil
	})
	k.Dynamic.(*dynamicfake.FakeDynamicClient).PrependReactor("patch", "gameservers", func(a ktesting.Action) (bool, runtime.Object, error) {
		assertIdentityPatch(t, a, "authorized", "10")
		return false, nil, nil
	})
	rr := doWithUser(t, identityRouter(k, gs), "PUT", "/servers/alpha:tunnel-credentials", putReq{Provider: "frp", Values: map[string]string{"token": "rotated"}}, &auth.User{ID: 42})
	if rr.Code != http.StatusNoContent {
		t.Fatalf("got %d %s", rr.Code, rr.Body)
	}
	secret, err := k.Typed.CoreV1().Secrets(scope.DefaultNamespace).Get(t.Context(), "alpha-tunnel-auth", metav1.GetOptions{})
	if err != nil || secret.StringData["token"] != "rotated" {
		t.Fatalf("rotation failed: %v %v", secret, err)
	}
}

func TestServerIdentity_TunnelSecretConflictStopsMutation(t *testing.T) {
	gs := identityServer("authorized")
	k := fakeKubeClient(gs)
	k.Typed = kubefake.NewClientset(identitySecret())
	k.Typed.(*kubefake.Clientset).PrependReactor("patch", "secrets", func(a ktesting.Action) (bool, runtime.Object, error) {
		assertIdentityPatch(t, a, "credential", "20")
		return true, nil, apierrors.NewConflict(corev1.Resource("secrets"), "alpha-tunnel-auth", errors.New("replaced after ownership check"))
	})
	rr := doWithUser(t, identityRouter(k, gs), "PUT", "/servers/alpha:tunnel-credentials", putReq{Provider: "frp", Values: map[string]string{"token": "rotated"}}, &auth.User{ID: 42})
	if rr.Code != http.StatusConflict {
		t.Fatalf("got %d %s", rr.Code, rr.Body)
	}
	for _, a := range k.Dynamic.(*dynamicfake.FakeDynamicClient).Actions() {
		if a.GetVerb() == "patch" {
			t.Fatal("server patched after Secret conflict")
		}
	}
}

func TestServerIdentity_TunnelDeletePinsSecret(t *testing.T) {
	gs := identityServer("authorized")
	if err := unstructured.SetNestedField(gs.Object, "alpha-tunnel-auth", "spec", "networking", "tunnel", "credentialsSecretRef", "name"); err != nil {
		t.Fatal(err)
	}
	k := fakeKubeClient(gs)
	k.Typed = kubefake.NewClientset(identitySecret())
	k.Typed.(*kubefake.Clientset).PrependReactor("delete", "secrets", func(a ktesting.Action) (bool, runtime.Object, error) {
		p := a.(ktesting.DeleteAction).GetDeleteOptions().Preconditions
		if p == nil || p.UID == nil || *p.UID != "credential" || p.ResourceVersion == nil || *p.ResourceVersion != "20" {
			t.Errorf("delete omitted checked Secret identity: %+v", p)
		}
		return true, nil, apierrors.NewConflict(corev1.Resource("secrets"), "alpha-tunnel-auth", errors.New("replaced after ownership check"))
	})
	rr := doWithUser(t, identityRouter(k, gs), "DELETE", "/servers/alpha:tunnel-credentials", nil, &auth.User{ID: 42})
	if rr.Code != http.StatusConflict {
		t.Fatalf("got %d %s", rr.Code, rr.Body)
	}
	if _, err := k.Typed.CoreV1().Secrets(scope.DefaultNamespace).Get(t.Context(), "alpha-tunnel-auth", metav1.GetOptions{}); err != nil {
		t.Fatalf("conflicting Secret was deleted: %v", err)
	}
}

func TestServerIdentity_DeleteConflictPreservesServer(t *testing.T) {
	gs := identityServer("authorized")
	k := fakeKubeClient(gs)
	k.Dynamic.(*dynamicfake.FakeDynamicClient).PrependReactor("delete", "gameservers", func(a ktesting.Action) (bool, runtime.Object, error) {
		p := a.(ktesting.DeleteAction).GetDeleteOptions().Preconditions
		if p == nil || p.UID == nil || *p.UID != "authorized" || p.ResourceVersion == nil || *p.ResourceVersion != "10" {
			t.Errorf("delete omitted server identity preconditions: %+v", p)
		}
		return true, nil, apierrors.NewConflict(kube.GVRs["servers"].GroupResource(), "alpha", errors.New("changed after read"))
	})
	rr := doWithUser(t, identityRouter(k, gs), "DELETE", "/servers/alpha", nil, &auth.User{ID: 42})
	if rr.Code != http.StatusConflict {
		t.Fatalf("got %d %s", rr.Code, rr.Body)
	}
	if _, err := k.GetServer(t.Context(), scope.DefaultNamespace, "alpha"); err != nil {
		t.Fatalf("conflicting server was deleted: %v", err)
	}
}

func TestServerIdentity_DeleteRechecksOwnerOnSameUID(t *testing.T) {
	authorized := identityServer("authorized")
	live := authorized.DeepCopy()
	live.SetAnnotations(map[string]string{ownerIDAnnotation: "99"})
	k := fakeKubeClient(live)
	rr := doWithUser(t, identityRouter(k, authorized), "DELETE", "/servers/alpha", nil, &auth.User{ID: 42})
	if rr.Code != http.StatusForbidden {
		t.Fatalf("got %d %s", rr.Code, rr.Body)
	}
	if _, err := k.GetServer(t.Context(), scope.DefaultNamespace, "alpha"); err != nil {
		t.Fatalf("server deleted after ownership was lost: %v", err)
	}
}

func TestServerIdentity_OwnerRetryNeverRebindsReplacement(t *testing.T) {
	for _, user := range []*auth.User{{ID: 42}, testAdminUser()} {
		t.Run(user.Username, func(t *testing.T) {
			gs := identityServer("authorized")
			k := fakeKubeClient(gs)
			dyn := k.Dynamic.(*dynamicfake.FakeDynamicClient)
			patches := 0
			dyn.PrependReactor("patch", "gameservers", func(a ktesting.Action) (bool, runtime.Object, error) {
				patches++
				assertIdentityPatch(t, a, "authorized", "10")
				replacement := identityServer("replacement")
				replacement.SetResourceVersion("11")
				if err := dyn.Tracker().Update(kube.GVRs["servers"], replacement, scope.DefaultNamespace); err != nil {
					t.Fatal(err)
				}
				return true, nil, apierrors.NewConflict(kube.GVRs["servers"].GroupResource(), "alpha", errors.New("deleted and recreated after read"))
			})
			rr := doWithUser(t, identityRouter(k, gs), "POST", "/servers/alpha:wipe-data", wipeDataReq{Confirm: "alpha"}, user)
			if rr.Code != http.StatusNotFound || patches != 1 {
				t.Fatalf("got %d %s, patches %d", rr.Code, rr.Body, patches)
			}
		})
	}
}

func TestServerIdentity_TunnelServerConflictKeepsSecret(t *testing.T) {
	gs := identityServer("authorized")
	if err := unstructured.SetNestedField(gs.Object, "alpha-tunnel-auth", "spec", "networking", "tunnel", "credentialsSecretRef", "name"); err != nil {
		t.Fatal(err)
	}
	k := fakeKubeClient(gs)
	k.Typed = kubefake.NewClientset(identitySecret())
	k.Dynamic.(*dynamicfake.FakeDynamicClient).PrependReactor("patch", "gameservers", func(a ktesting.Action) (bool, runtime.Object, error) {
		assertIdentityPatch(t, a, "authorized", "10")
		return true, nil, apierrors.NewConflict(kube.GVRs["servers"].GroupResource(), "alpha", errors.New("changed after read"))
	})
	rr := doWithUser(t, identityRouter(k, gs), "DELETE", "/servers/alpha:tunnel-credentials", nil, &auth.User{ID: 42})
	if rr.Code != http.StatusConflict {
		t.Fatalf("got %d %s", rr.Code, rr.Body)
	}
	for _, a := range k.Typed.(*kubefake.Clientset).Actions() {
		if a.GetVerb() == "delete" {
			t.Fatal("credential deleted after server patch conflict")
		}
	}
}

func TestServerIdentity_TunnelDeleteIgnoresSecretCreatedAfterCheck(t *testing.T) {
	gs := identityServer("authorized")
	if err := unstructured.SetNestedField(gs.Object, "alpha-tunnel-auth", "spec", "networking", "tunnel", "credentialsSecretRef", "name"); err != nil {
		t.Fatal(err)
	}
	k := fakeKubeClient(gs)
	k.Dynamic.(*dynamicfake.FakeDynamicClient).PrependReactor("patch", "gameservers", func(_ ktesting.Action) (bool, runtime.Object, error) {
		if _, err := k.Typed.CoreV1().Secrets(scope.DefaultNamespace).Create(t.Context(), identitySecret(), metav1.CreateOptions{}); err != nil {
			t.Fatal(err)
		}
		return false, nil, nil
	})
	rr := doWithUser(t, identityRouter(k, gs), "DELETE", "/servers/alpha:tunnel-credentials", nil, &auth.User{ID: 42})
	if rr.Code != http.StatusNoContent {
		t.Fatalf("got %d %s", rr.Code, rr.Body)
	}
	if _, err := k.Typed.CoreV1().Secrets(scope.DefaultNamespace).Get(t.Context(), "alpha-tunnel-auth", metav1.GetOptions{}); err != nil {
		t.Fatalf("new Secret was deleted without an ownership check: %v", err)
	}
}
