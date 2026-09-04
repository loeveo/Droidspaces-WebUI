# Droidspaces WebUI

<p align="right">
  <a href="./README_EN.md"><kbd>English Documentation</kbd></a>
</p>

Droidspaces WebUI 是面向 Android 和 Linux 宿主机的本地容器管理界面。它以一个独立的 Go HTTP 服务提供浏览器界面，负责管理 Droidspaces 容器、RootFS 模板、网络、后台任务、终端和诊断信息。

> 这是 root 级管理入口。请只在受信任设备和网络中运行；对局域网或公网监听时必须设置强 `authToken`。

<p align="center">
  <img src="jpg/001.jpg" alt="Droidspaces WebUI 概览页" width="31%">
  <img src="jpg/002.jpg" alt="Droidspaces WebUI 容器管理页" width="31%">
  <img src="jpg/003.jpg" alt="本地 RootFS 模板管理" width="31%">
</p>
<p align="center">
  <img src="jpg/0045.jpg" alt="Droidspaces Official 镜像说明" width="31%">
  <img src="jpg/004.jpg" alt="lxc-image CN 镜像列表" width="31%">
</p>

## 功能

- 容器概览、列表、详情、启动、停止、重启、删除和启动顺序管理。
- 本地模板、备份和云端 RootFS 仓库管理；支持下载、上传、删除、导出备份及转换模板。
- Droidspaces Official 镜像说明展示，以及 lxc-image（上游 Linux Containers）`rootfs.tar.xz` 云端镜像浏览与搜索。
- 设置中可将 lxc-image 的下载源切换为南京大学 CN 镜像；目录继续使用官方 catalog，镜像文件从 `https://mirror.nju.edu.cn/lxc-images/` 下载。
- Cloud 镜像 NoCloud 初始化：用户、密码、SSH、首启命令、包安装、静态 NAT 网络配置和自定义 YAML。
- NAT、端口转发、DNS、核心自动上游检测、容器配置、系统服务检查和诊断工具。
- WebSocket PTY 终端、后台任务进度与日志，以及 Android 主机资源、电池和网络概览。

## 工作方式

浏览器不会直接连接 Droidspaces 原生 socket。通信路径如下：

```text
Browser <-> WebUI HTTP / WebSocket <-> Droidspaces socketd
                                      \-> Droidspaces CLI / workspace fallback
```

WebUI 优先访问 Droidspaces daemon 创建的抽象 Unix socket `@droidspaces-socketd-backend`，用于读取实时容器状态、详情、事件并执行生命周期操作。socketd 不可用时，服务会回退到受限的 Droidspaces CLI 和配置中的 `workspace`，以便保留基础状态读取与恢复能力。

因此，WebUI 必须运行在与 Droidspaces 守护进程相同的 Android/Linux 宿主环境中。不能从另一台电脑、容器或构建机直接访问 Android 宿主的抽象 Unix socket；电脑访问 Android WebUI 时应使用浏览器本机访问或 `adb forward`。

## 仓库结构

```text
cmd/droidspaces-webui/  Go 服务入口
internal/config/        配置加载、环境变量和命令行覆盖
internal/socketd/       Droidspaces socketd 协议客户端
internal/web/           HTTP API、任务、RootFS、容器和静态资源嵌入
internal/web/static/    浏览器 UI
config/                 示例配置
scripts/                本地与 Android 冒烟测试
jpg/                    README 使用的手机界面截图
```

## 前置条件

构建环境：

- Go `1.22` 或更新版本。
- `make`、Git；本地冒烟测试还需要 `curl` 或 `wget`、`python3`、`tar`。
- Android 构建使用 `GOOS=linux CGO_ENABLED=0`，与 Droidspaces 的现有 Android Linux 用户空间部署方式一致；常用 ABI 已封装在 Makefile 中。

运行环境：

- 手动运行时需要已安装并可执行 Droidspaces 核心程序；Linux 一键安装脚本会直接从官方后端压缩包下载匹配的核心二进制，不使用 `.deb` 或 `apt`。
- WebUI 进程需要访问 Droidspaces workspace；Android 一般需要通过 `su` 以 root 上下文启动。
- `socketdEnabled: true` 时，WebUI 与 Droidspaces daemon 需要处于能连接同一 socket 的用户/SELinux 上下文。

