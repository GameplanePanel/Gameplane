import * as monaco from "monaco-editor/editor";

const libraries = import.meta.glob<string>("../../node_modules/@typescript/typescript-wasip1-wasm/lib/lib*.d.ts", {
  query: "?raw", import: "default",
});

// Like the TS Playground, materialize a model only when navigation points
// into a bundled declaration. A definition can never open a server file here.
export function libraryLocations(session: monaco.Uri, signal: AbortSignal, disposables: monaco.IDisposable[]) {
  const models = new Set<monaco.editor.ITextModel>();
  let watchingEditors = false;
  function watchPreview(editor: monaco.editor.ICodeEditor) {
    let previousReadOnly: boolean | undefined;
    function sync() {
      const current = editor.getModel();
      if (current && models.has(current)) {
        previousReadOnly ??= editor.getOption(monaco.editor.EditorOption.readOnly);
        editor.updateOptions({ readOnly: true });
      } else if (previousReadOnly !== undefined) {
        editor.updateOptions({ readOnly: previousReadOnly });
        previousReadOnly = undefined;
      }
    }
    disposables.push(editor.onDidChangeModel(sync));
    sync();
  }
  return async (uri: string) => {
    const source = monaco.Uri.parse(uri);
    const match = source.scheme === "bundled"
      ? /^\/libs\/(lib(?:\.[\w.]+)?\.d\.ts)$/.exec(source.path)
      : source.scheme === "file" && /^\/(?:typescript\/lib\/)?(lib(?:\.[\w.]+)?\.d\.ts)$/.exec(source.path);
    if (!match || signal.aborted) return;
    const file = match[1];
    const asset = libraries[`../../node_modules/@typescript/typescript-wasip1-wasm/lib/${file}`];
    if (!asset) return;
    const location = session.with({ scheme: "tslib", path: `/${file}` });
    if (!monaco.editor.getModel(location)) {
      const contents = await asset();
      if (signal.aborted) return;
      if (!monaco.editor.getModel(location)) {
        const model = monaco.editor.createModel(contents, "typescript", location);
        models.add(model);
        disposables.push(model);
      }
    }
    if (!watchingEditors) {
      watchingEditors = true;
      monaco.editor.getEditors().forEach(watchPreview);
      disposables.push(monaco.editor.onDidCreateEditor(watchPreview));
    }
    return location;
  };
}
