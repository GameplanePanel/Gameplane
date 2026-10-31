import { useMemo, useState } from "react";
import { useNavigate, useSearch } from "@tanstack/react-router";
import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { AlertCircle, ArrowLeft, ArrowRight, Check, ExternalLink, Loader2, Megaphone, X } from "lucide-react";
import { Button, Input, Alert, Select, Label, ListBox, ListBoxItem, Switch } from "@heroui/react";
import { GameIcon } from "@/components/ui/GameIcon";
import { ResourceInput } from "@/components/ui/ResourceInput";
import { PortOverridesEditor } from "@/components/server/PortOverridesEditor";
import { ConfigFields } from "@/components/server/ConfigFields";
import { assignGameCodesForTemplates } from "@/lib/gameIcon";
import { useFleetPlacements, type FleetPlacement } from "@/lib/fleet";
import { serverLink } from "@/lib/resourceTarget";
import { LoadingCard } from "@/components/ui/LoadingCard";
import { ErrorCard } from "@/components/ui/ErrorCard";
import { APIError } from "@/lib/api";
import { errorTextIntl } from "@/lib/errors";
import { createResourceClient, type ServerCreate } from "@/lib/endpoints";
import {
  defaultVersionId,
  isValidK8sName,
  isValidQuantity,
  isValidVersion,
  maskPasswordConfig,
  validateConfig,
} from "@/lib/validation";
import { parseCpuQuantity, cpuCores, parseMemQuantity, memBytes, cpuQuantityCores, memQuantityBytes } from "@/lib/quantity";
import { cn, ignoreRejection } from "@/lib/utils";
import { categoryFilters, categoryLabel, matchesCategory, resolveCategories } from "@/lib/games";
import type { GameTemplate, PortOverride, GameServerTunnel } from "@/types";
import { useOptionalIntl } from "@/lib/i18n/format";
import { moduleIds, useModuleText, useModuleTexts, type ModuleText } from "@/lib/i18n/moduleText";
import { defineMessages, type IntlShape, type MessageDescriptor } from "react-intl";


// Wizard steps are derived per-template: the "version" step only appears when
// the template declares a version catalog (spec.versions). Templates without
// one keep the original 4-step flow.
type StepKey = "template" | "version" | "configure" | "network" | "review";
const STEP_TITLES: Record<StepKey, MessageDescriptor> = defineMessages({
  template: { id: "web.routes.create_server.step_template", defaultMessage: "Template", description: "Create server: name of the template wizard step in the step bar" },
  version: { id: "web.routes.create_server.step_version", defaultMessage: "Version", description: "Create server: name of the version wizard step in the step bar" },
  configure: { id: "web.routes.create_server.step_configure", defaultMessage: "Configure", description: "Create server: name of the configure wizard step in the step bar" },
  network: { id: "web.routes.create_server.step_network", defaultMessage: "Network", description: "Create server: name of the network wizard step in the step bar" },
  review: { id: "web.routes.create_server.step_review", defaultMessage: "Review", description: "Create server: name of the review wizard step in the step bar" },
});

const DOCS_CREATE_SERVER_URL = "https://gameplanepanel.github.io/website/docs/getting-started";

function stepsFor(template: GameTemplate | null): StepKey[] {
  const base: StepKey[] = ["template", "configure", "network", "review"];
  if ((template?.spec.versions?.length ?? 0) > 0) base.splice(1, 0, "version");
  return base;
}

interface WizardState {
  name: string;
  description: string;
  template: GameTemplate | null;
  version: string;
  config: Record<string, string>;
  cpuLimit: string;
  memoryLimit: string;
  // Burstable QoS opt-in: reserve (request) less than the limits.
  reserveLess: boolean;
  cpuRequest: string;
  memoryRequest: string;
  storageSize: string;
  nodePlacement: "auto" | "pin" | "gpu";
  expose: "ClusterIP" | "NodePort" | "LoadBalancer";
  hostname: string;
  sourceRanges: string;
  portOverrides: PortOverride[];
  tunnelEnabled: boolean;
  tunnelProvider: "frp" | "tailscale" | "playit";
  tunnelCredentialsValue: string;
  tunnelCredentialsSecretName: string;
  frpServerAddr: string;
  frpServerPort: string;
  frpRemotePortMappings: Array<{ name: string; remotePort: number }>;
  tailscaleHostname: string;
  tailscaleTags: string;
  playitTunnelName: string;
  addressPool: string;
  requestedAddress: string;
}

const initial: WizardState = {
  name: "", description: "",
  template: null, version: "", config: {},
  cpuLimit: "2", memoryLimit: "4Gi",
  reserveLess: false, cpuRequest: "2", memoryRequest: "4Gi",
  storageSize: "20Gi", nodePlacement: "auto",
  expose: "NodePort", hostname: "", sourceRanges: "",
  portOverrides: [],
  tunnelEnabled: false, tunnelProvider: "frp",
  tunnelCredentialsValue: "",
  tunnelCredentialsSecretName: "",
  frpServerAddr: "",
  frpServerPort: "7000",
  frpRemotePortMappings: [],
  tailscaleHostname: "",
  tailscaleTags: "",
  playitTunnelName: "",
  addressPool: "",
  requestedAddress: "",
};

// Largest single-node capacity from the cluster view — the ceiling a pod
// can ever be scheduled onto. Returns 0 when node data is unavailable
// (non-admin / not loaded), which the UI treats as "no cap".
export function nodeCaps(nodes: { cpu?: { capacity?: number }; memory?: { capacity?: number } }[]) {
  const maxCpu = nodes.reduce((m, n) => Math.max(m, n.cpu?.capacity ?? 0), 0);
  const maxMemBytes = nodes.reduce((m, n) => Math.max(m, n.memory?.capacity ?? 0), 0);
  return { maxCpu, maxMemGi: Math.floor(maxMemBytes / 1024 ** 3) };
}

// Split the CIDR allow-list textarea (newline- or comma-separated) into a
// clean list, dropping blanks.
export function parseSourceRanges(raw: string): string[] {
  return raw
    .split(/[\n,]/)
    .map((s) => s.trim())
    .filter(Boolean);
}

// Drop rows whose port name is blank (an untouched "Add override" row) so an
// abandoned editor never pollutes the create payload.
export function nonEmptyPortOverrides(overrides: PortOverride[]): PortOverride[] {
  return overrides.filter((p) => p.name.trim() !== "");
}

// Drop rows whose name is blank or port is invalid (0 or unset) so an
// incomplete mapping never pollutes the tunnel config payload.
export function nonEmptyTunnelPortMappings(
  mappings: Array<{ name: string; remotePort: number }>,
): Array<{ name: string; remotePort: number }> {
  return mappings.filter((m) => m.name.trim() && m.remotePort && m.remotePort > 0);
}

/**
 * Turns the wizard state into the GameServer create payload: template and
 * version, config, networking or tunnel, resources (requests equal to limits
 * unless the user reserves less) and node placement.
 */
function buildCreateBody(state: WizardState): ServerCreate {
  let nodeSelector: Record<string, string> | undefined;
  if (state.nodePlacement === "pin") nodeSelector = { "gameplane.local/pinned": "true" };
  else if (state.nodePlacement === "gpu") nodeSelector = { "gameplane.local/gpu": "true" };

  let tunnel: GameServerTunnel | undefined;
  if (state.tunnelEnabled) {
    // Determine credentialsSecretRef: either an existing secret name (GitOps) or
    // a deterministic name derived from the server name (for new credentials).
    // The handler creates the Secret at <serverName>-tunnel-auth and the operator
    // mounts it with Optional: true, so the ref can point to a Secret that doesn't
    // yet exist at create time.
    let credentialsSecretRef: { name: string } | undefined;
    if (state.tunnelCredentialsSecretName.trim()) {
      credentialsSecretRef = { name: state.tunnelCredentialsSecretName };
    } else if (state.tunnelCredentialsValue.trim()) {
      credentialsSecretRef = { name: `${state.name}-tunnel-auth` };
    }

    const tunnelBase: GameServerTunnel = {
      enabled: true,
      provider: state.tunnelProvider,
      ...(credentialsSecretRef ? { credentialsSecretRef } : {}),
    };

    if (state.tunnelProvider === "frp") {
      tunnelBase.frp = {
        serverAddr: state.frpServerAddr,
        ...(state.frpServerPort && state.frpServerPort !== "0"
          ? { serverPort: parseInt(state.frpServerPort, 10) }
          : {}),
        remotePorts: nonEmptyTunnelPortMappings(state.frpRemotePortMappings),
      };
    } else if (state.tunnelProvider === "tailscale") {
      tunnelBase.tailscale = {
        ...(state.tailscaleHostname ? { hostname: state.tailscaleHostname } : {}),
        ...(state.tailscaleTags
          ? { tags: state.tailscaleTags.split(/[\s,]+/).filter(Boolean) }
          : {}),
      };
    } else if (state.tunnelProvider === "playit") {
      tunnelBase.playit = {
        ...(state.playitTunnelName ? { tunnelName: state.playitTunnelName } : {}),
      };
    }
    // tunnelBase always carries `provider` alongside `enabled`, so it is
    // never just `{ enabled: true }` — include it whenever the wizard step
    // is enabled.
    tunnel = tunnelBase;
  }

  return {
    name: state.name,
    description: state.description || undefined,
    templateRef: { name: state.template!.metadata.name },
    ...(state.version ? { version: state.version } : {}),
    config: state.config,
    storage: { size: state.storageSize },
    networking: {
      expose: state.expose,
      hostname: state.hostname || undefined,
      // Only included when at least one override names a template port.
      ...(nonEmptyPortOverrides(state.portOverrides).length > 0
        ? { portOverrides: nonEmptyPortOverrides(state.portOverrides) }
        : {}),
      // Only meaningful for LoadBalancer; omit otherwise so we don't store a
      // range the operator would ignore.
      ...(state.expose === "LoadBalancer" && parseSourceRanges(state.sourceRanges).length > 0
        ? { sourceRanges: parseSourceRanges(state.sourceRanges) }
        : {}),
      // Load-balancer address pool preference; omit when empty so we don't store
      // a preference the operator would ignore (absent = no pool selection).
      ...(state.addressPool.trim() ? { addressPool: state.addressPool.trim() } : {}),
      // Specific IP to request from the pool; omit when empty.
      ...(state.requestedAddress.trim() ? { address: state.requestedAddress.trim() } : {}),
      ...(tunnel ? { tunnel } : {}),
    },
    resources: {
      // The create form uses Guaranteed QoS by default: requests == limits, so
      // the pod is scheduled onto (and kept on) a node that can always give it
      // the full amount, and never gets OOM-killed or CPU-throttled below what
      // was configured. "Reserve less than the limit" opts into Burstable QoS
      // instead: lower requests fit more servers per node, in exchange for
      // throttling or eviction when the node runs short.
      requests: state.reserveLess
        ? { cpu: state.cpuRequest, memory: state.memoryRequest }
        : { cpu: state.cpuLimit, memory: state.memoryLimit },
      limits: { cpu: state.cpuLimit, memory: state.memoryLimit },
    },
    ...(nodeSelector ? { nodeSelector } : {}),
  };
}

