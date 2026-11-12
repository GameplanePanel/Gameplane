import { useState } from "react";
import { describe, expect, it, vi } from "vitest";
import { fireEvent, screen } from "@testing-library/react";

import { renderWithQuery } from "@/test/render";
import { ResourceTargetProvider } from "@/lib/resourceTarget";
import { CONFIG_REDACTED_MARKER } from "@/lib/validation";
import type { GameServer, GameTemplate } from "@/types";
import { GameConfigSection } from "./GameConfig";

type Schema = NonNullable<GameTemplate["spec"]["configSchema"]>;

const SCHEMA: Schema = [
  { name: "WORLD_NAME", displayName: "World name", type: "string", default: "Gameplane" },
  { name: "DIFFICULTY", displayName: "Difficulty", type: "enum", enum: ["Classic", "Expert"], default: "Classic" },
  { name: "MAX_PLAYERS", displayName: "Max players", type: "int", default: "16", min: 1, max: 255 },
  { name: "PVP", displayName: "PvP", type: "bool", default: "true" },
  { name: "SERVER_PASSWORD", displayName: "Server password", type: "password" },
  { name: "MOTD", displayName: "MOTD", type: "string", target: "file" },
];

const heaps: Schema = [
  { name: "INIT_MEMORY", type: "string", autoFromMemoryLimit: { percent: 75 }, applyOnRestart: true },
  { name: "MAX_MEMORY", type: "string", autoFromMemoryLimit: { percent: 75 }, applyOnRestart: true },
];

it("previews the two heap fields independently and rejects an initial heap above the maximum", () => {
  const gs = server({ MAX_MEMORY: "2G" });
  const template = tmpl(heaps);
  template.spec.resources = { limits: { memory: "4Gi" } };
  renderWithQuery(<Harness initial={gs} template={template} />);
  expect(screen.getByText(/3072M/)).toBeInTheDocument();
  expect(screen.getByText(/initial heap exceeds the maximum heap/i)).toBeInTheDocument();
  fireEvent.change(screen.getByLabelText("INIT_MEMORY"), { target: { value: "37.5%" } });
  expect(screen.queryByText(/initial heap exceeds the maximum heap/i)).not.toBeInTheDocument();
  expect(screen.getByText(/InitialRAMPercentage=37.5/)).toBeInTheDocument();
  expect(screen.getByTestId("config-restart-note")).toHaveTextContent(/next container start/i);
});

it("reports immediate fields when a custom template mixes startup and ordinary configuration", () => {
  const template = tmpl([...heaps, { name: "MOTD", type: "string" }]);
  renderWithQuery(<Harness initial={server()} template={template} />);
  fireEvent.change(screen.getByLabelText("MOTD"), { target: { value: "changed" } });
  expect(screen.getByTestId("config-restart-note")).toHaveTextContent(/restarts the server/i);
});

it("uses only declared environment options and keeps a custom launcher editable without guessing heap arguments", () => {
  const gs = server({ MAX_MEMORY: "512M" });
  gs.spec.image = "example/custom-launcher";
  const template = tmpl([heaps[1], { name: "JVM_OPTS", type: "string", target: "file" }]);
  renderWithQuery(<Harness initial={gs} template={template} canWrite={false} />);
  const startup = screen.getByRole("group", { name: "JVM startup" });
  expect(startup).toHaveTextContent("MAX_MEMORY");
  expect(startup).not.toHaveTextContent("JVM_OPTS");
  expect(screen.queryByText(/Heap options/)).not.toBeInTheDocument();
  expect(screen.queryByLabelText("INIT_MEMORY")).not.toBeInTheDocument();
  expect(screen.getByLabelText("MAX_MEMORY")).toBeDisabled();
  expect(screen.queryByTestId("config-restart-note")).not.toBeInTheDocument();
});

it("uses the launcher's MEMORY fallback with a partial schema and no memory limit", () => {
  const gs = server({ MAX_MEMORY: "512M" });
  const template = tmpl([heaps[1]]);
  template.spec.env = [{ name: "MEMORY", value: "2G" }];
  renderWithQuery(<Harness initial={gs} template={template} />);
  expect(screen.getByText(/-Xms2G -Xmx512M/)).toBeInTheDocument();
  expect(screen.getByText(/initial heap exceeds the maximum heap/i)).toBeInTheDocument();
  expect(screen.queryByLabelText("INIT_MEMORY")).not.toBeInTheDocument();
});

