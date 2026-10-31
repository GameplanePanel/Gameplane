// Command tunnel is a relay client supervisor that runs as a pod and
// configures/starts/supervises frp and playit, or embeds a Tailscale node.
// The binary picks its behavior from the TUNNEL_TYPE env var and handles provider-specific
// config rendering, credential management, and child process supervision.
//
// Configuration and contract (see operator/internal/controller/gameserver_tunnel.go
// on the operator side, which is authoritative):
//
//   - Env: GAMESERVER_NAME, GAMESERVER_NAMESPACE, TUNNEL_TYPE (frp|tailscale|playit).
//   - frp: FRP_SERVER_ADDR, FRP_SERVER_PORT, BACKING_SERVICE_DNS, BACKING_SERVICE_PORT.
//   - tailscale: TAILSCALE_HOSTNAME, TAILSCALE_TAGS, BACKING_SERVICE_DNS, BACKING_SERVICE_PORTS.
//   - playit: PLAYIT_TUNNEL_NAME, BACKING_SERVICE_DNS, BACKING_SERVICE_PORTS. Validated for the
//     operator contract but NOT passed to playitd: playitd has no local config file for port
//     forwards -- those are managed against the account tied to the secret key via the playit.gg
//     dashboard/API, not by this supervisor. BACKING_SERVICE_PORTS also names the ports the
//     address reporter (playit_reporter.go) maps playitd's assigned addresses onto before
//     patching them into the GameServer's status.tunnelEndpoints.
//   - Credentials Secret is mounted read-only at /etc/gameplane/tunnel-auth.
//   - Credential key names: frp uses "token", tailscale uses "authKey", playit uses "secretKey".
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// Version is overridden at build time via -ldflags (see Dockerfile).
var Version = "dev"

const (
	tunnelAuthMountDir = "/etc/gameplane/tunnel-auth"

	// Rendered config file paths, one per provider. These are fixed
	// (rather than a random os.CreateTemp name) for two reasons: (1) each
	// pod running this supervisor hosts exactly one relay process for the
	// lifetime of the container, so there is no concurrent-writer or
	// collision risk a random name would guard against, and (2) they must
	// be compile-time string constants -- not variables -- for buildCommand
	// below to pass gosec's G204 subprocess-argument check structurally
	// (see the doc comment on buildCommand).
	frpConfigPath  = "/tmp/gameplane-tunnel-frpc.toml"
	playitAuthPath = "/tmp/gameplane-tunnel-playit-auth"
)

// credentialsDir is the directory holding the mounted Secret's credential
// files. It defaults to the real mount point; it is a package-level
// variable, rather than a constant, solely so tests can repoint it at a
// t.TempDir() and exercise readCredentials' real success/error paths
// without depending on the container filesystem. Production code never
// changes it from tunnelAuthMountDir.
var credentialsDir = tunnelAuthMountDir

// allowedCredentialKeys is the closed set of credential file names the
// supervisor will read, keyed by TUNNEL_TYPE.
var allowedCredentialKeys = map[string]string{
	"frp":       "token",
	"tailscale": "authKey",
	"playit":    "secretKey",
}

func main() {
	cfg, err := loadConfig(os.Getenv)
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	log.Printf("tunnel %s starting for %s/%s (provider=%s)", Version, cfg.GameServerNamespace, cfg.GameServerName, cfg.TunnelType)

	ctx, cancel := context.WithCancel(context.Background())
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		sig := <-sigCh
		log.Printf("received signal %v, shutting down", sig)
		cancel()
	}()

	if err := run(ctx, cfg); err != nil {
		log.Fatalf("tunnel exiting: %v", err)
	}
	cancel()
	signal.Stop(sigCh)
}

