import { useState } from "react";
import { defineMessages, type MessageDescriptor } from "react-intl";
import { Loader2 } from "lucide-react";
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
  Checkbox,
} from "@heroui/react";
import { APIError } from "@/lib/api";
import type { ModuleSource, ModuleSourceSpec, ModuleSourceType } from "@/types";
import { useOptionalIntl } from "@/lib/i18n/format";

interface SourceDialogProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  // Existing source to edit; null creates a new one.
  source: ModuleSource | null;
  onConfirm: (args: { name: string; spec: ModuleSourceSpec }) => Promise<void> | void;
  busy?: boolean;
}

const TYPE_LABELS = defineMessages({
  oci: { id: "web.components.modules.source_dialog.type_oci", defaultMessage: "OCI registry", description: "Source dialog: source type option for an OCI registry" },
  git: { id: "web.components.modules.source_dialog.type_git", defaultMessage: "Git repository", description: "Source dialog: source type option for a git repository" },
  http: { id: "web.components.modules.source_dialog.type_http", defaultMessage: "HTTP archive (.tar.gz / .zip)", description: "Source dialog: source type option for an HTTP archive" },
  local: { id: "web.components.modules.source_dialog.type_local", defaultMessage: "Local directory (operator mount)", description: "Source dialog: source type option for a directory mounted into the operator" },
  upload: { id: "web.components.modules.source_dialog.type_upload", defaultMessage: "Uploaded bundles", description: "Source dialog: source type option for bundles uploaded to the dashboard" },
});

const TYPE_OPTIONS: Array<{ value: ModuleSourceType; label: MessageDescriptor }> = [
  { value: "oci", label: TYPE_LABELS.oci },
  { value: "git", label: TYPE_LABELS.git },
  { value: "http", label: TYPE_LABELS.http },
  { value: "local", label: TYPE_LABELS.local },
  { value: "upload", label: TYPE_LABELS.upload },
];

type VerifyMode = "none" | "keyed" | "keyless";

const VERIFY_LABELS = defineMessages({
  none: { id: "web.components.modules.source_dialog.verify_none", defaultMessage: "None", description: "Source dialog: signature verification option that turns verification off" },
  keyed: { id: "web.components.modules.source_dialog.verify_keyed", defaultMessage: "Keyed (cosign public key)", description: "Source dialog: signature verification option that checks a cosign public key" },
  keyless: { id: "web.components.modules.source_dialog.verify_keyless", defaultMessage: "Keyless (Fulcio)", description: "Source dialog: signature verification option that checks a keyless Fulcio signature" },
});

const VERIFY_OPTIONS: Array<{ value: VerifyMode; label: MessageDescriptor }> = [
  { value: "none", label: VERIFY_LABELS.none },
  { value: "keyed", label: VERIFY_LABELS.keyed },
  { value: "keyless", label: VERIFY_LABELS.keyless },
];

// form mirrors the spec union flattened into editable strings.
interface form {
  type: ModuleSourceType;
  url: string;
  modules: string; // comma/space separated (oci)
  secretName: string;
  insecure: boolean;
  ref: string;
  subPath: string;
  path: string;
  allow: string; // comma/space separated
  refreshInterval: string;
  // Cosign signature policy — OCI sources only (CEL-enforced on the CRD).
  verifyMode: VerifyMode;
  verifyKeySecret: string;
  verifyIssuer: string;
  verifyIdentity: string;
}

const emptyForm: form = {
  type: "oci",
  url: "",
  modules: "",
  secretName: "",
  insecure: false,
  ref: "",
  subPath: "",
  path: "",
  allow: "",
  refreshInterval: "",
  verifyMode: "none",
  verifyKeySecret: "",
  verifyIssuer: "",
  verifyIdentity: "",
};

function formFrom(source: ModuleSource): form {
  const spec = source.spec;
  const verify = spec.verify;
  return {
    ...emptyForm,
    type: spec.type ?? "oci",
    url: spec.oci?.url ?? spec.git?.url ?? spec.http?.url ?? "",
    modules: (spec.oci?.modules ?? []).map((m) => m.name).join(", "),
    secretName:
      spec.oci?.pullSecretRef?.name ?? spec.git?.secretRef?.name ?? spec.http?.secretRef?.name ?? "",
    insecure: spec.oci?.insecure ?? spec.http?.insecure ?? false,
    ref: spec.git?.ref ?? "",
    subPath: spec.git?.subPath ?? "",
    path: spec.local?.path ?? "",
    allow: (spec.allow ?? []).join(", "),
    refreshInterval: spec.refreshInterval ?? "",
    verifyMode: verify?.keyless ? "keyless" : verify?.key ? "keyed" : "none",
    verifyKeySecret: verify?.key?.name ?? "",
    verifyIssuer: verify?.keyless?.issuer ?? "",
    verifyIdentity: verify?.keyless?.identity ?? "",
  };
}

