//go:build e2e

package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/gopacket/gopacket"
	"github.com/gopacket/gopacket/layers"
	"github.com/gopacket/gopacket/pcapgo"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/intstr"
)

// gameTemplateGVR / gameServerGVR / networkCaptureGVR — typed clients
// aren't generated for the test/e2e module; we use the dynamic client.
var (
	gameTemplateGVR   = schema.GroupVersionResource{Group: "gameplane.local", Version: "v1alpha1", Resource: "gametemplates"}
	gameServerGVR     = schema.GroupVersionResource{Group: "gameplane.local", Version: "v1alpha1", Resource: "gameservers"}
	networkCaptureGVR = schema.GroupVersionResource{Group: "gameplane.local", Version: "v1alpha1", Resource: "networkcaptures"}
)

// testCaptureMaxSize is the maximum size requested in capture-start calls.
// Must stay at or below charts/gameplane/values.yaml's capture.defaultMaxSizeBytes (900 MiB).
// This is well below that limit to avoid hitting the emptyDir sizeLimit (1 GiB).
const testCaptureMaxSize = 10485760 // 10 MiB

// TestGameServer_OperatorMaterializesChildren — apply a tiny template
// + a GameServer that references it. The operator must produce a
// StatefulSet, Service, and PVC. We do NOT wait for pods to reach
// Ready — that requires a real game image and the kind node may not
// be able to pull large external images. The test asserts the operator
// shaped the right cluster objects, which is the contract that
// matters at the operator layer.
func TestGameServer_OperatorMaterializesChildren(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	ns := "gameplane-games"

	// Use a busybox-based "fake game" so the operator can construct a
	// pod spec that won't fail to render. Image is never actually
	// pulled here — we don't wait for the pod.
	tmplName := "e2e-busybox"
	tmpl := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "gameplane.local/v1alpha1",
		"kind":       "GameTemplate",
		"metadata":   map[string]any{"name": tmplName},
		"spec": map[string]any{
			"displayName": "E2E busybox",
			"game":        "busybox",
			"version":     "1",
			"image":       "busybox:1.36",
			"command":     []any{"sh", "-c", "sleep 100000"},
			"ports": []any{
				map[string]any{"name": "noop", "containerPort": int64(12345), "advertise": true, "protocol": "TCP"},
			},
			// Exercise spec.config materialization end to end: a value
			// set explicitly, a default the operator must fill in, and a
			// password that must land in a Secret instead of the pod spec.
			"configSchema": []any{
				map[string]any{"name": "DIFFICULTY", "type": "enum", "enum": []any{"easy", "hard"}, "default": "easy"},
				map[string]any{"name": "MAX_PLAYERS", "type": "int", "default": "16"},
				map[string]any{"name": "SERVER_PASS", "type": "password"},
				map[string]any{"name": "MOTD", "type": "string", "default": "hello e2e", "target": "file"},
			},
			// Exercise target:file materialization: the rendered file must
			// land in the <gs>-files Secret and be wired to the pod via the
			// config-init container, never into env.
			"configFiles": []any{
				map[string]any{
					"path":     "cfg/server.cfg",
					"template": "motd={{ .Values.MOTD }}\npass={{ .Values.SERVER_PASS }}\n",
				},
			},
		},
	}}
	if _, err := envInstance.Dyn.Resource(gameTemplateGVR).
		Create(ctx, tmpl, metav1.CreateOptions{}); err != nil && !apierrors.IsAlreadyExists(err) {
		t.Fatalf("create template: %v", err)
	}
	t.Cleanup(func() {
		_ = envInstance.Dyn.Resource(gameTemplateGVR).
			Delete(context.Background(), tmplName, metav1.DeleteOptions{})
	})

	gsName := "e2e-test-srv"
	gs := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "gameplane.local/v1alpha1",
		"kind":       "GameServer",
		"metadata":   map[string]any{"name": gsName, "namespace": ns},
		"spec": map[string]any{
			"templateRef": map[string]any{"name": tmplName},
			"config": map[string]any{
				"DIFFICULTY":  "hard",
				"SERVER_PASS": "e2e-secret",
			},
		},
	}}
	if _, err := envInstance.Dyn.Resource(gameServerGVR).Namespace(ns).
		Create(ctx, gs, metav1.CreateOptions{}); err != nil && !apierrors.IsAlreadyExists(err) {
		t.Fatalf("create gameserver: %v", err)
	}
	t.Cleanup(func() {
		_ = envInstance.Dyn.Resource(gameServerGVR).Namespace(ns).
			Delete(context.Background(), gsName, metav1.DeleteOptions{})
	})

	envInstance.Eventually(t, 60*time.Second, func() (bool, string) {
		// StatefulSet
		if _, err := envInstance.K8s.AppsV1().StatefulSets(ns).Get(ctx, gsName, metav1.GetOptions{}); err != nil {
			return false, "ss: " + err.Error()
		}
		// Service
		if _, err := envInstance.K8s.CoreV1().Services(ns).Get(ctx, gsName, metav1.GetOptions{}); err != nil {
			return false, "svc: " + err.Error()
		}
		// PVC named <gs>-data
		if _, err := envInstance.K8s.CoreV1().PersistentVolumeClaims(ns).Get(ctx, gsName+"-data", metav1.GetOptions{}); err != nil {
			return false, "pvc: " + err.Error()
		}
		return true, ""
	})

	// Sanity-check the StatefulSet's pod template has the agent sidecar.
	ss, err := envInstance.K8s.AppsV1().StatefulSets(ns).Get(ctx, gsName, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get statefulset: %v", err)
	}
	names := []string{}
	for _, c := range ss.Spec.Template.Spec.Containers {
		names = append(names, c.Name)
	}
	if !contains(names, "agent") {
		t.Errorf("sidecar missing — container names: %s", strings.Join(names, ","))
	}
	if !contains(names, "game") {
		t.Errorf("game container missing — container names: %s", strings.Join(names, ","))
	}

	// spec.config must be materialized: explicit value, schema default,
	// and the password routed through the <gs>-config Secret.
	for _, c := range ss.Spec.Template.Spec.Containers {
		if c.Name != "game" {
			continue
		}
		env := map[string]string{}
		var passRef string
		for _, e := range c.Env {
			env[e.Name] = e.Value
			if e.Value == "e2e-secret" {
				t.Errorf("password appears inline in pod spec env %s", e.Name)
			}
			if e.Name == "SERVER_PASS" && e.ValueFrom != nil && e.ValueFrom.SecretKeyRef != nil {
				passRef = e.ValueFrom.SecretKeyRef.Name
			}
		}
		if env["DIFFICULTY"] != "hard" {
			t.Errorf("DIFFICULTY = %q, want hard", env["DIFFICULTY"])
		}
		if env["MAX_PLAYERS"] != "16" {
			t.Errorf("MAX_PLAYERS = %q, want default 16", env["MAX_PLAYERS"])
		}
		if passRef != gsName+"-config" {
			t.Errorf("SERVER_PASS SecretKeyRef = %q, want %s-config", passRef, gsName)
		}
		if _, ok := env["MOTD"]; ok {
			t.Errorf("file-target field MOTD leaked into game env")
		}
	}
	sec, err := envInstance.K8s.CoreV1().Secrets(ns).Get(ctx, gsName+"-config", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get config secret: %v", err)
	}
	if string(sec.Data["SERVER_PASS"]) != "e2e-secret" {
		t.Errorf("config secret does not hold the password")
	}

	// target:file config must be rendered into the <gs>-files Secret and
	// reach the pod through the config-init container.
	filesSec, err := envInstance.K8s.CoreV1().Secrets(ns).Get(ctx, gsName+"-files", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get files secret: %v", err)
	}
	if got := string(filesSec.Data["file-0"]); got != "motd=hello e2e\npass=e2e-secret\n" {
		t.Errorf("rendered file-0 = %q", got)
	}
	inits := ss.Spec.Template.Spec.InitContainers
	if len(inits) != 1 || inits[0].Name != "config-init" {
		t.Fatalf("expected a config-init init container, got %+v", inits)
	}
	foundVol := false
	for _, v := range ss.Spec.Template.Spec.Volumes {
		if v.Name != "config-files" {
			continue
		}
		foundVol = true
		if v.Secret == nil || v.Secret.SecretName != gsName+"-files" {
			t.Errorf("config-files volume not backed by %s-files: %+v", gsName, v)
		} else if len(v.Secret.Items) != 1 || v.Secret.Items[0].Key != "file-0" ||
			v.Secret.Items[0].Path != "cfg/server.cfg" {
			t.Errorf("config-files items do not map file-0 to cfg/server.cfg: %+v", v.Secret.Items)
		}
	}
	if !foundVol {
		t.Errorf("config-files volume missing from pod spec")
	}
}

func contains(ss []string, s string) bool {
	for _, v := range ss {
		if v == s {
			return true
		}
	}
	return false
}

