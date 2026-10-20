package notify

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"golang.org/x/text/language"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kubefake "k8s.io/client-go/kubernetes/fake"

	"github.com/GameplanePanel/gameplane/api/internal/kube"
	"github.com/GameplanePanel/gameplane/api/internal/messages"
	"github.com/GameplanePanel/gameplane/i18n/catalog"
	"github.com/GameplanePanel/gameplane/i18n/icu"
)

// listedOnly points isListed at a fixed set of tags for one test.
func listedOnly(t *testing.T, tags ...string) {
	t.Helper()
	prev := isListed
	isListed = func(tag string) bool {
		for _, want := range tags {
			if tag == want {
				return true
			}
		}
		return false
	}
	t.Cleanup(func() { isListed = prev })
}

func TestLoadSinks_Language(t *testing.T) {
	store := newTestStore(t)
	n := &Notifier{store: store}
	putConfig(t, store, "notifications",
		`{"sinks":[{"name":"a","kind":"webhook","enabled":true,"configRef":"hook","language":"fr"},{"name":"b","kind":"webhook","enabled":true}]}`)
	sinks, err := n.loadSinks(context.Background())
	if err != nil {
		t.Fatalf("loadSinks: %v", err)
	}
	if len(sinks) != 2 || sinks[0].Language != "fr" || sinks[1].Language != "" {
		t.Fatalf("sinks = %+v; want language fr then empty", sinks)
	}
}

func TestEnglishOutputUnchanged(t *testing.T) {
	events := []Event{
		{Type: EventBackupFailed, TS: "t", Kind: "Backup", Name: "nightly", Namespace: "gameplane-games", Reason: "BackupFailed", Message: "restic exited with code 1", Instance: "prod"},
		{Type: EventServerUnhealthy, TS: "t", Kind: "GameServer", Name: "mc", Namespace: "gameplane-games", Instance: "prod"},
		{Type: EventBackupSucceeded, TS: "t", Instance: "prod"},
		{Type: EventTest, TS: "t", Instance: "prod", Test: true},
	}
	for _, base := range events {
		en := base
		en.lang = "en"
		if title(base) != title(en) || detail(base) != detail(en) || text(base) != text(en) {
			t.Fatalf("%s: empty and en renderings differ", base.Type)
		}
		a, _ := formatSlack(base)
		b, _ := formatSlack(en)
		if string(a) != string(b) {
			t.Fatalf("%s: slack differs: %s vs %s", base.Type, a, b)
		}
		subjA, bodyA := formatEmail(base)
		subjB, bodyB := formatEmail(en)
		if subjA != subjB || bodyA != bodyB {
			t.Fatalf("%s: email differs", base.Type)
		}
	}

	failed := events[0]
	if got := title(failed); got != "[prod] backup failed: gameplane-games/nightly" {
		t.Errorf("title = %q", got)
	}
	if got := detail(failed); got != "BackupFailed — restic exited with code 1" {
		t.Errorf("detail = %q", got)
	}
	test := events[3]
	if got := title(test); got != "[prod] test notification" {
		t.Errorf("test title = %q", got)
	}
	if got := detail(test); got != "This is a test notification from Gameplane. Your sink is wired up correctly." {
		t.Errorf("test detail = %q", got)
	}
	if got := title(Event{Type: EventBackupFailed, Instance: "prod", Test: false}); got != "[prod] backup failed" {
		t.Errorf("unnamed title = %q", got)
	}
}

func TestRenderUsesSinkLanguage(t *testing.T) {
	prev := lookupMessage
	lookupMessage = func(tag language.Tag, id string, args map[string]any) (string, bool) {
		if base, _ := tag.Base(); base.String() == "fr" {
			return "FR:" + id, true
		}
		return catalog.Lookup(tag, id, args)
	}
	t.Cleanup(func() { lookupMessage = prev })

	fr := Event{Type: EventBackupFailed, Name: "nightly", Namespace: "ns", Instance: "prod", lang: "fr"}
	if got := title(fr); got != "FR:notify.title.named" {
		t.Errorf("fr title = %q", got)
	}

	en := Event{Type: EventBackupFailed, Name: "nightly", Namespace: "ns", Instance: "prod", lang: "en", Reason: "R<x>", Message: "M {y} 'z'"}
	if got := title(en); got != "[prod] backup failed: ns/nightly" {
		t.Errorf("en title = %q", got)
	}
	if got := detail(en); got != "R<x> — M {y} 'z'" {
		t.Errorf("en detail = %q; Kubernetes text must pass through verbatim", got)
	}
}

