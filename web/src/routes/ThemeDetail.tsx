// Theme detail (spec 043, T030 + T044): previews of both modes, metadata,
// description and changelog, the problems list for an invalid theme, and the
// Preview / Install / Update / Remove actions. The page never shows a raw
// homepage or changelog as HTML; both are rendered as text.
import { useState, type ReactNode } from "react";
import { useQuery } from "@tanstack/react-query";
import { Link, useNavigate, useParams } from "@tanstack/react-router";
import { Button, Card, CardContent, CardHeader } from "@heroui/react";
import { ArrowLeft, ExternalLink } from "lucide-react";
import { PageHeader } from "@/components/ui/PageHeader";
import { SettingsNav } from "@/components/ui/SettingsNav";
import { ConfirmDialog } from "@/components/ui/ConfirmDialog";
import { ErrorCard } from "@/components/ui/ErrorCard";
import { LoadingCard } from "@/components/ui/LoadingCard";
import { ThemePreviewPanel } from "@/components/themes/ThemePreviewPanel";
import { ThemeStatusBadge } from "@/components/themes/ThemeCard";
import {
  findInstalled,
  themeErrorText,
  THEME_CATALOG_KEY,
  useThemeActions,
} from "@/components/themes/useThemeActions";
import {
  installedThemesMessages,
  themeCardMessages,
  themeDetailMessages,
  themePreviewBannerMessages,
} from "@/components/themes/messages";
import { Themes } from "@/lib/endpoints";
import { can, useMe } from "@/lib/auth";
import { THEMES_MANAGE_PERM } from "@/lib/themePermissions";
import { useThemePreview } from "@/lib/useThemePreview";
import { useOptionalIntl } from "@/lib/i18n/format";
import { formatWith } from "@/components/themes/formatWith";
import type { ThemeCatalogDetail } from "@/types";

