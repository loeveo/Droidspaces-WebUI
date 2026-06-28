BINARY_NAME = droidspaces-webui
OUT_DIR = output
CMD = ./cmd/droidspaces-webui

.PHONY: all build android-arm64 default-config run clean test android-smoke

all: build

build:
	@mkdir -p $(OUT_DIR)
	go build -o $(OUT_DIR)/$(BINARY_NAME) $(CMD)
	@echo "[+] Built: $(OUT_DIR)/$(BINARY_NAME)"

android-arm64:
	@mkdir -p $(OUT_DIR)
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -ldflags="-s -w" -o $(OUT_DIR)/$(BINARY_NAME)-android-arm64 $(CMD)
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

android-smoke:
	./scripts/android-smoke.sh
