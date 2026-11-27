package kube

import (
	"errors"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	k8stesting "k8s.io/client-go/testing"
)

func listServersFake(objs ...runtime.Object) *dynamicfake.FakeDynamicClient {
	return dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), map[schema.GroupVersionResource]string{
		GVRs["servers"]: "GameServerList",
	}, objs...)
}

func listServersObj(ns, name string) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "gameplane.local/v1alpha1",
		"kind":       "GameServer",
		"metadata":   map[string]any{"name": name, "namespace": ns},
	}}
}

func TestRegistryListServers_UnregisteredCluster(t *testing.T) {
	r := NewRegistry("local")
	if _, err := r.ListServers(t.Context(), "ghost", "gameplane-games"); err == nil {
		t.Fatal("ListServers on an unregistered cluster returned nil error")
	}
}

func TestRegistryListServers_ListsOnlyTheNamespace(t *testing.T) {
	dyn := listServersFake(
		listServersObj("gameplane-games", "alpha"),
		listServersObj("gameplane-games", "beta"),
		listServersObj("other", "gamma"),
	)
	r := NewRegistry("local")
	r.Set("local", &Client{Dynamic: dyn})

	items, err := r.ListServers(t.Context(), "local", "gameplane-games")
	if err != nil {
		t.Fatalf("ListServers: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("ListServers returned %d items, want 2", len(items))
	}
	got := map[string]bool{}
	for i := range items {
		got[items[i].GetName()] = true
	}
	if !got["alpha"] || !got["beta"] || got["gamma"] {
		t.Fatalf("items = %v, want alpha and beta only", got)
	}
}

func TestRegistryListServers_ListErrorIsWrapped(t *testing.T) {
	boom := errors.New("apiserver down")
	dyn := listServersFake()
	dyn.PrependReactor("list", "*", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, boom
	})
	r := NewRegistry("local")
	r.Set("local", &Client{Dynamic: dyn})

	if _, err := r.ListServers(t.Context(), "local", "gameplane-games"); !errors.Is(err, boom) {
		t.Fatalf("ListServers err = %v, want it to wrap the list error", err)
	}
}
