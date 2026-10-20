package handlers

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"

	"github.com/go-chi/chi/v5"

	"github.com/GameplanePanel/gameplane/api/internal/auth"
	"github.com/GameplanePanel/gameplane/api/internal/httperr"
	"github.com/GameplanePanel/gameplane/api/internal/kube"
	"github.com/GameplanePanel/gameplane/api/internal/messages"
	"github.com/GameplanePanel/gameplane/api/internal/notify"
)

// sinkSecretPrefix derives the managed Secret's name from the sink name.
// Kubernetes Secret names allow 253 chars, but the sink's configRef is
// validated as a DNS label (≤63), so the sink name itself is capped in
// putSinkSecret to keep the derived name referenceable.
const sinkSecretPrefix = "gameplane-notify-"

// MountNotifications exposes notification-sink operations under
// /admin/notifications. Sink CRUD stays in the config store
// (PUT /admin/config/notifications); this surface adds the test-send —
// which dials an external endpoint synchronously and so carries its own
// tight rate limit on top of the global mutation cap — and the managed
// credential Secret the Add-sink form writes values into.
func MountNotifications(r chi.Router, n *notify.Notifier, k *kube.Client, controlNS string) {
	h := notificationsHandler{k: k, ns: controlNS}
	r.With(auth.NotifyTestLimiter.Middleware).
		Post("/admin/notifications/sinks/{name}/test", func(w http.ResponseWriter, req *http.Request) {
			err := n.DeliverTest(req.Context(), chi.URLParam(req, "name"))
			switch {
			case err == nil:
				writeJSON(w, map[string]any{"delivered": true})
			case errors.Is(err, notify.ErrUnknownSink):
				httperr.WriteCode(w, req, http.StatusNotFound, err)
			case errors.Is(err, notify.ErrSinkNotConfigured):
				httperr.WriteCode(w, req, http.StatusUnprocessableEntity, err)
			default:
				// Sink exists and is configured but delivery failed — relay
				// the (URL-sanitized) cause so the admin can fix the endpoint
				// or the Secret.
				http.Error(w, err.Error(), http.StatusBadGateway)
			}
		})
	r.Put("/admin/notifications/sinks/{name}/secret", h.putSecret)
	r.Delete("/admin/notifications/sinks/{name}/secret", h.deleteSecret)
}

type notificationsHandler struct {
	k  *kube.Client
	ns string
}

// sinkSecretBody carries the credential material for one sink, keyed by
// kind. Only the fields for the given kind are honored; the full key set
// for that kind is always written (empty for unset optionals) so a
// re-save fully replaces the previous values.
type sinkSecretBody struct {
	Kind string `json:"kind"`

	// discord | slack | webhook | ntfy
	URL string `json:"url,omitempty"`
	// discord | slack | webhook: verbatim Authorization header value.
	Authorization string `json:"authorization,omitempty"`
	// ntfy: access token, stored as "Authorization: Bearer <token>".
	Token string `json:"token,omitempty"`

	// smtp
	Host     string `json:"host,omitempty"`
	Port     string `json:"port,omitempty"`
	Username string `json:"username,omitempty"`
	Password string `json:"password,omitempty"`
	From     string `json:"from,omitempty"`
	To       string `json:"to,omitempty"`
	TLS      string `json:"tls,omitempty"` // starttls (default) | implicit | none
}

// sinkSecretData validates b for its kind and returns the exact Secret
// key set the notify package's delivery paths read.
func sinkSecretData(b sinkSecretBody) (map[string]string, error) {
	requireURL := func() error {
		u, err := url.Parse(b.URL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return errors.New("url must be an http(s) URL")
		}
		return nil
	}
	switch b.Kind {
	case "discord", "slack", "webhook":
		if err := requireURL(); err != nil {
			return nil, err
		}
		return map[string]string{"url": b.URL, "authorization": b.Authorization}, nil
	case "ntfy":
		if err := requireURL(); err != nil {
			return nil, err
		}
		auth := ""
		if b.Token != "" {
			auth = "Bearer " + b.Token
		}
		return map[string]string{"url": b.URL, "authorization": auth}, nil
	case "smtp":
		if b.Host == "" || b.From == "" || b.To == "" {
			return nil, errors.New("host, from and to are required for smtp")
		}
		if b.TLS != "" && b.TLS != "starttls" && b.TLS != "implicit" && b.TLS != "none" {
			return nil, errors.New("tls must be starttls, implicit or none")
		}
		return map[string]string{
			"host": b.Host, "port": b.Port,
			"username": b.Username, "password": b.Password,
			"from": b.From, "to": b.To, "tls": b.TLS,
		}, nil
	default:
		return nil, fmt.Errorf("unknown sink kind %q", b.Kind)
	}
}

func (h notificationsHandler) putSecret(w http.ResponseWriter, req *http.Request) {
	name := chi.URLParam(req, "name")
	if !dnsLabelRE.MatchString(name) {
		httperr.WriteMessage(w, req, messages.New("server.notifications.sink_name_not_dns_label", nil))
		return
	}
	secretName := sinkSecretPrefix + name
	if len(secretName) > 63 {
		httperr.WriteCode(w, req, http.StatusUnprocessableEntity, fmt.Errorf("sink name too long: %q must fit a DNS label with the %q prefix", name, sinkSecretPrefix))
		return
	}
	var body sinkSecretBody
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		httperr.WriteMessage(w, req, messages.New("server.common.invalid_json", nil))
		return
	}
	data, err := sinkSecretData(body)
	if err != nil {
		httperr.WriteCode(w, req, http.StatusUnprocessableEntity, err)
		return
	}
	if err := upsertLabelledSecret(req.Context(), h.k, h.ns, secretName, notify.SinkSecretLabel, data); err != nil {
		if errors.Is(err, errNotManagedSecret) {
			httperr.WriteCode(w, req, http.StatusConflict, err)
			return
		}
		httperr.Write(w, req, err)
		return
	}
	keys := make([]string, 0, len(data))
	for k := range data {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	// Never echo values — the response only confirms which keys landed
	// and the configRef the sink row should carry.
	writeJSON(w, map[string]any{"name": secretName, "keys": keys})
}

func (h notificationsHandler) deleteSecret(w http.ResponseWriter, req *http.Request) {
	name := chi.URLParam(req, "name")
	if !dnsLabelRE.MatchString(name) {
		httperr.WriteMessage(w, req, messages.New("server.notifications.sink_name_not_dns_label", nil))
		return
	}
	if err := deleteManagedSecret(req.Context(), h.k, h.ns, sinkSecretPrefix+name, notify.SinkSecretLabel); err != nil {
		httperr.Write(w, req, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
