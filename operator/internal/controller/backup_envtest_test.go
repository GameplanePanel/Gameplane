//go:build envtest

package controller

import (
	"context"
	"strings"
	"testing"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	gameplanev1alpha1 "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
)

// TestBackup_CreatesJobWithExpectedSpec verifies the BackupReconciler
// produces a restic Job whose pod spec matches the documented
// invariants: read-only data mount, restic image+args, repo creds
// sourced from the Secret, restricted SecurityContext.
func TestBackup_CreatesJobWithExpectedSpec(t *testing.T) {
	ns := newNamespace(t)
	startMgr(t, ns, withBackupReconciler())
	seedGameServer(t, ns, "smp")

	if err := k8sClient.Create(context.Background(), buildResticRepoSecret(ns, "repo-secret")); err != nil {
		t.Fatalf("create secret: %v", err)
	}
	if err := k8sClient.Create(context.Background(), buildBackup(ns, "smp-manual", "smp", "repo-secret")); err != nil {
		t.Fatalf("create backup: %v", err)
	}

	var ps corev1.PodSpec
	eventually(t, func() (bool, string) {
		j, ok := getJob(t, ns, "smp-manual")
		if !ok {
			return false, "job not yet created"
		}
		ps = j.Spec.Template.Spec
		return true, ""
	})

	if ps.RestartPolicy != corev1.RestartPolicyNever {
		t.Errorf("RestartPolicy = %q, want Never", ps.RestartPolicy)
	}
	if len(ps.Containers) != 1 {
		t.Fatalf("Containers = %d, want 1", len(ps.Containers))
	}
	c := ps.Containers[0]
	if c.Name != "restic" {
		t.Errorf("container name = %q, want restic", c.Name)
	}
	if !strings.HasPrefix(c.Image, "restic/restic:") {
		t.Errorf("image = %q, want restic/restic:*", c.Image)
	}
	if got, want := c.Args, []string{"backup", "/data", "--json", "--retry-lock", "10m", "--tag", "gameplane"}; !equalStrings(got, want) {
		t.Errorf("args = %v, want %v", got, want)
	}

	// Init container runs `restic init` so first-ever backups against
	// an empty repo succeed.
	if len(ps.InitContainers) != 1 || ps.InitContainers[0].Name != "restic-init" {
		t.Errorf("expected one restic-init initContainer, got %+v", ps.InitContainers)
	}

	// Repo + password env vars must come from the configured Secret.
	gotRepoSecret, gotPwSecret := "", ""
	for _, e := range c.Env {
		switch e.Name {
		case "RESTIC_REPOSITORY":
			if e.ValueFrom != nil && e.ValueFrom.SecretKeyRef != nil {
				gotRepoSecret = e.ValueFrom.SecretKeyRef.Name
			}
		case "RESTIC_PASSWORD":
			if e.ValueFrom != nil && e.ValueFrom.SecretKeyRef != nil {
				gotPwSecret = e.ValueFrom.SecretKeyRef.Name
			}
		}
	}
	if gotRepoSecret != "repo-secret" {
		t.Errorf("RESTIC_REPOSITORY secret = %q, want repo-secret", gotRepoSecret)
	}
	if gotPwSecret != "repo-secret" {
		t.Errorf("RESTIC_PASSWORD secret = %q, want repo-secret", gotPwSecret)
	}

	// Data volume must be mounted read-only and reference <gs>-data PVC.
	var dataVol *corev1.Volume
	for i := range ps.Volumes {
		if ps.Volumes[i].Name == "data" {
			dataVol = &ps.Volumes[i]
			break
		}
	}
	if dataVol == nil {
		t.Fatal("no data volume found")
	}
	if dataVol.PersistentVolumeClaim == nil || dataVol.PersistentVolumeClaim.ClaimName != "smp-data" {
		t.Errorf("data PVC = %+v, want claim smp-data", dataVol.PersistentVolumeClaim)
	}
	if dataVol.PersistentVolumeClaim != nil && !dataVol.PersistentVolumeClaim.ReadOnly {
		t.Error("data PVC should be ReadOnly")
	}

	// Pod-level securityContext: nonroot, restricted seccomp.
	if ps.SecurityContext == nil || ps.SecurityContext.RunAsNonRoot == nil || !*ps.SecurityContext.RunAsNonRoot {
		t.Error("podSpec should RunAsNonRoot=true")
	}
	if ps.SecurityContext != nil && ps.SecurityContext.SeccompProfile != nil {
		if ps.SecurityContext.SeccompProfile.Type != corev1.SeccompProfileTypeRuntimeDefault {
			t.Errorf("SeccompProfile = %q, want RuntimeDefault", ps.SecurityContext.SeccompProfile.Type)
		}
	}

	// Container-level securityContext: caps drop ALL, ROFS, no priv esc.
	if c.SecurityContext == nil {
		t.Fatal("container SecurityContext nil")
	}
	if c.SecurityContext.ReadOnlyRootFilesystem == nil || !*c.SecurityContext.ReadOnlyRootFilesystem {
		t.Error("container should have ReadOnlyRootFilesystem=true")
	}
	if c.SecurityContext.AllowPrivilegeEscalation == nil || *c.SecurityContext.AllowPrivilegeEscalation {
		t.Error("container should disallow privilege escalation")
	}
	if c.SecurityContext.Capabilities == nil ||
		len(c.SecurityContext.Capabilities.Drop) == 0 ||
		c.SecurityContext.Capabilities.Drop[0] != "ALL" {
		t.Errorf("container should drop ALL capabilities, got %+v", c.SecurityContext.Capabilities)
	}
}

