import { useResourceClient, useResourceTarget, resourceKey, useResourceAccess } from "@/lib/resourceTarget";
import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  Backpack,
  Ban,
  Bot,
  Clock,
  Cloud,
  CloudLightning,
  CloudRain,
  Flag,
  Gamepad2,
  Gauge,
  Gift,
  Leaf,
  Lock,
  LockOpen,
  Megaphone,
  MessageSquare,
  MicOff,
  Pause,
  Play,
  RefreshCw,
  Repeat,
  RotateCcw,
  RotateCw,
  Save,
  Square,
  Sun,
  Sunrise,
  Terminal,
  UserMinus,
  UserPlus,
  Zap,
  type LucideIcon,
} from "lucide-react";
import {
  Button,
  Card,
  CardHeader,
  CardContent,
  Modal,
  ModalBackdrop,
  ModalContainer,
  ModalDialog,
  ModalHeader,
  ModalBody,
  ModalFooter,
  Input,
  Label,
  Select,
  ListBox,
  ListBoxItem,
  Checkbox,
  Description,
} from "@heroui/react";

import type { ActionParamDecl, GameServer, GameTemplate, ServerActionDecl, StatusReading } from "@/types";
import { type LifecycleVerb } from "@/lib/endpoints";
import { rconAvailable, resolveConsoleMode } from "@/lib/capabilities";
import { APIError } from "@/lib/api";
import { errorText } from "@/lib/errors";
import { ErrorBanner } from "@/components/ui/ErrorBanner";

// Curated icon map for the lucide names modules declare. Unknown names
// fall back to a generic bolt so a typo never breaks the row.
const iconMap: Record<string, LucideIcon> = {
  megaphone: Megaphone,
  clock: Clock,
  cloud: Cloud,
  "user-plus": UserPlus,
  "user-minus": UserMinus,
  save: Save,
  ban: Ban,
  "message-square": MessageSquare,
  gauge: Gauge,
  pause: Pause,
  play: Play,
  flag: Flag,
  repeat: Repeat,
  bot: Bot,
  leaf: Leaf,
  "refresh-cw": RefreshCw,
  "mic-off": MicOff,
  gift: Gift,
  "gamepad-2": Gamepad2,
  sun: Sun,
  backpack: Backpack,
  "cloud-rain": CloudRain,
  "cloud-lightning": CloudLightning,
  "rotate-ccw": RotateCcw,
  lock: Lock,
  "lock-open": LockOpen,
};

// resolveTransport mirrors the API's transport resolution so the UI can tell
// a fire-and-forget stdin action (shows "sent") from an rcon one (shows
// output): the action's explicit transport wins, else rcon when the game has
// a live console, else stdin.
function resolveTransport(action: ServerActionDecl, hasRcon: boolean): "rcon" | "stdin" {
  if (action.transport) return action.transport;
  return hasRcon ? "rcon" : "stdin";
}

function actionIcon(name?: string): LucideIcon {
  return (name && iconMap[name]) || Zap;
}

type RunStatus = { kind: "ok" | "err"; text: string; type?: "output" | "sent" };

