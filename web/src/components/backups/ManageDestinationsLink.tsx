import { Link } from "@tanstack/react-router";
import { can, useMe } from "@/lib/auth";
import { DESTINATION_NAMESPACE } from "@/lib/destinations";
import { useOptionalIntl } from "@/lib/i18n/format";

export function ManageDestinationsLink({ cluster, namespace = DESTINATION_NAMESPACE }: { cluster: string; namespace?: string }) {
  const identity = useMe();
  const intl = useOptionalIntl();
  if (!can(identity.data, "destinations:read", namespace, cluster) ||
      !can(identity.data, "destinations:manage", namespace, cluster)) return null;

  return (
    <Link to="/clusters/$clusterId" params={{ clusterId: cluster }} search={{ tab: "configuration", ns: namespace }}
      className="text-xs text-primary underline">
      {intl.formatMessage({ id: "web.backups.manage_destinations", defaultMessage: "Manage destinations", description: "Link from backups to the selected cluster's destination configuration" })}
    </Link>
  );
}
