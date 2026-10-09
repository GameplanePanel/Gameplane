package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"net/netip"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/GameplanePanel/gameplane/api/internal/audit"
	"github.com/GameplanePanel/gameplane/api/internal/auth"
	"github.com/GameplanePanel/gameplane/api/internal/db"
	"github.com/GameplanePanel/gameplane/api/internal/handlers"
	"github.com/GameplanePanel/gameplane/api/internal/kube"
	"github.com/GameplanePanel/gameplane/api/internal/notify"
	"github.com/GameplanePanel/gameplane/api/internal/rbac"
	"github.com/GameplanePanel/gameplane/api/internal/registry"
	"github.com/GameplanePanel/gameplane/api/internal/telemetry"
	"github.com/GameplanePanel/gameplane/api/internal/ws"
)

var Version = "dev"

// parseTrustedProxyPrefix parses a CIDR string and normalizes IPv4-mapped IPv6
// prefixes. If a prefix is IPv4-mapped (e.g., ::ffff:10.0.0.0/112):
// - If Bits() < 96: returns an error (IPv4-mapped prefix too short to contain full IPv4)
// - If Bits() >= 96: returns the unmapped IPv4 prefix (e.g., 10.0.0.0/16 for ::ffff:10.0.0.0/112)
// Plain IPv4 and IPv6 prefixes are returned unchanged.
func parseTrustedProxyPrefix(cidr string) (netip.Prefix, error) {
	prefix, err := netip.ParsePrefix(cidr)
	if err != nil {
		return netip.Prefix{}, err
	}

	if prefix.Addr().Is4In6() {
		if prefix.Bits() < 96 {
			return netip.Prefix{}, fmt.Errorf("IPv4-mapped prefix %q has less than 96 bits; unmapped IPv4 range would be incomplete", cidr)
		}
		return netip.PrefixFrom(prefix.Addr().Unmap(), prefix.Bits()-96), nil
	}

	return prefix, nil
}

