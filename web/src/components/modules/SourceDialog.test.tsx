import { describe, it, expect, vi } from "vitest";
import { screen, fireEvent, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { renderWithQuery } from "@/test/render";
import { makeModuleSource } from "@/test/factories";
import type { ModuleSourceSpec } from "@/types";
import { APIError } from "@/lib/api";
import { SourceDialog, specFrom } from "./SourceDialog";

function renderDialog(props: Partial<Parameters<typeof SourceDialog>[0]> = {}) {
  const onConfirm = vi.fn();
  renderWithQuery(
    <SourceDialog
      open
      onOpenChange={() => undefined}
      source={null}
      onConfirm={onConfirm}
      {...props}
    />,
  );
  return onConfirm;
}

async function selectByAriaLabel(ariaLabel: string, optionText: string) {
  const user = userEvent.setup();
  const trigger = document.querySelector(`button[aria-label="${ariaLabel}"]`);
  if (!trigger) throw new Error(`Select trigger with aria-label="${ariaLabel}" not found`);
  await user.click(trigger);
  const option = screen.getByRole("option", { name: new RegExp(optionText, "i") });
  await user.click(option);
}

async function selectType(value: "oci" | "git" | "http" | "local" | "upload") {
  const labelMap: Record<string, string> = {
    oci: "OCI registry",
    git: "Git repository",
    http: "HTTP archive",
    local: "Local directory",
    upload: "Uploaded bundles",
  };
  await selectByAriaLabel("Type", labelMap[value]);
}

async function selectVerifyMode(value: "none" | "keyed" | "keyless") {
  const labelMap: Record<string, string> = {
    none: "None",
    keyed: "Keyed",
    keyless: "Keyless",
  };
  await selectByAriaLabel("Signature verification", labelMap[value]);
}

describe("specFrom", () => {
  const base = {
    type: "oci" as const,
    url: "",
    modules: "",
    secretName: "",
    insecure: false,
    ref: "",
    subPath: "",
    path: "",
    allow: "",
    refreshInterval: "",
    verifyMode: "none" as const,
    verifyKeySecret: "",
    verifyIssuer: "",
    verifyIdentity: "",
  };

  it("builds an oci spec", () => {
    const spec: ModuleSourceSpec = specFrom({
      ...base,
      url: "ghcr.io/x",
      modules: "minecraft-java, valheim",
      secretName: "creds",
      insecure: true,
      allow: "minecraft-*",
      refreshInterval: "30m",
    });
    expect(spec).toEqual({
      type: "oci",
      oci: {
        url: "ghcr.io/x",
        modules: [{ name: "minecraft-java" }, { name: "valheim" }],
        insecure: true,
        pullSecretRef: { name: "creds" },
      },
      allow: ["minecraft-*"],
      refreshInterval: "30m",
    });
  });

  it("builds a git spec without empty optionals", () => {
    const spec = specFrom({ ...base, type: "git", url: "https://g/x.git", ref: "stable" });
    expect(spec).toEqual({ type: "git", git: { url: "https://g/x.git", ref: "stable" } });
  });

  it("builds http, local and upload specs", () => {
    expect(specFrom({ ...base, type: "http", url: "https://e/m.zip" })).toEqual({
      type: "http",
      http: { url: "https://e/m.zip" },
    });
    expect(specFrom({ ...base, type: "local", path: "bundles" })).toEqual({
      type: "local",
      local: { path: "bundles" },
    });
    expect(specFrom({ ...base, type: "local" })).toEqual({ type: "local", local: {} });
    expect(specFrom({ ...base, type: "upload" })).toEqual({ type: "upload" });
  });

  it("emits keyed and keyless verify only for oci sources", () => {
    const keyed = specFrom({
      ...base,
      url: "ghcr.io/x",
      modules: "mc",
      verifyMode: "keyed",
      verifyKeySecret: "cosign-pub",
    });
    expect(keyed.verify).toEqual({ key: { name: "cosign-pub" } });

    const keyless = specFrom({
      ...base,
      url: "ghcr.io/x",
      modules: "mc",
      verifyMode: "keyless",
      verifyIssuer: "https://issuer",
      verifyIdentity: "id@example.com",
    });
    expect(keyless.verify).toEqual({
      keyless: { issuer: "https://issuer", identity: "id@example.com" },
    });

    // none → no verify block.
    expect(specFrom({ ...base, url: "ghcr.io/x", modules: "mc" }).verify).toBeUndefined();

    // A non-oci type must never carry verify, even if the form has it set.
    const git = specFrom({
      ...base,
      type: "git",
      url: "https://g/x.git",
      verifyMode: "keyless",
      verifyIssuer: "https://issuer",
      verifyIdentity: "id@example.com",
    });
    expect(git.verify).toBeUndefined();
  });
});

describe("SourceDialog", () => {
  it("renders oci fields by default and switches per type", async () => {
    renderDialog();
    expect(screen.getByText("Registry URL")).toBeInTheDocument();
    expect(screen.getByText("Modules")).toBeInTheDocument();

    // Switch to upload: no url fields, just the explainer.
    await selectType("upload");
    await screen.findByText(/Indexes bundles uploaded/);
    expect(screen.queryByText("Registry URL")).not.toBeInTheDocument();
  });

  it("renders the git, http and local field sets", async () => {
    renderDialog();

    await selectType("git");
    await screen.findByText("Clone URL");
    expect(screen.getByText("Ref")).toBeInTheDocument();
    expect(screen.getByText("Subdirectory")).toBeInTheDocument();

    await selectType("http");
    await screen.findByText("Archive URL");
    expect(screen.getByText(/Allow plain HTTP/)).toBeInTheDocument();

    await selectType("local");
    await screen.findByText("Path");
    expect(screen.queryByText("Archive URL")).not.toBeInTheDocument();
  });

  it("prefills edit forms for oci and local sources", () => {
    const oci = makeModuleSource({
      metadata: { name: "upstream" },
      spec: {
        type: "oci",
        oci: {
          url: "ghcr.io/x",
          modules: [{ name: "mc" }, { name: "valheim" }],
          insecure: true,
          pullSecretRef: { name: "creds" },
        },
        allow: ["mc"],
        refreshInterval: "15m",
      },
    });
    const { unmount } = renderWithQuery(
      <SourceDialog open onOpenChange={() => undefined} source={oci} onConfirm={vi.fn()} />,
    );
    expect(screen.getByDisplayValue("ghcr.io/x")).toBeInTheDocument();
    expect(screen.getByDisplayValue("mc, valheim")).toBeInTheDocument();
    expect(screen.getByDisplayValue("creds")).toBeInTheDocument();
    expect(screen.getByDisplayValue("15m")).toBeInTheDocument();
    unmount();

    const local = makeModuleSource({
      metadata: { name: "disk" },
      spec: { type: "local", local: { path: "bundles" } },
    });
    renderWithQuery(
      <SourceDialog open onOpenChange={() => undefined} source={local} onConfirm={vi.fn()} />,
    );
    expect(screen.getByDisplayValue("bundles")).toBeInTheDocument();
  });

  it("validates before submitting", async () => {
    const onConfirm = renderDialog();
    const submitBtn = screen.getByRole("button", { name: "Add source" });
    expect(submitBtn).toBeDisabled();

    fireEvent.change(screen.getByPlaceholderText("community"), { target: { value: "up" } });
    expect(submitBtn).toBeDisabled();

    fireEvent.change(screen.getByPlaceholderText("ghcr.io/gameplanepanel/gameplane-modules"), {
      target: { value: "ghcr.io/x" },
    });
    expect(submitBtn).toBeDisabled();
    expect(onConfirm).not.toHaveBeenCalled();
  });

  it("submits the typed payload", async () => {
    const onConfirm = renderDialog();
    fireEvent.change(screen.getByPlaceholderText("community"), { target: { value: "upstream" } });
    fireEvent.change(screen.getByPlaceholderText("ghcr.io/gameplanepanel/gameplane-modules"), {
      target: { value: "ghcr.io/x" },
    });
    fireEvent.change(screen.getByPlaceholderText("minecraft-java, valheim"), {
      target: { value: "minecraft-java" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Add source" }));
    await waitFor(() => expect(onConfirm).toHaveBeenCalled());
    expect(onConfirm).toHaveBeenCalledWith({
      name: "upstream",
      spec: {
        type: "oci",
        oci: { url: "ghcr.io/x", modules: [{ name: "minecraft-java" }] },
      },
    });
  });

  it("enables submit and confirms once a valid oci source is filled", async () => {
    const onConfirm = renderDialog();
    const submitBtn = screen.getByRole("button", { name: "Add source" });
    expect(submitBtn).toBeDisabled();

    fireEvent.change(screen.getByPlaceholderText("community"), { target: { value: "upstream" } });
    fireEvent.change(screen.getByPlaceholderText("ghcr.io/gameplanepanel/gameplane-modules"), {
      target: { value: "ghcr.io/x" },
    });
    fireEvent.change(screen.getByPlaceholderText("minecraft-java, valheim"), {
      target: { value: "minecraft-java" },
    });
    expect(submitBtn).not.toBeDisabled();

    fireEvent.click(submitBtn);
    await waitFor(() => expect(onConfirm).toHaveBeenCalled());
  });

  it("reveals keyless verify fields and validates them", async () => {
    const onConfirm = renderDialog();
    fireEvent.change(screen.getByPlaceholderText("community"), { target: { value: "upstream" } });
    fireEvent.change(screen.getByPlaceholderText("ghcr.io/gameplanepanel/gameplane-modules"), {
      target: { value: "ghcr.io/x" },
    });
    fireEvent.change(screen.getByPlaceholderText("minecraft-java, valheim"), {
      target: { value: "minecraft-java" },
    });

    const submitBtn = screen.getByRole("button", { name: "Add source" });
    expect(submitBtn).not.toBeDisabled();

    // Keyless reveals issuer + identity inputs.
    await selectVerifyMode("keyless");
    await screen.findByText("OIDC issuer");
    const identity = screen.getByPlaceholderText(/release.yml/);
    fireEvent.change(screen.getByPlaceholderText("https://token.actions.githubusercontent.com"), {
      target: { value: "https://issuer" },
    });

    // Blank identity blocks submit.
    expect(submitBtn).toBeDisabled();
    expect(onConfirm).not.toHaveBeenCalled();

    // Filling it submits the verify block.
    fireEvent.change(identity, { target: { value: "id@example.com" } });
    expect(submitBtn).not.toBeDisabled();
    fireEvent.click(submitBtn);
    await waitFor(() => expect(onConfirm).toHaveBeenCalled());
    expect(onConfirm).toHaveBeenCalledWith({
      name: "upstream",
      spec: {
        type: "oci",
        oci: { url: "ghcr.io/x", modules: [{ name: "minecraft-java" }] },
        verify: { keyless: { issuer: "https://issuer", identity: "id@example.com" } },
      },
    });
  });

  it("prefills the verify config when editing a signed oci source", () => {
    const signed = makeModuleSource({
      metadata: { name: "upstream" },
      spec: {
        type: "oci",
        oci: { url: "ghcr.io/x", modules: [{ name: "mc" }] },
        verify: { keyless: { issuer: "https://issuer", identity: "id@example.com" } },
      },
    });
    renderWithQuery(
      <SourceDialog open onOpenChange={() => undefined} source={signed} onConfirm={vi.fn()} />,
    );
    expect(screen.getByDisplayValue("https://issuer")).toBeInTheDocument();
    expect(screen.getByDisplayValue("id@example.com")).toBeInTheDocument();
  });

  it("prefills when editing and surfaces API errors", async () => {
    const source = makeModuleSource({
      metadata: { name: "community" },
      spec: { type: "git", git: { url: "https://g/x.git", ref: "main" } },
    });
    const onConfirm = vi.fn().mockRejectedValue(new APIError(409, "still used by installed module(s)"));
    renderWithQuery(
      <SourceDialog open onOpenChange={() => undefined} source={source} onConfirm={onConfirm} />,
    );
    // Name is fixed when editing; git fields are prefilled.
    expect(screen.queryByPlaceholderText("community")).not.toBeInTheDocument();
    expect(screen.getByDisplayValue("https://g/x.git")).toBeInTheDocument();
    expect(screen.getByDisplayValue("main")).toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: "Save" }));
    await screen.findByText(/still used by installed module/);
  });

  it("reveals keyed verify fields and validates them", async () => {
    const onConfirm = renderDialog();
    fireEvent.change(screen.getByPlaceholderText("community"), { target: { value: "signed-upstream" } });
    fireEvent.change(screen.getByPlaceholderText("ghcr.io/gameplanepanel/gameplane-modules"), {
      target: { value: "ghcr.io/x" },
    });
    fireEvent.change(screen.getByPlaceholderText("minecraft-java, valheim"), {
      target: { value: "minecraft-java" },
    });

    const submitBtn = screen.getByRole("button", { name: "Add source" });
    expect(submitBtn).not.toBeDisabled();

    // Switch to keyed verify mode.
    await selectVerifyMode("keyed");
    await screen.findByText("Public key secret");

    // Blank secret name blocks submit.
    expect(submitBtn).toBeDisabled();
    expect(onConfirm).not.toHaveBeenCalled();

    // Filling it submits the verify block.
    fireEvent.change(screen.getByPlaceholderText("cosign-pub"), {
      target: { value: "my-cosign-key" },
    });
    expect(submitBtn).not.toBeDisabled();
    fireEvent.click(submitBtn);
    await waitFor(() => expect(onConfirm).toHaveBeenCalled());
    expect(onConfirm).toHaveBeenCalledWith({
      name: "signed-upstream",
      spec: {
        type: "oci",
        oci: { url: "ghcr.io/x", modules: [{ name: "minecraft-java" }] },
        verify: { key: { name: "my-cosign-key" } },
      },
    });
  });

  it("prefills keyed verify when editing a signed source", () => {
    const signed = makeModuleSource({
      metadata: { name: "upstream" },
      spec: {
        type: "oci",
        oci: { url: "ghcr.io/x", modules: [{ name: "mc" }] },
        verify: { key: { name: "cosign-pub" } },
      },
    });
    renderWithQuery(
      <SourceDialog open onOpenChange={() => undefined} source={signed} onConfirm={vi.fn()} />,
    );
    expect(screen.getByDisplayValue("cosign-pub")).toBeInTheDocument();
  });

  it("validates git source requires URL", async () => {
    const onConfirm = renderDialog();
    fireEvent.change(screen.getByPlaceholderText("community"), { target: { value: "git-src" } });
    await selectType("git");
    expect(screen.getByRole("button", { name: "Add source" })).toBeDisabled();
    expect(onConfirm).not.toHaveBeenCalled();
  });

  it("validates http source requires URL", async () => {
    const onConfirm = renderDialog();
    fireEvent.change(screen.getByPlaceholderText("community"), { target: { value: "http-src" } });
    await selectType("http");
    expect(screen.getByRole("button", { name: "Add source" })).toBeDisabled();
    expect(onConfirm).not.toHaveBeenCalled();
  });

  it("allows local and upload sources without URL", async () => {
    let onConfirm = vi.fn();
    let rendered = renderWithQuery(
      <SourceDialog open onOpenChange={() => undefined} source={null} onConfirm={onConfirm} />,
    );
    fireEvent.change(screen.getByPlaceholderText("community"), { target: { value: "local-src" } });
    await selectType("local");
    fireEvent.click(screen.getByRole("button", { name: "Add source" }));
    await waitFor(() => expect(onConfirm).toHaveBeenCalledTimes(1));

    // Unmount before re-rendering — otherwise both instances' "community"
    // placeholder inputs are in the DOM at once and getByPlaceholderText
    // throws on the ambiguity.
    rendered.unmount();

    onConfirm = vi.fn();
    renderWithQuery(
      <SourceDialog open onOpenChange={() => undefined} source={null} onConfirm={onConfirm} />,
    );
    fireEvent.change(screen.getByPlaceholderText("community"), { target: { value: "upload-src" } });
    await selectType("upload");
    fireEvent.click(screen.getByRole("button", { name: "Add source" }));
    await waitFor(() => expect(onConfirm).toHaveBeenCalledTimes(1));
  });

  it("prefills git source with subPath when editing", () => {
    const git = makeModuleSource({
      metadata: { name: "community" },
      spec: { type: "git", git: { url: "https://g/x.git", ref: "main", subPath: "mods" } },
    });
    renderWithQuery(
      <SourceDialog open onOpenChange={() => undefined} source={git} onConfirm={vi.fn()} />,
    );
    expect(screen.getByDisplayValue("https://g/x.git")).toBeInTheDocument();
    expect(screen.getByDisplayValue("main")).toBeInTheDocument();
    expect(screen.getByDisplayValue("mods")).toBeInTheDocument();
  });

  it("prefills http source with secret when editing", () => {
    const http = makeModuleSource({
      metadata: { name: "archive" },
      spec: { type: "http", http: { url: "https://e/m.zip", secretRef: { name: "archive-creds" } } },
    });
    renderWithQuery(
      <SourceDialog open onOpenChange={() => undefined} source={http} onConfirm={vi.fn()} />,
    );
    expect(screen.getByDisplayValue("https://e/m.zip")).toBeInTheDocument();
    expect(screen.getByDisplayValue("archive-creds")).toBeInTheDocument();
  });

  it("clears error when dialog reopens", async () => {
    const onConfirm = vi.fn().mockRejectedValue(new Error("failed"));
    const { rerender } = renderWithQuery(
      <SourceDialog
        open
        onOpenChange={() => undefined}
        source={null}
        onConfirm={onConfirm}
      />,
    );
    fireEvent.change(screen.getByPlaceholderText("community"), { target: { value: "src" } });
    fireEvent.change(screen.getByPlaceholderText("ghcr.io/gameplanepanel/gameplane-modules"), {
      target: { value: "ghcr.io/x" },
    });
    fireEvent.change(screen.getByPlaceholderText("minecraft-java, valheim"), {
      target: { value: "mc" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Add source" }));
    await screen.findByText(/failed/);

    // Close and reopen the dialog
    rerender(
      <SourceDialog
        open={false}
        onOpenChange={() => undefined}
        source={null}
        onConfirm={onConfirm}
      />,
    );
    rerender(
      <SourceDialog
        open
        onOpenChange={() => undefined}
        source={null}
        onConfirm={onConfirm}
      />,
    );

    expect(screen.queryByText(/failed/)).not.toBeInTheDocument();
  });
});