## 快速开始

在仓库根目录执行：

```sh
make build
./output/droidspaces-webui --config ./output/webui.json --write-default-config
# 编辑 ./output/webui.json，填入实际的 Droidspaces 与 workspace 路径
./output/droidspaces-webui --config ./output/webui.json
```

默认配置监听 `127.0.0.1:9090`。打开：

```text
http://127.0.0.1:9090
```

`--write-default-config` 会生成当前平台的默认路径。运行前仍应检查 `droidspacesPath`、`workspace`、镜像目录和认证令牌；路径必须与目标机器上的 Droidspaces 安装保持一致。

### 默认目录布局

新部署统一以工作区作为数据和配置根目录：

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

Linux 可额外将两个二进制软链接到 `/usr/sbin/droidspaces` 与 `/usr/sbin/droidspaces-webui`，但 `corePath` 和 `droidspacesPath` 应继续指向 `/var/lib/Droidspaces/bin/` 中的实际文件。

## 详细编译

### 本机 Linux 构建

```sh
go version
go mod download
make build
```

产物：

```text
output/droidspaces-webui
```

使用仓库中的 Linux 标准配置进行开发运行：

```sh
make default-config
# 编辑 output/webui.json，使路径与当前环境一致
make run
```

`make default-config` 复制 [config/webui.linux.example.json](config/webui.linux.example.json) 到 `output/webui.json`，可直接配合 `make run` 使用。需要同时生成两份平台模板时执行：

```sh
make config-templates
```

这会写入 `output/webui.linux.json` 和 `output/webui.android.json`。运行时 `--write-default-config` 仍会根据实际运行的平台生成对应默认路径。

### Android arm64 交叉编译

```sh
go version
make android-arm64 android-config
```

产物：

```text
output/droidspaces-webui-android-arm64
output/webui.android.json
```

Makefile 等价于：

```sh
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 \
  go build -trimpath -ldflags="-s -w -X main.webVersion=v0.1.0" \
  -o output/droidspaces-webui-android-arm64 \
  ./cmd/droidspaces-webui
```

编译版本会写入运行时状态页。需要指定发布版本时可传入 `VERSION`：

```sh
make VERSION=v0.1.0 android-arm64
```

### 一键全架构构建

```sh
make all
# 等同于 make release
```

该目标保留当前主机构建产物，并生成 Linux 与 Android 常用架构的静态发布二进制：

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

Android `arm64` 对应 `arm64-v8a`，`armv7` 对应 `armeabi-v7a`；`amd64` 与 `386` 主要用于 Android 模拟器或少量 x86 设备。`make all` 仅编译程序，如需两套配置模板另行执行 `make config-templates`。

### 可用 Make 目标

| 命令 | 结果 |
| --- | --- |
| `make all` / `make release` | 一次构建当前主机、Linux amd64/arm64/armv7/386/riscv64，以及 Android arm64/armv7/amd64/386。 |
| `make build` | 构建当前主机可执行文件。 |
| `make linux-amd64`、`make linux-arm64`、`make linux-armv7`、`make linux-386`、`make linux-riscv64` | 构建指定 Linux 架构的静态二进制。 |
| `make android-arm64`、`make android-armv7`、`make android-amd64`、`make android-386` | 构建指定 Android ABI 对应的 Linux ELF 静态二进制。 |
| `make default-config` | 生成 Linux 标准配置 `output/webui.json`。 |
| `make linux-config` | 生成 Linux 标准配置 `output/webui.linux.json`。 |
| `make android-config` | 生成 Android 标准配置 `output/webui.android.json`。 |
| `make config-templates` | 同时生成 Linux 与 Android 两份配置模板。 |
| `make package` | 编译全部发布架构，并生成支持 Droidspaces `v6.5.0` 的压缩发行包。 |
| `make run` | 以 `output/webui.json` 运行开发服务。 |
| `make test` | 运行全部 Go 单元与接口测试。 |
| `make local-smoke` | 启动临时本地服务并验证主要 API/UI 静态资源。 |
| `make android-smoke` | 推送 smoke 专用二进制/配置到已连接 Android 设备后验证 API。 |
| `make clean` | 删除 `output/` 构建产物。 |

