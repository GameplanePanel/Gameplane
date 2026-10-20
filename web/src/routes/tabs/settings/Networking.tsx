import { useResourceClient, useResourceTarget, resourceKey, serverLink } from "@/lib/resourceTarget";
import {
  Input,
  Button,
  Select,
  ListBox,
  ListBoxItem,
  Switch,
} from "@heroui/react";
import { X, AlertCircle, Check, Loader2, Megaphone } from "lucide-react";
import { useEffect, useState } from "react";
import { Link } from "@tanstack/react-router";
import { useMutation, useQuery } from "@tanstack/react-query";
import { defineMessages, type MessageDescriptor } from "react-intl";
import type { Expose, GameServer, GameServerNetworking, GameServerTunnel, RemotePortMapping } from "@/types";
import { PortOverridesEditor } from "@/components/server/PortOverridesEditor";

import { errorTextIntl } from "@/lib/errors";
import { Field } from "./Field";
import type { SectionProps } from "./types";
import { useOptionalIntl } from "@/lib/i18n/format";
import { conditionMessageText } from "@/lib/i18n/conditionMessage";

const EXPOSE_LABELS = defineMessages({
  clusterIp: { id: "web.routes.tabs.settings.networking.expose_cluster_ip", defaultMessage: "ClusterIP (in-cluster only)", description: "Networking: Expose option label for ClusterIP, a service reachable only inside the cluster" },
  nodePort: { id: "web.routes.tabs.settings.networking.expose_node_port", defaultMessage: "NodePort", description: "Networking: Expose option label for NodePort (Kubernetes service type name)" },
  loadBalancer: { id: "web.routes.tabs.settings.networking.expose_load_balancer", defaultMessage: "LoadBalancer", description: "Networking: Expose option label for LoadBalancer (Kubernetes service type name)" },
  hostport: { id: "web.routes.tabs.settings.networking.expose_hostport", defaultMessage: "Hostport", description: "Networking: Expose option label for Hostport, a port bound on the node host" },
});

const BADGE_LABELS = defineMessages({
  assigned: { id: "web.routes.tabs.settings.networking.badge_assigned", defaultMessage: "Assigned", description: "Networking: address status badge when the address is assigned" },
  pending: { id: "web.routes.tabs.settings.networking.badge_pending", defaultMessage: "Pending", description: "Networking: address status badge when the address assignment is still pending" },
  ignored: { id: "web.routes.tabs.settings.networking.badge_ignored", defaultMessage: "Ignored", description: "Networking: address status badge when the address preference is ignored in the current expose mode" },
  error: { id: "web.routes.tabs.settings.networking.badge_error", defaultMessage: "Error", description: "Networking: address status badge when address assignment failed" },
});

const CREDENTIAL_LABELS = defineMessages({
  frp: { id: "web.routes.tabs.settings.networking.credential_label_frp", defaultMessage: "frp token", description: "Networking: name of the frp credential, shown as the credential input placeholder and in the help text" },
  tailscale: { id: "web.routes.tabs.settings.networking.credential_label_tailscale", defaultMessage: "Tailscale auth key", description: "Networking: name of the Tailscale credential, shown as the credential input placeholder and in the help text" },
  playit: { id: "web.routes.tabs.settings.networking.credential_label_playit", defaultMessage: "playit secret key", description: "Networking: name of the playit.gg credential, shown as the credential input placeholder and in the help text" },
});

const EXPOSE_OPTIONS: Array<{ value: Expose; label: MessageDescriptor }> = [
  { value: "ClusterIP", label: EXPOSE_LABELS.clusterIp },
  { value: "NodePort", label: EXPOSE_LABELS.nodePort },
  { value: "LoadBalancer", label: EXPOSE_LABELS.loadBalancer },
  { value: "Hostport", label: EXPOSE_LABELS.hostport },
];

const TUNNEL_PROVIDER_OPTIONS: Array<{ value: "frp" | "tailscale" | "playit"; label: string }> = [
  { value: "frp", label: "frp" },
  { value: "tailscale", label: "Tailscale" },
  { value: "playit", label: "playit.gg" },
];

