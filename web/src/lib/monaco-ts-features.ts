import * as monaco from "monaco-editor/editor";
import * as lsp from "vscode-languageserver-protocol/browser";
import { libraryLocations } from "./monaco-ts-libraries";

export function registerTypeScriptFeatures(
  model: monaco.editor.ITextModel,
  textDocument: lsp.TextDocumentIdentifier,
  connection: lsp.ProtocolConnection,
  capabilities: lsp.ServerCapabilities,
  getDiagnostics: () => lsp.Diagnostic[],
  signal: AbortSignal,
  disposables: monaco.IDisposable[],
) {
  const language = model.getLanguageId();
  // The model has its own URI and worker. Exact model checks also cover
  // names containing glob metacharacters that Monaco's selector interprets.
  const selector = { language, scheme: model.uri.scheme };
  const positionParams = (position: monaco.IPosition) => ({ textDocument, position: toPosition(position) });
  const libraryLocation = libraryLocations(model.uri, signal, disposables);
  async function locations(entries: lsp.Location[]) {
    const result = await Promise.all(entries.map(async entry => {
      const uri = entry.uri === textDocument.uri ? model.uri : await libraryLocation(entry.uri);
      return uri && { uri, range: range(entry.range) };
    }));
    return result.filter(location => location !== undefined);
  }
  function fileChanges(edit: lsp.WorkspaceEdit): lsp.TextEdit[] | undefined {
    const changes: lsp.TextEdit[] = [];
    for (const [uri, edits] of Object.entries(edit.changes ?? {})) {
      if (uri !== textDocument.uri) return;
      changes.push(...edits);
    }
    for (const change of edit.documentChanges ?? []) {
      if (!("textDocument" in change) || change.textDocument.uri !== textDocument.uri) return;
      if (change.textDocument.version != null && change.textDocument.version !== model.getVersionId()) return;
      changes.push(...change.edits);
    }
    return changes;
  }
  function localEdits(edit: lsp.WorkspaceEdit): monaco.languages.WorkspaceEdit | undefined {
    const changes = fileChanges(edit);
    if (!changes) return;
    return { edits: changes.map(e => ({ resource: model.uri, textEdit: textEdit(e), versionId: model.getVersionId() })) };
  }
  connection.onRequest(lsp.ApplyWorkspaceEditRequest.type, ({ edit }) => {
    const changes = fileChanges(edit);
    if (!changes || model.isDisposed()) return { applied: false };
    model.pushStackElement();
    model.pushEditOperations(null, changes.map(textEdit), () => null);
    model.pushStackElement();
    return { applied: true };
  });
  const serverCommands = new Set(capabilities.executeCommandProvider?.commands);
  const forwardCommand = `typescript.lsp.${model.id}.execute`;
  if (serverCommands.size) {
    disposables.push(monaco.editor.registerCommand(forwardCommand, (_accessor, command: lsp.Command) => {
      if (serverCommands.has(command.command)) {
        return connection.sendRequest(lsp.ExecuteCommandRequest.type, { command: command.command, arguments: command.arguments });
      }
    }));
  }
  function editorCommand(command?: lsp.Command): monaco.languages.Command | undefined {
    if (!command) return;
    if (serverCommands.has(command.command)) return { id: forwardCommand, title: command.title, arguments: [command] };
    // These Monaco actions only open the current editor's suggestion UI.
    if (command.command === "editor.action.triggerSuggest" || command.command === "editor.action.triggerParameterHints") {
      return { id: command.command, title: command.title };
    }
  }
  const completions = new WeakMap<monaco.languages.CompletionItem, lsp.CompletionItem>();
  disposables.push(monaco.languages.registerCompletionItemProvider(selector, {
    triggerCharacters: capabilities.completionProvider?.triggerCharacters,
    async provideCompletionItems(current, position, _context, token) {
      if (current !== model) return;
      const result = await connection.sendRequest(lsp.CompletionRequest.type, positionParams(position), token);
      if (!result) return;
      const word = model.getWordUntilPosition(position);
      const defaultRange = new monaco.Range(position.lineNumber, word.startColumn, position.lineNumber, word.endColumn);
      const items = Array.isArray(result) ? result : result.items;
      const suggestions = items.map(item => {
        const suggestion: monaco.languages.CompletionItem = {
          label: item.label, detail: item.detail, sortText: item.sortText, filterText: item.filterText,
          kind: completionKind(item.kind), tags: item.tags, commitCharacters: item.commitCharacters, preselect: item.preselect,
          documentation: markup(item.documentation),
          insertText: item.textEdit?.newText ?? item.insertText ?? item.label,
          insertTextRules: item.insertTextFormat === 2 ? monaco.languages.CompletionItemInsertTextRule.InsertAsSnippet : undefined,
          additionalTextEdits: item.additionalTextEdits?.map(textEdit),
          command: editorCommand(item.command),
          range: item.textEdit
            ? ("range" in item.textEdit ? range(item.textEdit.range) : { insert: range(item.textEdit.insert), replace: range(item.textEdit.replace) })
            : defaultRange,
        };
        completions.set(suggestion, item);
        return suggestion;
      });
      return { suggestions, incomplete: !Array.isArray(result) && result.isIncomplete };
    },
    async resolveCompletionItem(suggestion, token) {
      const item = completions.get(suggestion);
      if (!item || !capabilities.completionProvider?.resolveProvider) return suggestion;
      const resolved = await connection.sendRequest(lsp.CompletionResolveRequest.type, item, token);
      return {
        ...suggestion, detail: resolved.detail ?? suggestion.detail,
        documentation: markup(resolved.documentation) ?? suggestion.documentation,
        additionalTextEdits: resolved.additionalTextEdits?.map(textEdit) ?? suggestion.additionalTextEdits,
        command: editorCommand(resolved.command) ?? suggestion.command,
      };
    },
  }));
  disposables.push(monaco.languages.registerHoverProvider(selector, {
    async provideHover(current, position, token) {
      if (current !== model) return;
      const hover = await connection.sendRequest(lsp.HoverRequest.type, positionParams(position), token);
      if (!hover) return;
      const parts = Array.isArray(hover.contents) ? hover.contents : [hover.contents];
      return { range: hover.range && range(hover.range), contents: parts.map(p => ({ value: typeof p === "string" ? p : p.value })) };
    },
  }));
  disposables.push(monaco.languages.registerSignatureHelpProvider(selector, {
    signatureHelpTriggerCharacters: capabilities.signatureHelpProvider?.triggerCharacters,
    signatureHelpRetriggerCharacters: capabilities.signatureHelpProvider?.retriggerCharacters,
    async provideSignatureHelp(current, position, token) {
      if (current !== model) return;
      const help = await connection.sendRequest(lsp.SignatureHelpRequest.type, positionParams(position), token);
      if (!help) return;
      return { dispose() {}, value: {
        activeSignature: help.activeSignature ?? 0, activeParameter: help.activeParameter ?? 0,
        signatures: help.signatures.map(s => ({ label: s.label, documentation: markup(s.documentation),
          parameters: (s.parameters ?? []).map(p => ({ label: p.label, documentation: markup(p.documentation) })) })),
      } };
    },
  }));
  disposables.push(monaco.languages.registerDefinitionProvider(selector, {
    async provideDefinition(current, position, token) {
      if (current !== model) return;
      const result = await connection.sendRequest(lsp.DefinitionRequest.type, positionParams(position), token);
      const entries = Array.isArray(result) ? result : result ? [result] : [];
      return locations(entries.map(entry => "targetUri" in entry ? { uri: entry.targetUri, range: entry.targetSelectionRange } : entry));
    },
  }));
  disposables.push(monaco.languages.registerReferenceProvider(selector, {
    async provideReferences(current, position, context, token) {
      if (current !== model) return;
      const result = await connection.sendRequest(lsp.ReferencesRequest.type, { ...positionParams(position), context }, token);
      return locations(result ?? []);
    },
  }));
  disposables.push(monaco.languages.registerDocumentHighlightProvider(selector, {
    async provideDocumentHighlights(current, position, token) {
      if (current !== model) return;
      const result = await connection.sendRequest(lsp.DocumentHighlightRequest.type, positionParams(position), token);
      return result?.map(highlight => ({
        kind: highlight.kind === lsp.DocumentHighlightKind.Write ? monaco.languages.DocumentHighlightKind.Write
          : highlight.kind === lsp.DocumentHighlightKind.Read ? monaco.languages.DocumentHighlightKind.Read
          : monaco.languages.DocumentHighlightKind.Text,
        range: range(highlight.range),
      }));
    },
  }));
  if (capabilities.documentSymbolProvider) {
    disposables.push(monaco.languages.registerDocumentSymbolProvider(selector, {
      async provideDocumentSymbols(current, token) {
        if (current !== model) return;
        const result = await connection.sendRequest(lsp.DocumentSymbolRequest.type, { textDocument }, token);
        return result?.map(symbol => "location" in symbol
          ? { name: symbol.name, kind: symbol.kind - 1, tags: [], detail: "", range: range(symbol.location.range),
            selectionRange: range(symbol.location.range), containerName: symbol.containerName }
          : documentSymbol(symbol));
      },
    }));
  }
  disposables.push(monaco.languages.registerRenameProvider(selector, {
    async provideRenameEdits(current, position, newName, token) {
      if (current !== model) return;
      const result = await connection.sendRequest(lsp.RenameRequest.type, { ...positionParams(position), newName }, token);
      return result && localEdits(result);
    },
  }));
  disposables.push(monaco.languages.registerDocumentFormattingEditProvider(selector, {
    async provideDocumentFormattingEdits(current, options, token) {
      if (current !== model) return;
      return (await connection.sendRequest(lsp.DocumentFormattingRequest.type, { textDocument, options: { ...options } }, token))?.map(textEdit);
    },
  }));
  disposables.push(monaco.languages.registerDocumentRangeFormattingEditProvider(selector, {
    async provideDocumentRangeFormattingEdits(current, selection, options, token) {
      if (current !== model) return;
      return (await connection.sendRequest(lsp.DocumentRangeFormattingRequest.type, {
        textDocument, range: toRange(selection), options: { ...options },
      }, token))?.map(textEdit);
    },
  }));
  const onType = capabilities.documentOnTypeFormattingProvider;
  if (onType) {
    disposables.push(monaco.languages.registerOnTypeFormattingEditProvider(selector, {
      autoFormatTriggerCharacters: [onType.firstTriggerCharacter, ...onType.moreTriggerCharacter ?? []],
      async provideOnTypeFormattingEdits(current, position, ch, options, token) {
        if (current !== model) return;
        return (await connection.sendRequest(lsp.DocumentOnTypeFormattingRequest.type, {
          ...positionParams(position), ch, options: { ...options },
        }, token))?.map(textEdit);
      },
    }));
  }
  if (capabilities.inlayHintProvider) {
    disposables.push(monaco.languages.registerInlayHintsProvider(selector, {
      async provideInlayHints(current, selection, token) {
        if (current !== model) return;
        const result = await connection.sendRequest(lsp.InlayHintRequest.type, { textDocument, range: toRange(selection) }, token);
        return { dispose() {}, hints: (result ?? []).map(hint => ({
          label: typeof hint.label === "string" ? hint.label : hint.label.map(part => ({ label: part.value, tooltip: markup(part.tooltip) })),
          kind: hint.kind, position: { lineNumber: hint.position.line + 1, column: hint.position.character + 1 },
          paddingLeft: hint.paddingLeft, paddingRight: hint.paddingRight, tooltip: markup(hint.tooltip),
          textEdits: hint.textEdits?.map(textEdit),
        })) };
      },
    }));
  }
  const unresolvedActions = new WeakMap<monaco.languages.CodeAction, lsp.CodeAction>();
  const resolveActions = typeof capabilities.codeActionProvider === "object" && capabilities.codeActionProvider.resolveProvider;
  disposables.push(monaco.languages.registerCodeActionProvider(selector, {
    async provideCodeActions(current, selection, context, token) {
      if (current !== model) return;
      const result = await connection.sendRequest(lsp.CodeActionRequest.type, {
        textDocument, range: toRange(selection), context: {
          diagnostics: getDiagnostics().filter(d => monaco.Range.areIntersectingOrTouching(range(d.range), selection)),
          only: context.only ? [context.only] : undefined,
        },
      }, token);
      const actions: monaco.languages.CodeAction[] = [];
      for (const action of result ?? []) {
        if (lsp.Command.is(action)) {
          const command = editorCommand(action);
          if (command) actions.push({ title: action.title, command });
          continue;
        }
        const edit = action.edit && localEdits(action.edit);
        const command = editorCommand(action.command);
        if (action.edit && !edit) continue;
        if (!edit && !command && !(resolveActions && action.data !== undefined)) continue;
        const suggestion = { title: action.title, kind: action.kind, isPreferred: action.isPreferred, edit, command };
        if (!edit && !command) unresolvedActions.set(suggestion, action);
        actions.push(suggestion);
      }
      return { actions, dispose() {} };
    },
    async resolveCodeAction(suggestion, token) {
      const source = unresolvedActions.get(suggestion);
      if (!source) return suggestion;
      const resolved = await connection.sendRequest(lsp.CodeActionResolveRequest.type, source, token);
      const edit = resolved.edit && localEdits(resolved.edit);
      if (resolved.edit && !edit) return suggestion;
      return { ...suggestion, edit, command: editorCommand(resolved.command) };
    },
  }));
}

