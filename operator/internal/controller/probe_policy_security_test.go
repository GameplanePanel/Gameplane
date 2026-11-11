package controller

import (
	"context"
	"strings"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	gameplanev1alpha1 "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
)

func probeSecGameServer() *gameplanev1alpha1.GameServer {
	return &gameplanev1alpha1.GameServer{
		ObjectMeta: metav1.ObjectMeta{Name: "alpha", Namespace: "games", UID: types.UID("alpha-uid")},
	}
}

func probeSecTemplate(p *gameplanev1alpha1.GameProbesSpec) *gameplanev1alpha1.GameTemplate {
	return &gameplanev1alpha1.GameTemplate{
		ObjectMeta: metav1.ObjectMeta{Name: "minecraft", Namespace: "games"},
		Spec:       gameplanev1alpha1.GameTemplateSpec{Probes: p},
	}
}

func probeSecTCP(port intstr.IntOrString) *corev1.Probe {
	return &corev1.Probe{ProbeHandler: corev1.ProbeHandler{
		TCPSocket: &corev1.TCPSocketAction{Port: port},
	}}
}

func probeSecTCPHost(host string, port intstr.IntOrString) *corev1.Probe {
	return &corev1.Probe{ProbeHandler: corev1.ProbeHandler{
		TCPSocket: &corev1.TCPSocketAction{Host: host, Port: port},
	}}
}

func probeSecHTTP(host string, port intstr.IntOrString) *corev1.Probe {
	return &corev1.Probe{ProbeHandler: corev1.ProbeHandler{
		HTTPGet: &corev1.HTTPGetAction{Host: host, Path: "/healthz", Port: port},
	}}
}

func probeSecGRPC(port int32) *corev1.Probe {
	return &corev1.Probe{ProbeHandler: corev1.ProbeHandler{
		GRPC: &corev1.GRPCAction{Port: port},
	}}
}

func probeSecExec() *corev1.Probe {
	return &corev1.Probe{ProbeHandler: corev1.ProbeHandler{
		Exec: &corev1.ExecAction{Command: []string{"true"}},
	}}
}

// The fake client can seed pre-upgrade state that a current API server
// would reject. In particular, a pinned snapshot can outlive its template.
func TestReconcileRefusesStoredProbeHosts(t *testing.T) {
	for _, source := range []string{"server override", "pinned snapshot", "catalog rollout"} {
		t.Run(source, func(t *testing.T) {
			r, gs, tmpl := revisionFixture(t)
			unsafe := &gameplanev1alpha1.GameProbesSpec{Liveness: probeSecHTTP("169.254.169.254", intstr.FromInt32(80))}
			switch source {
			case "server override":
				gs.Spec.Probes = unsafe
			case "pinned snapshot":
				tmpl.Spec.Probes = unsafe
				revision, err := r.saveTemplateSnapshot(t.Context(), gs, tmpl)
				if err != nil {
					t.Fatal(err)
				}
				gs.Annotations[effectiveTemplateAnnotation] = revision
			case "catalog rollout":
				tmpl.Spec.Probes = unsafe
				if err := r.Update(t.Context(), tmpl); err != nil {
					t.Fatal(err)
				}
				gs.Spec.TemplateRollout.Policy = "Automatic"
			}
			if err := r.Update(t.Context(), gs); err != nil {
				t.Fatal(err)
			}
			key := client.ObjectKeyFromObject(gs)
			if _, err := r.Reconcile(t.Context(), ctrl.Request{NamespacedName: key}); err != nil {
				t.Fatal(err)
			}
			if err := r.Get(t.Context(), key, gs); err != nil {
				t.Fatal(err)
			}
			ready := meta.FindStatusCondition(gs.Status.Conditions, "Ready")
			if gs.Status.Phase != gameplanev1alpha1.GameServerPhaseFailed || ready == nil || ready.Status != metav1.ConditionFalse ||
				!strings.Contains(ready.Message, "spec.probes.liveness.httpGet.host") {
				t.Fatalf("missing probe validation failure: %+v", gs.Status)
			}
			if err := r.Get(t.Context(), key, &appsv1.StatefulSet{}); !apierrors.IsNotFound(err) {
				t.Fatalf("unsafe workload: expected NotFound, got %v", err)
			}
			if gs.Annotations[lifecycleOperationAnnotation] != "" {
				t.Fatal("unsafe rollout claimed the server")
			}
		})
	}
}

