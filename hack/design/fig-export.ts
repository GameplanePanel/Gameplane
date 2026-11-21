// Exports every screen of a design .fig as its own .fig, a JSON property dump and a PNG.
//
// It runs on OpenPencil's headless CLI internals, so it runs from an OpenPencil checkout
// (https://github.com/ValgulNecron/open-pencil, master), not from this repository:
//
//   cp hack/design/fig-export.ts <open-pencil>/packages/cli/fig-export.ts
//   cd <open-pencil>/packages/cli
//   bun fig-export.ts <gameplane>/design.fig <gameplane>/assets/design-export
//   bun fig-export.ts <gameplane>/telemetry-receiver/telemetry-dashboard.fig \
//     <gameplane>/telemetry-receiver/design-export
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
// and the script fills in its ID and size. A name that matches no layer, or more than one, stops it.
import { mkdir, readFile, writeFile } from 'node:fs/promises'
import { join } from 'node:path'

import { BUILTIN_IO_FORMATS, IORegistry } from '@open-pencil/core/io'
import type { SceneGraph, SceneNode } from '@open-pencil/scene-graph'

import { loadDocument, populateWholeDocument } from '#cli/headless'

import { applyExportFontPolicy } from './src/commands/export/font-policy'

const EXCLUDED =
  /^(Note —|Row \d+ ·|HeroUI: Design System Components$|Gameplane: App Components$|Shared Components Band$|Reference frame$|dashboard-utility$)/
const SCALE = 2
/** Layers inside an instance get a fresh ID on each load; only saved records keep theirs. */
const SAVED_ID = /^[01]:\d+$/

interface ScreenEntry {
  file: string
  figId: string
  name: string
  width: number
  height: number
}

type Json = string | number | boolean | null | Json[] | { [key: string]: Json }

const [file, outDir] = process.argv.slice(2)
if (!file || !outDir) throw new Error('usage: bun fig-export.ts <design.fig> <export dir>')

const round = (value: number) => Math.round(value * 100) / 100

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

const listPath = join(outDir, 'screens.json')
const previous: ScreenEntry[] = await readFile(listPath, 'utf8')
  .then((text) => JSON.parse(text) as ScreenEntry[])
  .catch(() => [])
const fileByFigId = new Map(previous.map((entry) => [entry.figId, entry.file]))

for (const folder of ['fig', 'json', 'screenshots']) await mkdir(join(outDir, folder), { recursive: true })

async function exportNode(node: SceneNode, name: string): Promise<ScreenEntry> {
  const target = { scope: 'node' as const, nodeId: node.id }
  const json = { file: name, ...(dump(graph, node, true) as { [key: string]: Json }) }
  await writeFile(join(outDir, 'json', `${name}.json`), `${format(json)}\n`)
  const png = await io.exportContent('png', { graph, target, fileName: `${name}.png` }, { format: 'PNG', scale: SCALE })
  await writeFile(join(outDir, 'screenshots', `${name}.png`), png.data)
  const fig = await io.exportContent('fig', { graph, target, fileName: `${name}.fig` }, { renderThumbnail: true })
  await writeFile(join(outDir, 'fig', `${name}.fig`), fig.data)
  console.log(`${name}  ${node.name}`)
  return { file: name, figId: node.id, name: node.name, width: round(node.width), height: round(node.height) }
}

const screens: ScreenEntry[] = []
for (const node of graph.getChildren(page.id)) {
  if ((node.type !== 'FRAME' && node.type !== 'INSTANCE') || EXCLUDED.test(node.name)) continue
  screens.push(await exportNode(node, fileByFigId.get(node.id) ?? node.id.replace(':', '-')))
}
await writeFile(listPath, `${JSON.stringify(screens, null, 2)}\n`)
console.log(`${screens.length} screens`)

/** Every layer on the page that is not inside an instance, so each name has one owner. */
function ownLayers(id: string, out: SceneNode[] = []): SceneNode[] {
  for (const child of graph.getChildren(id)) {
    out.push(child)
    if (child.type !== 'INSTANCE') ownLayers(child.id, out)
  }
  return out
}

const partsPath = join(outDir, 'parts.json')
const wanted: Pick<ScreenEntry, 'file' | 'name'>[] = await readFile(partsPath, 'utf8')
  .then((text) => JSON.parse(text) as ScreenEntry[])
  .catch(() => [])
if (wanted.length) {
  const layers = ownLayers(page.id).filter((node) => node.type !== 'INSTANCE')
  const parts: ScreenEntry[] = []
  for (const { file: name, name: layerName } of wanted) {
    const matches = layers.filter((node) => node.name === layerName)
    if (matches.length !== 1)
      throw new Error(`parts.json: ${matches.length} layers are named ${JSON.stringify(layerName)}`)
    parts.push(await exportNode(matches[0] as SceneNode, name))
  }
  await writeFile(partsPath, `${JSON.stringify(parts, null, 2)}\n`)
  console.log(`${parts.length} parts`)
}
