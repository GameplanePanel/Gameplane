package modlocale

import (
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/GameplanePanel/gameplane/i18n/icu"
	"github.com/GameplanePanel/gameplane/i18n/pack"
	"golang.org/x/text/language"
)

func TestEscapeVectors(t *testing.T) {
	t.Parallel()
	cases := []struct{ raw, escaped string }{
		{"a.b", "a~1b"},
		{"a~b", "a~0b"},
		{"a~.b", "a~0~1b"},
		{"~1", "~01"},
		{"plain", "plain"},
		{"", ""},
	}
	for _, c := range cases {
		if got := Escape(c.raw); got != c.escaped {
			t.Errorf("Escape(%q) = %q, want %q", c.raw, got, c.escaped)
		}
		if got := Unescape(c.escaped); got != c.raw {
			t.Errorf("Unescape(%q) = %q, want %q", c.escaped, got, c.raw)
		}
	}
}

func TestEscapeRoundTrip(t *testing.T) {
	t.Parallel()
	alphabet := []string{".", "~", "0", "1", "a", "~0", "~1"}
	var walk func(prefix string, depth int)
	walk = func(prefix string, depth int) {
		if got := Unescape(Escape(prefix)); got != prefix {
			t.Fatalf("round trip of %q gave %q", prefix, got)
		}
		if strings.Contains(Escape(prefix), ".") {
			t.Fatalf("Escape(%q) still contains a dot", prefix)
		}
		if depth == 0 {
			return
		}
		for _, a := range alphabet {
			walk(prefix+a, depth-1)
		}
	}
	walk("", 4)
}

func TestICUEscapeParsesAsLiteral(t *testing.T) {
	t.Parallel()
	inputs := []string{
		"plain text",
		"Max players {20}",
		"a < b | c > d",
		"it's fine",
		"''double''",
		"'{",
		"{'",
		"{'{",
		"{{}}",
		"<b>bold</b>",
		"trailing'",
		"'",
		"''",
		"'|'",
		"100% {x} 'quoted' <tag>",
	}
	for _, in := range inputs {
		m, err := icu.Parse(ICUEscape(in))
		if err != nil {
			t.Errorf("ICUEscape(%q) = %q does not parse: %v", in, ICUEscape(in), err)
			continue
		}
		if args := icu.Arguments(m); len(args) != 0 {
			t.Errorf("ICUEscape(%q) has placeholders %v", in, args)
		}
		if tags := icu.Tags(m); len(tags) != 0 {
			t.Errorf("ICUEscape(%q) has tags %v", in, tags)
		}
		if got := icu.Format(m, language.English, nil); got != in {
			t.Errorf("ICUEscape(%q) formats back as %q", in, got)
		}
	}
}

func fixtureModule() map[string]any {
	return map[string]any{"displayName": "Minecraft", "summary": "Run {Java} servers"}
}

func fixtureTemplate() map[string]any {
	return map[string]any{
		"apiVersion": "gameplane.io/v1alpha1",
		"kind":       "GameTemplate",
		"spec": map[string]any{
			"displayName": "Minecraft Java",
			"description": "Blocky game",
			"versions": []any{
				map[string]any{"id": "1.21.4", "displayName": "1.21.4 (Paper)", "image": "x"},
				map[string]any{"id": "", "displayName": "ignored"},
			},
			"configSchema": []any{
				map[string]any{
					"name": "MAX_PLAYERS", "displayName": "Max players", "description": "How many {n}",
					"type": "string", "default": "20",
				},
				map[string]any{
					"name": "MODE", "displayName": "Mode",
					"enum": []any{"survival", "creative", "a.b"},
				},
				map[string]any{"name": "NOLABEL"},
			},
			"capabilities": map[string]any{
				"actions": []any{
					map[string]any{
						"id": "say", "displayName": "Say", "description": "Broadcast", "group": "Chat",
						"command": "say {msg}",
						"params": []any{
							map[string]any{
								"name": "msg", "displayName": "Message", "description": "Text",
								"enum": []any{"hi", "bye"},
							},
						},
					},
				},
				"mods": map[string]any{
					"loaders": map[string]any{
						"paper": map[string]any{"displayName": "Plugins", "path": "plugins"},
						"bare":  map[string]any{"path": "mods"},
					},
				},
				"status": map[string]any{
					"metrics": []any{
						map[string]any{"id": "tps", "displayName": "TPS", "command": "tps"},
					},
				},
			},
		},
	}
}

func idsOf(refs []Ref) []string {
	out := make([]string, len(refs))
	for i, r := range refs {
		out[i] = r.ID
	}
	return out
}

