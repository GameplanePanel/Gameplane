import { useInstallation } from "@/lib/useInstallation";
import { useState, useEffect, useRef, type ChangeEvent, type ComponentType, type ReactNode, type SetStateAction } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  Archive,
  Bell,
  BellRing,
  CircleHelp,
  CircleSlash2,
  Cog,
  Flame,
  Gamepad2,
  Globe,
  House,
  Key,
  Lock,
  Mail,
  Megaphone,
  MessagesSquare,
  Plus,
  Puzzle,
  RotateCcw,
  Trash2,
  Webhook,
} from "lucide-react";
import {
  Button,
  Input as HeroInput,
  Card,
  Select,
  ListBox,
  ListBoxItem,
  Switch,
  Label,
  Description,
  TextField,
  Alert,
} from "@heroui/react";
import { PageHeader } from "@/components/ui/PageHeader";
import { ConfirmDialog } from "@/components/ui/ConfirmDialog";
import { ConfirmAdminMappingDialog } from "@/components/ui/ConfirmAdminMappingDialog";
import { RemovableGroupChip } from "@/components/ui/RemovableGroupChip";
import { ProvenanceBadge } from "@/components/ui/ProvenanceBadge";
import { SlackIcon } from "@/components/ui/SlackIcon";
import { SettingsNav, type SettingsSectionKey } from "@/components/ui/SettingsNav";
import { cn, formatRelative } from "@/lib/utils";
import { errorTextIntl } from "@/lib/errors";
import { Telemetry, type TelemetryInfo } from "@/lib/api";
import { TELEMETRY_STATEMENT_URL } from "@/lib/links";
import { Auth, AuthProviders, createResourceClient, Cluster, Clusters, ModRegistries, Notifications, type SinkSecretBody } from "@/lib/endpoints";
import { useCurrentCluster } from "@/lib/cluster";
import { ClusterSelector } from "@/components/ClusterSelector";
import { LoadingCard } from "@/components/ui/LoadingCard";
import { ErrorCard } from "@/components/ui/ErrorCard";
import { can, useMe } from "@/lib/auth";
import type { ClusterInfo } from "@/types";
import {
  useConfig,
  useUpdateConfigSection,
  useResetRoleMapping,
  type AllConfig,
  type AuthCfg,
  type AuthDefaultRole,
  type AuthKind,
  type AuthProvider,
  type AuthRoleMappings,
  type GeneralCfg,
  type I18nCfg,
  type InstallTimeSettings,
  type KeyedRegistryProvider,
  type ModRegistryEntry,
  type ModRegistriesCfg,
  type NotifEventType,
  type NotifSink,
  type NotificationsCfg,
  type OIDCHelmProvider,
  type SinkKind,
  type TelemetryCfg,
} from "@/lib/config";
import { ErrorBanner } from "@/components/backups/ErrorBanner";
import { ModuleSourcesPanel } from "@/components/modules/ModuleSourcesPanel";
import { defineMessages, type IntlShape } from "react-intl";
import { useFormat, useOptionalIntl, type FormatApi } from "@/lib/i18n/format";
import { listedLanguages } from "@/lib/i18n/languages";

type Section =
  | "general" | "auth" | "backups" | "modules" | "modRegistries" | "notifications"
  | "telemetry" | "updates" | "about";

const SECTIONS: readonly Section[] = [
  "general", "auth", "backups", "modules", "modRegistries", "notifications",
  "telemetry", "updates", "about",
];

// initialSection honours /admin?section=<key> (the theme page's settings nav
// deep-links here). Read from window.location rather than the router so the
// page still renders standalone in tests.
function initialSection(): Section {
  if (typeof window === "undefined") return "general";
  const key = new URLSearchParams(window.location.search).get("section");
  return SECTIONS.find((s) => s === key) ?? "general";
}

export function AdminSettingsPage() {
  const intl = useOptionalIntl();
  const [section, setSection] = useState<Section>(initialSection);
  const cfg = useConfig();

  const selectSection = (key: SettingsSectionKey) => {
    setSection(key);
    // cfg is a single query shared by every section (staleTime: 0
    // in tests, 10s in prod) with one long-lived observer here in
    // AdminSettingsPage — switching sections alone never creates a
    // new observer, so nothing would otherwise refetch it. Force a
    // refetch on every nav click so revisiting a section (e.g.
    // Authentication, after another admin or Helm changed
    // installTimeSettings) shows current data.
    void cfg.refetch();
  };

  return (
    <div className="space-y-4 p-6">
      <PageHeader
        title={intl.formatMessage({ id: "web.routes.admin_settings.admin_settings", defaultMessage: "Admin settings", description: "Admin settings: page title at the top of the admin settings screen; keep it short" })}
        description={intl.formatMessage({ id: "web.routes.admin_settings.platform_wide_configuration_for_this", defaultMessage: "Platform-wide configuration for this Gameplane instance.", description: "Admin settings: description under the page title saying these settings apply to the whole Gameplane instance" })}
      />

      <div className="grid gap-6 lg:grid-cols-[220px_1fr]">
        {/* /admin is gated on config:manage, so every section is reachable. */}
        <SettingsNav active={section} onSelect={selectSection} showAdminSections />

        <div className="space-y-6">
          {cfg.isLoading && (
            <Card>
              <Card.Content className="text-sm text-muted">{intl.formatMessage({ id: "web.routes.admin_settings.loading_configuration", defaultMessage: "Loading configuration…", description: "Admin settings: loading text shown in a card while the configuration is fetched" })}</Card.Content>
            </Card>
          )}
          {cfg.isError && (
            <Card>
              <Card.Content className="text-sm text-danger">{intl.formatMessage({ id: "web.routes.admin_settings.failed_to_load_configuration_refresh_to", defaultMessage: "Failed to load configuration. Refresh to retry.", description: "Admin settings: error text in a card when the configuration cannot be fetched; asks the user to refresh the page" })}</Card.Content>
            </Card>
          )}
          {cfg.data && section === "general"       && <GeneralSection       initial={cfg.data.general} />}
          {cfg.data && section === "general"       && <LanguageSection      initial={cfg.data.i18n} />}
          {cfg.data && section === "auth"          && <AuthSection          initial={cfg.data.auth} general={cfg.data.general} installTimeSettings={cfg.data.installTimeSettings} />}
          {section === "backups"                   && <BackupDestSection />}
          {section === "modules"                   && <ModuleSourcesPanel />}
          {cfg.data && section === "modRegistries" && <ModRegistriesSection initial={cfg.data.modRegistries} />}
          {cfg.data && section === "notifications" && <NotificationsSection initial={cfg.data.notifications} />}
          {cfg.data && section === "telemetry"     && <TelemetrySection     initial={cfg.data.telemetry} />}
          {section === "updates"                   && <UpdatesSection />}
          {section === "about"                     && <AboutSection />}
        </div>
      </div>
    </div>
  );
}

function SectionCard({
  title,
  subtitle,
  footer,
  children,
  className,
  headerClassName,
  titleClassName,
  subtitleClassName,
  contentClassName,
  footerClassName,
}: {
  title: string;
  subtitle?: string;
  footer?: ReactNode;
  children: ReactNode;
  className?: string;
  headerClassName?: string;
  titleClassName?: string;
  subtitleClassName?: string;
  contentClassName?: string;
  footerClassName?: string;
}) {
  return (
    <Card role="region" aria-label={title} className={cn("space-y-4", className)}>
      <Card.Header className={cn("space-y-2 pb-2", headerClassName)}>
        <div className={cn("font-medium text-base", titleClassName)}>{title}</div>
        {subtitle && <div className={cn("text-xs text-muted", subtitleClassName)}>{subtitle}</div>}
      </Card.Header>
      <Card.Content className={cn("space-y-4", contentClassName)}>{children}</Card.Content>
      {footer && (
        <Card.Footer className={cn("flex items-center justify-end gap-3 pt-2", footerClassName)}>
          {footer}
        </Card.Footer>
      )}
    </Card>
  );
}

function Field({ label, children, hint }: { label: string; children: ReactNode; hint?: string }) {
  return (
    <TextField className="space-y-1.5">
      <Label className="text-xs">{label}</Label>
      {children}
      {hint && <Description className="text-[11px]">{hint}</Description>}
    </TextField>
  );
}

function SaveStatus({
  pending,
  error,
  saved,
}: {
  pending: boolean;
  error: string | null;
  saved: boolean;
}) {
  const intl = useOptionalIntl();
  if (pending) return <span className="text-xs text-muted">{intl.formatMessage({ id: "web.routes.admin_settings.saving", defaultMessage: "Saving…", description: "Admin settings: status text beside a save button while changes are being saved; keep it short" })}</span>;
  if (error)   return <span className="text-xs text-danger">{error}</span>;
  if (saved)   return <span className="text-xs text-success">{intl.formatMessage({ id: "web.routes.admin_settings.saved", defaultMessage: "Saved", description: "Admin settings: status text beside a save button confirming the changes were saved; keep it short" })}</span>;
  return null;
}

// A change to an API-managed Secret, staged alongside a section draft.
// Save runs writes before the config PUT (the saved rows reference them)
// and removals only after the PUT succeeds. Leaving the section without
// saving discards every staged change.
type StagedSecret = { kind: "write" | "remove"; run: () => Promise<unknown> };

// useSectionForm wires the common pattern: hold a draft of the section
// payload, submit on Save, surface server validation errors as the
// rendered SaveStatus, and reset the "Saved" indicator on next edit.
// Managed-Secret changes are staged with the draft (stageSecret) and
// applied only by Save, so the stored config and its Secrets change
// together.
function useSectionForm<T>(initial: T, section: Parameters<typeof useUpdateConfigSection>[0]) {
  const [draft, setDraft] = useState<T>(initial);
  const [base, setBase] = useState<T>(initial);
  const [dirty, setDirty] = useState(false);
  const revision = useRef(0);
  // A successful PUT must not be undone by the cache from before that write.
  const [committedAfter, setCommittedAfter] = useState<number | null>(null);
  const qc = useQueryClient();
  const configState = qc.getQueryState<AllConfig>(["config"]);
  const [syncedSource, setSyncedSource] = useState({ initial, count: configState?.dataUpdateCount });
  const [error, setError] = useState<string | null>(null);
  const [saved, setSaved] = useState(false);
  const [writing, setWriting] = useState(false);
  const [checking, setChecking] = useState(false);
  // Keyed per Secret (for example "sink:<name>"): a later change to the
  // same Secret replaces the earlier one.
  const [staged, setStaged] = useState<Record<string, StagedSecret>>({});
  const mut = useUpdateConfigSection(section);
  const intl = useOptionalIntl();

  // Adjust this component's state before rendering children for a new source.
  // Remember its identity and successful-read count so this converges on the
  // next render, including when a refetch returns structurally identical data.
  if (!dirty && !writing && !mut.isPending && configState?.status === "success"
    && (committedAfter === null || configState.dataUpdateCount > committedAfter)
    && (syncedSource.initial !== initial || syncedSource.count !== configState.dataUpdateCount)) {
    setSyncedSource({ initial, count: configState.dataUpdateCount });
    setDraft(initial);
    setBase(initial);
  }

  const markEdited = () => {
    revision.current++;
    setDirty(true);
    setError(null);
    setSaved(false);
  };

  const update = (patch: Partial<T>) => {
    markEdited();
    setDraft((d) => ({ ...d, ...patch }));
  };

  const replace = (next: SetStateAction<T>) => {
    markEdited();
    setDraft(next);
  };

  // A successful dedicated reset changes both the stored baseline and draft.
  const acceptReset = (transform: (value: T) => T) => {
    markEdited();
    setBase(transform);
    setDraft(transform);
  };

  const stageSecret = (key: string, change: StagedSecret) => {
    markEdited();
    setStaged((s) => ({ ...s, [key]: change }));
  };

  const fail = (message: string) => {
    setSaved(false);
    setError(message);
  };

  const run = async () => {
    setSaved(false);
    setError(null);
    const submittedRevision = revision.current;
    // A navigation refresh may still be in flight. Await that same request
    // before checking the baseline or performing any staged Secret writes.
    if (qc.isFetching({ queryKey: ["config"] })) {
      setChecking(true);
      try {
        await qc.refetchQueries({ queryKey: ["config"] }, { cancelRefetch: false });
      } finally {
        setChecking(false);
      }
    }
    const verified = qc.getQueryState<AllConfig>(["config"]);
    if (verified?.status !== "success"
      || (committedAfter !== null && verified.dataUpdateCount <= committedAfter)) {
      setError(intl.formatMessage({ id: "web.routes.admin_settings.could_not_refresh_configuration", defaultMessage: "Could not refresh configuration. Retry the refresh before saving.", description: "Admin settings: error shown when the config could not be re-fetched before a save" }));
      return;
    }
    const latest = verified.data?.[section] ?? initial;
    if (JSON.stringify(latest) !== JSON.stringify(base)) {
      setError(intl.formatMessage({ id: "web.routes.admin_settings.configuration_changed", defaultMessage: "Configuration changed. Copy your edits and reopen this section before saving.", description: "Admin settings: error shown when the saved config changed under an open edit form" }));
      return;
    }
    const changes = staged;
    const list = Object.values(changes);
    setWriting(true);
    try {
      await Promise.all(list.filter((c) => c.kind === "write").map((c) => c.run()));
    } catch (e) {
      setError(errorTextIntl(intl, e, intl.formatMessage({ id: "web.routes.admin_settings.failed_to_store_a_secret", defaultMessage: "Failed to store a secret", description: "Admin settings: fallback error when a Secret write fails" })));
      return;
    } finally {
      setWriting(false);
    }
    const beforeSave = qc.getQueryState(["config"])?.dataUpdateCount ?? 0;
    mut.mutate(draft as never, {
      onSuccess: () => {
        for (const c of list) {
          if (c.kind === "remove") void c.run().catch(() => undefined);
        }
        setStaged((s) =>
          Object.fromEntries(Object.entries(s).filter(([key, c]) => changes[key] !== c)),
        );
        // Invalidation awaits the GET but swallows its errors. Only a new,
        // successful response supersedes the value that the PUT committed.
        setCommittedAfter(beforeSave);
        const refreshedState = qc.getQueryState<AllConfig>(["config"]);
        const refreshed = refreshedState?.status === "success" && refreshedState.dataUpdateCount > beforeSave
          ? (refreshedState.data?.[section] ?? draft) as T
          : draft;
        setBase(refreshed);
        const unchanged = revision.current === submittedRevision;
        if (unchanged) {
          setDraft(refreshed);
          setDirty(false);
        }
        setSaved(unchanged);
      },
      onError: (e) => setError(errorTextIntl(intl, e, intl.formatMessage({ id: "web.routes.admin_settings.save_failed", defaultMessage: "Save failed", description: "Admin settings: fallback error when saving a section fails" }))),
    });
  };

  const save = () => {
    void run();
  };

  return { draft, update, replace, acceptReset, save, stageSecret, fail, pending: mut.isPending || writing || checking, error, saved };
}

const defaultGeneral: GeneralCfg = {
  instanceName: "",
  externalURL: "",
  defaultNamespace: "gameplane-games",
};

function GeneralSection({ initial }: { initial?: GeneralCfg }) {
  const intl = useOptionalIntl();
  const f = useSectionForm<GeneralCfg>(initial ?? defaultGeneral, "general");
  return (
    <SectionCard
      title={intl.formatMessage({ id: "web.routes.admin_settings.general", defaultMessage: "General", description: "Admin settings: title of the General section card that holds the instance identity fields; keep it short" })}
      subtitle={intl.formatMessage({ id: "web.routes.admin_settings.basic_identity_for_this_gameplane", defaultMessage: "Basic identity for this Gameplane instance.", description: "Admin settings: subtitle under the General title describing the basic identity of this Gameplane instance" })}
      footer={
        <>
          <SaveStatus pending={f.pending} error={f.error} saved={f.saved} />
          <Button variant="primary" onPress={f.save} isDisabled={f.pending}>{intl.formatMessage({ id: "web.routes.admin_settings.save_changes", defaultMessage: "Save changes", description: "Admin settings: button that saves the changes in this section; keep it short" })}</Button>
        </>
      }
    >
      <Field label={intl.formatMessage({ id: "web.routes.admin_settings.instance_name", defaultMessage: "Instance name", description: "Admin settings: label of the text field for the instance name, which shows on the Cluster page and in notifications" })} hint={intl.formatMessage({ id: "web.routes.admin_settings.optional_names_this_install_on_the", defaultMessage: "Optional. Names this install on the Cluster page and in notifications.", description: "Admin settings: hint under the instance name field saying the name is optional and where it appears" })}>
        <HeroInput
          value={f.draft.instanceName}
          onChange={(e) => f.update({ instanceName: e.target.value })}
        />
      </Field>
      <Field label={intl.formatMessage({ id: "web.routes.admin_settings.external_url", defaultMessage: "External URL", description: "Admin settings: label of the field for the public base URL of this instance" })} hint={intl.formatMessage({ id: "web.routes.admin_settings.canonical_base_url_used_in_emails", defaultMessage: "Canonical base URL, used in emails, webhooks, OIDC callbacks.", description: "Admin settings: hint under the external URL field: the canonical base URL used in emails, webhooks and OIDC callbacks (OIDC: technical term, usually left untranslated)" })}>
        <HeroInput
          value={f.draft.externalURL}
          onChange={(e) => f.update({ externalURL: e.target.value })}
        />
      </Field>
      <Field label={intl.formatMessage({ id: "web.routes.admin_settings.default_namespace", defaultMessage: "Default namespace", description: "Admin settings: label of the field for the Kubernetes namespace where new game servers are created by default" })} hint={intl.formatMessage({ id: "web.routes.admin_settings.where_new_gameservers_land_by_default", defaultMessage: "Where new GameServers land by default.", description: "Admin settings: hint under the default namespace field saying new GameServers are created in that namespace by default" })}>
        <HeroInput
          value={f.draft.defaultNamespace}
          onChange={(e) => f.update({ defaultNamespace: e.target.value })}
        />
      </Field>
    </SectionCard>
  );
}

const defaultI18n: I18nCfg = { defaultLanguage: "en" };

