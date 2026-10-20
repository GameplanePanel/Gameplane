package handlers

import (
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/GameplanePanel/gameplane/api/internal/httperr"
)

// These tests pin the per-section validators directly so error branches
// don't need a full HTTP round-trip through PUT.

func TestValidateGeneral(t *testing.T) {
	cases := []struct {
		name string
		in   string
		ok   bool
		errs string
	}{
		{"empty instanceName", `{"defaultNamespace":"x"}`, true, ""},
		{"missing default ns", `{"instanceName":"k"}`, false, "defaultNamespace"},
		{"bad ns label", `{"instanceName":"k","defaultNamespace":"BadCAPS"}`, false, "RFC1123"},
		{"bad external url", `{"instanceName":"k","defaultNamespace":"n","externalURL":"ftp://x"}`, false, "http"},
		{"happy path", `{"instanceName":"k","defaultNamespace":"n","externalURL":"https://example.com"}`, true, ""},
		{"happy path no url", `{"instanceName":"k","defaultNamespace":"n"}`, true, ""},
		{"bad json", `not json`, false, "invalid json"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := validateGeneral([]byte(tc.in))
			if tc.ok && err != nil {
				t.Fatalf("unexpected: %v", err)
			}
			if !tc.ok && (err == nil || !strings.Contains(err.Error(), tc.errs)) {
				t.Fatalf("got %v, want substring %q", err, tc.errs)
			}
		})
	}
}

func TestValidateGeneral_EmptyInstanceName(t *testing.T) {
	canon, err := validateGeneral([]byte(`{"instanceName":"","defaultNamespace":"games"}`))
	if err != nil {
		t.Fatalf("empty instanceName should be valid: %v", err)
	}
	if string(canon) != `{"instanceName":"","externalURL":"","defaultNamespace":"games"}` {
		t.Fatalf("canonicalized output: got %s", canon)
	}
}

