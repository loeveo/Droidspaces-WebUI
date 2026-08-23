#!/usr/bin/env bash
# Droidspaces WebUI Linux 安装脚本。
# 从官方 Droidspaces Release 直接提取核心二进制，并从本项目 Release
# 下载与本机架构匹配的 WebUI，安装到 /var/lib/Droidspaces 目录布局。
set -euo pipefail

readonly WEBUI_REPOSITORY="loeveo/Droidspaces-WebUI"
readonly WEBUI_API_ROOT="https://api.github.com/repos/${WEBUI_REPOSITORY}/releases"
readonly CORE_REPOSITORY="ravindu644/Droidspaces-OSS"
readonly CORE_API_ROOT="https://api.github.com/repos/${CORE_REPOSITORY}/releases"
readonly DEFAULT_CORE_RELEASE_TAG="v6.5.0"
readonly CORE_WORKSPACE="/var/lib/Droidspaces"
readonly MAX_CORE_ARCHIVE_BYTES=$((128 * 1024 * 1024))
readonly MAX_CORE_ARCHIVE_MEMBERS=256
readonly MAX_CORE_BINARY_BYTES=$((64 * 1024 * 1024))
readonly DEFAULT_WEBUI_PORT=9090
readonly DEFAULT_AUTH_TOKEN_LENGTH=8
readonly SYSTEMD_MANAGED_MARKER="# Managed by Droidspaces WebUI installer."

release_tag=""
core_release_tag=""
workspace="/var/lib/Droidspaces"
bin_dir=""
config_path=""
bin_dir_explicit=0
config_explicit=0
create_symlink=1
webui_symlink_path="/usr/sbin/droidspaces-webui"
core_symlink_path="/usr/sbin/droidspaces"
replace_config=0
install_core=1
replace_core=0
restart_core=0
install_systemd=1
start_systemd=1
replace_systemd_units=0
non_interactive=0
systemd_unit_dir="${DS_WEBUI_SYSTEMD_UNIT_DIR:-/etc/systemd/system}"
initial_config_mode="public"
initial_config_host="0.0.0.0"
initial_config_port="$DEFAULT_WEBUI_PORT"
initial_config_auth_token=""
write_initial_config=0

显示帮助() {
  cat <<'EOF'
用法：
  sudo bash install-linux.sh [选项]

功能：
  直接从官方 https://github.com/ravindu644/Droidspaces-OSS 的 Release
  下载并校验当前 CPU 架构的 Droidspaces 核心二进制，再从
  https://github.com/loeveo/Droidspaces-WebUI 的 Release 下载 WebUI。
  安装后创建、启用并启动 systemd 的 Droidspaces 与 WebUI 服务。

默认安装位置：
  Droidspaces 核心：/var/lib/Droidspaces/bin/droidspaces
  WebUI 二进制：/var/lib/Droidspaces/bin/droidspaces-webui
  WebUI 配置：  /var/lib/Droidspaces/webui.json
  模板目录：    /var/lib/Droidspaces/rootfs
  核心快捷链接：/usr/sbin/droidspaces
  快捷链接：    /usr/sbin/droidspaces-webui
  systemd 单元： /etc/systemd/system/droidspaces.service
                 /etc/systemd/system/droidspaces-webui.service

选项：
  --version <标签>       安装指定 Release 标签；默认安装最新正式 Release。
  --core-version <标签>  指定官方 Droidspaces Release 标签；默认使用 WebUI
                         发行包声明的核心版本，旧发行包回退到 v6.5.0。
  --no-core              不下载核心，仅使用现有 bin/droidspaces。
  --replace-core         下载并原子替换已有核心；默认保留已有核心。
  --restart-core         明确允许重启正在运行的核心服务；可与 --replace-core
                         配合立即切换新核心，也可单独用于手动重启。
  --workspace <目录>     当前官方 Linux 核心仅支持 /var/lib/Droidspaces；
                         此参数仅接受该默认路径。
  --bin-dir <目录>       指定 WebUI 与 Droidspaces 核心所在目录。
  --config <文件>        指定 WebUI 配置文件路径。
  --no-symlink           不创建 /usr/sbin 下的核心和 WebUI 快捷链接。
  --symlink <文件>       指定 WebUI 快捷链接路径。
  --core-symlink <文件>  指定核心快捷链接路径。
  --replace-config       使用 Release 内的 Linux 模板替换已有配置，并保留备份。
  --non-interactive      明确确认不显示初始设置问题，使用默认公网设置与随机 Token；
                         Token 仅写入权限为 0600 的配置文件，不输出到终端。
  --no-systemd           不创建、不启用 systemd 服务。
  --no-start             创建并启用服务，但本次不启动或重启服务。
  --replace-systemd-units
                         允许覆盖非本脚本管理的同名 systemd 单元。
  -h, --help             显示本帮助。

说明：
  - 核心使用官方后端 tar.gz 直接提取二进制；本脚本不使用 .deb 或 apt。
  - 已有 WebUI 二进制会备份为 .backup.<时间戳>，然后原子替换。
  - 已有核心默认保留；只有 --replace-core 才会下载并替换。
  - 已有 webui.json 默认不会被覆盖；使用 --replace-config 才会覆盖并备份。
  - 首次写入配置时会询问初始访问设置。直接回车使用 /var/lib/Droidspaces、
    公网监听 0.0.0.0:9090 和随机 8 位 Token；公网 Token 会在交互安装完成时显示一次。
  - 非交互环境必须显式传入 --non-interactive，避免脚本意外公开管理界面。
  - systemd 可用时，默认将启用并启动未运行的服务；服务异常退出会自动重启。
  - 已运行的核心服务不会被默认重启，以免中断容器或后台任务；升级核心后
    使用 --restart-core 才会立即切换到新核心。
  - 官方 Linux 核心的 Containers、Pids 与 Logs 工作区固定为
    /var/lib/Droidspaces，因此脚本不接受其他工作区以避免状态分裂。
EOF
}

