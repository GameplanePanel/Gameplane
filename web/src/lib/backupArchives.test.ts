import { describe, expect, it } from "vitest";
import { archiveFormatFromName, canDownloadBackups, canUploadBackups, isDownloadableBackup, isSnapshotBackup, restoreSourceName } from "./backupArchives";
import { makeBackup, makeRestore } from "@/test/factories";

describe("backup archive helpers", () => {
  it("detects the upload format from the file name", () => {
    expect(archiveFormatFromName("world.tar.gz")).toBe("tar.gz");
    expect(archiveFormatFromName("WORLD.TGZ")).toBe("tar.gz");
    expect(archiveFormatFromName("world.zip")).toBe("zip");
    expect(archiveFormatFromName("world.tar")).toBeNull();
    expect(archiveFormatFromName("zip")).toBeNull();
  });

  it("classifies backups", () => {
    expect(isSnapshotBackup(makeBackup({ spec: { serverRef: { name: "a" }, strategy: "volume-snapshot" } }))).toBe(true);
    expect(isSnapshotBackup(makeBackup())).toBe(false);
    expect(isDownloadableBackup(makeBackup())).toBe(true);
    expect(isDownloadableBackup(makeBackup({ status: { phase: "Succeeded" } }))).toBe(false);
    expect(isDownloadableBackup(makeBackup({ status: undefined }))).toBe(false);
    expect(isDownloadableBackup(makeBackup({ spec: { serverRef: { name: "a" }, strategy: "volume-snapshot" } }))).toBe(false);
  });

  it("maps permissions", () => {
    expect(canDownloadBackups(["backups:read"])).toBe(true);
    expect(canDownloadBackups(["backups:restore"])).toBe(false);
    expect(canUploadBackups(["backups:restore"])).toBe(true);
    expect(canUploadBackups(["*"])).toBe(true);
    expect(canUploadBackups(["backups:read"])).toBe(false);
  });

  it("names a restore's source", () => {
    expect(restoreSourceName(makeRestore())).toBe("alpha-2026-05-07");
    expect(restoreSourceName(makeRestore({ spec: { serverRef: { name: "a" }, upload: { fileName: "x.zip", format: "zip", declaredBytes: 1 } } }))).toBe("x.zip");
    expect(restoreSourceName(makeRestore({ spec: { serverRef: { name: "a" } } }))).toBe("");
  });
});