func TestValidateAuth(t *testing.T) {
	// A complete, valid OIDC provider entry the cases below vary from.
	const corp = `{"name":"corp","kind":"oidc","enabled":true,"issuer":"https://idp.example","clientID":"gameplane"}`
	cases := []struct {
		name string
		helm bool
		in   string
		ok   bool
		errs string
	}{
		{"empty providers", false, `{"providers":[]}`, false, "at least one identity provider"},
		{"missing name", false, `{"providers":[{"kind":"local"}]}`, false, "name is required"},
		{"duplicate name", false, `{"providers":[{"name":"a","kind":"local"},{"name":"a","kind":"oidc"}]}`, false, "duplicate"},
		{"unknown kind", false, `{"providers":[{"name":"a","kind":"weird"}]}`, false, "kind must"},
		{"bad configRef", false, `{"providers":[{"name":"a","kind":"local","configRef":"BAD"}]}`, false, "configRef"},
		{"happy path", false, `{"providers":[{"name":"local","kind":"local","enabled":true},` + corp + `]}`, true, ""},
		{"all providers disabled", false, `{"providers":[{"name":"local","kind":"local","enabled":false}]}`, false, "at least one identity provider"},
		{"helm provider satisfies the guard", true, `{"providers":[{"name":"local","kind":"local","enabled":false}]}`, true, ""},
		{"oidc missing issuer", false, `{"providers":[{"name":"corp","kind":"oidc","enabled":true,"clientID":"g"}]}`, false, "issuer"},
		{"oidc bad issuer", false, `{"providers":[{"name":"corp","kind":"oidc","enabled":true,"issuer":"ftp://x","clientID":"g"}]}`, false, "issuer"},
		{"oidc missing clientID", false, `{"providers":[{"name":"corp","kind":"oidc","enabled":true,"issuer":"https://idp.example"}]}`, false, "clientID"},
		{"oidc name not a label", false, `{"providers":[{"name":"Corp SSO","kind":"oidc","enabled":true,"issuer":"https://idp.example","clientID":"g"}]}`, false, "DNS label"},
		{"helm name reserved", false, `{"providers":[{"name":"helm","kind":"oidc","enabled":true,"issuer":"https://idp.example","clientID":"g"}]}`, false, "reserved"},
		{"two locals", false, `{"providers":[{"name":"local","kind":"local","enabled":true},{"name":"Local accounts","kind":"local","enabled":true}]}`, false, "one local"},
		{"legacy local name accepted", false, `{"providers":[{"name":"Local accounts","kind":"local","enabled":true}]}`, true, ""},
		{"bad json", false, `{`, false, "invalid json"},
		// Group→role mapping fields.
		{"mapping happy path", false, `{"providers":[{"name":"corp","kind":"oidc","enabled":true,"issuer":"https://idp.example","clientID":"g","scopes":["groups"],"groupsClaim":"memberOf","roleMappings":{"admin":["gp-admins"],"operator":["gp-ops"],"viewer":["gp-view"]},"defaultRole":"deny"}]}`, true, ""},
		{"mapping without defaultRole ok", false, `{"providers":[{"name":"corp","kind":"oidc","enabled":true,"issuer":"https://idp.example","clientID":"g","roleMappings":{"admin":["gp-admins"]}}]}`, true, ""},
		{"scopes on local", false, `{"providers":[{"name":"local","kind":"local","enabled":true,"scopes":["groups"]}]}`, false, "not valid for the local provider"},
		{"groupsClaim on local", false, `{"providers":[{"name":"local","kind":"local","enabled":true,"groupsClaim":"memberOf"}]}`, false, "not valid for the local provider"},
		{"roleMappings on local", false, `{"providers":[{"name":"local","kind":"local","enabled":true,"roleMappings":{"admin":["x"]}}]}`, false, "not valid for the local provider"},
		{"defaultRole on local", false, `{"providers":[{"name":"local","kind":"local","enabled":true,"defaultRole":"viewer"}]}`, false, "not valid for the local provider"},
		{"empty scope token", false, `{"providers":[{"name":"corp","kind":"oidc","enabled":true,"issuer":"https://idp.example","clientID":"g","scopes":["groups","  "]}]}`, false, "scopes[1]"},
		{"scope with inner whitespace", false, `{"providers":[{"name":"corp","kind":"oidc","enabled":true,"issuer":"https://idp.example","clientID":"g","scopes":["two scopes"]}]}`, false, "without whitespace"},
		{"blank groupsClaim", false, `{"providers":[{"name":"corp","kind":"oidc","enabled":true,"issuer":"https://idp.example","clientID":"g","groupsClaim":"   "}]}`, false, "groupsClaim"},
		{"bad defaultRole", false, `{"providers":[{"name":"corp","kind":"oidc","enabled":true,"issuer":"https://idp.example","clientID":"g","roleMappings":{"admin":["x"]},"defaultRole":"root"}]}`, false, "defaultRole must be one of"},
		{"defaultRole without mappings", false, `{"providers":[{"name":"corp","kind":"oidc","enabled":true,"issuer":"https://idp.example","clientID":"g","defaultRole":"deny"}]}`, false, "requires roleMappings"},
		{"empty mapping group", false, `{"providers":[{"name":"corp","kind":"oidc","enabled":true,"issuer":"https://idp.example","clientID":"g","roleMappings":{"operator":[""]}}]}`, false, "roleMappings.operator[0]"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := validateAuth(tc.helm)([]byte(tc.in))
			if tc.ok && err != nil {
				t.Fatalf("unexpected: %v", err)
			}
			if !tc.ok && (err == nil || !strings.Contains(err.Error(), tc.errs)) {
				t.Fatalf("got %v want %q", err, tc.errs)
			}
		})
	}
}