// Instance default language (config section "i18n"). Its own card with its own
// Save, so saving it never writes the General fields and vice versa.
function LanguageSection({ initial }: { initial?: I18nCfg }) {
  const intl = useOptionalIntl();
  const f = useSectionForm<I18nCfg>(initial ?? defaultI18n, "i18n");
  const languages = listedLanguages();
  const label = intl.formatMessage({
    id: "web.admin_settings.language.default_label",
    defaultMessage: "Default language",
    description: "Label of the instance default language select on the admin Language card",
  });
  return (
    <SectionCard
      title={intl.formatMessage({
        id: "web.admin_settings.language.title",
        defaultMessage: "Language",
        description: "Title of the admin Language card that sets the instance default language",
      })}
      subtitle={intl.formatMessage({
        id: "web.admin_settings.language.subtitle",
        defaultMessage: "Default language for this Gameplane instance.",
        description: "Subtitle of the admin Language card that sets the instance default language",
      })}
      footer={
        <>
          <SaveStatus pending={f.pending} error={f.error} saved={f.saved} />
          <Button variant="primary" onPress={f.save} isDisabled={f.pending}>
            {intl.formatMessage({
              id: "web.admin_settings.language.save",
              defaultMessage: "Save changes",
              description: "Save button of the admin Language card (instance default language)",
            })}
          </Button>
        </>
      }
    >
      <Field
        label={label}
        hint={intl.formatMessage({
          id: "web.admin_settings.language.default_hint",
          defaultMessage: "Used when someone has not chosen a language and none of their browser's languages are available. Notification sinks set to Instance default use it too.",
          description: "Hint under the instance default language select on the admin Language card",
        })}
      >
        <Select
          aria-label={label}
          selectedKey={f.draft.defaultLanguage}
          onSelectionChange={(key) => {
            if (key !== null) f.update({ defaultLanguage: String(key) });
          }}
        >
          <Select.Trigger className="w-full rounded border border-border bg-surface px-3 py-2 text-sm hover:bg-surface/80">
            <Select.Value />
            <Select.Indicator className="ml-auto h-4 w-4" />
          </Select.Trigger>
          <Select.Popover className="rounded border border-border">
            <ListBox aria-label={label}>
              {languages.map((lang) => (
                <ListBoxItem key={lang.tag} id={lang.tag}>
                  {lang.nativeName}
                </ListBoxItem>
              ))}
              {/* A saved tag that is not listed stays visible under its raw tag (display only). */}
              {f.draft.defaultLanguage && !languages.some((lang) => lang.tag === f.draft.defaultLanguage) && (
                <ListBoxItem id={f.draft.defaultLanguage}>{f.draft.defaultLanguage}</ListBoxItem>
              )}
            </ListBox>
          </Select.Popover>
        </Select>
      </Field>
    </SectionCard>
  );
}

const defaultAuth: AuthCfg = {
  providers: [{ name: "Local accounts", kind: "local", enabled: true }],
};

// The managed-Secret name the API derives for a provider's clientSecret
// (mirrors providerSecretPrefix in api/internal/handlers).
const providerSecretPrefix = "gameplane-auth-";
const maxProviderName = 63 - providerSecretPrefix.length;

// Scopes may be space- or comma-separated; group lists are comma-separated
// only (group names may contain spaces). Both trim entries and drop empties
// so blank inputs serialize to absent fields, never [""].
const parseScopes = (raw: string) => raw.split(/[\s,]+/).filter(Boolean);
const parseGroups = (raw: string) =>
  raw
    .split(",")
    .map((g) => g.trim())
    .filter(Boolean);

// withoutRoleOverride drops one role's dashboard override from an auth
// draft: the same change the reset endpoint makes to the stored config,
// including removing containers left empty.
function withoutRoleOverride(cfg: AuthCfg, role: "admin" | "operator" | "viewer"): AuthCfg {
  const mappings: AuthRoleMappings = { ...cfg.helmOverride?.roleMappings };
  delete mappings[role];
  const next: AuthCfg = { ...cfg };
  if (Object.keys(mappings).length > 0) {
    next.helmOverride = { ...cfg.helmOverride, roleMappings: mappings };
  } else {
    delete next.helmOverride;
  }
  return next;
}

function AuthSection({ initial, general, installTimeSettings }: { initial?: AuthCfg; general?: GeneralCfg; installTimeSettings?: InstallTimeSettings }) {
  const intl = useOptionalIntl();
  const f = useSectionForm<AuthCfg>(initial ?? defaultAuth, "auth");
  const [adding, setAdding] = useState(false);
  // Runtime providers reveal whether a Helm-flag ("helm") provider exists;
  // it always counts as enabled and is managed in values.yaml, not here.
  const { data: runtime } = useQuery({
    queryKey: ["login-providers"],
    queryFn: () => Auth.providers().catch(() => null),
  });
  const helm = runtime?.providers.find((p) => p.name === "helm") ?? null;
  const enabledCount = f.draft.providers.filter((p) => p.enabled).length;
  // With a Helm provider present, login always stays possible, so the
  // last dashboard-managed toggle may be turned off.
  const lastToggleLocked = enabledCount === 1 && !helm;
  const togglerFor = (idx: number) => () => {
    const next = f.draft.providers.map((p, i) =>
      i === idx ? { ...p, enabled: !p.enabled } : p,
    );
    f.replace({ ...f.draft, providers: next });
  };
  const removeProvider = (idx: number) => {
    const p = f.draft.providers[idx];
    // The API-managed clientSecret Secret is removed only by the Save that
    // drops this row; the server refuses Secrets it didn't create.
    if (!p.configRef || p.configRef === providerSecretPrefix + p.name) {
      f.stageSecret(`provider:${p.name}`, {
        kind: "remove",
        run: () => AuthProviders.deleteSecret(p.name),
      });
    }
    f.replace({ ...f.draft, providers: f.draft.providers.filter((_, i) => i !== idx) });
  };
  const me = useMe().data;
  return (
    <div className="space-y-6">
      <SectionCard
        className="gap-0 space-y-0 p-0"
        title={intl.formatMessage({ id: "web.routes.admin_settings.authentication", defaultMessage: "Authentication", description: "Admin settings: title of the Authentication section card for local accounts and identity providers; keep it short" })}
        subtitle={intl.formatMessage({ id: "web.routes.admin_settings.built_in_local_accounts_plus_federated", defaultMessage: "Built-in local accounts plus federated identity providers. Changes take effect on save — no restart needed.", description: "Admin settings: subtitle under the Authentication title; says changes take effect on save without a restart" })}
        titleClassName="text-sm font-medium"
        subtitleClassName="text-sm"
        headerClassName="px-5 pt-5 pb-4 space-y-1"
        contentClassName="px-5 space-y-4"
        footerClassName="px-5 pt-2 pb-5 justify-end"
        footer={
          <>
            <SaveStatus pending={f.pending} error={f.error} saved={f.saved} />
            <Button variant="primary" onPress={f.save} isDisabled={f.pending}>{intl.formatMessage({ id: "web.routes.admin_settings.save_changes", defaultMessage: "Save changes", description: "Admin settings: button that saves the changes in this section; keep it short" })}</Button>
          </>
        }
      >
        {f.draft.providers.length === 0 && !helm && (
          <div className="text-sm text-muted">{intl.formatMessage({ id: "web.routes.admin_settings.no_identity_providers_configured_yet", defaultMessage: "No identity providers configured yet.", description: "Admin settings: empty-state text in the Authentication section shown when no identity provider has been added" })}</div>
        )}
        <ul className="divide-y divide-border">
          {f.draft.providers.map((p, idx) => (
            <li key={p.name} className="flex items-center gap-3 py-3">
              <div className="flex h-8 w-8 items-center justify-center rounded-md bg-surface-secondary">
                <Key className="h-4 w-4 text-muted" />
              </div>
              <div className="min-w-0 flex-1 flex flex-col gap-0.5">
                <div className="text-sm">{p.displayName || p.name}</div>
                <div className="truncate text-xs text-muted">
                  {p.kind}
                  {p.issuer ? intl.formatMessage({ id: "web.routes.admin_settings.provider_issuer_suffix", defaultMessage: " · {issuer}", description: "Admin settings: issuer shown after the provider type; {issuer} is the issuer URL" }, { issuer: p.issuer }) : p.configRef ? intl.formatMessage({ id: "web.routes.admin_settings.provider_secret_suffix", defaultMessage: " · {ref}", description: "Admin settings: Secret reference shown after the provider type when no issuer is set; {ref} is the Secret name" }, { ref: p.configRef }) : ""}
                </div>
              </div>
              <button
                type="button"
                onClick={togglerFor(idx)}
                disabled={p.enabled && lastToggleLocked}
                title={
                  p.enabled && lastToggleLocked
                    ? intl.formatMessage({ id: "web.routes.admin_settings.at_least_one_identity_provider", defaultMessage: "At least one identity provider must stay enabled.", description: "Admin settings: tooltip on an identity provider toggle that cannot be turned off" })
                    : undefined
                }
                className={cn(
                  "rounded-full px-1 py-0.5 text-[11px] font-medium font-sans uppercase disabled:cursor-not-allowed disabled:opacity-60",
                  p.enabled ? "bg-success-soft text-success-soft-foreground" : "bg-default text-muted",
                )}
              >
                {p.enabled ? intl.formatMessage({ id: "web.routes.admin_settings.provider_enabled_state", defaultMessage: "Enabled", description: "Admin settings: state of an identity provider or toggle that is on" }) : intl.formatMessage({ id: "web.routes.admin_settings.provider_disabled_state", defaultMessage: "Disabled", description: "Admin settings: state of an identity provider or toggle that is off" })}
              </button>
              {p.kind !== "local" && (
                <Button
                  variant="ghost"
                  isIconOnly
                  aria-label={intl.formatMessage({ id: "web.routes.admin_settings.delete_provider", defaultMessage: "Delete provider {name}", description: "Admin settings: aria-label of the button that deletes an identity provider; {name} is the provider name" }, { name: p.name })}
                  onPress={() => removeProvider(idx)}
                >
                  <Trash2 className="h-4 w-4" />
                </Button>
              )}
            </li>
          ))}
          {helm && (
            <li className="flex items-center gap-3 py-3">
              <div className="flex h-8 w-8 items-center justify-center rounded-md bg-surface-secondary">
                <Lock className="h-4 w-4 text-muted" />
              </div>
              <div className="min-w-0 flex-1 flex flex-col gap-0.5">
                <div className="text-sm">{helm.label}</div>
                <div className="truncate text-xs text-muted">{intl.formatMessage({ id: "web.routes.admin_settings.oidc_configured_via_helm_manage_in", defaultMessage: "oidc · configured via Helm — manage in values.yaml", description: "Admin settings: small line under a Helm-configured provider name giving its kind (OIDC, technical term, usually left untranslated) and saying it is managed in values.yaml" })}</div>
              </div>
              <span className="rounded-full px-1 py-0.5 text-[11px] font-medium font-sans uppercase bg-success-soft text-success-soft-foreground">{intl.formatMessage({ id: "web.routes.admin_settings.enabled", defaultMessage: "Enabled", description: "Admin settings: status badge on a provider row showing the provider is turned on; keep it short" })}</span>
            </li>
          )}
        </ul>
        {lastToggleLocked && (
          <p className="text-xs text-muted">{intl.formatMessage({ id: "web.routes.admin_settings.at_least_one_identity_provider_must", defaultMessage: "At least one identity provider must stay enabled — the last enabled provider can't be turned off.", description: "Admin settings: note under the provider list explaining that the last enabled provider cannot be turned off" })}</p>
        )}
        {adding ? (
          <AddProviderForm
            existing={f.draft.providers.map((p) => p.name)}
            externalURLSet={Boolean(general?.externalURL)}
            onAdd={(p, clientSecret) => {
              f.stageSecret(`provider:${p.name}`, {
                kind: "write",
                run: () => AuthProviders.putSecret(p.name, { clientSecret }),
              });
              f.replace({ ...f.draft, providers: [...f.draft.providers, p] });
            }}
            onClose={() => setAdding(false)}
          />
        ) : (
          <Button
            variant="outline"
            size="sm"
            className="h-8 gap-1.5 text-[13px] font-medium"
            onPress={() => setAdding(true)}
          >
            <Plus className="h-3.5 w-3.5" />{intl.formatMessage({ id: "web.routes.admin_settings.add_provider", defaultMessage: "Add provider", description: "Admin settings: button that opens the form to add an identity provider, and the button in that form that submits it; keep it short" })}</Button>
        )}
      </SectionCard>
      {installTimeSettings?.oidcHelmProvider && (
        <HelmOIDCProviderCard provider={installTimeSettings.oidcHelmProvider} />
      )}
      {can(me, "config:manage") && (
        <RoleMappingOverridesCard
          initial={f.draft}
          installTimeSettings={installTimeSettings}
          onUpdate={f.replace}
          onResetDone={(role) => f.acceptReset((d) => withoutRoleOverride(d, role))}
          onResetError={f.fail}
          formState={{ pending: f.pending, error: f.error, saved: f.saved }}
          onSave={f.save}
        />
      )}
    </div>
  );
}

/**
 * Inline warning banner shown in AddProviderForm when admin groups are entered.
 * Alerts the user to the security implications of mapping admin group membership.
 */
function AdminGroupsInlineWarning() {
  const intl = useOptionalIntl();
  return (
    <div className="rounded-md border border-warning-soft-foreground bg-warning-soft p-3 flex gap-3">
      <Megaphone className="h-4 w-4 text-warning-soft-foreground flex-shrink-0 mt-0.5" />
      <div className="text-xs">
        <div className="font-medium text-warning-soft-foreground mb-1">{intl.formatMessage({ id: "web.routes.admin_settings.full_admin_access", defaultMessage: "Full admin access", description: "Admin settings: warning heading shown when groups are about to be mapped to the admin role" })}</div>
        <p className="text-warning-soft-foreground">{intl.formatMessage({ id: "web.routes.admin_settings.groups_added_here_will_be_mapped_to_the", defaultMessage: "Groups added here will be mapped to the admin role and get full cluster control from their next login. You'll be asked to confirm before this is saved.", description: "Admin settings: warning text explaining that groups added here get full cluster control from their next login and must be confirmed before saving" })}</p>
      </div>
    </div>
  );
}

