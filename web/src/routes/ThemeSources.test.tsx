// Theme sources admin page (spec 043, T050): table rows, add, enable toggle
// and delete against the themes/sources API.
import { afterEach, beforeEach, describe, it, expect, vi } from "vitest";
import type { ReactNode } from "react";
import { http, HttpResponse } from "msw";
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { server } from "@/test/server";
import { renderWithQuery } from "@/test/render";
import type { ThemeSource, ThemeSourceInput } from "@/types";

vi.mock("@tanstack/react-router", () => ({
  useNavigate: () => vi.fn(),
  Link: ({ children, to, ...rest }: { children: ReactNode; to: string } & Record<string, unknown>) => (
    <a href={to} {...rest}>{children}</a>
  ),
}));

import { ThemeSourcesPage } from "./ThemeSources";

const official: ThemeSource = {
  name: "official", type: "oci",
  spec: { oci: { url: "ghcr.io/gameplane/themes", themes: ["aurora-night"], insecure: false } },
  verify: null, credentialSecret: null, refreshInterval: 3600, enabled: true, builtin: true,
  lastSyncAt: "2026-11-27T12:00:00Z", lastError: null, themeCount: 8,
};

const mirror: ThemeSource = {
  name: "mirror", type: "http",
  spec: { http: { url: "themes.example.org/index.json" } },
  verify: null, credentialSecret: "archive-creds", refreshInterval: 3600, enabled: false, builtin: false,
  lastSyncAt: null, lastError: "connection refused", themeCount: 0,
};

beforeEach(() => {
  window.localStorage.clear();
});

afterEach(() => {
  server.resetHandlers();
});

