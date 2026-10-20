# Feature Specification: Easy Module Building & Authoring Toolkit

**Feature Branch**: `010-easy-module-building`

**Created**: 2026-08-27

**Status**: Draft

**Input**: User description: "Add a new thing on module for \"easy module building\""

---

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Guided Scaffolding for New Game Modules (Priority: P1)

A community contributor or game server administrator wants to create a new Gameplane module for an unsupported game (e.g., a newly released dedicated game server). Instead of manually copying YAML from existing modules and searching documentation for valid field names and types, they run a guided scaffolding tool. The tool prompts for key game details (game name, display title, category, server container image, networking ports, protocol, save directories, and default environment variables) or accepts non-interactive flags, and outputs a complete, pre-validated module directory containing `module.yaml`, `template.yaml`, `README.md`, and placeholder assets.

**Why this priority**: The barrier to entry for creating new game modules is currently high. New authors must understand internal CRD structures, OCI layer metadata specifications, and schema rules simultaneously. Providing guided scaffolding turns a multi-hour error-prone manual task into a 2-minute guided setup.

**Independent Test**: Can be tested independently by running the scaffolding command with both interactive prompts and non-interactive arguments for different game archetypes (e.g., SteamCMD dedicated server, Java server, generic container server), and verifying that all generated files match the official Gameplane module layout, carry valid JSON Schema modelines, and pass validation out of the box.

**Acceptance Scenarios**:

1. **Given** a module author wanting to create a new module named `my-game`, **When** they run the scaffolding command interactively and provide basic metadata (display name "My Game", category "Survival", container image `example/my-game:v1.0.0`, game port `7777/udp`), **Then** a new directory `modules/my-game` is created containing `module.yaml`, `template.yaml`, `README.md`, and default assets, all correctly populated and conforming to Gameplane schemas.
2. **Given** an automated pipeline or power user, **When** they run the scaffolding command with non-interactive flags (specifying name, archetype, image, and ports via CLI flags), **Then** the module directory is generated without interactive prompts and exits with success code 0.
3. **Given** a selected archetype (e.g., `steamcmd`, `java`, or `generic`), **When** scaffolding completes, **Then** the generated `template.yaml` contains archetype-appropriate defaults (such as standard stop sequences, volume mounts, environment variable structures, and health probe templates).
4. **Given** an existing module directory with the same target name, **When** the scaffolding command is run without an explicit overwrite flag, **Then** the command refuses to overwrite the existing files and reports a clear, non-destructive error.

---

### User Story 2 - Comprehensive Offline Validation and Linting (Priority: P1)

An author has created or updated a module and wants to ensure it satisfies all Gameplane requirements before committing or distributing it. They execute a validation command against the module directory. The tool verifies metadata integrity, CRD schema compatibility, digest pinning compliance, port and volume declarations, and config schema definitions, returning human-readable diagnostics with file locations and actionable resolution advice.

**Why this priority**: Without comprehensive offline validation, syntax errors, missing fields, or invalid configurations are only discovered when packaging OCI artifacts or when the operator fails at runtime in a cluster. Instant, local validation provides immediate feedback and guarantees bundle quality.

**Independent Test**: Can be tested independently by running the validation command against valid module definitions (asserting a clean pass) and against intentionally broken modules (missing mandatory fields, invalid categories, unpinned default image digests, invalid port numbers, malformed configSchema types), asserting that each issue is flagged with an accurate error code, message, and file location.

**Acceptance Scenarios**:

1. **Given** a fully compliant module directory, **When** the validation command is executed against it, **Then** the tool reports 0 errors, 0 warnings, and exits with code 0.
2. **Given** a module with an unpinned default container image (missing `@sha256:...` digest), **When** validation is executed, **Then** the tool flags the unpinned image as a blocking error and suggests the command to resolve and pin the digest.
3. **Given** a module containing an invalid category not in the canonical list, or a missing mandatory field in `module.yaml`, **When** validation is run, **Then** the tool reports the specific field error, lists acceptable values or missing keys, and exits with a non-zero code.
4. **Given** a `template.yaml` with invalid port configurations (e.g., port number > 65535 or missing container port), **When** validation is run, **Then** the tool flags the port specification error with the line number and field name.

---

### User Story 3 - Dry-Run Manifest Rendering & Configuration Preview (Priority: P2)

