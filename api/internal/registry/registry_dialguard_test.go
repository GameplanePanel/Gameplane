package registry

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/GameplanePanel/gameplane/netguard"
)

// TestSet_RegistryFetchesDialOnlyPublicAddresses checks that the client
// shared by every engine in a Set refuses non-public destinations at dial
// time: a link-local address, and a loopback listener that is really up.
func TestSet_RegistryFetchesDialOnlyPublicAddresses(t *testing.T) {
	ctx := context.Background()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"hits":[]}`))
	}))
	defer srv.Close()

	for name, base := range map[string]string{
		"link-local": "http://169.254.169.254/v2",
		"loopback":   srv.URL,
	} {
		s := NewSet("test", StaticKeys(map[string]string{}))
		s.modrinth.baseURL = base
		_, err := s.modrinth.Search(ctx, SearchQuery{Term: "x"})
		if !errors.Is(err, netguard.ErrBlockedAddr) {
			t.Errorf("%s: err = %v, want netguard.ErrBlockedAddr", name, err)
		}
	}
}

// TestSet_KeyedEnginesShareTheGuardedClient checks that a keyed engine
// built lazily by the Set uses the same guarded client as the keyless ones.
func TestSet_KeyedEnginesShareTheGuardedClient(t *testing.T) {
	ctx := context.Background()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[]}`))
	}))
	defer srv.Close()

	s := NewSet("test", StaticKeys(map[string]string{"curseforge": "cf-key"}))
	cf, err := s.curseforgeLazy(ctx)
	if err != nil || cf == nil {
		t.Fatalf("curseforgeLazy: cf=%v err=%v", cf, err)
	}
	if cf.client != s.client {
		t.Fatal("keyed engine does not share the Set's guarded client")
	}
	var out map[string]any
	if err := cf.get(ctx, srv.URL+"/v1/mods/search", &out); !errors.Is(err, netguard.ErrBlockedAddr) {
		t.Fatalf("err = %v, want netguard.ErrBlockedAddr", err)
	}
}
