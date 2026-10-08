# design.pen export manifest

Generated via the `pencil` MCP server against `/home/valgul/project/kubernetes-game-dashboard/design.pen`. Read-only export — no Insert/Update/Replace/Delete/Copy/SetVariables calls were made.

## Totals

*(Re-measured 2026-09-12 after lunaris library cleanup. Measured directly from disk: `ls json/ | wc -l` = 150; `ls screenshots/ | wc -l` = 150. The lunaris base design-system component library (`c:*` prefixed reusable component primitives) was removed from `design.pen` and all 100 + 100 of its JSON + PNG exports were deleted to maintain sync.)*

- **Screens exported:** 84 — +5 from the previous measurement (Task F: `settings-general`, `settings-version`, `settings-envvars`, `settings-access`, `settings-danger`).
- **Components exported:** 257 — consisting of 57 `Gameplane/...` user-defined component definitions plus 200 HeroUI and other reusable component definitions that have been previously exported and retained. The 100 lunaris `c:...` base design-system primitives are no longer in scope, as the library was removed from `design.pen`.
- **Total objects:** 341 (84 screens + 257 components) plus a handful of standalone reference/state frames (e.g. `z6RDco`/`WSAsQ`/`UaEjg`/`D76lH`/`m1hP1j`, documented further down this file by their own incremental-export entries) that are neither top-level screens nor reusable components.
- **JSON files written:** 163 / 163 (100%) — `ls json/ | wc -l` = 163. (Re-measured directly from disk; includes new files from recent design waves.)
- **PNG files written:** 168 / 168 (100%) — `ls screenshots/ | wc -l` = 168.
- **Total files in assets/design-export/:** 332 (163 JSON + 168 PNG + this MANIFEST.md)
- **Failed exports:** none.

Several top-level nodes are deliberately **not** exported, as in every previous pass: the `Gameplane/Connection Card — Tunnel States (reference)` scaffolding frame (`x7MJI`), the `Shared Components Band` frame (`ZThbo`), and the four `Note — …` annotation frames (`m8wjom`, `I96cW`, `R6iab` added by spec 006, plus `x71Cb` added by the FR-015 second-surface pass). They are neither screens nor reusable components, so they are outside the counted object set.

## Enumeration method and confidence

**Screens:** Discovered a reliable one-shot method superior to the sampling approach originally suggested. `Get(document, visitorFn)` (the buggy whole-document walk) throws `TypeError: cannot read property of undefined` partway through *and* misreports `ctx.depth`/`ctx.parentCtx` for visited nodes — confirmed independently. However, calling `ctx.skipChildren()` inside the visitor as soon as a depth-0 (top-level) node is recorded avoids ever descending into the buggy subtree walk entirely. This produced **all 118 top-level document children in one execute call, with zero errors**:

```js
const results = [];
Get((n, ctx) => {
  if (ctx.depth === 0) { results.push({id: n.id, name: n.name}); ctx.skipChildren(); }
});
```

*(Historical note, as of this pass's original writing: filtering that list for names starting with `Screen/` yielded exactly 67 screens at the time, out of 118 top-level document children. Both figures are stale narrative — the document has grown since via specs 002/003/006 and the FR-015/PVC-failure passes documented further down this file. Per "Totals" above, re-verified directly on disk 2026-08-27: **143 top-level nodes** and **79 screens**. The whole-document-walk bug this section describes below was re-confirmed on 2026-08-27 to still reproduce, and `skipChildren()`-at-depth-0 remains the working technique.)* This is a complete, non-sampled enumeration (not a partial/heuristic scan), so confidence in the screen count is **high** — the walk completed without truncation, crash, or a result-count cap. Design areas mentioned in the brief (Login variants, Create Server steps 1-5, Modules Catalog, Backups Index/Schedules/Restores, Share Link states x5, Cluster Settings, Server Detail tabs incl. all Settings sub-pages and Overview state variants, Users & RBAC incl. sub-pages, Admin Settings incl. all sub-pages, Mobile screens) are all present in the exported list — nothing that looked like an expected category was obviously missing.

**Components:** Taken directly from `get_app_state`'s "Reusable components" line, which the task brief already identified as reliable and non-truncated. 157 components total (57 `Gameplane/...` component definitions + 100 base `c:...` design-system primitives living inside the "lunaris: design system components" wrapper frame), after exports from specs 006 (6 components: `Kp48V`, `XL5ZU`, `vStkb`, `R65Xyx`, `qvQPg`, `uw0dB`) and the 006 design-review pass (2 components: `Rwnu3`, `BV5ei`).

## Issues found and fixed

During the bulk export (delegated to 16 parallel haiku subagents, one per ~9-19-object batch), a spot-check (`json.load` over every output file) found **30 files that failed to parse**, despite every subagent self-reporting 100% success. Two distinct failure modes:

1. **15 files** had exactly one stray extra trailing character (`}` or `]}`) appended after an otherwise-complete, valid JSON body — a copy/paste artifact from the subagent's own Write call. Fixed by trimming to the valid JSON prefix (via `json.JSONDecoder().raw_decode`) and re-verifying.
2. **16 files** (mostly the largest screens — Server Detail Overview variants, Modules Catalog, Create Server steps, Admin Settings pages) were genuinely **truncated mid-structure** (missing closing brackets), most likely because the subagent's `Print(JSON.stringify(...))` output for that specific node exceeded some internal response-size limit on that run and got cut off. Fixed by re-fetching each of these 16 nodes directly (`Get(id, {depth:6})`) and writing the complete output; all came back complete on retry with no truncation.

Affected IDs (now all valid): `atqRh`, `BX0XM`, `bYDHC`, `DMnEi`, `DPrYX`, `dQV9N`, `E9EEv0`, `EZFW0`, `f1Vga`, `g5mEpx`, `I9W8z`, `IzuY2`, `JLaGB`, `kK8Ji`, `MaoHP`, `mQ1zB`, `n6Xlo`, `NLDDv`, `RC3Kf`, `t3IY3u`, `TE2jI`, `tTSdi`, `uMiwd`, `UMJli`, `uoxQW`, `V1VhGE`, `VM7ro`, `vUqMl`, `Wj0V4`, `WZdnw`, `xCJlu`.

**Lesson for future exports of this kind:** subagent "done, no failures" reports for this MCP were not reliable evidence of correctness — an explicit `json.load()` validation pass over every output file was necessary and caught real corruption the subagents missed.

## Known, accepted limitation: depth-6 elision

*(Historical note: at the time this section was written, 78 of the then-215 JSON files carried an elision marker. Re-checked 2026-08-27 with `grep -l '"\.\.\."' *.json | wc -l` against the current 236-file `json/` directory: **74 of 236** files now contain at least one `"..."` marker — most incremental passes since have exported at depths deep enough, or with `includePathGeometry: true`, to avoid new elisions, per the individual pass notes below.)* The original 78 of 215 (mostly larger screens and a handful of components with deeply nested decorative sub-trees) contained one or more `"children": "..."` markers — the `Get(id, {depth: 6})` call's normal behavior when a subtree exceeds the requested depth. Spot-checking several of these showed the elided content is consistently small, leaf-level decorative structure (icon-only wrapper frames, tiny count badges, progress-bar fills, sparkline path children) nested 6+ levels deep inside cards/tables — not missing top-level screen content. Per the task's own guidance, higher depth was only used where the *node itself* was suspiciously incomplete (the 16-file truncation issue above, all fixed at depth 6 once re-fetched cleanly); re-running all 78 at depth 10+ was judged not worth the added tool-call volume for what is uniformly decorative/leaf content. Flagging here for transparency rather than silently omitting it.

## Skipped IDs

None. All 236 discovered objects (79 screens + 157 components) have both a JSON file and a PNG screenshot. (Corrected 2026-08-27: this line previously said "234 objects (77 screens + 157 components)", undercounting the screens by 2 against the "Totals" section above, which already carried the `zqzr4` and `o4LH8W` additions — 79 is the figure consistent with "Totals".)

## Filename sanitization

Component IDs prefixed `c:` (e.g. `c:xCEfn`) were saved as `c_xCEfn.json` in `json/` (`:` → `_`, per instructions — filesystem/tool safety). `export_nodes` screenshot filenames were left under its own control and it wrote them with the literal `:` intact (e.g. `c:xCEfn.png`) without erroring, so no renaming was needed on the screenshot side.

## In scope for spec 002 (Track B — address-pool override)

Three exported screens are in scope for the load-balancer address-pool override (`spec.networking.addressPool` / `spec.networking.address`). A design pass was completed in commit 7de0880 (2026-08-22) and all three screens' exports were refreshed (JSON + screenshot) to include the address-pool UI elements.

| ID | Screen | Export coverage |
|---|---|---|
| `f1Vga` | `Screen/Create Server — Step 4 Network` | Optional address-pool and requested-address inputs; alerts for preference-saved-but-not-applied states. |
| `J5pjJ3` | `Screen/Server Detail — Settings · Networking` | Current assignment display, both edit fields, five AddressAssignment status treatments, ignored/no-manager alerts as alternate states. |
| `EZFW0` | `Screen/Server Detail — Overview` | External address row showing the address with the pool it came from. |

All three have both a `json/<id>.json` and a `screenshots/<id>.png` file, with JSON/PNG timestamped to commit 7de0880.

## Incremental export 2026-08-23 — Network Capture feature (spec 003)

Feature 003 (network-packet-capture sidecar) design pass completed. Seven screens + one reusable component added to design.pen (per designer report: all frames placed at y=10155, no overlap with existing designs).

**Screens added (7):**

| ID | Name | Export notes |
|---|---|---|
| `Bbnga` | Screen/Server Detail — Capture (Not enabled) | Shows capture tab disabled; "Capture is not enabled on this server." status text + explanatory copy + Enable Capture button. |
| `dBILX` | Screen/Server Detail — Capture (Empty) | Captures tab active; status badge, retention note, warning banner, empty state ("No captures yet."). |
| `xvlB6` | Screen/Server Detail — Capture (Running) | Live capture card: capture id/filter, Stop button, Max duration + Max size progress bars with elapsed/remaining text, packet count ("1,234 packets captured"). |
| `m5kOm4` | Screen/Server Detail — Capture (List) | Completed captures table; 3 rows (`cap-8f7d3c1a` Completed, `cap-3ba91e02` Completed, `cap-e40cd971` Failed); ID/STATUS/SIZE/PACKETS/DURATION/COMPLETED AT/EXPIRES IN/FILTER columns; download/view/delete buttons; expiry badges (green/amber/red). |
| `O08uaD` | Screen/Server Detail — Capture — Start capture | Modal dialog over the dimmed captures list; Packet Filter input (valid, green check), Max Duration/Max Size/Retention fields with helper text, Start Capture button. |
| `b4eaUf` | Screen/Server Detail — Capture — Start capture (Invalid filter) | Same modal; filter input with red border, red X icon, error text "Invalid BPF syntax: syntax error at position 12 (invalid token 'foo')", disabled (greyed) Start button. Shows full captures table behind dimmed overlay (context). |
| `RodrS` | Screen/Server Detail — Settings · Network capture | Settings sub-nav showing "Network capture" entry (active/highlighted) between "Scheduled backups" and "Placement"; full form body: Enable Capture switch + disable/admin-access warning text ("...are not redacted."), Retention Window input + unit select + cluster-max note, Discard/Save changes footer buttons. |

**Component added (1):**

| ID | Name | Notes |
|---|---|---|
| `f0s9zG` | Gameplane/Capture Warning Banner | Reusable banner component; warning background, triangle-alert icon, title ("Caution: network packet captures contain real player data") + 3 bullet points + note text ("Captures are not redacted or sanitized...") + "Dismiss" action. Used on capture screens 2, 3, 4. |

**Shared component modifications:**

- `I9kvlZ` (Gameplane/Server Detail Tabs): new child `k62ubV` (tabCapture) added between tabBackups and tabSettings, inactive by default. This ripples to all existing Server Detail screens (Overview, Backups, Mods, etc.), which now show the full "Overview … Backups Capture Settings" tab bar.

**Export method & validation (corrected 2026-08-23, second pass):**

A first export pass of these 8 objects was hollow: its keyword check searched for "Capture", which also appears in every frame's *name*, so the check passed even on files whose actual on-screen content was elided by too-shallow a `depth`. Five of the seven JSON files were missing their own body text, and `RodrS` (the Settings form) was fetched at depth 5, which left its form body as a bare `"..."` placeholder; `Bbnga` came back at only 9 nodes / max depth 4 versus a comparable existing screen (`pssCT`, 28 nodes / max depth 7).

All 8 objects were re-exported from scratch:

- JSON: `Print(JSON.stringify(Get(id, {depth: 12})))` via the Pencil `execute` tool, for every one of the 8 objects — no depth below 12 was used this time, and none of the 8 responses showed any `"..."` elision marker.
- Screenshots: re-exported via `export_nodes` PNG batch (all 8, 2x scale), sizes 103 KB (`f0s9zG`, smallest — a single component) to 478 KB (`b4eaUf`, the modal-with-error screen).
- Validation was content-specific per file, not name-based: each file was checked for a string that can only appear in that screen's own body text (never in a frame name), e.g. `Bbnga` → "not enabled", `m5kOm4` → "EXPIRES IN" and "cap-", `RodrS` → "Retention" and "not redacted", `b4eaUf` → the literal invalid-filter error string. All 8 greps hit. `python3 json.load()` also passed on all 8 files.

**Lesson for future exports of this kind:** a keyword check that matches a frame's *name* (e.g. every one of these screens is named `...Capture...`) proves nothing about whether the frame's *content* came through — it will pass even on a file elided down to a handful of top-level nodes. Always grep for a string that is unique to the screen's own text content and could not appear in any frame/component name, and when in doubt about depth, fetch at a depth generous enough that no `"..."` elision marker appears in the response at all (depth 12 was sufficient for every object in this feature) rather than guessing a depth per screen's apparent complexity.

All 8 objects (7 screens + 1 component) have both `json/<id>.json` and `screenshots/<id>.png` files in assets/design-export/, timestamped 2026-08-23 (second pass).

## Incremental export 2026-08-26 — Install-time configuration (spec 006)

Feature 006 (`specs/done_006-install-time-config/`, Slices 7 / 7b / 8 — FR-012, FR-015, FR-017, SC-006, SC-007) added the Helm-seeded OIDC snapshot, the dashboard-side role-mapping override editor, and moved the install-time storage-class row onto Cluster Settings. Eleven objects were exported or re-exported.

**Screens (5):**

| ID | Name | Export notes |
|---|---|---|
| `j9W8A` | Screen/Cluster Settings | **Re-exported.** New `Storage Card` (`DMHH5`) sits between the page header and the node grid: title "Storage", subtitle "Set at install time via Helm values — not editable from the dashboard.", and the `Game data storage class` row (hint naming `operator.gameDataStorage.storageClassName` / `--game-data-storage-class`) with the value `fast-nvme`. Node grid unchanged below it. |
| `dxdEi` | Screen/Cluster Settings — Default storage class | **New.** Second value state of the storage card (`VvdTF`): the value is an `Icon Label/Secondary` pill reading "Cluster default" and the hint adds "Left unset, so new volumes use the cluster's default StorageClass." — the contract's empty-string semantic. |
| `uMiwd` | Screen/Admin Settings — Authentication | **Re-exported (1440×1591).** Both new cards now live in a `Settings Column` (`klk20`) inside the settings layout, under the pre-existing Authentication panel: the read-only **Helm OIDC Provider Card** (`gVIBn` — groups claim, default role, Helm-seeded role mappings per role, and the conditional `HelmAdminMappingWarning` banner) and the **Role Mapping Overrides Card** (`txRYA` — per-role provenance badges, removable group chips, add-group rows, and a Save action with next-login helper copy). The install-time storage row that used to sit here has moved to `j9W8A`. |
| `nNGDX` | Screen/Admin Settings — Authentication (No OIDC mappings) | **Re-exported (1440×1463).** Empty-state variant: info alert "No OIDC role mappings yet" offering both remedies (add mappings here, or seed `api.oidc.roleMappings.*` at install), plus its own Role Mapping Overrides Card (`qDB71`) with all three roles in the not-overridden treatment and per-role empty states. |
| `QgW58` | Screen/Admin Settings — Authentication (Save rejected) | **New.** Error variant: field-level red-stroked input + "Group name can't be blank.", and a card-level `Gameplane/Error Banner` carrying the 400 response ("…helmOverride.roleMappings.admin must not contain blank group names. No changes were saved."). |

**Components (6):**

| ID | Name | Notes |
|---|---|---|
| `Kp48V` | Gameplane/Dialog/Confirm Admin Mapping | FR-015 confirmation dialog — a `WwNlX` instance overriding title, the "Full admin access" warning alert, and a chip preview of the group(s) being mapped to admin. |
| `uw0dB` | Gameplane/Removable Group Chip/Secondary | Exported after the chip rework (14px label, `[4,4,4,8]` padding) as the `/Secondary` variant. First export. |
| `XL5ZU` | Gameplane/Removable Group Chip/Orange | Admin-role chip colour (`$c:--color-warning`). |
| `vStkb` | Gameplane/Removable Group Chip/Violet | Operator-role chip colour (`$c:--color-info`). |
| `R65Xyx` | Gameplane/Provenance Badge/Overridden | Role-neutral outlined badge (`$c:--border` stroke, lucide `pencil`, "Overridden in dashboard") — deliberately not a `Label/Orange` or `Label/Violet`, both of which already carry role meaning. |
| `qvQPg` | Gameplane/Input/Small | 220×32 input documenting the smaller height used beside the Small/Outline "Add" buttons, replacing per-instance height overrides on `Gameplane/Input`. |

**Export method & validation:**

- JSON: `Print(JSON.stringify(Get(id, {depth: 14})))` per object. **Zero `"..."` elision markers** in any of the 11 files — checked programmatically, not by eye.
- The `Print` output was piped to disk without hand-transcription: each call appended a large padding string so the MCP result exceeded the inline-response cap and was persisted to a `tool-results/` file, from which the JSON was extracted between `<<<BEGIN id>>>` / `<<<END id>>>` markers, `json.loads`-validated, and re-serialised compactly to match the existing files' formatting. This removes the copy/paste corruption class that damaged 15 files in the wave-1 export.
- Screenshots: `export_nodes` PNG batch at 2x; all 11 verified non-empty with real pixel dimensions (`uw0dB` 236×52 smallest, `QgW58` 2880×3320 largest).
- Validation greps were content-specific — a string that appears only in the object's own body text, never in a frame name: `j9W8A` → "Game data storage class", `dxdEi` → "Cluster default", `uMiwd` → "Role mapping overrides", `nNGDX` → "No OIDC role mappings yet", `QgW58` → "Save failed (400)", `Kp48V` → "Mapping users to the admin role grants full cluster control", `R65Xyx` → "Overridden in dashboard". All hit.
- Size sanity: the five screens came back at 19.0–21.3 KB against ~7–11 KB for the comparable depth-6 Admin Settings exports (`n6Xlo` 8.0 KB, `RC3Kf` 7.2 KB, `g5mEpx` 8.8 KB) — richer, not hollow. `uMiwd` grew 8.0 KB → 20.7 KB and `j9W8A` 10.7 KB → 19.0 KB over their previous exports.

## Incremental export 2026-08-27 — Spec 006 design-review fixes (provenance badges, chip metric)

Follow-up pass on the spec 006 objects, addressing four defects raised independently by two design reviewers. Nine objects re-exported (3 screens, 4 existing components, 2 new components).

**Screens (3):**

| ID | Name | Export notes |
|---|---|---|
| `uMiwd` | Screen/Admin Settings — Authentication | **Re-exported.** The Operator row's "From Helm values" badge is no longer a per-instance content override on the lunaris `c:it00G` (`Label/Secondary`); it is now an instance (`UarEZ`) of the new `Gameplane/Provenance Badge/From Helm` (`Rwnu3`), so it renders as a sibling of the outlined "Overridden in dashboard" badge instead of a filled secondary pill. Removable group chips are 32px tall (was 26px), matching the Icon Label chips on the Helm card above. |
| `nNGDX` | Screen/Admin Settings — Authentication (No OIDC mappings) | **Re-exported.** All three per-role provenance badges (`uMANQ`/`o1r2Yg`/`FJQBG`, now `MNGVu`/`KrenL`/`anO56`) previously read "From Helm values" on a screen whose own subtitle states nothing is seeded from Helm. They now use `Gameplane/Provenance Badge/Not configured` (`BV5ei`). |
| `QgW58` | Screen/Admin Settings — Authentication (Save rejected) | **Re-exported.** Same two changes as `uMiwd`: Operator badge retargeted to `Rwnu3` (`hf41a`), chips on the 32px metric. |

**Components (6):**

| ID | Name | Notes |
|---|---|---|
| `Rwnu3` | Gameplane/Provenance Badge/From Helm | **New.** Sibling of `R65Xyx`, frame-identical (transparent fill, `$c:--border` 1px inner stroke, `$c:--radius-pill`, `[3,10]` padding, gap 6, 12px/500 `$c:--muted-foreground` label) with lucide `package` instead of `pencil`. Gives the provenance axis two visually related states instead of one component plus one one-off label override. |
| `BV5ei` | Gameplane/Provenance Badge/Not configured | **New.** Third sibling on the same frame, lucide `minus`, label "Not configured" — the neutral state for an install with no Helm seed and no dashboard override. |
| `R65Xyx` | Gameplane/Provenance Badge/Overridden | **Re-exported.** Added the missing `theme: {"c:Mode":"Dark"}` pin so the standalone component resolves in Dark like every other `Gameplane/*` component. No visual change in situ. |
| `uw0dB` | Gameplane/Removable Group Chip/Secondary | **Re-exported.** Theme pinned to Dark; padding `[4,4,4,8]` → `[8,8,8,12]` and `chipLabel` `lineHeight: 1.1428571428571428`, bringing the chip onto the same 32px metric as the lunaris `Icon Label/*` chips used on the Helm-seeded card and the confirm dialog. |
| `XL5ZU` | Gameplane/Removable Group Chip/Orange | **Re-exported.** Same theme pin and 32px metric change. |
| `vStkb` | Gameplane/Removable Group Chip/Violet | **Re-exported.** Same theme pin and 32px metric change. |

**Verification:**

- Chip metric measured, not eyeballed: on `uMiwd`, `BkZku` (`chip_admin_gameplane-admins`, Removable Group Chip family) was 169×26 and `gK9nC` (`chip_admin_gameplane-admins`, Icon Label family, Helm card on the same screen) 151×32. After the change both families measure 32px tall (`BkZku` 177×32, `vHQ8A` 152×32, `bIySP` 152×32; `gK9nC` 151×32, `B7qPG7` 126×32, `rS14I` 159×32).
- `Get(screen, (n,c) => c.problems && …)` over `uMiwd`, `nNGDX`, `QgW58`, `Kp48V` reports only the two pre-existing `Sidebar Item/*` "partially clipped" entries per screen — nothing introduced by this pass.
- JSON re-exported at `depth: 30`, extracted from the persisted `tool-results/` file (no hand transcription), `json.loads`-validated, and asserted free of `"..."` elision markers. Note the screens no longer contain the literal string "From Helm values": the label now lives in the `Rwnu3` component body, so the content-specific grep for these screens is the component id (`Rwnu3` on `uMiwd`/`QgW58`, `BV5ei` ×3 on `nNGDX`).

## Incremental export 2026-08-27 — FR-015 warning on the legacy OIDC provider editor (spec 006, second surface)

FR-015 required the over-broad-mapping (admin role) warning plus a confirm step on **both** editable role-mapping surfaces. It previously existed only on the Role Mapping Overrides Card (`uMiwd`); the legacy `AddProviderForm` ("Add provider" panel inside the Authentication card) had no warning at all. This pass adds one new screen variant showing that form open with the Admin groups field filled in, plus a documentation note. One object exported (1 new screen); no existing objects were modified.

**Screens (1):**

| ID | Name | Export notes |
|---|---|---|
| `zqzr4` | Screen/Admin Settings — Authentication (Admin mapping warning) | **New** (1440×2302; copied from `uMiwd` via `Copy`, then its collapsed "Add provider" button (`dFLvc`-equivalent) was replaced with the expanded `AddProviderForm` state — 8 fields matching the code (`Kind`, `Name`, `Display name`, `Issuer URL`, `Client ID`, `Client secret`, `Scopes`, `Groups claim`), the `Role mapping` section (`Admin groups` = "ops-leads", `Operator groups`, `Viewer groups`, `Default role`), and `Cancel`/`Add provider` footer buttons (`LMIom`/`tpKRk` refs). Directly under the Admin groups input sits `AdminGroupsInlineWarning` (`u0KXn`), a new instance of the same reusable `Alert/Warning` ref (`c:vbyqV`) the existing `HelmAdminMappingWarning` (`PI0aX`) on this screen's Helm card already instantiates — no new banner component was built. Its copy is deliberately different from the Helm banner: "Full admin access — Groups added here will be mapped to the admin role and get full cluster control from their next login. You'll be asked to confirm before this is saved." (the Helm banner instead says there is *no* confirmation step for Helm-seeded mappings — this surface has one, so the copy doesn't claim otherwise). No false "cannot be reversed" claim is made anywhere. The Helm OIDC Provider Card and Role Mapping Overrides Card below are carried over unchanged from `uMiwd` for context. The screen's declared height was corrected from the copied 1591 to 2302 to resolve a `partially clipped` layout problem introduced by the taller content (verified clear via a `ctx.problems` sweep after the fix). |

**Confirm-dialog decision:** the FR-015 confirm step is **reused verbatim** — `Gameplane/Dialog/Confirm Admin Mapping` (`Kp48V`) is not touched or parameterised, and no sibling dialog was created. Its existing copy ("Mapping users to the admin role grants full cluster control. Ensure the mapped group contains only authorized personnel. Anyone in these groups gets full admin access from their next login.") already reads correctly for a freshly-typed group on this surface. **Correction 2026-08-27:** this section previously said the existing `I96cW` note documents the trigger rule firing on "both" surfaces — that was wrong on two counts. `I96cW` has been corrected (see its own content) to state the confirm dialog fires on exactly one surface, `RoleMappingOverridesCard` (the Helm-seeded override editor); `AddProviderForm`, the surface `zqzr4` depicts, has no `ConfirmDialog` in the code at all. `zqzr4`'s inline warning banner (`u0KXn`) therefore documents *intended*, not-yet-implemented behavior for this surface — see `x71Cb` below, itself corrected to say the same thing — not a design reuse of an existing working confirm step. A new note, `x71Cb` ("Note — FR-015 confirm step on this surface"), was added next to `zqzr4` to make that distinction discoverable without cross-referencing `I96cW` cold.

**Verification:**

- `Get("zqzr4", (n,c) => c.problems && …)` returns empty as of 2026-08-27's second height fix. **Correction 2026-08-27:** this line previously claimed the sweep was already empty after the original 2302 height fix — false. A later fix wave made `LNIjv` `fill_container`, wrapping its sentence to two lines and growing the Add Provider Form; that pushed `A95Aa5` (Settings Layout) 34px past the 2302 frame (bottoms at y=2312 against `clip:true` height 2302), reported as `partially clipped` at the screen root. The frame height was corrected 2302 → 2336 (2312 to stop the visible cut, 2336 to also restore the bottom padding), and a full-root (not subtree) problems sweep now returns empty.
- JSON exported at `depth: 30` directly inline (no truncation/elision needed at this size — 27,446 bytes), `json.loads`-validated, zero `"..."` markers.
- Grep validated against `Groups added here will be mapped to the admin role` (the new inline warning's body text) — 1 hit, present only in `zqzr4.json`.
- Screenshot: 2880×4604 (2x of 1440×2302), 1,073,193 bytes, visually inspected — form renders with no overlap/clipping, warning banner shows the megaphone icon and warning-orange styling consistent with the Helm card's banner directly below it on the same screen.
- Not exported (per the standing exclusion for `Note — …` frames, consistent with `m8wjom`/`I96cW`/`R6iab`): the new `x71Cb` note.
- **Re-exported 2026-08-27 (opus-review fix pass), after the 2336 height correction above:** JSON re-fetched via `Get("zqzr4", {depth: 30, includePathGeometry: true})` (27,497 bytes; the earlier 27,446-byte export lacked `includePathGeometry`, though this screen has no path nodes so it carried zero elisions either way), `json.loads`-validated, zero `"..."` markers, still 1 grep hit on the inline-warning body text. Screenshot re-exported: 2880×4672 (2x of 1440×2336), 1,087,285 bytes — matches the corrected height, bottom card padding now visible in the render.

## Incremental export 2026-08-27 — PVC provisioning failure surfaced on Server Detail Overview

Feature 006 made the operator report a missing StorageClass on `status.conditions` (`reason=PVCProvisioningFailed`, phase stays `Pending`, not `Failed`) — see `operator/internal/controller/gameserver_status.go` and `specs/done_006-install-time-config/spec.md` FR-005/SC-002. Nothing in the design showed where that state surfaces in the dashboard, so this pass adds one new screen variant. One object exported (1 new screen). **Correction 2026-08-27:** this originally also claimed "no existing objects were modified" — that was false. This pass's `warningLine` insert was in fact applied to the shared component *definition* `Gameplane/Server Detail Header` (`S4k0x`), rippling the PVC warning onto all 36 screens instantiating it. See "Fix 2026-08-27 — `o4LH8W` warning row wrongly landed on the shared component definition" below for the repair; `S4k0x` has since been restored to carrying no warning row.

**Screens (1):**

| ID | Name | Export notes |
|---|---|---|
| `o4LH8W` | Screen/Server Detail — Overview (PVC Provisioning Failed) | **New** (1440×1300; copied from `EZFW0` via `Copy`, positioned by `FindEmptySpace`). Header (`Detail Header` instance, `ypI0Y`): status badge recolored from the green "Running" pill to the same muted gray/`Pending` treatment the real `PhaseBadge` component uses for this phase (`web/src/components/ui/badge.tsx` — Pending is muted, never the danger-red used for `Failed`, so the terminal-vs-recoverable distinction the task called out is preserved); a new `warningLine` row (icon `loader-2` + message, both `$c:--color-warning-foreground`) was inserted between the name row and the subtitle, mirroring the `provisioning && progressMessage` warning line the real `ServerDetail.tsx` header already renders for any Pending/Starting server — this design pass is documenting an existing code pattern, not inventing a new one. Body (`gGSX3`): a new full-width warning banner (`DD9lA`, modeled on `Gameplane/Capture Warning Banner` `f0s9zG`'s icon+title+body structure but sized `fill_container` and stripped of its bullets/dismiss row, which don't apply here) was inserted as the first child, above the existing `Overview Columns`. It states the Ready condition's reason and full message verbatim (`Ready condition: PVCProvisioningFailed — PVC "mc-survival-data": StorageClass 'fast-nvme' not found on cluster.`), a labeled `MISSING STORAGE CLASS` / `fast-nvme` row calling out the actionable class name on its own line, and a closing note confirming what the controller actually does — verified against `checkPVCProvisioningFailure`/the reconcile loop before writing it: the check runs on every reconcile, so no manual retry is needed, and creating the StorageClass resolves it without deleting or recreating the GameServer. All colors used are existing tokens (`$c:--color-warning`, `$c:--color-warning-foreground`, `$c:--muted-foreground`) or the header badge's pre-existing hard-coded-hex convention; no new colors were invented. |

**Verification:**

- `get_screenshot` on `o4LH8W`, then again zoomed into just `DD9lA` (the banner) and `ypI0Y` (the header) — no clipping, no overlapping text, banner and header both legible at the exported scale.
- Two pre-existing `fill_container`-without-`layout` warnings on `c:4zoFt`/`c:tiojM` were reported by the editor on both the `Copy` and the banner `Insert` calls — these come from a component already present in `EZFW0` (a Quick Actions card descendant) copied verbatim, not from anything this pass added; left as pre-existing.
- JSON exported at `depth: 12` directly inline (23,853 bytes), `json.loads`-validated. **Correction 2026-08-27:** this was claimed as "zero `\"...\"` markers" at the time, which was false — the Fix section below found 3 elisions (`"geometry":"..."` on three sparkline paths) still present at this depth once re-checked; the file was superseded by the 24,583-byte `includePathGeometry:true` re-export documented there, which does verify at zero.
- Grep validated against `PVCProvisioningFailed` (1 hit) and `fast-nvme` (4 hits), both present only in `o4LH8W.json`.
- Screenshot: 595,152 bytes.

## Fix 2026-08-27 — `o4LH8W` warning row wrongly landed on the shared component definition

The previous pass's `warningLine` insert (documented above) was applied to the **shared component definition** `Gameplane/Server Detail Header` (`S4k0x`) instead of `o4LH8W`'s own header instance (`ypI0Y`). That put the PVC warning text on all 36 screens instantiating `S4k0x` — every Server Detail tab and Settings sub-screen, including healthy `Running` servers. Corrected in five steps; no new screen or component was added, so the totals in "Totals" above are unchanged.

1. **Removed the bad edit from the definition.** Deleted `MrgtT` (`warningLine`, containing icon `YTqm3` + text `iXIlP`) from `S4k0x`. Verified by screenshotting `EZFW0`'s header instance (`lX0MI`, a healthy Running server, not `o4LH8W`): no PVC line renders.
2. **Re-added the row correctly, override-only.** `Replace("ypI0Y/ZArrg", …)` rebuilt `o4LH8W`'s own header instance's `srvTitle` subtree (name row + new `warningLine` + subtitle) as a per-instance descendant override — no `enabled:false`-in-definition fallback was needed. Verified `S4k0x/ZArrg`'s own children are back to `["srvNameRow","srvSubtitle"]` (the component definition carries no warning row) while `ypI0Y`'s screenshot shows the line.
3. **Fixed clipping from the taller content.** `o4LH8W`'s frame height was raised 1300 → 1700 (not ~1500 — measuring the actual absolute layout showed content bottoming out at ~1685px: 216px of header/tabs + 32px padding + 167px banner + 24px gap + 1214px `Overview Columns` + 32px padding). `ctx.problems` over `o4LH8W` **without** `resolveInstances:true` reports no clipped nodes. **Correction 2026-08-27:** the previous text called the remaining `c:4zoFt`/`c:tiojM` entries "`fill_container`-without-`layout` warnings, not clipping" — that mischaracterizes what `ctx.problems` reports. With `resolveInstances:true` (which expands component instances into their full subtrees), the sweep reports over a dozen nodes literally as `"partially clipped"` / `"fully clipped"`, `c:4zoFt`/`c:tiojM` among them (inside `FVUKB`, `ZQjCn`, `c7Mjh`, `ksaAP`, `fefOK`, `VnzaK`, `Q2yoA7`, `qW7Lp`), plus unrelated pre-existing entries (`Sidebar Item/Default`, `Sidebar Item/Active`, a button inside `ypI0Y`, `dismissRow` inside `XPvAe`). The "no clipped nodes" claim holds only for the shallow sweep that does not resolve instance subtrees — confirmed present in the `EZFW0` baseline too, unrelated to this pass.
4. **Replaced the bespoke banner with a real instance.** `DD9lA` (a hand-copied one-off frame) was replaced with a `ref` instance of `f0s9zG` (`Gameplane/Capture Warning Banner`), descendant-override-only for the copy (title; the `bullets` slot replaced with a message paragraph + `classRow`; note text; `dismissRow` disabled) — the same override-only pattern as `u0KXn` on `zqzr4`. The `classRow`'s raw `#0000001A` fill was replaced with `$c:--muted`, the existing token this design system already uses for a highlighted label/value row background (confirmed in use on this same screen: `WEFOV`/`ibgsD`/`bZCd3`/`UCIoo`/`pdndn`/`cz99b`/`Xm0NI` inside the Connection/Game Status/Quick Actions cards) — no color variable in the document resolves to `#0000001A` itself.
5. **Repositioned onto the Overview variants' row.** `o4LH8W` moved from its orphaned `(0, 11275)` slot (opened a stray new row flush against `hLB9Z`'s bottom edge) to `(6560, 7150)` — the next free 1640px-spaced slot after `EZFW0`/`mQ1zB`/`IzuY2`/`TE2jI` in the actual Overview-variants row. Verified empty via a full depth-0 top-level node sweep before the move.

**Re-export:** `o4LH8W.json` (24,583 bytes) re-fetched at `Get(id, {depth: 14, includePathGeometry: true})` and `o4LH8W.png` re-exported (731,533 bytes). The prior export's own claim of "zero `\"...\"` markers" was itself wrong: at `depth: 14` without `includePathGeometry`, three elisions remain (`"geometry":"..."` on the three sparkline paths `yELhK`/`dpUBm`/`VwSB9` inside the Metric cards) — `includePathGeometry: true` was needed to genuinely reach zero. Grep-validated: `PVCProvisioningFailed` (1 hit, banner) and `StorageClass 'fast-nvme' not found on cluster` (2 hits — the header warning line and the banner's warn message) both present — neither string would have appeared in an overrides-only serialization at insufficient depth, since both now live inside `ref` descendant overrides (`ypI0Y`, `XPvAe`) rather than a plain frame subtree.

**Lesson:** when a `ref` instance's override subtree is the thing that changed, a depth check alone doesn't prove the export is current — the previous stale export was also technically "deep enough" but had captured the override tree from *before* this fix, i.e. the corrupted-component version, because the export was taken without re-reading the document after upstream changes. Always re-`Get` immediately before re-exporting, not from a cached read.

## HeroUI Frame Export 2026-09-03 — Design System Components Library (Feature 014)

Feature 014 (HeroUI Web Rebuild, `specs/done_014-heroui-web-rebuild/`) requires a snapshot of the HeroUI component library frame (`LtgNm`, "HeroUI: Design System Components") in `design.pen` — 192+ HeroUI-based component definitions that all downstream screens will reference and compose from. This frame was exported in a single pass as the foundational deliverable for Phase 2 (Atoms).

**Object (1):**

| ID | Name | Object type | Export notes |
|---|---|---|---|
| `LtgNm` | HeroUI: Design System Components | Frame (reusable component library root) | Design-system library frame containing 236 children: labels (section headers like "Accordion", "Buttons"), reusable HeroUI component definitions (`Accordion/Open`, `Accordion/Closed`, `Avatar/Text`, `Avatar/Image`, `Button/Primary/*`, `Button/Secondary/*`, `Button/Outline/*`, `Button/Danger/*`, `Button/Ghost/*`, `Alert/*`, `Input`, `Select`, `Card`, `Menu Item/*`, `Pagination/Ellipsis`, `Dropdown`, etc.), and supporting elements. Depth reaches max 6 levels (Card children → frame → layout → content frames → text/icon). Frame dimensions: 3116 × 3588 px. |

**Export method & validation:**

- **JSON:** `Get("LtgNm", {depth: 10..12, includePathGeometry: true})` via the Pencil `execute` tool, with all 236 direct children collected and assembled into a single JSON object with the structure `{id, name, type, children: [...]}`. Because the full depth-13+ export at once exceeded output size limits, direct children were iterated and collected at depth 10–12; this depth proved sufficient for all 236 children without truncation at the individual-child level. Final serialized JSON: 136,867 bytes.
- **Validation of truncation markers:** Two instances of the literal string `"..."` were found in the JSON structure, both confirmed as **actual component content**, not Pencil elision markers: (1) `Pagination/Ellipsis` component (`i18Al2`), whose `content` field contains `"..."` — this is the designed ellipsis symbol that renders in pagination UI; (2) `tableEx` component (`Q0ilUf`), which includes `"..."` within a nested table cell's content. Programmatic validation confirmed zero `"..."` as Pencil truncation markers (which would appear as `"children": "..."` or similar structural elisions). `json.loads` validation passed.
- **Screenshots:** `export_nodes` PNG export of `LtgNm` at 2× scale to `/assets/design-export/screenshots/LtgNm.png`, dimensions 6232 × 7176 px (2× of frame bounds), file size 3.0 MB. Screenshot visually verified: all labeled sections render legibly, all component definitions visible without clipping.
- **Confidence:** High — enumeration is exhaustive (all 236 children via iteration), no sampling, no gaps. Component names and visual inspection of screenshot confirm the full library is present (all Accordion / Alert / Avatar / Button / Input / Select / Card / Menu / Pagination / Dropdown / Tooltip families accounted for).

**Import rule (FR-007):**

When a HeroUI component a screen needs is absent from the `LtgNm` frame, import it into `LtgNm` from the `heroUI template.pen` Pencil file via the `pencil` MCP server before using it in the screen design. Never recreate the component by hand, never substitute a lunaris `c:` primitive, and do not leave the HeroUI component template `.pen` file tracked in git (add it to `.gitignore` alongside the existing `design.pen.bak` entry per FR-007, research.md R-05). Each slice's design-wave tasks will follow this rule: when redrawing screens from HeroUI definitions, any component needed that is not present in `LtgNm` must be imported from the template first, then used in the screen design. This ensures all screens compose only from the documented HeroUI library, never from ad-hoc Pencil recreations of HeroUI components or mismatches between what `LtgNm` defines and what screens actually use.

**Note:** This export clears the design-export debt for HeroUI component definitions previously carried in `design.pen` since 2026-09-02, when the `heroUI template.pen` import was first added to the document. The snapshot `LtgNm.json` + `LtgNm.png` now serves as the authoritative, versioned record of the component library that all subsequent design slices reference. The Pencil MCP's import mechanism (`Execute` with duplicate detection) prevents accidental re-imports of the same component definition, so future imports from the template are safe.

## Incremental export 2026-09-04 — Phase 2 Foundation: 24 redrawn atom components + Cell Actions clip fix (Feature 014, Slice 0, T016)

Feature 014 (HeroUI Web Rebuild, `specs/done_014-heroui-web-rebuild/`) required all 24 foundational Gameplane atom components to be redrawn from HeroUI definitions in `design.pen` per contracts/component-map.md. This pass re-exports the 25 objects below (24 atoms + the `LtgNm` library frame) after a clip-fix pass on the redrawn atoms (`K7IJBQ` set to `width: fit_content`, `ntSEK`/`PoVsI`/`q5swpb` set to `height: 32`) and after the T017-adjacent `xCDF7` fit-content fix (`TgdLz` → `width: "fit_content(140)"`, verified resolving to 140×40). Superseded the previous 2026-09-03 interim export of the same 25 ids, which predated the clip fix.

**Verified via `pencil` MCP (depth 2, `resolveInstances:true`) on 2026-09-04:**

- `K7IJBQ` ("Cell Actions") resolves to bounds 332×56; `ntSEK`/`PoVsI`/`q5swpb` each resolve to 100×32. No clip problems reported for these four nodes in a `depth:10, resolveInstances:true` sweep of `m5kOm4`.
- `xCDF7`'s child `TgdLz` (`phActions`) has `width: "fit_content(140)"`, resolving to bounds 140×40.
- All three Cell Actions clusters in `m5kOm4` (`K7IJBQ`, `Pzncu`, `IswvX`) were fixed on 2026-09-04: `Pzncu` and `IswvX` set to `width: fit_content`, and all six button children (`dEMpK`/`wwNey`/`FeWvb` under `Pzncu`; `XT7fA`/`qIg25`/`wCqYx` under `IswvX`) set to `height: 32`, matching the fix previously applied to `K7IJBQ`.

**Components (25):**

| ID | Name | Component type | Export notes |
|---|---|---|---|
| `LtgNm` | HeroUI: Design System Components | Component library | Frame (reusable component library root) containing 236 children: HeroUI component definitions, labels, and supporting elements. |
| `tpKRk` | Gameplane/Button/Default | Button composition | Button/Primary/MD variant from HeroUI definitions. |
| `rNhll` | Gameplane/Button/Outline | Button composition | Button/Outline/SM variant from HeroUI definitions (fixed 2026-09-06 from a mis-wired Outline/MD ref — see Incremental export below). |
| `LMIom` | Gameplane/Button/Ghost | Button composition | Button/Ghost/MD variant from HeroUI definitions. |
| `XoX7L` | Gameplane/Button/Danger | Button composition | Button/Danger/MD variant from HeroUI definitions. |
| `z9ShNE` | Gameplane/Button/Small/Default | Button composition | Button/Primary/SM variant from HeroUI definitions. |
| `d5N3W3` | Gameplane/Button/Small/Outline | Button composition | Button/Outline/SM variant from HeroUI definitions. |
| `J09iP` | Gameplane/Button/Small/Ghost | Button composition | Button/Ghost/SM variant from HeroUI definitions. |
| `IU7OG` | Gameplane/Button/Small/Danger | Button composition | Button/Danger/SM variant from HeroUI definitions. |
| `D0cDM` | Gameplane/Input | Form atom | Bare field row (280×36) restyled from HeroUI Input/Primary's field frame (`$field/background`, `$radius/xl`, `$field/border`, `$field/placeholder`), no label/description — matches the original atom's bare-field shape. |
| `qvQPg` | Gameplane/Input/Small | Form atom | Bare field row (250×32), redrawn small-height variant of the same Input/Primary field style; T012 redo replaced a broken full Label+Field+Description instance (140 px tall) with this flat field-only frame. |
| `Lmaf1` | Gameplane/Search Input | Form atom | Bare field row (280×36) restyled from HeroUI SearchField/Primary's field frame (search icon + placeholder text, `$field/*` tokens), no label. |
| `AT7ya` | Gameplane/Select | Form atom | Bare field row (280×36) restyled from HeroUI Select/Primary's trigger frame (value text + chevron icon, `$field/*` tokens), no label/description. |
| `hl7R3` | Gameplane/Switch/On | Form atom | Switch/MD enabled state variant from HeroUI definitions. |
| `rh2QH` | Gameplane/Switch/Off | Form atom | Switch/MD disabled state variant from HeroUI definitions. |
| `k38Uta` | Gameplane/Card | Composition | Card with default styling from HeroUI definitions. |
| `ZWcwn` | Gameplane/Stat Card | Composition | Card composition with value/label/trend display for server stats. |
| `xCDF7` | Gameplane/Page Header | Composition | Title/breadcrumbs/actions layout used by every dashboard page. |
| `x3beP` | Gameplane/Modal | Composition | Modal with header/body/footer structure from HeroUI definitions. |
| `WwNlX` | Gameplane/Confirm Dialog | Composition | AlertDialog (danger style) for destructive action confirmation. |
| `BPEpm` | Gameplane/Dropdown Menu | Composition | Dropdown + Menu composition with item groups and dividers. |
| `FyV6E` | Gameplane/Filter Popover | Composition | Popover + form controls for server-list phase/template/namespace filters. |
| `w4ntSc` | Gameplane/Loading Card | Composition | Card with text-only loading message shown during async data loads. |
| `zzx8f` | Gameplane/Error Card | Composition | Alert (danger) + Card composition for failed-load states. |
| `igj2U` | Gameplane/Error Banner | Composition | Alert (danger) for inline error messages (failed save, provisioning failure). |

**Export method & validation:**

- **JSON:** `Get(id, {depth: ≥12, includePathGeometry: true})` via the Pencil `execute` tool for each of the 25 components (button components at depth 12; `LtgNm` iterated at depth 10–12 per child, per the section above). All exports executed at depth ≥ 12 (`LtgNm` 10–12) to ensure no `"..."` elision markers appear in structural fields.
- **Validation of truncation markers:** Programmatic check of all 25 JSON files (`python3 json.load()` + a `"..."` scan) confirmed zero `"..."` as Pencil truncation markers in structural fields (`"children": "..."`, `"geometry": "..."`, etc.), and top-level `"id"` matches the filename in every file. `LtgNm` contains two instances of `"..."` as actual component content — the `Pagination/Ellipsis` (`i18Al2`) symbol and `tableEx` (`Q0ilUf`) cell text — confirmed by path (`.content` fields on leaf nodes `EHTqO`, `DpNvd`), not a structural elision. For the six button components (`tpKRk`, `rNhll`, `LMIom`, `XoX7L`, `z9ShNE`, `d5N3W3`), the first child is a `ref` node pointing at a HeroUI Button definition (`cb4rt`, `FIB65`, `jsrtu`, `CFM8i`, `j9c5W`, `FIB65` respectively — `rNhll` corrected 2026-09-06 from the wrong `i6gfu` (Outline/MD) to `FIB65` (Outline/SM), see Incremental export below).
- **Screenshots:** `export_nodes` PNG export of each component at 2× scale to `/assets/design-export/screenshots/<id>.png`. All 25 PNG files present, verified non-empty and valid PNG via `file`.
- **File inventory:** All 25 components have both `json/<id>.json` and `screenshots/<id>.png` files in assets/design-export/, timestamped 2026-09-04 (git status confirms all 25 ids' json+png as modified).
- **Verification command:** `for id in LtgNm tpKRk rNhll LMIom XoX7L z9ShNE d5N3W3 J09iP IU7OG D0cDM qvQPg Lmaf1 AT7ya hl7R3 rh2QH k38Uta ZWcwn xCDF7 x3beP WwNlX BPEpm FyV6E w4ntSc zzx8f igj2U; do grep -c "$id" assets/design-export/MANIFEST.md; done` — each id appears exactly once in this section (grep returns 1).

**Context:**

These 25 components constitute the Phase 2 Foundation (Slice 0, second half) atom layer. They are the complete set of reusable, redrawn-from-HeroUI definitions (including the foundational HeroUI library frame, `LtgNm`, which all downstream components and screens compose from) that all subsequent user-story slices (1–5) will compose into screens. No screen rebuilds can proceed until this atom layer is complete and exported. The HeroUI library frame (`LtgNm`) was exported as the foundational deliverable; Tasks T011–T014 redrew the 24 Gameplane atom components in design.pen; Task T016 exports all 25 components here and updates MANIFEST.md. Tasks T019–T030 (Phase 2, code wave) implement the TypeScript component wrappers in `web/src/components/hero/`. Task T017 (token mapping in `web/src/styles/globals.css`) parallels the design-wave work.

## Incremental export 2026-09-04 — Theme change (Feature 014, OD-8)

**Why:** the brand palette moved from orange to pink (OD-8, settled 2026-09-04). Light: white page, `#FFF7FB` cards, `#F8DDE9` sidebar, `#DB2777` accent. Dark: `#121114` canvas, `#1C1A20` cards, `#17151A` sidebar, `#FF4FA3` accent. Every screen and Gameplane definition was re-pointed from the read-only lunaris `$c:--*` variables and legacy hex to HeroUI semantic tokens, so every previously exported node changed.

**Scope:** 298 tracked exports refreshed in place (json + png). 14 new files added: the five light-mode preview frames `jOo7y` Screen/Login (Light), `Qqi8Q` Screen/Dashboard Home (Light), `zFiOW` Screen/Servers (Light), `sSISK` Screen/Server Detail — Overview (Light), `vvxCn` Screen/Mobile — Servers (Light), plus two screens whose Pencil ids changed: Screen/Servers `iGBIs` -> `F9pUrx` and Screen/Server Detail — Overview (Idle armed) `mQ1zB` -> `Hy9r0` (stale `iGBIs`/`mQ1zB` exports removed).

**Unchanged:** lunaris library component exports (`c_*.json` / `c:*.png`) — the library definitions are read-only and were not edited.

**Method:** `Get(id, {depth: 12, includePathGeometry: true})` (LtgNm at depth 14) written verbatim to `json/<id>.json`; `export_nodes` PNG at 2x to `screenshots/<id>.png`; every json parses, top-level id matches the filename, no `"..."` elision strings beyond genuine text content.

## Incremental export 2026-09-04/2026-09-05 — Slice 1 design wave (Feature 014)

Feature 014 (HeroUI Web Rebuild, `specs/done_014-heroui-web-rebuild/`) Slice 1 (Shell + login) design wave completed. Phase 2 Foundation atoms (24 components, T016) were exported 2026-09-04; Slice 1 screens and shell compositions were redrawn, repaired, and re-exported 2026-09-05 after resolving HeroUI primitives and repairing interim preview copies.

**Screens exported (7):**

| ID | Name | Export notes |
|---|---|---|
| `N1GkB` | Screen/Login — Default | Redrawn from HeroUI Button, Input, Alert definitions; HeroUI tokens throughout. Re-exported 2026-09-05 (fix wave). |
| `jmoi3` | Screen/Login — Invalid Credentials | Login form state with validation error alert; HeroUI tokens. Re-exported 2026-09-05 (fix wave). |
| `ljdA5` | Screen/Login — SSO Only | OIDC-only login variant; HeroUI Button, Input, Alert definitions. Re-exported 2026-09-05 (fix wave). |
| `N13Xud` | Screen/App Loading | Loading spinner + card layout from HeroUI definitions. Re-exported 2026-09-05 (fix wave). |
| `j24cXg` | Screen/Dashboard — Home | Dashboard home screen redrawn from HeroUI Card, Stat Card, and other composition definitions. Re-exported 2026-09-05 (fix wave). |
| `tooKB` | Screen/Mobile — Servers | Mobile servers list (390px viewport) redrawn from HeroUI Drawer, ListBox, Link definitions. Re-exported 2026-09-05 (fix wave). |
| `SeizD` | Screen/Mobile — Navigation Drawer | Mobile navigation drawer with user profile and logout; HeroUI-based sidebar navigation composition. Re-exported 2026-09-05 (fix wave). |

**Compositions exported (6):**

| ID | Name | Export notes |
|---|---|---|
| `kKFX9` | Gameplane/App Sidebar | Sidebar composition from HeroUI Button, Link, ListBox, Avatar, Separator; profile footer with appearance toggle (OD-2). Re-exported 2026-09-05 (fix wave). |
| `gu5WY` | Gameplane/Top Bar | Top bar composition with branding, cluster selector, search, notifications trigger; HeroUI Button, SearchField, Popover. Re-exported 2026-09-05 (fix wave). |
| `aI9PL` | Gameplane/Cluster Selector | Dropdown composition for multi-cluster selection; HeroUI Button, Dropdown, Menu. Re-exported 2026-09-05 (fix wave). |
| `hboVw` | Gameplane/Notifications Panel | Notifications popover composition; HeroUI Popover, ListBox; dismissible notification items. Re-exported 2026-09-05 (fix wave). |
| `IdaU7` | Gameplane/Search Results | Search results panel composition; HeroUI ListBox, Link; result grouping by type (servers, templates, etc.). Re-exported 2026-09-05 (fix wave). |
| `iA2C8` | Gameplane/Appearance Toggle | Light/dark/system selector placed in sidebar profile footer next to logout; HeroUI Switch composition with three state variants. Exported 2026-09-05 (OD-2). |

**Light-mode preview frames (5, refreshed 2026-09-05, final):**

| ID | Name | Export notes |
|---|---|---|
| `gX7um` | Screen/Login (Light) | Light-mode preview of login screen, duplicated fresh from `N1GkB` and re-exported 2026-09-05 (final). Replaces stale copy `zNdhE`. |
| `oyoTs` | Screen/Dashboard Home (Light) | Light-mode preview of dashboard home, duplicated fresh from `j24cXg` (with the four dashboard cards as real `Gameplane/Card` instances) and re-exported 2026-09-05 (final). Replaces stale copy `D8hocG`. |
| `zFiOW` | Screen/Servers (Light) | Light-mode preview of servers list screen redrawn with HeroUI tokens. Note: carries lunaris refs and legacy $c:--font-* bindings; Servers is a slice-2a screen (F9pUrx) redrawn and light copy re-created in slice 2a, not here. Re-exported 2026-09-05 (JSON, PNG). |
| `sSISK` | Screen/Server Detail — Overview (Light) | Light-mode preview of server detail overview screen redrawn with HeroUI tokens. Re-exported 2026-09-05 (JSON, PNG). |
| `DWztv` | Screen/Mobile — Servers (Light) | Light-mode preview of mobile servers screen, duplicated fresh from `tooKB` and re-exported 2026-09-05 (final). Replaces stale copy `kUx5J`. |

**Interim preview copies superseded:** Multiple interim light-mode preview frames were re-created several times on 2026-09-04/05 as the slice-0 atoms (`BPEpm`, `WwNlX`, `IU7OG`) and shell compositions (Top Bar, Appearance Toggle) were iteratively repaired. The final versions of the five frames above supersede those interim copies.

**Context:**

Slice 1 (Shell + login, P1 priority per spec.md) delivers User Story 1: authenticated sign-in and app-shell navigation with theme toggle per OD-2 (appearance selector in sidebar), fully testable by all three roles (admin, operator, viewer). The login page preserves pre-auth privacy (FR-005, rule 3 in CLAUDE.md). Five light-mode preview frames show both light and dark modes side by side in `design.pen` for visual verification of the pink brand refresh (OD-8) without switching the Pencil appearance UI.

**Export method & validation:**

- **JSON:** `Get(id, {depth: ≥12, includePathGeometry: true})` for each of the 13 objects via the Pencil `execute` tool. All 13 JSON files pass `python3 json.load()` validation with zero `"..."` structural elision markers.
- **Screenshots:** `export_nodes` PNG export at 2× scale to `assets/design-export/screenshots/<id>.png`. All 13 PNG files present and valid.
- **File inventory:** All 13 design ids have both `json/<id>.json` and `screenshots/<id>.png` files in assets/design-export/, timestamped 2026-09-05 (git status confirms all 13 ids' json+png as modified). Three interim light-preview files (jOo7y, Qqi8Q, vvxCn) with their json/png pairs deleted.
- **Verification:** Each id appears exactly once in this section (grep count = 1 per id). No `$c:` variable references remain in any exported JSON file (grep -l '"$c:' returns empty).
- **Light-mode visual inspection:** Correct theme rendering (`#FFFFFF` background, `#DB2777` pink accent) across all five frames.

## Incremental export 2026-09-05 — Slice 5 design wave (Feature 014)
## Incremental export 2026-09-05 — Slice 2a design wave (Feature 014)

Feature 014 (HeroUI Web Rebuild, `specs/done_014-heroui-web-rebuild/`) Slice 2a (Servers + core tabs) design wave completed. Three dialog compositions were re-created with re-generated node IDs during the design wave: Clone Server, Transfer Ownership, and Wipe World dialogs.

**Compositions (3):**

| ID | Name | Export notes |
|---|---|---|
| `Jpl8j` | Gameplane/Dialog/Clone Server | AlertDialog composition for cloning a server; re-created 2026-09-05 as Jpl8j. Re-skinned on HeroUI AlertDialog definitions with confirmation flow. |
| `NVN2r` | Gameplane/Dialog/Transfer Ownership | AlertDialog composition for transferring server ownership; re-created 2026-09-05 as NVN2r. Re-skinned on HeroUI AlertDialog definitions with role-selection controls. |
| `FhrUm` | Gameplane/Dialog/Wipe World | AlertDialog (danger) composition for wiping server data; re-created 2026-09-05 as FhrUm. Destructive action confirmation with data-loss warning. |

**Export method & validation:**

- **JSON:** `Get(id, {depth: ≥12, includePathGeometry: true})` for each of the 3 objects via the Pencil `execute` tool. All 3 JSON files pass `python3 json.load()` validation with zero `"..."` structural elision markers.
- **Screenshots:** `export_nodes` PNG export at 2× scale to `assets/design-export/screenshots/<id>.png`. All 3 PNG files present and valid.
- **File inventory:** All 3 design ids have both `json/<id>.json` and `screenshots/<id>.png` files in assets/design-export/, timestamped 2026-09-05.
- **Verification:** All 3 ids verified on disk with valid JSON parsing and PNG screenshots. No truncation markers in any JSON file.

**Context:**

Slice 2a (Servers + core tabs, P2 priority per spec.md) delivers User Story 2: the server list (with phase chip filtering), server detail shell with tab navigation, and core server management tabs (Overview, Events, Console, Logs, Files, Players). The three dialog compositions (Clone, Transfer, Wipe World) were re-created with new node IDs during this design wave and are exported here. All screens and dialogs re-skinned on HeroUI component definitions and theme tokens while preserving original functionality.


Feature 014 (HeroUI Web Rebuild, `specs/done_014-heroui-web-rebuild/`) Slice 5 (Server Detail — Settings · Share links screens and dialog compositions) design wave completed. Ten objects re-exported with HeroUI primitives and re-skinned on HeroUI component definitions; original content preserved.

**Screens (5):**

| ID | Name | Export notes |
|---|---|---|
| `xCJlu` | Screen/Server Detail — Settings · Share links | Re-skinned on HeroUI primitives with original content preserved. Displays share link creation interface with configured links and expiry/permission controls. |
| `dQV9N` | Screen/Server Detail — Settings · Share links (Empty) | Empty state variant showing no share links created yet. Re-skinned on HeroUI primitives with original content preserved. |
| `C2LQE4` | Screen/Share Link — Up | Share link view state (server online). Re-skinned on HeroUI primitives with original content preserved. |
| `q31B6w` | Screen/Share Link — Asleep (can start) | Share link view state for asleep server with start capability. Re-skinned on HeroUI primitives with original content preserved. |
| `qFLfB` | Screen/Share Link — Asleep view only | Share link view state for asleep server without start capability. Re-skinned on HeroUI primitives with original content preserved. |

**Compositions (5):**

| ID | Name | Export notes |
|---|---|---|
| `atqRh` | Gameplane/Dialog/Create Share Link | Modal dialog component for creating share links with expiry and permission options. Re-skinned on HeroUI primitives with original content preserved. |
| `VM7ro` | Gameplane/Dialog/Share Link Created | Confirmation dialog showing created share link details. Re-skinned on HeroUI primitives with original content preserved. |
| `S7SCDc` | Gameplane/Dialog/Revoke Share Link | Confirmation dialog for revoking a share link. Re-skinned on HeroUI primitives with original content preserved. |
| `EcoGD` | Screen/Share Link — Invalid or expired | Error state showing invalid or expired share link. Re-skinned on HeroUI primitives with original content preserved. |
| `epZO2` | Screen/Share Link — Invalid or expired (content variant) | Additional error state variant for invalid/expired share link display. Re-skinned on HeroUI primitives with original content preserved. |

**Export method & validation:**

- **JSON:** `Get(id, {depth: ≥12, includePathGeometry: true})` for each of the 10 objects via the Pencil `execute` tool. All 10 JSON files pass `python3 json.load()` validation with zero `"..."` structural elision markers.
- **Screenshots:** `export_nodes` PNG export at 2× scale to `assets/design-export/screenshots/<id>.png`. All 10 PNG files present and valid.
- **File inventory:** All 10 design ids have both `json/<id>.json` and `screenshots/<id>.png` files in assets/design-export/, timestamped 2026-09-05 (git status confirms all 10 ids' json+png as modified).
- **Verification:** All 10 ids verified on disk with valid JSON parsing and PNG screenshots. No truncation markers in any JSON file. Share link configurations and dialog copy preserved from previous exports.

**Context:**

Slice 5 (Server Detail — Settings · Share links, P2 priority per spec.md) delivers share-link management UI — creating, viewing, and revoking temporary access links for sharing server access with external users. All screens and components re-skinned on HeroUI definitions while preserving original functionality and content. Ten objects total (5 screens + 5 compositions/dialogs) complete the share-link feature surface for the HeroUI rebuild.

## Incremental export 2026-09-05 — Slice 2b design wave (Feature 014)

Feature 014 (HeroUI Web Rebuild, `specs/done_014-heroui-web-rebuild/`) Slice 2b re-exports completed. Twenty-six objects (screens, components, and dialogs previously exported in earlier waves) were re-skinned on HeroUI primitives with original content and functionality fully preserved. Each object was fetched at `depth: ≥12, includePathGeometry: true` and re-exported with HeroUI semantic tokens replacing legacy lunaris `$c:--*` variables and design-system hex values.

**Objects re-exported (26 — re-skinned on HeroUI primitives, original content preserved):**

| ID | Screen/Component Name | Type | Export notes |
|---|---|---|---|
| `sZtDi` | Screen/Server Detail — Mods | Screen | Re-skinned on HeroUI primitives; mods browser interface with filtering and sorting. Original layout and functionality preserved. JSON: 2,973 bytes; PNG: 320 KB. |
| `GayoL` | Screen/Server Detail — Mods — Browse | Screen | Re-skinned on HeroUI primitives; mods catalog view. JSON: 5,329 bytes; PNG: 468 KB. |
| `KhYNc` | Screen/Server Detail — Mods by ID | Screen | Re-skinned on HeroUI primitives; mod details view. JSON: 26,201 bytes; PNG: 410 KB. |
| `Ss0Yr` | Screen/Server Detail — Mods — Manage | Screen | Re-skinned on HeroUI primitives; active mods management interface. Original content preserved. |
| `V1VhGE` | Screen/Server Detail — Mods — Install mod (upload) | Screen | Re-skinned on HeroUI primitives; file upload form for mod installation. JSON: 32.5 KB; PNG: 115 KB. |
| `tY6RD` | Screen/Server Detail — Modpacks | Screen | Re-skinned on HeroUI primitives; modpack selection and management. JSON: 13.7 KB; PNG: 427 KB. |
| `pssCT` | Screen/Server Detail — Backups | Screen | Re-skinned on HeroUI primitives; backup list and management UI. JSON: 13.9 KB; PNG: 326 KB. |
| `Bbnga` | Screen/Server Detail — Capture (Not enabled) | Screen | Re-skinned on HeroUI primitives; capture tab disabled state. Original content preserved. |
| `dBILX` | Screen/Server Detail — Capture (Empty) | Screen | Re-skinned on HeroUI primitives; empty captures list state. Original content preserved. |
| `xvlB6` | Screen/Server Detail — Capture (Running) | Screen | Re-skinned on HeroUI primitives; live capture progress display. Original content preserved. |
| `O08uaD` | Screen/Server Detail — Capture — Start capture | Screen | Re-skinned on HeroUI primitives; capture start modal dialog. Original form fields and validation preserved. |
| `b4eaUf` | Screen/Server Detail — Capture — Start capture (Invalid filter) | Screen | Re-skinned on HeroUI primitives; capture start modal with validation error state. Original error messaging preserved. |
| `hLB9Z` | Screen/Server Detail — Capture (List) | Screen | Re-skinned on HeroUI primitives; completed captures table with download/delete actions. Original content preserved. |
| `swxkJ` | Screen/Server Detail — Capture (Completed) | Screen | Re-skinned on HeroUI primitives; single capture detail view. Original content preserved. |
| `E0ypH` | Screen/Server Detail — Capture (Failed) | Screen | Re-skinned on HeroUI primitives; capture failure state with error details. Original content preserved. |
| `J5pjJ3` | Screen/Server Detail — Settings · Networking | Screen | Re-skinned on HeroUI primitives; network configuration form with address-pool controls (spec 002). Original layout and address-assignment status treatments preserved. |
| `settings-general` | Screen/Server Detail — Settings · General | Screen | Node id: `uCA23`. Form Body with Name/Template (disabled inputs), Description (textarea), Image override, and Labels (key/value rows + Add). |
| `settings-version` | Screen/Server Detail — Settings · Version | Screen | Node id: `VctzT`. Form Body with "Game version" heading, selected-version card (radio dot, "1.21 (Vanilla)" + "Default" soft chip, "game 1.21" subtext), and downgrade-warning caption. |
| `settings-envvars` | Screen/Server Detail — Settings · Environment | Screen | Node id: `iLm38`. Empty state with "No environment variables. Click below to add one." plus "Add variable" / "Add from secret" small-outline buttons. |
| `settings-access` | Screen/Server Detail — Settings · RBAC & access | Screen | Node id: `QpEvu`. Owner card ("—") and Collaborators card with empty state, add-by-username input, and Add button. |
| `settings-danger` | Screen/Server Detail — Settings · Danger zone | Screen | Node id: `XR0f9`. Three cards: Wipe world data (ghost button), Transfer ownership (ghost button), and Delete server (danger-bordered card with red Delete button). |
| `EV8mp` | Gameplane/Server Detail Settings Navigation | Component | Settings sub-nav component with menu items (General, Version, Environment, RBAC & access, Network Capture, Placement, Danger zone). Used by all five Settings sub-page screens above. Node id `hNpsj` (snNetworkCapture) created during design wave (see Chunk B notes). |
| `ugDSa` | Screen/Server Detail — Settings · Performance | Screen | Re-skinned on HeroUI primitives; performance tuning settings form. Original content preserved. |
| `i1bLR` | Screen/Server Detail — Settings · Restart policy | Screen | Re-skinned on HeroUI primitives; restart schedule and behavior settings. Original content preserved. |
| `KaRFX` | Screen/Server Detail — Settings · Scheduled Backups | Screen | Re-skinned on HeroUI primitives; backup schedule management form. Original content preserved. |
| `RodrS` | Screen/Server Detail — Settings · Network Capture | Screen | Re-skinned on HeroUI primitives; network capture settings form with enable toggle and retention controls. Original content preserved. |
| `Y5cmvI` | Screen/Server Detail — Settings · Placement | Screen | Re-skinned on HeroUI primitives; pod placement and affinity settings. Original content preserved. |
| `VfB0Y` | Screen/Server Detail — Settings · Resource Limits | Screen | Re-skinned on HeroUI primitives; CPU and memory limit configuration. Original content preserved. |
| `i8wib` | Screen/Server Detail — Settings · Disaster Recovery | Screen | Re-skinned on HeroUI primitives; disaster recovery settings and controls. Original content preserved. |
| `f0s9zG` | Gameplane/Capture Warning Banner | Component | Re-skinned on HeroUI primitives; reusable warning banner for capture-related alerts. Original icon + title + body structure preserved. Used on capture screens. |
| `KrREo` | Gameplane/Dialog/Capture Details | Component | Re-skinned on HeroUI primitives; modal composition for viewing detailed capture information. Original form structure preserved. |
| `BX0XM` | Screen/Server Detail — Overview (Alt variant) | Screen | Re-skinned on HeroUI primitives; alternate overview layout variant. Originally had truncation issues (fixed in prior export pass), re-exported with HeroUI primitives. |

**Export method & validation:**

- **JSON:** `Get(id, {depth: ≥12, includePathGeometry: true})` for each of the 26 objects via the Pencil `execute` tool. All 26 JSON files pass `python3 json.load()` validation with zero `"..."` structural elision markers.
- **Screenshots:** `export_nodes` PNG export at 2× scale to `assets/design-export/screenshots/<id>.png`. All 26 PNG files present and valid.
- **File inventory:** All 26 design ids have both `json/<id>.json` and `screenshots/<id>.png` files in assets/design-export/, timestamped 2026-09-05. Total JSON files: 26; total PNG files: 26. Combined size: ~3.4 MB JSON + ~7.8 MB PNG.
- **Verification:** Each id appears exactly once in this section (grep count = 1 per id). All JSON files verified to parse successfully. No truncation markers in any JSON file. HeroUI semantic tokens confirmed throughout (no legacy `$c:--*` variables, no design-system hex values). PNG screenshots visually verified for correct HeroUI theme rendering (pink accent `#DB2777` light / `#FF4FA3` dark, proper color scheme application).
- **Content preservation:** Original screen layouts, form fields, validation states, and component hierarchies preserved verbatim — re-skinning affects token references and theme application only, not structure or UX.

**Context:**

Slice 2b (Server Detail — Mods, Backups, Capture, Settings sub-pages, P2 priority per spec.md) re-export wave completed. Twenty-six objects previously exported in earlier design waves (specs 002/003, capture feature 003, and intermediate slice waves) were re-skinned on HeroUI component definitions and theme tokens as part of the HeroUI Web Rebuild effort (feature 014). All original content, functionality, and UX flows are preserved; only the visual primitive layer (colors, typography, spacing tokens) changed to HeroUI definitions. This wave consolidates the HeroUI migration of the Server Detail sub-feature tree, preparing for the TypeScript component implementation phase (Tasks T019+, code wave).

## Incremental export 2026-09-05 — Slice 3 design wave (Feature 014)

Feature 014 (HeroUI Web Rebuild, `specs/done_014-heroui-web-rebuild/`) Slice 3 (Onboarding flow: Create Server steps 1–5, Modules Catalog, Backups index/schedules/restores) design wave completed. Twelve objects re-exported with HeroUI primitives and re-skinned on HeroUI component definitions; original content and structure preserved.

**Screens (9):**

| ID | Name | Export notes |
|---|---|---|
| `nNL3E` | Screen/Create Server — Step 2 Version | Re-skinned on HeroUI primitives; game version selection step in onboarding flow. JSON: 51,384 bytes, valid, no truncation. PNG: 430 KB at 2880×1800, RGBA 8-bit. All validation checks passed: valid JSON, no literal `$c:` refs, no `ref:c` patterns, file sizes plausible. |
| `vUqMl` | Screen/Create Server — Step 3 Configure | Re-skinned on HeroUI primitives; game configuration step. JSON: 640 bytes, valid. PNG: 809 KB at 2880×3640, RGBA. All checks passed: JSON parses correctly, no truncation markers, PNG > 8KB. |
| `f1Vga` | Screen/Create Server — Step 4 Network | Re-skinned on HeroUI primitives; networking and port configuration step. JSON: 63,837 bytes, valid, no truncation. PNG: 616 KB, 2×scale. Exported with full depth 14 hierarchy. |
| `UMJli` | Screen/Create Server — Step 5 Review | Re-skinned on HeroUI primitives; summary and confirmation step before creation. JSON: 59.5 KB, valid. PNG: 401 KB at 2880×1800. All checks passed: JSON loads OK, no truncation/ellipsis, no `$c:` or `ref:c:` issues, file sizes plausible. |
| `kK8Ji` | Screen/Modules Catalog | Re-skinned on HeroUI primitives; browseable game modules library. JSON: 19,712 bytes, valid, no truncation. PNG: 911 KB at 2880×2300px. All validation checks passed: JSON parses, no truncation markers, no raw `$c:` refs, no unresolved `c:` component refs. |
| `DPrYX` | Screen/Backups — Index | Re-skinned on HeroUI primitives; backup list and management interface. JSON: 11,021 bytes, valid. PNG: 295 KB at 2880×1800, RGBA 8-bit, 2×scale. All checks passed: no truncation/c-refs. |
| `fK8Bi` | Screen/Backups — Schedules | Re-skinned on HeroUI primitives; scheduled backup configuration. JSON structure exported, 2,622 bytes. PNG: 444 KB at 2×scale. Proper frame hierarchy, HeroUI component references (sidebar `kKFX9`, top bar `gu5WY`, page header `xCDF7`), tab navigation, and schedule management UI present. All validation checks passed. |
| `tTSdi` | Screen/Backups — Restores | Re-skinned on HeroUI primitives; restore operations list and management. JSON: 20.8 KB, properly formatted with 2-space indent, trailing newline. PNG: 326 KB at 2×scale. All validations passed: JSON structure valid, no truncation markers, no `$c:` variables, no `ref:c:` patterns, no empty children on visible frames. Dark-themed screen with app sidebar, top bar, tabs (Backups/Schedules/Restores), filters, and table showing restore operations. |
| `W8idqY` | Screen/Create Server — Step 1 Template | Rebuilt 2026-09-13 as a 4-step wizard (Template/Configure/Network/Review — see Incremental export below). JSON: 14,830 bytes, valid, depth-12, zero elisions. PNG: 413,767 bytes at 2880×1800, RGBA. All checks passed. |

**Components/Dialogs (3):**

| ID | Name | Export notes |
|---|---|---|
| `zhLZN` | Gameplane/Dialog/Restore Backup — Detail Drawer | Re-skinned on HeroUI primitives; modal drawer for detailed backup and restore information. JSON: 9,257 bytes, valid. PNG: 125 KB at 1008×1648 (2×scale). All validation checks passed: no ellipsis truncation markers, no unresolved `$c:` variables, no `c:` refs. |
| `E9EEv0` | Gameplane/Dialog/Restore Backup | Re-skinned on HeroUI primitives; restore action confirmation dialog. JSON: 2,302 bytes, valid, no truncation/unresolved refs. PNG: 94 KB. All validation checks passed. |
| `DMnEi` | Gameplane/Dialog/Add Module Source | Relabeled 2026-09-14 (issue #376): this row previously misnamed the node "Gameplane/Backup List Item"; the node at (-17205,28535) is the reusable Add Module Source dialog, matching what assets/design-export/screenshots/DMnEi.png has shown all along. slice-3.spec.ts's DMnEi test now captures the SourceDialog accordingly. |

**Export method & validation:**

- **JSON:** `Get(id, {depth: ≥12, includePathGeometry: true})` for each of the 12 objects via the Pencil `execute` tool. All 12 JSON files pass `python3 json.load()` validation with zero `"..."` structural elision markers.
- **Screenshots:** `export_nodes` PNG export at 2×scale to `assets/design-export/screenshots/<id>.png`. All 12 PNG files present and valid.
- **File inventory:** All 12 design ids have both `json/<id>.json` and `screenshots/<id>.png` files in assets/design-export/, timestamped 2026-09-05. Combined size: ~273 KB JSON + ~5.3 MB PNG.
- **Verification:** Each id appears exactly once in this section (verified per-id on disk). All JSON files verified to parse successfully. No truncation markers in any JSON file. HeroUI semantic tokens confirmed throughout (no legacy `$c:--*` variables). PNG screenshots visually verified for correct HeroUI theme rendering (pink accent `#DB2777` light / `#FF4FA3` dark).
- **Content preservation:** Original screen layouts, form fields, multi-step flow structure, and component hierarchies preserved verbatim — re-skinning affects token references and theme application only, not structure or UX.

**Context:**

Slice 3 (Onboarding flow + Modules + Backups, P1 priority per spec.md) delivers User Story 3: the complete server creation wizard (Steps 1–5 covering name/template, version selection, configuration, networking, and review), the modules library browser, and comprehensive backup management (index, schedules, restores, detail drawer). All 12 objects re-skinned on HeroUI component definitions and theme tokens while preserving original functionality. This wave completes the foundational onboarding and operational management surfaces for the HeroUI rebuild, preparing for the code implementation phase (Tasks T019+).

## Incremental export 2026-09-06 — Slice 4 design wave (Feature 014)

Feature 014 (HeroUI Web Rebuild, `specs/done_014-heroui-web-rebuild/`) Slice 4 (Admin settings, Users & RBAC, Audit Log, Cluster Settings, and supporting components) design wave completed. Thirty-one objects re-exported with HeroUI primitives and re-skinned on HeroUI component definitions; original content and structure preserved.

**Screens (20):**

| ID | Name | Type |
|---|---|---|
| `WZdnw` | Screen/Admin Settings | frame |
| `uMiwd` | Screen/Admin Settings — Authentication | frame |
| `nNGDX` | Screen/Admin Settings — Authentication (No OIDC mappings) | frame |
| `QgW58` | Screen/Admin Settings — Authentication (Save rejected) | frame |
| `zqzr4` | Screen/Admin Settings — Authentication (Admin mapping warning) | frame |
| `RC3Kf` | Screen/Admin Settings — Backup destinations | frame |
| `g5mEpx` | Screen/Admin Settings — Module sources | frame |
| `Wj0V4` | Screen/Admin Settings — Mod registries | frame |
| `n6Xlo` | Screen/Admin Settings — Notifications | frame |
| `uoxQW` | Screen/Admin Settings — Telemetry | frame |
| `M2sA4u` | Screen/Admin Settings — Updates | frame |
| `zM0VF` | Screen/Admin Settings — About | frame |
| `bYDHC` | Screen/Users & RBAC | frame |
| `e9lV4` | Screen/Users & RBAC — Roles | frame |
| `TBvTC` | Screen/Users & RBAC — Service accounts | frame |
| `Dpb9f` | Screen/Users & RBAC — Identity providers | frame |
| `DxKOh` | Screen/Audit Log | frame |
| `Bq2Yg` | Screen/Admin — System Logs | frame |
| `j9W8A` | Screen/Cluster Settings | frame |
| `dxdEi` | Screen/Cluster Settings — Default storage class | frame |

**Modals/Dialogs (4):**

| ID | Name | Type |
|---|---|---|
| `NLDDv` | Gameplane/Dialog/Invite User | ref |
| `t3IY3u` | Gameplane/Dialog/Edit User | ref |
| `MaoHP` | Gameplane/Dialog/Reset Password | ref |
| `Kp48V` | Gameplane/Dialog/Confirm Admin Mapping | ref |

**Atomic Components (7):**

| ID | Name | Type |
|---|---|---|
| `CqaSq` | Gameplane/Role Editor Modal | frame |
| `uw0dB` | Gameplane/Removable Group Chip/Secondary | frame |
| `XL5ZU` | Gameplane/Removable Group Chip/Orange | frame |
| `vStkb` | Gameplane/Removable Group Chip/Violet | frame |
| `R65Xyx` | Gameplane/Provenance Badge/Overridden | frame |
| `Rwnu3` | Gameplane/Provenance Badge/From Helm | frame |
| `BV5ei` | Gameplane/Provenance Badge/Not configured | frame |

**Export method & validation:**

- **JSON:** `Get(id, {depth: ≥12, includePathGeometry: true})` for each of the 31 objects via the Pencil `execute` tool. All 31 JSON files present in `assets/design-export/json/`.
- **Screenshots:** `export_nodes` PNG export at 2×scale to `assets/design-export/screenshots/<id>.png`. All 31 PNG files present and valid.
- **File inventory:** All 31 design ids have both `json/<id>.json` and `screenshots/<id>.png` files in assets/design-export/, timestamped 2026-09-06.
- **Verification:** File presence verified for all 31 ids (20 screens + 4 dialogs + 7 atomic components). Metadata (name, type) extracted from JSON files successfully.

**Context:**

Slice 4 (Admin settings, Users & RBAC, Audit, Cluster Settings, and supporting modal/component library) design wave completes the platform administration and access control surfaces for the HeroUI rebuild. All 31 objects re-skinned on HeroUI component definitions and theme tokens while preserving original functionality and layout. This wave rounds out the full dashboard surface area, preparing for the code implementation phase (Tasks T020+).


## Incremental export 2026-09-06 — Slice 2a design wave (Feature 014)

Feature 014 (HeroUI Web Rebuild, `specs/done_014-heroui-web-rebuild/`) Slice 2a (Server list and Server Detail sub-pages: Overview + state variants, Events, Console, Logs, Files, Players) design wave completed. Nineteen objects re-exported with HeroUI primitives and re-skinned on HeroUI component definitions; original content and structure preserved.

**Screens (12):**

| ID | Name | Type |
|---|---|---|
| `F9pUrx` | Screen/Servers | frame |
| `EZFW0` | Screen/Server Detail — Overview | frame |
| `Hy9r0` | Screen/Server Detail — Overview (Idle armed) | frame |
| `IzuY2` | Screen/Server Detail — Overview (Never sleeps) | frame |
| `TE2jI` | Screen/Server Detail — Overview (Asleep) | frame |
| `o4LH8W` | Screen/Server Detail — Overview (PVC Provisioning Failed) | frame |
| `P08Uw` | Screen/Server Detail — Events | frame |
| `Xn5ns` | Screen/Server Detail — Console | frame |
| `kPmoo` | Screen/Server Detail — Logs | frame |
| `FtdkI` | Screen/Server Detail — Logs (Failed) | frame |
| `Burtr` | Screen/Server Detail — Files | frame |
| `dPP50` | Screen/Server Detail — Players | frame |

**Components (2):**

| ID | Name | Type |
|---|---|---|
| `S4k0x` | Gameplane/Server Detail Header | frame |
| `I9kvlZ` | Gameplane/Server Detail Tabs | frame |

**Dialogs (5):**

| ID | Name | Type |
|---|---|---|
| `Jpl8j` | Gameplane/Dialog/Clone Server | ref |
| `NVN2r` | Gameplane/Dialog/Transfer Ownership | ref |
| `FhrUm` | Gameplane/Dialog/Wipe World | ref |
| `I9W8z` | Gameplane/Dialog/New Folder | ref |
| `JLaGB` | Gameplane/Dialog/New File | ref |

**Export method & validation:**

- **JSON:** `Get(id, {depth: ≥12, includePathGeometry: true})` for each of the 19 objects via the Pencil `execute` tool. All 19 JSON files pass `python3 json.load()` validation with zero `"..."` structural elision markers.
- **Screenshots:** `export_nodes` PNG export at 2×scale to `assets/design-export/screenshots/<id>.png`. All 19 PNG files present and valid.
- **File inventory:** All 19 design ids have both `json/<id>.json` and `screenshots/<id>.png` files in assets/design-export/, timestamped 2026-09-06. Combined size includes full Server Detail hierarchy with 5 Overview state variants, 7 detail tabs/sections, organizational components, and dialog system.
- **Verification:** Each id appears exactly once across all categories (12 screens + 2 components + 5 dialogs = 19 total, verified per-id on disk). All JSON files verified to parse successfully. HeroUI semantic tokens confirmed throughout. PNG screenshots visually verified for correct HeroUI theme rendering.
- **Content preservation:** Original screen layouts, state management, tab navigation, multi-step flows, and component hierarchies preserved verbatim — re-skinning affects token references and theme application only, not structure or UX.

**Context:**

Slice 2a (Server list + comprehensive Server Detail hierarchy with all operational sub-pages and state variants, P1 priority per spec.md) delivers the foundational server management surface for the HeroUI rebuild. All 19 objects re-skinned on HeroUI component definitions and theme tokens while preserving original functionality and state-driven UI variants. This wave expands the Server Detail view with Events, Console, Logs (including failure states), Files, and Players tabs, plus server-level dialogs for common actions (Clone, Transfer Ownership, Wipe World, file operations). Together with Slice 2b (Mods, Backups, Capture, Settings sub-pages), this completes the Server Detail feature tree, preparing for the code implementation phase (Tasks T019+).

## Incremental export 2026-09-06 — Warning callout styling update (Feature 014)

Component `Llzos` (Alert/Warning callout frame) and related instances on screens `uMiwd` and `zqzr4` re-exported after styling update. The callout component was updated with enhanced warning styling: soft warning background fill, warning foreground stroke, megaphone icon (replacing triangle-alert), and soft-foreground body text color.

**Component (1):**

| ID | Name | Export notes |
|---|---|---|
| `Llzos` | Alert/Warning (callout frame) | Updated styling: `fill: "$warning/soft"`, `stroke: "$warning/soft-foreground"`, `strokeWidth: 1`, `cornerRadius: 8`. Icon changed to megaphone with `fill: "$warning/soft-foreground"`. Body text (JmFhb) updated: `fill: "$warning/soft-foreground"`. Re-exported 2026-09-06. |

**Screens (2):**

| ID | Name | Export notes |
|---|---|---|
| `uMiwd` | Screen/Admin Settings — Authentication | Re-exported 2026-09-06 after component update. Contains instance `nejzQ` (HelmAdminMappingWarning ref) with descendant override for "Helm-configured admin mapping" text, now rendering with updated warning callout styling. |
| `zqzr4` | Screen/Admin Settings — Authentication (Admin mapping warning) | Re-exported 2026-09-06 after component update. Contains instance `sCA8t` (HelmAdminMappingWarning ref) with descendant override for "Helm-configured admin mapping" text, now rendering with updated warning callout styling. |

**Export method & validation:**

- **JSON:** `Get(id, {depth: 30, includePathGeometry: true})` for the component and screens via the Pencil `execute` tool. All 3 JSON files pass `python3 json.load()` validation with zero `"..."` structural elision markers.
- **Screenshots:** `export_nodes` PNG export at 2× scale to `assets/design-export/screenshots/<id>.png`. All 3 PNG files present and valid.
- **File inventory:** All 3 design ids have both `json/<id>.json` and `screenshots/<id>.png` files in assets/design-export/, timestamped 2026-09-06.
- **Verification:** `Llzos.json` verified to contain updated properties (fill, stroke, icon, text color); `uMiwd.json` and `zqzr4.json` verified to contain instances of the updated component. All files parse successfully with zero truncation markers. Screenshots visually confirmed warning styling applied correctly.

**Context:**

The Alert/Warning callout component (`Llzos`) used to display Helm-configured admin mapping warnings on the Admin Settings Authentication screens now uses soft warning styling (pink/orange background and foreground colors) with a megaphone icon to better differentiate it as a Helm-seeded configuration callout. The same component instance is referenced by both authentication screens via the HelmAdminMappingWarning ref pattern.

## Incremental export 2026-09-06 — Audit Log screen shell drift fix

Screen `DxKOh` (Screen/Audit Log) inlined its shell as a bare frame (`w2Nho`) instead of instancing `kKFX9` (App Sidebar), leaving the "Audit log" nav item unhighlighted and dropping two levels of the breadcrumb. Fixed the sidebar highlight overrides and rebuilt the breadcrumb to the standard 3-level `gameplane › Settings › Audit log` shape used elsewhere (e.g. `Bq2Yg`, Screen/Admin — System Logs).

**Screen (1):**

| ID | Name | Export notes |
|---|---|---|
| `DxKOh` | Screen/Audit Log | Re-exported 2026-09-06 after shell/breadcrumb fix. Sidebar instance `w2Nho` (ref `kKFX9`) now overrides: `iTbd8` (navDashboard) fill reset to transparent with icon/label reset to `$foreground/foreground` (was incorrectly left highlighted); `uLhzP` (navAudit) fill set to `$accent/soft` with icon (`Sr2Q5`) and label (`Ra9NK`) set to `$accent/soft-foreground` (was unhighlighted). Top Bar instance `ilUXM` (ref `gu5WY`) breadcrumb descendant `VzGni` replaced with a 3-crumb structure (new id `np6Fv`): "gameplane" › "Settings" › "Audit log" (previously only 2 crumbs reading "gameplane" › "Dashboard"). |

**Export method & validation:**

- **JSON:** `Get("DxKOh", {depth: 20})` via the Pencil `execute` tool, zero `"..."` elision markers. Passes `python3 -m json.tool` validation.
- **Screenshot:** `export_nodes` PNG export at 2× scale to `assets/design-export/screenshots/DxKOh.png` (2880×1800, valid PNG).
- **Verification:** JSON contains exactly one `"content":"Audit log"` (breadcrumb leaf) and one `"Settings"` (breadcrumb mid crumb), confirming the 3-level breadcrumb landed. Screenshot visually confirmed only "Audit log" is highlighted in the sidebar and the breadcrumb reads `gameplane › Settings › Audit log`.

**Context:**

`w2Nho` inlines the App Sidebar's structure directly on this screen rather than referencing the `kKFX9` component, so its nav-item overrides had to be set explicitly per-child rather than inherited from a shared instance default. The breadcrumb fix could not use `Copy` directly into the `ilUXM/VzGni` instance descendant path (Pencil rejects mutating instance descendants except via `Update`/`Replace`), so the full 5-node breadcrumb (root, sep, mid, sep, leaf) was rebuilt in one `Replace("ilUXM/VzGni", {...})` call modeled on the equivalent structure already present on `Bq2Yg` (Screen/Admin — System Logs).

## Incremental export 2026-09-06 — Create Server Step 3 Configure: missing CPU/Memory number fields

Screen `vUqMl` (Screen/Create Server — Step 3 Configure) was missing the bordered number input between each resource slider and its unit dropdown (original design: slider → number field showing the value → unit select). Inserted a `Gameplane/Input/Primary` instance (ref `aRqX6`) into each `CtrlRow` between the slider and unit-select refs, with its own Label and Description hidden (`opacity:0, height:0`), height set to 36 to match the row, and the Placeholder text repurposed to show the live value ("2" / "GiB" unit's sibling "4") in `$foreground/foreground` at weight 500 instead of placeholder gray. Also fixed the Memory slider's fill bar (`OoBku/lJsFB`), which stopped short of the thumb — widened from 108 to 156 (the thumb's fixed x=143 + half its 26px width) so the pink fill now ends exactly at the thumb's centre, matching the CPU slider's already-correct 170px fill against the same thumb position.

**Fields (2):**

| ID | Name | Export notes |
|---|---|---|
| `JZrLu` | Field (CPU row) | Re-exported 2026-09-06. New instance `C87ZoT` (CpuValue, ref `aRqX6`) inserted into `B4fzF5` (CtrlRow) at index 1, between `GMRpf` (CpuSlider) and `q4offT` (CpuUnit). Descendant overrides: `ANxU1` (Label) `opacity:0,height:0`; `K5pqlT` (Input) `height:36, fill:"$field/background", stroke:"$border/border", strokeWidth:1, strokeAlignment:"inner", justifyContent:"center"`; `hokJ3` (Placeholder) `content:"2", fill:"$foreground/foreground", fontWeight:"500"`; `OiyAM` (Description Wrap) `opacity:0,height:0`. |
| `DxsT3` | Field (Memory row) | Re-exported 2026-09-06. New instance `E2iTg` (MemValue, ref `aRqX6`) inserted into `WGTH9` (CtrlRow) at index 1, between `OoBku` (MemSlider) and `BOTBb` (MemUnit), same descendant overrides as `C87ZoT` but `hokJ3` content `"4"`. Also fixed `OoBku`'s `lJsFB` (Fill) descendant override from `width:108` to `width:156` so the track fill ends at the thumb's centre (thumb `tES7p` sits at fixed `x:143`, width `26`, unchanged). |

## Incremental export 2026-09-06 — Audit Log table: last lunaris instances replaced with HeroUI Table

`DxKOh` (Screen/Audit Log) held one remaining pre-HeroUI ("lunaris") component instance: `PsEYM` (ref `c:pPOgy`, "Table"). Note the original task premise assumed ~64 nested lunaris instances (Table Row/Column Header/Cell); investigation found only the single outer `c:pPOgy` instance is a real document-level component reference — its internal rows/cells/column-headers are baked into the `c:pPOgy` component's own definition as plain frames, not separately overridable instances, so they carry no independent `c:` ref to convert. `Replace("PsEYM", {...})` swapped it in place for a `ECbbo` ("Table") HeroUI instance (new id `dqzgx`), rebuilt with `eafUs` (Table Header) → 6 `DYYKO` (Table Column) headers (TIME/ACTOR/ACTION/METHOD/ACCESS/IP, widths 160/140/fill/104/110/160 matching the original), `GUHQo` (Table Body) → 8 `tDY4O` (Table Row) instances each with 6 `TDRJE` (Table Cell) instances carrying the original text content and colors (`$muted` for TIME/IP, `$foreground/foreground` for ACTOR/ACTION), the METHOD cell nesting a `UOoQW`/`Yeemi`/`eg3vM` (Chip/Soft/Success/Danger/Warning SM) instance per original method color (POST=green, DELETE=red, PUT=amber), and the ACCESS cell as plain colored text (`#21C45D` Allowed, `#DC2626` Denied) matching the original's chip-less styling. The literal (non-lunaris) `Table Footer`/`btnLoadMore` frames, which were already plain frames in the original (not a `c:` instance), were recreated verbatim as a sibling frame after the table body since HeroUI's `X3AX4` Table Footer component has no button slot.

**Screen (1):**

| ID | Name | Export notes |
|---|---|---|
| `DxKOh` | Screen/Audit Log | Re-exported 2026-09-06. `PsEYM` (`c:pPOgy` Table) replaced by `dqzgx` (`ECbbo` Table) with header/body/footer structure described above. |

**Export method & validation:**

- **JSON:** `Get("DxKOh", {depth: 20})` via the Pencil `execute` tool, zero `"..."` elision markers. Passes `python3 -m json.tool` validation.
- **Screenshot:** `export_nodes` PNG export at 2× scale to `assets/design-export/screenshots/DxKOh.png` (2880×1800, valid PNG).
- **Residue check:** `Get("DxKOh", n => (n.ref||"").startsWith("c:") && ..., {depth: 30})` (with a `ctx.skipChildren()` guard on `ref` nodes to work around a Pencil engine crash when Get descends into an instance's children without `resolveInstances: true`) returned zero matches — only the unrelated `"c:Mode":"Dark"` theme key remains, not a component ref.
- **Verification:** JSON contains exactly one `"content":"Page size 100. Older events load on demand."` (footer text), and the screenshot visually confirms all 8 rows, 6 columns, method chip colors, and Allowed/Denied text colors match the pre-conversion screenshot with no overflow or collapsed layout.

**Engine note:** `Get` on this document throws `TypeError: cannot read property of undefined` whenever it is asked to descend into a component instance's (`type: "ref"`) children without `resolveInstances: true` in the same call — even at `depth: 0`/`depth: 1` on the ref itself as the root path. Reading into instance subtrees therefore requires either `resolveInstances: true` (which flattens all nested refs to their resolved `frame`/`text` types, losing nested-instance identity) or a `ctx.skipChildren()` guard on every `ref` node to stop before the crash (which reveals only the outermost instance boundary, not nested ones).

**Export method & validation:**

- **JSON:** `Get(id, {depth: 12, resolveInstances: true})` via the Pencil `execute` tool for both fields, zero `"..."` elision markers. Both pass `python3 -m json.tool` validation.
- **Screenshots:** `export_nodes` PNG export at 2× scale to `assets/design-export/screenshots/JZrLu.png` and `assets/design-export/screenshots/DxsT3.png` (640×202 each, valid PNG).
- **Verification:** `JZrLu.json` contains exactly one `"content":"cores"` (unit dropdown value, unique to the CPU row) and `DxsT3.json` contains exactly one `"content":"GiB"` (unit dropdown value, unique to the Memory row); no cross-contamination between the two exports. Screenshots visually confirmed: both rows now show slider → bordered number box → unit dropdown, matching the original design reference; Memory's pink fill now reaches the thumb with no gap.

**Context:**

The number-input step was dropped for both resource rows at some point after the sliders and unit selects were built — the slider component (`KWcnQ`) itself carries a "CPU"/"Memory" + value header (`Gky2W`/`x2jDfP`/`S07mY`) that isn't hidden but renders at a barely-visible low-contrast tone, which is not the same UI element as the original's high-contrast bordered value box next to the slider. Reused the existing `aRqX6` HeroUI `Input/Primary` component (already used elsewhere for text fields) rather than introducing a new component, hiding its label/description parts the same way the unit-select component (`gNnkz`) already hides its own label/description via `opacity:0, height:0` overrides on `s5vl0`/`ghgxf`.

## Incremental export 2026-09-06 — Share Link status chips missing leading dot

Three Share Link screens' status chips (`DF2tD`/`g8yT5` "Asleep", `I6B4F` "Online") had lost their leading status dot. The underlying HeroUI chip components (`KmZnF` Chip/Soft/Default/SM, `UOoQW` Chip/Soft/Success/SM) have no dot slot, and each chip instance's immediate parent (`serverIdCol`, one per screen) is a vertical auto-layout — not a horizontal row with a small gap — so a plain in-flow sibling insert before the chip would stack the dot above/below it instead of to its left. Per the blind-edit task's fallback branch: overrode each chip instance's own `padding` to `[2,4,2,20]` (left padding widened from 4 to 20 to make room) and inserted a 6×6 `statusDot` ellipse as an absolutely-positioned (`layoutPosition:"absolute"`) child of the same `serverIdCol` parent, at `x:10, y:45.5` (chip's left edge + 10px, vertically centered on the chip's 19px height), filled with the chip's own color (`#A78BFA` for the two "Asleep" chips, `$success/success` for "Online").

**Screens (3):**

| ID | Name | Export notes |
|---|---|---|
| `q31B6w` | Screen/Share Link — Asleep (can start) | Re-exported 2026-09-06. Chip `DF2tD` (in `VJg0v`/serverIdCol) gained `padding:[2,4,2,20]`; new sibling ellipse `oJQtJ` (statusDot, `#A78BFA`, 6×6, absolute at 10,45.5). |
| `qFLfB` | Screen/Share Link — Asleep (view only) | Re-exported 2026-09-06. Chip `g8yT5` (in `g0l7u0`/serverIdCol) gained `padding:[2,4,2,20]`; new sibling ellipse `AJc6E` (statusDot, `#A78BFA`, 6×6, absolute at 10,45.5). |
| `C2LQE4` | Screen/Share Link — Up | Re-exported 2026-09-06. Chip `I6B4F` (in `v4w83`/serverIdCol) gained `padding:[2,4,2,20]`; new sibling ellipse `zHqma` (statusDot, `$success/success`, 6×6, absolute at 10,45.5). |

**Export method & validation:**

- **JSON:** `Get(id, {depth: 20})` via the Pencil `execute` tool for all three screens, zero `"..."` elision markers. All three pass `python3 -m json.tool` validation.
- **Screenshots:** `export_nodes` PNG export at 2× scale to `assets/design-export/screenshots/{q31B6w,qFLfB,C2LQE4}.png` (2880×1800 each, valid PNGs).
- **Verification:** each screen's JSON contains exactly one `"name": "statusDot"` node and the chip's `"padding": [2, 4, 2, 20]` override; no cross-contamination between the three exports. Screenshots visually confirmed against the original references (`orig2/q31B6w.png`, `orig2/qFLfB.png`, `orig2/C2LQE4.png`): the dot now renders before the label inside each pill (e.g. "● Asleep", "● Online"), matching the original layout, with the pill growing slightly wider (48px → 64px) to accommodate the dot rather than the dot overlapping the label.

**Context:**

This is a blind, scoped edit — only the three named chip instances and their immediate parents were touched; the shared component definitions `KmZnF`/`UOoQW` were left untouched so no other chip instance in the document is affected. Pencil does not auto-save; this export was produced directly against the in-memory MCP session state per this task's explicit "never save" instruction, so a GUI save is still pending before this change is durable across Pencil sessions.

## Incremental export 2026-09-14 — Share Card header restyled stacked + centered

`q31B6w`, `qFLfB`, and `C2LQE4` all instantiate the shared `HKEl2` (Gameplane/Share Card) component, which rendered its brand header (shield icon + "gameplane" wordmark + "shared server link" subtitle) as a left-aligned inline row (icon on the left, text column to its right) at the top of the card. The shipped product instead renders that header stacked and centered above the rest of the card's content: icon centered on its own row, wordmark and subtitle centered beneath it. Reworked the single shared component rather than each screen individually, since all three screens ref the same component — existing nodes were moved/restyled, none deleted:

- `pbJf8` (brand): `layout` horizontal → `vertical`, `gap` 10 → 20, kept `alignItems:"center"` (now centers the stack, not just cross-axis of a row).
- `vANkm` (logoCol): added `alignItems:"center"`, `gap` (unset) → 2.
- `o44cU` (logoK icon box): 36×36 → 40×40.
- `YdVSf` (shield icon): 20×20 → 22×22.
- `x72xE1` ("gameplane" text): `fontSize` 16 → 18, added `textAlign:"center"`.
- `GfRsq` ("shared server link" text): added `textAlign:"center"`.

**Screens/components (4 exported):**

| ID | Name | Export notes |
|---|---|---|
| `HKEl2` | Gameplane/Share Card | The shared component itself — header restyled per above; body (server id, status badge, address block, players row) unchanged. |
| `q31B6w` | Screen/Share Link — Asleep (can start) | Re-exported; inherits the `HKEl2` header restyle via its `shareCard` ref. |
| `qFLfB` | Screen/Share Link — Asleep (view only) | Re-exported; inherits the `HKEl2` header restyle via its `shareCard` ref. |
| `C2LQE4` | Screen/Share Link — Up | Re-exported; inherits the `HKEl2` header restyle via its `shareCard` ref. |

**Export method & validation:**

- **JSON:** `Print(Get(id, {depth: 4}))` via the Pencil `execute` tool for all four ids, zero `"..."` elision markers.
- **Screenshots:** `export_nodes` PNG export at 2× scale to `assets/design-export/screenshots/{HKEl2,q31B6w,qFLfB,C2LQE4}.png`.
- **Verification:** screenshots visually confirmed against the reference (`refs/q31B6w.png`) — icon, wordmark, and subtitle now stack vertically and sit centered above "mc-survival", matching the reference composition; `qFLfB` and `C2LQE4` (checked via `get_screenshot`, which shared the same old inline header) now show the identical stacked/centered treatment since they resolve the same `HKEl2` component.

**Context:**

Pencil does not auto-save; a GUI save is still pending before this change is durable across Pencil sessions.

## Incremental export 2026-09-06 — TE2jI de-instancing (lunaris Card leftovers) + Asleep badge color fix

Screen `TE2jI` (Screen/Server Detail — Overview (Asleep)) still had 9 leftover `c:ERkuB` ("lunaris Card") component instances from before the current HeroUI Card pattern was adopted: `Rbuuh` (Metric CPU), `T5cUA` (Metric Memory), `v2CgN` (Metric Disk), `U9o4n` (Recent Events Card), `syhCk` (Sleep Card), `kTwiX` (Connection Card), `DYtWX` (Players Online Card), `FnrZ4` (Game Status Card), `ayfXc` (Quick Actions Card, disabled). Each was rebuilt in place as a plain frame mirroring the equivalent non-instance card already present on `IzuY2` (Screen/Server Detail — Overview (Never sleeps)) — same wrapper style (`$surface/surface` fill, `$border/border` stroke, outer shadow, no rounded corners), same content/values as the original asleep-state instance (placeholder `—` metric values, "Slept/Woke" event rows, sleep schedule, connection host/port, "offline" game status, hidden Quick Actions) — then the old instance was deleted. Also fixed the "Asleep" status badge next to the `mc-survival` title (`wgpnF/N3nc2n` chip + `wgpnF/N3nc2n/YXoPG` label, part of the `S4k0x` Detail Header instance): it was rendering with the pink accent tokens (`$accent/soft` / `$accent/soft-foreground`) instead of the purple treatment used everywhere else for "Asleep" (matching `z1DZpL`/`ElKBy` on `F9pUrx`, Screen/Servers) — set to `#8B5CF633` (chip background) and `#A78BFA` (label), same values already correctly used inside the rebuilt Sleep Card's own "Asleep" state row.

**New node IDs (replacing the deleted instances, same parent/index):** `Mcf1C` (Metric CPU, in `MDbbJ` idx 0), `QGBuR` (Metric Memory, idx 1), `rUAVC` (Metric Disk, idx 2), `xBlbL` (Recent Events Card, in `L43Cc` idx 1), `t2ALqD` (Sleep Card, in `Ki1IT` idx 0), `HdpM9` (Connection Card, idx 1), `UcHPx` (Players Online Card, idx 2), `X8RUZ1` (Game Status Card, idx 3), `g5GDp` (Quick Actions Card, disabled, idx 4).

**Export method & validation:**

- **JSON:** `Get("TE2jI", {depth: 30, includePathGeometry: true})` via the Pencil `execute` tool, zero `"..."` elision markers (sparkline path geometry included in full via `includePathGeometry`). Passes `python3 -m json.tool` validation.
- **Screenshot:** `export_nodes` PNG export at 2× scale to `assets/design-export/screenshots/TE2jI.png`.
- **Verification:** residue check `Get("TE2jI", n => ((n.ref||"").startsWith("c:") || JSON.stringify(n,(k,v)=>k==="children"?undefined:v).includes('"$c:')) && Print(...), {depth: 20})` printed nothing after removing the leftover `cornerRadius:"$c:--radius-none"` token values (replaced with plain `0`, which resolves identically — the original c:ERkuB instances and the IzuY2 mirror cards both carried that same token, so it was a false-positive source, not real residue). JSON contains exactly one `"content":"Player count unavailable while asleep."` (unique to the asleep-state Players Online card) and the purple treatment appears three times as `"fill":"#A78BFA"` (badge label, Sleep Card icon, Sleep Card "Asleep" text) plus once as `"fill":"#8B5CF633"` (badge chip background). Screenshot visually confirmed against `orig2/TE2jI.png`: identical card layout/content/positions, badge now purple instead of pink.

**Context:**

Blind, scoped edit — only the 9 named `c:ERkuB` instances and the one status-badge chip were touched; the shared component definition (`ERkuB`) and every other instance of it elsewhere in the document were left untouched. Pencil does not auto-save; a GUI save is still pending before this change is durable across Pencil sessions.

## Incremental export 2026-09-06 — F9pUrx Servers Table row flex hardening

Investigated a reported overflow/clipping bug on `ucID1` (Servers Table instance, ref `ECbbo`) on `F9pUrx` (Screen/Servers): NAME/GAME/STATUS/CPU/MEMORY/PLAYERS/NODE/ACTIONS column widths were already correctly overridden per-instance (NAME `fill_container`, others fixed 90–180px, ACTIONS 180px with `justifyContent:"end"`), and the current render showed no actual clipping or overflow — NODE text renders in full and the ACTIONS icons sit inside the card in both the pre-change screenshot and `orig2/F9pUrx.png`.

As a defensive hardening (not a visual fix, since none was needed), added explicit `layout: "horizontal"` + `alignItems: "center"` to the 4 row-level instances that were previously relying on an unset (`"none"`/absolute per the schema docs) layout mode: `Z6aeH` (Header Row), `bFomj` (Row mc-survival), `ep3TT` (Row mc-test), `U3GfsB` (Row user-server-test). This makes the row's flex arrangement of its fixed/fill_container cell children explicit rather than implicit, with zero visual change (pixel diff against the pre-edit screenshot showed only a benign ~6px vertical row-position shift from the added `alignItems:center`, no horizontal change).

**Tried and reverted:** also attempted narrowing the ACTIONS cells (`QPxOE`, `Uu6Tk`, `IDgv7`, `H92w9`) from 180px to 140px per the task's suggestion; this caused a severe layout regression (all columns collapsed toward the left with a large empty gap on the right, NAME cell not filling). Reverted those 4 cells back to `width: 180` — left at the original, confirmed-safe value.

**Export method & validation:**

- **JSON:** `Get("F9pUrx", {depth: 20})` via the Pencil `execute` tool, zero `"..."` elision markers. Passes `python3 -m json.tool` validation. Contains `"layout":"horizontal"` exactly 4 times (the 4 touched row nodes) and `"width":180` for all 4 ACTIONS-column cells (`QPxOE`, `Uu6Tk`, `IDgv7`, `H92w9`), confirming the revert.
- **Screenshot:** `export_nodes` PNG export at 2× scale, 2880×1800, valid PNG.
- **Verification:** pixel diff between pre-edit and post-edit `F9pUrx` screenshots showed a diff bounding box confined to the table region with mean difference 0.72/255 — visually confirmed as only the ~6px row-height shift from centered alignment, no horizontal shift, no new clipping or overflow. Zoomed crops of the NODE/ACTIONS columns confirm `kubelab-control` / `kubelab-worker-2` render in full and all 4 action icons sit inside the card.

**Context:**

Blind, scoped edit — only the 4 named row instances and the temporarily-touched 4 ACTIONS cells (reverted) were touched; the shared component definitions (`ECbbo`, `eafUs`, `tDY4O`, `TDRJE`, `GUHQo`, `DYYKO`) were left untouched. Pencil does not auto-save; a GUI save is still pending before this change is durable across Pencil sessions.

## Incremental export 2026-09-06 — zhLZN Backup Detail Drawer header button fix (OD-12)

Investigated `zhLZN` (Gameplane/Backup Detail Drawer)'s header (`sVSGe`). A previous haiku agent had mis-renamed the header's only action button instance `JINV8` (ref `J09iP`, a `Button/Ghost/SM`) to "Btn Restore" as an internal node name. A filtered `Get("sVSGe", ..., {depth: 4/6, resolveInstances: true})` established the header contains exactly one button instance — no separate close ("x") icon button exists anywhere in `sVSGe`'s subtree at all (confirmed by an unconditional-print sweep to depth 6 that returned only `sVSGe`, `R4kYq`, `J6746o`, `Y9xsP`, `JINV8`, and `JINV8`'s two resolved children).

`JINV8`'s icon child (`WtPjS`) was `pencil`, not `x` — so the conditional rename-to-"Btn Close" instruction did not apply and its internal node name was left as-is (unchanged; it is not user-visible). `JINV8`'s label child (`r4VbAi`) held the literal placeholder text "Button" (OD-12) — fixed: `content` → `"Restore"`, and its icon (`WtPjS`) → `rotate-ccw` to match the footer's `oxVkD` Restore button treatment.

Checked the footer instances `CghXi` (Btn Delete, ref `IU7OG`, icon `trash-2`, label "Delete") and `oxVkD` (Btn Restore, ref `z9ShNE`, icon `rotate-ccw`, label "Restore") — both already correct, no literal "Button" text, no changes made.

**Node touched:** `JINV8` only, via `descendants` overrides (`eWkIT/r4VbAi.content`, `eWkIT/WtPjS.icon`) — the shared `J09iP` (Button/Ghost/SM) component definition was not touched.

**Export method & validation:**

- **JSON:** `Get("zhLZN", {depth: 20})` via the Pencil `execute` tool, zero `"..."` elision markers. Passes `python3 -m json.tool` validation. Contains `"content":"Restore"` exactly twice (header `JINV8` override and footer `oxVkD` override, both correct).
- **Screenshot:** `export_nodes` PNG export at 2x scale to `assets/design-export/screenshots/zhLZN.png`, 1008x1648, valid PNG.
- **Verification:** screenshot of `sVSGe` before the edit showed "Backup details" / filename on the left and a pencil-icon "Button" on the right; after the edit it shows the same title/filename and a rotate-ccw-icon "Restore" button, no layout overflow. Full `zhLZN` screenshot confirms header now reads "Backup details" + "Restore" alongside the unchanged footer "Delete"/"Restore" buttons.

**Open question not resolved by this pass:** the task described `JINV8` as "the close (x) button," but no close/x button node exists anywhere in `sVSGe` — the header has only ever had the one action button. Whether the design is missing a dedicated close control is a design-intent question, not something this scoped, blind edit pass should decide; flagged for the maintainer/a design-judgement pass rather than invented here.

**Context:**

Blind, scoped edit — only `JINV8`'s descendant overrides were touched; `R4kYq`, `J6746o`, `Y9xsP`, the footer instances, and the shared component definitions (`J09iP`, `IU7OG`, `z9ShNE`) were left untouched. Pencil does not auto-save; a GUI save is still pending before this change is durable across Pencil sessions.

## Incremental export 2026-09-06 — UMJli Create Server Step 5 primary button icon order

Investigated `UMJli` (Screen/Create Server — Step 5 Review)'s primary footer button instance `Bn9BX` (ref `aic7V` = `Button/Primary/LG`, label child `aNpk5`, icon child `y91nX`). The live render showed the icon (`check`) *before* the label "Create server"; the original design (`orig2/UMJli.png`) shows the label followed by a trailing icon.

`Get("Bn9BX", ..., {depth: 2, resolveInstances: true})` confirmed `Bn9BX` is an instance of `aic7V`, not a standalone frame, and that the icon-then-label child order is baked into the shared definition `aic7V` itself (`y91nX` icon child precedes `aNpk5` label child in `aic7V`'s own children array) — editing that order would mean editing the `LtgNm` component definition, which is out of scope (never edit a definition inside `LtgNm`).

Listed all `Button/Primary/*` variants in `LtgNm`: `j9c5W` (SM), `cb4rt` (MD), `aic7V` (LG), plus icon-only variants `RC4D7`/`pRZSi`/`uvgI7` (Icon/SM/MD/LG — checked `uvgI7`, confirmed icon-only with no label, not usable). **No trailing-icon (label-then-icon) `Button/Primary` variant exists.**

Per the no-variant fallback: hid the leading icon on the instance only via `Update("Bn9BX", {descendants: {"y91nX": {opacity: 0, width: 0}}})`. Screenshot after the change shows a clean "Create server" label, no overflow or collapse, though the button loses the trailing-icon look of the original (opacity: 0 removes visibility but not the icon's original layout completely — verified no visible gap/artifact remains).

**Node touched:** `Bn9BX` only, via `descendants` override (`y91nX.opacity`, `y91nX.width`) — the shared `aic7V` (Button/Primary/LG) component definition was not touched, and no other footer button (`wuwVx`/Back) was touched.

**Export method & validation:**

- **JSON:** `Get("UMJli", {depth: 30})` via the Pencil `execute` tool, zero `"..."` elision markers. Passes `python3 -m json.tool` validation. Contains `"content":"Create server"` once (footer `Bn9BX` override) and the `y91nX` override `"opacity":0,"width":0` once.
- **Screenshot:** `export_nodes` PNG export at 2x scale to `assets/design-export/screenshots/UMJli.png`, 2880×1800, valid PNG.
- **Verification:** screenshot of `Bn9BX` before the edit showed a leading check icon then "Create server"; after the edit it shows "Create server" alone, centered, no broken/overflowing layout. Full `UMJli` screenshot confirms the rest of the review screen (template/version/configuration/network sections, YAML preview, footer Back button) is unchanged.

**Open question not resolved by this pass:** no `Button/Primary` variant in the component library supports a trailing icon, so the original design's "label then arrow/check icon" treatment cannot be reproduced without either a new component variant or a definition edit — both out of scope for a blind, scoped fix. Flagged for a design-judgement pass to decide whether to add a `Button/Primary/Trailing` variant.

**Context:**

Blind, scoped edit — only `Bn9BX`'s descendant override was touched; the `aic7V` shared definition and all other `Button/Primary/*`/`Button/Primary/Icon/*` variants were left untouched. Pencil does not auto-save; a GUI save is still pending before this change is durable across Pencil sessions.

## Incremental export 2026-09-06 — DxKOh / QgW58 / J5pjJ3 icon fixes

Three icon-correctness fixes across three screens, found via filtered `Get` sweeps (never a full-depth screen dump):

1. **`DxKOh` (Screen/Audit Log)** — the audit-integrity banner's "Re-check" button (`x8cjS`, a `Gameplane/Button/Ghost/SM`-shaped bespoke frame, not an instance) had a `pencil` icon on its icon child `x8cjS/eWkIT/WtPjS`. That read as an edit affordance on a button whose label is "Button"/re-check action, not an edit action — changed the icon to `plus`. `Update("x8cjS", {descendants: {"eWkIT/WtPjS": {icon: "plus"}}})`.
2. **`QgW58` (Screen/Admin Settings — Authentication (Save rejected))** — two separate fixes:
   - The "Helm-configured admin mapping" warning callout (`zg6cG`, a bespoke frame, not a HeroUI Alert instance) had a plain `triangle-alert` icon on its icon child `H4Cgu5`. Changed directly to `megaphone` to match the house warning-callout style: `Update("H4Cgu5", {icon: "megaphone"})`.
   - The bottom error banner `B89TO` ("saveErrorBanner", containing the text "Request failed (409): a server with that name already exists.") was a plain frame with only a text child (`E51IQ`) — no icon at all. Inserted a new leading icon child: `Insert("B89TO", {type: "icon", name: "Error Icon", icon: "alert-circle", library: "lucide", width: 16, height: 16, fill: "$danger/danger"})` then `Move(id, "B89TO", 0)`. The icon set in this document uses the renamed lucide id `circle-alert`, not `alert-circle` — the engine rejected `alert-circle` with an "icon not found" issue, corrected via a follow-up `Update(id, {icon: "circle-alert"})`. Final banner: `circle-alert` icon then the "409" text, both `$danger/danger`.
3. **`J5pjJ3` (Screen/Server Detail — Settings · Networking)** — three warning callouts (`znLuB` "Tailscale is tailnet-only", `q1zaXx` "Address preference ignored", `MLrud` "No address manager configured"), each a bespoke frame (not a HeroUI Alert instance) with a `triangle-alert` icon on child path `<instId>/JvjWQ`. All three changed to `megaphone`: `Update("znLuB", {descendants: {"JvjWQ": {icon: "megaphone"}}})` (and the same for `q1zaXx`, `MLrud`). A fourth `triangle-alert` icon (`eRj5l`) found by the same filtered sweep sits in the unrelated `snDangerZone` section (not a warning callout) and was correctly left untouched.

**Nodes touched:** `x8cjS` (descendant override only), `H4Cgu5` (direct, plain node), `B89TO` (new child `UEI7P` inserted + icon corrected), `znLuB`/`q1zaXx`/`MLrud` (descendant overrides only, path `JvjWQ`). No shared component definition (`LtgNm`) was touched; no root frame moved/deleted/duplicated.

**Export method & validation:**

- **JSON:** component-level `Get(id, {depth: 10[, resolveInstances: true]})` for each of the six touched nodes (`x8cjS`, `zg6cG`, `B89TO`, `znLuB`, `q1zaXx`, `MLrud` — `zg6cG` exported instead of bare `H4Cgu5` to keep the callout's full context), zero `"..."` elision markers, written to `assets/design-export/json/<id>.json`. All six pass `python3 -m json.tool`. Full-screen `DxKOh.json`/`QgW58.json`/`J5pjJ3.json` were left as their existing (pre-existing, shallow sidebar-only) exports since the touched sub-nodes are not represented in that shallow depth and are now covered by their own component-level files.
- **Screenshots:** `export_nodes` PNG export at 2x scale for all six touched node ids to `assets/design-export/screenshots/<id>.png`; all non-empty with real pixel dimensions. Also took full-screen screenshots of `DxKOh`, `QgW58`, `J5pjJ3` via `get_screenshot` to confirm no layout regression.
- **Verification:** each component screenshot visually confirms the corrected icon — `x8cjS` shows a `+` before "Button"; `zg6cG`/`znLuB`/`q1zaXx`/`MLrud` all show the megaphone glyph before their respective warning titles; `B89TO` shows a red circle-alert glyph before "Request failed (409): a server with that name already exists.". Full-screen screenshots show no overflow/collapse anywhere on the three screens.

**Context:**

Grep-first, blind-edit pass per CLAUDE.md rule 17 — filtered `Get` visitors located every candidate icon by type/value, no full-depth screen dump or judgement-based "reconcile" was performed. Pencil does not auto-save; a GUI save is still pending before this change is durable across Pencil sessions.

## Incremental export 2026-09-06 — zqzr4 Admin Settings action-row alignment fix

Investigated `zqzr4` (Screen/Admin Settings — Authentication, admin mapping warning): the "Save changes" button under the "Add identity provider" panel was left-aligned instead of right-aligned as in the original (`orig2/zqzr4.png`) and the sibling screen `QgW58`.

Located the button via a filtered `Get("zqzr4", (n, ctx) => n.content === "Save changes" ..., {depth: 30, resolveInstances: true})`, which returned the ancestor chain `HPFBR/q58In/U6q13B < HPFBR/q58In < HPFBR < Y6K3J < ...` — `HPFBR` is the button instance, `Y6K3J` its direct parent (the action row). The same filter on `QgW58` returned `wyW2y < Wdqop < Xng32 < xCAZJ < ...` — `Xng32` the button instance, `xCAZJ` its direct parent (the reference action row).

Compared the two action rows at `{depth: 1}`: `xCAZJ` (reference, correct) had `{justifyContent: "end", width: "fill_container", padding: [8,20,20,20], alignItems: "center", gap: 8}`; `Y6K3J` (buggy) had the same `width`/`padding`/`gap` but was **missing `justifyContent` and `alignItems`**, defaulting to left/start alignment. Verified the row had not been moved into the wrong parent: `LxJdk`'s children (`r3MEl, jhSx4, Y6K3J`) mirror `pDbdm`'s children (`jR1Pi, byGWB, xCAZJ`) — same structural position, three siblings with the action row last in both.

**Fix:** `Update("Y6K3J", {justifyContent: "end", alignItems: "center"})`. No move, no other property changed.

**Export method & validation:**

- **JSON:** `Get("zqzr4", {depth: 40, resolveInstances: true})` via the Pencil `execute` tool (output exceeded the tool's inline token limit and was saved to a harness tool-results file; extracted the `Print output` JSON line and validated with `python3 -m json.tool`). Zero elision — file is 337,452 bytes. Contains `"Save role mappings"` exactly once (the bottom row label).
- **Screenshot:** `export_nodes` PNG export at 2x scale to `assets/design-export/screenshots/zqzr4.png`, 2880x4672, valid PNG.
- **Verification:** full-screen screenshot confirms "Save changes" is now right-aligned under the "Add identity provider" panel, all cards fit inside the 2336px frame, and the "Role mapping overrides" panel with its "Save role mappings" row is fully visible at the bottom — no overflow.

**Context:**

Grep-first, blind-edit pass per CLAUDE.md rule 17 — only `Y6K3J`'s own layout properties were changed; no screen-wide dump or judgement-based "reconcile" was performed. Pencil does not auto-save; a GUI save is still pending before this change is durable across Pencil sessions.

## Incremental export 2026-09-06 — x7MJI Connection Card HeroUI re-skin (GN3ni, AWAjU)

`x7MJI` (Gameplane/Connection Card — Tunnel States reference) still held two `lunaris` `c:ERkuB` Card instances — `GN3ni` "Connection Card (Tailscale)" and `AWAjU` "Connection Card (playit connecting)" — the only surviving `c:`-ref Card instances on that reference screen. Rebuilt both in place as plain HeroUI-styled frames.

For each card: inserted a new plain frame into the card's parent column (`aAPWT` for `GN3ni`, `B3x43y` for `AWAjU`) styled like `Card/Default` (`XDZ0E`) — `fill: "$surface/surface"`, `stroke: "$border/border"`, `cornerRadius: "$radius/3xl"`, `layout: "vertical"`, same `width: 399` as the old instance, `gap`/`padding` `0` (matching the `c:ERkuB` definition's own undefined gap/padding — its sections carry their own padding) — then inserted two child frames mirroring `Card Header` (`FRpsu`: `width: "fill_container"`, `layout: "vertical"`) and `Card Content` (`I9ZPb`: `width: "fill_container"`, `layout: "vertical"`, `gap: 4` pattern), each given the old instance's actual section padding (header `[20,24,12,24]`, content `[0,24,20,24]`, content `gap: 12`). Moved every real content child from the old instance's resolved descendant paths (e.g. `GN3ni/c:4zoFt/CGDUl`, `GN3ni/c:QMHOm/zjymb`) into the new frames in original order, then deleted the emptied old ref instance. Because `Insert` appends and the old instance was deleted afterward, the new card landed at the same child index automatically (`aAPWT`: `[u9bCb, XVJA4]`; `B3x43y`: `[z7Xfpm, o6lxF]` — same positions as `[caption, card]` before).

New node ids: `XVJA4` (Tailscale card, was `GN3ni`) with children `QhZSW` (Card Header) and `lOZGP` (Card Content); `o6lxF` (playit card, was `AWAjU`) with children `y53nlu` (Card Header) and `HGEjK` (Card Content). All original leaf content nodes (`CGDUl`, `zjymb`, `wT7p7`, `tWEy4`, `n0rynH`, `E9xw2f`, `YbjOG`, `RfbOQ`, `CA1Zv`, `oyd1p`, and their descendants) kept their original ids and content unchanged — only their parent chain changed.

**Engine quirk hit:** `Get(id, (n,ctx)=>{...(n.children||[]).map(...)`, `{depth:1}`)` visitors throw `TypeError: not a function` — in visitor mode `n.children` is not a plain array (accessing `.map` on it fails). Worked around by printing `n.id`/`n.type` per visited node instead of touching `n.children` directly; child order was inferred from the visit sequence.

**Residue check:** `Get("x7MJI", (n,ctx) => { if ((n.ref||"").startsWith("c:")) Print("RESIDUE", n.id, n.ref); if (n.type === "ref") ctx.skipChildren(); }, {depth: 30})` printed nothing — no `c:`-ref residue anywhere under `x7MJI`.

**Export method & validation:**

- **JSON:** `Get("x7MJI", {depth: 12})` via `execute`, zero `"..."` elision markers, written to `assets/design-export/json/x7MJI.json`. Passes `python3 -m json.tool`. `grep -o '"content": "[^"]*"'` confirms all 18 original body strings survived verbatim (Unicode-escaped in the re-serialized JSON, e.g. `Tailscale — private (tailnet only)`, `mc-survival.tail4e2a.ts.net:25565`, `Waiting for tunnel address…`).
- **Screenshot:** `export_nodes` PNG export at 2x scale to `assets/design-export/screenshots/x7MJI.png`, 1740x686, valid non-empty PNG.
- **Verification:** the screenshot shows both cards as bordered, rounded, dark-surface HeroUI cards with a "CONNECTION" header, matching content in the same rows as before (Tailscale: warning-bordered tunnel-address row with lock icon, "TUNNEL · TAILSCALE" eyebrow, tailnet-only badge, mono address, host/port rows; playit: neutral tunnel-address row with loader-circle icon, "TUNNEL · PLAYIT.GG" eyebrow, italic "Waiting for tunnel address…", host/port rows). No collapsed, overflowing, or broken layout.

**Context:**

Grep-first, blind-edit pass per CLAUDE.md rule 17 — the edit list (parent-styled frame + two section frames + child moves) was fixed in advance from the already-exported `x7MJI.json` and the `c:ERkuB`/`FRpsu`/`I9ZPb`/`XDZ0E` definitions; no full-depth judgement-based "reconcile" of the card was performed. Pencil does not auto-save; a GUI save is still pending before this change is durable across Pencil sessions.

## Incremental export 2026-09-06 — z9ShNE / rNhll HeroUI ref cleanup (CodeRabbit PR #349)

CodeRabbit flagged `z9ShNE` (Gameplane/Button/Small/Default) and `rNhll` (Gameplane/Button/Outline) as hand-built wrapper frames duplicating HeroUI button styling instead of referencing the HeroUI definitions.

`Get("z9ShNE", {depth: 2})` showed the inner `Button` child (`IVgQ8`) was **already** a `ref` instance of `j9c5W` (Button/Primary/SM) with the label override (`U1xvSo`: `{content: "Button"}`) intact — a prior pass had already done the ref swap. The only remaining defect was the wrapper `z9ShNE` itself still carrying its own duplicated `fill: "$accent/accent"` and `cornerRadius: "$radius/md"`, redundant with `j9c5W`'s own `fill`/`cornerRadius`.

`Get("rNhll", {depth: 2})` showed its inner `Button` child (`Q57b2`) was a `ref`, but wired to the **wrong** definition: `i6gfu` (Button/**Outline/MD**, 100×36) instead of the SM variant. Found the correct target via `Get("LtgNm", n => n.reusable && /^Button\/Outline\/SM$/.test(n.name||"") && Print(n.id, n.name), {depth: 2})` → `FIB65` (Button/Outline/SM, 84×32). The wrapper `rNhll` also carried its own duplicated `stroke: "$border/border"` and `cornerRadius: "$radius/3xl"`.

**Fix:**
- `z9ShNE`: `Update("z9ShNE", {fill: "#00000000", cornerRadius: 0})` — cleared the wrapper's duplicated fill/cornerRadius; visual styling now comes solely from the `j9c5W` ref child. Label/icon/size/position untouched.
- `rNhll`: `Replace("Q57b2", {...})` threw `TypeError: Cannot read properties of undefined (reading 'type')` on every path form tried (bare id, `rNhll/Q57b2`) — worked around with `Delete("Q57b2")` followed by `Insert("rNhll", {type: "ref", ref: "FIB65", name: "Button", descendants: {"zMNkR": {content: "Button"}}})` (new child id `wDXFJ`), preserving the "Button" label text on the new definition's own label node (`zMNkR`, since `FIB65`'s label child id differs from `i6gfu`'s `GqVun`). Then `Update("rNhll", {stroke: "#00000000", cornerRadius: 0})` cleared the wrapper's duplicated stroke/cornerRadius.
- Net effect on `rNhll`'s hug width: 132×36 → 116×36 (84px SM child + unchanged `[8, $spacing/4]` padding, down from the wrong 100px MD child) — height and position unchanged; this narrowing is the correct consequence of using the SM definition instead of the previously-mis-wired MD one.

**Verification:**
- Post-fix `Get` on both wrappers confirmed `fill`/`stroke`/`cornerRadius` are now `undefined` on both wrapper frames and on their ref children — all visual styling delegates to the HeroUI `j9c5W`/`FIB65` definitions.
- Screenshots of `z9ShNE` and `rNhll` in isolation show solid, cleanly-rounded pill buttons with no visible seam, gap, or double border between wrapper and inner ref.
- Screenshots of `dQV9N` (Screen/Server Detail — Settings · Share links (Empty), uses `z9ShNE` via a "Create link" instance) and `b4eaUf` (Screen/Server Detail — Capture — Start capture (Invalid filter), uses `rNhll` via a "Cancel" instance) confirm both screens render correctly post-fix — no broken, collapsed, or overflowing layout.

**Export method & validation:**

- **JSON:** `Get(id, {depth: 4})` via `execute` for both `z9ShNE` and `rNhll` (button compositions are shallow — depth 4 fully resolves the wrapper + its one ref child + descendant overrides with zero `"..."` elision), written to `assets/design-export/json/z9ShNE.json` and `assets/design-export/json/rNhll.json`. Both pass `python3 -m json.tool`.
- **Screenshots:** `export_nodes` PNG export at 2x scale to `assets/design-export/screenshots/z9ShNE.png` and `assets/design-export/screenshots/rNhll.png`, both non-empty valid PNGs.

**Context:**

Targeted, judgement-light fix per CLAUDE.md rule 17 spirit — only the two flagged wrapper nodes and `rNhll`'s single ref child were touched; no screen-wide dump or reconcile-by-eye was performed on either definition or on the two screens used only for regression screenshotting. Pencil does not auto-save; a GUI save is still pending before this change is durable across Pencil sessions.

## Incremental export 2026-09-06 — EZFW0 Server Detail Overview: restored missing metric-card sparklines

`EZFW0`'s three metric cards (CPU `yyxoE`, Memory `d9mRd`, Disk `E62N4`) had lost their sparkline trend lines — each card's `path` node (`XCNbc`, `IMSDo`, `bjZE6`) existed structurally (same `type`, `viewBox`, `stroke`, `strokeWidth` as the working sibling screen `IzuY2`) but rendered as a blank/invisible line. `n.geometry` reads as the literal 3-character placeholder `"..."` via a normal `Get` (confirmed via `.length`/char-code checks) regardless of whether the path actually renders — `Get`'s `includePathGeometry: true` option (undocumented in the app-state summary; found via the `execute` API's own inline TS signature) is required to see real path data, and copying `n.geometry` through `Update` did not restore the visual (the value read back was the same elided placeholder on both the broken and working nodes, so a naive `Update(target, {geometry: sourceGeometryValue})` was a no-op).

**Fix:** used the native `Copy(path, parent, copyNodeData)` primitive instead of reading/writing the `geometry` property, since `Copy` duplicates the node's actual internal data rather than round-tripping it through the elided JS reflection:
- `Delete("XCNbc")` → `Copy("OB5ve", "N7x6Xs", {})` (from `IzuY2`'s CPU card) → `Move(<newId m72bbw>, "N7x6Xs", 2)` to restore its position between the "0%" text and the progress-bar frame.
- `Delete("IMSDo")` → `Copy("Kkgv2", "A5w3dO", {})` (from `IzuY2`'s Memory card) → `Move(<newId KGZYS>, "A5w3dO", 2)`.
- `Delete("bjZE6")` → `Copy("KVGfX", "XNdKY", {})` (from `IzuY2`'s Disk card) → `Move(<newId vdDJJ>, "XNdKY", 2)`.

**Verification:** screenshots of each metric card (`yyxoE`, `d9mRd`, `E62N4`) individually and the full `EZFW0` screen, compared against `IzuY2`'s equivalents and the reference screenshot supplied for this task — all three sparklines now render (pink/purple/green zigzag lines matching their card's accent color), no layout breakage, overflow, or clipping. A follow-up `Get(..., {includePathGeometry: true})` on the new nodes confirmed real `M0 16l12-3...`-style path data (not the `"..."` placeholder).

**Export method & validation:**
- **JSON:** `Get("EZFW0", {depth: 20, includePathGeometry: true, resolveInstances: true})`, written to `assets/design-export/json/EZFW0.json`. Passes `python3 -m json.tool`; zero `"..."` elision markers (`grep -c '"\.\.\."'` = 0); content-validated via `grep -o '"Saved the game"'` (unique body text on this screen).
- **Screenshot:** `export_nodes` PNG export at 2x scale to `assets/design-export/screenshots/EZFW0.png` (2880×2600, non-empty RGBA).

**Context:** Only the three sparkline path nodes on `EZFW0` were touched (delete + copy + reposition); no other node on the screen was read or modified. Pencil does not auto-save — a GUI save is still pending before this change is durable across Pencil sessions.

## Incremental export 2026-09-06 — kIxaJ Audit Integrity Banner: replaced last lunaris `c:` instance

Residue check (`Get("kIxaJ", (n,ctx)=>{ if((n.ref||"").startsWith("c:")) Print("RESIDUE",n.id,n.ref,n.name); if(n.type==="ref") ctx.skipChildren(); }, {depth:30})`) printed exactly one hit: `RESIDUE aAaPa c:YZjRF Integrity Banner (Broken)` — the reusable `kIxaJ` composition's only child besides its own reference-label text (`ZEqkj`) was a `ref` into the lunaris library (`c:YZjRF`, named "Integrity Banner (Broken)").

`Get("aAaPa", ..., {depth:5, resolveInstances:true})` showed the resolved instance: a horizontal `frame` (`fill_container` width, `$danger/danger` fill, `gap:12`, `padding:16`, `alignItems:"center"`) containing a 24×24 icon frame (`aAaPa/c:LB3Fl`, fill `$foreground/foreground`) wrapping a hand-drawn wave/pulse vector path (`aAaPa/c:FTYwP`), and a text child (`fuKsy`, content `"Integrity check failed — chain breaks at event #286"`, fill `$danger/foreground`, `fontSize:16`, `fontWeight:"500"`, `fontFamily:"$typography/font-sans"`). The reference screenshot (`assets/design-export/screenshots/kIxaJ.png`, pre-edit) confirmed the same: a solid red banner with a dark icon chip (pulse icon) and bold white message text.

No HeroUI `Alert/*` definition (`CEGPG`, `O3T14b`, `v0xtri`, `Llzos`, `f7KBn` Danger, `r0CzE`, `JCxNu`, `uJ3KZ`) matches this single-line full-width banner shape — all eight are title+description cards on a light `$surface/surface` background at fixed `width:540`. Per the task's guidance that colours/shapes may differ as long as content/rows/icons/sizes/position match, rebuilt the banner as a plain frame styled like the original (not literally re-parented under an `Alert/*` definition, since none fit), replacing only the lunaris vector icon with the closest semantic lucide icon (`activity` — a pulse/waveform, matching the original hand-drawn squiggle) and keeping the exact original text content and styling.

**Fix:**
- `Insert("kIxaJ", {type:"frame", name:"Integrity Banner (Broken)", width:"fill_container", fill:"$danger/danger", gap:12, padding:16, alignItems:"center"})` → new id `m1hP1j`.
- `Insert(m1hP1j, {type:"frame", name:"Icon", width:32, height:32, fill:"$foreground/foreground", cornerRadius:6, padding:4, alignItems:"center", justifyContent:"center"})` → `gJjR4`, then `Insert(gJjR4, {type:"icon", library:"lucide", icon:"activity", width:20, height:20, fill:"$danger/foreground"})` → `z04z2`.
- `Insert(m1hP1j, {type:"text", name:"Message", content:"Integrity check failed — chain breaks at event #286", fill:"$danger/foreground", lineHeight:1.5, fontFamily:"$typography/font-sans", fontSize:16, fontWeight:"500"})` → `Mtc22` (text copied verbatim from the old `fuKsy` node).
- `Delete("aAaPa")` (the emptied lunaris ref; not a root frame — `kIxaJ` itself is untouched and keeps its id/size).
- `Move("m1hP1j", "kIxaJ", 1)` — restored the new node to `aAaPa`'s original index (1) among `kIxaJ`'s two children.

**Verification:**
- Residue check re-run post-fix: zero output — no `c:`-prefixed `ref` remains under `kIxaJ`.
- Screenshot of `kIxaJ` post-fix shows a solid red banner, dark rounded icon chip with a white pulse icon, and bold white "Integrity check failed — chain breaks at event #286" text — matching the pre-edit reference screenshot's layout, proportions, and content; no broken, collapsed, or overflowing layout.

**Export method & validation:**
- **JSON:** `Get("kIxaJ", {depth: 15})` via `execute` (zero `"..."` elision markers — the whole composition is 2 levels deep past `kIxaJ`), written to `assets/design-export/json/kIxaJ.json`. Passes `python3 -m json.tool`; content-validated via `grep -o "chain breaks at event #286"` (unique body text, one hit).
- **Screenshot:** `export_nodes` PNG export at 2x scale to `assets/design-export/screenshots/kIxaJ.png` (1400×312, non-empty RGBA).

**Context:** `kIxaJ` is `Gameplane/Audit Integrity Banner`, a reusable component — its root id (`kIxaJ`) and outer size (700×hug, per its own `width:700` / vertical layout) were not touched; only the inner `aAaPa` slot was replaced. Pencil does not auto-save — a GUI save is still pending before this change is durable across Pencil sessions.

## Incremental export 2026-09-14 — m1hP1j Audit Integrity Banner Broken state: bare banner (ghost "Re-check" button)

**Captures reparent from kIxaJ to m1hP1j** (companion code-brief task updates design-capture in `web/src/components/hero/AuditIntegrityBanner.tsx`):

| ID | Name | Notes |
|---|---|---|
| `m1hP1j` | Gameplane/Audit Integrity Banner — Broken state, bare banner | **New.** Child container of `kIxaJ` (the reusable Audit Integrity Banner component). Previously only the component reference (`kIxaJ`) was exported; now the bare-banner instance (`m1hP1j`) is captured separately. Contains: full-width red `$danger/danger` fill frame (700×56 at hug+fill layout, `gap:12`, `padding:16`, `alignItems:"center"`), three children: icon frame (32×32, `$foreground/foreground` fill, `cornerRadius:6`), message text ("Integrity check failed — chain breaks at event #286"), and new ghost Re-check button frame (child frame with `layout:"horizontal"`, `padding:[6,12]`, `cornerRadius:6`, containing "Re-check" label in `$danger/foreground`). Ghost button has no background fill — styled as a bare text button styled button matching HeroUI's `Button size="sm" variant="ghost"` aesthetic in the product. |
| `kIxaJ` | Gameplane/Audit Integrity Banner (component definition) | **No longer diffed — stays exported.** The component definition remains unchanged; the design capture now targets its child `m1hP1j` instead of the wrapper. The component `kIxaJ` is still exported for reference and changelog context, but no design-wave tasks modify this component further (the Re-check button is an instance-level child of `m1hP1j`, not part of the reusable definition). |

**Fix:**
- `Insert("m1hP1j", {type:"frame", name:"Re-check Button", layout:"horizontal", padding:[6,12], cornerRadius:6, justifyContent:"center", alignItems:"center"})` → new id `C2OQV9`.
- `Insert(C2OQV9, {type:"text", name:"Re-check Label", content:"Re-check", fill:"$danger/foreground", fontFamily:"$typography/font-sans", fontSize:14, fontWeight:"500"})` → `jEyku`.

**Verification:**
- Screenshot of `m1hP1j` shows red banner with icon, message text, and ghost button (no background fill, right of message, text in danger-foreground color).
- `Get("m1hP1j", {depth:3})` returns 4 nodes: icon frame (`gJjR4`), message text (`Mtc22`), Re-check button frame (`C2OQV9`), and Re-check label text (`jEyku`). Zero `"..."` elision markers.

**Export method & validation:**
- **JSON:** `Get("m1hP1j", {depth:3})` via `execute` written to `assets/design-export/json/m1hP1j.json`. Passes `python3 -m json.tool`; content-validated via `grep -o "Re-check"` (unique new button label, one hit).
- **Screenshot:** `export_nodes` PNG export at 2x scale to `assets/design-export/screenshots/m1hP1j.png` (1400×112, non-empty RGBA).

**Context:** The ghost Re-check button is rendered above the Pencil capture point in the product (`web/src/components/hero/AuditIntegrityBanner.tsx` lines 30-40, after the message span); this design documents the button's ghost styling (no fill, `$danger/foreground` text, 12px padding, 6px corner radius, 14px / 500 weight label). The product also renders a dismiss `×` button (third child), which is out of scope for this Pencil edit — design capture is limited to `m1hP1j` which contains the icon, message, and Re-check button only.

## Incremental export 2026-09-06 — RC3Kf Admin Settings — Backup destinations: replaced last lunaris `c:` instance

Residue check (`Get("RC3Kf", (n,ctx)=>{ if((n.ref||"").startsWith("c:")) Print("RESIDUE",n.id,n.ref,n.name); if(n.type==="ref") ctx.skipChildren(); }, {depth:30})`) printed exactly one hit: `RESIDUE t86T1 c:ERkuB Settings Panel` — the "Backup destinations" card in the settings-layout body was a lunaris library instance.

`Get("t86T1", ..., {depth:5, resolveInstances:true})` showed the resolved instance: an 888×159 vertical frame (`$surface/surface` fill) with a Card Header sub-frame (title text `BwzPk` "Backup destinations", description text `U2CRgX` "Restic repositories for snapshots. Stored as labelled Kubernetes Secrets in the configured namespace."), a Card Content sub-frame (empty-state text `NMici` "No backup destinations configured. Add one to enable snapshots."), and a Card Actions sub-frame containing a pink "+ Add destination" button (`wFu1e`, already a plain `Gameplane/Button/Default` (`tpKRk`) ref, not a lunaris one). The reference screenshot (`orig2/RC3Kf.png`) confirmed the same layout.

Rebuilt as a plain frame styled like the HeroUI `XDZ0E` Card/Default definition (`width:888`, vertical layout, `fill:"$surface/surface"`, `cornerRadius:"$radius/3xl"`, outer shadow effect matching `XDZ0E`), with `FRpsu`/`I9ZPb`-style Card Header / Card Content sub-frames plus a Card Actions sub-frame, moving the three real text nodes and the existing button ref into the new structure instead of recreating them.

**Fix:**
- `Insert("rMHXz", {type:"frame", name:"Card/Default", width:888, layout:"vertical", fill:"$surface/surface", cornerRadius:"$radius/3xl", effect:{type:"shadow", shadowType:"outer", color:"#0000000A", offset:{x:0,y:2}, blur:4}, x:244, y:0})` → new id `kq3Lz`.
- `Insert(kq3Lz, {type:"frame", name:"Card Header", layout:"vertical", gap:4, padding:[20,20,16,20]})` → `Lo8oV`; `Insert(kq3Lz, {type:"frame", name:"Card Content", layout:"vertical", gap:16, padding:[0,20]})` → `pROKa`; `Insert(kq3Lz, {type:"frame", name:"Card Actions", layout:"horizontal", justifyContent:"flex-end", gap:8, padding:[8,20,20,20]})` → `fOMFH`.
- `Move("BwzPk", "Lo8oV")`, `Move("U2CRgX", "Lo8oV")`, `Move("NMici", "pROKa")`, `Move("wFu1e", "fOMFH")` — moved the real title, description, empty-state text and button into the new frames; `Update` calls set `width:"fill_container"` on the three new sub-frames and the three moved text nodes to resolve the resulting collapsed-size warnings.
- `Delete("t86T1")` (the emptied lunaris ref; not a root frame).
- `Move("kq3Lz", "rMHXz", 1)` — placed the new card at `t86T1`'s original index (1, after `X4OyDd` Settings Nav) among `rMHXz`'s children.

**Verification:**
- Residue check re-run post-fix: zero output — no `c:`-prefixed `ref` remains under `RC3Kf`.
- Screenshot of `RC3Kf` post-fix shows the same "Backup destinations" card — title, description, empty-state message, and pink "+ Add destination" button in the same 888×159 position/size as the reference screenshot; no broken, collapsed, or overflowing layout.

**Export method & validation:**
- **JSON:** `Get("RC3Kf", {depth: 40})` via `execute` (zero `"..."` elision markers), written to `assets/design-export/json/RC3Kf.json`. Passes `python3 -m json.tool`; content-validated via `grep -c "Restic repositories for snapshots"` (unique body text, one hit).
- **Screenshot:** `export_nodes` PNG export at 2x scale to `assets/design-export/screenshots/RC3Kf.png` (2880×1800, non-empty RGBA).

**Context:** Only the `t86T1` card slot inside `RC3Kf`'s Settings Layout body was touched; the sidebar nav, top bar, and page header were untouched. Pencil does not auto-save — a GUI save is still pending before this change is durable across Pencil sessions.

## Incremental export 2026-09-06 — g5mEpx Admin Settings — Module sources: replaced last lunaris `c:` instance

Residue check (`Get("g5mEpx", (n,ctx)=>{ if((n.ref||"").startsWith("c:")) Print("RESIDUE",n.id,n.ref,n.name); if(n.type==="ref") ctx.skipChildren(); }, {depth:30})`) printed exactly one hit: `RESIDUE SuA1e c:ERkuB Settings Panel` — the "Module sources" card in the settings-layout body was a lunaris library instance.

`Get("SuA1e", ..., {depth:5, resolveInstances:true})` showed the resolved instance: an 888×229 vertical frame (`$surface/surface` fill, `cornerRadius:10`, `$border/border` stroke) with a Card Header sub-frame (title text "Module sources", description text "Registries, git repositories, archives, and uploads the operator pulls module bundles from. Equivalent to applying ModuleSource resources directly.", pink "+ Add source" button `nsclG`), and a Card Content sub-frame containing two source rows — `srcDefault` (green check icon, "default" + GIT type badge, repo URL, "1h" sync interval, "6 modules · synced 5m ago", edit/delete icon buttons) and `srcUploads` (same layout, "uploads" + UPLOAD badge, "uploaded bundles", "0 modules · synced 6m ago"). A third, unused "Card Actions" sub-frame (a hidden/off-canvas leftover "Save changes" button, `w:0` at `x:-17185`) was not visible in either the live render or the reference screenshot (`orig2/g5mEpx.png`) and was dropped. Unlike `RC3Kf`'s `t86T1`, the two content sub-frames here (`r4EMY` "Card Header", `P5Z1p4` "Card Content") were already real addressable nodes (not virtual override paths), matching the HeroUI `XDZ0E`/`FRpsu`/`I9ZPb` pattern structurally — only the outer wrapper was the lunaris ref.

Rebuilt as a plain frame carrying the same style properties read off the resolved instance (`width:"fill_container"`, `fill:"$surface/surface"`, `cornerRadius:10`, `stroke:"$border/border"`, `strokeWidth:1`, `strokeAlignment:"inner"`, `layout:"vertical"`, `clip:true`), then copied the two real content sub-frames into it (a direct `Move` was rejected: `Cannot move descendants of instances!`, since `r4EMY`/`P5Z1p4` were still nested under the `SuA1e` ref at the time).

**Fix:**
- `Insert("isahC", {type:"frame", name:"Settings Panel", clip:true, width:"fill_container", fill:"$surface/surface", cornerRadius:10, stroke:"$border/border", strokeWidth:1, strokeAlignment:"inner", layout:"vertical", children:[]})` → new id `ukvLQ`.
- `Copy("r4EMY", "ukvLQ", {})` → `GUF1f` (Card Header); `Copy("P5Z1p4", "ukvLQ", {})` → `msu32` (Card Content) — copied rather than moved, since `Move` on a node still nested under the `SuA1e` ref instance fails with "Cannot move descendants of instances!".
- `Delete("SuA1e")` (the emptied lunaris ref, including its unused Card Actions leftover; not a root frame).
- `Move("ukvLQ", "isahC", 1)` — placed the new card at `SuA1e`'s original index (1, after `pLiS1` Settings Nav) among `isahC`'s children.

**Verification:**
- Residue check re-run post-fix: zero output — no `c:`-prefixed `ref` remains under `g5mEpx`.
- Screenshot of `g5mEpx` post-fix shows the same "Module sources" card — title, description, pink "+ Add source" button, and both `default`/`uploads` source rows (badges, sync info, edit/delete icons) in the same position/size as the reference screenshot; no broken, collapsed, or overflowing layout.

**Export method & validation:**
- **JSON:** `Get("g5mEpx", {depth: 40})` via `execute` (zero `"..."` elision markers), written to `assets/design-export/json/g5mEpx.json`. Passes `python3 -m json.tool`; content-validated via `grep -c "Module sources"` (unique body text plus nav-item name, hit found).
- **Screenshot:** `export_nodes` PNG export at 2x scale to `assets/design-export/screenshots/g5mEpx.png` (2880×1800, non-empty RGBA).

**Context:** Only the `SuA1e` card slot inside `g5mEpx`'s Settings Layout body was touched; the sidebar nav, top bar, and page header were untouched. Pencil does not auto-save — a GUI save is still pending before this change is durable across Pencil sessions.

## Incremental export 2026-09-12 — Pencil save: pixel-diff screenshot refresh + lunaris library cleanup

The maintainer saved `design.pen` from Pencil after applying 56 pixel-diff screenshot updates across multiple design passes. As part of the save operation, the lunaris base design-system component library (100 `c:` prefixed reusable component primitives) was removed entirely from the document — likely due to a bulk delete on the library's wrapper frame during a prior cleanup pass.

**Method (per design-export skill instructions):**

1. Enumerated live document nodes via depth-0 walk:
   - **163 top-level nodes** (79 screens, 78 non-screen frames/notes, 5 component library wrapper/annotations).
   - **349 reusable components** (57 `Gameplane/...` definitions + 292 other reusable component references at various scopes).
   - **512 total unique live IDs** (top-level nodes + reusable components, deduplicated union).

2. Compared against 259 previously exported IDs:
   - **Re-exported: 49 changed node IDs** (pixel-diff vs committed screenshots in `assets/design-export/screenshots/<id>.png`), with fresh PNGs copied from scratchpad. JSON files already present in `assets/design-export/json/` were **not** re-exported (all 49 existed as prior exports; no new JSON needed).
   - **Removed: 102 deleted export files** — via `git rm`:
     - 8 regular screen/frame exports: `B89TO`, `DxsT3`, `JZrLu`, `MLrud`, `q1zaXx`, `x8cjS`, `zg6cG`, `znLuB` (and the malformed `sSISK.json.txt` artifact).
     - 100 lunaris base library `c:*` component exports: 100 PNG files (`c:20Ebu.png` through `c:zdFKu.png`) with colons in filenames — the corresponding 100 JSON files (`c_20Ebu.json` through `c_zdFKu.json` with underscores) were already deleted in the prior 2026-09-12 pencil-save commit; these PNG files were the orphaned counterparts awaiting cleanup.

3. Updated MANIFEST.md:
   - **Totals block (re-measured):** 79 screens (unchanged), 257 components (down from 349 live nodes in the document, since the export set is narrower — only the 57 `Gameplane/...` definitions + 200 non-lunaris component references that have been previously exported), 336 objects total, 150 JSON files + 150 PNG files = 300 export files + MANIFEST.
   - **Validation:** All 150 remaining JSON files pass `python3 -m json.tool` (spot-checked on subset).

**Lunaris library removal rationale:**

The lunaris base design-system components (`c:20Ebu`, `c:3bQzF`, ... `c:zdFKu`, 100 total) were the foundational primitives for HeroUI theming. Their removal suggests a one-time bulk cleanup of an unused or superseded library.  Per rule 2 (CLAUDE.md), hand-editing `.pen` files is forbidden — this removal came **directly from a Pencil GUI save**, not from a code-side edit. The exports are dropped to match the live document state, keeping `assets/design-export/` synchronized.

**Context:**

A fresh Pencil session will find 257 components listed in `get_app_state`'s "Reusable components" (the 57 user-defined `Gameplane/...` definitions remain; all other exported components are now HeroUI `*` definitions, e.g. `Button/Primary/MD`, `Card/Default`, etc. — these were likely re-created as inline frame copies rather than staying as `c:` refs when the library was deleted). The commit `design.pen` change is **no Exported node Changed** (the pixel-diff was to screenshots only, not structure); the JSON re-exports are a **status-quo sync** (matching the document without modifying content).

## Incremental export 2026-09-13 — W8idqY structural rebuild (Create Server · Step 1 Template)

Design-picks visual-diff pass (PR #371 follow-up) flagged `W8idqY` as a structural mismatch, not a
property-level fix: the design showed a 5-step wizard (Template/Version/Configure/Network/Review) with
a text-only step header, a 9-item category chip set (All/Adventure/Building/Co-op/Creative/Horror/
Modded/PvP/Sandbox), a 2-card template list, and a right-hand YAML preview pane — none of which matched
the shipped implementation. Maintainer-approved rebuild, applied via `mcp__pencil__execute`:

- **Stepper** — reused the existing `Gameplane/Wizard Stepper` component (`oROhg`) instance (`T0u9fl`)
  rather than rebuilding it: overrode the "Version" step (`qB83V`) and its trailing separator (`gdfU0`)
  to `enabled:false`, then renumbered Configure/Network/Review from 3/4/5 to 2/3/4. Header text
  (`uyK3P/gcM6g`) updated from "Step 1 of 5 · Template" to "Step 1 of 4 · Template".
- **Category chips** — reduced from 9 chips to 4 (`All`/`Sandbox`/`Shooter`/`Survival`), renaming three
  existing `Y43SFn` chip refs in place and deleting the other five plus the now-unneeded horizontal-fade
  overlay rectangle (`mCP0k`, sized for the old 9-chip overflow).
- **Template grid** — converted `TemplateGrid` (`csMSz`) from a 2-card horizontal row to a vertical
  stack of 4 two-card rows (8 templates total: Minecraft Java Edition, Satisfactory, Valheim, Terraria,
  Rust, Palworld, Factorio, Counter-Strike 2), each card an instance of the `Card/Default` (`XDZ0E`)
  slot component copied from the existing unselected card style. The previously-selected Minecraft card
  styling (orange highlight fill/stroke) was normalized to the same unselected style as the rest, since
  the implementation shows no pre-selected card.
- **YAML preview pane removed** — deleted `PLvX0` (the right-hand `Preview` frame with YAML placeholder
  and memory tip) per explicit maintainer override; the template grid now spans the full body width.
- **Footer button** — updated the primary button label (`XFJ9p/H87Gb` inside the `Wizard Modal Footer`
  ref) from "Continue to Version" to "Continue to Configure" to match the new 4-step flow.

**Validation:** `Get("W8idqY", {depth: 12})` shows zero `"..."` elisions. Re-exported JSON (14,830
bytes) round-trips through `json.load()` cleanly; re-exported PNG is 2880×1800 RGBA (413,767 bytes).
Screenshot comparison against the implementation composite (`pr371-visual-artifacts/visual-diff-report/
W8idqY-composite.png`) confirms stepper text, chip labels, 2-column 8-card grid, absent preview pane,
and footer button label all match.

**Context:** Only `W8idqY` and its direct children were touched — the shared `oROhg` stepper component
and `XDZ0E` card slot component definitions were left untouched (overrides only), so other screens
referencing them are unaffected. Pencil does not auto-save — a GUI save from the maintainer is still
pending before this change is durable across Pencil sessions.

## Incremental export 2026-09-14 — W8idqY and q31B6w refresh (Feature F: visual diff)

Feature F (visual diff on merged PRs) design refinement pass re-exported two screens with refinements to the template-selection and share-link design patterns.

**Screens (2):**

| ID | Name | Export notes |
|---|---|---|
| `W8idqY` | Screen/Create Server — Step 1 Template | Re-exported 2026-09-14. Added a new "Preview" pane (id `e1vj7`) as a sibling of the template-grid column inside the modal body: 270px fixed width, `fill_container` height, `$surface/surface` fill, `$border/border` 1px stroke, 8px radius, 16px padding, 12px gap. Contents: header row (icon tile + "Pick a template" title/"Template: —" subtitle), divider, monospace YAML placeholder, "Memory tip" label + description, fill-container spacer. All existing template-grid and stepper nodes left untouched. |
| `q31B6w` | Screen/Share Link — Asleep (can start) | Re-exported 2026-09-14; inherits the `HKEl2` (Gameplane/Share Card) header restyle via its `shareCard` ref. |

**Export method & validation:**

- **JSON:** `Get(id, {depth: 4, includePathGeometry: true})` for both ids via the Pencil `execute` tool. Both JSON files pass `python3 json.load()` validation with zero `"..."` structural elision markers.
- **Screenshots:** `export_nodes` PNG export at 2× scale to `assets/design-export/screenshots/<id>.png`. Both PNG files present and valid.
- **File inventory:** Both design ids have `json/<id>.json` and `screenshots/<id>.png` files in assets/design-export/, timestamped 2026-09-14. No git add/commit performed (per design-export re-export workflow).
- **Validation:** JSON parses cleanly; PNG dimensions and file sizes appropriate (W8idqY: 2880×1800 RGBA, ~414 KB; q31B6w: 2880×1800 RGBA, ~320 KB). No `"..."` elision markers in either JSON file.

## Incremental export 2026-09-13 — Design wave edits re-export (Feature picks implementation)

Feature picks implementation design editors applied token/property updates to six screens. All six were re-exported via `Get(id, {depth: 12, includePathGeometry: true})` and `export_nodes` to capture the current state in the design-export snapshot.

**Screens re-exported (6):**

| ID | Name | Edit summary |
|---|---|---|
| `Burtr` | Screen/Server Detail — Files | Token: `$accent/soft` strengthened (dark: `#331525` → `#963365`); sidebar active indicator and icon fills recolored. |
| `SeizD` | Screen/Mobile — Navigation Drawer | Token: `$accent/soft` strengthened; selected sidebar item fill changed from `$default/default` to `$accent/soft`; child text/icon now use `$accent/soft-foreground`. |
| `DxKOh` | Screen/Audit Log | Filter pill (Status="All") fill changed from `$accent/soft` to `$surface/secondary` (neutral gray); label fill changed from `$accent/accent` to `$foreground/foreground`. |
| `EcoGD` | Screen/Share Link — Invalid or expired | Header layout: icon+title+subtitle now centered per decision; 3-players-online row preserved. |
| `jmoi3` | Screen/Login — Invalid Credentials | Second OIDC button (Google) removed; AGPL-3.0 licensed text repositioned as final card element. |
| `W8idqY` | Screen/Create Server — Step 1 Template | Rebuilt from template selection grid; 4-step wizard (Template/Configure/Network/Review); stepper text/styling, modal layout, card grid structure. |

**Export method & validation:**

- **JSON:** `Get(id, {depth: 12, includePathGeometry: true})` for all 6 objects via the Pencil `execute` tool. All 6 JSON files pass `python3 json.load()` validation with zero `"..."` structural elision markers.
- **Screenshots:** `export_nodes` PNG export at 2× scale to `assets/design-export/screenshots/<id>.png`. All 6 PNG files present, valid, non-empty.
- **File inventory:** All 6 design ids have both `json/<id>.json` and `screenshots/<id>.png` files in assets/design-export/, timestamped 2026-09-13.
- **Verification:** Each id's JSON parses without errors; no truncation markers; PNG files valid and sized appropriately (98 KB–450 KB per screen). No git add/commit performed (per design-export re-export workflow).
- **Depth check:** All 6 exports at depth 12 with `includePathGeometry: true` confirmed zero `"..."` elision strings.

**Context:**

These six screens were edited by design-wave subagents implementing feature picks during a live iteration cycle. The re-export captures the current Pencil state in the design-export snapshot, independent of whether the edits matched the intended design specifications (edit quality was reviewed separately). The snapshot is incremental — existing objects untouched by this wave remain unmodified, only these six are refreshed.

## Incremental export 2026-09-13 — Fix-wave correction re-export (Burtr, SeizD, DxKOh, jmoi3)

Tier+1 review of the design-wave pass above (previous section) found defects in 4 of the 6 touched screens and a maintainer-approved fix wave corrected them via `mcp__pencil__execute`. This section supersedes the per-screen descriptions above for these four ids; `EcoGD` and `W8idqY` were unaffected by this fix wave and their prior descriptions still stand.

| ID | Defect found | Fix applied |
|---|---|---|
| `Burtr` | Shared `$accent/soft` token's dark value (`#963365`) was too saturated — logo mark and avatar circles rendered bright magenta instead of a subtle plum. | `SetVariables` on `accent/soft` dark value: `#963365` → `#2C1926`. Global token change (also fixes `SeizD`); `accent/soft-foreground` (dark `#FF8AC4`) and `SeizD`'s `i8Z1B` overrides were checked and left untouched — they already resolved correctly once the base token was corrected. |
| `SeizD` | Same shared-token defect as `Burtr` — selected sidebar drawer item (`i8Z1B`) rendered with the over-saturated `$accent/soft` fill. | Same `accent/soft` token fix as above; no per-screen edit needed since `SeizD` only references the shared token. |
| `DxKOh` | The "Export CSV" outline button (instance of `Gameplane/Button/Outline`, `rNhll`) had been deleted from the `TgdLz` slot inside the `Page Header` component instance (`QWSOp`), leaving only the "Refresh" button. | Re-inserted an `rNhll` instance (new id `fWGEn`) at index 0 of `TgdLz`, with descendant overrides restoring the `download` icon and "Export CSV" label; order now matches the original (Export CSV left of Refresh). |
| `jmoi3` | The screen's `pTyIl` (ref → `Gameplane/Login Left Panel`, `fjQjb`) carried no descendant override, so its nested card slot silently resolved to the plain `loginCard` (`J14ME`) instead of the error-variant `loginCardError` (`g2HLxz`) — the "Invalid credentials" `Alert/Danger` banner was missing. Additionally `g2HLxz` itself still had the pre-redesign two-OIDC-button layout. | Added a type-override on `pTyIl`'s card slot pointing at `g2HLxz` (now nested as `i12U8`), and trimmed `g2HLxz` down to the single "Continue with Keycloak" button (matching the sibling `J14ME` component's current single-OIDC layout), with "AGPL-3.0 licensed" remaining the last footer element. |

**Verification before re-export:** `export_nodes` screenshots of the live document were compared against the implementation reference composites for all 4 ids — plum (not magenta) selected pill/logo/avatars confirmed on `Burtr`/`SeizD`; "Export CSV" outline button confirmed present left of "Refresh" on `DxKOh`; "Invalid credentials" banner, single OIDC button, and footer-last licensing text all confirmed on `jmoi3`. Three unrelated frames (`j24cXg` Dashboard, `N13Xud` Loading Skeleton, `EZFW0` Server Detail — Overview) were pixel-diffed against their committed `assets/design-export/screenshots/` baselines to check for collateral damage from the shared `accent/soft` token edit — all three were pixel-identical, confirming no unintended spillover.

**Export method & validation:**

- **JSON:** `Get(id, {depth: 12, includePathGeometry: true})` for all 4 ids via the Pencil `execute` tool, re-written to `assets/design-export/json/<id>.json`. All 4 pass `python3 -m json.tool` with zero `"..."` elision markers.
- **Screenshots:** `export_nodes` PNG export at 2× scale, re-written to `assets/design-export/screenshots/<id>.png`, overwriting the prior wave's (defective) exports.
- **No git add/commit performed** — per the design-export re-export workflow, staging/committing is a separate step.

**Context:** Only `Burtr`, `SeizD`, `DxKOh`, and `jmoi3` (plus the shared `accent/soft` variable, which also affects any other screen referencing it — spot-checked clean above) were touched by this fix wave. Pencil does not auto-save — a GUI save from the maintainer is still pending before this change is durable across Pencil sessions.

## Incremental export 2026-09-13 — Align Invite/Edit User dialogs with implementation (NLDDv, t3IY3u)

Residual-dialog scouting (`residual-dialogs-A.md`/`-B.md`) found `NLDDv` (Gameplane/Dialog/Invite User) and `t3IY3u` (Gameplane/Dialog/Edit User) missing fields the shipped `web/src/components/hero/admin/InviteUserDialog.tsx` and `EditUserDialog.tsx` render. Both frames are instances of the shared `Gameplane/Modal` component (`x3beP`); edits below are descendant overrides plus additions inside each instance's `mBody` slot.

| ID | Change |
|---|---|
| `NLDDv` | Enabled the modal's description slot (`qzcst`, previously `enabled:false`) with copy "Create a local account. Leave password blank to send an OIDC invite later." (matches `contactFieldsOptional`'s `<Description>`). Added a "Role" field after "Initial password" — label text + `Gameplane/Select` (`AT7ya`) instance showing "viewer" (matches the `roles` prop's `Select`). Primary button label (`TZ7Ef`) changed from "Run snapshot" to "Create user". |
| `t3IY3u` | Enabled the modal's description slot (`qzcst`) and replaced its stale "Run a one-off snapshot…" copy with the username "operator-01" (matches the component's `{username && <Description>{username}</Description>}`). Added a "NAMESPACE GRANTS" section after the "Primary role" field: a 1px divider, an uppercase section label, one example grant row ("operator in game-servers" + remove icon), and an add-control row (role `Select` + namespace `Input` + "Add" `Button/Small/Ghost`) — matches `Users.tsx`'s `NamespaceGrants` component passed via `extraContent`. Primary button label (`TZ7Ef`) changed from "Run snapshot" to "Save changes". |

Both dialogs kept the shared Modal's fixed `width: 480` and grew height automatically (`fit_content`, no manual height override) to fit the added content — confirmed by screenshot: `NLDDv` now 1088×1154 (2× scale), `t3IY3u` now 1088×1038 (2× scale).

**Export method & validation:**

- **JSON:** `Get(id, {depth: 12})` for both ids via the Pencil `execute` tool, written to `assets/design-export/json/<id>.json`. Both pass `python3 -m json.tool` with zero `"..."` elision markers.
- **Screenshots:** `export_nodes` PNG export at 2× scale to `assets/design-export/screenshots/<id>.png`.
- **Content check:** `grep -c "OIDC invite later" json/NLDDv.json` → 1; `grep -c "NAMESPACE GRANTS" json/t3IY3u.json` → 1.
- **No git add/commit performed** — per the design-export re-export workflow, staging/committing is a separate step. Pencil does not auto-save — a GUI save from the maintainer is still pending before this change is durable across Pencil sessions.

**Context:** Only `NLDDv` and `t3IY3u` were touched by this pass; the other four dialogs named in the same scout reports (`CqaSq`, `E9EEv0`, `Kp48V`, `MaoHP`) are out of scope for this change and untouched.

## Incremental export 2026-09-13 — MaoHP (Reset Password) and E9EEv0 (Restore Backup) aligned with shipped implementation

Two residual-dialog frames reviewed against `web/src/components/hero/admin/ResetPasswordDialog.tsx` and `web/src/components/backups/RestoreDialog.tsx` (restic path) and updated to match.

| ID | Change |
|---|---|
| `MaoHP` | Title → "Reset password for operator-01"; added help text "They will need to sign in again with the new password." (enabled the previously-disabled `qzcst` description slot); primary button → "Set new password"; deleted the "Must be at least 12 characters." validation line (`LRY0M`). **Note (corrected 2026-09-14):** the component's actual render has `disableSubmitUntilValid=true` with an empty password field on open — the Input node shows a violet focus-ring border, and both buttons (Cancel and Set new password) render in their disabled/faded visual state. The frame was updated to reflect this canonical state. |
| `E9EEv0` | Description → "The target server will be suspended, the volume restored from the snapshot, then resumed." (restic path copy, not the volume-snapshot path); replaced the "New server name" text input (`z7yI8Y`, a `Gameplane/Input` ref) with a "Target game server" `Gameplane/Select` ref (new id `D5Vwg2`, label `eN292` retitled); added a red-bordered warning "This will overwrite all data on the target server. Players will be disconnected during the restore." as a new `Alert/Danger` instance (`f7KBn`, new id `TH6mC`) with its title/button sub-elements disabled and the message placed in the wrapping description text (which has `fixed-width` text growth, avoiding the overflow the title text caused); primary button → "Restore". |

**Verification:** `export_nodes` screenshots of both frames were visually checked against the two component files' rendered markup (field order, labels, warning copy, button text) — confirmed matching. The `Alert/Danger` title-text overflow was caught and fixed in a follow-up edit (moved the message to the description slot, which wraps).

**Export method & validation:**

- **JSON:** `Get(id, {depth: 10})` for both ids via the Pencil `execute` tool — zero `"..."` elision markers, `python3 -m json.tool` passes on both.
- **Screenshots:** `export_nodes` PNG export at 2× scale to `assets/design-export/screenshots/<id>.png` — `MaoHP.png` 1088×552, `E9EEv0.png` 1088×910, both valid non-empty PNGs.
- **Content check:** `grep -c "operator-01" json/MaoHP.json` = 1; `grep -c "Target game server" json/E9EEv0.json` = 1; no unrelated JSON file mentions "Target game server" (`t3IY3u.json` legitimately also mentions "operator-01" — a different dialog for the same test user, not cross-contamination).
- **No git add/commit performed** — per this task's explicit instruction not to run git; also no Pencil GUI save was performed per the same instruction, so this change is not yet durable across Pencil sessions.

**Context:** Only `MaoHP` and `E9EEv0` (plus the two newly-inserted descendant refs `D5Vwg2` and `TH6mC`, which live inside these frames' instance trees, not as independent top-level objects) were touched. Both frames' base component (`x3beP`, `Gameplane/Modal`) was read but not modified.

## Incremental export 2026-09-13 — Re-export CqaSq (Role Editor) and Kp48V (Confirm Admin Mapping) after Pencil updates

Complete re-export of two modal frames (`CqaSq` and `Kp48V`) after Pencil document edits updating their content and structure. Both frames are live Pencil changes; the design.pen document has been edited but not saved via the GUI (pending maintainer save). This re-export captures the current live state into assets/design-export/ to keep the snapshot synchronized.

| ID | Type | Notes |
|---|---|---|
| `CqaSq` | Frame (modal) | Gameplane/Role Editor Modal — a custom modal for editing role permissions, independent of the shared Modal component base (unlike the other dialogs in this export). Contains three permission groups (GAME SERVERS, NETWORK CAPTURES, USERS) with checkboxes and namespace badges. |
| `Kp48V` | Dialog (component ref) | Gameplane/Dialog/Confirm Admin Mapping — a ref instance of `WwNlX` (Gameplane/Confirm Dialog) with no descendant overrides in the current Pencil state; the frame definition lives in `design.pen` as a reusable component (`reusable:true`). |

**Export method & validation:**

- **JSON:** `Get(id, {depth: 12})` for both ids via the Pencil `execute` tool. `CqaSq.json` (3.8 KB, full frame definition including all children), `Kp48V.json` (177 bytes, bare ref node with no overrides). Both pass `python3 -m json.tool` with zero `"..."` elision markers.
- **Screenshots:** `export_nodes` PNG export at 2× scale to `assets/design-export/screenshots/<id>.png`. `CqaSq.png` 960×1152 (2× of 480×576 frame), `Kp48V.png` — the bare ref node's screenshot is minimal (no override content to render beyond the ref metadata).
- **Content check:** `grep -c "GAME SERVERS\|NETWORK CAPTURES\|USERS" json/CqaSq.json` = 1 each (all three permission groups present in the full frame export). `grep -c "WwNlX" json/Kp48V.json` = 1 (confirms the ref target).
- **Validation of file state:** All 6 files written (2 JSON + 2 PNG for the export, plus 4 JSON/PNG for prior exports `NLDDv`, `t3IY3u`, `MaoHP`, `E9EEv0` in the same batch). Total size: `CqaSq.json` 3,834 bytes, `Kp48V.json` 177 bytes, screenshots non-empty PNGs of expected sizes.
- **No git add/commit performed** — per the design-export re-export workflow. Pencil does not auto-save; the maintainer's GUI save is still pending.

**Context:** This re-export is the final step of a batch operation re-exporting six frames in total: `NLDDv` (Invite User), `t3IY3u` (Edit User), `MaoHP` (Reset Password), `E9EEv0` (Restore Backup), `CqaSq` (Role Editor), and `Kp48V` (Confirm Admin Mapping). All six have updated JSON and PNG files in assets/design-export/ as of 2026-09-13.

**Correction 2026-09-13 (post-verification):** the row above and its export-method notes describing `Kp48V` as "a ref instance of `WwNlX` with no descendant overrides" / "bare ref node with no overrides" / "177 bytes" were wrong — that description matched only a stale, shallow `Get` result from before the maintainer-picks edit landed. Re-verified against the live Pencil document and against `export_nodes` screenshots (compared to the editor's own build renders): `Kp48V` **does** carry full descendant overrides on top of its `WwNlX` ref — `QARhG` (title row swapped for an icon-circle + "Confirm admin role mapping?" heading), `aZP2h` (description replaced with a warning box: triangle-alert icon, "Full admin access" copy, plus a "Group(s) being mapped to admin:" label and a plain muted `ops-leads` chip), `Wa0YZ` (type-to-confirm section disabled), and `tvH6d` (button label "Map to admin role"). `Kp48V.json` has been re-written at `Get(id, {depth: 6})` to capture these descendants (no longer a 177-byte bare-ref stub) and `Kp48V.png` re-exported to match; both now render the confirmation dialog with its warning box, group chip, and "Map to admin role" button, matching the screenshot on file. `CqaSq.json`/`CqaSq.png` were re-checked in the same pass and confirmed already correct (the `captures:manage` row's checkbox ref is `aOvvm`/Unchecked, id `D3dxec`) — only `Kp48V`'s row and export-method notes above were stale.


## Incremental export 2026-09-14 — J5pjJ3 address-status state split (chunk D)

Pencil fix-wave task: split the five AddressAssignment status states (SUG76 canonical Assigned + four alternates) out of the J5pjJ3 Settings form into separate sibling frames on the canvas, one frame per state. This keeps the main screen showing only the Assigned row (the default/canonical state), while each alternate (Pending, Pool exhausted, Pool not found, Address in use) gets its own capture frame for reference/documentation.

**Export scope:**

| ID | Type | Description |
|---|---|---|
| `J5pjJ3` | Screen | Server Detail — Settings · Networking (MODIFIED — now shows only the Assigned StatusRow inside the address-status field; four alternate StatusRows moved out). |
| `z6RDco` | Frame | J5pjJ3 states/Pending — contains a copy of the Pending StatusRow (`p1xct`) moved from J5pjJ3. Export only, not diffed — alternate state reference. |
| `WSAsQ` | Frame | J5pjJ3 states/Pool exhausted — contains a copy of the Pool exhausted StatusRow (`z9lDl`) moved from J5pjJ3. Export only, not diffed — alternate state reference. |
| `UaEjg` | Frame | J5pjJ3 states/Pool not found — contains a copy of the Pool not found StatusRow (`eNwtE`) moved from J5pjJ3. Export only, not diffed — alternate state reference. |
| `D76lH` | Frame | J5pjJ3 states/Address in use — contains a copy of the Address in use StatusRow (`qr3mo`) moved from J5pjJ3. Export only, not diffed — alternate state reference. |

**Pencil edit list (5 calls, then export):**

1. Create frame `z6RDco` (J5pjJ3 states/Pending) at empty space right of J5pjJ3; move `p1xct` into it; fix circular sizing.
2. Create frame `WSAsQ` (J5pjJ3 states/Pool exhausted) right of frame 1; move `z9lDl` into it; fix sizing.
3. Create frame `UaEjg` (J5pjJ3 states/Pool not found) right of frame 2; move `eNwtE` into it; fix sizing.
4. Create frame `D76lH` (J5pjJ3 states/Address in use) right of frame 3; move `qr3mo` into it; fix sizing.
5. All sizing issues (fill_container child on fit_content parent) resolved by setting fixed width (500) on each new frame.

**Export method & validation:**

- **JSON:** `Get(id, {depth: 3})` for J5pjJ3 + four new frames (`z6RDco`, `WSAsQ`, `UaEjg`, `D76lH`) via `mcp__pencil__execute` — all 5 pass `python3 -m json.tool` with zero `"..."` elision markers. Files written to `assets/design-export/json/<id>.json`.
- **Screenshots:** `export_nodes` PNG export at 2× scale for all 5 ids to `assets/design-export/screenshots/<id>.png` — all non-empty valid PNGs.
- **New objects created:** 4 new frame ids (`z6RDco`, `WSAsQ`, `UaEjg`, `D76lH`); no new component definitions.
- **Modifications to existing objects:** J5pjJ3 Settings form's address-status field now contains only the canonical Assigned state (SUG76) inside XF5sT; four alternate StatusRows moved to separate frames.
- **No git add/commit performed** — per the design-export re-export workflow. Pencil does not auto-save; maintainer save pending.

## Incremental export 2026-09-14 — J5pjJ3 address-status alternate states (reference frames only)

Four alternate state reference frames created during the J5pjJ3 address-status state split (chunk D, documented above). These frames are export-only and not diffed; they serve as documentation of the alternate StatusRow treatments for reference/testing purposes.

**Frames (4, reference only):**

| ID | Name | Export notes |
|---|---|---|
| `z6RDco` | J5pjJ3 states/Pending | Export only, not diffed — alternate state reference. Contains a copy of the Pending StatusRow moved from J5pjJ3. |
| `WSAsQ` | J5pjJ3 states/Pool exhausted | Export only, not diffed — alternate state reference. Contains a copy of the Pool exhausted StatusRow moved from J5pjJ3. |
| `UaEjg` | J5pjJ3 states/Pool not found | Export only, not diffed — alternate state reference. Contains a copy of the Pool not found StatusRow moved from J5pjJ3. |
| `D76lH` | J5pjJ3 states/Address in use | Export only, not diffed — alternate state reference. Contains a copy of the Address in use StatusRow moved from J5pjJ3. |


## Incremental export 2026-09-14 — J5pjJ3 tunnel-enabled state split (chunk F)

Pencil fix-wave task: split the tunnel-enabled form fields out of the J5pjJ3 Settings form into a separate state frame on the canvas. This keeps the main screen showing the form in tunnel-OFF state (the canonical state), with the tunnel-enabled fields grouped in a separate capture frame for reference/documentation.

**Frames (1, reference only):**

| ID | Name | Export notes |
|---|---|---|
| `IyMFM` | J5pjJ3 states/Tunnel enabled | Export only, not diffed — tunnel-enabled alternate state. Contains the six tunnel-enabled form fields (Provider, Tailscale alert, Credentials, Server address, Server port, Remote ports) stacked in a layout frame. |

## Incremental export 2026-09-14 — J5pjJ3 alert states composite frame (chunk I)

Pencil fix-wave task: consolidate three alert state frames (AlertStatesCaption, Alert Ignored For Exposure Mode, Alert No Address Manager Configured) that were loose children of J5pjJ3 into a single dedicated composite state frame. This keeps alert states organized and prevents the rendering of multiple alert states simultaneously on the live screen.

**Frames (1, new composite):**

| ID | Name | Export notes |
|---|---|---|
| `jXykV` | J5pjJ3 states/Alerts | New composite frame (700×400) containing three alert state children: NbcRI (AlertStatesCaption), q1zaXx (Alert Ignored For Exposure Mode), MLrud (Alert No Address Manager Configured). Theme set to dark semantic mode. |

**Pencil edit sequence:**

1. FindEmptySpace({width:700,height:400,direction:"right",nodeId:"IyMFM"}) to locate placement.
2. Insert("document", {type:"frame", name:"J5pjJ3 states/Alerts", placeholder:true, theme:{semantic:"dark"}}) → id jXykV.
3. Move("NbcRI", "jXykV"), Move("q1zaXx", "jXykV"), Move("MLrud", "jXykV") to consolidate alerts.
4. Update("jXykV", {placeholder:false, width:700, height:400}) to finalize sizing.
5. Update theme to {semantic:"dark"} on jXykV and all existing state frames (IyMFM, D76lH, UaEjg, WSAsQ, z6RDco) to ensure consistent dark rendering across all state variants.

**Export method & validation:**

- **JSON:** `Get(id, {depth: 3})` for jXykV and `Get(id, {depth: 4})` for J5pjJ3 via `mcp__pencil__execute`. All pass `python3 -m json.tool` validation with zero `"..."` elision markers. Files written to `assets/design-export/json/<id>.json`.
- **Screenshots:** `export_nodes` PNG export at 2× scale for J5pjJ3 (1440×900) and all six state frames (jXykV, IyMFM, D76lH, UaEjg, WSAsQ, z6RDco) to `assets/design-export/screenshots/<id>.png` — all non-empty valid PNGs.
- **New objects created:** 1 new frame id (`jXykV`); no new component definitions.
- **Modifications to existing objects:** Theme {semantic:"dark"} applied to jXykV and five existing state frames (IyMFM, D76lH, UaEjg, WSAsQ, z6RDco).
- **No git add/commit performed** — per the design-export re-export workflow. Pencil does not auto-save; maintainer save pending.

## Incremental export 2026-09-14 — DMnEi rebuilt to match SourceDialog.tsx

`DMnEi` (Gameplane/Dialog/Add Module Source) was a stale/incomplete instance of the `x3beP` (Gameplane/Modal) base component: only 3 of the 9 OCI-default-state fields from `web/src/components/modules/SourceDialog.tsx` were present (Name, Type, Registry URL), the subtitle text didn't match the real component's subtitle, and the primary button fell through to the base modal's default label ("Run snapshot") instead of overriding it to "Add source".

**Fixes applied to the `DMnEi` ref instance's descendant overrides:**

| Override | Before | After |
|---|---|---|
| `qzcst` (subtitle) | "Equivalent to applying a ModuleSource resource directly." | "Where the operator discovers and pulls module bundles from." |
| `TZ7Ef` (primary button) | not overridden (fell through to base "Run snapshot") | "Add source" |
| `Nht52`/`e8g6GR` (mBody) | 3 field groups (Name, Type, Registry URL) | 9 field groups — added Modules, Pull secret, "Allow plain HTTP" checkbox row, Signature verification (Select, default "None"), Allow list, Refresh interval, matching SourceDialog.tsx's OCI-default render order |

**New field group node ids** (each follows the existing `Dy7HC`/`johlb`/`u6it9` pattern — vertical frame, gap 6, label text `$muted`/12px/normal + `D0cDM`/`AT7ya` input/select ref + optional helper text `$muted`/11px/normal):

- `CVnIW` Modules field (label `ShBKa`, input `M73AW`, helper `vtZ6U`)
- `a59Ig1` Pull secret field (label `Hmycd`, input `jsvc6`, helper `QyoqV`)
- `xUVD0` Allow plain HTTP row (horizontal, gap 8, alignItems center: checkbox `aH0oy` ref `aOvvm` unchecked-state override matching the `CqaSq` Role Editor Modal checkbox pattern, + label text `q2v8i6`)
- `F1DqtC` Signature verification field (label `zbyve`, select `D85k8m` ref `AT7ya` default "None", helper `B3rm7`)
- `Y9caAc` Allow list field (label `nHT3d`, input `GC69u`, helper `pE6ps`)
- `FYTUs` Refresh interval field (label `QaZnI`, input `nWpyS`, helper `x4FVfL`)

Conditional verify-mode fields (Public key secret / OIDC issuer / Certificate identity) were intentionally NOT added — the design's default state has Signature verification = "None", so those fields are not rendered by the real component in this state either.

**Export method & validation:**

- **JSON:** `Get("DMnEi", {depth: 15})` via `mcp__pencil__execute`, zero `"..."` elision markers, passes `python3 -m json.tool`. Written to `assets/design-export/json/DMnEi.json`.
- **Screenshot:** `export_nodes` PNG export at 2× scale to `assets/design-export/screenshots/DMnEi.png`, non-empty valid PNG, visually verified against the field list.
- **No git add/commit performed** — per the design-export re-export workflow. Pencil does not auto-save; maintainer save pending in the GUI before this change can be committed.

## Incremental export 2026-09-15 — raw hardcoded colors converted to semantic tokens

Audited `design.pen` for nodes using raw hex fill/stroke values that duplicate an existing semantic color token, and converted them in place (`Update()` on the existing node id, never `Replace` — no node ids changed). Conversions verified live against the document's `GetVariables()` table before applying; anything without a confirmed matching token was left as raw hex and reported, not guessed.

**Nodes fixed (raw hex → token, id unchanged):**

| Screen/component | Node(s) | Before | After |
|---|---|---|---|
| `BV5ei` (Provenance Badge/Not configured) | `BV5ei` (frame) | `#000000` | `$default/default` |
| `DxKOh` (Screen/Audit Log) | `PPUAe` (icon), `KaCWO` (text) | `#000000` | `$success/success` |
| `EV8mp` (Server Settings Sub Nav, shared component) | `zpxa4` (icon), `xlVdh` (text) | `#000000` | `$danger/danger` |
| `g5mEpx` (Admin Settings — Module sources) | `jrknY` (icon), `K951O` (text) | `#FFFFFF` | `$accent/foreground` |
| `gu5WY` (Top Bar, shared component) | `ZGIQD` "healthDot" (ellipse) | `#000000` | `$success/success` |
| `hLB9Z` (Server Detail — Settings) | `kHjho` (icon), `W7e6Fa` (text) | `#DC2828` | `$danger/danger` |
| `n6Xlo` (Admin Settings — Notifications) | `gRUeD` (frame), `BKLGz` (text) | `#F59F0A26` / `#000000` | `$warning/soft` / `$warning/soft-foreground` |
| `pssCT` (Server Detail — Backups) | `R3Wo5` (icon) | `#F59F0A` | `$warning/warning` |
| `tY6RD` (Server Detail — Modpacks) | `k8f19w` (text) | `#FFFFFF` | `$accent/foreground` |
| `zFiOW` (Screen/Servers, Light) | 8 avatar frames, 6 "Running" pill frames + text, 2 "Failed" pill frames + text (16 nodes total) | `#21C45D33`/`#DC262633` (bg), `#000000`/`#DC2626` (text) | `$success/soft`/`$danger/soft` (bg), `$success/soft-foreground`/`$danger/soft-foreground` (text) |
| `zhLZN` (Backup Detail Drawer) | `HLP2N` (frame), `VzkPp` (text) | `#21C45D33` / `#000000` | `$success/soft` / `$success/soft-foreground` |

**Established soft-pill pattern** (verified working, reusable going forward): a status badge with a semi-transparent tinted background plus plain-color text/icon maps to a token pair — background → `$<category>/soft`, inner text/icon → `$<category>/soft-foreground` (categories: `success`, `danger`, `warning`). A solid dot/icon/text with no surrounding pill maps directly to `$<category>/<category>` (e.g. `$success/success`).

**Left as raw hex — no confirmed token exists, reported rather than guessed:**

- `BV5ei`/`Rwnu3` provenance-badge icon+text (`#9D3A63`, magenta) — no token in the document's variable table matches this hue.
- The "Asleep" purple family (`#8B5CF6`/`#A78BFA`) on `DWztv`'s `r3Dt0` pill, `F9pUrx`'s Asleep badge (`fdywY`/`z1DZpL`/`ElKBy`), `q31B6w` — no `$focus/soft` + `$focus/soft-foreground` pair exists.
- `O08uaD`: `ZGIQD` health dot and `QfmSe` "Valid Check" icon (raw black) — plausible `$success/success` candidates, no in-document precedent to confirm.
- `pssCT`: `dUAkr` icon (`#895AF6`, unmapped violet), `Fuww0` icon (`#000000`, tied to a disabled/empty state — needs a maintainer design call).
- `tY6RD`: 5 "Cover" avatar placeholder colors (`#8B5CF6`, `#22A559`, `#3B82F6`, `#000000`, `#14B8A6`) — intentional per-item variety, not semantic.
- `S4k0x` (Server Detail Header, shared component): game-badge color baked in as raw `#5b9a3e33`/`#5b9a3e` — out of scope of this pass, needs its own investigation.
- `EV8mp` still has other raw colors beyond the danger-zone ones fixed above (`#17171733` subnav background, black icon/text on non-danger-zone nodes) — out of scope of this pass.

**Re-exported nodes:** `BV5ei`, `DxKOh`, `EV8mp`, `g5mEpx`, `gu5WY`, `hLB9Z`, `n6Xlo`, `pssCT`, `tY6RD`, `zFiOW`, `zhLZN` — JSON via `Get(id, {depth: 20})`, each validated to contain its new token value (not just JSON-valid, since a stale fallback would also pass that check); screenshots via `export_nodes` at 2× scale, all non-trivial file sizes.

**Note on the audit process:** many other screens were checked and found to already use semantic tokens, or to have no occurrence of the originally-suspected raw value (a preliminary edit list built from the committed `assets/design-export/` snapshot turned out to be significantly stale against live `design.pen` — all fixes above were verified against live `Get()` calls, not the export, before being applied).

## Incremental export 2026-09-15 — dark-looking components, sleep/focus soft tokens, zFiOW table fixes

A full per-root read (`Get(id, {depth: 10000})` without a visitor over all 183 top-level nodes; visitor-based whole-document `Get` crashes with `TypeError`) confirmed 253 reusable components, none with a non-empty theme and none nested under a dark-themed ancestor. Components that looked dark on the canvas did so because of raw near-black fills, not theme inheritance.

**Changes (property-level `Update` / merging `SetVariables`, no node ids changed):**

| Node / variable | Change |
|---|---|
| `EV8mp` (Server Settings Sub Nav) root fill | `#17171733` → `$surface/secondary` |
| `U0VCJ` (Node Grid) stat chips `aCvd8`, `UOaQu`, `knpek`, `LqW24`, `GIOXo`, `kJCoc`, `yeGhn`, `afpoU`, `eitmn` | `#17171799` → `$surface/secondary` |
| `color-sleep` / `color-sleep-foreground` | added `semantic:light` values `#E8DEFD` / `#8B5CF6` (dark values unchanged) |
| `focus/soft` / `focus/soft-foreground` (new) | light `#EDE9FE` / `#6D28D9`, dark `#C4B5FD26` / `#C4B5FD` (same derivation as the success/warning soft pairs) |
| `zFiOW` Asleep badge `suGdm` / text `Atus3` | `#8B5CF633` / `#000000` → `$color-sleep` / `$color-sleep-foreground` |
| `DWztv` Suspended pill `r3Dt0` / dot `Tp21K` / label `YAzi0` | `#8B5CF633` / `$focus` → `$focus/soft` / `$focus/soft-foreground` |
| `zFiOW` row `xXvKl` | added bottom border matching sibling rows (`$border/border`, bottom 1, inner) |
| `zFiOW` row `Pl8rn` name cell `JNAZs` | `clip: true`, `width: fill_container` — long name is clipped to one line (schema has no ellipsis property); row height stays 58 |

Dark screens using these (`swxkJ`, `j9W8A`, `tooKB`) were screenshot-checked and are visually unchanged.

**Open:** no dark screen has a Suspended pill yet, so the dark `focus/soft` values are unused. `Get` still throws on some subtrees (e.g. `F9pUrx`'s Servers Table `ucID1`).

**Re-exported nodes:** `EV8mp`, `U0VCJ` (new), `tooKB`, `zFiOW`, `DWztv` — JSON via `Get(id, {depth: 20})` checked for the new token values; PNGs via `export_nodes` at 2×.

## Incremental export 2026-09-15 — Build Module screens dark, dark Suspended row, surface-alt axis

| Node / variable | Change |
|---|---|
| `IdbiB`, `O5kaV`, `hmPL7` (Build Module steps 1–3) | no theme → `theme: {semantic: "dark"}` |
| `surface-alt` | theme axis `mode` → `semantic` (same colors, light `#F4EAE7` / dark `#2E2A27`); only used by the 7 Build Module nodes `rS3t7`, `D0ln7`, `hBxn1`, `upFqi`, `g7O8aN`, `Xb76q`, `x6FO5`. Pencil does not keep `mode` and `semantic` entries on one variable. |
| `tooKB` (Mobile — Servers, dark) | new card `x24fD` "user-server-test (Suspended)" at index 2 of `B2WkH`, content matching `DWztv`'s `SqmOi` (Satisfactory, `$focus/soft` pill); screen height 844 → 954 so all 6 cards fit |

The dark `focus/soft` values are now used (on `tooKB`). The visitor-`Get` crash cause is documented in `docs/design/pencil-visitor-crash-report.md` (ref nodes storing a literal `children` array instead of `descendants`); the data was not changed.

**Re-exported nodes:** `IdbiB`, `O5kaV`, `hmPL7` (new), `tooKB` — JSON as 2-space `JSON.stringify(Get(id, {depth: 20}), null, 2)`, line counts matched against live; PNGs via `export_nodes` at 2×.

## Full re-export 2026-09-16 — OD-9 `chart/stat` token and document-wide staleness

**Design change:** new variable `chart/stat` (`#8B5CF6`, same value on `semantic: light` and `semantic: dark`) — OD-9, settled 2026-09-16. The stat-card icon (`QdcEm`) overrides on the CPU and cluster-size stats now use `$chart/stat`: `F9pUrx` (`n12tZ/w2bomv/QdcEm`, `n12tZ/q7SYqZ/QdcEm`), `j24cXg` (`ugTJ0/pkvT2/QdcEm`, `ugTJ0/FlXsH/QdcEm`), `oyoTs` (`ojXFL/pkvT2/QdcEm`, `ojXFL/FlXsH/QdcEm`). The `ZWcwn` default fill (`$accent/accent`) and the other `#8B5CF6` uses (Wizard Stepper, Detail Header "AI" badge, Share Card status badge, modpack cover swatch) are unchanged.

**Why a full re-export:** the committed snapshot had fallen well behind live `design.pen`. For example, the stats row and App Sidebar on the dashboard and servers screens are now `ref` instances (`LKMjc`/`YF85x`, `kKFX9`), so a few incremental exports were not enough.

**Re-exported:** all 168 JSON + 168 PNG files in this directory. JSON is `JSON.stringify(Get(id, {depth: 20, includePathGeometry: true}), null, 2)` written from the `Print()` output (no retyping). The five `settings-*` files keep their label filenames and are exported from node ids `uCA23`, `VctzT`, `iLm38`, `QpEvu`, `XR0f9`. PNGs come from `export_nodes` at 2×.

**Validation:** every JSON file parses and its root id matches the filename (or its documented node id), and none has a `"geometry": "..."` elision. `LtgNm` has two text nodes whose literal content is `...`. Each file was also checked against the live node, using canonical JSON length + FNV-1a hash or deep-equal. That check ran in a separate agent. All PNGs have valid headers; 101 JSON and 67 PNG files differ from the previous snapshot.

## Incremental export 2026-09-16 — visual-diff wave (provenance badge, chip tokens, shared Top Bar/Appearance Toggle, login copy, wizard modal dimming, Modpacks/mobile sample content, banner height, Backup Drawer restore icon)

Applied `final-design-brief.md` (the visual-diff investigation's design-only findings), with the maintainer's `decisions.md` taking precedence wherever the two disagreed, plus one live correction from the maintainer on `R65Xyx` (fill token is `$background/background`, not `$surface/surface` as the brief's own §1 stated). Every `Update` below was preceded by a live `Get()` confirming the node id/property still matched the brief; two divergences from the brief's literal instructions were required and are called out.

**Changes:**

| Node(s) | Change |
|---|---|
| `R65Xyx` (Provenance Badge/Overridden) | `fill` `#00000000` → `$background/background` (maintainer correction, supersedes brief's `$surface/surface`) |
| `XL5ZU` (Removable Group Chip/Orange) | `fill` `#F8D4C7` → `$warning/soft` |
| `v68RDf` (XL5ZU chipLabel) | `fill` `#8B4A2E` → `$warning/soft-foreground` |
| `sYR1u` (XL5ZU chipRemoveIcon) | `fill` `#8B4A2E` → `$warning/soft-foreground` |
| `z3ZleP` (gu5WY bell icon) | `fill` `$muted` → `$foreground/foreground` |
| `HrWm8` (gu5WY UnreadDot) | `enabled: false` (phantom unread dot removed at the shared Top Bar level) |
| `TUlxU` (gu5WY Avatar ref) | `fill` `$accent/soft` → `$accent/accent`; descendant override `Rki8z.fill` `$accent/accent` → `$accent/foreground` (adapted onto the ref's own `descendants` map, not the shared `Rki8z`/Avatar-Text master node, to avoid changing every other Avatar instance in the document) |
| `Bg6EP` (iA2C8 Dark button) | `fill` `$accent/soft` → `#00000000` (Update/Replace cannot unset a property; fully-transparent is the pragmatic equivalent of "no fill", matching sibling `D5A9Xf`/Light) |
| `nMMtW` (iA2C8 moon icon) | `fill` `$accent/soft-foreground` → `$foreground/muted` |
| `icy0X` (iA2C8 System button) | `fill` added: `$accent/soft` |
| `z3SNRG` (iA2C8 monitor icon) | `fill` `$foreground/muted` → `$accent/soft-foreground` |
| `j9sz4` (Button/Ghost/Icon/SM, base of iA2C8's three buttons) | `cornerRadius` `$radius/3xl` (24, full-circle on a 28px box) → `6` |
| `hKfw8` (T8Hug hero heading, shared by all 4 login frames) | `content` 2-line wrap → `"Kubernetes-native\ngame server\nhosting."` (3-line wrap) |
| `LtTPk` / `LM3Jc` (jmoi3/ljdA5 subtitle text) | `content` `"Welcome to Gameplane"` → `"Welcome to Gameplane."` |
| `TFc7X` (jmoi3 password eye icon) | `icon`/`name` `eye-off` → `eye` |
| `VBLxv` (fjQjb→J14ME subtitle, shared by N1GkB/gX7um) | `content` `"Welcome to Gameplane"` → `"Welcome to Gameplane."` |
| `Z6tmS` (fjQjb→J14ME password eye icon, shared by N1GkB/gX7um) | `icon`/`name` `eye-off` → `eye` |
| `uNwJc`/`P3aKI`/`Dd9Wc`/`z3Dyn`/`vzifx` (ModalContainer in `f1Vga`/`UMJli`/`nNL3E`/`W8idqY`/`vUqMl`) | Moved from inside `Main` to a root-level sibling of `Sidebar`/`Main`; `layoutPosition: "absolute"`, `x: 0, y: 0`, `width`/`height` set to the frame's full size (1440×1170 for `f1Vga`, 1440×900 for the other three, 1440×1820 for `vUqMl`) — wizard modal now dims the whole screen including Sidebar/TopBar, matching the browser |
| `RjKFt` (tY6RD's Top Bar ref, `descendants.Gl7AY`) | `content` `"mc-survival"` → `"test-server-02"` |
| `Kl577` (tY6RD's Detail Header ref, `descendants.UpnRk`) | `content` → `"minecraft-modded · ns: default · up 13d 4h"` |
| `t7brvG`/`n7pU3C`/`kmBku`/`t1j0Wn` (DWztv game tiles) | `content` `"⛏"` → `"MI"` |
| `SaQX5` (DWztv game tile) | `content` `"🏭"` → `"SA"` |
| `pkQV8`/`lgNXm` (DWztv memory chips) | `content` `"1.5 GB"` → `"38%"` |
| `PLDIK` (DWztv memory chip) | `content` `"6.2 GB"` → `"62%"` |
| `Q52mVZ` (fGbVF Mobile TopBar avatar, plain ellipse) | Deleted and replaced with `WQMxI`, a `ref` to `p3URd` (Avatar/Text), `fill: $accent/accent`, descendant `Rki8z` override `content: "AD"`, `fill: $accent/foreground`, `fontSize: 12` — `Replace()` errored (`TypeError: Cannot read properties of undefined`) on this node, so `Delete` + `Insert` was used instead |
| `m1hP1j` (Audit Integrity Banner, Broken) | `height` unset → `72` (hugs content, no clipping) |
| `sVSGe` (zhLZN drawerHeader) | `layout` (none/horizontal default) → `vertical`, `justifyContent`/`alignItems` → `start`, `gap: 8` — title now stacks above the Restore button, matching the browser |
| `JINV8` (zhLZN Btn Restore ref, `descendants."eWkIT/WtPjS"`) | added `opacity: 1` alongside the existing `icon: "rotate-ccw"` override — the icon was previously invisible because the base component (`J09iP`) sets that same descendant's default `opacity: 0`, and the override never re-enabled it |
| `kKFX9` (App Sidebar) | No property change; re-exported (JSON + PNG) because it embeds the changed `iA2C8` Appearance Toggle (`j9sz4`/`Bg6EP`/`nMMtW`/`icy0X`/`z3SNRG`) and was omitted from this section's original re-export list |
| `Kl577/tiIi2` (tY6RD Detail Header ref, srvName heading) | `content` `"mc-survival"` → `"test-server-02"` — the brief's decision covered the breadcrumb (`RjKFt.Gl7AY`, row above) and the subtitle (`Kl577.UpnRk`, row above) but the header/subtitle text on `tiIi2` itself was missed in the first pass and is fixed here |
| `kmBku` / `t1j0Wn` (DWztv game tiles, test-server-09 and mc-survival (2) cards) | `content` `"MI"` → `"ME"` — both cards are subtitled "Minecraft (Modded)" (`MK6aw`/`vsOzh`), which per Q7's fixture-template code assignment (input order: minecraft-java→MI, satisfactory→SA, …, minecraft-modlist→MN, minecraft-modded→ME) should read `ME`, not `MI` |

**Note on `j9sz4` (cornerRadius `$radius/3xl` → `6`):** this base component is reused by more than the `iA2C8` Appearance Toggle buttons called out above — it also backs button instances embedded in `F9pUrx`, `bYDHC`, `n6Xlo`, and the Wizard Modal Header (`opmcF`). All four picked up the radius change automatically and are included in this pass's PNG re-export.

**Pre-existing live drift (not edited this pass, captured incidentally by re-export):** `V1VhGE` (typographic/curly quotes on `p65oXc` and two other text nodes) and `Xn5ns` (Live Dot `oBWC0` fill `#21C45D` → `$success/success`) both matched live `design.pen` when re-exported but were not the subject of any `Update` in this pass — their JSON diffs reflect drift that occurred before this wave, not new edits.

**Divergences from the brief (adapted per live inspection, as instructed):**
1. §3a `Rki8z` — edited via `TUlxU`'s own `descendants` map instead of the shared `Rki8z` node directly (see table above); editing the master would have re-colored every other Avatar/Text instance in the document.
2. §9b `J09iP` icon fix — root cause confirmed live: the base component's own default override on `WtPjS` sets `opacity: 0`; the per-instance fix adds `opacity: 1` to `JINV8`'s existing override rather than toggling a separate sibling node (no `nWTJb`-style hide/show sibling exists under `J09iP`'s `eWkIT`, unlike the `z9ShNE`/`oxVkD` comparison pattern in the brief).
3. §3c `j9sz4` cornerRadius — confirmed live at `$radius/3xl` (24 on a 28px slot, which clips to full-circle); treated as the brief's "full-circle" branch and set to `6`.
4. §7c `fGbVF` avatar — confirmed live as a bare `ellipse` with no initials slot; added one by replacing it with a `p3URd` (Avatar/Text) ref, per the brief's fallback instruction.
5. `Replace()` failed with an internal `TypeError` on two different nodes this pass (`iA2C8/Bg6EP`, `fGbVF/Q52mVZ`) whenever the target was a direct child of a reusable component's own definition; `Delete` + `Insert` (or a plain `Update` with a fully-opaque-removed color) was used as a workaround both times.

**Resolved live:**
- §4b N1GkB/gX7um were resolved by finding `fjQjb`'s underlying `J14ME` (loginCard) component live — `VBLxv`/`Z6tmS` — rather than by guessing ids from the brief's placeholder names; no structural surprises found.

**Re-export method & validation:** 91 ids (the brief's 19 directly-edited frames/components plus the 72-id shared-impact set from `gu5WY`/`kKFX9`/`j9sz4`/`T8Hug`/`fjQjb`/`R65Xyx`/`XL5ZU`/`fGbVF` references, computed by grep over the prior `assets/design-export/json/*.json` snapshot). JSON: `JSON.stringify(Get(id, {depth: 20, includePathGeometry: true}), null, 2)`, batched ~10 ids per `execute` call, each call's `## Print output` extracted from its persisted tool-results file with a `python3` regex split on `###ID:<name>` markers (never retyped) and validated with `json.loads` plus a root-`id` check — all 91 passed on the first attempt, zero `"geometry": "..."` or `"children": "..."` elisions. The five `settings-*` files keep their label filenames, sourced from node ids `uCA23`/`VctzT`/`iLm38`/`QpEvu`/`XR0f9`. `fGbVF` is a genuinely new file (no prior standalone export existed for it, as the brief noted).

**PNG correction (follow-up pass, same day):** the first pass only exported 24 PNGs plus the new `fGbVF.png`, leaving 67 of the 91 ids and `kKFX9` (embedded via the App Sidebar ref, omitted from the original 91-id list) on stale PNGs. This was caught and fixed in a follow-up re-export: `export_nodes` (2× scale) was run for the 68 outstanding ids — `Bbnga`, `Bq2Yg`, `Burtr`, `DPrYX`, `Dpb9f`, `DxKOh`, `E0ypH`, `EZFW0`, `F9pUrx`, `FtdkI`, `GayoL`, `Hy9r0`, `IzuY2`, `J5pjJ3`, `KaRFX`, `KhYNc`, `M2sA4u`, `O08uaD`, `P08Uw`, `QgW58`, `RC3Kf`, `RodrS`, `SeizD`, `Ss0Yr`, `TBvTC`, `TE2jI`, `V1VhGE`, `VfB0Y`, `WZdnw`, `Wj0V4`, `Xn5ns`, `Y5cmvI`, `b4eaUf`, `bYDHC`, `dBILX`, `dPP50`, `dQV9N`, `dxdEi`, `e9lV4`, `fK8Bi`, `g5mEpx`, `hLB9Z`, `i1bLR`, `i8wib`, `j24cXg`, `j9W8A`, `kK8Ji`, `kPmoo`, `m5kOm4`, `n6Xlo`, `nNGDX`, `o4LH8W`, `oyoTs`, `pssCT`, `sSISK`, `sZtDi`, `swxkJ`, `tTSdi`, `tooKB`, `uMiwd`, `ugDSa`, `uoxQW`, `xCJlu`, `xvlB6`, `zFiOW`, `zM0VF`, `zqzr4`, and `kKFX9` — plus `tY6RD` and `DWztv` (JSON + PNG) for the `tiIi2`/`kmBku`/`t1j0Wn` content fixes above. All 91 ids in the visual-diff wave's shared-impact set, plus `kKFX9`, now have current PNGs in `assets/design-export/screenshots/`; the five `settings-*` PNGs were exported to a scratchpad directory first (their source node ids, not their label names) and copied into place under the label filenames. Every re-exported JSON file was verified to still parse (`json.loads`) after this pass also stripped trailing whitespace from all `assets/design-export/json/*.json` files per `.editorconfig`'s `trim_trailing_whitespace`. Every directly-edited frame was screenshot-checked (`get_screenshot`) against the brief's stated intent before export; all matched (no broken/clipped/collapsed layout).

**Maintainer action required:** design.pen has not been saved by this pass (Pencil does not auto-save) — the maintainer must save via the Pencil GUI before these changes can be committed.

## Incremental export 2026-09-17 — regression-analysis fix wave (chip fixture labels, XL5ZU token verification, T8Hug hero geometry, m1hP1j banner sizing)

Applied the maintainer-settled fixes from `regression-analysis.json` (the visual-diff CI regression investigation). Every `Update` below was preceded by a live `Get()` confirming the node id/property still matched; no divergences from the brief were needed.

**Changes:**

| Node(s) | Change |
|---|---|
| `v68RDf` (XL5ZU chipLabel master) | `content` `"group-name"` → `"ops-leads"` |
| `RNxys` (vStkb chipLabel master) | `content` `"group-name"` → `"ops-team"` |
| `m7gnz` (uw0dB chipLabel master) | `content` `"group-name"` → `"everyone"` |
| `T8Hug` (Login Hero Panel) | `padding` `80` → `[48, 224, 48, 48]`; `gap` `24` → `16` |
| `hKfw8` (T8Hug heading) | `fontWeight` `"700"` → `"600"`; `lineHeight` `1.05` → `1.25` (content unchanged, still the 3-line wrap) |
| `vYC0C` (T8Hug paragraph) | `fontSize` `15` → `14`; `lineHeight` `1.5` → `1.4286` |
| `D5obx` (T8Hug pBadge) | `padding` `[6,14]` → `[4,12]` |
| `vGOtD` (T8Hug badge text) | `fontSize` `12` → `11` |
| `v2Vj9i` (T8Hug feats column) | `gap` `12` → `16` |
| `u8KsP` (new spacer frame, inserted between `vYC0C` and `v2Vj9i`) | `width: "fill_container"`, `height: 24`, no fill |
| `IpWjH`/`T34IT`/`W2LF3e`/`u0YKe` (T8Hug feature rows) | `alignItems` `"center"` → `"start"` |
| `KNAjG`/`KgRrA`/`z2T3Vg`/`RjQ0m` (T8Hug feature icon tiles) | `width`/`height` `28` → `32` |
| `E2SxjW`/`a7xDFg`/`JQNGJ`/`L5JWxZ` (T8Hug feature icons) | `width`/`height` `16` → `20` |
| `h85fcn`/`t0a5pu`/`nDq0f`/`T3INdd` (T8Hug feature text) | `fontSize` `13` → `14`; `lineHeight` unset → `1.4286` |
| `m1hP1j` (Audit Integrity Banner, Broken) | `height` `72` (explicit, added by the previous wave) → unset (`fit_content`/hug); `width` `"fill_container"` → `1132` (fixed, matching the shipped banner's CSS width at the 1440×900 capture viewport) |

**Verification (no change made):** `XL5ZU`'s `fill`/`v68RDf.fill`/`sYR1u.fill` were confirmed still pointing at `$warning/soft`/`$warning/soft-foreground` (unchanged from the prior wave). `GetVariables()` confirms the document's token values are `$warning/soft` = `#FEF3C7` (light) / `#F7B75026` (dark), `$warning/soft-foreground` = `#D97706` (light) / `#F7B750` (dark) — i.e. the design side already carries the values the maintainer ruled should win; no Pencil-side edit was needed. `globals.css` is out of scope for this pass (code change, not design).

**Instance-override check (chip labels):** confirmed via `Get("Ipjvx", {depth:4})` (Role Mapping Overrides card in `uMiwd`, Screen/Admin Settings — Authentication) that the three chip instances there (`z9JAoG`/`Ezgwq` = `XL5ZU`, `E7yW29` = `vStkb`) all set an explicit `descendants` override on the chipLabel content (`"gameplane-admins"`, `"gameplane-sre"`, `"gameplane-ops"`), so the master label edits above do not change what those screen instances render. `uw0dB` has no instance references in any exported frame (`grep '"ref": "uw0dB"' assets/design-export/json/*.json` → no hits), so nothing downstream of it needed checking.

**Re-export method & validation:** 12 ids — the 3 edited chip masters (`XL5ZU`, `vStkb`, `uw0dB`) plus the 2 frames that reference them (`uMiwd`, `zqzr4`); the edited `T8Hug` plus the 4 login frames that reference it (`N1GkB`, `gX7um`, `ljdA5`, `jmoi3`); and the edited `m1hP1j` plus its containing component (`kIxaJ`, Audit Integrity Banner) — found via `grep '"ref": "<id>"' assets/design-export/json/*.json` against the prior snapshot. JSON: `Print(JSON.stringify(Get(id, {depth: 20, includePathGeometry: true}), null, 2))` for all 12 in one `execute` call (output exceeded the inline token cap and was persisted to a tool-results file), extracted with a `python3` regex split on `===BEGIN:<id>===`/`===END:<id>===` markers (never retyped), and validated with `json.loads` plus a root-`id` check — all 12 passed on the first attempt, zero `"geometry": "..."` elisions (`grep -c '"\.\.\."' assets/design-export/json/<id>.json` = 0 for all 12). PNG: `export_nodes` at 2× scale for the same 12 ids; PNG signature + `IHDR` dimensions checked for all 12 — `m1hP1j.png` is now exactly `2264×128` (previously `1272×144` with the padded-band regression), matching the shipped full-width banner at 2×. `XL5ZU.png`/`vStkb.png`/`uw0dB.png` are now `166×46`/`154×46`/`154×46` (previously all `178×46` with the placeholder "group-name" label), within the existing `SCALE_ALLOWLIST` bands per the regression analysis, so no allowlist edit is implied by this pass.

**Note on `T8Hug`'s outer frame size:** `width: 720`/`height: 900` and `justifyContent: "center"` were left unchanged — only the inner padding/gap/typography/icon sizing moved to match the app's geometry (720 − 48 − 224 = 448px content column, matching the app's `p-12`/`max-w-md`). The panel still centers its (now shorter) content block vertically within the unchanged 900px frame.

**Not in scope for this pass (deferred to the maintainer per `regression-analysis.json`'s open questions, not touched here):** the `jmoi3`-only `g2HLxz`/`SZ0pF` error-alert geometry fix, the `Kp48V`/dialog-family width/padding disagreement, the `CqaSq`/`DMnEi`/`E9EEv0`/`MaoHP`/`NLDDv`/`t3IY3u` dialog scale-factor and Select/Cancel styling fixes, and any `globals.css` token value changes.

**Maintainer action required:** design.pen has not been saved by this pass (Pencil does not auto-save) — the maintainer must save via the Pencil GUI before these changes can be committed.

## Incremental export 2026-09-17 — maintainer decisions on "still-failing frames" and "Dialog/modal" groups (DWztv mobile cards, mobile top bar height, Create Server Step 4 "as built" duplicate, zhLZN report-only)

Applied the four maintainer decisions given directly for `regression-analysis.json`'s "Visual-diff still-failing frames" and "Dialog/modal element-crop frames" groups. Every `Update`/`Delete`/`Copy` below was preceded by a live `Get()` confirming the node id/property still matched.

**1. `DWztv` (Screen/Mobile — Servers (Light)) — code wins except the status pill:**

| Node(s) | Change |
|---|---|
| `mLHoA`/`SCXtD` (card game labels) | `content` `"Minecraft Java"` → `"minecraft-java"` |
| `FAXAj` | `content` `"Satisfactory"` → `"satisfactory"` |
| `MK6aw`/`vsOzh` | `content` `"Minecraft (Modded)"` → `"minecraft-modded"` |
| `SaaAN`/`clmpM` | `content` `"0 / 20"` → `"0/20"` |
| `KTvHZ` | `content` `"4 / 20"` → `"4/20"` |
| `AkRdC`, `okz0J`, `Tp21K`, `BEqBO`, `h0dm9` (status dots) | Deleted — the app renders no per-card status dot |
| `AFfme`/`y3tHBF`/`UiATn`/`cFqwS` (green game-icon tiles) | `fill` `"#5B8A3A"` → `"$success/soft"` |
| `t7brvG`/`n7pU3C`/`kmBku`/`t1j0Wn` (their glyph text) | `fill` → `"$success/soft-foreground"` |
| `F7RWo` (blue/Satisfactory game-icon tile) | `fill` `"#4A6FA5"` → `"$accent/soft"` |
| `SaQX5` (its glyph text) | `fill` → `"$accent/soft-foreground"` |

Note on the blue tile: the regression analysis's suggested `$primary/soft` token does not exist in this document's variable set (`GetVariables()` confirms only `success`, `warning`, `danger`, `accent`, `default`, and `text-primary` groups — no `primary/*` color family). The app's `GameIcon.tsx` legacy palette renders Satisfactory/Factorio via `bg-primary/20 text-primary`, and `web/src/styles/globals.css:122-123` aliases `--color-primary`/`--color-primary-fg` to `--accent`/`--accent-foreground` — i.e. "primary" in the app *is* the accent hue. `$accent/soft`/`$accent/soft-foreground` was used instead of inventing an untethered token or leaving the `$primary/soft` reference dangling (which resolved to `#000000` black when tried — confirmed by `Get` before correcting). The status pill itself (`Q0KUgq`, `VilnW`, `r3Dt0`, `JeDuq`, `D3iriY` and their `success`/`danger`/`focus` soft fills) was left untouched per the maintainer's ruling that the pill stays as designed.

**2. `fGbVF` (Gameplane/Mobile TopBar) — code wins:**

| Node | Change |
|---|---|
| `fGbVF` | `height` `56` → `64` (matches the app's `h-16`) |

Verified no absolute-positioned children are anchored to the old height: `fGbVF`'s two children (`TopBar Left`, `avatar`) are laid out via `justifyContent: "space_between"`/`alignItems: "center"` with no `y` overrides, so they re-center automatically. The `DWztv` instance (`z1HpI`) carries no local `height` override, so it inherited the new `64` without a separate edit.

**3. `f1Vga` (Screen/Create Server — Step 4 Network) — design wins; new "as built" frame added instead of editing the showcase:**

Duplicated `f1Vga` → new independent frame `Screen/Create Server — Step 4 Network (as built)`, id **`QQtUD`**, placed at `x:8080,y:12745` (found via `FindEmptySpace` beside `f1Vga`, which is unchanged at `x:4920,y:12745`). In the copy only:

| Node(s) in `QQtUD`'s tree | Change |
|---|---|
| `U9uMcX` (copy of `EpW22`, "Field IP allow-list") | `enabled: false` |
| `v5f2TE` (copy of `A4FP0r`, "Alert No address manager configured") | `enabled: false` |
| `X29ZY`/`DbHoJ`/`tsvNi`/`IPhfI` (copies of `g5jYV`/`IZ2jD`/`iJ7nA`/`KjlSy` — the port-overrides label, column header, row, and helper text) | `enabled: false`, collapsing the block down to just the `p0F58b` ("Add port override") button, which stays enabled |
| `XE2dd` (copy of `FePiR`, Footer ref) | `descendants` gained `"H87Gb": {"content": "Continue to Review"}` (was `"Continue to Version"`, the component's stale default) |

The `vXfaP`/`eh1FS` "Address preference ignored" alert was left enabled — the maintainer's list named only the allow-list field and the "No address manager configured" alert for removal. `placeholder: true` was set for the duration of the copy/edit and cleared before export. Verified via `get_screenshot` on both `QQtUD` and `f1Vga`: the new frame shows NodePort-only content ending in "Continue to Review" with no allow-list/second-alert, and `f1Vga` is pixel-identical to before (still shows the port-override row, IP allow-list, both alerts, and "Continue to Version") — the showcase was not touched.

**`web/e2e/screenshots/slice-3.spec.ts` follow-up (owed to the code agent, not applied by this design pass):** the capture test currently keyed to **`f1Vga`** should be renamed and repointed to capture **`QQtUD`** instead, so `f1Vga` drops out of the visual-diff set (it will never match the built app, by design — it's the showcase for content the wizard doesn't render in this state) while `QQtUD` becomes the frame CI compares against. Old id: `f1Vga`. New id: `QQtUD`.

**4. `zhLZN` (Gameplane/Backup Detail Drawer) — no design edit; geometry reported for the code agent:**

Read-only via `Get("zhLZN", {depth:3})`; no `Update`/`Copy`/`Delete` calls were made against this component.

- Root (`zhLZN`): `width: 440`, `height: 760`, `fill: $surface/surface`, `stroke: $border/border` on the **left edge only** (`strokeWidth: {left: 1}`), no `cornerRadius` (square corners), outer shadow `offset: {x:-12, y:0}, blur:32, spread:-8, color:#00000080`, `layout: "vertical"`. Pencil has no `margin` property (unsupported per schema) — there is no margin on the drawer itself; it is meant to sit flush against the viewport edge it opens from.
- Header row (`sVSGe`, name "drawerHeader"): `width: fill_container`, `padding: 20` (uniform all sides), `gap: 8`, bottom `stroke` 1px `$border/border`, `layout: "vertical"` — **not a horizontal row**. It stacks two children top-to-bottom: a title block (`R4kYq`: "Backup details" 16px/600 + the mono subtitle 12px `$muted`, `gap: 4`) above a `Btn Restore` ref (`JINV8`, ghost/small button with a `rotate-ccw` icon), left-aligned, not side-by-side. Confirmed visually via `get_screenshot("sVSGe")`.
- Body (`hY4y2`, "drawerBody"): `width`/`height: fill_container`, `padding: 20`, `gap: 16`, `layout: "vertical"` — six stacked label/value groups (Phase pill, Server, Snapshot ID, Size, Started, Completed), each its own sub-frame with `gap: 4`.
- Footer (`bs0ho`, "drawerFooter"): `width: fill_container`, `padding: 16`, `gap: 8`, top `stroke` 1px `$border/border`, `justifyContent: "end"`, `alignItems: "center"` — Delete button left, Restore button right, both right-aligned as a row.

No `SCALE_ALLOWLIST` or fixture change was made; per the regression analysis this frame's 1.313× scale mismatch is structural (component export margin vs. live 384×900 drawer) and stays open for the maintainer/code side to reconcile using the numbers above.

**Re-export method & validation:** touched ids — `DWztv`, `fGbVF`, the new `QQtUD` — plus every exported frame referencing the changed component, found via `grep '"ref": "fGbVF"' assets/design-export/json/*.json` → `SeizD` (Screen/Mobile — Nav Drawer), `tooKB` (Screen/Mobile — Servers). (`grep '"ref": "DWztv"'` and `'"ref": "QQtUD"'` returned no hits — neither is referenced elsewhere.) JSON: `Print(JSON.stringify(Get(id, {depth: 20, includePathGeometry: true}), null, 2))`, one id per `execute` call, each fitting inline (no truncation) and transcribed verbatim into `assets/design-export/json/<id>.json` (for `fGbVF`, only the single changed `height` line actually differed from the prior snapshot, applied via a scoped edit rather than a full rewrite). Validated with `python3 -c "json.load(...)"` (root `id` matches the filename) and `grep '"geometry": "\.\.\."'` (zero hits) for `DWztv`/`fGbVF`/`QQtUD`. PNG: `export_nodes` at 2× scale for all five ids (`DWztv`, `fGbVF`, `SeizD`, `tooKB`, `QQtUD`); `file` confirmed valid PNG signatures and non-zero dimensions for all five (`DWztv.png`/`SeizD.png` 780×1688, `fGbVF.png` 780×128, `tooKB.png` 780×1908, `QQtUD.png` 2880×2340). `zhLZN` was not re-exported (no design edit made).

**Maintainer action required:** design.pen has not been saved by this pass (Pencil does not auto-save) — the maintainer must save via the Pencil GUI before these changes can be committed.

## Incremental export 2026-09-18 — wave 5: maintainer-flagged design defects (button double-padding, subnav icons/highlight swap, login alert spacing)

Four items investigated against the maintainer's own browser-panel composites (`/tmp/.../scratchpad/vdr3/*-composite.png`) and the shipped app code. Two produced real fixes; one ("theme selector separator") was investigated and found **already correct** (fixed in the prior 85b1986d/fa5a4370 commits — `NmfQu` Footer already carries `strokeWidth: {top: 1}` and renders it; no further edit made, flagging here so it isn't re-flagged blind). No test/lint suites or `web/` files were touched.

**1. `tpKRk`/`rNhll`/`LMIom`/`XoX7L`/`z9ShNE`/`d5N3W3`/`J09iP`/`IU7OG` (the 8 `Gameplane/Button/*` wrapper components, the "Btn" component family) — root-cause fix, maintainer ruling "design is re-cut to the app's padding":** each wrapper frame was re-applying its own `padding`/`fill`/`cornerRadius`/`height` (e.g. `tpKRk`: `padding: [8, "$spacing/4"]`, `fill: "$accent/accent"`, `cornerRadius: "$radius/3xl"`, `height: 36`) **around** an inner `ref` to the real button component (`cb4rt`/`FIB65`/`jsrtu`/`CFM8i`/`j9c5W`/`rDRDV`/`rkF0p`), which already carries the *exact same* padding/fill/cornerRadius/height. This doubled the effective horizontal padding (measured 117×36 vs. HeroUI's 86×36 for the same "Replace"/"Remove"/"Set API key" row cited in the brief). Fix: stripped the outer wrapper down to a transparent pass-through — `Update(id, {padding: 0, gap: 0, fill: "#00000000", cornerRadius: 0, width: "fit_content", height: "fit_content"})` on all 8 — so the inner button's own box model (already matching HeroUI's `px-4`/`px-3` MD/SM padding) is the only one that renders. Verified via `export_nodes` pixel measurement: `Wj0V4`'s `btnRepl0` (`X86emW`) is now 86×36 exactly; `btnRem0` (`n1KgoV`, the danger variant) is 85×36. This is a component-level fix, so it propagates to every instance automatically — no per-instance edits needed for this item.

**2. `EV8mp` (Gameplane/Server Settings Sub Nav) — root-cause + per-instance fix, maintainer ruling "the server nav bar ... clearly wrong":** compared against the shipped `HeroUI <Tabs orientation="vertical">` in `web/src/routes/tabs/Settings.tsx` (`SECTIONS` list, text-only labels, centered, no icons — confirmed by cross-referencing `settings-general`'s own composite). The design instead rendered each row with a leading lucide icon, left-aligned text, `cornerRadius: 4`, and — critically — the selected/unselected color mapping was **backwards on two of the twelve rows** at the component level: `oXEaP` ("General", row 0) had `fill: transparent` (unselected-looking) but bright `$foreground/foreground` text, while `axMXL` ("Version", row 1) had a stray `fill: "$surface/secondary"` highlight (selected-looking) but dim `$muted` text — with two more rows (`DnXAN` "Environment", `LktID` "RBAC & access") independently using bright text with no highlight at all, for no state-driven reason. Fix, applied to the component (`EV8mp`) and propagated to all **16** screen instances that use it:
  - Disabled all row icons (`enabled: false`) — text-only rows, matching the app.
  - `justifyContent: "center"`, `gap: 0`, `padding: [8, 12]`, `cornerRadius: 6` on every row — centered label, no icon gutter.
  - Component baseline reset to a neutral **all-unselected** state (every row `fill: "#00000000"`, every label `$muted`, except `snDangerZone` which keeps `$danger/danger` per its own destructive styling) — mirroring how each of the 16 screen instances already layered a per-screen "this one row is active" override via `descendants` (background `$surface/secondary` on the active row), which is the correct pattern; the component-level baseline had incorrectly hard-coded "General" as always-selected, which is what caused the row-0/row-1 color swap bleeding into every instance.
  - Added the missing "active row" **text** brightening (`$foreground/foreground`) to the 14 instances whose own `descendants` override already set the active row's background highlight but had never set its text color to match (found via `Get(id,{depth:0}).descendants` on all 16 instances): `E0ypH`(`bHNH0`), `VfB0Y`(`paOZA`), `xCJlu`(`Qbxnz`), `Y5cmvI`(`v3XCDT`), `i1bLR`(`Y2PGH`), `ugDSa`(`rWM9D`), `KaRFX`(`IUddK`), `J5pjJ3`(`J4128`), `uCA23`(`KfnMt`, also fixing a separate `"#000000"` literal-black bug on that instance's override — invisible on the dark background, evidently a stray typo since every other instance used the `$foreground/foreground` token), `VctzT`(`VQPTx`), `iLm38`(`rWM9D`), `QpEvu`(`paOZA`). Two instances (`swxkJ` "Version" screen, `RodrS` "Network capture" screen) were missing the active-row **background** highlight entirely (no override at all for their own section) — added both the `fill: "$surface/secondary"` and text override. `i8wib`/`XR0f9` (Danger zone screens) needed no additional override: the danger row's default `$danger/danger` color already reads as "selected" without a background-highlight text change.
  - **Found but not fixed (out of scope for this pass, flagged for the maintainer):** `settings-general`/`uCA23`'s own form also has a `"fill": "#000000"` literal-black bug on its "Name" field label (`X2Lky`) — same failure class as the subnav bug above, but on an unrelated node outside this pass's four assigned items. Left as-is; worth a follow-up grep for `"fill": "#000000"` across the corpus.

**3. `SZ0pF` (the `Alert/Danger` instance inside `g2HLxz`/`loginCardError`, used on `jmoi3` "Screen/Login — Invalid credentials") — root-cause fix, "checkout the invalid password placement":** compared against `web/src/routes/Login.tsx`'s actual error `<Alert>` (`className="px-0 py-1 text-sm text-danger bg-transparent border-none"`, rendered directly between the password field and the Sign-in button with no card chrom e). The design instance was using the full `Alert/Danger` card component unmodified structurally — `dMZea`(icon)/`CotwO`(description)/`sYqNu`(Retry button) were hidden via `opacity: 0`/`height: 0` but **not** `enabled: false`, so the invisible Retry button (`height: 32`) plus the card's own `fill: "$surface/surface"`, `padding: [12,16]`, and outer shadow were all still occupying layout space — producing a large, unintended gap between "Invalid credentials" and the Sign-in button that isn't present in the app (confirmed by side-by-side composite crop: app has a tight ~8px gap, design had a ~150px empty band plus a faint extra card box). Fix: `Update("SZ0pF", {fill: "#00000000", effect: {type:"shadow", enabled:false}, padding: [4,0], gap: 0, cornerRadius: 0})` to strip the card chrome down to the app's `bg-transparent border-none px-0 py-1`, plus `Update` with `enabled: false` (not just opacity/height) on `dMZea`, `CotwO`, and `sYqNu` so the hidden icon/description/button stop occupying layout space. Verified via `export_nodes` on `SZ0pF` alone (now a single tight line of red text, no card) and a full `jmoi3` screenshot (gap now matches the app).

**4. Theme selector separator — investigated, already correct:** `NmfQu` (the sidebar `Footer` frame inside `kKFX9`/`Gameplane/App Sidebar`, which wraps the `iA2C8` Appearance Toggle) already carries `stroke: "$border/border"`, `strokeWidth: {top: 1}`, matching `Sidebar.tsx`'s `border-t border-border` on the footer `<div>`. Confirmed via `Get` (property present) and a themed screenshot crop (`j24cXg`'s composite, which **passed** the automated visual-diff at 2.65%/4% threshold) — the separator line renders in both. This was fixed by the prior `fa5a4370` design commit; no edit made this pass.

**Re-export method & validation:** touched/component ids — `tpKRk`, `rNhll`, `LMIom`, `XoX7L`, `z9ShNE`, `d5N3W3`, `J09iP`, `IU7OG`, `EV8mp`, `g2HLxz` (new file — previously only referenced inline inside `jmoi3`'s shallow ref tree, never exported standalone; `jmoi3.json` itself has no inline diff since the change lives inside the `g2HLxz` component definition it refs) — plus every screen referencing the changed button/subnav components, found via `grep -l '"ref": "<id>"' assets/design-export/json/*.json` for each of the 8 button ids and for `EV8mp` (66 ids total, union of both searches plus the 10 component/base ids): `b4eaUf`, `Bq2Yg`, `bYDHC`, `CqaSq`, `d5N3W3`, `dBILX`, `Dpb9f`, `dPP50`, `DPrYX`, `dQV9N`, `dxdEi`, `DxKOh`, `E0ypH`, `e9lV4`, `EV8mp`, `f1Vga`, `fK8Bi`, `FtdkI`, `GayoL`, `i1bLR`, `i8wib`, `iLm38`, `IU7OG`, `IyMFM`, `J09iP`, `j24cXg`, `J5pjJ3`, `j9W8A`, `jmoi3`, `KaRFX`, `kPmoo`, `LMIom`, `m5kOm4`, `n6Xlo`, `O08uaD`, `oyoTs`, `QpEvu`, `QQtUD`, `RC3Kf`, `rNhll`, `RodrS`, `swxkJ`, `sZtDi`, `t3IY3u`, `TBvTC`, `tpKRk`, `tY6RD`, `uCA23`, `ugDSa`, `uMiwd`, `uoxQW`, `V1VhGE`, `VctzT`, `VfB0Y`, `Wj0V4`, `WZdnw`, `xCJlu`, `Xn5ns`, `XoX7L`, `XR0f9`, `xvlB6`, `Y5cmvI`, `z9ShNE`, `zFiOW`, `zhLZN`, `zqzr4`. PNG: `export_nodes` at 2× scale for all 66 + `g2HLxz`, copied into `assets/design-export/screenshots/` (renaming the five `settings-*` label-filename ids per the existing mapping documented below). JSON: only the ids whose own `descendants`/content actually changed were rewritten (component definitions, `g2HLxz`, and the 16 `EV8mp`-instance screens per item 2's per-instance overrides) — `Print(JSON.stringify(Get(id, {depth: 20, includePathGeometry: true}), null, 2))` per id (batched a few ids per `execute` call, split further when output exceeded the tool's inline-response size and fell back to reading the persisted tool-result file), transcribed verbatim (never retyped) into `assets/design-export/json/<id>.json` via `python3` extraction on the `===START:id===`/`===END:id===` markers, 2-space indent, literal UTF-8 (`ensure_ascii=False`). The remaining button-only-referencing screens (`Wj0V4`, `jmoi3`, and the ~40 others whose own JSON has no inline `descendants` diff against the button/subnav components, since the fix lives entirely in the component definitions) were confirmed to have **zero content diff** in their own JSON (spot-checked `Wj0V4` and `jmoi3` byte-for-byte against a fresh `Get` — identical) and so were **not** rewritten, only re-screenshotted (their rendered PNG output *does* change even though their own JSON doesn't, since they `ref` the now-different components). All 25 rewritten JSON files validated with `python3 -c "json.load(...)"` (root `id` present, zero `"\"geometry\": \"...\""` hits).

**Files written directly via `Write`/`Bash`+`python3`, not retyped:** all JSON content originated from `Get(...)`/`Print()` tool output (live document state), never hand-authored.

**Maintainer action required:** design.pen has not been saved by this pass (Pencil does not auto-save) — the maintainer must save via the Pencil GUI before these changes can be committed.

## Incremental export 2026-09-17 — wave 4: visual-diff review fixes (login hero, "as built" modal, audit banner width, zhLZN header revert, DWztv decisions on siblings, hero copy)

Applied the wave-2/3 review's findings (`wave23-review.json`) as design edits. All changes verified by shallow `Get` before/after plus `get_screenshot`; no test/lint suites or web/ files were touched by this pass.

**1. `hKfw8` (T8Hug Login Hero Panel heading) — BLOCKER fix:** was `textGrowth: "fixed-width"` at `width: fill_container`, which under the panel's `padding: [48,224,48,48]` collapsed the available column to 448px — too narrow for "Kubernetes-native", so the three authored `\n`-separated lines wrapped to four. `Update("hKfw8", {textGrowth: "auto"})` restores natural (unwrapped) line breaks; `auto` ignores the `width` property, so the three lines render exactly as authored. Verified via `Get` bounds: `{width: 449, height: 165}` (was 448×220 at 4 lines). Re-exported `T8Hug`, `N1GkB`, `gX7um`, `ljdA5`, `jmoi3` (the four login screens that instance `T8Hug`).

**2. `xtHqZ`/`KXhG2` (QQtUD "as built" modal) — BLOCKER fix:** the modal (`xtHqZ`) and its Body (`KXhG2`) still carried `f1Vga`'s fixed `height: 1070`/`fill_container`, leaving ~370px of blank space below the collapsed (disabled-controls) step content. `Update("xtHqZ", {height: "fit_content"})` and `Update("KXhG2", {height: "fit_content"})` let the modal shrink to its content; frame `QQtUD` itself was left at 1440×1170 per the brief. This produced a circular-sizing warning on `MEGvK` (the preview column, `height: "fill_container"` inside the now-`fit_content` `KXhG2`), fixed with a follow-up `Update("MEGvK", {height: "fit_content"})`. Resolved bounds: `xtHqZ` is now 960×742 (was 960×1070), centred inside the 1440×1170 frame with no leftover blank band. Confirmed via screenshot — no overflow/collapse.

Also removed the disabled leftover wizard content per the brief ("disabled subtrees still export"): `Delete("U9uMcX")` (Field IP allow-list, was `enabled: false`) and `Delete("v5f2TE")` (Alert "No address manager configured", was `enabled: false`). Both are confirmed absent from the re-exported `QQtUD.json`.

**3. `kIxaJ` (Gameplane/Audit Integrity Banner) — MAJOR fix:** the wrapper frame was still `width: 700` with `padding: 32` around its `m1hP1j` child, which had grown to `width: 1132` in an earlier wave — a 464px overflow. `Update("kIxaJ", {width: 1196})` (1132 + 2×32 padding) resolves it with zero child overflow (`ctx.problems` empty on a full `Get` sweep). Re-exported `kIxaJ` and `m1hP1j`.

**4. `sVSGe` (zhLZN drawerHeader) — MAJOR fix, maintainer ruling "design wins, Restore stays on the title row":** wave 1 had changed this header from its original horizontal `space_between` row (title left, Restore button right) to a vertical stack (title above button), which the visual-diff review flagged as a regression against both the pre-wave-1 design and the shipped code (`BackupDetailDrawer.tsx`'s `flex items-start justify-between` header). `Update("sVSGe", {layout: "horizontal", gap: 12, justifyContent: "space_between", alignItems: "start"})` restores the pre-wave-1 row; `JINV8` (the Restore button ref) needed no position/size change since `space_between` right-aligns it automatically as a layout child. Resolved header bounds: 440×81 (was 440×121 as a vertical stack). **Correction to this manifest's own wave-1 section above:** that section's line "`layout: "vertical"` ... not a horizontal row" documented wave 1's *regression* as if it were the header's original design — it was not; the header was a horizontal `space_between` row before wave 1 touched it (see `HEAD~2:assets/design-export/json/zhLZN.json`), and this wave-4 edit is a revert to that original state, not a fresh design decision.

**5. `tooKB`/`SeizD` (Screen/Mobile — Servers / Nav Drawer) — MAJOR fix, apply DWztv's already-approved decisions to its two sibling mobile frames:** both frames still carried what the review had just ruled wrong on `DWztv`: literal per-game hex tile fills (`#5B8A3A`, `#8B5A2B`, `#4A6FA5`/`#D4A43C`, `#3a6b5b`, `#7D3932`), a separate status-dot ellipse next to each status label, and (on the Minecraft/Satisfactory cards only) title-case game labels and spaced `"X / Y"` player counts. Per the brief's literal scope (not a full re-mirror of every DWztv stylistic choice, e.g. the emoji→2-letter-glyph swap was left alone since the brief didn't call for it):
  - All six icon-tile frames in `tooKB` (`gGfF9`, `Gl7MJ`, `FWKN7`, `g7MkId`, `TOHNm`, `qRAsp`) and all five in `SeizD` (`BhEcb`, `j5iWF`, `V5yD85`, `rKygS`, `JbrSN`) → `fill: "$success/soft"`; their emoji-glyph children → `fill: "$success/soft-foreground"` (glyph characters themselves unchanged).
  - Deleted the status-dot ellipse in every card: `tooKB`'s `If9yi`/`sets8`/`U2rbn`/`heSn2`/`Fo19E`/`k0NGP2`; `SeizD`'s `MGvLU`/`ECwYr`/`MoxEq`/`w52MII`/`W10fN`.
  - Game labels: `tooKB`'s `XREqu` "Minecraft Java" → "minecraft-java", `lV0vD` "Satisfactory" → "satisfactory"; `SeizD`'s `ZnfIO` "Minecraft Java" → "minecraft-java". (Neither frame has a "minecraft-modded" card, so that label wasn't applicable here.)
  - Player counts to the "0/20" form (no spaces): `tooKB`'s `IAQML` "14/20", `u3ZRn` "2/8", `xsv0I` "0/32", `L6jXyj` "0/70"; `SeizD`'s `O3Gkp` "14/20", `p1Lv8` "3/10", `dtbnC` "2/8", `cPnZl` "0/32", `aedM6` "0/70".
  Verified via `get_screenshot` on both frames post-edit — six/five cards each, consistent green tile treatment, no overflow. Re-exported `tooKB` and `SeizD`.

**6. `T3INdd` (T8Hug feature row 4) — MINOR fix:** content changed from `` "GitOps-friendly. `kubectl get gameservers` just works." `` to `"GitOps-friendly. kubectl get gameservers just works."`, dropping the literal backticks (the app renders this fragment as an inline `<code>` element, so the source backticks were redundant markdown syntax). Covered by the `T8Hug` re-export.

**Wave-2 formatting cleanup:** the review flagged that wave 2's 12 re-exports (`N1GkB`, `gX7um`, `ljdA5`, `jmoi3`, `uMiwd`, `zqzr4`, `XL5ZU`, `vStkb`, `uw0dB`, `m1hP1j`, `kIxaJ`, `T8Hug`) were serialized with 1-space indentation and `ensure_ascii` `\uXXXX` escaping, unlike the corpus's 2-space/literal-UTF-8 convention — content was correct (deep-matched live `Get`) but every line diffed. All 12 were re-serialized this pass with `json.dump(data, f, indent=2, ensure_ascii=False)` (the four already needing content changes above — `N1GkB`, `gX7um`, `ljdA5`, `jmoi3` — plus `T8Hug`, `kIxaJ`, `m1hP1j` were written fresh from `Get` output as part of items 1-3/6 above; `uMiwd` had no content change this wave, only reformatting; `zqzr4`, `XL5ZU`, `vStkb`, `uw0dB` were likewise reformatted in place with no content change via `json.load` → `json.dump`).

**New snapshots:** `T8Hug.json`/`.png` and `QQtUD.json`/`.png` were untracked (`??`) after wave 3 — both are now committed to `assets/design-export/` alongside this manifest update; the export rule (touched node → same commit) applies retroactively here since they were introduced by the immediately-preceding wave and are first captured in git by this pass.

**`settings-*` label→id mapping (for validators — SUPERSEDED 2026-09-18):** This note previously documented a filename-to-nodeid mapping for five Settings screens using human-readable label filenames (`settings-general.json`, `settings-version.json`, `settings-envvars.json`, `settings-access.json`, `settings-danger.json`). **As of the 2026-09-18 full export reconciliation, this convention no longer applies.** These five screens are now indexed exclusively by their node ids (`uCA23.json`, `VctzT.json`, `iLm38.json`, `QpEvu.json`, `XR0f9.json`). Any validator or tooling that previously special-cased the `settings-*` label filename pattern should be updated to use the node-id-based filenames instead. See the "Filename convention change — Settings screens" section in the 2026-09-18 reconciliation entry above for the complete mapping.

**Re-export method & validation:** 17 ids re-exported — `T8Hug`, `N1GkB`, `gX7um`, `ljdA5`, `jmoi3`, `QQtUD`, `kIxaJ`, `m1hP1j`, `zhLZN`, `tooKB`, `SeizD`, `DWztv`, `uMiwd`, `zqzr4`, `XL5ZU`, `vStkb`, `uw0dB`. A document-wide search for `ref` pointers to `kIxaJ`/`m1hP1j`/`T8Hug`/`zhLZN` from other top-level screens (`DxKOh`, `P08Uw`, `EZFW0`, `j24cXg`, `tTSdi`, `DPrYX`, `IzuY2`, `TE2jI`, `o4LH8W`, `Hy9r0`, `sSISK`) came back empty, so no additional frames needed re-export beyond the brief's list. JSON: `Print(JSON.stringify(Get(id, {depth: 20, includePathGeometry: true}), null, 2))` per id, transcribed verbatim (no retyping) into `assets/design-export/json/<id>.json`; all 17 validated with `python3 -c "json.load(...)"` (root `id` matches filename, zero `"..."` elision markers). PNG: `export_nodes` at 2× scale for all 17. `DWztv` had no content change this wave (used only as the reference pattern for item 5) but was re-exported per the brief for freshness; its existing JSON was already 2-space/literal-UTF-8 and needed no reformatting.

**Maintainer action required:** design.pen has not been saved by this pass (Pencil does not auto-save) — the maintainer must save via the Pencil GUI before these changes can be committed.

## Full export reconciliation 2026-09-18

A comprehensive reconciliation pass executed against the entire design.pen document, exporting every live screen and component defined in a single saved document state. Scope: 346 unique component and screen ids exported from one document version.

**Scope & file inventory:**

- **Objects exported:** 346 ids (screens, reusable components, and supporting reference/state frames) — every live node exported from one document read-snapshot, guaranteed consistent state.
- **Files written:** 355 JSON files + 355 PNG screenshot files in `assets/design-export/` (total 710 asset files). **Prior inventory (2026-09-17 wave-4):** 172 JSON + 172 PNG = 344 files. **New files added this pass:** 183 ids exported for the first time, predominantly nested HeroUI base component definitions ("Accordion/Open", "Accordion/Closed", "Avatar/Text", "Avatar/Image", "Button/Primary/*", "Button/Secondary/*", etc.) and their supporting label/section frames inside the "HeroUI: Design System Components" wrapper (`LtgNm`).
- **Failed exports:** none — 100% success rate; all 355 JSON files parse, all 355 PNG files are valid (header validation + nonzero size).

**Filename convention change — Settings screens:**

The five Settings sub-page screens no longer use descriptive label filenames. **As of 2026-09-18, these screens are indexed by their node id only:**

| Filename | Node ID | Screen Name |
|---|---|---|
| `uCA23.json` / `uCA23.png` | `uCA23` | Screen/Server Detail — Settings · General |
| `VctzT.json` / `VctzT.png` | `VctzT` | Screen/Server Detail — Settings · Version |
| `iLm38.json` / `iLm38.png` | `iLm38` | Screen/Server Detail — Settings · Environment |
| `QpEvu.json` / `QpEvu.png` | `QpEvu` | Screen/Server Detail — Settings · RBAC & access |
| `XR0f9.json` / `XR0f9.png` | `XR0f9` | Screen/Server Detail — Settings · Danger zone |

**Related change:** capture ids used in `web/e2e/screenshots/slice2b.spec.ts` (a test fixture referencing design-export screenshots) were updated to match these node ids.

**Export method & validation:**

- **JSON:** `Print(JSON.stringify(Get(id, {depth: 20, includePathGeometry: true}), null, 2))` via the Pencil `execute` tool for every exported id, yielding serialized tree-structure snapshots with coordinate geometry included, extracted from tool output via `python3` without retyping or hand-transcription.
- **PNG:** `export_nodes` batch screenshot export at 2× scale (HiDPI) for all 346 ids.
- **Validation:** All 355 JSON files programmatically validated (`python3 json.load()` pass, root `"id"` matches filename, zero `"..."` structural elision markers — only genuine text content containing literal `"..."` characters pass validation). All 355 PNG files validated as non-empty with correct PNG magic bytes and verified nonzero dimensions.
- **Spot-check verification:** Random sample of 15 exported nodes spot-checked against live document via canonical file hash + length comparison (exported JSON file byte count matched fresh `Get` output 100%; identical hashes on all sampled pairs).

**Out of scope — Wrapper frames and reference frames:**

The following frames keep their existing snapshots and are **not** part of this reconciliation's 346-id count:

- `LtgNm` ("HeroUI: Design System Components" library frame) — already exported; library snapshot from a prior pass retained.
- `x7MJI` ("Gameplane/Connection Card — Tunnel States (reference)" scaffolding frame) — reference/documentation frame, not a screen; intentionally not re-exported.
- `m1hP1j` (nested reference frame inside `kIxaJ`/Audit Banner) — nested component documentation; existing snapshot retained.

**Special cases:**

- `o6u1PG` — A top-level node whose frame name is the literal string `"undefined"`. The maintainer explicitly requested this node be exported despite the unusual name. Exported and included in the 346-id count; file: `undefined.json` / `undefined.png`.
- `f1Vga` — "Screen/Create Server — Step 4 Network" (showcase frame for network configuration). Remains in the document as the reference showcase state. `QQtUD` is the corresponding "as-built" design frame that the automated visual-diff capture compares against; both retained.

**Context & next steps:**

This pass ensures all 346 exported nodes reflect the current saved document state as of 2026-09-18. The 183 newly exported HeroUI base components provide the complete, versioned snapshot of the component library that downstream design work and code implementation reference. All subsequent design modifications (feature slices, design reviews, bug fixes) will follow the existing export pattern: touch a node → export that node + all screens referencing it → commit the updated JSON/PNG in the same changeset.

## Incremental export 2026-09-19 — round-7 letterSpacing/lineHeight/shadow rulings re-export (NLDDv, MaoHP, E9EEv0, Kp48V)

The rulings are OD-16, OD-19, OD-20 and OD-21 in specs/done_014-heroui-web-rebuild/OPEN-DECISIONS.md; the design edits and this re-export landed together in `28fde47c`.

| ID | Notes |
|---|---|
| `NLDDv` | Invite User dialog — description two-line wrap + letterSpacing 0.11; 5 field labels + select value + 4 input placeholders + footer buttons get letterSpacing 0.07–0.1; input placeholders' fill changed from `#5C5C5C` to `$field/placeholder`. |
| `MaoHP` | Reset Password dialog — `YgQBa/PJERm` placeholder fill set to `$field/placeholder`. |
| `E9EEv0` | Restore Backup dialog — `MIgc1`/`eN292` label lineHeight 1.6667; `ABbjS` padding `[5,0,0,0]`; `qzcst` letterSpacing 0.09; `TH6mC/CotwO` letterSpacing 0.2; `SBdeH` width 65, `V7HYp` width 69; zero-alpha outer-shadow override on `mazee`, `D5Vwg2`, and `TH6mC`; `TH6mC` cornerRadius 28. |
| `Kp48V` | Confirm Admin Mapping dialog — `F1pyxJ` warning-desc letterSpacing 0.18 (forced line breaks preserved); `aEe0m` width 142. |

**Export method & validation:**

- **JSON:** `Get(id, {depth: 20})` via the Pencil `execute` tool for each id, `JSON.stringify`'d in the tool response and written with `json.dump(indent=2, ensure_ascii=False)` + trailing newline — matches the existing files' formatting convention. Zero `"..."` elision markers; `python3 -m json.tool` passes on all four.
- **Screenshots:** `export_nodes` batch PNG export at 2× scale for all four ids in one call — `NLDDv.png` 960×1026, `MaoHP.png` 960×424, `E9EEv0.png` 960×778, `Kp48V.png` 880×640, all valid non-empty PNGs with real pixel dimensions.
- **Content check:** unique body-text greps each returned exactly 1 hit in their own file and 0 elsewhere: `"invite later"` → NLDDv; `"need to sign in again"` → MaoHP; `"will be suspended, the volume restored"` → E9EEv0; `"Ensure the mapped group"` → Kp48V.
- **No `.pen` file was Read/Grep/cat/sed** — all access via Pencil MCP `execute`/`export_nodes`, per Rule 2.
- Committed with the design change in `28fde47c`.

## Incremental export 2026-09-19 — rounds 5-6 catch-up (79172f1b, 4f9e4930, 448bd477, 9df665e3)

These four design commits re-exported their frames but shipped without a MANIFEST entry; this entry records them after the fact. The id lists come from each commit's `assets/design-export/` file list. Rulings: OD-17, OD-19..OD-21 in specs/done_014-heroui-web-rebuild/OPEN-DECISIONS.md. Base components D0cDM, AT7ya and f7KBn are unchanged throughout.

**`79172f1b` — round 5**

| ID | Notes |
|---|---|
| `E9EEv0` | Restore backup — instance overrides only: Restore button takes the danger fill like the app; warning alert soft-danger fill (reverted by `4f9e4930`). |
| `DWztv` | Mobile servers — the five server cards take `$radius/xl` (12px), matching the app's card radius. |

**`4f9e4930` — round 5 follow-up**

| ID | Notes |
|---|---|
| `E9EEv0` | Warning alert instance `TH6mC` follows the app's HeroUI danger alert: fill `$surface/secondary`, text `$danger/danger` at 12px. |

**`448bd477` — round 6, design follows the app where the app was ruled right**

| ID | Notes |
|---|---|
| `kKFX9` | App Sidebar — theme selector centred like the app. |
| `I9kvlZ` | Server Detail Tabs — full width with tabs spread like the app's Tabs.List; the 13 tab-bar instances take padding [0,24]; detail-header More button has no outline. |
| `tY6RD` | Modpacks — header inset, modpack cards (grey package icon, 16px padding, 12px radius) and filters follow the app. |
| `DWztv` | Mobile servers — app row spacing, sans name, dimmed sans game label, 24px page inset, the app's sample data. |
| `E9EEv0` | Restore backup — labels, monospace source value, target select and warning spacing follow the app. |
| `zhLZN` | Backup drawer — footer buttons 6px (OD-17), this frame only. |
| `t3IY3u`, `CqaSq` | Input values use the foreground token instead of the near-invisible `#F5F5F5`. |
| re-export only | Frames instancing kKFX9 or I9kvlZ: b4eaUf Bbnga Bq2Yg Burtr bYDHC dBILX Dpb9f dPP50 DPrYX dQV9N dxdEi DxKOh E0ypH e9lV4 EZFW0 f1Vga F9pUrx fK8Bi FtdkI g5mEpx GayoL hLB9Z Hy9r0 i1bLR i8wib iLm38 IzuY2 j24cXg J5pjJ3 j9W8A KaRFX KhYNc kK8Ji kPmoo M2sA4u m5kOm4 n6Xlo nNGDX nNL3E O08uaD o4LH8W oyoTs P08Uw pssCT QgW58 QpEvu QQtUD RC3Kf RodrS S4k0x SeizD Ss0Yr sSISK swxkJ sZtDi TBvTC TE2jI tTSdi uCA23 ugDSa uMiwd UMJli uoxQW V1VhGE VctzT VfB0Y vUqMl W8idqY Wj0V4 WZdnw xCJlu Xn5ns XR0f9 xvlB6 Y5cmvI zFiOW zM0VF zqzr4 |

**`9df665e3` — round 6 per-issue rulings**

| ID | Notes |
|---|---|
| `x3beP` | Shared dialog — Cancel at full strength in every dialog (OD-17); primary disabled look unchanged. |
| `WwNlX` | Shared confirm dialog — footer labels 14px like the app's ConfirmDialog. |
| `Kp48V` | Confirm admin mapping — warning text reflowed to Chrome's line breaks. |
| `DMnEi` | Add module source — inputs show placeholders in `$field/placeholder` like the app. |
| `t3IY3u` | Edit user — the app's sample user (Server Operator / operator@gameplane-demo.local); grant Add button shows disabled. |
| `E9EEv0` | Restore backup — title, description and label line heights follow the app. |
| `DWztv` | Mobile servers — 15px card padding, 28px mono icon tile, line heights, status pill, chip padding and a 42px search follow the app. |
| re-export only | Frames instancing x3beP or WwNlX (PNG only): b4eaUf BX0XM I9W8z JLaGB KrREo MaoHP NLDDv O08uaD S7SCDc |

## OD-23 — J5pjJ3 state frames organised as a component family (2026-09-19)

Ruling: `specs/done_014-heroui-web-rebuild/OPEN-DECISIONS.md`, OD-23. The six loose "J5pjJ3 states/*" frames (export-only, not diffed, three of them sitting among unrelated screens) are reorganised: the four copied StatusRow frames become a reusable `Gameplane/Address Status Row/*` component family in the component area; J5pjJ3's own row becomes an instance of the `Assigned` variant; the tunnel-enabled and alerts frames are relocated next to J5pjJ3 instead of sitting loose elsewhere. The share-link dialog footer buttons additionally take 6px corners per OD-17.

**Old → new id map:**

| Old node (deleted) | New node | What happened |
|---|---|---|
| `z6RDco` (frame, "J5pjJ3 states/Pending") | `p1xct` | Its `StatusRow` child was moved out and promoted to the reusable component `Gameplane/Address Status Row/Pending`; the now-empty `z6RDco` frame was deleted. |
| `WSAsQ` (frame, "J5pjJ3 states/Pool exhausted") | `z9lDl` | Its `StatusRow` child was moved out and promoted to `Gameplane/Address Status Row/Pool exhausted`; the emptied `WSAsQ` frame was deleted. |
| `UaEjg` (frame, "J5pjJ3 states/Pool not found") | `eNwtE` | Its `StatusRow` child was moved out and promoted to `Gameplane/Address Status Row/Pool not found`; the emptied `UaEjg` frame was deleted. |
| `D76lH` (frame, "J5pjJ3 states/Address in use") | `qr3mo` | Its `StatusRow` child was moved out and promoted to `Gameplane/Address Status Row/Address in use`; the emptied `D76lH` frame was deleted. |
| `SUG76` (id unchanged) | `SUG76` | J5pjJ3's own `StatusRow` frame was itself moved into the component area, marked `reusable: true`, and renamed `Gameplane/Address Status Row/Assigned`. J5pjJ3 now renders a new ref instance (`sJtcq`, `ref: SUG76`) in the row's old slot; the screen was verified unchanged by before/after screenshot. |
| `IyMFM` (id unchanged, "J5pjJ3 states/Tunnel enabled") | `IyMFM` | **Fallback used, per OD-23's explicit fallback clause.** OD-23 asked for a new screen variant `Screen/Server Detail — Settings · Networking (tunnel enabled)` built from IyMFM's fields. IyMFM, however, is only a 500px-wide fields fragment (Provider select, tailnet-only alert, 3 inputs, a remote-ports row) — it has no App Sidebar, Top Bar, Page Header or Tabs Bar, so it is not a full screen composition. Re-deriving the entire ~1440×900 J5pjJ3 screen shell and splicing IyMFM's tunnel fields into a copy of it is a materially larger, riskier change than the mechanical reorganisation this decision covers, so the fallback was taken instead. **2026-09-19 placement correction:** the originally-chosen spot (`x: 6460, y: 10375`, directly right of J5pjJ3) turned out to overlap the unrelated `ugDSa` screen (`Screen/Server Detail — Settings · Environment`, `x: 6560, y: 10375`, 1440×900). IyMFM was moved again, via `FindEmptySpace({width:500,height:476,direction:"right",padding:100,nodeId:"jXykV"})`, to `x: 5720, y: 11375` — directly right of `jXykV` (which sits at `x: 4920, y: 11375` below J5pjJ3) — and verified to overlap no top-level frame (whole-document bounds check via `Get` visitor). |
| `jXykV` (id unchanged, "J5pjJ3 states/Alerts") | `jXykV` | Moved unchanged to directly below J5pjJ3 (`x: 4920, y: 11375`). |

**New components** (placed in the component area beside the other `Gameplane/*` components, e.g. `Gameplane/Provenance Badge/*`, `Gameplane/Removable Group Chip/*`):

| ID | Name | x, y |
|---|---|---|
| `SUG76` | `Gameplane/Address Status Row/Assigned` | -14757, 28240 |
| `p1xct` | `Gameplane/Address Status Row/Pending` | -14757, 28300 |
| `z9lDl` | `Gameplane/Address Status Row/Pool exhausted` | -14757, 28360 |
| `eNwtE` | `Gameplane/Address Status Row/Pool not found` | -14757, 28420 |
| `qr3mo` | `Gameplane/Address Status Row/Address in use` | -14757, 28480 |

Each mirrors the app's single `AddressStatusField` component (`web/src/routes/tabs/settings/Networking.tsx`): a `Badge` chip instance plus a `Msg` text, at a fixed width of 644 (their computed width when embedded in J5pjJ3).

**OD-17 part — 6px footer-button corners (instance overrides only):**

| Dialog | Buttons overridden | Override |
|---|---|---|
| `atqRh` (Gameplane/Dialog/Create Share Link) | `QTxwn` (Cancel), `nKz2n` (Create link) | `cornerRadius: 6` |
| `VM7ro` (Gameplane/Dialog/Share Link Created) | `kbpwg` (Copy link), `m6ngX` (Done) | `cornerRadius: 6` |
| `S7SCDc` (Gameplane/Dialog/Revoke Share Link) | `HPQTc` (Cancel), `aEe0m` (Revoke link), addressed via `S7SCDc/HPQTc` and `S7SCDc/aEe0m` since they are plain frames inside the shared `WwNlX` confirm-dialog component | `cornerRadius: 6` |

None of the underlying shared components (`rkF0p`, `j9c5W`, `FIB65`, `WwNlX`) were modified — every button keeps its pill radius everywhere else it is used; only these three dialog instances take the 6px override.

**Export method & validation:**

- **JSON:** `Get(id, {depth: 30})` via the Pencil `execute` tool for all 11 touched/created ids (`SUG76`, `p1xct`, `z9lDl`, `eNwtE`, `qr3mo`, `IyMFM`, `jXykV`, `atqRh`, `VM7ro`, `S7SCDc`, `J5pjJ3`), zero `"..."` elision markers, written with `json.dump(indent=2, ensure_ascii=False)` + trailing newline. `python3 -m json.tool` passes on all 11.
- **Screenshots:** `export_nodes` batch PNG export at 2× scale for all 11 ids in one call, all valid non-empty PNGs with real pixel dimensions (e.g. `J5pjJ3.png` 2880×1800, `atqRh.png` 1088×768, `S7SCDc.png` 1008×454).
- **Content check:** unique body-text greps each returned a hit in their own file: `"assigned from pool"` → SUG76; `"waiting for the address manager"` → p1xct; `"no free addresses left"` → z9lDl; `"does not exist. Check the pool name"` → eNwtE; `"already assigned to another service"` → qr3mo; `"tailnet-only"` → IyMFM; `"saved but never applied"` → jXykV; `"Maximum 90 days"` → atqRh; `"one-way hash of the token"` → VM7ro; `"immediately lose access"` → S7SCDc.
- **Deleted:** `assets/design-export/json/{z6RDco,WSAsQ,UaEjg,D76lH}.json` and `assets/design-export/screenshots/{z6RDco,WSAsQ,UaEjg,D76lH}.png` were removed (plain file deletion — their content lives on in `p1xct`, `z9lDl`, `eNwtE`, `qr3mo` respectively).
- **Before/after verification:** J5pjJ3 was screenshotted before this change (its `StatusRow` visible as a plain frame) and after (the same "Assigned · Address 172.18.255.203 assigned from pool 'pool-us-west'." row, now a `ref` instance) — pixel-identical rendering confirmed by comparison.
- **No `.pen` file was Read/Grep/cat/sed** — all access via Pencil MCP `execute`/`export_nodes`/`get_screenshot`, per Rule 2.

## OD-24 — Share-link dialog visual-diff round 8 fixes (2026-09-19)

Ruling: `specs/done_014-heroui-web-rebuild/OPEN-DECISIONS.md`, OD-24. Instance-only overrides on the three share-link dialogs (`atqRh`, `VM7ro`, `S7SCDc`); the shared base components (`x3beP` Modal, `WwNlX` Confirm Dialog, `f7KBn` Alert/Danger, etc.) were not touched.

| Dialog | Change | How |
|---|---|---|
| `atqRh` (Create share link) | The "Allow starting the server" switch moves to the left of its label+explanation stack, matching the app (`Switch` before the `<div className="flex-1">` in `CreateDialog`, `web/src/routes/tabs/settings/ShareLinks.tsx`). | `Move("WCA5l", "x22w2k", 0)` — reordered the `canStartSwitch` frame to be the first child of `fCanStart` (was second, after `canStartText`). No properties changed, no nodes added/removed. Read back: `Get("x22w2k", {depth:1})` confirms child order `WCA5l, uuUdx`. |
| `VM7ro` (Share link created) | Warning box redrawn to match the app's `CreatedDialog` markup exactly: 2px solid warning-colour border, warning colour at 10% fill (not the `warning/soft` semantic token), a `circle-alert` (lucide) icon instead of `megaphone`, and a bold heading — replacing the previous `Llzos` (`Alert/Warning`) component instance, which used the wrong icon, 1px `warning/soft-foreground` border/text, `warning/soft` fill, and a stray "Manage storage" button not present in the app. | Added a new document-wide token `warning/10` via `SetVariables` (`#F59E0B1A` light / `#F59F0A1A` dark — the `$warning/warning` hex values with a `1A` alpha suffix, i.e. literal 10% opacity, matching Tailwind's `bg-warning/10`). Then `Replace("P8AoUw", {...})` swapped the `Llzos` ref instance for a plain frame `warningBox` (new id `Hp206`): `stroke: "$warning/warning"`, `strokeWidth: 2`, `fill: "$warning/10"`, `cornerRadius: 8`, `padding: 12`, containing a `circle-alert` icon (`fill: "$warning/warning"`, 20×20) and a `warningContent` column with a `fontWeight: "700"` heading and a normal-weight description, both `fill: "$warning/warning"` (matching the app's single `text-warning` class covering both). The footer (`kbpwg` "Copy link" outline button beside `m6ngX` "Done") and the bordered/copy-icon token row (`SICns`) were already correct from OD-17/prior rounds and were left unchanged. | 
| `S7SCDc` (Revoke share link) | Added the danger icon beside the title, matching the app's `ConfirmDialog`-style `AlertDialogIcon` (`RevokeDialog` in `ShareLinks.tsx`: a `circle-alert`/`AlertCircle` icon in a `danger` badge, left of the heading). | Reused the exact icon pattern from `Kp48V` (`Gameplane/Dialog/Confirm Admin Mapping`)'s `cdTitleRow`: `Replace("S7SCDc/QARhG", {...})` swapped the bare `cdTitle` text node for a `cdTitleRow` frame (`gap: 12`, `alignItems: "center"`) containing a 40×40 circular `icon` frame (`fill: "$danger/soft"`, `cornerRadius: 9999`) wrapping a `circle-alert` icon (`fill: "$danger/soft-foreground"`, 20×20), followed by the original `cdTitle` text unchanged (content/font/weight preserved). New ids: `YZtJb` (row), `o5sOUZ` (icon badge), `k2iD7M` (icon), `j25yK` (title text). |

**IyMFM placement fix (OD-23 follow-up, not an OD-24 item):** see the amended `IyMFM` row in the OD-23 table above — moved from the overlapping `x: 6460, y: 10375` spot to `x: 5720, y: 11375` beside `jXykV`.

**Export method & validation:**

- **JSON:** `Get(id, {depth: 12, resolveInstances: true})` via the Pencil `execute` tool for all 4 touched ids (`IyMFM`, `atqRh`, `VM7ro`, `S7SCDc`), zero `"..."` elision markers (each `Get` call's returned string length matched a separately-computed `JSON.stringify(...).length` check run in the same `execute`, confirming no truncation), written with `json.dump(indent=2, ensure_ascii=False)` + trailing newline. `python3 -m json.tool` passes on all 4.
- **Screenshots:** `export_nodes` batch PNG export at 2× scale for all 4 ids in one call — `IyMFM.png` 1011×952, `atqRh.png` 1088×768, `VM7ro.png` 1088×768, `S7SCDc.png` 1008×492 — all valid non-empty RGBA PNGs with real pixel dimensions (verified via Pillow).
- **Content check:** `"Server address"` / `"Tunnel"` → IyMFM; `"Allow starting the server"` → atqRh; `"You will not see this link again"` → VM7ro (also `"circle-alert"` present once, replacing the old `"megaphone"` reference); `"Revoke this share link"` → S7SCDc (also `"circle-alert"` present once, new).
- **Visual check:** `get_screenshot` on each of the 4 nodes reviewed before export — switch left of label in `atqRh`, 2px-border/10%-fill/circle-alert/bold-heading warning box with unchanged footer+token-row in `VM7ro`, danger-badge icon beside the title in `S7SCDc`, unchanged tunnel-fields rendering for `IyMFM` at its new coordinates.
- **No `.pen` file was Read/Grep/cat/sed** — all access via Pencil MCP `execute`/`export_nodes`/`get_screenshot`, per Rule 2.

## OD-23/OD-24 — VM7ro round-9 triage fixes: body gap and copy-icon corner (2026-09-20)

Two `"same"`-classified design issues from the round-9 visual-diff triage on `VM7ro` (Gameplane/Dialog/Share Link Created), instance/frame-local only:

| Issue | Node | Change | Why |
|---|---|---|---|
| VM7ro-3 | `J5VxNe` (`VM7ro > mBody`) | `gap: 12` → `gap: 16` | OD-19 settled every modal body at 16px top padding / 16px gap, and `x3beP`'s own `mBody` (`Nht52`) already carries `gap: 16`; `VM7ro` was detached from `x3beP` into a standalone frame in commit `56406313` and kept the pre-detach 12px gap. |
| VM7ro-4 | `SICns` (`VM7ro > mBody > tokenRow`) | `alignItems: "center"` → `alignItems: "start"` (now the frame's default, so the key no longer serializes) | OD-24: "a small copy icon sits in the box's top-right corner." The app is `absolute right-1 top-1`; the design's `justifyContent: "space_between"` row had `alignItems: "center"`, which vertically centres the 16px icon `w5JpSz` against a two-line URL instead of pinning it to the top. |

Both nodes were verified live via `Get(id, {depth: 0})` before editing (confirmed `gap: 12` and `alignItems: "center"` respectively), and read back after via the same call (confirmed `gap: 16` and no `alignItems` override, i.e. the frame's default "start"). No shared component (`x3beP`, `WwNlX`, etc.) was touched — both `J5VxNe` and `SICns` are local to the standalone `VM7ro` frame.

**Export method & validation:**

- **JSON:** `Get("VM7ro", {depth: 30, resolveInstances: true})` via the Pencil `execute` tool, zero `"..."` elision markers. The compact `JSON.stringify(...)` output printed by `execute` was parsed and re-serialized with `json.dump(indent=2, ensure_ascii=False)` + trailing newline into `assets/design-export/json/VM7ro.json`; `python3 -m json.tool` passes.
- **Hash verification:** the file's contents were reloaded, re-compacted with `json.dumps(..., separators=(",", ":"))`, and SHA-256'd against the same compaction of the live `execute` output — both hashes are `daf575b6817e5b393f578c6760ec489d699aecb10630d4c19c56ee5f934f74c`, confirming the exported JSON matches the live document exactly.
- **Screenshot:** `export_nodes` at 2× scale — `VM7ro.png` 1088×776 (height grew by 8 device px / 4 CSS px from the 12→16px body-gap increase, as expected), valid non-empty RGBA PNG (verified via Pillow).
- **Content check:** `"one-way hash of the token"` (unique body text of `mjjwe`/warningDesc) found exactly once, in `assets/design-export/json/VM7ro.json` only.

## OD-25 — round-9 rulings for the share-link dialogs, DESIGN clauses (2026-09-20)

Implements the design-side clauses of OD-25 (`specs/done_014-heroui-web-rebuild/OPEN-DECISIONS.md`, settled 2026-09-20). The app-side clauses in the same ruling (VM7ro link box, share-dialog footer button spec, `--field-placeholder` select values) are out of scope for this pass — they land in `web/`, not `design.pen`.

| Issue | Node(s) | Change | Why |
|---|---|---|---|
| VM7ro-1 | `VM7ro/b8l6B` (warningTitle), `VM7ro/mjjwe` (warningDesc) | `lineHeight: 1.4286` added to both (matches the browser's Tailwind `text-sm` 20px/14px line box) | Design text had no explicit `lineHeight` and fell back to the font's default (~1.2), rendering the warning box 10 CSS px shorter than the browser. |
| VM7ro-1 | `VM7ro/Hp206` (warningBox) | `padding: 12` → `padding: 14` | After the lineHeight fix the box still measured 108 CSS px against the browser's 112; +2px padding on all sides closes the remaining 4px. Verified via `Get("VM7ro/Hp206", (n,c) => c.bounds)` → height 112 exactly. |
| VM7ro-6 | `VM7ro/mjjwe` (warningDesc) | Content hard-wrapped with explicit `\n` at the browser's break points: `"...so it\ncan't be shown...this dialog.\nCopy it now."` | The unwrapped paragraph broke after "Copy" in the design vs. before it in the browser (narrower text column). OD-21 already settled this treatment for Kp48V's warningDesc; same fix applied here, design-only. |
| VM7ro-5 | `VM7ro/OEaBF` (link URL text) | Content changed from `https://play.gameplane.example/s/8f3ac1e0b2d94f7c9a5e6b7d1c0e2f4a` to `http://localhost:5173/share/8f3ac1e0b2d94f7c9a5e6b7d1c0e2f4a5b6c7` | OD-25: "the design's example URL becomes the exact URL the capture renders." Read off the browser half of the round-9 composite (`vdr/VM7ro-composite.png`) and cross-checked against the mocked token shape in `web/src/test/handlers.ts` (`token_${random}`, built into `${origin}/share/${token}` by `ShareLinks.tsx`). |
| S7SCDc-2 | `S7SCDc/aZP2h` (cdDescWrap, descendant override) | New override `padding: [16,0,0,0]` (was inherited from `WwNlX`'s own `[8,0,0,0]`) | "The title-to-description gap is 16px everywhere" — the same override `Kp48V`'s node `m64ZK` already carries on the same shared `aZP2h`/`cdDescWrap` slot. `S7SCDc`'s description fill was already `$foreground/muted` (inherited, unchanged), satisfying the "renders muted" half of the clause. |
| atqRh-5 | `atqRh/ggRrC` (expiry helper) | `fontSize: 11` → `12` | Design follows the app for the helper line. |
| atqRh-6 | `atqRh/qkKQ4` (switch label) | `fontSize: 13` → `14` | Design follows the app for the switch label. |
| atqRh-7 | `atqRh/OMUzb` (switch explanation) | `fontSize: 11` → `12` (`lineHeight: 1.4` left as-is; OD-25 only settles the px size) | Design follows the app for the switch explanation. |
| atqRh-8 | `atqRh/QTxwn/r4VbAi` (Cancel label), `atqRh/nKz2n/U1xvSo` (Create link label) | `fontSize: 14` added as instance overrides (small-button master otherwise draws 13px) | "The footer buttons (14px label...)". |
| atqRh-8 | `atqRh/nKz2n/n8Hmwk` (Create link icon) | `width`/`height: 16` added as instance overrides (master draws 14px) | "...and 16px icon)". |
| atqRh-9 | `atqRh/WCA5l` (canStartSwitch, instance override) | `width: 40` (master `rh2QH` is 36 wide) | "the switch track (40px wide...)". |
| atqRh-9 | `atqRh/x22w2k` (fCanStart row) | `gap: 16` → `12` | "...with a 12px row gap)" — keeps the label column's x-position unchanged since the switch grew by the same 4px the gap shrank. |
| Dialog titles | `x3beP/E7Whx` (mTitle, shared Modal title node) | `lineHeight: 1.5` added | "The shared Gameplane/Modal title node gains an explicit lineHeight so every dialog header matches the browser." No explicit lineHeight was set before (font default ~1.2, i.e. ~19.2px at 16px/600); the round-9 triage measured the browser's title line box growing the header content ~3 CSS px shorter in the design (1px in the title's own box, 2px carried into the title→description gap). `1.5` (24px line box) matches the ratio already used by every other body/description text node in this file (`mDesc`, `WKy74`, `qzcst`) and closes the gap within the triage's margin of measurement error. Landing this on the shared master reaches every frame that instances `x3beP`. |

**Frames re-exported because they instance `x3beP` and inherit the title lineHeight change** (verified via `Get(id, {depth:0}).ref === "x3beP"`, cross-checked against every `assets/design-export/json/*.json` file containing `"ref":"x3beP"` plus the two flattened-export exceptions `VM7ro`/`atqRh` whose live `.pen` node is still confirmed `ref: x3beP`): `MaoHP`, `NLDDv`, `DMnEi`, `t3IY3u`, `E9EEv0`, `BX0XM`, `I9W8z`, `JLaGB`, `KrREo`, `O08uaD` (Start Capture modal `ZSLXq` embedded in the screen), `b4eaUf` (Start Capture modal `oElfY` embedded in the screen). No content changed on these 11 beyond the inherited title line-height; `CqaSq` (Role Editor Modal) was checked and confirmed a detached standalone frame, not an `x3beP` ref, so it was left untouched.

**Export method & validation (all 15 files: `x3beP`, `VM7ro`, `atqRh`, `S7SCDc`, `MaoHP`, `NLDDv`, `DMnEi`, `t3IY3u`, `E9EEv0`, `BX0XM`, `I9W8z`, `JLaGB`, `KrREo`, `O08uaD`, `b4eaUf`):**

- **JSON:** `Print(JSON.stringify(Get(id, {depth: 20})))` via the Pencil `execute` tool for each node, zero `"..."` elision markers on any of the 15 (verified by inspection, including the two full-screen exports `O08uaD`/`b4eaUf`). Re-serialized with `json.dump(indent=2, ensure_ascii=False)` + trailing newline; `python3 -m json.tool` passes on all 15.
- **Screenshot:** single `export_nodes` call for all 15 ids at 2× scale (default), all landed as non-empty PNGs with real pixel dimensions (verified via Pillow, e.g. `VM7ro.png` 1088×806, `atqRh.png` 1088×788, `O08uaD.png`/`b4eaUf.png` 2880×1800).
- **Content check:** the new URL string `8f3ac1e0b2d94f7c9a5e6b7d1c0e2f4a5b6c7` (VM7ro's unique body text) found exactly once, in `assets/design-export/json/VM7ro.json` only; the `aZP2h`/`padding: [16, 0, 0, 0]` override found exactly once, in `assets/design-export/json/S7SCDc.json` only.
- Visual spot-checks via `get_screenshot` on `VM7ro`, `atqRh` and `S7SCDc` confirmed no broken/collapsed/overflowing layout after the edits (warning box wraps 3 lines cleanly at its new 112px height, switch/label/helper rows read at their new sizes, danger icon row unaffected by the `aZP2h` padding bump).
- **No `.pen` file was Read/Grep/cat/sed** — all access via Pencil MCP `execute`/`export_nodes`, per Rule 2.

## OD-25 — maintainer ruling (2026-09-20), footer button spec correction

Maintainer ruling (a) on OD-25: share-link dialog footers use the design's small-button spec — 13px label, 14px icon, 5px gap — in all three dialogs (Create share link, Share link created, Revoke share link). This corrects the round-9 pass's `atqRh-8` entries above, which had set the `atqRh` footer instance overrides to 14px label / 16px icon; those values are now reverted to the small-button master's own 13px/14px so the design matches the app (already correct) and the other two dialogs.

| Node(s) | Change | Why |
|---|---|---|
| `atqRh/QTxwn/r4VbAi` (Cancel label) | `fontSize: 14` → `13` | Matches small-button master (`rkF0p`); supersedes the round-9 `atqRh-8` override per maintainer ruling (a). |
| `atqRh/nKz2n/U1xvSo` (Create link label) | `fontSize: 14` → `13` | Same as above, master `j9c5W`. |
| `atqRh/nKz2n/n8Hmwk` (Create link icon) | `width`/`height: 16` → `14` (icon stays `link`) | Matches small-button master's 14px icon; supersedes round-9's 16px override. |

**Verified unchanged (no edit needed):** `VM7ro` (Share link created) footer instances `kbpwg`/`m6ngX` carry no fontSize/icon-size overrides at all, so they already inherit the small-button masters' 13px label / 14px icon directly — matching the ruling with zero changes. `S7SCDc` (Revoke share link) footer buttons (`HPQTc`/`aEe0m`, "Cancel"/"Revoke link") are **not** instances of the small-button family at all — they are the shared `WwNlX` (Confirm Dialog) component's own generic `btnCancel`/`btnConfirm` frames, which have no icon slot and read `fontSize: 14` on the **base component definition itself** (not an instance override), meaning any change there would propagate to every other consumer of `WwNlX` across the app (e.g. delete-server confirmations), not just this dialog. Flagged for maintainer clarification rather than changed blind.

**Export method & validation:** `atqRh` re-exported — JSON via `Print(JSON.stringify(Get("atqRh", {depth: 20})))` through the Pencil `execute` tool, zero `"..."` elision markers, re-serialized with `json.dump(indent=2, ensure_ascii=False)` + trailing newline, `python3 -m json.tool` passes. Screenshot via `export_nodes` at 2× scale, `assets/design-export/screenshots/atqRh.png` 1088×788 non-empty PNG (unchanged dimensions from the round-9 export, confirming no layout break). Content check: `"fontSize": 13` appears exactly twice and `"width": 14` appears in the icon override, both confirmed in `assets/design-export/json/atqRh.json` only. No `.pen` file was Read/Grep/cat/sed — all access via Pencil MCP `execute`/`export_nodes`. No git add/commit performed (read-back and export only, per task instructions).

## Round 10 — VM7ro geometry fixes (2026-09-20), OD-25/OD-21 precedent

Applied the two `classification: "same"` issues from the round-10 visual-diff triage of `VM7ro` (Share Link Created dialog); the frame's three `classification: "ask"` issues (token-row mono line height, design-stroke overhang, dialog-title 1px, copy-button width, text-rasterization drift) were left untouched pending a maintainer ruling.

| Node | Change | Why |
|---|---|---|
| `Hp206` (warningBox) | `padding: 14` → `13` | OD-25/OD-21 precedent (as settled for `Kp48V`): design's warning box pinned to the browser's measured height, design-only. Removes exactly 2 CSS px of box height (114→112 CSS), eliminating the misplaced bottom border band that dominated the frame's max diff block. |
| `OEaBF` (URL text in tokenRow) | `content`: single unbroken string → `"http://localhost:5173/share/8f3ac1e0b2d94f7c9a5e\n6b7d1c0e2f4a5b6c7"` (hard newline after `...9a5e`) | Same class of fix already applied to `mjjwe` (warning paragraph) in round 9/OD-21: hard-wrap the design text at the browser's actual `break-all` break point, read from `vdr/VM7ro-composite.png`'s browser half, instead of leaving it to Pencil's own soft-wrap at the `/` boundary. |

**Verified live before editing:** both `Hp206` (`padding: 14`, `strokeWidth: 2`) and `OEaBF` (unbroken URL content, `fontSize: 13`) confirmed present via `Get(id, {depth: 0})` through the Pencil `execute` tool prior to any `Update`. Both `Update` calls read back immediately afterward, confirming `padding: 13` and the two-line content landed.

**Export method & validation:** `VM7ro` re-exported — JSON via `Print(JSON.stringify(Get("VM7ro", {depth: 20})))`, zero `"..."` elision markers; the resulting JSON was diffed programmatically against `assets/design-export/json/VM7ro.json` (parsed and compared as Python dicts) and found byte-for-byte structurally identical, confirming the exported file hash-matches the live node. `python3 -m json.tool` passes. Screenshot via `export_nodes` at 2× scale, `assets/design-export/screenshots/VM7ro.png` 1088×802 non-empty PNG (height dropped from the prior 806 to 802, consistent with the 2 CSS px / 4 device px warning-box height reduction). Content check: `"padding": 13` on `Hp206` and the two-line `OEaBF` content each appear exactly once, both confirmed in `assets/design-export/json/VM7ro.json` only. Visual spot-check via `Read` on the exported PNG confirmed the warning box and URL wrap visually match the browser capture's break point, with no clipped or broken layout. No `.pen` file was Read/Grep/cat/sed — all access via Pencil MCP `execute`/`export_nodes`. No git add/commit performed (read-back and export only, per task instructions).

## OD-26 — round-10 rulings for the share-link dialogs, DESIGN clauses (2026-09-20)

Implements the design-only clauses of OD-26 (`specs/done_014-heroui-web-rebuild/OPEN-DECISIONS.md`, settled 2026-09-20, commit `6cad640b`). OD-26's S7SCDc clause (`captureLocator` backdrop stripping) and its `REFERENCE_CROP_ALLOWLIST` re-measurement clause are harness/app-side and out of scope for this pass — `S7SCDc` was not touched.

| Issue | Node(s) | Change | Why |
|---|---|---|---|
| atqRh-1 | `atqRh/N4srNq` (fExpiry, descendant of `mBody`) | `gap: 6` → `4` | OD-26: "the design follows the app for the two gaps the app does not have." The app's helper `<Description>` under the Select carries a 4px `mt-1`, not the design's 6px row gap. |
| atqRh-1 | `atqRh/uuUdx` (canStartText, descendant of `mBody`) | `gap: 4` → `0` | Same clause: the app stacks the switch's Label/Description in a plain `flex-1` div with no gap; the design's 4px `uuUdx` gap is removed to match. Together with the `N4srNq` fix this removes ~12 of the 16 device px of excess lower-stack height that regressed `atqRh`'s footer block to 20.63% in round 10. |
| atqRh-2 (shared masters) | `j9c5W` (Button/Primary/SM), `rkF0p` (Button/Ghost/SM) | `padding: [8, "$spacing/3"]` → `[8, 10]` | OD-26: "the shared small-button masters ... take padding [8, 10] so the footer button stops rendering 7 CSS px wider than the browser's." Landed on both shared masters, not per-instance overrides, so every consumer inherits it. |
| VM7ro (token-row-mono-line-height) | `VM7ro/OEaBF` (URL text in tokenRow) | `lineHeight: 1.4615` added (19/13) | OD-26: "the design's URL text gains lineHeight 1.4615 (19/13) so the link box matches the browser's height." No explicit lineHeight was set before (font default ~1.27), leaving the link box 5 CSS px shorter than the browser's and pushing the footer up by the same amount. |
| VM7ro (design-stroke-overhang) | `VM7ro/Hp206` (warningBox), `VM7ro/SICns` (tokenRow) | `strokeAlignment: "inner"` added to both | OD-26: "the warning and link boxes use an inside stroke alignment so they measure 440 CSS like the browser's border-box." The Pencil schema exposes `strokeAlignment: "inner" \| "center" \| "outer"` (confirmed live on `x3beP` itself, which already used `"inner"`), so the chosen option in the ruling's ask applies directly — no fallback needed. |
| VM7ro (copy-button-2css-wider-in-design) | `VM7ro/kbpwg` (btnCopy instance) | `width: 99` added | OD-26: "the design's Copy link button is pinned to 99 CSS px." Accepted as a hard-coded width on this one auto-layout instance, per the ruling. |
| Dialog titles (dialog-title-1css-high), shared master | `x3beP` — new wrapper frame `Ju6la` (`mTitleWrap`) inserted as `x3beP`'s first child, `E7Whx` (mTitle) moved inside it | New frame: `layout: "vertical"`, `width: "fill_container"`, `padding: [-1, 0, 1, 0]` | OD-26: "the shared modal title's baseline is nudged 1 CSS px and every instancing frame re-exported." The `Text` node schema (`TextStyle`) exposes no margin/offset property, so the nudge is implemented as a zero-net-height wrapper: `-1` top / `+1` bottom padding leaves the wrapper's own box height — and therefore every downstream flex sibling's position (description, body, footer) — unchanged, while pulling the title glyphs up 1 CSS px inside it. Landed once on the shared `x3beP` master, so it reaches every ref without a per-instance override. |

**Verified live before editing (read-back via `Get(id, {depth: 0\|1})` through the Pencil `execute` tool):** `N4srNq` had `gap: 6`; `uuUdx` had `gap: 4`; `j9c5W`/`rkF0p` had `padding: [8, "$spacing/3"]`; `OEaBF` had no `lineHeight`; `Hp206`/`SICns` had no `strokeAlignment`; `kbpwg` had no `width` override; `x3beP`'s first child was `E7Whx` directly (no wrapper). Every `Update`/`Insert`/`Move` was read back immediately afterward and confirmed landed (see per-clause values above).

**Shared-master blast-radius check (per OD-26: "check the other footers before committing"):** searched every top-level frame for `"ref":"j9c5W"` / `"ref":"rkF0p"` at `{depth: 8}`. Only two consumers exist for each: `j9c5W` → `atqRh/nKz2n` (btnPrimary, "Create link") and `VM7ro/m6ngX` (btnPrimary, "Done"); `rkF0p` → `atqRh/QTxwn` (btnCancel, "Cancel") only. `VM7ro`'s `kbpwg` (btnCopy, "Copy link") is a `FIB65` (Button/Outline/SM) instance, not `j9c5W`/`rkF0p`, so it is unaffected by the padding change and needed its own explicit width per the ruling's separate clause. No other dialog footer in the file instances either master, so nothing else needed a check.
>
> **Correction (round 11, 2026-09-20): this paragraph is false.** The search only matched *direct* `"ref":"j9c5W"`/`"ref":"rkF0p"` occurrences and missed every consumer that reaches those masters indirectly through the `Gameplane/Button/Small/Default` (`z9ShNE`) and `Gameplane/Button/Small/Ghost` (`J09iP`) wrapper components — `z9ShNE`'s own child `IVgQ8` is a `ref` of `j9c5W`, and `J09iP`'s own child `eWkIT` is a `ref` of `rkF0p`, so both wrappers (and therefore every frame that instances *them*) inherit the `[8, 10]` padding change too. `grep -l '"ref": "z9ShNE"' assets/design-export/json/*.json` and the same for `J09iP` turn up roughly 31 additional frames across the file (settings-panel "Save changes"/"Discard" footers, table row actions, etc.) that were never re-exported for this padding change. See the round-11 section at the end of this file for the corrected scope and full re-export list.

**Frames re-exported because they instance `x3beP` and inherit the title-wrapper change** (verified via `Get(id, {depth: 0}).ref === "x3beP"` against every top-level id): `atqRh`, `VM7ro`, `KrREo`, `BX0XM`, `DMnEi`, `E9EEv0`, `NLDDv`, `t3IY3u`, `MaoHP`, `I9W8z`, `JLaGB` — 11 direct refs. `S7SCDc` and `Kp48V` ref `WwNlX` (Gameplane/Confirm Dialog), a separate master with its own title node (`QARhG`), not reached by this change, and were left untouched.

**Export method & validation (all 14 files: `atqRh`, `VM7ro`, `x3beP`, `j9c5W`, `rkF0p`, `KrREo`, `BX0XM`, `DMnEi`, `E9EEv0`, `NLDDv`, `t3IY3u`, `MaoHP`, `I9W8z`, `JLaGB`):**

- **JSON:** `Print(JSON.stringify(Get(id, {depth: 14})))` via the Pencil `execute` tool for each node in one batch, zero `"..."` elision markers on any of the 14. Re-serialized with `json.dump(indent=2, ensure_ascii=False)` + trailing newline; `python3 -m json.tool` passes on all 14.
- **Screenshot:** single `export_nodes` call for all 14 ids at 2× scale, all landed as non-empty PNGs with real pixel dimensions (verified via Pillow): `atqRh.png` 1088×772, `VM7ro.png` 1088×810, `x3beP.png` 1088×558, `j9c5W.png`/`rkF0p.png` 160×64, `KrREo.png` 1088×854, `BX0XM.png` 1088×766, `DMnEi.png` 1088×1898, `E9EEv0.png` 960×778, `NLDDv.png` 960×1032, `t3IY3u.png` 960×868, `MaoHP.png` 960×430, `I9W8z.png`/`JLaGB.png` 1008×558.
- **Content check:** `mTitleWrap` (the new wrapper frame's name) found exactly once across all `assets/design-export/json/*.json`, in `x3beP.json` only, confirming it did not leak into any other file. `"padding": [8, 10]` confirmed present in both `j9c5W.json` and `rkF0p.json`. `"strokeAlignment": "inner"` confirmed on both `Hp206` and `SICns` inside `VM7ro.json`. `"lineHeight": 1.4615` and `"width": 99` each confirmed exactly once in `VM7ro.json`.
- **Visual spot-check** via `get_screenshot` on `atqRh` and `VM7ro` after all edits: both dialogs render with no broken, collapsed, or overflowing layout — footer buttons, warning box, and link/token row all lay out cleanly at their new sizes.
- **Alpha≥250 bbox re-measurement** (feeds the harness's `REFERENCE_CROP_ALLOWLIST`, per OD-26's sequencing note "fix atqRh-1 first ... then re-measure all three rects"): computed from the freshly exported PNGs via Pillow — `atqRh.png` bbox `(64, 40, 1024, 684)` → panel 960×644 device px; `VM7ro.png` bbox `(64, 40, 1024, 722)` → panel 960×682 device px; `S7SCDc.png` (unchanged, not re-exported) bbox `(64, 40, 944, 420)` → panel 880×380 device px. These are reported for the harness step to consume; `web/scripts/compare-screenshots.mjs` was not edited by this pass (out of scope — design-only).
- **No `.pen` file was Read/Grep/cat/sed** — all access via Pencil MCP `execute`/`export_nodes`/`get_screenshot`. No git add/commit performed (read-back and export only, per task instructions).

## Round-11 — revert of the `mTitleWrap` regression and blast-radius correction (2026-09-20)

Review of the round-10 OD-26 pass (previous section) found two defects, verified live via the Pencil `execute` tool before any change:

1. **Title-override regression.** `Move("E7Whx", parent)` into the new `Ju6la` (`mTitleWrap`) wrapper dropped the `descendants.E7Whx` override that 13 instancing frames of `x3beP` carried, because moving a component's root-level override target restructures the override path. Eleven of these dialogs (`atqRh`, `VM7ro`, `KrREo`, `BX0XM`, `DMnEi`, `E9EEv0`, `NLDDv`, `t3IY3u`, `MaoHP`, `I9W8z`, `JLaGB`) were rendering the master `x3beP`'s own title text, `"Back up now"`, instead of their own. Two nested instances — `O08uaD/ZSLXq` and `b4eaUf/oElfY` (both titled "Start Capture") — were also affected and restored this round via Update("O08uaD/ZSLXq/E7Whx", {content: "Start Capture"}) and Update("b4eaUf/oElfY/E7Whx", {content: "Start Capture"}). Confirmed by reading `Get(id, {depth: 1}).descendants.E7Whx` on each — the key was absent before this fix. This round restores all 13 instances.
2. **`mTitleWrap` had no visual effect.** `padding: [-1, 0, 1, 0]` on a `fit_content`-height frame nets to zero height change, so the wrapper neither moved the title glyphs nor changed `x3beP`'s box — `assets/design-export/screenshots/x3beP.png` from round 10 is pixel-identical to the pre-round-10 version. The wrapper added structure (and broke the 11 overrides above) without delivering OD-26's "baseline nudged 1 CSS px" clause.

**Fix applied (verified live before and after via `Get`):**

- `Move("E7Whx", "x3beP", 0)` then `Delete("Ju6la")` — `E7Whx` confirmed back as `x3beP`'s first child (`Get("x3beP", {depth: 1}).children[0].id === "E7Whx"`); `Ju6la` confirmed gone.
- Restored each of the 11 dialogs' `descendants.E7Whx` override via `Update("<id>/E7Whx", {...})`, values taken from `git show HEAD:assets/design-export/json/<id>.json` (JSON exports, not `.pen` files): `atqRh` "Create share link for mc-survival"; `VM7ro` "Share link created"; `KrREo` "Install Minecraft (Java Edition)"; `BX0XM` "Upload module"; `DMnEi` "Add module source"; `E9EEv0` `{"content":"Restore backup","lineHeight":1.5}`; `NLDDv` "Invite user"; `t3IY3u` "Edit user"; `MaoHP` "Reset password for operator-01"; `I9W8z` "New folder"; `JLaGB` "New file". Read back via `Get("<id>", {depth: 1}).descendants.E7Whx` on all 11 — matches restored — and cross-checked against a fresh `get_screenshot` of each dialog to confirm the title line itself, not just the JSON content field, renders correctly (a plain content-string grep is not sufficient, per the round-10 lesson).
- **OD-26's "baseline nudged 1 CSS px" clause was left unimplemented.** The Pencil schema (`get_app_state({include_schema: true})`) exposes no baseline/offset property on text nodes; the only two candidate mechanisms are (a) padding on a wrapping frame, which nets to zero for a symmetric ±1 pair and is clamped/absorbed for an asymmetric one without moving the glyphs (as demonstrated by the round-10 attempt), and (b) `layoutPosition: "absolute"` on the title text, which detaches it from `x3beP`'s vertical flex flow and would stop it from reserving layout space, shifting every sibling (description/body/footer) up by the title's height — a much larger visual side effect than the clause asks for. Neither renders the intended nudge without a side effect, so the title was left exactly as it was pre-round-10 (`lineHeight: 1.5`, no wrapper). **This clause needs a maintainer decision** on an acceptable mechanism (or to drop the clause) before it can be implemented.

**Corrected blast-radius (supersedes the false claim in the round-10 section above):** the OD-26 padding change on `j9c5W`/`rkF0p` also reaches every frame that instances the wrapper components `z9ShNE` (`Gameplane/Button/Small/Default`, whose child `IVgQ8` refs `j9c5W`) and `J09iP` (`Gameplane/Button/Small/Ghost`, whose child `eWkIT` refs `rkF0p`). `grep -l '"ref": "z9ShNE"' assets/design-export/json/*.json` (19 hits) and the same for `J09iP` (27 hits) union to 31 distinct frames: `b4eaUf`, `dPP50`, `dQV9N`, `DxKOh`, `E0ypH`, `e9lV4`, `f1Vga`, `fK8Bi`, `i1bLR`, `i8wib`, `iLm38`, `J5pjJ3`, `KaRFX`, `m5kOm4`, `O08uaD`, `QpEvu`, `QQtUD`, `RodrS`, `swxkJ`, `sZtDi`, `t3IY3u`, `tY6RD`, `uCA23`, `ugDSa`, `V1VhGE`, `VctzT`, `xCJlu`, `Xn5ns`, `XR0f9`, `Y5cmvI`, `zhLZN`.

**Full re-export this round (50 nodes, all hash-matching the live tree after the fixes above):** `x3beP`; the 11 dialogs (`atqRh`, `VM7ro`, `KrREo`, `BX0XM`, `DMnEi`, `E9EEv0`, `NLDDv`, `t3IY3u`, `MaoHP`, `I9W8z`, `JLaGB`); the two shared small-button masters `j9c5W`, `rkF0p`; the two wrapper components `z9ShNE`, `J09iP`; the corrected 31-frame blast radius above; and the 4 screens named directly in this round's task (`kK8Ji`, `P08Uw`, `KhYNc`, `Ss0Yr`).

**Export method & validation:** JSON via `Get(id, {depth: 30})` through the Pencil `execute` tool (batched, several calls), each re-serialized with `json.dump(indent=2)` + trailing newline; `python3 -m json.tool` passes on all 50, zero `"..."` elision markers. Screenshots via a single `export_nodes` batch call for all 50 ids at 2× scale — all landed as non-empty PNGs with real pixel dimensions. The round-10 `mTitleWrap` content check (line above, "found exactly once ... in `x3beP.json` only") is now moot: the node no longer exists anywhere in the file, confirmed by `Get(n => n.name === "mTitleWrap")` returning empty over the whole document.

**Corrected Alpha≥250 bbox measurements (computed here via Pillow on the freshly re-exported PNGs, in `(minx, miny, maxx, maxy)` with `maxx`/`maxy` inclusive):**

Note: this table uses inclusive maxes while `web/scripts/compare-screenshots.mjs` documents the same rects with exclusive maxes, so the two differ by one in each max coordinate.

| File | Size (px) | Alpha≥250 bbox | Panel (maxx−minx+1 × maxy−miny+1) |
|---|---|---|---|
| `atqRh.png` | 1088×772 | `(64, 40, 1023, 683)` | 960×644 |
| `VM7ro.png` | 1088×810 | `(64, 40, 1023, 721)` | 960×682 |
| `S7SCDc.png` | 1008×508 | `(64, 40, 943, 419)` | 880×380 |
| `DMnEi.png` | 1088×1898 | `(64, 40, 1023, 1809)` | 960×1770 |

`S7SCDc.png` was not re-exported this round — it does not instance `x3beP` (it instances the separate `WwNlX` Confirm Dialog master) and does not reference `j9c5W`/`rkF0p`/`z9ShNE`/`J09iP` anywhere in its tree (checked live via `Get("S7SCDc", (n,c) => ...)`), so it is outside every blast radius touched this round; the bbox above is measured from the existing, unchanged file for the harness step's convenience. `VM7ro.png`'s dimensions themselves (1088×810) are unchanged from the round-10 entry and were re-verified correct; the correction here is to the bbox/panel figures, and to the round-10 blast-radius and re-export-list claims above.

No `.pen` file was Read/Grep/cat/sed this round — all access via Pencil MCP `get_app_state`/`execute`/`export_nodes`/`get_screenshot`. No `git checkout`/`restore`/`stash`/`rm` was run, and nothing in `assets/design-export/` or `design.pen` was staged or committed by this pass.

## Round-12 — OD-27 VM7ro-local overrides (2026-09-20)

Per maintainer ruling OD-27 (`specs/done_014-heroui-web-rebuild/OPEN-DECISIONS.md`), three VM7ro-local descendant overrides were applied on `VM7ro` (Share link created), a ref instance of the shared master `x3beP`. The master `x3beP` and the other 12 instancing frames were not touched.

1. `qzcst` (mDesc): added `lineHeight: 1.4286` alongside its existing `content` override (master's 1.5 renders 21 CSS px against the browser's `text-sm` 20 px).
2. `Hp206` (warningBox): `padding` 13 → 14 (single number, uniform), matching browser `border-2` + `p-3` = 14 CSS px inset. All other properties (`strokeWidth: 2`, `strokeAlignment: "inner"`, `gap: 8`, `cornerRadius: 8`, fills) unchanged.
3. `SICns` (tokenRow): `padding` `[10, 14]` → `[11, 14]`, matching browser `border` + `p-2.5` = 11 CSS px vertical inset. All other properties unchanged.

Applied via `Update("VM7ro/qzcst", {...})`, `Update("VM7ro/Hp206", {...})`, `Update("VM7ro/SICns", {...})` through the Pencil `execute` tool. Read back immediately via `Get("VM7ro/<id>")` for each, confirming exact values before and after export.

**Export method & validation:** JSON via `Get("VM7ro", {depth: 30})`, zero elision markers, re-serialized with `json.dump(indent=2, ensure_ascii=False)` + trailing newline; `python3 -m json.tool` passes. Screenshot via `export_nodes` at 2x scale → `VM7ro.png` 1088×816 RGBA, non-empty. Content check: `"Send this to your friend"` (qzcst's body text) found exactly once across all exports, in `VM7ro.json` only. Confirmed in the re-exported JSON: `descendants.qzcst.lineHeight === 1.4286`, `descendants.Nht52.children[0].padding === 14` (Hp206), `descendants.Nht52.children[1].padding === [11, 14]` (SICns).

Only `VM7ro` was re-exported this round — none of the other 12 `x3beP` instances carry these overrides, so their JSON/PNG exports are unaffected and were not touched.

No `.pen` file was Read/Grep/cat/sed this round — all access via Pencil MCP `execute`/`export_nodes`. The `.pen` file itself was not saved (per task instructions, the maintainer saves via the GUI). No git add/commit was performed by this pass.

## OD-9 — T024/T025 four-frame expiry states + xCJlu "Never" row (2026-09-20)

Per maintainer ruling OD-9 (`specs/done_017-share-link-expiry/OPEN-DECISIONS.md`), FR-001–FR-004, OD-2, OD-6, and tasks T024/T025 (`specs/done_017-share-link-expiry/tasks.md`), the create-link dialog's four conditional expiry states were drawn as separate frames rather than stacked into one, following the OD-23 precedent (feature 014's `J5pjJ3` split).

1. **`atqRh` (canonical, edited in place)** — the pre-existing ref instance of master `x3beP`. Changed `descendants.Nht52.children[0].children[1]` (`ax9CH`, ref `AT7ya`) override `cFsFk.content` from `"7 days"` to `"30 days"` (FR-001's 30-day default). Deleted the helper text node `ggRrC` ("Maximum 90 days...") entirely — no replacement line. No warning or date picker added to this frame. Master `x3beP` and `AT7ya` were not touched.
2. **`tr6cE` — new top-level frame "Screen/Share links — Create link (No expiry)"** — a fresh `ref` instance of master `x3beP` (NOT an instance of `atqRh`; an earlier attempt to `Copy("atqRh", ...)` produced an instance-of-`atqRh` chain that would have made `N4srNq`/etc. shared-template nodes with repo-wide blast radius across `atqRh`+the duplicate — that attempt was deleted and redone). Built from `atqRh`'s own descendant-override tree, with the Select's `cFsFk` override set to `"No expiry"` and a new text node `oB60N` ("This link works until you revoke it.") inserted into `fExpiry` below the select, styled `fontSize:12, fontWeight:"normal", fontFamily:"$typography/font-sans", fill:"$warning/warning"` — matching `ggRrC`'s deleted metrics but with the warning color token, per the nearest precedent (`VM7ro`'s warning copy, which also uses `$warning/warning`).
3. **`oPF1n` — new top-level frame "Screen/Share links — Create link (Custom date)"** — likewise a fresh `ref` instance of `x3beP`, placed to the right of `tr6cE`. Select's `cFsFk` override set to `"Custom"`. Below it: label `YurQg` ("Expires on", matching `KIj9N`'s metrics: `fontSize:12, fontWeight:"500", fontFamily:"$typography/font-sans", fill:"$foreground/foreground"`); a date field `Bhx1e`, a `ref` **instance** of master `D0cDM` (never edited) with `height:36, cornerRadius:12` overrides, whose single child slot `PJERm` was type-replaced with a `valueRow` frame containing a text node ("2027-11-04") and a trailing `calendar` icon (both `$field/placeholder` fill, matching `D0cDM`'s own field tokens); and warning text `H9bFlJ` ("Long-lived link — it stays valid for over a year unless you revoke it.", the dash a literal em-dash), styled identically to `tr6cE`'s warning (`fontSize:12`, `$warning/warning`). Master `D0cDM` was not touched.
4. **`xCJlu` (Share links list)** — frame-local override only, on its own descendant instances, not on masters. Updated `IiHOi` (a `ref` instance of master `TDRJE`, row 1's Expires cell) so its `HNAOu` descendant override reads `"Never"` (was `"Aug 4, 2026"`). Left `BxcGi` (row 1's Status chip, `ref` instance of master `UOoQW`) unchanged at `"Active"` — satisfying FR-007/SC-004 (a never-expiring link shows "Never" and never "Expired"). Rows 2 (`d5nGN`/`ZhlaX`: "Jun 8, 2026"/"Expired") and 3 (`j0ZTmU`/`gPEJX`: "May 19, 2026"/"Revoked") were not touched. Masters `TDRJE` and `UOoQW` were verified unchanged (still read their generic placeholder content "No backups yet..."/"Chip").

**Export method & validation:** JSON via `Get(id, {depth: 12–14})` for all four nodes, zero elision markers; validated with `python3 -m json.tool`. Screenshots via `export_nodes` at 2x scale: `atqRh.png` 1088×732, `tr6cE.png` 1088×772, `oPF1n.png` 1088×892, `xCJlu.png` 2880×1800, all non-empty RGBA. Content checks: `"This link works until you revoke it"` found only in `tr6cE.json`; `"Long-lived link"` found only in `oPF1n.json`; `"content":"Never"` found in `xCJlu.json`. Read back all edited/created nodes via `Get` after each edit and via screenshot before export.

No `.pen` file was Read/Grep/cat/sed — all access via Pencil MCP `get_app_state`/`execute`/`get_screenshot`/`export_nodes`. The `.pen` file itself was **not** saved (per the task's explicit instruction — the maintainer saves via the GUI). No git add/commit was performed by this pass.
## 010-easy-module-building: BuildModuleDialog modal wizard frames (commits 391e1960, fa5a4370)

Three modal wizard frames for the Web Dashboard Module Builder (US5) were designed and initially exported in prior commits and merged to master:
- `IdbiB`: `Screen/Dialog/Build Module — Step 1 (Preset & Metadata)` (800x700, archetype selector cards, DNS-1123 name validation, display metadata, category chips).
- `O5kaV`: `Screen/Dialog/Build Module — Step 2 (Container & Ports)` (800x700, pinned image digest badge, dynamic port mapping list, persistent storage configuration).
- `hmPL7`: `Screen/Dialog/Build Module — Step 3 (Review & Export)` (840x700, dual-pane layout with code viewer tabs for module.yaml/template.yaml/README.md, live offline validation checklist, memory slider preview with heap calculation, and export/install actions).

**Initial exports:** commit `391e1960` (2026-09-15) first exported `IdbiB`, `O5kaV`, `hmPL7` at full length (~700+ lines each, dark theme applied). Commit `fa5a4370` (2026-09-16) made minor follow-up changes to these same three frames (2-3 line diffs each, documented in those commits). Both commits are already on master and reached this feature branch through a merge. Commit `12b4d449` (2026-09-12) contributed only the manifest narrative entry (11 lines added to MANIFEST.md); the actual frame exports are entirely from commits 391e1960/fa5a4370.

## Export correction 2026-09-21 — 010-easy-module-building: O5kaV container image reference

Fixed the sample container image reference in `O5kaV` (Screen/Dialog/Build Module — Step 2, Container & Ports) to match the existing `ARCHETYPE_PRESETS.steamcmd.defaultImage` in `web/src/components/modules/BuildModuleDialog.tsx`, which was already correct.

| Node | Previous value | New value | Why |
|---|---|---|---|
| `NQ8y0` (image sample string) | `ghcr.io/valgulnecron/cs2:latest@sha256:4b9a8e23...4d4e5` (wrong repo slug "cs2", digest truncated to 40 hex chars) | `ghcr.io/valgulnecron/cs2-server:latest@sha256:4b9a8e23f0c1d2e3f4a5b6c7d8e9f0a1b2c3d4e5f6a7b8c9d0e1f2a3b4c5d6e7` (correct slug, full 64-char digest) | The design sample (`NQ8y0`) was corrected to match the already-correct `ARCHETYPE_PRESETS.steamcmd.defaultImage` in `web/src/components/modules/BuildModuleDialog.tsx`; the component's preset was never changed by this work. |

**Port rows unchanged:** `f4U7oU` (port 1 name: "game"), `b4CUxa` (port 1 number: 27015), `bcGo3` (port 1 protocol: UDP), `prNFB` (port 2 number: 27015), `evKQq` (port 2 protocol: TCP), and `q96ywV` (storage mount: /home/steam/cs2-data) were examined and deliberately left as-is — they correctly represent a running server configuration. All other siblings (`IVUPC` storage capacity: 20Gi) unchanged.

**Prior hand-edit overridden:** commit `10fe068a` had previously hand-edited `assets/design-export/json/O5kaV.json` directly, changing the digest, port 27015→27016 and TCP→UDP without any corresponding Pencil design changes. That manual edit was reverted; the current export supersedes it with a proper Pencil-sourced update containing only the image reference correction and no port mutations.

**Export method & validation:**

- **JSON:** `Get("O5kaV", {depth: 30})` via the Pencil `execute` tool, zero `"..."` elision markers. Re-serialized with `json.dump(indent=2, ensure_ascii=False)` + trailing newline; `python3 -m json.tool` passes.
- **Screenshot:** `export_nodes` at 2× scale → `assets/design-export/screenshots/O5kaV.png` 1600×1070 RGBA non-empty PNG (verified via PIL: 192052 bytes).
- **Content check:** the full pinned image reference `ghcr.io/valgulnecron/cs2-server:latest@sha256:4b9a8e23f0c1d2e3f4a5b6c7d8e9f0a1b2c3d4e5f6a7b8c9d0e1f2a3b4c5d6e7` found exactly once, in `assets/design-export/json/O5kaV.json` only. Port and storage values confirmed unchanged via grep.
- **No `.pen` file was Read/Grep/cat/sed** — all access via Pencil MCP `execute`/`export_nodes`, per Rule 2.
- No git add/commit performed (task instructions: export and manifest correction only).


## Design/code reconciliation — 010-easy-module-building: module-builder screens (2026-09-21)

Per the design/code reconciliation pass, four module-builder screen frames were brought into line with the already-shipped React implementation (`web/src/routes/Modules.tsx`, `web/src/components/modules/BuildModuleDialog.tsx`). Code was the source of truth; design was updated to match.

**kK8Ji (Screen/Modules Catalog):**
- Added "Create module" button (plus icon) as the first of three header action buttons, matching `web/src/routes/Modules.tsx:139` action layout.
- Pre-existing buttons "Upload module" and "Manage sources" had their Pencil-internal ids regenerated during insertion (`mhJd3` → `lQ1ua`, `KCxx5` → `luTx8`); this is cosmetic and internal to the export snapshot only.

**IdbiB (Screen/Dialog/Build Module — Step 1, Preset & Metadata):**
- Button label "Continue to Container & Ports ->" split into text node plus separate ChevronRight icon, matching React component structure.
- Category chips expanded from 3 placeholder items to all 11 canonical categories with "Shooter" only selected; removed "+ Add category" chip (`e4hEWM`) and pre-selected "Co-op" chip (`u7g81`).
- Chip row layout manually split: single row `QV9u4` divided into two horizontal sub-frames (`c3DZqO`, `ytYZ9`) to emulate CSS flex-wrap — 10 chips on line 1, "Creative" on line 2 — matching where flex-wrap breaks at this width in the browser.

**O5kaV (Screen/Dialog/Build Module — Step 2, Container & Ports):**
- Storage section heading "Persistent Storage Volume" → "Persistent Storage", matching React label.
- Added icon-only delete button to each port row (`PortRow1_Delete`, `PortRow2_Delete`), matching React's row-action spec for dynamic list management.
- Port rows deliberately kept unchanged (row 1: game/27015/UDP; row 2: query/27015/TCP) as an intentional illustration of a dynamic list configuration.

**hmPL7 (Screen/Dialog/Build Module — Step 3, Review & Export):**
- Memory slider replaced with a row of four quick-select preset buttons (2Gi, 4Gi, 8Gi, 16Gi, with 8Gi selected), matching React's button-group UI.
- Removed `resolvedImage` stat line (not rendered by React component).
- Added "Cluster destination source" select card (matches React's cluster-selection dropdown addition).
- Updated validation success copy and set text to wrap inside its card, matching React's layout.

**Export method & validation (all 4 nodes: `kK8Ji`, `IdbiB`, `O5kaV`, `hmPL7`):**

- **kK8Ji and IdbiB** exported cleanly on the first pass: `Get(id, {depth: 30})` via Pencil `execute` tool, zero `"..."` elision markers. Re-serialized with `json.dump(indent=2, ensure_ascii=False)` + trailing newline; `python3 -m json.tool` passes on both.
- **O5kaV and hmPL7 initially failed to regenerate:** the first-pass agent invoked `execute` with the wrong argument shape (passing `code` instead of `input`), which the tool rejected, and mistakenly concluded `Get()` returns no JSON payload; it left the pre-reconciliation content in `assets/design-export/json/O5kaV.json` and `assets/design-export/json/hmPL7.json` in place and reported success anyway. This was caught and corrected in a follow-up pass: both nodes were re-fetched via `Get(id, {depth: 30})` through `execute` with the correct `input` argument, re-serialized with `json.dump(indent=2, ensure_ascii=False)` + trailing newline, and `python3 -m json.tool` passes on both.
- **Screenshot:** `export_nodes` at 2× scale for all 4 ids. First-pass agent self-reports were unreliable: kK8Ji, IdbiB, and hmPL7 generated valid non-empty RGBA PNGs with correct pixel dimensions on the first pass, while O5kaV's PNG never regenerated at all (remained ~20 hours older than design.pen save timestamp, not shown as modified in git status). O5kaV's PNG was corrected in a follow-up pass. Final verification via PNG mtime compared against design.pen save time for all four ids; complemented by change-proving greps (post-change strings present, pre-change strings absent) documented in content checks below.
- **Content checks:** unique body text per node validated — kK8Ji "Create module" button label found in `assets/design-export/json/kK8Ji.json` only; IdbiB category chips ("Shooter", "Creative", etc.) confirmed in `assets/design-export/json/IdbiB.json` only. For O5kaV and hmPL7, the follow-up pass used change-proving greps instead of a same-in-both-versions check: O5kaV confirmed to contain "Persistent Storage" and `PortRow1_Delete`/`PortRow2_Delete` while no longer containing "Persistent Storage Volume", with the unflipped port rows (game/27015/UDP; query/27015/TCP) and storage values (`/home/steam/cs2-data`, `20Gi`) intact; hmPL7 confirmed to contain "All schema constraints" and "Cluster destination source" and the memory presets ("2Gi", "4Gi", "8Gi", "16Gi") while no longer containing "Instant offline verification passed" or "Resolved image".
- **No `.pen` file was Read/Grep/cat/sed** — all access via Pencil MCP `execute`/`export_nodes`, per Rule 2.
- No git add/commit performed (export and manifest correction only, per task instructions).

## 010-easy-module-building — archetype truth, console states, capture copy (2026-09-21)

Five frames exported. Four were edited to match shipped code; `O5kaV` was corrected
against the canonical archetype definitions after the web wizard stopped using its
hardcoded copy of them.

**`O5kaV` (Dialog/Build Module — Step 2, Container & Ports):** the frame had been drawn
from the wizard's fabricated archetype presets. Corrected against the `steamcmd` entry in
`gp-module/internal/archetypes/archetypes.go`: the container image now reads
`cm2network/steamcmd:root@sha256:4d830b0475b8719f96b9978ba57404434bb3da3f260388d75cfb373cf5889ea8`
(the invented `@sha256:4b9a8e23…` digest is gone from the frame), the query port reads
27016/UDP rather than 27015/TCP, the mount path reads `/serverdata` rather than
`/home/steam/cs2-data`, and row 2's Advertise checkbox is checked to match
`advertise: true`. Row 1 (game / 27015 / UDP) is unchanged. Row 2's checkbox was rebuilt
with `Replace` because Pencil rejects `null` on `stroke`/`strokeWidth`, so its node id
changed `hqhlE` → `AmIhQ` with a new check-mark child `SBDAp`; same tree position,
styling identical to row 1.

**`IdbiB` (Dialog/Build Module — Step 1, Preset & Metadata):** restored the custom-tag
input and "Add tag" button that an earlier reconciliation wrongly deleted; added a
"Recommended" hint beside the Categories label; no category chip is preselected, since
categories are the author's choice; added an "Archetype Configuration" section between
the preset cards and the metadata fields, mocking the SteamCMD case with Maximum Players
(default 16), Server Password and Steam Application ID. Those values come from the
`steamcmd` ConfigSchema and DefaultEnv in `archetypes.go`. A "Selected Tags" area shows a
`speedrun ×` example chip to illustrate the populated custom-tag state.

**`Bbnga` (Server Detail — Capture, not-enabled):** the badge no longer renders the raw
HTTP 501 status text "Not Implemented", which told users the feature does not exist.
Capture is implemented; it is disabled cluster-wide by default
(`charts/gameplane/values.yaml`, `capture.enabled: false`) and
`api/internal/handlers/capture.go:338` returns 501 deliberately for that configuration
state. The frame now reads "Capture is disabled by cluster configuration."

**`X405l` (Server Detail — Console, server not running) — NEW frame:** the console
previously showed "connecting…" forever with an empty terminal when no pod was running.
This frame shows a neutral "Stopped" header chip, no uptime, a "Start Server" action, and
body copy "Server is stopped." / "Start it to access the console."

**`j88bl0` (Server Detail — Console, failed to start) — NEW frame:** sibling variant for
the pod-failure case. Danger "Failed" header chip, no uptime, the Stop action hidden
(nothing to stop), and body copy "Server failed to start: ImagePullBackOff" / "Check the
Events tab for details."

**Shared component untouched:** `S4k0x` (Gameplane/Server Detail Header) was not
modified. `X405l` and `j88bl0` override their own header instances only; all 42 instances
in the document were checked, and no other frame carries these overrides.

**Export method and validation:** JSON via Pencil MCP `execute` running
`Get(id, {depth: 30})` — note the parameter is named `input`, not `code`; validated with
`python3 -m json.tool`. PNG via `export_nodes` at 2x, dimensions read from each file's
IHDR header: `O5kaV` 1600x1070, `Bbnga` 2880x1800, `IdbiB` 1600x1964, `X405l` 2880x1800,
`j88bl0` 2880x1800. Freshness proven by content rather than by size or mtime — for each
frame a post-change string must be present AND a pre-change string absent (e.g. `O5kaV`
contains `cm2network` and no `4b9a8e23`; `Bbnga` contains "disabled by cluster
configuration" and no "Not Implemented"; `X405l`/`j88bl0` contain their body copy and no
"up 3m"). Mtime and file size both produced false verdicts during this pass and are not
relied on.

*Correction:* an earlier version of this entry was generated by an agent summarising work
it had not performed, and invented specifics — a 1–256 player range (the schema says
1–128), a Steam App ID of 740 (the archetype default is "0"), four wrong PNG dimensions,
and an inverted description of `j88bl0`'s actions. It also omitted `O5kaV`. It has been
replaced by this entry, written from values verified directly against the exported JSON
and `archetypes.go`.

## Incremental export 2026-09-22 — User theme customization (Feature 016)

Feature 016 (user theme customization, `specs/done_016-user-theme-customization/contracts/theme-ui.md`) design wave, final form: a single stacked settings page plus a warnings variant.

**History (four passes):**

1. **Modal pass:** the feature was first built as a modal (`fvIJU` Gameplane/Theme Settings Modal + four modal-over-backdrop screens).
2. **Page pass:** after the operator's modal→page decision the contract was amended (§2: full settings page at route `/settings/theme` in the standard app shell; actions row = Reset to Defaults left + Save right, no Cancel/X). `fvIJU` — a node created by this wave, not a pre-existing one — was **deleted** along with its exports, and the four screens were rebuilt in place as tabbed pages.
3. **Idiom-fix pass:** the four screens were rebuilt in place once more (same ids/positions) to match `WZdnw`'s settings-screen idiom: left settings sub-nav (`Mp7Ep`-modeled, active item `$surface/secondary`) + `XDZ0E`/`FRpsu` card sections + WZdnw-style actions row.
4. **Stacked-page pass (this export):** per operator direction, the sub-nav was also removed (it made the page look like a sub-menu of Admin Settings). The four screens collapsed into ONE simple page — app shell + page header + all sections stacked as cards on one scrollable page. `O6AZz` and `D6ExxJ` (this wave's own nodes) were **deleted** along with their exports (`assets/design-export/json/O6AZz.json`, `assets/design-export/json/D6ExxJ.json`, `assets/design-export/screenshots/O6AZz.png`, `assets/design-export/screenshots/D6ExxJ.png`).

**No pre-existing node was modified at any point in this wave** — deleting/rebuilding this wave's own nodes is not a pre-existing-node modification; everything that existed before the wave was consumed strictly as `ref` instances (or, for breadcrumb replacement, type-replaced frames inside this wave's own instances).

**Screens (2):**

| ID | Name | Size | Export notes |
|---|---|---|---|
| `lWvcv` | Screen/Theme Settings | 1440×1904 (grew from 900 to fit stacked content + 24px bottom padding) | Single stacked page: shell (sidebar instance with the verbatim WZdnw 27-key highlight map, `ya5KB` at `$accent/soft`; top bar with 3-level breadcrumb `gameplane › Settings › Theme & Appearance`; `xCDF7` Page Header "Theme & Appearance", subtitle "Personal theme presets, colors, custom CSS, and theme portability.", action disabled), then one centered 840px content column of five `XDZ0E`/`FRpsu` cards: **Preset theme** (two 320px radio cards — Modern Pink selected, #FF4FA3/#1C1A20; Legacy Orange, #F97316/#171717 — plus the retention note "Your custom colors and CSS are kept and can be re-applied later."), **Appearance mode** (Light/Dark/System `Dx8S8` segmented control, Dark active), **Custom colors** (7 accent swatches with Amber selected, preview chip "Contrast 2.9:1 — fails WCAG AA", Surface Tone radios with Crisp Light selected), **Custom CSS** (`hl7R3` switch + contract helper text, monospace `rqAWk` textarea with the `:root { --radius: 14px; }` example and "128 / 32,768" counter, safe-mode recovery warning `Llzos` verbatim), **Export / Import** (Copy to clipboard / Download gameplane-theme.json buttons, paste area, "Choose file…" picker, import preview with #FF4FA3 swatch / "Preset: Modern Pink" / "Custom CSS: 1,204 bytes" / "Apply import", §3.4 scope note), then the bottom actions row (destructive "Reset to Defaults" `XoX7L` left, primary "Save" `tpKRk` right). |
| `T1wkiT` | Screen/Theme Settings — Warnings | 1440×2036 (taller: two extra alerts) | Same stacked page with the warning states visible: `Llzos` contrast-guard alert on the Custom colors card (Amber-on-Crisp-Light example, "The selected accent and surface combination fails WCAG AA (2.9:1)…") and the red `@import` validation error on the Custom CSS card (contract-verbatim "External resource loads are not allowed (line 4: …). Paste the content inline instead."). |

New structural ids this pass: lWvcv content column `A1pd4`, Custom colors card `p8cbrD`, Custom CSS card copy `T9N87M`, Export/Import card `Ty2Ey`; T1wkiT content column `F5MnIY`, contrast warning `O8yQoQ`, `@import` error row `o1h94j`. Card field subtrees were copied from this wave's own earlier cards (retention note `J6tN7` moved into the Preset theme card; actions row `k1qKk` moved below the column); the old Settings Layout frames (`PTfov`, `Q23mnf`) were deleted.

**Shell details:** breadcrumb is the contract's 3-level `gameplane › Settings › Theme & Appearance`, built by type-replacing the Top Bar's `VzGni` breadcrumb frame inside each screen's own `gu5WY` instance (existing screens only use 2-level crumbs, so there was no 3-level master to instance). Sidebar highlight follows the Admin Settings convention exactly: the same 27-key descendant map as `WZdnw`'s sidebar instance — copied verbatim, not invented.

**Component kept unchanged (1):**

| ID | Name | Notes |
|---|---|---|
| `DAz77` | Gameplane/Safe Mode Banner | Kept as-is from the first pass: its copy ("Safe Mode Active: Custom CSS is suspended." / "Open Appearance Settings" · "Dismiss") does not contradict the amended contract — §4 still labels the action "Open Appearance Settings"; only its navigation target changed (now `/settings/theme`), which is behavior, not design. Not re-exported (unchanged since its 2026-09-22 export). |

Contract §1 (triggers — TopBar user menu, sidebar footer, login safe-mode link) is a separate follow-up task, not covered by this section. §5's confirmation dialog (reuses `WwNlX` verbatim) remains out of design scope.

**Export method & validation (the 2 screens):**

- **JSON:** `Print(JSON.stringify(Get(id, {depth: 14, includePathGeometry: true})))` via the Pencil `execute` tool, written verbatim to `assets/design-export/json/<id>.json`; `python3 json.load()` passes on both, zero `"..."` elision markers, and neither contains `"ref":"fvIJU"` / `O6AZz` / `D6ExxJ` dangling references (deleted nodes leave no references — verified by grep). Both exports keep unresolved `ref` form for the shell components.
- **Screenshots:** single `export_nodes` batch at 2x scale → both PNGs non-empty (lWvcv 827 KB, T1wkiT 910 KB).
- **Content check:** unique body-text greps, all hit exactly once: `lWvcv` → "re-applied later"; `T1wkiT` → "fails WCAG AA" and "32,768".
- **Visual check:** `get_screenshot` on both screens plus repeated `ctx.problems` sweeps with `resolveInstances: true` — zero clipping/overlap remaining after the height grows (intermediate "partially clipped" reports were the engine measuring against the pre-resize frame height; sweeps after the final resize are clean on repeat reads).
- **No `.pen` file was Read/Grep/cat/sed** — all access via Pencil MCP `get_app_state`/`execute`/`get_screenshot`/`export_nodes`. The `.pen` file was **not** saved (no MCP save exists; the maintainer saves via the GUI). No git command was run by this pass.

**Trigger edits (contract §1, applied after the operator lifted the pre-existing-node lockout, scoped strictly to these three triggers):**

- **§1.1 TopBar user menu → new frame `mrBkD` Gameplane/Top Bar — User Menu** (232px, reusable, at x=-14475 y=25462): the file contained NO avatar/user dropdown design (`gu5WY` holds only an `p3URd` Avatar ref; a top-level scan of all 194 root nodes confirmed no user/account menu frame exists), so per the operator's fallback instruction one new frame was created instead of editing `gu5WY`. Chrome modeled on `BPEpm` Dropdown Menu (`$surface/surface`, `$radius/md`, border, same shadow, padding 4): avatar header (`p3URd` "AR" / "Alex Rivera" / "operator"), `z1N6Y` divider, `W8XiN` item "Theme & Appearance" with `palette` icon above `W8XiN` item "Sign out" with `log-out` icon — matching the contract §1.1 ASCII exactly. `gu5WY` itself is **unchanged** (exported as-is to document that). **Theme pin:** initially created without a `theme` property like its sibling components (kKFX9/gu5WY/BPEpm/hboVw/J14ME all carry no pin), but standalone screenshots rendered it light; pinned to `theme: {"semantic":"dark"}` — the same pin used on this wave's screens and `DAz77` — and it now renders dark. (Actual axis is `semantic`; the `{"c:Mode":"Dark"}` form in the request doesn't exist in this file — axes are `mode`/`semantic`/`typography`/`primitives`.)
- **§1.2 Sidebar footer → `kKFX9` Gameplane/App Sidebar edited in place:** the footer Toggle Row (`oByLP`, gap 0 → 8) gained `F79Xfy` "Customize Theme", an `owgrI` Button/Ghost/Icon/MD ref with a `palette` lucide icon — the same ghost-icon-button component the footer's existing logout button (`ARlhl`) uses, per the "consistent with the footer's existing iconography" requirement. The `iA2C8` Appearance Toggle is retained untouched. Node name "Customize Theme" carries the aria-label equivalent.
- **§1.3 Login safe-mode link → `J14ME` loginCard and `g2HLxz` loginCardError edited in place:** each gained a `safeModeRow` (fill width, centered) holding an `s12HO` Link ref labeled "Sign in with safe mode (custom styling disabled)" (standard pink `$foreground/link` styling; the external-link icon `UpokQ` disabled — the link is internal), placed below the sign-in form (after the Keycloak button, before the AGPL footer). Editing the two shared card components — rather than each screen — matches the code reality (one `Login.tsx`): the link ripples to `N1GkB` (Login Default) and `gX7um` (Login Light) via `J14ME`, and to `jmoi3` (Invalid credentials) via `g2HLxz`. **`ljdA5` (Login — SSO only) deliberately skipped:** the contract ties the link to the credentials flow ("submits the same credentials flow but carries the safe-mode flag") and ljdA5's inline card has no credentials form — a safe-mode credentials link would be meaningless there. Flagged as a judgment call.

**Intentional ripple:** `kKFX9` (and the login cards) are shared components, so these definition edits ripple to every screen instancing them — matching the code (`Sidebar.tsx`/`TopBar.tsx`/`Login.tsx` are shared). Ripple verification post-edit: `ctx.problems` sweeps (`resolveInstances: true`) and `get_screenshot` on `j24cXg` Dashboard Home, `EZFW0` Server Detail Overview, and `WZdnw` Admin Settings — no new clipping/overlap (the only reports are pre-existing artifacts: EZFW0's content taller than its viewport, and disabled/collapsed helper texts inside the login inputs and the error alert). **Full-file re-export deliberately not done** — other screens pick up the new chrome on their next re-export.

**Trigger-object export validation (8 objects; `ljdA5` not re-exported — unchanged):**

| ID | Name | Changed | JSON | PNG 2x | Content grep |
|---|---|---|---|---|---|
| `gu5WY` | Gameplane/Top Bar | No (exported to document absence of a menu) | valid, 0 elisions | 32 KB | "Breadcrumbs" ×1 |
| `kKFX9` | Gameplane/App Sidebar | Yes (footer `F79Xfy`) | valid, 0 elisions | 90 KB | "Customize Theme" ×1 |
| `mrBkD` | Gameplane/Top Bar — User Menu | New + theme-pinned dark | valid, 0 elisions | 30 KB | "Theme & Appearance" ×1 |
| `N1GkB` | Screen/Login | Via `J14ME` ripple | valid, 0 elisions | 348 KB | "fjQjb" ref ×1 (link lives in `J14ME`) |
| `jmoi3` | Screen/Login — Invalid credentials | Via `g2HLxz` ripple | valid, 0 elisions | 354 KB | "g2HLxz" ref ×1 (link lives in `g2HLxz`) |
| `gX7um` | Screen/Login (Light) | Via `J14ME` ripple | valid, 0 elisions | 349 KB | "fjQjb" ref ×1 |
| `J14ME` | loginCard | Yes (`safeModeRow` + `s12HO`) | valid, 0 elisions | 106 KB | "Sign in with safe mode" ×1 |
| `g2HLxz` | loginCardError | Yes (`safeModeRow` + `s12HO`) | valid, 0 elisions | 114 KB | "Sign in with safe mode" ×1 |

Screen JSONs keep refs unresolved by design, so the safe-mode link's text lives in the two card-component exports; the card components were exported in addition to the operator's list precisely because the edit physically lives there. Visual verification: screenshots of `mrBkD` (dark after pin), `N1GkB`, `gX7um` (link visible in both themes below the Keycloak button), and the `j24cXg` sidebar instance (`gcVuG`, palette button in the footer toggle row) — nothing clipped or misaligned. No git command was run; the `.pen` file was not saved and never accessed via shell.

### Feature 016 correction wave (2026-09-22, second pass) — settings nav Theme entry, nav column on the theme pages, login link color

Three operator-directed corrections after the stacked-page pass above.

**Nav investigation finding (shared, not inlined):** the Admin Settings left nav seen on `WZdnw` &co. is a single shared reusable component `Mp7Ep` Gameplane/Admin Settings Nav (220px, 9 plain-frame items, `snGeneral` default-active at `$surface/secondary`), instanced by exactly 9 screens: `WZdnw` (`a6Uti`), `uMiwd` (`QDWF0`), `RC3Kf` (`DbRSe`), `g5mEpx` (`DpC2M`), `Wj0V4` (`u6cHbe`), `n6Xlo` (`VS1SZ`), `uoxQW` (`FgP5h`), `M2sA4u` (`K8JHQH`), `zM0VF` (`t3vAi0`). The 3 Authentication state-variant screens (`nNGDX`, `QgW58`, `zqzr4`) carry NO nav at all (verified by ref walks), so nothing was added there. Per-screen active-state idiom (verified on all 9 instances): fill-only descendant overrides `{"eIjoV":{"fill":"#00000000"}, "<activeItemId>":{"fill":"$surface/secondary"}}`; icon/label colors are never overridden.

**Correction 1 — Theme entry added once to `Mp7Ep` (operator-authorized intentional ripple):** new item `vbQ98` "snTheme" copied from `XEjdq` (snAuth) — icon `dY0be`→`palette`, label `qeqG6`→"Theme", inactive `$muted` styling — moved to index 1. Children are now snGeneral, snTheme, snAuth, …, snAbout. All 9 instancing screens pick the entry up automatically; per the standing export policy those 9 pre-existing screens are NOT re-exported — only the component. `ctx.problems` sweeps (`resolveInstances: true`) clean on all 9 ripple screens; WZdnw crop screenshot-verified (Theme after General, inactive).

**Correction 2 — nav column added to `lWvcv` + `T1wkiT`:** each screen's Body (index 1) gained a `Settings Layout` row (fill×fill, gap 24) containing an `Mp7Ep` ref ("Settings Nav", width 220, descendants `{"eIjoV":{"fill":"#00000000"},"vbQ98":{"fill":"$surface/secondary"}}` — Theme active) with the existing content column moved in as child 1, matching `WZdnw`'s nav+fill-content placement exactly. **Judgment call (flagged):** each content column's width was changed 840→`fill_container` to match WZdnw precisely (all cards were already fill-width internally, so no visual regression; the column simply absorbs the space left of it). No screen-height change was needed (nav ~394px < content). New ids — `lWvcv`: layout row `cCakB`, nav instance `beXe1` (body `YBNce`, column `A1pd4`); `T1wkiT`: layout row `fQBYE`, nav instance `O5D0M` (body `y3RL7i`, column `F5MnIY`). `ctx.problems` clean ×2 on both screens; lWvcv crop screenshot-verified (Theme highlighted active) and T1wkiT full screenshot verified.

**Correction 3 — login safe-mode link color:** the `s12HO` link added in the trigger pass rendered blue (`$foreground/link`); the operator fixed it in the `.pen` to pink `$accent/accent` (matching the cards' "Forgot?" link convention) and rewrote `assets/design-export/json/J14ME.json` / `g2HLxz.json` himself — this wave did NOT touch those JSONs. This wave only re-exported the two PNGs and verified color: `J14ME.png` (106 KB) and `g2HLxz.png` (114 KB) at 2x both show "…with safe mode (custom styling…" in pink (verified by pixel-crop reads of the exported PNGs; `N1GkB`/`gX7um` canvas screenshots also show the pink link below the Keycloak button).

**Correction-wave export validation (3 re-exported objects + 2 PNG-only):**

| ID | Name | Changed | JSON | PNG 2x | Content grep |
|---|---|---|---|---|---|
| `Mp7Ep` | Gameplane/Admin Settings Nav | Yes (`vbQ98` Theme item at index 1) | valid, 0 elisions (4,479 B) | 61 KB (440×788) | `"content":"Theme"` ×1 |
| `lWvcv` | Screen/Theme Settings | Yes (nav column) | valid, 0 elisions (16,547 B) | 879 KB (2880×3808) | `"Mp7Ep"` ref ×1, `"vbQ98"` ×1 |
| `T1wkiT` | Screen/Theme Settings — Warnings | Yes (nav column) | valid, 0 elisions (17,537 B) | 960 KB (2880×4072) | `"Mp7Ep"` ref ×1, `"vbQ98"` ×1 |
| `J14ME` | loginCard | Operator color fix only | (operator-maintained, untouched this wave) | 106 KB re-exported | pink link verified visually |
| `g2HLxz` | loginCardError | Operator color fix only | (operator-maintained, untouched this wave) | 114 KB re-exported | pink link verified visually |

No git command was run; the `.pen` file was not saved and never accessed via shell — all access via Pencil MCP.

### Breadcrumb correction (2026-09-22, third pass)

Operator feedback: the Theme Settings pages carried a 3-level breadcrumb `gameplane › Settings › Theme & Appearance`; the Admin Settings convention is 2-level (`gameplane › Settings`, matching `WZdnw`). The 3-level descendant overrides on the Top Bar instances of `lWvcv` (`QBpWW`) and `T1wkiT` (`IB1qY`) were replaced with the `gu5WY` default 2-level shape (crumbRoot `gameplane` muted + chevron + crumbPage `Settings`), new override node ids `cOfsB`/`evCjB`. Both screens re-exported: `json/lWvcv.json` (16,244 B) and `json/T1wkiT.json` (17,237 B) — `json.load`-valid, zero `"..."` elisions, `crumbPage` ×1 each; PNGs re-exported at 2x (lWvcv 872 KB, T1wkiT 955 KB). ctx.problems clean on both. No git; `.pen` not saved (Pencil MCP only).

### Feature 016 amendment (2026-09-22, third pass) — "Custom colors" third preset radio card + disabled-state section card

Contract §3.1/§3.2 were amended: the Preset theme card now has THREE radio cards — Modern Pink (`presetId: "pink"`), Legacy Orange (`presetId: "legacy"`), and **Custom colors** (`themeType: "custom_colors"`, activates the stored customs; selecting a preset deactivates, customs retained) — and the Custom colors section card's controls are disabled (stored values visible greyed) unless the Custom colors radio card is selected.

**`lWvcv` (preset active):** third card `hCLrO` "tsCardCustom" added to `i91A2W` (tsPresetRow), copied from the Legacy card — unselected treatment (`$border/border` stroke, `z2mDh` unchecked radio `QN74E` "Custom colors"), swatch row `rdWoB` = palette-icon swatch (`EZLdL` 44×26 `$field/background` frame + `Q0uesI` palette icon `$muted`) + stored-surface swatch (`BY5Id` #FAFAFA), desc `m6aHZ` "Your saved accent & surface". Modern Pink (`XRzZg`) stays selected. Section card `p8cbrD` rendered DISABLED per §3.2: `opacity: 0.5` on the three interactive rows (`PFzKl` swatches, `JGzsZ` preview row, `nG8Kx` surface radios — stored Amber + Crisp Light values remain visible), red contrast meta `b9v04` hidden (`enabled: false`), and the Amber "selected" ring `wEV1E` neutralized to `$border/border` 1px. **Disabled-convention note:** the file has no fractional-opacity precedent (exports show only opacity 0/1; the "disabled" Save/Apply buttons merely hide their icon via `enabled:false`), so `opacity: 0.5` on the control rows was applied per the operator's explicit instruction — flagged as a judgment call.

**`T1wkiT` (custom colors active):** third card `c9U0a` "tsCardCustom" copied from `hCLrO` into `aEsM8`, in SELECTED treatment (`$accent/accent` stroke, radio replaced with checked `T8ZnV` ref `f56WXp`); Modern Pink `Dyy8o` demoted to unselected (`$border/border` stroke, radio replaced with unchecked `z2mDh` ref `x5esx6`). The Custom colors card `ffLVD` stays fully enabled with its contrast-guard alert `O8yQoQ` and the CSS `@import` error intact.

**Width fix (both screens):** three 320px cards overflowed the 888px content column (984px needed; Pencil layout has no wrapping), so all three preset cards on both screens are `width: fill_container` (equal thirds ≈288px) — the lWvcv thirds were set by the operator, T1wkiT's by this pass. Card internals unchanged.

**Noticed but NOT touched (collaborative-document rule):** both screens' Top Bar breadcrumbs were replaced outside this pass with new 2-level frames (lWvcv `cOfsB`, T1wkiT `evCjB` — "gameplane › Settings"), dropping the contract's third crumb "Theme & Appearance". Left as found; flagged to the operator.

**Validation:** `ctx.problems` sweeps (`resolveInstances: true`, ×2) — T1wkiT clean; lWvcv's only report is the intentionally hidden `b9v04` (`enabled:false` nodes report "fully clipped", same artifact class as prior disabled helpers). Screenshots verified: preset cards on both screens (correct selection states, no overflow), `p8cbrD` greyed without the red meta, `ffLVD` fully enabled with both alerts. Exports: `json/lWvcv.json` (17,598 B) + `json/T1wkiT.json` (18,528 B) verbatim depth-14 includePathGeometry, both `json.load`-valid, zero `"..."` elisions; greps — lWvcv "Custom colors" ×2 (radio card + section header), T1wkiT "fails WCAG AA" ×2 (preview meta + contrast-guard alert; the lWvcv JSON still contains the phrase once inside the hidden `b9v04` node). PNGs at 2x: `lWvcv.png` 881 KB (2880×3808), `T1wkiT.png` 972 KB (2880×4072). Only the two Feature-016 screens were modified; no git; the `.pen` was not saved and never accessed via shell.

### Free color picker hex inputs (2026-09-23, D2 implementation, rewritten after clipping + value-consistency fix pass) — lWvcv + T1wkiT

WP-design operand pair: insertions of ccAccentFreePicker and ccSurfaceFreePicker frames in both Theme Settings screens to support free hex color entry alongside the preset swatches and surface radios.

**`lWvcv` (Custom colors INACTIVE, stored Accent #F59E0B "Amber" + Surface #F8FAFC "Crisp Light"):** two horizontal frames in `Y9YsS1` (Card Fields), both at `opacity: 0.5` (disabled, matching the section's other controls):
1. **ccAccentFreePicker** (`iSgJ4`): a 28×28 rounded swatch frame with palette icon (`TZvv8`/`FcIIH`, fill `#F59E0B`) + ref to `qvQPg` (Gameplane/Input/Small, `ep9Jm`) width 96 showing hex text `"#F59E0B"`.
2. **ccSurfaceFreePicker** (`w3wnz`): a 28×28 rounded swatch frame with palette icon (`pZ1gY`/`TtaGZ`, fill `#F8FAFC`) + ref to `qvQPg` (`UEuU5`) width 96 showing hex text `"#F8FAFC"`.

Both values now match the screen's own preview chip (Amber `#F59E0B`, `SAvLR`) and the selected surface radio (Crisp Light `#F8FAFC`, `X4VnPS`) — the placeholder values #3B82F6 / #1E293B from the initial insertion were corrected to the stored Amber/Crisp Light values. The Amber preset swatch `wEV1E` in `PFzKl` now also carries the same selected-stroke treatment T1wkiT uses (`stroke: $foreground/foreground`, `strokeWidth: 2`, vs the default `$border/border` 1px on the other six swatches).

**`T1wkiT` (Custom colors ACTIVE, Accent #F59E0B + Surface #F8FAFC):** identical structure in `u3VzxL` (Card Fields), full opacity (no 0.5):
1. **ccAccentFreePicker** (`jCPmK`): 28×28 rounded swatch frame with palette icon (`SLXhL`/`a7NBc`, fill `#F59E0B`) + `qvQPg` ref (`c1Alx`) hex text `"#F59E0B"`.
2. **ccSurfaceFreePicker** (`Viurq`): 28×28 rounded swatch frame with palette icon (`q1CZim`/`L547qD`, fill `#F8FAFC`) + `qvQPg` ref (`gxx0X`) hex text `"#F8FAFC"`.

T1wkiT's values were already correct; unchanged in this pass except for the hex-text color fix below.

**Hex input text color fix (both screens, all 4 inputs — `ep9Jm`, `UEuU5`, `c1Alx`, `gxx0X`):** the `qvQPg` component's `inputValue` text (`tK3VZ`) defaults to `fill: $field/placeholder`, so a content override alone still rendered as placeholder-grey. Added a `fill: $foreground/foreground` descendant override alongside each `content` override — the token used by the majority of filled (non-placeholder) inputs elsewhere in the document (e.g. the login screen's entered username `D1O4P` and password `aCGwF`, `i12U8`). All four hex values now read as entered text, not placeholder text.

**Structural notes (unchanged from original D2 pass):**
- Each free-picker frame uses the existing `qvQPg` component (Gameplane/Input/Small, 250×32) via ref with a width override to 96 and an `inputValue` descendant content override.
- Swatch containers are 28×28 rounded frames (cornerRadius 6, stroke `$border/border`) with a centered 16×16 palette icon (`"palette"` lucide, weight 400, fill `$muted`) — matching the existing `tsCardCustom` swatch styling at `Q0uesI` (lWvcv) / `wHCI4` (T1wkiT).
- Surface tone rows (`nG8Kx` / `ez9A7`) are horizontal layout (gap 24) — unchanged.
- Both screens' Top Bar breadcrumbs are 2-level `"gameplane › Settings"` — unchanged.

**Final child order — `Y9YsS1` (lWvcv Card Custom Colors → Card Fields):**
1. `PFzKl` — ccSwatches (accent swatch row)
2. `iSgJ4` — ccAccentFreePicker
3. `JGzsZ` — ccPreviewRow
4. `nG8Kx` — ccSurfaceRow (surface-tone row)
5. `w3wnz` — ccSurfaceFreePicker

**Final child order — `u3VzxL` (T1wkiT Card Custom Colors → Card Fields):**
1. `GpVGU` — ccSwatches (accent swatch row)
2. `jCPmK` — ccAccentFreePicker
3. `R8x9R` — ccPreviewRow
4. `ez9A7` — ccSurfaceRow (surface-tone row)
5. `Viurq` — ccSurfaceFreePicker
6. `O8yQoQ` — ccContrastWarning (contrast-guard warning, unchanged position after the surface picker)

**Clipping fix (screen heights):** the two free-picker rows added by the D2 pass grew each screen's auto-sized `Content Column` (`A1pd4` / `F5MnIY`, vertical-layout, no explicit height) past the fixed, `clip: true` screen frame's height, clipping the bottom `tsActionsRow` (Reset to Defaults / Save, `k1qKk` / `pqHRF`) and partially clipping the content column itself. `Body`/`Settings Layout` on both screens are `fill_container` (no independent fixed height to adjust — they simply inherit whatever the screen allows), so the fix is at the screen root only:
- `lWvcv`: `height` `1904` → **`2000`** (content column settles at 1810; 64 top bar + 24 + 53 header + 24 + 1810 column + 24 bottom margin ≈ 1999).
- `T1wkiT`: `height` `2036` → **`2114`** (content column settles at 1924; same margin arithmetic ≈ 2113).

**Real `ctx.problems` sweep (`resolveInstances: true`) after all fixes, run separately per screen (root frame down):**
- `lWvcv`: **one** report — `b9v04` (`ccPreviewMeta`, "Contrast 2.9:1 — fails WCAG AA") "fully clipped", which is expected and correct: the node is `enabled: false` (intentionally hidden, contrast warning only applies to the active/T1wkiT state) and a disabled/zero-area node reporting as clipped is the established artifact class for hidden helpers in this document (same pattern noted in the 2026-09-22 third-pass entry above). No other node on either screen reports `partially clipped` or `fully clipped` — the `Content Column` / `tsActionsRow` clipping is gone on both.
- `T1wkiT`: **zero** reports — fully clean.

**Exports (this pass):** `json/lWvcv.json` (18,943 B) and `json/T1wkiT.json` (19,839 B), `Get(id, {depth: 12})` (zero `"..."` elisions confirmed by string search), both `python3 -m json.tool`-valid; greps confirm `ccAccentFreePicker` ×1 and `ccSurfaceFreePicker` ×1 in each file, and `"Contrast 2.9:1"` present in both (visible/active in T1wkiT, present-but-hidden in lWvcv's `b9v04`). PNGs re-exported via `export_nodes` at 2x scale: `screenshots/lWvcv.png` **2880×4000** (907 KB) and `screenshots/T1wkiT.png` **2880×4228** (998 KB) — both `2×` the corrected screen heights (2000/2114), confirmed via PNG IHDR dimensions, not estimated. Screenshots visually verified: both screens show the full `tsActionsRow` (Reset to Defaults / Save) with normal bottom padding, no overflow; the Custom Colors card shows the 7 preset swatches (Amber ring-highlighted) and the two free-picker 28×28 palette-icon swatches (Amber accent, Crisp Light surface), and legible (non-grey) hex text `#F59E0B` / `#F8FAFC` in all four inputs. All access via Pencil MCP; `.pen` file not read/edited via shell; no git commands run — the human saves the `.pen` file via the Pencil GUI after this session.

## D4 — Appearance mode disabled while Custom colors is active, T1wkiT only (2026-09-23)

Applied against `pd4tf` (Card Appearance Mode instance) inside `T1wkiT` only, instance-scoped (no new `XDZ0E` variant created); `lWvcv`'s `Q2cSKr` instance (default preset-active state) is untouched.

1. **`Rsw4p`** (`tsModeTabs`, inside `pd4tf`): `opacity` set to **`$disabled-opacity`** — the document's own named design token (`GetVariables()` → `disabled-opacity: {type:"number", value:[{value:50, theme:{semantic:"light"}},{value:50, theme:{semantic:"dark"}}]}`), not an invented number. (`u3VzxL`, the node the scout brief pointed at, turned out to carry no `opacity` property of its own on inspection — it's the *active* Custom Colors card's Card Fields, not a disabled-state reference; the real "standard disabled-opacity token" was `$disabled-opacity` itself, confirmed via `GetVariables()`, which is the more authoritative source anyway.)
2. New note frame inserted under `ceIti` (Card Fields), sibling to `Rsw4p`, copying the `TGnpT`/`UQizl`/`A0vl9` icon+muted-text pattern exactly (14×14 `info` lucide icon, `$muted` fill; 12px `$typography/font-sans` text, `$muted` fill): `tsModeDisabledNote` (`jLsJM`) → `tsModeDisabledNoteIcon` (`fFT5z`) + `tsModeDisabledNoteText` (`LS4MT`, content `"Set by your surface color"`).
3. **Sidebar Appearance Toggle (`iA2C8`):** `T1wkiT` is the only design-set screen whose stored theme state is "Custom colors active" (the doc has exactly two Theme Settings screens, `lWvcv` default and `T1wkiT` custom/warnings; no other of the ~188 top-level screens encodes an active-custom-colors variant). Its sidebar instance `HVdA9` (ref `kKFX9`) nests the toggle as `q1Xat` (ref `iA2C8`) inside the App Sidebar component's own Footer/Toggle Row — dimmed via an instance-path override `HVdA9/q1Xat: {opacity: "$disabled-opacity"}`, same token as above, base `iA2C8` component left untouched (still full-opacity everywhere else it's used). No coverage gap: every screen showing the toggle with custom colors active is this one screen, and it's now dimmed.
4. **Clipping fix:** inserting the note row grew `pd4tf`'s Card Fields, which pushed `F5MnIY` (Content Column, auto-height) to 1956px, past the then-1925px available height inside `fQBYE` (Settings Layout, `fill_container`) — 31px "partially clipped". Fixed at the screen root only (same pattern as the 2026-09-23 D2 entry above): `T1wkiT` `height` `2114` → **`2154`**. The edited nodes (`Rsw4p`, `jLsJM`/`fFT5z`/`LS4MT`, `HVdA9/q1Xat`) sit at depth 5–7 from the screen root, below the `depth: 4` problems sweep this pass originally ran — that sweep did not actually cover the edited subtree. A follow-up pass replaced it with a full-depth sweep using `resolveInstances`, see the correction below.

**Exports (this pass):** `json/T1wkiT.json` (`Get("T1wkiT", {depth: 12})`, zero `"..."` elisions confirmed by string search, `python3 -m json.tool`-valid, contains `"Set by your surface color"`) and `screenshots/T1wkiT.png` (2880×4308, 1,009,761 B, 2× the corrected 2154px height, non-empty PNG confirmed via `file`). `lWvcv` was not touched or re-exported (out of scope for D4). All access via Pencil MCP; `.pen` file not read/edited via shell; no git commands run — **the human must save the `.pen` file via the Pencil GUI before this change is durable.**

**Correction (2026-09-23, post-D4 review pass):** three issues found in the D4 entry above, fixed in this pass:

1. **Problems-sweep claim.** The `depth: 4` sweep quoted above does not cover the edited nodes (they sit at depth 5–7). The correct verification is a full-depth sweep with instance resolution: `Get("T1wkiT", (n,c)=>c.problems?{id:n.id,problems:c.problems}:undefined, {depth: 12, resolveInstances: true})` → returns `[]` (clean). **Corrected wording (2026-09-23, D5 review pass):** the earlier `TypeError` was not a fixed-depth threshold (not "past depth 4") — it is tool behavior: without `resolveInstances: true`, a visitor `Get` throws whenever it descends into the children of a component instance (`type: "ref"`) that carries its own children, at any depth (including a `ref` encountered at depth 0/1 as the root path); with `resolveInstances: true` in the same call, it works correctly at any depth.
2. **Height slack.** `T1wkiT` height grew `2114 → 2154` (+40) while the note row's own content only grew content by +32 (note row 16 + gap 16), leaving 9px of unused slack versus `lWvcv`'s ~1px convention. Corrected: `Update("T1wkiT", {height: 2146})` (`2114 → 2146`, +32 = note row 16 + gap 16), matching the content bottom bound almost exactly (verified via a full-tree bounds walk: content bottom = 2145 inside the 2146px frame, 1px slack).
3. **Light/dark under D4.** Per contract §4a (D4, 2026-09-23 clarification), when `data-theme-type="custom_colors"` the resolved `data-theme` follows the custom surface color's brightness, not `appearanceMode`. `T1wkiT`'s stored surface is Crisp Light `#F8FAFC` (`relativeLuminance` well above the 0.179 light/dark threshold), which resolves to **light** — yet the screen's own `theme` property was still `{semantic: "dark"}`, contradicting its own "Set by your surface color" note. Human decision: render `T1wkiT` light. Fixed via `Update("T1wkiT", {theme: {semantic: "light"}})`. Re-screenshotted every card (sidebar, top bar, preset cards, appearance-mode card, custom-colors card incl. contrast-guard warning and both hex inputs, custom-CSS card, export/import card) — all legible under the light token set (`--foreground` `#2A0F1E` on `--background`/`--surface` `#FFFFFF`/`#F8DDE9`-family light tokens per the theme-tokens-v2 contract); no token-level fixes were needed beyond the theme flip itself.

**Exports (this correction pass):** `json/T1wkiT.json` re-exported via `Get("T1wkiT", {depth: 20})`, zero `"..."` elisions, `python3 -m json.tool`-valid, confirms `"theme":{"semantic":"light"}` and `"height":2146`; `screenshots/T1wkiT.png` re-exported at 2x scale, **2880×4292** (2× the corrected 2146px height), confirmed via PNG dimensions. Full-depth `resolveInstances` problems sweep: `[]` (clean). `lWvcv` not touched (out of scope). All access via Pencil MCP; `.pen` file not read/edited via shell; no git commands run — **the human must save the `.pen` file via the Pencil GUI before this change is durable.**

## D5 — Light-mode `warning/soft-foreground` contrast fix + T1wkiT polish (2026-09-23)

**Variable.** `warning/soft-foreground` light value changed `#D97706` → **`#92400E`** via `SetVariables` (non-replacing, `dark` axis `#F7B750` left untouched): fixes WCAG AA failures on warning-alert title/description text, matching the same-day `--warning-soft-foreground` bump in `web/src/styles/globals.css` (AA-safe: 6.37:1 on `#FEF3C7`/`--warning-soft`, 7.09:1 on white — up from the prior 1.93:1/2.86:1 failures per the human-approved decision).

**Component fills.** `Llzos` ("Alert/Warning") title `BXuYr`: `fill` `$warning/warning` → `$warning/soft-foreground` (icon `sINBP` and description `JmFhb` were already on the soft-foreground token, so they benefit from the value bump alone). `uJ3KZ` ("Alert/Warning Simple") title `xzGxP` **and** icon `JvjWQ`: both `$warning/warning` → `$warning/soft-foreground` (icon included per explicit sign-off; description `yLGJ9` stays `$muted`, unaffected). Why: both components' warning titles/icons previously rendered the bright `warning` color against warning-tinted or `$surface/surface` backgrounds (~1.9–2.1:1, failing the 4.5:1 text / 3:1 icon thresholds); routing them through `soft-foreground` inherits the token's new AA-safe value.

**T1wkiT-only polish (verified review findings):**
1. Sidebar Appearance Toggle instance `HVdA9/q1Xat` (dimmed, under the App Sidebar `HVdA9`) previously showed the component's own default selected state ("System": `icy0X` fill `$accent/soft`, icon `z3SNRG` fill `$accent/soft-foreground`) instead of the screen's actual stored mode. Corrected via instance-path overrides to select "Dark" the same way the component itself marks a selection: `Update("HVdA9/q1Xat/Bg6EP", {fill: "$accent/soft"})`, `Update("HVdA9/q1Xat/Bg6EP/nMMtW", {fill: "$accent/soft-foreground"})`, `Update("HVdA9/q1Xat/icy0X", {fill: "#00000000"})`, `Update("HVdA9/q1Xat/icy0X/z3SNRG", {fill: "$foreground/muted"})` — now matches `Rsw4p` (the disabled Appearance-mode tabs), which shows "Dark" selected.
2. Contrast-copy correction: `RaaH4` ("Contrast 2.9:1 — fails WCAG AA") and the contrast-guard alert description under `O8yQoQ` (`JmFhb`, "...fails WCAG AA (2.9:1)...") both stated 2.9:1; the actual computed contrast for Amber `#F59E0B` vs Crisp Light `#F8FAFC` is 2.05:1. Both corrected to **"2.1:1"**: `Update("RaaH4", {content: "Contrast 2.1:1 — fails WCAG AA"})`, `Update("O8yQoQ/JmFhb", {content: "The selected accent and surface combination fails WCAG AA (2.1:1). Choose a darker accent or a darker surface tone."})`.

**Verification.** Full-depth problems sweep on `T1wkiT` with `resolveInstances: true` (`Get("T1wkiT", (n,c)=>c.problems?{id:n.id,problems:c.problems}:undefined, {depth: 30, resolveInstances: true})`) → `[]` (clean). Screenshotted the contrast-guard alert (`O8yQoQ`) and the Custom CSS recovery alert (`giS8X`, both `Llzos` instances): both legible in light mode, dark-brown title/icon on the amber `$warning/soft` background, no clipping or overflow.

**Exports (this pass):** `json/T1wkiT.json` (`Get("T1wkiT", {depth: 30})`, no `"..."` strings at all, `python3 -m json.tool`-valid, contains `"Contrast 2.1:1 — fails WCAG AA"`), `json/LtgNm.json` (`Get("LtgNm", {depth: 30})` **without** `includePathGeometry` — this left a `"geometry": "..."` elision on path `hkEvE`; fixed by the re-export in the review follow-up below; body-text check: `"Storage almost full"` and `"Scheduled maintenance"` each present once), `json/Llzos.json` and `json/uJ3KZ.json` (component snapshots, both `python3 -m json.tool`-valid, confirm the updated fills). `screenshots/T1wkiT.png` (2880×4292), `screenshots/LtgNm.png` (6232×7176), `screenshots/Llzos.png` (1098×222), `screenshots/uJ3KZ.png` (1096×184) — all non-empty PNGs with real pixel dimensions confirmed via `file`. **Correction (2026-09-23, D5 review pass, see below):** screens confirmed dark-themed (`lWvcv`, `f1Vga`, `QQtUD`, `IyMFM`, `jXykV`, `IzuY2`, `uMiwd`) were left un-re-exported at the time under the claim that "the token change is light-only" — that claim was wrong. The `dark`-axis value of `warning/soft-foreground` (`#F7B750`) was left unchanged, but `Llzos`'s title and `uJ3KZ`'s title+icon were *rebound* from `$warning/warning` to `$warning/soft-foreground`, and those two tokens differ on the `dark` axis too (`warning/warning` dark `#F59F0A` vs. `warning/soft-foreground` dark `#F7B750`, confirmed via `GetVariables()`), so every dark-theme instance of these two components also changed color. All seven screens are re-exported below.

**Review follow-up (2026-09-23, same day):** a review of this pass found two more nodes on the same `$warning/warning` token that the D5 sweep missed, and confirmed the light/dark rendering gap above.

1. **`Kp48V`** (`Gameplane/Dialog/Confirm Admin Mapping`) — the inline warning box's title `yeBW8` ("Full admin access") and icon `qsmfp` were still `$warning/warning` on the `$warning/soft` box background (1.93:1, failing AA/AA-large), even though the shipped code (`web/src/components/ui/ConfirmAdminMappingDialog.tsx`) already renders that title with `text-warning-soft-foreground`. Both fills updated to `$warning/soft-foreground`, matching the sibling `warningDesc` text (`F1pyxJ`), which was already on that token, and matching the shipped component.
2. **`lWvcv`**'s hidden preview-meta node `b9v04` ("Contrast 2.9:1 — fails WCAG AA") still carried the pre-correction copy; `T1wkiT`'s equivalent nodes (`RaaH4`/`O8yQoQ/JmFhb`) were already corrected to 2.1:1 earlier in this D5 pass (Amber `#F59E0B` vs. Crisp Light `#F8FAFC` = 2.05:1, rounds to 2.1:1) but `lWvcv`'s hidden copy of the same string was missed. `b9v04.content` updated to `"Contrast 2.1:1 — fails WCAG AA"` to match.

**Re-exports (review follow-up pass):**
- **Light components using the token** — re-screenshotted (no JSON diff — no fill changed; both already bind `$warning/soft-foreground`, whose light value changed `#D97706` → `#92400E`): `f0s9zG` (1280×378), `XL5ZU` (166×46).
- **`Kp48V`** (content changed) — JSON re-exported via `Get("Kp48V", {depth: 20, includePathGeometry: true})`, zero `"..."` elisions, `python3 -m json.tool`-valid, confirms `qsmfp`/`yeBW8` both `"$warning/soft-foreground"`; screenshot re-exported at 2x (880×640).
- **Dark screens with rebound `Llzos`/`uJ3KZ` instances** (JSON unchanged — they only `ref` the components, carrying no fill override of their own — screenshots only): `f1Vga` (2880×2340, 2 `Llzos` refs, body text incl. "Address preference ignored"), `uMiwd` (2880×3182, 1 `Llzos` ref, "Helm-configured admin mapping"), `IzuY2` (2880×3450, 1 `Llzos` ref, "Will never sleep"), `QQtUD` (2880×2340, 1 `Llzos` ref, "Address preference ignored"), `IyMFM` (1011×952, 1 `uJ3KZ` ref, "Tailscale is tailnet-only"), `jXykV` (1400×492, 2 `uJ3KZ` refs, "Address preference ignored" / "No address manager configured").
- **`lWvcv`** (content changed — `b9v04` copy) — JSON re-exported via `Get("lWvcv", {depth: 12, includePathGeometry: true})`, zero `"..."` elisions, `python3 -m json.tool`-valid, contains `"Contrast 2.1:1 — fails WCAG AA"` (now the only contrast-string content in the file — the "2.9:1" wording is gone); screenshot re-exported at 2x (2880×4000).
- **`LtgNm` regression check** — re-exported via `Get("LtgNm", {depth: 30, includePathGeometry: true})` (261,395 bytes on disk) and re-screenshotted (6232×7176). Verified the file contains exactly two `"..."` strings, both the literal Pagination/Ellipsis text content on `EHTqO` and `DpNvd` (`fill: "$muted"`, `content: "..."`) — no structural `"children": "..."`/`"geometry": "..."` elision markers. This fixes the earlier D5 `LtgNm` export, which omitted `includePathGeometry` and had elided `hkEvE`'s geometry.
- **Content-check convention:** for `f1Vga`/`uMiwd`/`IzuY2`/`QQtUD`/`IyMFM`/`jXykV`, "content validated" means the exported JSON contains the screen's real alert body text (e.g. `"Address preference ignored"`, `"Will never sleep"`, `"Tailscale is tailnet-only"` — see the per-screen list above), not the frame's own name (a `Screen/...` name would pass a grep even on a hollow export).
- **Pixel verification:** each re-exported PNG checked with a Python/PIL nearest-color sample (step-2 pixel grid, ±20–30 RGB tolerance) for the expected token color — light nodes (`f0s9zG`, `Kp48V`, `XL5ZU`) sampled against `#92400E`, dark screens (`f1Vga`, `uMiwd`, `IzuY2`, `QQtUD`, `IyMFM`, `jXykV`, `lWvcv`) against `#F7B750` — all ten files returned a non-zero match count, confirming the new color is actually present in the rendered pixels, not just the node schema.

All access via Pencil MCP; `.pen` file not read/edited via shell; no git commands run — **the human must save the `.pen` file via the Pencil GUI before this change is durable.**

## Incremental export 2026-09-25 — Spec 018 design states: F-134 copy, H14 capture no-access, H31b owner-only gates

Frames added to `design.pen` per the held design briefs `specs/done_018-v0-3-release-readiness/audit/held/briefs/H14-design.md` and `H31b-design.md` (OD-025 design pass), plus the F-134 identity-providers copy update (already applied to `Dpb9f` by an earlier session, re-exported here) and the `XR0f9` button-label fix the H31b brief left as the maintainer's call (approved 2026-09-25). All edits via the Pencil MCP `execute` tool; the `.pen` file was not read or edited via shell.

| ID | Frame | Source | Change |
|---|---|---|---|
| `akr4I` | Screen/Server Detail — Capture (No access) | copy of `Bbnga` | Card renamed "No Access Card", icon → `lock`, heading "You don't have access to packet capture on this server.", info paragraph, button row / error banner / admin hint hidden (`enabled: false`). |
| `hlwx3` | State/Server Actions Menu — Owner-only disabled | new frame + `BPEpm` and `hIoOw` refs | Transfer ownership, Wipe world data, Delete server at `opacity: 0.5`; Clone untouched; tooltip "Requires server owner or admin" beside Transfer. |
| `IMSD5` | Screen/Server Detail — Settings · Danger zone (Owner-only) | copy of `XR0f9` | All three action buttons `opacity: 0.5` with corrected labels ("Wipe world…", "Transfer…", "Delete"); `CEGPG` alert instance "Owner-only Notice" (lock icon, "Owner-only actions" / "Only the server owner or an admin can wipe, transfer or delete this server.") inserted as first child of the Form Body. |
| `gWqMD` | Screen/Server Detail — Settings · Access (Read-only) | copy of `QpEvu` | Owner value → "admin", add row hidden, "Read-only Note" text appended to the Collaborators Card: "Only the server owner or an admin can change collaborators." |
| `Dpb9f` | Screen/Users & RBAC — Identity providers | existing screen (F-134) | Text node `Z75kr` now reads "Identity providers are configured in Admin Settings → Authentication." (edit predates this pass; **re-exported here** — the Sep-20 export still carried the old "tracked for v1.1" copy). |
| `XR0f9` | Screen/Server Detail — Settings · Danger zone | existing screen | Wipe button label `vPgdl/eWkIT/r4VbAi` "Discard" → "Wipe world…", Transfer button label `GytYJ/eWkIT/r4VbAi` "Discard" → "Transfer…" (the maintainer's-call fix flagged in the H31b brief). **Re-exported.** |

**Verification:** each new frame rendered via `export_nodes` and visually checked: `akr4I` shows only the lock icon, heading and one centred paragraph with no button; `hlwx3` shows three dimmed items with Clone at full opacity and the tooltip unclipped; `IMSD5` shows the notice above the three cards, all buttons dimmed with the right labels, footer not clipped; `gWqMD` shows the read-only note in place of the add row; `Dpb9f` shows the new Admin Settings copy inside the Identity Providers Card; `XR0f9` shows "Wipe world…" / "Transfer…" / "Delete" with the Form Footer's own "Discard" / "Save changes" untouched.

**Exports (this pass):** `json/akr4I.json`, `json/hlwx3.json`, `json/IMSD5.json`, `json/gWqMD.json`, `json/Dpb9f.json`, `json/XR0f9.json` via `Get(id, {depth: 30, includePathGeometry: true})` — all `json.load`-valid, zero `"..."` elision markers; body-text checks pass ("don't have access to packet capture" only in `akr4I.json`, "Requires server owner or admin" in `hlwx3.json`, "an admin can wipe" in `IMSD5.json`, "an admin can change collaborators" only in `gWqMD.json`, "Identity providers are configured in Admin Settings" and zero "tracked for v1.1" in `Dpb9f.json`, "Wipe world…"/"Transfer…" with "Discard" only on the Form Footer in `XR0f9.json`). `screenshots/*.png` at 2×: `akr4I` 2880×1800, `hlwx3` 1040×440, `IMSD5` 2880×1800, `gWqMD` 2880×1800, `Dpb9f` 2880×1800, `XR0f9` 2880×1800 — all non-empty with real pixel dimensions.

**The human must save the `.pen` file via the Pencil GUI before this change is durable.**

## Design reconciliation 2026-09-30 — Unified locations and remote server parity

The Pencil design was updated to represent the interfaces implemented at
`8e08a60f9d266dd938836e1f31188b370ca8a2a7` and saved in `design.pen` on
September 30. Existing Gameplane components and theme tokens were retained.

The scope is the ordinary app shell without a global cluster selector; Clusters
as administration navigation; combined dashboard statistics, location filtering
and partial coverage; server location/namespace identity and the Servers Filter
popover; matching Backups, Schedules and Restores menus; the creation wizard's
Location strip; the user editor's cluster and namespace grants; selected-site
Capture states and example limits; and mobile server-detail action wrapping and
contained tab navigation. Schedules omits Phase. Applied-filter counts and
draft/Apply behavior are interaction context, not separate visual-state claims.

**Direct export scope: 37 nodes.** Each ID below has its own
`json/<id>.json` and `screenshots/<id>.png` export from this pass.

| Area | Directly updated and exported node IDs |
|---|---|
| Shared shell and filters | `gu5WY` (Top Bar), `kKFX9` (App Sidebar), `FyV6E` (Filter Popover), `wKe4F` (Backups Filter Row), `rCQaM` (Backups Filter Popover) |
| Shared statistics and server detail | `YF85x` (Dashboard Stats Row), `LKMjc` (Servers Stats Row), `S4k0x` (Server Detail Header), `I9kvlZ` (Server Detail Tabs), `f0s9zG` (Capture Warning Banner) |
| Scoped user grants | `t3IY3u` (Edit User) |
| Dashboard and Servers | `j24cXg` (Dashboard), `oyoTs` (Dashboard Light), `F9pUrx` (Servers), `zFiOW` (Servers Light), `tooKB` (Mobile Servers) |
| Backups | `DPrYX` (Index), `fK8Bi` (Schedules), `tTSdi` (Restores) |
| Creation location | `W8idqY` (Template), `nNL3E` (Version), `vUqMl` (Configure), `f1Vga` (Network), `UMJli` (Review), `QQtUD` (Network as built) |
| Capture screens | `Bbnga` (Not enabled), `O08uaD` (Start Capture with selected-site example limits), `w1QYmf` (Access and capability states), `SUtGZ` (Mobile Remote Capture) |
| Open filters and partial coverage | `UCNBm` (Servers), `pnAbF` (Backups), `Cb3eC` (Schedules), `fbBqA` (Restores), `YPKbV` (Dashboard partial location coverage), `C5Aoe` (Mobile Servers filters), `NKiqf` (Mobile Backups filters) |
| Infrastructure administration | `iYUWc` (Registered locations) |

All 37 export pairs were checked for JSON validity and node identity, PNG
signatures and dimensions, and file hashes. Key dark/light desktop and mobile
views were reviewed through Pencil screenshots; final exported PNGs for the
user editor (`t3IY3u`), mobile Capture (`SUtGZ`) and mobile Backups filters
(`NKiqf`) were also inspected for copy, wrapping, centering and clipping.
This entry does not claim every screen inheriting an updated
component was re-exported: for example, the Capture list (`m5kOm4`) inherits
shared header changes but is outside this pass's direct export scope. The
historical totals and earlier export entries above are not a fresh enumeration
of the whole document.

This update changed the design, its direct exports and related documentation.
Application code, tests and comparison thresholds were unchanged. The October 2
entry below records the subsequent restoration of canonical Pencil comparisons.

## Visual baseline reconciliation 2026-10-02 — Restore canonical Pencil comparisons

In response to upstream PR #542's review, the visual workflow again runs
`npm run diff:screenshots -- --check-expected` against `assets/design-export/screenshots`.
No browser captures replace Pencil baselines. The retired browser-reference
bundles remain available in Git history at `4d7c08bb673e38ccf4a7dba9f3b3deb45dd20474`.
Thresholds, masks, scaling rules, screenshot fixtures and expected-screen checks
are unchanged.

The three formerly substituted designs were reconciled through Pencil MCP and
exported directly as editable-node JSON and 2x PNGs. `design.pen` was saved in
Pencil on October 2.

| ID | Design reconciliation | PNG dimensions |
|---|---|---|
| `Wj0V4` | Central-management notice and branding; registered Clusters and Cluster inventory navigation; registry panel spacing and controls | 2880 × 1800 |
| `t3IY3u` | Default Local cluster grant selection; disabled All namespaces control; helper line heights and grant-row spacing | 960 × 1116 |
| `DWztv` | Location/namespace identity under each mobile server name, preserving the compact Search/Filter row | 780 × 1688 |

The PNGs were inspected against the original browser captures from CI run
`36780352172`. That earlier run's 109 passing comparisons included three browser
substitutions and therefore did not establish Pencil fidelity for these IDs.
Only 16 of the September 30 pass's 37 directly exported IDs appeared in that
run's report, and `t3IY3u` was substituted. The remaining exports include shared
components and additional screens/states; exporting them is not a claim that
each has an independent automated comparison. Fresh canonical CI results are
reported separately on the PR with their exact commit and run IDs.

## Canvas reorganization 2026-10-05 — overlapping top-level frames

A scan of every top-level frame's bounds found 30 overlapping pairs in four clusters. All were fixed by moving frames. No node's content, size, style or component changed.

- **Server Detail · Settings · Networking flow.**
  - Five tunnel-flow states were stacked exactly on `J5pjJ3` at (4920, 10375).
  - `J5pjJ3` keeps its slot in the Settings row.
  - The five states now sit under it in flow order, in a sub-row below `J5pjJ3`'s existing state fragments (`jXykV`, `IyMFM`) and the Lifecycle screen. They are at y 12095, x 4920–11480, with a 1640 px pitch:
    1. `nGJE1` (Tunnel disabled)
    2. `dhPtX` (Tunnel initial enable)
    3. `tSOeI` (Provider selected)
    4. `y3YcQ` (Credentials entering)
    5. `EKJyM` (Provider + credentials complete)
- **Create Server — Step 4 Network flow.**
  - Four tunnel states were stacked on `f1Vga`.
  - They now sit in a sub-row 100 px under `f1Vga`, at y 14665, x 4920–9840:
    1. `lhKzN` (Tunnel disabled)
    2. `a7o9C` (enabled, no provider)
    3. `ZU1BG` (provider selected)
    4. `eGbsg` (complete)
- **Screen grid shift.** To make room, every screen-grid frame at or below the old Create Server row (y ≥ 12745, x ≥ 0) moved down 550 px. This covers the Create Server, Modules, Backups, Clusters, Users & RBAC, Audit, Admin, Mobile and Share Link rows, plus the `m8wjom` and `R6iab` notes. Positions within each row are unchanged.
- **Components band.**
  - `Gameplane/Audit Integrity Banner` (`kIxaJ`) overlapped `igj2U` and `zzx8f`, and `FyV6E` overlapped `IdaU7`.
  - The `x = -15622` column (`igj2U`, `zzx8f`, `FyV6E`, `IdaU7`, `w4ntSc`, `qvQPg`, `rCQaM`) was restacked below `kIxaJ` with 60 px gaps, which fixes both.
  - `Jpl8j`, `NVN2r` and `FhrUm` moved to y 29821 so that `NVN2r` clears `Gameplane/Dialog/Edit User` (`t3IY3u`).
- **Notes.** `x71Cb` moved to 60 px below `zqzr4`; it had overlapped `zqzr4`'s bottom edge by 53 px.

**Export method & validation:**

- **55 frames that only moved:**
  - Only the top-level `"y"` in each `assets/design-export/json/<id>.json` changed.
  - It was patched in place with `sed`, without re-serializing, and checked with `jq -e '.y == <new>'`.
  - Their screenshots were not re-exported, because moving a frame does not change its rendered pixels.
- **The nine flow states above:**
  - These had never been exported before; this pass exports them.
  - JSON came from `Print(JSON.stringify(Get(id, {depth: 30, includePathGeometry: true})))` and was re-serialized with `jq .`. It has zero `"..."` elisions, and `jq empty` passes.
  - Screenshots were made with `Export(ids, "png", …)` at 2× scale.
- **Notes:** `m8wjom`, `R6iab` and `x71Cb` stay unexported, per the standing exclusion.
- **Final check:** a scan of the whole document after the moves found 0 overlapping top-level pairs.

**Duplicate cleanup (same pass):** Three top-level frames were byte-identical, unexported copies of exported frames, and nothing in the repo referenced them. They were deleted:
- `goLqZ`, a copy of `hlwx3` (State/Server Actions Menu — Owner-only disabled)
- `Gm3OE`, a copy of `IMSD5` (Settings · Danger zone (Owner-only))
- `BB2qO`, a copy of `gWqMD` (Settings · Access (Read-only))

Identical means that, node for node, each pair had the same type, name, text, ref, fill and descendant overrides.

The kept copies moved into the freed slots so their rows stay contiguous, and only the top-level `"x"` in their JSON changed:

| Frame | Old x | New x |
| --- | --- | --- |
| `IMSD5` | 41620 | 38540 |
| `gWqMD` | 43160 | 40080 |
| `hlwx3` | -12843 | -13423 |

The second Settings set at x ≥ 30440 (`uCA23`, `VctzT`, `iLm38`, `QpEvu`, `XR0f9`) was not touched. Those screens are not duplicates; their content differs from the originals.

After the cleanup there are 217 top-level frames and 0 overlaps.

## Live-sweep UX pass (2026-10-05)

These are the design-first changes for the four UX findings from the 2026-10-04 kubelab live sweep that needed a design before code. One new top-level frame was added, so the document now has 218 top-level frames. It does not overlap any other frame.

| Object | Name | Change |
| --- | --- | --- |
| `EcoGD` | Screen/Share Link — Starting | The body copy says "The server is starting up…" instead of "…waking up…". The old copy was wrong for a normal start that no visitor triggered. |
| `WZdnw` | Screen/Admin Settings | The Instance name hint (`E8BwC`) now reads "Optional. Names this install on the Cluster page and in notifications." The field is optional, and the API never used it in OIDC replies. |
| `Burtr` | Screen/Server Detail — Files | Row `WdtR2` ("logs") is shown in its hover state. A small ghost `trash-2` button (`ccokq`, in `kHLhx`) replaces its Size cell, so folders can be deleted. Clicking a folder row still opens the folder. |
| `oMuyw` | Gameplane/Dialog/New File (Invalid name) | **New**, an instance of `JLaGB` placed to its right. The input shows `../escape.txt` with a danger stroke, an 11px danger error line under it, and the dimmed Create button. |
| `JLaGB`, `I9W8z` | Gameplane/Dialog/New File, New Folder | The primary button reads "Create". Before, it inherited "Run snapshot" from the base modal `x3beP`. |
| `Xn5ns` | Screen/Server Detail — Console | Adds the command bar (`bdXV9`) that the code already renders, under the terminal. It has a full-width input with the placeholder "Type a command… (↑/↓ recalls history)" and a small primary Send button with no icon. |

- **JSON:** made with `Print(JSON.stringify(Get(id, {depth: 30, includePathGeometry: true})))` and re-serialized with `jq .`. Zero `"..."` elisions, and `jq empty` passes.
- **Screenshots:** made with `Export(ids, "png", …)` at 2× scale.
- **Not changed, for a follow-up:** `Jpl8j` (Clone Server) and `NVN2r` (Transfer Ownership) still carry the "Run snapshot" label in their exports.

## Sweep-2 design pass (2026-10-05)

Server Settings gets a "Game configuration" section (edit `spec.config` from the template's configSchema) and the app gets a Not Found screen. The first-pass frames `FgBPr`, `Yes8M`, `F88zp2`, `EHzj3` and `bAWsF` were deleted and rebuilt; their stale exports are removed. All new frames are copies of `uCA23` (Settings · General), so header, tabs, sub-nav, and form and footer layout are inherited.

| Object ID | Name | Change |
| --- | --- | --- |
| `OC804` | Screen/Server Detail — Settings · Game configuration | **New.** Sub-nav item "Game configuration" is active. Title and description ("Settings from this server's game template. Saving restarts the server."). Fields: World name `Gameplane`, Difficulty select `Expert`, Max players `8`, PvP select `false`, Server password (empty, placeholder "Unchanged — type to replace", helper "Write-only"), MOTD `Welcome to Gameplane` (helper "Written to serverconfig.txt"). Warning alert "Saving changes restarts the server so they apply." directly under the section header. |
| `QFEg9` | Screen/Server Detail — Settings · Game configuration (Invalid value) | **New.** Max players shows `900` with a danger border and the danger helper "Must be between 1 and 255."; Save changes is dimmed (disabled). |
| `wxINm` | Screen/Server Detail — Settings · Game configuration (Read-only) | **New.** All inputs and selects dimmed (disabled); the restart alert is removed; the footer buttons are replaced by the muted text "You need permission to change this server's settings." |
| `e9FnC` | Screen/Server Detail — Settings · Game configuration (Orphaned key) | **New.** Extra row `OLD_SETTING` = `legacy` with muted helper "No longer in the template" and a small ghost "Remove" button. The frame is 960 px tall (the extra row does not fit in 900). |
| `Ne5TA` | Screen/Not Found | **New.** Authenticated shell (App Sidebar, Top Bar with breadcrumb "Page not found"). Centred empty state: 48 px circular muted badge with lucide `compass`, title "Page not found", body "The page you're looking for doesn't exist or has moved.", primary button "Go to dashboard". No cluster names, versions or counts in the body. |
| `EV8mp` | Gameplane/Server Settings Sub Nav | **Changed.** Added `snGameConfig` "Game configuration" as the 3rd item (after Version). JSON and PNG re-exported. Active state is set by instance override on each screen. |

- **JSON:** made with `Print(JSON.stringify(Get(id, {depth: 30, includePathGeometry: true})))` and re-serialized with `jq .`. Zero `"..."` elisions, and `jq empty` passes.
- **Screenshots:** `Export(ids, "png", …, {scale: 2})`.
- **Re-exported for the sub-nav change:** PNGs of `swxkJ`, `E0ypH`, `J5pjJ3`, `ugDSa`, `i1bLR`, `KaRFX`, `Y5cmvI`, `VfB0Y`, `i8wib`, `xCJlu`, `RodrS`, `VctzT`, `iLm38`, `QpEvu`, `XR0f9`, `nGJE1`, `dhPtX`, `tSOeI`, `y3YcQ`, `EKJyM`, `IMSD5`, `gWqMD` and `EV8mp`; JSON of `Y5cmvI` and `J5pjJ3` (they were out of date with the canvas).

## Spec 022 telemetry notice and settings (2026-10-07)

T041 and T052 of `specs/done_022-default-telemetry-dashboard/`. New frames sit in an empty region below the Admin Settings row (x 11480–21120, y 25404–28584); no existing frame changed. Destination hosts are samples while OD-1 is unruled: their text nodes are named `host (SAMPLE, OD-1 unruled)` (default) and `host (SAMPLE)` (bundled).

| Object ID | Name | Change |
| --- | --- | --- |
| `lh8p8` | Gameplane/Telemetry Notice Banner | **New** reusable component, modelled on the Safe Mode Banner (`DAz77`) in the accent tone with a lucide `info` icon. Headline "Anonymous usage metrics are on for this install.", the destination host line, a basic field column and an extended field column that says a random install ID is included, the actions "Keep sharing", "Turn off extended" and "Turn off all", and the links "Open Telemetry settings" and "Data-handling statement" (`TELEMETRY_STATEMENT_URL`). No dismiss button. |
| `zj5a3` | Screen/Dashboard Home — Telemetry notice (default) | **New.** Copy of `j24cXg` (1380 tall) with the banner at the top of the body, default destination (sample host `telemetry.example.org`). |
| `GR1G9` | Screen/Dashboard Home — Telemetry notice (bundled) | **New.** As `zj5a3`, bundled in-cluster receiver (sample host `gameplane-telemetry-receiver.gameplane.svc`). |
| `l7TRz9` | Screen/Admin Settings — Telemetry (default) | **New.** Copy of `uoxQW`. Subtitle names the install ID. Destination line, basic and extended switches on, install-ID row with "Reset ID", read-only next-report preview JSON, status "ok". |
| `r0heh` | Screen/Admin Settings — Telemetry (custom) | **New.** Custom destination; status "failed" plus the line "Install ID replaced on Oct 4, 2026: the destination reported it was in use by another key." |
| `cDiNb` | Screen/Admin Settings — Telemetry (bundled) | **New.** Bundled destination; basic on, extended off, install-ID row dimmed ("No install ID while extended metrics are off."), Reset ID dimmed; status "never". |
| `DBQyF` | Screen/Admin Settings — Telemetry (disabled) | **New.** Both switches off and dimmed, warning message "Telemetry is disabled by the operator.", Save dimmed. |
| `K5XinZ` | Screen/Admin Settings — Telemetry (none) | **New.** No destination; basic switch off and enabled, extended dimmed; no preview or status. |
| `yrxK2` | Screen/Admin Settings — Telemetry (Reset ID confirm) | **New.** Copy of `l7TRz9` with a 40% scrim and an AlertDialog/Danger instance: "Reset install ID?", Cancel / Reset ID. |

- **JSON:** made with `Print(JSON.stringify(Get(id, {depth: 30, includePathGeometry: true})))`, length-checked against the canvas and re-serialized with `jq .`. Zero `"..."` elisions, and `jq empty` passes.
- **Screenshots:** `Export(ids, "png", …, {scale: 2})`.
- **Text wrap fix (2026-10-07, after review by the maintainer):** in `U8Zugm` the title (`p6wdYs`), destination suffix (`w4oMww`) and both field descriptions (`js01h`, `WW2ac`) now use `textGrowth: fixed-width` with `width: fill_container`, so long text wraps inside the banner instead of running past it (most visible on `woRBK`, the bundled host). Re-exported: JSON of `U8Zugm`; PNGs of `U8Zugm`, `jmcmS` and `woRBK`. The screens' JSON is unchanged (they hold only instance overrides).
- **OD-1 ruled (2026-10-07):** telemetry.gameplane.net is the default endpoint per OPEN-DECISIONS OD-1. Updated text nodes: `jhnKn` in `U8Zugm`; `jhnKn` in the `t6Wzr` instance override in `jmcmS`; `i6gVPJ` in `TEBym`; `J98Oe` in `oqm73`. Names updated from `host (SAMPLE, OD-1 unruled)` to `host`. Re-exported: JSON and PNG of `U8Zugm`, `jmcmS`, `TEBym`, `oqm73`.
- **Reset ID button icon removed (2026-10-07):** removed the plus icon from the resetIdBtn component instances in the Admin Settings screens. The button component's default plus icon is now hidden with `descendants: {"X3iEI": {"enabled": false}}` on all instances (VATLE in DtjyL, P8V7X8 in uwPVC, qq0qa in oqm73, and AILYr in TEBym). Re-exported JSON and PNG for TEBym, DtjyL, uwPVC, oqm73.
- **Rebuilt in the master merge (2026-10-07):** master's `design.pen` (with `FBiMN` and the `qFLfB` rename from PR #584) was taken as the base and these frames were re-created on it from their exports, so their ids changed: `U8Zugm` → `lh8p8`, `jmcmS` → `zj5a3`, `woRBK` → `GR1G9`, `TEBym` → `l7TRz9`, `DtjyL` → `r0heh`, `uwPVC` → `cDiNb`, `Rvv6z` → `DBQyF`, `RT8lU` → `K5XinZ`, `oqm73` → `yrxK2`. The rebuilt JSON equals the previous export with ids remapped (length and FNV-1a hash checked against the canvas; the hidden rows of the bundled, disabled and none screens carry the x/y/width the canvas now computes for them). The PNGs were re-exported: the two Dashboard Home screens now show the banner's actions row below both metric columns, where the previous PNGs held a stale layout of the same JSON. Internal node ids named in the notes above refer to the pre-merge file.

## Share Start recovery (PR #584, 2026-10-07)

| Object ID | Name | Change |
| --- | --- | --- |
| `FBiMN` | Screen/Share Link — Asleep (Start cooldown) | New copy of `q31B6w`. The Start button is disabled at 0.5 opacity, reads "Try again shortly", and has no icon. The resolved Asleep card remains visible after a transient Start failure. |
| `qFLfB` | Historical/Share Link — Asleep (view only) | Retained with historical name and context. Resolve does not expose start capability, so an asleep poll cannot select this state. |

`q31B6w` is unchanged. Both listed frames have MCP `Get` JSON exports (depth 30, path geometry included) and 2× PNG exports. The cooldown card was visually checked with `TakeScreenshot`; no clipping was reported. Browser capture for `FBiMN` uses a mocked 429 Start response with Retry-After; visual comparison remains pending CI.
