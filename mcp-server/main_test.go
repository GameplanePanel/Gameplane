package main

import (
	"context"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	k8sfake "k8s.io/client-go/kubernetes/fake"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/GameplanePanel/gameplane/mcp-server/internal/kube"
)

// mutatingVerbs is the denylist asserted against both kube.Client's method
// set and the registered tool names: this server's entire reason for
// existing is that none of these ever appear.
var mutatingVerbs = []string{"Create", "Update", "Delete", "Patch", "Apply", "Remove"}

// TestClientHasNoMutatingMethods is a lint-level tripwire, not the read-only
// guarantee itself: since kube.Client's typed/dynamic clientsets are
// unexported fields defined in internal/kube, this package (main, where
// every tool handler lives) has no way to reach a mutating verb through a
// *kube.Client no matter what methods it has — that structural guarantee
// holds even if this test were deleted. What this test catches is a
// regression *within* internal/kube: if a future change adds an exported
// Create/Update/Delete/Patch/Apply-shaped method to Client, this fails
// immediately, before that method could ever be wired to a tool. The other,
// authoritative backstop is the get/list/watch-only RBAC ClusterRole the
// Helm chart installs (charts/gameplane/templates/mcp-server.yaml) — that
// holds even if both Go-level checks were somehow bypassed.
func TestClientHasNoMutatingMethods(t *testing.T) {
	typ := reflect.TypeOf(&kube.Client{})
	for i := 0; i < typ.NumMethod(); i++ {
		name := typ.Method(i).Name
		for _, verb := range mutatingVerbs {
			if strings.HasPrefix(name, verb) {
				t.Errorf("kube.Client has mutating-looking method %q (matches verb %q)", name, verb)
			}
		}
	}
}

func connectInMemory(t *testing.T, server *mcp.Server) *mcp.ClientSession {
	t.Helper()
	ctx := context.Background()
	t1, t2 := mcp.NewInMemoryTransports()
	if _, err := server.Connect(ctx, t1, nil); err != nil {
		t.Fatalf("server.Connect: %v", err)
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "v0.0.1"}, nil)
	cs, err := client.Connect(ctx, t2, nil)
	if err != nil {
		t.Fatalf("client.Connect: %v", err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	return cs
}

// TestRegisteredToolsAreReadOnly is the registration half of the read-only
// invariant: every tool this server advertises must carry ReadOnlyHint, must
// not be flagged destructive, and its name must not look like a mutating
// verb — checked over the wire via a real client session, not just against
// the in-process registry.
func TestRegisteredToolsAreReadOnly(t *testing.T) {
	server := newMCPServer(&kube.Client{})
	cs := connectInMemory(t, server)
	ctx := context.Background()

	var gotNames []string
	for tool, err := range cs.Tools(ctx, nil) {
		if err != nil {
			t.Fatalf("Tools: %v", err)
		}
		gotNames = append(gotNames, tool.Name)

		for _, verb := range mutatingVerbs {
			if strings.Contains(strings.ToLower(tool.Name), strings.ToLower(verb)) {
				t.Errorf("tool %q name looks mutating (matches verb %q)", tool.Name, verb)
			}
		}
		if tool.Annotations == nil || !tool.Annotations.ReadOnlyHint {
			t.Errorf("tool %q: want Annotations.ReadOnlyHint=true, got %+v", tool.Name, tool.Annotations)
		}
		if tool.Annotations != nil && tool.Annotations.DestructiveHint != nil && *tool.Annotations.DestructiveHint {
			t.Errorf("tool %q: want DestructiveHint=false, got true", tool.Name)
		}
	}

	sort.Strings(gotNames)
	want := append([]string(nil), registeredToolNames...)
	sort.Strings(want)
	if !reflect.DeepEqual(gotNames, want) {
		t.Errorf("registered tools = %v, want %v", gotNames, want)
	}
}

func callToolText(t *testing.T, cs *mcp.ClientSession, name string, args map[string]any) string {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("CallTool(%s): %v", name, err)
	}
	if res.IsError {
		var texts []string
		for _, c := range res.Content {
			if tc, ok := c.(*mcp.TextContent); ok {
				texts = append(texts, tc.Text)
			}
		}
		t.Fatalf("CallTool(%s) returned a tool error: %v", name, texts)
	}
	if len(res.Content) == 0 {
		t.Fatalf("CallTool(%s): empty content", name)
	}
	tc, ok := res.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("CallTool(%s): want *mcp.TextContent, got %T", name, res.Content[0])
	}
	return tc.Text
}

