# Layout

Target folder shape for every Go module and for `web/src`, and how far each one
is from it today. Spec 028 (`specs/028-split-big-packages/`) applies it with
behavior-preserving moves; until then, use it for new code and in review. Build
and test commands are in [`AGENTS.md`](../AGENTS.md) and
[`contributing.md`](contributing.md).

## 1. Kinds

Root files allowed in every Go module: `go.mod`, `go.sum`, `specs.md`,
`README.md`, `Dockerfile` (and `Dockerfile.*`), `.testcoverage.yml`.

### Service

A long-running binary (a Deployment, DaemonSet, sidecar or Job image).

```text
<module>/
├── cmd/main.go          thin wiring: flags and env, construct, run
├── internal/<concern>/  all behavior, one package per concern
├── specs.md
├── README.md
└── Dockerfile
```

- `cmd/`: only wiring. A second binary gets `cmd/<binary>/main.go`.
- `internal/<concern>/`: the code, grouped by what it does (`auth`, `console`, `store`, …).
- Tests sit next to the file they test (`x.go` → `x_test.go`).
- No `.go` file at the module root.

### Library

A module other modules import, with no binary of its own.

```text
<module>/
├── <name>.go …          one flat root package named after the module
├── testdata/            fixtures, if any
└── specs.md
```

- One package at the root. A subpackage only for a separately importable concern.
- Tests next to the file they test.

### CLI tool

A binary people run by hand.

```text
<module>/
├── cmd/<binary>/main.go  commands and flag parsing
├── internal/<concern>/   behavior
├── pkg/<name>/           only packages other modules import
└── specs.md
```

- Tests next to the file they test.

### Web app (`web/src`)

```text
web/src/
├── pages/<Screen>.tsx            one file per screen
├── pages/<Screen>/<Section>.tsx  sections of a screen over 500 lines
├── routes/                       route definitions only, no screen code
├── router/                       assembles the route tree
├── components/                   parts used by 2+ screens
├── components/ui/                primitives
├── lib/                          API client, hooks, helpers, i18n
├── styles/
├── test/                         shared test setup
├── api/types.ts                  generated, never edited by hand
└── types.ts                      hand-written shared types
```

- `pages/<Screen>.tsx` holds a screen. When it passes 500 lines it is split into
  section files under `pages/<Screen>/` (e.g. `pages/<Screen>/Footer.tsx`,
  `pages/<Screen>/UpperSection.tsx`) and `pages/<Screen>.tsx` composes them.
- `routes/` only exposes pages: one route-definition file per page; `router/`
  only assembles them (spec 028 OD-7 fixes the file names).
- A part moves to `components/` once two or more screens use it; until then it
  stays with its screen.
- Tests sit next to the file they test (`X.test.tsx`, `X_<topic>.test.tsx`).

## 2. Layout map

Measured on `master` at `54d5819` (2026-10-22) and re-counted on 2026-11-17 for
the file and line numbers below. "Gap today" is `matches` or the move spec 028
makes.

| Module | Kind | Target | Gap today |
| --- | --- | --- | --- |
| `agent` | Service | `cmd/main.go` + `internal/<concern>/` | matches |
| `api` | Service | `cmd/main.go` + `internal/<concern>/` | `cmd/` holds 9 non-test files (bootstrap, gateway, health, main, management, memlimit, metrics, panel_key, workers) → keep wiring in `cmd/`, move behavior to `internal/<concern>/`; second binary `cmd/messages-export/` matches; `internal/handlers/` is one flat package of 160 `.go` files, 51 of them non-test (spec 028 splits it) |
| `audit-syslog-bridge` | Service | `cmd/main.go` + `internal/<concern>/` | 3 files in root `package main` (`main.go` 460 lines) → `cmd/main.go` + `internal/<concern>/`. Lives in the `GameplanePanel/audit-syslog-bridge` submodule. |
| `capture-sidecar` | Service | `cmd/main.go` + `internal/<concern>/` | matches. Lives in the `GameplanePanel/capture-sidecar` submodule. |
| `mcp-server` | Service | `cmd/main.go` + `internal/<concern>/` | root `package main` (`main.go`, `tools.go`, `fixadvice.go`) → `cmd/main.go` + `internal/<concern>/`; `internal/kube/` matches. Lives in the `GameplanePanel/mcp` submodule; the layout is tracked there. |
| `operator` | Service | `cmd/main.go` + `internal/<concern>/` | matches; `internal/controller/` is one flat package of 222 `.go` files, 65 of them non-test (spec 028 splits it); `api/v1alpha1/` see §3 |
| `sentinel` | Service | `cmd/main.go` + `internal/<concern>/` | `main.go` 1,068 lines at root → `cmd/main.go` + `internal/<concern>/` |
| `telemetry-receiver` | Service | `cmd/main.go` + `internal/<concern>/` | 12 source files in root `package main` → `cmd/main.go` + `internal/<concern>/`; `web/` and `design-export/` assets stay |
| `tunnel` | Service | `cmd/main.go` + `internal/<concern>/` | `main.go` 619 and `playit_reporter.go` 525 lines at root → `cmd/main.go` + `internal/<concern>/`; `frpc/` (separate build module, not in `go.work`) stays. Lives in the `GameplanePanel/tunnel` submodule. |
| `gp-module` | CLI tool | `cmd/gp-module/` + `internal/<concern>/` | matches; `pkg/` holds the 5 packages `api` and `test/e2e` import |
| `i18n` | Library + CLI | flat root package + `cmd/i18n-check/` | matches (see §3) |
| `gameaction` | Library | flat root package | matches |
| `gameproto` | Library | flat root package | matches |
| `netguard` | Library | flat root package | matches |
| `telemetryschema` | Library | flat root package | matches |
| `test/e2e` | Test suite | root package + `internal/<game>/` | matches (see §3) |
| `web` | Web app | `src/pages/` + route definitions in `src/routes/` | `src/pages/` has started (`ClusterDetail.tsx`, `Nodes.tsx`); most screens still live in `src/routes/` → move each to `src/pages/<Screen>.tsx`; screens over 500 lines to split: `AdminSettings.tsx` 2,290, `CreateServer.tsx` 1,448, `tabs/Mods.tsx` 1,291, `tabs/settings/Networking.tsx` 1,045, `ThemeSettings.tsx` 886, `Users.tsx` 825, `Servers.tsx` 680, `tabs/settings/ShareLinks.tsx` 658, `tabs/Files.tsx` 657, `Backups.tsx` 642, `Dashboard.tsx` 599 |

## 3. Exceptions

- `test/e2e`: a test suite, not a service or library. Tests sit at the root
  (`//go:build e2e`) with per-game packages under `internal/<game>/` and shared
  helpers in `internal/fakeoidc/`, `internal/probe/` and `internal/protocol/`.
- `operator/api/v1alpha1`: CRD types follow the kubebuilder layout that
  `make generate` and `make manifests` expect.
- `i18n`: a library (root package plus `catalog/`, `icu/`, `modlocale/`, `pack/`,
  each imported on its own) that also ships the `i18n-check` CLI under
  `cmd/i18n-check/`; it matches both kinds.

## 4. Enforcement

Spec 028 adds a folder-shape check to `make lint` once every module matches this
doc. Until then, review enforces it: new code follows the target shape even in a
module that does not match yet.

## 5. Not decided here

Where each large package splits, and the line limit for files other than web
screens, belong to spec 028 (`specs/028-split-big-packages/`).
