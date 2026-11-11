package controller

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	snapshotv1 "github.com/kubernetes-csi/external-snapshotter/client/v8/apis/volumesnapshot/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	gameplanev1alpha1 "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
	"github.com/GameplanePanel/gameplane/operator/internal/agent"
)

func backupRunningGameStatus() corev1.ContainerStatus {
	return corev1.ContainerStatus{Name: "game", Image: "test/game:fixture", ImageID: "containerd://fixture-image", ContainerID: "containerd://original-game", RestartCount: 0,
		State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{StartedAt: metav1.NewTime(time.Unix(1, 0))}}}
}

func changeBackupGamePod(t *testing.T, r *BackupReconciler, change func(*corev1.Pod)) {
	t.Helper()
	var pod corev1.Pod
	if err := r.Get(t.Context(), client.ObjectKey{Namespace: "ns", Name: "server-0"}, &pod); err != nil {
		t.Fatal(err)
	}
	change(&pod)
	if err := r.Status().Update(t.Context(), &pod); err != nil {
		t.Fatal(err)
	}
}

// Pod phase/UID remain stable through all of these kubelet observations.
func backupGameChanges() map[string]func(*corev1.Pod) {
	return map[string]func(*corev1.Pod){
		"container-id":   func(p *corev1.Pod) { p.Status.ContainerStatuses[0].ContainerID = "containerd://replacement-game" },
		"restart-count":  func(p *corev1.Pod) { p.Status.ContainerStatuses[0].RestartCount++ },
		"missing-status": func(p *corev1.Pod) { p.Status.ContainerStatuses = nil },
		"missing-id":     func(p *corev1.Pod) { p.Status.ContainerStatuses[0].ContainerID = "" },
		"waiting": func(p *corev1.Pod) {
			p.Status.ContainerStatuses[0].State = corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: "CrashLoopBackOff"}}
		},
		"waiting-unrelated-exit": func(p *corev1.Pod) {
			game := &p.Status.ContainerStatuses[0]
			game.ContainerID = ""
			game.LastTerminationState.Terminated = &corev1.ContainerStateTerminated{ContainerID: "containerd://earlier-game", ExitCode: 1}
			game.State = corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: "CrashLoopBackOff"}}
		},
		"waiting-after-exit": func(p *corev1.Pod) {
			game := &p.Status.ContainerStatuses[0]
			game.LastTerminationState.Terminated = &corev1.ContainerStateTerminated{ContainerID: game.ContainerID, ExitCode: 1}
			game.ContainerID = ""
			game.State = corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: "CrashLoopBackOff"}}
		},
		"waiting-after-restart": func(p *corev1.Pod) {
			game := &p.Status.ContainerStatuses[0]
			game.RestartCount++
			game.ContainerID = ""
			game.State = corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: "ContainerCreating"}}
		},
		"terminated": func(p *corev1.Pod) {
			p.Status.ContainerStatuses[0].State = corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{ExitCode: 1}}
		},
	}
}