func main() {
	// Level from the environment first so the subcommand dispatch and
	// bootstrap-admin honor it; the serve path rebuilds the logger from
	// the --log-level flag once it has parsed its flags.
	logger := newLogger(envOr("GAMEPLANE_LOG_LEVEL", "info"))
	slog.SetDefault(logger)
	applyMemoryLimit(logger)

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	// Subcommand dispatch. The legacy entrypoint (no arg / leading flag)
	// remains the serve path so existing manifests don't break.
	args := os.Args[1:]
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		switch args[0] {
		case "serve":
			args = args[1:]
		case "gateway":
			if err := runGateway(ctx, args[1:]); err != nil {
				logger.Error("gateway", "err", err)
				os.Exit(1)
			}
			return
		case "bootstrap-admin":
			if err := bootstrapAdmin(ctx, args[1:], os.Stdin, os.Stderr); err != nil {
				logger.Error("bootstrap-admin", "err", err)
				os.Exit(1)
			}
			return
		case "rotate-panel-key":
			if err := rotatePanelKey(ctx, args[1:]); err != nil {
				logger.Error("rotate-panel-key", "err", err)
				os.Exit(1)
			}
			logger.Info("panel key rotation completed; restart the API with the new key file")
			return
		default:
			logger.Error("unknown subcommand", "name", args[0])
			os.Exit(2)
		}
	}

	var cfg config
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	cfg.bindFlags(fs)
	if err := fs.Parse(args); err != nil {
		os.Exit(2)
	}
	logger = newLogger(cfg.logLevel)
	slog.SetDefault(logger)

	// Validate OIDC configuration flags.
	if cfg.oidcDefaultRole != "" {
		validRoles := map[string]bool{
			"viewer":   true,
			"operator": true,
			"admin":    true,
			"deny":     true,
		}
		if !validRoles[cfg.oidcDefaultRole] {
			msg := fmt.Sprintf("invalid --oidc-default-role %q (accepted: '', 'viewer', 'operator', 'admin', 'deny')", cfg.oidcDefaultRole)
			logger.Error(msg)
			os.Exit(1)
		}
	}

	// Validate and parse OIDC role mapping lists.
	var err error
	cfg.oidcRoleMappingAdminParsed, err = parseRoleMapping(cfg.oidcRoleMappingAdmin)
	if err != nil {
		msg := fmt.Sprintf("invalid --oidc-role-mapping-admin: %v", err)
		logger.Error(msg)
		os.Exit(1)
	}
	cfg.oidcRoleMappingOperatorParsed, err = parseRoleMapping(cfg.oidcRoleMappingOperator)
	if err != nil {
		msg := fmt.Sprintf("invalid --oidc-role-mapping-operator: %v", err)
		logger.Error(msg)
		os.Exit(1)
	}
	cfg.oidcRoleMappingViewerParsed, err = parseRoleMapping(cfg.oidcRoleMappingViewer)
	if err != nil {
		msg := fmt.Sprintf("invalid --oidc-role-mapping-viewer: %v", err)
		logger.Error(msg)
		os.Exit(1)
	}

	// Validate and trim trusted proxies CIDR list.
	validProxies := []string{}
	trustedPrefixes := []netip.Prefix{}
	for _, p := range cfg.trustedProxies {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		prefix, perr := parseTrustedProxyPrefix(p)
		if perr != nil {
			logger.Error("invalid trusted proxy CIDR", "cidr", p, "err", perr)
			os.Exit(1)
		}
		validProxies = append(validProxies, p)
		trustedPrefixes = append(trustedPrefixes, prefix)
	}
	cfg.trustedProxies = validProxies

	// Resolve where telemetry goes once, now: a bad default endpoint or an
	// interval below one minute is a configuration error, not something to
	// discover at the first report.
	if cfg.telemetryInterval < time.Minute {
		logger.Error("invalid --telemetry-interval (minimum 1m)", "value", cfg.telemetryInterval.String())
		os.Exit(1)
	}
	telemetryDest, err := telemetry.ResolveDestination(cfg.telemetryDisabled, cfg.telemetryEndpoint, cfg.telemetryBundled)
	if err != nil {
		logger.Error("resolve telemetry destination", "err", err)
		os.Exit(1)
	}
	logger.Info("telemetry destination", "kind", telemetryDest.Kind, "host", telemetryDest.Host)

	store, err := db.Open(ctx, cfg.dbDriver, cfg.dbDSN)
	if err != nil {
		logger.Error("open db", "err", err)
		os.Exit(1)
	}
	defer func() { _ = store.Close() }()
	if err := store.Migrate(ctx); err != nil {
		logger.Error("migrate", "err", err)
		os.Exit(1)
	}

	k8s, reg, err := managementClients(ctx, cfg, store)
	if err != nil {
		logger.Error("initialize management storage", "err", err)
		os.Exit(1)
	}

	sessions := auth.NewSessionStore(store)
	sessions.StartGC(ctx, time.Hour)
	localAuth := auth.NewLocal(store)
	// The Helm-flag OIDC provider (if any) is built once at startup; the
	// registry lists it as the read-only "helm" provider next to the
	// dashboard-managed providers it resolves live from the auth config.
	// Build the Helm-seeded role mapping policy from the parsed CLI flags.
	var helmPolicy *auth.ProviderPolicy
	if cfg.oidcIssuer != "" {
		// Only create RoleMappings if at least one role list is non-empty;
		// nil RoleMappings disables mapping entirely (SC-008 backward compat).
		var roleMappings *auth.RoleMappings
		if len(cfg.oidcRoleMappingAdminParsed) > 0 || len(cfg.oidcRoleMappingOperatorParsed) > 0 || len(cfg.oidcRoleMappingViewerParsed) > 0 {
			roleMappings = &auth.RoleMappings{
				Admin:    cfg.oidcRoleMappingAdminParsed,
				Operator: cfg.oidcRoleMappingOperatorParsed,
				Viewer:   cfg.oidcRoleMappingViewerParsed,
			}
		}
		helmPolicy = &auth.ProviderPolicy{
			GroupsClaim:  cfg.oidcGroupsClaim,
			DefaultRole:  cfg.oidcDefaultRole,
			RoleMappings: roleMappings,
		}
	}
	oidcAuth, err := auth.NewOIDCWithPolicy(ctx, cfg.oidcIssuer, cfg.oidcClientID, cfg.oidcClientSecret, cfg.oidcRedirectURL, helmPolicy)
	if err != nil && cfg.oidcIssuer != "" {
		logger.Warn("oidc disabled", "err", err)
	}
	authRegistry := auth.NewRegistry(store,
		auth.NewK8sSecretReader(k8s, cfg.namespace), oidcAuth, cfg.oidcDisplayName, helmPolicy)

	var auditOpts []audit.Option
	var webhookDone <-chan struct{}
	var s3Done <-chan struct{}
	if cfg.auditStdout {
		auditOpts = append(auditOpts, audit.WithStdoutSink(logger))
	}
	if cfg.auditWebhookURL != "" {
		webhook := audit.NewWebhookSink(cfg.auditWebhookURL, cfg.auditWebhookAuth)
		done := make(chan struct{})
		go func() { webhook.Start(ctx); close(done) }()
		webhookDone = done
		auditOpts = append(auditOpts, audit.WithWebhookSink(webhook))
		logger.Info("audit webhook sink enabled", "url", cfg.auditWebhookURL)
	}
	if cfg.auditS3Endpoint != "" && cfg.auditS3Bucket != "" {
		s3Sink, err := audit.NewS3Sink(audit.S3Config{
			Endpoint:  cfg.auditS3Endpoint,
			Bucket:    cfg.auditS3Bucket,
			Prefix:    cfg.auditS3Prefix,
			Region:    cfg.auditS3Region,
			Insecure:  cfg.auditS3Insecure,
			AccessKey: cfg.auditS3AccessKey,
			SecretKey: cfg.auditS3SecretKey,
		})
		if err != nil {
			logger.Warn("audit s3 sink init failed", "err", err)
		} else {
			done := make(chan struct{})
			go func() { s3Sink.Start(ctx); close(done) }()
			s3Done = done
			auditOpts = append(auditOpts, audit.WithS3Sink(s3Sink))
			logger.Info("audit s3 sink enabled", "endpoint", cfg.auditS3Endpoint, "bucket", cfg.auditS3Bucket)
		}
	}
	auditor := audit.New(store, auditOpts...)
	// Wire every OIDC provider to the auditor for role-assignment audit events (FR-014):
	// the Helm-flag provider directly, and the dashboard-managed providers through the
	// registry, which attaches the func to each provider it builds.
	// auth must not import audit (audit imports auth), so the dependency is inverted here
	// via the concrete auditor's WriteSync method value.
	if oidcAuth != nil {
		oidcAuth.AttachAuditWriteSyncFunc(auditor.WriteSync)
		oidcAuth.SetProviderName(auth.HelmProviderName)
	}
	authRegistry.AttachAuditWriteSyncFunc(auditor.WriteSync)

	// Notification delivery: watch CRD status transitions (server health,
	// backup/restore outcomes) and push matching events to the sinks
	// configured in Admin Settings. Best-effort mirror — never gates
	// reconciliation or requests.
	notifier := notify.New(store, k8s, cfg.namespace)
	go notifier.RunWithRegistry(ctx, reg)

	// Cluster registry: watch Cluster CRDs and populate the client registry
	// with remote cluster connections. Single-cluster installs only have the
	// "local" cluster (the control plane); this watcher loads remote clusters
	// from kubeconfigs stored as Secrets.
	go kube.WatchClusters(ctx, k8s, reg, cfg.namespace)

	r := chi.NewRouter()
	r.Use(middleware.RequestID, middleware.Recoverer)
	// Client IP middleware runs before rate limiting and audit so the determined
	// IP is used for rate-limit buckets and audit records. X-Forwarded-For is
	// read only when the TCP peer is inside the trusted proxy networks, which
	// come from explicit operator configuration (default: private ranges).
	r.Use(auth.ClientIPFromTrustedProxies(trustedPrefixes))
	r.Use(secureHeaders)
	r.Use(requestTimeout(60 * time.Second))
	r.Use(bodyLimit(1 << 20)) // 1 MiB default; upload proxy raises its own ceiling
	r.Use(audit.Middleware(auditor))

	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	})
	// Prometheus metrics are served by the separate metrics listener below,
	// never on this public router.

	// Public auth routes
	r.Route("/auth", func(r chi.Router) {
		// Pre-auth: which login methods are enabled, resolved through the
		// registry per request so config saves apply without a restart.
		// No version/host/count — login-privacy.
		r.Get("/providers", handlers.AuthProvidersHandler(authRegistry))
		// Rate-limit /login specifically — argon2id is ~200 ms of
		// single-core CPU per attempt, so an unlimited path invites a
		// trivial DoS.
		r.With(auth.LoginLimiter.Middleware).Post("/login", localAuth.HandleLogin(sessions, authRegistry))
		r.Post("/logout", sessions.HandleLogout())
		// Dashboard-managed providers, resolved live per request. The
		// callback triggers an IdP token exchange + DB write per hit; cap
		// it so a misbehaving client (or a redirect loop) can't flood
		// either. Unknown/disabled providers 404 neutrally.
		r.Get("/oidc/{provider}/start", handlers.OIDCStart(authRegistry))
		r.With(auth.OIDCCallbackLimiter.Middleware).
			Get("/oidc/{provider}/callback", handlers.OIDCCallback(authRegistry, sessions))
		// Legacy single-provider paths alias the Helm-flag provider — its
		// redirect URL is registered verbatim at existing IdPs. Registered
		// unconditionally; they 404 neutrally when no helm provider exists.
		r.Get("/oidc/start", handlers.OIDCStartLegacy(authRegistry))
		r.With(auth.OIDCCallbackLimiter.Middleware).
			Get("/oidc/callback", handlers.OIDCCallbackLegacy(authRegistry, sessions))
	})

	// Public share link endpoints (unauthenticated, token-guessable)
	handlers.MountPublicShares(r, reg, store, auth.ShareLimiter)

	// What the telemetry reporter and the admin preview both collect from.
	telemetryDeps := telemetry.Deps{
		Kube:    k8s,
		Store:   store,
		Version: Version,
		Flags: telemetry.Flags{
			CaptureEnabled:       cfg.captureFeatureEnabled,
			OIDCConfigured:       cfg.oidcIssuer != "",
			AuditWebhook:         cfg.auditWebhookURL != "",
			AuditS3:              cfg.auditS3Endpoint != "" && cfg.auditS3Bucket != "",
			DBDriver:             cfg.dbDriver,
			OfficialModuleSource: cfg.officialModuleSource,
		},
	}

	// Protected API
	r.Group(func(p chi.Router) {
		p.Use(sessions.Authenticate)
		// A broad per-IP cap on writes keeps one authenticated client from
		// pegging the DB or k8s API. Reads go through unlimited (the main
		// bottleneck there is the dashboard's own polling).
		p.Use(mutationRateLimit)
		p.Use(rbac.Middleware(reg))

		handlers.MountResources(p, reg, store)
		handlers.MountFleet(p, reg, store)
		handlers.MountNamespaces(p, reg)
		handlers.MountPodEvents(p, reg)
		handlers.MountLifecycle(p, reg)
		handlers.MountShareLinks(p, reg, store)
		handlers.MountOwnership(p, reg, store)
		handlers.MountUsers(p, store, sessions, reg)
		handlers.MountRoles(p, store)
		handlers.MountAudit(p, auditor)
		telemetrySettings := handlers.TelemetrySettings{Dest: telemetryDest, Interval: cfg.telemetryInterval, Deps: telemetryDeps}
		handlers.MountConfigWithTelemetry(p, store, auditor, oidcAuth != nil, cfg.gameDataStorageClass, helmPolicy, telemetrySettings)
		handlers.MountTelemetry(p, store, telemetrySettings)
		handlers.MountNotifications(p, notifier, k8s, cfg.namespace)
		handlers.MountAuthProviderSecrets(p, k8s, cfg.namespace)
		handlers.MountCluster(p, reg, store, Version, cfg.clusterOps, cfg.updateChannel)
		handlers.MountClusterActions(p, k8s, cfg.clusterOps && !cfg.standalone, cfg.clusterExternalAddress)
		handlers.MountClusters(p, reg, k8s, cfg.namespace)
		handlers.MountInstallation(p, cfg.standalone)
		handlers.MountEvents(p, reg)
		handlers.MountDestinations(p, reg)
		handlers.MountTunnelCredentials(p, reg)
		handlers.MountSystemLogs(p, k8s, cfg.namespace)
		handlers.MountModulesWithRegistry(p, reg, cfg.namespace)
		handlers.MountRegistrySecrets(p, k8s, cfg.namespace)
		// Precedence: a runtime, admin-configured key (DB row + labelled
		// Secret, resolved lazily per registry.DBKeyFunc) always wins over
		// the startup --curseforge-api-key flag, so adding/rotating a key
		// via /admin/registries/{provider}/secret takes effect without a
		// redeploy. The flag is consulted only when no DB key exists, so
		// air-gapped/GitOps installs that never touch admin config keep
		// working exactly as before.
		regSet := registry.NewSet(Version, registry.FallbackKeys(
			registry.DBKeyFunc(store, registry.NewK8sSecretReader(k8s, cfg.namespace)),
			registry.StaticKeys(map[string]string{"curseforge": cfg.curseforgeAPIKey}),
		))
		handlers.MountRegistryWithRegistry(p, reg, regSet)
		// Internal reads use the same selected cluster and credentials as
		// browser-facing agent routes, including remote-only management installs.
		agentLister := ws.NewClusterAgentClient(reg, cfg.namespace, cfg.agentCABundle, cfg.agentClientCert, cfg.agentClientKey)
		handlers.MountModUpdatesWithRegistry(p, reg, regSet, agentLister)
		// ID-managed mods (ARK CurseForge ids, Project Zomboid MOD_IDS,
		// Steam Workshop lists): the API only writes GameServer.spec.mods.ids;
		// the operator projects it into the game's env (rule 10).
		handlers.MountModIDsWithRegistry(p, reg)
		captureConfig := handlers.CaptureConfig{
			FeatureEnabled:          cfg.captureFeatureEnabled,
			GatewayNamespace:        cfg.namespace,
			DefaultRetentionSeconds: cfg.captureDefaultRetentionSecs,
			MaxRetentionSeconds:     cfg.captureMaxRetentionSecs,
			DefaultMaxDurationSecs:  cfg.captureDefaultMaxDurationS,
			DefaultMaxSizeBytes:     cfg.captureDefaultMaxSizeBytes,
		}
		handlers.MountServerCapabilities(p, reg, ws.NewCaptureGatewayClient(reg, cfg.namespace), captureConfig)
		handlers.MountCapture(p, reg, auditor, captureConfig, cfg.agentCABundle, cfg.agentClientCert, cfg.agentClientKey)
		ws.Mount(p, reg, cfg.agentCABundle, cfg.agentClientCert, cfg.agentClientKey, ws.AgentGatewayOptions{Namespace: cfg.namespace})
	})

	// Usage telemetry. Run returns at once when the resolved destination is
	// disabled or none. Otherwise it sends nothing while the admin's basic
	// toggle is off or, on a fresh install, until an admin was shown the
	// notice (spec 022 FR-004); each report slot is claimed in the database.
	go telemetry.New(telemetry.Config{
		Dest:     telemetryDest,
		Interval: cfg.telemetryInterval,
		Auth:     cfg.telemetryAuth,
		Deps:     telemetryDeps,
	}).Run(ctx)

	// Opt-in audit-event retention. Off by default (0 days = keep forever);
	// when set, a daily sweep prunes events past the window so the table
	// doesn't grow unbounded on long-lived installs.
	go auditor.RunRetention(ctx,
		time.Duration(cfg.auditRetentionDays)*24*time.Hour, 24*time.Hour, logger)

	srv := &http.Server{
		Addr:              cfg.addr,
		Handler:           r,
		ReadHeaderTimeout: 10 * time.Second,
		// 64 KiB header ceiling blocks request-smuggling probes and
		// cheap header-flood DoS. Legitimate clients never need more.
		MaxHeaderBytes: 64 << 10,
	}

	go func() {
		logger.Info("api listening", "addr", cfg.addr, "version", Version)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("listen", "err", err)
			os.Exit(1)
		}
	}()

	// Prometheus metrics get their own listener, so the public API port
	// (the one the ingress and the web front end route to) never serves
	// them. The chart's ServiceMonitor scrapes this port in-cluster.
	var metricsSrv *http.Server
	if cfg.metricsAddr != "" {
		if cfg.metricsAddr == cfg.addr {
			logger.Error("--metrics-addr must differ from --addr", "addr", cfg.addr)
			os.Exit(1)
		}
		metricsSrv = newMetricsServer(cfg.metricsAddr)
		go func() {
			logger.Info("metrics listening", "addr", cfg.metricsAddr)
			if err := metricsSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				logger.Error("metrics listen", "err", err)
				os.Exit(1)
			}
		}()
	}

	<-ctx.Done()
	shutCtx, shutCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutCancel()
	_ = srv.Shutdown(shutCtx)
	if metricsSrv != nil {
		_ = metricsSrv.Shutdown(shutCtx)
	}
	// The webhook worker flushes its buffered audit events once ctx is
	// cancelled; wait for that drain (bounded) so a rolling restart doesn't cut
	// off the final events instead of letting them reach the external sink.
	if webhookDone != nil {
		select {
		case <-webhookDone:
		case <-time.After(3 * time.Second):
			logger.Warn("audit webhook drain did not finish before deadline")
		}
	}
	// The S3 worker drains buffered events similarly on ctx cancellation.
	if s3Done != nil {
		select {
		case <-s3Done:
		case <-time.After(3 * time.Second):
			logger.Warn("audit s3 drain did not finish before deadline")
		}
	}
}

