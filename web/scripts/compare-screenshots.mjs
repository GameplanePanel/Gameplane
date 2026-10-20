#!/usr/bin/env node

/**
 * Visual regression comparison script for Gameplane dashboard.
 *
 * Compares Playwright browser captures (web/e2e/screenshots/*.png) against
 * reference baseline screenshots exported from Pencil (assets/design-export/screenshots/*.png).
 *
 * Key features:
 * - Scale normalization: when a capture's width differs from its reference's width
 *   (e.g. a 2880-wide design frame vs. a 1440 browser capture), the capture is resized
 *   to the reference width first (sharp, lanczos3 kernel, aspect ratio preserved,
 *   upscaling capped at 4x so a tiny element crop can't be blown up past recognition),
 *   then any remaining height difference is padded as below. The applied factor is
 *   logged per screen (console + summary.json) even when it is 1 (no scaling needed).
 * - Dimension normalization via sharp padding (anchored at top-left, no distorting stretch)
 * - Subpixel & anti-aliasing tolerance via pixelmatch ({ threshold: 0.15, includeAA: false })
 * - Generation of visual diff highlighting (magenta) and 3-panel composite previews
 * - Configurable global & per-screen diff factor thresholds
 * - Output to console, JSON summary, and GitHub Step Summary ($GITHUB_STEP_SUMMARY)
 *
 * Scale gate (SCALE_ALLOWLIST): the 4%/16% global/block diff thresholds and the
 * 4x4 regional block gate above are deliberately left as-is (maintainer ruling)
 * — this gate is a separate, narrower check on top of them. A capture whose
 * scaleFactor is not exactly 1.000 means its native pixel size doesn't match
 * its reference's width class, and for a full screen that's a real fidelity
 * problem worth failing on. But a handful of ids are intentionally captured
 * as tight element crops (a single badge/chip/dialog via captureLocator())
 * at the component's actual implementation size, while their assets/design-export
 * reference PNG was exported at a different crop size — for those, a
 * non-unity scale factor is the expected, accepted shape of the comparison,
 * not a regression (maintainer ruling: chips/badges keep implementation
 * size). SCALE_ALLOWLIST names exactly those ids, each mapped to the scale
 * factor recorded from the last local run. The gate is a two-sided band: a
 * listed id's scaleFactor must stay within 5% of its recorded value in
 * *either* direction (SCALE_BAND_FRACTION) — both growth and shrinkage past
 * the band fail, since either one means the capture's relationship to its
 * reference crop has changed and the recorded value is stale. A screen not
 * listed here that drifts off scale=1.000 also fails the gate. This applies
 * in both the normal run and --check-expected, since both go through the
 * same per-screen comparison loop below.
 *
 * Reference-alpha mask (REFERENCE_ALPHA_MASK, below): maintainer-flagged,
 * kept behind its own const so it stays easy to find and to turn off. When
 * on, pixels that are fully transparent in the REFERENCE image (alpha < 8)
 * are treated as carrying no design intent and are excluded from BOTH the
 * global and the 4x4 block diff denominators/numerators — i.e. the diff
 * ratio is computed only over reference-opaque pixels, and a block with
 * fewer than 8 opaque reference pixels is skipped from the block-max
 * computation entirely rather than diffing whatever sparse content it has.
 * The 4%/16% thresholds themselves are unchanged; only which pixels count
 * toward them changes. The resulting opaque fraction (opaque reference
 * pixels ÷ canvas pixels) is logged per screen, in summary.json, regardless
 * of whether the mask is on.
 */

import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import process from 'node:process';
import { Buffer } from 'node:buffer';
import sharp from 'sharp';
import pixelmatch from 'pixelmatch';

const __filename = fileURLToPath(import.meta.url);
const __dirname = path.dirname(__filename);
const REPO_ROOT = path.resolve(__dirname, '../..');
const WEB_ROOT = path.resolve(__dirname, '..');

// Default configurations
const DEFAULT_REF_DIR = path.join(REPO_ROOT, 'assets/design-export/screenshots');
const DEFAULT_CURR_DIR = path.join(WEB_ROOT, 'e2e/screenshots');
const DEFAULT_OUT_DIR = path.join(WEB_ROOT, 'test-results/visual-diff');
const DEFAULT_MAX_DIFF_FACTOR = 0.04; // 4.0% maximum allowed global difference
const DEFAULT_MAX_BLOCK_DIFF_FACTOR = 0.16; // 16.0% maximum allowed difference in any 4x4 regional block
const DEFAULT_PIXEL_THRESHOLD = 0.15; // Color delta sensitivity (0 to 1)
const MAX_UPSCALE_FACTOR = 4; // Never scale a capture up by more than 4x, however small it is vs. the reference

// Maintainer-flagged, kept clearly separable: excludes reference-transparent
// pixels from both the global and per-block diff denominators/numerators.
// See the header comment's "Reference-alpha mask" section. Flip this to
// `false` to fall back to the prior behavior (every canvas pixel compared).
const REFERENCE_ALPHA_MASK = true;
const ALPHA_MASK_THRESHOLD = 8; // reference alpha below this = "no design intent"
const ALPHA_MASK_MIN_BLOCK_PIXELS = 8; // a block with fewer opaque reference pixels than this is skipped

