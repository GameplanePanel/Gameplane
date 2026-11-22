import { describe, expect, it } from "vitest";
import type { AuthCfg, AuthProvider, OIDCHelmProvider } from "@/lib/config";
import { addedOverrideAdminGroups, normalizeAuthForSave } from "./RoleRulesEditor";

const corp: AuthProvider = {
  name: "corp",
  kind: "oidc",
  enabled: true,
  issuer: "https://idp.example.com",
  clientID: "gameplane",
};

const helm: OIDCHelmProvider = {
  groupsClaim: "groups",
  defaultRole: "deny",
  roleMappings: { admin: ["ops"] },
};

describe("normalizeAuthForSave", () => {
  it("rewrites an old-form provider that maps groups as roleRules", () => {
    const cfg: AuthCfg = { providers: [{ ...corp, roleMappings: { admin: ["ops"], viewer: ["all"] } }] };
    const out = normalizeAuthForSave(cfg);
    expect(out.providers[0].roleMappings).toBeUndefined();
    expect(out.providers[0].roleRules).toEqual([
      { group: "ops", role: "admin" },
      { group: "all", role: "viewer" },
    ]);
  });

  it("keeps an old-form provider with no groups and a deny fallback in the old form", () => {
    // roleMappings: {} keeps mapping on, so deny still refuses unmatched
    // users; roleRules: [] would turn mapping off and admit them as viewers.
    const provider: AuthProvider = { ...corp, roleMappings: {}, defaultRole: "deny" };
    const out = normalizeAuthForSave({ providers: [provider] });
    expect(out.providers[0]).toEqual(provider);
  });

  it("keeps an old-form Helm override that clears the last mapped group in the old form", () => {
    const cfg: AuthCfg = { providers: [], helmOverride: { roleMappings: { admin: [] } } };
    const out = normalizeAuthForSave(cfg, helm);
    expect(out.helmOverride).toEqual({ roleMappings: { admin: [] } });
  });

  it("rewrites an old-form Helm override that leaves rules as roleRules", () => {
    const cfg: AuthCfg = { providers: [], helmOverride: { roleMappings: { operator: ["devs"] } } };
    const out = normalizeAuthForSave(cfg, helm);
    expect(out.helmOverride).toEqual({
      roleRules: [
        { group: "ops", role: "admin" },
        { group: "devs", role: "operator" },
      ],
    });
  });
});

describe("addedOverrideAdminGroups", () => {
  it("reports a group whose first matching rule becomes admin after a reorder", () => {
    // Stored: "ops" first matches viewer; the admin rule for it is shadowed.
    const stored: AuthCfg = {
      providers: [],
      helmOverride: {
        roleRules: [
          { group: "ops", role: "viewer" },
          { group: "ops", role: "admin" },
        ],
      },
    };
    // Draft: the same two rules, with the admin rule moved first.
    const draft: AuthCfg = {
      providers: [],
      helmOverride: {
        roleRules: [
          { group: "ops", role: "admin" },
          { group: "ops", role: "viewer" },
        ],
      },
    };
    expect(addedOverrideAdminGroups(draft, stored, helm)).toEqual(["ops"]);
  });

  it("ignores a shadowed admin rule that grants nothing", () => {
    const stored: AuthCfg = { providers: [], helmOverride: { roleRules: [{ group: "ops", role: "viewer" }] } };
    const draft: AuthCfg = {
      providers: [],
      helmOverride: {
        roleRules: [
          { group: "ops", role: "viewer" },
          { group: "ops", role: "admin" },
        ],
      },
    };
    expect(addedOverrideAdminGroups(draft, stored, helm)).toEqual([]);
  });

  it("does not report a group that was already admin", () => {
    const stored: AuthCfg = { providers: [] };
    const draft: AuthCfg = {
      providers: [],
      helmOverride: {
        roleRules: [
          { group: "ops", role: "admin" },
          { group: "new-admins", role: "admin" },
        ],
      },
    };
    expect(addedOverrideAdminGroups(draft, stored, helm)).toEqual(["new-admins"]);
  });
});
