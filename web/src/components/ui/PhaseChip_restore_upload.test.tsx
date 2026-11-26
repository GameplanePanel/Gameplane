import { describe, it, expect } from "vitest";
import { render, screen } from "@testing-library/react";
import { PhaseChip } from "./PhaseChip";

describe("PhaseChip restore upload phases", () => {
  it("labels Receiving and colors it like the in-progress phases", () => {
    render(<PhaseChip phase="Receiving" />);
    const chip = screen.getByText("Receiving").closest('[data-slot="chip"]');
    expect(chip).toHaveAttribute("data-color", "success");
    expect(chip).toHaveAttribute("data-phase", "Receiving");
  });

  it("labels AwaitingConfirmation with a space and the warning color", () => {
    render(<PhaseChip phase="AwaitingConfirmation" />);
    const chip = screen.getByText("Awaiting confirmation").closest('[data-slot="chip"]');
    expect(chip).toHaveAttribute("data-color", "warning");
    expect(chip).toHaveAttribute("data-phase", "AwaitingConfirmation");
  });
});
