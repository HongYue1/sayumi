# Backend review tracker

**156/164 files reviewed; 8 pending (all tooling).**

Scope: Go source, tests, and backend build/tooling configuration. Embedded fonts,
icons, compiled resources, generated frontend output, and `.skills/` are excluded.

`[x]` Fully reviewed and verified. `[ ]` Review or required verification remains.
Current task and completion requirements: `TASK.md`. Change history: Git.

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

- [x] `internal/api/auth.go`
- [x] `internal/api/auth_test.go`
- [x] `internal/api/bookedit.go`
- [x] `internal/api/bookedit_test.go`
- [x] `internal/api/bookmarks.go`
- [x] `internal/api/bookmarks_test.go`
- [x] `internal/api/bookresponse_test.go`
- [x] `internal/api/books.go`
- [x] `internal/api/books_test.go`
- [x] `internal/api/chapters.go`
- [x] `internal/api/chapters_test.go`
- [x] `internal/api/context.go`
- [x] `internal/api/context_test.go`
- [x] `internal/api/customthemes.go`
- [x] `internal/api/customthemes_test.go`
- [x] `internal/api/download.go`
- [x] `internal/api/download_test.go`
- [x] `internal/api/flairs.go`
- [x] `internal/api/flairs_test.go`
- [x] `internal/api/fonts.go`
- [x] `internal/api/fonts_test.go`
- [x] `internal/api/gofile.go`
- [x] `internal/api/gofile_test.go`
- [x] `internal/api/library.go`
- [x] `internal/api/library_test.go`
- [x] `internal/api/middleware.go`
- [x] `internal/api/middleware_bench_test.go`
- [x] `internal/api/middleware_test.go`
- [x] `internal/api/middleware/gzip.go`
- [x] `internal/api/middleware/gzip_bench_test.go`
- [x] `internal/api/middleware/gzip_contract_test.go`
- [x] `internal/api/middleware/gzip_test.go`
- [x] `internal/api/presets.go`
- [x] `internal/api/presets_test.go`
- [x] `internal/api/profilemgr.go`
- [x] `internal/api/profilemgr_test.go`
- [x] `internal/api/progress.go`
- [x] `internal/api/progress_coalescer.go`
- [x] `internal/api/progress_coalescer_test.go`
- [x] `internal/api/progress_test.go`
- [x] `internal/api/resources.go`
- [x] `internal/api/resources_test.go`
- [x] `internal/api/router.go`
- [x] `internal/api/router_test.go`
- [x] `internal/api/search.go`
- [x] `internal/api/search_test.go`
- [x] `internal/api/setting.go`
- [x] `internal/api/setting_contract_test.go`
- [x] `internal/api/setting_test.go`
- [x] `internal/api/throttle.go`
- [x] `internal/api/throttle_bench_test.go`
- [x] `internal/api/throttle_contract_test.go`
- [x] `internal/api/throttle_test.go`
- [x] `internal/api/upload.go`
- [x] `internal/api/upload_handler_test.go`
- [x] `internal/api/upload_test.go`
- [x] `internal/api/version.go`
- [x] `internal/api/version_test.go`

## EPUB processing

- [x] `internal/epub/chapter.go`
- [x] `internal/epub/chapter_bench_test.go`
- [x] `internal/epub/chapter_cache_test.go`
- [x] `internal/epub/chapter_test.go`
- [x] `internal/epub/edit.go`
- [x] `internal/epub/edit_bench_test.go`
- [x] `internal/epub/edit_test.go`
- [x] `internal/epub/parser.go`
- [x] `internal/epub/parser_bench_test.go`
- [x] `internal/epub/parser_contract_test.go`
- [x] `internal/epub/parser_test.go`
- [x] `internal/epub/reader.go`
- [x] `internal/epub/reader_bench_test.go`
- [x] `internal/epub/reader_contract_test.go`
- [x] `internal/epub/reader_test.go`
- [x] `internal/epub/sanitize.go`
- [x] `internal/epub/sanitize_bench_test.go`
- [x] `internal/epub/sanitize_test.go`
- [x] `internal/epub/search.go`
- [x] `internal/epub/search_bench_test.go`
- [x] `internal/epub/search_test.go`
- [x] `internal/epub/store.go`
- [x] `internal/epub/store_bench_test.go`
- [x] `internal/epub/store_concurrency_test.go`
- [x] `internal/epub/store_contract_test.go`
- [x] `internal/epub/store_test.go`

## Fonts

- [x] `internal/fonts/embed.go`
- [x] `internal/fonts/embed_bench_test.go`
- [x] `internal/fonts/embed_test.go`
- [x] `internal/fonts/metrics.go`
- [x] `internal/fonts/metrics_bench_test.go`
- [x] `internal/fonts/metrics_test.go`
- [x] `internal/fonts/scan.go`
- [x] `internal/fonts/scan_bench_test.go`
- [x] `internal/fonts/scan_test.go`
- [x] `internal/fonts/sfnt.go`
- [x] `internal/fonts/sfnt_test.go`

## Library

- [x] `internal/library/cover.go`
- [x] `internal/library/cover_bench_test.go`
- [x] `internal/library/cover_contract_test.go`
- [x] `internal/library/cover_test.go`
- [x] `internal/library/scanner.go`
- [x] `internal/library/scanner_bench_test.go`
- [x] `internal/library/scanner_contract_test.go`
- [x] `internal/library/scanner_test.go`

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
