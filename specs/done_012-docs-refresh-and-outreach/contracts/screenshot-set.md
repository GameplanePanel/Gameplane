# Contract: Dashboard Screenshot Deliverable

**Feature**: 012 — Documentation Refresh and Outreach  
**Status**: Specification (Ruled 2026-09-02 — Ready for Implementation)  
**Applies To**: Dashboard screenshots in `docs/img/` and README.md gallery  
**Requirements Addressed**: FR-015, FR-016, FR-017, FR-018, FR-019, SC-009, SC-010, SC-011

---

## Purpose

This contract specifies the screenshot deliverable: which six existing JPEG files must be refreshed with current UI state, which five mandatory (plus one recommended) new JPEG files must be captured, where they appear in README.md, how to ensure data safety (no real hostnames/IPs), the capture environment, and verification procedures. The screenshot capture itself is PLANNED here; execution is deferred (per OD-3) to implementation.

---

## Scope: Six Existing Screenshots (FR-016)

The following six JPEG files in `docs/img/` are the source of truth for the current dashboard state. These **must be kept** (same filenames) and retaken to show the current web/ codebase as of v0.2.0-beta.8.

### Existing Screenshot Inventory

| Filename | Route | Component | Purpose | Alt Text |
|---|---|---|---|---|
| `dashboard.jpg` | `/` | `DashboardPage` | Fleet health overview, cluster stats, node status, recent activity feed | Fleet health at a glance: running/stopped/failed server counts, cluster CPU/memory/storage, node status, recent activity |
| `servers-list.jpg` | `/servers` | `ServersPage` | Table listing all game servers with status, resource usage, node placement | Every game server, one list: live status, CPU, memory, node placement |
| `server-overview.jpg` | `/servers/$name?tab=overview` | `ServerDetail` Overview tab | CPU/memory/disk usage sparklines, quick actions, sleep status, recent events summary | Server detail Overview: CPU, memory, disk usage, quick actions, connection info |
| `mods-registry-browse.jpg` | `/servers/$name?tab=mods` | `ServerDetail` Mods tab | Game template registry browser showing mod grid with metadata | Mods tab registry browser: grid of Thunderstore mods for Valheim, download counts |
| `server-console.jpg` | `/servers/$name?tab=console` | `ServerDetail` Console tab | Live xterm.js streaming output over WebSocket (game server stdout) | Live streaming console: Terraria server stdout, world-save progress |
| `admin-mod-registries.jpg` | `/admin?section=mod-registries` | `AdminSettings` Mod registries section | Admin configuration for external mod registry API keys (CurseForge, Steam, Nexus, etc.) | Admin Settings Mod registries: CurseForge and Steam Workshop configured, Nexus not configured |

**Evidence**: Route mappings from `/home/user/Gameplane/web/src/router/tree.tsx:5-144` and tab definitions from component files in `web/src/routes/`. Current README.md lines 18-27 link these six files.

---

## Scope: At Least Five New Screenshots, Six Recommended (FR-017)

At least five new JPEG files must be added to `docs/img/`, capturing currently uncovered screens. The table below ranks them by FR-017 priority ("screens new users encounter first"). **The first five are mandatory; a sixth is recommended. If capture cannot occur for a planned screen, substitute a lower-priority one and document why in the audit log (D-F).**

### Recommended New Screenshots (Ranked by FR-017 Priority)

