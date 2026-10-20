// Package main runs the Gameplane agent sidecar.
package main

import (
	"context"
	"errors"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/GameplanePanel/gameplane/agent/internal/actions"
	"github.com/GameplanePanel/gameplane/agent/internal/auth"
	"github.com/GameplanePanel/gameplane/agent/internal/caps"
	"github.com/GameplanePanel/gameplane/agent/internal/console"
	"github.com/GameplanePanel/gameplane/agent/internal/files"
	"github.com/GameplanePanel/gameplane/agent/internal/heartbeat"
	"github.com/GameplanePanel/gameplane/agent/internal/lifecycle"
	"github.com/GameplanePanel/gameplane/agent/internal/logs"
	"github.com/GameplanePanel/gameplane/agent/internal/metrics"
	"github.com/GameplanePanel/gameplane/agent/internal/mods"
	"github.com/GameplanePanel/gameplane/agent/internal/players"
	"github.com/GameplanePanel/gameplane/agent/internal/quiesce"
	"github.com/GameplanePanel/gameplane/agent/internal/rcon"
	"github.com/GameplanePanel/gameplane/agent/internal/status"
	"github.com/GameplanePanel/gameplane/agent/internal/target"
	"github.com/GameplanePanel/gameplane/agent/internal/usage"
)

// Version is overridden at build time via -ldflags.
var Version = "dev"

