package controller

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	gameplanev1alpha1 "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
)

const exportTestNS = "games"

func exportTestBackup() *gameplanev1alpha1.Backup {
	completed := metav1.NewTime(time.Date(2026, 11, 26, 10, 0, 0, 0, time.UTC))
	return &gameplanev1alpha1.Backup{
		ObjectMeta: metav1.ObjectMeta{Name: "bk", Namespace: exportTestNS, UID: "bk-uid"},
		Spec: gameplanev1alpha1.BackupSpec{
			ServerRef: gameplanev1alpha1.LocalObjectRef{Name: "mc"},
			RepoRef:   &gameplanev1alpha1.SecretKeySelector{Name: "repo", Key: "url"},
			Strategy:  "restic-snapshot",
		},
		Status: gameplanev1alpha1.BackupStatus{
			Phase:          gameplanev1alpha1.BackupPhaseSucceeded,
			SnapshotID:     "snap-1",
			CompletionTime: &completed,
		},
	}
}

func exportTestExport(ttl *int32) *gameplanev1alpha1.BackupExport {
	return &gameplanev1alpha1.BackupExport{
		ObjectMeta: metav1.ObjectMeta{
			Name: "bk-export", Namespace: exportTestNS, UID: "exp-uid",
			CreationTimestamp: metav1.Now(),
		},
		Spec: gameplanev1alpha1.BackupExportSpec{
			BackupRef:  gameplanev1alpha1.LocalObjectRef{Name: "bk"},
			BackupUID:  "bk-uid",
			Format:     "tar.gz",
			TTLSeconds: ttl,
		},
	}
}

func exportTestClient(t *testing.T, objs ...client.Object) client.Client {
	t.Helper()
	s := runtime.NewScheme()
	if err := corev1.AddToScheme(s); err != nil {
		t.Fatalf("corev1 scheme: %v", err)
	}
	if err := gameplanev1alpha1.AddToScheme(s); err != nil {
		t.Fatalf("gameplane scheme: %v", err)
	}
	return fake.NewClientBuilder().
		WithScheme(s).
		WithObjects(objs...).
		WithStatusSubresource(&gameplanev1alpha1.BackupExport{}).
		Build()
}

func decodeExportManifest(t *testing.T, raw []byte) exportManifest {
	t.Helper()
	var m exportManifest
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("decode manifest: %v\n%s", err, raw)
	}
	return m
}

func TestBuildExportManifest_V2CaptureListsIncludedVolumes(t *testing.T) {
	b := exportTestBackup()
	b.Status.CaptureManifest = &gameplanev1alpha1.BackupCaptureManifest{
		FormatVersion:    2,
		State:            "Recorded",
		TemplateName:     "minecraft-java",
		TemplateRevision: "rev-7",
		GameVersion:      "1.21.4",
		ServerUID:        "srv-uid",
		Volumes: []gameplanev1alpha1.BackupVolumeCoverage{
			{LogicalName: "data", Included: true, Classification: "data", ClaimName: "mc-data"},
			{LogicalName: "mods", Included: true, Classification: "rebuildable", ClaimName: "mc-mods"},
			{LogicalName: "extra", Included: false, Classification: "unclassified", ClaimName: "mc-extra"},
		},
	}

	raw, err := buildExportManifest(b, "v0.5.0", "prod")
	if err != nil {
		t.Fatalf("buildExportManifest: %v", err)
	}
	m := decodeExportManifest(t, raw)

	if m.Cluster != "prod" {
		t.Errorf("cluster = %q, want prod", m.Cluster)
	}
	if m.FormatVersion != 1 {
		t.Errorf("formatVersion = %d, want 1", m.FormatVersion)
	}
	if m.Writer != "gameplane v0.5.0" {
		t.Errorf("writer = %q", m.Writer)
	}
	if m.Namespace != exportTestNS {
		t.Errorf("namespace = %q", m.Namespace)
	}
	if m.Server.Name != "mc" || m.Server.UID != "srv-uid" {
		t.Errorf("server = %+v", m.Server)
	}
	if m.Template.Name != "minecraft-java" || m.Template.Revision != "rev-7" {
		t.Errorf("template = %+v", m.Template)
	}
	if m.GameVersion != "1.21.4" {
		t.Errorf("gameVersion = %q", m.GameVersion)
	}
	if m.CapturedAt != "2026-11-26T10:00:00Z" {
		t.Errorf("capturedAt = %q", m.CapturedAt)
	}
	if m.ResticSnapshotID != "snap-1" {
		t.Errorf("resticSnapshotID = %q", m.ResticSnapshotID)
	}
	if m.CaptureFormatVersion != 2 {
		t.Errorf("captureFormatVersion = %d, want 2", m.CaptureFormatVersion)
	}
	want := []exportManifestVolume{
		{LogicalName: "data", Folder: "volumes/data", Classification: "data"},
		{LogicalName: "mods", Folder: "volumes/mods", Classification: "rebuildable"},
	}
	if len(m.Volumes) != len(want) {
		t.Fatalf("volumes = %+v, want %+v", m.Volumes, want)
	}
	for i := range want {
		if m.Volumes[i] != want[i] {
			t.Errorf("volumes[%d] = %+v, want %+v", i, m.Volumes[i], want[i])
		}
	}
}

