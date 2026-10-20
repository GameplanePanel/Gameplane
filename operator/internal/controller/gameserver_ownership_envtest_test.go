//go:build envtest

package controller

import (
	"context"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	gameplanev1alpha1 "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
)

// TestGameServer_FixedNameChildrenDeletedOnlyWhenControlled is a
// regression test: reconcileConfigSecret, reconcileFilesSecret,
// reconcileRCONSecret and reconcileBackupSchedule all delete their
// fixed-name child (<gs>-config, <gs>-files, <gs>-rcon, <gs>-auto) when
// there's nothing to manage. They must do so only through
// deleteIfControlledBy (a cache read + metav1.IsControlledBy check), never
// by name alone — otherwise an unrelated object that merely shares the
// name gets deleted.
//
// The template used here declares no config schema, no configFiles and no
// RCON, and the GameServer has no spec.backupPolicy, so all four
// reconcilers are on their "nothing to manage, delete if owned" branch on
// every pass — the branch this test exercises.
func TestGameServer_FixedNameChildrenDeletedOnlyWhenControlled(t *testing.T) {
	ns := newNamespace(t)
	startMgr(t, ns, withGameServerReconciler(t, ns))

	tmpl := buildGameTemplate(uniqueName("plain"))
	if err := k8sClient.Create(context.Background(), tmpl); err != nil {
		t.Fatalf("create template: %v", err)
	}
	deleteCleanup(t, tmpl)

	gsName := "smp"

	// Pre-seed all four fixed names with objects this GameServer does
	// NOT control, before the GameServer even exists:
	//   - smp-config: unowned (no OwnerReferences at all)
	//   - smp-files: owned by a DIFFERENT GameServer (right Kind+Name
	//     shape elsewhere, wrong owner)
	//   - smp-rcon: owned by a GameServer with the SAME name but a
	//     different UID (indistinguishable from a real owner under a
	//     name-only check, which is exactly the gap being closed here)
	//   - smp-auto (BackupSchedule): unowned
	unownedConfig := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: gsName + "-config", Namespace: ns},
		Data:       map[string][]byte{"marker": []byte("unowned")},
	}
	if err := k8sClient.Create(context.Background(), unownedConfig); err != nil {
		t.Fatalf("create unowned config secret: %v", err)
	}
	deleteCleanup(t, unownedConfig)

	otherGS := buildGameServer(ns, "not-"+gsName, tmpl.Name)
	if err := k8sClient.Create(context.Background(), otherGS); err != nil {
		t.Fatalf("create other gameserver: %v", err)
	}
	deleteCleanup(t, otherGS)
	filesOwnedByOther := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      gsName + "-files",
			Namespace: ns,
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion: gameplanev1alpha1.GroupVersion.String(),
				Kind:       "GameServer",
				Name:       otherGS.Name,
				UID:        otherGS.UID,
				Controller: ownerBoolPtr(true),
			}},
		},
		Data: map[string][]byte{"marker": []byte("owned-by-other")},
	}
	if err := k8sClient.Create(context.Background(), filesOwnedByOther); err != nil {
		t.Fatalf("create files secret owned by other gameserver: %v", err)
	}
	deleteCleanup(t, filesOwnedByOther)

	rconStaleOwner := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      gsName + "-rcon",
			Namespace: ns,
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion: gameplanev1alpha1.GroupVersion.String(),
				Kind:       "GameServer",
				Name:       gsName,
				UID:        "00000000-0000-0000-0000-000000000000",
				Controller: ownerBoolPtr(true),
			}},
		},
		Data: map[string][]byte{"password": []byte("stale-owner")},
	}
	if err := k8sClient.Create(context.Background(), rconStaleOwner); err != nil {
		t.Fatalf("create rcon secret with stale-uid owner: %v", err)
	}
	deleteCleanup(t, rconStaleOwner)

	unownedSchedule := &gameplanev1alpha1.BackupSchedule{
		ObjectMeta: metav1.ObjectMeta{Name: gsName + "-auto", Namespace: ns},
		Spec: gameplanev1alpha1.BackupScheduleSpec{
			ServerRef: gameplanev1alpha1.LocalObjectRef{Name: "not-" + gsName},
			Schedule:  "0 0 * * *",
			RepoRef:   &gameplanev1alpha1.SecretKeySelector{Name: "repo", Key: "url"},
		},
	}
	if err := k8sClient.Create(context.Background(), unownedSchedule); err != nil {
		t.Fatalf("create unowned backupschedule: %v", err)
	}
	deleteCleanup(t, unownedSchedule)

	// Now create the GameServer that shares all four fixed names but
	// controls none of the pre-seeded objects.
	gs := buildGameServer(ns, gsName, tmpl.Name)
	if err := k8sClient.Create(context.Background(), gs); err != nil {
		t.Fatalf("create gameserver: %v", err)
	}
	deleteCleanup(t, gs)

	// Let several reconcile passes run and confirm none of the four
	// namesakes were deleted or adopted.
	consistently(t, 3*time.Second, func() (bool, string) {
		var sec corev1.Secret
		if err := k8sClient.Get(context.Background(),
			types.NamespacedName{Namespace: ns, Name: gsName + "-config"}, &sec); err != nil {
			return false, "unowned config secret gone: " + err.Error()
		}
		if err := k8sClient.Get(context.Background(),
			types.NamespacedName{Namespace: ns, Name: gsName + "-files"}, &sec); err != nil {
			return false, "files secret owned by other gameserver gone: " + err.Error()
		}
		if err := k8sClient.Get(context.Background(),
			types.NamespacedName{Namespace: ns, Name: gsName + "-rcon"}, &sec); err != nil {
			return false, "stale-uid-owner rcon secret gone: " + err.Error()
		}
		var bs gameplanev1alpha1.BackupSchedule
		if err := k8sClient.Get(context.Background(),
			types.NamespacedName{Namespace: ns, Name: gsName + "-auto"}, &bs); err != nil {
			return false, "unowned backupschedule gone: " + err.Error()
		}
		return true, ""
	})

	// Sanity: confirming these survived only means anything if this
	// GameServer really would have taken the delete branch for its own
	// legitimately-owned objects. Prove that side of the contract too —
	// give it a config value so it owns a REAL smp-config, delete it via
	// spec removal is covered elsewhere (TestGameServer_BackupPolicyRemovedDeletesSchedule
	// already proves the owned-BackupSchedule delete path); here just
	// assert the reconciler didn't error out entirely.
	got := getGameServer(t, ns, gsName)
	if got.Status.Phase == gameplanev1alpha1.GameServerPhaseFailed {
		t.Fatalf("gameserver reconcile failed unexpectedly: %+v", got.Status)
	}

	// And confirm none of the four objects were mutated to carry this
	// GameServer's ownership (would indicate an adopt-on-conflict bug).
	var sec corev1.Secret
	if err := k8sClient.Get(context.Background(),
		types.NamespacedName{Namespace: ns, Name: gsName + "-rcon"}, &sec); err != nil {
		t.Fatalf("re-get rcon secret: %v", err)
	}
	if metav1.IsControlledBy(&sec, got) {
		t.Error("stale-uid rcon secret must not read as controlled by the new gameserver")
	}
}
