// Catalog grid tile for one theme (spec 043, T029). The primary action follows
// the state: Update when a newer version exists, Remove when installed, Install
// otherwise. Invalid themes show no Preview and a disabled Install.
import { Link } from "@tanstack/react-router";
import { Button, Card, CardContent, CardFooter, CardHeader, Chip } from "@heroui/react";
import { ShieldCheck, TriangleAlert } from "lucide-react";
import type { ThemeCatalogItem } from "@/types";
import { useOptionalIntl } from "@/lib/i18n/format";
import { formatWith } from "@/components/themes/formatWith";
import { cn } from "@/lib/utils";
import { themeCardMessages } from "@/components/themes/messages";
import { ThemePreviewPanel } from "@/components/themes/ThemePreviewPanel";

interface StatusBadge {
  color: "success" | "warning" | "danger";
  message: (typeof themeCardMessages)[keyof typeof themeCardMessages];
}

// One badge per theme, in priority order (the design shows a single chip).
function statusBadge(item: ThemeCatalogItem): StatusBadge | null {
  if (item.status === "invalid") return { color: "danger", message: themeCardMessages.badgeInvalid };
  if (item.updateAvailable) return { color: "warning", message: themeCardMessages.badgeUpdate };
  if (item.installed) return { color: "success", message: themeCardMessages.badgeInstalled };
  if (item.lowContrast) return { color: "warning", message: themeCardMessages.badgeLowContrast };
  if (item.status === "unverified") return { color: "warning", message: themeCardMessages.badgeUnverified };
  return null;
}

export function ThemeStatusBadge({ item }: { item: ThemeCatalogItem }) {
  const intl = useOptionalIntl();
  const badge = statusBadge(item);
  if (!badge) return null;
  return (
    <Chip
      color={badge.color}
      size="sm"
      variant="soft"
      className="font-mono text-[10px] font-normal uppercase"
      data-color={badge.color}
    >
      {intl.formatMessage(badge.message)}
    </Chip>
  );
}

export interface ThemeCardProps {
  item: ThemeCatalogItem;
  busy?: boolean;
  onPreview: (item: ThemeCatalogItem) => void;
  onInstall: (item: ThemeCatalogItem) => void;
  onUpdate: (item: ThemeCatalogItem) => void;
  onRemove: (item: ThemeCatalogItem) => void;
}

export function ThemeCard({ item, busy = false, onPreview, onInstall, onUpdate, onRemove }: ThemeCardProps) {
  const intl = useOptionalIntl();
  const invalid = item.status === "invalid";
  const unverified = item.status === "unverified";

  // One "v" per version: the update form names both versions itself.
  const metaText =
    item.updateAvailable && item.installed
      ? formatWith(intl, themeCardMessages.byAuthorUpdate, {
          author: item.author,
          from: item.installed.version,
          to: item.version,
        })
      : formatWith(intl, themeCardMessages.byAuthor, { author: item.author, version: item.version });

  const modeLabels =
    item.modes.length === 1
      ? [intl.formatMessage(item.modes[0] === "dark" ? themeCardMessages.darkOnly : themeCardMessages.lightOnly)]
      : item.modes.map((m) => intl.formatMessage(m === "dark" ? themeCardMessages.modeDark : themeCardMessages.modeLight));

  const SourceIcon = unverified ? TriangleAlert : ShieldCheck;
  const sourceTitle = intl.formatMessage(unverified ? themeCardMessages.unverifiedSource : themeCardMessages.verifiedSource);

  return (
    <Card className="flex flex-col border border-border" data-testid="theme-card">
      <CardHeader className="flex flex-col gap-3 pb-0">
        <div className="grid grid-cols-2 gap-3">
          <ThemePreviewPanel themeId={item.id} mode="light" modes={item.modes} />
          <ThemePreviewPanel themeId={item.id} mode="dark" modes={item.modes} />
        </div>
        <div className="flex items-start justify-between gap-3">
          <Link
            to="/settings/theme/catalog/$id"
            params={{ id: String(item.id) }}
            className="text-base font-semibold text-foreground hover:text-primary"
          >
            {item.displayName}
          </Link>
          <ThemeStatusBadge item={item} />
        </div>
        <p className="font-mono text-xs text-muted">
          {metaText}
        </p>
      </CardHeader>
      <CardContent className="flex flex-col gap-3">
        <p className="line-clamp-2 text-sm text-muted">{item.description}</p>
        <div className="flex flex-wrap gap-3 text-sm text-foreground">
          {modeLabels.map((label) => (
            <span key={label}>{label}</span>
          ))}
        </div>
        <span
          className={cn("inline-flex items-center gap-1 font-mono text-xs", unverified ? "text-warning" : "text-success")}
          title={sourceTitle}
        >
          <SourceIcon aria-hidden="true" className="h-3.5 w-3.5" />
          {item.source}
        </span>
      </CardContent>
      <CardFooter className="flex flex-wrap gap-2">
        {invalid ? null : (
          <Button variant="outline" size="sm" onPress={() => onPreview(item)}>
            {intl.formatMessage(themeCardMessages.preview)}
          </Button>
        )}
        {item.updateAvailable ? (
          <Button variant="primary" size="sm" isDisabled={busy} onPress={() => onUpdate(item)}>
            {intl.formatMessage(themeCardMessages.update)}
          </Button>
        ) : item.installed ? (
          <Button variant="outline" size="sm" isDisabled={busy} onPress={() => onRemove(item)}>
            {intl.formatMessage(themeCardMessages.remove)}
          </Button>
        ) : (
          <Button variant="primary" size="sm" isDisabled={busy || invalid} onPress={() => onInstall(item)}>
            {intl.formatMessage(themeCardMessages.install)}
          </Button>
        )}
      </CardFooter>
    </Card>
  );
}
