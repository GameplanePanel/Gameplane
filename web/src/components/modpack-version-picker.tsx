import { useEffect, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import {
  Button,
  Chip,
  Modal,
  ModalBackdrop,
  ModalBody,
  ModalContainer,
  ModalDialog,
  ModalFooter,
  ModalHeader,
  ModalHeading,
  Radio,
  RadioGroup,
} from "@heroui/react";
import type { RegistryVersion } from "@/types";
import { useResourceClient, useResourceTarget, resourceKey } from "@/lib/resourceTarget";
import { errorTextIntl } from "@/lib/errors";
import { useFormat, useOptionalIntl } from "@/lib/i18n/format";
import { providerLabel } from "@/components/registry-browser";

// isOlderThan reports whether candidateId sits further down the newest-first
// list than pinnedId. Missing ids (or no pin) mean "not older".
export function isOlderThan(versions: RegistryVersion[], candidateId: string, pinnedId?: string): boolean {
  if (!pinnedId) return false;
  const candidate = versions.findIndex((v) => v.id === candidateId);
  const pinned = versions.findIndex((v) => v.id === pinnedId);
  if (candidate < 0 || pinned < 0) return false;
  return candidate > pinned;
}

// versionDetailLine is the second line of a version row: the publish date
// (when known) followed by the loader and game version the row matches.
function versionDetailLine(v: RegistryVersion, fmt: ReturnType<typeof useFormat>, intl: ReturnType<typeof useOptionalIntl>): string {
  const details = [(v.loaders ?? []).join(", "), v.gameVersions?.[0] ?? ""].filter(Boolean).join(" ");
  const published = v.publishedAt ? new Date(v.publishedAt) : null;
  if (!published || Number.isNaN(published.getTime())) return details;
  const date = fmt.date(published, { day: "numeric", month: "short", year: "numeric" });
  if (!details) {
    return intl.formatMessage({ id: "web.components.modpack_version_picker.published", defaultMessage: "Published {date}", description: "Modpack version picker: version row line giving the publish date; {date} is a formatted date" }, { date });
  }
  return intl.formatMessage({ id: "web.components.modpack_version_picker.published_with_details", defaultMessage: "Published {date} · {details}", description: "Modpack version picker: version row line giving the publish date and the loader and game version; {date} is a formatted date, {details} the loader and game version" }, { date, details });
}

// releaseChip marks the release channel: Release, Beta or Alpha. Versions
// without a reported channel get no chip.
function releaseChip(v: RegistryVersion, intl: ReturnType<typeof useOptionalIntl>) {
  switch (v.releaseType) {
    case "release":
      return <Chip size="sm" variant="soft" color="success">{intl.formatMessage({ id: "web.components.modpack_version_picker.release_type_release", defaultMessage: "Release", description: "Modpack version picker: chip for a stable release of the modpack version" })}</Chip>;
    case "beta":
      return <Chip size="sm" variant="soft" color="warning">{intl.formatMessage({ id: "web.components.modpack_version_picker.release_type_beta", defaultMessage: "Beta", description: "Modpack version picker: chip for a beta release of the modpack version" })}</Chip>;
    case "alpha":
      return <Chip size="sm" variant="soft" color="danger">{intl.formatMessage({ id: "web.components.modpack_version_picker.release_type_alpha", defaultMessage: "Alpha", description: "Modpack version picker: chip for an alpha release of the modpack version" })}</Chip>;
    default:
      return null;
  }
}

// ModpackVersionPicker lists the versions of one modpack (newest first) and
// hands the chosen one back to the caller. The caller decides what happens
// next (usually the confirmation dialog).
export function ModpackVersionPicker({
  open,
  onOpenChange,
  name,
  ns,
  provider,
  project,
  title,
  mcLabel,
  pinnedVersionId,
  onContinue,
  confirmLabel,
}: {
  open: boolean;
  onOpenChange(o: boolean): void;
  name: string;
  ns?: string;
  provider: string;
  project: string;
  title: string;
  mcLabel: string;
  pinnedVersionId?: string;
  onContinue(v: RegistryVersion, isOlder: boolean): void;
  confirmLabel?: string;
}) {
  const intl = useOptionalIntl();
  const fmt = useFormat();
  const target = useResourceTarget({ name, namespace: ns });
  const client = useResourceClient(target);
  // undefined means "the newest version", so the selection resets on each open.
  const [selectedId, setSelectedId] = useState<string | undefined>(undefined);

  useEffect(() => {
    if (open) setSelectedId(undefined);
  }, [open]);

  const versionsQ = useQuery({
    queryKey: resourceKey(target, "modpack-versions", provider, project),
    queryFn: ({ signal }) => client.withSignal(signal).Servers.modpackVersions(name, project, provider, ns),
    enabled: open,
  });

  const versions = versionsQ.data ?? [];
  const selected = versions.find((v) => v.id === selectedId) ?? versions[0];
  const selectedOlder = selected ? isOlderThan(versions, selected.id, pinnedVersionId) : false;
  const dialogTitle = confirmLabel
    ? intl.formatMessage({ id: "web.components.modpack_version_picker.change_version", defaultMessage: "Change version", description: "Modpack version picker: dialog title when changing the version of the modpack already on the server" })
    : intl.formatMessage({ id: "web.components.modpack_version_picker.choose_a_version", defaultMessage: "Choose a version", description: "Modpack version picker: dialog title when choosing a version of a modpack to install" });

  return (
    <Modal isOpen={open} onOpenChange={onOpenChange}>
      <ModalBackdrop>
        <ModalContainer>
          <ModalDialog className="w-[520px] max-w-full">
            <ModalHeader>
              <ModalHeading>{dialogTitle}</ModalHeading>
              <p className="text-xs text-muted">
                {intl.formatMessage({ id: "web.components.modpack_version_picker.title_provider", defaultMessage: "{title} · {provider}", description: "Modpack version picker: subtitle with the modpack title and its registry name; {title} is the modpack title, {provider} the registry label" }, { title, provider: providerLabel(provider) })}
              </p>
            </ModalHeader>
            <ModalBody className="gap-3">
              <p className="text-sm text-muted">
                {intl.formatMessage({ id: "web.components.modpack_version_picker.showing_versions", defaultMessage: "Showing versions for {mcLabel}, newest first.", description: "Modpack version picker: explains which Minecraft or game version and loader the listed versions match; {mcLabel} is that label" }, { mcLabel })}
              </p>
              {versionsQ.isPending && (
                <p className="text-sm text-muted">
                  {intl.formatMessage({ id: "web.components.modpack_version_picker.loading_versions", defaultMessage: "Loading versions…", description: "Modpack version picker: shown while the versions of the modpack are loading" })}
                </p>
              )}
              {versionsQ.isError && (
                <p role="alert" className="text-sm text-danger">
                  {errorTextIntl(intl, versionsQ.error, intl.formatMessage({ id: "web.components.modpack_version_picker.load_failed", defaultMessage: "Couldn't load versions.", description: "Modpack version picker: fallback error when the versions of the modpack cannot be loaded" }))}
                </p>
              )}
              {versionsQ.isSuccess && versions.length === 0 && (
                <p className="text-sm text-muted">
                  {intl.formatMessage({ id: "web.components.modpack_version_picker.no_versions", defaultMessage: "No versions of this pack match {mcLabel}.", description: "Modpack version picker: empty state when the modpack has no version for the server's game version and loader; {mcLabel} is that label" }, { mcLabel })}
                </p>
              )}
              {versions.length > 0 && (
                <RadioGroup
                  aria-label={intl.formatMessage({ id: "web.components.modpack_version_picker.version_list", defaultMessage: "Version list", description: "Modpack version picker: accessible name of the radio list of versions" })}
                  value={selected?.id ?? null}
                  onChange={(value) => setSelectedId(String(value))}
                  className="grid gap-2"
                >
                  {versions.map((v) => (
                    <Radio key={v.id} value={v.id}>
                      <Radio.Content className="flex w-full items-center gap-3 rounded-lg border border-border px-3 py-2 data-[selected]:border-primary data-[selected]:bg-primary/5">
                        <Radio.Control className="shrink-0" />
                        <span className="min-w-0 flex-1">
                          <span className="block truncate text-sm font-medium">{v.name || v.versionNumber || v.id}</span>
                          <span className="block truncate text-xs text-muted">
                            {versionDetailLine(v, fmt, intl)}
                          </span>
                        </span>
                        {v.id === pinnedVersionId && (
                          <Chip size="sm" variant="tertiary">
                            {intl.formatMessage({ id: "web.components.modpack_version_picker.pinned", defaultMessage: "Pinned", description: "Modpack version picker: chip marking the version currently pinned on the server" })}
                          </Chip>
                        )}
                        {isOlderThan(versions, v.id, pinnedVersionId) && (
                          <Chip size="sm" variant="tertiary">
                            {intl.formatMessage({ id: "web.components.modpack_version_picker.older", defaultMessage: "Older", description: "Modpack version picker: chip marking a version older than the one pinned on the server" })}
                          </Chip>
                        )}
                        {releaseChip(v, intl)}
                      </Radio.Content>
                    </Radio>
                  ))}
                </RadioGroup>
              )}
            </ModalBody>
            <ModalFooter>
              <Button variant="outline" onPress={() => onOpenChange(false)}>
                {intl.formatMessage({ id: "web.components.modpack_version_picker.cancel", defaultMessage: "Cancel", description: "Modpack version picker: button that closes the picker without choosing a version" })}
              </Button>
              <Button
                isDisabled={!selected}
                onPress={() => {
                  if (selected) onContinue(selected, selectedOlder);
                }}
              >
                {confirmLabel ?? intl.formatMessage({ id: "web.components.modpack_version_picker.continue", defaultMessage: "Continue", description: "Modpack version picker: default primary button that continues with the chosen version" })}
              </Button>
            </ModalFooter>
          </ModalDialog>
        </ModalContainer>
      </ModalBackdrop>
    </Modal>
  );
}
