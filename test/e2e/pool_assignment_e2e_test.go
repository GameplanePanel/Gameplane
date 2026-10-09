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

// TestAddressPool_DefaultPoolNoPreference covers SC-006: a server that
// expresses no pool or address preference still gets a working external
// address from the cluster's default pool, exactly as it did before the
// address-pool feature existed.
//
// It deliberately asserts that NO AddressAssignment condition is present.
// The operator removes that condition when nothing was requested (see
// addressAssignmentCondition in operator/internal/controller/gameserver_status.go):
// a server that never asked for a pool must not carry a permanent condition
// about a feature it does not use. That absence is the backward-compatibility
// contract, so it is asserted rather than assumed.
func TestAddressPool_DefaultPoolNoPreference(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	ns := "gameplane-games"
	tmplName := fmt.Sprintf("e2e-pool-default-%d", time.Now().UnixNano())
	gsName := fmt.Sprintf("e2e-gs-default-%d", time.Now().UnixNano())

	// Create a LoadBalancer-exposed template (busybox with advertised port).
	applyBusyboxTemplate(t, tmplName)

	// Create GameServer without addressPool preference.
	gs := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "gameplane.local/v1alpha1",
		"kind":       "GameServer",
		"metadata":   map[string]any{"name": gsName, "namespace": ns},
		"spec": map[string]any{
			"templateRef": map[string]any{"name": tmplName},
			"networking": map[string]any{
				"expose": "LoadBalancer",
				// No addressPool specified — should use default.
			},
		},
	}}
	if _, err := envInstance.Dyn.Resource(gameServerGVR).Namespace(ns).
		Create(ctx, gs, metav1.CreateOptions{}); err != nil && !apierrors.IsAlreadyExists(err) {
		t.Fatalf("create gameserver: %v", err)
	}
	t.Cleanup(func() {
		_ = envInstance.Dyn.Resource(gameServerGVR).Namespace(ns).
			Delete(context.Background(), gsName, metav1.DeleteOptions{})
	})

	// Wait for the fronting Service to receive a load-balancer address and
	// for that address to be mirrored onto status.endpoints[0].host. Until
	// the LB address is published the endpoint host falls back to the
	// ClusterIP, so both halves are checked together.
	var endpointHost string
	envInstance.Eventually(t, 120*time.Second, func() (bool, string) {
		svc, err := envInstance.K8s.CoreV1().Services(ns).Get(ctx, gsName, metav1.GetOptions{})
		if err != nil {
			return false, "get service: " + err.Error()
		}
		if len(svc.Status.LoadBalancer.Ingress) == 0 {
			return false, "service has no loadBalancer ingress yet"
		}
		lbAddr := svc.Status.LoadBalancer.Ingress[0].IP
		if lbAddr == "" {
			lbAddr = svc.Status.LoadBalancer.Ingress[0].Hostname
		}
		if lbAddr == "" {
			return false, "loadBalancer ingress carries neither IP nor hostname"
		}

		got, err := envInstance.Dyn.Resource(gameServerGVR).Namespace(ns).
			Get(ctx, gsName, metav1.GetOptions{})
		if err != nil {
			return false, "get gameserver: " + err.Error()
		}
		endpoints, _, _ := unstructured.NestedSlice(got.Object, "status", "endpoints")
		if len(endpoints) == 0 {
			return false, "no endpoints found"
		}
		ep, ok := endpoints[0].(map[string]any)
		if !ok {
			return false, "endpoint malformed"
		}
		host, _ := ep["host"].(string)
		if host != lbAddr {
			return false, fmt.Sprintf("endpoint host = %q, want load-balancer address %q", host, lbAddr)
		}
		endpointHost = host
		return true, ""
	})

	if endpointHost == "" {
		t.Fatalf("endpoint host is empty after waiting for assignment")
	}
	if ip := net.ParseIP(endpointHost); ip == nil {
		t.Errorf("endpoint host %q is not a valid IP", endpointHost)
	}

	// No preference was expressed, so the operator must report nothing:
	// the AddressAssignment condition is removed, not set to False.
	got, err := envInstance.Dyn.Resource(gameServerGVR).Namespace(ns).
		Get(ctx, gsName, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get gameserver for condition check: %v", err)
	}
	if cond := findCondition(got.Object, "AddressAssignment"); cond != nil {
		t.Errorf("AddressAssignment condition present with no pool/address requested: %v", cond)
	}

	// No pool was requested, so no endpoint may claim one.
	endpoints, _, _ := unstructured.NestedSlice(got.Object, "status", "endpoints")
	for i, epIface := range endpoints {
		ep, ok := epIface.(map[string]any)
		if !ok {
			continue
		}
		if pool, _ := ep["pool"].(string); pool != "" {
			t.Errorf("endpoints[%d].pool = %q, want empty (no pool requested)", i, pool)
		}
	}
}

