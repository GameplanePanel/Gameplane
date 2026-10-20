//go:build envtest

package controller

import (
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	"k8s.io/apimachinery/pkg/types"

	gameplanev1alpha1 "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
)

func TestGameServer_RecoversWhenMissingTemplateAppears(t *testing.T) {
	ns := newNamespace(t)
	startMgr(t, ns, withGameServerReconciler(t, ns))
	tmpl := buildGameTemplate(uniqueName("late-template"))
	gs := buildGameServer(ns, "waiting", tmpl.Name)
	if err := k8sClient.Create(t.Context(), gs); err != nil {
		t.Fatal(err)
	}
	key := types.NamespacedName{Namespace: ns, Name: gs.Name}
	eventually(t, func() (bool, string) {
		if err := k8sClient.Get(t.Context(), key, gs); err != nil {
			return false, err.Error()
		}
		return gs.Status.Phase == gameplanev1alpha1.GameServerPhaseFailed, "waiting for missing-template status"
	})
	rv := gs.ResourceVersion
	// Let its initial status event settle before creating the template, so an
	// unrelated queued GameServer event cannot accidentally recover the server.
	consistently(t, time.Second, func() (bool, string) {
		if err := k8sClient.Get(t.Context(), key, gs); err != nil {
			return false, err.Error()
		}
		return gs.ResourceVersion == rv, "unchanged failure rewrote status"
	})
	if err := k8sClient.Create(t.Context(), tmpl); err != nil {
		t.Fatal(err)
	}
	deleteCleanup(t, tmpl)
	// The template event recovers immediately, before the 15-second backstop.
	eventually(t, func() (bool, string) {
		var ss appsv1.StatefulSet
		if err := k8sClient.Get(t.Context(), key, &ss); err != nil {
			return false, err.Error()
		}
		if err := k8sClient.Get(t.Context(), key, gs); err != nil {
			return false, err.Error()
		}
		return gs.Status.Phase != gameplanev1alpha1.GameServerPhaseFailed, "failure not cleared"
	})
}
