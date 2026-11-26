# Contract: shared UI rules (design.fig and web)

These hold for every screen, not just the four reviewed ones.

| Rule | Value |
|---|---|
| Corner shape | 12 px for buttons, inputs, tabs, segmented controls, cards. Full pill only for the page's single main action and small labels (status pill, count badge, chip). |
| Tab lists | Horizontal tabs size to their label and never wrap. Selected highlight 12 px. |
| Tables | One frame per table. |
| Status pill | Text size of its row, leading dot: green Running/Succeeded, grey Stopped/Pending/Suspended, violet sleep colour Asleep (OD-9), amber Starting/Stopping/Suspending/Resuming, red Failed. |
| Sliders | Track filled in the accent colour from start to handle, shared style. |
| Colours | Theme tokens only, defined for dark and light. |

## Colour tokens

| Use | Token | Colour |
|---|---|---|
| Running card | `--success` | green |
| Players card, Info level | `--stat-players` / `--level-info` | blue |
| vCPUs card | `--chart-stat` | violet |
| Storage card | `--stat-storage` | teal |
| Nodes card, Debug level | `--stat-nodes` / `--level-debug` | slate |
| Warn level, card warnings | `--warning` | amber |
| Error level | `--danger` | red |
| Row action hover | Start `--success`, Stop `--danger`, Restart `--foreground` | |
