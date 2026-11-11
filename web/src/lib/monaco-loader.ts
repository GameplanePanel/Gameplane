import loader from "@monaco-editor/loader";
import * as monaco from "monaco-editor/editor";
import "monaco-editor/features/register.all";
import "monaco-editor/languages/definitions/ini/register";
import "monaco-editor/languages/definitions/javascript/register";
import "monaco-editor/languages/definitions/markdown/register";
import "monaco-editor/languages/definitions/shell/register";
import "monaco-editor/languages/definitions/typescript/register";
import "monaco-editor/languages/definitions/yaml/register";
import "monaco-editor/languages/features/json/register";
import EditorWorker from "monaco-editor/editor/editor.worker?worker";
import JsonWorker from "monaco-editor/languages/features/json/json.worker?worker";

// Vite serves each worker from the same origin. The Files editor connects
// JavaScript and TypeScript to their own TS7 language service on demand.
self.MonacoEnvironment = {
  getWorker: (_moduleId, label) => label === "json" ? new JsonWorker() : new EditorWorker(),
};
loader.config({ monaco });

export { default } from "@monaco-editor/react";
