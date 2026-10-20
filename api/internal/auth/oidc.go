package auth

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"

	"github.com/GameplanePanel/gameplane/api/internal/db"
	"github.com/GameplanePanel/gameplane/api/internal/httperr"
	"github.com/GameplanePanel/gameplane/api/internal/scope"
)

const (
	oidcStateCookie = "gameplane_oidc_state"
	oidcNonceCookie = "gameplane_oidc_nonce"
)

// RoleAssignmentOutcome describes the result of assigning or re-evaluating an OIDC user's role.
// T009 consumes this to emit audit events.
type RoleAssignmentOutcome struct {
	// PreviousRole is the user's role before this login attempt. For first login, it is "new_user".
	PreviousRole string
	// NewRole is the role assigned (or re-evaluated) at this login: "admin", "operator", "viewer", or "".
	NewRole string
	// Applied is true iff the role change was persisted to the database. It is false when the
	// change was skipped due to the demotion guard (would remove the last user-manager).
	Applied bool
	// MatchedGroup is the specific group claim value that matched a role mapping,
	// or "none" if the user's groups did not match any mapping and the default role was applied.
	MatchedGroup string
}

// ProviderPolicy carries a provider's group→role mapping configuration
// into the OIDC client: extra OAuth scopes, the groups claim name, the
// mappings themselves, and the fallback role. A nil policy — or a policy
// whose RoleMappings is nil — disables mapping entirely: new users get
// viewer and their role is never touched again.
type ProviderPolicy struct {
	Scopes       []string
	GroupsClaim  string
	RoleMappings *RoleMappings
	DefaultRole  string
}

// OIDC handles OpenID Connect authentication.
type OIDC struct {
	provider             *oidc.Provider
	verifier             *oidc.IDTokenVerifier
	oauth                *oauth2.Config
	policy               *ProviderPolicy
	db                   *db.Store
	auditWriteSync       func(ctx context.Context, method, path, target, reason string, status int) error
	providerName         string
	getHelmRoleOverrides func(context.Context) *RoleMappings
}

// NewOIDCWithPolicy is NewOIDC carrying a group→role mapping policy. The
// requested scopes are the base openid/profile/email set plus the
// policy's extras, deduplicated and order-preserving.
func NewOIDCWithPolicy(
	ctx context.Context, issuer, clientID, clientSecret, redirectURL string, pol *ProviderPolicy,
) (*OIDC, error) {
	if issuer == "" {
		return nil, nil
	}
	provider, err := oidc.NewProvider(ctx, issuer)
	if err != nil {
		return nil, err
	}
	scopes := []string{oidc.ScopeOpenID, "profile", "email"}
	if pol != nil {
		seen := map[string]bool{}
		for _, s := range scopes {
			seen[s] = true
		}
		for _, s := range pol.Scopes {
			if !seen[s] {
				scopes = append(scopes, s)
				seen[s] = true
			}
		}
	}
	return &OIDC{
		provider: provider,
		verifier: provider.Verifier(&oidc.Config{ClientID: clientID}),
		oauth: &oauth2.Config{
			ClientID:     clientID,
			ClientSecret: clientSecret,
			RedirectURL:  redirectURL,
			Endpoint:     provider.Endpoint(),
			Scopes:       scopes,
		},
		policy: pol,
	}, nil
}

// NewOIDC returns (nil, nil) when no issuer is configured — the caller
// treats that as "OIDC disabled" rather than an error.
func NewOIDC(ctx context.Context, issuer, clientID, clientSecret, redirectURL string) (*OIDC, error) {
	return NewOIDCWithPolicy(ctx, issuer, clientID, clientSecret, redirectURL, nil)
}

// defaultGroupsClaim returns the default groups claim name ("groups") when the configured
// name is empty. This is the single place where the fallback default is defined.
func defaultGroupsClaim(claimName string) string {
	if claimName == "" {
		return "groups"
	}
	return claimName
}

// extractGroups pulls group memberships out of raw ID-token claims.
// claimName defaults to "groups" when empty. IdPs disagree on the claim's
// shape, so both a JSON array of strings (non-strings skipped) and a bare
// string are accepted; a missing claim yields nil.
func extractGroups(claims map[string]any, claimName string) []string {
	claimName = defaultGroupsClaim(claimName)
	switch v := claims[claimName].(type) {
	case []any:
		var groups []string
		for _, item := range v {
			if s, ok := item.(string); ok {
				groups = append(groups, s)
			}
		}
		return groups
	case string:
		return []string{v}
	default:
		return nil
	}
}

