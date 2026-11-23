// Exports each screen of a design .fig as its own .fig, a JSON property dump and a PNG.
//
// It runs on OpenPencil's headless CLI internals, so it runs from an OpenPencil checkout
// (https://github.com/ValgulNecron/open-pencil, master), not from this repository:
//
//   cp hack/design/fig-export.ts <open-pencil>/packages/cli/fig-export.ts
//   cd <open-pencil>/packages/cli
//   bun fig-export.ts <gameplane>/design.fig <gameplane>/assets/design-export
//   bun fig-export.ts <gameplane>/telemetry-receiver/telemetry-dashboard.fig \
//     <gameplane>/telemetry-receiver/design-export
//   bun fig-export.ts <gameplane>/website/website.fig <gameplane>/website/website-export
//
// The export is incremental: each screen's entry in screens.json (and each part's in parts.json)
// stores a hash of everything that renders it (its layers, the component layers its instances
// show, the document's variables, and FORMAT below). A screen whose hash matches and whose three
// files exist is skipped; only new or changed screens are rendered again. Pass --all after the
// two paths to re-render every screen, for example after updating the OpenPencil checkout or
// its fonts. Files are only written when their bytes change, and a screen whose JSON and PNG come
// out identical keeps its existing .fig unless its hash changed, so a run does not churn files that
// did not change. Files of screens and parts that no longer exist are deleted.
//
// A screen is a top-level frame or instance on the document's first page, other than notes,
// canvas row labels and component boards (EXCLUDED). Files are named after the screen's entry in
// <out>/screens.json, which keeps the IDs screens had in the old design.pen; a new screen is named
// after its .fig ID. JSON holds the layout, paints, text and the variables each layer binds; an
// instance lists the text it shows instead of its component's layers.
//
// <out>/parts.json lists layers below screen level that the browser visual diff
// (web/scripts/compare-screenshots.mjs) compares against, such as one dialog or one badge. Each
// entry names its file and the layer's exact name; the layer is exported the same way as a screen,
// and the script fills in its ID and size. A top-level layer with that name wins; otherwise the name
// must match exactly one layer outside instances, or the script stops.
import { createHash } from 'node:crypto'
import { access, mkdir, readdir, readFile, rm, writeFile } from 'node:fs/promises'
import { join } from 'node:path'

import { BUILTIN_IO_FORMATS, IORegistry } from '@open-pencil/core/io'
import { strFromU8, strToU8, unzipSync, zipSync } from 'fflate'
import type { SceneGraph, SceneNode } from '@open-pencil/scene-graph'

import { loadDocument, populateWholeDocument } from '#cli/headless'

import { applyExportFontPolicy } from './src/commands/export/font-policy'

const EXCLUDED =
  /^(Note —|Row \d+ ·|HeroUI: Design System Components$|Gameplane: App Components$|Shared Components Band$|Reference frame$|dashboard-utility$)/
const SCALE = 2
/** Part of every screen's hash: bump it when a change to this script changes what it writes. */
const FORMAT = 1
/** Fixed time for the exported .fig archives, so an unchanged screen gives identical bytes. */
const FIXED_TIME = '1980-01-01T00:00:00.000Z'
/** Layers inside an instance get a fresh ID on each load; only saved records keep theirs. */
const SAVED_ID = /^[01]:\d+$/

interface ScreenEntry {
  file: string
  figId: string
  name: string
  width: number
  height: number
  /** Hash of everything that renders the screen; an unchanged hash skips the screen. */
  hash: string
}

type Json = string | number | boolean | null | Json[] | { [key: string]: Json }

const args = process.argv.slice(2)
const all = args.includes('--all')
const [file, outDir] = args.filter((arg) => arg !== '--all')
if (!file || !outDir) throw new Error('usage: bun fig-export.ts <design.fig> <export dir> [--all]')

const round = (value: number) => Math.round(value * 100) / 100

/** The .fig archive with a fixed `exported_at` and fixed zip entry times. */
function stableFig(bytes: Uint8Array): Uint8Array {
  const entries = unzipSync(bytes)
  const meta = entries['meta.json']
  if (meta) {
    const json = JSON.parse(strFromU8(meta)) as { [key: string]: unknown }
    entries['meta.json'] = strToU8(JSON.stringify({ ...json, exported_at: FIXED_TIME }))
  }
  return zipSync(entries, { mtime: new Date(FIXED_TIME) })
}

