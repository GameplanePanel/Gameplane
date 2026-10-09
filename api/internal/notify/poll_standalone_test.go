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
}

func TestStandaloneNotificationPollDoesNotPairRecreatedServer(t *testing.T) {
	server := gsObj(scope.DefaultNamespace, "game", "Failed", "False", "Failed", "", "")
	server.SetUID("old")
	k := pollTestClient(server)
	n := &Notifier{ch: make(chan Event, 10)}
	state := newClusterSnapshot()
	n.pollClusterSnapshot(t.Context(), k, "remote", state)
	state.alerted[scope.DefaultNamespace+"/game"] = true
	replacement := server.DeepCopy()
	replacement.SetUID("new")
	if err := unstructured.SetNestedSlice(replacement.Object, []any{map[string]any{"type": "Healthy", "status": "True"}}, "status", "conditions"); err != nil {
		t.Fatal(err)
	}
	if _, err := k.Dynamic.Resource(kube.GVRs["servers"]).Namespace(scope.DefaultNamespace).Update(t.Context(), replacement, metav1.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	n.pollClusterSnapshot(t.Context(), k, "remote", state)
	if len(n.ch) != 0 {
		t.Fatal("replacement inherited old outage")
	}
}
