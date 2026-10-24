package handlers

import (
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/GameplanePanel/gameplane/api/internal/httperr"
	"github.com/GameplanePanel/gameplane/api/internal/kube"
	"github.com/GameplanePanel/gameplane/api/internal/messages"
	"github.com/GameplanePanel/gameplane/api/internal/scope"
)

// wipeRequestedAnnotation matches the operator's
// controller.WipeRequestedAnnotation (a different, internal package, so the
// string is duplicated here deliberately).
const wipeRequestedAnnotation = "gameplane.local/wipe-data-requested"

// restartRequestedAnnotation matches the operator's
// controller.RestartRequestedAnnotation (a different, internal package, so the
// string is duplicated here deliberately).
const restartRequestedAnnotation = "gameplane.local/restart-requested"

// idleWakeRequestedAnnotation matches the operator's
// controller.IdleWakeRequestedAnnotation (duplicated for the same reason).
const idleWakeRequestedAnnotation = "gameplane.local/idle-wake-requested"

// MountLifecycle wires start/stop/restart/wake/clone verbs on GameServers.
//
// start/stop are expressed as patches to spec.suspend (the operator
// reconciles the rest). Restart and wake are single stamped annotations the
// operator acks: it owns the whole transition, so the request can't be
// lost the way a client-issued suspend/resume patch pair can (rule 10).
func MountLifecycle(r chi.Router, reg *kube.Registry) {
	r.Post("/servers/{name}:start", patchSuspend(reg, false))
	r.Post("/servers/{name}:stop", patchSuspend(reg, true))
	r.Post("/servers/{name}:restart", restartHandler(reg))
	r.Post("/servers/{name}:wake", wakeHandler(reg))
	r.Post("/servers/{name}:clone", cloneHandler(reg))
	r.Post("/servers/{name}:wipe-data", wipeDataHandler(reg))
}

type wipeDataReq struct {
	Confirm string `json:"confirm"`
}

// wipeDataHandler suspends the server and stamps a wipe-request annotation;
// the operator runs a one-shot Job that empties the data PVC while the pod
// is down, then acks. Destructive, so it requires the server name typed
// back as confirmation.
func wipeDataHandler(reg *kube.Registry) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		k, ok := resolveCluster(w, req, reg)
		if !ok {
			return
		}
		name := chi.URLParam(req, "name")
		ns, ok := resolveNS(w, req)
		if !ok {
			return
		}
		obj, ok := requireOwnerOrAdmin(w, req, reg, k, ns, name)
		if !ok {
			return
		}
		var body wipeDataReq
		if err := json.NewDecoder(io.LimitReader(req.Body, 1<<16)).Decode(&body); err != nil {
			httperr.WriteMessage(w, req, messages.New("server.bad_request", nil))
			return
		}
		if body.Confirm != name {
			httperr.WriteMessage(w, req, messages.New("server.lifecycle.confirmation_mismatch", nil))
			return
		}
		token := strconv.FormatInt(time.Now().UTC().UnixNano(), 10)
		// Conditional on the server the ownership check read, so a caller
		// who loses ownership before the patch lands is refused.
		if !patchServerAsOwner(w, req, reg, k, ns, name, obj, func(*unstructured.Unstructured) map[string]any {
			return map[string]any{
				"metadata": map[string]any{
					"annotations": map[string]any{wipeRequestedAnnotation: token},
				},
				"spec": map[string]any{"suspend": true},
			}
		}) {
			return
		}
		w.WriteHeader(http.StatusAccepted)
	}
}

func patchSuspend(reg *kube.Registry, suspend bool) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		k, ok := resolveCluster(w, req, reg)
		if !ok {
			return
		}
		name := chi.URLParam(req, "name")
		ns, ok := resolveNS(w, req)
		if !ok {
			return
		}
		obj, ok := authorizedServer(w, req, k, ns, name)
		if !ok {
			return
		}
		if err := patchServerSuspend(req.Context(), k, obj, suspend); err != nil {
			httperr.Write(w, req, err)
			return
		}
		w.WriteHeader(http.StatusAccepted)
	}
}