function AddProviderForm({
  existing,
  externalURLSet,
  onAdd,
  onClose,
}: {
  existing: string[];
  externalURLSet: boolean;
  onAdd: (p: AuthProvider, clientSecret: string) => void;
  onClose: () => void;
}) {
  const intl = useOptionalIntl();
  const [kind, setKind] = useState<AuthKind>("oidc");
  const [name, setName] = useState("");
  const [displayName, setDisplayName] = useState("");
  const [issuer, setIssuer] = useState("");
  const [clientID, setClientID] = useState("");
  const [clientSecret, setClientSecret] = useState("");
  const [scopes, setScopes] = useState("");
  const [groupsClaim, setGroupsClaim] = useState("");
  const [adminGroups, setAdminGroups] = useState("");
  const [operatorGroups, setOperatorGroups] = useState("");
  const [viewerGroups, setViewerGroups] = useState("");
  const [defaultRole, setDefaultRole] = useState<AuthDefaultRole | "">("");
  const [confirmingAdmin, setConfirmingAdmin] = useState(false);


  // Role-mapping lists, parsed live so the Default role select can unlock
  // as soon as any mapping exists (the API rejects defaultRole without
  // roleMappings).
  const adminList = parseGroups(adminGroups);
  const operatorList = parseGroups(operatorGroups);
  const viewerList = parseGroups(viewerGroups);
  const hasMappings = adminList.length + operatorList.length + viewerList.length > 0;

  // Presets prefill what they can: Google is a real OIDC issuer;
  // github.com publishes no OIDC discovery for user login, so that kind
  // needs a bridge (Dex or similar) whose issuer goes here.
  const applyPreset = (k: AuthKind) => {
    setKind(k);
    if (k === "google") {
      setIssuer("https://accounts.google.com");
      if (!displayName) setDisplayName("Google");
    } else if (k === "github" && !displayName) {
      setDisplayName("GitHub");
    }
  };

  const dns = /^[a-z0-9]([-a-z0-9]{0,61}[a-z0-9])?$/;
  const nameOk =
    dns.test(name) && name !== "helm" && name.length <= maxProviderName && !existing.includes(name);
  const valid = nameOk && /^https?:\/\/.+/.test(issuer) && clientID !== "" && clientSecret !== "";

  // The clientSecret is written by the section's Save, just before the
  // provider row that references it; nothing is stored before then.
  const submit = () => {
    const scopeList = parseScopes(scopes);
    const claim = groupsClaim.trim();
    onAdd(
      {
        name,
        kind,
        ...(displayName ? { displayName } : {}),
        enabled: true,
        issuer,
        clientID,
        configRef: providerSecretPrefix + name,
        ...(scopeList.length > 0 ? { scopes: scopeList } : {}),
        ...(claim ? { groupsClaim: claim } : {}),
        ...(hasMappings
          ? {
              roleMappings: {
                ...(adminList.length > 0 ? { admin: adminList } : {}),
                ...(operatorList.length > 0 ? { operator: operatorList } : {}),
                ...(viewerList.length > 0 ? { viewer: viewerList } : {}),
              },
            }
          : {}),
        ...(hasMappings && defaultRole ? { defaultRole } : {}),
      },
      clientSecret,
    );
    onClose();
  };

  // Handle submit button click — if admin groups exist and not yet confirmed,
  // show confirmation dialog; otherwise proceed with submit
  const handleSubmitClick = () => {
    if (adminList.length > 0 && !confirmingAdmin) {
      setConfirmingAdmin(true);
      return;
    }
    submit();
  };

  return (
    <div className="space-y-3 rounded-md border border-border bg-surface/30 p-4">
      <div className="text-sm font-medium">{intl.formatMessage({ id: "web.routes.admin_settings.add_identity_provider", defaultMessage: "Add identity provider", description: "Admin settings: heading of the card for adding a new identity provider" })}</div>
      {!externalURLSet && (
        <Alert status="warning">
          <Alert.Content className="text-xs">{intl.formatMessage({ id: "web.routes.admin_settings.set", defaultMessage: "Set", description: "Admin settings: opening word of the warning saying the external URL must be set first, followed by the setting path and the rest of the sentence" })} <span className="font-mono">{intl.formatMessage({ id: "web.routes.admin_settings.general_external_url", defaultMessage: "General → External URL", description: "Admin settings: setting path shown in monospace inside the warning, pointing to the External URL field of the General section" })}</span> {intl.formatMessage({ id: "web.routes.admin_settings.first_it_forms_the_provider_s_oidc", defaultMessage: "first — it forms the provider's OIDC redirect URL.", description: "Admin settings: end of the warning sentence saying the external URL forms the provider's OIDC redirect URL (OIDC: technical term, usually left untranslated)" })}</Alert.Content>
        </Alert>
      )}
      <div className="grid gap-3 md:grid-cols-2">
        <TextField>
          <Label className="text-xs">{intl.formatMessage({ id: "web.routes.admin_settings.kind", defaultMessage: "Kind", description: "Admin settings: label of the dropdown that chooses the provider kind, such as generic OIDC, Google or GitHub" })}</Label>
          <Select
            aria-label={intl.formatMessage({ id: "web.routes.admin_settings.provider_kind", defaultMessage: "Provider kind", description: "Admin settings: accessible name of the provider kind dropdown; its options are generic OIDC, Google and GitHub" })}
            selectedKey={kind}
            onSelectionChange={(key) => applyPreset(key as AuthKind)}
          >
            <Select.Trigger className="w-full rounded border border-border bg-surface px-3 py-2 text-sm hover:bg-surface/80">
              <Select.Value />
              <Select.Indicator className="ml-auto h-4 w-4" />
            </Select.Trigger>
            <Select.Popover className="rounded border border-border">
              <ListBox aria-label={intl.formatMessage({ id: "web.routes.admin_settings.provider_kind", defaultMessage: "Provider kind", description: "Admin settings: accessible name of the provider kind dropdown; its options are generic OIDC, Google and GitHub" })}>
                <ListBoxItem id="oidc">{intl.formatMessage({ id: "web.routes.admin_settings.generic_oidc", defaultMessage: "Generic OIDC", description: "Admin settings: option in the provider kind dropdown for a generic OIDC provider; keep it short" })}</ListBoxItem>
                <ListBoxItem id="google">{intl.formatMessage({ id: "web.routes.admin_settings.google", defaultMessage: "Google", description: "Admin settings: option in the provider kind dropdown for Google sign-in; keep it short" })}</ListBoxItem>
                <ListBoxItem id="github">{intl.formatMessage({ id: "web.routes.admin_settings.github_via_oidc_bridge", defaultMessage: "GitHub (via OIDC bridge)", description: "Admin settings: option in the provider kind dropdown for GitHub, reached through an OIDC bridge; keep it short" })}</ListBoxItem>
              </ListBox>
            </Select.Popover>
          </Select>
          {kind === "github" && (
            <Description className="text-[11px]">{intl.formatMessage({ id: "web.routes.admin_settings.github_com_has_no_oidc_discovery_for", defaultMessage: "GitHub.com has no OIDC discovery for user login — run an OIDC bridge such as Dex and enter its issuer URL below.", description: "Admin settings: small note under the provider kind dropdown saying GitHub.com needs an OIDC bridge such as Dex, with its issuer URL entered below" })}</Description>
          )}
        </TextField>
        <TextField>
          <Label className="text-xs">{intl.formatMessage({ id: "web.routes.admin_settings.name", defaultMessage: "Name", description: "Admin settings: label of the name field, a short identifier for the provider or sink; the same label is used in the add-sink form" })}</Label>
          <HeroInput
            placeholder={intl.formatMessage({ id: "web.routes.admin_settings.corp_sso", defaultMessage: "corp-sso", description: "Admin settings: grey example text inside the provider name field, shown only while it is empty" })}
            value={name}
            onChange={(e) => setName(e.target.value)}
          />
          <Description className="text-[11px]">{intl.formatMessage({ id: "web.routes.admin_settings.a_short_lowercase_identifier_letters", defaultMessage: "A short lowercase identifier (letters, digits, and dashes) used in the login route and the Secret name.", description: "Admin settings: hint under the provider name field: a short lowercase identifier of letters, digits and dashes, used in the login route and the Secret name" })}</Description>
        </TextField>
        <TextField>
          <Label className="text-xs">{intl.formatMessage({ id: "web.routes.admin_settings.display_name_login_button", defaultMessage: "Display name (login button)", description: "Admin settings: label of the field for the name shown on the sign-in button for this provider" })}</Label>
          <HeroInput
            placeholder={intl.formatMessage({ id: "web.routes.admin_settings.acme_sso", defaultMessage: "Acme SSO", description: "Admin settings: grey example text inside the display name field, shown only while it is empty" })}
            value={displayName}
            onChange={(e) => setDisplayName(e.target.value)}
          />
        </TextField>
        <TextField>
          <Label className="text-xs">{intl.formatMessage({ id: "web.routes.admin_settings.issuer_url", defaultMessage: "Issuer URL", description: "Admin settings: label of the field for the issuer URL of the identity provider" })}</Label>
          <HeroInput
            placeholder={intl.formatMessage({ id: "web.routes.admin_settings.https_idp_example_com", defaultMessage: "https://idp.example.com", description: "Admin settings: grey example text inside the issuer URL field, shown only while it is empty" })}
            value={issuer}
            onChange={(e) => setIssuer(e.target.value)}
          />
        </TextField>
        <TextField>
          <Label className="text-xs">{intl.formatMessage({ id: "web.routes.admin_settings.client_id", defaultMessage: "Client ID", description: "Admin settings: label of the OAuth client ID field for the provider" })}</Label>
          <HeroInput value={clientID} onChange={(e) => setClientID(e.target.value)} />
        </TextField>
        <TextField>
          <Label className="text-xs">{intl.formatMessage({ id: "web.routes.admin_settings.client_secret", defaultMessage: "Client secret", description: "Admin settings: label of the OAuth client secret field for the provider" })}</Label>
          <HeroInput
            type="password"
            value={clientSecret}
            onChange={(e) => setClientSecret(e.target.value)}
          />
        </TextField>
        <TextField>
          <Label className="text-xs">{intl.formatMessage({ id: "web.routes.admin_settings.scopes_optional", defaultMessage: "Scopes (optional)", description: "Admin settings: label of the optional field for extra OAuth scopes" })}</Label>
          <HeroInput value={scopes} onChange={(e) => setScopes(e.target.value)} />
          <Description className="text-[11px]">{intl.formatMessage({ id: "web.routes.admin_settings.extra_oauth_scopes_beyond", defaultMessage: "Extra OAuth scopes beyond", description: "Admin settings: opening words of the hint under the scopes field, followed by example scope names and a note that scopes are space- or comma-separated" })} <span className="font-mono">{intl.formatMessage({ id: "web.routes.admin_settings.openid_profile_email", defaultMessage: "openid profile email", description: "Admin settings: example default OAuth scope names shown in monospace in the scopes hint (technical term, usually left untranslated)" })}</span> {intl.formatMessage({ id: "web.routes.admin_settings.like", defaultMessage: "— like", description: "Admin settings: the words for example in the scopes hint, introducing the example scope named groups" })} <span className="font-mono">{intl.formatMessage({ id: "web.routes.admin_settings.groups", defaultMessage: "groups", description: "Admin settings: the scope name groups, shown in the scopes hint and as the grey placeholder of the groups claim field (technical term, usually left untranslated)" })}</span>{intl.formatMessage({ id: "web.routes.admin_settings.space_or_comma_separated", defaultMessage: ". Space- or comma-separated.", description: "Admin settings: end of the scopes hint telling the user to separate several scopes with spaces or commas" })}</Description>
        </TextField>
        <TextField>
          <Label className="text-xs">{intl.formatMessage({ id: "web.routes.admin_settings.groups_claim_optional", defaultMessage: "Groups claim (optional)", description: "Admin settings: label of the optional field naming the token claim that carries the user's group memberships" })}</Label>
          <HeroInput
            placeholder={intl.formatMessage({ id: "web.routes.admin_settings.groups", defaultMessage: "groups", description: "Admin settings: the scope name groups, shown in the scopes hint and as the grey placeholder of the groups claim field (technical term, usually left untranslated)" })}
            value={groupsClaim}
            onChange={(e) => setGroupsClaim(e.target.value)}
          />
          <Description className="text-[11px]">{intl.formatMessage({ id: "web.routes.admin_settings.id_token_claim_holding_the_user_s_group", defaultMessage: "ID-token claim holding the user's group memberships; defaults to {groups}.", description: "Admin settings: help text under the groups claim field; {groups} is the default claim name groups" }, { groups: <span className="font-mono">{intl.formatMessage({ id: "web.routes.admin_settings.groups_claim_name", defaultMessage: "groups", description: "Admin settings: the default groups claim name" })}</span> })}
          </Description>
        </TextField>
      </div>
      <div className="space-y-3 border-t border-border pt-3">
        <div>
          <div className="text-sm font-medium">{intl.formatMessage({ id: "web.routes.admin_settings.role_mapping", defaultMessage: "Role mapping", description: "Admin settings: heading of the part of the provider form that maps identity-provider groups to dashboard roles" })}</div>
          <p className="pt-0.5 text-xs text-muted">{intl.formatMessage({ id: "web.routes.admin_settings.map_idp_groups_comma_separated_to", defaultMessage: "Map IdP groups (comma-separated) to dashboard roles. A user's highest matching role wins (admin > operator > viewer), and roles re-sync on every login while any mapping is set.", description: "Admin settings: explanation under the role mapping heading: comma-separated IdP groups map to dashboard roles, the highest match wins and roles resync on each login" })}</p>
        </div>
        <div className="grid gap-3 md:grid-cols-2">
          <div className="flex flex-col gap-3">
            <TextField>
              <Label className="text-xs">{intl.formatMessage({ id: "web.routes.admin_settings.admin_groups", defaultMessage: "Admin groups", description: "Admin settings: label of the field listing the identity-provider groups that map to the admin role" })}</Label>
              <HeroInput
                placeholder={intl.formatMessage({ id: "web.routes.admin_settings.gameplane_admins", defaultMessage: "gameplane-admins", description: "Admin settings: grey example group name inside the admin groups field, shown only while it is empty" })}
                value={adminGroups}
                onChange={(e) => setAdminGroups(e.target.value)}
              />
            </TextField>
            {adminList.length > 0 && <AdminGroupsInlineWarning />}
          </div>
          <TextField>
            <Label className="text-xs">{intl.formatMessage({ id: "web.routes.admin_settings.operator_groups", defaultMessage: "Operator groups", description: "Admin settings: label of the field listing the identity-provider groups that map to the operator role" })}</Label>
            <HeroInput
              placeholder={intl.formatMessage({ id: "web.routes.admin_settings.gameplane_operators", defaultMessage: "gameplane-operators", description: "Admin settings: grey example group name inside the operator groups field, shown only while it is empty" })}
              value={operatorGroups}
              onChange={(e) => setOperatorGroups(e.target.value)}
            />
          </TextField>
          <TextField>
            <Label className="text-xs">{intl.formatMessage({ id: "web.routes.admin_settings.viewer_groups", defaultMessage: "Viewer groups", description: "Admin settings: label of the field listing the identity-provider groups that map to the viewer role" })}</Label>
            <HeroInput
              placeholder={intl.formatMessage({ id: "web.routes.admin_settings.gameplane_viewers", defaultMessage: "gameplane-viewers", description: "Admin settings: grey example group name inside the viewer groups field, shown only while it is empty" })}
              value={viewerGroups}
              onChange={(e) => setViewerGroups(e.target.value)}
            />
          </TextField>
          <TextField>
            <Label className="text-xs">{intl.formatMessage({ id: "web.routes.admin_settings.default_role", defaultMessage: "Default role", description: "Admin settings: label for the default role, the role given to users whose groups match no mapping; also a row label in the Helm provider details" })}</Label>
            <Select
              aria-label={intl.formatMessage({ id: "web.routes.admin_settings.default_role", defaultMessage: "Default role", description: "Admin settings: label for the default role, the role given to users whose groups match no mapping; also a row label in the Helm provider details" })}
              selectedKey={defaultRole}
              onSelectionChange={(key) => setDefaultRole((key ?? "") as AuthDefaultRole | "")}
              isDisabled={!hasMappings}
            >
              <Select.Trigger className="w-full rounded border border-border bg-surface px-3 py-2 text-sm hover:bg-surface/80">
                <Select.Value />
                <Select.Indicator className="ml-auto h-4 w-4" />
              </Select.Trigger>
              <Select.Popover className="rounded border border-border">
                <ListBox aria-label={intl.formatMessage({ id: "web.routes.admin_settings.default_role", defaultMessage: "Default role", description: "Admin settings: label for the default role, the role given to users whose groups match no mapping; also a row label in the Helm provider details" })}>
                  <ListBoxItem id="">{intl.formatMessage({ id: "web.routes.admin_settings.no_mapping", defaultMessage: "(no mapping)", description: "Admin settings: first option in the default role dropdown, meaning no default role is set; keep it short" })}</ListBoxItem>
                  <ListBoxItem id="viewer">{intl.formatMessage({ id: "web.routes.admin_settings.viewer", defaultMessage: "Viewer", description: "Admin settings: option in the default role dropdown for the viewer role; keep it short" })}</ListBoxItem>
                  <ListBoxItem id="operator">{intl.formatMessage({ id: "web.routes.admin_settings.operator", defaultMessage: "Operator", description: "Admin settings: option in the default role dropdown for the operator role; keep it short" })}</ListBoxItem>
                  <ListBoxItem id="admin">{intl.formatMessage({ id: "web.routes.admin_settings.admin", defaultMessage: "Admin", description: "Admin settings: option in the default role dropdown for the admin role; keep it short" })}</ListBoxItem>
                  <ListBoxItem id="deny">{intl.formatMessage({ id: "web.routes.admin_settings.deny_sign_in", defaultMessage: "Deny sign-in", description: "Admin settings: option in the default role dropdown that refuses sign-in when no group matches; keep it short" })}</ListBoxItem>
                </ListBox>
              </Select.Popover>
            </Select>
            <Description className="text-[11px]">{intl.formatMessage({ id: "web.routes.admin_settings.applied_when_no_group_matches_deny_sign", defaultMessage: "Applied when no group matches. “Deny sign-in” refuses logins without a mapped group.", description: "Admin settings: hint under the default role dropdown explaining the default applies when no group matches, and that Deny sign-in refuses such logins" })}</Description>
          </TextField>
        </div>
      </div>
      <div className="flex justify-end gap-2 pt-1">
        <Button variant="ghost" onPress={onClose}>{intl.formatMessage({ id: "web.routes.admin_settings.cancel", defaultMessage: "Cancel", description: "Admin settings: button that closes an add or set form without saving; keep it short" })}</Button>
        <Button variant="primary" isDisabled={!valid} onPress={handleSubmitClick}>{intl.formatMessage({ id: "web.routes.admin_settings.add_provider", defaultMessage: "Add provider", description: "Admin settings: button that opens the form to add an identity provider, and the button in that form that submits it; keep it short" })}</Button>
      </div>

      <ConfirmAdminMappingDialog
        open={confirmingAdmin}
        onOpenChange={(open) => {
          if (!open) {
            setConfirmingAdmin(false);
          }
        }}
        adminGroups={adminList}
        onConfirm={() => {
          setConfirmingAdmin(false);
          submit();
        }}
      />
    </div>
  );
}

function BackupDestSection() {
  const intl = useOptionalIntl();
  const installation = useInstallation();
  const selected = useCurrentCluster();
  const registry = useQuery({
    queryKey: ["clusters"], queryFn: () => Clusters.list(),
    enabled: installation.data?.standalone === true,
  });
  if (installation.isPending) return <LoadingCard message={intl.formatMessage({ id: "web.routes.admin_settings.loading_installation", defaultMessage: "Loading installation…", description: "Admin settings: loading text in the backup destinations area while installation details are fetched" })} />;
  if (installation.isError) return <ErrorCard message={intl.formatMessage({ id: "web.routes.admin_settings.couldn_t_load_installation_capabilities", defaultMessage: "Couldn't load installation capabilities.", description: "Admin settings: error message in the backup destinations area when installation details fail to load; a retry button follows it" })} onRetry={() => void installation.refetch()} />;
  if (!installation.data.standalone) return <ClusterBackupDestSection cluster="local" />;
  const target = registry.data?.items.find((cluster) => cluster.name === selected);
  return <div className="space-y-4">
    <ClusterSelector />
    {registry.isPending ? <LoadingCard message={intl.formatMessage({ id: "web.routes.admin_settings.loading_clusters", defaultMessage: "Loading clusters…", description: "Admin settings: loading text in the backup destinations area while the list of registered clusters loads" })} />
      : registry.isError ? <ErrorCard message={intl.formatMessage({ id: "web.routes.admin_settings.couldn_t_load_registered_clusters", defaultMessage: "Couldn't load registered clusters.", description: "Admin settings: error message in the backup destinations area when the list of registered clusters fails to load" })} onRetry={() => void registry.refetch()} />
      : target ? <ClusterBackupDestSection key={target.name} cluster={target.name} />
      : <SectionCard title={intl.formatMessage({ id: "web.routes.admin_settings.select_a_workload_cluster", defaultMessage: "Select a workload cluster", description: "Admin settings: title of the placeholder card shown when no workload cluster is selected for backup destinations" })} subtitle={intl.formatMessage({ id: "web.routes.admin_settings.backup_destinations_are_stored_on_the", defaultMessage: "Backup destinations are stored on the workload cluster that runs your servers.", description: "Admin settings: subtitle under the select-a-cluster title saying backup destinations live on the workload cluster that runs your servers" })}>
        <p className="text-sm text-muted">{intl.formatMessage({ id: "web.routes.admin_settings.choose_a_registered_cluster_above_or", defaultMessage: "Choose a registered cluster above, or register one on the Clusters page.", description: "Admin settings: text in the select-a-cluster card asking the user to pick a cluster above or register one on the Clusters page" })}</p>
      </SectionCard>}
  </div>;
}

