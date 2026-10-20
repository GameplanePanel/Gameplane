import { act, cleanup, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { useIntl } from "react-intl";

import { I18nProvider } from "./I18nProvider";

const state = vi.hoisted(() => ({ pseudo: true }));

vi.mock("./languages", async (importOriginal) => {
  const actual = await importOriginal<typeof import("./languages")>();
  const pseudo = {
    tag: "en-XA",
    nativeName: "Pseudo",
    englishName: "Pseudo",
    direction: "ltr" as const,
    completeness: 100,
    listed: true,
  };
  const fr = {
    tag: "fr",
    nativeName: "Français",
    englishName: "French",
    direction: "ltr" as const,
    completeness: 90,
    listed: false,
  };
  return {
    ...actual,
    listedLanguages: () => (state.pseudo ? [actual.ENGLISH, pseudo] : [actual.ENGLISH]),
    allLanguages: () => [actual.ENGLISH, pseudo, fr],
    loadMessages: vi.fn((tag: string) =>
      Promise.resolve(tag === "en-XA" ? { "greeting.hello": [{ type: 0, value: "[Ĥéļļó~·]" }] } : null),
    ),
  };
});

function Probe() {
  const intl = useIntl();
  return <p>{intl.formatMessage({ id: "greeting.hello", defaultMessage: "Hello" })}</p>;
}

beforeEach(() => {
  state.pseudo = true;
  window.localStorage.clear();
  document.documentElement.lang = "en";
  delete document.documentElement.dataset.i18nLoaded;
});

afterEach(() => {
  cleanup();
});

describe("I18nProvider pseudo-language test hook", () => {
  it("exposes the hook and switches to en-XA and back", async () => {
    render(
      <I18nProvider>
        <Probe />
      </I18nProvider>,
    );
    expect(window.gameplaneI18nTest).toBeDefined();

    act(() => window.gameplaneI18nTest?.setOverride("en-XA"));
    expect(await screen.findByText("[Ĥéļļó~·]")).toBeInTheDocument();
    await waitFor(() => expect(document.documentElement.dataset.i18nLoaded).toBe("en-XA"));
    expect(document.documentElement.lang).toBe("en-XA");

    act(() => window.gameplaneI18nTest?.setOverride(null));
    expect(await screen.findByText("Hello")).toBeInTheDocument();
    await waitFor(() => expect(document.documentElement.lang).toBe("en"));
  });

  it("ignores an override that is not listed", () => {
    render(
      <I18nProvider>
        <Probe />
      </I18nProvider>,
    );
    act(() => window.gameplaneI18nTest?.setOverride("fr"));
    expect(screen.getByText("Hello")).toBeInTheDocument();
    expect(document.documentElement.lang).toBe("en");
  });

  it("removes the hook on unmount", () => {
    const { unmount } = render(
      <I18nProvider>
        <Probe />
      </I18nProvider>,
    );
    unmount();
    expect(window.gameplaneI18nTest).toBeUndefined();
  });

  it("has no hook without the pseudo-language", () => {
    state.pseudo = false;
    render(
      <I18nProvider>
        <Probe />
      </I18nProvider>,
    );
    expect(window.gameplaneI18nTest).toBeUndefined();
  });
});
