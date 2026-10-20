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

	"github.com/GameplanePanel/gameplane/api/internal/auth"
	"github.com/GameplanePanel/gameplane/api/internal/kube"
)

func providerSecretRouter(secrets ...runtime.Object) (chi.Router, *kube.Client) {
	r := chi.NewRouter()
	k := &kube.Client{Typed: kubefake.NewClientset(secrets...)}
	MountAuthProviderSecrets(r, k, notifNS)
	return r, k
}

func putProviderSecret(t *testing.T, h http.Handler, name string, body any) *httptest.ResponseRecorder {
	t.Helper()
	b, _ := json.Marshal(body)
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPut, "/admin/auth/providers/"+name+"/secret", bytes.NewReader(b))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

func TestProviderSecret_Create(t *testing.T) {
	r, k := providerSecretRouter()
	rr := putProviderSecret(t, r, "corp", map[string]string{"clientSecret": "s3cret"})
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rr.Code, rr.Body)
	}
	if strings.Contains(rr.Body.String(), "s3cret") {
		t.Fatal("response echoed the clientSecret")
	}
	sec, err := k.Typed.CoreV1().Secrets(notifNS).Get(context.Background(), "gameplane-auth-corp", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get secret: %v", err)
	}
	if sec.Labels[auth.ProviderSecretLabel] != "true" || sec.Labels[ManagedByLabel] != managedByValue {
		t.Fatalf("labels = %v", sec.Labels)
	}
	if sec.StringData["clientSecret"] != "s3cret" {
		t.Fatalf("data = %v", sec.StringData)
	}
}

func TestProviderSecret_Validation(t *testing.T) {
	r, _ := providerSecretRouter()
	cases := []struct {
		name     string
		provider string
		body     map[string]string
	}{
		{"empty clientSecret", "corp", map[string]string{"clientSecret": ""}},
		{"reserved helm", "helm", map[string]string{"clientSecret": "x"}},
		{"bad name", "Not_A_Label", map[string]string{"clientSecret": "x"}},
		{"name too long", strings.Repeat("a", 55), map[string]string{"clientSecret": "x"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if rr := putProviderSecret(t, r, tc.provider, tc.body); rr.Code != http.StatusUnprocessableEntity {
				t.Fatalf("status = %d body=%s, want 422", rr.Code, rr.Body)
			}
		})
	}
}

func TestProviderSecret_RefusesForeignAndDeletesManagedOnly(t *testing.T) {
	foreign := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "gameplane-auth-corp", Namespace: notifNS},
	}
	userOwned := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name: "gameplane-auth-byo", Namespace: notifNS,
			Labels: map[string]string{auth.ProviderSecretLabel: "true"},
		},
	}
	r, k := providerSecretRouter(foreign, userOwned)

	// Upsert onto an unlabelled same-named Secret is refused.
	if rr := putProviderSecret(t, r, "corp", map[string]string{"clientSecret": "x"}); rr.Code != http.StatusConflict {
		t.Fatalf("foreign upsert: status = %d, want 409", rr.Code)
	}

	del := func(name string) int {
		req := httptest.NewRequestWithContext(t.Context(), http.MethodDelete, "/admin/auth/providers/"+name+"/secret", nil)
		rr := httptest.NewRecorder()
		r.ServeHTTP(rr, req)
		return rr.Code
	}
	// Labelled but not API-managed: delete refuses (reads as 404).
	if code := del("byo"); code != http.StatusNotFound {
		t.Fatalf("delete user-owned: %d, want 404", code)
	}
	// API-managed: create then delete round-trips.
	if rr := putProviderSecret(t, r, "mine", map[string]string{"clientSecret": "x"}); rr.Code != http.StatusOK {
		t.Fatalf("create mine: %d", rr.Code)
	}
	if code := del("mine"); code != http.StatusNoContent {
		t.Fatalf("delete managed: %d, want 204", code)
	}
	if _, err := k.Typed.CoreV1().Secrets(notifNS).Get(context.Background(), "gameplane-auth-mine", metav1.GetOptions{}); err == nil {
		t.Fatal("managed secret still exists after delete")
	}
}
