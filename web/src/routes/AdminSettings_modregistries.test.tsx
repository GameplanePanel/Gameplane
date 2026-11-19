import { describe, it, expect, vi } from "vitest";
import type { ReactNode } from "react";
import { http, HttpResponse } from "msw";
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { server } from "@/test/server";
import { renderWithQuery } from "@/test/render";
import { makeConfig } from "@/test/factories";
import type { ModRegistriesCfg } from "@/lib/config";

vi.mock("@tanstack/react-router", () => ({
  Link: ({ children, to, ...rest }: { children: ReactNode; to: string } & Record<string, unknown>) => (
    <a href={to} {...rest}>{children}</a>
  ),
}));

import { AdminSettingsPage } from "./AdminSettings";

function seedRegistries(modRegistries: ModRegistriesCfg) {
  const state = { modRegistries };
  server.use(
    http.get("/admin/config", () => HttpResponse.json(makeConfig({ modRegistries: state.modRegistries }))),
    http.put("/admin/config/modRegistries", async ({ request }) => {
      state.modRegistries = await request.json() as ModRegistriesCfg;
      return new HttpResponse(null, { status: 204 });
    }),
  );
  return state;
}

async function gotoModRegistries() {
  await screen.findByRole("heading", { name: /Admin settings/i });
  await userEvent.click(await screen.findByRole("button", { name: /Mod registries/i }));
}

