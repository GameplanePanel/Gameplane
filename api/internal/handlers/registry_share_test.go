package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/go-chi/chi/v5"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8sfake "k8s.io/client-go/kubernetes/fake"

	"github.com/GameplanePanel/gameplane/api/internal/kube"
	"github.com/GameplanePanel/gameplane/api/internal/registry"
)

func shareRouter(t *testing.T, secret *corev1.Secret) (http.Handler, *kube.Client) {
	t.Helper()
	k := &kube.Client{Typed: k8sfake.NewSimpleClientset()}
	if secret != nil {
		if _, err := k.Typed.CoreV1().Secrets("gameplane-system").Create(context.Background(), secret, metav1.CreateOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	r := chi.NewRouter()
	MountRegistryShare(r, k, "gameplane-system")
	return r, k
}

func managedKeySecret() *corev1.Secret {
	return &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: registry.DefaultKeySecretName("curseforge"), Namespace: "gameplane-system",
		Labels: map[string]string{registry.RegistryKeySecretLabel: "true"}}, Data: map[string][]byte{"apiKey": []byte("k")}}
}

func TestShare_PutSetsAndClearsAnnotation(t *testing.T) {
	r, k := shareRouter(t, managedKeySecret())
	var v shareView
	if rr := do(t, r, http.MethodGet, "/admin/registries/curseforge/share", nil); rr.Code != 200 || json.Unmarshal(rr.Body.Bytes(), &v) != nil || v.Share || !v.ManagedKey {
		t.Fatalf("initial GET = %d %s", rr.Code, rr.Body)
	}
	if rr := do(t, r, http.MethodPut, "/admin/registries/curseforge/share", map[string]any{"share": true}); rr.Code != 200 {
		t.Fatalf("PUT true = %d %s", rr.Code, rr.Body)
	}
	sec, _ := k.Typed.CoreV1().Secrets("gameplane-system").Get(context.Background(), registry.DefaultKeySecretName("curseforge"), metav1.GetOptions{})
	if sec.Annotations[curseforgeShareAnnotation] != "true" || string(sec.Data["apiKey"]) != "k" {
		t.Errorf("secret after PUT true: annotations=%v", sec.Annotations)
	}
	if rr := do(t, r, http.MethodPut, "/admin/registries/curseforge/share", map[string]any{"share": false}); rr.Code != 200 {
		t.Fatalf("PUT false = %d %s", rr.Code, rr.Body)
	}
	sec, _ = k.Typed.CoreV1().Secrets("gameplane-system").Get(context.Background(), registry.DefaultKeySecretName("curseforge"), metav1.GetOptions{})
	if _, has := sec.Annotations[curseforgeShareAnnotation]; has {
		t.Errorf("annotation not cleared: %v", sec.Annotations)
	}
}

func TestShare_NeedsManagedKey(t *testing.T) {
	r, _ := shareRouter(t, nil) // only a startup flag would hold a key: no Secret
	rr := modpackReq(t, r, http.MethodPut, "/admin/registries/curseforge/share", map[string]any{"share": true})
	if rr.Code != http.StatusConflict || problemID(t, rr) != "server.registry.share_needs_managed_key" {
		t.Fatalf("status %d id %q, want 409 server.registry.share_needs_managed_key", rr.Code, problemID(t, rr))
	}
	if rr := do(t, r, http.MethodPut, "/admin/registries/curseforge/share", map[string]any{"share": false}); rr.Code != 200 {
		t.Errorf("clearing with no managed key = %d, want 200", rr.Code)
	}
	var v shareView
	rr = do(t, r, http.MethodGet, "/admin/registries/curseforge/share", nil)
	if err := json.Unmarshal(rr.Body.Bytes(), &v); err != nil || v.Share || v.ManagedKey {
		t.Errorf("GET without secret = %s", rr.Body)
	}
	if rr := do(t, r, http.MethodPut, "/admin/registries/curseforge/share", "not an object"); rr.Code != http.StatusBadRequest {
		t.Errorf("bad body = %d, want 400", rr.Code)
	}
}
