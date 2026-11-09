//go:build e2e

package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// TestBackup_DeleteForgetsSnapshot proves deleting a restic Backup also removes
// its snapshot from the repository: the operator holds the Backup with a
// finalizer, runs `restic forget <id> --prune` in a one-shot Job, and only then
// lets the object go. It also proves a snapshot that is already gone does not
// wedge a later delete.
//
// The repository is shared with every other backup test running in parallel, so
// the assertions are about this test's own snapshot ids only, never about the
// repository being empty.
func TestBackup_DeleteForgetsSnapshot(t *testing.T) {
	t.Parallel()

	ns := "gameplane-games"
	tmpl := "e2e-forget-busybox-tmpl"
	gs := "e2e-forget-target"

	ensureResticRepo(t)

	applyBusyboxTemplate(t, tmpl)
	applyBusyboxGameServer(t, ns, gs, tmpl)
	waitPVCBound(t, ns, gs+"-data", 90*time.Second)

	keepName := "e2e-forget-keep"
	dropName := "e2e-forget-drop"
	twiceName := "e2e-forget-twice"
	ids := map[string]string{}
	for _, name := range []string{keepName, dropName, twiceName} {
		createBackup(t, ns, name, gs, "e2e-restic-creds", "repo")
	}
	for _, name := range []string{keepName, dropName, twiceName} {
		ids[name] = waitBackupSucceeded(t, ns, name, 5*time.Minute)
	}

	// All three snapshots are in the repository before anything is deleted.
	present := resticSnapshotIDs(t)
	for _, name := range []string{keepName, dropName, twiceName} {
		if !snapshotListed(present, ids[name]) {
			t.Fatalf("snapshot %s of Backup %s not in the repository before delete; repo has %v",
				ids[name], name, present)
		}
	}

	// Deleting one Backup forgets exactly its snapshot.
	deleteBackupCR(t, ns, dropName)
	waitBackupGone(t, ns, dropName, 5*time.Minute)
	present = resticSnapshotIDs(t)
	if snapshotListed(present, ids[dropName]) {
		t.Errorf("snapshot %s of deleted Backup %s is still in the repository", ids[dropName], dropName)
	}
	for _, name := range []string{keepName, twiceName} {
		if !snapshotListed(present, ids[name]) {
			t.Errorf("snapshot %s of surviving Backup %s was removed from the repository", ids[name], name)
		}
	}

	// A Backup whose snapshot was already forgotten out of band must still be
	// deletable: restic forgetting an unknown id may not fail the Job.
	runResticJob(t, "restic forget "+ids[twiceName]+" --prune --retry-lock 5m")
	deleteBackupCR(t, ns, twiceName)
	waitBackupGone(t, ns, twiceName, 5*time.Minute)
	envInstance.Eventually(t, time.Minute, func() (bool, string) {
		evs, err := envInstance.K8s.CoreV1().Events(ns).List(context.Background(),
			metav1.ListOptions{FieldSelector: "involvedObject.name=" + twiceName})
		if err != nil {
			return false, "list events: " + err.Error()
		}
		for _, ev := range evs.Items {
			if ev.Reason == "SnapshotForgetAbandoned" {
				t.Fatalf("Backup %s was released by the give-up path: %s", twiceName, ev.Message)
			}
			if ev.Reason == "SnapshotForgotten" {
				return true, ""
			}
		}
		return false, "no SnapshotForgotten event for Backup " + twiceName + " yet"
	})

	present = resticSnapshotIDs(t)
	if !snapshotListed(present, ids[keepName]) {
		t.Errorf("snapshot %s of surviving Backup %s was removed from the repository", ids[keepName], keepName)
	}
}

// deleteBackupCR deletes a Backup CR, tolerating one that is already gone.
func deleteBackupCR(t *testing.T, ns, name string) {
	t.Helper()
	deleteBackupCRContext(context.Background(), t, ns, name)
}

func deleteBackupCRContext(ctx context.Context, t *testing.T, ns, name string) {
	t.Helper()
	err := envInstance.Dyn.Resource(backupGVR).Namespace(ns).
		Delete(ctx, name, metav1.DeleteOptions{})
	if err != nil && !apierrors.IsNotFound(err) {
		t.Fatalf("delete backup %s/%s: %v", ns, name, err)
	}
}

// waitBackupGone polls until the Backup no longer exists. A Backup with a
// snapshot finalizer stays Terminating while its forget Job runs, so this is
// the observable end of the forget.
func waitBackupGone(t *testing.T, ns, name string, timeout time.Duration) {
	t.Helper()
	envInstance.Eventually(t, timeout, func() (bool, string) {
		_, err := envInstance.Dyn.Resource(backupGVR).Namespace(ns).
			Get(context.Background(), name, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			return true, ""
		}
		if err != nil {
			return false, "get backup: " + err.Error()
		}
		return false, "backup " + name + " still present (Terminating)"
	})
}

// recordBackupSnapshotIDs adds the snapshot id of every Backup in items that
// has one to seen, keyed by Backup name. Retention deletes Backups, so a test
// that wants to know which snapshots were trimmed has to capture the ids while
// the Backups still exist.
func recordBackupSnapshotIDs(items []unstructured.Unstructured, seen map[string]string) {
	for i := range items {
		id, _, _ := unstructured.NestedString(items[i].Object, "status", "snapshotID")
		if id != "" {
			seen[items[i].GetName()] = id
		}
	}
}

// snapshotListed reports whether want is among ids. restic reports a snapshot
// as an 8-character short id and a 64-character full id, and ids holds both, so
// an exact match covers whichever form the Backup status carries.
func snapshotListed(ids []string, want string) bool {
	for _, id := range ids {
		if id == want {
			return true
		}
	}
	return false
}

