import { getTypeScriptStdinMode, SharedStdin, type TypeScriptWorkerEvent, type TypeScriptWorkerRequest } from "./monaco-ts-stdio";

// Each editor owns one server and one virtual file. Disposing the transport
// also cancels any download or compilation in progress inside its worker.
export async function createTypeScriptTransport(
  fileName: string,
  text: string,
  signal?: AbortSignal,
  onError?: (error: Error) => void,
): Promise<{ port: MessagePort; dispose(): void }> {
  signal?.throwIfAborted();
  const mode = getTypeScriptStdinMode();
  if (!mode) {
    throw new Error("TypeScript language services require cross-origin isolation or WebAssembly Promise Integration");
  }
  if (!fileName || /[/\\\0]/.test(fileName) || !/\.(?:[cm]?[jt]s|[jt]sx)$/i.test(fileName)) {
    throw new Error("Invalid TypeScript or JavaScript filename");
  }
  const pipe = mode === "shared" ? new SharedStdin() : undefined;
  const worker = new Worker(new URL("./monaco-ts-worker.ts", import.meta.url), { type: "module", name: "typescript-lsp" });
  const channel = new MessageChannel();
  const pending: Uint8Array[] = [];
  let offset = 0;
  let disposed = false;
  let started = false;
  const encoder = new TextEncoder();

  function flush() {
    while (pipe && pending.length) {
      const bytes = pending[0];
      const count = pipe.write(bytes.subarray(offset));
      if (!count) return;
      offset += count;
      if (offset === bytes.length) {
        pending.shift();
        offset = 0;
      }
    }
  }

  function dispose() {
    if (disposed) return;
    disposed = true;
    signal?.removeEventListener("abort", abort);
    worker.terminate();
    channel.port1.close();
    channel.port2.close();
    pending.length = 0;
  }

  let rejectStartup: (reason: unknown) => void;
  function abort() {
    rejectStartup(signal?.reason);
    dispose();
  }
  function fail(error: Error) {
    if (disposed) return;
    rejectStartup(error);
    dispose();
    if (started) onError?.(error);
  }

  await new Promise<void>((resolve, reject) => {
    rejectStartup = reject;
    signal?.addEventListener("abort", abort, { once: true });
    worker.addEventListener("error", event => fail(new Error(event.message || "TypeScript worker failed")));
    worker.addEventListener("messageerror", () => fail(new Error("Invalid message from the TypeScript worker")));
    worker.addEventListener("message", (event: MessageEvent<TypeScriptWorkerEvent>) => {
      const message = event.data;
      switch (message.type) {
        case "ready":
          started = true;
          resolve();
          break;
        case "drain":
          flush();
          break;
        case "message":
          channel.port1.postMessage(message.message);
          break;
        case "error":
          fail(new Error(message.error));
          break;
      }
    });
    channel.port1.onmessage = (event: MessageEvent<unknown>) => {
      const body = encoder.encode(JSON.stringify(event.data));
      const header = encoder.encode(`Content-Length: ${body.length}\r\n\r\n`);
      if (pipe) {
        pending.push(header, body);
        flush();
      } else {
        const bytes = new Uint8Array(header.length + body.length);
        bytes.set(header);
        bytes.set(body, header.length);
        worker.postMessage({ type: "stdin", bytes } satisfies TypeScriptWorkerRequest, [bytes.buffer]);
      }
    };
    try {
      const init = pipe ? { fileName, text, stdin: pipe.buffer } : { fileName, text };
      worker.postMessage(init satisfies TypeScriptWorkerRequest);
    } catch (error) {
      dispose();
      reject(error);
    }
  });
  return { port: channel.port2, dispose };
}
