BINARY_NAME = droidspaces-webui
OUT_DIR = output
CMD = ./cmd/droidspaces-webui
GIT_VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
BUILD_ID ?= $(shell date -u +%Y%m%dT%H%M%S%NZ)

# Keep an explicitly supplied VERSION intact, while making default dirty builds
# distinguishable in the running WebUI.
ifeq ($(origin VERSION), undefined)
VERSION := $(GIT_VERSION)+build.$(BUILD_ID)
endif

VERSION_LDFLAGS := -X main.webVersion=$(VERSION)

.PHONY: all build android-arm64 default-config run clean test local-smoke android-smoke

all: build

build:
	@mkdir -p $(OUT_DIR)
	go build -ldflags="$(VERSION_LDFLAGS)" -o $(OUT_DIR)/$(BINARY_NAME) $(CMD)
	@echo "[+] Built: $(OUT_DIR)/$(BINARY_NAME)"

android-arm64:
	@mkdir -p $(OUT_DIR)
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -ldflags="-s -w $(VERSION_LDFLAGS)" -o $(OUT_DIR)/$(BINARY_NAME)-android-arm64 $(CMD)
	@echo "[+] Built: $(OUT_DIR)/$(BINARY_NAME)-android-arm64"

default-config:
	@mkdir -p $(OUT_DIR)
	cp config/webui.example.json $(OUT_DIR)/webui.json
	@echo "[+] Wrote: $(OUT_DIR)/webui.json"

run:
	go run $(CMD) --config $(OUT_DIR)/webui.json

test:
	go test ./...

clean:
	@rm -rf $(OUT_DIR)
	@echo "[+] Cleaned WebUI build artifacts"

local-smoke:
	./scripts/local-smoke.sh

android-smoke:
	./scripts/android-smoke.sh
