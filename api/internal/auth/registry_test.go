package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/GameplanePanel/gameplane/api/internal/db"
)

func seedConfigRow(t *testing.T, s *db.Store, key, blob string) {
	t.Helper()
	_, err := s.DB.ExecContext(context.Background(),
		`INSERT INTO config(key, value, updated_at) VALUES (?, ?, datetime('now'))
		 ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key, blob)
	if err != nil {
		t.Fatalf("seed config %s: %v", key, err)
	}
}

func seedExternalURL(t *testing.T, s *db.Store) {
	t.Helper()
	seedConfigRow(t, s, "general",
		`{"instanceName":"t","externalURL":"https://gameplane.example","defaultNamespace":"games"}`)
}

func providerRow(name, issuer string, enabled bool) string {
	return fmt.Sprintf(
		`{"name":%q,"kind":"oidc","enabled":%t,"issuer":%q,"clientID":"gameplane"}`,
		name, enabled, issuer)
}

func authRow(entries ...string) string {
	return `{"providers":[{"name":"local","kind":"local","enabled":true},` +
		strings.Join(entries, ",") + `]}`
}

func staticSecrets(m map[string]map[string][]byte) SecretReader {
	return func(_ context.Context, name string) (map[string][]byte, error) {
		data, ok := m[name]
		if !ok {
			return nil, errors.New("secret not found: " + name)
		}
		return data, nil
	}
}

func secretFor(name string) map[string]map[string][]byte {
	return map[string]map[string][]byte{
		"gameplane-auth-" + name: {"clientSecret": []byte("s3cret")},
	}
}

func TestRegistry_DefaultsWithoutRow(t *testing.T) {
	s := newAuthDB(t)
	reg := NewRegistry(s, staticSecrets(nil), nil, "", nil)
	if !reg.LocalEnabled(context.Background()) {
		t.Fatal("missing auth row must mean local login enabled")
	}
	enabled := reg.Enabled(context.Background())
	if len(enabled) != 1 || enabled[0].Kind != "local" {
		t.Fatalf("enabled = %+v, want just local", enabled)
	}
}

func TestRegistry_MalformedRowFailsOpen(t *testing.T) {
	s := newAuthDB(t)
	seedConfigRow(t, s, "auth", `{not json`)
	reg := NewRegistry(s, staticSecrets(nil), nil, "", nil)
	if !reg.LocalEnabled(context.Background()) {
		t.Fatal("malformed auth row must degrade to local login, not a lockout")
	}
}

func TestRegistry_LocalDisabled(t *testing.T) {
	s := newAuthDB(t)
	seedConfigRow(t, s, "auth", `{"providers":[{"name":"local","kind":"local","enabled":false}]}`)
	reg := NewRegistry(s, staticSecrets(nil), nil, "", nil)
	if reg.LocalEnabled(context.Background()) {
		t.Fatal("local disabled in the row must gate local login")
	}
}

// A config save must be visible on the next resolve without a restart —
// the whole point of the registry.
func TestRegistry_PicksUpConfigChangesLive(t *testing.T) {
	s := newAuthDB(t)
	reg := NewRegistry(s, staticSecrets(nil), nil, "", nil)
	ctx := context.Background()

	if got := len(reg.Enabled(ctx)); got != 1 {
		t.Fatalf("initial enabled = %d, want 1 (local)", got)
	}
	idp := newFakeIDP(t, "gameplane")
	seedConfigRow(t, s, "auth", authRow(providerRow("corp", idp.issuer(), true)))
	enabled := reg.Enabled(ctx)
	if len(enabled) != 2 {
		t.Fatalf("after save enabled = %+v, want local + corp", enabled)
	}
	seedConfigRow(t, s, "auth", authRow(providerRow("corp", idp.issuer(), false)))
	if got := len(reg.Enabled(ctx)); got != 1 {
		t.Fatalf("after disable enabled = %d, want 1", got)
	}
}

func TestRegistry_OIDCForBuildsAndCaches(t *testing.T) {
	s := newAuthDB(t)
	seedExternalURL(t, s)
	idp := newFakeIDP(t, "gameplane")
	seedConfigRow(t, s, "auth", authRow(providerRow("corp", idp.issuer(), true)))
	reg := NewRegistry(s, staticSecrets(secretFor("corp")), nil, "", nil)
	ctx := context.Background()

	o, err := reg.OIDCFor(ctx, "corp")
	if err != nil {
		t.Fatalf("OIDCFor: %v", err)
	}
	if o == nil || o.db == nil {
		t.Fatal("built OIDC must have the store attached for account linking")
	}
	hits := idp.discoveryHits.Load()
	// Second resolve with an unchanged row must come from the cache — no
	// new discovery round-trip.
	if _, err := reg.OIDCFor(ctx, "corp"); err != nil {
		t.Fatalf("cached OIDCFor: %v", err)
	}
	if idp.discoveryHits.Load() != hits {
		t.Fatalf("cached resolve re-ran discovery (%d → %d hits)", hits, idp.discoveryHits.Load())
	}
	// A row change (any change — reordering counts) invalidates the cache.
	seedConfigRow(t, s, "auth", authRow(providerRow("corp", idp.issuer(), true), providerRow("other", idp.issuer(), false)))
	if _, err := reg.OIDCFor(ctx, "corp"); err != nil {
		t.Fatalf("post-change OIDCFor: %v", err)
	}
	if idp.discoveryHits.Load() == hits {
		t.Fatal("config change must rebuild the provider")
	}
}

func TestRegistry_UnknownDisabledLocal(t *testing.T) {
	s := newAuthDB(t)
	idp := newFakeIDP(t, "gameplane")
	seedConfigRow(t, s, "auth", authRow(providerRow("off", idp.issuer(), false)))
	reg := NewRegistry(s, staticSecrets(nil), nil, "", nil)
	ctx := context.Background()
	for _, name := range []string{"ghost", "off", "local"} {
		if _, err := reg.OIDCFor(ctx, name); !errors.Is(err, ErrUnknownProvider) {
			t.Fatalf("OIDCFor(%q) = %v, want ErrUnknownProvider", name, err)
		}
	}
}

func TestRegistry_BuildErrorBacksOff(t *testing.T) {
	s := newAuthDB(t)
	seedExternalURL(t, s)
	// An issuer that always 500s: construction fails, and the failure
	// must be remembered instead of re-dialed on the next click.
	var hits atomic.Int32
	broken := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(broken.Close)
	seedConfigRow(t, s, "auth", authRow(providerRow("corp", broken.URL, true)))
	reg := NewRegistry(s, staticSecrets(secretFor("corp")), nil, "", nil)
	now := time.Now()
	reg.now = func() time.Time { return now }
	ctx := context.Background()

	if _, err := reg.OIDCFor(ctx, "corp"); err == nil {
		t.Fatal("expected a build error from the broken issuer")
	}
	before := hits.Load()
	if _, err := reg.OIDCFor(ctx, "corp"); err == nil {
		t.Fatal("expected the cached build error")
	}
	if hits.Load() != before {
		t.Fatal("second resolve inside the backoff window must not re-dial the issuer")
	}
	// Past the backoff window the registry tries again.
	reg.now = func() time.Time { return now.Add(errorBackoffTTL + time.Second) }
	if _, err := reg.OIDCFor(ctx, "corp"); err == nil {
		t.Fatal("expected another build error")
	}
	if hits.Load() == before {
		t.Fatal("resolve after the backoff window must re-dial the issuer")
	}
}

func TestRegistry_MissingSecretAndKeys(t *testing.T) {
	s := newAuthDB(t)
	seedExternalURL(t, s)
	idp := newFakeIDP(t, "gameplane")
	seedConfigRow(t, s, "auth", authRow(providerRow("corp", idp.issuer(), true)))

	// No Secret at all.
	reg := NewRegistry(s, staticSecrets(nil), nil, "", nil)
	if _, err := reg.OIDCFor(context.Background(), "corp"); err == nil || errors.Is(err, ErrUnknownProvider) {
		t.Fatalf("missing secret: err = %v, want a build error", err)
	}
	// Secret present but without the clientSecret key.
	reg = NewRegistry(s, staticSecrets(map[string]map[string][]byte{
		"gameplane-auth-corp": {"wrong": []byte("x")},
	}), nil, "", nil)
	if _, err := reg.OIDCFor(context.Background(), "corp"); err == nil || !strings.Contains(err.Error(), "clientSecret") {
		t.Fatalf("missing key: err = %v, want clientSecret complaint", err)
	}
}

func TestRegistry_RequiresExternalURL(t *testing.T) {
	s := newAuthDB(t)
	idp := newFakeIDP(t, "gameplane")
	seedConfigRow(t, s, "auth", authRow(providerRow("corp", idp.issuer(), true)))
	reg := NewRegistry(s, staticSecrets(secretFor("corp")), nil, "", nil)
	if _, err := reg.OIDCFor(context.Background(), "corp"); err == nil || !strings.Contains(err.Error(), "External URL") {
		t.Fatalf("err = %v, want the externalURL guidance", err)
	}
}

func TestRegistry_HelmProvider(t *testing.T) {
	s := newAuthDB(t)
	idp := newFakeIDP(t, "gameplane")
	legacy, err := NewOIDC(context.Background(), idp.issuer(), "gameplane", "sec", "https://gameplane.example/auth/oidc/callback")
	if err != nil {
		t.Fatalf("legacy oidc: %v", err)
	}
	reg := NewRegistry(s, staticSecrets(nil), legacy, "Acme SSO", nil)
	ctx := context.Background()

	if legacy.db == nil {
		t.Fatal("NewRegistry must attach the store to the legacy provider")
	}
	o, err := reg.OIDCFor(ctx, HelmProviderName)
	if err != nil || o != legacy {
		t.Fatalf("OIDCFor(helm) = %v, %v — want the legacy instance", o, err)
	}
	var helm *Provider
	for _, p := range reg.Enabled(ctx) {
		if p.Name == HelmProviderName {
			helm = &p
			break
		}
	}
	if helm == nil || helm.Label() != "Acme SSO" || helm.Kind != "oidc" {
		t.Fatalf("helm listing = %+v", helm)
	}

	// Without the flags there is no helm provider.
	none := NewRegistry(s, staticSecrets(nil), nil, "", nil)
	if _, err := none.OIDCFor(ctx, HelmProviderName); err == nil {
		t.Fatal("OIDCFor(helm) without flags must fail")
	}
}

// The redirect URL is derived from externalURL + the provider's route.
func TestRegistry_RedirectURL(t *testing.T) {
	s := newAuthDB(t)
	seedExternalURL(t, s)
	reg := NewRegistry(s, staticSecrets(nil), nil, "", nil)
	got, err := reg.redirectURL(context.Background(), "corp")
	if err != nil {
		t.Fatalf("redirectURL: %v", err)
	}
	if got != "https://gameplane.example/auth/oidc/corp/callback" {
		t.Fatalf("redirectURL = %q", got)
	}
}

// Local gating end to end: HandleLogin refuses with a neutral 403 when
// the config disables local, before reading any credentials.
func TestLogin_DisabledByRegistry(t *testing.T) {
	s := newAuthDB(t)
	seedConfigRow(t, s, "auth", `{"providers":[{"name":"local","kind":"local","enabled":false}]}`)
	reg := NewRegistry(s, staticSecrets(nil), nil, "", nil)
	// A username no other test in this package logs in with:
	// LoginUserLimiter is a package singleton keyed by username, and the
	// local_test logins would otherwise have drained "alice"'s bucket by
	// the time this file's tests run.
	seedUser(t, s, "registry-alice", "correct-horse-battery", "admin")

	rr := httptest.NewRecorder()
	body, _ := json.Marshal(map[string]string{"username": "registry-alice", "password": "correct-horse-battery"})
	req := httptest.NewRequestWithContext(context.Background(), "POST", "/login", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	NewLocal(s).HandleLogin(NewSessionStore(s), reg).ServeHTTP(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("code = %d, want 403 with local disabled", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "login method disabled") {
		t.Fatalf("body = %q", rr.Body.String())
	}

	// Re-enabling in the row takes effect on the next request.
	seedConfigRow(t, s, "auth", `{"providers":[{"name":"local","kind":"local","enabled":true}]}`)
	rr = httptest.NewRecorder()
	req = httptest.NewRequestWithContext(context.Background(), "POST", "/login", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	NewLocal(s).HandleLogin(NewSessionStore(s), reg).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("code = %d after re-enable, want 200 (%s)", rr.Code, rr.Body)
	}
}

// Dashboard-managed OIDC providers audit their role assignments the same
// way the Helm provider does, naming the provider in the audit reason.
func TestRegistry_DashboardProviderAuditsRoleAssignment(t *testing.T) {
	s := newAuthDB(t)
	seedExternalURL(t, s)
	idp := newFakeIDP(t, "gameplane")
	idp.nonce = "nonce-dash"
	idp.groups = []string{"corp-admins"}
	seedConfigRow(t, s, "auth", authRow(fmt.Sprintf(
		`{"name":"corp","kind":"oidc","enabled":true,"issuer":%q,"clientID":"gameplane","roleMappings":{"admin":["corp-admins"]}}`,
		idp.issuer())))
	reg := NewRegistry(s, staticSecrets(secretFor("corp")), nil, "", nil)
	rec := &auditWriteRecorder{}
	reg.AttachAuditWriteSyncFunc(rec.write)

	o, err := reg.OIDCFor(context.Background(), "corp")
	if err != nil {
		t.Fatalf("OIDCFor: %v", err)
	}
	if rr := callbackViaIDP(t, o, NewSessionStore(s), "nonce-dash"); rr.Code != http.StatusFound {
		t.Fatalf("login: code=%d body=%q", rr.Code, rr.Body)
	}
	if len(rec.reasons) != 1 {
		t.Fatalf("audit events = %d (%v), want 1", len(rec.reasons), rec.reasons)
	}
	if want := "oidc role assigned: provider=corp matched=corp-admins from=new_user to=admin"; rec.reasons[0] != want {
		t.Fatalf("audit reason = %q, want %q", rec.reasons[0], want)
	}
}

// Attaching the audit func drops providers built before it, so none keeps
// running without it.
func TestRegistry_AttachAuditWriteSyncFuncRebuildsCachedProviders(t *testing.T) {
	s := newAuthDB(t)
	seedExternalURL(t, s)
	idp := newFakeIDP(t, "gameplane")
	seedConfigRow(t, s, "auth", authRow(providerRow("corp", idp.issuer(), true)))
	reg := NewRegistry(s, staticSecrets(secretFor("corp")), nil, "", nil)
	ctx := context.Background()

	before, err := reg.OIDCFor(ctx, "corp")
	if err != nil {
		t.Fatalf("OIDCFor: %v", err)
	}
	if before.auditWriteSync != nil {
		t.Fatal("no audit func attached yet, provider must have none")
	}
	rec := &auditWriteRecorder{}
	reg.AttachAuditWriteSyncFunc(rec.write)
	after, err := reg.OIDCFor(ctx, "corp")
	if err != nil {
		t.Fatalf("OIDCFor after attach: %v", err)
	}
	if after == before {
		t.Fatal("provider built before the audit func was attached must be rebuilt")
	}
	if after.auditWriteSync == nil {
		t.Fatal("rebuilt provider must carry the audit func")
	}
	if after.providerName != "corp" {
		t.Fatalf("providerName = %q, want corp", after.providerName)
	}
}