// ServerActionsCard renders the module-declared operator actions
// (spec.capabilities.actions) as buttons. Actions with parameters or a
// confirm flag open a dialog; the rest run immediately. Every run POSTs
// to /servers/{name}/actions/run, which the API gates to operator+.
//
// When the template declares no actions, and a GameServer is available,
// this falls back to a generic "Quick actions" body of lifecycle shortcuts
// (start/stop/restart/wake, open console) built from data already on the
// GameServer — so the design's four-card right column
// (assets/design-export/screenshots/EZFW0.png) is populated for every template,
// not just ones with declared actions. `gs`/`ns`/`onOpenConsole` are
// optional: callers that don't pass `gs` keep the old "render nothing"
// behavior.
export function ServerActionsCard({
  name,
  tmpl,
  gs,
  ns,
  onOpenConsole,
}: {
  name: string;
  tmpl?: GameTemplate;
  gs?: GameServer;
  ns?: string;
  onOpenConsole?: () => void;
}) {
  const resourceTarget = useResourceTarget({ name, namespace: ns });
  const resourceClient = useResourceClient(resourceTarget);
  const { Servers } = resourceClient;
  const qc = useQueryClient();
  const access = useResourceAccess();
  const canRun = access?.canControl === true;
  const [active, setActive] = useState<ServerActionDecl | null>(null);
  const [status, setStatus] = useState<RunStatus | null>(null);

  const actions = tmpl?.spec.capabilities?.actions ?? [];
  const hasRcon = rconAvailable(tmpl);
  // stdin-transport actions are written to the game container over pod
  // attach (the API's runStdinAction), so they only need an attachable
  // console (rcon or pty), not RCON. rcon-transport actions need RCON.
  const consoleAttachable = resolveConsoleMode(tmpl) !== "none";
  const transportReady = (a: ServerActionDecl) =>
    resolveTransport(a, hasRcon) === "rcon" ? hasRcon : consoleAttachable;

  // Live readings for params that pre-fill from a status metric. Same key as
  // ServerStatusCard, so both share one cache entry and one poll.
  const wantsStatus = actions.some((a) => a.params?.some((p) => p.currentFrom));
  const { data: readings } = useQuery({
    queryKey: resourceKey(resourceTarget, "server-status", name, ns),
    queryFn: ({ signal }) => resourceClient.withSignal(signal).Servers.status(name, ns),
    enabled: wantsStatus && hasRcon,
    staleTime: 10_000,
    retry: false,
  });

  const lifecycle = useMutation({
    mutationFn: (verb: LifecycleVerb) => Servers.lifecycle(name, verb, ns),
    onSuccess: () => qc.invalidateQueries({ queryKey: resourceKey(resourceTarget, "server", name, ns) }),
  });

  const run = useMutation<
    { ok: boolean; raw?: string },
    unknown,
    { action: ServerActionDecl; params?: Record<string, string> }
  >({
    mutationFn: (vars) => Servers.runAction(name, { id: vars.action.id, params: vars.params }, ns),
    onSuccess: (resp, vars) => {
      void qc.invalidateQueries({ queryKey: ["fleet"] });
      setActive(null);
      // Base sent-vs-output on the action's TRANSPORT, not on whether the
      // response body is empty: an rcon command legitimately returns no
      // output (cs2's `say`, project-zomboid's `servermsg`), and that must
      // still read as "ran", not the fire-and-forget "sent" chip. Only a
      // stdin action truly returns nothing because the output goes to the
      // Console stream.
      if (resolveTransport(vars.action, hasRcon) === "stdin") {
        setStatus({ kind: "ok", type: "sent", text: `${vars.action.displayName} sent` });
      } else {
        setStatus({
          kind: "ok",
          type: "output",
          text: resp.raw ? truncate(resp.raw, 200) : `${vars.action.displayName} ran`,
        });
      }
      return qc.invalidateQueries({ queryKey: resourceKey(resourceTarget, "server-status", name) });
    },
    onError: (err) => {
      setActive(null);
      setStatus({ kind: "err", text: errMsg(err) });
    },
  });

  if (actions.length === 0) {
    if (!gs) return null;
    const phase = gs.status?.phase;
    // Mirrors ServerDetail.tsx's header gating: Stopping still counts as
    // "not asleep yet" (players may be connected during the drain window).
    const asleep = gs.status?.idle?.asleep === true && phase !== "Stopping";
    const running = phase === "Running";
    const canStart = (phase === "Stopped" || phase === "Suspended" || phase === "Failed") && !asleep;
    return (
      <Card>
        <CardHeader className="flex flex-col gap-1 px-6 py-5">
          <h2 className="text-base font-semibold text-foreground">Quick actions</h2>
        </CardHeader>
        <CardContent className="space-y-1 px-0 py-2">
          {lifecycle.error && (
            <div className="px-6 pb-2">
              <ErrorBanner err={lifecycle.error} onDismiss={() => lifecycle.reset()} />
            </div>
          )}
          <LifecycleButton
            icon={RotateCw}
            label="Restart"
            onClick={() => lifecycle.mutate("restart")}
            disabled={!canRun || !running || lifecycle.isPending}
          />
          <LifecycleButton
            icon={Square}
            label="Stop"
            onClick={() => lifecycle.mutate("stop")}
            disabled={!canRun || (!running && !asleep) || lifecycle.isPending}
          />
          {asleep && (
            <LifecycleButton
              icon={Sunrise}
              label="Wake"
              onClick={() => lifecycle.mutate("wake")}
              disabled={!canRun || lifecycle.isPending}
            />
          )}
          {canStart && (
            <LifecycleButton
              icon={Play}
              label="Start"
              onClick={() => lifecycle.mutate("start")}
              disabled={!canRun || lifecycle.isPending}
            />
          )}
          {onOpenConsole && (
            <LifecycleButton icon={Terminal} label="Open console" onClick={onOpenConsole} />
          )}
        </CardContent>
      </Card>
    );
  }

  // Group actions: separate grouped and ungrouped, preserving group order
  const grouped = new Map<string, ServerActionDecl[]>();
  const ungrouped: ServerActionDecl[] = [];
  const groupOrder: string[] = [];

  for (const action of actions) {
    if (action.group) {
      if (!grouped.has(action.group)) {
        grouped.set(action.group, []);
        groupOrder.push(action.group);
      }
      grouped.get(action.group)!.push(action);
    } else {
      ungrouped.push(action);
    }
  }

  // Render action button
  const renderActionButton = (a: ServerActionDecl) => {
    const Icon = actionIcon(a.icon);
    const needsDialog = (a.params?.length ?? 0) > 0 || a.confirm;
    const disabled = !canRun || !transportReady(a) || run.isPending;
    return (
      <button
        key={a.id}
        disabled={disabled}
        title={!canRun ? "Requires operator role" : a.description}
        onClick={() =>
          needsDialog ? setActive(a) : run.mutate({ action: a })
        }
        className="flex w-full items-start gap-3 rounded-md px-2 py-2 text-left hover:bg-surface disabled:cursor-not-allowed disabled:opacity-50"
      >
        <span className={a.danger ? "text-danger" : "text-muted"}>
          <Icon className="h-4 w-4" />
        </span>
        <div className="flex-1">
          <div className={a.danger ? "text-sm text-danger" : "text-sm text-foreground"}>
            {a.displayName}
          </div>
          {a.description && (
            <div className="pt-0.5 text-[11px] text-muted">{a.description}</div>
          )}
        </div>
      </button>
    );
  };

  return (
    <Card>
      <CardHeader className="flex flex-col gap-1 px-6 py-5">
        <h2 className="text-base font-semibold text-foreground">Quick actions</h2>
      </CardHeader>
      <CardContent className="space-y-1 px-0 py-0">
        {actions.some((a) => !transportReady(a)) && (
          <p className="px-6 pb-2 text-xs text-muted">
            {consoleAttachable
              ? "Some actions need RCON; this game has none."
              : "Actions need a live console; this game has none."}
          </p>
        )}
        {status && (
          <div
            className={
              status.kind === "ok" && status.type === "sent"
                ? "mb-2 mx-6 flex items-center justify-between gap-3 rounded border border-success/40 bg-success/10 px-3 py-2 text-xs text-success"
                : status.kind === "ok"
                  ? "mb-2 mx-6 flex items-center justify-between gap-3 rounded border border-border bg-surface/40 px-3 py-2 text-sm text-foreground"
                  : "mb-2 mx-6 flex items-center justify-between gap-3 rounded border border-danger/40 bg-danger/10 px-3 py-2 text-sm text-danger"
            }
          >
            <span
              className={
                ((status.kind === "ok" && status.type === "output") || status.kind === "err")
                  ? "break-all font-mono"
                  : "break-all"
              }
            >
              {status.text}
            </span>
            <button
              onClick={() => setStatus(null)}
              className={
                status.kind === "ok" && status.type === "sent"
                  ? "shrink-0 text-xs text-success hover:text-success/80"
                  : "shrink-0 text-xs text-muted hover:text-foreground"
              }
            >
              dismiss
            </button>
          </div>
        )}
        {groupOrder.length > 0 ? (
          <>
            {groupOrder.map((groupName) => (
              <div key={groupName}>
                <div className="text-xs uppercase tracking-wide text-muted px-6 py-2">
                  {groupName}
                </div>
                {grouped.get(groupName)!.map(renderActionButton)}
              </div>
            ))}
            {ungrouped.length > 0 && (
              <div>
                <div className="text-xs uppercase tracking-wide text-muted px-6 py-2">
                  Actions
                </div>
                {ungrouped.map(renderActionButton)}
              </div>
            )}
          </>
        ) : (
          ungrouped.map(renderActionButton)
        )}
      </CardContent>

      {active && (
        <ActionDialog
          action={active}
          readings={readings}
          pending={run.isPending}
          onCancel={() => setActive(null)}
          onRun={(params) => run.mutate({ action: active, params })}
        />
      )}
    </Card>
  );
}