失败() {
  printf '安装失败：%s\n' "$*" >&2
  exit 1
}

提示() {
  printf '[Droidspaces 安装] %s\n' "$*"
}

警告() {
  printf '[Droidspaces 安装] 警告：%s\n' "$*" >&2
}

需要命令() {
  command -v "$1" >/dev/null 2>&1 || 失败 "缺少命令：$1"
}

生成随机授权密钥() {
  python3 - "$DEFAULT_AUTH_TOKEN_LENGTH" <<'PY'
import secrets
import string
import sys

length = int(sys.argv[1])
alphabet = string.ascii_letters + string.digits
print("".join(secrets.choice(alphabet) for _ in range(length)))
PY
}

验证监听端口() {
  local value=$1 number
  [[ "$value" =~ ^[0-9]{1,5}$ ]] || return 1
  number=$((10#$value))
  [ "$number" -ge 1 ] && [ "$number" -le 65535 ]
}

验证授权密钥() {
  local value=$1
  [ -n "$value" ] || return 1
  [ "${#value}" -le 128 ] || return 1
  [[ "$value" =~ ^[A-Za-z0-9._~+-]+$ ]]
}

读取初始WebUI设置() {
  local answer selected_port

  initial_config_mode="public"
  initial_config_host="0.0.0.0"
  initial_config_port="$DEFAULT_WEBUI_PORT"
  initial_config_auth_token=$(生成随机授权密钥) || 失败 "无法生成随机授权密钥"

  if [ "$non_interactive" -eq 1 ]; then
    提示 "使用默认 WebUI 设置：工作区 $CORE_WORKSPACE，公网监听 0.0.0.0:$DEFAULT_WEBUI_PORT，随机 ${DEFAULT_AUTH_TOKEN_LENGTH} 位 Token"
    return
  fi
  [ -t 0 ] || 失败 "首次写入配置需要交互终端；自动化安装请明确传入 --non-interactive"

  printf '\nWebUI 初始设置\n'
  printf '  默认工作区：%s（官方 Linux 核心固定）\n' "$CORE_WORKSPACE"
  printf '  默认访问：公网监听 0.0.0.0:%s，随机 %s 位 Token\n' "$DEFAULT_WEBUI_PORT" "$DEFAULT_AUTH_TOKEN_LENGTH"
  printf '  注意：脚本不会自动开放防火墙或路由器端口。\n'
  answer=""
  read -r -p '是否修改初始 WebUI 设置？[y/N] ' answer || true
  case "${answer,,}" in
    ""|n|no)
      return
      ;;
    y|yes)
      ;;
    *)
      失败 "初始设置选项只能输入 y 或 n"
      ;;
  esac

  answer=""
  read -r -p "Droidspaces 工作区 [$CORE_WORKSPACE]：" answer || true
  if [ -n "$answer" ] && [ "$answer" != "$CORE_WORKSPACE" ]; then
    失败 "官方 Droidspaces Linux 核心的工作区固定为 $CORE_WORKSPACE，不能改为 $answer"
  fi

  answer=""
  read -r -p '允许外网访问（监听 0.0.0.0）[Y/n] ' answer || true
  case "${answer,,}" in
    ""|y|yes)
      ;;
    n|no)
      initial_config_mode="local"
      initial_config_host="127.0.0.1"
      ;;
    *)
      失败 "外网访问选项只能输入 y 或 n"
      ;;
  esac

  if [ "$initial_config_mode" = "public" ]; then
    answer=""
    read -r -p "公网访问端口 [$DEFAULT_WEBUI_PORT]：" answer || true
  else
    answer=""
    read -r -p "本机监听端口 [$DEFAULT_WEBUI_PORT]：" answer || true
  fi
  selected_port=${answer:-$DEFAULT_WEBUI_PORT}
  验证监听端口 "$selected_port" || 失败 "监听端口必须在 1-65535 之间"
  initial_config_port=$selected_port

  printf '授权密钥 [回车自动生成 %s 位随机字符串]：' "$DEFAULT_AUTH_TOKEN_LENGTH"
  answer=""
  read -r -s answer || true
  printf '\n'
  if [ -n "$answer" ]; then
    initial_config_auth_token=$answer
  fi
  验证授权密钥 "$initial_config_auth_token" || \
    失败 "授权密钥必须为 1-128 个 ASCII 字符，且只可包含字母、数字、.、_、~、+ 或 -"
}

下载文件() {
  local url=$1 destination=$2
  if command -v curl >/dev/null 2>&1; then
    curl --fail --location --proto '=https' --proto-redir '=https' \
      --retry 3 --retry-delay 2 --connect-timeout 15 \
      --user-agent "Droidspaces-WebUI-Linux-Installer" \
      --output "$destination" "$url"
    return
  fi
  wget --tries=3 --timeout=15 \
    --user-agent="Droidspaces-WebUI-Linux-Installer" \
    --output-document="$destination" "$url"
}

检测WebUI架构() {
  case "$(uname -m)" in
    x86_64|amd64) printf '%s\n' "linux-amd64" ;;
    aarch64|arm64) printf '%s\n' "linux-arm64" ;;
    armv7l|armv7*) printf '%s\n' "linux-armv7" ;;
    i386|i486|i586|i686) printf '%s\n' "linux-386" ;;
    riscv64) printf '%s\n' "linux-riscv64" ;;
    *) 失败 "当前 CPU 架构 $(uname -m) 没有可用的 Linux WebUI 发行二进制" ;;
  esac
}

