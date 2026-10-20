import { useResourceClient, useResourceTarget, resourceKey, useResourceAccess } from "@/lib/resourceTarget";
import { useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { Package, X } from "lucide-react";
import { Button, Card, Alert } from "@heroui/react";

import type { GameTemplate, RegistryProject } from "@/types";

import { APIError } from "@/lib/api";
import { errorTextIntl } from "@/lib/errors";
import { RegistryBrowser, RegistryIcon, compactNum } from "@/components/registry-browser";
import { useOptionalIntl } from "@/lib/i18n/format";
import { defineMessages, type IntlShape, type MessageDescriptor } from "react-intl";

type Banner = { kind: "ok" | "err"; text: string };

// Curated Modrinth modpack categories for the filter chips (shown only when
// the active provider is Modrinth).
const MODPACK_CATEGORY_LABELS = defineMessages({
  adventure: { id: "web.routes.tabs.modpacks.category_adventure", defaultMessage: "Adventure", description: "Modpacks: category filter chip for adventure modpacks" },
  technology: { id: "web.routes.tabs.modpacks.category_technology", defaultMessage: "Tech", description: "Modpacks: category filter chip for technology modpacks" },
  magic: { id: "web.routes.tabs.modpacks.category_magic", defaultMessage: "Magic", description: "Modpacks: category filter chip for magic modpacks" },
  optimization: { id: "web.routes.tabs.modpacks.category_optimization", defaultMessage: "Optimized", description: "Modpacks: category filter chip for optimization modpacks" },
  multiplayer: { id: "web.routes.tabs.modpacks.category_multiplayer", defaultMessage: "Multiplayer", description: "Modpacks: category filter chip for multiplayer modpacks" },
});

const MODPACK_CATEGORIES: { value: string; label: MessageDescriptor }[] = [
  { value: "adventure", label: MODPACK_CATEGORY_LABELS.adventure },
  { value: "technology", label: MODPACK_CATEGORY_LABELS.technology },
  { value: "magic", label: MODPACK_CATEGORY_LABELS.magic },
  { value: "optimization", label: MODPACK_CATEGORY_LABELS.optimization },
  { value: "multiplayer", label: MODPACK_CATEGORY_LABELS.multiplayer },
];

// ModpacksTab browses whole modpacks and installs one. Install differs by
// the active provider: env-mode (e.g. Modrinth/itzg) pins the pack via the
// server's env and restarts (one active pack); deps-mode (e.g.
// Thunderstore) resolves the pack's dependencies and installs each.
export function ModpacksTab({
  name,
  tmpl,
  ns,
}: {
  name: string;
  tmpl?: GameTemplate;
  ns?: string;
}) {
  const intl = useOptionalIntl();
  const resourceTarget = useResourceTarget({ name, namespace: ns });
  const resourceClient = useResourceClient(resourceTarget);
  const { Servers } = resourceClient;
  const qc = useQueryClient();
  const access = useResourceAccess();
  const canManage = access?.canControl === true;

  const providers = tmpl?.spec.capabilities?.mods?.registry?.providers ?? [];
  const declFor = (p: string) => providers.find((x) => x.provider === p);

  const [banner, setBanner] = useState<Banner | null>(null);
  const [busy, setBusy] = useState<string | null>(null); // project id being installed

  const installEnv = useMutation({
    mutationFn: (v: { p: RegistryProject; provider: string }) =>
      Servers.installModpack(name, { ref: v.p.slug || v.p.id }, v.provider, ns),
    onMutate: (v) => setBusy(v.p.id),
    onSuccess: (_r, v) => {
      void qc.invalidateQueries({ queryKey: ["fleet"] });
      setBanner({ kind: "ok", text: intl.formatMessage({ id: "web.routes.tabs.modpacks.banner_set", defaultMessage: "Set modpack {title}. The server is restarting to install it.", description: "Modpacks: banner shown after a modpack is set on the server; {title} is the modpack title" }, { title: v.p.title }) });
      return qc.invalidateQueries({ queryKey: resourceKey(resourceTarget, "server", name, ns) });
    },
    onError: (err) => setBanner({ kind: "err", text: errMsg(err, intl) }),
    onSettled: () => setBusy(null),
  });

  const installDeps = useMutation({
    mutationFn: async (v: { p: RegistryProject; provider: string }) => {
      const files = await Servers.modpackDeps(name, v.p.id, v.provider, ns);
      for (const f of files) {
        await Servers.installMod(name, { url: f.downloadUrl, name: f.filename }, ns);
      }
      return files.length;
    },
    onMutate: (v) => setBusy(v.p.id),
    onSuccess: (count, v) => {
      void qc.invalidateQueries({ queryKey: ["fleet"] });
      setBanner({ kind: "ok", text: intl.formatMessage({ id: "web.routes.tabs.modpacks.banner_installed", defaultMessage: "Installed {title} — {count, plural, one {# mod} other {# mods}}.", description: "Modpacks: banner shown after a modpack's dependency mods are installed; {title} is the modpack title, {count} the number of mods installed" }, { title: v.p.title, count }) });
      return qc.invalidateQueries({ queryKey: resourceKey(resourceTarget, "mods", name) });
    },
    onError: (err) => setBanner({ kind: "err", text: errMsg(err, intl) }),
    onSettled: () => setBusy(null),
  });

  const install = (p: RegistryProject, provider: string) => {
    const v = { p, provider };
    if (declFor(provider)?.modpacks?.refEnv) installEnv.mutate(v);
    else installDeps.mutate(v);
  };

  return (
    <div className="flex h-full flex-col gap-4 p-6">
      <header className="space-y-0.5">
        <h2 className="text-sm text-muted">{intl.formatMessage({ id: "web.routes.tabs.modpacks.browse_modpacks", defaultMessage: "Browse modpacks", description: "Modpacks tab: heading of the section where modpacks are browsed from a registry" })}</h2>
        <p className="text-[11px] text-muted">{intl.formatMessage({ id: "web.routes.tabs.modpacks.installing_a_modpack_either_pins_it_on", defaultMessage: "Installing a modpack either pins it on the server (and restarts) or adds all of its mods, depending on the registry.", description: "Modpacks tab: hint under the modpacks heading saying an install either pins the modpack and restarts the server, or adds all its mods, depending on the registry" })}</p>
      </header>


      {banner && (
        <Alert status={banner.kind === "ok" ? "success" : "danger"} className="flex items-start justify-between gap-3">
          <Alert.Content className="flex flex-1 flex-col gap-0.5">
            <Alert.Description className="text-sm">{banner.text}</Alert.Description>
          </Alert.Content>
          <Button variant="ghost" size="sm" onPress={() => setBanner(null)} isIconOnly aria-label={intl.formatMessage({ id: "web.routes.tabs.modpacks.dismiss", defaultMessage: "dismiss", description: "Modpacks tab: icon-only button that closes the result banner; read by screen readers; keep it short" })}>
            <X className="h-3 w-3" />
          </Button>
        </Alert>
      )}

      <div className="min-h-0 flex-1">
        <RegistryBrowser
          name={name}
          ns={ns}
          type="modpack"
          categories={MODPACK_CATEGORIES.map((c) => ({ value: c.value, label: intl.formatMessage(c.label) }))}
          pillButtons={true}
          renderItem={(p, provider) => (
            <Card className="flex flex-row items-center gap-3 p-4">
              <div className="shrink-0">
                <RegistryIcon url={p.iconUrl} fallback={<Package className="h-9 w-9 rounded p-2 text-muted" />} />
              </div>
              <div className="min-w-0 flex-1">
                <div className="truncate text-sm font-medium">{p.title}</div>
                <div className="truncate text-xs text-muted">
                  {[p.author, p.downloads != null ? `${compactNum(p.downloads)} downloads` : null]
                    .filter(Boolean)
                    .join(" · ")}
                </div>
              </div>
              {/* The disabled reason lives on a wrapping element's title
                  (not the Button's aria-label) so the button's accessible
                  name stays its visible text ("Install"/"Installing…") for
                  role queries and assistive tech alike. */}
              <span title={canManage ? undefined : intl.formatMessage({ id: "web.routes.tabs.modpacks.requires_operator_role", defaultMessage: "Requires operator role", description: "Modpacks: tooltip on a disabled install control for users without the operator role" })} tabIndex={0}>
                <Button
                  size="sm"
                  variant="primary"
                  className="rounded-full"
                  isDisabled={!canManage || busy !== null}
                  onPress={() => install(p, provider)}
                >
                  {busy === p.id
                    ? intl.formatMessage({ id: "web.routes.tabs.modpacks.installing", defaultMessage: "Installing…", description: "Modpacks: label of the install button while this modpack is installing" })
                    : intl.formatMessage({ id: "web.routes.tabs.modpacks.install", defaultMessage: "Install", description: "Modpacks: label of the button that installs a modpack" })}
                </Button>
              </span>
            </Card>
          )}
        />
      </div>
    </div>
  );
}

function errMsg(err: unknown, intl: IntlShape): string {
  if (err instanceof APIError) {
    try {
      const parsed = JSON.parse(err.body) as { error?: string };
      if (parsed.error) return parsed.error;
    } catch {
      // fall through
    }
    if (err.status === 403) return intl.formatMessage({ id: "web.routes.tabs.modpacks.error_not_permitted", defaultMessage: "Your role does not allow managing this server.", description: "Modpacks: error shown when the user's role cannot manage this server" });
    return err.body || intl.formatMessage({ id: "web.routes.tabs.modpacks.error_request_failed_status", defaultMessage: "request failed ({status})", description: "Modpacks: fallback error text for an API error with no body; {status} is the HTTP status code" }, { status: String(err.status) });
  }
  return errorTextIntl(intl, err, intl.formatMessage({ id: "web.routes.tabs.modpacks.error_request_failed", defaultMessage: "request failed", description: "Modpacks: fallback error text when the error has no message" }));
}
