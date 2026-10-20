import { useEffect, useState, type ReactNode } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { useNavigate } from "@tanstack/react-router";
import {
  Archive,
  Code,
  HardDrive,
  KeyRound,
  Network,
  ShieldCheck,
  Eye,
  EyeOff,
} from "lucide-react";
import {
  Input,
  Label,
  Button,
  Alert,
  Spinner,
} from "@heroui/react";
import { APIError } from "@/lib/api";
import { Auth } from "@/lib/endpoints";
import { enforceUnauthenticatedTheme } from "@/lib/enforceUnauthenticatedTheme";
import { SAFE_MODE_SESSION_KEY } from "@/lib/useThemePreferences";
import { CompactLanguagePicker } from "@/components/settings/CompactLanguagePicker";
import type { LoginProvider } from "@/types";
import { useOptionalIntl } from "@/lib/i18n/format";

// IMPORTANT: This pre-auth surface must not display any internal
// cluster metrics, server counts, hostnames, or version strings. The
// right panel is static product-marketing content only.
export function LoginPage() {
  const intl = useOptionalIntl();
  const queryClient = useQueryClient();
  const navigate = useNavigate();
  const [u, setU] = useState("");
  const [p, setP] = useState("");
  const [err, setErr] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [showPassword, setShowPassword] = useState(false);
  // Enabled login methods, fetched pre-auth. null = not loaded (failed or
  // pending) — the password form stays visible then, so a failed/slow
  // fetch never blocks sign-in.
  const [providers, setProviders] = useState<LoginProvider[] | null>(null);
  // Self-service reset isn't wired up (admins reset via the CLI), so "Forgot?"
  // just reveals an inline hint rather than linking to a non-existent flow.
  const [forgot, setForgot] = useState(false);

  // Unauthenticated surfaces always render the Pink preset with no custom
  // CSS (FR-011): pin the DOM for the route's lifetime so theme prefs cached
  // by a previous session on this browser can never leak onto /login.
  useEffect(() => enforceUnauthenticatedTheme(), []);

  useEffect(() => {
    let active = true;
    void Auth.providers()
      .then((r) => {
        if (active) setProviders(r.providers);
      })
      .catch(() => {
        /* local-only fallback — leave providers null */
      });
    return () => {
      active = false;
    };
  }, []);

  // Shared by the form submit and the safe-mode link below the form: the
  // same credentials flow, but the safe-mode variant plants the session
  // flag before navigating so the authenticated session starts with the
  // custom CSS overlay suspended (contracts/theme-ui.md §1.3).
  const login = async (safe: boolean) => {
    if (busy) return;
    setErr(null);
    setBusy(true);
    try {
      const { user } = await Auth.login({ username: u, password: p });
      // Seed the identity cache so the app shell renders the real user on
      // first paint — no post-reload "guest" flash, no second /users/me fetch.
      queryClient.setQueryData(["me"], user);
      if (safe) {
        try {
          window.sessionStorage.setItem(SAFE_MODE_SESSION_KEY, "1");
        } catch {
          // sessionStorage blocked — sign-in still proceeds, just without
          // the safe-mode flag carried into the session.
        }
      }
      await navigate({ to: "/" });
    } catch (e) {
      setErr(
        e instanceof APIError
          ? intl.formatMessage({ id: "web.routes.login.invalid_credentials", defaultMessage: "Invalid credentials", description: "Login: error shown when the username or password is rejected; deliberately generic" })
          : intl.formatMessage({ id: "web.routes.login.network_error", defaultMessage: "Network error", description: "Login: error shown when the sign-in request could not reach the server" }),
      );
    } finally {
      setBusy(false);
    }
  };

  // One button per enabled SSO provider; the password form renders unless
  // the server explicitly reports local login disabled.
  const sso = providers?.filter((x) => x.kind !== "local") ?? [];
  const localEnabled = providers === null || providers.some((x) => x.kind === "local");

  return (
    <div className="grid h-full grid-cols-1 md:grid-cols-2">
      <section className="flex items-center justify-center bg-background p-8">
        <div className="relative w-full max-w-[420px] rounded-[12px] border border-border bg-card p-8">
          <div className="absolute right-3 top-3">
            <CompactLanguagePicker />
          </div>
          <div className="mb-8 flex items-center gap-3">
            <div className="flex h-10 w-10 items-center justify-center rounded-md bg-primary/15">
              <ShieldCheck className="h-5 w-5 text-primary" />
            </div>
            <div>
              <div className="font-mono text-lg font-semibold">{intl.formatMessage({ id: "web.routes.login.gameplane", defaultMessage: "gameplane", description: "Login: product name shown next to the logo at the top of the sign-in page; a brand name, usually kept as is" })}</div>
              <div className="text-xs text-muted">{intl.formatMessage({ id: "web.routes.login.game_server_control_panel", defaultMessage: "game server control panel", description: "Login: tagline under the product name next to the logo on the sign-in page" })}</div>
            </div>
          </div>

          <div className="mb-6">
            <h1 className="font-mono text-[22px] font-semibold">{intl.formatMessage({ id: "web.routes.login.sign_in", defaultMessage: "Sign in", description: "Login: main heading of the sign-in form" })}</h1>
            <p className="pt-1 text-sm text-muted">{intl.formatMessage({ id: "web.routes.login.welcome_to_gameplane", defaultMessage: "Welcome to Gameplane.", description: "Login: short line under the sign-in heading that greets the user" })}</p>
          </div>

          {localEnabled ? (
            <form
              className="space-y-4"
              onSubmit={(e) => {
                e.preventDefault();
                void login(false);
              }}
            >
              <div className="space-y-1.5">
                <Label htmlFor="username" className="font-mono text-xs text-muted">{intl.formatMessage({ id: "web.routes.login.email_or_username", defaultMessage: "Email or username", description: "Login: label of the username field on the sign-in form; the field accepts an email address or a username" })}</Label>
                <Input
                  id="username"
                  name="username"
                  value={u}
                  onChange={(e) => setU(e.target.value)}
                  autoFocus
                  autoComplete="username"
                  fullWidth
                />
              </div>

              <div className="space-y-1.5">
                {/* The "Forgot?" control must sit OUTSIDE the <label>: a <label>
                    that wraps a second interactive element steals that element's
                    accessible name and mis-associates the field, so screen
                    readers (and tests) resolve the label to the button, not the
                    password input. */}
                <div className="flex items-center justify-between">
                  <Label htmlFor="password" className="font-mono text-xs text-muted">{intl.formatMessage({ id: "web.routes.login.password", defaultMessage: "Password", description: "Login: label of the password field on the sign-in form" })}</Label>
                  <Button
                    type="button"
                    variant="ghost"
                    size="sm"
                    className="font-mono text-xs text-accent hover:text-accent/80"
                    onPress={() => setForgot((v) => !v)}
                  >{intl.formatMessage({ id: "web.routes.login.forgot", defaultMessage: "Forgot?", description: "Login: small button beside the password label that shows the password-reset hint; keep it short" })}</Button>
                </div>
                <div className="relative">
                  <Input
                    id="password"
                    name="password"
                    type={showPassword ? "text" : "password"}
                    value={p}
                    onChange={(e) => setP(e.target.value)}
                    autoComplete="current-password"
                    fullWidth
                    className="pr-10"
                  />
                  <Button
                    isIconOnly
                    type="button"
                    variant="ghost"
                    size="sm"
                    aria-label={showPassword ? intl.formatMessage({ id: "web.routes.login.hide_password", defaultMessage: "Hide password", description: "Login: aria-label of the button that hides the password" }) : intl.formatMessage({ id: "web.routes.login.show_password", defaultMessage: "Show password", description: "Login: aria-label of the button that reveals the password" })}
                    onPress={() => setShowPassword(!showPassword)}
                    className="absolute right-1 top-1/2 -translate-y-1/2"
                  >
                    {showPassword ? (
                      <EyeOff className="h-4 w-4" />
                    ) : (
                      <Eye className="h-4 w-4" />
                    )}
                  </Button>
                </div>
                {forgot && (
                  <p className="text-xs text-muted">{intl.formatMessage({ id: "web.routes.login.contact_your_administrator_to_reset", defaultMessage: "Contact your administrator to reset your password.", description: "Login: hint shown under the password field after the forgot button is pressed, telling the user to ask an administrator to reset the password" })}</p>
                )}
              </div>

              {err && (
                <Alert
                  role="alert"
                  status="danger"
                  className="px-0 py-1 text-sm text-danger bg-transparent border-none"
                >
                  {err}
                </Alert>
              )}

              <Button
                type="submit"
                isDisabled={busy}
                variant="primary"
                size="lg"
                fullWidth
                className="rounded-full font-mono"
              >
                {busy && <Spinner size="sm" color="current" className="mr-2" />}{intl.formatMessage({ id: "web.routes.login.sign_in_2", defaultMessage: "Sign in →", description: "Login: main submit button of the sign-in form; a spinner shows while the sign-in runs; keep it short" })}</Button>

              {sso.length > 0 && (
                <div className="relative py-2 text-center text-[11px] text-muted">
                  <span className="relative z-10 bg-background px-2">{intl.formatMessage({ id: "web.routes.login.or", defaultMessage: "or", description: "Login: small word between the sign-in form and the single sign-on buttons; keep it short" })}</span>
                  <span className="absolute inset-x-0 top-1/2 h-px -translate-y-1/2 bg-border" />
                </div>
              )}

              <SSOButtons providers={sso} />

              {/* Safe-mode entry point (contracts/theme-ui.md §1.3): same
                  credentials flow, but the session lands with the custom CSS
                  overlay suspended. Rendered as a link (design ref s12HO),
                  styled like the card's "Forgot?" link — standard pink
                  chrome via enforceUnauthenticatedTheme. href is the
                  post-login destination; the click runs the sign-in flow. */}
              <div className="text-center">
                <a
                  href="/"
                  onClick={(e) => {
                    e.preventDefault();
                    void login(true);
                  }}
                  className="font-mono text-xs text-accent hover:text-accent/80"
                >{intl.formatMessage({ id: "web.routes.login.sign_in_with_safe_mode_custom_styling", defaultMessage: "Sign in with safe mode (custom styling disabled)", description: "Login: link below the sign-in form that signs in with safe mode, which turns off custom styling" })}</a>
              </div>
            </form>
          ) : (
            <div className="space-y-4">
              <SSOButtons providers={sso} />
            </div>
          )}

          <p className="mt-10 text-center text-[11px] text-muted">{intl.formatMessage({ id: "web.routes.login.agpl_3_0_licensed", defaultMessage: "AGPL-3.0 licensed", description: "Login: footer line below the sign-in form stating the license; AGPL-3.0 is the license name, a technical term usually left untranslated" })}</p>
        </div>
      </section>

      <section className="hidden border-l border-border bg-surface/40 p-12 md:flex md:flex-col md:justify-center">
        <div className="max-w-md">
          <div className="mb-2">
            <span className="inline-flex items-center gap-2 rounded-full border border-border px-3 py-1 font-mono text-[11px] text-muted">
              <span className="h-1.5 w-1.5 rounded-full bg-accent" />{intl.formatMessage({ id: "web.routes.login.agpl_3_0", defaultMessage: "AGPL-3.0", description: "Login: small badge above the side panel headline showing the license name; AGPL-3.0 is a technical term, usually left untranslated; keep it short" })}</span>
          </div>
          <h2 className="text-[44px] font-semibold leading-tight font-mono">{intl.formatMessage({ id: "web.routes.login.kubernetes_native", defaultMessage: "Kubernetes-native", description: "Login: first line of the large headline on the side panel of the sign-in page; Kubernetes-native is a technical term, usually left untranslated" })}<br />{intl.formatMessage({ id: "web.routes.login.game_server_hosting", defaultMessage: "game server hosting.", description: "Login: second line of the large headline on the side panel, completing the phrase that starts with the first line" })}</h2>
          <p className="mt-4 text-sm text-muted">{intl.formatMessage({ id: "web.routes.login.an_open_source_alternative_to", defaultMessage: "An open-source alternative to proprietary game panels — CRDs, Operators, and StatefulSets all the way down.", description: "Login: paragraph under the side panel headline describing the product; CRDs, Operators and StatefulSets are technical terms, usually left untranslated" })}</p>
          <ul className="mt-10 space-y-4">
            <MarketingRow icon={<HardDrive className="h-5 w-5" />}>{intl.formatMessage({ id: "web.routes.login.deploy_any_game_minecraft_valheim", defaultMessage: "Deploy any game. Minecraft, Valheim, Factorio, Palworld, ARK, CS2, Terraria, and more.", description: "Login: first feature point on the side panel saying any game can be deployed; the game names are product names, usually kept as is" })}</MarketingRow>
            <MarketingRow icon={<Network className="h-5 w-5" />}>{intl.formatMessage({ id: "web.routes.login.scale_from_single_node_k3s_to_multi", defaultMessage: "Scale from single-node k3s to multi-node bare metal or cloud.", description: "Login: feature point on the side panel about scaling from a single k3s node to multi-node bare metal or cloud; k3s is a technical term, usually left untranslated" })}</MarketingRow>
            <MarketingRow icon={<Archive className="h-5 w-5" />}>{intl.formatMessage({ id: "web.routes.login.backups_auto_restart_rolling_upgrades", defaultMessage: "Backups, auto-restart, rolling upgrades, RBAC. Production-grade defaults.", description: "Login: feature point on the side panel listing backups, auto-restart, rolling upgrades and RBAC as production-ready defaults; RBAC is a technical term, usually left untranslated" })}</MarketingRow>
            <MarketingRow icon={<Code className="h-5 w-5" />}>{intl.formatMessage({ id: "web.routes.login.gitops_friendly", defaultMessage: "GitOps-friendly.", description: "Login: opening words of the last feature point on the side panel; GitOps is a technical term, usually left untranslated; a command sample and closing words follow" })} <code className="text-xs">{intl.formatMessage({ id: "web.routes.login.kubectl_get_gameservers", defaultMessage: "kubectl get gameservers", description: "Login: command sample shown in code style inside the last feature point; kubectl is a technical term, usually left untranslated" })}</code> {intl.formatMessage({ id: "web.routes.login.just_works", defaultMessage: "just works.", description: "Login: closing words after the command sample in the last feature point on the side panel" })}</MarketingRow>
          </ul>
        </div>
      </section>
    </div>
  );
}

// One "Continue with …" button per enabled SSO provider. Labels come
// from the pre-auth providers endpoint (admin display names — never
// issuer URLs, per the login-privacy rule).
function SSOButtons({ providers }: { providers: LoginProvider[] }) {
  const intl = useOptionalIntl();
  return (
    <>
      {providers.map((p) => (
        <Button
          key={p.name ?? p.label}
          type="button"
          variant="outline"
          fullWidth
          className="font-mono"
          onPress={() => location.assign(Auth.oidcStartURL(p.name))}
        >
          <KeyRound className="h-4 w-4" />{intl.formatMessage({ id: "web.routes.login.continue_with_label", defaultMessage: "Continue with {label}", description: "Login: sentence with label in Button" }, { label: p.label })}</Button>
      ))}
    </>
  );
}

function MarketingRow({ icon, children }: { icon: ReactNode; children: ReactNode }) {
  return (
    <li className="flex gap-3">
      <div className="mt-0.5 flex h-8 w-8 shrink-0 items-center justify-center rounded-md bg-accent/20 text-accent">
        {icon}
      </div>
      <div className="font-mono text-sm text-foreground">{children}</div>
    </li>
  );
}
