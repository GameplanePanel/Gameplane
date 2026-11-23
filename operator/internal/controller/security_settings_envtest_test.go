//go:build envtest

package controller

import (
	"context"
	"strings"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/manager"

	gameplanev1alpha1 "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
)

// withGameServerReconcilerControlPlane wires GameServerReconciler with
// ControlPlaneNamespace set to ns, so the scoped envtest cache sees the
// security settings ConfigMap there (spec 030, OD-4, OD-8).
func withGameServerReconcilerControlPlane(t *testing.T, ns string) setupReconciler {
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
			ControlPlaneNamespace:  ns,
			EventRecorder:          mgr.GetEventRecorder("gameserver-controller-security-test"),
		}).SetupWithManager(mgr)
	}
}

// secSettingsWriteConfigMap creates or replaces the settings ConfigMap in ns
// with allowRootOverrides set to value.
func secSettingsWriteConfigMap(t *testing.T, ns, value string) {
	t.Helper()
	ctx := context.Background()
	key := types.NamespacedName{Namespace: ns, Name: securitySettingsConfigMapName}
	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		var cm corev1.ConfigMap
		if err := k8sClient.Get(ctx, key, &cm); err != nil {
			if !apierrors.IsNotFound(err) {
				return err
			}
			fresh := &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{Name: key.Name, Namespace: key.Namespace},
				Data:       map[string]string{securitySettingsAllowRootKey: value},
			}
			return k8sClient.Create(ctx, fresh)
		}
		if cm.Data == nil {
			cm.Data = map[string]string{}
		}
		cm.Data[securitySettingsAllowRootKey] = value
		return k8sClient.Update(ctx, &cm)
	})
	if err != nil {
		t.Fatalf("write security settings ConfigMap %s/%s: %v", ns, key.Name, err)
	}
}

// secSettingsRootServer returns a GameServer that saves a root runAsUser.
func secSettingsRootServer(ns, name, tmplName string) *gameplanev1alpha1.GameServer {
	gs := buildGameServer(ns, name, tmplName)
	gs.Spec.Security = &gameplanev1alpha1.GameServerSecuritySpec{
		RunAsUser: &gameplanev1alpha1.SecurityIDOverride{Value: secSettingsInt64(0)},
	}
	return gs
}

// secSettingsCreateTemplate creates a plain GameTemplate and registers cleanup.
func secSettingsCreateTemplate(t *testing.T) string {
	t.Helper()
	tmpl := buildGameTemplate(uniqueName("secset"))
	if err := k8sClient.Create(context.Background(), tmpl); err != nil {
		t.Fatalf("create template: %v", err)
	}
	deleteCleanup(t, tmpl)
	return tmpl.Name
}

// secSettingsStatefulSet returns the StatefulSet of the named server, or nil.
func secSettingsStatefulSet(t *testing.T, ns, name string) *appsv1.StatefulSet {
	t.Helper()
	var ss appsv1.StatefulSet
	if err := k8sClient.Get(context.Background(), types.NamespacedName{Namespace: ns, Name: name}, &ss); err != nil {
		return nil
	}
	return &ss
}

// secSettingsReplicas reports the StatefulSet replica count, or -1 while the
// StatefulSet does not exist yet.
func secSettingsReplicas(t *testing.T, ns, name string) int32 {
	t.Helper()
	ss := secSettingsStatefulSet(t, ns, name)
	if ss == nil || ss.Spec.Replicas == nil {
		return -1
	}
	return *ss.Spec.Replicas
}

// secSettingsCondition returns the SecurityAllowed condition of the named server.
func secSettingsCondition(t *testing.T, ns, name string) *metav1.Condition {
	t.Helper()
	gs := getGameServer(t, ns, name)
	return meta.FindStatusCondition(gs.Status.Conditions, gameplanev1alpha1.GameServerConditionSecurityAllowed)
}

// TestSecuritySettings_OffHoldsStoppedServer: with the ConfigMap absent, "false"
// or any value other than "true", a new server that saves a root override is
// created at zero replicas and reports SecurityAllowed=False.
func TestSecuritySettings_OffHoldsStoppedServer(t *testing.T) {
	values := []struct {
		name  string
		value *string
	}{
		{name: "absent", value: nil},
		{name: "false", value: strPtrSecSettings("false")},
		{name: "garbage", value: strPtrSecSettings("yes")},
	}
	for _, tc := range values {
		t.Run(tc.name, func(t *testing.T) {
			ns := newNamespace(t)
			if tc.value != nil {
				secSettingsWriteConfigMap(t, ns, *tc.value)
			}
			startMgr(t, ns, withGameServerReconcilerControlPlane(t, ns))
			tmplName := secSettingsCreateTemplate(t)

			if err := k8sClient.Create(context.Background(), secSettingsRootServer(ns, "held", tmplName)); err != nil {
				t.Fatalf("create gameserver: %v", err)
			}

			eventually(t, func() (bool, string) {
				c := secSettingsCondition(t, ns, "held")
				if c == nil {
					return false, "no SecurityAllowed condition yet"
				}
				if c.Status != metav1.ConditionFalse || c.Reason != gameplanev1alpha1.GameServerReasonRootOverrideDisallowed {
					return false, "condition " + string(c.Status) + "/" + c.Reason
				}
				if !strings.Contains(c.Message, "runAsUser") {
					return false, "message does not name runAsUser: " + c.Message
				}
				return true, ""
			})
			eventually(t, func() (bool, string) {
				if r := secSettingsReplicas(t, ns, "held"); r != 0 {
					return false, "replicas not yet zero"
				}
				return true, ""
			})
			consistently(t, defaultEventuallyInterval*5, func() (bool, string) {
				if r := secSettingsReplicas(t, ns, "held"); r != 0 {
					return false, "replicas rose above zero"
				}
				return true, ""
			})
		})
	}
}

