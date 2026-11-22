//go:build e2e

package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	authenticationv1 "k8s.io/api/authentication/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
)

// The standalone bucket owns its cluster, so the panel uses fixed names that
// the CI failure dump can list. The combined release in gameplane-system is
// the "remote" cluster's operator.
const (
	standaloneNamespace = "e2e-standalone-panel"
	standaloneRelease   = "e2e-standalone"
	standaloneCluster   = "kind-remote"
	standaloneGames     = "gameplane-games"
)

// standalonePanel is one panel-only Helm release and its admin password.
type standalonePanel struct {
	t        *testing.T
	password string
}

// TestStandalone_HelmPanelLifecycle installs the chart's panel-only profile
// (api.standalone=true, operator.enabled=false) next to the combined release
// and drives it through spec 025's user stories (OD-6): an empty panel with no
// local cluster, a refused and an accepted remote registration, a GameServer
// created through the panel and reconciled by the remote operator, kubeconfig
// replacement with the old credential revoked, an API restart, and removal.
// The panel has its own database, key and login limiter, so it spends none of
// the shared panel's login budget (3 logins against its own limiter).
func TestStandalone_HelmPanelLifecycle(t *testing.T) {
	t.Parallel()
	e := envInstance
	ctx := t.Context()
	suffix := strconv.FormatInt(time.Now().UnixNano(), 36)
	p := &standalonePanel{t: t, password: recoveryRandom(t)}

	// Step 1: install the panel-only profile and check its pod settings.
	allowed := standaloneAllowedCIDR(t)
	p.install(allowed)
	p.checkDeployment()

	// Step 2: an empty standalone panel with no local cluster.
	p.bootstrapAdmin()
	admin := p.login()
	defer admin.Close()
	var installation struct {
		Standalone   bool `json:"standalone"`
		LocalCluster bool `json:"localCluster"`
	}
	p.decode(p.expect(admin, http.MethodGet, "/admin/installation", nil, http.StatusOK), &installation)
	if !installation.Standalone || installation.LocalCluster {
		t.Fatalf("installation = %+v, want standalone without a local cluster", installation)
	}
	anonymous := &APIClient{BaseURL: admin.BaseURL, HTTP: &http.Client{Jar: newInsecureCookieJar(), Timeout: 20 * time.Second}}
	p.expect(anonymous, http.MethodGet, "/admin/installation", nil, http.StatusUnauthorized)
	p.requireNoClusters(admin)
	var fleet struct {
		Items []json.RawMessage `json:"items"`
	}
	p.decode(p.expect(admin, http.MethodGet, "/fleet/servers", nil, http.StatusOK), &fleet)
	if len(fleet.Items) != 0 {
		t.Fatalf("fleet has %d servers, want none on a fresh standalone panel", len(fleet.Items))
	}
	p.expect(admin, http.MethodGet, "/admin/system-logs/api", nil, http.StatusNotImplemented)

	// Step 3: refuse disallowed kubeconfigs, then register this Kind cluster.
	first := standaloneRemoteAccount(t, "standalone-remote-a-"+suffix)
	for name, kubeconfig := range map[string]string{
		"plain HTTP":           standaloneKubeconfig(t, "http://kubernetes.default.svc", first, false),
		"outside allowed CIDR": standaloneKubeconfig(t, "https://203.0.113.10", first, false),
		"unverified TLS":       standaloneKubeconfig(t, "https://kubernetes.default.svc", first, true),
	} {
		resp, _, err := admin.DoContext(ctx, http.MethodPost, "/clusters", map[string]any{"name": standaloneCluster, "displayName": "Refused", "kubeconfig": kubeconfig})
		if err != nil {
			t.Fatalf("register %s kubeconfig: request failed (details withheld)", name)
		}
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("register %s kubeconfig: status=%d, want 400 (body withheld)", name, resp.StatusCode)
		}
	}
	p.requireNoClusters(admin)
	p.expect(admin, http.MethodPost, "/clusters", map[string]any{
		"name": standaloneCluster, "displayName": "Kind remote",
		"kubeconfig": standaloneKubeconfig(t, "https://kubernetes.default.svc", first, false),
	}, http.StatusCreated)
	p.waitHealthy(admin)
	adminID := extractIntField(string(p.expect(admin, http.MethodGet, "/users/me", nil, http.StatusOK)), "id")
	p.expect(admin, http.MethodPost, "/users/"+adminID+"/bindings", map[string]any{"roleName": "admin", "cluster": standaloneCluster, "namespace": standaloneGames}, http.StatusCreated)
	// A binding change revokes the user's sessions: log in again.
	admin.Close()
	admin = p.login()
	defer admin.Close()

	// Step 4: create a GameServer through the panel; the remote operator
	// reconciles it.
	game := "e2e-standalone-" + suffix
	applyBusyboxTemplate(t, game)
	t.Cleanup(func() {
		_ = e.Dyn.Resource(gameServerGVR).Namespace(standaloneGames).Delete(context.Background(), game, metav1.DeleteOptions{})
	})
	query := "?cluster=" + standaloneCluster + "&namespace=" + standaloneGames
	p.expect(admin, http.MethodPost, "/servers"+query, map[string]any{
		"apiVersion": "gameplane.local/v1alpha1",
		"kind":       "GameServer",
		"metadata":   map[string]any{"name": game, "namespace": standaloneGames},
		"spec":       map[string]any{"templateRef": map[string]any{"name": game}},
	}, http.StatusCreated)
	e.Eventually(t, 3*time.Minute, func() (bool, string) {
		_, err := e.K8s.AppsV1().StatefulSets(standaloneGames).Get(ctx, game, metav1.GetOptions{})
		return err == nil, fmt.Sprintf("waiting for the remote operator to create StatefulSet %s: %v", game, err)
	})
	gs, err := e.Dyn.Resource(gameServerGVR).Namespace(standaloneGames).Get(ctx, game, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get GameServer %s: %v", game, err)
	}
	uid := string(gs.GetUID())
	p.waitServer(admin, "/servers/"+game+query, uid)

	// Step 5: replace the kubeconfig, revoke the first credential, and keep
	// reaching the remote through the replacement.
	second := standaloneRemoteAccount(t, "standalone-remote-b-"+suffix)
	p.expect(admin, http.MethodPut, "/clusters/"+standaloneCluster+"/kubeconfig", map[string]any{
		"kubeconfig": standaloneKubeconfig(t, "https://kubernetes.default.svc", second, false),
	}, http.StatusNoContent)
	if err := e.K8s.CoreV1().ServiceAccounts(standaloneNamespace).Delete(ctx, first.account, metav1.DeleteOptions{}); err != nil {
		t.Fatalf("revoke first remote credential: %v", err)
	}
	standaloneWaitRevoked(t, first)
	p.waitHealthy(admin)
	p.waitServer(admin, "/servers/"+game+query, uid)

	// Step 6: restart the API; the database and key survive, so the
	// registration still decrypts and dispatches.
	p.restart()
	admin.Close()
	admin = p.login()
	defer admin.Close()
	p.decode(p.expect(admin, http.MethodGet, "/admin/installation", nil, http.StatusOK), &installation)
	if !installation.Standalone || installation.LocalCluster {
		t.Fatalf("installation after restart = %+v, want standalone without a local cluster", installation)
	}
	p.waitHealthy(admin)
	p.waitServer(admin, "/servers/"+game+query, uid)

	// Step 7: delete the server through the panel, then remove the cluster.
	p.expect(admin, http.MethodDelete, "/servers/"+game+query, nil, http.StatusNoContent)
	e.Eventually(t, 3*time.Minute, func() (bool, string) {
		_, err := e.Dyn.Resource(gameServerGVR).Namespace(standaloneGames).Get(ctx, game, metav1.GetOptions{})
		return apierrors.IsNotFound(err), fmt.Sprintf("waiting for GameServer %s to be deleted: %v", game, err)
	})
	p.expect(admin, http.MethodDelete, "/clusters/"+standaloneCluster, nil, http.StatusNoContent)
	p.requireNoClusters(admin)
}