type config struct {
	standalone          bool
	panelKeyFile        string
	panelKeyProvisioned bool
	remoteAllowedCIDRs  string
	addr                string
	metricsAddr         string
	dbDriver            string
	dbDSN               string
	logLevel            string

	oidcIssuer                    string
	oidcClientID                  string
	oidcClientSecret              string
	oidcRedirectURL               string
	oidcDisplayName               string
	oidcGroupsClaim               string
	oidcDefaultRole               string
	oidcRoleMappingAdmin          string
	oidcRoleMappingOperator       string
	oidcRoleMappingViewer         string
	oidcRoleMappingAdminParsed    []string
	oidcRoleMappingOperatorParsed []string
	oidcRoleMappingViewerParsed   []string

	telemetryEndpoint      string
	telemetryAuth          string
	telemetryDisabled      bool
	telemetryBundled       bool
	telemetryInterval      time.Duration
	officialModuleSource   string
	clusterOps             bool
	clusterExternalAddress string
	updateChannel          string
	curseforgeAPIKey       string
	auditRetentionDays     int
	auditStdout            bool
	auditWebhookURL        string
	auditWebhookAuth       string
	auditS3Endpoint        string
	auditS3Bucket          string
	auditS3Prefix          string
	auditS3Region          string
	auditS3Insecure        bool
	auditS3AccessKey       string
	auditS3SecretKey       string

	agentCABundle   string
	agentClientCert string
	agentClientKey  string

	captureFeatureEnabled       bool
	captureDefaultRetentionSecs int64
	captureMaxRetentionSecs     int64
	captureDefaultMaxDurationS  int
	captureDefaultMaxSizeBytes  int64

	gameDataStorageClass string

	namespace      string
	trustedProxies []string
}

