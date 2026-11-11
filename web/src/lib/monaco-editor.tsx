import { lazy, Suspense } from "react";
import type { EditorProps, OnMount } from "@monaco-editor/react";

const MonacoEditor = lazy(() => import("./monaco-loader"));

export default function Editor(props: EditorProps) {
  return <Suspense fallback={null}><MonacoEditor {...props} /></Suspense>;
}

export const startFileLanguageService: OnMount = editor => {
  const model = editor.getModel();
  if (!model || !["javascript", "typescript"].includes(model.getLanguageId()) || !self.crossOriginIsolated) return;
  const lifetime = new AbortController();
  editor.onDidDispose(() => lifetime.abort());
  void import("./monaco-ts-client")
    .then(({ startTypeScriptClient }) => {
      if (!lifetime.signal.aborted) return startTypeScriptClient(model, lifetime.signal);
    })
    .catch(() => {
      if (!lifetime.signal.aborted) console.warn("Could not start TypeScript language services");
    });
};
