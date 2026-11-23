import { describe, it, expect, vi } from "vitest";
import type { ReactElement } from "react";
import { screen, within, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { http, HttpResponse } from "msw";
import { server } from "@/test/server";
import { renderWithQuery } from "@/test/render";
import { ResourceTargetProvider, type ServerAccess } from "@/lib/resourceTarget";
import { makeServer, makeTemplate } from "@/test/factories";
import type { GameServer, GameServerSecuritySpec } from "@/types";
import { SecuritySection, needsFileAccessWarning } from "./Security";
import { SettingsTab } from "../Settings";

const ALL = ["*"];
const WRITE_ONLY = ["servers:write"];

function accessFor(permissions: string[]): ServerAccess {
  return {
    canWrite: true,
    canControl: true,
    canConsole: false,
    canDelete: false,
    isOwner: false,
    isCollaborator: false,
    permissions,
  };
}

function draftWith(security?: GameServerSecuritySpec, status: Partial<GameServer["status"]> = {}): GameServer {
  const base = makeServer({ status });
  return { ...base, spec: { ...base.spec, security } };
}

function renderSection(ui: ReactElement, permissions: string[] = ALL) {
  return renderWithQuery(
    <ResourceTargetProvider target={{ cluster: "local", namespace: "gameplane-games", name: "alpha" }} access={accessFor(permissions)}>
      {ui}
    </ResourceTargetProvider>,
  );
}

describe("SecuritySection", () => {
  it("shows the four settings open by default with their module defaults", () => {
    const template = makeTemplate({
      spec: { security: { runAsUser: 1000, runAsGroup: 1000, volumeOwnershipCheck: "EveryStart" } },
    });
    renderSection(<SecuritySection draft={draftWith()} onChange={vi.fn()} template={template} />);

    expect(screen.getByRole("button", { name: /Run-as user/ })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /Run-as group/ })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /Filesystem group/ })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /Volume ownership check/ })).toBeInTheDocument();
    expect(screen.getAllByText("Module default: 1000")).toHaveLength(2);
    expect(screen.getByText("Module default: Every start")).toBeInTheDocument();
    // No template value for the filesystem group, so the image default is named.
    expect(screen.getByText("Module default: image default")).toBeInTheDocument();
  });

  it("names the image default and built-in ownership check when the template sets none", () => {
    renderSection(<SecuritySection draft={draftWith()} onChange={vi.fn()} />);

    expect(screen.getAllByText("Module default: image default")).toHaveLength(3);
    expect(screen.getByText("Module default: Only when wrong (built-in)")).toBeInTheDocument();
  });

  it("reports validity as valid when nothing is overridden", () => {
    const onValidityChange = vi.fn();
    renderSection(<SecuritySection draft={draftWith()} onChange={vi.fn()} onValidityChange={onValidityChange} />);
    expect(onValidityChange).toHaveBeenLastCalledWith(true);
  });

  it("counts overrides in the section header", () => {
    renderSection(
      <SecuritySection
        draft={draftWith({ runAsUser: { value: 1000 }, fsGroup: { useImageDefault: true } })}
        onChange={vi.fn()}
      />,
    );
    expect(screen.getByText("2 overrides")).toBeInTheDocument();
  });

  it("collapses and expands the settings from the header", async () => {
    renderSection(<SecuritySection draft={draftWith()} onChange={vi.fn()} />);
    const header = screen.getByRole("button", { name: /Advanced security/ });
    expect(header).toHaveAttribute("aria-expanded", "true");
    expect(screen.getByRole("button", { name: /Run-as user/ })).toBeInTheDocument();

    await userEvent.click(header);
    expect(header).toHaveAttribute("aria-expanded", "false");
    expect(screen.queryByRole("button", { name: /Run-as user/ })).not.toBeInTheDocument();

    await userEvent.click(header);
    expect(screen.getByRole("button", { name: /Run-as user/ })).toBeInTheDocument();
  });

  it("is read-only without the servers:security permission and shows values as text", () => {
    renderSection(<SecuritySection draft={draftWith({ runAsUser: { value: 1000 } })} onChange={vi.fn()} />, WRITE_ONLY);

    expect(screen.getByText("Read-only")).toBeInTheDocument();
    expect(screen.getByText(/Changing them needs the servers:security permission/)).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /Run-as user/ })).not.toBeInTheDocument();
    expect(screen.getByText("1000")).toBeInTheDocument();
  });

  it("is read-only when no access is provided at all", () => {
    renderWithQuery(
      <ResourceTargetProvider target={{ cluster: "local", name: "alpha" }}>
        <SecuritySection draft={draftWith()} onChange={vi.fn()} />
      </ResourceTargetProvider>,
    );
    expect(screen.getByText("Read-only")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /Run-as user/ })).not.toBeInTheDocument();
  });

  it("is read-only when the access carries no permission list", () => {
    renderWithQuery(
      <ResourceTargetProvider
        target={{ cluster: "local", name: "alpha" }}
        access={{ canWrite: true, canControl: true, canConsole: false, canDelete: false, isOwner: false, isCollaborator: false }}>
        <SecuritySection draft={draftWith()} onChange={vi.fn()} />
      </ResourceTargetProvider>,
    );
    expect(screen.getByText("Read-only")).toBeInTheDocument();
  });

  it("shows the stopped-server hold-back notice with the API's own message", () => {
    const draft = draftWith(
      { runAsUser: { value: 0 } },
      {
        phase: "Stopped",
        conditions: [
          { type: "SecurityAllowed", status: "False", reason: "RootOverrideDisallowed", message: "Root is off on this cluster." },
        ],
      },
    );
    renderSection(<SecuritySection draft={draft} onChange={vi.fn()} />);
    expect(screen.getByText("Will not start: root override not allowed")).toBeInTheDocument();
    expect(screen.getByText("Root is off on this cluster.")).toBeInTheDocument();
  });

  it("uses the default body when a running server is held back without a message", () => {
    const draft = draftWith(
      { runAsUser: { value: 0 } },
      {
        phase: "Running",
        conditions: [{ type: "SecurityAllowed", status: "False", reason: "RootOverrideDisallowed" }],
      },
    );
    renderSection(<SecuritySection draft={draft} onChange={vi.fn()} />);
    expect(screen.getByText("Keeps running, but will not start again")).toBeInTheDocument();
    expect(screen.getByText(/It will not start again until the override is cleared/)).toBeInTheDocument();
  });

  it("does not show the hold-back notice for other reasons", () => {
    const draft = draftWith(
      { runAsUser: { value: 0 } },
      {
        phase: "Stopped",
        conditions: [{ type: "SecurityAllowed", status: "True", reason: "Allowed" }],
      },
    );
    renderSection(<SecuritySection draft={draft} onChange={vi.fn()} />);
    expect(screen.queryByText("Will not start: root override not allowed")).not.toBeInTheDocument();
    expect(screen.queryByText("Keeps running, but will not start again")).not.toBeInTheDocument();
  });

  it("shows the restart note only while values are pending", () => {
    const status = {
      phase: "Running" as const,
      security: { pending: true },
    };
    const { unmount } = renderSection(<SecuritySection draft={draftWith(undefined, status)} onChange={vi.fn()} />);
    expect(screen.getByTestId("security-restart-note")).toHaveTextContent(/Applies on the next restart/);
    unmount();

    renderSection(
      <SecuritySection draft={draftWith(undefined, { phase: "Running", security: { pending: false } })} onChange={vi.fn()} />,
    );
    expect(screen.queryByTestId("security-restart-note")).not.toBeInTheDocument();
  });

  it("shows applied values with their source and the pending saved value", () => {
    const status = {
      phase: "Running" as const,
      security: {
        pending: true,
        applied: {
          runAsUser: { source: "ImageDefault" as const },
          runAsGroup: { value: 1000, source: "Override" as const },
          volumeOwnershipCheck: { value: "OnlyWhenWrong" as const, source: "BuiltInDefault" as const },
        },
        desired: {
          runAsUser: { value: 2000, source: "Override" as const },
          runAsGroup: { value: 1000, source: "Override" as const },
          fsGroup: { value: 5, source: "Override" as const },
        },
      },
    };
    renderSection(<SecuritySection draft={draftWith({ runAsUser: { value: 2000 } }, status)} onChange={vi.fn()} />);

    expect(screen.getByText("Applied: image default")).toBeInTheDocument();
    expect(screen.getByText("Applied: 1000")).toBeInTheDocument();
    expect(screen.getByText("Applied: Only when wrong")).toBeInTheDocument();
    expect(screen.getByText("Built-in default")).toBeInTheDocument();
    expect(screen.getByText(/Pending: 2000/)).toBeInTheDocument();
    // Filesystem group has never been applied, so its saved value is the next start.
    expect(screen.getByText("Next start: 5")).toBeInTheDocument();
  });

  it("flags that the filesystem group does nothing when no group applies", () => {
    renderSection(
      <SecuritySection
        draft={draftWith(undefined, { phase: "Running", security: { desired: { runAsUser: { value: 1000, source: "Override" } } } })}
        onChange={vi.fn()}
      />,
    );
    expect(screen.getByText("No effect: no filesystem group")).toBeInTheDocument();
  });

  it("does not flag the ownership check when the module sets a filesystem group", () => {
    const template = makeTemplate({ spec: { security: { fsGroup: 1000 } } });
    renderSection(<SecuritySection draft={draftWith()} onChange={vi.fn()} template={template} />);
    expect(screen.queryByText("No effect: no filesystem group")).not.toBeInTheDocument();
  });

  it("marks an overridden risky setting", () => {
    const template = makeTemplate({
      spec: { security: { risky: { runAsUser: { reason: "Runs as root." } } } },
    });
    renderSection(
      <SecuritySection draft={draftWith({ runAsUser: { value: 1000 } })} onChange={vi.fn()} template={template} />,
    );
    expect(screen.getByText("Risky")).toBeInTheDocument();
  });

  it("saves a typed custom value as a run-as override", async () => {
    const onChange = vi.fn();
    renderSection(<SecuritySection draft={draftWith()} onChange={onChange} />);

    await userEvent.click(screen.getByRole("button", { name: /Run-as user/ }));
    await userEvent.click(await screen.findByRole("option", { name: "Custom value" }));
    await userEvent.type(screen.getByLabelText("Run-as user value"), "1000{Enter}");

    expect(onChange).toHaveBeenLastCalledWith(
      expect.objectContaining({ spec: expect.objectContaining({ security: { runAsUser: { value: 1000 } } }) }),
    );
  });

  it("rejects an out-of-range ID without writing it and reports the section invalid", async () => {
    const onChange = vi.fn();
    const onValidityChange = vi.fn();
    renderSection(
      <SecuritySection draft={draftWith()} onChange={onChange} onValidityChange={onValidityChange} />,
    );

    await userEvent.click(screen.getByRole("button", { name: /Run-as group/ }));
    await userEvent.click(await screen.findByRole("option", { name: "Custom value" }));
    await userEvent.type(screen.getByLabelText("Run-as group value"), "4294967296{Enter}");

    expect(screen.getByText("Enter a whole number from 0 to 4294967295.")).toBeInTheDocument();
    expect(onValidityChange).toHaveBeenLastCalledWith(false);
    expect(onChange).not.toHaveBeenCalled();
  });

  it("clears an override when the owner picks Module default", async () => {
    const onChange = vi.fn();
    renderSection(<SecuritySection draft={draftWith({ runAsUser: { value: 1000 } })} onChange={onChange} />);

    await userEvent.click(screen.getByRole("button", { name: /Run-as user/ }));
    await userEvent.click(await screen.findByRole("option", { name: "Module default" }));

    expect(onChange).toHaveBeenLastCalledWith(
      expect.objectContaining({ spec: expect.objectContaining({ security: undefined }) }),
    );
  });

  it("sets the image default for a run-as user", async () => {
    const onChange = vi.fn();
    renderSection(<SecuritySection draft={draftWith()} onChange={onChange} />);

    await userEvent.click(screen.getByRole("button", { name: /Run-as user/ }));
    await userEvent.click(await screen.findByRole("option", { name: "Use image default" }));

    expect(onChange).toHaveBeenLastCalledWith(
      expect.objectContaining({ spec: expect.objectContaining({ security: { runAsUser: { useImageDefault: true } } }) }),
    );
  });

  it("sets the volume ownership check", async () => {
    const onChange = vi.fn();
    renderSection(<SecuritySection draft={draftWith()} onChange={onChange} />);

    await userEvent.click(screen.getByRole("button", { name: /Volume ownership check/ }));
    await userEvent.click(await screen.findByRole("option", { name: "Every start" }));

    expect(onChange).toHaveBeenLastCalledWith(
      expect.objectContaining({ spec: expect.objectContaining({ security: { volumeOwnershipCheck: "EveryStart" } }) }),
    );
  });

  it("asks before keeping a risky value and shows the module's reason as plain text", async () => {
    const onChange = vi.fn();
    const template = makeTemplate({
      spec: { security: { risky: { runAsUser: { reason: "Runs as <b>root</b>." } } } },
    });
    renderSection(<SecuritySection draft={draftWith()} onChange={onChange} template={template} />);

    await userEvent.click(screen.getByRole("button", { name: /Run-as user/ }));
    await userEvent.click(await screen.findByRole("option", { name: "Custom value" }));
    await userEvent.type(screen.getByLabelText("Run-as user value"), "0{Enter}");

    const dialog = await screen.findByRole("alertdialog");
    expect(within(dialog).getByText("Keep a risky value for Run-as user?")).toBeInTheDocument();
    expect(within(dialog).getByText("Runs as <b>root</b>.")).toBeInTheDocument();
    expect(onChange).not.toHaveBeenCalled();

    await userEvent.click(within(dialog).getByRole("button", { name: "Keep value" }));
    expect(onChange).toHaveBeenLastCalledWith(
      expect.objectContaining({ spec: expect.objectContaining({ security: { runAsUser: { value: 0 } } }) }),
    );
  });

  it("explains a risky setting without a reason when the module gives none", async () => {
    const template = makeTemplate({ spec: { security: { risky: { fsGroup: {} } } } });
    renderSection(<SecuritySection draft={draftWith()} onChange={vi.fn()} template={template} />);

    await userEvent.click(screen.getByRole("button", { name: /Filesystem group/ }));
    await userEvent.click(await screen.findByRole("option", { name: "Custom value" }));
    await userEvent.type(screen.getByLabelText("Filesystem group value"), "1000{Enter}");

    const dialog = await screen.findByRole("alertdialog");
    expect(within(dialog).getByText("The module marks this setting as risky.")).toBeInTheDocument();
  });

  it("discards a risky value when the owner cancels the confirmation", async () => {
    const onChange = vi.fn();
    const template = makeTemplate({ spec: { security: { risky: { runAsUser: { reason: "Runs as root." } } } } });
    renderSection(<SecuritySection draft={draftWith()} onChange={onChange} template={template} />);

    await userEvent.click(screen.getByRole("button", { name: /Run-as user/ }));
    await userEvent.click(await screen.findByRole("option", { name: "Custom value" }));
    await userEvent.type(screen.getByLabelText("Run-as user value"), "0{Enter}");

    const dialog = await screen.findByRole("alertdialog");
    await userEvent.click(within(dialog).getByRole("button", { name: "Cancel" }));
    await waitFor(() => expect(screen.queryByRole("alertdialog")).not.toBeInTheDocument());
    expect(onChange).not.toHaveBeenCalled();
  });
});

