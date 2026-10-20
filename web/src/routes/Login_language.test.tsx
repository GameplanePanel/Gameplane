import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { useNavigate } from "@tanstack/react-router";
import { renderWithQuery } from "@/test/render";
import { I18nProvider } from "@/lib/i18n/I18nProvider";
import { LoginPage } from "./Login";

vi.mock("@tanstack/react-router", async () => {
  const actual = await vi.importActual("@tanstack/react-router");
  return {
    ...actual,
    useNavigate: vi.fn(),
  };
});

// Lists French next to English so the compact picker has a real choice.
// The generated manifest does not exist under vitest, so the list is mocked.
vi.mock("@/lib/i18n/languages", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/lib/i18n/languages")>();
  return {
    ...actual,
    listedLanguages: () => [
      actual.ENGLISH,
      {
        tag: "fr",
        nativeName: "Français",
        englishName: "French",
        direction: "ltr" as const,
        completeness: 90,
        listed: true,
      },
    ],
  };
});

const fetchMock = vi.fn();
const navigateMock = vi.fn().mockResolvedValue(undefined);

function router(url: RequestInfo | URL): Promise<Response> {
  const u = String(url);
  if (u.includes("/auth/providers")) {
    return Promise.resolve(
      new Response(JSON.stringify({ providers: [{ kind: "local", label: "Local account" }] }), { status: 200 }),
    );
  }
  return Promise.resolve(new Response("{}", { status: 200 }));
}

function renderLogin() {
  return renderWithQuery(
    <I18nProvider>
      <LoginPage />
    </I18nProvider>,
  );
}

beforeEach(() => {
  localStorage.clear();
  fetchMock.mockImplementation(router);
  vi.stubGlobal("fetch", fetchMock);
  vi.mocked(useNavigate).mockReturnValue(navigateMock);
});

afterEach(() => {
  fetchMock.mockReset();
  navigateMock.mockReset();
  vi.unstubAllGlobals();
  localStorage.clear();
});

describe("LoginPage compact language picker (T045)", () => {
  it("shows the picker with the current language in the card corner", async () => {
    renderLogin();
    const trigger = await screen.findByRole("button", { name: "Language" });
    expect(trigger).toHaveTextContent("English");
  });

  it("choosing Français stores the browser choice and relabels the picker", async () => {
    renderLogin();
    await userEvent.click(await screen.findByRole("button", { name: "Language" }));
    await userEvent.click(await screen.findByRole("menuitem", { name: "Français" }));

    const stored = JSON.parse(localStorage.getItem("gameplane-language") ?? "{}") as { choice?: string };
    expect(stored.choice).toBe("fr");
    expect(await screen.findByRole("button", { name: "Language" })).toHaveTextContent("Français");
  });
});
