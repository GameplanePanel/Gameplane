// Package handlers implements the REST surface of the API. The design
// deliberately sticks close to the Kubernetes API shape — the dashboard
// talks to the API, the API to the dynamic client, and the dynamic
// client to Gameplane CRDs. No intermediate DTOs.
package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"reflect"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"

	"github.com/GameplanePanel/gameplane/api/internal/auth"
	"github.com/GameplanePanel/gameplane/api/internal/db"
	"github.com/GameplanePanel/gameplane/api/internal/httperr"
	"github.com/GameplanePanel/gameplane/api/internal/kube"
	"github.com/GameplanePanel/gameplane/api/internal/messages"
	"github.com/GameplanePanel/gameplane/api/internal/scope"
)

// heartbeatStaleTTL is how long the agent's reported status.agent values
// (playersOnline, gameVersion) are trusted. It matches the operator's
// heartbeatFreshness window. Once a heartbeat is older than this, the
// counts are no longer current — a crashed agent would otherwise show
// its last player count forever — so the API blanks playersOnline and
// flags status.agent.stale on read.
const heartbeatStaleTTL = 60 * time.Second

// MountResources wires /servers, /templates, /backups, /schedules.
// store may be nil (tests); when set, deleting a GameServer revokes its share
// links so they cannot attach to a later server with the same name.
//
// Templates are cluster-scoped; the others are namespaced. The kube
// package's GVR map determines which is which via resource path. Every
// handler resolves its target cluster per request from reg via the
// `?cluster=` selector (resolveCluster) — a request with no selector
// resolves to scope.DefaultCluster, preserving single-cluster behavior.
func MountResources(r chi.Router, reg *kube.Registry, store *db.Store) {
	for path, gvr := range kube.GVRs {
		mountOne(r, reg, store, path, gvr)
	}
}

func mountOne(r chi.Router, reg *kube.Registry, store *db.Store, path string, gvr schema.GroupVersionResource) {
	r.Route("/"+path, func(r chi.Router) {
		r.Get("/", listHandler(reg, gvr))
		r.Post("/", createHandler(reg, gvr))
		r.Get("/{name}", getHandler(reg, gvr))
		r.Put("/{name}", updateHandler(reg, gvr))
		r.Delete("/{name}", deleteHandler(reg, store, gvr))
	})
}

// resolveNS validates the namespace query parameter and writes a 403
// response when the caller is not permitted to use it. ok=false means
// the caller should stop processing and return.
func resolveNS(w http.ResponseWriter, req *http.Request) (string, bool) {
	ns, err := scope.Resolve(req)
	if err != nil {
		httperr.Write(w, req, err)
		return "", false
	}
	return ns, true
}

// resolveCluster validates the ?cluster= query param and returns the target
// cluster's client. ok=false means a response was already written; stop.
func resolveCluster(w http.ResponseWriter, req *http.Request, reg *kube.Registry) (*kube.Client, bool) {
	id, err := scope.ResolveCluster(req, reg)
	if err != nil {
		httperr.Write(w, req, err)
		return nil, false
	}
	c, ok := reg.Get(id)
	if !ok {
		httperr.Write(w, req, scope.ErrForbiddenCluster)
		return nil, false
	}
	return c, true
}

// rejectRemoteCluster answers 501 Not Implemented for a request carrying a
// non-local `?cluster=` selector, for handlers that hold a bare *kube.Client
// (the LOCAL/home cluster only) instead of a cluster-dispatch *kube.Registry —
// MountModIDs, MountModUpdates and MountRegistry. These routes serve only the
// home cluster until a cross-cluster agent exists to serve them on a remote
// cluster, so 501 (with a readable reason) is the honest answer.
//
// The guard must also stay in place for correctness: rbac.Middleware
// (api/internal/rbac/rbac.go) authorizes namespaced permissions against
// whatever `?cluster=` the caller supplies, so without it a user bound only
// to a registered REMOTE cluster could pass `?cluster=<remote>` to satisfy
// that check while still reaching the LOCAL cluster's same-named GameServer
// here. See ws.rejectRemoteCluster for the WS-side twin of this guard.
// Returns true if the request was rejected (caller must stop processing).
func rejectRemoteCluster(w http.ResponseWriter, req *http.Request) bool {
	if isRemoteCluster(req) {
		httperr.WriteRemoteClusterNotImplemented(w)
		return true
	}
	return false
}

