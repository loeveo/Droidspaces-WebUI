#!/usr/bin/env bash
set -euo pipefail

BIN=${BIN:-output/droidspaces-webui}
HOST=${HOST:-127.0.0.1}
PORT=${PORT:-}
AUTH_TOKEN=${AUTH_TOKEN:-smoke-token}
START_TIMEOUT=${START_TIMEOUT:-10}
KEEP_WORKDIR=${KEEP_WORKDIR:-0}
WORKDIR=${WORKDIR:-}

if ! command -v curl >/dev/null 2>&1 && ! command -v wget >/dev/null 2>&1; then
  echo "curl or wget is required" >&2
  exit 1
fi
if ! command -v python3 >/dev/null 2>&1; then
  echo "python3 is required for JSON validation" >&2
  exit 1
fi
if [ ! -x "$BIN" ]; then
  echo "missing executable binary: $BIN" >&2
  echo "run: make build" >&2
  exit 1
fi
if [ -z "$PORT" ]; then
  PORT=$(python3 - <<'PY'
import socket
s = socket.socket()
s.bind(('127.0.0.1', 0))
print(s.getsockname()[1])
s.close()
PY
)
fi

if [ -z "$WORKDIR" ]; then
  WORKDIR=$(mktemp -d)
else
  mkdir -p "$WORKDIR"
fi
WORKSPACE="$WORKDIR/workspace"
CORE="$WORKDIR/bin"
TEMPLATES="$WORKSPACE/rootfs"
CONFIG="$WORKDIR/webui.json"
LOG="$WORKDIR/webui.log"
PID_FILE="$WORKDIR/webui.pid"

cleanup() {
  if [ -f "$PID_FILE" ]; then
    kill "$(cat "$PID_FILE")" >/dev/null 2>&1 || true
    wait "$(cat "$PID_FILE")" >/dev/null 2>&1 || true
    rm -f "$PID_FILE"
  fi
  if [ "$KEEP_WORKDIR" != "1" ]; then
    rm -rf "$WORKDIR"
  else
    echo "local smoke workdir kept: $WORKDIR"
  fi
}
trap cleanup EXIT

mkdir -p "$CORE" "$WORKSPACE/Containers/demo/rootfs/etc" "$WORKSPACE/Pids" "$WORKSPACE/Logs" "$TEMPLATES/demo-template/etc"
printf 'demo rootfs\n' > "$WORKSPACE/Containers/demo/rootfs/etc/issue"
printf 'template rootfs\n' > "$TEMPLATES/demo-template/etc/issue"
printf 'delete me\n' > "$TEMPLATES/delete-me.tar.gz"
cat > "$WORKSPACE/Containers/demo/container.config" <<CFG
name=demo
hostname=demo
rootfs_path=$WORKSPACE/Containers/demo/rootfs
net_mode=host
allow_userns=1
run_at_boot=1
run_at_boot_priority=2
CFG
cat > "$CORE/droidspaces" <<'SH'
#!/bin/sh
printf '%s\n' "$*" >> "$PWD/droidspaces-calls.log"
if [ "$1" = "--name" ] && [ "$3" = "enter" ]; then
  exec /bin/sh -i
fi
if [ "$1" = "pid" ]; then
  exit 1
fi
if [ "$1" = "version" ]; then
  printf 'v6.4.5\n'
  exit 0
fi
printf 'fake droidspaces %s\n' "$*"
SH
chmod 755 "$CORE/droidspaces"
cat > "$CORE/busybox" <<'SH'
#!/bin/sh
cmd="$1"
shift
case "$cmd" in
  tar) exec tar "$@" ;;
  cp) exec cp "$@" ;;
  xzcat) exec xzcat "$@" ;;
  *) echo "unsupported busybox applet: $cmd" >&2; exit 127 ;;
esac
SH
chmod 755 "$CORE/busybox"

