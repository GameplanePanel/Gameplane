//go:build e2e

package e2e

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/GameplanePanel/gameplane/test/e2e/internal/protocol/joindepth"
)

// fastGameSet lists the games run by default when GAMEPLANE_E2E_GAME_BOT=1 is
// set and GAMEPLANE_E2E_GAMES is unset. These six cover a JOINED Java protocol
// (Minecraft), a JOINED .NET protocol (Terraria), a hand-rolled UDP protocol
// (Factorio), the shared Source family (Garry's Mod), a modded .NET protocol
// (tModLoader) and a UDP-based multiplayer protocol (BeamMP). They boot
// quickly (minutes at most) and fit within a single kind node.
//
// fastGameSet is NOT the same axis as buckets.sh's "bot-fast" CI bucket: the
// CI bot-fast bucket only runs TestGameServer_MinecraftJavaBot_Joined,
// TestGameServer_TerrariaBot_Joined and TestGameServer_GarrysModBot_Query
// (plus the wake-on-connect tests) to stay within GitHub runner disk limits.
// fastGameSet is the broader default scope for a manual/local
// GAMEPLANE_E2E_GAME_BOT=1 run; factorio, tmodloader and beammp are in
// fastGameSet but are excluded from CI's bot-fast bucket and instead
// bucketed under bot-heavy, mainly for CI disk budget reasons. See
// buckets.sh's bucket_bot_fast/bucket_bot_heavy comments for the CI-side
// rationale, including the per-game notes on why each one is heavy.
var fastGameSet = []string{
	"minecraft-java", "terraria", "factorio", "garrys-mod",
	"tmodloader", "beammp",
}

// heavyGameSet lists the games not in the fast set. These are opt-in only
// (GAMEPLANE_E2E_GAMES=all) due to large disk/network requirements; they are
// not run in CI. Note: a game appearing here does not mean a client exists for
// it yet; skipUnlessGameInScope only decides scope, not test existence.
var heavyGameSet = []string{
	"cs2", "7-days-to-die", "project-zomboid", "valheim", "palworld", "rust",
	"v-rising", "dayz", "ark-survival-ascended", "dont-starve-together",
	"enshrouded", "satisfactory",
	"fivem", "team-fortress-2", "farming-simulator-25", "euro-truck-simulator-2",
	"mount-and-blade-2-bannerlord", "left-4-dead-2", "the-isle",
	"ark-survival-evolved", "arma-reforger", "hell-let-loose", "squad",
}

// parseGameScope parses GAMEPLANE_E2E_GAMES and returns the set of games to test,
// or nil if the gate is not enabled. The env var can be:
//   - unset  => the fast set
//   - "all"  => every game (fast + heavy)
//   - comma-separated list => those games (whitespace trimmed around entries)
func parseGameScope() []string {
	games := os.Getenv("GAMEPLANE_E2E_GAMES")
	if games == "" {
		return fastGameSet
	}
	if games == "all" {
		// Return fast + heavy concatenated. Build a fresh slice to avoid
		// aliasing fastGameSet's backing array.
		result := make([]string, 0, len(fastGameSet)+len(heavyGameSet))
		result = append(result, fastGameSet...)
		result = append(result, heavyGameSet...)
		return result
	}
	// Parse comma-separated list, trim whitespace.
	parts := strings.Split(games, ",")
	var result []string
	for _, p := range parts {
		if trimmed := strings.TrimSpace(p); trimmed != "" {
			result = append(result, trimmed)
		}
	}
	return result
}

// skipUnlessGameInScope skips the test if GAMEPLANE_E2E_GAME_BOT is unset,
// or if the game is not in scope according to GAMEPLANE_E2E_GAMES.
// It logs a clear reason in each case.
func skipUnlessGameInScope(t *testing.T, game string) {
	t.Helper()
	if os.Getenv("GAMEPLANE_E2E_GAME_BOT") == "" {
		t.Skip("heavy: set GAMEPLANE_E2E_GAME_BOT=1 to run the game bot tests")
	}
	scope := parseGameScope()
	for _, allowed := range scope {
		if allowed == game {
			return
		}
	}
	t.Skipf("game %q not in scope (GAMEPLANE_E2E_GAMES=%q)", game, os.Getenv("GAMEPLANE_E2E_GAMES"))
}