// ActionDialog collects the action's declared parameters, validates them
// client-side, and hands the values back. The server re-validates and
// sanitizes — this is just to catch obvious mistakes before the round
// trip and to render the right input per type.
function ActionDialog({
  action,
  readings,
  pending,
  onCancel,
  onRun,
}: {
  action: ServerActionDecl;
  readings?: StatusReading[];
  pending: boolean;
  onCancel: () => void;
  onRun: (params: Record<string, string>) => void;
}) {
  const params = action.params ?? [];
  // Only the user's edits are state. Everything else is derived per render,
  // so a reading that lands after the dialog opened still pre-fills its
  // input until the user picks a value themselves.
  const [edits, setEdits] = useState<Record<string, string>>({});
  const values: Record<string, string> = Object.fromEntries(
    params.map((p) => [
      p.name,
      edits[p.name] ?? currentValue(p, readings) ?? p.default ?? defaultFor(p),
    ]),
  );

  const errors = params
    .map((p) => validate(p, values[p.name] ?? ""))
    .filter((e): e is string => e !== null);
  const valid = errors.length === 0;

  return (
    <Modal isOpen onOpenChange={(open) => !open && onCancel()}>
      <ModalBackdrop isDismissable={!pending}>
        <ModalContainer>
          <ModalDialog>
            <ModalHeader className="flex flex-col gap-1">
              <h2 className="text-base font-semibold">{action.displayName}</h2>
            </ModalHeader>
            <ModalBody>
              {action.description && (
                <p className="text-sm text-muted">{action.description}</p>
              )}
              <div className="space-y-3">
                {params.map((p) => (
                  <ParamField
                    key={p.name}
                    param={p}
                    value={values[p.name] ?? ""}
                    onChange={(v) => setEdits((prev) => ({ ...prev, [p.name]: v }))}
                  />
                ))}
                {params.length === 0 && (
                  <p className="text-sm text-muted">Run this action now?</p>
                )}
              </div>
            </ModalBody>
            <ModalFooter>
              <Button variant="ghost" onPress={onCancel} isDisabled={pending}>
                Cancel
              </Button>
              <Button
                variant={action.danger ? "danger" : "primary"}
                isDisabled={!valid || pending}
                onPress={() => onRun(collect(params, values))}
              >
                {pending ? "Running…" : "Run"}
              </Button>
            </ModalFooter>
          </ModalDialog>
        </ModalContainer>
      </ModalBackdrop>
    </Modal>
  );
}

