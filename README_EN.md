# Droidspaces WebUI

<p align="right">
  <a href="./README.md"><kbd>Chinese Documentation</kbd></a>
</p>

Droidspaces WebUI is a local container management interface for Android and Linux hosts. It is served by a standalone Go HTTP service and manages Droidspaces containers, RootFS templates, networking, background tasks, terminals, and diagnostics.

> This is a root-level management entry point. Run it only on trusted devices and networks. A strong `authToken` is required when listening on a LAN or the public Internet.

<p align="center">
  <img src="jpg/001.jpg" alt="Droidspaces WebUI overview" width="31%">
  <img src="jpg/002.jpg" alt="Droidspaces WebUI container management" width="31%">
  <img src="jpg/003.jpg" alt="Local RootFS template management" width="31%">
</p>
<p align="center">
  <img src="jpg/0045.jpg" alt="Droidspaces Official image information" width="31%">
  <img src="jpg/004.jpg" alt="lxc-image CN image list" width="31%">
</p>

## Features

- Container overview, list, details, start, stop, restart, delete, and boot order management.
- Local templates, backups, and cloud RootFS repository management, including download, upload, delete, backup export, and template conversion.
- Droidspaces Official image information plus browsing and searching of upstream Linux Containers lxc-image `rootfs.tar.xz` cloud images.
- The lxc-image download source can be switched to the Nanjing University CN mirror in Settings. The catalog remains official, while image files are downloaded from `https://mirror.nju.edu.cn/lxc-images/`.
- Cloud image NoCloud initialization: users, passwords, SSH, first-boot commands, package installation, static NAT network configuration, and custom YAML.
- NAT, port forwarding, DNS, automatic core upstream detection, container configuration, system service inspection, and diagnostics.
- WebSocket PTY terminal, background task progress and logs, and Android host resource, battery, and network overviews.

## How It Works

The browser never connects directly to the Droidspaces native socket. The communication path is:

```text
Browser <-> WebUI HTTP / WebSocket <-> Droidspaces socketd
                                      \-> Droidspaces CLI / workspace fallback
```

WebUI first connects to the abstract Unix socket `@droidspaces-socketd-backend` created by the Droidspaces daemon to read live container status, details, and events and to perform lifecycle actions. When socketd is unavailable, the service falls back to a restricted Droidspaces CLI and the configured `workspace`, preserving basic status reading and recovery.

WebUI must therefore run in the same Android/Linux host environment as the Droidspaces daemon. Another computer, container, or build host cannot directly access the Android host's abstract Unix socket. To access an Android WebUI from a computer, use a browser on the device or `adb forward`.

## Repository Layout

```text
cmd/droidspaces-webui/  Go service entry point
internal/config/        Configuration loading, environment variables, and CLI overrides
internal/socketd/       Droidspaces socketd protocol client
internal/web/           HTTP API, tasks, RootFS, containers, and embedded static assets
internal/web/static/    Browser UI
config/                 Example configuration
scripts/                Local and Android smoke tests
jpg/                    Phone UI screenshots used by the README
```

## Prerequisites

Build environment:

- Go `1.22` or later.
- `make` and Git. Local smoke tests also require `curl` or `wget`, `python3`, and `tar`.
- Android builds use `GOOS=linux CGO_ENABLED=0`, matching the existing Android Linux userspace deployment model for Droidspaces. Common ABIs are wrapped by the Makefile.

Runtime environment:

- Manual operation requires an installed, executable Droidspaces core binary. The Linux installer downloads the matching core binary directly from the official backend archive; it does not use `.deb` or `apt`.
- The WebUI process needs access to the Droidspaces workspace. Android usually needs a root context via `su`.
- When `socketdEnabled: true`, WebUI and the Droidspaces daemon must run in user and SELinux contexts that can connect to the same socket.

## Quick Start

Run from the repository root:

```sh
make build
./output/droidspaces-webui --config ./output/webui.json --write-default-config
# Edit ./output/webui.json and set the actual Droidspaces and workspace paths.
./output/droidspaces-webui --config ./output/webui.json
```

The default configuration listens on `127.0.0.1:9090`. Open:

```text
http://127.0.0.1:9090
```

`--write-default-config` writes paths appropriate for the current platform. Before running, still check `droidspacesPath`, `workspace`, image directories, and the authentication token. The paths must match the Droidspaces installation on the target machine.

