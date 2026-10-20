# Contract: Feature catalog format

Applies to `FEATURES.md` (main repo root) and `src/content/docs/features.mdx` (website). The sync check ([features-sync-check.md](./features-sync-check.md)) relies on this grammar.

## FEATURES.md

```markdown
# Gameplane features

<intro: what the labels mean (Clarification Q1), status date, link to docs/roadmap.md>

## <Area name>

| Capability | Maturity | Default | Description | Docs |
|---|---|---|---|---|
| <name> | <stable|beta|experimental|planned|in progress> | <on|off|n/a> | <one sentence> | [<text>](<relative link>) |
```

Rules:
- Area headings are level-2 (`## `) and appear in the same order in both files. Level-1 and level-3+ headings are ignored by the check.
- Exactly one catalog table per area, header row exactly as above (column names and order). Any other table in the file is outside an area heading or is ignored only if it does not use this header.
- Cell values for Maturity and Default are lower-case, from the fixed sets, with no extra text or markup.
- Capability cells are plain text: no links, no backticks, no `|`.
- Rows appear in the same order in both files.

## features.mdx (website)

Frontmatter (website content schema):

```yaml
title: Features
description: Every Gameplane capability with its maturity and default.
section: reference
group: releases-roadmap
order: 0
icon: list   # an icon already in src/components/icons.ts
eyebrow: DOCUMENTATION
```

Body: same intro meaning, then the same `##` area headings and tables with the same Capability, Maturity and Default cells. Description and Docs cells may differ (website links are relative docs links per the website CLAUDE.md). MDX imports and components are allowed outside the tables.
