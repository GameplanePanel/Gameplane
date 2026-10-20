package handlers

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	frJSON = `{"template.displayName":{"defaultMessage":"Jeu"},"module.summary":{"defaultMessage":"Résumé","description":"d"}}`
	ptJSON = `{"template.displayName":{"defaultMessage":"Jogo"}}`
)

func localesFixture(t *testing.T, data map[string]string) http.Handler {
	t.Helper()
	k := fakeKubeClient(newModule("mc", map[string]any{"name": "minecraft"}))
	if data != nil {
		if _, err := k.Typed.CoreV1().ConfigMaps("gameplane-system").Create(t.Context(), &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{Name: "mc-locales", Namespace: "gameplane-system"},
			Data:       data,
		}, metav1.CreateOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	return mountModulesRouter(k)
}

func decodeLocales(t *testing.T, body []byte) map[string]string {
	t.Helper()
	var out map[string]string
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode %q: %v", body, err)
	}
	return out
}

func TestGetLocales_TagHit(t *testing.T) {
	h := localesFixture(t, map[string]string{"fr.json": frJSON, "pt.json": ptJSON})
	rr := do(t, h, "GET", "/modules/mc/locales/fr", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rr.Code, rr.Body)
	}
	got := decodeLocales(t, rr.Body.Bytes())
	if len(got) != 2 || got["template.displayName"] != "Jeu" || got["module.summary"] != "Résumé" {
		t.Errorf("body = %v", got)
	}
}

func TestGetLocales_FallsBackToBaseLanguage(t *testing.T) {
	h := localesFixture(t, map[string]string{"fr.json": frJSON, "pt.json": ptJSON})
	rr := do(t, h, "GET", "/modules/mc/locales/pt-BR", nil)
	if got := decodeLocales(t, rr.Body.Bytes()); got["template.displayName"] != "Jogo" {
		t.Errorf("body = %v", got)
	}
}

func TestGetLocales_RegionalPackWinsOverBase(t *testing.T) {
	h := localesFixture(t, map[string]string{
		"pt.json":    ptJSON,
		"pt-BR.json": `{"template.displayName":{"defaultMessage":"Jogo BR"}}`,
	})
	rr := do(t, h, "GET", "/modules/mc/locales/pt-BR", nil)
	if got := decodeLocales(t, rr.Body.Bytes()); got["template.displayName"] != "Jogo BR" {
		t.Errorf("body = %v", got)
	}
}

func TestGetLocales_UnknownAndInvalidTagsAreEmpty(t *testing.T) {
	h := localesFixture(t, map[string]string{"fr.json": frJSON})
	for _, tag := range []string{"de", "not a tag!", "x"} {
		rr := do(t, h, "GET", "/modules/mc/locales/"+url.PathEscape(tag), nil)
		if rr.Code != http.StatusOK {
			t.Errorf("tag %q: status = %d", tag, rr.Code)
			continue
		}
		if got := decodeLocales(t, rr.Body.Bytes()); len(got) != 0 {
			t.Errorf("tag %q: body = %v", tag, got)
		}
	}
}

func TestGetLocales_NoConfigMapIsEmpty(t *testing.T) {
	h := localesFixture(t, nil)
	rr := do(t, h, "GET", "/modules/mc/locales/fr", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}
	if got := decodeLocales(t, rr.Body.Bytes()); len(got) != 0 {
		t.Errorf("body = %v", got)
	}
}

func TestGetLocales_MissingModuleIs404(t *testing.T) {
	h := localesFixture(t, map[string]string{"fr.json": frJSON})
	if rr := do(t, h, "GET", "/modules/ghost/locales/fr", nil); rr.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rr.Code)
	}
}

func TestGetLocales_MalformedValueIsSkipped(t *testing.T) {
	h := localesFixture(t, map[string]string{"fr.json": `{not json`})
	rr := do(t, h, "GET", "/modules/mc/locales/fr", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}
	if got := decodeLocales(t, rr.Body.Bytes()); len(got) != 0 {
		t.Errorf("body = %v", got)
	}
}

func TestGetLocales_MalformedRegionalFallsThroughToBase(t *testing.T) {
	h := localesFixture(t, map[string]string{"pt-BR.json": `{bad`, "pt.json": ptJSON})
	rr := do(t, h, "GET", "/modules/mc/locales/pt-BR", nil)
	if got := decodeLocales(t, rr.Body.Bytes()); got["template.displayName"] != "Jogo" {
		t.Errorf("body = %v", got)
	}
}

func TestResolveLocale(t *testing.T) {
	data := map[string]string{"zh-Hans.json": `{"a":{"defaultMessage":"简"}}`, "zh.json": `{"a":{"defaultMessage":"中"}}`}
	cases := []struct {
		tag, want string
	}{
		{"zh-Hans", "简"},
		{"zh-Hans-CN", "中"},
		{"zh", "中"},
		{"zh-TW", "中"},
		{"en", ""},
		{"", ""},
	}
	for _, c := range cases {
		if got := resolveLocale(data, c.tag)["a"]; got != c.want {
			t.Errorf("resolveLocale(%q) = %q, want %q", c.tag, got, c.want)
		}
	}
	if got := resolveLocale(nil, "fr"); got == nil || len(got) != 0 {
		t.Errorf("nil data = %v", got)
	}
}

func TestGetLocales_MalformedTagsAreEmptyJSON(t *testing.T) {
	h := localesFixture(t, map[string]string{"fr.json": frJSON})
	for _, tag := range []string{"a%00b", strings.Repeat("a", 200), "en-XA-"} {
		rr := do(t, h, "GET", "/modules/mc/locales/"+tag, nil)
		if rr.Code != http.StatusOK {
			t.Errorf("tag %q: status = %d", tag, rr.Code)
			continue
		}
		if got := decodeLocales(t, rr.Body.Bytes()); len(got) != 0 {
			t.Errorf("tag %q: body = %v", tag, got)
		}
	}
}

func TestGetLocales_ReturnsICUStringsVerbatim(t *testing.T) {
	const icu = "{count, plural, one {# item} other {# items}}"
	h := localesFixture(t, map[string]string{"fr.json": `{"template.displayName":{"defaultMessage":"` + icu + `"}}`})
	rr := do(t, h, "GET", "/modules/mc/locales/fr", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}
	if ct := rr.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("Content-Type = %q", ct)
	}
	if got := decodeLocales(t, rr.Body.Bytes()); got["template.displayName"] != icu {
		t.Errorf("body = %v", got)
	}
}