| # | Filename | Route | Component | Purpose | Alt Text Draft | Dummy Data Required |
|---|---|---|---|---|---|---|
| **1** | `login.jpg` | `/login` | `LoginPage` | Authentication entry point; pre-auth surface with no metrics/hostnames (verified CLAUDE.md rule 3 compliance at Login.tsx:19-21) | Sign-in form with local username/password field and SSO provider buttons ("Continue with…"), brand logo, and marketing pitch sidebar describing Gameplane's features | None; login form is static UI. SSO provider button labels are mocked to generic names (e.g., "GitHub", "Keycloak") without issuer URLs |
| **2** | `create-server-template-select.jpg` | `/servers/new` (template selection step) | `CreateServerWizard` template step | Game selection in multi-step create workflow; shows available GameTemplates (Minecraft, Valheim, Terraria, Rust, Palworld, Factorio, CS2, ARK) with icons and descriptions | Game template grid in Create Server wizard: card grid showing game icons, titles (Minecraft Java Edition, Valheim, Terraria, etc.), brief descriptions, and "Select" button per template | 8+ `GameTemplate` resources from MSW fixtures: name, icon URL (data: URI), description. Include both "official" templates and an "example user-uploaded" template to show extensibility |
| **3** | `server-detail-events.jpg` | `/servers/$name?tab=events` | `ServerDetail` Events tab | Kubernetes event timeline for diagnostics; shows scheduling, image pulls, crashes, readiness transitions | Events tab timeline: shows Kubernetes events with reason (ImagePull, Scheduling, Ready), message (brief status), timestamp, and event type indicator; demonstrates failure diagnostics for a previously-failed server |  `GameServer` resource in Pending/Failed phase with `status.conditions` (Progressing=False, Ready=False). Kubernetes events list: mix of normal events (Scheduled, PullImage, Created) and warning events (ImagePullBackOff, CrashLoopBackOff) with realistic timestamps |
| **4** | `admin-settings-general.jpg` | `/admin?section=general` | `AdminSettings` General section | Instance identity configuration; external URL (used for OIDC callbacks), cluster namespace defaults | Admin Settings General section: form fields for Instance Name ("My Gameplane Cluster"), External URL ("https://gameplane-demo.local"), Default Namespace dropdown; visible sidebar navigation showing all 9 settings sections | Instance config from MSW handler: `{ instanceName: "My Gameplane Cluster", externalUrl: "https://gameplane-demo.local", defaultNamespace: "default" }`. Namespace dropdown with options: "default", "gameplane-production", "test-cluster" |
| **5** | `cluster-nodes.jpg` | `/cluster` | `ClusterPage` | Multi-node cluster management; shows node list with CPU/memory/storage utilization meters | Cluster page with node list: at least 3 nodes displayed, each showing uptime, CPU/memory/storage usage bars (green for available, orange/red for high usage), capacity info, and "Join Node" wizard card | 3+ `ClusterNode` resources with realistic resource metrics: CPU 40–80% utilization, memory 50–90%, storage 20–60% used; mix of "Ready" and "Provisioning" statuses to show operational variety |
| **6** | `server-detail-logs.jpg` | `/servers/$name?tab=logs` | `ServerDetail` Logs tab | Application log streaming view; shows server stdout/stderr lines as they arrive | Logs tab with live application output: shows >20 game server log lines (e.g., world save progress for Terraria, player join notifications for Minecraft) with timestamps; demonstrates scrolling capability; footer shows "tail 100 lines" selector | Streamed log lines (mocked WebSocket in mock mode): realistic game-server output. Example format for Minecraft: `[HH:MM:SS] [main/INFO] [net.minecraft.server.MinecraftServer]: Player alice joined the game`. Example for Terraria: `[HH:MM:SS] Server started in single-player mode.` |

**Evidence**: R4 research findings (screen inventory at lines 9–87, gap analysis at lines 106–211, capture assessment at lines 233–365); spec FR-017 examples (Login, Create Server, Server Detail Events/Logs, Admin Settings, Cluster) all exist and are ranked by user priority.

---

## README.md Gallery Structure (FR-015, SC-010)

The README's `## Screenshots` section (currently lines 18–27) is a 2-column table linking the six existing screenshots. This table MUST be extended to display at least 11 total images (six existing + at least five new).

### Current Structure (lines 18–27)

```markdown
## Screenshots

| | |
|---|---|
| ![<alt-text>](docs/img/dashboard.jpg) | ![<alt-text>](docs/img/servers-list.jpg) |
| <caption> | <caption> |
| ![<alt-text>](docs/img/server-overview.jpg) | ![<alt-text>](docs/img/mods-registry-browse.jpg) |
| <caption> | <caption> |
| ![<alt-text>](docs/img/server-console.jpg) | ![<alt-text>](docs/img/admin-mod-registries.jpg) |
| <caption> | <caption> |
```

### Required Changes

1. **Extend the table** to include all new screenshots (at least 5 new → 11+ total images)
2. **Maintain 2-column layout** (or 3+ columns if preferred; decision left to maintainer)
3. **Keep image rows first**, alt-text row second in each pair, for visual scanning
4. **Preserve existing alt text** from current README.md lines 22, 24, 26 for the six existing images
5. **Add new alt text** for each new image (see below)
6. **File paths** remain `docs/img/<filename>`
7. **Ordering**: Display in recommended priority order (new images first for evaluator impact, or existing images first for backward compatibility; recommend new-first for evaluator prioritization per FR-017)

### Gallery Alt Text Examples

**Format**: One sentence per image. State **purpose** (what the screen shows) and **key UI elements** (buttons, metrics, tabs, lists). Do **NOT** mention mocking in alt text; a single disclosure sentence in the README gallery intro (OD-3d, ruled 2026-09-02) covers all screenshots.