export function NetworkingSection({ draft, onChange, onValidityChange }: SectionProps) {
  const intl = useOptionalIntl();
  const resourceTarget = useResourceTarget({ name: draft.metadata.name, namespace: draft.metadata.namespace });
  const resourceClient = useResourceClient(resourceTarget);
  const { Servers } = resourceClient;
  const net = draft.spec.networking ?? {};
  const tunnel = net.tunnel;
  const serverName = draft.metadata.name;
  const serverNamespace = draft.metadata.namespace;

  // Credential entry state — NOT part of the spec form's dirty tracking.
  const [showCredentialInput, setShowCredentialInput] = useState(false);
  const [credentialValue, setCredentialValue] = useState("");

  // Query current tunnel credentials status.
  const { data: credentialStatus, refetch: refetchCredentials } = useQuery({
    queryKey: resourceKey(resourceTarget, "tunnel-credentials", serverName),
    queryFn: ({ signal }) => resourceClient.withSignal(signal).Servers.getTunnelCredentials(serverName, serverNamespace),
    enabled: tunnel?.enabled ?? false,
  });

  const setNet = (next: GameServerNetworking) => {
    const cleaned: GameServerNetworking = {};
    if (next.expose) cleaned.expose = next.expose;
    if (next.hostname) cleaned.hostname = next.hostname;
    if (next.serviceAnnotations && Object.keys(next.serviceAnnotations).length) {
      cleaned.serviceAnnotations = next.serviceAnnotations;
    }
    if (next.portOverrides && next.portOverrides.length) {
      cleaned.portOverrides = next.portOverrides;
    }
    if (next.sourceRanges && next.sourceRanges.length) {
      cleaned.sourceRanges = next.sourceRanges;
    }
    if (next.addressPool) cleaned.addressPool = next.addressPool;
    if (next.address) cleaned.address = next.address;
    if (next.tunnel) cleaned.tunnel = next.tunnel;
    onChange({
      ...draft,
      spec: {
        ...draft.spec,
        networking: Object.keys(cleaned).length ? cleaned : undefined,
      },
    });
  };

  // Mutation to save credentials.
  const saveCredentialMutation = useMutation({
    mutationFn: async () => {
      if (!tunnel?.provider || !credentialValue.trim()) {
        throw new Error(intl.formatMessage({ id: "web.routes.tabs.settings.networking.provider_and_credential_required", defaultMessage: "Provider and credential value required", description: "Networking: error shown when a tunnel credential is saved without a provider or a value" }));
      }
      const key =
        tunnel.provider === "frp"
          ? "token"
          : tunnel.provider === "tailscale"
            ? "authKey"
            : "secretKey";
      await Servers.setTunnelCredentials(serverName, tunnel.provider, { [key]: credentialValue }, serverNamespace);
      // Refetch credential status to get the updated secretName.
      void refetchCredentials();
    },
    onSuccess: () => {
      setCredentialValue("");
      setShowCredentialInput(false);
    },
  });

  // Mutation to remove credentials.
  const removeCredentialMutation = useMutation({
    mutationFn: () => Servers.removeTunnelCredentials(serverName, serverNamespace),
    onSuccess: () => {
      // Clear the credentialsSecretRef from the spec.
      setNet({ ...net, tunnel: tunnel ? { ...tunnel, credentialsSecretRef: undefined } : undefined });
      void refetchCredentials();
    },
  });

  // Compute tunnel configuration validity.
  let computedValidityError: string | null = null;
  if (tunnel?.enabled) {
    const errors: string[] = [];

    // Credentials must be configured (either already in spec or being entered).
    const hasConfiguredCredential = tunnel.credentialsSecretRef?.name || credentialStatus?.configured;
    if (!hasConfiguredCredential && !credentialValue.trim()) {
      errors.push(intl.formatMessage({ id: "web.routes.tabs.settings.networking.tunnel_credentials_required", defaultMessage: "Tunnel credentials are required", description: "Networking: validation error when a tunnel is enabled without credentials" }));
    }

    // Provider-specific validation.
    if (tunnel.provider === "frp") {
      if (!tunnel.frp?.serverAddr) {
        errors.push(intl.formatMessage({ id: "web.routes.tabs.settings.networking.frp_server_address_required", defaultMessage: "Server address is required for frp", description: "Networking: validation error when the frp server address is empty" }));
      }
      const ports = tunnel.frp?.remotePorts ?? [];
      if (ports.length === 0) {
        errors.push(intl.formatMessage({ id: "web.routes.tabs.settings.networking.frp_port_mapping_required", defaultMessage: "At least one port mapping is required for frp", description: "Networking: validation error when no frp port mapping is configured" }));
      } else {
        // Check each port mapping is valid.
        for (let i = 0; i < ports.length; i++) {
          const p = ports[i];
          if (!p.name) {
            errors.push(intl.formatMessage({ id: "web.routes.tabs.settings.networking.port_mapping_name_required", defaultMessage: "Port mapping {index}: name is required", description: "Networking: validation error when a port mapping has no name; {index} is the 1-based position of the mapping" }, { index: i + 1 }));
          }
          if (!p.remotePort || p.remotePort < 1 || p.remotePort > 65535) {
            errors.push(intl.formatMessage({ id: "web.routes.tabs.settings.networking.port_mapping_remote_port_range", defaultMessage: "Port mapping {index}: remote port must be 1–65535", description: "Networking: validation error when a port mapping's remote port is out of range; {index} is the 1-based position of the mapping" }, { index: i + 1 }));
          }
        }
      }
    }
    // tailscale and playit have no additional required fields beyond credentials.

    computedValidityError = errors.length === 0 ? null : errors[0];
  }

  // Call validity change callback whenever computed result changes.
  useEffect(() => {
    const isValid = computedValidityError === null;
    onValidityChange?.(isValid);
  }, [computedValidityError, onValidityChange]);

  return (
    <div className="space-y-6">
      <Field
        label={intl.formatMessage({ id: "web.routes.tabs.settings.networking.expose", defaultMessage: "Expose", description: "Networking: field label for the service type that exposes the game pod; keeps applying when a tunnel is enabled" })}
        hint={intl.formatMessage({ id: "web.routes.tabs.settings.networking.service_type_fronting_the_game_pod", defaultMessage: "Service type fronting the game pod. Still applies when a tunnel is enabled.", description: "Networking: hint under the Expose field saying the service type fronts the game pod and still applies when a tunnel is enabled" })}>
        <Select
          selectedKey={net.expose ?? "ClusterIP"}
          onSelectionChange={(v) => setNet({ ...net, expose: v as Expose })}
        >
          <Select.Trigger className="w-full">
            <Select.Value />
            <Select.Indicator />
          </Select.Trigger>
          <Select.Popover>
            <ListBox className="p-0">
              {EXPOSE_OPTIONS.map((opt) => (
                <ListBoxItem key={opt.value} id={opt.value}>
                  {intl.formatMessage(opt.label)}
                </ListBoxItem>
              ))}
            </ListBox>
          </Select.Popover>
        </Select>
      </Field>

      {/* Tunnel section */}
      <div className="border-t border-border pt-6">
        <div className="space-y-4">
          <div className="space-y-2">
            <div className="flex items-center gap-3">
              <Switch
                isSelected={tunnel?.enabled ?? false}
                onChange={(selected) => {
                  const newTunnel: GameServerTunnel | undefined = selected
                    ? { enabled: true, provider: tunnel?.provider ?? "frp" }
                    : undefined;
                  setNet({ ...net, tunnel: newTunnel });
                }}
                aria-label={intl.formatMessage({ id: "web.routes.tabs.settings.networking.enable_tunnel", defaultMessage: "Enable tunnel", description: "Networking: switch that turns a relay tunnel on or off; the text is its accessible name, not shown as text" })}
              >
                <Switch.Content>
                  <Switch.Control>
                    <Switch.Thumb />
                  </Switch.Control>
                </Switch.Content>
              </Switch>
              <span className="text-sm font-medium">{intl.formatMessage({ id: "web.routes.tabs.settings.networking.enable_tunnel", defaultMessage: "Enable tunnel", description: "Networking: switch that turns a relay tunnel on or off; the text is its accessible name, not shown as text" })}</span>
            </div>
            <div className="text-xs text-muted">{intl.formatMessage({ id: "web.routes.tabs.settings.networking.route_players_through_a_relay_so_they", defaultMessage: "Route players through a relay so they can connect without port-forwarding or a public IP.", description: "Networking: hint under the Enable tunnel switch explaining a relay lets players connect without port forwarding or a public IP" })}</div>
          </div>

          {tunnel?.enabled && tunnel && (
            <div className="space-y-4 rounded-lg border border-border bg-surface/40 p-4">
              <Field label={intl.formatMessage({ id: "web.routes.tabs.settings.networking.provider", defaultMessage: "Provider", description: "Networking: field label for the tunnel provider picker, shown when a tunnel is enabled" })} hint="">
                <Select
                  selectedKey={tunnel.provider}
                  onSelectionChange={(v) =>
                    setNet({
                      ...net,
                      tunnel: { ...tunnel, provider: v as "frp" | "tailscale" | "playit" },
                    })
                  }
                >
                  <Select.Trigger className="w-full">
                    <Select.Value />
                    <Select.Indicator />
                  </Select.Trigger>
                  <Select.Popover>
                    <ListBox className="p-0">
                      {TUNNEL_PROVIDER_OPTIONS.map((opt) => (
                        <ListBoxItem key={opt.value} id={opt.value}>
                          {opt.label}
                        </ListBoxItem>
                      ))}
                    </ListBox>
                  </Select.Popover>
                </Select>
              </Field>

              {tunnel.provider === "tailscale" && (
                <div className="rounded-md border-l-4 border-warning bg-warning/10 p-3 text-sm">
                  <div className="flex gap-2">
                    <AlertCircle className="h-5 w-5 shrink-0 text-warning-soft-foreground" />
                    <div>
                      <div className="font-medium text-warning-soft-foreground">{intl.formatMessage({ id: "web.routes.tabs.settings.networking.tailnet_only_not_public", defaultMessage: "Tailnet only — not public", description: "Networking: warning title shown for the Tailscale provider; the line below says the server is reachable only from the Tailscale network" })}</div>
                      <div className="pt-0.5 text-xs text-warning-soft-foreground">{intl.formatMessage({ id: "web.routes.tabs.settings.networking.the_server_becomes_reachable_only_from", defaultMessage: "The server becomes reachable only from your Tailscale network, never from the public internet.", description: "Networking: text under the Tailnet only warning saying the server is reachable only from the user's Tailscale network, never from the public internet" })}</div>
                    </div>
                  </div>
                </div>
              )}

              <TunnelCredentialField
                configured={credentialStatus?.configured}
                secretName={credentialStatus?.secretName}
                isLoading={saveCredentialMutation.isPending || removeCredentialMutation.isPending}
                error={
                  saveCredentialMutation.error
                    ? errorTextIntl(intl, saveCredentialMutation.error, intl.formatMessage({ id: "web.routes.tabs.settings.networking.save_credential_failed", defaultMessage: "Failed to save the credential", description: "Networking: fallback error shown when saving a tunnel credential fails" }))
                    : removeCredentialMutation.error
                      ? errorTextIntl(intl, removeCredentialMutation.error, intl.formatMessage({ id: "web.routes.tabs.settings.networking.remove_credential_failed", defaultMessage: "Failed to remove the credential", description: "Networking: fallback error shown when removing a tunnel credential fails" }))
                      : undefined
                }
                showInput={showCredentialInput}
                onShowInput={() => setShowCredentialInput(true)}
                provider={tunnel.provider}
                credentialValue={credentialValue}
                onCredentialChange={setCredentialValue}
                onSave={() => saveCredentialMutation.mutate()}
                onCancel={() => {
                  setShowCredentialInput(false);
                  setCredentialValue("");
                }}
                onRemove={() => removeCredentialMutation.mutate()}
                hasSecretRef={!!tunnel.credentialsSecretRef?.name}
              />

              {tunnel.provider === "frp" && (
                <>
                  <Field label={intl.formatMessage({ id: "web.routes.tabs.settings.networking.server_address", defaultMessage: "Server address", description: "Networking: field label for the frp relay server address" })} hint={intl.formatMessage({ id: "web.routes.tabs.settings.networking.frp_server_address", defaultMessage: "frp server address.", description: "Networking: hint under the frp server address field; frp is a technical term, usually left untranslated" })}>
                    <Input
                      value={tunnel.frp?.serverAddr ?? ""}
                      onChange={(e) =>
                        setNet({
                          ...net,
                          tunnel: {
                            ...tunnel,
                            frp: {
                              serverAddr: e.target.value,
                              serverPort: tunnel.frp?.serverPort,
                              remotePorts: tunnel.frp?.remotePorts ?? [],
                            },
                          },
                        })
                      }
                      placeholder={intl.formatMessage({ id: "web.routes.tabs.settings.networking.relay_example_com", defaultMessage: "relay.example.com", description: "Networking: example placeholder in the frp server address box; an example host, not a default" })}
                      spellCheck={false}
                    />
                  </Field>
                  <Field label={intl.formatMessage({ id: "web.routes.tabs.settings.networking.server_port", defaultMessage: "Server port", description: "Networking: field label for the port of the frp relay server" })} hint={intl.formatMessage({ id: "web.routes.tabs.settings.networking.frp_server_port_default_7000", defaultMessage: "frp server port (default 7000).", description: "Networking: hint under the frp server port field giving the default port 7000" })}>
                    <Input
                      type="number"
                      value={tunnel.frp?.serverPort ?? 7000}
                      onChange={(e) =>
                        setNet({
                          ...net,
                          tunnel: {
                            ...tunnel,
                            frp: {
                              serverAddr: tunnel.frp?.serverAddr ?? "",
                              serverPort: e.target.value ? Number(e.target.value) : undefined,
                              remotePorts: tunnel.frp?.remotePorts ?? [],
                            },
                          },
                        })
                      }
                      placeholder={intl.formatMessage({ id: "web.routes.tabs.settings.networking.frp_server_port_placeholder", defaultMessage: "7000", description: "Networking: default frp server port shown as the server port input placeholder" })}
                      inputMode="numeric"
                    />
                  </Field>
                  <div>
                    <div className="text-sm text-fg">{intl.formatMessage({ id: "web.routes.tabs.settings.networking.port_mappings", defaultMessage: "Port mappings", description: "Networking: heading of the frp port mappings list; the line under it explains each entry names a game port and its remote port" })}</div>
                    <div className="pt-1 text-xs text-muted">{intl.formatMessage({ id: "web.routes.tabs.settings.networking.name_and_remote_port_for_each_game_port", defaultMessage: "Name and remote port for each game port.", description: "Networking: hint under the port mappings heading saying each entry holds a name and the remote port for each game port" })}</div>
                    <div className="pt-2">
                      <FrpPortMappingEditor
                        values={tunnel.frp?.remotePorts ?? []}
                        onChange={(v) =>
                          setNet({
                            ...net,
                            tunnel: {
                              ...tunnel,
                              frp: {
                                serverAddr: tunnel.frp?.serverAddr ?? "",
                                serverPort: tunnel.frp?.serverPort,
                                remotePorts: v,
                              },
                            },
                          })
                        }
                      />
                    </div>
                  </div>
                </>
              )}

              {tunnel.provider === "tailscale" && (
                <>
                  <Field label={intl.formatMessage({ id: "web.routes.tabs.settings.networking.hostname", defaultMessage: "Hostname", description: "Networking: field label for an optional hostname, used for Tailscale or for ingress and external-dns annotations" })} hint={intl.formatMessage({ id: "web.routes.tabs.settings.networking.hostname_in_your_tailnet_optional", defaultMessage: "Hostname in your tailnet (optional).", description: "Networking: hint under the Tailscale hostname field saying it is the hostname in the tailnet and is optional" })}>
                    <Input
                      value={tunnel.tailscale?.hostname ?? ""}
                      onChange={(e) =>
                        setNet({
                          ...net,
                          tunnel: {
                            ...tunnel,
                            tailscale: {
                              ...tunnel.tailscale,
                              hostname: e.target.value || undefined,
                              tags: tunnel.tailscale?.tags,
                            },
                          },
                        })
                      }
                      placeholder={intl.formatMessage({ id: "web.routes.tabs.settings.networking.game_server", defaultMessage: "game-server", description: "Networking: example placeholder in the Tailscale hostname box; an example value, not a default" })}
                      spellCheck={false}
                    />
                  </Field>
                  <Field label={intl.formatMessage({ id: "web.routes.tabs.settings.networking.tags", defaultMessage: "Tags", description: "Networking: field label for the optional Tailscale tags" })} hint={intl.formatMessage({ id: "web.routes.tabs.settings.networking.comma_separated_tags_optional", defaultMessage: "Comma-separated tags (optional).", description: "Networking: hint under the Tailscale tags field saying tags are comma-separated and optional" })}>
                    <Input
                      value={(tunnel.tailscale?.tags ?? []).join(", ")}
                      onChange={(e) =>
                        setNet({
                          ...net,
                          tunnel: {
                            ...tunnel,
                            tailscale: {
                              hostname: tunnel.tailscale?.hostname,
                              tags: e.target.value
                                ? e.target.value
                                    .split(",")
                                    .map((t) => t.trim())
                                    .filter(Boolean)
                                : undefined,
                            },
                          },
                        })
                      }
                      placeholder={intl.formatMessage({ id: "web.routes.tabs.settings.networking.tag_server_tag_minecraft", defaultMessage: "tag:server, tag:minecraft", description: "Networking: example placeholder in the Tailscale tags box showing the tag format; an example value, not a default" })}
                      spellCheck={false}
                    />
                  </Field>
                </>
              )}

              {tunnel.provider === "playit" && (
                <Field label={intl.formatMessage({ id: "web.routes.tabs.settings.networking.tunnel_name", defaultMessage: "Tunnel name", description: "Label for the playit.gg tunnel name" })} hint={intl.formatMessage({ id: "web.routes.tabs.settings.networking.name_of_the_tunnel_on_playit_gg", defaultMessage: "Name of the tunnel on playit.gg.", description: "Networking: hint under the playit tunnel name field saying it is the name of the tunnel on playit.gg; playit is a technical term" })}>
                  <Input
                    value={tunnel.playit?.tunnelName ?? ""}
                    onChange={(e) =>
                      setNet({
                        ...net,
                        tunnel: {
                          ...tunnel,
                          playit: {
                            tunnelName: e.target.value || undefined,
                          },
                        },
                      })
                    }
                    placeholder={intl.formatMessage({ id: "web.routes.tabs.settings.networking.my_server", defaultMessage: "my-server", description: "Networking: example placeholder in the playit tunnel name box; an example value, not a default" })}
                    spellCheck={false}
                  />
                </Field>
              )}
              {tunnel.provider === "playit" && (
                <div className="rounded-md border-l-4 border-info bg-info/10 p-3 text-sm">
                  <div className="text-xs font-medium text-info">{intl.formatMessage({ id: "web.routes.tabs.settings.networking.public_address_assigned_at_runtime", defaultMessage: "Public address assigned at runtime", description: "Networking: title of the info note for playit saying the public address is assigned at runtime" })}</div>
                  <div className="pt-0.5 text-xs text-info/80">{intl.formatMessage({ id: "web.routes.tabs.settings.networking.the_tunnel_s_public_address_will_be", defaultMessage: "The tunnel's public address will be assigned by playit.gg and displayed on the connection card.", description: "Networking: text under the playit info note saying playit.gg assigns the public address, which then shows on the connection card" })}</div>
                </div>
              )}
              {computedValidityError && (
                <div className="rounded-md border-l-4 border-danger bg-danger/10 p-3 text-sm">
                  <div className="flex gap-2">
                    <AlertCircle className="h-5 w-5 shrink-0 text-danger" />
                    <div className="text-xs text-danger">{computedValidityError}</div>
                  </div>
                </div>
              )}
            </div>
          )}
        </div>
      </div>

      <Field label={intl.formatMessage({ id: "web.routes.tabs.settings.networking.hostname", defaultMessage: "Hostname", description: "Networking: field label for an optional hostname, used for Tailscale or for ingress and external-dns annotations" })} hint={intl.formatMessage({ id: "web.routes.tabs.settings.networking.optional_dns_name_for_ingress_external", defaultMessage: "Optional DNS name for ingress / external-dns annotations.", description: "Networking: hint under the Hostname field of the general network settings, saying it is an optional DNS name for ingress or external-dns annotations" })}>
        <Input
          value={net.hostname ?? ""}
          onChange={(e) => setNet({ ...net, hostname: e.target.value || undefined })}
          placeholder={intl.formatMessage({ id: "web.routes.tabs.settings.networking.mc_example_com", defaultMessage: "mc.example.com", description: "Networking: example placeholder in the general hostname field; an example value, not a default" })}
          spellCheck={false}
        />
      </Field>

      <Field
        label={intl.formatMessage({ id: "web.routes.tabs.settings.networking.service_annotations", defaultMessage: "Service annotations", description: "Networking: field label for the optional annotations added to the Kubernetes Service" })}
        hint={intl.formatMessage({ id: "web.routes.tabs.settings.networking.merged_into_the_service_useful_for", defaultMessage: "Merged into the Service. Useful for cloud LB or external-dns config.", description: "Networking: hint under the service annotations field saying they are merged into the Service and are useful for cloud load balancer or external-dns setup" })}
      >
        <KVEditor
          values={net.serviceAnnotations ?? {}}
          onChange={(v) => setNet({ ...net, serviceAnnotations: v })}
          keyPlaceholder="service.beta.kubernetes.io/…"
          valuePlaceholder={intl.formatMessage({ id: "web.routes.tabs.settings.networking.annotation_value_placeholder", defaultMessage: "value", description: "Networking: placeholder of the value input when adding a service annotation" })}
        />
      </Field>

      <Field
        label={intl.formatMessage({ id: "web.routes.tabs.settings.networking.port_overrides", defaultMessage: "Port overrides", description: "Networking: field label for the port overrides list that pins a NodePort or changes the Service port of a template port" })}
        hint={intl.formatMessage({ id: "web.routes.tabs.settings.networking.pin_a_nodeport_or_override_the_service", defaultMessage: "Pin a NodePort or override the Service port for a named template port.", description: "Networking: hint under the port overrides label explaining that overrides pin a NodePort or change the Service port for a template port" })}
      >
        <PortOverridesEditor
          values={net.portOverrides ?? []}
          onChange={(v) => setNet({ ...net, portOverrides: v })}
        />
      </Field>

      {net.expose === "LoadBalancer" && (
        <Field
          label={intl.formatMessage({ id: "web.routes.tabs.settings.networking.loadbalancer_ip_allow_list", defaultMessage: "LoadBalancer IP allow-list", description: "Networking: field label for the LoadBalancer IP allow-list, shown only when Expose is LoadBalancer; the accessible name of its text box is the same" })}
          hint={intl.formatMessage({ id: "web.routes.tabs.settings.networking.one_cidr_per_line_restricts_which", defaultMessage: "One CIDR per line. Restricts which clients reach the LoadBalancer; empty allows all.", description: "Networking: hint under the IP allow-list saying one CIDR goes on each line and an empty list allows all clients" })}
        >
          <textarea
            className="block w-full rounded-md border border-border bg-surface px-3 py-2 font-mono text-sm"
            rows={3}
            value={(net.sourceRanges ?? []).join("\n")}
            onChange={(e) =>
              setNet({
                ...net,
                sourceRanges: e.target.value
                  .split(/[\n,]/)
                  .map((s) => s.trim())
                  .filter(Boolean),
              })
            }
            placeholder={intl.formatMessage({ id: "web.routes.tabs.settings.networking.allow_list_placeholder", defaultMessage: "203.0.113.0/24\n10.0.0.0/8", description: "Networking: example CIDR ranges, one per line, shown as the LoadBalancer IP allow-list placeholder" })}
            aria-label={intl.formatMessage({ id: "web.routes.tabs.settings.networking.loadbalancer_ip_allow_list", defaultMessage: "LoadBalancer IP allow-list", description: "Networking: field label for the LoadBalancer IP allow-list, shown only when Expose is LoadBalancer; the accessible name of its text box is the same" })}
          />
        </Field>
      )}

      <AddressAssignmentSection
        key={`${draft.metadata.namespace}/${draft.metadata.name}`}
        draft={draft}
        net={net}
        setNet={setNet}
      />
    </div>
  );
}

