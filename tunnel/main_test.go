package main

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// -----------------------------------------------------------------------
// loadConfig tests
// -----------------------------------------------------------------------

func stubEnv(vars map[string]string) func(string) string {
	return func(key string) string { return vars[key] }
}

func TestLoadConfigFrp(t *testing.T) {
	cfg, err := loadConfig(stubEnv(map[string]string{
		"GAMESERVER_NAME":      "my-server",
		"GAMESERVER_NAMESPACE": "games",
		"TUNNEL_TYPE":          "frp",
		"FRP_SERVER_ADDR":      "frp.example.com",
		"FRP_SERVER_PORT":      "7000",
		"BACKING_SERVICE_DNS":  "my-server.games.svc",
		"BACKING_SERVICE_PORT": "game:25565",
	}))
	if err != nil {
		t.Fatalf("loadConfig() error = %v", err)
	}
	if cfg.GameServerName != "my-server" {
		t.Errorf("GameServerName = %q, want %q", cfg.GameServerName, "my-server")
	}
	if cfg.TunnelType != "frp" {
		t.Errorf("TunnelType = %q, want %q", cfg.TunnelType, "frp")
	}
	if cfg.FrpServerAddr != "frp.example.com" {
		t.Errorf("FrpServerAddr = %q, want %q", cfg.FrpServerAddr, "frp.example.com")
	}
	if cfg.FrpServerPort != 7000 {
		t.Errorf("FrpServerPort = %d, want %d", cfg.FrpServerPort, 7000)
	}
}

func TestLoadConfigFrpDefaultPort(t *testing.T) {
	cfg, err := loadConfig(stubEnv(map[string]string{
		"GAMESERVER_NAME":      "my-server",
		"GAMESERVER_NAMESPACE": "games",
		"TUNNEL_TYPE":          "frp",
		"FRP_SERVER_ADDR":      "frp.example.com",
		"BACKING_SERVICE_DNS":  "my-server.games.svc",
		"BACKING_SERVICE_PORT": "game:25565",
	}))
	if err != nil {
		t.Fatalf("loadConfig() error = %v", err)
	}
	if cfg.FrpServerPort != 7000 {
		t.Errorf("default FrpServerPort = %d, want %d", cfg.FrpServerPort, 7000)
	}
}

func TestLoadConfigTailscale(t *testing.T) {
	cfg, err := loadConfig(stubEnv(map[string]string{
		"GAMESERVER_NAME":       "my-server",
		"GAMESERVER_NAMESPACE":  "games",
		"TUNNEL_TYPE":           "tailscale",
		"TAILSCALE_HOSTNAME":    "my-game",
		"TAILSCALE_TAGS":        "tag:gameplane,tag:game",
		"BACKING_SERVICE_DNS":   "my-server.games.svc",
		"BACKING_SERVICE_PORTS": "game:25565:tcp",
	}))
	if err != nil {
		t.Fatalf("loadConfig() error = %v", err)
	}
	if cfg.TunnelType != "tailscale" {
		t.Errorf("TunnelType = %q, want %q", cfg.TunnelType, "tailscale")
	}
	if cfg.TailscaleHostname != "my-game" {
		t.Errorf("TailscaleHostname = %q, want %q", cfg.TailscaleHostname, "my-game")
	}
	if cfg.TailscaleTags != "tag:gameplane,tag:game" {
		t.Errorf("TailscaleTags = %q, want %q", cfg.TailscaleTags, "tag:gameplane,tag:game")
	}
}

func TestLoadConfigPlayit(t *testing.T) {
	cfg, err := loadConfig(stubEnv(map[string]string{
		"GAMESERVER_NAME":       "my-server",
		"GAMESERVER_NAMESPACE":  "games",
		"TUNNEL_TYPE":           "playit",
		"PLAYIT_TUNNEL_NAME":    "my-tunnel",
		"BACKING_SERVICE_DNS":   "my-server.games.svc",
		"BACKING_SERVICE_PORTS": "game:25565",
	}))
	if err != nil {
		t.Fatalf("loadConfig() error = %v", err)
	}
	if cfg.TunnelType != "playit" {
		t.Errorf("TunnelType = %q, want %q", cfg.TunnelType, "playit")
	}
	if cfg.PlayitTunnelName != "my-tunnel" {
		t.Errorf("PlayitTunnelName = %q, want %q", cfg.PlayitTunnelName, "my-tunnel")
	}
}

