import { TriangleAlert } from "lucide-react";
import { useOptionalIntl } from "@/lib/i18n/format";

export interface SafeModeBannerProps {
  /** Navigates to the theme settings page (/settings/theme). */
  onOpenSettings: () => void;
  /** Hides the banner. Safe mode itself stays active for the session. */
  onDismiss: () => void;
}

/**
 * Safe-mode banner (contracts/theme-ui.md §4, design frame DAz77). Shown
 * while safe mode is active — via `?safe-mode=1`, the Ctrl+Shift+Alt+T
 * shortcut, or the login-page safe-mode link. Only the custom CSS overlay is
 * suspended; the stored stylesheet is kept, and "Open Appearance Settings"
 * leads to the theme settings page where it can be edited or cleared.
 */
export function SafeModeBanner({ onOpenSettings, onDismiss }: SafeModeBannerProps) {
  const intl = useOptionalIntl();
  return (
    <div
      role="alert"
      className="flex w-full items-center gap-3 rounded-lg border border-warning-soft-foreground bg-warning-soft p-4"
    >
      <TriangleAlert aria-hidden="true" className="h-5 w-5 shrink-0 text-warning-soft-foreground" />
      <div className="flex min-w-0 flex-1 flex-col gap-2">
        <p className="text-sm font-medium text-warning-soft-foreground">{intl.formatMessage({ id: "web.components.ui.safe_mode_banner.safe_mode_active_custom_css_is_suspended", defaultMessage: "Safe Mode Active: Custom CSS is suspended.", description: "Safe mode banner: warning saying Safe Mode is active and custom CSS is suspended; CSS is a technical term, usually left untranslated" })}</p>
        <div className="flex items-center gap-2.5 text-sm">
          <button
            type="button"
            onClick={onOpenSettings}
            className="font-medium text-warning-soft-foreground underline-offset-2 hover:underline"
          >{intl.formatMessage({ id: "web.components.ui.safe_mode_banner.open_appearance_settings", defaultMessage: "Open Appearance Settings", description: "Safe mode banner: link-style button that opens the Appearance settings; keep it short" })}</button>
          <span aria-hidden="true" className="select-none text-warning-soft-foreground/60">
            {intl.formatMessage({ id: "web.common.separator_bar", defaultMessage: "|", description: "Vertical bar that separates inline action links" })}
          </span>
          <button
            type="button"
            onClick={onDismiss}
            className="font-medium text-warning-soft-foreground underline-offset-2 hover:underline"
          >{intl.formatMessage({ id: "web.components.ui.safe_mode_banner.dismiss", defaultMessage: "Dismiss", description: "Safe mode banner: link-style button that hides the Safe Mode banner; keep it short" })}</button>
        </div>
      </div>
    </div>
  );
}
