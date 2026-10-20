package notify

import (
	"context"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"

	"github.com/GameplanePanel/gameplane/api/internal/kube"
)

// gsObj builds a GameServer with the status fields the watcher reads. The
// Ready condition carries reason/message like the operator's Failed path.
func gsObj(ns, name, phase, healthy, healthyReason, readyReason, readyMsg string) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "gameplane.local/v1alpha1",
		"kind":       "GameServer",
		"metadata":   map[string]any{"name": name, "namespace": ns},
		"status": map[string]any{
			"phase": phase,
			"conditions": []any{
				map[string]any{"type": "Healthy", "status": healthy, "reason": healthyReason},
				map[string]any{"type": "Ready", "status": "False", "reason": readyReason, "message": readyMsg},
			},
		},
	}}
}

func phaseObj(kind, ns, name, phase, message string) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "gameplane.local/v1alpha1",
		"kind":       kind,
		"metadata":   map[string]any{"name": name, "namespace": ns},
		"status":     map[string]any{"phase": phase, "message": message},
	}}
}

func TestServerEvents(t *testing.T) {
	running := gsObj("games", "mc", "Running", "True", "AgentFresh", "Running", "")
	failed := gsObj("games", "mc", "Failed", "False", "Failed", "ImagePullFailed", "cannot pull the image — check the image reference")
	stale := gsObj("games", "mc", "Starting", "False", "AgentStale", "Starting", "")
	stopping := gsObj("games", "mc", "Stopping", "False", "Stopping", "Stopping", "")
	suspended := gsObj("games", "mc", "Suspended", "False", "Suspended", "Suspended", "")
	pending := gsObj("games", "mc", "Pending", "False", "Unknown", "Unknown", "")

	cases := []struct {
		name        string
		old, new    *unstructured.Unstructured
		alerted     bool
		wantTypes   []EventType
		wantAlerted bool
		wantReason  string
	}{
		{"escalation to Failed alerts", running, failed, false, []EventType{EventServerUnhealthy}, true, "ImagePullFailed"},
		{"first-boot Failed alerts", pending, failed, false, []EventType{EventServerUnhealthy}, true, "ImagePullFailed"},
		{"heartbeat loss alerts", running, stale, false, []EventType{EventServerUnhealthy}, true, "AgentStale"},
		{"stale steady-state is silent", stale, stale, true, nil, true, ""},
		{"Failed steady-state is silent", failed, failed, true, nil, true, ""},
		{"already-alerted Failed escalation is silent", stale, failed, true, nil, true, ""},
		{"user stop is silent", running, stopping, false, nil, false, ""},
		{"suspend is silent", stopping, suspended, false, nil, false, ""},
		{"ordinary start is silent", stale, running, false, nil, false, ""},
		{"recovery pairs with an alert", stale, running, true, []EventType{EventServerRecovered}, false, "AgentFresh"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			events, nowAlerted := serverEvents(tc.old, tc.new, tc.alerted)
			if len(events) != len(tc.wantTypes) {
				t.Fatalf("events = %+v, want types %v", events, tc.wantTypes)
			}
			for i, want := range tc.wantTypes {
				if events[i].Type != want {
					t.Errorf("events[%d].Type = %s, want %s", i, events[i].Type, want)
				}
				if events[i].Reason != tc.wantReason {
					t.Errorf("events[%d].Reason = %q, want %q", i, events[i].Reason, tc.wantReason)
				}
				if events[i].Kind != "GameServer" || events[i].Namespace != "games" || events[i].Name != "mc" {
					t.Errorf("events[%d] identity = %+v", i, events[i])
				}
			}
			if nowAlerted != tc.wantAlerted {
				t.Errorf("alerted = %v, want %v", nowAlerted, tc.wantAlerted)
			}
		})
	}
}