interface AddressAssignmentSectionProps {
  draft: GameServer;
  net: GameServerNetworking;
  setNet: (next: GameServerNetworking) => void;
}

function AddressAssignmentSection({
  draft,
  net,
  setNet,
}: AddressAssignmentSectionProps) {
  const intl = useOptionalIntl();
  const status = draft.status;
  const condition = status?.conditions?.find(
    (c) => c.type === "AddressAssignment",
  );

  const currentAddress = draft.status?.endpoints?.[0];

  // Locally-tracked field values, seeded from the spec and re-synced whenever
  // the underlying draft changes them (e.g. switching servers, a save
  // round-trip, or SettingsTab.reload replacing draft with server-returned
  // values). Deriving these two fields from `net` alone — the way the rest
  // of this form does — breaks when a caller edits both fields back to back
  // without an intervening re-render carrying the updated draft back in as
  // props: each edit would recompute against the same stale `net` snapshot
  // and the second edit could resurrect the first one's clear. Tracking them
  // locally makes consecutive edits within one render cumulative instead of
  // independently-stale.
  //
  // The parent's `key={namespace/name}` remounts this component on identity
  // change, which re-initializes useState — but identity and value are two
  // different things (two different servers can share the same addressPool
  // string, and the same server's net.addressPool/net.address can change
  // without its identity changing, e.g. reload() or a successful save
  // echoing back a normalized value). So local state is also re-seeded
  // in-render, without an effect, whenever the incoming addressPool/address
  // values differ (by value) from what was last synced from props. This is
  // React's documented "adjust state during render" pattern rather than an
  // effect, so it neither violates react-hooks/set-state-in-effect nor needs
  // its own dependency array to get right.
  const nextAddressPool = net.addressPool ?? "";
  const nextAddress = net.address ?? "";
  const [syncedFrom, setSyncedFrom] = useState({ pool: nextAddressPool, address: nextAddress });
  const [addressPool, setAddressPool] = useState(nextAddressPool);
  const [address, setAddress] = useState(nextAddress);
  if (syncedFrom.pool !== nextAddressPool || syncedFrom.address !== nextAddress) {
    setSyncedFrom({ pool: nextAddressPool, address: nextAddress });
    setAddressPool(nextAddressPool);
    setAddress(nextAddress);
  }

  const updateAddressPool = (value: string) => {
    setAddressPool(value);
    setNet({ ...net, addressPool: value || undefined, address: address || undefined });
  };

  const updateAddress = (value: string) => {
    setAddress(value);
    setNet({ ...net, addressPool: addressPool || undefined, address: value || undefined });
  };

  return (
    <div className="space-y-4 border-t border-border pt-6">
      {currentAddress && (
        <Field
          label={intl.formatMessage({ id: "web.routes.tabs.settings.networking.current_assignment", defaultMessage: "Current assignment", description: "Networking: field label for the address and pool the operator has currently bound to this server" })}
          hint={intl.formatMessage({ id: "web.routes.tabs.settings.networking.address_and_pool_currently_bound_to", defaultMessage: "Address and pool currently bound to this server by the operator.", description: "Networking: hint under the current assignment label saying the address and pool are bound to this server by the operator" })}
        >
          <div className="rounded-md border border-border bg-surface px-3 py-2">
            <div className="flex items-center gap-2">
              <span className="text-sm text-fg">{currentAddress.host}</span>
              {currentAddress.pool && (
                <span className="text-xs text-muted">{intl.formatMessage({ id: "web.routes.tabs.settings.networking.from_pool_pool", defaultMessage: "from pool ''{pool}'", description: "Networking: sentence with pool in span" }, { pool: currentAddress.pool })}</span>
              )}
            </div>
          </div>
        </Field>
      )}

      <Field
        label={intl.formatMessage({ id: "web.routes.tabs.settings.networking.address_pool", defaultMessage: "Address pool", description: "Networking: field label for the load balancer address pool name; the hint says leaving it blank lets the address manager choose" })}
        hint={intl.formatMessage({ id: "web.routes.tabs.settings.networking.name_of_a_load_balancer_address_pool", defaultMessage: "Name of a load-balancer address pool configured by your cluster admin. Leave blank to let the address manager choose.", description: "Networking: hint under the address pool field saying the pool is configured by the cluster admin and blank lets the address manager choose" })}
      >
        <Input
          value={addressPool}
          onChange={(e) => updateAddressPool(e.target.value)}
          placeholder={intl.formatMessage({ id: "web.routes.tabs.settings.networking.pool_name", defaultMessage: "pool-name", description: "Networking: example placeholder in the address pool box; an example name, not a default" })}
          spellCheck={false}
        />
      </Field>

      <Field
        label={intl.formatMessage({ id: "web.routes.tabs.settings.networking.requested_address", defaultMessage: "Requested address", description: "Networking: field label for an optional specific address to request from the address pool" })}
        hint={intl.formatMessage({ id: "web.routes.tabs.settings.networking.a_specific_address_to_request_from_the", defaultMessage: "A specific address to request from the pool. Must be free — the address manager rejects addresses already in use. Leave blank unless you need a specific address.", description: "Networking: hint under the requested address field saying the address must be free and should be left blank unless a specific address is needed" })}
      >
        <Input
          value={address}
          onChange={(e) => updateAddress(e.target.value)}
          placeholder={intl.formatMessage({ id: "web.routes.tabs.settings.networking.e_g_203_0_113_50", defaultMessage: "e.g. 203.0.113.50", description: "Networking: example placeholder in the requested address box; an example address, not a default" })}
          spellCheck={false}
        />
      </Field>

      <AddressStatusField
        condition={condition}
        messageText={conditionMessageText(intl, condition, status?.conditionArgs)}
        serverNamespace={draft.metadata.namespace}
      />

      {condition?.reason === "IgnoredForExposureMode" && (
        <div className="flex items-start gap-4 rounded-lg border border-warning-soft-foreground bg-warning-soft px-5 py-4 text-sm">
          <Megaphone className="mt-0.5 h-4 w-4 shrink-0 text-warning-soft-foreground" />
          <div className="flex-1">
            <div className="mb-0.5 font-medium text-warning-soft-foreground">{intl.formatMessage({ id: "web.routes.tabs.settings.networking.address_preference_ignored", defaultMessage: "Address preference ignored", description: "Networking: notice title shown when an address preference is set but Expose is not LoadBalancer, so the preference has no effect; the line below names the Expose value" })}</div>
            <div className="text-xs text-warning-soft-foreground">{intl.formatMessage({ id: "web.routes.tabs.settings.networking.address_pool_and_requested_address_only", defaultMessage: "Expose is set to {expose}. Address pool and requested address only take effect when Expose (above) is set to LoadBalancer.", description: "Networking: note under the ignored address preference banner; {expose} is the current Expose type, such as ClusterIP" }, { expose: net.expose || "ClusterIP" })}</div>
          </div>
        </div>
      )}

      {condition?.reason === "NoAddressManagerConfigured" && (
        <div className="rounded-md border-l-4 border-warning bg-warning/10 p-3 text-sm">
          <div className="flex gap-2">
            <AlertCircle className="h-5 w-5 shrink-0 text-warning-soft-foreground" />
            <div>
              <div className="font-medium text-warning-soft-foreground">{intl.formatMessage({ id: "web.routes.tabs.settings.networking.no_address_manager_configured", defaultMessage: "No address manager configured", description: "Networking: warning title shown when the cluster has no address manager; the line below says the pool and address preference will be saved but not applied" })}</div>
              <div className="pt-0.5 text-xs text-warning-soft-foreground">{intl.formatMessage({ id: "web.routes.tabs.settings.networking.this_cluster_has_no_metallb_or_cilium", defaultMessage: "This cluster has no MetalLB or Cilium address manager. Your pool and address preference will be saved but never applied.", description: "Networking: note under the no-address-manager warning; without MetalLB or Cilium the pool and address preference are saved but never applied" })}</div>
            </div>
          </div>
        </div>
      )}
    </div>
  );
}

