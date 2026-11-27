package audit

import "testing"

// TestThemeEventNames pins the event strings to the names in data-model.md so a
// rename cannot silently change what operators search for in the audit log.
func TestThemeEventNames(t *testing.T) {
	cases := map[string]string{
		EventThemeSourceCreate:  "theme_source.create",
		EventThemeSourceUpdate:  "theme_source.update",
		EventThemeSourceDelete:  "theme_source.delete",
		EventThemeSourceRefresh: "theme_source.refresh",
		EventThemeInstall:       "theme.install",
		EventThemeUpdate:        "theme.update",
		EventThemeRemove:        "theme.remove",
	}
	for got, want := range cases {
		if got != want {
			t.Errorf("event name = %q, want %q", got, want)
		}
	}
}

// TestThemeTarget checks the target layout and the quoting rules.
func TestThemeTarget(t *testing.T) {
	cases := []struct {
		name  string
		event string
		kv    []string
		want  string
	}{
		{
			name:  "install with empty from",
			event: EventThemeInstall,
			kv:    []string{"source", "official", "theme", "deep-ocean", "from", "", "to", "1.2.0", "digest", "sha256:abc"},
			want:  "theme.install source=official theme=deep-ocean from= to=1.2.0 digest=sha256:abc",
		},
		{
			name:  "no pairs",
			event: EventThemeRemove,
			want:  "theme.remove",
		},
		{
			name:  "value with space is quoted",
			event: EventThemeSourceCreate,
			kv:    []string{"name", "my source"},
			want:  `theme_source.create name="my source"`,
		},
		{
			name:  "value with equals and quote is quoted",
			event: EventThemeSourceUpdate,
			kv:    []string{"name", `a="b"`},
			want:  `theme_source.update name="a=\"b\""`,
		},
		{
			name:  "trailing key without value",
			event: EventThemeSourceRefresh,
			kv:    []string{"name", "community", "type"},
			want:  "theme_source.refresh name=community type=",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := ThemeTarget(c.event, c.kv...); got != c.want {
				t.Errorf("ThemeTarget() = %q, want %q", got, c.want)
			}
		})
	}
}
