# Droidspaces WebUI

Droidspaces WebUI 是面向 Android 和 Linux 宿主机的本地容器管理界面。它以一个独立的 Go HTTP 服务提供浏览器界面，负责管理 Droidspaces 容器、RootFS 模板、网络、后台任务、终端和诊断信息。

> 这是 root 级管理入口。请只在受信任设备和网络中运行；对局域网或公网监听时必须设置强 `authToken`。

<p align="center">
  <img src="jpg/001.jpg" alt="Droidspaces WebUI 概览页" width="31%">
  <img src="jpg/002.jpg" alt="Droidspaces WebUI 容器管理页" width="31%">
  <img src="jpg/003.jpg" alt="本地 RootFS 模板管理" width="31%">
</p>
<p align="center">
  <img src="jpg/0045.jpg" alt="Droidspaces Official 镜像说明" width="31%">
  <img src="jpg/004.jpg" alt="Linux Containers CN 镜像列表" width="31%">
</p>

## 功能

- 容器概览、列表、详情、启动、停止、重启、删除和启动顺序管理。
- 本地模板、备份和云端 RootFS 仓库管理；支持下载、上传、删除、导出备份及转换模板。
- Droidspaces Official 镜像说明展示，以及 Linux Containers `rootfs.tar.xz` 云端镜像浏览与搜索。
- 设置中可将 Linux Containers 的下载源切换为南京大学 CN 镜像；目录继续使用官方 catalog，镜像文件从 `https://mirror.nju.edu.cn/lxc-images/` 下载。
- Cloud 镜像 NoCloud 初始化：用户、密码、SSH、首启命令、包安装、静态 NAT 网络配置和自定义 YAML。
- NAT、端口转发、DNS、上游接口优先级、容器配置、系统服务检查和诊断工具。
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
- Android arm64 构建使用 `GOOS=linux GOARCH=arm64 CGO_ENABLED=0`，已封装在 Makefile 中。

运行环境：

- 已安装并可执行 Droidspaces 核心程序。
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

使用仓库中的 Android 示例配置进行开发运行：

```sh
make default-config
# 编辑 output/webui.json，使路径与当前环境一致
make run
```

`make default-config` 只复制 [config/webui.example.json](config/webui.example.json)，其中是 Android 路径。普通 Linux 开发机应优先使用上方的 `--write-default-config` 生成配置，或手动修改 `output/webui.json` 后再执行 `make run`。

### Android arm64 交叉编译

```sh
go version
make android-arm64 default-config
```

产物：

```text
output/droidspaces-webui-android-arm64
output/webui.json
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

### 可用 Make 目标

| 命令 | 结果 |
| --- | --- |
| `make build` | 构建当前主机可执行文件。 |
| `make android-arm64` | 构建 Android/Linux arm64 静态 Go 二进制。 |
| `make default-config` | 生成 `output/webui.json`。 |
| `make run` | 以 `output/webui.json` 运行开发服务。 |
| `make test` | 运行全部 Go 单元与接口测试。 |
| `make local-smoke` | 启动临时本地服务并验证主要 API/UI 静态资源。 |
| `make android-smoke` | 推送 smoke 专用二进制/配置到已连接 Android 设备后验证 API。 |
| `make clean` | 删除 `output/` 构建产物。 |

## Android 部署

以下示例不会替换 Droidspaces 核心，只替换 WebUI 二进制和配置。先在电脑上完成 arm64 构建，并按设备实际目录修改 `output/webui.json`。

```sh
make android-arm64 default-config

adb push output/droidspaces-webui-android-arm64 /data/local/tmp/droidspaces-webui-android-arm64
adb push output/webui.json /data/local/tmp/webui.json

adb shell su -c '
  cp /data/local/tmp/droidspaces-webui-android-arm64 /data/local/Droidspaces/droidspaces-webui-android-arm64 &&
  cp /data/local/tmp/webui.json /data/local/Droidspaces/webui.json &&
  chmod 755 /data/local/Droidspaces/droidspaces-webui-android-arm64
'
```

启动服务：

```sh
adb shell su -c '
  cd /data/local/Droidspaces &&
  ./droidspaces-webui-android-arm64 --config /data/local/Droidspaces/webui.json
'
```

若在电脑浏览器访问，建立 ADB 端口转发：

```sh
adb forward tcp:9090 tcp:9090
```

然后打开 `http://127.0.0.1:9090`。服务长期运行时，应由设备现有的启动脚本或服务管理方式托管，并把 stdout/stderr 重定向到 Droidspaces 日志目录。

### Android 冒烟测试

设备已通过 ADB 连接、具有 `su`，并且构建产物已生成时：

```sh
make android-arm64 default-config
make android-smoke
```

脚本默认使用 smoke 专用的远端文件：

```text
/data/local/Droidspaces/droidspaces-webui-smoke-android-arm64
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
| `droidspacesPath` | Droidspaces 核心二进制的完整路径。 |
| `corePath` | 核心目录；为空时取 `droidspacesPath` 的父目录。 |
| `workspace` | Droidspaces 工作区。Android 默认 `/data/local/Droidspaces`，Linux 默认 `/var/lib/Droidspaces`。 |
| `imageRoot` | 容器镜像存放目录。 |
| `templateImageRoot` | 本地可复用模板目录。 |
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
| `overviewRefreshSeconds` | 概览页可见时的刷新周期，范围 `1-60` 秒。 |
| `rootfsRepositories` | 云端 RootFS 仓库列表。默认包含 Droidspaces Official 和 Linux Containers。 |

完整带注释样例见 [config/webui.example.json](config/webui.example.json)。示例内的 `_说明` 或 `*_说明` 字段会被程序忽略。

### Linux Containers CN 加速

在 WebUI 的系统设置中启用 CN 加速后，Linux Containers 会从：

```text
https://mirror.nju.edu.cn/lxc-images/
```

下载镜像。该切换替换同一个 Linux Containers 来源，不会并列新增第二个仓库；镜像目录仍由官方 SimpleStreams catalog 解析，以保证可选择的镜像与上游一致。

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
git add README.md jpg/
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
