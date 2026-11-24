package handlers

import (
	"context"
	"net/http"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"

	"github.com/GameplanePanel/gameplane/api/internal/kube"
)

func templateObj(name string, labels map[string]string) *unstructured.Unstructured {
	o := newServerObj("", name)
	o.Object["apiVersion"] = "gameplane.local/v1alpha1"
	o.Object["kind"] = "GameTemplate"
	delete(o.Object["metadata"].(map[string]any), "namespace")
	if labels != nil {
		o.SetLabels(labels)
	}
	return o
}

// gameServerWithTunnel builds a GameServer with spec.networking.tunnel set,
// its UID fixed so a Secret's ownerReference can be pinned to match it.
func gameServerWithTunnel(ns, name, uid, provider string) *unstructured.Unstructured {
	o := newServerObj(ns, name)
	o.SetUID(types.UID(uid))
	o.Object["spec"].(map[string]any)["networking"] = map[string]any{
		"expose": "ClusterIP",
		"tunnel": map[string]any{"enabled": true, "provider": provider},
	}
	return o
}

// ownedTunnelAuthSecret builds a <server>-tunnel-auth Secret owned by the
// named GameServer (matching isServerOwnedSecretObject), pre-populated with
// data (as if StringData had already been synced to Data, as the real API
// server does).
func ownedTunnelAuthSecret(ns, serverName, uid string, data map[string]string) *corev1.Secret {
	byteData := make(map[string][]byte, len(data))
	for k, v := range data {
		byteData[k] = []byte(v)
	}
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      serverName + "-tunnel-auth",
			Namespace: ns,
			OwnerReferences: []metav1.OwnerReference{
				{Kind: "GameServer", Name: serverName, UID: types.UID(uid)},
			},
		},
		Data: byteData,
	}
}

// updateHandler's success paths (namespaced + cluster) and its own decode
// error path are distinct from createHandler's; the existing fake tests
// only reach the managed-template block. Cover the rest here.

func TestResources_Update_NamespacedSuccess(t *testing.T) {
	k := fakeKubeClient(newServerObj("gameplane-games", "alpha"))
	r := mountResourcesRouter(k)
	body := map[string]any{
		"apiVersion": "gameplane.local/v1alpha1",
		"kind":       "GameServer",
		"metadata":   map[string]any{"name": "alpha", "namespace": "gameplane-games"},
		"spec":       map[string]any{"templateRef": map[string]any{"name": "minecraft"}, "suspended": true},
	}
	rr := do(t, r, "PUT", "/servers/alpha", body)
	if rr.Code != http.StatusOK {
		t.Fatalf("put namespaced: got %d %s, want 200", rr.Code, rr.Body)
	}
}

func TestResources_Update_ClusterUnmanagedSuccess(t *testing.T) {
	k := fakeKubeClient(templateObj("minecraft", nil)) // no managed-by label
	r := mountResourcesRouter(k)
	body := map[string]any{
		"apiVersion": "gameplane.local/v1alpha1",
		"kind":       "GameTemplate",
		"metadata":   map[string]any{"name": "minecraft"},
		"spec":       map[string]any{"image": "y", "game": "minecraft", "version": "2"},
	}
	rr := do(t, r, "PUT", "/templates/minecraft", body)
	if rr.Code != http.StatusOK {
		t.Fatalf("put cluster unmanaged: got %d %s, want 200", rr.Code, rr.Body)
	}
}

func TestResources_Update_BadJSON(t *testing.T) {
	k := fakeKubeClient(newServerObj("gameplane-games", "alpha"))
	r := mountResourcesRouter(k)
	rr := doRaw(t, r, "PUT", "/servers/alpha", "not json")
	if rr.Code < 400 {
		t.Fatalf("put bad json: got %d, want a client/server error", rr.Code)
	}
}

// A managed template missing the module-name label falls back to the
// template name in the conflict message.
func TestResources_ManagedTemplate_ModNameFallback(t *testing.T) {
	k := fakeKubeClient(templateObj("minecraft", map[string]string{"gameplane.local/managed-by": "Module"}))
	r := mountResourcesRouter(k)
	body := map[string]any{
		"apiVersion": "gameplane.local/v1alpha1",
		"kind":       "GameTemplate",
		"metadata":   map[string]any{"name": "minecraft"},
		"spec":       map[string]any{"image": "x"},
	}
	rr := do(t, r, "PUT", "/templates/minecraft", body)
	if rr.Code != http.StatusConflict {
		t.Fatalf("put managed (no module-name): got %d %s, want 409", rr.Code, rr.Body)
	}
	if !strings.Contains(rr.Body.String(), "minecraft") {
		t.Fatalf("conflict message should name the template: %s", rr.Body.String())
	}
}

