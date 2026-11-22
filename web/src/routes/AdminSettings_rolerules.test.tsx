import { describe, it, expect, vi } from "vitest";
import type { ReactNode } from "react";
import { http, HttpResponse } from "msw";
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { server } from "@/test/server";
import { renderWithQuery } from "@/test/render";
import { makeConfig } from "@/test/factories";
import { rolesWithCustomRoleHandler } from "@/test/handlers";
import type { AuthCfg, AuthProvider, InstallTimeSettings } from "@/lib/config";

vi.mock("@tanstack/react-router", () => ({
  Link: ({ children, to, ...rest }: { children: ReactNode; to: string } & Record<string, unknown>) => (
    <a href={to} {...rest}>{children}</a>
  ),
}));

import { AdminSettingsPage } from "./AdminSettings";

// Spec 034 T025: the ordered role rule editor (design UdnBT / mrF6f) in the
// add-provider form, the read-only Helm card and the Helm override card.

type User = ReturnType<typeof userEvent.setup>;

const helmInstall: InstallTimeSettings = {
  gameDataStorageClass: "",
  oidcHelmProvider: {
    groupsClaim: "teams",
    defaultRole: "deny",
    roleRules: [
      { group: "gp-admins", role: "admin" },
      { group: "gp-ops", role: "operator" },
    ],
  },
};

function serveAuth(auth: AuthCfg, installTimeSettings?: InstallTimeSettings) {
  let saved: AuthCfg | undefined;
  const put = vi.fn(async ({ request }: { request: Request }) => {
    saved = (await request.json()) as AuthCfg;
    return new HttpResponse(null, { status: 204 });
  });
  server.use(
    http.get("/admin/config", () => HttpResponse.json({ ...makeConfig({ auth }), ...(installTimeSettings ? { installTimeSettings } : {}) })),
    http.put("/admin/config/auth", put),
    http.put("/admin/auth/providers/:name/secret", ({ params }) =>
      HttpResponse.json({ name: `gameplane-auth-${String(params.name)}`, keys: ["clientSecret"] }),
    ),
  );
  return { put, saved: () => saved };
}

async function gotoAuth(user: User) {
  renderWithQuery(<AdminSettingsPage />);
  await screen.findByRole("heading", { name: /Admin settings/i });
  await user.click(await screen.findByRole("button", { name: /Authentication/i }));
}

async function openProviderForm(user: User) {
  await gotoAuth(user);
  await user.click(await screen.findByRole("button", { name: /Add provider/i }));
  await user.type(screen.getByPlaceholderText("corp-sso"), "corp");
  await user.type(screen.getByPlaceholderText(/idp\.example/i), "https://idp.corp.example");
  await user.type(screen.getByLabelText(/Client ID/i), "gameplane");
  await user.type(screen.getByLabelText(/Client secret/i), "s3cret");
}

async function pick(user: User, trigger: RegExp, option: string, scope: HTMLElement = document.body) {
  await user.click(within(scope).getByRole("button", { name: trigger }));
  await user.click(await screen.findByRole("option", { name: option }));
}

const groupField = (n: number, scope: HTMLElement = document.body) =>
  within(scope).getByRole("textbox", { name: `Rule ${n} group` });

function card(title: string): HTMLElement {
  const el = screen.getByText(title).closest<HTMLElement>('[data-slot="card"]');
  if (!el) throw new Error(`no card titled ${title}`);
  return el;
}

