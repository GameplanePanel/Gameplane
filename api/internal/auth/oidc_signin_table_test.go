package auth

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/GameplanePanel/gameplane/api/internal/db"
)

// These tests cover the data-model.md "Sign-in decision" table (spec 034):
// what one SSO sign-in does for a new user, an SSO-managed (sso or
// sso_pending) user and a hand-set (manual) user, and the audit event each
// outcome writes (contracts/audit-events.md).

// decisionRecorder captures each audit reason and its HTTP status.
type decisionRecorder struct {
	reasons  []string
	statuses []int
}

func (r *decisionRecorder) write(_ context.Context, _, _, _, reason string, status int) error {
	r.reasons = append(r.reasons, reason)
	r.statuses = append(r.statuses, status)
	return nil
}

// signInFixture drives sign-ins of one fake-IdP user through a "corp"
// provider whose policy the test may change between sign-ins.
type signInFixture struct {
	t        *testing.T
	idp      *fakeIDP
	o        *OIDC
	pol      *ProviderPolicy
	store    *db.Store
	sessions *SessionStore
	rec      *decisionRecorder
	logins   int
}

func newSignInFixture(t *testing.T, pol *ProviderPolicy) *signInFixture {
	t.Helper()
	SetFastHashParams(t)
	idp := newFakeIDP(t, "client-1")
	o, err := NewOIDCWithPolicy(context.Background(), idp.issuer(), "client-1", "secret", "https://app/cb", pol)
	if err != nil {
		t.Fatalf("NewOIDCWithPolicy: %v", err)
	}
	store := newAuthDB(t)
	o.AttachStore(store)
	rec := &decisionRecorder{}
	o.AttachAuditWriteSyncFunc(rec.write)
	o.SetProviderName("corp")
	return &signInFixture{t: t, idp: idp, o: o, pol: pol, store: store, sessions: NewSessionStore(store), rec: rec}
}

// login signs the user in once with the given groups.
func (f *signInFixture) login(groups ...string) *httptest.ResponseRecorder {
	f.t.Helper()
	f.logins++
	nonce := fmt.Sprintf("nonce-signin-%d", f.logins)
	f.idp.nonce = nonce
	f.idp.groups = groups
	return callbackViaIDP(f.t, f.o, f.sessions, nonce)
}

// mustLogin signs in and requires the sign-in to succeed.
func (f *signInFixture) mustLogin(groups ...string) {
	f.t.Helper()
	if rr := f.login(groups...); rr.Code != http.StatusFound {
		f.t.Fatalf("login %v: code=%d body=%q, want 302", groups, rr.Code, rr.Body)
	}
}

// mustRefuse signs in and requires the generic refusal, naming no rule or role.
func (f *signInFixture) mustRefuse(groups ...string) {
	f.t.Helper()
	rr := f.login(groups...)
	if rr.Code != http.StatusForbidden {
		f.t.Fatalf("login %v: code=%d body=%q, want 403", groups, rr.Code, rr.Body)
	}
	body := rr.Body.String()
	if !strings.Contains(body, "login not permitted") {
		f.t.Fatalf("refusal body = %q, want the generic login not permitted", body)
	}
	for _, leak := range []string{"rule", "ghost", "deny", "role"} {
		if strings.Contains(body, leak) {
			f.t.Fatalf("refusal body %q leaks %q", body, leak)
		}
	}
	for _, c := range rr.Result().Cookies() {
		if c.Name == sessionCookie {
			f.t.Fatal("refused sign-in set a session cookie")
		}
	}
}

// stored returns the user's stored role and role source; ok is false when
// no user exists.
func (f *signInFixture) stored() (role, source string, ok bool) {
	f.t.Helper()
	err := f.store.DB.QueryRowContext(context.Background(),
		`SELECT role, role_source FROM users WHERE email = ?`, f.idp.email).Scan(&role, &source)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", false
	}
	if err != nil {
		f.t.Fatalf("read user: %v", err)
	}
	return role, source, true
}

// wantStored requires the user to exist with role and source.
func (f *signInFixture) wantStored(role, source string) {
	f.t.Helper()
	gotRole, gotSource, ok := f.stored()
	if !ok {
		f.t.Fatal("user does not exist")
	}
	if gotRole != role || gotSource != source {
		f.t.Fatalf("stored role/source = %q/%q, want %q/%q", gotRole, gotSource, role, source)
	}
}

