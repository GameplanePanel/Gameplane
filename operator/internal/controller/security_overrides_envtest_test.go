//go:build envtest

package controller

import (
	"context"
	"reflect"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/manager"

	gameplanev1alpha1 "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
)

// secEnvTemplate creates a cluster-scoped GameTemplate with the given security
// block and registers its cleanup.
func secEnvTemplate(t *testing.T, sec *gameplanev1alpha1.GameSecuritySpec) *gameplanev1alpha1.GameTemplate {
	t.Helper()
	tmpl := buildGameTemplate(uniqueName("secure"))
	tmpl.Spec.Security = sec
	if err := k8sClient.Create(context.Background(), tmpl); err != nil {
		t.Fatalf("create template: %v", err)
	}
	deleteCleanup(t, tmpl)
	return tmpl
}

// secEnvRestrictedReconciler wires GameServerReconciler with RestrictedWorkloads
// on, the opt-in PSA-compatible profile the OD-5 refusal runs under.
func secEnvRestrictedReconciler(t *testing.T, ns string) setupReconciler {
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
			RestrictedWorkloads:    true,
		}).SetupWithManager(mgr)
	}
}

// secEnvGetStatefulSet returns the StatefulSet, or false when it does not exist.
func secEnvGetStatefulSet(t *testing.T, ns, name string) (*appsv1.StatefulSet, bool) {
	t.Helper()
	var ss appsv1.StatefulSet
	err := k8sClient.Get(context.Background(), types.NamespacedName{Namespace: ns, Name: name}, &ss)
	if apierrors.IsNotFound(err) {
		return nil, false
	}
	if err != nil {
		t.Fatalf("get statefulset: %v", err)
	}
	return &ss, true
}

// secEnvWaitStatefulSet waits until the reconciler has written the security
// annotation on the StatefulSet pod template, which is the first start.
func secEnvWaitStatefulSet(t *testing.T, ns, name string) *appsv1.StatefulSet {
	t.Helper()
	var out *appsv1.StatefulSet
	eventually(t, func() (bool, string) {
		ss, ok := secEnvGetStatefulSet(t, ns, name)
		if !ok {
			return false, "statefulset not created"
		}
		if ss.Spec.Template.Annotations[securityAppliedAnnotation] == "" {
			return false, "security-applied annotation not written yet"
		}
		out = ss
		return true, ""
	})
	return out
}

// secEnvNudge patches a label on the server. The GameServer watch has no
// predicates, so the patch queues a reconcile. Envtest has no StatefulSet or pod
// controller, so a seeded pod is only read on the next pass.
func secEnvNudge(t *testing.T, ns, name string) {
	t.Helper()
	gs := getGameServer(t, ns, name)
	base := gs.DeepCopy()
	if gs.Labels == nil {
		gs.Labels = map[string]string{}
	}
	gs.Labels["test/nudge"] = uniqueName("n")
	if err := k8sClient.Patch(context.Background(), gs, client.MergeFrom(base)); err != nil {
		t.Fatalf("nudge gameserver: %v", err)
	}
}

// secEnvWaitStatusNudged polls the server's security status, nudging a reconcile
// on each pass, until cond holds.
func secEnvWaitStatusNudged(t *testing.T, ns, name string, cond func(*gameplanev1alpha1.GameServerSecurityStatus) (bool, string)) {
	t.Helper()
	eventually(t, func() (bool, string) {
		secEnvNudge(t, ns, name)
		st := getGameServer(t, ns, name).Status.Security
		if st == nil {
			return false, "status.security not set"
		}
		return cond(st)
	})
}

// secEnvWaitStatus polls the server's security status without nudging.
func secEnvWaitStatus(t *testing.T, ns, name string, cond func(*gameplanev1alpha1.GameServerSecurityStatus) (bool, string)) {
	t.Helper()
	eventually(t, func() (bool, string) {
		st := getGameServer(t, ns, name).Status.Security
		if st == nil {
			return false, "status.security not set"
		}
		return cond(st)
	})
}

