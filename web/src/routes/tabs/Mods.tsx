import { useResourceClient, useResourceTarget, resourceKey, useResourceAccess } from "@/lib/resourceTarget";
import { useEffect, useMemo, useRef, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  ArrowLeft,
  ArrowUpCircle,
  Compass,
  Download,
  Info,
  Package,
  Plus,
  RotateCcw,
  RotateCw,
  TriangleAlert,
  Trash2,
  Upload,
  X,
} from "lucide-react";
import { Button, Input, Chip } from "@heroui/react";

import type {
  GameServer,
  GameTemplate,
  InstalledMod,
  ModID,
  ModMeta,
  ModUpdate,
  RegistryProject,
} from "@/types";

import { APIError } from "@/lib/api";
import { errorTextIntl } from "@/lib/errors";
import { resolveModVolume } from "@/lib/capabilities";
import { ConfirmDialog } from "@/components/ui/ConfirmDialog";
import { RegistryBrowser, RegistryIcon, compactNum, providerLabel } from "@/components/registry-browser";
import { cn, formatBytes, formatRelative } from "@/lib/utils";
import { useOptionalIntl } from "@/lib/i18n/format";
import { defineMessages, type IntlShape, type MessageDescriptor } from "react-intl";

type Banner = { kind: "ok" | "err"; text: string };

// InstallBody is the proxied agent install request: plain URL installs,
// registry installs (meta records provenance in the agent's manifest), and
// in-place upgrades (replaces swaps out the old file).
type InstallBody = { url: string; name?: string; replaces?: string; meta?: ModMeta };

// ModsTab is the Mods tab's entry point. It's rendered only when the
// template declares spec.capabilities.mods (see serverHasMods). Games
// split into two entirely different UIs here: a template that declares
// idList (its server downloads its own mods given a list of ids — ARK's
// CurseForge ids, Project Zomboid's MOD_IDS, Steam Workshop lists) gets
// the id-managed batch editor; every other game keeps the existing
// file-based list/install/upload flow untouched. The branch happens
// before either side calls a hook, so switching modes cleanly unmounts
// one component and mounts the other rather than reordering hooks within
// one.
export function ModsTab({ name, tmpl, gs, ns }: { name: string; tmpl?: GameTemplate; gs?: GameServer; ns?: string }) {
  if (tmpl?.spec.capabilities?.mods?.idList) {
    return <ModsByIdTab name={name} tmpl={tmpl} ns={ns} />;
  }
  return <FileModsTab name={name} tmpl={tmpl} gs={gs} ns={ns} />;
}

