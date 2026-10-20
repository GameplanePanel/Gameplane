package controller

import (
	"context"
	"fmt"
	"strconv"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	gameplanev1alpha1 "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
)

func testScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	if err := gameplanev1alpha1.AddToScheme(s); err != nil {
		t.Fatalf("gameplane scheme: %v", err)
	}
	if err := appsv1.AddToScheme(s); err != nil {
		t.Fatalf("apps scheme: %v", err)
	}
	if err := corev1.AddToScheme(s); err != nil {
		t.Fatalf("core scheme: %v", err)
	}
	return s
}

// TestFindAddressConflict_NoConflict tests the case where no other server
// requests or holds the address.
func TestFindAddressConflict_NoConflict(t *testing.T) {
	s := testScheme(t)
	gs := &gameplanev1alpha1.GameServer{
		ObjectMeta: metav1.ObjectMeta{Name: "alpha", Namespace: "games"},
		Spec: gameplanev1alpha1.GameServerSpec{
			Networking: gameplanev1alpha1.GameServerNetworking{
				Address: "10.0.0.5",
			},
		},
	}
	other := &gameplanev1alpha1.GameServer{
		ObjectMeta: metav1.ObjectMeta{Name: "beta", Namespace: "games"},
		Spec: gameplanev1alpha1.GameServerSpec{
			Networking: gameplanev1alpha1.GameServerNetworking{
				Address: "10.0.0.6", // Different address
			},
		},
	}
	cl := fake.NewClientBuilder().WithScheme(s).WithObjects(gs, other).Build()
	r := &GameServerReconciler{Client: cl, APIReader: cl, Scheme: s}

	conflict, err := r.findAddressConflict(context.Background(), gs)
	if err != nil {
		t.Fatalf("findAddressConflict: %v", err)
	}
	if conflict != "" {
		t.Errorf("want no conflict, got %q", conflict)
	}
}

// TestFindAddressConflict_HoldingInStatus tests the defect-1 case: another
// server HOLDS the requested address in status.endpoints (LoadBalancer
// expose, no tunnel provider) but its spec does NOT request it — e.g. it was
// assigned the address from a pool via spec.networking.addressPool with no
// explicit spec.networking.address. This must still be caught: it is the
// feature's central real-world scenario (a pool-assigned holder colliding
// with an explicit requester). Before the defect-1 fix, "holds" was only
// ever computed when "requests" was already true, so this case was invisible
// and this test would have failed with "no conflict" instead of "beta".
func TestFindAddressConflict_HoldingInStatus(t *testing.T) {
	s := testScheme(t)
	now := metav1.Now()
	gs := &gameplanev1alpha1.GameServer{
		ObjectMeta: metav1.ObjectMeta{Name: "alpha", Namespace: "games", CreationTimestamp: now},
		Spec: gameplanev1alpha1.GameServerSpec{
			Networking: gameplanev1alpha1.GameServerNetworking{
				Address: "10.0.0.5",
			},
		},
	}
	// The holder sorts first because "holds" is the leading key in
	// addressConflictLess: a genuine holder outranks a mere requester
	// regardless of creation order.
	other := &gameplanev1alpha1.GameServer{
		ObjectMeta: metav1.ObjectMeta{
			Name: "beta", Namespace: "games",
			CreationTimestamp: metav1.NewTime(now.Add(-10 * time.Second)),
		},
		Spec: gameplanev1alpha1.GameServerSpec{
			Networking: gameplanev1alpha1.GameServerNetworking{
				// No explicit address requested — assigned from a pool instead.
				Expose: "LoadBalancer",
			},
		},
		Status: gameplanev1alpha1.GameServerStatus{
			Endpoints: []gameplanev1alpha1.GameServerEndpoint{
				{Host: "10.0.0.5", Port: 25565},
			},
		},
	}
	cl := fake.NewClientBuilder().WithScheme(s).WithObjects(gs, other).Build()
	r := &GameServerReconciler{Client: cl, APIReader: cl, Scheme: s}

	conflict, err := r.findAddressConflict(context.Background(), gs)
	if err != nil {
		t.Fatalf("findAddressConflict: %v", err)
	}
	if conflict != "beta" {
		t.Errorf("want conflict 'beta', got %q", conflict)
	}
}