type StepCheck = { ok: true } | { ok: false; reason: string };

// caps bounds the resource inputs to the largest single node; {0,0} means
// node data is unavailable (non-admin / not loaded) and no cap is applied.
function validateStep(
  key: StepKey,
  state: WizardState,
  intl: IntlShape,
  caps: { maxCpu: number; maxMemGi: number } = { maxCpu: 0, maxMemGi: 0 },
): StepCheck {
  if (key === "template") {
    return state.template
      ? { ok: true }
      : { ok: false, reason: intl.formatMessage({ id: "web.routes.create_server.validation_pick_template", defaultMessage: "Pick a game template to continue", description: "Create server: validation message shown when the template step has no template picked" }) };
  }
  if (key === "version") {
    return isValidVersion(state.template ?? undefined, state.version)
      ? { ok: true }
      : { ok: false, reason: intl.formatMessage({ id: "web.routes.create_server.validation_choose_version", defaultMessage: "Choose a version to continue", description: "Create server: validation message shown when the version step has no version chosen" }) };
  }
  if (key === "configure") {
    if (!isValidK8sName(state.name)) {
      return {
        ok: false,
        reason: state.name
          ? intl.formatMessage({ id: "web.routes.create_server.validation_name_format", defaultMessage: "Name must be lowercase letters, digits, dashes (max 63)", description: "Create server: validation message shown when the server name is not a valid Kubernetes name" })
          : intl.formatMessage({ id: "web.routes.create_server.validation_name_required", defaultMessage: "Server name is required", description: "Create server: validation message shown when the server name is empty" }),
      };
    }
    if (!isValidQuantity(state.storageSize)) {
      return { ok: false, reason: intl.formatMessage({ id: "web.routes.create_server.validation_storage_quantity", defaultMessage: "Storage must be a Kubernetes quantity (e.g. 50Gi)", description: "Create server: validation message shown when the storage size is not a valid Kubernetes quantity" }) };
    }
    if (
      !isValidQuantity(state.cpuLimit) ||
      !isValidQuantity(state.memoryLimit) ||
      (state.reserveLess && (!isValidQuantity(state.cpuRequest) || !isValidQuantity(state.memoryRequest)))
    ) {
      return { ok: false, reason: intl.formatMessage({ id: "web.routes.create_server.validation_cpu_memory_quantity", defaultMessage: "CPU and memory must be valid quantities", description: "Create server: validation message shown when the CPU or memory limit is not a valid Kubernetes quantity" }) };
    }
    const cpuParsed = parseCpuQuantity(state.cpuLimit);
    if (caps.maxCpu > 0 && cpuParsed && cpuCores(cpuParsed) > caps.maxCpu) {
      return { ok: false, reason: intl.formatMessage({ id: "web.routes.create_server.validation_cpu_over_node", defaultMessage: "CPU can't exceed {cores} cores — no single node has more", description: "Create server: validation message shown when the CPU limit is above the largest node; {cores} is the core count of the largest node" }, { cores: String(caps.maxCpu) }) };
    }
    const memParsed = parseMemQuantity(state.memoryLimit);
    if (caps.maxMemGi > 0 && memParsed && memBytes(memParsed) / 1024 ** 3 > caps.maxMemGi) {
      return { ok: false, reason: intl.formatMessage({ id: "web.routes.create_server.validation_memory_over_node", defaultMessage: "Memory can't exceed {gib} GiB — no single node has more", description: "Create server: validation message shown when the memory limit is above the largest node; {gib} is the memory of the largest node in GiB" }, { gib: String(caps.maxMemGi) }) };
    }
    const cfgErrors = validateConfig(state.template?.spec.configSchema ?? [], state.config, intl);
    if (cfgErrors.length > 0) {
      return { ok: false, reason: cfgErrors[0].message };
    }
    return { ok: true };
  }
  if (key === "network") {
    if (state.tunnelEnabled) {
      // Tunnel requires either a credential value (new) or existing secret name (GitOps).
      if (!state.tunnelCredentialsValue.trim() && !state.tunnelCredentialsSecretName.trim()) {
        return { ok: false, reason: intl.formatMessage({ id: "web.routes.create_server.validation_tunnel_credentials", defaultMessage: "Tunnel requires credentials (enter value or existing secret name)", description: "Create server: validation message shown when the tunnel is enabled without credentials or a secret name" }) };
      }
      if (state.tunnelProvider === "frp") {
        if (!state.frpServerAddr.trim()) {
          return { ok: false, reason: intl.formatMessage({ id: "web.routes.create_server.validation_frp_server_address", defaultMessage: "frp requires a server address", description: "Create server: validation message shown when frp is selected without a server address" }) };
        }
        // Filter out empty mappings and check that at least one remains
        const nonEmptyMappings = state.frpRemotePortMappings.filter(
          (m) => m.name.trim() && m.remotePort && m.remotePort > 0,
        );
        if (nonEmptyMappings.length === 0) {
          return { ok: false, reason: intl.formatMessage({ id: "web.routes.create_server.validation_frp_port_mapping", defaultMessage: "frp requires at least one complete port mapping", description: "Create server: validation message shown when frp has no complete port mapping" }) };
        }
        // Validate port numbers are in valid range
        for (const mapping of nonEmptyMappings) {
          const port = mapping.remotePort;
          if (port < 1 || port > 65535) {
            return { ok: false, reason: intl.formatMessage({ id: "web.routes.create_server.validation_port_range", defaultMessage: "Port numbers must be between 1 and 65535", description: "Create server: validation message shown when a frp remote port is outside 1-65535" }) };
          }
        }
        if (state.frpServerPort) {
          const serverPort = parseInt(state.frpServerPort, 10);
          if (isNaN(serverPort) || serverPort < 1 || serverPort > 65535) {
            return { ok: false, reason: intl.formatMessage({ id: "web.routes.create_server.validation_server_port_range", defaultMessage: "Server port must be between 1 and 65535", description: "Create server: validation message shown when the frp server port is outside 1-65535" }) };
          }
        }
      }
    }
    return { ok: true };
  }
  return { ok: true };
}

function errorMessage(err: unknown, name: string, intl: IntlShape): { title: string; body: string } {
  if (err instanceof APIError) {
    if (err.status === 409) {
      return {
        title: intl.formatMessage({ id: "web.routes.create_server.error_name_taken_title", defaultMessage: "A server named {name} already exists", description: "Create server: error title when a server with the same name exists; {name} is the server name" }, { name }),
        body: intl.formatMessage({ id: "web.routes.create_server.error_name_taken_body", defaultMessage: "Pick a different name, or open the existing server. Names must be unique inside the namespace.", description: "Create server: error body when a server with the same name exists" }),
      };
    }
    if (err.status === 403) {
      return {
        title: intl.formatMessage({ id: "web.routes.create_server.error_not_permitted_title", defaultMessage: "Not permitted", description: "Create server: error title when the user's role cannot create servers" }),
        body: intl.formatMessage({ id: "web.routes.create_server.error_not_permitted_body", defaultMessage: "Your role does not allow creating servers in this namespace.", description: "Create server: error body when the user's role cannot create servers" }),
      };
    }
    return {
      title: intl.formatMessage({ id: "web.routes.create_server.error_create_failed_status", defaultMessage: "Create failed ({status})", description: "Create server: error title for an API error; {status} is the HTTP status code" }, { status: String(err.status) }),
      body: err.body.slice(0, 240) || intl.formatMessage({ id: "web.routes.create_server.error_api_rejected", defaultMessage: "The API rejected the request.", description: "Create server: fallback error body when the API returned no body" }),
    };
  }
  return {
    title: intl.formatMessage({ id: "web.routes.create_server.error_create_failed", defaultMessage: "Create failed", description: "Create server: error title when server creation fails" }),
    body: errorTextIntl(intl, err, intl.formatMessage({ id: "web.routes.create_server.error_unknown", defaultMessage: "Unknown error", description: "Create server: fallback error body when the error has no message" })),
  };
}

export function CreateServerWizard() {
  const intl = useOptionalIntl();
  const search = useSearch({ from: "/app-layout/servers/new" });
  const placements = useFleetPlacements();
  const [selected, setSelected] = useState<string>();
  const choices = placements.data?.items ?? [];
  const key = (p: FleetPlacement) => JSON.stringify([p.cluster, p.namespace]);
  const requested = choices.filter((p) => (!search.cluster || p.cluster === search.cluster) && (!search.ns || p.namespace === search.ns));
  const placement = selected ? choices.find((p) => key(p) === selected) : requested.find((p) => p.cluster === "local") ?? requested[0];
  if (!selected && placement) setSelected(key(placement));
  if (placements.isPending) return <LoadingCard message={intl.formatMessage({ id: "web.routes.create_server.finding_available_locations", defaultMessage: "Finding available locations…", description: "Create server: loading text shown while the list of server locations is being fetched" })} />;
  if (placements.error) return <ErrorCard title={intl.formatMessage({ id: "web.routes.create_server.locations_unavailable", defaultMessage: "Locations unavailable", description: "Create server: title of the error card shown when the list of authorized server locations fails to load" })} message={intl.formatMessage({ id: "web.routes.create_server.unable_to_load_authorized_server", defaultMessage: "Unable to load authorized server locations.", description: "Create server: message under the locations error title, saying the authorized server locations could not be loaded" })} onRetry={() => void placements.refetch()} />;
  if (!placement) return <ErrorCard title={intl.formatMessage({ id: "web.routes.create_server.no_available_location", defaultMessage: "No available location", description: "Create server: title of the error card shown when no location is available to create a server in" })} message={intl.formatMessage({ id: "web.routes.create_server.the_selected_location_is_unavailable_or", defaultMessage: "The selected location is unavailable or no reachable location permits creation. Restore its access or return to the server list to choose an available location.", description: "Create server: explanation under the no-location title; tells the user to restore access to the location or go back to the server list" })} />;
  return <PlacedServerWizard key={key(placement)} placement={placement} choices={choices} onPlacementChange={setSelected} partial={placements.data?.partial === true} />;
}

