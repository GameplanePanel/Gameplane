# web — Specification

**Status:** pre-v1 (v0.3.0)  
**Module / package:** @gameplane/web  
**Build:** Vite 8.3 + React 19.3 + TypeScript 6.0 (strict)

## Purpose

The Gameplane dashboard is a React SPA providing a UI layer over the Gameplane API. It exposes Kubernetes GameServer/GameTemplate/Backup/Module operations through browser-based workflows: server creation and lifecycle, mod/player management, file browsing, backup scheduling, multi-cluster routing, RBAC configuration, and audit trails. All persistence and reconciliation logic lives in the operator and API; the dashboard is a pure UX adapter.

## Responsibilities

- Render all authenticated Gameplane CRDs and operations as React pages and interactive forms
- Handle user authentication (local + OIDC) and session management (cookies + CSRF tokens)
- Fetch and cache data via TanStack Query; invalidate caches on Kubernetes watch events (SSE)
- Stream console input/output (WebSocket) and pod/game logs (WebSocket) to the Console and Logs tabs
- Wrap the API client in three layers: thin fetch wrapper (`api<T>()`), typed endpoint namespaces (`Servers`, `Templates`, `Cluster`, etc.), and domain helpers (validations, RCON mode resolution, mod/modpack capability detection)
- Present authorized resources from all locations by default, with optional filters; bind each resource operation to its explicit cluster and namespace rather than an ambient global selection
- Enforce role-based access control (RBAC) on frontend routes via `<RequirePermission>` middleware
- Validate form inputs against declared server/template schema before submit
- Display real-time resource status (CPU, memory, uptime) and live action metrics from RCON/agent

### Standalone panel installations

- Authenticated `GET /admin/installation` reports `standalone` and `localCluster`. The dashboard waits for this capability response before making local infrastructure or module requests; a failed capability request does not imply a local cluster.
- Standalone `/clusters` may be empty. The UI must not synthesize a local registration or fetch local inventory, templates, or module catalogs in that state. Existing fleet pages continue to render their empty states.
- Cluster managers can register an existing workload cluster from the Clusters page using its name, display name, and self-contained kubeconfig. Registration does not install an operator or make the panel host a workload node. Removing a registration requires confirmation and leaves running workloads intact. Agent features additionally require remote gateway configuration through the documented API.
- On standalone installations, Modules and Admin → Module sources require an explicitly selected registered cluster. Catalogs, source mutations, module installation, uploads, builder installation, and deployment use that same captured target. Switching the target remounts dialogs so pending form state cannot be submitted to another cluster.
- The Modules page exposes its source editor directly, so a workload module manager does not need central configuration permission to manage sources. Source controls use the catalog's target and permissions.
- Standalone backup destination settings also require an explicitly selected registered workload cluster. Destination reads and mutations retain that target; switching clusters discards open destination forms.
- Combined installations retain the local module catalog and local module deployment behavior. Account, authentication, configuration, registry, fleet, and sharing endpoints remain management APIs independent of workload selection.
- Standalone About and Updates sections do not query local Kubernetes information; upgrades use panel/API container images.
- Standalone System logs shows container-runtime guidance without opening a local Kubernetes log stream. Module reads and management controls check permissions against the selected workload cluster (local on combined installations); permissions granted on another cluster do not enable these controls.
- The local-only notifications SSE subscription starts only after installation capabilities confirm a local cluster. Standalone dashboards retain polling and do not retry an unscoped `/events` stream against the panel host.
- Standalone user grants require explicit remote selection, omit the local workload option, and keep the primary panel role separate. Remote cluster-wide roles may contain module read/manage and template read/write permissions as well as inventory and namespaced permissions.

## Non-goals / Boundaries

**Design-first rule:** Any change to the dashboard's visual surface (new page, new form field, layout shift, color/icon change) originates in `design.pen` (Pencil MCP server), not in React code. The Pencil file is the source of truth for all UI design; code-led redesigns are reverted. See docs/architecture.md and CLAUDE.md rule 1.

**API-only:** The dashboard never speaks directly to the operator or agent. All reads/writes flow through the Kubernetes API server and the REST/WebSocket API gateway (`api/`).

**Pre-auth privacy:** The login page and any unauthenticated screen must not leak internal state: no hostnames, cluster names, server counts, version strings, user lists, or "user not found" errors. See docs/security.md "pre-auth privacy" and CLAUDE.md rule 3.

**Cluster-bound WebSocket:** Server streams carry the open server's explicit cluster and namespace. Reconnects retain that target; leaving the server view closes its streams. Changing an infrastructure selection cannot retarget a stream. Pod logs and PTY attach use the target Kubernetes client; remote agent streams use the configured authenticated gateway and fail closed when unavailable.

## Unified location views

The [unified dashboard](../docs/unified-dashboard.md) combines authorized
resources across locations. [Cluster administration](../docs/multicluster-ui.md)
retains a separate inventory selection. `design.pen` and the
[design exports](../design-export/MANIFEST.md) define the visual surface.

- Dashboard, Servers, Backups and global Search consume `/fleet/*` envelopes. Exact location/namespace filters request narrower backend results so they can recover resources omitted by the combined result limit. Authorized scope metadata keeps filter options available for empty or unavailable locations.
- Each row retains `{cluster, namespace, name, uid}`, resource data and exact target permissions. Same-named resources in different locations remain distinct. Server routes carry `cluster` and `ns`; legacy routes without a cluster resolve local, independently of remembered infrastructure selection.
- `createRequestClient`, `createResourceClient` and `ResourceTargetProvider` bind JSON requests, raw file/download paths, dialogs, chained mutations and WebSocket URLs to the original scope. Detail waits for matching resource/access identity before mounting actionable children. Instance query keys include UID; the current server-object lookup and namespace collections use stable scope keys so a replacement object can be detected and forms remounted.
- Server controls consume `/servers/{name}/access` capabilities and exact named permissions. Owner/collaborator operations remain distinct from full-object writes, owner/admin management, strictly owner-only share links, backup permissions and capture management. The API remains authoritative.
- Fleet totals visibly distinguish complete, partial, failed and truncated reads. CPU/memory percentages use summed measured usage divided by matching capacity; missing measurements are unknown. Storage reports provisioned capacity, not actual disk usage. Player totals indicate missing server readings.
- Cluster registration and node selection remain in infrastructure administration; ordinary pages have no global cluster switcher. Accounts, roles, modules, audit and installation settings retain central ownership.
- Creation uses eligible placements and templates supplied by `/fleet/placements`. An initial location is captured once, changing location resets the draft, and losing that location does not automatically choose another. All creation follow-ups and success navigation retain the captured target. This adds no cross-cluster scheduler or data migration.

Coverage includes duplicate-name navigation, exact permission boundaries, asynchronous target retention, unavailable and capped responses, weighted metrics, and desktop/mobile filtering. Repository suites run in GitHub Actions.

## HeroUI Component Layer

The dashboard's visual surface composes its foundational atom components on `@heroui/react` (HeroUI v3) primitives, `@heroui/styles`, and Gameplane brand design tokens. These atoms reside in `web/src/components/ui/` — the Gameplane composition layer over HeroUI — and form the basis for every screen:

- **StatCard** — displays a metric with label, value, and optional trend indicator
- **PhaseChip** — status badge rendering server phase (Running/Stopped/Pending/Failed) with semantic color
- **PageHeader** — page title, description, and action button slot
- **ConfirmDialog** — high-stakes confirmation modal with warning styling
- **DropdownMenu** — contextual menu with keyboard navigation and icon support
- **FilterPopover** — filter/search popover with form controls
- **LoadingCard** — skeleton placeholder during data fetch
- **ErrorCard** — error state card with message and recovery action slot
- **ErrorBanner** — top-of-page alert for form/request errors; shows short status text instead of raw HTML when APIError.isHTML is set
- **Meter** — horizontal progress indicator with percentage display
- **Sparkline** — mini inline chart for resource trends (CPU, memory)
- **GameIcon** — cached game-specific icon with fallback

Each atom composes HeroUI's headless react-aria-components (Adobe React Aria) and applies Gameplane's brand palette — orange accent, dark default mode, light mode supported — via HeroUI's semantic token layer. Token mapping from Gameplane brand values to HeroUI variables is documented in `specs/done_014-heroui-web-rebuild/contracts/theme-tokens.md` (FR-013).

The multi-slice rebuild is complete: the previous Radix-based primitives that used to live in `web/src/components/ui/` have been deleted, and `web/src/components/hero/` was renamed to `web/src/components/ui/` to become the permanent HeroUI composition layer (OD-7, Settled 2026-09-03). **No screen imports `@radix-ui/*` or `class-variance-authority`** — this was enforced mechanically at review throughout the rebuild (FR-012) and is now enforced by their absence from `web/package.json`.

## T057 — Login Privacy Compliance (FR-005)

**Verdict: PASS.** The login page (`web/src/routes/Login.tsx`) and all pre-auth hero components comply with FR-005 (login privacy).

**Audit findings:**

1. **Login.tsx pre-auth surface (lines 26–233):**
   - ✅ No cluster name, version string, or hostname rendered
   - ✅ No server count, deployment metrics, or internal status
   - ✅ No user enumeration signals (error copy at line 94 is neutral: "Invalid credentials" or "Network error" only)
   - ✅ Marketing panel (lines 196–231) contains static product copy only (no internal metrics)
   - ✅ Explicit comment (lines 23–25) guards this rule at source

