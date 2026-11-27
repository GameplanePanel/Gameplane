// One preview image of a catalog theme (light or dark). The image comes from
// the API preview endpoint and is sent with the session cookie, so no fetch is
// needed. A theme without the requested mode, or an image that fails to load,
// shows the dashed placeholder from the design ("Dark only" / "Light only").
import { useState } from "react";
import { Moon } from "lucide-react";
import { Themes } from "@/lib/endpoints";
import type { ThemeMode } from "@/types";
import { useOptionalIntl } from "@/lib/i18n/format";
import { cn } from "@/lib/utils";
import { themeCardMessages } from "@/components/themes/messages";

export interface ThemePreviewPanelProps {
  themeId: number;
  mode: ThemeMode;
  /** The modes the theme ships; a single-mode theme names its only mode in the placeholder. */
  modes: readonly ThemeMode[];
  className?: string;
}

export function ThemePreviewPanel({ themeId, mode, modes, className }: ThemePreviewPanelProps) {
  const intl = useOptionalIntl();
  // The failure belongs to one image URL, so a panel reused for another theme
  // or mode loads its own image.
  const src = Themes.previewUrl(themeId, mode);
  const [failedSrc, setFailedSrc] = useState<string | null>(null);
  const failed = failedSrc === src;
  const missing = !modes.includes(mode);

  if (missing || failed) {
    const onlyLabel =
      modes.length === 1
        ? intl.formatMessage(modes[0] === "dark" ? themeCardMessages.darkOnly : themeCardMessages.lightOnly)
        : null;
    return (
      <div
        className={cn(
          "flex aspect-[16/10] flex-col items-center justify-center gap-1 overflow-hidden rounded-md border border-dashed border-border bg-card/40 text-xs text-muted",
          className,
        )}
        data-testid="theme-preview-placeholder"
      >
        <Moon aria-hidden="true" className="h-5 w-5" />
        {onlyLabel ? <span>{onlyLabel}</span> : null}
      </div>
    );
  }

  return (
    <img
      src={src}
      alt=""
      loading="lazy"
      onError={() => setFailedSrc(src)}
      className={cn("aspect-[16/10] w-full rounded-md border border-border object-cover", className)}
      data-testid="theme-preview"
    />
  );
}