// effectiveHelmPolicy merges the DB overlay's per-role list replacements onto
// the Helm-seeded base policy, producing the *ProviderPolicy that computeRole
// is then called with, unmodified. Per-role list replacement (M3): for each
// of admin/operator/viewer, a non-nil override list replaces the Helm-seeded
// list for that role only; an empty non-nil list validly means "nobody maps
// to this role"; a nil override leaves the Helm-seeded list standing. The
// three roles resolve independently.
//
// Parameters:
//   - base: the Helm-seeded policy for the synthetic "helm" provider
//   - ov: the helmOverride.roleMappings value from the "auth" config row; nil
//     when helmOverride is absent
//
// Return: a *ProviderPolicy with RoleMappings merged per role and Scopes,
// GroupsClaim, DefaultRole carried through unchanged from base. Returns base
// unchanged when ov is nil (SC-008 — no override = byte-identical behavior).
func effectiveHelmPolicy(base *ProviderPolicy, ov *RoleMappings) *ProviderPolicy {
	if base == nil {
		return nil
	}
	if ov == nil {
		// No override — return base unchanged (SC-008)
		return base
	}

	// Start with a copy of the base policy
	effective := &ProviderPolicy{
		Scopes:       base.Scopes,
		GroupsClaim:  base.GroupsClaim,
		RoleMappings: &RoleMappings{},
		DefaultRole:  base.DefaultRole,
	}

	// Copy base role mappings
	if base.RoleMappings != nil {
		effective.RoleMappings.Admin = base.RoleMappings.Admin
		effective.RoleMappings.Operator = base.RoleMappings.Operator
		effective.RoleMappings.Viewer = base.RoleMappings.Viewer
	}

	// Apply per-role overrides: non-nil override list replaces the base list for that role
	if ov.Admin != nil {
		effective.RoleMappings.Admin = ov.Admin
	}
	if ov.Operator != nil {
		effective.RoleMappings.Operator = ov.Operator
	}
	if ov.Viewer != nil {
		effective.RoleMappings.Viewer = ov.Viewer
	}

	return effective
}

// computeRole resolves a user's dashboard role from their IdP groups. A
// nil policy or nil RoleMappings means mapping is off: viewer, never
// denied. With mappings, the most privileged matching role wins (admin >
// operator > viewer); an unmatched user gets the policy's DefaultRole,
// where "" means viewer and "deny" refuses the login (deny=true, empty
// role).
func computeRole(groups []string, pol *ProviderPolicy) (role string, deny bool) {
	if pol == nil || pol.RoleMappings == nil {
		return "viewer", false
	}
	member := map[string]bool{}
	for _, g := range groups {
		member[g] = true
	}
	matches := func(mapped []string) bool {
		for _, g := range mapped {
			if member[g] {
				return true
			}
		}
		return false
	}
	switch {
	case matches(pol.RoleMappings.Admin):
		return "admin", false
	case matches(pol.RoleMappings.Operator):
		return "operator", false
	case matches(pol.RoleMappings.Viewer):
		return "viewer", false
	}
	switch pol.DefaultRole {
	case "deny":
		return "", true
	case "admin", "operator":
		return pol.DefaultRole, false
	default: // "" and "viewer" (validateAuth admits nothing else)
		return "viewer", false
	}
}

// getMatchedGroup determines which group from the user's group list matched a role mapping,
// following the same admin > operator > viewer precedence as computeRole. If no group matched,
// returns "none" (indicating the default role will apply).
func getMatchedGroup(groups []string, pol *ProviderPolicy) string {
	if pol == nil || pol.RoleMappings == nil {
		return "none"
	}
	member := map[string]bool{}
	for _, g := range groups {
		member[g] = true
	}
	// Check admin tier first (highest priority)
	for _, g := range groups {
		if member[g] {
			for _, adminGroup := range pol.RoleMappings.Admin {
				if g == adminGroup {
					return g
				}
			}
		}
	}
	// Check operator tier
	for _, g := range groups {
		if member[g] {
			for _, opGroup := range pol.RoleMappings.Operator {
				if g == opGroup {
					return g
				}
			}
		}
	}
	// Check viewer tier
	for _, g := range groups {
		if member[g] {
			for _, viewerGroup := range pol.RoleMappings.Viewer {
				if g == viewerGroup {
					return g
				}
			}
		}
	}
	return "none"
}