// FileModsTab is the generic file-based mod/plugin manager (the original
// ModsTab, unchanged): lists, installs by URL, browses a registry, and
// uploads files into the resolved mods directory.
function FileModsTab({ name, tmpl, gs, ns }: { name: string; tmpl?: GameTemplate; gs?: GameServer; ns?: string }) {
  const intl = useOptionalIntl();
  const resourceTarget = useResourceTarget({ name, namespace: ns });
  const resourceClient = useResourceClient(resourceTarget);
  const { Servers } = resourceClient;
  const qc = useQueryClient();
  const access = useResourceAccess();
  const canManage = access?.canControl === true;

  const caps = tmpl?.spec.capabilities?.mods;
  // URL installs need the module's install (allowlist) block; uploads only
  // need a mods directory, so the install page is reachable whenever the
  // template declares mods at all.
  const canInstall = !!caps?.install;
  // Browse mode is offered when the template declares a registry. The
  // server is authoritative (it 501s when its game has no registry); the
  // dialog surfaces that by letting the user fall back to "From URL".
  const canBrowse = !!caps?.registry;

  // The active version+loader fully implies which mod volume this server
  // manages — surface it as a header label (no selector). For the legacy
  // single-path model this is just the path.
  const active = resolveModVolume(tmpl, gs);
  const activeLabel = active
    ? [active.versionLabel ?? active.loader, active.path].filter(Boolean).join(" · ")
    : null;

  const [browsing, setBrowsing] = useState(false);
  const [confirmRemove, setConfirmRemove] = useState<InstalledMod | null>(null);
  const [banner, setBanner] = useState<Banner | null>(null);

  const { data: mods, isFetching, isError, error: listError, refetch } = useQuery({
    queryKey: resourceKey(resourceTarget, "mods", name, ns),
    queryFn: ({ signal }) => resourceClient.withSignal(signal).Servers.mods(name, ns),
  });

  // Update check is on demand (button), not on mount — it fans out to
  // external registries server-side, so the tab shouldn't trigger it on
  // every visit. Results stay cached for the session.
  const updates = useQuery({
    queryKey: resourceKey(resourceTarget, "mod-updates", name, ns),
    queryFn: ({ signal }) => resourceClient.withSignal(signal).Servers.modUpdates(name, ns),
    enabled: false,
  });
  const updateByName = useMemo(() => {
    const out = new Map<string, ModUpdate>();
    for (const u of updates.data?.updates ?? []) out.set(u.name, u);
    return out;
  }, [updates.data]);

  // Install stays on the browse page (so you can add several); the banner
  // reports each result and the installed list refreshes underneath.
  const install = useMutation({
    mutationFn: (body: InstallBody) => Servers.installMod(name, body, ns),
    onSuccess: (mod, body) => {
      void qc.invalidateQueries({ queryKey: ["fleet"] });
      setBanner({ kind: "ok", text: body.replaces
        ? intl.formatMessage({ id: "web.routes.tabs.mods.banner_updated_to", defaultMessage: "Updated to {name}", description: "Mods: banner shown after a mod is updated to a new file; {name} is the mod name" }, { name: mod.name })
        : intl.formatMessage({ id: "web.routes.tabs.mods.banner_installed", defaultMessage: "Installed {name}", description: "Mods: banner shown after a mod is installed; {name} is the mod name" }, { name: mod.name }) });
      if (body.replaces && updates.data) void updates.refetch();
      return qc.invalidateQueries({ queryKey: resourceKey(resourceTarget, "mods", name) });
    },
    onError: (err) => setBanner({ kind: "err", text: errMsg(err, intl) }),
  });

  // upgradeBody maps one available update to the install request that
  // applies it: the new file lands first, then the old one is removed.
  const upgradeBody = (u: ModUpdate): InstallBody => ({
    url: u.file.downloadUrl,
    name: u.file.filename,
    replaces: u.name,
    meta: {
      provider: u.provider,
      projectId: u.projectId,
      projectName: u.projectName,
      versionId: u.latestVersionId,
      versionNumber: u.latestVersionNumber,
      loader: active?.loader,
    },
  });

  const upload = useMutation({
    mutationFn: (file: File) => Servers.uploadMod(name, file, ns),
    onSuccess: (mod) => {
      void qc.invalidateQueries({ queryKey: ["fleet"] });
      setBanner({ kind: "ok", text: intl.formatMessage({ id: "web.routes.tabs.mods.banner_uploaded", defaultMessage: "Uploaded {name}", description: "Mods: banner shown after a mod file is uploaded; {name} is the mod name" }, { name: mod.name }) });
      return qc.invalidateQueries({ queryKey: resourceKey(resourceTarget, "mods", name) });
    },
    onError: (err) => setBanner({ kind: "err", text: errMsg(err, intl) }),
  });

  const updateAll = useMutation({
    mutationFn: async () => {
      // Sequential on purpose: each install is a download on the agent;
      // parallel requests would just contend on the same volume.
      for (const u of updates.data?.updates ?? []) {
        await Servers.installMod(name, upgradeBody(u), ns);
      }
      return updates.data?.updates.length ?? 0;
    },
    onSuccess: (n) => {
      void qc.invalidateQueries({ queryKey: ["fleet"] });
      setBanner({ kind: "ok", text: intl.formatMessage({ id: "web.routes.tabs.mods.banner_updated_count", defaultMessage: "Updated {count, plural, one {# mod} other {# mods}}", description: "Mods: banner shown after updating mods; {count} is the number of mods updated" }, { count: n }) });
      void updates.refetch();
      return qc.invalidateQueries({ queryKey: resourceKey(resourceTarget, "mods", name) });
    },
    onError: (err) => {
      setBanner({ kind: "err", text: errMsg(err, intl) });
      void updates.refetch();
      return qc.invalidateQueries({ queryKey: resourceKey(resourceTarget, "mods", name) });
    },
  });

  const remove = useMutation({
    mutationFn: (mod: string) => Servers.removeMod(name, mod, ns),
    onSuccess: (_void, mod) => {
      void qc.invalidateQueries({ queryKey: ["fleet"] });
      setConfirmRemove(null);
      setBanner({ kind: "ok", text: intl.formatMessage({ id: "web.routes.tabs.mods.banner_removed", defaultMessage: "Removed {name}", description: "Mods: banner shown after a mod is removed; {name} is the mod file name" }, { name: mod }) });
      return qc.invalidateQueries({ queryKey: resourceKey(resourceTarget, "mods", name) });
    },
    onError: (err) => {
      setConfirmRemove(null);
      setBanner({ kind: "err", text: errMsg(err, intl) });
    },
  });

  // Dedicated install/browse page (replaces the old popup), matching the
  // Modpacks tab. Rendered after all hooks to keep hook order stable.
  const extensions =
    (active?.loader ? caps?.loaders?.[active.loader]?.extensions : undefined) ??
    caps?.extensions ??
    [];

  if (browsing) {
    return (
      <InstallPage
        name={name}
        ns={ns}
        canInstall={canInstall}
        canBrowse={canBrowse}
        allowedHosts={caps?.install?.allowedHosts ?? []}
        extensions={extensions}
        pending={install.isPending}
        uploadPending={upload.isPending}
        banner={banner}
        onDismiss={() => setBanner(null)}
        onBack={() => {
          setBrowsing(false);
          setBanner(null);
        }}
        onInstall={(body) => install.mutate(body)}
        onUpload={(file) => upload.mutate(file)}
      />
    );
  }

  return (
    <div className="space-y-6 p-6">
      <header className="flex items-center justify-between gap-3">
        <div className="space-y-0.5">
          <h2 className="text-sm text-muted">
            {mods
              ? intl.formatMessage({ id: "web.routes.tabs.mods.installed_count", defaultMessage: "{count} installed", description: "Mods: heading with the number of installed mods; {count} is that number" }, { count: mods.length })
              : isError
                ? intl.formatMessage({ id: "web.routes.tabs.mods.couldnt_load", defaultMessage: "Couldn’t load mods", description: "Mods: heading shown when the mod list fails to load" })
                : intl.formatMessage({ id: "web.routes.tabs.mods.loading", defaultMessage: "Loading…", description: "Mods: heading shown while the mod list loads" })}
            {updates.data && updateByName.size > 0 && (
              <span className="text-primary">{intl.formatMessage({ id: "web.routes.tabs.mods.updates_available", defaultMessage: " · {count, plural, one {# update} other {# updates}} available", description: "Mods: shown beside the installed count when mods have updates; {count} is the number of mods with an update" }, { count: updateByName.size })}</span>
            )}
          </h2>
          {(activeLabel || updates.data) && (
            <p className="text-[11px] text-muted" data-testid="mods-active">
              {[activeLabel, updates.data ? `checked ${formatRelative(updates.data.checkedAt, intl)}` : null]
                .filter(Boolean)
                .join(" · ")}
            </p>
          )}
        </div>
        <div className="flex items-center gap-2">
          <Button variant="ghost" size="sm" onPress={() => void refetch()} isDisabled={isFetching} aria-label={intl.formatMessage({ id: "web.routes.tabs.mods.refresh", defaultMessage: "Refresh", description: "Mods tab: icon-only button that reloads the mod list and checks for updates; read by screen readers; keep it short" })}>
            <RotateCw className={cn("h-3 w-3", isFetching && "animate-spin")} />
          </Button>
          {canInstall && (
            <Button
              variant="outline"
              size="sm"
              onPress={() => void updates.refetch()}
              isDisabled={updates.isFetching || !mods?.some((m) => m.meta && m.meta.provider !== "upload")}
            >
              <RotateCw className={cn("h-3 w-3", updates.isFetching && "animate-spin")} /> {updates.isFetching ? intl.formatMessage({ id: "web.routes.tabs.mods.checking", defaultMessage: "Checking…", description: "Mods: label of the check-for-updates button while checking" }) : intl.formatMessage({ id: "web.routes.tabs.mods.check_updates", defaultMessage: "Check updates", description: "Mods: label of the button that checks installed mods for updates" })}
            </Button>
          )}
          {canInstall && canManage && updateByName.size > 0 && (
            <Button
              size="sm"
              variant="secondary"
              onPress={() => updateAll.mutate()}
              isDisabled={updateAll.isPending || install.isPending}
            >
              <ArrowUpCircle className="h-4 w-4" /> {updateAll.isPending ? intl.formatMessage({ id: "web.routes.tabs.mods.updating", defaultMessage: "Updating…", description: "Mods: label of the update-all button while updates run" }) : intl.formatMessage({ id: "web.routes.tabs.mods.update_all_count", defaultMessage: "Update all ({count})", description: "Mods: label of the button that updates every mod; {count} is how many mods have an update" }, { count: updateByName.size })}
            </Button>
          )}
          {/* The disabled reason lives on a wrapping element's title (not
              the Button's aria-label) so the button's accessible name
              stays its visible text, "Install mod", for role queries and
              assistive tech alike. */}
          <span title={canManage ? undefined : intl.formatMessage({ id: "web.routes.tabs.mods.requires_operator_role", defaultMessage: "Requires operator role", description: "Mods: tooltip on a disabled control for users without the operator role" })} tabIndex={0}>
            <Button
              size="sm"
              onPress={() => setBrowsing(true)}
              isDisabled={!canManage}
            >
              <Plus className="h-4 w-4" /> {intl.formatMessage({ id: "web.routes.tabs.mods.install_mod", defaultMessage: "Install mod", description: "Mods tab: button that opens the form to add a mod; disabled for users without the operator role; keep it short" })}</Button>
          </span>
        </div>
      </header>

      {banner && (
        <div
          className={cn(
            "rounded border px-3 py-2 text-sm",
            banner.kind === "ok"
              ? "border-border bg-surface/40 text-fg"
              : "border-danger/40 bg-danger/10 text-danger",
          )}
        >
          <div className="flex items-start justify-between gap-3">
            <span className="font-mono break-all">{banner.text}</span>
            <Button variant="ghost" size="sm" onPress={() => setBanner(null)} isIconOnly aria-label={intl.formatMessage({ id: "web.routes.tabs.mods.dismiss", defaultMessage: "dismiss", description: "Mods tab: icon-only button that closes the message banner; read by screen readers; keep it short" })}>
              <X className="h-3 w-3" />
            </Button>
          </div>
        </div>
      )}

      {isError && !mods && (
        <div className="rounded border border-danger/40 bg-danger/10 px-3 py-2 text-sm text-danger">
          {errMsg(listError, intl)}{intl.formatMessage({ id: "web.routes.tabs.mods.error_retry_separator", defaultMessage: " · ", description: "Mods: separator between the list error and the retry button" })}<Button variant="ghost" size="sm" className="underline" onPress={() => void refetch()} aria-label={intl.formatMessage({ id: "web.routes.tabs.mods.retry", defaultMessage: "retry", description: "Mods: label of the retry button after a failed list load" })}>{intl.formatMessage({ id: "web.routes.tabs.mods.retry", defaultMessage: "retry", description: "Mods: label of the retry button after a failed list load" })}</Button>
        </div>
      )}

      {mods && mods.length === 0 ? (
        <div className="flex flex-col items-center gap-2 rounded-lg border border-dashed border-border py-12 text-center">
          <Package className="h-6 w-6 text-muted" />
          <p className="text-sm text-muted">{intl.formatMessage({ id: "web.routes.tabs.mods.no_mods_installed", defaultMessage: "No mods installed.", description: "Mods tab: empty-state text shown when no mods are installed; a hint below it explains how to add one" })}</p>
          {canManage && (
            <p className="text-xs text-muted">
              {canBrowse
                ? intl.formatMessage({ id: "web.routes.tabs.mods.empty_hint_browse", defaultMessage: "Use “Install mod” to search a registry, add one from a URL, or upload a file.", description: "Mods: hint under the empty list when the user can browse, install from a URL and upload" })
                : canInstall
                  ? intl.formatMessage({ id: "web.routes.tabs.mods.empty_hint_install", defaultMessage: "Use “Install mod” to add one from a URL or upload a file.", description: "Mods: hint under the empty list when the user can install from a URL or upload" })
                  : intl.formatMessage({ id: "web.routes.tabs.mods.empty_hint_upload", defaultMessage: "Use “Install mod” to upload a file.", description: "Mods: hint under the empty list when the user can only upload a file" })}
            </p>
          )}
        </div>
      ) : (
        <ul className="space-y-1">
          {mods?.map((m) => {
            const update = updateByName.get(m.name);
            return (
              <li
                key={m.name}
                className="flex items-center justify-between gap-3 rounded border border-border bg-surface/30 px-3 py-2"
              >
                <div className="flex min-w-0 items-center gap-3">
                  <Package className="h-4 w-4 shrink-0 text-muted" />
                  <div className="min-w-0">
                    <div className="flex min-w-0 items-center gap-2">
                      <span className="truncate font-mono text-sm">{m.name}</span>
                      {m.meta ? (
                        <span className="shrink-0 rounded-full border border-border px-2 py-0.5 text-[10px] capitalize text-muted">
                          {[m.meta.provider, m.meta.versionNumber].filter(Boolean).join(" · ")}
                        </span>
                      ) : (
                        <span
                          className="shrink-0 rounded-full bg-surface px-2 py-0.5 text-[10px] text-muted"
                          aria-label={intl.formatMessage({ id: "web.routes.tabs.mods.placed_outside_the_panel_no_update", defaultMessage: "Placed outside the panel — no update checks", description: "Mods tab: accessible label of the grey tag on a mod placed outside the panel; the tag tells the user update checks do not run for it" })}
                        >{intl.formatMessage({ id: "web.routes.tabs.mods.unmanaged", defaultMessage: "unmanaged", description: "Mods tab: grey tag on a mod that was placed outside the panel, meaning no update checks run for it; keep it short" })}</span>
                      )}
                    </div>
                    <div className="text-xs text-muted">
                      {formatBytes(m.size)}
                      {m.modTime ? intl.formatMessage({ id: "web.routes.tabs.mods.modified_relative", defaultMessage: " · {time}", description: "Mods: suffix after a mod's version showing when it was modified; {time} is how long ago" }, { time: formatRelative(m.modTime, intl) }) : ""}
                    </div>
                  </div>
                </div>
                <div className="flex shrink-0 items-center gap-2">
                  {update && (
                    <>
                      <span className="rounded-full border border-primary/40 bg-primary/10 px-2 py-0.5 text-[10px] font-medium text-primary">
                        {update.latestVersionNumber || intl.formatMessage({ id: "web.routes.tabs.mods.new_version", defaultMessage: "new version", description: "Mods: stand-in for the latest version number when it is unknown" })} {intl.formatMessage({ id: "web.routes.tabs.mods.available", defaultMessage: "available", description: "Mods tab: word after the latest version number in the update badge, meaning a newer version is available; keep it short" })}</span>
                      {canManage && (
                        <Button
                          size="sm"
                          onPress={() => install.mutate(upgradeBody(update))}
                          isDisabled={install.isPending || updateAll.isPending}
                        >
                          <ArrowUpCircle className="h-3 w-3" /> {intl.formatMessage({ id: "web.routes.tabs.mods.update", defaultMessage: "Update", description: "Mods tab: button on a mod row that updates it to the newer version; keep it short" })}</Button>
                      )}
                    </>
                  )}
                  {canManage && (
                    <Button
                      variant="ghost"
                      size="sm"
                      aria-label={intl.formatMessage({ id: "web.routes.tabs.mods.remove_mod_aria", defaultMessage: "Remove {name}", description: "Mods: aria-label of the remove button on a mod row; {name} is the mod name" }, { name: m.name })}
                      onPress={() => setConfirmRemove(m)}
                      isDisabled={remove.isPending}
                    >
                      <Trash2 className="h-3 w-3" />
                    </Button>
                  )}
                </div>
              </li>
            );
          })}
        </ul>
      )}

      <ConfirmDialog
        open={confirmRemove !== null}
        onOpenChange={(o) => !o && setConfirmRemove(null)}
        title={intl.formatMessage({ id: "web.routes.tabs.mods.remove_mod", defaultMessage: "Remove mod", description: "Mods tab: title of the confirmation dialog that asks before a mod is removed from the server" })}
        description={
          <>{intl.formatMessage({ id: "web.routes.tabs.mods.remove", defaultMessage: "Remove", description: "Mods tab: the word Remove, used as the confirm button of the removal dialog, as the start of its sentence, and as a row button marking a mod for removal; keep it short" })} <span className="font-mono text-fg">{confirmRemove?.name}</span> {intl.formatMessage({ id: "web.routes.tabs.mods.from_the_server_it_will_be_gone_on_the", defaultMessage: "from the server? It will be gone on the next restart.", description: "Mods tab: rest of the removal confirmation sentence after the mod name, saying the mod will be gone on the next restart" })}</>
        }
        confirmLabel={intl.formatMessage({ id: "web.routes.tabs.mods.remove", defaultMessage: "Remove", description: "Mods tab: the word Remove, used as the confirm button of the removal dialog, as the start of its sentence, and as a row button marking a mod for removal; keep it short" })}
        destructive
        busy={remove.isPending}
        onConfirm={() => confirmRemove && remove.mutate(confirmRemove.name)}
      />
    </div>
  );
}

