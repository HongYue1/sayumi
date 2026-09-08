# Sayumi — portable, local-first EPUB reader.
# Thin wrapper over the build scripts; see build.sh / check.sh / release.sh.

.DEFAULT_GOAL := build
.PHONY: build run check fix release web clean fmt version bench deps tools release-tools workflow-check

# Explicit setup; build/check never install or switch toolchains.
deps:
	bash ./provision.sh frontend

tools:
	bash ./provision.sh quality-tools

release-tools:
	bash ./provision.sh release-tools

# CGO-free local build (Go's configured CPU baseline) → ./sayumi or ./sayumi.exe
build:
	bash ./build.sh

# Build and run.
run:
	bash ./build.sh --run

# All quality gates (frontend + backend), read-only/CI-safe.
check:
	bash ./check.sh

# Explicit validator setup: bash ./provision.sh workflow-tools. Keep the same
# built-in schema/expression checks on every host; shell behavior is tested below.
workflow-check:
	bash ./provision.sh check-bun
	actionlint -shellcheck= -pyflakes= .github/workflows/ci.yml .github/workflows/go-toolchain-bump.yml
	bun .github/scripts/workflow-tests.mjs

# Repeatable Go-only benchmarks; e.g. make bench PKG=./cmd/sayumi BENCH=PrettyHandler.
PKG ?= ./...
BENCH ?= .
COUNT ?= 10
BENCHTIME ?= 1s
bench:
	go test -run='^$$' -bench='$(BENCH)' -benchmem -count=$(COUNT) -benchtime=$(BENCHTIME) $(PKG)

# Auto-fix pass (mutates files): imports, formatting, lint --fix, mod tidy.
fix:
	bash ./fix.sh

# Cross-compiled, portable release artifacts → ./dist-release/
release:
	bash ./release.sh

# Frontend production build only (embeds into cmd/sayumi/dist).
web:
	cd frontend && bun run build

# Go-only formatting with the same required tools/order as the full fix pass.
fmt:
	bash ./fix.sh --go-format

version: web
	@GOTOOLCHAIN=local go run ./cmd/sayumi --version

clean:
	rm -f sayumi sayumi.exe
	rm -rf dist-release
	rm -rf cmd/sayumi/dist
	mkdir -p cmd/sayumi/dist
	touch cmd/sayumi/dist/.gitkeep
