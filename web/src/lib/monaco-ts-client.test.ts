import { MessageChannel as NodeMessageChannel } from "node:worker_threads";
import * as monaco from "monaco-editor/editor";
import * as lsp from "vscode-languageserver-protocol/browser";
import { afterEach, expect, it, vi } from "vitest";
import { startTypeScriptClient } from "./monaco-ts-client";
import { createTypeScriptTransport } from "./monaco-ts-transport";

vi.mock("./monaco-ts-transport", () => ({ createTypeScriptTransport: vi.fn() }));
vi.mock("monaco-editor/editor", async () => {
  // Exercise Monaco's own URI escaping and UTF-16 ranges without a rendered editor.
  const { createMonacoBaseAPI } = await vi.importActual<{
    createMonacoBaseAPI(): Pick<typeof monaco, "Uri" | "Range" | "Position" | "MarkerSeverity" | "CancellationTokenSource">;
  }>("monaco-editor/editor/common/services/editorBaseApi");
  const provider = () => vi.fn(() => ({ dispose: vi.fn() }));
  return {
    ...createMonacoBaseAPI(),
    editor: { setModelMarkers: vi.fn() },
    languages: {
      CompletionItemKind: { Text: 18, Method: 0 }, CompletionItemInsertTextRule: { InsertAsSnippet: 4 },
      registerCompletionItemProvider: provider(), registerHoverProvider: provider(),
      registerSignatureHelpProvider: provider(), registerDefinitionProvider: provider(),
      registerReferenceProvider: provider(), registerDocumentHighlightProvider: provider(),
      registerRenameProvider: provider(), registerDocumentFormattingEditProvider: provider(),
      registerDocumentRangeFormattingEditProvider: provider(), registerCodeActionProvider: provider(),
    },
  };
});

function makeModel(name: string, instance: string, language: string) {
  let value = "const port = 7;";
  let version = 1;
  const changes = new Set<(event: monaco.editor.IModelContentChangedEvent) => void>();
  const model = {
    uri: monaco.Uri.file(`/workspace/${name}`).with({ query: instance }),
    getLanguageId: () => language, getVersionId: () => version, getValue: () => value,
    getWordUntilPosition: ({ column }: monaco.IPosition) => ({ word: "", startColumn: column, endColumn: column }),
    isDisposed: () => false,
    onDidChangeContent(listener: (event: monaco.editor.IModelContentChangedEvent) => void) {
      changes.add(listener);
      return { dispose: () => changes.delete(listener) };
    },
  } as unknown as monaco.editor.ITextModel;
  const append = (text: string) => {
    const column = value.length + 1;
    value += text;
    version++;
    for (const listener of changes) listener({
      changes: [{ range: new monaco.Range(1, column, 1, column), text, rangeOffset: column - 1, rangeLength: 0 }],
      versionId: version, eol: "\n", isEolChange: false, isUndoing: false, isRedoing: false, isFlush: false,
      detailedReasonsChangeLengths: [],
    });
  };
  return { model, append };
}

const cleanup: Array<() => void> = [];
afterEach(() => {
  for (const dispose of cleanup.splice(0).reverse()) dispose();
  vi.clearAllMocks();
});

function languageServer(capabilities: lsp.ServerCapabilities = {}) {
  const { port1, port2 } = new NodeMessageChannel() as unknown as MessageChannel;
  const reader = new lsp.BrowserMessageReader(port1);
  const writer = new lsp.BrowserMessageWriter(port1);
  const server = lsp.createProtocolConnection(reader, writer);
  server.onRequest(lsp.InitializeRequest.type, () => ({ capabilities }));
  const opened = vi.fn();
  const changed = vi.fn();
  server.onNotification(lsp.DidOpenTextDocumentNotification.type, opened);
  server.onNotification(lsp.DidChangeTextDocumentNotification.type, changed);
  server.listen();
  const controller = new AbortController();
  const dispose = vi.fn();
  vi.mocked(createTypeScriptTransport).mockResolvedValueOnce({ port: port2, dispose });
  cleanup.push(() => {
    controller.abort();
    server.dispose();
    reader.dispose();
    writer.dispose();
    port1.close();
    port2.close();
  });
  return { server, opened, changed, controller, dispose };
}

it("synchronizes the selected TSX document and applies diagnostics only to its current version", async () => {
  const { model, append } = makeModel("view #1.tsx", "editor=7", "typescript");
  const { server, opened, changed, controller, dispose } = languageServer({ textDocumentSync: lsp.TextDocumentSyncKind.Incremental });
  await startTypeScriptClient(model, controller.signal);
  const uri = monaco.Uri.file("/workspace/view #1.tsx").toString();
  await vi.waitFor(() => expect(opened).toHaveBeenCalledWith(expect.objectContaining({
    textDocument: { uri, languageId: "typescriptreact", text: "const port = 7;", version: 1 },
  })));
  append("port;");
  await vi.waitFor(() => expect(changed).toHaveBeenCalledWith(expect.objectContaining({
    textDocument: { uri, version: 2 }, contentChanges: [{ text: "port;", range: lsp.Range.create(0, 15, 0, 15) }],
  })));

  const diagnostic = { range: lsp.Range.create(0, 6, 0, 10), message: "current error", code: 2322 };
  await server.sendNotification(lsp.PublishDiagnosticsNotification.type, { uri, version: 1, diagnostics: [diagnostic] });
  await server.sendNotification(lsp.PublishDiagnosticsNotification.type, { uri: "file:///workspace/other.ts", diagnostics: [diagnostic] });
  await server.sendNotification(lsp.PublishDiagnosticsNotification.type, { uri, version: 2, diagnostics: [diagnostic] });
  const markers = vi.mocked(monaco.editor.setModelMarkers);
  await vi.waitFor(() => expect(markers).toHaveBeenCalledWith(model, "typescript", [
    expect.objectContaining({ message: "current error", startColumn: 7, endColumn: 11, startLineNumber: 1, code: "2322" }),
  ]));
  expect(markers).toHaveBeenCalledTimes(1);
  controller.abort();
  expect(dispose).toHaveBeenCalledOnce();
  expect(markers).toHaveBeenLastCalledWith(model, "typescript", []);
});

