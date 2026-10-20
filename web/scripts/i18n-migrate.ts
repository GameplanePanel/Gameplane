#!/usr/bin/env node
// i18n codemod (spec 019 T052). Wraps hard-coded UI strings in
// `intl.formatMessage(...)` calls with generated `web.<path>.<slug>` ids, adds a
// `useOptionalIntl()` hook to the owning component and a description stub.
//
//   node scripts/i18n-migrate.ts [--dry-run] [--join-spaces] [--report <file>] [--root <dir>] <file|dir|glob>...
//   node scripts/i18n-migrate.ts --check [--root <dir>] <file|dir|glob>...
//
// --join-spaces (also run on every migrated file) replaces JSX `{" "}` children by a plain
// space on the same line. --check is read-only: it reproduces the formatjs lint rules
// (no-literal-string-in-jsx, enforce-placeholders, enforce-description, id regex) and exits
// 1 when it finds anything.
//
// Run from web/. --root is the directory whose `src/` child defines id paths
// (default: the current directory). Erasable TypeScript only (Node 22 type
// stripping). Dependencies: typescript and @formatjs/icu-messageformat-parser.
import fs from "node:fs";
import path from "node:path";
import ts from "typescript";
import { parse } from "@formatjs/icu-messageformat-parser";

type Edit = { start: number; end: number; text: string };
type Skip = { line: number; kind: string; text: string };
type Seg = { kind: "lit"; text: string } | { kind: "ph"; expr: string; name: string };
type FileReport = { path: string; migrated: number; skipped: Skip[] };

const ID_RE = /^[a-z][a-z0-9_]*(\.[a-z0-9_-]+)+$/;
const HOOK_IMPORT = `import { useOptionalIntl } from "@/lib/i18n/format";`;
const MIGRATE_ANY_PROPS = new Set(["aria-label", "title", "placeholder", "alt"]);
const MIGRATE_COMPONENT_PROPS = new Set([
  "label",
  "hint",
  "subtitle",
  "description",
  "emptyText",
  "confirmLabel",
  "cancelLabel",
  "message",
  "emptyMessage",
  "tooltip",
  "helperText",
  "errorMessage",
  "heading",
  "caption",
]);
// Calls whose result is side-effect free when the receiver and arguments are.
const PURE_CALL_MEMBERS = new Set(["length", "toString", "toLocaleString", "toFixed"]);
const PURE_ARITH = new Set<ts.SyntaxKind>([
  ts.SyntaxKind.PlusToken,
  ts.SyntaxKind.MinusToken,
  ts.SyntaxKind.AsteriskToken,
  ts.SyntaxKind.SlashToken,
]);
// JSX attributes whose string values are never user-visible copy.
const IGNORED_ATTRS = new Set([
  "className",
  "class",
  "style",
  "id",
  "key",
  "role",
  "type",
  "href",
  "to",
  "variant",
  "size",
  "color",
  "placement",
  "name",
  "value",
]);
const CLASS_HELPERS = new Set(["cn", "clsx", "twMerge"]);
const CLASS_TOKEN_RE = /^[a-z0-9:\/\[\]().%#_!-]+$/;
const NAMED_ENTITIES: Record<string, string> = {
  amp: "&",
  lt: "<",
  gt: ">",
  quot: '"',
  apos: "'",
  nbsp: " ",
  copy: "©",
  hellip: "…",
  mdash: "—",
  ndash: "–",
  lsquo: "‘",
  rsquo: "’",
  ldquo: "“",
  rdquo: "”",
  middot: "·",
  times: "×",
  bull: "•",
  larr: "←",
  rarr: "→",
};

interface FileCtx {
  sf: ts.SourceFile;
  text: string;
  displayPath: string;
  pathPart: string;
  screen: string;
  edits: Edit[];
  skipped: Skip[];
  migrated: number;
  hooks: Map<ts.Node, boolean>;
  takenIds: Set<string>;
  idByMessage: Map<string, string>;
  descByMessage: Map<string, string>;
  replaced: Array<[number, number]>;
}

function decodeEntities(s: string): string {
  return s.replace(/&(#x[0-9a-fA-F]+|#[0-9]+|[A-Za-z][A-Za-z0-9]*);/g, (match, body: string) => {
    if (body.startsWith("#")) {
      const cp = body[1] === "x" || body[1] === "X" ? parseInt(body.slice(2), 16) : parseInt(body.slice(1), 10);
      return Number.isFinite(cp) && cp <= 0x10ffff ? String.fromCodePoint(cp) : match;
    }
    return NAMED_ENTITIES[body] ?? match;
  });
}

// JSX whitespace rules (same algorithm as the TypeScript/Babel JSX transform).
function jsxWhitespace(raw: string): string {
  const lines = raw.split(/\r\n|\n|\r/);
  // Babel's cleanJSXElementLiteralChild: the default is line 0, so whitespace-only text
  // gets no extra space appended.
  let lastNonEmpty = 0;
  lines.forEach((line, i) => {
    if (/[^ \t]/.test(line)) lastNonEmpty = i;
  });
  let out = "";
  lines.forEach((line, i) => {
    let t = line;
    if (i !== 0) t = t.replace(/^[ \t]+/, "");
    if (i !== lines.length - 1) t = t.replace(/[ \t]+$/, "");
    if (t.length > 0) {
      if (i !== lastNonEmpty) t += " ";
      out += t;
    }
  });
  return out;
}

function icuEscape(s: string, nextChar: string): string {
  let out = "";
  for (let i = 0; i < s.length; i++) {
    const c = s[i];
    const n = i + 1 < s.length ? s[i + 1] : nextChar;
    if (c === "{") out += "'{'";
    else if (c === "}") out += "'}'";
    else if (c === "'" && n !== "" && "{}#|".includes(n)) out += "''";
    else out += c;
  }
  return out;
}

function slugify(text: string): string {
  const s = text
    .normalize("NFD")
    .replace(/\p{M}/gu, "")
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, "_")
    .replace(/^_+|_+$/g, "");
  if (s === "") return "text";
  if (s.length <= 40) return s;
  let cut = s.slice(0, 40);
  const idx = cut.lastIndexOf("_");
  if (idx > 0) cut = cut.slice(0, idx);
  cut = cut.replace(/_+$/, "");
  return cut === "" ? "text" : cut;
}

