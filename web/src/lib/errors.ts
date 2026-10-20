import { defineMessages, type IntlShape } from "react-intl";

import { APIError } from "@/lib/api";
import { englishIntl } from "@/lib/i18n/format";
import { serverMessageText } from "@/lib/i18n/serverMessage";

const MESSAGES = defineMessages({
  request_failed: {
    id: "web.lib.errors.request_failed",
    defaultMessage: "request failed",
    description: "Fallback error text when a failed request has no usable message",
  },
});

// errorText renders an unknown thrown value as a user-facing string,
// unwrapping the JSON {error|message} body of an APIError when present.
// The optional intl localizes only the fallback text; English is the default.
export function errorText(err: unknown, fallback?: string, intl: IntlShape = englishIntl): string {
  if (err instanceof APIError) {
    try {
      const parsed = JSON.parse(err.body) as { error?: string; message?: string };
      if (parsed.error) return parsed.error;
      if (parsed.message) return parsed.message;
    } catch {
      // body isn't JSON — fall through to the raw body
    }
    return err.body || intl.formatMessage({
      id: "web.lib.errors.request_failed_status",
      defaultMessage: "request failed ({status})",
      description: "Fallback error text for an HTTP error with an empty body; {status} is the HTTP status code",
    }, { status: String(err.status) });
  }
  return err instanceof Error ? err.message : (fallback ?? intl.formatMessage(MESSAGES.request_failed));
}

// errorTextWithStatus is errorText plus an HTTP status prefix for APIError
// (e.g. "404: no pods found") — for diagnostic surfaces (like the admin log
// viewer) where the status code itself is useful context, not just the body.
export function errorTextWithStatus(err: unknown, fallback?: string, intl: IntlShape = englishIntl): string {
  if (err instanceof APIError) return `${err.status}: ${errorText(err, fallback, intl)}`;
  return errorText(err, fallback, intl);
}

// errorTextIntl prefers the translated server message (problem+json messageId)
// when the active catalog has it, and otherwise behaves exactly like errorText.
export function errorTextIntl(intl: IntlShape, err: unknown, fallback?: string): string {
  return serverMessageText(intl, err) ?? errorText(err, fallback, intl);
}