func TestBuildExportManifest_CarriesClusterFromAnnotation(t *testing.T) {
	withAnn := exportTestExport(nil)
	withAnn.Annotations = map[string]string{backupExportClusterAnnotation: "prod"}
	raw, err := buildExportManifest(exportTestBackup(), "v0.5.0", withAnn.Annotations[backupExportClusterAnnotation])
	if err != nil {
		t.Fatalf("buildExportManifest: %v", err)
	}
	if m := decodeExportManifest(t, raw); m.Cluster != "prod" {
		t.Errorf("cluster = %q, want prod (from annotation)", m.Cluster)
	}

	// A BackupExport without the annotation yields an empty cluster, and the
	// omitempty tag keeps the key out of the manifest.
	noAnn := exportTestExport(nil)
	raw, err = buildExportManifest(exportTestBackup(), "v0.5.0", noAnn.Annotations[backupExportClusterAnnotation])
	if err != nil {
		t.Fatalf("buildExportManifest: %v", err)
	}
	if m := decodeExportManifest(t, raw); m.Cluster != "" {
		t.Errorf("cluster = %q, want empty when annotation is missing", m.Cluster)
	}
	var keys map[string]json.RawMessage
	if err := json.Unmarshal(raw, &keys); err != nil {
		t.Fatalf("decode manifest keys: %v", err)
	}
	if _, ok := keys["cluster"]; ok {
		t.Errorf("manifest carries a cluster key when the annotation is missing: %s", raw)
	}
}

func TestBuildExportManifest_LegacyCaptureGetsDataEntry(t *testing.T) {
	cases := map[string]*gameplanev1alpha1.BackupCaptureManifest{
		"missing capture manifest": nil,
		"format version absent": {
			State: "LegacyUnknown",
			Volumes: []gameplanev1alpha1.BackupVolumeCoverage{
				{LogicalName: "data", Included: true, Classification: "data", ClaimName: "mc-data"},
			},
		},
	}
	for name, cm := range cases {
		t.Run(name, func(t *testing.T) {
			b := exportTestBackup()
			b.Status.CaptureManifest = cm
			raw, err := buildExportManifest(b, "v0.5.0", "")
			if err != nil {
				t.Fatalf("buildExportManifest: %v", err)
			}
			m := decodeExportManifest(t, raw)
			if len(m.Volumes) != 1 || m.Volumes[0] != (exportManifestVolume{LogicalName: "data", Folder: "data", Classification: "data"}) {
				t.Errorf("volumes = %+v, want single legacy data entry", m.Volumes)
			}
			if m.CaptureFormatVersion != 0 {
				t.Errorf("captureFormatVersion = %d, want 0", m.CaptureFormatVersion)
			}
			if m.Server.UID != "" || m.Template.Name != "" {
				t.Errorf("capture-only fields set on legacy manifest: %+v %+v", m.Server, m.Template)
			}
			if m.ResticSnapshotID != "snap-1" {
				t.Errorf("resticSnapshotID = %q", m.ResticSnapshotID)
			}
		})
	}
}

