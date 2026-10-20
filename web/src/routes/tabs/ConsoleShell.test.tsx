import { createRef } from "react";
import { render, screen, fireEvent } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, it, expect, vi } from "vitest";

import type { WSStatus } from "@/lib/ws";
import type { ConsoleHandle } from "./useConsoleTerminal";
import { ConsoleShell } from "./ConsoleShell";

function makeHandle(status: WSStatus = "open"): ConsoleHandle {
  return {
    hostRef: createRef<HTMLDivElement>(),
    status,
    clear: vi.fn(),
    download: vi.fn(),
    toggleFullscreen: vi.fn(),
    sendCommand: vi.fn(),
  };
}

function getInput(): HTMLInputElement {
  return screen.getByPlaceholderText(/Type a command/) as HTMLInputElement;
}

function getForm(input: HTMLInputElement): HTMLFormElement {
  return input.closest("form") as HTMLFormElement;
}

describe("ConsoleShell", () => {
  it.each([
    ["open", "LIVE"],
    ["reconnecting", "reconnecting…"],
    ["connecting", "connecting…"],
    ["closed", "offline"],
  ] as const)("renders the %s status as %s", (status, label) => {
    render(<ConsoleShell handle={makeHandle(status)} />);
    expect(screen.getByText(label)).toBeInTheDocument();
  });

  it("submits the trimmed command on Enter and clears the input", async () => {
    const user = userEvent.setup();
    const handle = makeHandle();
    render(<ConsoleShell handle={handle} />);
    const input = getInput();

    await user.type(input, "  say hi  {Enter}");

    expect(handle.sendCommand).toHaveBeenCalledTimes(1);
    expect(handle.sendCommand).toHaveBeenCalledWith("say hi");
    expect(input.value).toBe("");
  });

  it("does not send an empty or whitespace-only command and disables Send", async () => {
    const user = userEvent.setup();
    const handle = makeHandle();
    render(<ConsoleShell handle={handle} />);
    const input = getInput();
    const sendButton = screen.getByRole("button", { name: "Send" });

    expect(sendButton).toBeDisabled();

    await user.type(input, "   {Enter}");

    expect(handle.sendCommand).not.toHaveBeenCalled();
    expect(sendButton).toBeDisabled();
  });

  it("submits the same command when the Send button is clicked", async () => {
    const user = userEvent.setup();
    const handle = makeHandle();
    render(<ConsoleShell handle={handle} />);
    const input = getInput();
    const sendButton = screen.getByRole("button", { name: "Send" });

    await user.type(input, "ping");
    expect(sendButton).toBeEnabled();
    await user.click(sendButton);

    expect(handle.sendCommand).toHaveBeenCalledTimes(1);
    expect(handle.sendCommand).toHaveBeenCalledWith("ping");
    expect(input.value).toBe("");
  });

  it("recalls history with ArrowUp/ArrowDown and restores the draft past the newest entry", async () => {
    const user = userEvent.setup();
    const handle = makeHandle();
    render(<ConsoleShell handle={handle} />);
    const input = getInput();

    await user.type(input, "a{Enter}");
    await user.type(input, "b{Enter}");
    await user.type(input, "c{Enter}");
    expect(handle.sendCommand).toHaveBeenCalledTimes(3);

    await user.type(input, "dr");
    expect(input.value).toBe("dr");

    await user.keyboard("{ArrowUp}");
    expect(input.value).toBe("c");
    await user.keyboard("{ArrowUp}");
    expect(input.value).toBe("b");
    await user.keyboard("{ArrowUp}");
    expect(input.value).toBe("a");
    await user.keyboard("{ArrowUp}");
    expect(input.value).toBe("a");

    await user.keyboard("{ArrowDown}");
    expect(input.value).toBe("b");
    await user.keyboard("{ArrowDown}");
    expect(input.value).toBe("c");
    await user.keyboard("{ArrowDown}");
    expect(input.value).toBe("dr");
    await user.keyboard("{ArrowDown}");
    expect(input.value).toBe("dr");
  });

  it("leaves the input unchanged for ArrowUp with empty history and ArrowDown when not browsing", async () => {
    const user = userEvent.setup();
    const handle = makeHandle();
    render(<ConsoleShell handle={handle} />);
    const input = getInput();

    await user.type(input, "keep");
    await user.keyboard("{ArrowUp}");
    expect(input.value).toBe("keep");
    await user.keyboard("{ArrowDown}");
    expect(input.value).toBe("keep");
    expect(handle.sendCommand).not.toHaveBeenCalled();
  });

  it("stores a duplicate consecutive command only once", async () => {
    const user = userEvent.setup();
    const handle = makeHandle();
    render(<ConsoleShell handle={handle} />);
    const input = getInput();

    await user.type(input, "x{Enter}");
    await user.type(input, "x{Enter}");
    expect(handle.sendCommand).toHaveBeenCalledTimes(2);

    await user.keyboard("{ArrowUp}");
    expect(input.value).toBe("x");
    await user.keyboard("{ArrowUp}");
    expect(input.value).toBe("x");
    // With a single stored entry, ArrowDown from the oldest entry restores
    // the (empty) draft; a duplicate entry would show "x" here instead.
    await user.keyboard("{ArrowDown}");
    expect(input.value).toBe("");
  });

  it("caps history at 100 entries, dropping the oldest", () => {
    const handle = makeHandle();
    render(<ConsoleShell handle={handle} />);
    const input = getInput();
    const form = getForm(input);
    expect(form).not.toBeNull();
    if (!form) return;

    for (let i = 0; i <= 100; i++) {
      fireEvent.change(input, { target: { value: `c${i}` } });
      fireEvent.submit(form);
    }
    expect(handle.sendCommand).toHaveBeenCalledTimes(101);

    for (let i = 0; i < 101; i++) {
      fireEvent.keyDown(input, { key: "ArrowUp" });
    }
    // 101 commands were sent but only the newest 100 are kept, so the
    // oldest ("c0") was dropped and the recall floor is "c1".
    expect(input.value).toBe("c1");
  });

  it("calls each toolbar handle exactly once and nothing else", async () => {
    const user = userEvent.setup();
    const handle = makeHandle();
    render(<ConsoleShell handle={handle} />);

    await user.click(screen.getByRole("button", { name: "Clear terminal" }));
    expect(handle.clear).toHaveBeenCalledTimes(1);
    expect(handle.download).not.toHaveBeenCalled();
    expect(handle.toggleFullscreen).not.toHaveBeenCalled();
    expect(handle.sendCommand).not.toHaveBeenCalled();

    await user.click(screen.getByRole("button", { name: "Download terminal buffer" }));
    expect(handle.download).toHaveBeenCalledTimes(1);
    expect(handle.clear).toHaveBeenCalledTimes(1);
    expect(handle.toggleFullscreen).not.toHaveBeenCalled();
    expect(handle.sendCommand).not.toHaveBeenCalled();

    await user.click(screen.getByRole("button", { name: "Toggle fullscreen" }));
    expect(handle.toggleFullscreen).toHaveBeenCalledTimes(1);
    expect(handle.clear).toHaveBeenCalledTimes(1);
    expect(handle.download).toHaveBeenCalledTimes(1);
    expect(handle.sendCommand).not.toHaveBeenCalled();
  });

  it("stays usable and still sends commands when the status is closed", async () => {
    const user = userEvent.setup();
    const handle = makeHandle("closed");
    render(<ConsoleShell handle={handle} />);

    expect(screen.getByText("offline")).toBeInTheDocument();
    const input = getInput();
    expect(input).toBeEnabled();

    await user.type(input, "status{Enter}");
    expect(handle.sendCommand).toHaveBeenCalledTimes(1);
    expect(handle.sendCommand).toHaveBeenCalledWith("status");
  });
});
