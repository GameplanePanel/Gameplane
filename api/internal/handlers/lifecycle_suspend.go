package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/util/retry"

	"github.com/GameplanePanel/gameplane/api/internal/kube"
)

// patchServerSuspend retries controller write contention without replaying a
// power request against a replacement server or changed configuration/grants.
// original is the snapshot accepted by authorizedServer for this request.
func patchServerSuspend(ctx context.Context, k *kube.Client, original *unstructured.Unstructured, suspend bool) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	name := original.GetName()
	resource := k.Dynamic.Resource(kube.GVRs["servers"]).Namespace(original.GetNamespace())
	if original.GetDeletionTimestamp() != nil {
		return apierrors.NewNotFound(kube.GVRs["servers"].GroupResource(), name)
	}
	var lastConflict error
	attempt := 0
	err := wait.ExponentialBackoffWithContext(ctx, retry.DefaultRetry, func(ctx context.Context) (bool, error) {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		current := original
		if attempt > 0 {
			fresh, err := resource.Get(ctx, name, metav1.GetOptions{})
			if err != nil {
				return false, err
			}
			if original.GetUID() == "" || fresh.GetUID() != original.GetUID() || fresh.GetDeletionTimestamp() != nil {
				return false, apierrors.NewNotFound(kube.GVRs["servers"].GroupResource(), name)
			}
			if !reflect.DeepEqual(fresh.Object["spec"], original.Object["spec"]) ||
				fresh.GetAnnotations()[ownerIDAnnotation] != original.GetAnnotations()[ownerIDAnnotation] ||
				fresh.GetAnnotations()[collaboratorsAnnotation] != original.GetAnnotations()[collaboratorsAnnotation] {
				return false, lastConflict
			}
			current = fresh
		}
		body, err := json.Marshal(conditionObjectPatch(map[string]any{"spec": map[string]any{"suspend": suspend}}, current))
		if err != nil {
			return false, err
		}
		attempt++
		_, err = resource.Patch(ctx, name, types.MergePatchType, body, metav1.PatchOptions{})
		if apierrors.IsConflict(err) {
			lastConflict = err
			return false, nil
		}
		return err == nil, err
	})
	if wait.Interrupted(err) {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		if lastConflict != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
			return lastConflict
		}
	}
	return err
}