// TestFindAddressConflict_HoldingInStatusCrossNamespace is the cross-
// namespace variant of the defect-1 pool-holder case above: the holder is in
// a different namespace, requests nothing in spec, and holds the address
// only via a LoadBalancer status.endpoint.
func TestFindAddressConflict_HoldingInStatusCrossNamespace(t *testing.T) {
	s := testScheme(t)
	now := metav1.Now()
	gs := &gameplanev1alpha1.GameServer{
		ObjectMeta: metav1.ObjectMeta{Name: "alpha", Namespace: "games", CreationTimestamp: now},
		Spec: gameplanev1alpha1.GameServerSpec{
			Networking: gameplanev1alpha1.GameServerNetworking{
				Address: "10.0.0.5",
			},
		},
	}
	// Holder wins regardless of creation order, as "holds" is the leading key.
	other := &gameplanev1alpha1.GameServer{
		ObjectMeta: metav1.ObjectMeta{
			Name: "beta", Namespace: "other-ns",
			CreationTimestamp: metav1.NewTime(now.Add(-10 * time.Second)),
		},
		Spec: gameplanev1alpha1.GameServerSpec{
			Networking: gameplanev1alpha1.GameServerNetworking{
				// No explicit address requested — assigned from a pool instead.
				Expose: "LoadBalancer",
			},
		},
		Status: gameplanev1alpha1.GameServerStatus{
			Endpoints: []gameplanev1alpha1.GameServerEndpoint{
				{Host: "10.0.0.5", Port: 25565},
			},
		},
	}
	cl := fake.NewClientBuilder().WithScheme(s).WithObjects(gs, other).Build()
	r := &GameServerReconciler{Client: cl, APIReader: cl, Scheme: s}

	conflict, err := r.findAddressConflict(context.Background(), gs)
	if err != nil {
		t.Fatalf("findAddressConflict: %v", err)
	}
	if conflict != "other-ns/beta" {
		t.Errorf("want conflict 'other-ns/beta', got %q", conflict)
	}
}

// TestFindAddressConflict_TwoRequestingWithTiebreak tests that when two servers
// both request the same address, only the earlier-created one reports a conflict.
// The tiebreak is based on creation timestamp (and namespace/name if timestamps match).
func TestFindAddressConflict_TwoRequestingWithTiebreak(t *testing.T) {
	s := testScheme(t)

	// Create two servers requesting the same address.
	// Use creationTimestamps to control the tiebreak.
	older := &gameplanev1alpha1.GameServer{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "older",
			Namespace:         "games",
			CreationTimestamp: metav1.NewTime(metav1.Now().Add(-10 * time.Second)),
		},
		Spec: gameplanev1alpha1.GameServerSpec{
			Networking: gameplanev1alpha1.GameServerNetworking{
				Address: "10.0.0.5",
			},
		},
	}
	newer := &gameplanev1alpha1.GameServer{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "newer",
			Namespace:         "games",
			CreationTimestamp: metav1.Now(),
		},
		Spec: gameplanev1alpha1.GameServerSpec{
			Networking: gameplanev1alpha1.GameServerNetworking{
				Address: "10.0.0.5",
			},
		},
	}

	cl := fake.NewClientBuilder().WithScheme(s).WithObjects(older, newer).Build()
	r := &GameServerReconciler{Client: cl, APIReader: cl, Scheme: s}

	// The newer server (created later) should report the older one as a conflict.
	conflict, err := r.findAddressConflict(context.Background(), newer)
	if err != nil {
		t.Fatalf("findAddressConflict for newer: %v", err)
	}
	if conflict != "older" {
		t.Errorf("newer: want conflict 'older', got %q", conflict)
	}

	// The older server (created first) should NOT report a conflict.
	conflict, err = r.findAddressConflict(context.Background(), older)
	if err != nil {
		t.Fatalf("findAddressConflict for older: %v", err)
	}
	if conflict != "" {
		t.Errorf("older: want no conflict, got %q", conflict)
	}
}

// TestFindAddressConflict_TwoRequestingWithNamespaceTiebreak tests the tiebreak
// when two servers have the same creation timestamp in different namespaces.
func TestFindAddressConflict_TwoRequestingWithNamespaceTiebreak(t *testing.T) {
	s := testScheme(t)

	// Truncated to whole-second precision: the fake client's List JSON-round-
	// trips objects, and metav1.Time marshals at second precision, so a
	// nanosecond-precision CreationTimestamp on the in-memory seed object
	// would come back truncated from List — making it sort *before* the
	// original and hit the timestamp branch of addressConflictLess instead
	// of the namespace branch this test exists to exercise.
	now := metav1.Now().Rfc3339Copy()
	// Server in namespace "aaa" (sorts first)
	earlier := &gameplanev1alpha1.GameServer{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "test",
			Namespace:         "aaa",
			CreationTimestamp: now,
		},
		Spec: gameplanev1alpha1.GameServerSpec{
			Networking: gameplanev1alpha1.GameServerNetworking{
				Address: "10.0.0.5",
			},
		},
	}
	// Server in namespace "bbb" (sorts later)
	later := &gameplanev1alpha1.GameServer{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "test",
			Namespace:         "bbb",
			CreationTimestamp: now,
		},
		Spec: gameplanev1alpha1.GameServerSpec{
			Networking: gameplanev1alpha1.GameServerNetworking{
				Address: "10.0.0.5",
			},
		},
	}

	cl := fake.NewClientBuilder().WithScheme(s).WithObjects(earlier, later).Build()
	r := &GameServerReconciler{Client: cl, APIReader: cl, Scheme: s}

	// The one in "bbb" should report "aaa" as a conflict (namespace sort).
	conflict, err := r.findAddressConflict(context.Background(), later)
	if err != nil {
		t.Fatalf("findAddressConflict for later: %v", err)
	}
	if conflict != "aaa/test" {
		t.Errorf("later: want conflict 'aaa/test', got %q", conflict)
	}

	// The one in "aaa" should report no conflict.
	conflict, err = r.findAddressConflict(context.Background(), earlier)
	if err != nil {
		t.Fatalf("findAddressConflict for earlier: %v", err)
	}
	if conflict != "" {
		t.Errorf("earlier: want no conflict, got %q", conflict)
	}
}