// emitRoleAssignmentAudit emits an audit event for OIDC role assignment (FR-014).
// It emits an event when the role CHANGED or on first login. If the assignment was
// blocked by the demotion guard (Applied=false), it logs a warning instead and does
// not emit an audit event.
//
// A nil auditWriteSync func is a safe no-op — no event is emitted. An audit write
// failure is logged but does not break the login flow.
func (o *OIDC) emitRoleAssignmentAudit(ctx context.Context, user *User, path string, target string, outcome *RoleAssignmentOutcome) {
	if o.auditWriteSync == nil {
		return
	}
	if outcome == nil {
		return
	}

	// If the assignment was blocked by the demotion guard, log it and do not emit an audit event.
	if !outcome.Applied {
		slog.Warn("oidc role assignment skipped: would remove last user-manager",
			"user", user.Username, "attempted_role", outcome.NewRole)
		return
	}

	// Emit an audit event only if the role changed or this is a first login.
	roleChanged := outcome.PreviousRole != outcome.NewRole
	isFirstLogin := outcome.PreviousRole == "new_user"
	if !roleChanged && !isFirstLogin {
		return
	}

	// Build the reason string: oidc role assigned: provider=<name> matched=<group> from=<old> to=<new>
	providerName := o.providerName
	if providerName == "" {
		providerName = "unknown"
	}
	reason := fmt.Sprintf("oidc role assigned: provider=%s matched=%s from=%s to=%s",
		providerName, outcome.MatchedGroup, outcome.PreviousRole, outcome.NewRole)

	// Enrich the context with the newly-authenticated user so WriteSync can extract the actor.
	enrichedCtx := WithUser(ctx, user)

	// Call the audit write func. An audit write failure is logged by the underlying
	// auditor itself (via slog.Warn), so we just call it without additional error
	// handling — the login still succeeds.
	_ = o.auditWriteSync(enrichedCtx, "POST", path, target, reason, http.StatusOK)
}

// AttachStore attaches a database store to the OIDC handler.
func (o *OIDC) AttachStore(s *db.Store) { o.db = s }

// AttachAuditWriteSyncFunc attaches a function that performs a synchronous audit
// write for FR-014 OIDC role-assignment audit event emission. The signature mirrors
// audit.Auditor.WriteSync (method, path, target, reason, status) without importing
// the audit package, avoiding an import cycle (audit imports auth). Pass nil to
// disable audit emission; a nil func is a safe no-op.
func (o *OIDC) AttachAuditWriteSyncFunc(fn func(ctx context.Context, method, path, target, reason string, status int) error) {
	o.auditWriteSync = fn
}

// SetProviderName sets the provider name (e.g., "helm", "okta") used in audit events.
func (o *OIDC) SetProviderName(name string) { o.providerName = name }

// AttachHelmRoleOverridesFunc attaches a function that reads the current Helm-seeded
// role-mapping overrides from the auth config row. Called on every login for the Helm
// provider to deliver SC-007 (no restart needed after admin edit).
func (o *OIDC) AttachHelmRoleOverridesFunc(fn func(context.Context) *RoleMappings) {
	o.getHelmRoleOverrides = fn
}

// HandleStart returns an HTTP handler for starting an OIDC authorization flow.
func (o *OIDC) HandleStart() http.HandlerFunc {
	return o.HandleStartAt("/")
}

// HandleStartAt is HandleStart with an explicit cookie path. Dynamic
// providers scope their state/nonce cookies to /auth/oidc/{name} so two
// concurrent flows against different providers can't clobber each
// other's cookies; the legacy routes keep Path=/.
func (o *OIDC) HandleStartAt(cookiePath string) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		state := randomToken()
		nonce := randomToken()
		ttl := 5 * time.Minute
		http.SetCookie(w, &http.Cookie{
			Name: oidcStateCookie, Value: state, Path: cookiePath, HttpOnly: true, Secure: true,
			SameSite: http.SameSiteLaxMode, Expires: time.Now().Add(ttl),
		})
		// Nonce is bound to the ID token via OpenID Connect spec — the
		// IdP echoes it back in the `nonce` claim. Verifying the claim
		// matches the cookie prevents ID-token replay, complementing
		// the CSRF-style state check.
		http.SetCookie(w, &http.Cookie{
			Name: oidcNonceCookie, Value: nonce, Path: cookiePath, HttpOnly: true, Secure: true,
			SameSite: http.SameSiteLaxMode, Expires: time.Now().Add(ttl),
		})
		http.Redirect(w, req, o.oauth.AuthCodeURL(state, oidc.Nonce(nonce)), http.StatusFound)
	}
}

