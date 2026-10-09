// Clusters wires the multi-cluster registration surface:
//
//   - GET    /clusters              — list registered remote clusters
//   - POST   /clusters              — register a new remote cluster
//   - DELETE /clusters/{name}       — unregister a cluster

package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"sort"
	"time"

	"github.com/go-chi/chi/v5"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/util/retry"

	"github.com/GameplanePanel/gameplane/api/internal/auth"
	"github.com/GameplanePanel/gameplane/api/internal/httperr"
	"github.com/GameplanePanel/gameplane/api/internal/kube"
)

// MountClusters wires /clusters onto the supplied router.
func MountClusters(r chi.Router, reg *kube.Registry, k *kube.Client, ns string) {
	h := clustersHandler{reg: reg, k: k, namespace: ns}
	r.Route("/clusters", func(r chi.Router) {
		r.Get("/", h.list)
		r.Post("/", h.create)
		r.Delete("/{name}", h.delete)
		r.Put("/{name}/kubeconfig", h.replaceKubeconfig)
		r.Put("/{name}/gateway", h.configureGateway)
		r.Delete("/{name}/gateway", h.removeGateway)
	})
}

// clusterKubeconfigSecretName returns the Kubernetes Secret name that POST /clusters
// generates for a cluster's kubeconfig.
func clusterKubeconfigSecretName(cluster string) string {
	return "cluster-" + cluster + "-kubeconfig"
}

// deleteClusterKubeconfigSecret deletes only the API's fixed-name or rotated
// kubeconfig credential. Legacy fixed names predate managed-by labelling;
// rotated names require both ownership labels. Other references are preserved.
func deleteClusterKubeconfigSecret(ctx context.Context, k *kube.Client, ns, cluster, secretName string) error {
	secret, err := clusterKubeconfigSecret(ctx, k, ns, cluster, secretName)
	if err != nil {
		return err
	}
	return k.Secrets(ns).Delete(ctx, secretName, metav1.DeleteOptions{Preconditions: objectDeletePreconditions(secret)})
}

func clusterKubeconfigSecret(ctx context.Context, k *kube.Client, ns, cluster, secretName string) (*corev1.Secret, error) {
	// Permit the legacy fixed name and API-generated immutable rotations.
	if !ownedKubeconfigSecretName(cluster, secretName) {
		return nil, apierrors.NewNotFound(corev1.Resource("secrets"), secretName)
	}

	// Fetch the Secret to check its labels.
	secret, err := k.Secrets(ns).Get(ctx, secretName, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}

	// Only delete if it carries the kubeconfig label.
	if secret.Labels[kube.ClusterKubeconfigLabel] != "true" {
		return nil, apierrors.NewNotFound(corev1.Resource("secrets"), secretName)
	}
	// Legacy API secrets preceded the managed-by label. Rotated names are new
	// and must have explicit ownership before they qualify for cleanup.
	if secretName != clusterKubeconfigSecretName(cluster) && secret.Labels[ManagedByLabel] != managedByValue {
		return nil, apierrors.NewNotFound(corev1.Resource("secrets"), secretName)
	}

	return secret, nil
}

type clustersHandler struct {
	reg       *kube.Registry
	k         *kube.Client
	namespace string
}

// clusterRegistryView is the public projection of a remote cluster. Never includes kubeconfig data.
type clusterRegistryView struct {
	CanViewInventory bool   `json:"canViewInventory"`
	Name             string `json:"name"`
	DisplayName      string `json:"displayName"`
	Phase            string `json:"phase"`
	Message          string `json:"message,omitempty"`
	ServerVersion    string `json:"serverVersion,omitempty"`
	LastCheckTime    string `json:"lastCheckTime,omitempty"`
}

type clusterCreateReq struct {
	Name        string `json:"name"`
	DisplayName string `json:"displayName"`
	Kubeconfig  string `json:"kubeconfig"`
}

type clustersListResp struct {
	Items []clusterRegistryView `json:"items"`
}