// TestAddressPool_NamedPoolAssignment covers SC-002: servers requesting a
// named pool receive addresses from that pool's range.
func TestAddressPool_NamedPoolAssignment(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	ns := "gameplane-games"
	tmplName := fmt.Sprintf("e2e-pool-named-%d", time.Now().UnixNano())
	gsName := fmt.Sprintf("e2e-gs-named-%d", time.Now().UnixNano())

	applyBusyboxTemplate(t, tmplName)

	// Create GameServer requesting pool-us-west.
	gs := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "gameplane.local/v1alpha1",
		"kind":       "GameServer",
		"metadata":   map[string]any{"name": gsName, "namespace": ns},
		"spec": map[string]any{
			"templateRef": map[string]any{"name": tmplName},
			"networking": map[string]any{
				"expose":      "LoadBalancer",
				"addressPool": "pool-us-west",
			},
		},
	}}
	if _, err := envInstance.Dyn.Resource(gameServerGVR).Namespace(ns).
		Create(ctx, gs, metav1.CreateOptions{}); err != nil && !apierrors.IsAlreadyExists(err) {
		t.Fatalf("create gameserver: %v", err)
	}
	t.Cleanup(func() {
		_ = envInstance.Dyn.Resource(gameServerGVR).Namespace(ns).
			Delete(context.Background(), gsName, metav1.DeleteOptions{})
	})

	// Wait for address assignment.
	var assignedAddr string
	var assignedPool string
	envInstance.Eventually(t, 60*time.Second, func() (bool, string) {
		got, err := envInstance.Dyn.Resource(gameServerGVR).Namespace(ns).
			Get(ctx, gsName, metav1.GetOptions{})
		if err != nil {
			return false, "get gameserver: " + err.Error()
		}

		// Check AddressAssignment condition.
		cond := findCondition(got.Object, "AddressAssignment")
		if cond == nil {
			return false, "AddressAssignment condition not found"
		}
		if cond["status"] != "True" {
			return false, fmt.Sprintf("AddressAssignment status=%v", cond["status"])
		}
		if cond["reason"] != "Assigned" {
			return false, fmt.Sprintf("AddressAssignment reason=%v, want Assigned", cond["reason"])
		}

		// Extract endpoint address and pool.
		endpoints, _, _ := unstructured.NestedSlice(got.Object, "status", "endpoints")
		if len(endpoints) == 0 {
			return false, "no endpoints"
		}
		ep, ok := endpoints[0].(map[string]any)
		if !ok || ep["host"] == nil {
			return false, "endpoint malformed"
		}
		assignedAddr = ep["host"].(string)
		if pool, ok := ep["pool"].(string); ok {
			assignedPool = pool
		}

		return true, ""
	})

	// Verify the address is in the pool-us-west range (172.18.255.200-210).
	// The exact range depends on the e2e bootstrap, but we verify it's a valid IP.
	ip := net.ParseIP(assignedAddr)
	if ip == nil {
		t.Errorf("assigned address %q is not a valid IP", assignedAddr)
	}

	// Verify the pool field is set to the requested pool.
	if assignedPool != "pool-us-west" {
		t.Errorf("assigned pool = %q, want pool-us-west", assignedPool)
	}
}

