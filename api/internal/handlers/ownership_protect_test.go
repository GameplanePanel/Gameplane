package handlers

import (
	"context"
	"net/http"
	"regexp"
	"testing"

	certv1 "k8s.io/api/certificates/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/rest"
	k8stesting "k8s.io/client-go/testing"

	"github.com/GameplanePanel/gameplane/api/internal/kube"
)

func dangling() []any {
	return []any{map[string]any{
		"apiVersion": "v1", "kind": "ConfigMap", "name": "x", "uid": "00000000-0000-0000-0000-000000000000",
	}}
}

func TestResources_Update_GameServerKeepsLiveOwnership(t *testing.T) {
	live := newServerObj("gameplane-games", "alpha")
	live.SetFinalizers([]string{"gameplane.local/keep"})
	k := fakeKubeClient(live)
	r := mountResourcesRouter(k)
	body := map[string]any{
		"apiVersion": "gameplane.local/v1alpha1",
		"kind":       "GameServer",
		"metadata": map[string]any{
			"name": "alpha", "namespace": "gameplane-games",
			"ownerReferences": dangling(), "finalizers": []any{"evil/x"},
		},
		"spec": map[string]any{"templateRef": map[string]any{"name": "minecraft"}},
	}
	rr := do(t, r, "PUT", "/servers/alpha", body)
	if rr.Code != http.StatusOK {
		t.Fatalf("put: %d %s", rr.Code, rr.Body)
	}
	got, err := k.Dynamic.Resource(kube.GVRs["servers"]).Namespace("gameplane-games").Get(context.Background(), "alpha", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.GetOwnerReferences()) != 0 {
		t.Errorf("ownerReferences = %v, want none", got.GetOwnerReferences())
	}
	if f := got.GetFinalizers(); len(f) != 1 || f[0] != "gameplane.local/keep" {
		t.Errorf("finalizers = %v, want live value", f)
	}
}