// modIDPattern mirrors the API's modIDPattern (api/internal/handlers/
// mod_ids.go) and the CRD's ModRef.ID validation — client-side so the Add
// button/input give immediate feedback instead of round-tripping to a 400.
const modIDPattern = /^[A-Za-z0-9._-]{1,64}$/;

// A row's local, unsaved status relative to the last-saved list. "kept"
// mirrors the server; "added" is a new selection not yet saved; "removed"
// is marked for removal but still rendered (dimmed, with Undo) until Save.
type ModIDRowState = "kept" | "added" | "removed";
interface ModIDRow extends ModID {
  state: ModIDRowState;
}

// ModsByIdTab is the mods editor for games whose server downloads its own
// mods given a list of ids (spec.capabilities.mods.idList) — ARK: Survival
// Ascended's CurseForge ids, Project Zomboid's MOD_IDS, generic Steam
// Workshop lists. There is no mods directory and no per-mod file, so the
// file-based list/install/upload UI (FileModsTab) is meaningless here.
//
// Saving changes the game container's env and restarts the server (see
// GameTemplate.spec.capabilities.mods.idList and the operator's env
// projection), so this is a batch-then-save editor: adds/removes are held
// in local `rows` state and only reach the API as a single PUT — never a
// per-row write, which would cost one restart per mod.
function ModsByIdTab({
  name,
  tmpl,
  ns,
}: {
  name: string;
  tmpl?: GameTemplate;
  ns?: string;
}) {
  const intl = useOptionalIntl();
  const resourceTarget = useResourceTarget({ name, namespace: ns });
  const resourceClient = useResourceClient(resourceTarget);
  const { Servers } = resourceClient;
  const qc = useQueryClient();
  const access = useResourceAccess();
  const hasManagePermission = access?.canControl === true;

  const caps = tmpl?.spec.capabilities?.mods;
  // A registry provider (e.g. ARK declares curseforge) enables in-app
  // browse; the RegistryBrowser itself falls back to a message when the
  // provider is declared but unavailable (e.g. no CurseForge API key) — in
  // either case Add-by-ID below keeps working on its own.
  const provider = caps?.registry?.providers?.[0]?.provider;
  const canBrowse = !!caps?.registry;
  const providerName = provider ? providerLabel(provider) : undefined;

  const [browsing, setBrowsing] = useState(false);
  const [rows, setRows] = useState<ModIDRow[]>([]);
  const [idInput, setIdInput] = useState("");
  const [banner, setBanner] = useState<Banner | null>(null);

  // savedRef holds the last-known server truth (for Discard); lastSeenRef
  // dedupes re-syncs to the same query result (mirrors the Settings tab's
  // draft/baseline pattern for the same reason: a background refetch must
  // not clobber edits in progress).
  const savedRef = useRef<ModID[]>([]);
  const lastSeenRef = useRef<ModID[] | undefined>(undefined);

  const { data: saved, isFetching, isError, error: listError, refetch } = useQuery({
    queryKey: resourceKey(resourceTarget, "mod-ids", name, ns),
    queryFn: ({ signal }) => resourceClient.withSignal(signal).Servers.modIDs(name, ns),
  });
  // Never offer edits against an unknown or unavailable server-side list.
  const canManage = hasManagePermission && saved !== undefined && !isError;

  const dirty = useMemo(() => rows.some((r) => r.state !== "kept"), [rows]);

  useEffect(() => {
    if (!saved || saved === lastSeenRef.current) return;
    lastSeenRef.current = saved;
    if (dirty) return; // local edits in flight — don't clobber
    const applySaved = () => {
      savedRef.current = saved;
      setRows(saved.map((m) => ({ ...m, state: "kept" as const })));
    };
    applySaved();
  }, [saved, dirty]);

  const save = useMutation({
    mutationFn: () =>
      Servers.setModIDs(
        name,
        rows
          .filter((r) => r.state !== "removed")
          .map(({ id, name: label }): ModID => (label ? { id, name: label } : { id })),
        ns,
      ),
    onSuccess: (updated) => {
      void qc.invalidateQueries({ queryKey: ["fleet"] });
      savedRef.current = updated;
      lastSeenRef.current = updated;
      setRows(updated.map((m) => ({ ...m, state: "kept" as const })));
      setBanner({
        kind: "ok",
        text: intl.formatMessage({ id: "web.routes.tabs.mods.banner_saved_restart", defaultMessage: "Saved — the server will restart to apply {count, plural, one {# mod} other {# mods}}.", description: "Mods: banner shown after the mod list is saved; {count} is the number of mods applied on restart" }, { count: updated.length }),
      });
      return qc.invalidateQueries({ queryKey: resourceKey(resourceTarget, "mod-ids", name) });
    },
    onError: (err) => setBanner({ kind: "err", text: errMsg(err, intl) }),
  });

  const discard = () => {
    setRows(savedRef.current.map((m) => ({ ...m, state: "kept" as const })));
    setBanner(null);
  };

  // addModID adds an id to the pending list. Re-adding an id that's
  // currently marked for removal just undoes the removal (the intuitive
  // outcome); adding an id already selected is a no-op.
  const addModID = (id: string, label?: string) => {
    if (save.isPending) return;
    const trimmed = id.trim();
    if (!modIDPattern.test(trimmed)) return;
    setRows((prev) => {
      const existing = prev.find((r) => r.id === trimmed);
      if (existing) {
        if (existing.state === "removed") {
          return prev.map((r) => (r.id === trimmed ? { ...r, state: "kept" as const } : r));
        }
        return prev;
      }
      return [...prev, { id: trimmed, name: label, state: "added" as const }];
    });
  };

  // markRemoved drops a never-saved "added" row outright (nothing to
  // undo — it never reached the server); a "kept" row is marked
  // "removed" so it stays visible (dimmed, with Undo) until Save.
  const markRemoved = (id: string) => {
    if (save.isPending) return;
    setRows((prev) =>
      prev.flatMap((r) => {
        if (r.id !== id) return [r];
        if (r.state === "added") return [];
        return [{ ...r, state: "removed" as const }];
      }),
    );
  };

  const undoRemove = (id: string) => {
    if (save.isPending) return;
    setRows((prev) => prev.map((r) => (r.id === id ? { ...r, state: "kept" as const } : r)));
  };

  // Every row renders (a "removed" row stays visible, dimmed, until Save
  // actually drops it); the header count excludes rows marked for removal.
  const selectedCount = rows.filter((r) => r.state !== "removed").length;

  if (browsing && canManage) {
    return (
      <div className="flex h-full flex-col gap-4 p-6">
        <header className="space-y-2">
          <Button
            variant="ghost"
            size="sm"
            className="text-xs"
            onPress={() => setBrowsing(false)}
            aria-label={intl.formatMessage({ id: "web.routes.tabs.mods.back_to_selected_mods", defaultMessage: "Back to selected mods", description: "Mods tab: accessible label of the back button that returns from the registry browser to the selected mods list; keep it short" })}
          >
            <ArrowLeft className="h-3.5 w-3.5" /> {intl.formatMessage({ id: "web.routes.tabs.mods.selected_mods", defaultMessage: "Selected mods", description: "Mods tab: visible text of the back button in the registry browser, and the heading of the selected mods list; keep it short" })}</Button>
          <h2 className="text-base font-semibold">{intl.formatMessage({ id: "web.routes.tabs.mods.browse", defaultMessage: "Browse", description: "Mods tab: word Browse that starts the registry browse heading and button; the registry name follows it; keep it short" })} {providerName ?? intl.formatMessage({ id: "web.routes.tabs.mods.registry", defaultMessage: "registry", description: "Mods: fallback name of the mod registry when the provider has no name" })}</h2>
        </header>
        <div className="min-h-0 flex-1">
          <RegistryBrowser
            name={name}
            ns={ns}
            type="mod"
            categories={MOD_CATEGORIES.map((c) => ({ value: c.value, label: intl.formatMessage(c.label) }))}
            renderItem={(p) => (
              <IdModCard
                project={p}
                added={rows.some((r) => r.id === p.id && r.state !== "removed")}
                onAdd={() => addModID(p.id, p.title)}
              />
            )}
          />
        </div>
      </div>
    );
  }

  return (
    <div className="space-y-6 p-6">
      <header className="flex items-center justify-between gap-3">
        <div className="space-y-0.5">
          <h2 className="text-sm text-muted">{saved !== undefined
            ? intl.formatMessage({ id: "web.routes.tabs.mods.selected_count", defaultMessage: "{count} selected", description: "Mods: heading with how many mods are selected; {count} is that number" }, { count: selectedCount })
            : isError
              ? intl.formatMessage({ id: "web.routes.tabs.mods.couldnt_load", defaultMessage: "Couldn’t load mods", description: "Mods: heading shown when the mod list fails to load" })
              : intl.formatMessage({ id: "web.routes.tabs.mods.loading", defaultMessage: "Loading…", description: "Mods: heading shown while the mod list loads" })}</h2>
          <p className="text-[11px] text-muted">
            {[tmpl?.spec.displayName, providerName ? intl.formatMessage({ id: "web.routes.tabs.mods.provider_mod_ids", defaultMessage: "{provider} mod IDs", description: "Mods: subtitle line naming the mod provider whose mod IDs are listed; {provider} is the provider name" }, { provider: providerName }) : null].filter(Boolean).join(" · ")}
          </p>
        </div>
        <Button
          isIconOnly
          variant="ghost"
          size="sm"
          onPress={() => void refetch()}
          isDisabled={isFetching}
          aria-label={intl.formatMessage({ id: "web.routes.tabs.mods.refresh", defaultMessage: "Refresh", description: "Mods tab: icon-only button that reloads the mod list and checks for updates; read by screen readers; keep it short" })}
          className="h-auto w-auto"
        >
          <RotateCw className={cn("h-4 w-4", isFetching && "animate-spin")} />
        </Button>
      </header>

      <div className="flex items-start gap-2 rounded-lg border border-border bg-surface/40 p-3 text-xs text-fg">
        <Info className="h-4 w-4 shrink-0 text-muted" />
        <p>{intl.formatMessage({ id: "web.routes.tabs.mods.this_game_s_server_downloads_its_own", defaultMessage: "This game’s server downloads its own mods. Select them here — Gameplane passes the list to the server at launch.", description: "Mods tab: info note at the top of the by-ID mods view saying the game server downloads its own mods and Gameplane passes the list at launch" })}</p>
      </div>

      {banner && (
        <div
          className={cn(
            "rounded border px-3 py-2 text-sm",
            banner.kind === "ok"
              ? "border-border bg-surface/40 text-fg"
              : "border-danger/40 bg-danger/10 text-danger",
          )}
        >
          <div className="flex items-start justify-between gap-3">
            <span className="font-mono break-all">{banner.text}</span>
            <Button variant="ghost" size="sm" onPress={() => setBanner(null)} isIconOnly aria-label={intl.formatMessage({ id: "web.routes.tabs.mods.dismiss", defaultMessage: "dismiss", description: "Mods tab: icon-only button that closes the message banner; read by screen readers; keep it short" })}>
              <X className="h-3 w-3" />
            </Button>
          </div>
        </div>
      )}

      {isError && (
        <div className="rounded border border-danger/40 bg-danger/10 px-3 py-2 text-sm text-danger">
          {errMsg(listError, intl)}{intl.formatMessage({ id: "web.routes.tabs.mods.error_retry_separator", defaultMessage: " · ", description: "Mods: separator between the list error and the retry button" })}<Button variant="ghost" size="sm" className="underline" onPress={() => void refetch()} aria-label={intl.formatMessage({ id: "web.routes.tabs.mods.retry", defaultMessage: "retry", description: "Mods: label of the retry button after a failed list load" })}>{intl.formatMessage({ id: "web.routes.tabs.mods.retry", defaultMessage: "retry", description: "Mods: label of the retry button after a failed list load" })}</Button>
        </div>
      )}

      <div className="flex flex-wrap items-center gap-3">
        {canBrowse && canManage && (
          <>
            <Button
              variant="outline"
              size="sm"
              onPress={() => setBrowsing(true)}
              isDisabled={save.isPending}
            >
              <Compass className="h-4 w-4" /> {intl.formatMessage({ id: "web.routes.tabs.mods.browse", defaultMessage: "Browse", description: "Mods tab: word Browse that starts the registry browse heading and button; the registry name follows it; keep it short" })} {providerName ?? intl.formatMessage({ id: "web.routes.tabs.mods.registry", defaultMessage: "registry", description: "Mods: fallback name of the mod registry when the provider has no name" })}
            </Button>
            <div className="h-6 w-px bg-border" />
          </>
        )}
        <div className="flex items-center gap-2">
          <Input
            placeholder={providerName
              ? intl.formatMessage({ id: "web.routes.tabs.mods.paste_provider_mod_id", defaultMessage: "Paste a {provider} mod ID…", description: "Mods: placeholder of the mod ID input when the provider has a name; {provider} is the provider name" }, { provider: providerName })
              : intl.formatMessage({ id: "web.routes.tabs.mods.paste_mod_id", defaultMessage: "Paste a mod ID…", description: "Mods: placeholder of the mod ID input when the provider has no name" })}
            value={idInput}
            onChange={(e) => setIdInput(e.target.value)}
            disabled={!canManage || save.isPending}
            className="w-56"
            spellCheck={false}
          />
          <Button
            size="sm"
            isDisabled={!canManage || save.isPending || !modIDPattern.test(idInput.trim())}
            onPress={() => {
              addModID(idInput.trim());
              setIdInput("");
            }}
          >
            <Plus className="h-4 w-4" /> {intl.formatMessage({ id: "web.routes.tabs.mods.add", defaultMessage: "Add", description: "Mods: label of the button that adds a mod to the list" })}</Button>
        </div>
      </div>

      <div className="space-y-2">
        <h3 className="text-sm font-semibold">{intl.formatMessage({ id: "web.routes.tabs.mods.selected_mods", defaultMessage: "Selected mods", description: "Mods tab: visible text of the back button in the registry browser, and the heading of the selected mods list; keep it short" })}</h3>
        {saved === undefined ? null : rows.length === 0 ? (
          <div className="flex flex-col items-center gap-2 rounded-lg border border-dashed border-border py-12 text-center">
            <Package className="h-6 w-6 text-muted" />
            <p className="text-sm text-muted">{intl.formatMessage({ id: "web.routes.tabs.mods.no_mods_selected", defaultMessage: "No mods selected.", description: "Mods tab: empty-state text in the selected mods list when no mods are selected" })}</p>
            {canManage && <p className="text-xs text-muted">{intl.formatMessage({ id: "web.routes.tabs.mods.browse", defaultMessage: "Browse", description: "Mods tab: word Browse that starts the registry browse heading and button; the registry name follows it; keep it short" })} {providerName ?? intl.formatMessage({ id: "web.routes.tabs.mods.a_registry", defaultMessage: "a registry", description: "Mods: fallback that reads 'a registry' when the provider has no name" })} {intl.formatMessage({ id: "web.routes.tabs.mods.or_add_a_mod_id", defaultMessage: "or add a mod ID.", description: "Mods tab: end of the empty-state hint that tells the user to browse a registry or add a mod ID" })}</p>}
          </div>
        ) : (
          <ul className="space-y-2">
            {rows.map((r) => (
              <li
                key={r.id}
                className={cn(
                  "flex items-center justify-between gap-3 rounded-lg border border-border bg-card px-3.5 py-3.5",
                  r.state === "removed" && "opacity-60",
                )}
              >
                <div className="flex min-w-0 items-center gap-3">
                  <div className="flex h-9 w-9 shrink-0 items-center justify-center rounded bg-surface">
                    <Package className="h-4 w-4 text-muted" />
                  </div>
                  <div className="min-w-0">
                    <div className={cn("truncate text-sm font-semibold", r.state === "removed" && "line-through")}>
                      {r.name || r.id}
                    </div>
                    {r.name && <div className="truncate text-xs text-muted">{intl.formatMessage({ id: "web.routes.tabs.mods.id_id", defaultMessage: "ID {id}", description: "Mods: label showing a mod's registry ID; {id} is the ID" }, { id: r.id })}</div>}
                  </div>
                </div>
                <div className="flex shrink-0 items-center gap-2">
                  {r.state === "added" && (
                    <Chip size="sm" color="success" variant="soft">{intl.formatMessage({ id: "web.routes.tabs.mods.added", defaultMessage: "Added", description: "Mods: shown when a mod is already in the list (chip or button label)" })}</Chip>
                  )}
                  {r.state === "removed" && (
                    <Chip size="sm" color="warning" variant="soft">{intl.formatMessage({ id: "web.routes.tabs.mods.marked_for_removal", defaultMessage: "Marked for removal", description: "Mods tab: badge on a mod row showing it is marked for removal and will be removed when saved; keep it short" })}</Chip>
                  )}
                  {canManage &&
                    (r.state === "removed" ? (
                      <Button
                        variant="ghost"
                        size="sm"
                        onPress={() => undoRemove(r.id)}
                        isDisabled={save.isPending}
                      >
                        <RotateCcw className="h-3 w-3" /> {intl.formatMessage({ id: "web.routes.tabs.mods.undo", defaultMessage: "Undo", description: "Mods tab: row button that cancels a pending removal of a mod; keep it short" })}</Button>
                    ) : (
                      <Button
                        variant="ghost"
                        size="sm"
                        onPress={() => markRemoved(r.id)}
                        isDisabled={save.isPending}
                      >
                        <X className="h-3 w-3" /> {intl.formatMessage({ id: "web.routes.tabs.mods.remove", defaultMessage: "Remove", description: "Mods tab: the word Remove, used as the confirm button of the removal dialog, as the start of its sentence, and as a row button marking a mod for removal; keep it short" })}</Button>
                    ))}
                </div>
              </li>
            ))}
          </ul>
        )}
      </div>

      <div className="flex flex-wrap items-center justify-between gap-3 border-t border-border pt-4">
        <div className="flex items-center gap-2 text-xs text-warning">
          <TriangleAlert className="h-4 w-4" /> {intl.formatMessage({ id: "web.routes.tabs.mods.saving_restarts_the_server_to_apply_the", defaultMessage: "Saving restarts the server to apply the new mod list.", description: "Mods tab: warning note above the save bar saying that saving restarts the server to apply the new mod list" })}</div>
        <div className="flex items-center gap-2">
          <Button
            variant="ghost"
            size="sm"
            onPress={discard}
            isDisabled={!dirty || save.isPending}
          >{intl.formatMessage({ id: "web.routes.tabs.mods.discard", defaultMessage: "Discard", description: "Mods tab: footer button that throws away unsaved mod list changes; disabled when nothing changed; keep it short" })}</Button>
          <Button
            size="sm"
            onPress={() => save.mutate()}
            isDisabled={!dirty || save.isPending || !canManage}
          >
            {save.isPending ? intl.formatMessage({ id: "web.routes.tabs.mods.saving", defaultMessage: "Saving…", description: "Mods: label of the save button while the mod list is saving" }) : intl.formatMessage({ id: "web.routes.tabs.mods.save_changes", defaultMessage: "Save changes", description: "Mods: label of the button that saves the mod list" })}
          </Button>
        </div>
      </div>
    </div>
  );
}

