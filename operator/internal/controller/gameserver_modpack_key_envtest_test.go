//go:build envtest

package controller

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/manager"

	gameplanev1alpha1 "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
)

// Spec 035 key sharing envtest (rule 5). The managed key Secret lives in a
// control-plane namespace separate from the game namespace; it is read through the manager's API reader (uncached), and the operator does
// not watch that Secret, so tests kickGameServer after changing it.

// withGameServerReconcilerKeyShare is withGameServerReconciler with the
// control-plane namespace set, so the reconciler reads the managed key there.
func withGameServerReconcilerKeyShare(t *testing.T, ns, ctrlNS string) setupReconciler {
	t.Helper()
	seedAgentCA(t, ns, "agent-ca")
	return func(mgr manager.Manager) error {
		return (&GameServerReconciler{
			Client:                 mgr.GetClient(),
			APIReader:              mgr.GetAPIReader(),
			Scheme:                 mgr.GetScheme(),
			AgentImage:             "ghcr.io/gameplanepanel/gameplane/agent:test",
			AgentCASecretName:      "agent-ca",
			AgentCASecretNamespace: ns,
			ControlPlaneNamespace:  ctrlNS,
		}).SetupWithManager(mgr)
	}
}

// kickGameServer forces a reconcile without waiting for the 60s key recheck
// (the operator no longer watches the managed Secret): it bumps an annotation,
// and the For(&GameServer{}) watch enqueues the server.
func kickGameServer(t *testing.T, ns, name string) {
	t.Helper()
	if err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		var gs gameplanev1alpha1.GameServer
		if err := k8sClient.Get(context.Background(), types.NamespacedName{Namespace: ns, Name: name}, &gs); err != nil {
			return err
		}
		if gs.Annotations == nil {
			gs.Annotations = map[string]string{}
		}
		gs.Annotations["test.gameplane.local/kick"] = fmt.Sprint(time.Now().UnixNano())
		return k8sClient.Update(context.Background(), &gs)
	}); err != nil {
		t.Fatalf("kick GameServer: %v", err)
	}
}

// buildManagedCFKey is the API-owned managed key Secret, shared when share is true.
func buildManagedCFKey(ns, key string, share bool) *corev1.Secret {
	ann := map[string]string{}
	if share {
		ann[curseforgeShareAnnotation] = "true"
	}
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:        curseforgeManagedName,
			Namespace:   ns,
			Labels:      map[string]string{modRegistryLabel: "true"},
			Annotations: ann,
		},
		Type: corev1.SecretTypeOpaque,
		Data: map[string][]byte{"apiKey": []byte(key)},
	}
}

// mutateManagedCFKey applies mut to the managed key Secret with a fresh read
// and retries on conflict.
func mutateManagedCFKey(t *testing.T, ns string, mut func(sec *corev1.Secret)) {
	t.Helper()
	if err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		var sec corev1.Secret
		if err := k8sClient.Get(context.Background(), types.NamespacedName{Namespace: ns, Name: curseforgeManagedName}, &sec); err != nil {
			return err
		}
		mut(&sec)
		return k8sClient.Update(context.Background(), &sec)
	}); err != nil {
		t.Fatalf("update managed key Secret: %v", err)
	}
}

// keyCopyOf reads the per-server key copy with the uncached client. found is
// false when the copy does not exist.
func keyCopyOf(t *testing.T, ns, gsName string) (*corev1.Secret, bool) {
	t.Helper()
	var sec corev1.Secret
	err := k8sClient.Get(context.Background(), types.NamespacedName{Namespace: ns, Name: gsName + curseforgeKeyCopySuffix}, &sec)
	if apierrors.IsNotFound(err) {
		return nil, false
	}
	if err != nil {
		t.Fatalf("get key copy: %v", err)
	}
	return &sec, true
}

// keyRefOf returns the secretKeyRef of the named env var, or nil when the
// var is absent or not sourced from a Secret.
func keyRefOf(env []corev1.EnvVar, name string) *corev1.SecretKeySelector {
	for _, e := range env {
		if e.Name == name && e.ValueFrom != nil {
			return e.ValueFrom.SecretKeyRef
		}
	}
	return nil
}

// statefulSetOf returns the server's StatefulSet, or nil when absent.
func statefulSetOf(t *testing.T, ns, name string) *appsv1.StatefulSet {
	t.Helper()
	var ss appsv1.StatefulSet
	if err := k8sClient.Get(context.Background(), types.NamespacedName{Namespace: ns, Name: name}, &ss); err != nil {
		return nil
	}
	return &ss
}