**Examples**:
- ✅ `Sign-in form with local username/password field and SSO provider buttons, brand logo, and marketing sidebar.`
- ✅ `Game template grid in Create Server wizard: card grid showing game icons, titles, descriptions, and Select buttons.`
- ✅ `Cluster page with node list showing 3 nodes with CPU/memory/storage usage meters, uptime, and capacity info.`
- ✅ `Logs tab showing >20 game server log lines with timestamps and scroll indicators.`
- ❌ `Shows the login page.` (too vague; does not state key UI elements)
- ❌ `Kubernetes event timeline.` (does not state purpose relative to operator/new user)
- ❌ `Logs tab with mocked game-server output (mock mode).` (do not mention mocking in alt text; README intro handles disclosure)

**README Gallery Disclosure**: A single sentence in the README's `## Screenshots` section intro states: "Screenshots are captured against mocked data for consistency and reproducibility; all UI layouts and components reflect the current dashboard." This disclosure applies to all 11+ images; alt texts focus on purpose and UI elements only.

**Evidence**: FR-018, SC-010; OD-3d ruling (2026-09-02).

---

## Format Convention: JPEG 1920×1080

All screenshots in `docs/img/` follow a consistent format. The six existing files (currently 1568×773 JPEG) are **replaced at 1920×1080 JPEG** with filenames kept per FR-016.

| Property | Specification | Evidence | Notes |
|---|---|---|---|
| **File Format** | JPEG | All files are JPEG; `docs/img/*.jpg` filename convention | Binary transparency not needed (not PNG); JPEG compression reduces file size while maintaining visual quality |
| **Viewport Dimensions** | 1920×1080 pixels (width × height) | Standard 16:9 HD resolution; ruled OD-3a (2026-09-02) | Existing six files are currently 1568×773 and are replaced at the new size. Provides a larger, clearer dashboard view. Playwright Desktop Chrome default is 1280×720; explicit viewport config required to match 1920×1080 |
| **Aspect Ratio** | 16:9 (landscape) | Standard HD widescreen | Accommodates dashboard left-sidebar + content area + right-sidebar with room for detail |
| **JPEG Compression Quality** | 75–85 (good quality, typical 60–95 KB per file) | Larger viewport increases file size proportionally from existing 47–74 KB range | Lossless/optimized compression acceptable; no visible quality loss |
| **Color Space** | sRGB (standard RGB) | Standard JPEG color space | No color management requirements; standard monitors render correctly |
| **Metadata** | Minimal (filename only) | No EXIF/XMP metadata required | Acceptable to strip metadata; timestamp and capture tool name not required |

### Playwright Viewport Configuration

To capture at 1920×1080 with Playwright (mock mode per OD-3, ruled 2026-09-02):

```typescript
// In playwright.config.ts or within test:
await page.setViewportSize({ width: 1920, height: 1080 });
```

Or set globally in config (preferred):

```typescript
use: {
  viewport: { width: 1920, height: 1080 },
  deviceScaleFactor: 1,
}
```

**Evidence**: OD-3a ruling (2026-09-02); FR-016 (keep filenames, refresh content); playwright.config.ts viewport override required.

---

## Dummy Data Rule: What NOT to Show (FR-019, SC-011)

Screenshots must not display **any** real user data, infrastructure details, or personally identifiable information that could aid operational reconnaissance.

### Forbidden Patterns (Do NOT Include)

| Category | Forbidden | Reason | Acceptable Substitute |
|---|---|---|---|
| **IPv4 Addresses** | `192.168.1.100`, `10.0.0.5`, `203.0.113.42` | Could expose private network topology or real server IP | `<internal>`, `[redacted]`, or omit field entirely |
| **Public Hostnames** | `prod-cluster-1.company.com`, `gameplane.example.com`, `minecraft.org` | Could identify real infrastructure or operator identity | `test-cluster-01`, `gameplane-demo.local` (use `.local` TLD for clarity) |
| **Real Cluster Names** | `production`, `aws-us-east-1`, `customer-acme-games` | Identifies specific deployments or customers | `test-cluster`, `demo-cluster`, `my-cluster` |
| **Real Usernames** | `alice@company.com`, `gamemaster`, `admin@customer.net` | Violates pre-auth privacy (rule 3) and user privacy | `test-user-01`, `admin-demo`, `operator-01` |
| **Real Email Addresses** | `ops@example.com`, `support@gameplane.io` | PII; could be harvested for social engineering | `test-user-01@local` (or omit, use generic "test user") |
| **Real Game Server Names** | `production-minecraft`, `customer-valheim-private`, `survival-map-v3` | Identifies operational intent | `test-server-01`, `demo-minecraft`, `example-valheim` |
| **Real Player Names / UUIDs** | Minecraft player UUIDs like `550e8400-e29b-41d4-a716-446655440000` or real game usernames | PII for game account holders | Fabricated UUIDs (`00000000-0000-0000-0000-000000000001`) or generic names (`Player-01`, `TestUser`) |
| **Real Module / Template Names** | Names showing customer-specific or copyrighted game variants | Mods tab might show user-uploaded custom modules | Stick to official template names (Minecraft, Valheim, Terraria) or generic examples (`custom-game-01`) |

