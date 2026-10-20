package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	"github.com/GameplanePanel/gameplane/api/internal/httperr"
	"github.com/GameplanePanel/gameplane/api/internal/kube"
	"github.com/GameplanePanel/gameplane/api/internal/messages"
)

// Provider key names as documented in docs/tunnels.md.
var tunnelProviderKeys = map[string][]string{
	"frp":       {"token"},
	"tailscale": {"authKey"},
	"playit":    {"secretKey"},
}

// tunnelProviderOrder fixes the order in which providers are matched
// against a Secret's keys, so reads never depend on map iteration order.
// It must list every key of tunnelProviderKeys.
var tunnelProviderOrder = []string{"frp", "tailscale", "playit"}

// MountTunnelCredentials wires tunnel credential management endpoints.
// Routes use the :tunnel-credentials verb so parseServerPath correctly
// extracts the server name and verb, and RBAC checks servers:write.
func MountTunnelCredentials(r chi.Router, reg *kube.Registry) {
	h := &tunnelCredsHandler{reg: reg}
	r.Put("/servers/{name}:tunnel-credentials", h.put)
	r.Get("/servers/{name}:tunnel-credentials", h.get)
	r.Delete("/servers/{name}:tunnel-credentials", h.delete)
}

type tunnelCredsHandler struct {
	reg *kube.Registry
}

// putReq is the request body for PUT /servers/{name}:tunnel-credentials.
type putReq struct {
	Provider string            `json:"provider"`
	Values   map[string]string `json:"values"`
}

// getResp is the response body for GET /servers/{name}:tunnel-credentials.
type getResp struct {
	Configured bool     `json:"configured"`
	SecretName string   `json:"secretName"`
	Keys       []string `json:"keys"`
}

