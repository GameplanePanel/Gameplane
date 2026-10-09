import { useResourceClient, useResourceAccess, useResourceTarget, resourceKey } from "@/lib/resourceTarget";
import { useEffect, useMemo, useRef, useState, Suspense, lazy } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Button, Tabs } from "@heroui/react";
import {
  AlertTriangle,
  CalendarClock,
  HardDrive,
  Layers,
  Link2,
  MapPin,
  Network,
  RadioTower,
  Settings as SettingsIcon,
  ShieldCheck,
  Sliders,
  SlidersHorizontal,
  Variable,
} from "lucide-react";

import type { GameServer } from "@/types";
import { APIError } from "@/lib/api";
import { errorText } from "@/lib/errors";
import { mergeDraftOntoLatest, SettingsConflictError } from "@/lib/settingsMerge";
import { validateConfig } from "@/lib/validation";


import { GeneralSection } from "./settings/General";
import { VersionSection } from "./settings/Version";
import { GameConfigSection } from "./settings/GameConfig";
import { ResourcesSection } from "./settings/Resources";
import { NetworkingSection } from "./settings/Networking";
import { EnvVarsSection } from "./settings/EnvVars";
import { LifecycleSection } from "./settings/Lifecycle";
import { BackupsSection } from "./settings/Backups";
import { NetworkCaptureSection } from "./settings/NetworkCapture";
import { AccessSection } from "./settings/Access";
import { ShareLinksSection } from "./settings/ShareLinks";
import { DangerSection } from "./settings/Danger";
const PlacementSection = lazy(() =>
  import("./settings/Placement").then((m) => ({ default: m.PlacementSection })),
);

type SectionKey =
  | "general"
  | "version"
  | "config"
  | "resources"
  | "networking"
  | "env"
  | "lifecycle"
  | "backups"
  | "capture"
  | "placement"
  | "access"
  | "sharelinks"
  | "danger";

const SECTIONS: { key: SectionKey; label: string; icon: typeof SettingsIcon }[] = [
  { key: "general",    label: "General",       icon: SettingsIcon },
  { key: "version",    label: "Version",       icon: Layers },
  { key: "config",     label: "Game configuration", icon: SlidersHorizontal },
  { key: "resources",  label: "Resources",     icon: HardDrive },
  { key: "networking", label: "Networking",    icon: Network },
  { key: "env",        label: "Environment",   icon: Variable },
  { key: "lifecycle",  label: "Lifecycle",     icon: Sliders },
  { key: "backups",    label: "Scheduled backups", icon: CalendarClock },
  { key: "capture",    label: "Network capture", icon: RadioTower },
  { key: "placement",  label: "Placement",     icon: MapPin },
  { key: "access",     label: "RBAC & access", icon: ShieldCheck },
  { key: "sharelinks", label: "Share links",   icon: Link2 },
  { key: "danger",     label: "Danger zone",   icon: AlertTriangle },
];

export interface SettingsTabProps {
  gs?: GameServer;
  name: string;
  ns?: string;
  onDirtyChange?: (dirty: boolean) => void;
}