describe("AdminSettings mod registries", () => {
  it("shows a configured provider as Configured with Replace/Remove actions", async () => {
    seedRegistries({ registries: [{ provider: "curseforge", configRef: "gameplane-modreg-curseforge" }] });
    renderWithQuery(<AdminSettingsPage />);
    await gotoModRegistries();
    // Scope to the CurseForge row: Steam and Nexus are legitimately
    // unconfigured in this fixture and render their own "Set API key"
    // buttons, so an unscoped query would see more than one.
    const row = (await screen.findByText("CurseForge")).closest("li")!;
    expect(within(row).getByText("Configured")).toBeInTheDocument();
    expect(within(row).getByText(/Active in the Mods browser/i)).toBeInTheDocument();
    expect(within(row).getByRole("button", { name: /Replace/i })).toBeInTheDocument();
    expect(within(row).getByRole("button", { name: /Remove/i })).toBeInTheDocument();
    expect(within(row).queryByRole("button", { name: /Set API key/i })).not.toBeInTheDocument();
  });

  it("shows unconfigured providers as Not configured, hidden from the Mods browser", async () => {
    seedRegistries({ registries: [] });
    renderWithQuery(<AdminSettingsPage />);
    await gotoModRegistries();
    expect(await screen.findByText("Steam Workshop")).toBeInTheDocument();
    expect(screen.getByText("Nexus Mods")).toBeInTheDocument();
    // Scope to the registries list: the section's own subtitle copy
    // ("...stay hidden from the Mods browser until a key is configured")
    // also satisfies the /Hidden from the Mods browser/i regex, so an
    // unscoped query over-counts by one.
    const list = screen.getByRole("list");
    const notConfigured = within(list).getAllByText("Not configured");
    expect(notConfigured).toHaveLength(3); // curseforge, steam, nexus
    expect(within(list).getAllByText(/Hidden from the Mods browser/i)).toHaveLength(3);
    expect(within(list).getAllByRole("button", { name: /Set API key/i })).toHaveLength(3);
    expect(screen.getByText(
      /Always available: Modrinth, Thunderstore, Hangar, Factorio, Spigot, GitHub, uMod/i,
    )).toBeInTheDocument();
  });

  it("saves an API key with Enter and retains it after reopening settings", async () => {
    const calls: string[] = [];
    let secretBody: Record<string, string> | undefined;
    let saved: ModRegistriesCfg | undefined;
    const state = seedRegistries({ registries: [] });
    server.use(
      http.put("/admin/registries/:provider/secret", async ({ params, request }) => {
        calls.push("secret");
        secretBody = (await request.json()) as Record<string, string>;
        return HttpResponse.json({ name: `gameplane-modreg-${String(params.provider)}`, keys: ["apiKey"] });
      }),
      http.put("/admin/config/modRegistries", async ({ request }) => {
        calls.push("config");
        saved = (await request.json()) as ModRegistriesCfg;
        state.modRegistries = saved;
        return new HttpResponse(null, { status: 204 });
      }),
    );
    const rendered = renderWithQuery(<AdminSettingsPage />);
    await gotoModRegistries();
    expect(screen.queryByRole("button", { name: /^Save changes$/i })).not.toBeInTheDocument();
    const steamRow = (await screen.findByText("Steam Workshop")).closest("li")!;
    await userEvent.click(within(steamRow).getByRole("button", { name: /Set API key/i }));
    await userEvent.type(screen.getByLabelText(/API key/i), "steam-secret-key");
    await userEvent.keyboard("{Enter}");
    expect(await screen.findByText("Saved")).toBeInTheDocument();
    expect(screen.queryByLabelText(/API key/i)).not.toBeInTheDocument();
    expect(within(steamRow).getByText("Configured")).toBeInTheDocument();
    expect(secretBody).toEqual({ apiKey: "steam-secret-key" });
    expect(calls).toEqual(["secret", "config"]);
    expect(saved?.registries).toEqual([
      { provider: "steam", configRef: "gameplane-modreg-steam" },
    ]);
    rendered.unmount();
    renderWithQuery(<AdminSettingsPage />);
    await gotoModRegistries();
    const reopened = (await screen.findByText("Steam Workshop")).closest("li")!;
    expect(within(reopened).getByText("Configured")).toBeInTheDocument();
    await userEvent.click(within(reopened).getByRole("button", { name: /Replace/i }));
    expect(screen.getByLabelText(/API key/i)).toHaveValue("");
  });

  it("removes a configured key immediately and deletes its managed Secret", async () => {
    let deletedProvider: string | null = null;
    seedRegistries({ registries: [{ provider: "curseforge", configRef: "gameplane-modreg-curseforge" }] });
    server.use(
      http.delete("/admin/registries/:provider/secret", ({ params }) => {
        deletedProvider = String(params.provider);
        return new HttpResponse(null, { status: 204 });
      }),
    );
    renderWithQuery(<AdminSettingsPage />);
    await gotoModRegistries();
    await screen.findByText("Configured");
    await userEvent.click(screen.getByRole("button", { name: /Remove/i }));
    // Scope to the registries list — see the note in the "unconfigured
    // providers" test above about the section subtitle colliding with the
    // /Hidden from the Mods browser/i regex.
    const list = screen.getByRole("list");
    await waitFor(() => expect(within(list).getAllByText("Not configured")).toHaveLength(3));
    expect(within(list).getAllByText(/Hidden from the Mods browser/i)).toHaveLength(3);
    await waitFor(() => expect(deletedProvider).toBe("curseforge"));
  });

  it("does not delete a Secret the section didn't create when removing a provider", async () => {
    let deleteCalled = false;
    seedRegistries({ registries: [{ provider: "nexus", configRef: "my-own-secret" }] });
    server.use(
      http.delete("/admin/registries/:provider/secret", () => {
        deleteCalled = true;
        return new HttpResponse(null, { status: 204 });
      }),
    );
    renderWithQuery(<AdminSettingsPage />);
    await gotoModRegistries();
    await screen.findByText("Configured");
    await userEvent.click(screen.getByRole("button", { name: /Remove/i }));
    expect(await screen.findByText("Saved")).toBeInTheDocument();
    expect(screen.getAllByText("Not configured")).toHaveLength(3);
    expect(deleteCalled).toBe(false);
  });

  it("keeps the form and key through a pending save and prevents duplicate actions", async () => {
    seedRegistries({ registries: [] });
    let release: (() => void) | undefined;
    const pending = new Promise<void>((resolve) => { release = resolve; });
    const secret = vi.fn(async () => {
      await pending;
      return HttpResponse.json({ name: "gameplane-modreg-steam", keys: ["apiKey"] });
    });
    server.use(http.put("/admin/registries/steam/secret", secret));
    renderWithQuery(<AdminSettingsPage />);
    await gotoModRegistries();
    const row = (await screen.findByText("Steam Workshop")).closest("li")!;
    await userEvent.click(within(row).getByRole("button", { name: /Set API key/i }));
    const input = screen.getByLabelText(/API key/i);
    await userEvent.type(input, "pending-secret");
    const save = screen.getByRole("button", { name: /^Save$/i });
    await userEvent.dblClick(save);
    await waitFor(() => expect(secret).toHaveBeenCalledTimes(1));
    expect(input).toHaveValue("pending-secret");
    expect(input).toBeDisabled();
    expect(save).toBeDisabled();
    expect(screen.getByRole("button", { name: /Cancel/i })).toBeDisabled();
    for (const button of screen.getAllByRole("button", { name: /Set API key/i })) expect(button).toBeDisabled();
    expect(within(row).getByText("Not configured")).toBeInTheDocument();
    release?.();
    expect(await screen.findByText("Saved")).toBeInTheDocument();
    expect(screen.queryByLabelText(/API key/i)).not.toBeInTheDocument();
    expect(secret).toHaveBeenCalledTimes(1);
  });

  it.each(["secret", "config"])("keeps an unsuccessful %s save open for retry without showing a configured row", async (failure) => {
    seedRegistries({ registries: [] });
    let failed = true;
    server.use(
      http.put("/admin/registries/steam/secret", () => failed && failure === "secret"
        ? HttpResponse.text("Key storage unavailable", { status: 503 })
        : HttpResponse.json({ name: "gameplane-modreg-steam", keys: ["apiKey"] })),
      http.put("/admin/config/modRegistries", async ({ request }) => {
        if (failed && failure === "config") return HttpResponse.text("Configuration unavailable", { status: 503 });
        const modRegistries = await request.json() as ModRegistriesCfg;
        seedRegistries(modRegistries);
        return new HttpResponse(null, { status: 204 });
      }),
    );
    renderWithQuery(<AdminSettingsPage />);
    await gotoModRegistries();
    const row = (await screen.findByText("Steam Workshop")).closest("li")!;
    await userEvent.click(within(row).getByRole("button", { name: /Set API key/i }));
    const input = screen.getByLabelText(/API key/i);
    await userEvent.type(input, "retry-secret");
    await userEvent.click(screen.getByRole("button", { name: /^Save$/i }));
    expect(await screen.findByText(failure === "secret" ? "Key storage unavailable" : "Configuration unavailable")).toBeInTheDocument();
    expect(input).toHaveValue("retry-secret");
    expect(input).toBeEnabled();
    expect(within(row).getByText("Not configured")).toBeInTheDocument();
    failed = false;
    await userEvent.click(screen.getByRole("button", { name: /^Save$/i }));
    expect(await screen.findByText("Saved")).toBeInTheDocument();
    expect(screen.queryByLabelText(/API key/i)).not.toBeInTheDocument();
    expect(within(row).getByText("Configured")).toBeInTheDocument();
  });

  it("shows a cleanup failure without restoring the removed provider", async () => {
    seedRegistries({ registries: [{ provider: "curseforge", configRef: "gameplane-modreg-curseforge" }] });
    server.use(http.delete("/admin/registries/curseforge/secret", () => HttpResponse.text("Cleanup unavailable", { status: 503 })));
    const rendered = renderWithQuery(<AdminSettingsPage />);
    await gotoModRegistries();
    await userEvent.click(await screen.findByRole("button", { name: /Remove/i }));
    expect(await screen.findByText("Cleanup unavailable")).toBeInTheDocument();
    expect(screen.getAllByText("Not configured")).toHaveLength(3);
    rendered.unmount();
    renderWithQuery(<AdminSettingsPage />);
    await gotoModRegistries();
    await waitFor(() => expect(screen.getAllByText("Not configured")).toHaveLength(3));
  });

  it("never renders the stored API key anywhere, including the Replace form", async () => {
    seedRegistries({ registries: [{ provider: "curseforge", configRef: "gameplane-modreg-curseforge" }] });
    renderWithQuery(<AdminSettingsPage />);
    await gotoModRegistries();
    // GET /admin/config never carries key material — only the configRef.
    expect(screen.queryByText(/apiKey/i)).not.toBeInTheDocument();
    await userEvent.click(await screen.findByRole("button", { name: /Replace/i }));
    const input = screen.getByLabelText(/API key/i) as HTMLInputElement;
    // Write-only: the Replace form never prefills a value to reveal.
    expect(input).toHaveValue("");
    expect(input).toHaveAttribute("type", "password");
  });
});
