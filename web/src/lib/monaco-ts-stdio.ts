// The LSP server owns the worker thread while it runs. A shared stdin pipe lets
// the editor send another request while the Go runtime is waiting for input.
const controlWords = 2;
const readPosition = 0;
const writePosition = 1;
const pipeSize = 256 * 1024;

export class SharedStdin {
  readonly buffer: SharedArrayBuffer;
  private readonly positions: Int32Array<SharedArrayBuffer>;
  private readonly bytes: Uint8Array<SharedArrayBuffer>;

  constructor(buffer = new SharedArrayBuffer(controlWords * Int32Array.BYTES_PER_ELEMENT + pipeSize)) {
    this.buffer = buffer;
    this.positions = new Int32Array(buffer, 0, controlWords);
    this.bytes = new Uint8Array(buffer, this.positions.byteLength);
  }

  get readable() {
    return (Atomics.load(this.positions, writePosition) - Atomics.load(this.positions, readPosition)) >>> 0;
  }

  wait(timeout: number) {
    const previous = Atomics.load(this.positions, writePosition);
    if (!this.readable) Atomics.wait(this.positions, writePosition, previous, timeout);
  }

  // Each call copies a contiguous slice. The caller retries after wraparound or
  // when the other end frees space, including for messages larger than the pipe.
  write(data: Uint8Array) {
    const position = Atomics.load(this.positions, writePosition) >>> 0;
    const offset = position % this.bytes.length;
    const count = Math.min(data.length, this.bytes.length - this.readable, this.bytes.length - offset);
    if (count) {
      this.bytes.set(data.subarray(0, count), offset);
      Atomics.store(this.positions, writePosition, position + count);
      Atomics.notify(this.positions, writePosition);
    }
    return count;
  }

  read(size: number) {
    const position = Atomics.load(this.positions, readPosition) >>> 0;
    const offset = position % this.bytes.length;
    const count = Math.min(size, this.readable, this.bytes.length - offset);
    const data = this.bytes.slice(offset, offset + count);
    Atomics.store(this.positions, readPosition, position + count);
    return data;
  }
}

// DOM declarations in TS 7.0 predate WebAssembly JavaScript Promise Integration.
// Only the WASI poll import suspends. All other syscalls run synchronously.
type WasmPromiseIntegration = {
  Suspending: new (poll: (...args: number[]) => Promise<number>) => WebAssembly.ImportValue;
  promising: (start: () => unknown) => () => Promise<unknown>;
};

export function getTypeScriptJspi(): WasmPromiseIntegration | undefined {
  const api = globalThis.WebAssembly as (Partial<WasmPromiseIntegration> & typeof WebAssembly) | undefined;
  if (typeof api?.Suspending === "function" && typeof api.promising === "function") return api as WasmPromiseIntegration;
}

export function getTypeScriptStdinMode(): "shared" | "message" | undefined {
  if (globalThis.crossOriginIsolated && typeof SharedArrayBuffer !== "undefined") return "shared";
  if (getTypeScriptJspi()) return "message";
}

export type TypeScriptWorkerInit = { stdin?: SharedArrayBuffer; fileName: string; text: string };
export type TypeScriptWorkerRequest = TypeScriptWorkerInit | { type: "stdin"; bytes: Uint8Array<ArrayBuffer> };
export type TypeScriptWorkerEvent =
  | { type: "ready" | "drain" }
  | { type: "error"; error: string }
  | { type: "message"; message: unknown };
