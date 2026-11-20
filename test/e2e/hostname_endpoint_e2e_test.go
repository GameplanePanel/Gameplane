//go:build e2e

package e2e

import (
	"context"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// TestAddressPool_HostnameHolderKeepsAddress — a pool-assigned LoadBalancer
// server with spec.networking.hostname reports the hostname as each
// endpoint's host and its load-balancer IP as serviceAddress. It still holds
// that IP: a second server requesting it is refused with AddressInUse naming
// the holder, as it would be if the holder showed its IP.
func TestAddressPool_HostnameHolderKeepsAddress(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	ns := "gameplane-games"
	suffix := time.Now().UnixNano()
	tmplName := fmt.Sprintf("e2e-hostname-%d", suffix)
	holderName := fmt.Sprintf("e2e-gs-hostname-holder-%d", suffix)
	contenderName := fmt.Sprintf("e2e-gs-hostname-contender-%d", suffix)
	hostname := holderName + ".games.example.com"

	applyBusyboxTemplate(t, tmplName)

	createServer := func(name string, networking map[string]any) {
		t.Helper()
		gs := &unstructured.Unstructured{Object: map[string]any{
			"apiVersion": "gameplane.local/v1alpha1",
			"kind":       "GameServer",
			"metadata":   map[string]any{"name": name, "namespace": ns},
			"spec": map[string]any{
				"templateRef": map[string]any{"name": tmplName},
				"networking":  networking,
			},
		}}
		if _, err := envInstance.Dyn.Resource(gameServerGVR).Namespace(ns).
			Create(ctx, gs, metav1.CreateOptions{}); err != nil && !apierrors.IsAlreadyExists(err) {
			t.Fatalf("create gameserver %s: %v", name, err)
		}
		t.Cleanup(func() {
			_ = envInstance.Dyn.Resource(gameServerGVR).Namespace(ns).
				Delete(context.WithoutCancel(ctx), name, metav1.DeleteOptions{})
		})
	}

	// Step 1: the holder gets an address from the pool and advertises its
	// hostname, keeping the address as serviceAddress.
	createServer(holderName, map[string]any{
		"expose":      "LoadBalancer",
		"addressPool": "pool-us-west",
		"hostname":    hostname,
	})
	var heldAddr string
	envInstance.Eventually(t, 120*time.Second, func() (bool, string) {
		svc, err := envInstance.K8s.CoreV1().Services(ns).Get(ctx, holderName, metav1.GetOptions{})
		if err != nil {
			return false, "get holder service: " + err.Error()
		}
		if len(svc.Status.LoadBalancer.Ingress) == 0 || svc.Status.LoadBalancer.Ingress[0].IP == "" {
			return false, "holder service has no load-balancer IP yet"
		}
		lbIP := svc.Status.LoadBalancer.Ingress[0].IP

		got, err := envInstance.Dyn.Resource(gameServerGVR).Namespace(ns).
			Get(ctx, holderName, metav1.GetOptions{})
		if err != nil {
			return false, "get holder gameserver: " + err.Error()
		}
		endpoints, _, _ := unstructured.NestedSlice(got.Object, "status", "endpoints")
		if len(endpoints) == 0 {
			return false, "holder has no endpoints"
		}
		for i, epIface := range endpoints {
			ep, ok := epIface.(map[string]any)
			if !ok {
				return false, fmt.Sprintf("endpoints[%d] malformed", i)
			}
			if host, _ := ep["host"].(string); host != hostname {
				return false, fmt.Sprintf("endpoints[%d].host = %q, want the hostname %q", i, host, hostname)
			}
			if addr, _ := ep["serviceAddress"].(string); addr != lbIP {
				return false, fmt.Sprintf("endpoints[%d].serviceAddress = %q, want the load-balancer IP %q", i, addr, lbIP)
			}
		}
		heldAddr = lbIP
		return true, ""
	})
	if net.ParseIP(heldAddr) == nil {
		t.Fatalf("held address %q is not an IP", heldAddr)
	}

	// Step 2: a second server asks for the holder's IP and must be refused,
	// with the holder named.
	createServer(contenderName, map[string]any{
		"expose":      "LoadBalancer",
		"addressPool": "pool-us-west",
		"address":     heldAddr,
	})
	envInstance.Eventually(t, 60*time.Second, func() (bool, string) {
		got, err := envInstance.Dyn.Resource(gameServerGVR).Namespace(ns).
			Get(ctx, contenderName, metav1.GetOptions{})
		if err != nil {
			return false, "get contender gameserver: " + err.Error()
		}
		cond := findCondition(got.Object, "AddressAssignment")
		if cond == nil {
			return false, "contender AddressAssignment condition not found"
		}
		reason, _ := cond["reason"].(string)
		status, _ := cond["status"].(string)
		message, _ := cond["message"].(string)
		if status != "False" || reason != "AddressInUse" {
			return false, fmt.Sprintf("contender status=%q reason=%q, want False/AddressInUse", status, reason)
		}
		if !strings.Contains(message, holderName) {
			return false, fmt.Sprintf("message=%q does not name the holder %q", message, holderName)
		}
		return true, ""
	})
}
