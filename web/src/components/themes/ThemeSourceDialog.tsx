// Add / edit dialog for a theme source (spec 043, T049, designs 906199366-709
// and -1485). The type cannot change on edit. Pull secret is not offered: the
// API refuses credentials (OD-5). Field validation is minimal and client-side;
// the server's plain-English 422 message is shown inline.
import { useState, type ReactNode } from "react";
import {
  Button,
  Description,
  Input,
  Label,
  ListBox,
  ListBoxItem,
  Modal,
  ModalBackdrop,
  ModalBody,
  ModalContainer,
  ModalDialog,
  ModalFooter,
  ModalHeader,
  ModalHeading,
  Select,
  Switch,
  TextArea,
} from "@heroui/react";
import { Loader2 } from "lucide-react";
import type { MessageDescriptor } from "react-intl";
import { useOptionalIntl } from "@/lib/i18n/format";
import type { ThemeSource, ThemeSourceInput, ThemeSourceType, ThemeSourceVerify } from "@/types";
import { themeSourcesMessages as M } from "./messages";

type VerifyMode = "none" | "key" | "keyless";

interface FormState {
  name: string;
  type: ThemeSourceType;
  ociUrl: string;
  ociThemes: string;
  ociInsecure: boolean;
  verify: VerifyMode;
  publicKey: string;
  issuer: string;
  identity: string;
  allow: string;
  interval: string;
  gitUrl: string;
  gitRef: string;
  gitPath: string;
  httpUrl: string;
  localPath: string;
}

const TYPE_OPTIONS: Array<{ value: ThemeSourceType; label: MessageDescriptor }> = [
  { value: "oci", label: M.typeOci },
  { value: "git", label: M.typeGit },
  { value: "http", label: M.typeHttp },
  { value: "local", label: M.typeLocal },
];

const VERIFY_OPTIONS: Array<{ value: VerifyMode; label: MessageDescriptor }> = [
  { value: "none", label: M.verifyNone },
  { value: "key", label: M.verifyKey },
  { value: "keyless", label: M.verifyKeyless },
];

// The API stores refreshInterval in seconds (minimum 300).
const MIN_INTERVAL_SECONDS = 300;

export function intervalToText(seconds: number): string {
  return seconds % 3600 === 0 ? `${seconds / 3600}h` : `${seconds / 60}m`;
}

export function parseInterval(text: string): number | null {
  const m = /^(\d+)([mh])$/.exec(text.trim());
  if (!m) return null;
  const seconds = Number(m[1]) * (m[2] === "h" ? 3600 : 60);
  return seconds >= MIN_INTERVAL_SECONDS ? seconds : null;
}

export function splitList(text: string): string[] {
  return text.split(",").map((s) => s.trim()).filter((s) => s !== "");
}

function initialForm(source: ThemeSource | null): FormState {
  const verify = source?.verify;
  const mode: VerifyMode = verify?.keyless ? "keyless" : verify?.key ? "key" : "none";
  return {
    name: source?.name ?? "",
    type: source?.type ?? "oci",
    ociUrl: source?.spec.oci?.url ?? "",
    ociThemes: (source?.spec.oci?.themes ?? []).join(", "),
    ociInsecure: source?.spec.oci?.insecure ?? false,
    verify: mode,
    publicKey: verify?.key ?? "",
    issuer: verify?.keyless?.issuer ?? "",
    identity: verify?.keyless?.identity ?? "",
    allow: (source?.spec.allow ?? []).join(", "),
    interval: intervalToText(source?.refreshInterval ?? 3600),
    gitUrl: source?.spec.git?.url ?? "",
    gitRef: source?.spec.git?.ref ?? "",
    gitPath: source?.spec.git?.path ?? "",
    httpUrl: source?.spec.http?.url ?? "",
    localPath: source?.spec.local?.path ?? "",
  };
}

// requiredFilled reports whether the fields the chosen type needs are present.
function requiredFilled(f: FormState, editing: boolean): boolean {
  if (!editing && f.name.trim() === "") return false;
  if (parseInterval(f.interval) === null) return false;
  switch (f.type) {
    case "oci":
      if (f.ociUrl.trim() === "" || splitList(f.ociThemes).length === 0) return false;
      if (f.verify === "key") return f.publicKey.trim() !== "";
      if (f.verify === "keyless") return f.issuer.trim() !== "" && f.identity.trim() !== "";
      return true;
    case "git":
      return f.gitUrl.trim() !== "";
    case "http":
      return f.httpUrl.trim() !== "";
    case "local":
      return f.localPath.trim() !== "";
  }
}