function PlacedServerWizard({ placement, choices, onPlacementChange, partial }: {
  placement: FleetPlacement; choices: FleetPlacement[]; onPlacementChange: (key: string) => void; partial: boolean;
}) {
  const intl = useOptionalIntl();
  const clusterId = placement.cluster;
  const namespace = placement.namespace;
  const client = useMemo(() => createResourceClient({ cluster: clusterId, namespace }), [clusterId, namespace]);
  const { Servers } = client;
  const [stepIndex, setStepIndex] = useState(0);
  const [state, setState] = useState<WizardState>(initial);
  const nav = useNavigate();
  const qc = useQueryClient();
  const templateText = useModuleTexts(placement.templates.map((t) => t.metadata.name), clusterId);
  // Cancel/Close just leave the wizard; a rejected navigation (e.g. the
  // route change was interrupted) has nothing useful to show, but must not
  // become an unhandled promise rejection.
  const closeWizard = () => ignoreRejection(nav({ to: "/servers" }));

  // When arriving from the Modules catalog "Deploy" action
  // (/servers/new?template=<name>), pre-select that template once the list
  // loads. One-shot, so manual changes afterwards aren't clobbered.
  const search = useSearch({ from: "/app-layout/servers/new" });
  const templates = { items: placement.templates };
  const gameCodes = useMemo(() => assignGameCodesForTemplates(placement.templates), [placement.templates]);
  const byName = useMemo(() => new Map(placement.templates.map((t) => [t.metadata.name, t])), [placement.templates]);
  const [presetApplied, setPresetApplied] = useState(false);
  // Adjusted directly during render (not in an effect): re-checks on every
  // render until a match is found (templates may still be loading, or the
  // list may not include the requested name yet), but only calls setState
  // — flipping presetApplied, which stops the check — once it actually
  // finds one.
  if (!presetApplied && search.template && templates) {
    const match = templates.items.find((t) => t.metadata.name === search.template);
    if (match) {
      setPresetApplied(true);
      setState((s) => ({ ...s, template: match, config: {}, version: defaultVersionId(match) ?? "" }));
    }
  }

  const create = useMutation({
    mutationFn: async () => {
      const server = await Servers.create(buildCreateBody(state));

      // If tunnel is enabled and credential value was provided, save credentials after server creation.
      if (state.tunnelEnabled && state.tunnelCredentialsValue.trim() && !state.tunnelCredentialsSecretName.trim()) {
        const key =
          state.tunnelProvider === "frp"
            ? "token"
            : state.tunnelProvider === "tailscale"
              ? "authKey"
              : "secretKey";
        try {
          await Servers.setTunnelCredentials(state.name, state.tunnelProvider, { [key]: state.tunnelCredentialsValue }, placement.namespace);
        } catch (err) {
          // Server was created with tunnel enabled and a ref to the secret, but the credential
          // save (Secret creation) failed. Tell the user the server exists and how to retry.
          const detail = errorTextIntl(intl, err, String(err));
          throw new Error(
            intl.formatMessage({ id: "web.routes.create_server.error_credential_save_failed", defaultMessage: "Server created but credential save failed: {detail}. The server exists with tunnel enabled. You can set the credential from the server's Networking settings.", description: "Create server: error shown when the server was created but saving its tunnel credential failed; {detail} is the underlying error text" }, { detail }),
            { cause: err }
          );
        }
      }

      return server;
    },
    onSuccess: async (server) => {
      await qc.invalidateQueries({ queryKey: ["fleet"] });
      await nav(serverLink({ cluster: placement.cluster, namespace: server.metadata.namespace ?? placement.namespace, name: server.metadata.name }));
    },
  });

  const steps = stepsFor(state.template);
  const currentKey: StepKey = steps[stepIndex] ?? "template";
  const isLast = stepIndex === steps.length - 1;
  // Resource ceilings from the cluster, for the Configure step's cap.
  const { data: clusterView } = useQuery({
    queryKey: ["cluster-view", clusterId],
    queryFn: ({ signal }) => client.withSignal(signal).Cluster.view(),
    staleTime: 30_000,
  });
  const caps = nodeCaps(clusterView?.nodes ?? []);
  const stepCheck = validateStep(currentKey, state, intl, caps);
  const finalCheck = steps
    .filter((k) => k !== "review")
    .every((k) => validateStep(k, state, intl, caps).ok)
    ? ({ ok: true } as const)
    : ({ ok: false } as const);

  return (
    // Fixed inset-0 backdrop over the surrounding app chrome (design W8idqY:
    // 960×760 centered card at 12px radius over a #000000B3 backdrop). The
    // cap is viewport-relative rather than a flat 760px: step content height
    // varies per step (design QQtUD's Step 4/Network frame is the showcase,
    // design f1Vga shows the broader modal layout). So a fixed 760px clips
    // the taller steps' scrollable body instead of just capping the dialog.
    <div className="fixed inset-0 flex items-center justify-center overflow-auto bg-black/70">
      <div role="dialog" aria-modal="true" className="w-[960px] max-h-[90vh] overflow-hidden rounded-[12px] border border-border bg-card shadow-2xl flex flex-col">
        <div className="flex shrink-0 items-start justify-between border-b border-border px-7 py-5">
          <div>
            <div className="text-lg font-semibold">{intl.formatMessage({ id: "web.routes.create_server.new_game_server", defaultMessage: "New game server", description: "Create server: heading of the wizard dialog for creating a new game server, shown at the top left" })}</div>
            <div className="pt-0.5 text-xs text-muted">{intl.formatMessage({ id: "web.routes.create_server.step_stepindex_of_length_currentkey", defaultMessage: "Step {stepIndex} of {length} · {currentKey}", description: "Create server: wizard progress line in the header; {stepIndex} is the current step number, {length} the step count, {currentKey} the current step name" }, { stepIndex: stepIndex + 1, length: steps.length, currentKey: intl.formatMessage(STEP_TITLES[currentKey]) })}</div>
          </div>
          <Button
            isIconOnly
            variant="ghost"
            isDisabled={create.isPending} onPress={closeWizard}
            aria-label={intl.formatMessage({ id: "web.routes.create_server.close", defaultMessage: "Close", description: "Create server: accessible label of the icon-only close button in the wizard header; read by screen readers, not shown as text" })}
          >
            <X className="h-5 w-5" />
          </Button>
        </div>

        <div className="border-b border-border px-6 py-3">
          <Select aria-label={intl.formatMessage({ id: "web.routes.create_server.location", defaultMessage: "Location", description: "Create server: label of the location dropdown in the wizard header, which chooses the cluster and namespace the server is created in" })} value={JSON.stringify([placement.cluster, placement.namespace])} isDisabled={create.isPending || choices.length < 2} onChange={(value) => onPlacementChange(String(value))}>
            <Label>{intl.formatMessage({ id: "web.routes.create_server.location", defaultMessage: "Location", description: "Create server: label of the location dropdown in the wizard header, which chooses the cluster and namespace the server is created in" })}</Label><Select.Trigger><Select.Value /><Select.Indicator /></Select.Trigger>
            <Select.Popover><ListBox>{choices.map((p) => <ListBoxItem key={JSON.stringify([p.cluster, p.namespace])} id={JSON.stringify([p.cluster, p.namespace])}>{intl.formatMessage({ id: "web.routes.create_server.cluster_namespace", defaultMessage: "{cluster} / {namespace}", description: "Create server: location choice label; {cluster} is the cluster name and {namespace} the namespace" }, { cluster: p.cluster, namespace: p.namespace })}</ListBoxItem>)}</ListBox></Select.Popover>
          </Select>
          <p className="mt-1 text-xs text-muted">{intl.formatMessage({ id: "web.routes.create_server.changing_location_starts_a_fresh_draft", defaultMessage: "Changing location starts a fresh draft. The chosen location handles all creation steps.", description: "Create server: hint under the location dropdown warning that switching location starts a fresh draft and the chosen location handles all steps" })}</p>
          {partial && <p role="status" className="text-xs text-warning">{intl.formatMessage({ id: "web.routes.create_server.some_locations_are_unavailable", defaultMessage: "Some locations are unavailable. Available locations are shown.", description: "Create server: status note under the location dropdown shown when some locations are unavailable and only the available ones are listed" })}</p>}
        </div>
        <StepBar steps={steps} stepIndex={stepIndex} />

        <div className="grid flex-1 min-h-0 overflow-auto gap-6 px-6 py-6 md:grid-cols-[1fr_260px]">
          <fieldset disabled={create.isPending} className="min-w-0">
            {currentKey === "template" && <PickTemplate templates={placement.templates} state={state} setState={setState} gameCodes={gameCodes} byName={byName} templateText={templateText} />}
            {currentKey === "version" && <PickVersion state={state} setState={setState} cluster={clusterId} />}
            {currentKey === "configure" && <Configure caps={caps} state={state} setState={setState} cluster={clusterId} />}
            {currentKey === "network" && <Network state={state} setState={setState} />}
            {currentKey === "review" && (
              <Review
                location={`${placement.cluster} / ${placement.namespace}`}
                cluster={clusterId}
                state={state}
                onEdit={(key) => {
                  const idx = steps.indexOf(key);
                  if (idx >= 0) setStepIndex(idx);
                }}
              />
            )}
          </fieldset>
          <Preview state={state} gameCodes={gameCodes} byName={byName} cluster={clusterId} />
        </div>

        {create.isError && (
          <ErrorAlert {...errorMessage(create.error, state.name, intl)} />
        )}

        <div className="flex shrink-0 items-center justify-between border-t border-border px-7 py-4">
          <a href={DOCS_CREATE_SERVER_URL} target="_blank" rel="noreferrer" className="flex items-center gap-1 text-xs text-muted hover:text-fg">
            <ExternalLink className="h-3 w-3" /> {intl.formatMessage({ id: "web.routes.create_server.docs_creating_game_servers", defaultMessage: "Docs: Creating game servers", description: "Create server: link at the bottom of the wizard that opens the documentation page about creating game servers in a new tab" })}</a>
          <div className="flex items-center gap-3">
            {!stepCheck.ok && !isLast && (
              <span className="text-[11px] text-muted" data-testid="step-reason">
                {stepCheck.reason}
              </span>
            )}
            {stepIndex === 0 ? (
              <Button variant="ghost" onPress={closeWizard}>{intl.formatMessage({ id: "web.routes.create_server.cancel", defaultMessage: "Cancel", description: "Create server: footer button that closes the wizard without creating a server; keep it short" })}</Button>
            ) : (
              <Button
                variant="ghost"
                isDisabled={create.isPending} onPress={() => setStepIndex((i) => Math.max(0, i - 1))}
              >
                <ArrowLeft className="h-4 w-4" /> {intl.formatMessage({ id: "web.routes.create_server.back", defaultMessage: "Back", description: "Create server: footer button that returns to the previous wizard step; keep it short" })}</Button>
            )}
            {!isLast ? (
              <Button
                variant="primary"
                isDisabled={!stepCheck.ok}
                onPress={() => setStepIndex((i) => Math.min(steps.length - 1, i + 1))}
              >{intl.formatMessage({ id: "web.routes.create_server.continue_to_stepindex", defaultMessage: "Continue to {stepIndex}", description: "Create server: label of the button that moves to the next wizard step; {stepIndex} is the next step name" }, { stepIndex: intl.formatMessage(STEP_TITLES[steps[stepIndex + 1]]) })} <ArrowRight className="h-4 w-4" />
              </Button>
            ) : (
              <Button
                variant="primary"
                className="rounded-full"
                onPress={() => create.mutate()}
                isDisabled={create.isPending || !finalCheck.ok}
              >
                {create.isPending ? (
                  <>{intl.formatMessage({ id: "web.routes.create_server.creating", defaultMessage: "Creating…", description: "Create server: label of the final footer button while the server is being created, shown with a spinner; keep it short" })} <Loader2 className="h-4 w-4 animate-spin" /></>
                ) : (
                  <>{intl.formatMessage({ id: "web.routes.create_server.create_server", defaultMessage: "Create server", description: "Create server: label of the final footer button that submits the wizard and creates the server; keep it short" })} <Check className="h-4 w-4" /></>
                )}
              </Button>
            )}
          </div>
        </div>
      </div>
    </div>
  );
}