// IdModCard is one registry-browse result in id-managed mode: no
// version/file picker (unlike FileModsTab's ModCard) — the operator only
// ever needs the provider-native project id, not a specific downloadable
// file, since the *game server itself* fetches the mod at launch.
function IdModCard({
  project,
  added,
  onAdd,
}: {
  project: RegistryProject;
  added: boolean;
  onAdd: () => void;
}) {
  const intl = useOptionalIntl();
  return (
    <div className="flex items-center gap-3 rounded-md border border-border bg-surface/30 p-2.5">
      <RegistryIcon url={project.iconUrl} fallback={<Package className="h-9 w-9 shrink-0 rounded p-2 text-muted" />} />
      <div className="min-w-0 flex-1">
        <div className="truncate text-sm font-medium">{project.title}</div>
        <div className="truncate text-xs text-muted">
          {[project.author, project.downloads != null ? `${compactNum(project.downloads)} downloads` : null]
            .filter(Boolean)
            .join(" · ")}
        </div>
      </div>
      <Button
        size="sm"
        variant={added ? "outline" : "primary"}
        isDisabled={added}
        onPress={onAdd}
      >
        {added ? intl.formatMessage({ id: "web.routes.tabs.mods.added", defaultMessage: "Added", description: "Mods: shown when a mod is already in the list (chip or button label)" }) : intl.formatMessage({ id: "web.routes.tabs.mods.add", defaultMessage: "Add", description: "Mods: label of the button that adds a mod to the list" })}
      </Button>
    </div>
  );
}

