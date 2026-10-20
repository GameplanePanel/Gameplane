package notify

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestTitle(t *testing.T) {
	cases := []struct {
		name string
		e    Event
		want string
	}{
		{"instance fallback", Event{Type: EventBackupFailed, Namespace: "games", Name: "nightly"},
			"[Gameplane] backup failed: games/nightly"},
		{"named instance", Event{Type: EventServerUnhealthy, Instance: "prod", Namespace: "games", Name: "mc"},
			"[prod] server unhealthy: games/mc"},
		{"remote cluster", Event{Type: EventServerUnhealthy, Instance: "prod", Cluster: "remote-1", Namespace: "games", Name: "mc"},
			"[prod] server unhealthy: remote-1/games/mc"},
		{"test event", Event{Type: EventTest, Test: true},
			"[Gameplane] test notification"},
	}
	for _, tc := range cases {
		if got := title(tc.e); got != tc.want {
			t.Errorf("%s: title = %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestDetail(t *testing.T) {
	cases := []struct {
		name string
		e    Event
		want string
	}{
		{"reason and message", Event{Reason: "CrashLoop", Message: "restarted 3 times"}, "CrashLoop — restarted 3 times"},
		{"message only", Event{Message: "restic exit 1"}, "restic exit 1"},
		{"reason only", Event{Reason: "AgentStale"}, "AgentStale"},
		{"empty", Event{}, ""},
	}
	for _, tc := range cases {
		if got := detail(tc.e); got != tc.want {
			t.Errorf("%s: detail = %q, want %q", tc.name, got, tc.want)
		}
	}
	if d := detail(Event{Test: true}); !strings.Contains(d, "test notification") {
		t.Errorf("test detail = %q, want test copy", d)
	}
}

func TestFormatDiscord(t *testing.T) {
	cases := []struct {
		name      string
		e         Event
		wantColor int
	}{
		{"failure is red", Event{Type: EventBackupFailed, TS: "2026-07-03T10:00:00Z", Namespace: "games", Name: "nightly", Message: "boom"}, colorRed},
		{"recovery is green", Event{Type: EventServerRecovered, Namespace: "games", Name: "mc"}, colorGreen},
		{"test is neutral", Event{Type: EventTest, Test: true}, colorNeutral},
	}
	for _, tc := range cases {
		raw, err := formatDiscord(tc.e)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		var p struct {
			Embeds []struct {
				Title       string `json:"title"`
				Description string `json:"description"`
				Color       int    `json:"color"`
				Timestamp   string `json:"timestamp"`
			} `json:"embeds"`
		}
		if err := json.Unmarshal(raw, &p); err != nil {
			t.Fatalf("%s: unmarshal: %v", tc.name, err)
		}
		if len(p.Embeds) != 1 {
			t.Fatalf("%s: embeds = %d, want 1", tc.name, len(p.Embeds))
		}
		if p.Embeds[0].Color != tc.wantColor {
			t.Errorf("%s: color = %d, want %d", tc.name, p.Embeds[0].Color, tc.wantColor)
		}
		if p.Embeds[0].Title != title(tc.e) {
			t.Errorf("%s: title = %q", tc.name, p.Embeds[0].Title)
		}
		if p.Embeds[0].Timestamp != tc.e.TS {
			t.Errorf("%s: timestamp = %q, want %q", tc.name, p.Embeds[0].Timestamp, tc.e.TS)
		}
	}
}

func TestFormatSlack(t *testing.T) {
	raw, err := formatSlack(Event{Type: EventRestoreFailed, Namespace: "games", Name: "mc", Reason: "PVCMissing"})
	if err != nil {
		t.Fatal(err)
	}
	var p struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{":red_circle:", "restore failed", "games/mc", "PVCMissing"} {
		if !strings.Contains(p.Text, want) {
			t.Errorf("slack text %q missing %q", p.Text, want)
		}
	}
	raw, err = formatSlack(Event{Type: EventBackupSucceeded, Namespace: "games", Name: "mc"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), ":large_green_circle:") {
		t.Errorf("success slack payload %s missing green emoji", raw)
	}
}

func TestFormatWebhookRoundTrip(t *testing.T) {
	e := Event{
		Type: EventServerUnhealthy, TS: "2026-07-03T10:00:00Z", Kind: "GameServer",
		Name: "mc", Namespace: "games", Reason: "CrashLoop", Message: "boom", Instance: "prod",
	}
	raw, err := formatWebhook(e)
	if err != nil {
		t.Fatal(err)
	}
	var got Event
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if got != e {
		t.Errorf("webhook round-trip: got %+v, want %+v", got, e)
	}
}

func TestFormatEmail(t *testing.T) {
	subject, body := formatEmail(Event{
		Type: EventBackupFailed, TS: "2026-07-03T10:00:00Z", Kind: "Backup",
		Namespace: "games", Name: "nightly", Reason: "ResticError", Message: "exit 1",
		Instance: "evil\r\nBcc: attacker@example.com",
	})
	if strings.ContainsAny(subject, "\r\n") {
		t.Errorf("subject not header-sanitized: %q", subject)
	}
	for _, want := range []string{"backup.failed", "Backup games/nightly", "ResticError", "exit 1", "2026-07-03T10:00:00Z"} {
		if !strings.Contains(body, want) {
			t.Errorf("email body missing %q:\n%s", want, body)
		}
	}
	_, testBody := formatEmail(Event{Type: EventTest, Test: true, TS: "2026-07-03T10:00:00Z"})
	if !strings.Contains(testBody, "test notification") {
		t.Errorf("test email body missing test copy:\n%s", testBody)
	}
}
