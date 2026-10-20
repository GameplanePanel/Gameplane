package handlers

import (
	"net/http"
	"strings"
	"testing"

	"github.com/GameplanePanel/gameplane/api/internal/kube"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestStandaloneRegistrationAndRotationRejectUnsafeDestinations(t *testing.T) {
	_, home, reg, _ := standaloneHandlerStore(t)
	router := standaloneClustersRouter(home, reg)
	if rr := doClusters(t, router, http.MethodPost, "/clusters/", clusterCreateReq{Name: "remote", Kubeconfig: standaloneTestKubeconfig}); rr.Code != http.StatusCreated {
		t.Fatalf("initial registration: %d %s", rr.Code, rr.Body)
	}
	for _, server := range []string{"http://remote.example", "https://127.0.0.1", "https://169.254.169.254", "https://metadata.google.internal"} {
		t.Run(server, func(t *testing.T) {
			config := strings.ReplaceAll(standaloneTestKubeconfig, "https://remote.example.invalid", server)
			for _, request := range []struct {
				method, path string
				body         any
			}{
				{http.MethodPost, "/clusters/", clusterCreateReq{Name: "blocked", Kubeconfig: config}},
				{http.MethodPut, "/clusters/remote/kubeconfig", map[string]string{"kubeconfig": config}},
			} {
				rr := doClusters(t, router, request.method, request.path, request.body)
				if rr.Code != http.StatusBadRequest {
					t.Fatalf("%s unsafe endpoint accepted: %d %s", request.method, rr.Code, rr.Body)
				}
			}
		})
	}
	secrets, err := home.Secrets(standaloneTestNamespace).List(t.Context(), metav1.ListOptions{})
	if err != nil || len(secrets.Items) != 1 {
		t.Fatalf("rejected operations persisted credentials: %v", err)
	}
}

func TestStandaloneGatewayAppliesOperatorDestinationRestriction(t *testing.T) {
	_, home, reg, _ := standaloneHandlerStore(t)
	policy, err := kube.NewRemoteAccessPolicy([]string{"10.20.0.0/16"})
	if err != nil {
		t.Fatal(err)
	}
	home.RemoteAccess = policy
	if _, err := home.Clusters().Create(t.Context(), newCluster("remote", map[string]any{}, nil), metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	router := standaloneClustersRouter(home, reg)
	in := gatewayCertificateRequest(t)
	for _, destination := range []string{"https://127.0.0.1:8443", "https://10.21.0.1:8443"} {
		in.URL = destination
		if rr := doClusters(t, router, http.MethodPut, "/clusters/remote/gateway", in); rr.Code != http.StatusBadRequest {
			t.Fatalf("unsafe gateway accepted: %d %s", rr.Code, rr.Body)
		}
	}
	in.URL = "https://10.20.0.1:8443"
	if rr := doClusters(t, router, http.MethodPut, "/clusters/remote/gateway", in); rr.Code != http.StatusNoContent {
		t.Fatalf("allowed gateway rejected: %d %s", rr.Code, rr.Body)
	}
}
