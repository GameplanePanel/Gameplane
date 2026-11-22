//go:build e2e

package e2e

import (
	"context"
	"fmt"
	"testing"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
)

// a2sFixture answers Valve's A2S_INFO on UDP 27015, challenge round trip
// included, the way Steam dedicated servers such as Valheim do. /tmp/a2s
// holds "<players> <bots>"; while /tmp/a2s-silent exists it doesn't answer,
// so the agent's query times out.
const a2sFixture = `import pathlib,socket,struct
state=pathlib.Path('/tmp/a2s')
silent=pathlib.Path('/tmp/a2s-silent')
state.write_text('3 1')
request=b'\xff\xff\xff\xffTSource Engine Query\x00'
challenge=b'\x01\x02\x03\x04'
s=socket.socket(socket.AF_INET,socket.SOCK_DGRAM)
s.bind(('0.0.0.0',27015))
while True:
    data,peer=s.recvfrom(1400)
    if silent.exists() or not data.startswith(request): continue
    if data[len(request):]!=challenge:
        s.sendto(b'\xff\xff\xff\xffA'+challenge,peer); continue
    players,bots=(int(v) for v in state.read_text().split())
    s.sendto(b'\xff\xff\xff\xffI\x11e2e\x00map\x00e2e\x00e2e\x00'+struct.pack('<H',0)+bytes([players,10,bots])+b'dl\x00\x01e2e\x00',peer)
`

// TestGameServer_A2SPlayerCountDrivesIdle — a template's
// capabilities.players.query reaches the agent through the operator, the
// agent's heartbeat reports the A2S count with bots left out, and idle sleep
// follows it: a nonzero count keeps the idle clock stopped, a real zero
// starts it, and a failed query clears the count and the clock, so the
// server never sleeps on an unknown count.
func TestGameServer_A2SPlayerCountDrivesIdle(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	ns := "gameplane-games"
	name := fmt.Sprintf("e2e-a2s-%d", time.Now().UnixNano())

	tmpl := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "gameplane.local/v1alpha1", "kind": "GameTemplate",
		"metadata": map[string]any{"name": name},
		"spec": map[string]any{
			"displayName": "A2S fixture", "game": "noop", "version": "1",
			"image": "python:3.14-alpine", "consoleMode": "none",
			"command": []any{"python", "-u", "-c"}, "args": []any{a2sFixture},
			"ports": []any{map[string]any{"name": "query", "containerPort": int64(27015), "protocol": "UDP"}},
			"capabilities": map[string]any{"players": map[string]any{
				"query": map[string]any{"protocol": "a2s", "port": int64(27015)},
			}},
		},
	}}
	if _, err := envInstance.Dyn.Resource(gameTemplateGVR).
		Create(ctx, tmpl, metav1.CreateOptions{}); err != nil && !apierrors.IsAlreadyExists(err) {
		t.Fatalf("create template: %v", err)
	}
	t.Cleanup(func() {
		_ = envInstance.Dyn.Resource(gameTemplateGVR).Delete(context.Background(), name, metav1.DeleteOptions{})
	})
	applyBusyboxGameServer(t, ns, name, name)
	waitPVCBound(t, ns, name+"-data", 90*time.Second)
	requireAgentReady(t, ns, name)

	get := func() *unstructured.Unstructured {
		t.Helper()
		obj, err := envInstance.Dyn.Resource(gameServerGVR).Namespace(ns).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			t.Fatalf("get gameserver: %v", err)
		}
		return obj
	}
	exec := func(args ...string) {
		t.Helper()
		cmd := append([]string{"exec", "-n", ns, name + "-0", "-c", "game", "--"}, args...)
		if out, err := envInstance.Kubectl(ctx, cmd...); err != nil {
			t.Fatalf("exec %v: %v %s", args, err, out)
		}
	}

	// A nonzero count: 3 players, 1 of them a bot, reported as 2, with the
	// server's limit. Idle is enabled once it is Running.
	envInstance.Eventually(t, 3*time.Minute, func() (bool, string) {
		obj := get()
		if phase, _, _ := unstructured.NestedString(obj.Object, "status", "phase"); phase != "Running" {
			return false, "phase=" + phase
		}
		online, found, _ := unstructured.NestedInt64(obj.Object, "status", "agent", "playersOnline")
		if !found || online != 2 {
			return false, fmt.Sprintf("playersOnline=%d (found=%v), want 2", online, found)
		}
		if limit, _, _ := unstructured.NestedInt64(obj.Object, "status", "agent", "playersMax"); limit != 10 {
			return false, fmt.Sprintf("playersMax=%d, want 10", limit)
		}
		return true, ""
	})
	idlePatch := []byte(`{"spec":{"idle":{"enabled":true,"afterMinutes":5}}}`)
	if _, err := envInstance.Dyn.Resource(gameServerGVR).Namespace(ns).
		Patch(ctx, name, types.MergePatchType, idlePatch, metav1.PatchOptions{}); err != nil {
		t.Fatalf("patch idle: %v", err)
	}
	time.Sleep(15 * time.Second)
	if _, found, _ := unstructured.NestedString(get().Object, "status", "idle", "emptySince"); found {
		t.Fatal("idle clock started while players are online")
	}

	// A real zero: only a bot is left, so the count is 0 and the idle clock
	// starts.
	exec("python", "-c", `import pathlib; pathlib.Path('/tmp/a2s').write_text('1 1')`)
	envInstance.Eventually(t, 2*time.Minute, func() (bool, string) {
		obj := get()
		online, found, _ := unstructured.NestedInt64(obj.Object, "status", "agent", "playersOnline")
		if !found || online != 0 {
			return false, fmt.Sprintf("playersOnline=%d (found=%v), want 0", online, found)
		}
		if _, found, _ := unstructured.NestedString(obj.Object, "status", "idle", "emptySince"); !found {
			return false, "idle clock not started on a real zero"
		}
		return true, ""
	})

	// A failed query: the count is cleared, not left at 0, and the idle clock
	// is cleared with it. A clock backdated past the threshold must not sleep
	// the server.
	exec("touch", "/tmp/a2s-silent")
	envInstance.Eventually(t, 2*time.Minute, func() (bool, string) {
		obj := get()
		if online, found, _ := unstructured.NestedInt64(obj.Object, "status", "agent", "playersOnline"); found {
			return false, fmt.Sprintf("playersOnline=%d, want it cleared after a failed query", online)
		}
		return true, ""
	})
	past := time.Now().UTC().Add(-2 * time.Hour).Format(time.RFC3339)
	clockPatch := []byte(`{"status":{"idle":{"emptySince":"` + past + `"}}}`)
	if _, err := envInstance.Dyn.Resource(gameServerGVR).Namespace(ns).
		Patch(ctx, name, types.MergePatchType, clockPatch, metav1.PatchOptions{}, "status"); err != nil {
		t.Fatalf("backdate idle clock: %v", err)
	}
	time.Sleep(30 * time.Second)

	obj := get()
	if ann := obj.GetAnnotations(); ann["gameplane.local/idle-asleep-since"] != "" {
		t.Fatal("server slept after a failed player query")
	}
	if phase, _, _ := unstructured.NestedString(obj.Object, "status", "phase"); phase != "Running" {
		t.Errorf("phase = %q, want Running", phase)
	}
	if _, found, _ := unstructured.NestedString(obj.Object, "status", "idle", "emptySince"); found {
		t.Error("idle clock still running after a failed player query")
	}
}
