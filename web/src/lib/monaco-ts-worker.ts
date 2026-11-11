// The WASI poll and stdio handling follows the TypeScript Playground v2 worker:
// https://github.com/microsoft/TypeScript-Website/blob/a93dd416c393cbc7570b829a9e0c753b2678249f/packages/playground-v2/src/tsgo-lsp.worker.ts
// Copyright (c) Microsoft Corporation. Permission is hereby granted, free of
// charge, to any person obtaining a copy of this software and associated
// documentation files (the "Software"), to deal in the Software without
// restriction, including without limitation the rights to use, copy, modify,
// merge, publish, distribute, sublicense, and/or sell copies of the Software,
// and to permit persons to whom the Software is furnished to do so, subject to
// the following conditions: The above copyright notice and this permission
// notice shall be included in all copies or substantial portions of the Software.
// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
// OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
// SOFTWARE.
import { ConsoleStdout, Directory, Fd, File, Inode, PreopenDirectory, WASI, wasi } from "@bjorn3/browser_wasi_shim";
import compilerUrl from "@typescript/typescript-wasip1-wasm/lib/tsc.wasm?url";
import { SharedStdin, type TypeScriptWorkerEvent, type TypeScriptWorkerRequest } from "./monaco-ts-stdio";

const declarations = import.meta.glob<string>("../../node_modules/@typescript/typescript-wasip1-wasm/lib/lib*.d.ts", {
  eager: true, import: "default", query: "?raw",
});
const encoder = new TextEncoder();
const decoder = new TextDecoder();

function send(event: TypeScriptWorkerEvent) {
  self.postMessage(event);
}

class Stdin extends Fd {
  constructor(private readonly pipe: SharedStdin) {
    super();
  }

  override fd_fdstat_get() {
    const fdstat = new wasi.Fdstat(wasi.FILETYPE_CHARACTER_DEVICE, wasi.FDFLAGS_NONBLOCK);
    fdstat.fs_rights_base = BigInt(wasi.RIGHTS_FD_READ | wasi.RIGHTS_POLL_FD_READWRITE);
    return { ret: wasi.ERRNO_SUCCESS, fdstat };
  }

  override fd_filestat_get() {
    const filestat = new wasi.Filestat(Inode.issue_ino(), wasi.FILETYPE_CHARACTER_DEVICE, 0n);
    return { ret: wasi.ERRNO_SUCCESS, filestat };
  }

  override fd_fdstat_set_flags() {
    return wasi.ERRNO_SUCCESS;
  }

  override fd_read(size: number) {
    const data = this.pipe.read(size);
    if (data.length) send({ type: "drain" });
    return { ret: data.length ? wasi.ERRNO_SUCCESS : wasi.ERRNO_AGAIN, data };
  }
}

// fd_write may split a UTF-8 code point or write several complete LSP frames.
// Decode only after receiving the Content-Length bytes for the entire message.
class Stdout extends ConsoleStdout {
  constructor() {
    let header = "";
    let body: Uint8Array | undefined;
    let written = 0;
    super(chunk => {
      let offset = 0;
      while (offset < chunk.length) {
        if (!body) {
          header += String.fromCharCode(chunk[offset++]);
          if (header.length > 1024) throw new Error("Invalid TypeScript language server frame header");
          if (!header.endsWith("\r\n\r\n")) continue;
          const length = Number(/^Content-Length: (\d+)\r?$/im.exec(header)?.[1]);
          if (!Number.isSafeInteger(length) || length <= 0) throw new Error("Missing language server frame length");
          body = new Uint8Array(length);
          written = 0;
          header = "";
        }
        const count = Math.min(body.length - written, chunk.length - offset);
        body.set(chunk.subarray(offset, offset + count), written);
        written += count;
        offset += count;
        if (written === body.length) {
          send({ type: "message", message: JSON.parse(decoder.decode(body)) });
          body = undefined;
        }
      }
    });
  }
}

function clockMilliseconds(id: number) {
  return id === wasi.CLOCKID_REALTIME ? Date.now() : performance.now();
}

