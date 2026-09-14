# Console QA record

Last verified: 2026-09-15. Result: passed.

| Area | Verified coverage |
| --- | --- |
| Layout | Desktop 1280 × 720 and 2048 × 1077; mobile 390 × 844; no page-wide overflow |
| Traces | Call tree, span selection, timeline, timing analysis, child tasks, logs |
| Monitoring | 1-hour, 7-day, and 30-day ranges; keyboard selection; interval → failed task → trace → logs |
| Resources | File preview/search/download, environment profiles, session messages, task events/tools |
| Benchmarks | Report import and incompatible-baseline feedback |
| Memory | Empty state; populated entries, revisions, and conflicts were not browser-tested |
| Browser diagnostics | No warnings or errors |

Validation included frontend build/tests, SDK/CLI contract checks, Go vet/build,
and PostgreSQL/object-storage integration tests with race detection. Monitoring
tests covered concurrent aggregation, rollback/retry, retention, owner isolation,
log pagination, and completion-time filters. Trace tests covered a 20,000-level
tree. Small-fixture checks do not establish production capacity.

See [console development](web/README.md#develop-the-frontend) for validation commands.
