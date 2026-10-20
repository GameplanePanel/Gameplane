// ModuleSource management surface (admin-only via RBAC):
//
//   - POST   /modules/sources         — create a ModuleSource
//   - PUT    /modules/sources/{name}  — replace its spec
//   - DELETE /modules/sources/{name}  — delete (409 while Modules reference it)
//
// The API validates the discriminated union before writing, but the
// CRD's CEL rules remain authoritative — kubectl-applied sources go
// through the same admission checks.

package handlers

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/go-chi/chi/v5"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/GameplanePanel/gameplane/api/internal/httperr"
	"github.com/GameplanePanel/gameplane/api/internal/kube"
)

// sourceRequest mirrors ModuleSourceSpec plus the object name (used on
// POST; ignored on PUT where the path wins).
type sourceRequest struct {
	Name            string           `json:"name,omitempty"`
	Type            string           `json:"type"`
	OCI             *ociSourceSpec   `json:"oci,omitempty"`
	Git             *gitSourceSpec   `json:"git,omitempty"`
	HTTP            *httpSourceSpec  `json:"http,omitempty"`
	Local           *localSourceSpec `json:"local,omitempty"`
	Allow           []string         `json:"allow,omitempty"`
	RefreshInterval string           `json:"refreshInterval,omitempty"`
	// Verify is the cosign signature policy. The CRD restricts it to OCI
	// sources (CEL); validate() mirrors that and the operator enforces it.
	Verify *verifySourceSpec `json:"verify,omitempty"`
}

// verifySourceSpec mirrors ModuleSource.spec.verify: exactly one of key
// (a Secret holding a cosign public key) or keyless (Fulcio issuer +
// certificate identity) is set.
type verifySourceSpec struct {
	Key     *nameRef          `json:"key,omitempty"`
	Keyless *keylessVerifyReq `json:"keyless,omitempty"`
}

type keylessVerifyReq struct {
	Issuer   string `json:"issuer"`
	Identity string `json:"identity"`
}

// nameRef is the {"name": "..."} shape shared by module lists and
// secret references.
type nameRef struct {
	Name string `json:"name"`
}

type ociSourceSpec struct {
	URL           string    `json:"url"`
	Modules       []nameRef `json:"modules"`
	PullSecretRef *nameRef  `json:"pullSecretRef,omitempty"`
	Insecure      bool      `json:"insecure,omitempty"`
}

type gitSourceSpec struct {
	URL       string   `json:"url"`
	Ref       string   `json:"ref,omitempty"`
	SubPath   string   `json:"subPath,omitempty"`
	SecretRef *nameRef `json:"secretRef,omitempty"`
}

type httpSourceSpec struct {
	URL       string   `json:"url"`
	SecretRef *nameRef `json:"secretRef,omitempty"`
	Insecure  bool     `json:"insecure,omitempty"`
}

type localSourceSpec struct {
	Path string `json:"path,omitempty"`
}

// validate checks the union shape; the CRD re-validates on write.
func (in *sourceRequest) validate() error {
	configs := map[string]bool{
		"oci":   in.OCI != nil,
		"git":   in.Git != nil,
		"http":  in.HTTP != nil,
		"local": in.Local != nil,
	}
	switch in.Type {
	case "oci":
		if in.OCI == nil {
			return errors.New("oci config is required for type oci")
		}
		if in.OCI.URL == "" || len(in.OCI.Modules) == 0 {
			return errors.New("oci sources need url and at least one module")
		}
	case "git":
		if in.Git == nil {
			return errors.New("git config is required for type git")
		}
		if in.Git.URL == "" {
			return errors.New("git sources need url")
		}
	case "http":
		if in.HTTP == nil {
			return errors.New("http config is required for type http")
		}
		if in.HTTP.URL == "" {
			return errors.New("http sources need url")
		}
	case "local":
		if in.Local == nil {
			return errors.New("local config is required for type local")
		}
	case "upload":
		// No nested config.
	default:
		return fmt.Errorf("unknown source type %q", in.Type)
	}
	for cfgType, set := range configs {
		if set && cfgType != in.Type {
			return fmt.Errorf("%s config is only valid for type %s", cfgType, cfgType)
		}
	}
	// verify mirrors the CRD's two CEL rules (shape only; the CRD remains
	// authoritative): it is OCI-only, and exactly one of key/keyless is set.
	if in.Verify != nil {
		if in.Type != "oci" {
			return fmt.Errorf("verify is only valid for type oci, not %q", in.Type)
		}
		hasKey := in.Verify.Key != nil
		hasKeyless := in.Verify.Keyless != nil
		if hasKey == hasKeyless {
			return errors.New("verify needs exactly one of key or keyless")
		}
		if hasKey && in.Verify.Key.Name == "" {
			return errors.New("verify.key needs a secret name")
		}
		if hasKeyless && (in.Verify.Keyless.Issuer == "" || in.Verify.Keyless.Identity == "") {
			return errors.New("verify.keyless needs issuer and identity")
		}
	}
	return nil
}