// A GameServer PUT that switches spec.networking.tunnel.provider must prune
// the superseded provider's key from <name>-tunnel-auth once the spec write
// succeeds (pruneTunnelProviderOnSpecChange), closing the gap left by the
// credential PUT, which deliberately keeps the still-active provider's key
// alive during an in-flight switch.
func TestResources_Update_TunnelProviderSwitch_PrunesStaleCredential(t *testing.T) {
	const ns, name, uid = "gameplane-games", "alpha", "test-uid-1"
	gs := gameServerWithTunnel(ns, name, uid, "frp")
	secret := ownedTunnelAuthSecret(ns, name, uid, map[string]string{"token": "frp-token", "authKey": "ts-key"})
	k := fakeKubeClient(gs)
	if _, err := k.Typed.CoreV1().Secrets(ns).Create(t.Context(), secret, metav1.CreateOptions{}); err != nil {
		t.Fatalf("seed secret: %v", err)
	}
	r := mountResourcesRouter(k)

	body := map[string]any{
		"apiVersion": "gameplane.local/v1alpha1",
		"kind":       "GameServer",
		"metadata":   map[string]any{"name": name, "namespace": ns, "uid": uid},
		"spec": map[string]any{
			"templateRef": map[string]any{"name": "minecraft"},
			"networking": map[string]any{
				"expose": "ClusterIP",
				"tunnel": map[string]any{"enabled": true, "provider": "tailscale"},
			},
		},
	}
	rr := do(t, r, "PUT", "/servers/"+name, body)
	if rr.Code != http.StatusOK {
		t.Fatalf("put: got %d %s, want 200", rr.Code, rr.Body)
	}

	got, err := k.Typed.CoreV1().Secrets(ns).Get(t.Context(), name+"-tunnel-auth", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get secret: %v", err)
	}
	if _, ok := got.Data["token"]; ok {
		t.Fatalf("frp token still present after provider switch: %v", got.Data)
	}
	if string(got.Data["authKey"]) != "ts-key" {
		t.Fatalf("authKey = %q, want ts-key (new provider's key must survive)", got.Data["authKey"])
	}
}

// An update that leaves spec.networking.tunnel.provider unchanged must not
// touch the tunnel-auth Secret at all.
func TestResources_Update_TunnelProviderUnchanged_LeavesSecretAlone(t *testing.T) {
	const ns, name, uid = "gameplane-games", "beta", "test-uid-2"
	gs := gameServerWithTunnel(ns, name, uid, "frp")
	secret := ownedTunnelAuthSecret(ns, name, uid, map[string]string{"token": "frp-token"})
	k := fakeKubeClient(gs)
	if _, err := k.Typed.CoreV1().Secrets(ns).Create(t.Context(), secret, metav1.CreateOptions{}); err != nil {
		t.Fatalf("seed secret: %v", err)
	}
	r := mountResourcesRouter(k)

	body := map[string]any{
		"apiVersion": "gameplane.local/v1alpha1",
		"kind":       "GameServer",
		"metadata":   map[string]any{"name": name, "namespace": ns, "uid": uid},
		"spec": map[string]any{
			"templateRef": map[string]any{"name": "minecraft"},
			"suspended":   true,
			"networking": map[string]any{
				"expose": "ClusterIP",
				"tunnel": map[string]any{"enabled": true, "provider": "frp"},
			},
		},
	}
	rr := do(t, r, "PUT", "/servers/"+name, body)
	if rr.Code != http.StatusOK {
		t.Fatalf("put: got %d %s, want 200", rr.Code, rr.Body)
	}

	got, err := k.Typed.CoreV1().Secrets(ns).Get(t.Context(), name+"-tunnel-auth", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get secret: %v", err)
	}
	if string(got.Data["token"]) != "frp-token" {
		t.Fatalf("token = %q, want frp-token (unchanged provider must not touch the secret)", got.Data["token"])
	}
}