// Per-screen threshold overrides where text density, terminal streams, or
// specific layouts have justified rendering variance.
const SCREEN_THRESHOLD_OVERRIDES = {
  // Console/Logs have high text density and live terminal streaming
  Xn5ns: 0.06, // Server Detail — Console
  kPmoo: 0.06, // Server Detail — Logs
  FtdkI: 0.06, // Server Detail — Logs (Failed)
  // Mobile responsive layout (narrow 390px viewport with condensed cards)
  tooKB: 0.06, // Servers — Mobile
  SeizD: 0.06, // Navigation Drawer — Mobile
  // Removable group chips: tiny pills whose glyph rows differ only by text
  // rasterization (Pencil export crisp vs Chrome smoothed). Maintainer
  // ruled these a capture issue and signed off on per-frame overrides
  // (2026-09-19). CI worst case: XL5ZU 8.54%, vStkb 7.81%, uw0dB 11.88%.
  XL5ZU: 0.13, // Removable Group Chip — admin
  vStkb: 0.13, // Removable Group Chip — operator
  uw0dB: 0.13, // Removable Group Chip — viewer
};

const SCREEN_BLOCK_THRESHOLD_OVERRIDES = {
  Xn5ns: 0.25,
  kPmoo: 0.25,
  FtdkI: 0.25,
  tooKB: 0.25,
  SeizD: 0.25,
  // Same three chips, same cause. CI worst case: 28.21% / 31.56% / 35.70%.
  XL5ZU: 0.38,
  vStkb: 0.38,
  uw0dB: 0.38,
};

// Ids maintainer-accepted as component crops captured at implementation size
// rather than at the reference's exact crop dimensions (see the header
// comment's "Scale gate" section). Each value is the scaleFactor recorded
// from the last local run; the gate allows ±5% (SCALE_BAND_FRACTION) around
// it in either direction, not just growth.
const SCALE_ALLOWLIST = {
  // BV5ei, Rwnu3 (Provenance Badge chips): entries removed after the
  // ProvenanceBadge typography/sizing fix — that fix makes rendered size
  // match the design size (~230x34/254x34), so the expected scale factor is
  // at/near 1.0x (the allowlist default) rather than the old, stale factors
  // recorded against the previous, larger chip size.
  R65Xyx: 0.9883, // Provenance Badge (Overridden) — CI-measured post-typography-fix scale
  XL5ZU: 1.023, // Removable Group Chip — Orange (admin)
  vStkb: 1.023, // Removable Group Chip — Violet (operator)
  uw0dB: 1.0349, // Removable Group Chip — Secondary (viewer)
  Kp48V: 0.9821, // Confirm Admin Mapping dialog
  // Revoke Share Link: provisional. Same w-[440px] AlertDialogDialog pin as
  // Kp48V (ConfirmDialog.tsx), so the same 880px-reference / captured-width
  // ratio is expected. The gate only applies when scaleFactor != 1.000, so
  // this entry is inert if the capture lands at exactly 1. Re-record from the
  // first CI run.
  S7SCDc: 0.9821,
  // zhLZN: entry removed (maintainer-approved re-record). Its reference is
  // now cropped to the opaque panel via REFERENCE_CROP_ALLOWLIST, so the
  // cropped 880px reference and the 880px captureLocator() crop match and
  // scaleFactor is exactly 1. The old 1.3125 no longer matched CI's measured
  // 1.1455 (= 1008px shadow-inclusive export / 880px capture).
};

