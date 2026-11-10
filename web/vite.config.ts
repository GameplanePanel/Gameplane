import { defineConfig } from "vite";
import type { Plugin } from "vite";
import react from "@vitejs/plugin-react";
import { execFileSync } from "node:child_process";
import { createHash } from "node:crypto";
import { cpSync, mkdirSync, readFileSync, writeFileSync } from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

// __dirname isn't defined in ESM; derive it from import.meta.url.
const here = path.dirname(fileURLToPath(import.meta.url));

// i18nFixtures: VITE_I18N_FIXTURES=1 writes the fixture language "zz" (spec 019,
// T046) so the E2E i18n specs have a language with 0% completeness to switch to.
// Fixtures are written only on request; release builds never request them, and
// web/src/generated/ is gitignored, so a clean checkout never contains them.
function i18nFixtures(): Plugin {
  return {
    name: "gameplane-i18n-fixtures",
    configResolved() {
      if (process.env.VITE_I18N_FIXTURES === "1") {
        execFileSync(process.execPath, [path.resolve(here, "scripts/i18n-fixtures.mjs")], { stdio: "inherit" });
      }
    },
  };
}

// i18nPseudo: VITE_I18N_PSEUDO=1 writes the pseudo-language "en-XA" (spec 019,
// T099, FR-038) for the dev server (and the planned Playwright en-XA project,
// T100). Release builds never set the flag, so en-XA never reaches a release
// languages.json.
function i18nPseudo(): Plugin {
  return {
    name: "gameplane-i18n-pseudo",
    configResolved() {
      if (process.env.VITE_I18N_PSEUDO === "1") {
        execFileSync(process.execPath, [path.resolve(here, "scripts/i18n-pseudo.mjs")], { stdio: "inherit" });
      }
    },
  };
}

// monacoAssets copies Monaco's AMD build into dist/monaco/vs, where
// src/lib/monaco-loader.ts points the editor loader, so the editor loads
// from the dashboard's own origin instead of cdn.jsdelivr.net.
function monacoAssets(): Plugin {
  let outDir = "";
  return {
    name: "gameplane-monaco-assets",
    apply: "build",
    configResolved(config) {
      outDir = path.resolve(config.root, config.build.outDir);
    },
    closeBundle() {
      cpSync(path.resolve(here, "node_modules/monaco-editor/min/vs"), path.join(outDir, "monaco/vs"), {
        recursive: true,
      });
    },
  };
}

// cspHeader writes web/build/csp.conf, the Content-Security-Policy header
// nginx sends with the SPA shell (web/nginx.conf.template includes it). The
// shell's inline boot scripts (theme and language, run before first paint)
// are allowed by their sha256 hash, so the policy is derived from the built
// index.html instead of being kept in step by hand.
function cspHeader(): Plugin {
  let outDir = "";
  return {
    name: "gameplane-csp-header",
    apply: "build",
    configResolved(config) {
      outDir = path.resolve(config.root, config.build.outDir);
    },
    closeBundle() {
      const html = readFileSync(path.join(outDir, "index.html"), "utf8");
      const inline = [...html.matchAll(/<script(?![^>]*\bsrc=)[^>]*>([\s\S]*?)<\/script>/gi)];
      const hashes = inline.map((m) => `'sha256-${createHash("sha256").update(m[1] ?? "").digest("base64")}'`);
      const policy = [
        "default-src 'self'",
        ["script-src 'self'", ...hashes].join(" "),
        "style-src 'self' 'unsafe-inline' https://fonts.googleapis.com",
        "font-src 'self' data: https://fonts.gstatic.com",
        "img-src 'self' data: blob: https:",
        "connect-src 'self'",
        "worker-src 'self' blob:",
        "object-src 'none'",
        "base-uri 'self'",
        "form-action 'self'",
        "frame-ancestors 'none'",
      ].join("; ");
      mkdirSync(path.resolve(here, "build"), { recursive: true });
      writeFileSync(
        path.resolve(here, "build/csp.conf"),
        `add_header Content-Security-Policy "${policy}" always;\n`,
      );
    },
  };
}

