package controller

import (
	"context"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	gameplanev1alpha1 "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
)

func TestDerivePhase(t *testing.T) {
	table := []struct {
		name     string
		suspend  bool
		ssExists bool
		ssReady  bool
		hbFresh  bool
		want     gameplanev1alpha1.GameServerPhase
	}{
		{"pending when no ss", false, false, false, false, gameplanev1alpha1.GameServerPhasePending},
		{"starting when ss not ready", false, true, false, false, gameplanev1alpha1.GameServerPhaseStarting},
		{"starting when ready but no heartbeat", false, true, true, false, gameplanev1alpha1.GameServerPhaseStarting},
		{"running when ready and fresh heartbeat", false, true, true, true, gameplanev1alpha1.GameServerPhaseRunning},
		{"suspended when suspend + ss gone", true, false, false, false, gameplanev1alpha1.GameServerPhaseSuspended},
		{"stopping when suspend + ss still ready", true, true, true, true, gameplanev1alpha1.GameServerPhaseStopping},
	}
	for _, tc := range table {
		t.Run(tc.name, func(t *testing.T) {
			gs := &gameplanev1alpha1.GameServer{
				Spec: gameplanev1alpha1.GameServerSpec{Suspend: tc.suspend},
			}
			got := derivePhase(gs, tc.ssExists, tc.ssReady, tc.hbFresh, idleAwake)
			if got != tc.want {
				t.Errorf("want %q, got %q", tc.want, got)
			}
		})
	}
}

func TestHeartbeatFresh(t *testing.T) {
	gs := &gameplanev1alpha1.GameServer{}
	if heartbeatFresh(gs) {
		t.Error("no heartbeat should be stale")
	}
	now := metav1.Now()
	gs.Status.Agent = &gameplanev1alpha1.AgentStatus{LastHeartbeat: &now}
	if !heartbeatFresh(gs) {
		t.Error("heartbeat now should be fresh")
	}
	old := metav1.NewTime(time.Now().Add(-10 * time.Minute))
	gs.Status.Agent.LastHeartbeat = &old
	if heartbeatFresh(gs) {
		t.Error("heartbeat 10m ago should be stale")
	}
}

func TestValidatePlayitEndpoint(t *testing.T) {
	tests := []struct {
		name              string
		endpoint          *gameplanev1alpha1.GameServerEndpoint
		advertisedPorts   []string
		expectValid       bool
		expectMessagePart string
	}{
		{
			name: "valid ipv4",
			endpoint: &gameplanev1alpha1.GameServerEndpoint{
				Name: "java",
				Host: "203.0.113.1",
				Port: 25565,
			},
			advertisedPorts: []string{"java"},
			expectValid:     true,
		},
		{
			name: "valid ipv6",
			endpoint: &gameplanev1alpha1.GameServerEndpoint{
				Name: "java",
				Host: "2001:db8::1",
				Port: 25565,
			},
			advertisedPorts: []string{"java"},
			expectValid:     true,
		},
		{
			name: "valid dns name",
			endpoint: &gameplanev1alpha1.GameServerEndpoint{
				Name: "java",
				Host: "game.example.com",
				Port: 25565,
			},
			advertisedPorts: []string{"java"},
			expectValid:     true,
		},
		{
			name: "valid dns name with hyphens",
			endpoint: &gameplanev1alpha1.GameServerEndpoint{
				Name: "java",
				Host: "my-game-server.example.com",
				Port: 25565,
			},
			advertisedPorts: []string{"java"},
			expectValid:     true,
		},
		{
			name: "empty host",
			endpoint: &gameplanev1alpha1.GameServerEndpoint{
				Name: "java",
				Host: "",
				Port: 25565,
			},
			advertisedPorts:   []string{"java"},
			expectValid:       false,
			expectMessagePart: "hostname",
		},
		{
			name: "control character in host",
			endpoint: &gameplanev1alpha1.GameServerEndpoint{
				Name: "java",
				Host: "game\x00server.com",
				Port: 25565,
			},
			advertisedPorts:   []string{"java"},
			expectValid:       false,
			expectMessagePart: "control",
		},
		{
			name: "embedded scheme http",
			endpoint: &gameplanev1alpha1.GameServerEndpoint{
				Name: "java",
				Host: "http://game.example.com",
				Port: 25565,
			},
			advertisedPorts:   []string{"java"},
			expectValid:       false,
			expectMessagePart: "scheme",
		},
		{
			name: "embedded scheme https",
			endpoint: &gameplanev1alpha1.GameServerEndpoint{
				Name: "java",
				Host: "https://game.example.com",
				Port: 25565,
			},
			advertisedPorts:   []string{"java"},
			expectValid:       false,
			expectMessagePart: "scheme",
		},
		{
			name: "port 0",
			endpoint: &gameplanev1alpha1.GameServerEndpoint{
				Name: "java",
				Host: "203.0.113.1",
				Port: 0,
			},
			advertisedPorts:   []string{"java"},
			expectValid:       false,
			expectMessagePart: "port",
		},
		{
			name: "port 65536",
			endpoint: &gameplanev1alpha1.GameServerEndpoint{
				Name: "java",
				Host: "203.0.113.1",
				Port: 65536,
			},
			advertisedPorts:   []string{"java"},
			expectValid:       false,
			expectMessagePart: "port",
		},
		{
			name: "port name not advertised",
			endpoint: &gameplanev1alpha1.GameServerEndpoint{
				Name: "rcon",
				Host: "203.0.113.1",
				Port: 25575,
			},
			advertisedPorts:   []string{"java"},
			expectValid:       false,
			expectMessagePart: "advertised",
		},
		{
			name: "very long hostname",
			endpoint: &gameplanev1alpha1.GameServerEndpoint{
				Name: "java",
				Host: strings.Repeat("a", 254),
				Port: 25565,
			},
			advertisedPorts:   []string{"java"},
			expectValid:       false,
			expectMessagePart: "too long",
		},
		{
			name: "whitespace in host",
			endpoint: &gameplanev1alpha1.GameServerEndpoint{
				Name: "java",
				Host: "game server.com",
				Port: 25565,
			},
			advertisedPorts:   []string{"java"},
			expectValid:       false,
			expectMessagePart: "whitespace",
		},
		{
			name: "tab character in host",
			endpoint: &gameplanev1alpha1.GameServerEndpoint{
				Name: "java",
				Host: "game\tserver.com",
				Port: 25565,
			},
			advertisedPorts:   []string{"java"},
			expectValid:       false,
			expectMessagePart: "whitespace",
		},
		{
			name: "minimum valid port",
			endpoint: &gameplanev1alpha1.GameServerEndpoint{
				Name: "java",
				Host: "203.0.113.1",
				Port: 1,
			},
			advertisedPorts: []string{"java"},
			expectValid:     true,
		},
		{
			name: "maximum valid port",
			endpoint: &gameplanev1alpha1.GameServerEndpoint{
				Name: "java",
				Host: "203.0.113.1",
				Port: 65535,
			},
			advertisedPorts: []string{"java"},
			expectValid:     true,
		},
		{
			name: "private ipv4 rejected",
			endpoint: &gameplanev1alpha1.GameServerEndpoint{
				Name: "java",
				Host: "10.0.0.1",
				Port: 25565,
			},
			advertisedPorts:   []string{"java"},
			expectValid:       false,
			expectMessagePart: "private",
		},
		{
			name: "loopback ipv4 rejected",
			endpoint: &gameplanev1alpha1.GameServerEndpoint{
				Name: "java",
				Host: "127.0.0.1",
				Port: 25565,
			},
			advertisedPorts:   []string{"java"},
			expectValid:       false,
			expectMessagePart: "internal",
		},
		{
			name: "cluster-local dns rejected",
			endpoint: &gameplanev1alpha1.GameServerEndpoint{
				Name: "java",
				Host: "myserver.default.svc.cluster.local",
				Port: 25565,
			},
			advertisedPorts:   []string{"java"},
			expectValid:       false,
			expectMessagePart: "cluster-local",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			valid, msg := validatePlayitEndpoint(tt.endpoint, tt.advertisedPorts)
			if valid != tt.expectValid {
				t.Errorf("expected valid=%v, got %v", tt.expectValid, valid)
			}
			if !tt.expectValid && !strings.Contains(msg, tt.expectMessagePart) {
				t.Errorf("expected error containing %q, got %q", tt.expectMessagePart, msg)
			}
		})
	}
}