function snake(seg: string): string {
  return seg
    .replace(/[-.\s]+/g, "_")
    .replace(/([A-Z]+)([A-Z][a-z])/g, "$1_$2")
    .replace(/([a-z0-9])([A-Z])/g, "$1_$2")
    .toLowerCase()
    .replace(/[^a-z0-9_]/g, "_")
    .replace(/_+/g, "_")
    .replace(/^_+|_+$/g, "");
}

function camelName(raw: string): string {
  const parts = raw.split(/[^A-Za-z0-9]+/).filter((p) => p.length > 0);
  const out = parts
    .map((p, i) => {
      const lower = p === p.toUpperCase() ? p.toLowerCase() : p;
      const head = i === 0 ? lower.charAt(0).toLowerCase() : lower.charAt(0).toUpperCase();
      return head + lower.slice(1);
    })
    .join("");
  if (out === "") return "value";
  if (!/^[a-z]/.test(out)) return `v${out}`;
  return out;
}

// Name for a placeholder: the last identifier in the expression, camelCased
// ("stepIndex + 1" -> stepIndex, "steps.length" -> length, "STEP_TITLES[currentKey]" -> currentKey).
function placeholderName(e: ts.Expression): string {
  const names: string[] = [];
  const visit = (n: ts.Node): void => {
    if (ts.isIdentifier(n)) names.push(n.text);
    ts.forEachChild(n, visit);
  };
  visit(e);
  const last = names[names.length - 1];
  return last === undefined ? "value" : camelName(last);
}

// Root identifier of a property chain (`format.bytes` -> `format`).
function rootIdentifier(e: ts.Expression): string | undefined {
  let cur: ts.Expression = e;
  while (ts.isPropertyAccessExpression(cur)) cur = cur.expression;
  return ts.isIdentifier(cur) ? cur.text : undefined;
}

// String literals: "any" allows them (call arguments such as format options); "nonprose"
// allows only letter-free ones, so prose concatenated into an expression stays visible
// to translators instead of being hidden inside a placeholder value.
type StringMode = "any" | "nonprose";

// Side-effect-free expression that may stand inside a sentence as one placeholder:
// identifiers, property/element access, numbers, + - * / arithmetic and concatenation,
// parentheses, length/toString/toLocaleString/toFixed calls, and format.* / formatNumber calls.
function isPureExpr(e: ts.Expression, strings: StringMode): boolean {
  if (ts.isIdentifier(e) || ts.isNumericLiteral(e)) return true;
  if (ts.isStringLiteral(e) || ts.isNoSubstitutionTemplateLiteral(e)) {
    return strings === "any" || !/\p{L}/u.test(e.text);
  }
  if (ts.isParenthesizedExpression(e) || ts.isNonNullExpression(e) || ts.isPropertyAccessExpression(e)) {
    return isPureExpr(e.expression, strings);
  }
  if (ts.isElementAccessExpression(e)) {
    return isPureExpr(e.expression, strings) && isPureExpr(e.argumentExpression, "any");
  }
  if (ts.isBinaryExpression(e)) {
    return PURE_ARITH.has(e.operatorToken.kind) && isPureExpr(e.left, "nonprose") && isPureExpr(e.right, "nonprose");
  }
  if (ts.isCallExpression(e)) {
    if (!e.arguments.every((a) => isPureExpr(a, "any"))) return false;
    const callee = e.expression;
    if (ts.isPropertyAccessExpression(callee) && PURE_CALL_MEMBERS.has(callee.name.text)) {
      return isPureExpr(callee.expression, strings);
    }
    if (ts.isPropertyAccessExpression(callee)) return rootIdentifier(callee) === "format";
    return ts.isIdentifier(callee) && callee.text === "formatNumber";
  }
  return false;
}

function isRunPiece(c: ts.JsxChild): boolean {
  if (ts.isJsxText(c)) return true;
  if (ts.isJsxExpression(c)) {
    const e = c.expression;
    if (e === undefined) return false;
    return ts.isStringLiteral(e) || ts.isNoSubstitutionTemplateLiteral(e) || isPureExpr(e, "any");
  }
  return false;
}

// Children that split a sentence: JSX elements and expressions the codemod leaves alone.
function isSplitter(c: ts.JsxChild): boolean {
  return isElementNode(c) || (ts.isJsxExpression(c) && c.expression !== undefined);
}

function isElementNode(c: ts.Node): boolean {
  return ts.isJsxElement(c) || ts.isJsxSelfClosingElement(c) || ts.isJsxFragment(c);
}

function isFn(n: ts.Node): boolean {
  return ts.isArrowFunction(n) || ts.isFunctionExpression(n) || ts.isFunctionDeclaration(n) || ts.isMethodDeclaration(n);
}

function fnParts(fn: ts.Node): { params: readonly ts.ParameterDeclaration[]; body: ts.ConciseBody | undefined } {
  if (ts.isArrowFunction(fn) || ts.isFunctionExpression(fn) || ts.isFunctionDeclaration(fn) || ts.isMethodDeclaration(fn)) {
    return { params: fn.parameters, body: fn.body };
  }
  return { params: [], body: undefined };
}

function isWrapperCall(c: ts.CallExpression): boolean {
  const callee = c.expression;
  const name = ts.isIdentifier(callee) ? callee.text : ts.isPropertyAccessExpression(callee) ? callee.name.text : "";
  return name === "forwardRef" || name === "memo";
}

// Name of a function: its own name, or the const it is assigned to (through forwardRef/memo).
function ownerName(fn: ts.Node): string | undefined {
  if (ts.isFunctionDeclaration(fn) || ts.isFunctionExpression(fn) || ts.isMethodDeclaration(fn)) {
    if (fn.name && ts.isIdentifier(fn.name)) return fn.name.text;
  }
  let n: ts.Node = fn;
  while (n.parent && ts.isCallExpression(n.parent) && isWrapperCall(n.parent)) n = n.parent;
  const p = n.parent;
  if (p && ts.isVariableDeclaration(p) && ts.isIdentifier(p.name)) return p.name.text;
  return undefined;
}

