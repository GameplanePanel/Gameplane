package registry

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/GameplanePanel/gameplane/netguard"
)

// TestSet_FTBFetchesDialOnlyPublicAddresses checks that the FTB engine built
// by NewSet uses the Set's netguard.IsPublic client: a link-local address,
// and a loopback listener that is really up, are refused at dial time for
// both the search and the pack-detail paths.
func TestSet_FTBFetchesDialOnlyPublicAddresses(t *testing.T) {
	ctx := context.Background()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"success","packs":[]}`))
	}))
	defer srv.Close()

	for name, base := range map[string]string{
		"link-local": "http://169.254.169.254/v1/modpacks/public/modpack",
		"loopback":   srv.URL,
	} {
		s := NewSet("test", StaticKeys(map[string]string{}))
		if s.ftb.client != s.client {
			t.Fatalf("%s: ftb engine does not share the Set's guarded client", name)
		}
		s.ftb.baseURL = base
		_, err := s.ftb.Search(ctx, SearchQuery{Term: "x", ProjectType: "modpack"})
		if !errors.Is(err, netguard.ErrBlockedAddr) {
			t.Errorf("%s search: err = %v, want netguard.ErrBlockedAddr", name, err)
		}
		_, err = s.ftb.Versions(ctx, "130", Filter{})
		if !errors.Is(err, netguard.ErrBlockedAddr) {
			t.Errorf("%s versions: err = %v, want netguard.ErrBlockedAddr", name, err)
		}
	}
}
