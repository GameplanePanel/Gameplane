import { APIError } from "@/lib/api";
import type { ResourceClient } from "@/lib/endpoints";
import type { GameTemplate } from "@/types";

// getServerTemplate reads the template a server was created from. The
// catalog read (GET /templates/{name}) needs templates:read on the cluster,
// which a server's owners and collaborators may not hold. They still manage
// the server's mods, modpacks and console, and those tabs need the template
// to render (#1063), so a refused catalog read falls back to the
// server-scoped GET /servers/{name}/template, which the API allows for the
// server's owners and collaborators.
export async function getServerTemplate(
  client: ResourceClient,
  name: string,
  ns: string | undefined,
  templateName: string,
): Promise<GameTemplate> {
  try {
    return await client.Templates.get(templateName);
  } catch (err) {
    if (err instanceof APIError && err.status === 403) {
      return client.Servers.effectiveTemplate(name, ns);
    }
    throw err;
  }
}
