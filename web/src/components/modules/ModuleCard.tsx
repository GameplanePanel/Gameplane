import { useNavigate } from "@tanstack/react-router";
import {
  ArrowUpCircle,
  Download,
  ExternalLink,
  Fingerprint,
  History,
  Loader2,
  ShieldCheck,
  ShieldQuestion,
  Trash2,
} from "lucide-react";
import { Button, Card, CardHeader, CardContent, CardFooter, Chip } from "@heroui/react";
import { GameIcon } from "@/components/ui/GameIcon";
import { useGameCodes } from "@/lib/useGameCodes";
import { categoryLabel, resolveCategories } from "@/lib/games";
import type { EntryVerify } from "@/lib/verify";
import type { CatalogEntry } from "@/types";
import { cn } from "@/lib/utils";
import { useOptionalIntl } from "@/lib/i18n/format";

interface ModuleCardProps {
  cluster?: string;
  clusterDisplayName?: string;
  canManage?: boolean;
  entry: CatalogEntry;
  // Verification posture for this entry, joined client-side from the
  // sources list by the parent (see lib/verify.verifyForEntry).
  verify?: EntryVerify;
  onInstall: (entry: CatalogEntry) => void;
  onUpgrade: (entry: CatalogEntry) => void;
  onUninstall: (entry: CatalogEntry) => void;
  onRemoveUpload?: (entry: CatalogEntry) => void;
  busy?: boolean;
}

