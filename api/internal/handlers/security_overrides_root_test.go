package handlers

import (
	"context"
	"errors"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	k8sfake "k8s.io/client-go/kubernetes/fake"

	"github.com/GameplanePanel/gameplane/api/internal/httperr"
	"github.com/GameplanePanel/gameplane/api/internal/kube"
	"github.com/GameplanePanel/gameplane/api/internal/scope"
)

// T048 (unit): the root rule (case 5) of contracts/api-server-security.md,
// validateSecurityRootRule, against a fake clientset holding the switch
// ConfigMap. The envtest half is in security_overrides_root_envtest_test.go.

const secRootNamespace = "gameplane-credentials"

// secRootObj builds a GameServer whose spec.security is security (nil omits it).
func secRootObj(security map[string]any) *unstructured.Unstructured {
	spec := map[string]any{"templateRef": map[string]any{"name": "minecraft"}}
	if security != nil {
		spec["security"] = security
	}
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "gameplane.local/v1alpha1",
		"kind":       "GameServer",
		"metadata":   map[string]any{"name": "root-case", "namespace": scope.DefaultNamespace},
		"spec":       spec,
	}}
}

// secRootRegistry builds a home registry. cmValue "" means no ConfigMap at
// all; "-" means the ConfigMap exists without the key. A registry with no
// client at all is built when noClient is true.
func secRootRegistry(cmValue string, noClient bool) *kube.Registry {
	reg := kube.NewRegistry(scope.DefaultCluster)
	reg.SetControlPlaneNamespace(secRootNamespace)
	if noClient {
		return reg
	}
	var objs []*corev1.ConfigMap
	if cmValue != "" {
		cm := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: securitySettingsConfigMap, Namespace: secRootNamespace}}
		if cmValue != "-" {
			cm.Data = map[string]string{securitySettingsKey: cmValue}
		}
		objs = append(objs, cm)
	}
	cs := k8sfake.NewSimpleClientset()
	for _, cm := range objs {
		if _, err := cs.CoreV1().ConfigMaps(secRootNamespace).Create(context.Background(), cm, metav1.CreateOptions{}); err != nil {
			panic(err)
		}
	}
	reg.Set(scope.DefaultCluster, &kube.Client{Typed: cs})
	return reg
}

// secRootRefusedSetting returns the setting named by a root refusal, or "" when err is nil.
// It fails the test when err is some other error.
func secRootRefusedSetting(t *testing.T, err error) string {
	t.Helper()
	if err == nil {
		return ""
	}
	var m *httperr.Message
	if !errors.As(err, &m) || m.ID != "server.security.root_disabled" {
		t.Fatalf("err = %v, want server.security.root_disabled", err)
	}
	setting, _ := m.Args["setting"].(string)
	return setting
}