func TestValidatePlayitEndpoints(t *testing.T) {
	tests := []struct {
		name            string
		endpoints       []gameplanev1alpha1.GameServerEndpoint
		advertisedPorts []string
		expectValid     []string // expected port names in valid result
		expectErrorPart string
	}{
		{
			name: "single valid endpoint",
			endpoints: []gameplanev1alpha1.GameServerEndpoint{
				{Name: "java", Host: "203.0.113.1", Port: 25565},
			},
			advertisedPorts: []string{"java"},
			expectValid:     []string{"java"},
			expectErrorPart: "",
		},
		{
			name: "multi-port happy path",
			endpoints: []gameplanev1alpha1.GameServerEndpoint{
				{Name: "java", Host: "203.0.113.1", Port: 25565},
				{Name: "bedrock", Host: "203.0.113.1", Port: 19133},
			},
			advertisedPorts: []string{"java", "bedrock"},
			expectValid:     []string{"java", "bedrock"},
			expectErrorPart: "",
		},
		{
			name: "duplicate port names",
			endpoints: []gameplanev1alpha1.GameServerEndpoint{
				{Name: "java", Host: "203.0.113.1", Port: 25565},
				{Name: "java", Host: "203.0.113.1", Port: 25566},
			},
			advertisedPorts: []string{"java"},
			expectValid:     []string{"java"},
			expectErrorPart: "duplicate",
		},
		{
			name: "invalid entry filtered out",
			endpoints: []gameplanev1alpha1.GameServerEndpoint{
				{Name: "java", Host: "203.0.113.1", Port: 25565},
				{Name: "query", Host: "10.0.0.1", Port: 25577},
			},
			advertisedPorts: []string{"java", "query"},
			expectValid:     []string{"java"},
			expectErrorPart: "private",
		},
		{
			name: "port name not advertised",
			endpoints: []gameplanev1alpha1.GameServerEndpoint{
				{Name: "java", Host: "203.0.113.1", Port: 25565},
				{Name: "rcon", Host: "203.0.113.1", Port: 25575},
			},
			advertisedPorts: []string{"java"},
			expectValid:     []string{"java"},
			expectErrorPart: "advertised",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			valid, errMsg := validatePlayitEndpoints(tt.endpoints, tt.advertisedPorts)
			if len(valid) != len(tt.expectValid) {
				t.Errorf("expected %d valid endpoints, got %d", len(tt.expectValid), len(valid))
			}
			for i, name := range tt.expectValid {
				if i >= len(valid) || valid[i].Name != name {
					t.Errorf("expected valid[%d].Name=%q", i, name)
				}
			}
			if tt.expectErrorPart != "" && !strings.Contains(errMsg, tt.expectErrorPart) {
				t.Errorf("expected error containing %q, got %q", tt.expectErrorPart, errMsg)
			}
			if tt.expectErrorPart == "" && errMsg != "" {
				t.Errorf("expected no error, got %q", errMsg)
			}
		})
	}
}

func TestGetAdvertisedPortNames(t *testing.T) {
	tmpl := &gameplanev1alpha1.GameTemplate{
		Spec: gameplanev1alpha1.GameTemplateSpec{
			Ports: []gameplanev1alpha1.GamePort{
				{
					Name:      "java",
					Advertise: true,
				},
				{
					Name:      "rcon",
					Advertise: false,
				},
				{
					Name:      "query",
					Advertise: true,
				},
			},
		},
	}

	got := getAdvertisedPortNames(tmpl)
	expected := []string{"java", "query"}

	if len(got) != len(expected) {
		t.Errorf("expected %d advertised ports, got %d", len(expected), len(got))
		return
	}

	for i, name := range expected {
		if got[i] != name {
			t.Errorf("expected port %d to be %q, got %q", i, name, got[i])
		}
	}
}

func TestGetAdvertisedPortNamesEmpty(t *testing.T) {
	tmpl := &gameplanev1alpha1.GameTemplate{
		Spec: gameplanev1alpha1.GameTemplateSpec{
			Ports: []gameplanev1alpha1.GamePort{
				{
					Name:      "java",
					Advertise: false,
				},
			},
		},
	}

	got := getAdvertisedPortNames(tmpl)
	if len(got) != 0 {
		t.Errorf("expected 0 advertised ports, got %d", len(got))
	}
}

// condByType indexes a condition list for the address-assignment tests below.
func condByType(conds []metav1.Condition, condType string) (metav1.Condition, bool) {
	for _, c := range conds {
		if c.Type == condType {
			return c, true
		}
	}
	return metav1.Condition{}, false
}

// lbService builds a LoadBalancer game Service carrying one port, optionally
// with an address already published by the cluster's address manager.
func lbService(ingressIP string) *corev1.Service {
	svc := &corev1.Service{
		Spec: corev1.ServiceSpec{
			Type:      corev1.ServiceTypeLoadBalancer,
			ClusterIP: "10.96.0.10",
			Ports: []corev1.ServicePort{
				{Name: "game", Port: 25565, Protocol: corev1.ProtocolTCP},
			},
		},
	}
	if ingressIP != "" {
		svc.Status.LoadBalancer.Ingress = []corev1.LoadBalancerIngress{{IP: ingressIP}}
	}
	return svc
}

// gsWithAddressRequest builds a GameServer asking for a pool and/or address
// under the given expose mode.
func gsWithAddressRequest(expose, pool, address string) *gameplanev1alpha1.GameServer {
	return &gameplanev1alpha1.GameServer{
		Spec: gameplanev1alpha1.GameServerSpec{
			Networking: gameplanev1alpha1.GameServerNetworking{
				Expose:      expose,
				AddressPool: pool,
				Address:     address,
			},
		},
	}
}

func TestEndpointsFromServicePool(t *testing.T) {
	cases := []struct {
		name     string
		svc      *corev1.Service
		plan     addressPlan
		wantHost string
		wantPool string
	}{
		{
			// The whole point of the field: a translated request that the
			// address manager has answered stamps the pool it was asked for.
			name:     "translated request with an assigned address stamps the pool",
			svc:      lbService("203.0.113.7"),
			plan:     addressPlan{Manager: addressManagerMetalLB, Pool: "games", Outcome: addressPlanTranslated},
			wantHost: "203.0.113.7",
			wantPool: "games",
		},
		{
			// Backward compatibility: an untouched server's endpoints must
			// look exactly as they did before the field existed.
			name:     "no request leaves the pool empty",
			svc:      lbService("203.0.113.7"),
			plan:     addressPlan{Manager: addressManagerMetalLB, Outcome: addressPlanNotRequested},
			wantHost: "203.0.113.7",
			wantPool: "",
		},
		{
			name:     "address-only request leaves the pool empty",
			svc:      lbService("203.0.113.7"),
			plan:     addressPlan{Manager: addressManagerMetalLB, Address: "203.0.113.7", Outcome: addressPlanTranslated},
			wantHost: "203.0.113.7",
			wantPool: "",
		},
		{
			// The ClusterIP fallback shown while assignment is pending was
			// not drawn from any pool, so it must not claim one.
			name:     "pending assignment falls back to the cluster IP with no pool",
			svc:      lbService(""),
			plan:     addressPlan{Manager: addressManagerCilium, Pool: "games", Outcome: addressPlanTranslated},
			wantHost: "10.96.0.10",
			wantPool: "",
		},
		{
			name:     "no address manager configured claims no pool",
			svc:      lbService("203.0.113.7"),
			plan:     addressPlan{Pool: "games", Outcome: addressPlanNoAddressManagerConfigured},
			wantHost: "203.0.113.7",
			wantPool: "",
		},
		{
			name:     "request ignored for the expose mode claims no pool",
			svc:      lbService("203.0.113.7"),
			plan:     addressPlan{Manager: addressManagerMetalLB, Pool: "games", Outcome: addressPlanIgnoredForExposureMode},
			wantHost: "203.0.113.7",
			wantPool: "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			eps := endpointsFromService(tc.svc, tc.plan)
			if len(eps) != 1 {
				t.Fatalf("got %d endpoints, want 1", len(eps))
			}
			if eps[0].Host != tc.wantHost {
				t.Errorf("Host = %q, want %q", eps[0].Host, tc.wantHost)
			}
			if eps[0].Pool != tc.wantPool {
				t.Errorf("Pool = %q, want %q", eps[0].Pool, tc.wantPool)
			}
		})
	}
}

