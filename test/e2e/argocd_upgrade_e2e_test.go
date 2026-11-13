//go:build e2e

package e2e

import (
	"bytes"
	"context"
	"crypto"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"math/big"
	"net/http"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
)

var argoAppGVR = schema.GroupVersionResource{Group: "argoproj.io", Version: "v1alpha1", Resource: "applications"}
var argoSyncOptions = []any{"ServerSideApply=true", "ClientSideApplyMigration=false", "FailOnSharedResource=true"}

func checkArgoUpgrade(t *testing.T, ns, game, marker, username, password, credentialsNS string) {
	t.Helper()
	const system = "gameplane-system"
	const workload = "gameplane"
	ctx := t.Context()
	repo, revision := argoGitSource(t)
	projectName := "upgrade-fixture"
	destination := func(namespace string) map[string]any {
		return map[string]any{"server": "https://kubernetes.default.svc", "namespace": namespace}
	}
	projectGVR := schema.GroupVersionResource{Group: "argoproj.io", Version: "v1alpha1", Resource: "appprojects"}
	// The preceding RBAC phase left a reduced privilege profile and a second
	// managed namespace. Carry those settings through adoption without copying
	// arbitrary release values, such as credentials, into an Application.
	var permissions struct {
		NativeRBAC map[string]any `json:"nativeRBAC"`
		Operator   struct {
			GameNamespaces []string `json:"gameNamespaces"`
		} `json:"operator"`
	}
	rawValues, err := exec.CommandContext(ctx, "helm", "get", "values", workload, "--namespace", system,
		"--kube-context", envInstance.Context, "-o", "json").Output()
	if err != nil {
		t.Fatalf("read installed privilege profile: %v", err)
	}
	if err := json.Unmarshal(rawValues, &permissions); err != nil {
		t.Fatal(err)
	}
	if permissions.NativeRBAC["catalogAdmin"] != false {
		t.Fatal("expected the reduced native RBAC profile before GitOps adoption")
	}
	destinations := []any{destination(system), destination(ns), destination(credentialsNS)}
	var gameNamespaces []any
	for _, n := range permissions.Operator.GameNamespaces {
		destinations = append(destinations, destination(n))
		gameNamespaces = append(gameNamespaces, n)
	}
	var clusterResources []any
	for _, resource := range [][2]string{
		{"", "Namespace"},
		{"apiextensions.k8s.io", "CustomResourceDefinition"},
		{"rbac.authorization.k8s.io", "ClusterRole"},
		{"rbac.authorization.k8s.io", "ClusterRoleBinding"},
		{"admissionregistration.k8s.io", "ValidatingAdmissionPolicy"},
		{"admissionregistration.k8s.io", "ValidatingAdmissionPolicyBinding"},
	} {
		clusterResources = append(clusterResources, map[string]any{"group": resource[0], "kind": resource[1]})
	}
	create := func(gvr schema.GroupVersionResource, kind, name string, spec map[string]any) {
		t.Helper()
		resource := envInstance.Dyn.Resource(gvr).Namespace("argocd")
		obj := &unstructured.Unstructured{Object: map[string]any{"apiVersion": "argoproj.io/v1alpha1", "kind": kind,
			"metadata": map[string]any{"name": name, "namespace": "argocd"}, "spec": spec}}
		if _, err := resource.Create(ctx, obj, metav1.CreateOptions{}); err != nil {
			t.Fatal(err)
		}
		// Applications have no cascading-deletion finalizer; the later Helm
		// reinstall phase owns teardown of the populated world.
		t.Cleanup(func() {
			cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
			defer cancel()
			if err := resource.Delete(cleanup, name, metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
				t.Errorf("delete Argo CD %s %s: %v", kind, name, err)
			}
		})
	}
	create(projectGVR, "AppProject", projectName, map[string]any{
		"sourceRepos":              []any{repo},
		"destinations":             destinations,
		"clusterResourceWhitelist": clusterResources,
	})
	app := func(name, path string, helm map[string]any) {
		t.Helper()
		source := map[string]any{"repoURL": repo, "targetRevision": revision, "path": path}
		if helm != nil {
			source["helm"] = helm
		}
		create(argoAppGVR, "Application", name, map[string]any{
			"project": projectName, "source": source, "destination": destination(system),
			"syncPolicy": map[string]any{"syncOptions": argoSyncOptions},
		})
	}
	identity := readUpgradeIdentity(ctx, t, "gameplane-agent-ca")
	claim, err := envInstance.K8s.CoreV1().PersistentVolumeClaims(ns).Get(ctx, game+"-data", metav1.GetOptions{})
	if err != nil || claim.UID == "" {
		t.Fatalf("read populated game PVC: %v", err)
	}
	currentSecret := func(name string) *corev1.Secret {
		t.Helper()
		s, err := envInstance.K8s.CoreV1().Secrets(system).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	immutable := func(name string, kind corev1.SecretType, material map[string][]byte) {
		t.Helper()
		_, err := envInstance.K8s.CoreV1().Secrets(system).Create(ctx, &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: name},
			Type: kind, Immutable: ptr.To(true), Data: material}, metav1.CreateOptions{})
		if err != nil {
			t.Fatal(err)
		}
	}
	firstCA, firstClient := currentSecret("gameplane-agent-ca"), currentSecret("gameplane-agent-client")
	issuer, clientCert := "gameplane-external-ca-v1", "gameplane-external-client-v1"
	immutable(issuer, firstCA.Type, firstCA.Data)
	immutable(clientCert, firstClient.Type, firstClient.Data)
	// Reauthenticate against the current API on every rollout; a stale
	// port-forward or cached /players response cannot verify an agent's mTLS.
	checkWorld := func(issuerSecret, clientSecret string, expectedCA []byte) {
		t.Helper()
		now := readUpgradeIdentity(ctx, t, issuerSecret)
		expected := fmt.Sprintf("%x", sha256.Sum256(expectedCA))
		if now["agent CA fingerprint"] != expected {
			t.Fatal("active issuer fingerprint differs from staged certificate")
		}
		for field, value := range identity {
			if field != "agent CA fingerprint" && now[field] != value {
				t.Fatalf("changed %s: %s -> %s", field, value, now[field])
			}
		}
		sameClaim, err := envInstance.K8s.CoreV1().PersistentVolumeClaims(ns).Get(ctx, game+"-data", metav1.GetOptions{})
		if err != nil || sameClaim.UID != claim.UID {
			t.Fatalf("game PVC was replaced: %v", err)
		}
		envInstance.Eventually(t, 3*time.Minute, func() (bool, string) {
			cert, err := envInstance.K8s.CoreV1().Secrets(ns).Get(ctx, game+"-agent-tls", metav1.GetOptions{})
			return err == nil && bytes.Equal(cert.Data["ca.crt"], expectedCA), "waiting for agent TLS issuance under the selected CA"
		})
		for _, deployment := range []string{"gameplane-api", "gameplane-operator"} {
			out, err := envInstance.Kubectl(ctx, "rollout", "status", "-n", system, "deployment/"+deployment, "--timeout=3m")
			if err != nil {
				t.Fatalf("%s rollout: %v %s", deployment, err, out)
			}
			dep, err := envInstance.K8s.AppsV1().Deployments(system).Get(ctx, deployment, metav1.GetOptions{})
			if err != nil {
				t.Fatal(err)
			}
			mounted := map[string]bool{}
			for _, volume := range dep.Spec.Template.Spec.Volumes {
				if volume.Secret != nil {
					mounted[volume.Secret.SecretName] = true
				}
			}
			if !mounted[issuerSecret] || !mounted[clientSecret] {
				t.Fatalf("%s rolled out without the intended issuer/client Secret references", deployment)
			}
		}
		cli := envInstance.apiClientContext(ctx, t, username, password)
		defer cli.Close()
		envInstance.Eventually(t, 3*time.Minute, func() (bool, string) {
			resp, body, err := cli.DoContext(ctx, http.MethodGet, "/servers/"+game+"/files/read?path=%2Fmarker.txt", nil)
			if err != nil {
				return false, err.Error()
			}
			defer resp.Body.Close()
			return resp.StatusCode == http.StatusOK && string(body) == marker, fmt.Sprintf("reading the seeded world through agent mTLS: HTTP %d", resp.StatusCode)
		})
	}
	checkWorld("gameplane-agent-ca", "gameplane-agent-client", firstCA.Data["ca.crt"])
	app("gameplane-crds", "charts/gameplane/charts/gameplane-crds/crds", nil)
	syncArgo(t, "gameplane-crds", revision, "crds", false, nil)
	trust := map[string]any{
		"caSecretRef":   map[string]any{"name": issuer},
		"clientCertRef": map[string]any{"name": clientCert},
	}
	apiValues := map[string]any{
		"credentialsNamespace": credentialsNS,
		"resources":            map[string]any{"limits": map[string]any{"memory": "1Gi"}},
		"agentMTLS":            trust,
	}
	values := map[string]any{
		"image":               map[string]any{"registry": "gameplane-test", "tag": envInstance.Tag},
		"operator":            map[string]any{"leaderElect": false, "agentImage": "gameplane-test/agent:" + envInstance.Tag, "gameNamespaces": gameNamespaces},
		"nativeRBAC":          permissions.NativeRBAC,
		"podSecurity":         map[string]any{"enforceRestricted": false},
		"web":                 map[string]any{"enabled": false},
		"ingress":             map[string]any{"enabled": false},
		"defaultModuleSource": map[string]any{"enabled": false},
		"uploadModuleSource":  map[string]any{"enabled": false},
		"grafanaDashboards":   map[string]any{"enabled": true},
		"api":                 apiValues,
	}
	helm := map[string]any{"releaseName": "gameplane", "skipCrds": true, "valueFiles": []any{"examples/argocd-values.yaml"}, "valuesObject": values}
	app(workload, "charts/gameplane", helm)
	syncArgo(t, workload, revision, "adopt", false, nil)
	checkWorld(issuer, clientCert, firstCA.Data["ca.crt"])
	checkArgoWaker(t, ns, game)
	dashboards := envInstance.K8s.CoreV1().ConfigMaps(system)
	dashboard, err := dashboards.Get(ctx, "gameplane-operator-dashboard", metav1.GetOptions{})
	if err != nil || dashboard.Annotations["argocd.argoproj.io/tracking-id"] == "" {
		t.Fatalf("expected Argo-owned disposable dashboard before prune: %v", err)
	}
	dbClaim, err := envInstance.K8s.CoreV1().PersistentVolumeClaims(system).Get(ctx, "gameplane-api-data", metav1.GetOptions{})
	if err != nil || !strings.Contains(dbClaim.Annotations["argocd.argoproj.io/sync-options"], "Prune=false") || dbClaim.Annotations["argocd.argoproj.io/tracking-id"] == "" {
		t.Fatalf("expected a tracked, protected SQLite claim before prune: %v", err)
	}
	newClient := "gameplane-external-client-v2"
	immutable(newClient, corev1.SecretTypeTLS, renewArgoClient(t, firstCA))
	values["grafanaDashboards"] = map[string]any{"enabled": false}
	apiValues["storage"] = map[string]any{"existingClaim": "gameplane-api-data"}
	trust["clientCertRef"] = map[string]any{"name": newClient}
	syncArgo(t, workload, revision, "prune-and-renew-client", true, values)
	if _, err := dashboards.Get(ctx, dashboard.Name, metav1.GetOptions{}); !apierrors.IsNotFound(err) {
		t.Fatalf("expected Argo to prune the dashboard: %v", err)
	}
	checkWorld(issuer, newClient, firstCA.Data["ca.crt"])
	replacement, leaf := harvestMTLSPair(ctx, t, filepath.Join("..", "..", "charts", "gameplane"), "argo-rotation-donor")
	issuer, newClient = "gameplane-external-ca-v2", "gameplane-external-client-v3"
	immutable(issuer, corev1.SecretTypeOpaque, map[string][]byte{"ca.crt": []byte(replacement.crt), "ca.key": []byte(replacement.key)})
	immutable(newClient, corev1.SecretTypeTLS, map[string][]byte{"tls.crt": []byte(leaf.crt), "tls.key": []byte(leaf.key)})
	trust["caSecretRef"] = map[string]any{"name": issuer}
	trust["clientCertRef"] = map[string]any{"name": newClient}
	syncArgo(t, workload, revision, "rotate-issuer", true, values)
	checkWorld(issuer, newClient, []byte(replacement.crt))
	checkArgoWaker(t, ns, game)
}

