import { AlertTriangle, Info } from "lucide-react";

import { RadioGroup, Radio, Button, Chip } from "@heroui/react";
import type { SectionProps } from "./types";
import { useOptionalIntl } from "@/lib/i18n/format";
import { identityText, moduleIds } from "@/lib/i18n/moduleText";

// VersionSection lets an existing server switch to another entry of the
// template's version catalog (spec.versions) — the same choice the Create
// wizard offers, post-create. Saving rides the tab's read-merge-PUT; the
// operator re-renders the StatefulSet (image/env/per-loader mod volume)
// and the pod restarts on the new version.
export function VersionSection({ draft, onChange, template, moduleText }: SectionProps) {
  const intl = useOptionalIntl();
  const versions = template?.spec.versions ?? [];
  const defaultId = versions.find((v) => v.default)?.id ?? versions[0]?.id ?? "";
  const selectedId = draft.spec.version || defaultId;
  const hasLoaderVolumes = Object.keys(
    template?.spec.capabilities?.mods?.loaders ?? {},
  ).length > 0;

  if (versions.length === 0) {
    return (
      <p className="text-sm text-muted">{draft.spec.image
        ? intl.formatMessage({ id: "web.routes.tabs.settings.version.no_catalog_image_overridden", defaultMessage: "This template has no version catalog — the server always runs the template image (currently overridden below in General).", description: "Version: note shown when the template has no version catalog and the image is overridden in General" })
        : intl.formatMessage({ id: "web.routes.tabs.settings.version.no_catalog", defaultMessage: "This template has no version catalog — the server always runs the template image.", description: "Version: note shown when the template has no version catalog" })}
      </p>
    );
  }

  const pick = (id: string) =>
    onChange({ ...draft, spec: { ...draft.spec, version: id } });

  return (
    <div className="max-w-2xl space-y-5">
      <div>
        <h2 className="text-sm font-medium">{intl.formatMessage({ id: "web.routes.tabs.settings.version.game_version", defaultMessage: "Game version", description: "Version: heading of the game version section, where the version and loader are picked" })}</h2>
        <p className="pt-1 text-xs text-muted">{intl.formatMessage({ id: "web.routes.tabs.settings.version.pick_a_version_loader_from_the_template", defaultMessage: "Pick a version + loader from the template's catalog. Saving restarts the server on the new version.", description: "Version: hint under the game version heading saying the version and loader come from the template catalog and saving restarts the server" })}</p>
      </div>

      <RadioGroup value={selectedId} onChange={pick} aria-label={intl.formatMessage({ id: "web.routes.tabs.settings.version.game_version", defaultMessage: "Game version", description: "Version: heading of the game version section, where the version and loader are picked" })} className="space-y-2">
        {versions.map((v) => (
          <Radio key={v.id} value={v.id}>
            <Radio.Content className="flex w-full items-start gap-3 rounded-lg border border-border px-3 py-3 transition-colors hover:bg-surface/60 data-[selected]:border-primary data-[selected]:bg-primary/5">
              <Radio.Control className="shrink-0 mt-0.5" />
              <div className="min-w-0 flex-1">
                <div className="flex items-center gap-2">
                  <span className="text-sm font-medium">{(moduleText ?? identityText)(moduleIds.versionDisplayName(v.id), v.displayName)}</span>
                  {v.default && (
                    <Chip size="sm" variant="soft" color="accent">{intl.formatMessage({ id: "web.routes.tabs.settings.version.default", defaultMessage: "Default", description: "Badge marking the template's default version" })}</Chip>
                  )}
                </div>
                <p className="pt-0.5 text-[11px] text-muted">
                  {[
                    v.loader
                      ? intl.formatMessage({ id: "web.routes.tabs.settings.version.loader_meta", defaultMessage: "loader {loader}", description: "Version: metadata line for a version entry naming its mod loader; {loader} is the loader name" }, { loader: v.loader })
                      : null,
                    v.gameVersion
                      ? intl.formatMessage({ id: "web.routes.tabs.settings.version.game_meta", defaultMessage: "game {version}", description: "Version: metadata line for a version entry naming its game version; {version} is the game version number" }, { version: v.gameVersion })
                      : null,
                  ]
                    .filter(Boolean)
                    .join(" · ") || v.id}
                </p>
              </div>
            </Radio.Content>
          </Radio>
        ))}
      </RadioGroup>

      {hasLoaderVolumes && (
        <div className="flex items-start gap-2 rounded-lg border border-border bg-surface/40 px-3 py-2.5">
          <Info className="mt-0.5 h-4 w-4 shrink-0 text-primary" />
          <p className="text-xs text-muted">{intl.formatMessage({ id: "web.routes.tabs.settings.version.each_loader_keeps_its_own_mod_volume_a", defaultMessage: "Each loader keeps its own mod volume — a loader's mods are preserved and restored if you switch back to it later.", description: "Version: info note under the version choices saying each loader keeps its own mod volume, so its mods come back if you switch to it again" })}</p>
        </div>
      )}

      {draft.spec.image && (
        <div className="flex items-center gap-2 rounded-lg border border-warning/40 bg-warning/10 px-3 py-2.5">
          <AlertTriangle className="h-4 w-4 shrink-0 text-warning" />
          <p className="flex-1 text-xs text-muted">{intl.formatMessage(
            { id: "web.routes.tabs.settings.version.the_selected_version_s_image_is_ignored", defaultMessage: "Image override active ({image}) — the selected version's image is ignored until cleared.", description: "Version: warning under the version list when a custom image override is set; {image} is the override image reference, shown in monospace" },
            { image: <code className="font-mono">{draft.spec.image}</code> },
          )}</p>
          <Button
            variant="outline"
            size="sm"
            onPress={() =>
              onChange({ ...draft, spec: { ...draft.spec, image: undefined } })
            }
          >{intl.formatMessage({ id: "web.routes.tabs.settings.version.clear_override", defaultMessage: "Clear override", description: "Button that removes the custom image override so the version image is used" })}</Button>
        </div>
      )}

      <p className="text-[11px] text-muted">{intl.formatMessage({ id: "web.routes.tabs.settings.version.downgrading_across_game_versions_may_be", defaultMessage: "Downgrading across game versions may be unsupported by the game's world data — take a backup first.", description: "Version: warning under the version section saying a downgrade may not work with the game's world data, so a backup should be taken first" })}</p>
    </div>
  );
}