function ClusterBackupDestSection({ cluster }: { cluster: string }) {
  const intl = useOptionalIntl();
  const qc = useQueryClient();
  const { BackupDestinations } = createResourceClient({ cluster });
  const [adding, setAdding] = useState(false);
  const [deleting, setDeleting] = useState<string | null>(null);

  const list = useQuery({
    queryKey: ["backup-destinations", cluster],
    queryFn: () => BackupDestinations.list(),
  });
  const remove = useMutation({
    mutationFn: (name: string) => BackupDestinations.remove(name),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["backup-destinations", cluster] });
      setDeleting(null);
    },
  });

  const items = list.data?.items ?? [];

  return (
    <SectionCard
      title={intl.formatMessage({ id: "web.routes.admin_settings.backup_destinations", defaultMessage: "Backup destinations", description: "Admin settings: title of the backup destinations section card for one cluster; keep it short" })}
      subtitle={intl.formatMessage({ id: "web.routes.admin_settings.restic_repositories_for_snapshots", defaultMessage: "Restic repositories for snapshots. Stored as labelled Kubernetes Secrets in the configured namespace.", description: "Admin settings: subtitle under Backup destinations: Restic repositories for snapshots, stored as labelled Kubernetes Secrets (Restic: technical term, usually left untranslated)" })}
      footer={
        <Button onPress={() => setAdding(true)} isDisabled={adding}>
          <Plus className="h-4 w-4" />{intl.formatMessage({ id: "web.routes.admin_settings.add_destination", defaultMessage: "Add destination", description: "Admin settings: button that opens the form to add a backup destination, and the heading of that form; keep it short" })}</Button>
      }
    >
      {list.isLoading && <div className="text-sm text-muted">{intl.formatMessage({ id: "web.routes.admin_settings.loading_destinations", defaultMessage: "Loading destinations…", description: "Admin settings: loading text in the backup destinations card while the list is fetched" })}</div>}
      {list.isError && <ErrorBanner err={list.error} />}
      {!list.isLoading && items.length === 0 && !adding && (
        <div className="text-sm text-muted">{intl.formatMessage({ id: "web.routes.admin_settings.no_backup_destinations_configured_add", defaultMessage: "No backup destinations configured. Add one to enable snapshots.", description: "Admin settings: empty-state text in the backup destinations card when none exist, asking the user to add one to enable snapshots" })}</div>
      )}
      {adding && <NewDestinationForm cluster={cluster} onClose={() => setAdding(false)} />}
      <ul className="divide-y divide-border">
        {items.map((d) => (
          <li key={d.name} className="flex items-center gap-3 py-3">
            <div className="flex h-8 w-8 items-center justify-center rounded-md bg-surface">
              <Archive className="h-4 w-4 text-muted" />
            </div>
            <div className="min-w-0 flex-1">
              <div className="text-sm">{d.name}</div>
              <div className="truncate font-mono text-xs text-muted">{d.url}</div>
            </div>
            <span className="rounded bg-success/15 px-2 py-0.5 text-[10px] font-mono uppercase text-success">
              {d.hasPassword ? intl.formatMessage({ id: "web.routes.admin_settings.configured_lower", defaultMessage: "configured", description: "Admin settings: badge on a backup destination whose password is stored" }) : intl.formatMessage({ id: "web.routes.admin_settings.no_password", defaultMessage: "no-password", description: "Admin settings: badge on a backup destination with no stored password" })}
            </span>
            {d.createdAt && (
              <span className="text-[11px] text-muted">{formatRelative(d.createdAt, intl)}</span>
            )}
            <button
              type="button"
              aria-label={intl.formatMessage({ id: "web.routes.admin_settings.delete_destination", defaultMessage: "Delete {name}", description: "Admin settings: aria-label of the button that deletes a backup destination; {name} is the destination name" }, { name: d.name })}
              onClick={() => setDeleting(d.name)}
              className="rounded p-1 text-muted hover:bg-surface/60 hover:text-danger"
            >
              <Trash2 className="h-4 w-4" />
            </button>
          </li>
        ))}
      </ul>
      {remove.error && <ErrorBanner err={remove.error} />}
      <ConfirmDialog
        open={deleting !== null}
        onOpenChange={(open) => { if (!open) setDeleting(null); }}
        title={intl.formatMessage({ id: "web.routes.admin_settings.delete_backup_destination", defaultMessage: "Delete backup destination?", description: "Admin settings: title of the confirmation dialog that asks whether to delete a backup destination" })}
        description={
          <>
            <p>{intl.formatMessage({ id: "web.routes.admin_settings.the_repository_at_this_destination_will", defaultMessage: "The repository at this destination will", description: "Admin settings: opening words of the delete dialog text, followed by the emphasised word not and the rest of the sentence" })} <strong>{intl.formatMessage({ id: "web.routes.admin_settings.not", defaultMessage: "not", description: "Admin settings: the word not, shown in bold in the delete dialog text to stress the repository is not erased" })}</strong> {intl.formatMessage({ id: "web.routes.admin_settings.be_erased_only_the_credentials", defaultMessage: "be erased — only the credentials Gameplane uses to reach it. Existing backups remain intact; new backups against this destination will fail until it's re-added.", description: "Admin settings: end of the delete dialog text: only Gameplane's credentials for the destination are removed, existing backups stay and new backups fail" })}</p>
            {deleting && (
              <p className="pt-2">{intl.formatMessage({ id: "web.routes.admin_settings.type", defaultMessage: "Type", description: "Admin settings: first word of the line in the delete dialog asking the user to type the destination name to confirm" })} <span className="font-mono">{deleting}</span> {intl.formatMessage({ id: "web.routes.admin_settings.to_confirm", defaultMessage: "to confirm.", description: "Admin settings: last words of the line asking the user to type the destination name to confirm; the name is shown in monospace before them" })}</p>
            )}
          </>
        }
        confirmPhrase={deleting ?? undefined}
        confirmLabel={intl.formatMessage({ id: "web.routes.admin_settings.delete", defaultMessage: "Delete", description: "Admin settings: confirm button of the delete backup destination dialog; keep it short" })}
        destructive
        busy={remove.isPending}
        onConfirm={() => deleting && remove.mutate(deleting)}
      />
    </SectionCard>
  );
}

function NewDestinationForm({ cluster, onClose }: { cluster: string; onClose: () => void }) {
  const intl = useOptionalIntl();
  const qc = useQueryClient();
  const { BackupDestinations } = createResourceClient({ cluster });
  const [form, setForm] = useState({ name: "", url: "", password: "" });
  const create = useMutation({
    mutationFn: () => BackupDestinations.upsert(form),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["backup-destinations", cluster] });
      onClose();
    },
  });
  const valid =
    /^[a-z0-9]([-a-z0-9]{0,61}[a-z0-9])?$/.test(form.name) &&
    form.url.length > 0 &&
    form.password.length > 0;
  return (
    <div className="space-y-3 rounded-md border border-border bg-surface/30 p-4">
      <div className="text-sm font-medium">{intl.formatMessage({ id: "web.routes.admin_settings.add_destination", defaultMessage: "Add destination", description: "Admin settings: button that opens the form to add a backup destination, and the heading of that form; keep it short" })}</div>
      <p className="text-xs text-muted">{intl.formatMessage({ id: "web.routes.admin_settings.restic_url_hint", defaultMessage: "Stored as a Secret labelled {secret}. Restic URL formats: {s3}, {b2}, {azure}, etc.", description: "Admin settings: hint under the backup destination form; {secret} is the Secret label, {s3}, {b2} and {azure} are example Restic URL formats" }, { secret: <span className="font-mono">{intl.formatMessage({ id: "web.routes.admin_settings.gameplane_local_backup_destination_true", defaultMessage: "gameplane.local/backup-destination=true", description: "Admin settings: the Secret label example in the backup destination hint" })}</span>, s3: <span className="font-mono">{intl.formatMessage({ id: "web.routes.admin_settings.s3_host_bucket", defaultMessage: "s3:host/bucket", description: "Admin settings: example Restic URL format for S3" })}</span>, b2: <span className="font-mono">{intl.formatMessage({ id: "web.routes.admin_settings.b2_bucket", defaultMessage: "b2:bucket", description: "Admin settings: example Restic URL format for Backblaze B2" })}</span>, azure: <span className="font-mono">{intl.formatMessage({ id: "web.routes.admin_settings.azure_account_container", defaultMessage: "azure:account/container", description: "Admin settings: example Restic URL format for Azure" })}</span> })}</p>
      <div className="grid gap-3 md:grid-cols-2">
        <Field label={intl.formatMessage({ id: "web.routes.admin_settings.name_dns_label", defaultMessage: "Name (DNS label)", description: "Admin settings: label of the destination name field, which must be a DNS label of lowercase letters, digits and dashes (DNS label: technical term, usually left untranslated)" })}>
          <HeroInput
            placeholder={intl.formatMessage({ id: "web.routes.admin_settings.gameplane_backup_repo", defaultMessage: "gameplane-backup-repo", description: "Admin settings: grey example name inside the destination name field, shown only while it is empty" })}
            value={form.name}
            onChange={(e) => setForm({ ...form, name: e.target.value })}
          />
        </Field>
        <Field label={intl.formatMessage({ id: "web.routes.admin_settings.restic_url", defaultMessage: "Restic URL", description: "Admin settings: label of the field for the Restic repository URL (Restic: technical term, usually left untranslated)" })}>
          <HeroInput
            placeholder={intl.formatMessage({ id: "web.routes.admin_settings.s3_s3_example_com_gameplane_bucket", defaultMessage: "s3:s3.example.com/gameplane-bucket", description: "Admin settings: grey example Restic URL inside the URL field, pointing at an S3 bucket (S3: technical term, usually left untranslated)" })}
            value={form.url}
            onChange={(e) => setForm({ ...form, url: e.target.value })}
          />
        </Field>
        <Field label={intl.formatMessage({ id: "web.routes.admin_settings.repository_password", defaultMessage: "Repository password", description: "Admin settings: label of the password field for the Restic backup repository" })}>
          <HeroInput
            type="password"
            placeholder={intl.formatMessage({ id: "web.routes.admin_settings.strong_unique_passphrase", defaultMessage: "Strong, unique passphrase", description: "Admin settings: grey hint inside the repository password field, suggesting a strong unique passphrase; shown only while it is empty" })}
            value={form.password}
            onChange={(e) => setForm({ ...form, password: e.target.value })}
          />
        </Field>
      </div>
      {create.error && <ErrorBanner err={create.error} />}
      <div className="flex justify-end gap-2 pt-1">
        <Button variant="ghost" onPress={onClose}>{intl.formatMessage({ id: "web.routes.admin_settings.cancel", defaultMessage: "Cancel", description: "Admin settings: button that closes an add or set form without saving; keep it short" })}</Button>
        <Button
          onPress={() => create.mutate()}
          isDisabled={!valid || create.isPending}
        >
          {create.isPending ? intl.formatMessage({ id: "web.routes.admin_settings.saving_destination", defaultMessage: "Saving…", description: "Admin settings: button label while a backup destination is being saved" }) : intl.formatMessage({ id: "web.routes.admin_settings.save_destination", defaultMessage: "Save destination", description: "Admin settings: button label that saves a new backup destination" })}
        </Button>
      </div>
    </div>
  );
}

const defaultModRegistries: ModRegistriesCfg = { registries: [] };

// The managed-Secret name the API derives for a provider's key (mirrors
// registryKeySecretPrefix / DefaultKeySecretName in api/internal/registry/keys.go).
const registryKeySecretPrefix = "gameplane-modreg-";

// Keyed providers only: curseforge, steam, and nexus need an admin-set API
// key and stay hidden from the Mods browser until one exists (mirrors
// registry.KeyedProviders server-side). The icon is purely decorative, one
// per provider, distinct from the section's own nav icon (Key).
const keyedRegistryProviders: Array<{ provider: KeyedRegistryProvider; label: string; icon: typeof Cog }> = [
  { provider: "curseforge", label: "CurseForge", icon: Flame },
  { provider: "steam", label: "Steam Workshop", icon: Gamepad2 },
  { provider: "nexus", label: "Nexus Mods", icon: Puzzle },
];

function SetRegistryKeyForm({
  provider,
  label,
  replacing,
  onSaved,
  onClose,
}: {
  provider: KeyedRegistryProvider;
  label: string;
  replacing: boolean;
  onSaved: (entry: ModRegistryEntry, apiKey: string) => void;
  onClose: () => void;
}) {
  const intl = useOptionalIntl();
  const [apiKey, setApiKey] = useState("");

  // The key is written by the section's Save, just before the registries
  // row that references it; nothing is stored before then. The key itself
  // is never displayed — this input only ever writes, and the server never
  // echoes the value.
  const submit = () => {
    onSaved({ provider, configRef: registryKeySecretPrefix + provider }, apiKey);
    onClose();
  };

  return (
    <div className="space-y-3 rounded-md border border-border bg-surface/30 p-4">
      <div className="text-sm font-medium">
        {replacing ? intl.formatMessage({ id: "web.routes.admin_settings.replace_verb", defaultMessage: "Replace", description: "Admin settings: verb on the button that replaces a stored API key" }) : intl.formatMessage({ id: "web.routes.admin_settings.set_verb", defaultMessage: "Set", description: "Admin settings: verb on the button that sets an API key for the first time" })} {intl.formatMessage({ id: "web.routes.admin_settings.api_key_label", defaultMessage: "API key — {label}", description: "Admin settings: sentence with label in div" }, { label: label })}</div>
      <Field label={intl.formatMessage({ id: "web.routes.admin_settings.api_key", defaultMessage: "API key", description: "Admin settings: label of the API key field in the dialog that sets the key for a module registry" })}>
        <HeroInput
          type="password"
          autoFocus
          value={apiKey}
          onChange={(e) => setApiKey(e.target.value)}
          spellCheck={false}
        />
      </Field>
      <div className="flex justify-end gap-2 pt-1">
        <Button variant="ghost" onPress={onClose}>{intl.formatMessage({ id: "web.routes.admin_settings.cancel", defaultMessage: "Cancel", description: "Admin settings: button that closes an add or set form without saving; keep it short" })}</Button>
        <Button isDisabled={apiKey.trim() === ""} onPress={submit}>{intl.formatMessage({ id: "web.routes.admin_settings.save", defaultMessage: "Save", description: "Admin settings: button that saves the module registry API key; keep it short" })}</Button>
      </div>
    </div>
  );
}

function ModRegistriesSection({ initial }: { initial?: ModRegistriesCfg }) {
  const intl = useOptionalIntl();
  const f = useSectionForm<ModRegistriesCfg>(initial ?? defaultModRegistries, "modRegistries");
  const [editing, setEditing] = useState<KeyedRegistryProvider | null>(null);

  const entryFor = (p: KeyedRegistryProvider) => f.draft.registries.find((r) => r.provider === p);

  const removeEntry = (provider: KeyedRegistryProvider) => {
    const entry = entryFor(provider);
    // The API-managed key Secret is removed only by the Save that drops
    // this row; the server refuses Secrets it didn't create.
    if (!entry?.configRef || entry.configRef === registryKeySecretPrefix + provider) {
      f.stageSecret(`registry:${provider}`, {
        kind: "remove",
        run: () => ModRegistries.deleteSecret(provider),
      });
    }
    f.replace({ ...f.draft, registries: f.draft.registries.filter((r) => r.provider !== provider) });
  };

  return (
    <SectionCard
      title={intl.formatMessage({ id: "web.routes.admin_settings.mod_registries", defaultMessage: "Mod registries", description: "Admin settings: title of the Mod registries card that lists mod registries which may need an API key; keep it short" })}
      subtitle={intl.formatMessage({ id: "web.routes.admin_settings.registries_that_require_an_api_key_stay", defaultMessage: "Registries that require an API key stay hidden from the Mods browser until a key is configured.", description: "Admin settings: subtitle under Mod registries: registries that need an API key stay hidden from the Mods browser until a key is set" })}
      footer={
        <>
          <SaveStatus pending={f.pending} error={f.error} saved={f.saved} />
          <Button onPress={f.save} isDisabled={f.pending}>{intl.formatMessage({ id: "web.routes.admin_settings.save_changes", defaultMessage: "Save changes", description: "Admin settings: button that saves the changes in this section; keep it short" })}</Button>
        </>
      }
    >
      <ul className="divide-y divide-border">
        {keyedRegistryProviders.map(({ provider, label, icon: Icon }) => {
          const entry = entryFor(provider);
          const configured = Boolean(entry);
          return (
            <li key={provider} className="flex items-center gap-3 py-3">
              <div className="flex h-8 w-8 shrink-0 items-center justify-center rounded-md bg-surface">
                <Icon className="h-4 w-4 text-muted" />
              </div>
              <div className="min-w-0 flex-1">
                <div className="text-sm">{label}</div>
                <div className="truncate text-xs text-muted">
                  {configured ? intl.formatMessage({ id: "web.routes.admin_settings.active_in_mods_browser", defaultMessage: "Active in the Mods browser", description: "Admin settings: registry status when a key is configured and the provider shows in the Mods browser" }) : intl.formatMessage({ id: "web.routes.admin_settings.hidden_from_mods_browser", defaultMessage: "Hidden from the Mods browser", description: "Admin settings: registry status when no key is configured and the provider is hidden from the Mods browser" })}
                </div>
              </div>
              <span
                className={cn(
                  "flex shrink-0 items-center gap-1.5 rounded-full px-2.5 py-1 text-xs",
                  configured ? "bg-primary/15 text-primary" : "bg-muted/15 text-muted",
                )}
              >
                <span className="h-1.5 w-1.5 rounded-full bg-current" />
                {configured ? intl.formatMessage({ id: "web.routes.admin_settings.configured", defaultMessage: "Configured", description: "Admin settings: badge on a mod registry whose API key is set" }) : intl.formatMessage({ id: "web.routes.admin_settings.not_configured", defaultMessage: "Not configured", description: "Admin settings: badge on a mod registry whose API key is not set" })}
              </span>
              {editing !== provider && (
                configured ? (
                  <>
                    <Button variant="primary" size="sm" onPress={() => setEditing(provider)}>{intl.formatMessage({ id: "web.routes.admin_settings.replace", defaultMessage: "Replace", description: "Admin settings: button on a configured module registry that opens the dialog to replace its API key; keep it short" })}</Button>
                    <Button variant="danger" size="sm" onPress={() => removeEntry(provider)}>{intl.formatMessage({ id: "web.routes.admin_settings.remove", defaultMessage: "Remove", description: "Admin settings: button that removes the API key entry of a module registry; keep it short" })}</Button>
                  </>
                ) : (
                  <Button variant="primary" size="sm" onPress={() => setEditing(provider)}>{intl.formatMessage({ id: "web.routes.admin_settings.set_api_key", defaultMessage: "Set API key", description: "Admin settings: button on an unconfigured module registry that opens the dialog to add its API key; keep it short" })}</Button>
                )
              )}
            </li>
          );
        })}
      </ul>
      {editing && (
        <SetRegistryKeyForm
          provider={editing}
          label={keyedRegistryProviders.find((p) => p.provider === editing)?.label ?? editing}
          replacing={Boolean(entryFor(editing))}
          onSaved={(entry, apiKey) => {
            f.stageSecret(`registry:${entry.provider}`, {
              kind: "write",
              run: () => ModRegistries.putSecret(entry.provider, apiKey),
            });
            f.replace({
              ...f.draft,
              registries: [...f.draft.registries.filter((r) => r.provider !== entry.provider), entry],
            });
          }}
          onClose={() => setEditing(null)}
        />
      )}
      <p className="text-xs text-muted">{intl.formatMessage({ id: "web.routes.admin_settings.always_available_modrinth_thunderstore", defaultMessage: "Always available: Modrinth, Thunderstore, Hangar, Factorio, Spigot, GitHub, uMod", description: "Admin settings: note under the registry list naming the registries that need no key; the names are product names, usually left untranslated" })}</p>
    </SectionCard>
  );
}

