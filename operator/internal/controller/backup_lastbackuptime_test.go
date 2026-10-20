package controller

import (
	"context"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	gameplanev1alpha1 "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
)

func lastBackupReconciler(t *testing.T, objs ...client.Object) *BackupReconciler {
	t.Helper()
	s := scrapeScheme(t)
	cl := fake.NewClientBuilder().WithScheme(s).
		WithObjects(objs...).
		WithStatusSubresource(&gameplanev1alpha1.GameServer{}).
		Build()
	return &BackupReconciler{Client: cl, Scheme: s}
}

func succeededBackupFor(server string, completed time.Time) *gameplanev1alpha1.Backup {
	b := scrapeBackup()
	b.Spec.ServerRef = gameplanev1alpha1.LocalObjectRef{Name: server}
	ct := metav1.NewTime(completed)
	b.Status.CompletionTime = &ct
	return b
}

func gameServer(name string) *gameplanev1alpha1.GameServer {
	gs := &gameplanev1alpha1.GameServer{}
	gs.Name = name
	gs.Namespace = "ns"
	return gs
}

// A completed backup stamps its owning GameServer's status.lastBackupTime.
func TestRecordServerBackupTime_StampsWhenUnset(t *testing.T) {
	done := time.Now().Truncate(time.Second)
	b := succeededBackupFor("srv", done)
	r := lastBackupReconciler(t, gameServer("srv"), b)

	if err := r.recordServerBackupTime(context.Background(), b); err != nil {
		t.Fatalf("recordServerBackupTime: %v", err)
	}

	got := &gameplanev1alpha1.GameServer{}
	if err := r.Get(context.Background(), types.NamespacedName{Namespace: "ns", Name: "srv"}, got); err != nil {
		t.Fatalf("get gameserver: %v", err)
	}
	if got.Status.LastBackupTime == nil || !got.Status.LastBackupTime.Time.Equal(done) {
		t.Errorf("lastBackupTime = %v, want %v", got.Status.LastBackupTime, done)
	}
}

// An older backup completing after a newer one must not rewind the timestamp.
func TestRecordServerBackupTime_DoesNotRewind(t *testing.T) {
	newer := metav1.NewTime(time.Now().Truncate(time.Second))
	gs := gameServer("srv")
	gs.Status.LastBackupTime = &newer
	b := succeededBackupFor("srv", newer.Add(-time.Hour))
	r := lastBackupReconciler(t, gs, b)

	if err := r.recordServerBackupTime(context.Background(), b); err != nil {
		t.Fatalf("recordServerBackupTime: %v", err)
	}

	got := &gameplanev1alpha1.GameServer{}
	if err := r.Get(context.Background(), types.NamespacedName{Namespace: "ns", Name: "srv"}, got); err != nil {
		t.Fatalf("get gameserver: %v", err)
	}
	if got.Status.LastBackupTime == nil || !got.Status.LastBackupTime.Time.Equal(newer.Time) {
		t.Errorf("lastBackupTime = %v, want unchanged %v", got.Status.LastBackupTime, newer.Time)
	}
}

// A backup whose GameServer is gone (deleted, or a cross-namespace ref) is a
// best-effort no-op, not an error.
func TestRecordServerBackupTime_IgnoresMissingServer(t *testing.T) {
	b := succeededBackupFor("gone", time.Now())
	r := lastBackupReconciler(t, b)

	if err := r.recordServerBackupTime(context.Background(), b); err != nil {
		t.Errorf("missing server should be ignored, got %v", err)
	}
}

// Nothing to record when the backup carries no completion time or no server.
func TestRecordServerBackupTime_NoOpWithoutData(t *testing.T) {
	r := lastBackupReconciler(t)
	if err := r.recordServerBackupTime(context.Background(), scrapeBackup()); err != nil {
		t.Errorf("empty backup should be a no-op, got %v", err)
	}
}