function AddressStatusField({
  condition,
  messageText,
  serverNamespace,
}: {
  condition?: { reason?: string; message?: string; status?: string };
  messageText?: string;
  serverNamespace?: string;
}) {
  const intl = useOptionalIntl();
  const target = useResourceTarget({ name: "", namespace: serverNamespace });
  if (!condition) {
    return null;
  }

  const getBadgeColor = (
    reason?: string,
    status?: string,
  ): { bgColor: string; textColor: string; label: MessageDescriptor } => {
    if (status === "True") {
      return {
        bgColor: "bg-success/20",
        textColor: "text-success",
        label: BADGE_LABELS.assigned,
      };
    }

    // For False status, determine label from reason or message
    if (reason === "AssignmentPending" || reason === "ServiceNotReady") {
      return {
        bgColor: "bg-warning/20",
        textColor: "text-warning",
        label: BADGE_LABELS.pending,
      };
    }

    return {
      bgColor: "bg-danger/20",
      textColor: "text-danger",
      label: reason === "IgnoredForExposureMode"
        ? BADGE_LABELS.ignored
        : BADGE_LABELS.error,
    };
  };

  // Parse AddressInUse message to extract the conflicting server name.
  // Message format: "Requested address "<address>" is already in use by GameServer "<name>"."
  // The operator names conflicting servers as:
  // - "namespace/name" if from a different namespace
  // - bare "name" if from the same namespace
  const parseConflictingServer = (
    message?: string,
  ): { name: string; namespace?: string } | null => {
    if (!message || !message.includes('is already in use by GameServer')) {
      return null;
    }
    // Extract the server identifier between the last pair of quotes.
    // Pattern: GameServer "name" or GameServer "namespace/name"
    const match = message.match(/GameServer "([^"]+)"/);
    if (!match || !match[1]) {
      return null;
    }
    const identifier = match[1];
    // Check if the identifier contains a "/" separator (cross-namespace)
    if (identifier.includes('/')) {
      const parts = identifier.split('/');
      // Guard against malformed input: must be exactly 2 parts, both non-empty
      if (parts.length === 2 && parts[0] && parts[1]) {
        return { name: parts[1], namespace: parts[0] };
      }
      // Malformed cross-namespace identifier; return null to fall back to plain message
      return null;
    }
    // Same namespace: use the viewed server's namespace
    return { name: identifier, namespace: serverNamespace };
  };

  const badge = getBadgeColor(condition.reason, condition.status);
  const conflictingServer = condition.reason === "AddressInUse"
    ? parseConflictingServer(condition.message)
    : null;

  return (
    <Field
      label={intl.formatMessage({ id: "web.routes.tabs.settings.networking.address_status", defaultMessage: "Address status", description: "Networking: field label for the status of the address assignment, taken from the server's address condition" })}
      hint={intl.formatMessage({ id: "web.routes.tabs.settings.networking.reflects_the_gameserver_s", defaultMessage: "Reflects the GameServer's AddressAssignment condition.", description: "Networking: hint under the address status label saying it reflects the game server's address assignment condition" })}
    >
      <div className="space-y-4">
        <div className="flex items-start gap-3">
          <div
            className={`${badge.bgColor} rounded px-2 py-1 text-xs font-medium ${badge.textColor} shrink-0`}
          >
            {intl.formatMessage(badge.label)}
          </div>
          <div className="flex-1 pt-1 text-sm text-fg">
            {conflictingServer ? (
              <>{intl.formatMessage(
                { id: "web.routes.tabs.settings.networking.address_in_use", defaultMessage: "Requested address {address} is already in use by <link>GameServer \"{name}\"</link>.", description: "Networking: status text when the requested address is taken by another GameServer; {address} is the IP address and {name} is the other server's name; link wraps that server's name and links to it" },
                {
                  address: condition.message?.match(/"([^"]+)"/)?.[1] ?? "",
                  name: conflictingServer.name,
                  link: (chunks) => (
                    <Link
                      {...serverLink({ ...target, name: conflictingServer.name, namespace: conflictingServer.namespace ?? target.namespace })}
                      className="text-primary hover:underline"
                    >
                      {chunks}
                    </Link>
                  ),
                },
              )}</>
            ) : (
              messageText ?? condition.message
            )}
          </div>
        </div>
      </div>
    </Field>
  );
}