检测核心架构() {
  case "$(uname -m)" in
    x86_64|amd64) printf '%s\n' "x86_64" ;;
    aarch64|arm64) printf '%s\n' "aarch64" ;;
    armv7l|armv7*) printf '%s\n' "armhf" ;;
    i386|i486|i586|i686) printf '%s\n' "x86" ;;
    riscv64) printf '%s\n' "riscv64" ;;
    *) 失败 "当前 CPU 架构 $(uname -m) 没有可用的官方 Droidspaces 核心" ;;
  esac
}

解析WebUI发行资产() {
  local metadata_file=$1
  python3 - "$metadata_file" <<'PY'
import json
import re
import sys

metadata_path = sys.argv[1]
try:
    with open(metadata_path, encoding="utf-8") as source:
        release = json.load(source)
except (OSError, json.JSONDecodeError) as error:
    print(f"无法解析 GitHub Release 元数据：{error}", file=sys.stderr)
    raise SystemExit(1)

if release.get("draft") or release.get("prerelease"):
    print("所选 Release 不是可安装的正式发行版本", file=sys.stderr)
    raise SystemExit(1)

archives = [
    asset for asset in release.get("assets", [])
    if asset.get("name", "").startswith("Droidspaces-WebUI-")
    and asset.get("name", "").endswith(".tar.gz")
    and asset.get("browser_download_url")
]
if len(archives) != 1:
    print("Release 中必须恰好包含一个 Droidspaces-WebUI-*.tar.gz 发行包", file=sys.stderr)
    raise SystemExit(1)

archive = archives[0]
url = archive["browser_download_url"]
expected_prefix = "https://github.com/loeveo/Droidspaces-WebUI/releases/download/"
if not url.startswith(expected_prefix):
    print("WebUI 下载地址不符合预期 GitHub Release 路径", file=sys.stderr)
    raise SystemExit(1)
checksum_name = archive["name"] + ".sha256"
checksum = next(
    (
        asset for asset in release.get("assets", [])
        if asset.get("name") == checksum_name and asset.get("browser_download_url")
    ),
    None,
)
digest = archive.get("digest", "").lower()
if digest.startswith("sha256:"):
    digest = digest.split(":", 1)[1]
if not re.fullmatch(r"[0-9a-f]{64}", digest):
    digest = ""

if not digest and checksum is None:
    print("Release 未提供可验证的 SHA-256 摘要或 .sha256 校验文件", file=sys.stderr)
    raise SystemExit(1)

print(release.get("tag_name", ""))
print(archive["name"])
print(url)
print(digest)
print(checksum["browser_download_url"] if checksum else "")
PY
}

读取校验文件() {
  local checksum_file=$1
  python3 - "$checksum_file" <<'PY'
import re
import sys

with open(sys.argv[1], encoding="utf-8") as source:
    for line in source:
        match = re.match(r"^([0-9A-Fa-f]{64})(?:\s+|\s+\*)", line)
        if match:
            print(match.group(1).lower())
            raise SystemExit(0)
raise SystemExit(".sha256 文件中没有有效的 SHA-256 值")
PY
}

解析核心发行资产() {
  local metadata_file=$1
  python3 - "$metadata_file" "$CORE_REPOSITORY" "$MAX_CORE_ARCHIVE_BYTES" <<'PY'
import json
import re
import sys

metadata_path, repository, max_archive_bytes = sys.argv[1:]
max_archive_bytes = int(max_archive_bytes)
try:
    with open(metadata_path, encoding="utf-8") as source:
        release = json.load(source)
except (OSError, json.JSONDecodeError) as error:
    print(f"无法解析 Droidspaces 官方 Release 元数据：{error}", file=sys.stderr)
    raise SystemExit(1)

if release.get("draft") or release.get("prerelease"):
    print("所选 Droidspaces Release 不是可安装的正式发行版本", file=sys.stderr)
    raise SystemExit(1)

archives = [
    asset for asset in release.get("assets", [])
    if asset.get("name", "").startswith("droidspaces-v")
    and asset.get("name", "").endswith(".tar.gz")
    and asset.get("browser_download_url")
]
if len(archives) != 1:
    print("官方 Release 中必须恰好包含一个 droidspaces-v*.tar.gz 后端压缩包", file=sys.stderr)
    raise SystemExit(1)

archive = archives[0]
url = archive["browser_download_url"]
expected_prefix = f"https://github.com/{repository}/releases/download/"
if not url.startswith(expected_prefix):
    print("官方核心下载地址不符合预期 GitHub Release 路径", file=sys.stderr)
    raise SystemExit(1)

digest = archive.get("digest", "").lower()
if digest.startswith("sha256:"):
    digest = digest.split(":", 1)[1]
if not re.fullmatch(r"[0-9a-f]{64}", digest):
    print("官方核心 Release 未提供有效的 SHA-256 摘要", file=sys.stderr)
    raise SystemExit(1)

size = archive.get("size")
if not isinstance(size, int) or size <= 0 or size > max_archive_bytes:
    print(f"官方核心 Release 文件大小无效或超过 {max_archive_bytes} 字节上限", file=sys.stderr)
    raise SystemExit(1)

tag = release.get("tag_name", "")
if not isinstance(tag, str) or not re.fullmatch(r"[A-Za-z0-9._-]+", tag):
    print("官方核心 Release 标签无效", file=sys.stderr)
    raise SystemExit(1)

print(tag)
print(archive["name"])
print(url)
print(digest)
print(size)
PY
}

