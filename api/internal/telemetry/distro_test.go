package telemetry

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// testNode builds a Node with just the fields detectDistro and nodeArches read.
func testNode(name string, labels map[string]string, providerID, osImage, arch string) corev1.Node {
	return corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: name, Labels: labels},
		Spec:       corev1.NodeSpec{ProviderID: providerID},
		Status: corev1.NodeStatus{NodeInfo: corev1.NodeSystemInfo{
			OSImage:      osImage,
			Architecture: arch,
		}},
	}
}

func TestDetectDistro_EveryRow(t *testing.T) {
	plain := testNode("n", nil, "", "Ubuntu 24.04", "amd64")
	cases := []struct {
		name       string
		gitVersion string
		nodes      []corev1.Node
		want       string
	}{
		{"k3s", "v1.31.2+k3s1", []corev1.Node{plain}, "k3s"},
		{"rke2", "v1.30.4+rke2r1", []corev1.Node{plain}, "rke2"},
		{"k0s", "v1.31.1+k0s", []corev1.Node{plain}, "k0s"},
		{"eks", "v1.29.4-eks-036c24b", []corev1.Node{plain}, "eks"},
		{"gke", "v1.29.4-gke.1043002", []corev1.Node{plain}, "gke"},
		{"aks label", "v1.30.0", []corev1.Node{testNode("n", map[string]string{"kubernetes.azure.com/cluster": "MC_x"}, "", "", "amd64")}, "aks"},
		{"openshift label", "v1.30.0", []corev1.Node{testNode("n", map[string]string{"node.openshift.io/os_id": "rhcos"}, "", "", "amd64")}, "openshift"},
		{"microk8s label", "v1.30.0", []corev1.Node{testNode("n", map[string]string{"microk8s.io/cluster": "true"}, "", "", "amd64")}, "microk8s"},
		{"minikube label", "v1.30.0", []corev1.Node{testNode("n", map[string]string{"minikube.k8s.io/name": "minikube"}, "", "", "amd64")}, "minikube"},
		{"kind providerID", "v1.31.0", []corev1.Node{testNode("n", nil, "kind://docker/kind/kind-control-plane", "", "amd64")}, "kind"},
		{"doks providerID", "v1.30.2", []corev1.Node{testNode("n", nil, "digitalocean://123456", "", "amd64")}, "doks"},
		{"talos osImage", "v1.31.0", []corev1.Node{testNode("n", nil, "", "Talos (v1.8.0)", "amd64")}, "talos"},
		{"plain kubeadm is other", "v1.31.0", []corev1.Node{plain}, "other"},
		{"no nodes and no marker is other", "", nil, "other"},
		{"match on a later node", "v1.30.0", []corev1.Node{plain, testNode("w", nil, "kind://docker/kind/w", "", "arm64")}, "kind"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := detectDistro(tc.gitVersion, tc.nodes); got != tc.want {
				t.Fatalf("detectDistro(%q) = %q, want %q", tc.gitVersion, got, tc.want)
			}
		})
	}
}

// The first matching row wins, in the order of research R16.
func TestDetectDistro_Precedence(t *testing.T) {
	kindNode := testNode("n", map[string]string{"minikube.k8s.io/name": "m"}, "kind://docker/x", "Talos", "amd64")
	if got := detectDistro("v1.31.0+k3s1", []corev1.Node{kindNode}); got != "k3s" {
		t.Errorf("gitVersion marker must beat labels and providerID, got %q", got)
	}
	if got := detectDistro("v1.31.0", []corev1.Node{kindNode}); got != "minikube" {
		t.Errorf("a node label must beat providerID and osImage, got %q", got)
	}
	noLabel := testNode("n", nil, "kind://docker/x", "Talos", "amd64")
	if got := detectDistro("v1.31.0", []corev1.Node{noLabel}); got != "kind" {
		t.Errorf("providerID must beat osImage, got %q", got)
	}
	if got := detectDistro("v1.31.0+k3s1-eks-1", nil); got != "k3s" {
		t.Errorf("k3s must be checked before eks, got %q", got)
	}
}