**Note on Timestamps**: Timestamps are not sensitive data. FR-019 concerns real user data, hostnames, cluster names, and IP addresses only. Render whatever the UI displays at capture time; never back-date or fabricate dates to make documentation appear fresher than it is.

### Allowed Dummy Data Naming Scheme

Consistent test data makes screenshots cohesive and professional:

| Entity Type | Naming Scheme | Examples |
|---|---|---|
| **Servers** | `test-server-NN` or `demo-game-NN` | `test-server-01`, `demo-minecraft`, `example-valheim` |
| **Clusters** | `test-cluster` or `demo-cluster` | `my-cluster`, `test-cluster`, `local-dev` |
| **Namespaces** | `default`, `gameplane-demo`, `test-ns` | Do not use `production`, `prod`, `live` |
| **Nodes** | `node-01`, `node-02`, `node-03` (or auto-assigned node names from K8s) | Do not use cloud provider names like `aws-us-east-1-a` |
| **Users** | `test-user-01`, `admin-demo`, `operator-test` | Do not use names with `.com` or real email domains |
| **API Keys / Tokens** | Truncated or masked (`sk-...`) or omitted entirely | Do not show real secrets; mask with `[redacted]` if UI shows secret field |
| **Timestamps** | Not applicable — render whatever the UI displays at capture time; see Note above | No fabrication or back-dating required |
| **Module Names** | Official Gameplane templates + generic examples | Minecraft, Valheim, Terraria, Rust, Palworld, Factorio, CS2, ARK (shipped templates) or `test-mod-01` (custom) |

**Evidence**: FR-019, SC-011; CLAUDE.md rule 3 (login privacy); spec edge case (lines 82–89); R4 examples (lines 219–226).

---

## Capture Procedure (OD-3, Ruled 2026-09-02)

Screenshot capture uses **Playwright Mock Mode (Option A)** as of 2026-09-02 maintainer ruling. Three options were outlined for evaluation; the ruling selected Option A for speed, reproducibility, and CI integration. Implementation proceeds with mock mode; Options B and C remain as documented alternatives for reference.

### Option A: Playwright Mock Mode (MSW + Vite) — **RECOMMENDED**

**Prerequisites**: None (no cluster required)  
**Setup**: `GAMEPLANE_E2E_TARGET=mock npm run test:e2e:mock` (or equivalent Playwright invocation)  
**Configuration**: Uses existing `web/playwright.config.ts` and MSW handlers in `web/src/test/handlers.ts`

#### How It Works

1. Playwright launches Vite dev server on `localhost:5173` with mock mode enabled
2. MSW intercepts all API fetch requests and returns stubbed responses (realistic data shapes, mocked values)
3. Dashboard renders with mocked data (no live Kubernetes connection, no real servers running)
4. Screenshots capture the UI as it appears with mock data (no cluster provisioning needed)

#### Pros (Why Recommended)

- ✅ **Reproducible**: MSW handlers are deterministic; screenshots are identical on every run
- ✅ **Fast**: No cluster setup; Vite + Playwright screenshot cycle <2 seconds per screen
- ✅ **CI-native**: Runs in standard GitHub Actions runners (no Docker, kubectl, or cluster access needed)
- ✅ **Realistic UI**: MSW responses match production API shapes; UI rendering is identical to production
- ✅ **Data Control**: Test factories (`web/src/test/factories.ts`) can be updated per release without re-provisioning
- ✅ **Isolation**: Each test independent; no shared cluster lock or state drift between runs
- ✅ **Cost**: Zero infra overhead; no compute resources beyond GitHub Actions standard allocation

#### Cons & Trade-offs

- ⚠️ **Console / Logs Tabs**: Live stream (xterm, WebSocket) shows mocked log lines (not real server output). **Acceptable for screenshots documenting UI layout and scrolling capability.** README gallery intro discloses mocking once (OD-3d); alt texts focus on purpose and UI elements.
- ⚠️ **Cluster / Node Stats**: CPU/memory/storage meters show fixed mock values, not real cluster variation. **Acceptable for demo purposes.**
- ⚠️ **Player Rosters**: Player names and UUIDs are fabricated. **Acceptable; reinforces dummy-data compliance.**
- ⚠️ **Mod Registries**: Shows a subset of mods (5–10 per registry, not 100s). **Acceptable; documents the UI layout.**

#### Implementation Steps (OD-3b: Playwright Mock Mode, Ruled 2026-09-02)