// gamePort describes a single port exported by a game container.
type gamePort struct {
	Name     string
	Port     int
	Protocol string // "TCP" | "UDP"
}

// gameResources describes CPU/memory requests and limits for a game container.
type gameResources struct {
	ReqCPU, ReqMem, LimCPU, LimMem string
}

// pathAControl declares how a game is driven THROUGH Gameplane after Path B proves
// the server is running. Empty/unset means no Path A test (server is unchecked for
// control channel availability).
type pathAControl struct {
	Mode   string // "rcon" | "stdin" | "" (no control channel)
	Action string // action id to run

	// ExpectRaw is required when Mode == "rcon": the substring the
	// action's RCON reply (the `raw` field on POST /actions/run's
	// response — see agent/internal/actions/actions.go's runResp, which
	// api/internal/ws/actions.go's actionRunResp mirrors) must contain to
	// prove the command actually ran on the game server. Most chat/
	// broadcast RCON commands (e.g. Minecraft's "say") return an EMPTY
	// RCON reply — the message only shows up in game chat/logs, never in
	// the reply itself — so asserting a broadcast action's reply would be
	// vacuous. Pick a declared action whose reply carries real text
	// instead (verified against the game's actual output at the call
	// site, not assumed).
	ExpectRaw string
}

// gameBotSpec fully describes a game for bot testing: template config,
// container config, readiness expectations, and probe parameters.
type gameBotSpec struct {
	Game          string // module dir name, e.g. "minecraft-java"
	Template      string // GameTemplate name
	DisplayName   string
	Image         string
	Env           map[string]string
	Ports         []gamePort
	StorageSize   string
	MountPath     string
	Resources     gameResources
	ReadyTimeout  time.Duration
	ProbePort     int
	ProbeDeadline time.Duration
	ExpectDepth   joindepth.JoinDepth
	ProbeArgs     []string
	Probes        map[string]any // spec.probes; nil means the operator sets no probe
	RCON          map[string]any // spec.rcon
	ConsoleMode   string         // spec.consoleMode
	Actions       []any          // spec.capabilities.actions; nil means no actions
	Control       pathAControl   // Path A: drive the server through Gameplane (optional)
}