// Config holds the parsed configuration for the tunnel supervisor.
type Config struct {
	GameServerName      string
	GameServerNamespace string
	GameServerUID       string
	TunnelType          string // "frp", "tailscale", or "playit"

	// FRP-specific
	FrpServerAddr      string
	FrpServerPort      int
	BackingServiceDNS  string
	BackingServicePort string // "name:port,..." format

	// Tailscale-specific
	TailscaleHostname   string
	TailscaleTags       string // comma-separated
	BackingServicePorts string // Tailscale: name:port:protocol; playit: name:port

	// Playit-specific
	PlayitTunnelName string
	// BackingServicePorts shared with Tailscale
}

// loadConfig reads and validates the tunnel's configuration via getenv.
func loadConfig(getenv func(string) string) (Config, error) {
	cfg := Config{
		GameServerName:      getenv("GAMESERVER_NAME"),
		GameServerNamespace: getenv("GAMESERVER_NAMESPACE"),
		GameServerUID:       getenv("GAMESERVER_UID"),
		TunnelType:          getenv("TUNNEL_TYPE"),
		BackingServiceDNS:   getenv("BACKING_SERVICE_DNS"),
	}

	if cfg.GameServerName == "" {
		return Config{}, errors.New("GAMESERVER_NAME is required")
	}
	if cfg.GameServerNamespace == "" {
		return Config{}, errors.New("GAMESERVER_NAMESPACE is required")
	}
	if cfg.TunnelType == "" {
		return Config{}, errors.New("TUNNEL_TYPE is required")
	}
	if cfg.BackingServiceDNS == "" {
		return Config{}, errors.New("BACKING_SERVICE_DNS is required")
	}

	switch cfg.TunnelType {
	case "frp":
		if !validFrpIdentity(cfg.GameServerUID) {
			return Config{}, errors.New("GAMESERVER_UID must be a nonempty alphanumeric or hyphen identity for frp; update the operator and tunnel images together")
		}
		cfg.FrpServerAddr = getenv("FRP_SERVER_ADDR")
		if cfg.FrpServerAddr == "" {
			return Config{}, errors.New("FRP_SERVER_ADDR is required for frp provider")
		}

		portStr := getenv("FRP_SERVER_PORT")
		if portStr == "" {
			cfg.FrpServerPort = 7000
		} else {
			port, err := strconv.Atoi(portStr)
			if err != nil || port < 1 || port > 65535 {
				return Config{}, fmt.Errorf("invalid FRP_SERVER_PORT: %q", portStr)
			}
			cfg.FrpServerPort = port
		}

		cfg.BackingServicePort = getenv("BACKING_SERVICE_PORT")
		if cfg.BackingServicePort == "" {
			return Config{}, errors.New("BACKING_SERVICE_PORT is required for frp provider")
		}

	case "tailscale":
		cfg.TailscaleHostname = getenv("TAILSCALE_HOSTNAME")
		if cfg.TailscaleHostname == "" {
			return Config{}, errors.New("TAILSCALE_HOSTNAME is required for tailscale provider")
		}
		cfg.TailscaleTags = getenv("TAILSCALE_TAGS")

		cfg.BackingServicePorts = getenv("BACKING_SERVICE_PORTS")
		if cfg.BackingServicePorts == "" {
			return Config{}, errors.New("BACKING_SERVICE_PORTS is required for tailscale provider")
		}

	case "playit":
		cfg.PlayitTunnelName = getenv("PLAYIT_TUNNEL_NAME")
		if cfg.PlayitTunnelName == "" {
			return Config{}, errors.New("PLAYIT_TUNNEL_NAME is required for playit provider")
		}

		cfg.BackingServicePorts = getenv("BACKING_SERVICE_PORTS")
		if cfg.BackingServicePorts == "" {
			return Config{}, errors.New("BACKING_SERVICE_PORTS is required for playit provider")
		}

	default:
		return Config{}, fmt.Errorf("unsupported TUNNEL_TYPE: %q", cfg.TunnelType)
	}

	return cfg, nil
}

