package notify

import (
	"fmt"
	"testing"

	"github.com/GameplanePanel/gameplane/api/internal/kube"
	"github.com/GameplanePanel/gameplane/api/internal/scope"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"
)

func pollTestClient(objects ...runtime.Object) *kube.Client {
	kinds := map[schema.GroupVersionResource]string{
		kube.GVRs["servers"]: "GameServerList", kube.GVRs["backups"]: "BackupList", kube.GVRs["restores"]: "RestoreList",
	}
	return &kube.Client{Dynamic: dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), kinds, objects...)}
}

func TestStandaloneNotificationPollReportsNewTerminalObjectsAfterBaseline(t *testing.T) {
	k := pollTestClient()
	n := &Notifier{ch: make(chan Event, 10)}
	state := newClusterSnapshot()
	n.pollClusterSnapshot(t.Context(), k, "remote", state)
	for _, kind := range []string{"Backup", "Restore", "GameServer"} {
		resource, phase := "backups", "Succeeded"
		if kind == "Restore" {
			resource, phase = "restores", "Failed"
		}
		if kind == "GameServer" {
			resource, phase = "servers", "Failed"
		}
		obj := &unstructured.Unstructured{Object: map[string]any{"apiVersion": "gameplane.local/v1alpha1", "kind": kind, "status": map[string]any{"phase": phase}}}
		obj.SetName("new-object")
		obj.SetNamespace(scope.DefaultNamespace)
		obj.SetUID("new-identity")
		if _, err := k.Dynamic.Resource(kube.GVRs[resource]).Namespace(scope.DefaultNamespace).Create(t.Context(), obj, metav1.CreateOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	n.pollClusterSnapshot(t.Context(), k, "remote", state)
	if len(n.ch) != 3 {
		t.Fatalf("missed newly observed terminal objects: %d events", len(n.ch))
	}
	n.pollClusterSnapshot(t.Context(), k, "remote", state)
	if len(n.ch) != 3 {
		t.Fatal("stable terminal objects emitted duplicate alerts")
	}
}

func TestStandaloneNotificationPollScopesBeforeBounding(t *testing.T) {
	objects := make([]runtime.Object, 0, 1101)
	for i := range 1100 {
		objects = append(objects, gsObj("ignored", fmt.Sprintf("server-%d", i), "Running", "True", "AgentFresh", "", ""))
	}
	objects = append(objects, gsObj(scope.DefaultNamespace, "visible", "Running", "True", "AgentFresh", "", ""))
	n := &Notifier{ch: make(chan Event, 10)}
	state := newClusterSnapshot()
	n.pollClusterSnapshot(t.Context(), pollTestClient(objects...), "remote", state)
	if len(state.previous["servers"]) != 1 {
		t.Fatal("ignored namespace exhausted notification snapshot")
	}
}

func TestStandaloneNotificationPollPreservesTransitions(t *testing.T) {
	server := gsObj(scope.DefaultNamespace, "game", "Running", "True", "AgentFresh", "", "")
	server.SetUID("original")
	k := pollTestClient(server)
	n := &Notifier{ch: make(chan Event, 10)}
	state := newClusterSnapshot()
	n.pollClusterSnapshot(t.Context(), k, "remote", state)
	if len(n.ch) != 0 {
		t.Fatal("initial snapshot emitted alerts")
	}
	failed := gsObj(scope.DefaultNamespace, "game", "Failed", "False", "Failed", "ImagePullFailed", "cannot pull")
	failed.SetUID("original")
	if _, err := k.Dynamic.Resource(kube.GVRs["servers"]).Namespace(scope.DefaultNamespace).Update(t.Context(), failed, metav1.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	n.pollClusterSnapshot(t.Context(), k, "remote", state)
	select {
	case e := <-n.ch:
		if e.Cluster != "remote" || e.Type != EventServerUnhealthy || e.Reason != "ImagePullFailed" {
			t.Fatalf("wrong event: %+v", e)
		}
	default:
		t.Fatal("failure transition was missed")
	}
	n.pollClusterSnapshot(t.Context(), k, "remote", state)
	if len(n.ch) != 0 {
		t.Fatal("stable failure emitted duplicate alerts")
	}
	if _, err := k.Dynamic.Resource(kube.GVRs["servers"]).Namespace(scope.DefaultNamespace).Update(t.Context(), server, metav1.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	n.pollClusterSnapshot(t.Context(), k, "remote", state)
	select {
	case e := <-n.ch:
		if e.Type != EventServerRecovered {
			t.Fatalf("wrong recovery: %+v", e)
		}
	default:
		t.Fatal("recovery transition was missed")
	}
}

func TestStandaloneNotificationPollRejectsExcessObjects(t *testing.T) {
	objects := make([]runtime.Object, 0, 1100)
	for i := range 1100 {
		objects = append(objects, gsObj(scope.DefaultNamespace, fmt.Sprintf("server-%d", i), "Running", "True", "AgentFresh", "", ""))
	}
	n := &Notifier{ch: make(chan Event, 10)}
	state := newClusterSnapshot()
	n.pollClusterSnapshot(t.Context(), pollTestClient(objects...), "remote", state)
	if len(state.previous["servers"]) != 0 {
		t.Fatal("oversize remote inventory retained objects")
	}
	if len(n.ch) != 0 {
		t.Fatal("rejected inventory emitted alerts")
	}
	firstWarning := state.lastWarning["servers"]
	if firstWarning.IsZero() {
		t.Fatal("oversize snapshot was silently rejected")
	}
	n.pollClusterSnapshot(t.Context(), pollTestClient(objects...), "remote", state)
	if state.lastWarning["servers"] != firstWarning {
		t.Fatal("snapshot warnings were not rate limited")
	}
}

func TestStandaloneNotificationPollDoesNotPairRecreatedServer(t *testing.T) {
	for _, tc := range []struct {
		phase, healthy string
		wantFailure    bool
	}{{"Running", "True", false}, {"Failed", "False", true}} {
		t.Run(tc.phase, func(t *testing.T) {
			server := gsObj(scope.DefaultNamespace, "game", "Failed", "False", "Failed", "", "")
			server.SetUID("old")
			k := pollTestClient(server)
			n := &Notifier{ch: make(chan Event, 10)}
			state := newClusterSnapshot()
			n.pollClusterSnapshot(t.Context(), k, "remote", state)
			state.alerted[scope.DefaultNamespace+"/game"] = true
			replacement := gsObj(scope.DefaultNamespace, "game", tc.phase, tc.healthy, "AgentFresh", "ReplacementFailed", "new failure")
			replacement.SetUID("new")
			if _, err := k.Dynamic.Resource(kube.GVRs["servers"]).Namespace(scope.DefaultNamespace).Update(t.Context(), replacement, metav1.UpdateOptions{}); err != nil {
				t.Fatal(err)
			}
			n.pollClusterSnapshot(t.Context(), k, "remote", state)
			if tc.wantFailure {
				select {
				case event := <-n.ch:
					if event.Type != EventServerUnhealthy || event.Reason != "ReplacementFailed" {
						t.Fatalf("replacement did not report its own failure: %+v", event)
					}
				default:
					t.Fatal("replacement failure was suppressed by the old outage")
				}
			}
			if len(n.ch) != 0 {
				t.Fatal("replacement inherited an old recovery or emitted extra events")
			}
			n.pollClusterSnapshot(t.Context(), k, "remote", state)
			if len(n.ch) != 0 {
				t.Fatal("stable replacement emitted duplicate events")
			}
		})
	}
}