export function range(value: lsp.Range) {
  return new monaco.Range(value.start.line + 1, value.start.character + 1, value.end.line + 1, value.end.character + 1);
}

function toPosition(position: monaco.IPosition) {
  return { line: position.lineNumber - 1, character: position.column - 1 };
}

export function toRange(value: monaco.Range) {
  return { start: toPosition(value.getStartPosition()), end: toPosition(value.getEndPosition()) };
}

function textEdit(edit: lsp.TextEdit) {
  return { range: range(edit.range), text: edit.newText };
}

function documentSymbol(symbol: lsp.DocumentSymbol): monaco.languages.DocumentSymbol {
  // The symbol enums share the same ordering; LSP starts at one and Monaco at zero.
  return {
    name: symbol.name, detail: symbol.detail ?? "", kind: symbol.kind - 1,
    tags: symbol.tags ?? [], range: range(symbol.range), selectionRange: range(symbol.selectionRange),
    children: symbol.children?.map(documentSymbol),
  };
}

function markup(value: lsp.MarkupContent | string | undefined) {
  return typeof value === "object" ? { value: value.value } : value;
}

const completionKinds = new Map(Object.entries(lsp.CompletionItemKind).map(([name, value]) =>
  [value, monaco.languages.CompletionItemKind[name as keyof typeof lsp.CompletionItemKind]] as const));

function completionKind(value?: lsp.CompletionItemKind) {
  return (value && completionKinds.get(value)) ?? monaco.languages.CompletionItemKind.Text;
}