// TestFindAddressConflict_TwoRequestingWithNameTiebreak tests the tiebreak
// when two servers have the same creation timestamp and namespace.
func TestFindAddressConflict_TwoRequestingWithNameTiebreak(t *testing.T) {
	s := testScheme(t)

	// Truncated to whole-second precision — see the namespace-tiebreak test
	// above for why: without it the fake client's List round-trip makes the
	// listed copy sort earlier than the raw seed object, hitting the
	// timestamp branch instead of the name branch this test exercises.
	now := metav1.Now().Rfc3339Copy()
	// Server named "alpha" (sorts first)
	alpha := &gameplanev1alpha1.GameServer{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "alpha",
			Namespace:         "games",
			CreationTimestamp: now,
		},
		Spec: gameplanev1alpha1.GameServerSpec{
			Networking: gameplanev1alpha1.GameServerNetworking{
				Address: "10.0.0.5",
			},
		},
	}
	// Server named "beta" (sorts later)
	beta := &gameplanev1alpha1.GameServer{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "beta",
			Namespace:         "games",
			CreationTimestamp: now,
		},
		Spec: gameplanev1alpha1.GameServerSpec{
			Networking: gameplanev1alpha1.GameServerNetworking{
				Address: "10.0.0.5",
			},
		},
	}

	cl := fake.NewClientBuilder().WithScheme(s).WithObjects(alpha, beta).Build()
	r := &GameServerReconciler{Client: cl, APIReader: cl, Scheme: s}

	// The one named "beta" should report "alpha" as a conflict (name sort).
	conflict, err := r.findAddressConflict(context.Background(), beta)
	if err != nil {
		t.Fatalf("findAddressConflict for beta: %v", err)
	}
	if conflict != "alpha" {
		t.Errorf("beta: want conflict 'alpha', got %q", conflict)
	}

	// The one named "alpha" should report no conflict.
	conflict, err = r.findAddressConflict(context.Background(), alpha)
	if err != nil {
		t.Fatalf("findAddressConflict for alpha: %v", err)
	}
	if conflict != "" {
		t.Errorf("alpha: want no conflict, got %q", conflict)
	}
}

// TestFindAddressConflict_UnrelatedAddress tests that an unrelated address
// doesn't cause a conflict.
func TestFindAddressConflict_UnrelatedAddress(t *testing.T) {
	s := testScheme(t)
	gs := &gameplanev1alpha1.GameServer{
		ObjectMeta: metav1.ObjectMeta{Name: "alpha", Namespace: "games"},
		Spec: gameplanev1alpha1.GameServerSpec{
			Networking: gameplanev1alpha1.GameServerNetworking{
				Address: "10.0.0.5",
			},
		},
	}
	other := &gameplanev1alpha1.GameServer{
		ObjectMeta: metav1.ObjectMeta{Name: "beta", Namespace: "games"},
		Spec: gameplanev1alpha1.GameServerSpec{
			Networking: gameplanev1alpha1.GameServerNetworking{
				Address: "10.0.0.6",
			},
		},
		Status: gameplanev1alpha1.GameServerStatus{
			Endpoints: []gameplanev1alpha1.GameServerEndpoint{
				{Host: "10.0.0.6", Port: 25565},
			},
		},
	}
	cl := fake.NewClientBuilder().WithScheme(s).WithObjects(gs, other).Build()
	r := &GameServerReconciler{Client: cl, APIReader: cl, Scheme: s}

	conflict, err := r.findAddressConflict(context.Background(), gs)
	if err != nil {
		t.Fatalf("findAddressConflict: %v", err)
	}
	if conflict != "" {
		t.Errorf("want no conflict, got %q", conflict)
	}
}

// TestFindAddressConflict_NoAddressRequested tests that a server with no
// address requested returns no conflict via the early return, before even
// listing other servers.
//
// The second seeded object also requests no address, matching gs's own
// requestedAddr of "" — so it WOULD become a candidate if the early return
// were removed. To make that observable, it is also given an earlier
// creationTimestamp than gs: were the early return absent, the
// (holds/timestamp/namespace/name) comparison would sort it before gs and
// this test would see a non-empty conflict. A same-timestamp or
// alphabetically-later second object would pass this test even with the
// early return deleted, for the wrong reason (gs would win the tiebreak on
// its own merits) — the earlier timestamp is what makes the early return
// load-bearing here.
func TestFindAddressConflict_NoAddressRequested(t *testing.T) {
	s := testScheme(t)
	now := metav1.Now()
	gs := &gameplanev1alpha1.GameServer{
		ObjectMeta: metav1.ObjectMeta{Name: "alpha", Namespace: "games", CreationTimestamp: now},
		Spec: gameplanev1alpha1.GameServerSpec{
			Networking: gameplanev1alpha1.GameServerNetworking{
				// No address requested
			},
		},
	}
	other := &gameplanev1alpha1.GameServer{
		ObjectMeta: metav1.ObjectMeta{
			Name: "beta", Namespace: "games",
			// Earlier than gs, so it would win the tiebreak (and this test
			// would fail) were the early return not firing.
			CreationTimestamp: metav1.NewTime(now.Add(-10 * time.Second)),
		},
		Spec: gameplanev1alpha1.GameServerSpec{
			Networking: gameplanev1alpha1.GameServerNetworking{
				// No address requested either — matches gs's requestedAddr of "".
			},
		},
	}
	cl := fake.NewClientBuilder().WithScheme(s).WithObjects(gs, other).Build()
	r := &GameServerReconciler{Client: cl, APIReader: cl, Scheme: s}

	conflict, err := r.findAddressConflict(context.Background(), gs)
	if err != nil {
		t.Fatalf("findAddressConflict: %v", err)
	}
	if conflict != "" {
		t.Errorf("want no conflict, got %q", conflict)
	}
}

