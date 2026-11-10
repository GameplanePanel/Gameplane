// Point @monaco-editor/loader at the Monaco build this image ships, before
// any <Editor> mounts. Without this the loader fetches Monaco from
// cdn.jsdelivr.net at runtime: code outside the lockfile and the image,
// and blocked by the dashboard's Content-Security-Policy. Production
// serves the copy vite.config.ts places in dist/monaco/vs; the dev server
// serves it straight from node_modules.
import loader from "@monaco-editor/loader";

const vs = import.meta.env.DEV ? "/node_modules/monaco-editor/min/vs" : `${import.meta.env.BASE_URL}monaco/vs`;

loader.config({ paths: { vs } });
