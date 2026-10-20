package controller

import (
	"context"
	"errors"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	gameplanev1alpha1 "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
)

// newScrapeReconcilerWithObjects is like newScrapeReconciler but seeds the
// fake client with extra objects (a GameServer, its pod, ...) alongside the
// Backup, for the finalizeDelete unquiesce-target tests below.
func newScrapeReconcilerWithObjects(
	t *testing.T, b *gameplanev1alpha1.Backup, agent AgentQuiescer, extra ...client.Object,
) *BackupReconciler {
	t.Helper()
	s := scrapeScheme(t)
	objs := append([]client.Object{b}, extra...)
	cl := fake.NewClientBuilder().WithScheme(s).
		WithObjects(objs...).
		WithStatusSubresource(&gameplanev1alpha1.Backup{}).
		Build()
	r := &BackupReconciler{Client: cl, Scheme: s}
	if agent != nil {
		r.AgentClient = agent
	}
	return r
}

// TestReconcile_RetriesUnquiesceOnTerminalBackup covers F-044: a Backup that
// has already gone terminal (phase persisted, snapshot id present) must keep
// retrying a previously-failed unquiesce instead of the top-of-Reconcile
// terminal guard abandoning it forever.
func TestReconcile_RetriesUnquiesceOnTerminalBackup(t *testing.T) {
	b := quiescedBackup()
	b.Status.Phase = gameplanev1alpha1.BackupPhaseSucceeded
	b.Status.SnapshotID = "snap-x"

	q := &scrapeQuiescer{unquiesceErr: errors.New("agent unreachable")}
	r := newScrapeReconciler(t, b, nil, time.Hour, q)
	req := ctrl.Request{NamespacedName: types.NamespacedName{Namespace: b.Namespace, Name: b.Name}}

	res, err := r.Reconcile(context.Background(), req)
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if res.RequeueAfter == 0 {
		t.Fatalf("expected a requeue to retry the failed unquiesce, got %+v", res)
	}
	if q.unquiesced != 0 {
		t.Fatalf("unquiesce recorded success while the agent call was failing")
	}

	// The agent recovers. A fresh Reconcile of the same (still terminal, per
	// its persisted status.phase) Backup must retry rather than short-circuit.
	q.unquiesceErr = nil
	if _, err := r.Reconcile(context.Background(), req); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if q.unquiesced != 1 {
		t.Fatalf("Unquiesce called %d times after recovery, want 1", q.unquiesced)
	}

	var got gameplanev1alpha1.Backup
	if err := r.Get(context.Background(), req.NamespacedName, &got); err != nil {
		t.Fatalf("get backup: %v", err)
	}
	if _, ok := got.Annotations[annoUnquiescedAt]; !ok {
		t.Error("unquiesced-at annotation missing after a successful retry")
	}
	if got.Status.Phase != gameplanev1alpha1.BackupPhaseSucceeded {
		t.Errorf("phase = %q, want Succeeded (unchanged)", got.Status.Phase)
	}
}

// TestReconcile_AddsFinalizerForQuiescedBackup covers the create-time half of
// F-048: a Backup with spec.quiesce=true must carry BackupFinalizer before it
// can be deleted, so the operator gets a chance to release the world.
func TestReconcile_AddsFinalizerForQuiescedBackup(t *testing.T) {
	b := scrapeBackup()
	b.Spec.Quiesce = true
	r := newScrapeReconciler(t, b, nil, time.Hour, nil)
	req := ctrl.Request{NamespacedName: types.NamespacedName{Namespace: b.Namespace, Name: b.Name}}

	if _, err := r.Reconcile(context.Background(), req); err != nil {
		t.Fatalf("reconcile: %v", err)
	}

	var got gameplanev1alpha1.Backup
	if err := r.Get(context.Background(), req.NamespacedName, &got); err != nil {
		t.Fatalf("get backup: %v", err)
	}
	found := false
	for _, f := range got.Finalizers {
		if f == gameplanev1alpha1.BackupFinalizer {
			found = true
		}
	}
	if !found {
		t.Errorf("finalizers = %v, want %q present", got.Finalizers, gameplanev1alpha1.BackupFinalizer)
	}
}