// isRemoteCluster reports whether req carries a `?cluster=` selector naming
// a cluster other than the home cluster (scope.DefaultCluster). An absent or
// blank selector means the home cluster.
func isRemoteCluster(req *http.Request) bool {
	c := strings.TrimSpace(req.URL.Query().Get("cluster"))
	return c != "" && c != scope.DefaultCluster
}

func listHandler(reg *kube.Registry, gvr schema.GroupVersionResource) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		k, ok := resolveCluster(w, req, reg)
		if !ok {
			return
		}
		var (
			list *unstructured.UnstructuredList
			err  error
		)
		if cluster(gvr) {
			list, err = k.Dynamic.Resource(gvr).List(req.Context(), metav1.ListOptions{})
		} else {
			ns, ok := resolveNS(w, req)
			if !ok {
				return
			}
			list, err = k.Dynamic.Resource(gvr).Namespace(ns).List(req.Context(), metav1.ListOptions{})
		}
		if err == nil && list != nil && gvr.Resource == "gameservers" {
			cfgRules := newConfigRuleCache(k)
			for i := range list.Items {
				gateStaleAgent(&list.Items[i])
				cfgRules.redact(req.Context(), &list.Items[i])
			}
		}
		writeOrErr(w, req, list, err)
	}
}

func getHandler(reg *kube.Registry, gvr schema.GroupVersionResource) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		k, ok := resolveCluster(w, req, reg)
		if !ok {
			return
		}
		name := chi.URLParam(req, "name")
		var (
			obj *unstructured.Unstructured
			err error
		)
		if cluster(gvr) {
			obj, err = k.Dynamic.Resource(gvr).Get(req.Context(), name, metav1.GetOptions{})
		} else {
			ns, ok := resolveNS(w, req)
			if !ok {
				return
			}
			if gvr.Resource == "gameservers" {
				obj, ok = authorizedServer(w, req, k, ns, name)
				if !ok {
					return
				}
			} else {
				obj, err = k.Dynamic.Resource(gvr).Namespace(ns).Get(req.Context(), name, metav1.GetOptions{})
			}
		}
		if err == nil && obj != nil && gvr.Resource == "gameservers" {
			gateStaleAgent(obj)
			newConfigRuleCache(k).redact(req.Context(), obj)
		}
		writeOrErr(w, req, obj, err)
	}
}

func createHandler(reg *kube.Registry, gvr schema.GroupVersionResource) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		k, ok := resolveCluster(w, req, reg)
		if !ok {
			return
		}
		obj, err := decode(req.Body)
		if err != nil {
			httperr.Write(w, req, err)
			return
		}
		// Record the creating user as the server's owner (informational).
		if gvr.Resource == "gameservers" {
			protectServerAnnotations(obj, nil)
			stampOwner(obj, req)
			stripRedactedConfig(obj)
		}
		var created *unstructured.Unstructured
		if cluster(gvr) {
			created, err = k.Dynamic.Resource(gvr).Create(req.Context(), obj, metav1.CreateOptions{})
		} else {
			ns, ok := resolveNS(w, req)
			if !ok {
				return
			}
			obj.SetNamespace(ns)
			if gvr.Resource == "gameservers" {
				cl := scope.RequestedCluster(req)
				if err := validateAndProtectGameServer(req.Context(), k, cl, ns, obj.GetName(), obj, nil); err != nil {
					writeServerValidationError(w, req, err)
					return
				}
			}
			created, err = k.Dynamic.Resource(gvr).Namespace(ns).Create(req.Context(), obj, metav1.CreateOptions{})
		}
		if err != nil {
			httperr.Write(w, req, err)
			return
		}
		if gvr.Resource == "gameservers" {
			created = created.DeepCopy()
			newConfigRuleCache(k).redact(req.Context(), created)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(created)
	}
}

