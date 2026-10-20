import type { IntlShape } from "react-intl";

import { APIError } from "@/lib/api";

import { useOptionalIntl } from "./format";

// serverMessageText returns the translated text for an API error carrying a
// messageId (problem+json) that exists in the active catalog. Returns null when
// the error has no messageId or the id is not translated, so the caller keeps
// its existing English text.
export function serverMessageText(intl: IntlShape, err: unknown): string | null {
  if (!(err instanceof APIError) || !err.messageId) return null;
  const id = err.messageId;
  if (!Object.hasOwn(intl.messages, id)) return null;
  // The id comes from the API response; its English text and translator note
  // live in the server.* catalog entries (api/internal/messages), not here.
  const formatted: unknown = intl.formatMessage(
    { id, description: "API error; text and note come from the server.* catalog entry" },
    err.args,
  );
  if (typeof formatted !== "string") return null;
  // Generic server errors carry a free-form English detail (for example the
  // underlying Kubernetes message) that is shown after the translated sentence.
  if (id.startsWith("server.generic.") && err.detail && err.detail !== formatted) {
    return `${formatted} ${err.detail}`;
  }
  return formatted;
}

// useServerMessage is serverMessageText bound to the active intl instance.
export function useServerMessage(err: unknown): string | null {
  return serverMessageText(useOptionalIntl(), err);
}