it("omits the image-specific preview for a template that replaces its entrypoint", () => {
  const template = tmpl(heaps);
  template.spec.command = ["/custom/launch"];
  renderWithQuery(<Harness initial={server({ INIT_MEMORY: "2G", MAX_MEMORY: "1G" })} template={template} />);
  expect(screen.queryByText(/Heap options before/)).not.toBeInTheDocument();
});

it("keeps the adoption warning until the running pod reports the selected revision", () => {
  const gs = server({ MAX_MEMORY: "2G" }, { phase: "Running", templateRevision: { running: "old" } });
  renderWithQuery(<Harness initial={gs} template={tmpl(heaps)} />);
  expect(screen.getByTestId("config-restart-note")).toHaveTextContent(/may restart once/i);
});

it("warns about adoption when the running server still uses its baseline Pod", () => {
  const gs = server();
  gs.metadata.annotations = { ...gs.metadata.annotations, "gameplane.local/template-baseline": "admitted" };
  renderWithQuery(<Harness initial={gs} template={tmpl(heaps)} />);
  expect(screen.getByTestId("config-restart-note")).toHaveTextContent(/may restart once/i);
});

function tmpl(configSchema: Schema = SCHEMA): GameTemplate {
  return {
    metadata: { name: "minecraft-java" },
    spec: { displayName: "Minecraft", game: "minecraft-java", version: "1.0", image: "itzg/minecraft-server:java21", configSchema },
  };
}

function server(config?: Record<string, string>, status?: GameServer["status"]): GameServer {
  return {
    metadata: { name: "mc-survival", namespace: "gameplane-games", resourceVersion: "1", annotations: { "gameplane.local/effective-template-revision": "admitted" } },
    spec: { templateRef: { name: "minecraft-java" }, config },
    status: status ?? { phase: "Running", templateRevision: { running: "admitted" } },
  };
}

function Harness({
  initial,
  template = tmpl(),
  canWrite = true,
  spy,
  onValidity,
}: {
  initial: GameServer;
  template?: GameTemplate;
  canWrite?: boolean;
  spy?: (next: GameServer) => void;
  onValidity?: (valid: boolean) => void;
}) {
  const [draft, setDraft] = useState(initial);
  return (
    <ResourceTargetProvider
      target={{ cluster: "local", name: "mc-survival", namespace: "gameplane-games" }}
      access={{ canWrite, canControl: canWrite, canConsole: canWrite, canDelete: canWrite, isOwner: false, isCollaborator: false, permissions: canWrite ? ["*"] : ["servers:read"] }}
    >
      <GameConfigSection
        draft={draft}
        template={template}
        storedConfig={initial.spec.config}
        onValidityChange={onValidity}
        onChange={(next) => {
          spy?.(next);
          setDraft(next);
        }}
      />
    </ResourceTargetProvider>
  );
}

