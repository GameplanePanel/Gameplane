import { test, expect } from "@playwright/test";
import type {
  APIRequestContext,
  APIResponse,
  Browser,
  BrowserContext,
  Page,
} from "@playwright/test";
import { readFileSync } from "node:fs";
import { loginIfNeeded, seedServer, seedTemplate } from "./_seed";

// Live: feature 016 user theme customization (specs/done_016-user-theme-customization).
// This is the Playwright tier of the feature's E2E coverage per quickstart.md
// §5.1/§5.2, written against the binding contracts:
//
//   - contracts/user-preferences-api.md   (GET/PUT/reset /users/me/preferences)
//   - contracts/theme-tokens-v2.md        (data-theme-preset / data-theme-type /
//                                          data-custom-css on <html>,
//                                          #gameplane-custom-theme-vars, then
//                                          #gameplane-custom-css as LAST <head> child)
//   - contracts/theme-ui.md               (/settings/theme page, three preset
//                                          radio cards incl. the Custom colors
//                                          activation card, banner, login link)
//   - contracts/theme-export.md           (gameplane-theme v1 export document)
//
// Coverage is appended per task in order: T015 (preset switching), T023
// (migration defaults + propagation + unauthenticated isolation), T028
// (custom colors), T033 (custom CSS overlay, safe mode, export/import).
//
// Identity/login budget (the API rate limiter is 5/min + burst 10 per IP and
// 3/min + burst 6 per username — see test/e2e/buckets.sh): the admin session
// is reused from storageState (loginIfNeeded is a no-op while it is valid),
// the seeded users log in over the API exactly once per scenario that needs
// them, and the only UI login is the safe-mode link flow, which must be a
// real form submission to prove the entry point.
//
// Migration provenance: no e2e path can manufacture a truly pre-migration
// user through the real API (migration 011 ran at pod startup, before any
// e2e account existed) — this mirrors the note in
// test/e2e/api_theme_preferences_e2e_test.go. The seeded "pre-migration"
// user below is therefore seeded as stored state (PUT presetId=legacy), and
// the INSERT..SELECT seeding itself is covered by the api module's migration
// test (quickstart.md §3.1).

// The `playwright` fixture exposes the bound APIRequest factory; only
// `request` is needed here (a fresh cookie jar per seeded user).
type PlaywrightFixture = Pick<typeof import("@playwright/test"), "request">;

// Preset accent tokens, contracts/theme-tokens-v2.md §2, normalized the same
// way as web/e2e/screenshots/all-screens-legacy.spec.ts (lowercase, collapsed
// whitespace) so they can be compared against getComputedStyle output.
const PINK_DARK_ACCENT = "oklch(69.50% 0.2229 355.31)"; // #FF4FA3
const PINK_LIGHT_ACCENT = "oklch(59.16% 0.2180 0.58)"; // #DB2777
const LEGACY_DARK_ACCENT = "oklch(69.11% 0.1944 44.01)"; // #F97316
const LEGACY_LIGHT_ACCENT = "oklch(62.0% 0.20 40.0)"; // #EA580C

// quickstart.md §5.2.3 picks the Emerald swatch; derivation (theme-derivation.ts)
// passes the accent through as lowercase hex.
const EMERALD_ACCENT = "#10b981";

// The overlay probe. The quickstart walkthrough targets `.topbar`, but the
// real TopBar root is a bare <header> (TopBar.tsx), so the rule uses the
// element selector — immune to Tailwind class renames.
const OVERLAY_RULE = "header { border-bottom: 3px solid lime !important; }";
const POISON_IMPORT_RULE = '@import url("https://evil.example.com/track.css");';
const LIME_RGB = "rgb(0, 255, 0)";

// FR-013 rejection surfaces. The editor quotes the offending line in design
// copy (theme-ui.md §3.3: "External resource loads are not allowed (line N:
// `@import …`)"); the server answers with the sanitizer's verbatim message
// (api/internal/handlers/users.go), which the import path renders inline.
const EDITOR_IMPORT_REJECTION = /external resource loads are not allowed/i;
const SERVER_IMPORT_REJECTION = /customCss rejected: @import rules are not allowed/i;

// localStorage cache the boot script (index.html) reads before first paint;
// also used to poison unauthenticated surfaces below.
const PREFS_STORAGE_KEY = "gameplane-theme-prefs";

interface ThemePrefs {
  themeType: "preset" | "custom_colors";
  presetId: "pink" | "legacy";
  appearanceMode: "light" | "dark" | "system";
  customColors: { accent: string; surface: string } | null;
  customCssEnabled: boolean;
  customCss: string | null;
  updatedAt?: string;
}

// contracts/theme-export.md §2 (gameplane-theme v1).
interface ThemeExportDoc {
  format: "gameplane-theme";
  version: 1;
  preferences: {
    themeType: ThemePrefs["themeType"];
    presetId: ThemePrefs["presetId"];
    appearanceMode: ThemePrefs["appearanceMode"];
    customColors: ThemePrefs["customColors"];
    customCssEnabled: boolean;
    customCss: string | null;
  };
}

interface SeededUser {
  username: string;
  password: string;
}

// ---------------------------------------------------------------------
// API helpers (through vite's proxy onto the kubectl port-forward, same as
// _seed.ts). Mutating calls pin Accept + the double-submit CSRF header.

async function csrfHeaders(ctx: APIRequestContext): Promise<Record<string, string>> {
  // Mirrors web/e2e/specs/live/_seed.ts's private seedHeaders.
  const state = await ctx.storageState();
  const token = state.cookies.find((c) => c.name === "gameplane_csrf")?.value ?? "";
  return { "X-Gameplane-CSRF": token, Accept: "application/json" };
}

async function getPrefs(ctx: APIRequestContext): Promise<ThemePrefs> {
  const res = await ctx.get("/users/me/preferences", { headers: { Accept: "application/json" } });
  expect(res.ok(), await res.text().catch(() => "")).toBeTruthy();
  return (await res.json()) as ThemePrefs;
}

async function putPrefs(ctx: APIRequestContext, body: Record<string, unknown>): Promise<APIResponse> {
  return ctx.put("/users/me/preferences", {
    headers: await csrfHeaders(ctx),
    data: body,
  });
}

