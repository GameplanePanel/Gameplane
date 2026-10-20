package telemetry

import "testing"

func TestResolveDestination_Table(t *testing.T) {
	const (
		bundledURL = "http://gameplane-telemetry-receiver.gameplane-system.svc:8080/ingest"
		defaultURL = "https://telemetry.example.org/ingest"
	)
	cases := []struct {
		name       string
		disabled   bool
		endpoint   string
		bundled    bool
		defaultURL string
		wantKind   string
		wantURL    string
		wantHost   string
		wantErr    bool
	}{
		{name: "disabled wins over endpoint and default", disabled: true, endpoint: "https://x.example/i", defaultURL: defaultURL, wantKind: KindDisabled},
		{name: "disabled wins over bundled", disabled: true, endpoint: bundledURL, bundled: true, wantKind: KindDisabled},
		{name: "bundled needs an endpoint and the marker", endpoint: bundledURL, bundled: true, defaultURL: defaultURL,
			wantKind: KindBundled, wantURL: bundledURL, wantHost: "gameplane-telemetry-receiver.gameplane-system.svc:8080"},
		{name: "custom keeps any scheme", endpoint: "http://collector.lan:9000/in", defaultURL: defaultURL,
			wantKind: KindCustom, wantURL: "http://collector.lan:9000/in", wantHost: "collector.lan:9000"},
		{name: "custom endpoint is trimmed", endpoint: "  https://c.example/i \n", wantKind: KindCustom,
			wantURL: "https://c.example/i", wantHost: "c.example"},
		{name: "custom with an unparsable URL is not an error", endpoint: "://bad", wantKind: KindCustom, wantURL: "://bad"},
		{name: "default when nothing else is set", defaultURL: defaultURL,
			wantKind: KindDefault, wantURL: defaultURL, wantHost: "telemetry.example.org"},
		{name: "bundled marker alone does not use the default", bundled: true, defaultURL: defaultURL,
			wantKind: KindDefault, wantURL: defaultURL, wantHost: "telemetry.example.org"},
		{name: "none when there is no endpoint and no default", wantKind: KindNone},
		{name: "none when only the bundled marker is set", bundled: true, wantKind: KindNone},
		{name: "default over http is rejected", defaultURL: "http://telemetry.example.org/ingest", wantErr: true},
		{name: "default without a host is rejected", defaultURL: "https://", wantErr: true},
		{name: "default that does not parse is rejected", defaultURL: "%%", wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := resolveDestination(tc.disabled, tc.endpoint, tc.bundled, tc.defaultURL)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("want an error, got %+v", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			want := Destination{Kind: tc.wantKind, URL: tc.wantURL, Host: tc.wantHost}
			if got != want {
				t.Fatalf("got %+v, want %+v", got, want)
			}
		})
	}
}

func TestResolveDestination_UsesProjectConstant(t *testing.T) {
	got, err := ResolveDestination(true, "", false)
	if err != nil || got.Kind != KindDisabled || got.URL != "" {
		t.Fatalf("disabled: got %+v, %v", got, err)
	}
	got, err = ResolveDestination(false, "http://collector.lan/in", false)
	if err != nil || got.Kind != KindCustom {
		t.Fatalf("custom: got %+v, %v", got, err)
	}
	// Whatever DefaultEndpoint holds must itself resolve: empty means none,
	// anything else must be a valid https URL.
	if _, err := ResolveDestination(false, "", false); err != nil {
		t.Fatalf("DefaultEndpoint %q does not resolve: %v", DefaultEndpoint, err)
	}
}
