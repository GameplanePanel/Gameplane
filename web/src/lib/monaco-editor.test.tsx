import { render, screen } from "@testing-library/react";
import type { EditorProps, Monaco, OnMount } from "@monaco-editor/react";
import { afterEach, expect, it, vi } from "vitest";
import Editor, { startFileLanguageService } from "./monaco-editor";
import { startTypeScriptClient } from "./monaco-ts-client";

vi.mock("./monaco-loader", () => ({
  default: ({ value, language }: EditorProps) => <textarea aria-label={language} readOnly value={value} />,
}));
vi.mock("./monaco-ts-client", () => ({ startTypeScriptClient: vi.fn() }));

afterEach(() => {
  vi.unstubAllGlobals();
  vi.clearAllMocks();
});

function mountFile(language: string | null) {
  const model = language ? { getLanguageId: () => language } : null;
  const onDidDispose = vi.fn<(listener: () => void) => void>();
  const editor = { getModel: () => model, onDidDispose } as unknown as Parameters<OnMount>[0];
  startFileLanguageService(editor, {} as Monaco);
  return { model, onDidDispose };
}

it("renders the wrapped Monaco editor for ordinary config files", async () => {
  render(<Editor language="json" value="[]" />);
  expect(await screen.findByRole("textbox", { name: "json" })).toHaveValue("[]");
});

it("starts the selected TypeScript model and aborts it when its editor closes", async () => {
  vi.stubGlobal("crossOriginIsolated", true);
  const { model, onDidDispose } = mountFile("typescript");
  const start = vi.mocked(startTypeScriptClient);
  await vi.waitFor(() => expect(start).toHaveBeenCalledOnce());
  const [[selected, signal]] = start.mock.calls;
  expect(selected).toBe(model);
  expect(signal.aborted).toBe(false);
  onDidDispose.mock.calls[0][0]();
  expect(signal.aborted).toBe(true);
});

it("can switch away from a JavaScript file while its language service is loading", async () => {
  vi.stubGlobal("crossOriginIsolated", true);
  const { onDidDispose } = mountFile("javascript");
  onDidDispose.mock.calls[0][0]();
  await vi.dynamicImportSettled();
  expect(startTypeScriptClient).not.toHaveBeenCalled();
});

it("starts the language service on a JSPI-capable HTTP origin", async () => {
  vi.stubGlobal("crossOriginIsolated", false);
  vi.stubGlobal("SharedArrayBuffer", undefined);
  vi.stubGlobal("WebAssembly", { Suspending: vi.fn(), promising: vi.fn() });
  const { model, onDidDispose } = mountFile("javascript");
  await vi.waitFor(() => expect(startTypeScriptClient).toHaveBeenCalledWith(model, expect.any(AbortSignal)));
  onDidDispose.mock.calls[0][0]();
});

it("leaves non-script documents and browsers without either transport editable", async () => {
  vi.stubGlobal("crossOriginIsolated", true);
  for (const language of [null, "json", "markdown"]) {
    expect(mountFile(language).onDidDispose).not.toHaveBeenCalled();
  }
  vi.stubGlobal("crossOriginIsolated", false);
  vi.stubGlobal("WebAssembly", {});
  expect(mountFile("typescript").onDidDispose).not.toHaveBeenCalled();
  await vi.dynamicImportSettled();
  expect(startTypeScriptClient).not.toHaveBeenCalled();
});
