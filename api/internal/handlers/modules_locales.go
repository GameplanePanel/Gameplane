package handlers

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"golang.org/x/text/language"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/GameplanePanel/gameplane/api/internal/httperr"
	"github.com/GameplanePanel/gameplane/api/internal/kube"
	"github.com/GameplanePanel/gameplane/i18n/pack"
)

// localesConfigMapSuffix names the ConfigMap the operator writes a module's
// validated translations to: <module name> + this suffix, in the operator
// namespace. Keys are "<tag>.json" holding an app.json style entry map.
const localesConfigMapSuffix = "-locales"

// getLocales serves GET /modules/{name}/locales/{tag}: the module's
// translated messages for the language as a flat id -> message map. The tag
// falls back to its base language; an unknown or malformed tag, a module
// without translations, and a missing ConfigMap all yield an empty object.
// Only a missing Module is a 404.
func (h modulesHandler) getLocales(w http.ResponseWriter, req *http.Request) {
	name, tag := chi.URLParam(req, "name"), chi.URLParam(req, "tag")
	ctx := req.Context()
	if _, err := h.k.Dynamic.Resource(kube.GVRModule).Get(ctx, name, metav1.GetOptions{}); err != nil {
		httperr.Write(w, req, err)
		return
	}
	cm, err := h.k.Typed.CoreV1().ConfigMaps(h.namespace).Get(ctx, name+localesConfigMapSuffix, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		writeJSON(w, map[string]string{})
		return
	}
	if err != nil {
		httperr.Write(w, req, err)
		return
	}
	writeJSON(w, resolveLocale(cm.Data, tag))
}

// resolveLocale picks the translation for tag from the ConfigMap data (keys
// "<tag>.json"), trying the full tag and then its base language. It returns
// an empty map when nothing usable matches.
func resolveLocale(data map[string]string, tag string) map[string]string {
	out := map[string]string{}
	parsed, err := language.Parse(tag)
	if err != nil {
		return out
	}
	candidates := []string{parsed.String()}
	if base, _ := parsed.Base(); base.String() != candidates[0] && base.String() != "und" {
		candidates = append(candidates, base.String())
	}
	for _, c := range candidates {
		raw, ok := data[c+".json"]
		if !ok {
			continue
		}
		entries, err := pack.ParseEntries([]byte(raw))
		if err != nil {
			continue
		}
		for id, e := range entries {
			out[id] = e.DefaultMessage
		}
		return out
	}
	return out
}
