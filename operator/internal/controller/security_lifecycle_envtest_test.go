//go:build envtest

package controller

import (
	"context"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"

	gameplanev1alpha1 "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
)

// secLifecycleTemplate creates a GameTemplate whose module default fsGroup is
// 2000 and registers cleanup.
func secLifecycleTemplate(t *testing.T) string {
	t.Helper()
	tmpl := buildGameTemplate(uniqueName("seclc"))
	tmpl.Spec.Security = &gameplanev1alpha1.GameSecuritySpec{FSGroup: secSettingsInt64(2000)}
	if err := k8sClient.Create(context.Background(), tmpl); err != nil {
		t.Fatalf("create template: %v", err)
	}
	deleteCleanup(t, tmpl)
	return tmpl.Name
}

// secLifecycleUpdateServer applies mutate to the named GameServer, retrying on
// conflicts with the reconciler.
func secLifecycleUpdateServer(t *testing.T, ns, name string, mutate func(gs *gameplanev1alpha1.GameServer)) {
	t.Helper()
	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		gs := getGameServer(t, ns, name)
		mutate(gs)
		return k8sClient.Update(context.Background(), gs)
	})
	if err != nil {
		t.Fatalf("update gameserver %s/%s: %v", ns, name, err)
	}
}

// secLifecycleSeedObserved plays the kubelet: once the StatefulSet template
// carries the security-applied annotation, copy it to the observed annotation,
// which is what the reconciler reads as status.security.applied (research R6).
func secLifecycleSeedObserved(t *testing.T, ns, name string) {
	t.Helper()
	key := types.NamespacedName{Namespace: ns, Name: name}
	eventually(t, func() (bool, string) {
		ss := secSettingsStatefulSet(t, ns, name)
		if ss == nil {
			return false, "statefulset not yet created"
		}
		raw := ss.Spec.Template.Annotations[securityAppliedAnnotation]
		if raw == "" {
			return false, "template has no security annotation yet"
		}
		err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
			var cur appsv1.StatefulSet
			if err := k8sClient.Get(context.Background(), key, &cur); err != nil {
				return err
			}
			if cur.Annotations == nil {
				cur.Annotations = map[string]string{}
			}
			cur.Annotations[securityAppliedObservedAnnotation] = raw
			return k8sClient.Update(context.Background(), &cur)
		})
		if err != nil {
			return false, "seed observed annotation: " + err.Error()
		}
		return true, ""
	})
}

// secLifecycleStatus returns the current status.security of the named server.
func secLifecycleStatus(t *testing.T, ns, name string) *gameplanev1alpha1.GameServerSecurityStatus {
	t.Helper()
	return getGameServer(t, ns, name).Status.Security
}

// TestSecurityLifecycle_NeverStartedHasNoApplied: a server that has never
// started reports desired values and no applied record, and is not pending.
func TestSecurityLifecycle_NeverStartedHasNoApplied(t *testing.T) {
	ns := newNamespace(t)
	startMgr(t, ns, withGameServerReconciler(t, ns))
	tmplName := secLifecycleTemplate(t)

	if err := k8sClient.Create(context.Background(), buildGameServer(ns, "fresh", tmplName)); err != nil {
		t.Fatalf("create gameserver: %v", err)
	}

	eventually(t, func() (bool, string) {
		sec := secLifecycleStatus(t, ns, "fresh")
		if sec == nil || sec.Desired == nil {
			return false, "status.security.desired not yet written"
		}
		if sec.Desired.FSGroup.Value == nil || *sec.Desired.FSGroup.Value != 2000 {
			return false, "desired fsGroup is not the module default 2000"
		}
		if sec.Applied != nil {
			return false, "never-started server reports applied"
		}
		if sec.Pending {
			return false, "never-started server reports pending"
		}
		return true, ""
	})
}

