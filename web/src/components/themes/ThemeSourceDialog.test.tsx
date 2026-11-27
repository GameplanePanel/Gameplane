// Theme source dialog (spec 043, T049): the interval helpers, and an
// unchanged edit of each source type sends its stored settings back.
import { describe, expect, it, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { ThemeSource, ThemeSourceInput } from "@/types";
import { ThemeSourceDialog, intervalToText, parseInterval } from "./ThemeSourceDialog";

const BASE: Omit<ThemeSource, "name" | "type" | "spec"> = {
  verify: null,
  credentialSecret: null,
  refreshInterval: 3600,
  enabled: true,
  builtin: false,
  lastSyncAt: null,
  lastError: null,
  themeCount: 0,
};

const gitSource: ThemeSource = {
  ...BASE,
  name: "community-git",
  type: "git",
  spec: { git: { url: "https://git.example.org/themes.git", ref: "stable", path: "themes" }, allow: ["aurora-*", "paper"] },
  credentialSecret: "git-creds",
  refreshInterval: 900,
};

const localSource: ThemeSource = {
  ...BASE,
  name: "dev-folder",
  type: "local",
  spec: { local: { path: "/srv/themes" } },
  enabled: false,
};

const keylessSource: ThemeSource = {
  ...BASE,
  name: "signed",
  type: "oci",
  spec: { oci: { url: "ghcr.io/example/themes", themes: ["aurora-night"], insecure: false } },
  verify: { keyless: { issuer: "https://token.actions.githubusercontent.com", identity: "https://github.com/example/.*" } },
};

const keySource: ThemeSource = {
  ...keylessSource,
  name: "keyed",
  verify: { key: "-----BEGIN PUBLIC KEY-----\nabc\n-----END PUBLIC KEY-----", requireTransparencyLog: true },
};

function openEdit(source: ThemeSource) {
  const onSubmit = vi.fn<(input: ThemeSourceInput) => void>();
  const onOpenChange = vi.fn<(open: boolean) => void>();
  render(
    <ThemeSourceDialog open onOpenChange={onOpenChange} source={source} busy={false} error={null} onSubmit={onSubmit} />,
  );
  return { onSubmit, onOpenChange };
}

describe("interval helpers", () => {
  it("formats seconds as hours, minutes or seconds and parses them back", () => {
    expect(intervalToText(3600)).toBe("1h");
    expect(intervalToText(900)).toBe("15m");
    expect(intervalToText(301)).toBe("301s");
    expect(parseInterval("1h")).toBe(3600);
    expect(parseInterval("15m")).toBe(900);
    expect(parseInterval("301s")).toBe(301);
  });

  it("rejects intervals under five minutes and unknown units", () => {
    expect(parseInterval("4m")).toBeNull();
    expect(parseInterval("1d")).toBeNull();
    expect(parseInterval("")).toBeNull();
  });
});

describe("ThemeSourceDialog edit", () => {
  it("prefills a git source and sends its settings back unchanged", async () => {
    const { onSubmit } = openEdit(gitSource);

    expect(screen.getByLabelText("Repository URL")).toHaveValue("https://git.example.org/themes.git");
    expect(screen.getByLabelText("Git ref")).toHaveValue("stable");
    expect(screen.getByLabelText("Path in repository")).toHaveValue("themes");
    expect(screen.getByLabelText("Credentials secret")).toHaveValue("git-creds");
    expect(screen.getByLabelText("Allow list")).toHaveValue("aurora-*, paper");
    expect(screen.getByLabelText("Refresh interval")).toHaveValue("15m");
    await userEvent.click(screen.getByRole("button", { name: "Save changes" }));

    expect(onSubmit).toHaveBeenCalledWith({
      name: "community-git",
      type: "git",
      spec: { git: { url: "https://git.example.org/themes.git", ref: "stable", path: "themes" }, allow: ["aurora-*", "paper"] },
      verify: null,
      credentialSecret: "git-creds",
      refreshInterval: 900,
      enabled: true,
    });
  });

  it("sends a local source without a credentials secret", async () => {
    const { onSubmit } = openEdit(localSource);

    expect(screen.getByLabelText("Folder path")).toHaveValue("/srv/themes");
    await userEvent.click(screen.getByRole("button", { name: "Save changes" }));

    expect(onSubmit).toHaveBeenCalledTimes(1);
    const input = onSubmit.mock.calls[0]![0];
    expect(input).toMatchObject({ name: "dev-folder", type: "local", spec: { local: { path: "/srv/themes" } }, enabled: false });
    expect(input).not.toHaveProperty("credentialSecret");
  });

  it("keeps the keyless issuer and identity of an OCI source", async () => {
    const { onSubmit } = openEdit(keylessSource);

    expect(screen.getByLabelText("Cosign issuer")).toHaveValue("https://token.actions.githubusercontent.com");
    expect(screen.getByLabelText("Cosign identity")).toHaveValue("https://github.com/example/.*");
    await userEvent.click(screen.getByRole("button", { name: "Save changes" }));

    expect(onSubmit.mock.calls[0]![0]).toMatchObject({
      type: "oci",
      verify: { keyless: { issuer: "https://token.actions.githubusercontent.com", identity: "https://github.com/example/.*" } },
    });
  });

  it("keeps a stored transparency-log requirement on an unchanged key edit", async () => {
    const { onSubmit } = openEdit(keySource);

    await userEvent.click(screen.getByRole("button", { name: "Save changes" }));

    expect(onSubmit.mock.calls[0]![0].verify).toEqual({
      key: "-----BEGIN PUBLIC KEY-----\nabc\n-----END PUBLIC KEY-----",
      requireTransparencyLog: true,
    });
  });

  it("blocks submit and explains when the interval is invalid", async () => {
    const { onSubmit } = openEdit(gitSource);

    const interval = screen.getByLabelText("Refresh interval");
    await userEvent.clear(interval);
    await userEvent.type(interval, "2m");
    await userEvent.click(screen.getByRole("button", { name: "Save changes" }));

    expect(screen.getByRole("alert")).toHaveTextContent("Fill in the required fields.");
    expect(screen.getByText("Use a duration such as 30m or 1h, at least 5m.")).toBeInTheDocument();
    expect(onSubmit).not.toHaveBeenCalled();
  });

  it("Cancel closes the dialog without submitting", async () => {
    const { onSubmit, onOpenChange } = openEdit(gitSource);

    await userEvent.click(screen.getByRole("button", { name: "Cancel" }));

    expect(onOpenChange).toHaveBeenCalledWith(false);
    expect(onSubmit).not.toHaveBeenCalled();
  });
});
