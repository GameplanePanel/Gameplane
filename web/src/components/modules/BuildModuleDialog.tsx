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

interface BuildModuleDialogProps {
  cluster?: string;
  open: boolean;
  onOpenChange: (open: boolean) => void;
  sources: string[];
  onInstalled?: () => Promise<void> | void;
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
      setError("Module name must be a valid lowercase DNS-1123 label.");
      return;
    }
    if (!image) {
      setError("Please select an archetype or provide a container image.");
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
              message: err instanceof APIError ? err.body || err.message : (err as Error).message || "Validation failed",
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
      setError("Please select an upload source destination.");
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
                <ModalHeading className="text-lg font-semibold">Create game module</ModalHeading>
                <Description className="text-xs text-muted">
                  Step {step} of 3 ·{" "}
                  {step === 1
                    ? "Preset & Metadata"
                    : step === 2
                    ? "Container & Ports"
                    : "Review, Preview & Export"}
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
                      {step > 1 ? "✓" : "1"}
                    </span>
                    <span className={step === 1 ? "font-medium text-fg" : "text-muted"}>
                      Preset & Metadata
                    </span>
                  </div>
                  <span className="text-muted">·</span>
                  <div className="flex items-center gap-2">
                    <span
                      className={`flex h-5 w-5 items-center justify-center rounded-full text-[11px] font-semibold ${
                        step === 2
                          ? "bg-primary text-primary-fg"
                          : "bg-surface text-fg border border-border"
                      }`}
                    >
                      {step > 2 ? "✓" : "2"}
                    </span>
                    <span className={step === 2 ? "font-medium text-fg" : "text-muted"}>
                      Container & Ports
                    </span>
                  </div>
                  <span className="text-muted">·</span>
                  <div className="flex items-center gap-2">
                    <span
                      className={`flex h-5 w-5 items-center justify-center rounded-full text-[11px] font-semibold ${
                        step === 3
                          ? "bg-primary text-primary-fg"
                          : "bg-surface text-fg border border-border"
                      }`}
                    >
                      3
                    </span>
                    <span className={step === 3 ? "font-medium text-fg" : "text-muted"}>
                      Review & Export
                    </span>
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
                  <label className="text-xs font-medium text-fg">Archetype preset</label>

                  {archetypesLoading && (
                    <div className="mt-2 flex items-center justify-center gap-2 py-8 text-muted">
                      <Loader2 className="h-4 w-4 animate-spin" />
                      <span className="text-xs">Loading archetypes...</span>
                    </div>
                  )}

                  {archetypesError && (
                    <div className="flex items-center gap-2 rounded-md bg-danger/10 p-3 text-xs text-danger mt-2">
                      <AlertCircle className="h-4 w-4 shrink-0" />
                      <span>Failed to load archetypes</span>
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
                      <label htmlFor={nameInputId} className="text-xs font-medium text-fg">
                        Module Name
                      </label>
                      {name && (
                        <span
                          className={`text-[11px] ${
                            isDnsValid ? "text-emerald-500 font-medium" : "text-danger"
                          }`}
                        >
                          {isDnsValid ? "✓ Valid DNS-1123 label" : "Must be lowercase alphanumeric with hyphens"}
                        </span>
                      )}
                    </div>
                    <Input
                      id={nameInputId}
                      value={name}
                      onChange={(e) => setName(e.target.value)}
                      placeholder="e.g. cs2-match"
                      className="mt-1"
                    />
                  </div>

                  <div>
                    <label htmlFor={displayNameInputId} className="text-xs font-medium text-fg">
                      Display Name
                    </label>
                    <Input
                      id={displayNameInputId}
                      value={displayName}
                      onChange={(e) => setDisplayName(e.target.value)}
                      placeholder="e.g. Counter-Strike 2"
                      className="mt-1"
                    />
                  </div>

                  <div>
                    <label htmlFor={summaryInputId} className="text-xs font-medium text-fg">
                      Summary
                    </label>
                    <Input
                      id={summaryInputId}
                      value={summary}
                      onChange={(e) => setSummary(e.target.value)}
                      placeholder="Short description of the game server"
                      className="mt-1"
                    />
                  </div>

                  <div>
                    <div className="flex items-center justify-between">
                      <label className="text-xs font-medium text-fg">Categories</label>
                      <span className="text-[11px] text-muted">Recommended</span>
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
                            {cat} {active && "×"}
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
                        placeholder="Add custom tag..."
                        className="text-xs"
                        aria-label="Custom tag input"
                      />
                      <Button
                        size="sm"
                        onPress={() => addCustomTag(customTagInput)}
                        isDisabled={busy || !customTagInput.trim()}
                        className="h-10"
                        aria-label="Add tag"
                      >
                        <Plus className="h-3.5 w-3.5" />
                      </Button>
                    </div>

                    {/* Selected tags (custom only) */}
                    {categories.filter((c) => !CANONICAL_CATEGORIES.includes(c)).length > 0 && (
                      <>
                        <div className="text-xs font-medium text-fg mt-3">Selected Tags</div>
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
                                {cat} ×
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
                    <label htmlFor={imageInputId} className="text-xs font-medium text-fg">
                      Container Image
                    </label>
                    {image && (
                      <span
                        className={`text-[11px] ${
                          isImagePinned ? "text-emerald-500 font-medium" : "text-amber-500"
                        }`}
                      >
                        {isImagePinned
                          ? "✓ Pinned by digest (@sha256:...)"
                          : "⚠ Unpinned image (recommend @sha256:...)"}
                      </span>
                    )}
                  </div>
                  <Input
                    id={imageInputId}
                    value={image}
                    onChange={(e) => setImage(e.target.value)}
                    placeholder="e.g. ghcr.io/valgul/game:v1.0@sha256:..."
                    className="mt-1 font-mono text-xs"
                  />
                </div>

                <div>
                  <div className="flex items-center justify-between">
                    <label className="text-xs font-medium text-fg">Port Mappings</label>
                    <Button variant="outline" size="sm" onPress={addPort} className="h-7 text-xs">
                      <Plus className="mr-1 h-3.5 w-3.5" />
                      Add Port
                    </Button>
                  </div>

                  <div className="mt-2 space-y-2">
                    {ports.map((p, idx) => (
                      <div key={idx} className="flex items-center gap-2">
                        <Input
                          value={p.name}
                          onChange={(e) => updatePort(idx, { name: e.target.value })}
                          placeholder="name"
                          className="w-36 text-xs"
                        />
                        <Input
                          type="number"
                          value={p.containerPort}
                          onChange={(e) =>
                            updatePort(idx, { containerPort: parseInt(e.target.value, 10) || 0 })
                          }
                          placeholder="port"
                          className="w-28 text-xs font-mono"
                        />
                        <div className="w-24">
                          <Select
                            value={p.protocol}
                            onChange={(v) => updatePort(idx, { protocol: (v ?? p.protocol) as string })}
                            className="w-24"
                            aria-label="Protocol"
                          >
                            <Select.Trigger>
                              <Select.Value />
                              <Select.Indicator className="ml-auto h-4 w-4" />
                            </Select.Trigger>
                            <Select.Popover>
                              <ListBox aria-label="Protocol options">
                                <ListBoxItem id="UDP" textValue="UDP">UDP</ListBoxItem>
                                <ListBoxItem id="TCP" textValue="TCP">TCP</ListBoxItem>
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
                          />
                          Advertise
                        </label>
                        {ports.length > 1 && (
                          <Button
                            isIconOnly
                            variant="ghost"
                            size="sm"
                            onPress={() => removePort(idx)}
                            className="text-muted hover:text-danger"
                            aria-label="Remove port"
                          >
                            <Trash2 className="h-4 w-4" />
                          </Button>
                        )}
                      </div>
                    ))}
                  </div>
                </div>

                <div className="pt-2">
                  <label className="text-xs font-medium text-fg">Persistent Storage</label>
                  <div className="mt-2 grid grid-cols-2 gap-3">
                    <div>
                      <label htmlFor={storageSizeInputId} className="text-[11px] text-muted">
                        Volume Size
                      </label>
                      <Input
                        id={storageSizeInputId}
                        value={storageSize}
                        onChange={(e) => setStorageSize(e.target.value)}
                        placeholder="e.g. 10Gi"
                        className="mt-1 text-xs"
                      />
                    </div>
                    <div>
                      <label htmlFor={storageMountPathInputId} className="text-[11px] text-muted">
                        Mount Path
                      </label>
                      <Input
                        id={storageMountPathInputId}
                        value={storageMountPath}
                        onChange={(e) => setStorageMountPath(e.target.value)}
                        placeholder="e.g. /data"
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
                      aria-label="Module definition source"
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
                          <span>Module Validated Cleanly</span>
                        </>
                      ) : (
                        <>
                          <AlertCircle className="h-4 w-4" />
                          <span>
                            {validationResult?.errorCount ?? 0} errors,{" "}
                            {validationResult?.warningCount ?? 0} warnings
                          </span>
                        </>
                      )}
                    </div>
                    {validationResult?.clean ? (
                      <p className="mt-1 text-[11px] text-muted">
                        All schema constraints, DNS names, and image rules passed.
                      </p>
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
                      <span className="font-medium text-fg">Memory Simulation</span>
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
                            <span className="text-muted">{k}:</span>
                            <span className="text-fg font-semibold">{v}</span>
                          </div>
                        ))}
                        {previewResult.effectiveEnv
                          .filter((e) => e.name === "MAX_MEMORY" || e.name === "MEMORY")
                          .map((e) => (
                            <div key={e.name} className="flex justify-between text-emerald-500">
                              <span>{e.name}:</span>
                              <span>{e.value}</span>
                            </div>
                          ))}
                      </div>
                    )}
                  </div>

                  {/* Target upload destination */}
                  {sources.length > 0 && (
                    <div className="text-xs">
                      <label className="text-muted text-[11px]">Cluster destination source</label>
                      <div className="mt-1">
                        <Select
                          value={targetSource}
                          onChange={(v) => setTargetSource((v ?? targetSource) as string)}
                          className="w-full"
                          aria-label="Cluster destination source"
                        >
                          <Select.Trigger>
                            <Select.Value />
                            <Select.Indicator className="ml-auto h-4 w-4" />
                          </Select.Trigger>
                          <Select.Popover>
                            <ListBox aria-label="Source options">
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
                  <ArrowLeft className="mr-1.5 h-3.5 w-3.5" />
                  Back
                </Button>
              ) : (
                <span className="text-xs text-muted">Step 1 of 3</span>
              )}

              <div className="flex items-center gap-2.5">
                <Button
                  variant="ghost"
                  size="sm"
                  onPress={() => onOpenChange(false)}
                  isDisabled={busy}
                >
                  Cancel
                </Button>

                {step === 1 && (
                  <Button
                    size="sm"
                    onPress={goToStep2}
                    isDisabled={!isDnsValid || busy || archetypesLoading || !!archetypesError || !image}
                  >
                    Continue to Container & Ports
                    <ChevronRight className="ml-1 h-3.5 w-3.5" />
                  </Button>
                )}

                {step === 2 && (
                  <Button size="sm" onPress={goToStep3} isDisabled={busy}>
                    {busy && <Loader2 className="mr-1.5 h-3.5 w-3.5 animate-spin" />}
                    Continue to Review & Export
                    <ChevronRight className="ml-1 h-3.5 w-3.5" />
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
                      )}
                      Download .tar.gz
                    </Button>
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
                        )}
                        Install to Cluster
                      </Button>
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