// run supervises the relay process until ctx is cancelled or the process
// exits with an unrecoverable error (e.g., bad credentials, missing config).
func run(ctx context.Context, cfg Config) error {
	// Read the provider's credentials from the mounted Secret.
	creds, err := readCredentials(cfg)
	if err != nil {
		return fmt.Errorf("read credentials: %w", err)
	}

	if cfg.TunnelType == "tailscale" {
		return runTailscale(ctx, cfg, creds)
	}

	// Render the provider's config file.
	configFile, err := renderConfig(cfg, creds)
	if err != nil {
		return fmt.Errorf("render config: %w", err)
	}
	defer func() {
		if configFile != "" {
			_ = os.Remove(configFile)
		}
	}()

	// playit assigns the public address server-side; poll playitd for it
	// and report it into status.tunnelEndpoints for the lifetime of run.
	// frp and tailscale addresses are computed by the operator from spec.
	if cfg.TunnelType == "playit" {
		reporterCtx, stopReporter := context.WithCancel(ctx)
		waitReporter := startPlayitReporter(reporterCtx, cfg)
		defer func() {
			stopReporter()
			waitReporter()
		}()
	}

	h := &relayHealth{}
	if ctx.Err() != nil {
		return nil
	}
	if cfg.TunnelType == "frp" || cfg.TunnelType == "playit" {
		closeHealth, err := startRelayHealth(ctx, h)
		if err != nil {
			return err
		}
		defer closeHealth()
	}
	return superviseRelay(ctx, cfg, h, relayHealthCheck(cfg, creds), func(attemptCtx context.Context) error {
		log.Printf("starting %s relay process", cfg.TunnelType)
		return runCommand(attemptCtx, buildCommand(attemptCtx, cfg))
	}, waitRelayRetry)
}

// readCredentials reads the provider's credential from the mounted Secret directory.
func readCredentials(cfg Config) (string, error) {
	keyName, ok := allowedCredentialKeys[cfg.TunnelType]
	if !ok {
		return "", fmt.Errorf("unknown tunnel type: %q", cfg.TunnelType)
	}

	dir := filepath.Clean(credentialsDir)
	path := filepath.Join(dir, keyName)

	// Defense in depth: keyName is drawn from the closed set above, but
	// confirm the resolved path did not escape the mount directory before
	// opening it, so a variable can never be used to read an arbitrary file.
	rel, err := filepath.Rel(dir, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, "../") {
		return "", fmt.Errorf("credential path %q escapes mount directory %q", path, dir)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		// Don't log the file contents; log only the path.
		return "", fmt.Errorf("read credential from %s: %w", path, err)
	}

	return strings.TrimSpace(string(data)), nil
}

// renderConfig generates the provider-specific config file and returns its path.
// The caller is responsible for cleaning it up.
func renderConfig(cfg Config, credential string) (string, error) {
	switch cfg.TunnelType {
	case "frp":
		return renderFrpConfig(cfg, credential)
	case "playit":
		return renderPlayitConfig(credential)
	default:
		return "", fmt.Errorf("unknown tunnel type: %q", cfg.TunnelType)
	}
}

