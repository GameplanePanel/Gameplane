import { afterEach, describe, expect, it, vi } from "vitest";
import { act, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { makeBackup } from "@/test/factories";
import { BackupDownloadDialog, DOWNLOAD_HANDOFF_MS } from "./BackupDownloadDialog";

afterEach(() => vi.restoreAllMocks());

describe("BackupDownloadDialog hand-off timer", () => {
  it("is not restarted by a parent re-render and calls the latest onClose", async () => {
    vi.spyOn(HTMLAnchorElement.prototype, "click").mockImplementation(() => undefined);
    const backup = makeBackup({ status: { phase: "Succeeded", snapshotID: "s1" } });
    const first = vi.fn();
    const second = vi.fn();
    const timers = vi.spyOn(globalThis, "setTimeout");
    const handoffs = () => timers.mock.calls.filter(([, delay]) => delay === DOWNLOAD_HANDOFF_MS);

    const { rerender } = render(<BackupDownloadDialog backup={backup} onClose={first} />);
    await userEvent.click(await screen.findByRole("button", { name: /^download$/i }));
    expect(await screen.findByText("Preparing download…")).toBeInTheDocument();
    expect(screen.getByText("If the download fails, your browser's download list shows the reason.")).toBeInTheDocument();
    expect(handoffs()).toHaveLength(1);

    // The index page polls and passes a new inline onClose each time.
    rerender(<BackupDownloadDialog backup={backup} onClose={second} />);
    rerender(<BackupDownloadDialog backup={backup} onClose={() => second()} />);
    expect(handoffs()).toHaveLength(1);

    act(() => { (handoffs()[0][0] as () => void)(); });
    expect(first).not.toHaveBeenCalled();
    expect(second).toHaveBeenCalledTimes(1);
  });
});
