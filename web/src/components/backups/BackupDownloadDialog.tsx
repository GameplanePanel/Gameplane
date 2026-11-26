import { useEffect, useState } from "react";
import {
  Modal,
  ModalBackdrop,
  ModalContainer,
  ModalDialog,
  ModalHeader,
  ModalHeading,
  ModalBody,
  ModalFooter,
  Button,
  Description,
  Radio,
  RadioGroup,
  Spinner,
} from "@heroui/react";
import { AlertTriangle, Download } from "lucide-react";

import { backupDownloadURL } from "@/lib/api";
import { useResourceTarget, type ResourceTarget } from "@/lib/resourceTarget";
import { formatBytes, parseQuantityToBytes } from "@/lib/utils";
import { useOptionalIntl } from "@/lib/i18n/format";
import type { ArchiveFormat, Backup } from "@/types";

// The export worker can take about this long before bytes flow. The browser's
// own download manager owns the transfer after the click, so the dialog hands
// over and closes by itself once this window has passed.
export const DOWNLOAD_HANDOFF_MS = 30_000;

interface Props {
  backup: Backup | null;
  target?: ResourceTarget;
  ns?: string;
  onClose: () => void;
}

export function BackupDownloadDialog({ backup, target: explicitTarget, ns, onClose }: Props) {
  const intl = useOptionalIntl();
  const resourceTarget = useResourceTarget({ name: backup?.metadata.name ?? "", namespace: ns ?? backup?.metadata.namespace }, explicitTarget);
  const open = backup !== null;
  const [format, setFormat] = useState<ArchiveFormat>("tar.gz");
  const [preparing, setPreparing] = useState(false);
  // Reset the choice for each newly opened backup (adjusted during render,
  // the same pattern RestoreDialog uses).
  const [initializedFor, setInitializedFor] = useState<Backup | null>(null);
  if (backup !== initializedFor) {
    setInitializedFor(backup);
    setFormat("tar.gz");
    setPreparing(false);
  }

  useEffect(() => {
    if (!preparing) return;
    const timer = setTimeout(onClose, DOWNLOAD_HANDOFF_MS);
    return () => clearTimeout(timer);
  }, [preparing, onClose]);

  const startDownload = () => {
    if (!backup) return;
    const link = document.createElement("a");
    link.href = backupDownloadURL(resourceTarget.cluster, resourceTarget.namespace, backup.metadata.name, format);
    link.download = "";
    link.rel = "noopener";
    document.body.appendChild(link);
    link.click();
    link.remove();
    setPreparing(true);
  };

  const size = backup?.status?.size;
  const estimated = size ? formatBytes(parseQuantityToBytes(size)) : intl.formatMessage({ id: "web.common.empty_value", defaultMessage: "—", description: "Shown in place of a value that is not set" });
  const subtitle = backup ? `${backup.metadata.name} · ${backup.spec.serverRef.name}` : "";

  return (
    <Modal isOpen={open} onOpenChange={(o) => !o && onClose()}>
      <ModalBackdrop>
        <ModalContainer>
          <ModalDialog className="w-[760px] max-w-[calc(100vw-2rem)]">
            <ModalHeader>
              <ModalHeading>{intl.formatMessage({ id: "web.components.backups.backup_download_dialog.title", defaultMessage: "Download backup", description: "Download dialog: title of the dialog for downloading one backup as an archive file" })}</ModalHeading>
              <Description className="text-sm text-muted">{subtitle}</Description>
            </ModalHeader>
            {preparing ? (
              <ModalBody className="items-center gap-3 py-6 text-center">
                <Spinner size="lg" />
                <p className="text-base font-medium">{intl.formatMessage({ id: "web.components.backups.backup_download_dialog.preparing", defaultMessage: "Preparing download…", description: "Download dialog: heading shown after Download was pressed while the backup worker starts" })}</p>
                <p className="text-sm text-muted">{intl.formatMessage({ id: "web.components.backups.backup_download_dialog.preparing_hint", defaultMessage: "The backup worker can take up to about 30 seconds before bytes flow.", description: "Download dialog: explains why the download has not started yet" })}</p>
              </ModalBody>
            ) : (
              <ModalBody className="gap-4">
                <RadioGroup
                  value={format}
                  onChange={(v) => setFormat(v === "zip" ? "zip" : "tar.gz")}
                  aria-label={intl.formatMessage({ id: "web.components.backups.backup_download_dialog.format", defaultMessage: "Format", description: "Download dialog: label of the archive format choice (.tar.gz or .zip)" })}
                  className="space-y-2"
                >
                  <Radio value="tar.gz">
                    <Radio.Content className="flex w-full items-start gap-3">
                      <Radio.Control className="mt-0.5 shrink-0" />
                      <div>
                        <div className="text-sm font-medium">{intl.formatMessage({ id: "web.components.backups.backup_download_dialog.tar_gz", defaultMessage: ".tar.gz (default)", description: "Download dialog: option label for the .tar.gz archive format; keep the .tar.gz extension as is" })}</div>
                        <div className="text-xs text-muted">{intl.formatMessage({ id: "web.components.backups.backup_download_dialog.tar_gz_hint", defaultMessage: "Keeps file permissions", description: "Download dialog: hint under the .tar.gz option" })}</div>
                      </div>
                    </Radio.Content>
                  </Radio>
                  <Radio value="zip">
                    <Radio.Content className="flex w-full items-start gap-3">
                      <Radio.Control className="mt-0.5 shrink-0" />
                      <div>
                        <div className="text-sm font-medium">{intl.formatMessage({ id: "web.components.backups.backup_download_dialog.zip", defaultMessage: ".zip", description: "Download dialog: option label for the .zip archive format; keep the .zip extension as is" })}</div>
                        <div className="text-xs text-muted">{intl.formatMessage({ id: "web.components.backups.backup_download_dialog.zip_hint", defaultMessage: "Opens everywhere. Permissions reset on restore.", description: "Download dialog: hint under the .zip option; a zip archive loses file permissions when it is restored" })}</div>
                      </div>
                    </Radio.Content>
                  </Radio>
                </RadioGroup>
                <div className="flex items-center gap-6 text-sm">
                  <span className="text-muted">{intl.formatMessage({ id: "web.components.backups.backup_download_dialog.estimated_size", defaultMessage: "Estimated size", description: "Download dialog: label before the estimated size of the archive" })}</span>
                  <span className="font-mono">{estimated}</span>
                </div>
                <div role="note" className="flex items-start gap-3 rounded-xl border border-warning/40 bg-warning/10 px-4 py-3">
                  <AlertTriangle className="mt-0.5 h-4 w-4 shrink-0 text-warning" aria-hidden="true" />
                  <div>
                    <p className="text-sm font-medium text-warning">{intl.formatMessage({ id: "web.components.backups.backup_download_dialog.contains_everything", defaultMessage: "This archive contains every server file", description: "Download dialog: warning heading saying the archive holds all server files" })}</p>
                    <p className="text-sm">{intl.formatMessage({ id: "web.components.backups.backup_download_dialog.contains_everything_hint", defaultMessage: "It includes configs that may hold passwords. Keep it private.", description: "Download dialog: warning text under the heading; anyone who can view backups can download them" })}</p>
                  </div>
                </div>
              </ModalBody>
            )}
            <ModalFooter className="flex items-center justify-between gap-2">
              <Button variant="ghost" onPress={onClose}>{intl.formatMessage({ id: "web.components.backups.backup_download_dialog.cancel", defaultMessage: "Cancel", description: "Download dialog: button that closes the dialog; keep it short" })}</Button>
              {!preparing && (
                <Button variant="primary" onPress={startDownload}>
                  <Download className="h-4 w-4" aria-hidden="true" />
                  {intl.formatMessage({ id: "web.components.backups.backup_download_dialog.download", defaultMessage: "Download", description: "Download dialog: button that starts the download; keep it short" })}
                </Button>
              )}
            </ModalFooter>
          </ModalDialog>
        </ModalContainer>
      </ModalBackdrop>
    </Modal>
  );
}