// Creates a local viewer user as the admin. Any authenticated user may read
// and write their own preferences (contracts §1.1 "Permissions: None"), so
// the viewer role keeps the seed least-privileged.
async function createLocalUser(
  request: APIRequestContext,
  username: string,
  password: string,
): Promise<{ id: number }> {
  const res = await request.post("/users", {
    headers: await csrfHeaders(request),
    data: { username, displayName: username, password, role: "viewer" },
  });
  expect(res.ok(), await res.text().catch(() => "")).toBeTruthy();
  const dto = (await res.json()) as { id: number };
  return { id: dto.id };
}

// Logs a seeded user in over the API and returns their private request
// context (own session + CSRF cookies). UI logins are reserved for flows
// that must exercise the form; everything else stays inside the rate budget.
async function loginUserApi(
  playwright: PlaywrightFixture,
  username: string,
  password: string,
): Promise<APIRequestContext> {
  const ctx = await playwright.request.newContext({ baseURL: "http://localhost:5173" });
  const res = await ctx.post("/auth/login", {
    data: { username, password },
    headers: { Accept: "application/json" },
  });
  expect(res.ok(), `login as ${username}: ${await res.text().catch(() => "")}`).toBeTruthy();
  await res.dispose();
  return ctx;
}

// Builds an authenticated browser context for a user's API session. The
// context starts with no localStorage — the prefs cache is written by the
// app itself, which is what makes these contexts behave like fresh devices.
async function browserContextFor(
  api: APIRequestContext,
  browser: Browser,
): Promise<BrowserContext> {
  const state = await api.storageState();
  return browser.newContext({ storageState: state });
}

// ---------------------------------------------------------------------
// UI helpers. Selectors prefer accessible roles/labels and the documented
// DOM contract attributes over CSS classes, aligned with the landed
// implementation where it exists (web/src/routes/ThemeSettings.tsx,
// SafeModeBanner.tsx, Login.tsx) and falling back to contract/design-derived
// labels (assets/design-export/json/lWvcv.json Theme Settings, DAz77.json banner,
// J14ME.json login card) otherwise. Scoping note: "Custom colors" exists
// both as a preset radio card label (inside the "Preset theme" radiogroup)
// and as the section card title — preset selections are therefore always
// scoped to the radiogroup.

async function gotoApp(page: Page): Promise<void> {
  await page.goto("/");
  await page.waitForLoadState("domcontentloaded");
  await loginIfNeeded(page);
}

async function openThemeSettings(page: Page): Promise<void> {
  await page.goto("/settings/theme");
  await expect(page.getByRole("heading", { name: /theme & appearance/i })).toBeVisible({
    timeout: 15_000,
  });
}

async function saveThemeSettings(page: Page): Promise<void> {
  // theme-ui.md §2: the actions row's primary Save persists to the server;
  // changes above it apply live for preview. /^save/i also matches the
  // transient "Saving…" label while the PUT is in flight.
  await page.getByRole("button", { name: /^save/i }).click();
}

// Same button, but a no-op when the draft already matches the last-saved
// preferences (Save is disabled while clean — ThemeSettings.tsx's Save
// button, isDisabled={!dirty || …}). Used where a
// call site can't otherwise tell whether its preceding steps left anything
// unsaved (e.g. a retried test whose earlier attempt already persisted the
// same target state), so an unconditional click would hang until the test
// timeout instead of failing fast or succeeding.
async function saveIfDirty(page: Page): Promise<void> {
  const button = page.getByRole("button", { name: /^save/i });
  if (await button.isDisabled()) return;
  await button.click();
}

// The Preset theme card's radio cards (theme-ui.md §3.1): Modern Pink,
// Legacy Orange, and the Custom colors activation card.
const presetThemeGroup = (page: Page) => page.getByRole("radiogroup", { name: "Preset theme" });

// Selects a preset radio card. Selection updates the draft (live preview)
// but does not persist until Save.
async function selectPresetCard(
  page: Page,
  name: "Modern Pink" | "Legacy Orange" | "Custom colors",
): Promise<void> {
  await presetThemeGroup(page).getByText(name, { exact: true }).click();
}

// Selects a preset base and waits until the server has the change.
async function selectPreset(
  page: Page,
  request: APIRequestContext,
  target: "pink" | "legacy",
): Promise<void> {
  await selectPresetCard(page, target === "pink" ? "Modern Pink" : "Legacy Orange");
  await saveThemeSettings(page);
  await expect
    .poll(async () => (await getPrefs(request)).presetId, { timeout: 15_000 })
    .toBe(target);
  await expect(page.locator("html")).toHaveAttribute("data-theme-preset", target);
}

// The Custom colors card's controls are disabled until the "Custom colors"
// radio card is selected (theme-ui.md §3.2 "Activation").
async function activateCustomColors(page: Page): Promise<void> {
  const swatch = page.getByRole("button", { name: /^emerald$/i });
  await expect(swatch).toBeVisible({ timeout: 15_000 });
  // While a preset is active the Custom colors controls stay visible but
  // disabled (theme-ui.md §3.2 "Activation").
  if ((await page.locator("html").getAttribute("data-theme-type")) !== "custom_colors") {
    await expect(swatch).toBeDisabled();
    await expect(page.getByRole("radiogroup", { name: /surface tone/i })).toBeVisible();
  }
  await selectPresetCard(page, "Custom colors");
  await expect(page.locator("html")).toHaveAttribute("data-theme-type", "custom_colors");
  await expect(swatch).toBeEnabled();
}

const MODE_LABEL: Record<"light" | "dark" | "system", string> = {
  light: "Light",
  dark: "Dark",
  system: "System",
};

async function setAppearance(
  page: Page,
  request: APIRequestContext,
  mode: "light" | "dark" | "system",
): Promise<void> {
  // The Appearance mode segmented control is a labeled group of buttons
  // (role="group" aria-label="Appearance mode"), distinct from the
  // sidebar footer's "Appearance" group.
  const modeGroup = page.getByRole("group", { name: "Appearance mode" });
  const button = modeGroup.getByRole("button", { name: MODE_LABEL[mode], exact: true });
  // Already selected: the draft stays clean and Save is disabled, so only
  // assert the stored and applied mode below.
  if ((await button.getAttribute("aria-pressed")) !== "true") {
    await button.click();
    await saveThemeSettings(page);
  }
  await expect
    .poll(async () => (await getPrefs(request)).appearanceMode, { timeout: 15_000 })
    .toBe(mode);
  await expect(page.locator("html")).toHaveAttribute(
    "data-theme",
    mode === "system" ? /light|dark/ : mode,
  );
}

