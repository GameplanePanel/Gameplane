import { describe, expect, it, vi } from "vitest";
import { http, HttpResponse } from "msw";
import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { server } from "@/test/server";
import { renderWithQuery } from "@/test/render";
import { makeUser } from "@/test/factories";
import { SecuritySettingsPanel } from "./SecuritySettingsPanel";

const SWITCH_NAME = "Allow root overrides";
const MISSING_TITLE = "Security settings missing";

describe("SecuritySettingsPanel", () => {
  it("shows a loading state while the current user is pending", async () => {
    let release!: () => void;
    const pending = new Promise<void>((resolve) => { release = resolve; });
    server.use(http.get("/users/me", async () => {
      await pending;
      return HttpResponse.json(makeUser());
    }));
    renderWithQuery(<SecuritySettingsPanel clusterId="east" />);
    expect(await screen.findByText("Loading permissions…")).toBeInTheDocument();
    release();
    expect(await screen.findByRole("switch", { name: SWITCH_NAME })).toBeInTheDocument();
  });

  it("shows a retryable error when the identity cannot be loaded", async () => {
    server.use(http.get("/users/me", () => HttpResponse.text("unauthorized", { status: 401 })));
    renderWithQuery(<SecuritySettingsPanel clusterId="east" />);
    expect(await screen.findByText("Couldn't load your permissions.")).toBeInTheDocument();
    expect(screen.queryByRole("switch", { name: SWITCH_NAME })).not.toBeInTheDocument();
  });

  it("shows a loading state while the security settings are fetched", async () => {
    let release!: () => void;
    const pending = new Promise<void>((resolve) => { release = resolve; });
    server.use(http.get("/security-settings", async () => {
      await pending;
      return HttpResponse.json({ cluster: "east", allowRootOverrides: false, present: true });
    }));
    renderWithQuery(<SecuritySettingsPanel clusterId="east" />);
    expect(await screen.findByText("Loading security settings…")).toBeInTheDocument();
    release();
    expect(await screen.findByRole("switch", { name: SWITCH_NAME })).toBeInTheDocument();
  });

  it("shows a retryable error when the settings cannot be fetched", async () => {
    server.use(http.get("/security-settings", () => HttpResponse.text("Server error", { status: 500 })));
    renderWithQuery(<SecuritySettingsPanel clusterId="east" />);
    expect(await screen.findByText("Couldn't load this cluster's security settings.")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Retry" })).toBeInTheDocument();
    expect(screen.queryByRole("switch", { name: SWITCH_NAME })).not.toBeInTheDocument();
  });

  it("does not request settings without read access to this cluster", async () => {
    const reads = vi.fn(() => HttpResponse.json({ cluster: "east", allowRootOverrides: false, present: true }));
    server.use(
      http.get("/users/me", () => HttpResponse.json(makeUser({ permissions: {} }))),
      http.get("/security-settings", reads),
    );
    renderWithQuery(<SecuritySettingsPanel clusterId="east" />);
    expect(await screen.findByText("You don't have permission to view this cluster's security settings.")).toBeInTheDocument();
    expect(reads).not.toHaveBeenCalled();
    expect(screen.queryByRole("switch", { name: SWITCH_NAME })).not.toBeInTheDocument();
  });

  it("shows the missing-settings alert and disables the switch when the ConfigMap is absent", async () => {
    server.use(http.get("/security-settings", () => HttpResponse.json({ cluster: "east", allowRootOverrides: false, present: false })));
    renderWithQuery(<SecuritySettingsPanel clusterId="east" />);
    expect(await screen.findByText(MISSING_TITLE)).toBeInTheDocument();
    expect(screen.getByText(/Run helm upgrade for Gameplane on this cluster/)).toBeInTheDocument();
    expect(screen.getByRole("switch", { name: SWITCH_NAME })).toBeDisabled();
  });

  it("reads the stored root-override state for the cluster", async () => {
    const reads: string[] = [];
    server.use(http.get("/security-settings", ({ request }) => {
      reads.push(request.url);
      return HttpResponse.json({ cluster: "east", allowRootOverrides: true, present: true });
    }));
    renderWithQuery(<SecuritySettingsPanel clusterId="east" />);
    const toggle = await screen.findByRole("switch", { name: SWITCH_NAME });
    expect(toggle).toBeChecked();
    expect(toggle).toBeEnabled();
    expect(reads.length).toBeGreaterThan(0);
    expect(new URL(reads[0]).searchParams.get("cluster")).toBe("east");
    expect(screen.queryByText(MISSING_TITLE)).not.toBeInTheDocument();
  });

  it("PUTs the toggled value for the cluster and shows the saved state", async () => {
    const puts: { url: string; body: unknown }[] = [];
    server.use(http.put("/security-settings", async ({ request }) => {
      puts.push({ url: request.url, body: await request.json() });
      return HttpResponse.json({ cluster: "east", allowRootOverrides: true, present: true });
    }));
    renderWithQuery(<SecuritySettingsPanel clusterId="east" />);
    const toggle = await screen.findByRole("switch", { name: SWITCH_NAME });
    expect(toggle).not.toBeChecked();
    await userEvent.click(toggle);
    await waitFor(() => expect(screen.getByRole("switch", { name: SWITCH_NAME })).toBeChecked());
    expect(puts).toHaveLength(1);
    expect(puts[0].body).toEqual({ allowRootOverrides: true });
    expect(new URL(puts[0].url).searchParams.get("cluster")).toBe("east");
  });

  it("shows the missing-settings alert when the save returns 409 security_settings_missing", async () => {
    server.use(
      http.get("/security-settings", () => HttpResponse.json({ cluster: "east", allowRootOverrides: false, present: true })),
      http.put("/security-settings", () => HttpResponse.text("security settings missing", { status: 409 })),
    );
    renderWithQuery(<SecuritySettingsPanel clusterId="east" />);
    await userEvent.click(await screen.findByRole("switch", { name: SWITCH_NAME }));
    expect(await screen.findByText(MISSING_TITLE)).toBeInTheDocument();
    expect(screen.getByRole("switch", { name: SWITCH_NAME })).toBeDisabled();
    expect(screen.queryByText("security settings missing")).not.toBeInTheDocument();
  });

  it("shows other save failures in an error banner and keeps the switch usable", async () => {
    server.use(
      http.get("/security-settings", () => HttpResponse.json({ cluster: "east", allowRootOverrides: false, present: true })),
      http.put("/security-settings", () => HttpResponse.text("Cannot save root setting", { status: 500 })),
    );
    renderWithQuery(<SecuritySettingsPanel clusterId="east" />);
    await userEvent.click(await screen.findByRole("switch", { name: SWITCH_NAME }));
    expect(await screen.findByText(/Cannot save root setting/)).toBeInTheDocument();
    expect(screen.queryByText(MISSING_TITLE)).not.toBeInTheDocument();
    expect(screen.getByRole("switch", { name: SWITCH_NAME })).toBeEnabled();
  });

  it("disables the switch without config:manage", async () => {
    server.use(
      http.get("/users/me", () => HttpResponse.json(makeUser({ permissions: { "*": ["config:read"] } }))),
      http.get("/security-settings", () => HttpResponse.json({ cluster: "east", allowRootOverrides: true, present: true })),
    );
    renderWithQuery(<SecuritySettingsPanel clusterId="east" />);
    const toggle = await screen.findByRole("switch", { name: SWITCH_NAME });
    expect(toggle).toBeChecked();
    expect(toggle).toBeDisabled();
  });
});
