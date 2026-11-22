import { afterEach, describe, it, expect, vi } from "vitest";
import type { ReactNode } from "react";
import { http, HttpResponse } from "msw";
import { act, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { server } from "@/test/server";
import { renderWithQuery } from "@/test/render";
import { makeConfig } from "@/test/factories";

vi.mock("@tanstack/react-router", () => ({
  Link: ({ children, to, ...rest }: { children: ReactNode; to: string } & Record<string, unknown>) => (
    <a href={to} {...rest}>{children}</a>
  ),
}));

import { AdminSettingsPage } from "./AdminSettings";

afterEach(() => {
  server.resetHandlers();
});

describe("AdminSettingsPage", () => {
  it.each([false, true])("keeps successful writes when their refresh fails, with later edits=%s", async (editDuringSave) => {
    let general = { instanceName: "old-name", externalURL: "https://example.com", defaultNamespace: "gameplane-games" };
    let failReads = false;
    let release!: () => void;
    const pending = new Promise<void>((resolve) => { release = resolve; });
    const save = vi.fn(async ({ request }: { request: Request }) => {
      const submitted = await request.json() as typeof general;
      await pending;
      general = submitted;
      failReads = true;
      return new HttpResponse(null, { status: 204 });
    });
    server.use(
      http.get("/admin/config", () => failReads ? new HttpResponse(null, { status: 500 }) : HttpResponse.json({ general })),
      http.put("/admin/config/general", save),
    );
    const { client } = renderWithQuery(<AdminSettingsPage />);
    const name = await screen.findByDisplayValue("old-name");
    await userEvent.clear(name);
    await userEvent.type(name, "submitted-name");
    await userEvent.click(within(screen.getByRole("region", { name: "General" })).getByRole("button", { name: /Save changes/i }));
    await waitFor(() => expect(save).toHaveBeenCalledTimes(1));
    if (editDuringSave) {
      const url = screen.getByDisplayValue("https://example.com");
      await userEvent.clear(url);
      await userEvent.type(url, "https://later.example.com");
    }
    await act(async () => { release(); });
    await waitFor(() => expect(client.isMutating()).toBe(0));
    expect(client.getQueryState(["config"])?.status).toBe("error");
    expect(screen.getByDisplayValue("submitted-name")).toBeInTheDocument();
    if (!editDuringSave) {
      const url = screen.getByDisplayValue("https://example.com");
      await userEvent.clear(url);
      await userEvent.type(url, "https://later.example.com");
    }
    // A failed read cannot validate a whole-section write against old cache.
    await userEvent.click(within(screen.getByRole("region", { name: "General" })).getByRole("button", { name: /Save changes/i }));
    expect(await screen.findByText(/Could not refresh configuration/i)).toBeInTheDocument();
    expect(save).toHaveBeenCalledTimes(1);
    failReads = false;
    await act(async () => { await client.refetchQueries({ queryKey: ["config"] }); });
    expect(screen.getByDisplayValue("https://later.example.com")).toBeInTheDocument();
    await userEvent.click(within(screen.getByRole("region", { name: "General" })).getByRole("button", { name: /Save changes/i }));
    await waitFor(() => expect(save).toHaveBeenCalledTimes(2));
    expect(general).toEqual({ instanceName: "submitted-name", externalURL: "https://later.example.com", defaultNamespace: "gameplane-games" });
  });

  it("blocks writes when the navigation refresh awaited by Save fails", async () => {
    const general = { instanceName: "old-name", externalURL: "https://example.com", defaultNamespace: "gameplane-games" };
    let reads = 0;
    let release!: () => void;
    const refresh = new Promise<void>((resolve) => { release = resolve; });
    const save = vi.fn(() => new HttpResponse(null, { status: 204 }));
    server.use(
      http.get("/admin/config", async () => {
        if (++reads === 1) return HttpResponse.json({ general });
        await refresh;
        return new HttpResponse(null, { status: 500 });
      }),
      http.put("/admin/config/general", save),
    );
    renderWithQuery(<AdminSettingsPage />);
    await userEvent.type(await screen.findByDisplayValue("old-name"), "-edited");
    await userEvent.click(screen.getByRole("button", { name: /^General$/i }));
    await waitFor(() => expect(reads).toBe(2));
    await userEvent.click(within(screen.getByRole("region", { name: "General" })).getByRole("button", { name: /Save changes/i }));
    await act(async () => { release(); });
    expect(await screen.findByText(/Could not refresh configuration/i)).toBeInTheDocument();
    expect(screen.getByDisplayValue("old-name-edited")).toBeInTheDocument();
    expect(save).not.toHaveBeenCalled();
  });

  it.each([false, true])("rebases a saved section while preserving later edits=%s", async (editDuringSave) => {
    let general = { instanceName: "old-name", externalURL: "https://example.com", defaultNamespace: "gameplane-games" };
    let release!: () => void;
    const pending = new Promise<void>((resolve) => { release = resolve; });
    const save = vi.fn(async ({ request }: { request: Request }) => {
      const submitted = await request.json() as typeof general;
      await pending;
      general = submitted;
      return new HttpResponse(null, { status: 204 });
    });
    server.use(
      http.get("/admin/config", () => HttpResponse.json({ general })),
      http.put("/admin/config/general", save),
    );
    const { client } = renderWithQuery(<AdminSettingsPage />);
    const input = await screen.findByDisplayValue("old-name");
    await userEvent.clear(input);
    await userEvent.type(input, "submitted-name");
    await userEvent.click(within(screen.getByRole("region", { name: "General" })).getByRole("button", { name: /Save changes/i }));
    await waitFor(() => expect(save).toHaveBeenCalledTimes(1));
    if (editDuringSave) {
      await userEvent.clear(input);
      await userEvent.type(input, "later-draft");
    }
    await act(async () => { release(); });
    await waitFor(() => expect(client.isMutating()).toBe(0));
    expect(screen.getByDisplayValue(editDuringSave ? "later-draft" : "submitted-name")).toBeInTheDocument();
    if (editDuringSave) {
      await userEvent.click(within(screen.getByRole("region", { name: "General" })).getByRole("button", { name: /Save changes/i }));
      await waitFor(() => expect(save).toHaveBeenCalledTimes(2));
      expect(general.instanceName).toBe("later-draft");
    } else {
      general = { ...general, externalURL: "https://later.example.com" };
      await userEvent.click(screen.getByRole("button", { name: /^General$/i }));
      expect(await screen.findByDisplayValue("https://later.example.com")).toBeInTheDocument();
    }
  });

  it.each([false, true])("handles a delayed navigation refresh with dirty=%s", async (dirty) => {
    const oldGeneral = { instanceName: "cached-name", externalURL: "https://old.example.com", defaultNamespace: "gameplane-games" };
    const freshGeneral = { ...oldGeneral, externalURL: "https://fresh.example.com" };
    let release!: () => void;
    const refresh = new Promise<void>((resolve) => { release = resolve; });
    let reads = 0;
    let body: unknown;
    const save = vi.fn(async ({ request }: { request: Request }) => {
      body = await request.json();
      return new HttpResponse(null, { status: 204 });
    });
    server.use(
      http.get("/admin/config", async () => {
        if (++reads > 1) await refresh;
        return HttpResponse.json({ general: reads > 1 ? freshGeneral : oldGeneral });
      }),
      http.put("/admin/config/general", save),
    );
    const { client } = renderWithQuery(<AdminSettingsPage />);
    await screen.findByDisplayValue("cached-name");
    // Re-selecting General triggers the same refresh as returning from a tab.
    await userEvent.click(screen.getByRole("button", { name: /^General$/i }));
    await waitFor(() => expect(reads).toBe(2));
    if (dirty) {
      const name = screen.getByDisplayValue("cached-name");
      await userEvent.clear(name);
      await userEvent.type(name, "my-draft");
    }
    await act(async () => { release(); });
    await waitFor(() => expect(client.isFetching({ queryKey: ["config"] })).toBe(0));
    if (dirty) {
      expect(screen.getByDisplayValue("my-draft")).toBeInTheDocument();
      await userEvent.click(within(screen.getByRole("region", { name: "General" })).getByRole("button", { name: /Save changes/i }));
      expect(await screen.findByText(/Configuration changed.*reopen this section/i)).toBeInTheDocument();
      expect(save).not.toHaveBeenCalled();
    } else {
      expect(await screen.findByDisplayValue("https://fresh.example.com")).toBeInTheDocument();
      await userEvent.type(screen.getByDisplayValue("cached-name"), "-edited");
      await userEvent.click(within(screen.getByRole("region", { name: "General" })).getByRole("button", { name: /Save changes/i }));
      await waitFor(() => expect(save).toHaveBeenCalled());
      expect(body).toEqual({ ...freshGeneral, instanceName: "cached-name-edited" });
    }
  });

  it("renders the General section by default", async () => {
    server.use(
      http.get("/admin/config", () =>
        HttpResponse.json({
          general: {
            instanceName: "gameplane-prod",
            externalURL: "https://example.com",
            defaultNamespace: "gameplane-games",
          },
        }),
      ),
    );
    renderWithQuery(<AdminSettingsPage />);
    expect(await screen.findByRole("heading", { name: /Admin settings/i })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /General/i })).toBeInTheDocument();
  });

  it("switches to a different section on click", async () => {
    renderWithQuery(<AdminSettingsPage />);
    const authBtn = await screen.findByRole("button", { name: /Authentication/i });
    await userEvent.click(authBtn);
    // The Authentication tab is now selected — its label is rendered in
    // the active style. Sanity-check via the nav button class change.
    await waitFor(() => {
      expect(authBtn.className).toContain("bg-surface");
    });
  });

  it("renders all section nav buttons", async () => {
    renderWithQuery(<AdminSettingsPage />);
    await screen.findByRole("heading", { name: /Admin settings/i });
    for (const label of [
      "General",
      "Authentication",
      "Module sources",
      "Notifications",
      "Telemetry",
      "Updates",
      "About",
    ]) {
      expect(screen.getByRole("button", { name: new RegExp(label, "i") })).toBeInTheDocument();
    }
    expect(screen.queryByRole("button", { name: "Backup destinations" })).not.toBeInTheDocument();
  });

  it("shows loading state when config is loading", async () => {
    server.use(
      http.get("/admin/config", async () => {
        await new Promise((r) => setTimeout(r, 100));
        return HttpResponse.json({
          general: { instanceName: "", externalURL: "", defaultNamespace: "" },
        });
      }),
    );
    renderWithQuery(<AdminSettingsPage />);
    expect(screen.getByText("Loading configuration…")).toBeInTheDocument();
  });

  it("shows error state when config fails to load", async () => {
    server.use(
      http.get("/admin/config", () =>
        new HttpResponse("Server error", { status: 500 }),
      ),
    );
    renderWithQuery(<AdminSettingsPage />);
    expect(await screen.findByText(/Failed to load configuration/i)).toBeInTheDocument();
  });

  it("navigates to Auth section and shows auth form", async () => {
    server.use(
      http.get("/admin/config", () =>
        HttpResponse.json({
          auth: {
            providers: [
              { name: "Local accounts", kind: "local", enabled: true },
            ],
          },
          general: { instanceName: "", externalURL: "", defaultNamespace: "" },
        }),
      ),
      http.get("/auth/providers", () =>
        HttpResponse.json({
          providers: [{ name: "Local accounts", kind: "local", label: "Local Accounts" }],
        }),
      ),
    );
    renderWithQuery(<AdminSettingsPage />);
    await screen.findByRole("heading", { name: /Admin settings/i });
    await userEvent.click(screen.getByRole("button", { name: /Authentication/i }));
    // Exact string, not a case-insensitive regex: the section subtitle
    // ("Built-in local accounts plus federated identity providers…") also
    // matches /Local accounts/i, so a regex query is ambiguous here.
    expect(await screen.findByText("Local accounts")).toBeInTheDocument();
  });

  it("saves general section changes", async () => {
    const saveHandler = vi.fn(() => new HttpResponse(null, { status: 204 }));
    server.use(
      http.get("/admin/config", () =>
        HttpResponse.json({
          general: {
            instanceName: "old-name",
            externalURL: "https://old.example.com",
            defaultNamespace: "gameplane-games",
          },
        }),
      ),
      http.put("/admin/config/general", saveHandler),
    );
    renderWithQuery(<AdminSettingsPage />);
    const input = await screen.findByDisplayValue("old-name");
    await userEvent.clear(input);
    await userEvent.type(input, "new-name");
    await userEvent.click(within(screen.getByRole("region", { name: "General" })).getByRole("button", { name: /Save changes/i }));
    await waitFor(() => expect(screen.getByText("Saved")).toBeInTheDocument());
    expect(saveHandler).toHaveBeenCalled();
  });

  it("shows error message on save failure", async () => {
    server.use(
      http.get("/admin/config", () =>
        HttpResponse.json({
          general: {
            instanceName: "",
            externalURL: "",
            defaultNamespace: "",
          },
        }),
      ),
      http.put("/admin/config/general", () =>
        new HttpResponse("Validation error", { status: 400 }),
      ),
    );
    renderWithQuery(<AdminSettingsPage />);
    // All three General fields are blank in this fixture, so
    // findByDisplayValue("") would match all of them ambiguously — wait
    // for the section's own Save button to render instead.
    await within(await screen.findByRole("region", { name: "General" })).findByRole("button", { name: /Save changes/i });
    await userEvent.click(within(screen.getByRole("region", { name: "General" })).getByRole("button", { name: /Save changes/i }));
    await waitFor(() =>
      expect(screen.getByText(/Validation error/i)).toBeInTheDocument(),
    );
  });

  it("shows telemetry toggle and saves setting", async () => {
    let body: unknown;
    server.use(
      http.get("/admin/config", () =>
        HttpResponse.json({
          general: { instanceName: "", externalURL: "", defaultNamespace: "" },
          telemetry: { sendMetrics: false, extended: false },
        }),
      ),
      http.put("/admin/config/telemetry", async ({ request }) => {
        body = await request.json();
        return new HttpResponse(null, { status: 204 });
      }),
    );
    renderWithQuery(<AdminSettingsPage />);
    await userEvent.click(screen.getByRole("button", { name: /Telemetry/i }));
    // The Switch component renders role="switch" (a <button>), not a
    // native checkbox input; assert via aria-checked, matching
    // switch.test.tsx's own convention for this component. There are two
    // switches now (basic and extended), so select by accessible name.
    expect(await screen.findAllByRole("switch")).toHaveLength(2);
    const toggle = screen.getByRole("switch", { name: "Enable telemetry" });
    expect(toggle).toHaveAttribute("aria-checked", "false");
    await userEvent.click(toggle);
    await userEvent.click(screen.getByRole("button", { name: /Save changes/i }));
    await waitFor(() => expect(screen.getByText("Saved")).toBeInTheDocument());
    expect(body).toEqual({ sendMetrics: true, extended: false });
  });

  it("navigates to Updates section and shows update channel", async () => {
    server.use(
      http.get("/cluster/info", () =>
        HttpResponse.json({
          updateChannel: "stable",
          version: "v1.28.0",
          gameplaneVersion: "v0.2.0",
        }),
      ),
    );
    renderWithQuery(<AdminSettingsPage />);
    await userEvent.click(screen.getByRole("button", { name: /Updates/i }));
    expect(await screen.findByText("stable")).toBeInTheDocument();
  });

  it("navigates to About section and shows version info", async () => {
    server.use(
      http.get("/cluster/info", () =>
        HttpResponse.json({
          gameplaneVersion: "v0.2.0-beta.7",
          version: "v1.28.0",
        }),
      ),
    );
    renderWithQuery(<AdminSettingsPage />);
    await userEvent.click(screen.getByRole("button", { name: /About/i }));
    expect(await screen.findByText("v0.2.0-beta.7")).toBeInTheDocument();
    expect(screen.getByText("v1.28.0")).toBeInTheDocument();
  });

  it("navigates to Notifications section and shows sink list", async () => {
    server.use(
      http.get("/admin/config", () =>
        HttpResponse.json({
          notifications: {
            sinks: [
              {
                name: "discord-alerts",
                kind: "discord",
                enabled: true,
                configRef: "gameplane-notify-discord-alerts",
                events: ["server.unhealthy"],
              },
            ],
          },
        }),
      ),
    );
    renderWithQuery(<AdminSettingsPage />);
    await userEvent.click(screen.getByRole("button", { name: /Notifications/i }));
    expect(await screen.findByText("discord-alerts")).toBeInTheDocument();
  });

  it("toggles notification sink enabled state", async () => {
    const updateHandler = vi.fn(() => new HttpResponse(null, { status: 204 }));
    server.use(
      http.get("/admin/config", () =>
        HttpResponse.json({
          notifications: {
            sinks: [
              {
                name: "test-sink",
                kind: "slack",
                enabled: true,
                configRef: "gameplane-notify-test",
                events: ["server.unhealthy"],
              },
            ],
          },
        }),
      ),
      http.put("/admin/config/notifications", updateHandler),
    );
    renderWithQuery(<AdminSettingsPage />);
    await userEvent.click(screen.getByRole("button", { name: /Notifications/i }));
    const toggle = await screen.findByLabelText(/Disable sink test-sink/i);
    await userEvent.click(toggle);
    await userEvent.click(screen.getByRole("button", { name: /Save changes/i }));
    await waitFor(() => expect(screen.getByText("Saved")).toBeInTheDocument());
  });

  it("shows unsaved changes warning when test is clicked with dirty form", async () => {
    server.use(
      http.get("/admin/config", () =>
        HttpResponse.json({
          general: { instanceName: "", externalURL: "", defaultNamespace: "" },
          notifications: {
            sinks: [
              {
                name: "test-sink",
                kind: "discord",
                enabled: true,
                configRef: "gameplane-notify-test",
                events: [],
              },
            ],
          },
        }),
      ),
    );
    renderWithQuery(<AdminSettingsPage />);
    await userEvent.click(screen.getByRole("button", { name: /Notifications/i }));
    const toggle = await screen.findByLabelText(/Disable sink test-sink/i);
    await userEvent.click(toggle);
    const testBtn = screen.getByRole("button", { name: /Send test/i });
    // toHaveAttribute compares the attribute value with strict equality
    // unless the expected value is an asymmetric matcher — a bare RegExp
    // literal never matches, so wrap it in stringMatching.
    expect(testBtn).toHaveAttribute("title", expect.stringMatching(/Save changes first/i));
  });

  it("runs test on notification sink", async () => {
    const testHandler = vi.fn(() => new HttpResponse(null, { status: 200 }));
    server.use(
      http.get("/admin/config", () =>
        HttpResponse.json({
          notifications: {
            sinks: [
              {
                name: "test-sink",
                kind: "slack",
                enabled: true,
                configRef: "gameplane-notify-test",
                events: ["server.unhealthy"],
              },
            ],
          },
        }),
      ),
      http.post("/notifications/test-sink:test", testHandler),
    );
    renderWithQuery(<AdminSettingsPage />);
    await userEvent.click(screen.getByRole("button", { name: /Notifications/i }));
    const testBtn = await screen.findByRole("button", { name: /Send test/i });
    await userEvent.click(testBtn);
    await waitFor(() =>
      expect(screen.getByText("✓ delivered")).toBeInTheDocument(),
    );
  });

  it("shows different event chips based on events list", async () => {
    server.use(
      http.get("/admin/config", () =>
        HttpResponse.json({
          notifications: {
            sinks: [
              {
                name: "many-events",
                kind: "webhook",
                enabled: true,
                configRef: "gameplane-notify-many",
                events: ["server.unhealthy", "server.recovered", "backup.failed", "backup.succeeded"],
              },
            ],
          },
        }),
      ),
    );
    renderWithQuery(<AdminSettingsPage />);
    await userEvent.click(screen.getByRole("button", { name: /Notifications/i }));
    expect(await screen.findByText(/\+2/)).toBeInTheDocument();
  });

  it("navigates to Mod Registries section", async () => {
    server.use(
      http.get("/admin/config", () =>
        HttpResponse.json({
          modRegistries: { registries: [] },
        }),
      ),
    );
    renderWithQuery(<AdminSettingsPage />);
    await userEvent.click(screen.getByRole("button", { name: /Mod registries/i }));
    expect(await screen.findByText(/CurseForge/i)).toBeInTheDocument();
    expect(screen.getByText(/Steam Workshop/i)).toBeInTheDocument();
    expect(screen.getByText(/Nexus Mods/i)).toBeInTheDocument();
  });

  it("shows configured state for mod registry with key set", async () => {
    server.use(
      http.get("/admin/config", () =>
        HttpResponse.json({
          modRegistries: {
            registries: [
              { provider: "curseforge", configRef: "gameplane-modreg-curseforge" },
            ],
          },
        }),
      ),
    );
    renderWithQuery(<AdminSettingsPage />);
    await userEvent.click(screen.getByRole("button", { name: /Mod registries/i }));
    expect(await screen.findByText("Configured")).toBeInTheDocument();
  });

  it("disables last enabled auth provider toggle", async () => {
    server.use(
      http.get("/admin/config", () =>
        HttpResponse.json({
          auth: {
            providers: [
              { name: "Local accounts", kind: "local", enabled: true },
            ],
          },
        }),
      ),
      http.get("/admin/auth/providers", () =>
        HttpResponse.json({ providers: [] }),
      ),
    );
    renderWithQuery(<AdminSettingsPage />);
    await userEvent.click(screen.getByRole("button", { name: /Authentication/i }));
    const enabledBtn = await screen.findByRole("button", { name: /Enabled/i });
    expect(enabledBtn).toBeDisabled();
  });

  it("allows disabling provider when helm provider exists", async () => {
    server.use(
      http.get("/admin/config", () =>
        HttpResponse.json({
          auth: {
            providers: [
              { name: "Local accounts", kind: "local", enabled: true },
            ],
          },
        }),
      ),
      // AuthSection's runtime-providers query hits /auth/providers, not
      // the /admin-prefixed secret-management path.
      http.get("/auth/providers", () =>
        HttpResponse.json({
          providers: [
            { name: "helm", kind: "oidc", label: "Helm-managed" },
          ],
        }),
      ),
    );
    renderWithQuery(<AdminSettingsPage />);
    await userEvent.click(screen.getByRole("button", { name: /Authentication/i }));
    const enabledBtn = await screen.findByRole("button", { name: /Enabled/i });
    expect(enabledBtn).not.toBeDisabled();
  });

  it("shows warning when last provider would be disabled", async () => {
    server.use(
      http.get("/admin/config", () =>
        HttpResponse.json({
          auth: {
            providers: [
              { name: "Local accounts", kind: "local", enabled: true },
            ],
          },
        }),
      ),
      http.get("/admin/auth/providers", () =>
        HttpResponse.json({ providers: [] }),
      ),
    );
    renderWithQuery(<AdminSettingsPage />);
    await userEvent.click(screen.getByRole("button", { name: /Authentication/i }));
    expect(
      await screen.findByText(/At least one identity provider must stay enabled/i),
    ).toBeInTheDocument();
  });

  // T026: Helm OIDC provider card renders with data and updates on config change
  it("T026: renders HelmOIDCProviderCard when oidcHelmProvider data is present", async () => {
    server.use(
      http.get("/admin/config", () =>
        HttpResponse.json({
          auth: {
            providers: [
              { name: "Local accounts", kind: "local", enabled: true },
            ],
          },
          installTimeSettings: {
            oidcHelmProvider: {
              groupsClaim: "teams",
              defaultRole: "viewer",
              roleMappings: {
                admin: ["gameplane-admins"],
                operator: ["gameplane-ops"],
                viewer: ["gameplane-members"],
              },
            },
          },
        }),
      ),
      http.get("/auth/providers", () =>
        HttpResponse.json({ providers: [] }),
      ),
    );
    renderWithQuery(<AdminSettingsPage />);
    await userEvent.click(screen.getByRole("button", { name: /Authentication/i }));
    const helmCardHeading = await screen.findByText("Helm-seeded OIDC provider");
    // The heading's immediate ancestor div only wraps the title/subtitle
    // block (see SectionCard) — walk up to the enclosing Card so the query
    // also covers the card's body content (groups claim, mappings, etc).
    // HeroUI v3's Card renders no Tailwind class (its styling comes from
    // "card"/"card--default" component classes, not "rounded-lg"), so scope
    // via the data-slot="card" attribute CardRoot always sets instead.
    const helmCard = helmCardHeading.closest<HTMLElement>('[data-slot="card"]');
    expect(helmCard).toBeInTheDocument();
    expect(within(helmCard!).getByText("teams")).toBeInTheDocument();
    // Scope "viewer" to the default role display, since it appears in both
    // the default role and the viewer role mapping; use within() to disambiguate
    const defaultRoleLabel = within(helmCard!).getByText("Fallback role");
    const defaultRoleContainer = defaultRoleLabel.closest<HTMLElement>("div");
    expect(within(defaultRoleContainer!).getByText("viewer")).toBeInTheDocument();
    // Scope "gameplane-admins" to the Helm-seeded card to distinguish it from the
    // override card below which also has group names
    expect(within(helmCard!).getByText("gameplane-admins")).toBeInTheDocument();
  });

  // T026: FR-012 empty state renders when no role mappings
  it("T026: shows FR-012 empty state when oidcHelmProvider has no role mappings", async () => {
    server.use(
      http.get("/admin/config", () =>
        HttpResponse.json({
          auth: {
            providers: [
              { name: "Local accounts", kind: "local", enabled: true },
            ],
          },
          installTimeSettings: {
            oidcHelmProvider: {
              groupsClaim: "groups",
              defaultRole: "viewer",
              roleMappings: {},
            },
          },
        }),
      ),
      http.get("/auth/providers", () =>
        HttpResponse.json({ providers: [] }),
      ),
    );
    renderWithQuery(<AdminSettingsPage />);
    await userEvent.click(screen.getByRole("button", { name: /Authentication/i }));
    const heading = await screen.findByText("Helm-seeded OIDC provider");
    const helmCard = heading.closest<HTMLElement>('[data-slot="card"]')!;
    expect(
      within(helmCard).getByText("No rules. SSO users get viewer and their role is never changed at sign-in."),
    ).toBeInTheDocument();
  });

  // T026: FR-015 warning banner renders when admin mapping exists
  it("T026: shows FR-015 warning banner when oidcHelmProvider has admin mapping", async () => {
    server.use(
      http.get("/admin/config", () =>
        HttpResponse.json({
          auth: {
            providers: [
              { name: "Local accounts", kind: "local", enabled: true },
            ],
          },
          installTimeSettings: {
            oidcHelmProvider: {
              groupsClaim: "teams",
              defaultRole: "viewer",
              roleMappings: {
                admin: ["gameplane-admins"],
              },
            },
          },
        }),
      ),
      http.get("/auth/providers", () =>
        HttpResponse.json({ providers: [] }),
      ),
    );
    renderWithQuery(<AdminSettingsPage />);
    await userEvent.click(screen.getByRole("button", { name: /Authentication/i }));
    expect(await screen.findByText("Helm-configured admin mapping")).toBeInTheDocument();
    expect(
      screen.getByText(/^Rule 1 above grants admin through Helm values/),
    ).toBeInTheDocument();
  });

  // T026: Updates when config data changes
  it("T026: HelmOIDCProviderCard updates when config data refetches with new values", async () => {
    server.use(
      http.get("/admin/config", () =>
        HttpResponse.json({
          auth: {
            providers: [
              { name: "Local accounts", kind: "local", enabled: true },
            ],
          },
          installTimeSettings: {
            oidcHelmProvider: {
              groupsClaim: "old-claim",
              defaultRole: "viewer",
              roleMappings: {},
            },
          },
        }),
      ),
      http.get("/auth/providers", () =>
        HttpResponse.json({ providers: [] }),
      ),
    );
    renderWithQuery(<AdminSettingsPage />);
    await userEvent.click(screen.getByRole("button", { name: /Authentication/i }));
    expect(await screen.findByText("old-claim")).toBeInTheDocument();

    // Replace with new config. Use server.use() rather than
    // server.resetHandlers(handlers): the latter REPLACES msw's initial
    // handler list for the whole file, so the default /users/me handler
    // vanishes and every later test's useMe() 404s -- which silently
    // turns off every can()-gated section.
    server.use(
      http.get("/admin/config", () =>
        HttpResponse.json({
          auth: {
            providers: [
              { name: "Local accounts", kind: "local", enabled: true },
            ],
          },
          installTimeSettings: {
            oidcHelmProvider: {
              groupsClaim: "new-claim",
              defaultRole: "viewer",
              roleMappings: {},
            },
          },
        }),
      ),
      http.get("/auth/providers", () =>
        HttpResponse.json({ providers: [] }),
      ),
    );

    // Trigger a refetch via navigation (clicking another section and back)
    await userEvent.click(screen.getByRole("button", { name: /General/i }));
    await userEvent.click(screen.getByRole("button", { name: /Authentication/i }));
    expect(await screen.findByText("new-claim")).toBeInTheDocument();
  });

  // T027: Save action calls PUT with helmOverride.roleMappings
  it("T027: role rule override save calls PUT /admin/config/auth with helmOverride.roleRules", async () => {
    let capturedBody: unknown = null;
    const saveHandler = vi.fn(async ({ request }) => {
      capturedBody = await request.json();
      return new HttpResponse(null, { status: 204 });
    });
    server.use(
      http.get("/admin/config", () =>
        HttpResponse.json({
          auth: {
            providers: [
              { name: "Local accounts", kind: "local", enabled: true },
            ],
            helmOverride: {
              roleMappings: {},
            },
          },
          installTimeSettings: {
            oidcHelmProvider: {
              groupsClaim: "groups",
              defaultRole: "viewer",
              roleMappings: {},
            },
          },
        }),
      ),
      http.get("/auth/providers", () =>
        HttpResponse.json({ providers: [] }),
      ),
      http.put("/admin/config/auth", saveHandler),
    );
    renderWithQuery(<AdminSettingsPage />);
    await userEvent.click(screen.getByRole("button", { name: /Authentication/i }));

    // Open the override card and add a rule: group + admin role.
    await userEvent.click(await screen.findByRole("button", { name: "Override" }));
    await userEvent.click(screen.getByRole("button", { name: "Add rule" }));
    await userEvent.type(screen.getByRole("textbox", { name: "Rule 1 group" }), "new-admin-group");
    await userEvent.click(screen.getByRole("button", { name: /Rule 1 role/ }));
    await userEvent.click(await screen.findByRole("option", { name: "admin" }));
    await userEvent.click(screen.getByRole("button", { name: "Save role rules" }));

    // Saving a rule that newly grants admin opens the confirmation dialog.
    await userEvent.click(await screen.findByRole("button", { name: /Map to admin role/ }));
    await waitFor(() => expect(saveHandler).toHaveBeenCalled());

    expect(capturedBody).toBeDefined();
    if (!capturedBody) throw new Error("capturedBody is null");
    const configBody = capturedBody as Record<string, unknown>;
    expect(configBody.helmOverride).toBeDefined();
    const helmOverride = configBody.helmOverride as
      | { roleMappings?: unknown; roleRules?: unknown }
      | undefined;
    expect(helmOverride?.roleMappings).toBeUndefined();
    expect(helmOverride?.roleRules).toContainEqual({ group: "new-admin-group", role: "admin" });
  });

  // T027: Reset to Helm default drops the override; the next save writes it
  it("T027: Reset to Helm default removes the whole override on the next save", async () => {
    let capturedBody: unknown = null;
    const saveHandler = vi.fn(async ({ request }) => {
      capturedBody = await request.json();
      return new HttpResponse(null, { status: 204 });
    });
    const resetHandler = vi.fn(() => new HttpResponse(null, { status: 204 }));
    server.use(
      http.get("/admin/config", () =>
        HttpResponse.json({
          auth: {
            providers: [
              { name: "Local accounts", kind: "local", enabled: true },
            ],
            helmOverride: {
              roleMappings: {
                operator: ["gameplane-ops"], // Only operator has an override
              },
            },
          },
          installTimeSettings: {
            oidcHelmProvider: {
              groupsClaim: "groups",
              defaultRole: "viewer",
              roleMappings: {
                operator: ["gameplane-operators"],
              },
            },
          },
        }),
      ),
      http.get("/auth/providers", () =>
        HttpResponse.json({ providers: [] }),
      ),
      http.delete("/admin/config/auth/role-mappings/operator", resetHandler),
      http.put("/admin/config/auth", saveHandler),
    );
    renderWithQuery(<AdminSettingsPage />);
    await userEvent.click(screen.getByRole("button", { name: /Authentication/i }));

    // Reset removes the whole override in the draft; the next section save writes it.
    await userEvent.click(await screen.findByRole("button", { name: "Reset to Helm default" }));
    expect(await screen.findByText("From Helm values")).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: /^Save changes$/i }));
    await waitFor(() => expect(saveHandler).toHaveBeenCalled());
    expect((capturedBody as { helmOverride?: unknown }).helmOverride).toBeUndefined();
    expect(resetHandler).not.toHaveBeenCalled();
  });

  // T027: Empty-list override [] is treated as override, not absent
  it("T027: empty-list override [] is treated as override, not absent (nobody)", async () => {
    server.use(
      http.get("/admin/config", () =>
        HttpResponse.json({
          auth: {
            providers: [
              { name: "Local accounts", kind: "local", enabled: true },
            ],
            helmOverride: {
              roleMappings: {
                viewer: [], // Empty list override — DISTINCT from absent
              },
            },
          },
          installTimeSettings: {
            oidcHelmProvider: {
              groupsClaim: "groups",
              defaultRole: "viewer",
              roleMappings: {
                viewer: ["gameplane-members"], // Helm-seeded, will be hidden by override
              },
            },
          },
        }),
      ),
      http.get("/auth/providers", () =>
        HttpResponse.json({ providers: [] }),
      ),
    );
    renderWithQuery(<AdminSettingsPage />);
    await userEvent.click(screen.getByRole("button", { name: /Authentication/i }));

    // The empty-list override [] means "nobody signs in as viewer via OIDC"
    // even though Helm seeded it with ["gameplane-members"]. The dashboard
    // override takes precedence. Provenance should read "Overridden in dashboard".
    const provenanceText = await screen.findByText("Overridden in dashboard");
    expect(provenanceText).toBeInTheDocument();

    // The effective rule list is empty: the empty-state line shows.
    expect(
      screen.getAllByText("No rules. SSO users get viewer and their role is never changed at sign-in.").length,
    ).toBeGreaterThan(0);
    // `viewer: []` replaces the Helm viewer groups: the effective list has no gameplane-members rule.
    expect(screen.queryByRole("textbox", { name: "Rule 1 group" })).not.toBeInTheDocument();

    // Most importantly: Reset button MUST appear (only shows when override exists)
    // This distinguishes [] (override exists) from undefined (no override, use Helm)
    expect(screen.getByRole("button", { name: "Reset to Helm default" })).toBeInTheDocument();
  });

  // T027: Displayed provenance updates after reset
  it("T027: provenance updates from 'Overridden in dashboard' to 'From Helm values' after reset", async () => {
    const configState: Record<string, unknown> = {
      auth: {
        providers: [
          { name: "Local accounts", kind: "local", enabled: true },
        ],
        helmOverride: {
          roleMappings: {
            admin: ["dashboard-admins"], // Initially overridden
          },
        },
      },
      installTimeSettings: {
        oidcHelmProvider: {
          groupsClaim: "groups",
          defaultRole: "viewer",
          roleMappings: {
            admin: ["helm-admins"], // Helm-seeded value
          },
        },
      },
    };

    server.use(
      http.get("/admin/config", () => HttpResponse.json(configState)),
      http.get("/auth/providers", () =>
        HttpResponse.json({ providers: [] }),
      ),
    );
    renderWithQuery(<AdminSettingsPage />);
    await userEvent.click(screen.getByRole("button", { name: /Authentication/i }));

    // Initially admin should show "Overridden in dashboard"
    expect(
      await screen.findByText(/Overridden in dashboard/),
    ).toBeInTheDocument();

    // Reset edits the draft, so the provenance flips at once.
    await userEvent.click(screen.getByRole("button", { name: "Reset to Helm default" }));
    expect(await screen.findByText("From Helm values")).toBeInTheDocument();
    expect(screen.queryByText("Overridden in dashboard")).not.toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Override" })).toBeInTheDocument();
  });

  // T031: Admin warning appears with exact copy and confirmation required
  it("T031: admin role mapping warning appears with exact copy and requires confirmation", async () => {
    server.use(
      http.get("/admin/config", () =>
        HttpResponse.json({
          auth: {
            providers: [
              { name: "Local accounts", kind: "local", enabled: true },
            ],
            helmOverride: {
              roleMappings: {},
            },
          },
          installTimeSettings: {
            oidcHelmProvider: {
              groupsClaim: "groups",
              defaultRole: "viewer",
              roleMappings: {},
            },
          },
        }),
      ),
      http.get("/auth/providers", () =>
        HttpResponse.json({ providers: [] }),
      ),
      http.put("/admin/config/auth", () => new HttpResponse(null, { status: 204 })),
    );
    renderWithQuery(<AdminSettingsPage />);
    await userEvent.click(screen.getByRole("button", { name: /Authentication/i }));

    // Add a rule that grants admin, then save the override.
    await userEvent.click(await screen.findByRole("button", { name: "Override" }));
    await userEvent.click(screen.getByRole("button", { name: "Add rule" }));
    await userEvent.type(screen.getByRole("textbox", { name: "Rule 1 group" }), "new-admin");
    await userEvent.click(screen.getByRole("button", { name: /Rule 1 role/ }));
    await userEvent.click(await screen.findByRole("option", { name: "admin" }));
    await userEvent.click(screen.getByRole("button", { name: "Save role rules" }));

    // Dialog should appear with exact warning text. The three sentences render
    // as one flowing paragraph (a single text node), so a plain exact-match
    // string here would never match — only a substring/regex query does,
    // matching the pattern already used for the next two assertions.
    expect(
      await screen.findByText(/Mapping users to the admin role grants full cluster control/),
    ).toBeInTheDocument();
    expect(
      screen.getByText(/Ensure the mapped group contains only authorized personnel/),
    ).toBeInTheDocument();
    expect(
      screen.getByText(/Anyone in these groups gets full admin access from their next login/),
    ).toBeInTheDocument();

    // Confirm button should be present
    const confirmBtn = await screen.findByRole("button", { name: /Map to admin role/ });
    expect(confirmBtn).toBeInTheDocument();
  });

  // T031: Unconfirmed dialog does NOT save
  it("T031: unconfirmed admin dialog does not add group when cancelled", async () => {
    const saveHandler = vi.fn(() => new HttpResponse(null, { status: 204 }));
    server.use(
      http.get("/admin/config", () =>
        HttpResponse.json({
          auth: {
            providers: [
              { name: "Local accounts", kind: "local", enabled: true },
            ],
            helmOverride: {
              roleMappings: {},
            },
          },
          installTimeSettings: {
            oidcHelmProvider: {
              groupsClaim: "groups",
              defaultRole: "viewer",
              roleMappings: {},
            },
          },
        }),
      ),
      http.get("/auth/providers", () =>
        HttpResponse.json({ providers: [] }),
      ),
      http.put("/admin/config/auth", saveHandler),
    );
    renderWithQuery(<AdminSettingsPage />);
    await userEvent.click(screen.getByRole("button", { name: /Authentication/i }));

    // Add a rule that grants admin, then save: the confirmation opens on save.
    await userEvent.click(await screen.findByRole("button", { name: "Override" }));
    await userEvent.click(screen.getByRole("button", { name: "Add rule" }));
    await userEvent.type(screen.getByRole("textbox", { name: "Rule 1 group" }), "test-admin");
    await userEvent.click(screen.getByRole("button", { name: /Rule 1 role/ }));
    await userEvent.click(await screen.findByRole("option", { name: "admin" }));
    await userEvent.click(screen.getByRole("button", { name: "Save role rules" }));

    // Dialog appears with confirmation button
    expect(await screen.findByRole("button", { name: /Map to admin role/ })).toBeInTheDocument();

    // Close dialog by pressing Escape (standard dialog behavior): cancels the save.
    await userEvent.keyboard("{Escape}");
    await waitFor(() =>
      expect(screen.queryByRole("button", { name: /Map to admin role/ })).not.toBeInTheDocument(),
    );

    // Verify save was not called
    expect(saveHandler).not.toHaveBeenCalled();
  });

  // T031: Exact warning copy from design spec
  it("T031: admin confirmation dialog contains exact warning text from design", async () => {
    server.use(
      http.get("/admin/config", () =>
        HttpResponse.json({
          auth: {
            providers: [
              { name: "Local accounts", kind: "local", enabled: true },
            ],
            helmOverride: {
              roleMappings: {},
            },
          },
          installTimeSettings: {
            oidcHelmProvider: {
              groupsClaim: "groups",
              defaultRole: "viewer",
              roleMappings: {},
            },
          },
        }),
      ),
      http.get("/auth/providers", () =>
        HttpResponse.json({ providers: [] }),
      ),
    );
    renderWithQuery(<AdminSettingsPage />);
    await userEvent.click(screen.getByRole("button", { name: /Authentication/i }));

    // Add a rule that grants admin, then save: the confirmation opens on save.
    await userEvent.click(await screen.findByRole("button", { name: "Override" }));
    await userEvent.click(screen.getByRole("button", { name: "Add rule" }));
    await userEvent.type(screen.getByRole("textbox", { name: "Rule 1 group" }), "security-team");
    await userEvent.click(screen.getByRole("button", { name: /Rule 1 role/ }));
    await userEvent.click(await screen.findByRole("option", { name: "admin" }));
    await userEvent.click(screen.getByRole("button", { name: "Save role rules" }));

    // Exact copy from design spec (FR-015)
    const exactWarning = "Mapping users to the admin role grants full cluster control. Ensure the mapped group contains only authorized personnel. Anyone in these groups gets full admin access from their next login.";
    const allText = screen.getByText(/Mapping users to the admin role/).textContent || "";
    expect(allText).toContain("Mapping users to the admin role grants full cluster control");
    expect(allText).toContain("Ensure the mapped group contains only authorized personnel");
    expect(allText).toContain("Anyone in these groups gets full admin access from their next login");
    expect(allText).toContain(exactWarning);
  });

  // T050: Admin mapping warning in provider editor
  it("T050: shows FR-015 warning when adding admin groups in provider editor", async () => {
    server.use(
      http.get("/admin/config", () =>
        HttpResponse.json({
          auth: {
            providers: [
              { name: "Local accounts", kind: "local", enabled: true },
            ],
          },
          general: { instanceName: "", externalURL: "https://example.com", defaultNamespace: "" },
        }),
      ),
      http.get("/auth/providers", () =>
        HttpResponse.json({ providers: [] }),
      ),
    );
    renderWithQuery(<AdminSettingsPage />);
    await userEvent.click(screen.getByRole("button", { name: /Authentication/i }));

    // Find the Add provider button
    const addBtn = await screen.findByRole("button", { name: "Add provider" });
    await userEvent.click(addBtn);

    // Fill in the form with admin groups
    const nameInput = await screen.findByLabelText("Name");
    await userEvent.type(nameInput, "test-provider");

    const issuerInput = screen.getByLabelText("Issuer URL");
    await userEvent.type(issuerInput, "https://example.com");

    const clientIDInput = screen.getByLabelText("Client ID");
    await userEvent.type(clientIDInput, "test-client");

    const clientSecretInput = screen.getByLabelText("Client secret");
    await userEvent.type(clientSecretInput, "test-secret");

    // Add a rule that grants admin
    await userEvent.click(screen.getByRole("button", { name: "Add rule" }));
    await userEvent.type(screen.getByRole("textbox", { name: "Rule 1 group" }), "admin-group");
    await userEvent.click(screen.getByRole("button", { name: /Rule 1 role/ }));
    await userEvent.click(await screen.findByRole("option", { name: "admin" }));

    // Click Add provider button (the one in the form)
    const submitBtn = screen.getByRole("button", { name: "Add provider" });
    await userEvent.click(submitBtn);

    // Dialog should appear with exact warning text
    expect(
      await screen.findByText(/Mapping users to the admin role grants full cluster control/),
    ).toBeInTheDocument();
    expect(
      screen.getByText(/Ensure the mapped group contains only authorized personnel/),
    ).toBeInTheDocument();
    expect(
      screen.getByText(/Anyone in these groups gets full admin access from their next login/),
    ).toBeInTheDocument();

    // Confirm button should be present
    const confirmBtn = await screen.findByRole("button", { name: /Map to admin role/ });
    expect(confirmBtn).toBeInTheDocument();
  });

  // T051: Confirmation required in provider editor
  it("T051: requires confirmation before adding provider with admin groups", async () => {
    const addHandler = vi.fn(() => new HttpResponse(null, { status: 204 }));
    const secretHandler = vi.fn(() =>
      HttpResponse.json({
        name: "test-provider-secret",
      }),
    );
    server.use(
      http.get("/admin/config", () =>
        HttpResponse.json({
          auth: {
            providers: [
              { name: "Local accounts", kind: "local", enabled: true },
            ],
          },
          general: { instanceName: "", externalURL: "https://example.com", defaultNamespace: "" },
        }),
      ),
      http.get("/auth/providers", () =>
        HttpResponse.json({ providers: [] }),
      ),
      http.put("/admin/auth/providers/test-provider/secret", secretHandler),
      http.put("/admin/config/auth", addHandler),
    );
    renderWithQuery(<AdminSettingsPage />);
    await userEvent.click(screen.getByRole("button", { name: /Authentication/i }));

    // Find the Add provider button
    const addBtn = await screen.findByRole("button", { name: "Add provider" });
    await userEvent.click(addBtn);

    // Fill in the form
    const nameInput = await screen.findByLabelText("Name");
    await userEvent.type(nameInput, "test-provider");

    const issuerInput = screen.getByLabelText("Issuer URL");
    await userEvent.type(issuerInput, "https://example.com");

    const clientIDInput = screen.getByLabelText("Client ID");
    await userEvent.type(clientIDInput, "test-client");

    const clientSecretInput = screen.getByLabelText("Client secret");
    await userEvent.type(clientSecretInput, "test-secret");

    // Add a rule that grants admin
    await userEvent.click(screen.getByRole("button", { name: "Add rule" }));
    await userEvent.type(screen.getByRole("textbox", { name: "Rule 1 group" }), "admin-group");
    await userEvent.click(screen.getByRole("button", { name: /Rule 1 role/ }));
    await userEvent.click(await screen.findByRole("option", { name: "admin" }));

    // Click Add provider button
    const submitBtn = screen.getByRole("button", { name: "Add provider" });
    await userEvent.click(submitBtn);

    // Dialog appears — close it without confirming
    await screen.findByText(/Mapping users to the admin role grants full cluster control/);
    await userEvent.keyboard("{Escape}");

    // Verify the secret handler was never called (provider was not added)
    expect(secretHandler).not.toHaveBeenCalled();

    // Verify the config update was not called
    expect(addHandler).not.toHaveBeenCalled();

    // Dialog should be closed
    await waitFor(() => {
      expect(screen.queryByRole("button", { name: /Map to admin role/ })).not.toBeInTheDocument();
    });
  });

  // T051: Confirmation completes the add
  it("T051: provider with admin groups is added after confirmation", async () => {
    const secretHandler = vi.fn(() =>
      HttpResponse.json({
        name: "test-provider-secret",
      }),
    );
    server.use(
      http.get("/admin/config", () =>
        HttpResponse.json({
          auth: {
            providers: [
              { name: "Local accounts", kind: "local", enabled: true },
            ],
          },
          general: { instanceName: "", externalURL: "https://example.com", defaultNamespace: "" },
        }),
      ),
      http.get("/auth/providers", () =>
        HttpResponse.json({ providers: [] }),
      ),
      http.put("/admin/auth/providers/test-provider/secret", secretHandler),
    );
    renderWithQuery(<AdminSettingsPage />);
    await userEvent.click(screen.getByRole("button", { name: /Authentication/i }));

    // Find and click the Add provider button
    const addBtn = await screen.findByRole("button", { name: "Add provider" });
    await userEvent.click(addBtn);

    // Fill in the form with admin groups
    const nameInput = await screen.findByLabelText("Name");
    await userEvent.type(nameInput, "test-provider");

    const issuerInput = screen.getByLabelText("Issuer URL");
    await userEvent.type(issuerInput, "https://example.com");

    const clientIDInput = screen.getByLabelText("Client ID");
    await userEvent.type(clientIDInput, "test-client");

    const clientSecretInput = screen.getByLabelText("Client secret");
    await userEvent.type(clientSecretInput, "test-secret");

    // Add a rule that grants admin
    await userEvent.click(screen.getByRole("button", { name: "Add rule" }));
    await userEvent.type(screen.getByRole("textbox", { name: "Rule 1 group" }), "admin-group");
    await userEvent.click(screen.getByRole("button", { name: /Rule 1 role/ }));
    await userEvent.click(await screen.findByRole("option", { name: "admin" }));

    // Click Add provider button
    const submitBtn = screen.getByRole("button", { name: "Add provider" });
    await userEvent.click(submitBtn);

    // Dialog appears
    await screen.findByText(/Mapping users to the admin role grants full cluster control/);

    // Click the confirm button
    const confirmBtn = screen.getByRole("button", { name: /Map to admin role/ });
    await userEvent.click(confirmBtn);

    // The provider row lands in the draft; its secret is stored by the
    // section's Save changes.
    await screen.findByText(/oidc · https:\/\/example\.com/i);
    expect(secretHandler).not.toHaveBeenCalled();
    await userEvent.click(screen.getByRole("button", { name: /^Save changes$/i }));

    // Verify the secret was stored (indicating the form was submitted)
    await waitFor(() => {
      expect(secretHandler).toHaveBeenCalled();
    });
  });
});

