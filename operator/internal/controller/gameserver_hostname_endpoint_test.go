package controller

import (
	"context"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	gameplanev1alpha1 "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
)

func udpLBService(ingress ...corev1.LoadBalancerIngress) *corev1.Service {
	return &corev1.Service{
		Spec: corev1.ServiceSpec{
			Type:      corev1.ServiceTypeLoadBalancer,
			ClusterIP: "10.0.0.5",
			Ports: []corev1.ServicePort{
				{Name: "game", Port: 2456, Protocol: corev1.ProtocolUDP},
				{Name: "query", Port: 2457, Protocol: corev1.ProtocolUDP},
			},
		},
		Status: corev1.ServiceStatus{LoadBalancer: corev1.LoadBalancerStatus{Ingress: ingress}},
	}
}

func TestReconcileStatus_NodePortPlayerAddress(t *testing.T) {
	const clusterIP = "10.43.178.205"
	for _, tc := range []struct {
		name, hostname, selectedApp, nodeIP, wantHost string
		svcType                                       corev1.ServiceType
	}{
		{"configured hostname wins", "mc.example.org", "gameplane-game", "192.0.2.10", "mc.example.org", corev1.ServiceTypeNodePort},
		{"scheduled game node", "", "gameplane-game", "192.0.2.10", "192.0.2.10", corev1.ServiceTypeNodePort},
		{"sleeping service selects the waker", "", sentinelValue, "192.0.2.10", "192.0.2.20", corev1.ServiceTypeNodePort},
		{"no external IP", "", "gameplane-game", "", "", corev1.ServiceTypeNodePort},
		{"external IP in a different address family", "", "gameplane-game", "2001:db8::10", "", corev1.ServiceTypeNodePort},
		{"no selected pod", "", "unmatched", "192.0.2.10", "", corev1.ServiceTypeNodePort},
		{"selected node lost contact", "", "lost", "192.0.2.10", "", corev1.ServiceTypeNodePort},
		{"missing service clears previous ports", "mc.example.org", "", "", "", ""},
		{"old cluster service clears previous ports", "mc.example.org", "", "", "", corev1.ServiceTypeClusterIP},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			gs := &gameplanev1alpha1.GameServer{
				ObjectMeta: metav1.ObjectMeta{Name: "minecraft", Namespace: "games"},
				Spec: gameplanev1alpha1.GameServerSpec{Networking: gameplanev1alpha1.GameServerNetworking{
					Expose: "NodePort", Hostname: tc.hostname, Tunnel: &gameplanev1alpha1.GameServerTunnel{Provider: "frp"},
				}},
				Status: gameplanev1alpha1.GameServerStatus{Endpoints: []gameplanev1alpha1.GameServerEndpoint{
					{Name: "java", Host: clusterIP, Port: 25565},
				}},
			}
			labels := func(role string) map[string]string {
				return map[string]string{"app.kubernetes.io/name": role, "app.kubernetes.io/instance": gs.Name}
			}
			pod := func(name, role, node string) *corev1.Pod {
				return &corev1.Pod{
					ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: gs.Namespace, Labels: labels(role)},
					Spec:       corev1.PodSpec{NodeName: node},
					Status:     corev1.PodStatus{Phase: corev1.PodRunning, HostIP: "10.0.0.8"},
				}
			}
			node := func(name, addr string, ready corev1.ConditionStatus) *corev1.Node {
				return &corev1.Node{
					ObjectMeta: metav1.ObjectMeta{Name: name},
					Status: corev1.NodeStatus{
						Conditions: []corev1.NodeCondition{{Type: corev1.NodeReady, Status: ready}},
						Addresses: []corev1.NodeAddress{
							{Type: corev1.NodeInternalIP, Address: "10.0.0.8"}, {Type: corev1.NodeExternalIP, Address: addr},
						},
					},
				}
			}
			scheme := testScheme(t)
			waker := pod("minecraft-waker-new", sentinelValue, "worker-waker")
			waker.Status.Conditions = []corev1.PodCondition{{Type: corev1.PodReady, Status: corev1.ConditionTrue}}
			builder := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(gs).WithObjects(gs,
				pod("minecraft-0", "gameplane-game", "worker-game"), waker, pod("lost-pod", "lost", "worker-lost"),
				pod("minecraft-waker-earlier", sentinelValue, "worker-game"))
			nodeReader := fake.NewClientBuilder().WithScheme(scheme).WithObjects(
				node("worker-game", tc.nodeIP, corev1.ConditionTrue), node("worker-waker", "192.0.2.20", corev1.ConditionTrue),
				node("worker-lost", "192.0.2.30", corev1.ConditionUnknown)).Build()
			if tc.svcType != "" {
				builder.WithObjects(&corev1.Service{
					ObjectMeta: metav1.ObjectMeta{Name: gs.Name, Namespace: gs.Namespace},
					Spec: corev1.ServiceSpec{
						Type: tc.svcType, ClusterIP: clusterIP, IPFamilies: []corev1.IPFamily{corev1.IPv4Protocol},
						Selector: labels(tc.selectedApp), Ports: []corev1.ServicePort{
							{Name: "java", Protocol: corev1.ProtocolTCP, Port: 25565, NodePort: 31493},
							{Name: "query", Protocol: corev1.ProtocolUDP, Port: 25566},
						},
					},
				})
			}
			cl := builder.Build()
			r := &GameServerReconciler{Client: cl, APIReader: nodeReader, Scheme: scheme}
			if err := cl.Get(ctx, client.ObjectKeyFromObject(gs), gs); err != nil {
				t.Fatal(err)
			}
			relay := gameplanev1alpha1.GameServerEndpoint{Name: "java", Host: "relay.example.org", Port: 31000, TunnelProvider: "frp"}
			tunnel := tunnelPlan{wantTunnel: true, endpoints: []gameplanev1alpha1.GameServerEndpoint{relay}}
			if _, err := r.reconcileStatus(ctx, gs, idleAwake, nil, nil, tunnel, nil, nil, ""); err != nil {
				t.Fatal(err)
			}
			if err := cl.Get(ctx, client.ObjectKeyFromObject(gs), gs); err != nil {
				t.Fatal(err)
			}
			got := gs.Status.Endpoints
			if len(got) == 0 || got[0] != relay {
				t.Fatalf("expected tunnel first, got %+v", got)
			}
			if tc.svcType != corev1.ServiceTypeNodePort {
				if len(got) != 1 {
					t.Errorf("expected only tunnel before NodePort Service exists, got %+v", got)
				}
				return
			}
			want := gameplanev1alpha1.GameServerEndpoint{Name: "java", Host: tc.wantHost, Port: 31493, Protocol: corev1.ProtocolTCP, ServiceAddress: clusterIP}
			if len(got) != 2 || got[1] != want {
				t.Errorf("expected allocated NodePort %+v after tunnel, got %+v", want, got)
			}
		})
	}
}