// standaloneAllowedCIDR returns the in-cluster Kubernetes API address as the
// only destination the panel may reach.
func standaloneAllowedCIDR(t *testing.T) string {
	t.Helper()
	svc, err := envInstance.K8s.CoreV1().Services("default").Get(t.Context(), "kubernetes", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get kubernetes Service: %v", err)
	}
	if strings.Contains(svc.Spec.ClusterIP, ":") {
		return svc.Spec.ClusterIP + "/128"
	}
	return svc.Spec.ClusterIP + "/32"
}

func (p *standalonePanel) install(allowed string) {
	t := p.t
	t.Helper()
	tag := envInstance.Tag
	if tag == "" {
		tag = "e2e"
	}
	chart := filepath.Join("..", "..", "charts", "gameplane")
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		_ = exec.CommandContext(ctx, "helm", "uninstall", standaloneRelease, "--namespace", standaloneNamespace,
			"--kube-context", envInstance.Context, "--wait", "--timeout", "2m").Run()
		_ = envInstance.K8s.CoreV1().Namespaces().Delete(ctx, standaloneNamespace, metav1.DeleteOptions{})
	})
	cmd := exec.CommandContext(t.Context(), "helm", "install", standaloneRelease, chart,
		"--namespace", standaloneNamespace, "--kube-context", envInstance.Context, "--create-namespace",
		"--set", "image.registry=gameplane-test,image.tag="+tag+",operator.enabled=false,api.standalone=true,web.enabled=false,ingress.enabled=false,crds.autoApply.enabled=false",
		"--set", "api.remoteAllowedCIDRs[0]="+allowed,
		"--wait", "--timeout", "5m")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("install standalone panel: %v: %s", err, out)
	}
}