// TestProbeNetworkPolicyPortsSecurity covers which probe targets get a
// NetworkPolicy port: only in-pod TCP targets, deduplicated, with server
// overrides applied before the template's probes.
func TestProbeNetworkPolicyPortsSecurity(t *testing.T) {
	cases := []struct {
		name string
		tmpl *gameplanev1alpha1.GameProbesSpec
		gs   *gameplanev1alpha1.GameProbesSpec
		want []string
	}{
		{
			name: "tcpSocket integer port",
			tmpl: &gameplanev1alpha1.GameProbesSpec{Readiness: probeSecTCP(intstr.FromInt32(25565))},
			want: []string{"25565"},
		},
		{
			name: "tcpSocket named port kept as name",
			tmpl: &gameplanev1alpha1.GameProbesSpec{Readiness: probeSecTCP(intstr.FromString("game"))},
			want: []string{"game"},
		},
		{
			name: "httpGet integer port",
			tmpl: &gameplanev1alpha1.GameProbesSpec{Liveness: probeSecHTTP("", intstr.FromInt32(8080))},
			want: []string{"8080"},
		},
		{
			name: "grpc port",
			tmpl: &gameplanev1alpha1.GameProbesSpec{Startup: probeSecGRPC(9000)},
			want: []string{"9000"},
		},
		{
			name: "exec-only probes need no network port",
			tmpl: &gameplanev1alpha1.GameProbesSpec{
				Readiness: probeSecExec(), Liveness: probeSecExec(), Startup: probeSecExec(),
			},
			want: nil,
		},
		{
			name: "no probes at all",
			tmpl: nil,
			want: nil,
		},
		{
			name: "tcpSocket with host set is skipped",
			tmpl: &gameplanev1alpha1.GameProbesSpec{Readiness: probeSecTCPHost("10.0.0.5", intstr.FromInt32(25565))},
			want: nil,
		},
		{
			name: "httpGet with host set is skipped",
			tmpl: &gameplanev1alpha1.GameProbesSpec{Liveness: probeSecHTTP("10.0.0.5", intstr.FromInt32(8080))},
			want: nil,
		},
		{
			name: "host-set probe skipped while in-pod probe kept",
			tmpl: &gameplanev1alpha1.GameProbesSpec{
				Readiness: probeSecTCPHost("10.0.0.5", intstr.FromInt32(1111)),
				Liveness:  probeSecTCP(intstr.FromInt32(25565)),
			},
			want: []string{"25565"},
		},
		{
			name: "zero integer port skipped",
			tmpl: &gameplanev1alpha1.GameProbesSpec{Readiness: probeSecTCP(intstr.FromInt32(0))},
			want: nil,
		},
		{
			name: "zero grpc port skipped",
			tmpl: &gameplanev1alpha1.GameProbesSpec{Startup: probeSecGRPC(0)},
			want: nil,
		},
		{
			name: "empty named port skipped",
			tmpl: &gameplanev1alpha1.GameProbesSpec{Readiness: probeSecTCP(intstr.FromString(""))},
			want: nil,
		},
		{
			name: "handler-less probe skipped",
			tmpl: &gameplanev1alpha1.GameProbesSpec{Readiness: &corev1.Probe{}},
			want: nil,
		},
		{
			name: "duplicate ports across probe kinds collapse",
			tmpl: &gameplanev1alpha1.GameProbesSpec{
				Readiness: probeSecTCP(intstr.FromInt32(25565)),
				Liveness:  probeSecHTTP("", intstr.FromInt32(25565)),
				Startup:   probeSecGRPC(25565),
			},
			want: []string{"25565"},
		},
		{
			name: "distinct ports kept in probe order",
			tmpl: &gameplanev1alpha1.GameProbesSpec{
				Readiness: probeSecTCP(intstr.FromInt32(25565)),
				Liveness:  probeSecTCP(intstr.FromInt32(27015)),
			},
			want: []string{"25565", "27015"},
		},
		{
			name: "server readiness override wins over template",
			tmpl: &gameplanev1alpha1.GameProbesSpec{Readiness: probeSecTCP(intstr.FromInt32(25565))},
			gs:   &gameplanev1alpha1.GameProbesSpec{Readiness: probeSecTCP(intstr.FromInt32(27015))},
			want: []string{"27015"},
		},
		{
			name: "server override per probe, template fills the rest",
			tmpl: &gameplanev1alpha1.GameProbesSpec{
				Readiness: probeSecTCP(intstr.FromInt32(25565)),
				Liveness:  probeSecTCP(intstr.FromInt32(25575)),
			},
			gs:   &gameplanev1alpha1.GameProbesSpec{Liveness: probeSecTCP(intstr.FromInt32(25580))},
			want: []string{"25565", "25580"},
		},
		{
			name: "server override to exec removes template network port",
			tmpl: &gameplanev1alpha1.GameProbesSpec{Readiness: probeSecTCP(intstr.FromInt32(25565))},
			gs:   &gameplanev1alpha1.GameProbesSpec{Readiness: probeSecExec()},
			want: nil,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gs := probeSecGameServer()
			gs.Spec.Probes = tc.gs
			got := probeNetworkPolicyPorts(gs, probeSecTemplate(tc.tmpl))
			if len(got) != len(tc.want) {
				t.Fatalf("got %d ports, want %d: %v", len(got), len(tc.want), got)
			}
			for i, port := range got {
				if port.Protocol == nil || *port.Protocol != corev1.ProtocolTCP {
					t.Errorf("port %d: protocol = %v, want TCP", i, port.Protocol)
				}
				if port.Port == nil || port.Port.String() != tc.want[i] {
					t.Errorf("port %d: got %v, want %q", i, port.Port, tc.want[i])
				}
			}
		})
	}
}