describe("AdminSettingsPage draft and managed Secret lifecycle", () => {
  const helmAdminInstall = {
    oidcHelmProvider: {
      groupsClaim: "groups",
      defaultRole: "viewer",
      roleMappings: { admin: ["helm-admins"] },
    },
  };

  it("keeps a reset role mapping reset through the next section save", async () => {
    let configState: Record<string, unknown> = {
      auth: {
        providers: [{ name: "Local accounts", kind: "local", enabled: true }],
        helmOverride: { roleMappings: { admin: ["dashboard-admins"] } },
      },
      installTimeSettings: helmAdminInstall,
    };
    let saved: Record<string, unknown> | undefined;
    server.use(
      http.get("/admin/config", () => HttpResponse.json(configState)),
      http.get("/auth/providers", () => HttpResponse.json({ providers: [] })),
      http.delete("/admin/config/auth/role-mappings/admin", () => {
        configState = {
          auth: { providers: [{ name: "Local accounts", kind: "local", enabled: true }] },
          installTimeSettings: helmAdminInstall,
        };
        return new HttpResponse(null, { status: 204 });
      }),
      http.put("/admin/config/auth", async ({ request }) => {
        saved = (await request.json()) as Record<string, unknown>;
        return new HttpResponse(null, { status: 204 });
      }),
    );
    renderWithQuery(<AdminSettingsPage />);
    await userEvent.click(screen.getByRole("button", { name: /Authentication/i }));
    await userEvent.click(await screen.findByRole("button", { name: /Reset to Helm default/ }));

    // The card shows the Helm value without a reload.
    await waitFor(() =>
      expect(screen.queryByText("Overridden in dashboard")).not.toBeInTheDocument(),
    );
    expect(screen.queryByRole("button", { name: /Reset to Helm default/ })).not.toBeInTheDocument();

    // The next save of the section doesn't carry the reset override.
    await userEvent.click(screen.getByRole("button", { name: /^Save changes$/i }));
    await waitFor(() => expect(saved).toBeDefined());
    expect(saved?.helmOverride).toBeUndefined();
  });

  it("reports a failed save after a reset and keeps the reset in the draft", async () => {
    server.use(
      http.get("/admin/config", () =>
        HttpResponse.json({
          auth: {
            providers: [{ name: "Local accounts", kind: "local", enabled: true }],
            helmOverride: { roleMappings: { admin: ["dashboard-admins"] } },
          },
          installTimeSettings: helmAdminInstall,
        }),
      ),
      http.get("/auth/providers", () => HttpResponse.json({ providers: [] })),
    );
    renderWithQuery(<AdminSettingsPage />);
    await userEvent.click(screen.getByRole("button", { name: /Authentication/i }));
    server.use(http.put("/admin/config/auth", () => HttpResponse.text("save refused", { status: 500 })));
    await userEvent.click(await screen.findByRole("button", { name: "Reset to Helm default" }));
    await userEvent.click(screen.getByRole("button", { name: /^Save changes$/i }));

    expect(await screen.findByText("save refused")).toBeInTheDocument();
    // The reset stays in the draft so the user can retry.
    expect(screen.getByText("From Helm values")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /^Save changes$/i })).toBeEnabled();
  });

  it("keeps a removed sink's Secret until the removal is saved", async () => {
    const calls: string[] = [];
    server.use(
      http.get("/admin/config", () =>
        HttpResponse.json(
          makeConfig({
            notifications: {
              sinks: [
                {
                  name: "team-alerts",
                  kind: "discord",
                  enabled: true,
                  configRef: "gameplane-notify-team-alerts",
                },
              ],
            },
          }),
        ),
      ),
      http.delete("/admin/notifications/sinks/:name/secret", ({ params }) => {
        calls.push(`delete:${String(params.name)}`);
        return new HttpResponse(null, { status: 204 });
      }),
      http.put("/admin/config/notifications", () => {
        calls.push("config");
        return new HttpResponse(null, { status: 204 });
      }),
    );
    renderWithQuery(<AdminSettingsPage />);
    await userEvent.click(await screen.findByRole("button", { name: /Notifications/i }));
    await screen.findByText("team-alerts");
    await userEvent.click(screen.getByRole("button", { name: /Delete sink team-alerts/i }));
    expect(await screen.findByText(/No notification sinks configured/i)).toBeInTheDocument();

    // Leaving the section without saving discards the draft and touches no Secret.
    await userEvent.click(screen.getByRole("button", { name: /General/i }));
    await userEvent.click(screen.getByRole("button", { name: /Notifications/i }));
    expect(await screen.findByText("team-alerts")).toBeInTheDocument();
    expect(calls).toEqual([]);

    // A saved removal removes the Secret after the config stops referencing it.
    await userEvent.click(screen.getByRole("button", { name: /Delete sink team-alerts/i }));
    await userEvent.click(screen.getByRole("button", { name: /^Save changes$/i }));
    await waitFor(() => expect(calls).toEqual(["config", "delete:team-alerts"]));
  });

  it("stores no Secret for a sink that is added but never saved", async () => {
    const secretWrites = vi.fn(() =>
      HttpResponse.json({ name: "gameplane-notify-ops", keys: ["url"] }),
    );
    server.use(http.put("/admin/notifications/sinks/:name/secret", secretWrites));
    renderWithQuery(<AdminSettingsPage />);
    await userEvent.click(await screen.findByRole("button", { name: /Notifications/i }));
    await userEvent.click(await screen.findByRole("button", { name: /Add sink/i }));
    await userEvent.type(screen.getByPlaceholderText("team-alerts"), "ops");
    await userEvent.type(
      screen.getByPlaceholderText(/discord\.com/i),
      "https://discord.com/api/webhooks/1/x",
    );
    await userEvent.click(screen.getByRole("button", { name: /^Add sink$/i }));
    expect(await screen.findByText(/discord · Secret: gameplane-notify-ops/i)).toBeInTheDocument();

    await userEvent.click(screen.getByRole("button", { name: /General/i }));
    await userEvent.click(screen.getByRole("button", { name: /Notifications/i }));
    expect(await screen.findByText(/No notification sinks configured/i)).toBeInTheDocument();
    expect(secretWrites).not.toHaveBeenCalled();
  });

  it("stores no client secret for a provider that is added but never saved", async () => {
    const secretWrites = vi.fn(() =>
      HttpResponse.json({ name: "gameplane-auth-corp", keys: ["clientSecret"] }),
    );
    server.use(http.put("/admin/auth/providers/:name/secret", secretWrites));
    renderWithQuery(<AdminSettingsPage />);
    await userEvent.click(await screen.findByRole("button", { name: /Authentication/i }));
    await userEvent.click(await screen.findByRole("button", { name: /Add provider/i }));
    await userEvent.type(screen.getByPlaceholderText("corp-sso"), "corp");
    await userEvent.type(screen.getByPlaceholderText(/idp\.example/i), "https://idp.corp.example");
    await userEvent.type(screen.getByLabelText(/Client ID/i), "gameplane");
    await userEvent.type(screen.getByLabelText(/Client secret/i), "s3cret");
    await userEvent.click(screen.getByRole("button", { name: /^Add provider$/i }));
    expect(await screen.findByText(/oidc · https:\/\/idp\.corp\.example/i)).toBeInTheDocument();

    await userEvent.click(screen.getByRole("button", { name: /General/i }));
    await userEvent.click(screen.getByRole("button", { name: /Authentication/i }));
    await screen.findByRole("button", { name: /Add provider/i });
    expect(screen.queryByText(/oidc · https:\/\/idp\.corp\.example/i)).not.toBeInTheDocument();
    expect(secretWrites).not.toHaveBeenCalled();
  });
});