func (h clustersHandler) list(w http.ResponseWriter, req *http.Request) {
	u := auth.UserFromContext(req.Context())
	if u == nil {
		http.Error(w, "unauthenticated", http.StatusUnauthorized)
		return
	}
	out := clustersListResp{Items: make([]clusterRegistryView, 0)}
	local := h.reg.DefaultID()
	if h.reg.Default() != nil && u.CanDiscoverCluster(local) {
		out.Items = append(out.Items, clusterRegistryView{Name: local, Phase: "Healthy",
			CanViewInventory: u.Can("cluster:read", true, local, "")})
	}
	// Persisted registrations remain discoverable when a kubeconfig cannot
	// load; the client registry alone would silently drop those clusters.
	registrations, err := h.k.Clusters().List(req.Context(), metav1.ListOptions{})
	if err != nil {
		// Older single-cluster installations may not have the optional CRD.
		// Only that absence may degrade to local discovery; permission and
		// transport failures must remain visible instead of hiding remotes.
		if apierrors.IsNotFound(err) {
			writeJSON(w, out)
			return
		}
		httperr.Write(w, req, err)
		return
	}
	sort.Slice(registrations.Items, func(i, j int) bool {
		return registrations.Items[i].GetName() < registrations.Items[j].GetName()
	})
	for _, registration := range registrations.Items {
		id := registration.GetName()
		if id == local || registration.GetDeletionTimestamp() != nil || !u.CanDiscoverCluster(id) {
			continue
		}
		item := clusterRegistryView{Name: id, CanViewInventory: u.Can("cluster:read", true, id, "")}
		item.DisplayName, _, _ = unstructured.NestedString(registration.Object, "spec", "displayName")
		item.Phase, _, _ = unstructured.NestedString(registration.Object, "status", "phase")
		if item.CanViewInventory {
			item.Message, _, _ = unstructured.NestedString(registration.Object, "status", "message")
			item.ServerVersion, _, _ = unstructured.NestedString(registration.Object, "status", "serverVersion")
			item.LastCheckTime, _, _ = unstructured.NestedString(registration.Object, "status", "lastCheckTime")
		}
		if client, ok := h.reg.Get(id); !ok || client == nil || client.Typed == nil {
			item.Phase = "Unhealthy"
			item.Message = "Cluster connection is unavailable"
		}
		out.Items = append(out.Items, item)
	}

	writeJSON(w, out)
}

func (h clustersHandler) create(w http.ResponseWriter, req *http.Request) {
	var in clusterCreateReq
	if err := json.NewDecoder(req.Body).Decode(&in); err != nil {
		httperr.Write(w, req, err)
		return
	}

	// Validate name.
	if !dnsLabelRE.MatchString(in.Name) {
		httperr.WriteCode(w, req, http.StatusBadRequest,
			errors.New("name must be a DNS label (lowercase, digits, hyphens)"))
		return
	}
	if in.Name == h.reg.DefaultID() || in.Name == "*" {
		httperr.WriteCode(w, req, http.StatusBadRequest,
			errors.New("cluster name conflicts with reserved names"))
		return
	}

	// Validate kubeconfig.
	if in.Kubeconfig == "" {
		httperr.WriteCode(w, req, http.StatusBadRequest,
			errors.New("kubeconfig is required"))
		return
	}
	err := h.validateRemoteKubeconfig([]byte(in.Kubeconfig))
	if err != nil {
		httperr.WriteCode(w, req, http.StatusBadRequest, errors.New("invalid or disallowed kubeconfig"))
		return
	}

	// Create the kubeconfig Secret in the control-plane namespace. The
	// managed-by label marks it as created by the API, which is what lets
	// DELETE /clusters/{name} remove it again.
	secretName := clusterKubeconfigSecretName(in.Name)
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      secretName,
			Namespace: h.namespace,
			Labels: map[string]string{
				kube.ClusterKubeconfigLabel: "true",
				ManagedByLabel:              managedByValue,
			},
		},
		Type: corev1.SecretTypeOpaque,
		Data: map[string][]byte{
			"kubeconfig": []byte(in.Kubeconfig),
		},
	}
	// Create the Cluster CR.
	clusterCR := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "gameplane.local/v1alpha1",
			"kind":       "Cluster",
			"metadata": map[string]any{
				"name": in.Name,
			},
			"spec": map[string]any{
				"displayName": in.DisplayName,
				"kubeconfigSecret": map[string]any{
					"name": secretName,
					"key":  "kubeconfig",
				},
			},
		},
	}
	if h.k.RegisterCluster != nil {
		_, err = h.k.RegisterCluster(req.Context(), h.namespace, secret, clusterCR)
	} else if h.k.IsStandalone() {
		err = errors.New("atomic registration storage is unavailable")
	} else {
		err = h.registerKubernetesCluster(req.Context(), secret, clusterCR)
	}
	if err != nil {
		if apierrors.IsAlreadyExists(err) {
			httperr.WriteCode(w, req, http.StatusConflict, errors.New("cluster already exists"))
			return
		}
		httperr.Write(w, req, err)
		return
	}

	writeJSONCreated(w, clusterRegistryView{
		Name:        in.Name,
		DisplayName: in.DisplayName,
		Phase:       "", // Will be populated by the operator
	})
}

