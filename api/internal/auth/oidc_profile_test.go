package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

func TestOIDCProfile_BaseUsername(t *testing.T) {
	cases := []struct {
		name string
		p    oidcProfile
		want string
	}{
		{"preferred username wins", oidcProfile{Sub: "s", Email: "e@x", PreferredUsername: "alice"}, "alice"},
		{"email when no preferred username", oidcProfile{Sub: "s", Email: "e@x"}, "e@x"},
		{"subject as last resort", oidcProfile{Sub: "s"}, "s"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.p.baseUsername(); got != tc.want {
				t.Fatalf("baseUsername() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestOIDCProfile_MissingProfile(t *testing.T) {
	full := oidcProfile{Sub: "s", Email: "e@x", Name: "N", PreferredUsername: "u"}
	if full.missingProfile() {
		t.Fatal("full profile reported as missing claims")
	}
	for _, p := range []oidcProfile{
		{Sub: "s", Name: "N", PreferredUsername: "u"},
		{Sub: "s", Email: "e@x", PreferredUsername: "u"},
		{Sub: "s", Email: "e@x", Name: "N"},
	} {
		if !p.missingProfile() {
			t.Fatalf("%+v not reported as missing claims", p)
		}
	}
}

// newUserInfoProvider serves a discovery document whose userinfo endpoint
// answers with the given status and body, and returns a provider for it.
func newUserInfoProvider(t *testing.T, status int, body string) *oidc.Provider {
	t.Helper()
	var srv *httptest.Server
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"issuer":                                srv.URL,
			"authorization_endpoint":                srv.URL + "/authorize",
			"token_endpoint":                        srv.URL + "/token",
			"jwks_uri":                              srv.URL + "/jwks",
			"userinfo_endpoint":                     srv.URL + "/userinfo",
			"id_token_signing_alg_values_supported": []string{"RS256"},
		})
	})
	mux.HandleFunc("/userinfo", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	})
	srv = httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	provider, err := oidc.NewProvider(oidc.ClientContext(context.Background(), oidcHTTPClient), srv.URL)
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}
	return provider
}

func TestFillFromUserInfo_FillsOnlyEmptyClaims(t *testing.T) {
	o := &OIDC{provider: newUserInfoProvider(t, http.StatusOK,
		`{"sub":"sub-1","email":"info@x","name":"Info Name","preferred_username":"info-user"}`)}
	p := oidcProfile{Sub: "sub-1", Name: "Token Name"}
	o.fillFromUserInfo(context.Background(), &oauth2.Token{AccessToken: "at"}, &p)
	if p.Email != "info@x" || p.PreferredUsername != "info-user" {
		t.Fatalf("empty claims not filled: %+v", p)
	}
	if p.Name != "Token Name" {
		t.Fatalf("ID token name overwritten: %q", p.Name)
	}
}

func TestFillFromUserInfo_IgnoresMismatchedSubject(t *testing.T) {
	o := &OIDC{provider: newUserInfoProvider(t, http.StatusOK,
		`{"sub":"someone-else","email":"info@x","name":"Info","preferred_username":"info-user"}`)}
	p := oidcProfile{Sub: "sub-1"}
	o.fillFromUserInfo(context.Background(), &oauth2.Token{AccessToken: "at"}, &p)
	if p != (oidcProfile{Sub: "sub-1"}) {
		t.Fatalf("userinfo for another subject was used: %+v", p)
	}
}

func TestFillFromUserInfo_ErrorsLeaveProfileUnchanged(t *testing.T) {
	cases := map[string]*OIDC{
		"endpoint error": {provider: newUserInfoProvider(t, http.StatusInternalServerError, `oops`)},
		// preferred_username as a number decodes in go-oidc's UserInfo but
		// fails oidcProfile's string field.
		"claim type mismatch": {provider: newUserInfoProvider(t, http.StatusOK, `{"sub":"sub-1","preferred_username":42}`)},
	}
	for name, o := range cases {
		t.Run(name, func(t *testing.T) {
			p := oidcProfile{Sub: "sub-1"}
			o.fillFromUserInfo(context.Background(), &oauth2.Token{AccessToken: "at"}, &p)
			if p != (oidcProfile{Sub: "sub-1"}) {
				t.Fatalf("profile changed on error: %+v", p)
			}
		})
	}
}