func updateHandler(reg *kube.Registry, gvr schema.GroupVersionResource) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		k, ok := resolveCluster(w, req, reg)
		if !ok {
			return
		}
		obj, err := decode(req.Body)
		if err != nil {
			httperr.Write(w, req, err)
			return
		}
		name := chi.URLParam(req, "name")
		obj.SetName(name)
		if gvr.Resource == "gametemplates" {
			if blocked, err := managedTemplateBlocked(req, k, name); err != nil {
				httperr.Write(w, req, err)
				return
			} else if blocked != "" {
				httperr.WriteCode(w, req, http.StatusConflict, errors.New(blocked))
				return
			}
		}
		// For GameServers, preserve ownership annotations from the live object
		// so clients can't mutate them via PUT, and validate sensitive spec fields.
		// live is also consulted after a successful Update below, to detect a
		// spec.networking.tunnel.provider switch and prune the superseded
		// provider's credential — so it is declared outside this block.
		var live *unstructured.Unstructured
		var cfgRules *configRuleCache
		if gvr.Resource == "gameservers" {
			ns, ok := resolveNS(w, req)
			if !ok {
				return
			}
			var getErr error
			live, getErr = k.Dynamic.Resource(gvr).Namespace(ns).
				Get(req.Context(), name, metav1.GetOptions{})
			if getErr != nil && !apierrors.IsNotFound(getErr) {
				httperr.Write(w, req, getErr)
				return
			}
			protectServerAnnotations(obj, live)
			// The dashboard sends spec back wholesale, so password config values
			// arrive as the redaction marker: keep the stored value for those.
			cfgRules = newConfigRuleCache(k)
			restoreRedactedConfig(req.Context(), cfgRules, obj, live)
			cl := scope.RequestedCluster(req)
			if err := validateAndProtectGameServer(req.Context(), k, cl, ns, name, obj, live); err != nil {
				writeServerValidationError(w, req, err)
				return
			}
		}
		var updated *unstructured.Unstructured
		if cluster(gvr) {
			updated, err = k.Dynamic.Resource(gvr).Update(req.Context(), obj, metav1.UpdateOptions{})
		} else {
			ns, ok := resolveNS(w, req)
			if !ok {
				return
			}
			obj.SetNamespace(ns)
			updated, err = k.Dynamic.Resource(gvr).Namespace(ns).Update(req.Context(), obj, metav1.UpdateOptions{})
			if err == nil && gvr.Resource == "gameservers" {
				pruneTunnelProviderOnSpecChange(req.Context(), k, ns, name, live, updated)
				updated = updated.DeepCopy()
				cfgRules.redact(req.Context(), updated)
			}
		}
		writeOrErr(w, req, updated, err)
	}
}

// pruneTunnelProviderOnSpecChange is called by updateHandler once a GameServer
// PUT has succeeded. The dashboard (web/src/routes/tabs/settings/Networking.tsx)
// saves a tunnel credential and a spec.networking.tunnel.provider switch as
// separate requests: tunnelCredsHandler.put keeps the still-active provider's
// key alive for an in-flight pod while the new credential is written, but
// nothing then prunes that superseded key once the spec switch itself lands
// here. This closes that gap: when live and updated disagree on the tunnel
// provider, every other provider's key is removed from the GameServer's
// <name>-tunnel-auth Secret via pruneStaleTunnelProviderKeys (tunnelcreds.go),
// which shares tunnelCredentialPatch with the credential PUT path.
//
// A prune failure never fails the spec update, which has already committed:
// it is logged, and the next credential PUT cleans up any leftover key too.
func pruneTunnelProviderOnSpecChange(ctx context.Context, k *kube.Client, ns, name string, live, updated *unstructured.Unstructured) {
	if live == nil || updated == nil {
		return
	}
	oldProvider, _, oldErr := getNestedString(live.Object, "spec", "networking", "tunnel", "provider")
	newProvider, _, newErr := getNestedString(updated.Object, "spec", "networking", "tunnel", "provider")
	if oldErr != nil || newErr != nil {
		slog.Warn("tunnel provider switch: could not read provider from spec; skipping stale credential prune",
			"server", name, "namespace", ns, "oldErr", oldErr, "newErr", newErr)
		return
	}
	// Removing or clearing the tunnel is not a provider switch: no tunnel pod
	// mounts the Secret then, and the credential DELETE endpoint is the
	// explicit way to remove a stored credential.
	if oldProvider == newProvider || newProvider == "" {
		return
	}
	if err := pruneStaleTunnelProviderKeys(ctx, k, ns, name, updated.GetUID(), newProvider); err != nil {
		slog.Warn("tunnel provider switch: stale credential prune failed; a subsequent credential PUT will retry it",
			"server", name, "namespace", ns, "oldProvider", oldProvider, "newProvider", newProvider, "err", err)
	}
}