type InstallMode = "search" | "url" | "upload";

// InstallPage is the dedicated in-tab install view (replaces the old
// popup): browse a registry (when the template declares one), add a mod
// from a URL (when the module allows downloads), or upload a local file
// (always — uploads carry no SSRF risk, so they need no allowlist). All
// paths converge on agent endpoints that enforce the same checks.
function InstallPage({
  name,
  ns,
  canInstall,
  canBrowse,
  allowedHosts,
  extensions,
  pending,
  uploadPending,
  banner,
  onDismiss,
  onBack,
  onInstall,
  onUpload,
}: {
  name: string;
  ns?: string;
  canInstall: boolean;
  canBrowse: boolean;
  allowedHosts: string[];
  extensions: string[];
  pending: boolean;
  uploadPending: boolean;
  banner: Banner | null;
  onDismiss: () => void;
  onBack: () => void;
  onInstall: (body: InstallBody) => void;
  onUpload: (file: File) => void;
}) {
  const intl = useOptionalIntl();
  const modes: { key: InstallMode; label: MessageDescriptor }[] = [
    ...(canBrowse ? [{ key: "search" as const, label: INSTALL_MODE_LABELS.search }] : []),
    ...(canInstall ? [{ key: "url" as const, label: INSTALL_MODE_LABELS.url }] : []),
    { key: "upload", label: INSTALL_MODE_LABELS.upload },
  ];
  const [mode, setMode] = useState<InstallMode>(modes[0].key);
  // Prefilled by the browse view for registry files the panel can't
  // one-click install (requiresAuth, e.g. the Factorio portal) — the user
  // appends their own credentials in the URL form.
  const [urlPrefill, setUrlPrefill] = useState("");
  const browsing = mode === "search" && canBrowse;
  const useUrlForm = (url: string) => {
    setUrlPrefill(url);
    setMode("url");
  };

  return (
    <div className="flex h-full flex-col gap-4 p-6">
      <header className="space-y-2">
        <Button
          variant="ghost"
          size="sm"
          className="text-xs"
          onPress={onBack}
          aria-label={intl.formatMessage({ id: "web.routes.tabs.mods.back_to_installed_mods", defaultMessage: "Back to installed mods", description: "Mods tab: accessible label of the back button that returns to the installed mods list; read by screen readers; keep it short" })}
        >
          <ArrowLeft className="h-3.5 w-3.5" /> {intl.formatMessage({ id: "web.routes.tabs.mods.installed_mods", defaultMessage: "Installed mods", description: "Mods tab: visible text of the back button that returns to the installed mods list; keep it short" })}</Button>
        <div className="flex items-center justify-between gap-3">
          <div className="space-y-0.5">
            <h2 className="text-base font-semibold">{intl.formatMessage({ id: "web.routes.tabs.mods.install_mods", defaultMessage: "Install mods", description: "Mods tab: heading of the page for installing mods, shown above the install options" })}</h2>
            <p className="text-[11px] text-muted">
              {browsing
                ? intl.formatMessage({ id: "web.routes.tabs.mods.install_hint_browse", defaultMessage: "Browse a registry and install for this server’s version + loader.", description: "Mods: hint under the install heading in browse mode" })
                : mode === "upload"
                  ? intl.formatMessage({ id: "web.routes.tabs.mods.install_hint_upload", defaultMessage: "Upload a mod file from your machine into the server’s mods directory.", description: "Mods: hint under the install heading in upload mode" })
                  : intl.formatMessage({ id: "web.routes.tabs.mods.install_hint_url", defaultMessage: "Download a mod from a URL into the server’s mods directory.", description: "Mods: hint under the install heading in URL download mode" })}
            </p>
          </div>
          {modes.length > 1 && (
            <div className="flex w-fit rounded border border-border text-xs">
              {modes.map((m, i) => (
                <button
                  key={m.key}
                  type="button"
                  onClick={() => setMode(m.key)}
                  aria-pressed={mode === m.key}
                  className={cn(
                    "h-8 px-3 rounded-none border-none bg-transparent cursor-pointer",
                    i === 0 && "rounded-l",
                    i === modes.length - 1 && "rounded-r",
                    i > 0 && "border-l border-border",
                    mode === m.key
                      ? m.key === "search"
                        ? "bg-primary font-medium text-primary-foreground"
                        : "bg-surface font-medium"
                      : "text-muted",
                  )}
                >
                  {intl.formatMessage(m.label)}
                </button>
              ))}
            </div>
          )}
        </div>
      </header>

      {banner && (
        <div
          className={cn(
            "rounded border px-3 py-2 text-sm",
            banner.kind === "ok"
              ? "border-border bg-surface/40 text-fg"
              : "border-danger/40 bg-danger/10 text-danger",
          )}
        >
          <div className="flex items-start justify-between gap-3">
            <span className="font-mono break-all">{banner.text}</span>
            <Button variant="ghost" size="sm" onPress={onDismiss} isIconOnly aria-label={intl.formatMessage({ id: "web.routes.tabs.mods.dismiss", defaultMessage: "dismiss", description: "Mods tab: icon-only button that closes the message banner; read by screen readers; keep it short" })}>
              <X className="h-3 w-3" />
            </Button>
          </div>
        </div>
      )}

      {browsing ? (
        <div className="min-h-0 flex-1">
          <BrowseForm name={name} ns={ns} pending={pending} onInstall={onInstall} onUseUrl={useUrlForm} />
        </div>
      ) : mode === "upload" ? (
        <UploadForm
          extensions={extensions}
          pending={uploadPending}
          onCancel={onBack}
          onUpload={onUpload}
        />
      ) : (
        <UrlForm
          key={urlPrefill}
          initialUrl={urlPrefill}
          allowedHosts={allowedHosts}
          pending={pending}
          onCancel={onBack}
          onInstall={onInstall}
        />
      )}
    </div>
  );
}

