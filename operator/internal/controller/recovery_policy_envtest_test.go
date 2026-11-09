//go:build envtest

package controller

import (
	v1 "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"testing"
)

func TestCRDValidation_RecoveryPoliciesRejectCSI(t *testing.T) {
	ns := newNamespace(t)
	for _, tc := range []struct{ name, power, volume string }{{"stopped", "stop-copy-restart", "primary-only"}, {"all-data", "preserve", "all-data"}} {
		t.Run(tc.name, func(t *testing.T) {
			b := &v1.Backup{ObjectMeta: metav1.ObjectMeta{Name: tc.name, Namespace: ns}, Spec: v1.BackupSpec{ServerRef: v1.LocalObjectRef{Name: "game"}, Strategy: "volume-snapshot", PowerPolicy: tc.power, VolumePolicy: tc.volume}}
			if err := k8sClient.Create(t.Context(), b); !apierrors.IsInvalid(err) {
				t.Fatalf("CSI policy accepted: %v", err)
			}
			schedule := &v1.BackupSchedule{ObjectMeta: metav1.ObjectMeta{Name: tc.name, Namespace: ns}, Spec: v1.BackupScheduleSpec{ServerRef: v1.LocalObjectRef{Name: "game"}, Strategy: "volume-snapshot", Schedule: "* * * * *", PowerPolicy: tc.power, VolumePolicy: tc.volume}}
			if err := k8sClient.Create(t.Context(), schedule); !apierrors.IsInvalid(err) {
				t.Fatalf("CSI schedule accepted: %v", err)
			}
		})
	}
	b := &v1.Backup{ObjectMeta: metav1.ObjectMeta{Name: "restic-all", Namespace: ns}, Spec: v1.BackupSpec{ServerRef: v1.LocalObjectRef{Name: "game"}, RepoRef: &v1.SecretKeySelector{Name: "repo", Key: "repo"}, PowerPolicy: "stop-copy-restart", VolumePolicy: "all-data"}}
	if err := k8sClient.Create(t.Context(), b); err != nil {
		t.Fatalf("valid recovery policy rejected: %v", err)
	}
}
