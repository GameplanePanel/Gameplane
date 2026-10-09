//go:build e2e

package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/GameplanePanel/gameplane/test/e2e/internal/protocol/joindepth"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

// probeNamespace is where the game-bot probe Job runs. It must NOT be the games
// namespace: the chart installs a default-deny-egress NetworkPolicy there with
// `podSelector: {}`, so every pod in it — including the probe — may egress only
// to DNS, and the probe's connect to the game port would be dropped. From
// outside that namespace the probe's egress is unrestricted, and the game pod's
// own `allow-kubelet-probes` policy admits ingress from any RFC1918 pod IP.
// This also mirrors how a real player reaches the game: from off-cluster.
const probeNamespace = "default"

// probeJobNameMax is the longest probe Job name that still fits the `job-name`
// pod label, which is a 63-character label value.
const probeJobNameMax = 63

// probeJobDeleteWait bounds how long RunGameProbe waits for an earlier probe Job
// with the same name to disappear before it creates the next one.
const probeJobDeleteWait = 2 * time.Minute

// probeImage is the in-cluster game-bot image: built by the game-bot CI job
// (docker-bake.hcl target "e2e-gameprobe") and side-loaded into kind by
// deploy/kind/e2e.sh. Override it when the cluster pulls from a registry
// instead (e.g. a reused remote cluster).
func (e *Env) probeImage() string {
	if img := os.Getenv("GAMEPLANE_E2E_PROBE_IMAGE"); img != "" {
		return img
	}
	tag := e.Tag
	if tag == "" {
		tag = "e2e"
	}
	return "gameplane-test/gameprobe:" + tag
}

// GameProbe describes one in-cluster probe run.
type GameProbe struct {
	// GameNS is the namespace holding the GameServer.
	GameNS string
	// GSName is the GameServer name; its Service is what the probe dials.
	GSName string
	// Game is the module dir name, which selects the /probe/<Game> binary.
	Game string

	Port     int
	Deadline time.Duration

	// ExpectDepth is the depth the probe must reach, asserted by the probe itself.
	ExpectDepth joindepth.JoinDepth
	// ExpectFail inverts the assertion: the probe must NOT reach ExpectDepth, and the
	// Job is invoked with -expect-fail. Used by the automatic negative control.
	ExpectFail bool
	// Args carries extra per-game flags, e.g. []string{"-user", "bot"}.
	Args []string
	// JobSuffix, when set, is appended to the Job name ("<GSName>-probe-<JobSuffix>")
	// so back-to-back probes against the same GameServer each get their own Job.
	JobSuffix string
}

// ProbeResult carries the exit code, the parsed verdict and the parsed metrics
// from a probe run.
type ProbeResult struct {
	ExitCode int
	Verdict  *joindepth.ProbeVerdict
	// Metrics is the raw JSON object from the probe's `METRICS\t<json>` line.
	// It is nil when the probe printed no such line (non-sustained modes).
	Metrics json.RawMessage
}

