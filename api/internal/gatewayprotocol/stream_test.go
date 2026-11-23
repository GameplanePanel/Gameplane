package gatewayprotocol

import (
	"net/http"
	"testing"
)

func TestGatewayFileStreamRoutes(t *testing.T) {
	if limit, ok := Allowed(http.MethodPut, "/files/stream"); !ok || limit != DefaultStreamBodyLimit {
		t.Errorf("PUT /files/stream = (%d, %v), want (%d, true)", limit, ok, DefaultStreamBodyLimit)
	}
	if _, ok := Allowed(http.MethodPut, "/files/write"); ok {
		t.Error("PUT /files/write allowed, want denied")
	}
	if _, ok := Allowed(http.MethodPut, "/files/create"); ok {
		t.Error("PUT /files/create allowed, want denied")
	}
	if _, ok := Allowed(http.MethodGet, "/files/stat"); !ok {
		t.Error("GET /files/stat denied, want allowed")
	}
	if limit, ok := Allowed(http.MethodPost, "/files/rename"); !ok || limit != 1<<20 {
		t.Errorf("POST /files/rename = (%d, %v), want (%d, true)", limit, ok, 1<<20)
	}

	target := Target{Cluster: "local", Namespace: "gameplane-games", Name: "srv", UID: "uid-1"}
	for _, agentPath := range []string{"/files/stat", "/files/rename", "/files/stream"} {
		if _, err := Path(target, agentPath); err != nil {
			t.Errorf("Path(%s) error: %v", agentPath, err)
		}
	}
}

func TestGatewayFileStreamLongRunning(t *testing.T) {
	if !LongRunningOperation(http.MethodPut, "/files/stream") {
		t.Error("PUT /files/stream is not long running")
	}
	if LongRunningOperation(http.MethodPut, "/files/write") {
		t.Error("PUT /files/write is long running")
	}
}