// renderFrpConfig generates an frpc config file at the fixed frpConfigPath
// (see the const block near the top of the file for why the path is fixed
// rather than a random os.CreateTemp name).
func renderFrpConfig(cfg Config, token string) (string, error) {
	if !validFrpIdentity(cfg.GameServerUID) {
		return "", errors.New("invalid GameServer UID for frp identity")
	}
	// Build the frpc config: server address/port, auth token, and per-port proxies.
	config := fmt.Sprintf(`
user = "%s"
serverAddr = "%s"
serverPort = %d
auth.method = "token"
auth.token = "%s"
webServer.addr = "127.0.0.1"
webServer.port = 7400
webServer.user = "gameplane"
webServer.password = "%s"
transport.heartbeatInterval = 5
transport.heartbeatTimeout = 15
transport.tcpMuxKeepaliveInterval = 5
`, escapeTomlString(cfg.GameServerUID), escapeTomlString(cfg.FrpServerAddr), cfg.FrpServerPort, escapeTomlString(token), escapeTomlString(token))

	// Parse BACKING_SERVICE_PORT format:
	// "name:localPort:remotePort:protocol,name:localPort:remotePort:protocol,...".
	// localPort is the backing Service's own port (from the GameTemplate's
	// containerPort) and remotePort is the public port on the frps host the
	// user picked (spec.networking.tunnel.frp.remotePorts); they are
	// independent values. protocol is "tcp" or "udp", from the template
	// port's own protocol. Using localPort/protocol unconditionally, rather
	// than assuming remotePort also names the Service port and the port is
	// always TCP, is the fix for F-052: frp only worked before when a
	// user's remotePort happened to equal the Service port and the game
	// used TCP.
	for _, entry := range strings.Split(cfg.BackingServicePort, ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		parts := strings.Split(entry, ":")
		if len(parts) != 4 {
			return "", fmt.Errorf("invalid port mapping: %q", entry)
		}
		name := strings.TrimSpace(parts[0])
		localPort := strings.TrimSpace(parts[1])
		remotePort := strings.TrimSpace(parts[2])
		protocol := strings.ToLower(strings.TrimSpace(parts[3]))
		if protocol != "tcp" && protocol != "udp" {
			return "", fmt.Errorf("invalid port mapping protocol: %q", entry)
		}

		config += fmt.Sprintf(`
[[proxies]]
name = "%s"
type = "%s"
localIP = "%s"
localPort = %s
remotePort = %s
`, escapeTomlString(name), protocol, cfg.BackingServiceDNS, localPort, remotePort)
	}

	if err := os.WriteFile(frpConfigPath, []byte(config), 0o600); err != nil {
		return "", fmt.Errorf("write frpc config: %w", err)
	}

	return frpConfigPath, nil
}

// tailscaleTagNameChar reports whether b may appear in a tag name after the
// "tag:" prefix: ASCII letters, digits and '-', per tailcfg.CheckTag.
func tailscaleTagNameChar(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9') || b == '-'
}

// parseTailscaleTags turns the raw TAILSCALE_TAGS value (a comma-separated
// list that may carry whitespace around each entry and empty entries from
// a leading, trailing or doubled comma) into fully qualified tags. Each
// entry is trimmed, empty entries are dropped, and a bare name gets the
// "tag:" prefix, as `tailscale up` itself does for a name with no colon.
// Duplicate tags are skipped, preserving order of first appearance.
// Every resulting tag must pass the same rule as tailscale's
// tailcfg.CheckTag: "tag:" followed by an ASCII letter, then only ASCII
// letters, digits or '-'. The first tag that fails is returned as an error
// and no tags are returned, so a bad value can never reach tsnet registration.
// It returns (nil, nil) when no
// non-empty entry remains.
func parseTailscaleTags(tagsStr string) ([]string, error) {
	var tags []string
	seen := make(map[string]bool)
	for _, tag := range strings.Split(tagsStr, ",") {
		tag = strings.TrimSpace(tag)
		if tag == "" {
			continue
		}
		if !strings.Contains(tag, ":") {
			tag = "tag:" + tag
		}
		name, ok := strings.CutPrefix(tag, "tag:")
		if !ok || name == "" {
			return nil, fmt.Errorf("invalid tag %q: must be \"tag:\" followed by a name", tag)
		}
		if (name[0] < 'a' || name[0] > 'z') && (name[0] < 'A' || name[0] > 'Z') {
			return nil, fmt.Errorf("invalid tag %q: the name must start with a letter", tag)
		}
		for i := 0; i < len(name); i++ {
			if !tailscaleTagNameChar(name[i]) {
				return nil, fmt.Errorf("invalid tag %q: the name may only contain letters, digits or '-'", tag)
			}
		}
		if seen[tag] {
			continue
		}
		seen[tag] = true
		tags = append(tags, tag)
	}
	return tags, nil
}