读取发行包核心版本() {
  local manifest_file=$1
  if [ ! -f "$manifest_file" ]; then
    警告 "发行包未提供核心版本清单，回退到 $DEFAULT_CORE_RELEASE_TAG"
    printf '%s\n' "$DEFAULT_CORE_RELEASE_TAG"
    return
  fi
  python3 - "$manifest_file" "$CORE_REPOSITORY" <<'PY'
import json
import re
import sys

manifest_path, expected_repository = sys.argv[1:]
try:
    with open(manifest_path, encoding="utf-8") as source:
        manifest = json.load(source)
except (OSError, json.JSONDecodeError) as error:
    print(f"无法解析发行包核心版本清单：{error}", file=sys.stderr)
    raise SystemExit(1)

if manifest.get("droidspacesRepository") != expected_repository:
    print("发行包声明的 Droidspaces 官方仓库不符合预期", file=sys.stderr)
    raise SystemExit(1)
tag = manifest.get("droidspacesReleaseTag")
if not isinstance(tag, str) or not re.fullmatch(r"[A-Za-z0-9._-]+", tag):
    print("发行包声明的 Droidspaces Release 标签无效", file=sys.stderr)
    raise SystemExit(1)
print(tag)
PY
}

验证Release标签() {
  local tag=$1 option_name=$2
  case "$tag" in
    *[!A-Za-z0-9._-]*|'') 失败 "$option_name 只能包含字母、数字、点、下划线和连字符" ;;
  esac
}

文件大小字节() {
  wc -c < "$1" | tr -d '[:space:]'
}

创建快捷链接() {
  local target=$1 link_path=$2 label=$3
  if [ -e "$link_path" ] && [ ! -L "$link_path" ]; then
    警告 "未创建${label}快捷链接：$link_path 已是普通文件"
    return
  fi
  mkdir -p "$(dirname "$link_path")"
  ln -sfn "$target" "$link_path"
  提示 "已创建${label}快捷链接：$link_path -> $target"
}

systemd可用() {
  command -v systemctl >/dev/null 2>&1 && [ -d /run/systemd/system ]
}

systemd引用() {
  local value=$1
  case "$value" in
    *$'\n'*|*$'\r'*) 失败 "systemd 服务路径不能包含换行符" ;;
  esac
  value=${value//\\/\\\\}
  value=${value//\"/\\\"}
  value=${value//\$/\$\$}
  value=${value//%/%%}
  printf '"%s"' "$value"
}

systemd路径() {
  local value=$1
  case "$value" in
    *$'\n'*|*$'\r'*) 失败 "systemd 服务路径不能包含换行符" ;;
  esac
  value=${value//\\/\\\\}
  value=${value//%/%%}
  printf '%s' "$value"
}

写入systemd单元() {
  local source_file=$1 destination=$2 backup stage
  [ ! -L "$destination" ] || 失败 "systemd 单元不能是符号链接：$destination"
  if [ -e "$destination" ] && ! grep -Fqx "$SYSTEMD_MANAGED_MARKER" "$destination"; then
    if [ "$replace_systemd_units" -ne 1 ]; then
      失败 "systemd 单元已存在且不是本脚本管理：$destination；确认后使用 --replace-systemd-units 覆盖"
    fi
    backup="${destination}.backup.$(date +%Y%m%d%H%M%S)"
    cp -p -- "$destination" "$backup"
    提示 "已备份原有 systemd 单元：$backup"
  fi
  stage="${destination}.new.$$"
  install -m 0644 "$source_file" "$stage"
  mv -f -- "$stage" "$destination"
}

同名进程运行中() {
  local executable_name=$1
  command -v pgrep >/dev/null 2>&1 && \
    pgrep -f -- "(^|/)${executable_name}([[:space:]]|$)" >/dev/null 2>&1
}

预检systemd单元() {
  local unit path fragment_path dropin_paths
  if [ "$install_systemd" -ne 1 ] || ! systemd可用; then
    return 0
  fi
  for unit in droidspaces.service droidspaces-webui.service; do
    path="$systemd_unit_dir/$unit"
    [ ! -L "$path" ] || 失败 "systemd 单元不能是符号链接：$path"
    if [ -e "$path" ] && ! grep -Fqx "$SYSTEMD_MANAGED_MARKER" "$path" && [ "$replace_systemd_units" -ne 1 ]; then
      失败 "systemd 单元已存在且不是本脚本管理：$path；确认后使用 --replace-systemd-units 覆盖"
    fi
    fragment_path=$(systemctl show "$unit" --property=FragmentPath --value 2>/dev/null || true)
    dropin_paths=$(systemctl show "$unit" --property=DropInPaths --value 2>/dev/null || true)
    if [ -n "$fragment_path" ] && [ "$fragment_path" != "$path" ] && [ "$replace_systemd_units" -ne 1 ]; then
      失败 "已加载的 $unit 来自 $fragment_path；确认后使用 --replace-systemd-units 覆盖"
    fi
    if [ -n "$dropin_paths" ]; then
      失败 "$unit 存在额外 systemd drop-in：$dropin_paths；请先审查并移除或合并该覆盖配置后再安装"
    fi
  done
}

while [ "$#" -gt 0 ]; do
  case "$1" in
    --version)
      [ "$#" -ge 2 ] || 失败 "--version 需要一个标签值"
      release_tag=$2
      shift 2
      ;;
    --core-version)
      [ "$#" -ge 2 ] || 失败 "--core-version 需要一个标签值"
      core_release_tag=$2
      shift 2
      ;;
    --no-core)
      install_core=0
      shift
      ;;
    --replace-core)
      replace_core=1
      shift
      ;;
    --restart-core)
      restart_core=1
      shift
      ;;
    --workspace)
      [ "$#" -ge 2 ] || 失败 "--workspace 需要一个目录"
      workspace=$2
      shift 2
      ;;
    --bin-dir)
      [ "$#" -ge 2 ] || 失败 "--bin-dir 需要一个目录"
      bin_dir=$2
      bin_dir_explicit=1
      shift 2
      ;;
    --config)
      [ "$#" -ge 2 ] || 失败 "--config 需要一个文件路径"
      config_path=$2
      config_explicit=1
      shift 2
      ;;
    --no-symlink)
      create_symlink=0
      shift
      ;;
    --symlink)
      [ "$#" -ge 2 ] || 失败 "--symlink 需要一个文件路径"
      webui_symlink_path=$2
      shift 2
      ;;
    --core-symlink)
      [ "$#" -ge 2 ] || 失败 "--core-symlink 需要一个文件路径"
      core_symlink_path=$2
      shift 2
      ;;
    --replace-config)
      replace_config=1
      shift
      ;;
    --non-interactive)
      non_interactive=1
      shift
      ;;
    --no-systemd)
      install_systemd=0
      shift
      ;;
    --no-start)
      start_systemd=0
      shift
      ;;
    --replace-systemd-units)
      replace_systemd_units=1
      shift
      ;;
    -h|--help)
      显示帮助
      exit 0
      ;;
    *)
      失败 "未知选项：$1；使用 --help 查看帮助"
      ;;
  esac
