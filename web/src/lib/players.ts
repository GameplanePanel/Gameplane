import type { PlayersResp } from "@/types";

export interface PlayerRow {
  /** Moderation identifier: the Steam ID for structured entries, the flat name otherwise. */
  id: string;
  /** What to render: display name, else the raw Steam ID (never blank). */
  label: string;
  faction?: string;
  /** False when label is a raw Steam ID fallback. */
  named: boolean;
}

export function playerRows(resp: PlayersResp | undefined): PlayerRow[] {
  if (!resp) return [];
  if (resp.entries) {
    return resp.entries.map((e) => {
      const name = e.displayName?.trim();
      return { id: e.steamId, label: name ? name : e.steamId, faction: e.faction || undefined, named: Boolean(name) };
    });
  }
  return resp.players.map((p) => ({ id: p, label: p, named: true }));
}
