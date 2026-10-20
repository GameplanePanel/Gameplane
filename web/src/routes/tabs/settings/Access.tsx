import { useResourceClient, useResourceTarget, resourceKey, useResourceAccess } from "@/lib/resourceTarget";
import { useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { X } from "lucide-react";
import type { GameServer } from "@/types";
import { Button, Input, Label, Chip, Card, CardContent } from "@heroui/react";

import { errorTextIntl } from "@/lib/errors";
import { OWNER_ANNOTATION } from "@/lib/annotations";
import { useOptionalIntl } from "@/lib/i18n/format";
import type { IntlShape } from "react-intl";

const COLLABORATORS_ANNOTATION = "gameplane.local/collaborators";
const COLLABORATOR_NAMES_ANNOTATION = "gameplane.local/collaborator-names";

interface Props {
  gs?: GameServer;
}

export function AccessSection({ gs }: Props) {
  const intl = useOptionalIntl();
  const resourceTarget = useResourceTarget({ name: gs?.metadata.name ?? "", namespace: gs?.metadata.namespace });
  const resourceClient = useResourceClient(resourceTarget);
  const { Servers } = resourceClient;
  const qc = useQueryClient();
  const access = useResourceAccess();
  const [addInput, setAddInput] = useState("");
  const [error, setError] = useState<string | null>(null);

  const setCollab = useMutation({
    mutationFn: async (body: { userIds?: number[]; usernames?: string[] }) => {
      if (!gs) return;
      const namespace = gs.metadata.namespace ?? "gameplane-games";
      return Servers.setCollaborators(gs.metadata.name, namespace, body);
    },
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["fleet"] });
      setAddInput("");
      setError(null);
      return qc.invalidateQueries({ queryKey: resourceKey(resourceTarget, "server", gs?.metadata.name) });
    },
    onError: (err) => {
      setError(errMsg(err, intl.formatMessage({ id: "web.routes.tabs.settings.access.failed", defaultMessage: "failed", description: "Access: fallback error shown when changing collaborators fails and the server gives no message" }), intl));
    },
  });

  if (!gs) {
    return <div className="p-6 text-sm text-default-500">{intl.formatMessage({ id: "web.routes.tabs.settings.access.loading", defaultMessage: "Loading…", description: "Settings access section: loading text shown while the server access details are fetched" })}</div>;
  }

  const ann = gs.metadata.annotations ?? {};
  const ownerName = ann[OWNER_ANNOTATION] ?? intl.formatMessage({ id: "web.common.empty_value", defaultMessage: "—", description: "Shown in place of a value that is not set" });
  const collaboratorIDs = ann[COLLABORATORS_ANNOTATION]
    ? ann[COLLABORATORS_ANNOTATION].split(",").map((s) => s.trim())
    : [];
  const collaboratorNames = ann[COLLABORATOR_NAMES_ANNOTATION]
    ? ann[COLLABORATOR_NAMES_ANNOTATION].split(",").map((s) => s.trim())
    : [];

  // Collaborator management is restricted to the owner or target administrator.
  const canManage = access?.canDelete === true;

  // Misalignment guard: if the parsed collaborators and collaborator-names
  // have different lengths, the annotations were modified outside the dashboard.
  const isAligned = collaboratorIDs.length === collaboratorNames.length;
  const canEditCollaborators = canManage && isAligned;

  const handleAddCollaborator = () => {
    const name = addInput.trim();
    if (!name) return;

    // Keep existing IDs; send only the new username.
    // The server unions and dedupes.
    const newUserIds = collaboratorIDs.map((id) => Number(id));

    setCollab.mutate({
      userIds: newUserIds,
      usernames: [name],
    });
  };

  const handleRemoveCollaborator = (index: number) => {
    // Remove the collaborator at the given index
    const newUserIds = collaboratorIDs
      .filter((_, i) => i !== index)
      .map((id) => Number(id));

    setCollab.mutate({
      userIds: newUserIds,
    });
  };

  return (
    <div className="space-y-3">
      <Card className="border border-divider bg-default/40">
        <CardContent className="gap-0 px-4 py-4">
          <Label className="text-sm font-medium">{intl.formatMessage({ id: "web.routes.tabs.settings.access.owner", defaultMessage: "Owner", description: "Label for the server owner's username" })}</Label>
          <div className="pt-1 font-mono text-sm text-default-500">{ownerName}</div>
        </CardContent>
      </Card>

      <Card className="border border-divider bg-default/40">
        <CardContent className="gap-0 px-4 py-4">
          <div className="space-y-3">
            <div>
              <Label className="text-sm font-medium">{intl.formatMessage({ id: "web.routes.tabs.settings.access.collaborators", defaultMessage: "Collaborators", description: "Label for the list of users who can manage this server" })}</Label>
              <p className="pt-1 text-xs text-default-500">{intl.formatMessage({ id: "web.routes.tabs.settings.access.collaborators_get_full_control_of_this", defaultMessage: "Collaborators get full control of this server (console, files, settings). They can't transfer ownership or edit this list.", description: "Settings access section: hint under the Collaborators label saying collaborators get full control of the server but cannot transfer ownership or edit this list" })}</p>
            </div>

            <div className="space-y-2">
              {!isAligned && (
                <div className="rounded border border-warning/40 bg-warning/10 px-2 py-1 text-xs text-warning-600 dark:text-warning-400">{intl.formatMessage({ id: "web.routes.tabs.settings.access.collaborators_were_modified_outside_the", defaultMessage: "Collaborators were modified outside the dashboard.", description: "Settings access section: warning shown when the collaborators list was changed outside the dashboard" })}</div>
              )}
              {collaboratorNames.length > 0 ? (
                <div className="flex flex-wrap gap-2">
                  {collaboratorNames.map((name, idx) => (
                    <Chip
                      key={idx}
                      variant="soft"
                      color="default"
                      className="flex items-center gap-1"
                    >
                      <span>{name}</span>
                      {canEditCollaborators && (
                        <Button
                          isIconOnly
                          variant="ghost"
                          size="sm"
                          className="ml-0.5 text-danger"
                          onPress={() => handleRemoveCollaborator(idx)}
                          isDisabled={setCollab.isPending}
                          aria-label={intl.formatMessage({ id: "web.routes.tabs.settings.access.remove_collaborator", defaultMessage: "Remove {name}", description: "Access: aria-label of the remove button on a collaborator chip; {name} is the collaborator's username" }, { name })}
                        >
                          <X className="h-3 w-3" />
                        </Button>
                      )}
                    </Chip>
                  ))}
                </div>
              ) : (
                <div className="text-xs text-default-500">{intl.formatMessage({ id: "web.routes.tabs.settings.access.none_yet", defaultMessage: "None yet", description: "Settings access section: empty-state text in the collaborators area when there are no collaborators" })}</div>
              )}
            </div>

            {canEditCollaborators && (
              <div className="flex items-center gap-2 pt-1">
                <Input
                  value={addInput}
                  onChange={(e) => setAddInput(e.target.value)}
                  onKeyDown={(e) => {
                    if (e.key === "Enter") {
                      handleAddCollaborator();
                    }
                  }}
                  placeholder={intl.formatMessage({ id: "web.routes.tabs.settings.access.add_collaborator_by_username", defaultMessage: "Add collaborator by username…", description: "Settings access section: placeholder in the box for adding a collaborator by username; pressing Enter adds them" })}
                  className="flex-1"
                  disabled={setCollab.isPending}
                  type="text"
                />
                <Button
                  size="sm"
                  variant="primary"
                  onPress={() => handleAddCollaborator()}
                  isDisabled={!addInput.trim() || setCollab.isPending}
                >{intl.formatMessage({ id: "web.routes.tabs.settings.access.add", defaultMessage: "Add", description: "Button that adds the typed username as a collaborator" })}</Button>
              </div>
            )}
          </div>
        </CardContent>
      </Card>

      {error && (
        <div className="rounded border border-danger/40 bg-danger/10 px-3 py-2 text-xs text-danger">
          {error}
        </div>
      )}
    </div>
  );
}

function errMsg(err: unknown, fallback: string, intl: IntlShape): string {
  return errorTextIntl(intl, err, fallback);
}
