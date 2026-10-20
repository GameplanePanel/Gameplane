// Package modlocale derives the translatable message ids of a game module from
// its module.yaml and template.yaml. gp-module and the operator share it so
// that authoring-time extraction and install-time validation agree on the id
// set. Inputs are already-decoded documents, which keeps this package free of
// any YAML dependency.
package modlocale

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/GameplanePanel/gameplane/i18n/pack"
)

// Ref is one translatable string of a module.
type Ref struct {
	// ID is the message id, e.g. "config.MAX_PLAYERS.displayName".
	ID string
	// English is the source text, already ICU-escaped so it parses as a
	// message with no placeholders.
	English string
	// Description is the translator note.
	Description string
}

// Escape makes a user-controlled name safe to use as one dot-separated id
// segment: "~" becomes "~0" and "." becomes "~1".
func Escape(seg string) string {
	return strings.ReplaceAll(strings.ReplaceAll(seg, "~", "~0"), ".", "~1")
}

// Unescape reverses Escape.
func Unescape(seg string) string {
	return strings.ReplaceAll(strings.ReplaceAll(seg, "~1", "."), "~0", "~")
}

// idRE is the module id rule: a fixed prefix (module, template, versions,
// config, actions, mods, metrics) followed by dot-separated segments of
// [A-Za-z0-9_~-]. Unlike dashboard and server ids (lower case only), module ids
// may use upper case because they embed names from module.yaml and
// template.yaml (for example MAX_PLAYERS).
var idRE = regexp.MustCompile(`^(module|template|versions|config|actions|mods|metrics)(\.[A-Za-z0-9_~-]+)+$`)

// ValidID reports whether id follows the module id rule: the prefixes above,
// dot-separated segments of [A-Za-z0-9_~-], and "~" only as the reversible
// escapes "~0" (a literal "~") and "~1" (a literal ".") produced by Escape.
func ValidID(id string) bool {
	if !idRE.MatchString(id) {
		return false
	}
	for i := 0; i < len(id); i++ {
		if id[i] != '~' {
			continue
		}
		if i+1 >= len(id) || (id[i+1] != '0' && id[i+1] != '1') {
			return false
		}
		i++
	}
	return true
}

// ICUEscape quotes plain text so that it parses as a literal ICU message:
// apostrophes are doubled and runs containing "{", "}", "<" or "|" are quoted.
func ICUEscape(s string) string {
	isSpecial := func(r byte) bool { return r == '{' || r == '}' || r == '<' || r == '|' }
	var b strings.Builder
	for i := 0; i < len(s); {
		c := s[i]
		if !isSpecial(c) {
			if c == '\'' {
				// A run of apostrophes that leads into a special character is
				// handled by the quoted-run branch below.
				j := i
				for j < len(s) && s[j] == '\'' {
					j++
				}
				if j < len(s) && isSpecial(s[j]) {
					i = quotedRun(&b, s, i)
					continue
				}
				b.WriteString("''")
				i++
				continue
			}
			b.WriteByte(c)
			i++
			continue
		}
		i = quotedRun(&b, s, i)
	}
	return b.String()
}

// quotedRun writes the maximal run of special characters and apostrophes
// starting at i as one quoted ICU section and returns the index after it.
func quotedRun(b *strings.Builder, s string, i int) int {
	j := i
	for j < len(s) && (s[j] == '\'' || s[j] == '{' || s[j] == '}' || s[j] == '<' || s[j] == '|') {
		j++
	}
	b.WriteByte('\'')
	for _, c := range []byte(s[i:j]) {
		if c == '\'' {
			b.WriteString("''")
			continue
		}
		b.WriteByte(c)
	}
	b.WriteByte('\'')
	return j
}