// UrlForm collects a download URL (and optional filename); a light http(s)
// check only, with the allowed hosts shown as a hint.
function UrlForm({
  allowedHosts,
  pending,
  onCancel,
  onInstall,
  initialUrl = "",
}: {
  allowedHosts: string[];
  pending: boolean;
  onCancel: () => void;
  onInstall: (body: InstallBody) => void;
  initialUrl?: string;
}) {
  const intl = useOptionalIntl();
  const [url, setUrl] = useState(initialUrl);
  const [fileName, setFileName] = useState("");
  const trimmed = url.trim();
  const validURL = /^https?:\/\/.+/i.test(trimmed);

  return (
    <>
      <div className="space-y-3 pt-4">
        <div>
          <label className="block pb-1 text-xs text-muted" htmlFor="mod-url">{intl.formatMessage({ id: "web.routes.tabs.mods.download_url", defaultMessage: "Download URL", description: "Mods tab: field label for the download URL of a mod; the red asterisk marks the field as required" })} <span className="text-danger">{intl.formatMessage({ id: "web.routes.tabs.mods.required_marker", defaultMessage: "*", description: "Mods: marker shown after the label of a required field" })}</span>
          </label>
          <Input
            id="mod-url"
            autoFocus
            placeholder={intl.formatMessage({ id: "web.routes.tabs.mods.https_cdn_modrinth_com_mod_jar", defaultMessage: "https://cdn.modrinth.com/…/mod.jar", description: "Mods tab: example placeholder in the download URL field showing the expected form; an example address, not a default" })}
            value={url}
            onChange={(e) => setUrl(e.target.value)}
            spellCheck={false}
          />
          {trimmed !== "" && !validURL && (
            <p className="pt-1 text-xs text-danger">{intl.formatMessage({ id: "web.routes.tabs.mods.must_be_an_http_s_url", defaultMessage: "Must be an http(s) URL.", description: "Mods tab: validation error under the download URL field when the text is not an http or https address" })}</p>
          )}
        </div>
        <div>
          <label className="block pb-1 text-xs text-muted" htmlFor="mod-name">{intl.formatMessage({ id: "web.routes.tabs.mods.filename", defaultMessage: "Filename", description: "Mods tab: field label for the optional file name the mod is saved under" })}</label>
          <Input
            id="mod-name"
            placeholder={intl.formatMessage({ id: "web.routes.tabs.mods.optional_derived_from_the_url", defaultMessage: "optional — derived from the URL", description: "Mods tab: placeholder in the file name field saying the name is taken from the URL when the field is left empty" })}
            value={fileName}
            onChange={(e) => setFileName(e.target.value)}
            spellCheck={false}
          />
        </div>
        {allowedHosts.length > 0 && (
          <p className="text-[11px] text-muted">{intl.formatMessage({ id: "web.routes.tabs.mods.allowed_hosts", defaultMessage: "Allowed hosts:", description: "Mods tab: hint under the download URL form; the list after the label shows the host names the URL may come from" })} <span className="font-mono">{allowedHosts.join(", ")}</span>
          </p>
        )}
      </div>

      <div className="flex items-center justify-end gap-2 pt-5">
        <Button variant="ghost" size="sm" onPress={onCancel} isDisabled={pending}>{intl.formatMessage({ id: "web.routes.tabs.mods.cancel", defaultMessage: "Cancel", description: "Mods tab: ghost button that closes the download URL or upload form without installing; keep it short" })}</Button>
        <Button
          size="sm"
          isDisabled={!validURL || pending}
          onPress={() =>
            onInstall({ url: trimmed, ...(fileName.trim() ? { name: fileName.trim() } : {}) })
          }
        >
          {pending ? intl.formatMessage({ id: "web.routes.tabs.mods.installing", defaultMessage: "Installing…", description: "Mods: label of the install button while the mod installs" }) : intl.formatMessage({ id: "web.routes.tabs.mods.install", defaultMessage: "Install", description: "Mods: label of the button that installs a mod" })}
        </Button>
      </div>
    </>
  );
}