// keyShareStatus returns status.modpack.keyShared for the server.
func keyShareStatus(t *testing.T, ns, name string) bool {
	t.Helper()
	gs := getGameServer(t, ns, name)
	return gs.Status.Modpack != nil && gs.Status.Modpack.KeyShared
}

// TestGameServer_ModpackKeyShare_OnOff covers spec 035 rule 5 on a CurseForge
// pack: the copy Secret exists and is owned by the server, the game container
// reads CF_API_KEY only through a secretKeyRef (no literal value anywhere in
// the pod template), rotating the managed key updates only the copy without
// touching the pod template or its config hash, and turning sharing off and
// on removes and restores the copy and the env.
func TestGameServer_ModpackKeyShare_OnOff(t *testing.T) {
	ns := newNamespace(t)
	ctrlNS := newNamespace(t)
	startMgr(t, ns, withGameServerReconcilerKeyShare(t, ns, ctrlNS))

	if err := k8sClient.Create(context.Background(), buildManagedCFKey(ctrlNS, "key-one", true)); err != nil {
		t.Fatalf("create managed key Secret: %v", err)
	}
	tmpl := buildModpackTemplate(uniqueName("minecraft"))
	if err := k8sClient.Create(context.Background(), tmpl); err != nil {
		t.Fatalf("create template: %v", err)
	}
	deleteCleanup(t, tmpl)

	gs := buildGameServer(ns, "keyed", tmpl.Name)
	gs.Spec.Version = "1.21.4-forge"
	gs.Spec.Modpack = modpackSpecOf("curseforge", "all-the-mods-10", "6512345")
	if err := k8sClient.Create(context.Background(), gs); err != nil {
		t.Fatalf("create gameserver: %v", err)
	}

	eventually(t, func() (bool, string) {
		sec, found := keyCopyOf(t, ns, "keyed")
		if !found {
			return false, "key copy Secret missing"
		}
		if got := string(sec.Data["apiKey"]); got != "key-one" {
			return false, fmt.Sprintf("copy apiKey=%q, want key-one", got)
		}
		if !metav1.IsControlledBy(sec, getGameServer(t, ns, "keyed")) {
			return false, "key copy is not controlled by the GameServer"
		}
		env, why := gameEnvOf(ns, "keyed")
		if why != "" {
			return false, why
		}
		ref := keyRefOf(env, "CF_API_KEY")
		if ref == nil || ref.Name != "keyed-curseforge-key" || ref.Key != "apiKey" {
			return false, fmt.Sprintf("CF_API_KEY secretKeyRef = %+v", ref)
		}
		if envCount(env, "CF_API_KEY") != 1 {
			return false, "CF_API_KEY appears more than once"
		}
		if v, _ := envValue(env, "CF_API_KEY"); v != "" {
			return false, "CF_API_KEY has a literal value"
		}
		if !keyShareStatus(t, ns, "keyed") {
			return false, "status.modpack.keyShared is false"
		}
		return true, ""
	})

	ss := statefulSetOf(t, ns, "keyed")
	if ss == nil {
		t.Fatalf("statefulset missing after key share")
	}
	podJSON, err := json.Marshal(ss.Spec.Template.Spec)
	if err != nil {
		t.Fatalf("marshal pod spec: %v", err)
	}
	if strings.Contains(string(podJSON), "key-one") {
		t.Fatalf("shared key value leaked into the pod template")
	}
	agent := containerByName(ss.Spec.Template.Spec.Containers, "agent")
	if agent == nil {
		t.Fatalf("agent container missing")
	}
	if envCount(agent.Env, "CF_API_KEY") != 0 {
		t.Fatalf("agent container received CF_API_KEY")
	}

	// Rotation: only the copy changes; the pod template and config hash stay.
	hashBefore, hadHash := ss.Spec.Template.Annotations[configHashAnnotation]
	envBefore, _ := json.Marshal(ss.Spec.Template.Spec.Containers)

	mutateManagedCFKey(t, ctrlNS, func(sec *corev1.Secret) { sec.Data["apiKey"] = []byte("key-two") })
	kickGameServer(t, ns, "keyed")
	eventually(t, func() (bool, string) {
		sec, found := keyCopyOf(t, ns, "keyed")
		if !found {
			return false, "key copy Secret missing after rotation"
		}
		if got := string(sec.Data["apiKey"]); got != "key-two" {
			return false, fmt.Sprintf("copy apiKey=%q, want key-two", got)
		}
		return true, ""
	})
	consistently(t, time.Second, func() (bool, string) {
		cur := statefulSetOf(t, ns, "keyed")
		if cur == nil {
			return false, "statefulset missing"
		}
		got, had := cur.Spec.Template.Annotations[configHashAnnotation]
		if got != hashBefore || had != hadHash {
			return false, fmt.Sprintf("config hash changed on rotation: %q -> %q", hashBefore, got)
		}
		envNow, _ := json.Marshal(cur.Spec.Template.Spec.Containers)
		if string(envNow) != string(envBefore) {
			return false, "pod template changed on key rotation"
		}
		return true, ""
	})

	// Sharing off: copy and env go away once the server is quiet (OD-10 A).
	modpackReadyIs(t, ns, "keyed", "True")
	setAgentPlayers(t, ns, "keyed", int32Ptr(0))
	mutateManagedCFKey(t, ctrlNS, func(sec *corev1.Secret) { delete(sec.Annotations, curseforgeShareAnnotation) })
	kickGameServer(t, ns, "keyed")
	eventually(t, func() (bool, string) {
		if _, found := keyCopyOf(t, ns, "keyed"); found {
			return false, "key copy Secret still present with sharing off"
		}
		cur := statefulSetOf(t, ns, "keyed")
		if cur == nil {
			return false, "statefulset missing"
		}
		game := containerByName(cur.Spec.Template.Spec.Containers, gameContainerName)
		if game == nil {
			return false, "no game container"
		}
		if envCount(game.Env, "CF_API_KEY") != 0 {
			return false, "CF_API_KEY still rendered with sharing off"
		}
		if keyShareStatus(t, ns, "keyed") {
			return false, "status.modpack.keyShared still true"
		}
		return true, ""
	})

	// Sharing on again: a kicked reconcile re-reads the managed Secret and restores copy and env.
	mutateManagedCFKey(t, ctrlNS, func(sec *corev1.Secret) {
		if sec.Annotations == nil {
			sec.Annotations = map[string]string{}
		}
		sec.Annotations[curseforgeShareAnnotation] = "true"
	})
	kickGameServer(t, ns, "keyed")
	eventually(t, func() (bool, string) {
		sec, found := keyCopyOf(t, ns, "keyed")
		if !found {
			return false, "key copy Secret not restored"
		}
		if got := string(sec.Data["apiKey"]); got != "key-two" {
			return false, fmt.Sprintf("restored copy apiKey=%q, want key-two", got)
		}
		env, why := gameEnvOf(ns, "keyed")
		if why != "" {
			return false, why
		}
		if ref := keyRefOf(env, "CF_API_KEY"); ref == nil || ref.Name != "keyed-curseforge-key" {
			return false, "CF_API_KEY secretKeyRef not restored"
		}
		if !keyShareStatus(t, ns, "keyed") {
			return false, "status.modpack.keyShared not restored"
		}
		return true, ""
	})
}

