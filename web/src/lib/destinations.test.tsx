import { describe, it, expect } from "vitest";
import { http, HttpResponse } from "msw";
import { act, screen } from "@testing-library/react";
import { server } from "@/test/server";
import { renderWithQuery } from "@/test/render";
import { makeDestination } from "@/test/factories";
import { useBackupDestinations } from "./destinations";

// A tiny probe component so the hook runs under the real providers; it
// surfaces the selected length, which exercises the query's `select`.
function Probe({ permissions = ["destinations:read"] }: { permissions?: string[] }) {
  const { data, isLoading } = useBackupDestinations(undefined, permissions);
  if (isLoading) return <div>loading</div>;
  return <div>count:{data?.length ?? -1}</div>;
}

describe("useBackupDestinations", () => {
  it("does not request destinations without explicit or contextual read access", async () => {
    let requests = 0;
    server.use(http.get("/backup-destinations", () => {
      requests++;
      return HttpResponse.json({ items: [makeDestination()] });
    }));
    const { client } = renderWithQuery(<Probe permissions={[]} />);
    await act(() => client.invalidateQueries());
    expect(requests).toBe(0);
    expect(screen.getByText("count:-1")).toBeInTheDocument();
  });

  it("selects the items array from the list response", async () => {
    server.use(
      http.get("/backup-destinations", () =>
        HttpResponse.json({ items: [makeDestination({ name: "a" }), makeDestination({ name: "b" })] }),
      ),
    );
    renderWithQuery(<Probe />);
    expect(await screen.findByText("count:2")).toBeInTheDocument();
  });

  it("yields an empty array when there are no destinations", async () => {
    server.use(http.get("/backup-destinations", () => HttpResponse.json({ items: [] })));
    renderWithQuery(<Probe />);
    expect(await screen.findByText("count:0")).toBeInTheDocument();
  });
});