// ModuleCard is the catalog grid tile. The right-hand action depends
// on installation state:
//   - not installed      → "Install"
//   - installed, current  → "Deploy" (navigate to /servers/new)
//   - upgrade available  → "Upgrade" (healthy: a newer version is out)
//   - VersionUnavailable → "Update to vX" (Failed but recoverable: the
//                          pinned version left the catalog, so offer the
//                          available one instead of a dead red error)
//   - other phase != Ready → render the phase + spinner (no actions)
export function ModuleCard({
  entry,
  cluster = "local",
  clusterDisplayName = cluster,
  canManage = true,
  verify,
  onInstall,
  onUpgrade,
  onUninstall,
  onRemoveUpload,
  busy,
}: ModuleCardProps) {
  const intl = useOptionalIntl();
  const { gameCodes, byName } = useGameCodes(cluster);
  const navigate = useNavigate();
  const deploy = async () => {
    await navigate({ to: "/servers/new", search: { template: entry.moduleName, cluster } });
  };
  const upgradeAvailable =
    entry.installed &&
    entry.installedVersion &&
    entry.latestVersion &&
    entry.latestVersion !== entry.installedVersion;

  // The pinned version left the catalog (e.g. a git-era version the OCI
  // source never carried). This is a Failed state, but an actionable one:
  // the catalog still has a version, so offer to update rather than dead-end
  // on a red error. Distinct from upgradeAvailable, which is the healthy
  // (Ready) "newer version out" case.
  const isVersionUnavailable = (e: CatalogEntry): boolean => {
    const pinnedInCatalog =
      !e.pinnedVersion || (e.versions ?? []).includes(e.pinnedVersion);
    return (
      e.installed &&
      e.phase === "Failed" &&
      e.reason === "VersionUnavailable" &&
      !pinnedInCatalog &&
      !!e.latestVersion
    );
  };

  const versionUnavailable = isVersionUnavailable(entry);

  // A module is in-flight if it's installed and the phase is actively
  // changing (neither Ready nor a terminal failure state). Failed is terminal
  // and must allow Uninstall.
  const inFlight = entry.installed && !!entry.phase && entry.phase !== "Ready" && entry.phase !== "Failed";

  const versionLabel = entry.installed
    ? `${intl.formatMessage({ id: "web.components.modules.module_card.installed_version", defaultMessage: "v{version} installed", description: "Module card: installed version text; {version} is the installed module version" }, { version: entry.installedVersion ?? "?" })}${
        upgradeAvailable ? intl.formatMessage({ id: "web.components.modules.module_card.update_available_suffix", defaultMessage: " · v{version} available", description: "Module card: suffix naming the newer version that is available; {version} is that version" }, { version: entry.latestVersion }) : ""
      }`
    : entry.latestVersion
    ? intl.formatMessage({ id: "web.components.modules.module_card.latest_version", defaultMessage: "v{version}", description: "Module card: latest published version; {version} is the version number" }, { version: entry.latestVersion })
    : intl.formatMessage({ id: "web.components.modules.module_card.no_published_versions", defaultMessage: "no published versions", description: "Module card: shown when a module has no published version" });

  // Installed entries show the digest of the running bundle; catalog
  // entries show the latest available digest.
  const digest = entry.appliedDigest ?? entry.digest;

  return (
    <Card className="flex flex-col border border-border">
      <CardHeader className="flex flex-col gap-3 pb-0">
        <div className="flex items-center justify-between gap-3">
          <GameIcon
            game={entry.moduleName ?? entry.game ?? entry.name}
            icon={byName.get(entry.moduleName ?? entry.game ?? entry.name)?.spec.icon}
            code={gameCodes.get(entry.moduleName ?? entry.game ?? entry.name)}
            size="md"
          />
          <div className="flex items-center gap-1.5">
            {verify && <VerifyBadge verify={verify} />}
            <StatusPill entry={entry} />
          </div>
        </div>
        <div>
          <div className="text-base font-medium text-foreground">
            {entry.displayName ?? entry.name}
          </div>
          <div className="font-mono text-[11px] text-muted">{versionLabel}</div>
          {digest && (
            <div className="flex items-center gap-1 font-mono text-[10px] text-muted">
              <Fingerprint className="h-3 w-3 shrink-0" />
              <span className="truncate">{shortDigest(digest)}</span>
            </div>
          )}
          {entry.installed && entry.previousVersion && (
            <div className="flex items-center gap-1 text-[10px] text-muted">
              <History className="h-3 w-3 shrink-0" />{intl.formatMessage({ id: "web.components.modules.module_card.rollback_target_vpreviousversion", defaultMessage: "rollback target · v{previousVersion}", description: "Module card: sentence with previousVersion in div" }, { previousVersion: entry.previousVersion })}</div>
          )}
        </div>
        {resolveCategories(entry.categories, entry.game ?? entry.name).length > 0 && (
          <div className="flex flex-wrap gap-1.5">
            {resolveCategories(entry.categories, entry.game ?? entry.name).map((c) => (
              <Chip key={c} variant="tertiary" size="sm" className="text-[11px] font-medium">
                {categoryLabel(c, intl)}
              </Chip>
            ))}
          </div>
        )}
      </CardHeader>
      <CardContent className="flex flex-col gap-3 flex-1 py-0">
        <p className="line-clamp-4 text-xs text-muted leading-[1.4]" title={entry.summary ?? undefined}>
          {entry.summary ?? intl.formatMessage({ id: "web.components.modules.module_card.no_summary", defaultMessage: "No summary.", description: "Module card: text shown when a module has no summary" })}
        </p>
        {versionUnavailable ? (
          <div className="rounded border border-primary/40 bg-primary/10 px-2 py-1 text-[11px] text-primary">{intl.formatMessage({ id: "web.components.modules.module_card.pinned_to_vpinnedversion_no_longer_in", defaultMessage: "Pinned to v{pinnedVersion} — no longer in the catalog. Update to v{latestVersion}.", description: "Module card: sentence with pinnedVersion, latestVersion in div" }, { pinnedVersion: entry.pinnedVersion, latestVersion: entry.latestVersion })}</div>
        ) : entry.phase === "Failed" &&
          entry.reason !== "VersionUnavailable" &&
          entry.lastError ? (
          // Only surface the raw error for a genuine, current failure. Gating on
          // phase===Failed hides a stale error while the operator is Pulling a
          // new version; excluding VersionUnavailable keeps the actionable
          // "update" banner (above) the sole treatment for that reason and
          // suppresses its leftover error during a re-pin transition.
          <div className="rounded border border-danger/40 bg-danger/10 px-2 py-1 text-[11px] text-danger">
            {entry.lastError}
          </div>
        ) : null}
      </CardContent>
      <CardFooter
        className={cn(
          "flex flex-wrap gap-2 text-[11px] text-muted pt-0",
          entry.installed ? "flex-col items-start" : "items-center justify-between",
        )}
      >
        <span className="font-mono">
          {entry.sources.length === 1
            ? intl.formatMessage({ id: "web.components.modules.module_card.source_label", defaultMessage: "{name} ({type})", description: "Module card: module source name and type; {name} is the source name, {type} is the source type" }, { name: entry.sources[0].name, type: entry.sources[0].type })
            : intl.formatMessage({ id: "web.components.modules.module_card.sources_count", defaultMessage: "{count, plural, one {# source} other {# sources}}", description: "Module card: number of module sources; {count} is the count" }, { count: entry.sources.length })}
        </span>
        <div className="flex max-w-full flex-wrap items-center gap-1">
          {entry.installed && entry.phase === "Ready" && entry.moduleName && (
            <Button
              size="sm"
              variant="outline"
              onPress={() => void deploy()}
              className="min-h-8 h-auto max-w-full whitespace-normal text-left"
            >
              <ExternalLink className="h-3.5 w-3.5" /> {cluster === "local" ? intl.formatMessage({ id: "web.components.modules.module_card.deploy_locally", defaultMessage: "Deploy locally", description: "Module card: button label to deploy the module to the local cluster" }) : intl.formatMessage({ id: "web.components.modules.module_card.deploy_to", defaultMessage: "Deploy to {cluster}", description: "Module card: button label to deploy the module to a remote cluster; {cluster} is the cluster name" }, { cluster: clusterDisplayName })}
            </Button>
          )}
          {upgradeAvailable && entry.phase === "Ready" && (
            <Button
              size="sm"
              onPress={() => onUpgrade(entry)}
              isDisabled={busy || !canManage}
              className="h-8"
            >
              <ArrowUpCircle className="h-3.5 w-3.5" />{intl.formatMessage({ id: "web.components.modules.module_card.upgrade", defaultMessage: "Upgrade", description: "Module card: button that upgrades an installed module to its latest version; keep it short" })}</Button>
          )}
          {versionUnavailable && (
            <Button
              size="sm"
              onPress={() => onUpgrade(entry)}
              isDisabled={busy || !canManage}
              className="h-8"
            >
              <ArrowUpCircle className="h-3.5 w-3.5" />{intl.formatMessage({ id: "web.components.modules.module_card.update_to_vlatestversion", defaultMessage: "Update to v{latestVersion}", description: "Module card: sentence with latestVersion in Button" }, { latestVersion: entry.latestVersion })}</Button>
          )}
          {!entry.installed && (
            <Button
              size="sm"
              onPress={() => onInstall(entry)}
              isDisabled={busy || !canManage}
              className="h-8"
            >
              {busy ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : <Download className="h-3.5 w-3.5" />}{intl.formatMessage({ id: "web.components.modules.module_card.install", defaultMessage: "Install", description: "Module card: button that installs a module that is not installed yet; keep it short" })}</Button>
          )}
          {entry.installed && (
            <Button
              size="sm"
              variant="outline"
              onPress={() => onUninstall(entry)}
              isDisabled={busy || !!inFlight || !canManage}
              className="h-8"
            >{intl.formatMessage({ id: "web.components.modules.module_card.uninstall", defaultMessage: "Uninstall", description: "Module card: button that removes an installed module; keep it short" })}</Button>
          )}
          {entry.sources.some((s) => s.type === "upload") && (
            <Button
              size="sm"
              variant="outline"
              onPress={() => onRemoveUpload?.(entry)}
              isDisabled={busy || !canManage}
              className="h-8"
            >
              <Trash2 className="h-3.5 w-3.5" />{intl.formatMessage({ id: "web.components.modules.module_card.remove_upload", defaultMessage: "Remove upload", description: "Module card: button that removes an uploaded module bundle; keep it short" })}</Button>
          )}
        </div>
      </CardFooter>
    </Card>
  );
}