// checkDeployment asserts the settled single-process shape (OD-1) and that the
// panel holds no Kubernetes credentials of its own.
func (p *standalonePanel) checkDeployment() {
	t := p.t
	t.Helper()
	d, err := envInstance.K8s.AppsV1().Deployments(standaloneNamespace).Get(t.Context(), "gameplane-api", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get standalone API Deployment: %v", err)
	}
	if d.Spec.Replicas == nil || *d.Spec.Replicas != 1 {
		t.Fatalf("standalone API replicas = %v, want 1", d.Spec.Replicas)
	}
	if d.Spec.Strategy.Type != "Recreate" {
		t.Fatalf("standalone API strategy = %q, want Recreate", d.Spec.Strategy.Type)
	}
	if a := d.Spec.Template.Spec.AutomountServiceAccountToken; a == nil || *a {
		t.Fatal("standalone API pod mounts a service-account token")
	}
}

func (p *standalonePanel) bootstrapAdmin() {
	t := p.t
	t.Helper()
	out, err := envInstance.KubectlWithStdin(t.Context(), p.password+"\n",
		"exec", "-i", "-n", standaloneNamespace, "deploy/gameplane-api", "-c", "api", "--",
		"/api", "bootstrap-admin", "--username=admin", "--password-stdin", "--force")
	if err != nil {
		t.Fatalf("bootstrap standalone admin: %v: %s", err, out)
	}
}

// login opens a fresh port-forward (a restart breaks the old one) and signs in.
func (p *standalonePanel) login() *APIClient {
	t := p.t
	t.Helper()
	port, stop := envInstance.PortForward(t, standaloneNamespace, "svc/gameplane-api", 80)
	t.Cleanup(stop)
	c := &APIClient{BaseURL: fmt.Sprintf("http://127.0.0.1:%d", port), HTTP: &http.Client{Jar: newInsecureCookieJar(), Timeout: 20 * time.Second}, stop: stop}
	var login struct {
		CSRF string `json:"csrf"`
	}
	p.decode(p.expect(c, http.MethodPost, "/auth/login", map[string]string{"username": "admin", "password": p.password}, http.StatusOK), &login)
	if login.CSRF == "" {
		t.Fatal("standalone login response lacked a CSRF token")
	}
	c.CSRF = login.CSRF
	return c
}

// expect sends one request and fails unless it returns want. Request bodies
// can carry kubeconfigs, so only the method, path and status are reported.
func (p *standalonePanel) expect(c *APIClient, method, path string, body any, want int) []byte {
	t := p.t
	t.Helper()
	resp, data, err := c.DoContext(t.Context(), method, path, body)
	if err != nil {
		t.Fatalf("standalone %s %s: request failed: %v", method, path, err)
	}
	if resp.StatusCode != want {
		t.Fatalf("standalone %s %s: status=%d want=%d (body withheld)", method, path, resp.StatusCode, want)
	}
	return data
}

