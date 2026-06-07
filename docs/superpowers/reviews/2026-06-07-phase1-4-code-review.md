# DS3 SQL — Phase 1–4 Code Review & Remediation

**Date:** 2026-06-07
**Scope:** Full review of the implemented Phases 1–4 (catalog, async jobs, caching, coordinator/worker, write path, scheduler, partition pruning, Postgres metastore, Web catalog UI).
**Spec:** [`../specs/2026-06-07-ds3-sql-bigquery-refactor-design.md`](../specs/2026-06-07-ds3-sql-bigquery-refactor-design.md)
**Plans:** [`../plans/2026-06-07-ds3-sql-phase1-foundations.md`](../plans/2026-06-07-ds3-sql-phase1-foundations.md) … `phase4-polish.md`

## Method

Health baseline: `go build ./...` ✓, `go vet ./...` ✓, `go test ./...` ✓ (all packages pass), `gofmt -l` reports several unformatted files. Four domain-partitioned review agents (security/multi-tenancy, core+concurrency, write path, Phase 4 + cross-cutting) produced findings; every CRITICAL and HIGH below was then **verified by hand** against the cited code.

## Verdict

Strong implementation that matches the spec's architecture, with green tests. **Not release-ready for multi-tenant use:** confirmed cross-tenant data-isolation holes (jobs/schedules/reports/managed-table paths), one silent data-correctness bug (`load overwrite` never deletes), and a partition-pruning correctness bug. All are fixable and mostly small.

---

## 🔴 CRITICAL

### C1 — Jobs are not project-scoped (IDOR)
- **Where:** `internal/api/job_handler.go:129` (`Get`), `:149` (`Cancel`) → `internal/job/job.go:237` (`Manager.Get`), `:250` (`Manager.Cancel`).
- **What:** `GET /jobs/{id}` and `DELETE /jobs/{id}` look up purely by UUID; neither compares the caller's `session.Projects` to `job.ProjectID`.
- **Impact:** Any authenticated tenant who knows/guesses a job UUID can read another project's full `Job.Result` (actual result rows) and `SQL`, or cancel a running job (DoS). Cross-tenant data leakage.
- **Fix:** Thread the caller's project set into `Get`/`Cancel`; return 404 unless `job.ProjectID` is one of the caller's projects.

### C2 — Schedule delete is not project-scoped (IDOR)
- **Where:** `internal/api/schedule_handler.go` (`DeleteForProject` receives `projectID` but does not use it) → `internal/metastore/sqlite.go` `DeleteSchedule` (`DELETE FROM schedules WHERE id = ?`).
- **What:** No ownership predicate on delete.
- **Impact:** Any tenant can delete any project's scheduled query by ID.
- **Fix:** `DELETE FROM schedules WHERE id = ? AND project_id = ?` (and the Postgres equivalent); pass `projectID` through.

### C3 — Reports are entirely unscoped (IDOR) — *pre-existing, now in scope*
- **Where:** `internal/api/report_handler.go` (`List`/`Get`/`Delete`/`Save`), `internal/report/store.go` (`List`/`Get`/`Delete` by id only).
- **What:** `List()` returns **all** projects' reports; `Get`/`Delete` are by id only; `Save` reads `ProjectID` from the **request body**, not the session.
- **Impact:** Reports embed `QueryRows` (actual data). Any tenant can list/read/delete every other tenant's reports.
- **Fix:** Derive `ProjectID` from the session; filter `List`/`Get`/`Delete` by the caller's project(s); add a project column/filter to the report store.

### C4 — Managed-table location omits the project ID (cross-project collision)
- **Where:** `internal/write/write.go:71` — `managedLocation` returns `s3://<bucket>/_managed/<dataset>/<table>/`.
- **What:** Storage-class buckets are server-level config shared across all tenants; the path has no `projectID`.
- **Impact:** Project A's `sales.orders` and Project B's `sales.orders` resolve to the **same** prefix → CTAS/load clobber each other's data; `catalog.DropTableWithData` deletes the other tenant's files. Data corruption / isolation breach.
- **Fix:** Namespace the prefix by project, e.g. `_managed/<projectID>/<dataset>/<table>/`; update the `splitS3`-based delete path accordingly.