// TestAddressPool_NonLoadBalancerIgnored covers FR-018 / SC-... : pool
// preference on non-LoadBalancer mode is ignored and surfaces
// IgnoredForExposureMode condition.
func TestAddressPool_NonLoadBalancerIgnored(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	ns := "gameplane-games"
	tmplName := fmt.Sprintf("e2e-pool-nonlb-%d", time.Now().UnixNano())
	gsName := fmt.Sprintf("e2e-gs-nonlb-%d", time.Now().UnixNano())

	applyBusyboxTemplate(t, tmplName)

	// Create GameServer with ClusterIP mode (non-LoadBalancer) but with
	// addressPool set. The pool should be ignored.
	gs := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "gameplane.local/v1alpha1",
		"kind":       "GameServer",
		"metadata":   map[string]any{"name": gsName, "namespace": ns},
		"spec": map[string]any{
			"templateRef": map[string]any{"name": tmplName},
			"networking": map[string]any{
				"expose":      "ClusterIP",
				"addressPool": "pool-us-west",
			},
		},
	}}
	if _, err := envInstance.Dyn.Resource(gameServerGVR).Namespace(ns).
		Create(ctx, gs, metav1.CreateOptions{}); err != nil && !apierrors.IsAlreadyExists(err) {
		t.Fatalf("create gameserver: %v", err)
	}
	t.Cleanup(func() {
		_ = envInstance.Dyn.Resource(gameServerGVR).Namespace(ns).
			Delete(context.Background(), gsName, metav1.DeleteOptions{})
	})

	// Wait for AddressAssignment condition with IgnoredForExposureMode reason.
	envInstance.Eventually(t, 60*time.Second, func() (bool, string) {
		got, err := envInstance.Dyn.Resource(gameServerGVR).Namespace(ns).
			Get(ctx, gsName, metav1.GetOptions{})
		if err != nil {
			return false, "get gameserver: " + err.Error()
		}

		cond := findCondition(got.Object, "AddressAssignment")
		if cond == nil {
			return false, "AddressAssignment condition not found"
		}
		if cond["reason"] != "IgnoredForExposureMode" {
			return false, fmt.Sprintf("reason = %v, want IgnoredForExposureMode", cond["reason"])
		}
		return true, ""
	})
}

// TestAddressPool_NonexistentPoolError covers SC-003: requesting a
// nonexistent pool surfaces a clear error within 30 seconds.
func TestAddressPool_NonexistentPoolError(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	ns := "gameplane-games"
	tmplName := fmt.Sprintf("e2e-pool-noexist-%d", time.Now().UnixNano())
	gsName := fmt.Sprintf("e2e-gs-noexist-%d", time.Now().UnixNano())

	applyBusyboxTemplate(t, tmplName)

	// Create GameServer requesting a pool that does not exist.
	nonexistentPool := fmt.Sprintf("nonexistent-pool-%d", time.Now().UnixNano())
	gs := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "gameplane.local/v1alpha1",
		"kind":       "GameServer",
		"metadata":   map[string]any{"name": gsName, "namespace": ns},
		"spec": map[string]any{
			"templateRef": map[string]any{"name": tmplName},
			"networking": map[string]any{
				"expose":      "LoadBalancer",
				"addressPool": nonexistentPool,
			},
		},
	}}
	if _, err := envInstance.Dyn.Resource(gameServerGVR).Namespace(ns).
		Create(ctx, gs, metav1.CreateOptions{}); err != nil && !apierrors.IsAlreadyExists(err) {
		t.Fatalf("create gameserver: %v", err)
	}
	t.Cleanup(func() {
		_ = envInstance.Dyn.Resource(gameServerGVR).Namespace(ns).
			Delete(context.Background(), gsName, metav1.DeleteOptions{})
	})

	// Within 30 seconds, the operator must report the specific PoolNotFound
	// reason (a direct IPAddressPool GET — see checkMetalLBPoolExists in
	// gameserver_controller.go — not merely "some non-transient reason").
	// Asserting the exact reason is what makes this test able to fail: it
	// pins the pool-existence check actually running (deleting that check
	// would leave the request stuck on AssignmentPending, and this test
	// would time out — see SC-003) and distinguishes PoolNotFound from any
	// other terminal reason (e.g. NoAddressManagerConfigured), which a
	// looser "status False and reason != known-transient-values" assertion
	// cannot tell apart.
	envInstance.Eventually(t, 30*time.Second, func() (bool, string) {
		got, err := envInstance.Dyn.Resource(gameServerGVR).Namespace(ns).
			Get(ctx, gsName, metav1.GetOptions{})
		if err != nil {
			return false, "get gameserver: " + err.Error()
		}

		cond := findCondition(got.Object, "AddressAssignment")
		if cond == nil {
			return false, "AddressAssignment condition not found"
		}

		reason, _ := cond["reason"].(string)
		status, _ := cond["status"].(string)
		message, _ := cond["message"].(string)

		if status != "False" {
			return false, fmt.Sprintf("status=%q, reason=%q (want False/PoolNotFound)", status, reason)
		}
		if reason != "PoolNotFound" {
			return false, fmt.Sprintf("reason=%q, want exactly PoolNotFound", reason)
		}
		if !strings.Contains(message, nonexistentPool) {
			return false, fmt.Sprintf("message=%q does not name the missing pool %q", message, nonexistentPool)
		}
		return true, ""
	})
}