func TestLoadConfigMissingRequired(t *testing.T) {
	tests := []struct {
		name string
		env  map[string]string
	}{
		{
			name: "missing GAMESERVER_NAME",
			env: map[string]string{
				"GAMESERVER_NAMESPACE": "games",
				"TUNNEL_TYPE":          "frp",
			},
		},
		{
			name: "missing GAMESERVER_NAMESPACE",
			env: map[string]string{
				"GAMESERVER_NAME": "my-server",
				"TUNNEL_TYPE":     "frp",
			},
		},
		{
			name: "missing TUNNEL_TYPE",
			env: map[string]string{
				"GAMESERVER_NAME":      "my-server",
				"GAMESERVER_NAMESPACE": "games",
			},
		},
		{
			name: "missing BACKING_SERVICE_DNS",
			env: map[string]string{
				"GAMESERVER_NAME":      "my-server",
				"GAMESERVER_NAMESPACE": "games",
				"TUNNEL_TYPE":          "frp",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := loadConfig(stubEnv(tt.env))
			if err == nil {
				t.Error("expected error, got nil")
			}
		})
	}
}

func TestLoadConfigFrpMissingAddress(t *testing.T) {
	_, err := loadConfig(stubEnv(map[string]string{
		"GAMESERVER_NAME":      "my-server",
		"GAMESERVER_NAMESPACE": "games",
		"TUNNEL_TYPE":          "frp",
		"BACKING_SERVICE_DNS":  "my-server.games.svc",
	}))
	if err == nil {
		t.Error("expected error for missing FRP_SERVER_ADDR")
	}
}

func TestLoadConfigFrpMissingPortMapping(t *testing.T) {
	_, err := loadConfig(stubEnv(map[string]string{
		"GAMESERVER_NAME":      "my-server",
		"GAMESERVER_NAMESPACE": "games",
		"TUNNEL_TYPE":          "frp",
		"FRP_SERVER_ADDR":      "frp.example.com",
		"BACKING_SERVICE_DNS":  "my-server.games.svc",
	}))
	if err == nil {
		t.Error("expected error for missing BACKING_SERVICE_PORT")
	}
}

