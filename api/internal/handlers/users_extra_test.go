package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/GameplanePanel/gameplane/api/internal/auth"
)

func TestUsers_Me_Authenticated(t *testing.T) {
	srv, _, _ := newUsersServer(t, &auth.User{
		ID: 42, Username: "root", DisplayName: "Root", Email: "r@x", Role: "admin",
	})
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, srv.URL+"/users/me", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status=%d", resp.StatusCode)
	}
	var got userDTO
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Username != "root" || got.Role != "admin" {
		t.Fatalf("got %+v", got)
	}
}

func TestUsers_Me_NoUser(t *testing.T) {
	srv, _, _ := newUsersServer(t, nil) // no caller injected
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, srv.URL+"/users/me", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 401 {
		t.Fatalf("status=%d", resp.StatusCode)
	}
}

func TestUsers_Del_BadID(t *testing.T) {
	srv, _, _ := newUsersServer(t, &auth.User{ID: 1, Role: "admin"})
	req, _ := newReqWithCSRF(srv, "DELETE", "/users/abc", "")
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 400 {
		t.Fatalf("status=%d", resp.StatusCode)
	}
}

func TestUsers_Del_Self(t *testing.T) {
	srv, _, _ := newUsersServer(t, &auth.User{ID: 7, Role: "admin"})
	req, _ := newReqWithCSRF(srv, "DELETE", "/users/7", "")
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 400 {
		t.Fatalf("status=%d", resp.StatusCode)
	}
}

// newReqWithCSRF builds a request without auth-cookie middleware in the
// chain. The users router exposes routes under /users/* and our test
// middleware injects auth.User directly.
func newReqWithCSRF(srv *httptest.Server, method, path, body string) (*http.Request, error) {
	return http.NewRequestWithContext(context.Background(), method, srv.URL+path, strings.NewReader(body))
}