// secEnvSeedPod creates the server's template pod, controlled by the StatefulSet.
// With setUp true it also marks volume setup as done, so the applied reader
// reports the pod annotation.
func secEnvSeedPod(t *testing.T, ss *appsv1.StatefulSet, annotations map[string]string, setUp bool) *corev1.Pod {
	t.Helper()
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:        ss.Name + "-0",
			Namespace:   ss.Namespace,
			Annotations: annotations,
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion: "apps/v1",
				Kind:       "StatefulSet",
				Name:       ss.Name,
				UID:        ss.UID,
				Controller: ownerBoolPtr(true),
			}},
		},
		Spec: corev1.PodSpec{NodeName: "fixture-node", Containers: []corev1.Container{{Name: gameContainerName, Image: "test/game:fixture"}}},
	}
	if err := k8sClient.Create(context.Background(), pod); err != nil {
		t.Fatalf("seed pod: %v", err)
	}
	if setUp {
		pod.Status.Phase = corev1.PodRunning
		pod.Status.Conditions = []corev1.PodCondition{{Type: corev1.PodReadyToStartContainers, Status: corev1.ConditionTrue}}
		if err := k8sClient.Status().Update(context.Background(), pod); err != nil {
			t.Fatalf("mark seeded pod set up: %v", err)
		}
	}
	return pod
}

// secEnvReadyMessage returns the Ready condition message of the server, or "".
func secEnvReadyMessage(gs *gameplanev1alpha1.GameServer) string {
	for _, c := range gs.Status.Conditions {
		if c.Type == "Ready" {
			return c.Message
		}
	}
	return ""
}

// secEnvGameContainer returns the game container of a StatefulSet template.
func secEnvGameContainer(t *testing.T, ss *appsv1.StatefulSet) *corev1.Container {
	t.Helper()
	for i := range ss.Spec.Template.Spec.Containers {
		if ss.Spec.Template.Spec.Containers[i].Name == gameContainerName {
			return &ss.Spec.Template.Spec.Containers[i]
		}
	}
	t.Fatalf("statefulset %s has no game container", ss.Name)
	return nil
}

// TestSecurityOverrides_ModuleDefaultsApplyAndSettle covers the no-override path:
// the module values render into the pod, and once a pod has passed volume setup
// desired equals applied with the module sources and nothing is pending.
func TestSecurityOverrides_ModuleDefaultsApplyAndSettle(t *testing.T) {
	ns := newNamespace(t)
	startMgr(t, ns, withGameServerReconciler(t, ns))

	tmpl := secEnvTemplate(t, &gameplanev1alpha1.GameSecuritySpec{
		RunAsUser:  secInt(25000),
		RunAsGroup: secInt(25000),
		FSGroup:    secInt(25000),
	})
	if err := k8sClient.Create(context.Background(), buildGameServer(ns, "smp", tmpl.Name)); err != nil {
		t.Fatalf("create gameserver: %v", err)
	}

	ss := secEnvWaitStatefulSet(t, ns, "smp")
	if gc := secEnvGameContainer(t, ss); gc.SecurityContext == nil || gc.SecurityContext.RunAsUser == nil || *gc.SecurityContext.RunAsUser != 25000 {
		t.Errorf("game container SecurityContext = %+v, want RunAsUser 25000", gc.SecurityContext)
	}
	secEnvSeedPod(t, ss, map[string]string{securityAppliedAnnotation: ss.Spec.Template.Annotations[securityAppliedAnnotation]}, true)

	wantModule := func(v int64) gameplanev1alpha1.SecurityIDStatus {
		return gameplanev1alpha1.SecurityIDStatus{Value: secInt(v), Source: gameplanev1alpha1.SecuritySourceModuleDefault}
	}
	secEnvWaitStatusNudged(t, ns, "smp", func(st *gameplanev1alpha1.GameServerSecurityStatus) (bool, string) {
		if st.Applied == nil {
			return false, "applied not yet recorded"
		}
		if st.Pending {
			return false, "still pending"
		}
		if !reflect.DeepEqual(*st.Desired, *st.Applied) {
			return false, "desired and applied differ"
		}
		return true, ""
	})

	st := getGameServer(t, ns, "smp").Status.Security
	if !reflect.DeepEqual(st.Desired.RunAsUser, wantModule(25000)) {
		t.Errorf("desired RunAsUser = %+v, want module 25000", st.Desired.RunAsUser)
	}
	if !reflect.DeepEqual(st.Desired.RunAsGroup, wantModule(25000)) {
		t.Errorf("desired RunAsGroup = %+v, want module 25000", st.Desired.RunAsGroup)
	}
	if !reflect.DeepEqual(st.Desired.FSGroup, wantModule(25000)) {
		t.Errorf("desired FSGroup = %+v, want module 25000", st.Desired.FSGroup)
	}
	if st.Desired.VolumeOwnershipCheck.Value != gameplanev1alpha1.VolumeOwnershipCheckOnlyWhenWrong ||
		st.Desired.VolumeOwnershipCheck.Source != gameplanev1alpha1.SecuritySourceBuiltInDefault {
		t.Errorf("desired check = %+v, want OnlyWhenWrong from BuiltInDefault", st.Desired.VolumeOwnershipCheck)
	}
}

