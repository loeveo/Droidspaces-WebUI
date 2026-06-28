# Droidspaces WebUI

这是一个使用 Go 编写的 Droidspaces 本地管理界面。

WebUI 优先通过 `droidspaces daemon` 启动的私有后端 socket
`@droidspaces-socketd-backend` 获取容器列表、详情、事件并执行
start/stop/restart。若 socketd 在 Android 上不可达，会从配置的 `workspace`
读取 `Pids/*.pid` 和 `Containers/*/container.config` 作为状态兜底，至少显示
正在运行的容器名称和 PID。诊断区只允许执行 `check`、`mode`、`scan`、`show`、
`version` 这几个只读或恢复类 CLI 命令。

## 部署位置

WebUI 必须运行在 Droidspaces 宿主系统上，也就是运行 `droidspaces daemon`
的同一个 Android/Linux 环境中。它连接的是 Linux 抽象 Unix socket，不能从
Codex 容器、ADB 主机或另一个容器里直接访问 Android 宿主上的 socket。

如果 Droidspaces 运行在 Android 上，就把 `droidspaces-webui` 放到 Android
设备上用 root 启动；然后通过手机浏览器访问 `127.0.0.1:9090`，或用
`adb forward` 把手机端口转发到电脑。

## 构建

WebUI 是独立套件，不挂靠主项目 Makefile。`webui/` 目录也是独立 Git 仓库，后续可以在该目录内单独提交和查看版本变化记录。进入 `webui/` 目录构建：

```sh
cd webui
make
```

Android arm64 设备构建目标：

```sh
cd webui
make android-arm64
```

产物路径：

```text
webui/output/droidspaces-webui-android-arm64
```


## 配置文件

默认配置路径：

- Android: `/data/local/Droidspaces/webui.json`
- Linux: `/var/lib/Droidspaces/webui.json`

项目内置了一份可直接复制到 Android 设备的配置模板：

```text
webui/config/webui.example.json
```

生成本地输出配置：

```sh
cd webui
make default-config
```

也可以由程序按当前平台写出默认配置：

```sh
./droidspaces-webui --config /data/local/Droidspaces/webui.json --write-default-config
```

模板中已经为每一项添加了说明字段，例如 `mode_说明`。这些说明字段会被程序忽略，可以保留；需要精简时也可以删除。核心字段如下：

```json
{
  "mode": "local",
  "host": "127.0.0.1",
  "port": 9090,
  "authToken": "",
  "droidspacesPath": "/data/local/Droidspaces/bin/droidspaces",
  "corePath": "/data/local/Droidspaces/bin",
  "imageRoot": "/data/local/Droidspaces/bin",
  "templateImageRoot": "/data/local/Droidspaces/rootfs",
  "workspace": "/data/local/Droidspaces",
  "socketdEnabled": true,
  "rootfsSkipTLSVerify": true,
  "rootfsRepositories": [
    {
      "name": "Droidspaces Official",
      "url": "https://github.com/Droidspaces/Droidspaces-rootfs-builder/raw/refs/heads/main/rootfs.json"
    }
  ]
}
```

字段说明：

- `mode`: `local` 只监听本机，`public` 监听公网/局域网入口。
- `host`: 监听地址；`local` 默认 `127.0.0.1`，`public` 默认 `0.0.0.0`。
- `port`: WebUI 端口。
- `authToken`: Web 访问授权密钥；`public` 模式必须配置，否则程序拒绝启动。
- `droidspacesPath`: Droidspaces 核心程序路径。
- `corePath`: 核心程序所在目录；不填时取 `droidspacesPath` 的父目录。
- `imageRoot`: 容器镜像默认目录；不填时与 `corePath` 相同。
- `templateImageRoot`: 模板镜像目录；当前 Android 模板默认是 `/data/local/Droidspaces/rootfs`。
- `workspace`: Droidspaces 工作区路径。
- `socketdEnabled`: 是否连接 Droidspaces socketd 后端；Android 真实部署建议为 `true`，本地调试或隔离验证可设为 `false`，避免读取到其他环境的全局 socketd 状态。
- `rootfsSkipTLSVerify`: 是否跳过 RootFS 仓库和下载请求的 TLS 证书校验；Android 精简环境缺少 CA 证书时可保持 `true`。
- `rootfsRepositories`: RootFS 云端列表仓库，格式与 Android App 的 `RootfsRepository` 使用的 `rootfs.json` 一致；为空时会恢复内置官方仓库。

加载顺序为：内置默认值 -> 配置文件 -> 环境变量 -> 命令行参数。

## Android 运行示例

把二进制和配置推到 Android 设备：

```sh
cd webui
make android-arm64 default-config
adb push output/droidspaces-webui-android-arm64 /data/local/Droidspaces/droidspaces-webui-android-arm64
adb push output/webui.json /data/local/Droidspaces/webui.json
adb shell su -c 'chmod 755 /data/local/Droidspaces/droidspaces-webui-android-arm64'
```