### 二进制发行包

首个发行包的运行时 WebUI 版本为 `v0.1.0-ds6.5.0`，目标核心版本为 Droidspaces `v6.5.0`：

```sh
make package
```

产物为：

```text
output/Droidspaces-WebUI-v0.1.0-ds6.5.0.tar.gz
output/Droidspaces-WebUI-v0.1.0-ds6.5.0.tar.gz.sha256
```

压缩包不包含本机配置、容器、模板或 RootFS 缓存，仅包含九个 Linux/Android 架构二进制、两份标准配置、中英文说明文档（`README.md`、`README_EN.md`）、`install-linux.sh`、Android/Magisk 启动脚本 `android-start-webui.sh`、发行包声明的官方核心版本清单、发行说明和 `SHA256SUMS`。验证解压后的文件：

```sh
tar -xzf output/Droidspaces-WebUI-v0.1.0-ds6.5.0.tar.gz -C /tmp
cd /tmp/Droidspaces-WebUI-v0.1.0-ds6.5.0
sha256sum -c SHA256SUMS
```

下载压缩包后，可先在 `output/` 或下载目录中验证归档本体：

```sh
sha256sum -c Droidspaces-WebUI-v0.1.0-ds6.5.0.tar.gz.sha256
```

后续发行可覆盖版本号：

```sh
make package RELEASE_VERSION=v0.2.0
```

### Linux 自动安装

仓库提供 [scripts/install-linux.sh](scripts/install-linux.sh)。它会按当前 Linux CPU 架构完成以下工作：