// Derive returns every translatable string of a module, sorted by id. module
// is the decoded module.yaml and template the decoded template.yaml (a full
// GameTemplate document or just its spec). Empty fields produce no id.
func Derive(module, template map[string]any) []Ref {
	var refs []Ref
	add := func(id, english, desc string) {
		if english == "" {
			return
		}
		refs = append(refs, Ref{ID: id, English: ICUEscape(english), Description: desc})
	}

	add("module.displayName", str(module, "displayName"), "Display name of the module (module.yaml displayName)")
	add("module.summary", str(module, "summary"), "One-line summary of the module (module.yaml summary)")

	spec := template
	if s, ok := template["spec"].(map[string]any); ok {
		spec = s
	}
	add("template.displayName", str(spec, "displayName"), "Display name of the game template (template.yaml spec.displayName)")
	add("template.description", str(spec, "description"), "Description of the game template (template.yaml spec.description)")

	for _, v := range list(spec, "versions") {
		id := str(v, "id")
		if id == "" {
			continue
		}
		add("versions."+Escape(id)+".displayName", str(v, "displayName"),
			fmt.Sprintf("Label of game version %s in the version picker (template.yaml spec.versions)", id))
	}

	for _, f := range list(spec, "configSchema") {
		name := str(f, "name")
		if name == "" {
			continue
		}
		base := "config." + Escape(name)
		add(base+".displayName", str(f, "displayName"),
			fmt.Sprintf("Label of configuration field %s (template.yaml spec.configSchema)", name))
		add(base+".description", str(f, "description"),
			fmt.Sprintf("Help text of configuration field %s (template.yaml spec.configSchema)", name))
		for _, opt := range strList(f, "enum") {
			add(base+".options."+Escape(opt), opt,
				fmt.Sprintf("Choice %q of configuration field %s (template.yaml spec.configSchema)", opt, name))
		}
	}

	caps, _ := spec["capabilities"].(map[string]any)
	for _, a := range list(caps, "actions") {
		id := str(a, "id")
		if id == "" {
			continue
		}
		base := "actions." + Escape(id)
		add(base+".displayName", str(a, "displayName"),
			fmt.Sprintf("Label of server action %s (template.yaml spec.capabilities.actions)", id))
		add(base+".description", str(a, "description"),
			fmt.Sprintf("Help text of server action %s (template.yaml spec.capabilities.actions)", id))
		add(base+".group", str(a, "group"),
			fmt.Sprintf("Group heading of server action %s (template.yaml spec.capabilities.actions)", id))
		for _, p := range list(a, "params") {
			pname := str(p, "name")
			if pname == "" {
				continue
			}
			pbase := base + ".params." + Escape(pname)
			add(pbase+".displayName", str(p, "displayName"),
				fmt.Sprintf("Label of parameter %s of server action %s (template.yaml spec.capabilities.actions)", pname, id))
			add(pbase+".description", str(p, "description"),
				fmt.Sprintf("Help text of parameter %s of server action %s (template.yaml spec.capabilities.actions)", pname, id))
			for _, opt := range strList(p, "enum") {
				add(pbase+".options."+Escape(opt), opt,
					fmt.Sprintf("Choice %q of parameter %s of server action %s (template.yaml spec.capabilities.actions)", opt, pname, id))
			}
		}
	}

	mods, _ := caps["mods"].(map[string]any)
	if loaders, ok := mods["loaders"].(map[string]any); ok {
		for key, raw := range loaders {
			l, _ := raw.(map[string]any)
			add("mods."+Escape(key)+".displayName", str(l, "displayName"),
				fmt.Sprintf("Label of the mod volume %s in the Mods tab (template.yaml spec.capabilities.mods.loaders)", key))
		}
	}

	status, _ := caps["status"].(map[string]any)
	for _, m := range list(status, "metrics") {
		id := str(m, "id")
		if id == "" {
			continue
		}
		add("metrics."+Escape(id)+".displayName", str(m, "displayName"),
			fmt.Sprintf("Label of status metric %s (template.yaml spec.capabilities.status.metrics)", id))
	}

	sort.SliceStable(refs, func(i, j int) bool { return refs[i].ID < refs[j].ID })
	return refs
}

// References turns Derive output into the English reference pack.
func References(refs []Ref) map[string]pack.Entry {
	out := make(map[string]pack.Entry, len(refs))
	for _, r := range refs {
		out[r.ID] = pack.Entry{DefaultMessage: r.English, Description: r.Description}
	}
	return out
}

func str(m map[string]any, key string) string {
	s, _ := m[key].(string)
	return s
}

func list(m map[string]any, key string) []map[string]any {
	raw, _ := m[key].([]any)
	out := make([]map[string]any, 0, len(raw))
	for _, item := range raw {
		if mm, ok := item.(map[string]any); ok {
			out = append(out, mm)
		}
	}
	return out
}

func strList(m map[string]any, key string) []string {
	raw, _ := m[key].([]any)
	out := make([]string, 0, len(raw))
	for _, item := range raw {
		if s, ok := item.(string); ok {
			out = append(out, s)
		}
	}
	return out
}
