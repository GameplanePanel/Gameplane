import { describe, it, expect, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { BackupFilters } from "./BackupFilters";
import { makeServer } from "@/test/factories";

const baseProps = {
  search: "",
  location: "",
  onLocationChange: vi.fn(),
  locations: ["local", "remote"],
  onSearchChange: vi.fn(),
  server: "",
  onServerChange: vi.fn(),
  phase: "",
  onPhaseChange: vi.fn(),
  servers: [makeServer({ metadata: { name: "alpha" } }), makeServer({ metadata: { name: "beta" } })],
  phases: ["Pending", "Succeeded"],
};

describe("BackupFilters", () => {
  it("renders all server options plus an All-servers row", async () => {
    render(<BackupFilters {...baseProps} />);
    await userEvent.click(screen.getByRole("button", { name: /^Filter$/ }));
    const serverButton = screen.getByRole("button", { name: /Filter by server/i });
    await userEvent.click(serverButton);
    expect(screen.getByRole("option", { name: "All servers" })).toBeInTheDocument();
    expect(screen.getByRole("option", { name: "alpha" })).toBeInTheDocument();
    expect(screen.getByRole("option", { name: "beta" })).toBeInTheDocument();
  });

  it("emits onSearchChange while user types", async () => {
    const onSearchChange = vi.fn();
    render(<BackupFilters {...baseProps} onSearchChange={onSearchChange} />);
    await userEvent.type(screen.getByPlaceholderText(/Search by name/i), "x");
    expect(onSearchChange).toHaveBeenCalledWith("x");
  });

  it("applies the server dropdown draft only after Apply", async () => {
    const onServerChange = vi.fn();
    render(<BackupFilters {...baseProps} onServerChange={onServerChange} />);
    await userEvent.click(screen.getByRole("button", { name: /^Filter$/ }));
    const serverButton = screen.getByRole("button", { name: /Filter by server/i });
    await userEvent.click(serverButton);
    const alphaOption = screen.getByRole("option", { name: "alpha" });
    await userEvent.click(alphaOption);
    expect(onServerChange).not.toHaveBeenCalled();
    await userEvent.click(screen.getByRole("button", { name: "Apply" }));
    expect(onServerChange).toHaveBeenCalledWith("alpha");
  });

  it("applies the phase dropdown draft only after Apply", async () => {
    const onPhaseChange = vi.fn();
    render(<BackupFilters {...baseProps} onPhaseChange={onPhaseChange} />);
    await userEvent.click(screen.getByRole("button", { name: /^Filter$/ }));
    const phaseButton = screen.getByRole("button", { name: /Filter by phase/i });
    await userEvent.click(phaseButton);
    const succeededOption = screen.getByRole("option", { name: "Succeeded" });
    await userEvent.click(succeededOption);
    expect(onPhaseChange).not.toHaveBeenCalled();
    await userEvent.click(screen.getByRole("button", { name: "Apply" }));
    expect(onPhaseChange).toHaveBeenCalledWith("Succeeded");
  });

  it("renders the trailing slot when provided", () => {
    render(<BackupFilters {...baseProps} trailing="3 results" />);
    expect(screen.getByText("3 results")).toBeInTheDocument();
  });
  it("discards a location draft when closed and applies only the reopened selection", async () => {
    const onLocationChange = vi.fn();
    render(<BackupFilters {...baseProps} onLocationChange={onLocationChange} />);
    expect(screen.queryByRole("button", { name: /Filter by cluster/ })).not.toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: /^Filter$/ }));
    await userEvent.click(screen.getByRole("button", { name: /Filter by cluster/ }));
    await userEvent.click(screen.getByRole("option", { name: "remote" }));
    // Dismiss the outer filter menu, not the nested Select trigger.
    screen.getByRole("button", { name: "Clear" }).focus();
    await userEvent.keyboard("{Escape}");
    expect(screen.queryByRole("button", { name: /Filter by cluster/ })).not.toBeInTheDocument();
    expect(onLocationChange).not.toHaveBeenCalled();
    await userEvent.click(screen.getByRole("button", { name: /^Filter$/ }));
    expect(screen.getByRole("button", { name: /Filter by cluster/ })).toHaveTextContent("All clusters");
    await userEvent.click(screen.getByRole("button", { name: /Filter by cluster/ }));
    await userEvent.click(screen.getByRole("option", { name: "remote" }));
    await userEvent.click(screen.getByRole("button", { name: "Apply" }));
    expect(onLocationChange).toHaveBeenCalledWith("remote");
  });

  it("counts applied filters and clears the draft without changing results until Apply", async () => {
    const onLocationChange = vi.fn();
    const onServerChange = vi.fn();
    const onPhaseChange = vi.fn();
    render(<BackupFilters {...baseProps} server="alpha" phase="Succeeded"
      onLocationChange={onLocationChange} onServerChange={onServerChange} onPhaseChange={onPhaseChange} />);
    await userEvent.click(screen.getByRole("button", { name: /^Filter\s*2$/ }));
    await userEvent.click(screen.getByRole("button", { name: "Clear" }));
    expect(onLocationChange).not.toHaveBeenCalled();
    expect(onServerChange).not.toHaveBeenCalled();
    expect(onPhaseChange).not.toHaveBeenCalled();
    expect(screen.getByRole("button", { name: /Filter by server/ })).toHaveTextContent("All servers");
    expect(screen.getByRole("button", { name: /Filter by phase/ })).toHaveTextContent("All phases");
    await userEvent.click(screen.getByRole("button", { name: "Apply" }));
    expect(onLocationChange).toHaveBeenCalledWith("");
    expect(onServerChange).toHaveBeenCalledWith("");
    expect(onPhaseChange).toHaveBeenCalledWith("");
  });

});
