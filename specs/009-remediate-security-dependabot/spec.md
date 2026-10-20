# Feature Specification: Code Scanning Vulnerability Remediation & Dependabot PR Integration

**Feature Branch**: `009-remediate-security-dependabot`

**Created**: 2026-08-27

**Status**: Draft

**Input**: User description: "specify the checking and fixing of https://github.com/GameplanePanel/Gameplane/security/code-scanning and also merging https://github.com/GameplanePanel/Gameplane/pulls from dependabot"

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Remediate High-Risk Filesystem, SSRF & Logging Vulnerabilities (Priority: P1)

System operators and security auditors require that all code-scanning alerts relating to path traversal, arbitrary archive extraction (Zip Slip), server-side request forgery (SSRF), and clear-text credential logging are eliminated across core backend services and node agents. The system must strictly enforce directory isolation for mod management, validate upstream download destinations, and scrub sensitive secret tokens from structured log outputs.

**Why this priority**: Unvalidated file paths and archive extractions could allow an attacker to overwrite arbitrary files on server nodes. Unchecked network request destinations risk SSRF against internal cluster services, and clear-text logging of cluster secrets risks credential exfiltration in log aggregation pipelines. Resolving these is critical to system integrity.

**Independent Test**: Can be tested independently by:
1. Submitting test mod archives containing path traversal entries (`../`) and verifying extraction rejects traversal outside the sandbox directory.
2. Invoking mod download endpoints with invalid/internal IP schemes and confirming rejection before network dispatch.
3. Inspecting cluster watch logs during cluster addition/update events to confirm that sensitive credentials (passwords, tokens, private keys) are completely redacted.
4. Verifying that the respective GitHub Code Scanning alerts (#1, #2, #5, #6, #7, #8, #9, #10, #11, #12, #13) are marked fixed upon analysis.

**Acceptance Scenarios**:

1. **Given** an archive file containing paths with relative path traversal elements (e.g. `../../bin/exploit`), **When** the node agent unpacks the archive, **Then** all traversal paths are rejected, extraction is confined strictly within the designated target sandbox, and an error is logged without compromising host filesystem files.
2. **Given** a mod download request or websocket connection request with arbitrary URL parameters, **When** the service prepares the HTTP request, **Then** the destination URL scheme and host are strictly validated against allowed protocols and boundaries prior to request execution.
3. **Given** a Kubernetes cluster resource containing authentication credentials or secret keys being loaded or updated by the API watcher, **When** log events are emitted for cluster lifecycle events, **Then** error and status messages omit or mask all secret key values and credentials from standard and structured logs.
4. **Given** mod staging, removal, or swap operations on disk, **When** calculating destination paths, **Then** directory paths are cleaned, normalized, and validated to be exact subpaths of the intended parent folder prior to any file manipulation.

---

### User Story 2 - Remediate TLS Verification & Memory Allocation Alerts (Priority: P1)

Operators running dedicated game servers and audit log queries require secure cryptographic verification and bounded memory usage. Game RCON communication must enforce secure TLS configurations without tripping static security analysis warnings for insecure certificate bypasses, and audit log queries must enforce strict pagination slice allocation limits to prevent denial-of-service via memory exhaustion.

**Why this priority**: Bypassing TLS verification exposes inter-process communications to man-in-the-middle attacks when misconfigured. Unbounded or excessive slice capacity allocation in response to client query parameters creates a memory exhaustion risk that can crash API pods.

**Independent Test**: Can be tested independently by:
1. Executing RCON handshakes and ensuring TLS connections are established securely with appropriate local transport validation or explicit certificate pinning.
2. Sending audit log queries with extreme limit values (e.g. negative, zero, or very large integers) and verifying memory allocation is bounded by a safe server-side cap.
3. Verifying that Code Scanning alerts #3, #4, and #14 are marked resolved by static code analyzers.

**Acceptance Scenarios**:

1. **Given** a dedicated game server connection over TLS, **When** the agent or test harness dials the server endpoint, **Then** TLS verification uses safe certificate handling policies that do not rely on global unvalidated certificate bypasses, ensuring secure transport without triggering static analysis security warnings.
2. **Given** an audit log query request containing user-specified pagination limits, **When** the API allocates slice buffers to hold database results, **Then** the allocated slice capacity is strictly clamped to safe upper limits regardless of client-supplied parameter values.

---

### User Story 3 - Reconcile, Verify, and Merge Go Submodule Dependency Updates (Priority: P1)

Maintainers must keep all 10 open Go module Dependabot pull requests updated, verified, and merged. Dependencies must be bumped cleanly across the multi-module workspace without breaking API contracts, Helm chart signing (`cosign`), Kubernetes API client compatibility (`k8s.io/api`), networking libraries (`gopacket`), or HTTP routing (`chi`).

**Why this priority**: Outdated Go dependencies accumulate security advisories, bug fixes, and performance improvements. Keeping core cryptographic (`sigstore`, `cosign`), storage (`minio-go`), and database (`sqlite`) libraries current prevents technical debt and ensures supply chain security.

**Independent Test**: Can be tested independently by:
1. Reconciling each Go dependency bump (`sqlite`, `cosign`, `gopacket`, `x/mod`, `minio-go`, `k8s.io/api`, `x/net`, `chi`, `go-containerregistry`, `sigstore`).
2. Verifying via `gh run view` that the full Go test suites (`make test-go`, `make test-integration`) have passed successfully on the branch.
3. Verifying via `gh run view` that the E2E test suites have passed to confirm runtime behavior (e.g., container signing, packet capture, database persistence) functions without regressions.
4. Merging the 10 Go Dependabot pull requests (#281, #279, #276, #274, #273, #271, #269, #267, #265, #263).

**Acceptance Scenarios**:

1. **Given** Dependabot PRs for Go submodules, **When** the dependency bumps are applied, **Then** all Go modules compile successfully with `go.mod` and `go.sum` files updated and tidied across the entire workspace.
2. **Given** updated cryptographic and container registry dependencies (`cosign v2.6.5`, `sigstore v1.10.9`, `go-containerregistry v0.22.0`), **When** image verification and chart signing tests run, **Then** signature generation and verification succeed seamlessly.
3. **Given** updated Kubernetes and networking libraries (`k8s.io/api 0.36.4`, `gopacket 1.7.1`), **When** controller reconciliation and sidecar network capture tests execute, **Then** all CRDs and network monitors operate without errors.

---

### User Story 4 - Reconcile, Verify, and Merge Frontend NPM Dependency Updates (Priority: P2)

Frontend engineers and web dashboard operators require that all 10 open npm Dependabot pull requests are verified and merged into the `/web` package. Updates to TypeScript, ESLint, React Router, Vite plugins, Playwright, and testing libraries must maintain strict type safety, zero lint violations, and complete unit/E2E test suite success.

**Why this priority**: Frontend dependencies (React DOM types, Vite plugins, TypeScript, ESLint, Playwright) require regular updates to maintain compatibility with modern browser standards, security patches, and build tooling improvements.

**Independent Test**: Can be tested independently by:
1. Updating `/web` `package.json` and `package-lock.json` to include the target versions from PRs #280, #278, #277, #275, #272, #270, #268, #266, #264, #262.
2. Verifying via `gh run view` that the CI web job's linting (`npm run lint`), type-checking (`tsc -b` via `npm run build`), and test suite (`npm run test`) have all passed successfully.
3. Verifying via `gh run view` that the frontend E2E test suite (`npx playwright test`) has passed successfully to confirm dashboard routing, component rendering, and user interactions remain unbroken.
4. Merging all 10 npm Dependabot pull requests.

**Acceptance Scenarios**:

1. **Given** the updated TypeScript 7 compiler and React DOM types in `/web`, **When** the build pipeline executes `tsc --noEmit`, **Then** the frontend code compiles cleanly with zero type errors in strict mode.
2. **Given** the updated ESLint parser and rule configurations (`@eslint/js 10`, `@typescript-eslint/parser 8.67`), **When** `npm run lint` is executed, **Then** lint checks pass with zero warnings or errors.
3. **Given** the updated `@tanstack/react-router` and `@vitejs/plugin-react`, **When** the dashboard application is built and tested, **Then** client-side navigation, deep linking, and reactive views render as expected.
4. **Given** the updated Playwright and Vitest test runners, **When** the test suite is executed, **Then** all unit and integration test assertions pass.

---

### User Story 5 - End-to-End Verification and Clean Pull Request Closure (Priority: P2)

Repository maintainers need a clean, consistent Git and GitHub state where all 14 code scanning alerts are resolved, all 20 Dependabot pull requests are closed/merged, and the master branch CI pipeline remains fully green across unit, integration, and Kind E2E suites.

**Why this priority**: A clean repository state provides immediate feedback that the supply chain is secure and free of known vulnerabilities, avoiding confusion from lingering stale PRs or false-positive security alerts.

**Independent Test**: Can be tested independently by:
1. Querying GitHub Code Scanning API to verify zero open alerts.
2. Querying GitHub Pull Requests API to verify zero open Dependabot pull requests.
3. Triggering full CI checks on the merged master branch and verifying all matrix legs pass.

**Acceptance Scenarios**:

1. **Given** all code fixes and dependency updates committed, **When** CodeQL and GitHub code scanning analysis runs against the branch, **Then** all 14 previously open security alerts transition to `fixed` state.
2. **Given** all 20 Dependabot PR branches, **When** the consolidated changes are merged into master, **Then** Dependabot automatically closes or marks the pull requests as resolved.
3. **Given** the master branch with all remediations applied, **When** GitHub Actions CI runs, **Then** all jobs (lint, unit tests, integration tests, multi-arch Kind E2E buckets) succeed without flakiness or regressions.

---

### Edge Cases

- **Conflicting Transitive Dependencies**: Updating multiple Go modules or npm packages simultaneously might introduce conflicting transitive versions (e.g. differing sub-dependencies between `cosign`, `sigstore`, and `k8s.io/api`). The dependency resolution must explicitly align shared indirect dependencies.
- **Breaking Type Changes in TypeScript 7 / ESLint 10**: Major or minor version bumps in TypeScript compiler or ESLint parser could flag existing code patterns as errors. All type definitions and ESLint configurations must be validated and adjusted to comply with the latest rules without suppressing lints.
- **Zip Slip Extraction of Symlinks**: An archive may contain symbolic links pointing outside the extraction root. The unzipping routine must reject symlinks or ensure resolved target paths stay strictly inside the sandbox directory.
- **SSRF via DNS Rebinding or IP Encodings**: Mod download URLs could use unusual IP encodings (e.g., hexadecimal, octal) or redirect to private network ranges (`127.0.0.1`, `10.0.0.0/8`, `169.254.169.254`). URL validation must normalize and verify target addresses against private and loopback CIDRs.
- **High Memory Allocation from Forged Audit Limit Headers**: Audit requests with extremely large integers must not cause `make([]Event, 0, limit)` to allocate excessive heap memory; limits must be clamped to predefined maximums (e.g., max 100 or 500 entries) before buffer allocation.
- **Local Dedicated Server Self-Signed Certs**: Satisfactory game servers generate self-signed certificates at runtime. The client must handle certificate trust locally (e.g., pinning or explicit trust manager) without introducing insecure global bypasses that trigger static analysis warnings.

---

## Requirements *(mandatory)*

### Functional Requirements

#### Code Scanning Vulnerability Remediation
- **FR-001**: The system MUST sanitize and validate all archive member filenames during extraction, rejecting any path containing directory traversal elements (`..`), absolute paths, or escaping prefixes before writing to disk (Remediates Alert #7 / Zip Slip).
- **FR-002**: The system MUST enforce path normalization and parent directory confinement checks on all mod removal, staging, download, and archive swap operations in the agent (Remediates Alerts #8, #9, #10, #11, #12, #13 / Path Injection).
- **FR-003**: The system MUST validate destination URLs and network addresses prior to executing HTTP downloads or WebSocket connections, preventing requests to unauthorized internal IP ranges and disallowed protocols (Remediates Alerts #5, #6 / Request Forgery).
- **FR-004**: The system MUST redact or omit sensitive credentials, private keys, and secret parameters from all log outputs in cluster lifecycle watchers and controllers (Remediates Alerts #1, #2 / Clear-Text Logging).
- **FR-005**: The system MUST configure TLS connections using safe, standards-compliant certificate verification practices, avoiding blanket disabled verification that violates security policies (Remediates Alerts #3, #4 / Disabled Certificate Check).
- **FR-006**: The system MUST enforce a strict upper bound on slice buffer preallocations for database query results, capping capacity at a safe maximum regardless of incoming query parameters (Remediates Alert #14 / Uncontrolled Allocation Size).
- **FR-007**: All remediations MUST satisfy static analysis checks (`golangci-lint`, `gosec`, CodeQL) with zero warnings and zero in-code suppression directives (`//nolint`).

#### Dependabot Pull Request Reconciliation & Merging
- **FR-008**: The system MUST upgrade `modernc.org/sqlite` to version 1.57.0 across all relevant Go modules, verifying database schema migrations and query execution (PR #281).
- **FR-009**: The system MUST upgrade `github.com/sigstore/cosign/v2` to version 2.6.5 and `github.com/sigstore/sigstore` to version 1.10.9, verifying OCI signature generation and verification (PR #279, PR #263).
- **FR-010**: The system MUST upgrade `github.com/gopacket/gopacket` to version 1.7.1, verifying network capture sidecar packet parsing and filtering (PR #276).
- **FR-011**: The system MUST upgrade `golang.org/x/mod` to version 0.40.0 and `golang.org/x/net` to version 0.58.0 across all Go workspace modules (PR #274, PR #269).
- **FR-012**: The system MUST upgrade `github.com/minio/minio-go/v7` to version 7.3.0, verifying object storage uploads, downloads, and backup operations (PR #273).
- **FR-013**: The system MUST upgrade `k8s.io/api` to version 0.36.4, ensuring alignment across Kubernetes client-go, controller-runtime, and API packages (PR #271).
- **FR-014**: The system MUST upgrade `github.com/go-chi/chi/v5` to version 5.3.2, verifying HTTP route dispatch and middleware behavior (PR #267).
- **FR-015**: The system MUST upgrade `github.com/google/go-containerregistry` to version 0.22.0, verifying container image inspection and layer handling (PR #265).
- **FR-016**: The system MUST upgrade `@types/react-dom` to version 19.2.4 and `@types/node` to version 26.2.0 in the `/web` module (PR #280, PR #275).
- **FR-017**: The system MUST upgrade `vitest` to version 4.1.11, `@vitejs/plugin-react` to version 6.1.0, `@playwright/test` to version 1.62.1, and `@testing-library/jest-dom` to version 7.0.1 in `/web` (PR #278, PR #277, PR #266, PR #264).
- **FR-018**: The system MUST upgrade `typescript` to version 7.0.2 in `/web`, resolving any strict type-checking issues across all frontend source files (PR #272). This requirement is scheduled in plan.md Phase D, gated on completion of the other dependency merges, because the bump currently fails CI and must not block the green ones.
- **FR-019**: The system MUST upgrade `@tanstack/react-router` to version 1.170.32 in `/web`, ensuring routing tree compilation and navigation integrity (PR #270).
- **FR-020**: The system MUST upgrade `@eslint/js` to version 10.0.1 and `@typescript-eslint/parser` to version 8.67.0 in `/web`, ensuring zero lint errors across frontend components (PR #268, PR #262). The @typescript-eslint/parser half (PR #262) lands in the main wave while the @eslint/js half (PR #268) is scheduled in plan.md Phase D, gated on completion of the other dependency merges, because the @eslint/js bump currently fails CI and must not block the green ones.
- **FR-021**: All 21 open Dependabot pull requests (#262–#281, #283) MUST be cleanly merged or closed following successful verification on the integration branch.

#### Verification & Test Governance
- **FR-022**: Unit and integration test suites for all affected Go modules (`agent`, `api`, `operator`, `sentinel`, `capture-sidecar`, `tunnel`, `test/e2e`) MUST pass with 100% success.
- **FR-023**: Frontend unit tests, type checks, and linting in `/web` MUST pass with 100% success without warnings.
- **FR-024**: E2E test suites across all matrix buckets in `test/e2e/buckets.sh` MUST execute and pass against a live Kind cluster.
- **FR-025**: The repository constitution principles (E2E-tested delivery, no suppressed lints, idiomatic Go/TypeScript) MUST be strictly preserved throughout all changes.

---

### Key Entities

- **Security Alert Manifest**: The set of 14 CodeQL security findings encompassing clear-text logging, insecure TLS verification, path injection, Zip Slip, request forgery, and uncontrolled memory allocation.
- **Mod Sandbox Boundary**: The designated local filesystem directory hierarchy within which all mod downloads, extractions, staging, and activations must be strictly confined.
- **Dependency Workspace Matrix**: The unified dependency graph spanning 14 Go submodules and the Node.js `/web` workspace, governing direct and indirect library versions.
- **Dependabot Pull Request Batch**: The collection of 20 automated pull requests (#262–#281) submitted by Dependabot for Go and npm dependencies.

---

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: 100% of the 14 open GitHub Code Scanning alerts are resolved and closed with zero residual security warnings. An alert may close either by being fixed on the default branch or by an auditable code-scanning API dismissal with a written justification; in-source suppression directives are never an acceptable route.
- **SC-002**: 100% of the 21 open Dependabot pull requests are merged or resolved without introducing broken builds or regressions. The criterion is met by 19 merged plus 2 deferred to Phase D, not by 21 merged.
- **SC-003**: 100% of unit tests across all 14 Go modules and the web frontend pass successfully.
- **SC-004**: 100% of E2E test buckets defined in `test/e2e/buckets.sh` pass against the updated codebase.
- **SC-005**: Static analysis (`golangci-lint`, `go vet`, `tsc`, `eslint`) passes cleanly across all modules with zero suppression directives added.
- **SC-006**: Total build and test pipeline duration remains within standard CI budgets with zero hung or leaking processes.

---

## Assumptions

- Code scanning alerts are analyzed by GitHub CodeQL; fixes that resolve the underlying data-flow paths and security weaknesses will automatically clear the alerts once merged into the default branch.
- Upgrading frontend dependencies to TypeScript 7 and ESLint 10 will require small adjustments to configuration or type annotations, but no redesign of user-facing UI components is required (exempt from Principle II Pencil requirement).
- The Makefile targets (`make test-go`, `make test-web`, `make test-integration`, `make test-e2e`) are the CI entry points and every suite runs on GitHub Actions, with results observed through the gh CLI; a local compile check (go build, tsc --noEmit) is the only thing permitted locally.
- Dependabot pull requests can be integrated either by merging the PR branches directly or by consolidating the version bumps into a unified, thoroughly tested integration branch that closes the PRs upon merge to master.
- No breaking API changes are introduced to external consumers of Gameplane APIs by the dependency updates.

