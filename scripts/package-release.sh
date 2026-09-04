#!/usr/bin/env bash
set -euo pipefail

release_name=${RELEASE_NAME:-Droidspaces-WebUI}
release_version=${RELEASE_VERSION:-}
supported_core_version=${SUPPORTED_CORE_VERSION:-}
out_dir=${OUT_DIR:-output}

fail() {
  echo "release package: $*" >&2
  exit 1
}

[ -n "$release_version" ] || fail "RELEASE_VERSION is required"
[ -n "$supported_core_version" ] || fail "SUPPORTED_CORE_VERSION is required"
case "$release_name-$release_version-$supported_core_version" in
  *[!A-Za-z0-9._-]*) fail "release name and versions may contain only letters, digits, dot, underscore, and hyphen" ;;
esac

release_id="${release_name}-${release_version}-ds${supported_core_version#v}"
release_root="$out_dir/release"
staging_dir="$release_root/$release_id"
archive="$out_dir/$release_id.tar.gz"
archive_checksum="$archive.sha256"

binaries=(
  droidspaces-webui-linux-amd64
  droidspaces-webui-linux-arm64
  droidspaces-webui-linux-armv7
  droidspaces-webui-linux-386
  droidspaces-webui-linux-riscv64
  droidspaces-webui-android-arm64
  droidspaces-webui-android-armv7
  droidspaces-webui-android-amd64
  droidspaces-webui-android-386
)

for binary in "${binaries[@]}"; do
  [ -s "$out_dir/$binary" ] || fail "missing compiled binary: $out_dir/$binary"
done
[ -s "$out_dir/webui.linux.json" ] || fail "missing Linux template: $out_dir/webui.linux.json"
[ -s "$out_dir/webui.android.json" ] || fail "missing Android template: $out_dir/webui.android.json"
[ -f README.md ] || fail "README.md is required"
[ -f README_EN.md ] || fail "README_EN.md is required"
[ -x scripts/install-linux.sh ] || fail "scripts/install-linux.sh is required and must be executable"
[ -x scripts/start-android-webui.sh ] || fail "scripts/start-android-webui.sh is required and must be executable"

mkdir -p "$release_root"
rm -rf "$staging_dir"
rm -f "$archive"
rm -f "$archive_checksum"
mkdir -p "$staging_dir/bin" "$staging_dir/config"

for binary in "${binaries[@]}"; do
  install -m 0755 "$out_dir/$binary" "$staging_dir/bin/$binary"
done
install -m 0644 "$out_dir/webui.linux.json" "$staging_dir/config/webui.linux.json"
install -m 0644 "$out_dir/webui.android.json" "$staging_dir/config/webui.android.json"
install -m 0644 README.md "$staging_dir/README.md"
install -m 0644 README_EN.md "$staging_dir/README_EN.md"
install -m 0755 scripts/install-linux.sh "$staging_dir/install-linux.sh"
install -m 0755 scripts/start-android-webui.sh "$staging_dir/android-start-webui.sh"
printf '{\n  "droidspacesRepository": "ravindu644/Droidspaces-OSS",\n  "droidspacesReleaseTag": "%s"\n}\n' \
  "$supported_core_version" > "$staging_dir/release-manifest.json"

printf '%s\n' \
  "# ${release_name} ${release_version}" \
  "" \
  "这是面向 Droidspaces ${supported_core_version} 的 WebUI 二进制发行包。" \
  "" \
  "## Linux 一键安装" \
  "" \
  "安装脚本会从本项目和官方 Droidspaces GitHub Releases 下载与当前 CPU 架构匹配的 WebUI 与核心，校验 SHA-256 后直接安装二进制，并创建、启用和启动 systemd 服务。核心不使用 .deb 或 apt 安装。首次写入配置时，直接回车会使用 /var/lib/Droidspaces、公开监听 0.0.0.0:9090 和随机 8 位 Token。" \
  "" \
  "\`\`\`sh" \
  "sudo bash ./install-linux.sh" \
  "\`\`\`" \
  "" \
  "默认安装目录为 /var/lib/Droidspaces；完整参数可通过 \`bash install-linux.sh --help\` 查看。" \
  "" \
  "## 内容" \
  "" \
  "- bin/：Linux 与 Android 常见 CPU 架构的静态 WebUI 二进制。" \
  "- config/webui.linux.json：/var/lib/Droidspaces 的 Linux 标准配置。" \
  "- config/webui.android.json：/data/local/Droidspaces 的 Android 标准配置。" \
  "- README.md / README_EN.md：中文与英文说明文档。" \
  "- release-manifest.json：本发行包锁定的官方 Droidspaces Release 标签。" \
  "- install-linux.sh：Linux 一键安装脚本。" \
  "- android-start-webui.sh：可放入 Magisk service.d 的 Android WebUI 启动脚本。" \
  "- SHA256SUMS：发行包内每个文件的 SHA-256 校验和。" \
  "" \
  "## 架构选择" \
  "" \
  "- Linux 服务器和 PC：droidspaces-webui-linux-amd64。" \
  "- Linux ARM64 开发板：droidspaces-webui-linux-arm64。" \
  "- Android arm64-v8a：droidspaces-webui-android-arm64。" \
  "- Android armeabi-v7a：droidspaces-webui-android-armv7。" \
  "- Android x86/x86_64：使用对应的 android-386/android-amd64。" \
  "" \
  "Android 产物刻意使用 Linux ELF，因为 Droidspaces 运行于 Android 的 Linux 用户空间。" \
  "手动安装时，将对应二进制复制到平台的 bin 目录，将对应配置复制为工作区的 webui.json；Android 可使用 android-start-webui.sh 启动或放入 Magisk service.d。" \
  > "$staging_dir/RELEASE.md"

(
  cd "$staging_dir"
  find . -type f ! -name SHA256SUMS -print0 | LC_ALL=C sort -z | xargs -0 sha256sum > SHA256SUMS
)
(
  cd "$release_root"
  tar -czf "../$release_id.tar.gz" "$release_id"
)
(
  cd "$out_dir"
  sha256sum "$release_id.tar.gz" > "$(basename "$archive_checksum")"
)

echo "[+] Release package: $archive"
echo "[+] Archive checksum: $archive_checksum"
echo "[+] Verify: tar -xzf $archive && (cd $release_id && sha256sum -c SHA256SUMS)"
