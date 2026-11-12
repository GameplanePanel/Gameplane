package main

import (
	"flag"
	"log/slog"
	"net/netip"
	"testing"
)

// TestManagementNamespaces pins the standalone lookup namespace used in existing
// credential records when an installation has no explicit credentials namespace.
func TestManagementNamespaces(t *testing.T) {
	t.Setenv("GAMEPLANE_NAMESPACE", "panel")
	for _, tc := range []struct {
		name, credentials, wantCredentials, wantConfigMaps string
		args                                               []string
	}{
		{name: "existing environment", wantCredentials: "panel", wantConfigMaps: "panel"},
		{name: "existing standalone store", args: []string{"--standalone", "--namespace=legacy-panel"}, wantCredentials: "legacy-panel", wantConfigMaps: "legacy-panel"},
		{name: "standalone remote operator", args: []string{"--standalone", "--namespace=remote-modules"}, credentials: "panel-records", wantCredentials: "panel-records", wantConfigMaps: "remote-modules"},
		{name: "isolated native credentials and uploads", args: []string{"--namespace=release"}, credentials: "isolated", wantCredentials: "isolated", wantConfigMaps: "isolated"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("GAMEPLANE_CREDENTIALS_NAMESPACE", tc.credentials)
			var cfg config
			fs := flag.NewFlagSet("serve", flag.ContinueOnError)
			cfg.bindFlags(fs)
			if err := fs.Parse(tc.args); err != nil {
				t.Fatal(err)
			}
			credentials, configMaps := cfg.managementNamespaces()
			if credentials != tc.wantCredentials || configMaps != tc.wantConfigMaps {
				t.Errorf("namespaces = %q, %q; want %q, %q", credentials, configMaps, tc.wantCredentials, tc.wantConfigMaps)
			}
		})
	}
}

func TestParseLogLevel(t *testing.T) {
	cases := map[string]slog.Level{
		"debug": slog.LevelDebug,
		"info":  slog.LevelInfo,
		"WARN":  slog.LevelWarn,
		"error": slog.LevelError,
		// Unknown values degrade to info rather than refusing to start.
		"":        slog.LevelInfo,
		"verbose": slog.LevelInfo,
	}
	for in, want := range cases {
		if got := parseLogLevel(in); got != want {
			t.Errorf("parseLogLevel(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestParseRoleMapping(t *testing.T) {
	cases := []struct {
		name    string
		csv     string
		wantErr bool
		want    []string
	}{
		{
			name:    "valid single group",
			csv:     "admin",
			wantErr: false,
			want:    []string{"admin"},
		},
		{
			name:    "valid multi-group",
			csv:     "group1,group2,group3",
			wantErr: false,
			want:    []string{"group1", "group2", "group3"},
		},
		{
			name:    "valid multi-group with surrounding whitespace",
			csv:     "  group1  ,  group2  , group3  ",
			wantErr: false,
			want:    []string{"group1", "group2", "group3"},
		},
		{
			name:    "empty/unset value accepted",
			csv:     "",
			wantErr: false,
			want:    nil,
		},
		{
			name:    "leading comma",
			csv:     ",group1",
			wantErr: true,
		},
		{
			name:    "trailing comma",
			csv:     "group1,",
			wantErr: true,
		},
		{
			name:    "doubled comma",
			csv:     "group1,,group2",
			wantErr: true,
		},
		{
			name:    "whitespace-only entry",
			csv:     "group1,  ,group2",
			wantErr: true,
		},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseRoleMapping(tt.csv)
			if (err != nil) != tt.wantErr {
				t.Errorf("parseRoleMapping(%q) error = %v, wantErr %v", tt.csv, err, tt.wantErr)
				return
			}
			if err != nil {
				return
			}
			if len(got) != len(tt.want) {
				t.Errorf("parseRoleMapping(%q) = %v, want %v", tt.csv, got, tt.want)
				return
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("parseRoleMapping(%q)[%d] = %q, want %q", tt.csv, i, got[i], tt.want[i])
					return
				}
			}
		})
	}
}

func TestParseTrustedProxyPrefix(t *testing.T) {
	cases := []struct {
		name    string
		cidr    string
		wantErr bool
		want    string
	}{
		{
			name:    "plain IPv4 prefix unchanged",
			cidr:    "10.42.0.0/16",
			wantErr: false,
			want:    "10.42.0.0/16",
		},
		{
			name:    "plain IPv6 prefix unchanged",
			cidr:    "fc00::/7",
			wantErr: false,
			want:    "fc00::/7",
		},
		{
			name:    "IPv4-mapped prefix with /112 normalizes to /16",
			cidr:    "::ffff:10.42.0.0/112",
			wantErr: false,
			want:    "10.42.0.0/16",
		},
		{
			name:    "IPv4-mapped prefix with /96 normalizes to /0",
			cidr:    "::ffff:0.0.0.0/96",
			wantErr: false,
			want:    "0.0.0.0/0",
		},
		{
			name:    "IPv4-mapped prefix with /126 normalizes to /30",
			cidr:    "::ffff:192.168.1.0/126",
			wantErr: false,
			want:    "192.168.1.0/30",
		},
		{
			name:    "IPv4-mapped prefix shorter than /96 errors",
			cidr:    "::ffff:0:0/95",
			wantErr: true,
		},
		{
			name:    "IPv4-mapped prefix at /64 errors",
			cidr:    "::ffff:0:0/64",
			wantErr: true,
		},
		{
			name:    "malformed CIDR errors",
			cidr:    "not-a-cidr",
			wantErr: true,
		},
		{
			name:    "IPv6 loopback unchanged",
			cidr:    "::1/128",
			wantErr: false,
			want:    "::1/128",
		},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseTrustedProxyPrefix(tt.cidr)
			if (err != nil) != tt.wantErr {
				t.Errorf("parseTrustedProxyPrefix(%q) error = %v, wantErr %v", tt.cidr, err, tt.wantErr)
				return
			}
			if err != nil {
				return
			}
			want := netip.MustParsePrefix(tt.want)
			if got != want {
				t.Errorf("parseTrustedProxyPrefix(%q) = %v, want %v", tt.cidr, got, want)
			}
		})
	}

	// Verify that normalized IPv4-mapped prefix matches expected address.
	normalizedPrefix, _ := parseTrustedProxyPrefix("::ffff:10.42.0.0/112")
	testAddr := netip.MustParseAddr("10.42.1.2")
	if !normalizedPrefix.Contains(testAddr) {
		t.Errorf("normalized prefix %v does not contain %v", normalizedPrefix, testAddr)
	}
}