// TestGameServer_PVCSurvivesPodDelete: deleting pod-0 must not destroy
// the persistent <gs>-data PVC. The StatefulSet's volumeClaimTemplate
// guarantees this in K8s, but a regression in how the operator scopes
// the PVC's owner references could tie its lifetime to the pod.
//
// We delete pod-0 and assert the StatefulSet recreates a pod with a
// different UID, while the PVC keeps the same UID throughout.
func TestGameServer_PVCSurvivesPodDelete(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	ns := "gameplane-games"
	tmpl := "e2e-pvc-survive-tmpl"
	gs := "e2e-pvc-survive-gs"

	applyBusyboxTemplate(t, tmpl)
	applyBusyboxGameServer(t, ns, gs, tmpl)
	waitPVCBound(t, ns, gs+"-data", 90*time.Second)
	waitStatefulSetReplicas(t, ns, gs, 1, 90*time.Second)

	// Wait for pod-0 to be present so we can capture its UID.
	envInstance.Eventually(t, 60*time.Second, func() (bool, string) {
		_, err := envInstance.K8s.CoreV1().Pods(ns).Get(ctx, gs+"-0", metav1.GetOptions{})
		if err != nil {
			return false, "get pod: " + err.Error()
		}
		return true, ""
	})

	pvcPre, err := envInstance.K8s.CoreV1().PersistentVolumeClaims(ns).
		Get(ctx, gs+"-data", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get pvc pre-delete: %v", err)
	}
	pvcUID := pvcPre.UID

	podPre, err := envInstance.K8s.CoreV1().Pods(ns).Get(ctx, gs+"-0", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get pod pre-delete: %v", err)
	}
	oldPodUID := podPre.UID

	if err := envInstance.K8s.CoreV1().Pods(ns).
		Delete(ctx, gs+"-0", metav1.DeleteOptions{}); err != nil {
		t.Fatalf("delete pod-0: %v", err)
	}

	// StatefulSet recreates pod-0 with a fresh UID.
	envInstance.Eventually(t, 2*time.Minute, func() (bool, string) {
		pod, err := envInstance.K8s.CoreV1().Pods(ns).Get(ctx, gs+"-0", metav1.GetOptions{})
		if err != nil {
			return false, "get pod: " + err.Error()
		}
		if pod.UID == oldPodUID {
			return false, "pod still has old UID"
		}
		return true, ""
	})

	// PVC UID is unchanged — it must NOT have been recreated.
	pvcPost, err := envInstance.K8s.CoreV1().PersistentVolumeClaims(ns).
		Get(ctx, gs+"-data", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get pvc post-delete: %v", err)
	}
	if pvcPost.UID != pvcUID {
		t.Errorf("PVC UID changed after pod delete (pre=%s, post=%s) — pod ownership leaked into PVC lifetime",
			pvcUID, pvcPost.UID)
	}
}

// TestGameServer_HeartbeatReachesRunning: with the per-GameServer
// ServiceAccount, the heartbeat RBAC, and the agent->apiserver egress
// policy in place, the agent's status heartbeat must land and the
// operator must derive phase Running. Before those existed, no chart
// install could ever leave Starting — this is the regression guard.
func TestGameServer_HeartbeatReachesRunning(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	ns := "gameplane-games"
	tmpl := "e2e-hb-tmpl"
	gs := "e2e-hb-gs"

	applyBusyboxTemplate(t, tmpl)
	applyBusyboxGameServer(t, ns, gs, tmpl)
	waitPVCBound(t, ns, gs+"-data", 90*time.Second)
	requireAgentReady(t, ns, gs)

	// Heartbeat interval is 20s and the status reconciler requeues every
	// ~15s, so a couple of minutes is comfortable without being flaky.
	envInstance.Eventually(t, 3*time.Minute, func() (bool, string) {
		obj, err := envInstance.Dyn.Resource(gameServerGVR).Namespace(ns).
			Get(ctx, gs, metav1.GetOptions{})
		if err != nil {
			return false, "get gameserver: " + err.Error()
		}
		hb, _, _ := unstructured.NestedString(obj.Object, "status", "agent", "lastHeartbeat")
		if hb == "" {
			return false, "no heartbeat yet"
		}
		phase, _, _ := unstructured.NestedString(obj.Object, "status", "phase")
		if phase != "Running" {
			return false, "heartbeat present but phase=" + phase
		}
		return true, ""
	})
}