// Kubernetes cannot transact across a Secret and a CR. Pin cleanup to the
// credential we created and use a bounded independent context so cancellation
// does not strand it. An ambiguous CR write retains possibly-live credentials.
func (h clustersHandler) registerKubernetesCluster(ctx context.Context, secret *corev1.Secret, registration *unstructured.Unstructured) error {
	created, err := h.k.Secrets(h.namespace).Create(ctx, secret, metav1.CreateOptions{})
	if err != nil {
		return err
	}
	if _, err := h.k.Clusters().Create(ctx, registration, metav1.CreateOptions{}); err != nil {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		current, readErr := h.k.Clusters().Get(cleanupCtx, registration.GetName(), metav1.GetOptions{})
		ref := ""
		if readErr == nil {
			ref, _, _ = unstructured.NestedString(current.Object, "spec", "kubeconfigSecret", "name")
		}
		if apierrors.IsNotFound(readErr) || (readErr == nil && ref != created.Name) {
			_ = h.k.Secrets(h.namespace).Delete(cleanupCtx, created.Name, metav1.DeleteOptions{Preconditions: objectDeletePreconditions(created)})
		}
		return err
	}
	return nil
}

func (h clustersHandler) delete(w http.ResponseWriter, req *http.Request) {
	name := chi.URLParam(req, "name")

	// Reject deletion of the default cluster.
	if name == h.reg.DefaultID() {
		httperr.WriteCode(w, req, http.StatusBadRequest,
			errors.New("cannot delete the local cluster"))
		return
	}

	// Read the Cluster CR to find the Secret name.
	u, err := h.k.Clusters().Get(req.Context(), name, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			httperr.WriteCode(w, req, http.StatusNotFound, err)
			return
		}
		httperr.Write(w, req, err)
		return
	}

	// Pin deletion to the registration we read. A health update may advance
	// resourceVersion; a new registration with this name must survive.
	originalUID := u.GetUID()
	var kubeconfigSecret *corev1.Secret
	ctx := req.Context()
	err = retry.RetryOnConflict(retry.DefaultRetry, func() error {
		current, err := h.k.Clusters().Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return err
		}
		if current.GetUID() != originalUID {
			return apierrors.NewNotFound(kube.GVRCluster.GroupResource(), name)
		}
		secretName, _, _ := unstructured.NestedString(current.Object, "spec", "kubeconfigSecret", "name")
		kubeconfigSecret, err = clusterKubeconfigSecret(ctx, h.k, h.namespace, name, secretName)
		if err != nil && !apierrors.IsNotFound(err) {
			return err
		}
		if err := h.k.Clusters().Delete(ctx, name, metav1.DeleteOptions{Preconditions: objectDeletePreconditions(current)}); err != nil {
			return err
		}
		u = current
		return nil
	})
	if err != nil {
		httperr.Write(w, req, err)
		return
	}

	// Drop the cluster's client now instead of waiting for the cluster
	// watch, so no request is dispatched through a removed registration.
	h.reg.RemoveIfUID(name, originalUID)
	// Only remove credentials this API owns; external GitOps credentials survive.
	h.cleanupGatewayCredential(req.Context(), name, gatewayCredentialName(u))

	// Clean up the fixed-name or immutable rotated credential captured before
	// deletion. Legacy fixed names predate managed-by; rotated names require it.
	// UID/version preconditions preserve replacements and external references.
	if kubeconfigSecret != nil {
		if err := h.k.Secrets(h.namespace).Delete(req.Context(), kubeconfigSecret.Name, metav1.DeleteOptions{Preconditions: objectDeletePreconditions(kubeconfigSecret)}); err != nil && !apierrors.IsNotFound(err) {
			slog.Warn("cluster delete: kubeconfig secret cleanup failed", "cluster", name, "err", err)
		}
	}

	w.WriteHeader(http.StatusNoContent)
}
