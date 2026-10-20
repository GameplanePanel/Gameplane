package handlers

import (
	"encoding/json"
	"net/http"
	"reflect"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	dynamicfake "k8s.io/client-go/dynamic/fake"

	"github.com/GameplanePanel/gameplane/api/internal/kube"
	"github.com/GameplanePanel/gameplane/api/internal/scope"
)

func TestModIDsRemoteReadsAndUpdatesOnlySelectedServer(t *testing.T) {
	home, remote := modClusterFixture("local"), modClusterFixture("remote")
	router := mountClusterModRouter(modClusterClients(home, remote), &recordingRegistrySet{provider: &fakeProvider{}}, modRouteUser("remote", "servers:read", "servers:write"), nil)
	path := "/servers/alpha/mods/ids?cluster=remote"
	response := do(t, router, http.MethodGet, path, nil)
	var got []ModID
	if err := json.Unmarshal(response.Body.Bytes(), &got); err != nil || response.Code != http.StatusOK || !reflect.DeepEqual(got, []ModID{{ID: "remote-saved", Name: "remote"}}) {
		t.Fatalf("read wrong list: status=%d list=%v err=%v", response.Code, got, err)
	}
	want := []ModID{{ID: "replacement", Name: "Remote mod"}}
	response = do(t, router, http.MethodPut, path, want)
	if err := json.Unmarshal(response.Body.Bytes(), &got); err != nil || response.Code != http.StatusOK || !reflect.DeepEqual(got, want) {
		t.Fatalf("write wrong list: status=%d list=%v err=%v", response.Code, got, err)
	}
	if kubeClientCalls(t, home) != 0 {
		t.Fatal("remote route accessed local namesake")
	}
	gs, err := remote.GetServer(t.Context(), scope.DefaultNamespace, "alpha")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(readModIDs(gs), want) || gs.GetUID() != "remote-uid" || gs.GetResourceVersion() != "17" {
		t.Fatalf("wrong persisted object: %v", gs.Object)
	}
	local, err := home.GetServer(t.Context(), scope.DefaultNamespace, "alpha")
	if err != nil || !reflect.DeepEqual(readModIDs(local), []ModID{{ID: "local-saved", Name: "local"}}) {
		t.Fatalf("local list changed: err=%v", err)
	}
}

func TestModIDsRemoteCapabilityComesFromRemoteTemplate(t *testing.T) {
	home, remote := modClusterFixture("local"), modClusterFixture("remote")
	tmpl, err := remote.Dynamic.Resource(kube.GVRs["templates"]).Get(t.Context(), "same-template", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	unstructured.RemoveNestedField(tmpl.Object, "spec", "capabilities", "mods", "idList")
	if _, err := remote.Dynamic.Resource(kube.GVRs["templates"]).Update(t.Context(), tmpl, metav1.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	router := mountClusterModRouter(modClusterClients(home, remote), &recordingRegistrySet{provider: &fakeProvider{}}, modRouteUser("remote", "servers:read", "servers:write"), nil)
	for _, method := range []string{http.MethodGet, http.MethodPut} {
		response := do(t, router, method, "/servers/alpha/mods/ids?cluster=remote", []ModID{{ID: "new"}})
		if response.Code != http.StatusNotImplemented || kubeClientCalls(t, home) != 0 {
			t.Fatalf("%s borrowed local capability: status=%d", method, response.Code)
		}
	}
}

func TestRemoteModWritesRejectInvalidInputsWithoutMutating(t *testing.T) {
	for _, test := range []struct {
		method, path string
		body         any
	}{
		{http.MethodPut, "/servers/alpha/mods/ids", []ModID{{ID: "../invalid"}}},
		{http.MethodPost, "/servers/alpha/modpack", map[string]any{"ref": "  "}},
	} {
		t.Run(test.path, func(t *testing.T) {
			home, remote := modClusterFixture("local"), modClusterFixture("remote")
			router := mountClusterModRouter(modClusterClients(home, remote), &recordingRegistrySet{provider: &fakeProvider{}}, modRouteUser("remote", "servers:write"), nil)
			response := do(t, router, test.method, test.path+"?cluster=remote", test.body)
			if response.Code != http.StatusBadRequest || kubeClientCalls(t, home) != 0 {
				t.Fatalf("invalid input status=%d home calls=%d", response.Code, kubeClientCalls(t, home))
			}
			for _, action := range remote.Dynamic.(*dynamicfake.FakeDynamicClient).Actions() {
				if action.GetVerb() != "get" {
					t.Fatalf("invalid input caused %s", action.GetVerb())
				}
			}
		})
	}
}