// HandleCallback returns an HTTP handler for OIDC authorization callbacks.
func (o *OIDC) HandleCallback(sessions *SessionStore) http.HandlerFunc {
	return o.HandleCallbackAt(sessions, "/")
}

// HandleCallbackAt is HandleCallback with an explicit cookie path
// matching HandleStartAt's — clearing a cookie only works on the path it
// was set with.
func (o *OIDC) HandleCallbackAt(sessions *SessionStore, cookiePath string) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		state, err := req.Cookie(oidcStateCookie)
		if err != nil || state.Value == "" || state.Value != req.URL.Query().Get("state") {
			httperr.WriteCode(w, req, http.StatusBadRequest, errors.New("state mismatch"))
			return
		}
		clearCookieAt(w, oidcStateCookie, cookiePath)

		tok, err := o.oauth.Exchange(req.Context(), req.URL.Query().Get("code"))
		if err != nil {
			slog.Warn("oidc exchange", "err", err)
			httperr.WriteCode(w, req, http.StatusBadRequest, errors.New("oauth exchange failed"))
			return
		}
		rawID, _ := tok.Extra("id_token").(string)
		if rawID == "" {
			httperr.WriteCode(w, req, http.StatusBadRequest, errors.New("no id_token"))
			return
		}
		idt, err := o.verifier.Verify(req.Context(), rawID)
		if err != nil {
			httperr.WriteCode(w, req, http.StatusUnauthorized, errors.New("invalid id_token"))
			return
		}
		// Nonce check — the IdP is expected to echo the nonce we issued
		// at the start route into the ID token. Missing cookie or
		// mismatch means replay or a broken IdP; either way, don't
		// accept the login.
		nonceCookie, err := req.Cookie(oidcNonceCookie)
		if err != nil || nonceCookie.Value == "" || idt.Nonce != nonceCookie.Value {
			clearCookieAt(w, oidcNonceCookie, cookiePath)
			httperr.WriteCode(w, req, http.StatusBadRequest, errors.New("nonce mismatch"))
			return
		}
		clearCookieAt(w, oidcNonceCookie, cookiePath)
		var claims struct {
			Sub   string `json:"sub"`
			Email string `json:"email"`
			Name  string `json:"name"`
		}
		if err := idt.Claims(&claims); err != nil {
			slog.Warn("oidc claim parse", "err", err)
			httperr.WriteCode(w, req, http.StatusBadRequest, errors.New("invalid id_token claims"))
			return
		}
		// The typed struct above can't see arbitrary claim names, so the
		// groups claim (admin-configurable) is read from a raw re-parse.
		var rawClaims map[string]any
		if err := idt.Claims(&rawClaims); err != nil {
			slog.Warn("oidc raw claim parse", "err", err)
			httperr.WriteCode(w, req, http.StatusBadRequest, errors.New("invalid id_token claims"))
			return
		}
		// Extract groups using the configured claim name, defaulting to "groups" if empty.
		claimName := ""
		if o.policy != nil {
			claimName = o.policy.GroupsClaim
		}
		groups := extractGroups(rawClaims, claimName)

		// For the Helm provider, read the current helmOverride and build the effective policy
		// on every login attempt (SC-007: no cache, read at login time).
		resolvePolicy := o.policy
		if o.providerName == HelmProviderName && o.getHelmRoleOverrides != nil {
			if helmOverride := o.getHelmRoleOverrides(req.Context()); helmOverride != nil {
				resolvePolicy = effectiveHelmPolicy(o.policy, helmOverride)
			}
		}

		role, deny := computeRole(groups, resolvePolicy)
		if deny {
			// Log the identity, never the tokens.
			slog.Warn("oidc login denied: no group grants a role and defaultRole is deny",
				"issuer", idt.Issuer, "subject", claims.Sub)
			httperr.WriteCode(w, req, http.StatusForbidden, errors.New("login not permitted"))
			return
		}

		// Determine which group matched a role mapping (or "none" if default role applies).
		// For the Helm provider with overrides, use the effective policy to match against
		// the merged role mappings, ensuring audit events report the correct matched group.
		matchedGroup := getMatchedGroup(groups, resolvePolicy)

		// Re-evaluation runs whenever the effective policy — the one the role
		// above was computed from, including any helmOverride merge — has role
		// mappings.
		syncRole := resolvePolicy != nil && resolvePolicy.RoleMappings != nil

		user, roleOutcome, err := o.resolveOrLinkUser(req.Context(), idt.Issuer, claims.Sub, claims.Email, claims.Name, role, matchedGroup, syncRole)
		if err != nil {
			slog.Error("oidc resolveOrLinkUser", "err", err)
			http.Error(w, "login failed", http.StatusInternalServerError)
			return
		}

		// Emit FR-014 audit event on role assignment. The target is the user's email (preferred),
		// username, or subject identifier, in that precedence order.
		auditTarget := claims.Email
		if auditTarget == "" {
			auditTarget = user.Username
		}
		if auditTarget == "" {
			auditTarget = claims.Sub
		}
		o.emitRoleAssignmentAudit(req.Context(), user, req.URL.Path, auditTarget, roleOutcome)

		sess, csrf, err := sessions.Create(req.Context(), user.ID)
		if err != nil {
			slog.Error("oidc session create", "err", err)
			http.Error(w, "login failed", http.StatusInternalServerError)
			return
		}
		setSessionCookie(w, sess, sessionTTL)
		setCSRFCookie(w, csrf, sessionTTL)

		// Redirect back to the SPA root; the SPA reads the CSRF cookie
		// and starts making authenticated requests.
		http.Redirect(w, req, "/", http.StatusFound)
	}
}

