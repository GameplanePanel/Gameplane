import { Directory, File, PreopenDirectory, WASI, wasi } from "@bjorn3/browser_wasi_shim";
import { afterEach, expect, it, vi } from "vitest";
import { SharedStdin, type TypeScriptWorkerEvent, type TypeScriptWorkerRequest } from "./monaco-ts-stdio";

// (module (memory (export "memory") 1)). The real WASI host handles all file
// descriptors and subscriptions; we substitute the compiler at its entry point.
const memoryModule = Uint8Array.of(0, 97, 115, 109, 1, 0, 0, 0, 5, 3, 1, 0, 1,
  7, 10, 1, 6, 109, 101, 109, 111, 114, 121, 2, 0);
const encoder = new TextEncoder();

function frame(value: unknown) {
  const body = JSON.stringify(value);
  return encoder.encode(`Content-Length: ${encoder.encode(body).length}\r\n\r\n${body}`);
}

async function launch(response: Response, pipe = new SharedStdin()) {
  const scope = Object.assign(new EventTarget(), {
    postMessage: vi.fn<(event: TypeScriptWorkerEvent) => void>(),
  });
  vi.stubGlobal("self", scope);
  vi.stubGlobal("fetch", vi.fn().mockResolvedValue(response));
  // A browser starts each new worker with a fresh module and message listener.
  vi.resetModules();
  await import("./monaco-ts-worker");
  scope.dispatchEvent(new MessageEvent("message", {
    data: { stdin: pipe.buffer, fileName: "script.ts", text: "const x: string = 1;" } satisfies TypeScriptWorkerRequest,
  }));
  await vi.waitFor(() => expect(scope.postMessage.mock.calls.some(([event]) => event.type === "error")).toBe(true));
  return scope.postMessage.mock.calls.map(([event]) => event);
}

afterEach(() => {
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

it("opens the selected file with its TS libraries and passes complete LSP replies across fragmented writes", async () => {
  const reply = { jsonrpc: "2.0", id: 1, result: "診断 🐈" };
  const next = { jsonrpc: "2.0", id: 2, result: [] };
  const request = frame({ jsonrpc: "2.0", id: 1, method: "initialize" });
  const pipe = new SharedStdin();
  pipe.write(request);
  // Capture assertion failures separately because the worker correctly turns
  // thrown errors in the runtime into error notifications for the editor.
  let failure: unknown;
  vi.spyOn(WASI.prototype, "start").mockImplementation(function (this: WASI, instance) {
    try {
      this.inst = instance;
      const [stdin, stdout, , root] = this.fds;
      const dirs = (root as PreopenDirectory).dir.contents;
      const workspace = dirs.get("workspace") as Directory;
      const selected = workspace.contents.get("script.ts") as File;
      const config = workspace.contents.get("tsconfig.json") as File;
      expect(new TextDecoder().decode(selected.data)).toBe("const x: string = 1;");
      expect(JSON.parse(new TextDecoder().decode(config.data))).toMatchObject({
        files: ["script.ts"], compilerOptions: { allowJs: true, checkJs: true, jsx: "preserve" },
      });
      const lib = (dirs.get("typescript") as Directory).contents.get("lib") as Directory;
      expect(lib.contents.has("lib.esnext.full.d.ts")).toBe(true);
      expect(stdin.fd_fdstat_get().fdstat?.fs_flags).toBe(wasi.FDFLAGS_NONBLOCK);
      expect(stdin.fd_fdstat_set_flags(wasi.FDFLAGS_NONBLOCK)).toBe(wasi.ERRNO_SUCCESS);
      expect(stdin.fd_filestat_get().filestat?.filetype).toBe(wasi.FILETYPE_CHARACTER_DEVICE);

      // WASI preview1 encodes subscriptions as 48-byte records and events as
      // 32-byte records. Check that the scheduler sees the pending request.
      const view = new DataView(instance.exports.memory.buffer);
      view.setBigUint64(0, 42n, true);
      view.setUint8(8, wasi.EVENTTYPE_FD_READ);
      expect(this.wasiImport.poll_oneoff(0, 128, 1, 256)).toBe(wasi.ERRNO_SUCCESS);
      expect(view.getBigUint64(128, true)).toBe(42n);
      expect(view.getUint32(256, true)).toBe(1);
      expect(Array.from(stdin.fd_read(request.length).data)).toEqual(Array.from(request));
      expect(stdin.fd_read(1).ret).toBe(wasi.ERRNO_AGAIN);

      // A short clock subscription must still wake an idle worker with no input.
      view.setUint8(8, wasi.EVENTTYPE_CLOCK);
      view.setUint32(16, wasi.CLOCKID_MONOTONIC, true);
      view.setBigUint64(24, 2_000_000n, true);
      expect(this.wasiImport.poll_oneoff(0, 128, 1, 256)).toBe(wasi.ERRNO_SUCCESS);
      expect(view.getUint8(138)).toBe(wasi.EVENTTYPE_CLOCK);

      const bytes = frame(reply);
      const emoji = bytes.indexOf(0xf0);
      expect(emoji).toBeGreaterThan(0);
      stdout.fd_write(bytes.subarray(0, 5));
      stdout.fd_write(bytes.subarray(5, emoji + 1));
      stdout.fd_write(Uint8Array.from([...bytes.subarray(emoji + 1), ...frame(next)]));
    } catch (error) {
      failure = error;
    }
    return 17;
  });
  const events = await launch(new Response(memoryModule), pipe);
  if (failure) throw failure;
  expect(events).toEqual([
    { type: "ready" }, { type: "drain" },
    { type: "message", message: reply }, { type: "message", message: next },
    { type: "error", error: expect.stringContaining("status 17") },
  ]);
});

it("reports an unavailable compiler asset before exposing a ready server", async () => {
  const events = await launch(new Response(null, { status: 404 }));
  expect(events).toEqual([{ type: "error", error: expect.stringContaining("HTTP 404") }]);
});

it("rejects malformed output from a streamed compiler instead of forwarding a partial response", async () => {
  vi.spyOn(WASI.prototype, "start").mockImplementation(function (this: WASI) {
    this.fds[1].fd_write(encoder.encode("Content-Length: -1\r\n\r\n{}"));
    return 0;
  });
  const response = new Response(memoryModule, { headers: { "Content-Type": "application/wasm" } });
  const events = await launch(response);
  expect(events).toEqual([
    { type: "ready" }, { type: "error", error: expect.stringContaining("frame length") },
  ]);
});
