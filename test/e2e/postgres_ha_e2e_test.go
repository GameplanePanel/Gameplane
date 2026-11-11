//go:build e2e

package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

const haNamespace = "gameplane-system"

// This test takes down an actual worker and the fixture database. Never run it
// on a developer's current cluster or share its fault domain with other tests.
func TestPanel_PostgresActiveActiveFailover(t *testing.T) {
	if envInstance.Context != "kind-gameplane-postgres-ha" || envInstance.ClusterName != "gameplane-postgres-ha" {
		t.Skip("requires the dedicated postgres-ha kind cluster")
	}
	ctx := t.Context()
	envInstance.BootstrapAdmin(t, adminUsername, adminPassword)
	admin := envInstance.APIClient(t, adminUsername, adminPassword)
	defer admin.Close()
	chart, err := findRepoRel("charts/gameplane")
	if err != nil {
		t.Fatal(err)
	}
	upgrade := func(args ...string) error {
		cmd := append([]string{"upgrade", "gameplane", chart, "--kube-context", envInstance.Context, "--namespace", haNamespace,
			"--reuse-values", "--wait", "--timeout", "4m", "--set", "api.postgresHA.enabled=true", "--set", "api.replicas=2"}, args...)
		if out, err := exec.CommandContext(ctx, "helm", cmd...).CombinedOutput(); err != nil {
			return fmt.Errorf("upgrade HA release: %w\n%s", err, out)
		}
		return nil
	}
	// The singleton is running the freshly built image, and has passed that
	// image's DB-backed capability probe before we enable concurrent writers.
	assertAPIStatus(t, admin, "/readyz/postgres-ha", http.StatusOK)
	if err := upgrade(); err != nil {
		t.Fatal(err)
	}
	haLegacyBinaryFence(t)
	pair := haPodPair(t)
	clients := haDirectClients(t, pair, admin)
	_, _, id := envInstance.CreateUser(t, clients[0], "viewer", "ha-writer")
	haConcurrentWrites(t, clients, id)
	haAudit(t, clients[0])
	haAudit(t, clients[1])
	haSessionAndLimiter(t, clients)

	dbPod, err := envInstance.K8s.CoreV1().Pods(haNamespace).Get(ctx, "gameplane-postgres-0", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	dbNode := dbPod.Spec.NodeName
	stable := haServiceClient(t, dbNode, admin)
	envInstance.Eventually(t, 30*time.Second, func() (bool, string) {
		return haStatus(ctx, stable, "/users/me", http.StatusOK)
	})
	// Unlike a kubectl Service port-forward, a NodePort exercises the real
	// kube-proxy endpoint set across Pod replacements. Keep using the cookie
	// issued by the singleton and make real SQL-backed requests.
	finish := haObserveService(ctx, stable)
	if err := upgrade("--set-string", "api.logLevel=debug"); err != nil {
		_, _ = finish()
		t.Fatal(err)
	}
	samples, err := finish()
	if err != nil || samples < 2 {
		t.Fatalf("rolling upgrade: %d successful service requests: %v", samples, err)
	}
	next := haPodPair(t)
	for _, old := range pair {
		if old.UID == next[0].UID || old.UID == next[1].UID {
			t.Fatal("rolling upgrade did not replace both API Pods")
		}
	}
	t.Logf("rolling upgrade served %d authenticated requests, replacing %s/%s with %s/%s", samples, pair[0].Name, pair[1].Name, next[0].Name, next[1].Name)
	clients = haDirectClients(t, next, admin)
	haEvictWorkerLeader(t, next, clients, id)
	pair = haPodPair(t)
	clients = haDirectClients(t, pair, admin)
	victim := 0
	if pair[0].Spec.NodeName == dbNode {
		victim = 1
	} else {
		victim = 0
	}
	node := pair[victim].Spec.NodeName
	if node != "gameplane-postgres-ha-worker" && node != "gameplane-postgres-ha-worker2" {
		t.Fatalf("unexpected fault node %q", node)
	}
	if node == dbNode {
		t.Fatal("the node loss drill must leave the database reachable")
	}
	restart := func(ctx context.Context) error {
		out, err := exec.CommandContext(ctx, "docker", "unpause", node).CombinedOutput()
		if err != nil {
			return fmt.Errorf("resume fixture worker: %w %s", err, out)
		}
		return nil
	}
	if out, err := exec.CommandContext(ctx, "docker", "pause", node).CombinedOutput(); err != nil {
		t.Fatalf("pause fixture worker: %v %s", err, out)
	}
	paused := true
	defer func() {
		if paused {
			cleanup, stop := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
			defer stop()
			if err := restart(cleanup); err != nil {
				t.Error(err)
			}
		}
	}()
	haPatch(t, clients[1-victim], id, "survived worker loss")
	envInstance.Eventually(t, 105*time.Second, func() (bool, string) {
		current, err := envInstance.K8s.CoreV1().Nodes().Get(ctx, node, metav1.GetOptions{})
		if err != nil {
			return false, err.Error()
		}
		for _, condition := range current.Status.Conditions {
			if condition.Type == corev1.NodeReady && condition.Status != corev1.ConditionTrue {
				return haStatus(ctx, stable, "/users/me", http.StatusOK)
			}
		}
		return false, "awaiting detected node loss and authenticated service failover"
	})
	if err := restart(ctx); err != nil {
		t.Fatal(err)
	}
	paused = false
	t.Logf("worker %s was unavailable while %s served authenticated writes", node, pair[1-victim].Name)
	pair = haPodPair(t)
	clients = haDirectClients(t, pair, admin)
	game, _ := recoveryGame(t, "ha")
	identity, before := recoveryGameIdentity(t, game), recoveryCounter(t, game)
	haDatabaseOutage(t, clients, id)
	if current := recoveryGameIdentity(t, game); current != identity || recoveryCounter(t, game) <= before {
		t.Fatalf("game or persistent world stopped progressing during the database outage: before=%v after=%v", identity, current)
	}
	haAudit(t, clients[0])
	haAudit(t, clients[1])
	if err := upgrade("--set", "api.postgresHA.enabled=false", "--set", "api.replicas=1"); err != nil {
		t.Fatal(err)
	}
	// Scale-down status alone may omit terminating Pods. Wait for complete
	// termination before an operator could switch to an old release.
	envInstance.Eventually(t, 2*time.Minute, func() (bool, string) {
		live, err := envInstance.K8s.CoreV1().Pods(haNamespace).List(ctx, metav1.ListOptions{LabelSelector: "app.kubernetes.io/name=gameplane-api"})
		if err != nil || len(live.Items) != 1 {
			return false, fmt.Sprintf("waiting for singleton termination: %v", err)
		}
		args := strings.Join(live.Items[0].Spec.Containers[0].Args, " ")
		if strings.Contains(args, "--postgres-ha-protocol") {
			return false, "waiting for singleton process argument set"
		}
		return haStatus(ctx, stable, "/users/me", http.StatusOK)
	})
}

func haLegacyBinaryFence(t *testing.T) {
	t.Helper()
	const name = "ha-legacy-binary"
	// Official 0.4.0 OCI index. The invalid DSN keeps the probe isolated from
	// production data and distinguishes flag parsing from any DB-open attempt.
	const legacy = "ghcr.io/gameplanepanel/gameplane/api@sha256:774229ab7f2d9ecdd0d43a742cd833124c299780b4623308208d87d4b3586f59"
	no := false
	pods := envInstance.K8s.CoreV1().Pods(haNamespace)
	_, err := pods.Create(t.Context(), &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec: corev1.PodSpec{RestartPolicy: corev1.RestartPolicyNever, AutomountServiceAccountToken: &no,
			Containers: []corev1.Container{{Name: "api", Image: legacy, Args: []string{"--postgres-ha-protocol=1", "--db-driver=postgres", "--db-dsn=invalid"}}}},
	}, metav1.CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	envInstance.Eventually(t, 2*time.Minute, func() (bool, string) {
		pod, err := pods.Get(t.Context(), name, metav1.GetOptions{})
		if err != nil || len(pod.Status.ContainerStatuses) != 1 {
			return false, fmt.Sprintf("waiting for legacy API: %v", err)
		}
		ended := pod.Status.ContainerStatuses[0].State.Terminated
		return ended != nil && ended.ExitCode != 0, fmt.Sprintf("waiting for historical binary to reject HA argument: %v", pod.Status.Phase)
	})
	logs, err := pods.GetLogs(name, &corev1.PodLogOptions{}).DoRaw(t.Context())
	if err != nil || !strings.Contains(string(logs), "flag provided but not defined: -postgres-ha-protocol") {
		t.Fatalf("legacy binary did not reject the protocol at flag parsing: %v %s", err, logs)
	}
}

func haEvictWorkerLeader(t *testing.T, pods []corev1.Pod, clients []*APIClient, id string) {
	t.Helper()
	ctx := t.Context()
	// Match the two-int advisory lock used by the database-backed worker
	// election. Inspect the live primary, independently of API-side logging.
	const holder = `SELECT host(a.client_addr) FROM pg_locks l JOIN pg_stat_activity a USING(pid)
WHERE l.locktype='advisory' AND l.classid=1196447563 AND l.objid=hashtext(current_schema())::oid AND l.objsubid=2 AND l.granted`
	leader := -1
	readLeader := func() (string, error) {
		out, err := envInstance.Kubectl(ctx, "exec", "-n", haNamespace, "gameplane-postgres-0", "--", "psql", "-XAt", "-v", "ON_ERROR_STOP=1", "-U", "gameplane", "-d", "gameplane", "-c", holder)
		return strings.TrimSpace(out), err
	}
	envInstance.Eventually(t, time.Minute, func() (bool, string) {
		ip, err := readLeader()
		for i, pod := range pods {
			if err == nil && ip == pod.Status.PodIP {
				leader = i
				return true, ""
			}
		}
		return false, fmt.Sprintf("expected one worker owner, got %q: %v", ip, err)
	})
	node := pods[leader].Spec.NodeName
	if out, err := envInstance.Kubectl(ctx, "cordon", node); err != nil {
		t.Fatalf("cordon worker owner before eviction: %v %s", err, out)
	}
	defer func() {
		cleanup, stop := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer stop()
		if out, err := envInstance.Kubectl(cleanup, "uncordon", node); err != nil {
			t.Errorf("restore scheduling to worker node: %v %s", err, out)
		}
	}()
	// Required hostname anti-affinity and the cordon leave only the existing
	// standby able to take over. Exercise the PDB via a normal eviction.
	err := envInstance.K8s.CoreV1().Pods(haNamespace).EvictV1(ctx, &policyv1.Eviction{
		ObjectMeta: metav1.ObjectMeta{Name: pods[leader].Name, Namespace: haNamespace},
	})
	if err != nil {
		t.Fatal(err)
	}
	standby := 1 - leader
	haPatch(t, clients[standby], id, "survived worker leader eviction")
	envInstance.Eventually(t, time.Minute, func() (bool, string) {
		ip, err := readLeader()
		return err == nil && ip == pods[standby].Status.PodIP, fmt.Sprintf("worker failover owner %q: %v", ip, err)
	})
	t.Logf("worker lock transferred from %s to %s", pods[leader].Name, pods[standby].Name)
}

func haPodPair(t *testing.T) []corev1.Pod {
	t.Helper()
	var ready []corev1.Pod
	envInstance.Eventually(t, 3*time.Minute, func() (bool, string) {
		pods, err := envInstance.K8s.CoreV1().Pods(haNamespace).List(t.Context(), metav1.ListOptions{LabelSelector: "app.kubernetes.io/name=gameplane-api"})
		if err != nil {
			return false, err.Error()
		}
		ready = nil
		for _, p := range pods.Items {
			for _, c := range p.Status.Conditions {
				if p.DeletionTimestamp == nil && c.Type == corev1.PodReady && c.Status == corev1.ConditionTrue {
					ready = append(ready, p)
				}
			}
		}
		return len(ready) == 2 && ready[0].Spec.NodeName != ready[1].Spec.NodeName, fmt.Sprintf("ready API replicas: %d", len(ready))
	})
	return ready
}

func haDirectClients(t *testing.T, pods []corev1.Pod, admin *APIClient) []*APIClient {
	t.Helper()
	var clients []*APIClient
	for _, pod := range pods {
		port, stop := envInstance.PortForward(t, haNamespace, "pod/"+pod.Name, 8000)
		t.Cleanup(stop)
		client := &APIClient{BaseURL: fmt.Sprintf("http://127.0.0.1:%d", port), HTTP: admin.HTTP, CSRF: admin.CSRF}
		assertAPIStatus(t, client, "/users/me", http.StatusOK)
		clients = append(clients, client)
	}
	return clients
}

func haPatch(t *testing.T, client *APIClient, id, displayName string) {
	t.Helper()
	resp, body, err := client.DoContext(t.Context(), http.MethodPatch, "/users/"+id, map[string]string{"displayName": displayName})
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("write through API replica: %v %s", err, body)
	}
}

