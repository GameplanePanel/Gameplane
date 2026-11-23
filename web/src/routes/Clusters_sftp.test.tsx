import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { http, HttpResponse } from "msw";
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { server } from "@/test/server";
import { renderWithQuery } from "@/test/render";
import { makeUser } from "@/test/factories";
import { setCurrentCluster } from "@/lib/cluster";

const navigate = vi.hoisted(() => vi.fn());
vi.mock("@tanstack/react-router", () => ({ useNavigate: () => navigate }));
import { ClustersPage } from "./Clusters";

// 43 base64 characters after SHA256:, the shape the API accepts (sftpFingerprintPattern).
const FINGERPRINT = `SHA256:${"A".repeat(43)}`;
const TOKEN = "gpsftps_7Kq2mX9vT4wR1sL8pN3dB6hY0cF5jA";

const registrations = [
  { name: "local", displayName: "Central", phase: "Healthy", canViewInventory: true },
  { name: "remote-lab", displayName: "remote-lab", phase: "Healthy", canViewInventory: false },
];

const tokenA = { id: 12, cluster: "remote-lab", prefix: "gpsftps_ab12…", createdAt: "2026-10-03T10:00:00Z", lastUsedAt: null, revokedAt: null };
const tokenB = { id: 13, cluster: "remote-lab", prefix: "gpsftps_cd34…", createdAt: "2026-11-21T10:00:00Z", lastUsedAt: null, revokedAt: null };

function remoteView(tokens: unknown[] = [tokenA]) {
  return { cluster: "remote-lab", local: false, address: "sftp.lab.example:2222", hostKeyFingerprint: FINGERPRINT, tokens };
}

async function openRemoteDrawer() {
  await userEvent.click(await screen.findByRole("button", { name: "SFTP for remote-lab" }));
}

