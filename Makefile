BINARY_NAME = droidspaces-webui
OUT_DIR = output
CMD = ./cmd/droidspaces-webui
GO ?= go
LINUX_CONFIG_TEMPLATE = config/webui.linux.example.json
ANDROID_CONFIG_TEMPLATE = config/webui.android.example.json
RELEASE_NAME ?= Droidspaces-WebUI
RELEASE_VERSION ?= v0.1.0
SUPPORTED_CORE_VERSION ?= v6.5.0
RELEASE_BUILD_VERSION := $(RELEASE_VERSION)-ds$(patsubst v%,%,$(SUPPORTED_CORE_VERSION))
GIT_VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
BUILD_ID ?= $(shell date -u +%Y%m%dT%H%M%S%NZ)

# Keep an explicitly supplied VERSION intact, while making default dirty builds
# distinguishable in the running WebUI.
ifeq ($(origin VERSION), undefined)
VERSION := $(GIT_VERSION)+build.$(BUILD_ID)
endif

VERSION_LDFLAGS := -X main.webVersion=$(VERSION)

# `all` intentionally covers the common Linux server/SBC and Android device
# architectures. Android artifacts keep GOOS=linux because Droidspaces runs
# them in Android's Linux userspace; this is the established deployment form.
RELEASE_LINUX_TARGETS := linux-amd64 linux-arm64 linux-armv7 linux-386 linux-riscv64
RELEASE_ANDROID_TARGETS := android-arm64 android-armv7 android-amd64 android-386
RELEASE_TARGETS := $(RELEASE_LINUX_TARGETS) $(RELEASE_ANDROID_TARGETS)

.PHONY: all release package release-package build $(RELEASE_TARGETS) default-config config-templates linux-config android-config run clean test local-smoke android-smoke

all: build $(RELEASE_TARGETS)

release: all

# Builds all published architectures, copies the two deployment templates, and
# produces a checksum-verified release archive. RELEASE_VERSION may be
# overridden for later releases, for example `make package RELEASE_VERSION=v0.2.0`.
package:
	$(MAKE) all config-templates VERSION="$(RELEASE_BUILD_VERSION)"
	RELEASE_NAME="$(RELEASE_NAME)" RELEASE_VERSION="$(RELEASE_VERSION)" SUPPORTED_CORE_VERSION="$(SUPPORTED_CORE_VERSION)" OUT_DIR="$(OUT_DIR)" ./scripts/package-release.sh

release-package: package

build:
	@mkdir -p $(OUT_DIR)
	$(GO) build -ldflags="$(VERSION_LDFLAGS)" -o $(OUT_DIR)/$(BINARY_NAME) $(CMD)
	@echo "[+] Built: $(OUT_DIR)/$(BINARY_NAME)"

# Linux releases
linux-amd64:
	@mkdir -p $(OUT_DIR)
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 $(GO) build -trimpath -ldflags="-s -w $(VERSION_LDFLAGS)" -o $(OUT_DIR)/$(BINARY_NAME)-linux-amd64 $(CMD)
	@echo "[+] Built: $(OUT_DIR)/$(BINARY_NAME)-linux-amd64"

linux-arm64:
	@mkdir -p $(OUT_DIR)
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 $(GO) build -trimpath -ldflags="-s -w $(VERSION_LDFLAGS)" -o $(OUT_DIR)/$(BINARY_NAME)-linux-arm64 $(CMD)
	@echo "[+] Built: $(OUT_DIR)/$(BINARY_NAME)-linux-arm64"

linux-armv7:
	@mkdir -p $(OUT_DIR)
	CGO_ENABLED=0 GOOS=linux GOARCH=arm GOARM=7 $(GO) build -trimpath -ldflags="-s -w $(VERSION_LDFLAGS)" -o $(OUT_DIR)/$(BINARY_NAME)-linux-armv7 $(CMD)
	@echo "[+] Built: $(OUT_DIR)/$(BINARY_NAME)-linux-armv7"

linux-386:
	@mkdir -p $(OUT_DIR)
	CGO_ENABLED=0 GOOS=linux GOARCH=386 $(GO) build -trimpath -ldflags="-s -w $(VERSION_LDFLAGS)" -o $(OUT_DIR)/$(BINARY_NAME)-linux-386 $(CMD)
	@echo "[+] Built: $(OUT_DIR)/$(BINARY_NAME)-linux-386"

linux-riscv64:
	@mkdir -p $(OUT_DIR)
	CGO_ENABLED=0 GOOS=linux GOARCH=riscv64 $(GO) build -trimpath -ldflags="-s -w $(VERSION_LDFLAGS)" -o $(OUT_DIR)/$(BINARY_NAME)-linux-riscv64 $(CMD)
	@echo "[+] Built: $(OUT_DIR)/$(BINARY_NAME)-linux-riscv64"

# Android releases (Android's supported ABI maps to Linux ELF targets here).
android-arm64:
	@mkdir -p $(OUT_DIR)
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 $(GO) build -trimpath -ldflags="-s -w $(VERSION_LDFLAGS)" -o $(OUT_DIR)/$(BINARY_NAME)-android-arm64 $(CMD)
	@echo "[+] Built: $(OUT_DIR)/$(BINARY_NAME)-android-arm64"

android-armv7:
	@mkdir -p $(OUT_DIR)
	CGO_ENABLED=0 GOOS=linux GOARCH=arm GOARM=7 $(GO) build -trimpath -ldflags="-s -w $(VERSION_LDFLAGS)" -o $(OUT_DIR)/$(BINARY_NAME)-android-armv7 $(CMD)
	@echo "[+] Built: $(OUT_DIR)/$(BINARY_NAME)-android-armv7"

android-amd64:
	@mkdir -p $(OUT_DIR)
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 $(GO) build -trimpath -ldflags="-s -w $(VERSION_LDFLAGS)" -o $(OUT_DIR)/$(BINARY_NAME)-android-amd64 $(CMD)
	@echo "[+] Built: $(OUT_DIR)/$(BINARY_NAME)-android-amd64"

android-386:
	@mkdir -p $(OUT_DIR)
	CGO_ENABLED=0 GOOS=linux GOARCH=386 $(GO) build -trimpath -ldflags="-s -w $(VERSION_LDFLAGS)" -o $(OUT_DIR)/$(BINARY_NAME)-android-386 $(CMD)
	@echo "[+] Built: $(OUT_DIR)/$(BINARY_NAME)-android-386"

default-config:
	@mkdir -p $(OUT_DIR)
	cp $(LINUX_CONFIG_TEMPLATE) $(OUT_DIR)/webui.json
	@echo "[+] Wrote Linux config: $(OUT_DIR)/webui.json"

config-templates: linux-config android-config

linux-config:
	@mkdir -p $(OUT_DIR)
	cp $(LINUX_CONFIG_TEMPLATE) $(OUT_DIR)/webui.linux.json
	@echo "[+] Wrote Linux config: $(OUT_DIR)/webui.linux.json"

android-config:
	@mkdir -p $(OUT_DIR)
	cp $(ANDROID_CONFIG_TEMPLATE) $(OUT_DIR)/webui.android.json
	@echo "[+] Wrote Android config: $(OUT_DIR)/webui.android.json"

run:
	$(GO) run $(CMD) --config $(OUT_DIR)/webui.json

test:
	$(GO) test ./...

clean:
	@rm -rf $(OUT_DIR)
	@echo "[+] Cleaned WebUI build artifacts"

local-smoke:
	./scripts/local-smoke.sh

android-smoke:
	./scripts/android-smoke.sh