describe("needsFileAccessWarning", () => {
  const started = (security?: GameServerSecuritySpec) =>
    draftWith(security, { phase: "Running", security: { applied: { runAsUser: { value: 1000, source: "Override" } } } });

  it("is false without a baseline", () => {
    expect(needsFileAccessWarning(undefined, draftWith({ runAsUser: { value: 2000 } }))).toBe(false);
  });

  it("is false before the server has ever started", () => {
    expect(needsFileAccessWarning(draftWith(), draftWith({ runAsUser: { value: 2000 } }))).toBe(false);
  });

  it("is true when the run-as user changes on a server that has started", () => {
    expect(needsFileAccessWarning(started({ runAsUser: { value: 1000 } }), started({ runAsUser: { value: 2000 } }))).toBe(true);
  });

  it("is true when the run-as group is added on a server that has started", () => {
    expect(needsFileAccessWarning(started(), started({ runAsGroup: { value: 1000 } }))).toBe(true);
  });

  it("is true when the run-as user override is removed", () => {
    expect(needsFileAccessWarning(started({ runAsUser: { value: 1000 } }), started())).toBe(true);
  });

  it("is true when the run-as user moves to the image default", () => {
    expect(needsFileAccessWarning(started(), started({ runAsUser: { useImageDefault: true } }))).toBe(true);
  });

  it("is false when only the filesystem group or the ownership check changes", () => {
    const before = started({ runAsUser: { value: 1000 } });
    expect(needsFileAccessWarning(before, started({ runAsUser: { value: 1000 }, fsGroup: { value: 5 } }))).toBe(false);
    expect(needsFileAccessWarning(before, started({ runAsUser: { value: 1000 }, volumeOwnershipCheck: "EveryStart" }))).toBe(false);
  });

  it("is false when nothing about the run-as user or group changes", () => {
    expect(needsFileAccessWarning(started(), started())).toBe(false);
  });
});

