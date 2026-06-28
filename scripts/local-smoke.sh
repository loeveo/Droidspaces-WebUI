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
cat > "$WORKSPACE/Containers/demo/container.config" <<CFG
name=demo
hostname=demo
rootfs_path=$WORKSPACE/Containers/demo/rootfs
net_mode=host
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

api_get "/api/containers?all=1" >"$WORKDIR/containers.json"
api_get "/api/rootfs/local" >"$WORKDIR/rootfs-local.json"
api_get "/api/events" >"$WORKDIR/events.json"
api_get "/api/containers/demo" >"$WORKDIR/demo.json"

python3 - "$WORKDIR" <<'PY'
import json
import sys
from pathlib import Path
root = Path(sys.argv[1])
status = json.loads((root / 'status.json').read_text())
containers = json.loads((root / 'containers.json').read_text())
rootfs = json.loads((root / 'rootfs-local.json').read_text())
events = json.loads((root / 'events.json').read_text())
demo = json.loads((root / 'demo.json').read_text())
assert status.get('authEnabled') is True, status
assert status.get('socketdEnabled') is False, status
assert status.get('backend') == 'socketd-disabled', status
assert isinstance(containers.get('containers'), list), containers
assert containers.get('source') in ('cli', 'workspace'), containers
assert len(containers['containers']) == 1, containers
assert containers['containers'][0].get('name') == 'demo', containers
assert containers['containers'][0].get('rootfsPath', '').startswith(status.get('workspace', '')), containers
assert isinstance(rootfs.get('items'), list), rootfs
assert any(item.get('name') == 'demo-template' and item.get('kind') == 'directory' for item in rootfs['items']), rootfs
assert isinstance(events.get('events'), list), events
assert demo.get('name') == 'demo', demo
assert demo.get('rootfsPath', '').endswith('/Containers/demo/rootfs'), demo
assert demo.get('source') in ('workspace', 'cli'), demo
PY

echo "WebUI local smoke passed"
echo "status: $(tr -d '\n' <"$WORKDIR/status.json")"
echo "containers: $(tr -d '\n' <"$WORKDIR/containers.json" | cut -c1-240)"
