import { describe, expect, it, vi } from "vitest";
import { FileDrafts } from "./fileDrafts";

describe("file drafts unmount flush", () => {
  it("writes a pending autosave when flushed before disabling", async () => {
    const write = vi.fn().mockResolvedValue(undefined);
    const drafts = new FileDrafts(write);
    drafts.open("/a", "old");
    drafts.edit("/a", "new");
    drafts.flushAll();
    drafts.setEnabled(false);
    await Promise.resolve();
    expect(write.mock.calls).toEqual([["/a", "new"]]);
  });

  it("writes newer text queued behind an in-flight write", async () => {
    let finish!: () => void;
    const write = vi.fn().mockImplementationOnce(() => new Promise<void>((r) => { finish = r; })).mockResolvedValue(undefined);
    const drafts = new FileDrafts(write);
    drafts.open("/a", "old");
    drafts.edit("/a", "first");
    const pending = drafts.save("/a");
    drafts.edit("/a", "second");
    drafts.flushAll();
    drafts.setEnabled(false);
    finish();
    await pending;
    expect(write.mock.calls).toEqual([["/a", "first"], ["/a", "second"]]);
  });

  it("does not write when saving is already disabled", () => {
    const write = vi.fn().mockResolvedValue(undefined);
    const drafts = new FileDrafts(write);
    drafts.open("/a", "old");
    drafts.edit("/a", "new");
    drafts.setEnabled(false);
    drafts.flushAll();
    expect(write).not.toHaveBeenCalled();
  });
});
