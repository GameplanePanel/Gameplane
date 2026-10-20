package handlers

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/GameplanePanel/gameplane/api/internal/kube"
)

func TestStandaloneDiscoveryDoesNotInventLocalCluster(t *testing.T) {
	home := fakeKubeClientWithClusters()
	reg := kube.NewRegistry("local")
	reg.SetManagement(home)
	rr := doClusters(t, mountClustersRouter(home, reg), http.MethodGet, "/clusters/", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body)
	}
	var out clustersListResp
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.Items == nil || len(out.Items) != 0 {
		t.Fatalf("expected empty array, got %+v", out.Items)
	}
}

func TestStandaloneFleetDiscoversOnlyManagementRegistrations(t *testing.T) {
	home := fakeKubeClientWithClusters(newCluster("offline", nil, nil))
	reg := kube.NewRegistry("local")
	reg.SetManagement(home)
	h := fleetHandler{reg: reg}
	items, issues := h.candidates(t.Context(), "")
	if len(issues) != 0 || len(items) != 1 || items[0].id != "offline" {
		t.Fatalf("items=%+v issues=%+v", items, issues)
	}
}

func TestStandaloneFleetEmptyIsComplete(t *testing.T) {
	reg := kube.NewRegistry("local")
	reg.SetManagement(fakeKubeClientWithClusters())
	h := fleetHandler{reg: reg}
	items, issues := h.candidates(t.Context(), "")
	if len(items) != 0 || len(issues) != 0 {
		t.Fatalf("items=%+v issues=%+v", items, issues)
	}
}
