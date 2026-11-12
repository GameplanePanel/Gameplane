import { describe, expect, it, vi } from "vitest";
import { FileDrafts } from "./fileDrafts";

describe("file drafts", () => {
  it("retains a rejected draft for another tab and retries the same text", async () => {
    const write = vi.fn().mockRejectedValueOnce(new Error("disk full")).mockResolvedValue(undefined);
    const drafts = new FileDrafts(write);
    drafts.open("/first", "before");
    drafts.edit("/first", "after");
    expect(await drafts.save("/first")).toBe(false);
    drafts.open("/second", "other");
    drafts.open("/first", "before");
    expect(drafts.get("/first")).toMatchObject({ text: "after", saved: "before", error: "disk full" });
    expect(await drafts.save("/first")).toBe(true);
    expect(write.mock.calls).toEqual([["/first", "after"], ["/first", "after"]]);
  });

  it("orders writes and blocks descendant writes while a folder is removed", async () => {
    let finish!: () => void;
    const write = vi.fn().mockImplementationOnce(() => new Promise<void>((resolve) => { finish = resolve; })).mockResolvedValue(undefined);
    const drafts = new FileDrafts(write);
    drafts.open("/dir/file", "initial");
    drafts.edit("/dir/file", "first");
    const pending = drafts.save("/dir/file");
    drafts.edit("/dir/file", "second");
    const deletion = vi.fn().mockResolvedValue(undefined);
    const remove = drafts.remove("/dir", deletion);
    expect(deletion).not.toHaveBeenCalled();
    finish();
    await pending;
    await remove;
    expect(deletion).toHaveBeenCalledOnce();
    expect(write.mock.calls).toEqual([["/dir/file", "first"]]);
    expect(drafts.get("/dir/file")).toBeUndefined();
  });
});

it("resumes a debounced save after deleting the containing folder fails", async () => {
  vi.useFakeTimers();
  try {
    const write = vi.fn().mockResolvedValue(undefined);
    const drafts = new FileDrafts(write);
    drafts.open("/dir/file", "before");
    drafts.edit("/dir/file", "edited");
    await expect(drafts.remove("/dir", async () => { throw new Error("busy"); })).rejects.toThrow("busy");
    await vi.advanceTimersByTimeAsync(1500);
    expect(write).toHaveBeenCalledWith("/dir/file", "edited");
  } finally {
    vi.useRealTimers();
  }
});

it("cancels outstanding reads of files removed in a batch", async () => {
  const drafts = new FileDrafts(vi.fn());
  const file = new AbortController();
  const other = new AbortController();
  const release = drafts.trackRead("/dir/file", file);
  const releaseOther = drafts.trackRead("/other/file", other);
  await drafts.remove("/dir", async () => {});
  expect(file.signal.aborted).toBe(true);
  expect(other.signal.aborted).toBe(false);
  release();
  releaseOther();
});