const defaultNotif: NotificationsCfg = { sinks: [] };

const allNotifEvents: NotifEventType[] = [
  "server.unhealthy",
  "server.recovered",
  "backup.failed",
  "backup.succeeded",
  "restore.failed",
  "restore.succeeded",
];

// Mirrors notify.DefaultOn server-side: failures plus the paired recovery.
const defaultOnEvents: NotifEventType[] = [
  "server.unhealthy",
  "server.recovered",
  "backup.failed",
  "restore.failed",
];

// SlackIcon (a plain function component) is mixed in alongside lucide-react's
// forwardRef-based icons; the only prop this map's consumer relies on is
// `className`, so type the map to that shared contract rather than the full
// LucideIcon (ForwardRefExoticComponent) shape.
const sinkIcons: Record<SinkKind, ComponentType<{ className?: string }>> = {
  discord: MessagesSquare,
  slack: SlackIcon,
  smtp: Mail,
  webhook: Webhook,
  ntfy: BellRing,
};

function EventChip({ label }: { label: string }) {
  return (
    <span className="rounded-full bg-surface px-2 py-0.5 text-[11px] text-muted">
      {label}
    </span>
  );
}

function EventChips({ events }: { events?: NotifEventType[] }) {
  const intl = useOptionalIntl();
  if (!events || events.length === 0) return <EventChip label={intl.formatMessage({ id: "web.routes.admin_settings.default_events", defaultMessage: "default events", description: "Admin settings: badge on a notification sink with no events selected, meaning the default event set applies; keep it short" })} />;
  return (
    <>
      {events.slice(0, 2).map((e) => (
        <EventChip key={e} label={e} />
      ))}
      {events.length > 2 && <EventChip label={intl.formatMessage({ id: "web.routes.admin_settings.more_events_count", defaultMessage: "+{count}", description: "Admin settings: chip showing how many more events a notification sink has beyond the two shown; {count} is that number" }, { count: String(events.length - 2) })} />}
    </>
  );
}

// The managed-Secret name the API derives for a sink (mirrors
// sinkSecretPrefix in api/internal/handlers/notifications.go). The
// configRef must stay a DNS label (≤63), which caps the sink name.
// Sink kinds are API values; they are shown through these messages and an
// unknown kind falls back to the raw value.
function sinkKindLabel(intl: IntlShape, kind: string): string {
  switch (kind) {
    case "discord":
      return intl.formatMessage({ id: "web.routes.admin_settings.sink_kind_discord", defaultMessage: "Discord", description: "Admin settings: name of the Discord notification sink kind" });
    case "slack":
      return intl.formatMessage({ id: "web.routes.admin_settings.sink_kind_slack", defaultMessage: "Slack", description: "Admin settings: name of the Slack notification sink kind" });
    case "smtp":
      return intl.formatMessage({ id: "web.routes.admin_settings.sink_kind_smtp", defaultMessage: "SMTP", description: "Admin settings: name of the SMTP email notification sink kind" });
    case "webhook":
      return intl.formatMessage({ id: "web.routes.admin_settings.sink_kind_webhook", defaultMessage: "Webhook", description: "Admin settings: name of the generic webhook notification sink kind" });
    case "ntfy":
      return intl.formatMessage({ id: "web.routes.admin_settings.sink_kind_ntfy", defaultMessage: "ntfy", description: "Admin settings: name of the ntfy notification sink kind; ntfy is a product name" });
    default:
      return kind;
  }
}

// Example URLs shown as placeholders; they are not translatable text.
const SINK_URL_EXAMPLES: Record<SinkKind, string> = {
  discord: "https://discord.com/api/webhooks/…",
  slack: "https://hooks.slack.com/services/…",
  ntfy: "https://ntfy.sh/my-topic",
  webhook: "https://example.com/hook",
  smtp: "https://example.com/hook",
};

const sinkSecretPrefix = "gameplane-notify-";
const maxSinkName = 63 - sinkSecretPrefix.length;

// Option key for "Instance default" in the sink language selects. A sentinel
// instead of "" because react-aria collections handle empty keys poorly.
const instanceDefaultKey = "__default__";

function AddSinkForm({
  existing,
  onAdd,
  onClose,
}: {
  existing: string[];
  onAdd: (s: NotifSink, credentials: SinkSecretBody) => void;
  onClose: () => void;
}) {
  const [name, setName] = useState("");
  const [kind, setKind] = useState<SinkKind>("discord");
  const [events, setEvents] = useState<NotifEventType[]>(defaultOnEvents);
  // instanceDefaultKey is the Instance default: the sink is then sent without a language key.
  const [language, setLanguage] = useState(instanceDefaultKey);
  const intl = useOptionalIntl();
  const languages = listedLanguages();
  const [creds, setCreds] = useState<Record<string, string>>({ tls: "starttls" });
  const cred = (key: string) => creds[key] ?? "";
  const setCred = (key: string) => (e: ChangeEvent<HTMLInputElement>) => {
    setCreds((c) => ({ ...c, [key]: e.target.value }));
  };

  const dns = /^[a-z0-9]([-a-z0-9]{0,61}[a-z0-9])?$/;
  const nameOk = dns.test(name) && !existing.includes(name) && name.length <= maxSinkName;
  const credsOk =
    kind === "smtp"
      ? cred("host") !== "" && cred("from") !== "" && cred("to") !== ""
      : /^https?:\/\/.+/.test(cred("url"));
  const toggleEvent = (ev: NotifEventType) =>
    setEvents((cur) => (cur.includes(ev) ? cur.filter((e) => e !== ev) : [...cur, ev]));

  // The credentials are written by the section's Save, just before the
  // sink row that references them; nothing is stored before then.
  const submit = () => {
    const body: SinkSecretBody =
      kind === "smtp"
        ? {
            kind,
            host: cred("host"),
            port: cred("port"),
            username: cred("username"),
            password: cred("password"),
            from: cred("from"),
            to: cred("to"),
            tls: cred("tls"),
          }
        : kind === "ntfy"
          ? { kind, url: cred("url"), token: cred("token") }
          : { kind, url: cred("url"), authorization: cred("authorization") };
    const sink: NotifSink = { name, kind, enabled: true, configRef: sinkSecretPrefix + name, events };
    // Only a chosen language is sent; Instance default omits the key.
    if (language !== instanceDefaultKey) sink.language = language;
    onAdd(sink, body);
    onClose();
  };

  return (
    <div className="space-y-3 rounded-md border border-border bg-surface/30 p-4">
      <div className="text-sm font-medium">{intl.formatMessage({ id: "web.routes.admin_settings.add_sink", defaultMessage: "Add sink", description: "Admin settings: heading of the add-sink form, and the button in its footer that adds the sink; keep it short" })}</div>
      <div className="grid gap-3 md:grid-cols-2">
        <Field label={intl.formatMessage({ id: "web.routes.admin_settings.name", defaultMessage: "Name", description: "Admin settings: label of the name field, a short identifier for the provider or sink; the same label is used in the add-sink form" })}>
          <HeroInput
            placeholder={intl.formatMessage({ id: "web.routes.admin_settings.team_alerts", defaultMessage: "team-alerts", description: "Admin settings: grey example sink name inside the name field of the add-sink form, shown only while it is empty" })}
            value={name}
            onChange={(e) => setName(e.target.value)}
          />
          <span className="text-[11px] text-muted">{intl.formatMessage({ id: "web.routes.admin_settings.sink_name_hint", defaultMessage: "A short lowercase identifier (letters, digits, and dashes) used to name the sink and its Secret — like {example}.", description: "Admin settings: hint under the sink name field; {example} is an example sink name" }, { example: <span className="font-mono">{intl.formatMessage({ id: "web.routes.admin_settings.sink_name_example", defaultMessage: "team-alerts", description: "Admin settings: example sink name in the sink name hint" })}</span> })}
          </span>
        </Field>
        <Field label={intl.formatMessage({ id: "web.routes.admin_settings.kind", defaultMessage: "Kind", description: "Admin settings: label of the dropdown that chooses the provider kind, such as generic OIDC, Google or GitHub" })}>
          <Select aria-label={intl.formatMessage({ id: "web.routes.admin_settings.sink_kind", defaultMessage: "Sink kind", description: "Admin settings: accessible name of the dropdown that picks the notification sink type, such as Discord, Slack, ntfy, email or webhook" })} selectedKey={kind} onSelectionChange={(key) => setKind(key as SinkKind)}>
            <Select.Trigger className="w-full rounded border border-border bg-surface px-3 py-2 text-sm hover:bg-surface/80">
              <Select.Value />
              <Select.Indicator className="ml-auto h-4 w-4" />
            </Select.Trigger>
            <Select.Popover className="rounded border border-border">
              <ListBox aria-label={intl.formatMessage({ id: "web.routes.admin_settings.sink_kind", defaultMessage: "Sink kind", description: "Admin settings: accessible name of the dropdown that picks the notification sink type, such as Discord, Slack, ntfy, email or webhook" })}>
                {(["discord", "slack", "smtp", "webhook", "ntfy"] as const).map((k) => (
                  <ListBoxItem key={k} id={k}>
                    {sinkKindLabel(intl, k)}
                  </ListBoxItem>
                ))}
              </ListBox>
            </Select.Popover>
          </Select>
        </Field>
        <Field
          label={intl.formatMessage({
            id: "web.admin_settings.sink.language_label",
            defaultMessage: "Language",
            description: "Label of the language select in the Add sink form on the admin Notifications card",
          })}
          hint={intl.formatMessage({
            id: "web.admin_settings.sink.language_hint",
            defaultMessage: "Notifications sent to this sink are written in this language.",
            description: "Helper text under the language select in the Add sink form on the admin Notifications card",
          })}
        >
          <Select
            aria-label={intl.formatMessage({
              id: "web.admin_settings.sink.language_select",
              defaultMessage: "Sink language",
              description: "Accessible name of the language select in the Add sink form on the admin Notifications card",
            })}
            selectedKey={language}
            onSelectionChange={(key) => setLanguage(key === null ? instanceDefaultKey : String(key))}
          >
            <Select.Trigger className="w-full rounded border border-border bg-surface px-3 py-2 text-sm hover:bg-surface/80">
              <Select.Value />
              <Select.Indicator className="ml-auto h-4 w-4" />
            </Select.Trigger>
            <Select.Popover className="rounded border border-border">
              <ListBox aria-label={intl.formatMessage({
                id: "web.admin_settings.sink.language_select",
                defaultMessage: "Sink language",
                description: "Accessible name of the language select in the Add sink form on the admin Notifications card",
              })}>
                <ListBoxItem id={instanceDefaultKey}>
                  {intl.formatMessage({
                    id: "web.admin_settings.language.instance_default",
                    defaultMessage: "Instance default",
                    description: "Option that makes a notification sink use the instance default language",
                  })}
                </ListBoxItem>
                {languages.map((lang) => (
                  <ListBoxItem key={lang.tag} id={lang.tag}>
                    {lang.nativeName}
                  </ListBoxItem>
                ))}
              </ListBox>
            </Select.Popover>
          </Select>
        </Field>
        {kind === "smtp" ? (
          <>
            <Field label={intl.formatMessage({ id: "web.routes.admin_settings.smtp_host", defaultMessage: "SMTP host", description: "Admin settings: label of the SMTP host field for the email notification sink" })}>
              <HeroInput placeholder={intl.formatMessage({ id: "web.routes.admin_settings.mail_example_com", defaultMessage: "mail.example.com", description: "Admin settings: grey example host name inside the SMTP host field, shown only while it is empty" })} value={cred("host")} onChange={setCred("host")} />
            </Field>
            <Field label={intl.formatMessage({ id: "web.routes.admin_settings.port_default_587", defaultMessage: "Port (default 587)", description: "Admin settings: label of the SMTP port field, which states 587 is the default port" })}>
              <HeroInput placeholder={String(587)} value={cred("port")} onChange={setCred("port")} />
            </Field>
            <Field label={intl.formatMessage({ id: "web.routes.admin_settings.username_optional", defaultMessage: "Username (optional)", description: "Admin settings: label of the optional SMTP username field" })}>
              <HeroInput value={cred("username")} onChange={setCred("username")} />
            </Field>
            <Field label={intl.formatMessage({ id: "web.routes.admin_settings.password_optional", defaultMessage: "Password (optional)", description: "Admin settings: label of the optional SMTP password field" })}>
              <HeroInput type="password" value={cred("password")} onChange={setCred("password")} />
            </Field>
            <Field label={intl.formatMessage({ id: "web.routes.admin_settings.from_address", defaultMessage: "From address", description: "Admin settings: label of the field for the sender address of email notifications" })}>
              <HeroInput placeholder={intl.formatMessage({ id: "web.routes.admin_settings.gameplane_example_com", defaultMessage: "gameplane@example.com", description: "Admin settings: grey example sender address inside the from address field, shown only while it is empty" })} value={cred("from")} onChange={setCred("from")} />
            </Field>
            <Field label={intl.formatMessage({ id: "web.routes.admin_settings.to_comma_separated", defaultMessage: "To (comma-separated)", description: "Admin settings: label of the field listing recipient addresses, separated by commas" })}>
              <HeroInput placeholder={intl.formatMessage({ id: "web.routes.admin_settings.ops_example_com", defaultMessage: "ops@example.com", description: "Admin settings: grey example recipient addresses inside the recipients field, shown only while it is empty" })} value={cred("to")} onChange={setCred("to")} />
            </Field>
            <Field label={intl.formatMessage({ id: "web.routes.admin_settings.tls", defaultMessage: "TLS", description: "Admin settings: label of the dropdown that sets how the SMTP connection uses TLS; TLS is a technical term, usually left untranslated" })}>
              <Select
                aria-label={intl.formatMessage({ id: "web.routes.admin_settings.smtp_tls_mode", defaultMessage: "SMTP TLS mode", description: "Admin settings: accessible name of the SMTP TLS mode dropdown; its options are STARTTLS, implicit TLS and none" })}
                selectedKey={cred("tls")}
                onSelectionChange={(key) => setCreds((c) => ({ ...c, tls: String(key ?? "") }))}
              >
                <Select.Trigger className="w-full rounded border border-border bg-surface px-3 py-2 text-sm hover:bg-surface/80">
                  <Select.Value />
                  <Select.Indicator className="ml-auto h-4 w-4" />
                </Select.Trigger>
                <Select.Popover className="rounded border border-border">
                  <ListBox aria-label={intl.formatMessage({ id: "web.routes.admin_settings.smtp_tls_mode", defaultMessage: "SMTP TLS mode", description: "Admin settings: accessible name of the SMTP TLS mode dropdown; its options are STARTTLS, implicit TLS and none" })}>
                    {(["starttls", "implicit", "none"] as const).map((m) => (
                      <ListBoxItem key={m} id={m}>
                        {m}
                      </ListBoxItem>
                    ))}
                  </ListBox>
                </Select.Popover>
              </Select>
            </Field>
          </>
        ) : (
          <>
            <Field label={kind === "ntfy" ? intl.formatMessage({ id: "web.routes.admin_settings.topic_url", defaultMessage: "Topic URL", description: "Admin settings: label of the URL field for an ntfy sink" }) : intl.formatMessage({ id: "web.routes.admin_settings.webhook_url", defaultMessage: "Webhook URL", description: "Admin settings: label of the URL field for a webhook-style sink" })}>
              <HeroInput
                placeholder={SINK_URL_EXAMPLES[kind]}
                value={cred("url")}
                onChange={setCred("url")}
              />
            </Field>
            {kind === "ntfy" && (
              <Field label={intl.formatMessage({ id: "web.routes.admin_settings.access_token_optional", defaultMessage: "Access token (optional)", description: "Admin settings: label of the optional access token field for an ntfy notification sink" })}>
                <HeroInput type="password" placeholder={intl.formatMessage({ id: "web.routes.admin_settings.tk", defaultMessage: "tk_…", description: "Admin settings: grey example token format inside the access token field, shown only while it is empty" })} value={cred("token")} onChange={setCred("token")} />
              </Field>
            )}
            {kind === "webhook" && (
              <Field label={intl.formatMessage({ id: "web.routes.admin_settings.authorization_header_optional", defaultMessage: "Authorization header (optional)", description: "Admin settings: label of the optional Authorization header field for a webhook notification sink" })}>
                <HeroInput type="password" placeholder={intl.formatMessage({ id: "web.routes.admin_settings.bearer", defaultMessage: "Bearer …", description: "Admin settings: grey example value inside the Authorization header field, showing the Bearer prefix; shown only while it is empty" })} value={cred("authorization")} onChange={setCred("authorization")} />
              </Field>
            )}
          </>
        )}
      </div>
      <div className="space-y-1.5">
        <div className="text-xs text-muted">{intl.formatMessage({ id: "web.routes.admin_settings.events_failures_and_recovery_are_pre", defaultMessage: "Events (failures and recovery are pre-selected)", description: "Admin settings: heading above the event checkboxes of a notification sink; failures and recovery are selected by default" })}</div>
        <div className="grid gap-1.5 sm:grid-cols-2">
          {allNotifEvents.map((ev) => (
            <label key={ev} className="flex items-center gap-2 text-xs text-fg">
              <input
                type="checkbox"
                className="accent-primary"
                checked={events.includes(ev)}
                onChange={() => toggleEvent(ev)}
              />
              <span className="font-mono">{ev}</span>
            </label>
          ))}
        </div>
      </div>
      <div className="flex justify-end gap-2 pt-1">
        <Button variant="ghost" onPress={onClose}>{intl.formatMessage({ id: "web.routes.admin_settings.cancel", defaultMessage: "Cancel", description: "Admin settings: button that closes an add or set form without saving; keep it short" })}</Button>
        <Button isDisabled={!nameOk || !credsOk} onPress={submit}>{intl.formatMessage({ id: "web.routes.admin_settings.add_sink", defaultMessage: "Add sink", description: "Admin settings: heading of the add-sink form, and the button in its footer that adds the sink; keep it short" })}</Button>
      </div>
    </div>
  );
}