// main is the entrypoint for the Gameplane sidecar agent process.
func main() {
	var (
		addr         string
		metricsAddr  string
		dataRoot     string
		rconHost     string
		rconPort     int
		rconPassFile string
		rconEnabled  bool
		rconProtocol string
		gameLogPath  string
		certFile     string
		keyFile      string
		clientCAFile string
		apiTokenFile string
		serverName   string
		serverUID    string
		templateName string
		gameName     string
		capsJSON     string
		cliPipe      string
		logLevel     string
	)
	flag.StringVar(&addr, "addr", ":8090", "HTTP listen address")
	flag.StringVar(&metricsAddr, "metrics-addr", envOr("GAMEPLANE_METRICS_ADDR", ":9090"),
		"listen address for the Prometheus metrics endpoint, separate from --addr and served "+
			"without TLS or auth (empty = metrics not served); --addr's mTLS control mux never "+
			"carries /metrics, so this port is what a PodMonitor scrapes")
	flag.StringVar(&dataRoot, "data-root", "/data", "path under which file ops are rooted")
	flag.StringVar(&rconHost, "rcon-host", "127.0.0.1", "RCON host (loopback in the pod)")
	flag.IntVar(&rconPort, "rcon-port", 25575, "RCON port")
	flag.StringVar(&rconPassFile, "rcon-password-file", "", "path to file holding the RCON password")
	flag.BoolVar(&rconEnabled, "rcon-enabled", envOr("GAMEPLANE_RCON_ENABLED", "true") != "false",
		"whether the game exposes RCON; when false, RCON-backed endpoints degrade instead of dialing")
	flag.StringVar(&rconProtocol, "rcon-protocol", envOr("GAMEPLANE_RCON_PROTOCOL", "source"),
		"RCON wire protocol: source (Valve/Minecraft packet framing), telnet (line-based, e.g. 7 Days to Die), "+
			"websocket (Rust WebRcon), battleye (BattlEye RCon, e.g. DayZ/Arma), satisfactory (Satisfactory "+
			"Dedicated Server's HTTPS function-call API), palworld (Palworld Dedicated Server's REST admin API, "+
			"replacing its deprecated source RCON), nuclearoption (Nuclear Option remote-command protocol), "+
			"rest (generic HTTP/JSON admin API, e.g. FiveM txAdmin or Farming Simulator 25), or cli (container stdin/PTY). "+
			"Unset or unrecognized falls back to source for back-compat.")
	flag.StringVar(&cliPipe, "cli-pipe", envOr("GAMEPLANE_CLI_PIPE", "/var/run/gameplane/console.pipe"),
		"path to FIFO stdin pipe for CLI console protocol")
	flag.StringVar(&gameLogPath, "game-log-path", "", "path to the game container's log file (for /logs/tail)")
	flag.StringVar(&certFile, "tls-cert", "", "server TLS cert (PEM). Enables HTTPS + requires client cert")
	flag.StringVar(&keyFile, "tls-key", "", "server TLS key (PEM)")
	flag.StringVar(&clientCAFile, "tls-client-ca", "", "CA that signs API client certs")
	flag.StringVar(&apiTokenFile, "api-token-file", "", "fallback shared-secret auth (used when TLS is not configured)")
	flag.StringVar(&serverName, "server-name", envOr("GAMEPLANE_SERVER_NAME", ""), "owning GameServer name")
	flag.StringVar(&serverUID, "server-uid", envOr("GAMEPLANE_SERVER_UID", ""), "immutable owning GameServer UID for versioned routes")
	flag.StringVar(&templateName, "template", envOr("GAMEPLANE_TEMPLATE", ""), "GameTemplate name")
	flag.StringVar(&gameName, "game", envOr("GAMEPLANE_GAME", ""), "game identifier")
	flag.StringVar(&capsJSON, "capabilities", envOr("GAMEPLANE_CAPABILITIES", ""),
		"declared game capabilities (JSON, from GameTemplate spec.capabilities)")
	flag.StringVar(&logLevel, "log-level", envOr("GAMEPLANE_LOG_LEVEL", "info"),
		"log verbosity: debug, info, warn, or error")
	flag.Parse()

	logger := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: parseLogLevel(logLevel)}))
	slog.SetDefault(logger)

	if err := validateTLSFlags(certFile, keyFile); err != nil {
		logger.Error("tls flags", "err", err)
		os.Exit(1)
	}

	authCheck, err := auth.New(auth.Config{
		ClientCAFile: clientCAFile,
		TokenFile:    apiTokenFile,
	})
	if err != nil {
		logger.Error("auth setup", "err", err)
		os.Exit(1)
	}

	// Declared capabilities drive players/quiesce. A malformed blob
	// degrades to "nothing declared" (built-in fallbacks for known
	// games) rather than crashing the sidecar.
	capSpec, err := caps.Parse(capsJSON)
	if err != nil {
		logger.Warn("capabilities ignored", "err", err)
		capSpec = nil
	}
	var playerActions *caps.PlayerActions
	var quiesceSpec *caps.Quiesce
	var lifecycleSpec *caps.Lifecycle
	var actionSpecs []caps.ServerAction
	var statusSpec *caps.Status
	var modsSpec *caps.Mods
	if capSpec != nil {
		playerActions = capSpec.Players
		quiesceSpec = capSpec.Quiesce
		lifecycleSpec = capSpec.Lifecycle
		actionSpecs = capSpec.Actions
		statusSpec = capSpec.Status
		modsSpec = capSpec.Mods
	}

	var rconClient interface {
		Exec(cmd string) (string, error)
	}
	switch {
	case !rconEnabled:
		// The game declares no RCON (e.g. consoleMode pty/none). Dialing
		// 127.0.0.1:25575 would just fail on every request; the Disabled
		// client makes that explicit so handlers degrade cleanly.
		rconClient = rcon.Disabled{}
	case strings.EqualFold(rconProtocol, "telnet"):
		rconClient = rcon.NewTelnet(rconHost, rconPort, rcon.PasswordFromFile(rconPassFile))
	case strings.EqualFold(rconProtocol, "websocket"):
		rconClient = rcon.NewWebSocket(rconHost, rconPort, rcon.PasswordFromFile(rconPassFile))
	case strings.EqualFold(rconProtocol, "battleye"):
		rconClient = rcon.NewBattlEye(rconHost, rconPort, rcon.PasswordFromFile(rconPassFile))
	case strings.EqualFold(rconProtocol, "satisfactory"):
		rconClient = rcon.NewSatisfactory(rconHost, rconPort, rcon.PasswordFromFile(rconPassFile))
	case strings.EqualFold(rconProtocol, "palworld"):
		rconClient = rcon.NewPalworld(rconHost, rconPort, rcon.PasswordFromFile(rconPassFile))
	case strings.EqualFold(rconProtocol, "nuclearoption"):
		rconClient = rcon.NewNuclearOption(rconHost, rconPort, rcon.PasswordFromFile(rconPassFile))
	case strings.EqualFold(rconProtocol, "rest"):
		rconClient = rcon.NewREST(rconHost, rconPort, rcon.PasswordFromFile(rconPassFile), rcon.WithGame(gameName))
	case strings.EqualFold(rconProtocol, "cli"):
		rconClient = rcon.NewCLI(rconHost, rconPort, rcon.PasswordFromFile(rconPassFile), rcon.WithPipePath(cliPipe))
	default:
		// "source", empty, or anything unrecognized: back-compat default.
		rconClient = rcon.New(rconHost, rconPort, rcon.PasswordFromFile(rconPassFile))
	}

	r := chi.NewRouter()
	r.Use(middleware.RequestID, middleware.Recoverer)

	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	})
	// Prometheus metrics are served by the separate plain listener below,
	// never on this mTLS control mux: Prometheus has no agent client cert
	// and isn't meant to be trusted with the console/files/RCON routes that
	// cert would also unlock.

	mountProtected := func(protected chi.Router) {

		// Console/Logs open long-lived WebSockets and Mods installs can run
		// well past a short request deadline (a large download bounded by
		// its own HTTP client timeout, not the router's). None of these are
		// ordinary bounded REST calls, so they're mounted without
		// middleware.Timeout (F-103): applying a request-scoped deadline to
		// them force-closes the socket / aborts the download every time the
		// deadline elapses, regardless of whether the stream is still healthy.
		logs.Mount(protected, dataRoot, gameLogPath)
		console.Mount(protected, rconClient)
		mods.Mount(protected, dataRoot, modsSpec)
		files.Mount(protected, dataRoot)

		protected.Group(func(bounded chi.Router) {
			bounded.Use(middleware.Timeout(30 * time.Second))
			players.Mount(bounded, rconClient, gameName, playerActions)
			quiesce.Mount(bounded, rconClient, gameName, quiesceSpec)
			lifecycle.Mount(bounded, rconClient, gameName, lifecycleSpec)
			actions.Mount(bounded, rconClient, gameName, actionSpecs)
			status.Mount(bounded, rconClient, statusSpec)
		})
	}
	r.Group(func(protected chi.Router) {
		protected.Use(authCheck.Middleware)
		mountProtected(protected)
	})
	r.Route("/v1/targets/{uid}", func(protected chi.Router) {
		protected.Use(authCheck.Middleware, target.Guard(serverUID))
		mountProtected(protected)
	})

	srv := &http.Server{
		Addr:              addr,
		Handler:           r,
		ReadHeaderTimeout: 10 * time.Second,
	}
	if certFile != "" && keyFile != "" {
		tlsCfg, err := auth.ServerTLS(certFile, keyFile, clientCAFile)
		if err != nil {
			logger.Error("tls setup", "err", err)
			os.Exit(1)
		}
		srv.TLSConfig = tlsCfg
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	// Prometheus metrics for per-server resource usage and player counts.
	// Registered with the default registry so promhttp.Handler() picks it up.
	m := metrics.New()

	// Resource usage: in proc mode (the operator shares the pod's PID
	// namespace and sets GAMEPLANE_USAGE_PROC) the agent reports the game
	// process's CPU/memory from /proc and uses the operator-supplied limits
	// as the denominator; otherwise it falls back to its own cgroup.
	go heartbeat.Run(ctx, heartbeat.Config{
		ServerName: serverName,
		Template:   templateName,
		Game:       gameName,
		Version:    Version,
		RCON:       rconClient,
		Interval:   20 * time.Second,
		PlayerList: func() *caps.PlayerList {
			if playerActions != nil {
				return playerActions.List
			}
			return nil
		}(),
		Usage: usage.New(usage.Config{
			DataDir:            dataRoot,
			ProcMode:           envOr("GAMEPLANE_USAGE_PROC", "") == "1",
			CPULimitMillicores: envInt("GAMEPLANE_CPU_LIMIT_MILLICORES"),
			MemLimitBytes:      envInt("GAMEPLANE_MEM_LIMIT_BYTES"),
		}),
		Metrics: m,
	})

	go func() {
		logger.Info("agent listening", "addr", addr, "tls", srv.TLSConfig != nil, "version", Version)
		var err error
		if srv.TLSConfig != nil {
			err = srv.ListenAndServeTLS("", "")
		} else {
			err = srv.ListenAndServe()
		}
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("listen", "err", err)
			os.Exit(1)
		}
	}()

	// Prometheus metrics get their own plain (no TLS, no auth) listener, so
	// a scraper never needs the mTLS client cert that unlocks the control
	// mux above.
	var metricsSrv *http.Server
	if metricsAddr != "" {
		if metricsAddr == addr {
			logger.Error("--metrics-addr must differ from --addr", "addr", addr)
			os.Exit(1)
		}
		metricsSrv = newMetricsServer(metricsAddr)
		go func() {
			logger.Info("metrics listening", "addr", metricsAddr)
			// Unlike the control listener above, a failure here (most often
			// the game container's own process binding this port first: the
			// agent shares the pod's network namespace with it, and this
			// listener's port is only ever configured by the operator, not
			// by the game template) must not exit the process — metrics are
			// optional, and taking down the mTLS control plane over them
			// would break console/files/RCON for a game that merely happens
			// to use this TCP port for something else.
			if err := metricsSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				logger.Error("metrics listen: Prometheus metrics will be unavailable", "err", err)
			}
		}()
	}

	<-ctx.Done()
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()
	_ = srv.Shutdown(shutdownCtx)
	if metricsSrv != nil {
		_ = metricsSrv.Shutdown(shutdownCtx)
	}
}

// errTLSFlagsUnpaired reports that only one of --tls-cert / --tls-key was set.
var errTLSFlagsUnpaired = errors.New("--tls-cert and --tls-key must both be set or both be empty")

// validateTLSFlags rejects a half-configured TLS setup: with only one of the
// cert/key pair set the agent would otherwise silently start in plain HTTP.
func validateTLSFlags(cert, key string) error {
	if (cert != "") != (key != "") {
		return errTLSFlagsUnpaired
	}
	return nil
}

// envOr returns the environment variable value for key if set, or fallback otherwise.
func envOr(key, fallback string) string {
	if v, ok := os.LookupEnv(key); ok {
		return v
	}
	return fallback
}

// parseLogLevel maps a --log-level value to a slog.Level. Unknown values
// degrade to info — a typo in a manifest shouldn't crash the sidecar.
func parseLogLevel(s string) slog.Level {
	switch strings.ToLower(s) {
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// envInt parses an integer env var, returning 0 when unset or unparseable.
func envInt(key string) int64 {
	if v, err := strconv.ParseInt(os.Getenv(key), 10, 64); err == nil {
		return v
	}
	return 0
}