const overlaySwitch = (page: Page) =>
  page.getByRole("switch", { name: /enable custom css overlay/i });
const cssEditor = (page: Page) => page.getByRole("textbox", { name: /^custom css$/i });

// The app TopBar is the only <header> carrying the user menu trigger.
async function topBarBorderBottomColor(page: Page): Promise<string> {
  return page
    .locator("header")
    .filter({ has: page.getByRole("button", { name: /user menu/i }) })
    .first()
    .evaluate((el: HTMLElement) => getComputedStyle(el).borderBottomColor);
}

function normalizeToken(value: string): string {
  return value.replace(/\s+/g, " ").trim().toLowerCase();
}

async function rootVar(page: Page, name: string): Promise<string> {
  const raw = await page.evaluate((n) => {
    return getComputedStyle(document.documentElement).getPropertyValue(n);
  }, name);
  return normalizeToken(raw);
}

// contracts/theme-tokens-v2.md §5: overlay injected, last <head> child, rule
// visibly winning over the base.
async function expectOverlayApplied(page: Page): Promise<void> {
  await expect(page.locator("html")).toHaveAttribute("data-custom-css", "on");
  const overlay = page.locator("#gameplane-custom-css");
  await expect(overlay).toBeAttached();
  expect(await overlay.textContent()).toContain("lime");
  const isLastHeadChild = await page.evaluate(() => {
    const el = document.getElementById("gameplane-custom-css");
    return el !== null && el.parentElement === document.head && el === document.head.lastElementChild;
  });
  expect(isLastHeadChild).toBe(true);
  expect(await topBarBorderBottomColor(page)).toBe(LIME_RGB);
}

// The overlay switch edits the draft; the DOM follows live, the server only
// on Save (ThemeSettings.tsx draft model). Reads the switch's own state —
// data-custom-css can read "off" while the draft has the overlay enabled if
// the stored stylesheet is empty.
async function setOverlayEnabled(
  page: Page,
  request: APIRequestContext,
  enabled: boolean,
): Promise<void> {
  const sw = overlaySwitch(page);
  await expect(sw).toBeVisible({ timeout: 15_000 });
  const on = await sw.isChecked();
  if (on === enabled) return;
  // HeroUI's Switch keeps the role=switch input visually hidden under its own decorative spans, so a pointer click is intercepted; toggle it the way a keyboard user does.
  await sw.press("Space");
  await expect(sw).toBeChecked({ checked: enabled });
  await saveThemeSettings(page);
  await expect
    .poll(async () => (await getPrefs(request)).customCssEnabled, { timeout: 15_000 })
    .toBe(enabled);
}

// The single-confirmation reset dialog (theme-ui.md §5). HeroUI's
// AlertDialog renders role="alertdialog"; the confirm label is "Reset".
const resetDialog = (page: Page) => page.locator('[role="dialog"], [role="alertdialog"]');

async function confirmResetDialog(page: Page): Promise<void> {
  const dialog = resetDialog(page);
  await expect(dialog).toBeVisible({ timeout: 10_000 });
  await expect(
    dialog.getByText(/this deletes your custom colors and custom css/i),
  ).toBeVisible();
  await dialog.getByRole("button", { name: /^(continue|confirm|delete|reset)$/i }).click();
}

