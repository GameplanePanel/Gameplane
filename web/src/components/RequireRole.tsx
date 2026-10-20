import { useEffect, type ReactNode } from "react";
import { ShieldAlert, PlugZap } from "lucide-react";
import { Link } from "@tanstack/react-router";
import { APIError } from "@/lib/api";
import { useMe, can } from "@/lib/auth";
import { Button, Card } from "@heroui/react";
import { useOptionalIntl } from "@/lib/i18n/format";

function isUnauthorized(error: unknown): boolean {
  return error instanceof APIError && error.status === 401;
}

interface PermProps {
  perm: string;
  children: ReactNode;
}

/**
 * RequirePermission gates a page on a single permission instead of a fixed
 * role list, so custom roles that hold the permission get access. Cluster-
 * scoped pages (Users, Settings, Audit) are the natural fit.
 */
export function RequirePermission({ perm, children }: PermProps) {
  const { data: me, error, isLoading, refetch } = useMe();

  useEffect(() => {
    if (isUnauthorized(error)) location.assign("/login");
  }, [error]);

  if (isLoading) return <RoleSkeleton />;
  if (isUnauthorized(error)) return <RoleSkeleton />;
  if (!me) return <IdentityUnavailable error={error} onRetry={() => void refetch()} />;
  if (!can(me, perm)) return <Forbidden />;
  return <>{children}</>;
}

function RoleSkeleton() {
  return (
    <div className="p-6">
      <Card className="h-32 animate-pulse">{null}</Card>
    </div>
  );
}

/**
 * IdentityUnavailable covers the case where /users/me could not be loaded for
 * a reason other than 401 — an API rollout, a proxy error, a stalled query.
 * The user's permissions are unknown, not absent, so claiming "access denied"
 * would be a lie that only a manual reload could undo. Exported so
 * in-page permission gates (e.g. the server Capture tab) can show the same
 * state instead of a false denial.
 */
export function IdentityUnavailable({ error, onRetry }: { error: unknown; onRetry: () => void }) {
  const intl = useOptionalIntl();
  const detail = error instanceof APIError ? `HTTP ${error.status}` : null;
  return (
    <div className="flex h-full items-center justify-center p-6">
      <Card className="max-w-md p-8 text-center">
        <div className="flex justify-center">
          <div className="flex h-12 w-12 items-center justify-center rounded-full bg-muted/15 text-muted">
            <PlugZap className="h-6 w-6" />
          </div>
        </div>
        <div className="pt-4 text-base font-medium text-fg">{intl.formatMessage({ id: "web.components.require_role.can_t_reach_the_control_plane", defaultMessage: "Can't reach the control plane", description: "Require role: heading of the error card shown when the user's identity cannot be confirmed because the API cannot be reached" })}</div>
        <p className="pt-1 text-sm text-muted">{intl.formatMessage({ id: "web.components.require_role.unreachable_explanation", defaultMessage: "We couldn't confirm who you are{hasDetail, select, true { ({detail})} other {}}. This is usually temporary — the API may be restarting.", description: "Require role: explanation under the unreachable heading saying the user's identity could not be confirmed and the problem is usually temporary; {hasDetail} is true or false and decides whether the error detail in parentheses is shown; {detail} is the error detail, e.g. HTTP 502" }, { hasDetail: detail ? "true" : "false", detail: detail ?? "" })}</p>
        <Button className="mt-4" onClick={onRetry}>{intl.formatMessage({ id: "web.components.require_role.try_again", defaultMessage: "Try again", description: "Require role: button on the unreachable error card that retries the identity check; keep it short" })}</Button>
      </Card>
    </div>
  );
}

function Forbidden() {
  const intl = useOptionalIntl();
  return (
    <div className="flex h-full items-center justify-center p-6">
      <Card className="max-w-md p-8 text-center">
        <div className="flex justify-center">
          <div className="flex h-12 w-12 items-center justify-center rounded-full bg-warning/15 text-warning">
            <ShieldAlert className="h-6 w-6" />
          </div>
        </div>
        <div className="pt-4 text-base font-medium text-fg">{intl.formatMessage({ id: "web.components.require_role.access_denied", defaultMessage: "Access denied", description: "Require role: heading of the card shown when the user's role does not allow the page they opened" })}</div>
        <p className="pt-1 text-sm text-muted">{intl.formatMessage({ id: "web.components.require_role.your_role_doesn_t_permit_access_to_this", defaultMessage: "Your role doesn't permit access to this page. Ask an administrator if you believe this is a mistake.", description: "Require role: explanation under the access denied heading saying the user's role does not allow this page and to ask an administrator if this is a mistake" })}</p>
        <Link
          to="/"
          className="mt-4 inline-block rounded-md bg-primary px-4 py-2 text-sm text-primary-fg hover:opacity-90"
        >{intl.formatMessage({ id: "web.components.require_role.back_to_dashboard", defaultMessage: "Back to dashboard", description: "Require role: link on the access denied card that returns to the home dashboard; keep it short" })}</Link>
      </Card>
    </div>
  );
}