function ErrorAlert({ title, body }: { title: string; body: string }) {
  return (
    <Alert status="danger" className="mx-6 mb-4" data-testid="create-error">
      <Alert.Indicator>
        <AlertCircle className="h-5 w-5 shrink-0" />
      </Alert.Indicator>
      <Alert.Content>
        <div className="font-medium">{title}</div>
        <div className="pt-1 text-xs">{body}</div>
      </Alert.Content>
    </Alert>
  );
}

function StepBar({ steps, stepIndex }: { steps: StepKey[]; stepIndex: number }) {
  const intl = useOptionalIntl();
  return (
    <ol className="flex items-center gap-2 border-b border-border px-6 py-3 text-xs">
      {steps.map((key, i) => {
        const active = i === stepIndex;
        const done = i < stepIndex;
        return (
          <li key={key} className="flex items-center gap-2">
            <span
              className={cn(
                "flex h-5 w-5 items-center justify-center rounded-full border font-mono text-[10px]",
                active ? "border-primary bg-primary/15 text-primary"
                       : done
                       ? "border-success bg-success/15 text-success"
                       : "border-border text-muted",
              )}
            >{done ? <Check className="h-3 w-3" /> : i + 1}</span>
            <span className={cn(active ? "text-fg" : "text-muted")}>{intl.formatMessage(STEP_TITLES[key])}</span>
            {i < steps.length - 1 && <span className="text-muted">{intl.formatMessage({ id: "web.common.separator", defaultMessage: "·", description: "Separator character between items in a list, such as wizard steps" })}</span>}
          </li>
        );
      })}
    </ol>
  );
}

function PickTemplate({
  templates,
  state,
  setState,
  gameCodes,
  byName,
  templateText,
}: {
  state: WizardState;
  setState: (s: WizardState) => void;
  gameCodes: Map<string, string>;
  byName: Map<string, GameTemplate>;
  templates: GameTemplate[];
  templateText: (moduleName: string) => ModuleText;
}) {
  const intl = useOptionalIntl();
  const [q, setQ] = useState("");
  const [cat, setCat] = useState<string>("all");

  // ⚡ Bolt: Prevent recreating intermediate objects before useMemo.
  // Using 'templates' directly instead of 'data' wrapper ensures stable reference.
  const templateCategories = useMemo(
    () =>
      categoryFilters((templates ?? []).map((t) => resolveCategories(t.spec.categories, t.spec.game))),
    [templates],
  );
  // The chips come from the templates, so a selected category can disappear when the
  // template list changes. Fall back to "all" instead of showing an empty
  // picker with no chip highlighted.
  const activeCat = templateCategories.includes(cat) ? cat : "all";

  const needle = q.trim().toLowerCase();
  const filtered = (templates ?? []).filter((t) => {
    if (!matchesCategory(t.spec.categories, t.spec.game, activeCat)) return false;
    if (needle) {
      const text = templateText(t.metadata.name);
      const hay = `${t.spec.displayName} ${t.spec.game} ${t.spec.description ?? ""} ${text(moduleIds.templateDisplayName, t.spec.displayName)} ${text(moduleIds.templateDescription, t.spec.description ?? "")}`.toLowerCase();
      if (!hay.includes(needle)) return false;
    }
    return true;
  });

  return (
    <div className="space-y-3">
      <div className="flex flex-col gap-2">
        <Input
          placeholder={intl.formatMessage({ id: "web.routes.create_server.search", defaultMessage: "Search…", description: "Create server: placeholder text in the template search box, shown before the user types; typing filters the template list" })}
          value={q}
          onChange={(e) => setQ(e.target.value)}
          aria-label={intl.formatMessage({ id: "web.routes.create_server.search_templates", defaultMessage: "Search templates", description: "Create server: accessible label of the template search box, read by screen readers; the box shows a placeholder instead" })}
        />
        <div className="flex flex-wrap gap-1 rounded-md border border-border bg-surface/40 p-1">
          {templateCategories.map((c: string) => (
            <Button
              key={c}
              size="sm"
              variant={activeCat === c ? "primary" : "ghost"}
              onPress={() => setCat(c)}
              aria-pressed={activeCat === c}
              // No radius class needed: globals.css's unlayered
              // `.button:not(.rounded-full) { border-radius: 6px }` already
              // pins every HeroUI Button — these category chips included —
              // to the design's 6px, and wins over any Tailwind utility
              // class added here anyway.
            >
              {c === "all" ? intl.formatMessage({ id: "web.routes.create_server.category_all", defaultMessage: "All", description: "Create server: template category chip that shows every template" }) : categoryLabel(c, intl)}
            </Button>
          ))}
        </div>
      </div>
      <div className="grid gap-3 sm:grid-cols-2">
        {filtered.map((t) => {
          const active = state.template?.metadata.name === t.metadata.name;
          const text = templateText(t.metadata.name);
          return (
            <button
              key={t.metadata.name}
              onClick={() => setState({ ...state, template: t, config: {}, version: defaultVersionId(t) ?? "" })}
              className={cn(
                "rounded-lg border p-4 text-left transition-colors",
                active ? "border-primary bg-primary/5"
                       : "border-border hover:bg-surface/60",
              )}
            >
              <div className="flex items-center gap-3">
                <GameIcon
                  game={t.spec.game}
                  icon={byName.get(t.metadata.name)?.spec.icon}
                  code={gameCodes.get(t.metadata.name)}
                  size="md"
                />
                <div>
                  <div className="text-sm font-medium">{text(moduleIds.templateDisplayName, t.spec.displayName)}</div>
                  <div className="text-[11px] text-muted">{intl.formatMessage({ id: "web.routes.create_server.vversion", defaultMessage: "v{version}", description: "Create server: template version shown under a template card; {version} is the version string" }, { version: t.spec.version })}</div>
                </div>
              </div>
              <p className="pt-2 text-xs text-muted line-clamp-2">
                {t.spec.description === undefined
                  ? intl.formatMessage({ id: "web.routes.create_server.no_description", defaultMessage: "No description.", description: "Create server: shown under a template that has no description" })
                  : text(moduleIds.templateDescription, t.spec.description)}
              </p>
            </button>
          );
        })}
      </div>
      {filtered.length === 0 && (
        <p className="text-sm text-muted">{intl.formatMessage({ id: "web.routes.create_server.no_templates_match_your_search", defaultMessage: "No templates match your search.", description: "Create server: message shown in the template list when the search text matches no template" })}</p>
      )}
    </div>
  );
}

function PickVersion({ state, setState, cluster }: { state: WizardState; setState: (s: WizardState) => void; cluster: string }) {
  const intl = useOptionalIntl();
  const text = useModuleText(state.template?.metadata.name, cluster);
  const versions = state.template?.spec.versions ?? [];
  return (
    <div className="space-y-3">
      <div className="text-sm font-medium">{intl.formatMessage({ id: "web.routes.create_server.choose_a_version", defaultMessage: "Choose a version", description: "Create server: heading of the version step, shown above the list of versions and loaders to pick from" })}</div>
      <p className="text-xs text-muted">{intl.formatMessage({ id: "web.routes.create_server.pick_the_version_and_loader_for_this", defaultMessage: "Pick the version and loader for this server. Each loader keeps its own mods on a separate volume, so switching never clobbers another.", description: "Create server: hint under the version step heading explaining that each loader keeps its own mods on a separate volume, so switching never removes other mods" })}</p>
      <div className="grid gap-3 sm:grid-cols-2">
        {versions.map((v) => {
          const active = state.version === v.id;
          return (
            <button
              key={v.id}
              onClick={() => setState({ ...state, version: v.id })}
              className={cn(
                "rounded-lg border p-4 text-left transition-colors",
                active ? "border-primary bg-primary/5"
                       : "border-border hover:bg-surface/60",
              )}
            >
              <div className="flex items-center justify-between gap-2">
                <div className="text-sm font-medium">{text(moduleIds.versionDisplayName(v.id), v.displayName)}</div>
                {v.default && (
                  <span className="rounded-full bg-primary/15 px-2 py-0.5 text-[10px] text-primary">{intl.formatMessage({ id: "web.routes.create_server.default", defaultMessage: "Default", description: "Create server: small badge on a version card marking the default version; keep it short" })}</span>
                )}
              </div>
              {v.loader && <div className="pt-1 text-[11px] text-muted">{intl.formatMessage({ id: "web.routes.create_server.loader_loader", defaultMessage: "Loader: {loader}", description: "Create server: loader line under a version option; {loader} is the loader name" }, { loader: v.loader })}</div>}
            </button>
          );
        })}
      </div>
    </div>
  );
}

/**
 * Configure step: server name and description, the template's config fields,
 * CPU and memory (capped at the largest node) with the optional reservation
 * below the limit, storage size and node placement.
 */
