import { Button } from "@heroui/react";
import { Activity } from "lucide-react";
import { useOptionalIntl } from "@/lib/i18n/format";

export interface AuditIntegrityBannerProps {
  message: string;
  onDismiss?: () => void;
  onRefetch?: () => void;
  isRefetching?: boolean;
}

// Solid danger banner (design frame kIxaJ) — deliberately not HeroUI's Alert,
// which only offers a soft-fill treatment for status="danger" (see
// alert.styles.ts); this state calls for a solid fill with a dark icon chip,
// matching the design's "broken chain" severity.
export function AuditIntegrityBanner({
  message,
  onDismiss,
  onRefetch,
  isRefetching,
}: AuditIntegrityBannerProps) {
  const intl = useOptionalIntl();
  return (
    <div className="flex w-full items-center gap-3 bg-danger p-4" role="alert">
      <div className="flex h-8 w-8 shrink-0 items-center justify-center rounded-md bg-foreground p-1">
        <Activity className="h-5 w-5 text-danger-foreground" />
      </div>
      <span className="text-base font-medium leading-normal text-danger-foreground">
        {message}
      </span>
      <div className="flex items-center gap-2">
        {onRefetch ? (
          <Button
            size="sm"
            variant="ghost"
            onPress={onRefetch}
            isDisabled={isRefetching}
            className="text-danger-foreground hover:bg-black/10"
          >{intl.formatMessage({ id: "web.components.ui.audit_integrity_banner.re_check", defaultMessage: "Re-check", description: "Audit integrity banner: button that runs the audit integrity check again; keep it short" })}</Button>
        ) : null}
        {onDismiss ? (
          <button
            onClick={onDismiss}
            className="inline-flex shrink-0 items-center justify-center rounded px-2 py-1 text-danger-foreground hover:bg-black/10 focus:outline-none focus:ring-1 focus:ring-danger-foreground"
            aria-label={intl.formatMessage({ id: "web.components.ui.audit_integrity_banner.dismiss_audit_integrity_alert", defaultMessage: "Dismiss audit integrity alert", description: "Audit integrity banner: screen-reader name of the icon-only close button on the alert; not visible on screen" })}
          >
            <span className="text-xs font-bold">{intl.formatMessage({ id: "web.common.close_glyph", defaultMessage: "×", description: "Multiplication sign used as the close icon on dismiss buttons" })}</span>
          </button>
        ) : null}
      </div>
    </div>
  );
}