cat > "$CONFIG" <<JSON
{
  "mode": "local",
  "host": "$HOST",
  "port": $PORT,
  "authToken": "$AUTH_TOKEN",
  "droidspacesPath": "$CORE/droidspaces",
  "corePath": "$CORE",
  "imageRoot": "$WORKSPACE/images",
  "templateImageRoot": "$TEMPLATES",
  "workspace": "$WORKSPACE",
  "socketdEnabled": false,
  "rootfsSkipTLSVerify": true,
  "rootfsRepositories": []
}
JSON

"$BIN" --config "$CONFIG" >"$LOG" 2>&1 &
echo $! > "$PID_FILE"

api_get() {
  local path="$1"
  local url="http://${HOST}:${PORT}${path}"
  if command -v curl >/dev/null 2>&1; then
    curl -fsS -H "Authorization: Bearer ${AUTH_TOKEN}" "$url"
  else
    wget -qO- --header="Authorization: Bearer ${AUTH_TOKEN}" "$url"
  fi
}

ready=0
for _ in $(seq 1 "$START_TIMEOUT"); do
  if api_get "/api/status" >"$WORKDIR/status.json" 2>"$WORKDIR/request.err"; then
    ready=1
    break
  fi
  sleep 1
done
if [ "$ready" != "1" ]; then
  echo "local WebUI did not become ready" >&2
  cat "$WORKDIR/request.err" >&2 || true
  echo "--- webui log ---" >&2
  cat "$LOG" >&2 || true
  exit 1
fi

if command -v curl >/dev/null 2>&1; then
  curl -fsS "http://${HOST}:${PORT}/" >"$WORKDIR/index.html"
  curl -fsS "http://${HOST}:${PORT}/app.js" >"$WORKDIR/app.js"
  curl -fsS "http://${HOST}:${PORT}/styles.css" >"$WORKDIR/styles.css"
else
  wget -qO "$WORKDIR/index.html" "http://${HOST}:${PORT}/"
  wget -qO "$WORKDIR/app.js" "http://${HOST}:${PORT}/app.js"
  wget -qO "$WORKDIR/styles.css" "http://${HOST}:${PORT}/styles.css"
fi
api_get "/api/containers?all=1" >"$WORKDIR/containers.json"
api_get "/api/rootfs/local" >"$WORKDIR/rootfs-local.json"
api_get "/api/events" >"$WORKDIR/events.json"
api_get "/api/tasks" >"$WORKDIR/tasks.json"
api_get "/api/host" >"$WORKDIR/host.json"
api_get "/api/containers/demo" >"$WORKDIR/demo.json"
api_get "/api/boot-priority" >"$WORKDIR/boot-priority.json"
DELETE_ROOTFS_PATH=$(DELETE_ME="$TEMPLATES/delete-me.tar.gz" python3 -c 'from urllib.parse import quote; import os; print(quote(os.environ["DELETE_ME"], safe=""))')
if command -v curl >/dev/null 2>&1; then
  curl -fsS -X PUT -H "Authorization: Bearer ${AUTH_TOKEN}" -H "Content-Type: application/json" \
    -d '{"names":["demo"]}' "http://${HOST}:${PORT}/api/boot-priority" >"$WORKDIR/boot-priority-update.json"
  curl -fsS -X PATCH -H "Authorization: Bearer ${AUTH_TOKEN}" -H "Content-Type: application/json"     -d '{"hostname":"demo-edited","netMode":"nat","portForwards":"2222:22/tcp","restoreAfterUpdate":true}'     "http://${HOST}:${PORT}/api/containers/demo/config" >"$WORKDIR/config-update.json"
  curl -fsS -X DELETE -H "Authorization: Bearer ${AUTH_TOKEN}"     "http://${HOST}:${PORT}/api/rootfs/local/delete?path=${DELETE_ROOTFS_PATH}" >"$WORKDIR/rootfs-delete.json"
else
  wget -qO "$WORKDIR/boot-priority-update.json" --method=PUT --header="Authorization: Bearer ${AUTH_TOKEN}" --header="Content-Type: application/json" \
    --body-data='{"names":["demo"]}' "http://${HOST}:${PORT}/api/boot-priority"
  wget -qO "$WORKDIR/config-update.json" --method=PATCH --header="Authorization: Bearer ${AUTH_TOKEN}" --header="Content-Type: application/json"     --body-data='{"hostname":"demo-edited","netMode":"nat","portForwards":"2222:22/tcp","restoreAfterUpdate":true}'     "http://${HOST}:${PORT}/api/containers/demo/config"
  wget -qO "$WORKDIR/rootfs-delete.json" --method=DELETE --header="Authorization: Bearer ${AUTH_TOKEN}"     "http://${HOST}:${PORT}/api/rootfs/local/delete?path=${DELETE_ROOTFS_PATH}"
