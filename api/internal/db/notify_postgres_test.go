//go:build postgres

package db_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/rest"

	"github.com/GameplanePanel/gameplane/api/internal/controlplane"
	"github.com/GameplanePanel/gameplane/api/internal/db"
	"github.com/GameplanePanel/gameplane/api/internal/kube"
	"github.com/GameplanePanel/gameplane/api/internal/notify"
	"github.com/GameplanePanel/gameplane/api/internal/scope"
)

func TestPostgresStandaloneNotificationPollDeliversDistinctVersions(t *testing.T) {
	ctx := t.Context()
	store := db.NewPostgresTestStore(t)
	control, err := controlplane.New(ctx, store, filepath.Join(t.TempDir(), "panel.key"))
	if err != nil {
		t.Fatal(err)
	}
	delivered := make(chan notify.Event, 8)
	sink := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		var event notify.Event
		if err := json.NewDecoder(req.Body).Decode(&event); err != nil {
			t.Errorf("decode webhook: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		delivered <- event
		w.WriteHeader(http.StatusNoContent)
	}))
	defer sink.Close()
	_, err = control.Secrets("panel").Create(ctx, &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "ops-hook", Namespace: "panel", Labels: map[string]string{notify.SinkSecretLabel: "true"}},
		Data:       map[string][]byte{"url": []byte(sink.URL)},
	}, metav1.CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	cfg := `{"sinks":[{"name":"ops","kind":"webhook","enabled":true,"configRef":"ops-hook","events":["backup.failed"]}]}`
	if _, err := store.DB.ExecContext(ctx, "INSERT INTO config(key,value) VALUES ('notifications',?) ON CONFLICT(key) DO UPDATE SET value=excluded.value", cfg); err != nil {
		t.Fatal(err)
	}

	// List through the real Kubernetes wire decoder and the standalone poller.
	// A repeated snapshot of the first failure must keep its dispatch identity.
	// Two opaque versions with the same long prefix must still be distinguishable.
	prefix := strings.Repeat("opaque/", 40)
	snapshots := []struct{ phase, version, message string }{
		{"Running", "initial", ""}, {"Failed", prefix + "a", "first"},
		{"Running", "initial", ""}, {"Failed", prefix + "a", "first"},
		{"Running", "retried", ""}, {"Failed", prefix + "b", "second"},
	}
	var polls atomic.Int32
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if req.URL.Path != "/apis/gameplane.local/v1alpha1/namespaces/"+scope.DefaultNamespace+"/backups" {
			fmt.Fprint(w, `{"apiVersion":"v1","kind":"List","items":[]}`)
			return
		}
		s := snapshots[min(int(polls.Add(1))-1, len(snapshots)-1)]
		fmt.Fprintf(w, `{"apiVersion":"gameplane.local/v1alpha1","kind":"BackupList","items":[{
			"apiVersion":"gameplane.local/v1alpha1","kind":"Backup",
			"metadata":{"namespace":%q,"name":"world","uid":"backup-uid","resourceVersion":%q},
			"status":{"phase":%q,"message":%q}}]}`, scope.DefaultNamespace, s.version, s.phase, s.message)
	}))
	defer api.Close()
	remote, err := kube.New(&rest.Config{Host: api.URL})
	if err != nil {
		t.Fatal(err)
	}
	reg := kube.NewRegistry("edge")
	reg.Set("edge", remote)
	workerCtx, cancel := context.WithTimeout(ctx, time.Minute)
	done := make(chan struct{})
	go func() {
		defer close(done)
		notify.New(store, control, "panel").RunWithRegistry(workerCtx, reg)
	}()
	defer func() { cancel(); <-done }()
	for _, want := range []string{"first", "second"} {
		select {
		case event := <-delivered:
			if event.Cluster != "edge" || event.Type != notify.EventBackupFailed || event.Message != want {
				t.Fatalf("wanted %s failure once, got %+v", want, event)
			}
		case <-workerCtx.Done():
			t.Fatalf("missing %s webhook after %d remote lists", want, polls.Load())
		}
	}
	var claims, longest int
	if err := store.DB.QueryRowContext(ctx, `SELECT COUNT(*), MAX(octet_length(object_version))
		FROM notification_claims WHERE cluster='edge' AND object_uid='backup-uid' AND sink_name='ops'`).Scan(&claims, &longest); err != nil {
		t.Fatal(err)
	}
	if claims != 2 || longest >= len(prefix) {
		t.Fatalf("got %d claims with longest key %d bytes", claims, longest)
	}
}
