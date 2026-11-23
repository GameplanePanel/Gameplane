import { describe, expect, it } from "vitest";
import {
  CONFIG_REDACTED_MARKER,
  defaultVersionId,
  isValidK8sName,
  isValidQuantity,
  isValidVersion,
  maskPasswordConfig,
  PASSWORD_MASK,
  validateConfig,
  type ConfigField,
} from "./validation";
import type { GameTemplate } from "@/types";

function tmplWithVersions(versions?: GameTemplate["spec"]["versions"]): GameTemplate {
  return {
    metadata: { name: "minecraft-java" },
    spec: {
      displayName: "Minecraft",
      game: "minecraft-java",
      version: "2.0.0",
      image: "itzg/minecraft-server:java21",
      versions,
    },
  };
}

describe("isValidK8sName", () => {
  it.each([
    ["mc", true],
    ["mc-hardcore", true],
    ["x".repeat(63), true],
    ["a1b2c3", true],
  ])("accepts %s", (s, expected) => {
    expect(isValidK8sName(s)).toBe(expected);
  });

  it.each([
    "",
    "MC",
    "mc test",
    "-mc",
    "mc-",
    "mc_test",
    "x".repeat(64),
    "1.2.3",
  ])("rejects %s", (s) => {
    expect(isValidK8sName(s)).toBe(false);
  });
});

describe("isValidQuantity", () => {
  it.each(["50Gi", "1Ti", "100Mi", "500m", "2", "1.5Gi"])("accepts %s", (s) => {
    expect(isValidQuantity(s)).toBe(true);
  });

  it.each(["", "50G ", " 50Gi", "50 Gi", "50gi", "Gi", "abc", "-5Gi"])(
    "rejects %s",
    (s) => {
      expect(isValidQuantity(s)).toBe(false);
    },
  );

  it("accepts plain integers (e.g. CPU count or millicores)", () => {
    expect(isValidQuantity("2")).toBe(true);
    expect(isValidQuantity("500m")).toBe(true);
  });
});

describe("maskPasswordConfig", () => {
  const schema: ConfigField[] = [
    { name: "MOTD", type: "string" },
    { name: "ADMIN_PASSWORD", type: "password" },
    { name: "RCON_PASSWORD", type: "password" },
  ];

  it("masks non-empty password values and leaves the rest alone", () => {
    expect(
      maskPasswordConfig(schema, { MOTD: "hello", ADMIN_PASSWORD: "hunter2", RCON_PASSWORD: "" }),
    ).toEqual({ MOTD: "hello", ADMIN_PASSWORD: PASSWORD_MASK, RCON_PASSWORD: "" });
  });

  it("masks the API redaction marker too", () => {
    expect(maskPasswordConfig(schema, { ADMIN_PASSWORD: "__gameplane_redacted__" })).toEqual({
      ADMIN_PASSWORD: PASSWORD_MASK,
    });
  });

  it("does not mutate its input and ignores values not in the schema", () => {
    const input = { ADMIN_PASSWORD: "hunter2", EXTRA: "x" };
    expect(maskPasswordConfig(schema, input)).toEqual({ ADMIN_PASSWORD: PASSWORD_MASK, EXTRA: "x" });
    expect(input.ADMIN_PASSWORD).toBe("hunter2");
  });
});

describe("validateConfig", () => {
  const schema: ConfigField[] = [
    { name: "TYPE", displayName: "Server type", type: "enum", enum: ["VANILLA", "PAPER"], required: true, default: "VANILLA" },
    { name: "VERSION", type: "string", required: true },
    { name: "DIFFICULTY", type: "enum", enum: ["peaceful", "easy", "normal", "hard"] },
    { name: "MOTD", type: "string" },
  ];

  it("returns no errors when required fields have values", () => {
    expect(validateConfig(schema, { VERSION: "1.21" })).toEqual([]);
  });

  it("flags missing required fields", () => {
    const errs = validateConfig(schema, {});
    expect(errs.map((e) => e.name)).toEqual(["VERSION"]);
  });

  it("flags enum mismatch", () => {
    const errs = validateConfig(schema, { VERSION: "1.21", DIFFICULTY: "extreme" });
    expect(errs).toHaveLength(1);
    expect(errs[0].name).toBe("DIFFICULTY");
    expect(errs[0].message).toContain("peaceful");
  });

  it("treats default as a satisfying value for required enum fields", () => {
    expect(validateConfig(schema, { VERSION: "1.21" })).toEqual([]);
  });
});

describe("isValidVersion", () => {
  const versions = [
    { id: "1.21.4-paper", displayName: "1.21.4 Paper", loader: "paper", default: true },
    { id: "1.21.4-forge", displayName: "1.21.4 Forge", loader: "forge" },
  ];

  it("is true when the template declares no versions", () => {
    expect(isValidVersion(tmplWithVersions(undefined), undefined)).toBe(true);
    expect(isValidVersion(tmplWithVersions([]), "anything")).toBe(true);
  });

  it("requires a value when the template declares versions", () => {
    expect(isValidVersion(tmplWithVersions(versions), undefined)).toBe(false);
    expect(isValidVersion(tmplWithVersions(versions), "")).toBe(false);
  });

  it("accepts a matching id and rejects an unknown one", () => {
    expect(isValidVersion(tmplWithVersions(versions), "1.21.4-forge")).toBe(true);
    expect(isValidVersion(tmplWithVersions(versions), "9.9-bogus")).toBe(false);
  });
});

