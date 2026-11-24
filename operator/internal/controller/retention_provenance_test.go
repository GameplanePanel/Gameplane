package controller

import (
	"context"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	gameplanev1alpha1 "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
)

const (
	provNS       = "games"
	provServer   = "arena"
	provSchedule = "arena-auto"
)

// provServerObj is the GameServer "arena" with the given UID.
func provServerObj(uid types.UID) *gameplanev1alpha1.GameServer {
	return &gameplanev1alpha1.GameServer{ObjectMeta: metav1.ObjectMeta{
		Name: provServer, Namespace: provNS, UID: uid,
	}}
}

// provScheduleObj is the inline schedule "arena-auto" with the given UID and
// repository Secret, controlled by the GameServer with serverUID. It was just
// created and its yearly cron is far from due, so a reconcile never fires it.
func provScheduleObj(uid, serverUID types.UID, repo string, keepLast int32) *gameplanev1alpha1.BackupSchedule {
	isController := true
	return &gameplanev1alpha1.BackupSchedule{
		ObjectMeta: metav1.ObjectMeta{
			Name: provSchedule, Namespace: provNS, UID: uid,
			CreationTimestamp: metav1.Now(),
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion: gameplanev1alpha1.GroupVersion.String(), Kind: "GameServer",
				Name: provServer, UID: serverUID, Controller: &isController,
			}},
		},
		Spec: gameplanev1alpha1.BackupScheduleSpec{
			ServerRef: gameplanev1alpha1.LocalObjectRef{Name: provServer},
			Schedule:  "0 0 1 1 *",
			RepoRef:   &gameplanev1alpha1.SecretKeySelector{Name: repo, Key: "repo"},
			Strategy:  "restic-snapshot",
			Retention: &gameplanev1alpha1.BackupRetention{KeepLast: keepLast},
		},
	}
}

// provBackup is a Backup labelled for "arena-auto", fired by the schedule with
// schedUID for the server with serverUID (an empty schedUID means no recorded
// origin). A non-empty snapshotID makes it restorable; completed is how long
// ago it finished. It carries the snapshot finalizer so a retention Delete is
// visible as a deletion timestamp.
func provBackup(name, schedUID, serverUID, repo, snapshotID string, phase gameplanev1alpha1.BackupPhase, completed time.Duration) *gameplanev1alpha1.Backup {
	b := &gameplanev1alpha1.Backup{
		ObjectMeta: metav1.ObjectMeta{
			Name: name, Namespace: provNS, UID: types.UID(name + "-uid"),
			Labels:     map[string]string{"gameplane.local/backup-schedule": provSchedule},
			Finalizers: []string{gameplanev1alpha1.BackupSnapshotFinalizer},
		},
		Spec: gameplanev1alpha1.BackupSpec{
			ServerRef: gameplanev1alpha1.LocalObjectRef{Name: provServer},
			RepoRef:   &gameplanev1alpha1.SecretKeySelector{Name: repo, Key: "repo"},
			Strategy:  "restic-snapshot",
		},
		Status: gameplanev1alpha1.BackupStatus{Phase: phase, SnapshotID: snapshotID},
	}
	if schedUID != "" {
		b.Spec.Origin = &gameplanev1alpha1.BackupOrigin{
			ScheduleName: provSchedule, ScheduleUID: schedUID, ServerUID: serverUID,
		}
	}
	if phase == gameplanev1alpha1.BackupPhaseSucceeded || phase == gameplanev1alpha1.BackupPhaseFailed {
		ct := metav1.NewTime(time.Now().Add(-completed).Truncate(time.Second))
		b.Status.CompletionTime = &ct
	}
	return b
}

func provReconcile(t *testing.T, objs ...client.Object) client.Client {
	t.Helper()
	s := scrapeScheme(t)
	cl := fake.NewClientBuilder().WithScheme(s).WithObjects(objs...).
		WithStatusSubresource(&gameplanev1alpha1.BackupSchedule{}, &gameplanev1alpha1.Backup{}).Build()
	r := &BackupScheduleReconciler{Client: cl, Scheme: s}
	req := ctrl.Request{NamespacedName: types.NamespacedName{Namespace: provNS, Name: provSchedule}}
	if _, err := r.Reconcile(context.Background(), req); err != nil {
		t.Fatalf("reconcile schedule: %v", err)
	}
	return cl
}

func provDeleting(t *testing.T, cl client.Client, name string) bool {
	t.Helper()
	var b gameplanev1alpha1.Backup
	if err := cl.Get(context.Background(), types.NamespacedName{Namespace: provNS, Name: name}, &b); err != nil {
		t.Fatalf("get backup %s: %v", name, err)
	}
	return !b.DeletionTimestamp.IsZero()
}

