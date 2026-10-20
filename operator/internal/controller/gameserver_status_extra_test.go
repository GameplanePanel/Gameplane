package controller

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	gameplanev1alpha1 "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
)

func TestComputeConditions(t *testing.T) {
	cases := []struct {
		phase  gameplanev1alpha1.GameServerPhase
		ready  metav1.ConditionStatus
		health metav1.ConditionStatus
	}{
		{gameplanev1alpha1.GameServerPhaseRunning, metav1.ConditionTrue, metav1.ConditionTrue},
		{gameplanev1alpha1.GameServerPhaseStarting, metav1.ConditionFalse, metav1.ConditionFalse},
		{gameplanev1alpha1.GameServerPhaseStopping, metav1.ConditionFalse, metav1.ConditionFalse},
		{gameplanev1alpha1.GameServerPhaseSuspended, metav1.ConditionFalse, metav1.ConditionFalse},
		{gameplanev1alpha1.GameServerPhaseFailed, metav1.ConditionFalse, metav1.ConditionFalse},
		{gameplanev1alpha1.GameServerPhase("unknown"), metav1.ConditionUnknown, metav1.ConditionUnknown},
	}
	for _, tc := range cases {
		t.Run(string(tc.phase), func(t *testing.T) {
			gs := &gameplanev1alpha1.GameServer{}
			conds := computeConditions(gs, tc.phase, nil, idleAwake, addressPlan{}, nil, addressFailureReason{}, "")
			byType := map[string]metav1.Condition{}
			for _, c := range conds {
				byType[c.Type] = c
			}
			if byType["Ready"].Status != tc.ready {
				t.Errorf("Ready=%v want %v", byType["Ready"].Status, tc.ready)
			}
			if byType["Healthy"].Status != tc.health {
				t.Errorf("Healthy=%v want %v", byType["Healthy"].Status, tc.health)
			}
		})
	}
}

func TestComputeConditions_ProvisioningRefinement(t *testing.T) {
	gs := &gameplanev1alpha1.GameServer{}
	prov := &provisioningInfo{reason: "PullingImage", message: "pulling the game image"}
	conds := computeConditions(gs, gameplanev1alpha1.GameServerPhaseStarting, prov, idleAwake, addressPlan{}, nil, addressFailureReason{}, "")

	var prog metav1.Condition
	for _, c := range conds {
		if c.Type == "Progressing" {
			prog = c
		}
	}
	if prog.Reason != "PullingImage" {
		t.Errorf("Progressing.Reason = %q, want PullingImage", prog.Reason)
	}
	if prog.Message != "pulling the game image" {
		t.Errorf("Progressing.Message = %q, want the provisioning message", prog.Message)
	}

	// nil prov leaves the generic Starting reason and no message.
	conds = computeConditions(gs, gameplanev1alpha1.GameServerPhaseStarting, nil, idleAwake, addressPlan{}, nil, addressFailureReason{}, "")
	for _, c := range conds {
		if c.Type == "Progressing" && (c.Reason != "Starting" || c.Message != "") {
			t.Errorf("nil prov: Progressing = %+v, want generic Starting/no message", c)
		}
	}
}

