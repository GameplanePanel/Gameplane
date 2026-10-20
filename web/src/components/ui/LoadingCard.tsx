// LoadingCard is a composed HeroUI Card + Spinner shown while async data loads.
// It renders a surface-colored card with a centered spinner and optional message.

import { Card, Spinner } from "@heroui/react";
import { useOptionalIntl } from "@/lib/i18n/format";

export interface LoadingCardProps {
  /** Optional message displayed below the spinner. Defaults to "Loading…" */
  message?: string;
  /** Optional CSS class name for additional styling */
  className?: string;
}

export function LoadingCard({
  message,
  className,
}: LoadingCardProps) {
  const intl = useOptionalIntl();
  return (
    <Card className={`flex items-center gap-3 ${className || ""}`}>
      <div className="flex items-center gap-3 p-5">
        <Spinner color="current" size="md" />
        <span className="text-sm text-muted">{message ?? intl.formatMessage({ id: "web.components.ui.loading_card.loading", defaultMessage: "Loading…", description: "Loading card: default message shown below the spinner when the caller gives none" })}</span>
      </div>
    </Card>
  );
}
