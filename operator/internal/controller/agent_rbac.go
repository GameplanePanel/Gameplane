package controller

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	gameplanev1alpha1 "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
)

// agentServiceAccountName is the per-GameServer ServiceAccount the game
// pod runs as (unless spec.serviceAccountName overrides it).
func agentServiceAccountName(gs *gameplanev1alpha1.GameServer) string {
	return gs.Name + "-agent"
}

// reconcileAgentRBAC gives the game pod an identity the agent sidecar
// can use for its status heartbeat (agent/internal/heartbeat patches
// gameservers/status with the pod's ServiceAccount). The grant is as
// narrow as RBAC allows: one ServiceAccount per GameServer, bound to a
// Role whose only rule is get/patch on this GameServer's status
// subresource via resourceNames. All three objects are owned by the
// GameServer so they're GC'd with it.
func (r *GameServerReconciler) reconcileAgentRBAC(
	ctx context.Context, gs *gameplanev1alpha1.GameServer,
) error {
	name := agentServiceAccountName(gs)

	sa := &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: gs.Namespace},
	}
	if _, err := controllerutil.CreateOrUpdate(ctx, r.Client, sa, func() error {
		return controllerutil.SetControllerReference(gs, sa, r.Scheme)
	}); err != nil {
		return fmt.Errorf("upsert agent ServiceAccount %s/%s: %w", gs.Namespace, name, err)
	}

	role := &rbacv1.Role{
		ObjectMeta: metav1.ObjectMeta{Name: name + "-heartbeat", Namespace: gs.Namespace},
	}
	if _, err := controllerutil.CreateOrUpdate(ctx, r.Client, role, func() error {
		role.Rules = []rbacv1.PolicyRule{{
			APIGroups:     []string{"gameplane.local"},
			Resources:     []string{"gameservers/status"},
			ResourceNames: []string{gs.Name},
			Verbs:         []string{"get", "patch"},
		}}
		return controllerutil.SetControllerReference(gs, role, r.Scheme)
	}); err != nil {
		return fmt.Errorf("upsert agent heartbeat Role %s/%s: %w", gs.Namespace, role.Name, err)
	}

	rb := &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{Name: name + "-heartbeat", Namespace: gs.Namespace},
	}
	if _, err := controllerutil.CreateOrUpdate(ctx, r.Client, rb, func() error {
		// RoleRef is immutable; the values below are deterministic so an
		// unchanged binding produces a no-op update.
		rb.RoleRef = rbacv1.RoleRef{
			APIGroup: rbacv1.GroupName,
			Kind:     "Role",
			Name:     role.Name,
		}
		rb.Subjects = []rbacv1.Subject{{
			Kind:      rbacv1.ServiceAccountKind,
			Name:      name,
			Namespace: gs.Namespace,
		}}
		return controllerutil.SetControllerReference(gs, rb, r.Scheme)
	}); err != nil {
		return fmt.Errorf("upsert agent heartbeat RoleBinding %s/%s: %w", gs.Namespace, rb.Name, err)
	}

	return nil
}