// TestGameServer_ModpackKeyShare_KeyDeletedAndPackRemoved covers the managed
// Secret disappearing and the pack leaving CurseForge: the copy and env follow
// the managed key in both directions, and a switch to another provider removes
// the copy and env entirely.
func TestGameServer_ModpackKeyShare_KeyDeletedAndPackRemoved(t *testing.T) {
	ns := newNamespace(t)
	ctrlNS := newNamespace(t)
	startMgr(t, ns, withGameServerReconcilerKeyShare(t, ns, ctrlNS))

	managed := buildManagedCFKey(ctrlNS, "key-one", true)
	if err := k8sClient.Create(context.Background(), managed); err != nil {
		t.Fatalf("create managed key Secret: %v", err)
	}
	tmpl := buildModpackTemplate(uniqueName("minecraft"))
	if err := k8sClient.Create(context.Background(), tmpl); err != nil {
		t.Fatalf("create template: %v", err)
	}
	deleteCleanup(t, tmpl)

	gs := buildGameServer(ns, "dropkey", tmpl.Name)
	gs.Spec.Version = "1.21.4-forge"
	gs.Spec.Modpack = modpackSpecOf("curseforge", "all-the-mods-10", "6512345")
	if err := k8sClient.Create(context.Background(), gs); err != nil {
		t.Fatalf("create gameserver: %v", err)
	}

	copyPresent := func(want bool) func() (bool, string) {
		return func() (bool, string) {
			_, found := keyCopyOf(t, ns, "dropkey")
			if found != want {
				return false, fmt.Sprintf("key copy present=%v, want %v", found, want)
			}
			env, why := gameEnvOf(ns, "dropkey")
			if why != "" {
				return false, why
			}
			if want && keyRefOf(env, "CF_API_KEY") == nil {
				return false, "CF_API_KEY secretKeyRef missing"
			}
			if !want && envCount(env, "CF_API_KEY") != 0 {
				return false, "CF_API_KEY still rendered"
			}
			if keyShareStatus(t, ns, "dropkey") != want {
				return false, fmt.Sprintf("status.modpack.keyShared, want %v", want)
			}
			return true, ""
		}
	}
	eventually(t, copyPresent(true))

	// The managed Secret is deleted: copy and env go away.
	setAgentPlayers(t, ns, "dropkey", int32Ptr(0))
	if err := k8sClient.Delete(context.Background(), managed); err != nil {
		t.Fatalf("delete managed key Secret: %v", err)
	}
	kickGameServer(t, ns, "dropkey")
	eventually(t, copyPresent(false))

	// Recreated: copy and env come back.
	if err := k8sClient.Create(context.Background(), buildManagedCFKey(ctrlNS, "key-one", true)); err != nil {
		t.Fatalf("recreate managed key Secret: %v", err)
	}
	kickGameServer(t, ns, "dropkey")
	eventually(t, copyPresent(true))

	// Pack moves off CurseForge: copy and env are removed.
	modpackReadyIs(t, ns, "dropkey", "True")
	setModpack(t, ns, "dropkey", modpackSpecOf("modrinth", "some-pack", "1.0.0"))
	eventually(t, func() (bool, string) {
		if _, found := keyCopyOf(t, ns, "dropkey"); found {
			return false, "key copy Secret still present after leaving CurseForge"
		}
		env, why := gameEnvOf(ns, "dropkey")
		if why != "" {
			return false, why
		}
		if envCount(env, "CF_API_KEY") != 0 {
			return false, "CF_API_KEY still rendered after leaving CurseForge"
		}
		if keyShareStatus(t, ns, "dropkey") {
			return false, "status.modpack.keyShared still true after leaving CurseForge"
		}
		return true, ""
	})
}

