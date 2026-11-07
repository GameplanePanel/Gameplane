//go:build e2e

package e2e

import (
	"context"
	"testing"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// TestBackup_FailsOnMissingPVC: a Backup that targets a nonexistent
// GameServer must transition to phase=Failed with a non-empty message.
// Operator is allowed to retry the resolve a few times, so we give a
// generous timeout. No restic server or PVC is required — the
// reconciler's serverRef resolution short-circuits before the Job spec
// is built.
func TestBackup_FailsOnMissingPVC(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	ns := "gameplane-games"
	bkName := "e2e-fail-missing-pvc"

	// We still need the restic creds Secret for the spec to be admissible
	// — only the serverRef is missing.
	envInstance.ApplyYAML(t, "backup-restic-secret.yaml")

	bk := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "gameplane.local/v1alpha1",
		"kind":       "Backup",
		"metadata":   map[string]any{"name": bkName, "namespace": ns},
		"spec": map[string]any{
			"serverRef": map[string]any{"name": "definitely-no-such-server"},
			"repoRef":   map[string]any{"name": "e2e-restic-creds", "key": "repo"},
			"strategy":  "restic-snapshot",
			"quiesce":   false,
		},
	}}
	if _, err := envInstance.Dyn.Resource(backupGVR).Namespace(ns).
		Create(ctx, bk, metav1.CreateOptions{}); err != nil && !apierrors.IsAlreadyExists(err) {
		t.Fatalf("create backup: %v", err)
	}
	t.Cleanup(func() {
		_ = envInstance.Dyn.Resource(backupGVR).Namespace(ns).
			Delete(context.Background(), bkName, metav1.DeleteOptions{})
	})

	envInstance.Eventually(t, 90*time.Second, func() (bool, string) {
		got, err := envInstance.Dyn.Resource(backupGVR).Namespace(ns).
			Get(ctx, bkName, metav1.GetOptions{})
		if err != nil {
			return false, "get backup: " + err.Error()
		}
		phase, _, _ := unstructured.NestedString(got.Object, "status", "phase")
		if phase != "Failed" {
			return false, "phase=" + phase
		}
		msg, _, _ := unstructured.NestedString(got.Object, "status", "message")
		if msg == "" {
			return false, "Failed phase but empty status.message — controller should explain why"
		}
		return true, ""
	})
}

// TestBackup_FailsOnBadCredentials: when the repo Secret hands the
// restic Job a wrong password, the Job exits non-zero and the operator
// must surface that as Backup.status.phase=Failed.
//
// We use the bad-creds fixture for repoRef but a real GameServer + PVC
// so the Job actually runs (and fails at the restic-init / restic-backup
// step rather than short-circuiting earlier).
func TestBackup_FailsOnBadCredentials(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	ns := "gameplane-games"
	tmpl := "e2e-fail-badcreds-tmpl"
	gs := "e2e-fail-badcreds-target"
	bkName := "e2e-fail-badcreds"

	ensureResticRepo(t)
	envInstance.ApplyYAML(t, "backup-restic-secret-bad.yaml")

	applyBusyboxTemplate(t, tmpl)
	applyBusyboxGameServer(t, ns, gs, tmpl)
	waitPVCBound(t, ns, gs+"-data", 90*time.Second)

	// Seed the repo with the GOOD credentials first. The repo password
	// is restic's encryption key, and the backup Job idempotently runs
	// `restic init` — against a repo that doesn't exist yet, the "bad"
	// password would simply initialize it and the backup would succeed.
	// The wrong-password failure only exists once the repo has a key.
	seedName := bkName + "-seed"
	seed := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "gameplane.local/v1alpha1",
		"kind":       "Backup",
		"metadata":   map[string]any{"name": seedName, "namespace": ns},
		"spec": map[string]any{
			"serverRef": map[string]any{"name": gs},
			"repoRef":   map[string]any{"name": "e2e-restic-creds", "key": "repo"},
			"strategy":  "restic-snapshot",
			"quiesce":   false,
		},
	}}
	if _, err := envInstance.Dyn.Resource(backupGVR).Namespace(ns).
		Create(ctx, seed, metav1.CreateOptions{}); err != nil && !apierrors.IsAlreadyExists(err) {
		t.Fatalf("create seed backup: %v", err)
	}
	t.Cleanup(func() {
		_ = envInstance.Dyn.Resource(backupGVR).Namespace(ns).
			Delete(context.Background(), seedName, metav1.DeleteOptions{})
	})
	envInstance.Eventually(t, 5*time.Minute, func() (bool, string) {
		got, err := envInstance.Dyn.Resource(backupGVR).Namespace(ns).
			Get(ctx, seedName, metav1.GetOptions{})
		if err != nil {
			return false, "get seed backup: " + err.Error()
		}
		phase, _, _ := unstructured.NestedString(got.Object, "status", "phase")
		if phase != "Succeeded" {
			message, _, _ := unstructured.NestedString(got.Object, "status", "message")
			return false, "seed phase=" + phase + ": " + message
		}
		return true, ""
	})

	bk := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "gameplane.local/v1alpha1",
		"kind":       "Backup",
		"metadata":   map[string]any{"name": bkName, "namespace": ns},
		"spec": map[string]any{
			"serverRef": map[string]any{"name": gs},
			"repoRef":   map[string]any{"name": "e2e-restic-creds-bad", "key": "repo"},
			"strategy":  "restic-snapshot",
			"quiesce":   false,
		},
	}}
	if _, err := envInstance.Dyn.Resource(backupGVR).Namespace(ns).
		Create(ctx, bk, metav1.CreateOptions{}); err != nil && !apierrors.IsAlreadyExists(err) {
		t.Fatalf("create backup: %v", err)
	}
	t.Cleanup(func() {
		_ = envInstance.Dyn.Resource(backupGVR).Namespace(ns).
			Delete(context.Background(), bkName, metav1.DeleteOptions{})
	})

	// restic image pull + init + retries can stretch — give it 5 minutes
	// before declaring the failure-path test itself failed.
	envInstance.Eventually(t, 5*time.Minute, func() (bool, string) {
		got, err := envInstance.Dyn.Resource(backupGVR).Namespace(ns).
			Get(ctx, bkName, metav1.GetOptions{})
		if err != nil {
			return false, "get backup: " + err.Error()
		}
		phase, _, _ := unstructured.NestedString(got.Object, "status", "phase")
		if phase != "Failed" {
			return false, "phase=" + phase
		}
		return true, ""
	})
}

