#!/usr/bin/env bash
set -euo pipefail

# Run from web/ after npm ci. Use the browser and FFmpeg from the locked CLI.
playwright=./node_modules/.bin/playwright

probe_browser() {
  timeout --kill-after=5s 45s node --input-type=module <<'JS'
import { stat } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { chromium } from 'playwright';

const browser = await chromium.launch();
try {
  const page = await browser.newPage({ recordVideo: { dir: tmpdir() } });
  await page.setContent('<button>Français</button>');
  await page.getByRole('button', { name: 'Français' }).screenshot();
  const video = page.video();
  await page.close();
  if ((await stat(await video.path())).size === 0) throw new Error('Chromium recorded an empty video');
  await video.delete();
} finally {
  await browser.close();
}
JS
}

# Hosted runners normally have Chromium's native libraries. Only contact the
# distro mirrors if the installed browser cannot render and record a page.
timeout --kill-after=10s 4m "$playwright" install chromium
if probe_browser; then
  exit 0
fi

echo "Chromium's runtime probe failed; installing Playwright's distribution dependencies." >&2
timeout --kill-after=10s 4m "$playwright" install --with-deps chromium
probe_browser
