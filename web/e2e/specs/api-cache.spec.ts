import { createServer } from "node:http";
import { test, expect } from "@playwright/test";

// Use a real HTTP server: Playwright route interception disables the browser's
// HTTP cache and would hide the document/API collision this test reproduces.
// Loading only api.ts from Vite also keeps MSW out of the fixture's origin.
test.use({ storageState: { cookies: [], origins: [] }, serviceWorkers: "block" });

test("API reads bypass a cached HTML document at the same URL", async ({ page, baseURL }) => {
  const shell = "<!doctype html><title>Cached cluster document</title>";
  let jsonRequests = 0;
  const jsonAccepts: string[] = [];
  const server = createServer((req, res) => {
    if (req.url !== "/cluster") {
      res.writeHead(404).end();
      return;
    }
    if (req.headers.accept?.includes("text/html")) {
      // Simulate a shell stored before the response gained Vary: Accept.
      res.writeHead(200, {
        "Content-Type": "text/html",
        "Cache-Control": "public, max-age=3600",
      });
      res.end(shell);
      return;
    }
    jsonRequests++;
    jsonAccepts.push(req.headers.accept ?? "");
    res.writeHead(200, { "Content-Type": "application/json" });
    res.end(JSON.stringify({
      ready: 1,
      total: 1,
      nodes: [{ name: "fixture-node", status: "Ready" }],
      revision: jsonRequests,
    }));
  });

  try {
    await new Promise<void>((resolve, reject) => {
      server.once("error", reject);
      server.listen(0, "127.0.0.1", resolve);
    });
    const address = server.address();
    if (!address || typeof address === "string" || !baseURL) {
      throw new Error("HTTP fixture or Vite URL unavailable");
    }
    await page.goto(`http://127.0.0.1:${address.port}/cluster`);

    // First prove the browser has the old response cached. A normal fetch
    // receives HTML even though the server would send JSON for Accept: */*.
    const cached = await page.evaluate(async () => (await fetch("/cluster")).text());
    expect(cached).toBe(shell);
    expect(jsonRequests).toBe(0);

    const moduleURL = new URL("/src/lib/api.ts", baseURL).href;
    for (const revision of [1, 2]) {
      const result: unknown = await page.evaluate(async (url) => {
        const { api } = await import(url) as { api: (path: string) => Promise<unknown> };
        return api("/cluster");
      }, moduleURL);
      expect(result).toEqual({
        ready: 1,
        total: 1,
        nodes: [{ name: "fixture-node", status: "Ready" }],
        revision,
      });
      expect(jsonRequests).toBe(revision);
      expect(jsonAccepts[revision - 1]).toBe("application/json, application/problem+json");
    }
  } finally {
    server.closeAllConnections();
    await new Promise<void>((resolve, reject) => {
      server.close((err) => err ? reject(err) : resolve());
    });
  }
});