// runGameBotTest creates the GameTemplate + GameServer, waits for
// status.phase == Running, then runs the in-cluster probe. It expects
// the probe to reach ExpectDepth or the test fails.
func runGameBotTest(t *testing.T, s gameBotSpec) {
	t.Helper()

	// Enforcement: verify the test name suffix matches the asserted depth.
	// Tests ending in _Joined must assert JOINED, _Query must assert QUERY,
	// _Partial must assert PARTIAL. This constraint prevents silent test-name
	// drift from the actual depth being tested.
	testName := t.Name()
	lastUnder := strings.LastIndex(testName, "_")
	var suffix string
	if lastUnder >= 0 {
		suffix = testName[lastUnder+1:]
	}

	var expectedSuffix string
	switch s.ExpectDepth {
	case joindepth.JOINED:
		expectedSuffix = "Joined"
	case joindepth.PARTIAL:
		expectedSuffix = "Partial"
	case joindepth.QUERY:
		expectedSuffix = "Query"
	}

	if expectedSuffix != "" && suffix != expectedSuffix {
		t.Fatalf("test name suffix mismatch: %s ends with %q but asserts depth %s (expected suffix _%s)", testName, suffix, s.ExpectDepth.String(), expectedSuffix)
	}

	gsName, ns := createGameBotServer(t, envInstance, s)

	// Run the in-cluster probe (positive control).
	result := envInstance.RunGameProbe(t, GameProbe{
		GameNS:      ns,
		GSName:      gsName,
		Game:        s.Game,
		Port:        s.ProbePort,
		Deadline:    s.ProbeDeadline,
		ExpectDepth: s.ExpectDepth,
		Args:        s.ProbeArgs,
	})
	if result.ExitCode != 0 {
		var verdictStr string
		if result.Verdict != nil {
			verdictStr = result.Verdict.String()
		} else {
			verdictStr = "UNKNOWN"
		}
		t.Fatalf("positive control probe failed for game %q: exit code %d (expected 0), expected depth %s, verdict %s", s.Game, result.ExitCode, s.ExpectDepth.String(), verdictStr)
	}

	// Negative control: verify the probe can fail. Run the same probe against a
	// guaranteed-closed address (127.0.0.1:1) with -expect-fail. This proves:
	//   - The probe correctly reports failure when it cannot reach the server.
	//   - The probe does not always report success (structural guarantee that
	//     a broken probe cannot masquerade as working).
	// 127.0.0.1:1 is reliably closed in-cluster: it's a loopback address
	// (the probe pod's own 127.0.0.1) with port 1, which is reserved and never
	// listens. The probe will immediately get connection refused, proving
	// transport failure, not a measurement error.
	negCtrlResult := envInstance.RunGameProbe(t, GameProbe{
		GameNS:      "default",
		GSName:      "negative-control-" + s.Game,
		Game:        s.Game,
		Port:        1,
		Deadline:    s.ProbeDeadline,
		ExpectDepth: s.ExpectDepth,
		ExpectFail:  true,
		Args:        s.ProbeArgs,
	})
	// Negative control passes only when the probe exits 0 (correctly
	// failed for transport reasons, depth UNKNOWN). If it reached a
	// live server (QUERY/PARTIAL/JOINED), had an internal error, or
	// exited non-zero for any reason, fail the test.
	if negCtrlResult.ExitCode != 0 {
		if negCtrlResult.Verdict != nil && negCtrlResult.Verdict.ReachedDepth.String() != "UNKNOWN" {
			t.Fatalf("negative control probe for game %q unexpectedly reached a live server: depth %s (expected UNKNOWN for transport failure)", s.Game, negCtrlResult.Verdict.ReachedDepth.String())
		}
		if negCtrlResult.Verdict == nil {
			t.Fatalf("negative control probe for game %q failed with exit code %d but verdict could not be parsed", s.Game, negCtrlResult.ExitCode)
		}
		// Fallback for any other non-zero exit code (e.g., internal error with UNKNOWN verdict).
		t.Fatalf("negative control probe for game %q failed with exit code %d (expected 0): verdict depth %s", s.Game, negCtrlResult.ExitCode, negCtrlResult.Verdict.ReachedDepth.String())
	}

	// Path A: drive the server THROUGH Gameplane if a control channel is declared.
	// This proves the API, agent, and control protocol integration work end-to-end.
	if s.Control.Mode != "" {
		runGameBotPathA(t, gsName, ns, s.Control)
	}
}