// Ids whose assets/design-export/screenshots/<id>.png includes the frame's outer
// drop shadow as transparent bleed around the opaque panel (Pencil exports
// the shadow's bounding box). A captureLocator() element crop has no such
// margin, so the reference is cropped to its opaque panel before the scale
// factor is computed and before diffing. Rect is sharp .extract() shape
// { left, top, width, height } in the PNG's raw pixels, taken from the
// alpha >= 250 bounding box. Re-measure it whenever the id is re-exported
// or its shadow/size changes.
const REFERENCE_CROP_ALLOWLIST = {
  // Backup Detail Drawer: 440x760 panel at 2x; shadow offset x -12 / blur 32 /
  // spread -8 leaves 44 CSS px bleed left, 20 right, 32 top and bottom (bbox
  // (88,64)-(968,1584) => 880x1520).
  zhLZN: { left: 88, top: 64, width: 880, height: 1520 },
  // Add module source dialog: 480x882 panel at 2x, bounding box re-verified
  // via PIL alpha>=250 scan of assets/design-export/screenshots/DMnEi.png (now
  // 1088x1898, bbox (64,40)-(1024,1810) => 960x1770).
  DMnEi: { left: 64, top: 40, width: 960, height: 1770 },
  // Create Share Link dialog: feature 017 / OD-9's redesign deleted the
  // "Maximum 90 days" helper line below the expiry Select, shrinking the
  // panel. This rect went stale at that re-export (it still cited the
  // round-10 1088x772 export) and would have cropped 40 rows of empty canvas
  // into the reference, scoring them as a full-width mismatch, had it reached
  // CI unfixed. Bounding box re-verified via PIL alpha>=250 scan of
  // assets/design-export/screenshots/atqRh.png (now 1088x732, bbox
  // (64,40)-(1024,644) => 960x604).
  atqRh: { left: 64, top: 40, width: 960, height: 604 },
  // Create Share Link dialog, "No expiry" state (OD-9 T024/T025 new frame):
  // same 480px-wide panel as atqRh but with the FR-002 warning line
  // ("This link works until you revoke it.") in place of the custom-date
  // fields, one text row taller than atqRh. Bounding box re-verified via PIL
  // alpha>=250 scan of assets/design-export/screenshots/tr6cE.png (1088x772, bbox
  // (64,40)-(1024,684) => 960x644).
  tr6cE: { left: 64, top: 40, width: 960, height: 644 },
  // Create Share Link dialog, "Custom" date state (OD-9 T024/T025 new
  // frame): adds the "Expires on" date field plus the OD-6 long-lived
  // warning line, two rows taller than atqRh. Bounding box re-verified via
  // PIL alpha>=250 scan of assets/design-export/screenshots/oPF1n.png (1088x892,
  // bbox (64,40)-(1024,804) => 960x764).
  oPF1n: { left: 64, top: 40, width: 960, height: 764 },
  // Share Link Created dialog: OD-27 round-12 (qzcst lineHeight 1.4286,
  // Hp206 padding 14, SICns padding [11,14]) re-export grew the panel by the
  // net +3 CSS px those three overrides add; bounding box re-verified via PIL
  // alpha>=250 scan of assets/design-export/screenshots/VM7ro.png (now 1088x816,
  // bbox (64,40)-(1024,728) => 960x688, which is exactly the browser
  // capture's 960x688 — the two halves now agree on height).
  VM7ro: { left: 64, top: 40, width: 960, height: 688 },
  // Revoke Share Link dialog: OD-26 round-10 HARNESS clause. Design side is
  // unchanged (S7SCDc references master WwNlX, not touched this round);
  // opaque-panel bbox re-verified via PIL alpha>=250 scan of
  // assets/design-export/screenshots/S7SCDc.png (1008x508, bbox
  // (64,40)-(944,420) => 880x380). Per OD-26, captureLocator() keeps 2 CSS px
  // (4 device px) of modal backdrop in the browser capture and capture.ts is
  // intentionally left unchanged — the reference crop absorbs the difference
  // instead. The round-10 rect assumed that backdrop was split 1 CSS px per
  // edge and used top 38, but the capture measures it all at the bottom: the
  // 880x384 browser capture carries its panel at rows 0-379 (0 device px of
  // backdrop above, 4 below), so a top-38 crop put the reference panel at rows
  // 2-381 and mis-registered every glyph by 2 device px (CI: 5.72% global,
  // over the 4% gate). Registering the crop on the panel's own top edge
  // (top 40, height still 384 => bbox (64,40)-(944,424)) re-aligns the two
  // panels: locally re-measured at 2.91% global / 5.67% block. Note the 4
  // extra rows below the panel are NOT free: measured alpha 46-98 across
  // x 64-943, all above ALPHA_MASK_THRESHOLD (8), so REFERENCE_ALPHA_MASK
  // keeps that shadow gradient in both the numerator and the denominator
  // and it is diffed against the browser's backdrop. It costs ~1.04% of the
  // 880x384 area, concentrated in the two bottom blocks, and the 2.91% /
  // 5.67% above already includes it.
  S7SCDc: { left: 64, top: 40, width: 880, height: 384 },
};

// Allowed deviation from an allowlisted id's recorded scaleFactor, in either
// direction (see the header comment's "Scale gate" section).
const SCALE_BAND_FRACTION = 0.05;

// Tolerance for float rounding when comparing a scaleFactor against 1.000 or
// against an allowlisted band edge.
const SCALE_EPSILON = 0.0005;

// Screens currently expected to be captured by shipped slices (Slice 1 + Slice 2a per contracts/screen-verification.md).
// Reconciled against current captures when --check-expected is enabled.
const DEFAULT_EXPECTED_SCREENS = [
  // Slice 1: Shell + Login (7 screens)
  'N1GkB', 'jmoi3', 'ljdA5', 'N13Xud', 'j24cXg', 'tooKB', 'SeizD',
  // Slice 2a: Servers list + Server Detail core tabs (12 screens)
  'F9pUrx', 'EZFW0', 'Hy9r0', 'TE2jI', 'IzuY2', 'o4LH8W',
  'P08Uw', 'Xn5ns', 'kPmoo', 'FtdkI', 'Burtr', 'dPP50',
];

/**
 * Parses and validates a ratio/percentage argument ensuring it is a finite number between 0 and 1.
 *
 * @param {string} valStr Raw string value from CLI argument or environment variable
 * @param {string} name Flag or environment variable name for error reporting
 * @returns {number} Parsed float ratio in range [0, 1]
 */