// Nearest enclosing named function. Anonymous callbacks are skipped; a named
// function qualifies when PascalCase or `use*`; anything else (or a class or
// module level) means there is no component to hook.
function findComponent(node: ts.Node): ts.Node | null {
  let cur: ts.Node | undefined = node.parent;
  while (cur) {
    if (ts.isClassLike(cur) || ts.isSourceFile(cur)) return null;
    if (isFn(cur)) {
      const name = ownerName(cur);
      if (name !== undefined) {
        return /^[A-Z]/.test(name) || /^use(?=[A-Z0-9_]|$)/.test(name) ? cur : null;
      }
    }
    cur = cur.parent;
  }
  return null;
}

// True when a binding pattern (identifier, object or array destructuring) binds `target`.
function bindsName(name: ts.BindingName, target: string): boolean {
  if (ts.isIdentifier(name)) return name.text === target;
  for (const el of name.elements) {
    if (ts.isBindingElement(el) && bindsName(el.name, target)) return true;
  }
  return false;
}

function isHookCall(e: ts.Expression | undefined, sf: ts.SourceFile): boolean {
  return e !== undefined && e.getText(sf) === "useOptionalIntl()";
}

// Name clash check for the `intl` local the codemod inserts. Parameters and every
// variable declaration in the body count; a top-level `const intl = useOptionalIntl()`
// is reused; anything else (including `useIntl()`) is taken.
function intlStatus(fn: ts.Node, sf: ts.SourceFile): "insert" | "reuse" | "taken" {
  const { params, body } = fnParts(fn);
  for (const p of params) {
    if (bindsName(p.name, "intl")) return "taken";
  }
  if (!body) return "insert";
  let reusable = false;
  let taken = false;
  const visit = (node: ts.Node): void => {
    if (ts.isVariableDeclaration(node) && bindsName(node.name, "intl")) {
      const stmt = node.parent.parent;
      const topLevel = ts.isVariableStatement(stmt) && stmt.parent === body;
      if (topLevel && ts.isIdentifier(node.name) && isHookCall(node.initializer, sf)) reusable = true;
      else taken = true;
    }
    if (ts.isFunctionDeclaration(node) && node.name?.text === "intl") taken = true;
    ts.forEachChild(node, visit);
  };
  visit(body);
  if (taken) return "taken";
  return reusable ? "reuse" : "insert";
}

function isIgnoredAttr(name: string): boolean {
  return IGNORED_ATTRS.has(name) || name.startsWith("data-");
}

// Every whitespace-separated token is a lowercase utility-class-like token.
function isClassLikeText(text: string): boolean {
  const toks = text.split(/\s+/).filter((t) => t.length > 0);
  return toks.length > 0 && toks.every((t) => CLASS_TOKEN_RE.test(t) && !/\p{Lu}/u.test(t));
}

function calleeName(e: ts.Expression): string {
  if (ts.isIdentifier(e)) return e.text;
  if (ts.isPropertyAccessExpression(e)) return e.name.text;
  return "";
}

// Joins consecutive literal segments so escaping sees the full literal text.
function mergeLiterals(segs: Seg[]): Seg[] {
  const out: Seg[] = [];
  for (const s of segs) {
    const prev = out[out.length - 1];
    if (s.kind === "lit" && prev !== undefined && prev.kind === "lit") {
      out[out.length - 1] = { kind: "lit", text: prev.text + s.text };
    } else if (s.kind === "lit") {
      out.push({ kind: "lit", text: s.text });
    } else {
      out.push(s);
    }
  }
  return out;
}

function lineOf(ctx: FileCtx, pos: number): number {
  return ctx.sf.getLineAndCharacterOfPosition(pos).line + 1;
}

function snippet(s: string): string {
  return s.replace(/\s+/g, " ").trim().slice(0, 160);
}

function startOf(ctx: FileCtx, c: ts.Node): number {
  return ts.isJsxText(c) ? c.pos : c.getStart(ctx.sf);
}

function tagOf(ctx: FileCtx, container: ts.JsxElement | ts.JsxFragment): string {
  return ts.isJsxElement(container) ? container.openingElement.tagName.getText(ctx.sf) : "Fragment";
}

function allocateId(ctx: FileCtx, icu: string, slug: string): string {
  const known = ctx.idByMessage.get(icu);
  if (known !== undefined) return known;
  const base = `web.${ctx.pathPart}.${slug}`;
  let id = base;
  let n = 2;
  while (ctx.takenIds.has(id)) {
    id = `${base}_${n}`;
    n += 1;
  }
  if (!ID_RE.test(id)) throw new Error(`generated id fails ${ID_RE}: ${id}`);
  ctx.takenIds.add(id);
  ctx.idByMessage.set(icu, id);
  return id;
}

// Asserts the produced ICU message parses, holds exactly the expected literal
// text and the expected argument names.
function assertIcu(icu: string, plain: string, names: string[], where: string): void {
  let els: Array<{ type: number; value?: string }>;
  try {
    els = parse(icu) as unknown as Array<{ type: number; value?: string }>;
  } catch (err) {
    throw new Error(`ICU parse failed at ${where}: ${icu}: ${String(err)}`);
  }
  const litText = els
    .map((e) => {
      if (e.type === 0) return e.value ?? "";
      if (e.type === 1) return "";
      throw new Error(`unexpected ICU element type ${e.type} at ${where}: ${icu}`);
    })
    .join("");
  if (litText !== plain) throw new Error(`ICU literal mismatch at ${where}: ${JSON.stringify(litText)} != ${JSON.stringify(plain)}`);
  const args = new Set(els.filter((e) => e.type === 1).map((e) => e.value ?? ""));
  if (args.size !== names.length || names.some((n) => !args.has(n))) {
    throw new Error(`ICU argument mismatch at ${where}: ${icu}`);
  }
}

function uniqueName(base: string, used: Set<string>): string {
  let n = base;
  let i = 2;
  while (used.has(n)) {
    n = `${base}${i}`;
    i += 1;
  }
  used.add(n);
  return n;
}

function screenWords(basename: string, parentDir: string): string {
  const raw = basename === "index" ? parentDir : basename;
  const spaced = raw
    .replace(/[-_]+/g, " ")
    .replace(/([a-z0-9])([A-Z])/g, "$1 $2")
    .replace(/([A-Z]+)([A-Z][a-z])/g, "$1 $2")
    .toLowerCase()
    .trim();
  return spaced.charAt(0).toUpperCase() + spaced.slice(1);
}