// createGameBotServer creates the GameTemplate and the GameServer described by s
// in the games namespace, then waits for status.phase == Running. Both objects are
// deleted when the test ends. It returns the GameServer name and its namespace.
func createGameBotServer(t *testing.T, env *Env, s gameBotSpec) (gsName, namespace string) {
	t.Helper()

	ctx := context.Background()
	ns := "gameplane-games"

	// Build the env array from the map, sorted for determinism.
	var envKeys []string
	for k := range s.Env {
		envKeys = append(envKeys, k)
	}
	sort.Strings(envKeys)
	var envArray []any
	for _, k := range envKeys {
		envArray = append(envArray, map[string]any{
			"name":  k,
			"value": s.Env[k],
		})
	}

	// Build the ports array.
	var portsArray []any
	for _, p := range s.Ports {
		portsArray = append(portsArray, map[string]any{
			"name":          p.Name,
			"containerPort": int64(p.Port),
			"advertise":     true,
			"protocol":      p.Protocol,
		})
	}

	// Create the GameTemplate.
	spec := map[string]any{
		"displayName": s.DisplayName,
		"game":        s.Game,
		"version":     "1",
		"image":       s.Image,
		"env":         envArray,
		"ports":       portsArray,
		"storage": map[string]any{
			"size":      s.StorageSize,
			"mountPath": s.MountPath,
		},
		"resources": map[string]any{
			"requests": map[string]any{
				"cpu":    s.Resources.ReqCPU,
				"memory": s.Resources.ReqMem,
			},
			"limits": map[string]any{
				"cpu":    s.Resources.LimCPU,
				"memory": s.Resources.LimMem,
			},
		},
	}

	// Set optional probes, rcon, consoleMode, and actions if provided.
	if s.Probes != nil {
		spec["probes"] = s.Probes
	}
	if s.RCON != nil {
		spec["rcon"] = s.RCON
	}
	if s.ConsoleMode != "" {
		spec["consoleMode"] = s.ConsoleMode
	}
	if s.Actions != nil {
		spec["capabilities"] = map[string]any{
			"actions": s.Actions,
		}
	}

	tmpl := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "gameplane.local/v1alpha1",
		"kind":       "GameTemplate",
		"metadata":   map[string]any{"name": s.Template},
		"spec":       spec,
	}}
	if _, err := env.Dyn.Resource(gameTemplateGVR).
		Create(ctx, tmpl, metav1.CreateOptions{}); err != nil && !apierrors.IsAlreadyExists(err) {
		t.Fatalf("create template: %v", err)
	}
	t.Cleanup(func() {
		_ = env.Dyn.Resource(gameTemplateGVR).
			Delete(context.Background(), s.Template, metav1.DeleteOptions{})
	})

	// Create the GameServer, deriving its name from the template name.
	gsName = s.Template + "-bot"
	gs := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "gameplane.local/v1alpha1",
		"kind":       "GameServer",
		"metadata":   map[string]any{"name": gsName, "namespace": ns},
		"spec": map[string]any{
			"templateRef": map[string]any{"name": s.Template},
		},
	}}
	if _, err := env.Dyn.Resource(gameServerGVR).Namespace(ns).
		Create(ctx, gs, metav1.CreateOptions{}); err != nil && !apierrors.IsAlreadyExists(err) {
		t.Fatalf("create gameserver: %v", err)
	}
	t.Cleanup(func() {
		_ = env.Dyn.Resource(gameServerGVR).Namespace(ns).
			Delete(context.Background(), gsName, metav1.DeleteOptions{})
	})

	// Wait for the server to reach Running phase.
	env.Eventually(t, s.ReadyTimeout, func() (bool, string) {
		obj, err := env.Dyn.Resource(gameServerGVR).Namespace(ns).Get(ctx, gsName, metav1.GetOptions{})
		if err != nil {
			return false, fmt.Sprintf("get gs: %v", err)
		}
		phase, _, _ := unstructured.NestedString(obj.Object, "status", "phase")
		if phase == "Running" {
			return true, ""
		}
		return false, "phase=" + phase
	})
	return gsName, ns
}

