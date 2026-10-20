# Feature Specification: Modular Dev Containers

**Feature Branch**: `claude/project-thread-427u3v`

**Created**: 2026-10-20

**Status**: Draft

**Input**: User description: "Add \"modular\" / \"multiple\" dev container setup that range from each component need. to \"part\" of the project (web including api, web, etc...) to full project this will lead to a better experience for dev". Added by valgul on 2026-10-20: "also make it so each component list it's required system deps".

## Context

Today the repository has one dev container (`.devcontainer/devcontainer.json` plus `.devcontainer/post-create.sh`). It always installs everything: both language toolchains, an in-container Docker daemon, a local Kubernetes toolchain, the module packaging tool, the Go linter, test cluster assets, every Go module's dependencies, the dashboard's packages, the spec tooling, four AI coding CLIs and about twenty editor extensions. It also initialises both submodules. A contributor who only wants to change the website, one game module or a dashboard screen still waits for and carries all of it.

No component lists its own system dependencies; the only record is the shared setup script. Versions already drift between the dev container and the task runner: the dev container installs `kubectl` 1.31 while test cluster assets are Kubernetes 1.36.2, and the setup script installs `setup-envtest@latest` while the `Makefile` pins `setup-envtest@release-0.24` (checked 2026-10-20, `Makefile:166-171`, `.devcontainer/devcontainer.json`, `.devcontainer/post-create.sh`).

This feature replaces "one size" with a set of dev container profiles at three sizes: one component, one part of the project (a group of components that are worked on together, such as the dashboard with its API), and the full project. The full profile keeps today's behaviour. Each component also lists its own required system dependencies (toolchains, command-line tools, OS packages, services) in one place, and profiles are built from those lists, so what a component needs is written down once and visible to anyone, inside or outside a dev container.

## Clarifications

### Session 2026-10-20 (valgul, on decision cards)

- Q: Which profiles should spec 026 start with? (OD-1) → A: Main components + 3 parts. Component profiles: web, api, operator, agent, website, modules, and one "Go services and libraries" profile for the smaller Go modules. Parts: "dashboard + API", "control plane" (operator, api, agent, chart, local cluster, end-to-end tests) and "modules + gp-module". Plus full.
- Q: Should the dashboard + API part include a local Kubernetes cluster? (OD-2) → A: Yes, include kind (in-container Docker and a local cluster) so the dashboard shows real servers.
- Q: Where should the website and module profiles live? (OD-3) → A: Also in the website and module repos, so each works when cloned on its own.
- Q: How should spec 026 meet the E2E rule (Principle I)? (OD-4) → A: The CI build job is the E2E path; no `test/e2e/` Go test.
- Q: Should the full profile fix today's version drift? (OD-5) → A: Align to the Makefile/CI values: kubectl 1.36, setup-envtest `release-0.24`, helm pinned. FR-003 allows exactly these changes.

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Open a small profile for one component (Priority: P1)

A contributor who works on a single component (for example the dashboard, the website or one game module) opens the repository, picks that component's profile from the list their editor shows, and gets a workspace that has only what that component needs. Its first build check works as soon as setup finishes.

**Why this priority**: This is the main gain the feature promises: faster setup and a lighter workspace for the most common, narrow change.

**Independent Test**: Open each component profile in a fresh workspace, wait for setup to finish, run that component's compile check from the ready message, and confirm tools that belong only to other components are absent.

**Acceptance Scenarios**:

1. **Given** a fresh clone, **When** the contributor opens the repository in a dev container, **Then** they are offered a named list of profiles that includes each component profile, each part profile and the full profile.
2. **Given** the dashboard component profile, **When** setup finishes, **Then** the dashboard's packages are installed, its compile check passes, and no Kubernetes cluster tooling, Go module downloads or test cluster assets were installed.
3. **Given** the website component profile, **When** setup finishes, **Then** the website submodule is ready and its build works, and the game-module submodule was not fetched.
4. **Given** any component profile, **When** setup finishes, **Then** a ready message lists the commands that work in this profile and names the profile to use for anything else.

---

### User Story 2 - Open a part profile for a group of components (Priority: P2)

A contributor who works across components that change together (for example a dashboard screen plus the API route behind it) picks a part profile. It contains the union of what those components need, plus whatever they need to run together, so the contributor can run the pieces side by side and see the change working.

