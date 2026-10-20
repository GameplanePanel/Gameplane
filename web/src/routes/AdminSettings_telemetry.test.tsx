import { describe, it, expect, vi } from "vitest";
import type { ReactNode } from "react";
import { http, HttpResponse } from "msw";
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { server } from "@/test/server";
import { renderWithQuery } from "@/test/render";
import { makeConfig } from "@/test/factories";
import type { TelemetryCfg } from "@/lib/config";
import type { TelemetryInfo } from "@/lib/api";
import { TELEMETRY_STATEMENT_URL } from "@/lib/links";

vi.mock("@tanstack/react-router", () => ({
  Link: ({ children, to, ...rest }: { children: ReactNode; to: string } & Record<string, unknown>) => (
    <a href={to} {...rest}>{children}</a>
  ),
}));

import { AdminSettingsPage } from "./AdminSettings";

const ID = "3f1c2a9e-8b4d-4e57-9a61-0c2d7e5b9f10";

const PREVIEW: NonNullable<TelemetryInfo["preview"]> = {
  version: "0.3.0",
  servers: 3,
  templates: 7,
  ext: {
    schema: 2,
    installId: ID,
    env: { k8s: ["1.31"], distro: ["k3s"], arch: ["amd64"], nodes: "1", deployment: "cluster" },
    games: { official: { "minecraft-java": 2, terraria: 1 }, custom: 0 },
    features: {
      wakeOnConnect: true, tunnels: ["playit"], capture: false, backups: true,
      sso: false, auditForwarding: false, clusters: "1", db: "sqlite", language: "en",
    },
    key: "pV0mY3Qn8wK2b7cXlR4sTt9uJ1eHaZ6dFgQmNoP5rSk",
    sentAt: "2026-10-06T09:12:44Z",
  },
};

function makeInfo(over: Partial<TelemetryInfo> = {}): TelemetryInfo {
  return {
    destination: { kind: "default", host: "telemetry.example.org" },
    operatorDisabled: false,
    consent: { basic: true, extended: true, source: "default" },
    installId: ID,
    preview: PREVIEW,
    status: {
      lastAttemptAt: "2026-10-06T09:12:44Z",
      lastSuccessAt: "2026-10-06T09:12:44Z",
      lastOutcome: "ok",
      lastIdRotationAt: null,
    },
    ...over,
  };
}

// Serves the config and telemetry info; returns request counters/bodies.
function setup(cfg: TelemetryCfg, info: TelemetryInfo | "error") {
  const seen = { infoGets: 0, puts: [] as unknown[], resets: 0 };
  server.use(
    http.get("/admin/config", () => HttpResponse.json(makeConfig({ telemetry: cfg }))),
    http.get("/admin/telemetry", () => {
      seen.infoGets += 1;
      return info === "error"
        ? HttpResponse.text("boom", { status: 500 })
        : HttpResponse.json(info);
    }),
    http.put("/admin/config/telemetry", async ({ request }) => {
      seen.puts.push(await request.json());
      return new HttpResponse(null, { status: 204 });
    }),
  );
  return seen;
}

async function open() {
  await screen.findByRole("heading", { name: /Admin settings/i });
  await userEvent.click(await screen.findByRole("button", { name: /Telemetry/i }));
}

// A disabled HeroUI switch may expose `disabled`, `aria-disabled` or
// `data-disabled` depending on the element carrying role="switch".
function switchDisabled(el: HTMLElement): boolean {
  return (
    el.hasAttribute("disabled") ||
    el.getAttribute("aria-disabled") === "true" ||
    el.getAttribute("data-disabled") === "true"
  );
}

const ON: TelemetryCfg = { sendMetrics: true, extended: true };
const BASIC: TelemetryCfg = { sendMetrics: true, extended: false };
const OFF: TelemetryCfg = { sendMetrics: false, extended: false };