func (c *config) bindFlags(fs *flag.FlagSet) {
	fs.StringVar(&c.addr, "addr", ":8000", "HTTP listen address")
	fs.StringVar(&c.metricsAddr, "metrics-addr", envOr("GAMEPLANE_METRICS_ADDR", ":9090"),
		"listen address for the Prometheus metrics endpoint, separate from --addr (empty = metrics not served)")
	fs.StringVar(&c.logLevel, "log-level", envOr("GAMEPLANE_LOG_LEVEL", "info"),
		"log verbosity: debug, info, warn, or error")
	fs.StringVar(&c.dbDriver, "db-driver", envOr("GAMEPLANE_DB_DRIVER", "sqlite"), "sqlite or postgres")
	fs.StringVar(&c.dbDSN, "db-dsn", envOr("GAMEPLANE_DB_DSN", "file:/data/gameplane.db?_pragma=journal_mode(WAL)"), "DSN")
	fs.StringVar(&c.oidcIssuer, "oidc-issuer", envOr("GAMEPLANE_OIDC_ISSUER", ""), "OIDC issuer URL")
	fs.StringVar(&c.oidcClientID, "oidc-client-id", envOr("GAMEPLANE_OIDC_CLIENT_ID", ""), "OIDC client id")
	fs.StringVar(&c.oidcClientSecret, "oidc-client-secret", envOr("GAMEPLANE_OIDC_CLIENT_SECRET", ""), "OIDC client secret")
	fs.StringVar(&c.oidcRedirectURL, "oidc-redirect-url", envOr("GAMEPLANE_OIDC_REDIRECT_URL", ""), "OIDC redirect URL")
	fs.StringVar(&c.oidcDisplayName, "oidc-display-name", envOr("GAMEPLANE_OIDC_DISPLAY_NAME", "Single sign-on"), "label for the OIDC login button (no hostname — shown pre-auth)")
	fs.StringVar(&c.oidcGroupsClaim, "oidc-groups-claim", envOr("GAMEPLANE_OIDC_GROUPS_CLAIM", ""),
		"OIDC claim name containing group memberships; empty defaults to \"groups\"")
	fs.StringVar(&c.oidcDefaultRole, "oidc-default-role", envOr("GAMEPLANE_OIDC_DEFAULT_ROLE", ""),
		"Default role for new OIDC users when no group matches: \"viewer\" (default), \"operator\", \"admin\", or \"deny\". Helm-only — not DB-overridable.")
	fs.StringVar(&c.oidcRoleMappingAdmin, "oidc-role-mapping-admin", envOr("GAMEPLANE_OIDC_ROLE_MAPPING_ADMIN", ""),
		"Comma-separated IdP group(s) seeding the admin role")
	fs.StringVar(&c.oidcRoleMappingOperator, "oidc-role-mapping-operator", envOr("GAMEPLANE_OIDC_ROLE_MAPPING_OPERATOR", ""),
		"Comma-separated IdP group(s) seeding the operator role")
	fs.StringVar(&c.oidcRoleMappingViewer, "oidc-role-mapping-viewer", envOr("GAMEPLANE_OIDC_ROLE_MAPPING_VIEWER", ""),
		"Comma-separated IdP group(s) seeding the viewer role")
	fs.StringVar(&c.telemetryEndpoint, "telemetry-endpoint", envOr("GAMEPLANE_TELEMETRY_ENDPOINT", ""), "URL to POST anonymous usage metrics to (empty = the project default)")
	fs.BoolVar(&c.telemetryDisabled, "telemetry-disabled", envOr("GAMEPLANE_TELEMETRY_DISABLED", "") == "true",
		"hard-disable telemetry: nothing is ever sent, whatever the admin toggles say")
	fs.DurationVar(&c.telemetryInterval, "telemetry-interval", envOrDuration("GAMEPLANE_TELEMETRY_INTERVAL", 24*time.Hour),
		"spacing between telemetry reports (minimum 1m; shorter values are for testing only)")
	fs.StringVar(&c.officialModuleSource, "official-module-source", envOr("GAMEPLANE_OFFICIAL_MODULE_SOURCE", ""),
		"name of the official ModuleSource, so telemetry can tell official game modules from custom ones (empty = every module counts as custom)")
	// Set only by the chart when it auto-wires the bundled in-cluster
	// receiver, so it is read from the environment and has no flag.
	c.telemetryBundled = envOr("GAMEPLANE_TELEMETRY_BUNDLED", "") == "true"
	// Like the audit webhook auth, the telemetry ingest token is a
	// credential and only comes from the environment (a mounted Secret),
	// never a flag.
	c.telemetryAuth = envOr("GAMEPLANE_TELEMETRY_AUTH", "")
	fs.BoolVar(&c.clusterOps, "cluster-ops", envOr("GAMEPLANE_CLUSTER_OPS", "") == "true", "enable credential-minting cluster ops (Add node, Download kubeconfig)")
	fs.StringVar(&c.clusterExternalAddress, "cluster-external-address", envOr("GAMEPLANE_CLUSTER_EXTERNAL_ADDRESS", ""),
		"external (node-routable) API server address, e.g. \"1.2.3.4:6443\" or \"https://k8s.example.com:6443\", used in the join command and downloaded kubeconfig instead of the in-cluster ClusterIP; empty = use the in-cluster address (may be unreachable from outside the cluster)")
	fs.StringVar(&c.updateChannel, "update-channel", envOr("GAMEPLANE_UPDATE_CHANNEL", ""), "informational release-channel label shown in the dashboard (mirrors the chart's updates.channel; Gameplane upgrades happen via Helm)")
	fs.StringVar(&c.curseforgeAPIKey, "curseforge-api-key", envOr("GAMEPLANE_CURSEFORGE_API_KEY", ""), "CurseForge API key (enables the CurseForge mod-registry provider; empty = hidden)")
	fs.IntVar(&c.auditRetentionDays, "audit-retention-days", envOrInt("GAMEPLANE_AUDIT_RETENTION_DAYS", 0), "delete audit events older than this many days (0 = keep forever)")
	fs.BoolVar(&c.auditStdout, "audit-stdout", envOr("GAMEPLANE_AUDIT_STDOUT", "") == "true", "also emit each audit event as a structured stdout log line (for external log aggregation)")
	fs.StringVar(&c.auditWebhookURL, "audit-webhook-url", envOr("GAMEPLANE_AUDIT_WEBHOOK_URL", ""), "POST each audit event as JSON to this URL (empty = disabled)")
	// The auth header is a credential, so it comes from the environment (a
	// mounted Secret) only — never a flag, since flag values are visible in the
	// pod spec and `ps`.
	c.auditWebhookAuth = envOr("GAMEPLANE_AUDIT_WEBHOOK_AUTH", "")
	fs.StringVar(&c.auditS3Endpoint, "audit-s3-endpoint", envOr("GAMEPLANE_AUDIT_S3_ENDPOINT", ""), "S3-compatible endpoint host:port (empty = disabled)")
	fs.StringVar(&c.auditS3Bucket, "audit-s3-bucket", envOr("GAMEPLANE_AUDIT_S3_BUCKET", ""), "S3 bucket for audit events (required if endpoint is set)")
	fs.StringVar(&c.auditS3Prefix, "audit-s3-prefix", envOr("GAMEPLANE_AUDIT_S3_PREFIX", ""), "S3 object key prefix (e.g., 'gameplane-audit')")
	fs.StringVar(&c.auditS3Region, "audit-s3-region", envOr("GAMEPLANE_AUDIT_S3_REGION", ""), "S3 region (e.g., 'us-east-1'; defaults to us-east-1 if empty)")
	fs.BoolVar(&c.auditS3Insecure, "audit-s3-insecure", envOr("GAMEPLANE_AUDIT_S3_INSECURE", "") == "true", "use plain HTTP instead of HTTPS for the S3 endpoint (no TLS at all; for local S3-compatible endpoints on dev/homelab clusters)")
	// S3 credentials come from environment only (mounted Secret), never flags.
	c.auditS3AccessKey = envOr("GAMEPLANE_AUDIT_S3_ACCESS_KEY", "")
	c.auditS3SecretKey = envOr("GAMEPLANE_AUDIT_S3_SECRET_KEY", "")
	fs.StringVar(&c.agentCABundle, "agent-ca-bundle", envOr("GAMEPLANE_AGENT_CA", ""), "CA bundle validating agent server certs")
	fs.StringVar(&c.agentClientCert, "agent-client-cert", envOr("GAMEPLANE_AGENT_CLIENT_CERT", ""), "client cert presented to agents")
	fs.StringVar(&c.agentClientKey, "agent-client-key", envOr("GAMEPLANE_AGENT_CLIENT_KEY", ""), "client key presented to agents")
	fs.BoolVar(&c.captureFeatureEnabled, "capture-enabled", envOr("GAMEPLANE_CAPTURE_ENABLED", "") == "true",
		"enable the network capture feature cluster-wide (mirrors the operator's --capture-enabled flag); "+
			"when false, POST /servers/{name}:capture-enable returns 501")
	c.captureDefaultRetentionSecs = envOrInt64("GAMEPLANE_CAPTURE_DEFAULT_RETENTION", 86400)
	c.captureMaxRetentionSecs = envOrInt64("GAMEPLANE_CAPTURE_MAX_RETENTION", 604800)
	fs.IntVar(&c.captureDefaultMaxDurationS, "capture-default-max-duration", envOrInt("GAMEPLANE_CAPTURE_DEFAULT_MAX_DURATION", 300), "default max capture duration in seconds")
	c.captureDefaultMaxSizeBytes = envOrInt64("GAMEPLANE_CAPTURE_DEFAULT_MAX_SIZE", 5368709120)
	fs.BoolVar(&c.standalone, "standalone", envOr("GAMEPLANE_STANDALONE", "") == "true", "run without a local Kubernetes cluster")
	fs.StringVar(&c.panelKeyFile, "panel-key-file", envOr("GAMEPLANE_PANEL_KEY_FILE", "/data/panel.key"), "persistent standalone credential encryption key file")
	fs.BoolVar(&c.panelKeyProvisioned, "panel-key-provisioned", envOr("GAMEPLANE_PANEL_KEY_PROVISIONED", "") == "true", "require an existing read-only standalone credential key; never generate it")
	fs.StringVar(&c.remoteAllowedCIDRs, "remote-allowed-cidrs", envOr("GAMEPLANE_REMOTE_ALLOWED_CIDRS", ""), "optional comma-separated CIDRs allowed for standalone Kubernetes and gateway connections")
	fs.StringVar(&c.namespace, "namespace", envOr("GAMEPLANE_NAMESPACE", "gameplane-system"),
		"namespace the control plane runs in (module upload ConfigMaps are stored here)")
	fs.StringVar(&c.gameDataStorageClass, "game-data-storage-class", envOr("GAMEPLANE_GAME_DATA_STORAGE_CLASS", ""),
		"StorageClass name for game data PVCs (empty = cluster default)")

	// Trusted proxy networks for client IP extraction. Comma-separated CIDRs.
	// Default: loopback + private ranges, which work out-of-the-box for
	// in-cluster ingress. X-Forwarded-For is read only when the TCP peer is
	// inside one of these ranges; any other peer is itself the client (see
	// auth.ClientIPFromTrustedProxies and docs/security.md).
	trustedProxiesStr := envOr("GAMEPLANE_TRUSTED_PROXIES",
		"127.0.0.0/8,10.0.0.0/8,172.16.0.0/12,192.168.0.0/16,169.254.0.0/16,::1/128,fc00::/7,fe80::/10")
	fs.StringVar(&trustedProxiesStr, "trusted-proxies", trustedProxiesStr,
		"comma-separated list of CIDR blocks for trusted reverse proxies; X-Forwarded-For is read only when the TCP peer is in one of these ranges")

	// Parse and validate the trusted proxies list after flags are parsed.
	// This must happen in main() after flag parsing, not here, so we can
	// apply error handling.
	c.trustedProxies = strings.Split(trustedProxiesStr, ",")
}

