#!/system/bin/sh
# Droidspaces WebUI Android launcher.
#
# This script may be invoked manually as root or installed directly as
# /data/adb/service.d/droidspaces-webui.sh for Magisk. It deliberately keeps
# the binary, configuration, log, and PID paths within the Android workspace.
set -eu

PATH=/data/adb/magisk:/data/adb/ksu/bin:/system/bin:/system/xbin:/vendor/bin:${PATH:-}
export PATH

WORKSPACE=${DROIDSPACES_WORKSPACE:-/data/local/Droidspaces}
WEBUI_BINARY=${DS_WEBUI_BINARY:-}
WEBUI_CONFIG=${DS_WEBUI_CONFIG:-}
LOG_DIR=${DS_WEBUI_LOG_DIR:-}
LOG_FILE=${DS_WEBUI_LOG_FILE:-}
PID_FILE=${DS_WEBUI_PID_FILE:-}
LOCK_DIR=${DS_WEBUI_LOCK_DIR:-}
BOOT_WAIT_TIMEOUT=${DS_WEBUI_BOOT_WAIT_TIMEOUT:-180}

[ -n "$WEBUI_BINARY" ] || WEBUI_BINARY="$WORKSPACE/bin/droidspaces-webui"
[ -n "$WEBUI_CONFIG" ] || WEBUI_CONFIG="$WORKSPACE/webui.json"
[ -n "$LOG_DIR" ] || LOG_DIR="$WORKSPACE/Logs"
[ -n "$LOG_FILE" ] || LOG_FILE="$LOG_DIR/webui.log"
[ -n "$PID_FILE" ] || PID_FILE="$WORKSPACE/webui.pid"
[ -n "$LOCK_DIR" ] || LOCK_DIR="$WORKSPACE/.webui-launch.lock"

foreground=0
wait_for_boot=0

