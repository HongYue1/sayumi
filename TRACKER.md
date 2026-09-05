# Backend review tracker

Overall goal, current task, and checkpoint/resume instructions: `TASK.md`.

Scope: Go source, tests, and backend build/tooling configuration. Embedded fonts,
icons, compiled resources, generated frontend output, and `.skills/` are excluded.

- `[ ]` Pending: full review or verification remains.
- `[x]` Done: fully reviewed; required fixes and tests are complete and verified.
- A passing test run alone does not mark a file reviewed.
- Keep this file to current status only: no change history, findings, or benchmark results.

## CLI and server entry point

- [x] `cmd/sayumi/cli.go`
- [x] `cmd/sayumi/cli_test.go`
- [x] `cmd/sayumi/debug.go`
- [x] `cmd/sayumi/debug_test.go`
- [x] `cmd/sayumi/main.go`
- [x] `cmd/sayumi/main_bench_test.go`
- [x] `cmd/sayumi/main_response_test.go`
- [x] `cmd/sayumi/main_test.go`
- [x] `cmd/sayumi/prettylog.go`
- [x] `cmd/sayumi/prettylog_bench_test.go`
- [x] `cmd/sayumi/prettylog_contract_test.go`
- [x] `cmd/sayumi/prettylog_test.go`

## HTTP API

- [ ] `internal/api/auth.go`
- [ ] `internal/api/auth_test.go`
- [ ] `internal/api/bookedit.go`
- [ ] `internal/api/bookedit_test.go`
- [ ] `internal/api/bookmarks.go`
- [ ] `internal/api/bookresponse_test.go`
- [ ] `internal/api/books.go`
- [ ] `internal/api/books_test.go`
- [ ] `internal/api/chapters.go`
- [ ] `internal/api/chapters_test.go`
- [ ] `internal/api/context.go`
- [ ] `internal/api/customthemes.go`
- [ ] `internal/api/customthemes_test.go`
- [ ] `internal/api/download.go`
- [ ] `internal/api/download_test.go`
- [ ] `internal/api/flairs.go`
- [ ] `internal/api/flairs_test.go`
- [ ] `internal/api/fonts.go`
- [ ] `internal/api/gofile.go`
- [ ] `internal/api/gofile_test.go`
- [ ] `internal/api/library.go`
- [ ] `internal/api/library_test.go`
- [ ] `internal/api/middleware.go`
- [ ] `internal/api/middleware_test.go`
- [ ] `internal/api/middleware/gzip.go`
- [ ] `internal/api/middleware/gzip_test.go`
- [ ] `internal/api/presets.go`
- [ ] `internal/api/presets_test.go`
- [ ] `internal/api/profilemgr.go`
- [ ] `internal/api/profilemgr_test.go`
- [ ] `internal/api/progress.go`
- [ ] `internal/api/progress_coalescer.go`
- [ ] `internal/api/progress_coalescer_test.go`
- [ ] `internal/api/resources.go`
- [ ] `internal/api/router.go`
- [ ] `internal/api/router_test.go`
- [ ] `internal/api/search.go`
- [ ] `internal/api/search_test.go`
- [ ] `internal/api/setting.go`
- [ ] `internal/api/setting_test.go`
- [ ] `internal/api/throttle.go`
- [ ] `internal/api/throttle_test.go`
- [ ] `internal/api/upload.go`
- [ ] `internal/api/upload_handler_test.go`
- [ ] `internal/api/upload_test.go`
- [ ] `internal/api/version.go`
- [ ] `internal/api/version_test.go`

## EPUB processing

- [ ] `internal/epub/chapter.go`
- [ ] `internal/epub/chapter_cache_test.go`
- [ ] `internal/epub/chapter_test.go`
- [ ] `internal/epub/edit.go`
- [ ] `internal/epub/edit_test.go`
- [ ] `internal/epub/parser.go`
- [ ] `internal/epub/parser_test.go`
- [ ] `internal/epub/reader.go`
- [ ] `internal/epub/reader_test.go`
- [ ] `internal/epub/sanitize.go`
- [ ] `internal/epub/sanitize_test.go`
- [ ] `internal/epub/search.go`
- [ ] `internal/epub/search_test.go`
- [ ] `internal/epub/store.go`
- [ ] `internal/epub/store_concurrency_test.go`
- [ ] `internal/epub/store_test.go`

## Fonts

- [ ] `internal/fonts/embed.go`
- [ ] `internal/fonts/embed_test.go`
- [ ] `internal/fonts/metrics.go`
- [ ] `internal/fonts/metrics_test.go`
- [ ] `internal/fonts/scan.go`
- [ ] `internal/fonts/scan_test.go`
- [ ] `internal/fonts/sfnt.go`

## Library

- [ ] `internal/library/cover.go`
- [ ] `internal/library/cover_test.go`
- [ ] `internal/library/scanner.go`
- [ ] `internal/library/scanner_test.go`

## Storage

- [x] `internal/storage/bench_test.go`
- [x] `internal/storage/book_operations_bench_test.go`
- [x] `internal/storage/bookcache.go`
- [x] `internal/storage/bookcache_concurrency_test.go`
- [x] `internal/storage/bookcache_test.go`
- [x] `internal/storage/bookmarks.go`
- [x] `internal/storage/bookmarks_test.go`
- [x] `internal/storage/books.go`
- [x] `internal/storage/books_test.go`
- [x] `internal/storage/cascade_test.go`
- [x] `internal/storage/cover_test.go`
- [x] `internal/storage/customthemes.go`
- [x] `internal/storage/customthemes_bench_test.go`
- [x] `internal/storage/customthemes_test.go`
- [x] `internal/storage/db.go`
- [x] `internal/storage/db_test.go`
- [x] `internal/storage/errors.go`
- [x] `internal/storage/flairs.go`
- [x] `internal/storage/flairs_bench_test.go`
- [x] `internal/storage/flairs_test.go`
- [x] `internal/storage/presets.go`
- [x] `internal/storage/presets_bench_test.go`
- [x] `internal/storage/presets_test.go`
- [x] `internal/storage/profiles.go`
- [x] `internal/storage/profiles_test.go`
- [x] `internal/storage/progress.go`
- [x] `internal/storage/progress_test.go`
- [x] `internal/storage/queryplan_test.go`
- [x] `internal/storage/reading_state_bench_test.go`
- [x] `internal/storage/scan_bench_test.go`
- [x] `internal/storage/sessions.go`
- [x] `internal/storage/sessions_test.go`
- [x] `internal/storage/settings.go`
- [x] `internal/storage/settings_test.go`
- [x] `internal/storage/storage_test.go`

## Tooling

- [ ] `.gitattributes`
- [x] `.gitignore`
- [x] `.golangci.yml`
- [ ] `.github/workflows/ci.yml`
- [ ] `.github/workflows/go-toolchain-bump.yml`
- [ ] `.github/workflows/release.yml`
- [x] `Makefile`
- [x] `build.sh`
- [x] `check.sh`
- [x] `fix.sh`
- [ ] `release.sh`
- [ ] `go.mod`
- [ ] `go.sum`
- [ ] `cmd/sayumi/winres/winres.json`

## Completion gates

- [ ] Every scoped file reviewed and resolved.
- [x] Performance changes supported by repeatable benchmarks where practical.
- [x] Regression tests cover behavior changes.
- [x] `make check` passes on the current reviewed tree.
- [x] `.skills/` remains ignored and untracked.