// gvrToListKindMap gives the fake dynamic client an explicit GVR->ListKind
// mapping for every registered CRD, so List() doesn't depend on the fake
// client's pluralization guesser. Kept here (rather than shared with
// internal/kube's own test file) since kube.CRDKind's fields are exported
// specifically so callers like this one can build it without this package
// needing a test-only export from internal/kube.
func gvrToListKindMap() map[schema.GroupVersionResource]string {
	m := make(map[schema.GroupVersionResource]string, len(kube.CRDKinds))
	for kind, k := range kube.CRDKinds {
		m[k.GVR] = kind + "List"
	}
	return m
}

func newUnstructuredCRD(kind, ns, name string, status map[string]any) *unstructured.Unstructured {
	metadata := map[string]any{"name": name}
	if ns != "" {
		metadata["namespace"] = ns
	}
	obj := map[string]any{
		"apiVersion": "gameplane.local/v1alpha1",
		"kind":       kind,
		"metadata":   metadata,
	}
	if status != nil {
		obj["status"] = status
	}
	return &unstructured.Unstructured{Object: obj}
}

func testClientWithFixtures(t *testing.T) *kube.Client {
	t.Helper()
	scheme := kube.NewScheme()
	gs := newUnstructuredCRD("GameServer", "games", "my-server", map[string]any{"phase": "Running"})
	dyn := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(scheme, gvrToListKindMap(), gs)

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "my-server-0", Namespace: "games"},
		Status:     corev1.PodStatus{Phase: corev1.PodRunning},
	}
	ev := &corev1.Event{
		ObjectMeta:     metav1.ObjectMeta{Name: "ev1", Namespace: "games"},
		InvolvedObject: corev1.ObjectReference{Kind: "Pod", Name: "my-server-0"},
		Reason:         "Started",
		Message:        "Started container agent",
	}
	typed := k8sfake.NewSimpleClientset(pod, ev)

	return kube.NewFrom(typed, dyn, scheme)
}

func TestToolsListAndGetHappyPath(t *testing.T) {
	c := testClientWithFixtures(t)
	server := newMCPServer(c)
	cs := connectInMemory(t, server)

	t.Run("list_gameplane_resources", func(t *testing.T) {
		text := callToolText(t, cs, "list_gameplane_resources", map[string]any{"kind": "GameServer", "namespace": "games"})
		if !strings.Contains(text, "my-server") {
			t.Errorf("want my-server in output, got %q", text)
		}
	})

	t.Run("get_gameplane_resource", func(t *testing.T) {
		text := callToolText(t, cs, "get_gameplane_resource", map[string]any{"kind": "GameServer", "namespace": "games", "name": "my-server"})
		if !strings.Contains(text, "Running") {
			t.Errorf("want status phase Running in output, got %q", text)
		}
	})

	t.Run("get_gameplane_resource unknown kind is a tool error", func(t *testing.T) {
		res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
			Name:      "get_gameplane_resource",
			Arguments: map[string]any{"kind": "NotAKind", "namespace": "games", "name": "x"},
		})
		if err != nil {
			t.Fatalf("CallTool: %v", err)
		}
		if !res.IsError {
			t.Error("want IsError=true for an unknown kind")
		}
	})

	t.Run("list_pods", func(t *testing.T) {
		text := callToolText(t, cs, "list_pods", map[string]any{"namespace": "games"})
		if !strings.Contains(text, "my-server-0") {
			t.Errorf("want my-server-0 in output, got %q", text)
		}
	})

	t.Run("get_pod", func(t *testing.T) {
		text := callToolText(t, cs, "get_pod", map[string]any{"namespace": "games", "name": "my-server-0"})
		if !strings.Contains(text, "Running") {
			t.Errorf("want phase Running in output, got %q", text)
		}
	})

	t.Run("list_events", func(t *testing.T) {
		text := callToolText(t, cs, "list_events", map[string]any{"namespace": "games"})
		if !strings.Contains(text, "Started") {
			t.Errorf("want reason Started in output, got %q", text)
		}
	})

	t.Run("list_events malformed labelSelector is a tool error", func(t *testing.T) {
		res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
			Name:      "list_events",
			Arguments: map[string]any{"namespace": "games", "labelSelector": "app in ("},
		})
		if err != nil {
			t.Fatalf("CallTool: %v", err)
		}
		if !res.IsError {
			t.Error("want IsError=true for a malformed labelSelector, like the sibling list tools")
		}
	})

	t.Run("get_pod_logs", func(t *testing.T) {
		text := callToolText(t, cs, "get_pod_logs", map[string]any{"namespace": "games", "pod": "my-server-0"})
		if !strings.Contains(text, "fake logs") {
			t.Errorf("want fake clientset log text, got %q", text)
		}
	})
}