function TestButton({ dirty, disabled, onPress, children }: { dirty: boolean; disabled: boolean; onPress: () => void; children: ReactNode }) {
  const intl = useOptionalIntl();
  const ref = useRef<HTMLButtonElement>(null);
  useEffect(() => {
    if (ref.current) {
      if (dirty) {
        ref.current.setAttribute("title", intl.formatMessage({ id: "web.routes.admin_settings.save_changes_first_tests", defaultMessage: "Save changes first — tests run against the saved config", description: "Admin settings: tooltip on a sink test button while the form has unsaved changes" }));
      } else {
        ref.current.removeAttribute("title");
      }
    }
  }, [dirty, intl]);

  return (
    <Button
      ref={ref}
      variant="outline"
      size="sm"
      isDisabled={disabled}
      onPress={onPress}
    >
      {children}
    </Button>
  );
}

function TelemetrySwitch({ isSelected, onChange, ariaLabel, isDisabled }: { isSelected: boolean; onChange: (value: boolean) => void; ariaLabel: string; isDisabled?: boolean }) {
  const containerRef = useRef<HTMLDivElement>(null);
  useEffect(() => {
    if (containerRef.current) {
      const button = containerRef.current.querySelector('[role="switch"]') as HTMLElement;
      if (button) {
        button.setAttribute("aria-checked", isSelected ? "true" : "false");
      }
    }
  }, [isSelected]);

  return (
    <div ref={containerRef}>
      <Switch
        aria-label={ariaLabel}
        isSelected={isSelected}
        isDisabled={isDisabled}
        onChange={onChange}
      >
        <Switch.Content>
          <Switch.Control>
            <Switch.Thumb />
          </Switch.Control>
        </Switch.Content>
      </Switch>
    </div>
  );
}

function NotificationsSection({ initial }: { initial?: NotificationsCfg }) {
  const f = useSectionForm<NotificationsCfg>(initial ?? defaultNotif, "notifications");
  const [adding, setAdding] = useState(false);
  const [testResults, setTestResults] = useState<
    Record<string, { ok: boolean; message: string }>
  >({});
  const test = useMutation({ mutationFn: (name: string) => Notifications.test(name) });
  // The test endpoint fires against the *persisted* config; with unsaved
  // edits in the draft it would test something other than what's shown.
  const dirty = JSON.stringify(f.draft) !== JSON.stringify(initial ?? defaultNotif);

  const intl = useOptionalIntl();
  const languages = listedLanguages();
  const instanceDefaultLabel = intl.formatMessage({
    id: "web.admin_settings.language.instance_default",
    defaultMessage: "Instance default",
    description: "Option that makes a notification sink use the instance default language",
  });
  const sinkLanguageLabel = intl.formatMessage({
    id: "web.admin_settings.sink.language_text",
    defaultMessage: "Language",
    description: "Label beside the language select of each notification sink row on the admin Notifications card",
  });

  // Sets one sink's language in the draft; Instance default removes the key.
  const setSinkLanguage = (idx: number, tag: string) => {
    const next = f.draft.sinks.map((x, i) => {
      if (i !== idx) return x;
      const copy: NotifSink = { ...x };
      if (tag === instanceDefaultKey) delete copy.language;
      else copy.language = tag;
      return copy;
    });
    f.update({ sinks: next });
  };

  const runTest = (name: string) =>
    test.mutate(name, {
      onSuccess: () =>
        setTestResults((r) => ({ ...r, [name]: { ok: true, message: "delivered" } })),
      onError: (e) =>
        setTestResults((r) => ({
          ...r,
          [name]: { ok: false, message: errorTextIntl(intl, e, intl.formatMessage({ id: "web.routes.admin_settings.delivery_failed", defaultMessage: "delivery failed", description: "Admin settings: fallback error when a notification test delivery fails" })) },
        })),
    });

  return (
    <SectionCard
      title={intl.formatMessage({ id: "web.routes.admin_settings.notifications", defaultMessage: "Notifications", description: "Admin settings: title of the Notifications section card that lists notification sinks; keep it short" })}
      subtitle={intl.formatMessage({ id: "web.routes.admin_settings.deliver_server_health_and_backup", defaultMessage: "Deliver server health and backup/restore events to Discord, Slack, ntfy, email, or webhooks. Credentials are entered when adding a sink and stored as a labelled Secret in the control-plane namespace.", description: "Admin settings: subtitle under Notifications: sinks receive server health and backup events; credentials are entered when adding a sink and stored as a labelled Secret" })}
      footer={
        <>
          <SaveStatus pending={f.pending} error={f.error} saved={f.saved} />
          <Button onPress={f.save} isDisabled={f.pending}>{intl.formatMessage({ id: "web.routes.admin_settings.save_changes", defaultMessage: "Save changes", description: "Admin settings: button that saves the changes in this section; keep it short" })}</Button>
        </>
      }
    >
      {f.draft.sinks.length === 0 && !adding && (
        <div className="text-sm text-muted">{intl.formatMessage({ id: "web.routes.admin_settings.no_notification_sinks_configured", defaultMessage: "No notification sinks configured.", description: "Admin settings: empty-state text in the Notifications section shown when no sink exists" })}</div>
      )}
      <ul className="divide-y divide-border">
        {f.draft.sinks.map((s, idx) => {
          const Icon = sinkIcons[s.kind] ?? Bell;
          const result = testResults[s.name];
          return (
            <li key={s.name} className="flex flex-wrap items-center gap-3 py-3">
              <div className="flex h-8 w-8 shrink-0 items-center justify-center rounded-md bg-surface">
                <Icon className="h-4 w-4 text-muted" />
              </div>
              <div className="min-w-0 flex-1">
                <div className="text-sm">{s.name}</div>
                <div className="truncate text-xs text-muted">
                  {/* The stored kind identifier, not a translated label. */}
                  {s.kind}
                  {s.configRef ? intl.formatMessage({ id: "web.routes.admin_settings.sink_secret_ref", defaultMessage: " · Secret: {ref}", description: "Admin settings: secret reference shown under a notification sink; {ref} is the Secret name" }, { ref: s.configRef }) : intl.formatMessage({ id: "web.routes.admin_settings.sink_no_secret", defaultMessage: " · no Secret configured", description: "Admin settings: shown under a notification sink that has no Secret configured" })}
                </div>
              </div>
              {s.configRef ? (
                <div className="hidden items-center gap-1.5 lg:flex">
                  <EventChips events={s.events} />
                </div>
              ) : (
                <span className="rounded-full bg-warning/15 px-2 py-0.5 text-[11px] font-medium text-warning">{intl.formatMessage({ id: "web.routes.admin_settings.needs_secret", defaultMessage: "Needs secret", description: "Admin settings: warning badge on a notification sink row showing its credentials Secret is missing; keep it short" })}</span>
              )}
              <div className="flex min-w-0 flex-wrap items-center gap-2">
                <span className="text-xs text-muted">{sinkLanguageLabel}</span>
                <Select
                  aria-label={intl.formatMessage(
                    {
                      id: "web.admin_settings.sink.language_for",
                      defaultMessage: "Language for {name}",
                      description: "Accessible name of a notification sink's language select on the admin Notifications card; {name} is the sink name",
                    },
                    { name: s.name },
                  )}
                  className="w-full sm:w-[200px]"
                  selectedKey={s.language ?? instanceDefaultKey}
                  onSelectionChange={(key) => setSinkLanguage(idx, key === null ? instanceDefaultKey : String(key))}
                >
                  <Select.Trigger className="w-full rounded border border-border bg-surface px-3 py-1.5 text-sm hover:bg-surface/80">
                    <Select.Value />
                    <Select.Indicator className="ml-auto h-4 w-4" />
                  </Select.Trigger>
                  <Select.Popover className="rounded border border-border">
                    <ListBox aria-label={sinkLanguageLabel}>
                      <ListBoxItem id={instanceDefaultKey}>
                        {instanceDefaultLabel}
                      </ListBoxItem>
                      {languages.map((lang) => (
                        <ListBoxItem key={lang.tag} id={lang.tag}>
                          {lang.nativeName}
                        </ListBoxItem>
                      ))}
                      {/* A saved tag that is not listed stays visible under its raw tag (display only). */}
                      {s.language && !languages.some((lang) => lang.tag === s.language) && (
                        <ListBoxItem id={s.language}>{s.language}</ListBoxItem>
                      )}
                    </ListBox>
                  </Select.Popover>
                </Select>
              </div>
              {result && (
                <span
                  className={cn(
                    "max-w-48 truncate text-[11px]",
                    result.ok ? "text-success" : "text-danger",
                  )}
                >
                  {result.ok ? intl.formatMessage({ id: "web.routes.admin_settings.test_delivered", defaultMessage: "✓ delivered", description: "Admin settings: status shown after a notification test was delivered" }) : result.message}
                </span>
              )}
              <TestButton
                dirty={dirty}
                disabled={!s.configRef || dirty || test.isPending}
                onPress={() => runTest(s.name)}
              >{intl.formatMessage({ id: "web.routes.admin_settings.send_test", defaultMessage: "Send test", description: "Admin settings: button that sends a test message to a notification sink; keep it short" })}</TestButton>
              <Switch
                aria-label={s.enabled ? intl.formatMessage({ id: "web.routes.admin_settings.disable_sink_name", defaultMessage: "Disable sink {name}", description: "Admin settings: aria-label of the switch that turns a notification sink off; {name} is the sink name" }, { name: s.name }) : intl.formatMessage({ id: "web.routes.admin_settings.enable_sink_name", defaultMessage: "Enable sink {name}", description: "Admin settings: aria-label of the switch that turns a notification sink on; {name} is the sink name" }, { name: s.name })}
                isSelected={s.enabled}
                onChange={(v) => {
                  const next = f.draft.sinks.map((x, i) =>
                    i === idx ? { ...x, enabled: v } : x,
                  );
                  f.update({ sinks: next });
                }}
              >
                <Switch.Content>
                  <Switch.Control>
                    <Switch.Thumb />
                  </Switch.Control>
                </Switch.Content>
              </Switch>
              <Button
                variant="ghost"
                isIconOnly
                aria-label={intl.formatMessage({ id: "web.routes.admin_settings.delete_sink_name", defaultMessage: "Delete sink {name}", description: "Admin settings: aria-label of the button that deletes a notification sink; {name} is the sink name" }, { name: s.name })}
                onPress={() => {
                  // The API-managed Secret is removed only by the Save
                  // that drops this sink; the server refuses user-created
                  // Secrets.
                  if (s.configRef === sinkSecretPrefix + s.name) {
                    f.stageSecret(`sink:${s.name}`, {
                      kind: "remove",
                      run: () => Notifications.deleteSecret(s.name),
                    });
                  }
                  f.update({ sinks: f.draft.sinks.filter((_, i) => i !== idx) });
                }}
              >
                <Trash2 className="h-4 w-4" />
              </Button>
            </li>
          );
        })}
      </ul>
      {adding ? (
        <AddSinkForm
          existing={f.draft.sinks.map((s) => s.name)}
          onAdd={(s, credentials) => {
            f.stageSecret(`sink:${s.name}`, {
              kind: "write",
              run: () => Notifications.putSecret(s.name, credentials),
            });
            f.update({ sinks: [...f.draft.sinks, s] });
          }}
          onClose={() => setAdding(false)}
        />
      ) : (
        <Button variant="outline" size="sm" onPress={() => setAdding(true)}>
          <Plus className="mr-1.5 h-4 w-4" />{intl.formatMessage({ id: "web.routes.admin_settings.add_sink", defaultMessage: "Add sink", description: "Admin settings: heading of the add-sink form, and the button in its footer that adds the sink; keep it short" })}</Button>
      )}
    </SectionCard>
  );
}

const defaultTelemetry: TelemetryCfg = { sendMetrics: false, extended: false };

const TELEMETRY_KEY = ["telemetry"] as const;

// Intl output is "Oct 6, 2026, 09:12" for these options; the section always
// renders UTC so the text matches what the receiver stores.
function formatUtc(iso: string, withTime: boolean, fmt: FormatApi): string {
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return iso;
  const text = fmt.dateTime(d, {
    month: "short",
    day: "numeric",
    year: "numeric",
    ...(withTime ? { hour: "2-digit", minute: "2-digit", hourCycle: "h23" as const } : {}),
    timeZone: "UTC",
  });
  return withTime ? `${text} UTC` : text;
}

const destinationCopy = defineMessages({
  defaultDetail: { id: "web.routes.admin_settings.telemetry_default_detail", defaultMessage: "The Gameplane project's telemetry service.", description: "Admin settings: telemetry destination description when reports go to the project default service" },
  defaultBadge: { id: "web.routes.admin_settings.telemetry_default_badge", defaultMessage: "Project default", description: "Admin settings: badge for the project default telemetry destination" },
  customDetail: { id: "web.routes.admin_settings.telemetry_custom_detail", defaultMessage: "Custom destination set by your operator. Nothing is sent to the Gameplane project.", description: "Admin settings: telemetry destination description for a custom destination set by the operator" },
  customBadge: { id: "web.routes.admin_settings.telemetry_custom_badge", defaultMessage: "Custom", description: "Admin settings: badge for a custom telemetry destination" },
  bundledDetail: { id: "web.routes.admin_settings.telemetry_bundled_detail", defaultMessage: "The receiver bundled with this install. Reports stay inside your cluster.", description: "Admin settings: telemetry destination description for the receiver bundled with this install" },
  bundledBadge: { id: "web.routes.admin_settings.telemetry_bundled_badge", defaultMessage: "Bundled", description: "Admin settings: badge for the telemetry receiver bundled with this install" },
});

function TelemetryDestinationBlock({ info }: { info: TelemetryInfo }) {
  const intl = useOptionalIntl();
  const kind = info.destination.kind;
  if (info.operatorDisabled || kind === "disabled") {
    return (
      <div className="flex items-start gap-3 rounded-md border border-warning/40 bg-warning/10 p-3">
        <CircleHelp className="mt-0.5 h-4 w-4 shrink-0 text-warning-soft-foreground" />
        <div className="flex-1">
          <div className="text-sm font-medium">{intl.formatMessage({ id: "web.routes.admin_settings.telemetry_is_disabled_by_the_operator", defaultMessage: "Telemetry is disabled by the operator.", description: "Admin settings: heading of the warning box saying telemetry was turned off by the operator" })}</div>
          <div className="pt-0.5 text-xs text-muted">{intl.formatMessage({ id: "web.routes.admin_settings.no_data_of_either_kind_is_sent_and_no", defaultMessage: "No data of either kind is sent and no telemetry destination is contacted, whatever the switches below say. Only an operator can change this, at install time.", description: "Admin settings: text under the disabled-telemetry heading: no data is sent, no destination is contacted, and only an operator can change this at install time" })}</div>
        </div>
        <span className="rounded bg-warning/20 px-2 py-0.5 text-xs text-warning-soft-foreground">{intl.formatMessage({ id: "web.routes.admin_settings.disabled", defaultMessage: "Disabled", description: "Admin settings: badge showing the telemetry destination is disabled; keep it short" })}</span>
      </div>
    );
  }
  if (kind === "none") {
    return (
      <div className="flex items-start gap-3 rounded-md bg-surface p-3">
        <CircleHelp className="mt-0.5 h-4 w-4 shrink-0 text-accent" />
        <div className="flex-1">
          <div className="text-sm font-medium">{intl.formatMessage({ id: "web.routes.admin_settings.no_telemetry_destination_configured", defaultMessage: "No telemetry destination configured.", description: "Admin settings: heading of the telemetry box shown when no telemetry destination is configured" })}</div>
          <div className="pt-0.5 text-xs text-muted">{intl.formatMessage({ id: "web.routes.admin_settings.nothing_is_sent_an_operator_can_set_a", defaultMessage: "Nothing is sent. An operator can set a custom or bundled destination at install time.", description: "Admin settings: text under the no-destination heading: nothing is sent, and an operator can set a destination at install time" })}</div>
        </div>
        <span className="rounded bg-background px-2 py-0.5 text-xs">{intl.formatMessage({ id: "web.routes.admin_settings.no_destination", defaultMessage: "No destination", description: "Admin settings: badge shown when no telemetry destination is configured; keep it short" })}</span>
      </div>
    );
  }
  const copy = {
    detail: intl.formatMessage(
      kind === "bundled" ? destinationCopy.bundledDetail : kind === "custom" ? destinationCopy.customDetail : destinationCopy.defaultDetail,
    ),
    badge: intl.formatMessage(
      kind === "bundled" ? destinationCopy.bundledBadge : kind === "custom" ? destinationCopy.customBadge : destinationCopy.defaultBadge,
    ),
  };
  const Icon = kind === "bundled" ? House : kind === "custom" ? CircleHelp : Globe;
  return (
    <div className="flex items-start gap-3 rounded-md bg-surface p-3">
      <Icon className="mt-0.5 h-4 w-4 shrink-0 text-accent" />
      <div className="flex-1">
        <div className="text-sm font-medium">{intl.formatMessage({ id: "web.routes.admin_settings.sending_to_host", defaultMessage: "Sending to {host}", description: "Admin settings: sentence with host in div" }, { host: info.destination.host })}</div>
        <div className="pt-0.5 text-xs text-muted">{copy.detail}</div>
      </div>
      <span className="rounded bg-background px-2 py-0.5 text-xs">{copy.badge}</span>
      <a
        href={TELEMETRY_STATEMENT_URL}
        target="_blank"
        rel="noreferrer"
        className="text-xs text-accent hover:underline"
      >{intl.formatMessage({ id: "web.routes.admin_settings.data_handling_statement", defaultMessage: "Data-handling statement", description: "Admin settings: link to the telemetry data-handling statement, which opens in a new tab; keep it short" })}</a>
    </div>
  );
}

