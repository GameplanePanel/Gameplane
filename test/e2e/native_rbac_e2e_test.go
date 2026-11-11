//go:build e2e

package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	authorizationv1 "k8s.io/api/authorization/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

const panelNamespace = "gameplane-system"

func verifyLocalClusterDiscovery(t *testing.T, client *APIClient) {
	t.Helper()
	resp, body, err := client.Get("/clusters")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var result struct{ Items []struct{ Name string } }
	if resp.StatusCode != http.StatusOK || json.Unmarshal(body, &result) != nil ||
		len(result.Items) != 1 || result.Items[0].Name != "local" {
		t.Fatalf("local cluster discovery returned %d: %s", resp.StatusCode, body)
	}
}

func nativeReadBinding(t *testing.T) types.UID {
	t.Helper()
	binding, err := envInstance.K8s.RbacV1().ClusterRoleBindings().Get(t.Context(), "gameplane-api-read", metav1.GetOptions{})
	if err != nil || binding.RoleRef.Kind != "ClusterRole" || binding.RoleRef.Name != "gameplane-api-read" || binding.UID == "" {
		t.Fatalf("native API read binding: %v, %v", binding, err)
	}
	return binding.UID
}

func checkNativeCatalogAccess(t *testing.T, template string, allow bool) {
	t.Helper()
	out, err := envInstance.Kubectl(t.Context(), "--as=system:serviceaccount:gameplane-system:gameplane-api",
		"annotate", "gametemplate", template, "e2e-rbac=verified", "--overwrite", "--dry-run=server")
	if (allow && err != nil) || (!allow && (err == nil || !strings.Contains(out, "Forbidden"))) {
		t.Fatalf("catalog mutation allow=%t: %v: %s", allow, err, out)
	}
}

type nativeGrant struct {
	actor, verb, group, resource, subresource, namespace, name string
	allow                                                      bool
}

func verifyNativeGrants(t *testing.T, grants []nativeGrant) {
	t.Helper()
	for _, g := range grants {
		ns, sa, ok := strings.Cut(g.actor, "/")
		if !ok {
			t.Fatalf("invalid service account %q", g.actor)
		}
		sar := &authorizationv1.SubjectAccessReview{Spec: authorizationv1.SubjectAccessReviewSpec{
			User:   "system:serviceaccount:" + ns + ":" + sa,
			Groups: []string{"system:authenticated", "system:serviceaccounts", "system:serviceaccounts:" + ns},
			ResourceAttributes: &authorizationv1.ResourceAttributes{Namespace: g.namespace, Verb: g.verb,
				Group: g.group, Resource: g.resource, Subresource: g.subresource, Name: g.name},
		}}
		// Bindings are applied through a different apiserver watch from the
		// Deployment rollout. Tolerate authorizer propagation on transitions.
		envInstance.Eventually(t, 30*time.Second, func() (bool, string) {
			verdict, err := envInstance.K8s.AuthorizationV1().SubjectAccessReviews().Create(t.Context(), sar, metav1.CreateOptions{})
			if err != nil {
				return false, err.Error()
			}
			return verdict.Status.EvaluationError == "" && verdict.Status.Allowed == g.allow,
				fmt.Sprintf("%+v: %+v", g, verdict.Status)
		})
	}
}

