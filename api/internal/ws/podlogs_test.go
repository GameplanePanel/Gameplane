package ws

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/go-chi/chi/v5"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/rest"

	"github.com/GameplanePanel/gameplane/api/internal/kube"
)

// newPod builds a fake pod for the default namespace with a running game
// container and the given (already-terminated) init containers.
func newPod(name string, initNames []string, initFailed bool) *corev1.Pod {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "gameplane-games", UID: "pod-uid",
			OwnerReferences: []metav1.OwnerReference{{APIVersion: "apps/v1", Kind: "StatefulSet", Name: "alpha", UID: "ss-uid", Controller: boolPtr(true)}}},
		Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "game"}}},
		Status: corev1.PodStatus{
			ContainerStatuses: []corev1.ContainerStatus{
				{Name: "game", State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{}}},
			},
		},
	}
	for _, n := range initNames {
		code := int32(0)
		if initFailed {
			code = 1
		}
		pod.Spec.InitContainers = append(pod.Spec.InitContainers, corev1.Container{Name: n})
		pod.Status.InitContainerStatuses = append(pod.Status.InitContainerStatuses, corev1.ContainerStatus{
			Name:  n,
			State: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{ExitCode: code}},
		})
	}
	return pod
}

func dialPodLogs(t *testing.T, k *kube.Client, query string) (string, error) {
	t.Helper()
	r := chi.NewRouter()
	mountPodLogs(r, streamTestRegistry(k))
	srv := httptest.NewServer(r)
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	url := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws/servers/alpha/logs/pod" + query
	cli, resp, err := websocket.Dial(ctx, url, nil)
	if resp != nil && resp.Body != nil {
		defer resp.Body.Close()
	}
	if err != nil {
		return "", err
	}
	defer cli.Close(websocket.StatusNormalClosure, "")

	var b strings.Builder
	for {
		_, data, rerr := cli.Read(ctx)
		if rerr != nil {
			return b.String(), nil
		}
		b.Write(data)
	}
}

// from=start with only a game container: no phase marker, just the stream.
func TestPodLogs_StreamsGameOnly(t *testing.T) {
	k := &kube.Client{Typed: fake.NewSimpleClientset(newPod("alpha-0", nil, false))}
	out, err := dialPodLogs(t, k, "")
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	if !strings.Contains(out, "fake logs") {
		t.Errorf("out = %q, want it to contain fake logs", out)
	}
	if strings.Contains(out, "──") {
		t.Errorf("single container should emit no phase marker, got %q", out)
	}
}

// from=start stitches the init container then the game container, each
// labelled with a phase marker.
func TestPodLogs_StitchesInitThenGame(t *testing.T) {
	k := &kube.Client{Typed: fake.NewSimpleClientset(newPod("alpha-0", []string{"config-init"}, false))}
	out, err := dialPodLogs(t, k, "")
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	if !strings.Contains(out, "── config-init ──") || !strings.Contains(out, "── game ──") {
		t.Errorf("expected both phase markers, got %q", out)
	}
	if !strings.Contains(out, "fake logs") {
		t.Errorf("expected streamed output, got %q", out)
	}
}

// A failed setup container stops the timeline (the game never starts), so
// its output is shown and the game marker is not.
func TestPodLogs_StopsOnFailedInit(t *testing.T) {
	k := &kube.Client{Typed: fake.NewSimpleClientset(newPod("alpha-0", []string{"config-init"}, true))}
	out, err := dialPodLogs(t, k, "")
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	if !strings.Contains(out, "── config-init ──") {
		t.Errorf("expected config-init marker, got %q", out)
	}
	if strings.Contains(out, "── game ──") {
		t.Errorf("game should not stream after a failed init, got %q", out)
	}
}

// from=start with no pod yet closes cleanly (the client reconnects).
func TestPodLogs_NoPodYet(t *testing.T) {
	k := &kube.Client{Typed: fake.NewSimpleClientset()}
	if _, err := dialPodLogs(t, k, ""); err != nil {
		t.Fatalf("dial: %v", err)
	}
}

// from=end verifies ownership, then tails only the game container.
func TestPodLogs_TailsFromEnd(t *testing.T) {
	k := &kube.Client{Typed: fake.NewSimpleClientset(newPod("alpha-0", nil, false))}
	out, err := dialPodLogs(t, k, "?from=end")
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	if !strings.Contains(out, "fake logs") {
		t.Errorf("out = %q, want fake logs", out)
	}
}

func boolPtr(b bool) *bool { return &b }

// streamTestRegistry supplies the real operator ownership chain for stream
// tests. An absent Pod remains absent to exercise provisioning retries.
func streamTestRegistry(k *kube.Client) *kube.Registry {
	gs := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "gameplane.local/v1alpha1", "kind": "GameServer",
		"metadata": map[string]any{"name": "alpha", "namespace": "gameplane-games", "uid": "gs-uid"},
	}}
	k.Dynamic = dynamicfake.NewSimpleDynamicClient(runtime.NewScheme(), gs)
	if k.Typed == nil {
		k.Typed = fake.NewSimpleClientset(newPod("alpha-0", nil, false))
	}
	_, _ = k.Typed.AppsV1().StatefulSets("gameplane-games").Create(context.Background(), &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{Name: "alpha", Namespace: "gameplane-games", UID: "ss-uid",
			OwnerReferences: []metav1.OwnerReference{{APIVersion: "gameplane.local/v1alpha1", Kind: "GameServer", Name: "alpha", UID: "gs-uid", Controller: boolPtr(true)}}},
	}, metav1.CreateOptions{})
	k.Config = &rest.Config{Host: "https://unused.invalid"}
	reg := kube.NewRegistry("local")
	reg.Set("local", k)
	return reg
}