done

[ "$(uname -s)" = "Linux" ] || 失败 "本脚本只能安装到 Linux 系统"
[ "$(id -u)" -eq 0 ] || 失败 "请使用 root 或 sudo 执行此脚本"
[ -n "$workspace" ] && [ "$workspace" != "/" ] || 失败 "工作区不能为空或根目录"
[ "$workspace" = "$CORE_WORKSPACE" ] || 失败 "当前官方 Droidspaces Linux 核心的工作区固定为 $CORE_WORKSPACE；不支持 --workspace $workspace"

if [ "$bin_dir_explicit" -eq 0 ]; then
  bin_dir="$workspace/bin"
fi
if [ "$config_explicit" -eq 0 ]; then
  config_path="$workspace/webui.json"
fi
[ -n "$bin_dir" ] && [ "$bin_dir" != "/" ] || 失败 "二进制目录不能为空或根目录"
[ -n "$config_path" ] && [ "$config_path" != "/" ] || 失败 "配置文件路径无效"
if [ -L "$config_path" ]; then
  失败 "目标配置是符号链接：$config_path；为避免覆盖错误目标，安装已停止"
fi

for command_name in python3 sha256sum tar install mktemp awk wc pgrep; do
  需要命令 "$command_name"
done
if ! command -v curl >/dev/null 2>&1 && ! command -v wget >/dev/null 2>&1; then
  失败 "需要 curl 或 wget 以下载 GitHub Release"
fi

if [ -n "$release_tag" ]; then
  验证Release标签 "$release_tag" "WebUI Release 标签"
fi
if [ -n "$core_release_tag" ]; then
  验证Release标签 "$core_release_tag" "Droidspaces Release 标签"
fi
if [ ! -e "$config_path" ] || [ "$replace_config" -eq 1 ]; then
  write_initial_config=1
  读取初始WebUI设置
fi
[ -n "$systemd_unit_dir" ] && [ "$systemd_unit_dir" != "/" ] || 失败 "systemd 单元目录无效"
预检systemd单元

core_service_was_active=0
webui_service_was_active=0
if systemd可用; then
  systemctl is-active --quiet droidspaces.service && core_service_was_active=1 || true
  systemctl is-active --quiet droidspaces-webui.service && webui_service_was_active=1 || true
fi

architecture=$(检测WebUI架构)
core_architecture=$(检测核心架构)
target_binary_name="droidspaces-webui-${architecture}"
temporary_dir=$(mktemp -d "${TMPDIR:-/tmp}/droidspaces-webui-install.XXXXXX")
cleanup() {
  rm -rf -- "$temporary_dir"
}
trap cleanup EXIT

metadata_file="$temporary_dir/release.json"
if [ -n "$release_tag" ]; then
  metadata_url="$WEBUI_API_ROOT/tags/$release_tag"
  requested_release="指定标签 $release_tag"
else
  metadata_url="$WEBUI_API_ROOT/latest"
  requested_release="最新正式 Release"
fi

提示 "检测到 Linux 架构：$architecture"
提示 "正在读取 $requested_release 的发行信息"
下载文件 "$metadata_url" "$metadata_file" || 失败 "无法访问 GitHub Releases；请检查网络、代理或 Release 标签"

mapfile -t release_fields < <(解析WebUI发行资产 "$metadata_file") || 失败 "无法从 Release 元数据中找到可验证的 WebUI 发行包"
[ "${#release_fields[@]}" -eq 5 ] || 失败 "Release 元数据格式不完整"
resolved_tag=${release_fields[0]}
archive_name=${release_fields[1]}
archive_url=${release_fields[2]}
expected_checksum=${release_fields[3]}
checksum_url=${release_fields[4]}
case "$archive_name" in
  *[!A-Za-z0-9._-]*|'') 失败 "WebUI 发行包文件名无效" ;;
esac

archive_path="$temporary_dir/$archive_name"
提示 "正在下载 Release $resolved_tag：$archive_name"
下载文件 "$archive_url" "$archive_path" || 失败 "发行包下载失败"

if [ -z "$expected_checksum" ]; then
  checksum_file="$temporary_dir/$archive_name.sha256"
  提示 "Release API 未提供摘要，正在下载 .sha256 校验文件"
  下载文件 "$checksum_url" "$checksum_file" || 失败 "校验文件下载失败"
  expected_checksum=$(读取校验文件 "$checksum_file") || 失败 "无法读取 .sha256 校验文件"