describe("GameConfigSection", () => {
  it("renders the header, the restart note and every field with draft values or defaults", () => {
    renderWithQuery(<Harness initial={server({ DIFFICULTY: "Expert", MAX_PLAYERS: "8", PVP: "false", MOTD: "Welcome" })} />);
    expect(screen.getByRole("heading", { name: "Game configuration" })).toBeInTheDocument();
    expect(screen.getByText("Settings from this server's game template. Saving restarts the server.")).toBeInTheDocument();
    expect(screen.getByText("Saving changes restarts the server so they apply.")).toBeInTheDocument();
    expect(screen.getByLabelText(/World name/)).toHaveValue("Gameplane");
    expect(screen.getByLabelText(/Difficulty/)).toHaveValue("Expert");
    expect(screen.getByLabelText(/Max players/)).toHaveValue("8");
    expect(screen.getByLabelText(/PvP/)).toHaveValue("false");
    expect(screen.getByLabelText(/MOTD/)).toHaveValue("Welcome");
    expect(screen.getByText("Written to file")).toBeInTheDocument();
    expect(screen.getByText("Write-only")).toBeInTheDocument();
  });

  it("writes a non-default value and drops the key (and the empty map) when set back to the default", () => {
    const spy = vi.fn();
    renderWithQuery(<Harness initial={server()} spy={spy} />);
    fireEvent.change(screen.getByLabelText(/Max players/), { target: { value: "8" } });
    expect(spy).toHaveBeenLastCalledWith(expect.objectContaining({ spec: expect.objectContaining({ config: { MAX_PLAYERS: "8" } }) }));
    fireEvent.change(screen.getByLabelText(/Max players/), { target: { value: "16" } });
    expect(spy.mock.calls.at(-1)?.[0].spec.config).toBeUndefined();
  });

  it("selecting a non-default enum/bool writes it; selecting the default removes it", () => {
    const spy = vi.fn();
    renderWithQuery(<Harness initial={server()} spy={spy} />);
    fireEvent.change(screen.getByLabelText(/Difficulty/), { target: { value: "Expert" } });
    expect(spy.mock.calls.at(-1)?.[0].spec.config).toEqual({ DIFFICULTY: "Expert" });
    fireEvent.change(screen.getByLabelText(/PvP/), { target: { value: "false" } });
    expect(spy.mock.calls.at(-1)?.[0].spec.config).toEqual({ DIFFICULTY: "Expert", PVP: "false" });
    fireEvent.change(screen.getByLabelText(/Difficulty/), { target: { value: "Classic" } });
    expect(spy.mock.calls.at(-1)?.[0].spec.config).toEqual({ PVP: "false" });
  });

  it("clearing a field that has a default stores an explicit empty value; clearing one without a default unsets it", () => {
    const spy = vi.fn();
    renderWithQuery(<Harness initial={server({ MOTD: "hi" })} spy={spy} />);
    fireEvent.change(screen.getByLabelText(/World name/), { target: { value: "" } });
    expect(spy.mock.calls.at(-1)?.[0].spec.config).toEqual({ MOTD: "hi", WORLD_NAME: "" });
    fireEvent.change(screen.getByLabelText(/MOTD/), { target: { value: "" } });
    expect(spy.mock.calls.at(-1)?.[0].spec.config).toEqual({ WORLD_NAME: "" });
  });

  it("shows the range error, disables validity, and recovers", () => {
    const onValidity = vi.fn();
    renderWithQuery(<Harness initial={server()} onValidity={onValidity} />);
    expect(onValidity).toHaveBeenLastCalledWith(true);
    fireEvent.change(screen.getByLabelText(/Max players/), { target: { value: "900" } });
    expect(screen.getByRole("alert")).toHaveTextContent("Must be between 1 and 255.");
    expect(onValidity).toHaveBeenLastCalledWith(false);
    fireEvent.change(screen.getByLabelText(/Max players/), { target: { value: "20" } });
    expect(screen.queryByRole("alert")).toBeNull();
    expect(onValidity).toHaveBeenLastCalledWith(true);
  });

  it("keeps a stored password marker untouched, never renders it, and replaces it when typed over", () => {
    const spy = vi.fn();
    const { container } = renderWithQuery(<Harness initial={server({ SERVER_PASSWORD: CONFIG_REDACTED_MARKER })} spy={spy} />);
    const input = screen.getByLabelText(/Server password/);
    expect(input).toHaveValue("");
    expect(input).toHaveAttribute("placeholder", "Unchanged — type to replace");
    expect(container.innerHTML).not.toContain(CONFIG_REDACTED_MARKER);
    expect(spy).not.toHaveBeenCalled();
    fireEvent.change(input, { target: { value: "s3cret" } });
    expect(spy.mock.calls.at(-1)?.[0].spec.config).toEqual({ SERVER_PASSWORD: "s3cret" });
    // emptying again returns to "unchanged" (the marker), not to a cleared password
    fireEvent.change(screen.getByLabelText(/Server password/), { target: { value: "" } });
    expect(spy.mock.calls.at(-1)?.[0].spec.config).toEqual({ SERVER_PASSWORD: CONFIG_REDACTED_MARKER });
  });

  it("emptying a password that was never stored removes the key", () => {
    const spy = vi.fn();
    renderWithQuery(<Harness initial={server()} spy={spy} />);
    fireEvent.change(screen.getByLabelText(/Server password/), { target: { value: "a" } });
    expect(spy.mock.calls.at(-1)?.[0].spec.config).toEqual({ SERVER_PASSWORD: "a" });
    fireEvent.change(screen.getByLabelText(/Server password/), { target: { value: "" } });
    expect(spy.mock.calls.at(-1)?.[0].spec.config).toBeUndefined();
  });

  it("lists orphaned keys with a Remove button that deletes only that key", () => {
    const spy = vi.fn();
    renderWithQuery(<Harness initial={server({ OLD_SETTING: "legacy", MOTD: "hi" })} spy={spy} />);
    expect(screen.getByText("OLD_SETTING")).toBeInTheDocument();
    expect(screen.getByDisplayValue("legacy")).toBeInTheDocument();
    expect(screen.getByText("No longer in the template")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Remove" }));
    expect(spy.mock.calls.at(-1)?.[0].spec.config).toEqual({ MOTD: "hi" });
  });

  it("hides the value of an orphaned key that came back as the marker", () => {
    renderWithQuery(<Harness initial={server({ OLD_PASS: CONFIG_REDACTED_MARKER })} />);
    expect(screen.getByPlaceholderText("Hidden")).toHaveValue("");
  });

  it("is read-only without write access: disabled fields and Remove, no restart note", () => {
    renderWithQuery(<Harness canWrite={false} initial={server({ OLD_SETTING: "legacy" })} />);
    expect(screen.getByLabelText(/World name/)).toBeDisabled();
    expect(screen.getByLabelText(/Difficulty/)).toBeDisabled();
    expect(screen.getByRole("button", { name: "Remove" })).toBeDisabled();
    expect(screen.queryByText("Saving changes restarts the server so they apply.")).toBeNull();
  });

  it("surfaces an operator 'invalid config:' status and ignores other Ready messages", () => {
    const bad = server({}, {
      phase: "Failed",
      conditions: [{ type: "Ready", status: "False", message: "invalid config: unknown config keys OLD" }],
    });
    const { unmount } = renderWithQuery(<Harness initial={bad} />);
    expect(screen.getByTestId("config-invalid")).toHaveTextContent("invalid config: unknown config keys OLD");
    expect(screen.getByText("The operator rejected this configuration")).toBeInTheDocument();
    unmount();
    const fine = server({}, { phase: "Running", conditions: [{ type: "Ready", status: "True", message: "all good" }] });
    renderWithQuery(<Harness initial={fine} />);
    expect(screen.queryByTestId("config-invalid")).toBeNull();
  });

  it("says so when the template has no schema and there are no stray keys", () => {
    renderWithQuery(<Harness initial={server()} template={tmpl([])} />);
    expect(screen.getByText("This template has no configurable settings.")).toBeInTheDocument();
  });

  it("copes with a template that has not loaded yet", () => {
    renderWithQuery(
      <ResourceTargetProvider target={{ cluster: "local", name: "mc-survival" }}>
        <GameConfigSection draft={server()} onChange={() => {}} template={undefined} />
      </ResourceTargetProvider>,
    );
    expect(screen.getByText("This template has no configurable settings.")).toBeInTheDocument();
  });
});

describe("GameConfigSection redaction marker for any field type", () => {
  it("masks a marker on every field type, keeps it in the draft, and stays valid", () => {
    const spy = vi.fn();
    const onValidity = vi.fn();
    const { container } = renderWithQuery(
      <Harness
        initial={server({ WORLD_NAME: CONFIG_REDACTED_MARKER, DIFFICULTY: CONFIG_REDACTED_MARKER, MAX_PLAYERS: CONFIG_REDACTED_MARKER, PVP: CONFIG_REDACTED_MARKER, MOTD: CONFIG_REDACTED_MARKER })}
        spy={spy}
        onValidity={onValidity}
      />,
    );
    expect(container.innerHTML).not.toContain(CONFIG_REDACTED_MARKER);
    expect(screen.getByLabelText(/World name/)).toHaveValue("");
    expect(screen.getByLabelText(/Max players/)).toHaveValue("");
    expect(screen.getByLabelText(/MOTD/)).toHaveValue("");
    expect(screen.getByLabelText(/Difficulty/)).toHaveValue("");
    expect(screen.getByLabelText(/PvP/)).toHaveValue("");
    expect(screen.queryByRole("alert")).toBeNull();
    expect(onValidity).toHaveBeenLastCalledWith(true);
    expect(spy).not.toHaveBeenCalled();
  });

  it("emptying a marker-backed non-password field restores the marker; typing replaces it", () => {
    const spy = vi.fn();
    renderWithQuery(<Harness initial={server({ MOTD: CONFIG_REDACTED_MARKER, WORLD_NAME: CONFIG_REDACTED_MARKER })} spy={spy} />);
    fireEvent.change(screen.getByLabelText(/MOTD/), { target: { value: "hello" } });
    expect(spy.mock.calls.at(-1)?.[0].spec.config).toEqual({ MOTD: "hello", WORLD_NAME: CONFIG_REDACTED_MARKER });
    fireEvent.change(screen.getByLabelText(/MOTD/), { target: { value: "" } });
    expect(spy.mock.calls.at(-1)?.[0].spec.config).toEqual({ MOTD: CONFIG_REDACTED_MARKER, WORLD_NAME: CONFIG_REDACTED_MARKER });
    // a field with a default is also restored to the marker, not to an explicit empty value
    fireEvent.change(screen.getByLabelText(/World name/), { target: { value: "w" } });
    fireEvent.change(screen.getByLabelText(/World name/), { target: { value: "" } });
    expect(spy.mock.calls.at(-1)?.[0].spec.config).toEqual({ MOTD: CONFIG_REDACTED_MARKER, WORLD_NAME: CONFIG_REDACTED_MARKER });
  });

  it("choosing a real option on a marker-backed select replaces the marker", () => {
    const spy = vi.fn();
    renderWithQuery(<Harness initial={server({ DIFFICULTY: CONFIG_REDACTED_MARKER, PVP: CONFIG_REDACTED_MARKER })} spy={spy} />);
    fireEvent.change(screen.getByLabelText(/Difficulty/), { target: { value: "Expert" } });
    expect(spy.mock.calls.at(-1)?.[0].spec.config).toEqual({ DIFFICULTY: "Expert", PVP: CONFIG_REDACTED_MARKER });
    fireEvent.change(screen.getByLabelText(/PvP/), { target: { value: "false" } });
    expect(spy.mock.calls.at(-1)?.[0].spec.config).toEqual({ DIFFICULTY: "Expert", PVP: "false" });
  });
});

describe("GameConfigSection required bool and enum without a default", () => {
  const REQUIRED: Schema = [
    { name: "FLAG", displayName: "Flag", type: "bool", required: true },
    { name: "MODE", displayName: "Mode", type: "enum", enum: ["a", "b"], required: true },
  ];

  it("shows the empty option, keeps Save disabled until real values are chosen, and choosing false sets the draft", () => {
    const spy = vi.fn();
    const onValidity = vi.fn();
    renderWithQuery(<Harness initial={server()} template={tmpl(REQUIRED)} spy={spy} onValidity={onValidity} />);
    expect(screen.getByLabelText(/Flag/)).toHaveValue("");
    expect(screen.getByLabelText(/Mode/)).toHaveValue("");
    expect(screen.getAllByRole("option", { name: "Select…" })).toHaveLength(2);
    expect(onValidity).toHaveBeenLastCalledWith(false);
    fireEvent.change(screen.getByLabelText(/Flag/), { target: { value: "false" } });
    expect(spy.mock.calls.at(-1)?.[0].spec.config).toEqual({ FLAG: "false" });
    expect(screen.getByLabelText(/Flag/)).toHaveValue("false");
    expect(onValidity).toHaveBeenLastCalledWith(false);
    fireEvent.change(screen.getByLabelText(/Mode/), { target: { value: "a" } });
    expect(spy.mock.calls.at(-1)?.[0].spec.config).toEqual({ FLAG: "false", MODE: "a" });
    expect(onValidity).toHaveBeenLastCalledWith(true);
  });
});