// API target for the dev server's proxy. Defaults to the standard local
// process; Playwright live-mode points it at a kubectl port-forward by
// setting GAMEPLANE_API_URL=http://localhost:18080 before spawning vite.
const apiTarget = process.env.GAMEPLANE_API_URL ?? "http://localhost:8000";
const wsTarget = apiTarget.replace(/^http/, "ws");

// htmlBypass returns "/index.html" for HTML navigations (Accept includes
// text/html), telling vite to serve the SPA shell instead of forwarding
// to the API. fetch() / XHR calls fall through to the proxy.
function htmlBypass(req: { headers: Record<string, string | string[] | undefined> }): string | undefined {
  const accept = req.headers.accept;
  const text = Array.isArray(accept) ? accept.join(",") : (accept ?? "");
  if (text.includes("text/html")) return "/index.html";
  return undefined;
}

export default defineConfig({
  plugins: [react(), i18nFixtures(), i18nPseudo(), monacoAssets(), cspHeader()],
  resolve: {
    alias: {
      "@": path.resolve(here, "./src"),
      "@i18n": path.resolve(here, "../i18n/locales"),
    },
  },
  server: {
    port: 5173,
    proxy: {
      // Proxy API + WS traffic to the in-cluster API during `npm run dev`.
      // The SPA uses unprefixed paths (api.ts → fetch("/users/me") etc.),
      // and several of those paths (/servers, /modules, /backups, /admin)
      // are ALSO front-end SPA routes. We disambiguate by Accept header:
      // a browser HTML navigation sends Accept: text/html and bypasses
      // the proxy (vite serves index.html), while fetch()/XHR sends
      // Accept: */* or application/json and is forwarded to the API.
      //
      // /auth, /events, /healthz, /ws, /cluster, /templates, /schedules,
      // /restores, /backup-destinations, /roles, /users, /fleet
      // have no SPA-route collision so they're forwarded unconditionally.
      // (/roles also covers /roles/permissions by prefix — the Users page's
      // Roles tab and the permission catalog both depend on it.)
      "/api":    { target: apiTarget, changeOrigin: true, rewrite: (p) => p.replace(/^\/api/, "") },
      "/auth":   { target: apiTarget, changeOrigin: true },
      "/events": { target: apiTarget, changeOrigin: true },
      "/fleet":  { target: apiTarget, changeOrigin: true },
      "/healthz": { target: apiTarget, changeOrigin: true },
      "/roles":  { target: apiTarget, changeOrigin: true },
      "/users":  { target: apiTarget, changeOrigin: true, bypass: htmlBypass },
      "/cluster":{ target: apiTarget, changeOrigin: true, bypass: htmlBypass },
      "/servers": { target: apiTarget, changeOrigin: true, bypass: htmlBypass },
      "/templates": { target: apiTarget, changeOrigin: true },
      "/backups": { target: apiTarget, changeOrigin: true, bypass: htmlBypass },
      "/schedules": { target: apiTarget, changeOrigin: true },
      "/restores": { target: apiTarget, changeOrigin: true },
      "/backup-destinations": { target: apiTarget, changeOrigin: true },
      "/shares": { target: apiTarget, changeOrigin: true },
      "/modules": { target: apiTarget, changeOrigin: true, bypass: htmlBypass },
      "/admin":  { target: apiTarget, changeOrigin: true, bypass: htmlBypass },
      "/ws":     { target: wsTarget,  ws: true },
    },
  },
  build: {
    rollupOptions: {
      output: {
        manualChunks: (id: string) => {
          if (id.includes("@monaco-editor") || id.includes("monaco-editor")) {
            return "monaco";
          }
          if (id.includes("@xterm")) {
            return "xterm";
          }
        },
      },
    },
  },
});
