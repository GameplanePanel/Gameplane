# Phase 0 Research: Easy Module Building & Authoring Toolkit

**Branch**: `010-easy-module-building` | **Date**: 2026-08-27 | **Spec**: [spec.md](spec.md)

---

## 1. Executive Summary

This research establishes the technical strategy and architectural decisions for delivering the **Easy Module Building & Authoring Toolkit** (`gp-module`). The toolkit lowers the barrier to entry for game server administrators and community contributors authoring Gameplane modules. It provides:
1. Guided interactive and automated non-interactive scaffolding across three primary game archetypes (`steamcmd`, `java`, `generic`).
2. An offline-first validation and linting engine with source line-number tracking, canonical category checks, port collision detection, and security rules.
3. A dry-run manifest renderer and configuration preview engine accurately computing dynamic fields like `autoFromMemoryLimit` and version overlays using Gameplane's native Kubernetes types.
4. An OCI bundle packager verifying asset sizes and media types for distribution via ORAS.

---

## 2. Technical Decisions & Tradeoffs (User Confirmed)

### Decision 1: Toolkit Implementation as a First-Class Go Module (`gp-module/`)

- **Decision**: Implement the toolkit in **Go 1.26.0** as a dedicated top-level module `gp-module/` in `go.work` (entrypoint at `gp-module/cmd/gp-module/main.go`), accompanied by unit tests, `.testcoverage.yml`, a `specs.md` file (complying with Constitution Principle IV), and top-level Makefile convenience targets (`make module-new`, `make module-validate`, `make module-preview`, `make module-package`).
- **Rationale**:
  - Unified with Gameplane's entire backend ecosystem (`operator`, `api`, `agent`, `sentinel`, `gameproto`, `svcutil`).
  - Direct type-safe access to Gameplane CRD Go structs (`operator/api/v1alpha1.GameTemplate`, `ConfigField`, `AutoFromMemoryLimit`) and Kubernetes resource parsers (`k8s.io/apimachinery/pkg/api/resource`).
  - Native line-number tracking during YAML AST parsing via `gopkg.in/yaml.v3` (`yaml.Node.Line` and `yaml.Node.Column`).
  - Fast compiled CLI binary (`bin/gp-module`) without Python runtime version inconsistencies.
- **Alternatives Considered**:
  - *Python in `modules/toolkit/`*: Evaluated during initial draft to keep the `gameplane-module` submodule standalone without Go; user confirmed Go CLI in `cmd/gp-module` is preferred to maintain a unified Go workspace.
  - *Shell scripts*: Rejected due to complex YAML AST traversal, JSON Schema validation, and arithmetic evaluation requirements.

---

### Decision 2: Scaffolding Archetype Architecture

- **Decision**: Provide three built-in starter archetypes (`steamcmd`, `java`, `generic`) embedded in Go (`embed.FS`):
  - `steamcmd`: Defaults tailored for dedicated game servers installed/updated via SteamCMD (Valve UDP ports 27015/udp, 27016/udp, persistent save path `/serverdata`, Steam AppID parameters, graceful console/SIGINT termination).
  - `java`: Defaults tailored for JVM-based servers like Minecraft (TCP game port 25565/tcp, RCON 25575/tcp, EULA acceptance variable, JVM heap memory configuration utilizing `autoFromMemoryLimit: {percent: 75}`).
  - `generic`: Defaults for standalone binaries or generic containerized game servers with standard TCP/UDP port mapping and persistent volume storage.
- **Rationale**:
  - Confirmed by user: covers over 90% of dedicated game server architectures.
  - Generates a fully populated, schema-compliant directory structure (`module.yaml`, `template.yaml`, `README.md`, and a placeholder 256x256 RGBA `icon.png`).
  - Enforces DNS-1123 naming rules (`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`, max 63 characters) and prevents destructive accidental overwrites unless `--overwrite` is explicitly provided.

---

### Decision 3: Offline Validation Engine & Backward Compatibility with `modules/validate.py`

- **Decision**: 
  1. `gp-module validate` provides pure offline static validation without contacting remote container registries or requiring a live Kubernetes cluster.
  2. The existing `modules/validate.py` script remains 100% backward-compatible. It can invoke `gp-module validate --json` to include offline checks in its preflight run while continuing to perform its online Docker Hub image config inspections.