An author wants to inspect how Gameplane's operator will interpret their `template.yaml` when applied to an actual game server instance, including how configuration schemas, environment variables, resource allocations, and volume mounts will be materialized. They run a preview command that simulates server creation with default or custom configuration values and renders the effective server specification.

**Why this priority**: Authors need confidence that their `configSchema` rules, `autoFromMemoryLimit` calculations, and version overrides behave as intended without needing to deploy to a live Kubernetes cluster for every minor YAML adjustment.

**Independent Test**: Can be tested independently by running the preview command against a module with various input parameters (e.g., memory limit 4Gi, custom version ID, custom config parameters) and verifying that the output matches the expected environment variables, computed memory allocations, and volume mounts.

**Acceptance Scenarios**:

1. **Given** a module with `configSchema` entries using `autoFromMemoryLimit`, **When** the author runs the preview command specifying a memory limit of `4Gi`, **Then** the tool displays the computed configuration values (e.g., `3072M` for 75%) and effective container environment variables.
2. **Given** a module defining multiple selectable versions in `spec.versions`, **When** the author runs preview with `--version-id=<id>`, **Then** the tool renders the effective image, environment variables, and loader mappings for that specific version.
3. **Given** invalid parameter values provided to the preview command (e.g., string value for integer field or out-of-enum value), **When** preview is run, **Then** the tool surfaces the schema validation error before rendering.

---

### User Story 4 - One-Step Packaging & Integrity Verification (Priority: P2)

An author has finished developing and validating their module and wants to package it into an OCI artifact ready for publishing to a registry or for local testing. They run a build command that verifies bundle contents, checks file size limits, ensures required media types and annotations are in place, and produces an OCI artifact bundle or archive.

**Why this priority**: Streamlining packaging ensures that generated OCI artifacts are strictly compliant with Gameplane's runtime specifications and prevents packaging anomalies from reaching production registries.

**Independent Test**: Can be tested independently by packaging a module, inspecting the resulting OCI artifact manifest and layer annotations, and validating that the artifact can be unpacked and consumed by Gameplane's module loader.

**Acceptance Scenarios**:

1. **Given** a validated module directory, **When** the author executes the build/package command, **Then** an OCI artifact is constructed containing the correct layers (`module.yaml`, `template.yaml`, `README.md`, `icon.png`) with standard media types and annotations.
2. **Given** an asset exceeding recommended size limits (e.g., an icon > 512 KiB or total bundle > 1 MiB), **When** the packaging command runs, **Then** the tool warns the author about bundle size constraints before completing.

### User Story 5 - Web Dashboard Module Builder (Priority: P2)

An administrator or server operator accesses the Gameplane web dashboard and wants to create a new game module directly from their browser without opening a terminal or cloning the repository. They click "Create Module" on the Modules catalog page, which opens a guided wizard. The wizard allows selecting an archetype, filling in metadata and container parameters, previewing the generated YAML and memory calculations in real time, and with one click, either downloading the module `.tar.gz` bundle or installing it directly into an in-cluster `upload` ModuleSource.

**Why this priority**: While the CLI toolkit empowers developers in local dev environments and automation pipelines, operators managing Gameplane exclusively via the web UI need an in-browser path to author, validate, and test new game modules.

**Independent Test**: Can be tested independently in Cypress/Vitest by opening the Create Module dialog on the Modules page, stepping through archetype selection and metadata entry, asserting that live validation and preview update reactively, and triggering bundle download and in-cluster installation.

**Acceptance Scenarios**:

1. **Given** an authenticated user on the `/modules` page, **When** they click "Create Module", **Then** the Module Builder wizard opens displaying archetype choices (`steamcmd`, `java`, `generic`).
2. **Given** an archetype selected in the wizard, **When** the author adjusts parameters (name, ports, image, env), **Then** the wizard renders live previews of `module.yaml` and `template.yaml` and executes validation feedback without page reload.
3. **Given** invalid configuration entered in the wizard (such as an unpinned default image or invalid port), **When** validation evaluates, **Then** error markers appear next to the invalid fields with remediation tips.
4. **Given** a valid module configuration, **When** the author clicks "Download Bundle", **Then** the browser downloads a valid `.tar.gz` bundle archive containing `module.yaml`, `template.yaml`, `README.md`, and `icon.png`.
5. **Given** a valid module configuration and an available `upload`-type ModuleSource, **When** the author clicks "Install to Cluster", **Then** the bundle is uploaded to the cluster source and appears in the Modules catalog immediately.