func haConcurrentWrites(t *testing.T, clients []*APIClient, id string) {
	t.Helper()
	var writers sync.WaitGroup
	for n, client := range clients {
		writers.Go(func() {
			for i := range 6 {
				resp, body, err := client.DoContext(t.Context(), http.MethodPatch, "/users/"+id, map[string]string{"displayName": fmt.Sprintf("writer-%d-%d", n, i)})
				if err != nil || resp.StatusCode != http.StatusOK {
					t.Errorf("writer %d: %v %s", n, err, body)
					return
				}
			}
		})
	}
	writers.Wait()
}

func haAudit(t *testing.T, client *APIClient) {
	t.Helper()
	body := assertAPIStatus(t, client, "/admin/audit/verify", http.StatusOK)
	var audit struct {
		OK      bool
		Checked int
	}
	if err := json.Unmarshal(body, &audit); err != nil || !audit.OK || audit.Checked < 12 {
		t.Fatalf("cross-writer audit integrity: %s", body)
	}
}

func haSessionAndLimiter(t *testing.T, clients []*APIClient) {
	t.Helper()
	// A fresh viewer session is first read (and potentially cached) on A.
	// Retain the original cookie in a second jar when B deletes its copy.
	username, password, _ := envInstance.CreateUser(t, clients[0], "viewer", "ha-session")
	first := &APIClient{BaseURL: clients[0].BaseURL, HTTP: &http.Client{Jar: newInsecureCookieJar(), Timeout: 5 * time.Second}}
	resp, body, err := first.Post("/auth/login", map[string]string{"username": username, "password": password})
	var login struct{ CSRF string }
	if err != nil || resp.StatusCode != 200 || json.Unmarshal(body, &login) != nil || login.CSRF == "" {
		t.Fatalf("viewer login on A did not return an authenticated session: %v", err)
	}
	assertAPIStatus(t, first, "/users/me", 200)
	a, _ := url.Parse(first.BaseURL)
	b, _ := url.Parse(clients[1].BaseURL)
	second := &APIClient{BaseURL: clients[1].BaseURL, CSRF: login.CSRF, HTTP: &http.Client{Jar: newInsecureCookieJar(), Timeout: 5 * time.Second}}
	second.HTTP.Jar.SetCookies(b, first.HTTP.Jar.Cookies(a))
	resp, _, err = second.Post("/auth/logout", nil)
	if err != nil || resp.StatusCode/100 != 2 {
		t.Fatalf("logout on B: %v %v", err, resp)
	}
	assertAPIStatus(t, first, "/users/me", http.StatusUnauthorized)
	for attempt := range 7 {
		anonymous := &APIClient{BaseURL: clients[attempt%2].BaseURL, HTTP: &http.Client{Timeout: 5 * time.Second}}
		resp, _, err := anonymous.Post("/auth/login", map[string]string{"username": "ha-unknown", "password": "invalid"})
		want := http.StatusUnauthorized
		if attempt == 6 {
			want = http.StatusTooManyRequests
		}
		if err != nil || resp.StatusCode != want {
			t.Fatalf("shared username login budget, attempt %d: %v %v (want %d)", attempt+1, err, resp, want)
		}
	}
}