// TestGameServer_ModpackKeyShare_NoPackNoCopy proves a sharing-enabled managed
// key creates no copy for a server that has no modpack.
func TestGameServer_ModpackKeyShare_NoPackNoCopy(t *testing.T) {
	ns := newNamespace(t)
	ctrlNS := newNamespace(t)
	startMgr(t, ns, withGameServerReconcilerKeyShare(t, ns, ctrlNS))

	if err := k8sClient.Create(context.Background(), buildManagedCFKey(ctrlNS, "key-one", true)); err != nil {
		t.Fatalf("create managed key Secret: %v", err)
	}
	tmpl := buildModpackTemplate(uniqueName("minecraft"))
	if err := k8sClient.Create(context.Background(), tmpl); err != nil {
		t.Fatalf("create template: %v", err)
	}
	deleteCleanup(t, tmpl)

	gs := buildGameServer(ns, "nopack", tmpl.Name)
	gs.Spec.Version = "1.21.4-forge"
	if err := k8sClient.Create(context.Background(), gs); err != nil {
		t.Fatalf("create gameserver: %v", err)
	}

	// Wait for the server to reconcile into a StatefulSet, then check no copy exists.
	eventually(t, func() (bool, string) {
		if _, why := gameEnvOf(ns, "nopack"); why != "" {
			return false, why
		}
		return true, ""
	})
	if _, found := keyCopyOf(t, ns, "nopack"); found {
		t.Fatalf("key copy Secret created for a server with no modpack")
	}
}

// keyShareStateIs reports whether the copy Secret, the secretKeyRef env and
// status.modpack.keyShared all show sharing as want.
func keyShareStateIs(t *testing.T, ns, name string, want bool) func() (bool, string) {
	return func() (bool, string) {
		if _, found := keyCopyOf(t, ns, name); found != want {
			return false, fmt.Sprintf("key copy present=%v, want %v", found, want)
		}
		env, why := gameEnvOf(ns, name)
		if why != "" {
			return false, why
		}
		if (keyRefOf(env, "CF_API_KEY") != nil) != want {
			return false, fmt.Sprintf("CF_API_KEY secretKeyRef present, want %v", want)
		}
		if keyShareStatus(t, ns, name) != want {
			return false, fmt.Sprintf("status.modpack.keyShared, want %v", want)
		}
		return true, ""
	}
}

