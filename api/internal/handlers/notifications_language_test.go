package handlers

import (
	"errors"
	"strings"
	"testing"

	"github.com/GameplanePanel/gameplane/api/internal/httperr"
)

// overrideListed points languageListed at a fixed set of tags for one test.
func overrideListed(t *testing.T, tags ...string) {
	t.Helper()
	prev := languageListed
	languageListed = func(tag string) bool {
		for _, want := range tags {
			if tag == want {
				return true
			}
		}
		return false
	}
	t.Cleanup(func() { languageListed = prev })
}

func TestValidateNotifications_Language(t *testing.T) {
	overrideListed(t, "en", "fr")

	out, err := validateNotifications([]byte(`{"sinks":[{"name":"a","kind":"webhook","enabled":true,"language":"fr"}]}`))
	if err != nil {
		t.Fatalf("listed language: %v", err)
	}
	if !strings.Contains(string(out), `"language":"fr"`) {
		t.Fatalf("canonical JSON dropped language: %s", out)
	}

	_, err = validateNotifications([]byte(`{"sinks":[{"name":"a","kind":"webhook","enabled":true,"language":"zz"}]}`))
	var m *httperr.Message
	if !errors.As(err, &m) {
		t.Fatalf("unknown language: err = %v, want *httperr.Message", err)
	}
	if m.ID != "server.i18n.unknown_language" {
		t.Fatalf("ID = %q", m.ID)
	}
	if m.Args["language"] != "zz" {
		t.Fatalf("args.language = %v, want zz", m.Args["language"])
	}

	out, err = validateNotifications([]byte(`{"sinks":[{"name":"a","kind":"webhook","enabled":true}]}`))
	if err != nil {
		t.Fatalf("omitted language: %v", err)
	}
	if strings.Contains(string(out), `"language"`) {
		t.Fatalf("omitted language emitted a key: %s", out)
	}
}
