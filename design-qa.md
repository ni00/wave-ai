# Wave AI Console design QA

final result: passed

Reference: `/tmp/codex-clipboard-9b1d080b-a2b8-44c9-b7c7-e366b9e3b17d.png` (3442 × 1810). The reference is a visual style/layout guide, not instructions or production data. Desktop comparison used the same aspect ratio at 2048 × 1077; also verified the normal 1280 × 720 layout and a 390 × 844 mobile viewport.

Screenshots on the development host:
- `/home/ni/.local/share/wave-ai/tests/web-console/trace-desktop.png`
- `/home/ni/.local/share/wave-ai/tests/web-console/trace-timeline.png`
- `/home/ni/.local/share/wave-ai/tests/web-console/trace-mobile.png`

The reference and final desktop screenshot were displayed together for direct comparison. The implementation uses actual VPS task/trace data, with a selected root task and a separate tool-span inspection. Retained the light sidebar, compact searchable collection, bordered trace tree, span input/output inspector, and a right-side analysis pane. Wave's deterministic timing overview replaces the reference's unrelated AI chat. Resource names and navigation reflect Wave's eight requested modules.

Visual decisions: system sans fonts, local monospace code, Tabler outline icons, neutral borders, restrained radii, purple model / blue agent / amber tool identifiers, green plain-text outputs, no remote fonts or decorative raster assets. Follow-up iteration improved secondary-text contrast and increased list/code text sizes. On smaller screens, an Overview tab preserves timing analysis and the list/detail flow uses one main pane; sidebar controls and disconnect remain reachable.

Browser checks used the in-app browser and the production Go-served build, authenticated with the existing VPS Wave key through a loopback SSH tunnel. Verified real trace tree, span selection, task input/output, timeline, overview-to-longest-span navigation, logs and event detail, log pagination, Environment backend/profile, CSV search and content preview, Session messages and task links, Task tools, Memory empty state, imported Bench reports, and incompatible-baseline feedback. Final production-browser console inspection returned no warnings or errors. Reset the temporary viewport override after testing.

Fixes found during verification: GORM subquery parentheses; TypeScript SDK browser fetch receiver and explicit JSON transport; live benchmark reports' server-managed `workers: 0`; file `mime_type` and message `text` field mappings; narrow-screen disconnect/sidebar access and Overview span navigation. All relevant fixes were rebuilt and checked.

Validation: full PostgreSQL/S3 integration suite with race detection; targeted console ownership, cursor, report-import and static-asset tests; SDK/CLI contract tests and generation checks; frontend production build and seven tests, including a 20,000-level trace tree without recursion overflow. Memory populated entries/history/conflicts use the existing paginated API; the VPS currently has no memory stores, so browser testing of that module covers its actual empty state.

Performance observations are for the existing small VPS dataset, not a capacity claim: first-load JS ~91.7 kB gzip, Trace chunk ~12.0 kB gzip, other detail chunk ~6.0 kB gzip. Ten sequential loopback reads per resource gave median API times of 1.4–3.0 ms; SSH-tunnel reads were ~127–133 ms. Lists are metadata projections with bounded cursor pages; details load on demand and trace rows are virtualized.

## Monitoring update — 2026-09-15

- Navigation now groups Monitoring (Overview / Metrics / Traces / Logs), resources, and Bench.
- Logs uses a searchable table with severity/module/time filters and a detail drawer. Events live under Task and Session. Removed the inherited generic `.error` block styling from severity badges.
- Verified on a local PostgreSQL-backed fixture environment: metric point → completed/failed Task filter → Events payload → task Logs → selected model Span → span-filtered Logs. These fixtures are test data, not performance claims or production history.
- Checked 1-hour, 7-day, and 30-day metric ranges, mergeable rollups, keyboard chart selection, desktop 1280×720 and mobile 390×844. No page-wide horizontal overflow at mobile width. Long log tables scroll within their own panel.
- Browser console: no errors or warnings. Evidence: `/home/ni/.local/share/wave-ai/tests/monitoring/metrics-desktop.png`.
- Validation: full PostgreSQL/SeaweedFS integration suite with race detector; SDK/CLI generation, contract checks and SDK tests; Go vet/build; frontend build and tests. Targeted tests cover concurrent aggregation, rollback/retry, owner isolation, log search/pagination, completion-time drilldown, histogram merging, retention and log metadata filtering.