// The homepage is shown only when it is an http(s) URL; the label drops the scheme.
function safeHomepage(raw: string): { href: string; label: string } | null {
  if (!raw) return null;
  try {
    const url = new URL(raw);
    if (url.protocol !== "http:" && url.protocol !== "https:") return null;
    return { href: url.href, label: raw.replace(/^https?:\/\//, "").replace(/\/$/, "") };
  } catch {
    return null;
  }
}

function Section({ title, children }: { title: string; children: ReactNode }) {
  return (
    <Card className="border border-border">
      <CardHeader className="pb-0">
        <h3 className="text-base font-semibold text-foreground">{title}</h3>
      </CardHeader>
      <CardContent className="space-y-3">{children}</CardContent>
    </Card>
  );
}

export function ThemeDetailPage() {
  const intl = useOptionalIntl();
  const navigate = useNavigate();
  const { data: me } = useMe();
  const canManage = can(me, THEMES_MANAGE_PERM);
  const { id } = useParams({ from: "/app-layout/settings/theme/catalog/$id" });
  const themeId = Number(id);
  const validId = Number.isInteger(themeId) && themeId > 0;

  const [pageError, setPageError] = useState<string | null>(null);
  const [removeOpen, setRemoveOpen] = useState(false);

  const detail = useQuery({
    queryKey: [THEME_CATALOG_KEY, themeId],
    queryFn: () => Themes.catalogEntry(themeId),
    enabled: validId,
  });
  const actions = useThemeActions(setPageError);
  const { start: startPreview } = useThemePreview();

  const item: ThemeCatalogDetail | undefined = detail.data;
  const installed = item ? findInstalled(actions.installed, item) : undefined;
  const invalid = item?.status === "invalid";
  const homepage = item ? safeHomepage(item.homepage) : null;

  const handlePreview = () => {
    if (!item?.tokens) return;
    setPageError(null);
    const started = startPreview({
      catalogId: item.id,
      installedId: installed?.id ?? null,
      displayName: item.displayName,
      version: item.version,
      tokens: item.tokens,
      css: item.css ?? null,
    });
    if (!started) {
      setPageError(intl.formatMessage(themePreviewBannerMessages.previewUnavailableSafeMode));
    }
  };

  const handleUpdate = () => {
    if (installed) actions.update.mutate(installed.id);
  };

  const confirmRemove = () => {
    if (!installed) {
      setRemoveOpen(false);
      return;
    }
    actions.remove.mutate(installed.id, {
      onSuccess: () => {
        setRemoveOpen(false);
        void navigate({ to: "/settings/theme/catalog" });
      },
    });
  };

  let body: ReactNode = null;
  if (!validId) {
    body = null;
  } else if (detail.isLoading) {
    body = <LoadingCard />;
  } else if (detail.isError) {
    body = <ErrorCard message={themeErrorText(detail.error)} onRetry={() => void detail.refetch()} />;
  } else if (item) {
    const versionText =
      installed && installed.version !== item.version
        ? formatWith(intl, themeDetailMessages.versionInstalled, {
            version: item.version,
            installed: installed.version,
          })
        : item.version;

    body = (
      <div className="space-y-6">
        <div className="space-y-3">
          <div className="flex flex-wrap items-center gap-3">
            <h2 className="text-2xl font-semibold text-foreground">{item.displayName}</h2>
            <ThemeStatusBadge item={item} />
          </div>
          <dl className="flex flex-wrap gap-x-6 gap-y-2 text-sm">
            <div className="flex gap-2">
              <dt className="text-muted">{intl.formatMessage(themeDetailMessages.author)}</dt>
              <dd className="text-foreground">{item.author}</dd>
            </div>
            <div className="flex gap-2">
              <dt className="text-muted">{intl.formatMessage(themeDetailMessages.version)}</dt>
              <dd className="font-mono text-foreground">{versionText}</dd>
            </div>
            <div className="flex gap-2">
              <dt className="text-muted">{intl.formatMessage(themeDetailMessages.license)}</dt>
              <dd className="text-foreground">{item.license}</dd>
            </div>
            {homepage ? (
              <div className="flex gap-2">
                <dt className="text-muted">{intl.formatMessage(themeDetailMessages.homepage)}</dt>
                <dd>
                  <a
                    href={homepage.href}
                    target="_blank"
                    rel="noopener noreferrer"
                    className="inline-flex items-center gap-1 text-primary hover:underline"
                  >
                    {homepage.label}
                    <ExternalLink aria-hidden="true" className="h-3.5 w-3.5" />
                  </a>
                </dd>
              </div>
            ) : null}
          </dl>
          <div className="flex flex-wrap items-center gap-2">
            {item.tags.map((label) => (
              <span key={label} className="rounded bg-surface px-2 py-0.5 text-xs text-muted">
                {label}
              </span>
            ))}
            <span className="rounded border border-border px-2 py-0.5 font-mono text-xs text-muted">{item.source}</span>
          </div>
        </div>

        <div className="grid gap-4 md:grid-cols-2">
          {(["light", "dark"] as const).map((mode) => (
            <div key={mode} className="space-y-2">
              <p className="text-sm text-muted">
                {intl.formatMessage(mode === "light" ? themeDetailMessages.previewLight : themeDetailMessages.previewDark)}
              </p>
              <ThemePreviewPanel themeId={item.id} mode={mode} modes={item.modes} className="w-full" />
            </div>
          ))}
        </div>

        {invalid ? (
          <div role="alert" className="rounded border border-danger/40 bg-danger/10 px-4 py-3">
            <p className="text-sm font-medium text-danger">
              {formatWith(intl, themeDetailMessages.problemsFound, { count: item.problems.length })}
            </p>
            <p className="text-sm text-danger">
              {formatWith(intl, themeDetailMessages.problemsNotice, { name: item.displayName })}
            </p>
          </div>
        ) : null}

        <div className="flex flex-wrap items-center gap-3">
          <Button variant="outline" isDisabled={invalid || actions.busy} onPress={handlePreview}>
            {intl.formatMessage(themeCardMessages.preview)}
          </Button>
          {installed ? (
            <Button variant="outline" isDisabled={actions.busy} onPress={() => setRemoveOpen(true)}>
              {intl.formatMessage(themeCardMessages.remove)}
            </Button>
          ) : null}
          {installed && item.updateAvailable ? (
            <>
              <span className="text-sm text-warning">
                {formatWith(intl, themeDetailMessages.updateFrom, { from: installed.version, to: item.version })}
              </span>
              <Button variant="primary" isDisabled={actions.busy} onPress={handleUpdate}>
                {intl.formatMessage(themeCardMessages.update)}
              </Button>
            </>
          ) : null}
          {!installed ? (
            <Button variant="primary" isDisabled={invalid || actions.busy} onPress={() => actions.install.mutate(item.id)}>
              {intl.formatMessage(themeCardMessages.install)}
            </Button>
          ) : null}
        </div>
        {invalid ? (
          <p className="text-sm text-muted">
            {formatWith(intl, themeDetailMessages.installUnavailable, { count: item.problems.length })}
          </p>
        ) : null}

        {item.problems.length > 0 ? (
          <Section title={intl.formatMessage(themeDetailMessages.problemsTitle)}>
            <ul className="space-y-2">
              {item.problems.map((p, index) => (
                <li key={`${p.rule}-${index}`} className="flex flex-col gap-1 sm:flex-row sm:gap-4">
                  <span className="font-mono text-xs text-danger">
                    {p.rule}
                    {p.line !== undefined ? formatWith(intl, themeDetailMessages.problemLine, { line: String(p.line) }) : null}
                  </span>
                  <span className="text-sm text-muted">{p.detail}</span>
                </li>
              ))}
            </ul>
          </Section>
        ) : null}

        <Section title={intl.formatMessage(themeDetailMessages.descriptionTitle)}>
          <p className="whitespace-pre-line text-sm text-muted">{item.description}</p>
        </Section>

        {item.changelog ? (
          <Section title={intl.formatMessage(themeDetailMessages.changelogTitle)}>
            <div className="whitespace-pre-wrap font-mono text-sm text-muted">{item.changelog}</div>
          </Section>
        ) : null}
      </div>
    );
  }

  return (
    <div className="space-y-6 p-6">
      <PageHeader
        title={intl.formatMessage(themeDetailMessages.title)}
        description={intl.formatMessage(themeDetailMessages.description)}
      />

      <div className="grid gap-6 lg:grid-cols-[220px_1fr]">
        <SettingsNav
          active="theme"
          showAdminSections={can(me, "config:manage")}
          showThemeSources={canManage}
          onSelect={(key) => void navigate({ to: "/admin", search: { section: key } })}
        />

        <div className="min-w-0 space-y-6">
          <Link
            to="/settings/theme/catalog"
            className="inline-flex items-center gap-1 text-sm text-muted hover:text-foreground"
          >
            <ArrowLeft aria-hidden="true" className="h-4 w-4" />
            {intl.formatMessage(themeDetailMessages.back)}
          </Link>

          {pageError ? (
            <div role="alert" className="rounded border border-danger/40 bg-danger/10 px-3 py-2 text-xs text-danger">
              {pageError}
            </div>
          ) : null}

          {body}
        </div>
      </div>

      <ConfirmDialog
        open={removeOpen}
        onOpenChange={setRemoveOpen}
        title={formatWith(intl, installedThemesMessages.removeTitle, { name: item?.displayName ?? "" })}
        description={intl.formatMessage(installedThemesMessages.removeText)}
        confirmLabel={intl.formatMessage(installedThemesMessages.removeConfirm)}
        destructive
        busy={actions.busy}
        onConfirm={confirmRemove}
      />
    </div>
  );
}