// TestBackup_OwnerReferenceSet verifies the Job is owned by the Backup
// so cluster GC reaps it on Backup delete.
func TestBackup_OwnerReferenceSet(t *testing.T) {
	ns := newNamespace(t)
	startMgr(t, ns, withBackupReconciler())
	seedGameServer(t, ns, "smp")

	if err := k8sClient.Create(context.Background(), buildResticRepoSecret(ns, "repo")); err != nil {
		t.Fatalf("create secret: %v", err)
	}
	if err := k8sClient.Create(context.Background(), buildBackup(ns, "smp-manual", "smp", "repo")); err != nil {
		t.Fatalf("create backup: %v", err)
	}

	eventually(t, func() (bool, string) {
		j, ok := getJob(t, ns, "smp-manual")
		if !ok {
			return false, "job not yet created"
		}
		for _, ref := range j.OwnerReferences {
			if ref.Kind == "Backup" && ref.Name == "smp-manual" && ref.Controller != nil && *ref.Controller {
				return true, ""
			}
		}
		return false, "no Backup controller ownerRef on Job"
	})
}

// TestBackup_MirrorsJobActiveToRunning patches the Job to Active=1 and
// expects the Backup status to roll forward to Running with StartTime.
func TestBackup_MirrorsJobActiveToRunning(t *testing.T) {
	ns := newNamespace(t)
	startMgr(t, ns, withBackupReconciler())
	seedGameServer(t, ns, "smp")

	if err := k8sClient.Create(context.Background(), buildResticRepoSecret(ns, "repo")); err != nil {
		t.Fatalf("create secret: %v", err)
	}
	if err := k8sClient.Create(context.Background(), buildBackup(ns, "smp-manual", "smp", "repo")); err != nil {
		t.Fatalf("create backup: %v", err)
	}

	eventually(t, func() (bool, string) {
		_, ok := getJob(t, ns, "smp-manual")
		return ok, "waiting for job"
	})

	patchJobStatus(t, ns, "smp-manual", func(s *batchv1.JobStatus) {
		now := metav1.Now()
		s.Active = 1
		s.StartTime = &now
	})

	eventually(t, func() (bool, string) {
		b := getBackup(t, ns, "smp-manual")
		if b.Status.Phase == gameplanev1alpha1.BackupPhaseRunning && b.Status.StartTime != nil {
			return true, ""
		}
		return false, describeBackupStatus(b)
	})
}