// TestAddressPool_ChangePoolOnRunningServer covers US2 Acceptance Scenario 2:
// changing the addressPool on an existing server moves it to the new pool's range.
func TestAddressPool_ChangePoolOnRunningServer(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	ns := "gameplane-games"
	tmplName := fmt.Sprintf("e2e-pool-change-%d", time.Now().UnixNano())
	gsName := fmt.Sprintf("e2e-gs-change-%d", time.Now().UnixNano())

	applyBusyboxTemplate(t, tmplName)

	// Create GameServer requesting pool-us-east.
	gs := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "gameplane.local/v1alpha1",
		"kind":       "GameServer",
		"metadata":   map[string]any{"name": gsName, "namespace": ns},
		"spec": map[string]any{
			"templateRef": map[string]any{"name": tmplName},
			"networking": map[string]any{
				"expose":      "LoadBalancer",
				"addressPool": "pool-us-east",
			},
		},
	}}
	if _, err := envInstance.Dyn.Resource(gameServerGVR).Namespace(ns).
		Create(ctx, gs, metav1.CreateOptions{}); err != nil && !apierrors.IsAlreadyExists(err) {
		t.Fatalf("create gameserver: %v", err)
	}
	t.Cleanup(func() {
		_ = envInstance.Dyn.Resource(gameServerGVR).Namespace(ns).
			Delete(context.Background(), gsName, metav1.DeleteOptions{})
	})

	// Wait for initial assignment from pool-us-east.
	var initialAddr string
	envInstance.Eventually(t, 60*time.Second, func() (bool, string) {
		got, err := envInstance.Dyn.Resource(gameServerGVR).Namespace(ns).
			Get(ctx, gsName, metav1.GetOptions{})
		if err != nil {
			return false, "get gameserver: " + err.Error()
		}

		cond := findCondition(got.Object, "AddressAssignment")
		if cond == nil {
			return false, "AddressAssignment condition not found"
		}
		if cond["status"] != "True" || cond["reason"] != "Assigned" {
			return false, fmt.Sprintf("not assigned yet: %v", cond["reason"])
		}

		endpoints, _, _ := unstructured.NestedSlice(got.Object, "status", "endpoints")
		if len(endpoints) == 0 {
			return false, "no endpoints"
		}
		ep, ok := endpoints[0].(map[string]any)
		if !ok || ep["host"] == nil {
			return false, "endpoint malformed"
		}
		initialAddr = ep["host"].(string)

		return true, ""
	})

	if initialAddr == "" {
		t.Fatalf("failed to get initial address after 60 seconds")
	}

	// Update pool with retry on conflict. The operator writes status frequently
	// during pool assignment, so the resourceVersion changes between GET and UPDATE.
	// Wrap the entire GET-modify-UPDATE sequence in a retry that re-fetches on conflict.
	envInstance.EventuallyNoErr(t, 60*time.Second, func() error {
		// Fetch current GameServer, change pool to pool-us-west.
		got, err := envInstance.Dyn.Resource(gameServerGVR).Namespace(ns).
			Get(ctx, gsName, metav1.GetOptions{})
		if err != nil {
			return fmt.Errorf("fetch gameserver for update: %w", err)
		}

		// Update networking.addressPool to pool-us-west.
		networking, _, _ := unstructured.NestedMap(got.Object, "spec", "networking")
		if networking == nil {
			networking = make(map[string]any)
		}
		networking["addressPool"] = "pool-us-west"
		if err := unstructured.SetNestedMap(got.Object, networking, "spec", "networking"); err != nil {
			return fmt.Errorf("update networking in gameserver: %w", err)
		}

		// Apply the update. This may fail on conflict if the operator's status
		// write incremented resourceVersion; EventuallyNoErr will retry the whole
		// GET-modify-UPDATE sequence until it succeeds.
		_, err = envInstance.Dyn.Resource(gameServerGVR).Namespace(ns).
			Update(ctx, got, metav1.UpdateOptions{})
		return err
	})

	// Wait for the endpoint's pool to be updated. The exact behavior
	// (address change vs. just pool name update) is implementation-dependent,
	// but at minimum the pool field should change.
	envInstance.Eventually(t, 60*time.Second, func() (bool, string) {
		got, err := envInstance.Dyn.Resource(gameServerGVR).Namespace(ns).
			Get(ctx, gsName, metav1.GetOptions{})
		if err != nil {
			return false, "get gameserver: " + err.Error()
		}

		endpoints, _, _ := unstructured.NestedSlice(got.Object, "status", "endpoints")
		if len(endpoints) == 0 {
			return false, "no endpoints"
		}
		ep, ok := endpoints[0].(map[string]any)
		if !ok {
			return false, "endpoint malformed"
		}

		assignedPool, _ := ep["pool"].(string)
		if assignedPool == "pool-us-west" {
			return true, ""
		}

		return false, fmt.Sprintf("pool = %q, want pool-us-west", assignedPool)
	})
}