// resolveOrLinkUser returns the user linked to (issuer, subject) and a RoleAssignmentOutcome
// describing the role assignment/re-evaluation that occurred, creating a new user with the given
// role on first login. syncRole (true iff the effective policy has role mappings) makes the IdP
// authoritative: an existing user whose stored role differs is re-pointed at role. Without it a
// manually-promoted user keeps their role.
//
// matchedGroup should be the specific group from the user's token that matched a role mapping,
// or "none" if no group matched and the default role was applied. For first login, it is included
// in the outcome so T009 can emit an audit event.
func (o *OIDC) resolveOrLinkUser(
	ctx context.Context, issuer, sub, email, name, role, matchedGroup string, syncRole bool,
) (*User, *RoleAssignmentOutcome, error) {
	if o.db == nil {
		return nil, nil, errors.New("oidc: no store attached")
	}
	var u User
	err := o.db.DB.QueryRowContext(ctx, `
		SELECT u.id, u.username, u.display_name, u.email, u.role
		FROM users u JOIN oidc_links l ON l.user_id = u.id
		WHERE l.issuer = ? AND l.subject = ?`, issuer, sub,
	).Scan(&u.ID, &u.Username, &u.DisplayName, &u.Email, &u.Role)
	if err == nil {
		// Existing user — re-evaluate role if mappings are configured.
		if syncRole {
			outcome, err := o.syncUserRole(ctx, u.ID, role, matchedGroup)
			if err != nil {
				return nil, nil, fmt.Errorf("sync role for user %d: %w", u.ID, err)
			}
			if outcome.Applied {
				u.Role = outcome.NewRole
			}
			return &u, outcome, nil
		}
		// No role mappings configured — return user as-is with no re-evaluation.
		return &u, &RoleAssignmentOutcome{
			PreviousRole: u.Role,
			NewRole:      u.Role,
			Applied:      true,
			MatchedGroup: "none",
		}, nil
	}
	// First login — create user + link in a single tx.
	baseUsername := email
	if baseUsername == "" {
		baseUsername = sub
	}
	tx, err := o.db.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = tx.Rollback() }()

	// Find a username that doesn't collide with an existing local user.
	// Suffix with a short piece of the OIDC subject on conflict — keeps
	// the username recognizable while guaranteeing uniqueness.
	username, err := pickUniqueUsername(ctx, tx, baseUsername, sub)
	if err != nil {
		return nil, nil, err
	}

	// RETURNING id instead of LastInsertId: pgx's database/sql driver has
	// no LastInsertId, and both SQLite and Postgres support RETURNING.
	var uid int64
	if err := tx.QueryRowContext(ctx,
		`INSERT INTO users(username, email, display_name, role) VALUES (?, ?, ?, ?) RETURNING id`,
		username, email, name, role,
	).Scan(&uid); err != nil {
		return nil, nil, err
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO oidc_links(user_id, issuer, subject, email) VALUES (?, ?, ?, ?)`,
		uid, issuer, sub, email,
	); err != nil {
		return nil, nil, err
	}
	// Mirror the role into a cluster-wide role binding so RBAC resolves
	// the new SSO user's permissions.
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO user_role_bindings(user_id, role_name, namespace) VALUES (?, ?, '*')`,
		uid, role,
	); err != nil {
		return nil, nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, nil, err
	}
	outcome := &RoleAssignmentOutcome{
		PreviousRole: "new_user",
		NewRole:      role,
		Applied:      true,
		MatchedGroup: matchedGroup,
	}
	return &User{
		ID: uid, Username: username, DisplayName: name, Email: email, Role: role,
	}, outcome, nil
}

