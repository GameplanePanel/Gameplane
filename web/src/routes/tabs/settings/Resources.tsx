import { Input } from "@heroui/react";
import type { ResourceRequirements } from "@/types";
import { isValidQuantity } from "@/lib/validation";
import { ResourceInput } from "@/components/ui/ResourceInput";
import { Field } from "./Field";
import type { SectionProps } from "./types";
import { useOptionalIntl } from "@/lib/i18n/format";


export function ResourcesSection({ draft, onChange }: SectionProps) {
  const intl = useOptionalIntl();
  const res = draft.spec.resources ?? {};

  const setResources = (next: ResourceRequirements) => {
    const cleaned = pruneResources(next);
    onChange({
      ...draft,
      spec: { ...draft.spec, resources: cleaned },
    });
  };

  const storage = draft.spec.storage ?? {};

  const setStorage = (next: typeof storage) => {
    const cleaned: typeof storage = {};
    if (next.size) cleaned.size = next.size;
    if (next.storageClassName) cleaned.storageClassName = next.storageClassName;
    if (next.mountPath) cleaned.mountPath = next.mountPath;
    onChange({
      ...draft,
      spec: {
        ...draft.spec,
        storage: Object.keys(cleaned).length ? cleaned : undefined,
      },
    });
  };

  const sizeValid = !storage.size || isValidQuantity(storage.size);

  const handleCpuChange = (qty: string) => {
    setResources({
      ...res,
      requests: { ...res.requests, cpu: qty },
      limits: { ...res.limits, cpu: qty },
    });
  };

  const handleMemoryChange = (qty: string) => {
    setResources({
      ...res,
      requests: { ...res.requests, memory: qty },
      limits: { ...res.limits, memory: qty },
    });
  };

  return (
    <div className="space-y-6">
      <Field
        label={intl.formatMessage({ id: "web.routes.tabs.settings.resources.cpu_cores", defaultMessage: "CPU cores", description: "Resources: field label for the number of CPU cores reserved for the server; CPU is a technical term, usually left untranslated" })}
        hint={intl.formatMessage({ id: "web.routes.tabs.settings.resources.sets_requests_limits_to_the_same_value", defaultMessage: "Sets requests=limits to the same value (Guaranteed QoS).", description: "Resources: hint under the CPU cores field saying the request and limit are set to the same value, which gives the Guaranteed QoS class; QoS is a technical term" })}
      >
        <ResourceInput
          kind="cpu"
          value={res.limits?.cpu ?? res.requests?.cpu ?? "2"}
          onChange={handleCpuChange}
        />
      </Field>

      <Field label={intl.formatMessage({ id: "web.routes.tabs.settings.resources.memory_gib", defaultMessage: "Memory (GiB)", description: "Resources: field label for the memory amount in GiB reserved for the server" })} hint={intl.formatMessage({ id: "web.routes.tabs.settings.resources.sets_requests_limits_to_the_same_value_2", defaultMessage: "Sets requests=limits to the same value.", description: "Resources: hint under the memory field saying the request and limit are set to the same value" })}>
        <ResourceInput
          kind="memory"
          value={res.limits?.memory ?? res.requests?.memory ?? "4Gi"}
          onChange={handleMemoryChange}
        />
      </Field>

      <Field
        label={intl.formatMessage({ id: "web.routes.tabs.settings.resources.storage_size", defaultMessage: "Storage size", description: "Resources: field label for the size of the server's persistent storage; the accessible name of its box is the same" })}
        hint={
          <>
            <div>{intl.formatMessage({ id: "web.routes.tabs.settings.resources.k8s_quantity_e_g_10gi_200gi", defaultMessage: "K8s quantity (e.g. 10Gi, 200Gi).", description: "Resources: hint under the storage size field saying the value is a Kubernetes quantity such as 10Gi or 200Gi; Kubernetes is a technical term" })}</div>
            <div className="pt-1">{intl.formatMessage(
              { id: "web.routes.tabs.settings.resources.resizing_requires_a_storageclass_with", defaultMessage: "Resizing requires a StorageClass with {setting}.", description: "Resources: hint under the storage size field saying resizing needs a StorageClass that allows volume expansion; {setting} is the allowVolumeExpansion: true field, shown in monospace" },
              { setting: <span className="font-mono">{intl.formatMessage({ id: "web.routes.tabs.settings.resources.allowvolumeexpansion_true", defaultMessage: "allowVolumeExpansion: true", description: "Resources: Kubernetes StorageClass field name shown in the storage size hint" })}</span> },
            )}
            </div>
          </>
        }
      >
        <Input
          type="text"
          value={storage.size ?? ""}
          onChange={(e) => setStorage({ ...storage, size: e.target.value || undefined })}
          placeholder={intl.formatMessage({ id: "web.routes.tabs.settings.resources.10gi", defaultMessage: "10Gi", description: "Resources: example placeholder in the storage size box; an example value, not a default" })}
          className={sizeValid ? "" : "border-danger focus:border-danger focus:ring-danger"}
          aria-label={intl.formatMessage({ id: "web.routes.tabs.settings.resources.storage_size", defaultMessage: "Storage size", description: "Resources: field label for the size of the server's persistent storage; the accessible name of its box is the same" })}
        />
        {!sizeValid && (
          <div className="pt-1 text-xs text-danger">{intl.formatMessage({ id: "web.routes.tabs.settings.resources.invalid_quantity_e_g_10gi", defaultMessage: "Invalid quantity (e.g. \"10Gi\").", description: "Resources: validation error under the storage size box when the value is not a valid quantity such as 10Gi" })}</div>
        )}
      </Field>

      <Field label={intl.formatMessage({ id: "web.routes.tabs.settings.resources.storageclass", defaultMessage: "StorageClass", description: "Resources: field label for the optional storage class that provides the server's storage; the hint below says blank uses the cluster default" })} hint={intl.formatMessage({ id: "web.routes.tabs.settings.resources.leave_blank_to_use_the_cluster_default", defaultMessage: "Leave blank to use the cluster default.", description: "Resources: hint under the StorageClass field saying a blank value uses the cluster default storage class" })}>
        <Input
          type="text"
          value={storage.storageClassName ?? ""}
          onChange={(e) =>
            setStorage({ ...storage, storageClassName: e.target.value || undefined })
          }
          placeholder={intl.formatMessage({ id: "web.routes.tabs.settings.resources.fast_ssd", defaultMessage: "fast-ssd", description: "Resources: example placeholder in the StorageClass box; an example class name, not a default" })}
          aria-label={intl.formatMessage({ id: "web.routes.tabs.settings.resources.storageclass", defaultMessage: "StorageClass", description: "Resources: field label for the optional storage class that provides the server's storage; the hint below says blank uses the cluster default" })}
        />
      </Field>
    </div>
  );
}

function pruneResources(r: ResourceRequirements): ResourceRequirements | undefined {
  const requests = pruneRecord(r.requests);
  const limits = pruneRecord(r.limits);
  if (!requests && !limits) return undefined;
  const out: ResourceRequirements = {};
  if (requests) out.requests = requests;
  if (limits) out.limits = limits;
  return out;
}

function pruneRecord(r: Partial<Record<"cpu" | "memory", string>> | undefined) {
  if (!r) return undefined;
  const out: Partial<Record<"cpu" | "memory", string>> = {};
  if (r.cpu) out.cpu = r.cpu;
  if (r.memory) out.memory = r.memory;
  return Object.keys(out).length ? out : undefined;
}
