import { afterEach, describe, it, expect, vi } from "vitest";
import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";

const navigate = vi.hoisted(() => vi.fn());
vi.mock("@tanstack/react-router", () => ({
  useNavigate: () => navigate,
}));

import { ModuleCard } from "./ModuleCard";
import { makeCatalog } from "@/test/factories";
import { renderWithQuery } from "@/test/render";
import { getCurrentCluster, setCurrentCluster } from "@/lib/cluster";

afterEach(() => { setCurrentCluster("local"); navigate.mockClear(); });

const handlers = { onInstall: vi.fn(), onUpgrade: vi.fn(), onUninstall: vi.fn() };

describe("ModuleCard", () => {
  it("deploys an explicitly targeted module to that remote cluster", async () => {
    setCurrentCluster("other");
    renderWithQuery(<ModuleCard cluster="east" entry={makeCatalog({ installed: true, installedVersion: "1", latestVersion: "1", phase: "Ready", moduleName: "minecraft" })} {...handlers} />);
    await userEvent.click(screen.getByRole("button", { name: "Deploy to east" }));
    expect(navigate).toHaveBeenCalledWith({ to: "/servers/new", search: { template: "minecraft", cluster: "east" } });
    expect(getCurrentCluster()).toBe("other");
  });
  it("not-installed shows Install action", async () => {
    const onInstall = vi.fn();
    renderWithQuery(
      <ModuleCard
        entry={makeCatalog({ installed: false })}
        {...handlers}
        onInstall={onInstall}
      />,
    );
    expect(screen.getByText(/available/i)).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: /Install/i }));
    expect(onInstall).toHaveBeenCalled();
  });

  it("installed current modules deploy explicitly to local without changing the admin selection", async () => {
    setCurrentCluster("remote-demo");
    renderWithQuery(
      <ModuleCard
        entry={makeCatalog({
          installed: true,
          installedVersion: "1.21",
          latestVersion: "1.21",
          phase: "Ready",
          moduleName: "minecraft-vanilla",
        })}
        {...handlers}
      />,
    );
    expect(screen.getByRole("button", { name: /Deploy locally/i })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /Uninstall/i })).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /^Install$/ })).not.toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: /Deploy locally/i }));
    expect(navigate).toHaveBeenCalledWith({ to: "/servers/new", search: { template: "minecraft-vanilla", cluster: "local" } });
    expect(getCurrentCluster()).toBe("remote-demo");
  });

  it("upgrade-available shows Upgrade button", async () => {
    const onUpgrade = vi.fn();
    renderWithQuery(
      <ModuleCard
        entry={makeCatalog({
          installed: true,
          installedVersion: "1.20",
          latestVersion: "1.21",
          phase: "Ready",
          moduleName: "minecraft-vanilla",
        })}
        {...handlers}
        onUpgrade={onUpgrade}
      />,
    );
    await userEvent.click(screen.getByRole("button", { name: /Upgrade/i }));
    expect(onUpgrade).toHaveBeenCalled();
  });

  it("Failed phase shows danger pill and last-error message", () => {
    renderWithQuery(
      <ModuleCard
        entry={makeCatalog({
          installed: true,
          phase: "Failed",
          lastError: "image-pull-backoff",
        })}
        {...handlers}
      />,
    );
    expect(screen.getByText(/failed/i)).toBeInTheDocument();
    expect(screen.getByText(/image-pull-backoff/)).toBeInTheDocument();
  });

  it("VersionUnavailable Failed offers an Update instead of a dead error", async () => {
    const onUpgrade = vi.fn();
    renderWithQuery(
      <ModuleCard
        entry={makeCatalog({
          installed: true,
          phase: "Failed",
          reason: "VersionUnavailable",
          pinnedVersion: "2.0.2",
          versions: ["2.0.4"],
          latestVersion: "2.0.4",
          lastError: 'version "2.0.2" not in catalog for "7dtd" (available: [2.0.4])',
          moduleName: "seven-days-to-die",
        })}
        {...handlers}
        onUpgrade={onUpgrade}
      />,
    );
    // Actionable "update" pill, not a red "failed", and no raw error dump.
    expect(screen.getByText("update")).toBeInTheDocument();
    expect(screen.queryByText("failed")).not.toBeInTheDocument();
    expect(screen.queryByText(/not in catalog/)).not.toBeInTheDocument();
    // Explains the situation and offers the available version.
    expect(screen.getByText(/no longer in the catalog/i)).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: /Update to v2\.0\.4/i }));
    expect(onUpgrade).toHaveBeenCalled();
  });

  it("does not flash a stale error mid-re-pin (spec.version now available, status still Failed)", () => {
    // Right after clicking Update: spec.version=2.0.4 (available) but the CR
    // status hasn't caught up (still Failed with the old error). Neither the
    // raw error nor the update affordance should show.
    renderWithQuery(
      <ModuleCard
        entry={makeCatalog({
          installed: true,
          phase: "Failed",
          reason: "VersionUnavailable",
          pinnedVersion: "2.0.4",
          versions: ["2.0.4"],
          latestVersion: "2.0.4",
          lastError: 'version "2.0.2" not in catalog for "7dtd" (available: [2.0.4])',
          moduleName: "seven-days-to-die",
        })}
        {...handlers}
      />,
    );
    expect(screen.queryByText(/not in catalog/)).not.toBeInTheDocument();
    expect(screen.queryByText(/no longer in the catalog/i)).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /Update to/i })).not.toBeInTheDocument();
  });

  it("VersionUnavailable with no latestVersion does not show update pill", () => {
    renderWithQuery(
      <ModuleCard
        entry={makeCatalog({
          installed: true,
          phase: "Failed",
          reason: "VersionUnavailable",
          pinnedVersion: "2.0.2",
          versions: ["2.0.4"],
          latestVersion: undefined, // Explicitly no latest version available
          lastError: 'version "2.0.2" not in catalog',
        })}
        {...handlers}
      />,
    );
    // Should show "failed" pill, not "update"
    expect(screen.getByText("failed")).toBeInTheDocument();
    expect(screen.queryByText("update")).not.toBeInTheDocument();
  });

  it("Pulling with a leftover lastError does not show the stale error", () => {
    renderWithQuery(
      <ModuleCard
        entry={makeCatalog({
          installed: true,
          phase: "Pulling",
          reason: "VersionUnavailable",
          pinnedVersion: "2.0.4",
          versions: ["2.0.4"],
          latestVersion: "2.0.4",
          lastError: 'version "2.0.2" not in catalog for "7dtd" (available: [2.0.4])',
        })}
        {...handlers}
      />,
    );
    expect(screen.queryByText(/not in catalog/)).not.toBeInTheDocument();
  });

  it("in-flight (phase != Ready) disables Uninstall", () => {
    renderWithQuery(
      <ModuleCard
        entry={makeCatalog({
          installed: true,
          phase: "Pulling",
        })}
        {...handlers}
      />,
    );
    expect(screen.getByRole("button", { name: /Uninstall/i })).toBeDisabled();
  });

  it("Failed module (terminal state) enables Uninstall", async () => {
    const onUninstall = vi.fn();
    renderWithQuery(
      <ModuleCard
        entry={makeCatalog({
          installed: true,
          phase: "Failed",
          lastError: "pull-error",
        })}
        {...handlers}
        onUninstall={onUninstall}
      />,
    );
    const btn = screen.getByRole("button", { name: /Uninstall/i });
    expect(btn).toBeEnabled();
    await userEvent.click(btn);
    expect(onUninstall).toHaveBeenCalled();
  });

  it("multiple sources renders 'N sources'", () => {
    renderWithQuery(
      <ModuleCard
        entry={makeCatalog({ sources: [{ name: "a", type: "oci" }, { name: "b", type: "git" }, { name: "c", type: "upload" }] })}
        {...handlers}
      />,
    );
    expect(screen.getByText("3 sources")).toBeInTheDocument();
  });

  it("enforced verification renders a solid 'verified' badge (keyed)", () => {
    renderWithQuery(
      <ModuleCard
        entry={makeCatalog({})}
        verify={{ mode: "keyed", enforced: true, mixed: false }}
        {...handlers}
      />,
    );
    const badge = screen.getByText("verified").closest('[data-slot="chip"]');
    expect(badge).toBeInTheDocument();
    expect(badge).toHaveAttribute("data-color", "success");
    expect(badge).toHaveAttribute("title", "signature verified");
    expect(screen.queryByText("policy")).not.toBeInTheDocument();
  });

  it("enforced keyless verification keeps the 'verified' label, with keyless in the tooltip", () => {
    renderWithQuery(
      <ModuleCard
        entry={makeCatalog({})}
        verify={{ mode: "keyless", enforced: true, mixed: false }}
        {...handlers}
      />,
    );
    expect(screen.getByText("verified")).toBeInTheDocument();
    expect(screen.getByTitle(/keyless/i)).toBeInTheDocument();
  });

  it("policy-declared (not installed) renders a softer outline 'policy' badge", () => {
    renderWithQuery(
      <ModuleCard
        entry={makeCatalog({})}
        verify={{ mode: "keyed", enforced: false, mixed: false }}
        {...handlers}
      />,
    );
    const badge = screen.getByText("policy").closest('[data-slot="chip"]');
    expect(badge).toBeInTheDocument();
    expect(badge).toHaveAttribute("data-color", "success");
    expect(screen.queryByText("verified")).not.toBeInTheDocument();
  });

  it("keyless policy carries keyless in the tooltip", () => {
    renderWithQuery(
      <ModuleCard
        entry={makeCatalog({})}
        verify={{ mode: "keyless", enforced: false, mixed: false }}
        {...handlers}
      />,
    );
    expect(screen.getByText("policy")).toBeInTheDocument();
    expect(screen.getByTitle(/keyless/i)).toBeInTheDocument();
  });

  it("mixed candidate sources suppress the badge (no over-claim)", () => {
    for (const mode of ["keyless", "keyed"] as const) {
      const { unmount } = renderWithQuery(
        <ModuleCard
          entry={makeCatalog({})}
          verify={{ mode, enforced: false, mixed: true }}
          {...handlers}
        />,
      );
      expect(screen.queryByText("verified")).not.toBeInTheDocument();
      expect(screen.queryByText("policy")).not.toBeInTheDocument();
      unmount();
    }
  });

  it("renders no verify badge when unsigned", () => {
    renderWithQuery(
      <ModuleCard
        entry={makeCatalog({})}
        verify={{ mode: "none", enforced: false, mixed: false }}
        {...handlers}
      />,
    );
    expect(screen.queryByText("verified")).not.toBeInTheDocument();
    expect(screen.queryByText("policy")).not.toBeInTheDocument();
  });

  it("shows the bundle digest and rollback target", () => {
    renderWithQuery(
      <ModuleCard
        entry={makeCatalog({
          installed: true,
          installedVersion: "1.21",
          phase: "Ready",
          appliedDigest: "sha256:a1b2c3d4e5f6a7b8c9d0e1f2",
          previousVersion: "1.20",
        })}
        {...handlers}
      />,
    );
    expect(screen.getByText(/sha256:a1b2c3d4/)).toBeInTheDocument();
    expect(screen.getByText(/rollback target/)).toBeInTheDocument();
  });
});