### Default Directory Layout

New deployments use the workspace as the root for data and configuration:

```text
Android: /data/local/Droidspaces/
  bin/droidspaces
  bin/droidspaces-webui
  rootfs/
  webui.json

Linux: /var/lib/Droidspaces/
  bin/droidspaces
  bin/droidspaces-webui
  rootfs/
  webui.json
```

Linux can additionally symlink both binaries into `/usr/sbin/droidspaces` and `/usr/sbin/droidspaces-webui`, but `corePath` and `droidspacesPath` should continue to point to the real files in `/var/lib/Droidspaces/bin/`.

## Detailed Build Instructions

### Native Linux Build

```sh
go version
go mod download
make build
```

Output:

```text
output/droidspaces-webui
```

Use the repository Linux configuration template for development:

```sh
make default-config
# Edit output/webui.json so its paths match the current environment.
make run
```

`make default-config` copies [config/webui.linux.example.json](config/webui.linux.example.json) to `output/webui.json`, ready for `make run`. To generate templates for both platforms:

```sh
make config-templates
```

This writes `output/webui.linux.json` and `output/webui.android.json`. At runtime, `--write-default-config` still creates defaults for the actual platform.

### Android arm64 Cross-Compilation

```sh
go version
make android-arm64 android-config
```

Output:

```text
output/droidspaces-webui-android-arm64
output/webui.android.json
```

The Makefile is equivalent to:

```sh
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 \
  go build -trimpath -ldflags="-s -w -X main.webVersion=v0.1.0" \
  -o output/droidspaces-webui-android-arm64 \
  ./cmd/droidspaces-webui
```

The build version is shown on the runtime status page. Pass `VERSION` to choose a release version:

```sh
make VERSION=v0.1.0 android-arm64
```

### Build All Architectures

```sh
make all
# Equivalent to make release
```

This target retains the current host build and creates static release binaries for common Linux and Android architectures:

```text
output/droidspaces-webui
output/droidspaces-webui-linux-amd64
output/droidspaces-webui-linux-arm64
output/droidspaces-webui-linux-armv7
output/droidspaces-webui-linux-386
output/droidspaces-webui-linux-riscv64
output/droidspaces-webui-android-arm64
output/droidspaces-webui-android-armv7
output/droidspaces-webui-android-amd64
output/droidspaces-webui-android-386
```

Android `arm64` maps to `arm64-v8a`, and `armv7` maps to `armeabi-v7a`. `amd64` and `386` are mainly for Android emulators and a small number of x86 devices. `make all` only builds the program; run `make config-templates` separately when both configuration templates are needed.

### Available Make Targets

| Command | Result |
| --- | --- |
| `make all` / `make release` | Builds the current host, Linux amd64/arm64/armv7/386/riscv64, and Android arm64/armv7/amd64/386 in one run. |
| `make build` | Builds the executable for the current host. |
| `make linux-amd64`, `make linux-arm64`, `make linux-armv7`, `make linux-386`, `make linux-riscv64` | Builds the static binary for the selected Linux architecture. |
| `make android-arm64`, `make android-armv7`, `make android-amd64`, `make android-386` | Builds a static Linux ELF binary for the selected Android ABI. |
| `make default-config` | Writes the Linux standard configuration to `output/webui.json`. |
| `make linux-config` | Writes the Linux standard configuration to `output/webui.linux.json`. |
| `make android-config` | Writes the Android standard configuration to `output/webui.android.json`. |
| `make config-templates` | Writes both Linux and Android configuration templates. |
| `make package` | Builds every release architecture and creates an archive that supports Droidspaces `v6.5.0`. |
| `make run` | Runs the development server using `output/webui.json`. |
| `make test` | Runs all Go unit and interface tests. |
| `make local-smoke` | Starts a temporary local service and verifies the main API and UI static assets. |
| `make android-smoke` | Pushes smoke-specific binary/configuration files to a connected Android device and verifies the API. |
| `make clean` | Deletes `output/` build artifacts. |

### Binary Release Archive

The first release archive uses runtime WebUI version `v0.1.0-ds6.5.0` and targets Droidspaces core `v6.5.0`:

```sh
make package
```

Output:

