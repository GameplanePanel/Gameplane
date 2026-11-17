import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { http, HttpResponse } from "msw";
import { server } from "@/test/server";
import { LogHistory } from "./LogHistory";

beforeEach(() => {
  vi.spyOn(HTMLElement.prototype, "scrollHeight", "get").mockImplementation(function (this: HTMLElement) {
    return (this.textContent?.split("\n").length ?? 0) * 20;
  });
});
afterEach(() => vi.restoreAllMocks());

describe("LogHistory scroll position", () => {
  it("opens the console at the bottom and preserves the visible position when older output arrives", async () => {
    let release!: () => void;
    const pending = new Promise<void>((resolve) => { release = resolve; });
    const latest = Array.from({ length: 30 }, (_, index) => `recent ${index}`).join("\n");
    server.use(http.get("/servers/alpha/logs/pod/history", async ({ request }) => {
      const cursor = new URL(request.url).searchParams.get("before");
      if (cursor === "oldest") return HttpResponse.json({ text: "oldest" });
      if (cursor) {
        await pending;
        return HttpResponse.json({ text: "older one\nolder two\n", before: "oldest" });
      }
      return HttpResponse.json({ text: latest, before: "older" });
    }));
    const { container } = render(<LogHistory name="alpha" source="pod" console />);
    await screen.findByText(/recent 29/);
    const output = container.querySelector("pre")!;
    expect(output.scrollTop).toBe(600);
    output.scrollTop = 100;
    await userEvent.click(screen.getByRole("button", { name: "Load older" }));
    // Scrolling during the request is preserved too, not just the click position.
    output.scrollTop = 60;
    release();
    await screen.findByText(/older one/);
    expect(output.textContent).toBe(`older one\nolder two\n${latest}`);
    expect(output.scrollTop).toBe(100);
    output.scrollTop = 25;
    await userEvent.click(screen.getByRole("button", { name: "Load older" }));
    await screen.findByText(/oldest/);
    expect(output.textContent).toBe(`oldest\nolder one\nolder two\n${latest}`);
    expect(output.scrollTop).toBe(45);
    expect(screen.queryByRole("button", { name: "Load older" })).not.toBeInTheDocument();
  });

  it("starts at the newest output again after closing and reopening history", async () => {
    server.use(http.get("/servers/alpha/logs/pod/history", () => HttpResponse.json({ text: "first\nlatest" })));
    const { container } = render(<LogHistory name="alpha" source="pod" console />);
    await screen.findByText(/latest/);
    container.querySelector("pre")!.scrollTop = 0;
    await userEvent.click(screen.getByRole("button", { name: "Hide history" }));
    await userEvent.click(screen.getByRole("button", { name: "Earlier output" }));
    await waitFor(() => expect(container.querySelector("pre")?.scrollTop).toBe(40));
  });

  it("keeps non-console history at the start of its first page", async () => {
    server.use(http.get("/servers/alpha/logs/pod/history", () => HttpResponse.json({ text: "first\nlatest" })));
    const { container } = render(<LogHistory name="alpha" source="pod" />);
    await userEvent.click(screen.getByRole("button", { name: "Earlier output" }));
    await screen.findByText(/latest/);
    expect(container.querySelector("pre")?.scrollTop).toBe(0);
  });
});
