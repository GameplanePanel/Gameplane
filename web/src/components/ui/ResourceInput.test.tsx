import { describe, it, expect, vi } from "vitest";
import { render, fireEvent, screen } from "@testing-library/react";
import { ResourceInput } from "./ResourceInput";

// The number field and the range slider are both <input> elements, so
// getByDisplayValue is ambiguous when they show the same value. Query the
// number field by its unique "spinbutton" role. Interactions use fireEvent
// (not userEvent) so a controlled type="number" input commits deterministically
// without per-keystroke sanitization of transient states like "0.".

describe("ResourceInput", () => {
  describe("CPU mode", () => {
    it("renders slider, numeric input, and unit dropdown for CPU", () => {
      render(<ResourceInput kind="cpu" value="2" onChange={() => {}} id="test-cpu" />);
      expect(screen.getByRole("slider")).toBeInTheDocument();
      expect(screen.getByRole("combobox")).toBeInTheDocument();
      expect(screen.getByRole("spinbutton")).toHaveValue(2);
    });

    it("parses canonical CPU quantity and displays in natural unit", () => {
      render(<ResourceInput kind="cpu" value="500m" onChange={() => {}} />);
      // 500m = 0.5 cores, displayed as 500 in the mCPU unit.
      expect(screen.getByRole("spinbutton")).toHaveValue(500);
      expect((screen.getByRole("combobox") as HTMLSelectElement).value).toBe("m");
    });

    it("moving the slider calls onChange with canonical CPU string", () => {
      const fn = vi.fn();
      render(<ResourceInput kind="cpu" value="1" onChange={fn} />);
      fireEvent.change(screen.getByRole("slider"), { target: { value: "2" } });
      expect(fn).toHaveBeenCalledWith("2");
    });

    it("emits fractional CPU as millicores", () => {
      const fn = vi.fn();
      render(<ResourceInput kind="cpu" value="1" onChange={fn} />);
      fireEvent.change(screen.getByRole("slider"), { target: { value: "0.5" } });
      expect(fn).toHaveBeenCalledWith("500m");
    });

    it("commits the typed input on blur as a canonical CPU string", () => {
      const fn = vi.fn();
      render(<ResourceInput kind="cpu" value="1" onChange={fn} />);
      const input = screen.getByRole("spinbutton");
      fireEvent.change(input, { target: { value: "0.5" } });
      fireEvent.blur(input);
      expect(fn).toHaveBeenCalledWith("500m");
    });

    it("switching CPU unit converts displayed value without changing emitted amount", () => {
      render(<ResourceInput kind="cpu" value="1" onChange={() => {}} />);
      expect(screen.getByRole("spinbutton")).toHaveValue(1);
      fireEvent.change(screen.getByRole("combobox"), { target: { value: "m" } });
      // 1 core = 1000 millicores; the slider still reflects the same 1 core.
      expect(screen.getByRole("spinbutton")).toHaveValue(1000);
      expect(Number((screen.getByRole("slider") as HTMLInputElement).value)).toBe(1);
    });

    it("clamps input outside min/max range on blur", () => {
      const fn = vi.fn();
      render(<ResourceInput kind="cpu" value="2" onChange={fn} min={0.5} max={8} />);
      const input = screen.getByRole("spinbutton");
      fireEvent.change(input, { target: { value: "20" } });
      fireEvent.blur(input);
      expect(fn).toHaveBeenCalledWith("8");
    });

    it("respects default CPU limits when min/max omitted", () => {
      render(<ResourceInput kind="cpu" value="1" onChange={() => {}} />);
      const slider = screen.getByRole("slider") as HTMLInputElement;
      expect(Number(slider.min)).toBe(0.1);
      expect(Number(slider.max)).toBe(16);
    });
  });

  describe("Memory mode", () => {
    it("renders slider, numeric input, and unit dropdown for memory", () => {
      render(<ResourceInput kind="memory" value="4Gi" onChange={() => {}} id="test-mem" />);
      expect(screen.getByRole("slider")).toBeInTheDocument();
      expect(screen.getByRole("combobox")).toBeInTheDocument();
      expect(screen.getByRole("spinbutton")).toHaveValue(4);
    });

    it("parses canonical memory quantity and displays in natural unit", () => {
      render(<ResourceInput kind="memory" value="512Mi" onChange={() => {}} />);
      // 512Mi keeps its unit (0.5 Gi isn't a whole promotion).
      expect(screen.getByRole("spinbutton")).toHaveValue(512);
      expect((screen.getByRole("combobox") as HTMLSelectElement).value).toBe("Mi");
    });

    it("moving the slider calls onChange with canonical memory string", () => {
      const fn = vi.fn();
      render(<ResourceInput kind="memory" value="2Gi" onChange={fn} />);
      fireEvent.change(screen.getByRole("slider"), { target: { value: "4" } });
      expect(fn).toHaveBeenCalledWith("4Gi");
    });

    it("emits fractional memory as a smaller whole unit", () => {
      const fn = vi.fn();
      render(<ResourceInput kind="memory" value="4Gi" onChange={fn} />);
      fireEvent.change(screen.getByRole("slider"), { target: { value: "1.5" } });
      // 1.5 Gi emits as "1536Mi" (integral binary unit)
      expect(fn).toHaveBeenCalledWith("1536Mi");
    });

    it("commits the typed input on blur as a canonical memory string", () => {
      const fn = vi.fn();
      render(<ResourceInput kind="memory" value="1Gi" onChange={fn} />);
      const input = screen.getByRole("spinbutton");
      fireEvent.change(input, { target: { value: "2" } });
      fireEvent.blur(input);
      expect(fn).toHaveBeenCalledWith("2Gi");
    });

    it("switching memory unit converts displayed value without changing emitted amount", () => {
      render(<ResourceInput kind="memory" value="4Gi" onChange={() => {}} />);
      expect(screen.getByRole("spinbutton")).toHaveValue(4);
      fireEvent.change(screen.getByRole("combobox"), { target: { value: "Mi" } });
      // 4 Gi = 4096 MiB; the slider still reflects the same 4 GiB.
      expect(screen.getByRole("spinbutton")).toHaveValue(4096);
      expect(Number((screen.getByRole("slider") as HTMLInputElement).value)).toBe(4);
    });

    it("clamps input outside min/max range on blur", () => {
      const fn = vi.fn();
      render(<ResourceInput kind="memory" value="4Gi" onChange={fn} min={1} max={16} />);
      const input = screen.getByRole("spinbutton");
      fireEvent.change(input, { target: { value: "100" } });
      fireEvent.blur(input);
      expect(fn).toHaveBeenCalledWith("16Gi");
    });

    it("respects default memory limits when min/max omitted", () => {
      render(<ResourceInput kind="memory" value="1Gi" onChange={() => {}} />);
      const slider = screen.getByRole("slider") as HTMLInputElement;
      expect(Number(slider.min)).toBe(0.25);
      expect(Number(slider.max)).toBe(64);
    });

    it("treats unparseable value as min", () => {
      render(<ResourceInput kind="memory" value="invalid" onChange={() => {}} min={1} max={16} />);
      expect(Number((screen.getByRole("slider") as HTMLInputElement).value)).toBe(1);
    });
  });

  describe("Synchronized behavior", () => {
    it("slider move emits the canonical quantity", () => {
      const fn = vi.fn();
      render(<ResourceInput kind="memory" value="2Gi" onChange={fn} />);
      fireEvent.change(screen.getByRole("slider"), { target: { value: "4" } });
      expect(fn).toHaveBeenCalledWith("4Gi");
    });

    it("input edit emits the canonical quantity on blur", () => {
      const fn = vi.fn();
      render(<ResourceInput kind="cpu" value="1" onChange={fn} />);
      const input = screen.getByRole("spinbutton");
      fireEvent.change(input, { target: { value: "4" } });
      fireEvent.blur(input);
      expect(fn).toHaveBeenCalledWith("4");
    });

    it("all three controls can be toggled without data loss", () => {
      const fn = vi.fn();
      render(<ResourceInput kind="cpu" value="2" onChange={fn} />);
      // Switch cores → mCPU: the field re-derives to 2000, no emit yet.
      fireEvent.change(screen.getByRole("combobox"), { target: { value: "m" } });
      expect(screen.getByRole("spinbutton")).toHaveValue(2000);
      // Edit the input in mCPU and commit.
      const input = screen.getByRole("spinbutton");
      fireEvent.change(input, { target: { value: "1000" } });
      fireEvent.blur(input);
      // 1000 millicores = 1 core.
      expect(fn).toHaveBeenCalledWith("1");
    });
  });

  describe("Accessibility", () => {
    it("passes aria-label to slider", () => {
      render(<ResourceInput kind="cpu" value="1" onChange={() => {}} />);
      expect(screen.getByRole("slider")).toHaveAttribute("aria-label", "CPU cores");
    });

    it("provides aria-labels for input and select", () => {
      render(<ResourceInput kind="memory" value="4Gi" onChange={() => {}} />);
      expect(screen.getByLabelText("Memory (GiB) value")).toBeInTheDocument();
      expect(screen.getByLabelText("Memory (GiB) unit")).toBeInTheDocument();
    });

    it("uses the label prop as the accessible name when given", () => {
      render(<ResourceInput kind="cpu" value="1" onChange={() => {}} label="Reserved CPU" />);
      expect(screen.getByRole("slider")).toHaveAttribute("aria-label", "Reserved CPU");
      expect(screen.getByLabelText("Reserved CPU value")).toBeInTheDocument();
      expect(screen.getByLabelText("Reserved CPU unit")).toBeInTheDocument();
    });

    it("disables all controls when disabled prop is true", () => {
      render(<ResourceInput kind="cpu" value="1" onChange={() => {}} disabled />);
      expect(screen.getByRole("slider")).toBeDisabled();
      expect(screen.getByRole("combobox")).toBeDisabled();
      expect(screen.getByRole("spinbutton")).toBeDisabled();
    });
  });

  describe("Edge cases", () => {
    it("does not emit while typing and snaps empty to min on blur", () => {
      const fn = vi.fn();
      render(<ResourceInput kind="cpu" value="2" onChange={fn} />);
      const input = screen.getByRole("spinbutton");
      fireEvent.change(input, { target: { value: "" } });
      // Editing the buffer alone doesn't emit (we commit on blur).
      expect(fn).not.toHaveBeenCalled();
      fireEvent.blur(input);
      // Empty on blur snaps to the min (0.1 cores → "100m").
      expect(fn).toHaveBeenCalledWith("100m");
    });

    it("passes the step through to the slider", () => {
      render(<ResourceInput kind="memory" value="2Gi" onChange={() => {}} step={2} />);
      expect(Number((screen.getByRole("slider") as HTMLInputElement).step)).toBe(2);
    });

    it("normalizes 1024Ki to 1Mi and converts across units", () => {
      render(<ResourceInput kind="memory" value="1024Ki" onChange={() => {}} />);
      // 1024Ki promotes to its natural whole unit, 1Mi.
      expect(screen.getByRole("spinbutton")).toHaveValue(1);
      const select = screen.getByRole("combobox") as HTMLSelectElement;
      expect(select.value).toBe("Mi");
      // Convert to Gi: 1Mi = 1/1024 Gi ≈ 0.001 after display rounding.
      fireEvent.change(select, { target: { value: "Gi" } });
      expect(screen.getByRole("spinbutton")).toHaveValue(0.001);
    });
  });
});
