import type { Expose, GameServerEndpoint } from "@/types";

type Endpoint = Pick<GameServerEndpoint, "host" | "port" | "serviceAddress" | "tunnelProvider">;

// This string is pasted into game clients, so the port's digits and the
// host/port separator must stay identical in every dashboard language.
export function connectionAddress(endpoint?: Endpoint, expose?: Expose): string | undefined {
  if (!endpoint) return;
  const { host, port } = endpoint;
  if (!host || host === "None" || /[\s/?#@\\]/.test(host)) return;
  if (!Number.isInteger(port) || port < 1 || port > 65535) return;

  // Older operators paired the ClusterIP with a NodePort. Only use a
  // native NodePort after reconciliation reports a distinct player host.
  if (expose === "NodePort" && !endpoint.tunnelProvider &&
      (!endpoint.serviceAddress || host === endpoint.serviceAddress)) return;

  const literal = host.includes(":") && !host.startsWith("[") ? `[${host}]` : host;
  const address = `${literal}:${port}`;
  return URL.canParse(`http://${address}`) ? address : undefined;
}

// The Overview and Networking assignment rows show bare hosts for other
// exposure modes. A NodePort assignment also needs its allocated port.
export function assignmentAddress(endpoint?: Endpoint, expose?: Expose): string | undefined {
  if (expose === "NodePort") return connectionAddress(endpoint, expose);
  return endpoint?.host;
}