describe("AdminSettings telemetry: destination", () => {
  it("shows the project default with a statement link", async () => {
    setup(ON, makeInfo());
    renderWithQuery(<AdminSettingsPage />);
    await open();
    expect(await screen.findByText("Sending to telemetry.example.org")).toBeInTheDocument();
    expect(screen.getByText("Project default")).toBeInTheDocument();
    expect(screen.getByText("The Gameplane project's telemetry service.")).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "Data-handling statement" })).toHaveAttribute(
      "href",
      TELEMETRY_STATEMENT_URL,
    );
  });

  it("shows a custom destination", async () => {
    setup(ON, makeInfo({ destination: { kind: "custom", host: "telemetry.corp.example.net" } }));
    renderWithQuery(<AdminSettingsPage />);
    await open();
    expect(await screen.findByText("Sending to telemetry.corp.example.net")).toBeInTheDocument();
    expect(screen.getByText("Custom")).toBeInTheDocument();
    expect(
      screen.getByText("Custom destination set by your operator. Nothing is sent to the Gameplane project."),
    ).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "Data-handling statement" })).toBeInTheDocument();
  });

  it("shows the bundled receiver", async () => {
    setup(BASIC, makeInfo({
      destination: { kind: "bundled", host: "gameplane-telemetry-receiver.gameplane.svc" },
      installId: null,
      preview: { version: "0.3.0", servers: 5, templates: 7 },
    }));
    renderWithQuery(<AdminSettingsPage />);
    await open();
    expect(await screen.findByText("Sending to gameplane-telemetry-receiver.gameplane.svc")).toBeInTheDocument();
    expect(screen.getByText("Bundled")).toBeInTheDocument();
    expect(screen.getByText("The receiver bundled with this install. Reports stay inside your cluster.")).toBeInTheDocument();
  });

  it("shows no destination", async () => {
    setup(OFF, makeInfo({
      destination: { kind: "none", host: null },
      installId: null,
      preview: null,
    }));
    renderWithQuery(<AdminSettingsPage />);
    await open();
    expect(await screen.findByText("No telemetry destination configured.")).toBeInTheDocument();
    expect(screen.getByText("No destination")).toBeInTheDocument();
    expect(screen.getByText("Nothing to preview: no destination is configured.")).toBeInTheDocument();
    expect(screen.queryByRole("link", { name: "Data-handling statement" })).not.toBeInTheDocument();
    expect(screen.queryByText(/No report sent yet|Last report sent|Last attempt failed/)).not.toBeInTheDocument();
    // Basic stays operable when there is no destination.
    expect(switchDisabled(screen.getByRole("switch", { name: "Enable telemetry" }))).toBe(false);
  });

  it("blocks both switches and saving when the operator disabled telemetry", async () => {
    const seen = setup(ON, makeInfo({
      destination: { kind: "disabled", host: null },
      operatorDisabled: true,
      installId: null,
      preview: null,
    }));
    renderWithQuery(<AdminSettingsPage />);
    await open();
    expect(await screen.findByText("Telemetry is disabled by the operator.")).toBeInTheDocument();
    expect(screen.getByText("Disabled")).toBeInTheDocument();
    expect(screen.getByText("Nothing to preview: telemetry is disabled by the operator.")).toBeInTheDocument();
    // Stored consent is on, but the UI shows both off and inert.
    const basic = screen.getByRole("switch", { name: "Enable telemetry" });
    const ext = screen.getByRole("switch", { name: "Enable extended telemetry" });
    expect(basic).toHaveAttribute("aria-checked", "false");
    expect(ext).toHaveAttribute("aria-checked", "false");
    expect(switchDisabled(basic)).toBe(true);
    expect(switchDisabled(ext)).toBe(true);
    expect(screen.getByRole("button", { name: "Save changes" })).toBeDisabled();
    expect(screen.getByRole("button", { name: "Reset ID" })).toBeDisabled();
    await userEvent.click(screen.getByRole("button", { name: "Save changes" }));
    expect(seen.puts).toHaveLength(0);
  });

  it("also treats operatorDisabled without kind disabled as disabled", async () => {
    setup(ON, makeInfo({ operatorDisabled: true }));
    renderWithQuery(<AdminSettingsPage />);
    await open();
    expect(await screen.findByText("Telemetry is disabled by the operator.")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Save changes" })).toBeDisabled();
  });

  it("still renders and saves when GET /admin/telemetry fails", async () => {
    const seen = setup(OFF, "error");
    renderWithQuery(<AdminSettingsPage />);
    await open();
    await userEvent.click(await screen.findByRole("switch", { name: "Enable telemetry" }));
    await userEvent.click(screen.getByRole("button", { name: "Save changes" }));
    expect(await screen.findByText("Saved")).toBeInTheDocument();
    expect(seen.puts).toEqual([{ sendMetrics: true, extended: false }]);
    expect(screen.queryByText(/^Sending to/)).not.toBeInTheDocument();
    expect(screen.getByText("No install ID while extended metrics are off.")).toBeInTheDocument();
  });
});