// TestFindAddressConflict_Livelock tests the specific scenario that made
// the old two-branch findAddressConflict non-total: A holds address X in
// status (via a genuine LoadBalancer-exposed endpoint) AND requests X; B was
// created EARLIER and merely requests X, never holding it. Exactly one of
// them must report a conflict, and — under the holder-priority precedence
// order — it must be B: an actual holder (A) outranks a mere requester (B)
// regardless of creation order, so A proceeds and B must yield. Never both,
// and never neither.
func TestFindAddressConflict_Livelock(t *testing.T) {
	s := testScheme(t)
	now := metav1.Now()

	// B: created first, merely requests X (never assigned it, never holds
	// it — no Expose/status.endpoints at all).
	b := &gameplanev1alpha1.GameServer{
		ObjectMeta: metav1.ObjectMeta{
			Name: "b", Namespace: "games",
			CreationTimestamp: metav1.NewTime(now.Add(-10 * time.Second)),
		},
		Spec: gameplanev1alpha1.GameServerSpec{
			Networking: gameplanev1alpha1.GameServerNetworking{Address: "10.0.0.5"},
		},
	}
	// A: created later, holds X in status (LoadBalancer-exposed, matching
	// endpoint with no TunnelProvider) AND requests X in spec.
	a := &gameplanev1alpha1.GameServer{
		ObjectMeta: metav1.ObjectMeta{Name: "a", Namespace: "games", CreationTimestamp: now},
		Spec: gameplanev1alpha1.GameServerSpec{
			Networking: gameplanev1alpha1.GameServerNetworking{
				Address: "10.0.0.5",
				Expose:  "LoadBalancer",
			},
		},
		Status: gameplanev1alpha1.GameServerStatus{
			Endpoints: []gameplanev1alpha1.GameServerEndpoint{
				{Host: "10.0.0.5", Port: 25565},
			},
		},
	}

	cl := fake.NewClientBuilder().WithScheme(s).WithObjects(a, b).Build()
	r := &GameServerReconciler{Client: cl, APIReader: cl, Scheme: s}

	// Reconciling A must report NO conflict — A actually holds the address,
	// so it outranks B's mere request even though B was created earlier.
	conflictA, err := r.findAddressConflict(context.Background(), a)
	if err != nil {
		t.Fatalf("findAddressConflict(a): %v", err)
	}
	if conflictA != "" {
		t.Errorf("A: want no conflict, got %q", conflictA)
	}

	// Reconciling B must report A as the conflict — B never holds the
	// address, so it must yield to the actual holder even though B is
	// older. Under the old timestamp-only tiebreak, B (being older) would
	// have won here and A would also have reported B (via the unconditional
	// holds branch), producing the livelock: both sides permanently
	// reporting each other. The holder-priority order fixes that by making
	// A's own hold status win the comparison outright, so only B reports.
	conflictB, err := r.findAddressConflict(context.Background(), b)
	if err != nil {
		t.Fatalf("findAddressConflict(b): %v", err)
	}
	if conflictB != "a" {
		t.Errorf("B: want conflict 'a', got %q", conflictB)
	}
}

