// Backup destinations are restic repositories the Backup/BackupSchedule
// CRDs reference by Secret name. They're modelled as plain Kubernetes
// Secrets labeled `gameplane.local/backup-destination=true` rather than a
// dedicated CRD — the on-disk shape is small (URL + password) and the
// generic CRD CRUD path doesn't cover core resources.
//
// The handler always reads/writes a *redacted* projection: the URL is
// visible (it's already shown in restic logs and BackupSchedule.spec),
// but the password value is never returned. Callers can verify a key
// exists via `hasPassword: true`; updating the password requires
// supplying a fresh value via POST.

package handlers

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	"github.com/GameplanePanel/gameplane/api/internal/httperr"
	"github.com/GameplanePanel/gameplane/api/internal/kube"
	"github.com/GameplanePanel/gameplane/api/internal/scope"
)

// destinationLabel marks a Secret as a Gameplane backup destination. The
// agent and operator never read this label — it's purely a discovery
// hint for the API.
const destinationLabel = "gameplane.local/backup-destination"

// MountDestinations wires /backup-destinations onto the supplied router.
// Reads return a redacted projection (no secret values); writes accept
// {url, password} and persist them as Secret stringData.
func MountDestinations(r chi.Router, reg *kube.Registry) {
	h := destinationHandler{reg: reg}
	r.Route("/backup-destinations", func(r chi.Router) {
		r.Get("/", h.list)
		r.Post("/", h.create)
		r.Get("/{name}", h.get)
		r.Delete("/{name}", h.del)
	})
}

type destinationHandler struct {
	reg *kube.Registry
}

// destinationView is the public projection. Note password is absent;
// hasPassword is the read-side existence check.
type destinationView struct {
	Name        string `json:"name"`
	URL         string `json:"url"`
	HasPassword bool   `json:"hasPassword"`
	CreatedAt   string `json:"createdAt,omitempty"`
}

type destinationCreate struct {
	Name     string `json:"name"`
	URL      string `json:"url"`
	Password string `json:"password"`
}

type destinationListResp struct {
	Items []destinationView `json:"items"`
}

func (h destinationHandler) list(w http.ResponseWriter, req *http.Request) {
	k, ok := resolveCluster(w, req, h.reg)
	if !ok {
		return
	}
	ns, ok := resolveNS(w, req)
	if !ok {
		return
	}
	secrets, err := k.Typed.CoreV1().Secrets(ns).List(req.Context(), metav1.ListOptions{
		LabelSelector: destinationLabel + "=true",
	})
	if err != nil {
		httperr.Write(w, req, err)
		return
	}
	out := destinationListResp{Items: make([]destinationView, 0, len(secrets.Items))}
	for i := range secrets.Items {
		out.Items = append(out.Items, project(&secrets.Items[i]))
	}
	writeJSON(w, out)
}

func (h destinationHandler) get(w http.ResponseWriter, req *http.Request) {
	k, ok := resolveCluster(w, req, h.reg)
	if !ok {
		return
	}
	ns, ok := resolveNS(w, req)
	if !ok {
		return
	}
	name := chi.URLParam(req, "name")
	s, err := k.Typed.CoreV1().Secrets(ns).Get(req.Context(), name, metav1.GetOptions{})
	if err != nil {
		httperr.Write(w, req, err)
		return
	}
	if !isDestination(s) {
		httperr.Write(w, req, apierrors.NewNotFound(corev1.Resource("secrets"), name))
		return
	}
	writeJSON(w, project(s))
}