// Emits one `intl.formatMessage` replacement for a site inside `target`.
// Returns false when the site had to be skipped (already reported).
function emitSite(
  ctx: FileCtx,
  site: { start: number; end: number; line: number; text: string },
  target: ts.Node | null,
  icu: string,
  plain: string,
  names: string[],
  description: string,
  slugSource: string,
  values: Map<string, string>,
  lead: string,
  trail: string,
  wrap: boolean,
): void {
  if (target === null) {
    ctx.skipped.push({ line: site.line, kind: "no-component", text: snippet(site.text) });
    return;
  }
  const status = intlStatus(target, ctx.sf);
  if (status === "taken") {
    ctx.skipped.push({ line: site.line, kind: "intl-name-taken", text: snippet(site.text) });
    return;
  }
  assertIcu(icu, plain, names, `${ctx.displayPath}:${site.line}`);
  const id = allocateId(ctx, icu, slugify(slugSource));
  if (!ctx.descByMessage.has(icu)) ctx.descByMessage.set(icu, description);
  const desc = ctx.descByMessage.get(icu) ?? description;
  if (status === "insert") ctx.hooks.set(target, true);
  else if (!ctx.hooks.has(target)) ctx.hooks.set(target, false);

  const valuesPart =
    values.size > 0 ? `, { ${[...values].map(([n, e]) => `${n}: ${e}`).join(", ")} }` : "";
  const call = `{intl.formatMessage({ id: ${JSON.stringify(id)}, defaultMessage: ${JSON.stringify(icu)}, description: ${JSON.stringify(desc)} }${valuesPart})}`;
  // Edge whitespace stays outside the message as plain JSX text (never `{" "}`).
  const text = wrap ? `${rawEdge(lead)}${call}${rawEdge(trail)}` : call;
  ctx.edits.push({ start: site.start, end: site.end, text });
  ctx.replaced.push([site.start, site.end]);
  ctx.migrated += 1;
}

function handleRun(ctx: FileCtx, run: ts.JsxChild[], container: ts.JsxElement | ts.JsxFragment, adjacentElement: boolean): void {
  const segs: Seg[] = [];
  const byExpr = new Map<string, string>();
  const usedNames = new Set<string>();
  for (const c of run) {
    if (ts.isJsxText(c)) {
      segs.push({ kind: "lit", text: decodeEntities(jsxWhitespace(c.text)) });
    } else if (ts.isJsxExpression(c) && c.expression) {
      const e = c.expression;
      if (ts.isStringLiteral(e) || ts.isNoSubstitutionTemplateLiteral(e)) {
        segs.push({ kind: "lit", text: e.text });
      } else {
        const expr = e.getText(ctx.sf);
        let name = byExpr.get(expr);
        if (name === undefined) {
          name = uniqueName(placeholderName(e), usedNames);
          byExpr.set(expr, name);
        }
        segs.push({ kind: "ph", expr, name });
      }
    }
  }
  if (!segs.some((s) => s.kind === "lit" && /\p{L}/u.test(s.text))) return;

  const first = run[0];
  const last = run[run.length - 1];
  const siteStart = startOf(ctx, first);
  const siteEnd = last.end;
  const line = lineOf(ctx, siteStart);
  const rawText = ctx.text.slice(siteStart, siteEnd);

  if (segs.some((s) => s.kind === "lit" && s.text.includes("<"))) {
    ctx.skipped.push({ line, kind: "angle-bracket", text: snippet(rawText) });
    return;
  }

  const merged = mergeLiterals(segs);
  let lead = "";
  let trail = "";
  const head = merged[0];
  if (head.kind === "lit") {
    const m = /^\s+/.exec(head.text);
    lead = m ? m[0] : "";
    head.text = head.text.slice(lead.length);
  }
  const tail = merged[merged.length - 1];
  if (tail.kind === "lit") {
    const m = /\s+$/.exec(tail.text);
    trail = m ? m[0] : "";
    tail.text = tail.text.slice(0, tail.text.length - trail.length);
  }
  const core = merged.filter((s) => s.kind === "ph" || s.text.length > 0);
  // Slug source: literal text with each placeholder replaced by its name ("Delete {username}?" -> delete_username).
  const slugSource = core.map((s) => (s.kind === "lit" ? s.text : s.name)).join("");

  let icu = "";
  let plain = "";
  core.forEach((s, i) => {
    if (s.kind === "lit") {
      const nxt = core[i + 1];
      icu += icuEscape(s.text, nxt && nxt.kind === "ph" ? "{" : "");
      plain += s.text;
    } else {
      icu += `{${s.name}}`;
    }
  });

  const names: string[] = [];
  const values = new Map<string, string>();
  for (const s of core) {
    if (s.kind === "ph" && !values.has(s.name)) {
      values.set(s.name, s.expr);
      names.push(s.name);
    }
  }
  const kind = names.length > 0 ? `sentence with ${names.join(", ")} in ${tagOf(ctx, container)}` : `text in ${tagOf(ctx, container)}`;
  const description = `${ctx.screen}: ${kind}`;

  const target = findComponent(container);
  const before = ctx.migrated;
  emitSite(
    ctx,
    { start: siteStart, end: siteEnd, line, text: rawText },
    target,
    icu,
    plain,
    names,
    description,
    slugSource,
    values,
    lead,
    trail,
    true,
  );
  if (adjacentElement && ctx.migrated > before) {
    ctx.skipped.push({ line, kind: "rich-text", text: snippet(rawText) });
  }
}

function processChildren(ctx: FileCtx, container: ts.JsxElement | ts.JsxFragment, children: ts.NodeArray<ts.JsxChild>): void {
  let i = 0;
  while (i < children.length) {
    if (!isRunPiece(children[i])) {
      i += 1;
      continue;
    }
    let j = i;
    while (j < children.length && isRunPiece(children[j])) j += 1;
    const run = children.slice(i, j);
    const adjacent = (i > 0 && isSplitter(children[i - 1])) || (j < children.length && isSplitter(children[j]));
    handleRun(ctx, run, container, adjacent);
    i = j;
  }
}