// provIntact asserts the Backup still exists as the same object (same UID)
// and carries no deletion timestamp. Only a deletion starts the snapshot
// finalizer that builds the `restic forget --prune` Job (backup_forget.go), so
// an intact Backup cannot have its snapshot forgotten.
func provIntact(t *testing.T, cl client.Client, want *gameplanev1alpha1.Backup) {
	t.Helper()
	var b gameplanev1alpha1.Backup
	if err := cl.Get(context.Background(), types.NamespacedName{Namespace: provNS, Name: want.Name}, &b); err != nil {
		t.Fatalf("get backup %s: %v", want.Name, err)
	}
	if b.UID != want.UID {
		t.Errorf("backup %s UID = %q, want the original %q", want.Name, b.UID, want.UID)
	}
	if !b.DeletionTimestamp.IsZero() {
		t.Errorf("backup %s got a deletion timestamp, which would forget its snapshot", want.Name)
	}
}

// #960: a GameServer and its inline schedule are deleted and recreated under
// the same names, with new UIDs and a different repository Secret. The first
// reconcile of the new schedule, before it has ever fired, must not delete,
// forget or otherwise touch the predecessor's Backups.
func TestTrimBackups_RecreatedScheduleKeepsPredecessorBackups(t *testing.T) {
	oldOne := provBackup("arena-auto-20261101-000000", "old-sched-uid", "old-server-uid", "old-repo", "1111aaaa", gameplanev1alpha1.BackupPhaseSucceeded, 48*time.Hour)
	oldTwo := provBackup("arena-auto-20261102-000000", "old-sched-uid", "old-server-uid", "old-repo", "2222bbbb", gameplanev1alpha1.BackupPhaseSucceeded, 24*time.Hour)
	cl := provReconcile(t,
		provServerObj("new-server-uid"),
		provScheduleObj("new-sched-uid", "new-server-uid", "new-repo", 1),
		oldOne, oldTwo,
	)
	provIntact(t, cl, oldOne)
	provIntact(t, cl, oldTwo)
}

// #960: the schedule survives but its GameServer is recreated. Backups of the
// old server carry the same schedule UID but a different server UID and must
// not be pruned for the new server.
func TestTrimBackups_RecreatedServerKeepsPredecessorBackups(t *testing.T) {
	oldOne := provBackup("old-one", "sched-uid", "old-server-uid", "repo", "1111aaaa", gameplanev1alpha1.BackupPhaseSucceeded, 48*time.Hour)
	oldTwo := provBackup("old-two", "sched-uid", "old-server-uid", "repo", "2222bbbb", gameplanev1alpha1.BackupPhaseSucceeded, 24*time.Hour)
	sched := provScheduleObj("sched-uid", "new-server-uid", "repo", 1)
	sched.OwnerReferences = nil // a standalone schedule that names the server
	cl := provReconcile(t, provServerObj("new-server-uid"), sched, oldOne, oldTwo)
	provIntact(t, cl, oldOne)
	provIntact(t, cl, oldTwo)
}

// #960: a schedule still prunes its own generations normally, and leaves
// Backups without a recorded origin (manual or pre-upgrade) alone.
func TestTrimBackups_PrunesOwnGenerationsOnly(t *testing.T) {
	legacy := provBackup("legacy", "", "", "repo", "0000ffff", gameplanev1alpha1.BackupPhaseSucceeded, 96*time.Hour)
	own1 := provBackup("own-1", "sched-uid", "server-uid", "repo", "1111aaaa", gameplanev1alpha1.BackupPhaseSucceeded, 48*time.Hour)
	own2 := provBackup("own-2", "sched-uid", "server-uid", "repo", "2222bbbb", gameplanev1alpha1.BackupPhaseSucceeded, 24*time.Hour)
	cl := provReconcile(t,
		provServerObj("server-uid"),
		provScheduleObj("sched-uid", "server-uid", "repo", 1),
		legacy, own1, own2,
	)
	if !provDeleting(t, cl, "own-1") {
		t.Error("own-1 was not trimmed, want keepLast=1 to prune the schedule's older Backup")
	}
	if provDeleting(t, cl, "own-2") {
		t.Error("own-2 is being deleted, want the newest restorable Backup kept")
	}
	if provDeleting(t, cl, "legacy") {
		t.Error("legacy Backup without provenance is being deleted, want it left for the operator")
	}
}