func TestLoadConfigInvalidFrpPort(t *testing.T) {
	tests := []struct {
		name    string
		portVal string
		wantErr bool
	}{
		{"valid", "7000", false},
		{"out of range", "70000", true},
		{"non-numeric", "abc", true},
		{"zero", "0", true},
		{"negative", "-1", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := loadConfig(stubEnv(map[string]string{
				"GAMESERVER_NAME":      "my-server",
				"GAMESERVER_NAMESPACE": "games",
				"TUNNEL_TYPE":          "frp",
				"FRP_SERVER_ADDR":      "frp.example.com",
				"FRP_SERVER_PORT":      tt.portVal,
				"BACKING_SERVICE_DNS":  "my-server.games.svc",
				"BACKING_SERVICE_PORT": "game:25565",
			}))
			if (err != nil) != tt.wantErr {
				t.Errorf("error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestLoadConfigTailscaleMissingHostname(t *testing.T) {
	_, err := loadConfig(stubEnv(map[string]string{
		"GAMESERVER_NAME":       "my-server",
		"GAMESERVER_NAMESPACE":  "games",
		"TUNNEL_TYPE":           "tailscale",
		"BACKING_SERVICE_DNS":   "my-server.games.svc",
		"BACKING_SERVICE_PORTS": "game:25565",
	}))
	if err == nil || !strings.Contains(err.Error(), "TAILSCALE_HOSTNAME") {
		t.Errorf("loadConfig() error = %v, want it to mention TAILSCALE_HOSTNAME", err)
	}
}

func TestLoadConfigTailscaleMissingBackingServicePorts(t *testing.T) {
	_, err := loadConfig(stubEnv(map[string]string{
		"GAMESERVER_NAME":      "my-server",
		"GAMESERVER_NAMESPACE": "games",
		"TUNNEL_TYPE":          "tailscale",
		"TAILSCALE_HOSTNAME":   "my-game",
		"BACKING_SERVICE_DNS":  "my-server.games.svc",
	}))
	if err == nil || !strings.Contains(err.Error(), "BACKING_SERVICE_PORTS") {
		t.Errorf("loadConfig() error = %v, want it to mention BACKING_SERVICE_PORTS", err)
	}
}

func TestLoadConfigPlayitMissingTunnelName(t *testing.T) {
	_, err := loadConfig(stubEnv(map[string]string{
		"GAMESERVER_NAME":       "my-server",
		"GAMESERVER_NAMESPACE":  "games",
		"TUNNEL_TYPE":           "playit",
		"BACKING_SERVICE_DNS":   "my-server.games.svc",
		"BACKING_SERVICE_PORTS": "game:25565",
	}))
	if err == nil || !strings.Contains(err.Error(), "PLAYIT_TUNNEL_NAME") {
		t.Errorf("loadConfig() error = %v, want it to mention PLAYIT_TUNNEL_NAME", err)
	}
}

func TestLoadConfigPlayitMissingBackingServicePorts(t *testing.T) {
	_, err := loadConfig(stubEnv(map[string]string{
		"GAMESERVER_NAME":      "my-server",
		"GAMESERVER_NAMESPACE": "games",
		"TUNNEL_TYPE":          "playit",
		"PLAYIT_TUNNEL_NAME":   "my-tunnel",
		"BACKING_SERVICE_DNS":  "my-server.games.svc",
	}))
	if err == nil || !strings.Contains(err.Error(), "BACKING_SERVICE_PORTS") {
		t.Errorf("loadConfig() error = %v, want it to mention BACKING_SERVICE_PORTS", err)
	}
}

func TestLoadConfigUnsupportedTunnelType(t *testing.T) {
	_, err := loadConfig(stubEnv(map[string]string{
		"GAMESERVER_NAME":      "my-server",
		"GAMESERVER_NAMESPACE": "games",
		"TUNNEL_TYPE":          "invalid-provider",
		"BACKING_SERVICE_DNS":  "my-server.games.svc",
	}))
	if err == nil {
		t.Error("expected error for unsupported TUNNEL_TYPE")
	}
}

// -----------------------------------------------------------------------
// renderConfig tests
// -----------------------------------------------------------------------

func TestRenderFrpConfig(t *testing.T) {
	cfg := Config{
		GameServerName:      "my-server",
		GameServerNamespace: "games",
		TunnelType:          "frp",
		FrpServerAddr:       "frp.example.com",
		FrpServerPort:       7000,
		BackingServiceDNS:   "my-server.games.svc",
		BackingServicePort:  "game:25565:30000:tcp",
	}

	path, err := renderFrpConfig(cfg, "test-token")
	if err != nil {
		t.Fatalf("renderFrpConfig() error = %v", err)
	}
	defer os.Remove(path)

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read config file: %v", err)
	}

	content := string(data)
	if !strings.Contains(content, "serverAddr = \"frp.example.com\"") {
		t.Errorf("config missing serverAddr")
	}
	if !strings.Contains(content, "serverPort = 7000") {
		t.Errorf("config missing serverPort")
	}
	if !strings.Contains(content, "auth.token = \"test-token\"") {
		t.Errorf("config missing auth token")
	}
	if !strings.Contains(content, "localIP = \"my-server.games.svc\"") {
		t.Errorf("config missing backing service DNS")
	}
	if !strings.Contains(content, "type = \"tcp\"") {
		t.Errorf("config missing proxy type")
	}
	if !strings.Contains(content, "localPort = 25565") {
		t.Errorf("config missing local (Service) port")
	}
	if !strings.Contains(content, "remotePort = 30000") {
		t.Errorf("config missing remote (public) port")
	}
}

func TestRenderFrpConfigInvalidPortMapping(t *testing.T) {
	cfg := Config{
		FrpServerAddr:      "frp.example.com",
		FrpServerPort:      7000,
		BackingServiceDNS:  "my-server.games.svc",
		BackingServicePort: "not-a-valid-mapping",
	}

	_, err := renderFrpConfig(cfg, "token")
	if err == nil || !strings.Contains(err.Error(), "invalid port mapping") {
		t.Errorf("renderFrpConfig() error = %v, want invalid port mapping error", err)
	}
}

func TestRenderFrpConfigMultiplePorts(t *testing.T) {
	cfg := Config{
		FrpServerAddr:      "frp.example.com",
		FrpServerPort:      7000,
		BackingServiceDNS:  "my-server.games.svc",
		BackingServicePort: "java:25565:25565:tcp,bedrock:19133:19133:udp",
	}

	path, err := renderFrpConfig(cfg, "token")
	if err != nil {
		t.Fatalf("renderFrpConfig() error = %v", err)
	}
	defer os.Remove(path)

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read config file: %v", err)
	}

	content := string(data)
	if !strings.Contains(content, "name = \"java\"") {
		t.Errorf("config missing java port mapping")
	}
	if !strings.Contains(content, "name = \"bedrock\"") {
		t.Errorf("config missing bedrock port mapping")
	}
}

func TestRenderFrpConfigUDPProtocol(t *testing.T) {
	// F-052: a UDP-advertised port (e.g. Factorio 34197/UDP) must render
	// type = "udp", not the old hard-coded "tcp", and localPort must be the
	// backing Service's own port even when it differs from the public
	// remotePort the user chose.
	cfg := Config{
		FrpServerAddr:      "frp.example.com",
		FrpServerPort:      7000,
		BackingServiceDNS:  "factorio.games.svc",
		BackingServicePort: "game:34197:30000:udp",
	}

	path, err := renderFrpConfig(cfg, "token")
	if err != nil {
		t.Fatalf("renderFrpConfig() error = %v", err)
	}
	defer os.Remove(path)

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read config file: %v", err)
	}

	content := string(data)
	if !strings.Contains(content, "type = \"udp\"") {
		t.Errorf("config missing udp proxy type, got:\n%s", content)
	}
	if !strings.Contains(content, "localPort = 34197") {
		t.Errorf("config missing local (Service) port 34197, got:\n%s", content)
	}
	if !strings.Contains(content, "remotePort = 30000") {
		t.Errorf("config missing remote (public) port 30000, got:\n%s", content)
	}
}

func TestRenderFrpConfigInvalidProtocol(t *testing.T) {
	cfg := Config{
		FrpServerAddr:      "frp.example.com",
		FrpServerPort:      7000,
		BackingServiceDNS:  "my-server.games.svc",
		BackingServicePort: "game:25565:25565:sctp",
	}

	_, err := renderFrpConfig(cfg, "token")
	if err == nil || !strings.Contains(err.Error(), "invalid port mapping protocol") {
		t.Errorf("renderFrpConfig() error = %v, want invalid port mapping protocol error", err)
	}
}

func TestParseTailscaleTags(t *testing.T) {
	tests := []struct {
		name    string
		tagsStr string
		want    []string
		wantErr bool
	}{
		{name: "whitespace and blank entries trimmed and dropped", tagsStr: " tag:a , tag:b,, ", want: []string{"tag:a", "tag:b"}},
		{name: "single tag", tagsStr: "tag:gameplane", want: []string{"tag:gameplane"}},
		{name: "bare name gets tag: prefix", tagsStr: "eng, tag:ops-2", want: []string{"tag:eng", "tag:ops-2"}},
		{name: "empty string", tagsStr: "", want: nil},
		{name: "only commas and whitespace", tagsStr: " , ,, ", want: nil},
		{name: "empty name", tagsStr: "tag:", wantErr: true},
		{name: "name starts with digit", tagsStr: "tag:1abc", wantErr: true},
		{name: "inner space", tagsStr: "tag:a b", wantErr: true},
		{name: "other prefix", tagsStr: "user:bob", wantErr: true},
		{name: "flag-like value", tagsStr: "tag:a,--reset", wantErr: true},
		{name: "shell metacharacter", tagsStr: "tag:a;rm", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseTailscaleTags(tt.tagsStr)
			if (err != nil) != tt.wantErr {
				t.Fatalf("parseTailscaleTags(%q) error = %v, wantErr %v", tt.tagsStr, err, tt.wantErr)
			}
			if !equalArgs(got, tt.want) || (got == nil) != (tt.want == nil) {
				t.Errorf("parseTailscaleTags(%q) = %#v, want %#v", tt.tagsStr, got, tt.want)
			}
		})
	}
}

