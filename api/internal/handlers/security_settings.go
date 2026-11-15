package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/go-chi/chi/v5"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/GameplanePanel/gameplane/api/internal/audit"
	"github.com/GameplanePanel/gameplane/api/internal/httperr"
	"github.com/GameplanePanel/gameplane/api/internal/kube"
	"github.com/GameplanePanel/gameplane/api/internal/messages"
	"github.com/GameplanePanel/gameplane/api/internal/scope"
)

// The per-cluster "allow root overrides" switch (spec 030, OD-7 C). Its only
// store is one ConfigMap in the cluster's operator control-plane namespace.
// The API reads it and updates it by name; it never creates it (Helm does).
const (
	securitySettingsConfigMap = "gameplane-security-settings"
	securitySettingsKey       = "allowRootOverrides"
	// defaultRemoteControlPlaneNamespace is used for a remote cluster whose
	// registration carries no spec.controlPlaneNamespace (OD-10 A).
	defaultRemoteControlPlaneNamespace = "gameplane"
)

// securitySettingsNamespace returns the namespace of cluster cl's switch
// ConfigMap. The home cluster uses the registry's control-plane namespace; a
// remote cluster uses the namespace on its Cluster registration.
func securitySettingsNamespace(ctx context.Context, reg *kube.Registry, cl string) (string, error) {
	if cl == reg.DefaultID() {
		ns := reg.ControlPlaneNamespace()
		if ns == "" {
			return "", errors.New("home control-plane namespace is not configured")
		}
		return ns, nil
	}
	// Clusters() is nil-safe; a nil store means registrations cannot be read.
	store := reg.Management().Clusters()
	if store == nil {
		return "", errors.New("cluster registrations are unavailable")
	}
	registration, err := store.Get(ctx, cl, metav1.GetOptions{})
	if err != nil {
		return "", fmt.Errorf("read cluster %s registration: %w", cl, err)
	}
	ns, _, _ := unstructured.NestedString(registration.Object, "spec", "controlPlaneNamespace")
	if ns == "" {
		ns = defaultRemoteControlPlaneNamespace
	}
	return ns, nil
}

// securitySettingsClient returns the client of cluster cl.
func securitySettingsClient(reg *kube.Registry, cl string) (*kube.Client, error) {
	c, ok := reg.Get(cl)
	if !ok || c == nil || c.Typed == nil {
		return nil, fmt.Errorf("cluster %s has no client", cl)
	}
	return c, nil
}

// getSecuritySettingsConfigMap reads the switch ConfigMap of cluster cl. It
// returns a nil ConfigMap (and no error) when the ConfigMap is not found.
func getSecuritySettingsConfigMap(ctx context.Context, reg *kube.Registry, cl string) (*corev1.ConfigMap, string, error) {
	ns, err := securitySettingsNamespace(ctx, reg, cl)
	if err != nil {
		return nil, "", err
	}
	c, err := securitySettingsClient(reg, cl)
	if err != nil {
		return nil, ns, err
	}
	cm, err := c.Typed.CoreV1().ConfigMaps(ns).Get(ctx, securitySettingsConfigMap, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return nil, ns, nil
	}
	if err != nil {
		return nil, ns, fmt.Errorf("read %s/%s: %w", ns, securitySettingsConfigMap, err)
	}
	return cm, ns, nil
}

// securityRootAllowed reports whether cluster cl allows root overrides. Only
// the exact value "true" is on. A missing ConfigMap, a missing key, any other
// value and any read error all read as off, with the error returned so the
// caller can refuse the request.
func securityRootAllowed(ctx context.Context, reg *kube.Registry, cl string) (bool, error) {
	cm, _, err := getSecuritySettingsConfigMap(ctx, reg, cl)
	if err != nil {
		return false, err
	}
	if cm == nil {
		return false, nil
	}
	return cm.Data[securitySettingsKey] == "true", nil
}

// securitySettingsBody is the GET and PUT response (contracts/cluster-setting.md).
type securitySettingsBody struct {
	Cluster            string `json:"cluster"`
	AllowRootOverrides bool   `json:"allowRootOverrides"`
	Present            bool   `json:"present"`
}

// securitySettingsHandler serves GET and PUT /security-settings?cluster=<id>.
type securitySettingsHandler struct {
	reg     *kube.Registry
	auditor *audit.Auditor
}

// MountSecuritySettings wires the per-cluster allow-root-overrides switch:
// GET /security-settings (config:read) and PUT /security-settings
// (config:manage). The cluster is chosen with ?cluster= as for
// /backup-destinations.
func MountSecuritySettings(r chi.Router, reg *kube.Registry, auditor *audit.Auditor) {
	h := securitySettingsHandler{reg: reg, auditor: auditor}
	r.Route("/security-settings", func(r chi.Router) {
		r.Get("/", h.get)
		r.Put("/", h.put)
	})
}