func checkNativeRBACUpgrade(t *testing.T, games, game, template string, legacyReadBinding types.UID) {
	t.Helper()
	if nativeReadBinding(t) != legacyReadBinding {
		t.Fatal("upgrade replaced the legacy read binding")
	}
	checkNativeCatalogAccess(t, template, true)
	const api = panelNamespace + "/gameplane-api"
	const operator = panelNamespace + "/gameplane-operator"
	const plane = "gameplane.local"
	const snapshots = "snapshot.storage.k8s.io"
	peer := game + "-peer"
	applyBusyboxGameServer(t, games, peer, template)
	waitGameContainerReady(t, games, peer+"-0", 3*time.Minute)
	agent, waker := games+"/"+game+"-agent", games+"/"+game+"-waker-sa"
	peerAgent, peerWaker := games+"/"+peer+"-agent", games+"/"+peer+"-waker-sa"
	extra := "e2e-rbac-extra"
	_, err := envInstance.K8s.CoreV1().Namespaces().Create(t.Context(), &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{
		Name: extra, Labels: map[string]string{"pod-security.kubernetes.io/enforce": "restricted"},
	}}, metav1.CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = envInstance.K8s.CoreV1().Namespaces().Delete(context.Background(), extra, metav1.DeleteOptions{})
	})
	checkSnapshotContentGrant := prepareSnapshotContentCheck(t, games, game)
	for _, full := range []bool{true, false} {
		if !full {
			helmUpgradeReuse(t.Context(), t, "nativeRBAC.remoteClusters=false", "nativeRBAC.catalogAdmin=false",
				"nativeRBAC.podAttach=false", "nativeRBAC.operatorStopAttach=false", "nativeRBAC.gameSecretManagement=false",
				"nativeRBAC.snapshotContentReclamation=false", "nativeRBAC.runtimeSecretNames[0]=idp-a",
				"operator.gameNamespaces[0]="+extra, "nativeRBAC.additionalAPIGameNamespaces[0]="+extra)
		}
		verifyNativeGrants(t, []nativeGrant{
			{api, "list", plane, "gametemplates", "", "", "", true},
			{api, "patch", plane, "gametemplates", "", "", template, full},
			{api, "create", plane, "modules", "", "", "", full},
			{api, "create", plane, "clusters", "", "", "", full},
			{api, "list", plane, "clusters", "", "", "", full},
			{api, "get", "", "secrets", "", credentialsNS, "cluster-canary-kubeconfig", full},
			{api, "get", "", "secrets", "", credentialsNS, "idp-a", true},
			{api, "list", "", "secrets", "", credentialsNS, "", false},
			{api, "update", "", "secrets", "", credentialsNS, "idp-a", full},
			{api, "get", "", "secrets", "", panelNamespace, "gameplane-agent-ca", false},
			{api, "list", "", "secrets", "", games, "", full},
			{api, "get", "", "pods", "log", games, game + "-0", true},
			{api, "create", "", "pods", "attach", games, game + "-0", full},
			{api, "create", plane, "gameservers", "", extra, "", !full},
			{api, "create", plane, "gameservers", "", "default", "", false},
			{api, "approve", "certificates.k8s.io", "signers", "", "", "kubernetes.io/kube-apiserver-client", false},
			{operator, "get", "", "secrets", "", panelNamespace, "gameplane-agent-ca", true},
			{operator, "get", "", "secrets", "", credentialsNS, "cluster-canary-kubeconfig", full},
			{operator, "get", "", "secrets", "", "default", "unrelated", false},
			{operator, "delete", snapshots, "volumesnapshotcontents", "", "", "rbac-content", full},
			{operator, "create", snapshots, "volumesnapshots", "", games, "", true},
			{operator, "create", "", "pods", "attach", games, game + "-0", full},
			{agent, "patch", plane, "gameservers", "status", games, game, true},
			{agent, "patch", plane, "gameservers", "status", games, peer, false},
			{agent, "patch", plane, "gameservers", "status", extra, game, false},
			{agent, "get", "", "secrets", "", panelNamespace, "gameplane-agent-ca", false},
			{peerAgent, "patch", plane, "gameservers", "status", games, peer, true},
			{waker, "patch", plane, "gameservers", "", games, game, true},
			{waker, "patch", plane, "gameservers", "", games, peer, false},
			{waker, "patch", plane, "gameservers", "status", games, game, false},
			{peerWaker, "patch", plane, "gameservers", "", games, peer, true},
		})
		verifyAPIIssuerDenied(t, gameplaneServiceAccountClient(t, "gameplane-api"))
		checkNativeCatalogAccess(t, template, full)
		checkSnapshotContentGrant(full)
	}
	checkStandaloneNativeGrants(t, games, game)
	// The enclosing upgrade fixture uninstalls the operator after this
	// check. Let it finish the peer's finalizer while it can still reconcile.
	servers := envInstance.Dyn.Resource(gameServerGVR).Namespace(games)
	if err := servers.Delete(t.Context(), peer, metav1.DeleteOptions{}); err != nil {
		t.Fatal(err)
	}
	envInstance.Eventually(t, 3*time.Minute, func() (bool, string) {
		_, err := servers.Get(t.Context(), peer, metav1.GetOptions{})
		return apierrors.IsNotFound(err), fmt.Sprintf("waiting for peer finalizer: %v", err)
	})
}