func TestBackupGameContainerChangesAbortAndDrain(t *testing.T) {
	for _, stage := range []string{"before-quiesce", "during-quiesce", "during-copy", "before-success"} {
		for name, change := range backupGameChanges() {
			t.Run(stage+"/"+name, func(t *testing.T) {
				r, q := guardFixtureWorkers(t, false, guardBackup("first"))
				budgetPass(t, r, "first")
				worker := budgetReadyWorker(t, r, "first", "copy-worker")
				mutate := func() { changeBackupGamePod(t, r, change) }
				switch stage {
				case "during-quiesce":
					q.afterQuiesce = mutate
				case "before-quiesce":
					mutate()
				default:
					budgetPass(t, r, "first")
					if q.quiesced != 1 {
						t.Fatalf("copy was not quiesced before source change: %+v", q)
					}
					mutate()
					if stage == "before-success" {
						var job batchv1.Job
						if err := r.Get(t.Context(), client.ObjectKey{Namespace: "ns", Name: "first"}, &job); err != nil {
							t.Fatal(err)
						}
						job.Status.Succeeded = 1
						if err := r.Status().Update(t.Context(), &job); err != nil {
							t.Fatal(err)
						}
					}
				}
				budgetPass(t, r, "first")
				b := guardGetBackup(t, r, "first")
				if (stage == "before-quiesce" && q.quiesced != 0) || (stage != "before-quiesce" && q.quiesced != 1) {
					t.Fatalf("unexpected quiesce attempts across source change: %+v", q)
				}
				if b.Status.Phase != gameplanev1alpha1.BackupPhaseFailed || b.Status.SnapshotID != "" || !strings.Contains(b.Status.Message, "source changed") {
					t.Fatalf("same-Pod source change allowed capture: %+v", b.Status)
				}
				if condition := meta.FindStatusCondition(b.Status.Conditions, "Consistency"); condition == nil || condition.Status != metav1.ConditionFalse {
					t.Fatalf("invalid capture retained a consistency guarantee: %+v", condition)
				}
				var gate corev1.ConfigMap
				if err := r.Get(t.Context(), client.ObjectKey{Namespace: "ns", Name: backupStartConfigMapName(b)}, &gate); err != nil || gate.Data[backupStartTokenKey] != "" {
					t.Fatalf("source change retained copy authorization: %+v, %v", gate.Data, err)
				}
				var gs gameplanev1alpha1.GameServer
				if err := r.Get(t.Context(), client.ObjectKey{Namespace: "ns", Name: "server"}, &gs); err != nil {
					t.Fatal(err)
				}
				if gs.Annotations[lifecycleOperationAnnotation] == "" || q.unquiesced != 0 {
					t.Fatalf("live copy worker lost protection: owner=%q, agent=%+v", gs.Annotations[lifecycleOperationAnnotation], q)
				}
				if err := r.Delete(t.Context(), worker); err != nil {
					t.Fatal(err)
				}
				budgetPass(t, r, "first")
				if err := r.Get(t.Context(), client.ObjectKeyFromObject(&gs), &gs); err != nil {
					t.Fatal(err)
				}
				unidentified := stage != "before-quiesce" && (name == "waiting" || name == "waiting-unrelated-exit" || name == "missing-id" || name == "missing-status")
				if (gs.Annotations[lifecycleOperationAnnotation] != "") != unidentified || q.unquiesced != 0 {
					t.Fatalf("wrong cleanup after worker drain: owner=%q, agent=%+v", gs.Annotations[lifecycleOperationAnnotation], q)
				}
				if stage != "before-quiesce" {
					b = guardGetBackup(t, r, "first")
					condition := meta.FindStatusCondition(b.Status.Conditions, "Unquiesced")
					if condition == nil || (unidentified && condition.Status != metav1.ConditionFalse) || (!unidentified && condition.Reason != "SourceProcessGone") {
						t.Fatalf("wrong original-process cleanup condition: %+v", condition)
					}
				}
			})
		}
	}
}