// TestFindAddressConflict_HolderCreatedAfterRequester tests defect 1's
// central case directly: a pool-assigned holder (empty spec.networking.address,
// addressPool set, genuinely LoadBalancer-exposed, address already present in
// status.endpoints) is created AFTER an explicit requester for the same
// address. The requester must still report the conflict and name the holder
// — creation order must not let the requester win just because it asked
// first, since the pool holder demonstrably has the address already. The
// holder itself, being a pure holder (empty spec address), hits the early
// return and never reports anything back — so this cannot become a mutual
// pair.
func TestFindAddressConflict_HolderCreatedAfterRequester(t *testing.T) {
	s := testScheme(t)
	now := metav1.Now()

	// Requester: created first, explicitly requests the address, does not
	// yet hold it.
	requester := &gameplanev1alpha1.GameServer{
		ObjectMeta: metav1.ObjectMeta{
			Name: "requester", Namespace: "games",
			CreationTimestamp: metav1.NewTime(now.Add(-10 * time.Second)),
		},
		Spec: gameplanev1alpha1.GameServerSpec{
			Networking: gameplanev1alpha1.GameServerNetworking{Address: "10.0.0.5"},
		},
	}
	// Holder: created later, assigned the address from a pool — no explicit
	// spec.networking.address, so it can never itself report a conflict
	// (early return), but it does genuinely hold the address.
	holder := &gameplanev1alpha1.GameServer{
		ObjectMeta: metav1.ObjectMeta{Name: "holder", Namespace: "games", CreationTimestamp: now},
		Spec: gameplanev1alpha1.GameServerSpec{
			Networking: gameplanev1alpha1.GameServerNetworking{
				Expose:      "LoadBalancer",
				AddressPool: "default",
			},
		},
		Status: gameplanev1alpha1.GameServerStatus{
			Endpoints: []gameplanev1alpha1.GameServerEndpoint{
				{Host: "10.0.0.5", Port: 25565, Pool: "default"},
			},
		},
	}

	cl := fake.NewClientBuilder().WithScheme(s).WithObjects(requester, holder).Build()
	r := &GameServerReconciler{Client: cl, APIReader: cl, Scheme: s}

	// The requester must report the holder as the conflict, even though the
	// requester is older.
	conflictRequester, err := r.findAddressConflict(context.Background(), requester)
	if err != nil {
		t.Fatalf("findAddressConflict(requester): %v", err)
	}
	if conflictRequester != "holder" {
		t.Errorf("requester: want conflict 'holder', got %q", conflictRequester)
	}

	// The holder must report no conflict — it never even reaches the
	// comparison, since its own spec.networking.address is empty and the
	// early return fires first.
	conflictHolder, err := r.findAddressConflict(context.Background(), holder)
	if err != nil {
		t.Fatalf("findAddressConflict(holder): %v", err)
	}
	if conflictHolder != "" {
		t.Errorf("holder: want no conflict, got %q", conflictHolder)
	}
}

// TestFindAddressConflict_BothHoldingStale tests the symmetric stale-status
// case: two servers are both genuinely LoadBalancer-exposed and both carry
// address X in status.endpoints (e.g. after a Service recreate left one
// stale), and both still request X. Since both are actual holders (equal
// rank on the holds key), the tiebreak falls through to creationTimestamp
// same as a plain two-requesters case. Exactly one must conflict — never
// both.
func TestFindAddressConflict_BothHoldingStale(t *testing.T) {
	s := testScheme(t)
	now := metav1.Now()

	first := &gameplanev1alpha1.GameServer{
		ObjectMeta: metav1.ObjectMeta{
			Name: "first", Namespace: "games",
			CreationTimestamp: metav1.NewTime(now.Add(-10 * time.Second)),
		},
		Spec: gameplanev1alpha1.GameServerSpec{
			Networking: gameplanev1alpha1.GameServerNetworking{
				Address: "10.0.0.5",
				Expose:  "LoadBalancer",
			},
		},
		Status: gameplanev1alpha1.GameServerStatus{
			Endpoints: []gameplanev1alpha1.GameServerEndpoint{
				{Host: "10.0.0.5", Port: 25565},
			},
		},
	}
	second := &gameplanev1alpha1.GameServer{
		ObjectMeta: metav1.ObjectMeta{Name: "second", Namespace: "games", CreationTimestamp: now},
		Spec: gameplanev1alpha1.GameServerSpec{
			Networking: gameplanev1alpha1.GameServerNetworking{
				Address: "10.0.0.5",
				Expose:  "LoadBalancer",
			},
		},
		Status: gameplanev1alpha1.GameServerStatus{
			Endpoints: []gameplanev1alpha1.GameServerEndpoint{
				{Host: "10.0.0.5", Port: 25565},
			},
		},
	}

	cl := fake.NewClientBuilder().WithScheme(s).WithObjects(first, second).Build()
	r := &GameServerReconciler{Client: cl, APIReader: cl, Scheme: s}

	conflictFirst, err := r.findAddressConflict(context.Background(), first)
	if err != nil {
		t.Fatalf("findAddressConflict(first): %v", err)
	}
	if conflictFirst != "" {
		t.Errorf("first: want no conflict, got %q", conflictFirst)
	}

	conflictSecond, err := r.findAddressConflict(context.Background(), second)
	if err != nil {
		t.Fatalf("findAddressConflict(second): %v", err)
	}
	if conflictSecond != "first" {
		t.Errorf("second: want conflict 'first', got %q", conflictSecond)
	}
}