// TestReconcile_DeleteReleasesQuiescedWorldBeforeFinalizerClears covers the
// delete-time half of F-048: deleting a Backup that quiesced the game must
// send the unquiesce before the finalizer (and therefore the object) clears,
// and must keep the finalizer (and retry) while the agent is unreachable.
func TestReconcile_DeleteReleasesQuiescedWorldBeforeFinalizerClears(t *testing.T) {
	b := backupWithFinalizerBeingDeleted("gs1")
	gs := &gameplanev1alpha1.GameServer{
		ObjectMeta: metav1.ObjectMeta{Name: "gs1", Namespace: b.Namespace},
	}
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "gs1-0", Namespace: b.Namespace},
	}

	q := &scrapeQuiescer{unquiesceErr: errors.New("agent unreachable")}
	r := newScrapeReconcilerWithObjects(t, b, q, gs, pod)
	req := ctrl.Request{NamespacedName: types.NamespacedName{Namespace: b.Namespace, Name: b.Name}}

	res, err := r.Reconcile(context.Background(), req)
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if res.RequeueAfter == 0 {
		t.Fatalf("expected a requeue while the unquiesce is failing, got %+v", res)
	}
	var mid gameplanev1alpha1.Backup
	if err := r.Get(context.Background(), req.NamespacedName, &mid); err != nil {
		t.Fatalf("get backup: %v", err)
	}
	stillHasFinalizer := false
	for _, f := range mid.Finalizers {
		if f == gameplanev1alpha1.BackupFinalizer {
			stillHasFinalizer = true
		}
	}
	if !stillHasFinalizer {
		t.Fatal("finalizer removed before the unquiesce succeeded")
	}

	q.unquiesceErr = nil
	if _, err := r.Reconcile(context.Background(), req); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if q.unquiesced != 1 {
		t.Errorf("Unquiesce called %d times, want 1", q.unquiesced)
	}
	var got gameplanev1alpha1.Backup
	err = r.Get(context.Background(), req.NamespacedName, &got)
	if err == nil {
		for _, f := range got.Finalizers {
			if f == gameplanev1alpha1.BackupFinalizer {
				t.Error("finalizer still present after a successful unquiesce")
			}
		}
	}
	// A fake client without a delete-on-zero-finalizers reaper keeps the
	// object around once its finalizer list is empty; either the object is
	// gone (real apiserver behavior) or its finalizer list is empty is
	// acceptable here.
}

// TestReconcile_DeleteReleasesFinalizerWhenServerRefEmpty covers the
// target-gone branch for a Backup whose spec.serverRef.name was never set
// (e.g. a manually crafted or malformed Backup): finalizeDelete must treat an
// empty ServerRef.Name as "target gone" and release the finalizer straight
// away instead of retrying an unquiesce it has no target for.
func TestReconcile_DeleteReleasesFinalizerWhenServerRefEmpty(t *testing.T) {
	b := quiescedBackup()
	b.Finalizers = []string{gameplanev1alpha1.BackupFinalizer}
	now := metav1.Now()
	b.DeletionTimestamp = &now

	q := &scrapeQuiescer{unquiesceErr: errors.New("agent unreachable")}
	r := newScrapeReconciler(t, b, nil, time.Hour, q)
	req := ctrl.Request{NamespacedName: types.NamespacedName{Namespace: b.Namespace, Name: b.Name}}

	if _, err := r.Reconcile(context.Background(), req); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if backupFinalizerPresent(t, r, req.NamespacedName) {
		t.Fatal("finalizer still present although spec.serverRef.name is empty")
	}
	if q.unquiesced != 0 {
		t.Error("Unquiesce called for a Backup with no unquiesce target")
	}
}

// TestReconcile_DeleteWithoutQuiesceClearsFinalizerImmediately covers the
// no-op path: a Backup that never quiesced (or wasn't asked to) must not be
// blocked on an unquiesce it never owed.
func TestReconcile_DeleteWithoutQuiesceClearsFinalizerImmediately(t *testing.T) {
	b := scrapeBackup()
	b.Finalizers = []string{gameplanev1alpha1.BackupFinalizer}
	now := metav1.Now()
	b.DeletionTimestamp = &now

	q := &scrapeQuiescer{}
	r := newScrapeReconciler(t, b, nil, time.Hour, q)
	req := ctrl.Request{NamespacedName: types.NamespacedName{Namespace: b.Namespace, Name: b.Name}}

	if _, err := r.Reconcile(context.Background(), req); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if q.unquiesced != 0 {
		t.Error("Unquiesce called for a Backup that never quiesced")
	}
}