describe("AdminSettings role rules: provider form", () => {
  it("shows the empty state and locks the fallback until a rule exists", async () => {
    const user = userEvent.setup();
    serveAuth({ providers: [{ name: "local", kind: "local", enabled: true }] });
    await gotoAuth(user);
    await user.click(await screen.findByRole("button", { name: /Add provider/i }));
    expect(screen.getByText("No rules. SSO users get viewer and their role is never changed at sign-in.")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /Fallback role/ })).toBeDisabled();
    await user.click(screen.getByRole("button", { name: "Add rule" }));
    expect(groupField(1)).toHaveValue("");
    expect(screen.getByRole("button", { name: /Fallback role/ })).toBeEnabled();
  });

  it("adds, reorders and removes rules, then saves them as roleRules with the fallback", async () => {
    const user = userEvent.setup();
    const api = serveAuth({ providers: [{ name: "local", kind: "local", enabled: true }] });
    await openProviderForm(user);
    for (let i = 0; i < 3; i++) await user.click(screen.getByRole("button", { name: "Add rule" }));
    await user.type(groupField(1), "sys-admin");
    await user.type(groupField(2), "ops");
    await user.type(groupField(3), "everyone");
    await pick(user, /Rule 1 role/, "admin");
    await pick(user, /Rule 2 role/, "operator");
    // Rule 3 keeps the new-rule default, viewer.
    await user.click(screen.getByRole("button", { name: "Move rule 3 up" }));
    expect(groupField(2)).toHaveValue("everyone");
    expect(groupField(3)).toHaveValue("ops");
    expect(screen.getByRole("button", { name: "Move rule 1 up" })).toBeDisabled();
    expect(screen.getByRole("button", { name: "Move rule 3 down" })).toBeDisabled();
    await user.click(screen.getByRole("button", { name: "Remove rule 3" }));
    expect(screen.queryByRole("textbox", { name: "Rule 3 group" })).not.toBeInTheDocument();
    await pick(user, /Fallback role/, "Refuse sign-in");

    await user.click(screen.getByRole("button", { name: /^Add provider$/i }));
    // FR-015: a rule granting admin still needs an explicit confirmation.
    await screen.findByText(/Mapping users to the admin role grants full cluster control/i);
    await user.click(screen.getByRole("button", { name: /Map to admin role/i }));
    await screen.findByText(/oidc · https:\/\/idp\.corp\.example/i);
    await user.click(screen.getByRole("button", { name: /^Save changes$/i }));
    await waitFor(() => expect(api.put).toHaveBeenCalled());
    expect(api.saved()?.providers).toContainEqual({
      name: "corp",
      kind: "oidc",
      enabled: true,
      issuer: "https://idp.corp.example",
      clientID: "gameplane",
      configRef: "gameplane-auth-corp",
      roleRules: [
        { group: "sys-admin", role: "admin" },
        { group: "everyone", role: "viewer" },
      ],
      defaultRole: "deny",
    });
  }, 20000);

  it("warns when a later rule repeats a group", async () => {
    const user = userEvent.setup();
    serveAuth({ providers: [{ name: "local", kind: "local", enabled: true }] });
    await openProviderForm(user);
    await user.click(screen.getByRole("button", { name: "Add rule" }));
    await user.click(screen.getByRole("button", { name: "Add rule" }));
    await user.type(groupField(1), "sys-admin");
    await user.type(groupField(2), "sys-admin");
    expect(
      await screen.findByText("This group is already matched by rule 1, so this rule never applies."),
    ).toBeInTheDocument();
    await user.clear(groupField(2));
    await user.type(groupField(2), "other");
    expect(screen.queryByText(/already matched by rule/)).not.toBeInTheDocument();
  });

  it("lists custom roles in the rule and fallback dropdowns, plus Refuse sign-in", async () => {
    const user = userEvent.setup();
    server.use(rolesWithCustomRoleHandler);
    serveAuth({ providers: [{ name: "local", kind: "local", enabled: true }] });
    await openProviderForm(user);
    await user.click(screen.getByRole("button", { name: "Add rule" }));
    await user.click(screen.getByRole("button", { name: /Rule 1 role/ }));
    expect(await screen.findByRole("option", { name: "login-only" })).toBeInTheDocument();
    await user.click(screen.getByRole("option", { name: "login-only" }));
    await user.click(screen.getByRole("button", { name: /Fallback role/ }));
    expect(await screen.findByRole("option", { name: "login-only" })).toBeInTheDocument();
    expect(screen.getByRole("option", { name: "Refuse sign-in" })).toBeInTheDocument();
    for (const role of ["admin", "operator", "viewer"]) {
      expect(screen.getByRole("option", { name: role })).toBeInTheDocument();
    }
  });

  it("saves a stored old-form provider as roleRules in admin, operator, viewer order", async () => {
    const user = userEvent.setup();
    const corp: AuthProvider = {
      name: "corp-sso",
      kind: "oidc",
      enabled: true,
      issuer: "https://idp.corp.example",
      clientID: "gameplane",
      configRef: "gameplane-auth-corp-sso",
      groupsClaim: "memberOf",
      roleMappings: { viewer: ["gp-view"], operator: ["gp-ops"], admin: ["gp-admins", "gp-root"] },
      defaultRole: "deny",
    };
    const api = serveAuth({ providers: [{ name: "local", kind: "local", enabled: true }, corp] });
    await gotoAuth(user);
    const toggles = await screen.findAllByRole("button", { name: /^Enabled$/i });
    await user.click(toggles[1]);
    await user.click(screen.getByRole("button", { name: /^Save changes$/i }));
    await waitFor(() => expect(api.put).toHaveBeenCalled());
    expect(api.saved()?.providers).toContainEqual({
      name: "corp-sso",
      kind: "oidc",
      enabled: false,
      issuer: "https://idp.corp.example",
      clientID: "gameplane",
      configRef: "gameplane-auth-corp-sso",
      groupsClaim: "memberOf",
      defaultRole: "deny",
      roleRules: [
        { group: "gp-admins", role: "admin" },
        { group: "gp-root", role: "admin" },
        { group: "gp-ops", role: "operator" },
        { group: "gp-view", role: "viewer" },
      ],
    });
  });
});