// TestRestore_FailsOnMissingSnapshot: a Restore whose backupRef points
// at a Backup that never reached Succeeded (or never existed) must end
// in phase=Failed without touching the GameServer's spec.suspend.
//
// Sister to restore_e2e_test.go's TestRestore_RejectsMissingBackup,
// which covers the "no Backup CR at all" case. This one creates a
// Backup CR that fails (so it exists but has no snapshotID), proving
// the Restore reconciler distinguishes "missing backup" from "backup
// without a usable snapshot".
func TestRestore_FailsOnMissingSnapshot(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	ns := "gameplane-games"
	tmpl := "e2e-restore-missing-snap-tmpl"
	gs := "e2e-restore-missing-snap-gs"
	bkName := "e2e-restore-missing-snap-bk"
	rsName := "e2e-restore-missing-snap-rs"

	// A bare Backup CR with nothing satisfying its spec — repoRef points
	// at a Secret that doesn't exist. The operator marks it Failed quickly,
	// and we then point a Restore at it.
	bk := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "gameplane.local/v1alpha1",
		"kind":       "Backup",
		"metadata":   map[string]any{"name": bkName, "namespace": ns},
		"spec": map[string]any{
			"serverRef": map[string]any{"name": gs},
			"repoRef":   map[string]any{"name": "secret-that-does-not-exist", "key": "repo"},
			"strategy":  "restic-snapshot",
			"quiesce":   false,
		},
	}}
	if _, err := envInstance.Dyn.Resource(backupGVR).Namespace(ns).
		Create(ctx, bk, metav1.CreateOptions{}); err != nil && !apierrors.IsAlreadyExists(err) {
		t.Fatalf("create stub backup: %v", err)
	}
	t.Cleanup(func() {
		_ = envInstance.Dyn.Resource(backupGVR).Namespace(ns).
			Delete(context.Background(), bkName, metav1.DeleteOptions{})
	})

	// GameServer to target. Doesn't have to be Ready — the Restore
	// reconciler short-circuits on the missing-snapshot resolve before
	// it suspends anything.
	applyBusyboxTemplate(t, tmpl)
	applyBusyboxGameServer(t, ns, gs, tmpl)

	createRestore(t, ns, rsName, gs, bkName)

	envInstance.Eventually(t, 2*time.Minute, func() (bool, string) {
		got, err := envInstance.Dyn.Resource(restoreGVR).Namespace(ns).
			Get(ctx, rsName, metav1.GetOptions{})
		if err != nil {
			return false, "get restore: " + err.Error()
		}
		phase, _, _ := unstructured.NestedString(got.Object, "status", "phase")
		if phase != "Failed" {
			return false, "phase=" + phase
		}
		msg, _, _ := unstructured.NestedString(got.Object, "status", "message")
		if msg == "" {
			return false, "Failed phase but empty status.message"
		}
		return true, ""
	})

	// Suspend was never touched — the Restore failed before the
	// Suspending phase could engage.
	gsObj, err := envInstance.Dyn.Resource(gameServerGVR).Namespace(ns).
		Get(ctx, gs, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get gs after failed restore: %v", err)
	}
	suspend, _, _ := unstructured.NestedBool(gsObj.Object, "spec", "suspend")
	if suspend {
		t.Errorf("missing-snapshot Restore should not have flipped GameServer.spec.suspend, but it did")
	}
}

