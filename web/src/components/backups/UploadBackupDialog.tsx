import { useRef, useState, type ReactNode } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
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
  Checkbox,
  Description,
  Spinner,
} from "@heroui/react";
import { AlertCircle, AlertTriangle, CheckCircle2, RotateCcw, Upload } from "lucide-react";

import { APIError, confirmRestore, uploadRestore } from "@/lib/api";
import { archiveFormatFromName } from "@/lib/backupArchives";
import { errorTextIntl } from "@/lib/errors";
import { useResourceClient, useResourceTarget, type ResourceTarget } from "@/lib/resourceTarget";
import { formatBytes } from "@/lib/utils";
import { useOptionalIntl } from "@/lib/i18n/format";
import type { ArchiveFormat, Restore } from "@/types";

// How often the restore is re-read while the operator checks, waits for the
// user, or copies the archive onto the server.
export const UPLOAD_POLL_MS = 2000;
// The operator fails an unconfirmed upload restore with this exact message.
const CONFIRMATION_TIMEOUT_MESSAGE = "confirmation timed out";

interface Props {
  serverName: string;
  target?: ResourceTarget;
  ns?: string;
  onClose: () => void;
}

// Where a rejected upload ends up: the design has a screen per cause.
type Rejection = "too_large" | "no_storage_class";

function rejectionOf(error: unknown): Rejection | null {
  if (!(error instanceof APIError)) return null;
  if (error.status === 413 || error.messageId === "server.restore.upload.too_large") return "too_large";
  if (error.messageId === "server.restore.upload.no_storage_class") return "no_storage_class";
  return null;
}

type UploadState =
  | { kind: "idle" }
  | { kind: "sending"; fileName: string; loaded: number; total: number }
  | { kind: "rejected"; reason: Rejection; fileSize: number }
  | { kind: "sent"; restore: string };

const ACTIVE_RESTORE_PHASES: ReadonlySet<string | undefined> = new Set([undefined, "Pending", "Receiving", "AwaitingConfirmation", "Suspending", "Running", "Resuming"]);