// TestSecurityOverrides_NoSecurityBlockShowsImageDefault covers a template with
// no security block: nothing is set, the sources are ImageDefault, and the check
// is the built-in default. A server that never started has no applied values.
func TestSecurityOverrides_NoSecurityBlockShowsImageDefault(t *testing.T) {
	ns := newNamespace(t)
	startMgr(t, ns, withGameServerReconciler(t, ns))

	tmpl := secEnvTemplate(t, nil)
	if err := k8sClient.Create(context.Background(), buildGameServer(ns, "smp", tmpl.Name)); err != nil {
		t.Fatalf("create gameserver: %v", err)
	}

	secEnvWaitStatus(t, ns, "smp", func(st *gameplanev1alpha1.GameServerSecurityStatus) (bool, string) {
		if st.Desired == nil {
			return false, "desired not set"
		}
		return true, ""
	})
	st := getGameServer(t, ns, "smp").Status.Security
	for name, id := range map[string]gameplanev1alpha1.SecurityIDStatus{
		"runAsUser":  st.Desired.RunAsUser,
		"runAsGroup": st.Desired.RunAsGroup,
		"fsGroup":    st.Desired.FSGroup,
	} {
		if id.Value != nil || id.Source != gameplanev1alpha1.SecuritySourceImageDefault {
			t.Errorf("desired %s = %+v, want no value from ImageDefault", name, id)
		}
	}
	if st.Desired.VolumeOwnershipCheck.Value != gameplanev1alpha1.VolumeOwnershipCheckOnlyWhenWrong {
		t.Errorf("desired check = %q, want OnlyWhenWrong", st.Desired.VolumeOwnershipCheck.Value)
	}
	if st.Applied != nil || st.Pending {
		t.Errorf("never-started server: applied = %+v, pending = %v; want nil and false", st.Applied, st.Pending)
	}
}