// #960: a stale inline schedule whose controlling GameServer is not the live
// one proves nothing about the live server's Backups, even when their origin
// names the schedule.
func TestTrimBackups_StaleInlineScheduleTrimsNothing(t *testing.T) {
	own1 := provBackup("own-1", "sched-uid", "new-server-uid", "repo", "1111aaaa", gameplanev1alpha1.BackupPhaseSucceeded, 48*time.Hour)
	own2 := provBackup("own-2", "sched-uid", "new-server-uid", "repo", "2222bbbb", gameplanev1alpha1.BackupPhaseSucceeded, 24*time.Hour)
	cl := provReconcile(t,
		provServerObj("new-server-uid"),
		provScheduleObj("sched-uid", "old-server-uid", "repo", 1),
		own1, own2,
	)
	for _, name := range []string{"own-1", "own-2"} {
		if provDeleting(t, cl, name) {
			t.Errorf("%s is being deleted by a schedule owned by a deleted server", name)
		}
	}
}

// #960: Replace cancels this schedule's own in-flight Backup but never a
// predecessor's in-flight Backup that shares the schedule label.
func TestShouldFire_ReplaceSparesPredecessorInFlight(t *testing.T) {
	theirs := provBackup("theirs", "old-sched-uid", "old-server-uid", "old-repo", "", gameplanev1alpha1.BackupPhaseRunning, 0)
	mine := provBackup("mine", "sched-uid", "server-uid", "repo", "", gameplanev1alpha1.BackupPhaseRunning, 0)
	sched := provScheduleObj("sched-uid", "server-uid", "repo", 1)
	sched.Spec.ConcurrencyPolicy = "Replace"
	s := scrapeScheme(t)
	cl := fake.NewClientBuilder().WithScheme(s).WithObjects(provServerObj("server-uid"), sched, theirs, mine).Build()
	r := &BackupScheduleReconciler{Client: cl, Scheme: s}

	now := time.Now()
	fire, err := r.shouldFire(context.Background(), sched, now, now,
		[]gameplanev1alpha1.Backup{*theirs, *mine})
	if err != nil {
		t.Fatalf("shouldFire: %v", err)
	}
	if !fire {
		t.Error("Replace did not fire")
	}
	if provDeleting(t, cl, "theirs") {
		t.Error("Replace cancelled the predecessor's in-flight Backup")
	}
	if !provDeleting(t, cl, "mine") {
		t.Error("Replace did not cancel the schedule's own in-flight Backup")
	}
}

// fire stamps the immutable origin with both UIDs.
func TestFire_RecordsOrigin(t *testing.T) {
	sched := provScheduleObj("sched-uid", "server-uid", "repo", 1)
	s := scrapeScheme(t)
	cl := fake.NewClientBuilder().WithScheme(s).WithObjects(provServerObj("server-uid"), sched).Build()
	r := &BackupScheduleReconciler{Client: cl, Scheme: s}
	at := time.Date(2026, time.November, 24, 3, 0, 0, 0, time.UTC)
	if err := r.fire(context.Background(), sched, at, "server-uid"); err != nil {
		t.Fatalf("fire: %v", err)
	}
	var b gameplanev1alpha1.Backup
	if err := cl.Get(context.Background(), types.NamespacedName{Namespace: provNS, Name: "arena-auto-20261124-030000"}, &b); err != nil {
		t.Fatalf("get fired backup: %v", err)
	}
	want := gameplanev1alpha1.BackupOrigin{ScheduleName: provSchedule, ScheduleUID: "sched-uid", ServerUID: "server-uid"}
	if b.Spec.Origin == nil || *b.Spec.Origin != want {
		t.Errorf("origin = %+v, want %+v", b.Spec.Origin, want)
	}
}

// #961: a newer Backup that is Succeeded but still waiting for its Restic
// snapshot ID (inside the scrape grace period) must not take the keepLast
// slot, nor advance lastSuccessfulTime, so the older usable snapshot stays.
func TestTrimBackups_WaitingForSnapshotIDKeepsLastRestorable(t *testing.T) {
	usable := provBackup("usable", "sched-uid", "server-uid", "repo", "1111aaaa", gameplanev1alpha1.BackupPhaseSucceeded, 24*time.Hour)
	waiting := provBackup("waiting", "sched-uid", "server-uid", "repo", "", gameplanev1alpha1.BackupPhaseSucceeded, time.Minute)
	cl := provReconcile(t,
		provServerObj("server-uid"),
		provScheduleObj("sched-uid", "server-uid", "repo", 1),
		usable, waiting,
	)
	provIntact(t, cl, usable)
	provIntact(t, cl, waiting)

	var sched gameplanev1alpha1.BackupSchedule
	if err := cl.Get(context.Background(), types.NamespacedName{Namespace: provNS, Name: provSchedule}, &sched); err != nil {
		t.Fatalf("get schedule: %v", err)
	}
	if sched.Status.LastSuccessfulTime == nil || !sched.Status.LastSuccessfulTime.Equal(usable.Status.CompletionTime) {
		t.Errorf("lastSuccessfulTime = %v, want the usable Backup's completion %v",
			sched.Status.LastSuccessfulTime, usable.Status.CompletionTime)
	}
}