function splitList(raw: string): string[] {
  return raw
    .split(/[\s,]+/)
    .map((s) => s.trim())
    .filter(Boolean);
}

// specFrom renders the form back into the discriminated union the API
// expects — only the nested config matching the type is sent.
export function specFrom(f: form): ModuleSourceSpec {
  const spec: ModuleSourceSpec = { type: f.type };
  if (f.allow.trim()) spec.allow = splitList(f.allow);
  if (f.refreshInterval.trim()) spec.refreshInterval = f.refreshInterval.trim();
  const secretRef = f.secretName.trim() ? { name: f.secretName.trim() } : undefined;
  switch (f.type) {
    case "oci":
      spec.oci = {
        url: f.url.trim(),
        modules: splitList(f.modules).map((name) => ({ name })),
        ...(f.insecure ? { insecure: true } : {}),
        ...(secretRef ? { pullSecretRef: secretRef } : {}),
      };
      // verify is OCI-only (CEL-enforced); never emit it for other types.
      if (f.verifyMode === "keyed") {
        spec.verify = { key: { name: f.verifyKeySecret.trim() } };
      } else if (f.verifyMode === "keyless") {
        spec.verify = {
          keyless: { issuer: f.verifyIssuer.trim(), identity: f.verifyIdentity.trim() },
        };
      }
      break;
    case "git":
      spec.git = {
        url: f.url.trim(),
        ...(f.ref.trim() ? { ref: f.ref.trim() } : {}),
        ...(f.subPath.trim() ? { subPath: f.subPath.trim() } : {}),
        ...(secretRef ? { secretRef } : {}),
      };
      break;
    case "http":
      spec.http = {
        url: f.url.trim(),
        ...(f.insecure ? { insecure: true } : {}),
        ...(secretRef ? { secretRef } : {}),
      };
      break;
    case "local":
      spec.local = f.path.trim() ? { path: f.path.trim() } : {};
      break;
    case "upload":
      break;
  }
  return spec;
}

