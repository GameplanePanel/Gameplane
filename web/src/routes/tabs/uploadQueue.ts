import { APIError } from "@/lib/api";
import { UploadConnectionLostError } from "@/lib/endpoints";

export type UploadStatus = "waiting" | "uploading" | "done" | "failed" | "cancelled";

export interface UploadItem {
  id: number;
  file: File;
  /** Folder the file goes into; captured when the file is picked. */
  dir: string;
  status: UploadStatus;
  loaded: number;
  error?: unknown;
}

export type UploadErrorKind = "connection" | "too_large" | "no_space" | "other";

/** Maps a failed streaming upload to the error text the tray shows (FR-016). */
export function classifyUploadError(err: unknown): UploadErrorKind {
  if (err instanceof UploadConnectionLostError) return "connection";
  if (err instanceof APIError) {
    if (err.status === 413) return "too_large";
    if (err.status === 507) return "no_space";
  }
  return "other";
}

/** English text the API sends with server.files.starting to a caller that did not ask for problem+json. */
const FILES_STARTING_PLAIN_TEXT = "files are starting, try again in a moment";

/**
 * True when the list request answered 503 because the file-only pod was not ready in time
 * (server.files.starting). Only this answer starts the files-starting state.
 */
export function isFilesStartingError(err: unknown): boolean {
  if (!(err instanceof APIError) || err.status !== 503) return false;
  return err.messageId === "server.files.starting" || err.body.trim() === FILES_STARTING_PLAIN_TEXT;
}

/** The upload limit in bytes from a 413 answer whose body names it ("... N byte limit"), else null. */
export function uploadLimitBytes(err: unknown): number | null {
  if (!(err instanceof APIError)) return null;
  const match = /(\d+)\s*byte limit/.exec(err.body);
  if (!match) return null;
  const bytes = Number(match[1]);
  return Number.isSafeInteger(bytes) && bytes > 0 ? bytes : null;
}

const IEC_UNITS = ["B", "KiB", "MiB", "GiB", "TiB"];

/** Formats a byte count in binary units, keeping the largest unit that divides it exactly (20 GiB, 1536 MiB). */
export function formatUploadLimit(bytes: number): string {
  let value = bytes;
  let unit = 0;
  while (unit < IEC_UNITS.length - 1 && value >= 1024 && value % 1024 === 0) {
    value /= 1024;
    unit += 1;
  }
  return `${value} ${IEC_UNITS[unit]}`;
}

/** Whole percent of loaded over total; an empty file counts as complete. */
export function uploadPercent(loaded: number, total: number): number {
  if (total <= 0) return 100;
  return Math.min(100, Math.round((loaded / total) * 100));
}