// TestEffectiveGameProbesSecurity checks that the server's per-probe
// overrides win and that unset overrides fall back to the template.
func TestEffectiveGameProbesSecurity(t *testing.T) {
	tmplReady := probeSecTCP(intstr.FromInt32(1))
	tmplLive := probeSecTCP(intstr.FromInt32(2))
	tmplStart := probeSecTCP(intstr.FromInt32(3))
	srvReady := probeSecTCP(intstr.FromInt32(11))
	srvLive := probeSecTCP(intstr.FromInt32(12))
	srvStart := probeSecTCP(intstr.FromInt32(13))

	tmpl := probeSecTemplate(&gameplanev1alpha1.GameProbesSpec{
		Readiness: tmplReady, Liveness: tmplLive, Startup: tmplStart,
	})

	t.Run("no probes anywhere", func(t *testing.T) {
		got := effectiveGameProbes(probeSecGameServer(), probeSecTemplate(nil))
		if got.Readiness != nil || got.Liveness != nil || got.Startup != nil {
			t.Fatalf("want three nil probes, got %v", got)
		}
	})

	t.Run("template only", func(t *testing.T) {
		got := effectiveGameProbes(probeSecGameServer(), tmpl)
		if got.Readiness != tmplReady || got.Liveness != tmplLive || got.Startup != tmplStart {
			t.Fatalf("want template probes, got %v", got)
		}
	})

	t.Run("server overrides all three", func(t *testing.T) {
		gs := probeSecGameServer()
		gs.Spec.Probes = &gameplanev1alpha1.GameProbesSpec{
			Readiness: srvReady, Liveness: srvLive, Startup: srvStart,
		}
		got := effectiveGameProbes(gs, tmpl)
		if got.Readiness != srvReady || got.Liveness != srvLive || got.Startup != srvStart {
			t.Fatalf("want server probes, got %v", got)
		}
	})

	t.Run("server override of one probe keeps template for the others", func(t *testing.T) {
		gs := probeSecGameServer()
		gs.Spec.Probes = &gameplanev1alpha1.GameProbesSpec{Startup: srvStart}
		got := effectiveGameProbes(gs, tmpl)
		if got.Readiness != tmplReady || got.Liveness != tmplLive || got.Startup != srvStart {
			t.Fatalf("want template readiness/liveness and server startup, got %v", got)
		}
	})
}

