package handlers

import (
	"context"
	"net/http"
	"testing"

	"github.com/GameplanePanel/gameplane/api/internal/kube"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func TestStandaloneRegistrationRecoversOwnedMatchingOrphanThroughRoute(t *testing.T) {
	store, home, reg, _ := standaloneHandlerStore(t)
	secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: clusterKubeconfigSecretName("remote"), Labels: map[string]string{kube.ClusterKubeconfigLabel: "true", ManagedByLabel: managedByValue}}, Data: map[string][]byte{"kubeconfig": []byte(standaloneTestKubeconfig)}}
	created, err := home.Secrets(standaloneTestNamespace).Create(t.Context(), secret, metav1.CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	rr := doClusters(t, standaloneClustersRouter(home, reg), http.MethodPost, "/clusters/", clusterCreateReq{Name: "remote", Kubeconfig: standaloneTestKubeconfig})
	if rr.Code != http.StatusCreated {
		t.Fatalf("retry must recover the orphan: %d %s", rr.Code, rr.Body)
	}
	current, err := home.Secrets(standaloneTestNamespace).Get(t.Context(), secret.Name, metav1.GetOptions{})
	if err != nil || current.UID != created.UID || current.ResourceVersion != created.ResourceVersion {
		t.Fatal("retry changed the legacy credential identity")
	}
	assertManagementCiphertext(t, store, secret.Name, standaloneTestKubeconfig)
}

type cancelledRegistrationStore struct {
	kube.ClusterStore
	cancel context.CancelFunc
}

func (s cancelledRegistrationStore) Create(context.Context, *unstructured.Unstructured, metav1.CreateOptions, ...string) (*unstructured.Unstructured, error) {
	s.cancel()
	return nil, context.Canceled
}

func TestKubernetesRegistrationCancellationUsesIndependentCleanupContext(t *testing.T) {
	home := fakeKubeClientWithClusters()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	home.ClusterStore = cancelledRegistrationStore{ClusterStore: home.Clusters(), cancel: cancel}
	secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "cluster-remote-kubeconfig", Labels: map[string]string{kube.ClusterKubeconfigLabel: "true"}}, Data: map[string][]byte{"kubeconfig": []byte(standaloneTestKubeconfig)}}
	h := clustersHandler{k: home, namespace: standaloneTestNamespace}
	if err := h.registerKubernetesCluster(ctx, secret, newCluster("remote", nil, nil)); err == nil {
		t.Fatal("cancelled registration succeeded")
	}
	if _, err := home.Secrets(standaloneTestNamespace).Get(t.Context(), secret.Name, metav1.GetOptions{}); !apierrors.IsNotFound(err) {
		t.Fatalf("cancelled Kubernetes registration stranded a credential: %v", err)
	}
}
