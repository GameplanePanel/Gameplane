import type { GameServer, GameTemplate } from "@/types";

export interface SectionProps {
  draft: GameServer;
  onChange: (next: GameServer) => void;
  template?: GameTemplate;
  /**
   * spec.config of the last saved server (stored passwords are the redaction
   * marker). Lets the game-config section tell "will be removed" from "unset"
   * across remounts.
   */
  storedConfig?: Record<string, string>;
  onValidityChange?: (valid: boolean) => void;
}

export const DESCRIPTION_ANNOTATION = "gameplane.local/description";
// Legacy: the grace period is now stored in spec.stopGracePeriodSeconds (the
// field the operator actually reads). Nothing reads this annotation — it is
// kept only as a read fallback for servers created before the migration, and
// is deleted on the first edit. Never write it.
export const GRACE_PERIOD_ANNOTATION = "gameplane.local/grace-period-seconds";
