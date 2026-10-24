package controller

import (
	"reflect"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	gameplanev1alpha1 "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
)

func TestAuxiliaryResourceConfiguration(t *testing.T) {
	for _, bad := range []string{"null", `{"request":{"cpu":"1"}}`, `{"requests":{"cpu":"-1"}}`, `{"requests":{"memory":"128Mi"},"limits":{"memory":"64Mi"}}`, `{} {}`} {
		if _, err := ParseAuxiliaryResources(bad); err == nil {
			t.Errorf("accepted invalid resources %s", bad)
		}
	}
	unset, err := ParseAuxiliaryResources("")
	if err != nil || unset != nil {
		t.Fatalf("absent configuration: %v %v", unset, err)
	}
	cleared, err := ParseAuxiliaryResources(`{"requests":{},"limits":{}}`)
	if err != nil || cleared == nil {
		t.Fatalf("explicit clear: %v %v", cleared, err)
	}
	if got := auxiliaryResources(cleared, "25m", "64Mi"); len(got.Requests) != 0 || len(got.Limits) != 0 {
		t.Fatal("clear restored default reservations")
	}
	defaults := auxiliaryResources(nil, "25m", "64Mi")
	if defaults.Requests.Cpu().String() != "25m" || defaults.Requests.Memory().String() != "64Mi" || len(defaults.Limits) != 0 {
		t.Fatalf("unexpected scheduling defaults: %+v", defaults)
	}
}

func TestAuxiliaryResourcesReachAgentAndResticContainers(t *testing.T) {
	configured := &corev1.ResourceRequirements{Requests: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("73m")}}
	gs := &gameplanev1alpha1.GameServer{}
	tmpl := &gameplanev1alpha1.GameTemplate{}
	agent := buildAgentContainer(gs, tmpl, nil, "agent", "", "", configured)
	if !reflect.DeepEqual(agent.Resources, *configured) {
		t.Fatalf("agent lost install resources: %+v", agent.Resources)
	}
	tmpl.Spec.Agent = &gameplanev1alpha1.AgentSpec{Image: "custom-agent"}
	agent = buildAgentContainer(gs, tmpl, nil, "agent", "", "", configured)
	if !reflect.DeepEqual(agent.Resources, *configured) {
		t.Fatal("image-only template override removed resource defaults")
	}
	tmpl.Spec.Agent.Resources = corev1.ResourceRequirements{Requests: corev1.ResourceList{corev1.ResourceMemory: resource.MustParse("91Mi")}}
	agent = buildAgentContainer(gs, tmpl, nil, "agent", "", "", configured)
	if !reflect.DeepEqual(agent.Resources, tmpl.Spec.Agent.Resources) {
		t.Fatal("template agent resources did not replace the install defaults")
	}
	b := &gameplanev1alpha1.Backup{Spec: gameplanev1alpha1.BackupSpec{RepoRef: &gameplanev1alpha1.SecretKeySelector{Name: "repo"}}}
	br := &BackupReconciler{BackupResources: configured}
	backup := br.buildBackupPodSpec(b, tmpl)
	forget := br.buildForgetPodSpec(b)
	rr := &RestoreReconciler{BackupResources: configured}
	restore := rr.buildRestorePodSpec(&gameplanev1alpha1.Restore{}, b, tmpl)
	for _, container := range append(append(append(backup.Containers, backup.InitContainers...), forget.Containers...), restore.Containers...) {
		if !reflect.DeepEqual(container.Resources, *configured) {
			t.Errorf("container %s lost resource configuration: %+v", container.Name, container.Resources)
		}
	}
	if init := buildConfigInitContainer("init", tmpl, configured); !reflect.DeepEqual(init.Resources, *configured) {
		t.Fatal("config init lost install resource configuration")
	}
	// Builders must not return the shared maps: one container's customization
	// cannot mutate the defaults for another server or operation.
	agent.Resources.Requests[corev1.ResourceMemory] = resource.MustParse("1Mi")
	if tmpl.Spec.Agent.Resources.Requests.Memory().String() != "91Mi" {
		t.Fatal("agent resources alias the template")
	}
}

func TestAuxiliaryDeploymentResourceOverrideAndRemoval(t *testing.T) {
	s := wipeScheme(t)
	gs := &gameplanev1alpha1.GameServer{ObjectMeta: metav1.ObjectMeta{Name: "server", Namespace: "games", UID: "server-uid"}, Spec: gameplanev1alpha1.GameServerSpec{Networking: gameplanev1alpha1.GameServerNetworking{Tunnel: &gameplanev1alpha1.GameServerTunnel{Enabled: true, Provider: "frp", Frp: &gameplanev1alpha1.FrpTunnelSpec{ServerAddr: "relay.example"}}}}}
	c := fake.NewClientBuilder().WithScheme(s).WithObjects(gs).Build()
	r := &GameServerReconciler{Client: c, Scheme: s}
	custom := &corev1.ResourceRequirements{Requests: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("81m")}}
	for _, configured := range []*corev1.ResourceRequirements{nil, custom, {}} {
		r.SentinelResources, r.TunnelResources = configured, configured
		if err := r.reconcileSentinel(t.Context(), gs, singlePortTemplate(true, "minecraft"), true); err != nil {
			t.Fatal(err)
		}
		if err := r.reconcileTunnel(t.Context(), gs, singlePortTemplate(true, "minecraft"), true); err != nil {
			t.Fatal(err)
		}
		for _, component := range []struct{ suffix, memory string }{{"waker", "32Mi"}, {"tunnel", "64Mi"}} {
			var dep appsv1.Deployment
			if err := c.Get(t.Context(), client.ObjectKey{Namespace: gs.Namespace, Name: gs.Name + "-" + component.suffix}, &dep); err != nil {
				t.Fatal(err)
			}
			want := auxiliaryResources(configured, "25m", component.memory)
			if !reflect.DeepEqual(dep.Spec.Template.Spec.Containers[0].Resources, want) {
				t.Fatalf("%s did not apply/clear resources", component.suffix)
			}
		}
	}
}