1. **Create test spec** at `web/e2e/specs/screenshots.spec.ts` (exact path per OD-3b)
2. **Test spec is run on CI** via `.github/workflows/screenshot-refresh.yaml` (workflow_dispatch); `npm run screenshots` (sets `GAMEPLANE_SCREENSHOTS=1` environment variable; `playwright.config.ts` then greps the `@screenshots` tag to isolate from other e2e tests)
3. **Seed MSW fixtures** with realistic data:
   - 8+ `GameTemplate` objects (Minecraft, Valheim, Terraria, Rust, Palworld, Factorio, CS2, ARK)
   - 3–5 `GameServer` resources (mix of phases: Running, Pending, Failed)
   - 3+ `ClusterNode` resources with CPU/memory/storage usage
   - 10–20 audit events (mix of methods, status codes)
   - 5–10 mods per registry (Thunderstore, CurseForge, Steam)
   - 3–5 test users with different roles (admin, operator, viewer)
   - Schedule and Restore resources for Backups tab
4. **Authenticate once** at test start (MSW provides stub `/users/me` response)
5. **Navigate to each route** in recommended priority order
6. **Set viewport** to 1920×1080 (see Viewport Configuration above)
7. **Capture screenshot** for each route:
   ```typescript
   await page.screenshot({
     path: 'docs/img/<filename>.jpg',
     fullPage: false,  // Capture only visible viewport, not full page
   });
   ```
8. **Post-process JPEG** (optional): Re-encode losslessly for consistent compression (e.g., `imagemin`, `mozjpeg`)
9. **Commit screenshots** to git with all filenames matching the table above

#### Testing & Validation

Capture runs **only via GitHub Actions CI** (OD-15, ruled 2026-09-02). The sole capture path is the tag-triggered or workflow_dispatch screenshot-refresh workflow (`.github/workflows/screenshot-refresh.yaml`). No other capture method (local live, manual browser) is authorized. Any future capture path requires a new maintainer ruling.

---

### OD-3 Decision: Ruled — Use Option A (Playwright Mock Mode, 2026-09-02)

**Ruling**: Use **Option A (Playwright Mock Mode)** for the following reasons:

1. **Reproducibility**: Identical screenshots on every run; diff-able in git
2. **CI Integration**: Integrates into `npm run test:e2e:mock` pipeline; can run on every PR
3. **Speed**: Seconds per screenshot vs. minutes per cluster provisioning
4. **Maintainability**: Data fixtures (`web/src/test/factories.ts`) update per release without cluster re-provisioning
5. **Acceptance**: Trade-off (mocked live streams) is acceptable and documented once in README intro

**OD-3b Captured**: Playwright spec at `web/e2e/specs/screenshots.spec.ts` run with `GAMEPLANE_E2E_TARGET=mock` and tag/grep filter, output to `docs/img/`.

**OD-3c: Tag-Triggered Recapture Workflow**: A release-triggered GitHub Actions workflow (`.github/workflows/screenshot-refresh.yaml`) regenerates MSW fixture data and screenshots and opens a pull request with the new images. The credential for opening that PR is a fine-grained personal access token scoped to this repository with contents and pull-requests write permission, stored as a repository secret (ruled 2026-09-02, OD-13); The secret is named `SCREENSHOT_BOT_PAT` (confirmed 2026-09-02). The workflow also carries a `workflow_dispatch` trigger for on-demand captures (OD-15); unlike a GITHUB_TOKEN-authored PR it triggers CI normally.

---

## Common Capture Steps (All Options)

Regardless of which capture method is chosen, all captures follow these common procedures:

### Pre-Capture

1. **Clear browser cache** (or use incognito mode): Ensures CSS/JS loads fresh
2. **Disable browser extensions**: No ad blockers, password managers, or toolbars that might obscure UI
3. **Set timezone to UTC** (or a fixed timezone) if timestamps are visible in UI: Consistent across all screenshots
4. **Set system theme to light** (if applicable): Gameplane dashboard uses light theme by default; no dark-mode variants needed
5. **Verify viewport is exactly 1920×1080**: Use DevTools or Playwright to confirm before capture

### Capture

6. **Authenticate** (once at test start, reuse session for all screens)
7. **Navigate to route** per recommended priority table
8. **Wait for content to load** (Playwright: `await page.waitForLoadState('networkidle')`)
9. **Set viewport to 1920×1080** if not already set
10. **Screenshot** with `page.screenshot({ path: 'docs/img/<filename>.jpg', fullPage: false })`
11. **Verify file was written** and is 1920×1080

### Post-Capture