/** Node fields that hold IDs, which change between loads for layers inside instances. */
const ID_FIELDS = new Set(['id', 'parentId', 'childIds'])

/** A value with sorted keys and without ID fields, so equal content hashes the same. */
function canonical(value: unknown): unknown {
  if (value === null || typeof value !== 'object') return value
  if (ArrayBuffer.isView(value))
    return createHash('sha256')
      .update(new Uint8Array(value.buffer, value.byteOffset, value.byteLength))
      .digest('hex')
  if (Array.isArray(value)) return value.map(canonical)
  if (value instanceof Map) return [...value.entries()].map(([key, item]) => [key, canonical(item)])
  if (value instanceof Set) return [...value].map(canonical)
  const record = value as { [key: string]: unknown }
  const out: { [key: string]: unknown } = {}
  for (const key of Object.keys(record).sort()) if (!ID_FIELDS.has(key)) out[key] = canonical(record[key])
  return out
}

/** The node and every layer below it, instance contents included, as a hash input. */
function subtree(graph: SceneGraph, node: SceneNode, out: unknown[] = []): unknown[] {
  out.push(canonical(node))
  for (const child of graph.getChildren(node.id)) subtree(graph, child, out)
  return out
}

function hex(color: { r: number; g: number; b: number; a: number }): string {
  const channels = [color.r, color.g, color.b, ...(color.a < 1 ? [color.a] : [])]
  return `#${channels
    .map((value) =>
      Math.round(value * 255)
        .toString(16)
        .padStart(2, '0')
    )
    .join('')
    .toUpperCase()}`
}

function paint(fill: SceneNode['fills'][number]): Json {
  const out: { [key: string]: Json } = { type: fill.type }
  if ('color' in fill && fill.color) out.color = hex(fill.color)
  if (fill.opacity !== undefined && fill.opacity !== 1) out.opacity = round(fill.opacity)
  if (fill.visible === false) out.visible = false
  if ('gradientStops' in fill && fill.gradientStops)
    out.stops = fill.gradientStops.map((stop) => [round(stop.position), hex(stop.color)])
  if ('imageHash' in fill && fill.imageHash) out.image = fill.imageHash
  return out
}

function shownText(graph: SceneGraph, id: string, texts: string[]): string[] {
  for (const child of graph.getChildren(id)) {
    if (!child.visible) continue
    if (child.type === 'TEXT' && child.text) texts.push(child.text)
    shownText(graph, child.id, texts)
  }
  return texts
}

function dump(graph: SceneGraph, node: SceneNode, expand: boolean): Json {
  const out: { [key: string]: Json } = SAVED_ID.test(node.id) ? { id: node.id } : {}
  Object.assign(out, {
    name: node.name,
    type: node.type,
    x: round(node.x),
    y: round(node.y),
    width: round(node.width),
    height: round(node.height)
  })
  if (!node.visible) out.visible = false
  if (node.opacity !== 1) out.opacity = round(node.opacity)
  if (node.rotation) out.rotation = round(node.rotation)
  if (node.componentId) {
    out.component = graph.getNode(node.componentId)?.name ?? node.componentId
    if (SAVED_ID.test(node.componentId)) out.componentId = node.componentId
  }
  if (node.fills.length) out.fills = node.fills.map(paint)
  if (node.strokes.length) {
    out.strokes = node.strokes.map(paint)
    out.strokeWeight = round(node.strokes[0]?.weight ?? 0)
  }
  if (node.effects.length)
    out.effects = node.effects.map((effect) => ({
      type: effect.type,
      radius: effect.radius,
      visible: effect.visible
    }))
  if (node.cornerRadius) out.cornerRadius = round(node.cornerRadius)
  if (node.layoutMode !== 'NONE') {
    out.layout = {
      mode: node.layoutMode,
      gap: node.itemSpacing,
      padding: [node.paddingTop, node.paddingRight, node.paddingBottom, node.paddingLeft],
      primary: node.primaryAxisAlign,
      counter: node.counterAxisAlign,
      sizing: [node.primaryAxisSizing, node.counterAxisSizing]
    }
  }
  if (node.layoutGrow) out.layoutGrow = node.layoutGrow
  if (node.layoutAlignSelf !== 'AUTO') out.layoutAlignSelf = node.layoutAlignSelf
  if (node.layoutPositioning === 'ABSOLUTE') out.absolute = true
  if (node.clipsContent) out.clips = true
  if (node.type === 'TEXT') {
    out.text = node.text
    out.font = [node.fontFamily, node.fontWeight, round(node.fontSize)]
    if (node.lineHeight) out.lineHeight = JSON.parse(JSON.stringify(node.lineHeight)) as Json
    if (node.textAlignHorizontal !== 'LEFT') out.align = node.textAlignHorizontal
  }
  const bound = Object.entries(node.boundVariables)
  if (bound.length)
    out.variables = Object.fromEntries(
      bound.map(([field, id]) => [field, graph.variables.get(id)?.name ?? id])
    )
  if (node.type === 'INSTANCE' && !expand) {
    const texts = shownText(graph, node.id, [])
    if (texts.length) out.texts = texts
    return out
  }
  const children = graph.getChildren(node.id)
  if (children.length) out.children = children.map((child) => dump(graph, child, false))
  return out
}

