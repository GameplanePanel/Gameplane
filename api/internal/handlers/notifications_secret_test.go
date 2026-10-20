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
	"github.com/GameplanePanel/gameplane/api/internal/notify"
)

// sinkSecretRouter is notifTestRouter without the Notifier — the secret
// endpoints only need the kube client, and skipping notify.New keeps
// these tests free of the delivery worker.
func sinkSecretRouter(t *testing.T, secrets ...runtime.Object) (chi.Router, *kube.Client) {
	t.Helper()
	r := chi.NewRouter()
	k := &kube.Client{Typed: kubefake.NewClientset(secrets...)}
	MountNotifications(r, notify.New(newTestStore(t), k, notifNS), k, notifNS)
	return r, k
}

func putSinkSecret(t *testing.T, h http.Handler, sink string, body any) *httptest.ResponseRecorder {
	t.Helper()
	b, _ := json.Marshal(body)
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPut, "/admin/notifications/sinks/"+sink+"/secret", bytes.NewReader(b))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

func TestSinkSecret_CreateWebhook(t *testing.T) {
	r, k := sinkSecretRouter(t)
	rr := putSinkSecret(t, r, "ops-hook", map[string]string{"kind": "webhook", "url": "https://hooks.example.com/x", "authorization": "Bearer tok"})
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rr.Code, rr.Body)
	}
	var resp struct {
		Name string   `json:"name"`
		Keys []string `json:"keys"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Name != "gameplane-notify-ops-hook" {
		t.Fatalf("name = %q", resp.Name)
	}
	if strings.Contains(rr.Body.String(), "Bearer tok") {
		t.Fatal("response echoed a secret value")
	}
	sec, err := k.Typed.CoreV1().Secrets(notifNS).Get(context.Background(), resp.Name, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get secret: %v", err)
	}
	if sec.Labels[notify.SinkSecretLabel] != "true" || sec.Labels[ManagedByLabel] != managedByValue {
		t.Fatalf("labels = %v", sec.Labels)
	}
	if sec.StringData["url"] != "https://hooks.example.com/x" || sec.StringData["authorization"] != "Bearer tok" {
		t.Fatalf("data = %v", sec.StringData)
	}
}

func TestSinkSecret_NtfyTokenBecomesBearer(t *testing.T) {
	r, k := sinkSecretRouter(t)
	rr := putSinkSecret(t, r, "alerts", map[string]string{"kind": "ntfy", "url": "https://ntfy.sh/gameplane-alerts", "token": "tk_x"})
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rr.Code, rr.Body)
	}
	sec, err := k.Typed.CoreV1().Secrets(notifNS).Get(context.Background(), "gameplane-notify-alerts", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get secret: %v", err)
	}
	if sec.StringData["authorization"] != "Bearer tk_x" {
		t.Fatalf("authorization = %q, want Bearer tk_x", sec.StringData["authorization"])
	}
}

func TestSinkSecret_UpdateReplacesOptionalKeys(t *testing.T) {
	r, k := sinkSecretRouter(t)
	if rr := putSinkSecret(t, r, "hook", map[string]string{"kind": "webhook", "url": "https://a.example", "authorization": "Bearer old"}); rr.Code != http.StatusOK {
		t.Fatalf("first put: %d %s", rr.Code, rr.Body)
	}
	// Re-save without authorization: the full key set is written, so the
	// stale header value must be cleared, not kept.
	if rr := putSinkSecret(t, r, "hook", map[string]string{"kind": "webhook", "url": "https://b.example"}); rr.Code != http.StatusOK {
		t.Fatalf("second put: %d %s", rr.Code, rr.Body)
	}
	sec, err := k.Typed.CoreV1().Secrets(notifNS).Get(context.Background(), "gameplane-notify-hook", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get secret: %v", err)
	}
	if sec.StringData["url"] != "https://b.example" || sec.StringData["authorization"] != "" {
		t.Fatalf("data = %v", sec.StringData)
	}
}

func TestSinkSecret_ValidationFailures(t *testing.T) {
	r, _ := sinkSecretRouter(t)
	cases := []struct {
		name string
		sink string
		body map[string]string
	}{
		{"unknown kind", "a", map[string]string{"kind": "telegram", "url": "https://x.example"}},
		{"bad url", "a", map[string]string{"kind": "discord", "url": "not-a-url"}},
		{"smtp missing host", "a", map[string]string{"kind": "smtp", "from": "a@b", "to": "c@d"}},
		{"smtp bad tls", "a", map[string]string{"kind": "smtp", "host": "mx", "from": "a@b", "to": "c@d", "tls": "wat"}},
		{"bad sink name", "Not_A_Label", map[string]string{"kind": "discord", "url": "https://x.example"}},
		{"name too long", strings.Repeat("a", 60), map[string]string{"kind": "discord", "url": "https://x.example"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if rr := putSinkSecret(t, r, tc.sink, tc.body); rr.Code != http.StatusUnprocessableEntity {
				t.Fatalf("status = %d body=%s, want 422", rr.Code, rr.Body)
			}
		})
	}
}

func TestSinkSecret_RefusesForeignSecret(t *testing.T) {
	// A same-named Secret without the sink label must not be overwritten.
	foreign := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "gameplane-notify-hook", Namespace: notifNS},
		Data:       map[string][]byte{"something": []byte("else")},
	}
	r, _ := sinkSecretRouter(t, foreign)
	rr := putSinkSecret(t, r, "hook", map[string]string{"kind": "webhook", "url": "https://a.example"})
	if rr.Code != http.StatusConflict {
		t.Fatalf("status = %d body=%s, want 409", rr.Code, rr.Body)
	}
}

func TestSinkSecret_DeleteManagedOnly(t *testing.T) {
	managed := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name: "gameplane-notify-mine", Namespace: notifNS,
			Labels: map[string]string{notify.SinkSecretLabel: "true", ManagedByLabel: managedByValue},
		},
	}
	// Labelled for the notifier but created by hand — delete must refuse.
	userOwned := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name: "gameplane-notify-yours", Namespace: notifNS,
			Labels: map[string]string{notify.SinkSecretLabel: "true"},
		},
	}
	r, k := sinkSecretRouter(t, managed, userOwned)

	del := func(sink string) int {
		req := httptest.NewRequestWithContext(t.Context(), http.MethodDelete, "/admin/notifications/sinks/"+sink+"/secret", nil)
		rr := httptest.NewRecorder()
		r.ServeHTTP(rr, req)
		return rr.Code
	}

	if code := del("mine"); code != http.StatusNoContent {
		t.Fatalf("delete managed: %d, want 204", code)
	}
	if _, err := k.Typed.CoreV1().Secrets(notifNS).Get(context.Background(), "gameplane-notify-mine", metav1.GetOptions{}); err == nil {
		t.Fatal("managed secret still exists after delete")
	}
	if code := del("yours"); code != http.StatusNotFound {
		t.Fatalf("delete user-owned: %d, want 404", code)
	}
	if _, err := k.Typed.CoreV1().Secrets(notifNS).Get(context.Background(), "gameplane-notify-yours", metav1.GetOptions{}); err != nil {
		t.Fatal("user-owned secret was deleted")
	}
	if code := del("ghost"); code != http.StatusNotFound {
		t.Fatalf("delete missing: %d, want 404", code)
	}
}