// A unique operation marker distinguishes successive syncs of the same pinned
// Git revision with different Helm inputs. OutOfSync is expected for retained PVCs.
func syncArgo(t *testing.T, name, revision, step string, prune bool, values map[string]any) {
	t.Helper()
	apps := envInstance.Dyn.Resource(argoAppGVR).Namespace("argocd")
	marker := []any{map[string]any{"name": "fixture-step", "value": step}}
	update := map[string]any{"operation": map[string]any{
		"info": marker,
		"sync": map[string]any{"revision": revision, "prune": prune, "syncOptions": argoSyncOptions},
	}}
	if values != nil {
		update["spec"] = map[string]any{"source": map[string]any{"helm": map[string]any{"valuesObject": values}}}
	}
	raw, err := json.Marshal(update)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := apps.Patch(t.Context(), name, types.MergePatchType, raw, metav1.PatchOptions{}); err != nil {
		t.Fatal(err)
	}
	envInstance.Eventually(t, 5*time.Minute, func() (bool, string) {
		app, err := apps.Get(t.Context(), name, metav1.GetOptions{})
		if err != nil {
			return false, err.Error()
		}
		info, _, _ := unstructured.NestedSlice(app.Object, "status", "operationState", "operation", "info")
		if !reflect.DeepEqual(info, marker) {
			return false, "waiting for " + step
		}
		phase, _, _ := unstructured.NestedString(app.Object, "status", "operationState", "phase")
		msg, _, _ := unstructured.NestedString(app.Object, "status", "operationState", "message")
		if phase == "Failed" || phase == "Error" {
			t.Fatalf("%s/%s: %s: %s", name, step, phase, msg)
		}
		applied, _, _ := unstructured.NestedString(app.Object, "status", "operationState", "syncResult", "revision")
		return phase == "Succeeded" && applied == revision, phase + ": " + msg
	})
}