func TestFillFromUserInfo_NoProviderOrToken(t *testing.T) {
	p := oidcProfile{Sub: "sub-1"}
	(&OIDC{}).fillFromUserInfo(context.Background(), &oauth2.Token{AccessToken: "at"}, &p)
	o := &OIDC{provider: newUserInfoProvider(t, http.StatusOK, `{"sub":"sub-1","email":"info@x"}`)}
	o.fillFromUserInfo(context.Background(), nil, &p)
	if p != (oidcProfile{Sub: "sub-1"}) {
		t.Fatalf("profile changed without provider or token: %+v", p)
	}
}

func TestResolveOrLinkUser_PreferredUsernameForNewUser(t *testing.T) {
	store := newAuthDB(t)
	o := &OIDC{}
	o.AttachStore(store)
	u, _, err := o.resolveOrLinkUser(context.Background(), "https://idp", "sub-p", "p@x", "P", "viewer", "none", false, "pat")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if u.Username != "pat" || u.Email != "p@x" || u.DisplayName != "P" {
		t.Fatalf("got %+v", u)
	}
}

func TestResolveOrLinkUser_RefreshesProfileAndRenamesSubjectUsername(t *testing.T) {
	store := newAuthDB(t)
	o := &OIDC{}
	o.AttachStore(store)
	ctx := context.Background()
	sub := "822f690935c6b943"

	first, _, err := o.resolveOrLinkUser(ctx, "https://idp", sub, "", "", "viewer", "none", false)
	if err != nil {
		t.Fatalf("first login: %v", err)
	}
	if first.Username != sub {
		t.Fatalf("first login username = %q, want subject", first.Username)
	}

	second, _, err := o.resolveOrLinkUser(ctx, "https://idp", sub, "bob@x", "Bob", "viewer", "none", false, "bob")
	if err != nil {
		t.Fatalf("second login: %v", err)
	}
	if second.ID != first.ID || second.Username != "bob" || second.Email != "bob@x" || second.DisplayName != "Bob" {
		t.Fatalf("second login = %+v", second)
	}
	var username, email, display, linkEmail string
	if err := store.DB.QueryRowContext(ctx,
		`SELECT username, email, display_name FROM users WHERE id = ?`, first.ID,
	).Scan(&username, &email, &display); err != nil {
		t.Fatalf("read user: %v", err)
	}
	if username != "bob" || email != "bob@x" || display != "Bob" {
		t.Fatalf("stored user = %q %q %q", username, email, display)
	}
	if err := store.DB.QueryRowContext(ctx,
		`SELECT email FROM oidc_links WHERE issuer = ? AND subject = ?`, "https://idp", sub,
	).Scan(&linkEmail); err != nil {
		t.Fatalf("read link: %v", err)
	}
	if linkEmail != "bob@x" {
		t.Fatalf("link email = %q, want bob@x", linkEmail)
	}

	// A later login without claims keeps what is stored and never renames
	// a username that is no longer the subject.
	third, _, err := o.resolveOrLinkUser(ctx, "https://idp", sub, "", "", "viewer", "none", false, "other")
	if err != nil {
		t.Fatalf("third login: %v", err)
	}
	if third.Username != "bob" || third.Email != "bob@x" || third.DisplayName != "Bob" {
		t.Fatalf("third login = %+v", third)
	}
}

func TestResolveOrLinkUser_RenameAvoidsTakenUsername(t *testing.T) {
	store := newAuthDB(t)
	ctx := context.Background()
	if _, err := store.DB.ExecContext(ctx,
		`INSERT INTO users(username, display_name, email, role) VALUES (?, ?, ?, 'admin')`,
		"carol", "Carol", "carol@local",
	); err != nil {
		t.Fatalf("seed: %v", err)
	}
	o := &OIDC{}
	o.AttachStore(store)
	sub := "subject-abcdef123"
	if _, _, err := o.resolveOrLinkUser(ctx, "https://idp", sub, "", "", "viewer", "none", false); err != nil {
		t.Fatalf("first login: %v", err)
	}
	u, _, err := o.resolveOrLinkUser(ctx, "https://idp", sub, "", "", "viewer", "none", false, "carol")
	if err != nil {
		t.Fatalf("second login: %v", err)
	}
	if u.Username == "carol" || !strings.HasPrefix(u.Username, "carol+") {
		t.Fatalf("username = %q, want a suffixed carol", u.Username)
	}
}
