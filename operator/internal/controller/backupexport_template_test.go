package controller

import (
	"context"
	"errors"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	gameplanev1alpha1 "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
)

func exportTemplateServer(uid string) *gameplanev1alpha1.GameServer {
	return &gameplanev1alpha1.GameServer{
		ObjectMeta: metav1.ObjectMeta{Name: "mc", Namespace: exportTestNS, UID: "gs-uid"},
		Spec: gameplanev1alpha1.GameServerSpec{
			TemplateRef: gameplanev1alpha1.GameTemplateRef{Name: "minecraft-java"},
			Version:     "1.21.4",
		},
		Status: gameplanev1alpha1.GameServerStatus{
			TemplateRevision: &gameplanev1alpha1.TemplateRevisionStatus{Effective: "rev-" + uid},
		},
	}
}

func TestWithServerTemplateFillsMissingTemplate(t *testing.T) {
	bk := exportTestBackup()
	bk.Status.CaptureManifest = &gameplanev1alpha1.BackupCaptureManifest{State: "Recorded", ServerUID: "gs-uid"}
	cl := exportTestClient(t, exportTemplateServer("1"))
	r := &BackupExportReconciler{Client: cl, Scheme: cl.Scheme()}

	got, err := r.withServerTemplate(context.Background(), bk)
	if err != nil {
		t.Fatalf("withServerTemplate: %v", err)
	}
	cm := got.Status.CaptureManifest
	if cm.TemplateName != "minecraft-java" || cm.TemplateRevision != "rev-1" || cm.GameVersion != "1.21.4" {
		t.Fatalf("manifest = %+v, want the server's template, revision and version", cm)
	}
	if bk.Status.CaptureManifest.TemplateName != "" {
		t.Fatal("the source Backup was modified")
	}
	m := decodeExportManifest(t, mustExportManifest(t, got))
	if m.Template.Name != "minecraft-java" || m.GameVersion != "1.21.4" {
		t.Fatalf("archive manifest = %+v", m)
	}
}

func TestWithServerTemplateKeepsRecordedTemplate(t *testing.T) {
	bk := exportTestBackup()
	bk.Status.CaptureManifest = &gameplanev1alpha1.BackupCaptureManifest{ServerUID: "gs-uid", TemplateName: "recorded", GameVersion: "1.0"}
	cl := exportTestClient(t, exportTemplateServer("1"))
	r := &BackupExportReconciler{Client: cl, Scheme: cl.Scheme()}

	got, err := r.withServerTemplate(context.Background(), bk)
	if err != nil {
		t.Fatalf("withServerTemplate: %v", err)
	}
	if got.Status.CaptureManifest.TemplateName != "recorded" || got.Status.CaptureManifest.GameVersion != "1.0" {
		t.Fatalf("manifest = %+v, want the recorded template kept", got.Status.CaptureManifest)
	}
}

func TestWithServerTemplateLeavesBackupWhenServerDoesNotMatch(t *testing.T) {
	cases := []struct {
		name string
		cm   *gameplanev1alpha1.BackupCaptureManifest
		objs []client.Object
	}{
		{name: "no manifest", objs: []client.Object{exportTemplateServer("1")}},
		{name: "server gone", cm: &gameplanev1alpha1.BackupCaptureManifest{ServerUID: "gs-uid"}},
		{name: "server replaced", cm: &gameplanev1alpha1.BackupCaptureManifest{ServerUID: "old-uid"}, objs: []client.Object{exportTemplateServer("1")}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			bk := exportTestBackup()
			bk.Status.CaptureManifest = tc.cm
			cl := exportTestClient(t, tc.objs...)
			r := &BackupExportReconciler{Client: cl, Scheme: cl.Scheme()}
			got, err := r.withServerTemplate(context.Background(), bk)
			if err != nil {
				t.Fatalf("withServerTemplate: %v", err)
			}
			if got != bk {
				t.Fatalf("backup was replaced: %+v", got.Status.CaptureManifest)
			}
		})
	}
}

func TestWithServerTemplateReportsLookupErrors(t *testing.T) {
	bk := exportTestBackup()
	bk.Status.CaptureManifest = &gameplanev1alpha1.BackupCaptureManifest{ServerUID: "gs-uid"}
	boom := errors.New("apiserver down")
	cl := interceptor.NewClient(exportTestClient(t).(client.WithWatch), interceptor.Funcs{
		Get: func(context.Context, client.WithWatch, client.ObjectKey, client.Object, ...client.GetOption) error {
			return boom
		},
	})
	r := &BackupExportReconciler{Client: cl, Scheme: cl.Scheme()}
	if _, err := r.withServerTemplate(context.Background(), bk); !errors.Is(err, boom) {
		t.Fatalf("err = %v, want %v", err, boom)
	}
}

func mustExportManifest(t *testing.T, bk *gameplanev1alpha1.Backup) []byte {
	t.Helper()
	raw, err := buildExportManifest(bk, "test", "")
	if err != nil {
		t.Fatalf("buildExportManifest: %v", err)
	}
	return raw
}