// buildInput maps the form onto the API body. Callers check requiredFilled first.
export function buildInput(f: FormState, editing: ThemeSource | null): ThemeSourceInput {
  const interval = parseInterval(f.interval) ?? MIN_INTERVAL_SECONDS;
  const allow = splitList(f.allow);
  let verify: ThemeSourceVerify | null = null;
  let spec: ThemeSourceInput["spec"];
  switch (f.type) {
    case "oci":
      if (f.verify === "key") verify = { key: f.publicKey.trim() };
      if (f.verify === "keyless") verify = { keyless: { issuer: f.issuer.trim(), identity: f.identity.trim() } };
      spec = { oci: { url: f.ociUrl.trim(), themes: splitList(f.ociThemes), insecure: f.ociInsecure } };
      break;
    case "git": {
      const ref = f.gitRef.trim();
      const path = f.gitPath.trim();
      spec = { git: { url: f.gitUrl.trim(), ...(ref ? { ref } : {}), ...(path ? { path } : {}) } };
      break;
    }
    case "http":
      spec = { http: { url: f.httpUrl.trim() } };
      break;
    case "local":
      spec = { local: { path: f.localPath.trim() } };
      break;
  }
  return {
    name: editing ? editing.name : f.name.trim(),
    type: f.type,
    spec: allow.length > 0 ? { ...spec, allow } : spec,
    verify,
    refreshInterval: interval,
    enabled: editing ? editing.enabled : true,
  };
}

export interface ThemeSourceDialogProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  /** null adds a source; a source edits it (name and type are fixed). */
  source: ThemeSource | null;
  busy: boolean;
  error: string | null;
  onSubmit: (input: ThemeSourceInput) => void;
}

export function ThemeSourceDialog({ open, onOpenChange, source, busy, error, onSubmit }: ThemeSourceDialogProps) {
  // The dialog body remounts per open (the parent keys it), so the form
  // starts from the current source each time it opens.
  if (!open) return null;
  return <ThemeSourceForm key={source?.name ?? "new"} onOpenChange={onOpenChange} source={source} busy={busy} error={error} onSubmit={onSubmit} />;
}

