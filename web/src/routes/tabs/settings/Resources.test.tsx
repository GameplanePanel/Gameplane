import { describe, it, expect, vi } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { ResourcesSection } from "./Resources";
import { makeServer } from "@/test/factories";
import type { ResourceRequirements } from "@/types";

function withResources(resources: ResourceRequirements) {
  const server = makeServer();
  return { ...server, spec: { ...server.spec, resources } };
}

const guaranteed: ResourceRequirements = {
  requests: { cpu: "2", memory: "4Gi" },
  limits: { cpu: "2", memory: "4Gi" },
};

const burstable: ResourceRequirements = {
  requests: { cpu: "500m", memory: "2Gi" },
  limits: { cpu: "2", memory: "4Gi" },
};

function commit(label: string, value: string) {
  const input = screen.getByLabelText(label);
  fireEvent.change(input, { target: { value } });
  fireEvent.blur(input);
}

describe("ResourcesSection", () => {
  it("renders default CPU and memory values when no resources set", () => {
    render(<ResourcesSection draft={makeServer()} onChange={() => {}} />);
    // Defaults are 2 CPU / 4 Gi.
    expect(screen.getByLabelText("CPU cores value")).toHaveValue(2);
    expect(screen.getByLabelText("Memory (GiB) value")).toHaveValue(4);
  });

  it("changing CPU slider updates draft to a quantity", () => {
    const onChange = vi.fn();
    render(<ResourcesSection draft={makeServer()} onChange={onChange} />);
    const cpuInput = screen.getByLabelText("CPU cores value");
    fireEvent.change(cpuInput, { target: { value: "4" } });
    fireEvent.blur(cpuInput);
    const lastCall = onChange.mock.calls.at(-1)![0];
    expect(lastCall.spec.resources.requests.cpu).toBe("4");
    expect(lastCall.spec.resources.limits.cpu).toBe("4");
  });

  it("fractional CPU produces a milli-string", () => {
    const onChange = vi.fn();
    render(<ResourcesSection draft={makeServer()} onChange={onChange} />);
    const cpuInput = screen.getByLabelText("CPU cores value");
    fireEvent.change(cpuInput, { target: { value: "0.5" } });
    fireEvent.blur(cpuInput);
    const lastCall = onChange.mock.calls.at(-1)![0];
    expect(lastCall.spec.resources.requests.cpu).toBe("500m");
  });

  it("memory slider sets memory limits", () => {
    const onChange = vi.fn();
    render(<ResourcesSection draft={makeServer()} onChange={onChange} />);
    const memInput = screen.getByLabelText("Memory (GiB) value");
    fireEvent.change(memInput, { target: { value: "8" } });
    fireEvent.blur(memInput);
    const lastCall = onChange.mock.calls.at(-1)![0];
    expect(lastCall.spec.resources.requests.memory).toBe("8Gi");
  });

  it("rejects an invalid storage size with an error message", () => {
    const onChange = vi.fn();
    render(
      <ResourcesSection
        draft={{
          ...makeServer(),
          spec: { ...makeServer().spec, storage: { size: "10Pi-bad" } },
        }}
        onChange={onChange}
      />,
    );
    expect(screen.getByText(/Invalid quantity/i)).toBeInTheDocument();
  });

  it("typing valid storage sets spec.storage.size", () => {
    const onChange = vi.fn();
    render(<ResourcesSection draft={makeServer()} onChange={onChange} />);
    const sizeInput = screen.getByPlaceholderText("10Gi");
    fireEvent.change(sizeInput, { target: { value: "20Gi" } });
    const lastCall = onChange.mock.calls.at(-1)![0];
    expect(lastCall.spec.storage.size).toBe("20Gi");
  });

  it("clearing storage size leaves storage undefined", () => {
    const onChange = vi.fn();
    render(
      <ResourcesSection
        draft={{
          ...makeServer(),
          spec: { ...makeServer().spec, storage: { size: "5Gi" } },
        }}
        onChange={onChange}
      />,
    );
    const sizeInput = screen.getByPlaceholderText("10Gi");
    fireEvent.change(sizeInput, { target: { value: "" } });
    const lastCall = onChange.mock.calls.at(-1)![0];
    expect(lastCall.spec.storage).toBeUndefined();
  });

  it("setting storageClassName", () => {
    const onChange = vi.fn();
    render(<ResourcesSection draft={makeServer()} onChange={onChange} />);
    const scInput = screen.getByPlaceholderText("fast-ssd");
    fireEvent.change(scInput, { target: { value: "fast-nvme" } });
    const lastCall = onChange.mock.calls.at(-1)![0];
    expect(lastCall.spec.storage.storageClassName).toBe("fast-nvme");
  });

  it("clearing storageClassName leaves storage undefined if no size", () => {
    const onChange = vi.fn();
    render(
      <ResourcesSection
        draft={{
          ...makeServer(),
          spec: { ...makeServer().spec, storage: { storageClassName: "fast-ssd" } },
        }}
        onChange={onChange}
      />,
    );
    const scInput = screen.getByPlaceholderText("fast-ssd");
    fireEvent.change(scInput, { target: { value: "" } });
    const lastCall = onChange.mock.calls.at(-1)![0];
    expect(lastCall.spec.storage).toBeUndefined();
  });

  it("preserves storage size when setting storageClassName", () => {
    const onChange = vi.fn();
    render(
      <ResourcesSection
        draft={{
          ...makeServer(),
          spec: { ...makeServer().spec, storage: { size: "20Gi" } },
        }}
        onChange={onChange}
      />,
    );
    const scInput = screen.getByPlaceholderText("fast-ssd");
    fireEvent.change(scInput, { target: { value: "fast-nvme" } });
    const lastCall = onChange.mock.calls.at(-1)![0];
    expect(lastCall.spec.storage.size).toBe("20Gi");
    expect(lastCall.spec.storage.storageClassName).toBe("fast-nvme");
  });

  it("renders storage section when storage already set", () => {
    render(
      <ResourcesSection
        draft={{
          ...makeServer(),
          spec: {
            ...makeServer().spec,
            storage: { size: "50Gi", storageClassName: "fast-ssd", mountPath: "/data" },
          },
        }}
        onChange={() => {}}
      />,
    );
    expect(screen.getByDisplayValue("50Gi")).toBeInTheDocument();
    expect(screen.getByDisplayValue("fast-ssd")).toBeInTheDocument();
  });

  it("CPU slider to 0 clamps to the 100m floor", () => {
    const onChange = vi.fn();
    render(<ResourcesSection draft={makeServer()} onChange={onChange} />);
    const cpuInput = screen.getByLabelText("CPU cores value");
    // ResourceInput's CPU_DEFAULTS floors the slider at 0.1 cores (100m) —
    // a raw DOM value of "0" (below the slider's own min) still gets
    // clamped there by emitBase, it never reaches true zero.
    fireEvent.change(cpuInput, { target: { value: "0" } });
    fireEvent.blur(cpuInput);
    const lastCall = onChange.mock.calls.at(-1)![0];
    expect(lastCall.spec.resources.requests.cpu).toBe("100m");
  });

  it("memory slider min value", () => {
    const onChange = vi.fn();
    render(<ResourcesSection draft={makeServer()} onChange={onChange} />);
    const memInput = screen.getByLabelText("Memory (GiB) value");
    fireEvent.change(memInput, { target: { value: "1" } });
    fireEvent.blur(memInput);
    const lastCall = onChange.mock.calls.at(-1)![0];
    expect(lastCall.spec.resources.requests.memory).toBe("1Gi");
  });

  it("preserves storage config when updating resources", () => {
    const onChange = vi.fn();
    render(
      <ResourcesSection
        draft={{
          ...makeServer(),
          spec: {
            ...makeServer().spec,
            storage: { size: "10Gi" },
          },
        }}
        onChange={onChange}
      />,
    );
    const cpuInput = screen.getByLabelText("CPU cores value");
    fireEvent.change(cpuInput, { target: { value: "8" } });
    fireEvent.blur(cpuInput);
    const lastCall = onChange.mock.calls.at(-1)![0];
    expect(lastCall.spec.storage.size).toBe("10Gi");
    expect(lastCall.spec.resources.requests.cpu).toBe("8");
  });

  describe("reserve less than the limit", () => {
    const switchName = { name: "Reserve less than the limit" };

    it("is off by default and hides the reserved fields", () => {
      render(<ResourcesSection draft={withResources(guaranteed)} onChange={() => {}} />);
      expect(screen.getByRole("switch", switchName)).not.toBeChecked();
      expect(screen.queryByLabelText("Reserved CPU value")).not.toBeInTheDocument();
      expect(screen.queryByLabelText("Reserved memory (GiB) value")).not.toBeInTheDocument();
    });

    it("switching on shows the reserved fields at the limits without changing the draft", async () => {
      const onChange = vi.fn();
      render(<ResourcesSection draft={withResources(guaranteed)} onChange={onChange} />);
      await userEvent.click(screen.getByRole("switch", switchName));
      expect(screen.getByRole("switch", switchName)).toBeChecked();
      expect(screen.getByLabelText("Reserved CPU value")).toHaveValue(2);
      expect(screen.getByLabelText("Reserved memory (GiB) value")).toHaveValue(4);
      expect(screen.getByText("Limit: the most CPU this server can use.")).toBeInTheDocument();
      expect(screen.getByText("Limit: the most memory this server can use.")).toBeInTheDocument();
      expect(onChange).not.toHaveBeenCalled();
    });

    it("lowering the reserved CPU writes requests below the limits", async () => {
      const onChange = vi.fn();
      render(<ResourcesSection draft={withResources(guaranteed)} onChange={onChange} />);
      await userEvent.click(screen.getByRole("switch", switchName));
      commit("Reserved CPU value", "0.5");
      expect(onChange.mock.calls.at(-1)![0].spec.resources).toEqual({
        requests: { cpu: "500m", memory: "4Gi" },
        limits: { cpu: "2", memory: "4Gi" },
      });
    });

    it("a reserved memory of 0 reserves nothing", async () => {
      const onChange = vi.fn();
      render(<ResourcesSection draft={withResources(guaranteed)} onChange={onChange} />);
      await userEvent.click(screen.getByRole("switch", switchName));
      commit("Reserved memory (GiB) value", "0");
      expect(onChange.mock.calls.at(-1)![0].spec.resources).toEqual({
        requests: { cpu: "2", memory: "0Gi" },
        limits: { cpu: "2", memory: "4Gi" },
      });
    });

    it("opens switched on when the saved requests are below the limits", () => {
      render(<ResourcesSection draft={withResources(burstable)} onChange={() => {}} />);
      expect(screen.getByRole("switch", switchName)).toBeChecked();
      // 500m shows in its natural mCPU unit.
      expect(screen.getByLabelText("Reserved CPU value")).toHaveValue(500);
      expect(screen.getByLabelText("Reserved memory (GiB) value")).toHaveValue(2);
      expect(screen.getByLabelText("CPU cores value")).toHaveValue(2);
      expect(screen.getByLabelText("Memory (GiB) value")).toHaveValue(4);
    });

    it("lowering a limit below its reservation lowers the reservation too", () => {
      const onChange = vi.fn();
      const draft = withResources({
        requests: { cpu: "1500m", memory: "3Gi" },
        limits: { cpu: "4", memory: "4Gi" },
      });
      const { unmount } = render(<ResourcesSection draft={draft} onChange={onChange} />);
      commit("CPU cores value", "1");
      expect(onChange.mock.calls.at(-1)![0].spec.resources).toEqual({
        requests: { cpu: "1", memory: "3Gi" },
        limits: { cpu: "1", memory: "4Gi" },
      });
      unmount();

      render(<ResourcesSection draft={draft} onChange={onChange} />);
      commit("Memory (GiB) value", "2");
      expect(onChange.mock.calls.at(-1)![0].spec.resources).toEqual({
        requests: { cpu: "1500m", memory: "2Gi" },
        limits: { cpu: "4", memory: "2Gi" },
      });
    });

    it("raising a limit keeps the reservation", () => {
      const onChange = vi.fn();
      const { unmount } = render(<ResourcesSection draft={withResources(burstable)} onChange={onChange} />);
      commit("CPU cores value", "4");
      expect(onChange.mock.calls.at(-1)![0].spec.resources).toEqual({
        requests: { cpu: "500m", memory: "2Gi" },
        limits: { cpu: "4", memory: "4Gi" },
      });
      unmount();

      render(<ResourcesSection draft={withResources(burstable)} onChange={onChange} />);
      commit("Memory (GiB) value", "8");
      expect(onChange.mock.calls.at(-1)![0].spec.resources).toEqual({
        requests: { cpu: "500m", memory: "2Gi" },
        limits: { cpu: "2", memory: "8Gi" },
      });
    });

    it("switching off puts the requests back on the limits", async () => {
      const onChange = vi.fn();
      render(<ResourcesSection draft={withResources(burstable)} onChange={onChange} />);
      await userEvent.click(screen.getByRole("switch", switchName));
      expect(onChange.mock.calls.at(-1)![0].spec.resources).toEqual(guaranteed);
    });

    it("switching off before lowering anything leaves the draft alone", async () => {
      const onChange = vi.fn();
      render(<ResourcesSection draft={withResources(guaranteed)} onChange={onChange} />);
      await userEvent.click(screen.getByRole("switch", switchName));
      await userEvent.click(screen.getByRole("switch", switchName));
      expect(screen.getByRole("switch", switchName)).not.toBeChecked();
      expect(screen.queryByLabelText("Reserved CPU value")).not.toBeInTheDocument();
      expect(onChange).not.toHaveBeenCalled();
    });
  });
});
