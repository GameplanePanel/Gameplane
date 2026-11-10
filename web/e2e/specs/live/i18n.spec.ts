import { test, expect } from "@playwright/test";
import type { APIRequestContext, Page } from "@playwright/test";
import { apiSend, loginIfNeeded } from "./_seed";

// Live: spec 019 T037, language switching end to end on the real backend.
// Covers:
//   A. Switching language applies in place on the same page, without a reload.
//      Keeping typed input across a switch is covered by the unit test
//      web/src/lib/i18n/I18nProvider.test.tsx ("switches language without
//      unmounting a stateful child").
//   B. A reload and a new browser context keep French, with no English flash.
//   C. The logged-out login and share pages offer a picker that is remembered
//      per browser, and a fresh logged-out context starts in English.
//   D. A 0%-complete fixture language shows English text everywhere (SC-008).
//      It runs only when the live web server was started with
//      VITE_I18N_FIXTURES=1; otherwise it is reported as skipped.
//
// The specs share one admin account, and the language choice is saved to that
// account. The suite runs serially and resets the account language to null
// when it finishes, so later live specs see English again.

const ADMIN_STATE = "./e2e/.auth/storage.json";
// Empty storage state for logged-out contexts (no cookies, no origins).
const EMPTY_STATE: { cookies: []; origins: [] } = { cookies: [], origins: [] };

// Window extensions used as markers across a reload or a language switch.
type I18nWindow = Window & { __i18nMark?: number; __englishSeen?: boolean };

// resetAccountLanguage clears the admin's saved language, so the shared account
// falls back to the browser language and then the instance default.
async function resetAccountLanguage(request: APIRequestContext): Promise<void> {
  const res = await apiSend(request, "PUT", "/users/me/language", { language: null });
  if (!res.ok()) {
    throw new Error(`reset account language: ${res.status()} ${await res.text().catch(() => "")}`);
  }
}

// pickFrench opens the compact picker on a logged-out page and chooses
// Français. The trigger is named "Language" in English and "Langue" in French.
async function pickFrenchFromCompactPicker(page: Page): Promise<void> {
  await page.getByRole("button", { name: /^(Langue|Language)$/ }).click();
  // React Aria labels the menu by its trigger (aria-labelledby wins over
  // aria-label), so pick the item by its own name.
  await page.getByRole("menuitem", { name: "Français" }).click();
}

// watchForEnglish records whether the English Servers h1 ever shows in main,
// from before React renders. Install it before the first goto. This title has
// a French translation; newer subtitles can legitimately fall back to English.
async function watchForEnglish(page: Page): Promise<void> {
  await page.addInitScript(() => {
    const w = window as I18nWindow;
    w.__englishSeen = false;
    // Observe from the init script itself: module scripts can render before
    // DOMContentLoaded, so waiting for it could miss a short English flash.
    const check = () => {
      if (document.querySelector("main h1")?.textContent?.trim() === "Servers") {
        w.__englishSeen = true;
      }
    };
    new MutationObserver(check).observe(document, { childList: true, subtree: true, characterData: true });
    check();
  });
}