# Magisk service.d scripts run during late_start, but the Android framework and
# the Droidspaces core can still be coming up. Only the service.d installation
# gets this wait automatically; manual use remains immediate by default.
case "$0" in
  /data/adb/service.d/*) wait_for_boot=1 ;;
esac

usage() {
  cat <<'EOF'
Usage: start-android-webui.sh [--foreground] [--wait] [--no-wait]

Start Droidspaces WebUI from /data/local/Droidspaces by default.

  --foreground  Replace this launcher with the WebUI process. Intended for
                interactive diagnosis, not Magisk service.d.
  --wait        Wait for Android sys.boot_completed before starting.
  --no-wait     Do not wait for Android boot completion.
  -h, --help    Show this help.

Environment overrides:
  DROIDSPACES_WORKSPACE, DS_WEBUI_BINARY, DS_WEBUI_CONFIG,
  DS_WEBUI_LOG_DIR, DS_WEBUI_LOG_FILE, DS_WEBUI_PID_FILE,
  DS_WEBUI_LOCK_DIR, DS_WEBUI_BOOT_WAIT_TIMEOUT.

DS_WEBUI_BOOT_WAIT_TIMEOUT is seconds; 0 waits indefinitely.
EOF
}

while [ "$#" -gt 0 ]; do
  case "$1" in
    --foreground) foreground=1 ;;
    --wait) wait_for_boot=1 ;;
    --no-wait) wait_for_boot=0 ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      usage >&2
      exit 2
      ;;
  esac
  shift
done

timestamp() {
  date '+%Y-%m-%dT%H:%M:%S%z' 2>/dev/null || printf '%s' 'unknown-time'
}

log() {
  line="$(timestamp) droidspaces-webui launcher: $*"
  printf '%s\n' "$line" >&2
  if [ -d "$LOG_DIR" ]; then
    printf '%s\n' "$line" >>"$LOG_FILE" 2>/dev/null || true
  fi
}

die() {
  log "$*"
  exit 1
}

is_pid() {
  case "$1" in
    ''|*[!0-9]*) return 1 ;;
  esac
  return 0
}

is_nonnegative_integer() {
  case "$1" in
    ''|*[!0-9]*) return 1 ;;
  esac
  return 0
}

read_pid_file() {
  value=""
  [ -r "$PID_FILE" ] || return 1
  IFS= read -r value <"$PID_FILE" || true
  is_pid "$value" || return 1
  printf '%s\n' "$value"
}

process_is_webui() {
  candidate=$1
  is_pid "$candidate" || return 1
  kill -0 "$candidate" >/dev/null 2>&1 || return 1
  [ -r "/proc/$candidate/cmdline" ] || return 2

  command_line=$(tr '\000' ' ' <"/proc/$candidate/cmdline" 2>/dev/null) || return 2
  case "$command_line" in
    *"$WEBUI_BINARY"*"$WEBUI_CONFIG"*) return 0 ;;
  esac
  return 1
}

find_running_webui() {
  for proc_dir in /proc/[0-9]*; do
    [ -r "$proc_dir/cmdline" ] || continue
    candidate=${proc_dir#/proc/}
    if process_is_webui "$candidate"; then
      printf '%s\n' "$candidate"
      return 0
    fi
  done
  return 1
}

pid_directory=${PID_FILE%/*}
[ "$pid_directory" = "$PID_FILE" ] && pid_directory=.
log_file_directory=${LOG_FILE%/*}
[ "$log_file_directory" = "$LOG_FILE" ] && log_file_directory=.
lock_directory=${LOCK_DIR%/*}
[ "$lock_directory" = "$LOCK_DIR" ] && lock_directory=.

write_pid_file() {
  candidate=$1
  temporary_pid_file="${PID_FILE}.new.$$"
  umask 077
  printf '%s\n' "$candidate" >"$temporary_pid_file" || return 1
  if ! mv -f "$temporary_pid_file" "$PID_FILE"; then
    rm -f "$temporary_pid_file"
    return 1
  fi
}

remove_pid_file_if_matches() {
  expected=$1
  current=$(read_pid_file || true)
  if [ "$current" = "$expected" ]; then
    rm -f "$PID_FILE"
  fi
}

release_lock() {
  rm -f "$LOCK_DIR/pid" 2>/dev/null || true
  rmdir "$LOCK_DIR" 2>/dev/null || true
}

on_signal() {
  release_lock
  exit 1
}

acquire_lock() {
  if mkdir "$LOCK_DIR" 2>/dev/null; then
    printf '%s\n' "$$" >"$LOCK_DIR/pid" || {
      rmdir "$LOCK_DIR" 2>/dev/null || true
      die "cannot write launch lock: $LOCK_DIR"
    }
    return 0
  fi

  lock_pid=""
  if [ -r "$LOCK_DIR/pid" ]; then
    IFS= read -r lock_pid <"$LOCK_DIR/pid" || true
  fi
  if is_pid "$lock_pid" && kill -0 "$lock_pid" >/dev/null 2>&1; then
    log "another launcher is already working (pid $lock_pid)"
    exit 0
  fi

  die "stale or unreadable launch lock exists at $LOCK_DIR; inspect it before removing it"
}

wait_for_android_boot() {
  is_nonnegative_integer "$BOOT_WAIT_TIMEOUT" || \
    die "DS_WEBUI_BOOT_WAIT_TIMEOUT must be a non-negative number of seconds"
  if ! command -v getprop >/dev/null 2>&1; then
    log "getprop is unavailable; skipping Android boot-complete wait"
    return 0
  fi

  elapsed=0
  while [ "$(getprop sys.boot_completed 2>/dev/null || true)" != "1" ]; do
    if [ "$BOOT_WAIT_TIMEOUT" -ne 0 ] && [ "$elapsed" -ge "$BOOT_WAIT_TIMEOUT" ]; then
      die "Android boot did not complete within ${BOOT_WAIT_TIMEOUT}s"
    fi
    sleep 1
    elapsed=$((elapsed + 1))
  done
  log "Android boot is complete"
}

umask 077
[ -x "$WEBUI_BINARY" ] || die "WebUI binary is not executable: $WEBUI_BINARY"
[ -r "$WEBUI_CONFIG" ] || die "WebUI configuration is not readable: $WEBUI_CONFIG"
mkdir -p "$LOG_DIR" "$log_file_directory" "$pid_directory" "$lock_directory" || \
  die "cannot create WebUI log, PID, or lock directory"

if [ "$wait_for_boot" -eq 1 ]; then
  wait_for_android_boot
fi

acquire_lock
trap 'release_lock' 0
trap 'on_signal' HUP INT TERM

existing_pid=$(read_pid_file || true)
if [ -n "$existing_pid" ]; then
  if process_is_webui "$existing_pid"; then
    log "WebUI is already running (pid $existing_pid)"
    exit 0
  else
    process_status=$?
  fi
  if [ "$process_status" -eq 2 ]; then
    die "cannot verify process $existing_pid from $PID_FILE; refusing to risk a duplicate instance"
  fi
  log "discarding stale WebUI PID file: $PID_FILE"
  rm -f "$PID_FILE"
fi

existing_pid=$(find_running_webui || true)
if [ -n "$existing_pid" ]; then
  write_pid_file "$existing_pid" || die "cannot record existing WebUI PID in $PID_FILE"
  log "WebUI is already running (pid $existing_pid); rebuilt PID file"
  exit 0
fi

if [ "$foreground" -eq 1 ]; then
  write_pid_file "$$" || die "cannot write WebUI PID file: $PID_FILE"
  log "starting WebUI in the foreground"
  release_lock
  exec "$WEBUI_BINARY" --config "$WEBUI_CONFIG"
fi

if command -v nohup >/dev/null 2>&1; then
  nohup "$WEBUI_BINARY" --config "$WEBUI_CONFIG" </dev/null >>"$LOG_FILE" 2>&1 &
elif [ -x /data/adb/magisk/busybox ]; then
  /data/adb/magisk/busybox nohup "$WEBUI_BINARY" --config "$WEBUI_CONFIG" </dev/null >>"$LOG_FILE" 2>&1 &
else
  "$WEBUI_BINARY" --config "$WEBUI_CONFIG" </dev/null >>"$LOG_FILE" 2>&1 &
fi
webui_pid=$!

if ! write_pid_file "$webui_pid"; then
  kill "$webui_pid" >/dev/null 2>&1 || true
  wait "$webui_pid" >/dev/null 2>&1 || true
  die "cannot write WebUI PID file: $PID_FILE"
fi

sleep 1
if kill -0 "$webui_pid" >/dev/null 2>&1; then
  log "started WebUI in the background (pid $webui_pid, log $LOG_FILE)"
  exit 0
fi

remove_pid_file_if_matches "$webui_pid"
die "WebUI exited during startup; inspect $LOG_FILE"