// TestGameServer_NetworkCaptureStartStopDownload — opt a GameServer into
// capture (spec.capture.enabled=true, set directly on the spec at creation
// so the sidecar is injected on the first reconcile), start a capture with a BPF filter
// restricted to a single advertised port, generate both filter-matching
// and non-matching traffic, stop the capture, download the file, and
// assert:
//
//   - SC-001 (third-party readability): the download parses cleanly as
//     structurally valid PCAPNG via gopacket's pcapgo.NewNgReader — a
//     real third-party parser, independent of the sidecar's own writer,
//     so a truncated or malformed file (right magic bytes, broken
//     structure) is caught rather than rubber-stamped. tshark/capinfos
//     are NOT installed anywhere in this repo's CI workflows (no step
//     in .github/workflows/*.yaml installs wireshark-common or
//     tshark, and the ubuntu-latest/ubuntu-24.04-arm runner images
//     don't ship them by default), so pcapgo is the actual third-party
//     verification that runs here — this comment is deliberately
//     explicit about that rather than claiming tshark coverage the
//     runner can't provide.
//   - SC-008 (filter correctness): every packet decoded from the file
//     carries the filter-matching TCP port, at least one such packet
//     exists (guards against a silently-empty-but-valid file passing
//     by coincidence), and zero packets carry the non-matching port.
//   - Default filter (FR-003): a second capture started with no filter
//     reaches Running, because the operator builds the filter from the
//     template's advertised ports, and every packet in its file is on
//     the advertised port. It reuses this test's API session.
func TestGameServer_NetworkCaptureStartStopDownload(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	ns := "gameplane-games"
	tmpl := "e2e-cap-tmpl"
	gsName := "e2e-cap-gs"
	trafficPodName := "e2e-cap-traffic-gen"

	const (
		matchPort    = 12345 // advertised port from applyBusyboxTemplate
		nonMatchPort = 54321
	)

	envInstance.BootstrapAdmin(t, adminUsername, adminPassword)
	cli := envInstance.APIClient(t, adminUsername, adminPassword)
	defer cli.Close()

	applyBusyboxTemplate(t, tmpl)
	// spec.capture.enabled=true at creation time — see
	// applyBusyboxGameServerWithCapture's doc comment for why the flag is
	// set at creation rather than through the :capture-enable route. The
	// operator injects the capture sidecar as an ephemeral container as
	// soon as it observes the flag.
	applyBusyboxGameServerWithCapture(t, ns, gsName, tmpl)
	waitPVCBound(t, ns, gsName+"-data", 90*time.Second)
	requireAgentReady(t, ns, gsName)

	// The ephemeral capture container must reach Running
	// (status.capture.ready=true on the GameServer CRD) before we start
	// a capture against it — starting against an unready sidecar would
	// race the sidecar's own startup and could silently produce an
	// empty file.
	envInstance.Eventually(t, 90*time.Second, func() (bool, string) {
		obj, err := envInstance.Dyn.Resource(gameServerGVR).Namespace(ns).
			Get(ctx, gsName, metav1.GetOptions{})
		if err != nil {
			return false, "get gameserver: " + err.Error()
		}
		ready, _, _ := unstructured.NestedBool(obj.Object, "status", "capture", "ready")
		if !ready {
			return false, "status.capture.ready still false"
		}
		return true, ""
	})

	// Start a capture with a filter restricted to matchPort.
	startReq := map[string]any{
		"filter":                  fmt.Sprintf("tcp port %d", matchPort),
		"maxDurationSeconds":      300,
		"maxSizeBytes":            testCaptureMaxSize,
		"ttlSecondsAfterFinished": 86400,
	}
	startHTTPResp, startBody, err := cli.Post("/servers/"+gsName+":capture-start", startReq)
	if err != nil {
		t.Fatalf("start capture: %v", err)
	}
	defer func() { _ = startHTTPResp.Body.Close() }()
	if startHTTPResp.StatusCode != http.StatusAccepted {
		t.Fatalf("start capture: status=%s body=%s", startHTTPResp.Status, startBody)
	}
	var startedCapture struct {
		CaptureID string `json:"captureId"`
	}
	if err := json.Unmarshal(startBody, &startedCapture); err != nil {
		t.Fatalf("parse capture-start response %q: %v", startBody, err)
	}
	captureID := startedCapture.CaptureID
	if captureID == "" {
		t.Fatalf("capture-start response had no captureId: %s", startBody)
	}
	t.Cleanup(func() {
		_ = envInstance.Dyn.Resource(networkCaptureGVR).Namespace(ns).
			Delete(context.Background(), captureID, metav1.DeleteOptions{})
	})

	// Wait for the operator to reconcile the NetworkCapture CRD from
	// Pending to Running — the sidecar isn't actually recording packets
	// until then, so generating traffic before this races capture start.
	envInstance.Eventually(t, 60*time.Second, func() (bool, string) {
		obj, err := envInstance.Dyn.Resource(networkCaptureGVR).Namespace(ns).
			Get(ctx, captureID, metav1.GetOptions{})
		if err != nil {
			return false, "get networkcapture: " + err.Error()
		}
		phase, _, _ := unstructured.NestedString(obj.Object, "status", "phase")
		if phase != "Running" {
			return false, "phase=" + phase
		}
		return true, ""
	})

	// Get the pod IP to generate traffic against.
	pod, err := envInstance.K8s.CoreV1().Pods(ns).Get(ctx, gsName+"-0", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get pod: %v", err)
	}
	podIP := pod.Status.PodIP
	if podIP == "" {
		t.Fatalf("pod has no IP yet")
	}

	// The games namespace ships a default-deny-egress NetworkPolicy
	// (podSelector: {}, DNS-only) and the operator's own per-server
	// ingress policy admits only the advertised game port. Left as-is,
	// the traffic pod's connects to nonMatchPort would never leave its
	// own netns and the "zero packets on the non-matching port"
	// assertion below would pass because the CNI dropped the traffic,
	// not because BPF filtered it — proving nothing about SC-008.
	// NetworkPolicies for a given pod are additive (OR across every
	// policy that selects it), so adding explicit allow rules here
	// makes the traffic genuinely reach the game pod's netns without
	// having to touch (or fight) the always-on cluster policies.
	trafficLabels := map[string]string{"gameplane.local/e2e-role": "capture-traffic"}
	gamePodLabels := map[string]string{
		"app.kubernetes.io/name":     "gameplane-game",
		"app.kubernetes.io/instance": gsName,
	}
	tcpPorts := func() []networkingv1.NetworkPolicyPort {
		proto := corev1.ProtocolTCP
		matchP := intstr.FromInt(matchPort)
		nonMatchP := intstr.FromInt(nonMatchPort)
		return []networkingv1.NetworkPolicyPort{
			{Protocol: &proto, Port: &matchP},
			{Protocol: &proto, Port: &nonMatchP},
		}
	}
	ingressAllow := &networkingv1.NetworkPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: gsName + "-e2e-allow-capture-traffic-in", Namespace: ns},
		Spec: networkingv1.NetworkPolicySpec{
			PodSelector: metav1.LabelSelector{MatchLabels: gamePodLabels},
			PolicyTypes: []networkingv1.PolicyType{networkingv1.PolicyTypeIngress},
			Ingress: []networkingv1.NetworkPolicyIngressRule{{
				From:  []networkingv1.NetworkPolicyPeer{{PodSelector: &metav1.LabelSelector{MatchLabels: trafficLabels}}},
				Ports: tcpPorts(),
			}},
		},
	}
	egressAllow := &networkingv1.NetworkPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: trafficPodName + "-e2e-allow-capture-traffic-out", Namespace: ns},
		Spec: networkingv1.NetworkPolicySpec{
			PodSelector: metav1.LabelSelector{MatchLabels: trafficLabels},
			PolicyTypes: []networkingv1.PolicyType{networkingv1.PolicyTypeEgress},
			Egress: []networkingv1.NetworkPolicyEgressRule{{
				To:    []networkingv1.NetworkPolicyPeer{{PodSelector: &metav1.LabelSelector{MatchLabels: gamePodLabels}}},
				Ports: tcpPorts(),
			}},
		},
	}
	if _, err := envInstance.K8s.NetworkingV1().NetworkPolicies(ns).Create(ctx, ingressAllow, metav1.CreateOptions{}); err != nil {
		t.Fatalf("create ingress-allow networkpolicy: %v", err)
	}
	t.Cleanup(func() {
		_ = envInstance.K8s.NetworkingV1().NetworkPolicies(ns).
			Delete(context.Background(), ingressAllow.Name, metav1.DeleteOptions{})
	})
	if _, err := envInstance.K8s.NetworkingV1().NetworkPolicies(ns).Create(ctx, egressAllow, metav1.CreateOptions{}); err != nil {
		t.Fatalf("create egress-allow networkpolicy: %v", err)
	}
	t.Cleanup(func() {
		_ = envInstance.K8s.NetworkingV1().NetworkPolicies(ns).
			Delete(context.Background(), egressAllow.Name, metav1.DeleteOptions{})
	})

	// A helper closure creates and waits for a traffic pod. The pod sends TCP
	// connections to both filter-matching and non-matching ports. Neither port
	// has a real listener (busybox never accepts connections), but the SYN/RST
	// exchange is captured regardless. Uses `nc -w <secs> <ip> <port> </dev/null`
	// rather than `-zv`: busybox's nc applet only supports -z/-v when built with
	// NC_110_COMPAT; `-w` plus stdin redirect is universally supported.
	createAndWaitTrafficPod := func(podName string) {
		trafficPod := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      podName,
				Namespace: ns,
				Labels:    trafficLabels,
			},
			Spec: corev1.PodSpec{
				RestartPolicy: corev1.RestartPolicyNever,
				Containers: []corev1.Container{
					{
						Name:  "traffic-gen",
						Image: "busybox:1.36",
						Command: []string{
							"sh", "-c",
							fmt.Sprintf(
								"(nc -w 2 %s %d </dev/null 2>&1; echo done-match) & "+
									"(nc -w 2 %s %d </dev/null 2>&1; echo done-nonmatch) & wait",
								podIP, matchPort, podIP, nonMatchPort,
							),
						},
					},
				},
			},
		}
		if _, err := envInstance.K8s.CoreV1().Pods(ns).Create(ctx, trafficPod, metav1.CreateOptions{}); err != nil {
			t.Fatalf("create traffic pod %s: %v", podName, err)
		}
		t.Cleanup(func() {
			// Pass the test's ctx through; WithoutCancel keeps the delete
			// running even if that context is cancelled by cleanup time.
			_ = envInstance.K8s.CoreV1().Pods(ns).Delete(context.WithoutCancel(ctx), podName, metav1.DeleteOptions{})
		})

		// Poll for the traffic pod to finish rather than a bare sleep — a
		// loaded CI runner can blow past a fixed sleep before scheduling
		// even starts the pod.
		envInstance.Eventually(t, 60*time.Second, func() (bool, string) {
			p, err := envInstance.K8s.CoreV1().Pods(ns).Get(ctx, podName, metav1.GetOptions{})
			if err != nil {
				return false, "get traffic pod: " + err.Error()
			}
			if p.Status.Phase != corev1.PodSucceeded && p.Status.Phase != corev1.PodFailed {
				return false, "traffic pod phase=" + string(p.Status.Phase)
			}
			return true, ""
		})
	}

	// Create and run the first traffic pod for the filtered capture.
	createAndWaitTrafficPod(trafficPodName)

	// Log the first traffic pod's output for debugging.
	if trafficLogs, err := envInstance.Kubectl(ctx, "logs", "-n", ns, trafficPodName); err != nil {
		t.Logf("traffic pod logs: (failed to fetch: %v)", err)
	} else {
		t.Logf("traffic pod logs:\n%s", trafficLogs)
	}

	// Stop the capture. The contract takes captureId in the JSON body,
	// not a query parameter.
	stopHTTPResp, stopBody, err := cli.Post("/servers/"+gsName+":capture-stop", map[string]any{
		"captureId": captureID,
	})
	if err != nil {
		t.Fatalf("stop capture: %v", err)
	}
	defer func() { _ = stopHTTPResp.Body.Close() }()
	if stopHTTPResp.StatusCode != http.StatusOK {
		t.Fatalf("stop capture: status=%s body=%s", stopHTTPResp.Status, stopBody)
	}

	// The capture must reach a terminal phase before the file is
	// guaranteed downloadable.
	envInstance.Eventually(t, 60*time.Second, func() (bool, string) {
		obj, err := envInstance.Dyn.Resource(networkCaptureGVR).Namespace(ns).
			Get(ctx, captureID, metav1.GetOptions{})
		if err != nil {
			return false, "get networkcapture: " + err.Error()
		}
		phase, _, _ := unstructured.NestedString(obj.Object, "status", "phase")
		if phase != "Completed" {
			return false, "phase=" + phase
		}
		return true, ""
	})

	// Download the capture file.
	fileHTTPResp, fileBody, err := cli.Get("/servers/" + gsName + ":capture-file?id=" + captureID)
	if err != nil {
		t.Fatalf("download capture file: %v", err)
	}
	defer func() { _ = fileHTTPResp.Body.Close() }()
	if fileHTTPResp.StatusCode != http.StatusOK {
		t.Fatalf("download capture file: status=%s", fileHTTPResp.Status)
	}
	if len(fileBody) == 0 {
		t.Fatalf("capture file is empty")
	}

	// SC-001: parse with gopacket's pcapgo reader — a genuine
	// third-party PCAPNG parser distinct from the sidecar's own writer.
	reader, err := pcapgo.NewNgReader(bytes.NewReader(fileBody), pcapgo.DefaultNgReaderOptions)
	if err != nil {
		t.Fatalf("capture file is not valid PCAPNG: %v", err)
	}

	// SC-008: every decoded packet must carry matchPort, and at least
	// one packet must be present.
	matchedPackets := 0
	for {
		data, _, err := reader.ReadPacketData()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("read packet from capture: %v", err)
		}
		packet := gopacket.NewPacket(data, reader.LinkType(), gopacket.Default)
		tcpLayer := packet.Layer(layers.LayerTypeTCP)
		if tcpLayer == nil {
			t.Fatalf("captured packet has no TCP layer despite filter %q: %s", startReq["filter"], packet.String())
		}
		tcp, ok := tcpLayer.(*layers.TCP)
		if !ok {
			t.Fatalf("TCP layer type assertion failed for packet: %s", packet.String())
		}
		srcPort := uint16(tcp.SrcPort)
		dstPort := uint16(tcp.DstPort)
		if srcPort != matchPort && dstPort != matchPort {
			t.Errorf("captured packet on neither src nor dst port %d: src=%d dst=%d", matchPort, srcPort, dstPort)
		}
		if srcPort == nonMatchPort || dstPort == nonMatchPort {
			t.Errorf("captured a packet on the non-matching port %d: src=%d dst=%d", nonMatchPort, srcPort, dstPort)
		}
		matchedPackets++
	}
	if matchedPackets == 0 {
		t.Fatalf("capture file contains zero packets — filter correctness (SC-008) cannot be verified against an empty capture")
	}
	t.Logf("capture file size=%d bytes, packets=%d, all matched filter %q (SC-001+SC-008 verified)",
		len(fileBody), matchedPackets, startReq["filter"])

	// Default filter (FR-003). Wait until the operator has told the sidecar
	// to stop the first capture (it holds one capture at a time), then start
	// a second capture with no filter on the same session.
	envInstance.Eventually(t, 60*time.Second, func() (bool, string) {
		obj, err := envInstance.Dyn.Resource(networkCaptureGVR).Namespace(ns).
			Get(ctx, captureID, metav1.GetOptions{})
		if err != nil {
			return false, "get networkcapture: " + err.Error()
		}
		conds, _, _ := unstructured.NestedSlice(obj.Object, "status", "conditions")
		for _, c := range conds {
			m, ok := c.(map[string]any)
			if ok && m["type"] == "SidecarStopped" && m["status"] == "True" {
				return true, ""
			}
		}
		return false, "first capture not yet stopped on the sidecar"
	})

	defaultReq := map[string]any{
		"maxDurationSeconds":      300,
		"maxSizeBytes":            testCaptureMaxSize,
		"ttlSecondsAfterFinished": 86400,
	}
	var defaultCaptureID string
	envInstance.Eventually(t, 60*time.Second, func() (bool, string) {
		resp, body, err := cli.Post("/servers/"+gsName+":capture-start", defaultReq)
		if err != nil {
			return false, "start capture without filter: " + err.Error()
		}
		_ = resp.Body.Close()
		if resp.StatusCode == http.StatusConflict {
			return false, "previous capture still holds the server's capture lock"
		}
		if resp.StatusCode != http.StatusAccepted {
			t.Fatalf("start capture without filter: status=%s body=%s", resp.Status, body)
		}
		var started struct {
			CaptureID string `json:"captureId"`
		}
		if err := json.Unmarshal(body, &started); err != nil || started.CaptureID == "" {
			t.Fatalf("parse capture-start response %q: %v", body, err)
		}
		defaultCaptureID = started.CaptureID
		return true, ""
	})
	t.Cleanup(func() {
		_ = envInstance.Dyn.Resource(networkCaptureGVR).Namespace(ns).
			Delete(context.Background(), defaultCaptureID, metav1.DeleteOptions{})
	})

	envInstance.Eventually(t, 60*time.Second, func() (bool, string) {
		obj, err := envInstance.Dyn.Resource(networkCaptureGVR).Namespace(ns).
			Get(ctx, defaultCaptureID, metav1.GetOptions{})
		if err != nil {
			return false, "get networkcapture: " + err.Error()
		}
		phase, _, _ := unstructured.NestedString(obj.Object, "status", "phase")
		if phase == "Failed" {
			msg, _, _ := unstructured.NestedString(obj.Object, "status", "message")
			t.Fatalf("capture without filter failed: %s", msg)
		}
		if phase != "Running" {
			return false, "phase=" + phase
		}
		return true, ""
	})

	// Create and run a second traffic pod for the default-filtered capture.
	// This ensures there is traffic to capture before the capture is stopped.
	createAndWaitTrafficPod(trafficPodName + "-default")

	stopDefaultResp, stopDefaultBody, err := cli.Post("/servers/"+gsName+":capture-stop", map[string]any{
		"captureId": defaultCaptureID,
	})
	if err != nil {
		t.Fatalf("stop capture without filter: %v", err)
	}
	defer func() { _ = stopDefaultResp.Body.Close() }()
	if stopDefaultResp.StatusCode != http.StatusOK {
		t.Fatalf("stop capture without filter: status=%s body=%s", stopDefaultResp.Status, stopDefaultBody)
	}
	envInstance.Eventually(t, 60*time.Second, func() (bool, string) {
		obj, err := envInstance.Dyn.Resource(networkCaptureGVR).Namespace(ns).
			Get(ctx, defaultCaptureID, metav1.GetOptions{})
		if err != nil {
			return false, "get networkcapture: " + err.Error()
		}
		phase, _, _ := unstructured.NestedString(obj.Object, "status", "phase")
		if phase != "Completed" {
			return false, "phase=" + phase
		}
		return true, ""
	})

	defaultFileResp, defaultFileBody, err := cli.Get("/servers/" + gsName + ":capture-file?id=" + defaultCaptureID)
	if err != nil {
		t.Fatalf("download capture file without filter: %v", err)
	}
	defer func() { _ = defaultFileResp.Body.Close() }()
	if defaultFileResp.StatusCode != http.StatusOK {
		t.Fatalf("download capture file without filter: status=%s", defaultFileResp.Status)
	}
	defaultReader, err := pcapgo.NewNgReader(bytes.NewReader(defaultFileBody), pcapgo.DefaultNgReaderOptions)
	if err != nil {
		t.Fatalf("capture file without filter is not valid PCAPNG: %v", err)
	}
	defaultPackets := 0
	for {
		data, _, err := defaultReader.ReadPacketData()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("read packet from capture without filter: %v", err)
		}
		packet := gopacket.NewPacket(data, defaultReader.LinkType(), gopacket.Default)
		tcp, ok := packet.Layer(layers.LayerTypeTCP).(*layers.TCP)
		if !ok {
			t.Fatalf("default-filter capture holds a non-TCP packet; the template advertises only TCP %d: %s", matchPort, packet.String())
		}
		if uint16(tcp.SrcPort) != matchPort && uint16(tcp.DstPort) != matchPort {
			t.Errorf("default-filter capture holds a packet off the advertised port %d: src=%d dst=%d", matchPort, tcp.SrcPort, tcp.DstPort)
		}
		defaultPackets++
	}
	if defaultPackets == 0 {
		t.Fatalf("capture without a filter recorded no packets; the default-filter port check did not run")
	}
	t.Logf("capture without filter reached Running; file size=%d bytes, packets=%d, all on the advertised port %d",
		len(defaultFileBody), defaultPackets, matchPort)
}