describe("defaultVersionId", () => {
  it("returns undefined without a catalog", () => {
    expect(defaultVersionId(tmplWithVersions(undefined))).toBeUndefined();
    expect(defaultVersionId(tmplWithVersions([]))).toBeUndefined();
  });

  it("prefers the entry marked default", () => {
    expect(
      defaultVersionId(
        tmplWithVersions([
          { id: "a", displayName: "A" },
          { id: "b", displayName: "B", default: true },
        ]),
      ),
    ).toBe("b");
  });

  it("falls back to the first entry when none is marked default", () => {
    expect(
      defaultVersionId(
        tmplWithVersions([
          { id: "a", displayName: "A" },
          { id: "b", displayName: "B" },
        ]),
      ),
    ).toBe("a");
  });
});

describe("validateConfig value checks", () => {
  const schema: ConfigField[] = [
    { name: "MAX_PLAYERS", displayName: "Max players", type: "int", min: 1, max: 255 },
    { name: "MIN_ONLY", type: "int", min: 5 },
    { name: "MAX_ONLY", type: "int", max: 9 },
    { name: "FREE_INT", type: "int" },
    { name: "PVP", type: "bool" },
    { name: "MOTD", type: "string", minLength: 2, maxLength: 5 },
    { name: "PASS", type: "password", required: true, minLength: 8 },
    { name: "MODE", type: "enum", enum: ["a", "b"] },
  ];
  const ok = { PASS: CONFIG_REDACTED_MARKER };

  it("accepts values inside every bound", () => {
    expect(
      validateConfig(schema, { ...ok, MAX_PLAYERS: "8", MIN_ONLY: "5", MAX_ONLY: "9", FREE_INT: "-3", PVP: "true", MOTD: "hey", MODE: "a" }),
    ).toEqual([]);
  });

  it("rejects non-integers", () => {
    const errs = validateConfig(schema, { ...ok, MAX_PLAYERS: "8.5" });
    expect(errs).toHaveLength(1);
    expect(errs[0].text).toBe("Must be a whole number.");
    expect(errs[0].message).toBe("Max players: Must be a whole number.");
    expect(validateConfig(schema, { ...ok, FREE_INT: "abc" })[0].text).toBe("Must be a whole number.");
  });

  it("reports the range with the design wording", () => {
    expect(validateConfig(schema, { ...ok, MAX_PLAYERS: "900" })[0].text).toBe("Must be between 1 and 255.");
    expect(validateConfig(schema, { ...ok, MAX_PLAYERS: "0" })[0].text).toBe("Must be between 1 and 255.");
  });

  it("reports one-sided bounds", () => {
    expect(validateConfig(schema, { ...ok, MIN_ONLY: "4" })[0].text).toBe("Must be at least 5.");
    expect(validateConfig(schema, { ...ok, MAX_ONLY: "10" })[0].text).toBe("Must be at most 9.");
  });

  it("checks booleans like the operator (Go ParseBool spellings)", () => {
    expect(validateConfig(schema, { ...ok, PVP: "maybe" })[0].text).toBe("Must be true or false.");
    expect(validateConfig(schema, { ...ok, PVP: "1" })).toEqual([]);
    expect(validateConfig(schema, { ...ok, PVP: "FALSE" })).toEqual([]);
  });

  it("checks string length in bytes", () => {
    expect(validateConfig(schema, { ...ok, MOTD: "a" })[0].text).toBe("Must be at least 2 characters.");
    expect(validateConfig(schema, { ...ok, MOTD: "abcdef" })[0].text).toBe("Must be at most 5 characters.");
    // two 2-byte characters + 2 ASCII = 6 bytes > 5
    expect(validateConfig(schema, { ...ok, MOTD: "ééab" })[0].text).toBe("Must be at most 5 characters.");
  });

  it("checks password length but treats the redaction marker as unchanged", () => {
    expect(validateConfig(schema, { PASS: "short" })[0].text).toBe("Must be at least 8 characters.");
    expect(validateConfig(schema, { PASS: CONFIG_REDACTED_MARKER })).toEqual([]);
  });

  it("a required password with no value and no stored marker is an error", () => {
    const errs = validateConfig(schema, {});
    expect(errs.map((e) => e.name)).toEqual(["PASS"]);
    expect(errs[0].text).toBe("PASS is required");
  });

  it("skips empty optional values and keeps enum errors", () => {
    expect(validateConfig(schema, { ...ok, MAX_PLAYERS: "" })).toEqual([]);
    const errs = validateConfig(schema, { ...ok, MODE: "z" });
    expect(errs[0].text).toBe("MODE must be one of: a, b");
    expect(errs[0].message).toBe(errs[0].text);
  });
});

describe("validateConfig redaction marker (any field type)", () => {
  const schema: ConfigField[] = [
    { name: "S", displayName: "Str", type: "string", required: true, minLength: 99 },
    { name: "I", type: "int", min: 1, max: 5 },
    { name: "E", type: "enum", enum: ["a", "b"], required: true },
    { name: "B", type: "bool", required: true },
  ];

  it("treats the marker as unchanged for string, int, enum and bool, including required ones", () => {
    const m = CONFIG_REDACTED_MARKER;
    expect(validateConfig(schema, { S: m, I: m, E: m, B: m })).toEqual([]);
  });

  it("still reports a real invalid value next to marker-backed fields", () => {
    const errs = validateConfig(schema, { S: CONFIG_REDACTED_MARKER, I: "9", E: CONFIG_REDACTED_MARKER, B: CONFIG_REDACTED_MARKER });
    expect(errs.map((e) => e.name)).toEqual(["I"]);
  });
});

describe("server names starting with a digit", () => {
  it("rejects a name that starts with a digit", () => {
    expect(isValidK8sName("7d2d")).toBe(false);
  });

  it("accepts the same name starting with a letter", () => {
    expect(isValidK8sName("sdtd")).toBe(true);
  });
});