describe("SettingsTab security section", () => {
  function serve(gs: GameServer, onPut: (body: GameServer) => void) {
    server.use(
      http.get("/templates/:name", () => HttpResponse.json(makeTemplate())),
      http.get("/servers/:name", () => HttpResponse.json(gs)),
      http.put("/servers/:name", async ({ request }) => {
        const body = (await request.json()) as GameServer;
        onPut(body);
        return HttpResponse.json(body);
      }),
    );
  }

  function renderSettings(gs: GameServer, permissions: string[] = ALL) {
    return renderWithQuery(
      <ResourceTargetProvider target={{ cluster: "local", name: "alpha" }} access={accessFor(permissions)}>
        <SettingsTab gs={gs} name="alpha" />
      </ResourceTargetProvider>,
    );
  }

  const startedServer = () =>
    makeServer({ status: { phase: "Running", security: { applied: { runAsUser: { value: 1000, source: "ModuleDefault" } } } } });

  it("lists Security between Placement and RBAC & access", async () => {
    serve(makeServer(), vi.fn<(body: GameServer) => void>());
    renderSettings(makeServer());
    const nav = await screen.findByRole("navigation");
    const labels = within(nav).getAllByRole("tab").map((b) => b.textContent?.trim());
    const placementIdx = labels.indexOf("Placement");

    expect(placementIdx).toBeGreaterThanOrEqual(0);
    expect(labels[placementIdx + 1]).toBe("Security");
    expect(labels[placementIdx + 2]).toBe("RBAC & access");
  });

  it("opens the Advanced security section from its tab", async () => {
    serve(makeServer(), vi.fn<(body: GameServer) => void>());
    renderSettings(makeServer());
    await userEvent.click(await screen.findByRole("tab", { name: "Security" }));
    expect(await screen.findByText("Advanced security")).toBeInTheDocument();
  });

  it("hides Save and explains the permission when the user lacks servers:security", async () => {
    serve(makeServer(), vi.fn<(body: GameServer) => void>());
    renderSettings(makeServer(), WRITE_ONLY);
    await userEvent.click(await screen.findByRole("tab", { name: "Security" }));
    expect(await screen.findByText(/You need permission to change this server/)).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /Save changes/ })).not.toBeInTheDocument();
  });

  it("asks before saving a changed run-as user on a server that has started, and saves on Save anyway", async () => {
    const put = vi.fn<(body: GameServer) => void>();
    serve(startedServer(), put);
    renderSettings(startedServer());

    await userEvent.click(await screen.findByRole("tab", { name: "Security" }));
    await userEvent.click(await screen.findByRole("button", { name: /Run-as user/ }));
    await userEvent.click(await screen.findByRole("option", { name: "Custom value" }));
    await userEvent.type(screen.getByLabelText("Run-as user value"), "2000{Enter}");

    const save = screen.getByRole("button", { name: /Save changes/ });
    await waitFor(() => expect(save).toBeEnabled());
    await userEvent.click(save);

    const dialog = await screen.findByRole("alertdialog");
    expect(within(dialog).getByText("Change who the game runs as?")).toBeInTheDocument();
    expect(put).not.toHaveBeenCalled();

    await userEvent.click(within(dialog).getByRole("button", { name: "Save anyway" }));
    await waitFor(() => expect(put).toHaveBeenCalledTimes(1));
    expect(put.mock.calls[0]![0].spec.security).toEqual({ runAsUser: { value: 2000 } });
  });

  it("keeps the change unsaved when the owner cancels the file-access warning", async () => {
    const put = vi.fn<(body: GameServer) => void>();
    serve(startedServer(), put);
    renderSettings(startedServer());

    await userEvent.click(await screen.findByRole("tab", { name: "Security" }));
    await userEvent.click(await screen.findByRole("button", { name: /Run-as user/ }));
    await userEvent.click(await screen.findByRole("option", { name: "Custom value" }));
    await userEvent.type(screen.getByLabelText("Run-as user value"), "2000{Enter}");

    const save = screen.getByRole("button", { name: /Save changes/ });
    await waitFor(() => expect(save).toBeEnabled());
    await userEvent.click(save);

    const dialog = await screen.findByRole("alertdialog");
    await userEvent.click(within(dialog).getByRole("button", { name: "Cancel" }));
    await waitFor(() => expect(screen.queryByRole("alertdialog")).not.toBeInTheDocument());
    expect(put).not.toHaveBeenCalled();
    expect(screen.getByRole("button", { name: /Save changes/ })).toBeEnabled();
  });

  it("saves a run-as change without confirmation when the server has never started", async () => {
    const put = vi.fn<(body: GameServer) => void>();
    serve(makeServer(), put);
    renderSettings(makeServer());

    await userEvent.click(await screen.findByRole("tab", { name: "Security" }));
    await userEvent.click(await screen.findByRole("button", { name: /Run-as user/ }));
    await userEvent.click(await screen.findByRole("option", { name: "Custom value" }));
    await userEvent.type(screen.getByLabelText("Run-as user value"), "2000{Enter}");

    const save = screen.getByRole("button", { name: /Save changes/ });
    await waitFor(() => expect(save).toBeEnabled());
    await userEvent.click(save);

    await waitFor(() => expect(put).toHaveBeenCalledTimes(1));
    expect(screen.queryByRole("alertdialog")).not.toBeInTheDocument();
  });

  it("saves a filesystem-group change without confirmation on a started server", async () => {
    const put = vi.fn<(body: GameServer) => void>();
    serve(startedServer(), put);
    renderSettings(startedServer());

    await userEvent.click(await screen.findByRole("tab", { name: "Security" }));
    await userEvent.click(await screen.findByRole("button", { name: /Filesystem group/ }));
    await userEvent.click(await screen.findByRole("option", { name: "Custom value" }));
    await userEvent.type(screen.getByLabelText("Filesystem group value"), "5{Enter}");

    const save = screen.getByRole("button", { name: /Save changes/ });
    await waitFor(() => expect(save).toBeEnabled());
    await userEvent.click(save);

    await waitFor(() => expect(put).toHaveBeenCalledTimes(1));
    expect(screen.queryByRole("alertdialog")).not.toBeInTheDocument();
  });
});