function Configure({ state, setState, caps, cluster }: { state: WizardState; setState: (s: WizardState) => void; caps: { maxCpu: number; maxMemGi: number }; cluster: string }) {
  const intl = useOptionalIntl();
  const text = useModuleText(state.template?.metadata.name, cluster);
  const fields = state.template?.spec.configSchema ?? [];
  // Cap CPU/memory at the largest single node's capacity — the scheduler
  // can never place a pod that requests more than one node provides, so
  // let the user know the ceiling and clamp their input to it.
  const { maxCpu, maxMemGi } = caps;
  const reserveLessLabel = intl.formatMessage({ id: "web.routes.create_server.reserve_less_than_the_limit", defaultMessage: "Reserve less than the limit", description: "Create server: label next to the switch that lets the CPU and memory reserved for the server (Kubernetes requests) be lower than its limits; the same text is the switch's accessible name" });
  const reservedCpuLabel = intl.formatMessage({ id: "web.routes.create_server.reserved_cpu", defaultMessage: "Reserved CPU", description: "Create server: field label for the CPU reserved for the server (the Kubernetes request), shown when the reserve-less switch is on; the same text is the accessible name of its slider; CPU is a technical term, usually left untranslated" });
  const reservedMemoryLabel = intl.formatMessage({ id: "web.routes.create_server.reserved_memory", defaultMessage: "Reserved memory", description: "Create server: field label for the memory reserved for the server (the Kubernetes request), shown when the reserve-less switch is on; the same text is the accessible name of its slider" });
  return (
    <div className="space-y-4">
      <div className="space-y-1.5">
        <label className="block text-xs text-muted">{intl.formatMessage({ id: "web.routes.create_server.server_name", defaultMessage: "Server name", description: "Create server: field label for the name of the new server; the name is used as the pod name and subdomain" })}</label>
        <Input
          value={state.name}
          onChange={(e) => setState({ ...state, name: e.target.value })}
          placeholder={intl.formatMessage({ id: "web.routes.create_server.mc_hardcore", defaultMessage: "mc-hardcore", description: "Create server: example placeholder shown in the server name field before the user types; an example value, not a default" })}
          required
        />
        <span className="text-[11px] text-muted">{intl.formatMessage({ id: "web.routes.create_server.used_as_pod_name_and_subdomain", defaultMessage: "Used as pod name and subdomain. Lowercase, dash-separated.", description: "Create server: hint under the server name field saying the name becomes the pod name and subdomain and should be lowercase with dashes" })}</span>
      </div>
      <div className="space-y-1.5">
        <label className="block text-xs text-muted">{intl.formatMessage({ id: "web.routes.create_server.description", defaultMessage: "Description", description: "Create server: field label for the optional free-text description of the new server" })}</label>
        <textarea
          className="min-h-[72px] w-full rounded-md border border-border bg-surface px-3 py-2 text-sm text-fg placeholder:text-muted focus:border-primary focus:outline-hidden"
          value={state.description}
          onChange={(e) => setState({ ...state, description: e.target.value })}
          placeholder={intl.formatMessage({ id: "web.routes.create_server.hardcore_survival_realm_with_curated", defaultMessage: "Hardcore survival realm with curated mods. Invite only.", description: "Create server: example placeholder shown in the description box before the user types; an example text, not a default" })}
        />
      </div>

      <div className="grid gap-3 sm:grid-cols-2">
        <div className="space-y-1.5">
          <label className="block text-xs text-muted">{intl.formatMessage({ id: "web.routes.create_server.cpu", defaultMessage: "CPU", description: "Create server: field label for the CPU amount given to the server; technical term, usually left untranslated" })}</label>
          <ResourceInput
            kind="cpu"
            value={state.cpuLimit}
            // Kubernetes rejects a request above its limit, so a lower limit
            // pulls the reservation down with it.
            onChange={(q) => setState({
              ...state,
              cpuLimit: q,
              cpuRequest: cpuQuantityCores(state.cpuRequest) > cpuQuantityCores(q) ? q : state.cpuRequest,
            })}
            max={maxCpu > 0 ? maxCpu : undefined}
          />
          {maxCpu > 0 && (
            <span className="text-[11px] text-muted">{intl.formatMessage({ id: "web.routes.create_server.max_maxcpu_largest_node", defaultMessage: "Max {maxCpu} (largest node)", description: "Create server: hint under the CPU field; {maxCpu} is the largest CPU count one node can provide" }, { maxCpu: maxCpu })}</span>
          )}
        </div>
        <div className="space-y-1.5">
          <label className="block text-xs text-muted">{intl.formatMessage({ id: "web.routes.create_server.memory", defaultMessage: "Memory", description: "Create server: field label for the memory amount given to the server" })}</label>
          <ResourceInput
            kind="memory"
            value={state.memoryLimit}
            onChange={(q) => setState({
              ...state,
              memoryLimit: q,
              memoryRequest: memQuantityBytes(state.memoryRequest) > memQuantityBytes(q) ? q : state.memoryRequest,
            })}
            max={maxMemGi > 0 ? maxMemGi : undefined}
          />
          {maxMemGi > 0 && (
            <span className="text-[11px] text-muted">{intl.formatMessage({ id: "web.routes.create_server.max_maxmemgi_gib_largest_node", defaultMessage: "Max {maxMemGi} GiB (largest node)", description: "Create server: hint under the memory field; {maxMemGi} is the largest memory in GiB one node can provide" }, { maxMemGi: maxMemGi })}</span>
          )}
        </div>
      </div>

      <div className="space-y-1.5">
        <div className="flex items-center gap-3">
          <Switch
            isSelected={state.reserveLess}
            // Switching on starts the reservation at the limits, so nothing
            // changes until the user lowers it.
            onChange={(on) => setState({ ...state, reserveLess: on, cpuRequest: state.cpuLimit, memoryRequest: state.memoryLimit })}
            aria-label={reserveLessLabel}
          >
            <Switch.Content>
              <Switch.Control>
                <Switch.Thumb />
              </Switch.Control>
            </Switch.Content>
          </Switch>
          <span className="text-sm text-fg">{reserveLessLabel}</span>
        </div>
        <span className="text-[11px] text-muted">{intl.formatMessage({ id: "web.routes.create_server.only_the_reserved_amount_counts_when", defaultMessage: "Only the reserved amount counts when the server is placed on a node, so more servers fit; it can use more, up to the limit, when the node has room (Burstable QoS).", description: "Create server: hint under the reserve-less switch; Burstable QoS is a Kubernetes technical term" })}</span>
      </div>

      {state.reserveLess && (
        <div className="grid gap-3 sm:grid-cols-2">
          <div className="space-y-1.5">
            <label className="block text-xs text-muted">{reservedCpuLabel}</label>
            <ResourceInput
              kind="cpu"
              label={reservedCpuLabel}
              value={state.cpuRequest}
              onChange={(q) => setState({ ...state, cpuRequest: q })}
              min={0}
              max={cpuQuantityCores(state.cpuLimit)}
            />
          </div>
          <div className="space-y-1.5">
            <label className="block text-xs text-muted">{reservedMemoryLabel}</label>
            <ResourceInput
              kind="memory"
              label={reservedMemoryLabel}
              value={state.memoryRequest}
              onChange={(q) => setState({ ...state, memoryRequest: q })}
              min={0}
              max={memQuantityBytes(state.memoryLimit) / 1024 ** 3}
            />
          </div>
        </div>
      )}

      <div className="space-y-1.5">
        <label className="block text-xs text-muted">{intl.formatMessage({ id: "web.routes.create_server.persistent_storage", defaultMessage: "Persistent storage", description: "Create server: field label for the storage size reserved for the server's data" })}</label>
        <Input
          value={state.storageSize}
          onChange={(e) => setState({ ...state, storageSize: e.target.value })}
          placeholder={intl.formatMessage({ id: "web.routes.create_server.50gi", defaultMessage: "50Gi", description: "Create server: example placeholder in the storage size field showing the size format; an example value, not a default" })}
        />
        <span className="text-[11px] text-muted">{intl.formatMessage({ id: "web.routes.create_server.mounted_at_data_rwo_pvc", defaultMessage: "Mounted at /data (RWO PVC).", description: "Create server: hint under the storage field saying the storage is mounted at /data as a RWO PVC; PVC is a technical term, usually left untranslated" })}</span>
      </div>

      <div className="space-y-1.5">
        <label className="block text-xs text-muted">{intl.formatMessage({ id: "web.routes.create_server.node_placement", defaultMessage: "Node placement", description: "Create server: field label for choosing which Kubernetes node runs the server, with automatic or pinned options" })}</label>
        <div className="inline-flex gap-1 rounded-md border border-border p-1">
          {(["auto", "pin", "gpu"] as const).map((p) => (
            <Button
              key={p}
              size="sm"
              variant={state.nodePlacement === p ? "primary" : "ghost"}
              onPress={() => setState({ ...state, nodePlacement: p })}
            >
              {p === "auto"
                ? intl.formatMessage({ id: "web.routes.create_server.node_placement_auto", defaultMessage: "Auto (scheduler)", description: "Create server: node placement option letting the scheduler pick a node" })
                : p === "pin"
                  ? intl.formatMessage({ id: "web.routes.create_server.node_placement_pin", defaultMessage: "Pin to node", description: "Create server: node placement option pinning the server to one node" })
                  : intl.formatMessage({ id: "web.routes.create_server.node_placement_gpu", defaultMessage: "GPU-enabled", description: "Create server: node placement option requiring a GPU-capable node" })}
            </Button>
          ))}
        </div>
        <div className="text-[11px] text-muted">
          {state.nodePlacement === "auto" && intl.formatMessage({ id: "web.routes.create_server.node_placement_auto_hint", defaultMessage: "Picks node with most free memory, tolerations or no taints.", description: "Create server: help text under the auto node placement option" })}
          {state.nodePlacement === "pin" && intl.formatMessage({ id: "web.routes.create_server.node_placement_pin_hint", defaultMessage: "Pins to a specific node (selectable after create).", description: "Create server: help text under the pin node placement option" })}
          {state.nodePlacement === "gpu" && intl.formatMessage({ id: "web.routes.create_server.node_placement_gpu_hint", defaultMessage: "Requires node with a GPU-capable device plugin.", description: "Create server: help text under the GPU node placement option" })}
        </div>
      </div>

      {fields.length > 0 && (
        <div className="space-y-3 pt-3">
          <div className="text-xs uppercase tracking-wide text-muted">{intl.formatMessage({ id: "web.routes.create_server.template_configuration", defaultMessage: "Template configuration", description: "Create server: small uppercase heading above the template-specific settings, shown only when the template has such settings" })}</div>
          <ConfigFields
            schema={fields}
            values={state.config}
            onChange={(name, value) =>
              setState({ ...state, config: { ...state.config, [name]: value } })
            }
            text={text}
          />
        </div>
      )}
    </div>
  );
}