// TestParseTailscaleTagsDeduplicate asserts that duplicate tags are skipped.
func TestParseTailscaleTagsDeduplicate(t *testing.T) {
	tags, err := parseTailscaleTags("a,tag:a,b")
	if err != nil {
		t.Fatalf("parseTailscaleTags() error = %v", err)
	}
	if len(tags) != 2 || tags[0] != "tag:a" || tags[1] != "tag:b" {
		t.Errorf("parseTailscaleTags(\"a,tag:a,b\") = %v, want [tag:a tag:b]", tags)
	}
}

func TestRenderFrpConfigServerAddrEscaped(t *testing.T) {
	cfg := Config{
		FrpServerAddr:      `server.example.com"` + "\n" + `extra = "value`,
		FrpServerPort:      7000,
		BackingServiceDNS:  "my-server.games.svc",
		BackingServicePort: "game:25565:30000:tcp",
	}
	path, err := renderFrpConfig(cfg, "test-token")
	if err != nil {
		t.Fatalf("renderFrpConfig() error = %v", err)
	}
	defer os.Remove(path)

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}

	config := string(data)
	// escapeTomlString backslash-escapes both the embedded quote and the
	// embedded newline, so the whole malicious value collapses onto a
	// single serverAddr line rather than the newline terminating that
	// line and "extra" starting a real new TOML key. Assert the exact
	// rendered line rather than a substring shape, since the escaped
	// quote is not immediately followed by the line's closing quote.
	wantLine := `serverAddr = "server.example.com\"\nextra = \"value"`
	foundServerAddr := false
	for _, line := range strings.Split(config, "\n") {
		if strings.HasPrefix(line, "serverAddr = ") {
			foundServerAddr = true
			if line != wantLine {
				t.Errorf("serverAddr line = %q, want %q", line, wantLine)
			}
		}
		// A real (unescaped) "extra = ..." line would mean the newline
		// broke out of the serverAddr string and injected a new key.
		if strings.TrimSpace(line) == `extra = "value` {
			t.Errorf("TOML injection detected: found injected line %q in:\n%s", line, config)
		}
	}
	if !foundServerAddr {
		t.Errorf("serverAddr line not found in:\n%s", config)
	}
	// Verify the file parses as valid TOML (basic check: no parse error)
	// We'd use encoding/toml but we want to avoid external imports for simple checks
	if !strings.Contains(config, "auth.token") {
		t.Errorf("rendered TOML missing auth.token")
	}
}

