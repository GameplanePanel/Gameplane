package audit

import (
	"strconv"
	"strings"
)

// Theme marketplace audit event names (spec 043, data-model.md "Audit events").
// The audit table has no event column: handlers pass one of these names as the
// first word of the request's audit target, built with ThemeTarget.
const (
	// EventThemeSourceCreate is recorded when an admin adds a theme source.
	EventThemeSourceCreate = "theme_source.create"
	// EventThemeSourceUpdate is recorded when an admin edits a theme source.
	EventThemeSourceUpdate = "theme_source.update"
	// EventThemeSourceDelete is recorded when an admin deletes a theme source.
	EventThemeSourceDelete = "theme_source.delete"
	// EventThemeSourceRefresh is recorded when an admin triggers an on-demand index of a source.
	EventThemeSourceRefresh = "theme_source.refresh"
	// EventThemeInstall is recorded when a user installs a theme from the catalog.
	EventThemeInstall = "theme.install"
	// EventThemeUpdate is recorded when a user moves an installed theme to a newer version.
	EventThemeUpdate = "theme.update"
	// EventThemeRemove is recorded when a user removes an installed theme.
	EventThemeRemove = "theme.remove"
)

// ThemeTarget builds the audit target string for a theme event: the event name
// followed by key=value pairs in the order given, for example
// "theme.install source=official theme=deep-ocean from= to=1.2.0 digest=sha256:...".
//
// kv is read as alternating keys and values. A trailing key with no value is
// recorded with an empty value. Values that contain spaces, quotes, '=' or
// control characters are quoted with strconv.Quote so the string stays
// unambiguous. Callers must pass only names, versions and digests. Never pass
// credentials, tokens, or URLs that may carry userinfo.
func ThemeTarget(event string, kv ...string) string {
	var b strings.Builder
	b.WriteString(event)
	for i := 0; i < len(kv); i += 2 {
		key := kv[i]
		value := ""
		if i+1 < len(kv) {
			value = kv[i+1]
		}
		b.WriteByte(' ')
		b.WriteString(key)
		b.WriteByte('=')
		if needsQuote(value) {
			b.WriteString(strconv.Quote(value))
		} else {
			b.WriteString(value)
		}
	}
	return b.String()
}

// needsQuote reports whether a target value must be quoted to stay unambiguous.
func needsQuote(v string) bool {
	for _, r := range v {
		if r == ' ' || r == '"' || r == '=' || r < 0x20 || r == 0x7f {
			return true
		}
	}
	return false
}
