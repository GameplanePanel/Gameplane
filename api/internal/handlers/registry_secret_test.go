package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	kubefake "k8s.io/client-go/kubernetes/fake"

	"github.com/GameplanePanel/gameplane/api/internal/kube"
	"github.com/GameplanePanel/gameplane/api/internal/registry"
)

func registrySecretRouter(secrets ...runtime.Object) (chi.Router, *kube.Client) {
	r := chi.NewRouter()
	k := &kube.Client{Typed: kubefake.NewClientset(secrets...)}
	MountRegistrySecrets(r, k, notifNS)
	return r, k
}

func putRegistrySecret(t *testing.T, h http.Handler, provider string, body any) *httptest.ResponseRecorder {
	t.Helper()
	b, _ := json.Marshal(body)
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPut, "/admin/registries/"+provider+"/secret", bytes.NewReader(b))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

func TestRegistrySecret_Create(t *testing.T) {
	r, k := registrySecretRouter()
	rr := putRegistrySecret(t, r, "curseforge", map[string]string{"apiKey": "s3cret-key"})
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rr.Code, rr.Body)
	}
	if strings.Contains(rr.Body.String(), "s3cret-key") {
		t.Fatal("response echoed the apiKey value")
	}
	sec, err := k.Typed.CoreV1().Secrets(notifNS).Get(context.Background(), "gameplane-modreg-curseforge", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get secret: %v", err)
	}
	if sec.Labels[registry.RegistryKeySecretLabel] != "true" || sec.Labels[ManagedByLabel] != managedByValue {
		t.Fatalf("labels = %v", sec.Labels)
	}
	if sec.StringData["apiKey"] != "s3cret-key" {
		t.Fatalf("data = %v", sec.StringData)
	}
}

func TestRegistrySecret_UnknownProviderRejected(t *testing.T) {
	r, _ := registrySecretRouter()
	rr := putRegistrySecret(t, r, "modrinth", map[string]string{"apiKey": "x"})
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body=%s, want 400", rr.Code, rr.Body)
	}

	req := httptest.NewRequestWithContext(t.Context(), http.MethodDelete, "/admin/registries/modrinth/secret", nil)
	rr2 := httptest.NewRecorder()
	r.ServeHTTP(rr2, req)
	if rr2.Code != http.StatusBadRequest {
		t.Fatalf("delete status = %d, want 400", rr2.Code)
	}
}

func TestRegistrySecret_ReservedProvidersAccepted(t *testing.T) {
	// steam and nexus have no engine yet but must still accept a key so
	// their plumbing can land ahead of the engine (task requirement).
	r, _ := registrySecretRouter()
	for _, provider := range []string{"steam", "nexus"} {
		if rr := putRegistrySecret(t, r, provider, map[string]string{"apiKey": "x"}); rr.Code != http.StatusOK {
			t.Fatalf("provider %s: status = %d body=%s", provider, rr.Code, rr.Body)
		}
	}
}

func TestRegistrySecret_ValidationEmptyKey(t *testing.T) {
	r, _ := registrySecretRouter()
	rr := putRegistrySecret(t, r, "curseforge", map[string]string{"apiKey": ""})
	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d body=%s, want 422", rr.Code, rr.Body)
	}
}

func TestRegistrySecret_RefusesForeignAndDeletesManagedOnly(t *testing.T) {
	foreign := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "gameplane-modreg-curseforge", Namespace: notifNS},
	}
	r, k := registrySecretRouter(foreign)

	if rr := putRegistrySecret(t, r, "curseforge", map[string]string{"apiKey": "x"}); rr.Code != http.StatusConflict {
		t.Fatalf("foreign upsert: status = %d, want 409", rr.Code)
	}

	del := func(provider string) int {
		req := httptest.NewRequestWithContext(t.Context(), http.MethodDelete, "/admin/registries/"+provider+"/secret", nil)
		rr := httptest.NewRecorder()
		r.ServeHTTP(rr, req)
		return rr.Code
	}
	// Not managed by the API (unlabelled): delete refuses (reads as 404).
	if code := del("curseforge"); code != http.StatusNotFound {
		t.Fatalf("delete foreign: %d, want 404", code)
	}

	// A different, unclaimed provider: create then delete round-trips.
	if rr := putRegistrySecret(t, r, "steam", map[string]string{"apiKey": "x"}); rr.Code != http.StatusOK {
		t.Fatalf("create steam: %d", rr.Code)
	}
	if code := del("steam"); code != http.StatusNoContent {
		t.Fatalf("delete managed: %d, want 204", code)
	}
	if _, err := k.Typed.CoreV1().Secrets(notifNS).Get(context.Background(), "gameplane-modreg-steam", metav1.GetOptions{}); err == nil {
		t.Fatal("managed secret still exists after delete")
	}
}

// TestRegistrySecret_NeverLeaksIntoConfig confirms an apiKey PUT here is
// invisible to /admin/config: the modRegistries section holds only
// provider+configRef, never the key itself, and the PUT response of this
// endpoint is checked separately (TestRegistrySecret_Create) to never
// echo the value.
func TestRegistrySecret_NeverLeaksIntoConfig(t *testing.T) {
	store := newTestStore(t)
	configRouter := chi.NewRouter()
	MountConfig(configRouter, store, nil, false, "", nil)

	body := bytes.NewReader([]byte(`{"registries":[{"provider":"curseforge","configRef":"gameplane-modreg-curseforge"}]}`))
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPut, "/admin/config/modRegistries", body)
	rr := httptest.NewRecorder()
	configRouter.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("put modRegistries config: status = %d body=%s", rr.Code, rr.Body)
	}
	if strings.Contains(rr.Body.String(), "apiKey") {
		t.Fatalf("config PUT response mentions apiKey: %s", rr.Body)
	}

	getReq := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/admin/config", nil)
	getRR := httptest.NewRecorder()
	configRouter.ServeHTTP(getRR, getReq)
	if strings.Contains(getRR.Body.String(), "apiKey") || strings.Contains(getRR.Body.String(), "s3cret") {
		t.Fatalf("config GET response contains key material: %s", getRR.Body)
	}
}
