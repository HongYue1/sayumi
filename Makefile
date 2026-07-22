# Sayumi — portable, local-first EPUB reader.
# Thin wrapper over the build scripts; see build.sh / check.sh / release.sh.

.DEFAULT_GOAL := build
.PHONY: build run check fix release web clean fmt version

# Optimized local build (GOAMD64=v3 when supported) → ./sayumi
build:
	./build.sh

# Build and run.
run:
	./build.sh --run

# All quality gates (frontend + backend), read-only/CI-safe.
check:
	./check.sh

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

# Format imports first, then apply gofumpt's stricter rules. gofmt is needed
# only when neither richer formatter is installed.
fmt:
ifneq (,$(shell command -v goimports))
	goimports -w -local sayumi cmd internal
endif
ifneq (,$(shell command -v gofumpt))
	gofumpt -w cmd internal
else ifeq (,$(shell command -v goimports))
	gofmt -w cmd internal
endif

version: web
	@go run ./cmd/sayumi --version

clean:
	rm -f sayumi sayumi.exe
	rm -rf dist-release
	rm -rf cmd/sayumi/dist
	mkdir -p cmd/sayumi/dist
	touch cmd/sayumi/dist/.gitkeep
