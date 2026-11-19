# Spec 034 dashboard: existing web tests the new UI breaks (sign-off needed)

Checked on `feat/034-sso-group-role-rules` HEAD `ee8f94ac` against the change in
`/tmp/claude-0/-home-user/b2229dd2-b1a5-5d80-8c58-0b04817139c5/scratchpad/dashboard-brief.md` (patches `dashboard-{rules,users}.patch`).
Found by grepping every `web/src/**/*.test.ts(x)` and `web/e2e/**` file for each removed label, id, placeholder, group name and endpoint
(Admin groups, Operator groups, Viewer groups, Default role, (no mapping), Deny sign-in, Role mapping, Role mapping overrides, `{role} role mapping`,
Add IdP group name…, Add group, Save role mappings, Reset to Helm default, role-mappings DELETE, No groups mapped, No OIDC role mappings yet,
Helm-seeded role mappings, Full admin access / AdminGroupsInlineWarning, The group(s) on the Admin row, roleMappings, helmOverride, defaultRole,
oidcHelmProvider, Not configured, From Helm values, Overridden in dashboard), then reading each hit and tracing it through the new code. Tests were
not run (repo rule 8); "fails" below is from reading the code.

Totals: **18 Vitest tests** (15 in `AdminSettings.test.tsx`, 3 in `AdminSettings_sections.test.tsx`) and **10 Playwright `@screenshots` tests** in
`web/e2e/screenshots/slice4.spec.ts` (run by `.github/workflows/visual-diff.yaml` and `screenshot-refresh.yaml`, not by the main CI Vitest job).
**28 existing tests in all.** No `Users*.test.tsx`, `EditUserDialog.test.tsx`, `ConfirmAdminMappingDialog.test.tsx`, `ProvenanceBadge`/`RemovableGroupChip`
test, `Standalone.test.tsx` or live Playwright spec breaks: their fixtures carry no `roleSource`, so no badge or menu item appears, and the new
`roleFooter` prop is optional.

Checked and unaffected (they pass unchanged): `AdminSettings.test.tsx` "T026: HelmOIDCProviderCard updates when config data refetches" (733),
"keeps a reset role mapping reset through the next section save" (1468: Reset still removes the override from the draft, "Overridden in dashboard" and
the Reset button disappear, and the next "Save changes" body has no `helmOverride`; it no longer calls the DELETE endpoint, which the test does not
assert), and in `AdminSettings_sections.test.tsx` "adds an identity provider…" (83: no rules means no `roleRules`/`defaultRole`, so the exact object
still matches) and "surfaces a backend rejection when saving the auth section" (285). Playwright `R65Xyx` still finds "Overridden in dashboard"
(the `oidc` variant has an active override).

Why most of these change: the design (UdnBT, mrF6f) replaces the three per-role group fields with an ordered rule list, renames "Default role" to
"Fallback role" with "Refuse sign-in", and replaces the per-role override card with one "Role rule override" card whose reset removes the whole
override (OD-3). FR-005 makes every save write the rule form, so a stored old-form provider comes back as `roleRules`.

Shared helpers used in the "after" code below (each rewritten test may inline them):

```ts
// Open the add-provider form's rule editor.
await userEvent.click(screen.getByRole("button", { name: "Add rule" }));
await userEvent.type(screen.getByRole("textbox", { name: "Rule 1 group" }), "admin-group");
await userEvent.click(screen.getByRole("button", { name: /Rule 1 role/ }));
await userEvent.click(await screen.findByRole("option", { name: "admin" }));
```

---

## A. `web/src/routes/AdminSettings.test.tsx`

### S-1. "T026: renders HelmOIDCProviderCard when oidcHelmProvider data is present" (line 617), assertion at 657

