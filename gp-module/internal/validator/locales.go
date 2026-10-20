package validator

import (
	"fmt"
	"sort"
	"strings"

	"github.com/GameplanePanel/gameplane/i18n/modlocale"
	"github.com/GameplanePanel/gameplane/i18n/pack"
	"gopkg.in/yaml.v3"
)

const (
	localesDir    = "locales/"
	localesSuffix = ".json"
	englishLocale = "en"
)

// localeTag returns the language tag of a "locales/<tag>.json" file name.
func localeTag(name string) (string, bool) {
	if !strings.HasPrefix(name, localesDir) || !strings.HasSuffix(name, localesSuffix) {
		return "", false
	}
	tag := strings.TrimSuffix(strings.TrimPrefix(name, localesDir), localesSuffix)
	if tag == "" || strings.Contains(tag, "/") {
		return "", false
	}
	return tag, true
}

// validateLocales checks every locales/<tag>.json translation against the
// English reference derived from module.yaml and template.yaml. Modules
// without a locales directory are unaffected.
func validateLocales(files map[string][]byte) []Finding {
	var names []string
	for name := range files {
		if _, ok := localeTag(name); ok {
			names = append(names, name)
		}
	}
	if len(names) == 0 {
		return nil
	}
	sort.Strings(names)

	ref, ok := localeReference(files)
	var findings []Finding
	for _, name := range names {
		tag, _ := localeTag(name)
		if tag == englishLocale {
			continue
		}
		if !pack.ValidTag(tag) {
			findings = append(findings, localeFinding(name, "", fmt.Sprintf("%q is not a valid language tag", tag),
				"Name the file after a BCP 47 tag such as fr.json, pt-BR.json or zh-Hans.json."))
			continue
		}
		tr, err := pack.ParseEntries(files[name])
		if err != nil {
			findings = append(findings, localeFinding(name, "", fmt.Sprintf("translation file is malformed: %v", err),
				"Make the file a JSON object mapping message ids to {\"defaultMessage\": \"...\"}."))
			continue
		}
		if !ok {
			continue
		}
		for _, f := range pack.ValidateTranslation(ref, tr, name) {
			if f.Code == pack.CodeNeedsReview {
				findings = append(findings, Finding{
					Level:       SeverityWarn,
					RuleID:      RuleLocaleNeedsReview,
					File:        name,
					Field:       f.ID,
					Message:     "English text changed since this was translated; please review",
					Remediation: "Review the translation, then run 'gp-module i18n stamp' to record the current English text.",
				})
				continue
			}
			if f.Severity != pack.SeverityError {
				continue
			}
			findings = append(findings, localeFinding(name, f.ID, fmt.Sprintf("[%s] %s", f.Code, f.Message),
				"Fix or remove the entry; invalid entries are dropped when the module is installed."))
		}
	}
	return findings
}

// localeReference derives the English reference pack from the manifests, or
// falls back to locales/en.json when the manifests derive nothing. ok is false
// when no reference is available.
func localeReference(files map[string][]byte) (map[string]pack.Entry, bool) {
	if refs := deriveLocaleRefs(files); len(refs) > 0 {
		return modlocale.References(refs), true
	}
	if data, found := files[localesDir+englishLocale+localesSuffix]; found {
		if ref, err := pack.ParseEntries(data); err == nil {
			return ref, true
		}
	}
	return nil, false
}

// deriveLocaleRefs parses module.yaml and template.yaml the way localeReference
// does and returns the translatable ids derived from them. A manifest that
// fails to parse contributes nothing.
func deriveLocaleRefs(files map[string][]byte) []modlocale.Ref {
	var module, template map[string]any
	if data, found := files["module.yaml"]; found {
		if err := yaml.Unmarshal(data, &module); err != nil {
			module = nil
		}
	}
	if data, found := files["template.yaml"]; found {
		if err := yaml.Unmarshal(data, &template); err != nil {
			template = nil
		}
	}
	return modlocale.Derive(module, template)
}

// validateLocaleIDs reports every derived translation id that the module id
// rule rejects, because translators could not target such an id. It runs
// whenever module.yaml or template.yaml is present, independent of locales/.
func validateLocaleIDs(files map[string][]byte) []Finding {
	_, hasModule := files["module.yaml"]
	_, hasTemplate := files["template.yaml"]
	if !hasModule && !hasTemplate {
		return nil
	}
	var findings []Finding
	for _, ref := range deriveLocaleRefs(files) {
		if modlocale.ValidID(ref.ID) {
			continue
		}
		file := "template.yaml"
		if strings.HasPrefix(ref.ID, "module.") {
			file = "module.yaml"
		}
		findings = append(findings, Finding{
			Level:       SeverityError,
			RuleID:      RuleLocaleIDInvalid,
			File:        file,
			Line:        0,
			Field:       ref.ID,
			Message:     fmt.Sprintf("%q cannot be used as a translation id: names may only use letters, digits, '_', '-' and '.'", ref.ID),
			Remediation: "Rename the key or option value so it contains no spaces, '/', ':' or other symbols; translators cannot target it otherwise.",
		})
	}
	return findings
}

func localeFinding(file, field, message, remediation string) Finding {
	return Finding{
		Level:       SeverityError,
		RuleID:      RuleLocaleInvalid,
		File:        file,
		Line:        0,
		Field:       field,
		Message:     message,
		Remediation: remediation,
	}
}
