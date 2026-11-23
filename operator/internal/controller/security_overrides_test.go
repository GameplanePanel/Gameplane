package controller

import (
	"context"
	"reflect"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	gameplanev1alpha1 "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
)

func secInt(v int64) *int64 { return &v }

func secBool(v bool) *bool { return &v }

func secTemplate(sec *gameplanev1alpha1.GameSecuritySpec) *gameplanev1alpha1.GameTemplate {
	tmpl := &gameplanev1alpha1.GameTemplate{}
	tmpl.Name = "secure-game"
	tmpl.Spec.Game = "secure-game"
	tmpl.Spec.Security = sec
	return tmpl
}

func secServer(sec *gameplanev1alpha1.GameServerSecuritySpec) *gameplanev1alpha1.GameServer {
	gs := &gameplanev1alpha1.GameServer{}
	gs.Name = "alpha"
	gs.Namespace = "games"
	gs.UID = types.UID("alpha-uid")
	gs.Spec.Security = sec
	return gs
}

// TestResolveGameSecurity_Sources is the resolver table (research R4): every id
// source, the override over module order, and the BuiltInDefault ownership
// check.
func TestResolveGameSecurity_Sources(t *testing.T) {
	cases := []struct {
		name string
		tmpl *gameplanev1alpha1.GameTemplate
		gs   *gameplanev1alpha1.GameServer
		want resolvedGameSecurity
	}{
		{
			name: "nil template and nil server read as image defaults",
			want: resolvedGameSecurity{
				RunAsUser:            securityIDResolved{Source: gameplanev1alpha1.SecuritySourceImageDefault},
				RunAsGroup:           securityIDResolved{Source: gameplanev1alpha1.SecuritySourceImageDefault},
				FSGroup:              securityIDResolved{Source: gameplanev1alpha1.SecuritySourceImageDefault},
				VolumeOwnershipCheck: securityCheckResolved{Value: gameplanev1alpha1.VolumeOwnershipCheckOnlyWhenWrong, Source: gameplanev1alpha1.SecuritySourceBuiltInDefault},
			},
		},
		{
			name: "template with no security block gives image default and built-in check",
			tmpl: secTemplate(nil),
			want: resolvedGameSecurity{
				RunAsUser:            securityIDResolved{Source: gameplanev1alpha1.SecuritySourceImageDefault},
				RunAsGroup:           securityIDResolved{Source: gameplanev1alpha1.SecuritySourceImageDefault},
				FSGroup:              securityIDResolved{Source: gameplanev1alpha1.SecuritySourceImageDefault},
				VolumeOwnershipCheck: securityCheckResolved{Value: gameplanev1alpha1.VolumeOwnershipCheckOnlyWhenWrong, Source: gameplanev1alpha1.SecuritySourceBuiltInDefault},
			},
		},
		{
			name: "module default only",
			tmpl: secTemplate(&gameplanev1alpha1.GameSecuritySpec{RunAsUser: secInt(25000), FSGroup: secInt(25000), VolumeOwnershipCheck: gameplanev1alpha1.VolumeOwnershipCheckEveryStart}),
			gs:   secServer(nil),
			want: resolvedGameSecurity{
				RunAsUser:            securityIDResolved{Value: secInt(25000), Source: gameplanev1alpha1.SecuritySourceModuleDefault},
				RunAsGroup:           securityIDResolved{Source: gameplanev1alpha1.SecuritySourceImageDefault},
				FSGroup:              securityIDResolved{Value: secInt(25000), Source: gameplanev1alpha1.SecuritySourceModuleDefault},
				VolumeOwnershipCheck: securityCheckResolved{Value: gameplanev1alpha1.VolumeOwnershipCheckEveryStart, Source: gameplanev1alpha1.SecuritySourceModuleDefault},
			},
		},
		{
			name: "override value beats module default",
			tmpl: secTemplate(&gameplanev1alpha1.GameSecuritySpec{RunAsUser: secInt(25000)}),
			gs: secServer(&gameplanev1alpha1.GameServerSecuritySpec{
				RunAsUser: &gameplanev1alpha1.SecurityIDOverride{Value: secInt(2000)},
			}),
			want: resolvedGameSecurity{
				RunAsUser:            securityIDResolved{Value: secInt(2000), Source: gameplanev1alpha1.SecuritySourceOverride},
				RunAsGroup:           securityIDResolved{Source: gameplanev1alpha1.SecuritySourceImageDefault},
				FSGroup:              securityIDResolved{Source: gameplanev1alpha1.SecuritySourceImageDefault},
				VolumeOwnershipCheck: securityCheckResolved{Value: gameplanev1alpha1.VolumeOwnershipCheckOnlyWhenWrong, Source: gameplanev1alpha1.SecuritySourceBuiltInDefault},
			},
		},
		{
			name: "override useImageDefault beats module default with no value",
			tmpl: secTemplate(&gameplanev1alpha1.GameSecuritySpec{RunAsGroup: secInt(25000)}),
			gs: secServer(&gameplanev1alpha1.GameServerSecuritySpec{
				RunAsGroup: &gameplanev1alpha1.SecurityIDOverride{UseImageDefault: secBool(true)},
			}),
			want: resolvedGameSecurity{
				RunAsUser:            securityIDResolved{Source: gameplanev1alpha1.SecuritySourceImageDefault},
				RunAsGroup:           securityIDResolved{Source: gameplanev1alpha1.SecuritySourceImageDefaultOverride},
				FSGroup:              securityIDResolved{Source: gameplanev1alpha1.SecuritySourceImageDefault},
				VolumeOwnershipCheck: securityCheckResolved{Value: gameplanev1alpha1.VolumeOwnershipCheckOnlyWhenWrong, Source: gameplanev1alpha1.SecuritySourceBuiltInDefault},
			},
		},
		{
			name: "override check beats module check",
			tmpl: secTemplate(&gameplanev1alpha1.GameSecuritySpec{VolumeOwnershipCheck: gameplanev1alpha1.VolumeOwnershipCheckEveryStart}),
			gs: secServer(&gameplanev1alpha1.GameServerSecuritySpec{
				VolumeOwnershipCheck: gameplanev1alpha1.VolumeOwnershipCheckOnlyWhenWrong,
			}),
			want: resolvedGameSecurity{
				RunAsUser:            securityIDResolved{Source: gameplanev1alpha1.SecuritySourceImageDefault},
				RunAsGroup:           securityIDResolved{Source: gameplanev1alpha1.SecuritySourceImageDefault},
				FSGroup:              securityIDResolved{Source: gameplanev1alpha1.SecuritySourceImageDefault},
				VolumeOwnershipCheck: securityCheckResolved{Value: gameplanev1alpha1.VolumeOwnershipCheckOnlyWhenWrong, Source: gameplanev1alpha1.SecuritySourceOverride},
			},
		},
		{
			name: "override value with no template resolves as override",
			gs: secServer(&gameplanev1alpha1.GameServerSecuritySpec{
				FSGroup: &gameplanev1alpha1.SecurityIDOverride{Value: secInt(3000)},
			}),
			want: resolvedGameSecurity{
				RunAsUser:            securityIDResolved{Source: gameplanev1alpha1.SecuritySourceImageDefault},
				RunAsGroup:           securityIDResolved{Source: gameplanev1alpha1.SecuritySourceImageDefault},
				FSGroup:              securityIDResolved{Value: secInt(3000), Source: gameplanev1alpha1.SecuritySourceOverride},
				VolumeOwnershipCheck: securityCheckResolved{Value: gameplanev1alpha1.VolumeOwnershipCheckOnlyWhenWrong, Source: gameplanev1alpha1.SecuritySourceBuiltInDefault},
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := resolveGameSecurity(tc.tmpl, tc.gs)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("resolveGameSecurity() = %+v, want %+v", got, tc.want)
			}
		})
	}
}

