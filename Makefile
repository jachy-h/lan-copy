SHELL := /bin/sh
.DEFAULT_GOAL := all

GO ?= go
DIST_DIR ?= release
STAGE_DIR := $(DIST_DIR)/.stage
VERSION_FILE ?= VERSION
VERSION ?=
PUBLISH_REMOTE ?= origin
DEPLOY_URL ?= http://192.168.2.88:8080/
DEPLOY_PACKAGE ?=
# Homebrew Tap 更新只在本机 macOS 上执行：CI 已不再使用 macOS runner。
HOMEBREW_TAP ?= jachy-h/jachy-h
HOMEBREW_FORMULA ?= lancopy
HOMEBREW_ASSET ?= Lan-Copy-macOS-Apple-Silicon.tar.gz
HOMEBREW_REPO ?= jachy-h/lan-copy
HOMEBREW_TAP_DIR ?= $(shell brew --repository 2>/dev/null)/Library/Taps/jachy-h/homebrew-jachy-h
HOMEBREW_WAIT ?= 0
# brew audit 的 --online 会访问 GitHub；网络受限时用 HOMEBREW_AUDIT_ONLINE=0 只跑本地检查。
HOMEBREW_AUDIT_ONLINE ?= 1
PUBLISH_HOMEBREW_WAIT ?= 900
# shellcheck disable=SC1073,SC1050,SC1072
# VERSION_FILE holds the current x.y.z-buildN version; a successful build advances it.
BUILD_VERSION = $(if $(strip $(VERSION)),$(strip $(VERSION)),$(shell cat "$(VERSION_FILE)" 2>/dev/null || printf '0.1.0'))
UPDATE_PUBLIC_KEY_FILE ?= .config/update.pub
UPDATE_SIGNING_KEY_FILE ?= .config/update.key
# Only the public key enters Make variables; private keys are read by the signer.
UPDATE_PUBLIC_KEY ?= $(shell cat "$(UPDATE_PUBLIC_KEY_FILE)" 2>/dev/null)
LDFLAGS := -s -w -X main.version=$(BUILD_VERSION) -X main.updatePublicKey=$(UPDATE_PUBLIC_KEY)

WINDOWS_ARCHES ?= amd64
WINDOWS_ARTIFACTS := $(addprefix $(DIST_DIR)/Lan-Copy-Windows-,$(addsuffix .zip,$(WINDOWS_ARCHES)))
MACOS_ARTIFACTS := $(DIST_DIR)/Lan-Copy-macOS-Apple-Silicon.tar.gz
SOURCES := Makefile go.mod go.sum $(wildcard *.go) $(shell find internal -type f) $(shell find web -type f) $(shell find frontend -type f ! -path 'frontend/node_modules/*') $(wildcard docs/*) $(wildcard scripts/*) $(wildcard scripts/update-manifest/*) README.md README.en.md $(wildcard release-assets/*) $(wildcard release-assets/*/*)

.PHONY: all build publish deploy artifacts windows macos checksums require-macos homebrew homebrew-check release-test test browser-test frontend-check frontend-build frontend-sync frontend-release frontend-dev deploytoken clean dist-clean FORCE

FORCE:

all: build

build:
	+@python3 scripts/build_release.py --version-file "$(VERSION_FILE)" --version "$(VERSION)" \
		$(foreach artifact,$(WINDOWS_ARTIFACTS) $(MACOS_ARTIFACTS),--artifact "$(artifact)") \
		--artifact "$(DIST_DIR)/SHA256SUMS" -- $(MAKE) --no-print-directory artifacts

# CI 只测 Linux 与 Windows；macOS 测试、Homebrew 校验与 Tap 更新都在本机完成。
# 推送标签后先等 Release 附件上线，再更新 Tap，避免公式指向不存在的归档。
publish:
	+@version_before="$$(cat "$(VERSION_FILE)" 2>/dev/null || printf '')"; \
	PYTHONDONTWRITEBYTECODE=1 python3 scripts/publish_release.py --version-file "$(VERSION_FILE)" --version "$(VERSION)" \
		--remote "$(PUBLISH_REMOTE)" -- $(MAKE) --no-print-directory release-test build; \
	status=$$?; \
	[ "$$status" -eq 0 ] || exit "$$status"; \
	version_after="$$(cat "$(VERSION_FILE)" 2>/dev/null || printf '')"; \
	if [ "$$version_before" = "$$version_after" ]; then \
		printf '\n未创建版本提交（已取消或预览），跳过 Homebrew 更新。\n'; \
		exit 0; \
	fi; \
	printf '\n等待 Release 上线后更新 Homebrew Tap…\n'; \
	$(MAKE) --no-print-directory homebrew VERSION="$$version_after" HOMEBREW_WAIT="$(PUBLISH_HOMEBREW_WAIT)"

