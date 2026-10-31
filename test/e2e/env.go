//go:build e2e

// Package e2e holds end-to-end tests that run against a real
// (kind-based) Kubernetes cluster with the Gameplane chart installed.
package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

// Env wraps the k8s clients + a kubectl shell helper. One Env is built
// per test process via newEnv, called from the test suite TestMain
// after the cluster is known to be up.
type Env struct {
	Cfg *rest.Config
	K8s kubernetes.Interface
	Dyn dynamic.Interface

	// ClusterName / Tag are set by the e2e bootstrap and used by
	// Kubectl for context selection.
	ClusterName string
	Tag         string

	// Context is the kubeconfig context the suite acts in. Defaults to
	// "kind-<ClusterName>" for the local kind path; set GAMEPLANE_E2E_CONTEXT
	// to target an existing (e.g. remote) cluster instead.
	Context string
}

func newEnv() (*Env, error) {
	cluster := getenvOr("GAMEPLANE_E2E_CLUSTER", "gameplane-e2e")
	tag := getenvOr("GAMEPLANE_E2E_TAG", "e2e")
	kctx := getenvOr("GAMEPLANE_E2E_CONTEXT", "kind-"+cluster)

	env, err := newEnvForContext(kctx)
	if err != nil {
		return nil, err
	}
	env.ClusterName = cluster
	env.Tag = tag
	return env, nil
}

// newEnvForContext builds an Env pointed at an explicit kubeconfig context,
// bypassing the GAMEPLANE_E2E_CLUSTER/GAMEPLANE_E2E_CONTEXT env vars newEnv
// reads. Used by dual-cluster tests (see multicluster_e2e_test.go) to talk
// directly to a second kind cluster that TestMain does not itself manage.
// ClusterName and Tag on the returned Env are set to kctx; callers that care
// about those fields (none of the dual-cluster helpers currently do) should
// overwrite them.
func newEnvForContext(kctx string) (*Env, error) {
	loader := clientcmd.NewDefaultClientConfigLoadingRules()
	if kc := os.Getenv("KUBECONFIG"); kc != "" {
		loader.ExplicitPath = kc
	}
	override := &clientcmd.ConfigOverrides{
		CurrentContext: kctx,
	}
	cfg, err := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(loader, override).ClientConfig()
	if err != nil {
		return nil, fmt.Errorf("build rest config for context %s: %w", kctx, err)
	}
	k8s, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return nil, err
	}
	dyn, err := dynamic.NewForConfig(cfg)
	if err != nil {
		return nil, err
	}
	return &Env{
		Cfg:         cfg,
		K8s:         k8s,
		Dyn:         dyn,
		ClusterName: kctx,
		Tag:         getenvOr("GAMEPLANE_E2E_TAG", "e2e"),
		Context:     kctx,
	}, nil
}

// getenvOr returns os.Getenv(key) when set, otherwise dflt.
func getenvOr(key, dflt string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return dflt
}

// Eventually polls cond every ~500ms until it returns true or timeout
// elapses. The condition's message is included on timeout.
func (e *Env) Eventually(t *testing.T, timeout time.Duration, cond func() (bool, string)) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var lastMsg string
	for time.Now().Before(deadline) {
		ok, msg := cond()
		if ok {
			return
		}
		lastMsg = msg
		time.Sleep(500 * time.Millisecond)
	}
	if ok, _ := cond(); ok {
		return
	}
	t.Fatalf("Eventually: timed out after %s: %s", timeout, lastMsg)
}

// Consistently polls cond every interval for the full duration and fails
// the test on the first false — the counterpart to Eventually. Where
// Eventually asserts a condition *becomes* true within a deadline,
// Consistently asserts an invariant *holds* throughout a window: every
// poll must return true, or the test fails immediately with the
// condition's message rather than waiting out the rest of the window.
func (e *Env) Consistently(t *testing.T, duration, interval time.Duration, cond func() (bool, string)) {
	t.Helper()
	deadline := time.Now().Add(duration)
	for time.Now().Before(deadline) {
		if ok, msg := cond(); !ok {
			t.Fatalf("Consistently: invariant broke before %s elapsed: %s", duration, msg)
		}
		time.Sleep(interval)
	}
}

