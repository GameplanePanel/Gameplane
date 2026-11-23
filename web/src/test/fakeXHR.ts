// FakeXHR stands in for XMLHttpRequest in tests. Streaming uploads report
// progress through the XHR upload events, which fetch mocks cannot drive.
// Tests call vi.stubGlobal("XMLHttpRequest", FakeXHR) and reset `instances`;
// the caller restores globals (vi.unstubAllGlobals) as usual.
export class FakeXHR {
  static instances: FakeXHR[] = [];
  method = "";
  url = "";
  headers: Record<string, string> = {};
  status = 0;
  statusText = "";
  responseText = "";
  responseHeaders: Record<string, string> = {};
  aborted = false;
  upload: { onprogress: ((e: unknown) => void) | null } = { onprogress: null };
  onload: (() => void) | null = null;
  onerror: (() => void) | null = null;
  ontimeout: (() => void) | null = null;
  onabort: (() => void) | null = null;
  open(method: string, url: string) { this.method = method; this.url = url; }
  withCredentials = false;
  setRequestHeader(key: string, value: string) { this.headers[key] = value; }
  getResponseHeader(key: string) { return this.responseHeaders[key] ?? null; }
  send() { FakeXHR.instances.push(this); }
  abort() { this.aborted = true; this.onabort?.(); }
  finish(status: number, body = "") {
    this.status = status;
    this.responseText = body;
    this.onload?.();
  }
  progress(loaded: number, total: number) {
    this.upload.onprogress?.({ lengthComputable: true, loaded, total });
  }
}
