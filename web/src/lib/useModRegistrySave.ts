import { useMutation, useQueryClient } from "@tanstack/react-query";
import { api } from "./api";
import { ModRegistries } from "./endpoints";
import type { AllConfig, KeyedRegistryProvider, ModRegistriesCfg, ModRegistryEntry } from "./config";
import { useOptionalIntl } from "./i18n/format";

export type RegistryChange = {
  provider: KeyedRegistryProvider;
  expected?: ModRegistryEntry;
} & ({ kind: "set"; apiKey: string } | { kind: "remove" });

// Key actions save immediately. Read the latest section before writing so a
// different provider's concurrent update is retained; refuse a stale edit of
// this provider before touching its Secret.
export function useModRegistrySave() {
  const qc = useQueryClient();
  const intl = useOptionalIntl();
  return useMutation({
    mutationFn: async (change: RegistryChange) => {
      const config = await qc.fetchQuery({
        queryKey: ["config"],
        queryFn: ({ signal }) => api<AllConfig>("/admin/config", { signal }),
        staleTime: 0,
      });
      const registries = config.modRegistries?.registries ?? [];
      const previous = registries.find((entry) => entry.provider === change.provider);
      if (Boolean(previous) !== Boolean(change.expected) || previous?.configRef !== change.expected?.configRef) {
        throw new Error(intl.formatMessage({ id: "web.routes.admin_settings.configuration_changed", defaultMessage: "Configuration changed. Copy your edits and reopen this section before saving.", description: "Admin settings: error shown when the saved config changed under an open edit form" }));
      }
      const secretName = `gameplane-modreg-${change.provider}`;
      const next: ModRegistriesCfg = { registries: registries.filter((entry) => entry.provider !== change.provider) };
      if (change.kind === "set") {
        await ModRegistries.putSecret(change.provider, change.apiKey);
        next.registries.push({ provider: change.provider, configRef: secretName });
      }
      await api("/admin/config/modRegistries", { method: "PUT", body: next });
      // The config PUT has committed. A failed follow-up read or cleanup must
      // not roll the visible configuration back to an older cached response.
      await qc.cancelQueries({ queryKey: ["config"] });
      qc.setQueryData<AllConfig>(["config"], (cached) => ({ ...cached, modRegistries: next }));
      if (change.kind === "remove" && (!previous?.configRef || previous.configRef === secretName)) {
        await ModRegistries.deleteSecret(change.provider);
      }
      return next;
    },
    onSettled: () => qc.invalidateQueries({ queryKey: ["config"] }),
  });
}