export function UploadBackupDialog({ serverName, target: explicitTarget, ns, onClose }: Props) {
  const intl = useOptionalIntl();
  const resourceTarget = useResourceTarget({ name: serverName, namespace: ns }, explicitTarget);
  const resourceClient = useResourceClient(resourceTarget);
  const qc = useQueryClient();
  const fileInput = useRef<HTMLInputElement>(null);
  const abort = useRef<AbortController | null>(null);
  const [file, setFile] = useState<File | null>(null);
  const [fileError, setFileError] = useState(false);
  const [plain, setPlain] = useState(false);
  const [template, setTemplate] = useState("");
  const [upload, setUpload] = useState<UploadState>({ kind: "idle" });
  const [sendError, setSendError] = useState<unknown>(null);
  const restoreName = upload.kind === "sent" ? upload.restore : null;

  const { data: templates } = useQuery({
    queryKey: ["resource", resourceTarget.cluster, "templates"],
    queryFn: ({ signal }) => resourceClient.withSignal(signal).Templates.list(resourceTarget.cluster),
  });
  // The target server's own game is the likely answer for a plain archive.
  const { data: server } = useQuery({
    queryKey: ["resource", resourceTarget.cluster, resourceTarget.namespace ?? "", serverName, "upload-target"],
    queryFn: ({ signal }) => resourceClient.withSignal(signal).Servers.get(serverName, resourceTarget.namespace, resourceTarget.cluster, signal),
    enabled: plain,
  });
  const serverTemplate = server?.spec.templateRef.name ?? "";
  const defaultTemplate = templates?.items.some((tpl) => tpl.metadata.name === serverTemplate) ? serverTemplate : "";
  const chosenTemplate = template || defaultTemplate;
  const templateLabel = (name: string | undefined): string =>
    (name && templates?.items.find((t) => t.metadata.name === name)?.spec.displayName) || name || "";

  const { data: restore, error: restoreError } = useQuery({
    queryKey: ["resource", resourceTarget.cluster, resourceTarget.namespace ?? "", "upload-restore", restoreName],
    queryFn: ({ signal }) => resourceClient.Restores.get(restoreName ?? "", resourceTarget.namespace, signal),
    enabled: restoreName !== null,
    refetchInterval: (query) => (ACTIVE_RESTORE_PHASES.has(query.state.data?.status?.phase) ? UPLOAD_POLL_MS : false),
  });

  const confirm = useMutation({
    mutationFn: (current: Restore) => confirmRestore(resourceTarget.cluster, resourceTarget.namespace, current.metadata.name, current.metadata.generation ?? 0),
    onSettled: () => qc.invalidateQueries({ queryKey: ["resource", resourceTarget.cluster, resourceTarget.namespace ?? "", "upload-restore", restoreName] }),
  });

  const chooseFile = (next: File | null) => {
    if (!next) return;
    setSendError(null);
    setFile(next);
    setFileError(archiveFormatFromName(next.name) === null);
  };

  const format: ArchiveFormat | null = file ? archiveFormatFromName(file.name) : null;
  const canStart = file !== null && format !== null && (!plain || chosenTemplate !== "");

  const start = async () => {
    if (!file || !format) return;
    const controller = new AbortController();
    abort.current = controller;
    setSendError(null);
    setUpload({ kind: "sending", fileName: file.name, loaded: 0, total: file.size });
    try {
      const result = await uploadRestore(
        resourceTarget.cluster,
        resourceTarget.namespace,
        serverName,
        file,
        { format, template: plain ? chosenTemplate : undefined, signal: controller.signal },
        (loaded, total) => setUpload({ kind: "sending", fileName: file.name, loaded, total }),
      );
      setUpload({ kind: "sent", restore: result.restore });
    } catch (error) {
      if (error instanceof DOMException && error.name === "AbortError") return;
      const reason = rejectionOf(error);
      if (reason) {
        setUpload({ kind: "rejected", reason, fileSize: file.size });
        return;
      }
      setSendError(error);
      setUpload({ kind: "idle" });
    } finally {
      abort.current = null;
    }
  };

  const close = () => {
    // Closing while the file is still going up cancels it; the API deletes the restore.
    abort.current?.abort();
    void qc.invalidateQueries({ queryKey: ["resource", resourceTarget.cluster, resourceTarget.namespace ?? "", "collection", "restores"] });
    onClose();
  };

  const stepTitle = (n: number, label: string) => intl.formatMessage({ id: "web.components.backups.upload_backup_dialog.step", defaultMessage: "Step {n} of 4 · {label}", description: "Upload dialog: subtitle naming the current step; {n} is the step number from 1 to 4 and {label} its name (File, Upload, Check or Review)" }, { n, label });
  const uploadTitle = intl.formatMessage({ id: "web.components.backups.upload_backup_dialog.title", defaultMessage: "Upload backup", description: "Upload dialog: title of the dialog for restoring a server from an uploaded archive file" });
  const closeLabel = intl.formatMessage({ id: "web.components.backups.upload_backup_dialog.close", defaultMessage: "Close", description: "Upload dialog: button that closes the dialog after an outcome; keep it short" });
  const cancelLabel = intl.formatMessage({ id: "web.components.backups.upload_backup_dialog.cancel", defaultMessage: "Cancel", description: "Upload dialog: button that closes the dialog without restoring; keep it short" });

  // What the dialog shows, derived from the upload state and the polled restore.
  let title = uploadTitle;
  let subtitle: ReactNode = stepTitle(1, intl.formatMessage({ id: "web.components.backups.upload_backup_dialog.step_file", defaultMessage: "File", description: "Upload dialog: name of step 1, choosing the archive file" }));
  let body: ReactNode;
  let footer: ReactNode;
  let dismissable = true;

  const outcome = (opts: { title: string; subtitle: string; heading: string; text?: string; ok?: boolean }): void => {
    title = opts.title;
    subtitle = opts.subtitle;
    body = (
      <div className="flex items-start gap-3">
        {opts.ok
          ? <CheckCircle2 className="mt-0.5 h-5 w-5 shrink-0 text-success" aria-hidden="true" />
          : <AlertCircle className="mt-0.5 h-5 w-5 shrink-0 text-danger" aria-hidden="true" />}
        <div role={opts.ok ? "status" : "alert"}>
          <p className="text-base font-medium">{opts.heading}</p>
          {opts.text && <p className="text-sm text-muted">{opts.text}</p>}
        </div>
      </div>
    );
    footer = <Button variant="primary" onPress={close}>{opts.ok ? intl.formatMessage({ id: "web.components.backups.upload_backup_dialog.done", defaultMessage: "Done", description: "Upload dialog: button that closes the dialog after the restore finished; keep it short" }) : closeLabel}</Button>;
  };

  const phase = restore?.status?.phase;
  const uploadStatus = restore?.status?.upload;
  const fileName = restore?.spec.upload?.fileName ?? "";
  const nothingChanged = intl.formatMessage({ id: "web.components.backups.upload_backup_dialog.nothing_changed", defaultMessage: "Nothing was changed", description: "Upload dialog: subtitle of an outcome screen saying the server was not touched" });

  if (upload.kind === "rejected") {
    if (upload.reason === "too_large") {
      outcome({
        title: intl.formatMessage({ id: "web.components.backups.upload_backup_dialog.not_started", defaultMessage: "Upload not started", description: "Upload dialog: title of an outcome screen shown when the upload was refused before it began" }),
        subtitle: intl.formatMessage({ id: "web.components.backups.upload_backup_dialog.nothing_uploaded", defaultMessage: "Nothing was uploaded", description: "Upload dialog: subtitle saying no data was uploaded" }),
        heading: intl.formatMessage({ id: "web.components.backups.upload_backup_dialog.too_large", defaultMessage: "File is larger than the upload limit", description: "Upload dialog: heading when the chosen file is bigger than the configured upload limit" }),
        text: intl.formatMessage({ id: "web.components.backups.upload_backup_dialog.too_large_hint", defaultMessage: "This file is {size}. Ask an administrator to raise backups.maxUploadSize in the Helm values, or use a smaller archive.", description: "Upload dialog: text under the too-large heading; {size} is the file size including its unit; backups.maxUploadSize is a Helm value name and stays as is" }, { size: formatBytes(upload.fileSize) }),
      });
    } else {
      outcome({
        title: intl.formatMessage({ id: "web.components.backups.upload_backup_dialog.not_started", defaultMessage: "Upload not started", description: "Upload dialog: title of an outcome screen shown when the upload was refused before it began" }),
        subtitle: nothingChanged,
        heading: intl.formatMessage({ id: "web.components.backups.upload_backup_dialog.no_storage_class", defaultMessage: "No staging storage class is set", description: "Upload dialog: heading when the cluster has no storage class for the temporary upload volume" }),
        text: intl.formatMessage({ id: "web.components.backups.upload_backup_dialog.no_storage_class_hint", defaultMessage: "Set backups.uploadStagingStorageClass in the Helm values, then try again.", description: "Upload dialog: text under the no-storage-class heading; backups.uploadStagingStorageClass is a Helm value name and stays as is" }),
      });
    }
  } else if (upload.kind === "sending") {
    const pct = upload.total > 0 ? Math.min(100, Math.floor((upload.loaded / upload.total) * 100)) : 0;
    subtitle = stepTitle(2, intl.formatMessage({ id: "web.components.backups.upload_backup_dialog.step_upload", defaultMessage: "Upload", description: "Upload dialog: name of step 2, sending the file" }));
    body = (
      <div className="space-y-3">
        <p className="font-mono text-sm">{upload.fileName}</p>
        <div role="progressbar" aria-valuemin={0} aria-valuemax={100} aria-valuenow={pct} aria-label={upload.fileName} className="h-2 w-full overflow-hidden rounded-full bg-surface">
          <div className="h-full bg-primary" style={{ width: `${pct}%` }} />
        </div>
        <p className="text-sm text-muted">{intl.formatMessage({ id: "web.components.backups.upload_backup_dialog.progress", defaultMessage: "{loaded} of {total} · {percent}%", description: "Upload dialog: upload progress; {loaded} and {total} are sizes with units and {percent} is a whole number" }, { loaded: formatBytes(upload.loaded), total: formatBytes(upload.total), percent: pct })}</p>
        <p className="text-sm text-muted">{intl.formatMessage({ id: "web.components.backups.upload_backup_dialog.keeps_running", defaultMessage: "The server keeps running while the file is uploaded and checked.", description: "Upload dialog: note during the upload that the server is not stopped" })}</p>
      </div>
    );
    footer = <Button variant="ghost" onPress={close}>{cancelLabel}</Button>;
  } else if (upload.kind === "sent") {
    const serverLabel = restore?.spec.serverRef.name ?? serverName;
    if (restoreError && !restore) {
      outcome({
        title: uploadTitle,
        subtitle: nothingChanged,
        heading: errorTextIntl(intl, restoreError, intl.formatMessage({ id: "web.components.backups.upload_backup_dialog.status_failed", defaultMessage: "The restore status could not be read.", description: "Upload dialog: fallback heading when the restore cannot be read after the upload" })),
      });
    } else if (phase === "Succeeded") {
      outcome({
        title: intl.formatMessage({ id: "web.components.backups.upload_backup_dialog.finished", defaultMessage: "Restore finished", description: "Upload dialog: title of the screen shown when the restore finished" }),
        subtitle: intl.formatMessage({ id: "web.components.backups.upload_backup_dialog.result", defaultMessage: "Result", description: "Upload dialog: subtitle of the finished screen" }),
        heading: intl.formatMessage({ id: "web.components.backups.upload_backup_dialog.restored", defaultMessage: "{server} restored", description: "Upload dialog: heading when the restore finished; {server} is the server name" }, { server: serverLabel }),
        text: intl.formatMessage({ id: "web.components.backups.upload_backup_dialog.restored_hint", defaultMessage: "Data was replaced from {file}. The server is running again.", description: "Upload dialog: text under the restored heading; {file} is the uploaded file name" }, { file: fileName }),
        ok: true,
      });
    } else if (phase === "Failed") {
      const message = restore?.status?.message ?? "";
      if (message === CONFIRMATION_TIMEOUT_MESSAGE) {
        outcome({
          title: intl.formatMessage({ id: "web.components.backups.upload_backup_dialog.not_restored", defaultMessage: "Restore not started", description: "Upload dialog: title of the screen shown when the restore never started" }),
          subtitle: intl.formatMessage({ id: "web.components.backups.upload_backup_dialog.timed_out", defaultMessage: "Confirmation timed out", description: "Upload dialog: subtitle when the user did not confirm in time" }),
          heading: intl.formatMessage({ id: "web.components.backups.upload_backup_dialog.timed_out", defaultMessage: "Confirmation timed out", description: "Upload dialog: subtitle when the user did not confirm in time" }),
          text: intl.formatMessage({ id: "web.components.backups.upload_backup_dialog.timed_out_hint", defaultMessage: "Nothing was restored. The partial upload was deleted and the server is running as before.", description: "Upload dialog: text under the timed-out heading" }),
        });
      } else if (restore?.spec.upload?.confirmed) {
        outcome({
          title: intl.formatMessage({ id: "web.components.backups.upload_backup_dialog.failed", defaultMessage: "Restore failed", description: "Upload dialog: title of the screen shown when the restore failed after it started" }),
          subtitle: intl.formatMessage({ id: "web.components.backups.upload_backup_dialog.failed_subtitle", defaultMessage: "Data may be partly replaced", description: "Upload dialog: subtitle saying the server's data may have been partly overwritten" }),
          heading: message || intl.formatMessage({ id: "web.components.backups.upload_backup_dialog.failed_heading", defaultMessage: "The restore failed", description: "Upload dialog: fallback heading when a restore failed without a reason" }),
          text: intl.formatMessage({ id: "web.components.backups.upload_backup_dialog.failed_hint", defaultMessage: "The server's data may be partly replaced and the server stays stopped.", description: "Upload dialog: text under the failed heading, after the copy onto the server began" }),
        });
      } else {
        outcome({
          title: intl.formatMessage({ id: "web.components.backups.upload_backup_dialog.refused", defaultMessage: "Upload refused", description: "Upload dialog: title of the screen shown when the archive failed its checks" }),
          subtitle: nothingChanged,
          heading: intl.formatMessage({ id: "web.components.backups.upload_backup_dialog.refused_heading", defaultMessage: "Refused: {reason}", description: "Upload dialog: heading with the reason the archive was refused; {reason} is the checker's message" }, { reason: message || intl.formatMessage({ id: "web.components.backups.upload_backup_dialog.refused_unknown", defaultMessage: "the archive failed its checks", description: "Upload dialog: fallback reason when the checker gave none" }) }),
          text: intl.formatMessage({ id: "web.components.backups.upload_backup_dialog.refused_hint", defaultMessage: "Nothing was written to {server} and the server was not stopped.", description: "Upload dialog: text under the refused heading; {server} is the server name" }, { server: serverLabel }),
        });
      }
    } else if (phase === "AwaitingConfirmation") {
      subtitle = stepTitle(4, intl.formatMessage({ id: "web.components.backups.upload_backup_dialog.step_review", defaultMessage: "Review", description: "Upload dialog: name of step 4, reviewing the checked archive" }));
      const manifest = uploadStatus?.manifest;
      const isPlain = uploadStatus?.kind === "plain";
      const rows: Array<[string, string]> = isPlain
        ? [
            [intl.formatMessage({ id: "web.components.backups.upload_backup_dialog.source", defaultMessage: "Source", description: "Upload dialog: review row label for where the archive came from" }), intl.formatMessage({ id: "web.components.backups.upload_backup_dialog.source_plain", defaultMessage: "Plain archive, no Gameplane manifest", description: "Upload dialog: review value for an archive without a Gameplane manifest" })],
            [intl.formatMessage({ id: "web.components.backups.upload_backup_dialog.game", defaultMessage: "Game", description: "Upload dialog: review row label for the game the archive is for" }), templateLabel(restore?.spec.upload?.confirmedTemplate)],
            [intl.formatMessage({ id: "web.components.backups.upload_backup_dialog.restores_onto", defaultMessage: "Restores onto", description: "Upload dialog: review row label for where the archive is written" }), intl.formatMessage({ id: "web.components.backups.upload_backup_dialog.primary_volume", defaultMessage: "Primary data volume", description: "Upload dialog: review value, the server's main data volume" })],
          ]
        : [
            [intl.formatMessage({ id: "web.components.backups.upload_backup_dialog.source_server", defaultMessage: "Source server", description: "Upload dialog: review row label for the server the archive was taken from" }), manifest?.serverName ?? ""],
            [intl.formatMessage({ id: "web.components.backups.upload_backup_dialog.game", defaultMessage: "Game", description: "Upload dialog: review row label for the game the archive is for" }), templateLabel(manifest?.templateName)],
            [intl.formatMessage({ id: "web.components.backups.upload_backup_dialog.game_version", defaultMessage: "Game version", description: "Upload dialog: review row label for the game version the archive was captured with" }), manifest?.gameVersion ?? ""],
            [intl.formatMessage({ id: "web.components.backups.upload_backup_dialog.captured_at", defaultMessage: "Captured at", description: "Upload dialog: review row label for when the archive was captured" }), manifest?.capturedAt ? `${manifest.capturedAt.slice(0, 16).replace("T", " ")} UTC` : ""],
            [intl.formatMessage({ id: "web.components.backups.upload_backup_dialog.volumes", defaultMessage: "Volumes", description: "Upload dialog: review row label for the volumes inside the archive" }), (manifest?.volumes ?? []).join(", ")],
          ];
      body = (
        <div className="space-y-4">
          <dl className="grid grid-cols-[10rem_1fr] gap-y-2 text-sm">
            {rows.map(([label, value]) => (
              <div key={label} className="contents">
                <dt className="text-muted">{label}</dt>
                <dd>{value}</dd>
              </div>
            ))}
          </dl>
          {isPlain && (
            <Note tone="warning" heading={intl.formatMessage({ id: "web.components.backups.upload_backup_dialog.cannot_check", defaultMessage: "Gameplane cannot check the contents", description: "Upload dialog: warning heading for a plain archive" })}>
              {intl.formatMessage({ id: "web.components.backups.upload_backup_dialog.cannot_check_hint", defaultMessage: "Only the safety checks ran. The archive is written onto the primary data volume as-is.", description: "Upload dialog: warning text for a plain archive" })}
            </Note>
          )}
          {(uploadStatus?.warnings ?? []).map((warning) => (
            <Note key={warning} tone="warning">{warning}</Note>
          ))}
          <Note tone="danger" heading={intl.formatMessage({ id: "web.components.backups.upload_backup_dialog.replaces", defaultMessage: "This replaces all data on {server}", description: "Upload dialog: danger heading before confirming; {server} is the server name" }, { server: serverLabel })}>
            <span className="block">{intl.formatMessage({ id: "web.components.backups.upload_backup_dialog.replaces_stop", defaultMessage: "The server stops during the restore.", description: "Upload dialog: first line of the danger note" })}</span>
            <span className="block">{intl.formatMessage({ id: "web.components.backups.upload_backup_dialog.replaces_partial", defaultMessage: "If the copy fails partway, data may be partly replaced and the server stays stopped.", description: "Upload dialog: second line of the danger note; the risk of a failed copy" })}</span>
          </Note>
          {confirm.isError && (
            <p role="alert" className="text-sm text-danger">{errorTextIntl(intl, confirm.error, intl.formatMessage({ id: "web.components.backups.upload_backup_dialog.confirm_failed", defaultMessage: "The restore could not be confirmed.", description: "Upload dialog: fallback error when confirming fails" }))}</p>
          )}
        </div>
      );
      footer = (
        <>
          <Button variant="ghost" onPress={close} isDisabled={confirm.isPending}>{cancelLabel}</Button>
          <Button variant="danger" isDisabled={confirm.isPending || confirm.isSuccess} onPress={() => confirm.mutate(restore as Restore)}>
            <RotateCcw className="h-4 w-4" aria-hidden="true" />
            {intl.formatMessage({ id: "web.components.backups.upload_backup_dialog.confirm", defaultMessage: "Confirm restore", description: "Upload dialog: button that confirms and starts the restore; keep it short" })}
          </Button>
        </>
      );
    } else if (phase === "Suspending" || phase === "Running" || phase === "Resuming") {
      dismissable = false;
      title = intl.formatMessage({ id: "web.components.backups.upload_backup_dialog.restoring", defaultMessage: "Restoring {server}", description: "Upload dialog: title while the restore runs; {server} is the server name" }, { server: serverLabel });
      subtitle = intl.formatMessage({ id: "web.components.backups.upload_backup_dialog.restore_running", defaultMessage: "Restore running", description: "Upload dialog: subtitle while the restore runs" });
      body = (
        <div className="flex items-center gap-3">
          <Spinner size="md" />
          <p className="text-sm">{intl.formatMessage({ id: "web.components.backups.upload_backup_dialog.restoring_hint", defaultMessage: "The server is stopped. Keep this window open until the restore finishes.", description: "Upload dialog: text while the restore runs" })}</p>
        </div>
      );
      footer = null;
    } else {
      // Pending or Receiving, or the first read has not arrived yet.
      subtitle = stepTitle(3, intl.formatMessage({ id: "web.components.backups.upload_backup_dialog.step_check", defaultMessage: "Check", description: "Upload dialog: name of step 3, checking the archive" }));
      body = (
        <div className="flex items-start gap-3">
          <Spinner size="md" />
          <div>
            <p className="text-base font-medium">{intl.formatMessage({ id: "web.components.backups.upload_backup_dialog.checking", defaultMessage: "Checking archive…", description: "Upload dialog: heading while the archive is checked" })}</p>
            <p className="text-sm text-muted">{intl.formatMessage({ id: "web.components.backups.upload_backup_dialog.checking_hint", defaultMessage: "Checks format, completeness, paths, links, device files and size. Nothing is written to the server.", description: "Upload dialog: text while the archive is checked" })}</p>
          </div>
        </div>
      );
      footer = <Button variant="ghost" onPress={close}>{cancelLabel}</Button>;
    }
  } else {
    body = (
      <div className="space-y-4">
        <input
          ref={fileInput}
          type="file"
          accept=".tar.gz,.tgz,.zip,application/gzip,application/zip"
          className="hidden"
          data-testid="backup-archive-input"
          onChange={(e) => chooseFile(e.target.files?.[0] ?? null)}
        />
        <Button
          variant="ghost"
          className="h-auto w-full flex-col gap-2 rounded-lg border border-dashed border-border bg-surface/40 px-4 py-6 text-sm hover:border-primary"
          onPress={() => fileInput.current?.click()}
        >
          <Upload className="h-5 w-5" aria-hidden="true" />
          <span className="font-medium">{file ? file.name : intl.formatMessage({ id: "web.components.backups.upload_backup_dialog.choose", defaultMessage: "Choose a .tar.gz or .zip file", description: "Upload dialog: file picker label; keep the .tar.gz and .zip extensions as is" })}</span>
          <span className="text-xs text-muted">{intl.formatMessage({ id: "web.components.backups.upload_backup_dialog.choose_hint", defaultMessage: "Gameplane backups or plain data-folder archives.", description: "Upload dialog: hint under the file picker" })}</span>
        </Button>
        {fileError && <p role="alert" className="text-sm text-danger">{intl.formatMessage({ id: "web.components.backups.upload_backup_dialog.bad_file", defaultMessage: "Choose a .tar.gz or .zip file.", description: "Upload dialog: error when the chosen file is not a .tar.gz or .zip archive; keep the extensions as is" })}</p>}
        <Checkbox isSelected={plain} onChange={setPlain} className="flex-row items-center gap-2">
          <Checkbox.Control>
            <Checkbox.Indicator />
          </Checkbox.Control>
          <Checkbox.Content className="text-sm font-normal">{intl.formatMessage({ id: "web.components.backups.upload_backup_dialog.not_gameplane", defaultMessage: "This is not a Gameplane backup", description: "Upload dialog: checkbox for an archive made outside Gameplane, which has no manifest" })}</Checkbox.Content>
        </Checkbox>
        {plain && (
          <>
            <div className="flex flex-col gap-1.5">
              <label htmlFor="upload-game" className="text-xs text-muted">{intl.formatMessage({ id: "web.components.backups.upload_backup_dialog.game", defaultMessage: "Game", description: "Upload dialog: review row label for the game the archive is for" })}</label>
              <select
                id="upload-game"
                value={chosenTemplate}
                onChange={(e) => setTemplate(e.target.value)}
                className="rounded-lg border border-[var(--field-border)] bg-[var(--field-background)] px-3 py-2 text-sm"
              >
                <option value="">{intl.formatMessage({ id: "web.components.backups.upload_backup_dialog.select_game", defaultMessage: "Select a game…", description: "Upload dialog: placeholder of the game dropdown" })}</option>
                {(templates?.items ?? []).map((tpl) => (
                  <option key={tpl.metadata.name} value={tpl.metadata.name}>{tpl.spec.displayName || tpl.metadata.name}</option>
                ))}
              </select>
            </div>
            <Note tone="warning" heading={intl.formatMessage({ id: "web.components.backups.upload_backup_dialog.cannot_check_archive", defaultMessage: "Gameplane cannot check this archive", description: "Upload dialog: warning heading for a plain archive before upload" })}>
              {intl.formatMessage({ id: "web.components.backups.upload_backup_dialog.cannot_check_archive_hint", defaultMessage: "Only the safety checks run: paths, links, device files and size.", description: "Upload dialog: warning text for a plain archive before upload" })}
            </Note>
          </>
        )}
        {sendError !== null && <p role="alert" className="text-sm text-danger">{errorTextIntl(intl, sendError, intl.formatMessage({ id: "web.components.backups.upload_backup_dialog.upload_failed", defaultMessage: "The upload failed. Try again.", description: "Upload dialog: fallback error when the upload fails" }))}</p>}
      </div>
    );
    footer = (
      <>
        <Button variant="ghost" onPress={close}>{cancelLabel}</Button>
        <Button variant="primary" isDisabled={!canStart} onPress={() => void start()}>{intl.formatMessage({ id: "web.components.backups.upload_backup_dialog.next", defaultMessage: "Next", description: "Upload dialog: button that uploads the chosen file and moves to the next step; keep it short" })}</Button>
      </>
    );
  }

  return (
    <Modal isOpen onOpenChange={(o) => !o && dismissable && close()}>
      <ModalBackdrop isDismissable={dismissable} isKeyboardDismissDisabled={!dismissable}>
        <ModalContainer>
          <ModalDialog className="w-[760px] max-w-[calc(100vw-2rem)]">
            <ModalHeader>
              <ModalHeading>{title}</ModalHeading>
              <Description className="text-sm text-muted">{subtitle}</Description>
            </ModalHeader>
            <ModalBody className="gap-4">{body}</ModalBody>
            {footer && <ModalFooter className="flex items-center justify-between gap-2">{footer}</ModalFooter>}
          </ModalDialog>
        </ModalContainer>
      </ModalBackdrop>
    </Modal>
  );
}

// Note is a tinted callout: warning for things to know, danger for the
// overwrite risk the user must accept before the restore starts.
function Note({ tone, heading, children }: { tone: "warning" | "danger"; heading?: string; children: ReactNode }) {
  const palette = tone === "danger" ? "border-danger/40 bg-danger/10" : "border-warning/40 bg-warning/10";
  const color = tone === "danger" ? "text-danger" : "text-warning";
  const Icon = tone === "danger" ? AlertCircle : AlertTriangle;
  return (
    <div role="note" className={`flex items-start gap-3 rounded-xl border px-4 py-3 ${palette}`}>
      <Icon className={`mt-0.5 h-4 w-4 shrink-0 ${color}`} aria-hidden="true" />
      <div className="text-sm">
        {heading && <p className={`font-medium ${color}`}>{heading}</p>}
        {children}
      </div>
    </div>
  );
}
