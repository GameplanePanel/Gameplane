import { describe, it, expect, vi } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import {
  SecurityAdvanced,
  isValidSecurityId,
  securityIsValid,
  securityOverrideCount,
  type SecurityAdvancedProps,
} from "./SecurityAdvanced";
import { makeTemplate } from "@/test/factories";
import type { GameServerSecurityStatus, SecuritySource } from "@/types";

function renderAdvanced(props: Partial<SecurityAdvancedProps> = {}) {
  const onChange = vi.fn();
  const onValidityChange = vi.fn();
  const view = render(
    <SecurityAdvanced value={undefined} onChange={onChange} onValidityChange={onValidityChange} {...props} />,
  );
  return { ...view, onChange, onValidityChange };
}

// Opens a HeroUI Select (found by a regex on its accessible name) and picks an option.
async function pick(trigger: RegExp, option: string) {
  await userEvent.click(screen.getByRole("button", { name: trigger }));
  await userEvent.click(await screen.findByRole("option", { name: option }));
}

function numberBox(label: string) {
  return screen.getByRole("spinbutton", { name: `${label} value` });
}

describe("securityOverrideCount", () => {
  it("is zero without a value or when no key is set", () => {
    expect(securityOverrideCount(undefined)).toBe(0);
    expect(securityOverrideCount({})).toBe(0);
  });

  it("counts every setting that has a key, including image-default overrides", () => {
    expect(
      securityOverrideCount({
        runAsUser: { value: 0 },
        fsGroup: { useImageDefault: true },
        volumeOwnershipCheck: "EveryStart",
      }),
    ).toBe(3);
  });
});

describe("isValidSecurityId", () => {
  it.each([0, 1000, 4294967295])("accepts %s", (v) => {
    expect(isValidSecurityId(v)).toBe(true);
  });

  it.each([-1, 1.5, 4294967296, Number.NaN, Number.POSITIVE_INFINITY])("rejects %s", (v) => {
    expect(isValidSecurityId(v)).toBe(false);
  });
});

describe("securityIsValid", () => {
  it("accepts an absent value and valid overrides", () => {
    expect(securityIsValid(undefined)).toBe(true);
    expect(securityIsValid({})).toBe(true);
    expect(
      securityIsValid({
        runAsUser: { value: 1000 },
        runAsGroup: { useImageDefault: true },
        fsGroup: { value: 0 },
        volumeOwnershipCheck: "OnlyWhenWrong",
      }),
    ).toBe(true);
  });

  it("rejects an out-of-range ID", () => {
    expect(securityIsValid({ runAsUser: { value: 4294967296 } })).toBe(false);
  });

  it("rejects an empty ID override that has neither a value nor the image default", () => {
    expect(securityIsValid({ fsGroup: {} })).toBe(false);
  });

  it("rejects the image default combined with a value", () => {
    expect(securityIsValid({ runAsGroup: { useImageDefault: true, value: 3 } })).toBe(false);
  });
});