// spec renders the validated request as the CR's spec field.
func (in *sourceRequest) spec() map[string]any {
	spec := map[string]any{"type": in.Type}
	if in.RefreshInterval != "" {
		spec["refreshInterval"] = in.RefreshInterval
	}
	if len(in.Allow) > 0 {
		allow := make([]any, 0, len(in.Allow))
		for _, a := range in.Allow {
			allow = append(allow, a)
		}
		spec["allow"] = allow
	}
	secretRef := func(ref *nameRef) any {
		if ref == nil || ref.Name == "" {
			return nil
		}
		return map[string]any{"name": ref.Name}
	}
	switch in.Type {
	case "oci":
		mods := make([]any, 0, len(in.OCI.Modules))
		for _, m := range in.OCI.Modules {
			mods = append(mods, map[string]any{"name": m.Name})
		}
		oci := map[string]any{"url": in.OCI.URL, "modules": mods}
		if in.OCI.Insecure {
			oci["insecure"] = true
		}
		if ref := secretRef(in.OCI.PullSecretRef); ref != nil {
			oci["pullSecretRef"] = ref
		}
		spec["oci"] = oci
		// verify is a sibling of oci on the spec, OCI-only (CEL-enforced).
		if v := verifyMap(in.Verify); v != nil {
			spec["verify"] = v
		}
	case "git":
		git := map[string]any{"url": in.Git.URL}
		if in.Git.Ref != "" {
			git["ref"] = in.Git.Ref
		}
		if in.Git.SubPath != "" {
			git["subPath"] = in.Git.SubPath
		}
		if ref := secretRef(in.Git.SecretRef); ref != nil {
			git["secretRef"] = ref
		}
		spec["git"] = git
	case "http":
		httpSpec := map[string]any{"url": in.HTTP.URL}
		if in.HTTP.Insecure {
			httpSpec["insecure"] = true
		}
		if ref := secretRef(in.HTTP.SecretRef); ref != nil {
			httpSpec["secretRef"] = ref
		}
		spec["http"] = httpSpec
	case "local":
		local := map[string]any{}
		if in.Local.Path != "" {
			local["path"] = in.Local.Path
		}
		spec["local"] = local
	}
	return spec
}

// verifyMap renders the cosign policy as the CR's spec.verify map, or nil
// when no policy is set. validate() guarantees exactly one of key/keyless.
func verifyMap(v *verifySourceSpec) map[string]any {
	if v == nil {
		return nil
	}
	if v.Key != nil && v.Key.Name != "" {
		return map[string]any{"key": map[string]any{"name": v.Key.Name}}
	}
	if v.Keyless != nil {
		return map[string]any{"keyless": map[string]any{
			"issuer":   v.Keyless.Issuer,
			"identity": v.Keyless.Identity,
		}}
	}
	return nil
}

func (h modulesHandler) createSource(w http.ResponseWriter, req *http.Request) {
	var in sourceRequest
	if err := json.NewDecoder(req.Body).Decode(&in); err != nil {
		httperr.WriteCode(w, req, http.StatusBadRequest, err)
		return
	}
	if !dnsLabelRE.MatchString(in.Name) {
		httperr.WriteCode(w, req, http.StatusBadRequest,
			errors.New("name must be a DNS label (lowercase, digits, hyphens)"))
		return
	}
	if err := in.validate(); err != nil {
		httperr.WriteCode(w, req, http.StatusBadRequest, err)
		return
	}
	desired := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "gameplane.local/v1alpha1",
		"kind":       "ModuleSource",
		"metadata":   map[string]any{"name": in.Name},
		"spec":       in.spec(),
	}}
	created, err := h.k.Dynamic.Resource(kube.GVRModuleSource).Create(req.Context(), desired, metav1.CreateOptions{})
	if err != nil {
		httperr.Write(w, req, err)
		return
	}
	writeJSONCreated(w, created)
}

func (h modulesHandler) updateSource(w http.ResponseWriter, req *http.Request) {
	name := chi.URLParam(req, "name")
	var in sourceRequest
	if err := json.NewDecoder(req.Body).Decode(&in); err != nil {
		httperr.WriteCode(w, req, http.StatusBadRequest, err)
		return
	}
	if err := in.validate(); err != nil {
		httperr.WriteCode(w, req, http.StatusBadRequest, err)
		return
	}
	existing, err := h.k.Dynamic.Resource(kube.GVRModuleSource).Get(req.Context(), name, metav1.GetOptions{})
	if err != nil {
		httperr.Write(w, req, err)
		return
	}
	existing.Object["spec"] = in.spec()
	updated, err := h.k.Dynamic.Resource(kube.GVRModuleSource).Update(req.Context(), existing, metav1.UpdateOptions{})
	if err != nil {
		httperr.Write(w, req, err)
		return
	}
	writeJSON(w, updated)
}

func (h modulesHandler) deleteSource(w http.ResponseWriter, req *http.Request) {
	name := chi.URLParam(req, "name")
	// Installed Modules resolve versions and pull bundles through their
	// source; deleting it would strand them. Mirror the module-uninstall
	// blocker UX with a 409.
	mods, err := h.k.Dynamic.Resource(kube.GVRModule).List(req.Context(), metav1.ListOptions{})
	if err != nil {
		httperr.Write(w, req, err)
		return
	}
	var users []string
	for i := range mods.Items {
		srcName, _, _ := unstructured.NestedString(mods.Items[i].Object, "spec", "source", "name")
		if srcName == name {
			users = append(users, mods.Items[i].GetName())
		}
	}
	if len(users) > 0 {
		httperr.WriteCode(w, req, http.StatusConflict,
			errors.New("source \""+name+"\" is still used by installed module(s): "+joinNames(users)))
		return
	}
	if err := h.k.Dynamic.Resource(kube.GVRModuleSource).Delete(req.Context(), name, metav1.DeleteOptions{}); err != nil {
		httperr.Write(w, req, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
