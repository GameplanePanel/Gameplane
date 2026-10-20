import { useState, useId, useRef } from "react";
import { useQuery } from "@tanstack/react-query";
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
  Select,
  ListBox,
  ListBoxItem,
  Description,
} from "@heroui/react";
import {
  CheckCircle2,
  AlertCircle,
  Download,
  UploadCloud,
  Plus,
  Trash2,
  Server,
  Coffee,
  Box,
  ChevronRight,
  ArrowLeft,
  Loader2,
} from "lucide-react";
import { APIError } from "@/lib/api";
import {
  ModuleBuilder,
  type BuilderPortDef,
  type BuilderValidateResponse,
  type BuilderPreviewResponse,
} from "@/lib/endpoints";
import { useOptionalIntl } from "@/lib/i18n/format";
import { defineMessages, type IntlShape } from "react-intl";

interface BuildModuleDialogProps {
  cluster?: string;
  open: boolean;
  onOpenChange: (open: boolean) => void;
  sources: string[];
  onInstalled?: () => Promise<void> | void;
}

// Display labels for the canonical category values. The value is what gets
// sent to the API and stored in module.yaml; only the chip text is translated.
// Unknown (custom) categories show the raw value.
const categoryMessages = defineMessages({
  "Survival": { id: "web.components.modules.build_module_dialog.category_survival", defaultMessage: "Survival", description: "Build module dialog: label of the Survival category chip (the stored category value stays English)" },
  "Sandbox": { id: "web.components.modules.build_module_dialog.category_sandbox", defaultMessage: "Sandbox", description: "Build module dialog: label of the Sandbox category chip (the stored category value stays English)" },
  "Shooter": { id: "web.components.modules.build_module_dialog.category_shooter", defaultMessage: "Shooter", description: "Build module dialog: label of the Shooter category chip (the stored category value stays English)" },
  "Simulation": { id: "web.components.modules.build_module_dialog.category_simulation", defaultMessage: "Simulation", description: "Build module dialog: label of the Simulation category chip (the stored category value stays English)" },
  "Building": { id: "web.components.modules.build_module_dialog.category_building", defaultMessage: "Building", description: "Build module dialog: label of the Building category chip (the stored category value stays English)" },
  "Adventure": { id: "web.components.modules.build_module_dialog.category_adventure", defaultMessage: "Adventure", description: "Build module dialog: label of the Adventure category chip (the stored category value stays English)" },
  "Horror": { id: "web.components.modules.build_module_dialog.category_horror", defaultMessage: "Horror", description: "Build module dialog: label of the Horror category chip (the stored category value stays English)" },
  "Co-op": { id: "web.components.modules.build_module_dialog.category_co_op", defaultMessage: "Co-op", description: "Build module dialog: label of the Co-op category chip (the stored category value stays English)" },
  "PvP": { id: "web.components.modules.build_module_dialog.category_pvp", defaultMessage: "PvP", description: "Build module dialog: label of the PvP category chip (the stored category value stays English)" },
  "Modded": { id: "web.components.modules.build_module_dialog.category_modded", defaultMessage: "Modded", description: "Build module dialog: label of the Modded category chip (the stored category value stays English)" },
  "Creative": { id: "web.components.modules.build_module_dialog.category_creative", defaultMessage: "Creative", description: "Build module dialog: label of the Creative category chip (the stored category value stays English)" },
});

function categoryLabel(intl: IntlShape, value: string): string {
  return Object.prototype.hasOwnProperty.call(categoryMessages, value)
    ? intl.formatMessage(categoryMessages[value as keyof typeof categoryMessages])
    : value;
}

const CANONICAL_CATEGORIES = [
  "Survival",
  "Sandbox",
  "Shooter",
  "Simulation",
  "Building",
  "Adventure",
  "Horror",
  "Co-op",
  "PvP",
  "Modded",
  "Creative",
];

// Map archetype IDs to lucide icons
function getArchetypeIcon(id: string) {
  const iconMap: Record<string, typeof Server> = {
    steamcmd: Server,
    java: Coffee,
    generic: Box,
  };
  return iconMap[id] || Box; // Default to Box for unknown archetypes
}

function isValidDns1123(name: string): boolean {
  if (!name || name.length > 63) return false;
  return /^[a-z0-9]([-a-z0-9]*[a-z0-9])?$/.test(name);
}

