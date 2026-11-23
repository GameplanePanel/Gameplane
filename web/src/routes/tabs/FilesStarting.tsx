import type { IntlShape } from "react-intl";

import { Button } from "@heroui/react";

/**
 * Files tab placeholder after the API answered 503 server.files.starting: the file-only pod of a stopped or
 * asleep server was not ready within the API's wait (OD-4). Shown only for that answer, never on a timer.
 */
export function FilesStarting({ intl, onRetry }: {
  intl: IntlShape;
  onRetry: () => void;
}) {
  return (
    <div role="alert" className="flex min-h-64 flex-1 flex-col items-center justify-center gap-4 px-4 text-center">
      <h2 className="text-base font-medium">
        {intl.formatMessage({ id: "web.routes.tabs.files.starting_timed_out_title", defaultMessage: "Files did not start", description: "Files tab: heading shown when the file-only pod was not ready in time" })}
      </h2>
      <p className="max-w-md rounded-large border border-danger/40 px-4 py-3 text-sm text-danger">
        {intl.formatMessage({ id: "web.routes.tabs.files.starting_timed_out_detail", defaultMessage: "Files did not start. Try again.", description: "Files tab: text under the heading when the file-only pod was not ready in time; the user can retry" })}
      </p>
      <Button variant="primary" onPress={onRetry}>
        {intl.formatMessage({ id: "web.routes.tabs.files.retry", defaultMessage: "Retry", description: "Retry a failed action such as an upload or a files-starting request; keep it short" })}
      </Button>
      <p className="text-sm text-default-500">
        {intl.formatMessage({ id: "web.routes.tabs.files.starting_timed_out_nothing", defaultMessage: "Nothing was started. The server stays stopped, and no file was changed.", description: "Files tab: reassurance after the files pod did not start; the server stays stopped and files are unchanged" })}
      </p>
    </div>
  );
}