// put creates or updates a tunnel credential Secret and patches the GameServer
// to reference it. The Secret is named <server>-tunnel-auth and is owned by
// the GameServer so it is garbage-collected when the server is deleted.
func (h *tunnelCredsHandler) put(w http.ResponseWriter, req *http.Request) {
	k, ok := resolveCluster(w, req, h.reg)
	if !ok {
		return
	}
	name := chi.URLParam(req, "name")
	ns, ok := resolveNS(w, req)
	if !ok {
		return
	}

	var body putReq
	if err := json.NewDecoder(io.LimitReader(req.Body, 1<<16)).Decode(&body); err != nil {
		httperr.WriteMessage(w, req, messages.New("server.tunnel.invalid_request_body", nil))
		return
	}

	// Validate provider is known.
	expectedKeys, ok := tunnelProviderKeys[body.Provider]
	if !ok {
		httperr.WriteCode(w, req, http.StatusBadRequest, fmt.Errorf("unknown provider: %s", body.Provider))
		return
	}

	// Validate values match provider's expected keys.
	if err := validateTunnelValues(body.Values, expectedKeys); err != nil {
		httperr.WriteCode(w, req, http.StatusBadRequest, err)
		return
	}

	// Check the authorized GameServer before accessing its credentials.
	gs, ok := authorizedServer(w, req, k, ns, name)
	if !ok {
		return
	}

	// Create or update the Secret with the credential values.
	secretName := name + "-tunnel-auth"
	desired := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      secretName,
			Namespace: ns,
			OwnerReferences: []metav1.OwnerReference{
				{
					APIVersion: "gameplane.local/v1alpha1",
					Kind:       "GameServer",
					Name:       name,
					UID:        gs.GetUID(),
				},
			},
		},
		StringData: body.Values,
	}

	// Try create first; on AlreadyExists, fall through to a JSON-merge patch.
	// This preserves any extra fields an admin may have set via kubectl and
	// pins updates to the Secret whose ownership was checked below.
	_, err := k.Typed.CoreV1().Secrets(ns).Create(req.Context(), desired, metav1.CreateOptions{})
	if err != nil && !apierrors.IsAlreadyExists(err) {
		httperr.Write(w, req, err)
		return
	}
	if err != nil {
		// Secret already exists; verify ownership before patching to prevent
		// modifying Secrets not owned by this server.
		existing, getErr := k.Typed.CoreV1().Secrets(ns).Get(req.Context(), secretName, metav1.GetOptions{})
		if getErr != nil {
			httperr.Write(w, req, getErr)
			return
		}
		if !isServerOwnedSecretObject(existing, name, gs.GetUID()) {
			httperr.WriteMessage(w, req, messages.New("server.tunnel.secret_not_owned_conflict", nil))
			return
		}

		// Patch it instead to preserve extra fields. The same patch removes
		// the keys of every stale tunnel provider, so the Secret only ever
		// holds the credential of the provider just set plus the credential
		// of the provider the GameServer spec still names as active — the
		// dashboard saves credentials and spec.networking.tunnel.provider in
		// separate requests, so an in-flight pod for the still-active
		// provider must keep being able to read its key until reconciliation
		// completes the switch.
		activeProvider, _, apErr := getNestedString(gs.Object, "spec", "networking", "tunnel", "provider")
		if apErr != nil {
			httperr.Write(w, req, apErr)
			return
		}
		patchBytes, mErr := json.Marshal(conditionObjectPatch(tunnelCredentialPatch(body.Provider, activeProvider, body.Values), existing))
		if mErr != nil {
			httperr.Write(w, req, mErr)
			return
		}
		_, err = k.Typed.CoreV1().Secrets(ns).Patch(
			req.Context(), secretName, types.MergePatchType, patchBytes, metav1.PatchOptions{},
		)
		if err != nil {
			httperr.Write(w, req, err)
			return
		}
	}

	// Patch the GameServer to set the credentialsSecretRef.
	if !patchAuthorizedServer(w, req, k, gs, map[string]any{
		"spec": map[string]any{
			"networking": map[string]any{
				"tunnel": map[string]any{
					"credentialsSecretRef": map[string]any{
						"name": secretName,
					},
				},
			},
		},
	}) {
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// get reports whether tunnel credentials are configured, returning only the
// secret name and expected keys — never the credential values themselves.
func (h *tunnelCredsHandler) get(w http.ResponseWriter, req *http.Request) {
	k, ok := resolveCluster(w, req, h.reg)
	if !ok {
		return
	}
	name := chi.URLParam(req, "name")
	ns, ok := resolveNS(w, req)
	if !ok {
		return
	}

	// Check the authorized GameServer before reading its credential reference.
	gs, ok := authorizedServer(w, req, k, ns, name)
	if !ok {
		return
	}

	// Check if credentialsSecretRef is set.
	secretRef, ok, err := getNestedString(gs.Object, "spec", "networking", "tunnel", "credentialsSecretRef", "name")
	if err != nil {
		httperr.Write(w, req, err)
		return
	}

	if !ok || secretRef == "" {
		// Not configured.
		writeJSON(w, getResp{
			Configured: false,
			SecretName: "",
			Keys:       []string{},
		})
		return
	}

	// Check if the Secret exists and infer the provider from its keys.
	secret, err := k.Typed.CoreV1().Secrets(ns).Get(req.Context(), secretRef, metav1.GetOptions{})
	if err != nil {
		// Secret referenced but doesn't exist (missing or deleted).
		// Report as not configured.
		writeJSON(w, getResp{
			Configured: false,
			SecretName: secretRef,
			Keys:       []string{},
		})
		return
	}

	// Determine the provider and expected keys from the Secret's keys. The
	// provider named in the GameServer spec wins when its keys are present;
	// otherwise providers are matched in tunnelProviderOrder, so the answer
	// never depends on map iteration order.
	specProvider, _, err := getNestedString(gs.Object, "spec", "networking", "tunnel", "provider")
	if err != nil {
		httperr.Write(w, req, err)
		return
	}
	expectedKeys := tunnelKeysForSecret(secret.Data, specProvider)

	writeJSON(w, getResp{
		Configured: true,
		SecretName: secretRef,
		Keys:       expectedKeys,
	})
}

// delete removes the tunnel credential Secret and clears the credentialsSecretRef
// from the GameServer. If the tunnel is currently enabled, delete is rejected
// because the CRD's CEL rule requires credentialsSecretRef when enabled.
func (h *tunnelCredsHandler) delete(w http.ResponseWriter, req *http.Request) {
	k, ok := resolveCluster(w, req, h.reg)
	if !ok {
		return
	}
	name := chi.URLParam(req, "name")
	ns, ok := resolveNS(w, req)
	if !ok {
		return
	}

	// Check the authorized GameServer before accessing its credentials.
	gs, ok := authorizedServer(w, req, k, ns, name)
	if !ok {
		return
	}

	// Check if tunnel is enabled; if so, refuse the DELETE.
	tunnelEnabled, ok, err := getNestedBool(gs.Object, "spec", "networking", "tunnel", "enabled")
	if err != nil {
		httperr.Write(w, req, err)
		return
	}
	if ok && tunnelEnabled {
		httperr.WriteMessage(w, req, messages.New("server.tunnel.credentials_in_use", nil))
		return
	}

	secretRef, ok, err := getNestedString(gs.Object, "spec", "networking", "tunnel", "credentialsSecretRef", "name")
	if err != nil {
		httperr.Write(w, req, err)
		return
	}

	// Verify that the secret to delete is actually owned by this GameServer.
	// This prevents an attacker from referencing an arbitrary secret (e.g. backup credentials
	// or another server's secret) and deleting it via this endpoint.
	var checkedSecret *corev1.Secret
	if ok && secretRef != "" {
		existing, err := k.Typed.CoreV1().Secrets(ns).Get(req.Context(), secretRef, metav1.GetOptions{})
		if err == nil {
			if !isServerOwnedSecretObject(existing, name, gs.GetUID()) {
				httperr.WriteMessage(w, req, messages.New("server.tunnel.secret_not_owned_forbidden", nil))
				return
			}
			checkedSecret = existing
		} else if !apierrors.IsNotFound(err) {
			httperr.Write(w, req, err)
			return
		}
	}

	// Patch the GameServer to clear the credentialsSecretRef first, before deleting the Secret.
	// This ensures the spec stays valid according to the CEL rule.
	if !patchAuthorizedServer(w, req, k, gs, map[string]any{
		"spec": map[string]any{
			"networking": map[string]any{
				"tunnel": map[string]any{
					"credentialsSecretRef": nil,
				},
			},
		},
	}) {
		return
	}

	// Now delete the Secret if it exists. Report the error if deletion fails.
	// A Secret absent at the check must not be deleted if one appears later.
	if checkedSecret != nil {
		err = k.Typed.CoreV1().Secrets(ns).Delete(req.Context(), secretRef, metav1.DeleteOptions{Preconditions: objectDeletePreconditions(checkedSecret)})
		if err != nil && !apierrors.IsNotFound(err) {
			// Secret deletion failed and it wasn't just "not found"; report it.
			httperr.Write(w, req, fmt.Errorf("patched spec but failed to delete credential Secret: %w", err))
			return
		}
	}

	w.WriteHeader(http.StatusNoContent)
}

// validateTunnelValues checks that the provided values map contains exactly
// the expected keys for the provider, and that all values are non-empty.
func validateTunnelValues(values map[string]string, expectedKeys []string) error {
	if len(values) != len(expectedKeys) {
		return fmt.Errorf("expected %d key(s) for provider, got %d", len(expectedKeys), len(values))
	}

	seen := make(map[string]bool)
	for key, val := range values {
		seen[key] = true
		if val == "" {
			return fmt.Errorf("empty value for key: %s", key)
		}
	}

	for _, expected := range expectedKeys {
		if !seen[expected] {
			return fmt.Errorf("missing required key: %s", expected)
		}
	}

	return nil
}

// hasKeys checks if a map[string][]byte contains all of the specified keys.
func hasKeys(m map[string][]byte, keys []string) bool {
	for _, k := range keys {
		if _, ok := m[k]; !ok {
			return false
		}
	}
	return true
}

// tunnelCredentialPatch builds the JSON merge patch that rotates an existing
// tunnel credential Secret to provider's values. Every key that belongs to a
// stale provider — one that is neither provider nor activeProvider — is set
// to null, which a merge patch treats as a delete, in both data and
// stringData. activeProvider is the provider currently named in the
// GameServer spec: its key is kept even when provider differs, because the
// dashboard saves credentials and switches the spec's provider in separate
// requests, and a pod still running the active provider must be able to keep
// reading its credential until reconciliation completes the handoff. Keys
// that no provider uses (for example fields an admin added with kubectl) are
// left untouched.
func tunnelCredentialPatch(provider, activeProvider string, values map[string]string) map[string]any {
	stringData := make(map[string]any, len(values))
	for key, val := range values {
		stringData[key] = val
	}
	data := map[string]any{}
	for _, other := range tunnelProviderOrder {
		if other == provider || other == activeProvider {
			continue
		}
		for _, key := range tunnelProviderKeys[other] {
			if _, keep := values[key]; keep {
				continue
			}
			data[key] = nil
			stringData[key] = nil
		}
	}
	patch := map[string]any{"stringData": stringData}
	if len(data) > 0 {
		patch["data"] = data
	}
	return patch
}

// pruneStaleTunnelProviderKeys removes every provider's credential key from
// the GameServer's <name>-tunnel-auth Secret except newProvider's. Callers
// use it once a switch of spec.networking.tunnel.provider is already
// committed (unlike the credential PUT path above, which must keep the
// still-active provider's key alive during the handoff). Callers pass the
// new, non-empty provider; removing the tunnel is not a switch and must not
// call this.
//
// It is a no-op, not an error, when the Secret doesn't exist or isn't the
// one owned by this GameServer (isServerOwnedSecretObject): either means
// there is nothing here for this endpoint to manage.
func pruneStaleTunnelProviderKeys(ctx context.Context, k *kube.Client, ns, serverName string, gsUID types.UID, newProvider string) error {
	secretName := serverName + "-tunnel-auth"
	existing, err := k.Typed.CoreV1().Secrets(ns).Get(ctx, secretName, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("get tunnel-auth secret %s/%s: %w", ns, secretName, err)
	}
	if !isServerOwnedSecretObject(existing, serverName, gsUID) {
		return nil
	}

	patch := tunnelCredentialPatch(newProvider, "", nil)
	if data, _ := patch["data"].(map[string]any); len(data) == 0 {
		// Nothing stale to remove.
		return nil
	}
	patchBytes, err := json.Marshal(conditionObjectPatch(patch, existing))
	if err != nil {
		return fmt.Errorf("marshal tunnel-auth prune patch: %w", err)
	}
	if _, err := k.Typed.CoreV1().Secrets(ns).Patch(
		ctx, secretName, types.MergePatchType, patchBytes, metav1.PatchOptions{},
	); err != nil {
		return fmt.Errorf("patch tunnel-auth secret %s/%s: %w", ns, secretName, err)
	}
	return nil
}

// tunnelKeysForSecret returns the credential keys of the provider whose keys
// are all present in data: preferred (the GameServer's spec provider) first,
// then each provider in tunnelProviderOrder. It returns nil when none match.
func tunnelKeysForSecret(data map[string][]byte, preferred string) []string {
	if keys, ok := tunnelProviderKeys[preferred]; ok && hasKeys(data, keys) {
		return keys
	}
	for _, p := range tunnelProviderOrder {
		if keys := tunnelProviderKeys[p]; hasKeys(data, keys) {
			return keys
		}
	}
	return nil
}

// getNestedString retrieves a string value from a nested map using a path of keys.
// Returns (value, found, error). Error is returned if a non-final key points to
// a non-map value.
func getNestedString(obj map[string]any, path ...string) (string, bool, error) {
	var current any = obj
	for i, key := range path {
		if m, ok := current.(map[string]any); ok {
			v, found := m[key]
			if !found {
				return "", false, nil
			}
			if i == len(path)-1 {
				// Final key: convert to string.
				s, ok := v.(string)
				return s, ok, nil
			}
			current = v
		} else {
			return "", false, fmt.Errorf("expected map at %s", strings.Join(path[:i], "."))
		}
	}
	return "", false, nil
}

// getNestedBool retrieves a bool value from a nested map using a path of keys.
// Returns (value, found, error). Error is returned if a non-final key points to
// a non-map value.
func getNestedBool(obj map[string]any, path ...string) (bool, bool, error) {
	var current any = obj
	for i, key := range path {
		if m, ok := current.(map[string]any); ok {
			v, found := m[key]
			if !found {
				return false, false, nil
			}
			if i == len(path)-1 {
				// Final key: convert to bool.
				b, ok := v.(bool)
				return b, ok, nil
			}
			current = v
		} else {
			return false, false, fmt.Errorf("expected map at %s", strings.Join(path[:i], "."))
		}
	}
	return false, false, nil
}

// isServerOwnedSecretObject reports whether sec has an OwnerReference matching
// the GameServer's Kind, Name, and UID. Canonical names or labels are not accepted as proof.
func isServerOwnedSecretObject(sec *corev1.Secret, serverName string, gsUID types.UID) bool {
	if sec == nil || serverName == "" {
		return false
	}
	for _, ref := range sec.OwnerReferences {
		if ref.Kind == "GameServer" && ref.Name == serverName {
			if ref.UID != "" && ref.UID != gsUID {
				continue
			}
			return true
		}
	}
	return false
}