// TestModule_FailsOnUnreachableRegistry: a ModuleSource pointing at a
// hostname that doesn't resolve must surface the failure in
// status.lastSync (or via a non-empty status condition) rather than
// crashlooping the controller.
//
// We don't assert on the exact error string — that's controller-internal
// and may change. We assert: status doesn't fill with modules, AND
// status.lastSync is non-empty within a reasonable timeout (proving the
// reconciler runs and records the result rather than wedging).
func TestModule_FailsOnUnreachableRegistry(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	const sourceName = "e2e-fail-unreachable-source"

	src := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "gameplane.local/v1alpha1",
		"kind":       "ModuleSource",
		"metadata":   map[string]any{"name": sourceName},
		"spec": map[string]any{
			"type": "oci",
			"oci": map[string]any{
				// .invalid is reserved (RFC 6761) so it never resolves on
				// any network — the controller must surface a DNS / connect
				// error rather than wait indefinitely.
				"url":      "gameplane-nonexistent-registry.invalid:5000",
				"insecure": true,
				"modules":  []any{map[string]any{"name": "ghost-game"}},
			},
			"refreshInterval": "10m",
		},
	}}
	if _, err := envInstance.Dyn.Resource(moduleSourceGVR).
		Create(ctx, src, metav1.CreateOptions{}); err != nil && !apierrors.IsAlreadyExists(err) {
		t.Fatalf("create unreachable modulesource: %v", err)
	}
	t.Cleanup(func() {
		_ = envInstance.Dyn.Resource(moduleSourceGVR).
			Delete(context.Background(), sourceName, metav1.DeleteOptions{})
	})

	envInstance.Eventually(t, 2*time.Minute, func() (bool, string) {
		got, err := envInstance.Dyn.Resource(moduleSourceGVR).
			Get(ctx, sourceName, metav1.GetOptions{})
		if err != nil {
			return false, "get modulesource: " + err.Error()
		}
		modules, _, _ := unstructured.NestedSlice(got.Object, "status", "modules")
		if len(modules) > 0 {
			return false, "status.modules unexpectedly populated despite unreachable registry"
		}
		// Either lastSync records the attempt, or a Ready=False condition
		// lands. Both prove the reconciler ran without crashing.
		lastSync, _, _ := unstructured.NestedString(got.Object, "status", "lastSync")
		if lastSync != "" {
			return true, ""
		}
		conditions, _, _ := unstructured.NestedSlice(got.Object, "status", "conditions")
		for _, cIface := range conditions {
			c, ok := cIface.(map[string]any)
			if !ok {
				continue
			}
			ct, _ := c["type"].(string)
			cs, _ := c["status"].(string)
			if ct == "Ready" && cs == "False" {
				return true, ""
			}
		}
		return false, "no error signal yet on status (lastSync empty, no Ready=False condition)"
	})

	// Sanity: the operator pod is still up. A panic on a bad ModuleSource
	// would have crashed it; a one-shot list is enough.
	pods, err := envInstance.K8s.CoreV1().Pods("gameplane-system").List(ctx, metav1.ListOptions{
		LabelSelector: "app.kubernetes.io/name=gameplane-operator",
	})
	if err != nil {
		t.Fatalf("list operator pods: %v", err)
	}
	if len(pods.Items) == 0 {
		t.Fatal("no operator pod after unreachable-registry test — controller crash?")
	}
	for _, p := range pods.Items {
		ready := false
		for _, c := range p.Status.Conditions {
			if c.Type == "Ready" && c.Status == "True" {
				ready = true
				break
			}
		}
		if !ready {
			t.Errorf("operator pod %s not Ready after unreachable-registry test", p.Name)
		}
	}
}
