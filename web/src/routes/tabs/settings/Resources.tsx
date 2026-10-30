import { useState } from "react";
import { Input, Switch } from "@heroui/react";
import type { ResourceRequirements } from "@/types";
import { isValidQuantity } from "@/lib/validation";
import { cpuQuantityCores, memQuantityBytes } from "@/lib/quantity";
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

  // What the fields show: the limit (falling back to the request, then the
  // dashboard default) and the request (falling back to the limit, as
  // Kubernetes defaults a missing request to its limit).
  const cpuLimit = res.limits?.cpu ?? res.requests?.cpu ?? "2";
  const memoryLimit = res.limits?.memory ?? res.requests?.memory ?? "4Gi";
  const cpuRequest = res.requests?.cpu ?? cpuLimit;
  const memoryRequest = res.requests?.memory ?? memoryLimit;

  // Reserving less than the limit (Burstable QoS) is not a stored field: it is
  // on whenever the saved requests sit below the limits, or once the user
  // switches it on to start lowering them.
  const requestsBelowLimits =
    cpuQuantityCores(cpuRequest) !== cpuQuantityCores(cpuLimit) ||
    memQuantityBytes(memoryRequest) !== memQuantityBytes(memoryLimit);
  const [reserveLessChosen, setReserveLessChosen] = useState(false);
  const reserveLess = requestsBelowLimits || reserveLessChosen;

  const handleReserveLessChange = (on: boolean) => {
    setReserveLessChosen(on);
    // Switching off puts the requests back on the limits (Guaranteed QoS).
    if (!on && requestsBelowLimits) {
      setResources({
        ...res,
        requests: { cpu: cpuLimit, memory: memoryLimit },
        limits: { cpu: cpuLimit, memory: memoryLimit },
      });
    }
  };

  // With reserveLess on, requests and limits are written together and in full:
  // spec.resources replaces the template's resources wholesale, so a half-set
  // pair would drop the other values.
  const setRequestsAndLimits = (
    requests: { cpu: string; memory: string },
    limits: { cpu: string; memory: string },
  ) => {
    setResources({ ...res, requests, limits });
  };

  // Off: the field sets the request and the limit to the same value. On: it
  // sets the limit, and a reservation above the new limit comes down with it,
  // since Kubernetes rejects a request above its limit.
  const handleCpuChange = (qty: string) => {
    if (reserveLess) {
      setRequestsAndLimits(
        {
          cpu: cpuQuantityCores(cpuRequest) > cpuQuantityCores(qty) ? qty : cpuRequest,
          memory: memoryRequest,
        },
        { cpu: qty, memory: memoryLimit },
      );
      return;
    }
    setResources({
      ...res,
      requests: { ...res.requests, cpu: qty },
      limits: { ...res.limits, cpu: qty },
    });
  };

  const handleMemoryChange = (qty: string) => {
    if (reserveLess) {
      setRequestsAndLimits(
        {
          cpu: cpuRequest,
          memory: memQuantityBytes(memoryRequest) > memQuantityBytes(qty) ? qty : memoryRequest,
        },
        { cpu: cpuLimit, memory: qty },
      );
      return;
    }
    setResources({
      ...res,
      requests: { ...res.requests, memory: qty },
      limits: { ...res.limits, memory: qty },
    });
  };

  const handleCpuRequestChange = (qty: string) => {
    setRequestsAndLimits({ cpu: qty, memory: memoryRequest }, { cpu: cpuLimit, memory: memoryLimit });
  };

  const handleMemoryRequestChange = (qty: string) => {
    setRequestsAndLimits({ cpu: cpuRequest, memory: qty }, { cpu: cpuLimit, memory: memoryLimit });
  };

  const reserveLessLabel = intl.formatMessage({ id: "web.routes.tabs.settings.resources.reserve_less_than_the_limit", defaultMessage: "Reserve less than the limit", description: "Resources: field label for the switch that lets the CPU and memory reserved for the server (Kubernetes requests) be lower than its limits; the same text is the switch's accessible name" });
  const reservedCpuLabel = intl.formatMessage({ id: "web.routes.tabs.settings.resources.reserved_cpu", defaultMessage: "Reserved CPU", description: "Resources: field label for the CPU reserved for the server (the Kubernetes request), shown when the reservation may be lower than the limit; the same text is the accessible name of its slider; CPU is a technical term, usually left untranslated" });
  const reservedMemoryLabel = intl.formatMessage({ id: "web.routes.tabs.settings.resources.reserved_memory_gib", defaultMessage: "Reserved memory (GiB)", description: "Resources: field label for the memory in GiB reserved for the server (the Kubernetes request), shown when the reservation may be lower than the limit; the same text is the accessible name of its slider" });

  return (
    <div className="space-y-6">
      <Field
        label={intl.formatMessage({ id: "web.routes.tabs.settings.resources.cpu_cores", defaultMessage: "CPU cores", description: "Resources: field label for the number of CPU cores reserved for the server; CPU is a technical term, usually left untranslated" })}
        hint={
          reserveLess
            ? intl.formatMessage({ id: "web.routes.tabs.settings.resources.limit_the_most_cpu_this_server_can_use", defaultMessage: "Limit: the most CPU this server can use.", description: "Resources: hint under the CPU cores field when the reservation may be lower, saying the value is the limit, the most CPU the server can use; CPU is a technical term" })
            : intl.formatMessage({ id: "web.routes.tabs.settings.resources.sets_requests_limits_to_the_same_value", defaultMessage: "Sets requests=limits to the same value (Guaranteed QoS).", description: "Resources: hint under the CPU cores field saying the request and limit are set to the same value, which gives the Guaranteed QoS class; QoS is a technical term" })
        }
      >
        <ResourceInput
          kind="cpu"
          value={cpuLimit}
          onChange={handleCpuChange}
        />
      </Field>

      <Field
        label={intl.formatMessage({ id: "web.routes.tabs.settings.resources.memory_gib", defaultMessage: "Memory (GiB)", description: "Resources: field label for the memory amount in GiB reserved for the server" })}
        hint={
          reserveLess
            ? intl.formatMessage({ id: "web.routes.tabs.settings.resources.limit_the_most_memory_this_server_can_use", defaultMessage: "Limit: the most memory this server can use.", description: "Resources: hint under the memory field when the reservation may be lower, saying the value is the limit, the most memory the server can use" })
            : intl.formatMessage({ id: "web.routes.tabs.settings.resources.sets_requests_limits_to_the_same_value_2", defaultMessage: "Sets requests=limits to the same value.", description: "Resources: hint under the memory field saying the request and limit are set to the same value" })
        }
      >
        <ResourceInput
          kind="memory"
          value={memoryLimit}
          onChange={handleMemoryChange}
        />
      </Field>

      <Field
        label={reserveLessLabel}
        hint={intl.formatMessage({ id: "web.routes.tabs.settings.resources.off_the_server_reserves_its_full_cpu", defaultMessage: "Off: the server reserves its full CPU and memory (Guaranteed QoS). On: it reserves less, so more servers fit on a node, and uses more up to its limit when the node has room (Burstable QoS).", description: "Resources: hint under the reserve-less switch explaining what off and on do; Guaranteed QoS and Burstable QoS are Kubernetes technical terms, CPU is a technical term" })}
      >
        <div className="pt-1">
          <Switch isSelected={reserveLess} onChange={handleReserveLessChange} aria-label={reserveLessLabel}>
            <Switch.Content>
              <Switch.Control>
                <Switch.Thumb />
              </Switch.Control>
            </Switch.Content>
          </Switch>
        </div>
      </Field>

      {reserveLess && (
        <>
          <Field
            label={reservedCpuLabel}
            hint={intl.formatMessage({ id: "web.routes.tabs.settings.resources.counted_when_the_server_is_placed_on_a", defaultMessage: "Counted when the server is placed on a node. 0 reserves nothing.", description: "Resources: hint under the reserved CPU field saying only this amount counts when the server is placed on a node, and that 0 reserves nothing" })}
          >
            <ResourceInput
              kind="cpu"
              label={reservedCpuLabel}
              value={cpuRequest}
              onChange={handleCpuRequestChange}
              min={0}
              max={cpuQuantityCores(cpuLimit)}
            />
          </Field>

          <Field
            label={reservedMemoryLabel}
            hint={intl.formatMessage({ id: "web.routes.tabs.settings.resources.when_a_node_runs_out_of_memory_servers", defaultMessage: "When a node runs out of memory, servers above their reservation are stopped first. Keep it close to real use.", description: "Resources: hint under the reserved memory field warning that servers using more memory than they reserved are stopped first when a node runs out of memory" })}
          >
            <ResourceInput
              kind="memory"
              label={reservedMemoryLabel}
              value={memoryRequest}
              onChange={handleMemoryRequestChange}
              min={0}
              max={memQuantityBytes(memoryLimit) / 1024 ** 3}
            />
          </Field>
        </>
      )}

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