describe("ClustersPage SFTP drawer", () => {
  beforeEach(() => {
    setCurrentCluster("local");
    navigate.mockReset();
    server.use(
      http.get("/users/me", () => HttpResponse.json(makeUser({ role: "admin" }))),
      http.get("/clusters", () => HttpResponse.json({ items: registrations })),
    );
  });
  afterEach(() => setCurrentCluster("local"));

  it("shows the endpoint, the active token count and token labels for a remote cluster", async () => {
    server.use(http.get("/clusters/remote-lab/sftp", () => HttpResponse.json(remoteView([tokenA, tokenB]))));
    renderWithQuery(<ClustersPage />);
    await openRemoteDrawer();
    expect(await screen.findByRole("heading", { name: "SFTP · remote-lab" })).toBeInTheDocument();
    expect(screen.getByLabelText("Address")).toHaveValue("sftp.lab.example:2222");
    expect(screen.getByLabelText(/Host key fingerprint/)).toHaveValue(FINGERPRINT);
    expect(screen.getByText("Endpoint tokens · 2 of 2 in use")).toBeInTheDocument();
    expect(screen.getByText("gpsftps_ab12…")).toBeInTheDocument();
    expect(screen.getByText("gpsftps_cd34…")).toBeInTheDocument();
  });

  it("saves the endpoint with the trimmed address and fingerprint", async () => {
    let body: unknown = null;
    server.use(
      http.get("/clusters/remote-lab/sftp", () => HttpResponse.json(remoteView())),
      http.put("/clusters/remote-lab/sftp", async ({ request }) => {
        body = await request.json();
        return HttpResponse.json({ cluster: "remote-lab", address: "edge.lab.example", hostKeyFingerprint: FINGERPRINT, updatedAt: "2026-11-23T10:00:00Z" });
      }),
    );
    renderWithQuery(<ClustersPage />);
    await openRemoteDrawer();
    const address = await screen.findByLabelText("Address");
    const save = screen.getByRole("button", { name: "Save endpoint" });
    expect(save).toBeEnabled();
    await userEvent.clear(address);
    await userEvent.type(address, "  edge.lab.example  ");
    await userEvent.click(save);
    await waitFor(() => expect(body).toEqual({ address: "edge.lab.example", hostKeyFingerprint: FINGERPRINT }));
  });

  it("keeps Save endpoint disabled until the fingerprint has the SHA256 form", async () => {
    server.use(http.get("/clusters/remote-lab/sftp", () => HttpResponse.json(remoteView())));
    renderWithQuery(<ClustersPage />);
    await openRemoteDrawer();
    const fingerprint = await screen.findByLabelText(/Host key fingerprint/);
    await userEvent.clear(fingerprint);
    await userEvent.type(fingerprint, "MD5:abcd");
    expect(screen.getByRole("button", { name: "Save endpoint" })).toBeDisabled();
  });

  it("shows the server message when the endpoint cannot be saved", async () => {
    server.use(
      http.get("/clusters/remote-lab/sftp", () => HttpResponse.json(remoteView())),
      http.put("/clusters/remote-lab/sftp", () => HttpResponse.json({ error: "bad" }, { status: 400 })),
    );
    renderWithQuery(<ClustersPage />);
    await openRemoteDrawer();
    await userEvent.click(await screen.findByRole("button", { name: "Save endpoint" }));
    expect(await screen.findByRole("alert")).toHaveTextContent(/./);
  });

  it("shows the token once after creation and then closes it", async () => {
    server.use(
      http.get("/clusters/remote-lab/sftp", () => HttpResponse.json(remoteView())),
      http.post("/clusters/remote-lab/sftp-token", () => HttpResponse.json({ ...tokenB, token: TOKEN }, { status: 201 })),
    );
    renderWithQuery(<ClustersPage />);
    await openRemoteDrawer();
    await userEvent.click(await screen.findByRole("button", { name: "Create token" }));
    expect(await screen.findByRole("heading", { name: "Endpoint token created" })).toBeInTheDocument();
    expect(screen.getByTestId("cluster-sftp-token-value")).toHaveTextContent(TOKEN);
    await userEvent.click(screen.getByRole("button", { name: "Done" }));
    await waitFor(() => expect(screen.queryByRole("heading", { name: "Endpoint token created" })).not.toBeInTheDocument());
  });

  it("disables token creation at the two-token limit and explains how to free a slot", async () => {
    server.use(http.get("/clusters/remote-lab/sftp", () => HttpResponse.json(remoteView([tokenA, tokenB]))));
    renderWithQuery(<ClustersPage />);
    await openRemoteDrawer();
    expect(await screen.findByRole("button", { name: "Create token" })).toBeDisabled();
    const alert = await screen.findByRole("alert");
    expect(within(alert).getByText(/Revoke one token before you can create another/)).toBeInTheDocument();
  });

  it("revokes one token and refreshes the list", async () => {
    let revoked = false;
    server.use(
      http.get("/clusters/remote-lab/sftp", () => HttpResponse.json(remoteView(revoked ? [] : [tokenA]))),
      http.delete("/clusters/remote-lab/sftp-token/12", () => {
        revoked = true;
        return new HttpResponse(null, { status: 204 });
      }),
    );
    renderWithQuery(<ClustersPage />);
    await openRemoteDrawer();
    await userEvent.click(await screen.findByRole("button", { name: "Revoke" }));
    expect(await screen.findByText("No active tokens.")).toBeInTheDocument();
    expect(screen.getByText("Endpoint tokens · 0 of 2 in use")).toBeInTheDocument();
  });

  it("shows the panel's own cluster endpoint read-only with no save action", async () => {
    server.use(
      http.get("/clusters/local/sftp", () =>
        HttpResponse.json({ cluster: "local", local: true, address: "sftp.home.example:2222", hostKeyFingerprint: FINGERPRINT, tokens: [] }),
      ),
    );
    renderWithQuery(<ClustersPage />);
    await userEvent.click(await screen.findByRole("button", { name: "SFTP for Central" }));
    expect(await screen.findByText("sftp.home.example:2222")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Save endpoint" })).not.toBeInTheDocument();
    expect(screen.queryByLabelText("Address")).not.toBeInTheDocument();
  });

  it("does not offer the SFTP drawer to users without cluster management", async () => {
    server.use(http.get("/users/me", () => HttpResponse.json(makeUser({ role: "user", permissions: {} }))));
    renderWithQuery(<ClustersPage />);
    expect(await screen.findByRole("heading", { name: "remote-lab" })).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "SFTP for remote-lab" })).not.toBeInTheDocument();
  });
});