// TestFSGroupChangePolicyMapping covers R8: EveryStart is Always, OnlyWhenWrong
// is OnRootMismatch, and no policy is written when no filesystem group applies.
func TestFSGroupChangePolicyMapping(t *testing.T) {
	withFS := func(check gameplanev1alpha1.VolumeOwnershipCheck) resolvedGameSecurity {
		return resolvedGameSecurity{
			FSGroup:              securityIDResolved{Value: secInt(25000), Source: gameplanev1alpha1.SecuritySourceModuleDefault},
			VolumeOwnershipCheck: securityCheckResolved{Value: check, Source: gameplanev1alpha1.SecuritySourceModuleDefault},
		}
	}
	withoutFS := func(check gameplanev1alpha1.VolumeOwnershipCheck) resolvedGameSecurity {
		return resolvedGameSecurity{
			FSGroup:              securityIDResolved{Source: gameplanev1alpha1.SecuritySourceImageDefault},
			VolumeOwnershipCheck: securityCheckResolved{Value: check, Source: gameplanev1alpha1.SecuritySourceModuleDefault},
		}
	}
	always := corev1.FSGroupChangeAlways
	onRoot := corev1.FSGroupChangeOnRootMismatch

	cases := []struct {
		name string
		in   resolvedGameSecurity
		want *corev1.PodFSGroupChangePolicy
	}{
		{"EveryStart with fsGroup is Always", withFS(gameplanev1alpha1.VolumeOwnershipCheckEveryStart), &always},
		{"OnlyWhenWrong with fsGroup is OnRootMismatch", withFS(gameplanev1alpha1.VolumeOwnershipCheckOnlyWhenWrong), &onRoot},
		{"EveryStart without fsGroup is unset", withoutFS(gameplanev1alpha1.VolumeOwnershipCheckEveryStart), nil},
		{"OnlyWhenWrong without fsGroup is unset", withoutFS(gameplanev1alpha1.VolumeOwnershipCheckOnlyWhenWrong), nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.in.fsGroupChangePolicy()
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("fsGroupChangePolicy() = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestSecurityAnnotationRoundTrip proves the annotation codec keeps values and
// sources, and that a malformed annotation is an error.
func TestSecurityAnnotationRoundTrip(t *testing.T) {
	tmpl := secTemplate(&gameplanev1alpha1.GameSecuritySpec{
		RunAsUser:            secInt(25000),
		RunAsGroup:           secInt(25000),
		FSGroup:              secInt(25000),
		VolumeOwnershipCheck: gameplanev1alpha1.VolumeOwnershipCheckEveryStart,
	})
	gs := secServer(&gameplanev1alpha1.GameServerSecuritySpec{
		RunAsGroup: &gameplanev1alpha1.SecurityIDOverride{UseImageDefault: secBool(true)},
	})
	want := resolveGameSecurity(tmpl, gs).settings()

	raw, err := encodeSecurityAnnotation(want)
	if err != nil {
		t.Fatalf("encodeSecurityAnnotation: %v", err)
	}
	got, err := decodeSecurityAnnotation(raw)
	if err != nil {
		t.Fatalf("decodeSecurityAnnotation: %v", err)
	}
	if !reflect.DeepEqual(*got, want) {
		t.Errorf("round trip = %+v, want %+v", *got, want)
	}
	if got.RunAsGroup.Source != gameplanev1alpha1.SecuritySourceImageDefaultOverride {
		t.Errorf("RunAsGroup source = %q, want ImageDefaultOverride", got.RunAsGroup.Source)
	}

	if _, err := decodeSecurityAnnotation("{not json"); err == nil {
		t.Error("decodeSecurityAnnotation on malformed input returned nil error")
	}
}

// TestSecurityPending covers the pending rule: nothing applied is never pending,
// a source-only change is not pending, and a value change is.
func TestSecurityPending(t *testing.T) {
	settings := func(user, group, fs *int64, check gameplanev1alpha1.VolumeOwnershipCheck) gameplanev1alpha1.SecuritySettings {
		src := func(v *int64) gameplanev1alpha1.SecuritySource {
			if v == nil {
				return gameplanev1alpha1.SecuritySourceImageDefault
			}
			return gameplanev1alpha1.SecuritySourceModuleDefault
		}
		return gameplanev1alpha1.SecuritySettings{
			RunAsUser:            gameplanev1alpha1.SecurityIDStatus{Value: user, Source: src(user)},
			RunAsGroup:           gameplanev1alpha1.SecurityIDStatus{Value: group, Source: src(group)},
			FSGroup:              gameplanev1alpha1.SecurityIDStatus{Value: fs, Source: src(fs)},
			VolumeOwnershipCheck: gameplanev1alpha1.SecurityCheckStatus{Value: check, Source: gameplanev1alpha1.SecuritySourceBuiltInDefault},
		}
	}
	every := gameplanev1alpha1.VolumeOwnershipCheckEveryStart
	onlyWrong := gameplanev1alpha1.VolumeOwnershipCheckOnlyWhenWrong
	base := settings(secInt(25000), secInt(25000), secInt(25000), every)

	t.Run("never started is not pending", func(t *testing.T) {
		if securityPending(base, nil) {
			t.Error("securityPending(applied=nil) = true, want false")
		}
	})
	t.Run("identical values are not pending", func(t *testing.T) {
		if securityPending(base, &base) {
			t.Error("securityPending(identical) = true, want false")
		}
	})
	t.Run("source-only change is not pending", func(t *testing.T) {
		applied := base
		applied.RunAsUser.Source = gameplanev1alpha1.SecuritySourceOverride
		if securityPending(base, &applied) {
			t.Error("securityPending(source-only change) = true, want false")
		}
	})
	t.Run("runAsUser value change is pending", func(t *testing.T) {
		desired := settings(secInt(2000), secInt(25000), secInt(25000), every)
		if !securityPending(desired, &base) {
			t.Error("securityPending(runAsUser changed) = false, want true")
		}
	})
	t.Run("fsGroup value change is pending", func(t *testing.T) {
		desired := settings(secInt(25000), secInt(25000), secInt(3000), every)
		if !securityPending(desired, &base) {
			t.Error("securityPending(fsGroup changed) = false, want true")
		}
	})
	t.Run("fsGroup removed is pending", func(t *testing.T) {
		desired := settings(secInt(25000), secInt(25000), nil, every)
		if !securityPending(desired, &base) {
			t.Error("securityPending(fsGroup removed) = false, want true")
		}
	})
	t.Run("ownership check change with fsGroup is pending", func(t *testing.T) {
		desired := settings(secInt(25000), secInt(25000), secInt(25000), onlyWrong)
		if !securityPending(desired, &base) {
			t.Error("securityPending(check changed, fsGroup set) = false, want true")
		}
	})
	t.Run("ownership check change without fsGroup on either side is not pending", func(t *testing.T) {
		desiredNoFS := settings(secInt(25000), secInt(25000), nil, onlyWrong)
		appliedNoFS := settings(secInt(25000), secInt(25000), nil, every)
		if securityPending(desiredNoFS, &appliedNoFS) {
			t.Error("securityPending(check changed, no fsGroup) = true, want false")
		}
	})
}

// TestPodVolumesSetUp covers the volume-setup signal used by the applied reader.
func TestPodVolumesSetUp(t *testing.T) {
	started := true
	cases := []struct {
		name string
		pod  corev1.Pod
		want bool
	}{
		{"empty pod has not passed volume setup", corev1.Pod{}, false},
		{
			"ready to start containers is set up",
			corev1.Pod{Status: corev1.PodStatus{Conditions: []corev1.PodCondition{
				{Type: corev1.PodReadyToStartContainers, Status: corev1.ConditionTrue},
			}}},
			true,
		},
		{
			"ready to start containers false is not set up",
			corev1.Pod{Status: corev1.PodStatus{Conditions: []corev1.PodCondition{
				{Type: corev1.PodReadyToStartContainers, Status: corev1.ConditionFalse},
			}}},
			false,
		},
		{
			"started config-init is set up",
			corev1.Pod{Status: corev1.PodStatus{InitContainerStatuses: []corev1.ContainerStatus{
				{Name: securityConfigInitContainerName, Started: &started},
			}}},
			true,
		},
		{
			"config-init waiting is not set up",
			corev1.Pod{Status: corev1.PodStatus{InitContainerStatuses: []corev1.ContainerStatus{
				{Name: securityConfigInitContainerName, State: corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{}}},
			}}},
			false,
		},
		{
			"other init container running is not set up",
			corev1.Pod{Status: corev1.PodStatus{InitContainerStatuses: []corev1.ContainerStatus{
				{Name: "sentinel", State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{}}},
			}}},
			false,
		},
		{
			"running game container is set up",
			corev1.Pod{Status: corev1.PodStatus{ContainerStatuses: []corev1.ContainerStatus{
				{Name: gameContainerName, State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{}}},
			}}},
			true,
		},
		{
			"terminated game container is set up",
			corev1.Pod{Status: corev1.PodStatus{ContainerStatuses: []corev1.ContainerStatus{
				{Name: gameContainerName, State: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{}}},
			}}},
			true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := podVolumesSetUp(&tc.pod); got != tc.want {
				t.Errorf("podVolumesSetUp() = %v, want %v", got, tc.want)
			}
		})
	}
}

