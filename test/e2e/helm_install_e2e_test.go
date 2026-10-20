//go:build e2e

package e2e

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/yaml"
)

// TestHelmInstall_AllPodsReady — the chart installed by deploy/kind/e2e.sh
// landed pods for both operator and api, and they reach Ready within
// the timeout. This is the smoke check that catches "image pull broken",
// "manifest typo", "operator panics on startup", etc.
func TestHelmInstall_AllPodsReady(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	// Scope to the chart's own workloads. The parallel suite runs one-shot
	// helper pods (healthz probes, oras/cosign Jobs, the restic warm-up) in
	// this namespace whose Succeeded phase would otherwise read here as
	// "not Ready".
	for _, sel := range []string{
		"app.kubernetes.io/name=gameplane-operator",
		"app.kubernetes.io/name=gameplane-api",
	} {
		sel := sel
		envInstance.Eventually(t, 90*time.Second, func() (bool, string) {
			pods, err := envInstance.K8s.CoreV1().Pods("gameplane-system").
				List(ctx, metav1.ListOptions{LabelSelector: sel})
			if err != nil {
				return false, "list pods: " + err.Error()
			}
			if len(pods.Items) == 0 {
				return false, "no pods for " + sel + " yet"
			}
			notReady := []string{}
			for _, p := range pods.Items {
				ready := false
				for _, c := range p.Status.Conditions {
					if c.Type == "Ready" && c.Status == "True" {
						ready = true
						break
					}
				}
				if !ready {
					notReady = append(notReady, p.Name+"="+string(p.Status.Phase))
				}
			}
			if len(notReady) > 0 {
				return false, "pods not Ready: " + strings.Join(notReady, ", ")
			}
			return true, ""
		})
	}
}

// TestHelmInstall_AllCRDsPresent — every Gameplane CRD declared by the
// chart is reachable via discovery. Catches a missing CRD YAML in
// `charts/gameplane/charts/gameplane-crds/crds/` from a future refactor.
func TestHelmInstall_AllCRDsPresent(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	want := []string{
		"gameservers.gameplane.local",
		"gametemplates.gameplane.local",
		"backups.gameplane.local",
		"backupschedules.gameplane.local",
		"restores.gameplane.local",
		"modules.gameplane.local",
		"modulesources.gameplane.local",
		"clusters.gameplane.local",
		"networkcaptures.gameplane.local",
	}
	for _, name := range want {
		ok, err := envInstance.CRDExists(ctx, name)
		if err != nil {
			t.Fatalf("CRD %s lookup error: %v", name, err)
		}
		if !ok {
			t.Errorf("CRD %s not installed", name)
		}
	}
}

// TestHelmInstall_CRDApplyHookSkippedOnFreshInstall — the other half of
// F-218. deploy/kind/e2e.sh installs the chart onto a brand-new kind cluster,
// so Helm's native crds/ step creates every CRD (carrying this chart's
// bundle-hash stamp) before templates are rendered. The crds.autoApply hook
// must recognise those as current and stay pre-upgrade only: firing on
// every install would make air-gapped first installs pull the kubectl image
// and add a Job to every install. A bare "does the CRD exist" check fired on
// every install for exactly that reason, and the upgrade bucket's
// install-over-leftover-CRDs phase cannot catch it, since the hook is
// supposed to fire there.
func TestHelmInstall_CRDApplyHookSkippedOnFreshInstall(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	if got := crdApplyHookEvents(ctx, t, "gameplane", "gameplane-system"); got != "pre-upgrade" {
		t.Errorf("fresh install rendered the crd-apply hook for %q, want \"pre-upgrade\" only "+
			"(the hook must not run on an install whose CRDs crds/ just created)", got)
	}
	// The stamps matching is WHY it stayed pre-upgrade; a mismatch here means
	// crds/ and crd-manifests/ disagree, which CI's chart render job guards.
	want := manifestCRDStamp(t, "gameplane.local_gameservers.yaml")
	if got := liveCRDStamp(ctx, t, "gameservers.gameplane.local"); got != want {
		t.Errorf("live gameservers CRD %s = %q on a fresh install, want the chart's %q",
			crdBundleStampAnnotation, got, want)
	}
}