---

### Edge Cases

- **Special Characters and Naming Conventions**: Module names containing uppercase letters, underscores, or invalid DNS-1123 characters must be rejected during scaffolding with guidance on valid naming rules (`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`).
- **Missing or Corrupted Dependencies**: If an author runs validation or scaffolding in an environment without external network access or container runtimes, offline features (scaffolding, schema validation, local manifest preview) must function fully without network calls.
- **Floating Image Tags vs Pinned Digests**: Scaffolding a module using a floating image tag (e.g., `:latest`) must guide the author to resolve and pin the digest, while respecting explicit `# gameplane:floating` annotations where intentional.
- **Port Protocol Conflicts**: If an author declares duplicate port numbers across incompatible protocols or binds the same port multiple times, validation must detect and flag the collision.
- **Incompatible Schema Versions**: If a module references a `gameplaneMinVersion` higher than the current tooling version, validation must notify the author of potential version discrepancies.

---

## Requirements *(mandatory)*

### Functional Requirements

**Module Scaffolding & Generation:**

- **FR-001**: The system MUST provide a module scaffolding capability that creates a standard module directory layout containing `module.yaml`, `template.yaml`, `README.md`, and default icon assets.
- **FR-002**: The scaffolding capability MUST support both an interactive guided mode (prompting for game metadata, server archetype, container image, ports, volumes, and description) and a non-interactive mode (accepting configuration via flags or specification file).
- **FR-003**: The system MUST provide built-in starter archetypes for common game server patterns (including SteamCMD dedicated server, Java application server, and generic container server).
- **FR-004**: Generated `module.yaml` files MUST include all mandatory metadata fields (`apiVersion`, `name`, `displayName`, `version`, `game`, `summary`, `categories`) pre-populated with valid values and schema modelines.
- **FR-005**: Generated `template.yaml` files MUST include the official JSON Schema modeline (`# yaml-language-server: $schema=...`), valid container definitions, standard port declarations, storage mounts, and graceful shutdown signal settings.
- **FR-006**: Scaffolding MUST validate module names against Kubernetes DNS-1123 label standards before directory creation and reject invalid names with actionable feedback.
- **FR-007**: Scaffolding MUST NOT overwrite an existing module directory unless an explicit overwrite instruction is supplied.

**Offline Validation & Linting:**

- **FR-008**: The system MUST provide an offline validation tool that validates module files against `module.yaml` metadata specifications and `GameTemplate` CRD schemas without requiring a running Kubernetes cluster.
- **FR-009**: The validation tool MUST check for image digest pinning on all default container images and report an error if a default image uses an unpinned floating tag without an opt-out annotation.
- **FR-010**: The validation tool MUST verify category values against the canonical Gameplane catalog taxonomy and report warnings or errors for unrecognized categories.
- **FR-011**: The validation tool MUST check port declarations for valid port ranges (1–65535), valid protocols (TCP, UDP), and absence of duplicate port collisions within the module.
- **FR-012**: The validation tool MUST validate `configSchema` definitions, ensuring all declared types (`string`, `int`, `enum`, `boolean`, `password`), default values, enum lists, and `autoFromMemoryLimit` parameters conform to specifications.
- **FR-013**: The validation tool MUST output structured, human-readable diagnostic messages containing the file name, line number (where available), problem description, and recommended remediation.

**Manifest Rendering & Preview (Dry-Run):**

- **FR-014**: The system MUST provide a dry-run preview capability that evaluates a module's `template.yaml` against user-provided or default configuration inputs and outputs the synthesized server specifications.
- **FR-015**: The preview capability MUST correctly compute dynamic configuration values, including memory-proportional fields derived via `autoFromMemoryLimit`.
- **FR-016**: The preview capability MUST allow selecting specific version entries defined in `spec.versions` and display the resulting container image, environment variables, and loader associations.

**Packaging & Asset Integrity:**

- **FR-017**: The system MUST provide a packaging helper that bundles the module directory into an OCI-compliant artifact structure with correct layer media types and title annotations.
- **FR-018**: The packaging helper MUST enforce bundle size sanity checks, warning if asset files exceed recommended limits.

**Web Dashboard Module Builder:**