```text
output/Droidspaces-WebUI-v0.1.0-ds6.5.0.tar.gz
output/Droidspaces-WebUI-v0.1.0-ds6.5.0.tar.gz.sha256
```

The archive contains no local configuration, containers, templates, or RootFS cache. It contains nine Linux/Android architecture binaries, two standard configuration templates, Chinese and English documentation (`README.md`, `README_EN.md`), `install-linux.sh`, the Android/Magisk launcher `android-start-webui.sh`, the official core version declared by the release package, release notes, and `SHA256SUMS`. Verify the extracted files:

```sh
tar -xzf output/Droidspaces-WebUI-v0.1.0-ds6.5.0.tar.gz -C /tmp
cd /tmp/Droidspaces-WebUI-v0.1.0-ds6.5.0
sha256sum -c SHA256SUMS
```

After downloading the archive, verify the archive itself from `output/` or the download directory:

```sh
sha256sum -c Droidspaces-WebUI-v0.1.0-ds6.5.0.tar.gz.sha256
```

Later releases can override the version:

```sh
make package RELEASE_VERSION=v0.2.0
```

### Linux Automated Installation

The repository includes [scripts/install-linux.sh](scripts/install-linux.sh). For the current Linux CPU architecture, it:

- Downloads the backend archive from the official [Droidspaces-OSS Release](https://github.com/ravindu644/Droidspaces-OSS/releases/latest) declared by the release package, validates the GitHub API file size and SHA-256 digest, and extracts only the matching `droidspaces` binary. It does not use `.deb`, `apt`, or a system package.
- Downloads and validates WebUI from this project's GitHub Release.
- Installs the core and WebUI in `/var/lib/Droidspaces/bin/`, creates `/usr/sbin` symlinks, and writes, enables, and starts `droidspaces.service` and `droidspaces-webui.service`. systemd restarts either service if it exits unexpectedly.
- On the first configuration write, or with `--replace-config`, asks for the workspace, exposure scope, listening port, and authorization key. Pressing Enter accepts fixed workspace `/var/lib/Droidspaces`, public listener `0.0.0.0:9090`, and an eight-character random token.

An existing core is preserved by default; it is downloaded and replaced only with `--replace-core`. An existing configuration is also preserved by default. Re-running the script does not restart a running core service unless `--restart-core` is explicitly supplied.

Download the script before running it so it can be inspected first:

```sh
curl -fsSLO https://raw.githubusercontent.com/loeveo/Droidspaces-WebUI/main/scripts/install-linux.sh
sudo bash install-linux.sh
```

The default installation paths are `/var/lib/Droidspaces/bin/droidspaces` and `/var/lib/Droidspaces/bin/droidspaces-webui`. The first installation writes `/var/lib/Droidspaces/webui.json` and creates `/usr/sbin/droidspaces` and `/usr/sbin/droidspaces-webui` symlinks. It displays the initial settings confirmation; pressing Enter accepts public `0.0.0.0:9090` and a generated eight-character token. The token is displayed once after installation. Existing binaries are backed up, and existing configuration is retained, so later script runs do not ask again or change exposure settings.

Common options:

```sh
# Install a specific WebUI Release tag.
sudo bash install-linux.sh --version v0.1.0

# Use a specific official Droidspaces core version and explicitly replace the existing core.
sudo bash install-linux.sh --core-version v6.5.0 --replace-core

# After updating the core, explicitly allow the core service to restart and switch immediately.
sudo bash install-linux.sh --core-version v6.5.0 --replace-core --restart-core

# Do not create /usr/sbin symlinks.
sudo bash install-linux.sh --no-symlink

# Replace an existing configuration with the Linux template from the Release (the old one is backed up).
sudo bash install-linux.sh --replace-config

# Non-interactive installation: use public defaults and a random token.
# The token is written only to /var/lib/Droidspaces/webui.json with mode 0600 and is not logged.
sudo bash install-linux.sh --non-interactive
```

Both services are enabled and started by default. Check their state immediately:

```sh
sudo systemctl status droidspaces.service droidspaces-webui.service
```

The official Linux core has a fixed workspace at `/var/lib/Droidspaces`, so the installer does not accept another `--workspace` value. This prevents container, log, and PID state from splitting between WebUI and the core. Public listening configures only the WebUI listening address; firewall rules, cloud security groups, and router port forwarding remain the administrator's responsibility. An eight-character random token is suitable only as an initial credential. Before long-term public exposure, replace it in `webui.json` with a longer, strong token. If the machine lacks systemd, the script still installs verified binaries and configuration and reports that service registration was skipped. To manage processes yourself, pass `--no-systemd`; to only write units without starting them, pass `--no-start`. For the complete parameter list:

```sh
bash install-linux.sh --help
```

## Android Deployment

The following example does not replace the Droidspaces core; it replaces only the WebUI binary and configuration. Build arm64 on the computer first, then check `output/webui.android.json` against the device's actual directories.

```sh
make android-arm64 android-config

adb push output/droidspaces-webui-android-arm64 /data/local/tmp/droidspaces-webui
adb push output/webui.android.json /data/local/tmp/webui.json
adb push scripts/start-android-webui.sh /data/local/tmp/start-android-webui.sh

adb shell su -c '
  mkdir -p /data/local/Droidspaces/bin &&
  cp /data/local/tmp/droidspaces-webui /data/local/Droidspaces/bin/droidspaces-webui &&
  cp /data/local/tmp/webui.json /data/local/Droidspaces/webui.json &&
  cp /data/local/tmp/start-android-webui.sh /data/local/Droidspaces/bin/start-android-webui.sh &&
  chmod 755 /data/local/Droidspaces/bin/droidspaces-webui /data/local/Droidspaces/bin/start-android-webui.sh
'
```

Start the service with the launcher:

```sh
adb shell su -c /data/local/Droidspaces/bin/start-android-webui.sh
```

By default, the launcher starts `/data/local/Droidspaces/bin/droidspaces-webui --config /data/local/Droidspaces/webui.json` in the background, appends launcher and WebUI output to `/data/local/Droidspaces/Logs/webui.log`, and records `/data/local/Droidspaces/webui.pid`. It validates an existing instance through its PID and `/proc/<pid>/cmdline`; it neither stops an unverified process nor starts another copy of the same WebUI. For interactive diagnosis, run it in the foreground:

```sh
adb shell su -c '/data/local/Droidspaces/bin/start-android-webui.sh --foreground'
```

### Magisk Autostart

On a rooted device with Magisk installed, copy the same launcher into `service.d`:

```sh
adb shell su -c '
  mkdir -p /data/adb/service.d &&
  cp /data/local/Droidspaces/bin/start-android-webui.sh /data/adb/service.d/droidspaces-webui.sh &&
  chmod 755 /data/adb/service.d/droidspaces-webui.sh
'
```

When invoked from `service.d`, the launcher waits for `sys.boot_completed` before starting (180 seconds by default). Use `--wait` for the same behavior during a manual invocation, or set `DS_WEBUI_BOOT_WAIT_TIMEOUT=0` to wait indefinitely. Do not use `--foreground` from `service.d`; it is for interactive diagnosis.

For access from a browser on the computer, create an ADB port forward:

```sh
adb forward tcp:9090 tcp:9090
```

Then open `http://127.0.0.1:9090`. The launcher uses the configured listen address and port; access from a computer still requires an ADB forward for that port.

### Android Smoke Test

When a device is connected through ADB, has `su`, and the build artifacts are available:

```sh
make android-arm64 android-config
make android-smoke
```

The script uses these smoke-specific remote files by default:

```text
/data/local/Droidspaces/bin/droidspaces-webui-smoke-android-arm64
/data/local/Droidspaces/webui-smoke.json
/data/local/Droidspaces/Logs/webui-smoke.log
```

It does not overwrite the production `webui.json`. Common overrides:

```sh
ADB=adb DEVICE_SERIAL=serial AUTH_TOKEN=change-me make android-smoke
HOST_PORT=19090 DEVICE_PORT=19090 make android-smoke
KEEP_RUNNING=1 make android-smoke
PUSH=0 make android-smoke
```

## Configuration

Configuration precedence from lowest to highest is: built-in defaults, JSON configuration file, environment variables, and command-line arguments.

| Field | Description |
| --- | --- |
| `mode` | `local` listens only on the local host; `public` is for LAN or public Internet access. |
| `host` / `port` | HTTP listening address and port. The default is `127.0.0.1:9090`. |
| `authToken` | API Bearer token. In `public` mode, an empty value generates a temporary eight-character token and writes it to the startup log. Production deployments should use a fixed, strong token. |
| `uiLanguage` | WebUI interface language. Supported values are `zh-CN` (Simplified Chinese, the default) and `en` (English). If the configuration file omits this field, the first WebUI visit asks for a language and writes the choice back to the configuration file. It can also be changed online in System Settings and takes effect immediately. |
| `droidspacesPath` | Full path to the Droidspaces core binary. Defaults to `workspace/bin/droidspaces`. |
| `corePath` | Core binary directory. When empty, uses the parent directory of `droidspacesPath`. Defaults to `workspace/bin`. |
| `workspace` | Droidspaces workspace. Android defaults to `/data/local/Droidspaces`; Linux defaults to `/var/lib/Droidspaces`. |
| `imageRoot` | Compatibility field for the legacy Core image scan directory. It is not shown in WebUI Settings; downloaded templates and cloud-list cache use `templateImageRoot`. |
| `templateImageRoot` | Local reusable template directory. Defaults to `workspace/rootfs`. |
| `socketdEnabled` | Whether to prefer Droidspaces socketd. Real Android deployments usually set this to `true`. |
| `rootfsSkipTLSVerify` | May be temporarily set to `true` when a minimal Android environment lacks CA certificates. Set it to `false` when a complete trust chain is available. |
| `defaultNatCIDR` | Compatibility field for the NAT address pool. It must currently be `172.28.0.0/16`. |
| `defaultNatThirdOctet` | Third octet for new NAT containers, resulting in `172.28.<value>.x`. Valid range: `1-254`. |
| `nestedAndroidNatCompat` | Disabled by default. Enable only when WebUI runs in a Linux container and also creates NAT containers there. WebUI uses a temporary shared policy-routing adjustment for the Android default egress; it does not change the DS core, firewall, NAT/DNAT, or images. The outer Android instance must still provide the FORWARD policy. |
| `batteryDirectPowerSupported` | Set to `true` on devices that support direct/bypass power so the current power state is identified correctly. |
| `batterySeriesCells` | Number of battery cells in series. `0` enables automatic detection; other valid values are `1-6`. |
| `overviewPowerEnabled` | Whether the overview displays the Current Power card. Disabling it does not affect enabled battery monitoring. |
| `batteryMonitoringEnabled` | Whether to enable battery monitoring. Disabling it hides the entry and page and stops background sampling and history collection. |
| `batteryDetailEnabled` | Retained compatibility field for detailed battery information. |
| `batteryStatsSampleSeconds` | Battery-statistics sampling period, from `1-60` seconds. |
| `batteryStatsWriteMinutes` | Battery-statistics disk-write period, from `5-1440` minutes. |
| `batteryStatsRetentionDays` | Power history is stored in local-date files. Retains the latest `1-365` days, `7` by default; raise it to deliberately retain more history. |
| `overviewRefreshSeconds` | Refresh period while the overview is visible, from `1-60` seconds. |
| `rootfsRepositories` | Cloud RootFS repository list. It includes Droidspaces Official and lxc-image by default. |

The standard templates are [config/webui.linux.example.json](config/webui.linux.example.json) and [config/webui.android.example.json](config/webui.android.example.json). The old [config/webui.example.json](config/webui.example.json) remains an Android-compatible path. Example fields named `_说明` (the Chinese description suffix) or ending in `_说明` are ignored by the program.

### lxc-image CN Acceleration

New Simplified Chinese configurations default to the Nanjing University lxc-image mirror; new English configurations default to the upstream source. A source explicitly selected in System Settings is preserved when the interface language changes.

After enabling CN acceleration in the WebUI system settings, lxc-image downloads images from:

```text
https://mirror.nju.edu.cn/lxc-images/
```

This switches the existing lxc-image source rather than adding a second repository alongside it. The image catalog is still parsed from the official Linux Containers SimpleStreams catalog, so available images remain aligned with upstream.

### Cloud Image Storage and Refresh

Downloaded cloud templates are organized by source in `templateImageRoot`:

```text
rootfs/
  droidspaces-official/
  lxc-image/
    cloud/
    default/
```

Cloud image-list metadata cache and templates reside in the same `templateImageRoot/lxc-image/` directory, for example `rootfs/lxc-image/catalog-<hash>.json`. Every day at `00:10` in the device's local time, WebUI removes stale list cache and fetches the catalog again. This scheduled operation does not delete downloaded template archives in `cloud/` or `default/`.

### Environment Variables and Command Line

Common environment variables:

```text
DS_WEBUI_CONFIG
DS_WEBUI_LISTEN
DS_WEBUI_MODE
DS_WEBUI_HOST
DS_WEBUI_PORT
DS_WEBUI_DROIDSPACES
DS_WEBUI_AUTH_TOKEN
DS_WEBUI_UI_LANGUAGE
DS_WEBUI_WORKSPACE
DS_WEBUI_CORE_PATH
DS_WEBUI_IMAGE_ROOT
DS_WEBUI_TEMPLATE_IMAGE_ROOT
DS_WEBUI_SOCKETD_ENABLED
DS_WEBUI_ROOTFS_SKIP_TLS_VERIFY
DS_WEBUI_DEFAULT_NAT_CIDR
DS_WEBUI_DEFAULT_NAT_THIRD_OCTET
DS_WEBUI_NESTED_ANDROID_NAT_COMPAT
DS_WEBUI_BATTERY_DIRECT_POWER_SUPPORTED
DS_WEBUI_BATTERY_SERIES_CELLS
DS_WEBUI_BATTERY_DETAIL_ENABLED
DS_WEBUI_BATTERY_STATS_SAMPLE_SECONDS
DS_WEBUI_BATTERY_STATS_WRITE_MINUTES
DS_WEBUI_BATTERY_STATS_RETENTION_DAYS
DS_WEBUI_OVERVIEW_REFRESH_SECONDS
```

Common command-line overrides:

```sh
./output/droidspaces-webui \
  --config /data/local/Droidspaces/webui.json \
  --listen 127.0.0.1:9090 \
  --droidspaces /data/local/Droidspaces/bin/droidspaces
```

LAN listening example:

```sh
./output/droidspaces-webui \
  --config /data/local/Droidspaces/webui.json \
  --listen 0.0.0.0:9090 \
  --auth-token 'replace-with-a-strong-token'
```

## Verification and Troubleshooting

### Local Smoke Verification

```sh
make build
make local-smoke
```

The script creates a temporary workspace and fake Droidspaces binary, then verifies status, containers, RootFS, events, tasks, host information, boot order, and static assets. To retain the temporary directory for log inspection:

```sh
KEEP_WORKDIR=1 make local-smoke
```

### API Health Check

Without a token:

```sh
curl -fsS http://127.0.0.1:9090/api/status
```

With a token:

```sh
curl -fsS \
  -H 'Authorization: Bearer replace-with-your-token' \
  http://127.0.0.1:9090/api/status
```

`backend: ready` means socketd is available. `cli-fallback` or `workspace-fallback` means WebUI switched to a degraded read path. Use the Diagnostics page to inspect socketd, Droidspaces version, paths, and permissions.

## Checks Before Pushing

When this repository already contains user workspace changes, stage only the files intended for this commit. Do not accidentally push local binaries, configuration, or temporary logs.

```sh
git status --short
git diff --check
make test
make build
make local-smoke
```

After checking the proposed commit, stage the relevant files:

```sh
git add README.md README_EN.md jpg/
git diff --cached --check
git commit -m 'Document Droidspaces WebUI build and deployment'
```

For an initial push without a configured remote, replace the placeholder with the real repository address:

```sh
git remote add origin <repository-url>
git branch -M main
git push -u origin main
```

When an upstream already exists, use `git push`. Add application changes separately after review. `output/`, logs, and temporary files are excluded by [.gitignore](.gitignore). Before pushing, still inspect `git status` to make sure no authentication tokens, private device paths, or unrelated local work have been staged.

## Security Boundary

- Do not expose this root-level management page to untrusted networks.
- `public` mode must use a fixed, strong `authToken` and network-layer access control.
- `rootfsSkipTLSVerify` is only for Android compatibility when CA certificates are missing. Disable it whenever certificate validation is available.
- Cloud-init user data, passwords, SSH settings, and first-boot commands change the container system. Use only trusted images and check configuration before creating a container.

## Scope

WebUI manages headless containers on Android and Linux. Android desktops, launchers, desktop interception, and Wayland interaction are outside this project's scope.
