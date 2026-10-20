import { useResourceClient, useResourceAccess, useResourceTarget, resourceKey } from "@/lib/resourceTarget";
import { useState } from "react";
import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import {
  ChevronDown,
  ChevronRight,
  RotateCw,
  UserMinus,
  UserPlus,
  Ban,
  Undo2,
  ListChecks,
} from "lucide-react";

import { Button, Input } from "@heroui/react";
import { StatCard } from "@/components/ui/StatCard";
import { ErrorBanner } from "@/components/ui/ErrorBanner";
import { errorTextIntl } from "@/lib/errors";
import { playerRows } from "@/lib/players";

import { cn } from "@/lib/utils";
import { useOptionalIntl } from "@/lib/i18n/format";
import { defineMessages, type IntlShape, type MessageDescriptor } from "react-intl";

interface ModResp {
  ok: boolean;
  raw?: string;
}

type Action = "kick" | "ban";

// Lowercase verbs shown in the status line after a moderation or whitelist action.
const ACTION_WORDS: Record<Action | "unban" | "add" | "remove", MessageDescriptor> = defineMessages({
  kick: { id: "web.routes.tabs.players.action_kick", defaultMessage: "kick", description: "Players: lowercase verb for the kick action, shown in the status line" },
  ban: { id: "web.routes.tabs.players.action_ban", defaultMessage: "ban", description: "Players: lowercase verb for the ban action, shown in the status line" },
  unban: { id: "web.routes.tabs.players.action_unban", defaultMessage: "unban", description: "Players: lowercase verb for the unban action, shown in the status line" },
  add: { id: "web.routes.tabs.players.action_add", defaultMessage: "add", description: "Players: lowercase verb for adding a player to the whitelist, shown in the status line" },
  remove: { id: "web.routes.tabs.players.action_remove", defaultMessage: "remove", description: "Players: lowercase verb for removing a player from the whitelist, shown in the status line" },
});

const PLAYERS_TEXT = defineMessages({
  empty_value: { id: "web.common.empty_value", defaultMessage: "—", description: "Shown in place of a value that is not set" },
});

