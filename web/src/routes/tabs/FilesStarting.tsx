import type { IntlShape } from "react-intl";
import { Loader2 } from "lucide-react";

import { Button } from "@heroui/react";

import { serverMessageText } from "@/lib/i18n/serverMessage";

/** Files tab placeholder while a stopped or asleep server's file-only pod comes up (OD-4). */
export function FilesStarting({ state, intl, error, onRetry }: {
  state: "starting" | "timed_out";
  intl: IntlShape;
  error?: unknown;
  onRetry: () => void;
}) {
  if (state === "timed_out") {
    const detail = serverMessageText(intl, error)
      ?? intl.formatMessage({ id: "web.routes.tabs.files.starting_timed_out_detail", defaultMessage: "Files are starting, try again.", description: "Files tab: fallback text when the files pod did not become ready and the server gave no message" });
    return (
      <div role="alert" className="flex min-h-64 flex-1 flex-col items-center justify-center gap-4 px-4 text-center">
        <h2 className="text-base font-medium">
          {intl.formatMessage({ id: "web.routes.tabs.files.starting_timed_out_title", defaultMessage: "Files did not start", description: "Files tab: heading shown when the file-only pod was not ready in time" })}
        </h2>
        <p className="max-w-md rounded-large border border-danger/40 px-4 py-3 text-sm text-danger">{detail}</p>
        <Button variant="primary" onPress={onRetry}>
          {intl.formatMessage({ id: "web.routes.tabs.files.retry", defaultMessage: "Retry", description: "Retry a failed action such as an upload or a files-starting request; keep it short" })}
        </Button>
        <p className="text-sm text-default-500">
          {intl.formatMessage({ id: "web.routes.tabs.files.starting_timed_out_nothing", defaultMessage: "Nothing was started. The server stays stopped, and no file was changed.", description: "Files tab: reassurance after the files pod did not start; the server stays stopped and files are unchanged" })}
        </p>
      </div>
    );
  }
  return (
    <div role="status" className="flex min-h-64 flex-1 flex-col items-center justify-center gap-3 px-4 text-center">
      <div className="flex items-center gap-2">
        <Loader2 aria-hidden="true" className="h-5 w-5 animate-spin text-primary" />
        <h2 className="text-base font-medium">
          {intl.formatMessage({ id: "web.routes.tabs.files.starting_title", defaultMessage: "Files are starting", description: "Files tab: heading shown while the file-only pod for a stopped server starts" })}
        </h2>
      </div>
      <p className="max-w-lg text-sm text-default-500">
        {intl.formatMessage({ id: "web.routes.tabs.files.starting_body", defaultMessage: "The server is stopped or asleep. Its files open in a short-lived file-only pod so you can edit them. The game does not start, and players cannot reach the pod.", description: "Files tab: explains that a stopped server's files open in a file-only pod and the game does not start" })}
      </p>
      <div className="relative h-1 w-64 max-w-full overflow-hidden rounded-full bg-default-200" aria-hidden="true">
        <div className="absolute inset-y-0 w-2/5 animate-pulse rounded-full bg-primary" />
      </div>
      <p className="text-xs text-default-500">
        {intl.formatMessage({ id: "web.routes.tabs.files.starting_preparing", defaultMessage: "Preparing files…", description: "Files tab: caption under the indeterminate bar while the file-only pod starts" })}
      </p>
      <p className="text-xs text-default-500">
        {intl.formatMessage({ id: "web.routes.tabs.files.starting_hint", defaultMessage: "Usually ready in a few seconds. The list appears as soon as the pod is ready.", description: "Files tab: reassurance that the file list appears once the file-only pod is ready" })}
      </p>
    </div>
  );
}
