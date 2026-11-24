package registry

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestStripKeysOnHostChange(t *testing.T) {
	via := []*http.Request{httptest.NewRequest("GET", "https://api.curseforge.com/v1/x", nil)}

	t.Run("cross-host drops provider keys", func(t *testing.T) {
		next := httptest.NewRequest("GET", "https://edge.example.com/f", nil)
		next.Header.Set("x-api-key", "k1")
		next.Header.Set("apikey", "k2")
		if err := stripKeysOnHostChange(next, via); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if v := next.Header.Get("x-api-key"); v != "" {
			t.Errorf("x-api-key kept across host change: %q", v)
		}
		if v := next.Header.Get("apikey"); v != "" {
			t.Errorf("apikey kept across host change: %q", v)
		}
	})

	t.Run("same host keeps provider keys", func(t *testing.T) {
		next := httptest.NewRequest("GET", "https://api.curseforge.com/v1/y", nil)
		next.Header.Set("x-api-key", "k1")
		next.Header.Set("apikey", "k2")
		if err := stripKeysOnHostChange(next, via); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if next.Header.Get("x-api-key") != "k1" || next.Header.Get("apikey") != "k2" {
			t.Errorf("keys dropped on same-host redirect: %v", next.Header)
		}
	})

	t.Run("ten redirects stop", func(t *testing.T) {
		long := make([]*http.Request, 10)
		for i := range long {
			long[i] = httptest.NewRequest("GET", "https://api.curseforge.com/v1/x", nil)
		}
		next := httptest.NewRequest("GET", "https://api.curseforge.com/v1/z", nil)
		if err := stripKeysOnHostChange(next, long); err == nil {
			t.Fatal("expected error after 10 redirects, got nil")
		}
	})
}

func TestNewSetSetsCheckRedirect(t *testing.T) {
	s := NewSet("test", nil)
	if s.client.CheckRedirect == nil {
		t.Fatal("shared registry client has no CheckRedirect")
	}
}
