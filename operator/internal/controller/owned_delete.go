package controller

import (
	"context"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	gameplanev1alpha1 "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
)

// deleteIfControlledBy deletes obj only if the GameServer gs is listed as a
// controller in the object's OwnerReferences (Kind, Name, AND UID must all
// match — the same check every other ownership guard in this package uses).
// Returns no error if the object doesn't exist or is not owned by gs.
func (r *GameServerReconciler) deleteIfControlledBy(
	ctx context.Context, gs *gameplanev1alpha1.GameServer, obj client.Object,
) error {
	// Read from the cache to check ownership.
	if err := r.Get(ctx, client.ObjectKeyFromObject(obj), obj); err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}
		return err
	}

	// Check if this GameServer is a controller of the object. UID must
	// match too (metav1.IsControlledBy), not just Kind+Name — otherwise an
	// unowned object that merely shares this GameServer's fixed name gets
	// deleted.
	if !metav1.IsControlledBy(obj, gs) {
		return nil
	}

	// Delete it, preconditioned on the UID we just verified ownership of.
	// Without this, an object deleted and replaced at the same name between
	// the ownership check above and this Delete reaching the API server
	// would still match on name and be deleted, even though this GameServer
	// never controlled the replacement.
	uid := obj.GetUID()
	return client.IgnoreNotFound(r.Delete(ctx, obj, client.Preconditions{UID: &uid}))
}