fi
if [[ ! "$expected_checksum" =~ ^[0-9a-fA-F]{64}$ ]]; then
  失败 "Release 提供的 SHA-256 格式无效"
fi
actual_checksum=$(sha256sum "$archive_path" | awk '{print $1}')
if [ "${actual_checksum,,}" != "${expected_checksum,,}" ]; then
  失败 "SHA-256 校验失败；已拒绝安装损坏或被替换的发行包"
fi
提示 "SHA-256 校验通过"

extract_dir="$temporary_dir/extract"
mkdir -p "$extract_dir"
tar -xzf "$archive_path" -C "$extract_dir" || 失败 "无法解压发行包"
release_root=$(tar -tzf "$archive_path" | awk -F/ 'NF && !found { print $1; found=1 }')
case "$release_root" in
  ''|.|..|*'/'*) 失败 "发行包目录结构无效" ;;
esac
release_dir="$extract_dir/$release_root"
release_binary="$release_dir/bin/$target_binary_name"
release_config="$release_dir/config/webui.linux.json"
[ -f "$release_binary" ] || 失败 "发行包中没有当前架构的二进制：$target_binary_name"
[ -f "$release_config" ] || 失败 "发行包中没有 Linux 标准配置"

if [ -z "$core_release_tag" ]; then
  core_release_tag=$(读取发行包核心版本 "$release_dir/release-manifest.json") || 失败 "无法读取发行包声明的 Droidspaces 核心版本"
fi
验证Release标签 "$core_release_tag" "Droidspaces Release 标签"

target_binary="$bin_dir/droidspaces-webui"
core_binary="$bin_dir/droidspaces"
if [ -L "$target_binary" ]; then
  失败 "目标二进制是符号链接：$target_binary；为避免覆盖错误目标，安装已停止"
fi

need_core_download=0
core_changed=0
if [ "$install_core" -eq 1 ]; then
  if [ ! -x "$core_binary" ] || [ "$replace_core" -eq 1 ]; then
    need_core_download=1
  else
    提示 "保留已有 Droidspaces 核心：$core_binary"
    existing_core_version=$("$core_binary" version 2>&1 | awk '/^v[0-9][0-9A-Za-z._-]*$/ { print; exit }' || true)
    if [ -z "$existing_core_version" ]; then
      警告 "无法读取已有 Droidspaces 核心版本；发行包目标版本为 $core_release_tag"
    elif [ "$existing_core_version" != "$core_release_tag" ]; then
      警告 "已有 Droidspaces 核心为 $existing_core_version，发行包目标为 $core_release_tag；使用 --replace-core 可更新"
    fi
  fi
fi
if [ "$need_core_download" -eq 1 ] && [ -L "$core_binary" ]; then
  失败 "目标核心是符号链接：$core_binary；为避免覆盖错误目标，安装已停止"
fi

if [ "$need_core_download" -eq 1 ]; then
  core_metadata_file="$temporary_dir/droidspaces-release.json"
  core_metadata_url="$CORE_API_ROOT/tags/$core_release_tag"
  提示 "正在读取 Droidspaces 官方 Release $core_release_tag"
  下载文件 "$core_metadata_url" "$core_metadata_file" || 失败 "无法访问 Droidspaces 官方 Release；请检查网络或核心版本"

  mapfile -t core_release_fields < <(解析核心发行资产 "$core_metadata_file") || 失败 "无法找到可验证的 Droidspaces 官方后端压缩包"
  [ "${#core_release_fields[@]}" -eq 5 ] || 失败 "Droidspaces 官方 Release 元数据格式不完整"
  resolved_core_tag=${core_release_fields[0]}
  core_archive_name=${core_release_fields[1]}
  core_archive_url=${core_release_fields[2]}
  expected_core_checksum=${core_release_fields[3]}
  expected_core_size=${core_release_fields[4]}
  [ "$resolved_core_tag" = "$core_release_tag" ] || 失败 "官方 Release 返回的核心标签与请求不一致：$resolved_core_tag"
  case "$core_archive_name" in
    *[!A-Za-z0-9._-]*|'') 失败 "Droidspaces 官方压缩包文件名无效" ;;
  esac

  core_archive_path="$temporary_dir/$core_archive_name"
  提示 "正在下载 Droidspaces 官方核心：$core_archive_name"
  下载文件 "$core_archive_url" "$core_archive_path" || 失败 "Droidspaces 官方核心下载失败"
  actual_core_size=$(文件大小字节 "$core_archive_path")
  [ "$actual_core_size" = "$expected_core_size" ] || 失败 "Droidspaces 官方核心文件大小校验失败"
  actual_core_checksum=$(sha256sum "$core_archive_path" | awk '{print $1}')
  if [ "${actual_core_checksum,,}" != "${expected_core_checksum,,}" ]; then
    失败 "Droidspaces 官方核心 SHA-256 校验失败；已拒绝安装"
  fi
  提示 "Droidspaces 官方核心 SHA-256 校验通过"

  core_member=$(python3 - "$core_archive_path" "$core_architecture" "$resolved_core_tag" "$MAX_CORE_ARCHIVE_MEMBERS" "$MAX_CORE_BINARY_BYTES" <<'PY'
import sys
import tarfile

archive_path, architecture, release_tag, max_members, max_binary_bytes = sys.argv[1:]
max_members = int(max_members)
max_binary_bytes = int(max_binary_bytes)
expected_member = f"droidspaces-{release_tag}/{architecture}/droidspaces"
try:
    with tarfile.open(archive_path, "r:gz") as archive:
        all_members = archive.getmembers()
except (OSError, tarfile.TarError) as error:
    print(f"无法读取 Droidspaces 官方压缩包：{error}", file=sys.stderr)
    raise SystemExit(1)
if len(all_members) > max_members:
    print(f"官方压缩包成员数量超过 {max_members} 个上限", file=sys.stderr)
    raise SystemExit(1)
members = [member for member in all_members if member.isfile() and member.name == expected_member]
if len(members) != 1:
    print(f"官方压缩包中未找到唯一的 {expected_member}", file=sys.stderr)
    raise SystemExit(1)
if members[0].size <= 0 or members[0].size > max_binary_bytes:
    print(f"官方核心二进制大小无效或超过 {max_binary_bytes} 字节上限", file=sys.stderr)
    raise SystemExit(1)
print(members[0].name)
PY
  ) || 失败 "无法定位当前架构的 Droidspaces 核心二进制"
  core_extracted_binary="$temporary_dir/droidspaces"
  tar -xOf "$core_archive_path" "$core_member" > "$core_extracted_binary" || 失败 "无法从官方压缩包提取 Droidspaces 核心"
  [ -s "$core_extracted_binary" ] || 失败 "官方压缩包中的 Droidspaces 核心为空"
  chmod 0755 "$core_extracted_binary"
  "$core_extracted_binary" version > "$temporary_dir/droidspaces-version.txt" 2>&1 || 失败 "官方 Droidspaces 核心无法在当前架构运行"
  extracted_core_version=$(awk '/^v[0-9][0-9A-Za-z._-]*$/ { print; exit }' "$temporary_dir/droidspaces-version.txt")
  [ "$extracted_core_version" = "$resolved_core_tag" ] || \
    失败 "官方核心版本与请求不一致：期望 $resolved_core_tag，实际 ${extracted_core_version:-未知}"
