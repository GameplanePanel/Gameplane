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

// secBackupTemplate returns a template whose module default fsGroup is fsGroup,
// or no security block when fsGroup is nil.
func secBackupTemplate(fsGroup *int64) *gameplanev1alpha1.GameTemplate {
	tmpl := &gameplanev1alpha1.GameTemplate{}
	if fsGroup != nil {
		tmpl.Spec.Security = &gameplanev1alpha1.GameSecuritySpec{FSGroup: fsGroup}
	}
	return tmpl
}

func TestBuildBackupPodSecurityContext_AppliedFSGroupWins(t *testing.T) {
	desired := secSettingsInt64(2000)
	applied := secSettingsInt64(1000)
	cases := []struct {
		name string
		tmpl *gameplanev1alpha1.GameTemplate
		args []*int64
		want int64
	}{
		{name: "applied group beats the module default", tmpl: secBackupTemplate(desired), args: []*int64{applied}, want: 1000},
		{name: "no argument falls back to the module default", tmpl: secBackupTemplate(desired), args: nil, want: 2000},
		{name: "nil argument falls back to the module default", tmpl: secBackupTemplate(desired), args: []*int64{nil}, want: 2000},
		{name: "applied group without a module default", tmpl: secBackupTemplate(nil), args: []*int64{applied}, want: 1000},
		{name: "no group anywhere uses the backup default", tmpl: secBackupTemplate(nil), args: nil, want: backupDefaultFSGroup},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sc := buildBackupPodSecurityContext(tc.tmpl, tc.args...)
			if sc.FSGroup == nil || *sc.FSGroup != tc.want {
				t.Fatalf("FSGroup = %v, want %d", sc.FSGroup, tc.want)
			}
		})
	}
}

// secBackupObservedSS returns a StatefulSet controlled by gs whose observed
// annotation records the given settings.
func secBackupObservedSS(t *testing.T, gs *gameplanev1alpha1.GameServer, observed gameplanev1alpha1.SecuritySettings) *appsv1.StatefulSet {
	t.Helper()
	raw, err := encodeSecurityAnnotation(observed)
	if err != nil {
		t.Fatalf("encode observed: %v", err)
	}
	return &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:        gs.Name,
			Namespace:   gs.Namespace,
			Annotations: map[string]string{securityAppliedObservedAnnotation: raw},
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion: gameplanev1alpha1.GroupVersion.String(),
				Kind:       "GameServer",
				Name:       gs.Name,
				UID:        gs.UID,
				Controller: ownerBoolPtr(true),
			}},
		},
	}
}

func TestAppliedBackupFSGroup_Sources(t *testing.T) {
	gsUID := types.UID("secbackup-gs-uid")
	newGS := func(override *gameplanev1alpha1.GameServerSecuritySpec) *gameplanev1alpha1.GameServer {
		return &gameplanev1alpha1.GameServer{
			ObjectMeta: metav1.ObjectMeta{Name: "smp", Namespace: "games", UID: gsUID},
			Spec:       gameplanev1alpha1.GameServerSpec{Security: override},
		}
	}
	overrideFS := func(v int64) *gameplanev1alpha1.GameServerSecuritySpec {
		return &gameplanev1alpha1.GameServerSecuritySpec{FSGroup: &gameplanev1alpha1.SecurityIDOverride{Value: secSettingsInt64(v)}}
	}
	appliedFS := func(v *int64) gameplanev1alpha1.SecuritySettings {
		return gameplanev1alpha1.SecuritySettings{FSGroup: gameplanev1alpha1.SecurityIDStatus{Value: v, Source: gameplanev1alpha1.SecuritySourceModuleDefault}}
	}

	cases := []struct {
		name string
		gs   *gameplanev1alpha1.GameServer
		tmpl *gameplanev1alpha1.GameTemplate
		objs func(gs *gameplanev1alpha1.GameServer) []*appsv1.StatefulSet
		want int64
	}{
		{
			name: "never started uses the resolved override, not the template",
			gs:   newGS(overrideFS(3000)),
			tmpl: secBackupTemplate(secSettingsInt64(2000)),
			want: 3000,
		},
		{
			name: "never started without an override uses the module default",
			gs:   newGS(nil),
			tmpl: secBackupTemplate(secSettingsInt64(2000)),
			want: 2000,
		},
		{
			name: "never started with no group anywhere uses the backup default",
			gs:   newGS(nil),
			tmpl: secBackupTemplate(nil),
			want: backupDefaultFSGroup,
		},
		{
			name: "pending start: applied group wins over the desired override",
			gs:   newGS(overrideFS(3000)),
			tmpl: secBackupTemplate(secSettingsInt64(2000)),
			objs: func(gs *gameplanev1alpha1.GameServer) []*appsv1.StatefulSet {
				return []*appsv1.StatefulSet{secBackupObservedSS(t, gs, appliedFS(secSettingsInt64(1000)))}
			},
			want: 1000,
		},
		{
			name: "applied record without a group means the image default",
			gs:   newGS(overrideFS(3000)),
			tmpl: secBackupTemplate(secSettingsInt64(2000)),
			objs: func(gs *gameplanev1alpha1.GameServer) []*appsv1.StatefulSet {
				return []*appsv1.StatefulSet{secBackupObservedSS(t, gs, appliedFS(nil))}
			},
			want: backupDefaultFSGroup,
		},
		{
			name: "a StatefulSet this server does not control is ignored",
			gs:   newGS(overrideFS(3000)),
			tmpl: secBackupTemplate(secSettingsInt64(2000)),
			objs: func(gs *gameplanev1alpha1.GameServer) []*appsv1.StatefulSet {
				ss := secBackupObservedSS(t, gs, appliedFS(secSettingsInt64(1000)))
				ss.OwnerReferences = nil
				return []*appsv1.StatefulSet{ss}
			},
			want: 3000,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b := fake.NewClientBuilder().WithScheme(testScheme(t))
			if tc.objs != nil {
				for _, ss := range tc.objs(tc.gs) {
					b = b.WithObjects(ss)
				}
			}
			got, err := appliedBackupFSGroup(context.Background(), b.Build(), tc.gs, tc.tmpl)
			if err != nil {
				t.Fatalf("appliedBackupFSGroup: %v", err)
			}
			if got == nil || *got != tc.want {
				t.Fatalf("appliedBackupFSGroup = %v, want %d", got, tc.want)
			}
		})
	}
}