// TestGameServer_ModpackKeyShare_ToggleWaitsForZeroPlayers covers spec 035
// OD-10 A: turning sharing off on an applied CurseForge pack rolls the pod, so
// it applies only at a known 0 players. While players are online or unknown the
// copy, env and applied state stay put and the pod template does not move; at
// 0 the change applies with exactly one rollout.
func TestGameServer_ModpackKeyShare_ToggleWaitsForZeroPlayers(t *testing.T) {
	ns := newNamespace(t)
	ctrlNS := newNamespace(t)
	startMgr(t, ns, withGameServerReconcilerKeyShare(t, ns, ctrlNS))

	if err := k8sClient.Create(context.Background(), buildManagedCFKey(ctrlNS, "key-one", true)); err != nil {
		t.Fatalf("create managed key Secret: %v", err)
	}
	tmpl := buildModpackTemplate(uniqueName("minecraft"))
	if err := k8sClient.Create(context.Background(), tmpl); err != nil {
		t.Fatalf("create template: %v", err)
	}
	deleteCleanup(t, tmpl)

	gs := buildGameServer(ns, "waitkey", tmpl.Name)
	gs.Spec.Version = "1.21.4-forge"
	gs.Spec.Modpack = modpackSpecOf("curseforge", "all-the-mods-10", "6512345")
	if err := k8sClient.Create(context.Background(), gs); err != nil {
		t.Fatalf("create gameserver: %v", err)
	}
	eventually(t, keyShareStateIs(t, ns, "waitkey", true))
	modpackReadyIs(t, ns, "waitkey", "True")
	setAgentPlayers(t, ns, "waitkey", int32Ptr(3))
	before := ssOf(t, ns, "waitkey").Generation
	podBefore := podTemplateJSON(t, ns, "waitkey")

	held := func() (bool, string) {
		if ok, why := keyShareStateIs(t, ns, "waitkey", true)(); !ok {
			return false, why
		}
		if got := podTemplateJSON(t, ns, "waitkey"); got != podBefore {
			return false, "pod template changed while the toggle waits"
		}
		return true, ""
	}

	// Sharing off with 3 players online: held.
	mutateManagedCFKey(t, ctrlNS, func(sec *corev1.Secret) { delete(sec.Annotations, curseforgeShareAnnotation) })
	kickGameServer(t, ns, "waitkey")
	consistently(t, 2*time.Second, held)

	// Unknown player count is never quiet: still held.
	setAgentPlayers(t, ns, "waitkey", nil)
	kickGameServer(t, ns, "waitkey")
	consistently(t, 2*time.Second, held)

	// Known 0 players: the toggle applies once.
	setAgentPlayers(t, ns, "waitkey", int32Ptr(0))
	kickGameServer(t, ns, "waitkey")
	eventually(t, keyShareStateIs(t, ns, "waitkey", false))
	if after := ssOf(t, ns, "waitkey").Generation; after != before+1 {
		t.Fatalf("statefulset generation %d -> %d, want exactly one rollout", before, after)
	}
	consistently(t, time.Second, func() (bool, string) {
		if g := ssOf(t, ns, "waitkey").Generation; g != before+1 {
			return false, fmt.Sprintf("statefulset generation moved to %d after the toggle", g)
		}
		return true, ""
	})
}