// renderPlayitConfig writes the secret key to the fixed playitAuthPath
// (see the const block near the top of the file for why the path is fixed
// rather than a random os.CreateTemp name) for playitd's --secret-path flag.
//
// playitd (confirmed against playit-cloud/playit-agent's own source: the
// playitd CLI in packages/playitd/src/bin/playitd.rs takes only --secret,
// --secret-path, --socket-path, --log-path, --platform-docker, and
// --version-overrides) has no local config file for port forwards or a
// tunnel name -- those are configured against the account tied to this
// secret key via the playit.gg dashboard/API, not by this supervisor. The
// PlayitTunnelName and BackingServicePorts fields on Config are
// intentionally not used here; see the package doc comment.
//
// --secret-path (rather than passing the key inline via --secret) is used
// so the secret never appears in the process's argv, which is readable by
// anything that can see /proc/<pid>/cmdline in the pod.
func renderPlayitConfig(secretKey string) (string, error) {
	if err := os.WriteFile(playitAuthPath, []byte(secretKey), 0o600); err != nil {
		return "", fmt.Errorf("write playit secret: %w", err)
	}

	return playitAuthPath, nil
}

// escapeTomlString escapes special characters in a TOML string value.
func escapeTomlString(s string) string {
	return strings.NewReplacer(
		"\\", "\\\\",
		"\"", "\\\"",
		"\n", "\\n",
		"\r", "\\r",
		"\t", "\\t",
	).Replace(s)
}

// buildCommand constructs the command to run the relay process for
// cfg.TunnelType, or nil for an unrecognized type. loadConfig already
// rejects any TunnelType outside "frp"/"tailscale"/"playit" before run()
// is ever reached in the normal main() contract, so the nil case only
// happens if a caller builds a Config by hand (see TestBuildCommandUnknownType).
//
// frpc's TOML embeds its token, and playitd's --secret-path points to a
// private credential file. buildCommand never needs the raw credential.
// Tailscale uses the embedded node path and does not call this function.
//
// The relay binary path and config-file path are both named constants
// (rather than parameters or computed variables) so every argument to
// exec.CommandContext below is either a literal or resolves to one: that is
// what lets this pass gosec's G204 check structurally instead of via
// suppression. G204 flags any subprocess argument it cannot prove constant;
// a previous revision here took the binary path as a flag-configurable
// parameter (validated post hoc by an allow-list) specifically for
// testability, but gosec's analysis doesn't follow that validation, so it
// kept flagging the variable regardless. Since loadConfig already restricts
// TunnelType to a closed set before run() is ever reached, there is no
// legitimate case where a different binary or config path should be used
// for a given provider, so hardcoding both as consts loses nothing at
// runtime. Tests cover the resulting argument lists directly (see
// TestBuildCommandFrp et al.) rather than by exec'ing anything.
//
// The command is tied to ctx so cancellation stops the child (see
// runCommand, which overrides the default Cancel behavior to signal
// SIGTERM rather than SIGKILL).
func buildCommand(ctx context.Context, cfg Config) *exec.Cmd {
	switch cfg.TunnelType {
	case "frp":
		// Confirmed against fatedier/frp's cmd/frpc/sub/root.go: "-c"/"--config"
		// is frpc's real config-file flag.
		return exec.CommandContext(ctx, "/usr/local/bin/frpc", "-c", frpConfigPath)
	case "playit":
		// Confirmed against playit-cloud/playit-agent's packages/playitd/src/bin/playitd.rs
		// and its own Dockerfile/docker/entrypoint.sh, which exec exactly this
		// (playitd --secret ... --platform-docker) as the foreground process in
		// their official container image. playit-cli (historically installed as
		// "playit") is a separate *service manager* around playitd with no flag
		// to run a tunnel in the foreground under this file's exec+Wait
		// supervision model, so playitd -- not playit-cli -- is the binary here.
		//
		// --socket-path moves playitd's IPC control socket from its Linux
		// default (/run/playit/playitd.sock, which this non-root distroless
		// image can't create) to a fixed /tmp path; the address reporter in
		// playit_reporter.go polls it for the assigned public address (F-174).
		return exec.CommandContext(ctx, "/usr/local/bin/playitd",
			"--secret-path", playitAuthPath,
			"--socket-path", playitSocketPath,
			"--platform-docker",
		)
	default:
		return nil
	}
}

