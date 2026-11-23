//go:build envtest

package controller

import (
	"context"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	gameplanev1alpha1 "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
)

// secBackupStoppedSS creates a stopped StatefulSet controlled by the named
// GameServer, carrying the observed security annotation with observedGroup as
// the applied filesystem group. Creating it with the annotation avoids a race
// with the restore job's read.
func secBackupStoppedSS(t *testing.T, ns, name string, observedGroup int64) {
	t.Helper()
	gs := getGameServer(t, ns, name)
	raw, err := encodeSecurityAnnotation(gameplanev1alpha1.SecuritySettings{
		FSGroup: gameplanev1alpha1.SecurityIDStatus{Value: secSettingsInt64(observedGroup), Source: gameplanev1alpha1.SecuritySourceOverride},
	})
	if err != nil {
		t.Fatalf("encode observed security: %v", err)
	}
	zero := int32(0)
	labels := map[string]string{"app": name}
	ss := &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:        name,
			Namespace:   ns,
			Annotations: map[string]string{securityAppliedObservedAnnotation: raw},
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion: gameplanev1alpha1.GroupVersion.String(),
				Kind:       "GameServer",
				Name:       name,
				UID:        gs.UID,
				Controller: ownerBoolPtr(true),
			}},
		},
		Spec: appsv1.StatefulSetSpec{
			Replicas:    &zero,
			ServiceName: name,
			Selector:    &metav1.LabelSelector{MatchLabels: labels},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: labels},
				Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "game", Image: "busybox"}}},
			},
		},
	}
	if err := k8sClient.Create(context.Background(), ss); err != nil {
		t.Fatalf("create stopped statefulset: %v", err)
	}
	ss.Status.ObservedGeneration = ss.Generation
	if err := k8sClient.Status().Update(context.Background(), ss); err != nil {
		t.Fatalf("observe stopped statefulset: %v", err)
	}
}

// TestRestore_UsesAppliedFSGroupNotPendingOverride: a restore of a stopped
// server whose saved fsGroup override is pending runs its job with the group
// the data volume was last set up with (FR-011), not the saved value.
func TestRestore_UsesAppliedFSGroupNotPendingOverride(t *testing.T) {
	ns := newNamespace(t)
	startMgr(t, ns, withRestoreReconciler())
	tmplName := uniqueName("secrs")
	tmpl := buildGameTemplate(tmplName)
	tmpl.Spec.Security = &gameplanev1alpha1.GameSecuritySpec{FSGroup: secSettingsInt64(2000)}
	if err := k8sClient.Create(context.Background(), tmpl); err != nil {
		t.Fatalf("create template: %v", err)
	}
	deleteCleanup(t, tmpl)

	gs := buildGameServer(ns, "smp", tmplName)
	gs.Spec.Security = &gameplanev1alpha1.GameServerSecuritySpec{
		FSGroup: &gameplanev1alpha1.SecurityIDOverride{Value: secSettingsInt64(3000)},
	}
	if err := k8sClient.Create(context.Background(), gs); err != nil {
		t.Fatalf("create gameserver: %v", err)
	}
	if err := k8sClient.Create(context.Background(), buildResticRepoSecret(ns, "repo")); err != nil {
		t.Fatalf("create secret: %v", err)
	}
	if err := k8sClient.Create(context.Background(), buildBackup(ns, "smp-001", "smp", "repo")); err != nil {
		t.Fatalf("create backup: %v", err)
	}
	markBackupSucceeded(t, ns, "smp-001", "snap-1", "")

	if err := k8sClient.Create(context.Background(), buildRestore(ns, "rs-1", "smp-001", "smp")); err != nil {
		t.Fatalf("create restore: %v", err)
	}
	eventually(t, func() (bool, string) {
		return getGameServer(t, ns, "smp").Spec.Suspend, "spec.suspend not yet true"
	})
	markGameServerPhase(t, ns, "smp", gameplanev1alpha1.GameServerPhaseSuspended)

	// The volume was last set up with fsGroup 1000; the saved 3000 is pending.
	secBackupStoppedSS(t, ns, "smp", 1000)

	var ps corev1.PodSpec
	eventually(t, func() (bool, string) {
		j, ok := getJob(t, ns, "restore-rs-1")
		if !ok {
			return false, "job not yet created"
		}
		ps = j.Spec.Template.Spec
		return true, ""
	})
	if ps.SecurityContext == nil || ps.SecurityContext.FSGroup == nil {
		t.Fatal("restore pod has no fsGroup")
	}
	if got := *ps.SecurityContext.FSGroup; got != 1000 {
		t.Errorf("restore FSGroup = %d, want applied 1000 (not desired 3000)", got)
	}
}
