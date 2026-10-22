SHELL := /bin/sh
.DEFAULT_GOAL := all

GO ?= go
DIST_DIR ?= release
STAGE_DIR := $(DIST_DIR)/.stage
VERSION_FILE ?= VERSION
VERSION ?=
PUBLISH_REMOTE ?= origin
# Packaging subtargets use the saved version; make build chooses the next patch.
BUILD_VERSION = $(if $(strip $(VERSION)),$(strip $(VERSION)),$(shell cat "$(VERSION_FILE)" 2>/dev/null || printf '0.1.0'))
UPDATE_PUBLIC_KEY_FILE ?= .config/update.pub
UPDATE_SIGNING_KEY_FILE ?= .config/update.key
# Only the public key enters Make variables; private keys are read by the signer.
UPDATE_PUBLIC_KEY ?= $(shell cat "$(UPDATE_PUBLIC_KEY_FILE)" 2>/dev/null)
LDFLAGS := -s -w -X main.version=$(BUILD_VERSION) -X main.updatePublicKey=$(UPDATE_PUBLIC_KEY)

WINDOWS_ARCHES ?= amd64
WINDOWS_ARTIFACTS := $(addprefix $(DIST_DIR)/局域网快传-Windows-,$(addsuffix .zip,$(WINDOWS_ARCHES)))
MACOS_ARTIFACTS := $(DIST_DIR)/Lan-Copy-macOS-Apple-Silicon.tar.gz
SOURCES := Makefile go.mod $(wildcard *.go) $(shell find web -type f) $(wildcard docs/*) $(wildcard scripts/*) $(wildcard scripts/update-manifest/*) README.md README.en.md $(wildcard release-assets/*) $(wildcard release-assets/*/*)

.PHONY: all build publish artifacts windows macos checksums test clean dist-clean FORCE

FORCE:

all: build

build:
	+@python3 scripts/build_release.py --version-file "$(VERSION_FILE)" --version "$(VERSION)" \
		$(foreach artifact,$(WINDOWS_ARTIFACTS) $(MACOS_ARTIFACTS),--artifact "$(artifact)") \
		--artifact "$(DIST_DIR)/SHA256SUMS" -- $(MAKE) --no-print-directory artifacts

publish:
	+@PYTHONDONTWRITEBYTECODE=1 python3 scripts/publish_release.py --version-file "$(VERSION_FILE)" --version "$(VERSION)" \
		--remote "$(PUBLISH_REMOTE)" -- $(MAKE) --no-print-directory build

artifacts: checksums

windows: $(WINDOWS_ARTIFACTS)

macos: $(MACOS_ARTIFACTS)

$(DIST_DIR)/局域网快传-Windows-%.zip: $(SOURCES) FORCE
	@set -eu; \
	name="局域网快传-Windows-$*"; \
	stage="$(STAGE_DIR)/$$name"; \
	rm -rf "$$stage" "$@"; \
	mkdir -p "$$stage"; \
	echo "Building Windows/$*..."; \
	CGO_ENABLED=0 GOOS=windows GOARCH=$* $(GO) build -trimpath -ldflags='$(LDFLAGS)' -o "$$stage/lan-copy.exe" .; \
	$(GO) run ./scripts/update-manifest -dir "$$stage" -version "$(BUILD_VERSION)" -os windows -arch "$*" -public-key "$(UPDATE_PUBLIC_KEY)" -key-file "$(UPDATE_SIGNING_KEY_FILE)"; \
	cp README.md "$$stage/README.md"; \
	cp docs/WINDOWS-SECURITY.md "$$stage/WINDOWS-SECURITY.md"; \
	cp release-assets/offline-start.html "$$stage/使用说明.html"; \
	python3 scripts/package_zip.py "$$stage" "$(DIST_DIR)/$(@F)" "$$name"; \
	rm -rf "$$stage"

$(DIST_DIR)/Lan-Copy-macOS-Apple-Silicon.tar.gz: $(SOURCES) FORCE
	@set -eu; \
	name="Lan-Copy-macOS-arm64"; \
	stage="$(STAGE_DIR)/$$name"; \
	rm -rf "$$stage" "$@"; \
	mkdir -p "$$stage"; \
	echo "Building macOS/Apple Silicon..."; \
	CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 $(GO) build -trimpath -ldflags='$(LDFLAGS)' -o "$$stage/lan-copy" .; \
	chmod +x "$$stage/lan-copy"; \
	$(GO) run ./scripts/update-manifest -dir "$$stage" -version "$(BUILD_VERSION)" -os darwin -arch arm64 -public-key "$(UPDATE_PUBLIC_KEY)" -key-file "$(UPDATE_SIGNING_KEY_FILE)"; \
	cp README.md "$$stage/README.md"; \
	tar -czf "$@" -C "$(STAGE_DIR)" "$$name"; \
	rm -rf "$$stage"

checksums: windows macos
	@set -eu; \
	cd "$(DIST_DIR)"; \
	set -- $(notdir $(WINDOWS_ARTIFACTS) $(MACOS_ARTIFACTS)); \
	if command -v sha256sum >/dev/null 2>&1; then \
		sha256sum "$${@}" > SHA256SUMS; \
	else \
		shasum -a 256 "$${@}" > SHA256SUMS; \
	fi; \
	echo "Checksums written to $(DIST_DIR)/SHA256SUMS"

test:
	PYTHONDONTWRITEBYTECODE=1 python3 -m unittest discover -s scripts -p 'test_*.py'
	node scripts/test-service-status.cjs
	node scripts/test-delete-confirmation.cjs
	node scripts/test-dialog.cjs
	node scripts/test-upload-tree.cjs
	node scripts/test-settings-update.cjs
	$(GO) test ./...

clean:
	rm -rf "$(STAGE_DIR)" $(foreach file,$(WINDOWS_ARTIFACTS) $(MACOS_ARTIFACTS),"$(file)") "$(DIST_DIR)/SHA256SUMS"

dist-clean:
	rm -rf "$(DIST_DIR)"