func haServiceClient(t *testing.T, nodeName string, admin *APIClient) *APIClient {
	t.Helper()
	api := envInstance.K8s.CoreV1()
	svc, err := api.Services(haNamespace).Create(t.Context(), &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: "ha-drill"},
		Spec: corev1.ServiceSpec{Type: corev1.ServiceTypeNodePort, Selector: map[string]string{"app.kubernetes.io/name": "gameplane-api"},
			Ports: []corev1.ServicePort{{Name: "http", Port: 80, TargetPort: intstr.FromString("http")}}},
	}, metav1.CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	node, err := api.Nodes().Get(t.Context(), nodeName, metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for _, addr := range node.Status.Addresses {
		if addr.Type == corev1.NodeInternalIP {
			base := &url.URL{Scheme: "http", Host: net.JoinHostPort(addr.Address, strconv.Itoa(int(svc.Spec.Ports[0].NodePort)))}
			u, _ := url.Parse(admin.BaseURL)
			cli := &http.Client{Jar: newInsecureCookieJar(), Timeout: 3 * time.Second}
			cli.Jar.SetCookies(base, admin.HTTP.Jar.Cookies(u))
			return &APIClient{BaseURL: base.String(), CSRF: admin.CSRF, HTTP: cli}
		}
	}
	t.Fatalf("node %q has no reachable InternalIP", nodeName)
	return nil
}