func TestEndpointsFromServicePoolStampsEveryPort(t *testing.T) {
	svc := lbService("203.0.113.7")
	svc.Spec.Ports = append(svc.Spec.Ports,
		corev1.ServicePort{Name: "query", Port: 25565, Protocol: corev1.ProtocolUDP})
	plan := addressPlan{Manager: addressManagerCilium, Pool: "games", Outcome: addressPlanTranslated}

	eps := endpointsFromService(svc, plan)
	if len(eps) != 2 {
		t.Fatalf("got %d endpoints, want 2", len(eps))
	}
	for _, ep := range eps {
		if ep.Pool != "games" {
			t.Errorf("endpoint %q: Pool = %q, want %q", ep.Name, ep.Pool, "games")
		}
	}
}

func TestAddressAssignmentCondition(t *testing.T) {
	cases := []struct {
		name       string
		gs         *gameplanev1alpha1.GameServer
		plan       addressPlan
		svc        *corev1.Service
		wantStatus metav1.ConditionStatus
		wantReason string
		wantInMsg  string
	}{
		{
			name:       "assigned from a requested pool",
			gs:         gsWithAddressRequest("LoadBalancer", "games", ""),
			plan:       addressPlan{Manager: addressManagerMetalLB, Pool: "games", Outcome: addressPlanTranslated},
			svc:        lbService("203.0.113.7"),
			wantStatus: metav1.ConditionTrue,
			wantReason: "Assigned",
			wantInMsg:  "from pool 'games'",
		},
		{
			name:       "assigned for an address-only request",
			gs:         gsWithAddressRequest("LoadBalancer", "", "203.0.113.7"),
			plan:       addressPlan{Manager: addressManagerCilium, Address: "203.0.113.7", Outcome: addressPlanTranslated},
			svc:        lbService("203.0.113.7"),
			wantStatus: metav1.ConditionTrue,
			wantReason: "Assigned",
			wantInMsg:  "203.0.113.7",
		},
		{
			name:       "pending while the address manager has not answered",
			gs:         gsWithAddressRequest("LoadBalancer", "games", ""),
			plan:       addressPlan{Manager: addressManagerMetalLB, Pool: "games", Outcome: addressPlanTranslated},
			svc:        lbService(""),
			wantStatus: metav1.ConditionFalse,
			wantReason: "AssignmentPending",
			wantInMsg:  "pool 'games'",
		},
		{
			name:       "waiting for the Service to exist",
			gs:         gsWithAddressRequest("LoadBalancer", "games", ""),
			plan:       addressPlan{Manager: addressManagerMetalLB, Pool: "games", Outcome: addressPlanTranslated},
			svc:        nil,
			wantStatus: metav1.ConditionFalse,
			wantReason: "ServiceNotReady",
			wantInMsg:  "waiting for the LoadBalancer Service",
		},
		{
			name:       "ignored on a NodePort server",
			gs:         gsWithAddressRequest("NodePort", "games", ""),
			plan:       addressPlan{Manager: addressManagerMetalLB, Pool: "games", Outcome: addressPlanIgnoredForExposureMode},
			svc:        nil,
			wantStatus: metav1.ConditionFalse,
			wantReason: "IgnoredForExposureMode",
			wantInMsg:  "'NodePort'",
		},
		{
			// An unset Expose is ClusterIP; the message must name that
			// rather than an empty string.
			name:       "ignored on a default-expose server names ClusterIP",
			gs:         gsWithAddressRequest("", "games", ""),
			plan:       addressPlan{Manager: addressManagerMetalLB, Pool: "games", Outcome: addressPlanIgnoredForExposureMode},
			svc:        nil,
			wantStatus: metav1.ConditionFalse,
			wantReason: "IgnoredForExposureMode",
			wantInMsg:  "'ClusterIP'",
		},
		{
			// Flavor "none": the Service is untouched, but the request must
			// still be reported so it never looks silently honored.
			name:       "no address manager configured",
			gs:         gsWithAddressRequest("LoadBalancer", "games", "203.0.113.7"),
			plan:       addressPlan{Pool: "games", Address: "203.0.113.7", Outcome: addressPlanNoAddressManagerConfigured},
			svc:        lbService("198.51.100.4"),
			wantStatus: metav1.ConditionFalse,
			wantReason: "NoAddressManagerConfigured",
			wantInMsg:  "address '203.0.113.7' from pool 'games'",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			conds := addressAssignmentCondition(nil, tc.gs, tc.plan, tc.svc, addressFailureReason{}, "")
			got, ok := condByType(conds, gameplanev1alpha1.GameServerConditionAddressAssignment)
			if !ok {
				t.Fatalf("no %s condition", gameplanev1alpha1.GameServerConditionAddressAssignment)
			}
			if got.Status != tc.wantStatus {
				t.Errorf("Status = %q, want %q", got.Status, tc.wantStatus)
			}
			if got.Reason != tc.wantReason {
				t.Errorf("Reason = %q, want %q", got.Reason, tc.wantReason)
			}
			if !strings.Contains(got.Message, tc.wantInMsg) {
				t.Errorf("Message = %q, want it to contain %q", got.Message, tc.wantInMsg)
			}
		})
	}
}

// TestAddressAssignmentConditionAbsentWhenNotRequested is the backward-
// compatibility guard: every GameServer that predates this feature must keep
// exactly the conditions it had, so a server that asked for nothing carries no
// AddressAssignment condition at all — not a False one.
func TestAddressAssignmentConditionAbsentWhenNotRequested(t *testing.T) {
	gs := &gameplanev1alpha1.GameServer{}
	plan := planAddressPreference(gs, addressManagerMetalLB)

	conds := computeConditions(gs, gameplanev1alpha1.GameServerPhaseRunning, nil, idleAwake, plan, lbService("203.0.113.7"), addressFailureReason{}, "")
	if _, ok := condByType(conds, gameplanev1alpha1.GameServerConditionAddressAssignment); ok {
		t.Error("a server that requested no pool or address must carry no AddressAssignment condition")
	}
	// The conditions it does own are untouched.
	for _, want := range []string{"Ready", "Progressing", "Healthy"} {
		if _, ok := condByType(conds, want); !ok {
			t.Errorf("missing %s condition", want)
		}
	}
}

// TestAddressAssignmentConditionClearedOnRequestRemoval covers the other half
// of the same rule: clearing the spec fields drops the condition on the next
// reconcile rather than leaving a stale report behind.
func TestAddressAssignmentConditionClearedOnRequestRemoval(t *testing.T) {
	gs := &gameplanev1alpha1.GameServer{}
	gs.Status.Conditions = []metav1.Condition{
		{
			Type:               gameplanev1alpha1.GameServerConditionAddressAssignment,
			Status:             metav1.ConditionTrue,
			Reason:             "Assigned",
			LastTransitionTime: metav1.Now(),
		},
	}
	plan := planAddressPreference(gs, addressManagerMetalLB)

	conds := computeConditions(gs, gameplanev1alpha1.GameServerPhaseRunning, nil, idleAwake, plan, lbService("203.0.113.7"), addressFailureReason{}, "")
	if _, ok := condByType(conds, gameplanev1alpha1.GameServerConditionAddressAssignment); ok {
		t.Error("clearing the request must remove the stale AddressAssignment condition")
	}
}

