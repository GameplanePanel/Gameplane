import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { act, fireEvent, screen, waitFor, within } from "@testing-library/react";
import { ResourceTargetProvider } from "@/lib/resourceTarget";
import { renderWithQuery as baseRenderWithQuery } from "@/test/render";
import { FilesTab } from "./Files";

vi.mock("@/lib/monaco-editor", () => ({
  startFileLanguageService: vi.fn(),
  default: () => <textarea data-testid="monaco" />,
}));

// FakeXHR stands in for XMLHttpRequest: streaming uploads report progress
// through the XHR upload events, which fetch mocks cannot drive.
class FakeXHR {
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

const fetchMock = vi.fn();

beforeEach(() => {
  FakeXHR.instances = [];
  vi.stubGlobal("fetch", fetchMock);
  vi.stubGlobal("XMLHttpRequest", FakeXHR);
  fetchMock.mockImplementation(async (url: string) => {
    if (url.startsWith("/servers/mc-survival/files/list")) return jsonRes(ROOT_ENTRIES);
    throw new Error(`unexpected fetch: ${url}`);
  });
});

afterEach(() => {
  fetchMock.mockReset();
  vi.unstubAllGlobals();
});

function jsonRes(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });
}

const ROOT_ENTRIES = [
  { name: "config", path: "/config", size: 0, dir: true },
  { name: "server.properties", path: "/server.properties", size: 2100, dir: false },
];

function renderFiles() {
  return baseRenderWithQuery(
    <ResourceTargetProvider
      target={{ cluster: "local", name: "mc-survival" }}
      access={{ canWrite: true, canControl: true, canConsole: true, canDelete: true, isOwner: true, isCollaborator: false, permissions: ["*"] }}
    >
      <FilesTab name="mc-survival" />
    </ResourceTargetProvider>,
  );
}

function pick(...files: File[]) {
  const input = screen.getByTestId("files-upload-input") as HTMLInputElement;
  const list = Object.assign({ length: files.length }, files) as unknown as FileList;
  fireEvent.change(input, { target: { files: list } });
}

function lastXHR(): FakeXHR {
  const xhr = FakeXHR.instances.at(-1);
  expect(xhr).toBeDefined();
  return xhr!;
}

function tray(): HTMLElement {
  return screen.getByRole("region", { name: "Upload progress" });
}

