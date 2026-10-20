package handlers

import (
	"encoding/json"
	"net/http"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"

	"github.com/GameplanePanel/gameplane/api/internal/httperr"
	"github.com/GameplanePanel/gameplane/api/internal/kube"
	"github.com/GameplanePanel/gameplane/api/internal/rbac"
	"github.com/GameplanePanel/gameplane/api/internal/scope"
)

// authorizedServer refuses a replacement of the resource that granted the
// request's owner/collaborator access. Namespace-wide grants remain unbound.
func authorizedServer(w http.ResponseWriter, req *http.Request, k *kube.Client, ns, name string) (*unstructured.Unstructured, bool) {
	obj, err := k.GetServer(req.Context(), ns, name)
	if err != nil {
		httperr.Write(w, req, err)
		return nil, false
	}
	if err := rbac.ValidateServerIdentity(req.Context(), scope.RequestedCluster(req), ns, name, string(obj.GetUID())); err != nil {
		httperr.Write(w, req, apierrors.NewNotFound(kube.GVRs["servers"].GroupResource(), name))
		return nil, false
	}
	return obj, true
}

// conditionObjectPatch pins a merge patch to the object that was checked.
// Kubernetes rejects a changed resourceVersion or an immutable UID mismatch.
// Real objects carry both fields; omissions support existing in-memory fixtures.
func conditionObjectPatch(patch map[string]any, obj metav1.Object) map[string]any {
	md, _ := patch["metadata"].(map[string]any)
	if md == nil {
		md = map[string]any{}
		patch["metadata"] = md
	}
	if uid := obj.GetUID(); uid != "" {
		md["uid"] = string(uid)
	}
	if rv := obj.GetResourceVersion(); rv != "" {
		md["resourceVersion"] = rv
	}
	return patch
}

// patchAuthorizedServer does not retry conflicts: a fresh request must pass
// authorization again before acting on a newer server snapshot.
func patchAuthorizedServer(w http.ResponseWriter, req *http.Request, k *kube.Client, obj *unstructured.Unstructured, patch map[string]any) bool {
	body, err := json.Marshal(conditionObjectPatch(patch, obj))
	if err == nil {
		_, err = k.Dynamic.Resource(kube.GVRs["servers"]).Namespace(obj.GetNamespace()).
			Patch(req.Context(), obj.GetName(), types.MergePatchType, body, metav1.PatchOptions{})
	}
	if err != nil {
		httperr.Write(w, req, err)
		return false
	}
	return true
}

func objectDeletePreconditions(obj metav1.Object) *metav1.Preconditions {
	p := &metav1.Preconditions{}
	if uid := obj.GetUID(); uid != "" {
		p.UID = &uid
	}
	if rv := obj.GetResourceVersion(); rv != "" {
		p.ResourceVersion = &rv
	}
	return p
}
