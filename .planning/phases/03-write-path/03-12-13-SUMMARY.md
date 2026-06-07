# Phase 3, Tasks 12-13 Summary: Schedule handlers, CTAS/load routing, managed-aware table drop

**One-liner:** Added schedule CRUD HTTP handlers (with cron validation), routed CTAS/load jobs through `SubmitWithCreds` returning 202, and implemented managed-aware `DropWithDeps` that deletes data objects for managed tables.

## Changes

### Task 12a: Created `internal/api/schedule_handler.go`
- `ScheduleStore` interface (CreateSchedule, ListSchedules, DeleteSchedule)
- `ScheduleHandler` struct with `NewScheduleHandler` constructor
- `CreateForProject` — validates cron expression, computes `NextRunAt`, returns 201
- `ListForProject` — returns schedules JSON array, 200
- `DeleteForProject` — deletes by ID from URL param, 204

### Task 12a: Created `internal/api/schedule_handler_test.go`
- `scheduleStoreStub` in-memory implementation
- `TestScheduleHandler_CreateListDelete` — creates with cron, checks 201 + ID/NextRunAt; bad cron → 400; list returns content

### Task 12b: Modified `internal/api/job_handler.go`
- Replaced `SubmitWithCreds` with version that decodes richer body (type, sql, source, into, format, partition_by, mode)
- Load (`type:"load"`) → async submit via `h.mgr.Submit`, returns 202
- CTAS (detected by `write.IsCTAS`) → async submit, returns 202
- Plain query → existing async submit + wait-poll, with 200/202/400 status as before
- Added `writeJSON` helper for consistent JSON responses
- Existing `Get`, `ListForProject`, `Cancel`, `isTerminal` unchanged

### Task 12b: Modified `internal/api/job_handler_test.go`
- `stubWriteExec` implementing `job.WriteExecutor` for CTAS/load tests
- `TestJobHandler_RoutesCTAS` — submits CTAS SQL, expects 202 + type "ctas"
- `TestJobHandler_RoutesLoad` — submits load JSON, expects 202 + type "load"

### Task 13: Modified `internal/api/table_handler.go`
- Added `DropWithDeps` method accepting `catalog.PrefixDeleter` + `catalog.CacheInvalidator` + creds
- Delegates to `cat.DropTableWithData` (which already handles managed vs external logic)
- Uses `metastore.ErrNotFound` for 404 mapping

### Task 13: Modified `internal/api/table_handler_test.go`
- `apiFakeDeleter` records `DeletePrefix` calls with `"bucket|prefix"` format
- `apiFakeInvalidator` counts `DeleteCacheEntriesForTable` calls
- `TestTableHandler_DropManagedDeletesData` — registers a managed table, drops via `DropWithDeps`, asserts 1 delete call was made

## Key Files

| File | Status |
|------|--------|
| `internal/api/schedule_handler.go` | created |
| `internal/api/schedule_handler_test.go` | created |
| `internal/api/job_handler.go` | modified (SubmitWithCreds routing) |
| `internal/api/job_handler_test.go` | modified (CTAS/load routing tests) |
| `internal/api/table_handler.go` | modified (DropWithDeps) |
| `internal/api/table_handler_test.go` | modified (managed drop test) |

## Dependencies
- **Requires:** `github.com/robfig/cron/v3` (already in go.mod from scheduler task)
- **Requires:** `write.IsCTAS` from Task 5
- **Requires:** `catalog.DropTableWithData`, `catalog.RegisterManagedInput` from Task 6
- **Affects:** All existing API handler tests must still pass

## Verification
- `go test ./internal/api/ -run 'TestJobHandler_RoutesCTAS|TestJobHandler_RoutesLoad|TestJobHandler_SubmitSyncAndGet|TestScheduleHandler_CreateListDelete'` — PASS (4 tests)
- `go test ./internal/api/ -run TestTableHandler_DropManagedDeletesData` — PASS
- `go test ./internal/api/` — all 11 tests PASS
- `go test ./internal/...` — all packages PASS

## Deviations
None — plan executed as written.

## Commits
```
267a4ea feat(api): schedule CRUD and ctas/load job routing
8776ab2 feat(api): managed-aware table drop with data deletion
```

## Self-Check: PASSED
- All 6 files exist and have the expected content
- Both commits found in git log
- `go test ./internal/api/` — all 11 tests pass
- No unintended deletions in commits
- Clean working tree