export function PlayersTab({ name, ns }: { name: string; ns?: string }) {
  const intl = useOptionalIntl();
  const canControl = useResourceAccess()?.canControl === true;
  const resourceTarget = useResourceTarget({ name, namespace: ns });
  const resourceClient = useResourceClient(resourceTarget);
  const { Players: PlayersAPI } = resourceClient;
  const qc = useQueryClient();
  const [pending, setPending] = useState<{ player: string; label: string; action: Action } | null>(null);
  const [reason, setReason] = useState("");
  const [showBanned, setShowBanned] = useState(false);
  const [showWhitelist, setShowWhitelist] = useState(false);
  const [wlName, setWlName] = useState("");
  const [status, setStatus] = useState<{ kind: "ok" | "err"; text: string } | null>(null);

  const { data, refetch, isFetching } = useQuery({
    queryKey: resourceKey(resourceTarget, "players", name, ns),
    queryFn: ({ signal }) => resourceClient.withSignal(signal).Players.snapshot(name, ns),
    refetchInterval: 5_000,
  });
  const caps = data?.capabilities;
  const rows = playerRows(data);

  // Banned + whitelist are fetched whenever the game supports them so the
  // summary tiles always have counts; the sections below just toggle the
  // list's visibility, not the fetch.
  const { data: banned, isFetching: bannedFetching } = useQuery({
    queryKey: resourceKey(resourceTarget, "banned", name, ns),
    queryFn: ({ signal }) => resourceClient.withSignal(signal).Players.banned(name, ns),
    enabled: caps?.unban ?? false,
  });
  const { data: whitelist, isFetching: whitelistFetching } = useQuery({
    queryKey: resourceKey(resourceTarget, "whitelist", name, ns),
    queryFn: ({ signal }) => resourceClient.withSignal(signal).Players.whitelist(name, ns),
    enabled: caps?.whitelist ?? false,
  });

  const moderate = useMutation<ModResp, unknown, { action: Action | "unban"; player: string; reason?: string }>({
    mutationFn: (vars) =>
      PlayersAPI.moderate(name, vars.action, {
        name: vars.player,
        ...(vars.reason ? { reason: vars.reason } : {}),
      }, ns),
    onSuccess: (resp, vars) => {
      void qc.invalidateQueries({ queryKey: ["fleet"] });
      setPending(null);
      setReason("");
      setStatus({
        kind: "ok",
        text: resp.raw ? truncate(resp.raw, 200) : intl.formatMessage({ id: "web.routes.tabs.players.action_player_ok", defaultMessage: "{action} {player} ok", description: "Players: status line after a moderation action succeeds; {action} is the action verb, {player} the player name" }, { action: intl.formatMessage(ACTION_WORDS[vars.action]), player: vars.player }),
      });
      return Promise.all([
        qc.invalidateQueries({ queryKey: resourceKey(resourceTarget, "players", name, ns) }),
        qc.invalidateQueries({ queryKey: resourceKey(resourceTarget, "banned", name, ns) }),
      ]);
    },
    onError: (err) => setStatus({ kind: "err", text: errMsg(err, intl) }),
  });

  const whitelistMut = useMutation<ModResp, unknown, { op: "add" | "remove"; player: string }>({
    mutationFn: (vars) =>
      vars.op === "add"
        ? PlayersAPI.whitelistAdd(name, vars.player, ns)
        : PlayersAPI.whitelistRemove(name, vars.player, ns),
    onSuccess: (resp, vars) => {
      void qc.invalidateQueries({ queryKey: ["fleet"] });
      setWlName("");
      setStatus({
        kind: "ok",
        text: resp.raw ? truncate(resp.raw, 200) : intl.formatMessage({ id: "web.routes.tabs.players.whitelist_player_ok", defaultMessage: "whitelist {op} {player} ok", description: "Players: status line after a whitelist change succeeds; {op} is add or remove, {player} the player name" }, { op: intl.formatMessage(ACTION_WORDS[vars.op]), player: vars.player }),
      });
      return qc.invalidateQueries({ queryKey: resourceKey(resourceTarget, "whitelist", name, ns) });
    },
    onError: (err) => setStatus({ kind: "err", text: errMsg(err, intl) }),
  });

  const noModeration = caps && !caps.kick && !caps.ban && !caps.unban && !caps.whitelist;

  return (
    <div className="space-y-6 p-6">
      <div className="grid grid-cols-2 gap-3 sm:grid-cols-3">
        <StatCard
          label={intl.formatMessage({ id: "web.routes.tabs.players.online", defaultMessage: "Online", description: "Players tab: stat card label for the number of online players; keep it short" })}
          value={data && data.online >= 0 ? (data.max >= 0 ? intl.formatMessage({ id: "web.routes.tabs.players.online_of_max", defaultMessage: "{online} / {max}", description: "Players: online player count against the server maximum; {online} is the current count, {max} the maximum" }, { online: String(data.online), max: String(data.max) }) : String(data.online)) : intl.formatMessage(PLAYERS_TEXT.empty_value)}
          accent="success"
        />
        {caps?.whitelist && (
          <StatCard label={intl.formatMessage({ id: "web.routes.tabs.players.whitelisted", defaultMessage: "Whitelisted", description: "Players tab: stat card label for the number of whitelisted players; shown only when the game supports a whitelist" })} value={whitelist?.length ?? intl.formatMessage(PLAYERS_TEXT.empty_value)} accent="primary" />
        )}
        {caps?.unban && (
          <StatCard label={intl.formatMessage({ id: "web.routes.tabs.players.banned", defaultMessage: "Banned", description: "Players tab: stat card label for banned players and heading of the collapsible banned list, followed by a count; keep it short" })} value={banned?.length ?? intl.formatMessage(PLAYERS_TEXT.empty_value)} accent="danger" />
        )}
      </div>

      <header className="flex items-center justify-between">
        <h2 className="text-sm text-muted">
          {data
            ? data.online < 0
              ? intl.formatMessage({ id: "web.routes.tabs.players.count_unknown", defaultMessage: "Player count unknown", description: "Players: heading when the online player count is unknown" })
              : data.max >= 0
                ? intl.formatMessage({ id: "web.routes.tabs.players.online_of_max_heading", defaultMessage: "{online} / {max} online", description: "Players: heading with the online count and the player slot limit; {online} is the number of players online and {max} the slot limit" }, { online: data.online, max: data.max })
                : intl.formatMessage({ id: "web.routes.tabs.players.online_count", defaultMessage: "{online} online", description: "Players: heading with the online player count; {online} is the number of players online" }, { online: data.online })
            : intl.formatMessage({ id: "web.routes.tabs.players.loading", defaultMessage: "Loading…", description: "Players: shown while the player list is loading" })}
        </h2>
        <Button
          isIconOnly
          variant="ghost"
          size="sm"
          onClick={() => refetch()}
          isDisabled={isFetching}
          aria-label={intl.formatMessage({ id: "web.routes.tabs.players.refresh_players", defaultMessage: "Refresh players", description: "Players tab: icon-only button that reloads the player list; read by screen readers; keep it short" })}
        >
          <span title={intl.formatMessage({ id: "web.routes.tabs.players.refresh", defaultMessage: "Refresh", description: "Players tab: tooltip on the refresh icon, same action as the Refresh players button" })}>
            <RotateCw className={cn("h-4 w-4", isFetching && "animate-spin")} />
          </span>
        </Button>
      </header>

      {status && status.kind === "err" && (
        <ErrorBanner
          err={status.text}
          onDismiss={() => setStatus(null)}
        />
      )}
      {status && status.kind === "ok" && (
        <div className="flex items-start justify-between gap-3 rounded border border-border bg-surface/30 px-3 py-2 text-sm">
          <span className="font-mono">{status.text}</span>
          <button onClick={() => setStatus(null)} className="text-xs text-muted hover:text-fg">{intl.formatMessage({ id: "web.routes.tabs.players.dismiss", defaultMessage: "dismiss", description: "Players tab: small text button that closes the result message; keep it short" })}</button>
        </div>
      )}

      {noModeration && (
        <p className="text-sm text-muted">{intl.formatMessage({ id: "web.routes.tabs.players.player_moderation_isn_t_supported_for", defaultMessage: "Player moderation isn't supported for this game.", description: "Players tab: text shown when this game does not support player moderation such as bans" })}</p>
      )}

      <ul className="grid gap-1 md:grid-cols-2 lg:grid-cols-3">
        {rows.map((r) => (
          <li
            key={r.id}
            className="flex items-center justify-between gap-2 rounded border border-border bg-surface/30 px-3 py-2 font-mono text-sm"
          >
            <div className="flex min-w-0 items-center gap-3">
              <span className={cn("truncate", !r.named && "text-muted")}>{r.label}</span>
              {r.faction && (
                <span className="rounded-full bg-accent/10 px-2 py-0.5 font-sans text-xs text-accent">{r.faction}</span>
              )}
            </div>
            {caps && (caps.kick || caps.ban) && (
              <div className="flex items-center gap-1">
                {caps.kick && (
                  <Button
                    isIconOnly
                    variant="ghost"
                    size="sm"
                    isDisabled={!canControl} aria-label={intl.formatMessage({ id: "web.routes.tabs.players.kick", defaultMessage: "Kick", description: "Players: verb for kicking a player (button label and confirm dialog)" })}
                    onClick={() => {
                      setPending({ player: r.id, label: r.label, action: "kick" });
                      setReason("");
                    }}
                  >
                    <span title={intl.formatMessage({ id: "web.routes.tabs.players.kick", defaultMessage: "Kick", description: "Players: verb for kicking a player (button label and confirm dialog)" })}>
                      <UserMinus className="h-4 w-4" />
                    </span>
                  </Button>
                )}
                {caps.ban && (
                  <Button
                    isIconOnly
                    variant="ghost"
                    size="sm"
                    isDisabled={!canControl} aria-label={intl.formatMessage({ id: "web.routes.tabs.players.ban_player_aria", defaultMessage: "Ban {player}", description: "Players: aria-label of the ban button on a player row; {player} is the player name" }, { player: r.label })}
                    onClick={() => {
                      setPending({ player: r.id, label: r.label, action: "ban" });
                      setReason("");
                    }}
                  >
                    <span title={intl.formatMessage({ id: "web.routes.tabs.players.ban", defaultMessage: "Ban", description: "Players: verb for banning a player (label and confirm dialog)" })}>
                      <Ban className="h-4 w-4" />
                    </span>
                  </Button>
                )}
              </div>
            )}
          </li>
        ))}
        {data && rows.length === 0 && <p className="text-sm text-muted">{intl.formatMessage({ id: "web.routes.tabs.players.nobody_online", defaultMessage: "Nobody online.", description: "Players tab: empty-state text in the player list when nobody is online" })}</p>}
      </ul>

      {pending && (
        <ConfirmAction
          player={pending.label}
          playerId={pending.player}
          action={pending.action}
          reason={reason}
          onReasonChange={setReason}
          submitting={moderate.isPending}
          onCancel={() => {
            setPending(null);
            setReason("");
          }}
          onConfirm={() =>
            moderate.mutate({
              action: pending.action,
              player: pending.player,
              reason: reason.trim() || undefined,
            })
          }
        />
      )}

      {caps?.whitelist && (
        <section>
          <button
            onClick={() => setShowWhitelist((v) => !v)}
            className="flex items-center gap-1 text-sm text-muted hover:text-fg"
          >
            {showWhitelist ? <ChevronDown className="h-4 w-4" /> : <ChevronRight className="h-4 w-4" />}
            <ListChecks className="h-4 w-4" /> {intl.formatMessage({ id: "web.routes.tabs.players.whitelist", defaultMessage: "Whitelist", description: "Players tab: collapsible heading of the whitelist section; the count in brackets follows when the list is loaded" })} {whitelist ? intl.formatMessage({ id: "web.routes.tabs.players.count_in_parens", defaultMessage: "({count})", description: "Players: entry count shown in parentheses after a list heading; {count} is the number of entries" }, { count: whitelist.length }) : ""}
          </button>
          {showWhitelist && (
            <div className="mt-2 space-y-2">
              <form
                className="flex items-center gap-2"
                onSubmit={(e) => {
                  e.preventDefault();
                  const n = wlName.trim();
                  if (canControl && n) whitelistMut.mutate({ op: "add", player: n });
                }}
              >
                <Input
                  className="flex-1"
                  placeholder={intl.formatMessage({ id: "web.routes.tabs.players.add_player_to_whitelist", defaultMessage: "Add player to whitelist…", description: "Players tab: placeholder in the box used to add a player name to the whitelist" })}
                  value={wlName}
                  onChange={(e) => setWlName(e.target.value)}
                  maxLength={32}
                />
                <Button
                  size="sm"
                  type="submit"
                  isDisabled={!canControl || !wlName.trim() || whitelistMut.isPending}
                  variant="primary"
                >
                  <UserPlus className="h-4 w-4" /> {intl.formatMessage({ id: "web.routes.tabs.players.add", defaultMessage: "Add", description: "Players tab: button that adds the typed player name to the whitelist; keep it short" })}</Button>
              </form>
              {whitelistFetching && !whitelist && <p className="text-sm text-muted">{intl.formatMessage({ id: "web.routes.tabs.players.loading", defaultMessage: "Loading…", description: "Players: shown while the player list is loading" })}</p>}
              {whitelist?.map((w) => (
                <div
                  key={w}
                  className="flex items-center justify-between gap-2 rounded border border-border bg-surface/30 px-3 py-2 font-mono text-sm"
                >
                  <span className="truncate">{w}</span>
                  <Button
                    isIconOnly
                    variant="ghost"
                    size="sm"
                    aria-label={intl.formatMessage({ id: "web.routes.tabs.players.remove_from_whitelist_aria", defaultMessage: "Remove {player} from whitelist", description: "Players: aria-label of the button removing a player from the whitelist; {player} is the player name" }, { player: w })}
                    isDisabled={!canControl || whitelistMut.isPending}
                    onClick={() => whitelistMut.mutate({ op: "remove", player: w })}
                  >
                    <span title={intl.formatMessage({ id: "web.routes.tabs.players.remove_from_whitelist", defaultMessage: "Remove from whitelist", description: "Players tab: tooltip on the icon button that removes a player from the whitelist; the same action is named in the button's accessible label" })}>
                      <UserMinus className="h-4 w-4" />
                    </span>
                  </Button>
                </div>
              ))}
              {whitelist?.length === 0 && <p className="text-sm text-muted">{intl.formatMessage({ id: "web.routes.tabs.players.whitelist_is_empty", defaultMessage: "Whitelist is empty.", description: "Players tab: empty-state text in the whitelist section when no player is whitelisted" })}</p>}
            </div>
          )}
        </section>
      )}

      {caps?.unban && (
        <section>
          <button
            onClick={() => setShowBanned((v) => !v)}
            className="flex items-center gap-1 text-sm text-muted hover:text-fg"
          >
            {showBanned ? <ChevronDown className="h-4 w-4" /> : <ChevronRight className="h-4 w-4" />}{intl.formatMessage({ id: "web.routes.tabs.players.banned", defaultMessage: "Banned", description: "Players tab: stat card label for banned players and heading of the collapsible banned list, followed by a count; keep it short" })} {banned ? intl.formatMessage({ id: "web.routes.tabs.players.count_in_parens", defaultMessage: "({count})", description: "Players: entry count shown in parentheses after a list heading; {count} is the number of entries" }, { count: banned.length }) : ""}
          </button>
          {showBanned && (
            <div className="mt-2 space-y-1">
              {bannedFetching && !banned && <p className="text-sm text-muted">{intl.formatMessage({ id: "web.routes.tabs.players.loading", defaultMessage: "Loading…", description: "Players: shown while the player list is loading" })}</p>}
              {banned?.map((b) => (
                <div
                  key={b.name}
                  className="flex items-center justify-between gap-2 rounded border border-border bg-surface/30 px-3 py-2 text-sm"
                >
                  <div className="min-w-0 flex-1">
                    <div className="font-mono">{b.name}</div>
                    {(b.reason || b.source) && (
                      <div className="truncate text-xs text-muted">
                        {b.source && <span>{intl.formatMessage({ id: "web.routes.tabs.players.by_source", defaultMessage: "by {source}", description: "Players: source of a ban shown under the player; {source} is who issued the ban" }, { source: b.source })}</span>}
                        {b.source && b.reason && <span>{intl.formatMessage({ id: "web.routes.tabs.players.source_reason_separator", defaultMessage: " · ", description: "Players: separator between the ban source and the ban reason" })}</span>}
                        {b.reason && <span>{b.reason}</span>}
                      </div>
                    )}
                  </div>
                  <Button
                    size="sm"
                    variant="ghost"
                    aria-label={intl.formatMessage({ id: "web.routes.tabs.players.unban_player_aria", defaultMessage: "Unban {player}", description: "Players: aria-label of the unban button on a banned player row; {player} is the player name" }, { player: b.name })}
                    isDisabled={!canControl || moderate.isPending}
                    onClick={() => moderate.mutate({ action: "unban", player: b.name })}
                  >
                    <span title={intl.formatMessage({ id: "web.routes.tabs.players.unban", defaultMessage: "Unban", description: "Players tab: row button that lifts a ban on one player; the text is also its accessible label with the player name; keep it short" })}>
                      <Undo2 className="h-4 w-4" />
                    </span> {intl.formatMessage({ id: "web.routes.tabs.players.unban", defaultMessage: "Unban", description: "Players tab: row button that lifts a ban on one player; the text is also its accessible label with the player name; keep it short" })}</Button>
                </div>
              ))}
              {banned?.length === 0 && <p className="text-sm text-muted">{intl.formatMessage({ id: "web.routes.tabs.players.nobody_is_banned", defaultMessage: "Nobody is banned.", description: "Players tab: empty-state text in the banned section when no player is banned" })}</p>}
            </div>
          )}
        </section>
      )}
    </div>
  );
}

