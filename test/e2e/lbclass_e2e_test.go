//go:build e2e

package e2e

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
)

// TestGameServer_LoadBalancerClassReplacesTheService — a LoadBalancer server
// created without a class gets a new Service carrying the class once one is
// requested, and another new one when the class changes. Kubernetes rejects
// adding or changing a LoadBalancer Service's class in place, so each step
// must be a replacement (a new Service UID), never an update.
func TestGameServer_LoadBalancerClassReplacesTheService(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	ns := "gameplane-games"
	tmplName := fmt.Sprintf("e2e-lbclass-%d", time.Now().UnixNano())
	gsName := fmt.Sprintf("e2e-gs-lbclass-%d", time.Now().UnixNano())
	applyBusyboxTemplate(t, tmplName)

	gs := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "gameplane.local/v1alpha1",
		"kind":       "GameServer",
		"metadata":   map[string]any{"name": gsName, "namespace": ns},
		"spec": map[string]any{
			"templateRef": map[string]any{"name": tmplName},
			"networking":  map[string]any{"expose": "LoadBalancer"},
		},
	}}
	if _, err := envInstance.Dyn.Resource(gameServerGVR).Namespace(ns).
		Create(ctx, gs, metav1.CreateOptions{}); err != nil && !apierrors.IsAlreadyExists(err) {
		t.Fatalf("create gameserver: %v", err)
	}
	t.Cleanup(func() {
		_ = envInstance.Dyn.Resource(gameServerGVR).Namespace(ns).
			Delete(context.Background(), gsName, metav1.DeleteOptions{})
	})

	// waitForClass waits for a LoadBalancer Service with the given class (nil
	// for none) whose UID differs from previous, and returns its UID.
	waitForClass := func(class *string, previous types.UID) types.UID {
		t.Helper()
		var uid types.UID
		envInstance.Eventually(t, 120*time.Second, func() (bool, string) {
			svc, err := envInstance.K8s.CoreV1().Services(ns).Get(ctx, gsName, metav1.GetOptions{})
			if err != nil {
				return false, "get service: " + err.Error()
			}
			if svc.Spec.Type != corev1.ServiceTypeLoadBalancer {
				return false, fmt.Sprintf("service type = %s, want LoadBalancer", svc.Spec.Type)
			}
			if svc.UID == previous {
				return false, "service not replaced yet"
			}
			got := svc.Spec.LoadBalancerClass
			switch {
			case class == nil && got != nil:
				return false, fmt.Sprintf("service class = %q, want none", *got)
			case class != nil && (got == nil || *got != *class):
				return false, fmt.Sprintf("service class = %v, want %q", got, *class)
			}
			uid = svc.UID
			return true, ""
		})
		return uid
	}
	setClass := func(class string) {
		t.Helper()
		patch := fmt.Appendf(nil, `{"spec":{"networking":{"loadBalancerClass":%q}}}`, class)
		if _, err := envInstance.Dyn.Resource(gameServerGVR).Namespace(ns).
			Patch(ctx, gsName, types.MergePatchType, patch, metav1.PatchOptions{}); err != nil {
			t.Fatalf("set loadBalancerClass %q: %v", class, err)
		}
	}

	unclassed := waitForClass(nil, "")

	first := "e2e.gameplane.local/first"
	setClass(first)
	classed := waitForClass(&first, unclassed)

	second := "e2e.gameplane.local/second"
	setClass(second)
	waitForClass(&second, classed)
}

// TestCRD_Validation_LoadBalancerClassNameLength — the class's name part may
// be up to 63 characters, as Kubernetes allows for a Service's
// loadBalancerClass, and its prefix up to 253. Longer ones are rejected at
// admission, before they could replace a working Service with one Kubernetes
// refuses to create.
func TestCRD_Validation_LoadBalancerClassNameLength(t *testing.T) {
	t.Parallel()

	manifest := func(name, class string) string {
		return fmt.Sprintf(`apiVersion: gameplane.local/v1alpha1
kind: GameServer
metadata:
  name: %s
  namespace: gameplane-games
spec:
  templateRef:
    name: e2e-template-test
  networking:
    expose: LoadBalancer
    loadBalancerClass: %s
`, name, class)
	}

	longest := "example.com/" + strings.Repeat("a", 63)
	if out, err := envInstance.KubectlWithStdin(t.Context(), manifest("e2e-validation-lbclass-63", longest),
		"apply", "--dry-run=server", "-f", "-"); err != nil {
		t.Fatalf("CRD schema rejected a 63-character class name, which Kubernetes allows.\nkubectl output:\n%s", out)
	}

	expectAdmissionRejection(t, manifest("e2e-validation-lbclass-64", "example.com/"+strings.Repeat("a", 64)),
		[]string{"loadBalancerClass"})

	// A qualified name's prefix can be 253 characters, so the whole value
	// can be 317.
	prefix := func(n int) string {
		label := strings.Repeat("a", 63)
		p := strings.Join([]string{label, label, label}, ".") // 191
		return p + "." + strings.Repeat("b", n-len(p)-1)
	}
	if out, err := envInstance.KubectlWithStdin(t.Context(),
		manifest("e2e-validation-lbclass-317", prefix(253)+"/"+strings.Repeat("a", 63)),
		"apply", "--dry-run=server", "-f", "-"); err != nil {
		t.Fatalf("CRD schema rejected a 317-character class, which Kubernetes allows.\nkubectl output:\n%s", out)
	}
	expectAdmissionRejection(t, manifest("e2e-validation-lbclass-prefix-254", prefix(254)+"/a"),
		[]string{"loadBalancerClass"})
}