fi
api_get "/api/containers/demo" >"$WORKDIR/demo-updated.json"
api_get "/api/rootfs/local" >"$WORKDIR/rootfs-local-after-delete.json"

python3 - "$WORKDIR" <<'PY'
import json
import sys
from pathlib import Path
root = Path(sys.argv[1])
index = (root / 'index.html').read_text()
app_js = (root / 'app.js').read_text()
styles = (root / 'styles.css').read_text()
assert 'window.DS_AUTH_REQUIRED = true' in index, index[:500]
for token in ['id="menuToggle"', 'id="menuPanel"', 'data-view="containers"', 'id="createBtn"', 'id="createVersionHint"', 'id="createTemplatePicker"', 'class="create-modal-body"', 'create-identity-fields', 'id="createValidationSummary"', 'id="createCloudRootfsSearch"', 'id="rootfsRemoteSearch"', 'id="createCloudInitField"', 'id="createCloudInitEnabled"', 'id="createCloudInitGuidedSettings"', 'id="createCloudInitUsername"', 'id="createCloudInitPassword"', 'id="createCloudInitPasswordVisibility"', 'id="createCloudInitPasswordRegenerate"', 'id="createCloudInitSSHEnabled"', 'id="createCloudInitSSHPort"', 'id="createCloudInitRootSSH"', 'id="createCloudInitUserData"', 'id="createCloudInitAdvancedField"', 'id="createCloudInitCommands"', 'id="settingsNjuMirrorEnabled"', 'id="taskStatusBtn"', 'aria-controls="taskOverviewFloat"', 'id="taskFloat"', 'id="taskOverviewFloat"', 'data-view="network"', 'data-view="security"', 'id="configModal"', 'id="bootPriorityModal"', 'id="loginOverlay"']:
    assert token in index, token
for token in ['function switchView', 'function renderSecurity', 'function renderNetwork', 'function renderRuntimeVersions', 'function clearCreateTemplateSelection', 'function setCreateTemplateSelectionLocked', 'function createTemplateVariantInfo', 'function createTemplateSupportsCloudInit', 'function isTinyCloudRootfsAsset', 'function rootfsRemoteSearchText', 'function isDroidspacesOfficialRootfsDownloadURL', 'function rootfsAssetDescription', 'function generateCloudInitRandomPassword', 'function createCloudInitSSHSettings', 'function createCloudInitSSHServiceCommand', 'function createCloudInitUserDataResult', 'function updateCreateCloudInitSSHControls', 'function updateCreateCloudInitUserDataUI', 'function updateCreateCloudInitUI', 'function updateCreateFormValidation', 'function validateCreatePortForwards', 'function renderCreateCloudTemplatePicker', 'function setLinuxContainersMirrorRepository', 'function normalizeTaskSummary', 'function renderTaskOverview', 'function settleTerminalTask', 'function submitConfig', 'function deleteLocalRootfs', 'function renderUpstreamOrder', 'function renderSystemdUnitInspector', 'function submitBootPriority']:
    assert token in app_js, token
for token in ['.nav-popover', '.menu-toggle', '#menuToggle', '.rootfs-tabs', '.rootfs-tab-panel', '.terminal-screen', '.runtime-version-hint', '.rootfs-local-create', '.rootfs-local-actions', '.rootfs-cloud-description', '.template-picker-card', '.template-picker-card-description', '.template-picker-results', '.template-picker.template-selection-locked', '.cloud-init-panel', '.cloud-init-user-data', '.cloud-init-guided-settings', '.cloud-init-password-row', '.interface-order-list', '.systemd-inspector', '.create-modal-body', '.task-summary-grid', '.form-validation-summary', '.field-error']:
    assert token in styles, token