// VerifyBadge surfaces a module's cosign posture in three states, taking
// care never to over-claim (absence of a badge is the neutral state — we
// never render a red "unsigned" pill, which would be noise on community
// modules):
//   - enforced   → the installed bytes were signature-checked: a solid
//                  "verified" badge.
//   - policy     → a source declares a signature policy but nothing is
//                  installed yet (enforced=false): a softer outline "policy"
//                  badge, so it reads as "declared", not "checked".
//   - mixed/none → suppressed. Mixed candidate sources disagree, so a single
//                  badge would over-claim; none is the neutral default.
// The keyed-vs-keyless distinction lives in the tooltip, not the label — the
// confident badge is about the running-bytes fact, not the policy flavor.
function VerifyBadge({ verify }: { verify: EntryVerify }) {
  const intl = useOptionalIntl();
  if (verify.mode === "none" || verify.mixed) return null;
  const keyless = verify.mode === "keyless";
  if (verify.enforced) {
    return (
      <Chip
        color="success"
        size="sm"
        variant="soft"
        title={keyless ? intl.formatMessage({ id: "web.components.modules.module_card.keyless_verified", defaultMessage: "keyless (Fulcio) signature verified", description: "Module card: tooltip when a keyless (Fulcio) signature is verified" }) : intl.formatMessage({ id: "web.components.modules.module_card.signature_verified", defaultMessage: "signature verified", description: "Module card: tooltip when a signature is verified" })}
        className="font-mono text-[10px] uppercase font-normal inline-flex items-center gap-1"
        data-color="success"
      >
        <ShieldCheck className="h-3 w-3" />{intl.formatMessage({ id: "web.components.modules.module_card.verified", defaultMessage: "verified", description: "Module card: small badge meaning the module's signature was verified; its tooltip says whether keyless (Fulcio) verification passed; keep it short" })}</Chip>
    );
  }
  return (
    <Chip
      color="success"
      size="sm"
      variant="soft"
      title={keyless ? intl.formatMessage({ id: "web.components.modules.module_card.keyless_policy", defaultMessage: "keyless (Fulcio) signature policy declared", description: "Module card: tooltip when a keyless (Fulcio) signature policy is declared" }) : intl.formatMessage({ id: "web.components.modules.module_card.signature_policy", defaultMessage: "signature policy declared", description: "Module card: tooltip when a signature policy is declared" })}
      className="font-mono text-[10px] uppercase font-normal inline-flex items-center gap-1"
      data-color="success"
    >
      <ShieldQuestion className="h-3 w-3" />{intl.formatMessage({ id: "web.components.modules.module_card.policy", defaultMessage: "policy", description: "Module card: small badge meaning a signature policy is declared for the module; its tooltip says whether keyless (Fulcio) signing is used; keep it short" })}</Chip>
  );
}

