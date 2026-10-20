import { type ReactNode } from "react";
import { ConfirmDialog } from "./ConfirmDialog";
import { AlertTriangle } from "lucide-react";
import { useOptionalIntl } from "@/lib/i18n/format";

export interface ConfirmAdminMappingDialogProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  adminGroups: string[];
  busy?: boolean;
  onConfirm: () => void;
}

export function ConfirmAdminMappingDialog({
  open,
  onOpenChange,
  adminGroups,
  busy,
  onConfirm,
}: ConfirmAdminMappingDialogProps) {
  const intl = useOptionalIntl();
  const description: ReactNode = (
    <div className="space-y-4">
      <div className="rounded-md border border-warning-soft-foreground bg-warning-soft p-[11px] flex gap-3">
        <AlertTriangle className="h-4 w-4 text-warning-soft-foreground flex-shrink-0 mt-0.5" />
        <div className="text-xs">
          <div className="font-medium text-warning-soft-foreground mb-1">{intl.formatMessage({ id: "web.components.ui.confirm_admin_mapping_dialog.full_admin_access", defaultMessage: "Full admin access", description: "Confirm admin mapping dialog: bold warning heading shown above the explanation of mapping users to the admin role" })}</div>
          <p className="text-warning-soft-foreground">{intl.formatMessage({ id: "web.components.ui.confirm_admin_mapping_dialog.mapping_users_to_the_admin_role_grants", defaultMessage: "Mapping users to the admin role grants full cluster control. Ensure the mapped group contains only authorized personnel. Anyone in these groups gets full admin access from their next login.", description: "Confirm admin mapping dialog: warning that the admin role grants full cluster control, so only trusted people belong in the mapped groups; applies at next sign-in" })}</p>
        </div>
      </div>
      {adminGroups.length > 0 && (
        <div className="space-y-2">
          <p className="text-xs text-muted">{intl.formatMessage({ id: "web.components.ui.confirm_admin_mapping_dialog.group_s_being_mapped_to_admin", defaultMessage: "Group(s) being mapped to admin:", description: "Confirm admin mapping dialog: label above the list of groups that are being mapped to the admin role" })}</p>
          <div className="flex flex-wrap gap-2">
            {adminGroups.map((group) => (
              <span
                key={group}
                className="px-2 py-1 rounded text-xs bg-[#94949426] text-muted"
              >
                {group}
              </span>
            ))}
          </div>
        </div>
      )}
    </div>
  );

  return (
    <ConfirmDialog
      open={open}
      onOpenChange={onOpenChange}
      title={intl.formatMessage({ id: "web.components.ui.confirm_admin_mapping_dialog.confirm_admin_role_mapping", defaultMessage: "Confirm admin role mapping?", description: "Confirm admin mapping dialog: title of the dialog asking the user to confirm the admin role mapping" })}
      description={description}
      confirmLabel={intl.formatMessage({ id: "web.components.ui.confirm_admin_mapping_dialog.map_to_admin_role", defaultMessage: "Map to admin role", description: "Confirm admin mapping dialog: confirm button that maps the groups to the admin role; keep it short" })}
      destructive
      busy={busy}
      onConfirm={onConfirm}
    />
  );
}
