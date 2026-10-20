package auth

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/GameplanePanel/gameplane/api/internal/db"
	"github.com/GameplanePanel/gameplane/api/internal/httperr"
	"github.com/GameplanePanel/gameplane/api/internal/messages"
)

// Local handles local (username/password) authentication.
type Local struct {
	db *db.Store
}

// NewLocal returns a new Local auth handler.
func NewLocal(store *db.Store) *Local { return &Local{db: store} }

type loginReq struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// userJSON is the wire shape of an authenticated user. It matches the
// dashboard's User type and the /users/me response exactly, so the
// frontend can seed its cached identity straight from the login reply.
type userJSON struct {
	ID          int64               `json:"id"`
	Username    string              `json:"username"`
	DisplayName string              `json:"displayName"`
	Email       string              `json:"email"`
	Role        string              `json:"role"`
	Provider    string              `json:"provider"`
	CreatedAt   string              `json:"createdAt"`
	Permissions map[string][]string `json:"permissions,omitempty"`
	// Preferences mirrors the "preferences" field GET /users/me embeds
	// (api/internal/handlers/users.go userDTO.Preferences), so the login
	// response seeds the dashboard's theme without a second round-trip
	// (D3, 2026-09-23; handlers can't be imported here — auth already sits
	// below handlers in the import graph — so this DTO is a deliberate
	// duplicate of handlers.userPreferencesDTO; keep both in sync).
	Preferences *userPreferencesJSON `json:"preferences,omitempty"`
}

// customColorsJSON mirrors handlers.customColorsDTO.
type customColorsJSON struct {
	Accent  string `json:"accent"`
	Surface string `json:"surface"`
}

// userPreferencesJSON mirrors handlers.userPreferencesDTO
// (api/internal/handlers/users.go) field-for-field and tag-for-tag.
type userPreferencesJSON struct {
	ThemeType        string            `json:"themeType"`
	PresetID         string            `json:"presetId"`
	AppearanceMode   string            `json:"appearanceMode"`
	CustomColors     *customColorsJSON `json:"customColors"`
	CustomCSSEnabled bool              `json:"customCssEnabled"`
	CustomCSS        *string           `json:"customCss"`
	UpdatedAt        string            `json:"updatedAt"`
}

// newUserPreferencesJSON mirrors handlers.preferencesDTO.
func newUserPreferencesJSON(p db.UserPreferences) *userPreferencesJSON {
	out := &userPreferencesJSON{
		ThemeType:        p.ThemeType,
		PresetID:         p.PresetID,
		AppearanceMode:   p.AppearanceMode,
		CustomCSSEnabled: p.CustomCSSEnabled,
		CustomCSS:        p.CustomCSS,
		UpdatedAt:        p.UpdatedAt,
	}
	if p.CustomAccent != nil || p.CustomSurface != nil {
		out.CustomColors = &customColorsJSON{}
		if p.CustomAccent != nil {
			out.CustomColors.Accent = *p.CustomAccent
		}
		if p.CustomSurface != nil {
			out.CustomColors.Surface = *p.CustomSurface
		}
	}
	return out
}

func newUserJSON(u *User, prefs db.UserPreferences) userJSON {
	return userJSON{
		ID: u.ID, Username: u.Username, DisplayName: u.DisplayName,
		Email: u.Email, Role: u.Role, Permissions: PermsToJSON(u.Perms),
		Preferences: newUserPreferencesJSON(prefs),
	}
}

type loginResp struct {
	User userJSON `json:"user"`
	CSRF string   `json:"csrf"`
}

// HandleLogin serves POST /auth/login. reg gates the whole method: when
// the admin has disabled the local provider, every request gets the same
// neutral 403 before any credential is read — the branch is
// provider-level, not per-user, so it can't become an enumeration oracle.
func (l *Local) HandleLogin(sessions *SessionStore, reg *Registry) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		if reg != nil && !reg.LocalEnabled(req.Context()) {
			httperr.WriteCode(w, req, http.StatusForbidden, errors.New("login method disabled"))
			return
		}
		// Enforce JSON content-type — a cross-site form POST can carry
		// Cookie but not application/json (without CORS preflight), so
		// rejecting non-JSON here is cheap defense-in-depth on top of
		// the CSRF token check on the mutation path.
		ct := req.Header.Get("Content-Type")
		if !strings.HasPrefix(ct, "application/json") {
			httperr.WriteCode(w, req, http.StatusUnsupportedMediaType, errors.New("expected application/json"))
			return
		}
		var body loginReq
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			httperr.WriteCode(w, req, http.StatusBadRequest, errors.New("bad request"))
			return
		}
		// Per-username throttle layered on top of the per-IP LoginLimiter:
		// a distributed attacker with a botnet still burns through
		// usernames at full speed when limiting is IP-only. Checked for
		// every submitted username (valid or not) so the decision doesn't
		// leak existence.
		if body.Username != "" && !LoginUserLimiter.AllowUser(body.Username) {
			w.Header().Set("Retry-After", "30")
			httperr.WriteCode(w, req, http.StatusTooManyRequests, errors.New("too many requests"))
			return
		}
		// Every branch below that returns 401 must first spend the argon2
		// cost so that response time can't distinguish "user not found",
		// "OIDC-only account", and "wrong password" — that differential
		// is a username-enumeration oracle.
		u, hash, err := l.fetchUser(req.Context(), body.Username)
		if err != nil || hash == "" {
			VerifyDummy(req.Context(), body.Password)
			httperr.WriteMessage(w, req, messages.New("server.auth.invalid_credentials", nil))
			return
		}
		ok, err := VerifyPassword(req.Context(), body.Password, hash)
		if err != nil || !ok {
			httperr.WriteMessage(w, req, messages.New("server.auth.invalid_credentials", nil))
			return
		}
		perms, err := sessions.LoadPerms(req.Context(), u.ID)
		if err != nil {
			http.Error(w, "session error", http.StatusInternalServerError)
			return
		}
		u.Perms = perms
		// Load preferences so the login response can seed the theme in one
		// round-trip (D3, 2026-09-23) — same data GET /users/me embeds.
		prefs, err := l.db.GetPreferences(req.Context(), u.ID)
		if err != nil {
			http.Error(w, "session error", http.StatusInternalServerError)
			return
		}
		token, csrf, err := sessions.Create(req.Context(), u.ID)
		if err != nil {
			http.Error(w, "session error", http.StatusInternalServerError)
			return
		}
		setSessionCookie(w, token, sessionTTL)
		setCSRFCookie(w, csrf, sessionTTL)
		// Attribute the audit row to the user who just signed in; the
		// request itself is unauthenticated, so it would read "anonymous".
		SetActor(req.Context(), u.Username)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(loginResp{User: newUserJSON(u, prefs), CSRF: csrf})
	}
}

func (l *Local) fetchUser(ctx context.Context, username string) (*User, string, error) {
	var (
		u    User
		hash sql.NullString
	)
	err := l.db.DB.QueryRowContext(ctx,
		`SELECT id, username, display_name, email, role, COALESCE(pw_hash, '')
		 FROM users WHERE username = ?`,
		username,
	).Scan(&u.ID, &u.Username, &u.DisplayName, &u.Email, &u.Role, &hash)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, "", errors.New("no such user")
	}
	if err != nil {
		return nil, "", err
	}
	return &u, hash.String, nil
}