// RunGameProbe runs the headless protocol bot as a Job inside the cluster,
// pointed at the game Service's DNS name, and returns the probe result.
//
// The bot runs in-cluster on purpose — it dials the game Service the way a real
// in-cluster client does, rather than tunnelling the game protocol through an
// apiserver SPDY connection via `kubectl port-forward`.
//
// gameprobe owns its own retry loop (a game server accepts TCP well before it
// accepts a login), so this waits for a single terminal Job outcome rather than
// retrying the protocol here.
//
// The Job runs in probeNamespace (default) and dials the game Service in gameNS.
// Returns a ProbeResult with the exit code and parsed VERDICT line, allowing the caller
// to distinguish exit code 2 (connected but wrong depth) from exit code 3 (transport failure).
func (e *Env) RunGameProbe(t *testing.T, p GameProbe) *ProbeResult {
	t.Helper()
	ctx := context.Background()
	jobName, err := probeJobName(p.GSName, p.JobSuffix)
	if err != nil {
		t.Fatalf("%s probe: %v", p.Game, err)
	}
	addr := fmt.Sprintf("%s.%s.svc.cluster.local:%d", p.GSName, p.GameNS, p.Port)

	// Recreate so a previous failure can't leave a Failed shell behind. The old
	// Job must be fully gone before the create below, or the create fails with
	// AlreadyExists when probes run back to back.
	if err := e.deleteProbeJob(ctx, jobName); err != nil {
		t.Fatalf("%s probe: %v", p.Game, err)
	}
	t.Cleanup(func() {
		bg := metav1.DeletePropagationBackground
		_ = e.K8s.BatchV1().Jobs(probeNamespace).Delete(
			context.Background(), jobName, metav1.DeleteOptions{PropagationPolicy: &bg})
	})

	var (
		nonRoot   = true
		noPrivEsc = false
		roRootFS  = true
		uid       = int64(65532)
		backoff   = int32(0)
	)

	// Build args: -addr, -deadline, -expect-depth, then -expect-fail if set, then any per-game args.
	args := []string{
		"-addr", addr,
		"-deadline", p.Deadline.String(),
		"-expect-depth", p.ExpectDepth.String(),
	}
	if p.ExpectFail {
		args = append(args, "-expect-fail")
	}
	args = append(args, p.Args...)

	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{Name: jobName, Namespace: probeNamespace},
		Spec: batchv1.JobSpec{
			// One shot: gameprobe already retries internally, so a non-zero
			// exit means the server never became playable.
			BackoffLimit: &backoff,
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					RestartPolicy: corev1.RestartPolicyNever,
					SecurityContext: &corev1.PodSecurityContext{
						RunAsNonRoot:   &nonRoot,
						RunAsUser:      &uid,
						RunAsGroup:     &uid,
						SeccompProfile: &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault},
					},
					Containers: []corev1.Container{{
						Name:  "gameprobe",
						Image: e.probeImage(),
						// Side-loaded into kind; never pulled from a registry.
						ImagePullPolicy: corev1.PullNever,
						Command:         []string{"/probe/" + p.Game},
						Args:            args,
						SecurityContext: &corev1.SecurityContext{
							RunAsNonRoot:             &nonRoot,
							AllowPrivilegeEscalation: &noPrivEsc,
							ReadOnlyRootFilesystem:   &roRootFS,
							Capabilities:             &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}},
						},
					}},
				},
			},
		},
	}
	created, err := e.K8s.BatchV1().Jobs(probeNamespace).Create(ctx, job, metav1.CreateOptions{})
	if err != nil {
		t.Fatalf("create %s probe job: %v", p.Game, err)
	}
	jobUID := created.UID

	// Allow a little more than the probe's own deadline, so a probe timeout
	// surfaces as its logged reason rather than as this wait expiring.
	wait := p.Deadline + 2*time.Minute
	expiry := time.Now().Add(wait)
	for {
		j, err := e.K8s.BatchV1().Jobs(probeNamespace).Get(ctx, jobName, metav1.GetOptions{})
		if err == nil {
			if j.Status.Succeeded > 0 {
				out, _ := e.Kubectl(ctx, "logs", "-n", probeNamespace, "job/"+jobName, "--tail=50")
				verdict, verdictErr := parseVerdictFromLogs(out)
				if verdictErr != nil {
					t.Logf("warning: failed to parse verdict from %s probe logs: %v", p.Game, verdictErr)
				}
				t.Logf("%s probe exit 0 (reached depth=%s):\n%s", p.Game, p.ExpectDepth.String(), out)
				return &ProbeResult{
					ExitCode: 0,
					Verdict:  verdict,
					Metrics:  parseMetricsLogged(t, p.Game, out),
				}
			}
			if j.Status.Failed > 0 {
				out, _ := e.Kubectl(ctx, "logs", "-n", probeNamespace, "job/"+jobName, "--tail=200")
				exitCode, exitErr := e.extractExitCode(ctx, jobName, jobUID)
				verdict, verdictErr := parseVerdictFromLogs(out)
				if verdictErr != nil {
					t.Logf("warning: failed to parse verdict from %s probe logs: %v", p.Game, verdictErr)
				}
				if exitErr != nil {
					t.Logf("warning: failed to extract exit code from %s probe job: %v", p.Game, exitErr)
					exitCode = 1 // default to internal error if we can't read the exit code
				}
				t.Logf("%s probe exit %d (expected depth=%s):\n%s", p.Game, exitCode, p.ExpectDepth.String(), out)
				return &ProbeResult{
					ExitCode: exitCode,
					Verdict:  verdict,
					Metrics:  parseMetricsLogged(t, p.Game, out),
				}
			}
		}
		if time.Now().After(expiry) {
			out, _ := e.Kubectl(ctx, "logs", "-n", probeNamespace, "job/"+jobName, "--tail=200")
			pods, _ := e.Kubectl(ctx, "get", "pods", "-n", probeNamespace, "-l", "job-name="+jobName, "-o", "wide")
			t.Fatalf("%s probe job did not finish within %s (is %s loaded into the cluster?)\npods:\n%s\nlogs:\n%s",
				p.Game, wait, e.probeImage(), pods, out)
		}
		time.Sleep(5 * time.Second)
	}
}

