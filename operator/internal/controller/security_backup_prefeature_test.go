package controller

import (
	"context"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	gameplanev1alpha1 "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
)

// A StatefulSet from before spec 030 has no security annotations. Its backup
// and restore pods keep the template group and never pick up a pending
// fsGroup override before the server restarts (FR-011).
func TestAppliedBackupFSGroup_PreFeatureIgnoresPendingOverride(t *testing.T) {
	gs := &gameplanev1alpha1.GameServer{
		ObjectMeta: metav1.ObjectMeta{Name: "smp", Namespace: "games", UID: types.UID("secbackup-prefeature-uid")},
		Spec: gameplanev1alpha1.GameServerSpec{Security: &gameplanev1alpha1.GameServerSecuritySpec{
			FSGroup: &gameplanev1alpha1.SecurityIDOverride{Value: secSettingsInt64(3000)},
		}},
	}
	ss := &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      gs.Name,
			Namespace: gs.Namespace,
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion: gameplanev1alpha1.GroupVersion.String(),
				Kind:       "GameServer",
				Name:       gs.Name,
				UID:        gs.UID,
				Controller: ownerBoolPtr(true),
			}},
		},
	}
	cases := []struct {
		name string
		tmpl *gameplanev1alpha1.GameTemplate
		want int64
	}{
		{name: "template group, not the pending override", tmpl: secBackupTemplate(secSettingsInt64(2000)), want: 2000},
		{name: "no template group uses the backup default", tmpl: secBackupTemplate(nil), want: backupDefaultFSGroup},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := fake.NewClientBuilder().WithScheme(testScheme(t)).WithObjects(ss.DeepCopy()).Build()
			got, err := appliedBackupFSGroup(context.Background(), c, gs, tc.tmpl)
			if err != nil {
				t.Fatalf("appliedBackupFSGroup: %v", err)
			}
			if got == nil || *got != tc.want {
				t.Fatalf("appliedBackupFSGroup = %v, want %d", got, tc.want)
			}
		})
	}
}