function parseRatio(valStr, name) {
  const trimmed = valStr.trim();
  const num = Number(trimmed);
  if (!Number.isFinite(num) || num < 0 || num > 1) {
    console.error(`Error: Invalid ${name}: "${valStr}". Expected a finite number from 0 through 1 (e.g. 0.04 for 4%).`);
    process.exit(1);
  }
  return num;
}

/**
 * Parses command line arguments and environment variables into comparison options.
 *
 * @returns {object} Configured comparison options
 */
function parseArgs() {
  const args = process.argv.slice(2);
  const options = {
    refDir: DEFAULT_REF_DIR,
    currDir: DEFAULT_CURR_DIR,
    outDir: DEFAULT_OUT_DIR,
    maxDiffFactor: DEFAULT_MAX_DIFF_FACTOR,
    maxBlockDiffFactor: DEFAULT_MAX_BLOCK_DIFF_FACTOR,
    pixelThreshold: DEFAULT_PIXEL_THRESHOLD,
    allowMissing: false,
    checkExpected: false,
    expectedScreens: null,
  };

  for (const arg of args) {
    if (arg.startsWith('--ref-dir=')) {
      options.refDir = path.resolve(process.cwd(), arg.slice('--ref-dir='.length));
    } else if (arg.startsWith('--curr-dir=')) {
      options.currDir = path.resolve(process.cwd(), arg.slice('--curr-dir='.length));
    } else if (arg.startsWith('--out-dir=')) {
      options.outDir = path.resolve(process.cwd(), arg.slice('--out-dir='.length));
    } else if (arg.startsWith('--threshold=')) {
      options.maxDiffFactor = parseRatio(arg.slice('--threshold='.length), '--threshold');
    } else if (arg.startsWith('--block-threshold=')) {
      options.maxBlockDiffFactor = parseRatio(arg.slice('--block-threshold='.length), '--block-threshold');
    } else if (arg.startsWith('--pixel-threshold=')) {
      options.pixelThreshold = parseRatio(arg.slice('--pixel-threshold='.length), '--pixel-threshold');
    } else if (arg === '--allow-missing') {
      options.allowMissing = true;
    } else if (arg === '--check-expected') {
      options.checkExpected = true;
    } else if (arg.startsWith('--expected-screens=')) {
      options.checkExpected = true;
      options.expectedScreens = arg.slice('--expected-screens='.length).split(',').map((s) => s.trim()).filter(Boolean);
    }
  }

  // Environment variable overrides
  if (process.env.VISUAL_DIFF_THRESHOLD) {
    options.maxDiffFactor = parseRatio(process.env.VISUAL_DIFF_THRESHOLD, 'VISUAL_DIFF_THRESHOLD');
  }
  if (process.env.VISUAL_DIFF_BLOCK_THRESHOLD) {
    options.maxBlockDiffFactor = parseRatio(process.env.VISUAL_DIFF_BLOCK_THRESHOLD, 'VISUAL_DIFF_BLOCK_THRESHOLD');
  }

  return options;
}

/**
 * Computes the scale factor to apply to a capture so its width matches the reference
 * width, capped so an undersized capture (e.g. a tiny element crop) is never upscaled
 * by more than MAX_UPSCALE_FACTOR. Downscaling (capture wider than reference) is never
 * capped. Returns 1 when no source/target width is known or the widths already match.
 */
function computeScaleFactor(srcWidth, refWidth) {
  if (!srcWidth || !refWidth || srcWidth === refWidth) {
    return 1;
  }
  const rawScale = refWidth / srcWidth;
  return Math.min(rawScale, MAX_UPSCALE_FACTOR);
}

/**
 * Loads an image and, if scale !== 1, resizes it (lanczos3, aspect-ratio preserved,
 * width-driven) to correct for a reference/capture size class mismatch. Returns the
 * raw RGBA pixel buffer plus sharp's own reported dimensions for the result — never
 * a manually-recomputed width/height, since sharp's aspect-ratio rounding for the
 * resized height can land a pixel off from a naive `width * (height/width)` guess.
 */
async function loadAndScaleImage(filePath, scale) {
  let pipeline = sharp(filePath).ensureAlpha();

  if (scale !== 1) {
    const meta = await sharp(filePath).metadata();
    const scaledWidth = Math.max(1, Math.round((meta.width || 0) * scale));
    pipeline = pipeline.resize({ width: scaledWidth, kernel: sharp.kernel.lanczos3 });
  }

  const { data, info } = await pipeline.raw().toBuffer({ resolveWithObject: true });
  return { data, width: info.width, height: info.height, channels: info.channels };
}

/**
 * Pads a raw RGBA buffer to the target canvas by extending at bottom and right
 * (top-left aligned, no distorting stretch). Finishes with an `extract` clamp to
 * exactly targetWidth x targetHeight — `extend` only ever adds pixels, so if the
 * source is already >= the target on either axis (e.g. sharp's real resized height
 * came out larger than the caller's own maxHeight estimate) the pad step alone
 * cannot shrink it back down, and callers rely on this buffer being sized to
 * exactly match the canvas they allocated for diffing (pixelmatch requires equal
 * buffer sizes and a mismatch here would throw or read out of bounds).
 */