test.describe("live: theme customization", () => {
  test.skip(
    process.env.GAMEPLANE_E2E_TARGET !== "live",
    "live-only — the theme DOM contract needs the real API-backed session",
  );

  const stamp = Date.now().toString(36);
  const legacyUser: SeededUser = {
    username: `e2e-theme-legacy-${stamp}`,
    password: `e2e-${stamp}-legacy-pw`,
  };
  const freshUser: SeededUser = {
    username: `e2e-theme-fresh-${stamp}`,
    password: `e2e-${stamp}-fresh-pw`,
  };
  const roundTripUser: SeededUser = {
    username: `e2e-theme-roundtrip-${stamp}`,
    password: `e2e-${stamp}-roundtrip-pw`,
  };
  const tmplName = `e2e-pw-theme-tmpl-${stamp}`;
  const serverName = `e2e-pw-theme-${stamp}`;

  let userIds: number[] = [];
  let cleanups: Array<(request: APIRequestContext) => Promise<void>> = [];

  test.beforeAll(async ({ request }) => {
    const a = await createLocalUser(request, legacyUser.username, legacyUser.password);
    const b = await createLocalUser(request, freshUser.username, freshUser.password);
    const c = await createLocalUser(request, roundTripUser.username, roundTripUser.password);
    userIds = [a.id, b.id, c.id];
    const tmpl = await seedTemplate(request, tmplName);
    const server = await seedServer(request, {
      name: serverName,
      template: tmplName,
      description: "Live theme-customization probe",
    });
    cleanups = [server.cleanup, tmpl.cleanup];
  });

  test.afterAll(async ({ request }) => {
    // Restore the shared admin account to factory styling so any later live
    // spec starts from the default Pink preset no matter where this file
    // (or a retried test in it) left off.
    try {
      const res = await request.post("/users/me/preferences/reset", {
        headers: await csrfHeaders(request),
        data: { presetId: "pink", appearanceMode: "system" },
      });
      await res.dispose();
    } catch {
      // best effort — a leaked theme choice must not fail the run
    }
    for (const id of userIds) {
      await request
        .delete(`/users/${id}`, { headers: await csrfHeaders(request) })
        .catch(() => undefined);
    }
    for (const cleanup of cleanups) await cleanup(request);
  });

  // -------------------------------------------------------------------
  // T015 / US1: preset switching without reload, persistence, brand accent
  // per mode (quickstart.md §5.2.2, SC-003).

  test("preset switch applies without reload in under 100ms, persists across reload, and light/dark keeps each preset's brand accent", async ({
    page,
    request,
  }) => {
    await gotoApp(page);

    // contracts/theme-ui.md §1.1: the TopBar user menu carries the
    // "Theme & Appearance" entry point.
    await page.getByRole("button", { name: /user menu/i }).click();
    const entry = page.getByRole("menuitem", { name: /theme & appearance/i });
    await expect(entry).toBeVisible();
    await entry.click();
    await page.waitForURL((u) => new URL(u).pathname === "/settings/theme", { timeout: 15_000 });
    await expect(page.getByRole("heading", { name: /theme & appearance/i })).toBeVisible();

    // A retried run can leave the admin account on Legacy — establish the
    // Pink baseline the SC-003 measurement compares against.
    if ((await page.locator("html").getAttribute("data-theme-preset")) !== "pink") {
      await selectPreset(page, request, "pink");
    }

    // FR-012 note renders on the preset card (theme-ui.md §3.1).
    await expect(page.getByText(/kept and can be re-applied later/i)).toBeVisible();

    // SC-003: selecting the preset applies in under 100ms without a reload.
    // The provider applies the change optimistically to the DOM
    // (useThemePreferences.ts), so the attribute flip is the assert — the
    // waitForFunction timeout IS the 100ms budget.
    const previousPreset = "pink";
    await presetThemeGroup(page).getByText("Legacy Orange", { exact: true }).click();
    await page.waitForFunction(
      (prev: string) => document.documentElement.getAttribute("data-theme-preset") !== prev,
      previousPreset,
      { timeout: 100 },
    );
    // No reload happened: exactly one navigation since page load.
    expect(
      await page.evaluate(() => performance.getEntriesByType("navigation").length),
    ).toBe(1);

    // Save persists (theme-ui.md §2 actions row), verified through the API.
    await saveThemeSettings(page);
    await expect
      .poll(async () => (await getPrefs(request)).presetId, { timeout: 15_000 })
      .toBe("legacy");

    // …and the choice survives a reload.
    await page.reload();
    await page.waitForLoadState("domcontentloaded");
    await expect(page.getByRole("heading", { name: /theme & appearance/i })).toBeVisible({
      timeout: 15_000,
    });
    await expect(page.locator("html")).toHaveAttribute("data-theme-preset", "legacy");
    await expect(page.locator("html")).toHaveAttribute("data-theme-type", "preset");

    // Light/dark toggle keeps each preset's brand accent (theme-tokens-v2.md
    // §2): Legacy orange and Pink pink in BOTH modes.
    await setAppearance(page, request, "dark");
    expect(await rootVar(page, "--accent")).toBe(LEGACY_DARK_ACCENT);
    await setAppearance(page, request, "light");
    expect(await rootVar(page, "--accent")).toBe(LEGACY_LIGHT_ACCENT);

    await selectPreset(page, request, "pink");
    await setAppearance(page, request, "dark");
    expect(await rootVar(page, "--accent")).toBe(PINK_DARK_ACCENT);
    await setAppearance(page, request, "light");
    expect(await rootVar(page, "--accent")).toBe(PINK_LIGHT_ACCENT);
  });

  // -------------------------------------------------------------------
  // T023 / US2: migration defaults, multi-device propagation, and
  // unauthenticated isolation (quickstart.md §5.2.1, §5.2.8).

  test("a pre-migration-seeded account lands on Legacy while a freshly created account lands on Pink", async ({
    browser,
    playwright,
  }) => {
    // Seed the "pre-migration" user's stored state. Legacy is what migration
    // 011 wrote into existing users' rows; the synthesized no-row default
    // for post-migration users is Pink (contracts §1.1 note).
    const aApi = await loginUserApi(playwright, legacyUser.username, legacyUser.password);
    try {
      const put = await putPrefs(aApi, {
        themeType: "preset",
        presetId: "legacy",
        appearanceMode: "system",
        customCssEnabled: false,
      });
      expect(put.ok(), await put.text().catch(() => "")).toBeTruthy();

      const aCtx = await browserContextFor(aApi, browser);
      try {
        const aPage = await aCtx.newPage();
        // The fresh context boots the Pink default (no cache); the backend
        // reconciliation must override it with the stored Legacy choice.
        await aPage.goto("/");
        await expect(aPage.getByRole("navigation", { name: "Primary" })).toBeVisible({
          timeout: 20_000,
        });
        await expect(aPage.locator("html")).toHaveAttribute("data-theme-preset", "legacy", {
          timeout: 15_000,
        });
        await expect(aPage.locator("html")).toHaveAttribute("data-theme-type", "preset");
        await expect(aPage.locator("#gameplane-custom-css")).toHaveCount(0);
      } finally {
        await aCtx.close();
      }
    } finally {
      await aApi.dispose();
    }

    const fApi = await loginUserApi(playwright, freshUser.username, freshUser.password);
    try {
      const fCtx = await browserContextFor(fApi, browser);
      try {
        const fPage = await fCtx.newPage();
        await fPage.goto("/");
        await expect(fPage.getByRole("navigation", { name: "Primary" })).toBeVisible({
          timeout: 20_000,
        });
        await expect(fPage.locator("html")).toHaveAttribute("data-theme-preset", "pink", {
          timeout: 15_000,
        });
        await expect(fPage.locator("html")).toHaveAttribute("data-theme-type", "preset");
        await expect(fPage.locator("#gameplane-custom-css")).toHaveCount(0);
      } finally {
        await fCtx.close();
      }
    } finally {
      await fApi.dispose();
    }
  });

  test("a theme change propagates to a fresh browser context (multi-device)", async ({
    page,
    browser,
    request,
  }) => {
    await gotoApp(page);
    await openThemeSettings(page);

    // "Device A": switch the preset in the admin's session.
    await selectPreset(page, request, "legacy");

    // "Device B": same account, brand-new context with only the session
    // cookies — no localStorage prefs cache. The stored change must win.
    const cookiesOnly = await page
      .context()
      .storageState()
      .then((s) => ({ cookies: s.cookies, origins: [] }));
    const ctxB = await browser.newContext({ storageState: cookiesOnly });
    try {
      const pB = await ctxB.newPage();
      await pB.goto("/");
      await expect(pB.getByRole("navigation", { name: "Primary" })).toBeVisible({
        timeout: 20_000,
      });
      await expect(pB.locator("html")).toHaveAttribute("data-theme-preset", "legacy", {
        timeout: 15_000,
      });
    } finally {
      await ctxB.close();
    }

    // And back: a later change propagates just the same.
    await selectPreset(page, request, "pink");
    const ctxC = await browser.newContext({ storageState: cookiesOnly });
    try {
      const pC = await ctxC.newPage();
      await pC.goto("/");
      await expect(pC.getByRole("navigation", { name: "Primary" })).toBeVisible({
        timeout: 20_000,
      });
      await expect(pC.locator("html")).toHaveAttribute("data-theme-preset", "pink", {
        timeout: 15_000,
      });
    } finally {
      await ctxC.close();
    }
  });

  test("/login and a public share link always render Pink with no custom CSS (FR-011)", async ({
    page,
    browser,
  }) => {
    // Mint a real share for the seeded server as the admin (owner).
    await gotoApp(page);
    const createRes = await page.request.post(`/servers/${serverName}:shares`, {
      headers: await csrfHeaders(page.request),
      data: { canStart: true, expiresIn: "24h" },
    });
    expect(createRes.ok(), await createRes.text().catch(() => "")).toBeTruthy();
    const share = (await createRes.json()) as { id: string; token: string };

    // A genuinely anonymous recipient context — and poison its prefs cache
    // the way a previously signed-in session would (Legacy + overlay), which
    // is exactly what FR-011 says must be ignored.
    const anon = await browser.newContext();
    try {
      await anon.addInitScript(
        (css) => {
          window.localStorage.setItem(
            PREFS_STORAGE_KEY,
            JSON.stringify({
              themeType: "preset",
              presetId: "legacy",
              appearanceMode: "dark",
              customColors: null,
              customCssEnabled: true,
              customCss: css,
            }),
          );
        },
        OVERLAY_RULE,
      );
      const aPage = await anon.newPage();

      await aPage.goto("/login");
      await aPage.waitForLoadState("domcontentloaded");
      await expect(aPage.locator("html")).toHaveAttribute("data-theme-preset", "pink", {
        timeout: 15_000,
      });
      await expect(aPage.locator("html")).toHaveAttribute("data-custom-css", "off");
      await expect(aPage.locator("#gameplane-custom-css")).toHaveCount(0);

      await aPage.goto(`/share/${share.token}`);
      await expect(aPage.getByRole("heading", { name: serverName })).toBeVisible({
        timeout: 20_000,
      });
      await expect(aPage.locator("html")).toHaveAttribute("data-theme-preset", "pink");
      await expect(aPage.locator("html")).toHaveAttribute("data-custom-css", "off");
      await expect(aPage.locator("#gameplane-custom-css")).toHaveCount(0);
    } finally {
      await anon.close();
      await page.request
        .delete(`/servers/${serverName}/shares/${share.id}`, {
          headers: await csrfHeaders(page.request),
        })
        .catch(() => undefined);
    }
  });

  // -------------------------------------------------------------------
  // T028 / US3: custom colors base — activation via the preset card's
  // "Custom colors" radio, live adoption (SC-004), contrast across
  // light/dark, reset to preset (quickstart.md §5.2.3, theme-ui.md §3.2).

  test("custom colors activate via the preset card, apply live within three clicks, follow the surface's brightness for light/dark, and reset restores the preset", async ({
    page,
    request,
  }) => {
    await gotoApp(page);
    await openThemeSettings(page);

    // Setup (not part of the SC-004 click budget): force dark so the token
    // reads below are deterministic.
    await setAppearance(page, request, "dark");
    expect(await rootVar(page, "--accent")).toBe(PINK_DARK_ACCENT);

    // Click 1 — activate the Custom colors radio card in the Preset theme
    // card (theme-ui.md §3.2 "Activation"). The Custom colors card's
    // controls are disabled until this is selected.
    await activateCustomColors(page);

    // Click 2 — Emerald accent swatch (theme-ui.md §3.2 palette).
    await page.getByRole("button", { name: /^emerald$/i }).click();
    // SC-004: the controls adopt the color live — after the SECOND click
    // (activation + accent), inside the three-click budget. The derived
    // tokens mount in #gameplane-custom-theme-vars (theme-tokens-v2.md §4).
    await expect(page.locator("#gameplane-custom-theme-vars")).toBeAttached();
    expect(await rootVar(page, "--accent")).toBe(EMERALD_ACCENT);

    // Click 3 — a surface tone (Midnight, not the default Dark Slate).
    const presetSurface = await rootVar(page, "--surface");
    await page.getByText("Midnight", { exact: true }).click();
    expect(await rootVar(page, "--surface")).not.toBe(presetSurface);

    // Save persists the custom-colors base (persisting is not an adoption
    // click — the colors were already adopted live above).
    await saveThemeSettings(page);
    await expect
      .poll(async () => (await getPrefs(request)).themeType, { timeout: 15_000 })
      .toBe("custom_colors");
    const saved = await getPrefs(request);
    expect(saved.customColors?.accent.toLowerCase()).toBe(EMERALD_ACCENT);
    await expect(page.locator("html")).toHaveAttribute("data-theme-type", "custom_colors");

    // The base survives a reload.
    await page.reload();
    await page.waitForLoadState("domcontentloaded");
    await expect(page.getByRole("heading", { name: /theme & appearance/i })).toBeVisible({
      timeout: 15_000,
    });
    await expect(page.locator("html")).toHaveAttribute("data-theme-type", "custom_colors");
    await expect.poll(() => rootVar(page, "--accent")).toBe(EMERALD_ACCENT);

    // D4: Appearance mode is disabled while Custom colors is active — light/dark
    // instead follows the surface tone's own brightness (spec.md D4).
    const modeGroup = page.getByRole("group", { name: "Appearance mode" });
    await expect(modeGroup.getByRole("button", { name: "Light", exact: true })).toBeDisabled();
    await expect(modeGroup).toHaveAccessibleDescription("Set by your surface color");

    for (const [surfaceLabel, expectDark] of [
      ["Dark Slate", true],
      ["Crisp Light", false],
    ] as const) {
      await page.getByText(surfaceLabel, { exact: true }).click();
      await expect(page.locator("html")).toHaveAttribute("data-theme", expectDark ? "dark" : "light");
      expect(await rootVar(page, "--accent")).toBe(EMERALD_ACCENT);
      const accentForeground = await rootVar(page, "--accent-foreground");
      expect(["#ffffff", "#000000"], `accent-foreground on ${surfaceLabel}`).toContain(accentForeground);
    }
    // Restore Dark Slate so the rest of the test (reload/reset assertions) is unaffected.
    await page.getByText("Dark Slate", { exact: true }).click();

    // Reset to Defaults: one confirmation, then the preset base is back and
    // the custom colors are deleted (FR-012 — reset is the only deletion).
    await page.getByRole("button", { name: /reset to defaults/i }).click();
    await confirmResetDialog(page);
    await expect
      .poll(async () => (await getPrefs(request)).themeType, { timeout: 15_000 })
      .toBe("preset");
    const afterReset = await getPrefs(request);
    expect(afterReset.customColors).toBeNull();
    expect(afterReset.customCssEnabled).toBe(false);
    await expect(page.locator("#gameplane-custom-theme-vars")).toHaveCount(0);
    await expect(page.locator("html")).toHaveAttribute("data-theme-type", "preset");
    const accentAfterReset = await rootVar(page, "--accent");
    expect([PINK_DARK_ACCENT, PINK_LIGHT_ACCENT]).toContain(accentAfterReset);
  });

  // -------------------------------------------------------------------
  // T033 / US4: custom CSS overlay, safe mode, export/import
  // (quickstart.md §5.2.4–§5.2.7).

  test("custom CSS overlay wins over the base and persists across Pink↔Legacy switches (FR-007)", async ({
    page,
    request,
  }) => {
    await gotoApp(page);
    await openThemeSettings(page);
    await setAppearance(page, request, "dark");

    // Enable the overlay and save the probe rule through the editor UI.
    await setOverlayEnabled(page, request, true);
    await cssEditor(page).fill(OVERLAY_RULE);
    await saveThemeSettings(page);
    await expect
      .poll(async () => (await getPrefs(request)).customCssEnabled, { timeout: 15_000 })
      .toBe(true);
    await expectOverlayApplied(page);

    // Switch the base preset: the untargeted --accent token follows the base
    // while the overlay rule (and its injection position) persists.
    await selectPreset(page, request, "legacy");
    expect(await rootVar(page, "--accent")).toBe(LEGACY_DARK_ACCENT);
    await expectOverlayApplied(page);
    // FR-012: the preset switch must not null the stored stylesheet either.
    expect((await getPrefs(request)).customCss).toContain("lime");

    await selectPreset(page, request, "pink");
    expect(await rootVar(page, "--accent")).toBe(PINK_DARK_ACCENT);
    await expectOverlayApplied(page);
  });

  test("stored CSS is retained when the overlay toggle is switched off and on (FR-012)", async ({
    page,
    request,
  }) => {
    await gotoApp(page);
    await openThemeSettings(page);

    await setOverlayEnabled(page, request, false);
    await expect(page.locator("#gameplane-custom-css")).toHaveCount(0);
    let prefs = await getPrefs(request);
    expect(prefs.customCssEnabled).toBe(false);
    expect(prefs.customCss).toContain("lime");

    await setOverlayEnabled(page, request, true);
    await expect(page.locator("#gameplane-custom-css")).toBeAttached();
    prefs = await getPrefs(request);
    expect(prefs.customCss).toContain("lime");
  });

  test("the CSS editor rejects @import with the offending rule named and does not save it", async ({
    page,
    request,
  }) => {
    await gotoApp(page);
    await openThemeSettings(page);
    // Precondition from the overlay scenario: the lime rule is live.
    await expect(page.locator("#gameplane-custom-css")).toBeAttached();

    // With invalid CSS in the draft the Save button is disabled (the editor
    // blocks the save client-side), so the error must surface on input.
    await cssEditor(page).fill(POISON_IMPORT_RULE);

    // FR-013 + theme-ui.md §3.3: an inline error names the offending rule.
    await expect(page.getByText(EDITOR_IMPORT_REJECTION)).toBeVisible({ timeout: 10_000 });
    await expect(page.getByText(/@import/i).first()).toBeVisible();

    // The stylesheet is not saved — neither server-side nor in the DOM.
    const prefs = await getPrefs(request);
    expect(prefs.customCss).toContain("lime");
    expect(prefs.customCss).not.toContain("@import");
    expect(await page.locator("#gameplane-custom-css").textContent()).toContain("lime");

    // Restore the probe rule for the scenarios that follow (matches the
    // stored value, so no save is needed — Save is disabled while clean).
    await cssEditor(page).fill(OVERLAY_RULE);
    await expect(page.getByText(EDITOR_IMPORT_REJECTION)).toHaveCount(0);
    await expect
      .poll(async () => (await getPrefs(request)).customCss, { timeout: 15_000 })
      .toContain("lime");
  });

  test("the CSS editor rejects a stylesheet over the 32,768-byte limit and does not save it", async ({
    page,
    request,
  }) => {
    await gotoApp(page);
    await openThemeSettings(page);

    // FR-013 + quickstart.md §5.2.5: ~33 KB of otherwise valid CSS trips the
    // size limit client-side, so the error surfaces on input.
    const oversized = `/* ${"a".repeat(33 * 1024)} */\n${OVERLAY_RULE}`;
    await cssEditor(page).fill(oversized);
    await expect(page.getByText(/exceeds the 32,768-byte limit/i)).toBeVisible({ timeout: 10_000 });

    // Nothing oversized reaches the server.
    const prefs = await getPrefs(request);
    expect(prefs.customCss ?? "").toContain("lime");
    expect((prefs.customCss ?? "").length).toBeLessThan(32_768);

    // Restore the probe rule for the scenarios that follow (matches the
    // stored value, so no save is needed).
    await cssEditor(page).fill(OVERLAY_RULE);
    await expect(page.getByText(/exceeds the 32,768-byte limit/i)).toHaveCount(0);
  });

  test("safe mode via ?safe-mode=1 suspends the overlay, shows the banner, and restores on reload", async ({
    page,
    request,
  }) => {
    await gotoApp(page);
    // Precondition: overlay enabled with the lime rule (server-side state
    // persisted by the earlier scenarios).
    await expect(page.locator("#gameplane-custom-css")).toBeAttached();

    await page.goto("/?safe-mode=1");
    await page.waitForLoadState("domcontentloaded");

    const html = page.locator("html");
    await expect(html).toHaveAttribute("data-custom-css", "off");
    await expect(page.locator("#gameplane-custom-css")).toHaveCount(0);
    // The base theme still renders (Pink), only the overlay is suspended.
    await expect(html).toHaveAttribute("data-theme-preset", "pink");
    expect(await topBarBorderBottomColor(page)).not.toBe(LIME_RGB);

    // SafeModeBanner (theme-ui.md §4 / design DAz77), rendered as an alert.
    await expect(page.getByText(/safe mode active/i)).toBeVisible({ timeout: 15_000 });
    await expect(page.getByText(/custom css is suspended/i)).toBeVisible();

    // The stored stylesheet is untouched — only its injection is suspended.
    expect((await getPrefs(request)).customCss).toContain("lime");

    // Safe mode is session-only: drop the parameter and the overlay is back.
    await page.goto("/");
    await page.waitForLoadState("domcontentloaded");
    await expect(page.locator("html")).toHaveAttribute("data-custom-css", "on");
    await expect(page.locator("#gameplane-custom-css")).toBeAttached();
    await expect(page.getByText(/safe mode active/i)).toHaveCount(0);
    expect(await topBarBorderBottomColor(page)).toBe(LIME_RGB);
  });

  test.describe("safe-mode sign-in (isolated session)", () => {
    // The shared storageState must stay signed out here: this flow exercises
    // the real login form, and logging in from the shared context would both
    // spend the per-username budget and prove nothing.
    test.use({ storageState: { cookies: [], origins: [] } });

    test("the login-page safe-mode link signs in with the overlay suspended and the banner shown", async ({
      page,
      playwright,
    }) => {
      const adminUsername =
        process.env.ADMIN_USERNAME ?? process.env.GAMEPLANE_E2E_ADMIN_USERNAME ?? "e2e-admin";
      const adminPassword =
        process.env.ADMIN_PASSWORD ?? process.env.GAMEPLANE_E2E_ADMIN_PASSWORD ?? "any-non-empty";

      // Precondition: the admin account keeps an enabled overlay, set over
      // the API — this test's `request` fixture is unauthenticated by design
      // (isolated storageState), so use a private admin API session.
      const adminApi = await loginUserApi(playwright, adminUsername, adminPassword);
      try {
        const put = await putPrefs(adminApi, {
          themeType: "preset",
          presetId: "pink",
          appearanceMode: "dark",
          customCssEnabled: true,
          customCss: OVERLAY_RULE,
        });
        expect(put.ok(), await put.text().catch(() => "")).toBeTruthy();
      } finally {
        await adminApi.dispose();
      }

      await page.goto("/login");
      await page.waitForLoadState("domcontentloaded");

      // FR-011: the login chrome is always Pink with no custom CSS…
      await expect(page.locator("html")).toHaveAttribute("data-theme-preset", "pink", {
        timeout: 15_000,
      });
      await expect(page.locator("#gameplane-custom-css")).toHaveCount(0);
      // …and it carries the safe-mode entry point (theme-ui.md §1.3). The
      // landed Login.tsx renders it as a link whose click runs the sign-in.
      const safeModeEntry = page
        .getByRole("link", { name: /sign in with safe mode/i })
        .or(page.getByRole("button", { name: /sign in with safe mode/i }));
      await expect(safeModeEntry).toBeVisible();

      await page.getByRole("textbox", { name: /email or username/i }).fill(adminUsername);
      await page.locator('input[name="password"]').fill(adminPassword);
      // The link submits the same credentials flow but carries the safe-mode
      // flag into the authenticated session.
      await safeModeEntry.click();
      await page.waitForURL((u) => !u.pathname.startsWith("/login"), { timeout: 15_000 });

      const html = page.locator("html");
      await expect(html).toHaveAttribute("data-custom-css", "off");
      await expect(page.locator("#gameplane-custom-css")).toHaveCount(0);
      await expect(page.getByText(/safe mode active/i)).toBeVisible({ timeout: 15_000 });
      // The base theme still renders; only the overlay is suspended.
      await expect(html).toHaveAttribute("data-theme-preset", "pink");
      expect(await topBarBorderBottomColor(page)).not.toBe(LIME_RGB);
    });
  });

  test("export → import round-trips the full setup on a second account", async ({
    page,
    request,
    browser,
    playwright,
  }) => {
    await gotoApp(page);
    await openThemeSettings(page);

    // Build the full configuration to transfer: Legacy preset base with
    // Emerald custom colors + enabled lime overlay, dark appearance. The
    // custom colors must be activated through the preset card first
    // (theme-ui.md §3.2).
    await setAppearance(page, request, "dark");
    await selectPreset(page, request, "legacy");
    await activateCustomColors(page);
    await page.getByRole("button", { name: /^emerald$/i }).click();
    await page.getByText("Dark Slate", { exact: true }).click();
    await cssEditor(page).fill(OVERLAY_RULE);
    await setOverlayEnabled(page, request, true);
    // A retried run of this test can already have every field above saved
    // from its earlier attempt (setOverlayEnabled's own save covers the
    // full draft), leaving nothing dirty here — Save would then stay
    // disabled and an unconditional click would hang until the test
    // timeout, so only click it while there's something to persist.
    await saveIfDirty(page);
    await expect
      .poll(
        async () => {
          const p = await getPrefs(request);
          return `${p.themeType}/${p.presetId}/${p.customCssEnabled}`;
        },
        { timeout: 15_000 },
      )
      .toBe("custom_colors/legacy/true");

    // Export: the versioned gameplane-theme v1 document (theme-export.md §2).
    const downloadPromise = page.waitForEvent("download");
    await page.getByRole("button", { name: /download gameplane-theme\.json/i }).click();
    const download = await downloadPromise;
    const doc = JSON.parse(readFileSync(await download.path(), "utf8")) as ThemeExportDoc;
    expect(doc.format).toBe("gameplane-theme");
    expect(doc.version).toBe(1);
    expect(doc.preferences.themeType).toBe("custom_colors");
    expect(doc.preferences.presetId).toBe("legacy");
    expect(doc.preferences.appearanceMode).toBe("dark");
    expect(doc.preferences.customColors?.accent.toLowerCase()).toBe(EMERALD_ACCENT);
    expect(doc.preferences.customCssEnabled).toBe(true);
    expect(doc.preferences.customCss).toContain("lime");

    // Import on the second account: paste, preview, apply.
    const bApi = await loginUserApi(playwright, roundTripUser.username, roundTripUser.password);
    try {
      const bCtx = await browserContextFor(bApi, browser);
      try {
        const bPage = await bCtx.newPage();
        await openThemeSettings(bPage);
        await bPage.getByRole("textbox", { name: /paste theme json/i }).fill(
          JSON.stringify(doc, null, 2),
        );

        // Preview before applying (theme-ui.md §3.4). Scoped to the "Preset:
        // <name>" summary line specifically — a bare "legacy orange" match
        // also resolves the Preset theme radiogroup's own "Legacy Orange"
        // card label underneath the import panel, tripping strict mode.
        await expect(bPage.getByText(/import preview/i)).toBeVisible({ timeout: 10_000 });
        await expect(bPage.getByText(/preset:\s*legacy orange/i)).toBeVisible();

        await bPage.getByRole("button", { name: /apply import/i }).click();

        // The stored setup reproduces wholesale (no merge).
        await expect
          .poll(async () => (await getPrefs(bApi)).presetId, { timeout: 15_000 })
          .toBe("legacy");
        const imported = await getPrefs(bApi);
        expect(imported.themeType).toBe("custom_colors");
        expect(imported.customColors?.accent.toLowerCase()).toBe(EMERALD_ACCENT);
        expect(imported.customCssEnabled).toBe(true);
        expect(imported.customCss).toContain("lime");

        // …and the dashboard reproduces it: custom base + overlay + preset.
        await bPage.goto("/");
        await expect(bPage.getByRole("navigation", { name: "Primary" })).toBeVisible({
          timeout: 20_000,
        });
        const html = bPage.locator("html");
        await expect(html).toHaveAttribute("data-theme-preset", "legacy");
        await expect(html).toHaveAttribute("data-theme-type", "custom_colors");
        await expect(html).toHaveAttribute("data-custom-css", "on");
        await expect(html).toHaveAttribute("data-theme", "dark");
        await expect.poll(() => rootVar(bPage, "--accent")).toBe(EMERALD_ACCENT);
        await expectOverlayApplied(bPage);
      } finally {
        await bCtx.close();
      }
    } finally {
      await bApi.dispose();
    }
  });

  test("an import hand-edited to contain @import is rejected server-side with a 400", async ({
    browser,
    playwright,
  }) => {
    const bApi = await loginUserApi(playwright, roundTripUser.username, roundTripUser.password);
    try {
      // A valid gameplane-theme v1 document whose customCss was hand-edited
      // to smuggle in an @import. Client import validation (theme-export.ts)
      // checks shape/enums/size — not FR-013 — so the PUT must reach the
      // server and be rejected there (contracts/theme-export.md §4.4).
      const poisoned: ThemeExportDoc = {
        format: "gameplane-theme",
        version: 1,
        preferences: {
          themeType: "preset",
          presetId: "pink",
          appearanceMode: "dark",
          customColors: null,
          customCssEnabled: true,
          customCss: POISON_IMPORT_RULE,
        },
      };

      const bCtx = await browserContextFor(bApi, browser);
      try {
        const bPage = await bCtx.newPage();
        await openThemeSettings(bPage);
        await bPage
          .getByRole("textbox", { name: /paste theme json/i })
          .fill(JSON.stringify(poisoned));

        const putPromise = bPage.waitForResponse(
          (res) =>
            res.url().includes("/users/me/preferences") && res.request().method() === "PUT",
          { timeout: 15_000 },
        );
        await bPage.getByRole("button", { name: /apply import/i }).click();
        const put = await putPromise;
        expect(put.status(), await put.text().catch(() => "")).toBe(400);

        // The server message naming the rule is surfaced inline…
        await expect(bPage.getByText(SERVER_IMPORT_REJECTION)).toBeVisible({ timeout: 10_000 });
        // …and the account's stored setup is untouched.
        const prefs = await getPrefs(bApi);
        expect(prefs.presetId).toBe("legacy");
        expect(prefs.themeType).toBe("custom_colors");
        expect(prefs.customCss).toContain("lime");
      } finally {
        await bCtx.close();
      }
    } finally {
      await bApi.dispose();
    }
  });

  test("from the banner, Open Appearance Settings leads to Reset to Defaults, which deletes the custom styling", async ({
    page,
    request,
  }) => {
    await gotoApp(page);
    // Precondition: the full setup from the export scenario is still active
    // (Legacy + Emerald custom colors + lime overlay).
    await expect(page.locator("#gameplane-custom-css")).toBeAttached();

    await page.goto("/?safe-mode=1");
    await page.waitForLoadState("domcontentloaded");
    await expect(page.getByText(/safe mode active/i)).toBeVisible({ timeout: 15_000 });

    const openSettings = page
      .getByRole("link", { name: /open appearance settings/i })
      .or(page.getByRole("button", { name: /open appearance settings/i }));
    await openSettings.click();
    await page.waitForURL((u) => new URL(u).pathname === "/settings/theme", { timeout: 15_000 });

    await page.getByRole("button", { name: /reset to defaults/i }).click();
    await confirmResetDialog(page);

    // Reset is the only operation that deletes stored customs (FR-012).
    await expect
      .poll(async () => (await getPrefs(request)).themeType, { timeout: 15_000 })
      .toBe("preset");
    const prefs = await getPrefs(request);
    expect(prefs.customColors).toBeNull();
    expect(prefs.customCss).toBeNull();
    expect(prefs.customCssEnabled).toBe(false);
    await expect(page.locator("html")).toHaveAttribute("data-theme-type", "preset");
    await expect(page.locator("#gameplane-custom-theme-vars")).toHaveCount(0);
    await expect(page.locator("#gameplane-custom-css")).toHaveCount(0);
    await expect(page.locator("html")).toHaveAttribute("data-custom-css", "off");
  });
});