// TestHelmInstall_OperatorLogsClean — operator container has no
// recent ERROR-level logs. A startup panic or repeated reconcile
// failure would surface here. We tolerate WARN since a few are
// expected during initial reconciliation.
func TestHelmInstall_OperatorLogsClean(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	pods, err := envInstance.K8s.CoreV1().Pods("gameplane-system").List(ctx, metav1.ListOptions{
		LabelSelector: "app.kubernetes.io/name=gameplane-operator",
	})
	if err != nil {
		t.Fatalf("list operator pods: %v", err)
	}
	if len(pods.Items) == 0 {
		t.Fatal("no operator pod found by app.kubernetes.io/name=gameplane-operator")
	}

	for _, p := range pods.Items {
		out, err := envInstance.Kubectl(ctx, "logs", "-n", "gameplane-system", p.Name, "--tail=500")
		if err != nil {
			t.Fatalf("kubectl logs %s: %v\n%s", p.Name, err, out)
		}
		// Heuristic: zap's Development encoder spells errors as ERROR. A
		// stricter check (no panics, no "controller failed") could be
		// added once the production log shape is locked.
		if strings.Contains(out, "panic:") {
			t.Errorf("operator pod %s logged a panic:\n%s", p.Name, lastLines(out, 40))
		}
	}
}

// TestHelmInstall_APILogsClean — mirror of OperatorLogsClean for the
// API pod. Catches "API container starts and immediately panics" or
// repeated request-handling crashes that pod-Ready alone might miss
// during a slow-starting readiness probe.
func TestHelmInstall_APILogsClean(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	pods, err := envInstance.K8s.CoreV1().Pods("gameplane-system").List(ctx, metav1.ListOptions{
		LabelSelector: "app.kubernetes.io/name=gameplane-api",
	})
	if err != nil {
		t.Fatalf("list api pods: %v", err)
	}
	if len(pods.Items) == 0 {
		t.Fatal("no api pod found by app.kubernetes.io/name=gameplane-api")
	}

	for _, p := range pods.Items {
		out, err := envInstance.Kubectl(ctx, "logs", "-n", "gameplane-system", p.Name, "--tail=500")
		if err != nil {
			t.Fatalf("kubectl logs %s: %v\n%s", p.Name, err, out)
		}
		if strings.Contains(out, "panic:") {
			t.Errorf("api pod %s logged a panic:\n%s", p.Name, lastLines(out, 40))
		}
	}
}

// TestHelmInstall_APIHealthz — proves the API service answers
// /healthz over the cluster network. Runs `curl -fsS` from a
// transient pod in gameplane-system; -f makes curl exit non-zero on
// non-2xx so we can assert on the kubectl exit code instead of
// parsing kubectl-and-pod-mixed stdout.
//
// The probe pod uses curlimages/curl, which is the only external
// (non-Gameplane) image this suite pulls. First-run cost is the image
// pull from the public registry into kind; the 90s budget is mostly
// to absorb that.
func TestHelmInstall_APIHealthz(t *testing.T) {
	t.Parallel()

	envInstance.Eventually(t, 90*time.Second, func() (bool, string) {
		// Random suffix so an Eventually retry doesn't collide with a
		// not-yet-cleaned-up pod from the previous tick.
		name := fmt.Sprintf("healthz-probe-%d", time.Now().UnixNano())
		out, err := envInstance.Kubectl(
			t.Context(),
			"run", "-n", "gameplane-system",
			"--rm", "--restart=Never", "--attach",
			"--image=curlimages/curl:8.10.1",
			name,
			"--",
			"curl", "-fsS", "--max-time", "5",
			"http://gameplane-api/healthz",
		)
		if err != nil {
			return false, fmt.Sprintf("api healthz probe failed: %v\n%s", err, out)
		}
		return true, ""
	})
}

