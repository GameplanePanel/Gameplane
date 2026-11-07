package controller

import (
	"strings"
	"testing"

	gameplanev1alpha1 "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestRestrictedHostportFailsBeforeWorkloadCreation(t *testing.T) {
	scheme := testScheme(t)
	if err := batchv1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	gs := &gameplanev1alpha1.GameServer{ObjectMeta: metav1.ObjectMeta{Name: "hostport", Namespace: "games", UID: "server-uid"}}
	gs.Spec.TemplateRef.Name = "compatible"
	gs.Spec.Networking.Expose = "Hostport"
	tmpl := &gameplanev1alpha1.GameTemplate{ObjectMeta: metav1.ObjectMeta{Name: gs.Spec.TemplateRef.Name}}
	tmpl.Spec.Security = &gameplanev1alpha1.GameSecuritySpec{RunAsUser: ptrTo(int64(1000)), FSGroup: ptrTo(int64(1000))}
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(gs, tmpl).WithStatusSubresource(gs).Build()
	r := &GameServerReconciler{Client: c, APIReader: c, Scheme: scheme, RestrictedWorkloads: true}
	if _, err := r.Reconcile(t.Context(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(gs)}); err != nil {
		t.Fatal(err)
	}
	if err := c.Get(t.Context(), client.ObjectKeyFromObject(gs), gs); err != nil {
		t.Fatal(err)
	}
	if gs.Status.Phase != gameplanev1alpha1.GameServerPhaseFailed || len(gs.Status.Conditions) == 0 || !strings.Contains(gs.Status.Conditions[0].Message, "use NodePort or LoadBalancer") {
		t.Fatalf("incompatible exposure did not report an actionable failure: %+v", gs.Status)
	}
	var workloads appsv1.StatefulSetList
	if err := c.List(t.Context(), &workloads); err != nil || len(workloads.Items) != 0 {
		t.Fatalf("created a workload before validation: %+v %v", workloads.Items, err)
	}
	var claims corev1.PersistentVolumeClaimList
	if err := c.List(t.Context(), &claims); err != nil || len(claims.Items) != 0 {
		t.Fatalf("created storage before validation: %+v %v", claims.Items, err)
	}
	var configs corev1.ConfigMapList
	if err := c.List(t.Context(), &configs); err != nil || len(configs.Items) != 0 {
		t.Fatalf("created template/config state before validation: %+v %v", configs.Items, err)
	}
	// Exposure validation must also precede template lookup. This keeps
	// future template admission/rollout work behind the same rejection.
	if err := c.Delete(t.Context(), tmpl); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Reconcile(t.Context(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(gs)}); err != nil {
		t.Fatal(err)
	}
	if err := c.Get(t.Context(), client.ObjectKeyFromObject(gs), gs); err != nil {
		t.Fatal(err)
	}
	if len(gs.Status.Conditions) == 0 || !strings.Contains(gs.Status.Conditions[0].Message, "use NodePort or LoadBalancer") {
		t.Fatalf("template lookup ran before exposure validation: %+v", gs.Status)
	}
}

func TestRestrictedTemplateIdentity(t *testing.T) {
	for _, test := range []struct {
		name         string
		uid, gid, fs *int64
		valid        bool
	}{
		{"absent", nil, nil, nil, false},
		{"root", ptrTo(int64(0)), nil, ptrTo(int64(1000)), false},
		{"missing-volume-group", ptrTo(int64(1000)), nil, nil, false},
		{"root-group", ptrTo(int64(1000)), ptrTo(int64(0)), ptrTo(int64(1000)), false},
		{"compatible", ptrTo(int64(1000)), ptrTo(int64(1000)), ptrTo(int64(1000)), true},
		{"default-primary-group", ptrTo(int64(1000)), nil, ptrTo(int64(1000)), true},
	} {
		t.Run(test.name, func(t *testing.T) {
			tmpl := &gameplanev1alpha1.GameTemplate{}
			tmpl.Spec.Security = &gameplanev1alpha1.GameSecuritySpec{RunAsUser: test.uid, RunAsGroup: test.gid, FSGroup: test.fs}
			if err := validateRestrictedTemplate(tmpl); (err == nil) != test.valid {
				t.Fatalf("valid=%t: %v", test.valid, err)
			}
		})
	}
}

func TestWorkloadTokenOnlyMountedInAgent(t *testing.T) {
	tmpl := &gameplanev1alpha1.GameTemplate{}
	tmpl.Spec.Security = &gameplanev1alpha1.GameSecuritySpec{RunAsUser: ptrTo(int64(1000)), FSGroup: ptrTo(int64(1000))}
	gs := &gameplanev1alpha1.GameServer{}
	pod := corev1.PodSpec{SecurityContext: gamePodSecurityContext(tmpl), Containers: []corev1.Container{{Name: "game"}, buildAgentContainer(gs, tmpl, nil, "agent", "", "")}, InitContainers: []corev1.Container{buildConfigInitContainer("", tmpl)}}
	applyWorkloadSecurity(&pod, tmpl, true, 3600)
	if pod.AutomountServiceAccountToken == nil || *pod.AutomountServiceAccountToken {
		t.Fatal("automatic credentials exposed")
	}
	for _, c := range append(pod.Containers, pod.InitContainers...) {
		sc := c.SecurityContext
		if sc == nil || sc.RunAsNonRoot == nil || !*sc.RunAsNonRoot || sc.AllowPrivilegeEscalation == nil || *sc.AllowPrivilegeEscalation || sc.SeccompProfile == nil || sc.SeccompProfile.Type != corev1.SeccompProfileTypeRuntimeDefault || sc.Capabilities == nil || len(sc.Capabilities.Drop) != 1 || sc.Capabilities.Drop[0] != "ALL" || len(sc.Capabilities.Add) != 0 {
			t.Fatalf("%s not Restricted: %+v", c.Name, sc)
		}
		mounted := false
		for _, m := range c.VolumeMounts {
			if m.Name == agentTokenVolumeName {
				mounted = true
				if !m.ReadOnly || m.MountPath != "/var/run/secrets/kubernetes.io/serviceaccount" {
					t.Fatalf("unsafe token mount: %+v", m)
				}
			}
		}
		if mounted != (c.Name == "agent") {
			t.Fatalf("%s token mount=%t", c.Name, mounted)
		}
	}
	var projection *corev1.ProjectedVolumeSource
	for _, v := range pod.Volumes {
		if v.Name == agentTokenVolumeName {
			projection = v.Projected
		}
	}
	if projection == nil || len(projection.Sources) != 3 || projection.Sources[0].ServiceAccountToken == nil || *projection.Sources[0].ServiceAccountToken.ExpirationSeconds != 3600 {
		t.Fatalf("missing rotating token projection: %+v", projection)
	}
}
