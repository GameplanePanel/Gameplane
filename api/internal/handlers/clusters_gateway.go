package handlers

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"reflect"
	"strings"

	"github.com/GameplanePanel/gameplane/api/internal/kube"
	"github.com/go-chi/chi/v5"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/util/retry"

	"github.com/GameplanePanel/gameplane/api/internal/httperr"
)

const clusterGatewayLabel = "gameplane.local/agent-gateway-credentials"

type clusterGatewayRequest struct {
	URL        string `json:"url"`
	CACert     string `json:"caCert"`
	ClientCert string `json:"clientCert"`
	ClientKey  string `json:"clientKey"`
}

func gatewaySecretName(name string) string { return "cluster-" + name + "-gateway" }

func (h clustersHandler) configureGateway(w http.ResponseWriter, req *http.Request) {
	name := chi.URLParam(req, "name")
	if name == h.reg.DefaultID() || !dnsLabelRE.MatchString(name) {
		httperr.WriteCode(w, req, http.StatusBadRequest, errors.New("select a registered remote cluster"))
		return
	}
	var in clusterGatewayRequest
	if err := json.NewDecoder(req.Body).Decode(&in); err != nil {
		httperr.WriteCode(w, req, http.StatusBadRequest, errors.New("invalid gateway configuration"))
		return
	}
	u, err := url.Parse(in.URL)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") || u.Opaque != "" {
		httperr.WriteCode(w, req, http.StatusBadRequest, errors.New("gateway URL must be an HTTPS origin"))
		return
	}
	if _, err := tls.X509KeyPair([]byte(in.ClientCert), []byte(in.ClientKey)); err != nil {
		httperr.WriteCode(w, req, http.StatusBadRequest, errors.New("invalid gateway client certificate or key"))
		return
	}
	if h.k.IsStandalone() {
		if err := h.k.RemoteAccess.ValidateURL(in.URL); err != nil {
			httperr.WriteCode(w, req, http.StatusBadRequest, errors.New("gateway destination is not allowed"))
			return
		}
	}
	if !x509.NewCertPool().AppendCertsFromPEM([]byte(in.CACert)) {
		httperr.WriteCode(w, req, http.StatusBadRequest, errors.New("invalid gateway CA certificate"))
		return
	}
	registration, err := h.k.Clusters().Get(req.Context(), name, metav1.GetOptions{})
	if err != nil {
		httperr.Write(w, req, err)
		return
	}
	// Publish a new immutable credential object before switching the pointer.
	// Concurrent rotations can never combine one request's URL with another's key.
	nonce := make([]byte, 16)
	if _, err := rand.Read(nonce); err != nil {
		httperr.Write(w, req, err)
		return
	}
	secretName := gatewaySecretName(name) + "-" + hex.EncodeToString(nonce)
	immutable := true
	_, err = h.k.Secrets(h.namespace).Create(req.Context(), &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: secretName, Namespace: h.namespace, Labels: map[string]string{
			clusterGatewayLabel: "true", ManagedByLabel: managedByValue,
		}},
		Immutable:  &immutable,
		Type:       corev1.SecretTypeOpaque,
		StringData: map[string]string{"ca.crt": in.CACert, "tls.crt": in.ClientCert, "tls.key": in.ClientKey},
	}, metav1.CreateOptions{})
	if err != nil {
		httperr.Write(w, req, err)
		return
	}
	if err := h.updateGatewayRegistration(req.Context(), registration, map[string]any{
		"url": in.URL, "tlsSecretRef": map[string]any{"name": secretName},
	}); err != nil {
		// An update may have committed before a transport failure. Retain the
		// credential when the live pointer cannot be checked safely.
		current, readErr := h.k.Clusters().Get(req.Context(), name, metav1.GetOptions{})
		if apierrors.IsNotFound(readErr) || (readErr == nil && gatewayCredentialName(current) != secretName) {
			h.cleanupGatewayCredential(req.Context(), name, secretName)
		}
		httperr.Write(w, req, err)
		return
	}
	h.cleanupGatewayCredential(req.Context(), name, gatewayCredentialName(registration))

	w.WriteHeader(http.StatusNoContent)
}

func (h clustersHandler) removeGateway(w http.ResponseWriter, req *http.Request) {
	name := chi.URLParam(req, "name")
	registration, err := h.k.Clusters().Get(req.Context(), name, metav1.GetOptions{})
	if err != nil {
		httperr.Write(w, req, err)
		return
	}
	secretName := gatewayCredentialName(registration)
	if err := h.updateGatewayRegistration(req.Context(), registration, nil); err != nil {
		httperr.Write(w, req, err)
		return
	}

	h.cleanupGatewayCredential(req.Context(), name, secretName)
	w.WriteHeader(http.StatusNoContent)
}

func gatewayCredentialName(registration *unstructured.Unstructured) string {
	name, _, _ := unstructured.NestedString(registration.Object, "spec", "agentGateway", "tlsSecretRef", "name")
	return name
}

func (h clustersHandler) cleanupGatewayCredential(ctx context.Context, cluster, secretName string) {
	// Preserve external references and support the original fixed-name API secret.
	base := gatewaySecretName(cluster)
	if secretName != base && (!strings.HasPrefix(secretName, base+"-") || len(secretName) != len(base)+33) {
		return
	}
	if secretName != base {
		if _, err := hex.DecodeString(strings.TrimPrefix(secretName, base+"-")); err != nil {
			return
		}
	}
	if err := deleteManagedSecret(ctx, h.k, h.namespace, secretName, clusterGatewayLabel); err != nil && !apierrors.IsNotFound(err) {
		slog.Warn("cluster gateway credential cleanup failed", "cluster", cluster)
	}
}

// A health status update may race configuration. Retry against fresh metadata,
// but never carry a gateway write across deletion/recreation of a registration.
func (h clustersHandler) updateGatewayRegistration(ctx context.Context, original *unstructured.Unstructured, gateway map[string]any) error {
	previous, _, err := unstructured.NestedMap(original.Object, "spec", "agentGateway")
	if err != nil {
		return err
	}
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		current, err := h.k.Clusters().Get(ctx, original.GetName(), metav1.GetOptions{})
		if err != nil {
			return err
		}
		if current.GetUID() != original.GetUID() {
			return apierrors.NewNotFound(kube.GVRCluster.GroupResource(), original.GetName())
		}
		live, _, err := unstructured.NestedMap(current.Object, "spec", "agentGateway")
		if err != nil {
			return err
		}
		if !reflect.DeepEqual(previous, live) {
			return apierrors.NewConflict(kube.GVRCluster.GroupResource(), original.GetName(), errors.New("gateway configuration changed; reload before updating"))
		}
		if gateway == nil {
			unstructured.RemoveNestedField(current.Object, "spec", "agentGateway")
		} else if err := unstructured.SetNestedMap(current.Object, gateway, "spec", "agentGateway"); err != nil {
			return err
		}
		_, err = h.k.Clusters().Update(ctx, current, metav1.UpdateOptions{})
		return err
	})
}
