import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { createIntl, IntlProvider } from "react-intl";
import type { IntlShape } from "react-intl";

import { APIError } from "@/lib/api";
import { errorText, errorTextIntl } from "@/lib/errors";
import { serverMessageText, useServerMessage } from "./serverMessage";

const PROBLEM = "application/problem+json";

function problem(body: Record<string, unknown>): APIError {
  return new APIError(409, JSON.stringify(body), "Conflict", PROBLEM);
}

function intlWith(messages: Record<string, string>): IntlShape {
  return createIntl({ locale: "fr", defaultLocale: "en", messages, onError: () => {} });
}

describe("serverMessageText", () => {
  it("returns null for errors that are not APIError", () => {
    expect(serverMessageText(intlWith({}), new Error("nope"))).toBeNull();
    expect(serverMessageText(intlWith({}), "string")).toBeNull();
  });

  it("returns null when the APIError has no messageId", () => {
    const err = new APIError(500, "plain body", "Server Error", "text/plain");
    expect(serverMessageText(intlWith({ "server.x": "Texte" }), err)).toBeNull();
  });

  it("returns null when the messageId is not in the catalog", () => {
    const err = problem({ detail: "English", messageId: "server.unknown" });
    expect(serverMessageText(intlWith({}), err)).toBeNull();
  });

  it("formats the translated message with the problem args", () => {
    const err = problem({
      detail: "server is at 5 of 3",
      messageId: "server.limit.reached",
      args: { max: 3 },
    });
    const intl = intlWith({ "server.limit.reached": "Limite de {max} atteinte" });
    expect(serverMessageText(intl, err)).toBe("Limite de 3 atteinte");
  });

  it("appends the English detail for server.generic.* ids when it differs", () => {
    const err = problem({
      detail: "pod evicted",
      messageId: "server.generic.failed",
    });
    const intl = intlWith({ "server.generic.failed": "Échec de l'opération" });
    expect(serverMessageText(intl, err)).toBe("Échec de l'opération pod evicted");
  });

  it("does not repeat the detail when it equals the translated text", () => {
    const err = problem({ detail: "Échec", messageId: "server.generic.same" });
    const intl = intlWith({ "server.generic.same": "Échec" });
    expect(serverMessageText(intl, err)).toBe("Échec");
  });

  it("does not append detail for non-generic ids", () => {
    const err = problem({ detail: "extra", messageId: "server.named" });
    const intl = intlWith({ "server.named": "Nommé" });
    expect(serverMessageText(intl, err)).toBe("Nommé");
  });

  it("does not append an empty detail", () => {
    const err = problem({ detail: "", messageId: "server.generic.empty" });
    const intl = intlWith({ "server.generic.empty": "Vide" });
    expect(serverMessageText(intl, err)).toBe("Vide");
  });
});

describe("errorTextIntl", () => {
  it("prefers the translated server message", () => {
    const err = problem({ detail: "English", messageId: "server.named" });
    expect(errorTextIntl(intlWith({ "server.named": "Nommé" }), err, "fallback")).toBe("Nommé");
  });

  it("falls back to errorText when the message is not translated", () => {
    const err = new APIError(400, JSON.stringify({ error: "bad input" }), "Bad Request", "application/json");
    expect(errorTextIntl(intlWith({}), err, "fallback")).toBe(errorText(err, "fallback"));
    expect(errorTextIntl(intlWith({}), new Error("boom"), "fallback")).toBe("boom");
  });
});

describe("useServerMessage", () => {
  function Message({ err }: { err: unknown }) {
    return <p>{useServerMessage(err) ?? "none"}</p>;
  }

  it("translates through the surrounding IntlProvider", () => {
    const err = problem({ detail: "English", messageId: "server.named" });
    render(
      <IntlProvider locale="fr" defaultLocale="en" messages={{ "server.named": "Nommé" }}>
        <Message err={err} />
      </IntlProvider>,
    );
    expect(screen.getByText("Nommé")).toBeInTheDocument();
  });

  it("returns null outside a provider so callers keep their English text", () => {
    const err = problem({ detail: "English", messageId: "server.named" });
    render(<Message err={err} />);
    expect(screen.getByText("none")).toBeInTheDocument();
  });
});
