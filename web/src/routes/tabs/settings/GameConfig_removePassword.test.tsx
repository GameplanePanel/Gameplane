import { useState } from "react";
import { describe, expect, it, vi } from "vitest";
import { fireEvent, screen } from "@testing-library/react";

import { renderWithQuery } from "@/test/render";
import { ResourceTargetProvider } from "@/lib/resourceTarget";
import { CONFIG_REDACTED_MARKER } from "@/lib/validation";
import type { GameServer, GameTemplate } from "@/types";
import { GameConfigSection } from "./GameConfig";

// Spec 021 OD-2: a stored OPTIONAL password is cleared only through the
// explicit Remove button; required passwords cannot be removed.

type Schema = NonNullable<GameTemplate["spec"]["configSchema"]>;

const SCHEMA: Schema = [
  { name: "SERVER_PASSWORD", displayName: "Server password", type: "password" },
  { name: "ADMIN_PASSWORD", displayName: "Admin password", type: "password", required: true },
  { name: "MOTD", displayName: "MOTD", type: "string" },
];

const template: GameTemplate = {
  metadata: { name: "minecraft-java" },
  spec: { displayName: "Minecraft", game: "minecraft-java", version: "1.0", image: "x", configSchema: SCHEMA },
};

function server(config: Record<string, string>): GameServer {
  return {
    metadata: { name: "mc-survival", namespace: "gameplane-games", resourceVersion: "1" },
    spec: { templateRef: { name: "minecraft-java" }, config },
  };
}

function Harness({ initial, canWrite = true, spy }: { initial: GameServer; canWrite?: boolean; spy?: (next: GameServer) => void }) {
  const [draft, setDraft] = useState(initial);
  return (
    <ResourceTargetProvider
      target={{ cluster: "local", name: "mc-survival", namespace: "gameplane-games" }}
      access={{ canWrite, canControl: canWrite, canConsole: canWrite, canDelete: canWrite, isOwner: false, isCollaborator: false, permissions: canWrite ? ["*"] : ["servers:read"] }}
    >
      <GameConfigSection
        draft={draft}
        template={template}
        onChange={(next) => {
          spy?.(next);
          setDraft(next);
        }}
      />
    </ResourceTargetProvider>
  );
}

const STORED = { SERVER_PASSWORD: CONFIG_REDACTED_MARKER, ADMIN_PASSWORD: CONFIG_REDACTED_MARKER };

describe("GameConfigSection: removing a stored optional password", () => {
  it("offers Remove only for the optional password, never the required one", () => {
    renderWithQuery(<Harness initial={server(STORED)} />);
    expect(screen.getAllByRole("button", { name: "Remove password" })).toHaveLength(1);
  });

  it("does not offer Remove for a password that is not stored", () => {
    renderWithQuery(<Harness initial={server({ ADMIN_PASSWORD: CONFIG_REDACTED_MARKER })} />);
    expect(screen.queryByRole("button", { name: "Remove password" })).toBeNull();
  });

  it("Remove drops the key, shows the will-be-removed state, and never renders the marker", () => {
    const spy = vi.fn();
    const { container } = renderWithQuery(<Harness initial={server(STORED)} spy={spy} />);
    fireEvent.click(screen.getByRole("button", { name: "Remove password" }));
    expect(spy.mock.calls.at(-1)?.[0].spec.config).toEqual({ ADMIN_PASSWORD: CONFIG_REDACTED_MARKER });
    const input = screen.getByLabelText(/Server password/);
    expect(input).toHaveValue("");
    expect(input).toHaveAttribute("placeholder", "Password will be removed");
    expect(screen.getByText("Removed when you save. Undo to keep the stored password.")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Remove password" })).toBeNull();
    expect(container.innerHTML).not.toContain(CONFIG_REDACTED_MARKER);
  });

  it("removing the only stored password writes an empty map as absent", () => {
    const spy = vi.fn();
    renderWithQuery(<Harness initial={server({ SERVER_PASSWORD: CONFIG_REDACTED_MARKER })} spy={spy} />);
    fireEvent.click(screen.getByRole("button", { name: "Remove password" }));
    expect(spy.mock.calls.at(-1)?.[0].spec.config).toBeUndefined();
  });

  it("Undo restores the marker and the unchanged state", () => {
    const spy = vi.fn();
    renderWithQuery(<Harness initial={server(STORED)} spy={spy} />);
    fireEvent.click(screen.getByRole("button", { name: "Remove password" }));
    fireEvent.click(screen.getByRole("button", { name: "Undo" }));
    expect(spy.mock.calls.at(-1)?.[0].spec.config).toEqual(STORED);
    expect(screen.getByLabelText(/Server password/)).toHaveAttribute("placeholder", "Unchanged — type to replace");
    expect(screen.getByRole("button", { name: "Remove password" })).toBeInTheDocument();
  });

  it("typing a new value after Remove replaces the password and ends the removed state", () => {
    const spy = vi.fn();
    renderWithQuery(<Harness initial={server(STORED)} spy={spy} />);
    fireEvent.click(screen.getByRole("button", { name: "Remove password" }));
    fireEvent.change(screen.getByLabelText(/Server password/), { target: { value: "n3w" } });
    expect(spy.mock.calls.at(-1)?.[0].spec.config).toEqual({ ...STORED, SERVER_PASSWORD: "n3w" });
    expect(screen.queryByText("Removed when you save. Undo to keep the stored password.")).toBeNull();
    expect(screen.queryByRole("button", { name: "Undo" })).toBeNull();
  });

  it("emptying the field while removed keeps it removed", () => {
    const spy = vi.fn();
    renderWithQuery(<Harness initial={server(STORED)} spy={spy} />);
    fireEvent.click(screen.getByRole("button", { name: "Remove password" }));
    const calls = spy.mock.calls.length;
    fireEvent.change(screen.getByLabelText(/Server password/), { target: { value: "" } });
    expect(spy.mock.calls.length).toBe(calls);
    expect(screen.getByRole("button", { name: "Undo" })).toBeInTheDocument();
  });

  it("is not offered without write access", () => {
    renderWithQuery(<Harness canWrite={false} initial={server(STORED)} />);
    expect(screen.getByRole("button", { name: "Remove password" })).toBeDisabled();
  });
});