// findContainerByName returns the container named `name` from cs, or nil.
func findContainerByName(cs []corev1.Container, name string) *corev1.Container {
	for i := range cs {
		if cs[i].Name == name {
			return &cs[i]
		}
	}
	return nil
}

// findContainerStatusByName returns the container status named `name`
// from css, or nil.
func findContainerStatusByName(css []corev1.ContainerStatus, name string) *corev1.ContainerStatus {
	for i := range css {
		if css[i].Name == name {
			return &css[i]
		}
	}
	return nil
}

// TestGameServer_NetworkCaptureEphemeralContainer — the structural
// counterpart to TestGameServer_NetworkCaptureStartStopDownload: no real
// packet traffic here, just the injection/removal-refusal contract for
// the capture ephemeral container.
//
// FR-001 as amended: enabling capture must not perturb the running game
// workload — no game-container restart, no image/securityContext/mount
// change. We snapshot the game container before enabling and diff it
// against the same fields after.
//
// The disable half asserts the documented asymmetry explicitly: disable
// stops the capability (status.capture.ready flips false, new
// :capture-start calls are refused) but Kubernetes provides no API to
// remove an already-injected ephemeral container, so its entry in
// pod.status.ephemeralContainerStatuses (and pod.spec.ephemeralContainers)
// legitimately lingers. That lingering is the ratified platform
// behavior — assert it stays present, don't "fix" the test if it does.
func TestGameServer_NetworkCaptureEphemeralContainer(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	ns := "gameplane-games"
	tmpl := "e2e-cap-ec-tmpl"
	gsName := "e2e-cap-ec-gs"

	envInstance.BootstrapAdmin(t, adminUsername, adminPassword)
	cli := envInstance.APIClient(t, adminUsername, adminPassword)
	defer cli.Close()

	applyBusyboxTemplate(t, tmpl)
	// Created WITHOUT capture enabled — this test drives enable/disable
	// entirely through the :capture-enable/:capture-disable HTTP routes.
	applyBusyboxGameServer(t, ns, gsName, tmpl)
	waitPVCBound(t, ns, gsName+"-data", 90*time.Second)
	requireAgentReady(t, ns, gsName)

	podPre, err := envInstance.K8s.CoreV1().Pods(ns).Get(ctx, gsName+"-0", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get pod pre-enable: %v", err)
	}
	if len(podPre.Spec.EphemeralContainers) != 0 {
		t.Fatalf("pod already has ephemeral containers before capture was ever enabled: %+v",
			podPre.Spec.EphemeralContainers)
	}
	gameContainerPre := findContainerByName(podPre.Spec.Containers, "game")
	if gameContainerPre == nil {
		t.Fatalf("no game container in pre-enable pod spec")
	}
	gameStatusPre := findContainerStatusByName(podPre.Status.ContainerStatuses, "game")
	if gameStatusPre == nil {
		t.Fatalf("no game container status in pre-enable pod")
	}
	preImage := gameContainerPre.Image
	preSecurityContext := gameContainerPre.SecurityContext
	preVolumeMounts := gameContainerPre.VolumeMounts
	preRestartCount := gameStatusPre.RestartCount

	// Enable capture.
	enableHTTPResp, enableBody, err := cli.Post("/servers/"+gsName+":capture-enable", nil)
	if err != nil {
		t.Fatalf("enable capture: %v", err)
	}
	defer func() { _ = enableHTTPResp.Body.Close() }()
	if enableHTTPResp.StatusCode != http.StatusOK {
		t.Fatalf("enable capture: status=%s body=%s", enableHTTPResp.Status, enableBody)
	}

	// The ephemeral container appears on the pod spec as soon as the
	// operator observes spec.capture.enabled=true — no pod recreation.
	var capEC corev1.EphemeralContainer
	envInstance.Eventually(t, 60*time.Second, func() (bool, string) {
		pod, err := envInstance.K8s.CoreV1().Pods(ns).Get(ctx, gsName+"-0", metav1.GetOptions{})
		if err != nil {
			return false, "get pod: " + err.Error()
		}
		for _, ec := range pod.Spec.EphemeralContainers {
			if ec.Name == "capture" {
				capEC = ec
				return true, ""
			}
		}
		return false, "no capture ephemeral container in pod spec yet"
	})

	if !strings.Contains(capEC.Image, "capture-sidecar") {
		t.Errorf("capture ephemeral container image = %q, want it to contain capture-sidecar", capEC.Image)
	}
	mountsCaptures := false
	for _, m := range capEC.VolumeMounts {
		if m.Name == "captures" {
			mountsCaptures = true
			break
		}
	}
	if !mountsCaptures {
		t.Errorf("capture ephemeral container does not mount the captures volume: %+v", capEC.VolumeMounts)
	}
	sc := capEC.SecurityContext
	if sc == nil {
		t.Fatalf("capture ephemeral container has no securityContext")
	}
	if sc.AllowPrivilegeEscalation == nil || !*sc.AllowPrivilegeEscalation {
		t.Errorf("capture ephemeral container allowPrivilegeEscalation = %v, want true", sc.AllowPrivilegeEscalation)
	}
	if sc.RunAsNonRoot == nil || !*sc.RunAsNonRoot {
		t.Errorf("capture ephemeral container runAsNonRoot = %v, want true", sc.RunAsNonRoot)
	}
	// Drop ALL + Add NET_RAW: Drop ALL alone would empty the bounding set,
	// which makes the kernel refuse to grant the setcap'd binary's file
	// capability at execve (EPERM). Re-adding NET_RAW keeps it in the
	// bounding set only; the capability grant itself still comes from the
	// binary's file capability (setcap cap_net_raw+ep), not from this Add.
	if sc.Capabilities == nil || len(sc.Capabilities.Add) != 1 || sc.Capabilities.Add[0] != corev1.Capability("NET_RAW") {
		t.Errorf("capture ephemeral container securityContext.capabilities.add = %v, want [NET_RAW]",
			sc.Capabilities.Add)
	}

	// The game container itself must be untouched: same image, same
	// securityContext, same mounts, no restart.
	podPostEnable, err := envInstance.K8s.CoreV1().Pods(ns).Get(ctx, gsName+"-0", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get pod post-enable: %v", err)
	}
	gameContainerPost := findContainerByName(podPostEnable.Spec.Containers, "game")
	if gameContainerPost == nil {
		t.Fatalf("no game container in post-enable pod spec")
	}
	if gameContainerPost.Image != preImage {
		t.Errorf("game container image changed after enabling capture: pre=%q post=%q", preImage, gameContainerPost.Image)
	}
	if !reflect.DeepEqual(gameContainerPost.SecurityContext, preSecurityContext) {
		t.Errorf("game container securityContext changed after enabling capture: pre=%+v post=%+v",
			preSecurityContext, gameContainerPost.SecurityContext)
	}
	if !reflect.DeepEqual(gameContainerPost.VolumeMounts, preVolumeMounts) {
		t.Errorf("game container volumeMounts changed after enabling capture: pre=%+v post=%+v",
			preVolumeMounts, gameContainerPost.VolumeMounts)
	}
	gameStatusPost := findContainerStatusByName(podPostEnable.Status.ContainerStatuses, "game")
	if gameStatusPost == nil {
		t.Fatalf("no game container status in post-enable pod")
	}
	if gameStatusPost.RestartCount != preRestartCount {
		t.Errorf("game container restart count changed after enabling capture: pre=%d post=%d",
			preRestartCount, gameStatusPost.RestartCount)
	}

	// Wait for the sidecar to actually come up before disabling — this
	// mirrors TestGameServer_NetworkCaptureStartStopDownload's rationale:
	// disabling before the sidecar was ever observed ready would still
	// exercise the asymmetry, but waiting first proves injection worked
	// end to end, not just that the pod spec mutation landed.
	envInstance.Eventually(t, 90*time.Second, func() (bool, string) {
		obj, err := envInstance.Dyn.Resource(gameServerGVR).Namespace(ns).
			Get(ctx, gsName, metav1.GetOptions{})
		if err != nil {
			return false, "get gameserver: " + err.Error()
		}
		ready, _, _ := unstructured.NestedBool(obj.Object, "status", "capture", "ready")
		if !ready {
			return false, "status.capture.ready still false"
		}
		return true, ""
	})

	// Disable capture.
	disableHTTPResp, disableBody, err := cli.Post("/servers/"+gsName+":capture-disable", nil)
	if err != nil {
		t.Fatalf("disable capture: %v", err)
	}
	defer func() { _ = disableHTTPResp.Body.Close() }()
	if disableHTTPResp.StatusCode != http.StatusOK {
		t.Fatalf("disable capture: status=%s body=%s", disableHTTPResp.Status, disableBody)
	}

	// Half one of the asymmetry: status.capture.ready flips false
	// immediately (the operator does not wait for the ephemeral container
	// to actually stop — it can't force that anyway).
	envInstance.Eventually(t, 30*time.Second, func() (bool, string) {
		obj, err := envInstance.Dyn.Resource(gameServerGVR).Namespace(ns).
			Get(ctx, gsName, metav1.GetOptions{})
		if err != nil {
			return false, "get gameserver: " + err.Error()
		}
		ready, _, _ := unstructured.NestedBool(obj.Object, "status", "capture", "ready")
		if ready {
			return false, "status.capture.ready still true"
		}
		active, found, _ := unstructured.NestedString(obj.Object, "status", "capture", "activeCapture")
		if found && active != "" {
			return false, "status.capture.activeCapture still set to " + active
		}
		return true, ""
	})

	// Half one, continued: a new capture is refused once disabled.
	startHTTPResp, startBody, err := cli.Post("/servers/"+gsName+":capture-start", map[string]any{
		"filter":             "tcp port 12345",
		"maxDurationSeconds": 60,
		"maxSizeBytes":       1048576,
	})
	if err != nil {
		t.Fatalf("capture-start after disable: %v", err)
	}
	defer func() { _ = startHTTPResp.Body.Close() }()
	if startHTTPResp.StatusCode != http.StatusBadRequest {
		t.Errorf("capture-start after disable: status=%s body=%s, want 400 (capture not enabled)",
			startHTTPResp.Status, startBody)
	}

	// Half two of the asymmetry, the one nobody should ever "fix": the
	// ephemeral container Kubernetes already injected cannot be removed
	// through any Kubernetes API (there is no pods/ephemeralcontainers
	// delete/remove operation), so both its spec entry and its
	// status.ephemeralContainerStatuses entry legitimately remain on the
	// pod after disable. This is documented, ratified platform behavior
	// (see FR-001's amendment and US2 acceptance scenario 4 in spec.md) —
	// asserting its absence here would be asserting a capability
	// Kubernetes does not offer.
	podPostDisable, err := envInstance.K8s.CoreV1().Pods(ns).Get(ctx, gsName+"-0", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get pod post-disable: %v", err)
	}
	specStillPresent := false
	for _, ec := range podPostDisable.Spec.EphemeralContainers {
		if ec.Name == "capture" {
			specStillPresent = true
			break
		}
	}
	if !specStillPresent {
		t.Errorf("capture ephemeral container vanished from pod.spec.ephemeralContainers after disable — " +
			"Kubernetes has no API to remove an ephemeral container; this entry should linger until the pod " +
			"is recreated (see FR-001's disable amendment)")
	}
	statusStillPresent := false
	for _, cs := range podPostDisable.Status.EphemeralContainerStatuses {
		if cs.Name == "capture" {
			statusStillPresent = true
			break
		}
	}
	if !statusStillPresent {
		t.Errorf("capture entry vanished from pod.status.ephemeralContainerStatuses after disable — " +
			"Kubernetes has no API to remove an ephemeral container; this entry should linger until the pod " +
			"is recreated (see FR-001's disable amendment)")
	}
}

