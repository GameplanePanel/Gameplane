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

/** True when the API could not make the file-only pod ready in time (server.files.starting). */
export function isFilesStartingError(err: unknown): boolean {
  return err instanceof APIError && err.messageId === "server.files.starting";
}

/** Whole percent of loaded over total; an empty file counts as complete. */
export function uploadPercent(loaded: number, total: number): number {
  if (total <= 0) return 100;
  return Math.min(100, Math.round((loaded / total) * 100));
}
