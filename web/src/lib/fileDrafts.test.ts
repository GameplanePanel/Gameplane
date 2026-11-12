import { describe, expect, it, vi } from "vitest";
import { FileDrafts } from "./fileDrafts";

function deferred() {
  let resolve!: () => void;
  const promise = new Promise<void>((done) => { resolve = done; });
  return { promise, resolve };
}

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
    const remove = drafts.remove("/dir", true, deletion);
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
    await expect(drafts.remove("/dir", true, async () => { throw new Error("busy"); })).rejects.toThrow("busy");
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
  await drafts.remove("/dir", true, async () => {});
  expect(file.signal.aborted).toBe(true);
  expect(other.signal.aborted).toBe(false);
  release();
  releaseOther();
});

it("closes a tab only after every edit queued behind its in-flight save is written", async () => {
  const first = deferred();
  const second = deferred();
  const write = vi.fn().mockReturnValueOnce(first.promise).mockReturnValueOnce(second.promise);
  const drafts = new FileDrafts(write);
  drafts.open("/a", "original");
  drafts.open("/other", "untouched");
  drafts.edit("/a", "first");
  const pending = drafts.save("/a");
  drafts.edit("/a", "second");
  const closed = drafts.close("/a");
  expect(drafts.save("/a")).toBe(pending);
  expect(drafts.discard("/a")).toBe(false);
  expect(write.mock.calls).toEqual([["/a", "first"]]);
  first.resolve();
  await first.promise;
  expect(drafts.get("/a")).toMatchObject({ saved: "first", text: "second" });
  expect(write.mock.calls).toEqual([["/a", "first"], ["/a", "second"]]);
  second.resolve();
  expect(await closed).toBe(true);
  expect(drafts.paths()).toEqual(["/other"]);
  expect(await drafts.close("/a")).toBe(false);
  expect(drafts.discard("/a")).toBe(false);
  expect(await drafts.close("/other")).toBe(true);
  expect(write).toHaveBeenCalledTimes(2);
});

it("keeps a failed close available for explicit discard without retrying in the background", async () => {
  vi.useFakeTimers();
  try {
    const write = vi.fn().mockRejectedValue(new Error("read-only filesystem"));
    const drafts = new FileDrafts(write);
    drafts.open("/config", "old");
    drafts.edit("/config", "unsaved");
    expect(await drafts.close("/config")).toBe(false);
    drafts.setEnabled(false);
    drafts.setEnabled(true);
    await vi.runAllTimersAsync();
    expect(write).toHaveBeenCalledOnce();
    expect(drafts.get("/config")).toMatchObject({ text: "unsaved", saved: "old", error: "read-only filesystem" });
    expect(drafts.discard("/config")).toBe(true);
    drafts.open("/config", "reloaded");
    expect(drafts.get("/config")).toMatchObject({ text: "reloaded", saved: "reloaded" });
  } finally {
    vi.useRealTimers();
  }
});

it("pauses a dirty draft when permission changes during a write and resumes the queued edit", async () => {
  vi.useFakeTimers();
  try {
    const first = deferred();
    const write = vi.fn().mockReturnValueOnce(first.promise).mockResolvedValue(undefined);
    const drafts = new FileDrafts(write);
    drafts.open("/config", "old");
    drafts.edit("/config", "sent");
    const pending = drafts.save("/config");
    drafts.edit("/config", "queued");
    drafts.setEnabled(false);
    drafts.edit("/config", "unauthorized");
    first.resolve();
    expect(await pending).toBe(false);
    expect(await drafts.close("/config")).toBe(false);
    await vi.runAllTimersAsync();
    expect(write.mock.calls).toEqual([["/config", "sent"]]);
    expect(drafts.get("/config")).toMatchObject({ text: "queued", saved: "sent" });
    drafts.setEnabled(true);
    await vi.runAllTimersAsync();
    expect(write.mock.calls).toEqual([["/config", "sent"], ["/config", "queued"]]);
    expect(drafts.get("/config")).toMatchObject({ text: "queued", saved: "queued" });
  } finally {
    vi.useRealTimers();
  }
});

it("protects files opened during a pending directory deletion and leaves similarly named siblings writable", async () => {
  const gate = deferred();
  const write = vi.fn().mockResolvedValue(undefined);
  const drafts = new FileDrafts(write);
  const removed = drafts.remove("/dir", true, () => gate.promise);
  drafts.open("/dir/file", "late read");
  drafts.open("/dir-copy/file", "sibling");
  drafts.edit("/dir/file", "blocked");
  expect(drafts.get("/dir/file")).toMatchObject({ text: "late read", deleting: true });
  expect(await drafts.save("/dir/file")).toBe(false);
  expect(await drafts.close("/dir/file")).toBe(false);
  expect(drafts.discard("/dir/file")).toBe(false);
  drafts.edit("/dir-copy/file", "kept");
  expect(await drafts.save("/dir-copy/file")).toBe(true);
  gate.resolve();
  await removed;
  expect(drafts.paths()).toEqual(["/dir-copy/file"]);
  expect(write.mock.calls).toEqual([["/dir-copy/file", "kept"]]);
});