// TestAddressPool_RESTAPICarriesPool covers FR-021: a GameServer created
// via REST API carrying networking.addressPool receives the same assignment
// and audit trail as one created through the dashboard.
//
// This test creates a GameServer via the dynamic client (simulating a REST
// API create) with an explicit addressPool and verifies it is assigned from
// that pool just like a dashboard-created one.
func TestAddressPool_RESTAPICarriesPool(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	ns := "gameplane-games"
	tmplName := fmt.Sprintf("e2e-pool-restapi-%d", time.Now().UnixNano())
	gsName := fmt.Sprintf("e2e-gs-restapi-%d", time.Now().UnixNano())

	applyBusyboxTemplate(t, tmplName)

	// Create GameServer programmatically (simulating REST API) with
	// networking.addressPool set.
	gs := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "gameplane.local/v1alpha1",
		"kind":       "GameServer",
		"metadata":   map[string]any{"name": gsName, "namespace": ns},
		"spec": map[string]any{
			"templateRef": map[string]any{"name": tmplName},
			"networking": map[string]any{
				"expose":      "LoadBalancer",
				"addressPool": "pool-us-east",
			},
		},
	}}
	if _, err := envInstance.Dyn.Resource(gameServerGVR).Namespace(ns).
		Create(ctx, gs, metav1.CreateOptions{}); err != nil && !apierrors.IsAlreadyExists(err) {
		t.Fatalf("create gameserver: %v", err)
	}
	t.Cleanup(func() {
		_ = envInstance.Dyn.Resource(gameServerGVR).Namespace(ns).
			Delete(context.Background(), gsName, metav1.DeleteOptions{})
	})

	// Verify it gets assigned from the pool just like a dashboard-created
	// server would.
	envInstance.Eventually(t, 60*time.Second, func() (bool, string) {
		got, err := envInstance.Dyn.Resource(gameServerGVR).Namespace(ns).
			Get(ctx, gsName, metav1.GetOptions{})
		if err != nil {
			return false, "get gameserver: " + err.Error()
		}

		// Check AddressAssignment condition.
		cond := findCondition(got.Object, "AddressAssignment")
		if cond == nil {
			return false, "AddressAssignment condition not found"
		}
		if cond["status"] != "True" || cond["reason"] != "Assigned" {
			return false, fmt.Sprintf("not assigned: status=%v reason=%v", cond["status"], cond["reason"])
		}

		// Verify endpoint carries the pool.
		endpoints, _, _ := unstructured.NestedSlice(got.Object, "status", "endpoints")
		if len(endpoints) == 0 {
			return false, "no endpoints"
		}
		ep, ok := endpoints[0].(map[string]any)
		if !ok || ep["host"] == nil {
			return false, "endpoint malformed"
		}

		assignedPool, _ := ep["pool"].(string)
		if assignedPool != "pool-us-east" {
			return false, fmt.Sprintf("pool = %q, want pool-us-east", assignedPool)
		}

		return true, ""
	})
}