// TestSecurityOverrides_PreFeatureStatefulSetIsNotRecreated covers a StatefulSet
// from before the feature: it has no security annotation and no change policy.
// Reconciling it leaves the pod template untouched and reports the difference as
// pending. A scale to zero and back then writes the policy and clears pending.
func TestSecurityOverrides_PreFeatureStatefulSetIsNotRecreated(t *testing.T) {
	ns := newNamespace(t)
	startMgr(t, ns, withGameServerReconciler(t, ns))

	tmpl := secEnvTemplate(t, &gameplanev1alpha1.GameSecuritySpec{FSGroup: secInt(25000)})
	if err := k8sClient.Create(context.Background(), buildGameServer(ns, "smp", tmpl.Name)); err != nil {
		t.Fatalf("create gameserver: %v", err)
	}
	secEnvWaitStatefulSet(t, ns, "smp")

	// Make the StatefulSet look as it did before the feature: no security
	// annotation and no fsGroupChangePolicy in the pod spec.
	var stripped *appsv1.StatefulSet
	if err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		ss, ok := secEnvGetStatefulSet(t, ns, "smp")
		if !ok {
			t.Fatalf("statefulset vanished before the pre-feature rewrite")
		}
		delete(ss.Spec.Template.Annotations, securityAppliedAnnotation)
		if ss.Spec.Template.Spec.SecurityContext != nil {
			ss.Spec.Template.Spec.SecurityContext.FSGroupChangePolicy = nil
		}
		if err := k8sClient.Update(context.Background(), ss); err != nil {
			return err
		}
		stripped = ss
		return nil
	}); err != nil {
		t.Fatalf("strip security fields: %v", err)
	}
	preGen := stripped.Generation

	secEnvWaitStatusNudged(t, ns, "smp", func(st *gameplanev1alpha1.GameServerSecurityStatus) (bool, string) {
		if st.Applied == nil {
			return false, "applied not yet read"
		}
		if !st.Pending {
			return false, "pending not yet set"
		}
		return true, ""
	})

	st := getGameServer(t, ns, "smp").Status.Security
	if st.Applied.VolumeOwnershipCheck.Value != gameplanev1alpha1.VolumeOwnershipCheckEveryStart {
		t.Errorf("applied check = %q, want EveryStart (kubelet default)", st.Applied.VolumeOwnershipCheck.Value)
	}
	if st.Desired.VolumeOwnershipCheck.Value != gameplanev1alpha1.VolumeOwnershipCheckOnlyWhenWrong {
		t.Errorf("desired check = %q, want OnlyWhenWrong", st.Desired.VolumeOwnershipCheck.Value)
	}

	// The running pod template is not rewritten while the server runs.
	consistently(t, 2*time.Second, func() (bool, string) {
		ss, ok := secEnvGetStatefulSet(t, ns, "smp")
		if !ok {
			return false, "statefulset gone"
		}
		if ss.Generation != preGen {
			return false, "pod template generation changed on a running server"
		}
		if policy := ss.Spec.Template.Spec.SecurityContext; policy != nil && policy.FSGroupChangePolicy != nil {
			return false, "fsGroupChangePolicy added to a running pod template"
		}
		if ss.Spec.Template.Annotations[securityAppliedAnnotation] != "" {
			return false, "security annotation added to a running pod template"
		}
		return true, ""
	})

	// Scale to zero and back: the next start renders the policy and clears pending.
	setSuspend := func(suspend bool) {
		gs := getGameServer(t, ns, "smp")
		base := gs.DeepCopy()
		gs.Spec.Suspend = suspend
		if err := k8sClient.Patch(context.Background(), gs, client.MergeFrom(base)); err != nil {
			t.Fatalf("set suspend=%v: %v", suspend, err)
		}
	}
	setSuspend(true)
	eventually(t, func() (bool, string) {
		ss, ok := secEnvGetStatefulSet(t, ns, "smp")
		if !ok || ss.Spec.Replicas == nil {
			return false, "statefulset replicas not set"
		}
		if *ss.Spec.Replicas != 0 {
			return false, "replicas not yet zero"
		}
		return true, ""
	})
	setSuspend(false)

	var restarted *appsv1.StatefulSet
	eventually(t, func() (bool, string) {
		ss, ok := secEnvGetStatefulSet(t, ns, "smp")
		if !ok {
			return false, "statefulset gone"
		}
		sc := ss.Spec.Template.Spec.SecurityContext
		if sc == nil || sc.FSGroupChangePolicy == nil || *sc.FSGroupChangePolicy != corev1.FSGroupChangeOnRootMismatch {
			return false, "fsGroupChangePolicy not yet OnRootMismatch"
		}
		restarted = ss
		return true, ""
	})
	if restarted.Spec.Template.Annotations[securityAppliedAnnotation] == "" {
		t.Error("security-applied annotation not written at the start from zero")
	}

	// Once the new pod has passed volume setup, pending clears.
	secEnvSeedPod(t, restarted, map[string]string{securityAppliedAnnotation: restarted.Spec.Template.Annotations[securityAppliedAnnotation]}, true)
	secEnvWaitStatusNudged(t, ns, "smp", func(st *gameplanev1alpha1.GameServerSecurityStatus) (bool, string) {
		if st.Applied == nil {
			return false, "applied not yet recorded"
		}
		if st.Pending {
			return false, "still pending"
		}
		return true, ""
	})
}

