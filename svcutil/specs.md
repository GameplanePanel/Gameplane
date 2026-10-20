# svcutil — Specification

**Status:** Stable  
**Module / command:** `github.com/GameplanePanel/gameplane/svcutil`  
**Dependencies:** stdlib only (Go 1.26+)

## Purpose

Shared stdlib-only service helpers for environment parsing and graceful HTTP server shutdown, intended to reduce code duplication and enforce consistent startup and shutdown behavior across the Go binaries in this repo. All functions degrade gracefully on invalid inputs; no startup crashes from bad configuration.

**Adoption status:** `svcutil` has no active consumers yet. No binary in the workspace currently imports it; `operator`, `api`, `agent`, `audit-syslog-bridge`, and `telemetry-receiver` each keep their own local `envOr`/`parseLogLevel`-style helpers. `capture-sidecar` does not import it either — `RunHTTP` only serves plain HTTP (`ListenAndServe`), while the sidecar's control server needs a TLS listener with client-certificate verification (see `capture-sidecar/cmd/main.go:80-86`).

## Responsibilities

- Parse environment variables with fallback semantics, distinguishing unset from empty-but-set.
- Parse log-level strings to `slog.Level` enums with case-insensitive recognition and graceful default on unknown values.
- Parse integer-valued environment variables with non-crashing fallback to defaults.
- Start HTTP servers and manage graceful shutdown on context cancellation with bounded timeout.
- Map shutdown success (`http.ErrServerClosed`) to nil so callers see clean completion.

## Non-goals / boundaries

- Does not provide configuration file parsing or command-line flag parsing; only environment-variable inputs.
- Does not enforce configuration schema or validation; callers own business-logic constraints.
- Does not handle signal forwarding; callers that need signal → context.Done bridging use `signal.NotifyContext`.
- Does not manage connection pooling or middleware; `RunHTTP` runs a stock `http.Server` as-is.

## Directory & package layout

```
svcutil/
├── env.go             # Or, OrInt, ParseLogLevel environment helpers
├── env_test.go        # Unit tests for environment parsing (21 subtests)
├── server.go          # RunHTTP graceful shutdown helper
├── server_test.go     # Unit tests for server lifecycle (5 functions)
├── go.mod             # Module declaration (stdlib-only)
└── .testcoverage.yml  # 90% coverage gate
```

Single package; no subdirectories or internal structure.

## External interface / contracts

### Exported Functions

| Function | Signature | Behavior |
|---|---|---|
| **`Or`** | `Or(key, fallback string) string` | Returns environment variable `key` if set (via `os.LookupEnv`); otherwise returns `fallback`. Distinguishes unset from empty-but-set: empty string in env returns `""`, not `fallback`. |
| **`OrInt`** | `OrInt(key string, def int) int` | Parses environment variable `key` as int via `strconv.Atoi`; returns `def` if unset or unparseable. Non-crashing: invalid values degrade to default rather than panicking. |
| **`ParseLogLevel`** | `ParseLogLevel(s string) slog.Level` | Maps case-insensitive log-level string to `slog.Level`. Recognized case-insensitively: debug, warn, error; anything else (including "info" or empty) defaults to `slog.LevelInfo`. Non-crashing fallback semantics. |
| **`RunHTTP`** | `RunHTTP(ctx context.Context, srv *http.Server, shutdownTimeout time.Duration) error` | Starts HTTP server; on context cancellation, performs graceful shutdown with deadline bounded by `shutdownTimeout`. Maps `http.ErrServerClosed` to `nil`. Returns listen error immediately or shutdown result on cancellation. |

## Key invariants

1. **Graceful fallback.** `OrInt` and `ParseLogLevel` never panic on invalid input; they degrade to sensible defaults (`def` for OrInt, `slog.LevelInfo` for ParseLogLevel). This is a guarantee: invalid manifest values must not crash the service at startup.

2. **Empty-string semantics.** `Or` distinguishes unset (`os.LookupEnv` returns `ok=false`) from empty-but-set (`os.LookupEnv` returns `v=""` with `ok=true`). Unset uses fallback; empty-but-set returns empty string (no fallback). This preserves user intent when an env var is intentionally cleared.

