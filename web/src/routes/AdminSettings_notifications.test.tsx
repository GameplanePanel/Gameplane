import { describe, it, expect, vi } from "vitest";
import type { ReactNode } from "react";
import { http, HttpResponse } from "msw";
import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { server } from "@/test/server";
import { renderWithQuery } from "@/test/render";
import { makeConfig } from "@/test/factories";
import type { NotificationsCfg } from "@/lib/config";

vi.mock("@tanstack/react-router", () => ({
  Link: ({ children, to, ...rest }: { children: ReactNode; to: string } & Record<string, unknown>) => (
    <a href={to} {...rest}>{children}</a>
  ),
}));

import { AdminSettingsPage } from "./AdminSettings";

function seedSinks(notifications: NotificationsCfg) {
  server.use(
    http.get("/admin/config", () => HttpResponse.json(makeConfig({ notifications }))),
  );
}

async function gotoNotifications() {
  await screen.findByRole("heading", { name: /Admin settings/i });
  await userEvent.click(await screen.findByRole("button", { name: /Notifications/i }));
}

describe("AdminSettings notifications", () => {
  it("flags a legacy sink without a Secret and disables its test button", async () => {
    seedSinks({ sinks: [{ name: "old-hook", kind: "discord", enabled: true }] });
    renderWithQuery(<AdminSettingsPage />);
    await gotoNotifications();
    expect(await screen.findByText("Needs secret")).toBeInTheDocument();
    expect(screen.getByText(/no Secret configured/i)).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /Send test/i })).toBeDisabled();
  });

  it("test-fires a configured sink and shows the outcome", async () => {
    seedSinks({
      sinks: [{ name: "team-alerts", kind: "webhook", enabled: true, configRef: "team-hook" }],
    });
    renderWithQuery(<AdminSettingsPage />);
    await gotoNotifications();
    await userEvent.click(await screen.findByRole("button", { name: /Send test/i }));
    expect(await screen.findByText(/delivered/i)).toBeInTheDocument();
  });

  it("surfaces a failed test delivery", async () => {
    seedSinks({
      sinks: [{ name: "team-alerts", kind: "slack", enabled: true, configRef: "team-hook" }],
    });
    server.use(
      http.post("/admin/notifications/sinks/:name/test", () =>
        HttpResponse.text("deliver test to \"team-alerts\": endpoint returned 404", {
          status: 502,
        }),
      ),
    );
    renderWithQuery(<AdminSettingsPage />);
    await gotoNotifications();
    await userEvent.click(await screen.findByRole("button", { name: /Send test/i }));
    expect(await screen.findByText(/endpoint returned 404/i)).toBeInTheDocument();
  });

  it("disables test-send while the draft has unsaved changes", async () => {
    seedSinks({
      sinks: [{ name: "team-alerts", kind: "discord", enabled: true, configRef: "team-hook" }],
    });
    renderWithQuery(<AdminSettingsPage />);
    await gotoNotifications();
    const testBtn = await screen.findByRole("button", { name: /Send test/i });
    expect(testBtn).toBeEnabled();
    await userEvent.click(screen.getByRole("switch", { name: /Disable sink team-alerts/i }));
    expect(testBtn).toBeDisabled();
  });

  it("adds a sink by entering the webhook URL, storing the Secret before the config save", async () => {
    const user = userEvent.setup();
    const calls: string[] = [];
    let secretBody: Record<string, string> | undefined;
    let saved: NotificationsCfg | undefined;
    server.use(
      http.put("/admin/notifications/sinks/:name/secret", async ({ params, request }) => {
        calls.push("secret");
        secretBody = (await request.json()) as Record<string, string>;
        return HttpResponse.json({ name: `gameplane-notify-${String(params.name)}`, keys: ["url", "authorization"] });
      }),
      http.put("/admin/config/notifications", async ({ request }) => {
        calls.push("config");
        saved = (await request.json()) as NotificationsCfg;
        return new HttpResponse(null, { status: 204 });
      }),
    );
    renderWithQuery(<AdminSettingsPage />);
    await gotoNotifications();
    await user.click(await screen.findByRole("button", { name: /Add sink/i }));
    // There is no configRef input anymore — the value is entered directly.
    expect(screen.queryByText(/Secret name/i)).not.toBeInTheDocument();
    await user.type(screen.getByPlaceholderText("team-alerts"), "ops-alerts");
    await user.click(screen.getByRole("button", { name: /Sink kind/i }));
    await user.click(await screen.findByRole("option", { name: /slack/i }));
    await user.type(
      screen.getByPlaceholderText(/hooks\.slack\.com/i),
      "https://hooks.slack.com/services/T00/B00/xyz",
    );
    // Defaults have server.recovered checked; narrow the filter.
    await user.click(screen.getByRole("checkbox", { name: /server\.recovered/i }));
    await user.click(screen.getByRole("button", { name: /^Add sink$/i }));
    expect(await screen.findByText(/slack · Secret: gameplane-notify-ops-alerts/i)).toBeInTheDocument();
    // Nothing is stored until the section's Save changes.
    expect(calls).toEqual([]);
    await userEvent.click(screen.getByRole("button", { name: /Save changes/i }));
    await waitFor(() => expect(saved).toBeDefined());
    expect(secretBody).toEqual({
      kind: "slack",
      url: "https://hooks.slack.com/services/T00/B00/xyz",
      authorization: "",
    });
    // The Secret must exist before the config row referencing it.
    expect(calls).toEqual(["secret", "config"]);
    expect(saved?.sinks).toEqual([
      {
        name: "ops-alerts",
        kind: "slack",
        enabled: true,
        configRef: "gameplane-notify-ops-alerts",
        events: ["server.unhealthy", "backup.failed", "restore.failed"],
      },
    ]);
  });

  it("adds an ntfy sink with a topic URL and token", async () => {
    const user = userEvent.setup();
    let secretBody: Record<string, string> | undefined;
    server.use(
      http.put("/admin/notifications/sinks/:name/secret", async ({ params, request }) => {
        secretBody = (await request.json()) as Record<string, string>;
        return HttpResponse.json({ name: `gameplane-notify-${String(params.name)}`, keys: ["url", "authorization"] });
      }),
    );
    renderWithQuery(<AdminSettingsPage />);
    await gotoNotifications();
    await user.click(await screen.findByRole("button", { name: /Add sink/i }));
    await user.type(screen.getByPlaceholderText("team-alerts"), "phone");
    await user.click(screen.getByRole("button", { name: /Sink kind/i }));
    await user.click(await screen.findByRole("option", { name: /ntfy/i }));
    await user.type(screen.getByPlaceholderText(/ntfy\.sh/i), "https://ntfy.sh/gameplane-oncall");
    await user.type(screen.getByPlaceholderText("tk_…"), "tk_secret");
    await user.click(screen.getByRole("button", { name: /^Add sink$/i }));
    expect(await screen.findByText(/ntfy · Secret: gameplane-notify-phone/i)).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: /Save changes/i }));
    await waitFor(() =>
      expect(secretBody).toEqual({
        kind: "ntfy",
        url: "https://ntfy.sh/gameplane-oncall",
        token: "tk_secret",
      }),
    );
  });

  it("surfaces a secret-store failure on Save changes and leaves the config unsaved", async () => {
    let configSaved = false;
    server.use(
      http.put("/admin/notifications/sinks/:name/secret", () =>
        HttpResponse.text("url must be an http(s) URL", { status: 422 }),
      ),
      http.put("/admin/config/notifications", () => {
        configSaved = true;
        return new HttpResponse(null, { status: 204 });
      }),
    );
    renderWithQuery(<AdminSettingsPage />);
    await gotoNotifications();
    await userEvent.click(await screen.findByRole("button", { name: /Add sink/i }));
    await userEvent.type(screen.getByPlaceholderText("team-alerts"), "bad");
    await userEvent.type(
      screen.getByPlaceholderText(/discord\.com/i),
      "https://discord.com/api/webhooks/1/x",
    );
    await userEvent.click(screen.getByRole("button", { name: /^Add sink$/i }));
    await userEvent.click(await screen.findByRole("button", { name: /Save changes/i }));
    expect(await screen.findByText(/url must be an http\(s\) URL/i)).toBeInTheDocument();
    // The config row that would reference the Secret is never written.
    expect(configSaved).toBe(false);
  });

  it("deletes a sink from the draft and removes its managed Secret on Save changes", async () => {
    let deleted: string | null = null;
    seedSinks({
      sinks: [
        {
          name: "team-alerts",
          kind: "discord",
          enabled: true,
          configRef: "gameplane-notify-team-alerts",
        },
      ],
    });
    server.use(
      http.delete("/admin/notifications/sinks/:name/secret", ({ params }) => {
        deleted = String(params.name);
        return new HttpResponse(null, { status: 204 });
      }),
    );
    renderWithQuery(<AdminSettingsPage />);
    await gotoNotifications();
    await screen.findByText("team-alerts");
    await userEvent.click(screen.getByRole("button", { name: /Delete sink team-alerts/i }));
    expect(await screen.findByText(/No notification sinks configured/i)).toBeInTheDocument();
    expect(screen.queryByText("team-alerts")).not.toBeInTheDocument();
    expect(deleted).toBeNull();
    await userEvent.click(screen.getByRole("button", { name: /Save changes/i }));
    await waitFor(() => expect(deleted).toBe("team-alerts"));
  });

  it("does not delete a user-referenced Secret when removing the sink", async () => {
    let deleteCalled = false;
    seedSinks({
      sinks: [{ name: "team-alerts", kind: "discord", enabled: true, configRef: "my-own-secret" }],
    });
    server.use(
      http.delete("/admin/notifications/sinks/:name/secret", () => {
        deleteCalled = true;
        return new HttpResponse(null, { status: 204 });
      }),
    );
    renderWithQuery(<AdminSettingsPage />);
    await gotoNotifications();
    await screen.findByText("team-alerts");
    await userEvent.click(screen.getByRole("button", { name: /Delete sink team-alerts/i }));
    expect(await screen.findByText(/No notification sinks configured/i)).toBeInTheDocument();
    expect(deleteCalled).toBe(false);
  });

  it("sends no language key for a new sink left on Instance default", async () => {
    const user = userEvent.setup();
    let saved: NotificationsCfg | undefined;
    server.use(
      http.put("/admin/notifications/sinks/:name/secret", ({ params }) =>
        HttpResponse.json({ name: `gameplane-notify-${String(params.name)}`, keys: ["url", "authorization"] }),
      ),
      http.put("/admin/config/notifications", async ({ request }) => {
        saved = (await request.json()) as NotificationsCfg;
        return new HttpResponse(null, { status: 204 });
      }),
    );
    renderWithQuery(<AdminSettingsPage />);
    await gotoNotifications();
    await user.click(await screen.findByRole("button", { name: /Add sink/i }));
    await user.type(screen.getByPlaceholderText("team-alerts"), "ops-hook");
    await user.click(screen.getByRole("button", { name: /Sink kind/i }));
    await user.click(await screen.findByRole("option", { name: /slack/i }));
    await user.type(
      screen.getByPlaceholderText(/hooks\.slack\.com/i),
      "https://hooks.slack.com/services/T00/B00/xyz",
    );
    await user.click(screen.getByRole("button", { name: /^Add sink$/i }));
    expect(await screen.findByText(/slack · Secret: gameplane-notify-ops-hook/i)).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: /Save changes/i }));
    await waitFor(() => expect(saved).toBeDefined());
    expect(saved?.sinks).toHaveLength(1);
    expect(saved?.sinks[0]).not.toHaveProperty("language");
  });

  it("sets a sink's language from its row and saves it with the sink", async () => {
    const user = userEvent.setup();
    let saved: NotificationsCfg | undefined;
    seedSinks({
      sinks: [
        {
          name: "team-alerts",
          kind: "discord",
          enabled: true,
          configRef: "gameplane-notify-team-alerts",
          events: ["server.unhealthy"],
        },
      ],
    });
    server.use(
      http.put("/admin/config/notifications", async ({ request }) => {
        saved = (await request.json()) as NotificationsCfg;
        return new HttpResponse(null, { status: 204 });
      }),
    );
    renderWithQuery(<AdminSettingsPage />);
    await gotoNotifications();
    await user.click(await screen.findByRole("button", { name: /Language for team-alerts/i }));
    await user.click(await screen.findByRole("option", { name: /English/i }));
    await user.click(screen.getByRole("button", { name: /Save changes/i }));
    await waitFor(() => expect(saved).toBeDefined());
    expect(saved?.sinks[0]).toMatchObject({ name: "team-alerts", language: "en" });
  });

  it("removes a sink's language key when its row goes back to Instance default", async () => {
    const user = userEvent.setup();
    let saved: NotificationsCfg | undefined;
    seedSinks({
      sinks: [
        {
          name: "team-alerts",
          kind: "discord",
          enabled: true,
          configRef: "gameplane-notify-team-alerts",
          events: ["server.unhealthy"],
          language: "en",
        },
      ],
    });
    server.use(
      http.put("/admin/config/notifications", async ({ request }) => {
        saved = (await request.json()) as NotificationsCfg;
        return new HttpResponse(null, { status: 204 });
      }),
    );
    renderWithQuery(<AdminSettingsPage />);
    await gotoNotifications();
    await user.click(await screen.findByRole("button", { name: /Language for team-alerts/i }));
    await user.click(await screen.findByRole("option", { name: /Instance default/i }));
    await user.click(screen.getByRole("button", { name: /Save changes/i }));
    await waitFor(() => expect(saved).toBeDefined());
    expect(saved?.sinks[0]).toMatchObject({ name: "team-alerts" });
    expect(saved?.sinks[0]).not.toHaveProperty("language");
  });
});