- **Rules Evaluated by `gp-module validate`**:
  - *Metadata integrity*: `module.yaml` required fields (`apiVersion`, `name`, `displayName`, `version`, `game`, `summary`), semver check, directory name matching.
  - *Taxonomy conformance*: Validation of `categories` against the canonical list (`Survival`, `Sandbox`, `Shooter`, `Simulation`, `Building`, `Adventure`, `Horror`, `Co-op`, `PvP`, `Modded`, `Creative`).
  - *Schema validation*: Evaluation against CRD openAPIV3Schema rules.
  - *Security & digest pinning*: Enforcing `@sha256:...` digest pinning on all default images unless marked with `# gameplane:floating`.
  - *Port & protocol checks*: Port range bounds (1–65535), protocols (`TCP`, `UDP`), and duplicate collision detection.
  - *ConfigSchema rules*: Type verification (`string`, `int`, `enum`, `boolean`, `password`), default value matching, `autoFromMemoryLimit` percentage range (1–100), and mandatory `type: password` for credential-shaped fields.
  - *Diagnostic output*: Structured output indicating `[LEVEL] [FILE:LINE] [RULE]: Message -> Recommendation`, plus `--json` output for machine consumers.
- **Rationale**:
  - Fulfills user decision A2: keeps `validate.py` backward-compatible while providing instant offline validation for authors.
  - `yaml.Node` in `gopkg.in/yaml.v3` gives precise source line numbers for each key/value node.

---

### Decision 4: Dry-Run Manifest Rendering & Configuration Preview Engine

- **Decision**: Replicate the Gameplane operator's `materializeConfig` and `autoMemoryValue` logic in Go using native Kubernetes packages:
  - Parse memory quantity strings (`4Gi`, `2048Mi`, etc.) with `resource.ParseQuantity`.
  - Compute memory-proportional fields:
    $$\text{mib} = \lfloor \text{bytes} \times \text{percent} / 100 / (1024 \times 1024) \rfloor \implies \text{"<mib>M"}$$
  - Merge environment variables in order of precedence: base `spec.env` $\to$ selected version `spec.versions[].env` $\to$ resolved `spec.configSchema` variables (schema overrides template env on collision).
  - Evaluate version selection via `--version-id`, resolving the active container image, version-specific environment variables, and mod loader mappings.
  - Output rendered result in YAML, JSON, or human-readable summary.
- **Rationale**:
  - Encapsulating this in `gp-module/internal/preview` (exposed via public `gp-module/pkg/preview`) provides exact arithmetic with unit and parity test coverage against operator behavior.

---

### Decision 5: One-Step Packaging & Integrity Verification

- **Decision**: Implement packaging in `gp-module package`:
  - Inspects bundle files before packaging, warning if `icon.png` > 512 KiB or total bundle > 1 MiB.
  - Enforces required layers (`module.yaml`, `template.yaml`, `README.md`) and standard Gameplane media types.
  - Executes `oras push` when `--registry` is specified (maintaining parity with `modules/build.sh`), or packages a local `.tar.gz` archive when `--output` is specified.
- **Rationale**:
  - Streamlines packaging and distribution while enforcing asset size guardrails.

---

### Decision 6: Testing & E2E Verification Strategy

- **Decision**:
  1. Per-module unit tests in `gp-module/` with coverage gate in `gp-module/.testcoverage.yml` (e.g. 80% threshold).
  2. Dedicated Go E2E test in `test/e2e/module_toolkit_e2e_test.go` registered in `bucket_operator` in `test/e2e/buckets.sh`:
     - Compiles and runs `gp-module init` to scaffold a test module.
     - Runs `gp-module validate` to assert 0 errors.
     - Runs `gp-module package` to push to the local in-cluster registry.
     - Creates `ModuleSource` and `Module` CRs, verifying the operator reconciles and materializes the `GameTemplate`.
  3. `hack/check-specs.sh` compliance: `gp-module/specs.md` documents the toolkit architecture, satisfying Constitution Principle IV.

---

## 3. Technology Matrix

| Component | Technology | Version | Purpose |
| :--- | :--- | :--- | :--- |
| **CLI Runtime** | Go | 1.26.0 | CLI engine, scaffolding, linting, preview |
| **YAML Engine** | `gopkg.in/yaml.v3` | v3.0.1 | YAML AST parsing and source line tracking |
| **K8s Types** | `k8s.io/apimachinery` | v0.31+ | Resource quantities, DNS-1123 validation |
| **Gameplane CRDs** | `operator/api/v1alpha1` | Internal | Type-safe GameTemplate definitions |
| **OCI Distribution**| ORAS CLI | $\ge$ 1.2.0 | OCI artifact bundle publishing |
| **E2E Integration** | Go (`test/e2e`) | 1.26.0 | End-to-end cluster lifecycle verification |
| **Build Automation**| GNU Make | 4.0+ | Developer shortcuts (`make module-*`, `make build-go`) |
