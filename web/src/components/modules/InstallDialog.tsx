import { useState } from "react";
import {
  Modal,
  ModalBackdrop,
  ModalContainer,
  ModalDialog,
  ModalHeader,
  ModalHeading,
  ModalBody,
  ModalFooter,
  Button,
  Input,
  Label,
  Description,
  Select,
  ListBox,
  ListBoxItem,
} from "@heroui/react";
import { APIError } from "@/lib/api";
import type { CatalogEntry } from "@/types";
import { useOptionalIntl } from "@/lib/i18n/format";

interface InstallDialogProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  entry: CatalogEntry | null;
  onConfirm: (args: { source: string; version: string; name: string }) => Promise<void> | void;
  busy?: boolean;
}

// InstallDialog asks the user which source + version to install from
// when a CatalogEntry is published by more than one ModuleSource or
// has multiple available versions. The "name" defaults to the module's
// canonical name but can be edited so the same bundle can be installed
// twice under different names (e.g. for staging/prod side-by-side).
export function InstallDialog({ open, onOpenChange, entry, onConfirm, busy }: InstallDialogProps) {
  const intl = useOptionalIntl();
  const [source, setSource] = useState("");
  const [version, setVersion] = useState("");
  const [name, setName] = useState("");
  const [error, setError] = useState<string | null>(null);

  // When the entry changes (or the dialog re-opens), reset to its
  // defaults: first source, latest version, canonical name. Adjusted
  // directly during render (not in an effect), gated on the
  // previously-seen (open, entry) pair.
  const [resetFor, setResetFor] = useState<{ open: boolean; entry: CatalogEntry | null }>({
    open: false,
    entry,
  });
  if (open !== resetFor.open || entry !== resetFor.entry) {
    setResetFor({ open, entry });
    if (open && entry) {
      setError(null);
      setSource(entry.sources[0]?.name ?? "");
      setVersion(entry.latestVersion ?? entry.versions?.[0] ?? "");
      setName(entry.name);
    }
  }

  if (!entry) return null;

  const versions = entry.versions ?? (entry.latestVersion ? [entry.latestVersion] : []);

  async function submit() {
    if (!source || !version || !name) {
      setError(intl.formatMessage({ id: "web.components.modules.install_dialog.fields_required", defaultMessage: "source, version, and name are all required", description: "Install dialog: validation error when the source, version or name is missing" }));
      return;
    }
    setError(null);
    try {
      await onConfirm({ source, version, name });
    } catch (err) {
      setError(err instanceof APIError ? err.body || err.message : (err as Error).message);
    }
  }

  return (
    <Modal isOpen={open} onOpenChange={onOpenChange}>
      <ModalBackdrop isDismissable={!busy} isKeyboardDismissDisabled={busy}>
        <ModalContainer>
          <ModalDialog>
          <ModalHeader>
            <ModalHeading className="text-base font-semibold">{intl.formatMessage({ id: "web.components.modules.install_dialog.title", defaultMessage: "Install {name}", description: "Install dialog: dialog title; {name} is the module display name" }, { name: entry.displayName ?? entry.name })}
            </ModalHeading>
            <Description className="text-sm text-muted">{intl.formatMessage({ id: "web.components.modules.install_dialog.pulls_the_module_bundle_and_creates_a", defaultMessage: "Pulls the module bundle and creates a Module resource. The cluster operator materializes the GameTemplate in the background.", description: "Install dialog: explanation under the title saying the install pulls the module bundle and creates a Module resource; Module and GameTemplate are Kubernetes resource names, usually left untranslated" })}</Description>
          </ModalHeader>

          <ModalBody className="gap-4">

            <div className="space-y-4">
              {/* Source */}
              {entry.sources.length > 1 ? (
                <div className="space-y-1">
                  <Select
                    selectedKey={source}
                    onSelectionChange={(v) => setSource(String(v))}
                  >
                    <Label className="text-xs">{intl.formatMessage({ id: "web.components.modules.install_dialog.source", defaultMessage: "Source", description: "Install dialog: label above the source field, which is a dropdown or a fixed read-only box depending on the available sources" })}</Label>
                    <Select.Trigger className="w-full rounded border border-border bg-surface px-3 py-2 text-sm hover:bg-surface/80">
                      <Select.Value />
                      <Select.Indicator className="ml-auto h-4 w-4" />
                    </Select.Trigger>
                    <Select.Popover className="rounded border border-border">
                      <ListBox className="p-0" aria-label={intl.formatMessage({ id: "web.components.modules.install_dialog.source", defaultMessage: "Source", description: "Install dialog: label above the source field, which is a dropdown or a fixed read-only box depending on the available sources" })}>
                        {entry.sources.map((s) => (
                          <ListBoxItem key={s.name} id={s.name}>
                            {intl.formatMessage({ id: "web.components.modules.install_dialog.source_label", defaultMessage: "{name} ({type})", description: "Install dialog: module source name and type; {name} is the source name, {type} is the source type" }, { name: s.name, type: s.type })}
                          </ListBoxItem>
                        ))}
                      </ListBox>
                    </Select.Popover>
                  </Select>
                </div>
              ) : (
                <div className="space-y-1">
                  <Label className="text-xs">{intl.formatMessage({ id: "web.components.modules.install_dialog.source", defaultMessage: "Source", description: "Install dialog: label above the source field, which is a dropdown or a fixed read-only box depending on the available sources" })}</Label>
                  <div className="flex h-10 w-full items-center rounded-md border border-border bg-surface/40 px-3 font-mono text-sm text-fg">
                    {entry.sources[0]
                      ? intl.formatMessage({ id: "web.components.modules.install_dialog.source_label", defaultMessage: "{name} ({type})", description: "Install dialog: module source name and type; {name} is the source name, {type} is the source type" }, { name: entry.sources[0].name, type: entry.sources[0].type })
                      : intl.formatMessage({ id: "web.common.empty_value", defaultMessage: "—", description: "Shown in place of a value that is not set" })}
                  </div>
                </div>
              )}

              {/* Version */}
              {versions.length > 1 ? (
                <div className="space-y-1">
                  <Select
                    selectedKey={version}
                    onSelectionChange={(v) => setVersion(String(v))}
                  >
                    <Label className="text-xs">{intl.formatMessage({ id: "web.components.modules.install_dialog.version", defaultMessage: "Version", description: "Install dialog: label above the version field, a dropdown of module versions or a fixed read-only box depending on what is available" })}</Label>
                    <Select.Trigger className="w-full rounded border border-border bg-surface px-3 py-2 text-sm hover:bg-surface/80">
                      <Select.Value />
                      <Select.Indicator className="ml-auto h-4 w-4" />
                    </Select.Trigger>
                    <Select.Popover className="rounded border border-border">
                      <ListBox className="p-0" aria-label={intl.formatMessage({ id: "web.components.modules.install_dialog.version", defaultMessage: "Version", description: "Install dialog: label above the version field, a dropdown of module versions or a fixed read-only box depending on what is available" })}>
                        {versions.map((v) => (
                          <ListBoxItem key={v} id={v}>
                            {v}
                          </ListBoxItem>
                        ))}
                      </ListBox>
                    </Select.Popover>
                  </Select>
                </div>
              ) : (
                <div className="space-y-1">
                  <Label className="text-xs">{intl.formatMessage({ id: "web.components.modules.install_dialog.version", defaultMessage: "Version", description: "Install dialog: label above the version field, a dropdown of module versions or a fixed read-only box depending on what is available" })}</Label>
                  <div className="flex h-10 w-full items-center rounded-md border border-border bg-surface/40 px-3 font-mono text-sm text-fg">
                    {versions[0] ?? intl.formatMessage({ id: "web.common.empty_value", defaultMessage: "—", description: "Shown in place of a value that is not set" })}
                  </div>
                </div>
              )}

              {/* Install as */}
              <div className="space-y-1">
                <Label htmlFor="install-name" className="text-xs">{intl.formatMessage({ id: "web.components.modules.install_dialog.install_as", defaultMessage: "Install as", description: "Install dialog: label above the name field that sets the name the installed module will get" })}</Label>
                <Input
                  id="install-name"
                  value={name}
                  onChange={(e) => setName(e.target.value.toLowerCase())}
                  placeholder={entry.name}
                  className="mt-0"
                />
                <Description className="text-xs text-muted">{intl.formatMessage({ id: "web.components.modules.install_dialog.the_module_gametemplate_name_must_be_a", defaultMessage: "The Module + GameTemplate name. Must be a DNS label.", description: "Install dialog: hint under the install name field saying the name is shared by the Module and GameTemplate and must be a DNS label; these resource names are usually left untranslated" })}</Description>
              </div>
            </div>

            {error && (
              <div
                role="alert"
                className="rounded border border-danger/40 bg-danger/10 px-3 py-2 text-xs text-danger"
              >
                {error}
              </div>
            )}
          </ModalBody>

          <ModalFooter className="flex items-center justify-end gap-2">
            <Button
              variant="ghost"
              size="sm"
              onPress={() => onOpenChange(false)}
              isDisabled={busy}
            >{intl.formatMessage({ id: "web.components.modules.install_dialog.cancel", defaultMessage: "Cancel", description: "Install dialog: button that closes the install dialog without installing; keep it short" })}</Button>
            <Button
              size="sm"
              variant="primary"
              isDisabled={busy}
              onPress={submit}
            >{intl.formatMessage({ id: "web.components.modules.install_dialog.install", defaultMessage: "Install", description: "Install dialog: the word Install used as the dialog title, followed by the module name, and as the confirm button; keep it short" })}</Button>
          </ModalFooter>
          </ModalDialog>
        </ModalContainer>
      </ModalBackdrop>
    </Modal>
  );
}