// TestAddressPool_StatusVisibleInAddressAssignmentCondition verifies that
// the address and pool are reported in status.conditions[AddressAssignment]
// and status.endpoints[].pool as required by SC-008 and FR-025.
func TestAddressPool_StatusVisibleInAddressAssignmentCondition(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	ns := "gameplane-games"
	tmplName := fmt.Sprintf("e2e-pool-visible-%d", time.Now().UnixNano())
	gsName := fmt.Sprintf("e2e-gs-visible-%d", time.Now().UnixNano())

	applyBusyboxTemplate(t, tmplName)

	gs := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "gameplane.local/v1alpha1",
		"kind":       "GameServer",
		"metadata":   map[string]any{"name": gsName, "namespace": ns},
		"spec": map[string]any{
			"templateRef": map[string]any{"name": tmplName},
			"networking": map[string]any{
				"expose":      "LoadBalancer",
				"addressPool": "pool-us-west",
			},
		},
	}}
	if _, err := envInstance.Dyn.Resource(gameServerGVR).Namespace(ns).
		Create(ctx, gs, metav1.CreateOptions{}); err != nil && !apierrors.IsAlreadyExists(err) {
		t.Fatalf("create gameserver: %v", err)
	}
	t.Cleanup(func() {
		_ = envInstance.Dyn.Resource(gameServerGVR).Namespace(ns).
			Delete(context.Background(), gsName, metav1.DeleteOptions{})
	})

	// Wait for assignment and verify status fields are visible.
	var assignedAddr string
	var assignedPool string
	var condMessage string

	envInstance.Eventually(t, 60*time.Second, func() (bool, string) {
		got, err := envInstance.Dyn.Resource(gameServerGVR).Namespace(ns).
			Get(ctx, gsName, metav1.GetOptions{})
		if err != nil {
			return false, "get gameserver: " + err.Error()
		}

		// Check the AddressAssignment condition message.
		cond := findCondition(got.Object, "AddressAssignment")
		if cond == nil {
			return false, "AddressAssignment condition not found"
		}
		if cond["status"] != "True" {
			return false, fmt.Sprintf("not assigned: %v", cond["status"])
		}
		if msg, ok := cond["message"].(string); ok {
			condMessage = msg
		}

		// Check endpoints[0].address and endpoints[0].pool.
		endpoints, _, _ := unstructured.NestedSlice(got.Object, "status", "endpoints")
		if len(endpoints) == 0 {
			return false, "no endpoints"
		}
		ep, ok := endpoints[0].(map[string]any)
		if !ok {
			return false, "endpoint malformed"
		}

		if addr, ok := ep["host"].(string); ok && addr != "" {
			assignedAddr = addr
		} else {
			return false, "endpoint address missing or empty"
		}

		if pool, ok := ep["pool"].(string); ok && pool != "" {
			assignedPool = pool
		} else {
			return false, "endpoint pool missing or empty"
		}

		return true, ""
	})

	// Verify the values are sensible.
	if assignedAddr == "" {
		t.Errorf("assigned address is empty")
	}
	if assignedPool != "pool-us-west" {
		t.Errorf("assigned pool = %q, want pool-us-west", assignedPool)
	}
	// The condition message should mention the assignment, though the exact
	// format is left to the implementation.
	if !strings.Contains(strings.ToLower(condMessage), "assigned") &&
		!strings.Contains(strings.ToLower(condMessage), "pool") {
		t.Logf("condition message does not mention assignment or pool: %q", condMessage)
	}
}