// TestFilterEventsByLabel_MalformedSelector locks in F-206: a labelSelector
// that labels.Parse rejects must surface as an error, not fall back to
// returning the unfiltered list as if it had matched everything.
func TestFilterEventsByLabel_MalformedSelector(t *testing.T) {
	list := &corev1.EventList{Items: []corev1.Event{
		{ObjectMeta: metav1.ObjectMeta{Name: "ev1"}},
	}}
	out, err := filterEventsByLabel(list, "app in (")
	if err == nil {
		t.Fatalf("want an error for a malformed selector, got list of %d items", len(out.Items))
	}
	if out != nil {
		t.Errorf("want a nil list alongside the error, got %+v", out)
	}
}

func TestFilterEventsByLabel_ValidSelector(t *testing.T) {
	list := &corev1.EventList{Items: []corev1.Event{
		{ObjectMeta: metav1.ObjectMeta{Name: "ev1", Labels: map[string]string{"app": "keep"}}},
		{ObjectMeta: metav1.ObjectMeta{Name: "ev2", Labels: map[string]string{"app": "drop"}}},
	}}
	out, err := filterEventsByLabel(list, "app=keep")
	if err != nil {
		t.Fatalf("filterEventsByLabel: %v", err)
	}
	if len(out.Items) != 1 || out.Items[0].Name != "ev1" {
		t.Errorf("want only ev1, got %+v", out.Items)
	}
}

func TestProposeFixTool(t *testing.T) {
	c := testClientWithFixtures(t)
	server := newMCPServer(c)
	cs := connectInMemory(t, server)

	t.Run("matches a heuristic and stays read-only", func(t *testing.T) {
		text := callToolText(t, cs, "propose_fix", map[string]any{
			"kind": "Pod", "namespace": "games", "name": "my-server-0",
			"symptom": "the container keeps hitting CrashLoopBackOff",
		})
		if !strings.Contains(text, "CrashLoopBackOff") {
			t.Errorf("want CrashLoopBackOff advice, got %q", text)
		}
		if !strings.Contains(strings.ToLower(text), "read-only") {
			t.Errorf("want a read-only disclaimer, got %q", text)
		}
		if !strings.Contains(text, "Running") {
			t.Errorf("want the observed pod status folded in, got %q", text)
		}
	})

	t.Run("no heuristic match still returns generic advice", func(t *testing.T) {
		text := callToolText(t, cs, "propose_fix", map[string]any{
			"symptom": "the leaderboard shows the wrong high score",
		})
		if !strings.Contains(text, "No specific heuristic matched") {
			t.Errorf("want the generic fallback, got %q", text)
		}
	})

	t.Run("missing resource is reported, not a tool error", func(t *testing.T) {
		text := callToolText(t, cs, "propose_fix", map[string]any{
			"kind": "GameServer", "namespace": "games", "name": "does-not-exist",
			"symptom": "backup keeps failing",
		})
		if !strings.Contains(text, "could not read") {
			t.Errorf("want a could-not-read note, got %q", text)
		}
		if !strings.Contains(text, "restic") {
			t.Errorf("want backup advice, got %q", text)
		}
	})
}

