import { describe, it, expect, vi } from "vitest";
import type { ReactNode } from "react";
import { http, HttpResponse } from "msw";
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { server } from "@/test/server";
import { renderWithQuery } from "@/test/render";
import { makeConfig } from "@/test/factories";
import { IntlProvider } from "react-intl";
import type { AuthProvider } from "@/lib/config";

vi.mock("@tanstack/react-router", () => ({
  Link: ({ children, to, ...rest }: { children: ReactNode; to: string } & Record<string, unknown>) => (
    <a href={to} {...rest}>{children}</a>
  ),
}));

import { AdminSettingsPage } from "./AdminSettings";

async function gotoSection(name: RegExp) {
  await screen.findByRole("heading", { name: /Admin settings/i });
  await userEvent.click(await screen.findByRole("button", { name }));
}

describe("AdminSettings sections", () => {
  it("saves an edited General field", async () => {
    renderWithQuery(<AdminSettingsPage />);
    const nameInput = await screen.findByDisplayValue("Gameplane (mock)");
    await userEvent.clear(nameInput);
    await userEvent.type(nameInput, "gameplane-prod");
    await userEvent.click(within(screen.getByRole("region", { name: "General" })).getByRole("button", { name: /Save changes/i }));
    expect(await screen.findByText("Saved")).toBeInTheDocument();
  });

  it("surfaces a save error from the server", async () => {
    server.use(
      http.put("/admin/config/general", () => HttpResponse.text("namespace invalid", { status: 400 })),
    );
    renderWithQuery(<AdminSettingsPage />);
    const nameInput = await screen.findByDisplayValue("Gameplane (mock)");
    await userEvent.clear(nameInput);
    await userEvent.type(nameInput, "x");
    await userEvent.click(within(screen.getByRole("region", { name: "General" })).getByRole("button", { name: /Save changes/i }));
    expect(await screen.findByText(/namespace invalid/i)).toBeInTheDocument();
  });

  it("refuses to toggle off the last enabled authentication provider", async () => {
    renderWithQuery(<AdminSettingsPage />);
    await gotoSection(/Authentication/i);
    // The seeded local provider is the only enabled one: its toggle is
    // disabled and the lockout hint is shown.
    const toggle = await screen.findByRole("button", { name: /^Enabled$/i });
    expect(toggle).toBeDisabled();
    expect(
      screen.getByText(/At least one identity provider must stay enabled/i),
    ).toBeInTheDocument();
  });

  it("toggles an authentication provider when another stays enabled", async () => {
    server.use(
      http.get("/admin/config", () =>
        HttpResponse.json(
          makeConfig({
            auth: {
              providers: [
                { name: "local", kind: "local", enabled: true },
                { name: "corp-sso", kind: "oidc", enabled: true },
              ],
            },
          }),
        ),
      ),
    );
    renderWithQuery(<AdminSettingsPage />);
    await gotoSection(/Authentication/i);
    const toggles = await screen.findAllByRole("button", { name: /^Enabled$/i });
    expect(toggles).toHaveLength(2);
    await userEvent.click(toggles[1]);
    expect(await screen.findByRole("button", { name: /^Disabled$/i })).toBeInTheDocument();
    // The remaining enabled provider is now locked.
    expect(screen.getByRole("button", { name: /^Enabled$/i })).toBeDisabled();
  });

  it("adds an identity provider: on Save changes the secret is stored first, then the row references it", async () => {
    const calls: string[] = [];
    let secretBody: Record<string, string> | undefined;
    let saved: { providers: Array<Record<string, unknown>> } | undefined;
    server.use(
      http.put("/admin/auth/providers/:name/secret", async ({ params, request }) => {
        calls.push("secret");
        secretBody = (await request.json()) as Record<string, string>;
        return HttpResponse.json({ name: `gameplane-auth-${String(params.name)}`, keys: ["clientSecret"] });
      }),
      http.put("/admin/config/auth", async ({ request }) => {
        calls.push("config");
        saved = (await request.json()) as { providers: Array<Record<string, unknown>> };
        return new HttpResponse(null, { status: 204 });
      }),
    );
    renderWithQuery(<AdminSettingsPage />);
    await gotoSection(/Authentication/i);
    await userEvent.click(await screen.findByRole("button", { name: /Add provider/i }));
    await userEvent.type(screen.getByPlaceholderText("corp-sso"), "corp");
    await userEvent.type(screen.getByPlaceholderText("Acme SSO"), "Acme SSO");
    await userEvent.type(screen.getByPlaceholderText(/idp\.example/i), "https://idp.corp.example");
    const [clientID, clientSecret] = [
      screen.getByLabelText(/Client ID/i),
      screen.getByLabelText(/Client secret/i),
    ];
    await userEvent.type(clientID, "gameplane");
    await userEvent.type(clientSecret, "s3cret");
    await userEvent.click(screen.getByRole("button", { name: /^Add provider$/i }));
    expect(await screen.findByText(/oidc · https:\/\/idp\.corp\.example/i)).toBeInTheDocument();
    // Nothing is stored until the section's Save changes.
    expect(calls).toEqual([]);
    await userEvent.click(screen.getByRole("button", { name: /Save changes/i }));
    await waitFor(() => expect(saved).toBeDefined());
    expect(secretBody).toEqual({ clientSecret: "s3cret" });
    expect(calls).toEqual(["secret", "config"]);
    expect(saved?.providers).toContainEqual({
      name: "corp",
      kind: "oidc",
      displayName: "Acme SSO",
      enabled: true,
      issuer: "https://idp.corp.example",
      clientID: "gameplane",
      configRef: "gameplane-auth-corp",
    });
  });

  it("renders the role-mapping fields and locks Default role until a mapping exists", async () => {
    const user = userEvent.setup();
    renderWithQuery(<AdminSettingsPage />);
    await gotoSection(/Authentication/i);
    await user.click(await screen.findByRole("button", { name: /Add provider/i }));
    expect(screen.getByLabelText(/Scopes/i)).toBeInTheDocument();
    expect(screen.getByLabelText(/Groups claim/i)).toBeInTheDocument();
    expect(screen.getByLabelText(/Admin groups/i)).toBeInTheDocument();
    expect(screen.getByLabelText(/Operator groups/i)).toBeInTheDocument();
    expect(screen.getByLabelText(/Viewer groups/i)).toBeInTheDocument();
    // The API rejects defaultRole without roleMappings, so the select stays
    // locked until at least one group is entered.
    const defaultRole = screen.getByRole("button", { name: /Default role/i });
    expect(defaultRole).toBeDisabled();
    await user.type(screen.getByLabelText(/Admin groups/i), "gp-admins");
    expect(defaultRole).toBeEnabled();
  });

  it("serializes scopes, groups claim, role mappings, and deny into the saved provider", async () => {
    const user = userEvent.setup();
    let saved: { providers: Array<Record<string, unknown>> } | undefined;
    server.use(
      http.put("/admin/auth/providers/:name/secret", ({ params }) =>
        HttpResponse.json({ name: `gameplane-auth-${String(params.name)}`, keys: ["clientSecret"] }),
      ),
      http.put("/admin/config/auth", async ({ request }) => {
        saved = (await request.json()) as { providers: Array<Record<string, unknown>> };
        return new HttpResponse(null, { status: 204 });
      }),
    );
    renderWithQuery(<AdminSettingsPage />);
    await gotoSection(/Authentication/i);
    await user.click(await screen.findByRole("button", { name: /Add provider/i }));
    await user.type(screen.getByPlaceholderText("corp-sso"), "corp");
    await user.type(screen.getByPlaceholderText(/idp\.example/i), "https://idp.corp.example");
    await user.type(screen.getByLabelText(/Client ID/i), "gameplane");
    await user.type(screen.getByLabelText(/Client secret/i), "s3cret");
    // Scopes accept space or comma separators; group lists are
    // comma-separated with whitespace trimmed around each name.
    await user.type(screen.getByLabelText(/Scopes/i), "groups, offline_access");
    await user.type(screen.getByLabelText(/Groups claim/i), "memberOf");
    await user.type(screen.getByLabelText(/Admin groups/i), "GP Admins , platform-admins");
    await user.type(screen.getByLabelText(/Viewer groups/i), "gp-view");
    await user.click(screen.getByRole("button", { name: /Default role/i }));
    await user.click(await screen.findByRole("option", { name: /deny/i }));
    await user.click(screen.getByRole("button", { name: /^Add provider$/i }));
    // FR-015: a non-empty admin-group mapping gates the add behind an
    // explicit confirmation dialog before the provider is actually saved.
    await screen.findByText(/Mapping users to the admin role grants full cluster control/i);
    await userEvent.click(screen.getByRole("button", { name: /Map to admin role/i }));
    await screen.findByText(/oidc · https:\/\/idp\.corp\.example/i);
    await userEvent.click(screen.getByRole("button", { name: /Save changes/i }));
    await waitFor(() => expect(saved).toBeDefined());
    // Exact object: empty inputs (display name, operator groups) stay
    // absent — never "" or [""].
    expect(saved?.providers).toContainEqual({
      name: "corp",
      kind: "oidc",
      enabled: true,
      issuer: "https://idp.corp.example",
      clientID: "gameplane",
      configRef: "gameplane-auth-corp",
      scopes: ["groups", "offline_access"],
      groupsClaim: "memberOf",
      roleMappings: { admin: ["GP Admins", "platform-admins"], viewer: ["gp-view"] },
      defaultRole: "deny",
    });
  }, 15000);

  it("round-trips a stored provider's mapping fields through an unrelated save", async () => {
    const corp: AuthProvider = {
      name: "corp-sso",
      kind: "oidc",
      enabled: true,
      issuer: "https://idp.corp.example",
      clientID: "gameplane",
      configRef: "gameplane-auth-corp-sso",
      scopes: ["groups"],
      groupsClaim: "memberOf",
      roleMappings: { admin: ["gp-admins"] },
      defaultRole: "deny",
    };
    let saved: { providers: Array<Record<string, unknown>> } | undefined;
    server.use(
      http.get("/admin/config", () =>
        HttpResponse.json(
          makeConfig({
            auth: {
              providers: [{ name: "local", kind: "local", enabled: true }, corp],
            },
          }),
        ),
      ),
      http.put("/admin/config/auth", async ({ request }) => {
        saved = (await request.json()) as { providers: Array<Record<string, unknown>> };
        return new HttpResponse(null, { status: 204 });
      }),
    );
    renderWithQuery(<AdminSettingsPage />);
    await gotoSection(/Authentication/i);
    const toggles = await screen.findAllByRole("button", { name: /^Enabled$/i });
    await userEvent.click(toggles[1]);
    await userEvent.click(screen.getByRole("button", { name: /Save changes/i }));
    await waitFor(() => expect(saved).toBeDefined());
    // The toggle flipped enabled; every mapping field survived untouched.
    expect(saved?.providers).toContainEqual({ ...corp, enabled: false });
  });

  it("prefills the issuer for the Google preset", async () => {
    const user = userEvent.setup();
    renderWithQuery(<AdminSettingsPage />);
    await gotoSection(/Authentication/i);
    await user.click(await screen.findByRole("button", { name: /Add provider/i }));
    await user.click(screen.getByRole("button", { name: /Provider kind/i }));
    await user.click(await screen.findByRole("option", { name: /Google/i }));
    expect(screen.getByPlaceholderText(/idp\.example/i)).toHaveValue("https://accounts.google.com");
  });

  it("shows the Helm-flag provider as a locked row and relaxes the last-toggle guard", async () => {
    server.use(
      http.get("/auth/providers", () =>
        HttpResponse.json({
          providers: [
            { name: "local", kind: "local", label: "Local account" },
            { name: "helm", kind: "oidc", label: "Corp SSO" },
          ],
        }),
      ),
    );
    renderWithQuery(<AdminSettingsPage />);
    await gotoSection(/Authentication/i);
    expect(await screen.findByText(/configured via Helm/i)).toBeInTheDocument();
    // With the always-on Helm provider present, even the last dashboard
    // toggle may be turned off — login stays possible.
    const toggle = await screen.findByRole("button", { name: /^Enabled$/i });
    expect(toggle).toBeEnabled();
  });

  it("warns when adding a provider without an External URL", async () => {
    server.use(
      http.get("/admin/config", () =>
        HttpResponse.json(
          makeConfig({
            general: { instanceName: "x", externalURL: "", defaultNamespace: "g" },
          }),
        ),
      ),
    );
    renderWithQuery(<AdminSettingsPage />);
    await gotoSection(/Authentication/i);
    await userEvent.click(await screen.findByRole("button", { name: /Add provider/i }));
    expect(screen.getByText(/External URL/)).toBeInTheDocument();
  });

  it("surfaces a backend rejection when saving the auth section", async () => {
    server.use(
      http.get("/admin/config", () =>
        HttpResponse.json(
          makeConfig({
            auth: {
              providers: [
                { name: "local", kind: "local", enabled: true },
                { name: "corp-sso", kind: "oidc", enabled: true },
              ],
            },
          }),
        ),
      ),
      http.put("/admin/config/auth", () =>
        HttpResponse.text("auth config rejected by server", { status: 422 }),
      ),
    );
    renderWithQuery(<AdminSettingsPage />);
    await gotoSection(/Authentication/i);
    const toggles = await screen.findAllByRole("button", { name: /^Enabled$/i });
    await userEvent.click(toggles[1]);
    await userEvent.click(screen.getByRole("button", { name: /Save changes/i }));
    expect(await screen.findByText(/auth config rejected by server/i)).toBeInTheDocument();
  });

  it("toggles telemetry and saves", async () => {
    let body: unknown;
    server.use(
      http.put("/admin/config/telemetry", async ({ request }) => {
        body = await request.json();
        return new HttpResponse(null, { status: 204 });
      }),
    );
    renderWithQuery(<AdminSettingsPage />);
    await gotoSection(/Telemetry/i);
    const sw = await screen.findByRole("switch", { name: "Enable telemetry" });
    await userEvent.click(sw);
    expect(screen.getByRole("switch", { name: "Disable telemetry" })).toHaveAttribute("aria-checked", "true");
    await userEvent.click(screen.getByRole("button", { name: /Save changes/i }));
    expect(await screen.findByText("Saved")).toBeInTheDocument();
    expect(body).toEqual({ sendMetrics: true, extended: false });
  });

  it("shows the read-only update channel from /cluster/info", async () => {
    renderWithQuery(<AdminSettingsPage />);
    await gotoSection(/Updates/i);
    // The default handler reports updateChannel: "stable"; the section is
    // informational — no select, no save button.
    expect(await screen.findByText("stable")).toBeInTheDocument();
    expect(screen.getByText(/Informational only/i)).toBeInTheDocument();
    expect(screen.queryByTestId("hidden-select-container")).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /Save changes/i })).not.toBeInTheDocument();
  });

  it("toggles a configured notification sink", async () => {
    server.use(
      http.get("/admin/config", () =>
        HttpResponse.json(
          makeConfig({ notifications: { sinks: [{ name: "ops-slack", kind: "slack", enabled: false }] } }),
        ),
      ),
    );
    renderWithQuery(<AdminSettingsPage />);
    await gotoSection(/Notifications/i);
    expect(await screen.findByText("ops-slack")).toBeInTheDocument();
    await userEvent.click(await screen.findByRole("switch", { name: /Enable sink/i }));
    expect(await screen.findByRole("switch", { name: /Disable sink/i })).toBeInTheDocument();
  });

  it("lists backup destinations and opens the add form", async () => {
    renderWithQuery(<AdminSettingsPage />);
    await gotoSection(/Backup destinations/i);
    // The default handler returns one destination ("default").
    await userEvent.click(await screen.findByRole("button", { name: /Add destination/i }));
    const nameField = await screen.findByPlaceholderText("gameplane-backup-repo");
    await userEvent.type(nameField, "repo1");
    await userEvent.type(screen.getByPlaceholderText(/s3:s3.example.com/i), "s3:host/bucket");
    await userEvent.type(screen.getByPlaceholderText(/passphrase/i), "a-strong-passphrase");
    const save = screen.getByRole("button", { name: /Save destination/i });
    await waitFor(() => expect(save).toBeEnabled());
    await userEvent.click(save);
    // On success the form closes; the Add button returns.
    expect(await screen.findByRole("button", { name: /Add destination/i })).toBeInTheDocument();
  });

  it("prompts before deleting a backup destination", async () => {
    renderWithQuery(<AdminSettingsPage />);
    await gotoSection(/Backup destinations/i);
    await userEvent.click(await screen.findByRole("button", { name: /Delete default/i }));
    expect(await screen.findByText(/Delete backup destination\?/i)).toBeInTheDocument();
  });

  it("renders the About section with the license", async () => {
    renderWithQuery(<AdminSettingsPage />);
    await gotoSection(/About/i);
    expect(await screen.findByText("AGPL-3.0")).toBeInTheDocument();
    expect(screen.getByText("Gameplane")).toBeInTheDocument();
  });

  it("saves the instance default language to the i18n section", async () => {
    let body: unknown;
    server.use(
      http.put("/admin/config/i18n", async ({ request }) => {
        body = await request.json();
        return new HttpResponse(null, { status: 204 });
      }),
    );
    renderWithQuery(<AdminSettingsPage />);
    const languageCard = await screen.findByRole("region", { name: "Language" });
    await userEvent.click(within(languageCard).getByRole("button", { name: /Save changes/i }));
    await waitFor(() => expect(body).toEqual({ defaultLanguage: "en" }));
    expect(await within(languageCard).findByText("Saved")).toBeInTheDocument();
  });

  it("shows the catalog text for a server message id when the i18n save is refused", async () => {
    server.use(
      http.put("/admin/config/i18n", () =>
        HttpResponse.json(
          {
            type: "about:blank",
            status: 400,
            title: "Bad Request",
            detail: 'language "xx" is not available',
            messageId: "server.i18n.unlisted_language",
            args: { language: "xx" },
          },
          { status: 400, headers: { "Content-Type": "application/problem+json" } },
        ),
      ),
    );
    renderWithQuery(
      <IntlProvider
        locale="en"
        messages={{ "server.i18n.unlisted_language": 'Language "{language}" is not available in this build.' }}
      >
        <AdminSettingsPage />
      </IntlProvider>,
    );
    const languageCard = await screen.findByRole("region", { name: "Language" });
    await userEvent.click(within(languageCard).getByRole("button", { name: /Save changes/i }));
    expect(
      await within(languageCard).findByText('Language "xx" is not available in this build.'),
    ).toBeInTheDocument();
  });
});