// TestBackup_MirrorsJobSucceededToSucceeded patches Job to Succeeded
// and expects Backup phase Succeeded + Completed=True condition.
func TestBackup_MirrorsJobSucceededToSucceeded(t *testing.T) {
	ns := newNamespace(t)
	startMgr(t, ns, withBackupReconciler())
	seedGameServer(t, ns, "smp")

	if err := k8sClient.Create(context.Background(), buildResticRepoSecret(ns, "repo")); err != nil {
		t.Fatalf("create secret: %v", err)
	}
	if err := k8sClient.Create(context.Background(), buildBackup(ns, "smp-manual", "smp", "repo")); err != nil {
		t.Fatalf("create backup: %v", err)
	}

	eventually(t, func() (bool, string) {
		_, ok := getJob(t, ns, "smp-manual")
		return ok, "waiting for job"
	})

	patchJobStatus(t, ns, "smp-manual", func(s *batchv1.JobStatus) {
		now := metav1.Now()
		s.Succeeded = 1
		s.StartTime = &now
		s.CompletionTime = &now
	})

	eventually(t, func() (bool, string) {
		b := getBackup(t, ns, "smp-manual")
		if b.Status.Phase != gameplanev1alpha1.BackupPhaseSucceeded {
			return false, describeBackupStatus(b)
		}
		if b.Status.CompletionTime == nil {
			return false, "completionTime unset"
		}
		var completed *metav1.Condition
		for i := range b.Status.Conditions {
			if b.Status.Conditions[i].Type == "Completed" {
				completed = &b.Status.Conditions[i]
				break
			}
		}
		if completed == nil {
			return false, "Completed condition missing"
		}
		if completed.Status != metav1.ConditionTrue || completed.Reason != "Succeeded" {
			return false, "Completed condition wrong: status=" + string(completed.Status) + " reason=" + completed.Reason
		}
		return true, ""
	})
}

// TestBackup_MirrorsJobFailedToFailed patches Job to Failed and expects
// Backup phase Failed with the documented message.
func TestBackup_MirrorsJobFailedToFailed(t *testing.T) {
	ns := newNamespace(t)
	startMgr(t, ns, withBackupReconciler())
	seedGameServer(t, ns, "smp")

	if err := k8sClient.Create(context.Background(), buildResticRepoSecret(ns, "repo")); err != nil {
		t.Fatalf("create secret: %v", err)
	}
	if err := k8sClient.Create(context.Background(), buildBackup(ns, "smp-manual", "smp", "repo")); err != nil {
		t.Fatalf("create backup: %v", err)
	}

	eventually(t, func() (bool, string) {
		_, ok := getJob(t, ns, "smp-manual")
		return ok, "waiting for job"
	})

	patchJobStatus(t, ns, "smp-manual", func(s *batchv1.JobStatus) {
		s.Failed = 1
	})

	eventually(t, func() (bool, string) {
		b := getBackup(t, ns, "smp-manual")
		if b.Status.Phase != gameplanev1alpha1.BackupPhaseFailed {
			return false, describeBackupStatus(b)
		}
		if b.Status.Message != "backup job reported Failed" {
			return false, "message = " + b.Status.Message
		}
		return true, ""
	})
}

