package controller

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	gameplanev1alpha1 "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
)

// tunnelServiceAccountName is the per-GameServer ServiceAccount the tunnel
// pod runs as, isolated from the game pod's agent ServiceAccount.
func tunnelServiceAccountName(gs *gameplanev1alpha1.GameServer) string {
	return gs.Name + "-tunnel"
}

// reconcileTunnelRBAC gives the tunnel pod an identity to patch the
// GameServer's status subresource with the playit-assigned endpoint.
// The grant is as narrow as RBAC allows: one ServiceAccount per GameServer,
// bound to a Role whose only rule is patch on this GameServer's status
// subresource via resourceNames. All three objects are owned by the
// GameServer so they're GC'd with it.
//
// When needPlayitRBAC is false, reconcileTunnelRBAC converges the RBAC
// objects to absent (deletes them if they exist and are owned by this
// GameServer). Only playit needs the grant, so this keeps the API surface
// tidy when playit is not in use.
func (r *GameServerReconciler) reconcileTunnelRBAC(
	ctx context.Context, gs *gameplanev1alpha1.GameServer, needPlayitRBAC bool,
) error {
	name := tunnelServiceAccountName(gs)

	// If RBAC is not needed, converge the objects to absent.
	if !needPlayitRBAC {
		// Delete ServiceAccount if it exists and is owned by this GameServer.
		sa := &corev1.ServiceAccount{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: gs.Namespace},
		}
		var existingSA corev1.ServiceAccount
		if err := r.Get(ctx, client.ObjectKeyFromObject(sa), &existingSA); err != nil {
			if !apierrors.IsNotFound(err) {
				return fmt.Errorf("get tunnel ServiceAccount %s/%s: %w", gs.Namespace, name, err)
			}
		} else if metav1.IsControlledBy(&existingSA, gs) {
			if err := r.Delete(ctx, &existingSA); err != nil && !apierrors.IsNotFound(err) {
				return fmt.Errorf("delete tunnel ServiceAccount %s/%s: %w", gs.Namespace, name, err)
			}
		}

		// Delete Role if it exists and is owned by this GameServer.
		role := &rbacv1.Role{
			ObjectMeta: metav1.ObjectMeta{Name: name + "-tunnel", Namespace: gs.Namespace},
		}
		var existingRole rbacv1.Role
		if err := r.Get(ctx, client.ObjectKeyFromObject(role), &existingRole); err != nil {
			if !apierrors.IsNotFound(err) {
				return fmt.Errorf("get tunnel Role %s/%s: %w", gs.Namespace, role.Name, err)
			}
		} else if metav1.IsControlledBy(&existingRole, gs) {
			if err := r.Delete(ctx, &existingRole); err != nil && !apierrors.IsNotFound(err) {
				return fmt.Errorf("delete tunnel Role %s/%s: %w", gs.Namespace, role.Name, err)
			}
		}

		// Delete RoleBinding if it exists and is owned by this GameServer.
		rb := &rbacv1.RoleBinding{
			ObjectMeta: metav1.ObjectMeta{Name: name + "-tunnel", Namespace: gs.Namespace},
		}
		var existingRB rbacv1.RoleBinding
		if err := r.Get(ctx, client.ObjectKeyFromObject(rb), &existingRB); err != nil {
			if !apierrors.IsNotFound(err) {
				return fmt.Errorf("get tunnel RoleBinding %s/%s: %w", gs.Namespace, rb.Name, err)
			}
		} else if metav1.IsControlledBy(&existingRB, gs) {
			if err := r.Delete(ctx, &existingRB); err != nil && !apierrors.IsNotFound(err) {
				return fmt.Errorf("delete tunnel RoleBinding %s/%s: %w", gs.Namespace, rb.Name, err)
			}
		}
		return nil
	}

	sa := &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: gs.Namespace},
	}
	if _, err := controllerutil.CreateOrUpdate(ctx, r.Client, sa, func() error {
		return controllerutil.SetControllerReference(gs, sa, r.Scheme)
	}); err != nil {
		return fmt.Errorf("upsert tunnel ServiceAccount %s/%s: %w", gs.Namespace, name, err)
	}

	role := &rbacv1.Role{
		ObjectMeta: metav1.ObjectMeta{Name: name + "-tunnel", Namespace: gs.Namespace},
	}
	if _, err := controllerutil.CreateOrUpdate(ctx, r.Client, role, func() error {
		role.Rules = []rbacv1.PolicyRule{{
			APIGroups:     []string{"gameplane.local"},
			Resources:     []string{"gameservers/status"},
			ResourceNames: []string{gs.Name},
			Verbs:         []string{"patch"},
		}}
		return controllerutil.SetControllerReference(gs, role, r.Scheme)
	}); err != nil {
		return fmt.Errorf("upsert tunnel Role %s/%s: %w", gs.Namespace, role.Name, err)
	}

	rb := &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{Name: name + "-tunnel", Namespace: gs.Namespace},
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
		return fmt.Errorf("upsert tunnel RoleBinding %s/%s: %w", gs.Namespace, rb.Name, err)
	}

	return nil
}