func envOr(key, fallback string) string {
	if v, ok := os.LookupEnv(key); ok {
		return v
	}
	return fallback
}

// newLogger builds the process logger at the given level. Unknown values
// degrade to info — a typo in a manifest shouldn't take the API down.
func newLogger(level string) *slog.Logger {
	return slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: parseLogLevel(level)}))
}

// parseLogLevel maps a --log-level value to a slog.Level, defaulting to
// info for unknown values.
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

// envOrInt reads an integer env var, falling back to def when unset or
// unparseable (a malformed value shouldn't crash startup — it degrades to
// the safe default).
func envOrInt(key string, def int) int {
	if v, ok := os.LookupEnv(key); ok {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

// envOrDuration reads a duration env var ("24h", "30m"), falling back to def
// when unset or unparseable, like envOrInt.
func envOrDuration(key string, def time.Duration) time.Duration {
	if v, ok := os.LookupEnv(key); ok {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return def
}

// envOrInt64 reads a 64-bit integer env var, falling back to def when unset or
// unparseable (a malformed value shouldn't crash startup — it degrades to
// the safe default).
func envOrInt64(key string, def int64) int64 {
	if v, ok := os.LookupEnv(key); ok {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			return n
		}
	}
	return def
}

// parseRoleMapping parses a comma-separated list of group names, trimming
// whitespace and rejecting empty entries.
func parseRoleMapping(csv string) ([]string, error) {
	if csv == "" {
		return nil, nil
	}
	out := make([]string, 0, 4)
	for _, g := range strings.Split(csv, ",") {
		g = strings.TrimSpace(g)
		if g == "" {
			return nil, errors.New("empty entry detected (check for consecutive commas or leading/trailing commas)")
		}
		out = append(out, g)
	}
	return out, nil
}

// bodyLimit wraps every request body in MaxBytesReader so a decoder
// that forgets its own cap still can't exhaust memory. The upload proxy
// path (/servers/*/files/upload) is exempt because it streams multi-MiB
// files to the agent — MaxBytesReader is *authoritative*: once wrapped
// at 1 MiB, re-wrapping at 64 MiB downstream doesn't raise the ceiling.
func bodyLimit(maxBytes int64) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			if req.Body != nil && !isUploadPath(req.URL.Path) {
				req.Body = http.MaxBytesReader(w, req.Body, maxBytes)
			}
			next.ServeHTTP(w, req)
		})
	}
}