func (h destinationHandler) create(w http.ResponseWriter, req *http.Request) {
	k, ok := resolveCluster(w, req, h.reg)
	if !ok {
		return
	}
	ns, ok := resolveNS(w, req)
	if !ok {
		return
	}
	var in destinationCreate
	if err := json.NewDecoder(req.Body).Decode(&in); err != nil {
		httperr.Write(w, req, err)
		return
	}
	if !dnsLabelRE.MatchString(in.Name) {
		httperr.WriteCode(w, req, http.StatusBadRequest, errors.New("name must be a DNS label (lowercase, digits, hyphens)"))
		return
	}
	if in.URL == "" || in.Password == "" {
		httperr.WriteCode(w, req, http.StatusBadRequest, errors.New("url and password are required"))
		return
	}

	desired := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      in.Name,
			Namespace: ns,
			Labels:    map[string]string{destinationLabel: "true"},
		},
		Type: corev1.SecretTypeOpaque,
		// Key is "repo" (not "url"): the operator's Backup/Restore Jobs read
		// RESTIC_REPOSITORY from secret key "repo" (see backup_controller.go /
		// restore_controller.go and test/e2e/fixtures). The API keeps "url"
		// as its external field name; only the on-Secret key changes.
		StringData: map[string]string{
			"repo":     in.URL,
			"password": in.Password,
		},
	}

	// Try create first; on AlreadyExists, fall through to a JSON-merge
	// patch that preserves any extra keys an admin may have set via
	// kubectl. This lets users rotate the password without nuking unrelated
	// fields like a pinned restic version.
	created, err := k.Typed.CoreV1().Secrets(ns).Create(req.Context(), desired, metav1.CreateOptions{})
	if err == nil {
		writeJSON(w, project(created))
		return
	}
	if !apierrors.IsAlreadyExists(err) {
		httperr.Write(w, req, err)
		return
	}
	// Verify the existing object is one of ours before patching.
	existing, err := k.Typed.CoreV1().Secrets(ns).Get(req.Context(), in.Name, metav1.GetOptions{})
	if err != nil {
		httperr.Write(w, req, err)
		return
	}
	if !isDestination(existing) {
		httperr.WriteCode(w, req, http.StatusConflict, errors.New("a non-Gameplane secret with that name already exists"))
		return
	}
	patch := map[string]any{
		"stringData": map[string]string{
			"repo":     in.URL,
			"password": in.Password,
		},
	}
	patchBytes, _ := json.Marshal(patch)
	updated, err := k.Typed.CoreV1().Secrets(ns).Patch(
		req.Context(), in.Name, types.MergePatchType, patchBytes, metav1.PatchOptions{},
	)
	if err != nil {
		httperr.Write(w, req, err)
		return
	}
	writeJSON(w, project(updated))
}

func (h destinationHandler) del(w http.ResponseWriter, req *http.Request) {
	k, ok := resolveCluster(w, req, h.reg)
	if !ok {
		return
	}
	ns, ok := resolveNS(w, req)
	if !ok {
		return
	}
	name := chi.URLParam(req, "name")
	// Refuse to delete arbitrary Secrets through this endpoint — only
	// labelled ones. This matters because the route name is friendly to
	// guess but the underlying object is a core Secret with broader trust.
	existing, err := k.Typed.CoreV1().Secrets(ns).Get(req.Context(), name, metav1.GetOptions{})
	if err != nil {
		httperr.Write(w, req, err)
		return
	}
	if !isDestination(existing) {
		httperr.Write(w, req, apierrors.NewNotFound(corev1.Resource("secrets"), name))
		return
	}
	if err := k.Typed.CoreV1().Secrets(ns).Delete(req.Context(), name, metav1.DeleteOptions{}); err != nil {
		httperr.Write(w, req, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func project(s *corev1.Secret) destinationView {
	// Prefer the operator key "repo"; fall back to the legacy "url" key so
	// destinations written before the key rename still display their URL
	// (re-saving migrates them to "repo").
	url := string(s.Data["repo"])
	if url == "" {
		url = string(s.Data["url"])
	}
	if v, ok := s.StringData["repo"]; ok {
		url = v
	} else if v, ok := s.StringData["url"]; ok {
		url = v
	}
	_, hasPwData := s.Data["password"]
	_, hasPwString := s.StringData["password"]
	return destinationView{
		Name:        s.Name,
		URL:         url,
		HasPassword: hasPwData || hasPwString,
		CreatedAt:   s.CreationTimestamp.Format("2006-01-02T15:04:05Z"),
	}
}

func isDestination(s *corev1.Secret) bool {
	return s != nil && s.Labels[destinationLabel] == "true"
}

// scope.ErrForbiddenNamespace handling already lives in resolveNS via
// httperr.Write, so we don't repeat it here.
var _ = scope.ErrForbiddenNamespace