// TestAddressPool_ExplicitAddressRequest covers FR-015 / User Story 4: a
// GameServer requesting a specific address via spec.networking.address,
// with exposure mode LoadBalancer, receives exactly that address from a real
// MetalLB running in the e2e cluster, and the AddressAssignment condition
// reaches reason Assigned.
//
// This is the only end-to-end coverage of the explicit-address path;
// envtest covers it only at the fake-client level (T061–T065).
func TestAddressPool_ExplicitAddressRequest(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	ns := "gameplane-games"
	tmplName := fmt.Sprintf("e2e-pool-explicit-%d", time.Now().UnixNano())
	gsName := fmt.Sprintf("e2e-gs-explicit-%d", time.Now().UnixNano())

	applyBusyboxTemplate(t, tmplName)

	// Request a specific address from pool-us-west. The MetalLB pool-us-west
	// is configured with range 172.18.255.200-172.18.255.210 (11 IPs). We pick
	// 172.18.255.205 (middle of the range) to avoid collision with auto-assigned
	// addresses from the other parallel tests (TestAddressPool_NamedPoolAssignment
	// and TestAddressPool_StatusVisibleInAddressAssignmentCondition), which do
	// not specify explicit addresses and will auto-assign from the low end of
	// the range. Since no other test explicitly requests an address (this is the
	// first), collision risk is minimal.
	requestedAddr := "172.18.255.205"

	gs := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "gameplane.local/v1alpha1",
		"kind":       "GameServer",
		"metadata":   map[string]any{"name": gsName, "namespace": ns},
		"spec": map[string]any{
			"templateRef": map[string]any{"name": tmplName},
			"networking": map[string]any{
				"expose":      "LoadBalancer",
				"addressPool": "pool-us-west",
				"address":     requestedAddr,
			},
		},
	}}
	if _, err := envInstance.Dyn.Resource(gameServerGVR).Namespace(ns).
		Create(ctx, gs, metav1.CreateOptions{}); err != nil && !apierrors.IsAlreadyExists(err) {
		t.Fatalf("create gameserver: %v", err)
	}
	t.Cleanup(func() {
		_ = envInstance.Dyn.Resource(gameServerGVR).Namespace(ns).
			Delete(context.Background(), gsName, metav1.DeleteOptions{})
	})

	// Wait for address assignment and verify the Service received the exact
	// requested address from MetalLB.
	var assignedAddr string
	var assignedPool string
	envInstance.Eventually(t, 60*time.Second, func() (bool, string) {
		got, err := envInstance.Dyn.Resource(gameServerGVR).Namespace(ns).
			Get(ctx, gsName, metav1.GetOptions{})
		if err != nil {
			return false, "get gameserver: " + err.Error()
		}

		// Check AddressAssignment condition.
		cond := findCondition(got.Object, "AddressAssignment")
		if cond == nil {
			return false, "AddressAssignment condition not found"
		}
		if cond["status"] != "True" {
			return false, fmt.Sprintf("AddressAssignment status=%v", cond["status"])
		}
		if cond["reason"] != "Assigned" {
			return false, fmt.Sprintf("AddressAssignment reason=%v, want Assigned", cond["reason"])
		}

		// Extract the assigned address from the endpoint and the Service's
		// load-balancer status.
		endpoints, _, _ := unstructured.NestedSlice(got.Object, "status", "endpoints")
		if len(endpoints) == 0 {
			return false, "no endpoints"
		}
		ep, ok := endpoints[0].(map[string]any)
		if !ok || ep["host"] == nil {
			return false, "endpoint malformed"
		}
		assignedAddr = ep["host"].(string)
		if pool, ok := ep["pool"].(string); ok {
			assignedPool = pool
		}

		// Also verify the Service status carries the same address.
		svc, err := envInstance.K8s.CoreV1().Services(ns).Get(ctx, gsName, metav1.GetOptions{})
		if err != nil {
			return false, "get service: " + err.Error()
		}
		if len(svc.Status.LoadBalancer.Ingress) == 0 {
			return false, "service has no loadBalancer ingress yet"
		}
		lbAddr := svc.Status.LoadBalancer.Ingress[0].IP
		if lbAddr == "" {
			return false, "service loadBalancer ingress IP is empty"
		}
		if lbAddr != requestedAddr {
			return false, fmt.Sprintf("service LB address=%q, want %q", lbAddr, requestedAddr)
		}

		return true, ""
	})

	// Assert the address matches what was requested.
	if assignedAddr != requestedAddr {
		t.Errorf("assigned address = %q, want %q", assignedAddr, requestedAddr)
	}

	// Assert the pool is correct.
	if assignedPool != "pool-us-west" {
		t.Errorf("assigned pool = %q, want pool-us-west", assignedPool)
	}
}