func TestProvisioningReason(t *testing.T) {
	waiting := func(reason string) corev1.ContainerState {
		return corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: reason}}
	}
	gameStatus := func(state corev1.ContainerState) corev1.ContainerStatus {
		return corev1.ContainerStatus{Name: "game", State: state}
	}
	ready := func(v corev1.ConditionStatus) []corev1.PodCondition {
		return []corev1.PodCondition{{Type: corev1.PodReady, Status: v}}
	}

	cases := []struct {
		name    string
		pod     corev1.Pod
		hbFresh bool
		reason  string
	}{
		{
			name:   "image pull backoff",
			pod:    corev1.Pod{Status: corev1.PodStatus{ContainerStatuses: []corev1.ContainerStatus{gameStatus(waiting("ImagePullBackOff"))}}},
			reason: "PullingImage",
		},
		{
			name:   "container creating",
			pod:    corev1.Pod{Status: corev1.PodStatus{ContainerStatuses: []corev1.ContainerStatus{gameStatus(waiting("ContainerCreating"))}}},
			reason: "ContainerCreating",
		},
		{
			name: "init container pulling",
			pod: corev1.Pod{Status: corev1.PodStatus{
				InitContainerStatuses: []corev1.ContainerStatus{{Name: "config-init", State: waiting("PodInitializing")}},
				ContainerStatuses:     []corev1.ContainerStatus{gameStatus(waiting("PodInitializing"))},
			}},
			reason: "ContainerCreating",
		},
		{
			name: "running but not ready installs files",
			pod: corev1.Pod{Status: corev1.PodStatus{
				ContainerStatuses: []corev1.ContainerStatus{gameStatus(corev1.ContainerState{Running: &corev1.ContainerStateRunning{}})},
				Conditions:        ready(corev1.ConditionFalse),
			}},
			reason: "InstallingServerFiles",
		},
		{
			name: "ready but stale agent waits for heartbeat",
			pod: corev1.Pod{Status: corev1.PodStatus{
				ContainerStatuses: []corev1.ContainerStatus{gameStatus(corev1.ContainerState{Running: &corev1.ContainerStateRunning{}})},
				Conditions:        ready(corev1.ConditionTrue),
			}},
			hbFresh: false,
			reason:  "WaitingForAgent",
		},
		{
			name:   "crash loop",
			pod:    corev1.Pod{Status: corev1.PodStatus{ContainerStatuses: []corev1.ContainerStatus{gameStatus(waiting("CrashLoopBackOff"))}}},
			reason: "CrashLoopBackOff",
		},
		{
			name:   "terminated during startup",
			pod:    corev1.Pod{Status: corev1.PodStatus{ContainerStatuses: []corev1.ContainerStatus{gameStatus(corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{}})}}},
			reason: "ContainerExited",
		},
		{
			name:   "no container status yet",
			pod:    corev1.Pod{Status: corev1.PodStatus{}},
			reason: "ContainerCreating",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reason, message := provisioningReason(&tc.pod, tc.hbFresh)
			if reason != tc.reason {
				t.Errorf("reason = %q, want %q", reason, tc.reason)
			}
			if message == "" {
				t.Error("message should not be empty")
			}
		})
	}
}

func TestStartupFailure(t *testing.T) {
	waiting := func(reason string) corev1.ContainerState {
		return corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: reason}}
	}
	terminated := func(code int32) corev1.ContainerState {
		return corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{ExitCode: code}}
	}
	game := func(state corev1.ContainerState, restarts int32) corev1.ContainerStatus {
		return corev1.ContainerStatus{Name: "game", State: state, RestartCount: restarts}
	}
	pod := func(cs ...corev1.ContainerStatus) corev1.Pod {
		return corev1.Pod{Status: corev1.PodStatus{ContainerStatuses: cs}}
	}

	cases := []struct {
		name       string
		pod        corev1.Pod
		wantFailed bool
		wantReason string
	}{
		{"image pull backoff fails", pod(game(waiting("ImagePullBackOff"), 0)), true, "ImagePullFailed"},
		{"transient ErrImagePull keeps starting", pod(game(waiting("ErrImagePull"), 0)), false, ""},
		{"crash loop at threshold fails", pod(game(waiting("CrashLoopBackOff"), 3)), true, "CrashLoopBackOff"},
		{"crash loop below threshold keeps starting", pod(game(waiting("CrashLoopBackOff"), 2)), false, ""},
		{"terminated non-zero fails", pod(game(terminated(1), 0)), true, "ContainerExited"},
		{"terminated zero keeps starting", pod(game(terminated(0), 0)), false, ""},
		{"creating keeps starting", pod(game(waiting("ContainerCreating"), 0)), false, ""},
		{"running keeps starting", pod(game(corev1.ContainerState{Running: &corev1.ContainerStateRunning{}}, 0)), false, ""},
		{"no game container keeps starting", corev1.Pod{Status: corev1.PodStatus{}}, false, ""},
		{
			name: "init image pull backoff fails before game container",
			pod: corev1.Pod{Status: corev1.PodStatus{
				InitContainerStatuses: []corev1.ContainerStatus{{Name: "config-init", State: waiting("ImagePullBackOff")}},
				ContainerStatuses:     []corev1.ContainerStatus{game(waiting("PodInitializing"), 0)},
			}},
			wantFailed: true, wantReason: "ImagePullFailed",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reason, message, failed := startupFailure(&tc.pod)
			if failed != tc.wantFailed {
				t.Fatalf("failed = %v, want %v (reason %q)", failed, tc.wantFailed, reason)
			}
			if failed {
				if reason != tc.wantReason {
					t.Errorf("reason = %q, want %q", reason, tc.wantReason)
				}
				if message == "" {
					t.Error("failure message should not be empty")
				}
			}
		})
	}
}

