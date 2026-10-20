package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/go-chi/chi/v5"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	kubefake "k8s.io/client-go/kubernetes/fake"

	"github.com/GameplanePanel/gameplane/api/internal/db"
	"github.com/GameplanePanel/gameplane/api/internal/kube"
	"github.com/GameplanePanel/gameplane/api/internal/notify"
)

const notifNS = "gameplane-system"

func seedNotifSinks(t *testing.T, store *db.Store, blob string) {
	t.Helper()
	_, err := store.DB.ExecContext(context.Background(),
		`INSERT INTO config(key, value, updated_at) VALUES ('notifications', ?, datetime('now'))
		 ON CONFLICT(key) DO UPDATE SET value = excluded.value`, blob)
	if err != nil {
		t.Fatalf("seed notifications config: %v", err)
	}
}

func notifSinkSecret(name, url string) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name: name, Namespace: notifNS,
			Labels: map[string]string{notify.SinkSecretLabel: "true"},
		},
		Data: map[string][]byte{"url": []byte(url)},
	}
}

func notifTestRouter(store *db.Store, secrets ...runtime.Object) chi.Router {
	r := chi.NewRouter()
	k := &kube.Client{Typed: kubefake.NewClientset(secrets...)}
	MountNotifications(r, notify.New(store, k, notifNS), k, notifNS)
	return r
}

// doNotifTest fires the test-send with a caller-chosen client IP so each
// test gets its own rate-limit bucket (NotifyTestLimiter is a package
// singleton keyed by IP).
func doNotifTest(t *testing.T, h http.Handler, sink, ip string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/admin/notifications/sinks/"+sink+"/test", nil)
	req.RemoteAddr = ip + ":40000"
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

func TestNotificationTestSendUnknownSink(t *testing.T) {
	store := newTestStore(t)
	seedNotifSinks(t, store, `{"sinks":[{"name":"a","kind":"discord","enabled":true}]}`)
	rr := doNotifTest(t, notifTestRouter(store), "nope", "10.10.0.1")
	if rr.Code != http.StatusNotFound {
		t.Fatalf("got %d %s", rr.Code, rr.Body)
	}
}

func TestNotificationTestSendUnconfiguredSink(t *testing.T) {
	store := newTestStore(t)
	seedNotifSinks(t, store, `{"sinks":[{"name":"a","kind":"discord","enabled":true}]}`)
	rr := doNotifTest(t, notifTestRouter(store), "a", "10.10.0.2")
	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("got %d %s", rr.Code, rr.Body)
	}
}

func TestNotificationTestSendDelivers(t *testing.T) {
	var got atomic.Value
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var e map[string]any
		_ = json.NewDecoder(r.Body).Decode(&e)
		got.Store(e)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	store := newTestStore(t)
	seedNotifSinks(t, store, `{"sinks":[{"name":"hook","kind":"webhook","enabled":true,"configRef":"hook-secret"}]}`)
	r := notifTestRouter(store, notifSinkSecret("hook-secret", srv.URL))

	rr := doNotifTest(t, r, "hook", "10.10.0.3")
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), `"delivered":true`) {
		t.Fatalf("got %d %s", rr.Code, rr.Body)
	}
	e, _ := got.Load().(map[string]any)
	if e == nil || e["type"] != "test" || e["test"] != true {
		t.Fatalf("endpoint received %v, want a test event", e)
	}
}

func TestNotificationTestSendFailureIs502(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	store := newTestStore(t)
	seedNotifSinks(t, store, `{"sinks":[{"name":"hook","kind":"webhook","enabled":true,"configRef":"hook-secret"}]}`)
	r := notifTestRouter(store, notifSinkSecret("hook-secret", srv.URL))

	rr := doNotifTest(t, r, "hook", "10.10.0.4")
	if rr.Code != http.StatusBadGateway {
		t.Fatalf("got %d %s", rr.Code, rr.Body)
	}
}

func TestNotificationTestSendRateLimited(t *testing.T) {
	store := newTestStore(t)
	seedNotifSinks(t, store, `{"sinks":[]}`)
	r := notifTestRouter(store)

	const ip = "10.10.0.5" // dedicated bucket: burst is 3
	for i := 0; i < 3; i++ {
		if rr := doNotifTest(t, r, "nope", ip); rr.Code != http.StatusNotFound {
			t.Fatalf("call %d: got %d, want 404 (under the limit)", i, rr.Code)
		}
	}
	if rr := doNotifTest(t, r, "nope", ip); rr.Code != http.StatusTooManyRequests {
		t.Fatalf("got %d, want 429 after burst", rr.Code)
	}
}
