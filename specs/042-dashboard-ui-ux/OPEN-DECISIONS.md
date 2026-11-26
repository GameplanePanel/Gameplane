# Open decisions: 042 dashboard UI and UX

All rulings are valgul's, in the spec 042 thread.

| ID | Question | Ruling | Date |
|----|----------|--------|------|
| OD-1 | Servers summary card colours | Running green, Players blue, vCPUs violet, Storage teal, Nodes slate; warnings as amber badge + sub-text, icon colour fixed ("yes" to the list) | 2026-11-26 |
| OD-2 | Servers row actions | Full contrast; hover Start green, Stop red, Restart neutral ("yes") | 2026-11-26 |
| OD-3 | Status pill scope | Bigger + coloured dot, changed everywhere incl. Dashboard ("yes") | 2026-11-26 |
| OD-4 | Corner shapes | 12 px corners (option B); full pill only for "important" things or things that are good as pills: main page action + small labels; status tabs 12 px ("B", then the rule, then "yes") | 2026-11-26 |
| OD-5 | Backup "real size" | Data size measured at backup time; space used as second line where known ("yes") | 2026-11-26 |
| OD-6 | Logs toolbar | Colour levels (Info, Debug, Warn, Error), fix the root cause of the source switch look ("add color to info, debug, etc... and fix the reason why the type of log pod / game look wrong", then "k") | 2026-11-26 |
| OD-7 | Resources | Slider fill + wider number box + no unit in labels + Storage size as number + unit ("yes") | 2026-11-26 |
| OD-8 | How to show backups taken before the change (no data size recorded) | **OPEN**. Proposed: label their size "volume size", leave them out of the data-size total, and say how many backups the total covers (spec FR-033) | — |