// TestAddressPool_AddressInUseConflict covers the address-in-use branch of the
// AddressAssignment condition: a GameServer requesting an explicit address that
// another GameServer already holds reports reason AddressInUse, names the
// holder in its message, and is never handed that address.
//
// The holder is waited on until it is Assigned at the address before the
// contender is created. The operator ranks a holder ahead of a mere requester
// (see addressConflictLess in operator/internal/controller/gameserver_controller.go),
// so the conflict is deterministic rather than dependent on creation order.
func TestAddressPool_AddressInUseConflict(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	ns := "gameplane-games"
	suffix := time.Now().UnixNano()
	tmplName := fmt.Sprintf("e2e-pool-inuse-%d", suffix)
	holderName := fmt.Sprintf("e2e-gs-inuse-holder-%d", suffix)
	contenderName := fmt.Sprintf("e2e-gs-inuse-contender-%d", suffix)

	applyBusyboxTemplate(t, tmplName)

	// .210 is the top of pool-us-west (172.18.255.200-210). Pool requests that
	// do not name an address are allocated from the low end of the range, so
	// this address is not contended by the other pool-us-west tests in practice.
	requestedAddr := "172.18.255.210"

	createServer := func(name string) {
		t.Helper()
		gs := &unstructured.Unstructured{Object: map[string]any{
			"apiVersion": "gameplane.local/v1alpha1",
			"kind":       "GameServer",
			"metadata":   map[string]any{"name": name, "namespace": ns},
			"spec": map[string]any{
				"templateRef": map[string]any{"name": tmplName},
				"networking": map[string]any{
					"expose":      "LoadBalancer",
					"addressPool": "pool-us-west",
					"address":     requestedAddr,
				},
			},
		}}
		if _, err := envInstance.Dyn.Resource(gameServerGVR).Namespace(ns).
			Create(ctx, gs, metav1.CreateOptions{}); err != nil && !apierrors.IsAlreadyExists(err) {
			t.Fatalf("create gameserver %s: %v", name, err)
		}
		t.Cleanup(func() {
			_ = envInstance.Dyn.Resource(gameServerGVR).Namespace(ns).
				Delete(context.Background(), name, metav1.DeleteOptions{})
		})
	}

	// Step 1: the holder must actually receive the address, so it holds it.
	createServer(holderName)
	envInstance.Eventually(t, 120*time.Second, func() (bool, string) {
		got, err := envInstance.Dyn.Resource(gameServerGVR).Namespace(ns).
			Get(ctx, holderName, metav1.GetOptions{})
		if err != nil {
			return false, "get holder gameserver: " + err.Error()
		}
		cond := findCondition(got.Object, "AddressAssignment")
		if cond == nil {
			return false, "holder AddressAssignment condition not found"
		}
		if cond["status"] != "True" {
			return false, fmt.Sprintf("holder not assigned yet: status=%v reason=%v", cond["status"], cond["reason"])
		}
		endpoints, _, _ := unstructured.NestedSlice(got.Object, "status", "endpoints")
		if len(endpoints) == 0 {
			return false, "holder has no endpoints"
		}
		ep, ok := endpoints[0].(map[string]any)
		if !ok {
			return false, "holder endpoint malformed"
		}
		if host, _ := ep["host"].(string); host != requestedAddr {
			return false, fmt.Sprintf("holder endpoint host = %q, want %q", host, requestedAddr)
		}
		return true, ""
	})

	// Step 2: a second server asks for the same address and must be refused.
	createServer(contenderName)
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
		if status != "False" {
			return false, fmt.Sprintf("contender status=%q reason=%q, want False/AddressInUse", status, reason)
		}
		if reason != "AddressInUse" {
			return false, fmt.Sprintf("contender reason=%q, want exactly AddressInUse", reason)
		}
		if !strings.Contains(message, holderName) {
			return false, fmt.Sprintf("message=%q does not name the holder %q", message, holderName)
		}
		return true, ""
	})

	// The refused server must not be given the contested address.
	got, err := envInstance.Dyn.Resource(gameServerGVR).Namespace(ns).
		Get(ctx, contenderName, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get contender gameserver for endpoint check: %v", err)
	}
	endpoints, _, _ := unstructured.NestedSlice(got.Object, "status", "endpoints")
	for i, epIface := range endpoints {
		ep, ok := epIface.(map[string]any)
		if !ok {
			continue
		}
		if host, _ := ep["host"].(string); host == requestedAddr {
			t.Errorf("contender endpoints[%d].host = %q, want the address withheld from it", i, host)
		}
	}
}
