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
import { resolveCategories } from "@/lib/games";
import type { EntryVerify } from "@/lib/verify";
import type { CatalogEntry } from "@/types";

interface ModuleCardProps {
  cluster?: string;
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
  canManage = true,
  verify,
  onInstall,
  onUpgrade,
  onUninstall,
  onRemoveUpload,
  busy,
}: ModuleCardProps) {
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
    ? `v${entry.installedVersion ?? "?"} installed${
        upgradeAvailable ? ` · v${entry.latestVersion} available` : ""
      }`
    : entry.latestVersion
    ? `v${entry.latestVersion}`
    : "no published versions";

  // Installed entries show the digest of the running bundle; catalog
  // entries show the latest available digest.
  const digest = entry.appliedDigest ?? entry.digest;

  return (
    <Card className="flex flex-col h-full">
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
          <div className="font-medium text-foreground">
            {entry.displayName ?? entry.name}
          </div>
          <div className="pt-0.5 font-mono text-[11px] text-muted">{versionLabel}</div>
          {digest && (
            <div className="flex items-center gap-1 pt-0.5 font-mono text-[10px] text-muted">
              <Fingerprint className="h-3 w-3 shrink-0" />
              <span className="truncate">{shortDigest(digest)}</span>
            </div>
          )}
          {entry.installed && entry.previousVersion && (
            <div className="flex items-center gap-1 pt-0.5 text-[10px] text-muted">
              <History className="h-3 w-3 shrink-0" />
              rollback target · v{entry.previousVersion}
            </div>
          )}
        </div>
        {resolveCategories(entry.categories, entry.game ?? entry.name).length > 0 && (
          <div className="flex flex-wrap gap-1.5 pt-0.5">
            {resolveCategories(entry.categories, entry.game ?? entry.name).map((c) => (
              <Chip key={c} variant="soft" size="sm" className="text-[11px]">
                {c}
              </Chip>
            ))}
          </div>
        )}
      </CardHeader>
      <CardContent className="flex flex-col gap-3 flex-1 py-3">
        <p className="line-clamp-3 text-xs text-muted leading-[1.4]">
          {entry.summary ?? "No summary."}
        </p>
        {versionUnavailable ? (
          <div className="rounded border border-primary/40 bg-primary/10 px-2 py-1 text-[11px] text-primary">
            Pinned to v{entry.pinnedVersion} — no longer in the catalog. Update to v
            {entry.latestVersion}.
          </div>
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
      <CardFooter className="flex flex-wrap items-center justify-between gap-2 text-[11px] text-muted pt-0">
        <span className="font-mono">
          {entry.sources.length === 1
            ? `${entry.sources[0].name} (${entry.sources[0].type})`
            : `${entry.sources.length} sources`}
        </span>
        <div className="flex items-center gap-1">
          {entry.installed && entry.phase === "Ready" && entry.moduleName && (
            <Button
              size="sm"
              variant="outline"
              onPress={() => void deploy()}
              className="h-8"
            >
              <ExternalLink className="h-3.5 w-3.5" /> {cluster === "local" ? "Deploy locally" : `Deploy to ${cluster}`}
            </Button>
          )}
          {upgradeAvailable && entry.phase === "Ready" && (
            <Button
              size="sm"
              onPress={() => onUpgrade(entry)}
              isDisabled={busy || !canManage}
              className="h-8"
            >
              <ArrowUpCircle className="h-3.5 w-3.5" />
              Upgrade
            </Button>
          )}
          {versionUnavailable && (
            <Button
              size="sm"
              onPress={() => onUpgrade(entry)}
              isDisabled={busy || !canManage}
              className="h-8"
            >
              <ArrowUpCircle className="h-3.5 w-3.5" />
              Update to v{entry.latestVersion}
            </Button>
          )}
          {!entry.installed && (
            <Button
              size="sm"
              onPress={() => onInstall(entry)}
              isDisabled={busy || !canManage}
              className="h-8"
            >
              {busy ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : <Download className="h-3.5 w-3.5" />}
              Install
            </Button>
          )}
          {entry.installed && (
            <Button
              size="sm"
              variant="outline"
              onPress={() => onUninstall(entry)}
              isDisabled={busy || !!inFlight || !canManage}
              className="h-8"
            >
              Uninstall
            </Button>
          )}
          {entry.sources.some((s) => s.type === "upload") && (
            <Button
              size="sm"
              variant="outline"
              onPress={() => onRemoveUpload?.(entry)}
              isDisabled={busy || !canManage}
              className="h-8"
            >
              <Trash2 className="h-3.5 w-3.5" />
              Remove upload
            </Button>
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
  if (verify.mode === "none" || verify.mixed) return null;
  const keyless = verify.mode === "keyless";
  if (verify.enforced) {
    return (
      <Chip
        color="success"
        size="sm"
        variant="soft"
        title={keyless ? "keyless (Fulcio) signature verified" : "signature verified"}
        className="font-mono text-[10px] uppercase inline-flex items-center gap-1"
        data-color="success"
      >
        <ShieldCheck className="h-3 w-3" />
        verified
      </Chip>
    );
  }
  return (
    <Chip
      color="success"
      size="sm"
      variant="soft"
      title={keyless ? "keyless (Fulcio) signature policy declared" : "signature policy declared"}
      className="font-mono text-[10px] uppercase inline-flex items-center gap-1"
      data-color="success"
    >
      <ShieldQuestion className="h-3 w-3" />
      policy
    </Chip>
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
  let label = "available";
  let color: "default" | "success" | "accent" | "danger" | "warning" = "default";
  if (entry.installed) {
    if (entry.phase === "Ready") {
      label = "installed";
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
      label = "update";
      color = "accent";
    } else if (entry.phase === "Failed") {
      label = "failed";
      color = "danger";
    } else {
      label = (entry.phase ?? "pending").toLowerCase();
      color = "warning";
    }
  }
  return (
    <Chip
      color={color}
      size="sm"
      variant="soft"
      className="font-mono text-[10px] uppercase"
    >
      {label}
    </Chip>
  );
}