it.each(["succeeds", "fails"])("keeps a failed file deletion fenced until deletion of its parent %s", async (outcome) => {
  vi.useFakeTimers();
  try {
    const write = vi.fn().mockResolvedValue(undefined);
    const drafts = new FileDrafts(write);
    const first = deferred();
    const second = deferred();
    drafts.open("/world/config", "old");
    drafts.edit("/world/config", "unsaved");
    const firstRemoval = drafts.remove("/world/config", false, async () => {
      await first.promise;
      throw new Error("first delete failed");
    });
    const secondRemoval = drafts.remove("/world", true, async () => {
      await second.promise;
      if (outcome === "fails") throw new Error("second delete failed");
    });

    first.resolve();
    await expect(firstRemoval).rejects.toThrow("first delete failed");
    await vi.runAllTimersAsync();
    expect(write).not.toHaveBeenCalled();
    expect(drafts.get("/world/config")).toMatchObject({ text: "unsaved", deleting: true });
    drafts.open("/world/late", "read during deletion");
    expect(await drafts.save("/world/late")).toBe(false);

    second.resolve();
    if (outcome === "fails") {
      await expect(secondRemoval).rejects.toThrow("second delete failed");
      await vi.runAllTimersAsync();
      expect(write.mock.calls).toEqual([["/world/config", "unsaved"]]);
      expect(drafts.get("/world/config")).toMatchObject({ saved: "unsaved", deleting: false });
      expect(drafts.get("/world/late")?.deleting).toBe(false);
    } else {
      await secondRemoval;
      await vi.runAllTimersAsync();
      expect(drafts.paths()).toEqual([]);
      expect(write).not.toHaveBeenCalled();
    }
  } finally {
    vi.useRealTimers();
  }
});

it("shares a delete across remounts without deleting a file recreated before the response arrives", async () => {
  const drafts = new FileDrafts(vi.fn());
  const started = deferred();
  const response = deferred();
  const remote = new Map([["/config", "original"]]);
  const firstRequest = vi.fn(async () => {
    remote.delete("/config");
    started.resolve();
    await response.promise;
  });
  const secondRequest = vi.fn(async () => { remote.delete("/config"); });
  const first = drafts.remove("/config", false, firstRequest);
  await started.promise;
  expect(remote.has("/config")).toBe(false);
  remote.set("/config", "recreated");
  const duplicate = drafts.remove("/config", false, secondRequest);
  response.resolve();
  await Promise.all([first, duplicate]);
  expect(secondRequest).not.toHaveBeenCalled();
  expect(first).toBe(duplicate);
  expect(firstRequest).toHaveBeenCalledOnce();
  expect(remote.get("/config")).toBe("recreated");
});

it("delivers a shared failure to both callers and admits a fresh retry", async () => {
  const drafts = new FileDrafts(vi.fn());
  drafts.open("/config", "keep until success");
  const failure = new Error("busy");
  const request = vi.fn().mockRejectedValueOnce(failure).mockResolvedValue(undefined);
  const first = drafts.remove("/config", false, request);
  const duplicate = drafts.remove("/config", false, request);
  expect(await Promise.allSettled([first, duplicate])).toEqual([
    { status: "rejected", reason: failure }, { status: "rejected", reason: failure },
  ]);
  expect(request).toHaveBeenCalledOnce();
  expect(drafts.get("/config")?.deleting).toBe(false);
  await drafts.remove("/config", false, request);
  expect(request).toHaveBeenCalledTimes(2);
  expect(drafts.paths()).toEqual([]);
});

it("keeps recursive and nonrecursive requests for the same path distinct", async () => {
  const drafts = new FileDrafts(vi.fn());
  const gate = deferred();
  const recursive = vi.fn(() => gate.promise);
  drafts.open("/world/config", "old");
  const entryRemoval = drafts.remove("/world", false, async () => { throw new Error("directory is not empty"); });
  const treeRemoval = drafts.remove("/world", true, recursive);
  expect(treeRemoval).not.toBe(entryRemoval);
  await expect(entryRemoval).rejects.toThrow("directory is not empty");
  expect(recursive).toHaveBeenCalledOnce();
  expect(drafts.get("/world/config")?.deleting).toBe(true);
  gate.resolve();
  await treeRemoval;
  expect(drafts.paths()).toEqual([]);
});
