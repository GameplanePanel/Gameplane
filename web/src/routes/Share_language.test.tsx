import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { SharePage } from "./Share";
import { Shares, APIError } from "@/lib/api";
import { I18nProvider } from "@/lib/i18n/I18nProvider";

const mockUseParams = vi.hoisted(() => vi.fn());

vi.mock("@/lib/api", () => ({
  Shares: {
    resolve: vi.fn(),
    start: vi.fn(),
  },
  APIError: class APIError extends Error {
    status: number;
    body: string;
    retryAfter: string | null;
    constructor(status: number, body: string, _statusText?: string, _contentType?: string, retryAfter: string | null = null) {
      super(`${status}: ${body}`);
      this.status = status;
      this.body = body;
      this.retryAfter = retryAfter;
    }
  },
}));

vi.mock("@tanstack/react-router", async () => {
  const actual = await vi.importActual("@tanstack/react-router");
  return {
    ...actual,
    useParams: mockUseParams,
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

function renderShare() {
  mockUseParams.mockReturnValue({ token: "test-token" });
  return render(
    <I18nProvider>
      <SharePage />
    </I18nProvider>,
  );
}

describe("SharePage compact language picker (T045)", () => {
  beforeEach(() => {
    vi.resetAllMocks();
    localStorage.clear();
  });

  it("shows the picker in the corner of the Online card", async () => {
    vi.mocked(Shares.resolve).mockResolvedValue({
      serverName: "mc-survival",
      status: "Running",
      address: { host: "play.gameplane.example", port: 25565 },
      playersOnline: 3,
    });
    renderShare();

    expect(await screen.findByText("mc-survival")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Language" })).toHaveTextContent("English");
  });

  it("shows the picker on the invalid-link card", async () => {
    vi.mocked(Shares.resolve).mockRejectedValue(new APIError(404, "not found"));
    renderShare();

    expect(await screen.findByText("Link not available")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Language" })).toBeInTheDocument();
  });

  it("choosing Français stores the browser choice", async () => {
    vi.mocked(Shares.resolve).mockResolvedValue({
      serverName: "mc-survival",
      status: "Running",
      address: { host: "play.gameplane.example", port: 25565 },
      playersOnline: 3,
    });
    renderShare();

    expect(await screen.findByText("mc-survival")).toBeInTheDocument();
    await userEvent.click(await screen.findByRole("button", { name: "Language" }));
    await userEvent.click(await screen.findByRole("menuitem", { name: "Français" }));

    const stored = JSON.parse(localStorage.getItem("gameplane-language") ?? "{}") as { choice?: string };
    expect(stored.choice).toBe("fr");
  });
});