describe("Files tab streaming upload", () => {
  it("PUTs each picked file to files/stream and shows its progress in the tray", async () => {
    renderFiles();
    await screen.findByText("server.properties");
    pick(new File(["0123456789"], "world.zip"));
    await waitFor(() => expect(FakeXHR.instances).toHaveLength(1));
    const xhr = lastXHR();
    expect(xhr.method).toBe("PUT");
    expect(xhr.url).toBe("/servers/mc-survival/files/stream?path=%2Fworld.zip");
    expect(xhr.headers["Content-Type"]).toBe("application/octet-stream");
    const t = tray();
    expect(within(t).getByText("Uploads · 1 file")).toBeInTheDocument();
    expect(within(t).getByText("world.zip")).toBeInTheDocument();
    act(() => xhr.progress(4, 10));
    expect(within(t).getByText(/40%/)).toBeInTheDocument();
    expect(within(t).getByRole("progressbar")).toHaveAttribute("aria-valuenow", "40");
  });

  it("uploads the next file only after the previous one finishes", async () => {
    renderFiles();
    await screen.findByText("server.properties");
    pick(new File(["a"], "one.jar"), new File(["b"], "two.jar"));
    await waitFor(() => expect(FakeXHR.instances).toHaveLength(1));
    expect(within(tray()).getByText("Waiting")).toBeInTheDocument();
    act(() => lastXHR().finish(204));
    await waitFor(() => expect(FakeXHR.instances).toHaveLength(2));
    expect(FakeXHR.instances[1].url).toBe("/servers/mc-survival/files/stream?path=%2Ftwo.jar");
  });

  it("cancel aborts the running upload, keeps its row as cancelled until the batch ends", async () => {
    renderFiles();
    await screen.findByText("server.properties");
    pick(new File(["0123456789"], "world.zip"), new File(["x"], "next.zip"));
    await waitFor(() => expect(FakeXHR.instances).toHaveLength(1));
    const xhr = lastXHR();
    fireEvent.click(within(tray()).getAllByRole("button", { name: "Cancel" })[0]);
    await waitFor(() => expect(xhr.aborted).toBe(true));
    expect(await within(tray()).findByText("Cancelled")).toBeInTheDocument();
    // The batch goes on with the next file; the cancelled row is cleared when the batch is done.
    await waitFor(() => expect(FakeXHR.instances).toHaveLength(2));
    expect(FakeXHR.instances[1].url).toBe("/servers/mc-survival/files/stream?path=%2Fnext.zip");
    act(() => lastXHR().finish(204));
    await waitFor(() => expect(screen.queryByRole("region", { name: "Upload progress" })).toBeNull());
  });

  it("cancel all stops the running file, clears the waiting and cancelled rows", async () => {
    renderFiles();
    await screen.findByText("server.properties");
    pick(new File(["a"], "one.jar"), new File(["b"], "two.jar"));
    await waitFor(() => expect(FakeXHR.instances).toHaveLength(1));
    fireEvent.click(within(tray()).getByRole("button", { name: "Cancel all" }));
    await waitFor(() => expect(FakeXHR.instances[0].aborted).toBe(true));
    await waitFor(() => expect(screen.queryByRole("region", { name: "Upload progress" })).toBeNull());
    expect(FakeXHR.instances).toHaveLength(1);
  });

  it("keeps the Upload button disabled while a file uploads", async () => {
    renderFiles();
    await screen.findByText("server.properties");
    pick(new File(["0123456789"], "world.zip"));
    await waitFor(() => expect(FakeXHR.instances).toHaveLength(1));
    expect(screen.getByRole("button", { name: /Upload/ })).toBeDisabled();
    act(() => lastXHR().finish(204));
    await waitFor(() => expect(screen.getByRole("button", { name: /Upload/ })).toBeEnabled());
  });

  it("shows the connection-lost message and retries the same file", async () => {
    renderFiles();
    await screen.findByText("server.properties");
    pick(new File(["0123456789"], "fabric-api.jar"));
    await waitFor(() => expect(FakeXHR.instances).toHaveLength(1));
    act(() => lastXHR().onerror?.());
    expect(
      await within(tray()).findByText(/Connection to the server was lost\. The old file is unchanged\./),
    ).toBeInTheDocument();
    expect(within(tray()).getByText("Failed at 0%")).toBeInTheDocument();
    fireEvent.click(within(tray()).getByRole("button", { name: "Retry" }));
    await waitFor(() => expect(FakeXHR.instances).toHaveLength(2));
    expect(FakeXHR.instances[1].url).toBe("/servers/mc-survival/files/stream?path=%2Ffabric-api.jar");
  });

  it("shows the size limit from the 413 problem detail when the API names it", async () => {
    renderFiles();
    await screen.findByText("server.properties");
    pick(new File(["x"], "world-2026.zip"));
    await waitFor(() => expect(FakeXHR.instances).toHaveLength(1));
    const xhr = lastXHR();
    xhr.responseHeaders["Content-Type"] = "application/problem+json";
    act(() => xhr.finish(413, JSON.stringify({ type: "about:blank", title: "Request Entity Too Large", status: 413, detail: "request body exceeds the 21474836480 byte limit" })));
    expect(await within(tray()).findByText("Larger than the 20 GiB upload limit")).toBeInTheDocument();
  });

  it("shows a generic size message for a 413 answer that does not name the limit", async () => {
    renderFiles();
    await screen.findByText("server.properties");
    pick(new File(["x"], "world-2026.zip"));
    await waitFor(() => expect(FakeXHR.instances).toHaveLength(1));
    act(() => lastXHR().finish(413, "too large"));
    expect(await within(tray()).findByText("Larger than the upload limit")).toBeInTheDocument();
  });

  it("shows the not-enough-space message for a 507 answer", async () => {
    renderFiles();
    await screen.findByText("server.properties");
    pick(new File(["x"], "backup.tar"));
    await waitFor(() => expect(FakeXHR.instances).toHaveLength(1));
    act(() => lastXHR().finish(507, "disk full"));
    expect(await within(tray()).findByText("Not enough free space on the server")).toBeInTheDocument();
  });

  it("refreshes the file list after a successful upload", async () => {
    renderFiles();
    await screen.findByText("server.properties");
    const listCalls = () => fetchMock.mock.calls.filter(([url]) => String(url).startsWith("/servers/mc-survival/files/list")).length;
    const before = listCalls();
    pick(new File(["x"], "eula.txt"));
    await waitFor(() => expect(FakeXHR.instances).toHaveLength(1));
    act(() => lastXHR().finish(204));
    await waitFor(() => expect(listCalls()).toBeGreaterThan(before));
    await waitFor(() => expect(screen.queryByRole("region", { name: "Upload progress" })).toBeNull());
  });
});