deploy:
	@node scripts/deploy-target.mjs --url "$(DEPLOY_URL)" \
		$(if $(strip $(DEPLOY_PACKAGE)),--package "$(DEPLOY_PACKAGE)") \
		$(if $(strip $(VERSION)),--version "$(VERSION)") \
		$(if $(filter 1,$(DRY_RUN)),--dry-run)

artifacts: checksums

windows: $(WINDOWS_ARTIFACTS)

macos: $(MACOS_ARTIFACTS)

$(WINDOWS_ARTIFACTS) $(MACOS_ARTIFACTS): | frontend-release

$(DIST_DIR)/Lan-Copy-Windows-%.zip: $(SOURCES) FORCE
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

# macOS 专属验证只在本地做：CI 已移除 macOS runner。
require-macos:
	@test "$$(uname -s)" = Darwin || { \
		echo '该目标必须在 macOS 上执行：darwin 测试与 Homebrew 校验只在本地运行。'; \
		exit 1; \
	}

# 只读校验本地 Tap 公式，不修改、不提交。
homebrew-check: require-macos
	@set -eu; \
	formula="$(HOMEBREW_TAP_DIR)/Formula/$(HOMEBREW_FORMULA).rb"; \
	if [ ! -f "$$formula" ]; then \
		echo "未找到 $$formula：先执行 brew tap $(HOMEBREW_TAP)"; \
		exit 1; \
	fi; \
	ruby -c "$$formula"; \
	brew style "$$formula"; \
	brew audit --strict $(if $(filter 1,$(HOMEBREW_AUDIT_ONLINE)),--online) --formula "$(HOMEBREW_TAP)/$(HOMEBREW_FORMULA)"

# 用已发布 Release 的校验和更新 Tap 公式并推送；HOMEBREW_WAIT 控制等待秒数。
homebrew: require-macos
	@PYTHONDONTWRITEBYTECODE=1 python3 scripts/publish_homebrew.py \
		--tap-dir "$(HOMEBREW_TAP_DIR)" \
		--tap "$(HOMEBREW_TAP)" \
		--formula "$(HOMEBREW_FORMULA)" \
		--asset "$(HOMEBREW_ASSET)" \
		--repo "$(HOMEBREW_REPO)" \
		--version "$(BUILD_VERSION)" \
		--wait "$(HOMEBREW_WAIT)" \
		$(if $(strip $(CHECKSUMS)),--checksums "$(CHECKSUMS)") \
		$(if $(filter 0,$(HOMEBREW_AUDIT_ONLINE)),--no-online-audit) \
		$(if $(filter 1,$(DRY_RUN)),--dry-run) \
		$(if $(filter 1,$(NO_PUSH)),--no-push)

release-test: require-macos
	$(GO) vet ./...
	PYTHONDONTWRITEBYTECODE=1 python3 -m unittest discover -s scripts -p 'test_*.py'
	$(GO) test ./...

test:
	PYTHONDONTWRITEBYTECODE=1 python3 -m unittest discover -s scripts -p 'test_*.py'
	$(GO) test ./...

# 可选：本机跑浏览器集成测试。发布链路不依赖它，CI 的 ubuntu job 才是常规门禁；
# Playwright 的 webkit 在 macOS 上是原生构建，排查 WebKit 差异时用它。
browser-test:
	cd frontend && npm ci && npx playwright install --with-deps chromium firefox webkit && npm run test:browser

frontend-check:
	cd frontend && npm ci && npm run check && npm run check-assets

frontend-build:
	cd frontend && npm run build

frontend-sync:
	cd frontend && npm run build && npm run sync-assets

# Release builds own the frontend pipeline: install, verify, build, embed and re-check.
frontend-release:
	cd frontend && npm ci && npm run check && npm run sync-assets && npm run check-assets

frontend-dev:
	cd frontend && npm run dev -- --host

deploytoken:
	@python3 scripts/deploy_token.py $(if $(ROTATE),--rotate,)

clean:
	rm -rf "$(STAGE_DIR)" $(foreach file,$(WINDOWS_ARTIFACTS) $(MACOS_ARTIFACTS),"$(file)") "$(DIST_DIR)/SHA256SUMS"

dist-clean:
	rm -rf "$(DIST_DIR)"
