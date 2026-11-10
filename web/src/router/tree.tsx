import {
  RootRoute, Route, Outlet,
} from "@tanstack/react-router";
import { AppLayout } from "@/components/AppLayout";
import { RequirePermission } from "@/components/RequireRole";
import { LoginPage } from "@/routes/Login";
import { SharePage } from "@/routes/Share";
import { DashboardPage } from "@/routes/Dashboard";
import { ServersPage } from "@/routes/Servers";
import { ServerDetailPage } from "@/routes/ServerDetail";
import { ModulesPage } from "@/routes/Modules";
import { ClusterPage } from "@/routes/Cluster";
import { NodesPage } from "@/pages/Nodes";
import { ClusterDetailPage } from "@/pages/ClusterDetail";
import { ClustersPage } from "@/routes/Clusters";
import { UsersPage } from "@/routes/Users";
import { AdminSettingsPage } from "@/routes/AdminSettings";
import { ThemeSettingsPage } from "@/routes/ThemeSettings";
import { CreateServerWizard } from "@/routes/CreateServer";
import { BackupsPage } from "@/routes/Backups";
import { AuditLogPage } from "@/routes/AuditLog";
import { AdminLogsPage } from "@/routes/AdminLogs";
import { NotFoundPage } from "@/routes/NotFound";

const rootRoute = new RootRoute({ component: Outlet });

const loginRoute = new Route({
  getParentRoute: () => rootRoute,
  path: "/login",
  component: LoginPage,
});

const shareRoute = new Route({
  getParentRoute: () => rootRoute,
  path: "/share/$token",
  component: SharePage,
});

const appLayoutRoute = new Route({
  getParentRoute: () => rootRoute,
  id: "app-layout",
  component: AppLayout,
});

const dashboardRoute = new Route({
  getParentRoute: () => appLayoutRoute,
  path: "/",
  component: DashboardPage,
  validateSearch: (search: Record<string, unknown>): { cluster?: string } => ({
    cluster: typeof search.cluster === "string" && search.cluster !== "" ? search.cluster : undefined,
  }),
});

const serversRoute = new Route({
  getParentRoute: () => appLayoutRoute,
  path: "/servers",
  component: ServersPage,
  validateSearch: (search: Record<string, unknown>): { cluster?: string } => ({
    cluster: typeof search.cluster === "string" && search.cluster !== "" ? search.cluster : undefined,
  }),
});

const serverDetailRoute = new Route({
  getParentRoute: () => appLayoutRoute,
  path: "/servers/$name",
  component: ServerDetailPage,
  validateSearch: (search: Record<string, unknown>): { ns?: string; cluster?: string } => {
    for (const key of ["cluster", "ns"] as const) {
      if (search[key] !== undefined && (typeof search[key] !== "string" || !search[key])) throw new Error(`Invalid server ${key}`);
    }
    return { ns: search.ns as string | undefined, cluster: search.cluster as string | undefined };
  },
});

const createServerRoute = new Route({
  getParentRoute: () => appLayoutRoute,
  path: "/servers/new",
  component: CreateServerWizard,
  // Lets the Modules catalog "Deploy" link pre-select a template via
  // /servers/new?template=<name>.
  validateSearch: (search: Record<string, unknown>): { template?: string; cluster?: string; ns?: string } => {
    for (const key of ["cluster", "ns"] as const) {
      if (search[key] !== undefined && (typeof search[key] !== "string" || !search[key])) throw new Error(`Invalid placement ${key}`);
    }
    return { template: typeof search.template === "string" ? search.template : undefined, cluster: search.cluster as string | undefined, ns: search.ns as string | undefined };
  },
});

const modulesRoute = new Route({
  getParentRoute: () => appLayoutRoute,
  path: "/modules",
  component: ModulesPage,
});

const clusterRoute = new Route({
  getParentRoute: () => appLayoutRoute,
  path: "/cluster",
  component: ClusterPage,
});

const nodesRoute = new Route({
  getParentRoute: () => appLayoutRoute,
  path: "/nodes",
  component: NodesPage,
  validateSearch: (search: Record<string, unknown>): { cluster?: string } => ({
    cluster: typeof search.cluster === "string" && search.cluster !== "" ? search.cluster : undefined,
  }),
});

const clusterDetailRoute = new Route({
  getParentRoute: () => appLayoutRoute,
  path: "/clusters/$clusterId",
  component: ClusterDetailPage,
  validateSearch: (search: Record<string, unknown>): { tab?: "overview" | "nodes" | "configuration" } => ({
    tab: search.tab === "nodes" || search.tab === "configuration" ? search.tab : "overview",
  }),
});

const clustersRoute = new Route({
  getParentRoute: () => appLayoutRoute,
  path: "/clusters",
  component: ClustersPage,
});

const usersRoute = new Route({
  getParentRoute: () => appLayoutRoute,
  path: "/users",
  component: () => (
    <RequirePermission perm="users:manage">
      <UsersPage />
    </RequirePermission>
  ),
});

const adminRoute = new Route({
  getParentRoute: () => appLayoutRoute,
  path: "/admin",
  // Lets the theme page's settings nav deep-link a section via
  // /admin?section=<key>; AdminSettingsPage reads it on mount.
  validateSearch: (search: Record<string, unknown>): { section?: string } => ({
    section: typeof search.section === "string" ? search.section : undefined,
  }),
  component: () => (
    <RequirePermission perm="config:manage">
      <AdminSettingsPage />
    </RequirePermission>
  ),
});

// Per-user theme preferences need no special permission (any authenticated
// user styles their own dashboard), so the route has no RequirePermission
// gate — same as /backups.
const themeSettingsRoute = new Route({
  getParentRoute: () => appLayoutRoute,
  path: "/settings/theme",
  component: ThemeSettingsPage,
});

const auditLogRoute = new Route({
  getParentRoute: () => appLayoutRoute,
  path: "/admin/audit",
  component: () => (
    <RequirePermission perm="audit:read">
      <AuditLogPage />
    </RequirePermission>
  ),
});

// The API guards /admin/system-logs with the admin wildcard permission
// ("*"), so the page gates on the same — not a narrower named permission.
const adminLogsRoute = new Route({
  getParentRoute: () => appLayoutRoute,
  path: "/admin/logs",
  component: () => (
    <RequirePermission perm="*">
      <AdminLogsPage />
    </RequirePermission>
  ),
});

const backupsRoute = new Route({
  getParentRoute: () => appLayoutRoute,
  path: "/backups",
  component: BackupsPage,
  validateSearch: (search: Record<string, unknown>): { cluster?: string; tab?: string } => ({
    cluster: typeof search.cluster === "string" && search.cluster !== "" ? search.cluster : undefined,
    tab: typeof search.tab === "string" ? search.tab : undefined,
  }),
});

// Catch-all: any URL no other route claims renders the 404 page inside the
// authenticated shell. AppLayout owns the auth guard (401 -> /login), and
// /login and /share/$token are siblings that match first, so neither ever
// gets the shell.
const notFoundRoute = new Route({
  getParentRoute: () => appLayoutRoute,
  path: "$",
  component: NotFoundPage,
});

export const routeTree = rootRoute.addChildren([
  loginRoute,
  shareRoute,
  appLayoutRoute.addChildren([
    dashboardRoute,
    createServerRoute,
    serversRoute,
    serverDetailRoute,
    modulesRoute,
    clusterRoute,
    nodesRoute,
    clustersRoute,
    clusterDetailRoute,
    usersRoute,
    adminRoute,
    themeSettingsRoute,
    auditLogRoute,
    adminLogsRoute,
    backupsRoute,
    notFoundRoute,
  ]),
]);
