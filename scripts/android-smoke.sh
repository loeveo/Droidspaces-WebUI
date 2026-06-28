#!/usr/bin/env bash
set -euo pipefail

ADB=${ADB:-adb}
DEVICE_SERIAL=${DEVICE_SERIAL:-}
REMOTE_DIR=${REMOTE_DIR:-/data/local/Droidspaces}
REMOTE_BIN=${REMOTE_BIN:-${REMOTE_DIR}/droidspaces-webui-smoke-android-arm64}
REMOTE_CONFIG=${REMOTE_CONFIG:-${REMOTE_DIR}/webui-smoke.json}
LOCAL_BIN=${LOCAL_BIN:-output/droidspaces-webui-android-arm64}
LOCAL_CONFIG=${LOCAL_CONFIG:-output/webui.json}
HOST_PORT=${HOST_PORT:-9090}
DEVICE_PORT=${DEVICE_PORT:-9090}
AUTH_TOKEN=${AUTH_TOKEN:-}
START_TIMEOUT=${START_TIMEOUT:-10}
LOG_FILE=${LOG_FILE:-${REMOTE_DIR}/Logs/webui-smoke.log}
PID_FILE=${PID_FILE:-${REMOTE_DIR}/webui-smoke.pid}
PUSH=${PUSH:-1}
KEEP_RUNNING=${KEEP_RUNNING:-0}

adb_cmd() {
  if [ -n "$DEVICE_SERIAL" ]; then
    "$ADB" -s "$DEVICE_SERIAL" "$@"
  else
    "$ADB" "$@"
  fi
}

remote_su() {
  adb_cmd shell su -c "$1"
}

shell_quote() {
  printf "'%s'" "$(printf "%s" "$1" | sed "s/'/'\\''/g")"
}

api_get() {
  local path="$1"
  local url="http://127.0.0.1:${HOST_PORT}${path}"
  if command -v curl >/dev/null 2>&1; then
    if [ -n "$AUTH_TOKEN" ]; then
      curl -fsS -H "Authorization: Bearer ${AUTH_TOKEN}" "$url"
    else
      curl -fsS "$url"
    fi
  else
    if [ -n "$AUTH_TOKEN" ]; then
      wget -qO- --header="Authorization: Bearer ${AUTH_TOKEN}" "$url"
    else
      wget -qO- "$url"
    fi
  fi
}

cleanup() {
  adb_cmd forward --remove "tcp:${HOST_PORT}" >/dev/null 2>&1 || true
  if [ "$KEEP_RUNNING" != "1" ]; then
    remote_su "if [ -f '${PID_FILE}' ]; then kill \$(cat '${PID_FILE}') >/dev/null 2>&1 || true; rm -f '${PID_FILE}'; fi"
  fi
}
trap cleanup EXIT

if ! command -v "$ADB" >/dev/null 2>&1; then
  echo "adb not found: $ADB" >&2
  exit 1
fi
if ! command -v curl >/dev/null 2>&1 && ! command -v wget >/dev/null 2>&1; then
  echo "curl or wget is required on the host" >&2
  exit 1
fi

adb_cmd get-state >/dev/null

if [ "$PUSH" = "1" ]; then
  if [ ! -f "$LOCAL_BIN" ]; then
    echo "missing local binary: $LOCAL_BIN" >&2
    echo "run: make android-arm64 default-config" >&2
    exit 1
  fi
  if [ ! -f "$LOCAL_CONFIG" ]; then
    echo "missing local config: $LOCAL_CONFIG" >&2
    echo "run: make default-config" >&2
    exit 1
  fi
  tmp_bin="/data/local/tmp/$(basename "$REMOTE_BIN")"
  tmp_config="/data/local/tmp/$(basename "$REMOTE_CONFIG")"
  remote_su "mkdir -p '${REMOTE_DIR}/Logs'"
  adb_cmd push "$LOCAL_BIN" "$tmp_bin" >/dev/null
  adb_cmd push "$LOCAL_CONFIG" "$tmp_config" >/dev/null
  remote_su "cp '$tmp_bin' '${REMOTE_BIN}' && cp '$tmp_config' '${REMOTE_CONFIG}' && chmod 755 '${REMOTE_BIN}' && rm -f '$tmp_bin' '$tmp_config'"
fi

remote_su "mkdir -p '${REMOTE_DIR}/Logs'; if [ -f '${PID_FILE}' ]; then kill \$(cat '${PID_FILE}') >/dev/null 2>&1 || true; rm -f '${PID_FILE}'; fi"
start_cmd="cd '${REMOTE_DIR}' && nohup '${REMOTE_BIN}' --config '${REMOTE_CONFIG}' --listen 127.0.0.1:${DEVICE_PORT}"
if [ -n "$AUTH_TOKEN" ]; then
  start_cmd="$start_cmd --auth-token $(shell_quote "$AUTH_TOKEN")"
fi
start_cmd="$start_cmd > '${LOG_FILE}' 2>&1 & echo \$! > '${PID_FILE}'"
remote_su "$start_cmd"

adb_cmd forward --remove "tcp:${HOST_PORT}" >/dev/null 2>&1 || true
adb_cmd forward "tcp:${HOST_PORT}" "tcp:${DEVICE_PORT}" >/dev/null

ready=0
for _ in $(seq 1 "$START_TIMEOUT"); do
  if api_get "/api/status" >/tmp/droidspaces-webui-smoke-status.json 2>/tmp/droidspaces-webui-smoke.err; then
    ready=1
    break
  fi
  sleep 1
done
if [ "$ready" != "1" ]; then
  echo "WebUI did not become ready" >&2
  echo "--- host request error ---" >&2
  cat /tmp/droidspaces-webui-smoke.err >&2 || true
  echo "--- remote log ---" >&2
  remote_su "cat '${LOG_FILE}' 2>/dev/null || true" >&2
  exit 1
fi

api_get "/api/containers?all=1" >/tmp/droidspaces-webui-smoke-containers.json
api_get "/api/rootfs/local" >/tmp/droidspaces-webui-smoke-rootfs-local.json
api_get "/api/events" >/tmp/droidspaces-webui-smoke-events.json

if command -v python3 >/dev/null 2>&1; then
  python3 - <<'PY'
import json
from pathlib import Path
for path in [
    '/tmp/droidspaces-webui-smoke-status.json',
    '/tmp/droidspaces-webui-smoke-containers.json',
    '/tmp/droidspaces-webui-smoke-rootfs-local.json',
    '/tmp/droidspaces-webui-smoke-events.json',
]:
    json.loads(Path(path).read_text())
PY
fi

echo "WebUI Android smoke passed"
echo "status: $(tr -d '\n' </tmp/droidspaces-webui-smoke-status.json)"
echo "containers: $(tr -d '\n' </tmp/droidspaces-webui-smoke-containers.json | cut -c1-240)"
echo "local rootfs: $(tr -d '\n' </tmp/droidspaces-webui-smoke-rootfs-local.json | cut -c1-240)"
if [ "$KEEP_RUNNING" = "1" ]; then
  echo "WebUI left running on device, pid file: ${PID_FILE}"
fi
