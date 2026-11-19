package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/go-chi/chi/v5"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/util/retry"

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

// curseforgeShareAnnotation on the managed CurseForge key Secret turns on
// sharing the key with game servers (the operator copies it into a Secret
// owned by each server that runs a CurseForge pack).
const curseforgeShareAnnotation = "gameplane.local/share-with-game-servers"

var errNoManagedKey = errors.New("no managed CurseForge key")

// MountRegistryShare exposes the admin "share the CurseForge key with game
// servers" setting. RBAC: /admin/registries is config:manage in rbac.go.
func MountRegistryShare(r chi.Router, k *kube.Client, controlNS string) {
	h := registrySecretHandler{k: k, ns: controlNS}
	r.Get("/admin/registries/curseforge/share", h.getShare)
	r.Put("/admin/registries/curseforge/share", h.putShare)
}

type shareView struct {
	Share      bool `json:"share"`
	ManagedKey bool `json:"managedKey"`
}

// curseforgeShareState reads the managed key Secret of the control namespace:
// managedKey is true when a Secret labelled for mod-registry keys holds a
// non-empty apiKey (a key given only by the startup flag does not count);
// share is true only then and with the annotation set to "true". Any read
// failure (or a nil client) is (false, false).
func curseforgeShareState(ctx context.Context, k *kube.Client, ns string) (share, managedKey bool) {
	store := k.Secrets(ns)
	if store == nil || ns == "" {
		return false, false
	}
	sec, err := store.Get(ctx, registry.DefaultKeySecretName("curseforge"), metav1.GetOptions{})
	if err != nil || sec.Labels[registry.RegistryKeySecretLabel] != "true" || len(sec.Data["apiKey"]) == 0 {
		return false, false
	}
	return sec.Annotations[curseforgeShareAnnotation] == "true", true
}

func (h registrySecretHandler) getShare(w http.ResponseWriter, req *http.Request) {
	share, managed := curseforgeShareState(req.Context(), h.k, h.ns)
	writeJSON(w, shareView{Share: share, ManagedKey: managed})
}

func (h registrySecretHandler) putShare(w http.ResponseWriter, req *http.Request) {
	var body struct {
		Share *bool `json:"share"`
	}
	if err := json.NewDecoder(io.LimitReader(req.Body, 1<<10)).Decode(&body); err != nil || body.Share == nil {
		httperr.WriteMessage(w, req, messages.New("server.common.invalid_json", nil))
		return
	}
	want := *body.Share
	store := h.k.Secrets(h.ns)
	name := registry.DefaultKeySecretName("curseforge")
	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		sec, err := store.Get(req.Context(), name, metav1.GetOptions{})
		if err != nil {
			return err
		}
		if sec.Labels[registry.RegistryKeySecretLabel] != "true" || len(sec.Data["apiKey"]) == 0 {
			return errNoManagedKey
		}
		if want {
			if sec.Annotations == nil {
				sec.Annotations = map[string]string{}
			}
			sec.Annotations[curseforgeShareAnnotation] = "true"
		} else {
			delete(sec.Annotations, curseforgeShareAnnotation)
		}
		_, err = store.Update(req.Context(), sec, metav1.UpdateOptions{})
		return err
	})
	switch {
	case err == nil:
		writeJSON(w, shareView{Share: want, ManagedKey: true})
	case errors.Is(err, errNoManagedKey) || apierrors.IsNotFound(err):
		if !want {
			writeJSON(w, shareView{})
			return
		}
		httperr.WriteMessage(w, req, messages.New("server.registry.share_needs_managed_key", nil))
	default:
		httperr.Write(w, req, err)
	}
}