describe("SecurityAdvanced", () => {
  describe("collapsed and open states", () => {
    it("starts collapsed and shows only the header and description", () => {
      renderAdvanced();
      const header = screen.getByRole("button", { name: /Advanced security/ });
      expect(header).toHaveAttribute("aria-expanded", "false");
      expect(screen.getByText(/Who the game runs as/)).toBeInTheDocument();
      expect(screen.queryByText("Run-as user")).not.toBeInTheDocument();
    }); 

    it("expands and collapses from the header", async () => {
      renderAdvanced();
      const header = screen.getByRole("button", { name: /Advanced security/ });
      await userEvent.click(header);
      expect(header).toHaveAttribute("aria-expanded", "true");
      expect(screen.getByText("Run-as user")).toBeInTheDocument();
      await userEvent.click(header);
      expect(header).toHaveAttribute("aria-expanded", "false");
      expect(screen.queryByText("Run-as user")).not.toBeInTheDocument();
    });

    it("starts open when defaultOpen is set", () => {
      renderAdvanced({ defaultOpen: true });
      expect(screen.getByRole("button", { name: /Advanced security/ })).toHaveAttribute("aria-expanded", "true");
      expect(screen.getByText("Run-as user")).toBeInTheDocument();
      expect(screen.getByText("Run-as group")).toBeInTheDocument();
      expect(screen.getByText("Filesystem group")).toBeInTheDocument();
      expect(screen.getByText("Volume ownership check")).toBeInTheDocument();
    });

    it("hides held-back and restart notices while collapsed", () => {
      renderAdvanced({ heldBack: { running: false }, status: { pending: true } });
      expect(screen.queryByText("Will not start: root override not allowed")).not.toBeInTheDocument();
      expect(screen.queryByTestId("security-restart-note")).not.toBeInTheDocument();
    });
  });

  describe("override count chip", () => {
    it("shows the plural count of overrides", () => {
      renderAdvanced({
        defaultOpen: true,
        value: { runAsUser: { value: 1000 }, volumeOwnershipCheck: "EveryStart" },
      });
      expect(screen.getByText("2 overrides")).toBeInTheDocument();
    });

    it("uses the singular for one override", () => {
      renderAdvanced({ value: { fsGroup: { value: 33 } } });
      expect(screen.getByText("1 override")).toBeInTheDocument();
    });

    it("shows no chip when nothing is overridden", () => {
      renderAdvanced();
      expect(screen.queryByText(/\d+ overrides?$/)).not.toBeInTheDocument();
    });
  });

  describe("read-only", () => {
    it("shows values as text with no controls and the permission note", () => {
      renderAdvanced({
        defaultOpen: true,
        readOnly: true,
        value: {
          runAsUser: { value: 1000 },
          runAsGroup: { useImageDefault: true },
          volumeOwnershipCheck: "OnlyWhenWrong",
        },
      });
      expect(screen.getByText("Read-only")).toBeInTheDocument();
      expect(
        screen.getByText("You can see these settings. Changing them needs the servers:security permission."),
      ).toBeInTheDocument();
      expect(screen.getByText("1000")).toBeInTheDocument();
      expect(screen.getByText("Use image default")).toBeInTheDocument();
      expect(screen.getByText("Only when wrong")).toBeInTheDocument();
      expect(screen.getByText("Module default")).toBeInTheDocument();
      expect(screen.queryByRole("spinbutton")).not.toBeInTheDocument();
      expect(screen.queryByRole("button", { name: /Run-as user/ })).not.toBeInTheDocument();
    });

    it("shows the read-only chip in the header", () => {
      renderAdvanced({ readOnly: true });
      expect(screen.getByText("Read-only")).toBeInTheDocument();
    });
  });

  describe("held-back notices", () => {
    it("shows the stopped notice with the default body", () => {
      renderAdvanced({ defaultOpen: true, heldBack: { running: false } });
      expect(screen.getByText("Will not start: root override not allowed")).toBeInTheDocument();
      expect(screen.getByText(/A root override is set, and root overrides are off on this cluster\. Clear the override/)).toBeInTheDocument();
    });

    it("prefers the server-provided message for a stopped server", () => {
      renderAdvanced({ defaultOpen: true, heldBack: { running: false, message: "Server says no." } });
      expect(screen.getByText("Server says no.")).toBeInTheDocument();
      expect(screen.queryByText(/Clear the override/)).not.toBeInTheDocument();
    });

    it("shows the running notice with the default body", () => {
      renderAdvanced({ defaultOpen: true, heldBack: { running: true } });
      expect(screen.getByText("Keeps running, but will not start again")).toBeInTheDocument();
      expect(screen.getByText(/The server keeps running\. It will not start again/)).toBeInTheDocument();
    });

    it("prefers the server-provided message for a running server", () => {
      renderAdvanced({ defaultOpen: true, heldBack: { running: true, message: "Still running." } });
      expect(screen.getByText("Keeps running, but will not start again")).toBeInTheDocument();
      expect(screen.getByText("Still running.")).toBeInTheDocument();
      expect(screen.queryByText(/The server keeps running/)).not.toBeInTheDocument();
    });

    it("shows no held-back notice when heldBack is absent", () => {
      renderAdvanced({ defaultOpen: true });
      expect(screen.queryByText("Will not start: root override not allowed")).not.toBeInTheDocument();
      expect(screen.queryByText("Keeps running, but will not start again")).not.toBeInTheDocument();
    });
  });

  describe("pending restart note", () => {
    it("shows the restart note when a status is pending", () => {
      renderAdvanced({ defaultOpen: true, status: { pending: true } });
      const note = screen.getByTestId("security-restart-note");
      expect(note).toHaveTextContent("Applies on the next restart");
      expect(note).toHaveTextContent("Saving never restarts the server.");
    });

    it("shows no restart note when nothing is pending", () => {
      renderAdvanced({ defaultOpen: true, status: { pending: false } });
      expect(screen.queryByTestId("security-restart-note")).not.toBeInTheDocument();
    });
  });

  describe("applied and next-start status lines", () => {
    it("shows the applied value with no pending chip when desired matches applied", () => {
      renderAdvanced({
        defaultOpen: true,
        status: {
          applied: { runAsUser: { value: 1000, source: "Override" } },
          desired: { runAsUser: { value: 1000, source: "Override" } },
          pending: false,
        },
      });
      expect(screen.getByText("Applied: 1000")).toBeInTheDocument();
      expect(screen.queryByText(/^Pending:/)).not.toBeInTheDocument();
    });

    it("does not show pending when only the source differs", () => {
      renderAdvanced({
        defaultOpen: true,
        status: {
          applied: { runAsUser: { value: 1000, source: "ModuleDefault" } },
          desired: { runAsUser: { value: 1000, source: "Override" } },
        },
      });
      expect(screen.getByText("Applied: 1000")).toBeInTheDocument();
      expect(screen.queryByText(/^Pending:/)).not.toBeInTheDocument();
    });

    it("shows a pending chip when desired differs from applied", () => {
      renderAdvanced({
        defaultOpen: true,
        status: {
          applied: { runAsUser: { value: 1000, source: "Override" } },
          desired: { runAsUser: { value: 2000, source: "Override" } },
          pending: true,
        },
      });
      expect(screen.getByText("Applied: 1000")).toBeInTheDocument();
      expect(screen.getByText("Pending: 2000 · applies on the next restart")).toBeInTheDocument();
    });

    it("shows the next-start line before the server has ever started", () => {
      renderAdvanced({
        defaultOpen: true,
        status: { desired: { runAsUser: { value: 0, source: "Override" } } },
      });
      expect(screen.getByText("Next start: 0")).toBeInTheDocument();
      expect(screen.getByText("Override")).toBeInTheDocument();
      expect(screen.queryByText(/^Applied:/)).not.toBeInTheDocument();
      expect(screen.getByText("Pending: 0 · applies on the next restart")).toBeInTheDocument();
    });

    it.each<{ source: SecuritySource; value?: number; line: string; chip: string }>([
      { source: "Override", value: 2000, line: "Applied: 2000", chip: "Override" },
      { source: "BuiltInDefault", value: 1000, line: "Applied: 1000", chip: "Built-in default" },
      { source: "ImageDefault", line: "Applied: image default", chip: "Image default" },
      { source: "ImageDefaultOverride", line: "Applied: image default", chip: "Image default (override)" },
    ])("labels an applied $source value with its source chip", ({ source, value, line, chip }) => {
      renderAdvanced({
        defaultOpen: true,
        status: { applied: { runAsUser: value === undefined ? { source } : { value, source } } },
      });
      expect(screen.getByText(line)).toBeInTheDocument();
      expect(screen.getByText(chip)).toBeInTheDocument();
    });

    it("renders the ownership check status with its label", () => {
      renderAdvanced({
        defaultOpen: true,
        status: {
          applied: { volumeOwnershipCheck: { value: "OnlyWhenWrong", source: "BuiltInDefault" } },
          desired: { volumeOwnershipCheck: { value: "OnlyWhenWrong", source: "BuiltInDefault" } },
        },
      });
      expect(screen.getByText("Applied: Only when wrong")).toBeInTheDocument();
      expect(screen.getByText("Built-in default")).toBeInTheDocument();
    });

    it("renders an applied every-start ownership check", () => {
      renderAdvanced({
        defaultOpen: true,
        status: {
          applied: { volumeOwnershipCheck: { value: "EveryStart", source: "Override" } },
        },
      });
      expect(screen.getByText("Applied: Every start")).toBeInTheDocument();
    });

    it("shows no status line when the status carries no values", () => {
      const status: GameServerSecurityStatus = {};
      renderAdvanced({ defaultOpen: true, status });
      expect(screen.queryByText(/^Applied:/)).not.toBeInTheDocument();
      expect(screen.queryByText(/^Next start:/)).not.toBeInTheDocument();
    });
  });

  describe("module default values", () => {
    it("shows the template's numeric and built-in ownership defaults", () => {
      renderAdvanced({
        defaultOpen: true,
        template: makeTemplate({ spec: { security: { runAsUser: 1000 } } }),
      });
      expect(screen.getByText("Module default: 1000")).toBeInTheDocument();
      expect(screen.getAllByText("Module default: image default")).toHaveLength(2);
      expect(screen.getByText("Module default: Only when wrong (built-in)")).toBeInTheDocument();
    });

    it("shows the template's ownership default when it sets one", () => {
      renderAdvanced({
        defaultOpen: true,
        template: makeTemplate({ spec: { security: { volumeOwnershipCheck: "EveryStart" } } }),
      });
      expect(screen.getByText("Module default: Every start")).toBeInTheDocument();
    });
  });

  describe("custom value input", () => {
    it("commits a valid number on Enter", async () => {
      const { onChange } = renderAdvanced({ defaultOpen: true });
      await pick(/Run-as user/, "Custom value");
      await userEvent.type(numberBox("Run-as user"), "1000{Enter}");
      expect(onChange).toHaveBeenCalledWith({ runAsUser: { value: 1000 } });
    });

    it("commits a valid number on blur", async () => {
      const { onChange } = renderAdvanced({ defaultOpen: true });
      await pick(/Filesystem group/, "Custom value");
      await userEvent.type(numberBox("Filesystem group"), "2000");
      await userEvent.tab();
      expect(onChange).toHaveBeenCalledWith({ fsGroup: { value: 2000 } });
    });

    it("keeps the entry invalid, shows an error and does not commit an out-of-range number", async () => {
      const { onChange, onValidityChange } = renderAdvanced({ defaultOpen: true });
      await pick(/Run-as user/, "Custom value");
      const box = numberBox("Run-as user");
      await userEvent.type(box, "4294967296{Enter}");
      expect(onChange).not.toHaveBeenCalled();
      expect(box).toHaveAttribute("aria-invalid", "true");
      expect(screen.getByText("Enter a whole number from 0 to 4294967295.")).toBeInTheDocument();
      expect(onValidityChange).toHaveBeenLastCalledWith(false);
    });

    it("shows the range error for an empty custom entry and does not commit it on blur", async () => {
      const { onChange } = renderAdvanced({ defaultOpen: true });
      await pick(/Run-as group/, "Custom value");
      expect(screen.getByText("Enter a whole number from 0 to 4294967295.")).toBeInTheDocument();
      await userEvent.click(numberBox("Run-as group"));
      await userEvent.tab();
      expect(onChange).not.toHaveBeenCalled();
    });

    it("does not commit when Enter re-submits the saved value", async () => {
      const { onChange } = renderAdvanced({
        defaultOpen: true,
        value: { runAsUser: { value: 1000 } },
      });
      await userEvent.click(numberBox("Run-as user"));
      await userEvent.keyboard("{Enter}");
      expect(onChange).not.toHaveBeenCalled();
    });

    it("follows a saved value that changes from outside the row", () => {
      const onChange = vi.fn();
      const { rerender } = render(
        <SecurityAdvanced value={{ runAsUser: { value: 1000 } }} onChange={onChange} defaultOpen />,
      );
      expect(numberBox("Run-as user")).toHaveValue(1000);
      rerender(<SecurityAdvanced value={{ runAsUser: { value: 42 } }} onChange={onChange} defaultOpen />);
      expect(numberBox("Run-as user")).toHaveValue(42);
    });

    it("reports invalid validity when the saved value is out of range, even while collapsed", () => {
      const { onValidityChange } = renderAdvanced({ value: { runAsUser: { value: 4294967296 } } });
      expect(onValidityChange).toHaveBeenLastCalledWith(false);
    });

    it("reports valid validity for an empty or valid value", () => {
      const { onValidityChange } = renderAdvanced({ value: { fsGroup: { value: 33 } } });
      expect(onValidityChange).toHaveBeenLastCalledWith(true);
    });

    it("reports valid again after collapsing with an unsaved invalid entry", async () => {
      const { onValidityChange } = renderAdvanced();
      const header = screen.getByRole("button", { name: /Advanced security/ });
      await userEvent.click(header);
      await pick(/Run-as user/, "Custom value");
      await userEvent.type(numberBox("Run-as user"), "4294967296");
      expect(onValidityChange).toHaveBeenLastCalledWith(false);
      await userEvent.click(header);
      expect(onValidityChange).toHaveBeenLastCalledWith(true);
    });
  });

  describe("choosing module default or the image default", () => {
    it("uses the image default for a setting", async () => {
      const { onChange } = renderAdvanced({ defaultOpen: true });
      await pick(/Run-as group/, "Use image default");
      expect(onChange).toHaveBeenCalledWith({ runAsGroup: { useImageDefault: true } });
    });

    it("clears the override and returns undefined when nothing is left", async () => {
      const { onChange } = renderAdvanced({
        defaultOpen: true,
        value: { runAsUser: { value: 1000 } },
      });
      await pick(/Run-as user/, "Module default");
      expect(onChange).toHaveBeenCalledWith(undefined);
    });

    it("clears only the chosen setting and keeps the others", async () => {
      const { onChange } = renderAdvanced({
        defaultOpen: true,
        value: { runAsUser: { value: 1000 }, fsGroup: { value: 33 } },
      });
      await pick(/Run-as user/, "Module default");
      expect(onChange).toHaveBeenCalledWith({ fsGroup: { value: 33 } });
    });
  });

  describe("volume ownership check", () => {
    it("sets every-start", async () => {
      const { onChange } = renderAdvanced({ defaultOpen: true });
      await pick(/Volume ownership check/, "Every start");
      expect(onChange).toHaveBeenCalledWith({ volumeOwnershipCheck: "EveryStart" });
    });

    it("sets only-when-wrong over an existing every-start", async () => {
      const { onChange } = renderAdvanced({
        defaultOpen: true,
        value: { volumeOwnershipCheck: "EveryStart" },
      });
      await pick(/Volume ownership check/, "Only when wrong");
      expect(onChange).toHaveBeenCalledWith({ volumeOwnershipCheck: "OnlyWhenWrong" });
    });

    it("returns to the module default", async () => {
      const { onChange } = renderAdvanced({
        defaultOpen: true,
        value: { volumeOwnershipCheck: "EveryStart" },
      });
      await pick(/Volume ownership check/, "Module default");
      expect(onChange).toHaveBeenCalledWith(undefined);
    });

    it("shows the ownership value as text when read-only", () => {
      renderAdvanced({
        defaultOpen: true,
        readOnly: true,
        value: { volumeOwnershipCheck: "EveryStart" },
      });
      expect(screen.getByText("Every start")).toBeInTheDocument();
    });
  });

  describe("no filesystem group chip", () => {
    it("shows when no filesystem group applies", () => {
      renderAdvanced({ defaultOpen: true });
      expect(screen.getByText("No effect: no filesystem group")).toBeInTheDocument();
    });

    it("hides when the module sets a filesystem group", () => {
      renderAdvanced({
        defaultOpen: true,
        template: makeTemplate({ spec: { security: { fsGroup: 33 } } }),
      });
      expect(screen.queryByText("No effect: no filesystem group")).not.toBeInTheDocument();
    });

    it("shows when the owner asks for the image default group, overriding the module's group", () => {
      renderAdvanced({
        defaultOpen: true,
        template: makeTemplate({ spec: { security: { fsGroup: 33 } } }),
        value: { fsGroup: { useImageDefault: true } },
      });
      expect(screen.getByText("No effect: no filesystem group")).toBeInTheDocument();
    });

    it("uses the saved status when one is given", () => {
      renderAdvanced({
        defaultOpen: true,
        status: { desired: { fsGroup: { value: 1000, source: "Override" } } },
      });
      expect(screen.queryByText("No effect: no filesystem group")).not.toBeInTheDocument();
    });
  });

  describe("risky settings", () => {
    it("shows the Risky chip when the owner overrides a risky setting", () => {
      renderAdvanced({
        defaultOpen: true,
        template: makeTemplate({ spec: { security: { risky: { runAsUser: { reason: "Needs root." } } } } }),
        value: { runAsUser: { value: 0 } },
      });
      expect(screen.getByText("Risky")).toBeInTheDocument();
    });

    it("hides the Risky chip when the risky setting is not overridden", () => {
      renderAdvanced({
        defaultOpen: true,
        template: makeTemplate({ spec: { security: { risky: { runAsUser: {} } } } }),
      });
      expect(screen.queryByText("Risky")).not.toBeInTheDocument();
    });

    it("asks before keeping a risky value and applies it on confirm", async () => {
      const { onChange } = renderAdvanced({
        defaultOpen: true,
        template: makeTemplate({ spec: { security: { risky: { runAsUser: { reason: "Plain reason text" } } } } }),
      });
      await pick(/Run-as user/, "Custom value");
      await userEvent.type(numberBox("Run-as user"), "0{Enter}");
      expect(onChange).not.toHaveBeenCalled();
      expect(await screen.findByText("Keep a risky value for Run-as user?")).toBeInTheDocument();
      expect(screen.getByText("The module marks this setting as risky. Its reason:")).toBeInTheDocument();
      expect(screen.getByText("Plain reason text")).toBeInTheDocument();
      await userEvent.click(screen.getByRole("button", { name: "Keep value" }));
      expect(onChange).toHaveBeenCalledWith({ runAsUser: { value: 0 } });
    });

    it("omits the reason box when the module gives no reason", async () => {
      const { onChange } = renderAdvanced({
        defaultOpen: true,
        template: makeTemplate({ spec: { security: { risky: { fsGroup: {} } } } }),
      });
      await pick(/Filesystem group/, "Custom value");
      await userEvent.type(numberBox("Filesystem group"), "33{Enter}");
      expect(await screen.findByText("Keep a risky value for Filesystem group?")).toBeInTheDocument();
      expect(screen.getByText("The module marks this setting as risky.")).toBeInTheDocument();
      expect(screen.queryByText(/Its reason/)).not.toBeInTheDocument();
      await userEvent.click(screen.getByRole("button", { name: "Keep value" }));
      expect(onChange).toHaveBeenCalledWith({ fsGroup: { value: 33 } });
    });

    it("asks before keeping the image default for a risky setting, and cancel leaves the value unchanged", async () => {
      const { onChange } = renderAdvanced({
        defaultOpen: true,
        template: makeTemplate({ spec: { security: { risky: { runAsGroup: { reason: "Group zero." } } } } }),
      });
      await pick(/Run-as group/, "Use image default");
      expect(await screen.findByText("Keep a risky value for Run-as group?")).toBeInTheDocument();
      await userEvent.click(screen.getByRole("button", { name: "Cancel" }));
      await waitFor(() => {
        expect(screen.queryByText("Keep a risky value for Run-as group?")).not.toBeInTheDocument();
      });
      expect(onChange).not.toHaveBeenCalled();
      expect(screen.queryByRole("spinbutton", { name: "Run-as group value" })).not.toBeInTheDocument();
    });

    it("cancel on a typed risky value clears the unsaved text", async () => {
      const { onChange } = renderAdvanced({
        defaultOpen: true,
        template: makeTemplate({ spec: { security: { risky: { runAsUser: { reason: "Needs root." } } } } }),
      });
      await pick(/Run-as user/, "Custom value");
      await userEvent.type(numberBox("Run-as user"), "0{Enter}");
      expect(await screen.findByText("Keep a risky value for Run-as user?")).toBeInTheDocument();
      await userEvent.click(screen.getByRole("button", { name: "Cancel" }));
      await waitFor(() => {
        expect(screen.queryByText("Keep a risky value for Run-as user?")).not.toBeInTheDocument();
      });
      expect(onChange).not.toHaveBeenCalled();
      expect(screen.getByRole("button", { name: /Run-as user/ })).toHaveTextContent("Module default");
    });

    it("asks before a risky ownership check and applies it on confirm", async () => {
      const { onChange } = renderAdvanced({
        defaultOpen: true,
        template: makeTemplate({ spec: { security: { risky: { volumeOwnershipCheck: {} } } } }),
      });
      await pick(/Volume ownership check/, "Every start");
      expect(await screen.findByText("Keep a risky value for Volume ownership check?")).toBeInTheDocument();
      expect(onChange).not.toHaveBeenCalled();
      await userEvent.click(screen.getByRole("button", { name: "Keep value" }));
      expect(onChange).toHaveBeenCalledWith({ volumeOwnershipCheck: "EveryStart" });
    });

    it("does not ask before returning a risky setting to the module default", async () => {
      const { onChange } = renderAdvanced({
        defaultOpen: true,
        value: { runAsUser: { value: 0 } },
        template: makeTemplate({ spec: { security: { risky: { runAsUser: { reason: "Needs root." } } } } }),
      });
      await pick(/Run-as user/, "Module default");
      expect(onChange).toHaveBeenCalledWith(undefined);
      expect(screen.queryByText("Keep a risky value for Run-as user?")).not.toBeInTheDocument();
    });
  });
});
