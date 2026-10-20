import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { MessageFormatError, ReactIntlError, ReactIntlErrorCode, useIntl } from "react-intl";

import { I18nProvider, onIntlError, useLanguage } from "./I18nProvider";
import { loadMessages } from "./languages";
import type { CompiledCatalog } from "./languages";

vi.mock("./languages", async (importOriginal) => {
  const actual = await importOriginal<typeof import("./languages")>();
  const fr = {
    tag: "fr",
    nativeName: "Français",
    englishName: "French",
    direction: "ltr" as const,
    completeness: 90,
    listed: true,
  };
  const catalogs: Record<string, CompiledCatalog> = {
    fr: { "greeting.hello": [{ type: 0, value: "Bonjour" }] },
  };
  return {
    ...actual,
    listedLanguages: () => [actual.ENGLISH, fr],
    loadMessages: vi.fn((tag: string) => Promise.resolve(catalogs[tag] ?? null)),
  };
});

// Probe renders what a stateful consumer sees: the resolved language, a translated
// and a missing-translation message, a text input, and buttons to change language.
function Probe() {
  const { language, setLanguage, direction } = useLanguage();
  const intl = useIntl();
  return (
    <div>
      <p data-testid="state">{`${language}/${direction}`}</p>
      <p>{intl.formatMessage({ id: "greeting.hello", defaultMessage: "Hello" })}</p>
      <p>{intl.formatMessage({ id: "missing.key", defaultMessage: "Fallback text" })}</p>
      <input aria-label="name" />
      <button type="button" onClick={() => setLanguage("fr")}>
        choose fr
      </button>
      <button type="button" onClick={() => setLanguage(null)}>
        auto
      </button>
    </div>
  );
}