// UploadForm sends a local file to the agent (multipart). The agent applies
// the same name/extension/size checks as URL installs; extract-mode loaders
// unpack archives the same way. Uploads are recorded as provider "upload"
// in the manifest and are never update-checked.
function UploadForm({
  extensions,
  pending,
  onCancel,
  onUpload,
}: {
  extensions: string[];
  pending: boolean;
  onCancel: () => void;
  onUpload: (file: File) => void;
}) {
  const intl = useOptionalIntl();
  const [file, setFile] = useState<File | null>(null);

  return (
    <>
      <div className="space-y-3 pt-4">
        <label
          htmlFor="mod-file"
          className="flex cursor-pointer flex-col items-center gap-1.5 rounded-lg border border-dashed border-border px-6 py-10 text-center hover:bg-surface/40"
          onDragOver={(e) => e.preventDefault()}
          onDrop={(e) => {
            e.preventDefault();
            const dropped = e.dataTransfer.files?.[0];
            if (dropped) setFile(dropped);
          }}
        >
          <Upload className="h-6 w-6 text-muted" />
          <span className="text-sm font-medium">
            {file ? file.name : intl.formatMessage({ id: "web.routes.tabs.mods.drop_mod_file", defaultMessage: "Drop a mod file here, or click to browse", description: "Mods: prompt in the upload drop zone when no file is chosen" })}
          </span>
          <span className="text-[11px] text-muted">
            {extensions.length > 0
              ? intl.formatMessage({ id: "web.routes.tabs.mods.accepted_extensions", defaultMessage: "Accepted: {extensions} · lands in the active loader’s mod volume", description: "Mods: upload hint listing the accepted file extensions; {extensions} is a comma-separated list of extensions" }, { extensions: extensions.join(", ") })
              : intl.formatMessage({ id: "web.routes.tabs.mods.lands_in_loader_volume", defaultMessage: "Lands in the active loader’s mod volume", description: "Mods: upload hint saying the file lands in the active loader's mod volume" })}
          </span>
          <input
            id="mod-file"
            type="file"
            className="sr-only"
            accept={extensions.join(",") || undefined}
            onChange={(e) => setFile(e.target.files?.[0] ?? null)}
          />
        </label>
        <p className="text-[11px] text-muted">{intl.formatMessage({ id: "web.routes.tabs.mods.uploads_are_recorded_as_provider_upload", defaultMessage: "Uploads are recorded as provider “upload” in the manifest — they’re never update-checked.", description: "Mods tab: note under the upload field saying uploaded mods are recorded with the upload provider and are never checked for updates" })}</p>
      </div>

      <div className="flex items-center justify-end gap-2 pt-5">
        <Button variant="ghost" size="sm" onPress={onCancel} isDisabled={pending}>{intl.formatMessage({ id: "web.routes.tabs.mods.cancel", defaultMessage: "Cancel", description: "Mods tab: ghost button that closes the download URL or upload form without installing; keep it short" })}</Button>
        <Button size="sm" isDisabled={!file || pending} onPress={() => file && onUpload(file)}>
          {pending ? intl.formatMessage({ id: "web.routes.tabs.mods.uploading", defaultMessage: "Uploading…", description: "Mods: label of the upload button while the file uploads" }) : intl.formatMessage({ id: "web.routes.tabs.mods.upload", defaultMessage: "Upload", description: "Mods: label of the button that uploads a mod file" })}
        </Button>
      </div>
    </>
  );
}

// BrowseForm searches the module's mod registry (debounced) and lets the
// user expand a result to pick a version/file. The API filters results to
// the server's active loader + game version, so the picker stays short.
const INSTALL_MODE_LABELS = defineMessages({
  search: { id: "web.routes.tabs.mods.mode_browse_registry", defaultMessage: "Browse registry", description: "Mods: install panel tab that browses a mod registry" },
  url: { id: "web.routes.tabs.mods.mode_from_url", defaultMessage: "From URL", description: "Mods: install panel tab that installs a mod from a URL" },
  upload: { id: "web.routes.tabs.mods.mode_upload_file", defaultMessage: "Upload file", description: "Mods: install panel tab that uploads a mod file" },
});

// Curated Modrinth categories for the mod browser's filter chips.
const MOD_CATEGORY_LABELS = defineMessages({
  optimization: { id: "web.routes.tabs.mods.category_optimization", defaultMessage: "Performance", description: "Mods: category filter chip for performance mods" },
  utility: { id: "web.routes.tabs.mods.category_utility", defaultMessage: "Utility", description: "Mods: category filter chip for utility mods" },
  library: { id: "web.routes.tabs.mods.category_library", defaultMessage: "Library", description: "Mods: category filter chip for library mods" },
  worldgen: { id: "web.routes.tabs.mods.category_worldgen", defaultMessage: "Worldgen", description: "Mods: category filter chip for world generation mods" },
  adventure: { id: "web.routes.tabs.mods.category_adventure", defaultMessage: "Adventure", description: "Mods: category filter chip for adventure mods" },
  storage: { id: "web.routes.tabs.mods.category_storage", defaultMessage: "Storage", description: "Mods: category filter chip for storage mods" },
  technology: { id: "web.routes.tabs.mods.category_technology", defaultMessage: "Tech", description: "Mods: category filter chip for technology mods" },
  magic: { id: "web.routes.tabs.mods.category_magic", defaultMessage: "Magic", description: "Mods: category filter chip for magic mods" },
});

