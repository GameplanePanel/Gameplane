package controller

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
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
