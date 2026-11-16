import * as monaco from "monaco-editor/editor";
import * as lsp from "vscode-languageserver-protocol/browser";
import { range, toRange, registerTypeScriptFeatures } from "./monaco-ts-features";
import { createTypeScriptTransport } from "./monaco-ts-transport";

// Each Files editor opens one file from one server. Only synchronize that
// model with its in-browser language server, even if another editor is open.
export async function startTypeScriptClient(model: monaco.editor.ITextModel, signal: AbortSignal) {
  const fileName = model.uri.path.slice(model.uri.path.lastIndexOf("/") + 1);
  const owner = "typescript";
  const disposables: monaco.IDisposable[] = [];
  const lifetime = new AbortController();
  let diagnosticTimer: ReturnType<typeof setTimeout> | undefined;
  let disposed = false;
  function dispose() {
    if (disposed) return;
    disposed = true;
    lifetime.abort();
    clearTimeout(diagnosticTimer);
    for (const entry of disposables.reverse()) entry.dispose();
    if (!model.isDisposed()) monaco.editor.setModelMarkers(model, owner, []);
    signal.removeEventListener("abort", dispose);
  }

  const session = await createTypeScriptTransport(fileName, model.getValue(), signal, () => {
    console.warn("TypeScript language service stopped");
    dispose();
  });
  if (signal.aborted || disposed) {
    session.dispose();
    return;
  }
  disposables.push(session);
  signal.addEventListener("abort", dispose, { once: true });
  const reader = new lsp.BrowserMessageReader(session.port);
  const writer = new lsp.BrowserMessageWriter(session.port);
  const connection = lsp.createProtocolConnection(reader, writer);
  disposables.push(reader, writer, connection);
  const textDocument = { uri: monaco.Uri.file(`/workspace/${fileName}`).toString() };
  const rootUri = monaco.Uri.file("/workspace").toString();
  let lastDiagnostics: lsp.Diagnostic[] = [];
  function publish(diagnostics: lsp.Diagnostic[]) {
    if (disposed) return;
    lastDiagnostics = diagnostics;
    monaco.editor.setModelMarkers(model, owner, diagnostics.map(diagnostic => ({
      ...range(diagnostic.range),
      message: diagnostic.message,
      severity: diagnostic.severity === lsp.DiagnosticSeverity.Warning ? monaco.MarkerSeverity.Warning
        : diagnostic.severity === lsp.DiagnosticSeverity.Hint ? monaco.MarkerSeverity.Hint
        : diagnostic.severity === lsp.DiagnosticSeverity.Information ? monaco.MarkerSeverity.Info
        : monaco.MarkerSeverity.Error,
      source: diagnostic.source,
      code: diagnostic.code === undefined ? undefined : String(diagnostic.code),
      tags: diagnostic.tags,
      relatedInformation: diagnostic.relatedInformation?.flatMap(info => info.location.uri === textDocument.uri
        ? [{ ...range(info.location.range), message: info.message, resource: model.uri }]
        : []),
    })));
  }
  let pullDiagnostics = false;
  function scheduleDiagnostics() {
    if (!pullDiagnostics || disposed) return;
    clearTimeout(diagnosticTimer);
    diagnosticTimer = setTimeout(() => {
      const version = model.getVersionId();
      void connection.sendRequest(lsp.DocumentDiagnosticRequest.type, { textDocument }).then(report => {
        if (!disposed && model.getVersionId() === version && report.kind === "full") publish(report.items);
      }).catch(() => {
        if (!disposed) console.warn("TypeScript diagnostics failed");
      });
    }, 150);
  }
  connection.onRequest(lsp.ConfigurationRequest.type, ({ items }) => items.map(() => ({})));
  connection.onRequest(lsp.DiagnosticRefreshRequest.type, scheduleDiagnostics);
  connection.onNotification(lsp.PublishDiagnosticsNotification.type, event => {
    if (event.uri === textDocument.uri && (event.version === undefined || event.version === model.getVersionId())) {
      publish(event.diagnostics);
    }
  });
  connection.listen();
  try {
    const { capabilities } = await connection.sendRequest(lsp.InitializeRequest.type, {
      processId: null, rootUri, workspaceFolders: [{ name: "workspace", uri: rootUri }],
      capabilities: {
        general: { positionEncodings: ["utf-16"] },
        workspace: { configuration: true, applyEdit: true, workspaceEdit: { documentChanges: true }, diagnostics: { refreshSupport: true } },
        textDocument: {
          diagnostic: { relatedDocumentSupport: false },
          publishDiagnostics: { relatedInformation: true, tagSupport: { valueSet: [1, 2] } },
          completion: { completionItem: {
            snippetSupport: true, documentationFormat: ["markdown", "plaintext"],
            resolveSupport: { properties: ["documentation", "detail", "additionalTextEdits"] },
          } },
          hover: { contentFormat: ["markdown", "plaintext"] },
          signatureHelp: {}, definition: {}, references: {}, rename: {}, formatting: {},
          documentSymbol: { hierarchicalDocumentSymbolSupport: true }, inlayHint: {},
          codeAction: {
            codeActionLiteralSupport: { codeActionKind: { valueSet: ["quickfix", "refactor", "source"] } },
            dataSupport: true, resolveSupport: { properties: ["edit", "command"] },
          },
        },
      },
    });
    if (disposed) return;
    await connection.sendNotification(lsp.InitializedNotification.type, {});
    if (disposed) return;
    const language = model.getLanguageId();
    const languageId = /x$/i.test(fileName) ? `${language}react` : language;
    void connection.sendNotification(lsp.DidOpenTextDocumentNotification.type, {
      textDocument: { ...textDocument, languageId, version: model.getVersionId(), text: model.getValue() },
    });
    pullDiagnostics = !!capabilities.diagnosticProvider;
    const syncKind = typeof capabilities.textDocumentSync === "object"
      ? capabilities.textDocumentSync.change : capabilities.textDocumentSync;
    disposables.push(model.onDidChangeContent(event => {
      const changes = syncKind === lsp.TextDocumentSyncKind.Incremental && !event.isEolChange
        ? event.changes.map(change => ({ range: toRange(monaco.Range.lift(change.range)), text: change.text }))
        : [{ text: model.getValue() }];
      void connection.sendNotification(lsp.DidChangeTextDocumentNotification.type, {
        textDocument: { ...textDocument, version: model.getVersionId() }, contentChanges: changes,
      });
      scheduleDiagnostics();
    }));
    scheduleDiagnostics();

    registerTypeScriptFeatures(model, textDocument, connection, capabilities, () => lastDiagnostics, lifetime.signal, disposables);
  } catch (error) {
    const wasDisposed = disposed;
    dispose();
    if (!wasDisposed) throw error;
  }
}