// TestSecurityOverrides_RunAsUserOverrideReachesGameContainer covers a server
// override: runAsUser 2000 reaches the game container, runAsGroup useImageDefault
// leaves the container runAsGroup unset, and the other settings keep their
// module sources.
func TestSecurityOverrides_RunAsUserOverrideReachesGameContainer(t *testing.T) {
	ns := newNamespace(t)
	startMgr(t, ns, withGameServerReconciler(t, ns))

	tmpl := secEnvTemplate(t, &gameplanev1alpha1.GameSecuritySpec{
		RunAsUser:  secInt(25000),
		RunAsGroup: secInt(25000),
		FSGroup:    secInt(25000),
	})
	gs := buildGameServer(ns, "smp", tmpl.Name)
	gs.Spec.Security = &gameplanev1alpha1.GameServerSecuritySpec{
		RunAsUser:  &gameplanev1alpha1.SecurityIDOverride{Value: secInt(2000)},
		RunAsGroup: &gameplanev1alpha1.SecurityIDOverride{UseImageDefault: secBool(true)},
	}
	if err := k8sClient.Create(context.Background(), gs); err != nil {
		t.Fatalf("create gameserver: %v", err)
	}

	ss := secEnvWaitStatefulSet(t, ns, "smp")
	sc := secEnvGameContainer(t, ss).SecurityContext
	if sc == nil || sc.RunAsUser == nil || *sc.RunAsUser != 2000 {
		t.Fatalf("game container SecurityContext = %+v, want RunAsUser 2000", sc)
	}
	if sc.RunAsGroup != nil {
		t.Errorf("game container RunAsGroup = %d, want unset for useImageDefault", *sc.RunAsGroup)
	}

	secEnvWaitStatus(t, ns, "smp", func(st *gameplanev1alpha1.GameServerSecurityStatus) (bool, string) {
		if st.Desired == nil {
			return false, "desired not set"
		}
		return true, ""
	})
	st := getGameServer(t, ns, "smp").Status.Security
	if want := (gameplanev1alpha1.SecurityIDStatus{Value: secInt(2000), Source: gameplanev1alpha1.SecuritySourceOverride}); !reflect.DeepEqual(st.Desired.RunAsUser, want) {
		t.Errorf("desired RunAsUser = %+v, want %+v", st.Desired.RunAsUser, want)
	}
	if want := (gameplanev1alpha1.SecurityIDStatus{Source: gameplanev1alpha1.SecuritySourceImageDefaultOverride}); !reflect.DeepEqual(st.Desired.RunAsGroup, want) {
		t.Errorf("desired RunAsGroup = %+v, want %+v", st.Desired.RunAsGroup, want)
	}
	if want := (gameplanev1alpha1.SecurityIDStatus{Value: secInt(25000), Source: gameplanev1alpha1.SecuritySourceModuleDefault}); !reflect.DeepEqual(st.Desired.FSGroup, want) {
		t.Errorf("desired FSGroup = %+v, want module default", st.Desired.FSGroup)
	}
}