function handleAttr(ctx: FileCtx, attr: ts.JsxAttribute): void {
  const init = attr.initializer;
  if (!init || !ts.isStringLiteral(init)) return;
  const name = attr.name.getText(ctx.sf);
  const tag = attr.parent.parent.tagName.getText(ctx.sf);
  const value = decodeEntities(init.text);
  const migrate = MIGRATE_ANY_PROPS.has(name) || (MIGRATE_COMPONENT_PROPS.has(name) && /^[A-Z]/.test(tag));
  if (!migrate) {
    if (isIgnoredAttr(name)) return;
    if ((value.includes(" ") || /^\p{Lu}/u.test(value)) && !isClassLikeText(value)) {
      ctx.skipped.push({ line: lineOf(ctx, init.getStart(ctx.sf)), kind: "prop-review", text: snippet(`${name}="${init.text}"`) });
    }
    return;
  }
  if (!/\p{L}/u.test(value)) return;
  const line = lineOf(ctx, init.getStart(ctx.sf));
  const raw = `${name}=${init.getText(ctx.sf)}`;
  if (value.includes("<")) {
    ctx.skipped.push({ line, kind: "angle-bracket", text: snippet(raw) });
    return;
  }
  const icu = icuEscape(value, "");
  const description = `${ctx.screen}: ${name} of ${tag}`;
  emitSite(
    ctx,
    { start: init.getStart(ctx.sf), end: init.end, line, text: raw },
    findComponent(attr),
    icu,
    value,
    [],
    description,
    value,
    new Map<string, string>(),
    "",
    "",
    false,
  );
}

// Reports strings inside JSX expression containers that the codemod does not change.
function scanExpressionStrings(ctx: FileCtx, node: ts.Node): void {
  if (ts.isStringLiteral(node) || ts.isNoSubstitutionTemplateLiteral(node) || ts.isTemplateExpression(node)) {
    const parent = node.parent;
    if (parent && ts.isJsxAttribute(parent)) return;
    let text = "";
    if (ts.isTemplateExpression(node)) {
      text = node.head.text + node.templateSpans.map((s) => s.literal.text).join("");
    } else {
      text = node.text;
    }
    if (!/\p{L}/u.test(text) || !text.includes(" ")) return;
    if (isClassLikeText(text)) return;
    const start = node.getStart(ctx.sf);
    if (ctx.replaced.some(([s, e]) => start >= s && start < e)) return;
    let inExpr = false;
    let cur: ts.Node | undefined = node.parent;
    while (cur) {
      if (ts.isJsxAttribute(cur) && isIgnoredAttr(cur.name.getText(ctx.sf))) return;
      if (ts.isJsxExpression(cur)) inExpr = true;
      if (ts.isCallExpression(cur)) {
        if (/formatMessage$/.test(cur.expression.getText(ctx.sf))) return;
        if (CLASS_HELPERS.has(calleeName(cur.expression))) return;
      }
      if (ts.isSourceFile(cur)) break;
      cur = cur.parent;
    }
    if (inExpr) ctx.skipped.push({ line: lineOf(ctx, start), kind: "string-in-expression", text: snippet(text) });
    return;
  }
  ts.forEachChild(node, (child) => scanExpressionStrings(ctx, child));
}

function collectExistingIds(ctx: FileCtx, node: ts.Node): void {
  if (ts.isPropertyAssignment(node) && ts.isIdentifier(node.name) && node.name.text === "id" && ts.isStringLiteral(node.initializer)) {
    ctx.takenIds.add(node.initializer.text);
  }
  ts.forEachChild(node, (child) => collectExistingIds(ctx, child));
}

function visitJsx(ctx: FileCtx, node: ts.Node): void {
  if (ts.isJsxElement(node) || ts.isJsxFragment(node)) {
    processChildren(ctx, node, node.children);
  } else if (ts.isJsxAttribute(node)) {
    handleAttr(ctx, node);
  }
  ts.forEachChild(node, (child) => visitJsx(ctx, child));
}

function indentOf(ctx: FileCtx, node: ts.Node): string {
  const line = ctx.sf.getLineAndCharacterOfPosition(node.getStart(ctx.sf)).line;
  const lineText = ctx.text.split("\n")[line] ?? "";
  const m = /^[ \t]*/.exec(lineText);
  return m ? m[0] : "";
}

function applyEdits(text: string, edits: Edit[]): string {
  const sorted = [...edits].sort((a, b) => b.start - a.start || b.end - a.end);
  let out = text;
  let floor = Number.POSITIVE_INFINITY;
  for (const e of sorted) {
    if (e.end > floor) throw new Error(`overlapping edits at offset ${e.start}`);
    out = out.slice(0, e.start) + e.text + out.slice(e.end);
    floor = e.start;
  }
  return out;
}