12. **Optional JPEG re-encode** (if needed): Use `imagemin-mozjpeg` or similar to maintain consistent compression (typical 60–95 KB at 1920×1080 resolution)
13. **Run `ls -lh docs/img/*.jpg`** to verify all 11+ files exist and are reasonable file sizes
14. **Spot-check alt text** for forbidden patterns (no hostnames, IPs, real usernames)
15. **Git add and commit** all new/refreshed screenshots
16. **Push to branch** and let CI verify screenshot integrity

---

## Substitution Rule (Spec Edge Case, Lines 82–89)

If a planned screenshot cannot be captured because:

- The feature is temporarily unavailable (e.g., a required component crashed or is disabled)
- The UI is not rendering (e.g., a route returns 404 or a page fails to load)
- The capture environment cannot reach the necessary state (e.g., mock mode MSW fixtures are incomplete)

**Then:**

1. **Substitute a lower-priority screen** from the tier list (Tier 2 or Tier 3 in R4, lines 140–211)
2. **Document the substitution** in the audit log (`specs/done_012-docs-refresh-and-outreach/audit-log.md`, per D-F):
   - Original planned screenshot (filename, route)
   - Reason (feature unavailable, route broken, fixture incomplete, etc.)
   - Substitute chosen (filename, route)
   - Evidence checked (path:line in logs or error message)
   - Resolution (e.g., "feature will be added in v0.2.0-beta.9; defer to next refresh cycle")
3. **Update the screenshot table** in this contract to reflect the substitution (filename, route, purpose)
4. **Commit the substitution decision** with the audit log entry

**Example**:
> **Original**: `admin-settings-authentication.jpg` (/admin?section=authentication) — OIDC provider form  
> **Reason**: Keycloak test fixture integration incomplete in MSW; route renders but form validation hangs  
> **Substitute**: `admin-settings-notifications.jpg` (/admin?section=notifications) — Event sink configuration  
> **Evidence**: web/src/test/handlers.ts:line 214 (authentication route stubbed but incomplete), playwright.config.ts globalSetup timeout logs  
> **Resolution**: Deferred to next minor release when OIDC seeding is available

---

## Verification Checklist

Use this checklist **before merging** the screenshot commits (D-F, per constitution Principle VI — CI is the system of record).

### File Inventory

- [ ] All six existing filenames are present in `docs/img/` (replaced at 1920×1080 per FR-016, OD-3a):
  - [ ] `dashboard.jpg` (1920×1080 JPEG)
  - [ ] `servers-list.jpg` (1920×1080 JPEG)
  - [ ] `server-overview.jpg` (1920×1080 JPEG)
  - [ ] `mods-registry-browse.jpg` (1920×1080 JPEG)
  - [ ] `server-console.jpg` (1920×1080 JPEG)
  - [ ] `admin-mod-registries.jpg` (1920×1080 JPEG)
- [ ] At least five new filenames are present:
  - [ ] `login.jpg`
  - [ ] `create-server-template-select.jpg`
  - [ ] `server-detail-events.jpg`
  - [ ] `admin-settings-general.jpg`
  - [ ] `cluster-nodes.jpg`
  - [ ] `server-detail-logs.jpg` (recommended sixth)
- [ ] All files are JPEG format (not PNG, not WebP)
- [ ] All files are exactly 1920×1080 pixels (run `identify docs/img/*.jpg` or similar)
- [ ] All files are <150 KB (reasonable compression; typical 60–95 KB at 1920×1080 resolution)

### README.md Gallery

- [ ] `README.md` lines 18+ contain a `## Screenshots` section
- [ ] Section includes all 11+ images (6 existing + ≥5 new)
- [ ] Each image is in an `![alt text](docs/img/filename.jpg)` Markdown link
- [ ] Each image has alt text (one sentence, purpose + key UI elements)
- [ ] Alt text is in the image markdown, not in a separate caption row (alt text is in square brackets, not in table caption)
- [ ] All filenames match the files in `docs/img/`

### Alt Text Compliance

- [ ] Each alt text is one sentence (under 160 characters ideally)
- [ ] Each alt text states **purpose** (e.g., "authentication entry point", "fleet health overview")
- [ ] Each alt text describes **key UI elements** (e.g., "form fields", "metrics", "buttons", "tabs")
- [ ] No alt text is vague (e.g., "Shows the dashboard" is too vague; "Fleet health overview with running/stopped counts" is good)
- [ ] Alt text does NOT mention mocking or mocked data (disclosure lives once in the README gallery intro per OD-3d).

### Dummy Data Compliance

- [ ] **Scan fixture source code** for forbidden patterns (hostnames, IPs, usernames, production labels):
  ```bash
  grep -nE "([0-9]{1,3}\.){3}[0-9]{1,3}|prod|production|@[a-z0-9-]+\.(com|net|org|io)" \
    web/src/test/screenshotData.ts web/src/test/handlers.ts
  ```
  (Scans the MSW fixture source, not rendered pixels, to catch hardcoded values in test data)
