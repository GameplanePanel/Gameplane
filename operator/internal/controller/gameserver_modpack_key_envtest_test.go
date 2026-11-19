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
			Name:        curseforgeManagedSecret,
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
		if err := k8sClient.Get(context.Background(), types.NamespacedName{Namespace: ns, Name: curseforgeManagedSecret}, &sec); err != nil {
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
	err := k8sClient.Get(context.Background(), types.NamespacedName{Namespace: ns, Name: gsName + curseforgeKeySecretSuffix}, &sec)
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

	// Sharing off: copy and env go away.
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