fi

mkdir -p "$workspace" "$bin_dir" "$workspace/rootfs" "$workspace/Logs" "$(dirname "$config_path")"
timestamp=$(date +%Y%m%d%H%M%S)
if [ -e "$target_binary" ]; then
  binary_backup="${target_binary}.backup.${timestamp}"
  cp -p -- "$target_binary" "$binary_backup"
  提示 "已备份旧二进制：$binary_backup"
fi

binary_stage="$bin_dir/.droidspaces-webui.new.$$"
install -m 0755 "$release_binary" "$binary_stage"
mv -f -- "$binary_stage" "$target_binary"
提示 "已安装 WebUI：$target_binary"

if [ "$need_core_download" -eq 1 ]; then
  if [ -e "$core_binary" ]; then
    core_backup="${core_binary}.backup.${timestamp}"
    cp -p -- "$core_binary" "$core_backup"
    提示 "已备份旧 Droidspaces 核心：$core_backup"
  fi
  core_stage="$bin_dir/.droidspaces.new.$$"
  install -m 0755 "$core_extracted_binary" "$core_stage"
  mv -f -- "$core_stage" "$core_binary"
  core_changed=1
  提示 "已安装 Droidspaces 官方核心 $resolved_core_tag：$core_binary"
elif [ "$install_core" -eq 0 ]; then
  提示 "已跳过 Droidspaces 核心下载（--no-core）"
fi

if [ "$install_systemd" -eq 0 ] && { [ "$core_service_was_active" -eq 1 ] || [ "$webui_service_was_active" -eq 1 ]; }; then
  警告 "已按 --no-systemd 跳过服务操作；正在运行的服务仍使用替换前的二进制映像"
  警告 "确认可中断服务后，请手动执行：systemctl restart droidspaces.service droidspaces-webui.service"
fi

if [ "$write_initial_config" -eq 1 ]; then
  if [ -e "$config_path" ]; then
    config_backup="${config_path}.backup.${timestamp}"
    cp -p -- "$config_path" "$config_backup"
    提示 "已备份旧配置：$config_backup"
  fi
  config_stage="$(dirname "$config_path")/.webui.json.new.$$"
  config_token_file="$temporary_dir/webui-auth-token"
  # Create both secret-bearing files with restrictive permissions from the start.
  install -m 0600 /dev/null "$config_stage"
  install -m 0600 /dev/null "$config_token_file"
  printf '%s' "$initial_config_auth_token" > "$config_token_file"
  python3 - "$release_config" "$config_stage" "$workspace" "$bin_dir" \
    "$initial_config_mode" "$initial_config_host" "$initial_config_port" "$config_token_file" <<'PY'
import json
import os
import sys

template, destination, workspace, bin_dir, mode, host, port, auth_token_path = sys.argv[1:]
with open(template, encoding="utf-8") as source:
    config = json.load(source)
with open(auth_token_path, encoding="utf-8") as source:
    auth_token = source.read()
rootfs_dir = os.path.join(workspace, "rootfs")
config["mode"] = mode
config["host"] = host
config["port"] = int(port)
config["authToken"] = auth_token
config["workspace"] = workspace
config["corePath"] = bin_dir
config["droidspacesPath"] = os.path.join(bin_dir, "droidspaces")
config["imageRoot"] = rootfs_dir
config["templateImageRoot"] = rootfs_dir
with open(destination, "w", encoding="utf-8") as output:
    json.dump(config, output, ensure_ascii=False, indent=2)
    output.write("\n")
PY
  chmod 0600 "$config_stage"
  mv -f -- "$config_stage" "$config_path"
  提示 "已写入 Linux 标准配置：$config_path"
else
  提示 "保留已有配置：$config_path"
fi