// secOwnedStatefulSet builds a StatefulSet controlled by gs, as reconcile does.
func secOwnedStatefulSet(gs *gameplanev1alpha1.GameServer, uid types.UID) *appsv1.StatefulSet {
	return &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      gs.Name,
			Namespace: gs.Namespace,
			UID:       uid,
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

// secTemplatePod builds the template pod of a StatefulSet, controlled by it.
func secTemplatePod(ss *appsv1.StatefulSet, annotations map[string]string) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:        ss.Name + "-0",
			Namespace:   ss.Namespace,
			Annotations: annotations,
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion: "apps/v1",
				Kind:       "StatefulSet",
				Name:       ss.Name,
				UID:        ss.UID,
				Controller: ownerBoolPtr(true),
			}},
		},
		Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: gameContainerName, Image: "test/game:fixture"}}},
	}
}

// TestReadAppliedSecurity covers the applied reader (R6): never started, the
// observed annotation, pre-feature StatefulSets read from the pod or template,
// and feature StatefulSets that report only after volume setup.
func TestReadAppliedSecurity(t *testing.T) {
	s := testScheme(t)
	ctx := context.Background()
	gs := secServer(nil)

	t.Run("never started returns nil", func(t *testing.T) {
		cl := fake.NewClientBuilder().WithScheme(s).Build()
		got, err := readAppliedSecurity(ctx, cl, gs)
		if err != nil || got != nil {
			t.Errorf("readAppliedSecurity() = %+v, %v; want nil, nil", got, err)
		}
	})

	t.Run("observed annotation wins", func(t *testing.T) {
		observed := gameplanev1alpha1.SecuritySettings{
			RunAsUser:            gameplanev1alpha1.SecurityIDStatus{Value: secInt(2000), Source: gameplanev1alpha1.SecuritySourceOverride},
			RunAsGroup:           gameplanev1alpha1.SecurityIDStatus{Source: gameplanev1alpha1.SecuritySourceImageDefault},
			FSGroup:              gameplanev1alpha1.SecurityIDStatus{Value: secInt(25000), Source: gameplanev1alpha1.SecuritySourceModuleDefault},
			VolumeOwnershipCheck: gameplanev1alpha1.SecurityCheckStatus{Value: gameplanev1alpha1.VolumeOwnershipCheckOnlyWhenWrong, Source: gameplanev1alpha1.SecuritySourceBuiltInDefault},
		}
		raw, err := encodeSecurityAnnotation(observed)
		if err != nil {
			t.Fatalf("encode: %v", err)
		}
		ss := secOwnedStatefulSet(gs, "alpha-ss-uid")
		ss.Annotations = map[string]string{securityAppliedObservedAnnotation: raw}
		cl := fake.NewClientBuilder().WithScheme(s).WithObjects(ss).Build()
		got, err := readAppliedSecurity(ctx, cl, gs)
		if err != nil || got == nil {
			t.Fatalf("readAppliedSecurity() = %+v, %v; want observed value", got, err)
		}
		if !reflect.DeepEqual(*got, observed) {
			t.Errorf("readAppliedSecurity() = %+v, want %+v", *got, observed)
		}
	})

	t.Run("pre-feature statefulset without pod reads its template", func(t *testing.T) {
		ss := secOwnedStatefulSet(gs, "alpha-ss-uid")
		ss.Spec.Template.Spec = corev1.PodSpec{
			Containers: []corev1.Container{{
				Name:            gameContainerName,
				SecurityContext: &corev1.SecurityContext{RunAsUser: secInt(25000), RunAsGroup: secInt(25000)},
			}},
			SecurityContext: &corev1.PodSecurityContext{FSGroup: secInt(25000)},
		}
		cl := fake.NewClientBuilder().WithScheme(s).WithObjects(ss).Build()
		got, err := readAppliedSecurity(ctx, cl, gs)
		if err != nil || got == nil {
			t.Fatalf("readAppliedSecurity() = %+v, %v; want template values", got, err)
		}
		want := gameplanev1alpha1.SecuritySettings{
			RunAsUser:            gameplanev1alpha1.SecurityIDStatus{Value: secInt(25000), Source: gameplanev1alpha1.SecuritySourceModuleDefault},
			RunAsGroup:           gameplanev1alpha1.SecurityIDStatus{Value: secInt(25000), Source: gameplanev1alpha1.SecuritySourceModuleDefault},
			FSGroup:              gameplanev1alpha1.SecurityIDStatus{Value: secInt(25000), Source: gameplanev1alpha1.SecuritySourceModuleDefault},
			VolumeOwnershipCheck: gameplanev1alpha1.SecurityCheckStatus{Value: gameplanev1alpha1.VolumeOwnershipCheckEveryStart, Source: gameplanev1alpha1.SecuritySourceBuiltInDefault},
		}
		if !reflect.DeepEqual(*got, want) {
			t.Errorf("readAppliedSecurity() = %+v, want %+v", *got, want)
		}
	})

	t.Run("pre-feature statefulset with empty template reads image defaults", func(t *testing.T) {
		ss := secOwnedStatefulSet(gs, "alpha-ss-uid")
		cl := fake.NewClientBuilder().WithScheme(s).WithObjects(ss).Build()
		got, err := readAppliedSecurity(ctx, cl, gs)
		if err != nil || got == nil {
			t.Fatalf("readAppliedSecurity() = %+v, %v; want image defaults", got, err)
		}
		if got.FSGroup.Value != nil || got.FSGroup.Source != gameplanev1alpha1.SecuritySourceImageDefault {
			t.Errorf("FSGroup = %+v, want image default", got.FSGroup)
		}
		if got.VolumeOwnershipCheck.Value != gameplanev1alpha1.VolumeOwnershipCheckEveryStart {
			t.Errorf("check = %q, want EveryStart", got.VolumeOwnershipCheck.Value)
		}
	})

	t.Run("pre-feature statefulset with pod reads the running pod", func(t *testing.T) {
		ss := secOwnedStatefulSet(gs, "alpha-ss-uid")
		ss.Spec.Template.Spec = corev1.PodSpec{
			SecurityContext: &corev1.PodSecurityContext{FSGroup: secInt(25000)},
		}
		pod := secTemplatePod(ss, nil)
		pod.Spec.SecurityContext = &corev1.PodSecurityContext{
			FSGroup:             secInt(30000),
			FSGroupChangePolicy: ptrTo(corev1.FSGroupChangeOnRootMismatch),
		}
		cl := fake.NewClientBuilder().WithScheme(s).WithObjects(ss, pod).Build()
		got, err := readAppliedSecurity(ctx, cl, gs)
		if err != nil || got == nil {
			t.Fatalf("readAppliedSecurity() = %+v, %v; want pod values", got, err)
		}
		if got.FSGroup.Value == nil || *got.FSGroup.Value != 30000 {
			t.Errorf("FSGroup = %+v, want the pod value 30000", got.FSGroup)
		}
		if got.VolumeOwnershipCheck.Value != gameplanev1alpha1.VolumeOwnershipCheckOnlyWhenWrong {
			t.Errorf("check = %q, want OnlyWhenWrong from the pod policy", got.VolumeOwnershipCheck.Value)
		}
	})

	t.Run("feature statefulset with no pod reports nothing", func(t *testing.T) {
		ss := secOwnedStatefulSet(gs, "alpha-ss-uid")
		ss.Annotations = nil
		ss.Spec.Template.Annotations = map[string]string{securityAppliedAnnotation: `{}`}
		cl := fake.NewClientBuilder().WithScheme(s).WithObjects(ss).Build()
		got, err := readAppliedSecurity(ctx, cl, gs)
		if err != nil || got != nil {
			t.Errorf("readAppliedSecurity() = %+v, %v; want nil, nil", got, err)
		}
	})

	t.Run("feature statefulset with pod that has not passed volume setup reports nothing", func(t *testing.T) {
		ss := secOwnedStatefulSet(gs, "alpha-ss-uid")
		ss.Spec.Template.Annotations = map[string]string{securityAppliedAnnotation: `{}`}
		pod := secTemplatePod(ss, map[string]string{securityAppliedAnnotation: `{}`})
		cl := fake.NewClientBuilder().WithScheme(s).WithObjects(ss, pod).Build()
		got, err := readAppliedSecurity(ctx, cl, gs)
		if err != nil || got != nil {
			t.Errorf("readAppliedSecurity() = %+v, %v; want nil, nil", got, err)
		}
	})

	t.Run("feature statefulset with a set-up pod reports the pod annotation", func(t *testing.T) {
		want := gameplanev1alpha1.SecuritySettings{
			RunAsUser:            gameplanev1alpha1.SecurityIDStatus{Value: secInt(2000), Source: gameplanev1alpha1.SecuritySourceOverride},
			RunAsGroup:           gameplanev1alpha1.SecurityIDStatus{Source: gameplanev1alpha1.SecuritySourceImageDefault},
			FSGroup:              gameplanev1alpha1.SecurityIDStatus{Value: secInt(25000), Source: gameplanev1alpha1.SecuritySourceModuleDefault},
			VolumeOwnershipCheck: gameplanev1alpha1.SecurityCheckStatus{Value: gameplanev1alpha1.VolumeOwnershipCheckEveryStart, Source: gameplanev1alpha1.SecuritySourceModuleDefault},
		}
		raw, err := encodeSecurityAnnotation(want)
		if err != nil {
			t.Fatalf("encode: %v", err)
		}
		ss := secOwnedStatefulSet(gs, "alpha-ss-uid")
		ss.Spec.Template.Annotations = map[string]string{securityAppliedAnnotation: raw}
		pod := secTemplatePod(ss, map[string]string{securityAppliedAnnotation: raw})
		pod.Status.Conditions = []corev1.PodCondition{{Type: corev1.PodReadyToStartContainers, Status: corev1.ConditionTrue}}
		cl := fake.NewClientBuilder().WithScheme(s).WithObjects(ss, pod).Build()
		got, err := readAppliedSecurity(ctx, cl, gs)
		if err != nil || got == nil {
			t.Fatalf("readAppliedSecurity() = %+v, %v; want pod annotation", got, err)
		}
		if !reflect.DeepEqual(*got, want) {
			t.Errorf("readAppliedSecurity() = %+v, want %+v", *got, want)
		}
	})
}

