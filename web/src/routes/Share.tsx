import { useEffect, useState, useRef } from "react";
import { useParams } from "@tanstack/react-router";
import {
  Card,
  Button,
  Spinner,
} from "@heroui/react";
import { ShieldCheck, Copy, Check } from "lucide-react";
import { APIError, Shares } from "@/lib/api";
import { enforceUnauthenticatedTheme } from "@/lib/enforceUnauthenticatedTheme";
import type { ShareLinkPublic } from "@/types";
import type { AppearanceMode } from "@/components/ui/AppearanceToggle";
import { CompactLanguagePicker } from "@/components/settings/CompactLanguagePicker";
import { useOptionalIntl } from "@/lib/i18n/format";

// localStorage key for appearance preference (must match AppLayout)
const THEME_STORAGE_KEY = "gameplane-theme";

function readStoredTheme(): AppearanceMode {
  try {
    const saved = localStorage.getItem(THEME_STORAGE_KEY);
    if (saved === "light" || saved === "dark" || saved === "system") return saved;
  } catch {
    // localStorage unavailable — fall back to system default.
  }
  return "system";
}

function applyTheme(mode: AppearanceMode) {
  const resolved =
    mode === "system"
      ? window.matchMedia("(prefers-color-scheme: dark)").matches
        ? "dark"
        : "light"
      : mode;

  const html = document.documentElement;
  html.setAttribute("data-theme", resolved);
  if (resolved === "dark") {
    html.classList.add("dark");
  } else {
    html.classList.remove("dark");
  }
}

type State = "loading" | "up" | "asleep-start" | "asleep-viewonly" | "starting" | "invalid";

// Public endpoints share a 20/minute per-IP budget. Five seconds between
// completed requests leaves room for the initial resolve and Start action.
const POLL_INTERVAL_MS = 5000;
const MAX_BACKOFF_MS = 60000;

function isTransient(err: unknown): boolean {
  return !(err instanceof APIError) || err.status === 429 || (err.status >= 500 && err.status < 600);
}

function retryDelay(err: unknown, failures: number): number {
  const backoff = Math.min(MAX_BACKOFF_MS, POLL_INTERVAL_MS * 2 ** Math.min(failures, 4));
  if (!(err instanceof APIError) || !err.retryAfter?.trim()) return backoff;
  const header = err.retryAfter.trim();
  const seconds = Number(header);
  const retryAfter = Number.isFinite(seconds) && seconds >= 0
    ? seconds * 1000 : Date.parse(header) - Date.now();
  // Do not let an oversized Retry-After overflow the browser's timer and cause
  // an immediate retry. HTTP-date and delta-seconds forms are both accepted.
  return Number.isFinite(retryAfter)
    ? Math.min(2147483647, Math.max(backoff, retryAfter)) : backoff;
}

// LanguageCorner pins the browser-local language picker to a shared card's
// top-right corner. Every state card renders it, so the switch is available
// before the link resolves and on the asleep/invalid views.
function LanguageCorner() {
  return (
    <div className="absolute right-3 top-3">
      <CompactLanguagePicker />
    </div>
  );
}

export function SharePage() {
  const { token } = useParams({ from: "/share/$token" });
  return <ShareView key={token} token={token} />;
}