func (p *standalonePanel) decode(data []byte, out any) {
	p.t.Helper()
	if err := json.Unmarshal(data, out); err != nil {
		p.t.Fatalf("decode standalone response: %v", err)
	}
}

type standaloneClusterList struct {
	Items []struct {
		Name  string `json:"name"`
		Phase string `json:"phase"`
	} `json:"items"`
}

// requireNoClusters asserts the list is empty: no synthetic local cluster and
// nothing left by refused or removed registrations.
func (p *standalonePanel) requireNoClusters(c *APIClient) {
	p.t.Helper()
	var list standaloneClusterList
	p.decode(p.expect(c, http.MethodGet, "/clusters", nil, http.StatusOK), &list)
	if len(list.Items) != 0 {
		names := make([]string, 0, len(list.Items))
		for _, item := range list.Items {
			names = append(names, item.Name)
		}
		p.t.Fatalf("standalone clusters = %v, want none", names)
	}
}

func (p *standalonePanel) waitHealthy(c *APIClient) {
	t := p.t
	t.Helper()
	envInstance.Eventually(t, 2*time.Minute, func() (bool, string) {
		resp, data, err := c.DoContext(t.Context(), http.MethodGet, "/clusters", nil)
		if err != nil {
			return false, fmt.Sprintf("list clusters: %v", err)
		}
		if resp.StatusCode != http.StatusOK {
			return false, fmt.Sprintf("list clusters: status=%d", resp.StatusCode)
		}
		var list standaloneClusterList
		if err := json.Unmarshal(data, &list); err != nil {
			return false, fmt.Sprintf("decode clusters: %v", err)
		}
		for _, item := range list.Items {
			if item.Name == standaloneCluster {
				return item.Phase == "Healthy", "cluster phase " + item.Phase
			}
		}
		return false, "cluster not listed"
	})
}

// waitServer waits until the panel reads the GameServer with the given UID
// from the remote cluster.
func (p *standalonePanel) waitServer(c *APIClient, path, uid string) {
	t := p.t
	t.Helper()
	envInstance.Eventually(t, 2*time.Minute, func() (bool, string) {
		resp, data, err := c.DoContext(t.Context(), http.MethodGet, path, nil)
		if err != nil {
			return false, fmt.Sprintf("read server: %v", err)
		}
		if resp.StatusCode != http.StatusOK {
			return false, fmt.Sprintf("read server: status=%d", resp.StatusCode)
		}
		var server struct {
			Metadata struct {
				UID string `json:"uid"`
			} `json:"metadata"`
		}
		if err := json.Unmarshal(data, &server); err != nil {
			return false, fmt.Sprintf("decode server: %v", err)
		}
		return server.Metadata.UID == uid, "server UID " + server.Metadata.UID + ", want " + uid
	})
}

// restart deletes the API pod and waits for the Recreate rollout's
// replacement to be ready.
func (p *standalonePanel) restart() {
	t := p.t
	t.Helper()
	ctx := t.Context()
	selector := metav1.ListOptions{LabelSelector: "app.kubernetes.io/name=gameplane-api"}
	pods, err := envInstance.K8s.CoreV1().Pods(standaloneNamespace).List(ctx, selector)
	if err != nil || len(pods.Items) != 1 {
		t.Fatalf("list standalone API pods: %v (found %d, want 1)", err, len(pods.Items))
	}
	old := pods.Items[0].Name
	if err := envInstance.K8s.CoreV1().Pods(standaloneNamespace).Delete(ctx, old, metav1.DeleteOptions{}); err != nil {
		t.Fatalf("delete standalone API pod: %v", err)
	}
	envInstance.Eventually(t, 3*time.Minute, func() (bool, string) {
		pods, err := envInstance.K8s.CoreV1().Pods(standaloneNamespace).List(ctx, selector)
		if err != nil {
			return false, err.Error()
		}
		if len(pods.Items) != 1 || pods.Items[0].Name == old || pods.Items[0].DeletionTimestamp != nil {
			return false, fmt.Sprintf("waiting for one replacement API pod (have %d)", len(pods.Items))
		}
		for _, cond := range pods.Items[0].Status.Conditions {
			if cond.Type == corev1.PodReady && cond.Status == corev1.ConditionTrue {
				return true, ""
			}
		}
		return false, "replacement API pod not ready"
	})
}