// TestSetObservedSecurityAnnotation covers the observed annotation writer: an
// empty value is ignored, and an unchanged value reports no change.
func TestSetObservedSecurityAnnotation(t *testing.T) {
	ss := &appsv1.StatefulSet{}
	if setObservedSecurityAnnotation(ss, "") {
		t.Error("empty value reported a change")
	}
	if !setObservedSecurityAnnotation(ss, `{"a":1}`) {
		t.Error("first write reported no change")
	}
	if got := ss.Annotations[securityAppliedObservedAnnotation]; got != `{"a":1}` {
		t.Errorf("observed annotation = %q", got)
	}
	if setObservedSecurityAnnotation(ss, `{"a":1}`) {
		t.Error("unchanged value reported a change")
	}
}

// TestRestrictedIdentityUsesEffectiveValues is the OD-5 table: restricted
// validation reads the effective values of one server, so a positive override
// passes where a root module default would fail, and a root or "use image
// default" value is refused.
func TestRestrictedIdentityUsesEffectiveValues(t *testing.T) {
	cases := []struct {
		name    string
		tmpl    *gameplanev1alpha1.GameTemplate
		gs      *gameplanev1alpha1.GameServer
		wantErr bool
	}{
		{
			name: "module identity is valid",
			tmpl: secTemplate(&gameplanev1alpha1.GameSecuritySpec{RunAsUser: secInt(25000), FSGroup: secInt(25000)}),
			gs:   secServer(nil),
		},
		{
			name: "root module default without override is refused",
			tmpl: secTemplate(&gameplanev1alpha1.GameSecuritySpec{RunAsUser: secInt(0), FSGroup: secInt(25000)}),
			gs:   secServer(nil),

			wantErr: true,
		},
		{
			name: "positive override of a root module default passes",
			tmpl: secTemplate(&gameplanev1alpha1.GameSecuritySpec{RunAsUser: secInt(0), FSGroup: secInt(25000)}),
			gs: secServer(&gameplanev1alpha1.GameServerSecuritySpec{
				RunAsUser: &gameplanev1alpha1.SecurityIDOverride{Value: secInt(2000)},
			}),
		},
		{
			name: "root override value is refused",
			tmpl: secTemplate(&gameplanev1alpha1.GameSecuritySpec{RunAsUser: secInt(25000), FSGroup: secInt(25000)}),
			gs: secServer(&gameplanev1alpha1.GameServerSecuritySpec{
				RunAsUser: &gameplanev1alpha1.SecurityIDOverride{Value: secInt(0)},
			}),
			wantErr: true,
		},
		{
			name: "use image default for run-as user is refused",
			tmpl: secTemplate(&gameplanev1alpha1.GameSecuritySpec{RunAsUser: secInt(25000), FSGroup: secInt(25000)}),
			gs: secServer(&gameplanev1alpha1.GameServerSecuritySpec{
				RunAsUser: &gameplanev1alpha1.SecurityIDOverride{UseImageDefault: secBool(true)},
			}),
			wantErr: true,
		},
		{
			name: "use image default for fsGroup is refused",
			tmpl: secTemplate(&gameplanev1alpha1.GameSecuritySpec{RunAsUser: secInt(25000), FSGroup: secInt(25000)}),
			gs: secServer(&gameplanev1alpha1.GameServerSecuritySpec{
				FSGroup: &gameplanev1alpha1.SecurityIDOverride{UseImageDefault: secBool(true)},
			}),
			wantErr: true,
		},
		{
			name: "zero runAsGroup override is refused",
			tmpl: secTemplate(&gameplanev1alpha1.GameSecuritySpec{RunAsUser: secInt(25000), RunAsGroup: secInt(25000), FSGroup: secInt(25000)}),
			gs: secServer(&gameplanev1alpha1.GameServerSecuritySpec{
				RunAsGroup: &gameplanev1alpha1.SecurityIDOverride{Value: secInt(0)},
			}),
			wantErr: true,
		},
		{
			name: "absent runAsGroup is allowed",
			tmpl: secTemplate(&gameplanev1alpha1.GameSecuritySpec{RunAsUser: secInt(25000), FSGroup: secInt(25000)}),
			gs:   secServer(nil),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateRestrictedSecurity(resolveGameSecurity(tc.tmpl, tc.gs))
			if (err != nil) != tc.wantErr {
				t.Errorf("validateRestrictedSecurity() error = %v, wantErr %v", err, tc.wantErr)
			}
		})
	}

	t.Run("template-only validation still refuses a root default", func(t *testing.T) {
		if err := validateRestrictedTemplate(secTemplate(&gameplanev1alpha1.GameSecuritySpec{RunAsUser: secInt(0), FSGroup: secInt(25000)})); err == nil {
			t.Error("validateRestrictedTemplate(root default) = nil, want error")
		}
	})
}