func TestAddressRequestSummary(t *testing.T) {
	cases := []struct {
		name string
		plan addressPlan
		want string
	}{
		{"pool only", addressPlan{Pool: "games"}, "pool 'games'"},
		{"address only", addressPlan{Address: "203.0.113.7"}, "address '203.0.113.7'"},
		{
			"both", addressPlan{Pool: "games", Address: "203.0.113.7"},
			"address '203.0.113.7' from pool 'games'",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := addressRequestSummary(tc.plan); got != tc.want {
				t.Errorf("addressRequestSummary() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestLoadBalancerAddress(t *testing.T) {
	cases := []struct {
		name string
		svc  *corev1.Service
		want string
	}{
		{"nil service", nil, ""},
		{"not a load balancer", &corev1.Service{
			Spec: corev1.ServiceSpec{Type: corev1.ServiceTypeClusterIP, ClusterIP: "10.96.0.10"},
		}, ""},
		{"no ingress yet", lbService(""), ""},
		{"ingress ip", lbService("203.0.113.7"), "203.0.113.7"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := loadBalancerAddress(tc.svc); got != tc.want {
				t.Errorf("loadBalancerAddress() = %q, want %q", got, tc.want)
			}
		})
	}
}

// A cloud load balancer that publishes both wants players resolving the name.
func TestLoadBalancerAddressPrefersHostname(t *testing.T) {
	svc := lbService("203.0.113.7")
	svc.Status.LoadBalancer.Ingress[0].Hostname = "lb.example.com"
	if got := loadBalancerAddress(svc); got != "lb.example.com" {
		t.Errorf("loadBalancerAddress() = %q, want the hostname", got)
	}
}

// TestAddressAssignmentCondition_PoolNotFound_Mapping is unit coverage of the
// two-line struct-copy shape only — it hands addressAssignmentCondition a
// pre-built addressFailureReason, so it cannot catch a regression in how
// PoolNotFound actually gets DERIVED (that's the direct IPAddressPool GET in
// reconcileStatus). For derivation-level coverage — a missing pool driving
// the real reconcileStatus wiring end to end to this same Reason — see
// TestReconcileStatus_PoolNotFoundClearsWhenPoolCreated's first pass.
func TestAddressAssignmentCondition_PoolNotFound_Mapping(t *testing.T) {
	gs := &gameplanev1alpha1.GameServer{
		Spec: gameplanev1alpha1.GameServerSpec{
			Networking: gameplanev1alpha1.GameServerNetworking{
				Expose:      "LoadBalancer",
				AddressPool: "nonexistent",
			},
		},
		ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
	}
	plan := addressPlan{
		Manager: "metallb",
		Pool:    "nonexistent",
		Outcome: addressPlanTranslated,
	}
	eventFailure := addressFailureReason{
		reason:  "PoolNotFound",
		message: "Pool not found: nonexistent pool",
	}
	conds := addressAssignmentCondition(nil, gs, plan, nil, eventFailure, "")
	if len(conds) != 1 {
		t.Fatalf("expected 1 condition, got %d", len(conds))
	}
	if conds[0].Reason != "PoolNotFound" {
		t.Errorf("expected reason PoolNotFound, got %q", conds[0].Reason)
	}
	if conds[0].Status != metav1.ConditionFalse {
		t.Errorf("expected status False, got %q", conds[0].Status)
	}
}

// TestAddressAssignmentCondition_AllocationFailedPassthrough covers the
// pass-through shape: a MetalLB AllocationFailed failure surfaces as reason
// "AllocationFailed" with MetalLB's own allocator-error text carried verbatim
// into the condition message, rather than being mapped onto an invented enum
// like the old PoolExhausted. Unlike a hand-built addressFailureReason, the
// failure here is DERIVED from a real event fixture via
// extractAddressFailureFromEvents — the same verbatim MetalLB v0.14.9
// pool-exhausted fixture TestExtractAddressFailureFromEvents pins — so a
// regression in event parsing or in stripAllocationFailedPrefix's boilerplate
// trim actually fails this test, not just a regression in the two-line struct
// copy addressAssignmentCondition does with whatever it's handed.
func TestAddressAssignmentCondition_AllocationFailedPassthrough(t *testing.T) {
	gs := &gameplanev1alpha1.GameServer{
		Spec: gameplanev1alpha1.GameServerSpec{
			Networking: gameplanev1alpha1.GameServerNetworking{
				Expose:      "LoadBalancer",
				AddressPool: "pool-tiny",
			},
		},
		ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
	}
	plan := addressPlan{
		Manager: "metallb",
		Pool:    "pool-tiny",
		Outcome: addressPlanTranslated,
	}
	events := []corev1.Event{warnEvent("metallb-controller", "AllocationFailed",
		`Failed to allocate IP for "games/mc-1": no available IPs in pool pool-tiny for ipv4 IPFamily`)}
	failure := extractAddressFailureFromEvents(events)
	if failure.reason != "AllocationFailed" {
		t.Fatalf("extractAddressFailureFromEvents derived reason = %q, want AllocationFailed (test fixture is broken)", failure.reason)
	}
	conds := addressAssignmentCondition(nil, gs, plan, nil, failure, "")
	if len(conds) != 1 {
		t.Fatalf("expected 1 condition, got %d", len(conds))
	}
	if conds[0].Reason != "AllocationFailed" {
		t.Errorf("expected reason AllocationFailed, got %q", conds[0].Reason)
	}
	if conds[0].Message != "no available IPs in pool pool-tiny for ipv4 IPFamily" {
		t.Errorf("expected MetalLB's allocator text verbatim, got %q", conds[0].Message)
	}
}

func TestAddressAssignmentCondition_AddressInUse(t *testing.T) {
	gs := &gameplanev1alpha1.GameServer{
		Spec: gameplanev1alpha1.GameServerSpec{
			Networking: gameplanev1alpha1.GameServerNetworking{
				Expose:  "LoadBalancer",
				Address: "203.0.113.1",
			},
		},
		ObjectMeta: metav1.ObjectMeta{Name: "server-a", Namespace: "default"},
	}
	plan := addressPlan{
		Manager: "metallb",
		Address: "203.0.113.1",
		Outcome: addressPlanTranslated,
	}
	conds := addressAssignmentCondition(nil, gs, plan, nil, addressFailureReason{}, "server-b")
	if len(conds) != 1 {
		t.Fatalf("expected 1 condition, got %d", len(conds))
	}
	if conds[0].Reason != "AddressInUse" {
		t.Errorf("expected reason AddressInUse, got %q", conds[0].Reason)
	}
	if !strings.Contains(conds[0].Message, "server-b") {
		t.Errorf("expected message to mention conflicting server, got %q", conds[0].Message)
	}
}

func TestAddressAssignmentCondition_IgnoredForExposureMode(t *testing.T) {
	gs := &gameplanev1alpha1.GameServer{
		Spec: gameplanev1alpha1.GameServerSpec{
			Networking: gameplanev1alpha1.GameServerNetworking{
				Expose:      "ClusterIP",
				AddressPool: "public",
			},
		},
		ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
	}
	plan := addressPlan{
		Manager: "metallb",
		Pool:    "public",
		Outcome: addressPlanIgnoredForExposureMode,
	}
	conds := addressAssignmentCondition(nil, gs, plan, nil, addressFailureReason{}, "")
	if len(conds) != 1 {
		t.Fatalf("expected 1 condition, got %d", len(conds))
	}
	if conds[0].Reason != "IgnoredForExposureMode" {
		t.Errorf("expected reason IgnoredForExposureMode, got %q", conds[0].Reason)
	}
}

func TestAddressAssignmentCondition_NoAddressManagerConfigured(t *testing.T) {
	gs := &gameplanev1alpha1.GameServer{
		Spec: gameplanev1alpha1.GameServerSpec{
			Networking: gameplanev1alpha1.GameServerNetworking{
				Expose:      "LoadBalancer",
				AddressPool: "public",
			},
		},
		ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
	}
	plan := addressPlan{
		Manager: "none",
		Pool:    "public",
		Outcome: addressPlanNoAddressManagerConfigured,
	}
	conds := addressAssignmentCondition(nil, gs, plan, nil, addressFailureReason{}, "")
	if len(conds) != 1 {
		t.Fatalf("expected 1 condition, got %d", len(conds))
	}
	if conds[0].Reason != "NoAddressManagerConfigured" {
		t.Errorf("expected reason NoAddressManagerConfigured, got %q", conds[0].Reason)
	}
}

func TestAddressAssignmentCondition_Assigned(t *testing.T) {
	gs := &gameplanev1alpha1.GameServer{
		Spec: gameplanev1alpha1.GameServerSpec{
			Networking: gameplanev1alpha1.GameServerNetworking{
				Expose:      "LoadBalancer",
				AddressPool: "public",
			},
		},
		ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
	}
	plan := addressPlan{
		Manager: "metallb",
		Pool:    "public",
		Outcome: addressPlanTranslated,
	}
	svc := lbService("203.0.113.5")
	conds := addressAssignmentCondition(nil, gs, plan, svc, addressFailureReason{}, "")
	if len(conds) != 1 {
		t.Fatalf("expected 1 condition, got %d", len(conds))
	}
	if conds[0].Reason != "Assigned" {
		t.Errorf("expected reason Assigned, got %q", conds[0].Reason)
	}
	if conds[0].Status != metav1.ConditionTrue {
		t.Errorf("expected status True, got %q", conds[0].Status)
	}
}

// warnEvent builds a Warning Service event attributed to the given reporting
// component, mirroring the shape MetalLB-style managers emit.
func warnEvent(component, reason, message string) corev1.Event {
	return corev1.Event{
		Type:    corev1.EventTypeWarning,
		Reason:  reason,
		Message: message,
		Source:  corev1.EventSource{Component: component},
	}
}

// TestExtractAddressFailureFromEvents uses the OBSERVED MetalLB v0.14.9
// fixtures verbatim (measured on a real cluster, not researched from docs):
// every allocation failure emits Reason="AllocationFailed"
// Source.Component="metallb-controller" and a Message of the shape
// `Failed to allocate IP for "<ns>/<name>": <allocator error>`. This pins
// that the operator passes the allocator error straight through (prefix
// stripped) rather than trying to classify it into an invented enum, and
// that it never invents a reason for events it doesn't understand.
func TestExtractAddressFailureFromEvents(t *testing.T) {
	cases := []struct {
		name        string
		events      []corev1.Event
		wantReason  string
		wantMessage string
	}{
		{name: "no events at all"},
		{name: "empty slice", events: []corev1.Event{}},
		{
			name: "pool missing — verbatim MetalLB v0.14.9 fixture",
			events: []corev1.Event{warnEvent("metallb-controller", "AllocationFailed",
				`Failed to allocate IP for "games/mc-1": unknown pool "no-such-pool-here"`)},
			wantReason:  "AllocationFailed",
			wantMessage: `unknown pool "no-such-pool-here"`,
		},
		{
			name: "address outside pool config — verbatim MetalLB v0.14.9 fixture",
			events: []corev1.Event{warnEvent("metallb-controller", "AllocationFailed",
				`Failed to allocate IP for "games/mc-1": ["203.0.113.99"] is not allowed in config`)},
			wantReason:  "AllocationFailed",
			wantMessage: `["203.0.113.99"] is not allowed in config`,
		},
		{
			name: "pool exhausted — verbatim MetalLB v0.14.9 fixture",
			events: []corev1.Event{warnEvent("metallb-controller", "AllocationFailed",
				"Failed to allocate IP for \"games/mc-1\": no available IPs in pool pool-tiny for ipv4 IPFamily")},
			wantReason:  "AllocationFailed",
			wantMessage: "no available IPs in pool pool-tiny for ipv4 IPFamily",
		},
		{
			// The success shape — verbatim MetalLB v0.14.9 fixture — is a
			// Normal event, so Type alone excludes it before Reason is even
			// checked.
			name: "success event (Normal, IPAllocated) is not a failure",
			events: []corev1.Event{{
				Type:    corev1.EventTypeNormal,
				Reason:  "IPAllocated",
				Message: `Assigned IP ["203.0.113.7"]`,
				Source:  corev1.EventSource{Component: "metallb-controller"},
			}},
		},
		{
			name:   "warning from an unrelated component is ignored",
			events: []corev1.Event{warnEvent("kubelet", "FailedMount", "volume is already in use by another pod")},
		},
		{
			// cilium is no longer in addressManagerEventSources (it emits no
			// events at all — see the var's doc comment) so even a
			// perfectly-shaped AllocationFailed warning from it must not match.
			name:   "AllocationFailed from a non-address-manager component is ignored",
			events: []corev1.Event{warnEvent("cilium-operator", "AllocationFailed", "would never actually be emitted")},
		},
		{
			name:   "warning with no source at all is ignored",
			events: []corev1.Event{warnEvent("", "AllocationFailed", `Failed to allocate IP for "games/mc-1": unknown pool "x"`)},
		},
		{
			name:   "address-manager warning with a different Reason is ignored",
			events: []corev1.Event{warnEvent("metallb-speaker", "SomethingElse", "unrelated speaker problem")},
		},
		{
			name: "unrelated warning ahead of a real one does not mask it",
			events: []corev1.Event{
				warnEvent("kubelet", "BackOff", "restarting failed container"),
				warnEvent("metallb-controller", "AllocationFailed", `Failed to allocate IP for "games/mc-1": unknown pool "no-such-pool-here"`),
			},
			wantReason:  "AllocationFailed",
			wantMessage: `unknown pool "no-such-pool-here"`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := extractAddressFailureFromEvents(tc.events)
			if got.reason != tc.wantReason {
				t.Fatalf("reason = %q, want %q", got.reason, tc.wantReason)
			}
			if tc.wantReason == "" {
				if got.message != "" {
					t.Errorf("zero value expected, got message %q", got.message)
				}
				return
			}
			if got.message != tc.wantMessage {
				t.Errorf("message = %q, want %q (MetalLB's allocator text verbatim, prefix stripped)", got.message, tc.wantMessage)
			}
		})
	}
}

// TestExtractAddressFailureFromCiliumCondition tests the mapping of Cilium
// LB-IPAM condition shapes to our reason codes. Cilium's condition reasons are:
// - no_pool: the requested pool does not exist (maps to PoolNotFound)
// - out_of_ips: the pool is exhausted (maps to AllocationFailed)
//
// (The condition shape was observed against a live cluster in gameserver_status.go
// lines 419-425, but this test exercises the mapping logic, not a live cluster.)
// Unlike MetalLB (which uses Events), Cilium's failure signal is a Service
// status condition (cilium.io/IPAMRequestSatisfied=False). This test pins that
// the operator reads this condition, derives the right reason, and carries
// Cilium's own message verbatim (same philosophy as the MetalLB event path).
func TestExtractAddressFailureFromCiliumCondition(t *testing.T) {
	cases := []struct {
		name        string
		svc         *corev1.Service
		wantReason  string
		wantMessage string
	}{
		{name: "nil Service", svc: nil},
		{
			name: "no conditions at all",
			svc: &corev1.Service{
				ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "games"},
				Status:     corev1.ServiceStatus{},
			},
		},
		{
			name: "condition absent",
			svc: &corev1.Service{
				ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "games"},
				Status: corev1.ServiceStatus{
					Conditions: []metav1.Condition{{
						Type:   "SomeOtherCondition",
						Status: metav1.ConditionFalse,
					}},
				},
			},
		},
		{
			name: "condition satisfied (True)",
			svc: &corev1.Service{
				ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "games"},
				Status: corev1.ServiceStatus{
					Conditions: []metav1.Condition{{
						Type:    "cilium.io/IPAMRequestSatisfied",
						Status:  metav1.ConditionTrue,
						Message: "Address allocated",
					}},
				},
			},
		},
		{
			name: "no_pool reason — pool does not exist",
			svc: &corev1.Service{
				ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "games"},
				Status: corev1.ServiceStatus{
					Conditions: []metav1.Condition{{
						Type:    "cilium.io/IPAMRequestSatisfied",
						Status:  metav1.ConditionFalse,
						Reason:  "no_pool",
						Message: "load-balancer pool unknown-pool not found",
					}},
				},
			},
			wantReason:  "PoolNotFound",
			wantMessage: "load-balancer pool unknown-pool not found",
		},
		{
			name: "out_of_ips reason — pool is exhausted",
			svc: &corev1.Service{
				ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "games"},
				Status: corev1.ServiceStatus{
					Conditions: []metav1.Condition{{
						Type:    "cilium.io/IPAMRequestSatisfied",
						Status:  metav1.ConditionFalse,
						Reason:  "out_of_ips",
						Message: "no available IPs in pool public",
					}},
				},
			},
			wantReason:  "AllocationFailed",
			wantMessage: "no available IPs in pool public",
		},
		{
			name: "unknown reason — not a recognized Cilium reason",
			svc: &corev1.Service{
				ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "games"},
				Status: corev1.ServiceStatus{
					Conditions: []metav1.Condition{{
						Type:    "cilium.io/IPAMRequestSatisfied",
						Status:  metav1.ConditionFalse,
						Reason:  "unknown_reason",
						Message: "some other failure",
					}},
				},
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := extractAddressFailureFromCiliumCondition(tc.svc)
			if got.reason != tc.wantReason {
				t.Fatalf("reason = %q, want %q", got.reason, tc.wantReason)
			}
			if tc.wantReason == "" {
				if got.message != "" {
					t.Errorf("zero value expected, got message %q", got.message)
				}
				return
			}
			if got.message != tc.wantMessage {
				t.Errorf("message = %q, want %q (Cilium's condition message verbatim)", got.message, tc.wantMessage)
			}
		})
	}
}