it("keeps providers scoped to their model and rejects a rename that also edits another file", async () => {
  const first = makeModel("server.mjs", "editor=one", "javascript").model;
  const second = makeModel("server.mjs", "editor=two", "javascript").model;
  const { server, controller } = languageServer();
  const rename = vi.fn(() => ({
    changes: {
      "file:///workspace/server.mjs": [lsp.TextEdit.replace(lsp.Range.create(0, 6, 0, 10), "port2")],
      "file:///workspace/remote.mjs": [lsp.TextEdit.replace(lsp.Range.create(0, 0, 0, 4), "port2")],
    },
  }));
  server.onRequest(lsp.RenameRequest.type, rename);
  await startTypeScriptClient(first, controller.signal);
  const [[, provider]] = vi.mocked(monaco.languages.registerRenameProvider).mock.calls;
  const position = new monaco.Position(1, 7);
  const tokenSource = new monaco.CancellationTokenSource();
  cleanup.push(() => tokenSource.dispose());
  expect(await provider.provideRenameEdits(second, position, "port2", tokenSource.token)).toBeUndefined();
  expect(rename).not.toHaveBeenCalled();
  expect(await provider.provideRenameEdits(first, position, "port2", tokenSource.token)).toBeUndefined();
  expect(rename).toHaveBeenCalledOnce();
  controller.abort();
  const registration = vi.mocked(monaco.languages.registerRenameProvider).mock.results[0].value;
  expect(registration.dispose).toHaveBeenCalledOnce();
});

it("displays the newest pulled diagnostics when two edits complete out of order", async () => {
  const { model, append } = makeModel("server.ts", "editor=first", "typescript");
  const { server, controller } = languageServer({
    diagnosticProvider: { interFileDependencies: false, workspaceDiagnostics: false },
  });
  const respond: Array<(report: lsp.FullDocumentDiagnosticReport) => void> = [];
  server.onRequest(lsp.DocumentDiagnosticRequest.type, () =>
    new Promise<lsp.FullDocumentDiagnosticReport>(resolve => respond.push(resolve)),
  );
  await startTypeScriptClient(model, controller.signal);
  await vi.waitFor(() => expect(respond).toHaveLength(1));
  append("port;");
  await vi.waitFor(() => expect(respond).toHaveLength(2));
  const range = lsp.Range.create(0, 0, 0, 2);
  respond[1]({ kind: "full", items: [{ range, message: "current" }] });
  const markers = vi.mocked(monaco.editor.setModelMarkers);
  await vi.waitFor(() => expect(markers).toHaveBeenCalledWith(model, "typescript", [expect.objectContaining({ message: "current" })]));
  respond[0]({ kind: "full", items: [{ range, message: "stale" }] });
  // Wait for the client to process a later message on this connection.
  await server.sendRequest(lsp.ConfigurationRequest.type, { items: [{}] });
  expect(markers).toHaveBeenCalledTimes(1);
});

it("converts LSP completion edits to the active editor's coordinates", async () => {
  const { model } = makeModel("server.ts", "editor=first", "typescript");
  const { server, controller } = languageServer({ completionProvider: { triggerCharacters: ["."] } });
  const completion = vi.fn<(params: lsp.CompletionParams) => lsp.CompletionList>(() => ({
    isIncomplete: true, items: [{
      label: "toFixed", kind: lsp.CompletionItemKind.Method, documentation: { kind: lsp.MarkupKind.Markdown, value: "Format a number" },
      textEdit: lsp.TextEdit.replace(lsp.Range.create(0, 4, 0, 7), "toFixed()"),
    }],
  }));
  server.onRequest(lsp.CompletionRequest.type, completion);
  await startTypeScriptClient(model, controller.signal);
  const [[, provider]] = vi.mocked(monaco.languages.registerCompletionItemProvider).mock.calls;
  const token = new monaco.CancellationTokenSource();
  cleanup.push(() => token.dispose());
  const result = await provider.provideCompletionItems(model, new monaco.Position(1, 8), { triggerKind: 0 }, token.token);
  expect(completion.mock.calls[0][0]).toMatchObject({
    textDocument: { uri: "file:///workspace/server.ts" }, position: { line: 0, character: 7 },
  });
  expect(result).toMatchObject({
    incomplete: true, suggestions: [{
      label: "toFixed", kind: monaco.languages.CompletionItemKind.Method, insertText: "toFixed()",
      range: new monaco.Range(1, 5, 1, 8), documentation: { value: "Format a number" },
    }],
  });
});