const MOD_CATEGORIES: { value: string; label: MessageDescriptor }[] = [
  { value: "optimization", label: MOD_CATEGORY_LABELS.optimization },
  { value: "utility", label: MOD_CATEGORY_LABELS.utility },
  { value: "library", label: MOD_CATEGORY_LABELS.library },
  { value: "worldgen", label: MOD_CATEGORY_LABELS.worldgen },
  { value: "adventure", label: MOD_CATEGORY_LABELS.adventure },
  { value: "storage", label: MOD_CATEGORY_LABELS.storage },
  { value: "technology", label: MOD_CATEGORY_LABELS.technology },
  { value: "magic", label: MOD_CATEGORY_LABELS.magic },
];

// BrowseForm is the full mod browser inside the install dialog: the shared
// RegistryBrowser (popular by default, search, sort, category chips,
// load-more) with each result expandable to a version picker + Install.
function BrowseForm({
  name,
  ns,
  pending,
  onInstall,
  onUseUrl,
}: {
  name: string;
  ns?: string;
  pending: boolean;
  onInstall: (body: InstallBody) => void;
  onUseUrl: (url: string) => void;
}) {
  const intl = useOptionalIntl();
  return (
    <div className="flex min-h-0 flex-1 flex-col pt-3">
      <RegistryBrowser
        name={name}
        ns={ns}
        type="mod"
        categories={MOD_CATEGORIES.map((c) => ({ value: c.value, label: intl.formatMessage(c.label) }))}
        renderItem={(p, provider) => (
          <ModCard
            name={name}
            ns={ns}
            project={p}
            provider={provider}
            pending={pending}
            onInstall={onInstall}
            onUseUrl={onUseUrl}
          />
        )}
      />
    </div>
  );
}

// ModCard is one browser result; expanding it loads the project's versions
// (from the active provider) and offers a version/file picker + Install.
function ModCard({
  name,
  ns,
  project,
  provider,
  pending,
  onInstall,
  onUseUrl,
}: {
  name: string;
  ns?: string;
  project: RegistryProject;
  provider: string;
  pending: boolean;
  onInstall: (body: InstallBody) => void;
  onUseUrl: (url: string) => void;
}) {
  const intl = useOptionalIntl();
  const resourceTarget = useResourceTarget({ name, namespace: ns });
  const resourceClient = useResourceClient(resourceTarget);
  const [open, setOpen] = useState(false);
  const [selId, setSelId] = useState("");
  const versions = useQuery({
    queryKey: resourceKey(resourceTarget, "mod-versions", name, provider, project.id, ns),
    queryFn: ({ signal }) => resourceClient.withSignal(signal).Servers.modVersions(name, project.id, provider, ns),
    enabled: open,
  });
  const list = versions.data ?? [];

  // Default to the first version once it loads. Adjusted directly during
  // render (not in an effect) — the condition becomes false as soon as
  // selId is set, so this can't loop.
  const firstVersion = versions.data?.[0];
  if (firstVersion && !selId) setSelId(firstVersion.id);

  const chosen = list.find((v) => v.id === selId) ?? list[0];
  const file = chosen?.files.find((f) => f.primary) ?? chosen?.files[0];

  return (
    <div className="rounded-md border border-border bg-surface/30">
      <button type="button" onClick={() => setOpen((o) => !o)} className="flex w-full items-center gap-3 p-2.5 text-left">
        <RegistryIcon url={project.iconUrl} fallback={<Package className="h-9 w-9 shrink-0 rounded p-2 text-muted" />} />
        <div className="min-w-0 flex-1">
          <div className="truncate text-sm font-medium">{project.title}</div>
          <div className="truncate text-xs text-muted">
            {[project.author, project.downloads != null ? `${compactNum(project.downloads)} downloads` : null]
              .filter(Boolean)
              .join(" · ")}
          </div>
        </div>
        <span className="shrink-0 rounded-full border border-border px-2 py-0.5 text-[10px] capitalize text-muted">
          {project.provider}
        </span>
      </button>
      {open && (
        <div className="flex items-center gap-2 border-t border-border p-2.5">
          {versions.isLoading ? (
            <span className="text-xs text-muted">{intl.formatMessage({ id: "web.routes.tabs.mods.loading_versions", defaultMessage: "Loading versions…", description: "Mods tab: loading text in a mod row's version area while the list of versions is fetched" })}</span>
          ) : versions.isError ? (
            <span className="text-xs text-danger">{errMsg(versions.error, intl)}</span>
          ) : list.length === 0 ? (
            <span className="text-xs text-muted">{intl.formatMessage({ id: "web.routes.tabs.mods.no_compatible_files", defaultMessage: "No compatible files.", description: "Mods tab: text in a mod row's version area when no file fits the server's version and loader" })}</span>
          ) : (
            <>
              <select
                value={selId}
                onChange={(e) => setSelId(e.target.value)}
                className="h-8 min-w-0 flex-1 rounded border border-border bg-surface px-2 text-xs"
                aria-label={intl.formatMessage({ id: "web.routes.tabs.mods.version", defaultMessage: "Version", description: "Mods tab: accessible label of the version dropdown in a mod row; not shown as text" })}
              >
                {list.map((v) => {
                  const f = v.files.find((x) => x.primary) ?? v.files[0];
                  return (
                    <option key={v.id} value={v.id}>
                      {(v.versionNumber ?? v.name ?? v.id) + (f ? intl.formatMessage({ id: "web.routes.tabs.mods.version_file_suffix", defaultMessage: " · {filename}", description: "Mods: suffix on a version option naming its file; {filename} is the file name" }, { filename: f.filename }) : "")}
                    </option>
                  );
                })}
              </select>
              {file?.requiresAuth ? (
                // Portal files (e.g. Factorio) download only with the
                // player's own credentials — hand off to the URL form so
                // the user can append them; never one-click install. The
                // hint lives on a wrapping element's title (not the
                // Button's aria-label) so the button's accessible name
                // stays its visible text, "Use URL form".
                <span title={intl.formatMessage({ id: "web.routes.tabs.mods.this_registry_s_downloads_need_your_own", defaultMessage: "This registry's downloads need your own account credentials appended to the URL", description: "Mods tab: tooltip on the Use URL form button explaining the registry needs the user's own account credentials appended to the URL" })}>
                  <Button
                    size="sm"
                    variant="outline"
                    onPress={() => onUseUrl(file.downloadUrl)}
                  >{intl.formatMessage({ id: "web.routes.tabs.mods.use_url_form", defaultMessage: "Use URL form", description: "Mods tab: button that moves a mod to the URL form so the user can add their own credentials; it is not a one-click install; keep it short" })}</Button>
                </span>
              ) : (
                <Button
                  size="sm"
                  isDisabled={!file || pending}
                  onPress={() =>
                    file &&
                    onInstall({
                      url: file.downloadUrl,
                      name: file.filename,
                      meta: {
                        provider: project.provider,
                        projectId: project.id,
                        projectName: project.title,
                        versionId: chosen?.id,
                        versionNumber: chosen?.versionNumber,
                        loader: chosen?.loaders?.[0],
                        gameVersion: chosen?.gameVersions?.[0],
                      },
                    })
                  }
                >
                  <Download className="h-3 w-3" /> {pending ? intl.formatMessage({ id: "web.routes.tabs.mods.installing", defaultMessage: "Installing…", description: "Mods: label of the install button while the mod installs" }) : intl.formatMessage({ id: "web.routes.tabs.mods.install", defaultMessage: "Install", description: "Mods: label of the button that installs a mod" })}
                </Button>
              )}
            </>
          )}
        </div>
      )}
    </div>
  );
}

function errMsg(err: unknown, intl: IntlShape): string {
  if (err instanceof APIError) {
    try {
      const parsed = JSON.parse(err.body) as { error?: string };
      if (parsed.error) return parsed.error;
    } catch {
      // fall through
    }
    if (err.status === 403) return intl.formatMessage({ id: "web.routes.tabs.mods.error_not_permitted", defaultMessage: "Your role does not allow managing mods.", description: "Mods: error shown when the user's role cannot manage mods" });
    return err.body || intl.formatMessage({ id: "web.routes.tabs.mods.error_request_failed_status", defaultMessage: "request failed ({status})", description: "Mods: fallback error text for an API error with no body; {status} is the HTTP status code" }, { status: String(err.status) });
  }
  return errorTextIntl(intl, err, intl.formatMessage({ id: "web.routes.tabs.mods.error_request_failed", defaultMessage: "request failed", description: "Mods: fallback error text when the error has no message" }));
}
