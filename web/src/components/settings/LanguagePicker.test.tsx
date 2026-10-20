import { afterEach, describe, expect, it, vi } from "vitest";
import { http, HttpResponse } from "msw";
import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { server } from "@/test/server";
import { renderWithQuery } from "@/test/render";
import { makeUser } from "@/test/factories";
import { DEFAULT_THEME_PREFERENCES } from "@/lib/useThemePreferences";
import type { LanguageInfo } from "@/lib/i18n/languages";
import type { User } from "@/types";

// The generated language manifest is absent in vitest, so the list is mocked.
// The unlisted entry mirrors the real manifest and must never be shown.
vi.mock("@/lib/i18n/languages", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/lib/i18n/languages")>();
  const all: LanguageInfo[] = [
    actual.ENGLISH,
    { tag: "fr", nativeName: "Français", englishName: "French", direction: "ltr", completeness: 100, listed: true },
    { tag: "de", nativeName: "Deutsch", englishName: "German", direction: "ltr", completeness: 86, listed: true },
    { tag: "ht", nativeName: "Kreyòl", englishName: "Haitian Creole", direction: "ltr", completeness: 20, listed: false },
  ];
  return { ...actual, listedLanguages: () => all.filter((l) => l.listed) };
});

import { LanguagePicker } from "./LanguagePicker";

// savedLanguage stands in for the server: GET /users/me returns it and a
// successful PUT /users/me/language replaces it.
let savedLanguage: string | null = null;
let putBodies: unknown[] = [];

function serveMe(language: string | null) {
  savedLanguage = language;
  server.use(
    http.get("/users/me", () =>
      HttpResponse.json(makeUser({ preferences: { ...DEFAULT_THEME_PREFERENCES, language: savedLanguage } })),
    ),
    http.put("/users/me/language", async ({ request }) => {
      const body = await request.json();
      putBodies.push(body);
      if (body && typeof body === "object" && "language" in body && typeof body.language === "string") {
        savedLanguage = body.language;
      } else {
        savedLanguage = null;
      }
      return new HttpResponse(null, { status: 204 });
    }),
  );
}

afterEach(() => {
  server.resetHandlers();
  putBodies = [];
  savedLanguage = null;
});

describe("LanguagePicker", () => {
  it("lists only listed languages by native name with their completeness", async () => {
    serveMe(null);
    renderWithQuery(<LanguagePicker />);

    expect(await screen.findByRole("radio", { name: /Français/ })).toBeInTheDocument();
    expect(screen.getByRole("radio", { name: /English/ })).toBeInTheDocument();
    const deutsch = screen.getByRole("radio", { name: /Deutsch/ });
    expect(deutsch).toHaveAccessibleName(/86% translated/);
    expect(screen.queryByText("Kreyòl")).not.toBeInTheDocument();
  });

  it("selects Automatic when no language is saved", async () => {
    serveMe(null);
    renderWithQuery(<LanguagePicker />);

    expect(await screen.findByRole("radio", { name: /Automatic/ })).toBeChecked();
    expect(screen.getByRole("radio", { name: /Français/ })).not.toBeChecked();
  });

  it("selects Automatic when the saved language is no longer listed", async () => {
    serveMe("xx");
    renderWithQuery(<LanguagePicker />);

    expect(await screen.findByRole("radio", { name: /Automatic/ })).toBeChecked();
    expect(putBodies).toEqual([]);
  });

  it("choosing Français PUTs the tag and updates the saved language in the me cache", async () => {
    serveMe(null);
    const { client } = renderWithQuery(<LanguagePicker />);
    await waitFor(() => expect(client.getQueryState(["me"])?.status).toBe("success"));

    await userEvent.click(screen.getByRole("radio", { name: /Français/ }));

    await waitFor(() => expect(putBodies).toEqual([{ language: "fr" }]));
    await waitFor(() => expect(screen.getByRole("radio", { name: /Français/ })).toBeChecked());
    expect(client.getQueryData<User>(["me"])?.preferences?.language).toBe("fr");
  });

  it("choosing Automatic PUTs a null language", async () => {
    serveMe("fr");
    const { client } = renderWithQuery(<LanguagePicker />);
    await waitFor(() => expect(client.getQueryState(["me"])?.status).toBe("success"));
    expect(await screen.findByRole("radio", { name: /Français/ })).toBeChecked();

    await userEvent.click(screen.getByRole("radio", { name: /Automatic/ }));

    await waitFor(() => expect(putBodies).toEqual([{ language: null }]));
    await waitFor(() => expect(screen.getByRole("radio", { name: /Automatic/ })).toBeChecked());
  });

  it("shows the server error when the language cannot be saved", async () => {
    serveMe(null);
    server.use(
      http.put("/users/me/language", () =>
        HttpResponse.json({ error: "language is not available" }, { status: 400 }),
      ),
    );
    renderWithQuery(<LanguagePicker />);

    await userEvent.click(await screen.findByRole("radio", { name: /Deutsch/ }));

    expect(await screen.findByRole("alert")).toHaveTextContent("language is not available");
    expect(screen.getByRole("radio", { name: /Automatic/ })).toBeChecked();
  });
});
