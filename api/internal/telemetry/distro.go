package telemetry

import (
	"strings"

	corev1 "k8s.io/api/core/v1"
)

// detectDistro names the Kubernetes distribution from signals the API can
// already read, without any new RBAC. The first matching rule wins, in the
// order of research R16: the server's gitVersion suffix, then well-known node
// labels, then the node providerID prefix, then the node OS image. Anything
// else, including plain kubeadm, is "other".
func detectDistro(gitVersion string, nodes []corev1.Node) string {
	for _, r := range []struct{ name, marker string }{
		{"k3s", "+k3s"},
		{"rke2", "+rke2"},
		{"k0s", "+k0s"},
		{"eks", "-eks-"},
		{"gke", "-gke."},
	} {
		if strings.Contains(gitVersion, r.marker) {
			return r.name
		}
	}
	for _, r := range []struct{ name, label string }{
		{"aks", "kubernetes.azure.com/cluster"},
		{"openshift", "node.openshift.io/os_id"},
		{"microk8s", "microk8s.io/cluster"},
		{"minikube", "minikube.k8s.io/name"},
	} {
		for i := range nodes {
			if _, ok := nodes[i].Labels[r.label]; ok {
				return r.name
			}
		}
	}
	for _, r := range []struct{ name, prefix string }{
		{"kind", "kind://"},
		{"doks", "digitalocean://"},
	} {
		for i := range nodes {
			if strings.HasPrefix(nodes[i].Spec.ProviderID, r.prefix) {
				return r.name
			}
		}
	}
	for i := range nodes {
		if strings.HasPrefix(nodes[i].Status.NodeInfo.OSImage, "Talos") {
			return "talos"
		}
	}
	return "other"
}