// securitySettingsCluster resolves ?cluster= and writes the refusal when the
// cluster is not known to the caller's registry.
func securitySettingsCluster(w http.ResponseWriter, req *http.Request, reg *kube.Registry) (string, bool) {
	if _, ok := resolveCluster(w, req, reg); !ok {
		return "", false
	}
	return scope.RequestedCluster(req), true
}

func (h securitySettingsHandler) get(w http.ResponseWriter, req *http.Request) {
	cl, ok := securitySettingsCluster(w, req, h.reg)
	if !ok {
		return
	}
	cm, _, err := getSecuritySettingsConfigMap(req.Context(), h.reg, cl)
	if err != nil {
		httperr.WriteCode(w, req, http.StatusBadGateway, err)
		return
	}
	if cm == nil {
		writeJSON(w, securitySettingsBody{Cluster: cl})
		return
	}
	writeJSON(w, securitySettingsBody{
		Cluster:            cl,
		AllowRootOverrides: cm.Data[securitySettingsKey] == "true",
		Present:            true,
	})
}

// decodeAllowRootOverrides reads {"allowRootOverrides": bool}. Other members
// are ignored. A missing or non-boolean value is an error.
func decodeAllowRootOverrides(body []byte) (bool, error) {
	var raw map[string]any
	if err := json.Unmarshal(body, &raw); err != nil {
		return false, fmt.Errorf("body must be a JSON object: %w", err)
	}
	v, ok := raw[securitySettingsKey].(bool)
	if !ok {
		return false, errors.New("allowRootOverrides must be true or false")
	}
	return v, nil
}

// writeSecurityAudit writes one security row with WriteSync. The caller decides
// what a failure means. A nil auditor skips auditing (test routers).
func (h securitySettingsHandler) writeSecurityAudit(ctx context.Context, target, reason string, status int) error {
	if h.auditor == nil {
		return nil
	}
	return h.auditor.WriteSync(ctx, http.MethodPut, "/security-settings", target, reason, status)
}

func onOff(v bool) string {
	if v {
		return "true"
	}
	return "false"
}

// put follows the order in contracts/cluster-setting.md: the ConfigMap must
// exist, an unchanged value writes nothing, a change writes the audit row
// first, then updates the ConfigMap.
func (h securitySettingsHandler) put(w http.ResponseWriter, req *http.Request) {
	cl, ok := securitySettingsCluster(w, req, h.reg)
	if !ok {
		return
	}
	body, err := io.ReadAll(req.Body)
	if err != nil {
		httperr.WriteMessage(w, req, messages.New("server.config.read_body", nil))
		return
	}
	if len(body) == 0 {
		httperr.WriteMessage(w, req, messages.New("server.empty_body", nil))
		return
	}
	want, err := decodeAllowRootOverrides(body)
	if err != nil {
		httperr.WriteCode(w, req, http.StatusUnprocessableEntity, err)
		return
	}

	cm, ns, err := getSecuritySettingsConfigMap(req.Context(), h.reg, cl)
	if err != nil {
		httperr.WriteCode(w, req, http.StatusBadGateway, err)
		return
	}
	if cm == nil {
		httperr.WriteMessage(w, req, messages.New("server.cluster.security_settings_missing", nil))
		return
	}
	current := cm.Data[securitySettingsKey] == "true"
	if current == want {
		writeJSON(w, securitySettingsBody{Cluster: cl, AllowRootOverrides: current, Present: true})
		return
	}

	target := "security/" + cl
	reason := fmt.Sprintf("%s:%s->%s", securitySettingsKey, onOff(current), onOff(want))
	if err := h.writeSecurityAudit(req.Context(), target, reason, http.StatusOK); err != nil {
		httperr.WriteCode(w, req, http.StatusInternalServerError, errors.New("audit write failed"))
		return
	}
	audit.MarkRecorded(req.Context())

	c, err := securitySettingsClient(h.reg, cl)
	if err != nil {
		httperr.WriteCode(w, req, http.StatusBadGateway, err)
		return
	}
	updated := cm.DeepCopy()
	if updated.Data == nil {
		updated.Data = map[string]string{}
	}
	updated.Data[securitySettingsKey] = onOff(want)
	if _, err := c.Typed.CoreV1().ConfigMaps(ns).Update(req.Context(), updated, metav1.UpdateOptions{}); err != nil {
		// The change was attempted and not stored: record that too. If this row
		// cannot be written the first row stands; the reply does not change.
		_ = h.writeSecurityAudit(req.Context(), target, "security_save_failed", http.StatusBadGateway)
		httperr.WriteCode(w, req, http.StatusBadGateway, fmt.Errorf("update %s/%s: %w", ns, securitySettingsConfigMap, err))
		return
	}
	writeJSON(w, securitySettingsBody{Cluster: cl, AllowRootOverrides: want, Present: true})
}