// bindingRole returns the role of the user's cluster-wide binding.
func (f *signInFixture) bindingRole() string {
	f.t.Helper()
	var role string
	if err := f.store.DB.QueryRowContext(context.Background(), `
		SELECT b.role_name FROM user_role_bindings b
		JOIN users u ON u.id = b.user_id
		WHERE u.email = ? AND b.namespace = '*'`, f.idp.email).Scan(&role); err != nil {
		f.t.Fatalf("read binding: %v", err)
	}
	return role
}

// setSource stores the user's role source, as the users API would.
func (f *signInFixture) setSource(source string) {
	f.t.Helper()
	if _, err := f.store.DB.ExecContext(context.Background(),
		`UPDATE users SET role_source = ? WHERE email = ?`, source, f.idp.email); err != nil {
		f.t.Fatalf("set role source: %v", err)
	}
}

// seedRole creates a custom role.
func (f *signInFixture) seedRole(name string) {
	f.t.Helper()
	if _, err := f.store.DB.ExecContext(context.Background(),
		`INSERT INTO roles(name, builtin, description) VALUES (?, 0, '')`, name); err != nil {
		f.t.Fatalf("seed role %s: %v", name, err)
	}
}

// wantEvents requires exactly n audit events so far.
func (f *signInFixture) wantEvents(n int) {
	f.t.Helper()
	if len(f.rec.reasons) != n {
		f.t.Fatalf("audit events = %d (%v), want %d", len(f.rec.reasons), f.rec.reasons, n)
	}
}

// wantLastEvent requires the latest audit event to have reason and status.
func (f *signInFixture) wantLastEvent(reason string, status int) {
	f.t.Helper()
	if len(f.rec.reasons) == 0 {
		f.t.Fatalf("no audit event, want %q", reason)
	}
	last := len(f.rec.reasons) - 1
	if f.rec.reasons[last] != reason {
		f.t.Fatalf("audit reason = %q, want %q", f.rec.reasons[last], reason)
	}
	if f.rec.statuses[last] != status {
		f.t.Fatalf("audit status = %d, want %d", f.rec.statuses[last], status)
	}
}

// --- New users (T014) ---

func TestSignIn_NewUserGetsRuleRoleAndSSOSource(t *testing.T) {
	f := newSignInFixture(t, &ProviderPolicy{Rules: []RoleRule{
		{Group: "sys-admin", Role: "admin"},
		{Group: "gameplane login only", Role: "login-only"},
	}})
	f.seedRole("login-only")

	f.mustLogin("gameplane login only")

	f.wantStored("login-only", db.RoleSourceSSO)
	if got := f.bindingRole(); got != "login-only" {
		t.Fatalf("binding role = %q, want login-only", got)
	}
	f.wantEvents(1)
	f.wantLastEvent("oidc role decision: provider=corp rule=2 group=gameplane login only role=login-only source=new result=applied", http.StatusOK)
}

func TestSignIn_NewUserFallbackRoleApplied(t *testing.T) {
	f := newSignInFixture(t, &ProviderPolicy{
		Rules:       []RoleRule{{Group: "ops", Role: "operator"}},
		DefaultRole: "login-only",
	})
	f.seedRole("login-only")

	f.mustLogin("unmapped")

	f.wantStored("login-only", db.RoleSourceSSO)
	f.wantLastEvent("oidc role decision: provider=corp rule=default group=- role=login-only source=new result=applied", http.StatusOK)
}

func TestSignIn_NewUserRefusedOnDeny(t *testing.T) {
	f := newSignInFixture(t, &ProviderPolicy{
		Rules:       []RoleRule{{Group: "ops", Role: "operator"}},
		DefaultRole: "deny",
	})

	f.mustRefuse("unmapped")

	if _, _, ok := f.stored(); ok {
		t.Fatal("a refused sign-in created a user")
	}
	f.wantEvents(1)
	f.wantLastEvent("oidc role decision: provider=corp rule=default group=- role=deny source=new result=refused_deny", http.StatusForbidden)
}

// OD-2: a matched rule naming a missing role refuses the sign-in; later
// rules are never tried, even when they would grant a role.
func TestSignIn_NewUserRefusedOnMissingRuleRoleWithoutFallThrough(t *testing.T) {
	f := newSignInFixture(t, &ProviderPolicy{Rules: []RoleRule{
		{Group: "g1", Role: "ghost"},
		{Group: "g2", Role: "admin"},
	}})

	f.mustRefuse("g1", "g2")

	if _, _, ok := f.stored(); ok {
		t.Fatal("a refused sign-in created a user")
	}
	f.wantLastEvent("oidc role decision: provider=corp rule=1 group=g1 role=ghost source=new result=refused_missing_role", http.StatusForbidden)
}

