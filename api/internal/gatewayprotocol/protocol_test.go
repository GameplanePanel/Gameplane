package gatewayprotocol

import (
	"net/http"
	"testing"
)

func TestPathRoundTrip(t *testing.T) {
	target := Target{Cluster: "remote", Namespace: "games", Name: "same-name", UID: "original-uid"}
	path, err := Path(target, "/files/write")
	if err != nil {
		t.Fatal(err)
	}
	parsed, operation, err := ParsePath(path)
	if err != nil || parsed != target || operation != "/files/write" {
		t.Fatalf("parse: %+v %q %v", parsed, operation, err)
	}
	for _, bad := range []string{path + "/../read", path + "/", "/v2" + path[3:], "/v1/clusters/remote/namespaces/games/servers/same-name/uids//files/write"} {
		if _, _, err := ParsePath(bad); err == nil {
			t.Fatalf("accepted malformed path %q", bad)
		}
	}
	for _, name := range []string{"bad/name", "..", "name?redirect=evil", "name%2f"} {
		target.Name = name
		if _, err := Path(target, "/files/write"); err == nil {
			t.Fatalf("accepted target %q", name)
		}
	}
}

func TestOperationAllowlist(t *testing.T) {
	for _, tc := range []struct {
		method, path string
		limit        int64
		allowed      bool
	}{
		{http.MethodGet, "/console", 0, true},
		{http.MethodPost, "/files/write", 64 << 20, true},
		{http.MethodPost, "/files/create", 64 << 20, true},
		{http.MethodGet, "/files/create", 0, false},
		{http.MethodPut, "/files/create", 0, false},
		{http.MethodPost, "/mods/upload", 512 << 20, true},
		{http.MethodPost, "/actions/run", 16 << 10, true},
		{http.MethodGet, "/files/write", 0, false},
		{http.MethodPost, "/quiesce", 0, false},
		{http.MethodPost, "/lifecycle/stop", 0, false},
		{http.MethodGet, "https://other/console", 0, false},
	} {
		limit, ok := Allowed(tc.method, tc.path)
		if limit != tc.limit || ok != tc.allowed {
			t.Errorf("%s %s = %d %v", tc.method, tc.path, limit, ok)
		}
	}
}
