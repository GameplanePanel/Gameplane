import { useRef, useState } from "react";
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
  Select,
  Label,
  Description,
  ListBox,
  ListBoxItem,
} from "@heroui/react";
import { Upload, Loader2 } from "lucide-react";
import { APIError } from "@/lib/api";
import { ModuleSources, type UploadedModule } from "@/lib/endpoints";
import { useOptionalIntl } from "@/lib/i18n/format";

interface UploadModuleDialogProps {
  cluster?: string;
  open: boolean;
  onOpenChange: (open: boolean) => void;
  // Names of the upload-type ModuleSources that can receive bundles.
  sources: string[];
  onUploaded: () => Promise<void> | void;
}

// UploadModuleDialog lets an admin add a module by uploading a bundle
// archive (.tar.gz/.zip with module.yaml + template.yaml). The file is
// validated server-side via a dry run first, so the user sees the
// parsed metadata before committing.
export function UploadModuleDialog({ open, onOpenChange, sources, onUploaded, cluster = "local" }: UploadModuleDialogProps) {
  const intl = useOptionalIntl();
  const fileInput = useRef<HTMLInputElement>(null);
  const [source, setSource] = useState("");
  const [file, setFile] = useState<File | null>(null);
  const [preview, setPreview] = useState<UploadedModule | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  // Reset the form whenever the dialog (re)opens or the available sources
  // list changes. Adjusted directly during render (not in an effect),
  // gated on the previously-seen (open, sources) pair.
  const [resetFor, setResetFor] = useState<{ open: boolean; sources: string[] }>({
    open: false,
    sources,
  });
  if (open !== resetFor.open || sources !== resetFor.sources) {
    setResetFor({ open, sources });
    if (open) {
      setSource(sources[0] ?? "");
      setFile(null);
      setPreview(null);
      setError(null);
    }
  }

  async function pick(f: File | null) {
    setFile(f);
    setPreview(null);
    setError(null);
    if (!f || !source) return;
    setBusy(true);
    try {
      setPreview(await ModuleSources.upload(source, f, { dryRun: true }, cluster));
    } catch (err) {
      setError(err instanceof APIError ? err.body || err.message : (err as Error).message);
    } finally {
      setBusy(false);
    }
  }

  async function submit() {
    if (!file || !source) {
      setError(intl.formatMessage({ id: "web.components.modules.upload_module_dialog.pick_bundle_first", defaultMessage: "pick a bundle file first", description: "Upload module dialog: validation error when no bundle file has been chosen" }));
      return;
    }
    setBusy(true);
    setError(null);
    try {
      await ModuleSources.upload(source, file, undefined, cluster);
      await onUploaded();
      onOpenChange(false);
    } catch (err) {
      setError(err instanceof APIError ? err.body || err.message : (err as Error).message);
    } finally {
      setBusy(false);
    }
  }

  return (
    <Modal isOpen={open} onOpenChange={onOpenChange}>
      <ModalBackdrop isDismissable={!busy} isKeyboardDismissDisabled={busy}>
        <ModalContainer>
          <ModalDialog>
          <ModalHeader>
            <ModalHeading>{intl.formatMessage({ id: "web.components.modules.upload_module_dialog.upload_module", defaultMessage: "Upload module", description: "Upload module dialog: title of the dialog for uploading a module bundle" })}</ModalHeading>
            <Description className="text-sm text-muted">{intl.formatMessage({ id: "web.components.modules.upload_module_dialog.a_tar_gz_or_zip_holding_one_module", defaultMessage: "A .tar.gz or .zip holding one module directory (module.yaml + template.yaml). Stored in the cluster; the catalog indexes it immediately.", description: "Upload module dialog: explanation under the title; the file is a .tar.gz or .zip holding one module directory, stored and indexed right away" })}</Description>
          </ModalHeader>

          <ModalBody className="gap-4">

            {sources.length > 1 && (
              <div className="space-y-1">
                <Select
                  selectedKey={source}
                  onSelectionChange={(v) => setSource(String(v))}
                >
                  <Label className="text-xs">{intl.formatMessage({ id: "web.components.modules.upload_module_dialog.upload_to", defaultMessage: "Upload to", description: "Upload module dialog: label above the dropdown that picks the cluster source the module is uploaded to" })}</Label>
                  <Select.Trigger className="w-full rounded border border-border bg-surface px-3 py-2 text-sm hover:bg-surface/80">
                    <Select.Value />
                    <Select.Indicator className="ml-auto h-4 w-4" />
                  </Select.Trigger>
                  <Select.Popover className="rounded border border-border">
                    <ListBox className="p-0" aria-label={intl.formatMessage({ id: "web.components.modules.upload_module_dialog.upload_to", defaultMessage: "Upload to", description: "Upload module dialog: label above the dropdown that picks the cluster source the module is uploaded to" })} items={sources.map(src => ({ id: src, name: src }))}>
                      {sources.map((src) => (
                        <ListBoxItem key={src} id={src}>
                          {src}
                        </ListBoxItem>
                      ))}
                    </ListBox>
                  </Select.Popover>
                </Select>
              </div>
            )}

            <input
              ref={fileInput}
              type="file"
              accept=".tar.gz,.tgz,.zip,application/gzip,application/zip"
              className="hidden"
              data-testid="bundle-file-input"
              onChange={(e) => void pick(e.target.files?.[0] ?? null)}
            />
            <Button
              variant="ghost"
              className="w-full flex-col gap-2 rounded-lg border border-dashed border-border bg-card/40 px-4 py-6 text-sm text-muted hover:border-primary hover:text-fg"
              onPress={() => fileInput.current?.click()}
            >
              <Upload className="h-4 w-4" />
              {file ? file.name : intl.formatMessage({ id: "web.components.modules.upload_module_dialog.choose_archive", defaultMessage: "Choose a bundle archive…", description: "Upload module dialog: file picker label shown before a file is chosen" })}
            </Button>

            {preview && (
              <div className="rounded border border-border bg-card/40 px-3 py-2 text-xs">
                <div className="font-medium text-fg">
                  {preview.module.displayName} <span className="font-mono text-muted">{intl.formatMessage({ id: "web.components.modules.upload_module_dialog.vversion", defaultMessage: "v{version}", description: "Upload module dialog: sentence with version in span" }, { version: preview.module.version })}</span>
                </div>
                <div className="pt-0.5 text-muted">{intl.formatMessage({ id: "web.components.modules.upload_module_dialog.game", defaultMessage: "game:", description: "Upload module dialog: label before the game identifier in the preview card of the chosen bundle; the value after it is the game name" })} <span className="font-mono">{preview.module.game}</span>
                  {preview.module.summary && intl.formatMessage({ id: "web.components.modules.upload_module_dialog.summary_line", defaultMessage: " — {summary}", description: "Upload module dialog: module summary after an em dash; {summary} is the module summary" }, { summary: preview.module.summary })}
                </div>
              </div>
            )}

            {error && (
              <div className="rounded border border-danger/40 bg-danger/10 px-3 py-2 text-xs text-danger" role="alert">
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
            >{intl.formatMessage({ id: "web.components.modules.upload_module_dialog.cancel", defaultMessage: "Cancel", description: "Upload module dialog: button that closes the dialog without uploading; keep it short" })}</Button>
            <Button
              size="sm"
              variant="primary"
              isDisabled={busy || !preview}
              onPress={submit}
            >
              {busy ? <Loader2 className="h-4 w-4 animate-spin" /> : null}{intl.formatMessage({ id: "web.components.modules.upload_module_dialog.upload", defaultMessage: "Upload", description: "Upload module dialog: button that uploads the chosen bundle; keep it short" })}</Button>
          </ModalFooter>
          </ModalDialog>
        </ModalContainer>
      </ModalBackdrop>
    </Modal>
  );
}