// TestBackup_StableInTerminalPhase asserts that once a Backup reaches
// a terminal phase the reconciler stops mutating its Status. (We can't
// cleanly assert "the Job is never re-created after deletion" because
// controller-runtime's workqueue may have a Reconcile already queued
// against a stale cache snapshot — see early-return logic in
// backup_controller.go for the actual guarantee.)
func TestBackup_StableInTerminalPhase(t *testing.T) {
	ns := newNamespace(t)
	startMgr(t, ns, withBackupReconciler())
	seedGameServer(t, ns, "smp")

	if err := k8sClient.Create(context.Background(), buildResticRepoSecret(ns, "repo")); err != nil {
		t.Fatalf("create secret: %v", err)
	}
	if err := k8sClient.Create(context.Background(), buildBackup(ns, "smp-manual", "smp", "repo")); err != nil {
		t.Fatalf("create backup: %v", err)
	}

	eventually(t, func() (bool, string) {
		_, ok := getJob(t, ns, "smp-manual")
		return ok, "waiting for job"
	})

	patchJobStatus(t, ns, "smp-manual", func(s *batchv1.JobStatus) {
		now := metav1.Now()
		s.Succeeded = 1
		s.CompletionTime = &now
	})
	eventually(t, func() (bool, string) {
		b := getBackup(t, ns, "smp-manual")
		return b.Status.Phase == gameplanev1alpha1.BackupPhaseSucceeded, describeBackupStatus(b)
	})

	// After a brief settling period, capture ResourceVersion. A correct
	// terminal short-circuit means no further Status updates → RV stays
	// stable.
	time.Sleep(300 * time.Millisecond)
	rv := getBackup(t, ns, "smp-manual").ResourceVersion

	consistently(t, time.Second, func() (bool, string) {
		b := getBackup(t, ns, "smp-manual")
		if b.Status.Phase != gameplanev1alpha1.BackupPhaseSucceeded {
			return false, "phase regressed to " + string(b.Status.Phase)
		}
		if b.ResourceVersion != rv {
			return false, "resourceVersion bumped from " + rv + " to " + b.ResourceVersion + " (controller still updating Status after terminal phase)"
		}
		return true, ""
	})
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestBackup_PassesTagsToRestic asserts spec.tags become `--tag` args
// on the restic container, in order, after the default "gameplane" tag.
func TestBackup_PassesTagsToRestic(t *testing.T) {
	ns := newNamespace(t)
	startMgr(t, ns, withBackupReconciler())
	seedGameServer(t, ns, "smp")

	if err := k8sClient.Create(context.Background(), buildResticRepoSecret(ns, "repo")); err != nil {
		t.Fatalf("create secret: %v", err)
	}
	b := buildBackup(ns, "smp-tagged", "smp", "repo")
	b.Spec.Tags = []string{"nightly", "preupgrade"}
	if err := k8sClient.Create(context.Background(), b); err != nil {
		t.Fatalf("create backup: %v", err)
	}

	eventually(t, func() (bool, string) {
		j, ok := getJob(t, ns, "smp-tagged")
		if !ok {
			return false, "job not yet created"
		}
		want := []string{"backup", "/data", "--json", "--retry-lock", "10m", "--tag", "gameplane", "--tag", "nightly", "--tag", "preupgrade"}
		if !equalStrings(j.Spec.Template.Spec.Containers[0].Args, want) {
			return false, "args = " + strings.Join(j.Spec.Template.Spec.Containers[0].Args, " ")
		}
		return true, ""
	})
}

// TestBackup_QuiesceAnnotationSetBeforeJob verifies that with a real
// agent client (faked) and spec.quiesce=true (the default), the
// controller calls Quiesce and stamps the quiesced-at annotation
// before the restic Job appears.
func TestBackup_QuiesceAnnotationSetBeforeJob(t *testing.T) {
	ns := newNamespace(t)
	fa := &fakeAgent{}
	startMgr(t, ns, withBackupReconciler(backupReconcilerOpts{agent: fa}))
	seedGameServer(t, ns, "smp")

	if err := k8sClient.Create(context.Background(), buildResticRepoSecret(ns, "repo")); err != nil {
		t.Fatalf("create secret: %v", err)
	}
	b := buildBackup(ns, "smp-q", "smp", "repo")
	b.Spec.Quiesce = true
	if err := k8sClient.Create(context.Background(), b); err != nil {
		t.Fatalf("create backup: %v", err)
	}

	eventually(t, func() (bool, string) {
		got := getBackup(t, ns, "smp-q")
		if _, ok := got.Annotations["backup.gameplane.local/quiesced-at"]; !ok {
			return false, "quiesced-at annotation missing"
		}
		if fa.quiesceCount() == 0 {
			return false, "agent.Quiesce never called"
		}
		return true, ""
	})

	// Job exists and was created at-or-after the quiesce call.
	eventually(t, func() (bool, string) {
		_, ok := getJob(t, ns, "smp-q")
		return ok, "job not yet created"
	})

	// Once the Job goes to Succeeded, the controller must call Unquiesce.
	patchJobStatus(t, ns, "smp-q", func(s *batchv1.JobStatus) {
		now := metav1.Now()
		s.Succeeded = 1
		s.CompletionTime = &now
	})
	eventually(t, func() (bool, string) {
		if fa.unquiesceCount() == 0 {
			return false, "agent.Unquiesce never called"
		}
		got := getBackup(t, ns, "smp-q")
		if _, ok := got.Annotations["backup.gameplane.local/unquiesced-at"]; !ok {
			return false, "unquiesced-at annotation missing"
		}
		return true, ""
	})
}

// TestBackup_PopulatesSnapshotIDFromLogs feeds a fake restic --json
// summary line and asserts the controller reads SnapshotID + Size out
// of it on Succeeded.
func TestBackup_PopulatesSnapshotIDFromLogs(t *testing.T) {
	ns := newNamespace(t)
	logs := &fakeLogReader{
		body: `{"message_type":"summary","snapshot_id":"deadbeef","total_bytes_processed":1048576}` + "\n",
	}
	startMgr(t, ns, withBackupReconciler(backupReconcilerOpts{logs: logs}))
	seedGameServer(t, ns, "smp")

	if err := k8sClient.Create(context.Background(), buildResticRepoSecret(ns, "repo")); err != nil {
		t.Fatalf("create secret: %v", err)
	}
	if err := k8sClient.Create(context.Background(), buildBackup(ns, "smp-sum", "smp", "repo")); err != nil {
		t.Fatalf("create backup: %v", err)
	}
	eventually(t, func() (bool, string) {
		_, ok := getJob(t, ns, "smp-sum")
		return ok, "waiting for job"
	})
	patchJobStatus(t, ns, "smp-sum", func(s *batchv1.JobStatus) {
		now := metav1.Now()
		s.Succeeded = 1
		s.CompletionTime = &now
	})

	eventually(t, func() (bool, string) {
		got := getBackup(t, ns, "smp-sum")
		if got.Status.Phase != gameplanev1alpha1.BackupPhaseSucceeded {
			return false, describeBackupStatus(got)
		}
		if got.Status.SnapshotID != "deadbeef" {
			return false, "snapshotID = " + got.Status.SnapshotID
		}
		if got.Status.Size == nil || got.Status.Size.Value() != 1048576 {
			return false, describeBackupStatus(got)
		}
		return true, ""
	})
}

// TestBackup_FailsFastOnMissingGameServer — a Backup whose serverRef
// resolves to nothing must go Failed with an explanatory message
// instead of producing a Job whose pod waits forever on a missing PVC.
func TestBackup_FailsFastOnMissingGameServer(t *testing.T) {
	ns := newNamespace(t)
	startMgr(t, ns, withBackupReconciler())

	if err := k8sClient.Create(context.Background(), buildResticRepoSecret(ns, "repo")); err != nil {
		t.Fatalf("create secret: %v", err)
	}
	if err := k8sClient.Create(context.Background(), buildBackup(ns, "smp-orphan", "no-such-server", "repo")); err != nil {
		t.Fatalf("create backup: %v", err)
	}

	eventually(t, func() (bool, string) {
		got := getBackup(t, ns, "smp-orphan")
		if got.Status.Phase != gameplanev1alpha1.BackupPhaseFailed {
			return false, "phase=" + string(got.Status.Phase)
		}
		if got.Status.Message == "" {
			return false, "Failed but no message"
		}
		return true, ""
	})

	if _, ok := getJob(t, ns, "smp-orphan"); ok {
		t.Error("no Job must be created for a Backup with an unresolvable serverRef")
	}
}

// TestBackup_FailsFastOnMissingRepoSecret — a Backup whose repoRef
// Secret doesn't exist must go Failed instead of producing a Job whose
// pod is stuck in CreateContainerConfigError.
func TestBackup_FailsFastOnMissingRepoSecret(t *testing.T) {
	ns := newNamespace(t)
	startMgr(t, ns, withBackupReconciler())
	seedGameServer(t, ns, "smp")

	if err := k8sClient.Create(context.Background(), buildBackup(ns, "smp-nosecret", "smp", "no-such-secret")); err != nil {
		t.Fatalf("create backup: %v", err)
	}

	eventually(t, func() (bool, string) {
		got := getBackup(t, ns, "smp-nosecret")
		if got.Status.Phase != gameplanev1alpha1.BackupPhaseFailed {
			return false, "phase=" + string(got.Status.Phase)
		}
		if got.Status.Message == "" {
			return false, "Failed but no message"
		}
		return true, ""
	})

	if _, ok := getJob(t, ns, "smp-nosecret"); ok {
		t.Error("no Job must be created for a Backup with a missing repo Secret")
	}
}

// TestBackup_FailsFastOnSecretMissingKeys — a repo Secret that exists but
// lacks the "repo" key (e.g. an old destination written under "url")
// must fail the Backup with a clear message, not stall it Pending while
// the Job pod sits in CreateContainerConfigError.
func TestBackup_FailsFastOnSecretMissingKeys(t *testing.T) {
	ns := newNamespace(t)
	startMgr(t, ns, withBackupReconciler())
	seedGameServer(t, ns, "smp")

	// password present, repo absent.
	badSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "legacy-url", Namespace: ns},
		StringData: map[string]string{"url": "rest:http://restic.local/repo", "password": "p"},
	}
	if err := k8sClient.Create(context.Background(), badSecret); err != nil {
		t.Fatalf("create secret: %v", err)
	}
	if err := k8sClient.Create(context.Background(), buildBackup(ns, "smp-badkey", "smp", "legacy-url")); err != nil {
		t.Fatalf("create backup: %v", err)
	}

	eventually(t, func() (bool, string) {
		got := getBackup(t, ns, "smp-badkey")
		if got.Status.Phase != gameplanev1alpha1.BackupPhaseFailed {
			return false, "phase=" + string(got.Status.Phase)
		}
		if !strings.Contains(got.Status.Message, "repo") {
			return false, "message should name the missing key: " + got.Status.Message
		}
		return true, ""
	})

	if _, ok := getJob(t, ns, "smp-badkey"); ok {
		t.Error("no Job must be created for a Backup whose repo Secret lacks required keys")
	}
}