// assertSnapshotsAbsent fails the test (without stopping it) for every id in
// want that is still in the shared restic repository.
func assertSnapshotsAbsent(t *testing.T, want ...string) {
	t.Helper()
	present := resticSnapshotIDs(t)
	for _, id := range want {
		if snapshotListed(present, id) {
			t.Errorf("snapshot %s is still in the restic repository after its Backup was deleted", id)
		}
	}
}

// resticSnapshotIDs lists the shared repository's snapshots and returns each
// one's full and short id.
func resticSnapshotIDs(t *testing.T) []string {
	t.Helper()
	out := runResticJob(t, "restic snapshots --json --retry-lock 5m 2>/dev/null")
	start := strings.Index(out, "[")
	end := strings.LastIndex(out, "]")
	if start < 0 || end < start {
		if strings.Contains(out, "null") {
			return nil // an empty repository can print null instead of []
		}
		t.Fatalf("restic snapshots printed no JSON array:\n%s", out)
	}
	var snaps []struct {
		ID      string `json:"id"`
		ShortID string `json:"short_id"`
	}
	if err := json.Unmarshal([]byte(out[start:end+1]), &snaps); err != nil {
		t.Fatalf("decode restic snapshots output: %v\n%s", err, out)
	}
	var ids []string
	for _, s := range snaps {
		ids = append(ids, s.ID, s.ShortID)
	}
	return ids
}

// runResticJob runs script in a one-shot restic Job against the shared e2e
// repository and returns the container's log once the Job completes. The Job
// uses the same credentials Secret, security posture and pod label (which the
// egress NetworkPolicy selects on) as the operator's own restic Jobs, and is
// deleted when the test ends. A Job that fails or does not finish in six
// minutes fails the test with its log.
func runResticJob(t *testing.T, script string) string {
	t.Helper()
	ctx := context.Background()
	e := envInstance
	ns := "gameplane-games"
	name := fmt.Sprintf("e2e-restic-run-%x", time.Now().UnixNano())

	nonRoot := true
	roRootFS := true
	noPrivEsc := false
	uid := int64(65532)
	backoff := int32(1)
	secretEnv := func(envName, key string) corev1.EnvVar {
		return corev1.EnvVar{Name: envName, ValueFrom: &corev1.EnvVarSource{
			SecretKeyRef: &corev1.SecretKeySelector{
				LocalObjectReference: corev1.LocalObjectReference{Name: "e2e-restic-creds"},
				Key:                  key,
			},
		}}
	}
	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: ns,
			Labels:    map[string]string{"app.kubernetes.io/component": "e2e-fixture"},
		},
		Spec: batchv1.JobSpec{
			BackoffLimit: &backoff,
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{
					"app.kubernetes.io/component": "e2e-fixture",
					"app.kubernetes.io/name":      "gameplane-backup-restore",
				}},
				Spec: corev1.PodSpec{
					RestartPolicy: corev1.RestartPolicyNever,
					SecurityContext: &corev1.PodSecurityContext{
						RunAsNonRoot:   &nonRoot,
						RunAsUser:      &uid,
						RunAsGroup:     &uid,
						SeccompProfile: &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault},
					},
					Containers: []corev1.Container{{
						Name:    "restic",
						Image:   "restic/restic:0.18.1",
						Command: []string{"/bin/sh", "-c"},
						Args:    []string{script},
						Env: []corev1.EnvVar{
							secretEnv("RESTIC_REPOSITORY", "repo"),
							secretEnv("RESTIC_PASSWORD", "password"),
							{Name: "XDG_CACHE_HOME", Value: "/tmp/restic-cache"},
						},
						VolumeMounts: []corev1.VolumeMount{{Name: "cache", MountPath: "/tmp"}},
						SecurityContext: &corev1.SecurityContext{
							RunAsNonRoot:             &nonRoot,
							ReadOnlyRootFilesystem:   &roRootFS,
							AllowPrivilegeEscalation: &noPrivEsc,
							Capabilities:             &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}},
						},
					}},
					Volumes: []corev1.Volume{{
						Name:         "cache",
						VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}},
					}},
				},
			},
		},
	}
	if _, err := e.K8s.BatchV1().Jobs(ns).Create(ctx, job, metav1.CreateOptions{}); err != nil {
		t.Fatalf("create restic job %s: %v", name, err)
	}
	t.Cleanup(func() {
		propagation := metav1.DeletePropagationBackground
		_ = e.K8s.BatchV1().Jobs(ns).Delete(context.Background(), name,
			metav1.DeleteOptions{PropagationPolicy: &propagation})
	})

	deadline := time.Now().Add(6 * time.Minute)
	for {
		j, err := e.K8s.BatchV1().Jobs(ns).Get(ctx, name, metav1.GetOptions{})
		if err == nil {
			for _, c := range j.Status.Conditions {
				if c.Status != corev1.ConditionTrue {
					continue
				}
				switch c.Type {
				case batchv1.JobComplete:
					logs, lerr := e.Kubectl(ctx, "logs", "-n", ns, "job/"+name)
					if lerr != nil {
						t.Fatalf("read logs of restic job %s: %v\n%s", name, lerr, logs)
					}
					return logs
				case batchv1.JobFailed:
					logs, _ := e.Kubectl(ctx, "logs", "-n", ns, "job/"+name, "--tail=50")
					t.Fatalf("restic job %s failed: %s\nlogs:\n%s", name, c.Message, logs)
				}
			}
		}
		if time.Now().After(deadline) {
			logs, _ := e.Kubectl(ctx, "logs", "-n", ns, "job/"+name, "--tail=50")
			t.Fatalf("restic job %s timed out:\nlogs:\n%s", name, logs)
		}
		time.Sleep(2 * time.Second)
	}
}