// TestSecurityLifecycle_ChangedOverrideWaitsForRestart: saving a changed
// override on a running server leaves the pod template and the running pod
// alone, marks the change pending, clears it when the value is edited back to
// the applied one, and returns to the module default when the override is
// removed.
func TestSecurityLifecycle_ChangedOverrideWaitsForRestart(t *testing.T) {
	ns := newNamespace(t)
	startMgr(t, ns, withGameServerReconciler(t, ns))
	tmplName := secLifecycleTemplate(t)

	if err := k8sClient.Create(context.Background(), buildGameServer(ns, "live", tmplName)); err != nil {
		t.Fatalf("create gameserver: %v", err)
	}
	eventually(t, func() (bool, string) {
		if r := secSettingsReplicas(t, ns, "live"); r != 1 {
			return false, "replicas not yet one"
		}
		return true, ""
	})
	secLifecycleSeedObserved(t, ns, "live")

	eventually(t, func() (bool, string) {
		sec := secLifecycleStatus(t, ns, "live")
		if sec == nil || sec.Applied == nil || sec.Applied.FSGroup.Value == nil {
			return false, "applied not yet recorded"
		}
		if *sec.Applied.FSGroup.Value != 2000 {
			return false, "applied fsGroup is not 2000"
		}
		return true, ""
	})

	before := secSettingsStatefulSet(t, ns, "live")
	uid := before.UID
	priorAnnotation := before.Spec.Template.Annotations[securityAppliedAnnotation]

	// Save fsGroup 3000 while the server runs.
	secLifecycleUpdateServer(t, ns, "live", func(gs *gameplanev1alpha1.GameServer) {
		gs.Spec.Security = &gameplanev1alpha1.GameServerSecuritySpec{
			FSGroup: &gameplanev1alpha1.SecurityIDOverride{Value: secSettingsInt64(3000)},
		}
	})

	eventually(t, func() (bool, string) {
		sec := secLifecycleStatus(t, ns, "live")
		if sec == nil || sec.Desired == nil || sec.Desired.FSGroup.Value == nil || *sec.Desired.FSGroup.Value != 3000 {
			return false, "desired fsGroup not yet 3000"
		}
		if !sec.Pending {
			return false, "change not marked pending"
		}
		return true, ""
	})
	consistently(t, defaultEventuallyInterval*5, func() (bool, string) {
		ss := secSettingsStatefulSet(t, ns, "live")
		if ss == nil || ss.UID != uid {
			return false, "statefulset was recreated"
		}
		if ss.Spec.Template.Annotations[securityAppliedAnnotation] != priorAnnotation {
			return false, "running pod template was rewritten"
		}
		if sc := ss.Spec.Template.Spec.SecurityContext; sc == nil || sc.FSGroup == nil || *sc.FSGroup != 2000 {
			return false, "running pod template fsGroup changed"
		}
		return true, ""
	})

	// Edit back to the applied value: pending clears.
	secLifecycleUpdateServer(t, ns, "live", func(gs *gameplanev1alpha1.GameServer) {
		gs.Spec.Security = &gameplanev1alpha1.GameServerSecuritySpec{
			FSGroup: &gameplanev1alpha1.SecurityIDOverride{Value: secSettingsInt64(2000)},
		}
	})
	eventually(t, func() (bool, string) {
		sec := secLifecycleStatus(t, ns, "live")
		if sec == nil || sec.Pending {
			return false, "pending not yet cleared"
		}
		return true, ""
	})

	// Remove the override: the module default applies again.
	secLifecycleUpdateServer(t, ns, "live", func(gs *gameplanev1alpha1.GameServer) {
		gs.Spec.Security = nil
	})
	eventually(t, func() (bool, string) {
		sec := secLifecycleStatus(t, ns, "live")
		if sec == nil || sec.Desired == nil {
			return false, "desired not yet written"
		}
		if sec.Desired.FSGroup.Source != gameplanev1alpha1.SecuritySourceModuleDefault {
			return false, "removed override is not ModuleDefault"
		}
		if sec.Desired.FSGroup.Value == nil || *sec.Desired.FSGroup.Value != 2000 {
			return false, "removed override does not return to 2000"
		}
		return true, ""
	})
}