func haStatus(ctx context.Context, client *APIClient, path string, want int) (bool, string) {
	resp, body, err := client.DoContext(ctx, http.MethodGet, path, nil)
	if err != nil {
		return false, err.Error()
	}
	return resp.StatusCode == want, fmt.Sprintf("%s returned %d: %s", path, resp.StatusCode, body)
}

func haObserveService(ctx context.Context, stable *APIClient) func() (int, error) {
	stop := make(chan struct{})
	done := make(chan struct{})
	var count int
	var failure error
	go func() {
		defer close(done)
		tick := time.NewTicker(250 * time.Millisecond)
		defer tick.Stop()
		for {
			if ok, why := haStatus(ctx, stable, "/users/me", http.StatusOK); !ok {
				failure = fmt.Errorf("service continuity: %s", why)
				return
			}
			count++
			select {
			case <-stop:
				return
			case <-ctx.Done():
				failure = ctx.Err()
				return
			case <-tick.C:
			}
		}
	}()
	return func() (int, error) { close(stop); <-done; return count, failure }
}

func haDatabaseOutage(t *testing.T, clients []*APIClient, id string) {
	t.Helper()
	setScale := func(ctx context.Context, n int32) error {
		sts := envInstance.K8s.AppsV1().StatefulSets(haNamespace)
		scale, err := sts.GetScale(ctx, "gameplane-postgres", metav1.GetOptions{})
		if err != nil {
			return err
		}
		scale.Spec.Replicas = n
		_, err = sts.UpdateScale(ctx, "gameplane-postgres", scale, metav1.UpdateOptions{})
		return err
	}
	if err := setScale(t.Context(), 0); err != nil {
		t.Fatal(err)
	}
	restored := false
	defer func() {
		if !restored {
			ctx, stop := context.WithTimeout(context.WithoutCancel(t.Context()), 30*time.Second)
			defer stop()
			if err := setScale(ctx, 1); err != nil {
				t.Error(err)
			}
		}
	}()
	for _, client := range clients {
		envInstance.Eventually(t, time.Minute, func() (bool, string) {
			return haStatus(t.Context(), client, "/readyz/postgres-ha", http.StatusServiceUnavailable)
		})
		assertAPIStatus(t, client, "/healthz", http.StatusOK)
		resp, _, err := client.DoContext(t.Context(), http.MethodPatch, "/users/"+id, map[string]string{"displayName": "database-unavailable"})
		if err != nil || resp.StatusCode != http.StatusServiceUnavailable {
			t.Fatalf("database outage must reject authenticated writes: %v %v", err, resp)
		}
	}
	if err := setScale(t.Context(), 1); err != nil {
		t.Fatal(err)
	}
	restored = true
	for _, client := range clients {
		envInstance.Eventually(t, 2*time.Minute, func() (bool, string) {
			return haStatus(t.Context(), client, "/readyz/postgres-ha", http.StatusOK)
		})
		body := assertAPIStatus(t, client, "/users", http.StatusOK)
		if strings.Contains(string(body), "database-unavailable") {
			t.Fatal("an unavailable write was reported failed but committed")
		}
		haPatch(t, client, id, "database-recovered")
	}
}
