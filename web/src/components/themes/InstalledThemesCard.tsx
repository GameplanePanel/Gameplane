// Installed themes card on the Theme settings page (spec 043, T038, design
// 385085606-1). Lists the themes the signed-in user installed from the
// catalog, with Update and Remove actions. Rendered only when the user has
// at least one installed theme; the parent owns the mutations and dialogs.
import { Button, Card, Chip } from "@heroui/react";
import { Link } from "@tanstack/react-router";
import { Search } from "lucide-react";
import { useOptionalIntl } from "@/lib/i18n/format";
import type { InstalledTheme } from "@/types";
import { formatThemeMessage, installedThemesMessages as M } from "./messages";

export interface InstalledThemesCardProps {
  installed: InstalledTheme[];
  busy: boolean;
  onUpdate: (theme: InstalledTheme) => void;
  onRemove: (theme: InstalledTheme) => void;
}

export function InstalledThemesCard({ installed, busy, onUpdate, onRemove }: InstalledThemesCardProps) {
  const intl = useOptionalIntl();
  const updates = installed.filter((t) => t.updateAvailable && !t.sourceRemoved).length;
  const removed = installed.filter((t) => t.sourceRemoved).length;

  return (
    <Card className="space-y-4" data-testid="installed-themes-card">
      <Card.Header className="space-y-2 pb-2">
        <div className="font-medium text-base">{intl.formatMessage(M.title)}</div>
        <div className="text-xs text-muted">
          {intl.formatMessage({ id: "web.components.themes.installed_themes_card.subtitle", defaultMessage: "Themes installed for your account. Update or remove them here.", description: "Settings theme: subtitle under the installed themes heading" })}
        </div>
      </Card.Header>
      <Card.Content className="space-y-4">
        <div className="flex flex-wrap items-center justify-between gap-3">
          <div className="text-xs text-muted" data-testid="installed-themes-summary">
            {[
              formatThemeMessage(intl, M.installedCount, { count: String(installed.length) }),
              updates > 0 ? formatThemeMessage(intl, M.updateCount, { count: String(updates) }) : null,
              removed > 0 ? formatThemeMessage(intl, M.removedCount, { count: String(removed) }) : null,
            ]
              .filter((part): part is string => part !== null)
              .join(" · ")}
          </div>
          <Link to="/settings/theme/catalog" className="inline-flex">
            <Button variant="primary" size="sm" className="text-xs">
              <Search className="h-3.5 w-3.5" />
              {intl.formatMessage(M.browse)}
            </Button>
          </Link>
        </div>

        <ul className="space-y-2">
          {installed.map((theme) => (
            <li
              key={theme.id}
              className="flex flex-wrap items-center gap-3 rounded-lg border border-border px-4 py-3"
              data-testid="installed-theme-row"
            >
              <div className="min-w-0 flex-1 space-y-1">
                <div className="flex flex-wrap items-center gap-2">
                  <span className="text-sm font-medium">{theme.displayName || theme.name}</span>
                  {theme.sourceRemoved ? (
                    <Chip color="danger" size="sm" variant="soft" className="font-mono text-[10px] uppercase font-normal">
                      {intl.formatMessage({ id: "web.components.themes.installed_themes_card.badge_source_removed", defaultMessage: "Source removed", description: "Settings theme: badge on an installed theme whose source was removed" })}
                    </Chip>
                  ) : theme.updateAvailable ? (
                    <Chip color="warning" size="sm" variant="soft" className="font-mono text-[10px] uppercase font-normal">
                      {intl.formatMessage({ id: "web.components.themes.installed_themes_card.badge_update", defaultMessage: "Update available", description: "Settings theme: badge on an installed theme with a newer version" })}
                    </Chip>
                  ) : (
                    <Chip color="success" size="sm" variant="soft" className="font-mono text-[10px] uppercase font-normal">
                      {intl.formatMessage({ id: "web.components.themes.installed_themes_card.badge_installed", defaultMessage: "Installed", description: "Settings theme: badge on an installed theme that is up to date" })}
                    </Chip>
                  )}
                </div>
                <div className="font-mono text-[11px] text-muted">
                  {theme.sourceRemoved
                    ? formatThemeMessage(intl, M.sourceRemovedCaption, { version: String(theme.version) })
                    : formatThemeMessage(intl, M.versionModes, {
                        version: String(theme.version),
                        modes: theme.modes.map((m) => intl.formatMessage(m === "dark" ? M.modeDark : M.modeLight)).join(", "),
                      })}
                </div>
              </div>
              <div className="flex items-center gap-2">
                {theme.updateAvailable && !theme.sourceRemoved ? (
                  <Button
                    variant="primary"
                    size="sm"
                    className="text-xs"
                    isDisabled={busy}
                    onPress={() => onUpdate(theme)}
                  >
                    {intl.formatMessage({ id: "web.components.themes.installed_themes_card.update", defaultMessage: "Update", description: "Settings theme: button that updates an installed theme" })}
                  </Button>
                ) : null}
                <Button
                  variant="outline"
                  size="sm"
                  className="text-xs"
                  isDisabled={busy}
                  onPress={() => onRemove(theme)}
                >
                  {intl.formatMessage({ id: "web.components.themes.installed_themes_card.remove", defaultMessage: "Remove", description: "Settings theme: button that removes an installed theme" })}
                </Button>
              </div>
            </li>
          ))}
        </ul>
      </Card.Content>
    </Card>
  );
}