func TestBackupInitialGameContainerEvidenceUsesExistingDeadline(t *testing.T) {
	for _, name := range []string{"missing-status", "missing-id", "waiting", "terminated"} {
		t.Run(name, func(t *testing.T) {
			r, q := guardFixture(t, guardBackup("first"))
			changeBackupGamePod(t, r, backupGameChanges()[name])
			budgetPass(t, r, "first")
			b := guardGetBackup(t, r, "first")
			deadline := b.Annotations[backupPreparationDeadlineAnnotation]
			if deadline == "" || b.Annotations[backupPreparationAnnotation] != "" || q.quiesced != 0 || b.Status.Phase == gameplanev1alpha1.BackupPhaseFailed {
				t.Fatalf("unproven container was admitted or failed before deadline: %+v %+v", b, q)
			}
			// Reinstantiation cannot refresh the existing preparation budget.
			r = &BackupReconciler{Client: r.Client, Scheme: r.Scheme, AgentClient: q, LogReader: r.LogReader}
			budgetPass(t, r, "first")
			b = guardGetBackup(t, r, "first")
			if b.Annotations[backupPreparationDeadlineAnnotation] != deadline {
				t.Fatal("missing container evidence reset the deadline")
			}
			b.Annotations[backupPreparationDeadlineAnnotation] = time.Now().Add(-time.Minute).UTC().Format(time.RFC3339Nano)
			if err := r.Update(t.Context(), b); err != nil {
				t.Fatal(err)
			}
			budgetPass(t, r, "first")
			if got := guardGetBackup(t, r, "first"); got.Status.Phase != gameplanev1alpha1.BackupPhaseFailed || q.quiesced != 0 {
				t.Fatalf("unproven container outlived its budget: %+v %+v", got.Status, q)
			}
		})
	}
}

func TestBackupUnchangedGameIgnoresAgentRestart(t *testing.T) {
	r, q := guardFixture(t, guardBackup("first"))
	guardPass(t, r, "first")
	b := guardGetBackup(t, r, "first")
	var proof map[string]any
	if err := json.Unmarshal([]byte(b.Annotations[backupPreparationAnnotation]), &proof); err != nil {
		t.Fatal(err)
	}
	if proof["gameContainerID"] != "containerd://original-game" || proof["gameRestartCount"] != float64(0) {
		t.Fatalf("source incarnation was not recorded: %+v", proof)
	}
	changeBackupGamePod(t, r, func(p *corev1.Pod) {
		p.Status.ContainerStatuses = append([]corev1.ContainerStatus{{Name: "agent", ContainerID: "containerd://replacement-agent", RestartCount: 7}}, p.Status.ContainerStatuses...)
	})
	var job batchv1.Job
	if err := r.Get(t.Context(), client.ObjectKeyFromObject(b), &job); err != nil {
		t.Fatal(err)
	}
	job.Status.Succeeded = 1
	if err := r.Status().Update(t.Context(), &job); err != nil {
		t.Fatal(err)
	}
	guardPass(t, r, "first")
	b = guardGetBackup(t, r, "first")
	if b.Status.Phase != gameplanev1alpha1.BackupPhaseSucceeded || b.Status.SnapshotID == "" || q.quiesced != 1 {
		t.Fatalf("unchanged game lost its capture: %+v %+v", b.Status, q)
	}
	if condition := meta.FindStatusCondition(b.Status.Conditions, "Consistency"); condition == nil || condition.Reason != "Quiesced" || condition.Status != metav1.ConditionTrue {
		t.Fatalf("unchanged game lost achieved consistency: %+v", condition)
	}
}

func TestBackupCSIRejectsSamePodContainerRestart(t *testing.T) {
	for _, duringRPC := range []bool{true, false} {
		t.Run(map[bool]string{true: "during-quiesce", false: "before-success"}[duringRPC], func(t *testing.T) {
			b := guardBackup("first")
			b.Spec.Strategy = "volume-snapshot"
			r, q := guardFixture(t, b)
			restart := func() { changeBackupGamePod(t, r, backupGameChanges()["container-id"]) }
			if duringRPC {
				q.afterQuiesce = restart
			} else {
				guardPass(t, r, "first")
				var snapshot snapshotv1.VolumeSnapshot
				if err := r.Get(t.Context(), client.ObjectKeyFromObject(b), &snapshot); err != nil {
					t.Fatal(err)
				}
				ready := true
				snapshot.Status = &snapshotv1.VolumeSnapshotStatus{ReadyToUse: &ready}
				if err := r.Status().Update(t.Context(), &snapshot); err != nil {
					t.Fatal(err)
				}
				restart()
			}
			guardPass(t, r, "first")
			if got := guardGetBackup(t, r, "first"); got.Status.Phase != gameplanev1alpha1.BackupPhaseFailed || got.Status.SnapshotID != "" || !strings.Contains(got.Status.Message, "source changed") {
				t.Fatalf("CSI captured a replacement game incarnation: %+v", got.Status)
			}
		})
	}
}