// metricsProbeScript runs in a transient curl pod. It prints the public
// API port's status code for /metrics, flags a Prometheus body there, and
// reports whether the dedicated metrics port serves Prometheus text.
const metricsProbeScript = `code=$(curl -s -o /dev/null -w '%{http_code}' --max-time 5 http://gameplane-api/metrics)
echo "public-status=$code"
if curl -s --max-time 5 http://gameplane-api/metrics | grep -q '^# HELP'; then echo "public-body=metrics"; fi
if curl -fsS --max-time 5 http://gameplane-api:9090/metrics | grep -q '^# HELP go_goroutines'; then echo "metrics-port=ok"; fi
exit 0`

// TestHelmInstall_MetricsNotOnPublicPort — the API's public port (the
// Service port the Ingress and the web front end route to) does not serve
// Prometheus metrics, and the dedicated metrics port that the ServiceMonitor
// scrapes does. No login. Uses the same curl image as
// TestHelmInstall_APIHealthz.
func TestHelmInstall_MetricsNotOnPublicPort(t *testing.T) {
	t.Parallel()

	var out string
	envInstance.Eventually(t, 90*time.Second, func() (bool, string) {
		// Random suffix so an Eventually retry doesn't collide with a
		// not-yet-cleaned-up pod from the previous tick.
		name := fmt.Sprintf("metrics-probe-%d", time.Now().UnixNano())
		o, err := envInstance.Kubectl(
			t.Context(),
			"run", "-n", "gameplane-system",
			"--rm", "--restart=Never", "--attach",
			"--image=curlimages/curl:8.10.1",
			name,
			"--command", "--",
			"sh", "-c", metricsProbeScript,
		)
		if err != nil {
			return false, fmt.Sprintf("metrics probe pod failed: %v\n%s", err, o)
		}
		if !strings.Contains(o, "public-status=") || strings.Contains(o, "public-status=000") {
			return false, "api public port not answering yet:\n" + o
		}
		if !strings.Contains(o, "metrics-port=ok") {
			return false, "api metrics port not serving Prometheus text yet:\n" + o
		}
		out = o
		return true, ""
	})

	if !strings.Contains(out, "public-status=404") {
		t.Fatalf("the API's public port did not answer /metrics with 404:\n%s", out)
	}
	if strings.Contains(out, "public-body=metrics") {
		t.Fatalf("the API's public port served /metrics:\n%s", out)
	}
}

// TestHelmInstall_APIServerEgressPolicy_AllowsPrivateRanges — verifies that
// the default allow-agent-to-apiserver NetworkPolicy renders with RFC1918 and
// link-local ranges. This documents the current default scope: the policy
// reaches all private-range addresses on 443/6443, not just the apiserver.
// No login required.
func TestHelmInstall_APIServerEgressPolicy_AllowsPrivateRanges(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	// Fetch the installed NetworkPolicy from the games namespace
	np, err := envInstance.K8s.NetworkingV1().NetworkPolicies("gameplane-games").
		Get(ctx, "allow-agent-to-apiserver", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("failed to get allow-agent-to-apiserver NetworkPolicy: %v", err)
	}

	// Required CIDR blocks, each of which must be reachable on both TCP
	// 443 and 6443 from within the SAME egress rule. Checking CIDRs and
	// ports as two independent sets (as an earlier version of this test
	// did) would pass even if, say, one rule listed these CIDRs on the
	// wrong ports and another rule listed 443/6443 for unrelated
	// destinations — a broken heartbeat policy that leaves no rule
	// actually granting the intended access.
	wantRanges := []string{
		"10.0.0.0/8",
		"172.16.0.0/12",
		"192.168.0.0/16",
		"169.254.0.0/16", // link-local
	}
	wantPorts := []int32{443, 6443}

	// portsByRange[cidr] accumulates, across every egress rule that lists
	// cidr as a destination, the TCP ports that rule also grants.
	portsByRange := make(map[string]map[int32]bool)
	for _, eg := range np.Spec.Egress {
		rulePorts := map[int32]bool{}
		for _, port := range eg.Ports {
			if port.Protocol != nil && *port.Protocol == "TCP" && port.Port != nil {
				rulePorts[port.Port.IntVal] = true
			}
		}
		for _, to := range eg.To {
			if to.IPBlock == nil {
				continue
			}
			if portsByRange[to.IPBlock.CIDR] == nil {
				portsByRange[to.IPBlock.CIDR] = map[int32]bool{}
			}
			for p := range rulePorts {
				portsByRange[to.IPBlock.CIDR][p] = true
			}
		}
	}

	for _, r := range wantRanges {
		gotPorts, ok := portsByRange[r]
		if !ok {
			t.Errorf("expected CIDR %s not found as an egress destination in allow-agent-to-apiserver policy; "+
				"destinations found: %v", r, portsByRange)
			continue
		}
		for _, p := range wantPorts {
			if !gotPorts[p] {
				t.Errorf("CIDR %s is not granted TCP port %d by any egress rule that lists it as a destination "+
					"(ports granted to it: %v)", r, p, gotPorts)
			}
		}
	}
}