async function padToCanvas(image, targetWidth, targetHeight) {
  const padRight = Math.max(0, targetWidth - image.width);
  const padBottom = Math.max(0, targetHeight - image.height);

  let extended = image;
  if (padRight > 0 || padBottom > 0) {
    // Materialize the extended buffer before extracting from it (rather than
    // chaining .extend().extract() on one sharp() pipeline instance): sharp
    // has a known quirk where an .extract() chained straight after .extend()
    // on a raw-buffer-backed pipeline can compute the extract region against
    // the pre-extend dimensions and throw "bad extract area" even though the
    // extend itself is correct — round-tripping through a fresh raw buffer
    // sidesteps it.
    const { data, info } = await sharp(image.data, {
      raw: { width: image.width, height: image.height, channels: image.channels },
    })
      .extend({
        top: 0,
        left: 0,
        right: padRight,
        bottom: padBottom,
        background: { r: 0, g: 0, b: 0, alpha: 0 },
      })
      .raw()
      .toBuffer({ resolveWithObject: true });
    extended = { data, width: info.width, height: info.height, channels: info.channels };
  }

  const { data, info } = await sharp(extended.data, {
    raw: { width: extended.width, height: extended.height, channels: extended.channels },
  })
    .extract({ left: 0, top: 0, width: targetWidth, height: targetHeight })
    .raw()
    .toBuffer({ resolveWithObject: true });

  return { data, width: info.width, height: info.height };
}

/**
 * Main execution routine for comparing reference design exports against current browser screenshots.
 *
 * Discovers current screenshots, verifies them against expected screens and reference baselines,
 * executes pixelmatch diffing, saves diff and composite preview artifacts, and writes summaries.
 */
