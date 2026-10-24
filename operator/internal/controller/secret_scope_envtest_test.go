//go:build envtest

package controller

import (
	"context"
	"fmt"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	gameplanev1alpha1 "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
	"github.com/GameplanePanel/gameplane/operator/internal/secretscope"
)

// Removing the per-object scope must make Secret informer synchronization fail
// under this identity, while removing any auxiliary namespace loses its Secret.
func TestSecretScope_CacheSyncWithNamespaceLocalRBAC(t *testing.T) {
	games, otherGames, control, ca, modules, unrelated := newNamespace(t), newNamespace(t), newNamespace(t), newNamespace(t), newNamespace(t), newNamespace(t)
	allowed := []string{games, otherGames, control, ca, modules}
	subject := rbacv1.Subject{Kind: "ServiceAccount", Namespace: control, Name: "scoped-operator"}
	for _, ns := range allowed {
		role := &rbacv1.Role{ObjectMeta: metav1.ObjectMeta{Name: "secret-read", Namespace: ns}, Rules: []rbacv1.PolicyRule{{APIGroups: []string{""}, Resources: []string{"secrets"}, Verbs: []string{"get", "list", "watch"}}}}
		binding := &rbacv1.RoleBinding{ObjectMeta: metav1.ObjectMeta{Name: "secret-read", Namespace: ns}, RoleRef: rbacv1.RoleRef{APIGroup: rbacv1.GroupName, Kind: "Role", Name: role.Name}, Subjects: []rbacv1.Subject{subject}}
		for _, obj := range []client.Object{role, binding} {
			if err := k8sClient.Create(t.Context(), obj); err != nil {
				t.Fatal(err)
			}
		}
	}
	roleName := uniqueName("scope-watch")
	role := &rbacv1.ClusterRole{ObjectMeta: metav1.ObjectMeta{Name: roleName}, Rules: []rbacv1.PolicyRule{{APIGroups: []string{"gameplane.local"}, Resources: []string{"gameservers", "gametemplates"}, Verbs: []string{"get", "list", "watch"}}}}
	binding := &rbacv1.ClusterRoleBinding{ObjectMeta: metav1.ObjectMeta{Name: roleName}, RoleRef: rbacv1.RoleRef{APIGroup: rbacv1.GroupName, Kind: "ClusterRole", Name: roleName}, Subjects: []rbacv1.Subject{subject}}
	for _, obj := range []client.Object{role, binding} {
		if err := k8sClient.Create(t.Context(), obj); err != nil {
			t.Fatal(err)
		}
		deleteCleanup(t, obj)
	}
	for _, ns := range append(append([]string{}, allowed...), unrelated) {
		if err := k8sClient.Create(t.Context(), &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "credential", Namespace: ns}}); err != nil {
			t.Fatal(err)
		}
	}
	limitedConfig := rest.CopyConfig(cfg)
	limitedConfig.Impersonate = rest.ImpersonationConfig{UserName: fmt.Sprintf("system:serviceaccount:%s:%s", control, subject.Name)}
	direct, err := client.New(limitedConfig, client.Options{Scheme: scheme})
	if err != nil {
		t.Fatal(err)
	}
	eventually(t, func() (bool, string) {
		err := direct.Get(t.Context(), client.ObjectKey{Namespace: ca, Name: "credential"}, &corev1.Secret{})
		return err == nil, fmt.Sprintf("waiting for RBAC propagation: %v", err)
	})
	if err := direct.Get(t.Context(), client.ObjectKey{Namespace: unrelated, Name: "credential"}, &corev1.Secret{}); !apierrors.IsForbidden(err) {
		t.Fatalf("unrelated Secret get = %v, want Forbidden", err)
	}
	if err := direct.List(t.Context(), &corev1.SecretList{}); !apierrors.IsForbidden(err) {
		t.Fatalf("cluster Secret list = %v, want Forbidden", err)
	}
	options, err := secretscope.CacheOptions(games+","+otherGames, control, ca, modules)
	if err != nil {
		t.Fatal(err)
	}
	mgr, err := ctrl.NewManager(limitedConfig, ctrl.Options{Scheme: scheme, Cache: options, Metrics: metricsserver.Options{BindAddress: "0"}, HealthProbeBindAddress: "0"})
	if err != nil {
		t.Fatal(err)
	}
	// This is the same Owns(Secret) informer registration used by the real
	// GameServer controller, including its cluster-wide parent watch.
	if err := ctrl.NewControllerManagedBy(mgr).Named(roleName).
		For(&gameplanev1alpha1.GameServer{}).Owns(&corev1.Secret{}).
		Complete(reconcile.Func(func(context.Context, ctrl.Request) (ctrl.Result, error) { return ctrl.Result{}, nil })); err != nil {
		t.Fatal(err)
	}
	if _, err := mgr.GetCache().GetInformer(t.Context(), &gameplanev1alpha1.GameTemplate{}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- mgr.Start(ctx) }()
	defer func() {
		cancel()
		if err := <-done; err != nil {
			t.Errorf("manager stopped: %v", err)
		}
	}()
	if !mgr.GetCache().WaitForCacheSync(ctx) {
		t.Fatal("scoped cache did not synchronize")
	}
	for _, ns := range allowed {
		if err := mgr.GetClient().Get(ctx, client.ObjectKey{Namespace: ns, Name: "credential"}, &corev1.Secret{}); err != nil {
			t.Fatalf("cached Secret in %s: %v", ns, err)
		}
	}
	// Rotation must arrive over the namespace-scoped watch, not require a
	// process restart or a direct-reader fallback.
	var rotated corev1.Secret
	if err := k8sClient.Get(ctx, client.ObjectKey{Namespace: ca, Name: "credential"}, &rotated); err != nil {
		t.Fatal(err)
	}
	rotated.Data = map[string][]byte{"generation": []byte("renewed")}
	if err := k8sClient.Update(ctx, &rotated); err != nil {
		t.Fatal(err)
	}
	eventually(t, func() (bool, string) {
		var observed corev1.Secret
		err := mgr.GetClient().Get(ctx, client.ObjectKeyFromObject(&rotated), &observed)
		return err == nil && string(observed.Data["generation"]) == "renewed", "waiting for Secret rotation"
	})
	var secrets corev1.SecretList
	if err := mgr.GetClient().List(ctx, &secrets); err != nil || len(secrets.Items) != len(allowed) {
		t.Fatalf("cached Secret union: count=%d, err=%v", len(secrets.Items), err)
	}
	if err := mgr.GetClient().Get(ctx, client.ObjectKey{Namespace: unrelated, Name: "credential"}, &corev1.Secret{}); err == nil {
		t.Fatal("cache exposed an unrelated Secret")
	}
	if err := mgr.GetClient().List(ctx, &gameplanev1alpha1.GameTemplateList{}); err != nil {
		t.Fatalf("cluster-scoped GameTemplate list: %v", err)
	}
}