// Go's WASI scheduler polls stdin and timer deadlines together. The shim's
// default poll_oneoff cannot wake for input from the editor.
function installPoll(runtime: WASI, pipe: SharedStdin) {
  runtime.wasiImport.poll_oneoff = (input: number, output: number, count: number, eventCount: number) => {
    const memory = new DataView(runtime.inst.exports.memory.buffer);
    const subscriptions = Array.from({ length: count }, (_, i) => {
      const subscription = wasi.Subscription.read_bytes(memory, input + i * 48);
      const timeout = Number(subscription.timeout) / 1e6;
      const absolute = subscription.flags & wasi.SUBCLOCKFLAGS_SUBSCRIPTION_CLOCK_ABSTIME;
      const deadline = absolute ? timeout : clockMilliseconds(subscription.clockid) + timeout;
      return { subscription, deadline };
    });
    for (;;) {
      let ready = 0;
      let wait = Infinity;
      for (const { subscription, deadline } of subscriptions) {
        if (subscription.eventtype === wasi.EVENTTYPE_CLOCK) {
          const remaining = deadline - clockMilliseconds(subscription.clockid);
          if (remaining > 0) {
            wait = Math.min(wait, remaining);
            continue;
          }
        } else if (subscription.eventtype === wasi.EVENTTYPE_FD_READ && !pipe.readable) {
          continue;
        }
        const pointer = output + ready++ * 32;
        new wasi.Event(subscription.userdata, wasi.ERRNO_SUCCESS, subscription.eventtype).write_bytes(memory, pointer);
        memory.setBigUint64(pointer + 16, 0n, true);
        memory.setUint16(pointer + 24, 0, true);
      }
      if (ready) {
        memory.setUint32(eventCount, ready, true);
        return wasi.ERRNO_SUCCESS;
      }
      pipe.wait(wait);
    }
  };
}

async function start({ stdin, fileName, text }: TypeScriptWorkerRequest) {
  const pipe = new SharedStdin(stdin);
  const libraries = new Directory(Object.entries(declarations).map(([path, source]) => [
    path.slice(path.lastIndexOf("/") + 1), new File(encoder.encode(source)),
  ]));
  const config = {
    compilerOptions: { target: "ESNext", module: "ESNext", jsx: "preserve", allowJs: true, checkJs: true, noEmit: true, types: [] },
    files: [fileName],
  };
  const workspace = new Directory([
    [fileName, new File(encoder.encode(text))],
    ["tsconfig.json", new File(encoder.encode(JSON.stringify(config)))],
  ]);
  const root = new PreopenDirectory("/", new Map([
    ["workspace", workspace], ["tmp", new Directory([])], ["typescript", new Directory([["lib", libraries]])],
  ]));
  // Compiler stderr can quote the user's file. Report startup and exit failures
  // through the transport and acknowledge stderr without retaining its contents.
  const stderr = new ConsoleStdout(() => {});
  const runtime = new WASI(
    ["tsc", "--lsp", "--stdio"],
    ["HOME=/workspace", "TMPDIR=/tmp"],
    [new Stdin(pipe), new Stdout(), stderr, root],
  );
  installPoll(runtime, pipe);
  const response = await fetch(compilerUrl);
  if (!response.ok) throw new Error(`Could not load the TypeScript compiler (HTTP ${response.status})`);
  const imports = { wasi_snapshot_preview1: runtime.wasiImport };
  const { instance } = response.headers.get("Content-Type")?.split(";")[0] === "application/wasm"
    ? await WebAssembly.instantiateStreaming(response, imports)
    : await WebAssembly.instantiate(await response.arrayBuffer(), imports);
  send({ type: "ready" });
  const status = runtime.start(instance as Parameters<WASI["start"]>[0]);
  throw new Error(`TypeScript language server exited with status ${status}`);
}

self.addEventListener("message", (event: MessageEvent<TypeScriptWorkerRequest>) => {
  void start(event.data).catch((error: unknown) => send({ type: "error", error: String(error) }));
}, { once: true });
