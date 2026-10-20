import { describe, it, expect, vi, beforeEach } from "vitest";
import type { ReactNode } from "react";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { CloneServerDialog } from "./CloneServerDialog";
import { APIError } from "@/lib/api";

// Mock the endpoints
vi.mock("@/lib/endpoints", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/lib/endpoints")>();
  const Servers = { ...actual.Servers, clone: vi.fn() };
  const createResourceClient: typeof actual.createResourceClient = vi.fn((...args: Parameters<typeof actual.createResourceClient>) => ({
    ...actual.createResourceClient(...args),
    Servers: { ...actual.createResourceClient(...args).Servers, clone: Servers.clone },
    withSignal: (signal: AbortSignal) => createResourceClient(args[0], signal),
  }));
  return { ...actual, Servers, createResourceClient };
});

// Mock the router
const mockNavigate = vi.fn();
vi.mock("@tanstack/react-router", () => ({
  useNavigate: () => mockNavigate,
}));

const queryClient = new QueryClient({
  defaultOptions: {
    queries: { retry: false },
    mutations: { retry: false },
  },
});

const Wrapper = ({ children }: { children: ReactNode }) => (
  <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>
);

describe("CloneServerDialog", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mockNavigate.mockClear();
    queryClient.clear();
  });

  it("renders when open", () => {
    render(
      <CloneServerDialog
        open
        onOpenChange={() => {}}
        sourceName="minecraft-server"
      />,
      { wrapper: Wrapper },
    );
    expect(screen.getByRole("heading", { name: "Clone server" })).toBeInTheDocument();
    expect(
      screen.getByText(
        "Creates a new server with the same configuration. World data is not copied.",
      ),
    ).toBeInTheDocument();
  });

  it("does not render when closed", () => {
    render(
      <CloneServerDialog
        open={false}
        onOpenChange={() => {}}
        sourceName="minecraft-server"
      />,
      { wrapper: Wrapper },
    );
    expect(screen.queryByText("Clone server")).not.toBeInTheDocument();
  });

  it("populates new name input with source name + '-copy' when opening", () => {
    render(
      <CloneServerDialog
        open
        onOpenChange={() => {}}
        sourceName="test-server"
      />,
      { wrapper: Wrapper },
    );
    const input = screen.getByDisplayValue("test-server-copy");
    expect(input).toBeInTheDocument();
  });

  it("truncates long source names to 58 chars when creating suggested name", () => {
    const longName = "a".repeat(70);
    render(
      <CloneServerDialog
        open
        onOpenChange={() => {}}
        sourceName={longName}
      />,
      { wrapper: Wrapper },
    );
    const input = screen.getByDisplayValue("a".repeat(58) + "-copy");
    expect(input).toBeInTheDocument();
  });

  it("updates input value when user types", async () => {
    const user = userEvent.setup();
    render(
      <CloneServerDialog
        open
        onOpenChange={() => {}}
        sourceName="source"
      />,
      { wrapper: Wrapper },
    );
    const input = screen.getByDisplayValue("source-copy") as HTMLInputElement;
    await user.clear(input);
    await user.type(input, "new-server-name");
    expect(input.value).toBe("new-server-name");
  });

  it("shows validation error for invalid name", async () => {
    const user = userEvent.setup();
    render(
      <CloneServerDialog
        open
        onOpenChange={() => {}}
        sourceName="source"
      />,
      { wrapper: Wrapper },
    );
    const input = screen.getByDisplayValue("source-copy") as HTMLInputElement;
    await user.clear(input);
    await user.type(input, "INVALID_NAME");
    expect(
      screen.getByText("Name must be lowercase letters, digits, dashes (max 63)"),
    ).toBeInTheDocument();
  });

  it("disables clone button when name is invalid", async () => {
    const user = userEvent.setup();
    render(
      <CloneServerDialog
        open
        onOpenChange={() => {}}
        sourceName="source"
      />,
      { wrapper: Wrapper },
    );
    const input = screen.getByDisplayValue("source-copy") as HTMLInputElement;
    await user.clear(input);
    await user.type(input, "INVALID");
    const cloneBtn = screen.getByRole("button", { name: "Clone server" });
    expect(cloneBtn).toBeDisabled();
  });

  it("enables clone button when name is valid", () => {
    render(
      <CloneServerDialog
        open
        onOpenChange={() => {}}
        sourceName="source"
      />,
      { wrapper: Wrapper },
    );
    const input = screen.getByDisplayValue("source-copy") as HTMLInputElement;
    expect(input).toBeInTheDocument();
    const cloneBtn = screen.getByRole("button", { name: "Clone server" });
    expect(cloneBtn).not.toBeDisabled();
  });

  it("calls onOpenChange(false) when Cancel is clicked", async () => {
    const user = userEvent.setup();
    const onOpenChange = vi.fn();
    render(
      <CloneServerDialog
        open
        onOpenChange={onOpenChange}
        sourceName="source"
      />,
      { wrapper: Wrapper },
    );
    const cancelBtn = screen.getByRole("button", { name: "Cancel" });
    await user.click(cancelBtn);
    expect(onOpenChange).toHaveBeenCalledWith(false);
  });

  it("renders input with correct label", () => {
    render(
      <CloneServerDialog
        open
        onOpenChange={() => {}}
        sourceName="test"
      />,
      { wrapper: Wrapper },
    );
    expect(screen.getByText("New name")).toBeInTheDocument();
  });

  it("accepts namespace prop", () => {
    render(
      <CloneServerDialog
        open
        onOpenChange={() => {}}
        sourceName="test"
        ns="custom-namespace"
      />,
      { wrapper: Wrapper },
    );
    expect(screen.getByRole("heading", { name: "Clone server" })).toBeInTheDocument();
  });

  it("navigates to the clone with the source's namespace after a successful clone", async () => {
    const { Servers } = await import("@/lib/endpoints");
    vi.mocked(Servers.clone).mockResolvedValue({
      metadata: { name: "source-copy", namespace: "team-a" },
      spec: { templateRef: { name: "minecraft-vanilla" } },
      status: {},
    } as never);

    const user = userEvent.setup();
    render(
      <CloneServerDialog
        open
        onOpenChange={() => {}}
        sourceName="source"
        ns="team-a"
      />,
      { wrapper: Wrapper },
    );

    await user.click(screen.getByRole("button", { name: "Clone server" }));

    expect(mockNavigate).toHaveBeenCalledWith({
      to: "/servers/$name",
      params: { name: "source-copy" },
      search: { cluster: "local", ns: "team-a" },
    });
  });

  it("keeps the dialog open and shows the conflict message when the name already exists", async () => {
    const { Servers } = await import("@/lib/endpoints");
    vi.mocked(Servers.clone).mockRejectedValue(new APIError(409, "conflict"));
    const onOpenChange = vi.fn();
    const user = userEvent.setup();
    render(
      <CloneServerDialog
        open
        onOpenChange={onOpenChange}
        sourceName="source"
      />,
      { wrapper: Wrapper },
    );

    await user.click(screen.getByRole("button", { name: "Clone server" }));

    expect(await screen.findByText("A server named source-copy already exists.")).toBeInTheDocument();
    expect(onOpenChange).not.toHaveBeenCalled();
    expect(mockNavigate).not.toHaveBeenCalled();
  });

  it.each([
    { status: 403, body: "", message: "Your role does not allow cloning servers." },
    { status: 500, body: "", message: "Clone failed (500)." },
    { status: 500, body: "disk full", message: "disk full" },
  ])("shows '$message' when the API answers $status", async ({ status, body, message }) => {
    const { Servers } = await import("@/lib/endpoints");
    vi.mocked(Servers.clone).mockRejectedValue(new APIError(status, body));
    const user = userEvent.setup();
    render(
      <CloneServerDialog
        open
        onOpenChange={() => {}}
        sourceName="source"
      />,
      { wrapper: Wrapper },
    );

    await user.click(screen.getByRole("button", { name: "Clone server" }));

    expect(await screen.findByText(message)).toBeInTheDocument();
    expect(mockNavigate).not.toHaveBeenCalled();
  });

  it("shows the error text when the request fails without an API response", async () => {
    const { Servers } = await import("@/lib/endpoints");
    vi.mocked(Servers.clone).mockRejectedValue(new Error("network down"));
    const user = userEvent.setup();
    render(
      <CloneServerDialog
        open
        onOpenChange={() => {}}
        sourceName="source"
      />,
      { wrapper: Wrapper },
    );

    await user.click(screen.getByRole("button", { name: "Clone server" }));

    expect(await screen.findByText("network down")).toBeInTheDocument();
    expect(mockNavigate).not.toHaveBeenCalled();
  });

  it("discards an edited name when the dialog is cancelled and reopened", async () => {
    const user = userEvent.setup();
    const { rerender } = render(
      <CloneServerDialog
        open
        onOpenChange={() => {}}
        sourceName="source"
      />,
      { wrapper: Wrapper },
    );
    const input = screen.getByDisplayValue("source-copy") as HTMLInputElement;
    await user.clear(input);
    await user.type(input, "edited-name");
    expect(input.value).toBe("edited-name");

    rerender(
      <CloneServerDialog
        open={false}
        onOpenChange={() => {}}
        sourceName="source"
      />,
    );
    rerender(
      <CloneServerDialog
        open
        onOpenChange={() => {}}
        sourceName="source"
      />,
    );

    expect(await screen.findByDisplayValue("source-copy")).toBeInTheDocument();
    expect(screen.queryByDisplayValue("edited-name")).not.toBeInTheDocument();
  });
});