function migrateFile(file: string, root: string, migrate: boolean): MigrateResult {
  const source = fs.readFileSync(file, "utf8");
  const displayPath = path.relative(root, file).split(path.sep).join("/");
  const joined = joinSpaces(source, displayPath);
  const text = joined.text;
  if (!migrate) {
    return { source, input: text, output: text, edits: [], report: { path: displayPath, migrated: 0, skipped: joined.lone } };
  }
  const srcDir = path.join(root, "src");
  const relFromSrc = path.relative(srcDir, file).split(path.sep).join("/");
  const base = path.basename(file).replace(/\.(tsx?)$/, "");
  const parentDir = path.basename(path.dirname(file));
  const sf = ts.createSourceFile(
    file,
    text,
    ts.ScriptTarget.Latest,
    true,
    file.endsWith(".tsx") ? ts.ScriptKind.TSX : ts.ScriptKind.TS,
  );
  const pathPart = relFromSrc
    .replace(/\.(tsx?)$/, "")
    .split("/")
    .map((seg) => snake(seg))
    .join(".");
  const ctx: FileCtx = {
    sf,
    text,
    displayPath: path.relative(root, file).split(path.sep).join("/"),
    pathPart,
    screen: screenWords(base, parentDir),
    edits: [],
    skipped: [],
    migrated: 0,
    hooks: new Map<ts.Node, boolean>(),
    takenIds: new Set<string>(),
    idByMessage: new Map<string, string>(),
    descByMessage: new Map<string, string>(),
    replaced: [],
  };

  ctx.skipped.push(...joined.lone);
  collectExistingIds(ctx, sf);
  visitJsx(ctx, sf);
  scanExpressionStrings(ctx, sf);

  let inserted = 0;
  for (const [fn, needsInsert] of ctx.hooks) {
    if (!needsInsert) continue;
    inserted += 1;
    const { body } = fnParts(fn);
    if (!body) continue;
    if (ts.isBlock(body)) {
      const at = body.getStart(sf) + 1;
      ctx.edits.push({ start: at, end: at, text: `\n${indentOf(ctx, fn)}  const intl = useOptionalIntl();` });
    } else {
      const at = body.getStart(sf);
      const ind = indentOf(ctx, fn);
      ctx.edits.push({ start: at, end: at, text: `{\n${ind}  const intl = useOptionalIntl();\n${ind}  return ` });
      ctx.edits.push({ start: body.end, end: body.end, text: `;\n${ind}}` });
    }
  }
  if (inserted > 0 && !sf.statements.some((st) => st.getText(sf).includes("useOptionalIntl"))) {
    const imports = sf.statements.filter((st) => ts.isImportDeclaration(st));
    const lastImport = imports[imports.length - 1];
    if (lastImport) {
      ctx.edits.push({ start: lastImport.end, end: lastImport.end, text: `\n${HOOK_IMPORT}` });
    } else {
      ctx.edits.push({ start: 0, end: 0, text: `${HOOK_IMPORT}\n` });
    }
  }

  const output = applyEdits(text, ctx.edits);
  ctx.skipped.sort((a, b) => a.line - b.line);
  return {
    source,
    input: text,
    output,
    edits: ctx.edits,
    report: { path: ctx.displayPath, migrated: ctx.migrated, skipped: ctx.skipped },
  };
}

type MigrateResult = { source: string; input: string; output: string; report: FileReport; edits: Edit[] };
type Finding = { file: string; line: number; col: number; rule: string; message: string };
type IcuEl = { type: number; value?: string; options?: Record<string, { value: IcuEl[] }>; children?: IcuEl[] };

// Props checked by formatjs/no-literal-string-in-jsx (config include plus the plugin defaults).
const CHECK_PROPS = new Set(["aria-label", "aria-description", "aria-details", "aria-errormessage", "title", "placeholder", "alt"]);
const LITERAL_OPERATORS = new Set<ts.SyntaxKind>([
  ts.SyntaxKind.PlusToken,
  ts.SyntaxKind.AmpersandAmpersandToken,
  ts.SyntaxKind.BarBarToken,
  ts.SyntaxKind.QuestionQuestionToken,
]);

function parseSource(file: string, text: string): ts.SourceFile {
  return ts.createSourceFile(
    file,
    text,
    ts.ScriptTarget.Latest,
    true,
    file.endsWith(".tsx") ? ts.ScriptKind.TSX : ts.ScriptKind.TS,
  );
}

function isNewline(s: string): boolean {
  return /[\r\n]/.test(s);
}

// Edge whitespace of a migrated run goes back into the JSX text as plain spaces (JSX keeps
// same-line spaces); anything else falls back to a string expression.
function rawEdge(s: string): string {
  if (s === "") return "";
  return /^[ \t]+$/.test(s) ? s : `{${JSON.stringify(s)}}`;
}

function isSpaceChild(c: ts.JsxChild): boolean {
  return (
    ts.isJsxExpression(c) &&
    c.expression !== undefined &&
    (ts.isStringLiteral(c.expression) || ts.isNoSubstitutionTemplateLiteral(c.expression)) &&
    c.expression.text === " "
  );
}

// Whitespace-only JSX text containing a newline is dropped by JSX, so it renders nothing.
function rendersChild(c: ts.JsxChild): boolean {
  return !(ts.isJsxText(c) && /^\s*$/.test(c.text) && isNewline(c.text));
}

type SpaceSite = { parent: ts.JsxElement | ts.JsxFragment; index: number };

function spaceSites(sf: ts.SourceFile): SpaceSite[] {
  const out: SpaceSite[] = [];
  const visit = (node: ts.Node): void => {
    if (ts.isJsxElement(node) || ts.isJsxFragment(node)) {
      const parent = node;
      parent.children.forEach((c, index) => {
        if (isSpaceChild(c)) out.push({ parent, index });
      });
    }
    ts.forEachChild(node, visit);
  };
  visit(sf);
  return out;
}

// A `{" "}` with no rendering sibling on either side (lone space child). Left alone and reported.
function isLoneSite(site: SpaceSite): boolean {
  return site.parent.children.every((c, j) => j === site.index || !rendersChild(c));
}

// End of the content before a child: trailing whitespace that holds a newline is dropped.
function edgeStart(sf: ts.SourceFile, c: ts.JsxChild): number {
  if (!ts.isJsxText(c)) return c.end;
  const trailing = /\s*$/.exec(sf.text.slice(c.pos, c.end))?.[0] ?? "";
  return isNewline(trailing) ? c.end - trailing.length : c.end;
}

// Start of the content after a child: leading whitespace that holds a newline is dropped.
function edgeEnd(sf: ts.SourceFile, c: ts.JsxChild): number {
  if (!ts.isJsxText(c)) return c.getStart(sf);
  const leading = /^\s*/.exec(sf.text.slice(c.pos, c.end))?.[0] ?? "";
  return isNewline(leading) ? c.pos + leading.length : c.pos;
}

// Range replaced by a single space: from the previous content (or the opening tag) to the
// next content (or the closing tag). It always contains the `{" "}` itself.
function joinRange(sf: ts.SourceFile, site: SpaceSite): { start: number; end: number } {
  const { parent, index } = site;
  const kids = parent.children;
  const start =
    index > 0
      ? edgeStart(sf, kids[index - 1])
      : ts.isJsxElement(parent)
        ? parent.openingElement.end
        : parent.openingFragment.end;
  const end =
    index < kids.length - 1
      ? edgeEnd(sf, kids[index + 1])
      : ts.isJsxElement(parent)
        ? parent.closingElement.getStart(sf)
        : parent.closingFragment.getStart(sf);
  return { start, end };
}

