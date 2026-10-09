package handlers

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"strings"
	"time"

	"github.com/GameplanePanel/gameplane/api/internal/httperr"
	"github.com/GameplanePanel/gameplane/api/internal/kube"
	"github.com/go-chi/chi/v5"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/util/retry"
)

// replaceKubeconfig publishes immutable credentials before changing their
// registration pointer. A CAS prevents overlapping rotations from mixing data
// or applying an old request to a deleted/recreated registration.
func (h clustersHandler) replaceKubeconfig(w http.ResponseWriter, req *http.Request) {
	name := chi.URLParam(req, "name")
	if name == h.reg.DefaultID() || !dnsLabelRE.MatchString(name) {
		httperr.WriteCode(w, req, http.StatusBadRequest, errors.New("select a registered remote cluster"))
		return
	}
	var in struct {
		Kubeconfig string `json:"kubeconfig"`
	}
	if err := json.NewDecoder(req.Body).Decode(&in); err != nil || in.Kubeconfig == "" {
		httperr.WriteCode(w, req, http.StatusBadRequest, errors.New("kubeconfig is required"))
		return
	}
	if err := h.validateRemoteKubeconfig([]byte(in.Kubeconfig)); err != nil {
		httperr.WriteCode(w, req, http.StatusBadRequest, errors.New("invalid kubeconfig"))
		return
	}
	registration, err := h.k.Clusters().Get(req.Context(), name, metav1.GetOptions{})
	if err != nil {
		httperr.Write(w, req, err)
		return
	}
	if registration.GetDeletionTimestamp() != nil {
		httperr.Write(w, req, apierrors.NewNotFound(kube.GVRCluster.GroupResource(), name))
		return
	}
	oldName, _, _ := unstructured.NestedString(registration.Object, "spec", "kubeconfigSecret", "name")
	// Capture the old credential identity before publishing the replacement;
	// cleanup must not remove a fixed-name secret recreated by another POST.
	oldSecret, err := clusterKubeconfigSecret(req.Context(), h.k, h.namespace, name, oldName)
	if err != nil && !apierrors.IsNotFound(err) {
		httperr.Write(w, req, err)
		return
	}
	nonce := make([]byte, 16)
	if _, err := rand.Read(nonce); err != nil {
		httperr.Write(w, req, err)
		return
	}
	secretName := clusterKubeconfigSecretName(name) + "-" + hex.EncodeToString(nonce)
	immutable := true
	created, err := h.k.Secrets(h.namespace).Create(req.Context(), &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: secretName, Namespace: h.namespace, Labels: map[string]string{kube.ClusterKubeconfigLabel: "true", ManagedByLabel: managedByValue}},
		Immutable:  &immutable, Type: corev1.SecretTypeOpaque,
		Data: map[string][]byte{"kubeconfig": []byte(in.Kubeconfig)},
	}, metav1.CreateOptions{})
	if err != nil {
		httperr.Write(w, req, err)
		return
	}
	err = h.updateKubeconfigRegistration(req.Context(), registration, secretName)
	// Cleanup and invalidation must finish even when the caller disconnects.
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(req.Context()), 5*time.Second)
	defer cancel()
	if err != nil {
		current, readErr := h.k.Clusters().Get(cleanupCtx, name, metav1.GetOptions{})
		liveName := ""
		if readErr == nil {
			liveName, _, _ = unstructured.NestedString(current.Object, "spec", "kubeconfigSecret", "name")
		}
		if apierrors.IsNotFound(readErr) || (readErr == nil && liveName != secretName) {
			h.cleanupKubeconfigCredential(cleanupCtx, name, created)
		} else {
			// A lost response may hide a committed pointer update. Retain the
			// credential and conservatively invalidate the old client.
			h.reg.RemoveIfUID(name, registration.GetUID())
		}
		httperr.Write(w, req, err)
		return
	}
	h.reg.RemoveIfUID(name, registration.GetUID())
	// Loading uses the same bounded and validated transport as startup.
	// It rechecks the live UID/version before publication; the poll/watch can
	// retry if a concurrent health update or removal defeats this refresh.
	_ = kube.RefreshRegisteredCluster(cleanupCtx, h.k, h.reg, h.namespace, name, registration.GetUID())
	h.cleanupKubeconfigCredential(cleanupCtx, name, oldSecret)
	w.WriteHeader(http.StatusNoContent)
}

func (h clustersHandler) validateRemoteKubeconfig(data []byte) error {
	if !h.k.IsStandalone() {
		_, err := kube.ConfigFromKubeconfig(data)
		return err
	}
	cfg, err := kube.ValidateStandaloneKubeconfig(data)
	if err != nil {
		return err
	}
	return h.k.RemoteAccess.ValidateURL(cfg.Host)
}

func (h clustersHandler) updateKubeconfigRegistration(ctx context.Context, original *unstructured.Unstructured, secretName string) error {
	previous, _, err := unstructured.NestedMap(original.Object, "spec", "kubeconfigSecret")
	if err != nil {
		return err
	}
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		current, err := h.k.Clusters().Get(ctx, original.GetName(), metav1.GetOptions{})
		if err != nil {
			return err
		}
		if current.GetUID() != original.GetUID() || current.GetDeletionTimestamp() != nil {
			return apierrors.NewNotFound(kube.GVRCluster.GroupResource(), original.GetName())
		}
		live, _, err := unstructured.NestedMap(current.Object, "spec", "kubeconfigSecret")
		if err != nil {
			return err
		}
		if !reflect.DeepEqual(previous, live) {
			return apierrors.NewConflict(kube.GVRCluster.GroupResource(), original.GetName(), errors.New("kubeconfig changed; reload before updating"))
		}
		if err := unstructured.SetNestedMap(current.Object, map[string]any{"name": secretName, "key": "kubeconfig"}, "spec", "kubeconfigSecret"); err != nil {
			return err
		}
		_, err = h.k.Clusters().Update(ctx, current, metav1.UpdateOptions{})
		return err
	})
}

func ownedKubeconfigSecretName(cluster, name string) bool {
	base := clusterKubeconfigSecretName(cluster)
	if name == base {
		return true
	}
	if !strings.HasPrefix(name, base+"-") || len(name) != len(base)+33 {
		return false
	}
	_, err := hex.DecodeString(strings.TrimPrefix(name, base+"-"))
	return err == nil
}

func (h clustersHandler) cleanupKubeconfigCredential(ctx context.Context, cluster string, secret *corev1.Secret) {
	if secret == nil || !ownedKubeconfigSecretName(cluster, secret.Name) || secret.Labels[kube.ClusterKubeconfigLabel] != "true" || secret.Labels[ManagedByLabel] != managedByValue {
		return
	}
	// UID/resourceVersion preconditions make stale cleanup harmless.
	_ = h.k.Secrets(h.namespace).Delete(ctx, secret.Name, metav1.DeleteOptions{Preconditions: objectDeletePreconditions(secret)})
}