func TestEndpointsFromService_AdvertisedHostnameReplacesTheLoadBalancerAddress(t *testing.T) {
	svc := udpLBService(corev1.LoadBalancerIngress{IP: "192.0.2.10"})
	got := endpointsFromService(svc, addressPlan{}, "valheim.example.com")
	if len(got) != 2 {
		t.Fatalf("got %d endpoints, want 2", len(got))
	}
	for _, ep := range got {
		if ep.Host != "valheim.example.com" {
			t.Errorf("%s: host %q, want the hostname", ep.Name, ep.Host)
		}
	}
	if got[1].Port != 2457 {
		t.Errorf("ports must be kept: got %d, want 2457", got[1].Port)
	}
	for _, ep := range got {
		if ep.ServiceAddress != "192.0.2.10" {
			t.Errorf("%s: serviceAddress %q, want the load balancer IP", ep.Name, ep.ServiceAddress)
		}
	}
}

func TestEndpointsFromService_AdvertisedHostnameWaitsForLoadBalancerAddress(t *testing.T) {
	svc := udpLBService()
	got := endpointsFromService(svc, addressPlan{}, "valheim.example.com")
	if got[0].Host != "10.0.0.5" {
		t.Errorf("host %q, want the cluster IP while no address is published", got[0].Host)
	}
}

func TestEndpointsFromService_ClusterIPIgnoresHostname(t *testing.T) {
	svc := udpLBService(corev1.LoadBalancerIngress{IP: "192.0.2.10"})
	svc.Spec.Type = corev1.ServiceTypeClusterIP
	got := endpointsFromService(svc, addressPlan{}, "valheim.example.com")
	if got[0].Host != svc.Spec.ClusterIP {
		t.Errorf("host %q, want the Service's cluster IP", got[0].Host)
	}
}

func TestEndpointsFromService_LoadBalancerWithoutAdvertisedHostname(t *testing.T) {
	svc := udpLBService(corev1.LoadBalancerIngress{IP: "192.0.2.10"})
	got := endpointsFromService(svc, addressPlan{}, "")
	if got[0].Host != "192.0.2.10" {
		t.Errorf("host %q, want the load balancer IP", got[0].Host)
	}
}