assert index.index('id="createTemplatePicker"') < index.index('id="createName"') < index.index('id="createCloudInitField"'), 'template picker, identity, and cloud-init fields must keep creation order'
assert 'return rootfsSystemVersion(item).toLocaleLowerCase("zh-CN");' in app_js, 'template search must use only system/version text'
assert '请选择本地模板' in app_js and '请选择云端镜像' in app_js, 'new-container flow must require an explicit template choice'
assert 'return taskIsActive(task);' in app_js, 'terminal tasks must not render in the task output'
status = json.loads((root / 'status.json').read_text())
containers = json.loads((root / 'containers.json').read_text())
rootfs = json.loads((root / 'rootfs-local.json').read_text())
events = json.loads((root / 'events.json').read_text())
tasks = json.loads((root / 'tasks.json').read_text())
host = json.loads((root / 'host.json').read_text())
demo = json.loads((root / 'demo.json').read_text())
boot_priority = json.loads((root / 'boot-priority.json').read_text())
boot_priority_update = json.loads((root / 'boot-priority-update.json').read_text())
config_update = json.loads((root / 'config-update.json').read_text())
rootfs_delete = json.loads((root / 'rootfs-delete.json').read_text())
demo_updated = json.loads((root / 'demo-updated.json').read_text())
rootfs_after_delete = json.loads((root / 'rootfs-local-after-delete.json').read_text())
assert status.get('authEnabled') is True, status
assert status.get('socketdEnabled') is False, status
assert status.get('backend') == 'socketd-disabled', status
assert status.get('webVersion'), status
assert status.get('coreVersion') == 'v6.4.5', status
assert status.get('supportedCoreVersion') == 'v6.5.0', status
assert isinstance(containers.get('containers'), list), containers
assert containers.get('source') in ('cli', 'workspace'), containers
assert len(containers['containers']) == 1, containers
assert containers['containers'][0].get('name') == 'demo', containers
assert containers['containers'][0].get('rootfsPath', '').startswith(status.get('workspace', '')), containers
assert isinstance(rootfs.get('items'), list), rootfs
assert any(item.get('name') == 'demo-template' and item.get('kind') == 'directory' for item in rootfs['items']), rootfs
assert isinstance(events.get('events'), list), events
assert isinstance(tasks.get('tasks'), list), tasks
assert isinstance(tasks.get('summary'), dict), tasks
assert host.get('goos') and host.get('goarch'), host
assert host.get('systemVersion') and host.get('kernelVersion') is not None, host
assert isinstance(host.get('memory'), dict), host
assert isinstance(host.get('network'), dict), host
assert any(item.get('key') == 'workspace' and item.get('exists') for item in host.get('paths', [])), host
assert demo.get('name') == 'demo', demo
assert demo.get('rootfsPath', '').endswith('/Containers/demo/rootfs'), demo
assert demo.get('source') in ('workspace', 'cli'), demo
assert demo.get('allowUserns') is True, demo
assert demo.get('runAtBoot') is True, demo
assert [item.get('name') for item in boot_priority.get('containers', [])] == ['demo'], boot_priority
assert boot_priority_update.get('ok') is True, boot_priority_update
assert config_update.get('ok') is True, config_update
assert config_update.get('updated', {}).get('net_mode') == 'nat', config_update
assert rootfs_delete.get('ok') is True, rootfs_delete
assert demo_updated.get('hostname') == 'demo-edited', demo_updated
assert demo_updated.get('netMode') == 'nat', demo_updated
assert demo_updated.get('allowUserns') is True, demo_updated
assert demo_updated.get('runAtBoot') is True, demo_updated
assert demo_updated.get('runAtBootPriority') == 1, demo_updated
assert not any(item.get('name') == 'delete-me.tar.gz' for item in rootfs_after_delete.get('items', [])), rootfs_after_delete
PY

echo "WebUI local smoke passed"
echo "status: $(tr -d '\n' <"$WORKDIR/status.json")"
echo "containers: $(tr -d '\n' <"$WORKDIR/containers.json" | cut -c1-240)"
