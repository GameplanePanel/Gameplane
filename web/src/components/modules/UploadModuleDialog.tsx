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
      setError("pick a bundle file first");
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
            <ModalHeading>Upload module</ModalHeading>
            <Description className="text-sm text-muted">
              A .tar.gz or .zip holding one module directory (module.yaml +
              template.yaml). Stored in the cluster; the catalog indexes it
              immediately.
            </Description>
          </ModalHeader>

          <ModalBody className="gap-4">

            {sources.length > 1 && (
              <div className="space-y-1">
                <Select
                  selectedKey={source}
                  onSelectionChange={(v) => setSource(String(v))}
                >
                  <Label className="text-xs">Upload to</Label>
                  <Select.Trigger className="w-full rounded border border-border bg-surface px-3 py-2 text-sm hover:bg-surface/80">
                    <Select.Value />
                    <Select.Indicator className="ml-auto h-4 w-4" />
                  </Select.Trigger>
                  <Select.Popover className="rounded border border-border">
                    <ListBox className="p-0" aria-label="Upload to" items={sources.map(src => ({ id: src, name: src }))}>
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
              {file ? file.name : "Choose a bundle archive…"}
            </Button>

            {preview && (
              <div className="rounded border border-border bg-card/40 px-3 py-2 text-xs">
                <div className="font-medium text-fg">
                  {preview.module.displayName}{" "}
                  <span className="font-mono text-muted">v{preview.module.version}</span>
                </div>
                <div className="pt-0.5 text-muted">
                  game: <span className="font-mono">{preview.module.game}</span>
                  {preview.module.summary && <> — {preview.module.summary}</>}
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
            >
              Cancel
            </Button>
            <Button
              size="sm"
              variant="primary"
              isDisabled={busy || !preview}
              onPress={submit}
            >
              {busy ? <Loader2 className="h-4 w-4 animate-spin" /> : null}
              Upload
            </Button>
          </ModalFooter>
          </ModalDialog>
        </ModalContainer>
      </ModalBackdrop>
    </Modal>
  );
}
