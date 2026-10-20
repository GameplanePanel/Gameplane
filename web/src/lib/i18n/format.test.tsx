import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { IntlProvider } from "react-intl";

import {
  formatDateIn,
  formatDateTimeIn,
  formatDurationIn,
  formatListIn,
  formatNumberIn,
  formatRelativeIn,
  formatTimeIn,
  makeFormat,
  useFormat,
} from "./format";

// Dates are compared against Intl output rather than literal strings so the
// tests do not depend on the ICU data version of the runner.
const when = new Date(2026, 0, 15, 14, 5, 9);

describe("pure format helpers", () => {
  it("formats dates with the default options in en and fr", () => {
    expect(formatDateIn("en", when)).toBe(
      new Intl.DateTimeFormat("en", { year: "numeric", month: "numeric", day: "numeric" }).format(when),
    );
    expect(formatDateIn("fr", when)).toBe(
      new Intl.DateTimeFormat("fr", { year: "numeric", month: "numeric", day: "numeric" }).format(when),
    );
  });

  it("uses explicit date options when given", () => {
    const options: Intl.DateTimeFormatOptions = { dateStyle: "long" };
    expect(formatDateIn("en", when, options)).toBe(new Intl.DateTimeFormat("en", options).format(when));
  });

  it("formats times with the default options and accepts epoch numbers", () => {
    const expected = new Intl.DateTimeFormat("en", { hour: "numeric", minute: "2-digit", second: "2-digit" }).format(
      when,
    );
    expect(formatTimeIn("en", when)).toBe(expected);
    expect(formatTimeIn("en", when.getTime())).toBe(expected);
    expect(formatTimeIn("fr", when)).toBe(
      new Intl.DateTimeFormat("fr", { hour: "numeric", minute: "2-digit", second: "2-digit" }).format(when),
    );
  });

  it("formats combined date and time in both locales", () => {
    const enDefault = formatDateTimeIn("en", when);
    expect(enDefault).toContain(formatDateIn("en", when));
    expect(enDefault).toContain(formatTimeIn("en", when));
    expect(formatDateTimeIn("fr", when, { dateStyle: "short" })).toBe(
      new Intl.DateTimeFormat("fr", { dateStyle: "short" }).format(when),
    );
  });

  it("formats numbers in en and fr", () => {
    expect(formatNumberIn("en", 1234567)).toBe(new Intl.NumberFormat("en").format(1234567));
    expect(formatNumberIn("fr", 1234567)).toBe(new Intl.NumberFormat("fr").format(1234567));
    expect(formatNumberIn("en", 1234, { notation: "compact" })).toBe(
      new Intl.NumberFormat("en", { notation: "compact" }).format(1234),
    );
  });

  it("formats relative time", () => {
    expect(formatRelativeIn("en", -3, "hour")).toBe(new Intl.RelativeTimeFormat("en").format(-3, "hour"));
    expect(formatRelativeIn("fr", 2, "day")).toBe(new Intl.RelativeTimeFormat("fr").format(2, "day"));
  });

  it("formats lists in en and fr", () => {
    expect(formatListIn("en", ["a", "b", "c"])).toBe(new Intl.ListFormat("en").format(["a", "b", "c"]));
    expect(formatListIn("fr", ["a", "b"], { type: "disjunction" })).toBe(
      new Intl.ListFormat("fr", { type: "disjunction" }).format(["a", "b"]),
    );
  });

  it("formats durations as unit parts and drops zero leading units", () => {
    const unitOf = (locale: string, amount: number, unit: "hour" | "minute" | "second") =>
      new Intl.NumberFormat(locale, { style: "unit", unit, unitDisplay: "short" }).format(amount);
    const enHour = unitOf("en", 1, "hour");
    const enMinute = unitOf("en", 5, "minute");
    expect(formatDurationIn("en", 3900)).toBe(
      new Intl.ListFormat("en", { style: "long", type: "unit" }).format([enHour, enMinute]),
    );
    expect(formatDurationIn("en", 0)).toBe(unitOf("en", 0, "second"));
    expect(formatDurationIn("en", 45)).toBe(unitOf("en", 45, "second"));
    expect(formatDurationIn("en", 120)).toBe(
      new Intl.ListFormat("en", { style: "long", type: "unit" }).format([unitOf("en", 2, "minute")]),
    );
    expect(formatDurationIn("fr", 3600)).toBe(
      new Intl.ListFormat("fr", { style: "long", type: "unit" }).format([unitOf("fr", 1, "hour")]),
    );
  });

  it("clamps invalid durations to zero seconds", () => {
    expect(formatDurationIn("en", -5)).toBe(formatDurationIn("en", 0));
    expect(formatDurationIn("en", Number.NaN)).toBe(formatDurationIn("en", 0));
    expect(formatDurationIn("en", Number.POSITIVE_INFINITY)).toBe(formatDurationIn("en", 0));
  });
});

describe("makeFormat", () => {
  it("binds the helpers to one locale", () => {
    const fmt = makeFormat("fr");
    expect(fmt.number(1000)).toBe(formatNumberIn("fr", 1000));
    expect(fmt.date(when)).toBe(formatDateIn("fr", when));
    expect(fmt.time(when)).toBe(formatTimeIn("fr", when));
    expect(fmt.dateTime(when)).toBe(formatDateTimeIn("fr", when));
    expect(fmt.relative(1, "week")).toBe(formatRelativeIn("fr", 1, "week"));
    expect(fmt.list(["x", "y"])).toBe(formatListIn("fr", ["x", "y"]));
    expect(fmt.duration(90)).toBe(formatDurationIn("fr", 90));
  });

  it("passes options through to the helpers", () => {
    const fmt = makeFormat("en");
    expect(fmt.number(0.5, { style: "percent" })).toBe(formatNumberIn("en", 0.5, { style: "percent" }));
    expect(fmt.dateTime(when, { dateStyle: "medium" })).toBe(
      formatDateTimeIn("en", when, { dateStyle: "medium" }),
    );
    expect(fmt.time(when, { hour: "2-digit" })).toBe(formatTimeIn("en", when, { hour: "2-digit" }));
    expect(fmt.date(when, { month: "long" })).toBe(formatDateIn("en", when, { month: "long" }));
    expect(fmt.list(["x", "y"], { type: "disjunction" })).toBe(
      formatListIn("en", ["x", "y"], { type: "disjunction" }),
    );
  });
});

describe("useFormat", () => {
  function Stamp() {
    const fmt = useFormat();
    return <p>{fmt.date(when, { dateStyle: "long" })}</p>;
  }

  it("uses the English fallback outside an IntlProvider", () => {
    render(<Stamp />);
    expect(screen.getByText(formatDateIn("en", when, { dateStyle: "long" }))).toBeInTheDocument();
  });

  it("follows the locale of the surrounding IntlProvider", () => {
    render(
      <IntlProvider locale="fr" defaultLocale="en" messages={{}}>
        <Stamp />
      </IntlProvider>,
    );
    expect(screen.getByText(formatDateIn("fr", when, { dateStyle: "long" }))).toBeInTheDocument();
  });
});
