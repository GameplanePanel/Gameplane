import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { act, fireEvent, screen, waitFor } from "@testing-library/react";
import { ResourceTargetProvider } from "@/lib/resourceTarget";
import { renderWithQuery as baseRenderWithQuery } from "@/test/render";
import { FilesTab } from "./Files";

vi.mock("@/lib/monaco-editor", () => ({
  startFileLanguageService: vi.fn(),
  default: () => <textarea data-testid="monaco" />,
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
  return new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });
}

// The API answers 503 with a problem+json body once the file-only pod is not ready in 60 s.
function startingRes(): Response {
  return new Response(
    JSON.stringify({ type: "about:blank", title: "Service Unavailable", status: 503, detail: "files are starting, try again in a moment", messageId: "server.files.starting" }),
    { status: 503, headers: { "Content-Type": "application/problem+json" } },
  );
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

describe("Files tab files-starting state", () => {
  it("does not show the starting state while a slow file list is still pending", async () => {
    fetchMock.mockImplementation(() => new Promise<Response>(() => {}));
    renderFiles();
    // Well past the old 1.5 s delay: a pending list never shows the state on a timer.
    await act(async () => { await new Promise((done) => setTimeout(done, 1600)); });
    expect(screen.queryByText("Files did not start")).not.toBeInTheDocument();
    expect(screen.queryByText("Files are starting")).not.toBeInTheDocument();
  });

  it("does not show the starting state for a list that answers quickly", async () => {
    fetchMock.mockImplementation(async () => jsonRes(ROOT_ENTRIES));
    renderFiles();
    await screen.findByText("server.properties");
    expect(screen.queryByText("Files did not start")).not.toBeInTheDocument();
  });

  it("shows the timed-out state with Retry when the API answers server.files.starting", async () => {
    fetchMock.mockImplementation(async () => startingRes());
    renderFiles();
    expect(await screen.findByText("Files did not start")).toBeInTheDocument();
    expect(screen.getByText("Files did not start. Try again.")).toBeInTheDocument();
    expect(screen.getByText("Nothing was started. The server stays stopped, and no file was changed.")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Retry" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /Upload/ })).toBeDisabled();
  });

  it("recognises the plain-text 503 the API sends to callers that did not ask for problem+json", async () => {
    fetchMock.mockImplementation(async () => new Response("files are starting, try again in a moment\n", { status: 503 }));
    renderFiles();
    expect(await screen.findByText("Files did not start")).toBeInTheDocument();
    expect(screen.queryByText(/files are starting, try again in a moment/)).not.toBeInTheDocument();
  });

  it("treats a 503 with another message as an ordinary list error", async () => {
    fetchMock.mockImplementation(async () => new Response("agent unavailable", { status: 503 }));
    renderFiles();
    await waitFor(() => expect(screen.getByText(/agent unavailable/)).toBeInTheDocument());
    expect(screen.queryByText("Files did not start")).not.toBeInTheDocument();
  });

  it("does not retry the starting request on its own", async () => {
    fetchMock.mockImplementation(async () => startingRes());
    renderFiles();
    await screen.findByText("Files did not start");
    await act(async () => { await new Promise((done) => setTimeout(done, 50)); });
    const listCalls = fetchMock.mock.calls.filter(([url]) => String(url).startsWith("/servers/mc-survival/files/list")).length;
    expect(listCalls).toBe(1);
  });

  it("Retry lists the files once the pod is ready", async () => {
    let attempts = 0;
    fetchMock.mockImplementation(async () => {
      attempts += 1;
      return attempts === 1 ? startingRes() : jsonRes(ROOT_ENTRIES);
    });
    renderFiles();
    await screen.findByText("Files did not start");
    fireEvent.click(screen.getByRole("button", { name: "Retry" }));
    expect(await screen.findByText("server.properties")).toBeInTheDocument();
    await waitFor(() => expect(screen.queryByText("Files did not start")).not.toBeInTheDocument());
  });

  it("keeps the normal error banner for other list failures", async () => {
    fetchMock.mockImplementation(async () => new Response("boom", { status: 500 }));
    renderFiles();
    await waitFor(() => expect(screen.getByText(/500/)).toBeInTheDocument());
    expect(screen.queryByText("Files did not start")).not.toBeInTheDocument();
  });
});