// TestGameServer_ModpackKeyShare_ShareOnWaitsForZeroPlayers covers OD-10 A for
// turning sharing on: the copy Secret is created at once (nothing names it
// yet), but the env and the applied state wait for a known 0 players.
func TestGameServer_ModpackKeyShare_ShareOnWaitsForZeroPlayers(t *testing.T) {
	ns := newNamespace(t)
	ctrlNS := newNamespace(t)
	startMgr(t, ns, withGameServerReconcilerKeyShare(t, ns, ctrlNS))

	if err := k8sClient.Create(context.Background(), buildManagedCFKey(ctrlNS, "key-one", false)); err != nil {
		t.Fatalf("create managed key Secret: %v", err)
	}
	tmpl := buildModpackTemplate(uniqueName("minecraft"))
	if err := k8sClient.Create(context.Background(), tmpl); err != nil {
		t.Fatalf("create template: %v", err)
	}
	deleteCleanup(t, tmpl)

	gs := buildGameServer(ns, "onwait", tmpl.Name)
	gs.Spec.Version = "1.21.4-forge"
	gs.Spec.Modpack = modpackSpecOf("curseforge", "all-the-mods-10", "6512345")
	if err := k8sClient.Create(context.Background(), gs); err != nil {
		t.Fatalf("create gameserver: %v", err)
	}
	eventually(t, keyShareStateIs(t, ns, "onwait", false))
	modpackReadyIs(t, ns, "onwait", "True")
	setAgentPlayers(t, ns, "onwait", int32Ptr(4))
	before := ssOf(t, ns, "onwait").Generation
	podBefore := podTemplateJSON(t, ns, "onwait")

	mutateManagedCFKey(t, ctrlNS, func(sec *corev1.Secret) { sec.Annotations[curseforgeShareAnnotation] = "true" })
	kickGameServer(t, ns, "onwait")
	eventually(t, func() (bool, string) {
		if _, found := keyCopyOf(t, ns, "onwait"); !found {
			return false, "share-on copy not created early"
		}
		return true, ""
	})
	consistently(t, 2*time.Second, func() (bool, string) {
		if _, found := keyCopyOf(t, ns, "onwait"); !found {
			return false, "share-on copy removed while players are online"
		}
		if keyShareStatus(t, ns, "onwait") {
			return false, "status.modpack.keyShared true while players are online"
		}
		if keyRefOf(mustGameEnv(t, ns, "onwait"), "CF_API_KEY") != nil {
			return false, "CF_API_KEY env rendered while players are online"
		}
		if got := podTemplateJSON(t, ns, "onwait"); got != podBefore {
			return false, "pod template changed while the share-on waits"
		}
		return true, ""
	})

	setAgentPlayers(t, ns, "onwait", int32Ptr(0))
	kickGameServer(t, ns, "onwait")
	eventually(t, keyShareStateIs(t, ns, "onwait", true))
	if after := ssOf(t, ns, "onwait").Generation; after != before+1 {
		t.Fatalf("statefulset generation %d -> %d, want exactly one rollout", before, after)
	}
}

// mustGameEnv returns the game container env, failing the test when unreadable.
func mustGameEnv(t *testing.T, ns, name string) []corev1.EnvVar {
	t.Helper()
	env, why := gameEnvOf(ns, name)
	if why != "" {
		t.Fatalf("game env: %s", why)
	}
	return env
}

// TestGameServer_ModpackKeyShare_ToggleStoppedAppliesImmediately covers OD-10 A
// with OD-8 B's stopped rule: a stopped (suspended) server applies a sharing
// toggle at once, whatever the player count.
func TestGameServer_ModpackKeyShare_ToggleStoppedAppliesImmediately(t *testing.T) {
	ns := newNamespace(t)
	ctrlNS := newNamespace(t)
	startMgr(t, ns, withGameServerReconcilerKeyShare(t, ns, ctrlNS))

	if err := k8sClient.Create(context.Background(), buildManagedCFKey(ctrlNS, "key-one", true)); err != nil {
		t.Fatalf("create managed key Secret: %v", err)
	}
	tmpl := buildModpackTemplate(uniqueName("minecraft"))
	if err := k8sClient.Create(context.Background(), tmpl); err != nil {
		t.Fatalf("create template: %v", err)
	}
	deleteCleanup(t, tmpl)

	gs := buildGameServer(ns, "stopkey", tmpl.Name)
	gs.Spec.Version = "1.21.4-forge"
	gs.Spec.Modpack = modpackSpecOf("curseforge", "all-the-mods-10", "6512345")
	if err := k8sClient.Create(context.Background(), gs); err != nil {
		t.Fatalf("create gameserver: %v", err)
	}
	eventually(t, keyShareStateIs(t, ns, "stopkey", true))
	modpackReadyIs(t, ns, "stopkey", "True")
	setAgentPlayers(t, ns, "stopkey", int32Ptr(5))

	mutateGameServer(t, ns, "stopkey", func(g *gameplanev1alpha1.GameServer) { g.Spec.Suspend = true })
	eventually(t, func() (bool, string) {
		ss := ssOf(t, ns, "stopkey")
		if ss.Spec.Replicas == nil || *ss.Spec.Replicas != 0 {
			return false, "statefulset not scaled to zero"
		}
		return true, ""
	})

	mutateManagedCFKey(t, ctrlNS, func(sec *corev1.Secret) { delete(sec.Annotations, curseforgeShareAnnotation) })
	kickGameServer(t, ns, "stopkey")
	eventually(t, keyShareStateIs(t, ns, "stopkey", false))
}