- **FR-019**: The backend API (`api/`) MUST provide endpoints for scaffolding module files (`POST /modules/builder/scaffold`), running validation (`POST /modules/builder/validate`), previewing runtime manifests (`POST /modules/builder/preview`), and exporting bundle archives (`POST /modules/builder/export`).
- **FR-020**: The web dashboard MUST provide a Module Builder interface accessible from `/modules` enabling guided archetype selection, live validation feedback, manifest preview, bundle archive download, and direct in-cluster installation.
- **FR-021**: The Module Builder UI MUST be designed first in `design.pen` via the Pencil MCP server and exported to `design-export/` before frontend React implementation, satisfying Constitution Principle II.

---

### Key Entities

- **Module Directory**: The root workspace folder for a game module (e.g., `modules/<name>/`) containing all source manifests and assets.
- **Module Metadata (`module.yaml`)**: The manifest defining user-facing metadata, display name, category taxonomy, semver version, and compatibility constraints.
- **Game Template (`template.yaml`)**: The blueprint defining the game server runtime, container images, versions, ports, storage, environment variables, and configuration schema.
- **Starter Archetype**: A predefined template pattern tailored for a specific category of game servers (e.g., SteamCMD-based, JVM-based, or generic binary).
- **Validation Report**: The structured output of the linter identifying passing checks, warnings, and blocking errors with precise source references.
- **Rendered Server Preview**: The simulated runtime manifest resulting from resolving template rules against a specific version and configuration input set.
- **OCI Module Bundle**: The packaged artifact carrying the module's manifests and assets ready for distribution.

---

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: A new module author can scaffold a fully valid, schema-compliant game module directory from scratch in under 2 minutes using the interactive scaffolding workflow.
- **SC-002**: 100% of newly scaffolded modules pass offline validation with zero errors and zero warnings immediately upon creation.
- **SC-003**: Offline validation detects 100% of schema violations, unpinned default container images, invalid categories, and malformed port configurations without requiring network access or a live Kubernetes cluster.
- **SC-004**: Validation diagnostics provide actionable feedback (identifying exact field name and resolution advice) for all detected errors.
- **SC-005**: The preview capability renders computed configuration values and effective environment variables matching operator runtime logic with 100% fidelity.
- **SC-006**: Existing modules in the repository continue to pass validation with 0 regressions.

---

## Assumptions

- **Local Execution Environment**: The authoring tools run in standard developer environments (Linux, macOS) with standard command-line tooling available.
- **Schema Source of Truth**: The `GameTemplate` CRD openAPIV3Schema remains the canonical source of truth for template fields, with JSON Schemas generated directly from the CRD.
- **Non-Intrusive Workflow**: The module builder tools augment and simplify the authoring workflow without restricting authors who prefer to edit YAML files directly.
- **Category Canon**: The canonical list of categories (`Survival`, `Sandbox`, `Shooter`, `Simulation`, `Building`, `Adventure`, `Horror`, `Co-op`, `PvP`, `Modded`, `Creative`) is supported by default while allowing custom categories with appropriate validation notices.
- **Offline First**: All core scaffolding, linting, and preview operations execute completely offline without requiring internet access or registry connectivity.

---

## Verification Required Before Implementation

**Claim 1: JSON Schema availability for GameTemplate CRD**
*Verified:* The project contains `hack/gen-module-schema.py` and `make module-schema`, which extract the openAPIV3Schema from `charts/gameplane/crds/gameplane.local_gametemplates.yaml` and generate `modules/.schema/gametemplate.schema.json`.

**Claim 2: Module validation and pinning infrastructure**
*Verified:* `validate.py` and `make module-pin` provide foundational validation and digest resolution logic that can be integrated and exposed through the unified authoring toolkit.

**Claim 3: OCI Artifact packaging specification**
*Verified:* `modules/build.sh` defines the canonical OCI artifact media types and ORAS invocation parameters for packaging modules.

---

## Out of Scope

- **Automated Game Reverse Engineering**: The tool will not automatically determine game network protocols or internal configuration formats from closed-source binaries.
- **Live Cluster Provisioning within Scaffolder**: The scaffolding and validation tools do not provision or manage live Kubernetes clusters.
- **Graphic Design Generation**: The tool creates placeholder icon files but does not generate custom graphic art for game icons.
- **Proprietary Game Binary Distribution**: Modules only reference container images and download scripts; the toolkit does not distribute copyrighted game server binaries.