function Network({ state, setState }: { state: WizardState; setState: (s: WizardState) => void }) {
  const intl = useOptionalIntl();
  return (
    <div className="space-y-4">
      <div className="space-y-1.5">
        <label className="block text-xs text-muted">{intl.formatMessage({ id: "web.routes.create_server.expose", defaultMessage: "Expose", description: "Create server: field label for how the server is exposed to players; the choice is shown as option cards" })}</label>
        <div className="grid gap-2 sm:grid-cols-3">
          {(["ClusterIP", "NodePort", "LoadBalancer"] as const).map((e) => (
            <button
              key={e}
              onClick={() => setState({ ...state, expose: e })}
              className={cn(
                "rounded-md border p-3 text-left text-sm",
                state.expose === e
                  ? "border-primary bg-primary/5"
                  : "border-border hover:bg-surface/60",
              )}
            >
              <div className="font-medium">{e}</div>
              <div className="pt-0.5 text-[11px] text-muted">
                {e === "ClusterIP" && intl.formatMessage({ id: "web.routes.create_server.expose_clusterip_hint", defaultMessage: "Internal to cluster only.", description: "Create server: help text under the ClusterIP expose option" })}
                {e === "NodePort" && intl.formatMessage({ id: "web.routes.create_server.expose_nodeport_hint", defaultMessage: "Reach via any node's IP.", description: "Create server: help text under the NodePort expose option" })}
                {e === "LoadBalancer" && intl.formatMessage({ id: "web.routes.create_server.expose_loadbalancer_hint", defaultMessage: "Provisions an external LB.", description: "Create server: help text under the LoadBalancer expose option" })}
              </div>
            </button>
          ))}
        </div>
      </div>

      <div className="space-y-1.5 border-t border-border pt-4">
        <label className="flex items-center gap-2 cursor-pointer">
          <input
            type="checkbox"
            checked={state.tunnelEnabled}
            onChange={(e) => setState({ ...state, tunnelEnabled: e.target.checked })}
            className="h-4 w-4 rounded border border-border bg-surface accent-primary"
          />
          <span className="text-xs font-medium">{intl.formatMessage({ id: "web.routes.create_server.enable_tunnel", defaultMessage: "Enable tunnel", description: "Create server: checkbox label that turns on a tunnel for reaching the server from outside the cluster" })}</span>
        </label>
        {state.tunnelEnabled && (
          <div className="rounded-md border border-border bg-surface/40 p-3 space-y-3">
            <div>
              <label className="block text-xs mb-2">{intl.formatMessage({ id: "web.routes.create_server.tunnel_provider", defaultMessage: "Tunnel provider", description: "Create server: field label above the choice of tunnel provider, shown when the tunnel is enabled" })}</label>
              <div className="grid gap-2 sm:grid-cols-3">
                {(["frp", "tailscale", "playit"] as const).map((p) => (
                  <button
                    key={p}
                    onClick={() => setState({ ...state, tunnelProvider: p })}
                    className={cn(
                      "rounded-md border p-2 text-left text-xs",
                      state.tunnelProvider === p
                        ? "border-primary bg-primary/5"
                        : "border-border hover:bg-surface/60",
                    )}
                  >
                    <div className="font-medium">{p === "tailscale" ? intl.formatMessage({ id: "web.routes.create_server.provider_tailscale", defaultMessage: "Tailscale", description: "Create server: name of the Tailscale tunnel provider option" }) : p === "playit" ? intl.formatMessage({ id: "web.routes.create_server.provider_playit", defaultMessage: "playit.gg", description: "Create server: name of the playit.gg tunnel provider option" }) : intl.formatMessage({ id: "web.routes.create_server.provider_frp", defaultMessage: "frp", description: "Create server: name of the frp tunnel provider option" })}</div>
                  </button>
                ))}
              </div>
            </div>

            <div className="space-y-3">
              <div className="space-y-1.5">
                <label className="block text-xs font-medium">{intl.formatMessage({ id: "web.routes.create_server.credentials", defaultMessage: "Credentials *", description: "Create server: field label for the tunnel credentials; the asterisk marks the field as required" })}</label>
                <Input
                  type="password"
                  value={state.tunnelCredentialsValue}
                  onChange={(e) => setState({ ...state, tunnelCredentialsValue: e.target.value })}
                  placeholder={
                    state.tunnelProvider === "frp"
                      ? intl.formatMessage({ id: "web.routes.create_server.frp_token_placeholder", defaultMessage: "frp token", description: "Create server: placeholder of the credential input when frp is selected" })
                      : state.tunnelProvider === "tailscale"
                        ? intl.formatMessage({ id: "web.routes.create_server.tailscale_auth_key_placeholder", defaultMessage: "Tailscale auth key", description: "Create server: placeholder of the credential input when Tailscale is selected" })
                        : intl.formatMessage({ id: "web.routes.create_server.playit_secret_key_placeholder", defaultMessage: "playit secret key", description: "Create server: placeholder of the credential input when playit.gg is selected" })
                  }
                  data-testid="tunnel-credentials-input"
                />
                <span className="text-[11px] text-muted">
                  {state.tunnelProvider === "frp"
                    ? intl.formatMessage({ id: "web.routes.create_server.frp_token_hint", defaultMessage: "Your frp authentication token", description: "Create server: help text under the credential input when frp is selected" })
                    : state.tunnelProvider === "tailscale"
                      ? intl.formatMessage({ id: "web.routes.create_server.tailscale_token_hint", defaultMessage: "Tailscale OAuth token (usually ts_{id}={secret})", description: "Create server: help text under the credential input when Tailscale is selected; {id} is the key id and {secret} the secret, shown as <id> and <secret>" }, { id: "<id>", secret: "<secret>" })
                      : intl.formatMessage({ id: "web.routes.create_server.playit_secret_key_hint", defaultMessage: "Your playit.gg secret key", description: "Create server: help text under the credential input when playit.gg is selected" })}
                </span>
              </div>

              <div className="border-t border-border/50 pt-3 space-y-1.5">
                <label className="block text-xs">{intl.formatMessage({ id: "web.routes.create_server.or_use_existing_secret_gitops", defaultMessage: "Or use existing Secret (GitOps)", description: "Create server: field label for an option to reuse an existing Kubernetes Secret for the tunnel credentials instead of entering them" })}</label>
                <Input
                  value={state.tunnelCredentialsSecretName}
                  onChange={(e) => setState({ ...state, tunnelCredentialsSecretName: e.target.value })}
                  placeholder={intl.formatMessage({ id: "web.routes.create_server.my_tunnel_secret", defaultMessage: "my-tunnel-secret", description: "Create server: example placeholder in the existing Secret name field; an example name, not a default" })}
                  data-testid="tunnel-existing-secret"
                />
                <span className="text-[11px] text-muted">{intl.formatMessage({ id: "web.routes.create_server.name_of_a_pre_existing_kubernetes", defaultMessage: "Name of a pre-existing Kubernetes Secret. Leave empty to create a new one from the credentials above.", description: "Create server: hint under the existing Secret name field saying to leave it empty to create a new secret from the credentials above" })}</span>
              </div>
            </div>

            {state.tunnelProvider === "frp" && (
              <div className="space-y-3 pt-2 border-t border-border/50">
                <div className="space-y-1.5">
                  <label className="block text-xs">{intl.formatMessage({ id: "web.routes.create_server.server_address", defaultMessage: "Server address *", description: "Create server: field label for the frp relay server address; the asterisk marks it required; frp is a technical term, usually left untranslated" })}</label>
                  <Input
                    value={state.frpServerAddr}
                    onChange={(e) => setState({ ...state, frpServerAddr: e.target.value })}
                    placeholder={intl.formatMessage({ id: "web.routes.create_server.relay_example_com", defaultMessage: "relay.example.com", description: "Create server: example placeholder in the frp server address field; an example host, not a default" })}
                    data-testid="frp-server-addr"
                  />
                </div>
                <div className="space-y-1.5">
                  <label className="block text-xs">{intl.formatMessage({ id: "web.routes.create_server.server_port", defaultMessage: "Server port", description: "Create server: field label for the port of the frp relay server" })}</label>
                  <Input
                    type="number"
                    min="1"
                    max="65535"
                    value={state.frpServerPort}
                    onChange={(e) => {
                      const val = e.target.value;
                      setState({ ...state, frpServerPort: val === "" ? "" : val });
                    }}
                    placeholder={intl.formatMessage({ id: "web.routes.create_server.frp_server_port_placeholder", defaultMessage: "7000", description: "Create server: placeholder of the frp server port input, an example port number" })}
                    data-testid="frp-server-port"
                  />
                  <span className="text-[11px] text-muted">{intl.formatMessage({ id: "web.routes.create_server.default_7000_must_be_between_1_and_65535", defaultMessage: "Default 7000. Must be between 1 and 65535.", description: "Create server: hint under the frp server port field giving the default port 7000 and the allowed range from 1 to 65535" })}</span>
                </div>
                <div className="space-y-1.5">
                  <label className="block text-xs">{intl.formatMessage({ id: "web.routes.create_server.port_mappings", defaultMessage: "Port mappings *", description: "Create server: field label for the list of frp port mappings; the asterisk marks it required" })}</label>
                  <TunnelPortMappingsEditor
                    values={state.frpRemotePortMappings}
                    onChange={(v) => setState({ ...state, frpRemotePortMappings: v })}
                  />
                  <span className="text-[11px] text-muted">{intl.formatMessage({ id: "web.routes.create_server.map_at_least_one_port_name_to_a_remote", defaultMessage: "Map at least one port name to a remote port (1–65535).", description: "Create server: hint under the port mappings editor saying at least one port name must map to a remote port from 1 to 65535" })}</span>
                </div>
              </div>
            )}

            {state.tunnelProvider === "tailscale" && (
              <div className="space-y-3 pt-2 border-t border-border/50">
                <p className="text-[11px] text-warning bg-warning/10 rounded px-2 py-1.5">{intl.formatMessage({ id: "web.routes.create_server.tailscale_access_is_tailnet_private", defaultMessage: "Tailscale access is tailnet-private only and not reachable from the internet.", description: "Create server: warning note in the Tailscale section saying access stays private to the tailnet and cannot be reached from the internet; Tailscale is a technical term" })}</p>
                <div className="space-y-1.5">
                  <label className="block text-xs">{intl.formatMessage({ id: "web.routes.create_server.hostname_optional", defaultMessage: "Hostname (optional)", description: "Create server: field label for the optional hostname, shown in the Tailscale section and in the general network settings" })}</label>
                  <Input
                    value={state.tailscaleHostname}
                    onChange={(e) => setState({ ...state, tailscaleHostname: e.target.value })}
                    placeholder={intl.formatMessage({ id: "web.routes.create_server.my_server", defaultMessage: "my-server", description: "Create server: example placeholder in the Tailscale hostname field; an example value, not a default" })}
                    data-testid="tailscale-hostname"
                  />
                </div>
                <div className="space-y-1.5">
                  <label className="block text-xs">{intl.formatMessage({ id: "web.routes.create_server.tags_optional", defaultMessage: "Tags (optional)", description: "Create server: field label for optional Tailscale tags; Tailscale is a technical term, usually left untranslated" })}</label>
                  <Input
                    value={state.tailscaleTags}
                    onChange={(e) => setState({ ...state, tailscaleTags: e.target.value })}
                    placeholder={intl.formatMessage({ id: "web.routes.create_server.tag_game_tag_public", defaultMessage: "tag:game,tag:public", description: "Create server: example placeholder in the Tailscale tags field showing the tag format; an example value, not a default" })}
                    data-testid="tailscale-tags"
                  />
                  <span className="text-[11px] text-muted">{intl.formatMessage({ id: "web.routes.create_server.comma_or_space_separated", defaultMessage: "Comma or space separated.", description: "Create server: hint under the Tailscale tags field saying the tags can be separated by commas or spaces" })}</span>
                </div>
              </div>
            )}

            {state.tunnelProvider === "playit" && (
              <div className="space-y-3 pt-2 border-t border-border/50">
                <div className="space-y-1.5">
                  <label className="block text-xs">{intl.formatMessage({ id: "web.routes.create_server.tunnel_name_optional", defaultMessage: "Tunnel name (optional)", description: "Create server: field label for the optional playit tunnel name; playit is a technical term, usually left untranslated" })}</label>
                  <Input
                    value={state.playitTunnelName}
                    onChange={(e) => setState({ ...state, playitTunnelName: e.target.value })}
                    placeholder={intl.formatMessage({ id: "web.routes.create_server.my_game_server", defaultMessage: "my-game-server", description: "Create server: example placeholder in the playit tunnel name field; an example value, not a default" })}
                    data-testid="playit-tunnel-name"
                  />
                </div>
              </div>
            )}
          </div>
        )}
      </div>

      <div className="space-y-1.5">
        <label className="block text-xs text-muted">{intl.formatMessage({ id: "web.routes.create_server.hostname_optional", defaultMessage: "Hostname (optional)", description: "Create server: field label for the optional hostname, shown in the Tailscale section and in the general network settings" })}</label>
        <Input
          value={state.hostname}
          onChange={(e) => setState({ ...state, hostname: e.target.value })}
          placeholder={intl.formatMessage({ id: "web.routes.create_server.mc_example_dev", defaultMessage: "mc.example.dev", description: "Create server: example placeholder in the optional hostname field; an example value, not a default" })}
        />
      </div>
      {/* A div, not a label: the editor holds several inputs and buttons, and
          a label wrapping multiple interactive elements mis-associates them. */}
      <div className="space-y-1.5">
        <label className="block text-xs text-muted">{intl.formatMessage({ id: "web.routes.create_server.port_overrides_optional", defaultMessage: "Port overrides (optional)", description: "Create server: field label for optional port overrides that pin a NodePort or change the Service port for a named template port; NodePort is a technical term" })}</label>
        <PortOverridesEditor
          values={state.portOverrides}
          onChange={(v) => setState({ ...state, portOverrides: v })}
        />
        <span className="block text-[11px] text-muted">{intl.formatMessage({ id: "web.routes.create_server.pin_a_nodeport_or_override_the_service", defaultMessage: "Pin a NodePort or override the Service port for a named template port.", description: "Create server: hint under the port overrides editor explaining that overrides pin a NodePort or change the Service port for a template port" })}</span>
      </div>
      {state.expose === "LoadBalancer" && (
        <div className="space-y-1.5">
          <label className="block text-xs text-muted">{intl.formatMessage({ id: "web.routes.create_server.ip_allow_list_cidrs_optional", defaultMessage: "IP allow-list (CIDRs, optional)", description: "Create server: field label for the optional IP allow-list, shown only when the expose choice is LoadBalancer; CIDR is a technical term" })}</label>
          <textarea
            className="block w-full rounded-md border border-border bg-surface px-3 py-2 font-mono text-sm"
            rows={3}
            value={state.sourceRanges}
            onChange={(e) => setState({ ...state, sourceRanges: e.target.value })}
            placeholder={intl.formatMessage({ id: "web.routes.create_server.source_ranges_placeholder", defaultMessage: "203.0.113.0/24\n10.0.0.0/8", description: "Create server: placeholder of the IP allow-list textarea, example CIDR ranges one per line" })}
            aria-label={intl.formatMessage({ id: "web.routes.create_server.ip_allow_list", defaultMessage: "IP allow-list", description: "Create server: accessible label of the multi-line IP allow-list box, read by screen readers; the same text as the visible field label" })}
          />
          <span className="text-[11px] text-muted">{intl.formatMessage({ id: "web.routes.create_server.one_cidr_per_line_restricts_which", defaultMessage: "One CIDR per line. Restricts which clients reach the LoadBalancer; empty allows all.", description: "Create server: hint under the IP allow-list box saying one CIDR goes on each line and an empty list allows all clients to reach the LoadBalancer" })}</span>
        </div>
      )}

      <div className="space-y-1.5 border-t border-border pt-4">
        <label className="block text-xs text-muted">{intl.formatMessage({ id: "web.routes.create_server.load_balancer_address_optional", defaultMessage: "Load balancer address (optional)", description: "Create server: label of the optional load balancer address group, which holds the address pool and requested address fields" })}</label>
        <div className="grid gap-3 sm:grid-cols-2">
          <div className="space-y-1">
            <label className="block text-xs font-medium">{intl.formatMessage({ id: "web.routes.create_server.address_pool", defaultMessage: "Address pool", description: "Create server: field label for the load balancer address pool the server draws its address from" })}</label>
            <Input
              value={state.addressPool}
              onChange={(e) => setState({ ...state, addressPool: e.target.value })}
              placeholder={intl.formatMessage({ id: "web.routes.create_server.pool_us_west", defaultMessage: "pool-us-west", description: "Create server: example placeholder in the address pool name field; an example name, not a default" })}
            />
          </div>
          <div className="space-y-1">
            <label className="block text-xs font-medium">{intl.formatMessage({ id: "web.routes.create_server.requested_address", defaultMessage: "Requested address", description: "Create server: field label for an optional specific load balancer address the server should request" })}</label>
            <Input
              value={state.requestedAddress}
              onChange={(e) => setState({ ...state, requestedAddress: e.target.value })}
              placeholder={intl.formatMessage({ id: "web.routes.create_server.requested_address_placeholder", defaultMessage: "203.0.113.50", description: "Create server: placeholder of the requested load balancer address input, an example IP" })}
            />
          </div>
        </div>
        <span className="text-[11px] text-muted">{intl.formatMessage({ id: "web.routes.create_server.name_of_a_load_balancer_address_pool", defaultMessage: "Name of a load-balancer address pool configured by your cluster admin, plus an optional specific address to request. Applies only when Expose is set to LoadBalancer — ignored otherwise.", description: "Create server: hint under the address pool and requested address fields; they apply only when Expose is LoadBalancer and are ignored otherwise" })}</span>

        {(state.addressPool.trim() || state.requestedAddress.trim()) && state.expose !== "LoadBalancer" && (
          <div className="flex items-start gap-4 rounded-lg border border-warning-soft-foreground bg-warning-soft px-5 py-4">
            <Megaphone className="mt-0.5 h-4 w-4 shrink-0 text-warning-soft-foreground" />
            <div className="flex-1">
              <div className="mb-0.5 font-medium text-warning-soft-foreground">{intl.formatMessage({ id: "web.routes.create_server.address_preference_ignored", defaultMessage: "Address preference ignored", description: "Create server: warning title of a notice shown when an address pool or requested address is set but Expose is not LoadBalancer" })}</div>
              <div className="text-sm text-warning-soft-foreground">{intl.formatMessage({ id: "web.routes.create_server.expose_is_set_to_expose_address_pool", defaultMessage: "Expose is set to {expose}. Address pool and requested address only take effect when Expose (above) is set to LoadBalancer.", description: "Create server: note that address pool and requested address apply only to LoadBalancer; {expose} is the currently selected expose type" }, { expose: state.expose })}</div>
            </div>
          </div>
        )}
      </div>
    </div>
  );
}

