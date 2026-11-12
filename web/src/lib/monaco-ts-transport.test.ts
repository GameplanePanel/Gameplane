import { Buffer } from "node:buffer";
import { MessageChannel as NodeMessageChannel } from "node:worker_threads";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { SharedStdin, type TypeScriptWorkerEvent, type TypeScriptWorkerRequest } from "./monaco-ts-stdio";
import { createTypeScriptTransport } from "./monaco-ts-transport";

class TestWorker extends EventTarget {
  static current: TestWorker;
  constructor() {
    super();
    TestWorker.current = this;
  }
  postMessage = vi.fn<(request: TypeScriptWorkerRequest, transfer?: Transferable[]) => void>();
  terminate = vi.fn();
  send(event: TypeScriptWorkerEvent) {
    this.dispatchEvent(new MessageEvent("message", { data: event }));
  }
}

beforeEach(() => {
  vi.stubGlobal("crossOriginIsolated", true);
  vi.stubGlobal("Worker", TestWorker);
  vi.stubGlobal("MessageChannel", NodeMessageChannel);
});
afterEach(() => vi.unstubAllGlobals());

it("keeps unread bytes in order as the writer wraps and applies backpressure", () => {
  const writer = new SharedStdin(new SharedArrayBuffer(2 * Int32Array.BYTES_PER_ELEMENT + 8));
  const reader = new SharedStdin(writer.buffer);
  expect(writer.write(Uint8Array.of(0, 1, 2, 3, 4, 5))).toBe(6);
  expect(Array.from(reader.read(4))).toEqual([0, 1, 2, 3]);
  const tail = Uint8Array.of(6, 7, 8, 9, 10, 11);
  const first = writer.write(tail);
  expect(writer.write(tail.subarray(first))).toBe(tail.length - first);
  expect(writer.write(Uint8Array.of(12))).toBe(0);
  expect([...reader.read(8), ...reader.read(8)]).toEqual([4, 5, 6, 7, 8, 9, 10, 11]);
  expect(reader.readable).toBe(0);
});

it("delivers an oversized unicode request as a single byte-counted frame and routes the response", async () => {
  const opening = createTypeScriptTransport("hello.ts", "const name = '世界';");
  const worker = TestWorker.current;
  const [request] = worker.postMessage.mock.calls[0];
  expect(request).toMatchObject({ fileName: "hello.ts", text: "const name = '世界';" });
  if (!("stdin" in request) || !request.stdin) throw new Error("Missing shared stdin");
  const reader = new SharedStdin(request.stdin);
  worker.send({ type: "ready" });
  const transport = await opening;
  try {
    const message = { jsonrpc: "2.0", id: 1, method: "example", params: "🐈".repeat(80_000) };
    transport.port.postMessage(message);
    await vi.waitFor(() => expect(reader.readable).toBeGreaterThan(0));
    const bytes: Uint8Array[] = [];
    const drain = () => { while (reader.readable) bytes.push(reader.read(reader.readable)); };
    drain();
    worker.send({ type: "drain" });
    drain();
    const body = JSON.stringify(message);
    const frame = Buffer.concat(bytes).toString("utf8");
    expect(frame).toBe(`Content-Length: ${Buffer.byteLength(body)}\r\n\r\n${body}`);

    const reply = { jsonrpc: "2.0", id: 1, result: { value: "✅" } };
    const received = new Promise<unknown>(resolve => {
      transport.port.onmessage = event => resolve(event.data);
    });
    worker.send({ type: "message", message: reply });
    expect(await received).toEqual(reply);
  } finally {
    transport.dispose();
  }
  expect(worker.terminate).toHaveBeenCalledOnce();
});

it("rejects an aborted download and releases the worker exactly once", async () => {
  const controller = new AbortController();
  const opening = createTypeScriptTransport("edit.js", "", controller.signal);
  controller.abort(new Error("Editor closed"));
  await expect(opening).rejects.toThrow("Editor closed");
  expect(TestWorker.current.terminate).toHaveBeenCalledOnce();
});

it("rejects startup failures and reports later failures to the active editor", async () => {
  const opening = createTypeScriptTransport("edit.ts", "");
  TestWorker.current.send({ type: "error", error: "Wasm download failed" });
  await expect(opening).rejects.toThrow("Wasm download failed");
  expect(TestWorker.current.terminate).toHaveBeenCalledOnce();

  const onError = vi.fn();
  const retry = createTypeScriptTransport("edit.ts", "", undefined, onError);
  const worker = TestWorker.current;
  worker.send({ type: "ready" });
  const transport = await retry;
  worker.dispatchEvent(new ErrorEvent("error", { message: "Server crashed" }));
  expect(onError).toHaveBeenCalledWith(new Error("Server crashed"));
  transport.dispose();
  expect(worker.terminate).toHaveBeenCalledOnce();
});

it("transfers a byte-counted LSP frame without shared memory on a JSPI-capable HTTP origin", async () => {
  vi.stubGlobal("crossOriginIsolated", false);
  vi.stubGlobal("SharedArrayBuffer", undefined);
  vi.stubGlobal("WebAssembly", { Suspending: vi.fn(), promising: vi.fn() });
  const opening = createTypeScriptTransport("edit.js", "let n = 1;");
  const worker = TestWorker.current;
  expect(worker.postMessage.mock.calls[0]).toEqual([{ fileName: "edit.js", text: "let n = 1;" }]);
  worker.send({ type: "ready" });
  const transport = await opening;
  try {
    const message = { jsonrpc: "2.0", id: 1, result: "你好" };
    transport.port.postMessage(message);
    await vi.waitFor(() => expect(worker.postMessage).toHaveBeenCalledTimes(2));
    const [data, transfer] = worker.postMessage.mock.calls[1];
    if (!("type" in data) || data.type !== "stdin") throw new Error("Missing stdio frame");
    expect(transfer).toEqual([data.bytes.buffer]);
    const body = JSON.stringify(message);
    expect(new TextDecoder().decode(data.bytes)).toBe(`Content-Length: ${Buffer.byteLength(body)}\r\n\r\n${body}`);
  } finally {
    transport.dispose();
  }
});

it("rejects unavailable transports and paths outside the virtual workspace", async () => {
  await expect(createTypeScriptTransport("../secret.ts", "")).rejects.toThrow("filename");
  vi.stubGlobal("crossOriginIsolated", false);
  vi.stubGlobal("WebAssembly", {});
  await expect(createTypeScriptTransport("valid.ts", "")).rejects.toThrow("cross-origin isolation");
});
