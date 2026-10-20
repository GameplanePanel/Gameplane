import { describe, expect, it } from "vitest";
import { verifyForEntry, verifyMode } from "./verify";
import type { ModuleSource } from "@/types";
import { makeCatalog, makeModuleSource } from "@/test/factories";

function ociSource(name: string, verify?: ModuleSource["spec"]["verify"]): ModuleSource {
  return makeModuleSource({
    metadata: { name },
    spec: {
      type: "oci",
      oci: { url: `ghcr.io/${name}`, modules: [{ name: "minecraft-vanilla" }] },
      verify,
    },
  });
}

const keylessSrc = ociSource("upstream", {
  keyless: { issuer: "https://token.actions.githubusercontent.com", identity: "https://github.com/GameplanePanel/module/.github/workflows/release.yml@refs/heads/main" },
});
const keyedSrc = ociSource("keyed", { key: { name: "cosign-pub" } });
const plainSrc = ociSource("mirror");

describe("verifyMode", () => {
  it("prefers keyless, falls back to keyed, else none", () => {
    expect(verifyMode({ keyless: { issuer: "i", identity: "d" } })).toBe("keyless");
    expect(verifyMode({ key: { name: "k" } })).toBe("keyed");
    expect(verifyMode({ key: { name: "k" }, keyless: { issuer: "i", identity: "d" } })).toBe("keyless");
    expect(verifyMode(undefined)).toBe("none");
    expect(verifyMode({})).toBe("none");
  });
});

describe("verifyForEntry", () => {
  it("reports the policy of the source an installed module was pulled from, enforced once a verification is recorded", () => {
    const entry = makeCatalog({
      installed: true,
      installedFrom: "upstream",
      sources: [{ name: "upstream", type: "oci" }],
      verifiedDigest: "sha256:abc",
      verifyPolicy: "keyless",
    });
    expect(verifyForEntry(entry, [keylessSrc, plainSrc])).toEqual({ mode: "keyless", enforced: true, mixed: false });
  });

  it("uses the recorded verifyPolicy, not the source's current policy, once a verification is enforced", () => {
    // The module was verified under the keyed policy in effect at install
    // time; the source has since switched to keyless. The badge must still
    // describe what was actually checked (keyed), not the live policy.
    const entry = makeCatalog({
      installed: true,
      installedFrom: "upstream",
      sources: [{ name: "upstream", type: "oci" }],
      verifiedDigest: "sha256:abc",
      verifyPolicy: "keyed",
    });
    expect(verifyForEntry(entry, [keylessSrc, plainSrc])).toEqual({ mode: "keyed", enforced: true, mixed: false });
  });

  it("does NOT claim enforced for an installed module with no recorded verification, even though its source now declares a policy", () => {
    // The badge must not over-claim: a policy added to the source AFTER
    // this module was installed doesn't retroactively mean the running
    // bytes were checked. The declared policy still surfaces as `mode`, so
    // the UI can show the softer "policy" chip instead of nothing.
    const entry = makeCatalog({
      installed: true,
      installedFrom: "upstream",
      sources: [{ name: "upstream", type: "oci" }],
    });
    expect(verifyForEntry(entry, [keylessSrc, plainSrc])).toEqual({ mode: "keyless", enforced: false, mixed: false });
  });

  it("does NOT claim enforced when installed from an unverified source, even if a sibling source verifies", () => {
    // The running bytes came from `mirror` (no verify); `upstream` verifying
    // the same module must not produce a misleading "signed" badge.
    const entry = makeCatalog({
      installed: true,
      installedFrom: "mirror",
      sources: [{ name: "upstream", type: "oci" }, { name: "mirror", type: "oci" }],
    });
    expect(verifyForEntry(entry, [keylessSrc, plainSrc])).toEqual({ mode: "none", enforced: false, mixed: false });
  });

  it("treats a missing installedFrom source as unverified", () => {
    const entry = makeCatalog({ installed: true, installedFrom: "gone", sources: [] });
    expect(verifyForEntry(entry, [keylessSrc])).toEqual({ mode: "none", enforced: false, mixed: false });
  });

  it("summarises a not-installed single keyed source without enforcing", () => {
    const entry = makeCatalog({ installed: false, sources: [{ name: "keyed", type: "oci" }] });
    expect(verifyForEntry(entry, [keyedSrc])).toEqual({ mode: "keyed", enforced: false, mixed: false });
  });

  it("flags mixed policy across candidate sources of a not-installed entry", () => {
    const entry = makeCatalog({
      installed: false,
      sources: [{ name: "upstream", type: "oci" }, { name: "mirror", type: "oci" }],
    });
    expect(verifyForEntry(entry, [keylessSrc, plainSrc])).toEqual({ mode: "keyless", enforced: false, mixed: true });
  });

  it("is none when no candidate source verifies", () => {
    const entry = makeCatalog({ installed: false, sources: [{ name: "mirror", type: "oci" }] });
    expect(verifyForEntry(entry, [plainSrc])).toEqual({ mode: "none", enforced: false, mixed: false });
  });

  it("tolerates an entry with no sources array", () => {
    const entry = makeCatalog({ installed: false });
    // @ts-expect-error — exercise the runtime guard for a null sources field
    entry.sources = undefined;
    expect(verifyForEntry(entry, [keylessSrc])).toEqual({ mode: "none", enforced: false, mixed: false });
  });
});