// Scope tokens and the groups claim are trimmed in place so the persisted
// canonical blob stores clean values.
func TestValidateAuthTrimsMappingFields(t *testing.T) {
	canon, err := validateAuth(false)([]byte(`{"providers":[{"name":"corp","kind":"oidc","enabled":true,` +
		`"issuer":"https://idp.example","clientID":"g","scopes":[" groups "],"groupsClaim":" memberOf "}]}`))
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if !strings.Contains(string(canon), `"scopes":["groups"]`) {
		t.Fatalf("scope not trimmed: %s", canon)
	}
	if !strings.Contains(string(canon), `"groupsClaim":"memberOf"`) {
		t.Fatalf("groupsClaim not trimmed: %s", canon)
	}
}

func TestValidateNotifications(t *testing.T) {
	cases := []struct {
		name string
		in   string
		ok   bool
		errs string
	}{
		{"empty", `{"sinks":[]}`, true, ""},
		{"missing name", `{"sinks":[{"kind":"discord"}]}`, false, "name is required"},
		{"duplicate", `{"sinks":[{"name":"a","kind":"discord"},{"name":"a","kind":"slack"}]}`, false, "duplicate"},
		{"bad kind", `{"sinks":[{"name":"a","kind":"weird"}]}`, false, "kind must"},
		{"happy", `{"sinks":[{"name":"x","kind":"smtp"}]}`, true, ""},
		{"happy ntfy", `{"sinks":[{"name":"x","kind":"ntfy","enabled":true,"configRef":"gameplane-notify-x"}]}`, true, ""},
		{"bad configRef", `{"sinks":[{"name":"a","kind":"discord","configRef":"Not_A_Label"}]}`, false, "configRef"},
		{"happy configRef+events", `{"sinks":[{"name":"a","kind":"discord","enabled":true,"configRef":"team-hook","events":["backup.failed","server.unhealthy"]}]}`, true, ""},
		{"unknown event", `{"sinks":[{"name":"a","kind":"discord","events":["server.rebooted"]}]}`, false, "unknown event"},
		{"duplicate event", `{"sinks":[{"name":"a","kind":"discord","events":["backup.failed","backup.failed"]}]}`, false, "duplicate"},
		{"bad json", `{`, false, "invalid json"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := validateNotifications([]byte(tc.in))
			if tc.ok && err != nil {
				t.Fatalf("unexpected: %v", err)
			}
			if !tc.ok && (err == nil || !strings.Contains(err.Error(), tc.errs)) {
				t.Fatalf("got %v want %q", err, tc.errs)
			}
		})
	}
}

// Sink rows persisted before configRef/events existed must keep loading
// and must canonicalize without sprouting the new fields.
func TestValidateNotificationsLegacyBlob(t *testing.T) {
	canon, err := validateNotifications([]byte(`{"sinks":[{"name":"a","kind":"discord","enabled":true}]}`))
	if err != nil {
		t.Fatalf("legacy blob: %v", err)
	}
	if string(canon) != `{"sinks":[{"name":"a","kind":"discord","enabled":true}]}` {
		t.Fatalf("canonicalized output: got %s", canon)
	}
}

func TestValidateTelemetry(t *testing.T) {
	canon, err := validateTelemetry([]byte(`{"sendMetrics":true,"unknown":"dropped"}`))
	if err != nil {
		t.Fatalf("happy: %v", err)
	}
	if string(canon) != `{"sendMetrics":true,"extended":false}` {
		t.Fatalf("canonicalized output: got %s", canon)
	}
	canon, err = validateTelemetry([]byte(`{"sendMetrics":true,"extended":true}`))
	if err != nil || string(canon) != `{"sendMetrics":true,"extended":true}` {
		t.Fatalf("both tiers on: got %s, %v", canon, err)
	}
	// Basic off turns extended off with it and the save is not rejected
	// (FR-003, spec Q7).
	canon, err = validateTelemetry([]byte(`{"sendMetrics":false,"extended":true}`))
	if err != nil || string(canon) != `{"sendMetrics":false,"extended":false}` {
		t.Fatalf("basic off must normalise extended off: got %s, %v", canon, err)
	}
	if _, err := validateTelemetry([]byte(`bogus`)); err == nil {
		t.Fatal("expected json error")
	}
}