// isUploadPath matches the routes that legitimately accept large bodies.
// Keep the list tight — anything else should be capped by bodyLimit.
// files/write and mods/upload must be exempt too: bodyLimit's
// MaxBytesReader is authoritative (main.go's bodyLimit doc comment), so
// leaving them capped at 1 MiB here makes the proxy's own 64 MiB
// (httpProxy default, files/write) and 512 MiB (mods/upload) ceilings
// dead code — see F-075.
func isUploadPath(path string) bool {
	if !strings.HasPrefix(path, "/servers/") {
		return false
	}
	// /servers/{name}/files/upload, /servers/{name}/files/write,
	// /servers/{name}/mods/upload
	return strings.HasSuffix(path, "/files/upload") ||
		strings.HasSuffix(path, "/files/write") ||
		strings.HasSuffix(path, "/mods/upload")
}

// secureHeaders sets hardening response headers on every API reply. The
// API only returns JSON, never HTML, so the CSP is locked down to
// "nothing renders" — if a bug ever caused an HTML reply to slip out,
// the browser would refuse to execute scripts or open iframes.
//
// Frame-ancestors 'none' is the modern replacement for X-Frame-Options;
// we set both for older browsers. HSTS is set with a long max-age plus
// includeSubDomains so the dashboard origin and any sub-origins stick
// to HTTPS on repeat visits.
func secureHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		h := w.Header()
		h.Set("Strict-Transport-Security", "max-age=63072000; includeSubDomains")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Permissions-Policy", "geolocation=(), microphone=(), camera=()")
		h.Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'; base-uri 'none'")
		next.ServeHTTP(w, req)
	})
}

