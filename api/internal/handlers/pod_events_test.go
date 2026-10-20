package handlers

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	kubefake "k8s.io/client-go/kubernetes/fake"

	"github.com/GameplanePanel/gameplane/api/internal/kube"
	"github.com/GameplanePanel/gameplane/api/internal/scope"
)

// scope.Resolve defaults to this namespace when no ?namespace is given.
const testNS = "gameplane-games"

func newEvent(name, kind, objName, evType, reason, msg, component string, t time.Time) *corev1.Event {
	return &corev1.Event{
		ObjectMeta:     metav1.ObjectMeta{Name: name, Namespace: testNS, UID: types.UID(name)},
		InvolvedObject: corev1.ObjectReference{Kind: kind, Name: objName, Namespace: testNS},
		Type:           evType,
		Reason:         reason,
		Message:        msg,
		Source:         corev1.EventSource{Component: component},
		LastTimestamp:  metav1.NewTime(t),
		Count:          1,
	}
}

func mountPodEventsRouter(k *kube.Client) http.Handler {
	r := chi.NewRouter()
	reg := kube.NewRegistry(scope.DefaultCluster)
	reg.Set(scope.DefaultCluster, k)
	MountPodEvents(r, reg)
	return r
}

func TestPodEvents_FiltersMapsAndSorts(t *testing.T) {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	k := &kube.Client{Typed: kubefake.NewClientset(
		newEvent("e1", "Pod", "alpha-0", "Normal", "Pulling", "Pulling image itzg/minecraft", "kubelet", base),
		newEvent("e2", "Pod", "alpha-0", "Warning", "Failed", "Back-off pulling image", "kubelet", base.Add(2*time.Minute)),
		newEvent("e3", "GameServer", "alpha", "Normal", "Scheduled", "assigned to node-1", "default-scheduler", base.Add(time.Minute)),
		newEvent("e4", "StatefulSet", "alpha", "Normal", "SuccessfulCreate", "created pod alpha-0", "statefulset-controller", base.Add(30*time.Second)),
		// Unrelated server — must be excluded.
		newEvent("e5", "Pod", "beta-0", "Normal", "Pulling", "Pulling image valheim", "kubelet", base.Add(3*time.Minute)),
	)}
	r := mountPodEventsRouter(k)

	rr := do(t, r, "GET", "/servers/alpha/events", nil)
	if rr.Code != 200 {
		t.Fatalf("got %d %s", rr.Code, rr.Body)
	}

	var got []PodEvent
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got) != 4 {
		t.Fatalf("got %d events, want 4 (alpha pod/gs/ss; beta excluded): %+v", len(got), got)
	}

	// Newest first: the Warning "Failed" at base+2m.
	if got[0].Reason != "Failed" || got[0].Type != "Warning" {
		t.Errorf("first event = %+v, want the Warning Failed (newest)", got[0])
	}
	if got[0].Object != "Pod/alpha-0" || got[0].Source != "kubelet" {
		t.Errorf("mapping wrong: object=%q source=%q", got[0].Object, got[0].Source)
	}
	if got[0].Message != "Back-off pulling image" {
		t.Errorf("message = %q", got[0].Message)
	}

	// Descending order overall.
	for i := 1; i < len(got); i++ {
		if got[i-1].Time < got[i].Time {
			t.Errorf("not sorted desc at %d: %q before %q", i, got[i-1].Time, got[i].Time)
		}
	}

	for _, e := range got {
		if e.Object == "Pod/beta-0" {
			t.Fatalf("beta event leaked into alpha's feed: %+v", e)
		}
	}
}

func TestPodEvents_EmptyForUnknownServer(t *testing.T) {
	k := &kube.Client{Typed: kubefake.NewClientset()}
	r := mountPodEventsRouter(k)

	rr := do(t, r, "GET", "/servers/ghost/events", nil)
	if rr.Code != 200 {
		t.Fatalf("got %d %s", rr.Code, rr.Body)
	}
	var got []PodEvent
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("want empty, got %+v", got)
	}
}