export function SettingsTab({ gs, name, ns, onDirtyChange }: SettingsTabProps) {
  const resourceTarget = useResourceTarget({ name, namespace: ns });
  const access = useResourceAccess();
  const resourceClient = useResourceClient(resourceTarget);
  const { Servers } = resourceClient;
  const qc = useQueryClient();
  const [section, setSection] = useState<SectionKey>("general");
  const [draft, setDraft] = useState<GameServer | null>(null);
  const [conflict, setConflict] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [savedAt, setSavedAt] = useState<number | null>(null);
  const [sectionValidity, setSectionValidity] = useState<Partial<Record<SectionKey, boolean>>>({});
  const [draftRevision, setDraftRevision] = useState(0);
  const savingRef = useRef(false);
  const validityCallbacks = useMemo(() => {
    const report = (key: SectionKey) => (valid: boolean) => {
      if (savingRef.current) return;
      setSectionValidity((previous) => previous[key] === valid ? previous : { ...previous, [key]: valid });
    };
    return { config: report("config"), networking: report("networking"), capture: report("capture"), placement: report("placement") };
  }, []);
  // `dirty` (does draft differ from the last known-good server snapshot) is
  // read directly in JSX, so it's tracked as its own piece of state,
  // recomputed imperatively at every site that mutates `draft` or the
  // baseline. That keeps `baselineRef` a plain ref (safe to read in effects
  // and handlers, never during render) instead of needing its `.current` in
  // a render-time computation.
  const [dirty, setDirty] = useState(false);
  const baselineRef = useRef<GameServer | null>(null);
  const lastSeenRef = useRef<GameServer | undefined>(undefined);

  // Initialize / reset the draft whenever a fresh server arrives and
  // the local form has no unsaved edits. We compare against the last gs
  // we processed (not against draft) so this effect only fires on real
  // gs changes, not on draft mutations.
  useEffect(() => {
    if (!gs) return;
    if (gs === lastSeenRef.current) return;
    lastSeenRef.current = gs;
    if (baselineRef.current && draft && isDirty(draft, baselineRef.current)) {
      // Local edits in flight — don't clobber.
      return;
    }
    const clone = structuredClone(gs);
    baselineRef.current = clone;
    setDraft(clone);
    setConflict(false);
    setDirty(false);
    setSectionValidity({});
    setDraftRevision((revision) => revision + 1);
  }, [gs, draft]);

  useEffect(() => {
    onDirtyChange?.(dirty);
  }, [dirty, onDirtyChange]);

  const { data: template } = useQuery({
    queryKey: resourceKey(resourceTarget, "template", draft?.spec.templateRef.name),
    queryFn: ({ signal }) => resourceClient.withSignal(signal).Templates.get(draft!.spec.templateRef.name),
    enabled: !!draft?.spec.templateRef.name,
  });

  // Validate config from the shared draft even when its section is unmounted.
  // Other sections retain their reported validity across navigation, including
  // invalid editor text that has not yet been committed into the draft.
  const draftValid = !!template &&
    validateConfig(template.spec.configSchema ?? [], draft?.spec.config ?? {}).length === 0 &&
    Object.values(sectionValidity).every(Boolean);

  const save = useMutation({
    onMutate: () => { savingRef.current = true; },
    onSettled: () => { savingRef.current = false; },
    mutationFn: async (next: GameServer) => {
      if (!template || validateConfig(template.spec.configSchema ?? [], next.spec.config ?? {}).length > 0 ||
        !Object.values(sectionValidity).every(Boolean)) {
        throw new Error("Correct invalid settings before saving.");
      }
      // Re-fetch latest to merge edits onto the freshest copy. This
      // keeps fields the UI doesn't model (e.g. operator-managed status,
      // newly-added spec keys) from being clobbered.
      const latest = await Servers.get(name, ns);
      const merged = mergeDraftOntoLatest(next, baselineRef.current!, latest);
      return Servers.update(name, merged, ns);
    },
    onSuccess: (saved) => {
      void qc.invalidateQueries({ queryKey: ["fleet"] });
      const clone = structuredClone(saved);
      baselineRef.current = clone;
      setDraft(clone);
      setDirty(false);
      setConflict(false);
      setError(null);
      setSavedAt(Date.now());
      setSectionValidity({});
      setDraftRevision((revision) => revision + 1);
      return qc.invalidateQueries({ queryKey: resourceKey(resourceTarget, "server", name, ns) });
    },
    onError: (err) => {
      if (err instanceof SettingsConflictError || (err instanceof APIError && err.status === 409)) {
        setConflict(true);
        setError(null);
      } else {
        setError(errMsg(err));
      }
    },
  });

  if (!draft) {
    return <div className="p-6 text-sm text-muted">Loading…</div>;
  }

  const reset = () => {
    if (baselineRef.current) {
      setDraft(structuredClone(baselineRef.current));
      setDirty(false);
    }
    setConflict(false);
    setError(null);
    setSectionValidity({});
    setDraftRevision((revision) => revision + 1);
  };

  const reload = async () => {
    try {
      const fresh = await Servers.get(name, ns);
      const clone = structuredClone(fresh);
      baselineRef.current = clone;
      setDraft(clone);
      setDirty(false);
      setConflict(false);
      setError(null);
      setSectionValidity({});
      setDraftRevision((revision) => revision + 1);
      qc.setQueryData(resourceKey(resourceTarget, "server", name, ns), fresh);
    } catch (err) {
      setError(errMsg(err));
    }
  };

  const onChangeDraft = (next: GameServer) => {
    if (savingRef.current) return;
    setDraft(next);
    setDirty(baselineRef.current ? isDirty(next, baselineRef.current) : false);
    if (savedAt) setSavedAt(null);
  };

  // The Version section only exists for templates with a version catalog;
  // Game configuration only for templates with a configSchema (or when the
  // server still carries config keys that need removing, because the operator
  // rejects unknown keys).
  const sections = SECTIONS.filter((s) => {
    if (s.key === "version") return (template?.spec.versions?.length ?? 0) > 0;
    if (s.key === "config") {
      return (
        (template?.spec.configSchema?.length ?? 0) > 0 ||
        Object.keys(draft.spec.config ?? {}).length > 0
      );
    }
    return true;
  });
  // Without servers:write the Game configuration section is view-only and the
  // footer shows a note instead of Discard/Save.
  const readOnlyConfig = section === "config" && !access?.canWrite;

  return (
    <div className="flex h-full">
      <nav className="w-56 shrink-0 border-r border-border bg-surface/30 p-2">
        <Tabs
          selectedKey={section}
          onSelectionChange={(k) => {
            setSection(k as SectionKey);
          }}
          orientation="vertical"
        >
          <Tabs.List>
            {sections.map((s) => (
              <Tabs.Tab key={s.key} id={s.key}>
                {s.label}
              </Tabs.Tab>
            ))}
          </Tabs.List>
        </Tabs>
      </nav>

      <div className="flex min-w-0 flex-1 flex-col">
        {/* Share dialogs own their state independently of the settings draft.
            A status refresh must not discard a newly issued, one-time token. */}
        <fieldset key={section === "sharelinks" ? "sharelinks" : draftRevision} disabled={save.isPending} inert={save.isPending} className="min-w-0 flex-1 overflow-auto border-0 p-6 scrollbar-thin">
          {section === "general"    && <GeneralSection    draft={draft} onChange={onChangeDraft} template={template} />}
          {section === "version"    && <VersionSection    draft={draft} onChange={onChangeDraft} template={template} />}
          {section === "config"     && <GameConfigSection draft={draft} onChange={onChangeDraft} template={template} onValidityChange={validityCallbacks.config} />}
          {section === "resources"  && <ResourcesSection  draft={draft} onChange={onChangeDraft} template={template} />}
          {section === "networking" && <NetworkingSection draft={draft} onChange={onChangeDraft} template={template} onValidityChange={validityCallbacks.networking} />}
          {section === "env"        && <EnvVarsSection    draft={draft} onChange={onChangeDraft} template={template} />}
          {section === "lifecycle"  && <LifecycleSection  draft={draft} onChange={onChangeDraft} template={template} />}
          {section === "backups"    && <BackupsSection    draft={draft} onChange={onChangeDraft} template={template} />}
          {section === "capture"    && <NetworkCaptureSection draft={draft} onChange={onChangeDraft} template={template} onValidityChange={validityCallbacks.capture} />}
          {section === "placement"  && (
            <Suspense fallback={<div className="text-sm text-muted">Loading…</div>}>
              <PlacementSection
                draft={draft}
                onChange={onChangeDraft}
                template={template}
                onValidityChange={validityCallbacks.placement}
              />
            </Suspense>
          )}
          {section === "access"     && <AccessSection     gs={gs} />}
          {section === "sharelinks" && <ShareLinksSection name={name} ns={ns} />}
          {section === "danger"     && <DangerSection     name={name} ns={ns} />}
        </fieldset>

        {section !== "danger" && section !== "access" && section !== "sharelinks" && (
          <footer className="flex items-center justify-between gap-4 border-t border-border bg-surface/30 px-6 py-3">
            <div className="min-w-0 text-xs">
              {conflict && !error && (
                <span className="text-warning">
                  Server changed since you opened this page.{" "}
                  <button onClick={() => void reload()} className="underline hover:text-fg">
                    Reload
                  </button>{" "}
                  to discard your edits and load the latest.
                </span>
              )}
              {error && (
                <span className="text-danger">
                  {error}
                  {conflict && (
                    <>
                      {" "}
                      <button onClick={() => void reload()} className="underline hover:text-fg">
                        Retry reload
                      </button>
                    </>
                  )}
                </span>
              )}
              {!conflict && !error && dirty && (
                <span className="text-muted">Unsaved changes</span>
              )}
              {!conflict && !error && !dirty && savedAt && (
                <span className="text-muted">Saved.</span>
              )}
              {readOnlyConfig && (
                <span className="text-muted">
                  You need permission to change this server&apos;s settings.
                </span>
              )}
            </div>
            {!readOnlyConfig && (
            <div className="flex items-center gap-2">
              <Button
                variant="ghost"
                size="sm"
                onPress={reset}
                isDisabled={!dirty || save.isPending}
              >
                Discard
              </Button>
              <Button
                size="sm"
                onPress={() => save.mutate(draft)}
                isDisabled={!access?.canWrite || !dirty || !draftValid || save.isPending}
              >
                {save.isPending ? "Saving…" : "Save changes"}
              </Button>
            </div>
            )}
          </footer>
        )}
      </div>
    </div>
  );
}

function isDirty(a: GameServer, b: GameServer): boolean {
  return JSON.stringify(serializeForDiff(a)) !== JSON.stringify(serializeForDiff(b));
}

// Strip status + resourceVersion before diffing so server-side updates
// (heartbeats, conditions) don't mark the form as dirty.
function serializeForDiff(gs: GameServer) {
  const meta = { ...gs.metadata };
  delete meta.resourceVersion;
  return { metadata: meta, spec: gs.spec };
}

function errMsg(err: unknown): string {
  return errorText(err, "save failed");
}