- [ ] **Documented visual review by maintainer**: Before merging the screenshot-refresh PR, maintainer reviews all 11+ final images for visible hostnames, IPs, usernames, and "production" labels. Record the review in tasks.md notes as completed (e.g., "Visual privacy audit: all images reviewed, no real user data detected [2026-09-XX]").
- [ ] Verify all server names follow `test-server-NN` or `demo-*` naming scheme
- [ ] Verify all cluster names are generic (`test-cluster`, `my-cluster`, not `production`, `customer-xyz`)
- [ ] Verify all usernames are generic (`test-user-01`, `admin-demo`, not real emails)
- [ ] Timestamps are not sensitive (FR-019); no action needed — render whatever the UI displays at capture time (see Dummy Data Rule Note)
- [ ] Verify no real player names, UUIDs, or mod names are visible

### Git & Commit

- [ ] All screenshots are committed to git (no large .gitignore exclusions of `docs/img/`)
- [ ] Commit message follows conventional-commit format (e.g., `docs: add six new dashboard screenshots (FR-016, FR-017)`)
- [ ] Commit message references FR-015, FR-016, FR-017 (or applicable FRs)
- [ ] Commit is signed (`git commit -s`)

### Audit Log (D-F)

- [ ] If any substitutions were made, `specs/done_012-docs-refresh-and-outreach/audit-log.md` includes one row per substitution:
  - [ ] Filename, route
  - [ ] Finding (original intended screenshot)
  - [ ] Evidence (path:line checked)
  - [ ] Resolution (substitute + reason)
  - [ ] Commit hash (link back to this audit-log change)

---

## Open Decisions

**Ruled decisions (2026-09-02)**, all closed:

| OD ID | Question | Ruling | Status |
|---|---|---|---|
| **OD-3** | Screenshot capture environment | **Use Option A (Playwright mock mode)** for speed, reproducibility, and CI integration | ✓ Ruled 2026-09-02 |
| **OD-3a** | Viewport dimensions | **Switch to 1920×1080 JPEG**; six existing files (currently 1568×773) are replaced at new size with filenames kept per FR-016 | ✓ Ruled 2026-09-02 |
| **OD-3b** | Capture method and path | **Playwright spec at `web/e2e/specs/screenshots.spec.ts`** run with `GAMEPLANE_E2E_TARGET=mock` and tag/grep filter; output to `docs/img/` | ✓ Ruled 2026-09-02 |
| **OD-3c** | Auto-recapture on release | **Tag-triggered workflow `.github/workflows/screenshot-refresh.yaml`** regenerates MSW fixtures and screenshots and opens a PR; credential is a fine-grained PAT repository secret `SCREENSHOT_BOT_PAT` (OD-13, ruled 2026-09-02); workflow also carries `workflow_dispatch` trigger (OD-15) | ✓ Ruled 2026-09-02 |
| **OD-3d** | Mock mode disclosure | **One sentence in README gallery intro** discloses mocking for all 11+ screenshots; **alt texts do NOT mention mocking**, focusing on purpose and UI elements only | ✓ Ruled 2026-09-02 |
| **OD-13** | PR credential for tag-triggered recapture | The credential for opening the screenshot-refresh PR is a fine-grained PAT repository secret `SCREENSHOT_BOT_PAT` (scoped to this repository with contents and pull-requests write permission, stored as a repository secret). | ✓ Ruled 2026-09-02 |
| **OD-15** | First capture path | **CI dispatch only** via workflow_dispatch of screenshot-refresh.yaml; agents never run Playwright locally (rule 8) | ✓ Ruled 2026-09-02 |

---

## Website Set (OD-16)

**Amendment (2026-10-07)**: Website screenshot sync extends the screenshot-refresh workflow to capture five dashboard screenshots for the gameplane-website repo. These are separate from the dashboard screenshots documented in the "Scope: Six Existing Screenshots" and "Scope: At Least Five New Screenshots" sections above.

### Scope: Website Screenshots (OD-16, FR-026)

The following five PNG files are captured for the gameplane-website repo at 2880×1800 PNG RGBA format (1440×900 viewport with deviceScaleFactor 2).

| # | Filename | Route | Component | Purpose | Alt Text | Format |
|---|---|---|---|---|---|---|
| 1 | `dashboard.png` | `/` | `DashboardPage` | Fleet health overview | Fleet health at a glance: running/stopped/failed server counts, cluster CPU/memory/storage, node status | 2880×1800 PNG |
| 2 | `servers.png` | `/servers` | `ServersPage` | Server list table | Every game server, one list: live status, CPU, memory, node placement | 2880×1800 PNG |
| 3 | `console.png` | `/servers/mc-survival (Console tab)` | `ServerDetail` Console tab | Live xterm.js streaming output | Live streaming console: game server stdout output | 2880×1800 PNG |
| 4 | `backups.png` | `/backups` | Backups page | Backup management and restore interface | Backups page: backup list with status, size, timestamps; restore button | 2880×1800 PNG |
| 5 | `modules.png` | `/modules` | Modules catalog | Game module registry browser | Modules catalog: grid of game modules, download counts | 2880×1800 PNG |

