import type { IntlShape } from "react-intl";

import { useOptionalIntl } from "./format";

// Operator condition messages (spec 019 T057, contracts/server-messages.md §5).
// The operator writes an English message on each condition and, when that
// message interpolates values, the same values in status.conditionArgs[type].
// The catalog holds one condition.<type>.<reason> entry (lower-cased) per reason.

export interface ConditionLike {
  type: string;
  reason?: string;
  message?: string;
}

export type ConditionArgs = Record<string, Record<string, string>>;

export function conditionMessageId(type: string, reason: string): string {
  return `condition.${type.toLowerCase()}.${reason.toLowerCase()}`;
}

// English locales keep the operator's own message: the catalog English is a
// normalized text that can differ from what the operator wrote (it drops the
// detail of a failed backup, for example), and the English must not change.
// The pseudo-language en-XA still uses the catalog, so layout tests see it.
function keepsOperatorText(locale: string): boolean {
  const tag = locale.toLowerCase();
  return tag !== "en-xa" && (tag === "en" || tag.startsWith("en-"));
}

type CatalogMessage = IntlShape["messages"][string];

// takesArguments reports whether a catalog message has a placeholder. Compiled
// messages are AST arrays whose literal elements have type 0.
function takesArguments(message: CatalogMessage): boolean {
  if (typeof message === "string") return message.includes("{");
  return message.some((element) => element.type !== 0);
}

// conditionMessageText returns the condition's message in the active language:
// the catalog entry for its reason, formatted with the operator's arguments,
// or the operator's English message when the language is English, the catalog
// has no entry, or the entry needs arguments the operator did not record.
export function conditionMessageText(
  intl: IntlShape,
  condition: ConditionLike | undefined,
  conditionArgs?: ConditionArgs,
): string | undefined {
  if (!condition) return undefined;
  const english = condition.message;
  if (!condition.reason || keepsOperatorText(intl.locale)) return english;
  const id = conditionMessageId(condition.type, condition.reason);
  const message: CatalogMessage | undefined = Object.hasOwn(intl.messages, id) ? intl.messages[id] : undefined;
  if (message === undefined) return english;
  const args = conditionArgs?.[condition.type];
  if (!args && takesArguments(message)) return english;
  const text: unknown = intl.formatMessage(
    { id, description: "Operator condition; text and note come from the condition.* catalog entry" },
    args,
  );
  return typeof text === "string" ? text : english;
}

// useConditionMessage is conditionMessageText bound to the active intl instance.
export function useConditionMessage(condition: ConditionLike | undefined, conditionArgs?: ConditionArgs): string | undefined {
  return conditionMessageText(useOptionalIntl(), condition, conditionArgs);
}
