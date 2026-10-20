import { useResourceAccess } from "@/lib/resourceTarget";
import { useState, type ReactNode } from "react";
import { useNavigate } from "@tanstack/react-router";
import { Button, Card, CardContent } from "@heroui/react";
import { ignoreRejection } from "@/lib/utils";
import { DeleteServerDialog } from "@/components/server/DeleteServerDialog";
import { WipeServerDialog } from "@/components/server/WipeServerDialog";
import { TransferServerDialog } from "@/components/server/TransferServerDialog";
import { useOptionalIntl } from "@/lib/i18n/format";

interface Props {
  name: string;
  ns?: string;
}

export function DangerSection({ name, ns }: Props) {
  const intl = useOptionalIntl();
  const access = useResourceAccess();
  const canManage = access?.canDelete === true;
  const navigate = useNavigate();
  const [confirmOpen, setConfirmOpen] = useState(false);
  const [wipeOpen, setWipeOpen] = useState(false);
  const [transferOpen, setTransferOpen] = useState(false);

  return (
    <div className="space-y-3">
      <Row
        title={intl.formatMessage({ id: "web.routes.tabs.settings.danger.wipe_world_data", defaultMessage: "Wipe world data", description: "Danger: title of the danger row that deletes all world data and restarts on a fresh world" })}
        body={intl.formatMessage({ id: "web.routes.tabs.settings.danger.wipe_world_body", defaultMessage: "Deletes everything on the data volume and restarts the server on a fresh world. Take a backup first.", description: "Danger: explanation under the Wipe world data row" })}
        action={
          <Button variant="ghost" size="sm" isDisabled={!canManage} onPress={() => setWipeOpen(true)}>{intl.formatMessage({ id: "web.routes.tabs.settings.danger.wipe_world", defaultMessage: "Wipe world…", description: "Button that opens the wipe world data confirmation" })}</Button>
        }
      />
      <Row
        title={intl.formatMessage({ id: "web.routes.tabs.settings.danger.transfer_ownership", defaultMessage: "Transfer ownership", description: "Danger: title of the danger row that hands the server to another user; the text after it explains the user keeps access only as a collaborator" })}
        body={intl.formatMessage({ id: "web.routes.tabs.settings.danger.transfer_ownership_body", defaultMessage: "Hand this server to another user. You keep access only if you stay a collaborator.", description: "Danger: explanation under the Transfer ownership row" })}
        action={
          <Button variant="ghost" size="sm" isDisabled={!canManage} onPress={() => setTransferOpen(true)}>{intl.formatMessage({ id: "web.routes.tabs.settings.danger.transfer", defaultMessage: "Transfer…", description: "Button that opens the transfer ownership dialog" })}</Button>
        }
      />
      <Row
        title={intl.formatMessage({ id: "web.routes.tabs.settings.danger.delete_server", defaultMessage: "Delete server", description: "Danger: title of the danger row that deletes the whole server, including its persistent volume; the text below warns it cannot be undone" })}
        body={intl.formatMessage({ id: "web.routes.tabs.settings.danger.delete_server_body", defaultMessage: "Removes the GameServer and its persistent volume. This cannot be undone.", description: "Danger: explanation under the Delete server row" })}
        dangerZone="delete"
        action={
          <Button variant="danger" size="sm" isDisabled={!access?.canDelete} onPress={() => setConfirmOpen(true)}>{intl.formatMessage({ id: "web.routes.tabs.settings.danger.delete", defaultMessage: "Delete", description: "Button that opens the delete server confirmation" })}</Button>
        }
      />
      <WipeServerDialog name={name} ns={ns} open={wipeOpen} onOpenChange={setWipeOpen} />
      <TransferServerDialog
        name={name}
        ns={ns}
        open={transferOpen}
        onOpenChange={setTransferOpen}
      />
      <DeleteServerDialog
        name={name}
        ns={ns}
        open={confirmOpen}
        onOpenChange={setConfirmOpen}
        onDeleted={() => ignoreRejection(navigate({ to: "/servers" }))}
      />
    </div>
  );
}

function Row({
  title,
  body,
  action,
  dangerZone,
}: {
  title: string;
  body: string;
  action: ReactNode;
  dangerZone?: "delete";
}) {
  return (
    <Card data-danger-zone={dangerZone}>
      <CardContent className="flex items-start justify-between gap-4">
        <div className="min-w-0">
          <div className="text-sm font-medium">{title}</div>
          <div className="pt-1 text-xs text-default-500">{body}</div>
        </div>
        {action}
      </CardContent>
    </Card>
  );
}