3. **Bounded shutdown.** `RunHTTP` calls `srv.Shutdown` with a deadline-bounded context derived from `shutdownTimeout`. If the timeout expires before all connections close, `srv.Shutdown` returns with a deadline-exceeded error and `RunHTTP` exits promptly. However, in-flight handlers continue running until they naturally complete or the process exits; the timeout bounds only how long `RunHTTP` waits, not handler execution. This prevents indefinite hangs in `RunHTTP` itself while preserving handler cleanup.

4. **Success mapping.** `RunHTTP` explicitly maps `http.ErrServerClosed` (the success path from `srv.Shutdown`) to `nil`, so callers see nil on clean shutdown and a non-nil error only on failure.

5. **Context propagation.** `RunHTTP` selects on both listen errors (returned immediately) and context cancellation (triggers shutdown). If the context is pre-cancelled or becomes cancelled before the server listens, shutdown proceeds normally.

## Dependencies

**Internal:** None.  
**External:** stdlib only (`context`, `errors`, `log/slog`, `net/http`, `os`, `strconv`, `strings`, `time`). No external modules in `go.mod`.

## Security considerations

- **No secrets in env vars.** This package reads environment variables for configuration only; credentials or sensitive values must be injected via files or mounts, not environment variables (which are visible in process listings and pod describe).
- **Graceful defaults prevent misconfiguration crashes.** Services that fail to start due to invalid log levels or port numbers in the environment are difficult to debug in containerized environments. Non-crashing fallbacks reduce incident severity (service starts, logs at info level, operator can adjust).
- **Bounded shutdown prevents hanging.** A service hung in shutdown — with `RunHTTP` waiting indefinitely for in-flight requests to close — can leave resources locked or cause deployment failures. `shutdownTimeout` bounds how long `RunHTTP` waits; Kubernetes `terminationGracePeriodSeconds` (usually 30 seconds) provides the outermost bound. Handlers themselves are not force-terminated by this timeout and may continue running after `RunHTTP` returns.

## Testing & coverage

**Test structure:**
- Unit tests in `env_test.go` cover `Or`, `OrInt`, `ParseLogLevel` with table-driven cases for set/unset/invalid/empty/default scenarios.
- Unit tests in `server_test.go` cover `RunHTTP` lifecycle:
  - `TestRunHTTPCleanShutdown`: clean context cancellation and graceful shutdown.
  - `TestRunHTTPListenError`: immediate return on listen errors.
  - `TestRunHTTPContextCancelledBeforeListen`: pre-cancelled context triggers shutdown.
  - `TestRunHTTPServerClosedError`: verification that `http.ErrServerClosed` is not returned to callers.
  - `TestRunHTTPShutdownTimeout`: confirms `RunHTTP` returns promptly after context cancellation with a short `shutdownTimeout`; it does not hold a request open across shutdown, so it does not exercise the timeout actually cutting off a slow handler.

**Coverage gate:** 90% (`.testcoverage.yml`, total threshold).

**Uncovered paths:** The small 10% gap accounts for the following shutdown edge case:
- Shutdown returning context.DeadlineExceeded because a handler is still active past the deadline: no test currently holds a request open past the timeout to verify that `RunHTTP` returns the deadline error while the handler continues running. This path is difficult to test reliably without introducing flaky timing assumptions.

These edge cases are acceptable because:
1. They occur only during service shutdown/restart, not during steady-state operation.
2. The outer Kubernetes `terminationGracePeriodSeconds` ensures eventual termination even if shutdown races occur.
3. Triggering them in a test would require flaky timing assumptions or mocking system calls.

**Key test cases:**
- **Environment parsing:** Unset, set, empty, invalid values; fallback semantics; case-insensitivity.
- **Server lifecycle:** Clean shutdown, listen error, pre-cancelled context, shutdown timeout, ErrServerClosed mapping.

## References

- **Consumers:** None currently. `svcutil` is linked into the workspace (`go.work`) but no binary imports it yet; see "Adoption status" above.
- **Architecture:** `docs/architecture.md` does not currently describe `svcutil`; there is no dedicated section to cross-reference.
- **Go workspace:** `go.work` — svcutil is listed as a workspace module so it builds and vets alongside the other components, independent of whether anything imports it.