// mutationRateLimit applies MutationLimiter to POST/PUT/PATCH/DELETE only.
// Authenticated reads are unrestricted (the dashboard polls at 5s on
// several tabs, which would burn through any reasonable bucket).
func mutationRateLimit(next http.Handler) http.Handler {
	mw := auth.MutationLimiter.Middleware(next)
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		switch req.Method {
		case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
			mw.ServeHTTP(w, req)
		default:
			next.ServeHTTP(w, req)
		}
	})
}

// requestTimeout wraps chi's middleware.Timeout(d) but exempts streaming
// and large-transfer requests from the deadline. chi.Timeout races the
// handler against a timer and, on expiry, unconditionally calls
// w.WriteHeader(504) in a deferred func — fine for a normal
// request/response route (real DoS protection), but wrong for a
// connection that's *supposed* to stay open past d: the WebSocket routes
// (ws.Mount), the /events SSE feed (handlers.MountEvents), and the
// file/log/capture download and upload proxies (isLargeTransferPath) all
// stream off req.Context() for as long as the transfer takes, so
// chi.Timeout would force-close every one of them at d regardless of
// whether they're still actively serving data (F-074), and then log a
// "superfluous response.WriteHeader call" once the real handler's next
// write lands after the deadline already fired. nginx in front of this
// already gives these routes a much longer read timeout, so the
// app-layer cap is simply the wrong layer for them — they get no
// deadline here at all, same as before chi.Timeout was ever wired in.
func requestTimeout(d time.Duration) func(http.Handler) http.Handler {
	timeoutMW := middleware.Timeout(d)
	return func(next http.Handler) http.Handler {
		timedNext := timeoutMW(next)
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			if isStreamingRequest(req) {
				next.ServeHTTP(w, req)
				return
			}
			timedNext.ServeHTTP(w, req)
		})
	}
}