func TestPhaseEvents(t *testing.T) {
	cases := []struct {
		name     string
		kind     string
		old, new *unstructured.Unstructured
		want     EventType // "" = no event
		wantMsg  string
	}{
		{"backup failure", "Backup", phaseObj("Backup", "games", "nightly", "Running", ""),
			phaseObj("Backup", "games", "nightly", "Failed", "restic exit 1"), EventBackupFailed, "restic exit 1"},
		{"backup success", "Backup", phaseObj("Backup", "games", "nightly", "Running", ""),
			phaseObj("Backup", "games", "nightly", "Succeeded", ""), EventBackupSucceeded, ""},
		{"restore failure", "Restore", phaseObj("Restore", "games", "r1", "Running", ""),
			phaseObj("Restore", "games", "r1", "Failed", "snapshot missing"), EventRestoreFailed, "snapshot missing"},
		{"restore success", "Restore", phaseObj("Restore", "games", "r1", "Resuming", ""),
			phaseObj("Restore", "games", "r1", "Succeeded", ""), EventRestoreSucceeded, ""},
		{"intermediate transition is silent", "Backup", phaseObj("Backup", "games", "nightly", "Pending", ""),
			phaseObj("Backup", "games", "nightly", "Running", ""), "", ""},
		{"resync same phase is silent", "Backup", phaseObj("Backup", "games", "nightly", "Failed", "x"),
			phaseObj("Backup", "games", "nightly", "Failed", "x"), "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			events := phaseEvents(tc.kind, tc.old, tc.new)
			if tc.want == "" {
				if len(events) != 0 {
					t.Fatalf("events = %+v, want none", events)
				}
				return
			}
			if len(events) != 1 || events[0].Type != tc.want {
				t.Fatalf("events = %+v, want one %s", events, tc.want)
			}
			if events[0].Message != tc.wantMsg {
				t.Errorf("message = %q, want %q", events[0].Message, tc.wantMsg)
			}
		})
	}
}

// notifiableGVRs mirrors the list kinds the watcher's informers need.
func fakeDynamic(objs ...runtime.Object) *dynamicfake.FakeDynamicClient {
	return dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(),
		map[schema.GroupVersionResource]string{
			kube.GVRs["servers"]:  "GameServerList",
			kube.GVRs["backups"]:  "BackupList",
			kube.GVRs["restores"]: "RestoreList",
		}, objs...)
}

// TestRunWatchers drives the informer plumbing end to end with a fake
// dynamic client: seed → sync (silent) → out-of-scope update (dropped) →
// in-scope transitions (enqueued).
func TestRunWatchers(t *testing.T) {
	const ns = "gameplane-games" // scope.DefaultNamespace — the allow-list default
	seedGS := gsObj(ns, "mc", "Running", "True", "AgentFresh", "Running", "")
	seedBackup := phaseObj("Backup", ns, "nightly", "Running", "")
	outOfScope := gsObj("other-ns", "rogue", "Running", "True", "AgentFresh", "Running", "")
	dyn := fakeDynamic(seedGS, seedBackup, outOfScope)

	n := &Notifier{k: &kube.Client{Dynamic: dyn}, ch: make(chan Event, 8)}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	n.runWatchers(ctx) // returns after cache sync — seeding emits nothing

	// An out-of-scope namespace must be filtered even though the informer
	// is cluster-wide.
	if _, err := dyn.Resource(kube.GVRs["servers"]).Namespace("other-ns").Update(ctx,
		gsObj("other-ns", "rogue", "Failed", "False", "Failed", "ImagePullFailed", "nope"), metav1.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	// In-scope: server fails, then the backup fails.
	if _, err := dyn.Resource(kube.GVRs["servers"]).Namespace(ns).Update(ctx,
		gsObj(ns, "mc", "Failed", "False", "Failed", "CrashLoopBackOff", "crash-looped 3 times"), metav1.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := dyn.Resource(kube.GVRs["backups"]).Namespace(ns).Update(ctx,
		phaseObj("Backup", ns, "nightly", "Failed", "restic exit 1"), metav1.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}

	// Collect until both transitions have been seen. Fake-client objects
	// carry no resourceVersion, so the informer's list/watch plumbing can
	// deliver the same logical transition more than once — a real
	// apiserver-backed informer can't. Assert set membership and identity
	// rather than exact counts; the namespace check is what proves the
	// out-of-scope update was filtered.
	wantName := map[EventType]string{
		EventServerUnhealthy: "mc",
		EventBackupFailed:    "nightly",
	}
	got := map[EventType]bool{}
	deadline := time.After(5 * time.Second)
	for !got[EventServerUnhealthy] || !got[EventBackupFailed] {
		select {
		case e := <-n.ch:
			if e.Namespace != ns {
				t.Fatalf("event from unexpected namespace: %+v", e)
			}
			name, ok := wantName[e.Type]
			if !ok || e.Name != name {
				t.Fatalf("unexpected event %+v", e)
			}
			if e.TS == "" {
				t.Errorf("event %s has no timestamp", e.Type)
			}
			got[e.Type] = true
		case <-deadline:
			t.Fatalf("timed out; got %v", got)
		}
	}
}
