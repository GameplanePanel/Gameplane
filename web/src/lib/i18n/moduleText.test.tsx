import { afterEach, describe, expect, it, vi } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { IntlProvider } from "react-intl";

import { Modules } from "@/lib/endpoints";

import { moduleIdSegment, moduleIds, moduleTextFrom, useModuleText } from "./moduleText";

afterEach(() => {
  vi.restoreAllMocks();
});

describe("moduleIdSegment and moduleIds", () => {
  it("escapes ~ then . in one id segment", () => {
    expect(moduleIdSegment("a.b")).toBe("a~1b");
    expect(moduleIdSegment("a~b")).toBe("a~0b");
    expect(moduleIdSegment("~.")).toBe("~0~1");
  });

  it("builds config option ids with escaped segments", () => {
    expect(moduleIds.configOption("difficulty", "1.5")).toBe("config.difficulty.options.1~15");
  });
});

describe("moduleTextFrom", () => {
  it("returns the fallback when there are no messages", () => {
    expect(moduleTextFrom("fr", undefined)("template.displayName", "Server")).toBe("Server");
    expect(moduleTextFrom("fr", {})("template.displayName", "Server")).toBe("Server");
  });

  it("formats an ICU message in the locale", () => {
    const text = moduleTextFrom("fr", { "template.displayName": "Serveur ''Java''" });
    expect(text("template.displayName", "Server")).toBe("Serveur 'Java'");
  });

  it("returns the fallback for an id the module lacks", () => {
    const text = moduleTextFrom("fr", { "template.displayName": "Serveur" });
    expect(text("template.description", "A description")).toBe("A description");
  });
});

function Probe() {
  const text = useModuleText("minecraft");
  return <p>{text("template.displayName", "Minecraft")}</p>;
}

function renderProbe(locale: string) {
  return render(
    <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
      <IntlProvider locale={locale} defaultLocale="en" messages={{}} onError={() => {}}>
        <Probe />
      </IntlProvider>
    </QueryClientProvider>,
  );
}

describe("useModuleText", () => {
  it("shows the template text in English without fetching", () => {
    const spy = vi.spyOn(Modules, "locales").mockResolvedValue({ "template.displayName": "Minecraft (EN)" });
    renderProbe("en");
    expect(screen.getByText("Minecraft")).toBeInTheDocument();
    expect(spy).not.toHaveBeenCalled();
  });

  it("shows the module translation when one is shipped", async () => {
    const spy = vi.spyOn(Modules, "locales").mockResolvedValue({ "template.displayName": "Minecraft (FR)" });
    renderProbe("fr");
    expect(await screen.findByText("Minecraft (FR)")).toBeInTheDocument();
    expect(spy).toHaveBeenCalledWith("minecraft", "fr", "local");
  });

  it("keeps the template text when the translations cannot be fetched", async () => {
    const spy = vi.spyOn(Modules, "locales").mockRejectedValue(new Error("boom"));
    renderProbe("fr");
    await waitFor(() => expect(spy).toHaveBeenCalled());
    expect(screen.getByText("Minecraft")).toBeInTheDocument();
  });
});