func TestSignIn_NewUserRefusedOnMissingFallbackRole(t *testing.T) {
	f := newSignInFixture(t, &ProviderPolicy{
		Rules:       []RoleRule{{Group: "ops", Role: "operator"}},
		DefaultRole: "ghost-default",
	})

	f.mustRefuse("unmapped")

	if _, _, ok := f.stored(); ok {
		t.Fatal("a refused sign-in created a user")
	}
	f.wantLastEvent("oidc role decision: provider=corp rule=default group=- role=ghost-default source=new result=refused_missing_role", http.StatusForbidden)
}

// Mapping off keeps today's behavior: a new user is created as viewer.
func TestSignIn_MappingOffNewUserIsViewer(t *testing.T) {
	f := newSignInFixture(t, &ProviderPolicy{Rules: []RoleRule{}, DefaultRole: "deny"})

	f.mustLogin("anything")

	f.wantStored("viewer", db.RoleSourceSSO)
	f.wantLastEvent("oidc role decision: provider=corp rule=none group=- role=viewer source=new result=applied", http.StatusOK)
}

// --- Existing users (T027) ---

func TestSignIn_SSOUserSynced(t *testing.T) {
	f := newSignInFixture(t, &ProviderPolicy{Rules: []RoleRule{
		{Group: "ops", Role: "operator"},
		{Group: "view", Role: "viewer"},
	}})
	f.mustLogin("view")
	f.wantStored("viewer", db.RoleSourceSSO)

	f.mustLogin("ops")

	f.wantStored("operator", db.RoleSourceSSO)
	if got := f.bindingRole(); got != "operator" {
		t.Fatalf("binding role = %q, want operator", got)
	}
	f.wantEvents(2)
	f.wantLastEvent("oidc role decision: provider=corp rule=1 group=ops role=operator source=sso result=applied", http.StatusOK)
}

// An SSO user whose rule result equals the stored role writes no event.
func TestSignIn_SSOUserUnchangedWritesNoEvent(t *testing.T) {
	f := newSignInFixture(t, &ProviderPolicy{Rules: []RoleRule{{Group: "view", Role: "viewer"}}})
	f.mustLogin("view")

	f.mustLogin("view")

	f.wantStored("viewer", db.RoleSourceSSO)
	f.wantEvents(1)
}

func TestSignIn_ManualUserKept(t *testing.T) {
	f := newSignInFixture(t, &ProviderPolicy{Rules: []RoleRule{
		{Group: "ops", Role: "operator"},
		{Group: "view", Role: "viewer"},
	}})
	f.mustLogin("view")
	f.setSource(db.RoleSourceManual)

	f.mustLogin("ops")

	f.wantStored("viewer", db.RoleSourceManual)
	if got := f.bindingRole(); got != "viewer" {
		t.Fatalf("binding role = %q, want viewer", got)
	}
	f.wantLastEvent("oidc role decision: provider=corp rule=1 group=ops role=operator source=manual result=kept_manual", http.StatusOK)
}

// A hand-set user whose rule result equals the stored role writes no event.
func TestSignIn_ManualUserSameRoleWritesNoEvent(t *testing.T) {
	f := newSignInFixture(t, &ProviderPolicy{Rules: []RoleRule{{Group: "view", Role: "viewer"}}})
	f.mustLogin("view")
	f.setSource(db.RoleSourceManual)

	f.mustLogin("view")

	f.wantStored("viewer", db.RoleSourceManual)
	f.wantEvents(1)
}

// OD-5: a hand-set user signs in with the stored role even when the
// decision is deny.
func TestSignIn_ManualUserAdmittedOnDeny(t *testing.T) {
	f := newSignInFixture(t, &ProviderPolicy{
		Rules:       []RoleRule{{Group: "view", Role: "viewer"}},
		DefaultRole: "deny",
	})
	f.mustLogin("view")
	f.setSource(db.RoleSourceManual)

	f.mustLogin("unmapped")

	f.wantStored("viewer", db.RoleSourceManual)
	f.wantLastEvent("oidc role decision: provider=corp rule=default group=- role=deny source=manual result=admitted_manual", http.StatusOK)
}

// OD-5 with OD-2: a hand-set user is admitted when the matched rule names
// a missing role.
func TestSignIn_ManualUserAdmittedOnMissingRole(t *testing.T) {
	f := newSignInFixture(t, &ProviderPolicy{Rules: []RoleRule{{Group: "view", Role: "viewer"}}})
	f.mustLogin("view")
	f.setSource(db.RoleSourceManual)
	f.pol.Rules = []RoleRule{{Group: "view", Role: "ghost"}}

	f.mustLogin("view")

	f.wantStored("viewer", db.RoleSourceManual)
	f.wantLastEvent("oidc role decision: provider=corp rule=1 group=view role=ghost source=manual result=admitted_manual", http.StatusOK)
}

