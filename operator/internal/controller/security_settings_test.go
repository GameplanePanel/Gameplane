package controller

import (
	"context"
	"sort"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	gameplanev1alpha1 "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
)

// secSettingsInt64 returns a pointer to v. Shared by the spec 030 settings
// tests in this package, including the envtest files.
func secSettingsInt64(v int64) *int64 {
	return &v
}

// secSettingsBool returns a pointer to v for the security settings tests.
func secSettingsBool(v bool) *bool {
	return &v
}

func TestSecuritySettingsAllowRoot_OnlyExactTrue(t *testing.T) {
	cases := []struct {
		name string
		cm   *corev1.ConfigMap
		want bool
	}{
		{name: "nil configmap", cm: nil, want: false},
		{name: "missing key", cm: &corev1.ConfigMap{}, want: false},
		{name: "empty value", cm: &corev1.ConfigMap{Data: map[string]string{"allowRootOverrides": ""}}, want: false},
		{name: "false", cm: &corev1.ConfigMap{Data: map[string]string{"allowRootOverrides": "false"}}, want: false},
		{name: "capitalised True", cm: &corev1.ConfigMap{Data: map[string]string{"allowRootOverrides": "True"}}, want: false},
		{name: "one", cm: &corev1.ConfigMap{Data: map[string]string{"allowRootOverrides": "1"}}, want: false},
		{name: "padded true", cm: &corev1.ConfigMap{Data: map[string]string{"allowRootOverrides": " true"}}, want: false},
		{name: "garbage", cm: &corev1.ConfigMap{Data: map[string]string{"allowRootOverrides": "yes please"}}, want: false},
		{name: "exact true", cm: &corev1.ConfigMap{Data: map[string]string{"allowRootOverrides": "true"}}, want: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := securitySettingsAllowRoot(tc.cm); got != tc.want {
				t.Fatalf("securitySettingsAllowRoot = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestRootOverrideSettings_RuleTable(t *testing.T) {
	cases := []struct {
		name string
		sec  *gameplanev1alpha1.GameServerSecuritySpec
		want []string
	}{
		{name: "no security block", sec: nil, want: nil},
		{name: "empty security block", sec: &gameplanev1alpha1.GameServerSecuritySpec{}, want: nil},
		{
			name: "runAsUser zero is root",
			sec:  &gameplanev1alpha1.GameServerSecuritySpec{RunAsUser: &gameplanev1alpha1.SecurityIDOverride{Value: secSettingsInt64(0)}},
			want: []string{"runAsUser"},
		},
		{
			name: "runAsGroup zero is root",
			sec:  &gameplanev1alpha1.GameServerSecuritySpec{RunAsGroup: &gameplanev1alpha1.SecurityIDOverride{Value: secSettingsInt64(0)}},
			want: []string{"runAsGroup"},
		},
		{
			name: "runAsUser useImageDefault may be root",
			sec:  &gameplanev1alpha1.GameServerSecuritySpec{RunAsUser: &gameplanev1alpha1.SecurityIDOverride{UseImageDefault: secSettingsBool(true)}},
			want: []string{"runAsUser"},
		},
		{
			name: "runAsGroup useImageDefault may be root",
			sec:  &gameplanev1alpha1.GameServerSecuritySpec{RunAsGroup: &gameplanev1alpha1.SecurityIDOverride{UseImageDefault: secSettingsBool(true)}},
			want: []string{"runAsGroup"},
		},
		{
			name: "fsGroup zero is root",
			sec:  &gameplanev1alpha1.GameServerSecuritySpec{FSGroup: &gameplanev1alpha1.SecurityIDOverride{Value: secSettingsInt64(0)}},
			want: []string{"fsGroup"},
		},
		{
			name: "fsGroup useImageDefault is not a root rule",
			sec:  &gameplanev1alpha1.GameServerSecuritySpec{FSGroup: &gameplanev1alpha1.SecurityIDOverride{UseImageDefault: secSettingsBool(true)}},
			want: nil,
		},
		{
			name: "non-root values are not named",
			sec: &gameplanev1alpha1.GameServerSecuritySpec{
				RunAsUser:  &gameplanev1alpha1.SecurityIDOverride{Value: secSettingsInt64(1000)},
				RunAsGroup: &gameplanev1alpha1.SecurityIDOverride{Value: secSettingsInt64(1000)},
				FSGroup:    &gameplanev1alpha1.SecurityIDOverride{Value: secSettingsInt64(2000)},
			},
			want: nil,
		},
		{
			name: "useImageDefault false is not an override",
			sec:  &gameplanev1alpha1.GameServerSecuritySpec{RunAsUser: &gameplanev1alpha1.SecurityIDOverride{UseImageDefault: secSettingsBool(false)}},
			want: nil,
		},
		{
			name: "all three in spec order",
			sec: &gameplanev1alpha1.GameServerSecuritySpec{
				FSGroup:    &gameplanev1alpha1.SecurityIDOverride{Value: secSettingsInt64(0)},
				RunAsGroup: &gameplanev1alpha1.SecurityIDOverride{Value: secSettingsInt64(0)},
				RunAsUser:  &gameplanev1alpha1.SecurityIDOverride{Value: secSettingsInt64(0)},
			},
			want: []string{"runAsUser", "runAsGroup", "fsGroup"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gs := &gameplanev1alpha1.GameServer{Spec: gameplanev1alpha1.GameServerSpec{Security: tc.sec}}
			got := rootOverrideSettings(gs)
			if len(got) != len(tc.want) {
				t.Fatalf("rootOverrideSettings = %v, want %v", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("rootOverrideSettings = %v, want %v", got, tc.want)
				}
			}
			if hasRootOverride(gs) != (len(tc.want) > 0) {
				t.Fatalf("hasRootOverride = %v, want %v", hasRootOverride(gs), len(tc.want) > 0)
			}
		})
	}
	if rootOverrideSettings(nil) != nil || hasRootOverride(nil) {
		t.Fatal("nil server must not hold a root override")
	}
}

func TestRootOverridesAllowed_ConfigMapReader(t *testing.T) {
	const cpNS = "gameplane-cp"
	cmWith := func(ns string, data map[string]string) *corev1.ConfigMap {
		return &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{Name: securitySettingsConfigMapName, Namespace: ns},
			Data:       data,
		}
	}
	cases := []struct {
		name string
		cpNS string
		objs []*corev1.ConfigMap
		want bool
	}{
		{name: "no control-plane namespace is off even with a true ConfigMap", cpNS: "", objs: []*corev1.ConfigMap{cmWith(cpNS, map[string]string{"allowRootOverrides": "true"})}, want: false},
		{name: "absent ConfigMap is off", cpNS: cpNS, objs: nil, want: false},
		{name: "missing key is off", cpNS: cpNS, objs: []*corev1.ConfigMap{cmWith(cpNS, map[string]string{"other": "true"})}, want: false},
		{name: "false is off", cpNS: cpNS, objs: []*corev1.ConfigMap{cmWith(cpNS, map[string]string{"allowRootOverrides": "false"})}, want: false},
		{name: "garbage is off", cpNS: cpNS, objs: []*corev1.ConfigMap{cmWith(cpNS, map[string]string{"allowRootOverrides": "maybe"})}, want: false},
		{name: "true is on", cpNS: cpNS, objs: []*corev1.ConfigMap{cmWith(cpNS, map[string]string{"allowRootOverrides": "true"})}, want: true},
		{name: "true in another namespace is ignored", cpNS: cpNS, objs: []*corev1.ConfigMap{cmWith("elsewhere", map[string]string{"allowRootOverrides": "true"})}, want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b := fake.NewClientBuilder().WithScheme(testScheme(t))
			for _, cm := range tc.objs {
				b = b.WithObjects(cm)
			}
			r := &GameServerReconciler{Client: b.Build(), ControlPlaneNamespace: tc.cpNS}
			if got := r.rootOverridesAllowed(context.Background()); got != tc.want {
				t.Fatalf("rootOverridesAllowed = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestMapSecuritySettingsToGameServers_OnlyHeldSettingsChange(t *testing.T) {
	const cpNS = "gameplane-cp"
	root := &gameplanev1alpha1.GameServerSecuritySpec{RunAsUser: &gameplanev1alpha1.SecurityIDOverride{Value: secSettingsInt64(0)}}
	servers := []*gameplanev1alpha1.GameServer{
		{ObjectMeta: metav1.ObjectMeta{Name: "held", Namespace: "games"}, Spec: gameplanev1alpha1.GameServerSpec{Security: root}},
		{ObjectMeta: metav1.ObjectMeta{Name: "plain", Namespace: "games"}},
		{ObjectMeta: metav1.ObjectMeta{Name: "held-elsewhere", Namespace: "other"}, Spec: gameplanev1alpha1.GameServerSpec{Security: root}},
	}
	s := testScheme(t)
	b := fake.NewClientBuilder().WithScheme(s)
	for _, gs := range servers {
		b = b.WithObjects(gs)
	}
	r := &GameServerReconciler{Client: b.Build(), ControlPlaneNamespace: cpNS}

	settings := func(ns, name string) *corev1.ConfigMap {
		return &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns}}
	}
	cases := []struct {
		name string
		obj  *corev1.ConfigMap
		want []string
	}{
		{name: "settings ConfigMap enqueues only servers that save a root override", obj: settings(cpNS, securitySettingsConfigMapName), want: []string{"games/held", "other/held-elsewhere"}},
		{name: "other ConfigMap name is ignored", obj: settings(cpNS, "something-else"), want: nil},
		{name: "same name outside the control-plane namespace is ignored", obj: settings("games", securitySettingsConfigMapName), want: nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reqs := r.mapSecuritySettingsToGameServers(context.Background(), tc.obj)
			var got []string
			for _, req := range reqs {
				got = append(got, req.Namespace+"/"+req.Name)
			}
			sort.Strings(got)
			if len(got) != len(tc.want) {
				t.Fatalf("enqueued %v, want %v", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("enqueued %v, want %v", got, tc.want)
				}
			}
		})
	}

	unset := &GameServerReconciler{Client: r.Client}
	if reqs := unset.mapSecuritySettingsToGameServers(context.Background(), settings(cpNS, securitySettingsConfigMapName)); len(reqs) != 0 {
		t.Fatalf("empty ControlPlaneNamespace must enqueue nothing, got %d", len(reqs))
	}
}