/** One-line JSON with a space after each comma and colon. */
function oneLine(value: Json): string {
  if (Array.isArray(value)) return `[${value.map(oneLine).join(', ')}]`
  if (typeof value === 'object' && value !== null)
    return `{${Object.entries(value)
      .map(([key, item]) => `${JSON.stringify(key)}: ${oneLine(item)}`)
      .join(', ')}}`
  return JSON.stringify(value)
}

/** JSON with short arrays and flat objects on one line, so diffs stay readable. */
function format(value: Json, indent = ''): string {
  if (typeof value !== 'object' || value === null) return JSON.stringify(value)
  const next = `${indent}  `
  if (Array.isArray(value)) {
    if (value.length === 0 || oneLine(value).length <= 100) return oneLine(value)
    return `[\n${value.map((item) => next + format(item, next)).join(',\n')}\n${indent}]`
  }
  const entries = Object.entries(value)
  if (entries.length === 0) return '{}'
  const flat = entries.every(
    ([, item]) => typeof item !== 'object' || item === null || Object.keys(item).length === 0
  )
  if (flat && oneLine(value).length <= 100) return oneLine(value)
  return `{\n${entries
    .map(([key, item]) => `${next}${JSON.stringify(key)}: ${format(item, next)}`)
    .join(',\n')}\n${indent}}`
}

const io = new IORegistry(BUILTIN_IO_FORMATS)
const graph = await loadDocument(file)
populateWholeDocument(graph)
const page = graph.getPages()[0]
if (!page) throw new Error(`${file} has no pages`)
await applyExportFontPolicy(graph, [page.id], 'PNG', 'warn')

/** Shared by every screen: a new FORMAT or a changed variable re-renders them all. */
const documentHash = createHash('sha256')
  .update(`${FORMAT} ${SCALE}`)
  .update(JSON.stringify(canonical([[...graph.variables.values()], [...graph.variableCollections.values()]])))
  .digest('hex')

const hashOf = (node: SceneNode) =>
  createHash('sha256').update(documentHash).update(JSON.stringify(subtree(graph, node))).digest('hex')

const readList = (path: string): Promise<ScreenEntry[]> =>
  readFile(path, 'utf8')
    .then((text) => JSON.parse(text) as ScreenEntry[])
    .catch(() => [])

const listPath = join(outDir, 'screens.json')
const partsPath = join(outDir, 'parts.json')
const previous = await readList(listPath)
const previousParts = await readList(partsPath)
const fileByFigId = new Map(previous.map((entry) => [entry.figId, entry.file]))
const previousByFile = new Map([...previous, ...previousParts].map((entry) => [entry.file, entry]))

for (const folder of ['fig', 'json', 'screenshots']) await mkdir(join(outDir, folder), { recursive: true })

const paths = (name: string) => ({
  fig: join(outDir, 'fig', `${name}.fig`),
  json: join(outDir, 'json', `${name}.json`),
  png: join(outDir, 'screenshots', `${name}.png`)
})

const exists = (path: string) =>
  access(path).then(
    () => true,
    () => false
  )

/** Writes the file only when its bytes differ; returns whether it did. */
async function writeIfChanged(path: string, data: Uint8Array | string): Promise<boolean> {
  const bytes = typeof data === 'string' ? strToU8(data) : data
  const old = await readFile(path).catch(() => null)
  if (old && Buffer.compare(old, bytes) === 0) return false
  await writeFile(path, bytes)
  return true
}