// probeJobName returns the probe Job name for a GameServer: "<GSName>-probe", with
// "-<suffix>" appended when a suffix is set. The GameServer part is trimmed so the
// whole name stays within probeJobNameMax.
func probeJobName(gsName, suffix string) (string, error) {
	tail := "-probe"
	if suffix != "" {
		tail += "-" + suffix
	}
	if len(tail) >= probeJobNameMax {
		return "", fmt.Errorf("probe job suffix %q leaves no room for a %d-char job name", suffix, probeJobNameMax)
	}
	base := gsName
	if room := probeJobNameMax - len(tail); len(base) > room {
		base = strings.TrimRight(base[:room], "-")
	}
	return base + tail, nil
}

// deleteProbeJob deletes a probe Job left by an earlier run and waits, bounded by
// probeJobDeleteWait, until the API server no longer returns it. A missing Job is
// not an error.
func (e *Env) deleteProbeJob(ctx context.Context, jobName string) error {
	bg := metav1.DeletePropagationBackground
	err := e.K8s.BatchV1().Jobs(probeNamespace).Delete(ctx, jobName, metav1.DeleteOptions{PropagationPolicy: &bg})
	if err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("delete probe job %s: %w", jobName, err)
	}

	expiry := time.Now().Add(probeJobDeleteWait)
	for {
		_, err := e.K8s.BatchV1().Jobs(probeNamespace).Get(ctx, jobName, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			return nil
		}
		if time.Now().After(expiry) {
			if err != nil {
				return fmt.Errorf("wait for probe job %s to be deleted: %w", jobName, err)
			}
			return fmt.Errorf("probe job %s still exists after %s", jobName, probeJobDeleteWait)
		}
		time.Sleep(time.Second)
	}
}

// extractExitCode reads the exit code from the gameprobe container of the pod
// owned by this run's Job. jobUID is the UID of the Job that was created for this
// run; a same-named Job from an earlier run can leave its pods behind briefly, so
// pods are matched by owner UID rather than by the job-name label alone.
func (e *Env) extractExitCode(ctx context.Context, jobName string, jobUID types.UID) (int, error) {
	pods, err := e.K8s.CoreV1().Pods(probeNamespace).List(ctx, metav1.ListOptions{
		LabelSelector: "job-name=" + jobName,
	})
	if err != nil {
		return -1, fmt.Errorf("list pods for job: %w", err)
	}

	var pod *corev1.Pod
	for i := range pods.Items {
		if ownedByJob(&pods.Items[i], jobUID) {
			pod = &pods.Items[i]
			break
		}
	}
	if pod == nil {
		return -1, fmt.Errorf("no pods found for job %s", jobName)
	}

	// Find the container status for the gameprobe container.
	for _, containerStatus := range pod.Status.ContainerStatuses {
		if containerStatus.Name == "gameprobe" {
			if containerStatus.State.Terminated != nil {
				return int(containerStatus.State.Terminated.ExitCode), nil
			}
		}
	}

	return -1, fmt.Errorf("container gameprobe not terminated or not found in pod %s", pod.Name)
}

// ownedByJob reports whether pod has an owner reference to the Job with uid.
func ownedByJob(pod *corev1.Pod, uid types.UID) bool {
	for _, ref := range pod.OwnerReferences {
		if ref.UID == uid {
			return true
		}
	}
	return false
}

// parseVerdictFromLogs searches the logs for a VERDICT line and parses it.
func parseVerdictFromLogs(logs string) (*joindepth.ProbeVerdict, error) {
	for _, line := range strings.Split(logs, "\n") {
		if strings.HasPrefix(line, "VERDICT") {
			return joindepth.ParseVerdict(strings.TrimSpace(line))
		}
	}
	return nil, fmt.Errorf("no VERDICT line found in logs")
}

// parseMetricsFromLogs searches the logs for a `METRICS\t<json>` line and returns
// its JSON object. It returns (nil, nil) when no such line exists, and (nil, err)
// when the line's payload is not valid JSON.
func parseMetricsFromLogs(logs string) (json.RawMessage, error) {
	for _, line := range strings.Split(logs, "\n") {
		payload, ok := strings.CutPrefix(line, "METRICS\t")
		if !ok {
			continue
		}
		raw := json.RawMessage(strings.TrimSpace(payload))
		if !json.Valid(raw) {
			return nil, fmt.Errorf("METRICS line is not valid JSON")
		}
		return raw, nil
	}
	return nil, nil
}

// parseMetricsLogged is parseMetricsFromLogs for RunGameProbe: a malformed METRICS
// line is logged as a warning and treated as absent, like a malformed VERDICT.
func parseMetricsLogged(t *testing.T, game, logs string) json.RawMessage {
	t.Helper()
	metrics, err := parseMetricsFromLogs(logs)
	if err != nil {
		t.Logf("warning: failed to parse METRICS from %s probe logs: %v", game, err)
		return nil
	}
	return metrics
}
