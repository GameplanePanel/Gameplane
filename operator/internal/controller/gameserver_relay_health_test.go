package controller

import (
	"reflect"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	gameplanev1alpha1 "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
)

func TestRelayDeploymentIdentityProbesAndResourceRemoval(t *testing.T) {
	t.Parallel()
	s := wipeScheme(t)
	for _, identity := range []struct{ namespace, uid string }{{"one", "uid-one"}, {"two", "uid-two"}, {"one", "replacement-uid"}} {
		gs := &gameplanev1alpha1.GameServer{ObjectMeta: metav1.ObjectMeta{Name: "same", Namespace: identity.namespace, UID: types.UID(identity.uid)}, Spec: gameplanev1alpha1.GameServerSpec{Networking: gameplanev1alpha1.GameServerNetworking{Tunnel: &gameplanev1alpha1.GameServerTunnel{Enabled: true, Provider: "frp", Frp: &gameplanev1alpha1.FrpTunnelSpec{ServerAddr: "relay.example"}}}}}
		c := fake.NewClientBuilder().WithScheme(s).WithObjects(gs).Build()
		r := &GameServerReconciler{Client: c, Scheme: s}
		for _, resources := range []*corev1.ResourceRequirements{nil, {Requests: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("90m")}, Limits: corev1.ResourceList{corev1.ResourceMemory: resource.MustParse("128Mi")}}, {}, nil} {
			r.TunnelResources = resources
			if err := r.reconcileTunnel(t.Context(), gs, singlePortTemplate(true, "minecraft"), true); err != nil {
				t.Fatal(err)
			}
			var dep appsv1.Deployment
			if err := c.Get(t.Context(), client.ObjectKey{Namespace: gs.Namespace, Name: gs.Name + "-tunnel"}, &dep); err != nil {
				t.Fatal(err)
			}
			if dep.Spec.Strategy.Type != appsv1.RecreateDeploymentStrategyType || dep.Spec.Strategy.RollingUpdate != nil {
				t.Fatal("relay permits overlapping identities")
			}
			container := dep.Spec.Template.Spec.Containers[0]
			if container.StartupProbe == nil || container.StartupProbe.HTTPGet == nil || container.StartupProbe.HTTPGet.Path != "/healthz" || !hasRelayReadinessProbe(&dep) || container.LivenessProbe != nil {
				t.Fatal("provider outage must affect readiness without restarting the supervisor")
			}
			if !reflect.DeepEqual(container.Resources, auxiliaryResources(resources, "25m", "64Mi")) {
				t.Fatal("resource defaults or override removal changed")
			}
			found := false
			for _, env := range container.Env {
				if env.Name == "GAMESERVER_UID" {
					found = env.Value == identity.uid
				}
			}
			if !found {
				t.Fatal("UID identity missing or unstable")
			}
		}
	}
}

