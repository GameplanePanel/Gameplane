import { describe, it, expect } from "vitest";
import { http, HttpResponse } from "msw";
import { server } from "@/test/server";
import { makeTemplate } from "@/test/factories";
import { createResourceClient } from "./endpoints";
import { getServerTemplate } from "./serverTemplate";

const client = () => createResourceClient({ cluster: "local", namespace: "gameplane-games" });

describe("getServerTemplate", () => {
  it("reads the catalog template when the caller may", async () => {
    let scoped = 0;
    server.use(
      http.get("/templates/:name", ({ params }) => HttpResponse.json(makeTemplate({ metadata: { name: String(params.name) } }))),
      http.get("/servers/alpha/template", () => { scoped += 1; return HttpResponse.json(makeTemplate()); }),
    );
    const tmpl = await getServerTemplate(client(), "alpha", "gameplane-games", "valheim");
    expect(tmpl.metadata.name).toBe("valheim");
    expect(scoped).toBe(0);
  });

  it("falls back to the server-scoped template when the catalog refuses", async () => {
    let namespace: string | null = null;
    server.use(
      http.get("/templates/:name", () => HttpResponse.json({ detail: "forbidden" }, { status: 403 })),
      http.get("/servers/alpha/template", ({ request }) => {
        namespace = new URL(request.url).searchParams.get("namespace");
        return HttpResponse.json(makeTemplate({ metadata: { name: "scoped" } }));
      }),
    );
    const tmpl = await getServerTemplate(client(), "alpha", "gameplane-games", "valheim");
    expect(tmpl.metadata.name).toBe("scoped");
    expect(namespace).toBe("gameplane-games");
  });

  it("passes other errors through", async () => {
    server.use(http.get("/templates/:name", () => HttpResponse.json({ detail: "boom" }, { status: 500 })));
    await expect(getServerTemplate(client(), "alpha", "gameplane-games", "valheim")).rejects.toMatchObject({ status: 500 });
  });
});
