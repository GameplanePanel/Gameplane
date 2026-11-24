import React from "react";
import ReactDOM from "react-dom/client";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { RouterProvider, createRouter } from "@tanstack/react-router";
import { RouterProvider as AriaRouterProvider } from "react-aria-components";

import { routeTree } from "@/router/tree";
import { createAriaNavigate, createAriaUseHref } from "@/lib/ariaRouter";
import { AppI18nProvider } from "@/lib/i18n/I18nProvider";
// Fonts are bundled, not loaded from Google Fonts: a third-party stylesheet
// would run in the dashboard origin on every page, the login page included,
// and the CSP would have to allow it (see cspHeader in vite.config.ts).
// The weights match what the design uses (Geist 400-700, JetBrains Mono 400-600).
import "@fontsource/geist/400.css";
import "@fontsource/geist/500.css";
import "@fontsource/geist/600.css";
import "@fontsource/geist/700.css";
import "@fontsource/jetbrains-mono/400.css";
import "@fontsource/jetbrains-mono/500.css";
import "@fontsource/jetbrains-mono/600.css";
import "@/styles/globals.css";

const router = createRouter({ routeTree });

declare module "@tanstack/react-router" {
  interface Register { router: typeof router }
}

const queryClient = new QueryClient({
  defaultOptions: {
    queries: { staleTime: 10_000, refetchOnWindowFocus: false },
  },
});

async function bootstrap(): Promise<void> {
  // Dynamic-import the MSW worker only when the E2E mock flag is set.
  // VITE_E2E_MOCK comes from web/.env.mock, loaded by `vite --mode mock`
  // (Playwright's webServer in mock target). The branch is statically
  // dead in production (vite resolves import.meta.env at build time),
  // so the bundle never ships MSW.
  if (import.meta.env.VITE_E2E_MOCK === "true") {
    const { startMSW } = await import("@/test/browser-msw");
    await startMSW();
  }

  ReactDOM.createRoot(document.getElementById("root")!).render(
    <React.StrictMode>
      <QueryClientProvider client={queryClient}>
        <AppI18nProvider>
          <AriaRouterProvider
            navigate={createAriaNavigate(router)}
            useHref={createAriaUseHref(router)}
          >
            <RouterProvider router={router} />
          </AriaRouterProvider>
        </AppI18nProvider>
      </QueryClientProvider>
    </React.StrictMode>,
  );
}

void bootstrap();