interface EditingPortMapping {
  name: string;
  remotePort?: number;
}

function FrpPortMappingEditor({
  values,
  onChange,
}: {
  values: RemotePortMapping[];
  onChange: (v: RemotePortMapping[]) => void;
}) {
  const intl = useOptionalIntl();
  const [editingValues, setEditingValues] = useState<EditingPortMapping[]>(
    values.map((v) => ({ ...v })),
  );

  const update = (idx: number, patch: Partial<EditingPortMapping>) => {
    const updated = editingValues.map((p, i) =>
      i === idx ? { ...p, ...patch } : p,
    );
    setEditingValues(updated);
    // Filter to only valid entries (both name and remotePort present).
    const valid = updated.filter(
      (p) => p.name && p.remotePort !== undefined && p.remotePort !== null,
    ) as RemotePortMapping[];
    onChange(valid);
  };

  const remove = (idx: number) => {
    const updated = editingValues.filter((_, i) => i !== idx);
    setEditingValues(updated);
    const valid = updated.filter(
      (p) => p.name && p.remotePort !== undefined && p.remotePort !== null,
    ) as RemotePortMapping[];
    onChange(valid);
  };

  const add = () => {
    setEditingValues([...editingValues, { name: "", remotePort: undefined }]);
  };

  return (
    <div className="space-y-2">
      {editingValues.length > 0 && (
        <div className="hidden gap-2 text-xs text-muted sm:grid sm:grid-cols-[1fr_120px_32px] sm:items-center">
          <span>{intl.formatMessage({ id: "web.routes.tabs.settings.networking.port_name", defaultMessage: "Port name", description: "Column label for the game port name of a port mapping" })}</span>
          <span>{intl.formatMessage({ id: "web.routes.tabs.settings.networking.remote_port", defaultMessage: "Remote port", description: "Column label for the remote port of a port mapping" })}</span>
          <span />
        </div>
      )}
      {editingValues.map((p, idx) => (
        <div
          key={idx}
          className="grid grid-cols-1 gap-2 rounded border border-border bg-surface/30 p-2 sm:grid-cols-[1fr_120px_32px] sm:items-center sm:border-0 sm:bg-transparent sm:p-0"
        >
          <Input
            value={p.name}
            onChange={(e) => update(idx, { name: e.target.value })}
            placeholder={intl.formatMessage({ id: "web.routes.tabs.settings.networking.game", defaultMessage: "game", description: "Networking: example placeholder in a frp port mapping's name box; an example value, not a default" })}
          />
          <Input
            type="number"
            value={p.remotePort !== undefined ? p.remotePort.toString() : ""}
            onChange={(e) =>
              update(idx, {
                remotePort: e.target.value ? Number(e.target.value) : undefined,
              })
            }
            inputMode="numeric"
            placeholder={intl.formatMessage({ id: "web.routes.tabs.settings.networking.remote_port_placeholder", defaultMessage: "8080", description: "Networking: example remote port shown as the port mapping input placeholder" })}
          />
          <Button
            variant="ghost"
            size="sm"
            isIconOnly
            className="justify-self-start sm:justify-self-auto"
            aria-label={intl.formatMessage({ id: "web.routes.tabs.settings.networking.remove", defaultMessage: "Remove", description: "Networking: icon-only button that removes one port mapping or key-value entry; read by screen readers; keep it short" })}
            onPress={() => remove(idx)}
          >
            <X className="h-3 w-3" />
          </Button>
        </div>
      ))}
      <Button size="sm" variant="secondary" onPress={add}>{intl.formatMessage({ id: "web.routes.tabs.settings.networking.add_port_mapping", defaultMessage: "Add port mapping", description: "Button that adds an empty frp port mapping row" })}</Button>
    </div>
  );
}

