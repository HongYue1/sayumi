# Sayumi — portable, local-first EPUB reader.
# Thin wrapper over the build scripts; see build.sh / check.sh / release.sh.

.DEFAULT_GOAL := build
.PHONY: build run check fix release web clean fmt version bench deps tools release-tools

# Explicit setup; build/check never install or switch toolchains.
deps:
	bash ./provision.sh frontend

tools:
	bash ./provision.sh quality-tools

release-tools:
	bash ./provision.sh release-tools

# Optimized local build (GOAMD64=v3 when supported) → ./sayumi or ./sayumi.exe
build:
	./build.sh

# Build and run.
run:
	./build.sh --run

# All quality gates (frontend + backend), read-only/CI-safe.
check:
	./check.sh

# Repeatable Go-only benchmarks; e.g. make bench PKG=./cmd/sayumi BENCH=PrettyHandler.
PKG ?= ./...
BENCH ?= .
COUNT ?= 10
BENCHTIME ?= 1s
bench:
	go test -run='^$$' -bench='$(BENCH)' -benchmem -count=$(COUNT) -benchtime=$(BENCHTIME) $(PKG)

# Auto-fix pass (mutates files): imports, formatting, lint --fix, mod tidy.
fix:
	./fix.sh

# Cross-compiled, portable release artifacts → ./dist-release/
release:
	./release.sh

# Frontend production build only (embeds into cmd/sayumi/dist).
web:
ifneq (,$(shell command -v bun))
	cd frontend && bun run build
else
	cd frontend && npm run build
endif

# Go-only formatting with the same required tools/order as the full fix pass.
fmt:
	bash ./fix.sh --go-format

version: web
	@go run ./cmd/sayumi --version

clean:
	rm -f sayumi sayumi.exe
	rm -rf dist-release
	rm -rf cmd/sayumi/dist
	mkdir -p cmd/sayumi/dist
	touch cmd/sayumi/dist/.gitkeep