func checkArgoWaker(t *testing.T, ns, game string) {
	t.Helper()
	sa := "system:serviceaccount:" + ns + ":" + game + "-waker-sa"
	patch := func(data string) (string, error) {
		return envInstance.Kubectl(t.Context(), "--as", sa, "-n", ns, "patch", "gameserver", game, "--dry-run=server", "--type=merge", "-p", data)
	}
	if out, err := patch(`{"metadata":{"annotations":{"gameplane.local/idle-wake-requested":"argo-check"}}}`); err != nil {
		t.Fatalf("waker annotation denied: %v %s", err, out)
	}
	out, err := patch(`{"spec":{"suspend":true}}`)
	if err == nil || !strings.Contains(out, "gameplane-waker-wake-only") || !strings.Contains(out, "may not change the GameServer spec") {
		t.Fatalf("expected wake-only admission to deny the spec patch: %v %s", err, out)
	}
}

func renewArgoClient(t *testing.T, ca *corev1.Secret) map[string][]byte {
	t.Helper()
	pair, err := tls.X509KeyPair(ca.Data["ca.crt"], ca.Data["ca.key"])
	if err != nil {
		t.Fatal(err)
	}
	issuer, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil {
		t.Fatal(err)
	}
	key, ok := pair.PrivateKey.(crypto.Signer)
	if !ok {
		t.Fatal("issuer key cannot sign")
	}
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		t.Fatal(err)
	}
	cert := &x509.Certificate{SerialNumber: serial, Subject: pkix.Name{CommonName: "gameplane-api-client"}, NotBefore: time.Now().Add(-time.Minute),
		NotAfter: time.Now().Add(6 * time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}}
	der, err := x509.CreateCertificate(rand.Reader, cert, issuer, public, key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(private)
	if err != nil {
		t.Fatal(err)
	}
	return map[string][]byte{"tls.crt": pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), "tls.key": pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})}
}