func TestResolveLanguage(t *testing.T) {
	listedOnly(t, "en", "fr")
	cases := []struct{ sink, def, want string }{
		{"fr", "de", "fr"},
		{"zz", "fr", "fr"},
		{"", "", "en"},
		{"zz", "zz", "en"},
		{"", "fr", "fr"},
	}
	for _, c := range cases {
		if got := resolveLanguage(c.sink, c.def); got != c.want {
			t.Errorf("resolveLanguage(%q, %q) = %q; want %q", c.sink, c.def, got, c.want)
		}
	}
}

func TestFormatWebhookAddsLanguageTitleText(t *testing.T) {
	e := Event{Type: EventBackupFailed, TS: "t", Name: "nightly", Namespace: "ns", Reason: "R", Message: "M", Instance: "prod", lang: "fr"}
	raw, err := formatWebhook(e)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{
		"type": "backup.failed", "ts": "t", "name": "nightly", "namespace": "ns",
		"reason": "R", "message": "M", "instance": "prod",
		"language": "fr", "title": title(e), "text": text(e),
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %v; want %v", k, got[k], v)
		}
	}

	raw, _ = formatWebhook(Event{Type: EventTest, TS: "t", Test: true})
	got = map[string]any{}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if got["language"] != "en" {
		t.Errorf("empty lang: language = %v; want en", got["language"])
	}
}

func TestMessagesHaveDescriptionsAndParse(t *testing.T) {
	seen := map[messages.ID]bool{}
	for _, m := range Messages() {
		if m.Description == "" {
			t.Errorf("%s: empty description", m.ID)
		}
		if !strings.HasPrefix(string(m.ID), "notify.") {
			t.Errorf("%s: id lacks notify. prefix", m.ID)
		}
		if seen[m.ID] {
			t.Errorf("%s: duplicate id", m.ID)
		}
		seen[m.ID] = true
		if _, err := icu.Parse(m.English); err != nil {
			t.Errorf("%s: parse: %v", m.ID, err)
		}
	}
}

func TestFanOutRendersInSinkOrDefaultLanguage(t *testing.T) {
	listedOnly(t, "en", "fr")
	store := newTestStore(t)

	var mu sync.Mutex
	bodies := map[string]map[string]any{}
	record := func(name string) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			data, _ := io.ReadAll(r.Body)
			var doc map[string]any
			_ = json.Unmarshal(data, &doc)
			mu.Lock()
			bodies[name] = doc
			mu.Unlock()
			w.WriteHeader(http.StatusNoContent)
		}))
	}
	srvA := record("a")
	defer srvA.Close()
	srvB := record("b")
	defer srvB.Close()

	secret := func(name, url string) *corev1.Secret {
		return &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name: name, Namespace: "gameplane-system",
				Labels: map[string]string{SinkSecretLabel: "true"},
			},
			Data: map[string][]byte{"url": []byte(url)},
		}
	}
	n := &Notifier{
		store:     store,
		k:         &kube.Client{Typed: kubefake.NewClientset(secret("hook-a", srvA.URL), secret("hook-b", srvB.URL))},
		controlNS: "gameplane-system",
		client:    testNotifier().client,
		ch:        make(chan Event, 1),
	}
	fastRetries(t)

	putConfig(t, store, "i18n", `{"defaultLanguage":"fr"}`)
	putConfig(t, store, "notifications",
		`{"sinks":[`+
			`{"name":"a","kind":"webhook","enabled":true,"configRef":"hook-a","events":["backup.failed"],"language":"fr"},`+
			`{"name":"b","kind":"webhook","enabled":true,"configRef":"hook-b","events":["backup.failed"]}]}`)
	n.fanOut(context.Background(), Event{Type: EventBackupFailed, TS: "t", Name: "nightly", Namespace: "ns"})
	mu.Lock()
	if bodies["a"]["language"] != "fr" {
		t.Errorf("sink a language = %v; want fr", bodies["a"]["language"])
	}
	if bodies["b"]["language"] != "fr" {
		t.Errorf("sink b (instance default) language = %v; want fr", bodies["b"]["language"])
	}
	mu.Unlock()

	putConfig(t, store, "i18n", `{}`)
	bodies = map[string]map[string]any{}
	n.fanOut(context.Background(), Event{Type: EventBackupFailed, TS: "t", Name: "nightly", Namespace: "ns"})
	mu.Lock()
	defer mu.Unlock()
	if bodies["b"]["language"] != "en" {
		t.Errorf("sink b with no default language = %v; want en", bodies["b"]["language"])
	}
	if bodies["a"]["language"] != "fr" {
		t.Errorf("sink a language = %v; want fr", bodies["a"]["language"])
	}
}
