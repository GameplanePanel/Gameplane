package handlers

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/GameplanePanel/gameplane/api/internal/httperr"
	"github.com/GameplanePanel/gameplane/api/internal/kube"
	"github.com/GameplanePanel/gameplane/api/internal/messages"
	"github.com/GameplanePanel/gameplane/api/internal/registry"
)

// MountRegistrySecrets exposes the managed apiKey Secret behind a
// mod-registry provider: PUT writes the key to a labelled Secret at the
// conventional name (registry.DefaultKeySecretName), so registry.DBKeyFunc
// picks it up on the next resolve — no redeploy needed, and the provider
// stays hidden (registry.Set reports it unavailable) until a key exists.
// Same labelled-Secret contract as notification sinks and auth-provider
// secrets, under the mod-registry feature label.
func MountRegistrySecrets(r chi.Router, k *kube.Client, controlNS string) {
	h := registrySecretHandler{k: k, ns: controlNS}
	r.Put("/admin/registries/{provider}/secret", h.put)
	r.Delete("/admin/registries/{provider}/secret", h.del)
}

type registrySecretHandler struct {
	k  *kube.Client
	ns string
}

// secretName validates provider against the closed set of keyed
// providers and returns the conventional Secret name for it. Unknown
// providers 400 rather than silently creating a Secret nothing will ever
// read.
func (h registrySecretHandler) secretName(w http.ResponseWriter, provider string) (string, bool) {
	if !registry.KeyedProviders[provider] {
		http.Error(w, "unknown mod-registry provider (must be one of curseforge|steam|nexus)", http.StatusBadRequest)
		return "", false
	}
	return registry.DefaultKeySecretName(provider), true
}

func (h registrySecretHandler) put(w http.ResponseWriter, req *http.Request) {
	secretName, ok := h.secretName(w, chi.URLParam(req, "provider"))
	if !ok {
		return
	}
	var body struct {
		APIKey string `json:"apiKey"`
	}
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		httperr.WriteMessage(w, req, messages.New("server.common.invalid_json", nil))
		return
	}
	if body.APIKey == "" {
		httperr.WriteMessage(w, req, messages.New("server.registry.api_key_required", nil))
		return
	}
	data := map[string]string{"apiKey": body.APIKey}
	if err := upsertLabelledSecret(req.Context(), h.k, h.ns, secretName, registry.RegistryKeySecretLabel, data); err != nil {
		if errors.Is(err, errNotManagedSecret) {
			httperr.WriteCode(w, req, http.StatusConflict, err)
			return
		}
		httperr.Write(w, req, err)
		return
	}
	// Never echo the value back — just the ref + key list, same contract
	// as the auth-provider secret endpoint.
	writeJSON(w, map[string]any{"name": secretName, "keys": []string{"apiKey"}})
}

func (h registrySecretHandler) del(w http.ResponseWriter, req *http.Request) {
	secretName, ok := h.secretName(w, chi.URLParam(req, "provider"))
	if !ok {
		return
	}
	if err := deleteManagedSecret(req.Context(), h.k, h.ns, secretName, registry.RegistryKeySecretLabel); err != nil {
		httperr.Write(w, req, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