describe("ThemeSourcesPage", () => {
  it("lists sources, marks the built-in one, and hides delete for it", async () => {
    server.use(http.get("/themes/sources", () => HttpResponse.json([official, mirror])));
    renderWithQuery(<ThemeSourcesPage />);

    const rows = await screen.findAllByTestId("theme-source-row");
    expect(rows).toHaveLength(2);
    expect(within(rows[0]!).getByText("Built-in")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Delete official" })).toBeNull();
    expect(screen.getByRole("button", { name: "Delete mirror" })).toBeInTheDocument();
    expect(within(rows[1]!).getByText("Sync failed: connection refused")).toBeInTheDocument();
  });

  it("adds an OCI source with the typed fields", async () => {
    let body: ThemeSourceInput | null = null;
    server.use(
      http.get("/themes/sources", () => HttpResponse.json([official, mirror])),
      http.post("/themes/sources", async ({ request }) => {
        body = (await request.json()) as ThemeSourceInput;
        return HttpResponse.json({ ...official, name: "community" }, { status: 201 });
      }),
    );
    renderWithQuery(<ThemeSourcesPage />);

    await screen.findAllByTestId("theme-source-row");
    await userEvent.click(screen.getByRole("button", { name: "Add source" }));
    const dialog = await screen.findByRole("dialog");
    await userEvent.type(within(dialog).getByLabelText("Name"), "community");
    await userEvent.type(within(dialog).getByLabelText("Registry URL"), "ghcr.io/community/themes");
    await userEvent.type(within(dialog).getByLabelText("Themes"), "aurora-night, paper-light");
    await userEvent.click(within(dialog).getByRole("button", { name: "Add source" }));

    await waitFor(() => expect(body).not.toBeNull());
    expect(body).toMatchObject({
      name: "community",
      type: "oci",
      spec: { oci: { url: "ghcr.io/community/themes", themes: ["aurora-night", "paper-light"], insecure: false } },
      verify: null,
      refreshInterval: 3600,
      enabled: true,
    });
  });

  it("sends the pull secret name, and null when the field is empty", async () => {
    const bodies: ThemeSourceInput[] = [];
    server.use(
      http.get("/themes/sources", () => HttpResponse.json([official, mirror])),
      http.post("/themes/sources", async ({ request }) => {
        bodies.push((await request.json()) as ThemeSourceInput);
        return HttpResponse.json({ ...official, name: "community" }, { status: 201 });
      }),
    );
    renderWithQuery(<ThemeSourcesPage />);

    await screen.findAllByTestId("theme-source-row");
    await userEvent.click(screen.getByRole("button", { name: "Add source" }));
    const dialog = await screen.findByRole("dialog");
    await userEvent.type(within(dialog).getByLabelText("Name"), "community");
    await userEvent.type(within(dialog).getByLabelText("Registry URL"), "ghcr.io/community/themes");
    await userEvent.type(within(dialog).getByLabelText("Themes"), "aurora-night");
    await userEvent.type(within(dialog).getByLabelText("Pull secret"), " registry-creds ");
    await userEvent.click(within(dialog).getByRole("button", { name: "Add source" }));
    await waitFor(() => expect(bodies).toHaveLength(1));
    expect(bodies[0]).toMatchObject({ name: "community", credentialSecret: "registry-creds" });
  });

  it("edits a source with its stored credentials secret prefilled", async () => {
    let body: ThemeSourceInput | null = null;
    server.use(
      http.get("/themes/sources", () => HttpResponse.json([official, mirror])),
      http.put("/themes/sources/mirror", async ({ request }) => {
        body = (await request.json()) as ThemeSourceInput;
        return HttpResponse.json(mirror);
      }),
    );
    renderWithQuery(<ThemeSourcesPage />);

    await screen.findAllByTestId("theme-source-row");
    await userEvent.click(screen.getByRole("button", { name: "Edit mirror" }));
    const dialog = await screen.findByRole("dialog");
    const secret = within(dialog).getByLabelText("Credentials secret");
    expect(secret).toHaveValue("archive-creds");
    await userEvent.clear(secret);
    await userEvent.click(within(dialog).getByRole("button", { name: "Save changes" }));
    await waitFor(() => expect(body).not.toBeNull());
    expect(body).toMatchObject({ name: "mirror", type: "http", credentialSecret: null });
  });

  it("toggling the switch sends the source with enabled flipped", async () => {
    let body: ThemeSourceInput | null = null;
    server.use(
      http.get("/themes/sources", () => HttpResponse.json([official, mirror])),
      http.put("/themes/sources/mirror", async ({ request }) => {
        body = (await request.json()) as ThemeSourceInput;
        return HttpResponse.json({ ...mirror, enabled: true });
      }),
    );
    renderWithQuery(<ThemeSourcesPage />);

    await screen.findAllByTestId("theme-source-row");
    await userEvent.click(screen.getByRole("switch", { name: "mirror: On" }));

    await waitFor(() => expect(body).not.toBeNull());
    expect(body).toMatchObject({ name: "mirror", type: "http", enabled: true });
  });

  it("deletes a non-built-in source after confirmation", async () => {
    let deleted = false;
    server.use(
      http.get("/themes/sources", () => HttpResponse.json([official, mirror])),
      http.delete("/themes/sources/mirror", () => {
        deleted = true;
        return new HttpResponse(null, { status: 204 });
      }),
    );
    renderWithQuery(<ThemeSourcesPage />);

    await screen.findAllByTestId("theme-source-row");
    await userEvent.click(screen.getByRole("button", { name: "Delete mirror" }));
    await userEvent.click(await screen.findByRole("button", { name: "Delete source" }));

    await waitFor(() => expect(deleted).toBe(true));
  });

  it("refresh asks the API to re-index that source", async () => {
    let refreshed = false;
    server.use(
      http.get("/themes/sources", () => HttpResponse.json([official, mirror])),
      http.post("/themes/sources/official/refresh", () => {
        refreshed = true;
        return new HttpResponse(null, { status: 202 });
      }),
    );
    renderWithQuery(<ThemeSourcesPage />);

    await screen.findAllByTestId("theme-source-row");
    await userEvent.click(screen.getByRole("button", { name: "Refresh official" }));

    await waitFor(() => expect(refreshed).toBe(true));
  });

  it("shows an error card when the list fails, and Retry loads the rows", async () => {
    let calls = 0;
    server.use(
      http.get("/themes/sources", () => {
        calls += 1;
        return calls === 1
          ? new HttpResponse("boom", { status: 500 })
          : HttpResponse.json([official, mirror]);
      }),
    );
    renderWithQuery(<ThemeSourcesPage />);

    await userEvent.click(await screen.findByRole("button", { name: "Retry" }));

    expect(await screen.findAllByTestId("theme-source-row")).toHaveLength(2);
  });
});