func TestRenderFrpConfigProxyNameEscaped(t *testing.T) {
	cfg := Config{
		FrpServerAddr:      "frp.example.com",
		FrpServerPort:      7000,
		BackingServiceDNS:  "my-server.games.svc",
		BackingServicePort: `bad"name:25565:30000:tcp`,
	}
	path, err := renderFrpConfig(cfg, "test-token")
	if err != nil {
		t.Fatalf("renderFrpConfig() error = %v", err)
	}
	defer os.Remove(path)

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}

	config := string(data)
	// Verify the proxy name is properly escaped
	if !strings.Contains(config, `name = "bad\"name"`) {
		t.Errorf("proxy name not properly escaped, got:\n%s", config)
	}
	// Verify no TOML injection
	if strings.Count(config, "[[proxies]]") != 1 {
		t.Errorf("TOML injection detected in proxy name escaping, got:\n%s", config)
	}
}

// TestRenderPlayitConfig covers what renderPlayitConfig actually does: write
// the raw secret to a file for playitd's --secret-path flag. playitd has no
// local config file for tunnel name or port forwards (see the function's
// doc comment), so there is nothing else to assert here.
func TestRenderPlayitConfig(t *testing.T) {
	path, err := renderPlayitConfig("test-secret-key")
	if err != nil {
		t.Fatalf("renderPlayitConfig() error = %v", err)
	}
	defer os.Remove(path)

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read secret file: %v", err)
	}

	if string(data) != "test-secret-key" {
		t.Errorf("playit secret file content = %q, want %q", string(data), "test-secret-key")
	}
}