func TestExportBackupProblem(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(b *gameplanev1alpha1.Backup, e *gameplanev1alpha1.BackupExport)
		want   string
	}{
		{"ok", func(*gameplanev1alpha1.Backup, *gameplanev1alpha1.BackupExport) {}, ""},
		{"uid mismatch", func(_ *gameplanev1alpha1.Backup, e *gameplanev1alpha1.BackupExport) {
			e.Spec.BackupUID = "other"
		}, "backup was replaced"},
		{"still running", func(b *gameplanev1alpha1.Backup, _ *gameplanev1alpha1.BackupExport) {
			b.Status.Phase = gameplanev1alpha1.BackupPhaseRunning
		}, "backup has not finished"},
		{"volume snapshot", func(b *gameplanev1alpha1.Backup, _ *gameplanev1alpha1.BackupExport) {
			b.Spec.Strategy = "volume-snapshot"
		}, "snapshot backups cannot be downloaded"},
		{"no snapshot id", func(b *gameplanev1alpha1.Backup, _ *gameplanev1alpha1.BackupExport) {
			b.Status.SnapshotID = ""
		}, "backup has no restic snapshot"},
		{"no repo ref", func(b *gameplanev1alpha1.Backup, _ *gameplanev1alpha1.BackupExport) {
			b.Spec.RepoRef = nil
		}, "backup has no restic snapshot"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b := exportTestBackup()
			e := exportTestExport(nil)
			tc.mutate(b, e)
			if got := exportBackupProblem(e, b); got != tc.want {
				t.Errorf("problem = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestReconcile_MissingBackupFails(t *testing.T) {
	export := exportTestExport(nil)
	cl := exportTestClient(t, export)
	r := &BackupExportReconciler{Client: cl, Scheme: cl.Scheme(), WriterVersion: "test"}

	req := ctrl.Request{NamespacedName: types.NamespacedName{Namespace: exportTestNS, Name: export.Name}}
	if _, err := r.Reconcile(context.Background(), req); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	var got gameplanev1alpha1.BackupExport
	if err := cl.Get(context.Background(), req.NamespacedName, &got); err != nil {
		t.Fatalf("get export: %v", err)
	}
	if got.Status.Phase != gameplanev1alpha1.BackupExportPhaseFailed {
		t.Fatalf("phase = %q, want Failed", got.Status.Phase)
	}
	if got.Status.Message != "backup not found" {
		t.Errorf("message = %q", got.Status.Message)
	}
}

func TestReconcile_ExpiredExportIsRetired(t *testing.T) {
	export := exportTestExport(nil)
	backup := exportTestBackup()
	cl := exportTestClient(t, export, backup)
	now := time.Now().Add(48 * time.Hour)
	r := &BackupExportReconciler{
		Client: cl, Scheme: cl.Scheme(), WriterVersion: "test",
		DefaultTTL: time.Hour,
		Now:        func() time.Time { return now },
	}

	req := ctrl.Request{NamespacedName: types.NamespacedName{Namespace: exportTestNS, Name: export.Name}}
	if _, err := r.Reconcile(context.Background(), req); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	var got gameplanev1alpha1.BackupExport
	if err := cl.Get(context.Background(), req.NamespacedName, &got); err != nil {
		t.Fatalf("get export: %v", err)
	}
	if got.Status.Phase != gameplanev1alpha1.BackupExportPhaseExpired {
		t.Fatalf("phase = %q, want Expired", got.Status.Phase)
	}
	if got.Status.Message != "download expired" {
		t.Errorf("message = %q", got.Status.Message)
	}
	if got.Status.CompletionTime == nil {
		t.Error("completionTime not recorded")
	}
}

func TestExportTTL(t *testing.T) {
	short := int32(120)
	cases := []struct {
		name string
		ttl  *int32
		def  time.Duration
		want time.Duration
	}{
		{"spec wins", &short, time.Hour, 2 * time.Minute},
		{"default used", nil, 30 * time.Minute, 30 * time.Minute},
		{"zero default is one hour", nil, 0, time.Hour},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := exportTTL(exportTestExport(tc.ttl), tc.def); got != tc.want {
				t.Errorf("exportTTL = %s, want %s", got, tc.want)
			}
		})
	}
}

func TestObserveExportPod_PhaseMapping(t *testing.T) {
	ready := &corev1.Pod{Status: corev1.PodStatus{
		Phase:      corev1.PodRunning,
		Conditions: []corev1.PodCondition{{Type: corev1.PodReady, Status: corev1.ConditionTrue}},
	}}
	pending := &corev1.Pod{Status: corev1.PodStatus{Phase: corev1.PodPending}}
	completed := &corev1.Pod{Status: corev1.PodStatus{
		Phase: corev1.PodSucceeded,
		ContainerStatuses: []corev1.ContainerStatus{{
			Name: exportWorkerContainer,
			State: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{
				Message: `{"bytesSent":4096}`,
			}},
		}},
	}}
	failedMsg := &corev1.Pod{Status: corev1.PodStatus{
		Phase: corev1.PodFailed,
		ContainerStatuses: []corev1.ContainerStatus{{
			Name: exportWorkerContainer,
			State: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{
				Message: `{"error":"restic dump exited 1"}`,
			}},
		}},
	}}
	failedReason := &corev1.Pod{Status: corev1.PodStatus{
		Phase: corev1.PodFailed,
		ContainerStatuses: []corev1.ContainerStatus{{
			Name: exportWorkerContainer,
			State: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{
				Reason: "OOMKilled",
			}},
		}},
	}}

	if got := observeExportPod(nil); got.Phase != gameplanev1alpha1.BackupExportPhasePending {
		t.Errorf("nil pod phase = %q", got.Phase)
	}
	if got := observeExportPod(pending); got.Phase != gameplanev1alpha1.BackupExportPhasePending {
		t.Errorf("pending pod phase = %q", got.Phase)
	}
	if got := observeExportPod(ready); got.Phase != gameplanev1alpha1.BackupExportPhaseReady {
		t.Errorf("ready pod phase = %q", got.Phase)
	}

	got := observeExportPod(completed)
	if got.Phase != gameplanev1alpha1.BackupExportPhaseCompleted {
		t.Fatalf("completed pod phase = %q", got.Phase)
	}
	if got.BytesSent == nil || *got.BytesSent != 4096 {
		t.Errorf("bytesSent = %v, want 4096", got.BytesSent)
	}

	got = observeExportPod(failedMsg)
	if got.Phase != gameplanev1alpha1.BackupExportPhaseFailed || got.Message != "restic dump exited 1" {
		t.Errorf("failed with error = %+v", got)
	}
	got = observeExportPod(failedReason)
	if got.Phase != gameplanev1alpha1.BackupExportPhaseFailed || got.Message != "OOMKilled" {
		t.Errorf("failed with reason = %+v", got)
	}
}