describe("AdminSettings telemetry: switches", () => {
  it("names the install ID in the subtitle", async () => {
    setup(ON, makeInfo());
    renderWithQuery(<AdminSettingsPage />);
    await open();
    expect(
      await screen.findByText("Anonymous usage metrics help us prioritize work. Extended metrics include a random install ID."),
    ).toBeInTheDocument();
  });

  it("disables and shows extended off while basic is off", async () => {
    setup(OFF, makeInfo({ installId: null, preview: null }));
    renderWithQuery(<AdminSettingsPage />);
    await open();
    const ext = await screen.findByRole("switch", { name: "Enable extended telemetry" });
    expect(ext).toHaveAttribute("aria-checked", "false");
    expect(switchDisabled(ext)).toBe(true);
    expect(screen.getByText("Turn on basic usage metrics to enable extended usage metrics.")).toBeInTheDocument();
    expect(screen.getByText("Nothing to preview: basic usage metrics are off.")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Reset ID" })).toBeDisabled();
  });

  it("enables extended once basic is turned on and saves both", async () => {
    const seen = setup(OFF, makeInfo({ installId: null, preview: null }));
    renderWithQuery(<AdminSettingsPage />);
    await open();
    await userEvent.click(await screen.findByRole("switch", { name: "Enable telemetry" }));
    const ext = screen.getByRole("switch", { name: "Enable extended telemetry" });
    expect(switchDisabled(ext)).toBe(false);
    expect(screen.getByText(/Adds Kubernetes version, distribution/)).toBeInTheDocument();
    await userEvent.click(ext);
    expect(screen.getByRole("switch", { name: "Disable extended telemetry" })).toHaveAttribute("aria-checked", "true");
    await userEvent.click(screen.getByRole("button", { name: "Save changes" }));
    expect(await screen.findByText("Saved")).toBeInTheDocument();
    expect(seen.puts).toEqual([{ sendMetrics: true, extended: true }]);
  });

  it("turning basic off clears extended in the saved body", async () => {
    const seen = setup(ON, makeInfo());
    renderWithQuery(<AdminSettingsPage />);
    await open();
    await userEvent.click(await screen.findByRole("switch", { name: "Disable telemetry" }));
    const ext = screen.getByRole("switch", { name: "Enable extended telemetry" });
    expect(ext).toHaveAttribute("aria-checked", "false");
    expect(switchDisabled(ext)).toBe(true);
    await userEvent.click(screen.getByRole("button", { name: "Save changes" }));
    expect(await screen.findByText("Saved")).toBeInTheDocument();
    expect(seen.puts).toEqual([{ sendMetrics: false, extended: false }]);
  });

  it("refetches the telemetry info after a save", async () => {
    const seen = setup(OFF, makeInfo({ installId: null, preview: null }));
    renderWithQuery(<AdminSettingsPage />);
    await open();
    await userEvent.click(await screen.findByRole("switch", { name: "Enable telemetry" }));
    const before = seen.infoGets;
    await userEvent.click(screen.getByRole("button", { name: "Save changes" }));
    await screen.findByText("Saved");
    await waitFor(() => expect(seen.infoGets).toBeGreaterThan(before));
  });
});

describe("AdminSettings telemetry: install ID", () => {
  it("shows the ID and help text when extended is on", async () => {
    setup(ON, makeInfo());
    renderWithQuery(<AdminSettingsPage />);
    await open();
    expect(await screen.findByText(ID)).toBeInTheDocument();
    expect(screen.getByText(/Random and anonymous\. Reset it to start reporting under a new ID/)).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Reset ID" })).toBeEnabled();
  });

  it("shows the empty text and a disabled reset when there is no ID", async () => {
    setup(BASIC, makeInfo({ installId: null, preview: { version: "0.3.0", servers: 5, templates: 7 } }));
    renderWithQuery(<AdminSettingsPage />);
    await open();
    expect(await screen.findByText("No install ID while extended metrics are off.")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Reset ID" })).toBeDisabled();
  });

  it("resets the ID after confirmation and shows the new one", async () => {
    const NEW_ID = "aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee";
    let current = ID;
    const seen = setup(ON, makeInfo());
    server.use(
      http.get("/admin/telemetry", () => HttpResponse.json(makeInfo({ installId: current }))),
      http.post("/admin/telemetry/install-id", () => {
        seen.resets += 1;
        current = NEW_ID;
        return HttpResponse.json({ installId: NEW_ID });
      }),
    );
    renderWithQuery(<AdminSettingsPage />);
    await open();
    await userEvent.click(await screen.findByRole("button", { name: "Reset ID" }));
    const dialog = await screen.findByRole("alertdialog");
    expect(within(dialog).getByText("Reset install ID?")).toBeInTheDocument();
    expect(within(dialog).getByText(new RegExp(`replaces your random install ID ${ID} with a new one`))).toBeInTheDocument();
    await userEvent.click(within(dialog).getByRole("button", { name: "Reset ID" }));
    expect(await screen.findByText(NEW_ID)).toBeInTheDocument();
    expect(seen.resets).toBe(1);
    await waitFor(() => expect(screen.queryByRole("alertdialog")).not.toBeInTheDocument());
  });

  it("cancels the reset without calling the API", async () => {
    const seen = setup(ON, makeInfo());
    server.use(
      http.post("/admin/telemetry/install-id", () => {
        seen.resets += 1;
        return HttpResponse.json({ installId: "x" });
      }),
    );
    renderWithQuery(<AdminSettingsPage />);
    await open();
    await userEvent.click(await screen.findByRole("button", { name: "Reset ID" }));
    const dialog = await screen.findByRole("alertdialog");
    await userEvent.click(within(dialog).getByRole("button", { name: "Cancel" }));
    await waitFor(() => expect(screen.queryByRole("alertdialog")).not.toBeInTheDocument());
    expect(seen.resets).toBe(0);
    expect(screen.getByText(ID)).toBeInTheDocument();
  });

  it("surfaces a reset failure", async () => {
    setup(ON, makeInfo());
    server.use(
      http.post("/admin/telemetry/install-id", () => HttpResponse.text("extended telemetry is off", { status: 409 })),
    );
    renderWithQuery(<AdminSettingsPage />);
    await open();
    await userEvent.click(await screen.findByRole("button", { name: "Reset ID" }));
    const dialog = await screen.findByRole("alertdialog");
    await userEvent.click(within(dialog).getByRole("button", { name: "Reset ID" }));
    expect(await screen.findByText(/extended telemetry is off/i)).toBeInTheDocument();
    expect(screen.getByText(ID)).toBeInTheDocument();
  });
});

describe("AdminSettings telemetry: preview", () => {
  it("renders the preview JSON read-only", async () => {
    setup(ON, makeInfo());
    renderWithQuery(<AdminSettingsPage />);
    await open();
    const block = await screen.findByRole("region", { name: "Next report JSON" });
    expect(block.textContent).toBe(JSON.stringify(PREVIEW, null, 2));
    expect(screen.getByText("Next report preview")).toBeInTheDocument();
    expect(screen.getByText("Exactly what the next report contains. Read-only.")).toBeInTheDocument();
  });

  it("renders a basic-only preview without ext", async () => {
    setup(BASIC, makeInfo({ installId: null, preview: { version: "0.3.0", servers: 5, templates: 7 } }));
    renderWithQuery(<AdminSettingsPage />);
    await open();
    const block = await screen.findByRole("region", { name: "Next report JSON" });
    expect(block.textContent).toBe(JSON.stringify({ version: "0.3.0", servers: 5, templates: 7 }, null, 2));
  });
});

describe("AdminSettings telemetry: status", () => {
  const status = (over: Partial<TelemetryInfo["status"]>): TelemetryInfo["status"] => ({
    lastAttemptAt: null, lastSuccessAt: null, lastOutcome: "never", lastIdRotationAt: null, ...over,
  });

  it("never", async () => {
    setup(BASIC, makeInfo({ status: status({}) }));
    renderWithQuery(<AdminSettingsPage />);
    await open();
    expect(await screen.findByText("No report sent yet.")).toBeInTheDocument();
    expect(screen.queryByText(/Install ID replaced on/)).not.toBeInTheDocument();
  });

  it("ok", async () => {
    setup(ON, makeInfo());
    renderWithQuery(<AdminSettingsPage />);
    await open();
    expect(await screen.findByText("Last report sent successfully on Oct 6, 2026, 09:12 UTC.")).toBeInTheDocument();
  });

  it("ok without a success timestamp falls back to a neutral phrase", async () => {
    setup(ON, makeInfo({ status: status({ lastOutcome: "ok" }) }));
    renderWithQuery(<AdminSettingsPage />);
    await open();
    expect(await screen.findByText("Last report sent successfully on an earlier attempt.")).toBeInTheDocument();
  });

  it("failed with a previous success", async () => {
    setup(ON, makeInfo({ status: status({ lastOutcome: "failed", lastSuccessAt: "2026-10-05T09:10:00Z" }) }));
    renderWithQuery(<AdminSettingsPage />);
    await open();
    expect(
      await screen.findByText("Last attempt failed, will retry. Last successful report: Oct 5, 2026, 09:10 UTC."),
    ).toBeInTheDocument();
  });

  it("failed with no success yet", async () => {
    setup(ON, makeInfo({ status: status({ lastOutcome: "failed" }) }));
    renderWithQuery(<AdminSettingsPage />);
    await open();
    expect(
      await screen.findByText("Last attempt failed, will retry. No report has been delivered yet."),
    ).toBeInTheDocument();
  });

  it("shows the ID rotation line (T085/T087)", async () => {
    setup(ON, makeInfo({
      destination: { kind: "custom", host: "telemetry.corp.example.net" },
      status: status({
        lastOutcome: "failed",
        lastSuccessAt: "2026-10-05T09:10:00Z",
        lastIdRotationAt: "2026-10-04T08:00:00Z",
      }),
    }));
    renderWithQuery(<AdminSettingsPage />);
    await open();
    expect(
      await screen.findByText("Install ID replaced on Oct 4, 2026: the destination reported it was in use by another key."),
    ).toBeInTheDocument();
    expect(screen.getByText(/Last attempt failed, will retry/)).toBeInTheDocument();
  });

  it("prints an unparseable timestamp as received instead of crashing", async () => {
    setup(ON, makeInfo({ status: status({ lastOutcome: "ok", lastSuccessAt: "not-a-date" }) }));
    renderWithQuery(<AdminSettingsPage />);
    await open();
    expect(await screen.findByText("Last report sent successfully on not-a-date.")).toBeInTheDocument();
  });
});