func TestResources_Create_ClearsClientOwnership(t *testing.T) {
	for _, tc := range []struct{ path, kind string }{
		{"/servers", "GameServer"},
		{"/backups", "Backup"},
	} {
		k := fakeKubeClient()
		r := mountResourcesRouter(k)
		body := map[string]any{
			"apiVersion": "gameplane.local/v1alpha1",
			"kind":       tc.kind,
			"metadata": map[string]any{
				"name": "n1", "ownerReferences": dangling(), "finalizers": []any{"evil/x"},
			},
			"spec": map[string]any{"templateRef": map[string]any{"name": "minecraft"}, "serverRef": map[string]any{"name": "alpha"}},
		}
		rr := do(t, r, "POST", tc.path, body)
		if rr.Code != http.StatusCreated {
			t.Fatalf("%s create: %d %s", tc.path, rr.Code, rr.Body)
		}
		got, err := k.Dynamic.Resource(kube.GVRs[tc.path[1:]]).Namespace("gameplane-games").Get(context.Background(), "n1", metav1.GetOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if len(got.GetOwnerReferences()) != 0 || len(got.GetFinalizers()) != 0 {
			t.Errorf("%s: ownerReferences=%v finalizers=%v, want none", tc.path, got.GetOwnerReferences(), got.GetFinalizers())
		}
	}
}

func TestProtectOwnership(t *testing.T) {
	obj := &unstructured.Unstructured{Object: map[string]any{"metadata": map[string]any{
		"ownerReferences": dangling(), "finalizers": []any{"evil/x"},
	}}}
	protectOwnership(obj, nil)
	if len(obj.GetOwnerReferences()) != 0 || len(obj.GetFinalizers()) != 0 {
		t.Fatal("nil live must clear ownership")
	}
}

func TestProtectOwnership_KeepsLiveValues(t *testing.T) {
	live := &unstructured.Unstructured{Object: map[string]any{"metadata": map[string]any{
		"ownerReferences": []any{map[string]any{
			"apiVersion": "gameplane.local/v1alpha1", "kind": "GameServer", "name": "alpha",
			"uid": "u1", "controller": true,
		}},
		"finalizers": []any{"gameplane.local/keep"},
	}}}
	obj := &unstructured.Unstructured{Object: map[string]any{"metadata": map[string]any{
		"ownerReferences": dangling(), "finalizers": []any{"evil/x"},
	}}}
	protectOwnership(obj, live)
	refs := obj.GetOwnerReferences()
	if len(refs) != 1 || refs[0].Name != "alpha" || refs[0].Kind != "GameServer" {
		t.Errorf("ownerReferences = %v, want the live reference", refs)
	}
	if f := obj.GetFinalizers(); len(f) != 1 || f[0] != "gameplane.local/keep" {
		t.Errorf("finalizers = %v, want live value", f)
	}
}

func TestResources_Update_ScheduleKeepsLiveOwnership(t *testing.T) {
	live := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "gameplane.local/v1alpha1",
		"kind":       "BackupSchedule",
		"metadata": map[string]any{
			"name": "alpha-auto", "namespace": "gameplane-games",
			"ownerReferences": []any{map[string]any{
				"apiVersion": "gameplane.local/v1alpha1", "kind": "GameServer", "name": "alpha",
				"uid": "u1", "controller": true,
			}},
			"finalizers": []any{"gameplane.local/keep"},
		},
		"spec": map[string]any{"serverRef": map[string]any{"name": "alpha"}},
	}}
	k := fakeKubeClient(live)
	r := mountResourcesRouter(k)
	body := map[string]any{
		"apiVersion": "gameplane.local/v1alpha1",
		"kind":       "BackupSchedule",
		"metadata": map[string]any{
			"name": "alpha-auto", "namespace": "gameplane-games",
			"ownerReferences": dangling(), "finalizers": []any{"evil/x"},
		},
		"spec": map[string]any{"serverRef": map[string]any{"name": "alpha"}},
	}
	rr := do(t, r, "PUT", "/schedules/alpha-auto", body)
	if rr.Code != http.StatusOK {
		t.Fatalf("put: %d %s", rr.Code, rr.Body)
	}
	got, err := k.Dynamic.Resource(kube.GVRs["schedules"]).Namespace("gameplane-games").Get(context.Background(), "alpha-auto", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	refs := got.GetOwnerReferences()
	if len(refs) != 1 || refs[0].Name != "alpha" || refs[0].Kind != "GameServer" || refs[0].UID != "u1" {
		t.Errorf("ownerReferences = %v, want exactly the live reference", refs)
	}
	if f := got.GetFinalizers(); len(f) != 1 || f[0] != "gameplane.local/keep" {
		t.Errorf("finalizers = %v, want exactly the live finalizer", f)
	}
}

func TestConfig_ResetRoleMapping_NoAuthRow_WritesNothing(t *testing.T) {
	srv, store := newConfigServer(t)
	status, body := doReq(t, "DELETE", srv.URL+"/admin/config/auth/role-mappings/admin", nil)
	if status != http.StatusOK {
		t.Fatalf("status = %d; body=%s", status, body)
	}
	var n int
	if err := store.DB.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM config WHERE key = 'auth'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("auth row count = %d, want 0", n)
	}
}

func TestSignClientCert_NameIsDNSSafeForAnyUsername(t *testing.T) {
	cs := fake.NewSimpleClientset()
	var created string
	cs.PrependReactor("create", "certificatesigningrequests", func(a k8stesting.Action) (bool, runtime.Object, error) {
		created = a.(k8stesting.CreateAction).GetObject().(*certv1.CertificateSigningRequest).Name
		return false, nil, nil
	})
	cs.PrependReactor("get", "certificatesigningrequests", signedCSRReactor)
	h := &clusterActions{k: &kube.Client{Typed: cs, Config: &rest.Config{}}, enabled: true}
	if _, err := h.signClientCert(t.Context(), "Alice+ab12@Example.com", []byte("csr")); err != nil {
		t.Fatalf("sign: %v", err)
	}
	if !regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`).MatchString(created) {
		t.Fatalf("CSR name %q is not DNS-safe", created)
	}
}