// TestSecuritySettings_FlipOnStartsHeldServer: a held stopped server starts as
// soon as the ConfigMap flips to "true", with no other change to the server.
func TestSecuritySettings_FlipOnStartsHeldServer(t *testing.T) {
	ns := newNamespace(t)
	secSettingsWriteConfigMap(t, ns, "false")
	startMgr(t, ns, withGameServerReconcilerControlPlane(t, ns))
	tmplName := secSettingsCreateTemplate(t)

	if err := k8sClient.Create(context.Background(), secSettingsRootServer(ns, "held", tmplName)); err != nil {
		t.Fatalf("create gameserver: %v", err)
	}
	eventually(t, func() (bool, string) {
		if r := secSettingsReplicas(t, ns, "held"); r != 0 {
			return false, "replicas not yet zero"
		}
		return true, ""
	})

	secSettingsWriteConfigMap(t, ns, "true")

	eventually(t, func() (bool, string) {
		if r := secSettingsReplicas(t, ns, "held"); r != 1 {
			return false, "replicas not yet one"
		}
		c := secSettingsCondition(t, ns, "held")
		if c == nil || c.Status != metav1.ConditionTrue || c.Reason != securityConditionReasonAllowed {
			return false, "condition not yet True/RootOverrideAllowed"
		}
		return true, ""
	})
}

// TestSecuritySettings_FlipOffKeepsRunningServer: flipping the setting off never
// stops a running server. It keeps its StatefulSet at one replica and gets only
// the informational condition.
func TestSecuritySettings_FlipOffKeepsRunningServer(t *testing.T) {
	ns := newNamespace(t)
	secSettingsWriteConfigMap(t, ns, "true")
	startMgr(t, ns, withGameServerReconcilerControlPlane(t, ns))
	tmplName := secSettingsCreateTemplate(t)

	if err := k8sClient.Create(context.Background(), secSettingsRootServer(ns, "live", tmplName)); err != nil {
		t.Fatalf("create gameserver: %v", err)
	}
	eventually(t, func() (bool, string) {
		if r := secSettingsReplicas(t, ns, "live"); r != 1 {
			return false, "replicas not yet one"
		}
		return true, ""
	})
	uidBefore := secSettingsStatefulSet(t, ns, "live").UID

	secSettingsWriteConfigMap(t, ns, "false")

	eventually(t, func() (bool, string) {
		c := secSettingsCondition(t, ns, "live")
		if c == nil || c.Status != metav1.ConditionFalse {
			return false, "condition not yet False"
		}
		if !strings.Contains(c.Message, "keeps running") {
			return false, "running message missing: " + c.Message
		}
		return true, ""
	})
	consistently(t, defaultEventuallyInterval*5, func() (bool, string) {
		ss := secSettingsStatefulSet(t, ns, "live")
		if ss == nil || ss.Spec.Replicas == nil || *ss.Spec.Replicas != 1 {
			return false, "running server was scaled"
		}
		if ss.UID != uidBefore {
			return false, "statefulset was recreated"
		}
		return true, ""
	})
}

// TestSecuritySettings_ClearingOverrideStartsHeldServer: removing the root
// override starts the held server even while the setting stays off, and the
// condition moves to True with reason NoRootOverride.
func TestSecuritySettings_ClearingOverrideStartsHeldServer(t *testing.T) {
	ns := newNamespace(t)
	secSettingsWriteConfigMap(t, ns, "false")
	startMgr(t, ns, withGameServerReconcilerControlPlane(t, ns))
	tmplName := secSettingsCreateTemplate(t)

	if err := k8sClient.Create(context.Background(), secSettingsRootServer(ns, "held", tmplName)); err != nil {
		t.Fatalf("create gameserver: %v", err)
	}
	eventually(t, func() (bool, string) {
		c := secSettingsCondition(t, ns, "held")
		if c == nil || c.Status != metav1.ConditionFalse {
			return false, "condition not yet False"
		}
		return true, ""
	})

	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		gs := getGameServer(t, ns, "held")
		gs.Spec.Security = nil
		return k8sClient.Update(context.Background(), gs)
	})
	if err != nil {
		t.Fatalf("clear override: %v", err)
	}

	eventually(t, func() (bool, string) {
		if r := secSettingsReplicas(t, ns, "held"); r != 1 {
			return false, "replicas not yet one"
		}
		c := secSettingsCondition(t, ns, "held")
		if c == nil || c.Status != metav1.ConditionTrue || c.Reason != securityConditionReasonNoOverride {
			return false, "condition not yet True/NoRootOverride"
		}
		return true, ""
	})
}

// strPtrSecSettings returns a pointer to s for the table cases above.
func strPtrSecSettings(s string) *string {
	return &s
}