// runGameBotPathA drives the SAME already-running server through Gameplane
// (API → agent, or API → kubelet-attach → game protocol) to prove
// end-to-end control integration. It expects the server to already be in
// Running phase and reachable — RunGameProbe (Path B), which runs
// immediately before this, already proved the game's own protocol port is
// live.
//
// The two Mode values assert through a DIFFERENT surface each, because
// the two shipped games declare different control planes:
//
//   - "rcon" (Minecraft): the action runs over RCON, proxied through the
//     agent sidecar (api/internal/ws/dialer.go's httpProxy). The RCON
//     reply itself carries the command's output, so that reply is what
//     gets asserted — see runControlActionRCON. This path still depends
//     on the agent being reachable.
//
//   - "stdin" (Terraria): the action is fire-and-forget pod-attach (no
//     RCON, no reply body) — its effect only ever shows up on the
//     container's stdout, which is exactly what the console-pty stream
//     carries for a consoleMode: pty template. With the e2e template
//     (no module console wrapper) nothing reads the container's stdin, so
//     what the stream carries is the TTY line-discipline echo of the
//     written line; the assertion therefore proves the write reached the
//     pod through a live attach, not that the game acted on it. Because
//     pod-attach output is live-tail only, runControlActionStdinPTY first
//     ARMS the console-pty session (writes a probe line through the same
//     WS and waits for its echo) before firing the action. See
//     runControlActionStdinPTY.
//     Neither the action write nor console-pty touch the agent sidecar —
//     both attach via the API's own in-cluster kubeconfig against the
//     kubelet (mountAttach/WriteStdinLines) — so this path has no
//     agent-unreachable failure mode at all.
//
// A prior version of this helper always tailed /ws/servers/{name}/logs
// (the agent-proxied log surface) regardless of Mode. That surface needs
// GameTemplate.spec.logPath configured — the operator only passes
// --game-log-path to the agent when it's set (operator/internal/
// controller/gameserver_controller.go:1264-1265) — and our hand-built bot
// templates never set it, so the agent's /logs/tail handler 503s
// ("log tailing not configured", agent/internal/logs/logs.go:66-69)
// instead of upgrading to a WebSocket, and the API's dial side
// (api/internal/ws/dialer.go:158-172) turns that failed upgrade into a
// bare "agent dial failed" WS close — exactly what CI observed for BOTH
// games. Terraria in particular has no log file to point logPath at in
// the first place: passivelemon/terraria-docker logs to stdout only, with
// "no persistent logfile" (modules/terraria/template.yaml's own doc
// comment) — so declaring logPath was never going to fix it. Asserting
// through each game's own declared control surface instead removes the
// log-tail dependency entirely, for both games.
func runGameBotPathA(t *testing.T, gsName, ns string, ctrl pathAControl) {
	t.Helper()

	// Log in once; reuse the same client for all Path A calls to stay
	// within the login budget (~2 for the entire bot bucket).
	envInstance.BootstrapAdmin(t, adminUsername, adminPassword)
	cli := envInstance.APIClient(t, adminUsername, adminPassword)
	defer cli.Close()

	switch ctrl.Mode {
	case "rcon":
		runControlActionRCON(t, cli, gsName, ns, ctrl)
	case "stdin":
		runControlActionStdinPTY(t, cli, gsName, ctrl)
	default:
		t.Fatalf("pathAControl for %s: unknown Mode %q", gsName, ctrl.Mode)
	}
}