// TestFindAddressConflict_BothRequestingWithYoungerHolder tests that when both
// servers set spec.networking.address, but only the younger one holds the
// address in status, the holder is still reported as the conflict by the
// requester — holder priority wins even over creation order. The holder itself
// (holding the address) outranks the requester (only requesting), so it reports
// no conflict.
func TestFindAddressConflict_BothRequestingWithYoungerHolder(t *testing.T) {
	s := testScheme(t)
	now := metav1.Now()

	// Requester: created first, explicitly requests the address but does not
	// hold it (no Expose/status.endpoints).
	requester := &gameplanev1alpha1.GameServer{
		ObjectMeta: metav1.ObjectMeta{
			Name: "requester", Namespace: "games",
			CreationTimestamp: metav1.NewTime(now.Add(-10 * time.Second)),
		},
		Spec: gameplanev1alpha1.GameServerSpec{
			Networking: gameplanev1alpha1.GameServerNetworking{
				Address: "10.0.0.5",
			},
		},
	}

	// Holder: created later, explicitly requests the address AND holds it in
	// status via LoadBalancer expose. Must outrank the older requester.
	holder := &gameplanev1alpha1.GameServer{
		ObjectMeta: metav1.ObjectMeta{
			Name: "holder", Namespace: "games",
			CreationTimestamp: now,
		},
		Spec: gameplanev1alpha1.GameServerSpec{
			Networking: gameplanev1alpha1.GameServerNetworking{
				Address: "10.0.0.5",
				Expose:  "LoadBalancer",
			},
		},
		Status: gameplanev1alpha1.GameServerStatus{
			Endpoints: []gameplanev1alpha1.GameServerEndpoint{
				{Host: "10.0.0.5", Port: 25565},
			},
		},
	}

	cl := fake.NewClientBuilder().WithScheme(s).WithObjects(requester, holder).Build()
	r := &GameServerReconciler{Client: cl, APIReader: cl, Scheme: s}

	// Requester (older but non-holding) must report the holder as the conflict.
	conflictRequester, err := r.findAddressConflict(context.Background(), requester)
	if err != nil {
		t.Fatalf("findAddressConflict(requester): %v", err)
	}
	if conflictRequester != "holder" {
		t.Errorf("requester: want conflict 'holder', got %q", conflictRequester)
	}

	// Holder (younger but actually holding) must report no conflict — it outranks
	// the requester because holds is the leading key.
	conflictHolder, err := r.findAddressConflict(context.Background(), holder)
	if err != nil {
		t.Fatalf("findAddressConflict(holder): %v", err)
	}
	if conflictHolder != "" {
		t.Errorf("holder: want no conflict, got %q", conflictHolder)
	}
}

// TestFindAddressConflict_ThreeContendersDeterministicName tests that with
// 3+ contenders the reported name is always the earliest-created one, and
// that this does not depend on the order objects were seeded into the fake
// client (controller-runtime's List order is not stable). The
// earliest-created server is deliberately named "zzz" — alphabetically LAST
// — so that alphabetical order disagrees with creation order. client-go's
// fake client always returns List results sorted by (namespace, name), so
// with all-same-namespace objects, every seed order below produces the same
// List order; naming the earliest "zzz" is what stops that coincidental
// List-order stability from silently agreeing with the (correct)
// creation-order answer and masking a bug that ignored creationTimestamp
// and used List/name order instead.
func TestFindAddressConflict_ThreeContendersDeterministicName(t *testing.T) {
	s := testScheme(t)
	now := metav1.Now()

	mk := func(name string, offsetSeconds int) *gameplanev1alpha1.GameServer {
		return &gameplanev1alpha1.GameServer{
			ObjectMeta: metav1.ObjectMeta{
				Name: name, Namespace: "games",
				CreationTimestamp: metav1.NewTime(now.Add(time.Duration(offsetSeconds) * time.Second)),
			},
			Spec: gameplanev1alpha1.GameServerSpec{
				Networking: gameplanev1alpha1.GameServerNetworking{Address: "10.0.0.5"},
			},
		}
	}

	// earliest / middle / latest by creationTimestamp — named "zzz" for the
	// earliest and "aaa" for the latest, so alphabetical name order is the
	// exact reverse of creation order.
	earliest := mk("zzz", -20)
	middle := mk("middle", -10)
	latest := mk("aaa", 0)

	orders := [][]client.Object{
		{earliest, middle, latest},
		{latest, earliest, middle},
		{middle, latest, earliest},
	}

	for i, order := range orders {
		t.Run(fmt.Sprintf("seedOrder%d", i), func(t *testing.T) {
			cl := fake.NewClientBuilder().WithScheme(s).WithObjects(order...).Build()
			r := &GameServerReconciler{Client: cl, APIReader: cl, Scheme: s}

			// "latest" (named "aaa", sorts alphabetically first) is the
			// reconciling server; the reported conflict must still be "zzz"
			// (the earliest-created one), regardless of seed order or of
			// "aaa" sorting before "zzz" alphabetically.
			conflict, err := r.findAddressConflict(context.Background(), latest)
			if err != nil {
				t.Fatalf("findAddressConflict(latest): %v", err)
			}
			if conflict != "zzz" {
				t.Errorf("want conflict 'zzz', got %q", conflict)
			}

			// "middle" must conflict with "zzz" too.
			conflict, err = r.findAddressConflict(context.Background(), middle)
			if err != nil {
				t.Fatalf("findAddressConflict(middle): %v", err)
			}
			if conflict != "zzz" {
				t.Errorf("middle: want conflict 'zzz', got %q", conflict)
			}

			// "zzz" (earliest-created) itself has no conflict, even though it
			// sorts last alphabetically.
			conflict, err = r.findAddressConflict(context.Background(), earliest)
			if err != nil {
				t.Fatalf("findAddressConflict(earliest): %v", err)
			}
			if conflict != "" {
				t.Errorf("earliest: want no conflict, got %q", conflict)
			}
		})
	}
}

