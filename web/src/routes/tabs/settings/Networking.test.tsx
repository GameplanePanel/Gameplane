import { describe, it, expect, vi } from "vitest";
import type { ReactNode } from "react";
import { screen, fireEvent } from "@testing-library/react";
import { QueryClientProvider } from "@tanstack/react-query";
import { renderWithQuery } from "@/test/render";

vi.mock("@tanstack/react-router", () => ({
  Link: ({ children, to, params, search, ...rest }: { children: ReactNode; to: string; params?: Record<string, string>; search?: Record<string, unknown> } & Record<string, unknown>) => {
    let href = to;
    // Replace $key placeholders with values from params
    if (params) {
      Object.entries(params).forEach(([key, value]) => {
        href = href.replace(`$${key}`, value);
      });
    }
    // Append search query string
    if (search && Object.keys(search).length > 0) {
      const queryParams = new URLSearchParams();
      Object.entries(search).forEach(([key, value]) => {
        if (value !== undefined && value !== null) {
          queryParams.set(key, String(value));
        }
      });
      href = `${href}?${queryParams.toString()}`;
    }
    return (
      <a href={href} {...rest}>
        {children}
      </a>
    );
  },
}));

import { NetworkingSection } from "./Networking";
import { makeServer } from "@/test/factories";

const baseDraft = makeServer();