// restartHandler stamps a restart-request annotation and returns. The operator
// owns the recycle: it drains the pod gracefully (module stop sequence), waits
// until the pod is confirmed gone, then brings a fresh one up and acks the
// token. Because the token persists on the object until acked, a restart can't
// be lost to a coalesced reconcile the way the old suspend→wait→resume patch
// pair could.
func restartHandler(reg *kube.Registry) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		k, ok := resolveCluster(w, req, reg)
		if !ok {
			return
		}
		name := chi.URLParam(req, "name")
		ns, ok := resolveNS(w, req)
		if !ok {
			return
		}
		obj, ok := authorizedServer(w, req, k, ns, name)
		if !ok {
			return
		}
		token := strconv.FormatInt(time.Now().UTC().UnixNano(), 10)
		if !patchAuthorizedServer(w, req, k, obj, map[string]any{
			"metadata": map[string]any{
				"annotations": map[string]any{restartRequestedAnnotation: token},
			},
		}) {
			return
		}
		w.WriteHeader(http.StatusAccepted)
	}
}

// wakeHandler stamps a wake-request annotation on a server the idle policy put
// to sleep. The operator owns the transition — it clears its own sleep marker,
// hands the server a full fresh idle period so it doesn't immediately sleep
// again, and acks the token.
//
// Deliberately not a patch of spec.suspend: that field is the user's own power
// switch and the operator never writes it, so clearing it here would both lie
// about intent and fail to touch the marker that actually keeps the server
// down. Waking a server that isn't asleep is a harmless no-op, so this needs no
// idle-state precondition check. The server identity is still checked and the
// mutation is conditional on the snapshot that was read.
func wakeHandler(reg *kube.Registry) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		k, ok := resolveCluster(w, req, reg)
		if !ok {
			return
		}
		name := chi.URLParam(req, "name")
		ns, ok := resolveNS(w, req)
		if !ok {
			return
		}
		obj, ok := authorizedServer(w, req, k, ns, name)
		if !ok {
			return
		}
		token := strconv.FormatInt(time.Now().UTC().UnixNano(), 10)
		if !patchAuthorizedServer(w, req, k, obj, map[string]any{
			"metadata": map[string]any{
				"annotations": map[string]any{idleWakeRequestedAnnotation: token},
			},
		}) {
			return
		}
		w.WriteHeader(http.StatusAccepted)
	}
}

type cloneReq struct {
	NewName string `json:"newName"`
}

func cloneHandler(reg *kube.Registry) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		k, ok := resolveCluster(w, req, reg)
		if !ok {
			return
		}
		name := chi.URLParam(req, "name")
		ns, ok := resolveNS(w, req)
		if !ok {
			return
		}
		var body cloneReq
		if err := json.NewDecoder(io.LimitReader(req.Body, 1<<16)).Decode(&body); err != nil || body.NewName == "" {
			httperr.WriteMessage(w, req, messages.New("server.lifecycle.new_name_required", nil))
			return
		}
		src, ok := authorizedServer(w, req, k, ns, name)
		if !ok {
			return
		}
		clone := src.DeepCopy()
		clone.SetName(body.NewName)
		clone.SetResourceVersion("")
		clone.SetUID("")
		unstructured.RemoveNestedField(clone.Object, "status")
		// A clone is a brand-new object, not a copy of the source's
		// server-managed metadata. Drop everything the apiserver/operator
		// owns so the source's lifecycle/request annotations, finalizers,
		// ownerReferences, and managedFields don't leak into the clone, then
		// stamp the caller as owner (mirrors createHandler) so ownership and
		// audit reflect who cloned rather than who owns the source.
		clone.SetAnnotations(nil)
		clone.SetOwnerReferences(nil)
		clone.SetFinalizers(nil)
		clone.SetManagedFields(nil)
		clone.SetCreationTimestamp(metav1.Time{})
		// A node diagnostic exception belongs to one server identity. Cloning
		// never grants host-level diagnostic privileges to the new server.
		unstructured.RemoveNestedField(clone.Object, "spec", "capture", "nodeDiagnostic")
		stampOwner(clone, req)

		cl := req.URL.Query().Get("cluster")
		if cl == "" {
			cl = scope.DefaultCluster
		}
		if err := validateAndProtectGameServer(req.Context(), k, cl, ns, body.NewName, clone, nil); err != nil {
			httperr.WriteCode(w, req, http.StatusForbidden, err)
			return
		}

		created, err := k.Dynamic.Resource(kube.GVRs["servers"]).
			Namespace(ns).
			Create(req.Context(), clone, metav1.CreateOptions{})
		if err != nil {
			httperr.Write(w, req, err)
			return
		}
		// The clone stores the source's config verbatim; only the response is redacted.
		created = created.DeepCopy()
		newConfigRuleCache(k).redact(req.Context(), created)
		writeOrErr(w, req, created, nil)
	}
}