// TestGameServer_NetworkCaptureRestartCleanup — a pod deletion mid-capture
// must not leave the NetworkCapture in Running forever. The reconciler must
// detect the pod restart (UID mismatch) and transition to Failed with a
// reason. Furthermore, status.capture.activeCapture must be cleared so the
// server is not left in a permanently locked state — we verify this by
// starting a new capture after the pod returns and asserting it succeeds.
func TestGameServer_NetworkCaptureRestartCleanup(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	ns := "gameplane-games"
	tmpl := "e2e-cap-restart-tmpl"
	gsName := "e2e-cap-restart-gs"

	const matchPort = 12345

	envInstance.BootstrapAdmin(t, adminUsername, adminPassword)
	cli := envInstance.APIClient(t, adminUsername, adminPassword)
	defer cli.Close()

	applyBusyboxTemplate(t, tmpl)
	applyBusyboxGameServerWithCapture(t, ns, gsName, tmpl)
	waitPVCBound(t, ns, gsName+"-data", 90*time.Second)
	requireAgentReady(t, ns, gsName)

	// Wait for the ephemeral capture container to be ready.
	envInstance.Eventually(t, 90*time.Second, func() (bool, string) {
		obj, err := envInstance.Dyn.Resource(gameServerGVR).Namespace(ns).
			Get(ctx, gsName, metav1.GetOptions{})
		if err != nil {
			return false, "get gameserver: " + err.Error()
		}
		ready, _, _ := unstructured.NestedBool(obj.Object, "status", "capture", "ready")
		return ready, ""
	})

	// Start a capture.
	startReq := map[string]any{
		"filter":                  fmt.Sprintf("tcp port %d", matchPort),
		"maxDurationSeconds":      300,
		"maxSizeBytes":            testCaptureMaxSize,
		"ttlSecondsAfterFinished": 86400,
	}
	startHTTPResp, startBody, err := cli.Post("/servers/"+gsName+":capture-start", startReq)
	if err != nil {
		t.Fatalf("start capture: %v", err)
	}
	defer func() { _ = startHTTPResp.Body.Close() }()
	if startHTTPResp.StatusCode != http.StatusAccepted {
		t.Fatalf("start capture: status=%s body=%s", startHTTPResp.Status, startBody)
	}
	var startedCapture struct {
		CaptureID string `json:"captureId"`
	}
	if err := json.Unmarshal(startBody, &startedCapture); err != nil {
		t.Fatalf("parse capture-start response: %v", err)
	}
	captureID := startedCapture.CaptureID
	t.Cleanup(func() {
		_ = envInstance.Dyn.Resource(networkCaptureGVR).Namespace(ns).
			Delete(context.Background(), captureID, metav1.DeleteOptions{})
	})

	// Wait for the NetworkCapture to reach Running.
	envInstance.Eventually(t, 60*time.Second, func() (bool, string) {
		obj, err := envInstance.Dyn.Resource(networkCaptureGVR).Namespace(ns).
			Get(ctx, captureID, metav1.GetOptions{})
		if err != nil {
			return false, "get networkcapture: " + err.Error()
		}
		phase, _, _ := unstructured.NestedString(obj.Object, "status", "phase")
		message, _, _ := unstructured.NestedString(obj.Object, "status", "message")
		if phase != "Running" {
			return false, fmt.Sprintf("phase=%s message=%s", phase, message)
		}
		return true, ""
	})

	// Capture the pod UID before deletion so we can detect the restart.
	podPre, err := envInstance.K8s.CoreV1().Pods(ns).Get(ctx, gsName+"-0", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get pod pre-delete: %v", err)
	}
	oldPodUID := podPre.UID

	// Delete the pod.
	if err := envInstance.K8s.CoreV1().Pods(ns).Delete(ctx, gsName+"-0", metav1.DeleteOptions{}); err != nil {
		t.Fatalf("delete pod: %v", err)
	}

	// Wait for the pod to be recreated with a new UID.
	envInstance.Eventually(t, 2*time.Minute, func() (bool, string) {
		pod, err := envInstance.K8s.CoreV1().Pods(ns).Get(ctx, gsName+"-0", metav1.GetOptions{})
		if err != nil {
			return false, "get pod: " + err.Error()
		}
		if pod.UID == oldPodUID {
			return false, "pod still has old UID"
		}
		return true, ""
	})

	// Wait for the NetworkCapture to transition to Failed. The reconciler
	// detects the pod UID mismatch and marks the capture as Failed with reason "PodRestarted".
	envInstance.Eventually(t, 60*time.Second, func() (bool, string) {
		obj, err := envInstance.Dyn.Resource(networkCaptureGVR).Namespace(ns).
			Get(ctx, captureID, metav1.GetOptions{})
		if err != nil {
			return false, "get networkcapture: " + err.Error()
		}
		phase, _, _ := unstructured.NestedString(obj.Object, "status", "phase")
		if phase != "Failed" {
			return false, fmt.Sprintf("phase=%s (want Failed)", phase)
		}
		// Verify there is a condition with reason "PodRestarted" recorded.
		conditions, _, _ := unstructured.NestedSlice(obj.Object, "status", "conditions")
		for _, c := range conditions {
			cond, ok := c.(map[string]any)
			if !ok {
				continue
			}
			if condType, ok := cond["type"].(string); ok && condType == "Failed" {
				if reason, ok := cond["reason"].(string); ok && reason == "PodRestarted" {
					return true, ""
				}
				return false, fmt.Sprintf("Failed condition has reason=%v message=%v (want reason PodRestarted)", cond["reason"], cond["message"])
			}
		}
		return false, fmt.Sprintf("no Failed condition found in conditions=%v", conditions)
	})

	// Verify status.capture.activeCapture is cleared on the GameServer.
	envInstance.Eventually(t, 30*time.Second, func() (bool, string) {
		obj, err := envInstance.Dyn.Resource(gameServerGVR).Namespace(ns).
			Get(ctx, gsName, metav1.GetOptions{})
		if err != nil {
			return false, "get gameserver: " + err.Error()
		}
		activeCapture, found, _ := unstructured.NestedString(obj.Object, "status", "capture", "activeCapture")
		if found && activeCapture != "" {
			return false, "status.capture.activeCapture still set to " + activeCapture
		}
		return true, ""
	})

	// Verify that a new capture can be started, proving no state is leaked
	// and the server is playable again.
	waitPVCBound(t, ns, gsName+"-data", 30*time.Second)
	requireAgentReady(t, ns, gsName)

	// Wait for the sidecar to be ready on the new pod.
	envInstance.Eventually(t, 90*time.Second, func() (bool, string) {
		obj, err := envInstance.Dyn.Resource(gameServerGVR).Namespace(ns).
			Get(ctx, gsName, metav1.GetOptions{})
		if err != nil {
			return false, "get gameserver: " + err.Error()
		}
		ready, _, _ := unstructured.NestedBool(obj.Object, "status", "capture", "ready")
		return ready, ""
	})

	// Start a new capture to prove the lock is released.
	newStartReq := map[string]any{
		"filter":                  fmt.Sprintf("tcp port %d", matchPort),
		"maxDurationSeconds":      60,
		"maxSizeBytes":            1048576,
		"ttlSecondsAfterFinished": 86400,
	}
	newStartHTTPResp, newStartBody, err := cli.Post("/servers/"+gsName+":capture-start", newStartReq)
	if err != nil {
		t.Fatalf("start second capture: %v", err)
	}
	defer func() { _ = newStartHTTPResp.Body.Close() }()
	if newStartHTTPResp.StatusCode != http.StatusAccepted {
		t.Fatalf("start second capture: status=%s body=%s", newStartHTTPResp.Status, newStartBody)
	}

	var newCapture struct {
		CaptureID string `json:"captureId"`
	}
	if err := json.Unmarshal(newStartBody, &newCapture); err != nil {
		t.Fatalf("parse second capture-start response: %v", err)
	}
	if newCapture.CaptureID == "" {
		t.Fatalf("second capture-start had no captureId")
	}

	t.Logf("pod restart handled correctly: first capture Failed after restart, second capture started successfully (captureId=%s)", newCapture.CaptureID)
}