// TestSecurityOverrides_RestrictedRefusalLeavesRunningPodAlone covers OD-5 under
// restricted mode: an override the identity rule refuses sets the existing
// failure phase and message, and a pod that is already running is untouched.
func TestSecurityOverrides_RestrictedRefusalLeavesRunningPodAlone(t *testing.T) {
	ns := newNamespace(t)
	startMgr(t, ns, secEnvRestrictedReconciler(t, ns))

	tmpl := secEnvTemplate(t, &gameplanev1alpha1.GameSecuritySpec{
		RunAsUser:  secInt(25000),
		RunAsGroup: secInt(25000),
		FSGroup:    secInt(25000),
	})
	if err := k8sClient.Create(context.Background(), buildGameServer(ns, "smp", tmpl.Name)); err != nil {
		t.Fatalf("create gameserver: %v", err)
	}
	ss := secEnvWaitStatefulSet(t, ns, "smp")
	pod := secEnvSeedPod(t, ss, map[string]string{securityAppliedAnnotation: ss.Spec.Template.Annotations[securityAppliedAnnotation]}, true)
	before := ss.Spec.Template.DeepCopy()
	beforeGen := ss.Generation

	// "Use image default" for runAsUser has no value, which restricted mode refuses.
	gs := getGameServer(t, ns, "smp")
	base := gs.DeepCopy()
	gs.Spec.Security = &gameplanev1alpha1.GameServerSecuritySpec{
		RunAsUser: &gameplanev1alpha1.SecurityIDOverride{UseImageDefault: secBool(true)},
	}
	if err := k8sClient.Patch(context.Background(), gs, client.MergeFrom(base)); err != nil {
		t.Fatalf("save refused override: %v", err)
	}

	eventually(t, func() (bool, string) {
		secEnvNudge(t, ns, "smp")
		got := getGameServer(t, ns, "smp")
		if got.Status.Phase != gameplanev1alpha1.GameServerPhaseFailed {
			return false, "phase is " + string(got.Status.Phase)
		}
		if msg := secEnvReadyMessage(got); msg != restrictedIdentityMessage {
			return false, "ready message is " + msg
		}
		return true, ""
	})

	consistently(t, 2*time.Second, func() (bool, string) {
		cur, ok := secEnvGetStatefulSet(t, ns, "smp")
		if !ok {
			return false, "statefulset gone"
		}
		if cur.Generation != beforeGen || !reflect.DeepEqual(cur.Spec.Template, *before) {
			return false, "pod template changed after a refused override"
		}
		var running corev1.Pod
		if err := k8sClient.Get(context.Background(), client.ObjectKeyFromObject(pod), &running); err != nil {
			return false, "running pod: " + err.Error()
		}
		if running.UID != pod.UID {
			return false, "running pod was replaced"
		}
		return true, ""
	})
}

// TestSecurityOverrides_SavingOverrideDoesNotRewriteRunningPod covers T075: on a
// baseline-pinned running server, saving a security override does not change the
// pod template. The override shows as pending until the next start from zero.
func TestSecurityOverrides_SavingOverrideDoesNotRewriteRunningPod(t *testing.T) {
	ns := newNamespace(t)
	startMgr(t, ns, withGameServerReconciler(t, ns))

	tmpl := secEnvTemplate(t, nil)
	if err := k8sClient.Create(context.Background(), buildGameServer(ns, "smp", tmpl.Name)); err != nil {
		t.Fatalf("create gameserver: %v", err)
	}
	ss := secEnvWaitStatefulSet(t, ns, "smp")
	secEnvSeedPod(t, ss, map[string]string{securityAppliedAnnotation: ss.Spec.Template.Annotations[securityAppliedAnnotation]}, true)

	// Wait until the server holds its template baseline pin.
	eventually(t, func() (bool, string) {
		secEnvNudge(t, ns, "smp")
		if getGameServer(t, ns, "smp").Annotations[effectiveTemplateAnnotation] == "" {
			return false, "baseline pin not yet recorded"
		}
		return true, ""
	})
	ss, _ = secEnvGetStatefulSet(t, ns, "smp")
	before := ss.Spec.Template.DeepCopy()
	beforeGen := ss.Generation

	gs := getGameServer(t, ns, "smp")
	base := gs.DeepCopy()
	gs.Spec.Security = &gameplanev1alpha1.GameServerSecuritySpec{
		RunAsUser: &gameplanev1alpha1.SecurityIDOverride{Value: secInt(2000)},
		FSGroup:   &gameplanev1alpha1.SecurityIDOverride{Value: secInt(3000)},
	}
	if err := k8sClient.Patch(context.Background(), gs, client.MergeFrom(base)); err != nil {
		t.Fatalf("save override: %v", err)
	}

	secEnvWaitStatusNudged(t, ns, "smp", func(st *gameplanev1alpha1.GameServerSecurityStatus) (bool, string) {
		if !st.Pending {
			return false, "override not yet reported as pending"
		}
		return true, ""
	})

	consistently(t, 2*time.Second, func() (bool, string) {
		cur, ok := secEnvGetStatefulSet(t, ns, "smp")
		if !ok {
			return false, "statefulset gone"
		}
		if cur.Generation != beforeGen || !reflect.DeepEqual(cur.Spec.Template, *before) {
			return false, "saving an override changed the running pod template"
		}
		return true, ""
	})
}