2. **HeroUI components in pre-auth context:**
   - Only `Input`, `Label`, `InputGroup` from `@heroui/react` are used before auth (lines 17–18)
   - No other ui/* components are rendered pre-auth (AppLayout, Sidebar, TopBar, etc. all require `useMe()`, which 401-redirects unauthenticated users)
   - All HeroUI imports follow FR-012 (from "@heroui/react" and "@/components/ui/" only; no @radix-ui or class-variance-authority leakage)

3. **SSO button labels (SSOButtons, lines 249–267; MarketingRow, lines 269–278):**
   - Provider display names come from the pre-auth `Auth.providers()` API response (`p.label`, line 262 in SSOButtons)
   - Never issuer URLs or internal identifiers
   - Compliant with the login-privacy rule

**Conclusion:** No metrics, hostnames, versions, cluster names, or enumeration hints leak on the unauthenticated surface. Error copy is neutral and user-facing. The pre-auth privacy invariant (CLAUDE.md rule 3) is satisfied.

## T058 — Slice 1: Shell + Login Architecture

**Scope:** Slice 1 (tasks T040–T053, feature 014) rebuilt the authenticated shell (AppLayout, Sidebar, TopBar) and the login page on HeroUI, establishing the visual layer for all downstream screens. The old Radix-based primitives that used to live in `web/src/components/ui/` were fully removed once slices 2–5 migrated their respective screens (see T198, T201).

### Components Added (T045–T052)

**New composition root:**
- **`AppLayout.tsx` (T042)** — layout orchestrator: composes `AppShell` (layout), `Sidebar` (fixed or drawer variant), `TopBar` (breadcrumbs + search + notifications), and the authenticated page outlet. Owns identity loading, 401 redirects, permission-aware navigation and appearance integration. The ordinary shell has no global cluster selector; infrastructure navigation uses the registry's inventory capabilities.

**New layout atoms (T045–T048, T050, T052):**
- **`ui/AppShell.tsx` (T045)** — pure layout wrapper: renders sidebar fixed-width, topbar + main content in flex column. No mobile logic (owned by Sidebar's drawer variant).
- **`ui/Sidebar.tsx` (T046, T052)** — left navigation: fixed variant (always visible on desktop) or drawer variant (mobile off-canvas). Renders nav groups + items, active route highlighting, appearance toggle footer, and user-info footer with logout. Permission gating is computed by AppLayout; Sidebar renders whatever nav items it receives.
- **`ui/TopBar.tsx` (T047)** — horizontal header: hamburger (mobile), breadcrumb slot, cluster selector slot, global search slot, notifications slot, user menu (avatar + logout). All four slots are ReactNode — each slot component fetches its own data (no centralized fetching in TopBar).
- **`ui/Breadcrumbs.tsx` (T048)** — route-hierarchy breadcrumbs: renders the tree path (gameplane / Servers / my-server) using `buildCrumbs` logic kept from the old AppLayout. Distinct from `ui/PageHeader.tsx`'s internal page-level breadcrumbs.
- **`ui/NotificationsPanel.tsx` (T049)** — bell icon + popover + SSE notification list: owns the `openEventStream` subscription and local state (same as today's `Notifications()` in AppLayout). Fetching moved into the component, not centralized in AppLayout. Each watch event schedules an invalidation of the queries that show its kind (`queryFilterForKind` in `lib/sse.ts`: the `["templates"]` prefix, or a predicate matching the `["fleet", <kind>, …]` lists, the per-server `["resource", …, "collection", <kind>]` lists and, for servers, the server-detail `["resource", …, "server"]` query), and events of the same kind within 500 ms (`INVALIDATE_COALESCE_MS`) share one invalidation. A MODIFIED event is ignored (no notice, no invalidation) when the object's `(metadata.generation, status.phase)` signature equals the last one seen for it, which filters out the agent's ~20 s status heartbeat patches; ADDED and DELETED are always shown, DELETED forgets the signature. If a fetch for that kind is already in flight when the timer fires, the invalidation waits for it to settle (re-checking every 500 ms) and then refetches. The in-flight read is never cancelled, which would starve it under a steady event stream, and its possibly pre-event result is never taken as current. Pending timers are cleared on unmount.
- **`ui/GlobalSearch.tsx` (T050)** — search field + results popover: owns the `useQuery` call for servers list and client-side filter. Kept from today's AppLayout.
- **`ui/AppearanceToggle.tsx` (T052)** — three-state appearance mode selector (light/dark/system): dispatches `onChange` to parent (AppLayout), which owns the `useTheme()` hook. Uses HeroUI `ToggleButtonGroup` or fallback three-button set.

**Refactored on HeroUI:**
- **`PageHeader.tsx` (T043)** — route-level page header: thin wrapper around `ui/PageHeader` (slice-0 atom), passing through `title/subtitle/actions/breadcrumbs` unchanged. Called by ~30+ route pages; no changes required in call sites.
- **`ClusterSelector.tsx` (T044)** — HeroUI dropdown on the Cluster inventory page, with authorized registrations, phase colors and a **View all clusters** action. It changes the infrastructure selection without setting a default target for server operations.
- **`Login.tsx` (T040)** — login form: refactored from raw DOM to HeroUI `TextField`/`Label`/`Input`/`InputGroup`, `Alert` for errors, `Button` for actions. Kept all state, error handling, SSO provider rendering, and marketing panel. Verified compliant with FR-005 (login privacy).
- **`Dashboard.tsx` (T041)** — landing page: renders fleet server/player summaries and inventory statistics, with optional location filtering and partial-coverage indicators. Missing measurements remain unknown. Recent-activity rows for audit entries whose path ends in `/auth/login` read "<actor> signed in" for a 2xx status and "<actor> failed to sign in" otherwise.

### Design Import Rule (FR-012)

Every file touched in slice 1 imports **only** from:
- `@heroui/react` (HeroUI components)
- `@/components/ui/` (Gameplane compositions over HeroUI)

**Forbidden imports (removed from the dependency tree entirely):**
- `@radix-ui/*` (raw Radix)
- `class-variance-authority` (replaced by HeroUI's variant system)

This rule was enforced by lint and review throughout the rebuild: any rebuilt file importing from forbidden sources failed CI. Both packages have since been removed from `web/package.json`.

### Theme Integration & Boot Script (T053)

**Appearance selection:** Two mechanisms work together to avoid theme flicker on page load:

1. **Boot script in `index.html`** (T053; extended by feature 016 — see "User Theme Customization" below):
   - Runs as the first script in `<head>` (before Google Fonts)
   - Reads `localStorage.getItem("gameplane-theme-prefs")` first (feature 016 prefs cache, authoritative); the legacy `gameplane-theme` key (light/dark/system only) remains the fallback when the prefs cache is absent
   - Resolves system theme via `window.matchMedia("(prefers-color-scheme: dark)")`
   - Sets `document.documentElement.classList` (add/remove "dark" and "light") and `document.documentElement.dataset.theme`, plus `data-theme-preset` ("pink" | "legacy") and `data-theme-type` ("preset" | "custom_colors")
   - Boot-time unauthenticated guard (FR-011): on `/login` and `/share/*` the cached prefs are never applied — those routes force pink/no-overlay chrome for their whole lifetime
   - Gracefully handles localStorage unavailability; keeps the dark default from markup

2. **`AppearanceToggle.tsx` + `AppLayout.tsx` (T052):**
   - AppLayout calls `useAppearance()` (custom hook) to get theme state and setter
   - Passes `theme` and `setTheme` as props to Sidebar
   - Sidebar renders `AppearanceToggle` with `value` and `onChange` callbacks
   - On toggle, AppearanceToggle calls the `onChange` callback (wired to `setTheme`)
   - `setTheme` syncs to localStorage; `useAppearance()` calls `applyTheme()` via useEffect on every theme change
   - `applyTheme()` (web/src/components/AppLayout.tsx lines 48–57) explicitly sets both `document.documentElement.classList` (adds/removes dark/light) and `document.documentElement.dataset.theme = resolved`, ensuring consistent theme application without requiring a separate hook from HeroUI

**Shipped default:** `<html class="dark" data-theme="dark">` in markup (dark theme as the no-JavaScript fallback; overridden by boot script if a stored preference exists).

### User Theme Customization (feature 016 — non-UI plumbing, landed 2026-09-21)

Spec: `specs/done_016-user-theme-customization/`. Both the non-UI plumbing and the UI have landed: preset tokens, the extended boot script, preferences sync, the isolation/safe-mode guards, the Theme Settings page (`ThemeSettings.tsx`), `SafeModeBanner`, the keyboard shortcut (`AppLayout.tsx`), the login-page safe-mode link (`Login.tsx`), the custom-colors derivation utility (`deriveCustomThemeTokens` in `lib/theme-derivation.ts`), and the export/import utilities (`lib/theme-export.ts`). Design specs live in `specs/done_016-user-theme-customization/contracts/` (theme-ui.md, theme-tokens-v2.md, theme-export.md). The `ThemeExport` wire type is declared in `src/types.ts` and is consumed by the export/import utilities.

**Preset token system (`web/src/styles/globals.css`):**
- The default (Pink) preset keeps the HeroUI semantic tokens from the slice-0 rebuild: `[data-theme]` / `.dark` / `.light` blocks define `--accent` (pink `#FF4FA3` dark / `#DB2777` light), surfaces, borders, fields, focus, link, etc.
- The **Legacy preset** adds `data-theme-preset="legacy"` blocks (dark + light) that rebind the same HeroUI semantic variables to the historical Gameplane orange palette — dark accent `oklch(69.11% 0.1944 44.01)` (#F97316), light accent `oklch(62.0% 0.20 40.0)` (#EA580C), dark ground `#0F0F0F`, card `#1C1C1C`, border `#292929`. Only CSS variables rebind, so every screen adapts with zero component changes (research.md R-02)
- The old `--gp-*` HSL triplets are retained alongside (renamed to avoid HeroUI token collisions) for legacy consumers
- The active preset is selected by the `data-theme-preset` attribute on `<html>` ("pink" default | "legacy")

**Accessibility guards (globals.css):**
- `@media (forced-colors: active)` — the OS palette wins: semantic tokens are restored to CSS system colors with `!important` (beats the higher-specificity preset selectors and any runtime-injected overlay), interactive controls get `forced-color-adjust: auto`, and links use `LinkText`
- `@media (prefers-contrast: more)` — field/border strokes (`--border`, `--separator`, `--field-border`) are strengthened to `CanvasText`

**Boot script hydration contract (inline `theme-boot` script, `index.html`, first script in `<head>`):**
- Storage key: `gameplane-theme-prefs` (localStorage), JSON shape `{ themeType, presetId, appearanceMode, customColors, customCssEnabled, customCss }` (the `UserThemePreferences` type minus `updatedAt`); the legacy `gameplane-theme` key (light/dark/system only) is the appearance-mode fallback when the prefs cache is absent
- Applies everything synchronously pre-paint (FOUC-free): `.dark`/`.light` class + `data-theme` (resolved from `appearanceMode` via matchMedia; markup's dark default survives unavailable matchMedia or storage), `data-theme-preset`, `data-theme-type`
- Unauthenticated guard: on `/login` and `/share/*` cached prefs are never applied (those routes pin pink/no-overlay themselves); only the legacy appearance key applies there
- Custom CSS overlay: when `customCssEnabled` and non-empty `customCss` and NOT safe mode, injects `<style id="gameplane-custom-css">` as the **last** `<head>` child (so user rules win at equal specificity); otherwise `data-custom-css="off"`
- Safe-mode skip: `?safe-mode=1` in the URL, or sessionStorage `gameplane-safe-mode` of `"1"`/`"true"`, suspends only the overlay (base theme still applies)

**`useThemePreferences` (`web/src/lib/useThemePreferences.ts`) — AppLayout-owned preferences pipeline:**
- Reconciles the backend `preferences` from `GET /users/me` over the localStorage cache: backend wins on drift (e.g. a change made on another device), rewriting the cache and re-applying the DOM
- `updatePreferences(patch)`: optimistic DOM + cache first, then `PUT /users/me/preferences` via `Users.updatePreferences`; on failure the previous state is restored and the error is reported via the `onError` callback **and** a `gameplane-theme-error` window `CustomEvent` (no toast UI yet — the banner/modal are deferred)
- Offline resilience: a connectivity failure (non-`APIError`, or `navigator.onLine === false`) queues the PUT and replays it when the browser fires `online`; the server response then re-reconciles cache and DOM
- PUT body follows FR-012 retention: optional custom fields are omitted when unset — never sent as explicit nulls
- Keeps `data-theme-preset` / `data-theme-type` / `data-custom-css` and the `#gameplane-custom-css` overlay in sync on every change ((re)append keeps the overlay last in `<head>`), and re-resolves the base theme when the OS preference flips while in "system" mode (matchMedia subscription)
- `AppLayout.tsx` consumes the hook through `useAppearance()`: when a profile is loaded, AppearanceToggle changes persist through the preferences pipeline (optimistic apply + PUT, syncing across devices); without a profile it falls back to the legacy `gameplane-theme` key + direct apply. `unmountCustomCssOverlay()` runs on AppLayout unmount and on logout — the overlay is an authenticated-only surface, and the stored stylesheet is never deleted by unmounting (disable-vs-reset)

**Unauthenticated isolation (FR-011) — route-lifetime pinning:**
- `enforceUnauthenticatedTheme()` (`web/src/lib/enforceUnauthenticatedTheme.ts`) pins `data-theme-preset="pink"`, `data-theme-type="preset"`, `data-custom-css="off"` on `<html>` and strips `#gameplane-custom-css` / `#gameplane-custom-theme-vars` for the route's lifetime, via a `MutationObserver` that reverts any flip (covers residue from a previously logged-in session on the same browser). Light/dark state is deliberately untouched
- Mounted by `Login.tsx` and `Share.tsx` as a route-level `useEffect(() => enforceUnauthenticatedTheme(), [])`; complements the boot-time guard in `index.html`, which only covers the pre-paint window

**Client endpoints:** `Users.getPreferences` / `Users.updatePreferences` / `Users.resetPreferences` (Layer 2 above); `Users.me()` returns `preferences` embedded (types.ts `User.preferences`)

### Test Count Rule (FR-010)

Each rewritten test file must maintain or exceed the original test count:
- `Login.test.tsx`: ✅ ported all cases (new file)
- `AppLayout.test.tsx`: ✅ 29 tests (old file had 28), porting every case including permission gating for nav items and 401-redirect behavior

Selectors updated to query by role (`getByRole("textbox", { name: /username/i })`, `getByRole("button", { name: /sign in/i })`, `getByRole("alert")`) since HeroUI markup changes internal DOM structure. No `data-testid` added unless HeroUI components genuinely lack accessible role/name.

### Playwright Specs TypeScript Check

**Status:** Clean — `npx tsc --noEmit` passes with no errors across all Playwright specs (`e2e/specs/*.spec.ts`, `e2e/globalSetup.ts`, `e2e/globalTeardown.ts`, `e2e/pages/*.ts`).

### Legacy Primitives (Removed)

The old Radix-based `ui/` components that shipped before this rebuild — `button.tsx`, `card.tsx`, `input.tsx`, `dialog.tsx`, `select.tsx`, `tabs.tsx`, and ~30 others (full list in T198) — were deleted once slices 2–5 finished migrating every screen that used them (Servers, ServerDetail, Modules, Cluster, Users, AdminSettings, AuditLog, AdminLogs, Backups). No screen ever mixed the HeroUI and legacy Radix families at the same time (enforced by import rule FR-012 during the migration).

### Deviation Notes

1. **AppearanceToggle three-state vs. binary Switch (OD-7, decision #5):** Task T052 text suggests "using HeroUI Switch", but the contract specifies a three-state control (light/dark/system). Implementation uses HeroUI `ToggleButtonGroup` if available; if not, falls back to three `Button` elements with `isSelected` state. Binary `Switch` would discard the system-preference state — flagged as a deviation in the PR if justification is needed.

2. **Dashboard.tsx narrowed scope (decision #6):** Task T041's literal wording ("displaying app-loading state and empty dashboard frame") narrows this slice's work to loading + empty states only. Full stat/table dashboard content is deferred; a `// TODO(slice-2+)` comment marks where it would be added. Flagged in PR description since this is a visible reduction from today's 470-line Dashboard.tsx.

## T066–T088 — Slice 2a: Servers List and ServerDetail Screens

**Scope:** Slice 2a (tasks T066–T088, `specs/done_014-heroui-web-rebuild/tasks.md`) rebuilds two core screens — the Servers list page and the ServerDetail (full server view with tabbed interface) — on HeroUI components. This is the primary user-facing surface after login, showing all game server instances and their live status. The slice composes existing ui/ atoms from slice 0 with new HeroUI form/table/dialog components.

### Screens and Routes

1. **Servers Page** (`web/src/routes/Servers.tsx`, 651 lines)
   - List all GameServers in a responsive table (desktop ≥768px) or stacked cards (mobile <768px)
   - Renders server phase, game icon, player count, resource usage, and uptime via StatCard compositions
   - Filter by phase (Running/Stopped/Pending/Failed), game template, and namespace via FilterPopover
   - Inline lifecycle buttons (play/stop/restart) using HeroUI Button variants
   - Server count summary via StatCard at the top; search box using HeroUI Input
   - Action menu (clone, transfer, wipe, delete) via ServerActionsMenu composition (uses DropdownMenu)

2. **ServerDetail Page** (`web/src/routes/ServerDetail.tsx`, 289 lines)
   - Full server view with header showing phase chip, uptime, player count, and action buttons
   - Tabbed interface (HeroUI Tabs) routing to nine sub-views: Overview, Events, Console, Logs, Files, Mods, Modpacks, Players, Backups, Capture, Settings
   - Phase chip from ui/PhaseChip; game icon from ui/GameIcon
   - Lifecycle action buttons (start/stop/restart) gated by phase state

### Design-Imported Compositions

**Slice 2a uses the following ui/ atom compositions from slice 0** (line numbers below are the `import` line in each file, verified by grep against the current tree):

- **StatCard** — metric display (players online, server uptime, resource usage gauges) in Servers list summary and Overview tab (`web/src/routes/tabs/Overview.tsx` line 7, `web/src/routes/Servers.tsx` line 23)
- **PhaseChip** — server phase badge (Running/Stopped/Pending/Failed) in Servers list rows and ServerDetail header (`web/src/routes/Servers.tsx` line 24, `web/src/routes/ServerDetail.tsx` line 16)
- **FilterPopover** — filter controls in Servers list (phase, template, namespace) (`web/src/routes/Servers.tsx` line 25)
- **GameIcon** — cached game-specific icon in Servers list rows and ServerDetail header (`web/src/routes/Servers.tsx` line 26, `web/src/routes/ServerDetail.tsx` line 17)
- **DropdownMenu** (`DropdownMenu`, `DropdownMenuContent`, `DropdownMenuItem`, `DropdownMenuSeparator`, `DropdownMenuTrigger`) — context menu (clone/transfer/wipe/delete) via ServerActionsMenu (`web/src/components/server/ServerActionsMenu.tsx` lines 12–17)
- **ConfirmDialog** — used in **DeleteServerDialog.tsx** (`web/src/components/server/DeleteServerDialog.tsx` line 2) and the file-delete confirmation in Files (`web/src/routes/tabs/Files.tsx` line 35). CloneServerDialog, TransferServerDialog and WipeServerDialog do **not** use ui/ConfirmDialog — they build their own dialogs directly from HeroUI `Modal`/`AlertDialog` primitives (see Server Component Helpers below).
- **ErrorBanner** — form/request error display in Files and Players tabs (`web/src/routes/tabs/Files.tsx` line 36, `web/src/routes/tabs/Players.tsx` line 16)
- **ErrorCard** — error state display in Console tab ("No console available") (`web/src/routes/tabs/Console.tsx` line 5)
- **LoadingCard** — skeleton placeholder in Console tab while data loads (`web/src/routes/tabs/Console.tsx` line 4)
- **Sparkline** — mini inline CPU/memory/disk charts in Overview tab (`web/src/routes/tabs/Overview.tsx` line 8)

**HeroUI Components imported directly** (component-name lists below are taken verbatim from each file's `@heroui/react` import, not restated from memory):

- **Button** — from `@heroui/react`, imported in Servers.tsx (line 22), ServerDetail.tsx (line 4), Events.tsx (line 3), Logs.tsx (line 4), Players.tsx (line 14), ServerActionsMenu.tsx (line 9), and the dialog/card files below
- **Card, CardHeader, CardTitle, CardContent, CardFooter, Alert** — from `@heroui/react`, imported together in Overview tab (`web/src/routes/tabs/Overview.tsx` line 6)
- **Input** — from `@heroui/react` (search box in Servers list, player filter in Players tab, file/folder name fields in Files) (`web/src/routes/Servers.tsx` line 22, `web/src/routes/tabs/Players.tsx` line 14, `web/src/routes/tabs/Logs.tsx` line 4, `web/src/routes/tabs/Files.tsx` line 17)
- **Chip, Tabs, Tab, Table** — from `@heroui/react` (`web/src/routes/Servers.tsx` line 22: `Button, Card, Input, Chip, Tabs, Tab, Table`; `web/src/routes/ServerDetail.tsx` line 4: `Button, Tabs, Tab`)
- **Modal, ModalBackdrop, ModalContainer, ModalDialog, ModalHeader, ModalBody, ModalFooter** — from `@heroui/react` (file create/folder dialogs in Files tab, `web/src/routes/tabs/Files.tsx` lines 9–19; also used, with `ModalHeading`/`Label`/`Description`/`FieldError` added, in `CloneServerDialog.tsx` lines 4–17, `TransferServerDialog.tsx` lines 3–16 with `ListBox`/`ListBoxItem`/`Popover`/`PopoverTrigger`/`PopoverContent` instead of form fields, and `ServerActionsCard.tsx` lines 20–39)
- **AlertDialog, AlertDialogBackdrop, AlertDialogContainer, AlertDialogDialog, AlertDialogHeader, AlertDialogHeading, AlertDialogBody, AlertDialogFooter, AlertDialogIcon, Checkbox** — from `@heroui/react` (`WipeServerDialog.tsx` lines 3–14)
- **Alert** — from `@heroui/react`, also imported in `ServerSleepCard.tsx` (line 2) for the idle-state summary

### Tab Components (Slice 2a)

Six of the nine ServerDetail tabs are rebuilt in this slice:

1. **Overview** (`web/src/routes/tabs/Overview.tsx`, 411 lines)
   - Composes `ServerStatusCard`, `ServerSleepCard`, `ServerActionsCard`, `EventList` (imports at lines 9–12) for status, sleep state, lifecycle actions, and recent pod events
   - Live metrics: CPU %, memory %, disk % via StatCard + Sparkline (`Sparkline` used at line 332)
   - Uses HeroUI Card/CardHeader/CardTitle/CardContent/CardFooter/Alert (line 6); ui/ StatCard, Sparkline (lines 7–8)

2. **Events** (`web/src/routes/tabs/Events.tsx`, 86 lines)
   - Kubernetes events (image pull, scheduling, crash-loops, agent startup) rendered via `EventList` (`web/src/components/server/EventList.tsx`, imported line 6)
   - Filter state (all/info/warnings) via HeroUI Button
   - Uses HeroUI Button, Card (line 3)

3. **Console** (`web/src/routes/tabs/Console.tsx`, 157 lines)
   - Interactive RCON/PTY terminal (xterm.js, lazy-loaded, NOT rebuilt — uses existing engine)
   - LoadingCard during template resolution (line 28); ErrorCard if no console available (line 35)
   - Uses ui/ LoadingCard, ErrorCard only (lines 4–5); console I/O engine unchanged
   - Command bar under the terminal (`ConsoleShell.tsx`): Send or Enter submits; ArrowUp/ArrowDown recall the last 100 commands sent from this tab (consecutive repeats collapsed), and ArrowDown past the newest restores the unsent line. History lives per mount and is not persisted

4. **Logs** (`web/src/routes/tabs/Logs.tsx`, 299 lines)
   - Pod stdout or configured game log file stream (WebSocket, NOT rebuilt — uses existing engine)
   - Requests last 500 lines of history via `?tail=500` query parameter before streaming new output
   - Search/filter input via HeroUI Input; download button
   - Virtualized log viewer (TanStack Virtual, NOT rebuilt)
   - Uses HeroUI Button, Input only (line 4); log streaming engine unchanged

5. **Files** (`web/src/routes/tabs/Files.tsx`, 602 lines)
   - File browser and editor (Monaco, lazy-loaded, NOT rebuilt)
   - Create folder/file dialogs via HeroUI Modal/ModalBackdrop/ModalContainer/ModalDialog/ModalHeader/ModalBody/ModalFooter (lines 9–19)
   - Delete confirmation via ui/ ConfirmDialog (line 35, used at line 408)
   - Error display via ui/ ErrorBanner (line 36, used at line 261)
   - Folder rows carry a trash button (on row hover or focus on desktop, always visible on mobile) that opens the same delete confirmation without opening the folder
   - The new file/folder name prompt explains a rejected name inline ("Names can't contain "/" or be "." or "..".") and marks the input `aria-invalid`; Create stays disabled
   - Monaco editor unchanged
   - File drafts bind to the selected path, resource target and selection generation. Selection clears content immediately and Save stays disabled until that file has loaded. Late reads and save completions cannot replace another selection's content or baseline, including reopening the same path. Saves capture their target, path, body and directory at click time; edits typed during a save remain dirty against the saved body. Changing server/namespace/cluster/UID starts a fresh file browser.

6. **Players** (`web/src/routes/tabs/Players.tsx`, 386 lines)
   - Online player snapshot, ban list, whitelist management
   - Player count summary via ui/ StatCard (line 15)
   - Kick/ban/unban actions with reason input via HeroUI Input (line 14)
   - Error display via ui/ ErrorBanner (line 16, used at line 131)

**Three tabs remain un-rebuilt (used only from legacy primitives or not yet touched):**

- **Mods** — unchanged from slice 0 (not in scope)
- **Modpacks** — unchanged from slice 0 (not in scope)
- **Backups** — unchanged from slice 0 (not in scope)
- **Settings** — unchanged from slice 0 (not in scope)

### Console and Logs Engines

Console input/output and Logs streaming use existing bidirectional WebSocket (console) and read-only WebSocket/SSE (logs) plumbing via `web/src/lib/ws.ts` and `web/src/lib/sse.ts`. These engines are **not rebuilt** in any slice; only the loading/error UI wrapper changes (LoadingCard, ErrorCard in slice 2a). The consumer-facing API (`openWS()`, `openEventStream()`) remains unchanged.

### Server Component Helpers

**Slice 2a adds/updates helper components in `web/src/components/server/`** (line counts are the file's current total, since internal structure shifts with every edit and precision there is not load-bearing):

- **ServerActionsMenu.tsx** (116 lines) — Dropdown menu context menu (clone/transfer/wipe/delete) using ui/ DropdownMenu (import lines 12–17, usage lines 48–86); wires the four dialogs below
- **CloneServerDialog.tsx** (138 lines) — Clone form (name, description, template selector) built directly from HeroUI `Modal`/`ModalBackdrop`/`ModalContainer`/`ModalDialog`/`ModalHeader`/`ModalHeading`/`ModalBody`/`ModalFooter`/`Button`/`Input`/`Label`/`Description`/`FieldError` (import lines 4–17) — not ui/ConfirmDialog
- **TransferServerDialog.tsx** (143 lines) — Transfer form (destination picker) from the HeroUI `Modal` family plus `ListBox`/`ListBoxItem`/`Popover`/`PopoverTrigger`/`PopoverContent` (import lines 3–16) — not ui/ConfirmDialog
- **WipeServerDialog.tsx** (123 lines) — Wipe-world confirmation from the HeroUI `AlertDialog` family plus `Checkbox` (import lines 3–14) — not ui/ConfirmDialog
- **DeleteServerDialog.tsx** (52 lines) — Delete server confirmation via ui/ ConfirmDialog (line 2)
- **ServerStatusCard.tsx** (73 lines) — Status summary card in Overview tab, built on HeroUI Card (line 3); displays "—" when the agent reports no game version instead of falling back to template name
- **ServerActionsCard.tsx** (487 lines) — Lifecycle action card in Overview tab; HeroUI Button/Card/CardHeader/CardContent/Modal family/Input/Label/Select/ListBox/ListBoxItem/Checkbox/Description/FieldError (import lines 20–39). A param with `currentFrom` is pre-filled from that status metric's live reading (shared `server-status` query; enum options match case-insensitively), falling back to `default`; the user's edits always take precedence. A button is enabled by its action's resolved transport (mirroring the API): stdin actions (explicit `transport: stdin`, or no transport on a game without RCON) only need an attachable console (`consoleMode` not `none`), rcon actions need RCON; when some action is blocked a hint says why
- **ServerSleepCard.tsx** (170 lines) — Server sleep/idle state summary; HeroUI Card/Alert (line 2) plus a `Chip` re-exported from ui/PhaseChip (line 6)
- **EventList.tsx** (43 lines) — Kubernetes event list renderer, used from both Events.tsx (line 6) and Overview.tsx (line 12)
- **PortOverridesEditor.tsx** (80 lines) — Port configuration helper; HeroUI Input/Button (line 1) (Settings tab, deferred)
- **ConfigFields.tsx** — `ConfigFieldInput`/`ConfigFields`: schema-driven config field renderer shared by the Create wizard (variant `wizard`, stacked) and Settings > Game configuration (variant `settings`: label-left rows, required marker, write-only passwords, file note, inline errors); native selects for enum and bool, text input with numeric keyboard for int

### Design Import Rule (FR-012)

Every file in slice 2a follows the import rule:

- ✅ Imports **only** from `@heroui/react` and `@/components/ui/` (no `@radix-ui/*`, no `class-variance-authority`)
- ✅ No mixed families within a single file
- ✅ Verified by grep: `grep -rE '@radix-ui|class-variance-authority' web/src/routes/{Servers,ServerDetail}.tsx web/src/routes/tabs/{Overview,Events,Console,Logs,Files,Players}.tsx web/src/components/server/ 2>/dev/null` returns **zero results**

**Mechanics:** Lint and review enforce the rule; any rebuilt file importing from forbidden sources fails CI.

### Test Count Rule (FR-010)

Each rewritten test file maintains or exceeds the original test count:

- `Servers.test.tsx`: ported filter state, search, lifecycle action, and phase-counting cases
- `ServerDetail.test.tsx`: ported tab switching and lifecycle action cases
- `Overview.test.tsx`: ported metric display and event summary cases
- `Events.test.tsx`: ported event filter and list cases
- `Console.test.tsx`: ported loading/error state cases
- `Logs.test.tsx`: ported log filtering and line rendering cases
- `Players.test.tsx`: ported player action and list cases
- `Files.test.tsx`: ported file browser and editor dialog cases

Selectors updated to query by role (`getByRole("button", { name: /clone/i })`, `getByRole("table")`, `getByRole("tablist")`) since HeroUI markup changes internal DOM structure. No `data-testid` added unless HeroUI components genuinely lack accessible role/name.

### Deviation Notes

CloneServerDialog, TransferServerDialog and WipeServerDialog do not route through ui/ConfirmDialog the way DeleteServerDialog and the Files delete confirmation do — each builds its own dialog directly from HeroUI `Modal`/`AlertDialog` primitives, since their forms need bespoke fields (name/description/template picker, destination picker, wipe checkbox) that ui/ConfirmDialog's fixed layout does not support. This is a real, verified divergence from the atom-reuse framing above, not a workaround pending cleanup — no forbidden-import (`@radix-ui`, `class-variance-authority`) is involved, per the Design Import Rule grep above.

## T097–T120 — Slice 2b: Mods, Modpacks, Backups Tab, and Settings

**Scope:** Slice 2b (tasks T097–T120, `specs/done_014-heroui-web-rebuild/tasks.md`) completes ServerDetail by rebuilding four tabbed interfaces — Mods (installed mod browsing and installation), Modpacks (modpack selection), Backups (per-server backup listing and restore), and Settings (11 sub-sections for server configuration). All nine tabs in ServerDetail are now HeroUI-based; legacy primitives are fully purged from the authenticated shell and server management surfaces.

### Screens and Routes

1. **Mods Tab** (`web/src/routes/tabs/Mods.tsx`, 1206 lines)
   - Browse installed mods (idList or file-based, per template capability)
   - Install from registry or upload custom mods via dialogs
   - Per-mod actions: upgrade, reinstall, uninstall using HeroUI Button + Dropdown
   - Registry browser (CurseForge/Modrinth) picker via ui/ composition
   - Rendering strategy differs by game: ARK/Project Zomboid use idList editor; others show file list
   - Uses HeroUI Button, Input, Chip; ui/ ConfirmDialog (for destructive actions)

2. **Modpacks Tab** (`web/src/routes/tabs/Modpacks.tsx`, 230 lines)
   - Modpack-capable games render a selector/browser
   - Select active pack, view pack contents, toggle mods within a pack
   - Uses HeroUI Select, Table, Button, Chip

3. **Backups Tab** (`web/src/routes/tabs/Backups.tsx`, 209 lines)
   - Per-server backup list (snapshots created via the /backups page or on-demand)
   - Table columns: backup name, phase (Pending/Succeeded/Failed), size, completion time, actions
   - Open backup detail drawer, trigger restore dialog, delete backup
   - Uses HeroUI Table, Button, Modal (restore trigger), ui/ PhaseChip

4. **Settings Tab** (`web/src/routes/tabs/Settings.tsx`, 289 lines)
   - Sub-section navigation tabs (11 sections below) via HeroUI Tabs
   - Layout: left sidebar with section buttons, right panel for section content
   - Each section maintains independent form state, save/discard flow, per-field validation

### Settings Sub-Sections (T101–T111)

All 11 sections render form controls from HeroUI (TextField, Select, Slider, Switch, Checkbox) with independent state management:

1. **General** (`web/src/routes/tabs/settings/General.tsx`) — Server name, description, game-icon display (read-only)
2. **Version** (`web/src/routes/tabs/settings/Version.tsx`) — Active version selector, available versions, rollback button
3. **Resources** (`web/src/routes/tabs/settings/Resources.tsx`) — CPU request/limit, memory request/limit, storage size sliders + TextField inputs
4. **Networking** (`web/src/routes/tabs/settings/Networking.tsx`) — Port table (external/internal/protocol), address pool picker, firewall rules via HeroUI Table + Modal dialogs
5. **Environment** (`web/src/routes/tabs/settings/EnvVars.tsx`) — Environment variable key/value editor (table, add/delete rows) via HeroUI Table + TextField
6. **Lifecycle** (`web/src/routes/tabs/settings/Lifecycle.tsx`) — Auto-pause threshold, idle sleep settings, quiesce strategy selector via HeroUI Switch/Select/Slider
7. **Scheduled backups** (`web/src/routes/tabs/settings/Backups.tsx`) — Retention days selector, retention policy dropdown via HeroUI Select/TextField
8. **Network capture** (`web/src/routes/tabs/settings/NetworkCapture.tsx`) — Capture enabled toggle, BPF filter input, retention policy via HeroUI Switch/TextField/Select; warning banner via ui/ component
9. **Placement** (`web/src/routes/tabs/settings/Placement.tsx`) — Node affinity rules, pod-node-selector builder via HeroUI form components
10. **RBAC & access** (`web/src/routes/tabs/settings/Access.tsx`) — Per-role read/exec/admin permissions toggle matrix via HeroUI Switch grid
11. **Danger zone** (`web/src/routes/tabs/settings/Danger.tsx`) — Destructive action buttons (Delete server, Wipe world, Transfer ownership to another user) wired to confirmation dialogs via HeroUI Button (danger variant) + ui/ ConfirmDialog

### New Components (T112–T115)

- **CaptureWidget.tsx** (T112) — Status display for active network packet capture; download + stop buttons via HeroUI Button; capture warning banner via ui/ component
- **registry-browser.tsx** (T113) — Shared mod registry browser (Mods + Modpacks tabs); search, category filter, mod list via HeroUI Table/SearchField/Chip; provider logo display
- **modules/InstallDialog.tsx** (T114) — Modal dialog for installing a module (name field pre-filled, version selector) via HeroUI Modal/TextField/Select/Button
- **modules/UploadModuleDialog.tsx** (T115) — Modal dialog for uploading custom module (file input, version field) via HeroUI Modal/Input/TextField/Button

### HeroUI Components Imported

All rebuilt Slice 2b files import **only** from `@heroui/react` and `@/components/ui/`:

- **Table** — Mods/Modpacks/Backups/Settings tabs (Networking, EnvVars, etc.) use HeroUI Table + Table.Header/Table.Column/Table.Body/Table.Row/Table.Cell (no `align`, `classNames`, `emptyContent`, `isLoading` props per HeroUI v3)
- **Modal, ModalBackdrop, ModalContainer, ModalDialog, ModalHeader, ModalBody, ModalFooter** — Module dialogs (install/upload), restore/delete confirmations
- **TextField, Input, Select, Checkbox, Switch, Slider, NumberField** — Settings form controls (no `onValueChange`, no `defaultValue` on Select; use `value`/`onChange`)
- **Button** — Lifecycle actions (install, upgrade, uninstall, delete, wipe, transfer), navigation (next/previous in Backups detail)
- **Tabs, Tab, TabList** — Settings sub-section navigation
- **Dropdown, DropdownTrigger, DropdownMenu, DropdownItem, DropdownSection, DropdownDivider** — Per-mod actions (upgrade, reinstall, uninstall)
- **Popover, PopoverTrigger, PopoverContent** — Restore destination picker (if needed), filter popovers
- **Drawer** — Backup detail drawer (if HeroUI Drawer is preferred; fallback to Modal anchored left)
- **SearchField** — Registry browser search (Mods/Modpacks)
- **Chip** — Phase badges (Backups), category tags (Modpacks)

### Registry Browser and Capture Widget

**registry-browser.tsx** (T113) is a shared composition used by both Mods and Modpacks tabs:
- Fetches mod list from configured registries (CurseForge, Modrinth, GitHub, custom)
- Renders search field + category filter (both HeroUI) + paginated result table (HeroUI Table)
- Displays mod name, version, downloads, authors, description; mod icon and provider logo
- Handles selection and passes back to parent tab (via callback or state mutation)

**CaptureWidget.tsx** (T112) is a per-server status indicator:
- Displays "Capture running" state with live frame count
- Shows download button (downloads captured PCAPNG) and stop button (halts capture)
- Displays warning banner (ui/ component) if capture is in progress
- Used in ServerDetail header or a separate Capture tab (depends on template capability)

### State Preservation Rule

Each Settings sub-section independently owns its form state (no shared parent provider):
- Read/fetch on tab navigation (`useEffect` keyed to tab key)
- State syncs to form fields (TextField.value = formState, etc.)
- User edits mutate local state (e.g., `setFormState({...formState, name: e.target.value})`)
- Save button triggers PATCH /servers/{name} with all changed fields
- Discard button resets form to the last-fetched state
- Navigation away without save prompts user (e.g., "You have unsaved changes")

### Design Import Rule (FR-012)

Every file in slice 2b imports **only** from `@heroui/react` and `@/components/ui/` (no `@radix-ui/*`, no `class-variance-authority`):

- ✅ Verified by `grep -rl -e '@radix-ui' -e 'class-variance-authority' web/src/routes/tabs/{Mods,Modpacks,Backups,Settings}.tsx web/src/routes/tabs/settings/ web/src/components/{CaptureWidget,registry-browser}.tsx web/src/components/modules/{InstallDialog,UploadModuleDialog}.tsx 2>/dev/null` must return **zero results** (task T120)
## T119–T137 — Slice 3: Create Server Wizard, Modules Catalog, and Backups Management

**Scope:** Slice 3 (tasks T119–T137, `specs/done_014-heroui-web-rebuild/tasks.md`) rebuilds three onboarding and operational management surfaces on HeroUI components: the multi-step Create Server wizard (Steps 1–5), the Modules library catalog, and the Backups management interface (index, schedules, restores). This slice completes the foundational surfaces for server creation and backup operations, applying the same HeroUI component family and theme tokens established in slices 0–2a.

### Screens and Routes

1. **Create Server Wizard** (`web/src/routes/CreateServer.tsx`, 52,147 bytes)
   - Multi-step form guiding server creation through five steps; each step has validation, back/next navigation, and error display
   - Step 1: Server name and template selection (game type pick)
   - Step 2: Version selection (pick released/beta/unstable version per template)
   - Step 3: Game configuration (slots, difficulty, world size, etc. — rendered from template `spec.configSchema`)
   - Step 4: Networking (service type, ports, tunnel config if enabled)
   - Step 5: Review and confirmation (summary of all selections; triggers server creation on submit)
   - Form state and validation managed per-step via React state; back button returns to previous step without data loss
   - Tunnel credentials saved to a Kubernetes Secret after server creation if tunnel is enabled (error chain preserved if secret-save fails)

2. **Modules Catalog Page** (`web/src/routes/Modules.tsx`, 11,410 bytes)
   - Browse available game modules (templates) from all registered ModuleSources
   - Module cards display game icon, name, description, version, install status
   - Actions: install from catalog, manage installed versions, bulk upload, add/edit ModuleSource
   - Filter by game type or source; search by name
   - InstallDialog, UploadModuleDialog, SourceDialog (add/edit module source) for workflow UI

3. **Backups Management Page** (`web/src/routes/Backups.tsx`, 18,395 bytes)
   - Tabbed interface with three sub-views: Backups (index), Schedules, Restores
   - **Backups tab:** List all Backup CRs (manual + scheduled); filter by server/status; view backup metadata (size, created/expires); select row to open detail drawer, or restore — no inline delete action on the row
   - **Schedules tab:** List BackupSchedule CRs; create (per-server, no edit mode) and suspend/delete schedules; plain cron-expression input (no daily/weekly template pickers); retention policy form
   - **Restores tab:** List Restore operations in progress and completed; view status, source backup, destination server; no row actions (no view-details drawer, no cancel-restore)
   - BackupDetailDrawer (slide-out panel) for full backup metadata, file size, retention expiry
   - RestoreDialog (modal) for restore workflow (pick source backup, destination server, confirm)
   - ScheduleForm (inline, non-modal form) for creating a schedule with cron input and retention-window controls — no edit mode

### Design-Imported Compositions

**Slice 3 uses the following ui/ atom compositions from slice 0:**

- **PhaseChip** — backup/restore phase badge (Completed/Failed/Pending) in Backups and Restores tables (`web/src/components/backups/BackupRow.tsx`, `web/src/routes/Backups.tsx`)
- **BackupFilters** — search box + server/phase filters with a trailing "N of M" count, used by the Backups and Restores tabs (`web/src/components/backups/BackupFilters.tsx`, `web/src/routes/Backups.tsx`); `web/src/routes/Backups.tsx` does not use `StatCard` or `FilterPopover` (both exist elsewhere in `web/src` — `StatCard` in Dashboard.tsx/Servers.tsx/Players.tsx/tabs/Backups.tsx, `FilterPopover` in Servers.tsx — but neither is imported by the Backups management route)
- **ErrorBanner** — error display in backup/schedule operations (`web/src/components/backups/ErrorBanner.tsx` line 1)
- **ErrorCard** — error state display when backup operations fail (`web/src/components/backups/` components, used at error boundaries)

**HeroUI Components imported directly** (component-name lists from `@heroui/react` import statements in each file):

- **Button** — from `@heroui/react`, imported in CreateServer.tsx (line 22), Modules.tsx (line 8), Backups.tsx (line 15), ModuleCard.tsx, InstallDialog.tsx, SourceDialog.tsx, UploadModuleDialog.tsx, RestoreDialog.tsx, ScheduleForm.tsx, BackupDetailDrawer.tsx
- **Input, Label, Description** — from `@heroui/react`, text/number fields in Create Server steps 1–5 and schedule/backup forms; label + description pattern for field validation and helper text
- **Select, ListBox, ListBoxItem** — from `@heroui/react`, version picker (Step 2), game selector (Step 1), namespace/server picker in Backups restore workflow, template selector in Modules install
- **Modal, ModalBackdrop, ModalContainer, ModalDialog, ModalHeader, ModalBody, ModalFooter, Tabs, Tab** — from `@heroui/react`, step progression UI in Create Server (using Tabs for step navigation; alternative: linear stepper), module install/upload dialogs (InstallDialog.tsx, UploadModuleDialog.tsx, SourceDialog.tsx), restore dialog (RestoreDialog.tsx), schedule form (ScheduleForm.tsx)
- **Card, CardHeader, CardTitle, CardContent, CardFooter** — from `@heroui/react`, module cards (ModuleCard.tsx), backup detail drawer (BackupDetailDrawer.tsx), server summary cards in Create Server Step 5
- **Checkbox, Radio** — from `@heroui/react`, configuration options in Step 3 (difficulty, world mode as checkboxes), retention policy toggles in ScheduleForm
- **Chip** — from `@heroui/react`, status badges (backup phase, restore status), module version/provider tags in Modules catalog
- **Table** — from `@heroui/react`, backup index table (Backups.tsx), schedules list (ScheduleForm.tsx scope), restores table in Backups tab 3
- **Slider, NumberField, Switch** — from `@heroui/react`, resource/slot range inputs in Step 3 (CPU, memory, player slots), retention window/duration inputs in ScheduleForm, tunnel toggle in Step 4
- **Popover, PopoverTrigger, PopoverContent** — from `@heroui/react`, underlying the `FilterPopover` composition, which is used only in `web/src/routes/Servers.tsx` (not in the top-level `Backups.tsx` route, which uses `BackupFilters` instead), module source selector in Modules
- **Alert** — from `@heroui/react`, success/info/warning displays in backup workflows, tunnel credential save status in Create Server Step 4
- **Drawer** (or modal equivalent for side panel) — BackupDetailDrawer.tsx implements slide-out detail view via Modal with custom positioning (right-side panel style)

### Create Server Form Fields & Validation (Steps 1–5)

**Step 1 — Name & Template Selection:**
- Server name input (required, alphanumeric + hyphens, max length from API constraint)
- Game template selector (required, ListBox with game icons, descriptions)
- Validation: name must match the Kubernetes name format (lowercase letters, digits, dashes, max 63 chars); no client-side uniqueness query against the API (a name collision surfaces as a 409 from the create call)

**Step 2 — Version Selection:**
- Version picker (ListBox or Radio group) showing released/beta/unstable versions per template
- No changelog or version-notes display
- Validation: selected version exists for the chosen template (`isValidVersion`)

**Step 3 — Configuration:**
- Dynamic form fields rendered from template's `spec.configSchema` (slots, difficulty, world size, etc.)
- Input types: Text, Number, Select (enum), Checkbox (boolean), Slider (range)
- Each field carries its schema-declared label and description (helper text)
- Client-side validation against declared constraints (min/max, enum values, string patterns)
- Validation: all required fields present; values within declared ranges

**Step 4 — Networking:**
- Service type picker (ClusterIP/NodePort/LoadBalancer) via Select/ListBox
- Port configuration: inline port overrides (game-specific ports like 25565 for Minecraft)
- Address pool / explicit address request (if LoadBalancer mode, optional pool name + requested IP)
- Tunnel configuration (if operator has `tunnel.enabled`): provider select (frp/Tailscale/playit), credentials form
- IP allow-list textarea (CIDRs, optional, newline- or comma-separated); restricts which clients reach the LoadBalancer, but the client does not validate the CIDR syntax — malformed entries are only caught server-side
- Validation (`validateStep`): tunnel credentials required if tunnel enabled; frp requires a server address, at least one complete port mapping, and port numbers in 1–65535 (server port too, when set); no client-side check on the LoadBalancer address/CIDR fields themselves

**Step 5 — Review & Confirmation:**
- Display-only summary of all prior steps' selections (name, template, version, config values, networking, tunnel). Values of password-type template config fields are shown as `********` (`maskPasswordConfig`, `web/src/lib/validation.ts`), never as typed text; the create payload is unchanged. The API never returns stored GameServer passwords (it sends the marker `__gameplane_redacted__`; see `api/specs.md`), so no screen may render a password-type `spec.config` value or that marker. The Settings tab's merged PUT (`mergeDraftOntoLatest`) preserves unchanged markers from the latest server: the API keeps the stored value for them.
- "Create server" button triggers POST to `/servers` endpoint
- Error display if creation fails (network error, validation error, server name conflict)
- On success, redirect to ServerDetail page for new server

### Modules Catalog Surface

**Modules.tsx (main page):**
- Single catalog view (no Catalog/Installed/Upload tab switcher): search box, source filter, and category chips over the combined catalog (`matchesAllCategories`); ModuleCard instances for each entry, each carrying its own install/upgrade/uninstall actions
- "Create module" button opens the Module Builder; "Upload module" button (shown when an upload-capable source exists) opens `UploadModuleDialog`
- `ModuleSourcesPanel` (source CRUD) is reached from Admin, not from a tab on this page

**ModuleCard.tsx (reusable card):**
- Game icon (ui/GameIcon); module name, description
- Version badge (current, latest available)
- Install button (if not installed) → opens InstallDialog
- Manage button (if installed) → opens version picker or uninstall confirmation
- Verify badge (`lib/verify.ts` `verifyForEntry`/`VerifyBadge`): a solid "verified" chip only when an installed entry's `CatalogEntry.verifiedDigest` is set — i.e. the operator actually ran cosign verification for that install (`Module.status.verifiedDigest`/`status.verifyPolicy`). A source's current `spec.verify` policy alone never sets this; it only supplies the outline "policy" chip's mode, so a policy added after install shows as declared-but-unchecked, not "verified". Mixed or absent policy across candidate sources suppresses the badge entirely.

**InstallDialog.tsx:**
- Modal with source/namespace pickers (destination cluster and namespace for module installation)
- Version selector (pick which version to install)
- Install button → POST to `/modules` endpoint

**UploadModuleDialog.tsx:**
- File drag-drop or file picker for OCI bundle upload
- Module source selector (which ModuleSource to upload to)
- Upload button → multipart POST to `/modules/:source/upload`

**SourceDialog.tsx (ModuleSource CRUD):**
- Modal form for create/edit ModuleSource (git URL, HTTP URL, local upload)
- Source name, type picker, credentials/auth fields as needed
- Add/Update button → POST/PUT to `/module-sources`

### Backups Management Surface

**Backups.tsx (main page with three tabs):**
- Tab structure (HeroUI Tabs) for Backups / Schedules / Restores
- No shared summary header — each tab renders its own `BackupFilters` row with a trailing "N of M" count instead of a `StatCard` summary

**Tab 1 — Backups Index:**
- Table of Backup CRs (name, server, phase, size, completed date)
- `BackupFilters`: search box, server picker, phase filter (no date-range filter)
- Row actions from `BackupRow`: select (opens `BackupDetailDrawer`), restore (opens `RestoreDialog`) — no inline delete action on the row
- Detail drawer (`BackupDetailDrawer`) opened on row click

**Tab 2 — Schedules:**
- Table of BackupSchedule CRs (name, server, cron expression, last/next run, active)
- Next run displays as a future relative time (e.g., "in 2 hours"); shows "—" when schedule is suspended
- Inline "New schedule for" server picker (not a button) opens `ScheduleForm` for that server
- Row actions: suspend/resume toggle (`Switch`), delete (confirm dialog) — no edit action

**Tab 3 — Restores:**
- Table of Restore CRs (name, backup, target, phase, completed, message)
- `BackupFilters`: search box, server picker, phase filter
- No row actions — no view-details drawer and no cancel-restore action from this tab
- Empty state when no restores

**BackupDetailDrawer.tsx:**
- Slide-out right panel (modal positioned right-side or Drawer component)
- Shows backup metadata: ID, server, size (human-readable), created/expires timestamps
- Retention expiry badge (green/yellow/red based on days remaining)
- Associated backup file listing (if applicable)
- Close button or swipe-to-close

**RestoreDialog.tsx:**
- Modal form for restore workflow
- Source backup display (read-only; passed as prop from table row)
- Destination server picker (Select/ListBox, namespace-aware)
- Confirm restore button → POST to `/restores` endpoint
- Cancel button → closes dialog

**ScheduleForm.tsx:**
- Inline (non-modal) form for creating a new BackupSchedule for a given server — no edit mode
- Backup destination picker (`Select`) and cron expression text input (no daily/weekly radio buttons)
- Retention policy: `RetentionFields` sub-component — numeric count inputs per retention bucket (`keepLast`, `keepHourly`, `keepDaily`, `keepWeekly`, `keepMonthly`, `keepYearly`), not a duration/unit-dropdown picker
- Create/Cancel buttons

### Backup-Related Components (`web/src/components/backups/`)

- **BackupRow.tsx** (32 lines) — Single table row rendering one Backup CR; ID, server, phase chip, size, timestamps
- **BackupDetailDrawer.tsx** (129 lines) — Slide-out detail panel for backup metadata
- **BackupFilters.tsx** (74 lines) — Filter controls (search box, server picker, phase filter — no date-range filter)
- **RestoreDialog.tsx** (228 lines) — Modal form for restore workflow (source backup, destination server, confirm)
- **ScheduleForm.tsx** (172 lines) — Inline (non-modal) form for creating a schedule (cron expression, retention) — no edit mode
- **RetentionFields.tsx** (106 lines) — Sub-component with a numeric count input per retention bucket (keep last/hourly/daily/weekly/monthly/yearly)
- **ErrorBanner.tsx** (14 lines) — Error display for backup/schedule operations

### Modules-Related Components (`web/src/components/modules/`)

- **ModuleCard.tsx** (358 lines) — Card displaying module metadata, install/manage actions
- **InstallDialog.tsx** (174 lines) — Modal for module installation (source, destination, version selection)
- **UploadModuleDialog.tsx** (181 lines) — Modal for bulk module file upload
- **SourceDialog.tsx** (531 lines) — Modal for ModuleSource creation/edit (git URL, HTTP, local)
- **ModuleSourcesPanel.tsx** (246 lines) — Panel for managing module sources (CRUD operations)

### Design Import Rule (FR-012)

Every file in slice 3 follows the import rule:

- ✅ Imports **only** from `@heroui/react` and `@/components/ui/` (no `@radix-ui/*`, no `class-variance-authority`)
- ✅ No mixed families within a single file
- ✅ Verified by grep: `grep -rE '@radix-ui|class-variance-authority' web/src/routes/{CreateServer,Modules,Backups}.tsx web/src/components/{modules,backups}/ 2>/dev/null` returns **zero results**

**Mechanics:** Lint and review enforce the rule; any rebuilt file importing from forbidden sources fails CI.

### T139 Compliance Findings (post-hoc, 2026-09-06) — resolved

T139's original check above greps for the forbidden **import paths** (`@/components/ui/`, `@radix-ui`) — it passes, and that finding stands. After T198/T201 (2026-09-16), the old `ui/` directory was deleted, `hero/` became `ui/`, and the forbidden list is now `@radix-ui` and `class-variance-authority`.

A tier+1 review at the time additionally found real HeroUI v2-API and syntax defects that the import-path check alone did not catch: curly-quote syntax corruption in `Backups.tsx`, v2-only props (`SelectItem`, `classNames`, `Table.Column align`, `Table.Body emptyContent`, `color="default"`, bare `disabled`/`onClick`) in `Backups.tsx`, and `Button ... asChild` in `Modules.tsx`. All of these have since been fixed: `web/src/routes/Backups.tsx` and `web/src/routes/Modules.tsx` use the v3 API throughout (`Modules.tsx` renders its admin link via `buttonVariants()` on a `Link`, not `asChild`), and a repo-wide grep for `SelectItem`, `emptyContent`, `classNames=`, and `asChild` under `web/src` returns nothing. Kept here as a historical record, not an open finding.

### Test Count Rule (FR-010)

Each rewritten test file maintains or exceeds the original test count:

- `Mods.test.tsx` — ported mod install/upgrade/uninstall/delete cases; registry search cases
- `Modpacks.test.tsx` — ported pack selection and toggle cases
- `Backups.test.tsx` — ported backup list, filter, restore, and delete cases (server-detail Backups tab only; the /backups page is slice 3)
- `Settings.test.tsx` — ported sub-section navigation and state preservation cases
- `settings/General.test.tsx`, `settings/Version.test.tsx`, etc. — ported form field, save/discard, and validation cases per section
- `CaptureWidget.test.tsx` — ported download/stop button interaction and status display cases
- `registry-browser.test.tsx` — ported search, filter, and result list cases
- `modules/InstallDialog.test.tsx`, `modules/UploadModuleDialog.test.tsx` — ported dialog open/close, form input, and submit cases

Selectors updated to query by role (`getByRole("table")`, `getByRole("button", { name: /install/i })`, `getByRole("tab")`) since HeroUI markup changes internal DOM structure.

### Playwright Specs (T116)

**serverDetail.spec.ts** updated to navigate all ServerDetail tabs including the newly-rebuilt Mods and Modpacks tabs (added to the labels array in the tab-switching test; both tabs are gated by template capability, so the spec gracefully skips them if not present).

**settingsSubTabs.spec.ts** updated with all 11 Settings sub-section names (General, Version, Resources, Networking, Environment, Lifecycle, Scheduled backups, Network capture, Placement, RBAC & access, Danger zone) to exercise the full configuration surface without console errors.

### Deviation Notes

1. **Slice 2b Backups tab vs. Slice 3 /backups page:** The Backups TAB (per-server backups within ServerDetail) is rebuilt in slice 2b. The global Backups INDEX page (`web/src/routes/Backups.tsx`, showing backups across all servers) is a slice-3 task; that page still uses native `<select>` elements and is not modified here.

2. **Registry browser data-testid:** The registry-browser table includes one `data-testid="mod-registry-results"` or similar to allow test selection when HeroUI Table's role-based queries alone do not narrow the scope tightly enough. No other data-testid is added unless justified by lack of accessible role/name.
- `CreateServer.test.tsx`, `CreateServer_more.test.tsx`, `CreateServer_tunnel.test.tsx`: ported all step progression, validation, and error cases
- `Modules.test.tsx`: ported catalog browse, install, upload cases
- `Backups.test.tsx`, `Backups_flows.test.tsx`: ported backup list, schedule CRUD, restore cases
- Component test files (`ModuleCard.test.tsx`, `InstallDialog.test.tsx`, `SourceDialog.test.tsx`, `UploadModuleDialog.test.tsx`, `BackupDetailDrawer.test.tsx`, `RestoreDialog.test.tsx`, `ScheduleForm.test.tsx`, `BackupFilters.test.tsx`, etc.): ported all interaction cases

Selectors updated to query by role (`getByRole("button", { name: /install/i })`, `getByRole("tablist")`, `getByRole("table")`) since HeroUI markup changes internal DOM structure. No `data-testid` added unless HeroUI components genuinely lack accessible role/name.

### Deviation Notes

BackupDetailDrawer's slide-out panel is implemented as a Modal with right-side positioning rather than a dedicated Drawer component, since HeroUI v3's Drawer API is not yet stable. The visual effect (right-aligned overlay panel) is achieved via CSS positioning and modal backdrop styling; functionality is identical.

ScheduleForm is a plain cron-expression text input (no daily/weekly template pickers or other cron-helper UI).

## Directory & Package Layout

```
src/
  main.tsx                  # React entry point; bootstraps QueryClient, Router, MSW
  types.ts                  # TypeScript types mirroring Gameplane CRDs (GameServer, GameTemplate, Backup, etc.)
  router/tree.tsx           # TanStack Router route tree (root, login, app-layout, all pages)
  routes/                   # Page components: Login, Dashboard, Servers, ServerDetail, CreateServer,
                            # Modules, Cluster, Users, AdminSettings, AuditLog, AdminLogs, Backups;
                            # ServerDetail sub-pages (tabs): Overview, Events, Console, Logs, Files,
                            # Mods, Modpacks, Players, Backups, Settings;
                            # Settings sub-sections: General, Version, Resources, Networking,
                            # Environment, Lifecycle, Backups (scheduled), Placement, Access (RBAC), Danger
  components/
    ui/                     # Gameplane compositions over HeroUI (@heroui/react): button, card, input,
                            # select, tabs, switch, slider, textarea, dialog, confirm-dialog, stat, etc.
    server/                 # Server-detail helpers: ServerActionsMenu, tab components
    backups/                # Backup-flow components: restore wizard, destination selector
    modules/                # Module catalog, install flow, upload preview
    AppLayout.tsx           # Main nav shell, user menu, breadcrumbs, fleet search
    PageHeader.tsx          # Standardized page title + action buttons
    ClusterSelector.tsx     # Infrastructure inventory selection
    RequireRole.tsx         # Permission gate middleware; wraps routes needing specific perms
    registry-browser.tsx    # Shared mod registry search/browse UI (Mods + Modpacks tabs)
  lib/
    api.ts                  # Thin fetch wrapper: api<T>(), APIError, csrfHeaders(), cluster threading
    endpoints.ts            # Typed URL-builder namespaces: Servers, Templates, Cluster(s),
                            # Backups, Schedules, Restores, BackupDestinations, Players,
                            # Users, Roles, Auth, AuthProviders, Audit, Notifications,
                            # ModRegistries, Files, Logs, Modules, ModuleSources
    ws.ts                   # openWS(): reconnecting WebSocket helper, exponential backoff
    sse.ts                  # openEventStream(): Server-Sent Events client for /events watch
    cluster.ts              # getCurrentCluster() / setCurrentCluster() for inventory selection
    auth.ts                 # Local user session, OIDC provider detection, logout
    capabilities.ts         # Server capability resolution: resolveConsoleMode(), serverHasMods(),
                            # serverHasModpacks() — drives tab visibility
    servers.ts              # Server validation, phase state helpers
    games.ts                # Game-specific logic: icon URLs, console protocol detection
    config.ts               # Global config: API base URL, feature flags, registry provider keys
    errors.ts               # Custom error types
    media.ts                # Image/icon URL helpers
    quantity.ts             # K8s quantity parsing (CPU, memory)
    events.ts               # Event severity / reason formatting
    annotations.ts          # K8s annotation key constants
    verify.ts               # Audit trail verification helpers (hash chain validation)
    destinations.ts         # Backup destination type detection
    validation.ts           # Form field validators (domain names, port ranges, resource specs)
    utils.ts                # capitalize(), formatUptime(), cn(), clsx helpers
  styles/
    globals.css             # Tailwind directives, CSS variables for theme, layout resets
  test/
    setup.ts                # Vitest setup: DOM matchers, MSW worker initialization
    browser-msw.ts          # MSW request handlers for mock e2e tests
    [*.test.tsx]            # Co-located unit + integration tests
e2e/
  [*.spec.ts]               # Playwright tests (mock mode and live mode)
  globalSetup.ts            # Live mode: spawn kubectl port-forward, bootstrap admin, save auth state
  globalTeardown.ts         # Live mode: kill port-forward
  .auth/storage.json        # Session + CSRF cookies persisted for live-mode tests

vite.config.ts              # Build config; dev proxy rules (Accept-header bypass for SPA routes)
tsconfig.json               # TS strict, noUnusedLocals, noUnusedParameters, noFallthroughCasesInSwitch
vitest.config.ts            # Coverage gates (lines 92 / functions 76 / branches 82 / statements 92)
playwright.config.ts        # Mock + live test modes, serial execution (login state is shared)
eslint.config.js            # Flat config: @typescript-eslint (strict), react-hooks, no-floating-promises
package.json                # @gameplane/web v0.3.0; dev: vite, npm scripts for build/test/lint
```

## Routing & Pages

**Router:** TanStack Router v1.75 in `src/router/tree.tsx` defines a two-level structure:
- Root route → `<Outlet>`
- Login route (`/login`) → public, unauthenticated
- App layout (`/app-layout`) → contains all authenticated pages
- Catch-all (`$` splat child of the app layout) → `NotFoundPage` ("Page not found", button "Go to dashboard") rendered inside the app shell; the layout's auth guard (401 → `/login`) applies, so unauthenticated visitors never see the page, and `/login` and `/share/$token` still match first. The breadcrumb reads "Page not found".

**Top-level Pages:**

1. **Login** (`/login`) → `LoginPage`
   - Public, pre-auth, unauthenticated
   - Local username/password form + OIDC provider buttons
   - No internal metrics/hostnames (privacy rule)

2. **Dashboard** (`/`) → `DashboardPage`
   - Landing page for authenticated users
   - Server count summary, recent events, admin shortcuts

3. **Servers** (`/servers`) → `ServersPage`
   - List all GameServers in a table
   - Filter by namespace, phase (Running/Stopped/Pending/Failed), template
   - Create + clone + delete actions
   - Namespace fan-out (F-263): fetches `Namespaces.list()` and issues one
     `Servers.list(ns)` per namespace it returns, merging the results.
     `namespaces: []` (servers:read in no namespace) fans out over nothing
     rather than falling back to a default namespace, so a user who only
     owns/collaborates on servers sees just "Shared with you" with no error.
     The default namespace is used only while `/namespaces` is pending or
     if it errors.

4. **ServerDetail** (`/servers/$name`) → `ServerDetailPage`
   - Full server view with query param `?ns=<namespace>` support
   - Lifecycle buttons (start/stop/restart) gated on phase state
   - Tabbed interface (below)

5. **CreateServer** (`/servers/new`) → `CreateServerWizard`
   - Multi-step form (template select, version pick, name, config, storage, networking, resources)
   - Supports query param `?template=<name>` to pre-select from Modules page Deploy link
   - Validation on each step
   - If tunnel is enabled with credentials entered at create time, the mutation saves the GameServer first, then saves tunnel credentials to a Kubernetes Secret. If credential-save fails after the server is created, the error is rethrown with `{ cause: err }` to preserve the original error chain, and includes remediation text (the server exists; credential can be set from Networking settings)

6. **Modules** (`/modules`) → `ModulesPage`
   - Merged catalog from all registered ModuleSources + installed Module CRs
   - Browse by game, install from catalog, manage installations, bulk upload
   - **Module Builder** (`BuildModuleDialog.tsx`): 3-step modal wizard for creating, validating, simulating, and packaging custom game modules directly from the dashboard:
     - **Step 1 (Preset & Metadata)**: Choose an archetype preset (`steamcmd`, `java`, `generic`), enter DNS-1123 module name with live validation, display title, summary, and canonical category chips.
     - **Step 2 (Container & Ports)**: Configure container image ref (with digest pinning verification badge), custom TCP/UDP ports with advertise flags, and persistent volume size and mount path.
     - **Step 3 (Review & Export)**: Dual-pane view with syntax-editable YAML manifests (`module.yaml`, `template.yaml`, `README.md`), live offline validation diagnostics, memory scaling simulation (`autoFromMemoryLimit`), and action buttons to download a `.tar.gz` archive or install directly into a cluster upload `ModuleSource`.

7. **Cluster** (`/cluster`) → `ClusterPage` (gated by `cluster:read` permission)
   - Selected cluster health and node inventory; **Clusters** (`/clusters`) lists authorized registrations
   - Kubeconfig download and node join credentials are local-only and require the corresponding cluster-operation permission and enabled configuration

8. **Users** (`/users`) → `UsersPage` (gated by `users:manage` permission)
   - Create/edit/delete users and OIDC links
   - Manage role bindings per namespace

9. **AdminSettings** (`/admin`) → `AdminSettingsPage` (gated by `config:manage` permission)
   - Sections: General (version), Telemetry (destination, tiers, install ID, preview, status), Authentication (OIDC providers), Mod registries (API keys),
     Notification sinks (Discord/Slack/SMTP/webhook), Backup destinations
   - **General:** Instance name is optional. It names this install on the Cluster page and in notifications, which fall back to "Gameplane" when it is blank
   - **Telemetry (spec 022):** the section reads `GET /admin/telemetry` (`Telemetry.get()`, query key `["telemetry"]`, refetched after a save and after an install-ID reset) and the stored consent via `GET /admin/config`. It shows the destination in effect (`default`, `custom`, `bundled`, `disabled` by the operator, `none`) with a link to the data-handling statement (`TELEMETRY_STATEMENT_URL` in `src/lib/links.ts`), two switches ("Enable/Disable telemetry" for basic, "Enable/Disable extended telemetry" for extended), the install ID with a confirmed "Reset ID" (`POST /admin/telemetry/install-id`), a read-only next-report preview identical to the payload, and a fixed-phrase delivery status (never, ok, failed, plus "Install ID replaced on <date>" when `lastIdRotationAt` is set; no failure detail is ever shown). Extended is disabled and shown off while basic is off, and turning basic off clears extended in the draft (the server also normalises it). When the operator disabled telemetry both switches are disabled and shown off and Save is disabled. Saves still go through `useSectionForm` and `PUT /admin/config/telemetry`.
   - **Telemetry notice (spec 022):** `src/components/ui/TelemetryNotice.tsx` renders the first-login banner at the top of the dashboard body for users with `config:manage` while `GET /admin/telemetry/notice` reports `pending`. It posts `seen` once on first render, offers "Keep sharing", "Turn off extended" and "Turn off all" (each posts the matching ack and hides the banner), and links to Admin Settings → Telemetry and to `TELEMETRY_STATEMENT_URL`. It has no dismiss button.
   - **Section save semantics:** each config section edits a local draft (`useSectionForm`). Nothing is stored until
     that section's Save; leaving the section discards the draft.
   - **Managed Secrets:** the API-managed Secrets behind identity providers (`gameplane-auth-<name>`), keyed mod
     registries (`gameplane-modreg-<provider>`) and notification sinks (`gameplane-notify-<name>`) change only as part
     of a section Save. Adding a row or replacing a key stages a Secret write; removing a row stages a Secret removal.
     Save runs the staged writes first, then the config `PUT` (whose rows reference those Secrets), then the staged
     removals once the `PUT` succeeds. A failed write or `PUT` shows in the section's save status and keeps the staged
     changes for a retry. A row added or removed but never saved leaves its Secret as it was. Secrets the section
     didn't create (a user-set `configRef`) are never removed.

### Install-Time Settings Display & OIDC Role Mapping Overrides (AdminSettings & Cluster split)

**Pattern:** Install-time cluster configuration (set via Helm values at deploy time) is read-only in the dashboard. These settings are **never dashboard-editable**; they originate from `installTimeSettings` injected via the API's `GET /admin/config` response.

**Data source:** `GET /admin/config` returns an optional `installTimeSettings` object (absent when there is nothing to report) containing:
- `gameDataStorageClass` — the `operator.gameDataStorage.storageClassName` Helm value (a plain string; never null when the object is present)
- `oidcHelmProvider` — OIDC provider metadata: `groupsClaim` (string), `defaultRole` (string), and `roleMappings` (the **Helm-seeded** role mappings as `{ admin?, operator?, viewer? }`, each role key optionally present)

**Split display across two routes (not one):**
1. **Cluster.tsx** displays the **storage class card** — `gameDataStorageClass` value or "Cluster default" badge if unset; rendered with a contextual hint when unset
2. **AdminSettings.tsx** displays the **OIDC provider and role mappings overrides** — all auth-related configuration

Admin settings section drafts follow refreshed configuration while pristine,
including delayed responses after navigation. Edits and staged Secret operations
remain local until Save. If a refreshed section differs from the draft's baseline,
Save reports a conflict before any Secret or config writes; the administrator can
copy their edits and reopen the section to start from current values. A successful
save rebases on refreshed config without discarding edits made during the request.
Dedicated role-mapping resets update both the baseline and draft.
If the refresh following a successful PUT fails, the submitted values remain the
committed baseline and visible draft (preserving newer local edits). Stale cached
values cannot reset that draft. Save requires a successful refresh before any
further writes when the last configuration read failed.

**Why the split:**  
A StorageClass is a cluster infrastructure concern, not an authentication concern. Cluster.tsx is the natural home for infrastructure settings; AdminSettings.tsx owns auth-only config. This separation keeps concerns aligned with where users expect to find them.

**Permission gating:**  
- **Route guard (web/src/router/tree.tsx):** The `/admin` route itself is guarded by `config:manage` permission (admin and operator roles only).
- **AdminSettings.tsx** (OIDC/auth section): The `RoleMappingOverridesCard` component is gated by `config:manage` permission check at render time — since it is an editing surface for role mappings (not read-only display). The route-level and component-level gates are now aligned.
- **Cluster.tsx** (storage card): Is **not** already admin-only, so it requires an explicit `can(me, "config:read")` permission check on both the query (to avoid 403 errors) and the rendered card (to hide it from viewers lacking permission). Precedent: Dashboard.tsx:51 and Users.tsx:688.

**OIDC Role Mapping Overrides (AdminSettings.tsx):**

The `helmOverride` object in the API's `AuthCfg` response (`auth.helmOverride.roleMappings`) allows dashboard users to override the Helm-seeded role mappings **per role**. Each role (admin/operator/viewer) is independently optional:

- **Key present** (including empty list `[]`): an override exists, stored in the dashboard database, superseding the Helm value
- **Key absent**: no override; the role uses the Helm-seeded value
- **Empty list `[]`**: distinct from absent; means "nobody maps to this role" (intentionally empty)

**Provenance is derived client-side from key presence**, not from a server-provided `source` field:
- If `helmOverride.roleMappings[role]` is present → "Overridden in dashboard"
- If absent and Helm-seeded value exists → "From Helm values"
- If absent and no Helm-seeded value → "Not configured"

**Per-role UI in RoleMappingOverridesCard (AdminSettings.tsx):**

For each role (admin/operator/viewer), the card displays:
1. **Effective mapping:** the list of IdP group names currently mapped (from override if present, else from Helm-seeded)
2. **Provenance badge:** one of the three states above
3. **Edit controls:** add/remove IdP group names
4. **Reset button:** "Reset to Helm default" shown only when an override exists (allows reverting to Helm values). A successful reset also drops that role's override from the section draft, so the card shows the Helm value at once and a later save doesn't write the override back. A failed reset shows its error in the Authentication card's save status and leaves the draft unchanged.
5. **Empty state:** a visual indicator when zero groups are mapped

**FR-012 — Empty state & remediation:**  
When no role mappings exist (neither overridden nor Helm-seeded), the card displays an empty state. Two remediation paths:
1. If the operator *is* configured (Helm `oidc.enabled`): add role mappings via the overrides editor
2. If the operator *is not* configured: the Helm provider panel shows a banner; no override editor is available (no mappings to configure)

**FR-015 — Admin-role mapping confirmation modal:**

Mapping users to the admin role grants full cluster control. The dashboard enforces a two-step confirmation:

- **Read-only Helm-seeded path (HelmOIDCProviderCard):** If Helm-seeded role mappings include admin mappings, show a warning banner (no confirm step; the admin already made this decision at deploy time)
- **Editable override path (RoleMappingOverridesCard):** When a user tries to **add** an admin role mapping via the overrides editor, show `AdminMappingConfirmDialog`

The confirm dialog displays:
- A fixed warning banner with exact copy: "Mapping users to the admin role grants full cluster control. Ensure the mapped group contains only authorized personnel. Anyone in these groups gets full admin access from their next login."
- The group name(s) being mapped
- Requires explicit user confirmation before the override is saved

**Warning unconditional on group size:**  
The warning is shown regardless of how many groups are being added (single or multiple). Gameplane cannot enumerate IdP membership at the dashboard layer, so it cannot verify that a group contains only authorized personnel — the warning applies uniformly.

**Implementation:** Uses existing form patterns (`useSectionForm`, `useUpdateConfigSection`, `useResetRoleMapping` hook for the reset DELETE request) for unified save/cancel. Overrides are written via the existing `PUT /admin/config` endpoint's `AuthCfg` round-trip (no separate endpoint). Resets use `DELETE /admin/config/auth/role-mappings/{role}`.

10. **AuditLog** (`/admin/audit`) → `AuditLogPage` (gated by `audit:read` permission)
    - Paginated audit event table (action, actor, resource, result, timestamp)
    - Server ownership transfer actions display as "Transferred ownership of" in the action column
    - Verify audit trail hash chain, export to CSV

11. **AdminLogs** (`/admin/logs`) → `AdminLogsPage` (gated by `*` wildcard permission)
    - API and system pod logs (tail, download)
    - Diagnostic endpoint for cluster issues

12. **Backups** (`/backups`) → `BackupsPage`
    - List Backups, Schedules, Restores (three sub-tabs)
    - Manual backup trigger, schedule create/suspend (no edit mode), restore from backup

13. **Share** (`/share/$token`) → `SharePage` (public route, outside AppLayout)
    - Public, unauthenticated, no sidebar or top bar
    - Resolves a share link token to its public view (server name, status, address, player count if exposed)
    - Rate-limited; invalid links and auth failures show neutral "Link not available" copy per FR-005, without raw error detail
    - Starting polls wait at least five seconds after each completed request, with one request in flight. Transient 429/5xx/network failures retain the last public Starting view (or loading spinner during initial resolve) and retry with 10/20/40/60-second bounded backoff; a larger Retry-After (seconds or HTTP date) is honored. Successful Starting/Pending responses reset the delay; Running, asleep, or neutral invalid responses stop polling. Token changes/unmount abort requests and ignore stale completions. `Shares.resolve` propagates transient errors with `APIError.retryAfter`, while invalid/expired/revoked 404s remain the same neutral response.
    - Five states: loading (spinner), up (server online), asleep-start (sleeping, can start), asleep-viewonly (sleeping, view-only), starting ("starting up" copy, shown both after a visitor's Start and for an ordinary start), invalid (link unavailable)
    - Respects stored appearance preference (light/dark/system); no theme toggle shown
    - Uses HeroUI Card, Button, Chip, Spinner; brand header with ShieldCheck icon; address copy button
    - Built directly from HeroUI primitives; no ui/ atom components

## ServerDetail Tabs

Visible tab set depends on server template + active version:

1. **Overview** — GameServer status, phase, uptime, restart count; live metrics from RCON (if available); recent pod events
2. **Events** — Kubernetes events: image pulls, scheduling, crash-loop, agent startup
3. **Console** — Interactive RCON/WebSocket terminal (hidden if template has no console, or consoleMode=none)
4. **Logs** — Live pod stdout OR configured game log file (agent-provided via mTLS; see gameplane.local/logPath annotation)
5. **Files** — Browser and editor for server data files (config, save games, logs)
6. **Mods** — List/install/remove mods; browse by registry provider (Modrinth, CurseForge, etc.) if template declares one
7. **Modpacks** — Install modpacks (only if template + active version supports loader with modpack capability)
8. **Players** — Online player snapshot, ban list, whitelist, kick/ban/unban actions
9. **Backups** — Per-server backup list, schedule management, restore trigger; reads backup repository URL from Secret `key: "repo"`
10. **Capture** — a `CaptureWidget` component driving start/stop of packet captures and a table of past captures for this server, gated on the `captures:manage` permission. Sits between the Backups and Settings tabs per `design-export/json` node `O08uaD`/`b4eaUf` (start-capture modal) and `m5kOm4` (capture list). `CaptureWidget.tsx` with `Captures` client namespace (`web/src/lib/api.ts:127-175`) and router/tab wiring (`ServerDetail.tsx:278`) are implemented in `web/src`.
11. **Settings** — Grouped form with sub-sections (below); changes are draft-until-save; conflict detection on reload

On narrow screens, server actions wrap and the detail tab strip scrolls inside its navigation container. Selecting a tab keeps that tab visible without horizontally scrolling the page or moving the panel controls off-screen.

### Permission Gates (Tab Visibility & Control Access)

ServerDetail verifies the selected server's access response against its cluster,
namespace, name and UID before providing access through `ResourceTargetProvider`:

- **Capture tab:** Once identity resolves, visibility and controls require `resourceCan` to find `captures:manage` in the verified target's permissions. `/users/me` provides identity readiness, not a replacement permission source. While identity loads, the tab stays visible and the widget shows "Checking access…" with capture queries and controls closed. If identity loading fails, the widget offers a retry through the identity-unavailable state. A resolved identity without the target grant receives the access-denied card. Capture settings use the same target permission and suppress denial text until target permissions resolve.
- **Mods/Modpacks tab controls:** Install/manage actions use the selected access response's `canControl`, which the API resolves from that server's permissions and allowed ownership/collaborator access. A grant in another cluster or namespace does not enable these controls.

**Other permission checks:** The `can(me, permission, namespace?, cluster?)` helper (`web/src/lib/auth.ts`) mirrors the server's RBAC logic. With `permissionsByCluster`, namespaced callers pass the explicit target cluster; only that cluster or the `*` wildcard cluster can supply a cluster-wide (`*` namespace) or exact namespace grant. Control-plane callers omit both namespace and cluster and accept a cluster-wide grant on any cluster. When `permissionsByCluster` is absent, `can` retains the legacy flat `permissions` fallback. Server resource controls use their verified target access instead of a global cluster selector. The API enforces authorization independently of UI visibility.

## ServerDetail Settings Sub-sections

Settings tab (`SettingsTab`, `web/src/routes/tabs/Settings.tsx`) displays 12 sections in a left sidebar (`SECTIONS` array; a 13th, Game configuration, is inserted after Version when the template declares a `configSchema`):

Save fetches the latest server and applies only the changes from the original baseline to the draft (`web/src/lib/settingsMerge.ts`). Untouched spec fields, labels, annotations, status and resourceVersion survive concurrent edits. Nested object keys merge independently; arrays are atomic. Divergent changes to the same field, including deleting a concurrently modified field, show the existing reload prompt before any PUT. Clearing config/label/annotation maps removes baseline keys while preserving concurrently added keys. Save waits for the template and validates the entire draft's game configuration regardless of the selected section; section-reported editor validity is retained across navigation. Discard, reload and successful save reset editor state and validity.

1. **General** — Server name, description, game-icon image field with placeholder from template's image or "(template image)" when no template
2. **Version** — Template version selector (triggers container restart)
2a. **Game configuration** — `GameConfigSection` (`web/src/routes/tabs/settings/GameConfig.tsx`), key `config`, shown only when `template.spec.configSchema` has entries (or the server still carries `spec.config` keys, so stray keys can be removed). Edits `draft.spec.config` through the tab's shared draft/Save footer; renders fields with the shared `ConfigFields` (`web/src/components/server/ConfigFields.tsx`, variant `settings`). An unset key means the template default (or the auto value); a value equal to the schema default removes the key, and an emptied map is written as `undefined` (never `{}`). Client validation (`validateConfig`, `web/src/lib/validation.ts`): required, enum, int (whole number, min/max), bool, string/password length; failures show inline and disable Save through `onValidityChange` (the operator stays authoritative; the API does not validate against the schema). Password fields are write-only: the API returns the marker `__gameplane_redacted__` for a stored password, the input is empty with the placeholder "Unchanged — type to replace" and a "Write-only" note, the marker is never rendered, stays in the draft until the user types (the PUT then keeps the stored value) and typing replaces it. `target: file` fields carry a "Written to file" note. Keys present in `spec.config` but absent from the schema are listed with a "Remove" button ("No longer in the template") because the operator rejects unknown keys. An info alert states that saving restarts the server (always; there is no apply-later mode). When `status.conditions[type=Ready].message` starts with `invalid config:` a danger alert shows it. Without `servers:write` (`access.canWrite`) the fields are disabled, the alert is hidden and the footer shows "You need permission to change this server's settings." instead of Discard/Save.
3. **Resources** — CPU request/limit, memory request/limit (Kubernetes resource specs)
4. **Networking** — Service type (ClusterIP/NodePort/LoadBalancer), LoadBalancer hostname, address pool / explicit address request, port overrides. Tunnel validation (`tunnel.enabled`, provider-specific config, credentials) is computed during render and reported via `onValidityChange` callback in an effect; local field state for `addressPool` and `address` is held in `useState` — so consecutive edits within one render are cumulative rather than each recomputing against the same stale `net` snapshot — and is re-seeded from props by two complementary mechanisms: on identity change, via a parent `key` remount; and in-render, whenever the incoming values differ by value from the last synced pair (so a save round-trip or a reload that returns changed values for the *same* server is picked up).
5. **Environment** — Custom env var key=value pairs
6. **Lifecycle** — Pre/post-start/stop scripts, quiesce grace period
7. **Scheduled backups** — `BackupsSection` (`web/src/routes/tabs/settings/Backups.tsx`): a single inline toggle (enable/disable) and form editing `spec.backupPolicy` on the server draft — cron-expression input (no daily/weekly picker), destination picker, retention fields, and a suspend switch; not a separate CRUD list
8. **Network capture** — `NetworkCaptureSection` (`web/src/routes/tabs/settings/NetworkCapture.tsx`). Enable/disable switch for `spec.capture.enabled` (the opt-in ephemeral-container sidecar); states plainly that disabling stops new captures immediately but the already-injected sidecar container stays in the pod, idle, until the pod is next recreated (Kubernetes has no API to remove an ephemeral container); admin-access + unredacted-data warning; a Retention Window control (numeric value + seconds/minutes/hours/days unit select) writing `spec.capture.retentionSeconds`, validated client-side against the cluster ceiling of 604,800 seconds (7 days — a storage-limitation-informed engineering default, explicitly not a legal requirement) mirroring the CRD's `+kubebuilder:validation:Maximum=604800` on `CaptureConfiguration.RetentionSeconds` (`operator/api/v1alpha1/gameserver_types.go`). Controls are disabled, with an explanatory note, for a session lacking the `captures:manage` permission (`api/internal/rbac/catalog.go`) — the API is still the real enforcer (FR-005: non-admin capture operations get 403).
9. **Placement** — Raw-JSON Monaco editors for `spec.tolerations` (array) and `spec.affinity` (object), each with inline parse-error feedback; no node-selector builder UI (`web/src/routes/tabs/settings/Placement.tsx`)
10. **RBAC & access** — Server owner + collaborator list, permission inheritance. The `setCollaborators` mutation runs unconditionally at component render (not gated by an early return), so hook invocation order is consistent. The mutation's namespace is derived from the GameServer's `metadata.namespace` (or `gameplane-games` as fallback); when no server is loaded, the mutation returns early without calling the API. On success, the mutation invalidates the `["server", gs.metadata.name]` query cache, clears input state, and resets errors; on error, it sets a locally-rendered error message and does not clear input, allowing retry.
11. **Danger zone** — Wipe world data (confirm-dialog), transfer ownership (confirm-dialog), delete server (confirm-dialog); no Clone action (`web/src/routes/tabs/settings/Danger.tsx`)

**Share links (T179):** Mounted in `Settings.tsx` by slice 5 (`ShareLinksSection`). `ShareLinksSection` (`web/src/routes/tabs/settings/ShareLinks.tsx`). Create/list/revoke share links per server; table shows Created, Expires, Can start capability (view-only vs. can-start), Status (Active/Expired/Revoked). The status shows Revoked when revokedAt is set, regardless of expiry state. Create dialog opens to set expiry and start permission; success shows Created dialog with full URL and one-time copy prompt (token hashed server-side and unrecoverable after close). Revoke dialog (AlertDialog danger) confirms destruction. Empty state when no links exist. Visible only to users with the API-enforced permission (owner/admin create/revoke, viewers see list read-only if API permits). Uses HeroUI Modal/ModalBackdrop/ModalContainer/ModalDialog/ModalHeader/ModalHeading/ModalBody/ModalFooter, AlertDialog family, Button, Table, Select, ListBox, ListBoxItem, Label, Description, Switch, Chip. Only Active links show a Revoke button; Expired and Revoked rows show "—" (design xCJlu).

**Configurable expiry (feature 017, `ShareLinks.tsx`):** The expiry selector offers exactly six choices — 15 days, 30 days, 60 days, 90 days, No expiry, Custom (date picker) — with 30 days pre-selected, replacing the old fixed 24h/7d/30d/90d menu and its "Maximum 90 days" help text. Selecting "No expiry" shows the warning "This link works until you revoke it." and sends `neverExpires: true`. Selecting "Custom" shows a date picker restricted to dates strictly after today (no maximum); choosing a date 365 days or more out additionally shows "Long-lived link — it stays valid for over a year unless you revoke it." Presets and the custom date are converted client-side to an absolute RFC3339 instant (a custom date is valid through 23:59:59 in the owner's local time) and sent as `expiresAt`; the dialog never sends the deprecated `expiresIn`. The Expires column renders "Never" for a link with `expiresAt: null`, and such a link is never shown as "Expired".

**Capture types (`src/types.ts`, built):** `CaptureConfiguration` (`{ enabled?, retentionSeconds? }`, mirrors `spec.capture` — `retentionSeconds` is bounded by the CRD's authoritative `+kubebuilder:validation:Minimum=1 / Maximum=604800` on `operator/api/v1alpha1/gameserver_types.go`'s `CaptureConfiguration.RetentionSeconds`, omit to use cluster default (86400)), `CaptureStatus` (mirrors `status.capture`: `ready`, `activeCapture`/`lastCaptureTime` typed nullable since the API's `formatOptionalTime` never omits the key), `CapturePhase` (`"Pending" | "Running" | "Completed" | "Failed" | "Expired"`), `NetworkCapture` (one capture record — merges the API's start/stop/list/get response shapes) and `NetworkCaptureList` (the `:captures` list envelope). All are implemented in the tree; the `CaptureWidget` component and `Captures` endpoint namespace are live (see Tabs and API Client sections above).

## External Interface / API Client

**Three-layer client:**

### Layer 1: Thin Fetch Wrapper (`lib/api.ts`)

```typescript
api<T>(path: string, opts?: Options): Promise<T>
```

- Base URL: relative paths (Vite proxy in dev, same-origin in prod)
- CSRF: reads `gameplane_csrf` cookie, injects `X-Gameplane-CSRF` header on POST/PUT/PATCH
- Cluster threading: uses the explicit `Options.cluster` or existing URL selector; defaults to local. Central endpoints do not inherit a workload cluster. Resource clients bind requests to the open target.
- Error: throws `APIError(status, body, statusText?, contentType?)` on !ok; detects HTML response bodies (text/html or body starting with `<`) and sets isHTML flag; TanStack Query treats it uniformly
- 204 No Content: returns `undefined as T`
- Credentials: `include` (send cookies)

**Helpers:**
- `csrfHeaders()` — returns `{ "X-Gameplane-CSRF": token }` for raw fetch (multipart, plaintext)
- `withClusterParam(path, clusterId)` — appends an explicit non-local cluster unless the URL is already scoped or the endpoint is centrally managed

### Layer 2: Typed Endpoint Namespaces (`lib/endpoints.ts`)

Each namespace is an object of typed functions building and fetching URLs:

- **Servers** — `list(ns?)`, `get(name, ns?)`, `create(body)`, `update(name, body, ns?)`, `remove(name, ns?)`, `lifecycle(name, verb, ns?)` (start/stop/restart), `clone(name, newName, ns?)`, `wipeData(name, confirm, ns?)`, `transfer(name, userId, ns?)`, `setCollaborators(name, ns, body)`, `getMyServers()`, `status(name, ns?)`, `events(name, ns?)`, `runAction(name, body, ns?)`, `mods(name, ns?)`, `installMod(name, body, ns?)`, `removeMod(name, mod, ns?)`, `modUpdates(name, ns?)`, `uploadMod(name, file, ns?)` (FormData), `registryProviders(name, ns?)`, `searchRegistry(name, opts?, ns?)`, `modVersions(name, project, provider?, ns?)`, `modpackDeps(name, project, provider?, ns?)`, `installModpack(name, body, provider?, ns?)`, `modIDs(name, ns?)`, `setModIDs(name, ids, ns?)`. **ServerCreate request shape:** `create(body)` accepts a `ServerCreate` object with optional `networking` sub-field carrying `expose`, `hostname`, `sourceRanges`, `portOverrides`, `addressPool` (load-balancer pool name), and `address` (requested IP); these are threaded into the `spec.networking` of the created GameServer. **Server response / GameServerEndpoint shape:** Each server's `status.endpoints` is an array of `GameServerEndpoint` objects, carrying: `name` (endpoint identifier), `host`, `port`, `protocol`, `private` (true for tailnet-only addresses), `tunnelProvider` (non-empty for tunnel-routed endpoints like frp/tailscale/playit), and `pool` (the load-balancer address pool the address was allocated from; set by the reconciler when a pool request is honored, absent for other address sources).

- **Templates** — `list()`, `get(name)`

- **Namespaces** (F-263) — `list()` → `{ namespaces: string[] }`, the namespaces the caller may read servers in on the requested cluster. The unified server list uses `/fleet/servers` and its authorized scope metadata.

- **Cluster** — `info()`, `stats()`, `view()`, `addNode()` (POST), `kubeconfig()` (blob download)

- **Clusters** — `list()` (multi-cluster registry)

- **Backups** — `list()`, `get(name)`, `create(opts)`, `remove(name)`

- **Schedules** — `list()`, `get(name)`, `create(opts)`, `patchSpec(name, patch)` (suspend toggle), `remove(name)`

- **Restores** — `list()`, `create(opts)`, `remove(name)`

- **BackupDestinations** — `list()`, `get(name)`, `upsert(body)` (POST), `remove(name)`

- **Players** — `snapshot(server, ns?)`, `banned(server, ns?)`, `moderate(server, action, body, ns?)` (kick/ban/unban), `whitelist(server, ns?)`, `whitelistAdd(server, name, ns?)`, `whitelistRemove(server, name, ns?)`

- **Users** — `me()`, `list()`, `create(body)`, `update(id, body)`, `remove(id)`, `resetPassword(id, password)`, `bindings(id)`, `addBinding(id, body)`, `removeBinding(id, roleName, namespace)`, `getPreferences()` (GET `/users/me/preferences`), `updatePreferences(body)` (PUT `/users/me/preferences`; optional custom fields omitted, never null — server-side FR-012 retention keeps stored customs), `resetPreferences(body?)` (POST `/users/me/preferences/reset`, the only operation that clears customs)

- **Roles** — `list()`, `catalog()` (permission groups), `create(body)`, `update(name, body)`, `remove(name)`

- **Auth** — `login(body)` (local), `logout()`, `oidcStartURL(name?)`, `providers()` (pre-auth public)

- **AuthProviders** — `putSecret(name, body)` (clientSecret), `deleteSecret(name)` (admin)

- **Audit** — `page(limit, before)` (pagination), `verify()` (hash chain), `exportCsv(filter?)` (blob)

- **Notifications** — `test(name)`, `putSecret(name, body)` (sink credentials), `deleteSecret(name)`

- **ModRegistries** — `putSecret(provider, apiKey)`, `deleteSecret(provider)`

- **Files** — `list(server, path, ns?)`, `read(server, path, ns?)`, `write(server, path, content, ns?)`, `mkdir(server, path, ns?)`, `remove(server, path, recursive?, ns?)`, `upload(server, dir, files, ns?)`, `downloadURL(server, path, ns?)`

- **Logs** — `downloadURL(server, ns?)`, `fileStreamPath(server, ns?)` (WebSocket), `podStreamPath(server, ns?)` (WebSocket)

- **Modules** — `catalog()`, `list()`, `get(name)`, `install(body)`, `upgrade(name, version)`, `uninstall(name)`

- **ModuleSources** — `list()`, `create(name, spec)`, `update(name, spec)`, `remove(name)`, `upload(source, file, opts?)` (blob), `removeUpload(source, module)`

- **ModuleBuilder** — `scaffold(body)`, `validate(body)`, `preview(body)`, `installToCluster(body)`, `downloadArchive(body)`

- **Captures** (`web/src/lib/api.ts:127-175`) — wraps the 8 REST routes `api/internal/handlers/capture.go` mounts under `MountCapture` (all gated by the `captures:manage` permission, `api/internal/rbac/catalog.go`): `POST /servers/{name}:capture-enable`, `POST /servers/{name}:capture-disable`, `POST /servers/{name}:capture-start`, `POST /servers/{name}:capture-stop`, `GET /servers/{name}:captures` (list), `GET /servers/{name}:capture?id=` (status), `GET /servers/{name}:capture-file?id=` (download), `DELETE /servers/{name}:capture?id=`. Per `specs/done_003-network-capture-sidecar/research.md`'s "Decision 1: Download Path" (T007, resolved): the download handler streams directly from the capture sidecar's `:9091 GET /captures/{id}/file` through the existing `<gs>-agent` ClusterIP Service's second port — not proxied through the agent's general `/files/*` file-browser surface (which is single-rooted at `--data-root` and cannot serve the capture emptyDir). The dashboard API client is expected to call through that one `:capture-file` indirection point rather than reconstructing the sidecar path itself.

**Helpers:** `withNS(path, ns?)` appends `?namespace=<ns>` when provided; `withCluster(path, clusterId)` uses an explicit cluster, defaulting to local. `createRequestClient` binds requests to a cluster and namespace; `createResourceClient` adds the target resource identity.

### Layer 3: Domain Helpers (`lib/*.ts`)

- **capabilities.ts** — `resolveConsoleMode(template)`, `serverHasMods(template, server)`, `serverHasModpacks(template, server)` — drive tab visibility
- **useThemePreferences.ts** — theme preferences plumbing (feature 016; full behavior documented under "User Theme Customization" above): `useThemePreferences(me?, opts?)` reconciles the backend `preferences` from `/users/me` over the `gameplane-theme-prefs` localStorage cache (backend wins on drift), exposes `preferences` + an optimistic `updatePreferences(patch)` mutation (DOM + cache first, PUT via `Users.updatePreferences`, revert-and-report on failure, replay-on-`online` after connectivity loss), and keeps the DOM attributes (`class`/`data-theme`, `data-theme-preset`, `data-theme-type`, `data-custom-css`) and the `#gameplane-custom-css` overlay (always last in `<head>`; unmounted on logout and on `/login`/`/share/:token`) in sync. Plain helpers exported for routes and the boot script's post-boot path: `isSafeModeActive()` (`?safe-mode=1` or the `gameplane-safe-mode` sessionStorage flag — suspends only the overlay), `readThemePreferences` / `writeThemePreferences`, `normalizeThemePreferences`, `themePreferencesEqual`, `applyThemePreferences`, `unmountCustomCssOverlay`, plus the storage keys/element ids and the `gameplane-theme-error` window event. The inline `theme-boot` script in `index.html` applies the same cache synchronously pre-paint and is the authoritative boot path (never applies cached prefs on `/login` or `/share/:token`).
- **servers.ts** — `countByState(items)` (running/stopped/pending/failed counts), `phaseGroups(items)` (items grouped by phase)
- **games.ts** — category helpers: `gameCategory(game)`, `resolveCategories(explicit, game)`, `matchesCategory`, `matchesAllCategories`, `categoryFilters(categories)`
- **auth.ts** — `useMe()` (current-user query hook), `hasRole(me, allowed)`, `can(me, perm, ns?, cluster?)`
- **config.ts** — admin-config hooks: `useConfig()`, `useUpdateConfigSection(section)`, `useResetRoleMapping()`
- **errors.ts** — `errorText(err, fallback?)`, `errorTextWithStatus(err, fallback?)`
- **cluster.ts** — `getCurrentCluster()` / `setCurrentCluster()` retain the localStorage-backed infrastructure selection; resource requests use their explicit target instead
- **validation.ts** — domain, port, K8s resource validation
- **events.ts** — event severity / reason → display strings
- **quantity.ts** — parse/format K8s quantities (500m → 0.5, 1Gi → 1073741824 bytes)

## Realtime (WebSocket & SSE)

### WebSocket (`lib/ws.ts`)

```typescript
openWS(path: string, opts: WSOptions)
  → { send(data), close() }
```

**Status states:** `connecting` | `open` | `reconnecting` | `closed` (emitted via `onStatus` callback)

**Behavior:**
- Auto-reconnect with exponential backoff: 500ms × 2^attempt, capped at 30s
- Attempt counter increments on each failed reconnection; resets to 0 on open
- `reconnect=false` option disables auto-reconnect (e.g., for intentional closes)
- Protocol detection: `wss://` on HTTPS, `ws://` on HTTP

**Used by:**
- Console tab: streams RCON/stdin input/output (bidirectional)
- Logs tab: streams container stdout and/or game log file (read-only)

**Cluster routing:** WebSocket paths retain the open server's cluster and namespace. Pod startup/stdout logs and PTY attach use that cluster's Kubernetes client; remote RCON and game-file logs use its authenticated gateway. Reconnects retain the original target. Leaving the server view closes its streams; infrastructure selection cannot retarget them.

### Server-Sent Events (`lib/sse.ts`)

```typescript
openEventStream(opts: EventStreamOptions)
  → () => void  // disposer
```

**Behavior:**
- Connects to `/events` (EventSource, auto-reconnect on transient close)
- Each frame is a Kubernetes watch event: `{ kind, eventType, object }`
- Frames are parsed as JSON; malformed frames are silently dropped
- Closes the stream when the tab becomes hidden (document.hidden); clears pending retries and reconnects when visible, calling onReconnect. A tab that is already hidden when `openEventStream` is called (opened in the background) does not connect at all until it first becomes visible
- Manual reconnect on onerror (after 3s backoff) if the browser closed the stream
- No-op fallback if EventSource is undefined (jsdom, ancient browser)

**Used by:**
- Global event listener in `main.tsx` or a provider component
- Invalidates the TanStack Query caches that show the event's kind (watches servers, templates, backups, schedules, restores) via `queryFilterForKind`; it never invalidates a whole `["fleet"]` or `["resource"]` prefix
- Powers notifications panel (shows recent activity)

**Note:** SSE does not thread cluster context (local only, for now).

## Key Invariants

1. **TypeScript strict mode** — `tsconfig.json` sets `strict: true`, `noUnusedLocals`, `noUnusedParameters`, `noFallthroughCasesInSwitch`
2. **No unjustified `any`** — `@typescript-eslint/no-explicit-any: error`; any real `any` must be a comment explaining why
3. **No floating promises** — `@typescript-eslint/no-floating-promises: error`; either `await` or `void` prefix every Promise
4. **Design-first for visuals** — all UI/layout changes start in `design.pen`, not React code
5. **Pre-auth privacy** — login page and unauthenticated screens leak no internal state (rule 3, CLAUDE.md)
6. **Fix, not silence** — ESLint / TypeScript flags are fixed at source, never suppressed inline (rule 4, CLAUDE.md)
7. **Operator is authoritative** — business logic lives in the operator; the dashboard is a pure view layer
8. **Generic, template-driven rendering** — Game configuration (Create Server steps, Settings tab) and status (Overview metrics) render via the template's declared schema (`spec.configSchema`, `spec.capabilities.status.metrics`, etc.), with no per-game branching; the surface works identically for every game type (FR-024). The Create wizard's Configure step and Settings > Game configuration share one field renderer, `ConfigFields` (`web/src/components/server/ConfigFields.tsx`)

## Dependencies

**Runtime:**
- `react@18.3.1` — React library
- `react-dom@18.3.1` — DOM renderer
- `@tanstack/react-router@1.75.0` — file-based routing (via tree.tsx)
- `@tanstack/react-query@5.59.0` — data fetching, caching, invalidation
- `@tanstack/react-virtual@3.10.8` — virtualized lists for large tables
- `@heroui/react@3.2.4` — component library (Button, Card, Modal, Table, Tabs, etc.); underlies every atom in `web/src/components/ui/`
- `@heroui/styles` — HeroUI's semantic token / theme layer, mapped to Gameplane brand tokens (see `specs/done_014-heroui-web-rebuild/contracts/theme-tokens.md`)
- `clsx@2.1.1` — conditional classNames
- `tailwind-merge@2.5.2` — Tailwind class conflict resolution
- `tailwindcss@3.4.13` — utility-first CSS framework
- `lucide-react@0.445.0` — SVG icon library
- `@monaco-editor/react@4.6.0` — code editor (lazy-loaded, file/config edit tabs)
- `@xterm/xterm@5.5.0` — terminal emulator (Console tab)
- `@xterm/addon-fit@0.10.0` — xterm fit-to-container addon

**Dev:**
- `typescript@5.6.2` — strict type checking
- `vite@5.4.8` — build + dev server
- `@vitejs/plugin-react@4.3.1` — React JSX transform + fast refresh
- `vitest@2.1.1` — unit test runner (Jest-like API, Vite-integrated)
- `@vitest/coverage-v8@2.1.1` — V8 coverage reporter
- `@testing-library/react@16.0.1` — component test utilities
- `@testing-library/jest-dom@6.5.0` — DOM matchers
- `@testing-library/user-event@14.6.1` — user interaction simulation
- `msw@2.14.4` — Mock Service Worker for request interception (e2e mock mode)
- `vitest-websocket-mock@0.4.0` — WebSocket mock for unit tests
- `@playwright/test@1.59.1` — browser e2e testing (mock + live modes)
- `jsdom@25.0.1` — DOM implementation for unit tests
- `eslint@9.11.1` — JavaScript linter
- `@typescript-eslint/eslint-plugin@8.7.0` — TypeScript linting rules
- `@typescript-eslint/parser@8.7.0` — TypeScript AST parser for ESLint
- `eslint-plugin-react@7.37.0` — React-specific rules
- `eslint-plugin-react-hooks@5.0.0` — React Hooks rules
- `tailwindcss@3.4.13` + `postcss@8.4.47` + `autoprefixer@10.4.20` — CSS processing
- `@tanstack/router-devtools@1.75.0` — TanStack Router dev tools (optional, for debugging)

## Security considerations

- **Pre-auth privacy:** The login page and unauthenticated screens leak no internal state: no hostnames, cluster names, server counts, version strings, or user-enumeration signals. Errors are neutral ("invalid credentials" only) — see CLAUDE.md rule 3 and docs/security.md.
- **CSRF protection:** Mutating requests carry a double-submit token; `gameplane_csrf` cookie is read and echoed as the `X-Gameplane-CSRF` header by `lib/api.ts` on POST/PUT/PATCH.
- **XSS surface:** User-controlled content rendered in the Monaco editor, xterm console, and log/event views is treated as untrusted. React's automatic escaping and avoiding `dangerouslySetInnerHTML` are the primary defenses.
- **No secrets in the bundle:** The SPA holds no API keys, credentials, or tokens; all privileged actions flow through the authenticated API. Session auth is cookie-based (credentials: include).
- **Session handling:** Auth is cookie-based (credentials: include); the dashboard never stores tokens in localStorage and relies on secure, HttpOnly cookies set by the API.

## Testing & Coverage

**Framework:** Vitest 2.1 + Testing Library (React) + jsdom

**Test files:** Co-located with source (`src/**/*.test.tsx`, `src/**/*.test.ts`)

**Mock server:** MSW 2 in mock e2e mode (Playwright); real API in live e2e mode

**Coverage thresholds** (`vitest.config.ts`):
- Lines: 92%
- Functions: 76%
- Branches: 82%
- Statements: 92%

**Exclusions from coverage:**
- `src/main.tsx` — bootstrapping only
- `src/router/**` — route tree is configuration, not logic
- `src/**/*.d.ts` — type definitions
- `src/types.ts` — type mirrors only
- `src/test/**` — test infrastructure
- `src/styles/**` — CSS
- `src/lib/config.ts` — config-only, no logic

**E2E testing:** Playwright in two modes:

1. **Mock mode** (`GAMEPLANE_E2E_TARGET=mock`, npm run test:e2e:mock)
   - Vite runs with `--mode mock`, loading `.env.mock` (sets `VITE_E2E_MOCK=true`)
   - Dashboard dynamically imports MSW browser worker at bootstrap
   - MSW intercepts every fetch; no cluster needed
   - Fast, deterministic, no side effects

2. **Live mode** (`GAMEPLANE_E2E_TARGET=live`, npm run test:e2e:live)
   - Tests run against a real Kubernetes cluster (gameplane-e2e, via kubectl port-forward)
   - globalSetup spawns port-forward, logs in as admin, saves session cookies
   - Tests inherit auth state from `.auth/storage.json`
   - Slower, flaky, full integration

**Execution:** Serial (workers: 1) because login state is shared across tests; retries: 0 local, 1 in CI.

## Remote server feature parity

The existing Mods, Modpacks and Capture screens use their immutable resource
target for both local and remote servers. ID-list editing stays disabled until
the target's current list has loaded successfully. Capture controls query
GET `/servers/{name}/capabilities` and verify the returned target identity.
Loading, unavailable or unsupported transport states cannot enable actions from
stale successful data. Capture enable/start and file download capabilities are
separate: disabling new captures at a site does not hide retained downloads.
Permission checks remain independent, and an already-open start dialog respects
subsequent capability loss. See [remote server feature parity](../docs/remote-parity.md)
for routing, identity and integration coverage.

## References

- **docs/architecture.md** — component overview, data flow, security boundaries, "operator is authoritative" rationale
- **docs/security.md** — auth model, RBAC, threat model, pod security, pre-auth privacy rule
- **docs/install.md** — Helm values, K8s prerequisites, OIDC setup (for deployment contexts)
- **CLAUDE.md rule 1** — Design-first: visual changes originate in design.pen, not code
- **CLAUDE.md rule 3** — Pre-auth privacy: login page must not leak internal metrics/hostnames/versions
- **CLAUDE.md rule 4** — Fix, don't silence: linter/type flags are fixed at source
- **CLAUDE.md rule 5** — TS strict; no unjustified `any`; no floating promises
- **design.pen** — Source of truth for all UI/layout (Pencil MCP server; do not edit as text)
- **README.md** — Project pitch, quickstart, architecture overview