function TunnelPortMappingsEditor({
  values,
  onChange,
}: {
  values: Array<{ name: string; remotePort: number }>;
  onChange: (v: Array<{ name: string; remotePort: number }>) => void;
}) {
  const intl = useOptionalIntl();
  return (
    <div className="space-y-2">
      {values.map((item, i) => (
        <div key={i} className="flex gap-2 items-end">
          <div className="flex-1 space-y-1">
            <label className="text-[11px] text-muted">{intl.formatMessage({ id: "web.routes.create_server.port_name", defaultMessage: "Port name", description: "Create server: small field label for the port name in a tunnel port mapping row" })}</label>
            <Input
              value={item.name}
              onChange={(e) => {
                const newValues = [...values];
                newValues[i] = { ...item, name: e.target.value };
                onChange(newValues);
              }}
              placeholder={intl.formatMessage({ id: "web.routes.create_server.game_port", defaultMessage: "game-port", description: "Create server: example placeholder in a tunnel port mapping's port name field; an example value, not a default" })}
              data-testid={`tunnel-port-name-${i}`}
            />
          </div>
          <div className="flex-1 space-y-1">
            <label className="text-[11px] text-muted">{intl.formatMessage({ id: "web.routes.create_server.remote_port", defaultMessage: "Remote port", description: "Create server: small field label for the remote port number in a tunnel port mapping row" })}</label>
            <Input
              type="number"
              min="1"
              max="65535"
              value={item.remotePort}
              onChange={(e) => {
                const newValues = [...values];
                const parsed = parseInt(e.target.value, 10);
                newValues[i] = { ...item, remotePort: isNaN(parsed) ? 0 : parsed };
                onChange(newValues);
              }}
              placeholder={intl.formatMessage({ id: "web.routes.create_server.tunnel_port_placeholder", defaultMessage: "25565", description: "Create server: placeholder of the tunnel remote port input, an example port number" })}
              data-testid={`tunnel-port-number-${i}`}
            />
          </div>
          <Button
            size="sm"
            variant="danger"
            isIconOnly
            onPress={() => onChange(values.filter((_, idx) => idx !== i))}
            aria-label={intl.formatMessage({ id: "web.routes.create_server.delete", defaultMessage: "Delete", description: "Create server: icon-only button that removes one tunnel port mapping row; read by screen readers; keep it short" })}
            data-testid={`tunnel-port-delete-${i}`}
          >
            <X className="h-4 w-4" />
          </Button>
        </div>
      ))}
      <Button
        size="sm"
        variant="primary"
        onPress={() => onChange([...values, { name: "", remotePort: 0 }])}
        data-testid="tunnel-port-add"
      >{intl.formatMessage({ id: "web.routes.create_server.add_port_mapping", defaultMessage: "Add port mapping", description: "Create server: button that adds a new empty tunnel port mapping row to the list; keep it short" })}</Button>
    </div>
  );
}

const REVIEW_LABELS = defineMessages({
  template: { id: "web.routes.create_server.review_template", defaultMessage: "Template", description: "Create server: review section title and row label for the chosen game template" },
  version: { id: "web.routes.create_server.review_version", defaultMessage: "Version", description: "Create server: review section title and row label for the chosen game version" },
  configuration: { id: "web.routes.create_server.review_configuration", defaultMessage: "Configuration", description: "Create server: review section title for the server name, location and resources" },
  name: { id: "web.routes.create_server.review_name", defaultMessage: "Name", description: "Create server: review row label for the server name" },
  location: { id: "web.routes.create_server.review_location", defaultMessage: "Location", description: "Create server: review row label for the cluster and namespace the server is created in" },
  cpu: { id: "web.routes.create_server.review_cpu", defaultMessage: "CPU", description: "Create server: review row label for the CPU limit" },
  memory: { id: "web.routes.create_server.review_memory", defaultMessage: "Memory", description: "Create server: review row label for the memory limit" },
  cpu_reserved: { id: "web.routes.create_server.review_cpu_reserved", defaultMessage: "Reserved CPU", description: "Create server: review row label for the CPU reserved for the server (the Kubernetes request), shown when the reserve-less switch is on" },
  memory_reserved: { id: "web.routes.create_server.review_memory_reserved", defaultMessage: "Reserved memory", description: "Create server: review row label for the memory reserved for the server (the Kubernetes request), shown when the reserve-less switch is on" },
  storage: { id: "web.routes.create_server.review_storage", defaultMessage: "Storage", description: "Create server: review row label for the storage size" },
  placement: { id: "web.routes.create_server.review_placement", defaultMessage: "Placement", description: "Create server: review row label for the node placement option" },
  network: { id: "web.routes.create_server.review_network", defaultMessage: "Network", description: "Create server: review section title for the network settings" },
  expose: { id: "web.routes.create_server.review_expose", defaultMessage: "Expose", description: "Create server: review row label for how the server is exposed" },
  hostname: { id: "web.routes.create_server.review_hostname", defaultMessage: "Hostname", description: "Create server: review row label for the server hostname" },
  tunnel_provider: { id: "web.routes.create_server.review_tunnel_provider", defaultMessage: "Tunnel provider", description: "Create server: review row label for the tunnel provider" },
  tunnel_credentials: { id: "web.routes.create_server.review_tunnel_credentials", defaultMessage: "Tunnel credentials", description: "Create server: review row label for the tunnel credentials" },
  credentials_configured: { id: "web.routes.create_server.review_credentials_configured", defaultMessage: "✓ Configured", description: "Create server: review value shown when tunnel credentials were entered" },
  frp_server: { id: "web.routes.create_server.review_frp_server", defaultMessage: "frp server", description: "Create server: review row label for the frp server address" },
  frp_port_mappings: { id: "web.routes.create_server.review_frp_port_mappings", defaultMessage: "frp port mappings", description: "Create server: review row label for the frp remote port mappings" },
  tailscale_hostname: { id: "web.routes.create_server.review_tailscale_hostname", defaultMessage: "Tailscale hostname", description: "Create server: review row label for the Tailscale hostname" },
  tailscale_tags: { id: "web.routes.create_server.review_tailscale_tags", defaultMessage: "Tailscale tags", description: "Create server: review row label for the Tailscale tags" },
  playit_tunnel_name: { id: "web.routes.create_server.review_playit_tunnel_name", defaultMessage: "playit tunnel name", description: "Create server: review row label for the playit.gg tunnel name" },
  port_overrides: { id: "web.routes.create_server.review_port_overrides", defaultMessage: "Port overrides", description: "Create server: review row label for the port override names" },
  ip_allow_list: { id: "web.routes.create_server.review_ip_allow_list", defaultMessage: "IP allow-list", description: "Create server: review row label for the load balancer source IP allow-list" },
  all_sources: { id: "web.routes.create_server.review_all_sources", defaultMessage: "all", description: "Create server: review value for the IP allow-list when no source range is set, meaning every address is allowed" },
});