func TestValidateModRegistries(t *testing.T) {
	cases := []struct {
		name string
		in   string
		ok   bool
		errs string
	}{
		{"empty", `{"registries":[]}`, true, ""},
		{"happy curseforge", `{"registries":[{"provider":"curseforge"}]}`, true, ""},
		{"happy with configRef", `{"registries":[{"provider":"curseforge","configRef":"my-cf-secret"}]}`, true, ""},
		{"happy all keyed providers", `{"registries":[{"provider":"curseforge"},{"provider":"steam"},{"provider":"nexus"}]}`, true, ""},
		{"unknown provider", `{"registries":[{"provider":"modrinth"}]}`, false, "must be one of"},
		{"duplicate provider", `{"registries":[{"provider":"curseforge"},{"provider":"curseforge"}]}`, false, "duplicate"},
		{"bad configRef", `{"registries":[{"provider":"curseforge","configRef":"Not_A_Label"}]}`, false, "configRef"},
		{"bad json", `{`, false, "invalid json"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := validateModRegistries([]byte(tc.in))
			if tc.ok && err != nil {
				t.Fatalf("unexpected: %v", err)
			}
			if !tc.ok && (err == nil || !strings.Contains(err.Error(), tc.errs)) {
				t.Fatalf("got %v want substring %q", err, tc.errs)
			}
		})
	}
}

// validateModRegistries never round-trips key material — the type itself
// has no field for it, so a canonicalized blob can't carry one even if a
// caller tried to sneak an extra "apiKey" field into the request body.
func TestValidateModRegistriesDropsUnknownFields(t *testing.T) {
	canon, err := validateModRegistries([]byte(`{"registries":[{"provider":"curseforge","apiKey":"should-be-dropped"}]}`))
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if strings.Contains(string(canon), "should-be-dropped") || strings.Contains(string(canon), "apiKey") {
		t.Fatalf("canonicalized output leaked apiKey: %s", canon)
	}
}

// The "updates" section is no longer writable — the channel is the
// chart's informational value on /cluster/info. PUTs must 400 as an
// unknown section (covered by the round-trip tests in config_test.go).
func TestUpdatesSectionRemoved(t *testing.T) {
	for _, helm := range []bool{false, true} {
		if _, ok := newValidators(helm)["updates"]; ok {
			t.Fatal(`"updates" must not be a writable config section`)
		}
	}
}

func TestValidateI18n(t *testing.T) {
	cases := []struct {
		name string
		in   string
		ok   bool
	}{
		{"valid en", `{"defaultLanguage":"en"}`, true},
		{"unknown key", `{"defaultLanguage":"en","extra":1}`, false},
		{"missing field", `{}`, false},
		{"empty tag", `{"defaultLanguage":""}`, false},
		{"bad json", `not json`, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			canon, err := validateI18n([]byte(tc.in))
			if tc.ok && err != nil {
				t.Fatalf("unexpected: %v", err)
			}
			if tc.ok && string(canon) != tc.in {
				t.Fatalf("canonicalized output: got %s, want %s", canon, tc.in)
			}
			if !tc.ok && err == nil {
				t.Fatal("want error, got nil")
			}
		})
	}
}

func TestValidateI18n_UnlistedTagIsLocalizableMessage(t *testing.T) {
	_, err := validateI18n([]byte(`{"defaultLanguage":"xx"}`))
	var m *httperr.Message
	if !errors.As(err, &m) {
		t.Fatalf("err = %v (%T), want *httperr.Message", err, err)
	}
	if m.ID != "server.i18n.unlisted_language" {
		t.Fatalf("ID = %q, want server.i18n.unlisted_language", m.ID)
	}
	if m.Status != http.StatusBadRequest {
		t.Fatalf("Status = %d, want 400", m.Status)
	}
	if m.Args["language"] != "xx" {
		t.Fatalf("args.language = %v, want xx", m.Args["language"])
	}
}