- Before: `const defaultRoleLabel = within(helmCard!).getByText("Default role");`
- After: `const defaultRoleLabel = within(helmCard!).getByText("Fallback role");` (lines 658-659 unchanged: the row's `div` still holds the `viewer` pill)
- Why: UdnBT `roleLabel` text is "Fallback role" (the fallback can now be any role or "Refuse sign-in", OD-7).

### S-2. "T026: shows FR-012 empty state when oidcHelmProvider has no role mappings" (line 666), assertions 690-696

- Before:
  ```ts
  expect(await screen.findByText("No OIDC role mappings yet")).toBeInTheDocument();
  expect(screen.getByText(/Two ways to change that/)).toBeInTheDocument();
  expect(screen.getByText(/add mappings in Role mapping overrides below/)).toBeInTheDocument();
  ```
- After:
  ```ts
  const heading = await screen.findByText("Helm-seeded OIDC provider");
  const helmCard = heading.closest<HTMLElement>('[data-slot="card"]')!;
  expect(within(helmCard).getByText("No rules. SSO users get viewer and their role is never changed at sign-in.")).toBeInTheDocument();
  ```
- Why: the old info block and its text (which pointed at the removed per-role card and `api.oidc.roleMappings.*`) are gone; the Helm card shows the
  rule list's empty state from mrF6f. Scoped to the Helm card because the override card shows the same empty list before an override.

### S-3. "T026: shows FR-015 warning banner when oidcHelmProvider has admin mapping" (line 700), assertion 726-728

- Before: `screen.getByText(/The group\(s\) on the Admin row above were mapped to admin via Helm values/)`
- After: `screen.getByText(/^Rule 1 above grants admin through Helm values/)` (line 725 "Helm-configured admin mapping" unchanged)
- Why: UdnBT `Description` text: "Rule 1 above grants admin through Helm values, not the dashboard, …". There is no "Admin row" any more.

### S-4. "T027: role mapping override save calls PUT /admin/config/auth with helmOverride.roleMappings" (line 793), steps 827-852

- Before: finds group `/admin role mapping/i`, types into "Add IdP group name…", clicks "Add group", confirms "Map to admin role", clicks
  "Save role mappings", expects `helmOverride.roleMappings.admin` to contain `new-admin-group`.
- After (fixture unchanged; `helmOverride: { roleMappings: {} }` counts as no override, so the card starts in the "From Helm values" state):
  ```ts
  await userEvent.click(await screen.findByRole("button", { name: "Override" }));
  await userEvent.click(screen.getByRole("button", { name: "Add rule" }));
  await userEvent.type(screen.getByRole("textbox", { name: "Rule 1 group" }), "new-admin-group");
  await userEvent.click(screen.getByRole("button", { name: /Rule 1 role/ }));
  await userEvent.click(await screen.findByRole("option", { name: "admin" }));
  await userEvent.click(screen.getByRole("button", { name: "Save role rules" }));
  await userEvent.click(await screen.findByRole("button", { name: /Map to admin role/ }));
  await waitFor(() => expect(saveHandler).toHaveBeenCalled());
  const helmOverride = (capturedBody as { helmOverride?: { roleRules?: unknown; roleMappings?: unknown } }).helmOverride;
  expect(helmOverride?.roleMappings).toBeUndefined();
  expect(helmOverride?.roleRules).toContainEqual({ group: "new-admin-group", role: "admin" });
  ```
  New name: "T027: role rule override save calls PUT /admin/config/auth with helmOverride.roleRules".
- Why: the per-role add rows and "Save role mappings" are gone (UdnBT: rule editor + "Save role rules"); the confirmation now opens on save.

### S-5. "T027: reset role mapping calls DELETE /admin/config/auth/role-mappings/{role}" (line 856), assertions 891-895

- Before: `findAllByRole("button", { name: /Reset to Helm default/ })` has length 1, click, `expect(resetHandler).toHaveBeenCalled()`.
- After (fixture unchanged; add `http.put("/admin/config/auth", saveHandler)` capturing the body):
  ```ts
  await userEvent.click(await screen.findByRole("button", { name: "Reset to Helm default" }));
  expect(await screen.findByText("From Helm values")).toBeInTheDocument();
  await userEvent.click(screen.getByRole("button", { name: /^Save changes$/i }));
  await waitFor(() => expect(saveHandler).toHaveBeenCalled());
  expect((capturedBody as { helmOverride?: unknown }).helmOverride).toBeUndefined();
  expect(resetHandler).not.toHaveBeenCalled();
  ```
  New name: "T027: Reset to Helm default removes the whole override on the next save".
- Why: OD-3/FR-007a: reset removes the whole override (rules and fallback); the per-role DELETE endpoint cannot remove `roleRules`/`defaultRole`
  (api/internal/handlers/config.go:212-280), so the reset is a draft change written by the section save. There is one Reset button, not one per role.

### S-6. "T027: empty-list override [] is treated as override, not absent (nobody)" (line 901), assertions 940-955

- Before: `getByText(/No groups mapped — nobody signs in as viewer via OIDC/)`, then finds a Reset button whose closest `div` text contains "viewer".
- After (line 936 "Overridden in dashboard" unchanged):
  ```ts
  expect(screen.getAllByText("No rules. SSO users get viewer and their role is never changed at sign-in.").length).toBeGreaterThan(0);
  // `viewer: []` replaces the Helm viewer groups: the effective list has no gameplane-members rule.
  expect(screen.queryByRole("textbox", { name: "Rule 1 group" })).not.toBeInTheDocument();
  expect(screen.getByRole("button", { name: "Reset to Helm default" })).toBeInTheDocument();
  ```
- Why: the per-role empty line and per-role Reset buttons are gone. The behaviour under test (an empty list is an override, not "absent") is kept:
  the card is in the overridden state, the effective rule list is empty, and Reset is offered.

### S-7. "T027: provenance updates from 'Overridden in dashboard' to 'From Helm values' after reset" (line 959), assertions 1022-1028

- Before: clicks Reset, `await waitFor(() => expect(resetHandler).toHaveBeenCalled())`.
- After:
  ```ts
  await userEvent.click(screen.getByRole("button", { name: "Reset to Helm default" }));
  expect(await screen.findByText("From Helm values")).toBeInTheDocument();
  expect(screen.queryByText("Overridden in dashboard")).not.toBeInTheDocument();
  expect(screen.getByRole("button", { name: "Override" })).toBeInTheDocument();
  ```
  (the `http.delete(...)` handler and `resetHandler` become unused; delete them.)
- Why: same as S-5; the provenance now flips immediately because reset edits the draft.

### S-8. "T031: admin role mapping warning appears with exact copy and requires confirmation" (line 1034), steps 1064-1070

- Before: admin group section + "Add IdP group name…" + "Add group".
- After: the S-4 steps up to and including `click("Save role rules")` (group `new-admin`), then the existing assertions 1076-1088 unchanged.
- Why: the confirmation is now triggered by saving an override that newly grants admin, not by adding a chip.

### S-9. "T031: unconfirmed admin dialog does not add group when cancelled" (line 1092), steps 1123-1153

- Before: add via the admin section, Escape, expects the admin input cleared and `queryByText("test-admin")` absent, save not called.
- After:
  ```ts
  // S-4 steps with group "test-admin", ending with click("Save role rules")
  expect(await screen.findByRole("button", { name: /Map to admin role/ })).toBeInTheDocument();
  await userEvent.keyboard("{Escape}");
  await waitFor(() => expect(screen.queryByRole("button", { name: /Map to admin role/ })).not.toBeInTheDocument());
  expect(saveHandler).not.toHaveBeenCalled();
  ```
- Why: cancelling now cancels the save; the typed rule stays in the unsaved draft (an editable field), so "the group is not shown" no longer holds.
  The protection under test (nothing is written without confirmation) is unchanged.

### S-10. "T031: admin confirmation dialog contains exact warning text from design" (line 1157), steps 1186-1192

- Before: admin section + "Add group".
- After: the S-4 steps up to `click("Save role rules")` (group `security-team`); assertions 1195-1200 unchanged.
- Why: as S-8.

### S-11. "shows AdminGroupsInlineWarning banner when admin groups are entered" (line 1204), assertions 1229-1255

- Before: types into "Admin groups" and expects the inline "Full admin access" banner with "Groups added here will be mapped to the admin role…" and
  "You'll be asked to confirm before this is saved", then expects it to disappear after clearing.
- After, option A (recommended, matches UdnBT): delete this test. The banner is not in the design (UdnBT's provider form shows an admin rule with no
  banner); the confirmation it announced is still covered by S-12 to S-14.
- After, option B: keep the banner under the rule editor whenever a rule's role is admin (code change in RULES group, not in the design), and rewrite
  the steps to "Add rule" + pick admin role + `findByText(/Full admin access/)`, then "Remove rule 1" + `waitFor(queryByText(...)).not...`.
- Why: the component `AdminGroupsInlineWarning` is removed with the per-role fields. **Decision needed from valgul (A or B).**

### S-12. "T050: shows FR-015 warning when adding admin groups in provider editor" (line 1259), step 1296-1297

- Before: `const adminInput = screen.getByLabelText("Admin groups"); await userEvent.type(adminInput, "admin-group");`
- After: the shared-helper four lines (Add rule, type "admin-group" into "Rule 1 group", pick "admin" in "Rule 1 role"). Rest unchanged.
- Why: the "Admin groups" field is replaced by rules (UdnBT).

### S-13. "T051: requires confirmation before adding provider with admin groups" (line 1320), step 1365-1366

- Before/after: same replacement as S-12. Rest unchanged.

### S-14. "T051: provider with admin groups is added after confirmation" (line 1389), step 1432-1433

- Before/after: same replacement as S-12. Rest unchanged.

### S-15. "reports a failed role mapping reset and keeps the override in the draft" (line 1508), assertions 1520-1529

- Before: DELETE answers 500 "reset refused"; expects that text and "Overridden in dashboard" still shown.
- After (reset no longer calls the server; test the failed save instead):
  ```ts
  server.use(http.put("/admin/config/auth", () => HttpResponse.text("save refused", { status: 500 })));
  await userEvent.click(await screen.findByRole("button", { name: "Reset to Helm default" }));
  await userEvent.click(screen.getByRole("button", { name: /^Save changes$/i }));
  expect(await screen.findByText("save refused")).toBeInTheDocument();
  // The reset stays in the draft so the user can retry.
  expect(screen.getByText("From Helm values")).toBeInTheDocument();
  expect(screen.getByRole("button", { name: /^Save changes$/i })).toBeEnabled();
  ```
  New name: "reports a failed save after a reset and keeps the reset in the draft". (The `http.delete` handler goes.)
- Why: same as S-5; there is no reset request that can fail on its own.

## B. `web/src/routes/AdminSettings_sections.test.tsx`

### S-16. "renders the role-mapping fields and locks Default role until a mapping exists" (line 131), assertions 138-146

- Before:
  ```ts
  expect(screen.getByLabelText(/Admin groups/i)).toBeInTheDocument();
  expect(screen.getByLabelText(/Operator groups/i)).toBeInTheDocument();
  expect(screen.getByLabelText(/Viewer groups/i)).toBeInTheDocument();
  const defaultRole = screen.getByRole("button", { name: /Default role/i });
  expect(defaultRole).toBeDisabled();
  await user.type(screen.getByLabelText(/Admin groups/i), "gp-admins");
  expect(defaultRole).toBeEnabled();
  ```
- After:
  ```ts
  expect(screen.getByText("Role rules")).toBeInTheDocument();
  expect(screen.getByText("No rules. SSO users get viewer and their role is never changed at sign-in.")).toBeInTheDocument();
  const fallback = screen.getByRole("button", { name: /Fallback role/i });
  expect(fallback).toBeDisabled();
  await user.click(screen.getByRole("button", { name: "Add rule" }));
  await user.type(screen.getByRole("textbox", { name: "Rule 1 group" }), "gp-admins");
  expect(screen.getByRole("button", { name: /Fallback role/i })).toBeEnabled();
  ```
  New name: "renders the role rule editor and locks the fallback until a rule exists". (Lines 136-137, scopes and groups claim, unchanged.)
- Why: UdnBT/mrF6f replace the three fields and "Default role" with the rule editor and "Fallback role"; the lock stays because the API refuses
  `defaultRole` without rules (config.go:749-751).

### S-17. "serializes scopes, groups claim, role mappings, and deny into the saved provider" (line 149), steps 172-175 and expected object 186-197

- Before (steps): type "GP Admins , platform-admins" into Admin groups, "gp-view" into Viewer groups, pick `/deny/i` in Default role.
- After (steps):
  ```ts
  for (let i = 0; i < 3; i++) await user.click(screen.getByRole("button", { name: "Add rule" }));
  await user.type(screen.getByRole("textbox", { name: "Rule 1 group" }), " GP Admins ");
  await user.type(screen.getByRole("textbox", { name: "Rule 2 group" }), "platform-admins");
  await user.type(screen.getByRole("textbox", { name: "Rule 3 group" }), "gp-view");
  for (const n of [1, 2]) {
    await user.click(screen.getByRole("button", { name: new RegExp(`Rule ${n} role`) }));
    await user.click(await screen.findByRole("option", { name: "admin" }));
  }
  await user.click(screen.getByRole("button", { name: /Fallback role/i }));
  await user.click(await screen.findByRole("option", { name: "Refuse sign-in" }));
  ```
- Before (expected): `roleMappings: { admin: ["GP Admins", "platform-admins"], viewer: ["gp-view"] }, defaultRole: "deny"`
- After (expected): `roleRules: [{ group: "GP Admins", role: "admin" }, { group: "platform-admins", role: "admin" }, { group: "gp-view", role: "viewer" }], defaultRole: "deny"`
  New name: "serializes scopes, groups claim, role rules, and Refuse sign-in into the saved provider".
- Why: FR-005 "The dashboard MUST save edited providers in the rule form"; the deny option is labelled "Refuse sign-in" (UdnBT `fallbackRole`).
  Group names are still trimmed.

### S-18. "round-trips a stored provider's mapping fields through an unrelated save" (line 200), assertion 236

- Before: `expect(saved?.providers).toContainEqual({ ...corp, enabled: false });` where `corp.roleMappings = { admin: ["gp-admins"] }`
- After:
  ```ts
  const { roleMappings, ...rest } = corp;
  expect(roleMappings).toEqual({ admin: ["gp-admins"] });
  expect(saved?.providers).toContainEqual({ ...rest, enabled: false, roleRules: [{ group: "gp-admins", role: "admin" }] });
  ```
  New name: "rewrites a stored old-form provider as roleRules on an unrelated save, keeping its other fields".
- Why: task T024 "Saving an old-form provider writes `roleRules`" and OD-1 (one form per provider; the dashboard rewrites old keys as rules on the next
  save). scopes, groupsClaim, defaultRole and the rest still round-trip untouched.

## C. `web/e2e/screenshots/slice4.spec.ts` (Playwright, `@screenshots`; visual-diff and screenshot-refresh workflows)

These captures compare against design PNGs. uMiwd, nNGDX, QgW58, zqzr4 and the chip/badge components XL5ZU, vStkb, uw0dB, BV5ei were drawn for the old
per-role UI, which UdnBT/mrF6f supersede. Beyond the selector fixes below, valgul decides whether to retire those captures or re-point them at the new
frames (UdnBT, mrF6f) with matching screenshot fixtures.

### S-19. "uMiwd: Admin Settings — Authentication" (line 134), assertions 144-145

- Before: `getByText("Role mapping overrides")`; `getByLabel("Admin role mapping").getByText("gameplane-admins")`
- After: `await expect(page.getByText("Role rule override")).toBeVisible(); await expect(page.getByRole("textbox", { name: "Rule 1 group" })).toHaveValue("gameplane-admins");`
- Why: card renamed and per-role groups removed (UdnBT). The `oidc-design` variant's old per-role override shows as the effective rule list.

### S-20. "Rwnu3: Provenance Badge — From Helm" (line 165), line 168

- Before: `await setAdminConfigVariant(context, "oidc");`
- After: `await setAdminConfigVariant(context, "oidc-empty-mappings");`
- Why: the badge is now per card, not per role; the `oidc` variant has an override, so only "Overridden in dashboard" shows. The empty-mappings variant
  has no override, so the card shows "From Helm values" (mrF6f).

### S-21 to S-23. "XL5ZU: Removable Group Chip — Orange (admin)" (177), "vStkb: … Violet (operator)" (204), "uw0dB: … Secondary (viewer)" (226)

- Before: `page.getByLabel("<Role> role mapping", { exact: true })…` (185/200, 212/222, 234/244) capturing a `RemovableGroupChip`.
- After: no page renders `RemovableGroupChip` any more (it was used only by the removed per-role card). Option A (recommended): delete the three tests and
  mark the three design components obsolete. Option B: render the chip in a Storybook-style harness page (new work, out of scope).
- Why: the per-role chip lists are replaced by the rule editor (UdnBT).

### S-24. "nNGDX: Admin Settings — Authentication (No OIDC mappings)" (line 248), line 256

- Before: `page.getByText("No OIDC role mappings yet")`
- After: `page.getByText("No rules. SSO users get viewer and their role is never changed at sign-in.").first()`; the capture target should become mrF6f
  (or the test is retired with the nNGDX frame).
- Why: the empty state is now the rule list's (mrF6f).

### S-25. "BV5ei: Provenance Badge — Not configured" (line 261), line 269

- Before: `page.getByText("Not configured").first()` capturing `[data-type="notConfigured"]`.
- After: no page renders `ProvenanceBadge type="notConfigured"` any more (the override card is hidden when there is no Helm provider and no override).
  Option A (recommended): delete the test. Option B: show the card with a "Not configured" badge when no Helm provider exists (not in the design).
- Why: per-role provenance is gone.

### S-26. "QgW58: Admin Settings — Authentication (Save rejected)" (line 275), lines 288-289

- Before: no config variant; `getByText("Role mapping overrides")`; click `/save role mappings/i`.
- After: add `await setAdminConfigVariant(context, "oidc");` before `page.goto`, then
  `await expect(page.getByText("Role rule override")).toBeVisible({ timeout: 10_000 }); await page.getByRole("button", { name: "Save role rules" }).click();`
  (assertion 290 on the rejected-save text unchanged).
- Why: the override card now shows only with a Helm provider or an override, and its button is "Save role rules".

### S-27. "zqzr4: Admin Settings — Authentication (Admin mapping warning)" (line 297), lines 311-312

- Before: `page.getByLabel("Admin groups").fill("ops-leads"); expect(page.getByLabel("Admin groups")).toHaveValue("ops-leads");`
- After:
  ```ts
  await page.getByRole("button", { name: "Add rule" }).click();
  await page.getByRole("textbox", { name: "Rule 1 group" }).fill("ops-leads");
  await page.getByRole("button", { name: /Rule 1 role/ }).click();
  await page.getByRole("option", { name: "admin" }).click();
  await expect(page.getByRole("textbox", { name: "Rule 1 group" })).toHaveValue("ops-leads");
  ```
- Why: field replaced by rules. The zqzr4 frame's inline banner is not built (see S-11).

### S-28. "Kp48V: Confirm Admin Mapping dialog" (line 317), line 332

- Before: `await page.getByLabel("Admin groups").fill("ops-leads");`
- After: the first four lines of S-27's after block. The rest (Add provider → "Confirm admin role mapping?" dialog) is unchanged.
- Why: as S-27; the dialog itself is unchanged.

## D. Fixture files touched only by appending (no sign-off needed, listed for completeness)

- `web/src/test/handlers.ts`: append `ROLES_WITH_CUSTOM_ROLE_FIXTURE` and `rolesWithCustomRoleHandler` after line 1939. No existing handler changes.
- `web/src/test/screenshotData.ts` is not changed. Its `oidc`, `oidc-design` and `oidc-empty-mappings` variants still use the old `roleMappings` form;
  the UI reads them through the old-form path, so S-19 to S-28 work with them as they are.
