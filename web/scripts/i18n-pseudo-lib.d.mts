import type { MessageFormatElement } from "@formatjs/icu-messageformat-parser";

export const PSEUDO_TAG: string;
export const LENGTH_FACTOR: number;
export function accent(text: string): string;
export function pseudoLiteral(text: string): string;
export function pseudoLocalizeAst(elements: MessageFormatElement[]): MessageFormatElement[];
export function pseudoLocalizeMessage(message: string): MessageFormatElement[];
export function buildPseudoCatalog(
  reference: Record<string, { defaultMessage: string }>,
): Record<string, MessageFormatElement[]>;
export const pseudoManifestEntry: {
  tag: string;
  nativeName: string;
  englishName: string;
  direction: "ltr" | "rtl";
  completeness: number;
  listed: boolean;
};