// syncUserRole re-points an existing user's primary role and their cluster-wide ("*") role
// binding based on the newly-resolved role and matched group. Returns a RoleAssignmentOutcome
// describing what happened (previous role, new role, whether applied).
//
// Namespace-scoped bindings are deliberately left alone — the IdP owns the primary role, not
// the per-namespace grants an admin may have added.
//
// One exception, mirroring the manual users handler's lockout guard: a demotion that would strip
// the install's LAST user who can manage users is skipped (applied=false, no error) — the login
// still succeeds and the stored role stays. Otherwise a group-mapping mistake at the IdP could
// have the sole admin demote themselves by logging in, locking everyone out of user administration.
//
// Re-evaluation only runs when the effective policy (post-helmOverride merge) has RoleMappings
// configured. With no mappings, the user's role is assigned once at first login and never re-evaluated.
func (o *OIDC) syncUserRole(ctx context.Context, userID int64, newRole, matchedGroup string) (*RoleAssignmentOutcome, error) {
	// Share the same guard as manual role changes and account deletion.
	// Keep the role/count reads and the transaction in one critical section.
	unlock := o.db.LockUserManagement()
	defer unlock()

	// Fetch the user's current role to report in the outcome.
	var currentRole string
	err := o.db.DB.QueryRowContext(ctx, `SELECT role FROM users WHERE id = ?`, userID).Scan(&currentRole)
	if err != nil {
		return nil, fmt.Errorf("fetch current role: %w", err)
	}

	outcome := &RoleAssignmentOutcome{
		PreviousRole: currentRole,
		NewRole:      newRole,
		MatchedGroup: matchedGroup,
		Applied:      false,
	}

	// If role is unchanged, consider it applied (no-op) and return.
	if currentRole == newRole {
		outcome.Applied = true
		return outcome, nil
	}

	// Check if the new role grants user-management capability.
	newGrantsManage, err := o.db.RoleGrantsUserManagement(ctx, newRole)
	if err != nil {
		return outcome, fmt.Errorf("check target role: %w", err)
	}

	// If the new role does NOT grant user management, check if the user currently manages users.
	// If so, check if they're the last one — if they are, block the demotion.
	if !newGrantsManage {
		managesNow, err := o.db.UserManagesUsers(ctx, userID)
		if err != nil {
			return outcome, fmt.Errorf("check current role: %w", err)
		}
		if managesNow {
			count, err := o.db.UserManagerCount(ctx)
			if err != nil {
				return outcome, fmt.Errorf("count user managers: %w", err)
			}
			if count <= 1 {
				slog.Warn("oidc role resync skipped: would remove last user-manager", "user", userID)
				return outcome, nil // applied=false, no error
			}
		}
	}

	// Update the user's role in a transaction.
	tx, err := o.db.DB.BeginTx(ctx, nil)
	if err != nil {
		return outcome, fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx,
		`UPDATE users SET role = ? WHERE id = ?`, newRole, userID); err != nil {
		return outcome, fmt.Errorf("update role: %w", err)
	}
	if err := o.db.SetClusterRoleBinding(ctx, tx, userID, scope.DefaultCluster, newRole); err != nil {
		return outcome, err
	}
	if err := tx.Commit(); err != nil {
		return outcome, fmt.Errorf("commit: %w", err)
	}

	outcome.Applied = true
	return outcome, nil
}

// pickUniqueUsername returns base if no existing user has that username,
// otherwise returns base with a short suffix derived from sub. Uses the
// transaction so the check and the caller's INSERT see a consistent view.
func pickUniqueUsername(ctx context.Context, tx *sql.Tx, base, sub string) (string, error) {
	var existing int64
	err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM users WHERE username = ?`, base).Scan(&existing)
	if err != nil {
		return "", err
	}
	if existing == 0 {
		return base, nil
	}
	// Use the first 8 chars of sub as the tiebreaker. OIDC subs are
	// opaque but usually long; 8 chars is enough to disambiguate in
	// practice without exposing the whole subject identifier.
	suffix := sub
	if len(suffix) > 8 {
		suffix = suffix[:8]
	}
	return base + "+" + suffix, nil
}
