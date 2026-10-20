import { useState } from "react";
import {
  ArrowRightLeft,
  Copy,
  Eraser,
  MoreHorizontal,
  Trash2,
} from "lucide-react";
import { buttonVariants } from "@heroui/styles";
import type { GameServer } from "@/types";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/DropdownMenu";
import { useResourceTarget, useResourceAccess, type ResourceTarget, type ServerAccess } from "@/lib/resourceTarget";

import { CloneServerDialog } from "./CloneServerDialog";
import { DeleteServerDialog } from "./DeleteServerDialog";
import { TransferServerDialog } from "./TransferServerDialog";
import { WipeServerDialog } from "./WipeServerDialog";
import { useOptionalIntl } from "@/lib/i18n/format";

interface Props {
  target?: ResourceTarget;
  access?: ServerAccess;
  gs: GameServer;
  onDeleted?: () => void;
  onTransferred?: () => void;
}

export function ServerActionsMenu({ gs, onDeleted, onTransferred, target: explicitTarget, access: explicitAccess }: Props) {
  const intl = useOptionalIntl();
  const ns = gs.metadata.namespace;
  const target = useResourceTarget({ name: gs.metadata.name, namespace: ns }, explicitTarget);
  const access = useResourceAccess(explicitAccess);
  const canClone = access?.canWrite === true;
  const canManage = access?.canDelete === true;
  const canDelete = access?.canDelete === true;

  const [cloneOpen, setCloneOpen] = useState(false);
  const [transferOpen, setTransferOpen] = useState(false);
  const [wipeOpen, setWipeOpen] = useState(false);
  const [deleteOpen, setDeleteOpen] = useState(false);

  return (
    <>
      <DropdownMenu>
        <DropdownMenuTrigger
          className={buttonVariants({ isIconOnly: true, variant: "ghost" })}
          aria-label={intl.formatMessage({ id: "web.components.server.server_actions_menu.server_actions", defaultMessage: "Server actions", description: "Server actions menu: screen-reader label for the icon-only menu button that opens the server actions; not visible on screen" })}
        >
          <MoreHorizontal className="h-4 w-4" />
        </DropdownMenuTrigger>
        <DropdownMenuContent>
          <DropdownMenuItem
            icon={<Copy className="h-4 w-4" />}
            label={intl.formatMessage({ id: "web.components.server.server_actions_menu.clone_server", defaultMessage: "Clone server", description: "Server actions menu: menu item that opens the Clone server dialog; keep it short" })}
            onSelect={() => setCloneOpen(true)}
            disabled={!canClone}
            hint={canClone ? undefined : intl.formatMessage({ id: "web.components.server.server_actions_menu.requires_operator_role", defaultMessage: "Requires operator role", description: "Server actions menu: hint shown on a disabled item when the user lacks the operator role" })}
          />
          <DropdownMenuItem
            icon={<ArrowRightLeft className="h-4 w-4" />}
            label={intl.formatMessage({ id: "web.components.server.server_actions_menu.transfer_ownership", defaultMessage: "Transfer ownership", description: "Server actions menu: menu item that opens the transfer ownership dialog; disabled with a hint for users without the operator role; keep it short" })}
            onSelect={() => setTransferOpen(true)}
            disabled={!canManage}
            hint={canManage ? undefined : intl.formatMessage({ id: "web.components.server.server_actions_menu.requires_owner_or_operator_role", defaultMessage: "Requires owner or operator role", description: "Server actions menu: hint shown on a disabled item when the user lacks the owner or operator role" })}
          />
          <DropdownMenuItem
            icon={<Eraser className="h-4 w-4" />}
            label={intl.formatMessage({ id: "web.components.server.server_actions_menu.wipe_world_data", defaultMessage: "Wipe world data", description: "Server actions menu: menu item that opens the wipe world data dialog; disabled with a hint for users without owner or operator rights; keep it short" })}
            onSelect={() => setWipeOpen(true)}
            disabled={!canManage}
            hint={canManage ? undefined : intl.formatMessage({ id: "web.components.server.server_actions_menu.requires_owner_or_operator_role", defaultMessage: "Requires owner or operator role", description: "Server actions menu: hint shown on a disabled item when the user lacks the owner or operator role" })}
            destructive
          />
          <DropdownMenuSeparator />
          <DropdownMenuItem
            icon={<Trash2 className="h-4 w-4" />}
            label={intl.formatMessage({ id: "web.components.server.server_actions_menu.delete_server", defaultMessage: "Delete server", description: "Server actions menu: destructive menu item that opens the delete server dialog; keep it short" })}
            onSelect={() => setDeleteOpen(true)}
            disabled={!canDelete}
            destructive
          />
        </DropdownMenuContent>
      </DropdownMenu>

      <CloneServerDialog
        target={target}
        open={cloneOpen}
        onOpenChange={setCloneOpen}
        sourceName={gs.metadata.name}
        ns={ns}
      />
      <TransferServerDialog
        target={target}
        name={gs.metadata.name}
        ns={ns}
        open={transferOpen}
        onOpenChange={setTransferOpen}
        onTransferred={onTransferred}
      />
      <WipeServerDialog
        target={target}
        name={gs.metadata.name}
        ns={ns}
        open={wipeOpen}
        onOpenChange={setWipeOpen}
      />
      <DeleteServerDialog
        target={target}
        name={gs.metadata.name}
        ns={ns}
        open={deleteOpen}
        onOpenChange={setDeleteOpen}
        onDeleted={onDeleted}
      />
    </>
  );
}