function KVEditor({
  values,
  onChange,
  keyPlaceholder,
  valuePlaceholder,
}: {
  values: Record<string, string>;
  onChange: (v: Record<string, string>) => void;
  keyPlaceholder: string;
  valuePlaceholder: string;
}) {
  const intl = useOptionalIntl();
  const [draftKV, setDraftKV] = useState({ key: "", value: "" });
  const add = () => {
    const key = draftKV.key.trim();
    if (!key) return;
    onChange({ ...values, [key]: draftKV.value });
    setDraftKV({ key: "", value: "" });
  };
  return (
    <div className="space-y-2">
      {Object.entries(values).map(([k, v]) => (
        <div
          key={k}
          className="flex items-center gap-2 rounded border border-border bg-surface/40 px-2 py-1"
        >
          <span className="flex-1 truncate font-mono text-xs">{k}</span>
          <span className="text-muted">{intl.formatMessage({ id: "web.routes.tabs.settings.networking.key_value_separator", defaultMessage: "=", description: "Networking: separator shown between an annotation key and its value" })}</span>
          <span className="flex-1 truncate font-mono text-xs">{v}</span>
          <Button
            variant="ghost"
            size="sm"
            isIconOnly
            aria-label={intl.formatMessage({ id: "web.routes.tabs.settings.networking.remove", defaultMessage: "Remove", description: "Networking: icon-only button that removes one port mapping or key-value entry; read by screen readers; keep it short" })}
            onPress={() => {
              const next = { ...values };
              delete next[k];
              onChange(next);
            }}
          >
            <X className="h-3 w-3" />
          </Button>
        </div>
      ))}
      <div className="flex items-center gap-2">
        <Input
          value={draftKV.key}
          onChange={(e) => setDraftKV({ ...draftKV, key: e.target.value })}
          placeholder={keyPlaceholder}
        />
        <Input
          value={draftKV.value}
          onChange={(e) => setDraftKV({ ...draftKV, value: e.target.value })}
          placeholder={valuePlaceholder}
        />
        <Button size="sm" variant="secondary" onPress={add} isDisabled={!draftKV.key.trim()}>{intl.formatMessage({ id: "web.routes.tabs.settings.networking.add", defaultMessage: "Add", description: "Button that adds the typed annotation key and value" })}</Button>
      </div>
    </div>
  );
}