describe("AdminSettings role rules: Helm provider", () => {
  it("lists the Helm rules in order with the fallback and the admin warning", async () => {
    const user = userEvent.setup();
    serveAuth({ providers: [{ name: "local", kind: "local", enabled: true }] }, helmInstall);
    await gotoAuth(user);
    await screen.findByText("Helm-seeded OIDC provider");
    const helmCard = card("Helm-seeded OIDC provider");
    expect(within(helmCard).getByText("teams")).toBeInTheDocument();
    expect(within(helmCard).getByText("Refuse sign-in")).toBeInTheDocument();
    const items = within(within(helmCard).getByRole("list", { name: "Helm rules" })).getAllByRole("listitem");
    expect(items.map((li) => li.textContent)).toEqual(["1gp-adminsadmin", "2gp-opsoperator"]);
    expect(
      within(helmCard).getByText(/^Rule 1 above grants admin through Helm values/),
    ).toBeInTheDocument();
  });

  it("starts an override from the Helm values and saves it as roleRules", async () => {
    const user = userEvent.setup();
    const api = serveAuth({ providers: [{ name: "local", kind: "local", enabled: true }] }, helmInstall);
    await gotoAuth(user);
    await screen.findByText("Role rule override");
    const override = card("Role rule override");
    expect(within(override).getByText("From Helm values")).toBeInTheDocument();
    expect(within(override).queryByRole("button", { name: "Save role rules" })).not.toBeInTheDocument();
    await user.click(within(override).getByRole("button", { name: "Override" }));
    expect(within(override).getByText("Overridden in dashboard")).toBeInTheDocument();
    expect(groupField(1, override)).toHaveValue("gp-admins");
    expect(groupField(2, override)).toHaveValue("gp-ops");
    await pick(user, /Rule 2 role/, "viewer", override);
    await user.click(within(override).getByRole("button", { name: "Save role rules" }));
    await waitFor(() => expect(api.put).toHaveBeenCalled());
    expect(api.saved()?.helmOverride).toEqual({
      roleRules: [
        { group: "gp-admins", role: "admin" },
        { group: "gp-ops", role: "viewer" },
      ],
      defaultRole: "deny",
    });
  });

  it("asks for confirmation before an override grants admin to a new group", async () => {
    const user = userEvent.setup();
    const api = serveAuth({ providers: [{ name: "local", kind: "local", enabled: true }] }, helmInstall);
    await gotoAuth(user);
    await screen.findByText("Role rule override");
    const override = card("Role rule override");
    await user.click(within(override).getByRole("button", { name: "Override" }));
    await user.click(within(override).getByRole("button", { name: "Add rule" }));
    await user.type(groupField(3, override), "new-admins");
    await pick(user, /Rule 3 role/, "admin", override);
    await user.click(within(override).getByRole("button", { name: "Save role rules" }));
    expect(await screen.findByText(/Mapping users to the admin role grants full cluster control/)).toBeInTheDocument();
    await user.keyboard("{Escape}");
    await waitFor(() =>
      expect(screen.queryByRole("button", { name: /Map to admin role/ })).not.toBeInTheDocument(),
    );
    expect(api.put).not.toHaveBeenCalled();
    await user.click(within(override).getByRole("button", { name: "Save role rules" }));
    await user.click(await screen.findByRole("button", { name: /Map to admin role/ }));
    await waitFor(() => expect(api.put).toHaveBeenCalled());
    expect(api.saved()?.helmOverride?.roleRules).toContainEqual({ group: "new-admins", role: "admin" });
  });

  it("Reset to Helm default drops the override from the next save", async () => {
    const user = userEvent.setup();
    const api = serveAuth(
      {
        providers: [{ name: "local", kind: "local", enabled: true }],
        helmOverride: { roleRules: [{ group: "dash", role: "viewer" }], defaultRole: "viewer" },
      },
      helmInstall,
    );
    await gotoAuth(user);
    await screen.findByText("Role rule override");
    const override = card("Role rule override");
    expect(within(override).getByText("Overridden in dashboard")).toBeInTheDocument();
    expect(groupField(1, override)).toHaveValue("dash");
    await user.click(within(override).getByRole("button", { name: "Reset to Helm default" }));
    expect(within(override).getByText("From Helm values")).toBeInTheDocument();
    expect(within(override).getByRole("button", { name: "Override" })).toBeInTheDocument();
    expect(within(override).getByText("gp-admins")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: /^Save changes$/i }));
    await waitFor(() => expect(api.put).toHaveBeenCalled());
    expect(api.saved()).toBeDefined();
    expect(api.saved()?.helmOverride).toBeUndefined();
  });

  it("flags a rule whose role no longer exists and blocks the save", async () => {
    const user = userEvent.setup();
    serveAuth(
      {
        providers: [{ name: "local", kind: "local", enabled: true }],
        helmOverride: { roleRules: [{ group: "contractors", role: "ops-team" }], defaultRole: "viewer" },
      },
      helmInstall,
    );
    await gotoAuth(user);
    expect(
      await screen.findByText("Role ops-team no longer exists. Pick another role or remove this rule."),
    ).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Save role rules" })).toBeDisabled();
    await pick(user, /Rule 1 role/, "operator");
    expect(screen.queryByText(/no longer exists/)).not.toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Save role rules" })).toBeEnabled();
  });

  it("saves an old per-role override as the effective roleRules", async () => {
    const user = userEvent.setup();
    const api = serveAuth(
      {
        providers: [{ name: "local", kind: "local", enabled: true }],
        helmOverride: { roleMappings: { admin: ["dash-admins"] } },
      },
      {
        gameDataStorageClass: "",
        oidcHelmProvider: {
          groupsClaim: "groups",
          defaultRole: "viewer",
          roleMappings: { admin: ["helm-admins"], operator: ["helm-ops"] },
        },
      },
    );
    await gotoAuth(user);
    await screen.findByText("Role rule override");
    const override = card("Role rule override");
    expect(groupField(1, override)).toHaveValue("dash-admins");
    expect(groupField(2, override)).toHaveValue("helm-ops");
    await user.click(screen.getByRole("button", { name: /^Save changes$/i }));
    await waitFor(() => expect(api.put).toHaveBeenCalled());
    expect(api.saved()?.helmOverride).toEqual({
      roleRules: [
        { group: "dash-admins", role: "admin" },
        { group: "helm-ops", role: "operator" },
      ],
    });
  });
});