function TelemetryPreviewBlock({ info }: { info: TelemetryInfo }) {
  const intl = useOptionalIntl();
  let empty: string | null = null;
  if (info.operatorDisabled || info.destination.kind === "disabled") {
    empty = intl.formatMessage({ id: "web.routes.admin_settings.nothing_to_preview_disabled", defaultMessage: "Nothing to preview: telemetry is disabled by the operator.", description: "Admin settings: shown in place of the telemetry preview when the operator disabled telemetry" });
  } else if (info.destination.kind === "none") {
    empty = intl.formatMessage({ id: "web.routes.admin_settings.nothing_to_preview_no_destination", defaultMessage: "Nothing to preview: no destination is configured.", description: "Admin settings: shown in place of the telemetry preview when no destination is configured" });
  } else if (!info.preview) {
    empty = intl.formatMessage({ id: "web.routes.admin_settings.nothing_to_preview_metrics_off", defaultMessage: "Nothing to preview: basic usage metrics are off.", description: "Admin settings: shown in place of the telemetry preview when basic usage metrics are off" });
  }
  return (
    <div className="space-y-1.5">
      <div className="text-sm font-medium">{intl.formatMessage({ id: "web.routes.admin_settings.next_report_preview", defaultMessage: "Next report preview", description: "Admin settings: heading of the block that previews the next telemetry report" })}</div>
      <div className="text-xs text-muted">{intl.formatMessage({ id: "web.routes.admin_settings.exactly_what_the_next_report_contains", defaultMessage: "Exactly what the next report contains. Read-only.", description: "Admin settings: text under the preview heading saying the shown content is exactly what the next report contains and is read-only" })}</div>
      {empty || !info.preview ? (
        <div className="rounded-md border border-border p-3 text-xs text-muted">{empty}</div>
      ) : (
        <pre
          role="region"
          aria-label={intl.formatMessage({ id: "web.routes.admin_settings.next_report_json", defaultMessage: "Next report JSON", description: "Admin settings: accessible name of the scrollable block that shows the next telemetry report as JSON" })}
          tabIndex={0}
          className="overflow-x-auto rounded-md border border-border bg-background p-3 font-mono text-xs"
        >
          {JSON.stringify(info.preview, null, 2)}
        </pre>
      )}
    </div>
  );
}

function TelemetryStatusLines({ info }: { info: TelemetryInfo }) {
  const intl = useOptionalIntl();
  const { status } = info;
  const fmt = useFormat();
  let text: string;
  if (status.lastOutcome === "ok") {
    text = intl.formatMessage({ id: "web.routes.admin_settings.last_report_sent_on", defaultMessage: "Last report sent successfully on {when}.", description: "Admin settings: telemetry status after a successful report; {when} is the time of that report or 'an earlier attempt'" }, { when: status.lastSuccessAt ? formatUtc(status.lastSuccessAt, true, fmt) : intl.formatMessage({ id: "web.routes.admin_settings.an_earlier_attempt", defaultMessage: "an earlier attempt", description: "Admin settings: stands in for the time of the last successful report when the time is unknown" }) });
  } else if (status.lastOutcome === "failed") {
    text = status.lastSuccessAt
      ? intl.formatMessage({ id: "web.routes.admin_settings.last_attempt_failed_with_success", defaultMessage: "Last attempt failed, will retry. Last successful report: {when}.", description: "Admin settings: telemetry status after a failed report when an earlier one succeeded; {when} is the time of that report" }, { when: formatUtc(status.lastSuccessAt, true, fmt) })
      : intl.formatMessage({ id: "web.routes.admin_settings.last_attempt_failed_no_success", defaultMessage: "Last attempt failed, will retry. No report has been delivered yet.", description: "Admin settings: telemetry status after a failed report when no report was ever delivered" });
  } else {
    text = intl.formatMessage({ id: "web.routes.admin_settings.no_report_sent_yet", defaultMessage: "No report sent yet.", description: "Admin settings: telemetry status before any report was sent" });
  }
  return (
    <div className="space-y-1 text-xs text-muted">
      <div>{text}</div>
      {status.lastIdRotationAt && (
        <div>{intl.formatMessage({ id: "web.routes.admin_settings.install_id_replaced_on", defaultMessage: "Install ID replaced on", description: "Admin settings: status line starting with the date the install ID was replaced; the explanation follows it" })} {formatUtc(status.lastIdRotationAt, false, fmt)}{intl.formatMessage({ id: "web.routes.admin_settings.the_destination_reported_it_was_in_use", defaultMessage: ": the destination reported it was in use by another key.", description: "Admin settings: end of the install-ID status line, saying the destination reported the ID was already in use by another key" })}</div>
      )}
    </div>
  );
}

function TelemetrySection({ initial }: { initial?: TelemetryCfg }) {
  const intl = useOptionalIntl();
  const f = useSectionForm<TelemetryCfg>(initial ?? defaultTelemetry, "telemetry");
  const qc = useQueryClient();
  const infoQ = useQuery({ queryKey: TELEMETRY_KEY, queryFn: () => Telemetry.get() });
  const info = infoQ.data;
  const [confirmingReset, setConfirmingReset] = useState(false);
  const reset = useMutation({
    mutationFn: () => Telemetry.resetInstallId(),
    onSuccess: () => {
      setConfirmingReset(false);
      return qc.invalidateQueries({ queryKey: TELEMETRY_KEY });
    },
    onError: () => setConfirmingReset(false),
  });
  // A saved change alters the preview, the install ID and the status.
  useEffect(() => {
    if (f.saved) void qc.invalidateQueries({ queryKey: TELEMETRY_KEY });
  }, [f.saved, qc]);

  const operatorDisabled = info?.operatorDisabled === true || info?.destination.kind === "disabled";
  // The operator switch overrides stored consent: show both off and inert.
  const basicOn = !operatorDisabled && f.draft.sendMetrics;
  const extendedOn = basicOn && f.draft.extended;
  const installId = info?.installId ?? null;

  return (
    <SectionCard
      title={intl.formatMessage({ id: "web.routes.admin_settings.telemetry", defaultMessage: "Telemetry", description: "Admin settings: title of the Telemetry section card; keep it short" })}
      subtitle={intl.formatMessage({ id: "web.routes.admin_settings.anonymous_usage_metrics_help_us", defaultMessage: "Anonymous usage metrics help us prioritize work. Extended metrics include a random install ID.", description: "Admin settings: subtitle under Telemetry: anonymous usage metrics help prioritise work, and extended metrics include a random install ID" })}
      footer={
        <>
          <SaveStatus pending={f.pending} error={f.error} saved={f.saved} />
          <Button onPress={f.save} isDisabled={f.pending || operatorDisabled}>{intl.formatMessage({ id: "web.routes.admin_settings.save_changes", defaultMessage: "Save changes", description: "Admin settings: button that saves the changes in this section; keep it short" })}</Button>
        </>
      }
    >
      {info && <TelemetryDestinationBlock info={info} />}
      <div className="flex items-center justify-between">
        <div>
          <div className="text-sm">{intl.formatMessage({ id: "web.routes.admin_settings.send_basic_usage_metrics", defaultMessage: "Send basic usage metrics", description: "Admin settings: label of the switch that turns on basic usage metrics" })}</div>
          <div className="pt-0.5 text-xs text-muted">{intl.formatMessage({ id: "web.routes.admin_settings.gameplane_version_number_of_servers_and", defaultMessage: "Gameplane version, number of servers and number of templates.", description: "Admin settings: hint under the basic metrics switch listing what is sent: the Gameplane version, number of servers and number of templates" })}</div>
        </div>
        <TelemetrySwitch
          ariaLabel={basicOn ? intl.formatMessage({ id: "web.routes.admin_settings.disable_telemetry", defaultMessage: "Disable telemetry", description: "Admin settings: aria-label of the basic telemetry switch when it is on" }) : intl.formatMessage({ id: "web.routes.admin_settings.enable_telemetry", defaultMessage: "Enable telemetry", description: "Admin settings: aria-label of the basic telemetry switch when it is off" })}
          isSelected={basicOn}
          isDisabled={operatorDisabled}
          // Turning basic off turns extended off with it (spec Q7).
          onChange={(v) => f.update(v ? { sendMetrics: true } : { sendMetrics: false, extended: false })}
        />
      </div>
      <div className="flex items-center justify-between pl-8">
        <div>
          <div className="text-sm">{intl.formatMessage({ id: "web.routes.admin_settings.send_extended_usage_metrics", defaultMessage: "Send extended usage metrics", description: "Admin settings: label of the switch that turns on extended usage metrics; basic metrics must be on first" })}</div>
          <div className="pt-0.5 text-xs text-muted">
            {!operatorDisabled && !basicOn
              ? intl.formatMessage({ id: "web.routes.admin_settings.telemetry_extended_needs_basic", defaultMessage: "Turn on basic usage metrics to enable extended usage metrics.", description: "Admin settings: hint under the extended telemetry switch while basic metrics are off" })
              : intl.formatMessage({ id: "web.routes.admin_settings.telemetry_extended_contents", defaultMessage: "Adds Kubernetes version, distribution, CPU architecture, node-count band, servers per official game module, which optional features are in use, and a random install ID.", description: "Admin settings: describes what extended telemetry adds to each report" })}
          </div>
        </div>
        <TelemetrySwitch
          ariaLabel={extendedOn ? intl.formatMessage({ id: "web.routes.admin_settings.disable_extended_telemetry", defaultMessage: "Disable extended telemetry", description: "Admin settings: aria-label of the extended telemetry switch when it is on" }) : intl.formatMessage({ id: "web.routes.admin_settings.enable_extended_telemetry", defaultMessage: "Enable extended telemetry", description: "Admin settings: aria-label of the extended telemetry switch when it is off" })}
          isSelected={extendedOn}
          isDisabled={operatorDisabled || !basicOn}
          onChange={(v) => f.update({ extended: v })}
        />
      </div>
      <div className="flex items-center justify-between pl-8">
        <div>
          <div className="text-sm">{intl.formatMessage({ id: "web.routes.admin_settings.install_id", defaultMessage: "Install ID", description: "Admin settings: label of the install ID row in the telemetry section" })}</div>
          {installId ? (
            <>
              <div className="font-mono text-xs">{installId}</div>
              <div className="pt-0.5 text-xs text-muted">{intl.formatMessage({ id: "web.routes.admin_settings.random_and_anonymous_reset_it_to_start", defaultMessage: "Random and anonymous. Reset it to start reporting under a new ID; the old ID is never sent again.", description: "Admin settings: hint under the install ID: it is random and anonymous; resetting starts reporting under a new ID and the old one is never sent again" })}</div>
            </>
          ) : (
            <div className="pt-0.5 text-xs text-muted">{intl.formatMessage({ id: "web.routes.admin_settings.no_install_id_while_extended_metrics", defaultMessage: "No install ID while extended metrics are off.", description: "Admin settings: hint shown in place of the install ID while extended metrics are off" })}</div>
          )}
        </div>
        <Button
          variant="outline"
          size="sm"
          isDisabled={!installId || operatorDisabled || reset.isPending}
          onPress={() => setConfirmingReset(true)}
        >{intl.formatMessage({ id: "web.routes.admin_settings.reset_id", defaultMessage: "Reset ID", description: "Admin settings: button that asks to reset the install ID, and the confirm button of the reset dialog; keep it short" })}</Button>
      </div>
      {reset.error && (
        <div className="pl-8 text-xs text-danger">{errorTextIntl(intl, reset.error, intl.formatMessage({ id: "web.routes.admin_settings.reset_install_id_failed", defaultMessage: "Could not reset the install ID", description: "Admin settings: fallback error text under the install ID when resetting it fails" }))}</div>
      )}
      {info && <TelemetryPreviewBlock info={info} />}
      {info && !operatorDisabled && info.destination.kind !== "none" && <TelemetryStatusLines info={info} />}
      <ConfirmDialog
        open={confirmingReset}
        onOpenChange={setConfirmingReset}
        title={intl.formatMessage({ id: "web.routes.admin_settings.reset_install_id", defaultMessage: "Reset install ID?", description: "Admin settings: title of the confirmation dialog for resetting the random install ID" })}
        confirmLabel={intl.formatMessage({ id: "web.routes.admin_settings.reset_id", defaultMessage: "Reset ID", description: "Admin settings: button that asks to reset the install ID, and the confirm button of the reset dialog; keep it short" })}
        destructive
        busy={reset.isPending}
        onConfirm={() => reset.mutate()}
        description={intl.formatMessage({ id: "web.routes.admin_settings.telemetry_reset_id_description", defaultMessage: "This replaces your random install ID {id} with a new one. The old ID is never sent again, and the destination cannot link the old and new IDs. The next report is sent under the new ID.", description: "Admin settings: confirm dialog text for resetting the install ID; {id} is the current install ID, or empty" }, { id: installId ?? "" })}
      />
    </SectionCard>
  );
}

function UpdatesSection() {
  const intl = useOptionalIntl();
  const installation = useInstallation();
  // Read-only: the channel mirrors the chart's informational
  // updates.channel value (served on /cluster/info). Nothing in-app
  // consumes it — Gameplane is upgraded via Helm, not a self-updater.
  const { data } = useQuery({
    queryKey: ["cluster-info"],
    queryFn: () => Cluster.info().catch(() => ({} as ClusterInfo)),
    enabled: installation.data?.localCluster === true,
    staleTime: 60_000,
  });
  return (
    <SectionCard
      title={intl.formatMessage({ id: "web.routes.admin_settings.updates", defaultMessage: "Updates", description: "Admin settings: title of the Updates section card about how this installation is upgraded; keep it short" })}
      subtitle={installation.data?.standalone ? intl.formatMessage({ id: "web.routes.admin_settings.updates_subtitle_standalone", defaultMessage: "Update the panel and API container images to upgrade this installation.", description: "Admin settings: subtitle of the Updates card on a standalone install" }) : intl.formatMessage({ id: "web.routes.admin_settings.updates_subtitle_helm", defaultMessage: "Gameplane is upgraded via Helm (image tags / chart versions).", description: "Admin settings: subtitle of the Updates card when the install is managed by Helm" })}
    >
      <div className="flex items-center justify-between">
        <div>
          <div className="text-sm">{intl.formatMessage({ id: "web.routes.admin_settings.channel", defaultMessage: "Channel", description: "Admin settings: label of the update channel row; its value is set by the container image tags" })}</div>
          <div className="pt-0.5 text-xs text-muted">
            {installation.data?.standalone ? intl.formatMessage({ id: "web.routes.admin_settings.container_tags_select_release", defaultMessage: "Container image tags select the release for this panel.", description: "Admin settings: channel note on a standalone install" }) : <>{intl.formatMessage({ id: "web.routes.admin_settings.informational_only_reflects_helm", defaultMessage: "Informational only — reflects {code} from your Helm values.", description: "Admin settings: channel note when managed by Helm; {code} is the updates.channel value name" }, { code: <code className="font-mono">{intl.formatMessage({ id: "web.routes.admin_settings.updates_channel", defaultMessage: "updates.channel", description: "Admin settings: the updates.channel Helm value name" })}</code> })}</>}
          </div>
        </div>
        <span className="rounded bg-surface px-2 py-0.5 font-mono text-xs">
          {data?.updateChannel || intl.formatMessage({ id: "web.common.empty_value", defaultMessage: "—", description: "Shown in place of a value that is not set" })}
        </span>
      </div>
    </SectionCard>
  );
}

function AboutSection() {
  const intl = useOptionalIntl();
  const installation = useInstallation();
  // Real versions from the API (the control plane is built and released as
  // one unit, so a single Gameplane version is honest); "—" until loaded.
  const { data } = useQuery({
    queryKey: ["cluster-info"],
    queryFn: () => Cluster.info().catch(() => ({} as ClusterInfo)),
    enabled: installation.data?.localCluster === true,
    staleTime: 60_000,
  });
  return (
    <SectionCard title={intl.formatMessage({ id: "web.routes.admin_settings.about", defaultMessage: "About", description: "Admin settings: title of the About section card showing version details; keep it short" })} subtitle={intl.formatMessage({ id: "web.routes.admin_settings.this_gameplane_build", defaultMessage: "This Gameplane build.", description: "Admin settings: subtitle under the About title describing the Gameplane build shown below" })}>
      <dl className="grid grid-cols-[auto_1fr] gap-x-6 gap-y-3 text-sm">
        <dt className="text-muted">{intl.formatMessage({ id: "web.routes.admin_settings.gameplane", defaultMessage: "Gameplane", description: "Admin settings: row label for the Gameplane version in the About list; keep it short" })}</dt><dd className="font-mono">{installation.data?.version || data?.gameplaneVersion || intl.formatMessage({ id: "web.common.empty_value", defaultMessage: "—", description: "Shown in place of a value that is not set" })}</dd>
        {installation.data?.localCluster && <><dt className="text-muted">{intl.formatMessage({ id: "web.routes.admin_settings.kubernetes", defaultMessage: "Kubernetes", description: "Admin settings: row label for the Kubernetes version in the About list, shown for the local cluster only; keep it short" })}</dt><dd className="font-mono">{data?.version || intl.formatMessage({ id: "web.common.empty_value", defaultMessage: "—", description: "Shown in place of a value that is not set" })}</dd></>}
        <dt className="text-muted">{intl.formatMessage({ id: "web.routes.admin_settings.license", defaultMessage: "License", description: "Admin settings: row label for the license in the About list; keep it short" })}</dt><dd>{intl.formatMessage({ id: "web.routes.admin_settings.agpl_3_0", defaultMessage: "AGPL-3.0", description: "Admin settings: license name shown in the About list, usually left untranslated" })}</dd>
      </dl>
    </SectionCard>
  );
}