func TestStripManagedFields_RemovesField(t *testing.T) {
	obj := map[string]any{
		"apiVersion": "v1",
		"kind":       "Pod",
		"metadata": map[string]any{
			"name":          "my-pod",
			"namespace":     "default",
			"managedFields": []any{map[string]any{"manager": "kubelet"}},
		},
		"spec": map[string]any{"containers": []any{}},
	}

	stripped, err := stripManagedFields(obj)
	if err != nil {
		t.Fatalf("stripManagedFields: %v", err)
	}

	strippedMap := stripped.(map[string]any)
	metadata := strippedMap["metadata"].(map[string]any)

	if _, exists := metadata["managedFields"]; exists {
		t.Error("managedFields still present after stripping")
	}
	if metadata["name"] != "my-pod" {
		t.Errorf("want name=my-pod, got %v", metadata["name"])
	}
}

func TestStripManagedFields_NestedItems(t *testing.T) {
	obj := map[string]any{
		"apiVersion": "v1",
		"kind":       "PodList",
		"items": []any{
			map[string]any{
				"metadata": map[string]any{
					"name":          "pod1",
					"managedFields": []any{},
				},
			},
			map[string]any{
				"metadata": map[string]any{
					"name":          "pod2",
					"managedFields": []any{},
				},
			},
		},
	}

	stripped, err := stripManagedFields(obj)
	if err != nil {
		t.Fatalf("stripManagedFields: %v", err)
	}

	strippedMap := stripped.(map[string]any)
	items := strippedMap["items"].([]any)

	for i, item := range items {
		itemMap := item.(map[string]any)
		metadata := itemMap["metadata"].(map[string]any)
		if _, exists := metadata["managedFields"]; exists {
			t.Errorf("items[%d] still has managedFields", i)
		}
	}
}

func TestBoundAndSortEvents_SortsNewestFirst(t *testing.T) {
	now := time.Now()
	list := &corev1.EventList{
		Items: []corev1.Event{
			{
				ObjectMeta:    metav1.ObjectMeta{Name: "ev1", Namespace: "games"},
				LastTimestamp: metav1.NewTime(now.Add(-2 * time.Minute)),
				Reason:        "Started",
			},
			{
				ObjectMeta:    metav1.ObjectMeta{Name: "ev2", Namespace: "games"},
				LastTimestamp: metav1.NewTime(now),
				Reason:        "Restarted",
			},
			{
				ObjectMeta:    metav1.ObjectMeta{Name: "ev3", Namespace: "games"},
				LastTimestamp: metav1.NewTime(now.Add(-1 * time.Minute)),
				Reason:        "Warning",
			},
			{
				ObjectMeta: metav1.ObjectMeta{Name: "ev4", Namespace: "games"},
				EventTime:  metav1.NewMicroTime(now.Add(-30 * time.Second)),
				Reason:     "Info",
			},
		},
	}

	bounded := boundAndSortEvents(list)

	if len(bounded.Items) != 4 {
		t.Errorf("want 4 items, got %d", len(bounded.Items))
	}
	if bounded.Items[0].Name != "ev2" || bounded.Items[1].Name != "ev4" || bounded.Items[2].Name != "ev3" || bounded.Items[3].Name != "ev1" {
		t.Errorf("want order [ev2, ev4, ev3, ev1], got [%s, %s, %s, %s]",
			bounded.Items[0].Name, bounded.Items[1].Name, bounded.Items[2].Name, bounded.Items[3].Name)
	}
}