// #961: once the newer Backup records its snapshot ID, normal pruning resumes.
func TestTrimBackups_RecoveredSnapshotIDAllowsPruning(t *testing.T) {
	usable := provBackup("usable", "sched-uid", "server-uid", "repo", "1111aaaa", gameplanev1alpha1.BackupPhaseSucceeded, 24*time.Hour)
	recovered := provBackup("recovered", "sched-uid", "server-uid", "repo", "2222bbbb", gameplanev1alpha1.BackupPhaseSucceeded, time.Minute)
	cl := provReconcile(t,
		provServerObj("server-uid"),
		provScheduleObj("sched-uid", "server-uid", "repo", 1),
		usable, recovered,
	)
	if !provDeleting(t, cl, "usable") {
		t.Error("the older Backup was not trimmed after the newer one recorded its snapshot ID")
	}
	if provDeleting(t, cl, "recovered") {
		t.Error("the newest restorable Backup is being deleted")
	}
}

// #961: when the summary is lost for good the newer Backup goes Failed; the
// older usable Backup must still be kept.
func TestTrimBackups_LostSnapshotIDKeepsLastRestorable(t *testing.T) {
	usable := provBackup("usable", "sched-uid", "server-uid", "repo", "1111aaaa", gameplanev1alpha1.BackupPhaseSucceeded, 24*time.Hour)
	lost := provBackup("lost", "sched-uid", "server-uid", "repo", "", gameplanev1alpha1.BackupPhaseFailed, time.Minute)
	cl := provReconcile(t,
		provServerObj("server-uid"),
		provScheduleObj("sched-uid", "server-uid", "repo", 1),
		usable, lost,
	)
	provIntact(t, cl, usable)
	provIntact(t, cl, lost)
}

// #961: a CSI Backup is restorable only once its VolumeSnapshotContent is bound.
func TestBackupRestorable(t *testing.T) {
	ct := metav1.Now()
	mk := func(strategy, id, content string, phase gameplanev1alpha1.BackupPhase) *gameplanev1alpha1.Backup {
		return &gameplanev1alpha1.Backup{
			Spec: gameplanev1alpha1.BackupSpec{Strategy: strategy},
			Status: gameplanev1alpha1.BackupStatus{
				Phase: phase, CompletionTime: &ct, SnapshotID: id, VolumeSnapshotContentName: content,
			},
		}
	}
	cases := []struct {
		name string
		b    *gameplanev1alpha1.Backup
		want bool
	}{
		{"restic with id", mk("restic-snapshot", "abcd1234", "", gameplanev1alpha1.BackupPhaseSucceeded), true},
		{"restic without id", mk("restic-snapshot", "", "", gameplanev1alpha1.BackupPhaseSucceeded), false},
		{"restic failed", mk("restic-snapshot", "abcd1234", "", gameplanev1alpha1.BackupPhaseFailed), false},
		{"csi bound", mk("volume-snapshot", "vs-1", "content-1", gameplanev1alpha1.BackupPhaseSucceeded), true},
		{"csi unbound", mk("volume-snapshot", "vs-1", "", gameplanev1alpha1.BackupPhaseSucceeded), false},
	}
	for _, tc := range cases {
		if got := backupRestorable(tc.b); got != tc.want {
			t.Errorf("%s: backupRestorable = %v, want %v", tc.name, got, tc.want)
		}
	}
	noTime := mk("restic-snapshot", "abcd1234", "", gameplanev1alpha1.BackupPhaseSucceeded)
	noTime.Status.CompletionTime = nil
	if backupRestorable(noTime) {
		t.Error("a Backup without a completion time is restorable, want not")
	}
}

// #960: a schedule recreated under a reused name must not inherit its
// predecessor's last success; only its own restorable Backups count.
func TestUpdateLastSuccessfulTime_IgnoresPredecessor(t *testing.T) {
	old := provBackup("old", "old-sched-uid", "old-server-uid", "old-repo", "1111aaaa", gameplanev1alpha1.BackupPhaseSucceeded, time.Hour)
	cl := provReconcile(t,
		provServerObj("new-server-uid"),
		provScheduleObj("new-sched-uid", "new-server-uid", "new-repo", 1),
		old,
	)
	var sched gameplanev1alpha1.BackupSchedule
	if err := cl.Get(context.Background(), types.NamespacedName{Namespace: provNS, Name: provSchedule}, &sched); err != nil {
		t.Fatalf("get schedule: %v", err)
	}
	if sched.Status.LastSuccessfulTime != nil {
		t.Errorf("lastSuccessfulTime = %v, want unset for a schedule that never succeeded", sched.Status.LastSuccessfulTime)
	}
}