**Why this priority**: Many real changes cross one component boundary. Without parts, those contributors fall back to the full profile and lose most of the gain.

**Independent Test**: Open the "dashboard and API" part profile, run both compile checks, start the dashboard dev server and confirm it reaches a running API the way the profile documents.

**Acceptance Scenarios**:

1. **Given** the "dashboard and API" part profile, **When** setup finishes, **Then** the dashboard, the API and the shared libraries the API depends on all pass their compile checks.
2. **Given** the "dashboard and API" part profile, **When** the contributor follows its ready message to run the stack, **Then** the dashboard dev server loads in their browser through a forwarded port and talks to a running API. The part includes a local Kubernetes cluster, so the API runs in it with the operator and agents and the dashboard shows real game servers (OD-2).
3. **Given** any part profile, **When** setup finishes, **Then** it contains every tool of each component profile it covers, at the same versions.

---

### User Story 3 - Keep the full profile for whole-project work (Priority: P2)

A maintainer who works across the whole project (releases, end-to-end debugging, cross-cutting refactors, design work) keeps a profile that has everything the current dev container has. Nothing they rely on today goes missing.

**Why this priority**: The current dev container is in daily use. Splitting it must not take anything away from people who need all of it.

**Independent Test**: Open the full profile and compare its installed tools, versions, forwarded ports, editor extensions and settings against the dev container on `master` before this feature. Nothing is missing.

**Acceptance Scenarios**:

1. **Given** the full profile, **When** setup finishes, **Then** every tool, version, forwarded port, editor extension and editor setting of today's dev container is present, except that kubectl, setup-envtest and helm use the aligned versions from OD-5.
2. **Given** an editor that opens the repository without the contributor picking a profile, **When** it starts a dev container, **Then** it uses the full profile, so existing workflows keep working.

---

### User Story 4 - Find the right profile (Priority: P3)

A new contributor reads the contributor documentation and learns which profiles exist, what each one includes, roughly how long it takes to set up, and which to pick for the change they have in mind.

**Why this priority**: Profiles only help if people pick the right one. It comes after the profiles themselves exist.

**Independent Test**: Give the documentation to someone who has not used the repository and three example changes; they name the right profile for each.

**Acceptance Scenarios**:

1. **Given** the contributor documentation, **When** a reader looks up dev containers, **Then** they find one table listing every profile, its scope, what it installs and when to use it.
2. **Given** the public website's local development page, **When** a reader opens it, **Then** it describes the profiles and links to the same table.

---

### User Story 5 - Keep profiles in step (Priority: P3)

A maintainer bumps a tool version or adds a new component. All profiles that use that tool get the new version together, and a CI check fails if a profile drifts, fails to build, or is missing a tool its scope needs.

**Why this priority**: Several profiles multiply the maintenance cost. Without one source for versions and a check, they will drift.

**Independent Test**: In a branch, change one tool version in the single version source and confirm every affected profile picks it up; then break one profile on purpose in a branch and confirm the CI check reports which profile and why.

**Acceptance Scenarios**:

1. **Given** a tool version change in the single version source, **When** profiles are rebuilt, **Then** every profile that includes that tool uses the new version.
2. **Given** a pull request that changes any profile or the version source, **When** CI runs, **Then** each affected profile is built and its scope's compile checks run, and the run fails naming the profile if any step fails.

---

### User Story 6 - See what a component needs (Priority: P1)

A contributor (or a maintainer setting up a machine without dev containers) opens a component's folder and finds a list of the system dependencies that component needs to build and run: each toolchain, command-line tool, OS package and service, with its required version and why it is needed. Profiles are assembled from these lists, so the list and the profile never disagree.

**Why this priority**: valgul asked for it explicitly, and every profile depends on it: a component profile is only as correct as the component's dependency list.

**Independent Test**: Pick any component, read its dependency list, set up a plain machine with only those items, and confirm the component's compile check passes. Then confirm the component's profile installs exactly the items on the list plus the shared baseline.

**Acceptance Scenarios**:

1. **Given** any component in the profile catalogue, **When** a reader opens that component's folder, **Then** they find its system dependency list in a fixed, documented place and format.
2. **Given** a component's dependency list, **When** its profile is built, **Then** the profile installs every item on the list at the listed version, and nothing outside the list and the shared baseline.
3. **Given** a part or full profile, **When** it is built, **Then** its contents are the combined lists of the components it covers plus the shared baseline and any part-level extras.
4. **Given** a change that adds a tool to a profile without adding it to a component's list (or the baseline, or a part's extras), **When** CI runs, **Then** the check fails naming the tool and the profile.

---

### Edge Cases

- A contributor in a component profile runs a command that needs a tool from another scope (for example starting a local cluster from the website profile). The command stops with a message that names the missing tool and the profile that has it, instead of a raw "command not found".
- A contributor switches profile on the same clone. The working tree, branches and uncommitted changes are untouched, and setup for the new profile completes without manual cleanup.
- A submodule a profile needs is not initialised yet. Setup initialises only the submodules that profile needs.
- Setup runs again on an existing container (rebuild or restart). It is idempotent, as today's setup is.
- A machine is on arm64. Every profile works on both amd64 and arm64, as today's dev container does.
- A new Go module is added to the workspace. The procedure for adding it to a profile is documented, and the CI check fails if a module is in no profile at all.
- Two components list the same tool. A profile covering both installs it once, at the single version from the version source.
- A component needs nothing beyond the shared baseline. Its list says so explicitly instead of being missing.
- Setup has no network for an optional step (for example an AI CLI install). Setup finishes with a warning, as today, instead of failing.

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: The repository MUST offer several named dev container profiles that a dev-container-capable editor lists for the contributor to pick from when opening the repository.
- **FR-002**: Profiles MUST come in three sizes: component profiles (one component each), part profiles (a named group of components worked on together) and one full profile. The initial catalogue (OD-1) is: component profiles for web, api, operator, agent, website and modules, plus one "Go services and libraries" profile covering every other Go module in the workspace; part profiles "dashboard + API" (web, api, the shared libraries they use, and a local Kubernetes cluster per OD-2), "control plane" (operator, api, agent, the Helm chart, a local Kubernetes cluster and the end-to-end test suite) and "modules + gp-module" (game modules and the module authoring tool); and the full profile.
- **FR-003**: The full profile MUST include everything today's dev container includes: every tool and version, forwarded port, editor extension, editor setting, environment variable and setup step. The only allowed version changes are the three drift fixes from OD-5: kubectl 1.31 → 1.36, `setup-envtest@latest` → `@release-0.24`, and helm `latest` → a pinned version. It MUST be the profile used when no profile is picked.
- **FR-004**: Each component and part profile MUST install only the tools, dependencies, submodules, editor extensions and forwarded ports its scope needs, as given by its components' dependency lists and the profile catalogue.
- **FR-004a**: Every component in the profile catalogue MUST list its required system dependencies in its own folder, in one documented location and format used by all components. Each entry names the dependency, its kind (toolchain, command-line tool, OS package, service or submodule), the version it needs (taken from the version source) and a one-line reason. A component with no needs beyond the shared baseline MUST still have a list that says so.
- **FR-004b**: Profiles MUST be assembled from the component dependency lists (plus the shared baseline and any part-level extras); the lists are the source of truth, not the profile definitions.
- **FR-005**: A part profile MUST include every tool of each component profile it covers, plus whatever its components need to run together.
- **FR-006**: Every profile MUST include a shared baseline: version control with signed-commit support, the GitHub CLI, the spec tooling, the AI coding CLIs and the AI assistant editor extension, and the project's shared editor settings.
- **FR-007**: Every tool version MUST be defined once, in a single source that all profiles read, so one change updates every profile that uses that tool.
- **FR-008**: When setup finishes, each profile MUST have fetched the dependencies its scope needs, so the compile checks listed in its ready message pass without further setup.
- **FR-009**: When setup finishes, each profile MUST print a ready message that lists the commands that work in it, which forwarded ports it opens, and which profile to use for work outside its scope.
- **FR-010**: Project task-runner commands that need a tool a profile does not install MUST stop with a message that names the missing tool and a profile that has it.
- **FR-011**: Setup in every profile MUST be idempotent and MUST NOT fail as a whole because an optional step (such as an AI CLI install) fails; it reports a warning instead.
- **FR-012**: No profile MAY contain credentials, tokens or personal sign-ins. Tools that need them authenticate at runtime, as today.
- **FR-013**: Every profile MUST work on amd64 and arm64.
- **FR-014**: A CI check MUST build each profile that a pull request affects, run the compile checks of that profile's scope, and fail naming the profile on any error. It MUST also fail when a Go module in the workspace belongs to no profile, when a profile pins a tool version outside the single version source, when a component in the catalogue has no dependency list, or when a profile installs something that no list, baseline or part extra accounts for.
- **FR-015**: Switching between profiles on the same clone MUST NOT change the working tree, and MUST NOT need a fresh clone or manual cleanup.
- **FR-016**: The contributor documentation MUST contain one profile table (name, size, scope, what it installs, typical setup time, when to use it) and the steps to add or change a profile. The public website's local development page MUST describe the profiles and link to that table.
- **FR-017**: The website and modules component profiles MUST exist both in the main repository and in the website and module repositories themselves, so each works when that repository is cloned on its own (OD-3). The copy in a submodule repository MUST use the same tool versions as the main repository's version source, and each of those repositories MUST run the FR-014 check for its own profile.

