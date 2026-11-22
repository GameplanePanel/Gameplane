import { describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen } from "@testing-library/react";
import type { ModpackMissingFile, ModpackView } from "@/types";
import { ModpackMissingFiles } from "./modpack-missing-files";

const files: ModpackMissingFile[] = [
  { mod: "Jei", versionName: "1.0", filename: "jei-1.0.jar", downloadPage: "https://www.curseforge.com/minecraft/mc-mods/jei/files/1" },
  { mod: "Sodium", versionName: "0.5", filename: "sodium-0.5.jar", downloadPage: "http://insecure.example/sodium" },
];

function view(over: Partial<ModpackView> = {}): ModpackView {
  return {
    active: null,
    phase: "",
    message: "",
    backupName: "",
    keyShared: true,
    missingFiles: files,
    missingFilesUnknown: false,
    downloadsPath: "/data/mods-downloads",
    backupAvailable: false,
    ...over,
  };
}

describe("ModpackMissingFiles", () => {
  it("renders nothing when there are no missing files", () => {
    const { container } = render(<ModpackMissingFiles view={view({ missingFiles: [] })} onOpenFiles={vi.fn()} />);
    expect(container).toBeEmptyDOMElement();
  });

  it("shows the unknown-state note instead of the table", () => {
    render(<ModpackMissingFiles view={view({ missingFilesUnknown: true, missingFiles: [] })} onOpenFiles={vi.fn()} />);
    expect(
      screen.getByText("Start the server to check whether this pack needs files you have to download yourself."),
    ).toBeInTheDocument();
    expect(screen.queryByRole("table")).not.toBeInTheDocument();
  });

  it("uses the plural title for several files and the singular for one", () => {
    const { unmount } = render(<ModpackMissingFiles view={view()} onOpenFiles={vi.fn()} />);
    expect(screen.getByText("2 files need a manual download")).toBeInTheDocument();
    unmount();
    render(<ModpackMissingFiles view={view({ missingFiles: [files[0]] })} onOpenFiles={vi.fn()} />);
    expect(screen.getByText("1 file needs a manual download")).toBeInTheDocument();
  });

  it("shows the downloads path in code style", () => {
    const { container } = render(<ModpackMissingFiles view={view()} onOpenFiles={vi.fn()} />);
    const code = container.querySelector("code");
    expect(code).not.toBeNull();
    expect(code).toHaveTextContent("/data/mods-downloads");
  });

  it("lists each mod and file name in the table", () => {
    render(<ModpackMissingFiles view={view()} onOpenFiles={vi.fn()} />);
    expect(screen.getByRole("table")).toBeInTheDocument();
    expect(screen.getByText("Jei")).toBeInTheDocument();
    expect(screen.getByText("jei-1.0.jar")).toBeInTheDocument();
    expect(screen.getByText("Sodium")).toBeInTheDocument();
    expect(screen.getByText("sodium-0.5.jar")).toBeInTheDocument();
  });

  it("renders https download pages as external links with safe rel and target", () => {
    render(<ModpackMissingFiles view={view()} onOpenFiles={vi.fn()} />);
    const link = screen.getByRole("link", { name: /download page/i });
    expect(link).toHaveAttribute("href", files[0].downloadPage);
    expect(link).toHaveAttribute("target", "_blank");
    expect(link).toHaveAttribute("rel", "noopener noreferrer");
  });

  it("renders a non-https download page as plain text, not a link", () => {
    render(<ModpackMissingFiles view={view()} onOpenFiles={vi.fn()} />);
    const links = screen.getAllByRole("link");
    expect(links).toHaveLength(1);
    expect(links[0]).toHaveAttribute("href", files[0].downloadPage);
    expect(screen.getAllByText("Download page")).toHaveLength(2);
  });

  it("calls onOpenFiles when Open Files is pressed", () => {
    const onOpenFiles = vi.fn();
    render(<ModpackMissingFiles view={view()} onOpenFiles={onOpenFiles} />);
    fireEvent.click(screen.getByRole("button", { name: "Open Files" }));
    expect(onOpenFiles).toHaveBeenCalledTimes(1);
  });

  it("hides the Open Files button when no handler is passed", () => {
    render(<ModpackMissingFiles view={view()} />);
    expect(screen.queryByRole("button", { name: "Open Files" })).not.toBeInTheDocument();
  });
});
