import { describe, expect, it } from "vitest";
import { APIError } from "@/lib/api";
import { formatUploadLimit, isFilesStartingError, uploadLimitBytes } from "./uploadQueue";

const problem = (body: unknown) => JSON.stringify(body);

describe("isFilesStartingError", () => {
  it("matches the 503 problem document carrying server.files.starting", () => {
    const err = new APIError(503, problem({ status: 503, detail: "files are starting, try again in a moment", messageId: "server.files.starting" }), undefined, "application/problem+json");
    expect(isFilesStartingError(err)).toBe(true);
  });

  it("matches the plain-text 503 the API sends without problem+json", () => {
    expect(isFilesStartingError(new APIError(503, "files are starting, try again in a moment\n", undefined, "text/plain"))).toBe(true);
  });

  it("does not match other 503 answers, other statuses or other errors", () => {
    expect(isFilesStartingError(new APIError(503, "agent unavailable"))).toBe(false);
    expect(isFilesStartingError(new APIError(500, "files are starting, try again in a moment"))).toBe(false);
    expect(isFilesStartingError(new Error("files are starting"))).toBe(false);
    expect(isFilesStartingError(undefined)).toBe(false);
  });
});

describe("uploadLimitBytes", () => {
  it("reads the byte limit named in the 413 detail", () => {
    const err = new APIError(413, problem({ status: 413, detail: "request body exceeds the 21474836480 byte limit" }), undefined, "application/problem+json");
    expect(uploadLimitBytes(err)).toBe(21474836480);
  });

  it("returns null when the body names no limit", () => {
    expect(uploadLimitBytes(new APIError(413, "too large"))).toBeNull();
    expect(uploadLimitBytes(new Error("request body exceeds the 5 byte limit"))).toBeNull();
  });
});

describe("formatUploadLimit", () => {
  it("keeps the largest binary unit that divides the limit", () => {
    expect(formatUploadLimit(20 * 1024 ** 3)).toBe("20 GiB");
    expect(formatUploadLimit(1536 * 1024 ** 2)).toBe("1536 MiB");
    expect(formatUploadLimit(512)).toBe("512 B");
  });
});