func TestComputeConditions_FailedCarriesReason(t *testing.T) {
	gs := &gameplanev1alpha1.GameServer{}
	prov := &provisioningInfo{
		reason:  "CrashLoopBackOff",
		message: "the container has crash-looped 3 times during startup; check the logs",
	}
	byType := func(conds []metav1.Condition) map[string]metav1.Condition {
		m := map[string]metav1.Condition{}
		for _, c := range conds {
			m[c.Type] = c
		}
		return m
	}

	m := byType(computeConditions(gs, gameplanev1alpha1.GameServerPhaseFailed, prov, idleAwake, addressPlan{}, nil, addressFailureReason{}, ""))
	if m["Ready"].Reason != "CrashLoopBackOff" || m["Ready"].Message != prov.message {
		t.Errorf("Ready = %+v, want CrashLoopBackOff reason + the failure message", m["Ready"])
	}
	if m["Progressing"].Reason != "CrashLoopBackOff" {
		t.Errorf("Progressing.Reason = %q, want CrashLoopBackOff", m["Progressing"].Reason)
	}

	// nil prov leaves the generic Failed reason and no message.
	m = byType(computeConditions(gs, gameplanev1alpha1.GameServerPhaseFailed, nil, idleAwake, addressPlan{}, nil, addressFailureReason{}, ""))
	if m["Ready"].Reason != "Failed" || m["Ready"].Message != "" {
		t.Errorf("nil prov: Ready = %+v, want generic Failed/no message", m["Ready"])
	}
}

func TestEndpointsFromService_ClusterIP(t *testing.T) {
	svc := &corev1.Service{
		Spec: corev1.ServiceSpec{
			Type:      corev1.ServiceTypeClusterIP,
			ClusterIP: "10.0.0.5",
			Ports: []corev1.ServicePort{
				{Name: "game", Port: 25565, Protocol: corev1.ProtocolTCP},
				{Name: "rcon", Port: 25575, Protocol: corev1.ProtocolTCP},
			},
		},
	}
	got := endpointsFromService(svc, addressPlan{})
	if len(got) != 2 || got[0].Host != "10.0.0.5" || got[0].Port != 25565 {
		t.Fatalf("got %+v", got)
	}
}

func TestEndpointsFromService_NodePort(t *testing.T) {
	svc := &corev1.Service{
		Spec: corev1.ServiceSpec{
			Type: corev1.ServiceTypeNodePort,
			Ports: []corev1.ServicePort{
				{Name: "game", Port: 25565, NodePort: 30001, Protocol: corev1.ProtocolTCP},
			},
		},
	}
	got := endpointsFromService(svc, addressPlan{})
	if got[0].Port != 30001 {
		t.Fatalf("expected NodePort %d, got %d", 30001, got[0].Port)
	}
}

func TestEndpointsFromService_LoadBalancer(t *testing.T) {
	svc := &corev1.Service{
		Spec: corev1.ServiceSpec{
			Type: corev1.ServiceTypeLoadBalancer,
			Ports: []corev1.ServicePort{
				{Name: "game", Port: 25565, Protocol: corev1.ProtocolTCP},
			},
		},
		Status: corev1.ServiceStatus{
			LoadBalancer: corev1.LoadBalancerStatus{
				Ingress: []corev1.LoadBalancerIngress{
					{IP: "203.0.113.1"},
				},
			},
		},
	}
	got := endpointsFromService(svc, addressPlan{})
	if got[0].Host != "203.0.113.1" {
		t.Fatalf("got host=%q", got[0].Host)
	}
}

func TestEndpointsFromService_LoadBalancerHostname(t *testing.T) {
	svc := &corev1.Service{
		Spec: corev1.ServiceSpec{
			Type:  corev1.ServiceTypeLoadBalancer,
			Ports: []corev1.ServicePort{{Name: "g", Port: 1}},
		},
		Status: corev1.ServiceStatus{
			LoadBalancer: corev1.LoadBalancerStatus{
				Ingress: []corev1.LoadBalancerIngress{{Hostname: "elb.example.com"}},
			},
		},
	}
	got := endpointsFromService(svc, addressPlan{})
	if got[0].Host != "elb.example.com" {
		t.Fatalf("got %q", got[0].Host)
	}
}