// Removing the tunnel from the spec is not a provider switch: the stored
// credential stays, so turning the tunnel back on does not need it re-entered.
func TestResources_Update_TunnelRemoved_KeepsStoredCredential(t *testing.T) {
	const ns, name, uid = "gameplane-games", "delta", "test-uid-4"
	gs := gameServerWithTunnel(ns, name, uid, "frp")
	secret := ownedTunnelAuthSecret(ns, name, uid, map[string]string{"token": "frp-token"})
	k := fakeKubeClient(gs)
	if _, err := k.Typed.CoreV1().Secrets(ns).Create(t.Context(), secret, metav1.CreateOptions{}); err != nil {
		t.Fatalf("seed secret: %v", err)
	}
	r := mountResourcesRouter(k)

	body := map[string]any{
		"apiVersion": "gameplane.local/v1alpha1",
		"kind":       "GameServer",
		"metadata":   map[string]any{"name": name, "namespace": ns, "uid": uid},
		"spec": map[string]any{
			"templateRef": map[string]any{"name": "minecraft"},
			"networking":  map[string]any{"expose": "ClusterIP"},
		},
	}
	rr := do(t, r, "PUT", "/servers/"+name, body)
	if rr.Code != http.StatusOK {
		t.Fatalf("put: got %d %s, want 200", rr.Code, rr.Body)
	}

	got, err := k.Typed.CoreV1().Secrets(ns).Get(t.Context(), name+"-tunnel-auth", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get secret: %v", err)
	}
	if string(got.Data["token"]) != "frp-token" {
		t.Fatalf("token = %q, want frp-token (removing the tunnel must not delete the stored credential)", got.Data["token"])
	}
}

// A provider switch on a GameServer with no tunnel-auth Secret yet (or one
// already deleted) must not fail the spec update.
func TestResources_Update_TunnelProviderSwitch_MissingSecretNotAnError(t *testing.T) {
	const ns, name, uid = "gameplane-games", "gamma", "test-uid-3"
	gs := gameServerWithTunnel(ns, name, uid, "frp")
	k := fakeKubeClient(gs) // no tunnel-auth secret seeded
	r := mountResourcesRouter(k)

	body := map[string]any{
		"apiVersion": "gameplane.local/v1alpha1",
		"kind":       "GameServer",
		"metadata":   map[string]any{"name": name, "namespace": ns, "uid": uid},
		"spec": map[string]any{
			"templateRef": map[string]any{"name": "minecraft"},
			"networking": map[string]any{
				"expose": "ClusterIP",
				"tunnel": map[string]any{"enabled": true, "provider": "tailscale"},
			},
		},
	}
	rr := do(t, r, "PUT", "/servers/"+name, body)
	if rr.Code != http.StatusOK {
		t.Fatalf("put: got %d %s, want 200 even with no tunnel-auth secret", rr.Code, rr.Body)
	}
	if _, err := k.Typed.CoreV1().Secrets(ns).Get(t.Context(), name+"-tunnel-auth", metav1.GetOptions{}); err == nil {
		t.Fatal("prune path must not create a tunnel-auth secret that didn't exist")
	}
}

// spec.origin marks a Backup as generated by a BackupSchedule, and retention
// prunes by it (#960). A Backup created through the panel is always manual,
// so a client-supplied origin must never reach the cluster.
func TestResources_Create_BackupDropsOrigin(t *testing.T) {
	k := fakeKubeClient()
	r := mountResourcesRouter(k)
	body := map[string]any{
		"apiVersion": "gameplane.local/v1alpha1",
		"kind":       "Backup",
		"metadata":   map[string]any{"name": "manual-1"},
		"spec": map[string]any{
			"serverRef": map[string]any{"name": "alpha"},
			"origin":    map[string]any{"scheduleName": "alpha-auto", "scheduleUID": "u1", "serverUID": "u2"},
		},
	}
	rr := do(t, r, "POST", "/backups", body)
	if rr.Code != http.StatusCreated {
		t.Fatalf("create backup: got %d %s, want 201", rr.Code, rr.Body)
	}
	got, err := k.Dynamic.Resource(kube.GVRs["backups"]).Namespace("gameplane-games").
		Get(context.Background(), "manual-1", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get created backup: %v", err)
	}
	if _, found, _ := unstructured.NestedFieldNoCopy(got.Object, "spec", "origin"); found {
		t.Error("spec.origin reached the cluster, want it dropped")
	}
	if name, _, _ := unstructured.NestedString(got.Object, "spec", "serverRef", "name"); name != "alpha" {
		t.Errorf("spec.serverRef.name = %q, want the rest of the spec kept", name)
	}
}