func deleteHandler(reg *kube.Registry, store *db.Store, gvr schema.GroupVersionResource) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		k, ok := resolveCluster(w, req, reg)
		if !ok {
			return
		}
		name := chi.URLParam(req, "name")
		if gvr.Resource == "gametemplates" {
			if blocked, err := managedTemplateBlocked(req, k, name); err != nil {
				httperr.Write(w, req, err)
				return
			} else if blocked != "" {
				httperr.WriteCode(w, req, http.StatusConflict, errors.New(blocked))
				return
			}
		}
		var err error
		var ns string
		if cluster(gvr) {
			err = k.Dynamic.Resource(gvr).Delete(req.Context(), name, metav1.DeleteOptions{})
		} else {
			var ok bool
			ns, ok = resolveNS(w, req)
			if !ok {
				return
			}
			options := metav1.DeleteOptions{}
			if gvr == kube.GVRs["servers"] {
				obj, ok := authorizedServer(w, req, k, ns, name)
				if !ok {
					return
				}
				// Repeat the owner-only check against the same snapshot used by
				// the delete preconditions. Middleware authenticates production
				// requests; direct handler fixtures may omit the caller.
				if u := auth.UserFromContext(req.Context()); u != nil &&
					!u.Can("*", true, scope.RequestedCluster(req), ns) && !isServerOwner(obj, u.ID) {
					httperr.WriteMessage(w, req, messages.New("server.forbidden", nil))
					return
				}
				options.Preconditions = objectDeletePreconditions(obj)
			}
			err = k.Dynamic.Resource(gvr).Namespace(ns).Delete(req.Context(), name, options)
		}
		if err != nil {
			httperr.Write(w, req, err)
			return
		}
		if store != nil && gvr == kube.GVRs["servers"] {
			// The server is gone; its share links must not outlive it. A
			// failure here is logged, not surfaced: the delete already
			// happened, and the public share handlers also reject any link
			// older than the live server's creationTimestamp.
			clusterID, _ := scope.ResolveCluster(req, reg) // already validated by resolveCluster above
			if rerr := store.RevokeShareLinksForServer(req.Context(), clusterID, ns, name); rerr != nil {
				slog.Warn("revoke share links after server delete", "server", name, "namespace", ns, "err", rerr)
			}
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// managedTemplateBlocked returns a non-empty reason when the named
// GameTemplate is managed by a Module (gameplane.local/managed-by=Module).
// In that case, direct mutations via /templates are refused — the user
// must go through /modules to install/upgrade/uninstall.
func managedTemplateBlocked(req *http.Request, k *kube.Client, name string) (string, error) {
	tmpl, err := k.Dynamic.Resource(kube.GVRs["templates"]).Get(req.Context(), name, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			return "", nil // create / new — guard doesn't apply
		}
		return "", err
	}
	if tmpl.GetLabels()["gameplane.local/managed-by"] == "Module" {
		modName := tmpl.GetLabels()["gameplane.local/module-name"]
		if modName == "" {
			modName = name
		}
		return "GameTemplate \"" + name + "\" is managed by Module \"" + modName +
			"\"; mutate via /modules instead", nil
	}
	return "", nil
}

func cluster(gvr schema.GroupVersionResource) bool {
	return gvr.Resource == "gametemplates"
}

// gateStaleAgent blanks the agent-reported values on a GameServer whose
// heartbeat has gone stale (or never arrived), so the dashboard renders
// "unknown" instead of a frozen-forever value from a dead agent. It
// mutates obj's status.agent in place: drops playersOnline and the
// resource-usage readings, and sets stale=true. Fresh heartbeats are
// left untouched.
func gateStaleAgent(obj *unstructured.Unstructured) {
	agent, found, err := unstructured.NestedMap(obj.Object, "status", "agent")
	if !found || err != nil || agent == nil {
		return
	}
	fresh := false
	if lh, ok := agent["lastHeartbeat"].(string); ok && lh != "" {
		if t, perr := time.Parse(time.RFC3339, lh); perr == nil {
			fresh = time.Since(t) < heartbeatStaleTTL
		}
	}
	if fresh {
		return
	}
	agent["stale"] = true
	for _, k := range []string{
		"playersOnline",
		"cpuMillicores", "cpuLimitMillicores",
		"memoryBytes", "memoryLimitBytes",
		"diskUsedBytes", "diskTotalBytes",
	} {
		delete(agent, k)
	}
	_ = unstructured.SetNestedMap(obj.Object, agent, "status", "agent")
}

