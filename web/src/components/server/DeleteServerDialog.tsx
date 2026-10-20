import { useResourceClient, useResourceTarget, type ResourceTarget } from "@/lib/resourceTarget";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { ConfirmDialog } from "@/components/ui/ConfirmDialog";

import { errorTextIntl } from "@/lib/errors";
import { useOptionalIntl } from "@/lib/i18n/format";

interface Props {
  target?: ResourceTarget;
  name: string;
  ns?: string;
  open: boolean;
  onOpenChange: (open: boolean) => void;
  /** Called after the server is deleted — navigate away or invalidate lists. */
  onDeleted?: () => void;
}

// DeleteServerDialog is the confirm-to-delete flow, self-contained (owns its
// mutation + error) so it can be dropped into the Settings danger zone, the
// server-detail header menu, or a Servers-list row menu alike.
export function DeleteServerDialog({
  target: explicitTarget, name, ns, open, onOpenChange, onDeleted }: Props) {
  const intl = useOptionalIntl();
  const qc = useQueryClient();
  const resourceTarget = useResourceTarget({ name, namespace: ns }, explicitTarget);
  const resourceClient = useResourceClient(resourceTarget);
  const { Servers } = resourceClient;
  const del = useMutation({
    mutationFn: () => Servers.remove(name, ns),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["fleet"] });
      onOpenChange(false);
      onDeleted?.();
    },
  });

  return (
    <ConfirmDialog
      open={open}
      onOpenChange={(v) => {
        if (!v) del.reset();
        onOpenChange(v);
      }}
      title={intl.formatMessage({ id: "web.components.server.delete_server_dialog.title", defaultMessage: "Delete {name}?", description: "Delete server dialog: dialog title; {name} is the server name" }, { name: name })}
      description={
        <>{intl.formatMessage({ id: "web.components.server.delete_server_dialog.this_will_permanently_remove_the", defaultMessage: "This will permanently remove the GameServer resource and detach its persistent volume. Data will be reclaimed by the cluster according to the StorageClass reclaim policy.", description: "Delete server dialog: warning that the GameServer resource is removed permanently and its volume detached, with data reclaimed per the StorageClass policy; resource names are kept in English" })}{del.isError && (
            <div className="pt-2 text-danger">{errorTextIntl(intl, del.error, intl.formatMessage({ id: "web.components.server.delete_server_dialog.delete_failed", defaultMessage: "delete failed", description: "Delete server dialog: fallback error text when the delete request fails without a message" }))}</div>
          )}
        </>
      }
      confirmPhrase={name}
      confirmLabel={intl.formatMessage({ id: "web.components.server.delete_server_dialog.delete_server", defaultMessage: "Delete server", description: "Delete server dialog: confirm button that deletes the server once the user has typed the server name; keep it short" })}
      destructive
      busy={del.isPending}
      onConfirm={() => del.mutate()}
    />
  );
}
