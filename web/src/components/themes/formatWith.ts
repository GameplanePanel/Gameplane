// formatWith formats a theme message that has placeholders. The descriptors in
// messages.ts are defined with defineMessages, whose typed overload does not
// accept placeholder values, so the values go through the descriptor overload.
import type { IntlShape, MessageDescriptor } from "react-intl";

export function formatWith(
  intl: IntlShape,
  descriptor: MessageDescriptor,
  values: Record<string, string | number>,
): string {
  return intl.formatMessage(descriptor, values);
}