// Replaces every non-lone `{" "}` child with a plain same-line space. One edit per pass, then
// re-parse, because neighbouring sites share whitespace. Lone sites are reported with their line.
function joinSpaces(source: string, displayPath: string): { text: string; lone: Skip[] } {
  let text = source;
  for (;;) {
    const sf = parseSource(displayPath, text);
    const site = spaceSites(sf).find((s) => !isLoneSite(s));
    if (site === undefined) break;
    const { start, end } = joinRange(sf, site);
    text = `${text.slice(0, start)} ${text.slice(end)}`;
  }
  const sf = parseSource(displayPath, text);
  const lone = spaceSites(sf)
    .filter(isLoneSite)
    .map((s) => {
      const node = s.parent.children[s.index];
      const line = sf.getLineAndCharacterOfPosition(node.getStart(sf)).line + 1;
      return { line, kind: "lone-space", text: snippet(node.getText(sf)) };
    });
  return { text, lone };
}

// Argument names of an ICU message: simple/number/date/time args, select and plural args
// (with their option bodies) and rich-text tags. `#` is not an argument.
function icuArgNames(els: IcuEl[], out: Set<string> = new Set<string>()): Set<string> {
  for (const el of els) {
    if (el.type >= 1 && el.type <= 6) out.add(el.value ?? "");
    if (el.type === 5 || el.type === 6) {
      for (const opt of Object.values(el.options ?? {})) icuArgNames(opt.value, out);
    }
    if (el.type === 8) {
      out.add(el.value ?? "");
      icuArgNames(el.children ?? [], out);
    }
  }
  return out;
}

// Read-only reproduction of the formatjs lint rules that web/eslint.config.js enables.
function checkFile(file: string, root: string): Finding[] {
  const sf = parseSource(file, fs.readFileSync(file, "utf8"));
  const displayPath = path.relative(root, file).split(path.sep).join("/");
  const findings: Finding[] = [];
  const report = (node: ts.Node, rule: string, message: string): void => {
    const pos = ts.isJsxText(node) ? node.pos : node.getStart(sf);
    const at = sf.getLineAndCharacterOfPosition(pos);
    findings.push({ file: displayPath, line: at.line + 1, col: at.character + 1, rule: `formatjs/${rule}`, message });
  };
  const literalRule = "no-literal-string-in-jsx";
  const literalMsg = "string literal in JSX; format it with intl.formatMessage";

  const checkJsxExpr = (e: ts.Expression): void => {
    if (ts.isParenthesizedExpression(e)) {
      checkJsxExpr(e.expression);
    } else if (ts.isStringLiteral(e) || ts.isNoSubstitutionTemplateLiteral(e)) {
      if (e.text !== "") report(e, literalRule, literalMsg);
    } else if (ts.isTemplateExpression(e)) {
      report(e, literalRule, literalMsg);
    } else if (ts.isConditionalExpression(e)) {
      checkJsxExpr(e.whenTrue);
      checkJsxExpr(e.whenFalse);
    } else if (ts.isBinaryExpression(e) && LITERAL_OPERATORS.has(e.operatorToken.kind)) {
      checkJsxExpr(e.left);
      checkJsxExpr(e.right);
    }
  };

  const checkDescriptor = (obj: ts.ObjectLiteralExpression, values: ts.Expression | undefined, isFormat: boolean): void => {
    const props = new Map<string, ts.Expression>();
    for (const p of obj.properties) {
      if (ts.isPropertyAssignment(p) && (ts.isIdentifier(p.name) || ts.isStringLiteral(p.name))) {
        props.set(p.name.text, p.initializer);
      } else if (ts.isShorthandPropertyAssignment(p)) {
        props.set(p.name.text, p.name);
      }
    }
    const id = props.get("id");
    // A computed id (a variable, e.g. a server catalog key) cannot be checked statically.
    if (id === undefined) {
      report(obj, "enforce-id", "descriptor has no id");
    } else if (ts.isStringLiteral(id) && !ID_RE.test(id.text)) {
      report(id, "enforce-id", `id must match ${ID_RE.source}`);
    }
    const desc = props.get("description");
    if (desc === undefined || !ts.isStringLiteral(desc) || desc.text === "") {
      report(desc ?? obj, "enforce-description", "description must be a non-empty string literal");
    }
    const msg = props.get("defaultMessage");
    if (msg === undefined || !(ts.isStringLiteral(msg) || ts.isNoSubstitutionTemplateLiteral(msg))) return;
    let els: IcuEl[];
    try {
      els = parse(msg.text) as unknown as IcuEl[];
    } catch (err) {
      report(msg, "enforce-placeholders", `ICU parse error: ${snippet(String(err))}`);
      return;
    }
    // defineMessages descriptors get their values at a later formatMessage call, so only the
    // formatMessage call site can be checked against its values object.
    if (!isFormat) return;
    const names = icuArgNames(els);
    if (values === undefined) {
      if (names.size > 0) report(obj, "enforce-placeholders", `no values for placeholder(s) ${[...names].join(", ")}`);
      return;
    }
    if (!ts.isObjectLiteralExpression(values)) return;
    const keys = new Set<string>();
    for (const p of values.properties) {
      if (ts.isSpreadAssignment(p)) return;
      if (ts.isIdentifier(p.name) || ts.isStringLiteral(p.name)) keys.add(p.name.text);
    }
    const missing = [...names].filter((n) => !keys.has(n));
    const extra = [...keys].filter((k) => !names.has(k));
    if (missing.length > 0) report(values, "enforce-placeholders", `missing value for placeholder(s) ${missing.join(", ")}`);
    if (extra.length > 0) report(values, "enforce-placeholders", `value(s) not used in defaultMessage: ${extra.join(", ")}`);
  };

  const checkCall = (call: ts.CallExpression): void => {
    const callee = call.expression;
    const first = call.arguments[0];
    if (ts.isPropertyAccessExpression(callee) && callee.name.text === "formatMessage") {
      if (first && ts.isObjectLiteralExpression(first)) checkDescriptor(first, call.arguments[1], true);
    } else if (ts.isIdentifier(callee) && callee.text === "defineMessages") {
      if (first && ts.isObjectLiteralExpression(first)) {
        for (const p of first.properties) {
          if (ts.isPropertyAssignment(p) && ts.isObjectLiteralExpression(p.initializer)) {
            checkDescriptor(p.initializer, undefined, false);
          }
        }
      }
    } else if (ts.isIdentifier(callee) && callee.text === "defineMessage") {
      if (first && ts.isObjectLiteralExpression(first)) checkDescriptor(first, undefined, false);
    }
  };

  const visit = (node: ts.Node): void => {
    if (ts.isJsxElement(node) || ts.isJsxFragment(node)) {
      for (const c of node.children) {
        if (ts.isJsxText(c)) {
          if (/\S/.test(decodeEntities(c.text))) report(c, literalRule, literalMsg);
        } else if (ts.isJsxExpression(c) && c.expression) {
          checkJsxExpr(c.expression);
        }
      }
    } else if (ts.isJsxAttribute(node)) {
      const init = node.initializer;
      if (init !== undefined && CHECK_PROPS.has(node.name.getText(sf))) {
        if (ts.isStringLiteral(init)) {
          if (init.text !== "") report(init, literalRule, literalMsg);
        } else if (ts.isJsxExpression(init) && init.expression) {
          checkJsxExpr(init.expression);
        }
      }
    } else if (ts.isCallExpression(node)) {
      checkCall(node);
    }
    ts.forEachChild(node, visit);
  };
  visit(sf);
  return findings;
}