interface TunnelCredentialFieldProps {
  configured?: boolean;
  secretName?: string;
  isLoading: boolean;
  error?: string;
  showInput: boolean;
  onShowInput: () => void;
  provider: "frp" | "tailscale" | "playit";
  credentialValue: string;
  onCredentialChange: (value: string) => void;
  onSave: () => void;
  onCancel: () => void;
  onRemove: () => void;
  hasSecretRef: boolean;
}

function TunnelCredentialField({
  configured,
  secretName,
  isLoading,
  error,
  showInput,
  onShowInput,
  provider,
  credentialValue,
  onCredentialChange,
  onSave,
  onCancel,
  onRemove,
  hasSecretRef,
}: TunnelCredentialFieldProps) {
  const intl = useOptionalIntl();
  const labels: Record<"frp" | "tailscale" | "playit", MessageDescriptor> = CREDENTIAL_LABELS;

  if (!showInput && (configured || hasSecretRef)) {
    return (
      <div className="space-y-2">
        <div className="flex items-center justify-between">
          <span className="text-sm font-medium text-foreground">{intl.formatMessage({ id: "web.routes.tabs.settings.networking.tunnel_credentials", defaultMessage: "Tunnel credentials", description: "Label for the tunnel credential field and its status section" })}</span>
        </div>
        <div className="rounded-md border border-border bg-surface/30 px-3 py-2 flex items-center justify-between">
          <div className="flex items-center gap-2">
            <Check className="h-4 w-4 text-success" />
            <div className="text-sm">
              <div className="text-foreground font-medium">{intl.formatMessage({ id: "web.routes.tabs.settings.networking.configured", defaultMessage: "Configured", description: "Networking: status text beside the tunnel credentials, shown when credentials are already stored" })}</div>
              {secretName && <div className="text-xs text-muted">{intl.formatMessage({ id: "web.routes.tabs.settings.networking.secret_secretname", defaultMessage: "Secret: {secretName}", description: "Networking: sentence with secretName in div" }, { secretName: secretName })}</div>}
            </div>
          </div>
          <div className="flex items-center gap-2">
            <Button
              size="sm"
              variant="secondary"
              onPress={onShowInput}
              isDisabled={isLoading}
            >{intl.formatMessage({ id: "web.routes.tabs.settings.networking.replace", defaultMessage: "Replace", description: "Button that replaces the saved tunnel credential with a new one" })}</Button>
            <Button
              size="sm"
              variant="ghost"
              isIconOnly
              aria-label={intl.formatMessage({ id: "web.routes.tabs.settings.networking.remove_tunnel_credentials", defaultMessage: "Remove tunnel credentials", description: "Networking: icon-only button that removes the stored tunnel credentials; read by screen readers; keep it short" })}
              onPress={onRemove}
              isDisabled={isLoading}
              className="text-danger hover:text-danger"
            >
              <X className="h-4 w-4" />
            </Button>
          </div>
        </div>
        {error && (
          <div className="rounded-md border-l-4 border-danger bg-danger/10 p-3 text-sm">
            <div className="flex gap-2">
              <AlertCircle className="h-5 w-5 shrink-0 text-danger" />
              <div className="text-xs text-danger">{error}</div>
            </div>
          </div>
        )}
      </div>
    );
  }

  return (
    <div className="space-y-2">
      <div className="space-y-1.5">
        <label htmlFor="tunnel-credential" className="block space-y-1.5">
          <span className="text-sm font-medium text-foreground">{intl.formatMessage({ id: "web.routes.tabs.settings.networking.tunnel_credentials", defaultMessage: "Tunnel credentials", description: "Label for the tunnel credential field and its status section" })}</span>
          <Input
            id="tunnel-credential"
            type="password"
            value={credentialValue}
            onChange={(e) => onCredentialChange(e.target.value)}
            placeholder={intl.formatMessage(labels[provider])}
            spellCheck={false}
            disabled={isLoading}
          />
          <span className="text-[11px] text-muted">{intl.formatMessage({ id: "web.routes.tabs.settings.networking.provider_stored_in_a_kubernetes_secret", defaultMessage: "{provider} — stored in a Kubernetes Secret, never displayed.", description: "Networking: sentence with provider in span" }, { provider: intl.formatMessage(labels[provider]) })}</span>
        </label>
      </div>
      {error && (
        <div className="rounded-md border-l-4 border-danger bg-danger/10 p-3 text-sm">
          <div className="flex gap-2">
            <AlertCircle className="h-5 w-5 shrink-0 text-danger" />
            <div className="text-xs text-danger">{error}</div>
          </div>
        </div>
      )}
      <div className="flex gap-2">
        <Button
          size="sm"
          onPress={onSave}
          isDisabled={isLoading || !credentialValue.trim()}
        >
          {isLoading ? (
            <>
              <Loader2 className="h-4 w-4 animate-spin" /> {intl.formatMessage({ id: "web.routes.tabs.settings.networking.saving", defaultMessage: "Saving...", description: "Save button label while the tunnel credential is being saved" })}</>
          ) : (
            <>
              <Check className="h-4 w-4" /> {intl.formatMessage({ id: "web.routes.tabs.settings.networking.save_credential", defaultMessage: "Save credential", description: "Button that saves the tunnel credential" })}</>
          )}
        </Button>
        <Button size="sm" variant="ghost" onPress={onCancel} isDisabled={isLoading}>{intl.formatMessage({ id: "web.routes.tabs.settings.networking.cancel", defaultMessage: "Cancel", description: "Button that closes the tunnel credential form without saving" })}</Button>
      </div>
    </div>
  );
}