// TestAddressAssignmentCondition_CiliumPoolNotFound covers mapping a Cilium
// no_pool condition reason to the PoolNotFound condition reason, carrying
// Cilium's message verbatim.
func TestAddressAssignmentCondition_CiliumPoolNotFound(t *testing.T) {
	gs := &gameplanev1alpha1.GameServer{
		Spec: gameplanev1alpha1.GameServerSpec{
			Networking: gameplanev1alpha1.GameServerNetworking{
				Expose:      "LoadBalancer",
				AddressPool: "nonexistent",
			},
		},
		ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
	}
	plan := addressPlan{
		Manager: addressManagerCilium,
		Pool:    "nonexistent",
		Outcome: addressPlanTranslated,
	}
	ciliumFailure := addressFailureReason{
		reason:  "PoolNotFound",
		message: "load-balancer pool nonexistent not found",
	}
	conds := addressAssignmentCondition(nil, gs, plan, nil, ciliumFailure, "")
	if len(conds) != 1 {
		t.Fatalf("expected 1 condition, got %d", len(conds))
	}
	if conds[0].Reason != "PoolNotFound" {
		t.Errorf("expected reason PoolNotFound, got %q", conds[0].Reason)
	}
	if conds[0].Status != metav1.ConditionFalse {
		t.Errorf("expected status False, got %q", conds[0].Status)
	}
	if conds[0].Message != "load-balancer pool nonexistent not found" {
		t.Errorf("expected Cilium's message verbatim, got %q", conds[0].Message)
	}
}