// maxBodyBytes caps request body size in JSON decoders. CRD objects
// we accept are far smaller than this; exceeding it indicates abuse.
const maxBodyBytes = 1 << 20 // 1 MiB

func decode(r io.ReadCloser) (*unstructured.Unstructured, error) {
	defer func() { _ = r.Close() }()
	dec := json.NewDecoder(io.LimitReader(r, maxBodyBytes))
	var raw map[string]any
	if err := dec.Decode(&raw); err != nil {
		return nil, err
	}
	return &unstructured.Unstructured{Object: raw}, nil
}

func writeOrErr(w http.ResponseWriter, req *http.Request, v any, err error) {
	if err != nil {
		httperr.Write(w, req, err)
		return
	}
	writeJSON(w, v)
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

// writeJSONCreated sets the JSON Content-Type header before writing a 201
// Created status code. net/http snapshots response headers at WriteHeader
// time, so Content-Type must be set first — calling WriteHeader directly
// and then writeJSON leaves the body sniffed as text/plain.
func writeJSONCreated(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(v)
}

// errTemplateRefImmutable is returned by validateAndProtectGameServer when
// an update attempts to change spec.templateRef on an existing GameServer.
// It mirrors the CRD's CEL rule (self == oldSelf on
// GameServerSpec.TemplateRef, see operator/api/v1alpha1/gameserver_types.go)
// so the handler can reject the change with a clear 409 before it ever
// reaches the API server, instead of surfacing the apiserver's 422 CEL
// validation error to the caller (F-047).
var errTemplateRefImmutable = errors.New(
	"spec.templateRef is immutable; delete and recreate the GameServer to switch templates",
)

// validateAndProtectGameServer enforces RBAC and security boundaries on
// GameServer spec fields during create and update mutations:
//   - spec.templateRef: Immutable once the GameServer exists. Changing it is
//     rejected with errTemplateRefImmutable regardless of role.
//   - spec.capture: Requires captures:manage permission. Non-admins cannot enable,
//     modify, or configure capture settings.
//   - spec.serviceAccountName: Requires admin privileges. Non-admins cannot set or override
//     the ServiceAccount.
//   - spec.image: Requires admin privileges. Non-admins choose versions from GameTemplate
//     catalog versions instead of supplying arbitrary container images.
//   - spec.env: Non-admins cannot reference arbitrary Secrets (such as backup destination
//     credentials or auth secrets) via valueFrom.secretKeyRef. Secret references are permitted
//     only to server-owned Secrets (e.g. matching OwnerReference, gameplane.local/server-name
//     label, or canonical server credential names).
func validateAndProtectGameServer(
	ctx context.Context,
	k *kube.Client,
	cl, ns, name string,
	desired *unstructured.Unstructured,
	live *unstructured.Unstructured,
) error {
	u := auth.UserFromContext(ctx)
	isAdmin := false
	canManageCaptures := false
	if u != nil {
		isAdmin = u.Role == "admin" || u.Can("*", false, cl, ns)
		canManageCaptures = isAdmin || u.Can("captures:manage", true, cl, ns)
	}

	// 0. Validate spec.templateRef is unchanged (immutable once created).
	if live != nil {
		desiredRef, _, _ := unstructured.NestedString(desired.Object, "spec", "templateRef", "name")
		liveRef, _, _ := unstructured.NestedString(live.Object, "spec", "templateRef", "name")
		if desiredRef != liveRef {
			return errTemplateRefImmutable
		}
	}
	if err := validateSnapshotDataSource(ctx, k, cl, ns, desired, live); err != nil {
		return err
	}

	// 1. Validate spec.capture
	captureRaw, hasCapture, _ := unstructured.NestedFieldNoCopy(desired.Object, "spec", "capture")
	if !canManageCaptures {
		if live == nil {
			if hasCapture && captureRaw != nil {
				return errors.New("configuring capture requires captures:manage permission")
			}
		} else {
			liveCaptureRaw, liveHasCapture, _ := unstructured.NestedFieldNoCopy(live.Object, "spec", "capture")
			if hasCapture && captureRaw != nil {
				if !liveHasCapture || !reflect.DeepEqual(captureRaw, liveCaptureRaw) {
					return errors.New("modifying capture settings requires captures:manage permission")
				}
			} else if liveHasCapture && liveCaptureRaw != nil {
				_ = unstructured.SetNestedField(desired.Object, liveCaptureRaw, "spec", "capture")
			}
		}
	}

	// 2. Validate spec.serviceAccountName
	// A host-capable diagnostic exception must be a separate, synchronously
	// audited administrator action. Generic edits may preserve an existing
	// opt-in, but cannot grant/revoke it or inherit it on a clone.
	nodeDiagnostic, hasNodeDiagnostic, err := unstructured.NestedBool(desired.Object, "spec", "capture", "nodeDiagnostic")
	if err != nil {
		return errors.New("spec.capture.nodeDiagnostic must be a boolean")
	}
	liveNodeDiagnostic := false
	if live != nil {
		liveNodeDiagnostic, _, _ = unstructured.NestedBool(live.Object, "spec", "capture", "nodeDiagnostic")
		if !hasNodeDiagnostic && liveNodeDiagnostic {
			if err := unstructured.SetNestedField(desired.Object, true, "spec", "capture", "nodeDiagnostic"); err != nil {
				return fmt.Errorf("preserve node diagnostic exception: %w", err)
			}
			nodeDiagnostic = true
		}
	}
	if nodeDiagnostic != liveNodeDiagnostic {
		return errors.New("change the node diagnostic exception through the administrator capture-node-enable/capture-node-disable action")
	}

	sa, hasSA, _ := unstructured.NestedString(desired.Object, "spec", "serviceAccountName")
	if !isAdmin {
		if live == nil {
			if hasSA && sa != "" {
				return errors.New("spec.serviceAccountName override requires admin privileges")
			}
		} else {
			liveSA, liveHasSA, _ := unstructured.NestedString(live.Object, "spec", "serviceAccountName")
			if hasSA && sa != "" {
				if !liveHasSA || sa != liveSA {
					return errors.New("spec.serviceAccountName override requires admin privileges")
				}
			} else if liveHasSA && liveSA != "" {
				_ = unstructured.SetNestedField(desired.Object, liveSA, "spec", "serviceAccountName")
			}
		}
	}

	// 3. Validate spec.image
	img, hasImg, _ := unstructured.NestedString(desired.Object, "spec", "image")
	if !isAdmin {
		if live == nil {
			if hasImg && img != "" {
				return errors.New("spec.image override requires admin privileges")
			}
		} else {
			liveImg, liveHasImg, _ := unstructured.NestedString(live.Object, "spec", "image")
			if hasImg && img != "" {
				if !liveHasImg || img != liveImg {
					return errors.New("spec.image override requires admin privileges")
				}
			} else if liveHasImg && liveImg != "" {
				_ = unstructured.SetNestedField(desired.Object, liveImg, "spec", "image")
			}
		}
	}

	// 4. Validate spec.env for unowned secret/configmap references
	envSlice, hasEnv, _ := unstructured.NestedSlice(desired.Object, "spec", "env")
	if hasEnv {
		for _, item := range envSlice {
			itemMap, ok := item.(map[string]any)
			if !ok {
				continue
			}
			valFromRaw, ok := itemMap["valueFrom"]
			if !ok || valFromRaw == nil {
				continue
			}
			valFrom, ok := valFromRaw.(map[string]any)
			if !ok {
				continue
			}
			if secRefRaw, ok := valFrom["secretKeyRef"]; ok && secRefRaw != nil {
				if secRef, ok := secRefRaw.(map[string]any); ok {
					secName, _ := secRef["name"].(string)
					if secName == "" {
						return errors.New("secretKeyRef name must not be empty")
					}
					var gsUID types.UID
					if live != nil {
						gsUID = live.GetUID()
					}
					if !isServerOwnedSecret(ctx, k, ns, name, gsUID, secName) {
						return fmt.Errorf("secret reference %q in env is not permitted: not a server-owned secret", secName)
					}
				}
			}
			if cmRefRaw, ok := valFrom["configMapKeyRef"]; ok && cmRefRaw != nil {
				if cmRef, ok := cmRefRaw.(map[string]any); ok {
					cmName, _ := cmRef["name"].(string)
					if cmName == "" {
						return errors.New("configMapKeyRef name must not be empty")
					}
					var gsUID types.UID
					if live != nil {
						gsUID = live.GetUID()
					}
					if !isServerOwnedConfigMap(ctx, k, ns, name, gsUID, cmName) {
						return fmt.Errorf("configMap reference %q in env is not permitted: not a server-owned configMap", cmName)
					}
				}
			}
		}
	}

	// 5. Validate spec.networking.tunnel.credentialsSecretRef for unowned secret references
	tunnelSecName, hasTunnelSec, _ := unstructured.NestedString(desired.Object, "spec", "networking", "tunnel", "credentialsSecretRef", "name")
	if hasTunnelSec && tunnelSecName != "" {
		var gsUID types.UID
		if live != nil {
			gsUID = live.GetUID()
		}
		if !isServerOwnedSecret(ctx, k, ns, name, gsUID, tunnelSecName) {
			return fmt.Errorf("tunnel credentials secret reference %q is not permitted: not a server-owned secret", tunnelSecName)
		}
	}

	// Validate tunnel config fields: serverAddr/name are escaped at render time, but pattern validation
	// ensures they parse as valid before reaching the pod (defense in depth).
	if tunnelFrp, ok, _ := unstructured.NestedMap(desired.Object, "spec", "networking", "tunnel", "frp"); ok && tunnelFrp != nil {
		// CRD Pattern already enforces these; defensive double-check.
		if serverAddr, ok := tunnelFrp["serverAddr"].(string); ok && serverAddr == "" {
			return fmt.Errorf("spec.networking.tunnel.frp.serverAddr must not be empty")
		}
		if remotePorts, ok := tunnelFrp["remotePorts"].([]interface{}); ok {
			for i, port := range remotePorts {
				if portMap, ok := port.(map[string]interface{}); ok {
					if portName, ok := portMap["name"].(string); ok && portName == "" {
						return fmt.Errorf("spec.networking.tunnel.frp.remotePorts[%d].name must not be empty", i)
					}
				}
			}
		}
	}

	return nil
}

// isServerOwnedSecret reports whether the given Secret belongs to the named GameServer
// in the specified namespace by inspecting the Secret's OwnerReferences.
// An OwnerReference specifying a non-empty UID must match the live GameServer UID;
// if the GameServer UID is unknown (e.g. during pre-creation validation), an OwnerReference
// that already specifies a concrete UID cannot match and is rejected.
func isServerOwnedSecret(ctx context.Context, k *kube.Client, ns, serverName string, gsUID types.UID, secretName string) bool {
	if secretName == "" || serverName == "" {
		return false
	}
	if k != nil && k.Dynamic != nil {
		gvr := schema.GroupVersionResource{Version: "v1", Resource: "secrets"}
		sec, err := k.Dynamic.Resource(gvr).Namespace(ns).Get(ctx, secretName, metav1.GetOptions{})
		if err == nil && sec != nil {
			for _, ref := range sec.GetOwnerReferences() {
				if ref.Kind == "GameServer" && ref.Name == serverName {
					// A create or clone has no live UID yet. An owner reference that
					// names a specific GameServer UID must not be accepted on a name match alone.
					if ref.UID != "" && ref.UID != gsUID {
						continue
					}
					return true
				}
			}
			return false
		}
	}
	return false
}

// isServerOwnedConfigMap reports whether the given ConfigMap belongs to the named GameServer
// in the specified namespace by inspecting the ConfigMap's OwnerReferences.
// An OwnerReference specifying a non-empty UID must match the live GameServer UID;
// if the GameServer UID is unknown (e.g. during pre-creation validation), an OwnerReference
// that already specifies a concrete UID cannot match and is rejected.
func isServerOwnedConfigMap(ctx context.Context, k *kube.Client, ns, serverName string, gsUID types.UID, cmName string) bool {
	if cmName == "" || serverName == "" {
		return false
	}
	if k != nil && k.Dynamic != nil {
		gvr := schema.GroupVersionResource{Version: "v1", Resource: "configmaps"}
		cm, err := k.Dynamic.Resource(gvr).Namespace(ns).Get(ctx, cmName, metav1.GetOptions{})
		if err == nil && cm != nil {
			for _, ref := range cm.GetOwnerReferences() {
				if ref.Kind == "GameServer" && ref.Name == serverName {
					// A create or clone has no live UID yet. An owner reference that
					// names a specific GameServer UID must not be accepted on a name match alone.
					if ref.UID != "" && ref.UID != gsUID {
						continue
					}
					return true
				}
			}
			return false
		}
	}
	return false
}