const PLACEMENT_LABELS = defineMessages({
  auto: { id: "web.routes.create_server.node_placement_auto", defaultMessage: "Auto (scheduler)", description: "Create server: node placement option letting the scheduler pick a node" },
  pin: { id: "web.routes.create_server.node_placement_pin", defaultMessage: "Pin to node", description: "Create server: node placement option pinning the server to one node" },
  gpu: { id: "web.routes.create_server.node_placement_gpu", defaultMessage: "GPU-enabled", description: "Create server: node placement option requiring a GPU-capable node" },
});

/**
 * Review step: a read-only summary of every earlier step, with an Edit link
 * per section. Password-type config values are masked, and the reservation
 * rows appear only when the user reserves less than the limits.
 */
function Review({ state, onEdit, location, cluster }: { state: WizardState; onEdit: (key: StepKey) => void; location: string; cluster: string }) {
  const intl = useOptionalIntl();
  const text = useModuleText(state.template?.metadata.name, cluster);
  const hasVersions = (state.template?.spec.versions?.length ?? 0) > 0;
  const empty = intl.formatMessage({ id: "web.common.empty_value", defaultMessage: "—", description: "Shown in place of a value that is not set" });
  const sections = [
    {
      key: "template",
      title: intl.formatMessage(REVIEW_LABELS.template),
      rows: [[intl.formatMessage(REVIEW_LABELS.template), state.template ? text(moduleIds.templateDisplayName, state.template.spec.displayName) : empty]] as Array<[string, string]>,
    },
    ...(hasVersions
      ? [{ key: "version", title: intl.formatMessage(REVIEW_LABELS.version), rows: [[intl.formatMessage(REVIEW_LABELS.version), state.version || empty]] as Array<[string, string]> }]
      : []),
    {
      key: "configure",
      title: intl.formatMessage(REVIEW_LABELS.configuration),
      rows: [
        [intl.formatMessage(REVIEW_LABELS.name), state.name || empty],
        [intl.formatMessage(REVIEW_LABELS.location), location],
        [intl.formatMessage(REVIEW_LABELS.cpu), state.cpuLimit],
        [intl.formatMessage(REVIEW_LABELS.memory), state.memoryLimit],
        ...(state.reserveLess
          ? [
              [intl.formatMessage(REVIEW_LABELS.cpu_reserved), state.cpuRequest],
              [intl.formatMessage(REVIEW_LABELS.memory_reserved), state.memoryRequest],
            ]
          : []),
        [intl.formatMessage(REVIEW_LABELS.storage), state.storageSize],
        [intl.formatMessage(REVIEW_LABELS.placement), intl.formatMessage(PLACEMENT_LABELS[state.nodePlacement])],
      ] as Array<[string, string]>,
    },
    {
      key: "network",
      title: intl.formatMessage(REVIEW_LABELS.network),
      rows: [
        [intl.formatMessage(REVIEW_LABELS.expose), state.expose],
        [intl.formatMessage(REVIEW_LABELS.hostname), state.hostname || empty],
        ...(state.tunnelEnabled
          ? [
              [intl.formatMessage(REVIEW_LABELS.tunnel_provider), state.tunnelProvider === "tailscale" ? "Tailscale" : state.tunnelProvider === "playit" ? "playit.gg" : "frp"] as [string, string],
              [intl.formatMessage(REVIEW_LABELS.tunnel_credentials), state.tunnelCredentialsValue.trim()
                ? intl.formatMessage(REVIEW_LABELS.credentials_configured)
                : state.tunnelCredentialsSecretName
                  ? intl.formatMessage({ id: "web.routes.create_server.review_credentials_secret", defaultMessage: "Secret: {name}", description: "Create server: review value naming the existing tunnel Secret; {name} is the Secret name" }, { name: state.tunnelCredentialsSecretName })
                  : empty] as [string, string],
              ...(state.tunnelProvider === "frp"
                ? [
                    [intl.formatMessage(REVIEW_LABELS.frp_server), state.frpServerAddr || empty] as [string, string],
                    ...(nonEmptyTunnelPortMappings(state.frpRemotePortMappings).length > 0
                      ? [[
                          intl.formatMessage(REVIEW_LABELS.frp_port_mappings),
                          nonEmptyTunnelPortMappings(state.frpRemotePortMappings)
                            .map((m) => `${m.name}→${m.remotePort}`)
                            .join(", "),
                        ] as [string, string]]
                      : []),
                  ]
                : []),
              ...(state.tunnelProvider === "tailscale"
                ? [
                    ...(state.tailscaleHostname ? [[intl.formatMessage(REVIEW_LABELS.tailscale_hostname), state.tailscaleHostname] as [string, string]] : []),
                    ...(state.tailscaleTags ? [[intl.formatMessage(REVIEW_LABELS.tailscale_tags), state.tailscaleTags] as [string, string]] : []),
                  ]
                : []),
              ...(state.tunnelProvider === "playit"
                ? [
                    ...(state.playitTunnelName ? [[intl.formatMessage(REVIEW_LABELS.playit_tunnel_name), state.playitTunnelName] as [string, string]] : []),
                  ]
                : []),
            ]
          : []),
        ...(nonEmptyPortOverrides(state.portOverrides).length > 0
          ? [[
              intl.formatMessage(REVIEW_LABELS.port_overrides),
              nonEmptyPortOverrides(state.portOverrides)
                .map((p) => p.name)
                .join(", "),
            ] as [string, string]]
          : []),
        ...(state.expose === "LoadBalancer"
          ? [[intl.formatMessage(REVIEW_LABELS.ip_allow_list), parseSourceRanges(state.sourceRanges).join(", ") || intl.formatMessage(REVIEW_LABELS.all_sources)] as [string, string]]
          : []),
      ] as Array<[string, string]>,
    },
  ] as Array<{ key: StepKey; title: string; rows: Array<[string, string]> }>;
  return (
    <div className="space-y-4 text-sm">
      {sections.map((sec) => (
        <div key={sec.key}>
          <div className="flex items-center justify-between pb-1">
            <span className="text-xs uppercase tracking-wide text-muted">{sec.title}</span>
            <Button
              variant="ghost"
              size="sm"
              onPress={() => onEdit(sec.key)}
            >{intl.formatMessage({ id: "web.routes.create_server.edit", defaultMessage: "Edit", description: "Create server: review step button that opens the matching wizard step so that section can be changed; keep it short" })}</Button>
          </div>
          {sec.rows.map(([k, v]) => (
            <div key={k} className="flex justify-between border-b border-border py-1.5">
              <span className="text-muted">{k}</span>
              <span className="font-mono">{v}</span>
            </div>
          ))}
        </div>
      ))}
      {Object.keys(state.config).length > 0 && (
        <div className="pt-1">
          <div className="pb-1 text-xs uppercase text-muted">{intl.formatMessage({ id: "web.routes.create_server.template_config", defaultMessage: "Template config", description: "Create server: small uppercase heading above the code block showing the chosen template configuration values on the review step" })}</div>
          <pre className="max-h-64 overflow-auto rounded bg-surface p-3 font-mono text-xs scrollbar-thin">
            {JSON.stringify(maskPasswordConfig(state.template?.spec.configSchema ?? [], state.config), null, 2)}
          </pre>
        </div>
      )}
    </div>
  );
}

function Preview({
  state,
  gameCodes,
  byName,
  cluster,
}: {
  state: WizardState;
  gameCodes: Map<string, string>;
  byName: Map<string, GameTemplate>;
  cluster: string;
}) {
  const intl = useOptionalIntl();
  const text = useModuleText(state.template?.metadata.name, cluster);
  const name = state.template ? text(moduleIds.templateDisplayName, state.template.spec.displayName) : intl.formatMessage({ id: "web.routes.create_server.pick_a_template", defaultMessage: "Pick a template", description: "Create server: preview pane title shown until a template is picked" });
  const yaml = state.template
    ? `apiVersion: gameplane.local/v1alpha1
kind: GameServer
metadata:
  name: ${state.name || "server-name"}
spec:
  templateRef:
    name: ${state.template.metadata.name}
${state.version ? `  version: ${state.version}\n` : ""}  resources:
    cpu: ${state.cpuLimit}
    memory: ${state.memoryLimit}
    storage: ${state.storageSize}
`
    : intl.formatMessage({ id: "web.routes.create_server.yaml_pick_a_template", defaultMessage: "# Pick a template to preview the YAML.", description: "Create server: YAML preview comment shown until a template is picked" });
  return (
    <aside className="min-w-0 rounded-lg border border-border bg-surface/40 flex flex-col">
      <div className="flex items-center gap-2 border-b border-border px-4 py-3 min-w-0">
        <GameIcon
          game={state.template?.spec.game}
          icon={state.template ? byName.get(state.template.metadata.name)?.spec.icon : undefined}
          code={state.template ? gameCodes.get(state.template.metadata.name) : undefined}
          size="sm"
        />
        <div className="min-w-0">
          <div className="truncate text-sm font-medium">{name}</div>
          <div className="truncate text-[10px] text-muted">{intl.formatMessage({ id: "web.routes.create_server.template", defaultMessage: "Template:", description: "Create server: label before the template name in the YAML preview header; shows a dash when no template is chosen yet" })} {state.template?.metadata.name ?? intl.formatMessage({ id: "web.common.empty_value", defaultMessage: "—", description: "Shown in place of a value that is not set" })}
          </div>
        </div>
      </div>
      {/* whitespace-pre-wrap + break-words: wrap long lines (e.g. a long
          server/template name) instead of growing a horizontal scrollbar —
          the pane must fit its column at any width. overflow-auto is still
          needed for vertical scroll past max-h-72. */}
      <pre className="min-w-0 max-h-72 overflow-auto whitespace-pre-wrap break-words px-4 py-3 font-mono text-[11px] leading-relaxed text-fg scrollbar-thin">
{yaml}
      </pre>
      <div className="min-w-0 border-t border-border px-4 py-3 text-[11px] text-muted">
        <span className="font-medium text-fg">{intl.formatMessage({ id: "web.routes.create_server.memory_tip", defaultMessage: "Memory tip.", description: "Create server: bold label that starts the memory hint at the bottom of the YAML preview panel" })}</span> {intl.formatMessage({ id: "web.routes.create_server.factor_in_mod_plugin_overhead_minecraft", defaultMessage: "Factor in mod/plugin overhead; Minecraft Vanilla uses ~1.5 GB at 4 players.", description: "Create server: memory hint in the YAML preview panel asking the user to allow for mod or plugin overhead; the example is about 1.5 GB for 4 Minecraft Vanilla players" })}</div>
    </aside>
  );
}
