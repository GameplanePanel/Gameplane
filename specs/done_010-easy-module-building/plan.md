# Implementation Plan: Easy Module Building & Authoring Toolkit

**Branch**: `010-easy-module-building` | **Date**: 2026-08-27 | **Spec**: [spec.md](spec.md)

**Input**: Feature specification from `/specs/010-easy-module-building/spec.md`

## Summary

Deliver a unified, offline-capable Go CLI developer toolkit (`gp-module`) and a web dashboard Module Builder interface that transforms Gameplane game module creation, validation, previewing, and packaging across both terminal and browser workflows:
1. **Interactive & Automated Scaffolding (`init` & Web Wizard)**: Generates pre-validated module directories (`module.yaml`, `template.yaml`, `README.md`, `icon.png`) for `steamcmd`, `java`, and `generic` archetypes with DNS-1123 validation.
2. **Offline Validation & Linting (`validate` & API)**: Static preflight checking against CRD/metadata schemas, canonical categories taxonomy, image digest pinning, port boundaries/collisions, and configuration rules with exact source line numbers using `yaml.v3`.
3. **Dry-Run Preview (`preview` & Live Simulator)**: Exact replication of operator runtime logic using native Kubernetes resource types for environment variable merging, version selection, and `autoFromMemoryLimit` percentage-to-megabyte calculations.
4. **OCI Packaging & Web Export (`package` & Download/Install)**: Asset size sanity checking (icon $\le$ 512 KiB, bundle $\le$ 1 MiB), automated OCI bundle packaging via ORAS, and one-click in-cluster installation or `.tar.gz` download from the browser.
5. **Design-First Web UI**: A clean, accessible Module Builder dialog on the `/modules` page designed first in `design.pen` (via `pencil` MCP server) and exported to `design-export/` before React implementation.

## Technical Context

**Language/Version**: 
- Go 1.26.0 (CLI `gp-module/` and API `api/`)
- TypeScript strict + React 18 + Vite (web dashboard `web/`)

**Primary Dependencies**:
- `gopkg.in/yaml.v3` (for YAML AST node traversal with source line/column numbers)
- `k8s.io/apimachinery` (for `resource.Quantity`, DNS-1123 label validation)
- `github.com/ValgulNecron/gameplane/operator/api/v1alpha1` (for `GameTemplate` CRD Go structs)
- `github.com/ValgulNecron/gameplane/gp-module/pkg/...` (public packages exported by `gp-module` and imported by `api/` handlers)
- `@tanstack/react-query`, Radix UI Dialog primitives, Lucide icons (web dashboard)
- `oras` (CLI $\ge$ 1.2.0) for OCI artifact packaging and publishing

**Storage**: Local filesystem operations on module directories; in-cluster bundle storage in `upload`-type ModuleSource ConfigMaps.

**Testing**:
- Go unit tests across `gp-module/internal/...` and `api/internal/handlers/modules_builder_test.go`
- React/Vitest unit and interaction tests in `web/src/components/modules/BuildModuleDialog.test.tsx`
- Coverage gates: `gp-module/.testcoverage.yml` and `api/.testcoverage.yml` enforced by `go-test-coverage`
- End-to-end Kubernetes verification in `test/e2e/module_toolkit_e2e_test.go` (registered in `bucket_operator` in `test/e2e/buckets.sh`)

**Target Platform**: Linux, macOS, modern web browsers.

**Project Type**: Go CLI binary (`gp-module`), Go module (`gp-module/`), API extension (`api/`), Web dashboard feature (`web/`), documentation update (`docs/module-authoring.md`), and E2E test suite extension.

**Performance Goals**:
- Scaffolding completion: $< 100\text{ ms}$
- Offline validation scan: $< 200\text{ ms}$ across 20+ modules
- Web preview reactivity: $< 50\text{ ms}$ live update

**Constraints**:
- **Strictly Offline First**: CLI Scaffolding, validation, and preview MUST execute without network connectivity or cluster access.
- **Design-First Delivery**: Web UI MUST be designed in `design.pen` via the `pencil` MCP server and exported to `design-export/` before frontend React code is written (Constitution Principle II).
- **Zero In-Source Suppressions**: No `//nolint`, `//#nosec`, `@ts-ignore`, or `eslint-disable` directives.
- **Spec-Driven Development**: Must maintain `gp-module/specs.md` complying with Constitution Principle IV and passing `hack/check-specs.sh`.
- **100% Backward Compatibility**: Existing scripts (`modules/validate.py`, `modules/build.sh`) and existing modules continue to pass with 0 regressions.

**Scale/Scope**: Covers 100% of official and community game modules (~17 existing modules, scalable to 50+).

## Constitution Check

*GATE: Must pass before Phase 0 research. Re-check after Phase 1 design.*