### Key Entities

- **Profile**: A named dev container setup. Has a size (component, part or full), a scope (the components it covers), the tools and versions it installs, the submodules it initialises, the dependencies it fetches, its forwarded ports, its editor extensions and its ready message.
- **Component**: One buildable unit of the project, such as the dashboard, the API, the operator, the agent, a supporting service, the website or the game modules. Each has the tools it needs and its compile check.
- **Part**: A named group of components worked on together, with any extra tools they need to run side by side.
- **Component dependency list**: A component's own declaration of the system dependencies it needs. Each entry has a name, a kind, a version reference into the version source and a reason. Lives in the component's folder.
- **Shared baseline**: The tools, extensions and settings every profile includes.
- **Version source**: The single place that defines each tool's version for all profiles.
- **Profile catalogue**: The documented list of profiles, their scopes and contents, which the CI check and the documentation both follow.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: On the same machine, every component profile finishes setup in at most half the time the full profile takes, measured from a cold start with no cached images.
- **SC-002**: Every component profile uses at most half the disk space of the full profile once setup finishes.
- **SC-003**: The full profile has 100% of the tools, versions, ports, extensions and settings of today's dev container (zero missing items in the comparison of User Story 3), apart from the three OD-5 version alignments.
- **SC-004**: 100% of profiles pass the CI check on both amd64 and arm64 on the pull request that adds them, and on every later pull request that changes them.
- **SC-005**: Across all profiles, each tool has exactly one version (zero drift), as reported by the CI check.
- **SC-006a**: 100% of components in the catalogue have a dependency list, and for each one a plain machine with only the listed items plus the baseline passes that component's compile check.
- **SC-006**: A contributor new to the project picks the right profile for three example changes using only the documentation, in under 5 minutes.

## Assumptions

- "Dev container" means the open dev container format that VS Code, GitHub Codespaces and the dev container command-line tool all read. Profiles must work in all three, the same as today's single dev container.
- Component profiles are for editing and compile checks of that component. Running the whole stack, end-to-end tests and anything that needs a local Kubernetes cluster live in the full profile and in the "dashboard + API" and "control plane" parts.
- Following the project's rules, tests and lint run in CI, not in the dev container. Profiles still install the tools those suites need (for example the Go linter) where the scope uses them, as today, so contributors who choose to use them can. The ready messages keep pointing at CI as the place that verifies.
- The shared baseline keeps today's AI coding CLIs and the AI assistant editor extension in every profile. Purely personal editor items (colour themes, and extensions for languages the project does not use) stay in the full profile only.
- Design work in Pencil happens on the maintainer's own machine, not inside any dev container; the Pencil extension stays in the full profile as today.
- Constitution Principle I (end-to-end testing) is met by the CI check in FR-014: it builds each real profile and runs its real compile checks, which is the end-to-end path for developer tooling. No `test/e2e/` Go test is added (OD-4, valgul 2026-10-20).
- A component's dependency list is for system-level needs (toolchains, CLIs, OS packages, services, submodules). Language-level libraries stay in each component's existing package manifests and are not repeated.
- The current dev container on `master` at commit `8a67f2e` is the baseline for "today's dev container".
