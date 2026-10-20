import { useContext, useMemo } from "react";
import { IntlContext, createIntl } from "react-intl";
import type { IntlShape } from "react-intl";

import { onIntlError } from "./I18nProvider";

// Locale-aware formatting (spec 019, FR-006). The pure helpers take a locale so
// non-React code can call them; useFormat() binds them to the active locale.
// Default options reproduce Date/Number toLocale*String() with no arguments, so
// English output does not change.

const DATE_DEFAULT: Intl.DateTimeFormatOptions = { year: "numeric", month: "numeric", day: "numeric" };
const TIME_DEFAULT: Intl.DateTimeFormatOptions = { hour: "numeric", minute: "2-digit", second: "2-digit" };
const DATE_TIME_DEFAULT: Intl.DateTimeFormatOptions = { ...DATE_DEFAULT, ...TIME_DEFAULT };

export function formatDateIn(locale: string, value: Date | number, options?: Intl.DateTimeFormatOptions): string {
  return new Intl.DateTimeFormat(locale, options ?? DATE_DEFAULT).format(value);
}

export function formatTimeIn(locale: string, value: Date | number, options?: Intl.DateTimeFormatOptions): string {
  return new Intl.DateTimeFormat(locale, options ?? TIME_DEFAULT).format(value);
}

export function formatDateTimeIn(locale: string, value: Date | number, options?: Intl.DateTimeFormatOptions): string {
  return new Intl.DateTimeFormat(locale, options ?? DATE_TIME_DEFAULT).format(value);
}

export function formatNumberIn(locale: string, value: number, options?: Intl.NumberFormatOptions): string {
  return new Intl.NumberFormat(locale, options).format(value);
}

export function formatRelativeIn(locale: string, value: number, unit: Intl.RelativeTimeFormatUnit): string {
  return new Intl.RelativeTimeFormat(locale).format(value, unit);
}

export function formatListIn(locale: string, items: readonly string[], options?: Intl.ListFormatOptions): string {
  return new Intl.ListFormat(locale, options).format(items);
}

// formatDurationIn renders seconds as unit parts, e.g. 3900 -> "1 hr, 5 min".
// Zero-valued leading units are dropped; zero seconds render as "0 sec".
export function formatDurationIn(locale: string, seconds: number): string {
  const total = Number.isFinite(seconds) ? Math.max(0, Math.round(seconds)) : 0;
  const hours = Math.floor(total / 3600);
  const minutes = Math.floor((total % 3600) / 60);
  const secs = total % 60;
  const unit = (amount: number, name: "hour" | "minute" | "second"): string =>
    formatNumberIn(locale, amount, { style: "unit", unit: name, unitDisplay: "short" });
  const parts: string[] = [];
  if (hours > 0) parts.push(unit(hours, "hour"));
  if (minutes > 0) parts.push(unit(minutes, "minute"));
  if (secs > 0 || parts.length === 0) parts.push(unit(secs, "second"));
  return formatListIn(locale, parts, { style: "long", type: "unit" });
}

export interface FormatApi {
  date: (value: Date | number, options?: Intl.DateTimeFormatOptions) => string;
  time: (value: Date | number, options?: Intl.DateTimeFormatOptions) => string;
  dateTime: (value: Date | number, options?: Intl.DateTimeFormatOptions) => string;
  number: (value: number, options?: Intl.NumberFormatOptions) => string;
  relative: (value: number, unit: Intl.RelativeTimeFormatUnit) => string;
  list: (items: readonly string[], options?: Intl.ListFormatOptions) => string;
  duration: (seconds: number) => string;
}

// makeFormat binds the pure helpers to one locale.
export function makeFormat(locale: string): FormatApi {
  return {
    date: (value, options) => formatDateIn(locale, value, options),
    time: (value, options) => formatTimeIn(locale, value, options),
    dateTime: (value, options) => formatDateTimeIn(locale, value, options),
    number: (value, options) => formatNumberIn(locale, value, options),
    relative: (value, unit) => formatRelativeIn(locale, value, unit),
    list: (items, options) => formatListIn(locale, items, options),
    duration: (seconds) => formatDurationIn(locale, seconds),
  };
}

// englishIntl serves components rendered outside an IntlProvider (isolated
// tests); it is English with no catalog.
export const englishIntl: IntlShape = createIntl({ locale: "en", defaultLocale: "en", onError: onIntlError });

// useOptionalIntl returns the provided IntlShape, or the English fallback when
// no IntlProvider is mounted. Unlike useIntl() it never throws.
export function useOptionalIntl(): IntlShape {
  const provided: IntlShape | null = useContext(IntlContext);
  return provided ?? englishIntl;
}

export function useFormat(): FormatApi {
  const intl = useOptionalIntl();
  return useMemo(() => makeFormat(intl.locale), [intl.locale]);
}
