import type { IntlShape } from "react-intl";

import { Button } from "@heroui/react";

import { serverMessageText } from "@/lib/i18n/serverMessage";
import { formatBytes } from "@/lib/utils";

import { classifyUploadError, uploadPercent, type UploadItem } from "./uploadQueue";

export function UploadTray({ items, intl, onCancel, onCancelAll, onRetry }: {
  items: UploadItem[];
  intl: IntlShape;
  onCancel: (id: number) => void;
  onCancelAll: () => void;
  onRetry: (id: number) => void;
}) {
  const active = items.some((item) => item.status === "waiting" || item.status === "uploading");
  const failed = items.filter((item) => item.status === "failed").length;
  const title = !active && failed > 0
    ? intl.formatMessage({ id: "web.routes.tabs.files.uploads_failed", defaultMessage: "Uploads · {count} failed", description: "Upload tray title when every remaining upload has failed; {count} is the number of failed files" }, { count: failed })
    : intl.formatMessage({ id: "web.routes.tabs.files.uploads_title", defaultMessage: "Uploads · {count, plural, one {# file} other {# files}}", description: "Upload tray title with the number of files in the batch" }, { count: items.length });

  return (
    <section
      aria-label={intl.formatMessage({ id: "web.routes.tabs.files.upload_tray", defaultMessage: "Upload progress", description: "Accessible name of the bottom-right tray that lists file uploads and their progress" })}
      className="fixed bottom-4 right-4 z-40 flex w-96 max-w-[calc(100vw-2rem)] flex-col gap-3 rounded-large border border-divider bg-content1 p-4 shadow-large"
    >
      <header className="flex items-center justify-between gap-3">
        <h2 className="text-sm font-medium">{title}</h2>
        {active && (
          <Button size="sm" variant="ghost" onPress={onCancelAll}>
            {intl.formatMessage({ id: "web.routes.tabs.files.cancel_all", defaultMessage: "Cancel all", description: "Upload tray: cancels every waiting and running upload" })}
          </Button>
        )}
      </header>
      <ul className="flex flex-col gap-3">
        {items.map((item) => (
          <UploadRow key={item.id} item={item} intl={intl} onCancel={onCancel} onRetry={onRetry} />
        ))}
      </ul>
      <p className="text-xs text-default-500">
        {intl.formatMessage({ id: "web.routes.tabs.files.upload_note", defaultMessage: "Uploads go straight to the server volume. Cancelling removes the part written so far, and the old file stays.", description: "Upload tray footnote explaining that uploads write directly to the volume and what cancelling does" })}
      </p>
    </section>
  );
}

function UploadRow({ item, intl, onCancel, onRetry }: {
  item: UploadItem;
  intl: IntlShape;
  onCancel: (id: number) => void;
  onRetry: (id: number) => void;
}) {
  const total = item.file.size;
  const percent = uploadPercent(item.loaded, total);
  const showBar = item.status === "uploading" || item.status === "failed";
  const cancellable = item.status === "waiting" || item.status === "uploading";
  return (
    <li className="flex flex-col gap-1 text-sm">
      <div className="flex items-center justify-between gap-2">
        <span className="truncate" title={item.file.name}>{item.file.name}</span>
        <span className="flex shrink-0 items-center gap-2 text-xs text-default-500">
          {item.status === "uploading" && (
            <span>{intl.formatMessage({ id: "web.routes.tabs.files.upload_progress", defaultMessage: "{loaded} of {total} · {percent}%", description: "Upload tray row while a file uploads: bytes sent, total size and whole percent" }, { loaded: formatBytes(item.loaded), total: formatBytes(total), percent })}</span>
          )}
          {item.status === "waiting" && intl.formatMessage({ id: "web.routes.tabs.files.upload_waiting", defaultMessage: "Waiting", description: "Upload tray row for a file queued behind the one that is uploading" })}
          {item.status === "done" && intl.formatMessage({ id: "web.routes.tabs.files.upload_done", defaultMessage: "Done", description: "Upload tray row for a file that finished uploading" })}
          {item.status === "cancelled" && intl.formatMessage({ id: "web.routes.tabs.files.upload_cancelled", defaultMessage: "Cancelled", description: "Upload tray row for a file the user cancelled" })}
          {item.status === "failed" && intl.formatMessage({ id: "web.routes.tabs.files.upload_failed_at", defaultMessage: "Failed at {percent}%", description: "Upload tray row for a failed file: whole percent sent before it failed" }, { percent })}
          {cancellable && (
            <Button size="sm" variant="ghost" onPress={() => onCancel(item.id)}>
              {intl.formatMessage({ id: "web.routes.tabs.files.upload_cancel", defaultMessage: "Cancel", description: "Upload tray: cancels one upload" })}
            </Button>
          )}
          {item.status === "failed" && (
            <Button size="sm" variant="ghost" onPress={() => onRetry(item.id)}>
              {intl.formatMessage({ id: "web.routes.tabs.files.retry", defaultMessage: "Retry", description: "Retry a failed action such as an upload or a files-starting request; keep it short" })}
            </Button>
          )}
        </span>
      </div>
      {showBar && (
        <div className="h-1 w-full overflow-hidden rounded-full bg-default-200" role="progressbar" aria-valuemin={0} aria-valuemax={100} aria-valuenow={percent}>
          <div className={item.status === "failed" ? "h-full bg-danger" : "h-full bg-primary"} style={{ width: `${percent}%` }} />
        </div>
      )}
      {item.status === "failed" && (
        <p role="alert" className="rounded-medium border border-danger/40 px-3 py-2 text-xs text-danger">{uploadErrorText(intl, item)}</p>
      )}
    </li>
  );
}

export function uploadErrorText(intl: IntlShape, item: UploadItem): string {
  switch (classifyUploadError(item.error)) {
    case "connection":
      return intl.formatMessage({ id: "web.routes.tabs.files.upload_connection_lost", defaultMessage: "Connection to the server was lost. The old file is unchanged. Retry to upload again.", description: "Upload error: the connection dropped mid-upload; the previous file is kept and the user can retry" });
    case "too_large":
      return intl.formatMessage({ id: "web.routes.tabs.files.upload_too_large", defaultMessage: "Not uploaded: {name} is over the upload size limit (files.maxUploadSize).", description: "Upload error: the file is larger than the server's upload limit; {name} is the file name" }, { name: item.file.name });
    case "no_space":
      return intl.formatMessage({ id: "web.routes.tabs.files.upload_no_space", defaultMessage: "Not uploaded: {name} does not fit in the free space on the server's volume. Nothing was written.", description: "Upload error: the volume has too little free space for the file; {name} is the file name" }, { name: item.file.name });
    default:
      return serverMessageText(intl, item.error)
        ?? intl.formatMessage({ id: "web.routes.tabs.files.upload_failed", defaultMessage: "Upload failed.", description: "Upload error when the cause is not known" });
  }
}