let rendered = 0

async function exportNode(node: SceneNode, name: string): Promise<ScreenEntry> {
  const hash = hashes.get(node.id) ?? hashOf(node)
  const entry = { file: name, figId: node.id, name: node.name, width: round(node.width), height: round(node.height), hash }
  const files = paths(name)
  const known = previousByFile.get(name)
  const present = await Promise.all(Object.values(files).map(exists))
  if (!all && known?.hash === hash && present.every(Boolean)) return entry

  const target = { scope: 'node' as const, nodeId: node.id }
  const json = { file: name, ...(dump(graph, node, true) as { [key: string]: Json }) }
  const jsonChanged = await writeIfChanged(files.json, `${format(json)}\n`)
  const png = await io.exportContent('png', { graph, target, fileName: `${name}.png` }, { format: 'PNG', scale: SCALE })
  const pngChanged = await writeIfChanged(files.png, png.data)
  // The .fig is not byte-stable between runs, so a screen re-rendered without a content change
  // (no stored hash yet, or --all) keeps the one in git unless its JSON or PNG changed.
  const contentChanged = known?.hash !== undefined && known.hash !== hash
  if (contentChanged || jsonChanged || pngChanged || !(await exists(files.fig))) {
    const fig = await io.exportContent('fig', { graph, target, fileName: `${name}.fig` }, { renderThumbnail: true })
    if (typeof fig.data === 'string') throw new Error(`${name}: the .fig export returned text`)
    await writeIfChanged(files.fig, stableFig(fig.data))
  }
  rendered += 1
  console.log(`${name}  ${node.name}${jsonChanged || pngChanged ? '' : '  (unchanged)'}`)
  return entry
}

/** Every layer on the page that is not inside an instance, so each name has one owner. */
function ownLayers(id: string, out: SceneNode[] = []): SceneNode[] {
  for (const child of graph.getChildren(id)) {
    out.push(child)
    if (child.type !== 'INSTANCE') ownLayers(child.id, out)
  }
  return out
}

const screenNodes = graph
  .getChildren(page.id)
  .filter((node) => (node.type === 'FRAME' || node.type === 'INSTANCE') && !EXCLUDED.test(node.name))

const layers = ownLayers(page.id).filter((node) => node.type !== 'INSTANCE')
const partNodes = previousParts.map(({ file: name, name: layerName }) => {
  const top = graph.getChildren(page.id).filter((node) => node.name === layerName)
  const matches = top.length === 1 ? top : layers.filter((node) => node.name === layerName)
  if (matches.length !== 1)
    throw new Error(`parts.json: ${matches.length} layers are named ${JSON.stringify(layerName)}`)
  return { name, node: matches[0] as SceneNode }
})

// Hash everything before rendering anything: rendering loads fonts and lays text out again,
// which would make the hashes of later screens depend on what was rendered before them.
const hashed = [...screenNodes, ...partNodes.map((part) => part.node)]
const hashes = new Map(hashed.map((node) => [node.id, hashOf(node)]))

const screens: ScreenEntry[] = []
for (const node of screenNodes)
  screens.push(await exportNode(node, fileByFigId.get(node.id) ?? node.id.replace(':', '-')))
await writeIfChanged(listPath, `${JSON.stringify(screens, null, 2)}\n`)
console.log(`${screens.length} screens, ${rendered} rendered`)

const parts: ScreenEntry[] = []
if (partNodes.length) {
  rendered = 0
  for (const { name, node } of partNodes) parts.push(await exportNode(node, name))
  await writeIfChanged(partsPath, `${JSON.stringify(parts, null, 2)}\n`)
  console.log(`${parts.length} parts, ${rendered} rendered`)
}

// Files of screens and parts that are gone from the design or from parts.json.
const kept = new Set([...screens, ...parts].map((entry) => entry.file))
const folders = { fig: '.fig', json: '.json', screenshots: '.png' }
for (const [folder, extension] of Object.entries(folders))
  for (const entry of await readdir(join(outDir, folder)))
    if (entry.endsWith(extension) && !kept.has(entry.slice(0, -extension.length)))
      await rm(join(outDir, folder, entry), { force: true })