// probeSecClient builds a reconciler over a fake client with the
// NetworkPolicy type registered on top of the package scheme.
func probeSecClient(t *testing.T, enabled bool, objs ...client.Object) (*GameServerReconciler, client.Client) {
	t.Helper()
	s := testScheme(t)
	if err := networkingv1.AddToScheme(s); err != nil {
		t.Fatalf("networking scheme: %v", err)
	}
	cl := fake.NewClientBuilder().WithScheme(s).WithObjects(objs...).Build()
	r := &GameServerReconciler{
		Client:                    cl,
		Scheme:                    s,
		ProbeIngressPolicyEnabled: enabled,
		ProbeIngressFromCIDRs:     []string{"10.244.0.0/16"},
	}
	return r, cl
}

func probeSecGetPolicy(t *testing.T, cl client.Client) (*networkingv1.NetworkPolicy, error) {
	t.Helper()
	var np networkingv1.NetworkPolicy
	err := cl.Get(context.Background(), client.ObjectKey{Namespace: "games", Name: "alpha-probes"}, &np)
	return &np, err
}

func TestReconcileProbePolicySecurity_CreateAndUpdate(t *testing.T) {
	r, cl := probeSecClient(t, true)
	ctx := context.Background()
	gs := probeSecGameServer()
	tmpl := probeSecTemplate(&gameplanev1alpha1.GameProbesSpec{
		Readiness: probeSecTCP(intstr.FromInt32(25565)),
	})

	if err := r.reconcileProbePolicy(ctx, gs, tmpl); err != nil {
		t.Fatalf("create: %v", err)
	}
	np, err := probeSecGetPolicy(t, cl)
	if err != nil {
		t.Fatalf("get created policy: %v", err)
	}
	if !metav1.IsControlledBy(np, gs) {
		t.Errorf("policy not controlled by GameServer: %v", np.OwnerReferences)
	}
	if got := np.Spec.PodSelector.MatchLabels["app.kubernetes.io/instance"]; got != "alpha" {
		t.Errorf("pod selector instance = %q, want alpha", got)
	}
	if got := np.Spec.PodSelector.MatchLabels["app.kubernetes.io/name"]; got != "gameplane-game" {
		t.Errorf("pod selector name = %q, want gameplane-game", got)
	}
	if len(np.Spec.PolicyTypes) != 1 || np.Spec.PolicyTypes[0] != networkingv1.PolicyTypeIngress {
		t.Errorf("policy types = %v, want [Ingress]", np.Spec.PolicyTypes)
	}
	if len(np.Spec.Ingress) != 1 {
		t.Fatalf("ingress rules = %d, want 1", len(np.Spec.Ingress))
	}
	rule := np.Spec.Ingress[0]
	if len(rule.From) != 1 || rule.From[0].IPBlock == nil || rule.From[0].IPBlock.CIDR != "10.244.0.0/16" {
		t.Errorf("from = %v, want single IPBlock 10.244.0.0/16", rule.From)
	}
	if len(rule.Ports) != 1 || rule.Ports[0].Port == nil || rule.Ports[0].Port.String() != "25565" {
		t.Errorf("ports = %v, want [25565]", rule.Ports)
	}

	// Update: probe port and CIDR change, the same policy is rewritten.
	tmpl.Spec.Probes.Readiness = probeSecTCP(intstr.FromInt32(27015))
	r.ProbeIngressFromCIDRs = []string{"192.168.0.0/16"}
	if err := r.reconcileProbePolicy(ctx, gs, tmpl); err != nil {
		t.Fatalf("update: %v", err)
	}
	np, err = probeSecGetPolicy(t, cl)
	if err != nil {
		t.Fatalf("get updated policy: %v", err)
	}
	rule = np.Spec.Ingress[0]
	if len(rule.Ports) != 1 || rule.Ports[0].Port == nil || rule.Ports[0].Port.String() != "27015" {
		t.Errorf("updated ports = %v, want [27015]", rule.Ports)
	}
	if len(rule.From) != 1 || rule.From[0].IPBlock == nil || rule.From[0].IPBlock.CIDR != "192.168.0.0/16" {
		t.Errorf("updated from = %v, want 192.168.0.0/16", rule.From)
	}
}