### Capture Method (OD-16, FR-026)

Website screenshots are captured by a dedicated Playwright spec `web/e2e/specs/websiteScreenshots.spec.ts` (separate from the dashboard spec `web/e2e/specs/screenshots.spec.ts`). The spec:
- Uses MSW mock-mode API responses (same mocked data as dashboard screenshots)
- Sets viewport to 1440×900 with deviceScaleFactor 2 (resulting in 2880×1800 PNG)
- Captures as PNG (not JPEG, for transparency and precision)
- Outputs to `web/website-screenshots/` (repo-local, gitignored)

### Website Workflow Integration (OD-16, FR-027, FR-028, FR-029)

The `.github/workflows/screenshot-refresh.yaml` workflow includes a step that:
1. Runs `web/e2e/specs/websiteScreenshots.spec.ts` to capture the five PNGs
2. Clones the gameplane-website repo
3. Copies the five PNGs to `src/assets/screenshots/{dashboard,servers,console,backups,modules}.png`
4. Opens or updates a pull request on gameplane-website main branch (never pushes directly to main)
5. Uses the `SCREENSHOT_BOT_PAT` fine-grained PAT secret (scoped to both GameplanePanel/Gameplane and GameplanePanel/website, Contents + Pull requests read/write)
6. If the token cannot write to gameplane-website, logs a `::warning::` and exits 0 (keeping the run green); if the token is empty, logs a `::notice::` and exits 0

### Dummy Data (FR-019, OD-16)

Website screenshots follow the same dummy-data rules as dashboard screenshots (test server names, generic cluster names, no real hostnames/IPs). The captured UI layout and components are identical; only the viewport and format differ.

---

## References

- **Specification**: `/home/user/Gameplane/specs/done_012-docs-refresh-and-outreach/spec.md` (FR-015 through FR-019, SC-009 through SC-011)
- **Research**: `/tmp/claude-0/-home-user-Gameplane/*/scratchpad/plan-012/R4-screens.md` (complete inventory, gap analysis, capture assessment)
- **Current README**: `/home/user/Gameplane/README.md` lines 18–27 (existing gallery)
- **Playwright Config**: `/home/user/Gameplane/web/playwright.config.ts` (mock/live mode setup, viewport defaults)
- **MSW Handlers**: `/home/user/Gameplane/web/src/test/handlers.ts` (API response stubs)
- **Test Factories**: `/home/user/Gameplane/web/src/test/factories.ts` (dummy data generation)
- **Route Definition**: `/home/user/Gameplane/web/src/router/tree.tsx` (route list, component mapping)
- **Component Routes**: `/home/user/Gameplane/web/src/routes/*.tsx` (Dashboard, ServerDetail, AdminSettings, etc.)
- **Login Privacy Verification**: `/home/user/Gameplane/web/src/routes/Login.tsx` lines 19–21 (CLAUDE.md rule 3 compliance)
- **CLAUDE.md Rule 3**: `/home/user/Gameplane/CLAUDE.md` (login privacy, pre-auth surface requirements)
- **Existing Screenshots**: `/home/user/Gameplane/docs/img/` (six JPEG files, currently 1568×773, to be replaced at 1920×1080 per OD-3a)
- **Constitution Principle VI**: `/home/user/Gameplane/.specify/memory/constitution.md` lines 249–271 (CI as system of record)
- **Style Reference**: `/home/user/Gameplane/specs/done_011-add-missing-module-specs/contracts/specs-md-structure.md` (document format and tone)

---

## Document Status

**Status**: Specification (Ruled — Ready for Implementation)  
**Version**: 1.2  
**Authored**: 2026-09-01  
**Updated**: 2026-09-02 (all maintainer rulings applied; no open items remain)  
**Applies To**: Implementation phase (screenshot capture, mock mode Playwright spec, README gallery refresh)  
**Maintainer Decisions Applied**: OD-3 (mock mode), OD-3a (1920×1080), OD-3b (Playwright spec at `web/e2e/specs/screenshots.spec.ts`), OD-3c (tag-triggered workflow), OD-3d (README disclosure, no mocking in alt text), OD-13 (PR credential is a fine-grained PAT repository secret `SCREENSHOT_BOT_PAT`), OD-15 (CI dispatch only, no local Playwright)
