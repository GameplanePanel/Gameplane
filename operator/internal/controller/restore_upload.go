package controller

import (
	"context"

	ctrl "sigs.k8s.io/controller-runtime"

	gameplanev1alpha1 "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
)

// reconcileUpload drives a restore from an uploaded archive (spec 041 R5).
// The receive, check, confirm and copy lifecycle lands with task T024; until
// then an upload restore fails without touching the target server.
func (r *RestoreReconciler) reconcileUpload(ctx context.Context, rs *gameplanev1alpha1.Restore) (ctrl.Result, error) {
	return r.fail(ctx, rs, "restore from an uploaded archive is not available yet")
}