func TestDeriveCoversEveryRow(t *testing.T) {
	t.Parallel()
	refs := Derive(fixtureModule(), fixtureTemplate())
	want := []string{
		"actions.say.description",
		"actions.say.displayName",
		"actions.say.group",
		"actions.say.params.msg.description",
		"actions.say.params.msg.displayName",
		"actions.say.params.msg.options.bye",
		"actions.say.params.msg.options.hi",
		"config.MAX_PLAYERS.description",
		"config.MAX_PLAYERS.displayName",
		"config.MODE.displayName",
		"config.MODE.options.a~1b",
		"config.MODE.options.creative",
		"config.MODE.options.survival",
		"metrics.tps.displayName",
		"mods.paper.displayName",
		"module.displayName",
		"module.summary",
		"template.description",
		"template.displayName",
		"versions.1~11~14.displayName",
	}
	// "1.21.4" escapes to "1~121~14"; recompute rather than hand-write it.
	want[len(want)-1] = "versions." + Escape("1.21.4") + ".displayName"
	sort.Strings(want)
	if got := idsOf(refs); !reflect.DeepEqual(got, want) {
		t.Fatalf("ids mismatch\n got: %v\nwant: %v", got, want)
	}
}

func TestDeriveAcceptsBareSpec(t *testing.T) {
	t.Parallel()
	spec := fixtureTemplate()["spec"].(map[string]any)
	a := idsOf(Derive(fixtureModule(), spec))
	b := idsOf(Derive(fixtureModule(), fixtureTemplate()))
	if !reflect.DeepEqual(a, b) {
		t.Fatalf("bare spec and full document differ:\n%v\n%v", a, b)
	}
}

func TestDeriveOmitsEmptyAndIsDeterministic(t *testing.T) {
	t.Parallel()
	refs := Derive(nil, map[string]any{})
	if len(refs) != 0 {
		t.Fatalf("expected no ids, got %v", idsOf(refs))
	}
	first := Derive(fixtureModule(), fixtureTemplate())
	for range 20 {
		if again := Derive(fixtureModule(), fixtureTemplate()); !reflect.DeepEqual(first, again) {
			t.Fatal("Derive is not deterministic")
		}
	}
	for _, r := range first {
		if strings.Contains(r.ID, "NOLABEL") {
			t.Errorf("field without labels produced id %s", r.ID)
		}
		if r.Description == "" {
			t.Errorf("%s has no description", r.ID)
		}
	}
}

func TestDeriveEscapesEnglish(t *testing.T) {
	t.Parallel()
	refs := Derive(fixtureModule(), fixtureTemplate())
	for _, r := range refs {
		if r.ID == "module.summary" && r.English != "Run '{'Java'}' servers" {
			t.Fatalf("summary English = %q", r.English)
		}
	}
}

func TestReferencesValidate(t *testing.T) {
	t.Parallel()
	ref := References(Derive(fixtureModule(), fixtureTemplate()))
	if fs := pack.ValidateReference(ref, "en.json"); len(fs) != 0 {
		t.Fatalf("derived reference is invalid: %v", fs)
	}
	if got := ref["module.displayName"].DefaultMessage; got != "Minecraft" {
		t.Fatalf("module.displayName = %q", got)
	}
}

func TestDeriveIgnoresMalformedShapes(t *testing.T) {
	t.Parallel()
	tmpl := map[string]any{
		"spec": map[string]any{
			"versions":     "not-a-list",
			"configSchema": []any{"scalar", map[string]any{"name": 7}},
			"capabilities": map[string]any{
				"actions": []any{map[string]any{"displayName": "no id"}},
				"mods":    map[string]any{"loaders": map[string]any{"x": "scalar"}},
				"status":  map[string]any{"metrics": []any{map[string]any{"displayName": "no id"}}},
			},
		},
	}
	if refs := Derive(nil, tmpl); len(refs) != 0 {
		t.Fatalf("expected no ids, got %v", idsOf(refs))
	}
}

func TestValidIDAcceptsDerivedIDs(t *testing.T) {
	t.Parallel()
	for _, r := range Derive(fixtureModule(), fixtureTemplate()) {
		if !ValidID(r.ID) {
			t.Errorf("derived id %q fails ValidID", r.ID)
		}
	}
}

func TestValidIDVectors(t *testing.T) {
	t.Parallel()
	valid := []string{
		"module.displayName",
		"config.MAX_PLAYERS.displayName",
		"config.a~1b.options.x~0y",
		"versions.1-21~1x.displayName",
	}
	invalid := []string{
		"config.a.b..c",
		"Config.x",
		"config.x~2",
		"config.x~",
		"config",
		"unknown.x",
		"config.has space",
		"",
	}
	for _, id := range valid {
		if !ValidID(id) {
			t.Errorf("ValidID(%q) = false, want true", id)
		}
	}
	for _, id := range invalid {
		if ValidID(id) {
			t.Errorf("ValidID(%q) = true, want false", id)
		}
	}
}

func TestEscapeRoundTripTildes(t *testing.T) {
	t.Parallel()
	for _, raw := range []string{"a~1b", "~~..", "~", ".", "~0.~1", "a.b~c"} {
		if got := Unescape(Escape(raw)); got != raw {
			t.Errorf("Unescape(Escape(%q)) = %q", raw, got)
		}
	}
}