func TestRelayConditionsRequireCurrentProviderProbe(t *testing.T) {
	t.Parallel()
	s := wipeScheme(t)
	gs := &gameplanev1alpha1.GameServer{ObjectMeta: metav1.ObjectMeta{Name: "relay", Namespace: "games", UID: "uid"}, Spec: gameplanev1alpha1.GameServerSpec{Networking: gameplanev1alpha1.GameServerNetworking{Tunnel: &gameplanev1alpha1.GameServerTunnel{Provider: "frp", Frp: &gameplanev1alpha1.FrpTunnelSpec{ServerAddr: "relay"}}}}}
	c := fake.NewClientBuilder().WithScheme(s).WithObjects(gs).Build()
	r := &GameServerReconciler{Client: c, Scheme: s}
	if err := r.reconcileTunnel(t.Context(), gs, singlePortTemplate(true, "minecraft"), true); err != nil {
		t.Fatal(err)
	}
	var dep appsv1.Deployment
	if err := c.Get(t.Context(), client.ObjectKey{Namespace: gs.Namespace, Name: "relay-tunnel"}, &dep); err != nil {
		t.Fatal(err)
	}
	dep.Generation = 2
	dep.Status = appsv1.DeploymentStatus{ReadyReplicas: 1, UpdatedReplicas: 1, ObservedGeneration: 2}
	assert := func(want metav1.ConditionStatus) {
		t.Helper()
		cond := meta.FindStatusCondition(computeTunnelConditions(gs, tunnelPlan{wantTunnel: true}, &dep), "TunnelReady")
		if cond == nil || cond.Status != want {
			t.Fatalf("condition=%+v want=%s", cond, want)
		}
	}
	assert(metav1.ConditionTrue)
	dep.Status.ObservedGeneration = 1
	assert(metav1.ConditionUnknown)
	dep.Status.ObservedGeneration = 2
	dep.Status.ReadyReplicas = 0
	assert(metav1.ConditionFalse)
	dep.Status.ReadyReplicas = 1
	dep.Spec.Template.Spec.Containers[0].ReadinessProbe = nil
	assert(metav1.ConditionUnknown)
}

func TestExistingFrpDeploymentGainsUIDDuringReconciliation(t *testing.T) {
	t.Parallel()
	s := wipeScheme(t)
	gs := &gameplanev1alpha1.GameServer{ObjectMeta: metav1.ObjectMeta{Name: "legacy", Namespace: "games", UID: "legacy-uid"}, Spec: gameplanev1alpha1.GameServerSpec{Networking: gameplanev1alpha1.GameServerNetworking{Tunnel: &gameplanev1alpha1.GameServerTunnel{Enabled: true, Provider: "frp", Frp: &gameplanev1alpha1.FrpTunnelSpec{ServerAddr: "relay"}}}}}
	old := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "legacy-tunnel", Namespace: "games", UID: "deployment-uid"}, Spec: appsv1.DeploymentSpec{Strategy: appsv1.DeploymentStrategy{Type: appsv1.RollingUpdateDeploymentStrategyType, RollingUpdate: &appsv1.RollingUpdateDeployment{}}, Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "tunnel", Image: "old", Env: []corev1.EnvVar{{Name: "TUNNEL_TYPE", Value: "frp"}}}}}}}}
	c := fake.NewClientBuilder().WithScheme(s).WithObjects(gs, old).Build()
	r := &GameServerReconciler{Client: c, Scheme: s}
	for i := 0; i < 2; i++ {
		if err := r.reconcileTunnel(t.Context(), gs, singlePortTemplate(true, "minecraft"), true); err != nil {
			t.Fatal(err)
		}
		var updated appsv1.Deployment
		if err := c.Get(t.Context(), client.ObjectKeyFromObject(old), &updated); err != nil {
			t.Fatal(err)
		}
		if updated.UID != old.UID || updated.Spec.Strategy.Type != appsv1.RecreateDeploymentStrategyType || updated.Spec.Strategy.RollingUpdate != nil {
			t.Fatalf("legacy Deployment was not migrated in place: %+v", updated.Spec.Strategy)
		}
		found := false
		for _, env := range updated.Spec.Template.Spec.Containers[0].Env {
			if env.Name == "GAMESERVER_UID" {
				found = env.Value == string(gs.UID)
			}
		}
		if !found || !hasRelayReadinessProbe(&updated) {
			t.Fatal("existing Deployment lacks UID or forwarding probe")
		}
	}
}