function ParamField({
  param,
  value,
  onChange,
}: {
  param: ActionParamDecl;
  value: string;
  onChange: (v: string) => void;
}) {
  const label = param.displayName || param.name;
  const id = `action-param-${param.name}`;
  const err = validate(param, value);
  return (
    <div className="space-y-2">
      {param.type === "enum" ? (
        <div className="space-y-1">
          <Select
            value={value}
            onChange={(v) => onChange(String(v))}
          >
            <Label className="text-sm">
              {label}
              {param.required && <span className="text-danger"> *</span>}
            </Label>
            <Select.Trigger id={id} className="w-full rounded border border-border bg-surface px-3 py-2 text-sm hover:bg-surface/80">
              <Select.Value />
              <Select.Indicator className="ml-auto h-4 w-4" />
            </Select.Trigger>
            <Select.Popover className="rounded border border-border">
              <ListBox className="p-0" aria-label={label}>
                {(param.enum ?? []).map((opt) => (
                  <ListBoxItem key={opt} id={opt}>{opt}</ListBoxItem>
                ))}
              </ListBox>
            </Select.Popover>
          </Select>
          {param.description && (
            <Description className="text-xs text-muted">{param.description}</Description>
          )}
          {err && value !== "" && (
            <p className="text-xs text-danger" role="alert">{err}</p>
          )}
        </div>
      ) : param.type === "bool" ? (
        <div className="flex items-center gap-2">
          <Checkbox
            id={id}
            isSelected={value === "true"}
            onChange={(isSelected) => onChange(isSelected ? "true" : "false")}
          >
            <Checkbox.Control>
              <Checkbox.Indicator />
            </Checkbox.Control>
            <Checkbox.Content className="text-sm">
              {label}
              {param.required && <span className="text-danger"> *</span>}
            </Checkbox.Content>
          </Checkbox>
        </div>
      ) : (
        <div className="space-y-1">
          <Label htmlFor={id} className="text-sm">
            {label}
            {param.required && <span className="text-danger"> *</span>}
          </Label>
          <Input
            id={id}
            value={value}
            inputMode={param.type === "int" ? "numeric" : undefined}
            onChange={(e) => onChange(e.target.value)}
            spellCheck={false}
          />
          {param.description && (
            <Description className="text-xs text-muted">{param.description}</Description>
          )}
          {err && value !== "" && (
            <p className="text-xs text-danger" role="alert">{err}</p>
          )}
        </div>
      )}
    </div>
  );
}