// forceModpackPhase sets status.modpack.phase directly, for phases the
// envtest cannot reach through the Backup controller in this suite.
func forceModpackPhase(t *testing.T, ns, name string, phase gameplanev1alpha1.ModpackPhase) {
	t.Helper()
	if err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		gs := getGameServer(t, ns, name)
		if gs.Status.Modpack == nil {
			return fmt.Errorf("status.modpack is nil on %s", name)
		}
		gs.Status.Modpack.Phase = phase
		return k8sClient.Status().Update(context.Background(), gs)
	}); err != nil {
		t.Fatalf("force phase %s: %v", phase, err)
	}
}

// TestGameServer_ModpackKeyShare_BackupFailedHoldsToggle covers OD-10 A: a
// share-off toggle on a server whose pack change failed its backup (no pod
// roll is under way) stays held while players are online.
func TestGameServer_ModpackKeyShare_BackupFailedHoldsToggle(t *testing.T) {
	ns := newNamespace(t)
	ctrlNS := newNamespace(t)
	startMgr(t, ns, withGameServerReconcilerKeyShare(t, ns, ctrlNS))

	if err := k8sClient.Create(context.Background(), buildManagedCFKey(ctrlNS, "key-one", true)); err != nil {
		t.Fatalf("create managed key Secret: %v", err)
	}
	tmpl := buildModpackTemplate(uniqueName("minecraft"))
	if err := k8sClient.Create(context.Background(), tmpl); err != nil {
		t.Fatalf("create template: %v", err)
	}
	deleteCleanup(t, tmpl)

	gs := buildGameServer(ns, "failheld", tmpl.Name)
	gs.Spec.Version = "1.21.4-forge"
	gs.Spec.Modpack = modpackSpecOf("curseforge", "all-the-mods-10", "6512345")
	if err := k8sClient.Create(context.Background(), gs); err != nil {
		t.Fatalf("create gameserver: %v", err)
	}
	eventually(t, keyShareStateIs(t, ns, "failheld", true))
	modpackReadyIs(t, ns, "failheld", "True")
	setAgentPlayers(t, ns, "failheld", int32Ptr(3))
	before := ssOf(t, ns, "failheld").Generation
	podBefore := podTemplateJSON(t, ns, "failheld")

	forceModpackPhase(t, ns, "failheld", gameplanev1alpha1.ModpackPhaseBackupFailed)
	waitModpackPhase(t, ns, "failheld", gameplanev1alpha1.ModpackPhaseBackupFailed)

	mutateManagedCFKey(t, ctrlNS, func(sec *corev1.Secret) { delete(sec.Annotations, curseforgeShareAnnotation) })
	kickGameServer(t, ns, "failheld")
	consistently(t, 2*time.Second, func() (bool, string) {
		if ok, why := keyShareStateIs(t, ns, "failheld", true)(); !ok {
			return false, why
		}
		if got := podTemplateJSON(t, ns, "failheld"); got != podBefore {
			return false, "pod template changed while BackupFailed holds the toggle"
		}
		if g := ssOf(t, ns, "failheld").Generation; g != before {
			return false, fmt.Sprintf("statefulset generation %d -> %d while held", before, g)
		}
		return true, ""
	})
}