// advertisingHolder is a LoadBalancer server holding addr, with the
// endpoints reconcileStatus writes for it: its advertised Host and assigned
// ServiceAddress differ.
func advertisingHolder(name, addr, requested string, created metav1.Time) *gameplanev1alpha1.GameServer {
	svc := udpLBService(corev1.LoadBalancerIngress{IP: addr})
	return &gameplanev1alpha1.GameServer{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "games", CreationTimestamp: created},
		Spec: gameplanev1alpha1.GameServerSpec{Networking: gameplanev1alpha1.GameServerNetworking{
			Expose: "LoadBalancer", Address: requested, Hostname: name + ".example.com",
		}},
		Status: gameplanev1alpha1.GameServerStatus{
			Endpoints: endpointsFromService(svc, addressPlan{}, name+".example.com"),
		},
	}
}

// TestFindAddressConflict_PoolHolderWithHostname — a pool-assigned holder
// that advertises a hostname still holds its load-balancer IP, so a server
// requesting that IP is told who has it.
func TestFindAddressConflict_PoolHolderWithHostname(t *testing.T) {
	s := testScheme(t)
	now := metav1.Now()
	holder := advertisingHolder("beta", "192.0.2.10", "", metav1.NewTime(now.Add(-10*time.Second)))
	if holder.Status.Endpoints[0].Host == "192.0.2.10" {
		t.Fatal("setup: the holder's Host should be its hostname")
	}
	requester := &gameplanev1alpha1.GameServer{
		ObjectMeta: metav1.ObjectMeta{Name: "alpha", Namespace: "games", CreationTimestamp: now},
		Spec: gameplanev1alpha1.GameServerSpec{Networking: gameplanev1alpha1.GameServerNetworking{
			Expose: "LoadBalancer", Address: "192.0.2.10",
		}},
	}
	cl := fake.NewClientBuilder().WithScheme(s).WithObjects(holder, requester).Build()
	r := &GameServerReconciler{Client: cl, APIReader: cl, Scheme: s}

	conflict, err := r.findAddressConflict(context.Background(), requester)
	if err != nil {
		t.Fatalf("findAddressConflict: %v", err)
	}
	if conflict != "beta" {
		t.Errorf("want conflict 'beta', got %q", conflict)
	}
}

// TestFindAddressConflict_ExplicitHolderWithHostnameKeepsPriority — an
// explicit holder that advertises a hostname keeps its priority over an
// older server requesting the same address: the holder reports no conflict,
// and the requester is the one told the address is taken.
func TestFindAddressConflict_ExplicitHolderWithHostnameKeepsPriority(t *testing.T) {
	s := testScheme(t)
	now := metav1.Now()
	holder := advertisingHolder("beta", "192.0.2.10", "192.0.2.10", now)
	older := &gameplanev1alpha1.GameServer{
		ObjectMeta: metav1.ObjectMeta{
			Name: "alpha", Namespace: "games", CreationTimestamp: metav1.NewTime(now.Add(-10 * time.Second)),
		},
		Spec: gameplanev1alpha1.GameServerSpec{Networking: gameplanev1alpha1.GameServerNetworking{
			Expose: "LoadBalancer", Address: "192.0.2.10",
		}},
	}
	cl := fake.NewClientBuilder().WithScheme(s).WithObjects(holder, older).Build()
	r := &GameServerReconciler{Client: cl, APIReader: cl, Scheme: s}

	conflict, err := r.findAddressConflict(context.Background(), holder)
	if err != nil {
		t.Fatalf("findAddressConflict(holder): %v", err)
	}
	if conflict != "" {
		t.Errorf("holder: want no conflict, got %q", conflict)
	}
	conflict, err = r.findAddressConflict(context.Background(), older)
	if err != nil {
		t.Fatalf("findAddressConflict(older): %v", err)
	}
	if conflict != "beta" {
		t.Errorf("older requester: want conflict 'beta', got %q", conflict)
	}
}

// TestCandidateHoldsAddress_FallsBackToHost — status written before
// serviceAddress existed has only Host, which was then always the Service's
// address, so it still counts.
func TestCandidateHoldsAddress_FallsBackToHost(t *testing.T) {
	gs := &gameplanev1alpha1.GameServer{
		Spec: gameplanev1alpha1.GameServerSpec{Networking: gameplanev1alpha1.GameServerNetworking{Expose: "LoadBalancer"}},
		Status: gameplanev1alpha1.GameServerStatus{Endpoints: []gameplanev1alpha1.GameServerEndpoint{
			{Name: "game", Host: "192.0.2.10", Port: 2456},
		}},
	}
	if !candidateHoldsAddress(gs, "192.0.2.10") {
		t.Error("an endpoint with only Host must still hold its address")
	}
	gs.Status.Endpoints[0].ServiceAddress = "192.0.2.11"
	if candidateHoldsAddress(gs, "192.0.2.10") {
		t.Error("serviceAddress, when set, decides what is held, not Host")
	}
}
