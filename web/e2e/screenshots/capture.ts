import { expect, type Locator, type Page } from "@playwright/test";
import path from "node:path";
import { fileURLToPath } from "node:url";
import fs from "node:fs";
import sharp from "sharp";

const here = path.dirname(fileURLToPath(import.meta.url));
const SCREENSHOTS_DIR = here;
const DESIGN_EXPORT_DIR = path.resolve(here, "../../../assets/design-export/screenshots");

/**
 * Capture a screenshot matching the reference frame dimensions and save to web/e2e/screenshots/<id>.png
 *
 * Automatically sizes the viewport to match the reference design frame's dimensions (at 2x scale)
 * so scrollable tall pages (e.g. 1440x1300 in Pencil -> 2880x2600 PNG) and mobile viewports
 * render and capture with exact dimension alignment without scrollbar gutter artifacts.
 */
export async function capture(page: Page, id: string): Promise<void> {
  const screenshotPath = path.join(SCREENSHOTS_DIR, `${id}.png`);
  const refPath = path.join(DESIGN_EXPORT_DIR, `${id}.png`);

  if (fs.existsSync(refPath)) {
    const meta = await sharp(refPath).metadata();
    if (meta.width && meta.height) {
      // Pencil exports frames at 2x scale. Set viewport to match the reference frame.
      const targetWidth = Math.round(meta.width / 2);
      const targetHeight = Math.round(meta.height / 2);
      const current = page.viewportSize();
      if (!current || current.width !== targetWidth || current.height !== targetHeight) {
        await page.setViewportSize({ width: targetWidth, height: targetHeight });
        await page.waitForTimeout(200);
      }
    }
  }

  await page.evaluate(() => {
    const el = document.activeElement as HTMLElement | null;
    if (el && !el.closest('[role="dialog"],[role="listbox"],[role="menu"],[data-open="true"]')) el.blur();
  });
  await page.screenshot({ path: screenshotPath, animations: "disabled" });
}

/**
 * Capture a screenshot of a single element (dialog, drawer, badge, chip, ...) rather
 * than the full page, and save to web/e2e/screenshots/<id>.png.
 *
 * Use this instead of `capture()` when the reference design frame is a tight crop of
 * one component rather than a full viewport — comparing a small element crop against
 * a full-page capture otherwise reads as a near-total diff (mismatched canvas sizes),
 * not a real rendering difference.
 *
 * Waits for web fonts to finish loading before waiting for the target to be visible,
 * scrolls it into view, then screenshots just that element with animations disabled
 * and the text caret hidden so the capture is deterministic.
 */
export async function captureLocator(page: Page, id: string, locator: Locator): Promise<void> {
  const screenshotPath = path.join(SCREENSHOTS_DIR, `${id}.png`);

  await page.evaluate(() => document.fonts.ready.then(() => undefined));
  await expect(locator).toBeVisible();
  await locator.scrollIntoViewIfNeeded();
  await page.evaluate(() => {
    const el = document.activeElement as HTMLElement | null;
    if (el && !el.closest('[role="dialog"],[role="listbox"],[role="menu"],[data-open="true"]')) el.blur();
  });
  // Chrome paints box edges snapped to whole device pixels, but the element
  // screenshot crops the *unsnapped* layout rect rounded outward: a chip at a
  // fractional y is captured with a strip of page background above it
  // (R65Xyx: 338x36 capture vs a 338x34 reference, whole chip 2 device px low,
  // which alone scored 24.91%/44.44%). Trim only fully uniform edge rows and
  // columns — a row holding any border, fill or glyph pixel stops the trim, so
  // this can never eat real content — and never by more than 2 device px, so a
  // genuine size regression still reaches the scale gate.
  const buf = await locator.screenshot({ animations: "disabled", caret: "hide" });
  const before = await sharp(buf).metadata();
  const trimmed = await sharp(buf).trim({ threshold: 0 }).toBuffer({ resolveWithObject: true });
  const overTrimmed =
    (before.width ?? 0) - trimmed.info.width > 2 || (before.height ?? 0) - trimmed.info.height > 2;
  let out = overTrimmed ? buf : trimmed.data;
  const heightAlreadyRemoved = overTrimmed ? 0 : (before.height ?? 0) - trimmed.info.height;

  // A dialog at a fractional y can still start or end with 1-2 CSS px of modal
  // backdrop that the threshold-0 trim above leaves behind, because the
  // backdrop rows are dithered rather than one exact colour (NLDDv rows 0-1 are
  // rgb 72-74,64-66,68-70; E9EEv0 has a 75/76 grey strip top and bottom).
  // Strip rows at the top and bottom only, and only rows whose every pixel is
  // within a small tolerance of that edge's corner pixel. A row holding any
  // border, fill or glyph pixel stops the strip, so real content is never
  // removed. Together with the trim above, never remove more than 2 CSS px
  // (4 device px) per edge, so a genuine size regression still reaches the
  // scale gate.
  //
  // This is specifically a *modal backdrop* artifact: only dialogs (or
  // captures nested inside one) sit on top of a backdrop that can bleed in
  // this way. A non-dialog element crop (a chip, a badge, an alert banner)
  // has no backdrop, and its own top/bottom edge row is frequently a solid
  // fill or border that legitimately matches its corner pixel — stripping it
  // would eat real content (m1hP1j: the alert's own uniform red top/bottom
  // edge was mistaken for backdrop and trimmed, moving the crop out of
  // alignment with the design reference). Gate the strip on the capture
  // actually being a dialog.
  const isDialogCapture = await locator.evaluate(
    (el) => el.closest('[role="dialog"],[role="alertdialog"]') !== null,
  );
  if (isDialogCapture) {
    const NEAR_UNIFORM_TOLERANCE = 6; // per RGB channel, 0-255
    const stripBudget = Math.max(0, 4 - heightAlreadyRemoved); // device px per edge
    const { data: raw, info } = await sharp(out).raw().toBuffer({ resolveWithObject: true });
    const { width, height, channels } = info;
    const rowIsNearUniform = (y: number, refY: number): boolean => {
      const ref = refY * width * channels;
      const rowStart = y * width * channels;
      for (let x = 0; x < width; x++) {
        const i = rowStart + x * channels;
        for (let c = 0; c < 3; c++) {
          if (Math.abs(raw[i + c] - raw[ref + c]) > NEAR_UNIFORM_TOLERANCE) return false;
        }
      }
      return true;
    };
    let top = 0;
    while (top < stripBudget && height - top > 4 && rowIsNearUniform(top, 0)) top++;
    let bottom = 0;
    while (
      bottom < stripBudget &&
      height - top - bottom > 4 &&
      rowIsNearUniform(height - 1 - bottom, height - 1)
    ) {
      bottom++;
    }
    if (top > 0 || bottom > 0) {
      out = await sharp(out).extract({ left: 0, top, width, height: height - top - bottom }).toBuffer();
    }
  }

  await fs.promises.writeFile(screenshotPath, out);
}
