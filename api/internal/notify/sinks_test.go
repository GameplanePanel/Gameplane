package notify

import (
	"context"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kubefake "k8s.io/client-go/kubernetes/fake"

	"github.com/GameplanePanel/gameplane/api/internal/db"
	"github.com/GameplanePanel/gameplane/api/internal/kube"
)

func newTestStore(t *testing.T) *db.Store {
	t.Helper()
	store, err := db.Open(context.Background(), "sqlite", "file::memory:?_pragma=journal_mode(WAL)&cache=shared")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return store
}

func putConfig(t *testing.T, store *db.Store, key, value string) {
	t.Helper()
	_, err := store.DB.ExecContext(context.Background(),
		`INSERT INTO config(key, value, updated_at) VALUES (?, ?, datetime('now'))
		 ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key, value)
	if err != nil {
		t.Fatalf("put config %s: %v", key, err)
	}
}

func TestSinkMatches(t *testing.T) {
	cases := []struct {
		name string
		s    Sink
		t    EventType
		want bool
	}{
		{"disabled never matches", Sink{Enabled: false}, EventBackupFailed, false},
		{"empty filter uses defaults: failure on", Sink{Enabled: true}, EventBackupFailed, true},
		{"empty filter uses defaults: success off", Sink{Enabled: true}, EventBackupSucceeded, false},
		{"explicit filter matches", Sink{Enabled: true, Events: []string{"backup.succeeded"}}, EventBackupSucceeded, true},
		{"explicit filter is authoritative", Sink{Enabled: true, Events: []string{"backup.succeeded"}}, EventServerUnhealthy, false},
	}
	for _, tc := range cases {
		if got := sinkMatches(tc.s, tc.t); got != tc.want {
			t.Errorf("%s: sinkMatches = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestLoadSinks(t *testing.T) {
	store := newTestStore(t)
	n := &Notifier{store: store}

	sinks, err := n.loadSinks(context.Background())
	if err != nil || sinks != nil {
		t.Fatalf("no config row: got %v, %v; want nil, nil", sinks, err)
	}

	putConfig(t, store, "notifications",
		`{"sinks":[{"name":"a","kind":"discord","enabled":true,"configRef":"hook","events":["backup.failed"]},{"name":"b","kind":"smtp","enabled":false}]}`)
	sinks, err = n.loadSinks(context.Background())
	if err != nil {
		t.Fatalf("loadSinks: %v", err)
	}
	if len(sinks) != 2 || sinks[0].ConfigRef != "hook" || sinks[0].Events[0] != "backup.failed" || sinks[1].Enabled {
		t.Fatalf("sinks = %+v", sinks)
	}

	putConfig(t, store, "notifications", `not json`)
	if _, err := n.loadSinks(context.Background()); err == nil {
		t.Fatal("corrupt row: expected error")
	}
}

func TestSinkSecret(t *testing.T) {
	labelled := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name: "hook", Namespace: "gameplane-system",
			Labels: map[string]string{SinkSecretLabel: "true"},
		},
		Data: map[string][]byte{"url": []byte("https://example.com/x")},
	}
	bare := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "other", Namespace: "gameplane-system"},
	}
	n := &Notifier{
		k:         &kube.Client{Typed: kubefake.NewClientset(labelled, bare)},
		controlNS: "gameplane-system",
	}

	data, err := n.sinkSecret(context.Background(), "hook")
	if err != nil {
		t.Fatalf("labelled secret: %v", err)
	}
	if string(data["url"]) != "https://example.com/x" {
		t.Fatalf("data = %v", data)
	}

	if _, err := n.sinkSecret(context.Background(), "other"); err == nil || !strings.Contains(err.Error(), SinkSecretLabel) {
		t.Fatalf("unlabelled secret: err = %v, want label error", err)
	}
	if _, err := n.sinkSecret(context.Background(), "missing"); err == nil {
		t.Fatal("missing secret: expected error")
	}
}
