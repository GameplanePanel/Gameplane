import { Sun, Moon, Monitor } from "lucide-react";
import { useId, type ReactNode } from "react";
import type { AppearanceMode } from "@/types";
import { useOptionalIntl } from "@/lib/i18n/format";

export type { AppearanceMode };

export interface AppearanceToggleProps {
  value: AppearanceMode;
  onChange: (mode: AppearanceMode) => void;
  /** D4: true while a custom-colors theme is active — mode then follows the surface color, not this toggle. */
  disabled?: boolean;
}

/** Three-state appearance toggle: light, dark, system. Controlled component — the caller owns persistence. */
export function AppearanceToggle({ value, onChange, disabled = false }: AppearanceToggleProps) {
  const intl = useOptionalIntl();
  const noteId = useId();
  const modes: Array<{ mode: AppearanceMode; icon: ReactNode; label: string }> = [
    {
      mode: "light",
      icon: <Sun className="h-4 w-4" />,
      label: intl.formatMessage({ id: "web.components.ui.appearance_toggle.light", defaultMessage: "Light", description: "Appearance toggle: label of the light mode button" }),
    },
    {
      mode: "dark",
      icon: <Moon className="h-4 w-4" />,
      label: intl.formatMessage({ id: "web.components.ui.appearance_toggle.dark", defaultMessage: "Dark", description: "Appearance toggle: label of the dark mode button" }),
    },
    {
      mode: "system",
      icon: <Monitor className="h-4 w-4" />,
      label: intl.formatMessage({ id: "web.components.ui.appearance_toggle.system", defaultMessage: "System", description: "Appearance toggle: label of the follow-system mode button" }),
    },
  ];

  return (
    <div
      role="group"
      aria-label={intl.formatMessage({ id: "web.components.ui.appearance_toggle.appearance", defaultMessage: "Appearance", description: "Appearance toggle: screen-reader name of the group of theme buttons for dark, light or system appearance; not visible on screen" })}
      aria-describedby={disabled ? noteId : undefined}
      className="flex items-center justify-center gap-1 rounded-lg bg-surface p-1"
    >
      {modes.map(({ mode, icon, label }) => (
        <button
          key={mode}
          type="button"
          aria-label={label}
          aria-pressed={value === mode}
          disabled={disabled}
          title={disabled ? intl.formatMessage({ id: "web.components.ui.appearance_toggle.set_by_your_surface_color", defaultMessage: "Set by your surface color", description: "Appearance toggle: tooltip and label shown while a custom theme sets the mode" }) : label}
          onClick={() => onChange(mode)}
          className={`
            flex h-8 w-8 items-center justify-center rounded-md
            transition-colors disabled:cursor-not-allowed disabled:opacity-50
            ${
              value === mode
                ? "bg-primary/20 text-primary"
                : "text-muted hover:bg-border/60 hover:text-fg"
            }
          `}
        >
          {icon}
        </button>
      ))}
      {disabled && (
        <span id={noteId} className="sr-only">{intl.formatMessage({ id: "web.components.ui.appearance_toggle.set_by_your_surface_color", defaultMessage: "Set by your surface color", description: "Appearance toggle: tooltip and label shown while a custom theme sets the mode" })}</span>
      )}
    </div>
  );
}
