//go:build e2e

package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/watch"
)

// This bucket owns the PostgreSQL fixture after the normal API buckets finish.
// It deliberately changes a live Deployment's strategy, upgrades the release,
// and stops the database; it must never run concurrently with other tests.
func TestUpgrade_PostgresAvailability(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 12*time.Minute)
	defer cancel()
	const ns = "gameplane-system"
	deployments := envInstance.K8s.AppsV1().Deployments(ns)
	dep, err := deployments.Get(ctx, "gameplane-api", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(dep.Spec.Template.Spec.Containers[0].Args, " "), "--db-driver=postgres") {
		t.Fatal("postgres-availability requires the PostgreSQL E2E fixture")
	}
	envInstance.BootstrapAdmin(t, adminUsername, adminPassword)
	admin := envInstance.APIClient(t, adminUsername, adminPassword)
	defer admin.Close()
	_, _, userID := envInstance.CreateUser(t, admin, "viewer", "e2e-availability")

	// Model a pre-fix combined PostgreSQL installation, including the stale
	// rollingUpdate block that must be removed before Helm applies Recreate.
	_, err = deployments.Patch(ctx, dep.Name, types.MergePatchType,
		[]byte(`{"spec":{"strategy":{"type":"RollingUpdate","rollingUpdate":{"maxSurge":1,"maxUnavailable":0}}}}`), metav1.PatchOptions{})
	if err != nil {
		t.Fatal(err)
	}
	finishWatch := watchSingletonAPI(ctx, t)
	chart, err := findRepoRel("charts/gameplane")
	if err != nil {
		t.Fatal(err)
	}
	// Changing a real container env value forces a Pod replacement even when
	// the working-tree image tag is the same as the fixture's installed tag.
	logLevel := "debug"
	for _, variable := range dep.Spec.Template.Spec.Containers[0].Env {
		if variable.Name == "GAMEPLANE_LOG_LEVEL" && variable.Value == "debug" {
			logLevel = "info"
		}
	}
	upgrade := exec.CommandContext(ctx, "helm", "upgrade", "gameplane", chart,
		"--kube-context", envInstance.Context, "--namespace", ns, "--reuse-values",
		"--set-string", "api.logLevel="+logLevel, "--wait", "--timeout", "4m")
	finishWrites := writeUsersDuringAPIUpgrade(ctx, t, admin, userID)
	if out, err := upgrade.CombinedOutput(); err != nil {
		t.Fatalf("PostgreSQL strategy migration failed: %v\n%s", err, out)
	}
	finishWrites()
	finishWatch()
	dep, err = deployments.Get(ctx, dep.Name, metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if dep.Spec.Strategy.Type != appsv1.RecreateDeploymentStrategyType || dep.Spec.Strategy.RollingUpdate != nil || *dep.Spec.Replicas != 1 {
		t.Fatalf("unsafe API strategy after upgrade: %+v replicas=%v", dep.Spec.Strategy, dep.Spec.Replicas)
	}

	// A direct Pod forward stays reachable after readiness removes the Pod
	// from Service endpoints, so the outage assertions test the API itself.
	pods, err := envInstance.K8s.CoreV1().Pods(ns).List(ctx, metav1.ListOptions{LabelSelector: "app.kubernetes.io/name=gameplane-api"})
	if err != nil || len(pods.Items) != 1 {
		t.Fatalf("expected one API Pod after upgrade: %v pods=%v", err, pods)
	}
	pod := pods.Items[0]
	port, stop := envInstance.PortForward(t, ns, "pod/"+pod.Name, 8000)
	defer stop()
	// Reuse the pre-upgrade cookie and CSRF token; do not log in again.
	client := &APIClient{BaseURL: fmt.Sprintf("http://127.0.0.1:%d", port), HTTP: admin.HTTP, CSRF: admin.CSRF}
	assertAPIStatus(t, client, "/readyz", http.StatusOK)
	assertAPIStatus(t, client, "/users/me", http.StatusOK)
	resp, body, err := client.Patch("/users/"+userID, map[string]string{"displayName": "after singleton upgrade"})
	if err == nil {
		_ = resp.Body.Close()
	}
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("pre-upgrade user could not be updated: %v %s", err, body)
	}
	resp, body, err = client.Get("/admin/audit/verify")
	if err == nil {
		_ = resp.Body.Close()
	}
	var auditResult struct {
		OK      bool  `json:"ok"`
		Checked int64 `json:"checked"`
	}
	if err != nil || resp.StatusCode != http.StatusOK || json.Unmarshal(body, &auditResult) != nil || !auditResult.OK || auditResult.Checked == 0 {
		t.Fatalf("audit integrity after PostgreSQL upgrade: %v %s", err, body)
	}

	postgres := envInstance.K8s.AppsV1().StatefulSets(ns)
	scale, err := postgres.GetScale(ctx, "gameplane-postgres", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	replicas := scale.Spec.Replicas
	restore := func() {
		restoreCtx, stopRestore := context.WithTimeout(context.Background(), time.Minute)
		defer stopRestore()
		current, getErr := postgres.GetScale(restoreCtx, "gameplane-postgres", metav1.GetOptions{})
		if getErr != nil {
			t.Errorf("read PostgreSQL scale for recovery: %v", getErr)
			return
		}
		current.Spec.Replicas = replicas
		if _, updateErr := postgres.UpdateScale(restoreCtx, "gameplane-postgres", current, metav1.UpdateOptions{}); updateErr != nil {
			t.Errorf("restore PostgreSQL: %v", updateErr)
		}
	}
	t.Cleanup(restore)
	scale.Spec.Replicas = 0
	if _, err := postgres.UpdateScale(ctx, "gameplane-postgres", scale, metav1.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	envInstance.Eventually(t, 45*time.Second, func() (bool, string) {
		resp, body, err := client.DoContext(ctx, http.MethodGet, "/readyz", nil)
		if err == nil {
			_ = resp.Body.Close()
		}
		return err == nil && resp.StatusCode == http.StatusServiceUnavailable && string(body) == "not ready\n", fmt.Sprintf("readiness: %v %s", err, body)
	})
	assertAPIStatus(t, client, "/healthz", http.StatusOK)
	body = assertAPIStatus(t, client, "/users/me", http.StatusServiceUnavailable)
	if string(body) != "Service Unavailable\n" {
		t.Fatalf("session failure exposed detail: %q", body)
	}
	// Stay down longer than the liveness failure window (3 x 10 seconds).
	// Neither failed readiness nor session lookup should restart this Pod.
	envInstance.Consistently(t, 35*time.Second, time.Second, func() (bool, string) {
		current, err := envInstance.K8s.CoreV1().Pods(ns).Get(ctx, pod.Name, metav1.GetOptions{})
		if err != nil {
			return false, err.Error()
		}
		if current.UID != pod.UID || apiRestartCount(current) != apiRestartCount(&pod) {
			return false, "API restarted during database outage"
		}
		resp, body, err := client.DoContext(ctx, http.MethodGet, "/healthz", nil)
		if err == nil {
			_ = resp.Body.Close()
		}
		return err == nil && resp.StatusCode == http.StatusOK, fmt.Sprintf("liveness: %v %s", err, body)
	})
	current, err := envInstance.K8s.CoreV1().Pods(ns).Get(ctx, pod.Name, metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for _, condition := range current.Status.Conditions {
		if condition.Type == corev1.PodReady && condition.Status == corev1.ConditionTrue {
			t.Fatal("API remained Ready while PostgreSQL was unavailable")
		}
	}
	restore()
	envInstance.Eventually(t, 90*time.Second, func() (bool, string) {
		resp, body, err := client.DoContext(ctx, http.MethodGet, "/readyz", nil)
		if err == nil {
			_ = resp.Body.Close()
		}
		return err == nil && resp.StatusCode == http.StatusOK, fmt.Sprintf("recovery readiness: %v %s", err, body)
	})
	assertAPIStatus(t, client, "/users/me", http.StatusOK)
	current, err = envInstance.K8s.CoreV1().Pods(ns).Get(ctx, pod.Name, metav1.GetOptions{})
	if err != nil || current.UID != pod.UID || apiRestartCount(current) != apiRestartCount(&pod) {
		t.Fatalf("recovery restarted the API: %v", err)
	}
	// An unknown credential remains a true unauthorized response after recovery.
	anonymous := &APIClient{BaseURL: client.BaseURL, HTTP: &http.Client{Timeout: 5 * time.Second}}
	assertAPIStatus(t, anonymous, "/users/me", http.StatusUnauthorized)
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, client.BaseURL+"/users/me", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.AddCookie(&http.Cookie{Name: "gameplane_session", Value: "unknown-session"})
	resp, err = anonymous.HTTP.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unknown session after recovery returned %d", resp.StatusCode)
	}
	// A release can enable the API for the first time on upgrade, or recover
	// a deleted Deployment. The pre-upgrade migration must skip an absent API.
	foreground := metav1.DeletePropagationForeground
	if err := deployments.Delete(ctx, "gameplane-api", metav1.DeleteOptions{PropagationPolicy: &foreground}); err != nil {
		t.Fatal(err)
	}
	envInstance.Eventually(t, time.Minute, func() (bool, string) {
		_, err := deployments.Get(ctx, "gameplane-api", metav1.GetOptions{})
		return apierrors.IsNotFound(err), fmt.Sprintf("waiting for absent API Deployment: %v", err)
	})
	recreate := exec.CommandContext(ctx, "helm", "upgrade", "gameplane", chart,
		"--kube-context", envInstance.Context, "--namespace", ns, "--reuse-values",
		"--set", "api.enabled=true", "--wait", "--timeout", "4m")
	if out, err := recreate.CombinedOutput(); err != nil {
		t.Fatalf("upgrade with no existing API Deployment failed: %v\n%s", err, out)
	}
	recreated, err := deployments.Get(ctx, "gameplane-api", metav1.GetOptions{})
	if err != nil || recreated.Status.ReadyReplicas != 1 || recreated.Spec.Strategy.Type != appsv1.RecreateDeploymentStrategyType {
		t.Fatalf("missing API did not recover as a ready singleton: %v %+v", err, recreated)
	}
}

func assertAPIStatus(t *testing.T, client *APIClient, path string, want int) []byte {
	t.Helper()
	resp, body, err := client.DoContext(t.Context(), http.MethodGet, path, nil)
	if err == nil {
		_ = resp.Body.Close()
	}
	if err != nil || resp.StatusCode != want {
		t.Fatalf("%s: want %d: %v %s", path, want, err, body)
	}
	return body
}

func apiRestartCount(pod *corev1.Pod) int32 {
	for _, container := range pod.Status.ContainerStatuses {
		if container.Name == "api" {
			return container.RestartCount
		}
	}
	return 0
}

// Keep concurrent audited mutations arriving while the old API shuts down.
// The accepted Recreate outage can close its port-forward; session persistence
// and a mutation through the replacement are checked separately above.
func writeUsersDuringAPIUpgrade(ctx context.Context, t *testing.T, client *APIClient, userID string) func() {
	t.Helper()
	ctx, cancel := context.WithCancel(ctx)
	t.Cleanup(cancel)
	var workers sync.WaitGroup
	var successes atomic.Int64
	for worker := range 2 {
		workers.Go(func() {
			for {
				select {
				case <-ctx.Done():
					return
				default:
				}
				resp, _, err := client.DoContext(ctx, http.MethodPatch, "/users/"+userID, map[string]string{"displayName": fmt.Sprintf("upgrade writer %d", worker)})
				if err != nil {
					return // The old Pod's port-forward ends with the old process.
				}
				_ = resp.Body.Close()
				if resp.StatusCode == http.StatusOK {
					successes.Add(1)
				}
				select {
				case <-ctx.Done():
					return
				case <-time.After(time.Second):
				}
			}
		})
	}
	envInstance.Eventually(t, 10*time.Second, func() (bool, string) {
		return successes.Load() >= 2, "concurrent mutations have not succeeded before upgrade"
	})
	return func() {
		cancel()
		workers.Wait()
		t.Logf("%d successful concurrent user mutations before the old API stopped", successes.Load())
	}
}

// Watch from the list's resourceVersion so even a brief overlapping writer is
// observed. A terminating Pod still counts while its API container is Running.
func watchSingletonAPI(ctx context.Context, t *testing.T) func() {
	t.Helper()
	pods := envInstance.K8s.CoreV1().Pods("gameplane-system")
	options := metav1.ListOptions{LabelSelector: "app.kubernetes.io/name=gameplane-api"}
	initial, err := pods.List(ctx, options)
	if err != nil || len(initial.Items) != 1 {
		t.Fatalf("singleton baseline: %v pods=%v", err, initial)
	}
	oldUID := initial.Items[0].UID
	options.ResourceVersion = initial.ResourceVersion
	events, err := pods.Watch(ctx, options)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(events.Stop)
	done := make(chan error, 1)
	replaced := make(chan struct{}, 1)
	stop := make(chan struct{})
	go func() {
		states := map[types.UID]corev1.Pod{oldUID: initial.Items[0]}
		for {
			select {
			case <-stop:
				done <- nil
				return
			case <-ctx.Done():
				done <- ctx.Err()
				return
			case event, ok := <-events.ResultChan():
				pod, isPod := event.Object.(*corev1.Pod)
				if !ok || event.Type == watch.Error || !isPod {
					done <- fmt.Errorf("API Pod watch ended without proving singleton rollout: %v", event.Type)
					return
				}
				if event.Type == watch.Deleted {
					delete(states, pod.UID)
				} else {
					states[pod.UID] = *pod
				}
				running := 0
				for _, state := range states {
					for _, container := range state.Status.ContainerStatuses {
						if container.Name == "api" && container.State.Running != nil {
							running++
							if state.UID != oldUID {
								select {
								case replaced <- struct{}{}:
								default:
								}
							}
						}
					}
				}
				if running > 1 {
					done <- fmt.Errorf("%d API writers ran concurrently during upgrade", running)
					return
				}
			}
		}
	}()
	return func() {
		t.Helper()
		select {
		case err := <-done:
			t.Fatalf("singleton rollout observation: %v", err)
		case <-replaced:
		case <-ctx.Done():
			t.Fatal("no replacement API Pod observed")
		}
		close(stop)
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
}