// Kubectl shells out for operations the typed client makes awkward —
// `kubectl exec`, `kubectl apply -f`, etc. Output (stdout+stderr
// combined) is returned along with the error, so callers can include
// it in failure messages.
func (e *Env) Kubectl(ctx context.Context, args ...string) (string, error) {
	// Validate arguments to prevent shell injection: reject anything
	// containing shell metacharacters or suspicious patterns.
	// Note: we stop validation at "--" because args after it are for the
	// container (in kubectl exec), not for kubectl itself.
	for _, arg := range args {
		if arg == "--" {
			break
		}
		if strings.ContainsAny(arg, "|;&$`<>()\\") {
			return "", fmt.Errorf("kubectl arg rejected (shell metachar): %q", arg)
		}
	}
	all := append([]string{"--context", e.Context}, args...)
	cmd := exec.CommandContext(ctx, "kubectl", all...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// ApplyYAML reads a YAML manifest from the fixtures directory and
// applies it via kubectl. Path is relative to the fixtures/ directory.
func (e *Env) ApplyYAML(t *testing.T, fixturePath string) {
	t.Helper()
	abs, err := filepath.Abs(filepath.Join("fixtures", fixturePath))
	if err != nil {
		t.Fatalf("resolve fixture path %s: %v", fixturePath, err)
	}
	if out, err := e.Kubectl(t.Context(), "apply", "-f", abs); err != nil {
		t.Fatalf("kubectl apply -f %s: %v\n%s", fixturePath, err, out)
	}
}

// PodIsReady reports whether the named pod has Ready=True.
func (e *Env) PodIsReady(ctx context.Context, ns, name string) (bool, error) {
	pod, err := e.K8s.CoreV1().Pods(ns).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return false, err
	}
	for _, c := range pod.Status.Conditions {
		if c.Type == "Ready" {
			return c.Status == "True", nil
		}
	}
	return false, nil
}

// CRDExists reports whether the named CustomResourceDefinition has
// been installed in the cluster.
func (e *Env) CRDExists(ctx context.Context, name string) (bool, error) {
	_, err := e.Dyn.Resource(crdGVR).Get(ctx, name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

// crdGVR is the GVR for the CustomResourceDefinition resource itself.
var crdGVR = schema.GroupVersionResource{
	Group:    "apiextensions.k8s.io",
	Version:  "v1",
	Resource: "customresourcedefinitions",
}

// ensureCluster verifies the e2e cluster is reachable. Used at TestMain
// time before launching tests so failures are fast and clear.
func (e *Env) ensureCluster() error {
	_, err := e.K8s.Discovery().ServerVersion()
	if err == nil {
		return nil
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("cluster %q not reachable: %w", e.ClusterName, err)
	}
	return err
}

// KubectlWithStdin is like Kubectl but pipes the given string as stdin
// to the kubectl process. Used for password-stdin and similar flows
// where putting the value in argv would leak it through /proc.
func (e *Env) KubectlWithStdin(ctx context.Context, stdin string, args ...string) (string, error) {
	// Validate arguments to prevent shell injection: reject anything
	// containing shell metacharacters or suspicious patterns.
	// Note: we stop validation at "--" because args after it are for the
	// container (in kubectl exec), not for kubectl itself.
	for _, arg := range args {
		if arg == "--" {
			break
		}
		if strings.ContainsAny(arg, "|;&$`<>()\\") {
			return "", fmt.Errorf("kubectl arg rejected (shell metachar): %q", arg)
		}
	}
	all := append([]string{"--context", e.Context}, args...)
	cmd := exec.CommandContext(ctx, "kubectl", all...)
	cmd.Stdin = strings.NewReader(stdin)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// KubectlExec is a readability wrapper for `kubectl exec` against a
// specific namespace + target. The variadic command runs inside the
// container; the typical caller is exec'ing a single binary with args.
func (e *Env) KubectlExec(t *testing.T, ns, target string, cmd ...string) (string, error) {
	t.Helper()
	args := append([]string{"exec", "-n", ns, target, "--"}, cmd...)
	return e.Kubectl(t.Context(), args...)
}

// EventuallyNoErr is the error-returning sibling of Eventually. Useful
// when the probe naturally returns (T, error) — the typical client-go
// API shape.
func (e *Env) EventuallyNoErr(t *testing.T, timeout time.Duration, fn func() error) {
	t.Helper()
	e.Eventually(t, timeout, func() (bool, string) {
		if err := fn(); err != nil {
			return false, err.Error()
		}
		return true, ""
	})
}

// bootstrapAdminOnce serializes BootstrapAdmin calls across the test
// process so a single (username, password) pair is materialized at most
// once. argon2id hashing inside the running api container is heavy;
// repeating it per test pushes the pod over its memory limit and the
// kubelet OOM-kills the container. Tests freely call BootstrapAdmin
// for idempotence; the helper short-circuits after the first.
var (
	bootstrapAdminOnce sync.Once
	bootstrapAdminKey  string
)

// BootstrapAdmin invokes the API binary's `bootstrap-admin` subcommand
// inside the running api Deployment. Pass --force so the call is
// idempotent across reruns of the e2e suite.
//
// Password is fed via --password-stdin so it never lands in argv. The
// distroless API image has no shell, but kubectl exec can still launch
// /api directly.
//
// Per-process: only one (username, password) pair is bootstrapped per
// `go test` invocation. Calls with a different pair after the first
// fail loudly so a test author notices the fixture clash rather than
// silently using stale credentials.
func (e *Env) BootstrapAdmin(t *testing.T, username, password string) {
	t.Helper()
	key := username + "\x00" + password
	bootstrapAdminOnce.Do(func() {
		bootstrapAdminKey = key
		out, err := e.KubectlWithStdin(
			t.Context(),
			password+"\n",
			"exec", "-i", "-n", "gameplane-system", "deploy/gameplane-api", "--",
			"/api", "bootstrap-admin",
			"--username="+username,
			"--password-stdin",
			"--force",
		)
		if err != nil {
			t.Fatalf("bootstrap-admin: %v\n%s", err, out)
		}
	})
	if bootstrapAdminKey != key {
		t.Fatalf("BootstrapAdmin called with a second (user, password) pair: " +
			"the helper bootstraps once per test process. Pick a single shared admin fixture.")
	}
}

// WriteAdminPasswordFile drops the admin password at
// test/e2e/.tmp/admin-password (mode 0600) so the Playwright suite can
// reuse the credentials in live mode. The directory is gitignored.
func (e *Env) WriteAdminPasswordFile(t *testing.T, pw string) {
	t.Helper()
	if err := os.MkdirAll(".tmp", 0o700); err != nil {
		t.Fatalf("mkdir .tmp: %v", err)
	}
	if err := os.WriteFile(filepath.Join(".tmp", "admin-password"), []byte(pw), 0o600); err != nil {
		t.Fatalf("write admin-password: %v", err)
	}
}

// PortForward starts `kubectl port-forward` for target in the given
// namespace, picks a free local port, and waits until the port accepts
// TCP. Returns the port and a stop func that kills the kubectl child.
//
// `target` follows kubectl conventions ("svc/foo" or "pod/foo").
func (e *Env) PortForward(t *testing.T, ns, target string, remotePort int) (int, func()) {
	t.Helper()
	// Retry the whole spawn: on a loaded self-hosted runner, kubectl
	// port-forward can take a while to wire the pod tunnel, or fail
	// transiently. A single attempt with a short deadline produces a
	// cascade of "never became ready" / "connection refused" failures once
	// the runner is under load. Respawn on a fresh local port up to a few
	// times before giving up.
	const attempts = 4
	var lastErr error
	for attempt := 1; attempt <= attempts; attempt++ {
		local, stop, err := e.tryPortForward(t.Context(), ns, target, remotePort)
		if err == nil {
			return local, stop
		}
		lastErr = err
		time.Sleep(time.Second)
	}
	t.Fatalf("port-forward never became ready (target %s/%s:%d) after %d attempts: %v",
		ns, target, remotePort, attempts, lastErr)
	return 0, nil
}

// tryPortForward starts one `kubectl port-forward` on a free local port and
// waits until the tunnel is usable. It returns the port and a stop func, or
// an error if the forward never became ready (the caller retries).
//
// ctx (the caller's t.Context()) only bounds the child process's maximum
// lifetime — it is not what tears the forward down in normal use. The
// returned stop func is the real owner of the process's lifecycle: every
// caller defers/cleanups it well before the test (and so ctx) ends. Binding
// to anything shorter-lived than the test itself would risk killing the
// tunnel out from under an in-flight caller.
func (e *Env) tryPortForward(ctx context.Context, ns, target string, remotePort int) (int, func(), error) {
	local, err := freePort(ctx)
	if err != nil {
		return 0, nil, fmt.Errorf("free port: %w", err)
	}
	for _, arg := range []string{ns, target} {
		if strings.ContainsAny(arg, "|;&$`<>()\\") {
			return 0, nil, fmt.Errorf("kubectl arg rejected (shell metachar): %q", arg)
		}
	}
	args := []string{
		"--context", e.Context,
		"port-forward",
		"-n", ns,
		target,
		fmt.Sprintf("%d:%d", local, remotePort),
	}
	cmd := exec.CommandContext(ctx, "kubectl", args...)
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	if err := cmd.Start(); err != nil {
		return 0, nil, fmt.Errorf("start port-forward: %w", err)
	}
	stop := func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	}
	// kubectl opens the local listener before the pod tunnel is fully wired,
	// so a single successful dial can still be followed by a "connection
	// refused" on the first real request. Require two spaced successes for a
	// steadier readiness signal, and allow a generous window for a busy
	// runner to establish the forward.
	deadline := time.Now().Add(45 * time.Second)
	streak := 0
	dialer := net.Dialer{Timeout: 500 * time.Millisecond}
	for time.Now().Before(deadline) {
		dialCtx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
		c, derr := dialer.DialContext(dialCtx, "tcp", fmt.Sprintf("127.0.0.1:%d", local))
		cancel()
		if derr == nil {
			_ = c.Close()
			streak++
			if streak >= 2 {
				return local, stop, nil
			}
			time.Sleep(400 * time.Millisecond)
			continue
		}
		streak = 0
		time.Sleep(200 * time.Millisecond)
	}
	stop()
	return 0, nil, fmt.Errorf("not ready within deadline on 127.0.0.1:%d", local)
}

// freePort returns an OS-allocated free TCP port on 127.0.0.1. There's a
// small race between releasing the port and kubectl binding it, but in
// practice the window is too short to matter for e2e tests.
func freePort(ctx context.Context) (int, error) {
	lc := net.ListenConfig{}
	l, err := lc.Listen(ctx, "tcp", "127.0.0.1:0")
	if err != nil {
		return 0, fmt.Errorf("listen for free port: %w", err)
	}
	defer func() { _ = l.Close() }()
	addr, ok := l.Addr().(*net.TCPAddr)
	if !ok {
		return 0, fmt.Errorf("unexpected listener addr type %T", l.Addr())
	}
	return addr.Port, nil
}

// insecureCookieJar is a minimal http.CookieJar that ignores the Secure
// attribute so the e2e client can carry the API's Secure session/CSRF cookies
// over the plain-HTTP port-forward. The standard net/http/cookiejar filters
// Secure cookies out of HTTP requests, which would drop the session and make
// every authenticated call 401. Production still sets Secure:true; this is a
// test-only accommodation for talking to a single localhost host.
type insecureCookieJar struct {
	mu      sync.Mutex
	cookies map[string]*http.Cookie
}

func newInsecureCookieJar() *insecureCookieJar {
	return &insecureCookieJar{cookies: map[string]*http.Cookie{}}
}

func (j *insecureCookieJar) SetCookies(_ *url.URL, cookies []*http.Cookie) {
	j.mu.Lock()
	defer j.mu.Unlock()
	for _, c := range cookies {
		if c.MaxAge < 0 {
			delete(j.cookies, c.Name)
			continue
		}
		j.cookies[c.Name] = c
	}
}

func (j *insecureCookieJar) Cookies(_ *url.URL) []*http.Cookie {
	j.mu.Lock()
	defer j.mu.Unlock()
	out := make([]*http.Cookie, 0, len(j.cookies))
	for _, c := range j.cookies {
		out = append(out, c)
	}
	return out
}

// APIClient is a minimal authenticated session against the API service.
// Mutations attach the X-Gameplane-CSRF header that the session
// middleware demands; reads pass through unchanged.
type APIClient struct {
	BaseURL string
	CSRF    string
	HTTP    *http.Client
	stop    func()
}

// APIClient logs in as username/password against the in-cluster API
// (via a freshly-spawned port-forward). Caller MUST defer Close() to
// tear the port-forward down.
func (e *Env) APIClient(t *testing.T, username, password string) *APIClient {
	t.Helper()
	local, stop := e.PortForward(t, "gameplane-system", "svc/gameplane-api", 80)
	base := fmt.Sprintf("http://127.0.0.1:%d", local)

	// 90s tolerates the few legitimately-slow endpoints (notably :restart,
	// which blocks until the graceful soft-stop drains the pod, up to the
	// stop grace period). It never masks a hung handler: the API's own 60s
	// request-timeout middleware returns 503 first.
	cli := &http.Client{Jar: newInsecureCookieJar(), Timeout: 90 * time.Second}

	body, err := json.Marshal(map[string]string{
		"username": username,
		"password": password,
	})
	if err != nil {
		stop()
		t.Fatalf("marshal login: %v", err)
	}

	// Retry on 429 with exponential backoff. The API's LoginLimiter
	// rate-limits per-IP (all tests share the port-forward's IP), and the
	// per-username limiter throttles the shared e2e-admin fixture. With
	// t.Parallel() several logins can queue behind the refill at once, so
	// allow up to ~90s of capped backoff before giving up.
	var resp *http.Response
	var rb []byte
	delay := 2 * time.Second
	const maxDelay = 30 * time.Second
	const maxAttempts = 7
	reconnected := false
	for attempt := 0; attempt < maxAttempts; attempt++ {
		req, rerr := http.NewRequestWithContext(t.Context(), http.MethodPost, base+"/auth/login", bytes.NewReader(body))
		if rerr != nil {
			stop()
			t.Fatalf("new login request: %v", rerr)
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err = cli.Do(req)
		if err != nil {
			stop()
			// A vanished local listener cannot have received this request.
			// Reopen once within the existing attempt budget; never replay an
			// ambiguous read/write failure or reconnect an established session.
			if !reconnected && attempt+1 < maxAttempts && t.Context().Err() == nil && loginDialRefused(err) {
				local, stop = e.PortForward(t, "gameplane-system", "svc/gameplane-api", 80)
				base = fmt.Sprintf("http://127.0.0.1:%d", local)
				reconnected = true
				continue
			}
			t.Fatalf("login: %v", err)
		}
		rb, _ = io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusTooManyRequests {
			break
		}
		time.Sleep(delay)
		delay *= 2
		if delay > maxDelay {
			delay = maxDelay
		}
	}
	if resp.StatusCode != http.StatusOK {
		stop()
		t.Fatalf("login %d: %s", resp.StatusCode, string(rb))
	}
	var lr struct {
		CSRF string `json:"csrf"`
	}
	if jerr := json.Unmarshal(rb, &lr); jerr != nil {
		stop()
		t.Fatalf("decode login response: %v\n%s", jerr, string(rb))
	}
	return &APIClient{BaseURL: base, CSRF: lr.CSRF, HTTP: cli, stop: stop}
}

func loginDialRefused(err error) bool {
	var opErr *net.OpError
	return errors.As(err, &opErr) && opErr.Op == "dial" && !opErr.Timeout() && errors.Is(opErr.Err, syscall.ECONNREFUSED)
}

// Close tears down the port-forward backing this client.
func (c *APIClient) Close() {
	if c.stop != nil {
		c.stop()
	}
}

// Do performs an authenticated request. Body, when non-nil, is
// marshalled as JSON and the Content-Type header is set. CSRF header is
// attached for mutating methods.
func (c *APIClient) Do(method, path string, body any) (*http.Response, []byte, error) {
	var br io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, nil, fmt.Errorf("marshal body: %w", err)
		}
		br = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(context.Background(), method, c.BaseURL+path, br)
	if err != nil {
		return nil, nil, fmt.Errorf("new request: %w", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if isMutation(method) {
		req.Header.Set("X-Gameplane-CSRF", c.CSRF)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, nil, fmt.Errorf("do %s %s: %w", method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	rb, _ := io.ReadAll(resp.Body)
	return resp, rb, nil
}

// Get is a shortcut for Do(http.MethodGet, path, nil).
func (c *APIClient) Get(path string) (*http.Response, []byte, error) {
	return c.Do(http.MethodGet, path, nil)
}

// Post is a shortcut for Do(http.MethodPost, path, body).
func (c *APIClient) Post(path string, body any) (*http.Response, []byte, error) {
	return c.Do(http.MethodPost, path, body)
}

// Patch is a shortcut for Do(http.MethodPatch, path, body).
func (c *APIClient) Patch(path string, body any) (*http.Response, []byte, error) {
	return c.Do(http.MethodPatch, path, body)
}

// Delete is a shortcut for Do(http.MethodDelete, path, nil).
func (c *APIClient) Delete(path string) (*http.Response, []byte, error) {
	return c.Do(http.MethodDelete, path, nil)
}

func isMutation(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return false
	}
	return true
}

// OCIPush is a backwards-compatible shim over OCIPushFromFixture pinned
// to the original "oras-push-job.yaml" fixture.
func (e *Env) OCIPush(t *testing.T, jobNS, jobName string) {
	t.Helper()
	e.OCIPushFromFixture(t, jobNS, jobName, "oras-push-job.yaml")
}

// OCIPushFromFixture applies the given fixture (a ConfigMap + oras Job)
// and waits for the Job to succeed. Used by module install and module
// upgrade tests, where each version needs its own Job + ConfigMap pair.
//
// jobNS is where the Job is created (typically "gameplane-system"), and
// jobName matches the metadata.name in the fixture.
func (e *Env) OCIPushFromFixture(t *testing.T, jobNS, jobName, fixture string) {
	t.Helper()
	ctx := context.Background()
	// Recreate each call so a previous failure doesn't leave a Failed
	// shell behind. Best-effort delete; ignore NotFound.
	bg := metav1.DeletePropagationBackground
	_ = e.K8s.BatchV1().Jobs(jobNS).Delete(ctx, jobName, metav1.DeleteOptions{
		PropagationPolicy: &bg,
	})
	e.ApplyYAML(t, fixture)
	e.Eventually(t, 90*time.Second, func() (bool, string) {
		j, err := e.K8s.BatchV1().Jobs(jobNS).Get(ctx, jobName, metav1.GetOptions{})
		if err != nil {
			return false, "get job: " + err.Error()
		}
		if j.Status.Succeeded > 0 {
			return true, ""
		}
		if j.Status.Failed > 0 {
			out, _ := e.Kubectl(ctx, "logs", "-n", jobNS, "job/"+jobName, "--tail=200")
			return false, "job failed:\n" + out
		}
		return false, fmt.Sprintf("job not done (succeeded=%d, failed=%d, active=%d)",
			j.Status.Succeeded, j.Status.Failed, j.Status.Active)
	})
}

// CreateUser POSTs /users as the given admin client and returns the
// generated credentials and the new user id. The username is suffixed
// with time.UnixNano() so a re-run against a stateful gameplane-system DB
// (the API PVC isn't wiped between local iterations) doesn't collide.
//
// The caller owns cleanup — register `t.Cleanup` to DELETE /users/{id}
// (best-effort; swallow 404). The helper does not register cleanup
// itself because some lifecycle tests want to assert on behavior across
// the user's full lifetime, including delete.
func (e *Env) CreateUser(t *testing.T, admin *APIClient, role, prefix string) (username, userPhrase, id string) {
	t.Helper()
	username = fmt.Sprintf("%s-%d", prefix, time.Now().UnixNano())
	userPhrase = "e2e-created-user-testdata-1234"
	resp, body, err := admin.Post("/users", map[string]string{
		"username": username,
		"password": userPhrase,
		"role":     role,
	})
	if err != nil {
		t.Fatalf("CreateUser post: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("CreateUser %s/%s %d: %s", role, prefix, resp.StatusCode, string(body))
	}
	id = extractIntField(string(body), "id")
	if id == "" {
		t.Fatalf("CreateUser: could not parse id from response: %s", string(body))
	}
	return username, userPhrase, id
}

// extractIntField is a tiny scanner for `"<key>":<digits>` inside a JSON
// blob. Lets the e2e tests parse the one or two integer fields they
// care about (user id, mostly) without pulling in encoding/json.
//
// Lives on env.go (not in a _test.go) so non-test helper code like
// CreateUser can use it.
func extractIntField(body, key string) string {
	needle := `"` + key + `":`
	i := strings.Index(body, needle)
	if i < 0 {
		return ""
	}
	rest := body[i+len(needle):]
	end := 0
	for end < len(rest) && rest[end] >= '0' && rest[end] <= '9' {
		end++
	}
	return rest[:end]
}