func TestReconcileProbePolicySecurity_DeletesWhenNotNeeded(t *testing.T) {
	gs := probeSecGameServer()
	networkProbe := probeSecTemplate(&gameplanev1alpha1.GameProbesSpec{
		Readiness: probeSecTCP(intstr.FromInt32(25565)),
	})
	execOnly := probeSecTemplate(&gameplanev1alpha1.GameProbesSpec{Readiness: probeSecExec()})

	cases := []struct {
		name    string
		enabled bool
		cidrs   []string
		tmpl    *gameplanev1alpha1.GameTemplate
	}{
		{name: "feature disabled", enabled: false, cidrs: []string{"10.0.0.0/8"}, tmpl: networkProbe},
		{name: "no CIDRs configured", enabled: true, cidrs: nil, tmpl: networkProbe},
		{name: "exec-only probes", enabled: true, cidrs: []string{"10.0.0.0/8"}, tmpl: execOnly},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Create the policy under the enabled configuration first.
			r, cl := probeSecClient(t, true)
			if err := r.reconcileProbePolicy(context.Background(), gs, networkProbe); err != nil {
				t.Fatalf("seed create: %v", err)
			}
			if _, err := probeSecGetPolicy(t, cl); err != nil {
				t.Fatalf("seed policy missing: %v", err)
			}

			r.ProbeIngressPolicyEnabled = tc.enabled
			r.ProbeIngressFromCIDRs = tc.cidrs
			if err := r.reconcileProbePolicy(context.Background(), gs, tc.tmpl); err != nil {
				t.Fatalf("reconcile: %v", err)
			}
			if _, err := probeSecGetPolicy(t, cl); !apierrors.IsNotFound(err) {
				t.Errorf("policy after reconcile: err = %v, want NotFound", err)
			}
		})
	}
}

func TestReconcileProbePolicySecurity_NoOpWhenNothingToDelete(t *testing.T) {
	r, cl := probeSecClient(t, false)
	gs := probeSecGameServer()
	tmpl := probeSecTemplate(&gameplanev1alpha1.GameProbesSpec{
		Readiness: probeSecTCP(intstr.FromInt32(25565)),
	})
	if err := r.reconcileProbePolicy(context.Background(), gs, tmpl); err != nil {
		t.Fatalf("disabled with no existing policy: %v", err)
	}
	if _, err := probeSecGetPolicy(t, cl); !apierrors.IsNotFound(err) {
		t.Errorf("policy created while disabled: err = %v", err)
	}
}

// TestReconcileProbePolicySecurity_KeepsForeignPolicy verifies a policy with
// the same name that this GameServer does not control is never deleted.
func TestReconcileProbePolicySecurity_KeepsForeignPolicy(t *testing.T) {
	gs := probeSecGameServer()
	tmpl := probeSecTemplate(&gameplanev1alpha1.GameProbesSpec{
		Readiness: probeSecTCP(intstr.FromInt32(25565)),
	})

	cases := []struct {
		name   string
		owners []metav1.OwnerReference
	}{
		{name: "no owner", owners: nil},
		{name: "owned by a different GameServer", owners: []metav1.OwnerReference{{
			APIVersion: "gameplane.local/v1alpha1",
			Kind:       "GameServer",
			Name:       "beta",
			UID:        "beta-uid",
			Controller: ptrBoolSecurity(true),
		}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			foreign := &networkingv1.NetworkPolicy{
				ObjectMeta: metav1.ObjectMeta{
					Name:            "alpha-probes",
					Namespace:       "games",
					OwnerReferences: tc.owners,
				},
				Spec: networkingv1.NetworkPolicySpec{
					PolicyTypes: []networkingv1.PolicyType{networkingv1.PolicyTypeIngress},
				},
			}
			r, cl := probeSecClient(t, false, foreign)
			if err := r.reconcileProbePolicy(context.Background(), gs, tmpl); err != nil {
				t.Fatalf("reconcile: %v", err)
			}
			if _, err := probeSecGetPolicy(t, cl); err != nil {
				t.Errorf("foreign policy was removed: %v", err)
			}
		})
	}
}

func ptrBoolSecurity(b bool) *bool {
	return &b
}
