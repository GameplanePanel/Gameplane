import type { JSX } from "react";
import { Breadcrumbs as HeroBreadcrumbs } from "@heroui/react";
import { ChevronRight } from "lucide-react";
import { defineMessages, type IntlShape } from "react-intl";
import { englishIntl, useOptionalIntl } from "@/lib/i18n/format";

export interface Crumb {
  label: string;
  to?: string;
}

// Crumb labels for route parts. Route parts themselves are URL segments and
// are never translated; only the label shown for a known part is.
const routeLabelMessages = defineMessages({
  servers: { id: "web.components.ui.breadcrumbs.servers", defaultMessage: "Servers", description: "Breadcrumb: label for the servers list crumb" },
  modules: { id: "web.components.ui.breadcrumbs.modules", defaultMessage: "Modules", description: "Breadcrumb: label for the modules crumb" },
  cluster: { id: "web.components.ui.breadcrumbs.cluster", defaultMessage: "Cluster", description: "Breadcrumb: label for the cluster crumb" },
  clusters: { id: "web.components.ui.breadcrumbs.clusters", defaultMessage: "Clusters", description: "Breadcrumb: label for the clusters crumb" },
  users: { id: "web.components.ui.breadcrumbs.users_rbac", defaultMessage: "Users & RBAC", description: "Breadcrumb: label for the users and roles crumb" },
  admin: { id: "web.components.ui.breadcrumbs.settings", defaultMessage: "Settings", description: "Breadcrumb: label for the settings crumb (also used for the theme settings page)" },
  audit: { id: "web.components.ui.breadcrumbs.audit_log", defaultMessage: "Audit log", description: "Breadcrumb: label for the audit log crumb" },
  logs: { id: "web.components.ui.breadcrumbs.system_logs", defaultMessage: "System logs", description: "Breadcrumb: label for the system logs crumb" },
  backups: { id: "web.components.ui.breadcrumbs.backups", defaultMessage: "Backups", description: "Breadcrumb: label for the backups crumb" },
  new: { id: "web.components.ui.breadcrumbs.new", defaultMessage: "New", description: "Breadcrumb: label for the create-new crumb" },
});

const dashboardLeafMessage = defineMessages({
  dashboard: { id: "web.components.ui.breadcrumbs.dashboard", defaultMessage: "Dashboard", description: "Breadcrumb: label of the leaf crumb on the root (dashboard) page" },
}).dashboard;

function routeLabel(intl: IntlShape, part: string): string {
  return Object.prototype.hasOwnProperty.call(routeLabelMessages, part)
    ? intl.formatMessage(routeLabelMessages[part as keyof typeof routeLabelMessages])
    : part;
}

/**
 * Builds breadcrumb items from the current pathname.
 * Maps route parts to human-readable labels per the label map,
 * with "gameplane" as the home link and "Dashboard" as the leaf
 * when on the root path. Pass the app's intl to translate the labels;
 * without it the English labels are returned.
 */
export function buildCrumbs(pathname: string, intl: IntlShape = englishIntl): Crumb[] {
  const crumbs: Crumb[] = [{ label: "gameplane", to: "/" }];
  const parts = pathname.split("/").filter(Boolean);
  let acc = "";
  for (const p of parts) {
    acc += "/" + p;
    // Server detail pages ("/servers/:name/...") drop the "Servers" ancestor
    // crumb per design (dPP50, F9pUrx) — the list page itself still shows it.
    if (p === "servers" && parts.length > 1) continue;
    // /settings/theme renders as the same single "Settings" crumb as /admin.
    if (p === "settings" && parts[parts.length - 1] === "theme") continue;
    const label = p === "theme" && parts[0] === "settings" ? routeLabel(intl, "admin") : routeLabel(intl, p);
    crumbs.push({ label, to: acc });
  }
  if (parts.length === 0) crumbs.push({ label: intl.formatMessage(dashboardLeafMessage) });
  return crumbs;
}

/**
 * Route-hierarchy breadcrumb component.
 * Renders the provided crumbs array as navigable links (except the last item,
 * which is marked as the current page via aria-current).
 * Uses HeroUI Breadcrumbs with ChevronRight separator.
 */
export function Breadcrumbs({ items }: { items: Crumb[] }): JSX.Element {
  const intl = useOptionalIntl();
  return (
    <nav aria-label={intl.formatMessage({ id: "web.components.ui.breadcrumbs.breadcrumb", defaultMessage: "Breadcrumb", description: "Breadcrumbs: screen-reader name of the navigation area holding the breadcrumb trail; not visible on screen" })}>
      <HeroBreadcrumbs separator={<ChevronRight className="h-3.5 w-3.5 text-muted" />}>
        {items.map((crumb, index) => {
          const isLast = index === items.length - 1;
          return (
            <HeroBreadcrumbs.Item
              key={crumb.to ?? crumb.label}
              href={crumb.to && !isLast ? crumb.to : undefined}
              className={isLast ? "text-sm text-foreground" : "text-sm text-muted hover:text-foreground"}
              style={isLast ? { color: "var(--foreground)" } : undefined}
            >
              {crumb.label}
            </HeroBreadcrumbs.Item>
          );
        })}
      </HeroBreadcrumbs>
    </nav>
  );
}