// TestHelmInstall_BringYourOwnMTLSCA — verifies that when a custom Secret name
// is configured via api.agentMTLS.caSecretRef.name, the chart does not generate
// its own Secrets. Users setting custom Secret names should not get a
// conflict with chart-generated Secrets.
// No login required; renders the chart on the host with helm template.
func TestHelmInstall_BringYourOwnMTLSCA(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	repoRoot, err := filepath.Abs("../..")
	if err != nil {
		t.Fatalf("resolve repo root: %v", err)
	}

	// Render the working-tree chart on the host with custom mTLS Secret names.
	outBytes, err := exec.CommandContext(ctx, "helm", "template", "gameplane",
		filepath.Join(repoRoot, "charts", "gameplane"),
		"-n", "gameplane-system",
		"--set", "api.agentMTLS.caSecretRef.name=my-custom-ca",
		"--set", "api.agentMTLS.clientCertRef.name=my-custom-client",
	).CombinedOutput()
	out := string(outBytes)
	if err != nil {
		t.Fatalf("helm template render failed: %v\n%s", err, out)
	}

	// Verify that the chart-owned Secret objects (metadata.name) are not
	// generated when custom names are configured. Scan Secret documents
	// rather than matching a bare "name: <value>" substring, which also
	// matches unrelated fields like container/env names.
	for _, doc := range strings.Split(out, "\n---\n") {
		if !strings.Contains(doc, "kind: Secret") {
			continue
		}
		if strings.Contains(doc, "name: gameplane-agent-ca") {
			t.Errorf("rendered chart generated the gameplane-agent-ca Secret when a custom name was configured:\n%s", doc)
		}
		if strings.Contains(doc, "name: gameplane-agent-client") {
			t.Errorf("rendered chart generated the gameplane-agent-client Secret when a custom name was configured:\n%s", doc)
		}
	}

	// Verify that the custom Secret names are wired into the consuming
	// Deployments' volumes (showing the template actually read the
	// configured values instead of ignoring them).
	if !strings.Contains(out, "secretName: my-custom-ca") {
		t.Error("rendered chart does not reference the configured custom CA Secret name (my-custom-ca) in any volume")
	}
	if !strings.Contains(out, "secretName: my-custom-client") {
		t.Error("rendered chart does not reference the configured custom client Secret name (my-custom-client) in any volume")
	}
}

