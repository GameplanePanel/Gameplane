import { Blob as NodeBlob, File as NodeFile } from "node:buffer";
import "@testing-library/jest-dom/vitest";
import { configure } from "@testing-library/react";
import { afterAll, afterEach, beforeAll } from "vitest";
import { server } from "./server";

// Raise the default async-utility timeout. On the loaded CI runner, components
// backed by TanStack Query can take well over the 1000ms default to settle, so
// waitFor/findBy intermittently time out (e.g. ServerDetail's lifecycle buttons
// asserting not-disabled). 5s only raises the ceiling — happy-path tests still
// resolve as fast as the data does — and removes a recurring slow-CI flake.
configure({ asyncUtilTimeout: 5000 });

// Recover Node's FormData constructor. By the time this module evaluates, vitest's
// jsdom environment has already replaced globalThis.FormData with jsdom's, so it
// cannot simply be read off the global. Response, however, is still Node's — the
// jsdom environment copies it over verbatim — and Response.formData() resolves to
// a Node FormData instance, so round-tripping a trivial urlencoded body hands back
// the real constructor. The cast is unavoidable: `.constructor` is typed as
// Function rather than as a FormData constructor.
const NodeFormData = (
  await new Response("a=1", {
    headers: { "content-type": "application/x-www-form-urlencoded" },
  }).formData()
).constructor as typeof globalThis.FormData;

// Radix UI components (Dropdown, Dialog) call into pointer-capture and
// scrollIntoView APIs that jsdom doesn't implement. Without these
// stubs, fireEvent against a Radix trigger throws or silently no-ops.
if (typeof window !== "undefined") {
  if (!Element.prototype.hasPointerCapture) {
    Element.prototype.hasPointerCapture = () => false;
  }
  if (!Element.prototype.releasePointerCapture) {
    Element.prototype.releasePointerCapture = () => {};
  }
  if (!Element.prototype.scrollIntoView) {
    Element.prototype.scrollIntoView = () => {};
  }

  // ResizeObserver / IntersectionObserver / matchMedia — frequently used
  // by virtualized lists, popovers, and theme detection. jsdom omits all
  // three; the stubs no-op so renders that depend on them succeed.
  if (!("ResizeObserver" in window)) {
    (window as unknown as { ResizeObserver: typeof ResizeObserver }).ResizeObserver =
      class {
        observe() {}
        unobserve() {}
        disconnect() {}
      } as unknown as typeof ResizeObserver;
  }
  if (!("IntersectionObserver" in window)) {
    (window as unknown as { IntersectionObserver: typeof IntersectionObserver }).IntersectionObserver =
      class {
        observe() {}
        unobserve() {}
        disconnect() {}
        takeRecords() { return []; }
        root = null;
        rootMargin = "";
        thresholds = [];
      } as unknown as typeof IntersectionObserver;
  }
  if (!window.matchMedia) {
    window.matchMedia = (query: string) =>
      ({
        matches: false,
        media: query,
        onchange: null,
        addListener: () => {},
        removeListener: () => {},
        addEventListener: () => {},
        removeEventListener: () => {},
        dispatchEvent: () => false,
      }) as MediaQueryList;
  }

  // Align jsdom's Blob with Node's Blob so Node 22+ Response and MSW interceptors
  // have native .stream() support and expect(res.blob()).toBeInstanceOf(Blob) succeeds.
  Object.defineProperty(window, "Blob", {
    value: NodeBlob,
    writable: true,
    configurable: true,
  });
  Object.defineProperty(globalThis, "Blob", {
    value: NodeBlob,
    writable: true,
    configurable: true,
  });

  // Align File with Node's File for the same reason. Vitest 5's jsdom environment
  // wraps the global Request and, for any body that is `instanceof` the jsdom
  // window's Blob, rewrites it through a helper that reads jsdom's internal impl
  // via Object.getOwnPropertySymbols(...)[0]. jsdom 30.1.0 moved that impl off the
  // wrapper instance, so the probe yields undefined and the rewrite throws
  // "Cannot read properties of undefined (reading '_buffer')" on every fetch that
  // posts a raw File. A Node File is not instanceof the jsdom Blob, so it skips
  // that path and reaches undici intact. See vitest's createCompatUtils.
  Object.defineProperty(window, "File", {
    value: NodeFile,
    writable: true,
    configurable: true,
  });
  Object.defineProperty(globalThis, "File", {
    value: NodeFile,
    writable: true,
    configurable: true,
  });

  // ...and FormData, for the same reason. endpoints.ts builds its upload bodies
  // with `new FormData()`; jsdom's FormData.append runs an IDL conversion that
  // rejects a Node File ("parameter 2 is not of type 'Blob'"). Node's FormData
  // accepts one natively and serialises to a correct multipart body. Neither call
  // site constructs FormData from an HTMLFormElement, which Node's does not
  // support, so the swap is safe.
  Object.defineProperty(window, "FormData", {
    value: NodeFormData,
    writable: true,
    configurable: true,
  });
  Object.defineProperty(globalThis, "FormData", {
    value: NodeFormData,
    writable: true,
    configurable: true,
  });
}

// MSW lifecycle. onUnhandledRequest:"error" makes a missed handler fail
// loudly rather than the test hanging on a real network call.
beforeAll(() => server.listen({ onUnhandledRequest: "error" }));
afterEach(() => server.resetHandlers());
afterAll(() => server.close());
