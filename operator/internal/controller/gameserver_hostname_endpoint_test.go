package controller

import (
	"context"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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

func TestWithAdvertisedHostname_ReplacesTheLoadBalancerAddress(t *testing.T) {
	svc := udpLBService(corev1.LoadBalancerIngress{IP: "192.0.2.10"})
	got := withAdvertisedHostname(endpointsFromService(svc, addressPlan{}), svc, "valheim.example.com")
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

func TestWithAdvertisedHostname_WaitsForAnAddress(t *testing.T) {
	svc := udpLBService()
	got := withAdvertisedHostname(endpointsFromService(svc, addressPlan{}), svc, "valheim.example.com")
	if got[0].Host != "10.0.0.5" {
		t.Errorf("host %q, want the cluster IP while no address is published", got[0].Host)
	}
}

func TestWithAdvertisedHostname_LeavesOtherServicesAlone(t *testing.T) {
	for _, svcType := range []corev1.ServiceType{corev1.ServiceTypeClusterIP, corev1.ServiceTypeNodePort} {
		svc := udpLBService(corev1.LoadBalancerIngress{IP: "192.0.2.10"})
		svc.Spec.Type = svcType
		got := withAdvertisedHostname(endpointsFromService(svc, addressPlan{}), svc, "valheim.example.com")
		if got[0].Host == "valheim.example.com" {
			t.Errorf("%s: hostname reported, want the Service's own address", svcType)
		}
	}
}

func TestWithAdvertisedHostname_NoHostnameIsUnchanged(t *testing.T) {
	svc := udpLBService(corev1.LoadBalancerIngress{IP: "192.0.2.10"})
	got := withAdvertisedHostname(endpointsFromService(svc, addressPlan{}), svc, "")
	if got[0].Host != "192.0.2.10" {
		t.Errorf("host %q, want the load balancer IP", got[0].Host)
	}
}

// advertisingHolder is a LoadBalancer server holding addr, with the
// endpoints reconcileStatus writes for it: built by endpointsFromService
// and withAdvertisedHostname, so Host is its hostname, not addr.
func advertisingHolder(name, addr, requested string, created metav1.Time) *gameplanev1alpha1.GameServer {
	svc := udpLBService(corev1.LoadBalancerIngress{IP: addr})
	return &gameplanev1alpha1.GameServer{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "games", CreationTimestamp: created},
		Spec: gameplanev1alpha1.GameServerSpec{Networking: gameplanev1alpha1.GameServerNetworking{
			Expose: "LoadBalancer", Address: requested, Hostname: name + ".example.com",
		}},
		Status: gameplanev1alpha1.GameServerStatus{
			Endpoints: withAdvertisedHostname(endpointsFromService(svc, addressPlan{}), svc, name+".example.com"),
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