// isStreamingRequest reports whether req is a long-lived stream that must
// not be cut off by the app-wide request timeout: a WebSocket upgrade
// (every route under ws.Mount, plus the PTY console-pty and pod-log
// routes) or the SSE /events feed (handlers.MountEvents mounts it at
// exactly this GET path).
func isStreamingRequest(req *http.Request) bool {
	if strings.EqualFold(req.Header.Get("Upgrade"), "websocket") {
		return true
	}
	if req.Method == http.MethodGet && req.URL.Path == "/events" {
		return true
	}
	return isLargeTransferPath(req.Method, req.URL.Path)
}

// isLargeTransferPath reports whether req is one of the file/log/capture
// download or upload proxies, or the audit export — routes whose
// duration is bounded by client bandwidth, not app logic, so the 60s
// app-wide DoS timeout is the wrong layer for them (see F-074). These
// share req.Context() with the agent-proxy io.Copy in ws/dialer.go and
// the capture/audit download paths, so cutting the context here
// truncates an in-flight transfer instead of just refusing a slow
// request up front.
func isLargeTransferPath(method, path string) bool {
	if method == http.MethodGet {
		if path == "/admin/audit/export" {
			return true
		}
		if !strings.HasPrefix(path, "/servers/") {
			return false
		}
		return strings.HasSuffix(path, "/files/download") ||
			strings.HasSuffix(path, "/logs/download") ||
			strings.HasSuffix(path, ":capture-file")
	}
	if method == http.MethodPost && strings.HasPrefix(path, "/servers/") {
		// Uploads stream their body on req.Context(). URL installs also
		// wait for the agent's download and extraction, which may exceed 60s.
		return strings.HasSuffix(path, "/files/upload") ||
			strings.HasSuffix(path, "/files/write") ||
			strings.HasSuffix(path, "/mods/upload") ||
			strings.HasSuffix(path, "/mods/install")
	}
	return false
}
