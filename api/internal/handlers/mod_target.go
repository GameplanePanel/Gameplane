package handlers

import (
	"context"
	"errors"
	"net/http"
	"reflect"

	"github.com/go-chi/chi/v5"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/util/retry"

	"github.com/GameplanePanel/gameplane/api/internal/httperr"
	"github.com/GameplanePanel/gameplane/api/internal/kube"
	"github.com/GameplanePanel/gameplane/api/internal/rbac"
	"github.com/GameplanePanel/gameplane/api/internal/scope"
)

// modTarget keeps the server, template and writes on the same selected client.
// Registry provider credentials are deliberately not part of this target: those
// remain centrally configured by the API administrator.
type modTarget struct {
	k         *kube.Client
	namespace string
	server    *unstructured.Unstructured
	template  *unstructured.Unstructured
}

// updateModTarget tolerates controller status writes without rebasing a user's
// change onto another server or concurrently edited configuration. Only an
// explicit Update conflict can retry; reads and all other failures stop here.
func updateModTarget(ctx context.Context, target *modTarget, apply func(*unstructured.Unstructured)) (*unstructured.Unstructured, error) {
	resource := target.k.Dynamic.Resource(kube.GVRs["servers"]).Namespace(target.namespace)
	original := target.server.DeepCopy()
	var updated *unstructured.Unstructured
	var lastConflict error
	attempt := 0
	err := wait.ExponentialBackoffWithContext(ctx, retry.DefaultRetry, func(ctx context.Context) (bool, error) {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		current := original.DeepCopy()
		if attempt > 0 {
			fresh, err := resource.Get(ctx, original.GetName(), metav1.GetOptions{})
			if err != nil {
				return false, err
			}
			if original.GetUID() == "" || fresh.GetUID() != original.GetUID() || fresh.GetDeletionTimestamp() != nil {
				return false, apierrors.NewNotFound(kube.GVRs["servers"].GroupResource(), original.GetName())
			}
			if !reflect.DeepEqual(fresh.Object["spec"], original.Object["spec"]) ||
				fresh.GetAnnotations()[ownerIDAnnotation] != original.GetAnnotations()[ownerIDAnnotation] ||
				fresh.GetAnnotations()[collaboratorsAnnotation] != original.GetAnnotations()[collaboratorsAnnotation] {
				return false, apierrors.NewConflict(kube.GVRs["servers"].GroupResource(), original.GetName(), errors.New("server configuration or ownership changed"))
			}
			current = fresh.DeepCopy()
		}
		attempt++
		apply(current)
		var err error
		updated, err = resource.Update(ctx, current, metav1.UpdateOptions{})
		if apierrors.IsConflict(err) {
			lastConflict = err
			return false, nil
		}
		return err == nil, err
	})
	if wait.Interrupted(err) {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		if lastConflict != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
			err = lastConflict
		}
	}
	if err != nil {
		return nil, err
	}
	return updated, nil
}

func loadModTarget(w http.ResponseWriter, req *http.Request, clients *kube.Registry, local *kube.Client) (*modTarget, bool) {
	cluster := scope.DefaultCluster
	k := local
	if clients == nil {
		// Legacy mounts have no way to select a remote client.
		if rejectRemoteCluster(w, req) {
			return nil, false
		}
	} else {
		var err error
		cluster, err = scope.ResolveCluster(req, clients)
		if err != nil {
			httperr.Write(w, req, err)
			return nil, false
		}
		k, _ = clients.Get(cluster)
	}
	if k == nil {
		httperr.Write(w, req, scope.ErrForbiddenCluster)
		return nil, false
	}
	ns, ok := resolveNS(w, req)
	if !ok {
		return nil, false
	}
	name := chi.URLParam(req, "name")
	gs, err := k.Dynamic.Resource(kube.GVRs["servers"]).Namespace(ns).Get(req.Context(), name, metav1.GetOptions{})
	if err != nil {
		httperr.Write(w, req, err)
		return nil, false
	}
	// Ownership fallback authorizes a UID, not a reusable server name. Check
	// before reading its template or making a provider request on its behalf.
	if err := rbac.ValidateServerIdentity(req.Context(), cluster, ns, name, string(gs.GetUID())); err != nil {
		http.NotFound(w, req)
		return nil, false
	}
	target := &modTarget{k: k, namespace: ns, server: gs}
	templateName, _, _ := unstructured.NestedString(gs.Object, "spec", "templateRef", "name")
	if templateName != "" {
		target.template, err = k.Dynamic.Resource(kube.GVRs["templates"]).Get(req.Context(), templateName, metav1.GetOptions{})
		if err != nil {
			httperr.Write(w, req, err)
			return nil, false
		}
	}
	return target, true
}
