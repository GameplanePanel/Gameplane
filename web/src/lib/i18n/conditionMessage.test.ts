import { describe, expect, it } from "vitest";
import { createIntl } from "react-intl";

import { conditionMessageId, conditionMessageText } from "./conditionMessage";

function intlFor(locale: string, messages: Record<string, string> = {}) {
  return createIntl({ locale, defaultLocale: "en", messages, onError: () => {} });
}

describe("conditionMessageText", () => {
  it("builds the lower-cased catalog id", () => {
    expect(conditionMessageId("AddressAssignment", "PoolNotFound")).toBe("condition.addressassignment.poolnotfound");
  });

  it("returns undefined for an undefined condition", () => {
    expect(conditionMessageText(intlFor("fr"), undefined)).toBeUndefined();
  });

  it("keeps the operator English message for en and en-GB", () => {
    const messages = { "condition.ready.imagepullfailed": "Image pull failed." };
    const condition = { type: "Ready", reason: "ImagePullFailed", message: "English" };
    expect(conditionMessageText(intlFor("en", messages), condition)).toBe("English");
    expect(conditionMessageText(intlFor("en-GB", messages), condition)).toBe("English");
  });

  it("returns the catalog text for a non-English locale", () => {
    const messages = { "condition.ready.imagepullfailed": "Impossible de télécharger l'image." };
    const condition = { type: "Ready", reason: "ImagePullFailed", message: "English" };
    expect(conditionMessageText(intlFor("fr", messages), condition)).toBe("Impossible de télécharger l'image.");
  });

  it("formats the catalog text with the operator arguments", () => {
    const messages = { "condition.addressassignment.poolnotfound": "Pool {pool} introuvable." };
    const condition = { type: "AddressAssignment", reason: "PoolNotFound", message: "Pool west not found." };
    expect(
      conditionMessageText(intlFor("fr", messages), condition, { AddressAssignment: { pool: "west" } }),
    ).toBe("Pool west introuvable.");
  });

  it("falls back to the English message when the arguments are undefined", () => {
    const messages = { "condition.addressassignment.poolnotfound": "Pool {pool} introuvable." };
    const condition = { type: "AddressAssignment", reason: "PoolNotFound", message: "Pool west not found." };
    expect(conditionMessageText(intlFor("fr", messages), condition, undefined)).toBe("Pool west not found.");
  });

  it("falls back to the English message when the arguments lack the condition type", () => {
    const messages = { "condition.addressassignment.poolnotfound": "Pool {pool} introuvable." };
    const condition = { type: "AddressAssignment", reason: "PoolNotFound", message: "Pool west not found." };
    expect(conditionMessageText(intlFor("fr", messages), condition, { Ready: { pool: "west" } })).toBe(
      "Pool west not found.",
    );
  });

  it("falls back to the English message when the catalog has no entry", () => {
    const condition = { type: "Ready", reason: "Running", message: "Server is running." };
    expect(conditionMessageText(intlFor("fr"), condition)).toBe("Server is running.");
  });

  it("falls back to the English message when the condition has no reason", () => {
    const messages = { "condition.ready.running": "En cours." };
    const condition = { type: "Ready", message: "Server is running." };
    expect(conditionMessageText(intlFor("fr", messages), condition)).toBe("Server is running.");
  });

  it("uses the catalog for the en-XA pseudo-language", () => {
    const messages = { "condition.ready.running": "[Ŕûññîñĝ]" };
    const condition = { type: "Ready", reason: "Running", message: "Server is running." };
    expect(conditionMessageText(intlFor("en-XA", messages), condition)).toBe("[Ŕûññîñĝ]");
  });
});