| Principle | Status | Justification |
| :--- | :---: | :--- |
| **I. E2E-Tested Delivery** | **PASS** | A new dedicated E2E test (`test/e2e/module_toolkit_e2e_test.go`) is added to `bucket_operator` in `test/e2e/buckets.sh`. It invokes `gp-module` to scaffold a module, validates it, packages it, pushes to the in-cluster registry, and confirms the operator discovers and materializes the `GameTemplate`. Web and API flows are tested in unit/component tests. |
| **II. Design-First** | **PASS** | The web dashboard visual surface is modified (adding the "Create module" button on `/modules` and the `BuildModuleDialog` modal wizard). The design MUST be created in `design.pen` using the `pencil` MCP server and exported to `design-export/{json,screenshots}/` before frontend React code is committed. |
| **III. Language & Ecosystem** | **PASS** | Follows Go 1.26 idioms (`%w` error wrapping, `t.Parallel()`, strict error handling) and TypeScript strict mode without `any`. Added to `go.work` and `GO_MODULES` in `Makefile`. Zero in-source suppressions. |
| **IV. Spec-Driven Development** | **PASS** | Complete specification (`spec.md`), research (`research.md`), data model (`data-model.md`), contracts (`contracts/cli-contract.md`, `contracts/archetypes-contract.md`, `contracts/diagnostics-contract.md`, `contracts/web-builder-contract.md`), and quickstart guide (`quickstart.md`) created. `gp-module/specs.md` maintained and verified by `hack/check-specs.sh`. |
| **V. Delegate to Workflows** | **PASS** | Implementation breakdown in Phase 2 will isolate scaffolding, validation, preview, packaging, Pencil design, API endpoints, Web UI, and E2E testing into discrete, parallelizable tasks. |
| **VI. CI Bears Heavy Lifting** | **PASS** | Verification of full test suites and E2E execution runs exclusively on GitHub Actions CI. Local execution is restricted to static preflight checks and Pencil design exports. |

## Project Structure

### Documentation (this feature)

```text
specs/010-easy-module-building/
├── spec.md                       # Feature specification
├── plan.md                       # Implementation plan (this document)
├── research.md                   # Phase 0 research findings and technical decisions
├── data-model.md                 # Phase 1 data entities and validation models
├── quickstart.md                 # Phase 1 verification and run walkthrough
├── contracts/                    # Phase 1 interface contracts
│   ├── cli-contract.md           # CLI commands, arguments, exit codes
│   ├── archetypes-contract.md    # Starter archetypes definition
│   ├── diagnostics-contract.md   # Linter report schema
│   └── web-builder-contract.md   # Web UI and REST API builder endpoints
└── checklists/
    └── requirements.md           # Specification quality checklist
```

### Source Code (repository root)

```text
gp-module/                        # NEW: Go module added to go.work
├── go.mod                        # Go 1.26.0 (module github.com/ValgulNecron/gameplane/gp-module)
├── go.sum
├── .testcoverage.yml             # Per-module coverage thresholds
├── specs.md                      # Architecture & boundary spec (Constitution Principle IV)
├── cmd/
│   └── gp-module/
│       └── main.go               # CLI entrypoint
└── internal/
    ├── scaffold/                 # US1: Scaffolding engine & DNS-1123 validation
    │   ├── scaffold.go
    │   └── scaffold_test.go
    ├── validator/                # US2: Offline validator & line-number diagnostics
    │   ├── validator.go
    │   ├── rules.go
    │   └── validator_test.go
    ├── preview/                  # US3: Dry-run preview & config materializer
    │   ├── preview.go
    │   └── preview_test.go
    ├── packager/                 # US4: OCI bundle packager & size verification
    │   ├── packager.go
    │   └── packager_test.go
    └── archetypes/               # Embedded starter archetypes
        ├── archetypes.go
        ├── steamcmd.go
        ├── java.go
        └── generic.go

api/
└── internal/
    └── handlers/
        ├── modules_builder.go    # NEW: /modules/builder endpoints (scaffold, validate, preview, export)
        └── modules_builder_test.go

web/
├── src/
│   ├── components/
│   │   └── modules/
│   │       ├── BuildModuleDialog.tsx       # NEW: Web module builder wizard dialog
│   │       └── BuildModuleDialog.test.tsx  # NEW: Component unit tests
│   ├── routes/
│   │   ├── Modules.tsx                     # MODIFIED: Adds "Create module" action button
│   │   └── Modules.test.tsx                # MODIFIED: Verifies dialog launch
│   └── lib/
│       └── endpoints.ts                    # MODIFIED: Adds Modules.builder.* API methods

design-export/                    # MODIFIED: Updated with Pencil export of BuildModuleDialog
├── json/
│   └── build-module-dialog.json
└── screenshots/
    └── build-module-dialog.png

modules/                          # GIT SUBMODULE: gameplane-module
├── validate.py                   # UNCHANGED: Preserved; invokes gp-module validate --json
├── build.sh                      # UNCHANGED: Preserved for existing workflows
└── .schema/                      # JSON schemas for template.yaml and module.yaml

go.work                           # MODIFIED: Includes ./gp-module
Makefile                          # MODIFIED: Adds gp-module to GO_MODULES, adds module-* targets
docs/module-authoring.md          # MODIFIED: Documents gp-module workflows and Web Builder

test/e2e/
├── buckets.sh                    # MODIFIED: Adds TestModule_ScaffoldAndPackage to bucket_operator
└── module_toolkit_e2e_test.go    # NEW: Full scaffolding -> packaging -> operator reconcile E2E test
```

**Structure Decision**:
`gp-module` provides the core Go logic (scaffolding, validation, preview, packaging). The `api/` handler imports the public `gp-module/pkg/...` wrappers directly to serve the web dashboard, ensuring 100% logic and behavior parity between the CLI tool and the in-browser Web Builder while respecting Go module package boundaries.

## Complexity Tracking

> **Constitution Check has no violations; Complexity Tracking table is empty.**

| Violation | Why Needed | Simpler Alternative Rejected Because |
| :--- | :--- | :--- |
| None | N/A | N/A |
