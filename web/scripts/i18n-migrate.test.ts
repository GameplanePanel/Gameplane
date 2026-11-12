import { spawnSync } from "node:child_process";
import { mkdtempSync, mkdirSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { expect, test } from "vitest";

const script = path.join(path.dirname(fileURLToPath(import.meta.url)), "i18n-migrate.ts");

test("migrates TSX without moving Unicode, JSX comments, or entity boundaries", () => {
  const root = mkdtempSync(path.join(tmpdir(), "gameplane-i18n-"));
  try {
    mkdirSync(path.join(root, "src"));
    const file = path.join(root, "src", "Banner.tsx");
    const report = path.join(root, "report.json");
    const source = [
      'import { memo } from "react";',
      "export const Banner = memo(({ name }: { name: string }) => (",
      '  <section title="Hi &amp; welcome">👋 Hello {name}!{/* keep me */}',
      '    <br />{" "}Finish now',
      "  </section>",
      "));",
      'import defer * as ns from "./decorators";',
      "export @ns.dec class Model { accessor value = 1; }",
    ].join("\r\n");
    writeFileSync(file, source);
    const run = (...args: string[]) => spawnSync(process.execPath, [script, ...args, file], { cwd: root, encoding: "utf8" });

    const check = run("--check");
    expect(check.status).toBe(1);
    expect(check.stdout).toContain("src/Banner.tsx:3:18 formatjs/no-literal-string-in-jsx");
    const preview = run("--dry-run", "--report", report);
    expect(preview.status).toBe(0);
    expect(preview.stdout).toContain("--- a/src/Banner.tsx");
    expect(JSON.parse(readFileSync(report, "utf8")).totals).toMatchObject({ files: 1, changedFiles: 1, migrated: 3 });
    expect(readFileSync(file, "utf8")).toBe(source);

    expect(run().status).toBe(0);
    const output = readFileSync(file, "utf8");
    expect(output).toContain("const intl = useOptionalIntl();");
    expect(output).toContain('defaultMessage: "Hi & welcome"');
    expect(output).toContain('defaultMessage: "👋 Hello {name}!"');
    expect(output).toContain("{/* keep me */}");
    expect(output).toContain("<br /> {intl.formatMessage(");
    expect(run("--check").status).toBe(0);
    expect(run().stdout).toContain("migrated 0 site(s) in 0 file(s)");
    expect(readFileSync(file, "utf8")).toBe(output);

    // The entity renders even though Babel decodes it to a whitespace character.
    // Joining its adjacent space must leave the original JSX entity spelling intact.
    writeFileSync(file, 'export const Space = () => <p>\r\n  &nbsp;\r\n  {" "}\r\n</p>');
    expect(run("--join-spaces").status).toBe(0);
    expect(readFileSync(file, "utf8")).toBe("export const Space = () => <p>\r\n  &nbsp; </p>");
  } finally {
    rmSync(root, { recursive: true, force: true });
  }
});