// TestAddressAssignmentCondition_CiliumAllocationFailed covers mapping a
// Cilium out_of_ips condition reason to the AllocationFailed condition
// reason, carrying Cilium's message verbatim.
func TestAddressAssignmentCondition_CiliumAllocationFailed(t *testing.T) {
	gs := &gameplanev1alpha1.GameServer{
		Spec: gameplanev1alpha1.GameServerSpec{
			Networking: gameplanev1alpha1.GameServerNetworking{
				Expose:      "LoadBalancer",
				AddressPool: "public",
			},
		},
		ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
	}
	plan := addressPlan{
		Manager: addressManagerCilium,
		Pool:    "public",
		Outcome: addressPlanTranslated,
	}
	ciliumFailure := addressFailureReason{
		reason:  "AllocationFailed",
		message: "no available IPs in pool public",
	}
	conds := addressAssignmentCondition(nil, gs, plan, nil, ciliumFailure, "")
	if len(conds) != 1 {
		t.Fatalf("expected 1 condition, got %d", len(conds))
	}
	if conds[0].Reason != "AllocationFailed" {
		t.Errorf("expected reason AllocationFailed, got %q", conds[0].Reason)
	}
	if conds[0].Status != metav1.ConditionFalse {
		t.Errorf("expected status False, got %q", conds[0].Status)
	}
	if conds[0].Message != "no available IPs in pool public" {
		t.Errorf("expected Cilium's message verbatim, got %q", conds[0].Message)
	}
}

// TestAddressAssignmentConditionLatch pins the fix for the oscillation bug
// this feature had during development: a previously-derived terminal
// AllocationFailed reason must survive a reconcile pass that finds no fresh
// failure signal — because a Kubernetes Event has a ~1h TTL and MetalLB does
// not re-emit AllocationFailed while the failure persists, so "no event this
// pass" is not evidence the problem cleared. Without the latch, the condition
// would revert to the generic AssignmentPending as soon as the event expired.
func TestAddressAssignmentConditionLatch(t *testing.T) {
	gs := gsWithAddressRequest("LoadBalancer", "no-such-pool-here", "")
	gs.Generation = 3
	prior := []metav1.Condition{{
		Type:               gameplanev1alpha1.GameServerConditionAddressAssignment,
		Status:             metav1.ConditionFalse,
		Reason:             "AllocationFailed",
		Message:            `unknown pool "no-such-pool-here"`,
		ObservedGeneration: 3,
		LastTransitionTime: metav1.Now(),
	}}
	plan := addressPlan{Manager: addressManagerMetalLB, Pool: "no-such-pool-here", Outcome: addressPlanTranslated}

	// This pass has NO fresh failure (the event has TTL'd out) and the
	// Service still carries no assigned address.
	conds := addressAssignmentCondition(prior, gs, plan, lbService(""), addressFailureReason{}, "")
	got, ok := condByType(conds, gameplanev1alpha1.GameServerConditionAddressAssignment)
	if !ok {
		t.Fatal("AddressAssignment condition disappeared")
	}
	if got.Reason != "AllocationFailed" {
		t.Errorf("Reason = %q, want the latched AllocationFailed to persist rather than revert to AssignmentPending", got.Reason)
	}
	if got.Message != `unknown pool "no-such-pool-here"` {
		t.Errorf("Message = %q, want the original message preserved verbatim", got.Message)
	}
}

// TestAddressAssignmentConditionLatchClearsOnAssignment covers the first of
// the latch's two release conditions: once the address manager actually
// assigns an address, the stale failure must stop winning.
func TestAddressAssignmentConditionLatchClearsOnAssignment(t *testing.T) {
	gs := gsWithAddressRequest("LoadBalancer", "games", "")
	gs.Generation = 3
	prior := []metav1.Condition{{
		Type:               gameplanev1alpha1.GameServerConditionAddressAssignment,
		Status:             metav1.ConditionFalse,
		Reason:             "AllocationFailed",
		Message:            "some earlier failure",
		ObservedGeneration: 3,
		LastTransitionTime: metav1.Now(),
	}}
	plan := addressPlan{Manager: addressManagerMetalLB, Pool: "games", Outcome: addressPlanTranslated}

	conds := addressAssignmentCondition(prior, gs, plan, lbService("203.0.113.9"), addressFailureReason{}, "")
	got, ok := condByType(conds, gameplanev1alpha1.GameServerConditionAddressAssignment)
	if !ok {
		t.Fatal("AddressAssignment condition disappeared")
	}
	if got.Reason != "Assigned" || got.Status != metav1.ConditionTrue {
		t.Errorf("Reason = %q Status = %q, want Assigned/True once the address manager actually succeeds", got.Reason, got.Status)
	}
}

