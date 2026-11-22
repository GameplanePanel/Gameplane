import { describe, it, expect, vi } from "vitest";
import type { ReactNode } from "react";
import { http, HttpResponse } from "msw";
import { screen } from "@testing-library/react";
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

describe("AdminSettings CurseForge share key source", () => {
  it("names the startup flag as the key source and keeps the switch disabled", async () => {
    server.use(
      http.get("/admin/config", () =>
        HttpResponse.json(makeConfig({ modRegistries: { registries: [{ provider: "curseforge", configRef: "gameplane-modreg-curseforge" }] } })),
      ),
      http.get("/admin/registries/curseforge/share", () =>
        HttpResponse.json({ share: false, managedKey: false, keySource: "startup-flag" }),
      ),
    );
    renderWithQuery(<AdminSettingsPage />);
    await screen.findByRole("heading", { name: /Admin settings/i });
    await userEvent.click(await screen.findByRole("button", { name: /Mod registries/i }));

    expect(await screen.findByText("From startup flag")).toBeInTheDocument();
    expect(screen.getByText("Set by the --curseforge-api-key startup flag")).toBeInTheDocument();
    expect(screen.getByRole("switch", { name: /Share the CurseForge key with game servers/i })).toBeDisabled();
  });
});