// shortDigest trims a "<algo>:<hash>" digest to a glanceable form, keeping
// the algorithm prefix and head/tail of the hash.
function shortDigest(d: string): string {
  const idx = d.indexOf(":");
  const algo = idx >= 0 ? d.slice(0, idx) : "";
  const hash = idx >= 0 ? d.slice(idx + 1) : d;
  const short = hash.length > 16 ? `${hash.slice(0, 8)}…${hash.slice(-4)}` : hash;
  return algo ? `${algo}:${short}` : short;
}

function StatusPill({ entry }: { entry: CatalogEntry }) {
  const intl = useOptionalIntl();
  let label = intl.formatMessage({ id: "web.components.modules.module_card.status_available", defaultMessage: "available", description: "Module card: status chip for a module that is not installed" });
  let color: "default" | "success" | "accent" | "danger" | "warning" = "default";
  if (entry.installed) {
    if (entry.phase === "Ready") {
      label = intl.formatMessage({ id: "web.components.modules.module_card.status_installed", defaultMessage: "installed", description: "Module card: status chip for an installed module" });
      color = "success";
    } else if (
      entry.installed &&
      entry.phase === "Failed" &&
      entry.reason === "VersionUnavailable" &&
      !!entry.pinnedVersion &&
      !(entry.versions ?? []).includes(entry.pinnedVersion) &&
      !!entry.latestVersion
    ) {
      // Failed, but recoverable via an update — read as actionable, not broken.
      // Guarded on the pin genuinely being gone (not a stale status mid-re-pin)
      // and that a newer version is available.
      label = intl.formatMessage({ id: "web.components.modules.module_card.status_update", defaultMessage: "update", description: "Module card: status chip for an installed module with a newer version available" });
      color = "accent";
    } else if (entry.phase === "Failed") {
      label = intl.formatMessage({ id: "web.components.modules.module_card.status_failed", defaultMessage: "failed", description: "Module card: status chip for an installed module whose last operation failed" });
      color = "danger";
    } else {
      label = entry.phase ? entry.phase.toLowerCase() : intl.formatMessage({ id: "web.components.modules.module_card.status_pending", defaultMessage: "pending", description: "Module card: status chip for an installed module with no phase yet" });
      color = "warning";
    }
  }
  return (
    <Chip
      color={color}
      size="sm"
      variant="soft"
      className="font-mono text-[10px] uppercase font-normal"
    >
      {label}
    </Chip>
  );
}