// TestAddressAssignmentConditionLatchClearsOnRequestChange covers the second
// release condition: editing the request (which bumps Generation) must not
// keep reporting the old pool's failure — ObservedGeneration on the stale
// condition no longer matches gs.Generation.
func TestAddressAssignmentConditionLatchClearsOnRequestChange(t *testing.T) {
	gs := gsWithAddressRequest("LoadBalancer", "a-different-pool", "")
	gs.Generation = 4 // bumped by the spec edit since the latched condition was written
	prior := []metav1.Condition{{
		Type:               gameplanev1alpha1.GameServerConditionAddressAssignment,
		Status:             metav1.ConditionFalse,
		Reason:             "PoolNotFound",
		Message:            `IPAddressPool "old-pool" not found in namespace "metallb-system".`,
		ObservedGeneration: 3,
		LastTransitionTime: metav1.Now(),
	}}
	plan := addressPlan{Manager: addressManagerMetalLB, Pool: "a-different-pool", Outcome: addressPlanTranslated}

	conds := addressAssignmentCondition(prior, gs, plan, lbService(""), addressFailureReason{}, "")
	got, ok := condByType(conds, gameplanev1alpha1.GameServerConditionAddressAssignment)
	if !ok {
		t.Fatal("AddressAssignment condition disappeared")
	}
	if got.Reason == "PoolNotFound" {
		t.Errorf("Reason = %q, want the stale reason to clear once the request changed (Generation bumped)", got.Reason)
	}
}

// TestAddressAssignmentConditionLatchClearsOnConfirmedPoolFound covers the
// case unique to PoolNotFound (as opposed to AllocationFailed): unlike an
// event, the direct IPAddressPool GET never goes stale, so a fresh, definite
// "it exists now" answer must release a latched PoolNotFound immediately —
// it does not need to wait for the request to change or the address to be
// assigned, because confirmedClear is exactly that live, authoritative
// signal (see addressFailureReason.confirmedClear).
func TestAddressAssignmentConditionLatchClearsOnConfirmedPoolFound(t *testing.T) {
	gs := gsWithAddressRequest("LoadBalancer", "games", "")
	gs.Generation = 3
	prior := []metav1.Condition{{
		Type:               gameplanev1alpha1.GameServerConditionAddressAssignment,
		Status:             metav1.ConditionFalse,
		Reason:             "PoolNotFound",
		Message:            `IPAddressPool "games" not found in namespace "metallb-system".`,
		ObservedGeneration: 3,
		LastTransitionTime: metav1.Now(),
	}}
	plan := addressPlan{Manager: addressManagerMetalLB, Pool: "games", Outcome: addressPlanTranslated}

	conds := addressAssignmentCondition(prior, gs, plan, lbService(""), addressFailureReason{confirmedClear: true}, "")
	got, ok := condByType(conds, gameplanev1alpha1.GameServerConditionAddressAssignment)
	if !ok {
		t.Fatal("AddressAssignment condition disappeared")
	}
	if got.Reason == "PoolNotFound" {
		t.Errorf("Reason = %q, want confirmedClear to release the stale PoolNotFound latch immediately", got.Reason)
	}
	if got.Reason != "AssignmentPending" {
		t.Errorf("Reason = %q, want AssignmentPending (waiting for the address manager) now that the pool is confirmed to exist", got.Reason)
	}
}

// TestAddressAssignmentConditionLatch_ConfirmedClearDoesNotReleaseAllocationFailed
// pins the DEFECT 1 fix: confirmedClear is a live "the pool exists" signal,
// which definitively disproves a latched PoolNotFound (see the test above)
// but says NOTHING about whether an exhausted/misconfigured pool
// (AllocationFailed) has cleared — a pool can exist and still be full. Before
// the fix, reconcileStatus set confirmedClear for ANY metallb pool-requesting
// server whose pool exists, and the latch consumed it regardless of which
// reason was latched — so a latched AllocationFailed got released the very
// next pass purely because the pool it names still exists, oscillating with
// AssignmentPending forever. See gameserver_status.go's LATCHING doc comment
// and addressFailureReason.confirmedClear.
func TestAddressAssignmentConditionLatch_ConfirmedClearDoesNotReleaseAllocationFailed(t *testing.T) {
	gs := gsWithAddressRequest("LoadBalancer", "pool-tiny", "")
	gs.Generation = 3
	prior := []metav1.Condition{{
		Type:               gameplanev1alpha1.GameServerConditionAddressAssignment,
		Status:             metav1.ConditionFalse,
		Reason:             "AllocationFailed",
		Message:            "no available IPs in pool pool-tiny for ipv4 IPFamily",
		ObservedGeneration: 3,
		LastTransitionTime: metav1.Now(),
	}}
	plan := addressPlan{Manager: addressManagerMetalLB, Pool: "pool-tiny", Outcome: addressPlanTranslated}

	// No fresh failure this pass (the AllocationFailed event TTL'd out), but
	// the pool itself was confirmed to exist by the direct IPAddressPool GET
	// — exactly the state reconcileStatus produces once the event expires
	// while the pool is merely exhausted, not missing.
	conds := addressAssignmentCondition(prior, gs, plan, lbService(""), addressFailureReason{confirmedClear: true}, "")
	got, ok := condByType(conds, gameplanev1alpha1.GameServerConditionAddressAssignment)
	if !ok {
		t.Fatal("AddressAssignment condition disappeared")
	}
	if got.Reason != "AllocationFailed" {
		t.Errorf("Reason = %q, want the latched AllocationFailed to survive — confirmedClear only disproves a latched PoolNotFound, not AllocationFailed", got.Reason)
	}
	if got.Message != "no available IPs in pool pool-tiny for ipv4 IPFamily" {
		t.Errorf("Message = %q, want the original AllocationFailed message preserved verbatim", got.Message)
	}
}

// newTestGameServerReconciler builds a GameServerReconciler backed by a fake
// client, for tests that need to drive reconcileStatus itself (the real
// production wiring) rather than calling the pure addressAssignmentCondition
// function directly.
func newTestGameServerReconciler(t *testing.T, objs ...client.Object) *GameServerReconciler {
	t.Helper()
	s := testScheme(t)
	b := fake.NewClientBuilder().WithScheme(s).WithStatusSubresource(&gameplanev1alpha1.GameServer{})
	if len(objs) > 0 {
		b = b.WithObjects(objs...)
	}
	return &GameServerReconciler{Client: b.Build(), Scheme: s, AddressManager: addressManagerMetalLB}
}

// metalLBIPAddressPoolFixture builds the unstructured IPAddressPool object
// checkMetalLBPoolExists GETs — see gameserver_controller.go's
// metalLBIPAddressPoolGVK.
func metalLBIPAddressPoolFixture(namespace, name string) *unstructured.Unstructured {
	u := &unstructured.Unstructured{}
	u.SetGroupVersionKind(metalLBIPAddressPoolGVK)
	u.SetNamespace(namespace)
	u.SetName(name)
	return u
}