function walk(dir: string): string[] {
  const out: string[] = [];
  for (const ent of fs.readdirSync(dir, { withFileTypes: true })) {
    const p = path.join(dir, ent.name);
    if (ent.isDirectory()) out.push(...walk(p));
    else if (/\.tsx?$/.test(ent.name)) out.push(p);
  }
  return out;
}

function collectInputs(input: string): string[] {
  if (/[*?[\]{}]/.test(input)) {
    return fs.globSync(input, { cwd: process.cwd() }).map((p) => path.resolve(process.cwd(), p));
  }
  const abs = path.resolve(input);
  if (fs.existsSync(abs) && fs.statSync(abs).isDirectory()) return walk(abs);
  return [abs];
}

function previewDiff(displayPath: string, original: string, edits: Edit[]): void {
  const sf = ts.createSourceFile("x.tsx", original, ts.ScriptTarget.Latest, true, ts.ScriptKind.TSX);
  console.log(`--- a/${displayPath}`);
  console.log(`+++ b/${displayPath}`);
  for (const e of [...edits].sort((a, b) => a.start - b.start)) {
    const line = sf.getLineAndCharacterOfPosition(e.start).line + 1;
    console.log(`@@ ${displayPath}:${line} @@`);
    const oldLines = original.slice(e.start, e.end).split("\n").slice(0, 6);
    const newLines = e.text.split("\n").slice(0, 6);
    for (const l of oldLines) console.log(`-${l}`);
    for (const l of newLines) console.log(`+${l}`);
  }
}

function main(): void {
  const args = process.argv.slice(2);
  let dryRun = false;
  let check = false;
  let joinOnly = false;
  let reportPath: string | undefined;
  let root = process.cwd();
  const inputs: string[] = [];
  for (let i = 0; i < args.length; i++) {
    const a = args[i];
    if (a === "--dry-run") dryRun = true;
    else if (a === "--check") check = true;
    else if (a === "--join-spaces") joinOnly = true;
    else if (a === "--report") reportPath = args[++i];
    else if (a === "--root") root = path.resolve(args[++i]);
    else inputs.push(a);
  }
  if (inputs.length === 0) {
    console.error("usage: node scripts/i18n-migrate.ts [--dry-run] [--join-spaces] [--report <file>] [--root <dir>] <file|dir|glob>...\n       node scripts/i18n-migrate.ts --check [--root <dir>] <file|dir|glob>...");
    process.exitCode = 2;
    return;
  }

  const files = [...new Set(inputs.flatMap(collectInputs))].sort();
  const srcDir = path.join(root, "src");
  const reports: FileReport[] = [];
  const kindTotals: Record<string, number> = {};
  let migratedTotal = 0;
  let changedFiles = 0;
  const findings: Finding[] = [];

  for (const file of files) {
    const rel = path.relative(srcDir, file).split(path.sep).join("/");
    if (rel.startsWith("..") || path.isAbsolute(rel)) {
      console.error(`skip (outside ${srcDir}): ${file}`);
      continue;
    }
    if (/\.test\.tsx?$/.test(file) || /\.d\.ts$/.test(file)) continue;
    if (check) {
      findings.push(...checkFile(file, root));
      continue;
    }
    if (rel.startsWith("generated/") || rel.startsWith("lib/i18n/")) continue;

    let result: MigrateResult;
    try {
      result = migrateFile(file, root, !joinOnly);
    } catch (err) {
      // Nothing is written for a file whose migration failed.
      const message = snippet(err instanceof Error ? err.message : String(err));
      reports.push({
        path: path.relative(root, file).split(path.sep).join("/"),
        migrated: 0,
        skipped: [{ line: 0, kind: "icu-error", text: message }],
      });
      kindTotals["icu-error"] = (kindTotals["icu-error"] ?? 0) + 1;
      console.error(`icu-error: ${file}: ${message}`);
      continue;
    }
    reports.push(result.report);
    migratedTotal += result.report.migrated;
    for (const s of result.report.skipped) kindTotals[s.kind] = (kindTotals[s.kind] ?? 0) + 1;
    if (result.output === result.source) continue;
    changedFiles += 1;
    if (dryRun) {
      previewDiff(result.report.path, result.input, result.edits);
    } else {
      fs.writeFileSync(file, result.output);
    }
  }

  if (check) {
    findings.sort((a, b) => a.file.localeCompare(b.file) || a.line - b.line || a.col - b.col);
    for (const f of findings) console.log(`${f.file}:${f.line}:${f.col} ${f.rule} ${f.message}`);
    process.exitCode = findings.length > 0 ? 1 : 0;
    return;
  }
  if (reportPath) {
    const totals = { files: reports.length, changedFiles, migrated: migratedTotal, skipped: kindTotals };
    fs.writeFileSync(reportPath, `${JSON.stringify({ files: reports, totals }, null, 2)}\n`);
  }
  const mode = dryRun ? "would migrate" : "migrated";
  console.log(`i18n-migrate: ${mode} ${migratedTotal} site(s) in ${changedFiles} file(s); skipped ${JSON.stringify(kindTotals)}`);
}

main();