// TestGameServer_NetworkCaptureConcurrencyRejected — attempting a second
// capture while one is already Running must be rejected with 409, and the
// first capture must proceed unaffected. After the first capture completes,
// a third capture must be allowed to start, verifying that the lock
// properly releases.
func TestGameServer_NetworkCaptureConcurrencyRejected(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	ns := "gameplane-games"
	tmpl := "e2e-cap-conc-tmpl"
	gsName := "e2e-cap-conc-gs"

	const matchPort = 12345

	envInstance.BootstrapAdmin(t, adminUsername, adminPassword)
	cli := envInstance.APIClient(t, adminUsername, adminPassword)
	defer cli.Close()

	applyBusyboxTemplate(t, tmpl)
	applyBusyboxGameServerWithCapture(t, ns, gsName, tmpl)
	waitPVCBound(t, ns, gsName+"-data", 90*time.Second)
	requireAgentReady(t, ns, gsName)

	// Wait for the sidecar to be ready.
	envInstance.Eventually(t, 90*time.Second, func() (bool, string) {
		obj, err := envInstance.Dyn.Resource(gameServerGVR).Namespace(ns).
			Get(ctx, gsName, metav1.GetOptions{})
		if err != nil {
			return false, "get gameserver: " + err.Error()
		}
		ready, _, _ := unstructured.NestedBool(obj.Object, "status", "capture", "ready")
		return ready, ""
	})

	// Start the first capture.
	startReq := map[string]any{
		"filter":                  fmt.Sprintf("tcp port %d", matchPort),
		"maxDurationSeconds":      60,
		"maxSizeBytes":            1048576,
		"ttlSecondsAfterFinished": 86400,
	}
	startHTTPResp, startBody, err := cli.Post("/servers/"+gsName+":capture-start", startReq)
	if err != nil {
		t.Fatalf("start first capture: %v", err)
	}
	defer func() { _ = startHTTPResp.Body.Close() }()
	if startHTTPResp.StatusCode != http.StatusAccepted {
		t.Fatalf("start first capture: status=%s body=%s", startHTTPResp.Status, startBody)
	}

	var firstCapture struct {
		CaptureID string `json:"captureId"`
	}
	if err := json.Unmarshal(startBody, &firstCapture); err != nil {
		t.Fatalf("parse first capture-start response: %v", err)
	}
	captureID := firstCapture.CaptureID
	t.Cleanup(func() {
		_ = envInstance.Dyn.Resource(networkCaptureGVR).Namespace(ns).
			Delete(context.Background(), captureID, metav1.DeleteOptions{})
	})

	// Immediately attempt a second capture on the same server.
	// The API handler's fast-path concurrency check should reject this with 409.
	secondStartResp, secondStartBody, err := cli.Post("/servers/"+gsName+":capture-start", startReq)
	if err != nil {
		t.Fatalf("issue second capture-start: %v", err)
	}
	defer func() { _ = secondStartResp.Body.Close() }()

	// The second capture must be rejected with 409 and the exact message.
	if secondStartResp.StatusCode != http.StatusConflict {
		t.Errorf("second capture-start: status=%s (want %s) body=%s",
			secondStartResp.Status, http.StatusText(http.StatusConflict), secondStartBody)
	} else {
		// Verify the message matches the contract.
		if !bytes.Contains(secondStartBody, []byte("capture already in progress")) {
			t.Errorf("second capture-start: body=%s, want message containing 'capture already in progress'", secondStartBody)
		}
	}

	// Wait for the first capture to reach Running to ensure it continues normally.
	envInstance.Eventually(t, 60*time.Second, func() (bool, string) {
		obj, err := envInstance.Dyn.Resource(networkCaptureGVR).Namespace(ns).
			Get(ctx, captureID, metav1.GetOptions{})
		if err != nil {
			return false, "get networkcapture: " + err.Error()
		}
		phase, _, _ := unstructured.NestedString(obj.Object, "status", "phase")
		message, _, _ := unstructured.NestedString(obj.Object, "status", "message")
		if phase != "Running" {
			return false, fmt.Sprintf("phase=%s message=%s", phase, message)
		}
		return true, ""
	})

	// Stop the first capture.
	stopHTTPResp, stopBody, err := cli.Post("/servers/"+gsName+":capture-stop", map[string]any{
		"captureId": captureID,
	})
	if err != nil {
		t.Fatalf("stop first capture: %v", err)
	}
	defer func() { _ = stopHTTPResp.Body.Close() }()
	if stopHTTPResp.StatusCode != http.StatusOK {
		t.Fatalf("stop first capture: status=%s body=%s", stopHTTPResp.Status, stopBody)
	}

	// Wait for the first capture to complete.
	//
	// NOTE: the API's StopNetworkCapture only sets the
	// gameplane.local/stop-requested annotation (F-259);
	// NetworkCaptureReconciler.completeRequestedStop then stops the
	// sidecar, releases the GameServer's status.capture.activeCapture lock
	// and only then writes phase=Completed. The lock lives on a different
	// object, though, and the API's hasActiveCapture fast-path check
	// consults it through its own read, so wait for the lock itself to
	// clear as well, not just for the terminal phase.
	envInstance.Eventually(t, 60*time.Second, func() (bool, string) {
		obj, err := envInstance.Dyn.Resource(networkCaptureGVR).Namespace(ns).
			Get(ctx, captureID, metav1.GetOptions{})
		if err != nil {
			return false, "get networkcapture: " + err.Error()
		}
		phase, _, _ := unstructured.NestedString(obj.Object, "status", "phase")
		if phase != "Completed" {
			return false, fmt.Sprintf("phase=%s", phase)
		}
		return true, ""
	})
	envInstance.Eventually(t, 60*time.Second, func() (bool, string) {
		obj, err := envInstance.Dyn.Resource(gameServerGVR).Namespace(ns).
			Get(ctx, gsName, metav1.GetOptions{})
		if err != nil {
			return false, "get gameserver: " + err.Error()
		}
		active, _, _ := unstructured.NestedString(obj.Object, "status", "capture", "activeCapture")
		if active == captureID {
			return false, "gameserver still reports activeCapture=" + active
		}
		return true, ""
	})

	// Now that the first capture has completed and the operator has
	// released the GameServer's active-capture lock, starting a new
	// capture must succeed.
	thirdStartReq := map[string]any{
		"filter":                  fmt.Sprintf("tcp port %d", matchPort),
		"maxDurationSeconds":      60,
		"maxSizeBytes":            1048576,
		"ttlSecondsAfterFinished": 86400,
	}
	thirdStartResp, thirdStartBody, err := cli.Post("/servers/"+gsName+":capture-start", thirdStartReq)
	if err != nil {
		t.Fatalf("start third capture: %v", err)
	}
	defer func() { _ = thirdStartResp.Body.Close() }()

	var thirdCapture struct {
		CaptureID string `json:"captureId"`
	}
	if thirdStartResp.StatusCode != http.StatusAccepted {
		// A non-2xx body here is plain text (see httperr), not JSON — report
		// the status and body as-is rather than attempting to unmarshal it,
		// which would just produce a confusing "invalid character" parse
		// error that hides the real (status) failure.
		t.Errorf("start third capture: status=%s (want %s) body=%s",
			thirdStartResp.Status, http.StatusText(http.StatusAccepted), thirdStartBody)
	} else if err := json.Unmarshal(thirdStartBody, &thirdCapture); err != nil {
		t.Errorf("parse third capture-start response: %v (body=%s)", err, thirdStartBody)
	} else if thirdCapture.CaptureID == "" {
		t.Errorf("third capture-start had no captureId")
	}

	t.Logf("concurrency correctly enforced: second concurrent start rejected 409, first capture completed normally, third capture allowed (captureId=%s)",
		thirdCapture.CaptureID)
}