### C5 — `load overwrite` never deletes prior data in production
- **Where:** `cmd/ds3sql-server/main.go:284` (`write.NewWriter(..., nil /* deleter bound per-call */)`), `internal/write/load.go:81` (`if mode == "overwrite" && w.deleter != nil`).
- **What:** Production constructs the `Writer` with a `nil` deleter, and `RunLoad` has no mechanism to bind a per-call deleter, so the overwrite branch is always skipped. A new `overwrite-<ts>/` subdir is written and the probe reader globs both old and new data.
- **Impact:** `overwrite` behaves like `append` — old rows never removed → wrong row counts / query results. The unit test injects a fake `recordingDeleter`, masking the bug.
- **Fix:** Wire a credential-bound deleter (mirror `credsDeleter` in `main.go`) into the write path per call; make a `nil` deleter on `overwrite` a hard error, not a silent skip.

---

## 🟠 HIGH

### H1 — `?project=` selector ignored on catalog/table/job/schedule routes
- **Where:** `cmd/ds3sql-server/main.go:417–524` — each closure does `for _, p := range session.Projects { handler(p.ProjectID); return }`, always using `Projects[0]`. Only `/query`, `/schema`, `/buckets`, `/ui/catalog` honor `r.URL.Query().Get("project")`.
- **Impact:** Multi-project users have all catalog/table/job/schedule operations silently routed to their first project regardless of selection (wrong-project reads and writes). Not a cross-tenant breach (still the caller's own projects), but a real correctness bug and a scoping-consistency hazard.
- **Fix:** Apply the same `projectID := r.URL.Query().Get("project")` + `if projectID == "" || p.ProjectID == projectID` match used by `/query` to every project-scoped route (the Phase 1 plan's `projectCreds(r)` helper).

### H2 — Partition pruning uses lexicographic comparison
- **Where:** `internal/planner/prune.go:162` (`matches`) — `OpGt/OpGte/OpLt/OpLte` use Go string `<`/`>` on partition values.
- **What:** Partition values are `map[string]string`. For numeric partition columns, `WHERE year < '2025'` evaluates `"999" > "2025"` (lexical), dropping the `year=999` partition that should match.
- **Impact:** **Incorrect (incomplete) query results** for range predicates on numeric partition columns. ISO-date partitions are lexicographically safe, which is why current tests pass.
- **Fix:** When both operands parse as numbers, compare numerically; otherwise string-compare. (`=`/`IN` are unaffected.)

### H3 — Admission slot permanently leaked on cancel/wake race
- **Where:** `internal/job/job.go:325` (`acquire`), `:358` (`wakeNext`), `:404` (`cancelWaiter`); `defer release` registered only at `:149` after `acquire` returns true.
- **What:** When a waiter is woken, `wakeNext` increments `inUse[p]` and `close(ch)` under lock. If the waiter's `ctx` is cancelled at the same instant, the `select` may pick `ctx.Done()` (Go chooses randomly when both are ready), call `cancelWaiter` which can't find the already-dequeued `ch`, and return `false` — so the slot `wakeNext` accounted for is never released.
- **Impact:** Each occurrence permanently shrinks a project's effective concurrency; repeated cancellations can drive it to 0 → all future jobs for that project hang in `queued` forever.
- **Fix:** In `acquire`, when `ctx.Done()` fires, detect whether the slot was already granted (e.g. have `cancelWaiter` report whether it removed the waiter; if it did not, release the granted slot).

### H4 — S3 credentials/endpoint concatenated into DuckDB SQL without escaping
- **Where:** `internal/query/engine.go:72–94` (`applyS3Creds`).
- **What:** `accessKey`, `secretKey`, derived `endpoint` are concatenated into `CREATE OR REPLACE SECRET … KEY_ID '…' SECRET '…' ENDPOINT '…'` and `SET s3_*` with no quote-escaping; all `db.Exec` results are discarded.
- **Impact:** A single quote breaks out of the literal and injects DuckDB SQL on the shared pooled connection (which can read local files / the local-SSD data cache → cross-tenant escalation). Creds are IAM-issued (lower likelihood), so this is primarily a defense-in-depth failure; the swallowed errors also hide misconfiguration.
- **Fix:** Escape each value (`strings.ReplaceAll(v, "'", "''")`) before concatenation; check the `Exec` errors.

### H5 — Worker data-plane: plaintext credentials, non-TLS, weak secret check
- **Where:** `cmd/ds3sql-server/main.go` (`srv.ListenAndServe`, worker route on the same router), `internal/worker/server.go:49` (`!= s.secret`), `internal/config/config.go` (`SharedSecret: ""` default).
- **What:** `/internal/execute` accepts SQL + S3 credentials in a cleartext JSON body over a non-TLS listener on the same router as public routes; the shared-secret comparison is non-constant-time; default secret is empty.
- **Impact:** Tenant S3 credentials are exposed on the coordinator→worker link to anyone on the network path; timing side-channel on the secret; a weak/guessable secret yields arbitrary SQL execution with attacker-supplied creds.
- **Fix:** Require TLS (ideally mTLS) for the worker link or bind it to a private interface; use `crypto/subtle.ConstantTimeCompare`; fail startup in `role=worker` when `SharedSecret` is empty.

### H6 — Cancelled/terminal job status not persisted to history
- **Where:** `internal/job/job.go` — the `Submit` goroutine calls `m.save(ctx, …)` with the job's own cancellable context for the final terminal transition.
- **What:** On cancellation `ctx` is already `Done`, so `UpdateJob`'s `ExecContext(ctx, …)` returns `context.Canceled`, which is swallowed; the persisted record stays `queued`/`running`.
- **Impact:** Query history is missing/stale for cancelled (and any terminal-save-racing-cancel) jobs — violates the Phase 2 history requirement.
- **Fix:** Persist terminal transitions with `context.WithoutCancel(parent)` (or a fresh background context).

---

## 🟡 MEDIUM

- **M1 — Partition pruning is inert in production.** `RegisterManaged`/`RegisterTable` set `Stats: {RowCount}` only; `Stats.Partitions` is never populated (`SaveTablePartitions` has no non-test caller), so `ResolvePruned`'s `len(Stats.Partitions) > 0` gate is always false → always full-scan glob. `internal/catalog/service.go:119,328,225`. *Fix:* enumerate written Hive partitions during managed-table registration, or document as not-yet-wired.
- **M2 — Scheduler `Running` can stick true.** The enqueuer only clears `Running` on `done`/`failed`; a `cancelled` job or one evicted by `jobManager.Cleanup(30m)` leaves `Running=true` forever → schedule never fires again. No startup reset of `running`. `cmd/ds3sql-server/main.go:90–100`. *Fix:* break the poll loop on `!ok` or any terminal status (incl. `cancelled`) and clear `Running`; reset all `running=0` on startup.
- **M3 — Scheduler double-fire window.** Completion clears `running` before writing `next_run_at`; a concurrent `Tick` can re-select the schedule. *Fix:* write `next_run_at` before clearing `running`, or update both atomically.
- **M4 — Scheduled jobs lack credentials / can't run `load`.** `Enqueue` submits with empty creds and only routes `query`/`ctas`. `cmd/ds3sql-server/main.go:80–89`. *Fix:* resolve owner credentials for scheduled runs; route `load`; surface missing-creds failures.
- **M5 — DataCache size counter double-counted.** Concurrent `Ensure` for the same uncached key both add to `c.total`. `internal/cache/data.go:80–110`. *Fix:* re-check membership under the second lock; only add when absent.
- **M6 — Result-cache eviction non-atomic.** `Put`→`evictLRU` runs list+sum+delete as separate statements; concurrent `Put`s over-evict below `MaxBytes`. `internal/cache/result.go:132–183`. *Fix:* wrap in a transaction or guard with a mutex.
- **M7 — Duration config fields unparseable in human form.** `ResultTTL`, `TokenExpiry`, `RefreshTokenExpiry` are `time.Duration`; `yaml.v3` only decodes integer nanoseconds, so `result_ttl: 1h` hard-fails `Load`. `internal/config/config.go`. *Fix:* parse via a string field + `time.ParseDuration` or a custom `UnmarshalYAML`.
- **M8 — CTAS/load partial-failure inconsistency.** If `ExecWrite` succeeds but `RegisterManaged` fails → orphaned data; if `BumpDataVersion` fails, `afterWrite` early-returns and skips cache invalidation → stale cached results against new data. `internal/write/ctas.go`, `load.go`, `write.go:76`. *Fix:* invalidate cache even if bump errors; clean up the written prefix on registration failure (or GC).
- **M9 — Stale result-cache hits via regex table matching.** Cache key includes only versions of tables matched by `referencesTable` (regex over raw SQL), which can miss (view alias/non-default quoting) or match inside string literals/comments. `internal/catalog/service.go:166`. *Fix:* strip string literals/comments before matching (or parse), and ensure every `ViewBinding` table contributes to the version map.
- **M10 — Postgres parity nits.** `GetDueSchedules` missing `ORDER BY next_run_at` (`postgres.go:561`); `DeleteCacheEntriesForTable` LIKE-escaping diverges from SQLite (`postgres.go:454` vs `sqlite.go:435`). Safe today (validated identifiers) but latent. *Fix:* mirror ordering and escaping.
- **M11 — Catalog routes ignore `?project=` (same root as H1) for multi-project users; also session map has no size cap / only lazy TTL purge** (`internal/auth/session.go:25`).

---

## ⚪ LOW

- **L1 — gofmt:** unformatted changed files — `internal/api/job_handler.go`, `internal/api/table_handler.go`, `internal/job/job.go`, `internal/scheduler/scheduler_test.go`, `internal/write/ctas_test.go`, `cmd/ds3sql-server/main.go`, `cmd/ds3sql/query.go` (plus pre-existing). *Fix:* `gofmt -w` the tree; consider a CI check.
- **L2 — JSON error bodies built by string concat** (`{"error":"`+err.Error()+`"}`) across handlers — a `"` in the message breaks the JSON. *Fix:* use the existing `writeJSON` helper.
- **L3 — `BumpDataVersion` non-atomic** (UPDATE then separate SELECT). Monotonicity is preserved; returned value may reflect a concurrent bump. *Fix:* `RETURNING data_version`.
- **L4 — Dead unsafe `DropForProject` handler** (`internal/api/table_handler.go:102`) — not routed, but drops without data delete/cache invalidation. *Fix:* remove or redirect to `DropTableWithData`.
- **L5 — Swallowed errors:** `applyS3Creds` Execs, numeric env-override parse errors (`DS3SQL_POOL_SIZE=abc` ignored silently). *Fix:* log/propagate.
- **L6 — Stale duplicate tree** at `Server/internal/...` in the repo root (not built; matches nothing the module compiles). *Fix:* delete to avoid confusion.
- **L7 — Missing env overrides** for `Query.MaxRows`/`MaxExecutionSecs`/`MaxResultBytes` (cache has them; query doesn't) — confirm against intent.

---

## Verified strengths (do not regress)

- **Result-cache keys are project-scoped** — hash includes `projectID` + referenced-table data-versions; cache/data blob filenames are sha256 → no cross-tenant reads, no path traversal. `internal/cache/result.go`, `data.go`.
- **DuckDB view cleanup is reliable** — `QueryView`/`ExecWrite` always `DROP SCHEMA … CASCADE` in `defer`, including error paths → no view/identifier leakage across pooled connections. `internal/query/engine.go`.
- **Pool connections never lost** — returned via `defer e.pool <- db` on all paths; `drainPool` closes opened connections on init failure.
- **Pruner is conservative** for `=`/`IN`/`AND` and falls back to all partitions on `OR`/unknown predicates and missing values (range comparison is the lone unsafe case — H2).
- **Postgres store is complete** — implements every `Store` method (no stubs/panics; `var _ Store` holds); shared conformance suite runs SQLite always + Postgres gated on `DS3SQL_TEST_POSTGRES_DSN`.
- **No XSS** — web fragments route through `escHtml`/`escAttr` + `html/template`; catalog identifiers constrained to `^[a-zA-Z_][a-zA-Z0-9_]*$`.
- **Identifier injection blocked** — dataset/table names validated before use in `quoteIdent`'d DDL; CTAS grammar enforces the same shape; metastore SQL uses placeholders; `DropTableWithData` deletes only the table's own validated prefix.

---

## Recommended remediation order

1. **Isolation + data-correctness blockers (must fix before any multi-tenant deploy):** C1, C2, C3, C4, C5, H1. The IDOR fixes (C1–C3) and H1 share one pattern — thread the selected `projectID` through and check ownership before acting.
2. **Correctness & stability:** H2 (numeric pruning), H3 (slot leak), H6 (history), M2/M3/M4 (scheduler).
3. **Security hardening:** H4 (escape creds), H5 (worker TLS/secret).
4. **Cache & config robustness:** M5, M6, M7, M8, M9, M10.
5. **Hygiene:** L1 (gofmt + CI), then the remaining LOW items. Consider wiring M1 (populate `Stats.Partitions`) so pruning actually engages, or explicitly mark it deferred.

Suggested approach: one failing test per bug, then the fix (TDD), committing per item.
