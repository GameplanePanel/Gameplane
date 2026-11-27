import { describe, it, expect } from "vitest";
import { render, screen } from "@testing-library/react";
import { StatCard } from "./StatCard";

describe("StatCard", () => {
  it("renders label, value, and sub", () => {
    render(<StatCard label="Players" value={42} sub="online" />);
    expect(screen.getByText("Players")).toBeInTheDocument();
    expect(screen.getByText("42")).toBeInTheDocument();
    expect(screen.getByText("online")).toBeInTheDocument();
  });

  it("renders without sub", () => {
    render(<StatCard label="X" value="ok" />);
    expect(screen.getByText("X")).toBeInTheDocument();
    expect(screen.getByText("ok")).toBeInTheDocument();
    expect(screen.queryByText("online")).not.toBeInTheDocument();
  });

  it("applies accent to icon", () => {
    render(
      <StatCard
        label="Z"
        value="v"
        icon={<span data-testid="test-icon">📊</span>}
        accent="danger"
      />,
    );
    const icon = screen.getByTestId("test-icon").parentElement!;
    expect(icon.className).toContain("text-danger");
  });

  it("defaults to primary accent when none specified", () => {
    render(
      <StatCard
        label="Z"
        value="v"
        icon={<span data-testid="test-icon">📊</span>}
      />,
    );
    const icon = screen.getByTestId("test-icon").parentElement!;
    expect(icon.className).toContain("text-accent");
  });

  it("renders with success accent", () => {
    render(
      <StatCard
        label="Status"
        value={100}
        sub="healthy"
        icon={<span data-testid="test-icon">✓</span>}
        accent="success"
      />,
    );
    const icon = screen.getByTestId("test-icon").parentElement!;
    expect(icon.className).toContain("text-success");
    expect(screen.getByText("Status")).toBeInTheDocument();
    expect(screen.getByText("100")).toBeInTheDocument();
    expect(screen.getByText("healthy")).toBeInTheDocument();
  });

  it("applies custom className", () => {
    const { container } = render(
      <StatCard
        label="Test"
        value="123"
        className="custom-class"
      />,
    );
    const card = container.querySelector("[class*='bg-surface']");
    expect(card?.className).toContain("custom-class");
  });

  it.each([
    ["info", "text-stat-players"],
    ["teal", "text-stat-storage"],
    ["slate", "text-stat-nodes"],
    ["violet", "text-chart-stat"],
  ] as const)("renders the %s accent as %s on the icon", (accent, cls) => {
    render(
      <StatCard label="Z" value="v" icon={<span data-testid="test-icon">i</span>} accent={accent} />,
    );
    expect(screen.getByTestId("test-icon").parentElement!.className).toContain(cls);
  });

  it("shows an amber badge and amber sub-text when warning is set, keeping the icon colour", () => {
    render(
      <StatCard
        label="Storage"
        value="77 GB"
        sub="of 1.7 TiB"
        icon={<span data-testid="test-icon">i</span>}
        accent="teal"
        warning="Overcommitted"
      />,
    );
    const badge = screen.getByText("Overcommitted");
    expect(badge.className).toContain("text-warning");
    expect(badge.className).toContain("bg-warning/15");
    expect(screen.getByText("of 1.7 TiB").className).toContain("text-warning");
    const icon = screen.getByTestId("test-icon").parentElement!;
    expect(icon.className).toContain("text-stat-storage");
    expect(icon.className).not.toContain("text-warning");
  });

  it("renders no badge and muted sub-text without the warning prop", () => {
    render(<StatCard label="X" value="1" sub="fine" />);
    expect(screen.queryByText("Overcommitted")).not.toBeInTheDocument();
    expect(screen.getByText("fine").className).toContain("text-muted");
    expect(screen.getByText("fine").className).not.toContain("text-warning");
  });
});