func TestBackupLegacyLiveIncarnationCannotBeInvented(t *testing.T) {
	for _, proof := range []string{"legacy-preparation", "partial-preparation", "no-preparation", "no-preparation-disabled-agent"} {
		t.Run(proof, func(t *testing.T) {
			r, q := guardFixtureWorkers(t, false, guardBackup("first"))
			budgetPass(t, r, "first")
			worker := budgetReadyWorker(t, r, "first", "legacy-worker")
			budgetPass(t, r, "first")
			b := guardGetBackup(t, r, "first")
			var identity map[string]any
			if err := json.Unmarshal([]byte(b.Annotations[backupPreparationAnnotation]), &identity); err != nil {
				t.Fatal(err)
			}
			delete(identity, "gameRestartCount")
			if proof == "legacy-preparation" {
				delete(identity, "gameContainerID")
			}
			encoded, err := json.Marshal(identity)
			if err != nil {
				t.Fatal(err)
			}
			b.Annotations[backupPreparationAnnotation] = string(encoded)
			if strings.HasPrefix(proof, "no-preparation") {
				delete(b.Annotations, backupPreparationAnnotation)
			}
			if proof == "no-preparation-disabled-agent" {
				r.AgentClient = &agent.Client{Disabled: true}
			}
			if err := r.Update(t.Context(), b); err != nil {
				t.Fatal(err)
			}
			budgetPass(t, r, "first")
			got := guardGetBackup(t, r, "first")
			if got.Status.Phase != gameplanev1alpha1.BackupPhaseFailed || got.Status.SnapshotID != "" || got.Annotations[backupPreparationAnnotation] != b.Annotations[backupPreparationAnnotation] || q.quiesced != 1 || q.unquiesced != 0 {
				t.Fatalf("legacy proof was rebound or cleanup debt lost: %+v %+v", got, q)
			}
			if err := r.Delete(t.Context(), worker); err != nil {
				t.Fatal(err)
			}
			budgetPass(t, r, "first")
			if !strings.HasPrefix(proof, "no-preparation") && q.unquiesced != 1 {
				t.Fatalf("legacy Pod-bound cleanup debt was lost: %+v", q)
			}
			if strings.HasPrefix(proof, "no-preparation") && q.unquiesced != 0 {
				t.Fatal("legacy unbound cleanup adopted the observed Pod")
			}
		})
	}
}

func TestBackupRawCopyDoesNotRequireGameContainerEvidence(t *testing.T) {
	for _, raw := range []bool{true, false} {
		t.Run(map[bool]string{true: "quiesce-not-requested", false: "agent-disabled"}[raw], func(t *testing.T) {
			b := guardBackup("first")
			b.Spec.Quiesce = !raw
			r, q := guardFixture(t, b)
			if !raw {
				r.AgentClient = &agent.Client{Disabled: true}
			}
			changeBackupGamePod(t, r, backupGameChanges()["missing-status"])
			guardPass(t, r, b.Name)
			b = guardGetBackup(t, r, b.Name)
			if b.Annotations[backupAuthorizedWorkerAnnotation] == "" || b.Annotations[backupPreparationAnnotation] != "" || b.Status.CaptureManifest == nil || q.quiesced != 0 {
				t.Fatalf("raw source acquired an incarnation requirement: %+v %+v", b, q)
			}
			want := "agent-unavailable"
			if raw {
				want = "not-requested"
			}
			if b.Status.CaptureManifest.QuiesceOutcome != want {
				t.Fatalf("raw source lost its recorded semantics: %+v", b.Status.CaptureManifest)
			}
		})
	}
}
