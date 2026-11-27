package controller

import (
	"context"
	"testing"
	"time"

	snapshotv1 "github.com/kubernetes-csi/external-snapshotter/client/v8/apis/volumesnapshot/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	gameplanev1alpha1 "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
)

func TestAgentDataSize(t *testing.T) {
	now := time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC)
	bytes := func(v int64) *int64 { return &v }
	heartbeat := func(d time.Duration) *metav1.Time {
		ts := metav1.NewTime(now.Add(-d))
		return &ts
	}

	tests := []struct {
		name string
		gs   *gameplanev1alpha1.GameServer
		want int64 // -1 means nil
	}{
		{name: "nil game server", gs: nil, want: -1},
		{name: "agent nil", gs: &gameplanev1alpha1.GameServer{}, want: -1},
		{
			name: "disk usage nil",
			gs: &gameplanev1alpha1.GameServer{Status: gameplanev1alpha1.GameServerStatus{
				Agent: &gameplanev1alpha1.AgentStatus{LastHeartbeat: heartbeat(time.Minute)},
			}},
			want: -1,
		},
		{
			name: "disk usage negative",
			gs: &gameplanev1alpha1.GameServer{Status: gameplanev1alpha1.GameServerStatus{
				Agent: &gameplanev1alpha1.AgentStatus{DiskUsedBytes: bytes(-1), LastHeartbeat: heartbeat(time.Minute)},
			}},
			want: -1,
		},
		{
			name: "heartbeat nil",
			gs: &gameplanev1alpha1.GameServer{Status: gameplanev1alpha1.GameServerStatus{
				Agent: &gameplanev1alpha1.AgentStatus{DiskUsedBytes: bytes(15032385536)},
			}},
			want: -1,
		},
		{
			name: "heartbeat older than max age",
			gs: &gameplanev1alpha1.GameServer{Status: gameplanev1alpha1.GameServerStatus{
				Agent: &gameplanev1alpha1.AgentStatus{DiskUsedBytes: bytes(15032385536), LastHeartbeat: heartbeat(11 * time.Minute)},
			}},
			want: -1,
		},
		{
			name: "fresh heartbeat",
			gs: &gameplanev1alpha1.GameServer{Status: gameplanev1alpha1.GameServerStatus{
				Agent: &gameplanev1alpha1.AgentStatus{DiskUsedBytes: bytes(15032385536), LastHeartbeat: heartbeat(time.Minute)},
			}},
			want: 15032385536,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := agentDataSize(tc.gs, now)
			if tc.want < 0 {
				if got != nil {
					t.Fatalf("agentDataSize() = %v, want nil", got.Value())
				}
				return
			}
			if got == nil || got.Value() != tc.want {
				t.Fatalf("agentDataSize() = %v, want %d", got, tc.want)
			}
		})
	}
}

func TestBackupCSIRecordsAgentDataSize(t *testing.T) {
	b := guardBackup("snapshot")
	b.Spec.Strategy = "volume-snapshot"
	r, _ := guardFixture(t, b)
	setAgentStatusForDataSize(t, r, time.Now(), 15032385536)
	guardPass(t, r, "snapshot")
	completeGuardSnapshot(t, r, "snapshot")
	guardPass(t, r, "snapshot")

	got := guardGetBackup(t, r, "snapshot")
	if got.Status.Phase != gameplanev1alpha1.BackupPhaseSucceeded || got.Status.DataSize == nil || got.Status.DataSize.Value() != 15032385536 {
		t.Fatalf("want Succeeded with dataSize 15032385536, got phase=%q dataSize=%v", got.Status.Phase, got.Status.DataSize)
	}
}

func TestBackupCSIStaleAgentLeavesDataSizeEmpty(t *testing.T) {
	b := guardBackup("snapshot")
	b.Spec.Strategy = "volume-snapshot"
	r, _ := guardFixture(t, b)
	setAgentStatusForDataSize(t, r, time.Now().Add(-time.Hour), 15032385536)
	guardPass(t, r, "snapshot")
	completeGuardSnapshot(t, r, "snapshot")
	guardPass(t, r, "snapshot")

	got := guardGetBackup(t, r, "snapshot")
	if got.Status.Phase != gameplanev1alpha1.BackupPhaseSucceeded || got.Status.DataSize != nil {
		t.Fatalf("want Succeeded with empty dataSize, got phase=%q dataSize=%v", got.Status.Phase, got.Status.DataSize)
	}
}

// setAgentStatusForDataSize records an agent disk sample on the "server"
// GameServer that the guard fixture creates.
func setAgentStatusForDataSize(t *testing.T, r *BackupReconciler, heartbeat time.Time, usedBytes int64) {
	t.Helper()
	var gs gameplanev1alpha1.GameServer
	if err := r.Get(context.Background(), types.NamespacedName{Namespace: "ns", Name: "server"}, &gs); err != nil {
		t.Fatal(err)
	}
	hb := metav1.NewTime(heartbeat)
	gs.Status.Agent = &gameplanev1alpha1.AgentStatus{LastHeartbeat: &hb, DiskUsedBytes: &usedBytes}
	if err := r.Status().Update(context.Background(), &gs); err != nil {
		t.Fatal(err)
	}
}

// completeGuardSnapshot marks the VolumeSnapshot named name as readyToUse, the
// same way the snapshot guard tests drive it.
func completeGuardSnapshot(t *testing.T, r *BackupReconciler, name string) {
	t.Helper()
	var vs snapshotv1.VolumeSnapshot
	if err := r.Get(context.Background(), types.NamespacedName{Namespace: "ns", Name: name}, &vs); err != nil {
		t.Fatal(err)
	}
	ready := true
	vs.Status = &snapshotv1.VolumeSnapshotStatus{ReadyToUse: &ready}
	if err := r.Status().Update(context.Background(), &vs); err != nil {
		t.Fatal(err)
	}
}
