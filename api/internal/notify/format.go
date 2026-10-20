package notify

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Discord embed colors: red for failures, green for recoveries/successes,
// blurple for test sends.
const (
	colorRed     = 0xED4245
	colorGreen   = 0x57F287
	colorNeutral = 0x5865F2
)

// isFailure reports whether t is bad news; formatters use it to pick the
// red styling.
func isFailure(t EventType) bool {
	switch t {
	case EventServerUnhealthy, EventBackupFailed, EventRestoreFailed:
		return true
	}
	return false
}

// title builds the one-line headline every sink kind shares:
// "[prod] backup failed: game-servers/nightly". The instance name falls
// back to "Gameplane" so multi-install admins can always tell senders apart.
func title(e Event) string {
	inst := e.Instance
	if inst == "" {
		inst = "Gameplane"
	}
	what := eventName(e)
	if e.Name == "" {
		return render(e.lang, "notify.title.unnamed", map[string]any{"instance": inst, "event": what})
	}
	if e.Cluster != "" {
		return render(e.lang, "notify.title.cluster", map[string]any{
			"instance":  inst,
			"event":     what,
			"cluster":   e.Cluster,
			"namespace": e.Namespace,
			"name":      e.Name,
		})
	}
	return render(e.lang, "notify.title.named", map[string]any{
		"instance":  inst,
		"event":     what,
		"namespace": e.Namespace,
		"name":      e.Name,
	})
}

// eventName returns the translated event name used inside the headline. An
// event type without a notify.event.* entry keeps the raw dotted name spelled
// with spaces, as before the catalog existed.
func eventName(e Event) string {
	if e.Test {
		return render(e.lang, "notify.event.test", nil)
	}
	id := "notify.event." + string(e.Type)
	if !hasMessage(id) {
		return strings.ReplaceAll(string(e.Type), ".", " ")
	}
	return render(e.lang, id, nil)
}

// hasMessage reports whether id is one of the notify.* catalog entries.
func hasMessage(id string) bool {
	for _, m := range Messages() {
		if string(m.ID) == id {
			return true
		}
	}
	return false
}

// detail composes the reason/message pair into one line; test events get
// fixed copy so a test send never renders an empty body. Kubernetes reason
// and message text is quoted verbatim.
func detail(e Event) string {
	switch {
	case e.Reason != "" && e.Message != "":
		return render(e.lang, "notify.detail.reason_message", map[string]any{"reason": e.Reason, "message": e.Message})
	case e.Message != "":
		return e.Message
	case e.Reason != "":
		return e.Reason
	case e.Test:
		return render(e.lang, "notify.detail.test", nil)
	}
	return ""
}

// text is the one-line sentence Slack shows, without the emoji: the headline,
// then the detail after an em dash when there is one.
func text(e Event) string {
	if d := detail(e); d != "" {
		return title(e) + " — " + d
	}
	return title(e)
}

// formatDiscord renders e as a Discord webhook execute payload: one embed,
// color-coded by outcome.
func formatDiscord(e Event) ([]byte, error) {
	color := colorGreen
	switch {
	case e.Test:
		color = colorNeutral
	case isFailure(e.Type):
		color = colorRed
	}
	type embed struct {
		Title       string `json:"title"`
		Description string `json:"description,omitempty"`
		Color       int    `json:"color"`
		Timestamp   string `json:"timestamp,omitempty"`
	}
	return json.Marshal(struct {
		Embeds []embed `json:"embeds"`
	}{Embeds: []embed{{
		Title:       title(e),
		Description: detail(e),
		Color:       color,
		Timestamp:   e.TS,
	}}})
}

// formatSlack renders e as a plain-text incoming-webhook payload — the one
// shape every Slack webhook variant (and most compatibles) accepts.
func formatSlack(e Event) ([]byte, error) {
	emoji := ":large_green_circle:"
	switch {
	case e.Test:
		emoji = ":white_check_mark:"
	case isFailure(e.Type):
		emoji = ":red_circle:"
	}
	text := emoji + " " + title(e)
	if d := detail(e); d != "" {
		text += " — " + d
	}
	return json.Marshal(struct {
		Text string `json:"text"`
	}{Text: text})
}

// formatWebhook ships the full structured event so generic consumers can
// route on any field.
func formatWebhook(e Event) ([]byte, error) {
	lang := e.lang
	if lang == "" {
		lang = "en"
	}
	// Embedding Event flattens its fields, so every existing key keeps its
	// name, order and value; the three additive fields come last.
	return json.Marshal(webhookPayload{
		Event:    e,
		Language: lang,
		Title:    title(e),
		Text:     text(e),
	})
}

// webhookPayload is the generic webhook body: the Event fields plus the
// language the text was rendered in and the rendered headline and sentence.
type webhookPayload struct {
	Event
	Language string `json:"language"`
	Title    string `json:"title"`
	Text     string `json:"text"`
}

// formatNtfy renders e for an ntfy topic POST: the detail is the
// plain-text body and the metadata rides in ntfy's Title/Priority/Tags
// headers. Header values are sanitized — the instance name inside the
// title is free-text admin config and must not inject headers.
func formatNtfy(e Event) ([]byte, map[string]string) {
	headers := map[string]string{
		"Content-Type": "text/plain; charset=utf-8",
		"Title":        sanitizeHeader(title(e)),
	}
	switch {
	case e.Test:
		headers["Priority"] = "default"
		headers["Tags"] = "white_check_mark"
	case isFailure(e.Type):
		headers["Priority"] = "high"
		headers["Tags"] = "rotating_light"
	default:
		headers["Priority"] = "default"
		headers["Tags"] = "green_circle"
	}
	return []byte(detail(e)), headers
}

// formatEmail renders the subject and plain-text body for SMTP sinks. The
// subject is header-sanitized: instance names are free-text admin config
// and must not be able to inject additional headers.
func formatEmail(e Event) (subject, body string) {
	subject = sanitizeHeader(title(e))
	var b strings.Builder
	fmt.Fprintf(&b, "Type:      %s\r\n", e.Type)
	if e.Name != "" {
		fmt.Fprintf(&b, "Resource:  %s %s/%s\r\n", e.Kind, e.Namespace, e.Name)
	}
	if e.Reason != "" {
		fmt.Fprintf(&b, "Reason:    %s\r\n", e.Reason)
	}
	if e.Message != "" {
		fmt.Fprintf(&b, "Message:   %s\r\n", e.Message)
	}
	fmt.Fprintf(&b, "Time:      %s\r\n", e.TS)
	if e.Instance != "" {
		fmt.Fprintf(&b, "Instance:  %s\r\n", e.Instance)
	}
	if e.Test {
		b.WriteString("\r\n" + render(e.lang, "notify.detail.test", nil) + "\r\n")
	}
	return subject, b.String()
}

// sanitizeHeader strips CR/LF so a value interpolated into a mail header
// can't smuggle extra headers in.
func sanitizeHeader(s string) string {
	s = strings.ReplaceAll(s, "\r", " ")
	return strings.ReplaceAll(s, "\n", " ")
}
