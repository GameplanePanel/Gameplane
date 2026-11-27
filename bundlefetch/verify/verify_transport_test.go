package verify

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/GameplanePanel/gameplane/netguard"
	"github.com/google/go-containerregistry/pkg/authn"
)

// TestKeyedVerify_SignatureFetchIsNetguarded proves signature fetches use the
// guarded transport: a registry at a metadata address must be refused at dial time.
func TestKeyedVerify_SignatureFetchIsNetguarded(t *testing.T) {
	v, err := newKeyed(context.Background(), testPubPEM(t), authn.Anonymous, false, false)
	if err != nil {
		t.Fatalf("newKeyed: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	err = v.Verify(ctx, "169.254.169.254/x/y", "sha256:"+strings.Repeat("0", 64))
	if err == nil {
		t.Fatal("expected an error for a metadata-address registry")
	}
	if !errors.Is(err, netguard.ErrBlockedAddr) && !strings.Contains(err.Error(), netguard.ErrBlockedAddr.Error()) {
		t.Fatalf("expected netguard block error, got: %v", err)
	}
}