// TestGameServer_InstallTimeDefaultApplies: when a GameServer has no
// explicit spec.storage.storageClassName, and the template has none,
// the operator must apply the install-time default (--game-data-storage-class
// from the Helm value operator.gameDataStorage.storageClassName). This test
// verifies SC-001: the PVC spec.storageClassName reflects the install-time
// default, and the PVC reaches Bound.
//
// This test runs in the e2e environment where the Helm install sets
// operator.gameDataStorage.storageClassName=gameplane-e2e-install-default,
// so the operator has an install-time default to apply. It verifies that
// the operator uses that default, proving the install-time rung of the
// precedence chain (GameServer > GameTemplate > install-time > cluster default).
func TestGameServer_InstallTimeDefaultApplies(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	ns := "gameplane-games"
	tmpl := "e2e-install-default-tmpl"
	gs := "e2e-install-default-gs"

	// Use a template without explicit storage class.
	// The Helm install configures operator.gameDataStorage.storageClassName
	// to gameplane-e2e-install-default, so the operator will apply that.
	// Expected result: PVC has storageClassName=gameplane-e2e-install-default.
	applyBusyboxTemplate(t, tmpl)
	applyBusyboxGameServer(t, ns, gs, tmpl)

	// Wait for the PVC to be created.
	envInstance.Eventually(t, 60*time.Second, func() (bool, string) {
		_, err := envInstance.K8s.CoreV1().PersistentVolumeClaims(ns).
			Get(ctx, gs+"-data", metav1.GetOptions{})
		if err != nil {
			return false, "get pvc: " + err.Error()
		}
		return true, ""
	})

	pvc, err := envInstance.K8s.CoreV1().PersistentVolumeClaims(ns).
		Get(ctx, gs+"-data", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get pvc: %v", err)
	}

	// Verify the PVC has the install-time default storage class.
	const installTimeDefault = "gameplane-e2e-install-default"
	if pvc.Spec.StorageClassName == nil {
		t.Errorf("PVC storageClassName is nil, want %q (install-time default)",
			installTimeDefault)
	} else if *pvc.Spec.StorageClassName != installTimeDefault {
		t.Errorf("PVC storageClassName=%q, want %q (install-time default)",
			*pvc.Spec.StorageClassName, installTimeDefault)
	}

	// Wait for the PVC to reach Bound, proving the install-time default was
	// acceptable and the precedence chain worked correctly end-to-end (SC-001).
	waitPVCBound(t, ns, gs+"-data", 90*time.Second)

	// Re-fetch to verify it remained Bound after the wait.
	pvc, err = envInstance.K8s.CoreV1().PersistentVolumeClaims(ns).
		Get(ctx, gs+"-data", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get pvc post-bind: %v", err)
	}

	if pvc.Status.Phase != corev1.ClaimBound {
		t.Errorf("PVC phase=%s, want Bound", pvc.Status.Phase)
	}
}

// TestGameServer_TemplateDefaultOverridesInstallTime: a GameTemplate with an
// explicit spec.storage.storageClassName must override the operator's
// install-time default. This verifies the precedence rung: GameTemplate default
// > install-time default.
//
// The e2e cluster has an install-time default configured
// (gameplane-e2e-install-default), but this GameServer's template specifies
// "standard" (kind's default StorageClass), which must win.
func TestGameServer_TemplateDefaultOverridesInstallTime(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	ns := "gameplane-games"
	tmpl := "e2e-template-storage-override-tmpl"
	gs := "e2e-template-storage-override-gs"

	// Create a template with an explicit storage class override.
	// Even though the operator has install-time default=gameplane-e2e-install-default,
	// this template specifies "standard", which must take precedence.
	tmplSpec := map[string]any{
		"displayName": "E2E busybox (" + tmpl + ")",
		"game":        "busybox",
		"version":     "1",
		"image":       "busybox:1.36",
		"command":     []any{"sh", "-c", "sleep 100000"},
		"ports": []any{
			map[string]any{"name": "noop", "containerPort": int64(12345), "advertise": true, "protocol": "TCP"},
		},
		// Template-level storage class override
		"storage": map[string]any{
			"storageClassName": "standard",
		},
	}
	tmplObj := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "gameplane.local/v1alpha1",
		"kind":       "GameTemplate",
		"metadata":   map[string]any{"name": tmpl},
		"spec":       tmplSpec,
	}}
	if _, err := envInstance.Dyn.Resource(gameTemplateGVR).
		Create(ctx, tmplObj, metav1.CreateOptions{}); err != nil && !apierrors.IsAlreadyExists(err) {
		t.Fatalf("create template: %v", err)
	}
	t.Cleanup(func() {
		_ = envInstance.Dyn.Resource(gameTemplateGVR).
			Delete(context.Background(), tmpl, metav1.DeleteOptions{})
	})

	// Create a GameServer pointing at this template, with no server-level override.
	applyBusyboxGameServer(t, ns, gs, tmpl)

	// Wait for the PVC to be created.
	envInstance.Eventually(t, 60*time.Second, func() (bool, string) {
		_, err := envInstance.K8s.CoreV1().PersistentVolumeClaims(ns).
			Get(ctx, gs+"-data", metav1.GetOptions{})
		if err != nil {
			return false, "get pvc: " + err.Error()
		}
		return true, ""
	})

	pvc, err := envInstance.K8s.CoreV1().PersistentVolumeClaims(ns).
		Get(ctx, gs+"-data", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get pvc: %v", err)
	}

	// Verify the PVC has the template's storage class (not the install-time default).
	if pvc.Spec.StorageClassName == nil {
		t.Errorf("PVC storageClassName is nil, want 'standard' (from template)")
	} else if *pvc.Spec.StorageClassName != "standard" {
		t.Errorf("PVC storageClassName=%q, want 'standard' (template overrides install-time default)",
			*pvc.Spec.StorageClassName)
	}

	// Wait for the PVC to bind successfully (proving the template override worked).
	waitPVCBound(t, ns, gs+"-data", 90*time.Second)
}