// TestGameServer_ModpackKeyShare_RotationWithPlayersOnline covers rule 5
// rotation under load: a rotated managed key reaches the copy Secret at once
// with 3 players online, and the StatefulSet generation does not move.
func TestGameServer_ModpackKeyShare_RotationWithPlayersOnline(t *testing.T) {
	ns := newNamespace(t)
	ctrlNS := newNamespace(t)
	startMgr(t, ns, withGameServerReconcilerKeyShare(t, ns, ctrlNS))

	if err := k8sClient.Create(context.Background(), buildManagedCFKey(ctrlNS, "key-one", true)); err != nil {
		t.Fatalf("create managed key Secret: %v", err)
	}
	tmpl := buildModpackTemplate(uniqueName("minecraft"))
	if err := k8sClient.Create(context.Background(), tmpl); err != nil {
		t.Fatalf("create template: %v", err)
	}
	deleteCleanup(t, tmpl)

	gs := buildGameServer(ns, "rotateload", tmpl.Name)
	gs.Spec.Version = "1.21.4-forge"
	gs.Spec.Modpack = modpackSpecOf("curseforge", "all-the-mods-10", "6512345")
	if err := k8sClient.Create(context.Background(), gs); err != nil {
		t.Fatalf("create gameserver: %v", err)
	}
	eventually(t, keyShareStateIs(t, ns, "rotateload", true))
	modpackReadyIs(t, ns, "rotateload", "True")
	setAgentPlayers(t, ns, "rotateload", int32Ptr(3))
	before := ssOf(t, ns, "rotateload").Generation
	podBefore := podTemplateJSON(t, ns, "rotateload")

	mutateManagedCFKey(t, ctrlNS, func(sec *corev1.Secret) { sec.Data["apiKey"] = []byte("key-two") })
	kickGameServer(t, ns, "rotateload")
	eventually(t, func() (bool, string) {
		sec, found := keyCopyOf(t, ns, "rotateload")
		if !found {
			return false, "key copy Secret missing after rotation"
		}
		if got := string(sec.Data["apiKey"]); got != "key-two" {
			return false, fmt.Sprintf("copy apiKey=%q, want key-two", got)
		}
		return true, ""
	})
	consistently(t, 2*time.Second, func() (bool, string) {
		if g := ssOf(t, ns, "rotateload").Generation; g != before {
			return false, fmt.Sprintf("statefulset generation %d -> %d on rotation with players online", before, g)
		}
		if got := podTemplateJSON(t, ns, "rotateload"); got != podBefore {
			return false, "pod template changed on rotation with players online"
		}
		return true, ""
	})
}

// TestGameServer_ModpackKeyShare_OffOnThenZeroPlayers covers OD-10 A for a
// toggle that is held, then reverted: share off with 3 players is held, share
// on again matches the applied state, and reaching 0 players causes no rollout
// and leaves the env in place.
func TestGameServer_ModpackKeyShare_OffOnThenZeroPlayers(t *testing.T) {
	ns := newNamespace(t)
	ctrlNS := newNamespace(t)
	startMgr(t, ns, withGameServerReconcilerKeyShare(t, ns, ctrlNS))

	if err := k8sClient.Create(context.Background(), buildManagedCFKey(ctrlNS, "key-one", true)); err != nil {
		t.Fatalf("create managed key Secret: %v", err)
	}
	tmpl := buildModpackTemplate(uniqueName("minecraft"))
	if err := k8sClient.Create(context.Background(), tmpl); err != nil {
		t.Fatalf("create template: %v", err)
	}
	deleteCleanup(t, tmpl)

	gs := buildGameServer(ns, "offon", tmpl.Name)
	gs.Spec.Version = "1.21.4-forge"
	gs.Spec.Modpack = modpackSpecOf("curseforge", "all-the-mods-10", "6512345")
	if err := k8sClient.Create(context.Background(), gs); err != nil {
		t.Fatalf("create gameserver: %v", err)
	}
	eventually(t, keyShareStateIs(t, ns, "offon", true))
	modpackReadyIs(t, ns, "offon", "True")
	setAgentPlayers(t, ns, "offon", int32Ptr(3))
	before := ssOf(t, ns, "offon").Generation

	// Share off with 3 players: held.
	mutateManagedCFKey(t, ctrlNS, func(sec *corev1.Secret) { delete(sec.Annotations, curseforgeShareAnnotation) })
	kickGameServer(t, ns, "offon")
	consistently(t, 2*time.Second, keyShareStateIs(t, ns, "offon", true))

	// Share on again: the desired state equals the applied state, so nothing flips.
	mutateManagedCFKey(t, ctrlNS, func(sec *corev1.Secret) { sec.Annotations[curseforgeShareAnnotation] = "true" })
	kickGameServer(t, ns, "offon")
	consistently(t, 2*time.Second, keyShareStateIs(t, ns, "offon", true))

	// Reaching 0 players must not roll the pod for a toggle that was reverted.
	setAgentPlayers(t, ns, "offon", int32Ptr(0))
	kickGameServer(t, ns, "offon")
	consistently(t, 2*time.Second, func() (bool, string) {
		if ok, why := keyShareStateIs(t, ns, "offon", true)(); !ok {
			return false, why
		}
		if g := ssOf(t, ns, "offon").Generation; g != before {
			return false, fmt.Sprintf("statefulset generation %d -> %d after a reverted toggle", before, g)
		}
		return true, ""
	})
}