- 从发行包声明的官方 [Droidspaces-OSS Release](https://github.com/ravindu644/Droidspaces-OSS/releases/latest) 下载后端压缩包，校验 GitHub API 提供的文件大小与 SHA-256 摘要，并只提取匹配架构的 `droidspaces` 二进制；不使用 `.deb`、`apt` 或系统包安装。
- 从本项目 GitHub Release 下载并校验 WebUI。
- 将核心和 WebUI 安装到 `/var/lib/Droidspaces/bin/`，创建 `/usr/sbin` 快捷链接，并写入、启用和启动 `droidspaces.service` 与 `droidspaces-webui.service`。两个服务异常退出时会由 systemd 自动重启。
- 首次写入配置或使用 `--replace-config` 时，询问工作区、访问范围、监听端口和授权密钥。直接回车使用固定工作区 `/var/lib/Droidspaces`、公网监听 `0.0.0.0:9090`，并生成一个 8 位随机 Token。

已有核心默认保留；只有传入 `--replace-core` 才会下载并替换它。已有配置默认也会保留。重复执行脚本时，正在运行的核心服务不会被默认重启；只有显式传入 `--restart-core` 才会中断并切换到新核心。

先下载脚本后执行，便于先检查脚本内容：

```sh
curl -fsSLO https://raw.githubusercontent.com/loeveo/Droidspaces-WebUI/main/scripts/install-linux.sh
sudo bash install-linux.sh
```

默认安装到 `/var/lib/Droidspaces/bin/droidspaces` 与 `/var/lib/Droidspaces/bin/droidspaces-webui`，首次安装时生成 `/var/lib/Droidspaces/webui.json`，并创建 `/usr/sbin/droidspaces`、`/usr/sbin/droidspaces-webui` 快捷链接。首次安装会显示初始设置确认，直接回车即使用公网 `0.0.0.0:9090` 与自动生成的 8 位 Token；安装完成时会显示该 Token 一次。已有二进制会备份；已有配置默认保留，因此重复执行脚本不会重新询问或改变访问设置。

常用选项：

```sh
# 安装指定 WebUI Release 标签
sudo bash install-linux.sh --version v0.1.0

# 使用指定的官方 Droidspaces 核心版本，并显式替换已有核心
sudo bash install-linux.sh --core-version v6.5.0 --replace-core

# 更新核心后，明确允许重启核心服务以立即切换版本
sudo bash install-linux.sh --core-version v6.5.0 --replace-core --restart-core

# 不创建 /usr/sbin 快捷链接
sudo bash install-linux.sh --no-symlink

# 用 Release 的 Linux 标准模板替换已有配置（旧配置会自动备份）
sudo bash install-linux.sh --replace-config

# 自动化安装：跳过交互并使用默认公网设置与随机 Token
# Token 仅写入权限为 0600 的 /var/lib/Droidspaces/webui.json，不输出到日志
sudo bash install-linux.sh --non-interactive
```

默认会启用并启动两项服务，可立即检查状态：

```sh
sudo systemctl status droidspaces.service droidspaces-webui.service
```

官方 Linux 核心的工作区固定为 `/var/lib/Droidspaces`，因此安装脚本不接受其他 `--workspace` 值，以避免 WebUI 与核心的容器、日志和 PID 状态分裂。公网监听只配置 WebUI 的监听地址；防火墙放行、云安全组和路由器端口映射需由管理员自行设置。8 位随机 Token 适合作为首次安装凭据，长期公网暴露前应在 `webui.json` 中替换为更长的高强度 Token。若机器没有 systemd，脚本仍会安装已校验的二进制和配置，并提示跳过服务注册。若需要自行管理进程，传入 `--no-systemd`；若只想写入单元而不立即启动，传入 `--no-start`。完整参数说明：

```sh
bash install-linux.sh --help
```

## Android 部署

以下示例不会替换 Droidspaces 核心，只替换 WebUI 二进制和配置。先在电脑上完成 arm64 构建，并按设备实际目录检查 `output/webui.android.json`。

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

使用启动脚本启动服务：

```sh
adb shell su -c /data/local/Droidspaces/bin/start-android-webui.sh
```

脚本默认在后台启动 `/data/local/Droidspaces/bin/droidspaces-webui --config /data/local/Droidspaces/webui.json`，将启动信息和 WebUI 输出追加到 `/data/local/Droidspaces/Logs/webui.log`，并记录 `/data/local/Droidspaces/webui.pid`。它通过 PID 和 `/proc/<pid>/cmdline` 核验已有实例，不会停止无法确认的进程，也不会重复启动同一份 WebUI。需要在前台排错时使用：

```sh
adb shell su -c '/data/local/Droidspaces/bin/start-android-webui.sh --foreground'
```

### Magisk 自启动

已安装 Magisk 且设备具有 root 权限时，将同一启动脚本复制到 `service.d`：

```sh
adb shell su -c '
  mkdir -p /data/adb/service.d &&
  cp /data/local/Droidspaces/bin/start-android-webui.sh /data/adb/service.d/droidspaces-webui.sh &&
  chmod 755 /data/adb/service.d/droidspaces-webui.sh
'
```

脚本从 `service.d` 执行时会等待 `sys.boot_completed`（默认最长 180 秒）再启动；可在手动调用时使用 `--wait` 获得相同行为，或设置 `DS_WEBUI_BOOT_WAIT_TIMEOUT=0` 无限等待。不要在 `service.d` 中使用 `--foreground`，它用于交互式诊断。

若在电脑浏览器访问，建立 ADB 端口转发：

```sh
adb forward tcp:9090 tcp:9090
```

然后打开 `http://127.0.0.1:9090`。脚本使用配置文件中的监听地址和端口；通过电脑访问时仍需建立匹配端口的 ADB 转发。

### Android 冒烟测试

设备已通过 ADB 连接、具有 `su`，并且构建产物已生成时：

```sh
make android-arm64 android-config
make android-smoke
```

脚本默认使用 smoke 专用的远端文件：

```text
/data/local/Droidspaces/bin/droidspaces-webui-smoke-android-arm64
/data/local/Droidspaces/webui-smoke.json
/data/local/Droidspaces/Logs/webui-smoke.log
```

它不会覆盖正式 `webui.json`。常用覆盖参数：

```sh
ADB=adb DEVICE_SERIAL=serial AUTH_TOKEN=change-me make android-smoke
HOST_PORT=19090 DEVICE_PORT=19090 make android-smoke
KEEP_RUNNING=1 make android-smoke
PUSH=0 make android-smoke
```

## 配置

配置加载优先级从低到高：内置默认值、JSON 配置文件、环境变量、命令行参数。

| 字段 | 说明 |
| --- | --- |
| `mode` | `local` 固定监听本机；`public` 用于局域网或公网。 |
| `host` / `port` | HTTP 监听地址与端口，默认 `127.0.0.1:9090`。 |
| `authToken` | API Bearer Token。`public` 模式为空时会生成临时 8 位 token 并输出到启动日志；生产使用应写入固定强 token。 |
| `uiLanguage` | WebUI 界面语言。支持 `zh-CN`（简体中文，默认）和 `en`（英语）。若配置文件缺少该字段，首次访问 WebUI 会询问语言并将选择写回配置文件；也可在“系统设置”中在线修改，切换后立即生效。 |
| `droidspacesPath` | Droidspaces 核心二进制的完整路径。默认是 `workspace/bin/droidspaces`。 |
| `corePath` | 核心二进制目录；为空时取 `droidspacesPath` 的父目录。默认是 `workspace/bin`。 |
| `workspace` | Droidspaces 工作区。Android 默认 `/data/local/Droidspaces`，Linux 默认 `/var/lib/Droidspaces`。 |
| `imageRoot` | 旧版 Core 镜像扫描目录兼容字段，不在 WebUI 设置中展示；已下载模板与云端列表缓存使用 `templateImageRoot`。 |
| `templateImageRoot` | 本地可复用模板目录，默认是 `workspace/rootfs`。 |
| `socketdEnabled` | 是否优先使用 Droidspaces socketd。真实 Android 部署通常设置为 `true`。 |
| `rootfsSkipTLSVerify` | Android 精简环境缺少 CA 时可暂时设为 `true`；具备完整证书链时建议设为 `false`。 |
| `defaultNatCIDR` | NAT 地址池兼容字段，目前必须为 `172.28.0.0/16`。 |
| `defaultNatThirdOctet` | 新 NAT 容器默认使用的 `172.28.<值>.x` 第三段，范围 `1-254`。 |
| `nestedAndroidNatCompat` | 默认关闭。仅在 Linux 容器内运行 WebUI、并在其中继续创建 NAT 容器时启用；WebUI 只用临时共享策略路由适配 Android 默认出口，不修改 DS 核心、防火墙、NAT/DNAT 或镜像。外层 Android 的 FORWARD 策略仍须由外层 Droidspaces 提供。 |
| `batteryDirectPowerSupported` | 设备支持直供电/旁路供电时设为 `true`，用于正确标记当前供电状态。 |
| `batterySeriesCells` | 电池串联单元数；`0` 为自动识别，其他有效值为 `1-6`。 |
| `overviewPowerEnabled` | 是否在首页概览显示“当前功耗”卡片；关闭不影响已经启用的电池监控。 |
| `batteryMonitoringEnabled` | 是否启用电池监控。关闭后隐藏入口和页面，停止后台采样与历史统计。 |
| `batteryDetailEnabled` | 保留的电池详细信息兼容字段。 |
| `batteryStatsSampleSeconds` | 电池统计采样周期，范围 `1-60` 秒。 |
| `batteryStatsWriteMinutes` | 电池统计写盘周期，范围 `5-1440` 分钟。 |
| `batteryStatsRetentionDays` | 功耗历史按本地日期分文件保存；保留最近 `1-365` 天，默认 `7` 天。提高该值可主动保留更长历史。 |
| `overviewRefreshSeconds` | 概览页可见时的刷新周期，范围 `1-60` 秒。 |
| `rootfsRepositories` | 云端 RootFS 仓库列表。默认包含 Droidspaces Official 和 lxc-image。 |

标准模板分别是 [config/webui.linux.example.json](config/webui.linux.example.json) 与 [config/webui.android.example.json](config/webui.android.example.json)。旧的 [config/webui.example.json](config/webui.example.json) 保留为 Android 兼容路径。示例内的 `_说明` 或 `*_说明` 字段会被程序忽略。

### lxc-image CN 加速

简体中文的新配置默认启用南京大学 lxc-image 镜像；英语新配置默认使用官方源。已在系统设置中手动选择的来源会被保留，不会因切换界面语言而覆盖。

在 WebUI 的系统设置中启用 CN 加速后，lxc-image 会从：

```text
https://mirror.nju.edu.cn/lxc-images/
```

下载镜像。该切换替换同一个 lxc-image 来源，不会并列新增第二个仓库；镜像目录仍由 Linux Containers 官方 SimpleStreams catalog 解析，以保证可选择的镜像与上游一致。

### 云端镜像存储与刷新

已下载的云端模板按来源保存到 `templateImageRoot`：

```text
rootfs/
  droidspaces-official/
  lxc-image/
    cloud/
    default/
```

云端镜像列表的元数据缓存与模板放在同一个 `templateImageRoot/lxc-image/` 目录，例如 `rootfs/lxc-image/catalog-<hash>.json`。WebUI 会按设备本地时间在每天 `00:10` 清理旧的列表缓存并重新拉取目录；这个定时操作不会删除 `cloud/`、`default/` 中已下载的模板归档。

### 环境变量和命令行

常用环境变量：

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

常用命令行覆盖：

```sh
./output/droidspaces-webui \
  --config /data/local/Droidspaces/webui.json \
  --listen 127.0.0.1:9090 \
  --droidspaces /data/local/Droidspaces/bin/droidspaces
```

局域网监听示例：

```sh
./output/droidspaces-webui \
  --config /data/local/Droidspaces/webui.json \
  --listen 0.0.0.0:9090 \
  --auth-token 'replace-with-a-strong-token'
```

## 验证与排错

### 本地冒烟验证

```sh
make build
make local-smoke
```

脚本创建临时 workspace 和 fake Droidspaces 二进制，验证状态、容器、RootFS、事件、任务、主机信息、启动顺序和静态资源。需要保留临时目录以检查日志时：

```sh
KEEP_WORKDIR=1 make local-smoke
```

### API 健康检查

未设置 token 时：

```sh
curl -fsS http://127.0.0.1:9090/api/status
```

设置 token 时：

```sh
curl -fsS \
  -H 'Authorization: Bearer replace-with-your-token' \
  http://127.0.0.1:9090/api/status
```

状态中 `backend: ready` 表示 socketd 可用；`cli-fallback` 或 `workspace-fallback` 表示 WebUI 已切换到降级读取路径。应在诊断页确认 socketd、Droidspaces 版本、路径和权限状态。

## 推送前检查

此仓库中已有用户工作区改动时，请只暂存本次希望提交的文件，避免把本地二进制、配置和临时日志一并推送。

```sh
git status --short
git diff --check
make test
make build
make local-smoke
```

确认后，先查看将被提交的内容，再按实际变更选择暂存文件：

```sh
git add README.md README_EN.md jpg/
git diff --cached --check
git commit -m 'Document Droidspaces WebUI build and deployment'
```

首次推送且尚未配置远端时，先替换为实际仓库地址：

```sh
git remote add origin <repository-url>
git branch -M main
git push -u origin main
```

已有 upstream 时直接执行 `git push`。业务代码变更应在确认后单独添加。`output/`、日志和临时文件已由 [.gitignore](.gitignore) 排除。推送前仍应检查 `git status`，确保没有认证 token、设备路径之外的私密内容或无关的本地工作成果被暂存。

## 安全边界

- 不要在不可信网络暴露 root 级管理页面。
- `public` 模式必须使用固定、强度足够的 `authToken`，并在网络层加访问控制。
- `rootfsSkipTLSVerify` 只应用于 Android 缺少 CA 的兼容场景；可验证证书时应关闭。
- Cloud-init 用户数据、密码、SSH 设置和首启命令会改变容器系统。仅使用可信镜像，并在创建前检查配置。

## 范围

WebUI 负责 Android/Linux 的无桌面容器管理。Android 桌面、launcher、桌面拦截以及 Wayland 交互不在本项目范围内。
