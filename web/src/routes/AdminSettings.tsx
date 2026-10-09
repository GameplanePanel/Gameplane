import { useInstallation } from "@/lib/useInstallation";
import { useState, useEffect, useRef, type ChangeEvent, type ComponentType, type ReactNode, type SetStateAction } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  AlertTriangle,
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
import { errorText } from "@/lib/errors";
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
    <div className="space-y-6 p-6">
      <PageHeader
        title="Admin settings"
        description="Platform-wide configuration for this Gameplane instance."
      />

      <div className="grid gap-6 lg:grid-cols-[220px_1fr]">
        {/* /admin is gated on config:manage, so every section is reachable. */}
        <SettingsNav active={section} onSelect={selectSection} showAdminSections />

        <div className="space-y-6">
          {cfg.isLoading && (
            <Card>
              <Card.Content className="text-sm text-muted">Loading configuration…</Card.Content>
            </Card>
          )}
          {cfg.isError && (
            <Card>
              <Card.Content className="text-sm text-danger">
                Failed to load configuration. Refresh to retry.
              </Card.Content>
            </Card>
          )}
          {cfg.data && section === "general"       && <GeneralSection       initial={cfg.data.general} />}
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
}: {
  title: string;
  subtitle?: string;
  footer?: ReactNode;
  children: ReactNode;
  className?: string;
}) {
  return (
    <Card className={cn("space-y-4", className)}>
      <Card.Header className="space-y-2 pb-2">
        <div className="font-medium text-base">{title}</div>
        {subtitle && <div className="text-xs text-muted">{subtitle}</div>}
      </Card.Header>
      <Card.Content className="space-y-4">{children}</Card.Content>
      {footer && <Card.Footer className="flex items-center justify-end gap-3 pt-2">{footer}</Card.Footer>}
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
  if (pending) return <span className="text-xs text-muted">Saving…</span>;
  if (error)   return <span className="text-xs text-danger">{error}</span>;
  if (saved)   return <span className="text-xs text-success">Saved</span>;
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
      setError("Could not refresh configuration. Retry the refresh before saving.");
      return;
    }
    const latest = verified.data?.[section] ?? initial;
    if (JSON.stringify(latest) !== JSON.stringify(base)) {
      setError("Configuration changed. Copy your edits and reopen this section before saving.");
      return;
    }
    const changes = staged;
    const list = Object.values(changes);
    setWriting(true);
    try {
      await Promise.all(list.filter((c) => c.kind === "write").map((c) => c.run()));
    } catch (e) {
      setError(errorText(e, "Failed to store a secret"));
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
      onError: (e) => setError(errorText(e, "Save failed")),
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
  const f = useSectionForm<GeneralCfg>(initial ?? defaultGeneral, "general");
  return (
    <SectionCard
      title="General"
      subtitle="Basic identity for this Gameplane instance."
      footer={
        <>
          <SaveStatus pending={f.pending} error={f.error} saved={f.saved} />
          <Button variant="primary" onPress={f.save} isDisabled={f.pending}>Save changes</Button>
        </>
      }
    >
      <Field label="Instance name" hint="Optional. Names this install on the Cluster page and in notifications.">
        <HeroInput
          value={f.draft.instanceName}
          onChange={(e) => f.update({ instanceName: e.target.value })}
        />
      </Field>
      <Field label="External URL" hint="Canonical base URL, used in emails, webhooks, OIDC callbacks.">
        <HeroInput
          value={f.draft.externalURL}
          onChange={(e) => f.update({ externalURL: e.target.value })}
        />
      </Field>
      <Field label="Default namespace" hint="Where new GameServers land by default.">
        <HeroInput
          value={f.draft.defaultNamespace}
          onChange={(e) => f.update({ defaultNamespace: e.target.value })}
        />
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
        title="Authentication"
        subtitle="Built-in local accounts plus federated identity providers. Changes take effect on save — no restart needed."
        footer={
          <>
            <SaveStatus pending={f.pending} error={f.error} saved={f.saved} />
            <Button variant="primary" onPress={f.save} isDisabled={f.pending}>Save changes</Button>
          </>
        }
      >
        {f.draft.providers.length === 0 && !helm && (
          <div className="text-sm text-muted">No identity providers configured yet.</div>
        )}
        <ul className="divide-y divide-border">
          {f.draft.providers.map((p, idx) => (
            <li key={p.name} className="flex items-center gap-3 py-3">
              <div className="flex h-8 w-8 items-center justify-center rounded-md bg-surface">
                <Key className="h-4 w-4 text-muted" />
              </div>
              <div className="min-w-0 flex-1">
                <div className="text-sm">{p.displayName || p.name}</div>
                <div className="truncate text-xs text-muted">
                  {p.kind}
                  {p.issuer ? ` · ${p.issuer}` : p.configRef ? ` · ${p.configRef}` : ""}
                </div>
              </div>
              <button
                type="button"
                onClick={togglerFor(idx)}
                disabled={p.enabled && lastToggleLocked}
                title={
                  p.enabled && lastToggleLocked
                    ? "At least one identity provider must stay enabled."
                    : undefined
                }
                className={cn(
                  "rounded px-2 py-0.5 text-[10px] font-mono uppercase disabled:cursor-not-allowed disabled:opacity-60",
                  p.enabled ? "bg-success/15 text-success" : "bg-muted/15 text-muted",
                )}
              >
                {p.enabled ? "Enabled" : "Disabled"}
              </button>
              {p.kind !== "local" && (
                <Button
                  variant="ghost"
                  isIconOnly
                  aria-label={`Delete provider ${p.name}`}
                  onPress={() => removeProvider(idx)}
                >
                  <Trash2 className="h-4 w-4" />
                </Button>
              )}
            </li>
          ))}
          {helm && (
            <li className="flex items-center gap-3 py-3">
              <div className="flex h-8 w-8 items-center justify-center rounded-md bg-surface">
                <Lock className="h-4 w-4 text-muted" />
              </div>
              <div className="min-w-0 flex-1">
                <div className="text-sm">{helm.label}</div>
                <div className="truncate text-xs text-muted">
                  oidc · configured via Helm — manage in values.yaml
                </div>
              </div>
              <span className="rounded bg-success/15 px-2 py-0.5 text-[10px] font-mono uppercase text-success">
                Enabled
              </span>
            </li>
          )}
        </ul>
        {lastToggleLocked && (
          <p className="mt-2 text-xs text-muted">
            At least one identity provider must stay enabled — the last enabled
            provider can&apos;t be turned off.
          </p>
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
          <Button variant="outline" size="sm" onPress={() => setAdding(true)}>
            <Plus className="mr-1.5 h-4 w-4" />
            Add provider
          </Button>
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
  return (
    <div className="rounded-md border border-warning-soft-foreground bg-warning-soft p-3 flex gap-3">
      <Megaphone className="h-4 w-4 text-warning-soft-foreground flex-shrink-0 mt-0.5" />
      <div className="text-xs">
        <div className="font-medium text-warning-soft-foreground mb-1">Full admin access</div>
        <p className="text-warning-soft-foreground">
          Groups added here will be mapped to the admin role and get full cluster
          control from their next login. You&apos;ll be asked to confirm before this is saved.
        </p>
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
      <div className="text-sm font-medium">Add identity provider</div>
      {!externalURLSet && (
        <Alert status="warning">
          <Alert.Content className="text-xs">
            Set <span className="font-mono">General → External URL</span> first — it forms the
            provider&apos;s OIDC redirect URL.
          </Alert.Content>
        </Alert>
      )}
      <div className="grid gap-3 md:grid-cols-2">
        <TextField>
          <Label className="text-xs">Kind</Label>
          <Select
            aria-label="Provider kind"
            selectedKey={kind}
            onSelectionChange={(key) => applyPreset(key as AuthKind)}
          >
            <Select.Trigger className="w-full rounded border border-border bg-surface px-3 py-2 text-sm hover:bg-surface/80">
              <Select.Value />
              <Select.Indicator className="ml-auto h-4 w-4" />
            </Select.Trigger>
            <Select.Popover className="rounded border border-border">
              <ListBox aria-label="Provider kind">
                <ListBoxItem id="oidc">Generic OIDC</ListBoxItem>
                <ListBoxItem id="google">Google</ListBoxItem>
                <ListBoxItem id="github">GitHub (via OIDC bridge)</ListBoxItem>
              </ListBox>
            </Select.Popover>
          </Select>
          {kind === "github" && (
            <Description className="text-[11px]">
              GitHub.com has no OIDC discovery for user login — run an OIDC bridge such as
              Dex and enter its issuer URL below.
            </Description>
          )}
        </TextField>
        <TextField>
          <Label className="text-xs">Name</Label>
          <HeroInput
            placeholder="corp-sso"
            value={name}
            onChange={(e) => setName(e.target.value)}
          />
          <Description className="text-[11px]">
            A short lowercase identifier (letters, digits, and dashes) used in the login route and the Secret name.
          </Description>
        </TextField>
        <TextField>
          <Label className="text-xs">Display name (login button)</Label>
          <HeroInput
            placeholder="Acme SSO"
            value={displayName}
            onChange={(e) => setDisplayName(e.target.value)}
          />
        </TextField>
        <TextField>
          <Label className="text-xs">Issuer URL</Label>
          <HeroInput
            placeholder="https://idp.example.com"
            value={issuer}
            onChange={(e) => setIssuer(e.target.value)}
          />
        </TextField>
        <TextField>
          <Label className="text-xs">Client ID</Label>
          <HeroInput value={clientID} onChange={(e) => setClientID(e.target.value)} />
        </TextField>
        <TextField>
          <Label className="text-xs">Client secret</Label>
          <HeroInput
            type="password"
            value={clientSecret}
            onChange={(e) => setClientSecret(e.target.value)}
          />
        </TextField>
        <TextField>
          <Label className="text-xs">Scopes (optional)</Label>
          <HeroInput value={scopes} onChange={(e) => setScopes(e.target.value)} />
          <Description className="text-[11px]">
            Extra OAuth scopes beyond <span className="font-mono">openid profile email</span> —
            like <span className="font-mono">groups</span>. Space- or comma-separated.
          </Description>
        </TextField>
        <TextField>
          <Label className="text-xs">Groups claim (optional)</Label>
          <HeroInput
            placeholder="groups"
            value={groupsClaim}
            onChange={(e) => setGroupsClaim(e.target.value)}
          />
          <Description className="text-[11px]">
            ID-token claim holding the user&apos;s group memberships; defaults to{" "}
            <span className="font-mono">groups</span>.
          </Description>
        </TextField>
      </div>
      <div className="space-y-3 border-t border-border pt-3">
        <div>
          <div className="text-sm font-medium">Role mapping</div>
          <p className="pt-0.5 text-xs text-muted">
            Map IdP groups (comma-separated) to dashboard roles. A user&apos;s highest
            matching role wins (admin &gt; operator &gt; viewer), and roles re-sync on
            every login while any mapping is set.
          </p>
        </div>
        <div className="grid gap-3 md:grid-cols-2">
          <div className="flex flex-col gap-3">
            <TextField>
              <Label className="text-xs">Admin groups</Label>
              <HeroInput
                placeholder="gameplane-admins"
                value={adminGroups}
                onChange={(e) => setAdminGroups(e.target.value)}
              />
            </TextField>
            {adminList.length > 0 && <AdminGroupsInlineWarning />}
          </div>
          <TextField>
            <Label className="text-xs">Operator groups</Label>
            <HeroInput
              placeholder="gameplane-operators"
              value={operatorGroups}
              onChange={(e) => setOperatorGroups(e.target.value)}
            />
          </TextField>
          <TextField>
            <Label className="text-xs">Viewer groups</Label>
            <HeroInput
              placeholder="gameplane-viewers"
              value={viewerGroups}
              onChange={(e) => setViewerGroups(e.target.value)}
            />
          </TextField>
          <TextField>
            <Label className="text-xs">Default role</Label>
            <Select
              aria-label="Default role"
              selectedKey={defaultRole}
              onSelectionChange={(key) => setDefaultRole((key ?? "") as AuthDefaultRole | "")}
              isDisabled={!hasMappings}
            >
              <Select.Trigger className="w-full rounded border border-border bg-surface px-3 py-2 text-sm hover:bg-surface/80">
                <Select.Value />
                <Select.Indicator className="ml-auto h-4 w-4" />
              </Select.Trigger>
              <Select.Popover className="rounded border border-border">
                <ListBox aria-label="Default role">
                  <ListBoxItem id="">(no mapping)</ListBoxItem>
                  <ListBoxItem id="viewer">Viewer</ListBoxItem>
                  <ListBoxItem id="operator">Operator</ListBoxItem>
                  <ListBoxItem id="admin">Admin</ListBoxItem>
                  <ListBoxItem id="deny">Deny sign-in</ListBoxItem>
                </ListBox>
              </Select.Popover>
            </Select>
            <Description className="text-[11px]">
              Applied when no group matches. &ldquo;Deny sign-in&rdquo; refuses logins
              without a mapped group.
            </Description>
          </TextField>
        </div>
      </div>
      <div className="flex justify-end gap-2 pt-1">
        <Button variant="ghost" onPress={onClose}>
          Cancel
        </Button>
        <Button variant="primary" isDisabled={!valid} onPress={handleSubmitClick}>
          Add provider
        </Button>
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
  const installation = useInstallation();
  const selected = useCurrentCluster();
  const registry = useQuery({
    queryKey: ["clusters"], queryFn: () => Clusters.list(),
    enabled: installation.data?.standalone === true,
  });
  if (installation.isPending) return <LoadingCard message="Loading installation…" />;
  if (installation.isError) return <ErrorCard message="Couldn't load installation capabilities." onRetry={() => void installation.refetch()} />;
  if (!installation.data.standalone) return <ClusterBackupDestSection cluster="local" />;
  const target = registry.data?.items.find((cluster) => cluster.name === selected);
  return <div className="space-y-4">
    <ClusterSelector />
    {registry.isPending ? <LoadingCard message="Loading clusters…" />
      : registry.isError ? <ErrorCard message="Couldn't load registered clusters." onRetry={() => void registry.refetch()} />
      : target ? <ClusterBackupDestSection key={target.name} cluster={target.name} />
      : <SectionCard title="Select a workload cluster" subtitle="Backup destinations are stored on the workload cluster that runs your servers.">
        <p className="text-sm text-muted">Choose a registered cluster above, or register one on the Clusters page.</p>
      </SectionCard>}
  </div>;
}

function ClusterBackupDestSection({ cluster }: { cluster: string }) {
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
      title="Backup destinations"
      subtitle="Restic repositories for snapshots. Stored as labelled Kubernetes Secrets in the configured namespace."
      footer={
        <Button onPress={() => setAdding(true)} isDisabled={adding}>
          <Plus className="h-4 w-4" />
          Add destination
        </Button>
      }
    >
      {list.isLoading && <div className="text-sm text-muted">Loading destinations…</div>}
      {list.isError && <ErrorBanner err={list.error} />}
      {!list.isLoading && items.length === 0 && !adding && (
        <div className="text-sm text-muted">
          No backup destinations configured. Add one to enable snapshots.
        </div>
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
              {d.hasPassword ? "configured" : "no-password"}
            </span>
            {d.createdAt && (
              <span className="text-[11px] text-muted">{formatRelative(d.createdAt)}</span>
            )}
            <button
              type="button"
              aria-label={`Delete ${d.name}`}
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
        title="Delete backup destination?"
        description={
          <>
            <p>
              The repository at this destination will <strong>not</strong> be erased — only the
              credentials Gameplane uses to reach it. Existing backups remain intact;
              new backups against this destination will fail until it&apos;s re-added.
            </p>
            {deleting && (
              <p className="pt-2">
                Type <span className="font-mono">{deleting}</span> to confirm.
              </p>
            )}
          </>
        }
        confirmPhrase={deleting ?? undefined}
        confirmLabel="Delete"
        destructive
        busy={remove.isPending}
        onConfirm={() => deleting && remove.mutate(deleting)}
      />
    </SectionCard>
  );
}

function NewDestinationForm({ cluster, onClose }: { cluster: string; onClose: () => void }) {
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
      <div className="text-sm font-medium">Add destination</div>
      <p className="text-xs text-muted">
        Stored as a Secret labelled <span className="font-mono">gameplane.local/backup-destination=true</span>.
        Restic URL formats: <span className="font-mono">s3:host/bucket</span>, <span className="font-mono">b2:bucket</span>, <span className="font-mono">azure:account/container</span>, etc.
      </p>
      <div className="grid gap-3 md:grid-cols-2">
        <Field label="Name (DNS label)">
          <HeroInput
            placeholder="gameplane-backup-repo"
            value={form.name}
            onChange={(e) => setForm({ ...form, name: e.target.value })}
          />
        </Field>
        <Field label="Restic URL">
          <HeroInput
            placeholder="s3:s3.example.com/gameplane-bucket"
            value={form.url}
            onChange={(e) => setForm({ ...form, url: e.target.value })}
          />
        </Field>
        <Field label="Repository password">
          <HeroInput
            type="password"
            placeholder="Strong, unique passphrase"
            value={form.password}
            onChange={(e) => setForm({ ...form, password: e.target.value })}
          />
        </Field>
      </div>
      {create.error && <ErrorBanner err={create.error} />}
      <div className="flex justify-end gap-2 pt-1">
        <Button variant="ghost" onPress={onClose}>Cancel</Button>
        <Button
          onPress={() => create.mutate()}
          isDisabled={!valid || create.isPending}
        >
          {create.isPending ? "Saving…" : "Save destination"}
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
        {replacing ? "Replace" : "Set"} API key — {label}
      </div>
      <Field label="API key">
        <HeroInput
          type="password"
          autoFocus
          value={apiKey}
          onChange={(e) => setApiKey(e.target.value)}
          spellCheck={false}
        />
      </Field>
      <div className="flex justify-end gap-2 pt-1">
        <Button variant="ghost" onPress={onClose}>Cancel</Button>
        <Button isDisabled={apiKey.trim() === ""} onPress={submit}>
          Save
        </Button>
      </div>
    </div>
  );
}

function ModRegistriesSection({ initial }: { initial?: ModRegistriesCfg }) {
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
      title="Mod registries"
      subtitle="Registries that require an API key stay hidden from the Mods browser until a key is configured."
      footer={
        <>
          <SaveStatus pending={f.pending} error={f.error} saved={f.saved} />
          <Button onPress={f.save} isDisabled={f.pending}>Save changes</Button>
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
                  {configured ? "Active in the Mods browser" : "Hidden from the Mods browser"}
                </div>
              </div>
              <span
                className={cn(
                  "flex shrink-0 items-center gap-1.5 rounded-full px-2.5 py-1 text-xs",
                  configured ? "bg-primary/15 text-primary" : "bg-muted/15 text-muted",
                )}
              >
                <span className="h-1.5 w-1.5 rounded-full bg-current" />
                {configured ? "Configured" : "Not configured"}
              </span>
              {editing !== provider && (
                configured ? (
                  <>
                    <Button variant="primary" size="sm" onPress={() => setEditing(provider)}>
                      Replace
                    </Button>
                    <Button variant="danger" size="sm" onPress={() => removeEntry(provider)}>
                      Remove
                    </Button>
                  </>
                ) : (
                  <Button variant="primary" size="sm" onPress={() => setEditing(provider)}>
                    Set API key
                  </Button>
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
      <p className="text-xs text-muted">
        Always available: Modrinth, Thunderstore, Hangar, Factorio, Spigot, GitHub, uMod
      </p>
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
  if (!events || events.length === 0) return <EventChip label="default events" />;
  return (
    <>
      {events.slice(0, 2).map((e) => (
        <EventChip key={e} label={e} />
      ))}
      {events.length > 2 && <EventChip label={`+${events.length - 2}`} />}
    </>
  );
}

// The managed-Secret name the API derives for a sink (mirrors
// sinkSecretPrefix in api/internal/handlers/notifications.go). The
// configRef must stay a DNS label (≤63), which caps the sink name.
const sinkSecretPrefix = "gameplane-notify-";
const maxSinkName = 63 - sinkSecretPrefix.length;

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
    onAdd({ name, kind, enabled: true, configRef: sinkSecretPrefix + name, events }, body);
    onClose();
  };

  return (
    <div className="space-y-3 rounded-md border border-border bg-surface/30 p-4">
      <div className="text-sm font-medium">Add sink</div>
      <div className="grid gap-3 md:grid-cols-2">
        <Field label="Name">
          <HeroInput
            placeholder="team-alerts"
            value={name}
            onChange={(e) => setName(e.target.value)}
          />
          <span className="text-[11px] text-muted">
            A short lowercase identifier (letters, digits, and dashes) used to
            name the sink and its Secret — like <span className="font-mono">team-alerts</span>.
          </span>
        </Field>
        <Field label="Kind">
          <Select aria-label="Sink kind" selectedKey={kind} onSelectionChange={(key) => setKind(key as SinkKind)}>
            <Select.Trigger className="w-full rounded border border-border bg-surface px-3 py-2 text-sm hover:bg-surface/80">
              <Select.Value />
              <Select.Indicator className="ml-auto h-4 w-4" />
            </Select.Trigger>
            <Select.Popover className="rounded border border-border">
              <ListBox aria-label="Sink kind">
                {(["discord", "slack", "smtp", "webhook", "ntfy"] as const).map((k) => (
                  <ListBoxItem key={k} id={k}>
                    {k}
                  </ListBoxItem>
                ))}
              </ListBox>
            </Select.Popover>
          </Select>
        </Field>
        {kind === "smtp" ? (
          <>
            <Field label="SMTP host">
              <HeroInput placeholder="mail.example.com" value={cred("host")} onChange={setCred("host")} />
            </Field>
            <Field label="Port (default 587)">
              <HeroInput placeholder="587" value={cred("port")} onChange={setCred("port")} />
            </Field>
            <Field label="Username (optional)">
              <HeroInput value={cred("username")} onChange={setCred("username")} />
            </Field>
            <Field label="Password (optional)">
              <HeroInput type="password" value={cred("password")} onChange={setCred("password")} />
            </Field>
            <Field label="From address">
              <HeroInput placeholder="gameplane@example.com" value={cred("from")} onChange={setCred("from")} />
            </Field>
            <Field label="To (comma-separated)">
              <HeroInput placeholder="ops@example.com" value={cred("to")} onChange={setCred("to")} />
            </Field>
            <Field label="TLS">
              <Select
                aria-label="SMTP TLS mode"
                selectedKey={cred("tls")}
                onSelectionChange={(key) => setCreds((c) => ({ ...c, tls: String(key ?? "") }))}
              >
                <Select.Trigger className="w-full rounded border border-border bg-surface px-3 py-2 text-sm hover:bg-surface/80">
                  <Select.Value />
                  <Select.Indicator className="ml-auto h-4 w-4" />
                </Select.Trigger>
                <Select.Popover className="rounded border border-border">
                  <ListBox aria-label="SMTP TLS mode">
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
            <Field label={kind === "ntfy" ? "Topic URL" : "Webhook URL"}>
              <HeroInput
                placeholder={
                  kind === "ntfy"
                    ? "https://ntfy.sh/my-topic"
                    : kind === "discord"
                      ? "https://discord.com/api/webhooks/…"
                      : kind === "slack"
                        ? "https://hooks.slack.com/services/…"
                        : "https://example.com/hook"
                }
                value={cred("url")}
                onChange={setCred("url")}
              />
            </Field>
            {kind === "ntfy" && (
              <Field label="Access token (optional)">
                <HeroInput type="password" placeholder="tk_…" value={cred("token")} onChange={setCred("token")} />
              </Field>
            )}
            {kind === "webhook" && (
              <Field label="Authorization header (optional)">
                <HeroInput type="password" placeholder="Bearer …" value={cred("authorization")} onChange={setCred("authorization")} />
              </Field>
            )}
          </>
        )}
      </div>
      <div className="space-y-1.5">
        <div className="text-xs text-muted">Events (failures and recovery are pre-selected)</div>
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
        <Button variant="ghost" onPress={onClose}>Cancel</Button>
        <Button isDisabled={!nameOk || !credsOk} onPress={submit}>
          Add sink
        </Button>
      </div>
    </div>
  );
}

function TestButton({ dirty, disabled, onPress, children }: { dirty: boolean; disabled: boolean; onPress: () => void; children: ReactNode }) {
  const ref = useRef<HTMLButtonElement>(null);
  useEffect(() => {
    if (ref.current) {
      if (dirty) {
        ref.current.setAttribute("title", "Save changes first — tests run against the saved config");
      } else {
        ref.current.removeAttribute("title");
      }
    }
  }, [dirty]);

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

  const runTest = (name: string) =>
    test.mutate(name, {
      onSuccess: () =>
        setTestResults((r) => ({ ...r, [name]: { ok: true, message: "delivered" } })),
      onError: (e) =>
        setTestResults((r) => ({
          ...r,
          [name]: { ok: false, message: errorText(e, "delivery failed") },
        })),
    });

  return (
    <SectionCard
      title="Notifications"
      subtitle="Deliver server health and backup/restore events to Discord, Slack, ntfy, email, or webhooks. Credentials are entered when adding a sink and stored as a labelled Secret in the control-plane namespace."
      footer={
        <>
          <SaveStatus pending={f.pending} error={f.error} saved={f.saved} />
          <Button onPress={f.save} isDisabled={f.pending}>Save changes</Button>
        </>
      }
    >
      {f.draft.sinks.length === 0 && !adding && (
        <div className="text-sm text-muted">No notification sinks configured.</div>
      )}
      <ul className="divide-y divide-border">
        {f.draft.sinks.map((s, idx) => {
          const Icon = sinkIcons[s.kind] ?? Bell;
          const result = testResults[s.name];
          return (
            <li key={s.name} className="flex items-center gap-3 py-3">
              <div className="flex h-8 w-8 shrink-0 items-center justify-center rounded-md bg-surface">
                <Icon className="h-4 w-4 text-muted" />
              </div>
              <div className="min-w-0 flex-1">
                <div className="text-sm">{s.name}</div>
                <div className="truncate text-xs text-muted">
                  {s.kind}
                  {s.configRef ? ` · Secret: ${s.configRef}` : " · no Secret configured"}
                </div>
              </div>
              {s.configRef ? (
                <div className="hidden items-center gap-1.5 lg:flex">
                  <EventChips events={s.events} />
                </div>
              ) : (
                <span className="rounded-full bg-warning/15 px-2 py-0.5 text-[11px] font-medium text-warning">
                  Needs secret
                </span>
              )}
              {result && (
                <span
                  className={cn(
                    "max-w-48 truncate text-[11px]",
                    result.ok ? "text-success" : "text-danger",
                  )}
                >
                  {result.ok ? "✓ delivered" : result.message}
                </span>
              )}
              <TestButton
                dirty={dirty}
                disabled={!s.configRef || dirty || test.isPending}
                onPress={() => runTest(s.name)}
              >
                Send test
              </TestButton>
              <Switch
                aria-label={s.enabled ? `Disable sink ${s.name}` : `Enable sink ${s.name}`}
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
                aria-label={`Delete sink ${s.name}`}
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
          <Plus className="mr-1.5 h-4 w-4" />
          Add sink
        </Button>
      )}
    </SectionCard>
  );
}

const defaultTelemetry: TelemetryCfg = { sendMetrics: false, extended: false };

const TELEMETRY_KEY = ["telemetry"] as const;

// Intl output is "Oct 6, 2026, 09:12" for these options; the section always
// renders UTC so the text matches what the receiver stores.
function formatUtc(iso: string, withTime: boolean): string {
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return iso;
  const text = new Intl.DateTimeFormat("en-US", {
    month: "short",
    day: "numeric",
    year: "numeric",
    ...(withTime ? { hour: "2-digit", minute: "2-digit", hourCycle: "h23" as const } : {}),
    timeZone: "UTC",
  }).format(d);
  return withTime ? `${text} UTC` : text;
}

const destinationCopy: Record<
  Exclude<TelemetryInfo["destination"]["kind"], "disabled" | "none">,
  { detail: string; badge: string }
> = {
  default: { detail: "The Gameplane project's telemetry service.", badge: "Project default" },
  custom: {
    detail: "Custom destination set by your operator. Nothing is sent to the Gameplane project.",
    badge: "Custom",
  },
  bundled: {
    detail: "The receiver bundled with this install. Reports stay inside your cluster.",
    badge: "Bundled",
  },
};

function TelemetryDestinationBlock({ info }: { info: TelemetryInfo }) {
  const kind = info.destination.kind;
  if (info.operatorDisabled || kind === "disabled") {
    return (
      <div className="flex items-start gap-3 rounded-md border border-warning/40 bg-warning/10 p-3">
        <CircleHelp className="mt-0.5 h-4 w-4 shrink-0 text-warning-soft-foreground" />
        <div className="flex-1">
          <div className="text-sm font-medium">Telemetry is disabled by the operator.</div>
          <div className="pt-0.5 text-xs text-muted">
            No data of either kind is sent and no telemetry destination is contacted, whatever the
            switches below say. Only an operator can change this, at install time.
          </div>
        </div>
        <span className="rounded bg-warning/20 px-2 py-0.5 text-xs text-warning-soft-foreground">Disabled</span>
      </div>
    );
  }
  if (kind === "none") {
    return (
      <div className="flex items-start gap-3 rounded-md bg-surface p-3">
        <CircleHelp className="mt-0.5 h-4 w-4 shrink-0 text-accent" />
        <div className="flex-1">
          <div className="text-sm font-medium">No telemetry destination configured.</div>
          <div className="pt-0.5 text-xs text-muted">
            Nothing is sent. An operator can set a custom or bundled destination at install time.
          </div>
        </div>
        <span className="rounded bg-background px-2 py-0.5 text-xs">No destination</span>
      </div>
    );
  }
  const copy = destinationCopy[kind];
  const Icon = kind === "bundled" ? House : kind === "custom" ? CircleHelp : Globe;
  return (
    <div className="flex items-start gap-3 rounded-md bg-surface p-3">
      <Icon className="mt-0.5 h-4 w-4 shrink-0 text-accent" />
      <div className="flex-1">
        <div className="text-sm font-medium">Sending to {info.destination.host}</div>
        <div className="pt-0.5 text-xs text-muted">{copy.detail}</div>
      </div>
      <span className="rounded bg-background px-2 py-0.5 text-xs">{copy.badge}</span>
      <a
        href={TELEMETRY_STATEMENT_URL}
        target="_blank"
        rel="noreferrer"
        className="text-xs text-accent hover:underline"
      >
        Data-handling statement
      </a>
    </div>
  );
}

function TelemetryPreviewBlock({ info }: { info: TelemetryInfo }) {
  let empty: string | null = null;
  if (info.operatorDisabled || info.destination.kind === "disabled") {
    empty = "Nothing to preview: telemetry is disabled by the operator.";
  } else if (info.destination.kind === "none") {
    empty = "Nothing to preview: no destination is configured.";
  } else if (!info.preview) {
    empty = "Nothing to preview: basic usage metrics are off.";
  }
  return (
    <div className="space-y-1.5">
      <div className="text-sm font-medium">Next report preview</div>
      <div className="text-xs text-muted">Exactly what the next report contains. Read-only.</div>
      {empty || !info.preview ? (
        <div className="rounded-md border border-border p-3 text-xs text-muted">{empty}</div>
      ) : (
        <pre
          role="region"
          aria-label="Next report JSON"
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
  const { status } = info;
  let text: string;
  if (status.lastOutcome === "ok") {
    text = `Last report sent successfully on ${status.lastSuccessAt ? formatUtc(status.lastSuccessAt, true) : "an earlier attempt"}.`;
  } else if (status.lastOutcome === "failed") {
    text = status.lastSuccessAt
      ? `Last attempt failed, will retry. Last successful report: ${formatUtc(status.lastSuccessAt, true)}.`
      : "Last attempt failed, will retry. No report has been delivered yet.";
  } else {
    text = "No report sent yet.";
  }
  return (
    <div className="space-y-1 text-xs text-muted">
      <div>{text}</div>
      {status.lastIdRotationAt && (
        <div>
          Install ID replaced on {formatUtc(status.lastIdRotationAt, false)}: the destination
          reported it was in use by another key.
        </div>
      )}
    </div>
  );
}

function TelemetrySection({ initial }: { initial?: TelemetryCfg }) {
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
      title="Telemetry"
      subtitle="Anonymous usage metrics help us prioritize work. Extended metrics include a random install ID."
      footer={
        <>
          <SaveStatus pending={f.pending} error={f.error} saved={f.saved} />
          <Button onPress={f.save} isDisabled={f.pending || operatorDisabled}>Save changes</Button>
        </>
      }
    >
      {info && <TelemetryDestinationBlock info={info} />}
      <div className="flex items-center justify-between">
        <div>
          <div className="text-sm">Send basic usage metrics</div>
          <div className="pt-0.5 text-xs text-muted">
            Gameplane version, number of servers and number of templates.
          </div>
        </div>
        <TelemetrySwitch
          ariaLabel={basicOn ? "Disable telemetry" : "Enable telemetry"}
          isSelected={basicOn}
          isDisabled={operatorDisabled}
          // Turning basic off turns extended off with it (spec Q7).
          onChange={(v) => f.update(v ? { sendMetrics: true } : { sendMetrics: false, extended: false })}
        />
      </div>
      <div className="flex items-center justify-between pl-8">
        <div>
          <div className="text-sm">Send extended usage metrics</div>
          <div className="pt-0.5 text-xs text-muted">
            {!operatorDisabled && !basicOn
              ? "Turn on basic usage metrics to enable extended usage metrics."
              : "Adds Kubernetes version, distribution, CPU architecture, node-count band, servers per official game module, which optional features are in use, and a random install ID."}
          </div>
        </div>
        <TelemetrySwitch
          ariaLabel={extendedOn ? "Disable extended telemetry" : "Enable extended telemetry"}
          isSelected={extendedOn}
          isDisabled={operatorDisabled || !basicOn}
          onChange={(v) => f.update({ extended: v })}
        />
      </div>
      <div className="flex items-center justify-between pl-8">
        <div>
          <div className="text-sm">Install ID</div>
          {installId ? (
            <>
              <div className="font-mono text-xs">{installId}</div>
              <div className="pt-0.5 text-xs text-muted">
                Random and anonymous. Reset it to start reporting under a new ID; the old ID is never
                sent again.
              </div>
            </>
          ) : (
            <div className="pt-0.5 text-xs text-muted">No install ID while extended metrics are off.</div>
          )}
        </div>
        <Button
          variant="outline"
          size="sm"
          isDisabled={!installId || operatorDisabled || reset.isPending}
          onPress={() => setConfirmingReset(true)}
        >
          Reset ID
        </Button>
      </div>
      {reset.error && (
        <div className="pl-8 text-xs text-danger">{errorText(reset.error, "Could not reset the install ID")}</div>
      )}
      {info && <TelemetryPreviewBlock info={info} />}
      {info && !operatorDisabled && info.destination.kind !== "none" && <TelemetryStatusLines info={info} />}
      <ConfirmDialog
        open={confirmingReset}
        onOpenChange={setConfirmingReset}
        title="Reset install ID?"
        confirmLabel="Reset ID"
        destructive
        busy={reset.isPending}
        onConfirm={() => reset.mutate()}
        description={`This replaces your random install ID ${installId ?? ""} with a new one. The old ID is never sent again, and the destination cannot link the old and new IDs. The next report is sent under the new ID.`}
      />
    </SectionCard>
  );
}

function UpdatesSection() {
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
      title="Updates"
      subtitle={installation.data?.standalone ? "Update the panel and API container images to upgrade this installation." : "Gameplane is upgraded via Helm (image tags / chart versions)."}
    >
      <div className="flex items-center justify-between">
        <div>
          <div className="text-sm">Channel</div>
          <div className="pt-0.5 text-xs text-muted">
            {installation.data?.standalone ? "Container image tags select the release for this panel." : <>Informational only — reflects <code className="font-mono">updates.channel</code> from your Helm values.</>}
          </div>
        </div>
        <span className="rounded bg-surface px-2 py-0.5 font-mono text-xs">
          {data?.updateChannel || "—"}
        </span>
      </div>
    </SectionCard>
  );
}

function AboutSection() {
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
    <SectionCard title="About" subtitle="This Gameplane build.">
      <dl className="grid grid-cols-[auto_1fr] gap-x-6 gap-y-3 text-sm">
        <dt className="text-muted">Gameplane</dt><dd className="font-mono">{data?.gameplaneVersion || "—"}</dd>
        {installation.data?.localCluster && <><dt className="text-muted">Kubernetes</dt><dd className="font-mono">{data?.version || "—"}</dd></>}
        <dt className="text-muted">License</dt><dd>AGPL-3.0</dd>
      </dl>
    </SectionCard>
  );
}

// T023: Read-only Helm-seeded OIDC provider display (from installTimeSettings.oidcHelmProvider)
function HelmOIDCProviderCard({ provider }: { provider: OIDCHelmProvider }) {
  const hasRoleMappings = provider.roleMappings && Object.keys(provider.roleMappings).length > 0;
  const adminMapped = provider.roleMappings?.admin && provider.roleMappings.admin.length > 0;
  return (
    <SectionCard
      title="Helm-seeded OIDC provider"
      subtitle="Read-only snapshot of the identity provider configured via api.oidc.* Helm values at install time. Connection settings are not editable here; role mappings can be overridden below."
    >
      <div className="space-y-4">
        <div className="space-y-2">
          <div className="font-medium text-sm">Provider</div>
          <p className="text-xs text-muted">
            Groups claim and default role are seeded from the same Helm values and are not editable here.
          </p>
        </div>
        <div className="flex items-center justify-between py-2">
          <span className="text-xs text-muted">Groups claim</span>
          <span className="font-mono text-sm">{provider.groupsClaim}</span>
        </div>
        <div className="flex items-center justify-between py-2">
          <span className="text-xs text-muted">Default role</span>
          <span className="px-2 py-0.5 rounded text-xs font-medium bg-muted/20 text-muted">
            {provider.defaultRole}
          </span>
        </div>
        {hasRoleMappings ? (
          <div className="space-y-3 pt-2">
            <div className="text-xs text-muted">Helm-seeded role mappings</div>
            {["admin", "operator", "viewer"].map((role) => {
              const groups = provider.roleMappings?.[role as keyof typeof provider.roleMappings];
              return (
                <div key={role} className="flex items-start gap-3">
                  <span className="text-xs font-medium w-16 flex-shrink-0 capitalize">{role}</span>
                  <div className="flex flex-wrap gap-2">
                    {groups && groups.length > 0 ? (
                      groups.map((group) => (
                        <span
                          key={group}
                          className="px-2 py-1 rounded text-xs bg-muted/20 text-muted"
                        >
                          {group}
                        </span>
                      ))
                    ) : (
                      <span className="text-xs text-muted italic">—</span>
                    )}
                  </div>
                </div>
              );
            })}
          </div>
        ) : (
          <div className="rounded-md border border-border bg-surface/50 p-4 space-y-2">
            <div className="font-medium text-sm">No OIDC role mappings yet</div>
            <p className="text-xs text-muted">
              Every OIDC user currently signs in with the {provider.defaultRole} default role. Two ways to change that:
              add mappings in Role mapping overrides below — they apply from the next login, with no restart or Helm
              re-run — or seed them at install time with api.oidc.roleMappings.admin / .operator / .viewer in your
              Helm values.
            </p>
          </div>
        )}
        {adminMapped && (
          <div className="rounded-md border border-warning/40 bg-warning/10 p-3 flex gap-3">
            <AlertTriangle className="h-4 w-4 text-warning-soft-foreground flex-shrink-0 mt-0.5" />
            <div className="text-xs">
              <div className="font-medium text-warning-soft-foreground mb-1">Helm-configured admin mapping</div>
              <p className="text-warning-soft-foreground">
                The group(s) on the Admin row above were mapped to admin via Helm values, not the dashboard, so there
                is no confirmation step here. Verify they contain only trusted accounts — anyone in them gets full
                admin access.
              </p>
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
      onError: (err) => onResetError(errorText(err, "Reset failed")),
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
        title="Role mapping overrides"
        subtitle="Override the Helm-seeded OIDC group mappings per role from here. A change takes effect the next time an affected user logs in — no restart or reinstall needed."
        className="bg-card"
        footer={
          <div className="flex items-center justify-between w-full">
            <p className="text-xs text-muted">
              Saved changes take effect on next login for affected users — no restart or reinstall needed.
            </p>
            <div className="flex gap-2">
              {/* No SaveStatus here: this card shares f.error/f.saved with the
                  Authentication card above (same useSectionForm, same PUT), whose
                  own footer already renders it. Duplicating it here means the exact
                  same text (e.g. a save error) appears twice in the DOM, breaking
                  any singular getByText/findByText query for it — see the
                  "surfaces a backend rejection when saving the auth section" test. */}
              <Button variant="outline" onPress={onSave} isDisabled={formState.pending}>
                Save role mappings
              </Button>
            </div>
          </div>
        }
      >
        <div className="space-y-6">
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
              <div key={role} role="group" aria-label={`${role.charAt(0).toUpperCase() + role.slice(1)} role mapping`}>
                {role !== "admin" && <div className="h-px bg-border" />}
                <div className="space-y-3 pt-3">
                  <div className="flex items-center justify-between">
                    <div className="flex items-center gap-2">
                      <span className="font-medium text-sm capitalize">{role}</span>
                      <ProvenanceBadge type={getProvenance(typedRole)} size="sm" />
                    </div>
                    {hasOverride && (
                      <Button
                        variant="ghost"
                        size="sm"
                        aria-label={`Reset to Helm default (${role} role mapping)`}
                        onPress={() => handleReset(typedRole)}
                        isDisabled={resetMutation.isPending}
                      >
                        <RotateCcw className="h-3.5 w-3.5 mr-1.5" />
                        Reset to Helm default
                      </Button>
                    )}
                  </div>

                  {groups.length > 0 ? (
                    <div className="flex flex-wrap gap-2">
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
                    <div className="flex items-center gap-2 px-2.5 py-2 rounded border border-border text-xs text-muted">
                      <CircleSlash2 className="h-3.5 w-3.5" />
                      No groups mapped — nobody signs in as {role} via OIDC.
                    </div>
                  )}

                  <div className="flex gap-2">
                    <HeroInput
                      type="text"
                      placeholder="Add IdP group name…"
                      value={input}
                      onChange={(e) => setInput(e.target.value)}
                      onKeyDown={(e) => {
                        if (e.key === "Enter") {
                          e.preventDefault();
                          handleAddGroup(typedRole, input);
                        }
                      }}
                      className="text-xs"
                    />
                    <Button
                      size="sm"
                      aria-label="Add group"
                      onPress={() => handleAddGroup(typedRole, input)}
                      isDisabled={!input.trim()}
                    >
                      <Plus className="h-3.5 w-3.5" />
                    </Button>
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

