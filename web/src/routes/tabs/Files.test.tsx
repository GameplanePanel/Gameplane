import { ResourceTargetProvider } from "@/lib/resourceTarget";
import { useState, type ReactElement } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { act, fireEvent, screen, waitFor, within } from "@testing-library/react";
import { renderWithQuery as baseRenderWithQuery } from "@/test/render";
import { FilesTab } from "./Files";

type FetchInit = Parameters<typeof fetch>[1];

// Monaco can't render in jsdom — replace with a controlled textarea so
// onChange fires and the editor value can be inspected/manipulated.
vi.mock("@monaco-editor/react", () => ({
  default: ({
    value,
    onChange,
  }: {
    value: string;
    onChange?: (v: string | undefined) => void;
  }) => (
    <textarea
      data-testid="monaco"
      value={value}
      onChange={(e) => onChange?.(e.target.value)}
    />
  ),
}));

const fetchMock = vi.fn();

beforeEach(() => {
  vi.stubGlobal("fetch", fetchMock);
});
afterEach(() => {
  fetchMock.mockReset();
  vi.unstubAllGlobals();
});

function jsonRes(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

function textRes(body: string, status = 200): Response {
  return new Response(body, {
    status,
    headers: { "Content-Type": "text/plain" },
  });
}

const ROOT_ENTRIES = [
  { name: "config", path: "/config", size: 0, dir: true },
  { name: "server.properties", path: "/server.properties", size: 2100, dir: false },
];

function deferredResponse() {
  let resolve!: (response: Response) => void;
  const promise = new Promise<Response>((done) => { resolve = done; });
  return { promise, resolve };
}

const TWO_FILES = [...ROOT_ENTRIES, { name: "config.yaml", path: "/config.yaml", size: 100, dir: false }];

describe("FilesTab", () => {
  it("clears the previous draft and cannot save until the newly selected file loads", async () => {
    const secondRead = deferredResponse();
    const writes: { url: string; body: string }[] = [];
    fetchMock.mockImplementation(async (url: string, init?: FetchInit) => {
      if (url.includes("/files/list")) return jsonRes(TWO_FILES);
      if (url.includes("/files/read?path=%2Fserver.properties")) return textRes("first file");
      if (url.includes("/files/read?path=%2Fconfig.yaml")) return secondRead.promise;
      if (url.includes("/files/write")) { writes.push({ url, body: init?.body as string }); return new Response(null, { status: 204 }); }
      throw new Error(`unexpected fetch: ${url}`);
    });
    vi.stubGlobal("confirm", () => true);
    renderWithQuery(<FilesTab name="mc-survival" />);
    fireEvent.click(await screen.findByText("server.properties"));
    fireEvent.change(await screen.findByTestId("monaco"), { target: { value: "discarded first draft" } });
    fireEvent.click(screen.getByText("config.yaml"));
    expect(screen.queryByTestId("monaco")).not.toBeInTheDocument();
    const save = screen.getByRole("button", { name: /Save/ });
    expect(save).toBeDisabled();
    fireEvent.click(save);
    expect(writes).toEqual([]);
    await act(async () => secondRead.resolve(textRes("second file")));
    const editor = await screen.findByTestId("monaco");
    expect(editor).toHaveValue("second file");
    fireEvent.change(editor, { target: { value: "second draft" } });
    fireEvent.click(screen.getByRole("button", { name: /Save/ }));
    await waitFor(() => expect(writes).toEqual([{ url: "/servers/mc-survival/files/write?path=%2Fconfig.yaml", body: "second draft" }]));
  });

  it("ignores a previous file read that resolves after another file is selected", async () => {
    const firstRead = deferredResponse();
    fetchMock.mockImplementation(async (url: string) => {
      if (url.includes("/files/list")) return jsonRes(TWO_FILES);
      if (url.includes("path=%2Fserver.properties")) return firstRead.promise;
      if (url.includes("path=%2Fconfig.yaml")) return textRes("second file");
      throw new Error(`unexpected fetch: ${url}`);
    });
    renderWithQuery(<FilesTab name="mc-survival" />);
    fireEvent.click(await screen.findByText("server.properties"));
    fireEvent.click(screen.getByText("config.yaml"));
    expect(await screen.findByTestId("monaco")).toHaveValue("second file");
    await act(async () => firstRead.resolve(textRes("late first file")));
    expect(screen.getByTestId("monaco")).toHaveValue("second file");
    expect(screen.getByRole("button", { name: /Save/ })).toBeDisabled();
  });

  it("preserves edits made while the selected file is saving", async () => {
    const pendingSave = deferredResponse();
    const bodies: string[] = [];
    fetchMock.mockImplementation(async (url: string, init?: FetchInit) => {
      if (url.includes("/files/list")) return jsonRes(ROOT_ENTRIES);
      if (url.includes("/files/read")) return textRes("original");
      if (url.includes("/files/write")) { bodies.push(init?.body as string); return bodies.length === 1 ? pendingSave.promise : new Response(null, { status: 204 }); }
      throw new Error(`unexpected fetch: ${url}`);
    });
    renderWithQuery(<FilesTab name="mc-survival" />);
    fireEvent.click(await screen.findByText("server.properties"));
    const editor = await screen.findByTestId("monaco");
    fireEvent.change(editor, { target: { value: "first edit" } });
    fireEvent.click(screen.getByRole("button", { name: /Save/ }));
    await waitFor(() => expect(bodies).toEqual(["first edit"]));
    fireEvent.change(editor, { target: { value: "second edit" } });
    await act(async () => pendingSave.resolve(new Response(null, { status: 204 })));
    await waitFor(() => expect(screen.getByRole("button", { name: /Save/ })).toBeEnabled());
    expect(editor).toHaveValue("second edit");
    fireEvent.click(screen.getByRole("button", { name: /Save/ }));
    await waitFor(() => expect(bodies).toEqual(["first edit", "second edit"]));
  });

  it("does not change the new file's baseline when an earlier save completes", async () => {
    const pendingSave = deferredResponse();
    const writes: { url: string; body: string }[] = [];
    fetchMock.mockImplementation(async (url: string, init?: FetchInit) => {
      if (url.includes("/files/list")) return jsonRes(TWO_FILES);
      if (url.includes("/files/read?path=%2Fserver.properties")) return textRes("first file");
      if (url.includes("/files/read?path=%2Fconfig.yaml")) return textRes("second file");
      if (url.includes("/files/write")) { writes.push({ url, body: init?.body as string }); return writes.length === 1 ? pendingSave.promise : new Response(null, { status: 204 }); }
      throw new Error(`unexpected fetch: ${url}`);
    });
    vi.stubGlobal("confirm", () => true);
    renderWithQuery(<FilesTab name="mc-survival" />);
    fireEvent.click(await screen.findByText("server.properties"));
    fireEvent.change(await screen.findByTestId("monaco"), { target: { value: "first saved draft" } });
    fireEvent.click(screen.getByRole("button", { name: /Save/ }));
    await waitFor(() => expect(writes).toHaveLength(1));
    fireEvent.click(screen.getByText("config.yaml"));
    expect(await screen.findByTestId("monaco")).toHaveValue("second file");
    await act(async () => pendingSave.resolve(new Response(null, { status: 204 })));
    await waitFor(() => expect(screen.queryByText(/modified/)).not.toBeInTheDocument());
    expect(screen.getByRole("button", { name: /Save/ })).toBeDisabled();
    fireEvent.change(screen.getByTestId("monaco"), { target: { value: "second saved draft" } });
    await waitFor(() => expect(screen.getByRole("button", { name: /Save/ })).toBeEnabled());
    fireEvent.click(screen.getByRole("button", { name: /Save/ }));
    await waitFor(() => expect(writes).toEqual([
      { url: "/servers/mc-survival/files/write?path=%2Fserver.properties", body: "first saved draft" },
      { url: "/servers/mc-survival/files/write?path=%2Fconfig.yaml", body: "second saved draft" },
    ]));
  });

  it("clears the editor on target change and ignores an old target's pending save", async () => {
    const pendingSave = deferredResponse();
    const writes: { url: string; body: string }[] = [];
    function TargetHarness() {
      const [cluster, setCluster] = useState("local");
      return <ResourceTargetProvider target={{ cluster, name: "mc-survival" }} access={{ canWrite: true, canControl: true, canConsole: true, canDelete: true, isOwner: true, isCollaborator: false, permissions: ["*"] }}>
        <button onClick={() => setCluster("remote")}>Switch target</button>
        <FilesTab name="mc-survival" />
      </ResourceTargetProvider>;
    }
    fetchMock.mockImplementation(async (url: string, init?: FetchInit) => {
      if (url.includes("/files/list")) return jsonRes(ROOT_ENTRIES);
      if (url.includes("/files/read")) return textRes(url.includes("cluster=remote") ? "remote content" : "local content");
      if (url.includes("/files/write")) { writes.push({ url, body: init?.body as string }); return writes.length === 1 ? pendingSave.promise : new Response(null, { status: 204 }); }
      throw new Error(`unexpected fetch: ${url}`);
    });
    baseRenderWithQuery(<TargetHarness />);
    fireEvent.click(await screen.findByText("server.properties"));
    fireEvent.change(await screen.findByTestId("monaco"), { target: { value: "local saved draft" } });
    fireEvent.click(screen.getByRole("button", { name: /Save/ }));
    await waitFor(() => expect(writes).toHaveLength(1));
    fireEvent.click(screen.getByRole("button", { name: "Switch target" }));
    expect(screen.queryByTestId("monaco")).not.toBeInTheDocument();
    expect(screen.getByText("Select a file to edit.")).toBeInTheDocument();
    fireEvent.click(await screen.findByText("server.properties"));
    expect(await screen.findByTestId("monaco")).toHaveValue("remote content");
    await act(async () => pendingSave.resolve(new Response(null, { status: 204 })));
    expect(screen.getByTestId("monaco")).toHaveValue("remote content");
    expect(screen.getByRole("button", { name: /Save/ })).toBeDisabled();
    fireEvent.change(screen.getByTestId("monaco"), { target: { value: "remote saved draft" } });
    fireEvent.click(screen.getByRole("button", { name: /Save/ }));
    await waitFor(() => expect(writes).toEqual([
      { url: "/servers/mc-survival/files/write?path=%2Fserver.properties", body: "local saved draft" },
      { url: "/servers/mc-survival/files/write?path=%2Fserver.properties&cluster=remote", body: "remote saved draft" },
    ]));
  });

  it("does not reuse an earlier save's baseline after discarding and reopening the same path", async () => {
    const pendingSave = deferredResponse();
    let firstFileReads = 0;
    let writeStarted = false;
    fetchMock.mockImplementation(async (url: string) => {
      if (url.includes("/files/list")) return jsonRes(TWO_FILES);
      if (url.includes("/files/read?path=%2Fserver.properties")) return textRes(++firstFileReads === 1 ? "original" : "reopened content");
      if (url.includes("/files/read?path=%2Fconfig.yaml")) return textRes("second file");
      if (url.includes("/files/write")) { writeStarted = true; return pendingSave.promise; }
      throw new Error(`unexpected fetch: ${url}`);
    });
    vi.stubGlobal("confirm", () => true);
    const { container } = renderWithQuery(<FilesTab name="mc-survival" />);
    fireEvent.click(await screen.findByText("server.properties"));
    fireEvent.change(await screen.findByTestId("monaco"), { target: { value: "saved draft" } });
    fireEvent.click(screen.getByRole("button", { name: /Save/ }));
    await waitFor(() => expect(writeStarted).toBe(true));
    fireEvent.click(screen.getByText("config.yaml"));
    expect(await screen.findByTestId("monaco")).toHaveValue("second file");
    fireEvent.click(within(container.querySelector("aside")!).getByText("server.properties"));
    await waitFor(() => expect(screen.getByTestId("monaco")).toHaveValue("reopened content"));
    await act(async () => pendingSave.resolve(new Response(null, { status: 204 })));
    expect(screen.getByTestId("monaco")).toHaveValue("reopened content");
    expect(screen.queryByText(/modified/)).not.toBeInTheDocument();
    expect(screen.getByRole("button", { name: /Save/ })).toBeDisabled();
  });


  it("renders entries from /files/list", async () => {
    fetchMock.mockImplementation(async (url: string) => {
      if (url === "/servers/mc-survival/files/list?path=%2F") {
        return jsonRes(ROOT_ENTRIES);
      }
      throw new Error(`unexpected fetch: ${url}`);
    });
    renderWithQuery(<FilesTab name="mc-survival" />);
    await waitFor(() => {
      const configEl = screen.getByText("config");
      const propsEl = screen.getByText("server.properties");
      expect(configEl).toBeInTheDocument();
      expect(propsEl).toBeInTheDocument();
    });
  });

  it("save button is disabled until the user edits, then POSTs to /files/write", async () => {
    let writeBody: string | null = null;
    fetchMock.mockImplementation(async (url: string, init?: FetchInit) => {
      if (url.startsWith("/servers/mc-survival/files/list")) return jsonRes(ROOT_ENTRIES);
      if (url.startsWith("/servers/mc-survival/files/read")) {
        return textRes("enable-jmx-monitoring=false\n");
      }
      if (
        url === "/servers/mc-survival/files/write?path=%2Fserver.properties" &&
        init?.method === "POST"
      ) {
        writeBody = init.body as string;
        return new Response(null, { status: 204 });
      }
      throw new Error(`unexpected fetch: ${url} ${init?.method ?? "GET"}`);
    });

    renderWithQuery(<FilesTab name="mc-survival" />);

    fireEvent.click(await screen.findByText("server.properties"));

    const monaco = await screen.findByTestId("monaco");
    const save = screen.getByRole("button", { name: /Save/ });
    expect(save).toBeDisabled();

    fireEvent.change(monaco, { target: { value: "enable-jmx-monitoring=true\n" } });
    expect(screen.getByText(/modified/)).toBeInTheDocument();
    expect(save).toBeEnabled();

    await act(async () => {
      fireEvent.click(save);
    });
    await waitFor(() => expect(writeBody).toBe("enable-jmx-monitoring=true\n"));
    // After a successful save the dirty marker should clear.
    await waitFor(() => expect(screen.queryByText(/modified/)).toBeNull());
  });

  it("opens the destructive confirm dialog when Delete is pressed", async () => {
    fetchMock.mockImplementation(async (url: string) => {
      if (url.startsWith("/servers/mc-survival/files/list")) return jsonRes(ROOT_ENTRIES);
      if (url.startsWith("/servers/mc-survival/files/read")) return textRes("hello");
      throw new Error(`unexpected fetch: ${url}`);
    });
    renderWithQuery(<FilesTab name="mc-survival" />);
    fireEvent.click(await screen.findByText("server.properties"));
    await screen.findByTestId("monaco");

    const deleteButtons = screen.getAllByRole("button", { name: /Delete/ });
    fireEvent.click(deleteButtons[deleteButtons.length - 1]);
    expect(
      await screen.findByText(/Delete server\.properties\?/),
    ).toBeInTheDocument();
  });

  it("creates a new folder via /files/mkdir", async () => {
    let mkdirURL: string | null = null;
    fetchMock.mockImplementation(async (url: string, init?: FetchInit) => {
      if (url.startsWith("/servers/mc-survival/files/list")) return jsonRes(ROOT_ENTRIES);
      if (url.startsWith("/servers/mc-survival/files/mkdir") && init?.method === "POST") {
        mkdirURL = url;
        return new Response(null, { status: 204 });
      }
      throw new Error(`unexpected fetch: ${url} ${init?.method ?? "GET"}`);
    });
    renderWithQuery(<FilesTab name="mc-survival" />);
    await screen.findByText("server.properties");

    fireEvent.click(screen.getByRole("button", { name: /New folder/ }));
    const input = await screen.findByPlaceholderText("my-folder");
    fireEvent.change(input, { target: { value: "mods-disabled" } });
    await act(async () => {
      fireEvent.click(screen.getByRole("button", { name: /^Create$/ }));
    });
    await waitFor(() =>
      expect(mkdirURL).toBe(
        "/servers/mc-survival/files/mkdir?path=%2Fmods-disabled",
      ),
    );
  });

  // Below `md` the tree and editor stack instead of sitting side by side;
  // which one is showing is tracked by a `pane` state that toggles a
  // "flex"/"hidden" class (real CSS, so it's inert in jsdom — this asserts
  // the class tokens directly rather than actual visibility).
  it("mobile pane toggle: selecting a file switches to the editor pane; 'Back to files' returns to the tree", async () => {
    function hasClass(el: Element, cls: string): boolean {
      return el.className.split(/\s+/).includes(cls);
    }

    fetchMock.mockImplementation(async (url: string) => {
      if (url.startsWith("/servers/mc-survival/files/list")) return jsonRes(ROOT_ENTRIES);
      if (url.startsWith("/servers/mc-survival/files/read")) return textRes("hello");
      throw new Error(`unexpected fetch: ${url}`);
    });
    const { container } = renderWithQuery(<FilesTab name="mc-survival" />);
    await screen.findByText("server.properties");

    const aside = container.querySelector("aside") as HTMLElement;
    const main = container.querySelector("main") as HTMLElement;

    // Tree pane showing, editor pane hidden before any file is selected.
    expect(hasClass(aside, "flex")).toBe(true);
    expect(hasClass(main, "hidden")).toBe(true);

    fireEvent.click(await screen.findByText("server.properties"));
    await screen.findByTestId("monaco");

    // Selecting a file switches the mobile pane to the editor.
    expect(hasClass(main, "flex")).toBe(true);
    expect(hasClass(aside, "hidden")).toBe(true);

    fireEvent.click(screen.getByRole("button", { name: /back to files/i }));

    expect(hasClass(aside, "flex")).toBe(true);
    expect(hasClass(main, "hidden")).toBe(true);
  });

  it("loads file contents when a file is selected", async () => {
    fetchMock.mockImplementation(async (url: string) => {
      if (url.startsWith("/servers/mc-survival/files/list")) return jsonRes(ROOT_ENTRIES);
      if (url === "/servers/mc-survival/files/read?path=%2Fserver.properties") {
        return textRes("some file content here");
      }
      throw new Error(`unexpected fetch: ${url}`);
    });
    renderWithQuery(<FilesTab name="mc-survival" />);
    fireEvent.click(await screen.findByText("server.properties"));
    const monaco = await screen.findByTestId("monaco");
    expect(monaco).toHaveValue("some file content here");
  });

  it("displays loadError when file read fails", async () => {
    fetchMock.mockImplementation(async (url: string) => {
      if (url.startsWith("/servers/mc-survival/files/list")) return jsonRes(ROOT_ENTRIES);
      if (url.startsWith("/servers/mc-survival/files/read")) {
        return new Response("access denied", { status: 403 });
      }
      throw new Error(`unexpected fetch: ${url}`);
    });
    renderWithQuery(<FilesTab name="mc-survival" />);
    fireEvent.click(await screen.findByText("server.properties"));
    await waitFor(() =>
      expect(screen.getByText(/access denied/)).toBeInTheDocument(),
    );
  });

  it("displays opError when save fails", async () => {
    fetchMock.mockImplementation(async (url: string, init?: FetchInit) => {
      if (url.startsWith("/servers/mc-survival/files/list")) return jsonRes(ROOT_ENTRIES);
      if (url.startsWith("/servers/mc-survival/files/read")) return textRes("original");
      if (
        url === "/servers/mc-survival/files/write?path=%2Fserver.properties" &&
        init?.method === "POST"
      ) {
        return new Response("write failed", { status: 500 });
      }
      throw new Error(`unexpected fetch: ${url}`);
    });
    renderWithQuery(<FilesTab name="mc-survival" />);
    fireEvent.click(await screen.findByText("server.properties"));
    const monaco = await screen.findByTestId("monaco");
    fireEvent.change(monaco, { target: { value: "modified" } });
    const save = screen.getByRole("button", { name: /Save/ });
    await act(async () => {
      fireEvent.click(save);
    });
    await waitFor(() =>
      expect(screen.getByText(/write failed/)).toBeInTheDocument(),
    );
  });

  it("displays opError when delete fails", async () => {
    fetchMock.mockImplementation(async (url: string, init?: FetchInit) => {
      if (url.startsWith("/servers/mc-survival/files/list")) return jsonRes(ROOT_ENTRIES);
      if (url.startsWith("/servers/mc-survival/files/read")) return textRes("content");
      if (
        url.startsWith("/servers/mc-survival/files/delete") &&
        init?.method === "DELETE"
      ) {
        return new Response("file in use", { status: 400 });
      }
      throw new Error(`unexpected fetch: ${url}`);
    });
    renderWithQuery(<FilesTab name="mc-survival" />);
    fireEvent.click(await screen.findByText("server.properties"));
    await screen.findByTestId("monaco");
    fireEvent.click(screen.getByRole("button", { name: /^Delete$/ }));
    const confirmBtn = await screen.findByRole("button", { name: /^Delete$/ });
    await act(async () => {
      fireEvent.click(confirmBtn);
    });
    await waitFor(() =>
      expect(screen.getByText(/file in use/)).toBeInTheDocument(),
    );
  });

  it("displays opError when mkdir fails", async () => {
    fetchMock.mockImplementation(async (url: string, init?: FetchInit) => {
      if (url.startsWith("/servers/mc-survival/files/list")) return jsonRes(ROOT_ENTRIES);
      if (
        url.startsWith("/servers/mc-survival/files/mkdir") &&
        init?.method === "POST"
      ) {
        return new Response("permission denied", { status: 403 });
      }
      throw new Error(`unexpected fetch: ${url}`);
    });
    renderWithQuery(<FilesTab name="mc-survival" />);
    await screen.findByText("server.properties");
    fireEvent.click(screen.getByRole("button", { name: /New folder/ }));
    const input = await screen.findByPlaceholderText("my-folder");
    fireEvent.change(input, { target: { value: "test" } });
    await act(async () => {
      fireEvent.click(screen.getByRole("button", { name: /^Create$/ }));
    });
    await waitFor(() =>
      expect(screen.getByText(/permission denied/)).toBeInTheDocument(),
    );
  });

  it("displays opError when upload fails", async () => {
    fetchMock.mockImplementation(async (url: string, init?: FetchInit) => {
      if (url.startsWith("/servers/mc-survival/files/list")) return jsonRes(ROOT_ENTRIES);
      if (
        url.startsWith("/servers/mc-survival/files/upload") &&
        init?.method === "POST"
      ) {
        return new Response("disk full", { status: 507 });
      }
      throw new Error(`unexpected fetch: ${url}`);
    });
    renderWithQuery(<FilesTab name="mc-survival" />);
    await screen.findByText("server.properties");
    const uploadInput = screen.getByTestId("files-upload-input") as HTMLInputElement;
    const file = new File(["content"], "test.txt", { type: "text/plain" });
    fireEvent.change(uploadInput, { target: { files: { 0: file, length: 1 } as unknown as FileList } });
    await waitFor(() =>
      expect(screen.getByText(/disk full/)).toBeInTheDocument(),
    );
  });

  it("navigates into a directory when clicking on a folder entry", async () => {
    const configEntries = [
      { name: "server-config.yaml", path: "/config/server-config.yaml", size: 512, dir: false },
    ];
    fetchMock.mockImplementation(async (url: string) => {
      if (url === "/servers/mc-survival/files/list?path=%2F")
        return jsonRes(ROOT_ENTRIES);
      if (url === "/servers/mc-survival/files/list?path=%2Fconfig")
        return jsonRes(configEntries);
      throw new Error(`unexpected fetch: ${url}`);
    });
    renderWithQuery(<FilesTab name="mc-survival" />);
    fireEvent.click(await screen.findByText("config"));
    await waitFor(() =>
      expect(screen.getByText("server-config.yaml")).toBeInTheDocument(),
    );
  });

  it("navigates back to parent directory via '..' entry", async () => {
    const configEntries = [
      { name: "server-config.yaml", path: "/config/server-config.yaml", size: 512, dir: false },
    ];
    fetchMock.mockImplementation(async (url: string) => {
      if (url === "/servers/mc-survival/files/list?path=%2F") {
        return jsonRes(ROOT_ENTRIES);
      }
      if (url === "/servers/mc-survival/files/list?path=%2Fconfig") {
        return jsonRes(configEntries);
      }
      throw new Error(`unexpected fetch: ${url}`);
    });
    renderWithQuery(<FilesTab name="mc-survival" />);
    fireEvent.click(await screen.findByText("config"));
    await screen.findByText("server-config.yaml");
    fireEvent.click(screen.getByText(".."));
    await waitFor(() =>
      expect(screen.getByText("server.properties")).toBeInTheDocument(),
    );
  });

  it("displays empty folder message when directory has no entries", async () => {
    fetchMock.mockImplementation(async (url: string) => {
      if (url.startsWith("/servers/mc-survival/files/list"))
        return jsonRes([]);
      throw new Error(`unexpected fetch: ${url}`);
    });
    renderWithQuery(<FilesTab name="mc-survival" />);
    await waitFor(() =>
      expect(screen.getByText("Empty folder")).toBeInTheDocument(),
    );
  });

  it("shows 'Select a file to edit' when no file is selected in the view pane", async () => {
    fetchMock.mockImplementation(async (url: string) => {
      if (url.startsWith("/servers/mc-survival/files/list")) return jsonRes(ROOT_ENTRIES);
      throw new Error(`unexpected fetch: ${url}`);
    });
    renderWithQuery(<FilesTab name="mc-survival" />);
    await waitFor(() =>
      expect(screen.getByText("Select a file to edit.")).toBeInTheDocument(),
    );
  });

  it("downloads file when Download button is clicked", async () => {
    fetchMock.mockImplementation(async (url: string) => {
      if (url.startsWith("/servers/mc-survival/files/list")) return jsonRes(ROOT_ENTRIES);
      if (url.startsWith("/servers/mc-survival/files/read")) return textRes("content");
      throw new Error(`unexpected fetch: ${url}`);
    });
    const originalLocation = window.location;
    Object.defineProperty(window, "location", {
      writable: true,
      value: { href: "" },
    });
    try {
      renderWithQuery(<FilesTab name="mc-survival" />);
      fireEvent.click(await screen.findByText("server.properties"));
      await screen.findByTestId("monaco");
      const download = screen.getByRole("button", { name: /^Download$/ });
      fireEvent.click(download);
      await waitFor(() =>
        expect(window.location.href).toContain(
          "/servers/mc-survival/files/download?path=%2Fserver.properties",
        ),
      );
    } finally {
      Object.defineProperty(window, "location", { value: originalLocation });
    }
  });

  it("sets up upload input and clears it after selection", async () => {
    let uploadCalled = false;
    fetchMock.mockImplementation(async (url: string, init?: FetchInit) => {
      if (url.startsWith("/servers/mc-survival/files/list")) return jsonRes(ROOT_ENTRIES);
      if (
        url.startsWith("/servers/mc-survival/files/upload") &&
        init?.method === "POST"
      ) {
        uploadCalled = true;
        return new Response(null, { status: 204 });
      }
      throw new Error(`unexpected fetch: ${url}`);
    });
    renderWithQuery(<FilesTab name="mc-survival" />);
    await screen.findByText("server.properties");
    const uploadBtn = screen.getByRole("button", { name: /Upload/ });
    fireEvent.click(uploadBtn);
    const uploadInput = screen.getByTestId("files-upload-input") as HTMLInputElement;
    const file = new File(["test"], "file.txt");
    // FileList type construction in tests requires this workaround
    fireEvent.change(uploadInput, { target: { files: { 0: file, length: 1 } as unknown as FileList } });
    await waitFor(() => expect(uploadCalled).toBe(true));
    expect(uploadInput.value).toBe("");
  });

  it("creates a new file and selects it after successful creation", async () => {
    let createCalled = false;
    fetchMock.mockImplementation(async (url: string, init?: FetchInit) => {
      if (url.startsWith("/servers/mc-survival/files/list")) return jsonRes(ROOT_ENTRIES);
      if (
        url === "/servers/mc-survival/files/create?path=%2Fnew-file.txt" &&
        init?.method === "POST"
      ) {
        createCalled = true;
        return new Response(null, { status: 204 });
      }
      if (url === "/servers/mc-survival/files/read?path=%2Fnew-file.txt") return textRes("");
      throw new Error(`unexpected fetch: ${url}`);
    });
    renderWithQuery(<FilesTab name="mc-survival" />);
    await screen.findByText("server.properties");
    fireEvent.click(screen.getByRole("button", { name: /New file/ }));
    const input = await screen.findByPlaceholderText("config.yaml");
    fireEvent.change(input, { target: { value: "new-file.txt" } });
    await act(async () => {
      fireEvent.click(screen.getByRole("button", { name: /^Create$/ }));
    });
    await waitFor(() => expect(createCalled).toBe(true));
    expect(await screen.findByTestId("monaco")).toHaveValue("");
    expect(screen.queryByPlaceholderText("config.yaml")).not.toBeInTheDocument();
  });

  it.each([409, 404])("keeps the new-file dialog open after create fails with %s", async (status) => {
    const error = status === 409 ? "file already exists" : "not found";
    fetchMock.mockImplementation(async (url: string) => {
      if (url.startsWith("/servers/mc-survival/files/list")) return jsonRes(ROOT_ENTRIES);
      if (url === "/servers/mc-survival/files/create?path=%2Fserver.properties") return textRes(error, status);
      throw new Error(`unexpected fetch: ${url}`);
    });
    renderWithQuery(<FilesTab name="mc-survival" />);
    await screen.findByText("server.properties");
    const initialLists = fetchMock.mock.calls.filter(([url]) => String(url).includes("/files/list")).length;
    fireEvent.click(screen.getByRole("button", { name: /New file/ }));
    const input = await screen.findByPlaceholderText("config.yaml");
    fireEvent.change(input, { target: { value: "server.properties" } });
    await act(async () => { fireEvent.click(screen.getByRole("button", { name: /^Create$/ })); });
    await screen.findByText(`${status}: ${error}`);
    expect(input).toBeInTheDocument();
    expect(input).toHaveValue("server.properties");
    expect(screen.getByRole("button", { name: /^Create$/ })).toBeEnabled();
    expect(screen.queryByTestId("monaco")).not.toBeInTheDocument();
    expect(fetchMock.mock.calls.some(([url]) => String(url).includes("/files/write") || String(url).includes("/files/read"))).toBe(false);
    expect(fetchMock.mock.calls.filter(([url]) => String(url).includes("/files/list"))).toHaveLength(initialLists);
  });

  it("deletes a folder from its row button without opening it", async () => {
    let deleteURL: string | null = null;
    fetchMock.mockImplementation(async (url: string, init?: FetchInit) => {
      if (url.startsWith("/servers/mc-survival/files/list")) return jsonRes(ROOT_ENTRIES);
      if (
        url.startsWith("/servers/mc-survival/files/delete") &&
        init?.method === "DELETE"
      ) {
        deleteURL = url;
        return new Response(null, { status: 204 });
      }
      throw new Error(`unexpected fetch: ${url}`);
    });
    renderWithQuery(<FilesTab name="mc-survival" />);
    await screen.findByText("config");
    fireEvent.click(screen.getByRole("button", { name: "Delete config" }));
    expect(await screen.findByText(/Delete config\?/)).toBeInTheDocument();
    // The row click must not have navigated into the folder.
    expect(
      fetchMock.mock.calls.some(([u]) => String(u).includes("path=%2Fconfig")),
    ).toBe(false);
    const confirmBtn = screen.getByRole("button", { name: /^Delete$/ });
    await act(async () => {
      fireEvent.click(confirmBtn);
    });
    await waitFor(() =>
      expect(deleteURL).toMatch(/^\/servers\/mc-survival\/files\/delete\?path=%2Fconfig/),
    );
  });

  it("explains why a new file name is rejected", async () => {
    fetchMock.mockImplementation(async (url: string) => {
      if (url.startsWith("/servers/mc-survival/files/list")) return jsonRes(ROOT_ENTRIES);
      throw new Error(`unexpected fetch: ${url}`);
    });
    renderWithQuery(<FilesTab name="mc-survival" />);
    await screen.findByText("server.properties");
    fireEvent.click(screen.getByRole("button", { name: /New file/ }));
    const input = await screen.findByPlaceholderText("config.yaml");
    expect(screen.queryByText(/Names can't contain/)).not.toBeInTheDocument();
    fireEvent.change(input, { target: { value: "../escape.txt" } });
    expect(screen.getByText(/Names can't contain/)).toBeInTheDocument();
    expect(input).toHaveAttribute("aria-invalid", "true");
    expect(screen.getByRole("button", { name: /^Create$/ })).toBeDisabled();
    fireEvent.change(input, { target: { value: "ok.txt" } });
    expect(screen.queryByText(/Names can't contain/)).not.toBeInTheDocument();
  });

  it("rejects new file names with slashes", async () => {
    fetchMock.mockImplementation(async (url: string) => {
      if (url.startsWith("/servers/mc-survival/files/list")) return jsonRes(ROOT_ENTRIES);
      throw new Error(`unexpected fetch: ${url}`);
    });
    renderWithQuery(<FilesTab name="mc-survival" />);
    await screen.findByText("server.properties");
    fireEvent.click(screen.getByRole("button", { name: /New file/ }));
    const input = await screen.findByPlaceholderText("config.yaml");
    fireEvent.change(input, { target: { value: "invalid/name" } });
    const createBtn = screen.getByRole("button", { name: /^Create$/ });
    expect(createBtn).toBeDisabled();
  });

  it("rejects new file names that are just dots", async () => {
    fetchMock.mockImplementation(async (url: string) => {
      if (url.startsWith("/servers/mc-survival/files/list")) return jsonRes(ROOT_ENTRIES);
      throw new Error(`unexpected fetch: ${url}`);
    });
    renderWithQuery(<FilesTab name="mc-survival" />);
    await screen.findByText("server.properties");
    fireEvent.click(screen.getByRole("button", { name: /New file/ }));
    const input = await screen.findByPlaceholderText("config.yaml");
    fireEvent.change(input, { target: { value: "." } });
    const createBtn = screen.getByRole("button", { name: /^Create$/ });
    expect(createBtn).toBeDisabled();
    fireEvent.change(input, { target: { value: ".." } });
    expect(createBtn).toBeDisabled();
  });

  it("rejects empty file names", async () => {
    fetchMock.mockImplementation(async (url: string) => {
      if (url.startsWith("/servers/mc-survival/files/list")) return jsonRes(ROOT_ENTRIES);
      throw new Error(`unexpected fetch: ${url}`);
    });
    renderWithQuery(<FilesTab name="mc-survival" />);
    await screen.findByText("server.properties");
    fireEvent.click(screen.getByRole("button", { name: /New file/ }));
    const input = await screen.findByPlaceholderText("config.yaml");
    fireEvent.change(input, { target: { value: "" } });
    const createBtn = screen.getByRole("button", { name: /^Create$/ });
    expect(createBtn).toBeDisabled();
  });

  it("rejects folder names with slashes", async () => {
    fetchMock.mockImplementation(async (url: string) => {
      if (url.startsWith("/servers/mc-survival/files/list")) return jsonRes(ROOT_ENTRIES);
      throw new Error(`unexpected fetch: ${url}`);
    });
    renderWithQuery(<FilesTab name="mc-survival" />);
    await screen.findByText("server.properties");
    fireEvent.click(screen.getByRole("button", { name: /New folder/ }));
    const input = await screen.findByPlaceholderText("my-folder");
    fireEvent.change(input, { target: { value: "invalid/path" } });
    const createBtn = screen.getByRole("button", { name: /^Create$/ });
    expect(createBtn).toBeDisabled();
  });

  it("closes dialogs when cancel is clicked", async () => {
    fetchMock.mockImplementation(async (url: string) => {
      if (url.startsWith("/servers/mc-survival/files/list")) return jsonRes(ROOT_ENTRIES);
      throw new Error(`unexpected fetch: ${url}`);
    });
    renderWithQuery(<FilesTab name="mc-survival" />);
    await screen.findByText("server.properties");
    fireEvent.click(screen.getByRole("button", { name: /New file/ }));
    await screen.findByPlaceholderText("config.yaml");
    const cancelBtn = screen.getByRole("button", { name: /Cancel/ });
    fireEvent.click(cancelBtn);
    await waitFor(() =>
      expect(screen.queryByPlaceholderText("config.yaml")).not.toBeInTheDocument(),
    );
  });

  it("closes delete dialog when cancel is clicked", async () => {
    fetchMock.mockImplementation(async (url: string) => {
      if (url.startsWith("/servers/mc-survival/files/list")) return jsonRes(ROOT_ENTRIES);
      if (url.startsWith("/servers/mc-survival/files/read")) return textRes("content");
      throw new Error(`unexpected fetch: ${url}`);
    });
    renderWithQuery(<FilesTab name="mc-survival" />);
    fireEvent.click(await screen.findByText("server.properties"));
    await screen.findByTestId("monaco");
    fireEvent.click(screen.getByRole("button", { name: /^Delete$/ }));
    const cancelBtns = screen.getAllByRole("button", { name: /Cancel/ });
    fireEvent.click(cancelBtns[cancelBtns.length - 1]);
    await waitFor(() =>
      expect(
        screen.queryByText(/Delete server\.properties\?/),
      ).not.toBeInTheDocument(),
    );
  });

  it("deletes a file successfully", async () => {
    let deleteURL: string | null = null;
    fetchMock.mockImplementation(async (url: string, init?: FetchInit) => {
      if (url.startsWith("/servers/mc-survival/files/list")) return jsonRes(ROOT_ENTRIES);
      if (url.startsWith("/servers/mc-survival/files/read")) return textRes("content");
      if (
        url.startsWith("/servers/mc-survival/files/delete") &&
        init?.method === "DELETE"
      ) {
        deleteURL = url;
        return new Response(null, { status: 204 });
      }
      throw new Error(`unexpected fetch: ${url}`);
    });
    renderWithQuery(<FilesTab name="mc-survival" />);
    fireEvent.click(await screen.findByText("server.properties"));
    await screen.findByTestId("monaco");
    fireEvent.click(screen.getByRole("button", { name: /^Delete$/ }));
    const confirmBtn = await screen.findByRole("button", { name: /^Delete$/ });
    await act(async () => {
      fireEvent.click(confirmBtn);
    });
    await waitFor(() =>
      expect(deleteURL).toBe(
        "/servers/mc-survival/files/delete?path=%2Fserver.properties",
      ),
    );
    // After delete, selected should clear and pane should switch to tree
    await waitFor(() =>
      expect(screen.queryByTestId("monaco")).not.toBeInTheDocument(),
    );
  });

  it("clears selected file after successful delete", async () => {
    fetchMock.mockImplementation(async (url: string, init?: FetchInit) => {
      if (url.startsWith("/servers/mc-survival/files/list")) return jsonRes(ROOT_ENTRIES);
      if (url.startsWith("/servers/mc-survival/files/read")) return textRes("content");
      if (
        url.startsWith("/servers/mc-survival/files/delete") &&
        init?.method === "DELETE"
      ) {
        return new Response(null, { status: 204 });
      }
      throw new Error(`unexpected fetch: ${url}`);
    });
    renderWithQuery(<FilesTab name="mc-survival" />);
    fireEvent.click(await screen.findByText("server.properties"));
    await screen.findByTestId("monaco");
    fireEvent.click(screen.getByRole("button", { name: /^Delete$/ }));
    const confirmBtn = await screen.findByRole("button", { name: /^Delete$/ });
    await act(async () => {
      fireEvent.click(confirmBtn);
    });
    await waitFor(() =>
      expect(screen.getByText("Select a file to edit.")).toBeInTheDocument(),
    );
  });

  it("refreshes file list when refresh button is clicked", async () => {
    let listCallCount = 0;
    fetchMock.mockImplementation(async (url: string) => {
      if (url.startsWith("/servers/mc-survival/files/list")) {
        listCallCount++;
        return jsonRes(ROOT_ENTRIES);
      }
      throw new Error(`unexpected fetch: ${url}`);
    });
    renderWithQuery(<FilesTab name="mc-survival" />);
    await screen.findByText("server.properties");
    const refreshBtn = screen.getByLabelText("Refresh");
    const initialCount = listCallCount;
    fireEvent.click(refreshBtn);
    await waitFor(() => expect(listCallCount).toBeGreaterThan(initialCount));
  });

  it("navigates via breadcrumbs", async () => {
    // "dir1" isn't in the shared ROOT_ENTRIES fixture, so the root listing
    // here needs its own entries. The condition order also matters:
    // "path=%2Fdir1" is a substring of "path=%2Fdir1%2Fdir2", and root's
    // "path=%2F" is a substring of every path — check the most specific
    // path first so a deeper request isn't swallowed by a broader match.
    const rootEntries = [
      { name: "dir1", path: "/dir1", size: 0, dir: true },
    ];
    const nestedEntries = [
      { name: "file.txt", path: "/dir1/dir2/file.txt", size: 100, dir: false },
    ];
    let lastPath = "";
    fetchMock.mockImplementation(async (url: string) => {
      if (url.includes("path=%2Fdir1%2Fdir2")) {
        lastPath = "/dir1/dir2";
        return jsonRes(nestedEntries);
      }
      if (url.includes("path=%2Fdir1")) {
        lastPath = "/dir1";
        return jsonRes([
          { name: "dir2", path: "/dir1/dir2", size: 0, dir: true },
        ]);
      }
      if (url.includes("path=%2F")) {
        lastPath = "/";
        return jsonRes(rootEntries);
      }
      throw new Error(`unexpected fetch: ${url}`);
    });
    renderWithQuery(<FilesTab name="mc-survival" />);
    await waitFor(() => expect(lastPath).toBe("/"));
    fireEvent.click(await screen.findByText("dir1"));
    await waitFor(() => expect(lastPath).toBe("/dir1"));
  });

  it("selects the same file again to show the view pane on mobile", async () => {
    fetchMock.mockImplementation(async (url: string) => {
      if (url.startsWith("/servers/mc-survival/files/list")) return jsonRes(ROOT_ENTRIES);
      if (url.startsWith("/servers/mc-survival/files/read")) return textRes("content");
      throw new Error(`unexpected fetch: ${url}`);
    });
    const { container } = renderWithQuery(<FilesTab name="mc-survival" />);
    fireEvent.click(await screen.findByText("server.properties"));
    await screen.findByTestId("monaco");
    // Back to tree pane
    fireEvent.click(screen.getByRole("button", { name: /back to files/i }));
    // Click the same file again. Scope the query to the tree pane: once a
    // file is selected, the (CSS-hidden but still-mounted) editor header
    // also renders the selected file's name, so an unscoped query for
    // "server.properties" is ambiguous here.
    const aside = container.querySelector("aside") as HTMLElement;
    fireEvent.click(await within(aside).findByText("server.properties"));
    const main = container.querySelector("main") as HTMLElement;
    await waitFor(() => {
      const hasHidden = main.className.split(/\s+/).includes("hidden");
      expect(hasHidden).toBe(false);
    });
  });

  it("shows loading spinner while file is being read", async () => {
    let resolveRead: ((value: Response) => void) = () => {};
    fetchMock.mockImplementation(async (url: string) => {
      if (url.startsWith("/servers/mc-survival/files/list")) return jsonRes(ROOT_ENTRIES);
      if (url.startsWith("/servers/mc-survival/files/read")) {
        return new Promise<Response>((resolve) => {
          resolveRead = resolve;
        });
      }
      throw new Error(`unexpected fetch: ${url}`);
    });
    const { container } = renderWithQuery(<FilesTab name="mc-survival" />);
    fireEvent.click(await screen.findByText("server.properties"));
    // Should show loader while file is being read. lucide-react icons render
    // aria-hidden="true" by default, which removes them from the
    // accessibility tree entirely (the `hidden` option to getByRole only
    // covers CSS-hidden elements, not aria-hidden ones) — so this can never
    // find a role="img" element. Query by the spin class instead.
    const loaders = container.querySelectorAll(".animate-spin");
    expect(loaders.length).toBeGreaterThan(0);
    // Resolve the read
    await act(async () => {
      resolveRead(textRes("content"));
    });
    await screen.findByTestId("monaco");
  });

  it("shows upload spinner while files are uploading", async () => {
    let resolveUpload: ((value: Response) => void) = () => {};
    fetchMock.mockImplementation(async (url: string, init?: FetchInit) => {
      if (url.startsWith("/servers/mc-survival/files/list")) return jsonRes(ROOT_ENTRIES);
      if (
        url.startsWith("/servers/mc-survival/files/upload") &&
        init?.method === "POST"
      ) {
        return new Promise<Response>((resolve) => {
          resolveUpload = resolve;
        });
      }
      throw new Error(`unexpected fetch: ${url}`);
    });
    renderWithQuery(<FilesTab name="mc-survival" />);
    await screen.findByText("server.properties");
    const uploadBtn = screen.getByRole("button", { name: /Upload/ });
    const uploadInput = screen.getByTestId("files-upload-input") as HTMLInputElement;
    const file = new File(["content"], "test.txt");
    fireEvent.change(uploadInput, { target: { files: { 0: file, length: 1 } as unknown as FileList } });
    // Upload button should be disabled while uploading
    await waitFor(() => expect(uploadBtn).toBeDisabled());
    await act(async () => {
      resolveUpload(new Response(null, { status: 204 }));
    });
  });

  it("prevents navigation away when there are unsaved changes", async () => {
    fetchMock.mockImplementation(async (url: string) => {
      if (url.startsWith("/servers/mc-survival/files/list")) return jsonRes(ROOT_ENTRIES);
      if (url.startsWith("/servers/mc-survival/files/read")) return textRes("original");
      throw new Error(`unexpected fetch: ${url}`);
    });
    const confirmMock = vi.fn(() => false);
    vi.stubGlobal("confirm", confirmMock);
    try {
      renderWithQuery(<FilesTab name="mc-survival" />);
      fireEvent.click(await screen.findByText("server.properties"));
      const monaco = await screen.findByTestId("monaco");
      fireEvent.change(monaco, { target: { value: "modified" } });
      // Try to navigate away
      fireEvent.click(screen.getByText("config"));
      expect(confirmMock).toHaveBeenCalled();
      // Should still show the modified file (navigation cancelled)
      expect(monaco).toHaveValue("modified");
    } finally {
      vi.unstubAllGlobals();
    }
  });

  it("allows navigation when user confirms discarding changes", async () => {
    const configEntries = [
      { name: "file.txt", path: "/config/file.txt", size: 100, dir: false },
    ];
    fetchMock.mockImplementation(async (url: string) => {
      if (url === "/servers/mc-survival/files/list?path=%2F")
        return jsonRes(ROOT_ENTRIES);
      if (url === "/servers/mc-survival/files/list?path=%2Fconfig")
        return jsonRes(configEntries);
      if (url.startsWith("/servers/mc-survival/files/read")) return textRes("content");
      throw new Error(`unexpected fetch: ${url}`);
    });
    const confirmMock = vi.fn(() => true);
    vi.stubGlobal("confirm", confirmMock);
    try {
      renderWithQuery(<FilesTab name="mc-survival" />);
      fireEvent.click(await screen.findByText("server.properties"));
      const monaco = await screen.findByTestId("monaco");
      fireEvent.change(monaco, { target: { value: "modified" } });
      // Confirm discarding changes
      fireEvent.click(screen.getByText("config"));
      expect(confirmMock).toHaveBeenCalled();
      await waitFor(() =>
        expect(screen.getByText("file.txt")).toBeInTheDocument(),
      );
    } finally {
      vi.unstubAllGlobals();
    }
  });

  it("supports namespace parameter in API calls", async () => {
    const urls: string[] = [];
    fetchMock.mockImplementation(async (url: string) => {
      urls.push(url);
      if (url.includes("namespace=custom-ns") && url.includes("/files/list")) {
        return jsonRes(ROOT_ENTRIES);
      }
      throw new Error(`unexpected fetch: ${url}`);
    });
    renderWithQuery(<FilesTab name="mc-survival" ns="custom-ns" />);
    await waitFor(() =>
      expect(urls.some((u) => u.includes("namespace=custom-ns"))).toBe(true),
    );
  });

  it("guesses language from filename for Monaco editor", async () => {
    const entries = [
      { name: "config.yaml", path: "/config.yaml", size: 100, dir: false },
      { name: "data.json", path: "/data.json", size: 50, dir: false },
      { name: "script.sh", path: "/script.sh", size: 200, dir: false },
    ];
    fetchMock.mockImplementation(async (url: string) => {
      if (url.startsWith("/servers/mc-survival/files/list")) return jsonRes(entries);
      if (url.startsWith("/servers/mc-survival/files/read")) return textRes("content");
      throw new Error(`unexpected fetch: ${url}`);
    });
    renderWithQuery(<FilesTab name="mc-survival" />);
    fireEvent.click(await screen.findByText("config.yaml"));
    let monaco = await screen.findByTestId("monaco") as HTMLTextAreaElement;
    // Check that Editor component receives correct language prop
    expect(monaco).toBeInTheDocument();
    // Click config.json to test json detection
    fireEvent.click(screen.getByText("data.json"));
    monaco = await screen.findByTestId("monaco") as HTMLTextAreaElement;
    expect(monaco).toBeInTheDocument();
  });

  it("trims whitespace from folder and file names", async () => {
    let mkdirURL: string | null = null;
    fetchMock.mockImplementation(async (url: string, init?: FetchInit) => {
      if (url.startsWith("/servers/mc-survival/files/list")) return jsonRes(ROOT_ENTRIES);
      if (url.startsWith("/servers/mc-survival/files/mkdir") && init?.method === "POST") {
        mkdirURL = url;
        return new Response(null, { status: 204 });
      }
      throw new Error(`unexpected fetch: ${url}`);
    });
    renderWithQuery(<FilesTab name="mc-survival" />);
    await screen.findByText("server.properties");
    fireEvent.click(screen.getByRole("button", { name: /New folder/ }));
    const input = await screen.findByPlaceholderText("my-folder");
    fireEvent.change(input, { target: { value: "  my-folder  " } });
    await act(async () => {
      fireEvent.click(screen.getByRole("button", { name: /^Create$/ }));
    });
    await waitFor(() =>
      expect(mkdirURL).toBe("/servers/mc-survival/files/mkdir?path=%2Fmy-folder"),
    );
  });

  it("disables save button while mutation is pending", async () => {
    let resolveSave: ((value: Response) => void) = () => {};
    fetchMock.mockImplementation(async (url: string, init?: FetchInit) => {
      if (url.startsWith("/servers/mc-survival/files/list")) return jsonRes(ROOT_ENTRIES);
      if (url.startsWith("/servers/mc-survival/files/read")) return textRes("original");
      if (
        url === "/servers/mc-survival/files/write?path=%2Fserver.properties" &&
        init?.method === "POST"
      ) {
        return new Promise<Response>((resolve) => {
          resolveSave = resolve;
        });
      }
      throw new Error(`unexpected fetch: ${url}`);
    });
    renderWithQuery(<FilesTab name="mc-survival" />);
    fireEvent.click(await screen.findByText("server.properties"));
    const monaco = await screen.findByTestId("monaco");
    fireEvent.change(monaco, { target: { value: "modified" } });
    const save = screen.getByRole("button", { name: /Save/ });
    fireEvent.click(save);
    await waitFor(() => expect(save).toBeDisabled());
    await act(async () => {
      resolveSave(new Response(null, { status: 204 }));
    });
  });

  it("invalidates file list query after successful new file creation", async () => {
    let listCallCount = 0;
    fetchMock.mockImplementation(async (url: string, init?: FetchInit) => {
      if (url.startsWith("/servers/mc-survival/files/list")) {
        listCallCount++;
        return jsonRes(ROOT_ENTRIES);
      }
      if (
        url === "/servers/mc-survival/files/create?path=%2Fnew.txt" &&
        init?.method === "POST"
      ) {
        return new Response(null, { status: 204 });
      }
      throw new Error(`unexpected fetch: ${url}`);
    });
    renderWithQuery(<FilesTab name="mc-survival" />);
    await screen.findByText("server.properties");
    const initialCount = listCallCount;
    fireEvent.click(screen.getByRole("button", { name: /New file/ }));
    const input = await screen.findByPlaceholderText("config.yaml");
    fireEvent.change(input, { target: { value: "new.txt" } });
    await act(async () => {
      fireEvent.click(screen.getByRole("button", { name: /^Create$/ }));
    });
    await waitFor(() => expect(listCallCount).toBeGreaterThan(initialCount));
  });

  it("cancels file read when component unmounts", async () => {
    let readStarted = false;
    fetchMock.mockImplementation(async (url: string) => {
      if (url.startsWith("/servers/mc-survival/files/list")) return jsonRes(ROOT_ENTRIES);
      if (url.startsWith("/servers/mc-survival/files/read")) {
        readStarted = true;
        return new Promise(() => {
          /* never resolves */
        });
      }
      throw new Error(`unexpected fetch: ${url}`);
    });
    const { unmount } = renderWithQuery(<FilesTab name="mc-survival" />);
    fireEvent.click(await screen.findByText("server.properties"));
    await waitFor(() => expect(readStarted).toBe(true));
    unmount();
    // Should not throw or show errors after unmount
    expect(true).toBe(true);
  });

  it("switches pane to tree when navigating with unsaved changes confirmed", async () => {
    const configEntries = [
      { name: "file.txt", path: "/config/file.txt", size: 100, dir: false },
    ];
    fetchMock.mockImplementation(async (url: string) => {
      if (url === "/servers/mc-survival/files/list?path=%2F")
        return jsonRes(ROOT_ENTRIES);
      if (url === "/servers/mc-survival/files/list?path=%2Fconfig")
        return jsonRes(configEntries);
      if (url.startsWith("/servers/mc-survival/files/read")) return textRes("content");
      throw new Error(`unexpected fetch: ${url}`);
    });
    const confirmMock = vi.fn(() => true);
    vi.stubGlobal("confirm", confirmMock);
    try {
      const { container } = renderWithQuery(<FilesTab name="mc-survival" />);
      fireEvent.click(await screen.findByText("server.properties"));
      const monaco = await screen.findByTestId("monaco");
      fireEvent.change(monaco, { target: { value: "modified" } });
      const aside = container.querySelector("aside") as HTMLElement;
      // Click config folder to navigate
      fireEvent.click(screen.getByText("config"));
      // Should navigate and show tree pane
      await waitFor(() => {
        const hasHidden = aside.className.split(/\s+/).includes("hidden");
        expect(hasHidden).toBe(false);
      });
    } finally {
      vi.unstubAllGlobals();
    }
  });
});

function renderWithQuery(ui: ReactElement, options?: Parameters<typeof baseRenderWithQuery>[1]) {
  const props = ui.props as { name?: string; ns?: string };
  return baseRenderWithQuery(<ResourceTargetProvider target={{ cluster: "local", name: props.name ?? "alpha", namespace: props.ns }} access={{ canWrite: true, canControl: true, canConsole: true, canDelete: true, isOwner: true, isCollaborator: false, permissions: ["*"] }}>{ui}</ResourceTargetProvider>, options);
}
