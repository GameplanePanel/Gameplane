package notify

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	kubefake "k8s.io/client-go/kubernetes/fake"

	"github.com/GameplanePanel/gameplane/api/internal/kube"
)

func TestPostgresNotifierDispatchesEachObservedVersionOncePerSink(t *testing.T) {
	store := newTestStore(t)
	putConfig(t, store, "notifications", `{"sinks":[{"name":"ops","kind":"webhook","enabled":true,"configRef":"hook","events":["backup.failed"]}]}`)
	// The claim statement is portable. The cmd PostgreSQL test races real
	// independent pgx pools after terminating the worker leader's backend.
	store.Driver = "postgres"
	var sent atomic.Int32
	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { sent.Add(1); w.WriteHeader(http.StatusNoContent) }))
	defer endpoint.Close()
	secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "hook", Namespace: "panel", Labels: map[string]string{SinkSecretLabel: "true"}}, Data: map[string][]byte{"url": []byte(endpoint.URL)}}
	replicas := []*Notifier{
		New(store, &kube.Client{Typed: kubefake.NewClientset(secret.DeepCopy())}, "panel"),
		New(store, &kube.Client{Typed: kubefake.NewClientset(secret.DeepCopy())}, "panel"),
	}
	for _, n := range replicas {
		n.client = testNotifier().client
	}
	obj := &unstructured.Unstructured{}
	obj.SetUID("uid")
	obj.SetResourceVersion("37")
	event := Event{Type: EventBackupFailed, Kind: "Backup", Name: "world", Namespace: "games"}
	var calls sync.WaitGroup
	for _, n := range replicas {
		calls.Go(func() { n.enqueueObserved(event, "edge", obj); n.fanOut(t.Context(), <-n.ch) })
	}
	calls.Wait()
	if got := sent.Load(); got != 1 {
		t.Fatalf("HTTP deliveries from overlapping workers: %d", got)
	}
	obj.SetResourceVersion("38")
	n := replicas[1]
	n.enqueueObserved(event, "edge", obj)
	n.fanOut(t.Context(), <-n.ch)
	if got := sent.Load(); got != 2 {
		t.Fatalf("distinct update lost after prior delivery: %d", got)
	}
}