// SourceDialog adds or edits a ModuleSource: a type selector plus the
// fields that type needs. Admin-only on the server side.
export function SourceDialog({ open, onOpenChange, source, onConfirm, busy }: SourceDialogProps) {
  const intl = useOptionalIntl();
  const editing = source !== null;
  const [name, setName] = useState("");
  const [f, setF] = useState<form>(emptyForm);
  const [error, setError] = useState<string | null>(null);

  // Reset the form whenever the dialog (re)opens or the source being edited
  // changes. Adjusted directly during render (not in an effect), gated on
  // the previously-seen (open, source) pair.
  const [resetFor, setResetFor] = useState<{ open: boolean; source: ModuleSource | null }>({
    open: false,
    source,
  });
  if (open !== resetFor.open || source !== resetFor.source) {
    setResetFor({ open, source });
    if (open) {
      setError(null);
      setName(source?.metadata.name ?? "");
      setF(source ? formFrom(source) : emptyForm);
    }
  }

  const set = (patch: Partial<form>) => setF((prev) => ({ ...prev, ...patch }));

  // canSubmit mirrors the required-field checks in submit() below (kept in
  // sync manually) so the primary button reflects backend-required fields
  // without duplicating or changing the validation error messages there.
  const canSubmit =
    name.trim().length > 0 &&
    (f.type === "upload" || f.type === "local" || f.url.trim().length > 0) &&
    (f.type !== "oci" || splitList(f.modules).length > 0) &&
    (f.type !== "oci" || f.verifyMode !== "keyed" || f.verifyKeySecret.trim().length > 0) &&
    (f.type !== "oci" ||
      f.verifyMode !== "keyless" ||
      (f.verifyIssuer.trim().length > 0 && f.verifyIdentity.trim().length > 0));

  async function submit() {
    if (!name.trim()) {
      setError(intl.formatMessage({ id: "web.components.modules.source_dialog.name_required", defaultMessage: "name is required", description: "Source dialog: validation error when the source name is empty" }));
      return;
    }
    if (f.type !== "upload" && f.type !== "local" && !f.url.trim()) {
      setError(intl.formatMessage({ id: "web.components.modules.source_dialog.url_required", defaultMessage: "url is required", description: "Source dialog: validation error when the source URL is empty" }));
      return;
    }
    if (f.type === "oci" && splitList(f.modules).length === 0) {
      setError(intl.formatMessage({ id: "web.components.modules.source_dialog.oci_modules_required", defaultMessage: "OCI sources need at least one module name", description: "Source dialog: validation error when an OCI source lists no module names" }));
      return;
    }
    if (f.type === "oci" && f.verifyMode === "keyed" && !f.verifyKeySecret.trim()) {
      setError(intl.formatMessage({ id: "web.components.modules.source_dialog.keyed_secret_required", defaultMessage: "keyed verification needs a public key secret name", description: "Source dialog: validation error when keyed verification has no public key secret name" }));
      return;
    }
    if (
      f.type === "oci" &&
      f.verifyMode === "keyless" &&
      (!f.verifyIssuer.trim() || !f.verifyIdentity.trim())
    ) {
      setError(intl.formatMessage({ id: "web.components.modules.source_dialog.keyless_identity_required", defaultMessage: "keyless verification needs an issuer and identity", description: "Source dialog: validation error when keyless verification has no issuer or identity" }));
      return;
    }
    setError(null);
    try {
      await onConfirm({ name: name.trim(), spec: specFrom(f) });
    } catch (err) {
      setError(err instanceof APIError ? err.body || err.message : (err as Error).message);
    }
  }

  return (
    <Modal isOpen={open} onOpenChange={onOpenChange}>
      <ModalBackdrop isDismissable={!busy} isKeyboardDismissDisabled={busy}>
        <ModalContainer>
          <ModalDialog className="w-[480px] max-w-[480px]">
          <ModalHeader>
            <ModalHeading>
              {editing ? intl.formatMessage({ id: "web.components.modules.source_dialog.edit_source_title", defaultMessage: "Edit source {name}", description: "Source dialog: title when editing; {name} is the source name" }, { name: source.metadata.name }) : intl.formatMessage({ id: "web.components.modules.source_dialog.add_source_title", defaultMessage: "Add module source", description: "Source dialog: title when adding a source" })}
            </ModalHeading>
            <Description className="text-sm leading-[21px] text-muted">{intl.formatMessage({ id: "web.components.modules.source_dialog.where_the_operator_discovers_and_pulls", defaultMessage: "Where the operator discovers and pulls module bundles from.", description: "Source dialog: explanation under the Add or Edit source heading saying where the operator discovers and pulls module bundles from" })}</Description>
          </ModalHeader>

          <ModalBody className="gap-4 max-h-[80vh] overflow-y-auto">

            {!editing && (
              <div className="flex flex-col gap-1.5">
                <Label htmlFor="source-name" className="text-xs leading-4 font-normal text-muted">{intl.formatMessage({ id: "web.components.modules.source_dialog.name", defaultMessage: "Name", description: "Source dialog: label above the source name field; the field is shown only when adding a new source, not when editing" })}</Label>
                <Input
                  id="source-name"
                  value={name}
                  onChange={(e) => setName(e.target.value.toLowerCase())}
                  placeholder={intl.formatMessage({ id: "web.components.modules.source_dialog.community", defaultMessage: "community", description: "Source dialog: example value shown inside the empty source name field; a sample name, not a default value" })}
                />
                <Description className="text-[11px] leading-[14px] text-muted">{intl.formatMessage({ id: "web.components.modules.source_dialog.dns_label_identifying_this_source", defaultMessage: "DNS label identifying this source.", description: "Source dialog: hint under the source name field saying it is a DNS label that identifies the source; DNS label is a technical term, usually left untranslated" })}</Description>
              </div>
            )}

            <div className="flex flex-col gap-1.5">
              <Select
                className="flex flex-col gap-1.5"
                value={f.type}
                onChange={(v) => set({ type: v as ModuleSourceType })}
                aria-label={intl.formatMessage({ id: "web.components.modules.source_dialog.type", defaultMessage: "Type", description: "Source dialog: label above the source type dropdown that picks the kind of source; also the screen-reader name of that dropdown" })}
              >
                <Label className="text-xs leading-4 font-normal text-muted">{intl.formatMessage({ id: "web.components.modules.source_dialog.type", defaultMessage: "Type", description: "Source dialog: label above the source type dropdown that picks the kind of source; also the screen-reader name of that dropdown" })}</Label>
                <Select.Trigger
                  className="w-full items-center rounded border border-border bg-surface px-3 py-2 text-sm hover:bg-surface/80"
                >
                  <Select.Value className="text-[var(--field-placeholder)]" />
                  <Select.Indicator className="ml-auto h-4 w-4" />
                </Select.Trigger>
                <Select.Popover className="rounded border border-border">
                  <ListBox aria-label={intl.formatMessage({ id: "web.components.modules.source_dialog.type_options", defaultMessage: "Type options", description: "Source dialog: screen-reader name of the list of source type choices; not visible on screen" })}>
                    {TYPE_OPTIONS.map((opt) => (
                      <ListBoxItem key={opt.value} id={opt.value}>
                        {intl.formatMessage(opt.label)}
                      </ListBoxItem>
                    ))}
                  </ListBox>
                </Select.Popover>
              </Select>
            </div>

            {f.type === "oci" && (
              <>
                <div className="flex flex-col gap-1.5">
                  <Label htmlFor="oci-url" className="text-xs leading-4 font-normal text-muted">{intl.formatMessage({ id: "web.components.modules.source_dialog.registry_url", defaultMessage: "Registry URL", description: "Source dialog: label above the registry URL field, shown for OCI registry sources; OCI is a technical term, usually left untranslated" })}</Label>
                  <Input
                    id="oci-url"
                    value={f.url}
                    onChange={(e) => set({ url: e.target.value })}
                    placeholder={intl.formatMessage({ id: "web.components.modules.source_dialog.ghcr_io_gameplanepanel_gameplane_modules", defaultMessage: "ghcr.io/gameplanepanel/gameplane-modules", description: "Source dialog: example value shown inside the empty registry URL field; a sample registry address, keep it as written" })}
                  />
                </div>

                <div className="flex flex-col gap-1.5">
                  <Label htmlFor="oci-modules" className="text-xs leading-4 font-normal text-muted">{intl.formatMessage({ id: "web.components.modules.source_dialog.modules", defaultMessage: "Modules", description: "Source dialog: label above the modules field for registry sources, which lists the module names to pull" })}</Label>
                  <Input
                    id="oci-modules"
                    value={f.modules}
                    onChange={(e) => set({ modules: e.target.value })}
                    placeholder={intl.formatMessage({ id: "web.components.modules.source_dialog.minecraft_java_valheim", defaultMessage: "minecraft-java, valheim", description: "Source dialog: example value shown inside the empty modules field; a sample list of module names separated by commas" })}
                  />
                  <Description className="text-[11px] leading-[14px] text-muted">{intl.formatMessage({ id: "web.components.modules.source_dialog.comma_separated_module_names_registries", defaultMessage: "Comma-separated module names (registries can't be enumerated).", description: "Source dialog: hint under the modules field saying the names are comma-separated and that registries cannot be listed in full" })}</Description>
                </div>

                <div className="flex flex-col gap-1.5">
                  <Label htmlFor="oci-secret" className="text-xs leading-4 font-normal text-muted">{intl.formatMessage({ id: "web.components.modules.source_dialog.pull_secret", defaultMessage: "Pull secret", description: "Source dialog: label above the pull secret name field for registry sources; the secret holds registry credentials" })}</Label>
                  <Input
                    id="oci-secret"
                    value={f.secretName}
                    onChange={(e) => set({ secretName: e.target.value })}
                    placeholder={intl.formatMessage({ id: "web.components.modules.source_dialog.registry_creds", defaultMessage: "registry-creds", description: "Source dialog: example value shown inside the empty pull secret field; a sample secret name, not a default value" })}
                  />
                  <Description className="text-[11px] leading-[14px] text-muted">{intl.formatMessage({ id: "web.components.modules.source_dialog.dockerconfigjson_secret_in_the_operator", defaultMessage: "dockerconfigjson Secret in the operator namespace (optional).", description: "Source dialog: hint under the pull secret field saying it is an optional dockerconfigjson Secret in the operator namespace; dockerconfigjson is a technical term, usually left untranslated" })}</Description>
                </div>

                <Checkbox
                  id="oci-insecure"
                  isSelected={f.insecure}
                  onChange={(isSelected) => set({ insecure: isSelected })}
                  className="flex-row items-center gap-2"
                >
                  <Checkbox.Control>
                    <Checkbox.Indicator />
                  </Checkbox.Control>
                  <Checkbox.Content className="text-xs font-normal text-fg">{intl.formatMessage({ id: "web.components.modules.source_dialog.allow_plain_http_local_registries_only", defaultMessage: "Allow plain HTTP (local registries only)", description: "Source dialog: checkbox label that allows plain HTTP for a source, meant only for local registries; the same label appears in the edit view" })}</Checkbox.Content>
                </Checkbox>

                <div className="flex flex-col gap-1.5">
                  <Select
                    className="flex flex-col gap-1.5"
                    value={f.verifyMode}
                    onChange={(v) => set({ verifyMode: v as VerifyMode })}
                    aria-label={intl.formatMessage({ id: "web.components.modules.source_dialog.signature_verification", defaultMessage: "Signature verification", description: "Source dialog: label above the signature verification dropdown that sets how bundles are checked; also the screen-reader name of that dropdown" })}
                  >
                    <Label className="text-xs leading-4 font-normal text-muted">{intl.formatMessage({ id: "web.components.modules.source_dialog.signature_verification", defaultMessage: "Signature verification", description: "Source dialog: label above the signature verification dropdown that sets how bundles are checked; also the screen-reader name of that dropdown" })}</Label>
                    <Select.Trigger
                      className="w-full items-center rounded border border-border bg-surface px-3 py-2 text-sm hover:bg-surface/80"
                    >
                      <Select.Value className="text-[var(--field-placeholder)]" />
                      <Select.Indicator className="ml-auto h-4 w-4" />
                    </Select.Trigger>
                    <Select.Popover className="rounded border border-border">
                      <ListBox aria-label={intl.formatMessage({ id: "web.components.modules.source_dialog.signature_verification_options", defaultMessage: "Signature verification options", description: "Source dialog: screen-reader name of the list of signature verification choices; not visible on screen" })}>
                        {VERIFY_OPTIONS.map((opt) => (
                          <ListBoxItem key={opt.value} id={opt.value}>
                            {intl.formatMessage(opt.label)}
                          </ListBoxItem>
                        ))}
                      </ListBox>
                    </Select.Popover>
                  </Select>
                  <Description className="text-[11px] leading-[14px] text-muted">{intl.formatMessage({ id: "web.components.modules.source_dialog.require_a_valid_cosign_signature_on", defaultMessage: "Require a valid cosign signature on every bundle pulled from this source.", description: "Source dialog: hint under the signature verification dropdown saying every bundle from the source must have a valid cosign signature; cosign is a technical term, usually left untranslated" })}</Description>
                </div>

                {f.verifyMode === "keyed" && (
                  <div className="flex flex-col gap-1.5">
                    <Label htmlFor="verify-key-secret" className="text-xs leading-4 font-normal text-muted">{intl.formatMessage({ id: "web.components.modules.source_dialog.public_key_secret", defaultMessage: "Public key secret", description: "Source dialog: label above the public key secret field, shown only for key-based verification" })}</Label>
                    <Input
                      id="verify-key-secret"
                      value={f.verifyKeySecret}
                      onChange={(e) => set({ verifyKeySecret: e.target.value })}
                      placeholder={intl.formatMessage({ id: "web.components.modules.source_dialog.cosign_pub", defaultMessage: "cosign-pub", description: "Source dialog: example value shown inside the empty public key secret field; a sample secret name, not a default value" })}
                    />
                    <Description className="text-[11px] leading-[14px] text-muted">{intl.formatMessage({ id: "web.components.modules.source_dialog.secret_holding_the_cosign_public_key", defaultMessage: "Secret holding the cosign public key under the cosign.pub data key.", description: "Source dialog: hint under the public key secret field saying the secret holds the cosign public key under the cosign.pub data key; key names are kept as written" })}</Description>
                  </div>
                )}

                {f.verifyMode === "keyless" && (
                  <>
                    <div className="flex flex-col gap-1.5">
                      <Label htmlFor="verify-issuer" className="text-xs leading-4 font-normal text-muted">{intl.formatMessage({ id: "web.components.modules.source_dialog.oidc_issuer", defaultMessage: "OIDC issuer", description: "Source dialog: label above the OIDC issuer field, shown for keyless verification; OIDC is a technical term, usually left untranslated" })}</Label>
                      <Input
                        id="verify-issuer"
                        value={f.verifyIssuer}
                        onChange={(e) => set({ verifyIssuer: e.target.value })}
                        placeholder={intl.formatMessage({ id: "web.components.modules.source_dialog.https_token_actions_githubusercontent", defaultMessage: "https://token.actions.githubusercontent.com", description: "Source dialog: example value shown inside the empty OIDC issuer field; a sample issuer address, keep it as written" })}
                      />
                      <Description className="text-[11px] leading-[14px] text-muted">{intl.formatMessage({ id: "web.components.modules.source_dialog.issuer_embedded_in_the_signing", defaultMessage: "Issuer embedded in the signing certificate.", description: "Source dialog: hint under the OIDC issuer field saying it is the issuer embedded in the signing certificate" })}</Description>
                    </div>

                    <div className="flex flex-col gap-1.5">
                      <Label htmlFor="verify-identity" className="text-xs leading-4 font-normal text-muted">{intl.formatMessage({ id: "web.components.modules.source_dialog.certificate_identity", defaultMessage: "Certificate identity", description: "Source dialog: label above the certificate identity field, shown for keyless verification; it names the identity that signed the bundle" })}</Label>
                      <Input
                        id="verify-identity"
                        value={f.verifyIdentity}
                        onChange={(e) => set({ verifyIdentity: e.target.value })}
                        placeholder={intl.formatMessage({ id: "web.components.modules.source_dialog.github_com_org_repo_github_workflows", defaultMessage: "github.com/org/repo/.github/workflows/release.yml@refs/heads/main", description: "Source dialog: example value shown inside the empty certificate identity field; a sample identity path, keep it as written" })}
                      />
                      <Description className="text-[11px] leading-[14px] text-muted">{intl.formatMessage({ id: "web.components.modules.source_dialog.san_identity_that_must_have_produced", defaultMessage: "SAN identity that must have produced the signature.", description: "Source dialog: hint under the certificate identity field saying it is the SAN identity that must have produced the signature; SAN is a technical term, usually left untranslated" })}</Description>
                    </div>
                  </>
                )}
              </>
            )}

            {f.type === "git" && (
              <>
                <div className="flex flex-col gap-1.5">
                  <Label htmlFor="git-url" className="text-xs leading-4 font-normal text-muted">{intl.formatMessage({ id: "web.components.modules.source_dialog.clone_url", defaultMessage: "Clone URL", description: "Source dialog: label above the clone URL field, shown for git sources, where the repository is fetched from" })}</Label>
                  <Input
                    id="git-url"
                    value={f.url}
                    onChange={(e) => set({ url: e.target.value })}
                    placeholder={intl.formatMessage({ id: "web.components.modules.source_dialog.https_github_com_example_gameplane", defaultMessage: "https://github.com/example/gameplane-modules", description: "Source dialog: example value shown inside the empty clone URL field; a sample repository address, keep it as written" })}
                  />
                </div>

                <div className="flex flex-col gap-1.5">
                  <Label htmlFor="git-ref" className="text-xs leading-4 font-normal text-muted">{intl.formatMessage({ id: "web.components.modules.source_dialog.ref", defaultMessage: "Ref", description: "Source dialog: label above the git ref field, the branch or tag to use from the repository" })}</Label>
                  <Input
                    id="git-ref"
                    value={f.ref}
                    onChange={(e) => set({ ref: e.target.value })}
                    placeholder={intl.formatMessage({ id: "web.components.modules.source_dialog.main", defaultMessage: "main", description: "Source dialog: example value shown inside the empty git ref field; the branch name main as a sample" })}
                  />
                  <Description className="text-[11px] leading-[14px] text-muted">{intl.formatMessage({ id: "web.components.modules.source_dialog.branch_or_tag_defaults_to_main", defaultMessage: "Branch or tag. Defaults to main.", description: "Source dialog: hint under the git ref field saying the value is a branch or tag and that it defaults to main" })}</Description>
                </div>

                <div className="flex flex-col gap-1.5">
                  <Label htmlFor="git-subpath" className="text-xs leading-4 font-normal text-muted">{intl.formatMessage({ id: "web.components.modules.source_dialog.subdirectory", defaultMessage: "Subdirectory", description: "Source dialog: label above the subdirectory field for git sources, the folder inside the repository to scan" })}</Label>
                  <Input
                    id="git-subpath"
                    value={f.subPath}
                    onChange={(e) => set({ subPath: e.target.value })}
                    placeholder={intl.formatMessage({ id: "web.components.modules.source_dialog.modules_2", defaultMessage: "modules", description: "Source dialog: example value shown inside the empty subdirectory field; a sample folder name, not a default value" })}
                  />
                  <Description className="text-[11px] leading-[14px] text-muted">{intl.formatMessage({ id: "web.components.modules.source_dialog.scan_only_this_path_inside_the_repo", defaultMessage: "Scan only this path inside the repo (optional).", description: "Source dialog: hint under the subdirectory field saying only this path inside the repository is scanned and that the field is optional" })}</Description>
                </div>

                <div className="flex flex-col gap-1.5">
                  <Label htmlFor="git-secret" className="text-xs leading-4 font-normal text-muted">{intl.formatMessage({ id: "web.components.modules.source_dialog.credentials_secret", defaultMessage: "Credentials secret", description: "Source dialog: label above the credentials secret name field, shown for git and archive sources; the secret holds access credentials" })}</Label>
                  <Input
                    id="git-secret"
                    value={f.secretName}
                    onChange={(e) => set({ secretName: e.target.value })}
                    placeholder={intl.formatMessage({ id: "web.components.modules.source_dialog.gh_creds", defaultMessage: "gh-creds", description: "Source dialog: example value shown inside the empty credentials secret field; a sample secret name, not a default value" })}
                  />
                  <Description className="text-[11px] leading-[14px] text-muted">{intl.formatMessage({ id: "web.components.modules.source_dialog.secret_with_token_username_password", defaultMessage: "Secret with token / username+password (https) or ssh-privatekey + known_hosts (ssh). Optional.", description: "Source dialog: hint under the credentials secret field saying the secret holds a token or username and password for https, or an SSH key and known hosts for ssh; the field is optional" })}</Description>
                </div>
              </>
            )}

            {f.type === "http" && (
              <>
                <div className="flex flex-col gap-1.5">
                  <Label htmlFor="http-url" className="text-xs leading-4 font-normal text-muted">{intl.formatMessage({ id: "web.components.modules.source_dialog.archive_url", defaultMessage: "Archive URL", description: "Source dialog: label above the archive URL field, shown for archive sources, where the archive is downloaded from" })}</Label>
                  <Input
                    id="http-url"
                    value={f.url}
                    onChange={(e) => set({ url: e.target.value })}
                    placeholder={intl.formatMessage({ id: "web.components.modules.source_dialog.https_example_com_modules_tar_gz", defaultMessage: "https://example.com/modules.tar.gz", description: "Source dialog: example value shown inside the empty archive URL field; a sample address, keep it as written" })}
                  />
                  <Description className="text-[11px] leading-[14px] text-muted">{intl.formatMessage({ id: "web.components.modules.source_dialog.a_tar_gz_or_zip_of_module_directories", defaultMessage: "A .tar.gz or .zip of module directories.", description: "Source dialog: hint under the archive URL field saying the archive is a .tar.gz or .zip of module directories; file extensions are kept as written" })}</Description>
                </div>

                <div className="flex flex-col gap-1.5">
                  <Label htmlFor="http-secret" className="text-xs leading-4 font-normal text-muted">{intl.formatMessage({ id: "web.components.modules.source_dialog.credentials_secret", defaultMessage: "Credentials secret", description: "Source dialog: label above the credentials secret name field, shown for git and archive sources; the secret holds access credentials" })}</Label>
                  <Input
                    id="http-secret"
                    value={f.secretName}
                    onChange={(e) => set({ secretName: e.target.value })}
                    placeholder={intl.formatMessage({ id: "web.components.modules.source_dialog.archive_creds", defaultMessage: "archive-creds", description: "Source dialog: example value shown inside the empty credentials secret field for archive sources; a sample secret name, not a default value" })}
                  />
                  <Description className="text-[11px] leading-[14px] text-muted">{intl.formatMessage({ id: "web.components.modules.source_dialog.secret_with_token_bearer_or_username", defaultMessage: "Secret with token (Bearer) or username+password. Optional.", description: "Source dialog: hint under the archive credentials secret field saying the secret holds a Bearer token or a username and password; the field is optional" })}</Description>
                </div>

                <Checkbox
                  id="http-insecure"
                  isSelected={f.insecure}
                  onChange={(isSelected) => set({ insecure: isSelected })}
                  className="flex-row items-center gap-2"
                >
                  <Checkbox.Control>
                    <Checkbox.Indicator />
                  </Checkbox.Control>
                  <Checkbox.Content className="text-xs font-normal text-fg">{intl.formatMessage({ id: "web.components.modules.source_dialog.allow_plain_http_local_registries_only", defaultMessage: "Allow plain HTTP (local registries only)", description: "Source dialog: checkbox label that allows plain HTTP for a source, meant only for local registries; the same label appears in the edit view" })}</Checkbox.Content>
                </Checkbox>
              </>
            )}

            {f.type === "local" && (
              <div className="flex flex-col gap-1.5">
                <Label htmlFor="local-path" className="text-xs leading-4 font-normal text-muted">{intl.formatMessage({ id: "web.components.modules.source_dialog.path", defaultMessage: "Path", description: "Source dialog: label above the path field for local sources, the folder path inside the operator's module mount" })}</Label>
                <Input
                  id="local-path"
                  value={f.path}
                  onChange={(e) => set({ path: e.target.value })}
                  placeholder={intl.formatMessage({ id: "web.components.modules.source_dialog.bundles", defaultMessage: "bundles", description: "Source dialog: example value shown inside the empty path field for local sources; a sample folder name, not a default value" })}
                />
                <Description className="text-[11px] leading-[14px] text-muted">{intl.formatMessage({ id: "web.components.modules.source_dialog.relative_to_the_operator_s_module_mount", defaultMessage: "Relative to the operator's module mount (Helm: operator.localModules). Empty scans the mount root.", description: "Source dialog: hint under the local path field; the path is relative to the operator's module mount (Helm value operator.localModules) and empty scans the mount root" })}</Description>
              </div>
            )}

            {f.type === "upload" && (
              <div className="rounded border border-border bg-card/40 px-3 py-2 text-xs text-muted">{intl.formatMessage({ id: "web.components.modules.source_dialog.indexes_bundles_uploaded_through_the", defaultMessage: "Indexes bundles uploaded through the dashboard (stored as ConfigMaps in the operator namespace). No further configuration.", description: "Source dialog: note for upload sources saying uploaded bundles are indexed and kept as ConfigMaps in the operator namespace; ConfigMaps is a technical term, usually left untranslated" })}</div>
            )}

            <div className="flex flex-col gap-1.5">
              <Label htmlFor="allow-list" className="text-xs leading-4 font-normal text-muted">{intl.formatMessage({ id: "web.components.modules.source_dialog.allow_list", defaultMessage: "Allow list", description: "Source dialog: label above the allow list field, an optional filter of module names for the source" })}</Label>
              <Input
                id="allow-list"
                value={f.allow}
                onChange={(e) => set({ allow: e.target.value })}
                placeholder={intl.formatMessage({ id: "web.components.modules.source_dialog.minecraft", defaultMessage: "minecraft-*", description: "Source dialog: example value shown inside the empty allow list field; a sample name pattern with a wildcard, keep it as written" })}
              />
              <Description className="text-[11px] leading-[14px] text-muted">{intl.formatMessage({ id: "web.components.modules.source_dialog.optional_module_name_filter_exact_names", defaultMessage: "Optional module name filter — exact names or globs, comma-separated.", description: "Source dialog: hint under the allow list field saying the optional filter takes exact module names or globs, separated by commas" })}</Description>
            </div>

            <div className="flex flex-col gap-1.5">
              <Label htmlFor="refresh-interval" className="text-xs leading-4 font-normal text-muted">{intl.formatMessage({ id: "web.components.modules.source_dialog.refresh_interval", defaultMessage: "Refresh interval", description: "Source dialog: label above the refresh interval field, how often the module catalog is re-indexed" })}</Label>
              <Input
                id="refresh-interval"
                value={f.refreshInterval}
                onChange={(e) => set({ refreshInterval: e.target.value })}
                placeholder={intl.formatMessage({ id: "web.components.modules.source_dialog.1h", defaultMessage: "1h", description: "Source dialog: example value shown inside the empty refresh interval field; a sample duration, keep it as written" })}
              />
              <Description className="text-[11px] leading-[14px] text-muted">{intl.formatMessage({ id: "web.components.modules.source_dialog.how_often_the_catalog_re_indexes", defaultMessage: "How often the catalog re-indexes. Defaults to 1h.", description: "Source dialog: hint under the refresh interval field saying how often the catalog is re-indexed and that the default is 1h" })}</Description>
            </div>

            {error && (
              <p role="alert" className="mt-2 text-xs text-danger">
                {error}
              </p>
            )}
          </ModalBody>

          <ModalFooter className="gap-2">
            <Button
              variant="ghost"
              size="sm"
              className="text-xs"
              onPress={() => onOpenChange(false)}
              isDisabled={busy}
            >{intl.formatMessage({ id: "web.components.modules.source_dialog.cancel", defaultMessage: "Cancel", description: "Source dialog: button that closes the source dialog without saving; keep it short" })}</Button>
            <Button
              variant="primary"
              size="sm"
              className="text-xs"
              isDisabled={busy || !canSubmit}
              onPress={submit}
            >
              {busy ? <Loader2 className="h-4 w-4 animate-spin" /> : null}
              {editing ? intl.formatMessage({ id: "web.components.modules.source_dialog.save", defaultMessage: "Save", description: "Source dialog: submit button label when editing an existing source" }) : intl.formatMessage({ id: "web.components.modules.source_dialog.add_source", defaultMessage: "Add source", description: "Source dialog: submit button label when adding a source" })}
            </Button>
          </ModalFooter>
          </ModalDialog>
        </ModalContainer>
      </ModalBackdrop>
    </Modal>
  );
}
