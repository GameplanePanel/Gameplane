package controller

import (
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	ctrl "sigs.k8s.io/controller-runtime"
)

// reconcileRetryInterval yields after expected write races or persisted
// lifecycle transitions so the next pass can read fresh state. These are not
// failures: real errors retain controller-runtime's exponential backoff.
// A positive delay avoids both dropping retries and spinning on a stale cache.
const reconcileRetryInterval = time.Second

// requeueOnConflict turns an optimistic-concurrency conflict ("the object has
// been modified") into a quiet requeue. Conflicts are an expected, transient
// race between reconcilers and the API server cache; returning them as errors
// logs a spurious ERROR on every race. Any other result passes through.
func requeueOnConflict(res ctrl.Result, err error) (ctrl.Result, error) {
	if apierrors.IsConflict(err) {
		return ctrl.Result{RequeueAfter: reconcileRetryInterval}, nil
	}
	return res, err
}
