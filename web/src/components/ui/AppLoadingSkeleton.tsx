// AppLoadingSkeleton mirrors the app shell (design N13Xud, "Screen/App
// Loading") — sidebar rail, top bar, stat-card row, table placeholder —
// so the transition into the loaded shell doesn't flash a layout shift.
//
// It is the fallback AppLayout renders while `useMe()` (GET /users/me) is
// pending (see AppLayout.tsx), which is what the "App — Loading State"
// screenshot spec (web/e2e/screenshots/slice1.spec.ts, node N13Xud) delays
// by 800ms to capture. It is also wired as the root route's
// `pendingComponent` (web/src/router/tree.tsx) for any future route-level
// loader that takes long enough to trigger it.

import { ShieldCheck } from "lucide-react";
import { Skeleton } from "@heroui/react";
import { useOptionalIntl } from "@/lib/i18n/format";

export interface AppLoadingSkeletonProps {
  /** Number of stat-card placeholders in the content row. Defaults to 4
   *  to match the design's `skStats` row (N13Xud). */
  statCount?: number;
}

export function AppLoadingSkeleton({ statCount = 4 }: AppLoadingSkeletonProps) {
  const intl = useOptionalIntl();
  return (
    <div
      className="flex h-full bg-background text-fg"
      role="status"
      aria-busy="true"
      aria-label={intl.formatMessage({ id: "web.components.ui.app_loading_skeleton.loading", defaultMessage: "Loading", description: "App loading skeleton: screen-reader label announced while the app is loading; not visible on screen" })}
    >
      {/* Desktop sidebar — always rendered, hidden on mobile */}
      <div className="hidden lg:flex">
        <aside className="flex w-[260px] shrink-0 flex-col border-r border-border bg-surface/60">
          {/* Brand block — real wordmark, skeleton cluster line */}
          <div className="flex items-center gap-2 px-5 py-4">
            <div className="flex h-8 w-8 items-center justify-center rounded-md bg-primary/15">
              <ShieldCheck className="h-4 w-4 text-primary" />
            </div>
            <div className="min-w-0 flex-1 leading-tight">
              <div className="font-mono text-base font-semibold text-fg">{intl.formatMessage({ id: "web.components.ui.app_loading_skeleton.gameplane", defaultMessage: "gameplane", description: "App loading skeleton: product name shown in the placeholder header while the app loads; keep the name as written" })}</div>
              <Skeleton className="h-3 w-20 rounded bg-surface/secondary" />
            </div>
          </div>

          {/* Nav area with skeleton items */}
          <nav className="flex-1 overflow-auto px-3 py-2 scrollbar-thin">
            <div className="px-3 pb-2 pt-3 text-[10px] font-semibold uppercase tracking-widest text-muted">{intl.formatMessage({ id: "web.components.ui.app_loading_skeleton.general", defaultMessage: "General", description: "App loading skeleton: placeholder heading above the first group of navigation items while the page loads" })}</div>
            <ul className="flex flex-col gap-0.5">
              {Array.from({ length: 4 }).map((_, i) => (
                <li key={`general-${i}`}>
                  <div className="flex items-center gap-3 rounded-md px-3 py-2">
                    <Skeleton className="h-[18px] w-[18px] shrink-0 rounded bg-surface/secondary" />
                    <Skeleton className="h-3 flex-1 rounded bg-surface/secondary" />
                  </div>
                </li>
              ))}
            </ul>

            <div className="h-3" />
            <div className="px-3 pb-2 pt-3 text-[10px] font-semibold uppercase tracking-widest text-muted">{intl.formatMessage({ id: "web.components.ui.app_loading_skeleton.admin", defaultMessage: "Admin", description: "App loading skeleton: placeholder heading above the admin group of navigation items while the page loads" })}</div>
            <ul className="flex flex-col gap-0.5">
              {Array.from({ length: 3 }).map((_, i) => (
                <li key={`admin-${i}`}>
                  <div className="flex items-center gap-3 rounded-md px-3 py-2">
                    <Skeleton className="h-[18px] w-[18px] shrink-0 rounded bg-surface/secondary" />
                    <Skeleton className="h-3 flex-1 rounded bg-surface/secondary" />
                  </div>
                </li>
              ))}
            </ul>
          </nav>

          {/* Profile footer skeleton */}
          <div className="border-t border-border px-3 py-3">
            <div className="flex items-center gap-3 rounded-md px-2 py-1.5">
              <Skeleton className="h-8 w-8 shrink-0 rounded-full bg-surface/secondary" />
              <div className="min-w-0 flex-1 space-y-1">
                <Skeleton className="h-3 rounded bg-surface/secondary" />
                <Skeleton className="h-2 w-16 rounded bg-surface/secondary" />
              </div>
            </div>
          </div>
        </aside>
      </div>

      {/* Main column */}
      <div className="flex min-w-0 flex-1 flex-col">
        {/* TopBar skeleton */}
        <header className="flex h-14 items-center justify-between gap-4 border-b border-border bg-background px-3 sm:px-6">
          <div className="flex min-w-0 items-center gap-2">
            <Skeleton className="h-3 w-20 rounded bg-surface" />
          </div>
          <div className="flex shrink-0 items-center gap-3">
            <div className="hidden md:flex">
              <Skeleton className="h-9 w-72 rounded-md bg-surface" />
            </div>
            <Skeleton className="h-8 w-8 shrink-0 rounded-full bg-surface" />
            <Skeleton className="h-8 w-8 shrink-0 rounded-full bg-surface" />
            <Skeleton className="h-8 w-8 shrink-0 rounded-full bg-surface" />
          </div>
        </header>

        {/* Main content skeleton */}
        <main className="flex-1 overflow-auto scrollbar-thin p-6">
          <Skeleton className="mb-6 h-4 w-48 rounded bg-surface/secondary" />

          <div className="mb-6 grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-4">
            {Array.from({ length: statCount }).map((_, i) => (
              <div
                key={`stat-${i}`}
                data-testid="stat-placeholder"
                className="rounded-md border border-border bg-surface/60 p-4"
              >
                <Skeleton className="mb-2 h-3 w-24 rounded bg-surface/secondary" />
                <Skeleton className="h-6 w-12 rounded bg-surface/secondary" />
              </div>
            ))}
          </div>

          <div className="rounded-md border border-border">
            <div className="flex border-b border-border px-4 py-3">
              <Skeleton className="h-3 flex-1 rounded bg-surface/secondary" />
              <Skeleton className="ml-4 h-3 w-20 rounded bg-surface/secondary" />
            </div>
            {Array.from({ length: 5 }).map((_, i) => (
              <div key={`row-${i}`} className="flex border-b border-border px-4 py-3 last:border-b-0">
                <Skeleton className="h-3 flex-1 rounded bg-surface/secondary" />
                <Skeleton className="ml-4 h-3 w-20 rounded bg-surface/secondary" />
              </div>
            ))}
          </div>
        </main>
      </div>
    </div>
  );
}