test.describe("live: i18n", () => {
  test.skip(
    process.env.GAMEPLANE_E2E_TARGET !== "live",
    "live-only — language switching is covered by the mock specs",
  );

  // Every test changes the admin's language, so the order matters.
  test.describe.configure({ mode: "serial" });

  test.afterAll(async ({ playwright }) => {
    const ctx = await playwright.request.newContext({
      baseURL: "http://localhost:5173",
      storageState: ADMIN_STATE,
    });
    try {
      await resetAccountLanguage(ctx);
    } finally {
      await ctx.dispose();
    }
  });

  test("switching language applies in place without a reload", async ({ page }) => {
    // One tab, no second page and no reconnect replay: the switch is made on
    // /settings/theme and the marker shows the page was never reloaded.
    await page.goto("/settings/theme");
    await page.waitForLoadState("domcontentloaded");
    await loginIfNeeded(page);

    const french = page.getByRole("radio", { name: /Français/ });
    await expect(french).toBeVisible({ timeout: 15_000 });
    await page.evaluate(() => {
      (window as I18nWindow).__i18nMark = 1;
    });
    // HeroUI's Radio hides the input under its decorative control, which
    // intercepts a pointer click; click the visible label text instead.
    await page.getByText("Français", { exact: true }).click();

    const html = page.locator("html");
    await expect(html).toHaveAttribute("lang", "fr", { timeout: 15_000 });
    await expect(html).toHaveAttribute("dir", "ltr");
    await expect(html).toHaveAttribute("data-i18n-loaded", "fr");
    // The personal Language card title is "Language" in English and "Langue" in French.
    await expect(page.getByText("Langue", { exact: true }).first()).toBeVisible();
    expect(await page.evaluate(() => (window as I18nWindow).__i18nMark)).toBe(1);

    // Client-side navigation to Servers keeps the marker and shows French there.
    await page.getByRole("link", { name: "Serveurs" }).first().click();
    await expect(page).toHaveURL(/\/servers$/);
    await expect(page.getByRole("main").getByRole("heading", { level: 1, name: "Serveurs", exact: true })).toBeVisible({ timeout: 15_000 });
    expect(await page.evaluate(() => (window as I18nWindow).__i18nMark)).toBe(1);
  });

  test("reload and a new browser context keep French without an English flash", async ({ page, browser }) => {
    // Watch the translated page title from before React renders. An English
    // Servers h1 indicates a language flash even if the subtitle uses fallback.
    await watchForEnglish(page);

    await page.goto("/servers");
    await page.waitForLoadState("domcontentloaded");
    await loginIfNeeded(page);

    await page.reload({ waitUntil: "domcontentloaded" });
    await expect(page.locator("html")).toHaveAttribute("lang", "fr");
    await expect(page.getByRole("main").getByRole("heading", { level: 1, name: "Serveurs", exact: true })).toBeVisible({ timeout: 15_000 });
    expect(await page.evaluate(() => (window as I18nWindow).__englishSeen)).toBe(false);

    // A new browser context has no local storage, so the account choice decides.
    const ctx2 = await browser.newContext({ storageState: ADMIN_STATE });
    try {
      const p2 = await ctx2.newPage();
      await watchForEnglish(p2);
      await p2.goto("/servers");
      await p2.waitForLoadState("domcontentloaded");
      await expect(p2.locator("html")).toHaveAttribute("lang", "fr", { timeout: 15_000 });
      await expect(p2.getByRole("main").getByRole("heading", { level: 1, name: "Serveurs", exact: true })).toBeVisible({ timeout: 15_000 });
      expect(await p2.evaluate(() => (window as I18nWindow).__englishSeen)).toBe(false);
    } finally {
      await ctx2.close();
    }
  });

  test.describe("logged out", () => {
    // Logged-out contexts, as login-and-shell.spec.ts does, so the shared admin
    // session is not touched.
    test.use({ storageState: EMPTY_STATE });

    test("login and share pages offer a picker, remembered per browser", async ({ page, browser }) => {
      await page.goto("/login");
      await page.waitForLoadState("domcontentloaded");
      await pickFrenchFromCompactPicker(page);

      await expect(page.locator("html")).toHaveAttribute("lang", "fr");
      await expect(page.getByRole("textbox", { name: /E-mail ou nom d'utilisateur/ })).toBeVisible();
      await expect(page.getByRole("heading", { name: "Connexion", exact: true })).toBeVisible();

      try {
        // The browser choice persists across a reload.
        await page.reload({ waitUntil: "domcontentloaded" });
        await expect(page.locator("html")).toHaveAttribute("lang", "fr");
        const stored = await page.evaluate(() => window.localStorage.getItem("gameplane-language"));
        expect(JSON.parse(stored ?? "null")).toMatchObject({ choice: "fr", applied: "fr" });

        // A fresh logged-out context has no stored choice, so it starts in English.
        const fresh = await browser.newContext({ storageState: EMPTY_STATE });
        try {
          const freshPage = await fresh.newPage();
          await freshPage.goto("/login");
          await expect(freshPage.getByRole("heading", { name: "Sign in", exact: true })).toBeVisible();
          await expect(freshPage.locator("html")).toHaveAttribute("lang", "en");
        } finally {
          await fresh.close();
        }

        // The share page shows the same picker in its invalid-token state.
        await page.goto("/share/e2e-pw-i18n-missing");
        await page.waitForLoadState("domcontentloaded");
        await pickFrenchFromCompactPicker(page);
        await expect(page.locator("html")).toHaveAttribute("lang", "fr");
      } finally {
        await page.evaluate(() => {
          try {
            window.localStorage.removeItem("gameplane-language");
          } catch {
            // Storage can be blocked; the context is discarded at the end anyway.
          }
        });
      }
    });
  });

  test("a 0% fixture language shows English everywhere (SC-008)", async ({ page, request }) => {
    // The API saves only the languages this release ships, so it refuses the
    // web-only fixture language "zz" as an account language ("unknown language:
    // zz"). Use the per-browser choice instead: with no account language, the
    // browser choice decides (FR-004).
    await resetAccountLanguage(request);
    await page.addInitScript(() => {
      try {
        window.localStorage.setItem("gameplane-language", JSON.stringify({ choice: "zz", applied: "zz" }));
      } catch {
        // Storage can be blocked; the lang check below then fails with a clear message.
      }
    });
    try {
      await page.goto("/settings/theme");
      await page.waitForLoadState("domcontentloaded");
      await expect(page.getByRole("radio", { name: /Français/ })).toBeVisible({ timeout: 15_000 });

      // The fixture language exists only when the web server was started with
      // VITE_I18N_FIXTURES=1 (vite.config.ts). The CI live job sets it on the
      // Playwright step, so the subtest runs there. Local runs without it report
      // the subtest as skipped; in CI it fails instead.
      const fixture = page.getByRole("radio", { name: /Fixture/ });
      if ((await fixture.count()) === 0) {
        // CI sets VITE_I18N_FIXTURES=1, so a missing fixture there is a failure.
        if (process.env.CI) {
          throw new Error("zz fixture language missing; CI sets VITE_I18N_FIXTURES=1 on the live web server");
        }
        test.skip(true, "VITE_I18N_FIXTURES is not set for the live web server, so the zz fixture language is absent");
      }

      await expect(page.locator("html")).toHaveAttribute("lang", "zz", { timeout: 15_000 });

      // Every string falls back to English, and no raw message id is shown.
      await page.goto("/servers");
      await page.waitForLoadState("domcontentloaded");
      await expect(page.getByRole("main").getByRole("heading", { level: 1, name: "Servers", exact: true })).toBeVisible({ timeout: 15_000 });
      await expect(page.getByText("Manage your game servers across all authorized clusters.", { exact: true })).toBeVisible();
      const visible = await page.locator("body").innerText();
      expect(visible.split("\n").some((line) => /^web\.[a-z_.]+$/.test(line.trim()))).toBe(false);
    } finally {
      await resetAccountLanguage(request);
    }
  });
});