async function run() {
  const options = parseArgs();

  console.log('\n======================================================');
  console.log('   Gameplane Visual Regression Comparison Engine');
  console.log('======================================================');
  console.log(`Reference Directory : ${path.relative(REPO_ROOT, options.refDir)}`);
  console.log(`Current Directory   : ${path.relative(REPO_ROOT, options.currDir)}`);
  console.log(`Output Directory    : ${path.relative(REPO_ROOT, options.outDir)}`);
  console.log(`Default Threshold   : ${(options.maxDiffFactor * 100).toFixed(2)}%`);
  console.log(`Pixel Sensitivity   : ${options.pixelThreshold}`);
  console.log('------------------------------------------------------\n');

  if (!fs.existsSync(options.currDir)) {
    console.warn(`Warning: Current screenshots directory not found: ${options.currDir}`);
    process.exit(options.allowMissing ? 0 : 1);
  }

  if (!fs.existsSync(options.refDir)) {
    console.error(`Error: Reference screenshots directory not found: ${options.refDir}`);
    process.exit(1);
  }

  fs.mkdirSync(options.outDir, { recursive: true });

  // Discover captured screens in currDir
  const currFiles = fs
    .readdirSync(options.currDir)
    .filter((file) => file.endsWith('.png') && !file.includes('-diff') && !file.includes('-composite'));

  if (currFiles.length === 0) {
    console.warn(`Warning: No PNG screenshots found in ${options.currDir}`);
    process.exit(options.allowMissing ? 0 : 1);
  }

  const results = [];
  let hasFailure = false;

  // Reconcile expected screens when --check-expected is enabled
  if (options.checkExpected) {
    const expectedList = options.expectedScreens || DEFAULT_EXPECTED_SCREENS;
    for (const expectedId of expectedList) {
      const expectedFile = `${expectedId}.png`;
      if (!currFiles.includes(expectedFile)) {
        console.warn(`❌ [${expectedId.padEnd(8)}] Missing current browser capture in ${path.relative(REPO_ROOT, options.currDir)}`);
        if (!options.allowMissing) {
          hasFailure = true;
        }
        results.push({
          id: expectedId,
          status: 'MISSING_CAPTURE',
          diffRatio: 1,
          diffPercentage: 'MISSING',
          threshold: 'N/A',
          totalPixels: 0,
          diffPixels: 0,
          dimensions: 'N/A',
          diffImage: null,
          compositeImage: null,
        });
      }
    }
  }

  for (const file of currFiles) {
    const screenId = path.basename(file, '.png');
    const currPath = path.join(options.currDir, file);
    const refPath = path.join(options.refDir, `${screenId}.png`);

    if (!fs.existsSync(refPath)) {
      console.warn(`⚠️  [${screenId}] Reference image missing at ${refPath}`);
      if (!options.allowMissing) {
        hasFailure = true;
        results.push({
          id: screenId,
          status: 'MISSING_REF',
          diffPercentage: 'N/A',
          threshold: 'N/A',
          totalPixels: 0,
          diffPixels: 0,
          dimensions: 'N/A',
        });
      }
      continue;
    }

    // Read metadata of both to determine the scale factor. A
    // REFERENCE_CROP_ALLOWLIST id's effective reference size is its crop rect.
    const refCrop = REFERENCE_CROP_ALLOWLIST[screenId];
    const refMeta = refCrop
      ? { width: refCrop.width, height: refCrop.height }
      : await sharp(refPath).metadata();
    const currMeta = await sharp(currPath).metadata();

    // Scale-normalize the capture to the reference's width class before padding —
    // a 2880-wide design frame vs. a 1440 browser capture is a scale mismatch, not
    // a crop/content mismatch, and diffing it unscaled would read as near-100% diff.
    const scaleFactor = computeScaleFactor(currMeta.width || 0, refMeta.width || 0);

    // Actually perform the scale now (rather than only predicting its output size)
    // so the final canvas dimensions below are driven by sharp's own reported
    // width/height, never a manually-recomputed one — sharp's aspect-ratio
    // rounding for the resized height can land a pixel off from a naive
    // `width * (height/width)` guess, which would otherwise make the diffData
    // buffer allocated below too small for the actual scaled+padded image.
    const scaledCurr = await loadAndScaleImage(currPath, scaleFactor);

    if (scaleFactor !== 1) {
      console.log(
        `  ↕ [${screenId}] scale-normalized capture ${currMeta.width}x${currMeta.height} → ${scaledCurr.width}x${scaledCurr.height} (factor: ${scaleFactor.toFixed(3)}x)`
      );
    }

    // Scale gate — see the header comment's "Scale gate (SCALE_ALLOWLIST)"
    // section. Independent of, and in addition to, the global/block diff
    // gate below. Two-sided: an allowlisted id must stay within
    // SCALE_BAND_FRACTION of its recorded value in either direction.
    const scaleRecorded = SCALE_ALLOWLIST[screenId];
    let scaleGateMessage = null;
    if (Math.abs(scaleFactor - 1) > SCALE_EPSILON) {
      if (scaleRecorded === undefined) {
        scaleGateMessage =
          `[${screenId}] scaleFactor ${scaleFactor.toFixed(4)}x is not 1.000x and "${screenId}" is not in ` +
          `SCALE_ALLOWLIST (web/scripts/compare-screenshots.mjs) — either this capture regressed to the wrong ` +
          `element/viewport size, or it's a deliberate implementation-size crop that needs adding to the allowlist.`;
      } else {
        const band = scaleRecorded * SCALE_BAND_FRACTION;
        const lowerBound = scaleRecorded - band;
        const upperBound = scaleRecorded + band;
        if (scaleFactor < lowerBound - SCALE_EPSILON || scaleFactor > upperBound + SCALE_EPSILON) {
          scaleGateMessage =
            `[${screenId}] scaleFactor ${scaleFactor.toFixed(4)}x deviates from its SCALE_ALLOWLIST recorded value ` +
            `of ${scaleRecorded.toFixed(2)}x by more than ${(SCALE_BAND_FRACTION * 100).toFixed(0)}% ` +
            `(allowed range ${lowerBound.toFixed(4)}x–${upperBound.toFixed(4)}x, web/scripts/compare-screenshots.mjs) — ` +
            `update the recorded value only if this shift is intentional.`;
        }
      }
    }
    const scalePassed = scaleGateMessage === null;

    const maxWidth = Math.max(refMeta.width || 0, scaledCurr.width);
    const maxHeight = Math.max(refMeta.height || 0, scaledCurr.height);

    // Standardize both onto maxWidth × maxHeight raw RGBA buffers
    const refRaw = refCrop
      ? await (async () => {
          const { data, info } = await sharp(refPath)
            .ensureAlpha()
            .extract(refCrop)
            .raw()
            .toBuffer({ resolveWithObject: true });
          return { data, width: info.width, height: info.height, channels: info.channels };
        })()
      : await loadAndScaleImage(refPath, 1);
    const paddedRef = await padToCanvas(refRaw, maxWidth, maxHeight);
    const paddedCurr = await padToCanvas(scaledCurr, maxWidth, maxHeight);

    const diffData = Buffer.alloc(maxWidth * maxHeight * 4);

    // Run pixelmatch to populate diffData (the magenta/yellow-highlighted diff
    // buffer used below). Its own returned count is unmasked (every canvas
    // pixel); the effective, possibly reference-alpha-masked count used for
    // gating and reporting is recomputed from diffData in the block loop below.
    pixelmatch(
      paddedRef.data,
      paddedCurr.data,
      diffData,
      maxWidth,
      maxHeight,
      {
        threshold: options.pixelThreshold,
        includeAA: false, // Disregard anti-aliasing subpixel differences
        diffColor: [255, 0, 100], // Highlight diffs in magenta
        aaColor: [255, 255, 0],   // AA pixels in yellow (if detected)
      }
    );

    // Compute regional 4x4 block diff ratios to protect against dark-canvas dilution.
    // Also tallies reference-opaque pixel counts (global + per-block) for the
    // REFERENCE_ALPHA_MASK gate — see the header comment's "Reference-alpha
    // mask" section. referenceOpaquePixelCount (and the derived opaqueFraction)
    // are always tracked and logged, independent of whether the mask is on;
    // only whether a non-opaque pixel is *excluded* from the diff counts below
    // depends on REFERENCE_ALPHA_MASK.
    const cols = 4;
    const rows = 4;
    const numBlocks = cols * rows;
    const blockDiffPixels = new Uint32Array(numBlocks);
    const blockTotalPixels = new Uint32Array(numBlocks);

    const blockWidth = Math.ceil(maxWidth / cols);
    const blockHeight = Math.ceil(maxHeight / rows);

    let referenceOpaquePixelCount = 0;

    for (let y = 0; y < maxHeight; y++) {
      const blockRow = Math.min(rows - 1, Math.floor(y / blockHeight));
      const rowOffset = y * maxWidth * 4;
      for (let x = 0; x < maxWidth; x++) {
        const pxIdx = rowOffset + x * 4;
        const isReferenceOpaque = paddedRef.data[pxIdx + 3] >= ALPHA_MASK_THRESHOLD;
        if (isReferenceOpaque) {
          referenceOpaquePixelCount++;
        }

        // A pixel that's transparent in the reference carries no design
        // intent — excluded from both denominators and numerators below,
        // global and per-block, when the mask is on.
        if (REFERENCE_ALPHA_MASK && !isReferenceOpaque) {
          continue;
        }

        const blockCol = Math.min(cols - 1, Math.floor(x / blockWidth));
        const blockIdx = blockRow * cols + blockCol;
        blockTotalPixels[blockIdx]++;
        if (diffData[pxIdx] === 255 && diffData[pxIdx + 1] === 0 && diffData[pxIdx + 2] === 100) {
          blockDiffPixels[blockIdx]++;
        }
      }
    }

    const opaqueFraction = maxWidth * maxHeight > 0 ? referenceOpaquePixelCount / (maxWidth * maxHeight) : 0;

    // A block whose opaque reference pixel count falls below
    // ALPHA_MASK_MIN_BLOCK_PIXELS is skipped from the block-max computation
    // entirely (mask on) — too little reference content to judge from.
    const minBlockPixels = REFERENCE_ALPHA_MASK ? ALPHA_MASK_MIN_BLOCK_PIXELS : 1;
    let maxBlockDiffRatio = 0;
    for (let b = 0; b < numBlocks; b++) {
      if (blockTotalPixels[b] >= minBlockPixels) {
        const ratio = blockDiffPixels[b] / blockTotalPixels[b];
        if (ratio > maxBlockDiffRatio) {
          maxBlockDiffRatio = ratio;
        }
      }
    }

    // Global totals are the sum of the per-block counts above, so they honor
    // the same reference-alpha exclusion (when REFERENCE_ALPHA_MASK is on,
    // this is the count of reference-opaque pixels and diffs among them;
    // when off, every block counted every pixel, so this equals
    // maxWidth * maxHeight and pixelmatch's own unmasked diff count, resp.).
    let totalPixels = 0;
    let effectiveDiffPixels = 0;
    for (let b = 0; b < numBlocks; b++) {
      totalPixels += blockTotalPixels[b];
      effectiveDiffPixels += blockDiffPixels[b];
    }

    const diffRatio = totalPixels > 0 ? effectiveDiffPixels / totalPixels : 0;
    const diffPercentage = (diffRatio * 100).toFixed(2);

    const allowedThreshold = SCREEN_THRESHOLD_OVERRIDES[screenId] ?? options.maxDiffFactor;
    const allowedPercentage = (allowedThreshold * 100).toFixed(2);
    const globalPassed = diffRatio <= allowedThreshold;

    const allowedBlockThreshold = SCREEN_BLOCK_THRESHOLD_OVERRIDES[screenId] ?? options.maxBlockDiffFactor;
    const allowedBlockPercentage = (allowedBlockThreshold * 100).toFixed(2);
    const maxBlockPercentage = (maxBlockDiffRatio * 100).toFixed(2);
    const blockPassed = maxBlockDiffRatio <= allowedBlockThreshold;

    const passed = globalPassed && blockPassed && scalePassed;

    if (!passed) {
      hasFailure = true;
    }

    if (!scalePassed) {
      console.error(`❌ ${scaleGateMessage}`);
    }

    // Save individual diff image
    const diffFileName = `${screenId}-diff.png`;
    const diffFilePath = path.join(options.outDir, diffFileName);
    const diffPngBuffer = await sharp(diffData, {
      raw: { width: maxWidth, height: maxHeight, channels: 4 },
    })
      .png()
      .toBuffer();
    await fs.promises.writeFile(diffFilePath, diffPngBuffer);

    // Create 3-panel composite preview: [ Reference | Current Browser | Diff ]
    const compositeFileName = `${screenId}-composite.png`;
    const compositeFilePath = path.join(options.outDir, compositeFileName);

    const paddedRefPng = await sharp(paddedRef.data, {
      raw: { width: maxWidth, height: maxHeight, channels: 4 },
    })
      .png()
      .toBuffer();

    const paddedCurrPng = await sharp(paddedCurr.data, {
      raw: { width: maxWidth, height: maxHeight, channels: 4 },
    })
      .png()
      .toBuffer();

    await sharp({
      create: {
        width: maxWidth * 3,
        height: maxHeight,
        channels: 4,
        background: { r: 18, g: 18, b: 20, alpha: 1 },
      },
    })
      .composite([
        { input: paddedRefPng, left: 0, top: 0 },
        { input: paddedCurrPng, left: maxWidth, top: 0 },
        { input: diffPngBuffer, left: maxWidth * 2, top: 0 },
      ])
      .png()
      .toFile(compositeFilePath);

    const statusStr = passed ? '✅ PASS' : '❌ FAIL';
    const scaleGateStr = scalePassed ? '' : ' | Scale gate: FAIL';
    console.log(
      `${statusStr} [${screenId.padEnd(8)}] Global: ${diffPercentage.padStart(6)}% (max: ${allowedPercentage}%) | ` +
      `Block: ${maxBlockPercentage.padStart(6)}% (max: ${allowedBlockPercentage}%) | ` +
      `Pixels: ${effectiveDiffPixels.toLocaleString().padStart(9)} / ${totalPixels.toLocaleString()} | Size: ${maxWidth}x${maxHeight} | ` +
      `Scale: ${scaleFactor.toFixed(3)}x${scaleGateStr} | Opaque: ${(opaqueFraction * 100).toFixed(1)}%`
    );

    results.push({
      id: screenId,
      status: passed ? 'PASS' : 'FAIL',
      scaleGatePassed: scalePassed,
      scaleGateMessage,
      diffRatio,
      diffPercentage,
      threshold: allowedPercentage,
      maxBlockDiffRatio,
      maxBlockDiffPercentage: maxBlockPercentage,
      blockThreshold: allowedBlockPercentage,
      totalPixels,
      diffPixels: effectiveDiffPixels,
      dimensions: `${maxWidth}x${maxHeight}`,
      scaleFactor: Number(scaleFactor.toFixed(4)),
      opaqueFraction: Number(opaqueFraction.toFixed(4)),
      diffImage: path.relative(WEB_ROOT, diffFilePath),
      compositeImage: path.relative(WEB_ROOT, compositeFilePath),
    });
  }

  // Write summary JSON
  const summaryJsonPath = path.join(options.outDir, 'summary.json');
  fs.writeFileSync(
    summaryJsonPath,
    JSON.stringify(
      {
        totalScreens: results.length,
        passed: results.filter((r) => r.status === 'PASS').length,
        failed: results.filter((r) => r.status === 'FAIL').length,
        timestamp: new Date().toISOString(),
        results,
      },
      null,
      2
    )
  );

  // Write GitHub Step Summary if environment variable exists
  if (process.env.GITHUB_STEP_SUMMARY) {
    try {
      let markdown = '## 🎨 Visual Regression Test Summary\n\n';
      markdown += `Total screens evaluated: **${results.length}** | `;
      markdown += `Passed: **${results.filter((r) => r.status === 'PASS').length}** | `;
      markdown += `Failed: **${results.filter((r) => r.status === 'FAIL').length}**\n\n`;
      markdown += '| Screen ID | Status | Global Diff % | Max Global % | Max Block % | Max Block % Allowed | Diff Pixels | Canvas Size | Scale |\n';
      markdown += '| :--- | :---: | :---: | :---: | :---: | :---: | :---: | :---: | :---: |\n';

      for (const r of results) {
        const icon = r.status === 'PASS' ? '✅' : '❌';
        const diffText = r.status === 'PASS' || r.status === 'FAIL' ? `**${r.diffPercentage}%**` : `*${r.diffPercentage}*`;
        const thresholdText = r.threshold === 'N/A' ? 'N/A' : `${r.threshold}%`;
        const blockText = r.maxBlockDiffPercentage ? `**${r.maxBlockDiffPercentage}%**` : 'N/A';
        const blockThresholdText = r.blockThreshold ? `${r.blockThreshold}%` : 'N/A';
        const scaleText = typeof r.scaleFactor === 'number' ? `${r.scaleFactor.toFixed(3)}x` : 'N/A';
        markdown += `| \`${r.id}\` | ${icon} ${r.status} | ${diffText} | ${thresholdText} | ${blockText} | ${blockThresholdText} | ${r.diffPixels.toLocaleString()} | ${r.dimensions} | ${scaleText} |\n`;
      }

      markdown += '\n> Diff highlighting: Baseline design (left) vs Current browser (center) vs Diff highlight (right, magenta).\n';
      fs.appendFileSync(process.env.GITHUB_STEP_SUMMARY, markdown);
    } catch (err) {
      console.error('Failed to append to GITHUB_STEP_SUMMARY:', err);
    }
  }

  console.log('\n------------------------------------------------------');
  const passCount = results.filter((r) => r.status === 'PASS').length;
  const failCount = results.filter((r) => r.status === 'FAIL').length;
  console.log(`Summary: ${passCount} passed, ${failCount} failed, ${results.length} total.`);
  console.log(`Artifacts saved in: ${path.relative(REPO_ROOT, options.outDir)}/`);
  console.log('======================================================\n');

  if (hasFailure) {
    console.error('❌ Visual diff check failed: one or more screens exceeded allowed diff threshold.');
    process.exit(1);
  } else {
    console.log('✅ Visual diff check passed: all screens within allowed diff thresholds.');
    process.exit(0);
  }
}

run().catch((err) => {
  console.error('Fatal error during visual comparison:', err);
  process.exit(1);
});