func TestRenderConfigDispatchesByType(t *testing.T) {
	tests := []struct {
		tunnelType string
		cfg        Config
	}{
		{
			"frp",
			Config{
				TunnelType:         "frp",
				FrpServerAddr:      "frp.example.com",
				BackingServiceDNS:  "svc.svc",
				BackingServicePort: "game:25565:30000:tcp",
			},
		},
		{
			"playit",
			Config{
				TunnelType:          "playit",
				PlayitTunnelName:    "my-tunnel",
				BackingServicePorts: "game:25565",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.tunnelType, func(t *testing.T) {
			path, err := renderConfig(tt.cfg, "cred")
			if err != nil {
				t.Errorf("renderConfig() error = %v", err)
				return
			}
			defer os.Remove(path)
			if path == "" {
				t.Error("renderConfig() returned empty path")
			}
		})
	}
}

func TestRenderConfigUnknownType(t *testing.T) {
	_, err := renderConfig(Config{TunnelType: "bogus"}, "cred")
	if err == nil || !strings.Contains(err.Error(), "unknown tunnel type") {
		t.Errorf("renderConfig() error = %v, want unknown tunnel type error", err)
	}
}

// -----------------------------------------------------------------------
// readCredentials tests
// -----------------------------------------------------------------------

// withCredentialsDir repoints the package-level credentialsDir var at dir
// for the duration of the test, restoring the original afterward. This is
// what lets readCredentials be exercised end-to-end without touching the
// real /etc/gameplane/tunnel-auth mount.
func withCredentialsDir(t *testing.T, dir string) {
	t.Helper()
	orig := credentialsDir
	credentialsDir = dir
	t.Cleanup(func() { credentialsDir = orig })
}

func TestReadCredentialsSuccess(t *testing.T) {
	tests := []struct {
		tunnelType string
		keyName    string
	}{
		{"frp", "token"},
		{"tailscale", "authKey"},
		{"playit", "secretKey"},
	}

	for _, tt := range tests {
		t.Run(tt.tunnelType, func(t *testing.T) {
			tmpdir := t.TempDir()
			withCredentialsDir(t, tmpdir)

			credPath := filepath.Join(tmpdir, tt.keyName)
			// Leading/trailing whitespace should be trimmed, as it commonly
			// is when a Secret value ends in a trailing newline.
			if err := os.WriteFile(credPath, []byte("  test-value\n"), 0o600); err != nil {
				t.Fatalf("write test credential: %v", err)
			}

			got, err := readCredentials(Config{TunnelType: tt.tunnelType})
			if err != nil {
				t.Fatalf("readCredentials() error = %v", err)
			}
			if got != "test-value" {
				t.Errorf("readCredentials() = %q, want %q", got, "test-value")
			}
		})
	}
}

func TestReadCredentialsMissingFile(t *testing.T) {
	tmpdir := t.TempDir() // empty; no credential files written

	withCredentialsDir(t, tmpdir)

	_, err := readCredentials(Config{TunnelType: "tailscale"})
	if err == nil || !strings.Contains(err.Error(), "read credential") {
		t.Errorf("readCredentials() error = %v, want credential read error", err)
	}
}

func TestReadCredentialsKeyNames(t *testing.T) {
	tests := []struct {
		tunnelType  string
		expectedKey string
	}{
		{"frp", "token"},
		{"tailscale", "authKey"},
		{"playit", "secretKey"},
	}

	tmpdir := t.TempDir() // empty; every lookup below is expected to fail
	withCredentialsDir(t, tmpdir)

	for _, tt := range tests {
		t.Run(tt.tunnelType, func(t *testing.T) {
			_, err := readCredentials(Config{TunnelType: tt.tunnelType})
			if err == nil {
				t.Fatal("expected error for missing credential file")
			}
			if !strings.Contains(err.Error(), tt.expectedKey) {
				t.Errorf("error should mention key name %q, got %v", tt.expectedKey, err)
			}
		})
	}
}

func TestReadCredentialsUnknownTunnelType(t *testing.T) {
	_, err := readCredentials(Config{TunnelType: "bogus"})
	if err == nil || !strings.Contains(err.Error(), "unknown tunnel type") {
		t.Errorf("readCredentials() error = %v, want unknown tunnel type error", err)
	}
}

// -----------------------------------------------------------------------
// escapeTomlString tests
// -----------------------------------------------------------------------

func TestEscapeTomlString(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"simple", "simple"},
		{"with\"quote", "with\\\"quote"},
		{"with\\slash", "with\\\\slash"},
		{"with\nnewline", "with\\nnewline"},
		{"with\ttab", "with\\ttab"},
		{"combined\\\"both", "combined\\\\\\\"both"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := escapeTomlString(tt.input)
			if got != tt.want {
				t.Errorf("escapeTomlString(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

// -----------------------------------------------------------------------
// buildCommand tests
// -----------------------------------------------------------------------

func TestBuildCommandFrp(t *testing.T) {
	cfg := Config{TunnelType: "frp"}
	cmd := buildCommand(context.Background(), cfg)
	if cmd == nil {
		t.Fatal("buildCommand() returned nil")
	}
	if cmd.Path != "/usr/local/bin/frpc" {
		t.Errorf("Path = %q, want %q", cmd.Path, "/usr/local/bin/frpc")
	}
	// frpc's real config flag, confirmed against fatedier/frp's
	// cmd/frpc/sub/root.go, is "-c"/"--config".
	wantArgs := []string{"/usr/local/bin/frpc", "-c", frpConfigPath}
	if !equalArgs(cmd.Args, wantArgs) {
		t.Errorf("Args = %v, want %v", cmd.Args, wantArgs)
	}
}

func TestBuildCommandPlayit(t *testing.T) {
	cfg := Config{TunnelType: "playit"}
	cmd := buildCommand(context.Background(), cfg)
	if cmd == nil {
		t.Fatal("buildCommand() returned nil")
	}
	// playitd (not playit-cli) takes --secret-path, --socket-path and
	// --platform-docker, confirmed against playit-cloud/playit-agent's
	// playitd.rs (v1.0.10) and its official Dockerfile/entrypoint.sh.
	// --socket-path points the IPC socket the address reporter polls at a
	// path the non-root image can create (F-174).
	wantArgs := []string{"/usr/local/bin/playitd", "--secret-path", playitAuthPath, "--socket-path", playitSocketPath, "--platform-docker"}
	if !equalArgs(cmd.Args, wantArgs) {
		t.Errorf("Args = %v, want %v", cmd.Args, wantArgs)
	}
}

func TestBuildCommandUnknownType(t *testing.T) {
	cfg := Config{TunnelType: "bogus"}
	cmd := buildCommand(context.Background(), cfg)
	if cmd != nil {
		t.Errorf("buildCommand() for unknown type = %v, want nil", cmd)
	}
}

func equalArgs(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

// -----------------------------------------------------------------------
// exponentialBackoff tests
// -----------------------------------------------------------------------

func TestExponentialBackoff(t *testing.T) {
	b := &exponentialBackoff{}

	// First retry: 1s (base 1).
	d1 := b.next()
	if d1 < 900*time.Millisecond || d1 > 1100*time.Millisecond {
		t.Errorf("first backoff = %v, want ~1s", d1)
	}

	// Second retry: ~2s (base 2).
	d2 := b.next()
	if d2 < 1800*time.Millisecond || d2 > 2200*time.Millisecond {
		t.Errorf("second backoff = %v, want ~2s", d2)
	}

	// Backoff should be increasing.
	if d2 <= d1 {
		t.Errorf("second backoff %v should be > first %v", d2, d1)
	}
}

func TestExponentialBackoffCap(t *testing.T) {
	b := &exponentialBackoff{}

	// Advance to near the cap: 256 = 2^8, next would be 512.
	for i := 0; i < 10; i++ {
		b.next()
	}

	// Check that we're capped at 5 minutes.
	d := b.next()
	if d > 5*time.Minute+200*time.Millisecond {
		t.Errorf("backoff %v exceeds 5-minute cap", d)
	}
}

func TestExponentialBackoffNeverZeroAcrossLifetimeRetries(t *testing.T) {
	// F-172: uncapped, retry 64 overflowed the shift and returned 0s,
	// putting the supervisor into a busy restart loop. Call next() 70
	// times (past that point) and confirm every delay stays in [1s, 5m].
	b := &exponentialBackoff{}
	for i := 1; i <= 70; i++ {
		d := b.next()
		if d < time.Second {
			t.Fatalf("retry %d: backoff = %v, want >= 1s (want never 0)", i, d)
		}
		if d > 5*time.Minute {
			t.Fatalf("retry %d: backoff = %v, want <= 5m", i, d)
		}
	}
	// From retry 64 on (the old overflow point) the delay must have
	// settled at the 5-minute cap, not reset or wrapped.
	if d := b.next(); d != 5*time.Minute {
		t.Fatalf("retry 71: backoff = %v, want exactly 5m (capped)", d)
	}
}

// -----------------------------------------------------------------------
// isUnrecoverable tests
// -----------------------------------------------------------------------

func TestIsUnrecoverable(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"permission denied", errors.New("permission denied"), true},
		{"exit 126", errors.New("exit status 126"), true},
		{"exit 127", errors.New("exit status 127"), true},
		{"network error", errors.New("dial: connection refused"), false},
		{"generic", errors.New("something went wrong"), false},
	}

	cfg := Config{}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isUnrecoverable(cfg, tt.err)
			if got != tt.want {
				t.Errorf("isUnrecoverable(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}

// -----------------------------------------------------------------------
// run/supervision tests
// -----------------------------------------------------------------------

func TestRunContextCancellation(t *testing.T) {
	tmpdir := t.TempDir()
	withCredentialsDir(t, tmpdir)
	if err := os.WriteFile(filepath.Join(tmpdir, "token"), []byte("test-token"), 0o600); err != nil {
		t.Fatalf("write test credential: %v", err)
	}

	cfg := Config{
		GameServerName:      "test",
		GameServerNamespace: "games",
		TunnelType:          "frp",
		FrpServerAddr:       "localhost",
		FrpServerPort:       7000,
		BackingServiceDNS:   "test.games.svc",
		BackingServicePort:  "game:25565:30000:tcp",
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	// With a cancelled context, run should return nil (clean shutdown) even
	// though it never gets far enough to actually exec anything: the loop's
	// first ctx.Done() check fires before buildCommand is ever called.
	err := run(ctx, cfg)
	if err != nil {
		t.Errorf("run with cancelled context = %v, want nil", err)
	}
}

// TestRunTransientFailureBacksOffThenCancels drives run() past its initial
// setup and into the supervision loop for real: buildCommand's relay binary
// (/usr/local/bin/frpc) does not exist on the machine running this test (no
// relay binaries are installed outside the shipped container image), so
// runCommand's cmd.Start() deterministically fails with "no such file or
// directory" -- a real, safe, side-effect-free exec attempt, not a mock.
// That's treated as a transient failure (it doesn't match isUnrecoverable's
// permission-denied/exit-126/127 patterns), so run() computes a backoff
// delay (1s) and sleeps; the short context timeout here fires well before
// that delay elapses, exercising the backoff select's ctx.Done() case.
func TestRunTransientFailureBacksOffThenCancels(t *testing.T) {
	tmpdir := t.TempDir()
	withCredentialsDir(t, tmpdir)
	if err := os.WriteFile(filepath.Join(tmpdir, "token"), []byte("test-token"), 0o600); err != nil {
		t.Fatalf("write test credential: %v", err)
	}

	cfg := Config{
		GameServerName:      "test",
		GameServerNamespace: "games",
		TunnelType:          "frp",
		FrpServerAddr:       "localhost",
		FrpServerPort:       7000,
		BackingServiceDNS:   "test.games.svc",
		BackingServicePort:  "game:25565:30000:tcp",
	}

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	err := run(ctx, cfg)
	if err != nil {
		t.Errorf("run() = %v, want nil (clean shutdown once the context times out)", err)
	}
}

func TestRunRenderConfigFailure(t *testing.T) {
	tmpdir := t.TempDir()
	withCredentialsDir(t, tmpdir)
	if err := os.WriteFile(filepath.Join(tmpdir, "token"), []byte("test-token"), 0o600); err != nil {
		t.Fatalf("write test credential: %v", err)
	}

	cfg := Config{
		GameServerName:      "test",
		GameServerNamespace: "games",
		TunnelType:          "frp",
		FrpServerAddr:       "localhost",
		FrpServerPort:       7000,
		BackingServiceDNS:   "test.games.svc",
		// Not a "name:port" pair, so renderFrpConfig fails before run() ever
		// reaches the supervision loop.
		BackingServicePort: "not-a-valid-mapping",
	}

	err := run(context.Background(), cfg)
	if err == nil || !strings.Contains(err.Error(), "render config") {
		t.Errorf("run() error = %v, want a render-config error", err)
	}
}

func TestRunReadCredentialsFailure(t *testing.T) {
	withCredentialsDir(t, t.TempDir()) // empty dir; no credential file present

	cfg := Config{
		GameServerName:      "test",
		GameServerNamespace: "games",
		TunnelType:          "tailscale",
		TailscaleHostname:   "my-game",
		BackingServiceDNS:   "test.games.svc",
		BackingServicePorts: "game:25565",
	}

	err := run(context.Background(), cfg)
	if err == nil || !strings.Contains(err.Error(), "read credentials") {
		t.Errorf("run() error = %v, want a read-credentials error", err)
	}
}

func TestRunPlayitConfigDispatch(t *testing.T) {
	tmpdir := t.TempDir()
	withCredentialsDir(t, tmpdir)
	if err := os.WriteFile(filepath.Join(tmpdir, "secretKey"), []byte("test-secret"), 0o600); err != nil {
		t.Fatalf("write test credential: %v", err)
	}

	cfg := Config{
		GameServerName:      "test",
		GameServerNamespace: "games",
		TunnelType:          "playit",
		PlayitTunnelName:    "my-tunnel",
		BackingServiceDNS:   "test.games.svc",
		BackingServicePorts: "game:25565",
	}

	// Cancel up front: run() should still walk readCredentials -> renderConfig
	// for the playit branch before observing the cancellation, and return
	// cleanly.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := run(ctx, cfg)
	if err != nil {
		t.Errorf("run() for playit with cancelled context = %v, want nil", err)
	}
}

// -----------------------------------------------------------------------
// runCommand tests
//
// These use real, standard Linux utilities (true/false/sleep) rather than
// the provider relay binaries: runCommand itself is provider-agnostic, and
// exercising it directly with well-known, always-present system binaries
// lets these assert real process-exit and cancellation behavior without
// touching any gosec-relevant exec call in main.go (this file is exempt
// from gosec per .golangci.yml, and none of these paths are variable
// binaries the production code would ever pass through).
// -----------------------------------------------------------------------

func TestRunCommandSuccess(t *testing.T) {
	cmd := exec.CommandContext(context.Background(), "true")
	if err := runCommand(context.Background(), cmd); err != nil {
		t.Errorf("runCommand() = %v, want nil", err)
	}
}

func TestRunCommandNonZeroExit(t *testing.T) {
	cmd := exec.CommandContext(context.Background(), "false")
	err := runCommand(context.Background(), cmd)
	if err == nil {
		t.Error("runCommand() = nil, want a non-zero-exit error")
	}
}

func TestRunCommandStartError(t *testing.T) {
	cmd := exec.CommandContext(context.Background(), "/nonexistent/binary/gameplane-tunnel-test")
	err := runCommand(context.Background(), cmd)
	if err == nil || !strings.Contains(err.Error(), "start relay") {
		t.Errorf("runCommand() error = %v, want a start-relay error", err)
	}
}

func TestRunCommandContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, "sleep", "5")

	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	start := time.Now()
	err := runCommand(ctx, cmd)
	elapsed := time.Since(start)

	if !errors.Is(err, context.Canceled) {
		t.Errorf("runCommand() error = %v, want context.Canceled", err)
	}
	// cmd.Cancel sends SIGTERM (see runCommand), which "sleep" honors
	// immediately, so this should return well before the 5s sleep would
	// have finished on its own and well before the 10s WaitDelay fallback.
	if elapsed > 4*time.Second {
		t.Errorf("runCommand() took %v after cancellation, want well under the sleep's 5s duration", elapsed)
	}
}