// TestFindAddressConflict_ClusterIPFalsePositive tests the two
// discriminators that keep a status.endpoints Host match from
// false-positiving as "holding" the address: expose mode and tunnel
// provenance. After the defect-1 fix, an actual LoadBalancer-exposed holder
// (no explicit spec request needed) DOES conflict — that's the feature's
// central case, asserted first — while a ClusterIP-exposed "holder" and a
// tunnel-sourced endpoint, both coincidental Host matches rather than real
// address-path assignments, do NOT.
func TestFindAddressConflict_ClusterIPFalsePositive(t *testing.T) {
	s := testScheme(t)
	now := metav1.Now()

	mk := func(name string, offsetSeconds int, expose, tunnelProvider string) *gameplanev1alpha1.GameServer {
		return &gameplanev1alpha1.GameServer{
			ObjectMeta: metav1.ObjectMeta{
				Name: name, Namespace: "games",
				CreationTimestamp: metav1.NewTime(now.Add(time.Duration(offsetSeconds) * time.Second)),
			},
			Spec: gameplanev1alpha1.GameServerSpec{
				Networking: gameplanev1alpha1.GameServerNetworking{
					// Never requests the address in spec — any conflict must
					// come purely from the status.endpoints "holds" path.
					Expose: expose,
				},
			},
			Status: gameplanev1alpha1.GameServerStatus{
				Endpoints: []gameplanev1alpha1.GameServerEndpoint{
					{Host: "10.0.0.5", Port: 25565, TunnelProvider: tunnelProvider},
				},
			},
		}
	}

	gs := &gameplanev1alpha1.GameServer{
		ObjectMeta: metav1.ObjectMeta{Name: "alpha", Namespace: "games", CreationTimestamp: now},
		Spec: gameplanev1alpha1.GameServerSpec{
			Networking: gameplanev1alpha1.GameServerNetworking{
				Address: "10.0.0.5",
			},
		},
	}

	// A genuine LoadBalancer-exposed holder, created earlier, DOES conflict
	// even though it never requested the address in spec.
	lbHolder := mk("lb-holder", -10, "LoadBalancer", "")
	// A ClusterIP-exposed "holder" is a fallback-Host coincidence, not a real
	// assignment, and must NOT conflict.
	clusterIPHolder := mk("clusterip-holder", -10, "ClusterIP", "")
	// A LoadBalancer-exposed endpoint that is tunnel-sourced is also a
	// coincidental Host match (tunnel hostnames aren't LB addresses) and
	// must NOT conflict.
	tunnelHolder := mk("tunnel-holder", -10, "LoadBalancer", "frp")

	t.Run("LoadBalancerHolderConflicts", func(t *testing.T) {
		cl := fake.NewClientBuilder().WithScheme(s).WithObjects(gs, lbHolder).Build()
		r := &GameServerReconciler{Client: cl, APIReader: cl, Scheme: s}
		conflict, err := r.findAddressConflict(context.Background(), gs)
		if err != nil {
			t.Fatalf("findAddressConflict: %v", err)
		}
		if conflict != "lb-holder" {
			t.Errorf("want conflict 'lb-holder', got %q", conflict)
		}
	})

	t.Run("ClusterIPFallbackDoesNotConflict", func(t *testing.T) {
		cl := fake.NewClientBuilder().WithScheme(s).WithObjects(gs, clusterIPHolder).Build()
		r := &GameServerReconciler{Client: cl, APIReader: cl, Scheme: s}
		conflict, err := r.findAddressConflict(context.Background(), gs)
		if err != nil {
			t.Fatalf("findAddressConflict: %v", err)
		}
		if conflict != "" {
			t.Errorf("want no conflict (ClusterIP fallback must not count as holding), got %q", conflict)
		}
	})

	t.Run("TunnelSourcedDoesNotConflict", func(t *testing.T) {
		cl := fake.NewClientBuilder().WithScheme(s).WithObjects(gs, tunnelHolder).Build()
		r := &GameServerReconciler{Client: cl, APIReader: cl, Scheme: s}
		conflict, err := r.findAddressConflict(context.Background(), gs)
		if err != nil {
			t.Fatalf("findAddressConflict: %v", err)
		}
		if conflict != "" {
			t.Errorf("want no conflict (tunnel-sourced endpoint must not count as holding), got %q", conflict)
		}
	})
}