// TestGameServer_NonexistentStorageClassSurfacesError: a GameServer that
// references a storage class that does not exist on the cluster must have
// its status.conditions[Ready] set to False with reason PVCProvisioningFailed,
// surfacing the error to the dashboard without crashing the operator.
func TestGameServer_NonexistentStorageClassSurfacesError(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	ns := "gameplane-games"
	tmpl := "e2e-storage-error-tmpl"
	gs := "e2e-storage-error-gs"

	applyBusyboxTemplate(t, tmpl)

	// Create a GameServer with an explicit but non-existent storage class.
	// This should leave the server Pending with a clear error condition.
	gsName := gs
	gsSpec := map[string]any{
		"templateRef": map[string]any{"name": tmpl},
		"storage": map[string]any{
			"storageClassName": "nonexistent-storage-class-that-will-never-exist-e2e-12345",
		},
	}
	gsObj := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "gameplane.local/v1alpha1",
		"kind":       "GameServer",
		"metadata":   map[string]any{"name": gsName, "namespace": ns},
		"spec":       gsSpec,
	}}
	if _, err := envInstance.Dyn.Resource(gameServerGVR).Namespace(ns).
		Create(ctx, gsObj, metav1.CreateOptions{}); err != nil && !apierrors.IsAlreadyExists(err) {
		t.Fatalf("create gameserver: %v", err)
	}
	t.Cleanup(func() {
		_ = envInstance.Dyn.Resource(gameServerGVR).Namespace(ns).
			Delete(context.Background(), gsName, metav1.DeleteOptions{})
	})

	// Wait for the operator to detect the provisioning failure and surface
	// it in the GameServer's status conditions. The check occurs on every
	// reconciliation (~30s default), so we allow up to 2 minutes.
	// The phase must stay Pending (not escalated to Failed) since a missing
	// StorageClass is recoverable.
	envInstance.Eventually(t, 2*time.Minute, func() (bool, string) {
		obj, err := envInstance.Dyn.Resource(gameServerGVR).Namespace(ns).
			Get(ctx, gsName, metav1.GetOptions{})
		if err != nil {
			return false, "get gameserver: " + err.Error()
		}

		// Check that phase is non-terminal (Pending or Starting, not Failed).
		// A PVC provisioning failure is recoverable and must not escalate to Failed.
		phase, _, _ := unstructured.NestedString(obj.Object, "status", "phase")
		if phase == "Failed" {
			return false, fmt.Sprintf("phase=%s, want a non-terminal phase (Pending or Starting); a PVC provisioning failure is recoverable and must not escalate to Failed", phase)
		}
		if phase != "Pending" && phase != "Starting" {
			return false, fmt.Sprintf("phase=%s, want Pending or Starting; a PVC provisioning failure is recoverable and must not escalate to Failed", phase)
		}

		// Look for a Ready=False condition with reason PVCProvisioningFailed.
		conditions, _, _ := unstructured.NestedSlice(obj.Object, "status", "conditions")
		for _, cIface := range conditions {
			c, ok := cIface.(map[string]any)
			if !ok {
				continue
			}
			if c["type"] == "Ready" && c["status"] == "False" {
				reason, _ := c["reason"].(string)
				message, _ := c["message"].(string)
				if reason == "PVCProvisioningFailed" {
					// Found the error condition.
					return true, ""
				}
				return false, fmt.Sprintf("Ready=False but reason=%s (want PVCProvisioningFailed), message=%s",
					reason, message)
			}
		}
		return false, "no Ready=False condition yet"
	})

	// Verify the condition reason and message.
	obj, err := envInstance.Dyn.Resource(gameServerGVR).Namespace(ns).
		Get(ctx, gsName, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get gameserver: %v", err)
	}

	conditions, _, _ := unstructured.NestedSlice(obj.Object, "status", "conditions")
	var readyCondition map[string]any
	for _, cIface := range conditions {
		c, ok := cIface.(map[string]any)
		if !ok {
			continue
		}
		if c["type"] == "Ready" {
			readyCondition = c
			break
		}
	}

	if readyCondition == nil {
		t.Fatal("Ready condition not found on GameServer")
	}
	if readyCondition["status"] != "False" {
		t.Errorf("Ready status=%s, want False", readyCondition["status"])
	}
	if readyCondition["reason"] != "PVCProvisioningFailed" {
		t.Errorf("Ready reason=%s, want PVCProvisioningFailed", readyCondition["reason"])
	}
	// The message should mention the missing storage class.
	msg := fmt.Sprintf("%v", readyCondition["message"])
	if !strings.Contains(msg, "nonexistent-storage-class-that-will-never-exist-e2e-12345") &&
		!strings.Contains(msg, "StorageClass") {
		t.Errorf("Ready message=%s, want mention of missing StorageClass", msg)
	}
}

// TestGameServer_TunnelCredentialRefusalSurfaced: a tunnel credentials Secret
// created without an ownerReference to the GameServer (the kubectl path the
// old docs described) must be refused by the operator, and that refusal must
// be visible as a GameServer status condition — not only in operator logs
// (F-069). No external relay is involved; the tunnel pod is never expected
// to run.
func TestGameServer_TunnelCredentialRefusalSurfaced(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	ns := "gameplane-games"
	// Unique per run: a leftover owned Secret or GameServer from an earlier
	// run must not let this test pass without exercising the refusal path.
	suffix := time.Now().UnixNano()
	tmpl := fmt.Sprintf("e2e-tunnel-cred-refused-tmpl-%d", suffix)
	gsName := fmt.Sprintf("e2e-tunnel-cred-refused-gs-%d", suffix)
	secName := fmt.Sprintf("e2e-tunnel-cred-refused-secret-%d", suffix)

	applyBusyboxTemplate(t, tmpl)

	// Secret with no ownerReference — the refused path (docs used to
	// recommend exactly this via `kubectl create secret generic`).
	sec := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: secName, Namespace: ns},
		StringData: map[string]string{"token": "e2e-fake-token"},
	}
	if _, err := envInstance.K8s.CoreV1().Secrets(ns).Create(ctx, sec, metav1.CreateOptions{}); err != nil {
		t.Fatalf("create secret: %v", err)
	}
	t.Cleanup(func() {
		_ = envInstance.K8s.CoreV1().Secrets(ns).Delete(context.Background(), secName, metav1.DeleteOptions{})
	})

	gsSpec := map[string]any{
		"templateRef": map[string]any{"name": tmpl},
		"networking": map[string]any{
			"tunnel": map[string]any{
				"enabled":              true,
				"provider":             "frp",
				"credentialsSecretRef": map[string]any{"name": secName},
				"frp": map[string]any{
					"serverAddr": "relay.example.invalid",
					"remotePorts": []any{
						map[string]any{"name": "noop", "remotePort": int64(25565)},
					},
				},
			},
		},
	}
	gsObj := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "gameplane.local/v1alpha1",
		"kind":       "GameServer",
		"metadata":   map[string]any{"name": gsName, "namespace": ns},
		"spec":       gsSpec,
	}}
	if _, err := envInstance.Dyn.Resource(gameServerGVR).Namespace(ns).
		Create(ctx, gsObj, metav1.CreateOptions{}); err != nil {
		t.Fatalf("create gameserver: %v", err)
	}
	t.Cleanup(func() {
		_ = envInstance.Dyn.Resource(gameServerGVR).Namespace(ns).
			Delete(context.Background(), gsName, metav1.DeleteOptions{})
	})

	envInstance.Eventually(t, 2*time.Minute, func() (bool, string) {
		obj, err := envInstance.Dyn.Resource(gameServerGVR).Namespace(ns).
			Get(ctx, gsName, metav1.GetOptions{})
		if err != nil {
			return false, "get gameserver: " + err.Error()
		}
		conditions, _, _ := unstructured.NestedSlice(obj.Object, "status", "conditions")
		for _, cIface := range conditions {
			c, ok := cIface.(map[string]any)
			if !ok {
				continue
			}
			if c["type"] == "TunnelReady" && c["status"] == "False" {
				reason, _ := c["reason"].(string)
				message, _ := c["message"].(string)
				if reason == "TunnelCredentialRefused" {
					if !strings.Contains(message, secName) {
						return false, fmt.Sprintf("TunnelReady message does not name the secret: %s", message)
					}
					return true, ""
				}
				return false, fmt.Sprintf("TunnelReady=False but reason=%s (want TunnelCredentialRefused)", reason)
			}
		}
		return false, "no TunnelReady=False condition yet"
	})
}

// TestGameServer_ExplicitStorageClassOverridesDefault: a GameServer with an
// explicit spec.storage.storageClassName must use that class, taking
// precedence over the template default and the operator's install-time default.
func TestGameServer_ExplicitStorageClassOverridesDefault(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	ns := "gameplane-games"
	tmpl := "e2e-storage-override-tmpl"
	gs := "e2e-storage-override-gs"

	// Create a template without explicit storage class (so it would normally
	// fall through to the install-time default).
	applyBusyboxTemplate(t, tmpl)

	// Create a GameServer with an explicit storage class. In kind clusters,
	// the default storage class is typically "standard". We'll use that as
	// our explicit override to ensure it can be provisioned successfully.
	gsName := gs
	gsSpec := map[string]any{
		"templateRef": map[string]any{"name": tmpl},
		// Explicit storage class at the GameServer level.
		// Kind clusters typically have "standard" as the default, so this
		// should be the same as (or compatible with) the cluster default.
		"storage": map[string]any{
			"storageClassName": "standard",
		},
	}
	gsObj := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "gameplane.local/v1alpha1",
		"kind":       "GameServer",
		"metadata":   map[string]any{"name": gsName, "namespace": ns},
		"spec":       gsSpec,
	}}
	if _, err := envInstance.Dyn.Resource(gameServerGVR).Namespace(ns).
		Create(ctx, gsObj, metav1.CreateOptions{}); err != nil && !apierrors.IsAlreadyExists(err) {
		t.Fatalf("create gameserver: %v", err)
	}
	t.Cleanup(func() {
		_ = envInstance.Dyn.Resource(gameServerGVR).Namespace(ns).
			Delete(context.Background(), gsName, metav1.DeleteOptions{})
	})

	// Wait for the PVC to be created.
	envInstance.Eventually(t, 60*time.Second, func() (bool, string) {
		_, err := envInstance.K8s.CoreV1().PersistentVolumeClaims(ns).
			Get(ctx, gs+"-data", metav1.GetOptions{})
		if err != nil {
			return false, "get pvc: " + err.Error()
		}
		return true, ""
	})

	pvc, err := envInstance.K8s.CoreV1().PersistentVolumeClaims(ns).
		Get(ctx, gs+"-data", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get pvc: %v", err)
	}

	// Verify the PVC has the explicit storage class.
	if pvc.Spec.StorageClassName == nil {
		t.Errorf("PVC storageClassName is nil, want 'standard'")
	} else if *pvc.Spec.StorageClassName != "standard" {
		t.Errorf("PVC storageClassName=%s, want 'standard'", *pvc.Spec.StorageClassName)
	}

	// Wait for the PVC to bind successfully (proving the storage class
	// override worked).
	waitPVCBound(t, ns, gs+"-data", 90*time.Second)
}