// T023: Read-only Helm-seeded OIDC provider display (from installTimeSettings.oidcHelmProvider)
function HelmOIDCProviderCard({ provider }: { provider: OIDCHelmProvider }) {
  const intl = useOptionalIntl();
  const hasRoleMappings = provider.roleMappings && Object.keys(provider.roleMappings).length > 0;
  const adminMapped = provider.roleMappings?.admin && provider.roleMappings.admin.length > 0;
  return (
    <SectionCard
      className="gap-0 space-y-0 p-0"
      title={intl.formatMessage({ id: "web.routes.admin_settings.helm_seeded_oidc_provider", defaultMessage: "Helm-seeded OIDC provider", description: "Admin settings: title of the card showing the OIDC provider set up through Helm values (OIDC: technical term, usually left untranslated)" })}
      subtitle={intl.formatMessage({ id: "web.routes.admin_settings.read_only_snapshot_of_the_identity", defaultMessage: "Read-only snapshot of the identity provider configured via api.oidc.* Helm values at install time. Connection settings are not editable here; role mappings can be overridden below.", description: "Admin settings: subtitle under the Helm-seeded provider title: a read-only snapshot taken at install time; role mappings can be overridden below" })}
      titleClassName="text-sm font-medium"
      subtitleClassName="text-sm"
      headerClassName="px-5 pt-5 pb-4 space-y-1"
      contentClassName="px-5 pb-5 space-y-4"
    >
      <div>
        <div className="space-y-0.5 pb-1">
          <div className="font-medium text-sm">{intl.formatMessage({ id: "web.routes.admin_settings.provider", defaultMessage: "Provider", description: "Admin settings: heading of the provider details block in the Helm-seeded card; keep it short" })}</div>
          <p className="text-[11px] text-muted">{intl.formatMessage({ id: "web.routes.admin_settings.groups_claim_and_default_role_are", defaultMessage: "Groups claim and default role are seeded from the same Helm values and are not editable here.", description: "Admin settings: note under the provider heading: groups claim and default role come from Helm values and cannot be edited here" })}</p>
        </div>
        <div className="flex items-center justify-between py-2">
          <span className="text-xs text-muted">{intl.formatMessage({ id: "web.routes.admin_settings.groups_claim", defaultMessage: "Groups claim", description: "Admin settings: row label for the groups claim in the Helm provider details; keep it short" })}</span>
          <span className="font-mono text-xs font-medium text-foreground">{provider.groupsClaim}</span>
        </div>
        <div className="flex items-center justify-between py-2">
          <span className="text-xs text-muted">{intl.formatMessage({ id: "web.routes.admin_settings.default_role", defaultMessage: "Default role", description: "Admin settings: label for the default role, the role given to users whose groups match no mapping; also a row label in the Helm provider details" })}</span>
          <span className="rounded-full px-1 py-0.5 text-[11px] font-mono font-medium bg-default text-default-foreground">
            {provider.defaultRole}
          </span>
        </div>
        {hasRoleMappings ? (
          <div className="space-y-2.5 pt-2">
            <div className="text-xs text-muted">{intl.formatMessage({ id: "web.routes.admin_settings.helm_seeded_role_mappings", defaultMessage: "Helm-seeded role mappings", description: "Admin settings: label above the role mappings seeded from Helm values" })}</div>
            {["admin", "operator", "viewer"].map((role) => {
              const groups = provider.roleMappings?.[role as keyof typeof provider.roleMappings];
              return (
                <div key={role} className="flex items-center gap-3">
                  <span className="text-xs font-medium w-[70px] flex-shrink-0 capitalize">{role}</span>
                  <div className="flex flex-wrap items-center gap-1.5">
                    {groups && groups.length > 0 ? (
                      groups.map((group) => (
                        <span
                          key={group}
                          className={cn(
                            "rounded-full px-1 py-0.5 text-[11px] font-mono font-medium",
                            role === "admin" && "bg-warning-soft text-warning-soft-foreground",
                            role === "operator" && "bg-accent-soft text-accent-soft-foreground",
                            role === "viewer" && "bg-default text-default-foreground",
                          )}
                        >
                          {group}
                        </span>
                      ))
                    ) : (
                      <span className="text-xs text-muted italic">{intl.formatMessage({ id: "web.common.empty_value", defaultMessage: "—", description: "Shown in place of a value that is not set" })}</span>
                    )}
                  </div>
                </div>
              );
            })}
          </div>
        ) : (
          <div className="rounded-md border border-border bg-surface/50 p-4 space-y-2">
            <div className="font-medium text-sm">{intl.formatMessage({ id: "web.routes.admin_settings.no_oidc_role_mappings_yet", defaultMessage: "No OIDC role mappings yet", description: "Admin settings: heading shown when the OIDC provider has no role mappings yet; the text below says how to add them" })}</div>
            <p className="text-xs text-muted">{intl.formatMessage({ id: "web.routes.admin_settings.every_oidc_user_currently_signs_in_with", defaultMessage: "Every OIDC user currently signs in with the {defaultRole} default role. Two ways to change that: add mappings in Role mapping overrides below — they apply from the next login, with no restart or Helm re-run — or seed them at install time with api.oidc.roleMappings.admin / .operator / .viewer in your Helm values.", description: "Admin settings: sentence with defaultRole in p" }, { defaultRole: provider.defaultRole })}</p>
          </div>
        )}
        {adminMapped && (
          <div className="mt-3 rounded-lg border border-warning-soft-foreground bg-warning-soft px-4 py-3 flex gap-4">
            <Megaphone className="h-4 w-4 text-warning-soft-foreground flex-shrink-0 mt-0.5" />
            <div className="text-sm">
              <div className="font-medium text-warning-soft-foreground mb-1">{intl.formatMessage({ id: "web.routes.admin_settings.helm_configured_admin_mapping", defaultMessage: "Helm-configured admin mapping", description: "Admin settings: warning heading shown when the admin role is mapped through Helm values" })}</div>
              <p className="text-warning-soft-foreground">{intl.formatMessage({ id: "web.routes.admin_settings.the_group_s_on_the_admin_row_above_were", defaultMessage: "The group(s) on the Admin row above were mapped to admin via Helm values, not the dashboard, so there is no confirmation step here. Verify they contain only trusted accounts — anyone in them gets full admin access.", description: "Admin settings: warning text: the groups on the Admin row were mapped to admin through Helm, so there is no confirmation step; check they hold only trusted accounts" })}</p>
            </div>
          </div>
        )}
      </div>
    </SectionCard>
  );
}

// T024: Role mapping overrides card
function RoleMappingOverridesCard({
  initial,
  installTimeSettings,
  onUpdate,
  onResetDone,
  onResetError,
  formState,
  onSave,
}: {
  initial: AuthCfg;
  installTimeSettings?: InstallTimeSettings;
  onUpdate: (cfg: AuthCfg) => void;
  onResetDone: (role: "admin" | "operator" | "viewer") => void;
  onResetError: (message: string) => void;
  formState: { pending: boolean; error: string | null; saved: boolean };
  onSave: () => void;
}) {
  const intl = useOptionalIntl();
  const [adminInput, setAdminInput] = useState("");
  const [operatorInput, setOperatorInput] = useState("");
  const [viewerInput, setViewerInput] = useState("");
  const [confirmingRole, setConfirmingRole] = useState<string | null>(null);
  const [pendingGroups, setPendingGroups] = useState<string[]>([]);
  const resetMutation = useResetRoleMapping();

  const helmMappings = installTimeSettings?.oidcHelmProvider?.roleMappings;

  const getRoleMapping = (role: "admin" | "operator" | "viewer") => {
    return initial.helmOverride?.roleMappings?.[role];
  };

  const getProvenance = (role: "admin" | "operator" | "viewer"): "overridden" | "fromHelm" | "notConfigured" => {
    const hasOverride = initial.helmOverride?.roleMappings && role in initial.helmOverride.roleMappings;
    if (hasOverride) return "overridden";
    if (helmMappings) return "fromHelm";
    return "notConfigured";
  };

  const getRoleVariant = (role: "admin" | "operator" | "viewer"): "secondary" | "orange" | "violet" => {
    if (role === "admin") return "orange";
    if (role === "operator") return "violet";
    return "secondary";
  };

  const getRoleGroups = (role: "admin" | "operator" | "viewer"): string[] => {
    const override = getRoleMapping(role);
    if (override !== undefined) return override;
    return helmMappings?.[role] || [];
  };

  const handleAddGroup = (role: "admin" | "operator" | "viewer", group: string) => {
    const trimmed = group.trim();
    if (!trimmed) return;

    // Check if it's trying to add an admin mapping - if so, show confirmation
    if (role === "admin" && !confirmingRole) {
      setConfirmingRole(role);
      setPendingGroups([...getRoleGroups(role), trimmed]);
      return;
    }

    const current = getRoleGroups(role);
    if (current.includes(trimmed)) return;

    const newMapping = [...current, trimmed];
    const newOverride = {
      ...initial.helmOverride,
      roleMappings: {
        ...initial.helmOverride?.roleMappings,
        [role]: newMapping,
      },
    };
    onUpdate({
      ...initial,
      helmOverride: newOverride,
    });

    if (role === "admin") setAdminInput("");
    else if (role === "operator") setOperatorInput("");
    else setViewerInput("");
  };

  const handleRemoveGroup = (role: "admin" | "operator" | "viewer", group: string) => {
    const current = getRoleGroups(role);
    const newMapping = current.filter((g) => g !== group);
    const newOverride = {
      ...initial.helmOverride,
      roleMappings: {
        ...initial.helmOverride?.roleMappings,
        [role]: newMapping,
      },
    };
    onUpdate({
      ...initial,
      helmOverride: newOverride,
    });
  };

  // A reset removes the stored override. Dropping it from the section
  // draft as well keeps the card on the Helm value and keeps a later save
  // from writing the override back. A failure shows in the section's save
  // status and leaves the draft as it was.
  const handleReset = (role: "admin" | "operator" | "viewer") => {
    resetMutation.mutate(role, {
      onSuccess: () => onResetDone(role),
      onError: (err) => onResetError(errorTextIntl(intl, err, intl.formatMessage({ id: "web.routes.admin_settings.reset_failed", defaultMessage: "Reset failed", description: "Admin settings: fallback error when resetting a role mapping fails" }))),
    });
  };

  const handleConfirmAdminMapping = () => {
    if (confirmingRole === "admin" && pendingGroups.length > 0) {
      const newOverride = {
        ...initial.helmOverride,
        roleMappings: {
          ...initial.helmOverride?.roleMappings,
          admin: pendingGroups,
        },
      };
      onUpdate({
        ...initial,
        helmOverride: newOverride,
      });
    }
    setConfirmingRole(null);
    setPendingGroups([]);
    setAdminInput("");
  };

  return (
    <>
      <SectionCard
        title={intl.formatMessage({ id: "web.routes.admin_settings.role_mapping_overrides", defaultMessage: "Role mapping overrides", description: "Admin settings: title of the card where role mappings can be overridden per role in the dashboard; keep it short" })}
        subtitle={intl.formatMessage({ id: "web.routes.admin_settings.override_the_helm_seeded_oidc_group", defaultMessage: "Override the Helm-seeded OIDC group mappings per role from here. A change takes effect the next time an affected user logs in — no restart or reinstall needed.", description: "Admin settings: subtitle under Role mapping overrides: change the Helm-seeded group mappings per role; changes apply at the next login without a restart" })}
        className="bg-card gap-0 space-y-0 p-0"
        titleClassName="text-sm font-medium"
        subtitleClassName="text-sm"
        headerClassName="px-5 pt-5 pb-4 space-y-1"
        contentClassName="px-5 pb-5 space-y-4"
        footerClassName="px-5 pt-2 pb-5 justify-between"
        footer={
          <div className="flex items-center justify-between w-full">
            <p className="text-[11px] text-muted">{intl.formatMessage({ id: "web.routes.admin_settings.saved_changes_take_effect_on_next_login", defaultMessage: "Saved changes take effect on next login for affected users — no restart or reinstall needed.", description: "Admin settings: footer note of the role mapping overrides card: saved changes apply at the next login for affected users, with no restart needed" })}</p>
            <div className="flex gap-2">
              {/* No SaveStatus here: this card shares f.error/f.saved with the
                  Authentication card above (same useSectionForm, same PUT), whose
                  own footer already renders it. Duplicating it here means the exact
                  same text (e.g. a save error) appears twice in the DOM, breaking
                  any singular getByText/findByText query for it — see the
                  "surfaces a backend rejection when saving the auth section" test. */}
              <Button
                variant="outline"
                size="sm"
                className="text-[13px] font-medium"
                onPress={onSave}
                isDisabled={formState.pending}
              >{intl.formatMessage({ id: "web.routes.admin_settings.save_role_mappings", defaultMessage: "Save role mappings", description: "Admin settings: button that saves the role mapping overrides; keep it short" })}</Button>
            </div>
          </div>
        }
      >
        <div className="space-y-3.5">
          {["admin", "operator", "viewer"].map((role) => {
            const typedRole = role as "admin" | "operator" | "viewer";
            const groups = getRoleGroups(typedRole);
            const hasOverride =
              initial.helmOverride?.roleMappings && typedRole in initial.helmOverride.roleMappings;
            const input =
              typedRole === "admin" ? adminInput : typedRole === "operator" ? operatorInput : viewerInput;
            const setInput =
              typedRole === "admin"
                ? setAdminInput
                : typedRole === "operator"
                  ? setOperatorInput
                  : setViewerInput;

            return (
              <div key={role} role="group" aria-label={intl.formatMessage({ id: "web.routes.admin_settings.role_mapping_group", defaultMessage: "{role} role mapping", description: "Admin settings: accessible name of the group for one role's mapping; {role} is the capitalized role name" }, { role: role.charAt(0).toUpperCase() + role.slice(1) })}>
                {role !== "admin" && <div className="mb-4 h-px bg-border" />}
                <div className="space-y-2">
                  <div className="flex items-center justify-between">
                    <div className="flex items-center gap-2">
                      <span className="text-[13px] font-semibold capitalize">{role}</span>
                      <ProvenanceBadge type={getProvenance(typedRole)} size="sm" />
                    </div>
                    {hasOverride && (
                      <Button
                        variant="outline"
                        size="sm"
                        className="text-[13px] font-medium"
                        aria-label={intl.formatMessage({ id: "web.routes.admin_settings.reset_helm_default_role", defaultMessage: "Reset to Helm default ({role} role mapping)", description: "Admin settings: accessible name of the button that resets one role's mapping to the Helm default; {role} is the role name" }, { role })}
                        onPress={() => handleReset(typedRole)}
                        isDisabled={resetMutation.isPending}
                      >
                        <RotateCcw className="h-3.5 w-3.5 mr-1.5" />{intl.formatMessage({ id: "web.routes.admin_settings.reset_to_helm_default", defaultMessage: "Reset to Helm default", description: "Admin settings: button that resets one role's mapping to the Helm default; keep it short" })}</Button>
                    )}
                  </div>

                  {groups.length > 0 ? (
                    <div className="flex flex-wrap items-center gap-1.5">
                      {groups.map((group) => (
                        <RemovableGroupChip
                          key={group}
                          label={group}
                          variant={getRoleVariant(typedRole)}
                          onRemove={() => handleRemoveGroup(typedRole, group)}
                          size="md"
                        />
                      ))}
                    </div>
                  ) : (
                    <div className="flex items-center gap-2 px-2.5 py-1.5 rounded-md border border-border text-xs italic text-muted">
                      <CircleSlash2 className="h-3.5 w-3.5" />{intl.formatMessage({ id: "web.routes.admin_settings.no_groups_mapped_nobody_signs_in_as", defaultMessage: "No groups mapped — nobody signs in as {role} via OIDC.", description: "Admin settings: sentence with role in div" }, { role: role })}</div>
                  )}

                  <div className="flex items-center gap-2">
                    <div className="w-[250px] shrink-0">
                      <HeroInput
                        type="text"
                        placeholder={intl.formatMessage({ id: "web.routes.admin_settings.add_idp_group_name", defaultMessage: "Add IdP group name…", description: "Admin settings: grey placeholder inside the field where an identity-provider group name is typed to add it to a role" })}
                        value={input}
                        onChange={(e) => setInput(e.target.value)}
                        onKeyDown={(e) => {
                          if (e.key === "Enter") {
                            e.preventDefault();
                            handleAddGroup(typedRole, input);
                          }
                        }}
                        className="h-8 text-[13px]"
                      />
                    </div>
                    <Button
                      variant="outline"
                      size="sm"
                      aria-label={intl.formatMessage({ id: "web.routes.admin_settings.add_group", defaultMessage: "Add group", description: "Admin settings: accessible name of the button that adds the typed group to a role; keep it short" })}
                      onPress={() => handleAddGroup(typedRole, input)}
                      isDisabled={!input.trim()}
                      className="h-8 gap-1 text-[13px] font-medium"
                    >
                      <Plus className="h-3.5 w-3.5" />{intl.formatMessage({ id: "web.routes.admin_settings.add", defaultMessage: "Add", description: "Admin settings: button that adds the typed group to the role; keep it short" })}</Button>
                  </div>
                </div>
              </div>
            );
          })}
        </div>
      </SectionCard>

      {/* T030: Admin mapping confirmation dialog */}
      <ConfirmAdminMappingDialog
        open={confirmingRole === "admin"}
        onOpenChange={(open) => {
          if (!open) {
            setConfirmingRole(null);
            setPendingGroups([]);
            setAdminInput("");
          }
        }}
        adminGroups={pendingGroups}
        onConfirm={handleConfirmAdminMapping}
      />
    </>
  );
}