// TestReconcileStatus_AllocationFailedSurvivesEventExpiry drives the ACTUAL
// reconcileStatus wiring (not the pure addressAssignmentCondition function —
// see DEFECT 2 in the review that produced this test) through the exact
// two-pass sequence that used to oscillate:
//
//   - Pass N: the game Service carries a fresh AllocationFailed event (the
//     pool exists but is exhausted). reconcileStatus must record
//     AllocationFailed.
//   - Pass N+1: the event is gone (as if its ~1h TTL had expired), but the
//     pool STILL exists — checkMetalLBPoolExists again reports Found, so
//     reconcileStatus computes confirmedClear=true. Before the DEFECT 1 fix
//     this unconditionally released the latch (regardless of which reason
//     was latched) and the condition reverted to AssignmentPending. The fix
//     scopes confirmedClear's release to a latched PoolNotFound only, so an
//     AllocationFailed latch must survive here.
func TestReconcileStatus_AllocationFailedSurvivesEventExpiry(t *testing.T) {
	gs := &gameplanev1alpha1.GameServer{
		ObjectMeta: metav1.ObjectMeta{Name: "smp", Namespace: "games", Generation: 1},
		Spec: gameplanev1alpha1.GameServerSpec{
			Networking: gameplanev1alpha1.GameServerNetworking{
				Expose:      "LoadBalancer",
				AddressPool: "pool-tiny",
			},
		},
	}
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: "smp", Namespace: "games"},
		Spec: corev1.ServiceSpec{
			Type:  corev1.ServiceTypeLoadBalancer,
			Ports: []corev1.ServicePort{{Name: "game", Port: 25565, Protocol: corev1.ProtocolTCP}},
		},
	}
	pool := metalLBIPAddressPoolFixture("metallb-system", "pool-tiny")
	r := newTestGameServerReconciler(t, gs, svc, pool)
	ctx := context.Background()

	// Pass N: a fresh AllocationFailed event for the exhausted pool.
	events := []corev1.Event{warnEvent("metallb-controller", "AllocationFailed",
		`Failed to allocate IP for "games/smp": no available IPs in pool pool-tiny for ipv4 IPFamily`)}
	if _, err := r.reconcileStatus(ctx, gs, idleAwake, nil, nil, tunnelPlan{}, nil, events, ""); err != nil {
		t.Fatalf("reconcileStatus (pass N): %v", err)
	}
	cond, ok := condByType(gs.Status.Conditions, gameplanev1alpha1.GameServerConditionAddressAssignment)
	if !ok || cond.Reason != "AllocationFailed" {
		t.Fatalf("pass N: Reason = %+v, want AllocationFailed", cond)
	}

	// Pass N+1: re-fetch (as the reconciler would) — the event is gone, the
	// pool is still there. This is the exact pass that used to destroy the
	// diagnosis.
	var refetched gameplanev1alpha1.GameServer
	if err := r.Get(ctx, types.NamespacedName{Name: "smp", Namespace: "games"}, &refetched); err != nil {
		t.Fatalf("re-fetch: %v", err)
	}
	if _, err := r.reconcileStatus(ctx, &refetched, idleAwake, nil, nil, tunnelPlan{}, nil, nil, ""); err != nil {
		t.Fatalf("reconcileStatus (pass N+1): %v", err)
	}

	var persisted gameplanev1alpha1.GameServer
	if err := r.Get(ctx, types.NamespacedName{Name: "smp", Namespace: "games"}, &persisted); err != nil {
		t.Fatalf("re-fetch persisted: %v", err)
	}
	cond, ok = condByType(persisted.Status.Conditions, gameplanev1alpha1.GameServerConditionAddressAssignment)
	if !ok {
		t.Fatal("pass N+1: AddressAssignment condition disappeared")
	}
	if cond.Reason != "AllocationFailed" {
		t.Errorf("pass N+1: Reason = %q, want the latched AllocationFailed to persist (this is DEFECT 1's oscillation — reverting here means the real diagnosis was destroyed)", cond.Reason)
	}
}

// TestReconcileStatus_PoolNotFoundClearsWhenPoolCreated covers the release
// path confirmedClear IS meant for: a latched PoolNotFound must clear, via
// reconcileStatus's real wiring, once the pool is actually created — without
// waiting for the address to be assigned or the request to change.
func TestReconcileStatus_PoolNotFoundClearsWhenPoolCreated(t *testing.T) {
	gs := &gameplanev1alpha1.GameServer{
		ObjectMeta: metav1.ObjectMeta{Name: "smp", Namespace: "games", Generation: 1},
		Spec: gameplanev1alpha1.GameServerSpec{
			Networking: gameplanev1alpha1.GameServerNetworking{
				Expose:      "LoadBalancer",
				AddressPool: "games-pool",
			},
		},
	}
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: "smp", Namespace: "games"},
		Spec: corev1.ServiceSpec{
			Type:  corev1.ServiceTypeLoadBalancer,
			Ports: []corev1.ServicePort{{Name: "game", Port: 25565, Protocol: corev1.ProtocolTCP}},
		},
	}
	// No IPAddressPool object seeded yet — checkMetalLBPoolExists must
	// report poolExistenceMissing this pass.
	r := newTestGameServerReconciler(t, gs, svc)
	ctx := context.Background()

	if _, err := r.reconcileStatus(ctx, gs, idleAwake, nil, nil, tunnelPlan{}, nil, nil, ""); err != nil {
		t.Fatalf("reconcileStatus (pool missing): %v", err)
	}
	cond, ok := condByType(gs.Status.Conditions, gameplanev1alpha1.GameServerConditionAddressAssignment)
	if !ok || cond.Reason != "PoolNotFound" {
		t.Fatalf("pool-missing pass: Reason = %+v, want PoolNotFound", cond)
	}

	// Create the pool, exactly as an admin fixing the misconfiguration
	// would, then re-fetch and reconcile again.
	pool := metalLBIPAddressPoolFixture("metallb-system", "games-pool")
	if err := r.Create(ctx, pool); err != nil {
		t.Fatalf("create pool: %v", err)
	}
	var refetched gameplanev1alpha1.GameServer
	if err := r.Get(ctx, types.NamespacedName{Name: "smp", Namespace: "games"}, &refetched); err != nil {
		t.Fatalf("re-fetch: %v", err)
	}
	if _, err := r.reconcileStatus(ctx, &refetched, idleAwake, nil, nil, tunnelPlan{}, nil, nil, ""); err != nil {
		t.Fatalf("reconcileStatus (pool created): %v", err)
	}
	cond, ok = condByType(refetched.Status.Conditions, gameplanev1alpha1.GameServerConditionAddressAssignment)
	if !ok {
		t.Fatal("AddressAssignment condition disappeared")
	}
	if cond.Reason == "PoolNotFound" {
		t.Errorf("Reason = %q, want the latch released now that the pool was actually created", cond.Reason)
	}
	if cond.Reason != "AssignmentPending" {
		t.Errorf("Reason = %q, want AssignmentPending (waiting on the address manager) now that the pool exists but no address is assigned yet", cond.Reason)
	}
}

// TestGameContainerStartedAt pins that uptime tracks the game container of
// the current pod, selected by name rather than by position.
func TestGameContainerStartedAt(t *testing.T) {
	started := metav1.NewTime(time.Date(2026, 10, 4, 21, 36, 8, 0, time.UTC))
	gs := &gameplanev1alpha1.GameServer{ObjectMeta: metav1.ObjectMeta{Name: "mc", Namespace: "games"}}
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "mc-0", Namespace: "games"},
		Status: corev1.PodStatus{ContainerStatuses: []corev1.ContainerStatus{
			{Name: "agent", State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{StartedAt: metav1.NewTime(started.Add(-time.Hour))}}},
			{Name: gameContainerName, State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{StartedAt: started}}},
		}},
	}
	r := newTestGameServerReconciler(t, pod)
	if got := r.gameContainerStartedAt(t.Context(), gs); got == nil || !got.Equal(&started) {
		t.Fatalf("gameContainerStartedAt = %v, want %v", got, started)
	}
	if got := newTestGameServerReconciler(t).gameContainerStartedAt(t.Context(), gs); got != nil {
		t.Fatalf("without a pod: gameContainerStartedAt = %v, want nil", got)
	}
}