// standaloneAccount is a ServiceAccount in the panel namespace, bound to
// cluster-admin, and a bound token for it. Deleting the account revokes the
// token.
type standaloneAccount struct {
	account, token string
}

func standaloneRemoteAccount(t *testing.T, name string) standaloneAccount {
	t.Helper()
	ctx := t.Context()
	k := envInstance.K8s
	if _, err := k.CoreV1().ServiceAccounts(standaloneNamespace).Create(ctx, &corev1.ServiceAccount{ObjectMeta: metav1.ObjectMeta{Name: name}}, metav1.CreateOptions{}); err != nil {
		t.Fatalf("create remote ServiceAccount %s: %v", name, err)
	}
	binding := &rbacv1.ClusterRoleBinding{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		RoleRef:    rbacv1.RoleRef{APIGroup: rbacv1.GroupName, Kind: "ClusterRole", Name: "cluster-admin"},
		Subjects:   []rbacv1.Subject{{Kind: rbacv1.ServiceAccountKind, Name: name, Namespace: standaloneNamespace}},
	}
	if _, err := k.RbacV1().ClusterRoleBindings().Create(ctx, binding, metav1.CreateOptions{}); err != nil {
		t.Fatalf("bind remote ServiceAccount %s: %v", name, err)
	}
	t.Cleanup(func() {
		_ = k.RbacV1().ClusterRoleBindings().Delete(context.Background(), name, metav1.DeleteOptions{})
	})
	expiry := int64(3600)
	token, err := k.CoreV1().ServiceAccounts(standaloneNamespace).CreateToken(ctx, name,
		&authenticationv1.TokenRequest{Spec: authenticationv1.TokenRequestSpec{ExpirationSeconds: &expiry}}, metav1.CreateOptions{})
	if err != nil {
		t.Fatalf("issue token for %s: %v", name, err)
	}
	return standaloneAccount{account: name, token: token.Status.Token}
}

// standaloneKubeconfig builds a token kubeconfig for server, trusting the Kind
// cluster CA unless insecure is set.
func standaloneKubeconfig(t *testing.T, server string, acct standaloneAccount, insecure bool) string {
	t.Helper()
	cfg := rest.CopyConfig(envInstance.Cfg)
	if err := rest.LoadTLSFiles(cfg); err != nil {
		t.Fatalf("load Kind CA: %v", err)
	}
	cluster := &clientcmdapi.Cluster{Server: server, InsecureSkipTLSVerify: insecure}
	if !insecure {
		if len(cfg.CAData) == 0 {
			t.Fatal("standalone e2e requires the Kind cluster CA")
		}
		cluster.CertificateAuthorityData = cfg.CAData
	}
	data, err := clientcmd.Write(clientcmdapi.Config{
		Clusters:       map[string]*clientcmdapi.Cluster{"kind": cluster},
		AuthInfos:      map[string]*clientcmdapi.AuthInfo{"kind": {Token: acct.token}},
		Contexts:       map[string]*clientcmdapi.Context{"kind": {Cluster: "kind", AuthInfo: "kind"}},
		CurrentContext: "kind",
	})
	if err != nil {
		t.Fatal("could not encode standalone kubeconfig")
	}
	return string(data)
}

// standaloneWaitRevoked waits until the Kubernetes API rejects acct's token,
// so a later successful read must use the replacement credential.
func standaloneWaitRevoked(t *testing.T, acct standaloneAccount) {
	t.Helper()
	cfg := rest.AnonymousClientConfig(envInstance.Cfg)
	if err := rest.LoadTLSFiles(cfg); err != nil {
		t.Fatalf("load Kind CA: %v", err)
	}
	cfg.BearerToken = acct.token
	client, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		t.Fatalf("build revoked-token client: %v", err)
	}
	envInstance.Eventually(t, time.Minute, func() (bool, string) {
		_, err := client.CoreV1().Namespaces().Get(t.Context(), "default", metav1.GetOptions{})
		return apierrors.IsUnauthorized(err), fmt.Sprintf("waiting for the first token to be rejected: %v", err)
	})
}
