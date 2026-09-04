(function initDroidspacesI18n(global) {
  "use strict";

  const STORAGE_KEY = "DS_WEBUI_LOCALE";
  const DEFAULT_LOCALE = "zh-CN";
  const SUPPORTED_LOCALES = ["zh-CN", "en"];

  // Keep presentation strings in one client-side catalog. API responses and user
  // supplied values are deliberately not translated here.
  const messages = {
    "zh-CN": {
      "document.title": "Droidspaces WebUI",
      "locale.label": "语言",
      "locale.zh-CN": "简体中文",
      "locale.en": "English",
      "locale.shortZh": "中文",
      "locale.shortEn": "EN",
      "locale.welcomeTitle": "选择显示语言",
      "locale.welcomeDescription": "当前配置文件尚未设置界面语言。请选择后将立即保存并应用。",
      "nav.main": "主导航",
      "nav.desktop": "桌面页面导航",
      "nav.pages": "页面导航",
      "nav.overview": "系统资源",
      "nav.containers": "容器管理",
      "nav.rootfs": "镜像管理",
      "nav.network": "网络管理",
      "nav.security": "安全",
      "nav.diagnostics": "诊断",
      "nav.battery": "电池监控",
      "nav.settings": "系统设置",
      "action.refreshData": "刷新数据",
      "action.refresh": "刷新",
      "action.createContainer": "新建容器",
      "action.backgroundTasks": "后台任务",
      "action.taskOutput": "任务输出",
      "action.switchPage": "切换页面",
      "action.viewContainers": "查看容器",
      "action.openTerminal": "打开终端",
      "action.connect": "连接",
      "action.disconnect": "断开",
      "action.clear": "清屏",
      "action.send": "发送",
      "action.uploadTemplate": "上传模板",
      "action.manageSources": "管理来源",
      "action.close": "关闭",
      "action.save": "保存",
      "action.cancel": "取消",
      "action.check": "检查",
      "action.settings": "设置",
      "action.download": "下载",
      "action.delete": "删除",
      "action.create": "创建",
      "action.edit": "编辑",
      "action.start": "启动",
      "action.stop": "停止",
      "action.restart": "重启",
      "action.backup": "备份",
      "action.details": "详情",
      "action.enter": "进入",
      "common.unknown": "未知",
      "common.loading": "加载中",
      "common.pending": "待上报",
      "common.enabled": "已启用",
      "common.disabled": "未启用",
      "common.running": "运行中",
      "common.stopped": "已停止",
      "common.all": "全部",
      "common.none": "无",
      "common.waiting": "等待刷新",
      "runtime.webVersionLoading": "WebUI 读取中",
      "runtime.versionHintLoading": "WebUI: 读取中 · 当前核心: 读取中 · 适配核心: 读取中",
      "view.overview.title": "概览",
      "view.overview.subtitle": "系统、资源与网络运行状态",
      "view.containers.title": "容器管理",
      "view.containers.subtitle": "列表、筛选和生命周期操作",
      "view.detail.title": "详细参数",
      "view.detail.subtitle": "容器配置、网络、挂载、环境变量和备份操作",
      "view.terminal.title": "交互终端",
      "view.terminal.subtitle": "进入运行中的容器 shell",
      "view.rootfs.title": "镜像管理",
      "view.network.title": "网络管理",
      "view.network.subtitle": "NAT 默认配置、网络模式和端口转发总览",
      "view.security.title": "安全",
      "view.security.subtitle": "授权、TLS、端口和容器敏感能力摘要",
      "view.diagnostics.title": "诊断",
      "view.diagnostics.subtitle": "主机路径、CLI 输出和 socketd 事件",
      "view.battery.title": "电池监控",
      "view.battery.subtitle": "实时供电状态、库仑计历史和功率区间",
      "view.settings.title": "系统设置",
      "view.settings.subtitle": "WebUI 配置、路径、镜像仓库和 Android 集成",
      "metric.systemVersion": "系统版本",
      "metric.kernelVersion": "内核版本",
      "metric.deviceUptime": "设备运行时间",
      "metric.cpuUsage": "CPU 占用",
      "metric.memoryUsage": "内存占用",
      "metric.networkIO": "网卡流量 / 累计流量",
      "metric.currentPower": "当前功耗",
      "metric.hostUsage": "主机整体使用率",
      "metric.usedTotalPending": "已用 / 总量待上报",
      "metric.containerStatus": "容器运行状态",
      "metric.containersTotal": "容器总数",
      "metric.localRootfs": "本地镜像",
      "metric.activeTasks": "活跃任务",
      "overview.containerLoading": "容器信息加载中",
      "overview.hostSummary": "主机摘要",
      "overview.recentEvents": "最近事件",
      "overview.eventLog": "事件日志",
      "containers.list": "容器列表",
      "containers.filter": "容器状态筛选",
      "containers.running": "运行中",
      "containers.notStarted": "未启动",
      "containers.search": "搜索名称、主机名、网络",
      "containers.startupOrder": "启动顺序",
      "containers.none": "暂无容器",
      "containers.noMatch": "无匹配容器",
      "detail.noneSelected": "尚未选择容器",
      "detail.selectHint": "从容器列表选择一个容器",
      "terminal.container": "容器",
      "terminal.user": "用户",
      "terminal.manualUser": "手动用户",
      "terminal.disconnected": "未连接",
      "terminal.selectRunning": "选择正在运行的容器后连接 shell",
      "terminal.inputPlaceholder": "移动端输入区：Enter 发送，Shift+Enter 换行",
      "rootfs.architecture": "架构",
      "rootfs.auto": "自动",
      "rootfs.tabs": "镜像功能",
      "rootfs.local": "本地模板",
      "rootfs.backups": "备份",
      "rootfs.remote": "云端镜像",
      "rootfs.noBackups": "暂无备份",
      "rootfs.search": "快速搜索云端镜像：系统或版本",
      "rootfs.filterSource": "按来源筛选镜像",
      "rootfs.allImages": "全部镜像",
      "rootfs.remoteSources": "云端来源",
      "rootfs.addRepository": "新增仓库",
      "rootfs.saveRepositories": "保存仓库",
      "network.defaultConfig": "NAT 默认配置",
      "network.autoDescription": "出口由官方核心按当前运行平台自动检测，并在网络切换时自动更新。",
      "network.defaultThirdOctet": "NAT 第 3 位",
      "network.defaultPrefix": "NAT 默认前缀",
      "network.gateway": "NAT 网关",
      "network.upstream": "上游出口（自动）",
      "network.autoDetected": "由核心自动检测",
      "network.ports": "端口与网络模式",
      "network.container": "容器",
      "network.status": "状态",
      "network.mode": "模式",
      "network.portForwarding": "端口转发",
      "security.summary": "安全摘要",
      "security.findings": "风险发现",
      "security.privilegedAcknowledgement": "我永远不会报告任何bug",
      "security.privilegedPrompt": "开启特权模式前请输入：{acknowledgement}",
      "security.privilegedAcknowledgementRequired": "需要输入：{acknowledgement}",
      "diagnostics.backendStatus": "后端状态",
      "diagnostics.backendDescription": "socketd 连接、兜底来源和最近后端错误",
      "diagnostics.webuiLogs": "WebUI 服务日志",
      "diagnostics.waitingRead": "等待读取",
      "diagnostics.logTail": "读取末尾日志行数",
      "diagnostics.tail": "末尾",
      "diagnostics.autoFollow": "自动跟随",
      "diagnostics.autoFollowHint": "仅在诊断页可见时自动刷新",
      "diagnostics.refreshLog": "刷新 WebUI 服务日志",
      "diagnostics.events": "事件",
      "diagnostics.refreshEvents": "刷新事件",
      "diagnostics.noBackendErrors": "暂无后端错误",
      "diagnostics.hostPaths": "主机与路径",
      "diagnostics.cli": "CLI 诊断",
      "diagnostics.waiting": "等待执行",
      "battery.noData": "设备未提供电池数据",
      "battery.monitoringDisabled": "电池监控已关闭",
      "battery.monitoringDisabledRedirect": "电池监控已在系统设置中关闭",
      "task.noTasks": "暂无任务",
      "task.noTaskOutput": "暂无任务输出",
      "task.noActiveTasks": "暂无运行中的后台任务",
      "task.current": "当前任务",
      "settings.savedRestartRequired": "已保存。监听模式、地址或端口变更需要重启 WebUI 后完全生效。",
      "settings.saved": "已保存并同步到配置文件。",
      "settings.configPath": "配置文件：{path}",
      "settings.noConfigPath": "当前未指定配置文件，运行态可保存但不会落盘。",
      "settings.notChecked": "尚未检查更新",
      "settings.downloadUpdate": "下载并更新",
      "settings.unknown": "未知",
      "settings.notFetched": "未获取",
      "settings.officialRelease": "GitHub 官方 Release",
      "settings.updating": "正在更新",
      "settings.upToDate": "已是最新版本",
      "settings.updateAvailable": "有可用更新",
      "settings.checkComplete": "检查完成",
      "settings.currentVersion": "当前版本",
      "settings.latestVersion": "最新版本",
      "settings.status": "状态",
      "settings.architecture": "架构",
      "settings.releaseAsset": "发布包",
      "settings.source": "来源",
      "settings.reinstall": "重新安装",
      "settings.latestOfficialVersion": "最新官方版本",
      "settings.updateConfirm": "将从 Droidspaces 官方 GitHub Release 下载并替换核心程序为 {target}。现有二进制会保留为 .previous 备份。继续吗？",
      "settings.updateComplete": "核心程序已更新并通知 daemon 刷新",
      "settings.njuMirror": "南京大学镜像",
      "settings.officialSource": "官方源",
      "settings.configLoading": "读取配置中",
      "settings.uiLanguage": "界面语言",
      "settings.uiLanguageHint": "切换后立即生效，并写入 WebUI 配置文件。",
      "settings.uiLanguageSaved": "界面语言已保存。",
      "settings.runtime": "WebUI 运行",
      "settings.paths": "路径配置",
      "settings.androidIntegration": "Android 集成",
      "settings.integrationWaiting": "等待状态",
      "settings.coreUpdate": "核心程序更新",
      "settings.imageRepositories": "云端镜像仓库",
      "settings.njuAcceleration": "lxc-image CN 加速",
      "settings.njuAccelerationDescription": "启用后将现有 lxc-image RootFS 下载地址切换到南京大学镜像站。",
      "settings.enableCNAcceleration": "启用 CN 加速",
      "settings.batteryStatsRetentionDays": "电池统计保留天数",
      "boot.title": "开机启动顺序",
      "boot.subtitle": "按从上到下的顺序写入启动优先级",
      "boot.noContainers": "没有启用开机启动的容器",
      "boot.saveOrder": "保存顺序",
      "boot.moveUp": "上移",
      "boot.moveDown": "下移",
      "task.runStats": "本次服务运行的任务统计",
      "task.summaryAria": "后台任务统计",
      "task.refreshOutput": "刷新任务输出",
      "task.closeOutput": "关闭任务输出",
      "task.refresh": "刷新后台任务",
      "task.close": "关闭后台任务",
      "form.name": "名称",
      "form.hostname": "主机名",
      "form.network": "网络",
      "form.natStaticIp": "NAT 静态 IP",
      "form.fourthOctet": "第 4 字节",
      "form.auto": "自动",
      "form.dns": "DNS",
      "form.portForwarding": "端口转发（每行一条）",
      "form.gatewayContainer": "网关容器",
      "form.gatewayNetwork": "网关网络名",
      "form.gatewayInterface": "网关内接口",
      "form.hostBridge": "宿主桥接名",
      "form.bindMounts": "绑定挂载（每行一条）",
      "form.init": "Init",
      "form.x11ExtraFlags": "X11 额外参数",
      "form.virglExtraFlags": "VirGL 额外参数",
      "form.memoryLimit": "内存限制",
      "form.cpuLimit": "CPU 限制",
      "form.pidsLimit": "PIDs 限制",
      "form.environment": "环境变量 .env",
      "create.title": "新建容器",
      "create.systemTemplate": "系统模板",
      "create.selectSystem": "选择容器系统",
      "create.imageSource": "镜像来源",
      "create.localTemplate": "本地模板",
      "create.cloudRepository": "云端仓库",
      "create.searchLocalTemplate": "搜索本地模板",
      "create.searchSystem": "搜索系统或版本",
      "create.localTemplateList": "本地模板列表",
      "create.cloudImagePicker": "云端镜像选择器",
      "create.cloudImageList": "云端镜像列表",
      "create.cloudSources": "按云端来源筛选镜像",
      "create.sourceWaiting": "来源：等待选择",
      "create.preDownload": "预下载到本地",
      "create.cloudTask": "选择镜像后直接创建，后台会下载并继续创建容器。",
      "create.defaultSameName": "默认同名称",
      "create.cloudInit": "云镜像初始化",
      "create.cloudInitDescription": "使用 NoCloud 种子在首次启动时应用主机名、用户数据和可选网络定义。",
      "create.enableInit": "启用初始化配置",
      "create.firstBoot": "首次启动配置",
      "create.noExtraUserData": "未设置额外用户数据，将使用容器主机名完成初始化。",
      "create.userDataMode": "用户数据配置方式",
      "create.guided": "引导配置",
      "create.advancedYaml": "高级 YAML",
      "create.initialUsername": "初始化用户名",
      "create.usernamePlaceholder": "root 或新用户名",
      "create.loginPassword": "登录密码",
      "create.showPassword": "显示密码",
      "create.hidePassword": "隐藏密码",
      "create.randomPassword": "重新生成 8 位随机密码",
      "create.passwordlessSudo": "授予免密 sudo",
      "create.sshRemote": "开启 SSH 远程管理",
      "create.sshPort": "SSH 远程访问端口",
      "create.sshPortHint": "容器内部 SSH 服务端口，范围 1-65535。",
      "create.rootSshRemote": "开启 root 用户 SSH 远程管理",
      "create.rootSshHint": "自动开启 SSH 远程管理，并允许 root 用户通过 SSH 密码登录。",
      "create.sshKeys": "SSH 公钥",
      "create.sshKeysPlaceholder": "每行一条公钥",
      "create.installPackages": "安装软件包",
      "create.firstBootCommands": "首次启动命令",
      "create.commandPerLine": "每行一条命令",
      "create.userDataYaml": "用户数据 YAML",
      "create.networkDefinition": "网络定义 YAML（可选）",
      "create.sparseImage": "启用稀疏镜像",
      "create.imageSize": "镜像大小 GB",
      "create.natIpHint": "只填写第 4 位（1-254）；留空时按当前容器 IP 顺序自动加一。",
      "create.startAfterCreate": "创建后启动",
      "config.title": "修改运行配置",
      "config.subtitle": "保存时运行中的容器会先停止，并默认恢复启动",
      "config.defaultSameName": "默认同容器名称",
      "config.clearStaticIp": "留空清除",
      "config.natIpHint": "只可修改第 4 位；留空表示清除静态 IP。",
      "config.restoreAfterSave": "保存后恢复启动运行中的容器",
      "config.save": "保存配置",
      "login.title": "WebUI 授权",
      "login.tokenPlaceholder": "输入 authToken",
      "login.submit": "进入",
    },
    en: {
      "document.title": "Droidspaces WebUI",
      "locale.label": "Language",
      "locale.zh-CN": "Chinese",
      "locale.en": "English",
      "locale.shortZh": "Chinese",
      "locale.shortEn": "EN",
      "locale.welcomeTitle": "Choose a display language",
      "locale.welcomeDescription": "This configuration file does not have an interface language yet. Your choice will be saved and applied immediately.",
      "nav.main": "Main navigation",
      "nav.desktop": "Desktop navigation",
      "nav.pages": "Page navigation",
      "nav.overview": "System Resources",
      "nav.containers": "Containers",
      "nav.rootfs": "Images",
      "nav.network": "Network",
      "nav.security": "Security",
      "nav.diagnostics": "Diagnostics",
      "nav.battery": "Battery Monitor",
      "nav.settings": "System Settings",
      "action.refreshData": "Refresh data",
      "action.refresh": "Refresh",
      "action.createContainer": "New container",
      "action.backgroundTasks": "Background tasks",
      "action.taskOutput": "Task output",
      "action.switchPage": "Switch page",
      "action.viewContainers": "View containers",
      "action.openTerminal": "Open terminal",
      "action.connect": "Connect",
      "action.disconnect": "Disconnect",
      "action.clear": "Clear",
      "action.send": "Send",
      "action.uploadTemplate": "Upload template",
      "action.manageSources": "Manage sources",
      "action.close": "Close",
      "action.save": "Save",
      "action.cancel": "Cancel",
      "action.check": "Check",
      "action.settings": "Settings",
      "action.download": "Download",
      "action.delete": "Delete",
      "action.create": "Create",
      "action.edit": "Edit",
      "action.start": "Start",
      "action.stop": "Stop",
      "action.restart": "Restart",
      "action.backup": "Back up",
      "action.details": "Details",
      "action.enter": "Enter",
      "common.unknown": "Unknown",
      "common.loading": "Loading",
      "common.pending": "Awaiting data",
      "common.enabled": "Enabled",
      "common.disabled": "Disabled",
      "common.running": "Running",
      "common.stopped": "Stopped",
      "common.all": "All",
      "common.none": "None",
      "common.waiting": "Waiting to refresh",
      "runtime.webVersionLoading": "WebUI Loading",
      "runtime.versionHintLoading": "WebUI: Loading · Current Core: Loading · Supported Core: Loading",
      "view.overview.title": "Overview",
      "view.overview.subtitle": "System, resource, and network status",
      "view.containers.title": "Containers",
      "view.containers.subtitle": "List, filters, and lifecycle actions",
      "view.detail.title": "Details",
      "view.detail.subtitle": "Container configuration, network, mounts, environment, and backups",
      "view.terminal.title": "Interactive Terminal",
      "view.terminal.subtitle": "Open a shell in a running container",
      "view.rootfs.title": "Image Management",
      "view.network.title": "Network Management",
      "view.network.subtitle": "NAT defaults, network modes, and port forwarding",
      "view.security.title": "Security",
      "view.security.subtitle": "Authorization, TLS, ports, and sensitive container capabilities",
      "view.diagnostics.title": "Diagnostics",
      "view.diagnostics.subtitle": "Host paths, CLI output, and socketd events",
      "view.battery.title": "Battery Monitor",
      "view.battery.subtitle": "Live power, coulomb-counter history, and power ranges",
      "view.settings.title": "System Settings",
      "view.settings.subtitle": "WebUI configuration, paths, image repositories, and Android integration",
      "metric.systemVersion": "System Version",
      "metric.kernelVersion": "Kernel Version",
      "metric.deviceUptime": "Device Uptime",
      "metric.cpuUsage": "CPU Usage",
      "metric.memoryUsage": "Memory Usage",
      "metric.networkIO": "Network I/O / Total Traffic",
      "metric.currentPower": "Current Power",
      "metric.hostUsage": "Overall host usage",
      "metric.usedTotalPending": "Usage / total awaiting data",
      "metric.containerStatus": "Container Status",
      "metric.containersTotal": "Total Containers",
      "metric.localRootfs": "Local Images",
      "metric.activeTasks": "Active Tasks",
      "overview.containerLoading": "Loading container information",
      "overview.hostSummary": "Host Summary",
      "overview.recentEvents": "Recent Events",
      "overview.eventLog": "Event Log",
      "containers.list": "Container List",
      "containers.filter": "Container status filter",
      "containers.running": "Running",
      "containers.notStarted": "Not started",
      "containers.search": "Search name, hostname, or network",
      "containers.startupOrder": "Startup order",
      "containers.none": "No containers",
      "containers.noMatch": "No matching containers",
      "detail.noneSelected": "No container selected",
      "detail.selectHint": "Select a container from the container list",
      "terminal.container": "Container",
      "terminal.user": "User",
      "terminal.manualUser": "Manual user",
      "terminal.disconnected": "Disconnected",
      "terminal.selectRunning": "Select a running container before connecting a shell",
      "terminal.inputPlaceholder": "Mobile input: Enter sends, Shift+Enter adds a line break",
      "rootfs.architecture": "Architecture",
      "rootfs.auto": "Automatic",
      "rootfs.tabs": "Image tools",
      "rootfs.local": "Local Templates",
      "rootfs.backups": "Backups",
      "rootfs.remote": "Cloud Images",
      "rootfs.noBackups": "No backups",
      "rootfs.search": "Search cloud images by distribution or version",
      "rootfs.filterSource": "Filter images by source",
      "rootfs.allImages": "All images",
      "rootfs.remoteSources": "Cloud sources",
      "rootfs.addRepository": "Add repository",
      "rootfs.saveRepositories": "Save repositories",
      "network.defaultConfig": "NAT Default Configuration",
      "network.autoDescription": "The official core detects the upstream for this platform and updates it when the network changes.",
      "network.defaultThirdOctet": "NAT Third Octet",
      "network.defaultPrefix": "Default NAT Prefix",
      "network.gateway": "NAT Gateway",
      "network.upstream": "Upstream (automatic)",
      "network.autoDetected": "Detected by the core",
      "network.ports": "Ports and Network Modes",
      "network.container": "Container",
      "network.status": "Status",
      "network.mode": "Mode",
      "network.portForwarding": "Port Forwarding",
      "security.summary": "Security Summary",
      "security.findings": "Risk Findings",
      "security.privilegedAcknowledgement": "I understand the risk",
      "security.privilegedPrompt": "Type the following before enabling privileged mode: {acknowledgement}",
      "security.privilegedAcknowledgementRequired": "You must type: {acknowledgement}",
      "diagnostics.backendStatus": "Backend Status",
      "diagnostics.backendDescription": "socketd connectivity, fallback sources, and recent backend errors",
      "diagnostics.webuiLogs": "WebUI Service Log",
      "diagnostics.waitingRead": "Waiting to read",
      "diagnostics.logTail": "Number of log lines to read from the end",
      "diagnostics.tail": "Tail",
      "diagnostics.autoFollow": "Auto-follow",
      "diagnostics.autoFollowHint": "Refresh automatically only while Diagnostics is visible",
      "diagnostics.refreshLog": "Refresh WebUI service log",
      "diagnostics.events": "Events",
      "diagnostics.refreshEvents": "Refresh events",
      "diagnostics.noBackendErrors": "No backend errors",
      "diagnostics.hostPaths": "Host and Paths",
      "diagnostics.cli": "CLI Diagnostics",
      "diagnostics.waiting": "Waiting to run",
      "battery.noData": "The device did not provide battery data",
      "battery.monitoringDisabled": "Battery monitoring is disabled",
      "battery.monitoringDisabledRedirect": "Battery monitoring is disabled in System Settings",
      "task.noTasks": "No tasks",
      "task.noTaskOutput": "No task output",
      "task.noActiveTasks": "No active background tasks",
      "task.current": "Current Tasks",
      "settings.savedRestartRequired": "Saved. Restart WebUI for changes to the listen mode, address, or port to fully take effect.",
      "settings.saved": "Saved and synchronized to the configuration file.",
      "settings.configPath": "Configuration file: {path}",
      "settings.noConfigPath": "No configuration file is configured. Runtime changes can be saved but will not persist.",
      "settings.notChecked": "No update check yet",
      "settings.downloadUpdate": "Download and update",
      "settings.unknown": "Unknown",
      "settings.notFetched": "Not fetched",
      "settings.officialRelease": "Official GitHub Release",
      "settings.updating": "Updating",
      "settings.upToDate": "Up to date",
      "settings.updateAvailable": "Update available",
      "settings.checkComplete": "Check complete",
      "settings.currentVersion": "Current Version",
      "settings.latestVersion": "Latest Version",
      "settings.status": "Status",
      "settings.architecture": "Architecture",
      "settings.releaseAsset": "Release Asset",
      "settings.source": "Source",
      "settings.reinstall": "Reinstall",
      "settings.latestOfficialVersion": "Latest official version",
      "settings.updateConfirm": "Download and replace the core with {target} from the official Droidspaces GitHub Release? The existing binary will be backed up as .previous.",
      "settings.updateComplete": "Core updated and daemon refresh requested",
      "settings.njuMirror": "Nanjing University mirror",
      "settings.officialSource": "Official source",
      "settings.configLoading": "Loading configuration",
      "settings.uiLanguage": "Interface Language",
      "settings.uiLanguageHint": "Applies immediately and is written to the WebUI configuration file.",
      "settings.uiLanguageSaved": "Interface language saved.",
      "settings.runtime": "WebUI Runtime",
      "settings.paths": "Path Configuration",
      "settings.androidIntegration": "Android Integration",
      "settings.integrationWaiting": "Awaiting status",
      "settings.coreUpdate": "Core Update",
      "settings.imageRepositories": "Cloud Image Repositories",
      "settings.njuAcceleration": "lxc-image CN Acceleration",
      "settings.njuAccelerationDescription": "Switch existing lxc-image RootFS download URLs to the Nanjing University mirror when enabled.",
      "settings.enableCNAcceleration": "Enable CN Acceleration",
      "settings.batteryStatsRetentionDays": "Battery Statistics Retention (days)",
      "boot.title": "Boot Startup Order",
      "boot.subtitle": "Write startup priorities from top to bottom",
      "boot.noContainers": "No containers are enabled for startup",
      "boot.saveOrder": "Save Order",
      "boot.moveUp": "Move Up",
      "boot.moveDown": "Move Down",
      "task.runStats": "Task statistics for this service run",
      "task.summaryAria": "Background task statistics",
      "task.refreshOutput": "Refresh task output",
      "task.closeOutput": "Close task output",
      "task.refresh": "Refresh background tasks",
      "task.close": "Close background tasks",
      "form.name": "Name",
      "form.hostname": "Hostname",
      "form.network": "Network",
      "form.natStaticIp": "Static NAT IP",
      "form.fourthOctet": "Fourth Octet",
      "form.auto": "Automatic",
      "form.dns": "DNS",
      "form.portForwarding": "Port Forwarding (one rule per line)",
      "form.gatewayContainer": "Gateway Container",
      "form.gatewayNetwork": "Gateway Network",
      "form.gatewayInterface": "Gateway Interface",
      "form.hostBridge": "Host Bridge",
      "form.bindMounts": "Bind Mounts (one per line)",
      "form.init": "Init",
      "form.x11ExtraFlags": "Extra X11 Flags",
      "form.virglExtraFlags": "Extra VirGL Flags",
      "form.memoryLimit": "Memory Limit",
      "form.cpuLimit": "CPU Limit",
      "form.pidsLimit": "PIDs Limit",
      "form.environment": ".env Environment Variables",
      "create.title": "New Container",
      "create.systemTemplate": "System Template",
      "create.selectSystem": "Select a Container System",
      "create.imageSource": "Image Source",
      "create.localTemplate": "Local Template",
      "create.cloudRepository": "Cloud Repository",
      "create.searchLocalTemplate": "Search local templates",
      "create.searchSystem": "Search system or version",
      "create.localTemplateList": "Local Template List",
      "create.cloudImagePicker": "Cloud Image Picker",
      "create.cloudImageList": "Cloud Image List",
      "create.cloudSources": "Filter images by cloud source",
      "create.sourceWaiting": "Source: awaiting selection",
      "create.preDownload": "Pre-download locally",
      "create.cloudTask": "After you select an image, it will download in the background and then create the container.",
      "create.defaultSameName": "Same as name by default",
      "create.cloudInit": "Cloud Image Initialization",
      "create.cloudInitDescription": "Use a NoCloud seed to apply hostname, user data, and optional network configuration at first boot.",
      "create.enableInit": "Enable initialization",
      "create.firstBoot": "First Boot Configuration",
      "create.noExtraUserData": "No additional user data is configured; the container hostname will be used for initialization.",
      "create.userDataMode": "User Data Mode",
      "create.guided": "Guided Setup",
      "create.advancedYaml": "Advanced YAML",
      "create.initialUsername": "Initial Username",
      "create.usernamePlaceholder": "root or a new username",
      "create.loginPassword": "Login Password",
      "create.showPassword": "Show password",
      "create.hidePassword": "Hide password",
      "create.randomPassword": "Generate a new 8-character password",
      "create.passwordlessSudo": "Grant passwordless sudo",
      "create.sshRemote": "Enable SSH remote access",
      "create.sshPort": "SSH Remote Port",
      "create.sshPortHint": "SSH service port inside the container, from 1 to 65535.",
      "create.rootSshRemote": "Allow root SSH remote access",
      "create.rootSshHint": "Enables SSH remote access and allows root to sign in with a password over SSH.",
      "create.sshKeys": "SSH Public Keys",
      "create.sshKeysPlaceholder": "One public key per line",
      "create.installPackages": "Install Packages",
      "create.firstBootCommands": "First Boot Commands",
      "create.commandPerLine": "One command per line",
      "create.userDataYaml": "User Data YAML",
      "create.networkDefinition": "Network Definition YAML (optional)",
      "create.sparseImage": "Use Sparse Image",
      "create.imageSize": "Image Size (GB)",
      "create.natIpHint": "Enter only the fourth octet (1-254). Leave it blank to allocate the next current container IP.",
      "create.startAfterCreate": "Start after creation",
      "config.title": "Edit Runtime Configuration",
      "config.subtitle": "Running containers stop before saving and are restarted by default",
      "config.defaultSameName": "Same as container name by default",
      "config.clearStaticIp": "Leave blank to clear",
      "config.natIpHint": "Only the fourth octet can be changed. Leave blank to clear the static IP.",
      "config.restoreAfterSave": "Restart running containers after saving",
      "config.save": "Save Configuration",
      "login.title": "WebUI Authorization",
      "login.tokenPlaceholder": "Enter authToken",
      "login.submit": "Continue",
    },
  };

  function forEachStorage(callback) {
    ["localStorage", "sessionStorage"].forEach((name) => {
      try {
        const storage = global[name];
        if (storage) callback(storage);
      } catch (_) {
        // Storage can be disabled by browser privacy settings.
      }
    });
  }

  function configuredDefaultLocale() {
    return SUPPORTED_LOCALES.includes(global.DS_UI_LANGUAGE_DEFAULT)
      ? global.DS_UI_LANGUAGE_DEFAULT
      : DEFAULT_LOCALE;
  }

  function readLocalePreference() {
    let storedLocale = "";
    forEachStorage((storage) => {
      if (storedLocale) return;
      try {
        const stored = storage.getItem(STORAGE_KEY);
        if (SUPPORTED_LOCALES.includes(stored)) storedLocale = stored;
      } catch (_) {
        // Continue with the next storage scope.
      }
    });
    return storedLocale;
  }

  function persistLocale(value) {
    let persisted = false;
    forEachStorage((storage) => {
      if (persisted) return;
      try {
        storage.setItem(STORAGE_KEY, value);
        persisted = true;
      } catch (_) {
        // Continue with the next storage scope.
      }
    });
    return persisted;
  }

  const storedLocale = readLocalePreference();
  const hasConfiguredLocale = global.DS_UI_LANGUAGE_CONFIGURED === true;
  let hasLocalePreference = Boolean(storedLocale);
  // Once the service configuration has a language, it is authoritative across
  // browsers. A stored preference only seeds the first-visit choice for an
  // older config that does not yet contain uiLanguage.
  let locale = hasConfiguredLocale ? configuredDefaultLocale() : (storedLocale || configuredDefaultLocale());
  let initialLocaleSetupRequired = !hasConfiguredLocale;

  function interpolate(message, values) {
    if (!values) return message;
    return message.replace(/\{([A-Za-z0-9_]+)\}/g, (match, key) => (
      Object.prototype.hasOwnProperty.call(values, key) ? String(values[key]) : match
    ));
  }

  function translate(key, values) {
    const catalog = messages[locale] || messages[DEFAULT_LOCALE];
    const fallback = messages[DEFAULT_LOCALE][key] || key;
    return interpolate(catalog[key] || fallback, values);
  }

  function translateElement(element) {
    if (element.dataset.i18n) element.textContent = translate(element.dataset.i18n);
    ["title", "aria-label", "aria-valuetext", "placeholder", "value"].forEach((attribute) => {
      const key = element.dataset[`i18n${attribute.replace(/-([a-z])/g, (_, letter) => letter.toUpperCase()).replace(/^./, (letter) => letter.toUpperCase())}`];
      if (!key) return;
      const value = translate(key);
      if (attribute === "value" && "value" in element) element.value = value;
      else element.setAttribute(attribute, value);
    });
  }

  function applyTranslations(root = document) {
    document.documentElement.lang = locale;
    document.title = translate("document.title");
    if (root.nodeType === Node.ELEMENT_NODE) translateElement(root);
    root.querySelectorAll?.("[data-i18n], [data-i18n-title], [data-i18n-aria-label], [data-i18n-aria-valuetext], [data-i18n-placeholder], [data-i18n-value]").forEach(translateElement);
    root.querySelectorAll?.("[data-locale-switcher]").forEach((control) => {
      control.value = locale;
    });
  }

  function updateLocaleWelcome() {
    const overlay = document.querySelector("#localeWelcomeOverlay");
    if (!overlay) return;
    overlay.classList.toggle("hidden", !initialLocaleSetupRequired);
  }

  function setLocale(nextLocale, options = {}) {
    const next = SUPPORTED_LOCALES.includes(nextLocale) ? nextLocale : DEFAULT_LOCALE;
    if (next === locale && hasLocalePreference) return;
    const changed = next !== locale;
    locale = next;
    hasLocalePreference = true;
    const persisted = persistLocale(locale);
    applyTranslations();
    updateLocaleWelcome();
    if (options.reload !== false && persisted) {
      global.location.reload();
      return;
    }
    if (changed) global.dispatchEvent(new CustomEvent("droidspaceslocalechange"));
  }

  function bindLocaleSwitchers() {
    document.querySelectorAll("[data-locale-switcher]").forEach((control) => {
      control.value = locale;
      control.addEventListener("change", (event) => setLocale(event.currentTarget.value, {
        reload: event.currentTarget.dataset.localeNoReload !== "true",
      }));
    });
  }

  function bindLocaleChoices() {
    document.querySelectorAll("[data-locale-choice]").forEach((button) => {
      button.addEventListener("click", () => {
        const setupWasRequired = initialLocaleSetupRequired;
        setLocale(button.dataset.localeChoice, { reload: false });
        if (!setupWasRequired) return;
        initialLocaleSetupRequired = false;
        updateLocaleWelcome();
        global.dispatchEvent(new CustomEvent("droidspacesinitiallocalechoice", { detail: { locale } }));
      });
    });
  }

  function setInitialLocaleSetupRequired(value) {
    initialLocaleSetupRequired = Boolean(value);
    updateLocaleWelcome();
  }

  global.DS_I18N = {
    DEFAULT_LOCALE,
    STORAGE_KEY,
    locales: SUPPORTED_LOCALES.slice(),
    getLocale: () => locale,
    getDefaultLocale: configuredDefaultLocale,
    hasLocalePreference: () => hasLocalePreference,
    isInitialLocaleSetupRequired: () => initialLocaleSetupRequired,
    setInitialLocaleSetupRequired,
    setLocale,
    t: translate,
    applyTranslations,
  };

  function initialize() {
    applyTranslations();
    bindLocaleSwitchers();
    bindLocaleChoices();
    updateLocaleWelcome();
  }

  if (document.readyState === "loading") document.addEventListener("DOMContentLoaded", initialize, { once: true });
  else initialize();
}(window));