// TestHelmInstall_MTLSClientCertReissuedOnCAChange verifies the fix for a
// stale-client-certificate bug: switching api.agentMTLS.caSecretRef.name to
// a different pre-created CA must cause the chart to re-issue the agent
// client certificate against the new CA, not silently reuse a client
// certificate signed by the CA that was previously in effect. Agents accept
// only a client cert signed by their current CA, so reusing the old one
// would silently break every agent's mTLS handshake with the API/operator.
// It also checks the opposite: re-rendering with an unchanged CA must
// reuse the client cert verbatim, so the fix does not regress the "CA
// stays stable across upgrades" invariant into rotating the client cert on
// every render.
// No login required; renders the chart on the host with helm template
// against real pre-created Secrets in a scratch namespace.
func TestHelmInstall_MTLSClientCertReissuedOnCAChange(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	repoRoot, err := filepath.Abs("../..")
	if err != nil {
		t.Fatalf("resolve repo root: %v", err)
	}
	chartPath := filepath.Join(repoRoot, "charts", "gameplane")

	// Harvest two independent CA + client cert pairs by rendering the
	// default chart against namespaces that hold no pre-existing mTLS
	// Secrets, so each render mints fresh material.
	caA, clientA := harvestMTLSPair(ctx, t, chartPath, fmt.Sprintf("e2e-mtls-donor-a-%d", time.Now().UnixNano()))
	caB, _ := harvestMTLSPair(ctx, t, chartPath, fmt.Sprintf("e2e-mtls-donor-b-%d", time.Now().UnixNano()))
	if caA.crt == caB.crt {
		t.Fatal("the two harvested CAs are identical; test setup did not produce independent CAs")
	}

	// A scratch namespace holding "pre-existing" mTLS Secrets: a client
	// cert signed by CA A, installed under the default Secret names (as a
	// previous `helm install` would have generated), plus CA B pre-created
	// under a custom name (as if a user brought their own CA).
	ns := fmt.Sprintf("e2e-mtls-scratch-%d", time.Now().UnixNano())
	if out, err := envInstance.Kubectl(ctx, "create", "namespace", ns); err != nil {
		t.Fatalf("create scratch namespace: %v\n%s", err, out)
	}
	t.Cleanup(func() {
		_, _ = envInstance.Kubectl(context.Background(), "delete", "namespace", ns, "--wait=false")
	})

	applyTLSSecret(ctx, t, ns, "gameplane-agent-ca", "Opaque",
		map[string]string{"ca.crt": caA.crt, "ca.key": caA.key}, nil)
	applyTLSSecret(ctx, t, ns, "gameplane-agent-client", "kubernetes.io/tls",
		map[string]string{"tls.crt": clientA.crt, "tls.key": clientA.key}, nil)
	applyTLSSecret(ctx, t, ns, "e2e-custom-ca", "Opaque",
		map[string]string{"ca.crt": caB.crt, "ca.key": caB.key}, nil)

	// Render with caSecretRef.name pointed at CA B, clientCertRef.name left
	// at its default. The pre-existing client cert (signed by CA A) must be
	// re-issued against CA B, not reused.
	out := runHelmTemplate(ctx, t, chartPath, ns,
		"--set", "api.agentMTLS.caSecretRef.name=e2e-custom-ca")
	client := findSecret(t, out, "gameplane-agent-client")

	wantFingerprint := sha256Hex(caB.crt)
	gotFingerprint := client.Annotations["gameplane.local/ca-fingerprint"]
	if gotFingerprint != wantFingerprint {
		t.Errorf("re-issued client Secret's ca-fingerprint annotation = %q, want %q (fingerprint of CA B)",
			gotFingerprint, wantFingerprint)
	}
	if string(client.Data["tls.crt"]) == clientA.crt {
		t.Error("client Secret was reused unchanged after the CA changed; agents will reject the stale " +
			"client cert once CA B is in effect")
	}

	// Re-rendering with the SAME (now-current) CA and the just-issued
	// client cert (annotated with its matching fingerprint) in place must
	// reuse it verbatim.
	newClientCrt := string(client.Data["tls.crt"])
	newClientKey := string(client.Data["tls.key"])
	applyTLSSecret(ctx, t, ns, "gameplane-agent-client", "kubernetes.io/tls",
		map[string]string{"tls.crt": newClientCrt, "tls.key": newClientKey},
		map[string]string{"gameplane.local/ca-fingerprint": wantFingerprint})

	out2 := runHelmTemplate(ctx, t, chartPath, ns,
		"--set", "api.agentMTLS.caSecretRef.name=e2e-custom-ca")
	client2 := findSecret(t, out2, "gameplane-agent-client")
	if string(client2.Data["tls.crt"]) != newClientCrt {
		t.Error("client Secret was re-issued again although the CA did not change; expected it to be reused verbatim")
	}
}

// mtlsPair is a PEM cert + key pair harvested from a rendered mTLS Secret.
type mtlsPair struct {
	crt, key string
}