func TestSignIn_SSOUserRefusedOnDeny(t *testing.T) {
	f := newSignInFixture(t, &ProviderPolicy{
		Rules:       []RoleRule{{Group: "ops", Role: "operator"}},
		DefaultRole: "deny",
	})
	f.mustLogin("ops")

	f.mustRefuse("unmapped")

	f.wantStored("operator", db.RoleSourceSSO)
	f.wantLastEvent("oidc role decision: provider=corp rule=default group=- role=deny source=sso result=refused_deny", http.StatusForbidden)
}

func TestSignIn_SSOUserRefusedOnMissingRole(t *testing.T) {
	f := newSignInFixture(t, &ProviderPolicy{Rules: []RoleRule{{Group: "ops", Role: "operator"}}})
	f.mustLogin("ops")
	f.pol.Rules = []RoleRule{{Group: "ops", Role: "ghost"}, {Group: "ops", Role: "viewer"}}

	f.mustRefuse("ops")

	f.wantStored("operator", db.RoleSourceSSO)
	if got := f.bindingRole(); got != "operator" {
		t.Fatalf("binding role = %q, want operator", got)
	}
	f.wantLastEvent("oidc role decision: provider=corp rule=1 group=ops role=ghost source=sso result=refused_missing_role", http.StatusForbidden)
}

// The last-manager guard still applies, and its skip is now audited.
func TestSignIn_SSOUserLastManagerGuardAudited(t *testing.T) {
	f := newSignInFixture(t, &ProviderPolicy{Rules: []RoleRule{{Group: "adm", Role: "admin"}}})
	f.mustLogin("adm")
	f.wantStored("admin", db.RoleSourceSSO)

	f.mustLogin("unmapped")

	f.wantStored("admin", db.RoleSourceSSO)
	f.wantEvents(2)
	f.wantLastEvent("oidc role decision: provider=corp rule=default group=- role=viewer source=sso result=guard_last_manager", http.StatusOK)
}

// --- Users returned to SSO control (T041) ---

func TestSignIn_PendingUserGetsRuleResultAndMovesToSSO(t *testing.T) {
	f := newSignInFixture(t, &ProviderPolicy{Rules: []RoleRule{
		{Group: "ops", Role: "operator"},
		{Group: "view", Role: "viewer"},
	}})
	f.mustLogin("view")
	f.setSource(db.RoleSourceSSOPending)

	f.mustLogin("ops")

	f.wantStored("operator", db.RoleSourceSSO)
	f.wantLastEvent("oidc role decision: provider=corp rule=1 group=ops role=operator source=sso_pending result=applied", http.StatusOK)
}

// OD-6: a pending user whose rule result equals the stored role still moves
// to sso, and that move is audited as unchanged.
func TestSignIn_PendingUserUnchangedMovesToSSOWithEvent(t *testing.T) {
	f := newSignInFixture(t, &ProviderPolicy{Rules: []RoleRule{
		{Group: "ops", Role: "operator"},
		{Group: "view", Role: "viewer"},
	}})
	f.mustLogin("view")
	f.setSource(db.RoleSourceSSOPending)

	f.mustLogin("view")

	f.wantStored("viewer", db.RoleSourceSSO)
	f.wantEvents(2)
	f.wantLastEvent("oidc role decision: provider=corp rule=2 group=view role=viewer source=sso_pending result=unchanged", http.StatusOK)
}

func TestSignIn_PendingUserRefusedKeepsPending(t *testing.T) {
	f := newSignInFixture(t, &ProviderPolicy{
		Rules:       []RoleRule{{Group: "view", Role: "viewer"}},
		DefaultRole: "deny",
	})
	f.mustLogin("view")
	f.setSource(db.RoleSourceSSOPending)

	f.mustRefuse("unmapped")

	f.wantStored("viewer", db.RoleSourceSSOPending)
	f.wantLastEvent("oidc role decision: provider=corp rule=default group=- role=deny source=sso_pending result=refused_deny", http.StatusForbidden)
}

// Mapping off leaves an existing user's role and source alone, writes no
// event, and never refuses.
func TestSignIn_MappingOffLeavesExistingUserAlone(t *testing.T) {
	f := newSignInFixture(t, &ProviderPolicy{})
	f.mustLogin()
	f.setSource(db.RoleSourceSSOPending)

	f.mustLogin()

	f.wantStored("viewer", db.RoleSourceSSOPending)
	f.wantEvents(1)
}
