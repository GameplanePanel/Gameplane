import { render, screen, waitFor, act } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { Auth } from "@/lib/endpoints";
import type { User } from "@/types";
import { AppI18nProvider, useLanguage } from "./I18nProvider";

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
  return {
    ...actual,
    listedLanguages: () => [actual.ENGLISH, fr],
    loadMessages: vi.fn(() => Promise.resolve(null)),
  };
});

function Probe() {
  const { language } = useLanguage();
  return <p data-testid="state">{language}</p>;
}

function user(language: string | null): User {
  return {
    id: 1,
    username: "u",
    displayName: "U",
    email: "u@example.test",
    role: "viewer",
    preferences: {
      themeType: "preset",
      presetId: "pink",
      appearanceMode: "system",
      customCssEnabled: false,
      language,
    },
  } as unknown as User;
}

function renderApp(client: QueryClient) {
  return render(
    <QueryClientProvider client={client}>
      <AppI18nProvider>
        <Probe />
      </AppI18nProvider>
    </QueryClientProvider>,
  );
}

function newClient(): QueryClient {
  return new QueryClient({ defaultOptions: { queries: { retry: false } } });
}

describe("AppI18nProvider", () => {
  let localeSpy: ReturnType<typeof vi.spyOn>;
  const languagesDescriptor = Object.getOwnPropertyDescriptor(window.navigator, "languages");

  beforeEach(() => {
    window.localStorage.clear();
    // Keep the browser candidates from matching English before the instance default.
    Object.defineProperty(window.navigator, "languages", { value: ["xx-YY"], configurable: true });
    localeSpy = vi.spyOn(Auth, "locale").mockResolvedValue({ defaultLanguage: "en" });
  });

  afterEach(() => {
    localeSpy.mockRestore();
    window.localStorage.clear();
    if (languagesDescriptor) {
      Object.defineProperty(window.navigator, "languages", languagesDescriptor);
    }
  });

  it("uses the saved user language from the cached profile ahead of the instance default", async () => {
    const client = newClient();
    client.setQueryData(["me"], user("fr"));
    renderApp(client);
    await waitFor(() => expect(screen.getByTestId("state")).toHaveTextContent("fr"));
    await waitFor(() => expect(localeSpy).toHaveBeenCalled());
  });

  it("does not fetch /users/me itself", async () => {
    const client = newClient();
    const fetchSpy = vi.spyOn(client, "fetchQuery");
    renderApp(client);
    await waitFor(() => expect(localeSpy).toHaveBeenCalled());
    expect(fetchSpy).not.toHaveBeenCalledWith(expect.objectContaining({ queryKey: ["me"] }));
    expect(client.getQueryData(["me"])).toBeUndefined();
  });

  it("falls back to the instance default when the user has no saved language", async () => {
    localeSpy.mockResolvedValue({ defaultLanguage: "fr" });
    const client = newClient();
    client.setQueryData(["me"], user(null));
    renderApp(client);
    await waitFor(() => expect(screen.getByTestId("state")).toHaveTextContent("fr"));
  });

  it("falls back to English when the locale request fails", async () => {
    localeSpy.mockRejectedValue(new Error("network down"));
    renderApp(newClient());
    await waitFor(() => expect(localeSpy).toHaveBeenCalled());
    await act(async () => {
      await new Promise((resolve) => setTimeout(resolve, 0));
    });
    expect(screen.getByTestId("state")).toHaveTextContent("en");
  });

  it("re-resolves when the cached profile gains a saved language", async () => {
    localeSpy.mockResolvedValue({ defaultLanguage: "en" });
    const client = newClient();
    renderApp(client);
    await waitFor(() => expect(localeSpy).toHaveBeenCalled());
    expect(screen.getByTestId("state")).toHaveTextContent("en");
    act(() => {
      client.setQueryData(["me"], user("fr"));
    });
    await waitFor(() => expect(screen.getByTestId("state")).toHaveTextContent("fr"));
  });
});