function ShareView({ token }: { token: string }) {
  const intl = useOptionalIntl();
  const [state, setState] = useState<State>("loading");
  const [data, setData] = useState<ShareLinkPublic | null>(null);
  const [isStarting, setIsStarting] = useState(false);
  const [startRetryDelay, setStartRetryDelay] = useState<number | null>(null);
  const [copied, setCopied] = useState(false);
  const startRequestRef = useRef<AbortController | null>(null);

  useEffect(() => () => startRequestRef.current?.abort(), []);

  useEffect(() => {
    if (startRetryDelay === null) return;
    const timeout = setTimeout(() => setStartRetryDelay(null), startRetryDelay);
    return () => clearTimeout(timeout);
  }, [startRetryDelay]);

  // Public share links always render the Pink preset with no custom CSS
  // (FR-011), even for browsers with theme prefs cached from a logged-in
  // session — pin for the route's lifetime, then keep the stored
  // light/dark appearance behavior below.
  useEffect(() => enforceUnauthenticatedTheme(), []);

  // Apply stored appearance preference on mount
  useEffect(() => {
    const mode = readStoredTheme();
    applyTheme(mode);
  }, []);

  // Initial resolve + polling for Starting state
  useEffect(() => {
    let active = true;
    let failures = 0;
    let timeout: ReturnType<typeof setTimeout> | undefined;
    const controller = new AbortController();

    const resolve = async () => {
      try {
        const result = await Shares.resolve(token, controller.signal);
        if (!active) return;

        // Check for neutral/error response (serverName is empty, indicating resolve returned an error)
        if (!result.serverName) {
          setState("invalid");
          return;
        }

        setData(result);

        // Determine state from result
        if (result.status === "Running") {
          setState("up");
        } else if (result.status === "Suspended" || result.status === "Stopped") {
          // For Asleep states, always show Start button by default
          // If user clicks and gets error, we'll transition to invalid
          setState("asleep-start");
        } else if (result.status === "Starting" || result.status === "Pending") {
          setState("starting");
        } else {
          // Any other state treats as invalid
          setState("invalid");
        }
      } catch (err) {
        if (!active) return;
        if (!isTransient(err)) {
          setState("invalid");
          return;
        }
        // The per-IP bucket may already be busy before the first load. Keep
        // the loading UI rather than declaring a valid token unavailable.
        failures++;
        timeout = setTimeout(() => void resolve(), retryDelay(err, failures));
      }
    };

    void resolve();

    return () => {
      active = false;
      if (timeout !== undefined) clearTimeout(timeout);
      controller.abort();
    };
  }, [token]);

  // Polling when Starting
  useEffect(() => {
    if (state !== "starting") return;
    let active = true;
    let failures = 0;
    let timeout: ReturnType<typeof setTimeout>;
    const controller = new AbortController();

    const schedule = (delay: number) => {
      timeout = setTimeout(() => void poll(), delay);
    };

    const poll = async () => {
      try {
        const result = await Shares.resolve(token, controller.signal);
        if (!active) return;

        // Check for neutral/error response
        if (!result.serverName) {
          setState("invalid");
          return;
        }

        if (result.status === "Running") {
          setData(result);
          setState("up");
          return;
        } else if (
          result.status === "Suspended" ||
          result.status === "Stopped"
        ) {
          // The wake may not have reached reconciliation yet. Server phase
          // does not tell us whether this link has permission to start.
          setState("asleep-start");
          setData(result);
          return;
        }
        if (result.status !== "Starting" && result.status !== "Pending") {
          setState("invalid");
          return;
        }
        setData(result);
        failures = 0;
        schedule(POLL_INTERVAL_MS);
      } catch (err) {
        if (!active) return;
        if (!isTransient(err)) {
          setState("invalid");
          return;
        }
        // Keep the last validated public view. A busy API or interrupted
        // connection does not mean that this token was revoked or expired.
        failures++;
        schedule(retryDelay(err, failures));
      }
    };

    schedule(POLL_INTERVAL_MS);

    return () => {
      active = false;
      clearTimeout(timeout);
      controller.abort();
    };
  }, [state, token]);

  const handleStart = async () => {
    if (isStarting || startRetryDelay !== null) return;
    const controller = new AbortController();
    startRequestRef.current = controller;
    setIsStarting(true);
    try {
      await Shares.start(token, controller.signal);
      if (controller.signal.aborted) return;
      // Transition to Starting state and start polling
      setState("starting");
    } catch (err) {
      if (controller.signal.aborted) return;
      if (isTransient(err)) {
        // A rejected wake has not started the server. Keep the last validated
        // public view and let the visitor retry after the shared budget recovers.
        setState("asleep-start");
        setStartRetryDelay(retryDelay(err, 0));
      } else {
        setState("invalid");
      }
    } finally {
      if (!controller.signal.aborted) setIsStarting(false);
    }
  };

  const handleCopyAddress = () => {
    if (data?.address) {
      const addr = `${data.address.host}:${data.address.port}`;
      navigator.clipboard.writeText(addr).catch(() => {
        // Silently fail if copy doesn't work
      });
      setCopied(true);
      setTimeout(() => setCopied(false), 2000);
    }
  };

  const containerClass = "flex h-full w-full items-center justify-center bg-background p-6";

  return (
    <div className={containerClass}>
      {state === "loading" && (
        <Card className="relative max-w-md p-8 text-center">
          <LanguageCorner />
          <div className="flex justify-center">
            <Spinner />
          </div>
        </Card>
      )}

      {state === "up" && data && (
        <Card className="relative max-w-md p-8">
          <LanguageCorner />
          <div className="mb-6">
            <div className="mb-4 flex items-center justify-center">
              <div className="flex h-10 w-10 items-center justify-center rounded-md bg-primary/15">
                <ShieldCheck className="h-5 w-5 text-primary" />
              </div>
            </div>
            <div className="mb-2 text-center">
              <div className="font-mono text-sm text-muted">{intl.formatMessage({ id: "web.routes.share.gameplane", defaultMessage: "gameplane", description: "Share: product name above the shared server title on the public share page; a brand name, usually kept as is" })}</div>
              <div className="font-mono text-sm text-muted">{intl.formatMessage({ id: "web.routes.share.shared_server_link", defaultMessage: "shared server link", description: "Share: caption under the product name on the public share page, identifying the page as a shared server link" })}</div>
            </div>
            <h1 className="font-mono text-2xl font-semibold">{data.serverName}</h1>
            <div className="mt-3 flex justify-center">
              <span className="inline-block rounded-full bg-success/20 px-3 py-1 text-sm font-medium text-success">{intl.formatMessage({ id: "web.routes.share.online", defaultMessage: "Online", description: "Share: status badge under the server name on the share page, shown when the server is running; keep it short" })}</span>
            </div>
          </div>

          <div className="space-y-4">
            <div>
              <div className="text-xs text-muted mb-1">{intl.formatMessage({ id: "web.routes.share.connect_at", defaultMessage: "CONNECT AT", description: "Share: small label above the server address on the share page; keep it short" })}</div>
              {data.address ? (
                <div className="flex items-center gap-2 rounded-lg bg-surface p-3">
                  <code className="flex-1 text-sm font-mono">
                    {intl.formatMessage({ id: "web.routes.share.host_port", defaultMessage: "{host}:{port}", description: "Share: connect address shown as host:port; {host} is the server host name and {port} is its port number" }, { host: data.address.host, port: String(data.address.port) })}
                  </code>
                  <button
                    type="button"
                    className="p-1 text-muted hover:text-fg transition-colors"
                    onClick={handleCopyAddress}
                    title={intl.formatMessage({ id: "web.routes.share.copy_address", defaultMessage: "Copy address", description: "Share: tooltip of the icon button that copies the server address to the clipboard; keep it short" })}
                  >
                    {copied ? (
                      <Check className="h-4 w-4 text-success" />
                    ) : (
                      <Copy className="h-4 w-4" />
                    )}
                  </button>
                </div>
              ) : (
                <div className="text-sm text-muted">{intl.formatMessage({ id: "web.routes.share.not_exposed", defaultMessage: "Not exposed", description: "Share: text shown in place of the server address when the server has no address to share" })}</div>
              )}
            </div>

            {data.playersOnline !== undefined && (
              <div>
                <div className="text-xs text-muted mb-1">{intl.formatMessage({ id: "web.routes.share.players", defaultMessage: "PLAYERS", description: "Share: small label above the players-online line on the share page" })}</div>
                <div className="text-sm">{intl.formatMessage({ id: "web.routes.share.playersonline_players_online", defaultMessage: "{playersOnline} players online", description: "Share: sentence with playersOnline in div" }, { playersOnline: data.playersOnline })}</div>
              </div>
            )}
          </div>
        </Card>
      )}

      {state === "asleep-start" && data && (
        <Card className="relative max-w-md p-8">
          <LanguageCorner />
          <div className="mb-6">
            <div className="mb-4 flex items-center justify-center">
              <div className="flex h-10 w-10 items-center justify-center rounded-md bg-primary/15">
                <ShieldCheck className="h-5 w-5 text-primary" />
              </div>
            </div>
            <div className="mb-2 text-center">
              <div className="font-mono text-sm text-muted">{intl.formatMessage({ id: "web.routes.share.gameplane", defaultMessage: "gameplane", description: "Share: product name above the shared server title on the public share page; a brand name, usually kept as is" })}</div>
              <div className="font-mono text-sm text-muted">{intl.formatMessage({ id: "web.routes.share.shared_server_link", defaultMessage: "shared server link", description: "Share: caption under the product name on the public share page, identifying the page as a shared server link" })}</div>
            </div>
            <h1 className="font-mono text-2xl font-semibold">{data.serverName}</h1>
            <div className="mt-3 flex justify-center">
              <span className="status-focus inline-flex items-center gap-1.5 rounded-full border px-3 py-1 text-sm font-medium">
                <span className="h-1.5 w-1.5 rounded-full bg-current" aria-hidden="true" />{intl.formatMessage({ id: "web.routes.share.asleep", defaultMessage: "Asleep", description: "Share: status badge under the server name when the server is asleep; keep it short" })}</span>
            </div>
          </div>

          <div className="mb-6">
            <p className="text-sm text-muted">{intl.formatMessage({ id: "web.routes.share.this_server_is_asleep_to_save_resources", defaultMessage: "This server is asleep to save resources. Start it and it'll be ready in a minute or two.", description: "Share: explanation under the asleep badge saying the server sleeps to save resources and takes a minute or two to be ready after it is started" })}</p>
          </div>

          <Button
            variant="primary"
            className="w-full"
            isDisabled={isStarting || startRetryDelay !== null}
            onPress={handleStart}
          >
            {isStarting ? intl.formatMessage({ id: "web.routes.share.starting_ellipsis", defaultMessage: "Starting...", description: "Share: start button label while the start request is in flight" }) : startRetryDelay !== null ? intl.formatMessage({ id: "web.routes.share.try_again_shortly", defaultMessage: "Try again shortly", description: "Share: start button label while a retry is scheduled after a failed start" }) : intl.formatMessage({ id: "web.routes.share.start_server", defaultMessage: "Start server", description: "Share: button label that starts the shared server" })}
          </Button>
        </Card>
      )}

      {state === "asleep-viewonly" && data && (
        <Card className="relative max-w-md p-8">
          <LanguageCorner />
          <div className="mb-6">
            <div className="mb-4 flex items-center justify-center">
              <div className="flex h-10 w-10 items-center justify-center rounded-md bg-primary/15">
                <ShieldCheck className="h-5 w-5 text-primary" />
              </div>
            </div>
            <div className="mb-2 text-center">
              <div className="font-mono text-sm text-muted">{intl.formatMessage({ id: "web.routes.share.gameplane", defaultMessage: "gameplane", description: "Share: product name above the shared server title on the public share page; a brand name, usually kept as is" })}</div>
              <div className="font-mono text-sm text-muted">{intl.formatMessage({ id: "web.routes.share.shared_server_link", defaultMessage: "shared server link", description: "Share: caption under the product name on the public share page, identifying the page as a shared server link" })}</div>
            </div>
            <h1 className="font-mono text-2xl font-semibold">{data.serverName}</h1>
            <div className="mt-3 flex justify-center">
              <span className="status-focus inline-flex items-center gap-1.5 rounded-full border px-3 py-1 text-sm font-medium">
                <span className="h-1.5 w-1.5 rounded-full bg-current" aria-hidden="true" />{intl.formatMessage({ id: "web.routes.share.asleep", defaultMessage: "Asleep", description: "Share: status badge under the server name when the server is asleep; keep it short" })}</span>
            </div>
          </div>

          <p className="text-sm text-muted">{intl.formatMessage({ id: "web.routes.share.this_server_is_asleep_right_now_check", defaultMessage: "This server is asleep right now. Check back later, or ask the server owner to start it.", description: "Share: explanation under the asleep badge telling the visitor to check back later or ask the server owner to start the server" })}</p>
        </Card>
      )}

      {state === "starting" && data && (
        <Card className="relative max-w-md p-8">
          <LanguageCorner />
          <div className="mb-6">
            <div className="mb-4 flex items-center justify-center">
              <div className="flex h-10 w-10 items-center justify-center rounded-md bg-primary/15">
                <ShieldCheck className="h-5 w-5 text-primary" />
              </div>
            </div>
            <div className="mb-2 text-center">
              <div className="font-mono text-sm text-muted">{intl.formatMessage({ id: "web.routes.share.gameplane", defaultMessage: "gameplane", description: "Share: product name above the shared server title on the public share page; a brand name, usually kept as is" })}</div>
              <div className="font-mono text-sm text-muted">{intl.formatMessage({ id: "web.routes.share.shared_server_link", defaultMessage: "shared server link", description: "Share: caption under the product name on the public share page, identifying the page as a shared server link" })}</div>
            </div>
            <h1 className="font-mono text-2xl font-semibold">{data.serverName}</h1>
            <div className="mt-3 flex justify-center">
              <div className="inline-flex items-center gap-2 rounded-full border border-warning/50 bg-warning/10 px-3 py-1">
                <Spinner size="sm" color="warning" />
                <span className="text-sm font-medium text-warning">{intl.formatMessage({ id: "web.routes.share.starting", defaultMessage: "Starting...", description: "Share: status badge with a spinner under the server name while the server is starting; keep it short" })}</span>
              </div>
            </div>
          </div>

          <p className="text-sm text-muted">{intl.formatMessage({ id: "web.routes.share.the_server_is_starting_up_this_usually", defaultMessage: "The server is starting up. This usually takes a minute or two — this page updates on its own.", description: "Share: explanation under the starting badge saying startup takes a minute or two and the page updates by itself" })}</p>
        </Card>
      )}

      {state === "invalid" && (
        <Card className="relative max-w-md p-8">
          <LanguageCorner />
          <div className="mb-6">
            <div className="mb-4 flex items-center justify-center">
              <div className="flex h-10 w-10 items-center justify-center rounded-md bg-primary/15">
                <ShieldCheck className="h-5 w-5 text-primary" />
              </div>
            </div>
            <div className="mb-2 text-center">
              <div className="font-mono text-sm text-muted">{intl.formatMessage({ id: "web.routes.share.gameplane", defaultMessage: "gameplane", description: "Share: product name above the shared server title on the public share page; a brand name, usually kept as is" })}</div>
              <div className="font-mono text-sm text-muted">{intl.formatMessage({ id: "web.routes.share.shared_server_link", defaultMessage: "shared server link", description: "Share: caption under the product name on the public share page, identifying the page as a shared server link" })}</div>
            </div>
          </div>

          <div className="text-center">
            <h1 className="font-mono text-2xl font-semibold">{intl.formatMessage({ id: "web.routes.share.link_not_available", defaultMessage: "Link not available", description: "Share: heading of the error page shown when the shared link is invalid or cannot be used" })}</h1>
            <p className="mt-2 text-sm text-muted">{intl.formatMessage({ id: "web.routes.share.this_link_may_be_invalid_expired_or", defaultMessage: "This link may be invalid, expired, or revoked. Ask the person who shared it for a new one.", description: "Share: explanation under the link-not-available heading telling the visitor to ask the person who shared the link for a new one" })}</p>
          </div>
        </Card>
      )}
    </div>
  );
}
