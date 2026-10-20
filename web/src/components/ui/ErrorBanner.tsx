import { Alert } from "@heroui/react";
import { AlertCircle } from "lucide-react";
import { APIError } from "@/lib/api";
import { useOptionalIntl } from "@/lib/i18n/format";

export interface ErrorBannerProps {
  err: unknown;
  onDismiss?: () => void;
}

export function ErrorBanner({ err, onDismiss }: ErrorBannerProps) {
  const intl = useOptionalIntl();
  const msg =
    err instanceof APIError
      ? err.isHTML ? err.message : err.body || err.message
      : String(err);

  return (
    <Alert status="danger" className="flex items-center gap-3">
      <Alert.Indicator>
        <AlertCircle className="h-5 w-5 shrink-0 text-danger" />
      </Alert.Indicator>
      <Alert.Content className="flex flex-1 items-center justify-between gap-3">
        <span className="text-sm">{msg}</span>
        {onDismiss ? (
          <button
            onClick={onDismiss}
            className="ml-2 inline-flex shrink-0 items-center justify-center rounded px-2 py-1 hover:bg-danger/20 focus:outline-none focus:ring-1 focus:ring-danger"
            aria-label={intl.formatMessage({ id: "web.components.ui.error_banner.dismiss_error", defaultMessage: "Dismiss error", description: "Error banner: screen-reader name of the icon-only close button on an error message; not visible on screen" })}
          >
            <span className="text-xs font-bold text-danger">{intl.formatMessage({ id: "web.common.close_glyph", defaultMessage: "×", description: "Multiplication sign used as the close icon on dismiss buttons" })}</span>
          </button>
        ) : null}
      </Alert.Content>
    </Alert>
  );
}