export function BuildModuleDialog({
  open,
  onOpenChange,
  sources,
  onInstalled,
  cluster = "local",
}: BuildModuleDialogProps) {
  const intl = useOptionalIntl();
  const { data: archetypesData, isLoading: archetypesLoading, error: archetypesError } = useQuery({
    queryKey: ["builder-archetypes", cluster],
    queryFn: () => ModuleBuilder.archetypes(cluster),
  });

  const [step, setStep] = useState<1 | 2 | 3>(1);
  const [archetype, setArchetype] = useState("");
  const [name, setName] = useState("");
  const [displayName, setDisplayName] = useState("");
  const [summary, setSummary] = useState("");
  const [categories, setCategories] = useState<string[]>([]);
  const [customTagInput, setCustomTagInput] = useState("");

  const [image, setImage] = useState("");
  const [ports, setPorts] = useState<BuilderPortDef[]>([]);
  const [storageSize, setStorageSize] = useState("");
  const [storageMountPath, setStorageMountPath] = useState("");

  const [activeTab, setActiveTab] = useState<"module.yaml" | "template.yaml" | "README.md">("module.yaml");
  const [moduleYaml, setModuleYaml] = useState("");
  const [templateYaml, setTemplateYaml] = useState("");
  const [readmeMd, setReadmeMd] = useState("");

  const [simMemory, setSimMemory] = useState("8Gi");
  const [validationResult, setValidationResult] = useState<BuilderValidateResponse | null>(null);
  const [previewResult, setPreviewResult] = useState<BuilderPreviewResponse | null>(null);

  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [targetSource, setTargetSource] = useState(sources[0] ?? "");
  const validationSeqRef = useRef(0);

  const nameInputId = useId();
  const displayNameInputId = useId();
  const summaryInputId = useId();
  const customTagInputId = useId();
  const imageInputId = useId();
  const storageSizeInputId = useId();
  const storageMountPathInputId = useId();

  const [resetFor, setResetFor] = useState<{ open: boolean; sources: string[] }>({
    open: false,
    sources,
  });
  if (open !== resetFor.open || sources !== resetFor.sources) {
    setResetFor({ open, sources });
    if (open) {
      setStep(1);
      setArchetype(""); // Will be set to first archetype once they load
      setName("");
      setDisplayName("");
      setSummary("");
      setCategories([]);
      setCustomTagInput("");
      setImage("");
      setPorts([]);
      setStorageSize("");
      setStorageMountPath("");
      setModuleYaml("");
      setTemplateYaml("");
      setReadmeMd("");
      setSimMemory("8Gi");
      setValidationResult(null);
      setPreviewResult(null);
      setError(null);
      setTargetSource(sources[0] ?? "");
    }
  }

  // Once archetypes are loaded and user hasn't selected one, auto-select the first
  if (archetypesData && !archetype && archetypesData.archetypes.length > 0) {
    const firstArch = archetypesData.archetypes[0];
    handleSelectArchetype(firstArch.id);
  }

  function handleSelectArchetype(archId: string) {
    setArchetype(archId);
    const arch = archetypesData?.archetypes.find((a) => a.id === archId);
    if (arch) {
      setImage(arch.defaultImage);
      setPorts(arch.defaultPorts.map((p) => ({ ...p })));
      setStorageSize(arch.defaultStorage.size);
      setStorageMountPath(arch.defaultStorage.mountPath);
    }
  }

  function toggleCategory(cat: string) {
    if (categories.includes(cat)) {
      setCategories(categories.filter((c) => c !== cat));
    } else {
      setCategories([...categories, cat]);
    }
  }

  function addCustomTag(tag: string) {
    const trimmed = tag.trim();
    if (!trimmed || categories.includes(trimmed)) {
      return;
    }
    setCategories([...categories, trimmed]);
    setCustomTagInput("");
  }

  function addPort() {
    setPorts([...ports, { name: `port-${ports.length + 1}`, containerPort: 8080, protocol: "TCP", advertise: true }]);
  }

  function removePort(idx: number) {
    setPorts(ports.filter((_, i) => i !== idx));
  }

  function updatePort(idx: number, patch: Partial<BuilderPortDef>) {
    setPorts(ports.map((p, i) => (i === idx ? { ...p, ...patch } : p)));
  }

  async function goToStep2() {
    if (!isValidDns1123(name)) {
      setError(intl.formatMessage({ id: "web.components.modules.build_module_dialog.module_name_invalid", defaultMessage: "Module name must be a valid lowercase DNS-1123 label.", description: "Build module dialog: validation error when the module name is not a valid DNS-1123 label" }));
      return;
    }
    if (!image) {
      setError(intl.formatMessage({ id: "web.components.modules.build_module_dialog.archetype_or_image_required", defaultMessage: "Please select an archetype or provide a container image.", description: "Build module dialog: validation error when neither an archetype nor a container image is chosen" }));
      return;
    }
    setError(null);
    setStep(2);
  }

  async function goToStep3() {
    setBusy(true);
    setError(null);
    try {
      const scaffoldRes = await ModuleBuilder.scaffold({
        name,
        displayName,
        archetype,
        image,
        ports,
        categories,
        summary,
        storageSize,
        storageMountPath,
      }, cluster);
      setModuleYaml(scaffoldRes.moduleYaml);
      setTemplateYaml(scaffoldRes.templateYaml);
      setReadmeMd(scaffoldRes.readmeMd);

      const seq = ++validationSeqRef.current;
      const [valRes, prevRes] = await Promise.all([
        ModuleBuilder.validate({
          moduleYaml: scaffoldRes.moduleYaml,
          templateYaml: scaffoldRes.templateYaml,
        }, cluster),
        ModuleBuilder.preview({
          templateYaml: scaffoldRes.templateYaml,
          memory: simMemory,
        }, cluster),
      ]);
      if (seq === validationSeqRef.current) {
        setValidationResult(valRes);
        setPreviewResult(prevRes);
      }
      setStep(3);
    } catch (err) {
      setError(err instanceof APIError ? err.body || err.message : (err as Error).message);
    } finally {
      setBusy(false);
    }
  }

  async function revalidate(mYaml: string, tYaml: string, mem: string) {
    const seq = ++validationSeqRef.current;
    try {
      const [valRes, prevRes] = await Promise.all([
        ModuleBuilder.validate({ moduleYaml: mYaml, templateYaml: tYaml }, cluster),
        ModuleBuilder.preview({ templateYaml: tYaml, memory: mem }, cluster),
      ]);
      if (seq === validationSeqRef.current) {
        setValidationResult(valRes);
        setPreviewResult(prevRes);
      }
    } catch (err) {
      if (seq === validationSeqRef.current) {
        setValidationResult({
          clean: false,
          errorCount: 1,
          warningCount: 0,
          findings: [
            {
              level: "ERROR",
              ruleId: "validation-error",
              file: "template.yaml",
              line: 1,
              message: err instanceof APIError ? err.body || err.message : (err as Error).message || intl.formatMessage({ id: "web.components.modules.build_module_dialog.validation_failed", defaultMessage: "Validation failed", description: "Build module dialog: fallback finding message when validation fails without a message" }),
            },
          ],
        });
        setPreviewResult(null);
      }
    }
  }

  async function handleDownloadArchive() {
    setBusy(true);
    setError(null);
    try {
      const blob = await ModuleBuilder.downloadArchive({
        name,
        moduleYaml,
        templateYaml,
        readmeMd,
      }, cluster);
      const url = URL.createObjectURL(blob);
      const a = document.createElement("a");
      a.href = url;
      a.download = `${name}.tar.gz`;
      document.body.appendChild(a);
      a.click();
      a.remove();
      URL.revokeObjectURL(url);
    } catch (err) {
      setError(err instanceof APIError ? err.body || err.message : (err as Error).message);
    } finally {
      setBusy(false);
    }
  }

  async function handleInstallToCluster() {
    if (!targetSource) {
      setError(intl.formatMessage({ id: "web.components.modules.build_module_dialog.upload_destination_required", defaultMessage: "Please select an upload source destination.", description: "Build module dialog: validation error when no upload source destination is chosen" }));
      return;
    }
    setBusy(true);
    setError(null);
    try {
      await ModuleBuilder.installToCluster({
        name,
        moduleYaml,
        templateYaml,
        readmeMd,
        targetSource,
      }, cluster);
      if (onInstalled) {
        await onInstalled();
      }
      onOpenChange(false);
    } catch (err) {
      setError(err instanceof APIError ? err.body || err.message : (err as Error).message);
    } finally {
      setBusy(false);
    }
  }

  const isDnsValid = isValidDns1123(name);
  const isImagePinned = /@sha256:[a-f0-9]{64}$/.test(image);

  return (
    <Modal isOpen={open} onOpenChange={onOpenChange}>
      <ModalBackdrop isDismissable={!busy} isKeyboardDismissDisabled={busy}>
        <ModalContainer>
          <ModalDialog className="w-[840px] max-w-[calc(100vw-2rem)] overflow-hidden flex flex-col max-h-[90vh]">
            <ModalHeader>
              <div className="w-full">
                <ModalHeading className="text-lg font-semibold">{intl.formatMessage({ id: "web.components.modules.build_module_dialog.create_game_module", defaultMessage: "Create game module", description: "Build module dialog: title of the three-step wizard for creating a new game module" })}</ModalHeading>
                <Description className="text-xs text-muted">{intl.formatMessage({ id: "web.components.modules.build_module_dialog.step_step_of_3", defaultMessage: "Step {step} of 3 ·", description: "Build module dialog: sentence with step in Description" }, { step: step })} {step === 1
                    ? intl.formatMessage({ id: "web.components.modules.build_module_dialog.step_preset_metadata", defaultMessage: "Preset & Metadata", description: "Build module dialog: name of step 1 in the step header" })
                    : step === 2
                    ? intl.formatMessage({ id: "web.components.modules.build_module_dialog.step_container_ports", defaultMessage: "Container & Ports", description: "Build module dialog: name of step 2 in the step header" })
                    : intl.formatMessage({ id: "web.components.modules.build_module_dialog.step_review_export", defaultMessage: "Review, Preview & Export", description: "Build module dialog: name of step 3 in the step header" })}
                </Description>
              </div>
            </ModalHeader>

            <ModalBody>
              <div className="space-y-4">
                {/* Stepper */}
                <div className="flex items-center gap-3 text-xs border-b border-border pb-4">
                  <div className="flex items-center gap-2">
                    <span
                      className={`flex h-5 w-5 items-center justify-center rounded-full text-[11px] font-semibold ${
                        step === 1
                          ? "bg-primary text-primary-fg"
                          : "bg-surface text-fg border border-border"
                      }`}
                    >
                      {step > 1 ? intl.formatMessage({ id: "web.components.modules.build_module_dialog.step_done", defaultMessage: "\u2713", description: "Build module dialog: mark shown on a completed step" }) : intl.formatMessage({ id: "web.components.modules.build_module_dialog.step_number_1", defaultMessage: "1", description: "Build module dialog: number of step 1 in the step indicator" })}
                    </span>
                    <span className={step === 1 ? "font-medium text-fg" : "text-muted"}>{intl.formatMessage({ id: "web.components.modules.build_module_dialog.preset_metadata", defaultMessage: "Preset & Metadata", description: "Build module dialog: name of step 1 in the wizard's progress bar, where the preset and module details are set; keep it short" })}</span>
                  </div>
                  <span className="text-muted">{intl.formatMessage({ id: "web.common.separator_dot", defaultMessage: "·", description: "Visual separator between two items" })}</span>
                  <div className="flex items-center gap-2">
                    <span
                      className={`flex h-5 w-5 items-center justify-center rounded-full text-[11px] font-semibold ${
                        step === 2
                          ? "bg-primary text-primary-fg"
                          : "bg-surface text-fg border border-border"
                      }`}
                    >
                      {step > 2 ? intl.formatMessage({ id: "web.components.modules.build_module_dialog.step_done", defaultMessage: "\u2713", description: "Build module dialog: mark shown on a completed step" }) : intl.formatMessage({ id: "web.components.modules.build_module_dialog.step_number_2", defaultMessage: "2", description: "Build module dialog: number of step 2 in the step indicator" })}
                    </span>
                    <span className={step === 2 ? "font-medium text-fg" : "text-muted"}>{intl.formatMessage({ id: "web.components.modules.build_module_dialog.container_ports", defaultMessage: "Container & Ports", description: "Build module dialog: name of step 2 in the wizard's progress bar, for the container image and ports; keep it short" })}</span>
                  </div>
                  <span className="text-muted">{intl.formatMessage({ id: "web.common.separator_dot", defaultMessage: "·", description: "Visual separator between two items" })}</span>
                  <div className="flex items-center gap-2">
                    <span
                      className={`flex h-5 w-5 items-center justify-center rounded-full text-[11px] font-semibold ${
                        step === 3
                          ? "bg-primary text-primary-fg"
                          : "bg-surface text-fg border border-border"
                      }`}
                    >
                      {intl.formatMessage({ id: "web.components.modules.build_module_dialog.step_number_3", defaultMessage: "3", description: "Build module dialog: number of step 3 in the step indicator" })}
                    </span>
                    <span className={step === 3 ? "font-medium text-fg" : "text-muted"}>{intl.formatMessage({ id: "web.components.modules.build_module_dialog.review_export", defaultMessage: "Review & Export", description: "Build module dialog: name of step 3 in the wizard's progress bar, for checking the module and exporting it; keep it short" })}</span>
                  </div>
                </div>

                {/* Error Banner */}
                {error && (
                  <div className="flex items-center gap-2 rounded-md bg-danger/10 p-3 text-xs text-danger">
                    <AlertCircle className="h-4 w-4 shrink-0" />
                    <span>{error}</span>
                  </div>
                )}

                {/* Body Content */}
                <div className="overflow-y-auto">
            {step === 1 && (
              <div className="space-y-5">
                <div>
                  <label className="text-xs font-medium text-fg">{intl.formatMessage({ id: "web.components.modules.build_module_dialog.archetype_preset", defaultMessage: "Archetype preset", description: "Build module dialog: label above the archetype preset choice on step 1 that fills the form from a starting preset" })}</label>

                  {archetypesLoading && (
                    <div className="mt-2 flex items-center justify-center gap-2 py-8 text-muted">
                      <Loader2 className="h-4 w-4 animate-spin" />
                      <span className="text-xs">{intl.formatMessage({ id: "web.components.modules.build_module_dialog.loading_archetypes", defaultMessage: "Loading archetypes...", description: "Build module dialog: loading text shown in place of the archetype preset choices while they are fetched; the dots are part of the text" })}</span>
                    </div>
                  )}

                  {archetypesError && (
                    <div className="flex items-center gap-2 rounded-md bg-danger/10 p-3 text-xs text-danger mt-2">
                      <AlertCircle className="h-4 w-4 shrink-0" />
                      <span>{intl.formatMessage({ id: "web.components.modules.build_module_dialog.failed_to_load_archetypes", defaultMessage: "Failed to load archetypes", description: "Build module dialog: error message shown in place of the archetype preset choices when they cannot be loaded" })}</span>
                    </div>
                  )}

                  {!archetypesLoading && archetypesData?.archetypes && (
                    <div className="mt-2 grid grid-cols-3 gap-3">
                      {archetypesData.archetypes.map((arch) => {
                        const Icon = getArchetypeIcon(arch.id);
                        const selected = archetype === arch.id;
                        return (
                          <button
                            key={arch.id}
                            type="button"
                            onClick={() => handleSelectArchetype(arch.id)}
                            disabled={busy}
                            className={`flex flex-col text-left rounded-lg border p-3.5 transition-all ${
                              selected
                                ? "border-primary bg-primary/5 shadow-xs"
                                : "border-border bg-surface/50 hover:bg-surface"
                            }`}
                          >
                            <div className="flex items-center gap-2">
                              <Icon className={`h-4 w-4 ${selected ? "text-primary" : "text-muted"}`} />
                              <span className="text-sm font-semibold text-fg">{arch.title}</span>
                            </div>
                            <p className="mt-1 text-xs text-muted leading-relaxed">{arch.description}</p>
                          </button>
                        );
                      })}
                    </div>
                  )}
                </div>

                <div className="space-y-4">
                  <div>
                    <div className="flex items-center justify-between">
                      <label htmlFor={nameInputId} className="text-xs font-medium text-fg">{intl.formatMessage({ id: "web.components.modules.build_module_dialog.module_name", defaultMessage: "Module Name", description: "Build module dialog: label above the module name field; the name must be a DNS-1123 label of lowercase letters, digits and hyphens; DNS-1123 is a technical term, usually left untranslated" })}</label>
                      {name && (
                        <span
                          className={`text-[11px] ${
                            isDnsValid ? "text-emerald-500 font-medium" : "text-danger"
                          }`}
                        >
                          {isDnsValid ? intl.formatMessage({ id: "web.components.modules.build_module_dialog.dns_valid", defaultMessage: "\u2713 Valid DNS-1123 label", description: "Build module dialog: hint shown when the module name is a valid DNS-1123 label" }) : intl.formatMessage({ id: "web.components.modules.build_module_dialog.dns_invalid", defaultMessage: "Must be lowercase alphanumeric with hyphens", description: "Build module dialog: hint shown when the module name is not a valid DNS-1123 label" })}
                        </span>
                      )}
                    </div>
                    <Input
                      id={nameInputId}
                      value={name}
                      onChange={(e) => setName(e.target.value)}
                      placeholder={intl.formatMessage({ id: "web.components.modules.build_module_dialog.e_g_cs2_match", defaultMessage: "e.g. cs2-match", description: "Build module dialog: example text shown inside the empty module name field; a sample name, not a default value" })}
                      className="mt-1"
                    />
                  </div>

                  <div>
                    <label htmlFor={displayNameInputId} className="text-xs font-medium text-fg">{intl.formatMessage({ id: "web.components.modules.build_module_dialog.display_name", defaultMessage: "Display Name", description: "Build module dialog: label above the display name field, the friendly name shown for the module" })}</label>
                    <Input
                      id={displayNameInputId}
                      value={displayName}
                      onChange={(e) => setDisplayName(e.target.value)}
                      placeholder={intl.formatMessage({ id: "web.components.modules.build_module_dialog.e_g_counter_strike_2", defaultMessage: "e.g. Counter-Strike 2", description: "Build module dialog: example text shown inside the empty display name field; a sample name, not a default value" })}
                      className="mt-1"
                    />
                  </div>

                  <div>
                    <label htmlFor={summaryInputId} className="text-xs font-medium text-fg">{intl.formatMessage({ id: "web.components.modules.build_module_dialog.summary", defaultMessage: "Summary", description: "Build module dialog: label above the summary field, a short description of the module" })}</label>
                    <Input
                      id={summaryInputId}
                      value={summary}
                      onChange={(e) => setSummary(e.target.value)}
                      placeholder={intl.formatMessage({ id: "web.components.modules.build_module_dialog.short_description_of_the_game_server", defaultMessage: "Short description of the game server", description: "Build module dialog: example text shown inside the empty summary field, hinting what to write there" })}
                      className="mt-1"
                    />
                  </div>

                  <div>
                    <div className="flex items-center justify-between">
                      <label className="text-xs font-medium text-fg">{intl.formatMessage({ id: "web.components.modules.build_module_dialog.categories", defaultMessage: "Categories", description: "Build module dialog: label above the category tags, which are chosen from suggestions or typed in as custom tags" })}</label>
                      <span className="text-[11px] text-muted">{intl.formatMessage({ id: "web.components.modules.build_module_dialog.recommended", defaultMessage: "Recommended", description: "Build module dialog: small note beside the Categories label marking the suggested tags" })}</span>
                    </div>
                    <div className="mt-1.5 flex flex-wrap gap-1.5">
                      {CANONICAL_CATEGORIES.map((cat) => {
                        const active = categories.includes(cat);
                        return (
                          <button
                            key={cat}
                            type="button"
                            onClick={() => toggleCategory(cat)}
                            disabled={busy}
                            className={`rounded-full px-2.5 py-1 text-xs transition-colors ${
                              active
                                ? "bg-primary text-primary-fg font-medium"
                                : "border border-border bg-surface text-muted hover:text-fg"
                            }`}
                          >
                            {categoryLabel(intl, cat)} {active && intl.formatMessage({ id: "web.components.modules.build_module_dialog.category_mark", defaultMessage: "\u00d7", description: "Build module dialog: marker shown on a category chip" })}
                          </button>
                        );
                      })}
                    </div>

                    {/* Custom tag input */}
                    <div className="mt-3 flex gap-2">
                      <Input
                        id={customTagInputId}
                        value={customTagInput}
                        onChange={(e) => setCustomTagInput(e.target.value)}
                        onKeyDown={(e) => {
                          if (e.key === "Enter") {
                            e.preventDefault();
                            addCustomTag(customTagInput);
                          }
                        }}
                        placeholder={intl.formatMessage({ id: "web.components.modules.build_module_dialog.add_custom_tag", defaultMessage: "Add custom tag...", description: "Build module dialog: placeholder inside the custom tag text field asking the user to type a tag; the trailing dots are part of the text" })}
                        className="text-xs"
                        aria-label={intl.formatMessage({ id: "web.components.modules.build_module_dialog.custom_tag_input", defaultMessage: "Custom tag input", description: "Build module dialog: screen-reader label for the custom tag text field; not visible on screen" })}
                      />
                      <Button
                        size="sm"
                        onPress={() => addCustomTag(customTagInput)}
                        isDisabled={busy || !customTagInput.trim()}
                        className="h-10"
                        aria-label={intl.formatMessage({ id: "web.components.modules.build_module_dialog.add_tag", defaultMessage: "Add tag", description: "Build module dialog: button that adds the typed custom tag to the categories; keep it short" })}
                      >
                        <Plus className="h-3.5 w-3.5" />
                      </Button>
                    </div>

                    {/* Selected tags (custom only) */}
                    {categories.filter((c) => !CANONICAL_CATEGORIES.includes(c)).length > 0 && (
                      <>
                        <div className="text-xs font-medium text-fg mt-3">{intl.formatMessage({ id: "web.components.modules.build_module_dialog.selected_tags", defaultMessage: "Selected Tags", description: "Build module dialog: heading above the list of custom tags already added; shown only when at least one custom tag exists" })}</div>
                        <div className="flex flex-wrap gap-1.5">
                          {categories
                            .filter((c) => !CANONICAL_CATEGORIES.includes(c))
                            .map((cat) => (
                              <button
                                key={cat}
                                type="button"
                                onClick={() => toggleCategory(cat)}
                                disabled={busy}
                                className="rounded-full px-2.5 py-1 text-xs bg-primary text-primary-fg font-medium transition-colors hover:opacity-80"
                              >
                                {cat} {intl.formatMessage({ id: "web.components.modules.build_module_dialog.category_mark", defaultMessage: "\u00d7", description: "Build module dialog: marker shown on a category chip" })}
                              </button>
                            ))}
                        </div>
                      </>
                    )}
                  </div>
                </div>
              </div>
            )}

            {step === 2 && (
              <div className="space-y-5">
                <div>
                  <div className="flex items-center justify-between">
                    <label htmlFor={imageInputId} className="text-xs font-medium text-fg">{intl.formatMessage({ id: "web.components.modules.build_module_dialog.container_image", defaultMessage: "Container Image", description: "Build module dialog: label above the container image field on step 2; the image reference should be pinned by digest" })}</label>
                    {image && (
                      <span
                        className={`text-[11px] ${
                          isImagePinned ? "text-emerald-500 font-medium" : "text-amber-500"
                        }`}
                      >
                        {isImagePinned
                          ? intl.formatMessage({ id: "web.components.modules.build_module_dialog.pinned_digest", defaultMessage: "\u2713 Pinned by digest (@sha256:...)", description: "Build module dialog: hint shown when the image is pinned by digest" })
                          : intl.formatMessage({ id: "web.components.modules.build_module_dialog.unpinned_image", defaultMessage: "\u26a0 Unpinned image (recommend @sha256:...)", description: "Build module dialog: warning shown when the image is not pinned by digest" })}
                      </span>
                    )}
                  </div>
                  <Input
                    id={imageInputId}
                    value={image}
                    onChange={(e) => setImage(e.target.value)}
                    placeholder={intl.formatMessage({ id: "web.components.modules.build_module_dialog.e_g_ghcr_io_valgul_game_v1_0_sha256", defaultMessage: "e.g. ghcr.io/valgul/game:v1.0@sha256:...", description: "Build module dialog: example image reference shown inside the empty container image field; keep the reference format as written" })}
                    className="mt-1 font-mono text-xs"
                  />
                </div>

                <div>
                  <div className="flex items-center justify-between">
                    <label className="text-xs font-medium text-fg">{intl.formatMessage({ id: "web.components.modules.build_module_dialog.port_mappings", defaultMessage: "Port Mappings", description: "Build module dialog: label above the list of port mappings on step 2" })}</label>
                    <Button variant="outline" size="sm" onPress={addPort} className="h-7 text-xs">
                      <Plus className="mr-1 h-3.5 w-3.5" />{intl.formatMessage({ id: "web.components.modules.build_module_dialog.add_port", defaultMessage: "Add Port", description: "Build module dialog: button that adds another row to the port mappings list; keep it short" })}</Button>
                  </div>

                  <div className="mt-2 space-y-2">
                    {ports.map((p, idx) => (
                      <div key={idx} className="flex items-center gap-2">
                        <Input
                          value={p.name}
                          onChange={(e) => updatePort(idx, { name: e.target.value })}
                          placeholder={intl.formatMessage({ id: "web.components.modules.build_module_dialog.name", defaultMessage: "name", description: "Build module dialog: example text shown inside the port name field of a port row; a short placeholder word, not a default value" })}
                          className="w-36 text-xs"
                        />
                        <Input
                          type="number"
                          value={p.containerPort}
                          onChange={(e) =>
                            updatePort(idx, { containerPort: parseInt(e.target.value, 10) || 0 })
                          }
                          placeholder={intl.formatMessage({ id: "web.components.modules.build_module_dialog.port", defaultMessage: "port", description: "Build module dialog: example text shown inside the container port number field of a port row; a placeholder word, not a default value" })}
                          className="w-28 text-xs font-mono"
                        />
                        <div className="w-24">
                          <Select
                            value={p.protocol}
                            onChange={(v) => updatePort(idx, { protocol: (v ?? p.protocol) as string })}
                            className="w-24"
                            aria-label={intl.formatMessage({ id: "web.components.modules.build_module_dialog.protocol", defaultMessage: "Protocol", description: "Build module dialog: screen-reader label for the protocol dropdown of a port row; not visible on screen" })}
                          >
                            <Select.Trigger>
                              <Select.Value />
                              <Select.Indicator className="ml-auto h-4 w-4" />
                            </Select.Trigger>
                            <Select.Popover>
                              <ListBox aria-label={intl.formatMessage({ id: "web.components.modules.build_module_dialog.protocol_options", defaultMessage: "Protocol options", description: "Build module dialog: screen-reader name of the list of protocol choices in a port row's dropdown; not visible on screen" })}>
                                <ListBoxItem id="UDP" textValue="UDP">{intl.formatMessage({ id: "web.components.modules.build_module_dialog.udp", defaultMessage: "UDP", description: "Build module dialog: protocol option UDP in a port row's dropdown; technical term, usually left untranslated; keep it short" })}</ListBoxItem>
                                <ListBoxItem id="TCP" textValue="TCP">{intl.formatMessage({ id: "web.components.modules.build_module_dialog.tcp", defaultMessage: "TCP", description: "Build module dialog: protocol option TCP in a port row's dropdown; technical term, usually left untranslated; keep it short" })}</ListBoxItem>
                              </ListBox>
                            </Select.Popover>
                          </Select>
                        </div>
                        <label className="flex items-center gap-1.5 text-xs text-muted cursor-pointer select-none px-2">
                          <input
                            type="checkbox"
                            checked={!!p.advertise}
                            onChange={(e) => updatePort(idx, { advertise: e.target.checked })}
                            className="rounded border-border"
                          />{intl.formatMessage({ id: "web.components.modules.build_module_dialog.advertise", defaultMessage: "Advertise", description: "Build module dialog: checkbox label on a port row that sets whether the port is advertised" })}</label>
                        {ports.length > 1 && (
                          <Button
                            isIconOnly
                            variant="ghost"
                            size="sm"
                            onPress={() => removePort(idx)}
                            className="text-muted hover:text-danger"
                            aria-label={intl.formatMessage({ id: "web.components.modules.build_module_dialog.remove_port", defaultMessage: "Remove port", description: "Build module dialog: button that removes this port row; keep it short" })}
                          >
                            <Trash2 className="h-4 w-4" />
                          </Button>
                        )}
                      </div>
                    ))}
                  </div>
                </div>

                <div className="pt-2">
                  <label className="text-xs font-medium text-fg">{intl.formatMessage({ id: "web.components.modules.build_module_dialog.persistent_storage", defaultMessage: "Persistent Storage", description: "Build module dialog: label above the persistent storage settings, the volume size and mount path, on step 2" })}</label>
                  <div className="mt-2 grid grid-cols-2 gap-3">
                    <div>
                      <label htmlFor={storageSizeInputId} className="text-[11px] text-muted">{intl.formatMessage({ id: "web.components.modules.build_module_dialog.volume_size", defaultMessage: "Volume Size", description: "Build module dialog: label above the volume size field, the storage size requested for the module's data" })}</label>
                      <Input
                        id={storageSizeInputId}
                        value={storageSize}
                        onChange={(e) => setStorageSize(e.target.value)}
                        placeholder={intl.formatMessage({ id: "web.components.modules.build_module_dialog.e_g_10gi", defaultMessage: "e.g. 10Gi", description: "Build module dialog: example text shown inside the empty volume size field; a sample size value, not a default" })}
                        className="mt-1 text-xs"
                      />
                    </div>
                    <div>
                      <label htmlFor={storageMountPathInputId} className="text-[11px] text-muted">{intl.formatMessage({ id: "web.components.modules.build_module_dialog.mount_path", defaultMessage: "Mount Path", description: "Build module dialog: label above the mount path field, the folder inside the server where the volume is attached" })}</label>
                      <Input
                        id={storageMountPathInputId}
                        value={storageMountPath}
                        onChange={(e) => setStorageMountPath(e.target.value)}
                        placeholder={intl.formatMessage({ id: "web.components.modules.build_module_dialog.e_g_data", defaultMessage: "e.g. /data", description: "Build module dialog: example text shown inside the empty mount path field; a sample folder path, not a default" })}
                        className="mt-1 text-xs font-mono"
                      />
                    </div>
                  </div>
                </div>
              </div>
            )}

            {step === 3 && (
              <div className="grid grid-cols-12 gap-5">
                {/* Left Pane: Code Tabs */}
                <div className="col-span-7 flex flex-col rounded-lg border border-border bg-surface/30 overflow-hidden">
                  <div className="flex border-b border-border bg-surface">
                    {(["module.yaml", "template.yaml", "README.md"] as const).map((tab) => (
                      <button
                        key={tab}
                        type="button"
                        onClick={() => setActiveTab(tab)}
                        disabled={busy}
                        className={`px-4 py-2 text-xs font-medium border-r border-border transition-colors ${
                          activeTab === tab
                            ? "bg-card text-fg border-b-2 border-b-primary"
                            : "text-muted hover:text-fg"
                        }`}
                      >
                        {tab}
                      </button>
                    ))}
                  </div>
                  <div className="p-3">
                    <textarea
                      aria-label={intl.formatMessage({ id: "web.components.modules.build_module_dialog.module_definition_source", defaultMessage: "Module definition source", description: "Build module dialog: screen-reader name of the large text box holding the module definition source; not visible on screen" })}
                      value={
                        activeTab === "module.yaml"
                          ? moduleYaml
                          : activeTab === "template.yaml"
                          ? templateYaml
                          : readmeMd
                      }
                      onChange={(e) => {
                        const val = e.target.value;
                        if (activeTab === "module.yaml") {
                          setModuleYaml(val);
                          void revalidate(val, templateYaml, simMemory);
                        } else if (activeTab === "template.yaml") {
                          setTemplateYaml(val);
                          void revalidate(moduleYaml, val, simMemory);
                        } else {
                          setReadmeMd(val);
                        }
                      }}
                      className="h-80 w-full resize-none rounded bg-transparent font-mono text-xs leading-relaxed text-fg focus:outline-hidden"
                      spellCheck={false}
                    />
                  </div>
                </div>

                {/* Right Pane: Validation & Simulation */}
                <div className="col-span-5 space-y-4">
                  {/* Validation Card */}
                  <div
                    className={`rounded-lg border p-3.5 text-xs ${
                      validationResult?.clean
                        ? "border-emerald-500/40 bg-emerald-500/5 text-emerald-600 dark:text-emerald-400"
                        : "border-danger/40 bg-danger/5 text-danger"
                    }`}
                  >
                    <div className="flex items-center gap-2 font-semibold">
                      {validationResult?.clean ? (
                        <>
                          <CheckCircle2 className="h-4 w-4" />
                          <span>{intl.formatMessage({ id: "web.components.modules.build_module_dialog.module_validated_cleanly", defaultMessage: "Module Validated Cleanly", description: "Build module dialog: success heading in the validation panel, shown when the module passes validation with no errors or warnings" })}</span>
                        </>
                      ) : (
                        <>
                          <AlertCircle className="h-4 w-4" />
                          <span>
                            {validationResult?.errorCount ?? 0} {intl.formatMessage({ id: "web.components.modules.build_module_dialog.errors", defaultMessage: "errors,", description: "Build module dialog: word after the error count in the validation summary; the number shown before it is the count of errors" })} {validationResult?.warningCount ?? 0} {intl.formatMessage({ id: "web.components.modules.build_module_dialog.warnings", defaultMessage: "warnings", description: "Build module dialog: word after the warning count at the end of the validation summary; the number shown before it is the count of warnings" })}</span>
                        </>
                      )}
                    </div>
                    {validationResult?.clean ? (
                      <p className="mt-1 text-[11px] text-muted">{intl.formatMessage({ id: "web.components.modules.build_module_dialog.all_schema_constraints_dns_names_and", defaultMessage: "All schema constraints, DNS names, and image rules passed.", description: "Build module dialog: explanation under the validation result saying the module passed the schema, DNS name and image checks; shown only when there are no errors" })}</p>
                    ) : (
                      <div className="mt-2 space-y-1.5 max-h-32 overflow-y-auto">
                        {validationResult?.findings.map((f, i) => (
                          <div key={i} className="text-[11px]">
                            <span className="font-semibold uppercase text-[10px] bg-danger/20 px-1 py-0.5 rounded mr-1">
                              {f.level}
                            </span>
                            {f.message}
                          </div>
                        ))}
                      </div>
                    )}
                  </div>

                  {/* Memory Simulation Card */}
                  <div className="rounded-lg border border-border bg-surface/50 p-3.5 text-xs space-y-2.5">
                    <div className="flex items-center justify-between">
                      <span className="font-medium text-fg">{intl.formatMessage({ id: "web.components.modules.build_module_dialog.memory_simulation", defaultMessage: "Memory Simulation", description: "Build module dialog: label of the memory estimate card on step 3; the value beside it is the simulated memory" })}</span>
                      <span className="font-mono text-primary font-semibold">{simMemory}</span>
                    </div>
                    <div className="flex gap-1.5">
                      {["2Gi", "4Gi", "8Gi", "16Gi"].map((mem) => (
                        <button
                          key={mem}
                          type="button"
                          onClick={() => {
                            setSimMemory(mem);
                            void revalidate(moduleYaml, templateYaml, mem);
                          }}
                          disabled={busy}
                          className={`flex-1 rounded py-1 text-center font-mono text-[11px] border transition-colors ${
                            simMemory === mem
                              ? "border-primary bg-primary/10 text-primary font-semibold"
                              : "border-border bg-surface text-muted hover:text-fg"
                          }`}
                        >
                          {mem}
                        </button>
                      ))}
                    </div>

                    {previewResult && (
                      <div className="mt-2 rounded bg-card p-2.5 border border-border space-y-1 font-mono text-[11px]">
                        {Object.entries(previewResult.computedConfig).map(([k, v]) => (
                          <div key={k} className="flex justify-between">
                            <span className="text-muted">{intl.formatMessage({ id: "web.components.modules.build_module_dialog.config_key_label", defaultMessage: "{key}:", description: "Build module dialog: label of a computed config row; {key} is the config key" }, { key: k })}</span>
                            <span className="text-fg font-semibold">{v}</span>
                          </div>
                        ))}
                        {previewResult.effectiveEnv
                          .filter((e) => e.name === "MAX_MEMORY" || e.name === "MEMORY")
                          .map((e) => (
                            <div key={e.name} className="flex justify-between text-emerald-500">
                              <span>{intl.formatMessage({ id: "web.components.modules.build_module_dialog.env_name_label", defaultMessage: "{name}:", description: "Build module dialog: label of an environment row; {name} is the variable name" }, { name: e.name })}</span>
                              <span>{e.value}</span>
                            </div>
                          ))}
                      </div>
                    )}
                  </div>

                  {/* Target upload destination */}
                  {sources.length > 0 && (
                    <div className="text-xs">
                      <label className="text-muted text-[11px]">{intl.formatMessage({ id: "web.components.modules.build_module_dialog.cluster_destination_source", defaultMessage: "Cluster destination source", description: "Build module dialog: label above the dropdown that picks where the module is uploaded in the cluster; shown only when sources exist" })}</label>
                      <div className="mt-1">
                        <Select
                          value={targetSource}
                          onChange={(v) => setTargetSource((v ?? targetSource) as string)}
                          className="w-full"
                          aria-label={intl.formatMessage({ id: "web.components.modules.build_module_dialog.cluster_destination_source", defaultMessage: "Cluster destination source", description: "Build module dialog: label above the dropdown that picks where the module is uploaded in the cluster; shown only when sources exist" })}
                        >
                          <Select.Trigger>
                            <Select.Value />
                            <Select.Indicator className="ml-auto h-4 w-4" />
                          </Select.Trigger>
                          <Select.Popover>
                            <ListBox aria-label={intl.formatMessage({ id: "web.components.modules.build_module_dialog.source_options", defaultMessage: "Source options", description: "Build module dialog: screen-reader name of the list of upload source choices in the cluster destination dropdown; not visible on screen" })}>
                              {sources.map((s) => (
                                <ListBoxItem key={s} id={s} textValue={s}>
                                  {s}
                                </ListBoxItem>
                              ))}
                            </ListBox>
                          </Select.Popover>
                        </Select>
                      </div>
                    </div>
                  )}
                </div>
              </div>
            )}
                </div>
              </div>
            </ModalBody>

            <ModalFooter className="flex items-center justify-between">
              {step > 1 ? (
                <Button
                  variant="outline"
                  size="sm"
                  onPress={() => setStep((s) => (s - 1) as 1 | 2)}
                  isDisabled={busy}
                >
                  <ArrowLeft className="mr-1.5 h-3.5 w-3.5" />{intl.formatMessage({ id: "web.components.modules.build_module_dialog.back", defaultMessage: "Back", description: "Build module dialog: button that returns to the previous step; keep it short" })}</Button>
              ) : (
                <span className="text-xs text-muted">{intl.formatMessage({ id: "web.components.modules.build_module_dialog.step_1_of_3", defaultMessage: "Step 1 of 3", description: "Build module dialog: footer text on step 1 showing that this is the first of the three wizard steps" })}</span>
              )}

              <div className="flex items-center gap-2.5">
                <Button
                  variant="ghost"
                  size="sm"
                  onPress={() => onOpenChange(false)}
                  isDisabled={busy}
                >{intl.formatMessage({ id: "web.components.modules.build_module_dialog.cancel", defaultMessage: "Cancel", description: "Build module dialog: button that closes the wizard without creating the module; keep it short" })}</Button>

                {step === 1 && (
                  <Button
                    size="sm"
                    onPress={goToStep2}
                    isDisabled={!isDnsValid || busy || archetypesLoading || !!archetypesError || !image}
                  >{intl.formatMessage({ id: "web.components.modules.build_module_dialog.continue_to_container_ports", defaultMessage: "Continue to Container & Ports", description: "Build module dialog: button on step 1 that moves on to the container and ports step; keep it short" })}<ChevronRight className="ml-1 h-3.5 w-3.5" />
                  </Button>
                )}

                {step === 2 && (
                  <Button size="sm" onPress={goToStep3} isDisabled={busy}>
                    {busy && <Loader2 className="mr-1.5 h-3.5 w-3.5 animate-spin" />}{intl.formatMessage({ id: "web.components.modules.build_module_dialog.continue_to_review_export", defaultMessage: "Continue to Review & Export", description: "Build module dialog: button on step 2 that moves on to the review and export step; keep it short" })}<ChevronRight className="ml-1 h-3.5 w-3.5" />
                  </Button>
                )}

                {step === 3 && (
                  <>
                    <Button
                      variant="outline"
                      size="sm"
                      onPress={handleDownloadArchive}
                      isDisabled={busy}
                    >
                      {busy ? (
                        <Loader2 className="mr-1.5 h-3.5 w-3.5 animate-spin" />
                      ) : (
                        <Download className="mr-1.5 h-3.5 w-3.5" />
                      )}{intl.formatMessage({ id: "web.components.modules.build_module_dialog.download_tar_gz", defaultMessage: "Download .tar.gz", description: "Build module dialog: button on step 3 that downloads the built module as an archive; keep the file extension as written; keep it short" })}</Button>
                    {sources.length > 0 && (
                      <Button
                        size="sm"
                        onPress={handleInstallToCluster}
                        isDisabled={busy || !targetSource}
                      >
                        {busy ? (
                          <Loader2 className="mr-1.5 h-3.5 w-3.5 animate-spin" />
                        ) : (
                          <UploadCloud className="mr-1.5 h-3.5 w-3.5" />
                        )}{intl.formatMessage({ id: "web.components.modules.build_module_dialog.install_to_cluster", defaultMessage: "Install to Cluster", description: "Build module dialog: button on step 3 that installs the built module into the chosen cluster source; keep it short" })}</Button>
                    )}
                  </>
                )}
              </div>
            </ModalFooter>
          </ModalDialog>
        </ModalContainer>
      </ModalBackdrop>
    </Modal>
  );
}