// runControlActionRCON fires an RCON-transport action via the API and
// asserts its effect through the RCON reply body itself — the same text a
// real RCON client would get back — rather than tailing logs.
func runControlActionRCON(t *testing.T, cli *APIClient, gsName, ns string, ctrl pathAControl) {
	t.Helper()
	if ctrl.ExpectRaw == "" {
		t.Fatalf("pathAControl for %s: rcon mode requires ExpectRaw", gsName)
	}

	// actions/run (rcon transport) dials the agent over the <gs>-agent
	// Service/mTLS, and "GameServer phase == Running" does not mean that
	// path is up yet: the agent container can still be starting, and even
	// once Ready=true the Service's EndpointSlice + kube-proxy dataplane
	// programming lag behind (see requireAgentReady/waitAgentReachable doc
	// comments in api_agent_e2e_test.go).
	requireAgentReady(t, ns, gsName)
	waitAgentReachable(t, cli, gsName)

	resp, body, err := cli.Post(
		fmt.Sprintf("/servers/%s/actions/run", gsName),
		map[string]any{"id": ctrl.Action},
	)
	if err != nil {
		t.Fatalf("API rejected the action: POST /actions/run transport error: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusBadGateway || resp.StatusCode == http.StatusGatewayTimeout {
		// api/internal/ws/dialer.go's writeUpstreamErr maps a failed
		// API→agent dial to 502/504 with body "agent unreachable" — a
		// distinct failure from the API validating and rejecting the
		// request outright (checked below). requireAgentReady/
		// waitAgentReachable above should have already ruled this out;
		// distinguish it clearly if it still happens (e.g. the agent
		// restarted between the check and now).
		t.Fatalf("agent unreachable: POST /servers/%s/actions/run returned %d: %s", gsName, resp.StatusCode, string(body))
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("API rejected the action: POST /servers/%s/actions/run returned %d: %s", gsName, resp.StatusCode, string(body))
	}

	var respBody struct {
		OK  bool   `json:"ok"`
		Raw string `json:"raw"`
	}
	if err := json.Unmarshal(body, &respBody); err != nil {
		t.Fatalf("API rejected the action: decode action response: %v\n%s", err, string(body))
	}
	if !respBody.OK {
		t.Fatalf("API rejected the action: response body reported ok=false: %s", string(body))
	}
	if !strings.Contains(respBody.Raw, ctrl.ExpectRaw) {
		t.Fatalf("action ran but its effect never appeared: RCON reply %q does not contain %q", respBody.Raw, ctrl.ExpectRaw)
	}
}

// runControlActionStdinPTY fires a stdin-transport action via the API and
// asserts its effect on the console-pty stream — the same route
// TestAPI_ConsolePTYRoundTrip exercises (api_ws_e2e_test.go) — since a
// fire-and-forget stdin action's own POST response carries no output (see
// runStdinAction's doc comment in api/internal/ws/actions.go): any effect
// only ever shows up on the game's stdout.
//
// The console-pty session is dialed AND ARMED before the action fires:
// pod-attach output is live-tail only (nothing buffers it for a client that
// attaches late), and the action's write goes through a SEPARATE,
// short-lived attach session (WriteStdinLines in
// api/internal/kube/stdin.go) rather than this one. A completed WS upgrade
// is not enough — mountAttach (attach.go) accepts the WS first and only
// then builds the SPDY attach to the kubelet, so the marker could race past
// a stdout reader that does not exist yet. Arming therefore writes a unique
// probe line as stdin through this very WS (retried periodically) and waits
// until its TTY echo comes back on stdout, which proves this session's
// stdout side is live. Only then is the action POSTed. One reader goroutine
// serves the whole session (coder/websocket closes the connection when a
// Read context expires, so per-read timeouts must not be used); the
// arming phase and the marker wait consume the same frame channel. Both
// connections attach via the API's own in-cluster kubeconfig (mountAttach
// in attach.go / WriteStdinLines in stdin.go), never the agent sidecar's
// mTLS proxy — so unlike runControlActionRCON, this path has no
// agent-unreachable failure mode at all; a read failure here means the
// pod-attach itself broke.
func runControlActionStdinPTY(t *testing.T, cli *APIClient, gsName string, ctrl pathAControl) {
	t.Helper()
	ctx := context.Background()
	uniqueMessage := gsName + "-pathA"

	wsConn, stop := dialAuthedWS(t, cli, fmt.Sprintf("/ws/servers/%s/console-pty", gsName))
	defer stop()

	// One reader for the whole session; cancelled (before stop() closes the
	// conn, LIFO) on every return path so the goroutine never leaks.
	sessCtx, sessCancel := context.WithCancel(ctx)
	defer sessCancel()
	frames := startPTYReader(sessCtx, wsConn)

	// Arm: the WS upgrade completes before the API's SPDY attach exists, so
	// prove the stdout side is live by echoing a probe line through this
	// same session before the action's separate attach fires.
	const (
		armTimeout  = 60 * time.Second
		armInterval = 2 * time.Second
	)
	armProbe := gsName + "-arm"
	probeFrame, mErr := json.Marshal(ptyEnvelope{
		Kind: "stdin",
		Body: base64.StdEncoding.EncodeToString([]byte(armProbe + "\n")),
	})
	if mErr != nil {
		t.Fatalf("marshal arming probe: %v", mErr)
	}
	writeProbe := func() {
		wCtx, wCancel := context.WithTimeout(sessCtx, 10*time.Second)
		defer wCancel()
		if wErr := wsConn.Write(wCtx, websocket.MessageText, probeFrame); wErr != nil {
			t.Fatalf("pod attach unreachable: write arming probe %q to console-pty failed: %v", armProbe, wErr)
		}
	}
	writeProbe()
	armTimer := time.NewTimer(armTimeout)
	defer armTimer.Stop()
	armTick := time.NewTicker(armInterval)
	defer armTick.Stop()
	awaitPTYMarker(t, frames, armProbe, armTimer.C, armTick.C, writeProbe)

	resp, body, err := cli.Post(
		fmt.Sprintf("/servers/%s/actions/run", gsName),
		map[string]any{
			"id":     ctrl.Action,
			"params": map[string]string{"message": uniqueMessage},
		},
	)
	if err != nil {
		t.Fatalf("API rejected the action: POST /actions/run transport error: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("API rejected the action: POST /servers/%s/actions/run returned %d: %s", gsName, resp.StatusCode, string(body))
	}
	defer func() { _ = resp.Body.Close() }()
	var respBody struct {
		OK bool `json:"ok"`
	}
	if err := json.Unmarshal(body, &respBody); err != nil {
		t.Fatalf("API rejected the action: decode action response: %v\n%s", err, string(body))
	}
	if !respBody.OK {
		t.Fatalf("API rejected the action: response body reported ok=false: %s", string(body))
	}

	// Same overall 30s deadline as before, started after the action POST
	// succeeded; same reader/frame channel as the arming phase.
	markerTimer := time.NewTimer(30 * time.Second)
	defer markerTimer.Stop()
	awaitPTYMarker(t, frames, uniqueMessage, markerTimer.C, nil, nil)
}

// ptyFrame is one decoded console-pty frame handed from the reader goroutine
// to the test goroutine. kind is "stdout" or "err"; text is the decoded
// stdout bytes or the server's error message; err is a WS read failure.
type ptyFrame struct {
	kind string
	text string
	err  error
}

// startPTYReader runs the single long-lived console-pty reader for a session.
// It decodes envelopes (dropping malformed/non-stdout/non-err frames), sends
// them on the returned channel, and exits (closing the channel) on the first
// read error or when ctx is cancelled. It never calls t. A Read ctx expiry
// closes a coder/websocket conn, so ctx here is only the session ctx.
func startPTYReader(ctx context.Context, ws *websocket.Conn) <-chan ptyFrame {
	out := make(chan ptyFrame, 16)
	go func() {
		defer close(out)
		for {
			_, data, err := ws.Read(ctx)
			var f ptyFrame
			if err != nil {
				f = ptyFrame{err: err}
			} else {
				var env ptyEnvelope
				if json.Unmarshal(data, &env) != nil {
					continue
				}
				switch env.Kind {
				case "err":
					f = ptyFrame{kind: "err", text: env.Body}
				case "stdout":
					raw, decErr := base64.StdEncoding.DecodeString(env.Body)
					if decErr != nil {
						continue
					}
					f = ptyFrame{kind: "stdout", text: string(raw)}
				default:
					continue
				}
			}
			select {
			case out <- f:
			case <-ctx.Done():
				return
			}
			if f.err != nil {
				return
			}
		}
	}()
	return out
}

// awaitPTYMarker consumes frames until marker appears in the stdout stream
// (matched across frame boundaries via a short rolling tail) and returns, or
// fails the test on a server err envelope, a WS read error, the reader
// stopping, or the deadline channel firing. If tick is non-nil, onTick is
// called on each tick (used to re-send the arming probe). Must run on the
// test goroutine (calls t.Fatalf).
func awaitPTYMarker(t *testing.T, frames <-chan ptyFrame, marker string, deadline, tick <-chan time.Time, onTick func()) {
	t.Helper()
	tail := ""
	for {
		select {
		case f, ok := <-frames:
			if !ok {
				t.Fatalf("pod attach unreachable: console-pty reader stopped before %q appeared", marker)
			}
			if f.err != nil {
				// mountAttach (attach.go) accepts the WS unconditionally, then
				// writes a {"kind":"err",...} envelope before closing on any
				// pod-attach failure (build-executor error, SPDY stream
				// error) — so an outright Read error here means the
				// connection dropped some OTHER way (TCP reset, close), not
				// a clean server-reported failure.
				t.Fatalf("pod attach unreachable: console-pty WS read failed before %q appeared: %v", marker, f.err)
			}
			if f.kind == "err" {
				t.Fatalf("pod attach unreachable: console-pty returned a server-side error envelope before %q appeared: %s", marker, f.text)
			}
			buf := tail + f.text
			if strings.Contains(buf, marker) {
				return // Success: marker seen on the console-pty stream.
			}
			if keep := len(marker) - 1; len(buf) > keep {
				tail = buf[len(buf)-keep:]
			} else {
				tail = buf
			}
		case <-tick:
			onTick()
		case <-deadline:
			t.Fatalf("pod attach unreachable: console-pty WS read failed before %q appeared: timed out waiting for it on the stream", marker)
		}
	}
}