// backupWithFinalizerBeingDeleted returns a quiesced, finalizer-carrying
// Backup with a DeletionTimestamp set and ServerRef pointing at server,
// ready to run through finalizeDelete.
func backupWithFinalizerBeingDeleted(server string) *gameplanev1alpha1.Backup {
	b := quiescedBackup()
	b.Finalizers = []string{gameplanev1alpha1.BackupFinalizer}
	b.Spec.ServerRef.Name = server
	now := metav1.Now()
	b.DeletionTimestamp = &now
	return b
}

func backupFinalizerPresent(t *testing.T, r *BackupReconciler, name types.NamespacedName) bool {
	t.Helper()
	var got gameplanev1alpha1.Backup
	if err := r.Get(context.Background(), name, &got); err != nil {
		return false
	}
	for _, f := range got.Finalizers {
		if f == gameplanev1alpha1.BackupFinalizer {
			return true
		}
	}
	return false
}

// TestReconcile_DeleteReleasesFinalizerWhenGameServerGone covers F-048's
// GameServer-gone branch: an auto BackupSchedule's owner chain
// (GameServer -> BackupSchedule -> Backup) can garbage-collect the Backup
// alongside its GameServer, so by the time finalizeDelete runs the agent
// will never answer. It must release the finalizer instead of requeuing
// forever.
func TestReconcile_DeleteReleasesFinalizerWhenGameServerGone(t *testing.T) {
	b := backupWithFinalizerBeingDeleted("gs1")
	// No GameServer object seeded: it is already gone.
	q := &scrapeQuiescer{unquiesceErr: errors.New("agent unreachable")}
	r := newScrapeReconcilerWithObjects(t, b, q)
	req := ctrl.Request{NamespacedName: types.NamespacedName{Namespace: b.Namespace, Name: b.Name}}

	if _, err := r.Reconcile(context.Background(), req); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if backupFinalizerPresent(t, r, req.NamespacedName) {
		t.Fatal("finalizer still present although the target GameServer is gone")
	}
}

// TestReconcile_DeleteReleasesFinalizerWhenGameServerBeingDeleted covers the
// same case but for a GameServer that still exists as an object yet is
// itself mid-deletion — its pod is on the way out too, so retrying is just
// as futile.
func TestReconcile_DeleteReleasesFinalizerWhenGameServerBeingDeleted(t *testing.T) {
	b := backupWithFinalizerBeingDeleted("gs1")
	now := metav1.Now()
	gs := &gameplanev1alpha1.GameServer{
		ObjectMeta: metav1.ObjectMeta{
			Name: "gs1", Namespace: b.Namespace,
			Finalizers:        []string{"keep-around-for-test"},
			DeletionTimestamp: &now,
		},
	}
	q := &scrapeQuiescer{unquiesceErr: errors.New("agent unreachable")}
	r := newScrapeReconcilerWithObjects(t, b, q, gs)
	req := ctrl.Request{NamespacedName: types.NamespacedName{Namespace: b.Namespace, Name: b.Name}}

	if _, err := r.Reconcile(context.Background(), req); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if backupFinalizerPresent(t, r, req.NamespacedName) {
		t.Fatal("finalizer still present although the target GameServer is being deleted")
	}
}