func TestPlayitPodReadinessUsesAvailableProcessSignal(t *testing.T) {
	t.Parallel()
	s := wipeScheme(t)
	gs := &gameplanev1alpha1.GameServer{ObjectMeta: metav1.ObjectMeta{Name: "playit", Namespace: "games", UID: "playit-uid"}, Spec: gameplanev1alpha1.GameServerSpec{Networking: gameplanev1alpha1.GameServerNetworking{Tunnel: &gameplanev1alpha1.GameServerTunnel{Enabled: true, Provider: "playit", Playit: &gameplanev1alpha1.PlayitTunnelSpec{}}}}}
	c := fake.NewClientBuilder().WithScheme(s).WithObjects(gs).Build()
	r := &GameServerReconciler{Client: c, Scheme: s}
	if err := r.reconcileTunnel(t.Context(), gs, singlePortTemplate(true, "minecraft"), true); err != nil {
		t.Fatal(err)
	}
	var dep appsv1.Deployment
	if err := c.Get(t.Context(), client.ObjectKey{Namespace: gs.Namespace, Name: gs.Name + "-tunnel"}, &dep); err != nil {
		t.Fatal(err)
	}
	probe := dep.Spec.Template.Spec.Containers[0].ReadinessProbe
	if probe == nil || probe.HTTPGet == nil || probe.HTTPGet.Path != "/healthz" || probe.HTTPGet.Port.IntValue() != 8091 {
		t.Fatalf("Playit requires unsupported connection evidence: %+v", probe)
	}
	condition := meta.FindStatusCondition(computeTunnelConditions(gs, tunnelPlan{wantTunnel: true}, &dep), "TunnelReady")
	if condition == nil || condition.Status == metav1.ConditionTrue {
		t.Fatalf("process readiness implied relay readiness: %+v", condition)
	}
}

func TestPlayitAssignmentDoesNotImplyRelayReady(t *testing.T) {
	t.Parallel()
	gs := &gameplanev1alpha1.GameServer{Spec: gameplanev1alpha1.GameServerSpec{Networking: gameplanev1alpha1.GameServerNetworking{Tunnel: &gameplanev1alpha1.GameServerTunnel{Provider: "playit", Playit: &gameplanev1alpha1.PlayitTunnelSpec{}, CredentialsSecretRef: &gameplanev1alpha1.SecretNameRef{Name: "relay-credentials"}}}}}
	dep := tunnelReadyDeployment()
	// A legacy deployment without the provider probe remains unknown even
	// when its Pod is Ready; assignment alone cannot bypass that requirement.
	legacy := meta.FindStatusCondition(computeTunnelConditions(gs, tunnelPlan{wantTunnel: true}, dep), "TunnelReady")
	if legacy == nil || legacy.Status != metav1.ConditionUnknown || legacy.Reason != "RelayConnectionUnknown" {
		t.Fatalf("legacy playit readiness=%+v, want unknown connection", legacy)
	}
	dep.Generation = 1
	dep.Status.ObservedGeneration = 1
	dep.Status.UpdatedReplicas = 1
	dep.Spec.Template.Spec.Containers = []corev1.Container{{Name: "tunnel", ReadinessProbe: &corev1.Probe{ProbeHandler: corev1.ProbeHandler{HTTPGet: &corev1.HTTPGetAction{Path: "/readyz", Port: intstr.FromInt32(8091)}}}}}
	for _, probePath := range []string{"/readyz", "/healthz"} {
		dep.Spec.Template.Spec.Containers[0].ReadinessProbe.HTTPGet.Path = probePath
		for _, endpoints := range [][]gameplanev1alpha1.GameServerEndpoint{nil, {{Name: "game", Host: "example.playit.gg", Port: 30000}}} {
			gs.Status.TunnelEndpoints = endpoints
			cond := meta.FindStatusCondition(computeTunnelConditions(gs, tunnelPlan{wantTunnel: true}, dep), "TunnelReady")
			wantStatus, wantReason := metav1.ConditionUnknown, "RelayConnectionUnknown"
			if len(endpoints) == 0 {
				wantStatus, wantReason = metav1.ConditionFalse, "AwaitingAddress"
			}
			if cond == nil || cond.Status != wantStatus || cond.Reason != wantReason {
				t.Fatalf("playit readiness=%+v, want %s/%s", cond, wantStatus, wantReason)
			}
		}
	}
}
