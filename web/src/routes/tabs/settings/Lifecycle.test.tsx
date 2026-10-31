import { describe, it, expect, vi } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { LifecycleSection } from "./Lifecycle";
import { GRACE_PERIOD_ANNOTATION } from "./types";
import { makeServer } from "@/test/factories";
import type { GameTemplate, IdleSpec } from "@/types";

const baseDraft = makeServer();

const templateWithProbe = {
  metadata: { name: "minecraft" },
  spec: {
    displayName: "MC",
    game: "minecraft",
    version: "1",
    image: "img",
    probes: {
      liveness: { initialDelaySeconds: 5, httpGet: { path: "/health", port: 8080 } },
    },
  },
} as GameTemplate;

describe("LifecycleSection", () => {
  it("Auto-restart switch toggles suspend", async () => {
    const onChange = vi.fn();
    render(
      <LifecycleSection draft={baseDraft} onChange={onChange} />,
    );
    const sw = screen.getByRole("switch", { name: "Auto-restart" });
    await userEvent.click(sw);
    expect(onChange).toHaveBeenCalledWith(
      expect.objectContaining({ spec: expect.objectContaining({ suspend: true }) }),
    );
  });

  it("renders the suspended description when suspend=true", () => {
    render(
      <LifecycleSection
        draft={{ ...baseDraft, spec: { ...baseDraft.spec, suspend: true } }}
        onChange={() => {}}
      />,
    );
    expect(screen.getByText(/Pod is suspended/i)).toBeInTheDocument();
  });

  it("setGrace writes spec.stopGracePeriodSeconds as a number", () => {
    const onChange = vi.fn();
    render(<LifecycleSection draft={baseDraft} onChange={onChange} />);
    // Controlled input — fire a single change with the full string
    // rather than typing char-by-char (the parent never re-renders
    // between keystrokes, so userEvent.type would lose interim chars).
    const grace = screen.getAllByRole("textbox")[0];
    fireEvent.change(grace, { target: { value: "30" } });
    const lastCall = onChange.mock.calls.at(-1)![0];
    expect(lastCall.spec.stopGracePeriodSeconds).toBe(30);
  });

  it("removing the value clears spec.stopGracePeriodSeconds", () => {
    const draft = {
      ...baseDraft,
      spec: { ...baseDraft.spec, stopGracePeriodSeconds: 10 },
    };
    const onChange = vi.fn();
    render(<LifecycleSection draft={draft} onChange={onChange} />);
    const grace = screen.getAllByRole("textbox")[0];
    fireEvent.change(grace, { target: { value: "" } });
    const lastCall = onChange.mock.calls.at(-1)![0];
    expect(lastCall.spec.stopGracePeriodSeconds).toBeUndefined();
  });

  it("reads the legacy annotation as a fallback and migrates it on edit", () => {
    const draft = {
      ...baseDraft,
      metadata: {
        ...baseDraft.metadata,
        annotations: { [GRACE_PERIOD_ANNOTATION]: "45" },
      },
    };
    const onChange = vi.fn();
    render(<LifecycleSection draft={draft} onChange={onChange} />);
    const grace = screen.getAllByRole("textbox")[0] as HTMLInputElement;
    // The legacy value is shown in the field...
    expect(grace.value).toBe("45");
    // ...and the first edit writes the real spec field and drops the
    // dead annotation.
    fireEvent.change(grace, { target: { value: "60" } });
    const last = onChange.mock.calls.at(-1)![0];
    expect(last.spec.stopGracePeriodSeconds).toBe(60);
    expect(last.metadata.annotations).toBeUndefined();
  });

  it("flags an out-of-range grace period", () => {
    render(
      <LifecycleSection
        draft={{ ...baseDraft, spec: { ...baseDraft.spec, stopGracePeriodSeconds: 900 } }}
        onChange={() => {}}
      />,
    );
    expect(screen.getByText(/between 0 and 600/i)).toBeInTheDocument();
  });

  it("editing a probe field seeds an override from the template", () => {
    const onChange = vi.fn();
    render(
      <LifecycleSection draft={baseDraft} onChange={onChange} template={templateWithProbe} />,
    );
    fireEvent.change(screen.getByLabelText("liveness Initial delay"), {
      target: { value: "20" },
    });
    const last = onChange.mock.calls.at(-1)![0];
    expect(last.spec.probes.liveness.initialDelaySeconds).toBe(20);
    // The action is inherited from the template probe.
    expect(last.spec.probes.liveness.httpGet).toEqual({ path: "/health", port: 8080 });
  });

  it("shows 'not defined' for probes the template omits", () => {
    render(
      <LifecycleSection draft={baseDraft} onChange={() => {}} template={templateWithProbe} />,
    );
    // readiness + startup are absent from the template → two notices.
    expect(screen.getAllByText(/not\s+defined by the template/i)).toHaveLength(2);
  });

  it("enables idle auto-sleep via switch", async () => {
    const onChange = vi.fn();
    render(
      <LifecycleSection draft={baseDraft} onChange={onChange} />,
    );
    const sw = screen.getByRole("switch", { name: "Enable idle auto-sleep" });
    await userEvent.click(sw);
    const lastCall = onChange.mock.calls.at(-1)![0];
    expect(lastCall.spec.idle?.enabled).toBe(true);
    expect(lastCall.spec.idle?.afterMinutes).toBe(30);
    expect(lastCall.spec.idle?.wakeWindows).toEqual([]);
  });

  it("disabling idle auto-sleep keeps the configuration and only flips enabled off", async () => {
    // Regression: this used to delete spec.idle entirely on disable, losing
    // the user's afterMinutes/wakeWindows with no undo (re-enabling started
    // from scratch). `enabled: false` is exactly what the operator reads as
    // disabled, so the config must survive the round trip.
    const onChange = vi.fn();
    const draftWithIdle = {
      ...baseDraft,
      spec: {
        ...baseDraft.spec,
        idle: { enabled: true, afterMinutes: 60, wakeWindows: ["0 9 * * 1-5"] },
      },
    };
    render(
      <LifecycleSection draft={draftWithIdle} onChange={onChange} />,
    );
    const sw = screen.getByRole("switch", { name: "Enable idle auto-sleep" });
    await userEvent.click(sw);
    const lastCall = onChange.mock.calls.at(-1)![0];
    expect(lastCall.spec.idle?.enabled).toBe(false);
    expect(lastCall.spec.idle?.afterMinutes).toBe(60);
    expect(lastCall.spec.idle?.wakeWindows).toEqual(["0 9 * * 1-5"]);
  });

  it("re-enabling idle auto-sleep restores the prior configuration instead of resetting it", async () => {
    const onChange = vi.fn();
    const draftDisabled = {
      ...baseDraft,
      spec: {
        ...baseDraft.spec,
        idle: { enabled: false, afterMinutes: 90, wakeWindows: ["0 8 * * *"] },
      },
    };
    render(
      <LifecycleSection draft={draftDisabled} onChange={onChange} />,
    );
    const sw = screen.getByRole("switch", { name: "Enable idle auto-sleep" });
    await userEvent.click(sw);
    const lastCall = onChange.mock.calls.at(-1)![0];
    expect(lastCall.spec.idle?.enabled).toBe(true);
    expect(lastCall.spec.idle?.afterMinutes).toBe(90);
    expect(lastCall.spec.idle?.wakeWindows).toEqual(["0 8 * * *"]);
  });

  it("changes sleep-after minutes", () => {
    const onChange = vi.fn();
    const draftWithIdle = {
      ...baseDraft,
      spec: { ...baseDraft.spec, idle: { enabled: true, afterMinutes: 30, wakeWindows: [] } },
    };
    render(
      <LifecycleSection draft={draftWithIdle} onChange={onChange} />,
    );
    const sleepAfter = screen.getByLabelText("Idle sleep after");
    fireEvent.change(sleepAfter, { target: { value: "60" } });
    const lastCall = onChange.mock.calls.at(-1)![0];
    expect(lastCall.spec.idle?.afterMinutes).toBe(60);
  });

  it("clearing sleep-after minutes leaves the field empty instead of snapping back to the default", () => {
    // Regression: value={String(afterMinutes ?? 30)} fought the field being
    // cleared — the moment afterMinutes went undefined the display fell back
    // to "30", so a user could never actually empty the box.
    const onChange = vi.fn();
    const draftWithIdle = {
      ...baseDraft,
      spec: { ...baseDraft.spec, idle: { enabled: true, afterMinutes: 30, wakeWindows: [] } },
    };
    const { rerender } = render(
      <LifecycleSection draft={draftWithIdle} onChange={onChange} />,
    );
    const sleepAfter = screen.getByLabelText("Idle sleep after") as HTMLInputElement;
    fireEvent.change(sleepAfter, { target: { value: "" } });
    const lastCall = onChange.mock.calls.at(-1)![0];
    expect(lastCall.spec.idle?.afterMinutes).toBeUndefined();

    rerender(<LifecycleSection draft={lastCall} onChange={onChange} />);
    expect((screen.getByLabelText("Idle sleep after") as HTMLInputElement).value).toBe("");
  });

  it("flags an out-of-range idle afterMinutes", () => {
    const draftWithIdle = {
      ...baseDraft,
      spec: { ...baseDraft.spec, idle: { enabled: true, afterMinutes: 2, wakeWindows: [] } },
    };
    render(
      <LifecycleSection draft={draftWithIdle} onChange={() => {}} />,
    );
    expect(screen.getByText(/between 5 and 1440/i)).toBeInTheDocument();
  });

  it("adds a wake window", async () => {
    const onChange = vi.fn();
    const draftWithIdle = {
      ...baseDraft,
      spec: { ...baseDraft.spec, idle: { enabled: true, afterMinutes: 30, wakeWindows: [] } },
    };
    render(
      <LifecycleSection draft={draftWithIdle} onChange={onChange} />,
    );
    const addBtn = screen.getByRole("button", { name: /Add wake window/i });
    await userEvent.click(addBtn);
    const lastCall = onChange.mock.calls.at(-1)![0];
    expect(lastCall.spec.idle?.wakeWindows).toHaveLength(1);
  });

  it("disables add-window button at 8 entries", () => {
    const windows = Array.from({ length: 8 }, (_, i) => `0 ${9 + i} * * *`);
    const draftWithIdle = {
      ...baseDraft,
      spec: { ...baseDraft.spec, idle: { enabled: true, afterMinutes: 30, wakeWindows: windows } },
    };
    render(
      <LifecycleSection draft={draftWithIdle} onChange={() => {}} />,
    );
    const addBtn = screen.getByRole("button", { name: /Add wake window/i });
    expect(addBtn).toBeDisabled();
  });

  it("removes a wake window", async () => {
    const onChange = vi.fn();
    const draftWithIdle = {
      ...baseDraft,
      spec: { ...baseDraft.spec, idle: { enabled: true, afterMinutes: 30, wakeWindows: ["0 9 * * *"] } },
    };
    render(
      <LifecycleSection draft={draftWithIdle} onChange={onChange} />,
    );
    const removeBtn = screen.getByRole("button", { name: /Remove/i });
    await userEvent.click(removeBtn);
    const lastCall = onChange.mock.calls.at(-1)![0];
    expect(lastCall.spec.idle?.wakeWindows).toHaveLength(0);
  });

  it("edits a wake window cron", () => {
    const onChange = vi.fn();
    const draftWithIdle = {
      ...baseDraft,
      spec: { ...baseDraft.spec, idle: { enabled: true, afterMinutes: 30, wakeWindows: ["0 9 * * *"] } },
    };
    render(
      <LifecycleSection draft={draftWithIdle} onChange={onChange} />,
    );
    const cronInput = screen.getByLabelText("Wake window 1") as HTMLInputElement;
    fireEvent.change(cronInput, { target: { value: "0 12 * * *" } });
    const lastCall = onChange.mock.calls.at(-1)![0];
    expect(lastCall.spec.idle?.wakeWindows[0]).toBe("0 12 * * *");
  });

  it("flags a blank wake window inline instead of silently dropping it", () => {
    // A freshly-added row is "" — MinLength=9 at the apiserver, so it must be
    // visibly invalid rather than filtered out (filtering would delete the
    // row a user is mid-typing).
    const draftWithIdle = {
      ...baseDraft,
      spec: { ...baseDraft.spec, idle: { enabled: true, afterMinutes: 30, wakeWindows: [""] } },
    };
    render(
      <LifecycleSection draft={draftWithIdle} onChange={() => {}} />,
    );
    expect(screen.getByText(/five-field cron expression/i)).toBeInTheDocument();
  });

  it("clears the invalid flag once a wake window becomes well-formed", () => {
    const draftWithIdle = {
      ...baseDraft,
      spec: { ...baseDraft.spec, idle: { enabled: true, afterMinutes: 30, wakeWindows: ["0 9 * * 1-5"] } },
    };
    render(
      <LifecycleSection draft={draftWithIdle} onChange={() => {}} />,
    );
    expect(screen.queryByText(/five-field cron expression/i)).not.toBeInTheDocument();
  });

  // Both caveats describe behaviour a user cannot discover from the controls:
  // that a game with no player source never triggers, and that a wake window
  // will not undo their own Stop.
  it("states the two idle caveats once enabled", () => {
    const draftWithIdle = {
      ...baseDraft,
      spec: { ...baseDraft.spec, idle: { enabled: true, afterMinutes: 30, wakeWindows: [] } },
    };
    render(<LifecycleSection draft={draftWithIdle} onChange={() => {}} />);
    expect(
      screen.getByText("A game that reports no player count will never sleep."),
    ).toBeInTheDocument();
    expect(
      screen.getByText("A wake window never restarts a server you stopped by hand."),
    ).toBeInTheDocument();
  });

  it("resetProbe early-returns when no override exists", () => {
    const onChange = vi.fn();
    render(
      <LifecycleSection draft={baseDraft} onChange={onChange} template={templateWithProbe} />,
    );
    // Since baseDraft has no probes, resetProbe(liveness) should be a no-op
    // We can't directly call it, but we can verify that the "Reset to template" button exists
    // and clicking it does nothing if there's no override.
    expect(screen.queryByText(/Reset to template/i)).not.toBeInTheDocument();
  });

  it("renders probe override button and can reset to template", () => {
    const onChange = vi.fn();
    const draftWithOverride = {
      ...baseDraft,
      spec: {
        ...baseDraft.spec,
        probes: {
          liveness: { initialDelaySeconds: 20, httpGet: { path: "/health", port: 8080 } },
        },
      },
    };
    render(
      <LifecycleSection draft={draftWithOverride} onChange={onChange} template={templateWithProbe} />,
    );
    const resetBtn = screen.getByRole("button", { name: /Reset to template/i });
    fireEvent.click(resetBtn);
    const last = onChange.mock.calls.at(-1)![0];
    // After reset, the override should be removed (probes should be undefined)
    expect(last.spec.probes).toBeUndefined();
  });

  it("renders probe with no template and no override shows 'not defined'", () => {
    render(
      <LifecycleSection
        draft={baseDraft}
        onChange={() => {}}
        template={{
          metadata: { name: "minecraft" },
          spec: {
            displayName: "MC",
            game: "minecraft",
            version: "1",
            image: "img",
            // No probes
          },
        } as GameTemplate}
      />,
    );
    expect(screen.getAllByText(/not\s+defined by the template/i)).toHaveLength(3);
  });

  it("flags invalid grace period (non-integer)", () => {
    render(
      <LifecycleSection
        draft={{ ...baseDraft, spec: { ...baseDraft.spec, stopGracePeriodSeconds: 30.5 } }}
        onChange={() => {}}
      />,
    );
    expect(screen.getByText(/between 0 and 600/i)).toBeInTheDocument();
  });

  it("flags invalid grace period (negative)", () => {
    render(
      <LifecycleSection
        draft={{ ...baseDraft, spec: { ...baseDraft.spec, stopGracePeriodSeconds: -1 } }}
        onChange={() => {}}
      />,
    );
    expect(screen.getByText(/between 0 and 600/i)).toBeInTheDocument();
  });

  it("accepts edge case grace period values (0 and 600)", () => {
    const { rerender } = render(
      <LifecycleSection
        draft={{ ...baseDraft, spec: { ...baseDraft.spec, stopGracePeriodSeconds: 0 } }}
        onChange={() => {}}
      />,
    );
    expect(screen.queryByText(/between 0 and 600/i)).not.toBeInTheDocument();

    rerender(
      <LifecycleSection
        draft={{ ...baseDraft, spec: { ...baseDraft.spec, stopGracePeriodSeconds: 600 } }}
        onChange={() => {}}
      />,
    );
    expect(screen.queryByText(/between 0 and 600/i)).not.toBeInTheDocument();
  });

  it("can add multiple wake windows up to the limit", async () => {
    const onChange = vi.fn();
    const draftWithIdle = {
      ...baseDraft,
      spec: { ...baseDraft.spec, idle: { enabled: true, afterMinutes: 30, wakeWindows: [] } },
    };
    const { rerender } = render(
      <LifecycleSection draft={draftWithIdle} onChange={onChange} />,
    );

    // Add 8 windows (max is 8)
    for (let i = 0; i < 8; i++) {
      const addBtn = screen.getByRole("button", { name: /Add wake window/i });
      expect(addBtn).not.toBeDisabled();
      await userEvent.click(addBtn);
      const lastCall = onChange.mock.calls.at(-1)![0];
      rerender(
        <LifecycleSection draft={lastCall} onChange={onChange} />,
      );
    }

    // Now we have 8, add button should be disabled
    expect(screen.getByRole("button", { name: /Add wake window/i })).toBeDisabled();
  });

  it("probe override preserves the template's action", () => {
    const onChange = vi.fn();
    render(
      <LifecycleSection draft={baseDraft} onChange={onChange} template={templateWithProbe} />,
    );
    fireEvent.change(screen.getByLabelText("liveness Initial delay"), {
      target: { value: "15" },
    });
    const last = onChange.mock.calls.at(-1)![0];
    // The override should include both the changed field and the inherited action
    expect(last.spec.probes.liveness.initialDelaySeconds).toBe(15);
    expect(last.spec.probes.liveness.httpGet).toEqual({ path: "/health", port: 8080 });
  });

  it("clearing a probe field from override value removes it", () => {
    const onChange = vi.fn();
    const draftWithOverride = {
      ...baseDraft,
      spec: {
        ...baseDraft.spec,
        probes: {
          liveness: { initialDelaySeconds: 20, httpGet: { path: "/health", port: 8080 } },
        },
      },
    };
    render(
      <LifecycleSection draft={draftWithOverride} onChange={onChange} template={templateWithProbe} />,
    );
    fireEvent.change(screen.getByLabelText("liveness Initial delay"), {
      target: { value: "" },
    });
    const last = onChange.mock.calls.at(-1)![0];
    // The field should be removed but action preserved
    expect(last.spec.probes.liveness.initialDelaySeconds).toBeUndefined();
    expect(last.spec.probes.liveness.httpGet).toEqual({ path: "/health", port: 8080 });
  });

  it("handles wake window with exactly 9 characters (minimum)", () => {
    const draftWithIdle = {
      ...baseDraft,
      spec: { ...baseDraft.spec, idle: { enabled: true, afterMinutes: 30, wakeWindows: ["0 9 * * *"] } },
    };
    render(
      <LifecycleSection draft={draftWithIdle} onChange={() => {}} />,
    );
    expect(screen.queryByText(/five-field cron expression/i)).not.toBeInTheDocument();
  });

  it("handles wake window with six fields (seconds)", () => {
    const draftWithIdle = {
      ...baseDraft,
      spec: { ...baseDraft.spec, idle: { enabled: true, afterMinutes: 30, wakeWindows: ["0 0 9 * * *"] } },
    };
    render(
      <LifecycleSection draft={draftWithIdle} onChange={() => {}} />,
    );
    expect(screen.queryByText(/five-field cron expression/i)).not.toBeInTheDocument();
  });

  it("edits sleep-after and clears invalid flag", () => {
    const onChange = vi.fn();
    const draftWithIdle = {
      ...baseDraft,
      spec: { ...baseDraft.spec, idle: { enabled: true, afterMinutes: 2, wakeWindows: [] } },
    };
    const { rerender } = render(
      <LifecycleSection draft={draftWithIdle} onChange={onChange} />,
    );
    expect(screen.getByText(/between 5 and 1440/i)).toBeInTheDocument();

    const sleepAfter = screen.getByLabelText("Idle sleep after");
    fireEvent.change(sleepAfter, { target: { value: "60" } });

    const lastCall = onChange.mock.calls.at(-1)![0];
    rerender(
      <LifecycleSection draft={lastCall} onChange={onChange} />,
    );
    expect(screen.queryByText(/between 5 and 1440/i)).not.toBeInTheDocument();
  });
  describe("wake on connect", () => {
    const idleOn = { enabled: true, afterMinutes: 45, wakeWindows: ["0 9 * * 1-5"] };
    const withIdle = (idle: IdleSpec) => ({
      ...baseDraft,
      spec: { ...baseDraft.spec, idle },
    });
    const templateWithPorts = (
      ports: NonNullable<GameTemplate["spec"]["ports"]>,
    ): GameTemplate => ({ ...templateWithProbe, spec: { ...templateWithProbe.spec, ports } });
    const switchName = "Wake the server when a player connects";

    it("arms the wake sentinel and keeps the other idle settings", async () => {
      const onChange = vi.fn();
      render(<LifecycleSection draft={withIdle(idleOn)} onChange={onChange} />);
      await userEvent.click(screen.getByRole("switch", { name: switchName }));
      const lastCall = onChange.mock.calls.at(-1)![0];
      expect(lastCall.spec.idle).toEqual({ ...idleOn, wakeOnConnect: true });
    });

    it("disarms it again", async () => {
      const onChange = vi.fn();
      render(
        <LifecycleSection draft={withIdle({ ...idleOn, wakeOnConnect: true })} onChange={onChange} />,
      );
      const sw = screen.getByRole("switch", { name: switchName });
      expect(sw).toBeChecked();
      await userEvent.click(sw);
      const lastCall = onChange.mock.calls.at(-1)![0];
      expect(lastCall.spec.idle?.wakeOnConnect).toBe(false);
    });

    it("survives switching idle auto-sleep off and on again", async () => {
      // Regression: re-enabling rebuilt spec.idle from afterMinutes and
      // wakeWindows only, silently dropping wakeOnConnect.
      const onChange = vi.fn();
      render(
        <LifecycleSection
          draft={withIdle({ ...idleOn, enabled: false, wakeOnConnect: true })}
          onChange={onChange}
        />,
      );
      await userEvent.click(screen.getByRole("switch", { name: "Enable idle auto-sleep" }));
      const lastCall = onChange.mock.calls.at(-1)![0];
      expect(lastCall.spec.idle?.enabled).toBe(true);
      expect(lastCall.spec.idle?.wakeOnConnect).toBe(true);
    });

    it("explains the extra pod and the UDP retry", () => {
      render(<LifecycleSection draft={withIdle(idleOn)} onChange={() => {}} />);
      expect(screen.getByText(/a small pod holds its ports/)).toBeInTheDocument();
      expect(screen.getByText(/the player joins on retry/)).toBeInTheDocument();
    });

    it("is available when the template has an advertised port that can wake the server", () => {
      render(
        <LifecycleSection
          draft={withIdle(idleOn)}
          onChange={() => {}}
          template={templateWithPorts([
            { name: "query", containerPort: 27015, protocol: "UDP", advertise: false, wakeProtocol: "none" },
            // No wakeProtocol: the CRD defaults it to "generic".
            { name: "game", containerPort: 8211, protocol: "UDP", advertise: true },
          ])}
        />,
      );
      expect(screen.getByRole("switch", { name: switchName })).not.toBeDisabled();
      expect(screen.queryByText(/Not available for this game/)).not.toBeInTheDocument();
    });

    it("is disabled when no advertised port can wake the server", () => {
      render(
        <LifecycleSection
          draft={withIdle(idleOn)}
          onChange={() => {}}
          template={templateWithPorts([
            { name: "game", containerPort: 7777, protocol: "UDP", advertise: true, wakeProtocol: "none" },
            { name: "rcon", containerPort: 25575, protocol: "TCP", advertise: false, wakeProtocol: "generic" },
          ])}
        />,
      );
      expect(screen.getByRole("switch", { name: switchName })).toBeDisabled();
      expect(
        screen.getByText("Not available for this game: its template marks every port as unable to wake the server."),
      ).toBeInTheDocument();
    });

    it("is disabled when the loaded template declares no ports", () => {
      // The operator finds no port to wake on, so it never arms the sentinel.
      render(<LifecycleSection draft={withIdle(idleOn)} onChange={() => {}} template={templateWithProbe} />);
      expect(screen.getByRole("switch", { name: switchName })).toBeDisabled();
      expect(screen.getByText(/Not available for this game/)).toBeInTheDocument();
    });

    it("still lets an already armed server be disarmed when the template cannot wake it", async () => {
      const onChange = vi.fn();
      render(
        <LifecycleSection
          draft={withIdle({ ...idleOn, wakeOnConnect: true })}
          onChange={onChange}
          template={templateWithPorts([
            { name: "game", containerPort: 7777, protocol: "UDP", advertise: true, wakeProtocol: "none" },
          ])}
        />,
      );
      const sw = screen.getByRole("switch", { name: switchName });
      expect(sw).not.toBeDisabled();
      await userEvent.click(sw);
      expect(onChange.mock.calls.at(-1)![0].spec.idle?.wakeOnConnect).toBe(false);
    });
  });
});
