SHELL := /bin/sh

GO ?= go
DIST_DIR ?= dist
STAGE_DIR := $(DIST_DIR)/.stage
LDFLAGS := -s -w

WINDOWS_ARCHES := amd64 arm64
MACOS_ARCHES := amd64 arm64
WINDOWS_ARTIFACTS := $(addprefix $(DIST_DIR)/lan-copy-windows-,$(addsuffix .zip,$(WINDOWS_ARCHES)))
MACOS_ARTIFACTS := $(addprefix $(DIST_DIR)/lan-copy-macos-,$(addsuffix .tar.gz,$(MACOS_ARCHES)))
SOURCES := Makefile go.mod $(wildcard *.go) $(wildcard web/*) README.md

.PHONY: all build artifacts windows macos checksums test clean dist-clean

all: build

build: artifacts

artifacts: checksums

windows: $(WINDOWS_ARTIFACTS)

macos: $(MACOS_ARTIFACTS)

$(DIST_DIR)/lan-copy-windows-%.zip: $(SOURCES)
	@set -eu; \
	name="lan-copy-windows-$*"; \
	stage="$(STAGE_DIR)/$$name"; \
	rm -rf "$$stage" "$@"; \
	mkdir -p "$$stage"; \
	echo "Building Windows/$*..."; \
	CGO_ENABLED=0 GOOS=windows GOARCH=$* $(GO) build -trimpath -ldflags='$(LDFLAGS) -H=windowsgui' -o "$$stage/lan-copy.exe" .; \
	cp README.md "$$stage/README.md"; \
	(cd "$(STAGE_DIR)" && zip -qr "../$(@F)" "$$name"); \
	rm -rf "$$stage"

$(DIST_DIR)/lan-copy-macos-%.tar.gz: $(SOURCES)
	@set -eu; \
	name="lan-copy-macos-$*"; \
	stage="$(STAGE_DIR)/$$name"; \
	rm -rf "$$stage" "$@"; \
	mkdir -p "$$stage"; \
	echo "Building macOS/$*..."; \
	CGO_ENABLED=0 GOOS=darwin GOARCH=$* $(GO) build -trimpath -ldflags='$(LDFLAGS)' -o "$$stage/lan-copy" .; \
	chmod +x "$$stage/lan-copy"; \
	cp README.md "$$stage/README.md"; \
	tar -czf "$@" -C "$(STAGE_DIR)" "$$name"; \
	rm -rf "$$stage"

checksums: windows macos
	@set -eu; \
	cd "$(DIST_DIR)"; \
	set -- *.tar.gz *.zip; \
	if command -v sha256sum >/dev/null 2>&1; then \
		sha256sum "$${@}" > SHA256SUMS; \
	else \
		shasum -a 256 "$${@}" > SHA256SUMS; \
	fi; \
	echo "Checksums written to $(DIST_DIR)/SHA256SUMS"

test:
	$(GO) test ./...

clean:
	rm -rf "$(STAGE_DIR)" $(foreach file,$(WINDOWS_ARTIFACTS) $(MACOS_ARTIFACTS),"$(file)") "$(DIST_DIR)/SHA256SUMS"

dist-clean:
	rm -rf "$(DIST_DIR)"