function ThemeSourceForm({ onOpenChange, source, busy, error, onSubmit }: Omit<ThemeSourceDialogProps, "open">) {
  const intl = useOptionalIntl();
  const editing = source !== null;
  const [f, setF] = useState<FormState>(() => initialForm(source));
  const [touched, setTouched] = useState(false);
  const set = (patch: Partial<FormState>) => setF((cur) => ({ ...cur, ...patch }));
  const filled = requiredFilled(f, editing);
  const label = (d: MessageDescriptor) => intl.formatMessage(d);

  const submit = () => {
    setTouched(true);
    if (!filled || busy) return;
    onSubmit(buildInput(f, source));
  };

  const field = (id: string, text: MessageDescriptor, input: ReactNode, hint?: MessageDescriptor) => (
    <div className="flex flex-col gap-1.5">
      <Label htmlFor={id} className="text-xs leading-4 font-normal text-muted">{label(text)}</Label>
      {input}
      {hint ? <Description className="text-[11px] leading-[14px] text-muted">{label(hint)}</Description> : null}
    </div>
  );

  return (
    <Modal isOpen onOpenChange={onOpenChange}>
      <ModalBackdrop isDismissable={!busy} isKeyboardDismissDisabled={busy}>
        <ModalContainer>
          <ModalDialog className="w-[480px] max-w-[480px]">
            <ModalHeader>
              <ModalHeading>{label(editing ? M.editTitle : M.addTitle)}</ModalHeading>
              <p className="text-xs text-muted">{label(editing ? M.editDescription : M.addDescription)}</p>
            </ModalHeader>
            <ModalBody className="gap-4 max-h-[80vh] overflow-y-auto">
              {!editing && field("ts-name", M.fieldName, <Input id="ts-name" value={f.name} onChange={(e) => set({ name: e.target.value.toLowerCase() })} placeholder="community" />, M.hintName)}

              <div className="flex flex-col gap-1.5">
                <Select
                  className="flex flex-col gap-1.5"
                  value={f.type}
                  isDisabled={editing}
                  onChange={(v) => set({ type: v as ThemeSourceType })}
                  aria-label={label(M.fieldType)}
                >
                  <Label className="text-xs leading-4 font-normal text-muted">{label(M.fieldType)}</Label>
                  <Select.Trigger className="w-full items-center rounded border border-border bg-surface px-3 py-2 text-sm hover:bg-surface/80">
                    <Select.Value className="text-[var(--field-placeholder)]" />
                    <Select.Indicator className="ml-auto h-4 w-4" />
                  </Select.Trigger>
                  <Select.Popover className="rounded border border-border">
                    <ListBox aria-label={label(M.fieldType)}>
                      {TYPE_OPTIONS.map((opt) => (
                        <ListBoxItem key={opt.value} id={opt.value}>{label(opt.label)}</ListBoxItem>
                      ))}
                    </ListBox>
                  </Select.Popover>
                </Select>
              </div>

              {f.type === "oci" && (
                <>
                  {field("ts-oci-url", M.fieldRegistryUrl, <Input id="ts-oci-url" value={f.ociUrl} onChange={(e) => set({ ociUrl: e.target.value })} placeholder="ghcr.io/gameplane/themes" />)}
                  {field("ts-oci-themes", M.fieldThemes, <Input id="ts-oci-themes" value={f.ociThemes} onChange={(e) => set({ ociThemes: e.target.value })} placeholder="aurora-night, paper-light" />, M.hintThemes)}
                  <Switch isSelected={f.ociInsecure} onChange={(v) => set({ ociInsecure: v })} aria-label={label(M.fieldInsecure)}>
                    <Switch.Content>
                      <span className="text-xs text-muted">{label(M.fieldInsecure)}</span>
                      <Switch.Control>
                        <Switch.Thumb />
                      </Switch.Control>
                    </Switch.Content>
                  </Switch>
                  <div className="flex flex-col gap-1.5">
                    <Select className="flex flex-col gap-1.5" value={f.verify} onChange={(v) => set({ verify: v as VerifyMode })} aria-label={label(M.fieldVerify)}>
                      <Label className="text-xs leading-4 font-normal text-muted">{label(M.fieldVerify)}</Label>
                      <Select.Trigger className="w-full items-center rounded border border-border bg-surface px-3 py-2 text-sm hover:bg-surface/80">
                        <Select.Value className="text-[var(--field-placeholder)]" />
                        <Select.Indicator className="ml-auto h-4 w-4" />
                      </Select.Trigger>
                      <Select.Popover className="rounded border border-border">
                        <ListBox aria-label={label(M.fieldVerify)}>
                          {VERIFY_OPTIONS.map((opt) => (
                            <ListBoxItem key={opt.value} id={opt.value}>{label(opt.label)}</ListBoxItem>
                          ))}
                        </ListBox>
                      </Select.Popover>
                    </Select>
                    <Description className="text-[11px] leading-[14px] text-muted">{label(M.hintVerify)}</Description>
                  </div>
                  {f.verify === "key" && field("ts-public-key", M.fieldPublicKey, <TextArea id="ts-public-key" value={f.publicKey} onChange={(e) => set({ publicKey: e.target.value })} rows={4} />, M.hintPublicKey)}
                  {f.verify === "keyless" && (
                    <>
                      {field("ts-issuer", M.fieldIssuer, <Input id="ts-issuer" value={f.issuer} onChange={(e) => set({ issuer: e.target.value })} placeholder="https://token.actions.githubusercontent.com" />, M.hintIssuer)}
                      {field("ts-identity", M.fieldIdentity, <Input id="ts-identity" value={f.identity} onChange={(e) => set({ identity: e.target.value })} placeholder="github.com/gameplane/themes" />, M.hintIdentity)}
                    </>
                  )}
                </>
              )}

              {f.type === "git" && (
                <>
                  {field("ts-git-url", M.fieldGitUrl, <Input id="ts-git-url" value={f.gitUrl} onChange={(e) => set({ gitUrl: e.target.value })} placeholder="github.com/community/gp-themes" />)}
                  {field("ts-git-ref", M.fieldRef, <Input id="ts-git-ref" value={f.gitRef} onChange={(e) => set({ gitRef: e.target.value })} placeholder="main" />)}
                  {field("ts-git-path", M.fieldPath, <Input id="ts-git-path" value={f.gitPath} onChange={(e) => set({ gitPath: e.target.value })} placeholder="themes" />)}
                </>
              )}

              {f.type === "http" && field("ts-http-url", M.fieldHttpUrl, <Input id="ts-http-url" value={f.httpUrl} onChange={(e) => set({ httpUrl: e.target.value })} placeholder="themes.example.org/index.json" />)}

              {f.type === "local" && field("ts-local-path", M.fieldLocalPath, <Input id="ts-local-path" value={f.localPath} onChange={(e) => set({ localPath: e.target.value })} placeholder="/srv/gameplane/themes" />)}

              {field("ts-allow", M.fieldAllow, <Input id="ts-allow" value={f.allow} onChange={(e) => set({ allow: e.target.value })} placeholder="aurora-*" />, M.hintAllow)}
              {field("ts-interval", M.fieldInterval, <Input id="ts-interval" value={f.interval} onChange={(e) => set({ interval: e.target.value })} placeholder="1h" />, touched && parseInterval(f.interval) === null ? M.intervalInvalid : M.hintInterval)}

              {touched && !filled && <p role="alert" className="text-xs text-danger">{label(M.fieldRequired)}</p>}
              {error && <p role="alert" className="text-xs text-danger">{error}</p>}
            </ModalBody>
            <ModalFooter className="gap-2">
              <Button variant="ghost" size="sm" className="text-xs" onPress={() => onOpenChange(false)} isDisabled={busy}>{label(M.cancel)}</Button>
              <Button variant="primary" size="sm" className="text-xs" isDisabled={busy} onPress={submit}>
                {busy ? <Loader2 className="h-4 w-4 animate-spin" /> : null}
                {label(editing ? M.submitSave : M.submitAdd)}
              </Button>
            </ModalFooter>
          </ModalDialog>
        </ModalContainer>
      </ModalBackdrop>
    </Modal>
  );
}
