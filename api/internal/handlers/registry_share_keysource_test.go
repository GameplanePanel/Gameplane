package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8sfake "k8s.io/client-go/kubernetes/fake"

	"github.com/GameplanePanel/gameplane/api/internal/kube"
	"github.com/GameplanePanel/gameplane/api/internal/registry"
)

func shareKeySourceRouter(t *testing.T, secret *corev1.Secret, opts RegistryShareOptions) http.Handler {
	t.Helper()
	k := &kube.Client{Typed: k8sfake.NewSimpleClientset()}
	if secret != nil {
		if _, err := k.Typed.CoreV1().Secrets("gameplane-system").Create(context.Background(), secret, metav1.CreateOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	r := chi.NewRouter()
	MountRegistryShareWithOptions(r, k, "gameplane-system", opts)
	return r
}

func getShareKeySource(t *testing.T, h http.Handler) string {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/admin/registries/curseforge/share", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", rec.Code, rec.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	src, _ := body["keySource"].(string)
	return src
}

func TestRegistryShareKeySourceVocabulary(t *testing.T) {
	managed := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      registry.DefaultKeySecretName("curseforge"),
			Namespace: "gameplane-system",
			Labels:    map[string]string{registry.RegistryKeySecretLabel: "true"},
		},
		Data: map[string][]byte{"apiKey": []byte("k-secret")},
	}
	cases := []struct {
		name   string
		secret *corev1.Secret
		opts   RegistryShareOptions
		want   string
	}{
		{"managed key wins over the flag", managed, RegistryShareOptions{StartupKeySet: true}, "admin"},
		{"managed key only", managed, RegistryShareOptions{}, "admin"},
		{"no secret, startup flag set", nil, RegistryShareOptions{StartupKeySet: true}, "startup-flag"},
		{"no secret, no flag", nil, RegistryShareOptions{}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := shareKeySourceRouter(t, tc.secret, tc.opts)
			if got := getShareKeySource(t, h); got != tc.want {
				t.Errorf("keySource = %q, want %q", got, tc.want)
			}
		})
	}
}