describe("I18nProvider", () => {
  beforeEach(() => {
    window.localStorage.clear();
    document.documentElement.lang = "en";
    document.documentElement.dir = "ltr";
  });

  afterEach(() => {
    vi.restoreAllMocks();
    window.localStorage.clear();
  });

  it("renders children with English defaults and sets the document language", async () => {
    render(
      <I18nProvider>
        <Probe />
      </I18nProvider>,
    );
    expect(screen.getByTestId("state")).toHaveTextContent("en/ltr");
    expect(screen.getByText("Hello")).toBeInTheDocument();
    await waitFor(() => expect(window.localStorage.getItem("gameplane-language")).toContain('"applied":"en"'));
    expect(document.documentElement.lang).toBe("en");
    expect(document.documentElement.dir).toBe("ltr");
  });

  it("switches language without unmounting a stateful child", async () => {
    render(
      <I18nProvider>
        <Probe />
      </I18nProvider>,
    );
    const input = screen.getByLabelText("name");
    fireEvent.change(input, { target: { value: "typed" } });

    fireEvent.click(screen.getByRole("button", { name: "choose fr" }));

    await waitFor(() => expect(document.documentElement.lang).toBe("fr"));
    expect(screen.getByLabelText("name")).toBe(input);
    expect(input).toHaveValue("typed");
    await waitFor(() => expect(screen.getByText("Bonjour")).toBeInTheDocument());
    expect(screen.getByTestId("state")).toHaveTextContent("fr/ltr");
  });

  it("renders the defaultMessage when the catalog lacks the id (FR-008)", async () => {
    render(
      <I18nProvider>
        <Probe />
      </I18nProvider>,
    );
    fireEvent.click(screen.getByRole("button", { name: "choose fr" }));
    await waitFor(() => expect(screen.getByText("Bonjour")).toBeInTheDocument());
    expect(screen.getByText("Fallback text")).toBeInTheDocument();
  });

  it("writes the applied language to storage after a change", async () => {
    render(
      <I18nProvider>
        <Probe />
      </I18nProvider>,
    );
    fireEvent.click(screen.getByRole("button", { name: "choose fr" }));
    await waitFor(() => expect(window.localStorage.getItem("gameplane-language")).toContain('"applied":"fr"'));
    expect(window.localStorage.getItem("gameplane-language")).toContain('"choice":"fr"');
  });

  it("returns to automatic resolution when the choice is cleared", async () => {
    render(
      <I18nProvider>
        <Probe />
      </I18nProvider>,
    );
    fireEvent.click(screen.getByRole("button", { name: "choose fr" }));
    await waitFor(() => expect(document.documentElement.lang).toBe("fr"));
    fireEvent.click(screen.getByRole("button", { name: "auto" }));
    await waitFor(() => expect(document.documentElement.lang).toBe("en"));
    expect(window.localStorage.getItem("gameplane-language")).toContain('"choice":null');
  });

  it("uses the user language from the account ahead of storage", async () => {
    window.localStorage.setItem("gameplane-language", JSON.stringify({ choice: "fr", applied: "fr" }));
    render(
      <I18nProvider userLanguage="en">
        <Probe />
      </I18nProvider>,
    );
    expect(screen.getByTestId("state")).toHaveTextContent("en/ltr");
  });

  it("applies the stored browser choice on mount", async () => {
    window.localStorage.setItem("gameplane-language", JSON.stringify({ choice: "fr", applied: "en" }));
    render(
      <I18nProvider>
        <Probe />
      </I18nProvider>,
    );
    await waitFor(() => expect(document.documentElement.lang).toBe("fr"));
    await waitFor(() => expect(screen.getByText("Bonjour")).toBeInTheDocument());
  });

  it("keeps rendering with English when a catalog fails to load", async () => {
    render(
      <I18nProvider>
        <Probe />
      </I18nProvider>,
    );
    // Arm the one-shot failure only after the initial "en" load, so it is consumed by "fr".
    await waitFor(() => expect(loadMessages).toHaveBeenCalledWith("en"));
    vi.mocked(loadMessages).mockImplementationOnce(() => Promise.reject(new Error("chunk failed")));
    fireEvent.click(screen.getByRole("button", { name: "choose fr" }));
    await waitFor(() => expect(loadMessages).toHaveBeenCalledWith("fr"));
    // Flush the rejection through the provider's catch handler.
    await act(async () => {
      await new Promise((resolve) => setTimeout(resolve, 0));
    });
    expect(document.documentElement.lang).toBe("fr");
    expect(screen.queryByText("Bonjour")).toBeNull();
    expect(screen.getByText("Hello")).toBeInTheDocument();
    expect(screen.getByLabelText("name")).toBeInTheDocument();
  });

  it("ignores a catalog that resolves after the language moved on", async () => {
    let resolveFr: (value: CompiledCatalog | null) => void = () => {};
    render(
      <I18nProvider>
        <Probe />
      </I18nProvider>,
    );
    // Arm the pending "fr" load only after the initial "en" load has been requested.
    await waitFor(() => expect(loadMessages).toHaveBeenCalledWith("en"));
    vi.mocked(loadMessages).mockImplementationOnce(
      () => new Promise<CompiledCatalog | null>((resolve) => (resolveFr = resolve)),
    );
    fireEvent.click(screen.getByRole("button", { name: "choose fr" }));
    await waitFor(() => expect(loadMessages).toHaveBeenCalledWith("fr"));
    fireEvent.click(screen.getByRole("button", { name: "auto" }));
    await waitFor(() => expect(loadMessages).toHaveBeenLastCalledWith("en"));
    // The stale "fr" catalog resolves after the cancelled effect; it must be ignored.
    await act(async () => {
      resolveFr({ "greeting.hello": [{ type: 0, value: "Bonjour" }] });
      await new Promise((resolve) => setTimeout(resolve, 0));
    });
    expect(screen.getByText("Hello")).toBeInTheDocument();
    expect(screen.queryByText("Bonjour")).not.toBeInTheDocument();
  });

  it("uses the English fallback outside a provider", () => {
    function Outside() {
      const { language, direction, setLanguage } = useLanguage();
      return (
        <button type="button" onClick={() => setLanguage("fr")}>
          {`${language}:${direction}`}
        </button>
      );
    }
    render(<Outside />);
    // setLanguage is a no-op outside a provider: the page stays English.
    fireEvent.click(screen.getByRole("button", { name: "en:ltr" }));
    expect(screen.getByRole("button", { name: "en:ltr" })).toBeInTheDocument();
    expect(document.documentElement.lang).toBe("en");
  });
});

describe("onIntlError", () => {
  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("ignores missing translations", () => {
    const warn = vi.spyOn(console, "warn").mockImplementation(() => {});
    onIntlError(new ReactIntlError(ReactIntlErrorCode.MISSING_TRANSLATION, "missing"));
    expect(warn).not.toHaveBeenCalled();
  });

  it("warns for other errors in development", () => {
    const warn = vi.spyOn(console, "warn").mockImplementation(() => {});
    onIntlError(new MessageFormatError("boom", "fr"));
    expect(warn).toHaveBeenCalledOnce();
  });
});
