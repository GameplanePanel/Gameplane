package notify

import (
	"context"
	"encoding/json"
	"strings"

	"golang.org/x/text/language"

	"github.com/GameplanePanel/gameplane/i18n/catalog"
	"github.com/GameplanePanel/gameplane/i18n/icu"
)

var (
	isListed      = catalog.IsListed // swapped in tests
	lookupMessage = catalog.Lookup   // swapped in tests
)

// resolveLanguage picks the first of sinkLang, instanceDefault, "en" that the
// catalog lists. An empty candidate is skipped. "en" is always listed.
func resolveLanguage(sinkLang, instanceDefault string) string {
	for _, c := range []string{sinkLang, instanceDefault} {
		if c != "" && isListed(c) {
			return c
		}
	}
	return "en"
}

// render formats notify message id for lang (BCP 47; "" means "en"). It asks the
// catalog first and falls back to the English text from Messages() rendered with
// icu.Parse/icu.Format(language.English), so output never goes empty.
func render(lang, id string, args map[string]any) string {
	if lang == "" {
		lang = "en"
	}
	tag, err := language.Parse(lang)
	if err != nil {
		tag = language.English
	}
	if s, ok := lookupMessage(tag, id, args); ok {
		return s
	}
	for _, m := range Messages() {
		if string(m.ID) != id {
			continue
		}
		msg, err := icu.Parse(m.English)
		if err != nil {
			return m.English
		}
		return icu.Format(msg, language.English, args)
	}
	return id
}

// defaultLanguage reads the admin config section "i18n" ({"defaultLanguage": tag});
// "" on any miss or error.
func (n *Notifier) defaultLanguage(ctx context.Context) string {
	raw, ok, err := n.store.ConfigValue(ctx, "i18n")
	if err != nil || !ok {
		return ""
	}
	var c struct {
		DefaultLanguage string `json:"defaultLanguage"`
	}
	if json.Unmarshal([]byte(raw), &c) != nil {
		return ""
	}
	return strings.TrimSpace(c.DefaultLanguage)
}