// runCommand executes the relay command and waits for it to exit or ctx to cancel.
//
// cmd must have been built with exec.CommandContext against the same ctx
// (see buildCommand). By default that arranges to SIGKILL the child the
// instant ctx is done; that's too abrupt for a relay process, so this
// overrides Cancel to send SIGTERM instead and gives it WaitDelay to exit
// cleanly before exec falls back to a hard kill.
func runCommand(ctx context.Context, cmd *exec.Cmd) error {
	// Inherit stdio so relay logs flow through to the pod's stdout/stderr.
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin

	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		return cmd.Process.Signal(syscall.SIGTERM)
	}
	cmd.WaitDelay = 10 * time.Second

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start relay: %w", err)
	}

	err := cmd.Wait()
	if ctx.Err() != nil {
		// Context was cancelled; cmd.Cancel already signalled the child and
		// exec.Cmd waited (up to WaitDelay) for it to exit.
		return ctx.Err()
	}
	return err
}

// isUnrecoverable reports whether an error is unrecoverable (e.g., bad config,
// missing credentials) vs. transient (e.g., network failure, relay unavailable).
// Heuristic: if the error looks like a permission/config issue, it's unrecoverable;
// otherwise assume it's transient and worth retrying.
func isUnrecoverable(_ Config, err error) bool {
	if err == nil {
		return false
	}
	errStr := err.Error()
	// Exit code 126 or 127: permission denied or command not found (unrecoverable).
	if strings.Contains(errStr, "exit status 126") || strings.Contains(errStr, "exit status 127") {
		return true
	}
	// Permission denied reading credentials or config: unrecoverable.
	if strings.Contains(errStr, "permission denied") {
		return true
	}
	return false
}

// exponentialBackoff tracks restart delay with capped exponential backoff.
type exponentialBackoff struct {
	mu      sync.Mutex
	retries int
}

func (b *exponentialBackoff) reset() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.retries = 0
}

// next returns the next backoff duration and increments the retry counter.
func (b *exponentialBackoff) next() time.Duration {
	b.mu.Lock()
	defer b.mu.Unlock()

	// Exponential backoff: 1s, 2s, 4s, 8s, 16s, capped at 5 minutes.
	//
	// No jitter: jitter exists to spread out a thundering herd of many
	// replicas reconnecting to the same endpoint at once. This supervisor
	// runs as a single-replica Deployment per GameServer talking to its own
	// dedicated relay connection, so there's no herd here to spread out,
	// and adding one would only make restart timing less predictable to
	// operators reading logs.
	//
	// retries is capped at 10 before the shift: base already saturates at
	// the 300s cap below by retries==10 (1<<9 = 512 > 300), so counting any
	// higher never changes the returned delay. The cap matters because,
	// uncapped, a relay that stays up long enough to reach 64 lifetime
	// retries hit undefined shift behavior: on a 64-bit int, 1<<63 is
	// math.MinInt64, which is not > 300 so the cap below was skipped, and
	// time.Duration(MinInt64)*time.Second wrapped again to 0 -- sending the
	// supervisor into a busy restart loop from the 64th retry on (F-172).
	if b.retries < 10 {
		b.retries++
	}

	base := 1 << uint(b.retries-1)
	if base > 300 {
		base = 300
	}
	return time.Duration(base) * time.Second
}
