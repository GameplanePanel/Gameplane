import React from "react";
import ReactDOM from "react-dom/client";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { RouterProvider, createRouter } from "@tanstack/react-router";
import { RouterProvider as AriaRouterProvider } from "react-aria-components";

import { routeTree } from "@/router/tree";
import { createAriaNavigate, createAriaUseHref } from "@/lib/ariaRouter";
import { AppI18nProvider } from "@/lib/i18n/I18nProvider";
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