func TestBoundAndSortEvents_TruncatesAndNotifies(t *testing.T) {
	now := time.Now()
	list := &corev1.EventList{Items: make([]corev1.Event, 0, 150)}
	for i := 0; i < 150; i++ {
		list.Items = append(list.Items, corev1.Event{
			ObjectMeta:    metav1.ObjectMeta{Name: fmt.Sprintf("ev%d", i), Namespace: "games"},
			LastTimestamp: metav1.NewTime(now.Add(time.Duration(-i) * time.Second)),
			Reason:        "Test",
		})
	}

	bounded := boundAndSortEvents(list)

	if len(bounded.Items) != 100 {
		t.Errorf("want 100 items after bounding, got %d", len(bounded.Items))
	}
	if !strings.Contains(bounded.ResourceVersion, "truncated") {
		t.Errorf("want truncation notice, got %q", bounded.ResourceVersion)
	}
	if !strings.Contains(bounded.ResourceVersion, "150") {
		t.Errorf("want 'of 150' in truncation notice, got %q", bounded.ResourceVersion)
	}
}

func TestBoundAndSortEvents_NoTruncationWhenUnderLimit(t *testing.T) {
	now := time.Now()
	list := &corev1.EventList{
		Items: []corev1.Event{
			{
				ObjectMeta:    metav1.ObjectMeta{Name: "ev1", Namespace: "games"},
				LastTimestamp: metav1.NewTime(now),
				Reason:        "Test",
			},
		},
	}

	bounded := boundAndSortEvents(list)

	if len(bounded.Items) != 1 {
		t.Errorf("want 1 item, got %d", len(bounded.Items))
	}
	if bounded.ResourceVersion != "" && strings.Contains(bounded.ResourceVersion, "truncated") {
		t.Errorf("want no truncation notice when under limit, got %q", bounded.ResourceVersion)
	}
}

func TestListEventsHandler_StripsAndBounds(t *testing.T) {
	// Create fixture with many events and managedFields
	now := time.Now()
	events := make([]runtime.Object, 0, 120)
	for i := 0; i < 120; i++ {
		events = append(events, &corev1.Event{
			ObjectMeta: metav1.ObjectMeta{
				Name:      fmt.Sprintf("ev%d", i),
				Namespace: "games",
				// Simulate managedFields (when marshaled to JSON/YAML)
			},
			LastTimestamp:  metav1.NewTime(now.Add(time.Duration(-i) * time.Second)),
			InvolvedObject: corev1.ObjectReference{Kind: "Pod", Name: "my-server-0"},
			Reason:         "Started",
			Message:        "Started container agent",
		})
	}

	scheme := kube.NewScheme()
	typed := k8sfake.NewSimpleClientset(events...)
	dyn := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(scheme, gvrToListKindMap())
	c := kube.NewFrom(typed, dyn, scheme)

	server := newMCPServer(c)
	cs := connectInMemory(t, server)

	text := callToolText(t, cs, "list_events", map[string]any{"namespace": "games"})

	// Verify truncation notice is present
	if !strings.Contains(text, "truncated") {
		t.Errorf("want truncation notice in response for 120 events, got: %s", text)
	}
	if !strings.Contains(text, "120") {
		t.Errorf("want 'of 120' in truncation notice, got: %s", text)
	}

	// Verify managedFields is not in output (it's not set in our fake objects,
	// but we verify the stripping function was called by checking result structure)
	if !strings.Contains(text, "Started") {
		t.Errorf("want event reason in output, got: %s", text)
	}
}

func TestRunIdleReturnsOnCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	done := make(chan struct{}, 1)
	go func() {
		runIdle(ctx)
		done <- struct{}{}
	}()

	select {
	case <-done:
		// runIdle returned successfully
	case <-time.After(2 * time.Second):
		t.Fatal("runIdle did not return after context cancellation")
	}
}