// TestReconcile_DeleteReleasesFinalizerWhenPodGone covers the pod-gone
// branch: the GameServer is alive but its pod no longer exists (e.g.
// deleted independently, or not yet (re)scheduled) — a fresh pod starts
// with auto-save on, so there is nothing left to unquiesce.
func TestReconcile_DeleteReleasesFinalizerWhenPodGone(t *testing.T) {
	b := backupWithFinalizerBeingDeleted("gs1")
	gs := &gameplanev1alpha1.GameServer{
		ObjectMeta: metav1.ObjectMeta{Name: "gs1", Namespace: b.Namespace},
	}
	q := &scrapeQuiescer{unquiesceErr: errors.New("agent unreachable")}
	r := newScrapeReconcilerWithObjects(t, b, q, gs)
	req := ctrl.Request{NamespacedName: types.NamespacedName{Namespace: b.Namespace, Name: b.Name}}

	if _, err := r.Reconcile(context.Background(), req); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if backupFinalizerPresent(t, r, req.NamespacedName) {
		t.Fatal("finalizer still present although the target pod is gone")
	}
}

// TestReconcile_DeleteRetriesWhileTargetStillExists covers the still-alive
// case: with both the GameServer and its pod present, a failing unquiesce
// must keep retrying rather than immediately giving up.
func TestReconcile_DeleteRetriesWhileTargetStillExists(t *testing.T) {
	b := backupWithFinalizerBeingDeleted("gs1")
	gs := &gameplanev1alpha1.GameServer{
		ObjectMeta: metav1.ObjectMeta{Name: "gs1", Namespace: b.Namespace},
	}
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "gs1-0", Namespace: b.Namespace},
	}
	q := &scrapeQuiescer{unquiesceErr: errors.New("agent unreachable")}
	r := newScrapeReconcilerWithObjects(t, b, q, gs, pod)
	req := ctrl.Request{NamespacedName: types.NamespacedName{Namespace: b.Namespace, Name: b.Name}}

	res, err := r.Reconcile(context.Background(), req)
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if res.RequeueAfter == 0 {
		t.Fatalf("expected a requeue while the target still exists, got %+v", res)
	}
	if !backupFinalizerPresent(t, r, req.NamespacedName) {
		t.Fatal("finalizer removed although the unquiesce target still exists")
	}

	// Recovering the agent should let the next reconcile finish normally.
	q.unquiesceErr = nil
	if _, err := r.Reconcile(context.Background(), req); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if backupFinalizerPresent(t, r, req.NamespacedName) {
		t.Fatal("finalizer still present after a successful unquiesce")
	}
}

// TestReconcile_DeleteBoundedRetryReleasesFinalizerAfterMaxAge covers the
// bounded-retry path: even with the target still present, an unquiesce that
// keeps failing must eventually release the finalizer rather than retrying
// forever, once maxUnquiesceFinalizeRetry has elapsed since the first
// failure.
func TestReconcile_DeleteBoundedRetryReleasesFinalizerAfterMaxAge(t *testing.T) {
	b := backupWithFinalizerBeingDeleted("gs1")
	gs := &gameplanev1alpha1.GameServer{
		ObjectMeta: metav1.ObjectMeta{Name: "gs1", Namespace: b.Namespace},
	}
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "gs1-0", Namespace: b.Namespace},
	}
	q := &scrapeQuiescer{unquiesceErr: errors.New("agent unreachable")}
	r := newScrapeReconcilerWithObjects(t, b, q, gs, pod)
	req := ctrl.Request{NamespacedName: types.NamespacedName{Namespace: b.Namespace, Name: b.Name}}

	// First failure records the retry-start annotation and requeues.
	if _, err := r.Reconcile(context.Background(), req); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if !backupFinalizerPresent(t, r, req.NamespacedName) {
		t.Fatal("finalizer removed on the very first failed attempt")
	}

	// Backdate the recorded first-failure time past the retry window,
	// simulating that maxUnquiesceFinalizeRetry has elapsed.
	var got gameplanev1alpha1.Backup
	if err := r.Get(context.Background(), req.NamespacedName, &got); err != nil {
		t.Fatalf("get backup: %v", err)
	}
	got.Annotations[annoUnquiesceRetryFirstFailedAt] =
		time.Now().Add(-maxUnquiesceFinalizeRetry - time.Minute).UTC().Format(time.RFC3339)
	if err := r.Update(context.Background(), &got); err != nil {
		t.Fatalf("backdate retry annotation: %v", err)
	}

	if _, err := r.Reconcile(context.Background(), req); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if backupFinalizerPresent(t, r, req.NamespacedName) {
		t.Fatal("finalizer still present after the bounded retry window elapsed")
	}
}
