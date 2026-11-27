import { describe, it, expect, vi } from "vitest";
import { render, fireEvent, screen } from "@testing-library/react";
import { StorageInput } from "./StorageInput";

describe("StorageInput", () => {
  it("shows a whole-unit quantity in its own unit", () => {
    render(<StorageInput label="Storage size" value="20Gi" onChange={() => {}} />);
    expect(screen.getByLabelText("Storage size value")).toHaveValue(20);
    expect((screen.getByLabelText("Storage size unit") as HTMLSelectElement).value).toBe("Gi");
  });

  it("round-trips 1536Mi: shown as 1536 MiB and not re-emitted", () => {
    const fn = vi.fn();
    render(<StorageInput label="Storage size" value="1536Mi" onChange={fn} />);
    const box = screen.getByLabelText("Storage size value");
    expect(box).toHaveValue(1536);
    expect((screen.getByLabelText("Storage size unit") as HTMLSelectElement).value).toBe("Mi");
    fireEvent.change(box, { target: { value: "1536" } });
    fireEvent.blur(box);
    expect(fn).not.toHaveBeenCalled();
  });

  it("emits a quantity on blur in the chosen unit", () => {
    const fn = vi.fn();
    render(<StorageInput label="Storage size" value="10Gi" onChange={fn} />);
    const box = screen.getByLabelText("Storage size value");
    fireEvent.change(box, { target: { value: "2" } });
    expect(fn).not.toHaveBeenCalled();
    fireEvent.blur(box);
    expect(fn).toHaveBeenCalledWith("2Gi");
    fireEvent.change(screen.getByLabelText("Storage size unit"), { target: { value: "Ti" } });
    fireEvent.change(box, { target: { value: "1" } });
    fireEvent.blur(box);
    expect(fn).toHaveBeenLastCalledWith("1Ti");
  });

  it("emits a fraction of a GiB as whole MiB", () => {
    const fn = vi.fn();
    render(<StorageInput label="Storage size" value="10Gi" onChange={fn} />);
    const box = screen.getByLabelText("Storage size value");
    fireEvent.change(box, { target: { value: "1.5" } });
    fireEvent.blur(box);
    expect(fn).toHaveBeenCalledWith("1536Mi");
  });

  it("emits an empty string when cleared and ignores zero or negative input", () => {
    const fn = vi.fn();
    render(<StorageInput label="Storage size" value="10Gi" onChange={fn} />);
    const box = screen.getByLabelText("Storage size value");
    fireEvent.change(box, { target: { value: "0" } });
    fireEvent.blur(box);
    expect(fn).not.toHaveBeenCalled();
    fireEvent.change(box, { target: { value: "" } });
    fireEvent.blur(box);
    expect(fn).toHaveBeenCalledWith("");
  });

  it("starts empty in GiB for an empty or invalid value", () => {
    render(<StorageInput label="Storage size" value="" onChange={() => {}} />);
    expect(screen.getByLabelText("Storage size value")).toHaveValue(null);
    expect((screen.getByLabelText("Storage size unit") as HTMLSelectElement).value).toBe("Gi");
  });

  it("switching unit converts the shown number without emitting", () => {
    const fn = vi.fn();
    render(<StorageInput label="Storage size" value="2Gi" onChange={fn} />);
    fireEvent.change(screen.getByLabelText("Storage size unit"), { target: { value: "Mi" } });
    expect(screen.getByLabelText("Storage size value")).toHaveValue(2048);
    expect(fn).not.toHaveBeenCalled();
  });

  it("picking a unit right after typing applies it to the typed number", () => {
    const fn = vi.fn();
    render(<StorageInput label="Storage size" value="10Gi" onChange={fn} />);
    const box = screen.getByLabelText("Storage size value");
    fireEvent.change(box, { target: { value: "500" } });
    fireEvent.blur(box); // focus moves to the unit select
    expect(fn).toHaveBeenLastCalledWith("500Gi");
    fireEvent.change(screen.getByLabelText("Storage size unit"), { target: { value: "Mi" } });
    expect(fn).toHaveBeenLastCalledWith("500Mi");
  });

  it("marks the box invalid and disables both controls when asked", () => {
    render(<StorageInput label="Storage size" value="2Gi" onChange={() => {}} invalid disabled />);
    expect(screen.getByLabelText("Storage size value")).toHaveAttribute("aria-invalid", "true");
    expect(screen.getByLabelText("Storage size value")).toBeDisabled();
    expect(screen.getByLabelText("Storage size unit")).toBeDisabled();
  });
});