function defaultFor(p: ActionParamDecl): string {
  if (p.type === "bool") return "false";
  if (p.type === "enum") return p.enum?.[0] ?? "";
  return "";
}

// currentValue maps the live metric named by p.currentFrom onto a value the
// input accepts, or undefined when there is no usable reading. Enum options
// match case-insensitively: Minecraft reports "Easy" for the option "easy".
function currentValue(p: ActionParamDecl, readings?: StatusReading[]): string | undefined {
  if (!p.currentFrom) return undefined;
  const raw = readings?.find((r) => r.id === p.currentFrom)?.value.trim();
  if (!raw) return undefined;
  switch (p.type) {
    case "enum":
      return p.enum?.find((o) => o.toLowerCase() === raw.toLowerCase());
    case "bool": {
      const b = raw.toLowerCase();
      return b === "true" || b === "false" ? b : undefined;
    }
    case "int":
      return /^-?\d+$/.test(raw) ? raw : undefined;
    default:
      return raw;
  }
}

// validate returns an error string or null. Mirrors the agent's
// server-side checks so the UI fails fast, but the agent remains the
// source of truth (it also rejects control characters etc.).
function validate(p: ActionParamDecl, value: string): string | null {
  const v = value.trim();
  if (p.required && v === "") return `${p.displayName || p.name} is required`;
  if (v === "") return null;
  if (p.type === "int" && !/^-?\d+$/.test(v)) return "Must be a whole number";
  if (p.type === "enum" && p.enum && !p.enum.includes(value)) return "Pick a valid option";
  // Mirror the agent's control-character rejection (notably CR/LF, which
  // could chain a second console command). Spaces are fine.
  if (hasControl(value)) return "No control characters";
  return null;
}

// hasControl reports whether s contains an ASCII control character.
// Checked by code point rather than a regex to satisfy no-control-regex.
function hasControl(s: string): boolean {
  for (const ch of s) {
    const c = ch.charCodeAt(0);
    if (c < 0x20 || c === 0x7f) return true;
  }
  return false;
}

// collect builds the params payload, dropping empty optional values so
// the agent applies its declared defaults.
function collect(
  params: ActionParamDecl[],
  values: Record<string, string>,
): Record<string, string> {
  const out: Record<string, string> = {};
  for (const p of params) {
    const v = values[p.name] ?? "";
    if (v !== "") out[p.name] = v;
  }
  return out;
}

function truncate(s: string, n: number): string {
  return s.length <= n ? s : s.slice(0, n) + "…";
}

// LifecycleButton renders one row of the generic (no module actions)
// "Quick actions" fallback — same row shape as renderActionButton above.
function LifecycleButton({
  icon: Icon,
  label,
  onClick,
  disabled,
}: {
  icon: LucideIcon;
  label: string;
  onClick: () => void;
  disabled?: boolean;
}) {
  return (
    <button
      disabled={disabled}
      onClick={onClick}
      className="flex w-full items-center gap-3 px-6 py-2 text-left hover:bg-surface disabled:cursor-not-allowed disabled:opacity-50"
    >
      <span className="text-muted">
        <Icon className="h-4 w-4" />
      </span>
      <span className="text-sm text-foreground">{label}</span>
    </button>
  );
}

function errMsg(err: unknown): string {
  if (err instanceof APIError) {
    try {
      const parsed = JSON.parse(err.body) as { error?: string };
      if (parsed.error) return parsed.error;
    } catch {
      // fall through
    }
    if (err.status === 403) return "Your role does not allow running actions.";
    return err.body || `request failed (${err.status})`;
  }
  return errorText(err, "action failed");
}