确认 Droidspaces 守护进程已经在 Android 上运行。然后启动 WebUI：

```sh
adb shell su -c 'cd /data/local/Droidspaces && ./droidspaces-webui-android-arm64 --config /data/local/Droidspaces/webui.json'
```

如果电脑访问，用 ADB 转发：

```sh
adb forward tcp:9090 tcp:9090
```

打开：

```text
http://127.0.0.1:9090
```

## 本地冒烟验证

在没有 Android 设备时，可以先验证 Linux 主机上的真实 WebUI 进程和 API 路径。脚本会创建临时 Droidspaces workspace、fake `droidspaces`/`busybox`，启动编译后的 WebUI，再访问主要 API：

```sh
cd webui
make build
make local-smoke
```

默认验证内容：`/api/status`、`/api/containers?all=1`、`/api/rootfs/local`、`/api/events`、`/api/containers/demo`。设置 `KEEP_WORKDIR=1` 可以保留临时目录和日志。

## Android 冒烟验证

在有真实 Android 设备、ADB、root 权限和 Droidspaces 工作区的环境中，可以运行自动冒烟验证。脚本使用已构建产物并推送为 smoke 专用文件，不覆盖正式 `webui.json`：

```sh
cd webui
make android-arm64 default-config
make android-smoke
```

默认验证内容：启动 WebUI、建立 `adb forward`、访问 `/api/status`、`/api/containers?all=1`、`/api/rootfs/local`、`/api/events`，并校验返回 JSON 可解析。脚本结束时会停止 smoke WebUI；设置 `KEEP_RUNNING=1` 可保留进程继续手动验证。

常用参数：

```sh
AUTH_TOKEN=change-me HOST_PORT=19090 DEVICE_PORT=19090 make android-smoke
KEEP_RUNNING=1 make android-smoke
PUSH=0 make android-smoke
```

脚本默认使用：

- 远端二进制：`/data/local/Droidspaces/droidspaces-webui-smoke-android-arm64`
- 远端配置：`/data/local/Droidspaces/webui-smoke.json`
- 远端日志：`/data/local/Droidspaces/Logs/webui-smoke.log`

如果要监听局域网地址，必须设置 token：

```sh
adb shell su -c 'cd /data/local/Droidspaces && ./droidspaces-webui-android-arm64 \
  --listen 0.0.0.0:9090 \
  --auth-token change-me \
  --droidspaces /data/local/Droidspaces/bin/droidspaces'
```

也支持环境变量：

- `DS_WEBUI_CONFIG`
- `DS_WEBUI_LISTEN`
- `DS_WEBUI_MODE`
- `DS_WEBUI_HOST`
- `DS_WEBUI_PORT`
- `DS_WEBUI_DROIDSPACES`
- `DS_WEBUI_AUTH_TOKEN`
- `DS_WEBUI_WORKSPACE`
- `DS_WEBUI_CORE_PATH`
- `DS_WEBUI_IMAGE_ROOT`
- `DS_WEBUI_TEMPLATE_IMAGE_ROOT`
- `DS_WEBUI_SOCKETD_ENABLED`
- `DS_WEBUI_ROOTFS_SKIP_TLS_VERIFY`

## 当前功能

- 概览页只显示统计和后端状态，不再置顶显示 Droidspaces 工作目录。
- 容器列表在侧栏二级菜单中，支持按全部、运行中、未运行筛选。
- 容器详情和终端只在容器列表中按需打开；详情仅点击“详细参数”后显示。
- 支持启动、停止、重启、删除容器。
- 支持通过 WebSocket PTY 连接容器 shell，并处理回车、退格和常见 ANSI 控制序列，减少终端输出错位。
- 新建容器可选择本地模板、云端仓库镜像或直接指定 rootfs 路径。
- 本地模板会复制到新容器目录；`.tar.gz`、`.tgz`、`.tar.xz` 会自动解包为 `rootfs/`；`.img` 会复制为 `rootfs.img`。
- RootFS 仓库在侧栏二级菜单中，云端列表和本地模板/备份分开显示。
- 云端 RootFS 下载以后台任务运行，WebUI 显示进度条；Android 缺 CA 证书时可用 `rootfsSkipTLSVerify`。
- 支持将指定容器打包为 rootfs 备份，或转换为可复用模板。
- 支持从 WebUI 下载生成的 rootfs 备份、模板压缩包和 `.img` 文件。
- 诊断页支持 `mode`、`scan`、`show`、`version` 等受限 Droidspaces CLI 命令。

## 注意

这个 WebUI 是 root 级容器管理入口。不要在没有认证和网络隔离的情况下暴露到不可信网络。
