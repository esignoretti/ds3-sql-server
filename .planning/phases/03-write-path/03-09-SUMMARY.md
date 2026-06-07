# Phase 3, Task 9 Summary: Write-executor seam and CTAS/load type routing

**One-liner:** Added `WriteExecutor` interface, `LoadRequest` type, and type-based routing in `Manager.Submit` to dispatch `ctas`/`load` jobs to the write executor path, plus a `LocalWriteExecutor` adapter.

## Changes

### Modified: `internal/job/job.go`
- Added `LoadRequest` struct mirroring `write.LoadRequest` at the job boundary
- Added `Load *LoadRequest` field to `ExecRequest` (set when `Type == "load"`)
- Added `IntoTable string` field to `Job` (records the output table for write jobs)
- Added `write WriteExecutor` field to `Manager` struct
- Added `WriteExecutor` interface with `RunCTAS` and `RunLoad` methods
- Added `SetWriteExecutor(we WriteExecutor)` method
- Updated `Submit` goroutine to route by `req.Type`:
  - `"ctas"` → calls `we.RunCTAS`, sets `IntoTable` on done
  - `"load"` → calls `we.RunLoad`, sets `IntoTable` on done
  - default/`"query"` → existing `m.exec.Execute` path unchanged

### Created: `internal/job/write_executor.go`
- `LocalWriteExecutor` adapting `*write.Writer` to the `WriteExecutor` interface
- Translates `job.LoadRequest` → `write.LoadRequest` in `RunLoad`

### Created: `internal/job/write_executor_test.go`
- `fakeWriteExec` implementing `WriteExecutor` for testing
- `readExec` implementing `Executor` for the query path
- `waitDone` helper for async job completion
- `TestManager_RoutesCTAS` — submits CTAS, asserts it reaches write executor and `IntoTable` is set
- `TestManager_RoutesLoad` — submits load, asserts it reaches write executor with correct `into`

## Key Files
| File | Status |
|------|--------|
| `internal/job/job.go` | modified (types, routing) |
| `internal/job/write_executor.go` | created (adapter) |
| `internal/job/write_executor_test.go` | created (tests) |

## Dependencies
- **Requires:** `write.Writer.RunCTAS` / `RunLoad` (committed in prior task)
- **Affects:** `internal/job/job_async_test.go` (existing async tests must still pass)

## Verification
- `go test -race ./internal/job/` — all 12 tests PASS (3 new + 9 existing)
- Existing tests remain unchanged: `TestSubmit_AsyncCompletes`, `TestSubmit_Cancel`, `TestExecutorFunc_Satisfies`, `TestList_ReturnsRecent`, `TestManager_RunSync`, `TestManager_RunError`, `TestLocalExecutor_EndToEnd`, `TestAdmission_*`, `TestMetastoreSink_PersistsLifecycle`

## Deviations
None — plan executed as written.

## Commit
```
e6e6cf5 feat(job): write-executor seam and ctas/load type routing
```

## Self-Check: PASSED
- Files all exist: `internal/job/job.go`, `internal/job/write_executor.go`, `internal/job/write_executor_test.go`
- Commit e6e6cf5 found in git log
- `go test -race ./internal/job/` — all 12 tests pass