// TestRestrictedGameContainerUsesOverride proves restricted mode takes the game
// container identity from the effective values (OD-5), and that a non-restricted
// build leaves the game container alone.
func TestRestrictedGameContainerUsesOverride(t *testing.T) {
	tmpl := secTemplate(&gameplanev1alpha1.GameSecuritySpec{RunAsUser: secInt(25000), RunAsGroup: secInt(25000), FSGroup: secInt(25000)})
	gs := secServer(&gameplanev1alpha1.GameServerSecuritySpec{
		RunAsUser: &gameplanev1alpha1.SecurityIDOverride{Value: secInt(2000)},
	})
	sec := resolveGameSecurity(tmpl, gs)

	newPod := func() *corev1.PodSpec {
		return &corev1.PodSpec{
			Containers: []corev1.Container{
				{Name: gameContainerName, Image: "test/game:fixture"},
				{Name: "agent", Image: "test/agent:fixture"},
			},
			SecurityContext: &corev1.PodSecurityContext{},
		}
	}

	t.Run("restricted game container takes the override", func(t *testing.T) {
		pod := newPod()
		applyWorkloadSecurityResolved(pod, sec, true, 3600)
		sc := podContainerNamed(pod, gameContainerName).SecurityContext
		if sc == nil || sc.RunAsUser == nil || *sc.RunAsUser != 2000 {
			t.Fatalf("game RunAsUser = %+v, want 2000", sc)
		}
		if sc.RunAsGroup == nil || *sc.RunAsGroup != 25000 {
			t.Errorf("game RunAsGroup = %v, want 25000", sc.RunAsGroup)
		}
		if sc.RunAsNonRoot == nil || !*sc.RunAsNonRoot {
			t.Error("game RunAsNonRoot not set in restricted mode")
		}
	})

	t.Run("non-restricted leaves the game container unchanged", func(t *testing.T) {
		pod := newPod()
		applyWorkloadSecurityResolved(pod, sec, false, 3600)
		if sc := podContainerNamed(pod, gameContainerName).SecurityContext; sc != nil {
			t.Errorf("game SecurityContext = %+v, want nil when not restricted", sc)
		}
	})
}