// Install the same CSI API definitions as the controller's envtest. A real
// apiserver authorizes the operator's deletion request. A server-side dry run
// avoids deleting or provisioning any underlying storage.
func prepareSnapshotContentCheck(t *testing.T, ns, game string) func(bool) {
	t.Helper()
	crds := filepath.Join("..", "..", "operator", "test", "crds", "snapshot")
	if out, err := envInstance.Kubectl(t.Context(), "apply", "--server-side", "-f", crds); err != nil {
		t.Fatalf("install CSI CRDs: %v: %s", err, out)
	}
	if out, err := envInstance.Kubectl(t.Context(), "wait", "--for=condition=Established",
		"crd/volumesnapshotcontents.snapshot.storage.k8s.io", "--timeout=1m"); err != nil {
		t.Fatalf("wait for snapshot content API: %v: %s", err, out)
	}
	const content = "e2e-gameplane-rbac-content"
	obj := fmt.Sprintf(`{"apiVersion":"snapshot.storage.k8s.io/v1","kind":"VolumeSnapshotContent","metadata":{"name":%q},"spec":{"deletionPolicy":"Retain","driver":"e2e.gameplane.local","source":{"snapshotHandle":"e2e-only"},"volumeSnapshotRef":{"name":%q,"namespace":%q}}}`, content, game, ns)
	if out, err := envInstance.KubectlWithStdin(t.Context(), obj, "create", "-f", "-"); err != nil {
		t.Fatalf("create snapshot content: %v: %s", err, out)
	}
	t.Cleanup(func() {
		_, _ = envInstance.Kubectl(context.Background(), "delete", "volumesnapshotcontent", content, "--ignore-not-found", "--wait=false")
	})
	return func(allow bool) {
		t.Helper()
		out, err := envInstance.Kubectl(t.Context(), "--as=system:serviceaccount:gameplane-system:gameplane-operator",
			"delete", "volumesnapshotcontent", content, "--dry-run=server")
		if (allow && err != nil) || (!allow && (err == nil || !strings.Contains(out, "Forbidden"))) {
			t.Fatalf("operator snapshot content deletion allow=%t: %v: %s", allow, err, out)
		}
	}
}

func checkStandaloneNativeGrants(t *testing.T, games, game string) {
	t.Helper()
	const ns = "e2e-rbac-standalone"
	tag := envInstance.Tag
	if tag == "" {
		tag = "e2e"
	}
	chart := filepath.Join("..", "..", "charts", "gameplane")
	cmd := exec.CommandContext(t.Context(), "helm", "install", "rbac-panel", chart, "--namespace", ns,
		"--kube-context", envInstance.Context, "--create-namespace",
		"--set", "image.registry=gameplane-test,image.tag="+tag+",operator.enabled=false,api.standalone=true,web.enabled=false,ingress.enabled=false",
		"--wait", "--timeout", "4m")
	t.Cleanup(func() {
		_ = envInstance.K8s.CoreV1().Namespaces().Delete(context.Background(), ns, metav1.DeleteOptions{})
	})
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("install standalone panel: %v: %s", err, out)
	}
	deployment, err := envInstance.K8s.AppsV1().Deployments(ns).Get(t.Context(), "gameplane-api", metav1.GetOptions{})
	if err != nil || deployment.Spec.Template.Spec.ServiceAccountName != "gameplane-api" ||
		deployment.Spec.Template.Spec.AutomountServiceAccountToken == nil || *deployment.Spec.Template.Spec.AutomountServiceAccountToken {
		t.Fatalf("standalone panel unexpectedly uses local API credentials: %v", err)
	}
	actor := ns + "/gameplane-api"
	verifyNativeGrants(t, []nativeGrant{
		{actor, "list", "gameplane.local", "clusters", "", "", "", false},
		{actor, "create", "gameplane.local", "gametemplates", "", "", "", false},
		{actor, "get", "", "secrets", "", panelNamespace, "gameplane-agent-ca", false},
		{actor, "get", "", "pods", "attach", games, game + "-0", false},
	})
	if _, err := envInstance.K8s.RbacV1().ClusterRoleBindings().Get(t.Context(), "rbac-panel-api-read", metav1.GetOptions{}); !apierrors.IsNotFound(err) {
		t.Fatalf("standalone panel has an unexpected native API binding: %v", err)
	}
}