func TestSecurityRootRule_Cases(t *testing.T) {
	root := func(v int64) map[string]any { return map[string]any{"value": v} }
	imageDefault := map[string]any{"useImageDefault": true}

	cases := []struct {
		name      string
		cm        string // "" no ConfigMap, "-" key missing, else the stored value
		noClient  bool
		desired   map[string]any
		live      map[string]any
		wantBlock string // the setting refused, "" when accepted
	}{
		{name: "new root user refused while ConfigMap absent", desired: map[string]any{"runAsUser": root(0)}, wantBlock: "runAsUser"},
		{name: "new root user refused when false", cm: "false", desired: map[string]any{"runAsUser": root(0)}, wantBlock: "runAsUser"},
		{name: "new root user accepted when true", cm: "true", desired: map[string]any{"runAsUser": root(0)}},
		{name: "only exact true is on", cm: "TRUE", desired: map[string]any{"runAsUser": root(0)}, wantBlock: "runAsUser"},
		{name: "yes is off", cm: "yes", desired: map[string]any{"runAsUser": root(0)}, wantBlock: "runAsUser"},
		{name: "key missing is off", cm: "-", desired: map[string]any{"runAsUser": root(0)}, wantBlock: "runAsUser"},
		{name: "no client is off (read error)", noClient: true, desired: map[string]any{"runAsUser": root(0)}, wantBlock: "runAsUser"},
		{name: "root group by image default refused", cm: "false", desired: map[string]any{"runAsGroup": imageDefault}, wantBlock: "runAsGroup"},
		{name: "root user by image default refused", cm: "false", desired: map[string]any{"runAsUser": imageDefault}, wantBlock: "runAsUser"},
		{name: "fsGroup value 0 refused", cm: "false", desired: map[string]any{"fsGroup": root(0)}, wantBlock: "fsGroup"},
		{name: "fsGroup image default is not root", cm: "false", desired: map[string]any{"fsGroup": imageDefault}},
		{name: "non-root user accepted while off", cm: "false", desired: map[string]any{"runAsUser": root(1000)}},
		{name: "non-root accepted without reading the ConfigMap", noClient: true, desired: map[string]any{"runAsUser": root(1000)}},
		{
			name: "unchanged stored root passes while off",
			cm:   "false",
			live: map[string]any{"runAsUser": root(0)}, desired: map[string]any{"runAsUser": root(0), "fsGroup": root(2000)},
		},
		{
			name: "replacing a stored root with non-root passes while off",
			cm:   "false",
			live: map[string]any{"runAsUser": root(0)}, desired: map[string]any{"runAsUser": root(2000)},
		},
		{
			name: "removing a stored root passes while off",
			cm:   "false",
			live: map[string]any{"runAsUser": root(0)}, desired: nil,
		},
		{
			name: "same root written as image default is a change and refused",
			cm:   "false",
			live: map[string]any{"runAsUser": root(0)}, desired: map[string]any{"runAsUser": imageDefault},
			wantBlock: "runAsUser",
		},
		{
			name:      "first offending setting in refusal order",
			cm:        "false",
			desired:   map[string]any{"fsGroup": root(0), "runAsUser": root(0)},
			wantBlock: "runAsUser",
		},
		{
			name: "a new root in fsGroup is refused beside an unchanged stored root",
			cm:   "false",
			live: map[string]any{"runAsUser": root(0)}, desired: map[string]any{"runAsUser": root(0), "fsGroup": root(0)},
			wantBlock: "fsGroup",
		},
		{name: "accepted once on", cm: "true", desired: map[string]any{"fsGroup": root(0), "runAsGroup": root(0)}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reg := secRootRegistry(tc.cm, tc.noClient)
			var live *unstructured.Unstructured
			if tc.live != nil {
				live = secRootObj(tc.live)
			}
			desired := secRootObj(tc.desired)
			if tc.desired == nil {
				desired = secRootObj(nil)
			}
			err := validateSecurityRootRule(context.Background(), reg, scope.DefaultCluster, desired, live)
			if got := secRootRefusedSetting(t, err); got != tc.wantBlock {
				t.Errorf("refused setting = %q, want %q (err=%v)", got, tc.wantBlock, err)
			}
		})
	}
}

// A saved override with the setting off is not refused by an unrelated
// whole-spec save: the rule looks only at a new or changed root value.
func TestSecurityRootRule_UnrelatedSaveWithStoredRootPasses(t *testing.T) {
	reg := secRootRegistry("false", false)
	live := secRootObj(map[string]any{"runAsUser": map[string]any{"value": int64(0)}})
	desired := secRootObj(map[string]any{"runAsUser": map[string]any{"value": int64(0)}})
	desired.SetLabels(map[string]string{"sec-test": "unrelated"})
	if err := validateSecurityRootRule(context.Background(), reg, scope.DefaultCluster, desired, live); err != nil {
		t.Fatalf("unrelated save with stored root = %v, want accepted", err)
	}
}

// A request in a cluster with no home control-plane namespace fails closed.
func TestSecurityRootRule_UnsetNamespaceFailsClosed(t *testing.T) {
	cs := k8sfake.NewSimpleClientset(&corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: securitySettingsConfigMap, Namespace: secRootNamespace},
		Data:       map[string]string{securitySettingsKey: "true"},
	})
	reg := kube.NewRegistry(scope.DefaultCluster)
	reg.Set(scope.DefaultCluster, &kube.Client{Typed: cs})
	desired := secRootObj(map[string]any{"runAsUser": map[string]any{"value": int64(0)}})
	err := validateSecurityRootRule(context.Background(), reg, scope.DefaultCluster, desired, nil)
	if got := secRootRefusedSetting(t, err); got != "runAsUser" {
		t.Errorf("unset namespace refused setting = %q, want runAsUser", got)
	}
}