describe("NetworkingSection", () => {
  it("changing expose persists the new value", () => {
    const onChange = vi.fn();
    renderWithQuery(<NetworkingSection draft={baseDraft} onChange={onChange} />);
    const exposeButton = screen.getByRole("button", { name: /ClusterIP/ }) as HTMLButtonElement;
    fireEvent.click(exposeButton);
    const nodePortOption = screen.getByRole("option", { name: /NodePort/ });
    fireEvent.click(nodePortOption);
    expect(onChange).toHaveBeenCalledWith(
      expect.objectContaining({
        spec: expect.objectContaining({
          networking: expect.objectContaining({ expose: "NodePort" }),
        }),
      }),
    );
  });

  it("clears networking entirely when no fields remain", () => {
    const draft = {
      ...baseDraft,
      spec: { ...baseDraft.spec, networking: { hostname: "h" } },
    };
    const onChange = vi.fn();
    renderWithQuery(<NetworkingSection draft={draft} onChange={onChange} />);
    const hostInput = screen.getAllByRole("textbox")[0];
    fireEvent.change(hostInput, { target: { value: "" } });
    const lastCall = onChange.mock.calls.at(-1)![0];
    // Empty hostname + no other fields → networking dropped to undefined.
    expect(lastCall.spec.networking).toBeUndefined();
  });

  it("renders all four expose options", () => {
    renderWithQuery(<NetworkingSection draft={baseDraft} onChange={() => {}} />);
    const exposeButton = screen.getByRole("button", { name: /ClusterIP/ });
    fireEvent.click(exposeButton);
    for (const label of ["ClusterIP", "NodePort", "LoadBalancer", "Hostport"]) {
      expect(screen.getByRole("option", { name: new RegExp(label) })).toBeInTheDocument();
    }
  });

  it("shows the IP allow-list only for LoadBalancer", () => {
    const lb = {
      ...baseDraft,
      spec: { ...baseDraft.spec, networking: { expose: "LoadBalancer" as const } },
    };
    const { client, rerender } = renderWithQuery(
      <NetworkingSection draft={lb} onChange={() => {}} />,
    );
    expect(screen.getByLabelText("LoadBalancer IP allow-list")).toBeInTheDocument();

    const np = {
      ...baseDraft,
      spec: { ...baseDraft.spec, networking: { expose: "NodePort" as const } },
    };
    rerender(
      <QueryClientProvider client={client}>
        <NetworkingSection draft={np} onChange={() => {}} />
      </QueryClientProvider>,
    );
    expect(screen.queryByLabelText("LoadBalancer IP allow-list")).not.toBeInTheDocument();
  });

  it("parses and persists sourceRanges from the allow-list", () => {
    const lb = {
      ...baseDraft,
      spec: { ...baseDraft.spec, networking: { expose: "LoadBalancer" as const } },
    };
    const onChange = vi.fn();
    renderWithQuery(<NetworkingSection draft={lb} onChange={onChange} />);
    fireEvent.change(screen.getByLabelText("LoadBalancer IP allow-list"), {
      target: { value: "203.0.113.0/24\n10.0.0.0/8" },
    });
    expect(onChange).toHaveBeenCalledWith(
      expect.objectContaining({
        spec: expect.objectContaining({
          networking: expect.objectContaining({
            sourceRanges: ["203.0.113.0/24", "10.0.0.0/8"],
          }),
        }),
      }),
    );
  });

  it("preserves existing sourceRanges when editing another field", () => {
    const draft = {
      ...baseDraft,
      spec: {
        ...baseDraft.spec,
        networking: { expose: "LoadBalancer" as const, sourceRanges: ["203.0.113.0/24"] },
      },
    };
    const onChange = vi.fn();
    renderWithQuery(<NetworkingSection draft={draft} onChange={onChange} />);
    fireEvent.change(screen.getByPlaceholderText("mc.example.com"), {
      target: { value: "mc.example.com" },
    });
    const lastCall = onChange.mock.calls.at(-1)![0];
    expect(lastCall.spec.networking.sourceRanges).toEqual(["203.0.113.0/24"]);
  });

  it("preserves an existing loadBalancerClass when editing another field", () => {
    const draft = {
      ...baseDraft,
      spec: {
        ...baseDraft.spec,
        networking: { expose: "LoadBalancer" as const, loadBalancerClass: "example.com/games" },
      },
    };
    const onChange = vi.fn();
    renderWithQuery(<NetworkingSection draft={draft} onChange={onChange} />);
    fireEvent.change(screen.getByPlaceholderText("mc.example.com"), {
      target: { value: "mc.example.com" },
    });
    const lastCall = onChange.mock.calls.at(-1)![0];
    expect(lastCall.spec.networking.loadBalancerClass).toBe("example.com/games");
  });

  describe("AddressAssignment (T044, T047, T109)", () => {
    it("displays and edits address pool and address fields", () => {
      const onChange = vi.fn();
      renderWithQuery(
        <NetworkingSection draft={baseDraft} onChange={onChange} />,
      );

      // Address pool field should exist
      const poolInput = screen.getByPlaceholderText("pool-name") as HTMLInputElement;
      expect(poolInput).toBeInTheDocument();
      expect(poolInput.value).toBe("");

      // Requested address field should exist
      const addressInput = screen.getByPlaceholderText(
        "e.g. 203.0.113.50",
      ) as HTMLInputElement;
      expect(addressInput).toBeInTheDocument();
      expect(addressInput.value).toBe("");

      // Edit address pool
      fireEvent.change(poolInput, { target: { value: "pool-us-west" } });
      expect(onChange).toHaveBeenCalledWith(
        expect.objectContaining({
          spec: expect.objectContaining({
            networking: expect.objectContaining({
              addressPool: "pool-us-west",
            }),
          }),
        }),
      );

      // Edit address
      fireEvent.change(addressInput, {
        target: { value: "203.0.113.50" },
      });
      expect(onChange).toHaveBeenCalledWith(
        expect.objectContaining({
          spec: expect.objectContaining({
            networking: expect.objectContaining({
              address: "203.0.113.50",
            }),
          }),
        }),
      );
    });

    it("re-syncs address pool and address fields when the server identity is unchanged (regression)", () => {
      const draft = {
        ...baseDraft,
        spec: {
          ...baseDraft.spec,
          networking: {
            addressPool: "pool-us-west",
            address: "203.0.113.50",
          },
        },
      };
      const onChange = vi.fn();
      const { client, rerender } = renderWithQuery(
        <NetworkingSection draft={draft} onChange={onChange} />,
      );

      expect((screen.getByPlaceholderText("pool-name") as HTMLInputElement).value).toBe(
        "pool-us-west",
      );
      expect(
        (screen.getByPlaceholderText("e.g. 203.0.113.50") as HTMLInputElement).value,
      ).toBe("203.0.113.50");

      // Same server identity (name/namespace unchanged), but addressPool and
      // address changed underneath — e.g. SettingsTab.reload() or a
      // successful save echoing back server-normalized values.
      const reloaded = {
        ...draft,
        spec: {
          ...draft.spec,
          networking: {
            addressPool: "pool-eu-central",
            address: "198.51.100.7",
          },
        },
      };
      rerender(
        <QueryClientProvider client={client}>
          <NetworkingSection draft={reloaded} onChange={onChange} />
        </QueryClientProvider>,
      );

      expect((screen.getByPlaceholderText("pool-name") as HTMLInputElement).value).toBe(
        "pool-eu-central",
      );
      expect(
        (screen.getByPlaceholderText("e.g. 203.0.113.50") as HTMLInputElement).value,
      ).toBe("198.51.100.7");

      // The real hazard CodeRabbit flagged: a later edit writing STALE values
      // back through setNet. Editing the address field calls updateAddress,
      // which writes `addressPool` from local state — so if the re-sync did not
      // happen, this would silently push "pool-us-west" back over the reloaded
      // "pool-eu-central". Edit the address field, not an unrelated one: the
      // hostname handler spreads `net` straight from props and would pass
      // whether or not the local state re-synced.
      fireEvent.change(screen.getByPlaceholderText("e.g. 203.0.113.50"), {
        target: { value: "198.51.100.9" },
      });
      const lastCall = onChange.mock.calls.at(-1)![0];
      expect(lastCall.spec.networking.addressPool).toEqual("pool-eu-central");
      expect(lastCall.spec.networking.address).toEqual("198.51.100.9");
    });

    it("preserves addressPool and address fields when editing unrelated settings (T047)", () => {
      const draft = {
        ...baseDraft,
        spec: {
          ...baseDraft.spec,
          networking: {
            addressPool: "pool-us-west",
            address: "203.0.113.50",
          },
        },
      };
      const onChange = vi.fn();
      renderWithQuery(<NetworkingSection draft={draft} onChange={onChange} />);

      // Edit hostname (unrelated field)
      fireEvent.change(screen.getByPlaceholderText("mc.example.com"), {
        target: { value: "mc.example.com" },
      });

      const lastCall = onChange.mock.calls.at(-1)![0];
      expect(lastCall.spec.networking.addressPool).toEqual("pool-us-west");
      expect(lastCall.spec.networking.address).toEqual("203.0.113.50");
    });

    it("clears address pool when set to empty string", () => {
      const draft = {
        ...baseDraft,
        spec: {
          ...baseDraft.spec,
          networking: {
            addressPool: "pool-us-west",
          },
        },
      };
      const onChange = vi.fn();
      renderWithQuery(<NetworkingSection draft={draft} onChange={onChange} />);

      const poolInput = screen.getByPlaceholderText("pool-name") as HTMLInputElement;
      fireEvent.change(poolInput, { target: { value: "" } });

      const lastCall = onChange.mock.calls.at(-1)![0];
      // Empty value should not be sent; addressPool was the only field set,
      // so clearing it drops networking to undefined entirely (same
      // convention as "clears networking entirely when no fields remain").
      expect(lastCall.spec.networking?.addressPool).toBeUndefined();
    });

    it("renders Assigned condition with green badge (T109)", () => {
      const draft = {
        ...baseDraft,
        spec: {
          ...baseDraft.spec,
          networking: { addressPool: "pool-us-west" },
        },
        status: {
          conditions: [
            {
              type: "AddressAssignment",
              status: "True",
              reason: "Assigned",
              message: "Address 172.18.255.203 assigned from pool 'pool-us-west'.",
            },
          ],
        },
      };
      renderWithQuery(<NetworkingSection draft={draft} onChange={() => {}} />);

      expect(screen.getByText("Assigned")).toBeInTheDocument();
      expect(
        screen.getByText(
          "Address 172.18.255.203 assigned from pool 'pool-us-west'.",
        ),
      ).toBeInTheDocument();
    });

    it("renders AssignmentPending condition with orange Pending badge (T109)", () => {
      const draft = {
        ...baseDraft,
        spec: {
          ...baseDraft.spec,
          networking: { addressPool: "pool-us-west" },
        },
        status: {
          conditions: [
            {
              type: "AddressAssignment",
              status: "False",
              reason: "AssignmentPending",
              message:
                "Requested address pool 'pool-us-west': the address manager has not assigned an address yet.",
            },
          ],
        },
      };
      renderWithQuery(<NetworkingSection draft={draft} onChange={() => {}} />);

      expect(screen.getByText("Pending")).toBeInTheDocument();
      expect(
        screen.getByText(
          "Requested address pool 'pool-us-west': the address manager has not assigned an address yet.",
        ),
      ).toBeInTheDocument();
    });

    it("renders ServiceNotReady condition with orange Pending badge (T109)", () => {
      const draft = {
        ...baseDraft,
        spec: {
          ...baseDraft.spec,
          networking: { addressPool: "pool-us-west" },
        },
        status: {
          conditions: [
            {
              type: "AddressAssignment",
              status: "False",
              reason: "ServiceNotReady",
              message:
                "Requested address pool 'pool-us-west': waiting for the LoadBalancer Service carrying the request to exist.",
            },
          ],
        },
      };
      renderWithQuery(<NetworkingSection draft={draft} onChange={() => {}} />);

      expect(screen.getByText("Pending")).toBeInTheDocument();
      expect(
        screen.getByText(
          "Requested address pool 'pool-us-west': waiting for the LoadBalancer Service carrying the request to exist.",
        ),
      ).toBeInTheDocument();
    });

    it("renders IgnoredForExposureMode alert when condition reason is IgnoredForExposureMode (T047)", () => {
      const draft = {
        ...baseDraft,
        spec: {
          ...baseDraft.spec,
          networking: {
            expose: "NodePort" as const,
            addressPool: "pool-us-west",
          },
        },
        status: {
          conditions: [
            {
              type: "AddressAssignment",
              status: "False",
              reason: "IgnoredForExposureMode",
              message:
                "Requested address pool 'pool-us-west' is ignored: it applies only to expose mode 'LoadBalancer', and this server is exposed as 'NodePort'.",
            },
          ],
        },
      };
      renderWithQuery(<NetworkingSection draft={draft} onChange={() => {}} />);

      expect(screen.getByText("Address preference ignored")).toBeInTheDocument();
      expect(
        screen.getByText(/Address pool and requested address only take effect/),
      ).toBeInTheDocument();
    });

    it("renders NoAddressManagerConfigured alert when condition reason is NoAddressManagerConfigured (T047)", () => {
      const draft = {
        ...baseDraft,
        spec: {
          ...baseDraft.spec,
          networking: {
            expose: "LoadBalancer" as const,
            addressPool: "pool-us-west",
          },
        },
        status: {
          conditions: [
            {
              type: "AddressAssignment",
              status: "False",
              reason: "NoAddressManagerConfigured",
              message:
                "Requested address pool 'pool-us-west' cannot be honored: this cluster has no load-balancer address manager configured, so any address the server receives comes from the default assignment policy.",
            },
          ],
        },
      };
      renderWithQuery(<NetworkingSection draft={draft} onChange={() => {}} />);

      expect(screen.getByText("No address manager configured")).toBeInTheDocument();
      expect(
        screen.getByText(
          /Your pool and address preference will be saved but never applied/,
        ),
      ).toBeInTheDocument();
    });

    it("displays current assignment when endpoint is available (T044)", () => {
      const draft = {
        ...baseDraft,
        spec: {
          ...baseDraft.spec,
          networking: { addressPool: "pool-us-west" },
        },
        status: {
          endpoints: [
            {
              name: "game",
              host: "172.18.255.203",
              port: 25565,
              pool: "pool-us-west",
            },
          ],
          conditions: [
            {
              type: "AddressAssignment",
              status: "True",
              reason: "Assigned",
              message:
                "Address 172.18.255.203 assigned from pool 'pool-us-west'.",
            },
          ],
        },
      };
      renderWithQuery(<NetworkingSection draft={draft} onChange={() => {}} />);

      expect(screen.getByText("172.18.255.203")).toBeInTheDocument();
      expect(screen.getByText("from pool 'pool-us-west'")).toBeInTheDocument();
    });

    it("shows the player address and assigned NodePort in the current assignment", () => {
      const draft = makeServer({
        spec: { networking: { expose: "NodePort" } },
        status: { endpoints: [{ name: "game", host: "play.example.net", serviceAddress: "10.96.0.12", port: 31742 }] },
      });
      renderWithQuery(<NetworkingSection draft={draft} onChange={() => {}} />);
      expect(screen.getByText("play.example.net:31742")).toBeInTheDocument();
    });

    it("preserves addressPool when editing hostname (round-trip test T047)", () => {
      const draft = {
        ...baseDraft,
        spec: {
          ...baseDraft.spec,
          networking: {
            addressPool: "pool-us-west",
            address: "203.0.113.50",
            hostname: "old-host.example.com",
          },
        },
      };
      const onChange = vi.fn();
      renderWithQuery(<NetworkingSection draft={draft} onChange={onChange} />);

      // Change hostname
      fireEvent.change(screen.getByPlaceholderText("mc.example.com"), {
        target: { value: "new-host.example.com" },
      });

      const lastCall = onChange.mock.calls.at(-1)![0];
      expect(lastCall.spec.networking).toEqual({
        addressPool: "pool-us-west",
        address: "203.0.113.50",
        hostname: "new-host.example.com",
      });
    });

    it("does not drop addressPool and address when exposing to LoadBalancer (T047)", () => {
      const draft = {
        ...baseDraft,
        spec: {
          ...baseDraft.spec,
          networking: {
            expose: "NodePort" as const,
            addressPool: "pool-us-west",
            address: "203.0.113.50",
          },
        },
      };
      const onChange = vi.fn();
      renderWithQuery(<NetworkingSection draft={draft} onChange={onChange} />);

      // Change expose to LoadBalancer
      const exposeButton = screen.getByRole("button", { name: /NodePort/ });
      fireEvent.click(exposeButton);
      const loadBalancerOption = screen.getByRole("option", { name: /LoadBalancer/ });
      fireEvent.click(loadBalancerOption);

      const lastCall = onChange.mock.calls.at(-1)![0];
      expect(lastCall.spec.networking.addressPool).toEqual("pool-us-west");
      expect(lastCall.spec.networking.address).toEqual("203.0.113.50");
    });

    it("clears networking when both addressPool and address are empty and no other fields (T047)", () => {
      const draft = {
        ...baseDraft,
        spec: {
          ...baseDraft.spec,
          networking: {
            addressPool: "pool-us-west",
            address: "203.0.113.50",
          },
        },
      };
      const onChange = vi.fn();
      renderWithQuery(<NetworkingSection draft={draft} onChange={onChange} />);

      // Clear address pool
      const poolInput = screen.getByPlaceholderText("pool-name") as HTMLInputElement;
      fireEvent.change(poolInput, { target: { value: "" } });

      // Clear address
      const addressInput = screen.getByPlaceholderText(
        "e.g. 203.0.113.50",
      ) as HTMLInputElement;
      fireEvent.change(addressInput, { target: { value: "" } });

      const lastCall = onChange.mock.calls.at(-1)![0];
      expect(lastCall.spec.networking).toBeUndefined();
    });

    it("renders AddressInUse condition with a link to the conflicting server (T044, T065, T109)", () => {
      const draft = {
        ...baseDraft,
        spec: {
          ...baseDraft.spec,
          networking: { address: "203.0.113.50" },
        },
        status: {
          conditions: [
            {
              type: "AddressAssignment",
              status: "False",
              reason: "AddressInUse",
              message:
                'Requested address "203.0.113.50" is already in use by GameServer "competing-server".',
            },
          ],
        },
      };
      renderWithQuery(<NetworkingSection draft={draft} onChange={() => {}} />);

      expect(screen.getByText("Error")).toBeInTheDocument();
      // Check that the link to the conflicting server is rendered
      const link = screen.getByRole("link", { name: 'GameServer "competing-server"' });
      expect(link).toBeInTheDocument();
      expect(link).toHaveAttribute("href", expect.stringContaining("/servers/competing-server"));
    });

    it("renders PoolNotFound condition message (T109)", () => {
      const draft = {
        ...baseDraft,
        spec: {
          ...baseDraft.spec,
          networking: { addressPool: "missing-pool" },
        },
        status: {
          conditions: [
            {
              type: "AddressAssignment",
              status: "False",
              reason: "PoolNotFound",
              message:
                "Requested address pool 'missing-pool' could not be found.",
            },
          ],
        },
      };
      renderWithQuery(<NetworkingSection draft={draft} onChange={() => {}} />);

      expect(screen.getByText("Error")).toBeInTheDocument();
      expect(
        screen.getByText("Requested address pool 'missing-pool' could not be found."),
      ).toBeInTheDocument();
    });

    it("renders AllocationFailed condition message (T109)", () => {
      const draft = {
        ...baseDraft,
        spec: {
          ...baseDraft.spec,
          networking: { addressPool: "exhausted-pool" },
        },
        status: {
          conditions: [
            {
              type: "AddressAssignment",
              status: "False",
              reason: "AllocationFailed",
              message:
                "Requested address pool 'exhausted-pool': the pool has no available addresses.",
            },
          ],
        },
      };
      renderWithQuery(<NetworkingSection draft={draft} onChange={() => {}} />);

      expect(screen.getByText("Error")).toBeInTheDocument();
      expect(
        screen.getByText(
          "Requested address pool 'exhausted-pool': the pool has no available addresses.",
        ),
      ).toBeInTheDocument();
    });
  });
});
