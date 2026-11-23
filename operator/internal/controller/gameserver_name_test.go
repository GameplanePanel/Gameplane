package controller

import (
	"context"
	"strings"
	"testing"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/validation/field"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	gameplanev1alpha1 "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
)

func TestValidateServerName(t *testing.T) {
	valid := []string{"sdtd", "mc-1", "seven-days", strings.Repeat("a", 57)}
	for _, name := range valid {
		if err := validateServerName(name); err != nil {
			t.Errorf("validateServerName(%q) = %v, want nil", name, err)
		}
	}
	invalid := []string{"7d2d", "1", "my.server", "-mc", strings.Repeat("a", 58)}
	for _, name := range invalid {
		if err := validateServerName(name); err == nil {
			t.Errorf("validateServerName(%q) = nil, want an error", name)
		}
	}
}

func reconcileNamedServer(t *testing.T, name string, failPatches int) (*gameplanev1alpha1.GameServer, error) {
	t.Helper()
	gs := &gameplanev1alpha1.GameServer{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "games"}}
	base := fake.NewClientBuilder().WithScheme(testScheme(t)).
		WithObjects(gs).WithStatusSubresource(&gameplanev1alpha1.GameServer{}).Build()
	failed := 0
	c := interceptor.NewClient(base, interceptor.Funcs{
		SubResourcePatch: func(ctx context.Context, cl client.Client, subresource string, obj client.Object, patch client.Patch, opts ...client.SubResourcePatchOption) error {
			if failed < failPatches {
				failed++
				return apierrors.NewInvalid(schema.GroupKind{Group: "gameplane.local", Kind: "GameServer"}, obj.GetName(), field.ErrorList{field.Invalid(field.NewPath("status"), "x", "rejected")})
			}
			return cl.SubResource(subresource).Patch(ctx, obj, patch, opts...)
		},
	})
	r := &GameServerReconciler{Client: c, APIReader: c, Scheme: base.Scheme()}
	_, err := r.Reconcile(t.Context(), ctrl.Request{NamespacedName: types.NamespacedName{Namespace: "games", Name: name}})
	var got gameplanev1alpha1.GameServer
	if getErr := base.Get(t.Context(), client.ObjectKeyFromObject(gs), &got); getErr != nil {
		t.Fatal(getErr)
	}
	return &got, err
}

func readyMessage(gs *gameplanev1alpha1.GameServer) string {
	for _, c := range gs.Status.Conditions {
		if c.Type == "Ready" {
			return c.Message
		}
	}
	return ""
}

func TestReconcileRejectsServerNameBeforeChildren(t *testing.T) {
	gs, err := reconcileNamedServer(t, "7d2d", 0)
	if err != nil {
		t.Fatalf("Reconcile() error = %v, want nil", err)
	}
	if gs.Status.Phase != gameplanev1alpha1.GameServerPhaseFailed {
		t.Fatalf("phase = %q, want Failed", gs.Status.Phase)
	}
	if msg := readyMessage(gs); !strings.Contains(msg, `"7d2d"`) {
		t.Fatalf("Ready message = %q, want it to name the server", msg)
	}
}

func TestReconcileRecordsInvalidAPIErrorOnStatus(t *testing.T) {
	gs, err := reconcileNamedServer(t, "7d2d", 1)
	if !apierrors.IsInvalid(err) {
		t.Fatalf("Reconcile() error = %v, want an Invalid error", err)
	}
	if msg := readyMessage(gs); !strings.HasPrefix(msg, "Kubernetes rejected a resource for this server: ") {
		t.Fatalf("Ready message = %q, want the rejected-resource reason", msg)
	}
}

func TestReconcileReturnsInvalidErrorWhenStatusCannotBeRecorded(t *testing.T) {
	if _, err := reconcileNamedServer(t, "7d2d", 2); !apierrors.IsInvalid(err) {
		t.Fatalf("Reconcile() error = %v, want an Invalid error", err)
	}
}
