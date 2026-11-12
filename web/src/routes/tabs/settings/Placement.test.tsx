import { describe, it, expect, vi } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";
import { PlacementSection } from "./Placement";
import { makeServer } from "@/test/factories";

// Monaco can't render in jsdom — replace with a textarea so onChange fires.
vi.mock("@/lib/monaco-editor", () => ({
  default: ({
    value,
    onChange,
  }: {
    value: string;
    onChange?: (v: string | undefined) => void;
  }) => (
    <textarea
      data-testid="monaco-editor"
      value={value}
      onChange={(e) => onChange?.(e.target.value)}
    />
  ),
}));

const baseDraft = makeServer();

describe("PlacementSection", () => {
  it("renders two editors for tolerations and affinity", () => {
    const onChange = vi.fn();
    render(
      <PlacementSection
        draft={baseDraft}
        onChange={onChange}
      />,
    );
    const editors = screen.getAllByTestId("monaco-editor");
    expect(editors).toHaveLength(2);
    // First is tolerations (array), second is affinity (object).
    expect(editors[0]).toHaveValue("[]");
    expect(editors[1]).toHaveValue("{}");
  });

  it("renders labels with HeroUI Label component", () => {
    const onChange = vi.fn();
    render(
      <PlacementSection
        draft={baseDraft}
        onChange={onChange}
      />,
    );
    expect(screen.getByText("Tolerations")).toBeInTheDocument();
    expect(screen.getByText("Affinity")).toBeInTheDocument();
  });

  it("renders descriptions with HeroUI Description component", () => {
    const onChange = vi.fn();
    render(
      <PlacementSection
        draft={baseDraft}
        onChange={onChange}
      />,
    );
    expect(screen.getByText(/Pod tolerations for Kubernetes/)).toBeInTheDocument();
    expect(screen.getByText(/Pod affinity\/anti-affinity/)).toBeInTheDocument();
  });

  it("initializes tolerations from draft when present", () => {
    const draft = {
      ...baseDraft,
      spec: {
        ...baseDraft.spec,
        tolerations: [
          { key: "gpu", operator: "Equal" as const, value: "true", effect: "NoSchedule" as const },
        ],
      },
    };
    const onChange = vi.fn();
    render(
      <PlacementSection
        draft={draft}
        onChange={onChange}
      />,
    );
    const tolEditor = screen.getAllByTestId("monaco-editor")[0];
    const content = JSON.parse((tolEditor as HTMLTextAreaElement).value);
    expect(content).toHaveLength(1);
    expect(content[0].key).toBe("gpu");
  });

  it("initializes affinity from draft when present", () => {
    const draft = {
      ...baseDraft,
      spec: {
        ...baseDraft.spec,
        affinity: {
          nodeAffinity: {
            requiredDuringSchedulingIgnoredDuringExecution: {
              nodeSelectorTerms: [{ matchExpressions: [] }],
            },
          },
        },
      },
    };
    const onChange = vi.fn();
    render(
      <PlacementSection
        draft={draft}
        onChange={onChange}
      />,
    );
    const affEditor = screen.getAllByTestId("monaco-editor")[1];
    const content = JSON.parse((affEditor as HTMLTextAreaElement).value);
    expect(content.nodeAffinity).toBeDefined();
  });

  it("valid JSON array in tolerations calls onChange with spec.tolerations", () => {
    const onChange = vi.fn();
    render(
      <PlacementSection
        draft={baseDraft}
        onChange={onChange}
      />,
    );
    const tolEditor = screen.getAllByTestId("monaco-editor")[0];
    const validJson = JSON.stringify([{ key: "tier", operator: "Equal", value: "worker" }]);
    fireEvent.change(tolEditor, { target: { value: validJson } });

    expect(onChange).toHaveBeenCalledWith(
      expect.objectContaining({
        spec: expect.objectContaining({
          tolerations: [{ key: "tier", operator: "Equal", value: "worker" }],
        }),
      }),
    );
  });

  it("valid JSON object in affinity calls onChange with spec.affinity", () => {
    const onChange = vi.fn();
    render(
      <PlacementSection
        draft={baseDraft}
        onChange={onChange}
      />,
    );
    const affEditor = screen.getAllByTestId("monaco-editor")[1];
    const validJson = JSON.stringify({ requiredDuringScheduling: {} });
    fireEvent.change(affEditor, { target: { value: validJson } });

    expect(onChange).toHaveBeenCalledWith(
      expect.objectContaining({
        spec: expect.objectContaining({
          affinity: { requiredDuringScheduling: {} },
        }),
      }),
    );
  });

  it("invalid JSON in tolerations sets an error and does not call onChange", () => {
    const onChange = vi.fn();
    const onValidityChange = vi.fn();
    render(
      <PlacementSection
        draft={baseDraft}
        onChange={onChange}
        onValidityChange={onValidityChange}
      />,
    );
    const tolEditor = screen.getAllByTestId("monaco-editor")[0];
    fireEvent.change(tolEditor, { target: { value: "not valid json" } });

    // Error message should be shown.
    expect(screen.getByText("Invalid JSON")).toBeInTheDocument();
    // onChange should NOT be called.
    expect(onChange).not.toHaveBeenCalled();
    // onValidityChange should report false.
    expect(onValidityChange).toHaveBeenCalledWith(false);
  });

  it("non-array in tolerations sets an error", () => {
    const onChange = vi.fn();
    const onValidityChange = vi.fn();
    render(
      <PlacementSection
        draft={baseDraft}
        onChange={onChange}
        onValidityChange={onValidityChange}
      />,
    );
    const tolEditor = screen.getAllByTestId("monaco-editor")[0];
    fireEvent.change(tolEditor, { target: { value: '{"key": "value"}' } });

    expect(screen.getByText("Must be a JSON array")).toBeInTheDocument();
    expect(onChange).not.toHaveBeenCalled();
    expect(onValidityChange).toHaveBeenCalledWith(false);
  });

  it("non-object in affinity sets an error", () => {
    const onChange = vi.fn();
    const onValidityChange = vi.fn();
    render(
      <PlacementSection
        draft={baseDraft}
        onChange={onChange}
        onValidityChange={onValidityChange}
      />,
    );
    const affEditor = screen.getAllByTestId("monaco-editor")[1];
    fireEvent.change(affEditor, { target: { value: '["item1", "item2"]' } });

    expect(screen.getByText("Must be a JSON object")).toBeInTheDocument();
    expect(onChange).not.toHaveBeenCalled();
  });

  it("affinity editor rejects null", () => {
    const onChange = vi.fn();
    const onValidityChange = vi.fn();
    render(
      <PlacementSection
        draft={baseDraft}
        onChange={onChange}
        onValidityChange={onValidityChange}
      />,
    );
    const affEditor = screen.getAllByTestId("monaco-editor")[1];
    fireEvent.change(affEditor, { target: { value: "null" } });

    expect(screen.getByText("Must be a JSON object")).toBeInTheDocument();
    expect(onChange).not.toHaveBeenCalled();
  });

  it("empty string clears tolerations", () => {
    const draft = {
      ...baseDraft,
      spec: {
        ...baseDraft.spec,
        tolerations: [{ key: "tier", operator: "Equal" as const, value: "worker" }],
      },
    };
    const onChange = vi.fn();
    render(
      <PlacementSection
        draft={draft}
        onChange={onChange}
      />,
    );
    const tolEditor = screen.getAllByTestId("monaco-editor")[0];
    fireEvent.change(tolEditor, { target: { value: "" } });

    expect(onChange).toHaveBeenCalledWith(
      expect.objectContaining({
        spec: expect.objectContaining({
          tolerations: undefined,
        }),
      }),
    );
  });

  it("empty string clears affinity", () => {
    const draft = {
      ...baseDraft,
      spec: {
        ...baseDraft.spec,
        affinity: { nodeAffinity: {} },
      },
    };
    const onChange = vi.fn();
    render(
      <PlacementSection
        draft={draft}
        onChange={onChange}
      />,
    );
    const affEditor = screen.getAllByTestId("monaco-editor")[1];
    fireEvent.change(affEditor, { target: { value: "" } });

    expect(onChange).toHaveBeenCalledWith(
      expect.objectContaining({
        spec: expect.objectContaining({
          affinity: undefined,
        }),
      }),
    );
  });

  it("empty array clears tolerations (undefined)", () => {
    const onChange = vi.fn();
    render(
      <PlacementSection
        draft={baseDraft}
        onChange={onChange}
      />,
    );
    const tolEditor = screen.getAllByTestId("monaco-editor")[0];
    fireEvent.change(tolEditor, { target: { value: '[{"key":"gpu","operator":"Exists"}]' } });
    fireEvent.change(tolEditor, { target: { value: "[]" } });

    const lastCall = onChange.mock.calls.at(-1)![0];
    expect(lastCall.spec.tolerations).toBeUndefined();
  });

  it("empty object clears affinity (undefined)", () => {
    const onChange = vi.fn();
    render(
      <PlacementSection
        draft={baseDraft}
        onChange={onChange}
      />,
    );
    const affEditor = screen.getAllByTestId("monaco-editor")[1];
    fireEvent.change(affEditor, { target: { value: '{"nodeAffinity":{}}' } });
    fireEvent.change(affEditor, { target: { value: "{}" } });

    const lastCall = onChange.mock.calls.at(-1)![0];
    expect(lastCall.spec.affinity).toBeUndefined();
  });

  it("reports validity=true initially and after clearing errors", () => {
    const onValidityChange = vi.fn();
    render(
      <PlacementSection
        draft={baseDraft}
        onChange={() => {}}
        onValidityChange={onValidityChange}
      />,
    );
    // Initial mount should report valid (true).
    expect(onValidityChange).toHaveBeenCalledWith(true);

    // Introduce an error.
    onValidityChange.mockClear();
    const tolEditor = screen.getAllByTestId("monaco-editor")[0];
    fireEvent.change(tolEditor, { target: { value: "not json" } });
    expect(onValidityChange).toHaveBeenCalledWith(false);

    // Clear the error.
    onValidityChange.mockClear();
    fireEvent.change(tolEditor, { target: { value: "[]" } });
    expect(onValidityChange).toHaveBeenCalledWith(true);
  });

  it("reports validity=false when either editor has an error", () => {
    const onValidityChange = vi.fn();
    render(
      <PlacementSection
        draft={baseDraft}
        onChange={() => {}}
        onValidityChange={onValidityChange}
      />,
    );
    onValidityChange.mockClear();

    const [tolEditor, affEditor] = screen.getAllByTestId("monaco-editor");

    // Tolerations error.
    fireEvent.change(tolEditor, { target: { value: "not json" } });
    expect(onValidityChange).toHaveBeenCalledWith(false);

    // Fix tolerations; affinity error.
    onValidityChange.mockClear();
    fireEvent.change(tolEditor, { target: { value: "[]" } });
    fireEvent.change(affEditor, { target: { value: "not json" } });
    expect(onValidityChange).toHaveBeenLastCalledWith(false);
  });
});