func TestApplyExportObservation_RecordsBytesAndStartOnce(t *testing.T) {
	var s gameplanev1alpha1.BackupExportStatus
	first := time.Date(2026, 11, 26, 10, 0, 0, 0, time.UTC)
	later := first.Add(time.Minute)

	applyExportObservation(&s, exportObservation{Phase: gameplanev1alpha1.BackupExportPhaseReady}, "bk-export", "snap-1", first)
	applyExportObservation(&s, exportObservation{Phase: gameplanev1alpha1.BackupExportPhaseReady}, "bk-export", "snap-1", later)
	if s.Phase != gameplanev1alpha1.BackupExportPhaseReady || s.ServiceName != "bk-export" || s.SnapshotID != "snap-1" {
		t.Fatalf("status after ready = %+v", s)
	}
	if s.StartTime == nil || !s.StartTime.Time.Equal(first) {
		t.Errorf("startTime = %v, want %v", s.StartTime, first)
	}

	bytes := int64(2048)
	applyExportObservation(&s, exportObservation{Phase: gameplanev1alpha1.BackupExportPhaseCompleted, BytesSent: &bytes}, "bk-export", "snap-1", later)
	if s.Phase != gameplanev1alpha1.BackupExportPhaseCompleted {
		t.Fatalf("phase = %q", s.Phase)
	}
	if s.BytesSent == nil || s.BytesSent.Value() != 2048 {
		t.Errorf("bytesSent = %v, want 2048", s.BytesSent)
	}
	if s.CompletionTime == nil || !s.CompletionTime.Time.Equal(later) {
		t.Errorf("completionTime = %v, want %v", s.CompletionTime, later)
	}
}

func TestParseExportWorkerReport_NonJSONIsError(t *testing.T) {
	rep := parseExportWorkerReport("  disk full\n")
	if rep.Error != "disk full" || rep.BytesSent != nil {
		t.Errorf("report = %+v", rep)
	}
}
