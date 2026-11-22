import { describe, it, expect, vi } from "vitest";
import { http, HttpResponse } from "msw";
import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { server } from "@/test/server";
import { renderWithQuery } from "@/test/render";
import { ModpackVersionPicker, isOlderThan } from "./modpack-version-picker";
import type { RegistryVersion } from "@/types";

const version = (id: string): RegistryVersion => ({
  id,
  name: `Pack ${id}`,
  versionNumber: id,
  gameVersions: ["1.21.1"],
  loaders: ["neoforge"],
  files: [],
});

// Newest first, as the API returns them.
const versions = [version("3"), version("2"), version("1")];
const MC = "Minecraft 1.21.1 · NeoForge";
const VERSIONS_PATH = "/servers/test/mods/registry/projects/proj-1/versions";

function mount(props: Partial<Parameters<typeof ModpackVersionPicker>[0]> = {}) {
  const onContinue = vi.fn();
  const onOpenChange = vi.fn();
  renderWithQuery(
    <ModpackVersionPicker
      open
      onOpenChange={onOpenChange}
      name="test"
      provider="curseforge"
      project="proj-1"
      title="All the Mods"
      mcLabel={MC}
      onContinue={onContinue}
      {...props}
    />,
  );
  return { onContinue, onOpenChange };
}

describe("isOlderThan", () => {
  it("is true only when the candidate is further down the newest-first list", () => {
    expect(isOlderThan(versions, "1", "2")).toBe(true);
    expect(isOlderThan(versions, "2", "2")).toBe(false);
    expect(isOlderThan(versions, "3", "2")).toBe(false);
  });

  it("is false when either id is missing or there is no pin", () => {
    expect(isOlderThan(versions, "1", undefined)).toBe(false);
    expect(isOlderThan(versions, "1", "")).toBe(false);
    expect(isOlderThan(versions, "missing", "2")).toBe(false);
    expect(isOlderThan(versions, "1", "missing")).toBe(false);
    expect(isOlderThan([], "1", "2")).toBe(false);
  });
});

describe("ModpackVersionPicker", () => {
  it("preselects the newest version and continues with it", async () => {
    server.use(http.get(VERSIONS_PATH, () => HttpResponse.json(versions)));
    const { onContinue } = mount();
    const radios = await screen.findAllByRole("radio");
    expect(radios).toHaveLength(3);
    expect(radios[0]).toBeChecked();
    await userEvent.click(screen.getByRole("button", { name: "Continue" }));
    expect(onContinue).toHaveBeenCalledWith(versions[0], false);
  });

  it("requests CurseForge modpack versions for the project", async () => {
    let seen: URL | undefined;
    server.use(
      http.get(VERSIONS_PATH, ({ request }) => {
        seen = new URL(request.url);
        return HttpResponse.json(versions);
      }),
    );
    mount();
    await screen.findAllByRole("radio");
    expect(seen?.searchParams.get("provider")).toBe("curseforge");
    expect(seen?.searchParams.get("type")).toBe("modpack");
  });

  it("marks the pinned and older rows and passes isOlder for an older choice", async () => {
    server.use(http.get(VERSIONS_PATH, () => HttpResponse.json(versions)));
    const { onContinue } = mount({ pinnedVersionId: "2" });
    const radios = await screen.findAllByRole("radio");
    expect(screen.getByText("Pinned")).toBeInTheDocument();
    expect(screen.getAllByText("Older")).toHaveLength(1);
    await userEvent.click(radios[2]);
    expect(radios[2]).toBeChecked();
    await userEvent.click(screen.getByRole("button", { name: "Continue" }));
    expect(onContinue).toHaveBeenCalledWith(versions[2], true);
  });

  it("uses the confirm label and change title in change mode", async () => {
    server.use(http.get(VERSIONS_PATH, () => HttpResponse.json(versions)));
    mount({ confirmLabel: "Switch version" });
    await screen.findAllByRole("radio");
    expect(screen.getByText("Change version")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Switch version" })).toBeEnabled();
  });

  it("shows the choose title and the default label outside change mode", async () => {
    server.use(http.get(VERSIONS_PATH, () => HttpResponse.json(versions)));
    mount();
    await screen.findAllByRole("radio");
    expect(screen.getByText("Choose a version")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Continue" })).toBeEnabled();
  });

  it("shows the loading state while versions are requested", () => {
    server.use(http.get(VERSIONS_PATH, () => new Promise<never>(() => {})));
    mount();
    expect(screen.getByText("Loading versions…")).toBeInTheDocument();
  });

  it("shows the empty state and disables Continue when nothing matches", async () => {
    server.use(http.get(VERSIONS_PATH, () => HttpResponse.json([])));
    mount();
    expect(await screen.findByText(`No versions of this pack match ${MC}.`)).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Continue" })).toBeDisabled();
  });

  it("shows an error message when versions cannot be loaded", async () => {
    server.use(http.get(VERSIONS_PATH, () => HttpResponse.json({ title: "boom" }, { status: 500 })));
    mount();
    expect(await screen.findByRole("alert")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Continue" })).toBeDisabled();
  });

  it("does not request versions while closed", () => {
    let requested = false;
    server.use(
      http.get(VERSIONS_PATH, () => {
        requested = true;
        return HttpResponse.json(versions);
      }),
    );
    mount({ open: false });
    expect(requested).toBe(false);
  });
});