// TestBackup_UsesTemplateSecurityContext verifies the backup Job pod uses
// the FSGroup from the GameTemplate's security context rather than hardcoding 65532.
func TestBackup_UsesTemplateSecurityContext(t *testing.T) {
	ns := newNamespace(t)
	startMgr(t, ns, withBackupReconciler())
	tmplName := uniqueName("fsg")
	tmpl := buildGameTemplate(tmplName)
	tmpl.Spec.Security = &gameplanev1alpha1.GameSecuritySpec{FSGroup: ptrInt64(2000)}
	if err := k8sClient.Create(context.Background(), tmpl); err != nil {
		t.Fatalf("create template: %v", err)
	}
	deleteCleanup(t, tmpl)
	gs := buildGameServer(ns, "smp", tmplName)
	if err := k8sClient.Create(context.Background(), gs); err != nil {
		t.Fatalf("create gameserver: %v", err)
	}
	if err := k8sClient.Create(context.Background(), buildResticRepoSecret(ns, "repo-secret")); err != nil {
		t.Fatalf("create secret: %v", err)
	}
	if err := k8sClient.Create(context.Background(), buildBackup(ns, "smp-manual", "smp", "repo-secret")); err != nil {
		t.Fatalf("create backup: %v", err)
	}

	var ps corev1.PodSpec
	eventually(t, func() (bool, string) {
		j, ok := getJob(t, ns, "smp-manual")
		if !ok {
			return false, "job not yet created"
		}
		ps = j.Spec.Template.Spec
		return true, ""
	})

	if ps.SecurityContext == nil {
		t.Fatal("pod SecurityContext nil")
	}
	if ps.SecurityContext.FSGroup == nil {
		t.Fatal("FSGroup nil")
	}
	if *ps.SecurityContext.FSGroup != 2000 {
		t.Errorf("FSGroup = %d, want 2000", *ps.SecurityContext.FSGroup)
	}
}

// TestBackupJobPermanentlyFailed_ConditionNotCounter verifies that a Job with
// Failed>0 but no Failed condition is still retrying, and that only the
// Failed=True condition counts as permanent failure.
func TestBackupJobPermanentlyFailed_ConditionNotCounter(t *testing.T) {
	retrying := &batchv1.Job{Status: batchv1.JobStatus{Failed: 1}}
	if jobPermanentlyFailed(retrying) {
		t.Error("Failed counter without a Failed condition must not be permanent failure")
	}
	failed := &batchv1.Job{Status: batchv1.JobStatus{Conditions: []batchv1.JobCondition{{
		Type: batchv1.JobFailed, Status: corev1.ConditionTrue,
	}}}}
	if !jobPermanentlyFailed(failed) {
		t.Error("Failed=True condition must be permanent failure")
	}
}

func ptrInt64(v int64) *int64 {
	return &v
}