function ConfirmAction({
  player,
  playerId,
  action,
  reason,
  onReasonChange,
  onConfirm,
  onCancel,
  submitting,
}: {
  player: string;
  playerId: string;
  action: Action;
  reason: string;
  onReasonChange: (v: string) => void;
  onConfirm: () => void;
  onCancel: () => void;
  submitting: boolean;
}) {
  const intl = useOptionalIntl();
  const verb = action === "kick"
    ? intl.formatMessage({ id: "web.routes.tabs.players.kick", defaultMessage: "Kick", description: "Players: verb for kicking a player (button label and confirm dialog)" })
    : intl.formatMessage({ id: "web.routes.tabs.players.ban", defaultMessage: "Ban", description: "Players: verb for banning a player (label and confirm dialog)" });
  const verbing = action === "kick"
    ? intl.formatMessage({ id: "web.routes.tabs.players.kicking", defaultMessage: "Kicking", description: "Players: progress verb shown while kicking a player" })
    : intl.formatMessage({ id: "web.routes.tabs.players.banning", defaultMessage: "Banning", description: "Players: progress verb shown while banning a player" });
  return (
    <div className="rounded border border-border bg-surface/50 p-4">
      <p className="text-sm text-foreground">
        {verb} <span className="font-mono">{player}</span>
        {playerId !== player && intl.formatMessage({ id: "web.routes.tabs.players.player_id_in_parens", defaultMessage: " ({playerId})", description: "Players: player ID shown in parentheses after the player name; {playerId} is the ID element" }, { playerId: <span className="font-mono">{playerId}</span> })}
        {intl.formatMessage({ id: "web.routes.tabs.players.question_mark", defaultMessage: "?", description: "Players: question mark that ends the confirm sentence" })}
      </p>
      <Input
        className="mt-3"
        placeholder={intl.formatMessage({ id: "web.routes.tabs.players.reason_optional_max_256_chars", defaultMessage: "Reason (optional, max 256 chars)", description: "Players tab: placeholder in the reason box of the kick or ban confirmation, saying the reason is optional and limited to 256 characters" })}
        value={reason}
        onChange={(e) => onReasonChange(e.target.value)}
        maxLength={256}
        autoFocus
      />
      <div className="mt-3 flex items-center justify-end gap-2">
        <Button
          variant="ghost"
          size="sm"
          onClick={onCancel}
          isDisabled={submitting}
        >{intl.formatMessage({ id: "web.routes.tabs.players.cancel", defaultMessage: "Cancel", description: "Players tab: button that closes the kick or ban confirmation without acting; keep it short" })}</Button>
        <Button
          size="sm"
          variant="primary"
          onClick={onConfirm}
          isDisabled={submitting}
        >
          {submitting ? intl.formatMessage({ id: "web.routes.tabs.players.verbing_ellipsis", defaultMessage: "{verbing}…", description: "Players: confirm button label while the action runs; {verbing} is the progress verb Kicking or Banning" }, { verbing }) : verb}
        </Button>
      </div>
    </div>
  );
}

function truncate(s: string, n: number): string {
  return s.length <= n ? s : s.slice(0, n) + "…";
}

function errMsg(err: unknown, intl: IntlShape): string {
  return errorTextIntl(intl, err, intl.formatMessage({ id: "web.routes.tabs.players.action_failed", defaultMessage: "action failed", description: "Players: error shown when a player action fails and the error has no message" }));
}