// TestFindAddressConflict_SkipsTerminatingCandidate tests defect 2: a
// candidate with a non-nil metadata.deletionTimestamp must be excluded from
// the contending set entirely, even though it is the earliest-created and
// would otherwise win the tiebreak. Without the skip, this terminating
// server would permanently block the live one from ever proceeding.
func TestFindAddressConflict_SkipsTerminatingCandidate(t *testing.T) {
	s := testScheme(t)
	now := metav1.Now()

	gs := &gameplanev1alpha1.GameServer{
		ObjectMeta: metav1.ObjectMeta{Name: "alpha", Namespace: "games", CreationTimestamp: now},
		Spec: gameplanev1alpha1.GameServerSpec{
			Networking: gameplanev1alpha1.GameServerNetworking{Address: "10.0.0.5"},
		},
	}

	terminating := now.Add(-10 * time.Second)
	terminatingCandidate := &gameplanev1alpha1.GameServer{
		ObjectMeta: metav1.ObjectMeta{
			Name: "dying", Namespace: "games",
			CreationTimestamp: metav1.NewTime(terminating),
			DeletionTimestamp: &metav1.Time{Time: terminating},
			// A real deleting object always carries a finalizer (else the
			// apiserver would have removed it already); set one so the fake
			// client's own consistency checks don't reject the object.
			Finalizers: []string{"gameplane.local/test-finalizer"},
		},
		Spec: gameplanev1alpha1.GameServerSpec{
			Networking: gameplanev1alpha1.GameServerNetworking{Address: "10.0.0.5"},
		},
	}

	cl := fake.NewClientBuilder().WithScheme(s).WithObjects(gs, terminatingCandidate).Build()
	r := &GameServerReconciler{Client: cl, APIReader: cl, Scheme: s}

	conflict, err := r.findAddressConflict(context.Background(), gs)
	if err != nil {
		t.Fatalf("findAddressConflict: %v", err)
	}
	if conflict != "" {
		t.Errorf("want no conflict (terminating candidate must be skipped), got %q", conflict)
	}
}

// TestBuildCaptureEphemeralContainer_VolumeBudgetMatchesSizeLimit guards
// against F-187 regressing: the CAPTURE_VOLUME_BUDGET_BYTES env var the
// operator hands the capture sidecar must always be derived from (and stay
// under) the "captures" emptyDir's own SizeLimit, so the sidecar's own
// admission check can never allow more than the kubelet itself will.
func TestBuildCaptureEphemeralContainer_VolumeBudgetMatchesSizeLimit(t *testing.T) {
	// The "captures" Volume's SizeLimit, reconcileStatefulSet, and this
	// env var must all trace back to the same constant - see
	// captureVolumeSizeLimitBytes' doc comment.
	if captureVolumeSizeLimitBytes != 1*1024*1024*1024 {
		t.Fatalf("captureVolumeSizeLimitBytes = %d, want 1Gi (1073741824)", captureVolumeSizeLimitBytes)
	}

	ec := buildCaptureEphemeralContainer("", "server-uid")
	uidFound := false
	for _, env := range ec.Env {
		if env.Name == "GAMEPLANE_SERVER_UID" {
			uidFound = env.Value == "server-uid"
		}
	}
	if !uidFound {
		t.Fatal("capture sidecar lacks immutable GameServer UID")
	}

	var budgetValue string
	found := false
	for _, env := range ec.Env {
		if env.Name == "CAPTURE_VOLUME_BUDGET_BYTES" {
			budgetValue = env.Value
			found = true
		}
	}
	if !found {
		t.Fatal("buildCaptureEphemeralContainer: no CAPTURE_VOLUME_BUDGET_BYTES env var")
	}

	gotBudget, err := strconv.ParseInt(budgetValue, 10, 64)
	if err != nil {
		t.Fatalf("CAPTURE_VOLUME_BUDGET_BYTES = %q is not an int64: %v", budgetValue, err)
	}
	if gotBudget != captureVolumeBudgetBytes {
		t.Errorf("CAPTURE_VOLUME_BUDGET_BYTES = %d, want captureVolumeBudgetBytes (%d)", gotBudget, captureVolumeBudgetBytes)
	}
	// The budget must always leave a margin under the real volume limit:
	// equal to or above it would let an admitted capture push the volume to
	// (or past) the size the kubelet itself enforces.
	if gotBudget >= captureVolumeSizeLimitBytes {
		t.Errorf("CAPTURE_VOLUME_BUDGET_BYTES (%d) must be strictly under the captures emptyDir SizeLimit (%d)", gotBudget, captureVolumeSizeLimitBytes)
	}
}

// TestCaptureVolume_SizeLimitMatchesConstant guards against the "captures"
// emptyDir Volume that reconcileStatefulSet actually provisions drifting
// from captureVolumeSizeLimitBytes, the constant CAPTURE_VOLUME_BUDGET_BYTES
// (above) and the chart/docs default are derived from. Unlike
// TestBuildCaptureEphemeralContainer_VolumeBudgetMatchesSizeLimit, which only
// checks the constant's own value, this exercises captureVolume() — the same
// function reconcileStatefulSet calls to build the StatefulSet's pod
// template — so a future edit to the emptyDir literal itself (not just the
// constant) is also caught.
func TestCaptureVolume_SizeLimitMatchesConstant(t *testing.T) {
	v := captureVolume()
	if v.Name != "captures" {
		t.Fatalf("captureVolume().Name = %q, want %q", v.Name, "captures")
	}
	if v.EmptyDir == nil {
		t.Fatal("captureVolume().EmptyDir is nil, want an EmptyDir volume source")
	}
	if v.EmptyDir.SizeLimit == nil {
		t.Fatal("captureVolume().EmptyDir.SizeLimit is nil")
	}
	if got := v.EmptyDir.SizeLimit.Value(); got != captureVolumeSizeLimitBytes {
		t.Errorf("captureVolume().EmptyDir.SizeLimit = %d, want captureVolumeSizeLimitBytes (%d)", got, captureVolumeSizeLimitBytes)
	}
}
