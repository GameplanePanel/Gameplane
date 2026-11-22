import { describe, it, expect, vi } from "vitest";
import type { ReactNode } from "react";
import { http, HttpResponse } from "msw";
import { screen, waitFor } from "@testing-library/react";
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

async function gotoModRegistries() {
  await screen.findByRole("heading", { name: /Admin settings/i });
  await userEvent.click(await screen.findByRole("button", { name: /Mod registries/i }));
}

function seed(initial: { share: boolean; managedKey: boolean }, putStatus = 200) {
  const puts: unknown[] = [];
  server.use(
    http.get("/admin/config", () =>
      HttpResponse.json(makeConfig({ modRegistries: { registries: [{ provider: "curseforge", configRef: "gameplane-modreg-curseforge" }] } })),
    ),
    http.get("/admin/registries/curseforge/share", () => HttpResponse.json(initial)),
    http.put("/admin/registries/curseforge/share", async ({ request }) => {
      puts.push(await request.json());
      if (putStatus === 409) {
        return HttpResponse.json(
          { type: "about:blank", status: 409, title: "Conflict", detail: "share needs a managed key", messageId: "server.registry.share_needs_managed_key" },
          { status: 409, headers: { "Content-Type": "application/problem+json" } },
        );
      }
      return HttpResponse.json({ share: true, managedKey: true });
    }),
  );
  return puts;
}

describe("AdminSettings CurseForge key share switch", () => {
  it("turns sharing on and shows the 0 players note", async () => {
    const puts = seed({ share: false, managedKey: true });
    renderWithQuery(<AdminSettingsPage />);
    await gotoModRegistries();
    const sw = await screen.findByRole("switch", { name: /Share the CurseForge key with game servers/i });
    expect(sw).not.toBeDisabled();
    expect(sw).not.toBeChecked();
    expect(screen.queryByText(/0 players/i)).not.toBeInTheDocument();
    await userEvent.click(sw);
    await waitFor(() => expect(puts).toEqual([{ share: true }]));
    await waitFor(() => expect(screen.getByRole("switch", { name: /Share the CurseForge key/i })).toBeChecked());
    expect(screen.getByText(/0 players/i)).toBeInTheDocument();
  });

  it("disables the switch with a reason when the key is not managed", async () => {
    const puts = seed({ share: false, managedKey: false });
    renderWithQuery(<AdminSettingsPage />);
    await gotoModRegistries();
    const sw = await screen.findByRole("switch", { name: /Share the CurseForge key with game servers/i });
    expect(sw).toBeDisabled();
    expect(screen.getByText(/A key from the startup flag stays inside the API/i)).toBeInTheDocument();
    await userEvent.click(sw);
    expect(puts).toEqual([]);
  });

  it("shows the error and keeps the switch off when the PUT is rejected", async () => {
    seed({ share: false, managedKey: true }, 409);
    renderWithQuery(<AdminSettingsPage />);
    await gotoModRegistries();
    const sw = await screen.findByRole("switch", { name: /Share the CurseForge key with game servers/i });
    await userEvent.click(sw);
    await waitFor(() => expect(document.body.textContent).toMatch(/share needs a managed key|managed/i));
    expect(screen.getByRole("switch", { name: /Share the CurseForge key/i })).not.toBeChecked();
  });

  it("refreshes the switch when the key is removed", async () => {
    let managedKey = true;
    server.use(
      http.get("/admin/config", () =>
        HttpResponse.json(makeConfig({ modRegistries: { registries: managedKey ? [{ provider: "curseforge", configRef: "gameplane-modreg-curseforge" }] : [] } })),
      ),
      http.put("/admin/config/modRegistries", () => new HttpResponse(null, { status: 204 })),
      http.delete("/admin/registries/:provider/secret", () => {
        managedKey = false;
        return new HttpResponse(null, { status: 204 });
      }),
      http.get("/admin/registries/curseforge/share", () => HttpResponse.json({ share: false, managedKey })),
    );
    renderWithQuery(<AdminSettingsPage />);
    await gotoModRegistries();
    const sw = await screen.findByRole("switch", { name: /Share the CurseForge key with game servers/i });
    expect(sw).not.toBeDisabled();
    await userEvent.click(await screen.findByRole("button", { name: /Remove/i }));
    await waitFor(() => expect(screen.getByRole("switch", { name: /Share the CurseForge key/i })).toBeDisabled());
  });
});