if [ "$create_symlink" -eq 1 ]; then
  if [ -x "$core_binary" ]; then
    创建快捷链接 "$core_binary" "$core_symlink_path" "核心"
  fi
  创建快捷链接 "$target_binary" "$webui_symlink_path" "WebUI"
fi

if [ "$install_systemd" -eq 1 ]; then
  if [ ! -x "$core_binary" ]; then
    警告 "未检测到可执行的 Droidspaces 核心：$core_binary"
    警告 "未创建 systemd 服务；安装核心后再次运行脚本或手动创建服务"
  elif ! systemd可用; then
    警告 "当前系统未运行 systemd；已安装二进制，但无法创建后台服务"
  else
    mkdir -p "$systemd_unit_dir"
    core_exec=$(systemd引用 "$core_binary")
    webui_exec=$(systemd引用 "$target_binary")
    config_exec=$(systemd引用 "$config_path")
    workspace_directory=$(systemd路径 "$workspace")
    core_unit_source="$temporary_dir/droidspaces.service"
    webui_unit_source="$temporary_dir/droidspaces-webui.service"
    cat > "$core_unit_source" <<EOF
$SYSTEMD_MANAGED_MARKER
[Unit]
Description=Droidspaces Daemon
Documentation=https://github.com/$CORE_REPOSITORY
After=network-online.target
Wants=network-online.target
StartLimitIntervalSec=60
StartLimitBurst=5

[Service]
Type=simple
ExecStart=$core_exec daemon --foreground
Restart=on-failure
RestartSec=5

[Install]
WantedBy=multi-user.target
EOF
    cat > "$webui_unit_source" <<EOF
$SYSTEMD_MANAGED_MARKER
[Unit]
Description=Droidspaces WebUI
Documentation=https://github.com/$WEBUI_REPOSITORY
Requires=droidspaces.service
After=network-online.target droidspaces.service
Wants=network-online.target
StartLimitIntervalSec=60
StartLimitBurst=5

[Service]
Type=simple
User=root
WorkingDirectory=$workspace_directory
ExecStart=$webui_exec --config $config_exec
Restart=always
RestartSec=3

[Install]
WantedBy=multi-user.target
EOF
    if command -v systemd-analyze >/dev/null 2>&1; then
      systemd-analyze verify "$core_unit_source" "$webui_unit_source" || \
        失败 "生成的 systemd 单元未通过 systemd-analyze verify"
    fi
    写入systemd单元 "$core_unit_source" "$systemd_unit_dir/droidspaces.service"
    写入systemd单元 "$webui_unit_source" "$systemd_unit_dir/droidspaces-webui.service"
    systemctl daemon-reload
    systemctl enable droidspaces.service droidspaces-webui.service
    提示 "已创建并启用 systemd 服务"
    if [ "$start_systemd" -eq 1 ]; then
      if { ! systemctl is-active --quiet droidspaces.service && 同名进程运行中 droidspaces; } || \
         { ! systemctl is-active --quiet droidspaces-webui.service && 同名进程运行中 droidspaces-webui; }; then
        警告 "检测到未由对应 systemd 服务管理的同名进程；为避免重复运行，本次没有启动服务"
        警告 "停止旧进程后执行：systemctl restart droidspaces.service droidspaces-webui.service"
      else
        if [ "$core_service_was_active" -eq 0 ]; then
          systemctl start droidspaces.service
          提示 "Droidspaces systemd 服务已启动"
        elif [ "$restart_core" -eq 1 ]; then
          systemctl restart droidspaces.service
          提示 "Droidspaces systemd 服务已按 --restart-core 重启"
        elif [ "$core_changed" -eq 1 ]; then
          警告 "Droidspaces 核心已替换，但正在运行的服务未重启；新核心会在下次重启后生效"
          警告 "确认可中断容器后执行：systemctl restart droidspaces.service，或重新运行脚本并传入 --restart-core"
        else
          提示 "Droidspaces systemd 服务已在运行，未重启"
        fi

        if [ "$webui_service_was_active" -eq 1 ]; then
          systemctl restart droidspaces-webui.service
          提示 "Droidspaces WebUI systemd 服务已重启"
        else
          systemctl start droidspaces-webui.service
          提示 "Droidspaces WebUI systemd 服务已启动"
        fi
      fi
    else
      提示 "已按 --no-start 跳过本次服务启动"
    fi
  fi
fi

printf '\n安装完成。\n'
printf 'Droidspaces 核心：%s\n' "$core_binary"
printf 'WebUI 二进制：%s\n' "$target_binary"
printf '配置文件：%s\n' "$config_path"
printf '手动启动：%s --config %s\n' "$target_binary" "$config_path"
if [ "$write_initial_config" -eq 1 ]; then
  if [ "$initial_config_mode" = "public" ]; then
    printf 'WebUI 监听：0.0.0.0:%s（请使用宿主机 IP 访问）\n' "$initial_config_port"
  else
    printf 'WebUI 监听：127.0.0.1:%s\n' "$initial_config_port"
  fi
  if [ "$non_interactive" -eq 0 ] && [ -t 1 ]; then
    printf '授权密钥：%s\n' "$initial_config_auth_token"
    if [ "$initial_config_mode" = "public" ]; then
      printf '请立即保存该密钥；公网访问还需要自行配置防火墙和路由器端口映射。\n'
    else
      printf '请立即保存该密钥；当前仅监听本机地址。\n'
    fi
  else
    printf '已生成授权密钥并以 0600 权限保存到配置文件；非交互模式不会将密钥写入标准输出。\n'
  fi
fi
if [ "$install_systemd" -eq 1 ] && systemd可用 && [ -x "$core_binary" ]; then
  printf '服务状态：systemctl status droidspaces.service droidspaces-webui.service\n'
fi