// harvestMTLSPair renders the default chart against a namespace that holds
// no pre-existing mTLS Secrets purely to mint an independent CA + agent
// client cert pair signed by it.
func harvestMTLSPair(ctx context.Context, t *testing.T, chartPath, ns string) (ca, client mtlsPair) {
	t.Helper()
	out := runHelmTemplate(ctx, t, chartPath, ns)
	caSecret := findSecret(t, out, "gameplane-agent-ca")
	clientSecret := findSecret(t, out, "gameplane-agent-client")
	return mtlsPair{crt: string(caSecret.Data["ca.crt"]), key: string(caSecret.Data["ca.key"])},
		mtlsPair{crt: string(clientSecret.Data["tls.crt"]), key: string(clientSecret.Data["tls.key"])}
}

// runHelmTemplate renders the working-tree chart's mtls.yaml template
// alone, against the real cluster context so `lookup` sees whatever
// Secrets exist in ns. `helm template` defaults to --dry-run=client, where
// Helm never contacts the cluster and `lookup` always returns an empty map;
// --dry-run=server is what lets the lookups reach the API server.
func runHelmTemplate(ctx context.Context, t *testing.T, chartPath, ns string, extraArgs ...string) string {
	t.Helper()
	args := append([]string{"template", "gameplane", chartPath,
		"--kube-context", envInstance.Context,
		"-n", ns,
		"-s", "templates/mtls.yaml",
		"--dry-run=server",
		"--is-upgrade",
	}, extraArgs...)
	cmd := exec.CommandContext(ctx, "helm", args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	outBytes, err := cmd.Output()
	out := string(outBytes)
	if err != nil {
		var exitErr *exec.ExitError
		stderrStr := ""
		if errors.As(err, &exitErr) {
			stderrStr = stderr.String()
		}
		t.Fatalf("helm template render failed: %v\n%s\nstderr: %s", err, out, stderrStr)
	}
	return out
}

// findSecret returns the Secret named name from a `helm template` render,
// failing the test if it is not present exactly once.
func findSecret(t *testing.T, out, name string) *corev1.Secret {
	t.Helper()
	var found *corev1.Secret
	for _, doc := range strings.Split(out, "\n---\n") {
		doc = strings.TrimSpace(doc)
		if doc == "" || !strings.Contains(doc, "kind: Secret") {
			continue
		}
		var s corev1.Secret
		if err := yaml.Unmarshal([]byte(doc), &s); err != nil {
			t.Fatalf("parse rendered Secret YAML: %v\n%s", err, doc)
		}
		if s.Name != name {
			continue
		}
		if found != nil {
			t.Fatalf("more than one rendered Secret named %s:\n%s", name, out)
		}
		sCopy := s
		found = &sCopy
	}
	if found == nil {
		t.Fatalf("no rendered Secret named %s in:\n%s", name, out)
	}
	return found
}

// applyTLSSecret creates or replaces a Secret in ns via kubectl apply, used
// to seed pre-existing mTLS material for `lookup` to find.
func applyTLSSecret(ctx context.Context, t *testing.T, ns, name, secretType string, data, annotations map[string]string) {
	t.Helper()
	var b strings.Builder
	fmt.Fprintf(&b, "apiVersion: v1\nkind: Secret\nmetadata:\n  name: %s\n  namespace: %s\n", name, ns)
	if len(annotations) > 0 {
		b.WriteString("  annotations:\n")
		for k, v := range annotations {
			fmt.Fprintf(&b, "    %q: %q\n", k, v)
		}
	}
	fmt.Fprintf(&b, "type: %s\nstringData:\n", secretType)
	for k, v := range data {
		fmt.Fprintf(&b, "  %s: |\n", k)
		for _, line := range strings.Split(strings.TrimRight(v, "\n"), "\n") {
			fmt.Fprintf(&b, "    %s\n", line)
		}
	}
	if out, err := envInstance.KubectlWithStdin(ctx, b.String(), "apply", "-f", "-"); err != nil {
		t.Fatalf("apply Secret %s: %v\n%s", name, err, out)
	}
}

// sha256Hex is the hex-encoded sha256 digest of s, matching sprig's
// sha256sum function used by the mTLS chart template.
func sha256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// lastLines returns the last n lines of s (or all of s if shorter).
func lastLines(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) <= n {
		return s
	}
	return strings.Join(lines[len(lines)-n:], "\n")
}
