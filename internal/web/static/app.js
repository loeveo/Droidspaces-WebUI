const DEFAULT_OVERVIEW_REFRESH_SECONDS = 3;
const DEFAULT_BATTERY_STATS_SAMPLE_SECONDS = 3;
const DEFAULT_BATTERY_STATS_WRITE_MINUTES = 5;
const DEFAULT_BATTERY_STATS_RETENTION_DAYS = 7;
const BATTERY_POWER_SPLIT_TOLERANCE_W = 0.15;
const BACKGROUND_REFRESH_MS = 30000;
const WEBUI_LOG_REFRESH_MS = 3000;
const NAT_IP_PREFIX = "172.28";
const DEFAULT_NAT_THIRD_OCTET = 1;
const LINUX_CONTAINERS_IMAGE_HOST = "images.linuxcontainers.org";
const LINUX_CONTAINERS_OFFICIAL_URL = "https://images.linuxcontainers.org/";
const DROIDSPACES_OFFICIAL_ROOTFS_REPOSITORY_PATH = "/droidspaces/droidspaces-rootfs-builder/";
const LINUX_CONTAINERS_NAME = "lxc-image";
const LINUX_CONTAINERS_CN_MIRROR_URL = "https://mirror.nju.edu.cn/lxc-images/";
const LINUX_CONTAINERS_CN_MIRROR_NAME = "lxc-image CN（南京大学镜像）";
const LEGACY_LINUX_CONTAINERS_NAME = "Linux Containers";
const LEGACY_LINUX_CONTAINERS_CN_MIRROR_NAME = "Linux Containers CN（南京大学镜像）";
const ROOTFS_ASSET_MEMORY_CACHE_MS = 24 * 60 * 60 * 1000;
const CLOUD_INIT_MAX_DOCUMENT_BYTES = 64 * 1024;
const CLOUD_INIT_RANDOM_PASSWORD_LENGTH = 8;
const CLOUD_INIT_RANDOM_PASSWORD_ALPHABET = "ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz23456789";

const state = {
  containers: [],
  selected: "",
  selectedDetail: null,
  busy: false,
  authenticated: !window.DS_AUTH_REQUIRED,
  status: {},
  host: {},
  events: [],
  lastEventSince: 0,
  currentView: "overview",
  containerFilter: "running",
  rootfsTab: "local",
  detailTab: "summary",
  serviceFilter: "running",
  serviceSearch: "",
  terminalSocket: null,
  terminalConnected: false,
  terminalTarget: "",
  terminalLines: [""],
  terminalRow: 0,
  terminalCol: 0,
  backendErrorLog: [],
  rootfsAssets: [],
  rootfsAssetsLoaded: false,
  rootfsAssetsLoadedAt: 0,
  rootfsAssetsArchitecture: "",
  rootfsLoading: false,
  rootfsRepositories: [],
  rootfsSourceFilter: "",
  rootfsRepositoryEditorOpen: false,
  rootfsErrors: [],
  systemSettings: {},
  systemSettingsLoaded: false,
  confirmedUILanguage: "",
  pendingInitialUILanguage: "",
  initialUILanguageSavePromise: null,
  uiLanguageSaveVersion: 0,
  uiLanguageSaveCompletedVersion: 0,
  uiLanguageSavePending: false,
  settingsWriteVersion: 0,
  settingsWriteCompletedVersion: 0,
  settingsWritePending: false,
  settingsSaveQueue: Promise.resolve(),
  systemSettingsSaving: false,
  coreUpdate: null,
  networkSettings: { defaultNatCIDR: "172.28.0.0/16", defaultNatThirdOctet: DEFAULT_NAT_THIRD_OCTET, natGatewayIP: "172.28.0.1" },
  containerUsers: {},
  containerServices: {},
  bootPriorityContainers: [],
  systemdUnit: null,
  diagnostics: {},
  localRootfs: [],
  batteryPower: null,
  tasks: {},
  taskSummary: { total: 0, active: 0, pending: 0, running: 0, done: 0, error: 0, cancelled: 0, byKind: {} },
  containerTasks: {},
  configTarget: "",
  configSaveInProgress: false,
  configNatThirdOctet: 0,
  overviewRefreshInFlight: false,
  overviewRefreshTimer: 0,
  webuiLog: { path: "", exists: false, truncated: false, lines: [], error: "", updatedAt: 0, loaded: false },
  webuiLogTail: 250,
  webuiLogAutoFollow: true,
  webuiLogLoading: false,
  webuiLogRefreshTimer: 0,
};

const $ = (selector) => document.querySelector(selector);
const $$ = (selector) => Array.from(document.querySelectorAll(selector));
const t = (key, values) => window.DS_I18N?.t(key, values) || key;
const uiText = (zh, en) => (window.DS_I18N?.getLocale?.() === "en" ? en : zh);
const uiLocale = () => (window.DS_I18N?.getLocale?.() === "en" ? "en-US" : "zh-CN");

function translateContainerOptionGrid(prefix) {
  const options = [
    ["AndroidStorage", "Android 存储", "Android Storage", "挂载 Android 存储。", "Mount Android storage."],
    ["HwAccess", "硬件访问", "Hardware Access", "完整的硬件访问权限。", "Full hardware access permissions."],
    ["Gpu", "GPU 访问", "GPU Access", "将 GPU 节点镜像到隔离的 /dev（非完整硬件直通）。", "Mirror GPU nodes into the isolated /dev (not full hardware passthrough)."],
    ["TermuxX11", "配置 Termux:X11", "Configure Termux:X11", "为 Termux:X11 启用 X11 套接字挂载。", "Enable the X11 socket mount for Termux:X11."],
    ["Virgl", "配置 VirGL 3D 加速功能", "Configure VirGL 3D Acceleration", "通过 VirGL 启用 3D 硬件加速。", "Enable 3D hardware acceleration through VirGL."],
    ["Pulse", "配置 PulseAudio", "Configure PulseAudio", "通过 PulseAudio 启用音频支持。", "Enable audio support through PulseAudio."],
    ["SelinuxPermissive", "SELinux 宽容模式", "SELinux Permissive Mode", "将 SELinux 设置为宽容模式。", "Set SELinux to permissive mode."],
    ["AllowUserns", "允许用户命名空间", "Allow User Namespaces", "为 Docker、Podman 等嵌套容器运行时解除用户命名空间限制。", "Allow user namespaces for nested runtimes such as Docker and Podman."],
    ["Volatile", "易失模式", "Volatile Mode", "仅内存模式（停止后更改将丢失）。", "Memory-only mode; changes are lost after stopping."],
    ["RunAtBoot", "开机启动", "Run at Boot", "加入 Droidspaces 的容器启动队列。", "Add this container to the Droidspaces startup queue."],
    ["ForceCgroupV1", "强制 Cgroup V1", "Force Cgroup V1", "强制使用旧版 Cgroup V1 层级结构。", "Force the legacy Cgroup V1 hierarchy."],
    ["DisableIpv6", "禁用 IPv6", "Disable IPv6", "在此容器中禁用 IPv6。这可能会导致宿主机的 VPN 应用异常。", "Disable IPv6 in this container. This can affect VPN applications on the host."],
    ["BlockNestedNS", "手动死锁防护", "Manual Deadlock Protection", "通过阻止嵌套命名空间创建，防止旧版内核上的 VFS 死锁；会屏蔽 Docker、Podman、LXC 及 Systemd 沙盒功能。", "Prevent VFS deadlocks on older kernels by blocking nested namespaces; this disables Docker, Podman, LXC, and systemd sandboxing."],
  ];
  options.forEach(([suffix, zhName, enName, zhDescription, enDescription]) => {
    const label = $(`#${prefix}${suffix}`)?.closest("label");
    if (!label) return;
    const name = label.querySelector(".option-name");
    const description = label.querySelector(".option-desc");
    if (name) name.textContent = uiText(zhName, enName);
    if (description) description.textContent = uiText(zhDescription, enDescription);
  });

  const privileged = $(`#${prefix}PrivilegedMode`);
  if (!privileged) return;
  const legend = privileged.querySelector("legend");
  const summary = privileged.querySelector(".privileged-summary");
  if (legend) legend.textContent = uiText("特权模式", "Privileged Mode");
  if (summary) summary.textContent = uiText("为嵌套虚拟化或高级硬件访问配置细粒度的安全限制放宽。", "Configure fine-grained security relaxations for nested virtualization or advanced hardware access.");
  const descriptions = {
    full: ["同时启用所有特权标志。", "Enable all privileged flags at once."],
    nomask: ["禁用隔离掩码以允许对 /proc 和 /sys 的写访问。注意：除非同时启用了“硬件访问”，否则 /sys 将保持只读状态。", "Disable isolation masks to allow writes to /proc and /sys. Note: /sys remains read-only unless Hardware Access is enabled."],
    nocaps: ["保留所有 Linux Capabilities（完整 root 权限）。", "Retain all Linux capabilities (full root privileges)."],
    noseccomp: ["禁用 Seccomp 系统调用过滤。启用非特权用户命名空间等高级内核功能，是嵌套沙箱的必要条件，并允许加载树外内核模块。", "Disable Seccomp syscall filtering. This enables advanced kernel features such as unprivileged user namespaces, supports nested sandboxes, and allows out-of-tree kernel modules."],
    shared: ["启用至宿主机的 MS_SHARED 挂载传播。", "Enable MS_SHARED mount propagation to the host."],
    "unfiltered-dev": ["绕过设备过滤。宿主机的所有 /dev 节点都将可见。", "Bypass device filtering. All host /dev nodes become visible."],
  };
  privileged.querySelectorAll('input[type="checkbox"]').forEach((input) => {
    const description = input.closest("label")?.querySelector(".option-desc");
    const pair = descriptions[input.value];
    if (description && pair) description.textContent = uiText(pair[0], pair[1]);
  });
}

function applyLocalizedDynamicLabels() {
  translateContainerOptionGrid("create");
  translateContainerOptionGrid("config");
  const fieldLabels = [
    ["#settingsMode", "监听模式", "Listen Mode"],
    ["#settingsHost", "监听地址", "Listen Address"],
    ["#settingsPort", "监听端口", "Listen Port"],
    ["#settingsOverviewRefreshSeconds", "概览刷新间隔（秒）", "Overview Refresh Interval (seconds)"],
    ["#settingsOverviewPowerEnabled", "首页显示当前功耗", "Show Current Power on Overview"],
    ["#settingsBatteryMonitoringEnabled", "启用电池监控", "Enable Battery Monitoring"],
    ["#settingsBatteryStatsSampleSeconds", "电池统计采样间隔（秒）", "Battery Statistics Sample Interval (seconds)"],
    ["#settingsBatteryStatsWriteMinutes", "电池统计写入间隔（分钟）", "Battery Statistics Write Interval (minutes)"],
    ["#settingsBatteryStatsRetentionDays", "电池统计保留天数", "Battery Statistics Retention (days)"],
    ["#settingsBatterySeriesCells", "电池串联结构", "Battery Series Cells"],
    ["#settingsAuthToken", "授权 Token", "Authorization Token"],
    ["#settingsSocketdEnabled", "启用 socketd 后端", "Enable socketd Backend"],
    ["#settingsRootfsSkipTLSVerify", "镜像下载跳过 TLS 校验", "Skip TLS Verification for Image Downloads"],
    ["#settingsBatteryDirectPower", "设备支持直供电/旁路供电", "Device Supports Direct/Bypass Power"],
    ["#settingsDroidspacesPath", "Droidspaces 二进制", "Droidspaces Binary"],
    ["#settingsCorePath", "核心目录", "Core Directory"],
    ["#settingsTemplateImageRoot", "模板镜像目录", "Template Image Directory"],
    ["#settingsWorkspace", "工作区", "Workspace"],
    ["#settingsConfigPath", "配置文件", "Configuration File"],
    ["#settingsDefaultNatThirdOctet", "NAT 第 3 位", "NAT Third Octet"],
    ["#settingsDefaultNatCIDR", "NAT 默认前缀", "Default NAT Prefix"],
    ["#settingsNatGatewayIP", "NAT 网关", "NAT Gateway"],
    ["#settingsNatUpstreamMode", "上游出口（自动）", "Upstream (Automatic)"],
    ["#settingsNestedAndroidNatCompat", "嵌套 Android NAT 路由兼容（仅在 Linux 容器中运行 WebUI 时启用；不修改防火墙）", "Nested Android NAT Routing Compatibility (only when WebUI runs in a Linux container; does not modify the firewall)"],
    ["#settingsDaemonMode", "Daemon mode", "Daemon Mode"],
    ["#settingsSymlinkEnabled", "system/bin 软链接", "system/bin Symlink"],
  ];
  fieldLabels.forEach(([selector, zh, en]) => {
    const field = $(selector);
    const text = field?.closest("label")?.querySelector("span");
    if (text) text.textContent = uiText(zh, en);
  });
  const sectionLabels = [
    ["#settingsMode", ".settings-card", "WebUI 运行", "WebUI Runtime"],
    ["#settingsDroidspacesPath", ".settings-card", "路径配置", "Path Configuration"],
    ["#settingsDefaultNatThirdOctet", ".settings-card", "NAT 默认配置", "NAT Default Configuration"],
    ["#settingsDaemonMode", ".settings-card", "Android 集成", "Android Integration"],
  ];
  sectionLabels.forEach(([selector, cardSelector, zh, en]) => {
    const heading = $(selector)?.closest(cardSelector)?.querySelector("h3");
    if (heading) heading.textContent = uiText(zh, en);
  });
  const batteryOptions = [
    ["#batteryPowerHours option[value=\"1\"]", "最近 1 小时", "Last 1 Hour"],
    ["#batteryPowerHours option[value=\"6\"]", "最近 6 小时", "Last 6 Hours"],
    ["#batteryPowerHours option[value=\"24\"]", "最近 24 小时", "Last 24 Hours"],
    ["#batteryPowerHours option[value=\"72\"]", "最近 72 小时", "Last 72 Hours"],
    ["#batteryPowerHours option[value=\"168\"]", "最近 7 天", "Last 7 Days"],
    ["#batteryPowerHours option[value=\"336\"]", "最近 14 天", "Last 14 Days"],
    ["#batteryPowerHours option[value=\"720\"]", "最近 30 天", "Last 30 Days"],
    ["#batteryPowerHours option[value=\"2160\"]", "最近 90 天", "Last 90 Days"],
    ["#batteryPowerHours option[value=\"4320\"]", "最近 180 天", "Last 180 Days"],
    ["#batteryPowerHours option[value=\"8760\"]", "最近 365 天", "Last 365 Days"],
  ];
  batteryOptions.forEach(([selector, zh, en]) => {
    const option = $(selector);
    if (option) option.textContent = uiText(zh, en);
  });
  const batteryHeadings = [
    ["#batteryPowerBins", "电池放电功率区间（仅放电）", "Battery Discharge Power Ranges (Discharging Only)"],
    ["#inputPowerBins", "外部输入功率区间", "External Input Power Ranges"],
    ["#batteryPowerChart", "功率曲线", "Power Chart"],
  ];
  batteryHeadings.forEach(([selector, zh, en]) => {
    const heading = $(selector)?.closest(".settings-card")?.querySelector("h3");
    if (heading) heading.textContent = uiText(zh, en);
  });
  const seriesOptions = [
    ["0", "自动识别", "Automatic"],
    ["1", "1S 单节/单块", "1S Single Cell/Pack"],
    ["2", "2S 双串", "2S Dual Series"],
    ["3", "3S 三串", "3S Triple Series"],
    ["4", "4S 四串", "4S Four Series"],
    ["5", "5S 五串", "5S Five Series"],
    ["6", "6S 六串", "6S Six Series"],
  ];
  seriesOptions.forEach(([value, zh, en]) => {
    const option = $(`#settingsBatterySeriesCells option[value="${value}"]`);
    if (option) option.textContent = uiText(zh, en);
  });
  const integrationStatus = $("#settingsIntegrationStatus");
  if (integrationStatus) integrationStatus.textContent = uiText("等待状态", "Awaiting status");
  $("#coreUpdateCheckBtn")?.setAttribute("title", uiText("检查核心更新", "Check for Core Updates"));
  $("#coreUpdateCheckBtn")?.setAttribute("aria-label", uiText("检查核心更新", "Check for Core Updates"));
  const settingsRepoAddButton = $("#settingsRepoAddBtn");
  if (settingsRepoAddButton) settingsRepoAddButton.textContent = t("rootfs.addRepository");
}

const SERVICE_FILTERS = [
  ["running", "运行中"],
  ["enabled", "已启用"],
  ["disabled", "已禁用"],
  ["abnormal", "异常"],
  ["static", "静态"],
  ["masked", "已屏蔽"],
  ["all", "全部"],
];

const SERVICE_EMPTY_TEXT = {
  running: "没有运行中的服务",
  enabled: "没有已启用的服务",
  disabled: "没有已禁用的服务",
  abnormal: "没有异常服务",
  static: "没有静态服务",
  masked: "没有已屏蔽的服务",
  all: "未找到服务",
};

let inMemoryAuthToken = "";
let authTokenLoaded = false;

function getAuthToken() {
  if (!authTokenLoaded) {
    authTokenLoaded = true;
    try {
      inMemoryAuthToken = localStorage.getItem("DS_WEBUI_AUTH_TOKEN") || "";
    } catch (_) {
      // Browser privacy settings can deny storage without denying API access.
    }
  }
  return inMemoryAuthToken;
}

function setAuthToken(token) {
  inMemoryAuthToken = token || "";
  authTokenLoaded = true;
  try {
    if (token) localStorage.setItem("DS_WEBUI_AUTH_TOKEN", token);
    else localStorage.removeItem("DS_WEBUI_AUTH_TOKEN");
  } catch (_) {
    // Keep the token for this page session when persistent storage is disabled.
  }
}

function authHeaders() {
  const headers = { "Content-Type": "application/json" };
  const token = getAuthToken();
  if (token) headers.Authorization = `Bearer ${token}`;
  return headers;
}

async function api(path, options = {}) {
  const response = await fetch(path, {
    ...options,
    headers: { ...authHeaders(), ...(options.headers || {}) },
  });
  const data = await response.json().catch(() => ({}));
  if (!response.ok) {
    if (response.status === 401 && window.DS_AUTH_REQUIRED) {
      state.authenticated = false;
      setAuthToken("");
      showLogin(uiText("授权失败，请重新输入 token", "Authorization failed. Enter the token again."));
    }
    throw new Error(data.error || `HTTP ${response.status}`);
  }
  return data;
}

async function loginWithToken(token) {
  setAuthToken(token);
  const response = await fetch("/api/login", { method: "POST", headers: authHeaders() });
  const data = await response.json().catch(() => ({}));
  if (!response.ok) {
    setAuthToken("");
    throw new Error(data.error || uiText("登录失败", "Login failed"));
  }
  state.authenticated = true;
  hideLogin();
  await loadSystemSettings(false).catch(() => {});
  await savePendingInitialUILanguage().catch((err) => toast(err.message));
  await refreshAll();
  restartOverviewRefreshTimer();
}

function showLogin(message = "") {
  const overlay = $("#loginOverlay");
  if (!overlay) return;
  overlay.classList.remove("hidden");
  $("#loginError").textContent = message;
  setTimeout(() => $("#loginToken")?.focus(), 0);
}

function hideLogin() {
  $("#loginOverlay")?.classList.add("hidden");
}

function setBusy(value) {
  state.busy = value;
  $$("#refreshBtn, #rootfsRefreshBtn, #hostRefreshBtn, #eventsBtn, #detailRefreshBtn, .row-actions button, .tool-buttons button, .modal-actions button, #configForm button").forEach((button) => {
    button.disabled = value;
  });
  if (state.rootfsLoading) setRootfsLoading(true);
  updateTerminalControls();
  if (!value) updateCreateFormValidation();
}

function setRootfsLoading(value) {
  state.rootfsLoading = Boolean(value);
  const disabled = state.rootfsLoading || state.busy;
  const refresh = $("#rootfsRefreshBtn");
  if (refresh) {
    refresh.disabled = disabled;
    refresh.classList.toggle("loading", state.rootfsLoading);
    refresh.setAttribute("aria-busy", String(state.rootfsLoading));
  }
  const arch = $("#rootfsArch");
  if (arch) arch.disabled = disabled;
  const sourceFilter = $("#rootfsSourceFilter");
  if (sourceFilter) sourceFilter.disabled = disabled || sourceFilter.options.length <= 1;
  const createRefresh = $("#createCloudPickerRefreshBtn");
  if (createRefresh) {
    createRefresh.disabled = disabled;
    createRefresh.classList.toggle("loading", state.rootfsLoading);
    createRefresh.setAttribute("aria-busy", String(state.rootfsLoading));
  }
  if (createTemplateSource() === "cloud") renderCreateCloudTemplatePicker();
}

function toast(message, duration = 3600) {
  const node = $("#toast");
  if (!node) return;
  node.textContent = message;
  node.classList.remove("hidden");
  clearTimeout(node._timer);
  node._timer = setTimeout(() => node.classList.add("hidden"), duration);
}

function fmtTime(unix) {
  if (!unix) return uiText("未知", "Unknown");
  return new Date(unix * 1000).toLocaleString(uiLocale());
}

function fmtBytes(value) {
  if (!value) return uiText("无限制", "Unlimited");
  const units = ["B", "KiB", "MiB", "GiB", "TiB"];
  let n = Number(value);
  let i = 0;
  while (n >= 1024 && i < units.length - 1) {
    n /= 1024;
    i += 1;
  }
  return `${n.toFixed(i === 0 ? 0 : 1)} ${units[i]}`;
}

function fmtBytesOptional(value) {
  if (value === undefined || value === null || value === "") return uiText("未知", "Unknown");
  if (Number(value) === 0) return "0 B";
  return fmtBytes(value);
}

function resourceInputValue(value) {
  if (value === undefined || value === null || value === "") return "";
  return String(value);
}

function memoryLimitInputValue(value) {
  const text = resourceInputValue(value).trim();
  if (!/^\d+$/.test(text)) return text;
  const bytes = Number(text);
  if (!Number.isSafeInteger(bytes) || bytes <= 0) return text;
  const units = [[1024 ** 4, "T"], [1024 ** 3, "G"], [1024 ** 2, "M"], [1024, "K"]];
  for (const [size, suffix] of units) {
    if (bytes % size === 0) return `${bytes / size}${suffix}`;
  }
  return text;
}

function cpusFromQuota(quota, period) {
  const q = Number(quota);
  const p = Number(period || 100000);
  if (!Number.isFinite(q) || q <= 0 || !Number.isFinite(p) || p <= 0) return "";
  return String(Math.round((q / p) * 10) / 10);
}

function cpuLimitPayload(value) {
  const text = String(value || "").trim();
  return { cpus: text };
}

function fmtPercent(value) {
  if (value === undefined || value === null || value === "") return uiText("待上报", "Awaiting data");
  const n = Number(value);
  if (!Number.isFinite(n)) return String(value);
  return `${n > 1 && n <= 100 ? n.toFixed(1) : (n * 100).toFixed(1)}%`;
}

function fmtNumber(value, digits = 1) {
  const n = Number(value);
  if (!Number.isFinite(n)) return "-";
  return n.toFixed(digits);
}

function metricNumber(value) {
  const n = Number(value);
  return Number.isFinite(n) ? n : null;
}

function usagePercentValue(value) {
  const n = metricNumber(value);
  if (n === null || n < 0) return null;
  const percent = n <= 1 ? n * 100 : n;
  return Math.min(percent, 100);
}

function usagePercentText(value) {
  const percent = usagePercentValue(value);
  return percent === null ? uiText("待上报", "Awaiting data") : `${fmtCompactNumber(percent, 1)}%`;
}

function setUsageMeter(id, percent, label) {
  const meter = $(`#${id}`);
  if (!meter) return;
  const hasValue = percent !== null;
  const displayed = hasValue ? Math.min(Math.max(percent, 0), 100) : 0;
  meter.style.setProperty("--usage-percent", `${displayed}%`);
  meter.classList.toggle("unknown", !hasValue);
  if (hasValue) {
    meter.setAttribute("aria-valuenow", String(Math.round(displayed * 10) / 10));
    meter.setAttribute("aria-valuetext", `${label} ${fmtCompactNumber(displayed, 1)}%`);
  } else {
    meter.removeAttribute("aria-valuenow");
    meter.setAttribute("aria-valuetext", uiText(`${label}待上报`, `${label} awaiting data`));
  }
}

function fmtCompactNumber(value, digits = 1) {
  const n = metricNumber(value);
  if (n === null) return "";
  const text = n.toFixed(digits);
  return text.includes(".") ? text.replace(/\.?0+$/, "") : text;
}

function fmtWh(value) {
  const n = metricNumber(value);
  if (n === null) return "";
  return `${fmtCompactNumber(n, Math.abs(n) < 10 ? 2 : 1)} Wh`;
}

function fmtMah(value) {
  const n = metricNumber(value);
  if (n === null) return "";
  return `${fmtCompactNumber(n, 0)} mAh`;
}

function fmtRuntimeHours(value) {
  const n = metricNumber(value);
  if (n === null || n < 0) return "";
  const minutes = Math.round(n * 60);
  if (minutes <= 0) return uiText("<1分钟", "<1 min");
  const hours = Math.floor(minutes / 60);
  const rest = minutes % 60;
  if (hours <= 0) return uiText(`${rest}分钟`, `${rest} min`);
  return rest > 0 ? uiText(`${hours}小时${rest}分钟`, `${hours}h ${rest}m`) : uiText(`${hours}小时`, `${hours}h`);
}

function fmtBatteryHealth(value) {
  const n = metricNumber(value);
  if (n === null) return "";
  const percent = n <= 1 ? n * 100 : n;
  return `${fmtCompactNumber(percent, 1)}%`;
}

function fmtEnergyWithMah(wh, mah) {
  const whText = fmtWh(wh);
  const mahText = fmtMah(mah);
  if (!whText) return mahText;
  return mahText ? `${whText}(${mahText})` : whText;
}

function statMetric(stats, key, hasKey) {
  if (stats && stats[hasKey] === false) return null;
  const n = metricNumber(stats ? stats[key] : undefined);
  if (n === null) return null;
  if (stats && stats[hasKey] !== true && n === 0) return null;
  return n;
}

function positiveStatMetric(stats, key, hasKey = "") {
  const n = statMetric(stats, key, hasKey);
  return n !== null && n > 0 ? n : null;
}

function positiveEnergyMetric(stats, key, hasKey = "") {
  const n = positiveStatMetric(stats, key, hasKey);
  return n !== null && n >= 0.005 ? n : null;
}

function batteryHasMetric(battery, flag, key) {
  if (!battery || battery[flag] === false) return false;
  return battery[flag] === true || metricNumber(battery[key]) !== null;
}

function batteryDirection(battery, currentMA, hasCurrent, powerW, hasPower) {
  const reported = String(battery?.batteryDirection || "").trim().toLowerCase();
  if (["charging", "discharging", "idle", "unknown"].includes(reported)) return reported;
  const text = String(battery?.status || "").trim().toLowerCase();
  if (text.includes("not charging")) return "idle";
  if (text.includes("discharg")) return "discharging";
  if (text.includes("charg")) return "charging";
  if (text.includes("full")) return "idle";
  if (hasCurrent && Math.abs(currentMA) >= 1) return currentMA < 0 ? "discharging" : "charging";
  if (hasPower && Math.abs(powerW) >= 0.01) return powerW < 0 ? "discharging" : "charging";
  return "unknown";
}

function formatSignedBatteryMetric(value, unit, direction, available, precision, alreadySigned = false) {
  if (!available || !Number.isFinite(value)) return uiText("未上报", "Awaiting data");
  if (Math.abs(value) < (unit === "W" ? 0.01 : 1)) return `0 ${unit}`;
  const signed = alreadySigned ? value : (direction === "discharging" ? -Math.abs(value) : (direction === "charging" ? Math.abs(value) : value));
  const sign = signed > 0 ? "+" : "";
  return `${sign}${fmtCompactNumber(signed, precision)} ${unit}`;
}

function batteryRemainingSourceLabel(source) {
  if (source === "database") return uiText("库仑计", "Coulomb counter");
  if (source === "energy" || source === "charge") return uiText("硬件", "Hardware");
  if (source === "capacity") return uiText("电量百分比", "Battery percentage");
  return "";
}

function batteryPresentation(battery) {
  if (battery?.enabled === false) {
    return {
      available: false,
      homeMetricLabel: uiText("当前功耗", "Current Power"),
      homeMain: uiText("电池监控已关闭", "Battery monitoring is disabled"),
      homeDetail: uiText("可在系统设置中重新启用", "It can be re-enabled in System Settings"),
      sourceLabel: uiText("已关闭", "Disabled"),
      statusLabel: uiText("电池监控已关闭", "Battery monitoring is disabled"),
      statusClass: "unknown",
    };
  }
  if (!battery || !battery.available) {
    return {
      available: false,
      homeMetricLabel: uiText("当前功耗", "Current Power"),
      homeMain: uiText("功耗未上报", "Power data unavailable"),
      homeDetail: "",
      sourceLabel: uiText("未上报", "Awaiting data"),
      statusLabel: uiText("设备未提供电池数据", "The device did not provide battery data"),
      statusClass: "unknown",
    };
  }

  const hasCapacity = batteryHasMetric(battery, "hasCapacity", "capacityPercent");
  const hasCurrent = batteryHasMetric(battery, "hasCurrent", "currentMa");
  const hasVoltage = batteryHasMetric(battery, "hasVoltage", "voltageV");
  const hasPower = batteryHasMetric(battery, "hasPower", "powerW");
  const hasInputCurrent = batteryHasMetric(battery, "hasInputCurrent", "inputCurrentMa");
  const hasInputVoltage = batteryHasMetric(battery, "hasInputVoltage", "inputVoltageV");
  const hasInputPower = batteryHasMetric(battery, "hasInputPower", "inputPowerW");
  const hasTemperature = batteryHasMetric(battery, "hasTemperature", "temperatureC");
  const capacity = metricNumber(battery.capacityPercent);
  const currentMA = metricNumber(battery.currentMa);
  const voltageV = metricNumber(battery.voltageV);
  const batteryPowerW = metricNumber(battery.powerW);
  const hasSignedCurrent = batteryHasMetric(battery, "hasSignedCurrent", "signedCurrentMa");
  const hasSignedPower = batteryHasMetric(battery, "hasSignedPower", "signedPowerW");
  const signedCurrentMA = metricNumber(battery.signedCurrentMa);
  const signedBatteryPowerW = metricNumber(battery.signedPowerW);
  const inputCurrentMA = metricNumber(battery.inputCurrentMa);
  const inputVoltageV = metricNumber(battery.inputVoltageV);
  const reportedInputPowerW = metricNumber(battery.inputPowerW);
  const computedInputPowerW = hasInputCurrent && hasInputVoltage && inputCurrentMA !== null && inputVoltageV !== null
    ? Math.abs(inputCurrentMA * inputVoltageV / 1000)
    : null;
  const inputPowerW = hasInputPower && reportedInputPowerW !== null ? Math.abs(reportedInputPowerW) : computedInputPowerW;
  const inputPowerKind = String(battery.inputPowerKind || "measured").trim().toLowerCase();
  const inputPowerIsMeasured = inputPowerKind === "measured";
  const inputPowerIsContract = inputPowerKind === "pd-contract";
  const inputPowerIsAverage = inputPowerKind === "average";
  const inputPowerLabel = inputPowerIsContract
    ? uiText("PD 协商功率", "PD Negotiated Power")
    : (inputPowerIsAverage ? uiText("总输入平均功率", "Average Total Input") : uiText("总输入功率", "Total Input Power"));
  const inputPowerDetail = inputPowerIsContract
    ? uiText("PD 协商档位，不代表设备实时功耗", "The PD contract is not the device real-time power draw")
    : (inputPowerIsAverage
      ? uiText("外部输入平均值，含充电与转换损耗", "Average external input, including charging and conversion losses")
      : uiText("外部输入测量值，含充电与转换损耗", "Measured external input, including charging and conversion losses"));
  const externalPowerActive = battery.externalPowerActive === true
    || (inputPowerW !== null && inputPowerW > 0.01)
    || (hasInputCurrent && inputCurrentMA !== null && Math.abs(inputCurrentMA) > 1);
  const direction = batteryDirection(battery, currentMA || 0, hasCurrent, batteryPowerW || 0, hasPower);
  const reportedPowerMode = String(battery.powerMode || "").trim().toLowerCase();
  const hasPowerMode = ["direct", "charging", "discharging", "external", "idle", "unknown"].includes(reportedPowerMode);
  const fallbackPowerMode = direction === "charging" || direction === "discharging" ? direction : (externalPowerActive ? "external" : direction);
  const powerMode = hasPowerMode ? reportedPowerMode : fallbackPowerMode;
  const directPower = battery.directPowerActive === true || powerMode === "direct";
  const batterySupply = powerMode === "discharging";
  const reportedChargingPowerW = metricNumber(battery.chargingPowerW);
  const hasReportedChargingPower = batteryHasMetric(battery, "hasChargingPower", "chargingPowerW") && reportedChargingPowerW !== null && reportedChargingPowerW > 0.01;
  const fallbackChargingPowerW = powerMode === "charging" && hasSignedPower && signedBatteryPowerW !== null && signedBatteryPowerW > 0.01
    ? signedBatteryPowerW
    : null;
  const chargingPowerW = hasReportedChargingPower ? reportedChargingPowerW : fallbackChargingPowerW;
  const hasChargingPower = chargingPowerW !== null;
  const reportedBoardPowerW = metricNumber(battery.boardPowerEstimateW);
  const hasReportedBoardPower = batteryHasMetric(battery, "hasBoardPowerEstimate", "boardPowerEstimateW") && reportedBoardPowerW !== null && reportedBoardPowerW >= 0;
  const fallbackBoardPowerW = hasChargingPower && inputPowerW !== null && inputPowerIsMeasured
    ? inputPowerW - chargingPowerW
    : null;
  const validFallbackBoardPowerW = fallbackBoardPowerW !== null && fallbackBoardPowerW >= -BATTERY_POWER_SPLIT_TOLERANCE_W
    ? Math.max(0, fallbackBoardPowerW)
    : null;
  const boardPowerEstimateW = hasReportedBoardPower ? reportedBoardPowerW : validFallbackBoardPowerW;
  const hasBoardPowerEstimate = boardPowerEstimateW !== null;
  const hasPowerSplit = powerMode === "charging" && inputPowerW !== null && hasChargingPower && hasBoardPowerEstimate && inputPowerIsMeasured;
  const stats = battery.stats && typeof battery.stats === "object" ? battery.stats : {};
  const remainingWh = statMetric(stats, "estimatedRemainingWh", "hasEstimatedRemainingWh");
  const usableWh = statMetric(stats, "estimatedUsableWh", "hasEstimatedUsableWh");
  const runtimeHours = statMetric(stats, "runtimeHours", "hasRuntime");
  const healthPercent = statMetric(stats, "estimatedHealthPercent", "hasEstimatedHealthPercent");
  const chargeTotal = fmtEnergyWithMah(positiveEnergyMetric(stats, "chargeWh"), positiveStatMetric(stats, "chargeMah"));
  const dischargeTotal = fmtEnergyWithMah(positiveEnergyMetric(stats, "dischargeWh"), positiveStatMetric(stats, "dischargeMah"));
  const inputTotal = fmtWh(positiveEnergyMetric(stats, "inputWh"));
  const capacityText = hasCapacity && capacity !== null ? `${fmtCompactNumber(capacity, 0)}%` : uiText("未上报", "Awaiting data");
  const remainingText = remainingWh !== null ? fmtWh(remainingWh) : uiText("未估算", "Not estimated");
  const remainingSource = batteryRemainingSourceLabel(stats.remainingSource);
  const runtimeText = runtimeHours !== null ? fmtRuntimeHours(runtimeHours) : uiText("暂无法估算", "Not yet estimated");
  const batteryPowerText = directPower
    ? uiText("0 W（旁路）", "0 W (bypass)")
    : formatSignedBatteryMetric(
      hasSignedPower && signedBatteryPowerW !== null ? signedBatteryPowerW : (batteryPowerW || 0),
      "W",
      direction,
      hasSignedPower || hasPower,
      3,
      hasSignedPower,
    );
  const batteryCurrentText = directPower
    ? uiText("0 mA（旁路）", "0 mA (bypass)")
    : formatSignedBatteryMetric(
      hasSignedCurrent && signedCurrentMA !== null ? signedCurrentMA : (currentMA || 0),
      "mA",
      direction,
      hasSignedCurrent || hasCurrent,
      0,
      hasSignedCurrent,
    );
  const inputParts = [];
  if (inputPowerW !== null) inputParts.push(`${fmtCompactNumber(inputPowerW, 3)} W`);
  if (hasInputCurrent && inputCurrentMA !== null) inputParts.push(`${fmtCompactNumber(Math.abs(inputCurrentMA), 0)} mA`);
  if (hasInputVoltage && inputVoltageV !== null) inputParts.push(`${fmtCompactNumber(inputVoltageV, 3)} V`);
  const inputText = inputParts.length ? inputParts.join(" / ") : uiText("未上报", "Awaiting data");
  const totalInputPowerText = inputPowerW !== null ? `${fmtCompactNumber(inputPowerW, 3)} W` : uiText("未上报", "Awaiting data");
  const batteryChargingPowerText = hasChargingPower ? `${fmtCompactNumber(chargingPowerW, 3)} W` : uiText("未上报", "Awaiting data");
  const boardPowerEstimateText = hasBoardPowerEstimate ? `${fmtCompactNumber(boardPowerEstimateW, 3)} W` : uiText("未上报", "Awaiting data");
  const powerSplitDetail = hasPowerSplit
    ? uiText(`总输入 ${totalInputPowerText} - 电池充电 ${batteryChargingPowerText}，包含转换损耗`, `Total input ${totalInputPowerText} - battery charging ${batteryChargingPowerText}; includes conversion losses`)
    : "";
  const voltageText = hasVoltage && voltageV !== null ? `${fmtCompactNumber(voltageV, 3)} V` : uiText("未上报", "Awaiting data");
  const temperature = metricNumber(battery.temperatureC);
  const temperatureText = hasTemperature && temperature !== null ? `${fmtCompactNumber(temperature, 1)} C` : uiText("未上报", "Awaiting data");
  const chargeNow = batteryHasMetric(battery, "hasCharge", "chargeMah") ? fmtMah(battery.chargeMah) : "";
  const energyNow = batteryHasMetric(battery, "hasEnergy", "energyWh") ? fmtWh(battery.energyWh) : "";
  const fullCharge = batteryHasMetric(battery, "hasFullCharge", "fullChargeMah") ? fmtMah(battery.fullChargeMah) : "";
  const fullEnergy = batteryHasMetric(battery, "hasFullEnergy", "fullEnergyWh") ? fmtWh(battery.fullEnergyWh) : "";
  const deviceHealth = batteryHasMetric(battery, "hasHealth", "healthPercent") ? fmtBatteryHealth(battery.healthPercent) : "";

  let sourceLabel = uiText("供电来源未确认", "Power source unconfirmed");
  let sourceDetail = uiText("等待电池端或外部输入数据", "Waiting for battery or external input data");
  let statusLabel = uiText("供电状态未知", "Power state unknown");
  let statusClass = "unknown";
  if (directPower) {
    sourceLabel = uiText("直供电", "Direct Power");
    sourceDetail = uiText("外部输入直接供电，电池未参与供电", "External input powers the device directly; the battery is not supplying power");
    statusLabel = uiText("旁路供电中", "Bypass Power");
    statusClass = "direct";
  } else if (powerMode === "discharging") {
    sourceLabel = uiText("电池供电", "Battery Power");
    sourceDetail = uiText("设备当前由电池供电", "The device is currently powered by the battery");
    statusLabel = uiText("正在放电", "Discharging");
    statusClass = "discharging";
  } else if (powerMode === "charging") {
    sourceLabel = uiText("外部供电", "External Power");
    sourceDetail = uiText("外部输入正在为设备和电池供电", "External input is powering the device and charging the battery");
    statusLabel = uiText("正在充电", "Charging");
    statusClass = "charging";
  } else if (powerMode === "external") {
    sourceLabel = uiText("外部供电", "External Power");
    sourceDetail = uiText("检测到外部输入，电池未参与充放电", "External input detected; the battery is neither charging nor discharging");
    statusLabel = uiText("外部供电中", "Externally Powered");
    statusClass = "idle";
  } else if (powerMode === "idle") {
    sourceLabel = externalPowerActive ? uiText("外部供电", "External Power") : uiText("电池待机", "Battery Idle");
    sourceDetail = externalPowerActive ? uiText("检测到外部输入，电池未充电", "External input detected; the battery is not charging") : uiText("电池当前未充电", "The battery is not charging");
    const full = String(battery.status || "").toLowerCase().includes("full");
    statusLabel = full ? uiText("已充满", "Fully Charged") : uiText("未在充放电", "Idle");
    statusClass = full ? "full" : "idle";
  }

  let currentPowerW = null;
  let currentPowerOrigin = "";
  let currentPowerLabel = uiText("当前功耗", "Current Power");
  if (hasPowerSplit) {
    currentPowerW = boardPowerEstimateW;
    currentPowerOrigin = powerSplitDetail;
    currentPowerLabel = uiText("主板运行功耗（估算）", "Motherboard Power (estimated)");
  } else if (batterySupply && (hasSignedPower || hasPower)) {
    currentPowerW = Math.abs(hasSignedPower && signedBatteryPowerW !== null ? signedBatteryPowerW : (batteryPowerW || 0));
    currentPowerOrigin = uiText("电池端实时功率", "Battery-side real-time power");
    currentPowerLabel = uiText("电池端功耗", "Battery-side Power");
  } else if (inputPowerW !== null && !inputPowerIsContract) {
    currentPowerW = inputPowerW;
    currentPowerOrigin = inputPowerDetail;
    currentPowerLabel = inputPowerLabel;
  } else if (inputPowerIsContract) {
    currentPowerOrigin = inputPowerDetail;
    currentPowerLabel = uiText("外部输入功率", "External Input Power");
  } else if (hasPower && batteryPowerW !== null) {
    currentPowerW = Math.abs(batteryPowerW);
    currentPowerOrigin = uiText("电池端实时功率", "Battery-side real-time power");
    currentPowerLabel = uiText("电池端功耗", "Battery-side Power");
  }
  const currentPowerText = currentPowerW !== null ? `${fmtCompactNumber(currentPowerW, 3)} W` : uiText("未上报", "Awaiting data");
  const homeMain = `${sourceLabel} · ${currentPowerText}`;
  const homeDetail = hasPowerSplit
    ? uiText(`总输入 ${totalInputPowerText} · 电池充电 ${batteryChargingPowerText}`, `Total input ${totalInputPowerText} · Battery charging ${batteryChargingPowerText}`)
    : batterySupply
    ? uiText(`预计可用 ${runtimeText}`, `Estimated runtime ${runtimeText}`)
    : (currentPowerOrigin || "");

  return {
    available: true,
    homeMetricLabel: currentPowerLabel,
    homeMain,
    homeDetail,
    sourceLabel,
    sourceDetail,
    statusLabel,
    statusClass,
    currentPowerText,
    currentPowerOrigin,
    currentPowerLabel,
    inputPowerLabel,
    inputPowerDetail,
    totalInputPowerText,
    batteryChargingPowerText,
    boardPowerEstimateText,
    powerSplitDetail,
    hasPowerSplit,
    capacityText,
    batteryPowerText,
    batteryCurrentText,
    inputText,
    voltageText,
    temperatureText,
    remainingText,
    remainingSource,
    usableText: usableWh !== null ? fmtWh(usableWh) : uiText("未估算", "Not estimated"),
    runtimeText,
    runtimeAvailable: runtimeHours !== null,
    batterySupply,
    energyText: [energyNow, chargeNow].filter(Boolean).join(" / ") || uiText("未上报", "Awaiting data"),
    fullEnergyText: [fullEnergy, fullCharge].filter(Boolean).join(" / ") || uiText("未上报", "Awaiting data"),
    healthText: healthPercent !== null ? fmtBatteryHealth(healthPercent) : (deviceHealth || uiText("未估算", "Not estimated")),
    chargeTotal: chargeTotal || uiText("暂无统计", "No statistics"),
    dischargeTotal: dischargeTotal || uiText("暂无统计", "No statistics"),
    inputTotal: inputTotal || uiText("暂无统计", "No statistics"),
    sampleCount: metricNumber(stats.sampleCount),
    lastSampleTime: metricNumber(stats.lastSampleTime),
    statsMessage: String(stats.message || "").trim(),
  };
}

function batteryText(battery) {
  const model = batteryPresentation(battery);
  return { main: model.homeMain, detail: model.homeDetail, model };
}

function batteryLiveMetric(label, value, detail = "") {
  return `<div class="battery-live-item"><span>${escapeHTML(label)}</span><strong>${escapeHTML(value || uiText("未上报", "Awaiting data"))}</strong>${detail ? `<small>${escapeHTML(detail)}</small>` : ""}</div>`;
}

function renderBatteryLiveOverview(model) {
  const node = $("#batteryLiveOverview");
  if (!node) return;
  if (!model?.available) {
    node.innerHTML = `<div class="empty-state">${escapeHTML(model?.statusLabel || uiText("设备未提供电池数据", "The device did not provide battery data"))}</div>`;
    return;
  }

  const runtimeDetail = model.batterySupply
    ? (model.runtimeAvailable ? uiText("按当前放电功率估算", "Estimated from current discharge power") : uiText("需要更多放电样本", "More discharge samples are needed"))
    : uiText("仅在电池供电时估算", "Estimated only on battery power");
  const sampleDetail = model.lastSampleTime ? uiText(`最近采样 ${fmtTime(model.lastSampleTime)}`, `Last sampled ${fmtTime(model.lastSampleTime)}`) : (model.statsMessage || uiText("等待采样", "Waiting for samples"));
  const critical = [
    batteryLiveMetric(uiText("供电来源", "Power Source"), model.sourceLabel, model.sourceDetail),
    batteryLiveMetric(uiText("当前状态", "Current State"), model.statusLabel, model.statusClass === "discharging" ? uiText("电池正在向设备供电", "The battery is powering the device") : ""),
    ...(model.hasPowerSplit
      ? [
        batteryLiveMetric(model.inputPowerLabel || uiText("总输入功率", "Total Input Power"), model.totalInputPowerText, model.inputPowerDetail),
        batteryLiveMetric(uiText("电池充电功率", "Battery Charging Power"), model.batteryChargingPowerText, uiText("电池端实时功率", "Battery-side real-time power")),
        batteryLiveMetric(uiText("主板运行功耗（估算）", "Motherboard Power (estimated)"), model.boardPowerEstimateText, model.powerSplitDetail),
      ]
      : [batteryLiveMetric(model.currentPowerLabel || uiText("当前功耗", "Current Power"), model.currentPowerText, model.currentPowerOrigin)]),
    batteryLiveMetric(uiText("预计可用", "Estimated Runtime"), model.batterySupply ? model.runtimeText : "--", runtimeDetail),
  ].join("");
  const powerDetails = model.hasPowerSplit
    ? [
      batteryLiveMetric(uiText("电池电流", "Battery Current"), model.batteryCurrentText, uiText("正值充电，负值放电", "Positive charges; negative discharges")),
      batteryLiveMetric(uiText("总输入详情", "Total Input Details"), model.inputText, model.inputPowerDetail),
    ]
    : [
      batteryLiveMetric(uiText("电池功率", "Battery Power"), model.batteryPowerText, uiText("正值充电，负值放电", "Positive charges; negative discharges")),
      batteryLiveMetric(uiText("电池电流", "Battery Current"), model.batteryCurrentText, uiText("正值充电，负值放电", "Positive charges; negative discharges")),
      batteryLiveMetric(uiText("外部输入", "External Input"), model.inputText, model.inputPowerDetail),
    ];
  const details = [
    batteryLiveMetric(uiText("电量", "Charge"), model.capacityText, uiText(`剩余能量 ${model.remainingText}${model.remainingSource ? ` · ${model.remainingSource}` : ""}`, `Remaining energy ${model.remainingText}${model.remainingSource ? ` · ${model.remainingSource}` : ""}`)),
    ...powerDetails,
    batteryLiveMetric(uiText("电池电压", "Battery Voltage"), model.voltageText),
    batteryLiveMetric(uiText("电池温度", "Battery Temperature"), model.temperatureText),
    batteryLiveMetric(uiText("当前能量", "Current Energy"), model.energyText),
    batteryLiveMetric(uiText("满电容量", "Full Capacity"), model.fullEnergyText, uiText(`估算可用 ${model.usableText}`, `Estimated usable ${model.usableText}`)),
    batteryLiveMetric(uiText("电池健康", "Battery Health"), model.healthText),
    batteryLiveMetric(uiText("累计充电", "Total Charge"), model.chargeTotal),
    batteryLiveMetric(uiText("累计放电", "Total Discharge"), model.dischargeTotal),
    batteryLiveMetric(uiText("输入累计", "Total Input"), model.inputTotal),
    batteryLiveMetric(uiText("采样", "Samples"), model.sampleCount !== null ? uiText(`${model.sampleCount} 条`, `${model.sampleCount}`) : uiText("未上报", "Awaiting data"), sampleDetail),
  ].join("");
  const powerNote = model.hasPowerSplit
    ? uiText("主板运行功耗按总输入功率减电池充电功率估算，包含充电与电源转换损耗；没有独立电源轨传感器时，它不是直接测得的主板功耗。", "Motherboard power is estimated as total input minus battery charging power. It includes battery-charging and power-conversion losses, so it is not a direct motherboard-rail measurement.")
    : uiText("电池端功率和电流：正值表示充电，负值表示放电；外部输入功率包含充电和转换损耗，不等同于设备净功耗。", "Battery power and current: positive charges and negative discharges. External input includes charging and conversion losses, so it is not the device net power draw.");
  node.innerHTML = `<div class="battery-live-head ${escapeHTML(model.statusClass)}"><div><span>${uiText("实时供电状态", "Live Power State")}</span><strong>${escapeHTML(model.statusLabel)}</strong><p>${escapeHTML(model.sourceDetail)}</p></div><div><span>${model.hasPowerSplit ? uiText("主板运行功耗（估算）", "Motherboard Power (estimated)") : uiText("当前供电", "Current Supply")}</span><strong>${escapeHTML(model.hasPowerSplit ? model.currentPowerText : model.sourceLabel)}</strong><p>${escapeHTML(model.hasPowerSplit ? model.powerSplitDetail : model.currentPowerText)}</p></div></div><div class="battery-live-grid battery-live-critical${model.hasPowerSplit ? " power-split" : ""}">${critical}</div><div class="battery-live-grid">${details}</div><p class="battery-live-note">${escapeHTML(powerNote)}</p>`;
}

function firstValue(...values) {
  return values.find((value) => value !== undefined && value !== null && value !== "");
}

function nestedValue(object, path) {
  return path.split(".").reduce((acc, key) => (acc && acc[key] !== undefined ? acc[key] : undefined), object);
}

function fmtDisk(value) {
  if (!value) return uiText("未知", "Unknown");
  return fmtBytes(value);
}

function fmtSize(bytes) {
  if (!bytes) return uiText("未知大小", "Unknown size");
  return fmtBytes(bytes);
}

function safeArray(value) {
  return Array.isArray(value) ? value : [];
}

function rootfsRepositoryURL(repository) {
  return String(firstValue(repository?.url, repository?.URL) || "").trim();
}

function isLinuxContainersURL(value) {
  const text = String(value || "").trim();
  if (!text) return false;
  try {
    return new URL(text).hostname.toLowerCase() === LINUX_CONTAINERS_IMAGE_HOST;
  } catch (_) {
    return text.toLowerCase().includes(LINUX_CONTAINERS_IMAGE_HOST);
  }
}

function normalizedRootfsRepositoryURL(value) {
  const text = String(value || "").trim();
  if (!text) return "";
  try {
    const url = new URL(text);
    url.hash = "";
    url.search = "";
    url.pathname = url.pathname.replace(/\/+$/, "") || "/";
    return url.toString().toLowerCase();
  } catch (_) {
    return text.replace(/\/+$/, "").toLowerCase();
  }
}

function isLinuxContainersCNMirrorURL(value) {
  const normalized = normalizedRootfsRepositoryURL(value);
  const mirrorBase = normalizedRootfsRepositoryURL(LINUX_CONTAINERS_CN_MIRROR_URL);
  const mirrorOrigin = normalizedRootfsRepositoryURL(new URL(LINUX_CONTAINERS_CN_MIRROR_URL).origin);
  return normalized === mirrorBase || normalized.startsWith(`${mirrorBase}/`) || normalized === mirrorOrigin;
}

function isLinuxContainersCNMirrorRepositoryURL(value) {
  return normalizedRootfsRepositoryURL(value) === normalizedRootfsRepositoryURL(LINUX_CONTAINERS_CN_MIRROR_URL);
}

function isLinuxContainersOfficialRepositoryURL(value) {
  const normalized = normalizedRootfsRepositoryURL(value);
  const base = normalizedRootfsRepositoryURL(LINUX_CONTAINERS_OFFICIAL_URL);
  return normalized === base || normalized === `${base}streams/v1/images.json`;
}

function isLinuxContainersCNMirrorSource(value) {
  const source = String(value || "").trim().toLowerCase();
  return source === LINUX_CONTAINERS_CN_MIRROR_NAME.toLowerCase()
    || source === LEGACY_LINUX_CONTAINERS_CN_MIRROR_NAME.toLowerCase()
    || ((source.includes("lxc-image") || source.includes("linux containers")) && (source.includes("南京大学") || source.includes("nju")));
}

function isLinuxContainersSource(value) {
  const source = String(value || "").trim().toLowerCase();
  return source === LINUX_CONTAINERS_NAME.toLowerCase()
    || source === LEGACY_LINUX_CONTAINERS_NAME.toLowerCase()
    || source.includes("lxc-image")
    || source.includes("linux containers")
    || source.includes("linuxcontainers")
    || source.includes(LINUX_CONTAINERS_IMAGE_HOST);
}

function rootfsRepositoryInfo(repository) {
  const url = rootfsRepositoryURL(repository);
  const isLinuxContainersCNMirror = isLinuxContainersCNMirrorURL(url);
  const isLinuxContainers = isLinuxContainersURL(url);
  const configuredName = String(firstValue(repository?.name, repository?.Name) || "").trim();
  return {
    url,
    isLinuxContainers: isLinuxContainers || isLinuxContainersCNMirror,
    name: configuredName || (isLinuxContainers ? LINUX_CONTAINERS_NAME : ""),
  };
}

function rootfsAssetDownloadURL(asset) {
  return String(firstValue(asset?.downloadUrl, asset?.downloadURL) || "").trim();
}

function rootfsAssetSource(asset) {
  const source = firstValue(asset?.sourceRepoName, asset?.sourceRepo, asset?.repositoryName, asset?.repository);
  const downloadURL = rootfsAssetDownloadURL(asset);
  if (isLinuxContainersCNMirrorURL(downloadURL) || isLinuxContainersCNMirrorSource(source)) return LINUX_CONTAINERS_CN_MIRROR_NAME;
  if (isLinuxContainersSource(source) || isLinuxContainersURL(downloadURL)) return LINUX_CONTAINERS_NAME;
  return String(source || uiText("未标注来源", "Unlabeled source")).trim() || uiText("未标注来源", "Unlabeled source");
}

function isDroidspacesOfficialRootfsDownloadURL(value) {
  try {
    const parsed = new URL(String(value || "").trim());
    return parsed.hostname.toLocaleLowerCase("en-US") === "github.com"
      && parsed.pathname.toLocaleLowerCase("en-US").startsWith(DROIDSPACES_OFFICIAL_ROOTFS_REPOSITORY_PATH);
  } catch (_) {
    return false;
  }
}

function rootfsAssetVariant(asset) {
  return String(firstValue(asset?.variant, asset?.releaseTitle) || "").trim();
}

function rootfsCloudVariant(asset) {
  return String(asset?.variant || "").trim() || uiText("默认", "Default");
}

function isTinyCloudRootfsAsset(asset) {
  return rootfsCloudVariant(asset).trim().toLowerCase() === "tinycloud";
}

function cloudRootfsAssetsForSelection(assets) {
  return safeArray(assets).filter((asset) => !isTinyCloudRootfsAsset(asset));
}

function formatRootfsBuildDate(value) {
  const text = String(value || "").trim();
  const match = text.match(/^(\d{4})(\d{2})(\d{2})_(\d{2}):(\d{2})$/);
  if (match) return `${match[1]}-${match[2]}-${match[3]} ${match[4]}:${match[5]}`;
  return text || "-";
}

function rootfsSystemVersion(item) {
  return rootfsDisplayName(item).replace(/\s+·\s+(?:minimal|default|base|cloud|server|desktop|xfce|kde|gnome)$/i, "");
}

function localRootfsArchitecture(item) {
  const name = String(firstValue(item?.name, item?.path) || "").toLowerCase();
  const architectures = [
    [/(?:^|[-_.\s])(?:aarch64|arm64)(?:$|[-_.\s])/, "aarch64"],
    [/(?:^|[-_.\s])armhf(?:$|[-_.\s])/, "armhf"],
    [/(?:^|[-_.\s])(?:x86_64|amd64)(?:$|[-_.\s])/, "x86_64"],
    [/(?:^|[-_.\s])(?:x86|i386)(?:$|[-_.\s])/, "x86"],
  ];
  return architectures.find(([pattern]) => pattern.test(name))?.[1] || uiText("未知架构", "Unknown architecture");
}

function localRootfsVariant(item) {
  const declared = String(item?.variant || "").trim();
  if (declared) return declared;
  const name = String(firstValue(item?.name, item?.path) || "").toLowerCase();
  const match = name.match(/(?:^|[-_.\s])(minimal|default|base|cloud|server|desktop|xfce|kde|gnome)(?:$|[-_.\s])/i);
  if (match) return match[1].charAt(0).toUpperCase() + match[1].slice(1).toLowerCase();
  return item?.kind === "directory" ? uiText("目录", "Directory") : uiText("标准", "Standard");
}

function rootfsDisplayName(item) {
  let name = String(firstValue(item?.name, item?.Name) || "").trim();
  if (!name) return uiText("未命名", "Unnamed");

  // Downloaded cloud assets include an internal, collision-resistant suffix. It is
  // useful on disk, but makes a template selector and image card hard to scan.
  name = name.replace(/\.(?:tar\.xz|tar\.gz|tgz|img)$/i, "");
  name = name.replace(/-(?:linux-containers|droidspaces(?:-[a-z0-9]+)?)(?:-[a-z0-9_]+)?-(?:\d{8}_\d{2}-\d{2}|\d{4}-\d{2}-\d{2}|\d{8})-[a-f0-9]{8}$/i, "");

  // Keep decimal version numbers intact. The original filename is commonly
  // separated with underscores or dashes, while releases such as 24.04 must
  // remain readable after the storage-only suffix has been removed.
  const normalized = name.replace(/[_-]+/g, " ").replace(/\s+/g, " ").trim();
  const distributions = [
    [/^alma\s*linux\s*(.*)$/i, "AlmaLinux"],
    [/^alpine(?:\s+linux)?\s*(.*)$/i, "Alpine"],
    [/^alt(?:\s*linux)?\s*(.*)$/i, "ALT Linux"],
    [/^amazon(?:\s*linux)?\s*(.*)$/i, "Amazon Linux"],
    [/^arch(?:\s*linux)?\s*(.*)$/i, "Arch Linux"],
    [/^azure(?:\s*linux)?\s*(.*)$/i, "Azure Linux"],
    [/^busy\s*box\s*(.*)$/i, "BusyBox"],
    [/^cent\s*os\s*(.*)$/i, "CentOS"],
    [/^debian(?:\s+gnu\s*\/?\s*linux)?\s*(.*)$/i, "Debian"],
    [/^devuan\s*(.*)$/i, "Devuan"],
    [/^fedora\s*(.*)$/i, "Fedora"],
    [/^free\s*bsd\s*(.*)$/i, "FreeBSD"],
    [/^gentoo\s*(.*)$/i, "Gentoo"],
    [/^kali(?:\s*linux)?\s*(.*)$/i, "Kali Linux"],
    [/^(?:linux\s*)?mint\s*(.*)$/i, "Linux Mint"],
    [/^nix\s*os\s*(.*)$/i, "NixOS"],
    [/^open\s*euler\s*(.*)$/i, "openEuler"],
    [/^open\s*wrt\s*(.*)$/i, "OpenWrt"],
    [/^open\s*suse\s*(.*)$/i, "openSUSE"],
    [/^oracle(?:\s*linux)?\s*(.*)$/i, "Oracle Linux"],
    [/^plamo\s*(.*)$/i, "Plamo"],
    [/^rocky\s*linux\s*(.*)$/i, "Rocky Linux"],
    [/^slackware\s*(.*)$/i, "Slackware"],
    [/^ubuntu(?:\s+gnu\s*\/?\s*linux)?\s*(.*)$/i, "Ubuntu"],
    [/^void\s*linux\s*(.*)$/i, "Void Linux"],
  ];
  for (const [pattern, label] of distributions) {
    const match = normalized.match(pattern);
    if (match) {
      const release = match[1] || "";
      return [label, displayRootfsRelease(label, release)].filter(Boolean).join(" ");
    }
  }
  return normalized || uiText("未命名", "Unnamed");
}

function displayRootfsRelease(distribution, release) {
  const original = String(release || "").trim().replace(/\s+/g, " ");
  const variantMatch = original.match(/^(.*?)(?:\s+(minimal|default|base|cloud|server|desktop|xfce|kde|gnome))$/i);
  const text = (variantMatch ? variantMatch[1] : original).replace(/^v(?=\d)/i, "");
  const variant = variantMatch ? variantMatch[2].charAt(0).toUpperCase() + variantMatch[2].slice(1).toLowerCase() : "";
  const withVariant = (value) => [value, variant].filter(Boolean).join(" · ");
  const codenameVersions = {
    Debian: { jessie: "8", stretch: "9", buster: "10", bullseye: "11", bookworm: "12", trixie: "13", forky: "14" },
    Ubuntu: { bionic: "18.04", cosmic: "18.10", disco: "19.04", eoan: "19.10", focal: "20.04", groovy: "20.10", hirsute: "21.04", impish: "21.10", jammy: "22.04", kinetic: "22.10", lunar: "23.04", mantic: "23.10", noble: "24.04", oracular: "24.10", plucky: "25.04", questing: "25.10", resolute: "26.04" },
    Devuan: { jessie: "1", ascii: "2", beowulf: "3", chimaera: "4", daedalus: "5", excalibur: "6" },
    "Linux Mint": { ulyssa: "20.1", uma: "20.2", una: "20.3", vanessa: "21", vera: "21.1", victoria: "21.2", virginia: "21.3", wilma: "22", xia: "22.1", zara: "22.2", zena: "22.3" },
  };
  const versions = codenameVersions[distribution];
  if (versions) {
    const match = text.match(/^(?:(\d+(?:\.\d+)?)\s+)?([a-z]+)$/i);
    if (match && versions[match[2].toLowerCase()]) {
      const codename = match[2].toLowerCase();
      return withVariant(`${match[1] || versions[codename]} (${codename})`);
    }
  }
  if (distribution === "CentOS") {
    const stream = text.match(/^(\d+)\s+stream$/i);
    if (stream) return withVariant(`Stream ${stream[1]}`);
  }
  if (["Arch Linux", "Void Linux", "Alpine", "openSUSE"].includes(distribution)) {
    if (["current", "edge", "snapshot", "tumbleweed", "sisyphus"].includes(text.toLowerCase())) {
      return withVariant(text.charAt(0).toUpperCase() + text.slice(1).toLowerCase());
    }
  }
  return withVariant(text);
}

function localRootfsSource(item) {
  return String(firstValue(item?.source, item?.storageSource) || uiText("本地模板", "Local template")).trim() || uiText("本地模板", "Local template");
}

function rootfsAssetOptionLabel(asset) {
  const name = rootfsDisplayName(asset);
  const architecture = String(asset?.architecture || "-").trim() || "-";
  const source = rootfsAssetSource(asset);
  const variant = rootfsAssetVariant(asset);
  return [name, architecture, source, variant].filter(Boolean).join(" · ");
}

function statusBadge(container) {
  if (!container.running) return `<span class="badge stopped">${uiText("已停止", "Stopped")}</span>`;
  const runtime = containerRuntimeInfo(container);
  const attrs = runtime.seconds === null ? "" : ` data-container-runtime-seconds="${runtime.seconds}" data-container-runtime-sampled-at="${Date.now()}"`;
  return `<span class="badge running" title="${uiText("容器运行时间", "Container uptime")}"${attrs}>${escapeHTML(runtime.text)}</span>`;
}

// Keep this order and matching behavior aligned with Android IconUtils.kt.
function distroIconInfoForName(name) {
  const text = String(name || "");
  const lower = text.toLowerCase();
  if (lower.includes("ubuntu")) return { id: "ubuntu", label: "Ubuntu" };
  if (lower.includes("debian")) return { id: "debian", label: "Debian" };
  if (lower.includes("alpine")) return { id: "alpine", label: "Alpine" };
  if (lower.includes("arch-") || lower.includes("arch_") || lower.includes("arch ") || lower === "arch") return { id: "arch", label: "Arch" };
  if (lower.includes("fedora")) return { id: "fedora", label: "Fedora" };
  if (lower.includes("nixos")) return { id: "nixos", label: "NixOS" };
  if (lower.includes("openwrt")) return { id: "openwrt", label: "OpenWrt" };
  if (lower.includes("gentoo")) return { id: "gentoo", label: "Gentoo" };
  if (lower.includes("devuan")) return { id: "devuan", label: "Devuan" };
  if (lower.includes("kali")) return { id: "kali", label: "Kali" };
  if (lower.includes("suse")) return { id: "suse", label: "SUSE" };
  if (lower.includes("centos")) return { id: "centos", label: "CentOS" };
  if (lower.includes("rocky")) return { id: "rocky", label: "Rocky Linux" };
  if (lower.includes("alma")) return { id: "almalinux", label: "AlmaLinux" };
  if (lower.includes("red") || lower.includes("rhel")) return { id: "redhat", label: "Red Hat" };
  if (lower.includes("void")) return { id: "void", label: "Void Linux" };
  if (lower.includes("manjaro")) return { id: "manjaro", label: "Manjaro" };
  if (lower.includes("raspberry") || lower.includes("raspbian")) return { id: "raspberry", label: "Raspberry Pi OS" };
  if (lower.includes("busybox")) return { id: "busybox", label: "BusyBox" };
  if (lower.includes("freebsd")) return { id: "freebsd", label: "FreeBSD" };
  if (lower.includes("slackware")) return { id: "slackware", label: "Slackware" };
  if (lower.includes("mint")) return { id: "mint", label: "Linux Mint" };
  if (lower.includes("azure") || lower.includes("mariner")) return { id: "azure", label: "Azure Linux" };
  return null;
}

function rootfsDistroInfo(item) {
  const candidates = [
    rootfsDisplayName(item),
    item?.description,
    item?.path,
  ];
  for (const candidate of candidates) {
    const info = distroIconInfoForName(candidate);
    if (info) return info;
  }
  return { id: "disk", label: "Linux" };
}

function rootfsDistroIcon(item, className = "") {
  const info = rootfsDistroInfo(item);
  const classes = ["distro-icon", "rootfs-distro-icon", `distro-${info.id}`, className].filter(Boolean).join(" ");
  const label = uiText(`系统：${info.label}`, `System: ${info.label}`);
  return `<span class="${classes}" style="--distro-icon: url('/assets/distro/${info.id}.svg')" role="img" aria-label="${escapeHTML(label)}" title="${escapeHTML(label)}"></span>`;
}

function rootfsHasOfficialSupport(asset) {
  const source = String(firstValue(asset?.sourceRepoName, asset?.sourceRepo, asset?.repositoryName, asset?.repository) || "").toLowerCase();
  return isDroidspacesOfficialRootfsDownloadURL(rootfsAssetDownloadURL(asset)) || (source.includes("droidspaces") && source.includes("official"));
}

function rootfsUnsupportedBadge() {
  return `<span class="badge rootfs-support-note" title="${uiText("该模板不在 Droidspaces 官方模板列表中", "This template is not in the official Droidspaces template list")}">${uiText("非官方支持系统", "Unofficial system")}</span>`;
}

function rootfsSupportBadge(asset) {
  if (rootfsHasOfficialSupport(asset)) return "";
  return rootfsUnsupportedBadge();
}

function localRootfsSupportBadge(item) {
  const source = localRootfsSource(item).toLowerCase();
  return source.includes("droidspaces") && source.includes("official") ? "" : rootfsUnsupportedBadge();
}

function rootfsAssetDescription(asset) {
  if (!rootfsHasOfficialSupport(asset)) return "";
  return String(asset?.description || "").replace(/\s+/g, " ").trim();
}

function containerDistroInfo(container) {
  const candidates = [
    container?.distroName,
    container?.distribution,
    container?.osName,
    container?.imageRef,
    container?.rootfsPath,
    container?.name,
    container?.hostname,
  ];
  for (const candidate of candidates) {
    const info = distroIconInfoForName(candidate);
    if (info) return info;
  }
  return { id: "disk", label: "Linux" };
}

function containerDistroIcon(container, className = "") {
  const info = containerDistroInfo(container);
  const stateClass = container?.running ? "running" : "stopped";
  const classes = ["distro-icon", `distro-${info.id}`, stateClass, className].filter(Boolean).join(" ");
  const label = uiText(`系统：${info.label}`, `System: ${info.label}`);
  return `<span class="${classes}" style="--distro-icon: url('/assets/distro/${info.id}.svg')" role="img" aria-label="${escapeHTML(label)}" title="${escapeHTML(label)}"></span>`;
}

function portRuleText(port) {
  const host = port.hostPortEnd ? `${port.hostPort}-${port.hostPortEnd}` : port.hostPort;
  const cont = port.containerPortEnd ? `${port.containerPort}-${port.containerPortEnd}` : port.containerPort;
  return `${host}:${cont}/${port.protocol || "tcp"}`;
}

function portRules(ports) {
  return safeArray(ports).map(portRuleText);
}

function portText(ports) {
  const rules = portRules(ports);
  return rules.length ? rules.join(", ") : uiText("无", "None");
}

function portLines(ports) {
  return portRules(ports).join("\n");
}

function compactPortText(container) {
  if ((container.netMode || "").toLowerCase() === "host") return "";
  return portText(container.ports);
}

function normalizeListInput(value) {
  return String(value || "")
    .split(/[\n,]+/)
    .map((item) => item.trim())
    .filter(Boolean)
    .join(",");
}

function cleanStaticNATOctetInput(input) {
  const cleaned = String(input.value || "").replace(/\D/g, "").slice(0, 3);
  if (input.value !== cleaned) input.value = cleaned;
}

function staticNATOctetValid(value, min, max) {
  if (value === "") return true;
  if (!/^\d{1,3}$/.test(value)) return false;
  const number = Number(value);
  return Number.isInteger(number) && number >= min && number <= max;
}

function setCreateFieldError(selector, message) {
  const input = $(selector);
  if (!input) return;
  const errorID = "create-error-" + input.id;
  let error = document.getElementById(errorID);
  if (!error) {
    error = document.createElement("small");
    error.id = errorID;
    error.className = "field-error";
    error.dataset.createErrorFor = input.id;
    const owner = input.matches("#createLocalRootfs, #createCloudRootfs")
      ? $("#createTemplatePicker")
      : (input.closest("label") || input.parentElement);
    if (owner) owner.appendChild(error);
  }
  error.textContent = message || "";
  error.classList.toggle("hidden", !message);
  input.classList.toggle("invalid", Boolean(message));
  input.setAttribute("aria-invalid", message ? "true" : "false");
  if (input.dataset.originalDescribedby === undefined) input.dataset.originalDescribedby = input.getAttribute("aria-describedby") || "";
  input.setAttribute("aria-describedby", message ? errorID : input.dataset.originalDescribedby);
  input.setCustomValidity(message || "");
}

function createPortRange(value) {
  const text = String(value || "").trim();
  if (!/^\d+(?:-\d+)?$/.test(text)) return null;
  const parts = text.split("-").map(Number);
  if (parts.some((part) => !Number.isInteger(part) || part < 1 || part > 65535)) return null;
  const end = parts.length === 1 ? parts[0] : parts[1];
  if (end < parts[0]) return null;
  return { start: parts[0], end };
}

function parseCreatePortForwards(value) {
  const rawItems = String(value || "").split(/[\n,]+/).map((item) => item.trim()).filter(Boolean);
  if (rawItems.length > 32) return { error: uiText("端口转发最多只能设置 32 条规则", "Port forwarding can contain at most 32 rules") };
  const parsed = [];
  for (const raw of rawItems) {
    const slashParts = raw.split("/");
    if (slashParts.length > 2) return { error: uiText(`端口转发规则“${raw}”格式错误，应为 主机端口:容器端口/协议`, `Port forwarding rule “${raw}” is invalid; use host-port:container-port/protocol`) };
    const protocol = String(slashParts[1] || "tcp").trim().toLowerCase();
    if (!["tcp", "udp"].includes(protocol)) return { error: uiText(`端口转发规则“${raw}”的协议只能是 tcp 或 udp`, `The protocol in port forwarding rule “${raw}” must be tcp or udp`) };
    const sides = slashParts[0].split(":");
    if (sides.length > 2) return { error: uiText(`端口转发规则“${raw}”只能包含一个冒号`, `Port forwarding rule “${raw}” can contain only one colon`) };
    const host = createPortRange(sides[0]);
    const container = createPortRange(sides.length === 2 ? sides[1] : sides[0]);
    if (!host) return { error: uiText(`端口转发规则“${raw}”的主机端口必须是 1-65535 的端口或范围`, `The host port in rule “${raw}” must be a port or range from 1 to 65535`) };
    if (!container) return { error: uiText(`端口转发规则“${raw}”的容器端口必须是 1-65535 的端口或范围`, `The container port in rule “${raw}” must be a port or range from 1 to 65535`) };
    if (host.end - host.start !== container.end - container.start) return { error: uiText(`端口转发规则“${raw}”的主机和容器范围长度必须一致`, `Host and container port ranges in rule “${raw}” must have the same length`) };
    parsed.push({ host, container, protocol, raw });
  }
  for (let i = 0; i < parsed.length; i += 1) {
    for (let j = i + 1; j < parsed.length; j += 1) {
      if (parsed[i].protocol !== parsed[j].protocol) continue;
      const hostOverlap = parsed[i].host.start <= parsed[j].host.end && parsed[j].host.start <= parsed[i].host.end;
      const containerOverlap = parsed[i].container.start <= parsed[j].container.end && parsed[j].container.start <= parsed[i].container.end;
      if (hostOverlap) return { error: uiText(`端口转发规则“${parsed[i].raw}”与“${parsed[j].raw}”的主机端口重复`, `Host ports overlap between rules “${parsed[i].raw}” and “${parsed[j].raw}”`) };
      if (containerOverlap) return { error: uiText(`端口转发规则“${parsed[i].raw}”与“${parsed[j].raw}”的容器端口重复`, `Container ports overlap between rules “${parsed[i].raw}” and “${parsed[j].raw}”`) };
    }
  }
  return { ports: parsed };
}

function validateCreatePortForwards(value) {
  return parseCreatePortForwards(value).error || "";
}

function validateCreatePortForwardAvailability(value) {
  const parsed = parseCreatePortForwards(value);
  if (parsed.error) return "";
  for (const requested of parsed.ports) {
    for (const container of state.containers) {
      for (const existing of safeArray(container.ports)) {
        if (String(existing.protocol || "tcp").toLowerCase() !== requested.protocol) continue;
        const hostStart = Number(existing.hostPort || existing.host_port || 0);
        const hostEnd = Number(existing.hostPortEnd || existing.host_port_end || hostStart);
        if (!Number.isInteger(hostStart) || !Number.isInteger(hostEnd) || hostStart < 1 || hostEnd < hostStart) continue;
        const overlaps = requested.host.start <= hostEnd && hostStart <= requested.host.end;
        if (!overlaps) continue;
        const range = hostStart === hostEnd ? String(hostStart) : `${hostStart}-${hostEnd}`;
        return uiText(`主机 ${requested.protocol.toUpperCase()} 端口与容器“${container.name}”的 ${range} 冲突`, `Host ${requested.protocol.toUpperCase()} port conflicts with ${range} on container “${container.name}”`);
      }
    }
  }
  return "";
}

function validateCreateMemoryLimit(value) {
  const text = String(value || "").trim().toLowerCase();
  if (!text || text === "0" || text === "none" || text === "unlimited") return "";
  const match = text.match(/^([0-9]+(?:\.[0-9]+)?)([kmgt]?i?b?|bytes?)?$/i);
  if (!match || Number(match[1]) <= 0) return uiText("内存限制应填写正数，例如 512M 或 2G", "Memory limit must be a positive number, such as 512M or 2G");
  let unit = String(match[2] || "").toLowerCase();
  unit = unit.replace(/bytes?$/, "").replace(/b$/, "").replace(/i$/, "");
  const multipliers = { "": 1, k: 1024, m: 1024 ** 2, g: 1024 ** 3, t: 1024 ** 4 };
  const bytes = Number(match[1]) * (multipliers[unit] || 0);
  if (!Number.isFinite(bytes) || bytes < 4 * 1024 * 1024) return uiText("内存限制不能小于 4 MiB", "Memory limit cannot be less than 4 MiB");
  return "";
}

function validateCreateCPU(value) {
  const text = String(value || "").trim().toLowerCase();
  if (!text || text === "0" || text === "none" || text === "unlimited") return "";
  const number = Number(text);
  if (!Number.isFinite(number) || number <= 0) return uiText("CPU 限制应填写大于 0 的数字", "CPU limit must be a number greater than 0");
  if (number > 1024) return uiText("CPU 限制不能超过 1024", "CPU limit cannot exceed 1024");
  return "";
}

function validateCreatePids(value) {
  const text = String(value || "").trim().toLowerCase();
  if (!text || text === "0" || text === "none" || text === "unlimited") return "";
  if (!/^\d+$/.test(text) || Number(text) <= 0) return uiText("PIDs 限制应填写正整数", "PIDs limit must be a positive integer");
  if (Number(text) > 4194304) return uiText("PIDs 限制不能超过 4194304", "PIDs limit cannot exceed 4194304");
  return "";
}

function validateCreateSafeText(value, label, allowNewline = false) {
  const text = String(value || "");
  if (text.includes(String.fromCharCode(0))) return uiText(`${label}不能包含 NUL 字符`, `${label} cannot contain NUL characters`);
  if (text.includes("\r") || (!allowNewline && text.includes("\n"))) return uiText(`${label}不能包含换行`, `${label} cannot contain line breaks`);
  return "";
}

function validateCreateBinds(value) {
  const items = String(value || "").split(/[\n,]+/).map((item) => item.trim()).filter(Boolean);
  for (const item of items) {
    const parts = item.split(":").map((part) => part.trim());
    if (parts.length < 2 || parts.length > 3 || !parts[0] || !parts[1]) {
      return uiText(`绑定挂载“${item}”格式错误，应为 源路径:容器路径[:ro]`, `Bind mount “${item}” is invalid; use source-path:container-path[:ro]`);
    }
    if (!parts[1].startsWith("/")) return uiText(`绑定挂载“${item}”的容器路径必须是绝对路径`, `The container path in bind mount “${item}” must be absolute`);
    if (parts.length === 3 && parts[2].toLowerCase() !== "ro") {
      return uiText(`绑定挂载“${item}”的第三段只能填写 ro`, `The third segment in bind mount “${item}” can only be ro`);
    }
  }
  return "";
}

function validateCreateInit(value) {
  const text = String(value || "").trim();
  if (!text) return "";
  if (!text.startsWith("/")) return uiText("Init 路径必须是绝对路径（以 / 开头）", "Init path must be absolute (start with /)");
  if (text.includes(" ")) return uiText("Init 路径不能包含空格", "Init path cannot contain spaces");
  return "";
}

function defaultNATThirdOctet() {
  const value = Number(state.systemSettings.defaultNatThirdOctet || state.networkSettings.defaultNatThirdOctet || DEFAULT_NAT_THIRD_OCTET);
  return Number.isInteger(value) && value >= 1 && value <= 254 ? value : DEFAULT_NAT_THIRD_OCTET;
}

function natThirdOctetFor(prefix) {
  if (prefix === "config" && Number.isInteger(state.configNatThirdOctet) && state.configNatThirdOctet >= 1 && state.configNatThirdOctet <= 254) {
    return state.configNatThirdOctet;
  }
  return defaultNATThirdOctet();
}

function natPrefixText(prefix = "create") {
  return `${NAT_IP_PREFIX}.${natThirdOctetFor(prefix)}.`;
}

function natAddressPoolText() {
  return `${natPrefixText("create")}x`;
}

function updateNATPrefixLabels() {
  ["create", "config"].forEach((prefix) => {
    const label = $(`#${prefix}StaticNatPrefix`);
    if (label) label.textContent = natPrefixText(prefix);
  });
  if ($("#defaultNatCIDR")) $("#defaultNatCIDR").value = natAddressPoolText();
  if ($("#settingsDefaultNatCIDR")) $("#settingsDefaultNatCIDR").value = natAddressPoolText();
}

function staticNATIPValue(prefix) {
  const octet4 = ($(`#${prefix}StaticNatOctet4`)?.value || "").trim();
  if (!octet4) return "";
  return `${natPrefixText(prefix)}${octet4}`;
}

function updateStaticNATIP(prefix) {
  const octet4 = $(`#${prefix}StaticNatOctet4`);
  const hidden = $(`#${prefix}StaticNatIp`);
  const value4 = (octet4?.value || "").trim();
  const valid4 = staticNATOctetValid(value4, 1, 254);
  if (octet4) {
    octet4.classList.toggle("invalid", !valid4);
    octet4.setCustomValidity(valid4 ? "" : uiText("第 4 字节范围：1-254", "Fourth octet range: 1-254"));
  }
  if (hidden) hidden.value = staticNATIPValue(prefix);
}

function setStaticNATIP(prefix, value) {
  const octet4 = $(`#${prefix}StaticNatOctet4`);
  const parts = String(value || "").trim().split(".");
  const matchesPrefix = parts.length === 4 && parts[0] === "172" && parts[1] === "28";
  if (prefix === "config") {
    const third = matchesPrefix ? Number(parts[2]) : 0;
    state.configNatThirdOctet = Number.isInteger(third) && third >= 1 && third <= 254 ? third : 0;
  }
  if (octet4) octet4.value = matchesPrefix ? parts[3] : "";
  updateNATPrefixLabels();
  updateStaticNATIP(prefix);
}

function containerCpuText(container) {
  const quota = firstValue(container.cpuQuota, container.cpu_quota);
  const period = firstValue(container.cpuPeriod, container.cpu_period);
  const percent = firstValue(container.cpuUsage, container.cpuPercent, nestedValue(container, "resources.cpuUsage"), nestedValue(container, "resources.cpuPercent"));
  if (percent !== undefined) return fmtPercent(percent);
  if (quota) return `${quota}/${period || uiText("默认", "default")}`;
  return "-";
}

function usageBytes(usage, bytesKey, kibKey) {
  if (!usage || typeof usage !== "object") return null;
  const rawBytes = usage[bytesKey];
  const bytes = rawBytes === undefined || rawBytes === null || rawBytes === "" ? null : metricNumber(rawBytes);
  if (bytes !== null && bytes >= 0) return bytes;
  const rawKib = usage[kibKey];
  const kib = rawKib === undefined || rawKib === null || rawKib === "" ? null : metricNumber(rawKib);
  return kib !== null && kib >= 0 ? kib * 1024 : null;
}

function usagePercent(usage) {
  const rawPercent = usage?.percent;
  const percent = rawPercent === undefined || rawPercent === null || rawPercent === "" ? null : metricNumber(rawPercent);
  return percent !== null && percent >= 0 ? percent : null;
}

function fmtResourcePercent(percent) {
  return percent === null ? "" : `${Math.min(percent, 100).toFixed(1)}%`;
}

function containerUsageText(usage) {
  const used = usageBytes(usage, "usedBytes", "usedKb");
  if (used === null) return "";
  const total = usageBytes(usage, "totalBytes", "totalKb");
  const percent = usagePercent(usage);
  const totalText = total !== null && total > 0 ? ` / ${fmtBytesOptional(total)}` : "";
  const percentText = percent !== null ? ` · ${fmtResourcePercent(percent)}` : "";
  return `${fmtBytesOptional(used)}${totalText}${percentText}`;
}

function containerMemoryText(container) {
  const text = containerUsageText(container?.memoryUsage);
  if (text) return text;
  return container?.running ? uiText("待采样", "Awaiting sample") : "-";
}

function containerMemoryLabel(container) {
  switch (String(container?.memoryUsageSource || "").trim()) {
    case "core-rss":
      return uiText("进程内存", "Process Memory");
    case "cgroup-memory.current":
      return uiText("Cgroup 内存", "Cgroup Memory");
    default:
      return uiText("内存", "Memory");
  }
}

function cgroupMemoryComponentText(usage, key) {
  const bytes = metricNumber(usage?.[key]);
  return bytes !== null && bytes > 0 ? fmtBytesOptional(bytes) : "";
}

function containerCgroupMemoryRows(container) {
  if (String(container?.memoryUsageSource || "").trim() !== "core-rss") return [];
  const usage = container?.cgroupMemoryUsage;
  const total = containerUsageText(usage);
  if (!total) return [];
  return [
    [uiText("Cgroup 总占用（含缓存）", "Cgroup Total (including cache)"), total],
    [uiText("文件缓存", "File Cache"), cgroupMemoryComponentText(usage, "fileBytes")],
    [uiText("匿名内存", "Anonymous Memory"), cgroupMemoryComponentText(usage, "anonBytes")],
    [uiText("内核内存", "Kernel Memory"), cgroupMemoryComponentText(usage, "kernelBytes")],
  ];
}

function containerDiskText(container) {
  const text = containerUsageText(container?.diskUsage);
  return text || "-";
}

function formatContainerRuntime(totalSeconds) {
  const secondsValue = Math.max(0, Math.floor(Number(totalSeconds) || 0));
  const days = Math.floor(secondsValue / 86400);
  const hours = Math.floor((secondsValue % 86400) / 3600);
  const minutes = Math.floor((secondsValue % 3600) / 60);
  const seconds = Math.floor(secondsValue % 60);
  return [days ? `${days}d` : "", hours ? `${hours}h` : "", minutes ? `${minutes}m` : "", `${seconds}s`].filter(Boolean).join(" ");
}

function parseContainerRuntimeSeconds(value) {
  if (value === undefined || value === null || value === "") return null;
  const text = String(value).trim();
  if (/^\d+$/.test(text)) {
    const seconds = Number(text);
    return Number.isFinite(seconds) && seconds >= 0 ? seconds : null;
  }
  const matches = Array.from(text.matchAll(/(\d+)\s*([dhms])/gi));
  if (!matches.length) return null;
  const total = matches.reduce((seconds, [, raw, unit]) => {
    const multiplier = { d: 86400, h: 3600, m: 60, s: 1 }[unit.toLowerCase()] || 0;
    return seconds + Number(raw) * multiplier;
  }, 0);
  return Number.isFinite(total) && total >= 0 ? total : null;
}

function containerStartedAtSeconds(container) {
  const raw = container?.startedAt;
  if (raw === undefined || raw === null || raw === "") return null;
  const value = Number(raw);
  if (!Number.isFinite(value) || value <= 0) return null;
  const seconds = value > 1e11 ? value / 1000 : value;
  return seconds <= (Date.now() / 1000) + 60 ? seconds : null;
}

function containerRuntimeInfo(container) {
  const value = firstValue(container?.uptime, container?.uptimeSeconds, nestedValue(container, "usage.uptime"));
  const uptimeSeconds = parseContainerRuntimeSeconds(value);
  if (uptimeSeconds !== null) return { text: formatContainerRuntime(uptimeSeconds), seconds: uptimeSeconds };
  const startedAt = containerStartedAtSeconds(container);
  if (startedAt !== null) {
    const seconds = Math.max(0, Math.floor(Date.now() / 1000 - startedAt));
    return { text: formatContainerRuntime(seconds), seconds };
  }
  const text = String(value || "").trim();
  return { text: text || "--", seconds: null };
}

function containerUptimeText(container) {
  return containerRuntimeInfo(container).text;
}

function refreshContainerRuntimeBadges() {
  const now = Date.now();
  $$('[data-container-runtime-seconds]').forEach((badge) => {
    const seconds = Number(badge.dataset.containerRuntimeSeconds);
    const sampledAt = Number(badge.dataset.containerRuntimeSampledAt);
    if (!Number.isFinite(seconds) || !Number.isFinite(sampledAt)) return;
    badge.textContent = formatContainerRuntime(seconds + (now - sampledAt) / 1000);
  });
}

function togglePortField(selectSelector, fieldSelector) {
  const select = $(selectSelector);
  const field = $(fieldSelector);
  if (!select || !field) return;
  const mode = select.value;
  const show = mode === "nat";
  field.classList.toggle("hidden", !show);
  field.querySelectorAll("input, textarea, select").forEach((input) => { input.disabled = !show; });
}

function toggleFieldForMode(selectSelector, fieldSelector, expectedMode) {
  const select = $(selectSelector);
  const field = $(fieldSelector);
  if (!select || !field) return;
  const show = select.value === expectedMode;
  field.classList.toggle("hidden", !show);
  field.querySelectorAll("input, textarea, select").forEach((input) => { input.disabled = !show; });
}

function updateNetworkModeFields() {
  togglePortField("#createNetMode", "#createPortsField");
  togglePortField("#configNetMode", "#configPortsField");
  toggleFieldForMode("#createNetMode", "#createNatIpField", "nat");
  toggleFieldForMode("#configNetMode", "#configNatIpField", "nat");
  toggleFieldForMode("#createNetMode", "#createGatewayFields", "gateway");
  toggleFieldForMode("#configNetMode", "#configGatewayFields", "gateway");
  syncForcedDisableIPv6("#createNetMode", "#createDisableIpv6");
  syncForcedDisableIPv6("#configNetMode", "#configDisableIpv6");
}

function modeForcesDisableIPv6(mode) {
  const value = String(mode || "").trim().toLowerCase();
  return value === "nat" || value === "none";
}

function syncForcedDisableIPv6(netSelector, checkboxSelector) {
  const net = $(netSelector);
  const checkbox = $(checkboxSelector);
  if (!net || !checkbox) return;
  const forced = modeForcesDisableIPv6(net.value);
  const desc = checkbox.closest("label")?.querySelector(".option-desc");
  if (forced) {
    if (!checkbox.disabled) checkbox.dataset.userChecked = checkbox.checked ? "1" : "0";
    checkbox.checked = true;
    checkbox.disabled = true;
  } else {
    checkbox.disabled = false;
    if (checkbox.dataset.userChecked !== undefined) {
      checkbox.checked = checkbox.dataset.userChecked === "1";
      delete checkbox.dataset.userChecked;
    }
  }
  if (desc) {
    desc.textContent = forced
      ? uiText("在 NAT 和 None 网络模式下，IPv6 始终被禁用。", "IPv6 is always disabled in NAT and None network modes.")
      : uiText("在此容器中禁用 IPv6。这可能会导致宿主机的 VPN 应用异常。", "Disable IPv6 in this container. This can affect VPN applications on the host.");
  }
}

function updateCreateStorageUI() {
  const imageMode = Boolean($("#createUseSparseImage")?.checked);
  const field = $("#createImageSizeField");
  const input = $("#createImageSize");
  if (!field || !input) return;
  field.classList.toggle("hidden", !imageMode);
  input.disabled = !imageMode;
}

function updateGraphicsFlagFields(prefix) {
  const tx11 = Boolean($(`#${prefix}TermuxX11`)?.checked);
  const virgl = Boolean($(`#${prefix}Virgl`)?.checked);
  toggleExtraFlagField(prefix, "Tx11", tx11);
  toggleExtraFlagField(prefix, "Virgl", virgl);
}

function toggleExtraFlagField(prefix, key, enabled) {
  const field = $(`#${prefix}${key}FlagsField`);
  const input = $(`#${prefix}${key}ExtraFlags`);
  if (!field || !input) return;
  field.classList.toggle("hidden", !enabled);
  input.disabled = !enabled;
}

function taskLabel(kind) {
  const labels = {
    "rootfs-download": uiText("镜像下载", "Image download"),
    "core-update": uiText("核心更新", "Core update"),
    "container-export": uiText("容器备份", "Container backup"),
    "container-template": uiText("转换为模板", "Convert to template"),
    "container-start": uiText("启动容器", "Start container"),
    "container-stop": uiText("停止容器", "Stop container"),
    "container-restart": uiText("重启容器", "Restart container"),
    "container-delete": uiText("删除容器", "Delete container"),
  };
  return labels[kind] || kind || uiText("任务", "Task");
}

const CONTAINER_OPERATION_ACTIONS = new Set(["start", "stop", "restart", "delete"]);
const terminalTaskSettles = new Set();

function taskIsActive(task) {
  const status = String(task?.status || "pending").toLowerCase();
  return status === "pending" || status === "running";
}

function taskShouldRender(task) {
  return taskIsActive(task);
}

function containerOperationAction(kind) {
  const value = String(kind || "");
  const prefix = "container-";
  if (!value.startsWith(prefix)) return "";
  const action = value.slice(prefix.length);
  return CONTAINER_OPERATION_ACTIONS.has(action) ? action : "";
}

function containerOperationLabel(action) {
  return ({
    start: uiText("启动", "Start"),
    stop: uiText("停止", "Stop"),
    restart: uiText("重启", "Restart"),
    delete: uiText("删除", "Delete"),
  })[action] || action || uiText("操作", "Action");
}

function containerTaskForName(name) {
  const key = String(name || "").trim();
  if (!key) return null;
  const operation = state.containerTasks[key];
  if (!operation) return null;
  if (operation.taskId) {
    const task = state.tasks[operation.taskId];
    if (task && !taskIsActive(task)) {
      delete state.containerTasks[key];
      return null;
    }
  }
  return operation;
}

function setContainerTask(name, action, taskId = "") {
  const key = String(name || "").trim();
  if (!key || !CONTAINER_OPERATION_ACTIONS.has(action)) return;
  state.containerTasks[key] = { action, taskId: String(taskId || "") };
}

function clearContainerTask(name, taskId = "") {
  const key = String(name || "").trim();
  if (!key) return false;
  const current = state.containerTasks[key];
  if (!current) return false;
  if (taskId && current.taskId && current.taskId !== String(taskId)) return false;
  delete state.containerTasks[key];
  return true;
}

function syncContainerTasksFromTasks() {
  const next = {};
  for (const task of Object.values(state.tasks)) {
    if (!taskIsActive(task)) continue;
    const action = containerOperationAction(task.kind);
    const name = String(task.name || "").trim();
    if (!action || !name) continue;
    next[name] = { action, taskId: String(task.id || "") };
  }
  // Keep an optimistic entry while the submit request is still in flight.
  for (const [name, operation] of Object.entries(state.containerTasks)) {
    if (!operation.taskId && !next[name]) next[name] = operation;
  }
  const previousKeys = Object.keys(state.containerTasks);
  const nextKeys = Object.keys(next);
  const changed = previousKeys.length !== nextKeys.length || nextKeys.some((name) => {
    const before = state.containerTasks[name];
    const after = next[name];
    return !before || before.action !== after.action || before.taskId !== after.taskId;
  });
  state.containerTasks = next;
  return changed;
}

function refreshContainerOperationUI() {
  renderContainers();
  if (state.selected && state.selectedDetail) renderDetail(state.selectedDetail);
}

function sourceLabel(source) {
  const labels = { socketd: "socketd", cli: "CLI", workspace: uiText("工作区", "Workspace") };
  return labels[source] || source || uiText("未知", "Unknown");
}

function recordBackendError(source, message, hint = "") {
  const text = String(message || "").trim();
  if (!text || text === "socketd disabled") return;
  const entry = { time: Math.floor(Date.now() / 1000), source: source || "backend", message: text, hint: String(hint || "").trim() };
  const previous = state.backendErrorLog[0];
  if (previous && previous.source === entry.source && previous.message === entry.message) {
    previous.time = entry.time;
    previous.hint = entry.hint || previous.hint || "";
    renderBackendDiagnostics();
    return false;
  }
  const existing = state.backendErrorLog.find((item) => item.source === entry.source && item.message === entry.message);
  if (existing) {
    existing.time = entry.time;
    existing.hint = entry.hint || existing.hint || "";
    state.backendErrorLog = [existing, ...state.backendErrorLog.filter((item) => item !== existing)];
    renderBackendDiagnostics();
    return false;
  } else {
    state.backendErrorLog.unshift(entry);
  }
  state.backendErrorLog = state.backendErrorLog.slice(0, 40);
  renderBackendDiagnostics();
  return true;
}

function mergeBackendErrors(entries) {
  let added = false;
  for (const item of entries || []) {
    const isNew = recordBackendError(item.source, item.message, item.hint);
    added = added || Boolean(isNew);
  }
  return added;
}

function backendText(value) {
  const labels = {
    ready: uiText("就绪", "Ready"),
    unreachable: uiText("不可达", "Unreachable"),
    "cli-fallback": uiText("CLI 兜底", "CLI fallback"),
    "workspace-fallback": uiText("工作区兜底", "Workspace fallback"),
    "socketd-disabled": uiText("socketd 已禁用", "socketd disabled"),
  };
  return labels[value] || value || uiText("未知", "Unknown");
}

function backendHasUsableFallback(value) {
  return value === "cli-fallback" || value === "workspace-fallback" || value === "socketd-disabled";
}

async function fetchContainerUsers(name) {
  const data = await api(`/api/containers/${encodeURIComponent(name)}/users`);
  state.containerUsers[name] = data;
  return data;
}

async function fetchContainerServices(name) {
  const data = await api(`/api/containers/${encodeURIComponent(name)}/services`);
  state.containerServices[name] = data;
  return data;
}

async function postServiceAction(name, manager, service, action) {
  return api(`/api/containers/${encodeURIComponent(name)}/services/${encodeURIComponent(manager)}/${encodeURIComponent(service)}/${encodeURIComponent(action)}`, { method: "POST" });
}

async function fetchBootPriority() {
  const data = await api("/api/boot-priority");
  state.bootPriorityContainers = safeArray(data.containers);
  return data;
}

async function saveBootPriority(names) {
  const data = await api("/api/boot-priority", { method: "PUT", body: JSON.stringify({ names }) });
  state.bootPriorityContainers = safeArray(data.containers);
  return data;
}

async function fetchSystemdUnitInspection(name, unit) {
  return api(`/api/containers/${encodeURIComponent(name)}/services/systemd/${encodeURIComponent(unit)}/inspect`);
}

async function fetchSystemdOverride(name, unit) {
  return api(`/api/containers/${encodeURIComponent(name)}/services/systemd/${encodeURIComponent(unit)}/override`);
}

async function saveSystemdOverride(name, unit, content) {
  return api(`/api/containers/${encodeURIComponent(name)}/services/systemd/${encodeURIComponent(unit)}/override`, { method: "PUT", body: JSON.stringify({ content }) });
}

async function deleteSystemdOverride(name, unit) {
  return api(`/api/containers/${encodeURIComponent(name)}/services/systemd/${encodeURIComponent(unit)}/override`, { method: "DELETE" });
}

async function migrateSparseImage(name) {
  return api(`/api/containers/${encodeURIComponent(name)}/sparse/migrate`, { method: "POST", body: JSON.stringify({ restoreAfter: true }) });
}

async function resizeSparseImage(name, sizeGB) {
  return api(`/api/containers/${encodeURIComponent(name)}/sparse/resize`, { method: "POST", body: JSON.stringify({ sizeGB: Number(sizeGB), restoreAfter: true }) });
}

async function fetchDiagnosticsRequirements() {
  const data = await api("/api/diagnostics/requirements");
  state.diagnostics.requirements = data;
  return data;
}

async function runDiagnosticsRequirements() {
  const data = await api("/api/diagnostics/requirements", { method: "POST", body: JSON.stringify({}) });
  state.diagnostics.requirementsTask = data;
  return data;
}

async function createDiagnosticsBugreport() {
  const data = await api("/api/diagnostics/bugreport", { method: "POST", body: JSON.stringify({ includeLogs: true }) });
  state.diagnostics.bugreport = data;
  return data;
}

async function fetchDiagnosticsSettings() {
  const data = await api("/api/diagnostics/settings");
  state.diagnostics.settings = data;
  return data;
}

async function saveDiagnosticsSettings(settings) {
  const data = await api("/api/diagnostics/settings", { method: "PUT", body: JSON.stringify(settings || {}) });
  state.diagnostics.settings = data;
  return data;
}

async function refreshAll() {
  if (window.DS_AUTH_REQUIRED && !state.authenticated) return;
  setBusy(true);
  try {
    await Promise.allSettled([loadStatus(true), loadContainers(), loadLocalRootfs(), loadTasks(), loadHost(), loadEvents(), loadNetworkSettings()]);
    if (state.selected && state.currentView === "detail") await inspect(state.selected, false, false);
    renderOverview();
  } catch (err) {
    toast(err.message);
  } finally {
    setBusy(false);
  }
}

function overviewRefreshSeconds() {
  const configured = Number(state.systemSettings?.overviewRefreshSeconds);
  if (Number.isFinite(configured) && configured >= 1 && configured <= 60) return configured;
  return DEFAULT_OVERVIEW_REFRESH_SECONDS;
}

function shouldHighRefreshOverview() {
  return state.authenticated && (state.currentView === "overview" || state.currentView === "battery") && !document.hidden;
}

async function refreshOverviewMetrics() {
  if (!shouldHighRefreshOverview() || state.busy || state.overviewRefreshInFlight) return;
  state.overviewRefreshInFlight = true;
  try {
    await Promise.allSettled([loadStatus(), loadHost(), loadTasks(), loadEvents()]);
    renderOverview();
  } catch (err) {
    toast(err.message);
  } finally {
    state.overviewRefreshInFlight = false;
  }
}

async function refreshBackendDiagnostics() {
  setBusy(true);
  try {
    await Promise.allSettled([loadStatus(), loadBackendDiagnostics(), loadContainers(), loadEvents()]);
    renderBackendDiagnostics();
  } finally {
    setBusy(false);
  }
}

function restartOverviewRefreshTimer() {
  if (state.overviewRefreshTimer) clearInterval(state.overviewRefreshTimer);
  state.overviewRefreshTimer = setInterval(refreshOverviewMetrics, overviewRefreshSeconds() * 1000);
}

async function loadStatus(refreshCoreVersion = false) {
	const data = await api(`/api/status${refreshCoreVersion ? "?refreshCoreVersion=1" : ""}`);
	state.status = data;
	updateAndroidOnlyControls();
	renderRuntimeVersions();
  renderCoreUpdate();
  mergeBackendErrors(data.backendErrors);
  $("#backendState").textContent = backendText(data.backend);
  const sidebarState = $("#sidebarBackendState");
  if (sidebarState) sidebarState.textContent = backendText(data.backend);
  const info = data.info || {};
  $("#totalContainers").textContent = info.containersTotal ?? state.containers.length ?? 0;
  $("#runningContainers").textContent = info.containersRunning ?? state.containers.filter((c) => c.running).length;
  $("#stoppedContainers").textContent = info.containersStopped ?? state.containers.filter((c) => !c.running).length;
  renderBackendSummary();
  renderBackendDiagnostics();
  renderSecurity();
  if (data.backendError) {
    const isNew = recordBackendError("status", data.backendError, data.backendErrorHint);
    if (isNew && !backendHasUsableFallback(data.backend)) toast(uiText(`后端提示：${data.backendError}`, `Backend notice: ${data.backendError}`));
  }
  if (data.fallbackError) recordBackendError("fallback", data.fallbackError);
}

function supportsAndroidStorage() {
  return state.status?.isAndroid === true;
}

function updateAndroidOnlyControls() {
  const enabled = supportsAndroidStorage();
  $$('[data-android-only]').forEach((field) => {
    field.hidden = !enabled;
    field.querySelectorAll('input, select, textarea, button').forEach((control) => {
      control.disabled = !enabled;
      if (!enabled && control.type === 'checkbox') control.checked = false;
    });
  });
}

async function loadBackendDiagnostics() {
  const data = await api("/api/diagnostics/backend");
  state.diagnostics.backend = data;
  mergeBackendErrors(data.errors);
  renderBackendDiagnostics();
  return data;
}

function normalizedWebUILogTail(value = state.webuiLogTail) {
  const tail = Number(value);
  return [100, 250, 500, 1000].includes(tail) ? tail : 250;
}

function isWebUILogPollingAllowed() {
  return state.authenticated && state.currentView === "diagnostics" && !document.hidden;
}

function renderWebUILog() {
  const output = $("#webuiLogOutput");
  const meta = $("#webuiLogMeta");
  if (!output || !meta) return;
  const log = state.webuiLog;
  const lines = safeArray(log.lines).map((line) => String(line));
  if (!log.loaded) output.textContent = uiText("等待读取 WebUI 服务日志", "Waiting to read the WebUI service log");
  else if (log.error && !lines.length) output.textContent = uiText(`读取日志失败：${log.error}`, `Failed to read log: ${log.error}`);
  else if (!log.exists) output.textContent = uiText("WebUI 服务日志文件尚不存在。服务写入日志后会自动显示。", "The WebUI service log does not exist yet. It will appear when the service writes logs.");
  else output.textContent = lines.length ? lines.join("\n") : uiText("日志文件为空", "Log file is empty");

  const details = [];
  if (log.path) details.push(log.path);
  if (!log.loaded) details.push(uiText("等待读取", "Waiting to read"));
  else if (log.error) details.push(uiText(`读取失败：${log.error}`, `Read failed: ${log.error}`));
  else if (!log.exists) details.push(uiText("日志文件不存在", "Log file does not exist"));
  else {
    details.push(uiText(`${lines.length} 行`, `${lines.length} lines`));
    if (log.truncated) details.push(uiText(`仅显示末尾 ${normalizedWebUILogTail()} 行`, `Showing the last ${normalizedWebUILogTail()} lines`));
    if (log.updatedAt) details.push(uiText(`更新于 ${fmtTime(log.updatedAt)}`, `Updated ${fmtTime(log.updatedAt)}`));
  }
  details.push(state.webuiLogAutoFollow ? uiText("自动跟随", "Auto-follow") : uiText("自动刷新已暂停", "Auto-refresh paused"));
  meta.textContent = details.join(" · ");
  if (state.webuiLogAutoFollow) output.scrollTop = output.scrollHeight;
}

async function loadWebUILog() {
  if (!isWebUILogPollingAllowed() || state.webuiLogLoading) return;
  state.webuiLogLoading = true;
  const refreshButton = $("#webuiLogRefreshBtn");
  if (refreshButton) refreshButton.disabled = true;
  try {
    const tail = normalizedWebUILogTail();
    const data = await api(`/api/diagnostics/webui-log?tail=${encodeURIComponent(tail)}`);
    state.webuiLog = {
      path: String(data.path || ""),
      exists: Boolean(data.exists),
      truncated: Boolean(data.truncated),
      lines: Array.isArray(data.lines) ? data.lines.map((line) => String(line)) : (typeof data.content === "string" && data.content !== "" ? data.content.split(/\r?\n/) : []),
      error: "",
      updatedAt: metricNumber(data.modifiedAt) || Math.floor(Date.now() / 1000),
      loaded: true,
    };
  } catch (err) {
    state.webuiLog = { ...state.webuiLog, error: err.message || uiText("未知错误", "Unknown error"), updatedAt: Math.floor(Date.now() / 1000), loaded: true };
  } finally {
    state.webuiLogLoading = false;
    if (refreshButton) refreshButton.disabled = state.busy;
    renderWebUILog();
  }
}

function updateWebUILogPolling() {
  const shouldPoll = state.webuiLogAutoFollow && isWebUILogPollingAllowed();
  if (!shouldPoll) {
    if (state.webuiLogRefreshTimer) clearInterval(state.webuiLogRefreshTimer);
    state.webuiLogRefreshTimer = 0;
    return;
  }
  if (!state.webuiLogRefreshTimer) {
    state.webuiLogRefreshTimer = setInterval(() => { loadWebUILog(); }, WEBUI_LOG_REFRESH_MS);
  }
}

async function loadHost() {
  try {
    state.host = await api("/api/host");
  } catch (err) {
    state.host = { error: err.message };
  }
  renderHost();
}

async function loadContainers() {
  const data = await api("/api/containers?all=1");
  state.containers = data.containers || [];
  if ((data.source === "workspace" || data.source === "cli") && data.backendError) {
    recordBackendError(`containers/${data.source}`, data.backendError, data.backendErrorHint);
  }
  renderContainers();
  renderTerminalTargets();
  renderNetwork();
  renderSecurity();
}

async function loadTasks() {
  try {
    const data = await api("/api/tasks");
    state.taskSummary = normalizeTaskSummary(data.summary, data.tasks || []);
    for (const task of data.tasks || []) {
      const taskId = String(task?.id || "").trim();
      if (!taskId) continue;
      const previous = state.tasks[taskId] || {};
      if (!taskIsActive(task)) {
        if (terminalTaskSettles.has(taskId)) continue;
        await settleTerminalTask(taskId, task, previous);
        continue;
      }
      state.tasks[taskId] = { ...previous, ...task, onDone: previous.onDone };
    }
    for (const [taskId, task] of Object.entries(state.tasks)) {
      if (!taskIsActive(task)) {
        delete state.tasks[taskId];
      }
    }
  } catch (err) {
    if (!/HTTP 404|not found/i.test(err.message)) toast(err.message);
  }
  if (syncContainerTasksFromTasks()) refreshContainerOperationUI();
  renderTasks();
}

async function loadEvents() {
  const data = await api(`/api/events?since=${state.lastEventSince}`);
  const events = data.events || [];
  if (events.length > 0) {
    state.lastEventSince = Math.max(...events.map((event) => event.time || 0));
    state.events = [...state.events, ...events].slice(-120);
  }
  if (data.backendError && data.backendError !== "socketd disabled") {
    recordBackendError("events", data.backendError);
  }
  renderEvents();
}

function renderOverview() {
  const active = Object.values(state.tasks).filter(taskIsActive).length;
  $("#activeTaskCount").textContent = active;
  renderDeviceMetrics();
  renderOverviewContainers();
  renderBackendSummary();
  renderHost();
  renderTasks();
  renderEvents();
}

function renderDeviceMetrics() {
  const host = state.host || {};
  const status = state.status || {};
  const device = firstValue(host.device, status.device, {});
  const runtime = firstValue(host.runtime, status.runtime, {});
  const resources = firstValue(host.resources, status.resources, host.metrics, status.metrics, {});
  const memory = firstValue(resources.memory, host.memory, status.memory, {});
  const network = firstValue(resources.network, host.network, status.network, {});
  const battery = firstValue(resources.battery, host.battery, status.battery, {});
  const systemVersion = firstValue(
    host.systemVersion,
    host.osVersion,
    host.androidVersion,
    status.systemVersion,
    status.osVersion,
    nestedValue(device, "systemVersion"),
    nestedValue(device, "osVersion"),
    nestedValue(device, "androidVersion"),
    host.goos && host.goarch ? `${host.goos}/${host.goarch}` : ""
  );
  const kernelVersion = firstValue(host.kernelVersion, status.kernelVersion, nestedValue(device, "kernelVersion"), nestedValue(runtime, "kernelVersion"));
  const uptimeSeconds = firstValue(host.uptimeSeconds, host.uptime, nestedValue(runtime, "uptimeSeconds"), nestedValue(runtime, "uptime"));
  const cpuUsage = firstValue(resources.cpuUsage, resources.cpuPercent, host.cpuUsage, host.cpuPercent, status.cpuUsage, status.cpuPercent, nestedValue(host, "cpu.usage"), nestedValue(status, "cpu.usage"));
  const memUsed = firstValue(memory.used, memory.usage, memory.usedBytes, host.memoryUsed, status.memoryUsed);
  const memTotal = firstValue(memory.total, memory.totalBytes, host.memoryTotal, status.memoryTotal);
  const memPercent = firstValue(memory.percent, memory.usagePercent, host.memoryPercent, status.memoryPercent);
  const rx = firstValue(network.rxBytes, network.receiveBytes, network.bytesReceived, host.networkRxBytes, status.networkRxBytes);
  const tx = firstValue(network.txBytes, network.transmitBytes, network.bytesSent, host.networkTxBytes, status.networkTxBytes);
  const netText = firstValue(network.io, network.summary, host.networkIO, status.networkIO, rx !== undefined || tx !== undefined ? `↓ ${fmtBytesOptional(rx)} / ↑ ${fmtBytesOptional(tx)}` : "");
  const cpuPercent = usagePercentValue(cpuUsage);
  const usedMemoryBytes = metricNumber(memUsed);
  const totalMemoryBytes = metricNumber(memTotal);
  const memoryPercent = usedMemoryBytes !== null && totalMemoryBytes !== null && totalMemoryBytes > 0
    ? Math.min((usedMemoryBytes / totalMemoryBytes) * 100, 100)
    : usagePercentValue(memPercent);
  const cpuCores = firstValue(host.numCPU, status.numCPU, nestedValue(device, "numCPU"), nestedValue(runtime, "numCPU"));
  const cpuDetail = cpuCores !== undefined
    ? uiText(`${cpuCores} 核 · 主机整体使用率`, `${cpuCores} cores · overall host usage`)
    : uiText("主机整体使用率", "Overall host usage");
  const memoryDetail = usedMemoryBytes !== null || totalMemoryBytes !== null
    ? `${fmtBytesOptional(usedMemoryBytes)} / ${fmtBytesOptional(totalMemoryBytes)}`
    : memoryPercent !== null ? uiText("总容量未上报", "Total capacity unavailable") : uiText("已用 / 总量待上报", "Usage / total awaiting data");
  const batteryStats = firstValue(nestedValue(battery, "stats"), nestedValue(host, "battery.stats"), nestedValue(status, "battery.stats"), nestedValue(resources, "battery.stats"));
  const batteryInfo = batteryText({
    ...battery,
    stats: batteryStats,
    directPowerSupported: Boolean(status.batteryDirectPowerSupported || host.batteryDirectPowerSupported || state.systemSettings.batteryDirectPowerSupported),
  });
  const values = {
    systemVersion: systemVersion || uiText("未知", "Unknown"),
    kernelVersion: kernelVersion || uiText("未知", "Unknown"),
    deviceUptime: formatDeviceUptime(uptimeSeconds),
    cpuUsage: usagePercentText(cpuUsage),
    cpuUsageDetail: cpuDetail,
    memoryUsage: usagePercentText(memoryPercent),
    memoryUsageDetail: memoryDetail,
    networkIO: netText || uiText("待上报", "Awaiting data"),
    overviewPowerLabel: batteryInfo.model.homeMetricLabel || uiText("当前功耗", "Current Power"),
    batteryMain: batteryInfo.main,
    batteryDetail: batteryInfo.detail,
  };
  Object.entries(values).forEach(([id, value]) => {
    const node = $(`#${id}`);
    if (node) node.textContent = value;
  });
  const batteryDetailNode = $("#batteryDetail");
  if (batteryDetailNode) batteryDetailNode.hidden = !batteryInfo.detail;
  setUsageMeter("cpuUsageMeter", cpuPercent, uiText("CPU 使用率", "CPU usage"));
  setUsageMeter("memoryUsageMeter", memoryPercent, uiText("内存使用率", "Memory usage"));
  renderBatteryLiveOverview(batteryInfo.model);
}

function formatDeviceUptime(value) {
  const rawSeconds = metricNumber(value);
  if (rawSeconds === null || rawSeconds < 0) return uiText("待上报", "Awaiting data");
  const totalSeconds = Math.floor(rawSeconds);
  const days = Math.floor(totalSeconds / 86400);
  const hours = Math.floor((totalSeconds % 86400) / 3600);
  const minutes = Math.floor((totalSeconds % 3600) / 60);
  const seconds = totalSeconds % 60;
  return uiText(
    [days ? `${days} 天` : "", hours ? `${hours} 小时` : "", minutes ? `${minutes} 分钟` : "", `${seconds} 秒`].filter(Boolean).join(" "),
    [days ? `${days}d` : "", hours ? `${hours}h` : "", minutes ? `${minutes}m` : "", `${seconds}s`].filter(Boolean).join(" ")
  );
}

function renderBackendSummary() {
  const node = $("#backendSummary");
  if (!node) return;
  const data = state.status || {};
  const rows = [
    [uiText("状态", "Status"), backendText(data.backend)],
    [uiText("当前核心", "Current Core"), data.coreVersion || uiText("未知", "Unknown")],
    [uiText("适配核心", "Supported Core"), data.supportedCoreVersion || "-"],
    [uiText("数据源", "Data Source"), sourceLabel(data.fallbackSource || data.backend)],
    [uiText("监听模式", "Listen Mode"), data.mode || "-"],
    [uiText("授权", "Authorization"), data.authEnabled ? uiText("已启用", "Enabled") : uiText("未启用", "Disabled")],
    ["socketd", data.socketdEnabled ? uiText("启用", "Enabled") : uiText("禁用", "Disabled")],
    [uiText("镜像仓库", "Image Repositories"), uiText(`${data.rootfsRepoCount ?? 0} 个`, `${data.rootfsRepoCount ?? 0}`)],
  ];
  node.innerHTML = rows.map(([k, v]) => `<div class="summary-row"><span>${k}</span><strong>${escapeHTML(v)}</strong></div>`).join("");
}

function renderBackendDiagnostics() {
  const node = $("#backendDiagnostics");
  const log = $("#backendErrorLog");
  const data = state.status || {};
  if (node) {
    const rows = [
      [uiText("状态", "Status"), backendText(data.backend)],
      [uiText("当前核心", "Current Core"), data.coreVersion || uiText("未知", "Unknown")],
      [uiText("适配核心", "Supported Core"), data.supportedCoreVersion || "-"],
      ["socketd", data.socketdEnabled ? uiText("启用", "Enabled") : uiText("禁用", "Disabled")],
      [uiText("兜底来源", "Fallback Source"), data.fallbackSource ? sourceLabel(data.fallbackSource) : "-"],
      [uiText("工作区", "Workspace"), data.workspace || "-"],
      ["Droidspaces", data.droidspacesPath || "-"],
      [uiText("最近错误", "Latest Error"), data.backendError || data.fallbackError || state.backendErrorLog[0]?.message || "-"],
      [uiText("处理建议", "Suggested Action"), data.backendErrorHint || state.backendErrorLog[0]?.hint || "-"],
    ];
    node.innerHTML = rows.map(([k, v]) => `<div class="summary-row"><span>${k}</span><strong>${escapeHTML(v)}</strong></div>`).join("");
  }
  if (log) {
    if (!state.backendErrorLog.length) {
      log.textContent = uiText("暂无后端错误", "No backend errors");
    } else {
      log.textContent = state.backendErrorLog
        .map((item) => `[${fmtTime(item.time)}] ${item.source}: ${item.message}${item.hint ? uiText(`\n  建议: ${item.hint}`, `\n  Suggested action: ${item.hint}`) : ""}`)
        .join("\n");
    }
  }
}

function renderHost() {
  renderDeviceMetrics();
  const summary = $("#hostSummary");
  const pathList = $("#hostPathList");
  const host = state.host || {};
  const cpuCores = metricNumber(host.numCPU);
  const cpuCoresText = cpuCores !== null && cpuCores > 0
    ? uiText(`${fmtCompactNumber(cpuCores, 0)} 核`, `${fmtCompactNumber(cpuCores, 0)} cores`)
    : uiText("未上报", "Awaiting data");
  if (summary) {
    if (host.error) {
      summary.innerHTML = `<div class="empty-state">${escapeHTML(host.error)}</div>`;
    } else {
      summary.innerHTML = [
        [uiText("当前核心", "Current Core"), state.status?.coreVersion || uiText("未知", "Unknown")],
        [uiText("适配核心", "Supported Core"), state.status?.supportedCoreVersion || "-"],
        [uiText("系统", "System"), `${host.goos || "-"}/${host.goarch || "-"}`],
        ["Go", host.goVersion || "-"],
        [uiText("CPU 核心", "CPU Cores"), cpuCoresText],
        [uiText("更新时间", "Updated"), fmtTime(host.time)],
      ].map(([k, v]) => `<div class="summary-row"><span>${k}</span><strong>${escapeHTML(v)}</strong></div>`).join("");
    }
  }
  if (!pathList) return;
  const paths = safeArray(host.paths);
  if (!paths.length) {
    pathList.innerHTML = `<div class="empty-state">${uiText("暂无主机路径信息", "No host path information")}</div>`;
    return;
  }
  pathList.innerHTML = paths.map((item) => {
    const free = item.diskAvailable
      ? uiText(`${fmtDisk(item.diskAvailable)} 可用 / ${fmtDisk(item.diskTotal)} 总计`, `${fmtDisk(item.diskAvailable)} available / ${fmtDisk(item.diskTotal)} total`)
      : uiText("容量未知", "Capacity unknown");
    return `<div class="path-item"><div><strong>${escapeHTML(item.key)}</strong><span class="mono">${escapeHTML(item.path || "-")}</span></div><div><span class="badge ${item.exists ? "running" : "stopped"}">${item.exists ? uiText("存在", "Available") : uiText("不可用", "Unavailable")}</span><span class="muted">${escapeHTML(free)}</span>${item.error ? `<span class="task-error">${escapeHTML(item.error)}</span>` : ""}</div></div>`;
  }).join("");
}

function renderContainers() {
  const wrap = $("#containerRows");
  if (!wrap) return;
  renderOverviewContainers();
  const filter = $("#filterInput")?.value.trim().toLowerCase() || "";
  const rows = filteredContainers(filter);
  const hint = $("#containerListHint");
  if (hint) {
    const labels = { all: uiText("全部容器", "All containers"), running: uiText("运行中", "Running"), stopped: uiText("未启动", "Not started") };
    hint.textContent = uiText(`${labels[state.containerFilter] || labels.all} · ${rows.length} 个`, `${labels[state.containerFilter] || labels.all} · ${rows.length}`);
  }
  if (rows.length === 0) {
    wrap.innerHTML = `<div class="empty-state">${uiText("无匹配容器", "No matching containers")}</div>`;
    return;
  }
  wrap.innerHTML = rows.map((container) => {
    const encoded = encodeURIComponent(container.name);
    const operation = containerTaskForName(container.name);
    const operationDisabled = operation ? " disabled" : "";
    const operationTitle = operation ? ` title="${escapeHTML(uiText(`正在${containerOperationLabel(operation.action)}容器`, `${containerOperationLabel(operation.action)} container in progress`))}"` : "";
    const action = container.running
      ? `<button class="mini-action danger" data-action="stop" data-name="${encoded}"${operationDisabled}${operationTitle}>${uiText("停止", "Stop")}</button>`
      : `<button class="mini-action primary" data-action="start" data-name="${encoded}"${operationDisabled}${operationTitle}>${uiText("启动", "Start")}</button>`;
    const meta = [
      ["PID", container.pid || "-"],
      [uiText("网络", "Network"), container.netMode || "-"],
    ];
    if (container.netMode === "nat") {
      meta.push(["NAT/IP", container.natIp || "-"]);
      if (compactPortText(container)) meta.push([uiText("端口", "Ports"), compactPortText(container)]);
    }
    meta.push(["CPU", containerCpuText(container)], [containerMemoryLabel(container), containerMemoryText(container)]);
    if (container.diskUsage || container.useSparseImage) meta.push([uiText("磁盘占用", "Disk Usage"), containerDiskText(container)]);
    return `<article class="container-card" data-name="${encoded}">
      <div class="container-card-head">
        <div class="container-title-block"><div class="container-title-line">${containerDistroIcon(container, "container-distro-icon")}<strong>${escapeHTML(container.name)}</strong>${statusBadge(container)}</div><span>${escapeHTML(container.distroName || container.hostname || container.uuid || "-")}</span></div>
        <button class="mini-action" data-action="inspect" data-name="${encoded}">${uiText("详情", "Details")}</button>
      </div>
      <div class="container-info-grid">
        ${meta.map(([label, value, html]) => `<div class="container-info"><span>${escapeHTML(label)}</span><strong class="mono">${html || escapeHTML(value)}</strong></div>`).join("")}
      </div>
      <div class="container-card-actions">${action}<button class="mini-action" data-action="restart" data-name="${encoded}"${operationDisabled}${operationTitle}>${uiText("重启", "Restart")}</button><button class="mini-action" data-action="config" data-name="${encoded}">${uiText("编辑", "Edit")}</button><button class="mini-action" data-action="terminal" data-name="${encoded}">${uiText("终端", "Terminal")}</button><button class="mini-action" data-action="export" data-name="${encoded}">${uiText("备份", "Back up")}</button><button class="mini-action" data-action="template" data-name="${encoded}">${uiText("转换为模板", "Convert to template")}</button><button class="mini-action danger" data-action="delete" data-name="${encoded}"${operationDisabled}${operationTitle}>${uiText("删除", "Delete")}</button></div>
    </article>`;
  }).join("");
}

function containerOverviewNetworkText(container) {
  const mode = String(container?.netMode || "").toLowerCase();
  if (mode === "nat") return `NAT${container.natIp ? ` · ${container.natIp}` : uiText(" · 自动分配", " · automatic")}`;
  if (mode === "host") return uiText("主机网络", "Host network");
  if (mode === "gateway") return uiText("网关网络", "Gateway network");
  if (mode === "none") return uiText("无网络", "No network");
  return mode ? uiText(`${mode} 网络`, `${mode} network`) : uiText("网络未上报", "Network unavailable");
}

function containerOverviewResourceText(container) {
  if (!container?.running) return uiText(`磁盘 ${containerDiskText(container)}`, `Disk ${containerDiskText(container)}`);
  return `CPU ${containerCpuText(container)} · ${containerMemoryLabel(container)} ${containerMemoryText(container)}`;
}

function renderOverviewContainers() {
  const node = $("#overviewContainerList");
  if (!node) return;
  const containers = safeArray(state.containers)
    .slice()
    .sort((a, b) => Number(Boolean(b.running)) - Number(Boolean(a.running)) || String(a.name || "").localeCompare(String(b.name || ""), uiLocale()));
  if (!containers.length) {
    node.innerHTML = `<div class="empty-state">${uiText("暂无容器", "No containers")}</div>`;
    return;
  }
  const visible = containers.slice(0, 4);
  node.innerHTML = visible.map((container) => {
    const running = Boolean(container.running);
    const runtime = containerRuntimeInfo(container);
    const runtimeAttrs = running && runtime.seconds !== null
      ? ` data-container-runtime-seconds="${runtime.seconds}" data-container-runtime-sampled-at="${Date.now()}"`
      : "";
    const encodedName = encodeURIComponent(container.name || "");
    const stateText = running ? uiText("运行中", "Running") : uiText("已停止", "Stopped");
    const timeText = running ? runtime.text : uiText("未启动", "Not started");
    return `<button class="overview-container-row ${running ? "running" : "stopped"}" type="button" data-action="inspect" data-name="${encodedName}">
      ${containerDistroIcon(container, "overview-distro-icon")}
      <span class="overview-container-name"><strong>${escapeHTML(container.name || "-")}</strong><small>${escapeHTML(containerOverviewNetworkText(container))}</small></span>
      <span class="overview-container-runtime"><strong>${stateText}</strong><small${runtimeAttrs}>${escapeHTML(timeText)}</small></span>
      <span class="overview-container-resource">${escapeHTML(containerOverviewResourceText(container))}</span>
    </button>`;
  }).join("");
}

function filteredContainers(filter) {
  return state.containers.filter((container) => {
    if (state.containerFilter === "running" && !container.running) return false;
    if (state.containerFilter === "stopped" && container.running) return false;
    const haystack = [container.name, container.netMode, container.hostname, container.natIp, portText(container.ports)].join(" ").toLowerCase();
    return haystack.includes(filter);
  });
}

async function inspect(name, showToast = true, navigate = true) {
  const data = await api(`/api/containers/${encodeURIComponent(name)}`);
  if (state.selected !== name) {
    state.serviceFilter = "running";
    state.serviceSearch = "";
  }
  state.selected = name;
  state.selectedDetail = data;
  renderDetail(data);
  renderSecurity();
  if (navigate) switchView("detail");
  ensureDetailLiveData(data.name || name);
  if (showToast) toast(uiText("已加载详细参数", "Details loaded"));
}

function restoreDetailLiveData(name) {
  if (!name) return;
  if (state.containerUsers[name]) renderDetailUsers(name, state.containerUsers[name]);
  if (state.containerServices[name]) renderDetailServices(name, state.containerServices[name]);
}

function ensureDetailLiveData(name) {
  if (!name) return;
  restoreDetailLiveData(name);
  if (!state.containerUsers[name]) loadDetailUsers(name).catch((err) => { const node = $("#detailUsers"); if (node) node.innerHTML = `<div class="empty-state">${escapeHTML(err.message)}</div>`; });
  if (!state.containerServices[name]) loadDetailServices(name).catch((err) => { const node = $("#detailServices"); if (node) node.innerHTML = `<div class="empty-state">${escapeHTML(err.message)}</div>`; });
}

function detailHasValue(value) {
  if (value === undefined || value === null) return false;
  if (typeof value === "string") {
    const text = value.trim();
    return text !== "" && !["-", "无", "未知", "null", "undefined"].includes(text.toLowerCase());
  }
  if (typeof value === "number") return Number.isFinite(value) && value !== 0;
  if (Array.isArray(value)) return value.length > 0;
  return true;
}

function detailRows(rows) {
  return rows.filter(([, value]) => detailHasValue(value));
}

function detailCard(title, rows, options = {}) {
  const filtered = detailRows(rows);
  if (!filtered.length) return "";
  return `<section class="detail-card ${options.wide ? "wide" : ""}"><h3>${escapeHTML(title)}</h3>${kvRows(filtered)}</section>`;
}

function detailHTMLCard(title, html, options = {}) {
  if (!detailHasValue(html)) return "";
  return `<section class="detail-card ${options.wide ? "wide" : ""}"><h3>${escapeHTML(title)}</h3>${html}</section>`;
}

function renderDetail(data) {
  const distroName = data.distroName || containerDistroInfo(data).label;
  $("#detailTitle").textContent = data.name || state.selected || uiText("详细参数", "Details");
  $("#detailSubtitle").textContent = [distroName, data.running ? uiText(`已启动 · ${containerUptimeText(data)}`, `Running · ${containerUptimeText(data)}`) : uiText("已停止", "Stopped"), data.source ? sourceLabel(data.source) : ""].filter(Boolean).join(" · ");
  const body = $("#detailBody");
  const flags = [
    [uiText("前台", "Foreground"), data.foreground],
    [uiText("易失", "Volatile"), data.volatileMode],
    ["cgroup v1", data.forceCgroupV1],
    [uiText("禁用 IPv6", "Disable IPv6"), data.disableIPv6],
    ...(supportsAndroidStorage() ? [[uiText("Android 存储", "Android Storage"), data.androidStorage]] : []),
    [uiText("SELinux 宽容", "SELinux Permissive"), data.selinuxPermissive],
    [uiText("用户命名空间", "User Namespaces"), data.allowUserns],
    [uiText("硬件访问", "Hardware Access"), data.hwAccess],
    ["GPU", data.gpuMode],
    ["Termux X11", data.termuxX11],
    [uiText("阻止嵌套 NS", "Block Nested Namespaces"), data.blockNestedNamespaces],
    [uiText("开机启动", "Start at Boot"), data.runAtBoot],
    [uiText("镜像挂载", "Image Mount"), data.isImageMount],
  ];
  const envRows = safeArray(data.env)
    .filter((env) => detailHasValue(env.key) || detailHasValue(env.value))
    .map((env) => `<div class="kv"><span>${escapeHTML(env.key || uiText("变量", "Variable"))}</span><span class="mono">${escapeHTML(env.value || "")}</span></div>`)
    .join("");
  const binds = safeArray(data.binds)
    .filter((bind) => detailHasValue(bind.source) || detailHasValue(bind.destination))
    .map((bind) => `<div class="kv"><span>${bind.readOnly ? uiText("只读", "Read-only") : uiText("读写", "Read-write")}</span><span class="mono">${escapeHTML(bind.source || "")} → ${escapeHTML(bind.destination || "")}</span></div>`)
    .join("");
  const networkRows = [
    [uiText("模式", "Mode"), data.netMode],
    ...(data.netMode === "nat" ? [["NAT/IP", data.natIp || data.staticNatIp], [uiText("端口", "Ports"), safeArray(data.ports).length ? portText(data.ports) : ""]] : []),
    ["DNS", data.dnsServers],
  ];
  const enabledFlags = flags.filter(([, value]) => Boolean(value));
  const flagRows = enabledFlags.map(([label]) => `<div class="flag on"><span>${label}</span><strong>${uiText("开", "On")}</strong></div>`).join("");
  const summaryCards = [
    detailCard(uiText("概览", "Overview"), [[uiText("系统", "System"), distroName], [uiText("状态", "Status"), data.running ? uiText("已启动", "Running") : uiText("已停止", "Stopped")], [uiText("运行时间", "Uptime"), data.running ? containerUptimeText(data) : ""], ["PID", data.running ? data.pid : ""], ["UUID", data.uuid], [uiText("主机名", "Hostname"), data.hostname], [uiText("启动时间", "Started"), data.running && data.startedAt ? fmtTime(data.startedAt) : ""], [uiText("开机启动", "Start at Boot"), data.runAtBoot ? uiText("启用", "Enabled") : ""], [uiText("启动顺序", "Startup Order"), data.runAtBootPriority || ""], [uiText("来源", "Source"), data.source ? sourceLabel(data.source) : ""]]),
    detailCard(uiText("网络", "Network"), networkRows),
    detailCard(uiText("资源", "Resources"), [[containerMemoryLabel(data), containerMemoryText(data)], ...containerCgroupMemoryRows(data), [uiText("内存限制", "Memory Limit"), data.memoryLimitText || (data.memoryLimit ? fmtBytes(data.memoryLimit) : "")], [uiText("磁盘占用", "Disk Usage"), containerDiskText(data)], ["CPU", data.cpusText || (data.cpuQuota ? `${data.cpuQuota}/${data.cpuPeriod || uiText("默认", "default")}` : "")], ["PIDs", data.pidsLimit || data.configValues?.pids_limit]]),
    detailCard(uiText("镜像", "Image"), [[uiText("路径", "Path"), data.rootfsPath], [uiText("镜像", "Image"), data.imageRef], ["Init", data.customInit]]),
    detailHTMLCard(uiText("绑定挂载", "Bind Mounts"), binds, { wide: true }),
    detailHTMLCard(uiText("环境变量", "Environment Variables"), envRows, { wide: true }),
    detailHTMLCard(uiText("运行标志", "Runtime Flags"), flagRows ? `<div class="flag-grid">${flagRows}</div>` : "", { wide: true }),
    detailCard(uiText("图形参数", "Graphics"), [[uiText("X11 参数", "X11 Arguments"), data.tx11ExtraFlags || data.tx11_extra_flags || data.configValues?.tx11_extra_flags], [uiText("VirGL 参数", "VirGL Arguments"), data.virglExtraFlags || data.virgl_extra_flags || data.configValues?.virgl_extra_flags]], { wide: true }),
  ].join("");
  const rootfsAction = data.isImageMount || String(data.rootfsPath || "").toLowerCase().endsWith(".img")
    ? `<button class="text-btn" data-sparse-action="resize" data-name="${encodeURIComponent(data.name || state.selected)}">${uiText("调整大小", "Resize")}</button>`
    : `<button class="text-btn" data-sparse-action="migrate" data-name="${encodeURIComponent(data.name || state.selected)}">${uiText("迁移为镜像", "Migrate to Image")}</button>`;
  const detailOperation = containerTaskForName(data.name || state.selected);
  const detailDeleteDisabled = detailOperation ? " disabled" : "";
  const detailDeleteTitle = detailOperation ? ` title="${escapeHTML(uiText(`正在${containerOperationLabel(detailOperation.action)}容器`, `${containerOperationLabel(detailOperation.action)} container in progress`))}"` : "";
  body.innerHTML = `<div class="entity-head"><div>${containerDistroIcon(data, "detail-distro-icon")}<span class="state-dot ${data.running ? "running" : ""}"></span><strong>${escapeHTML(data.name || "-")}</strong></div><div class="row-actions"><button class="text-btn" data-action="terminal" data-name="${encodeURIComponent(data.name || state.selected)}">${uiText("终端", "Terminal")}</button><button class="text-btn" data-action="config" data-name="${encodeURIComponent(data.name || state.selected)}">${uiText("编辑", "Edit")}</button><button class="text-btn" data-action="export" data-name="${encodeURIComponent(data.name || state.selected)}">${uiText("备份", "Back up")}</button><button class="text-btn" data-action="template" data-name="${encodeURIComponent(data.name || state.selected)}">${uiText("转换为模板", "Convert to template")}</button></div></div>
    <div class="detail-tabs" role="tablist" aria-label="${uiText("容器详情功能", "Container detail tools")}"><button class="segmented-option" type="button" data-detail-tab="summary">${uiText("摘要", "Summary")}</button><button class="segmented-option" type="button" data-detail-tab="users">${uiText("用户", "Users")}</button><button class="segmented-option" type="button" data-detail-tab="services">${uiText("服务", "Services")}</button><button class="segmented-option" type="button" data-detail-tab="image">${uiText("镜像", "Image")}</button><button class="segmented-option" type="button" data-detail-tab="diagnostics">${uiText("诊断", "Diagnostics")}</button><button class="segmented-option danger-tab" type="button" data-detail-tab="danger">${uiText("危险", "Danger")}</button></div>
    <div id="detailSummaryPanel" class="detail-tab-panel"><div class="detail-grid">${summaryCards || `<section class="detail-card wide"><div class="empty-state">${uiText("暂无详细参数", "No detail information")}</div></section>`}</div></div>
    <div id="detailUsersPanel" class="detail-tab-panel"><section class="detail-card wide compact-detail-card"><div class="section-line"><h3>${uiText("用户列表", "Users")}</h3><button class="text-btn" data-detail-load="users" data-name="${encodeURIComponent(data.name || state.selected)}">${uiText("刷新", "Refresh")}</button></div><div id="detailUsers" class="user-grid"><div class="empty-state">${uiText("加载中", "Loading")}</div></div></section></div>
    <div id="detailServicesPanel" class="detail-tab-panel"><section class="detail-card wide compact-detail-card"><div class="section-line"><h3>${uiText("服务管理", "Service Management")}</h3><button class="text-btn" data-detail-load="services" data-name="${encodeURIComponent(data.name || state.selected)}">${uiText("刷新", "Refresh")}</button></div><div class="service-tools"><input id="detailServiceSearch" type="search" value="${escapeHTML(state.serviceSearch)}" placeholder="${uiText("搜索服务...", "Search services...")}" autocomplete="off" /><div id="detailServiceFilters" class="service-filter-row" role="tablist" aria-label="${uiText("服务状态筛选", "Service status filter")}"></div></div><div id="detailServices" class="service-list"><div class="empty-state">${uiText("加载中", "Loading")}</div></div><div id="detailSystemdUnit" class="hidden"></div><pre id="detailServiceOutput" class="mini-output service-output">${uiText("等待操作", "Waiting for an action")}</pre></section></div>
    <div id="detailImagePanel" class="detail-tab-panel"><section class="detail-card wide"><h3>rootfs.img</h3><div class="row-actions">${rootfsAction}</div><pre id="detailSparseOutput" class="mini-output">${uiText("等待操作", "Waiting for an action")}</pre></section></div>
    <div id="detailDiagnosticsPanel" class="detail-tab-panel"><section class="detail-card wide"><h3>${uiText("诊断设置", "Diagnostic Settings")}</h3><div class="row-actions"><button class="text-btn" data-network-diagnose="${encodeURIComponent(data.name || state.selected)}">${uiText("网络诊断", "Network Diagnostics")}</button><button class="text-btn" data-detail-load="diagnostics" data-name="${encodeURIComponent(data.name || state.selected)}">${uiText("打开设置", "Open Settings")}</button><button class="text-btn" data-diagnostics-action="requirements">Requirements</button><button class="text-btn" data-diagnostics-action="bugreport">Bugreport</button></div><div id="detailDiagnosticsOutput" class="mini-output diagnostics-output">${uiText("等待执行", "Waiting to run")}</div></section>${data.rawOutput ? `<section class="detail-card wide"><h3>${uiText("原始 CLI", "Raw CLI")}</h3><pre class="mini-output">${escapeHTML(data.rawOutput)}</pre></section>` : ""}</div>
    <div id="detailDangerPanel" class="detail-tab-panel"><section class="detail-card wide danger-zone"><h3>${uiText("危险操作", "Danger Zone")}</h3><p class="muted">${uiText("删除会移除容器目录及其数据。", "Deletion removes the container directory and its data.")}</p><button class="text-btn danger" data-action="delete" data-name="${encodeURIComponent(data.name || state.selected)}"${detailDeleteDisabled}${detailDeleteTitle}>${uiText("删除容器", "Delete Container")}</button></section></div>`;
  restoreDetailLiveData(data.name || state.selected);
  switchDetailTab(state.detailTab || "summary");
}

function switchDetailTab(tab) {
  const panels = { summary: "#detailSummaryPanel", users: "#detailUsersPanel", services: "#detailServicesPanel", image: "#detailImagePanel", diagnostics: "#detailDiagnosticsPanel", danger: "#detailDangerPanel" };
  state.detailTab = panels[tab] ? tab : "summary";
  $$('[data-detail-tab]').forEach((button) => button.classList.toggle("active", button.dataset.detailTab === state.detailTab));
  Object.values(panels).forEach((selector) => $(selector)?.classList.remove("active"));
  $(panels[state.detailTab] || panels.summary)?.classList.add("active");
}

function normalizeUsers(data) {
  return safeArray(data.users || data.items || data).map((item) => typeof item === "string" ? { name: item } : item).filter((item) => item.name || item.username);
}

function withRootUser(users) {
  const out = [];
  const seen = new Set();
  const add = (item) => {
    const username = item?.name || item?.username;
    if (!username || seen.has(username)) return;
    seen.add(username);
    out.push(item);
  };
  add({ name: "root", uid: "0", gid: "0" });
  safeArray(users).forEach(add);
  return out;
}

function normalizeServices(data) {
  const raw = data?.services || data?.items || data;
  let services = [];
  if (Array.isArray(raw)) services = raw;
  else if (raw && typeof raw === "object") {
    services = Object.entries(raw).flatMap(([manager, items]) => safeArray(items).map((item) => ({ ...(typeof item === "string" ? { name: item } : item), manager: item?.manager || manager })));
  }
  return services.map((item) => typeof item === "string" ? { name: item } : item).filter((item) => item.name || item.service);
}

function renderDetailUsers(name, data) {
  const node = $("#detailUsers");
  if (!node) return;
  const users = withRootUser(normalizeUsers(data));
  if (!users.length) { node.innerHTML = `<div class="empty-state">${uiText("暂无用户", "No users")}</div>`; return; }
  node.innerHTML = users.map((user) => {
    const username = user.name || user.username;
    const meta = [user.uid ? `UID ${user.uid}` : "", user.gid ? `GID ${user.gid}` : "", user.shell || ""].filter(Boolean).join(" · ");
    return `<div class="user-item"><div><strong>${escapeHTML(username)}</strong>${meta ? `<span>${escapeHTML(meta)}</span>` : ""}</div><button class="text-btn" data-terminal-user="${encodeURIComponent(username)}" data-name="${encodeURIComponent(name)}">${uiText("进入终端", "Open Terminal")}</button></div>`;
  }).join("");
}

function serviceActionsForManager(manager) {
  if (manager === "systemd") return ["start", "stop", "restart", "enable", "disable", "mask", "unmask"];
  if (manager === "procd") return ["start", "stop", "restart", "reload", "enable", "disable"];
  return ["start", "stop", "restart", "enable", "disable"];
}

function normalizeServiceRunState(service) {
  const raw = String(firstValue(service.state, service.status, service.activeState, service.subState, "")).trim().toLowerCase();
  if (service.running === true || raw === "active" || raw === "running" || raw === "started" || raw === "online" || raw.includes("running")) return "running";
  if (raw === "failed" || raw === "crashed" || raw.includes("failed")) return "failed";
  if (raw === "activating" || raw === "starting") return "starting";
  if (raw === "deactivating" || raw === "stopping") return "stopping";
  if (raw === "inactive" || raw === "stopped" || raw === "dead" || raw === "exited" || raw.includes("inactive") || raw.includes("stopped")) return "stopped";
  if (service.running === false && !raw) return "stopped";
  return raw || "unknown";
}

function normalizeServiceEnableState(service) {
  const raw = String(firstValue(
    service.enableState,
    service.enabledState,
    service.unitFileState,
    service.enabled === true ? "enabled" : service.enabled === false ? "disabled" : "",
  )).trim().toLowerCase();
  if (raw.includes("masked")) return "masked";
  if (raw.includes("static")) return "static";
  if (raw.includes("disabled")) return "disabled";
  if (raw.includes("enabled")) return "enabled";
  if (raw.includes("indirect")) return "indirect";
  if (raw.includes("generated")) return "generated";
  if (raw.includes("transient")) return "transient";
  return raw || "unknown";
}

function serviceModel(service) {
  const runState = normalizeServiceRunState(service);
  const enableState = normalizeServiceEnableState(service);
  const isRunning = runState === "running";
  const isEnabled = enableState === "enabled";
  const isMasked = enableState === "masked";
  const isStatic = enableState === "static";
  return { runState, enableState, isRunning, isEnabled, isMasked, isStatic };
}

function serviceFilterKey(service) {
  const model = serviceModel(service);
  if (model.isMasked) return "masked";
  if (model.isStatic) return "static";
  if (model.isRunning && model.isEnabled) return "running";
  if (model.isRunning && !model.isEnabled) return "abnormal";
  if (model.isEnabled && !model.isRunning) return "enabled";
  if (!model.isEnabled && !model.isRunning) return "disabled";
  return "abnormal";
}

function serviceMatchesFilter(service, filter) {
  return filter === "all" || serviceFilterKey(service) === filter;
}

function serviceCounts(services) {
  return SERVICE_FILTERS.reduce((counts, [filter]) => {
    counts[filter] = filter === "all" ? services.length : services.filter((service) => serviceMatchesFilter(service, filter)).length;
    return counts;
  }, {});
}

function filterServicesForDetail(services) {
  const query = state.serviceSearch.trim().toLowerCase();
  const filtered = query
    ? services.filter((service) => String(service.name || service.service || "").toLowerCase().includes(query))
    : services.filter((service) => serviceMatchesFilter(service, state.serviceFilter || "running"));
  return filtered.sort((a, b) => {
    const ar = serviceModel(a).isRunning ? 1 : 0;
    const br = serviceModel(b).isRunning ? 1 : 0;
    if (ar !== br) return br - ar;
    return String(a.name || a.service || "").localeCompare(String(b.name || b.service || ""), uiLocale());
  });
}

function serviceRunLabel(state) {
  return {
    running: uiText("运行中", "Running"),
    stopped: uiText("已停止", "Stopped"),
    failed: uiText("失败", "Failed"),
    starting: uiText("启动中", "Starting"),
    stopping: uiText("停止中", "Stopping"),
    unknown: uiText("运行未知", "Unknown state"),
  }[state] || state;
}

function serviceEnableLabel(state) {
  return {
    enabled: uiText("已启用", "Enabled"),
    disabled: uiText("已禁用", "Disabled"),
    masked: uiText("已屏蔽", "Masked"),
    static: uiText("静态", "Static"),
    indirect: uiText("间接启用", "Indirect"),
    generated: uiText("生成", "Generated"),
    transient: uiText("临时", "Transient"),
    unknown: uiText("启用未知", "Enable state unknown"),
  }[state] || state;
}

function serviceStateClass(prefix, state) {
  const value = String(state || "unknown").replace(/[^a-z0-9_-]/g, "-") || "unknown";
  return `${prefix}-${value}`;
}

function renderServiceFilters(services) {
  const wrap = $("#detailServiceFilters");
  if (!wrap) return;
  const counts = serviceCounts(services);
  const labels = {
    running: uiText("运行中", "Running"),
    enabled: uiText("已启用", "Enabled"),
    disabled: uiText("已禁用", "Disabled"),
    abnormal: uiText("异常", "Abnormal"),
    static: uiText("静态", "Static"),
    masked: uiText("已屏蔽", "Masked"),
    all: uiText("全部", "All"),
  };
  wrap.innerHTML = SERVICE_FILTERS.map(([filter, label]) => {
    const active = !state.serviceSearch.trim() && (state.serviceFilter || "running") === filter;
    const dot = filter === "all" ? "" : `<span class="service-filter-dot service-filter-dot-${filter}"></span>`;
    return `<button class="service-filter-chip ${active ? "active" : ""}" type="button" data-service-filter="${filter}" aria-pressed="${active ? "true" : "false"}">${dot}${escapeHTML(labels[filter] || label)} (${counts[filter] || 0})</button>`;
  }).join("");
}

function renderDetailServices(name, data) {
  const node = $("#detailServices");
  if (!node) return;
  const services = normalizeServices(data);
  const search = $("#detailServiceSearch");
  if (search && search.value !== state.serviceSearch) search.value = state.serviceSearch;
  renderServiceFilters(services);
  if (!services.length) { node.innerHTML = `<div class="empty-state">${uiText("暂无服务", "No services")}</div>`; return; }
  const visibleServices = filterServicesForDetail(services);
  if (!visibleServices.length) {
    const emptyText = state.serviceSearch.trim()
      ? uiText("未找到服务", "No services found")
      : ({
        running: uiText("没有运行中的服务", "No running services"),
        enabled: uiText("没有已启用的服务", "No enabled services"),
        disabled: uiText("没有已禁用的服务", "No disabled services"),
        abnormal: uiText("没有异常服务", "No abnormal services"),
        static: uiText("没有静态服务", "No static services"),
        masked: uiText("没有已屏蔽的服务", "No masked services"),
        all: uiText("未找到服务", "No services found"),
      })[state.serviceFilter || "running"] || uiText("未找到服务", "No services found");
    node.innerHTML = `<div class="empty-state">${escapeHTML(emptyText)}</div>`;
    return;
  }
  node.innerHTML = visibleServices.map((service) => {
    const manager = service.manager || data.manager || data.defaultManager || "systemd";
    const serviceName = service.name || service.service;
    const { runState, enableState } = serviceModel(service);
    const filterKey = serviceFilterKey(service);
    const rawState = firstValue(service.state, service.status, "-");
    const rawEnableState = firstValue(service.enableState, service.enabledState, service.unitFileState, service.enabled === true ? "enabled" : service.enabled === false ? "disabled" : "-");
    const serviceTitle = uiText(`运行状态：${rawState} / 启用状态：${rawEnableState}`, `Run state: ${rawState} / Enable state: ${rawEnableState}`);
    const description = service.description && service.description !== serviceName ? `<span class="service-desc">${escapeHTML(service.description)}</span>` : "";
    const inspect = manager === "systemd" ? `<button class="mini-action" data-systemd-inspect="1" data-service-name="${escapeHTML(serviceName)}" data-name="${encodeURIComponent(name)}">${uiText("检查", "Inspect")}</button>` : "";
    const actions = inspect + serviceActionsForManager(manager).map((action) => `<button class="mini-action" data-service-action="${action}" data-service-manager="${escapeHTML(manager)}" data-service-name="${escapeHTML(serviceName)}" data-name="${encodeURIComponent(name)}">${escapeHTML({ start: uiText("启动", "Start"), stop: uiText("停止", "Stop"), restart: uiText("重启", "Restart"), reload: uiText("重载", "Reload"), enable: uiText("启用", "Enable"), disable: uiText("禁用", "Disable"), mask: "Mask", unmask: "Unmask" }[action] || action)}</button>`).join("");
    const filterLabel = {
      running: uiText("运行中", "Running"), enabled: uiText("已启用", "Enabled"), disabled: uiText("已禁用", "Disabled"), abnormal: uiText("异常", "Abnormal"), static: uiText("静态", "Static"), masked: uiText("已屏蔽", "Masked"), all: uiText("全部", "All"),
    }[filterKey] || filterKey;
    return `<div class="service-row service-row-${escapeHTML(filterKey)}"><div class="service-main"><strong>${escapeHTML(serviceName)}</strong><div class="service-badges" title="${escapeHTML(serviceTitle)}"><span class="service-badge service-status-${escapeHTML(filterKey)}">${escapeHTML(filterLabel)}</span><span class="service-badge ${serviceStateClass("run", runState)}">${escapeHTML(serviceRunLabel(runState))}</span><span class="service-badge ${serviceStateClass("enable", enableState)}">${escapeHTML(serviceEnableLabel(enableState))}</span><span class="mono service-manager">${escapeHTML(manager)}</span></div>${description}</div><div class="service-actions">${actions}</div></div>`;
  }).join("");
}

function renderSystemdUnitInspector() {
  const node = $("#detailSystemdUnit");
  if (!node) return;
  const selected = state.systemdUnit;
  if (!selected) {
    node.classList.add("hidden");
    node.innerHTML = "";
    return;
  }
  const properties = Object.entries(selected.inspection?.properties || {}).filter(([, value]) => String(value || "").trim() !== "");
  const propertyHTML = properties.length ? kvRows(properties) : `<div class="empty-state">${uiText("没有可用属性", "No properties available")}</div>`;
  const deps = safeArray(selected.inspection?.dependencies).map((item) => `<li class="mono">${escapeHTML(item)}</li>`).join("") || `<li class="muted">${uiText("没有依赖项", "No dependencies")}</li>`;
  const status = selected.inspection?.statusText || uiText("没有 systemctl status 输出", "No systemctl status output");
  const override = selected.override || { exists: false, content: "" };
  node.classList.remove("hidden");
  node.innerHTML = `<section class="systemd-inspector"><div class="section-line"><div><h3>${escapeHTML(selected.unit)}</h3><p class="muted">${uiText("systemd 单元检查与 override.conf", "systemd unit inspection and override.conf")}</p></div><div class="tool-buttons"><button class="icon-btn" type="button" data-systemd-inspect="1" data-service-name="${escapeHTML(selected.unit)}" data-name="${encodeURIComponent(selected.name)}" title="${uiText("刷新", "Refresh")}" aria-label="${uiText("刷新", "Refresh")}">↻</button><button class="icon-btn" type="button" data-systemd-inspector-close="1" title="${uiText("关闭", "Close")}" aria-label="${uiText("关闭", "Close")}">×</button></div></div><div class="systemd-inspector-grid"><section><h4>${uiText("属性", "Properties")}</h4>${propertyHTML}</section><section><h4>${uiText("依赖", "Dependencies")}</h4><ul class="systemd-dependencies">${deps}</ul></section></div><section class="systemd-status"><h4>systemctl status</h4><pre class="mini-output">${escapeHTML(status)}</pre></section><section class="systemd-override"><div class="section-line"><h4>override.conf</h4><div class="tool-buttons"><button class="text-btn" type="button" data-systemd-override-save="1">${uiText("保存", "Save")}</button>${override.exists ? `<button class="text-btn danger" type="button" data-systemd-override-delete="1">${uiText("删除", "Delete")}</button>` : ""}</div></div><textarea id="detailSystemdOverride" class="form-textarea" rows="10" spellcheck="false">${escapeHTML(override.content || "")}</textarea></section><pre id="detailSystemdOverrideOutput" class="mini-output service-output">${escapeHTML(selected.overrideOutput || uiText("等待操作", "Waiting for an action"))}</pre></section>`;
}

async function openSystemdUnitInspector(name, unit) {
  setBusy(true);
  try {
    const [inspection, override] = await Promise.all([fetchSystemdUnitInspection(name, unit), fetchSystemdOverride(name, unit)]);
    state.systemdUnit = { name, unit, inspection, override, overrideOutput: uiText("等待操作", "Waiting for an action") };
    renderSystemdUnitInspector();
  } catch (err) {
    toast(err.message);
  } finally {
    setBusy(false);
  }
}

async function persistSystemdOverride(remove = false) {
  const selected = state.systemdUnit;
  if (!selected) return;
  if (remove && !confirm(uiText(`删除 ${selected.unit} 的 override.conf？`, `Delete override.conf for ${selected.unit}?`))) return;
  setBusy(true);
  try {
    const content = $("#detailSystemdOverride")?.value || "";
    const result = remove
      ? await deleteSystemdOverride(selected.name, selected.unit)
      : await saveSystemdOverride(selected.name, selected.unit, content);
    selected.override = remove ? { exists: false, content: "" } : { exists: true, content };
    selected.overrideOutput = result.output || (remove ? uiText("override.conf 已删除并重新加载 systemd", "override.conf deleted and systemd reloaded") : uiText("override.conf 已保存并重新加载 systemd", "override.conf saved and systemd reloaded"));
    renderSystemdUnitInspector();
    toast(remove ? uiText("override.conf 已删除", "override.conf deleted") : uiText("override.conf 已保存", "override.conf saved"));
  } catch (err) {
    selected.overrideOutput = err.message;
    renderSystemdUnitInspector();
    toast(err.message);
  } finally {
    setBusy(false);
  }
}

function renderBootPriorityList() {
  const node = $("#bootPriorityList");
  if (!node) return;
  const containers = state.bootPriorityContainers;
  if (!containers.length) {
    node.innerHTML = `<div class="empty-state">${t("boot.noContainers")}</div>`;
    return;
  }
  node.innerHTML = containers.map((container, index) => `<div class="priority-row"><span class="priority-index">${index + 1}</span><div class="priority-main"><strong>${escapeHTML(container.name || "-")}</strong><span class="muted">${escapeHTML(container.hostname || container.netMode || "")}</span></div><div class="priority-actions"><button class="icon-btn" type="button" data-boot-priority-move="up" data-boot-priority-index="${index}" title="${t("boot.moveUp")}" aria-label="${t("boot.moveUp")}" ${index === 0 ? "disabled" : ""}>↑</button><button class="icon-btn" type="button" data-boot-priority-move="down" data-boot-priority-index="${index}" title="${t("boot.moveDown")}" aria-label="${t("boot.moveDown")}" ${index === containers.length - 1 ? "disabled" : ""}>↓</button></div></div>`).join("");
}

function moveBootPriority(index, direction) {
  const target = index + (direction === "up" ? -1 : 1);
  const containers = state.bootPriorityContainers;
  if (index < 0 || index >= containers.length || target < 0 || target >= containers.length) return;
  [containers[index], containers[target]] = [containers[target], containers[index]];
  renderBootPriorityList();
}

async function showBootPriorityModal() {
  setBusy(true);
  try {
    await fetchBootPriority();
    renderBootPriorityList();
    $("#bootPriorityModal")?.classList.remove("hidden");
  } catch (err) {
    toast(err.message);
  } finally {
    setBusy(false);
  }
}

function hideBootPriorityModal() {
  $("#bootPriorityModal")?.classList.add("hidden");
}

async function submitBootPriority() {
  setBusy(true);
  try {
    await saveBootPriority(state.bootPriorityContainers.map((container) => container.name));
    renderBootPriorityList();
    hideBootPriorityModal();
    await refreshAll();
    toast(uiText("开机启动顺序已保存", "Boot startup order saved"));
  } catch (err) {
    toast(err.message);
  } finally {
    setBusy(false);
  }
}

function renderDiagnosticsOutput(data, selector = "#diagnosticsOutput") {
  const node = $(selector) || $("#diagnosticsOutput");
  if (!node) return;
  const text = typeof data === "string" ? data : JSON.stringify(data, null, 2);
  if (node.tagName === "PRE") node.textContent = text;
  else node.innerHTML = `<pre class="mini-output">${escapeHTML(text)}</pre>`;
}

function renderDiagnosticsSettings(data, selector = "#diagnosticsOutput") {
  const node = $(selector) || $("#diagnosticsOutput");
  if (!node) return;
  const isPre = node.tagName === "PRE";
  const html = `<div class="diagnostics-card"><div class="settings-grid compact-settings"><label><span>Daemon mode</span><input type="checkbox" data-diag-toggle="daemonMode" ${data.daemonMode ? "checked" : ""} /></label><label><span>Symlink integration</span><input type="checkbox" data-diag-toggle="symlinkEnabled" ${data.symlinkEnabled ? "checked" : ""} /></label></div><div class="meta-grid"><span class="mono">${escapeHTML(data.daemonModeFile || "")}</span><span class="mono">${escapeHTML(data.symlinkPath || "")}</span><span class="mono">${escapeHTML(data.droidspacesPath || "")}</span></div></div>`;
  if (isPre) node.textContent = JSON.stringify(data, null, 2);
  else node.innerHTML = html;
}

function renderRequirements(data, selector = "#diagnosticsOutput") {
  const node = $(selector) || $("#diagnosticsOutput");
  if (!node) return;
  if (node.tagName === "PRE") { node.textContent = JSON.stringify(data, null, 2); return; }
  node.innerHTML = `<div class="diagnostics-card"><div class="row-actions"><button class="text-btn" data-copy-text="${escapeHTML(data.termuxSetup || "")}">${uiText("复制 Termux Setup", "Copy Termux Setup")}</button><button class="text-btn" data-copy-text="${escapeHTML(data.nonGKIConfig || "")}">${uiText("复制 non-GKI", "Copy non-GKI")}</button><button class="text-btn" data-copy-text="${escapeHTML(data.gkiConfig || "")}">${uiText("复制 GKI", "Copy GKI")}</button></div><h3>Termux setup</h3><pre class="mini-output">${escapeHTML(data.termuxSetup || "")}</pre><h3>non-GKI Kernel Config</h3><pre class="mini-output">${escapeHTML(data.nonGKIConfig || "")}</pre><h3>GKI Kernel Config</h3><pre class="mini-output">${escapeHTML(data.gkiConfig || "")}</pre></div>`;
}

async function loadDetailUsers(name) {
  const data = await fetchContainerUsers(name);
  renderDetailUsers(name, data);
  return data;
}

async function loadDetailServices(name) {
  const data = await fetchContainerServices(name);
  renderDetailServices(name, data);
  return data;
}

async function loadDetailDiagnostics() {
  const data = await fetchDiagnosticsSettings();
  renderDiagnosticsSettings(data, "#detailDiagnosticsOutput");
  return data;
}

async function runContainerNetworkDiagnostics(name) {
  if (!name) { toast(uiText("请先选择容器", "Select a container first")); return; }
  setBusy(true);
  renderDiagnosticsOutput(uiText("网络诊断执行中", "Running network diagnostics"), "#detailDiagnosticsOutput");
  try {
    const data = await api(`/api/containers/${encodeURIComponent(name)}/network-diagnose`, { method: "POST", body: JSON.stringify({}) });
    const output = `$ droidspaces --name ${name} run <network-diagnostics>
exit=${data.exitCode}

${data.output || ""}`;
    renderDiagnosticsOutput(output, "#detailDiagnosticsOutput");
    toast(data.ok ? uiText("网络诊断通过", "Network diagnostics passed") : uiText("网络诊断发现问题", "Network diagnostics found issues"));
  } catch (err) {
    renderDiagnosticsOutput(err.message, "#detailDiagnosticsOutput");
    toast(err.message);
  } finally {
    setBusy(false);
  }
}

async function runDiagnosticsAction(action, selector = "#diagnosticsOutput") {
  setBusy(true);
  renderDiagnosticsOutput(uiText("执行中", "Running"), selector);
  try {
    let data;
    if (action === "requirements") {
      data = await runDiagnosticsRequirements();
      const snippets = await fetchDiagnosticsRequirements();
      renderRequirements(snippets, selector);
    } else if (action === "bugreport") {
      data = await createDiagnosticsBugreport();
      renderDiagnosticsOutput(data, selector);
    } else {
      data = await fetchDiagnosticsSettings();
      renderDiagnosticsSettings(data, selector);
    }
    if (data.taskId) { trackTask(data.taskId); openTaskPanel(); }
    toast(action === "bugreport" ? uiText("Bugreport 已提交", "Bugreport submitted") : action === "requirements" ? uiText("Requirements 检查已提交", "Requirements check submitted") : uiText("诊断设置已加载", "Diagnostic settings loaded"));
  } catch (err) {
    renderDiagnosticsOutput(err.message, selector);
    toast(err.message);
  } finally {
    setBusy(false);
  }
}

async function runSparseAction(name, action) {
  const node = $("#detailSparseOutput");
  if (action === "migrate" && !confirm(uiText(`将 ${name} 迁移为 rootfs.img？运行中容器可能会被后端停止并恢复。`, `Migrate ${name} to rootfs.img? A running container may be stopped and restored by the backend.`))) return;
  const sizeGB = action === "resize" ? prompt(uiText("新的镜像大小 GB", "New image size in GB")) : "";
  if (action === "resize" && (!sizeGB || Number(sizeGB) <= 0)) return;
  setBusy(true);
  if (node) node.textContent = uiText("执行中", "Running");
  try {
    const data = action === "migrate" ? await migrateSparseImage(name) : await resizeSparseImage(name, sizeGB);
    if (node) node.textContent = JSON.stringify(data, null, 2);
    if (data.taskId) { trackTask(data.taskId); openTaskPanel(); }
    toast(action === "migrate" ? uiText("迁移任务已提交", "Migration task submitted") : uiText("调整大小任务已提交", "Resize task submitted"));
  } catch (err) {
    if (node) node.textContent = err.message;
    toast(err.message);
  } finally {
    setBusy(false);
  }
}

function copyText(value) {
  if (navigator.clipboard?.writeText) {
    navigator.clipboard.writeText(value).then(() => toast(uiText("已复制", "Copied")), (err) => toast(err.message));
    return;
  }
  const input = document.createElement("textarea");
  input.value = value;
  document.body.appendChild(input);
  input.select();
  document.execCommand("copy");
  input.remove();
  toast(uiText("已复制", "Copied"));
}

function kvRows(rows) {
  return rows.map(([key, value]) => `<div class="kv"><span>${escapeHTML(key)}</span><span class="mono">${escapeHTML(value)}</span></div>`).join("");
}

async function submitContainerOperation(name, action, request, onDone) {
  const target = String(name || "").trim();
  const existing = containerTaskForName(target);
  if (existing) {
    toast(uiText(`${target} 正在${containerOperationLabel(existing.action)}，请等待任务完成`, `${containerOperationLabel(existing.action)} is already in progress for ${target}. Wait for the task to finish.`));
    return null;
  }
  setContainerTask(target, action);
  refreshContainerOperationUI();
  try {
    const data = await api(request.path, request.options || {});
    const taskId = String(data.taskId || data.task?.id || "").trim();
    if (!taskId) throw new Error(uiText("后端未返回容器后台任务 ID", "The backend did not return a container background task ID"));
    const serverTask = data.task && typeof data.task === "object" ? data.task : {};
    const previous = state.tasks[taskId] || {};
    state.tasks[taskId] = {
      ...previous,
      ...serverTask,
      id: taskId,
      kind: serverTask.kind || `container-${action}`,
      name: serverTask.name || target,
      onDone: previous.onDone,
    };
    setContainerTask(target, action, taskId);
    refreshContainerOperationUI();
    trackTask(taskId, onDone);
    openTaskPanel();
    return data;
  } catch (err) {
    clearContainerTask(target);
    refreshContainerOperationUI();
    throw err;
  }
}

async function refreshAfterContainerOperation(name, action, task) {
  clearContainerTask(name, task?.id || "");
  await Promise.allSettled([loadStatus(), loadContainers(), loadEvents()]);
  if (action === "delete") {
    if (state.selected === name) clearDetail();
    $("#cliOutput").textContent = task?.path || uiText("已删除", "Deleted");
    toast(uiText(`已删除 ${name}`, `Deleted ${name}`));
    return;
  }
  if (state.selected === name) {
    await inspect(name, false, false).catch(() => {});
  }
  const output = Array.isArray(task?.log) ? task.log.slice(-80).join("\n") : (task?.output || "");
  if (output) $("#cliOutput").textContent = output;
  toast(uiText(`${containerOperationLabel(action)}完成：${name}`, `${containerOperationLabel(action)} completed: ${name}`));
}

async function runLifecycle(name, action) {
  if (!CONTAINER_OPERATION_ACTIONS.has(action)) return;
  try {
    const data = await submitContainerOperation(
      name,
      action,
      {
        path: `/api/containers/${encodeURIComponent(name)}/${action}?async=1`,
        options: { method: "POST" },
      },
      (task) => refreshAfterContainerOperation(name, action, task),
    );
    if (data) toast(uiText(`已开始${containerOperationLabel(action)}：${name}`, `${containerOperationLabel(action)} started: ${name}`));
  } catch (err) {
    toast(err.message);
  }
}

async function deleteContainer(name) {
  const existing = containerTaskForName(name);
  if (existing) {
    toast(uiText(`${name} 正在${containerOperationLabel(existing.action)}，请等待任务完成`, `${containerOperationLabel(existing.action)} is already in progress for ${name}. Wait for the task to finish.`));
    return;
  }
  if (!confirm(uiText(`删除容器 ${name}？这会删除该容器目录及其中数据。`, `Delete container ${name}? This removes its directory and all data in it.`))) return;
  try {
    const data = await submitContainerOperation(
      name,
      "delete",
      {
        path: `/api/containers/${encodeURIComponent(name)}?async=1`,
        options: { method: "DELETE" },
      },
      (task) => refreshAfterContainerOperation(name, "delete", task),
    );
    if (data) toast(uiText(`已开始删除：${name}`, `Deletion started: ${name}`));
  } catch (err) {
    toast(err.message);
  }
}

function clearDetail() {
  state.selected = "";
  state.selectedDetail = null;
  $("#detailTitle").textContent = uiText("详细参数", "Details");
  $("#detailSubtitle").textContent = uiText("从容器列表选择一个容器", "Select a container from the list");
  $("#detailBody").innerHTML = `<div class="empty-state">${uiText("尚未选择容器", "No container selected")}</div>`;
}

function setTerminalUser(user) {
  const username = user || "root";
  const input = $("#terminalUser");
  const select = $("#terminalUserSelect");
  if (input) input.value = username;
  if (select) select.value = Array.from(select.options).some((option) => option.value === username) ? username : "__manual";
}

function selectTerminal(name, user = "") {
  state.terminalTarget = name;
  const select = $("#terminalTarget");
  if (select) select.value = name;
  if (user) setTerminalUser(user);
  loadTerminalUsers(name).then(() => { if (user) setTerminalUser(user); }).catch((err) => toast(err.message));
  updateTerminalControls();
}

function openTerminalAsUser(name, user) {
  if (!name) { toast(uiText("请先选择容器", "Select a container first")); return; }
  selectTerminal(name, user || "root");
  switchView("terminal");
  connectTerminal();
}

function renderTerminalUserOptions(name, data) {
  const select = $("#terminalUserSelect");
  const input = $("#terminalUser");
  if (!select || !input) return;
  const users = withRootUser(normalizeUsers(data || state.containerUsers[name] || {}));
  const current = input.value.trim() || "root";
  select.innerHTML = users.map((user) => {
    const username = user.name || user.username;
    return `<option value="${escapeHTML(username)}">${escapeHTML(username)}</option>`;
  }).join("") + `<option value="__manual">${uiText("手动输入", "Manual input")}</option>`;
  if (users.some((user) => (user.name || user.username) === current)) select.value = current;
  else select.value = "__manual";
}

async function loadTerminalUsers(name, force = false) {
  if (!name) return;
  if (!force && state.containerUsers[name]) { renderTerminalUserOptions(name, state.containerUsers[name]); return; }
  const data = await fetchContainerUsers(name);
  renderTerminalUserOptions(name, data);
}

function renderTerminalTargets() {
  const select = $("#terminalTarget");
  if (!select) return;
  const current = select.value || state.terminalTarget;
  select.innerHTML = state.containers.map((container) => `<option value="${escapeHTML(container.name)}">${escapeHTML(container.name)}${container.running ? "" : uiText(" (停止)", " (stopped)")}</option>`).join("");
  if (state.containers.some((container) => container.name === current)) select.value = current;
  else if (state.containers.length > 0) select.value = state.containers[0].name;
  state.terminalTarget = select.value || "";
  if (state.terminalTarget) loadTerminalUsers(state.terminalTarget).catch(() => renderTerminalUserOptions(state.terminalTarget, { users: [{ name: "root" }] }));
  updateTerminalControls();
}

function terminalStatus(text, connected = false) {
  const node = $("#terminalStatus");
  if (!node) return;
  node.textContent = text;
  node.classList.toggle("connected", connected);
}

function updateTerminalControls() {
  const socket = state.terminalSocket;
  const connected = Boolean(socket && state.terminalConnected);
  const connecting = Boolean(socket && !state.terminalConnected);
  const connectBtn = $("#terminalConnectBtn");
  const disconnectBtn = $("#terminalDisconnectBtn");
  const sendBtn = $("#terminalSendBtn");
  const input = $("#terminalInput");
  const target = $("#terminalTarget");
  const user = $("#terminalUser");
  const userSelect = $("#terminalUserSelect");
  if (connectBtn) connectBtn.disabled = state.busy || connected || connecting;
  if (disconnectBtn) disconnectBtn.disabled = !socket;
  if (sendBtn) sendBtn.disabled = !connected;
  if (input) input.disabled = !connected;
  if (target) target.disabled = connected || connecting;
  if (user) user.disabled = connected || connecting;
  if (userSelect) userSelect.disabled = connected || connecting;
  if (connecting) terminalStatus(uiText("连接中", "Connecting"));
  else if (connected) terminalStatus(uiText(`已连接 ${state.terminalTarget}`, `Connected to ${state.terminalTarget}`), true);
  else terminalStatus(uiText("未连接", "Disconnected"));
}

function terminalURL(target, user) {
  const protocol = window.location.protocol === "https:" ? "wss:" : "ws:";
  const params = new URLSearchParams();
  const token = getAuthToken();
  if (token) params.set("token", token);
  if (user) params.set("user", user);
  return `${protocol}//${window.location.host}/api/containers/${encodeURIComponent(target)}/shell?${params.toString()}`;
}

function ensureTerminalLine(row) {
  while (state.terminalLines.length <= row) state.terminalLines.push("");
}

function applyTerminalCSI(sequence) {
  const final = sequence.slice(-1);
  const body = sequence.slice(0, -1).replace(/[?>]/g, "");
  const nums = body.split(";").filter(Boolean).map((part) => Number(part)).map((value) => (Number.isFinite(value) && value > 0 ? value : 1));
  const first = nums[0] || 1;
  if (final === "A") state.terminalRow = Math.max(0, state.terminalRow - first);
  else if (final === "B") { state.terminalRow += first; ensureTerminalLine(state.terminalRow); }
  else if (final === "C") state.terminalCol += first;
  else if (final === "D") state.terminalCol = Math.max(0, state.terminalCol - first);
  else if (final === "G") state.terminalCol = Math.max(0, first - 1);
  else if (final === "H" || final === "f") { state.terminalRow = Math.max(0, (nums[0] || 1) - 1); state.terminalCol = Math.max(0, (nums[1] || 1) - 1); ensureTerminalLine(state.terminalRow); }
  else if (final === "K") {
    ensureTerminalLine(state.terminalRow);
    const line = state.terminalLines[state.terminalRow] || "";
    const mode = nums[0] || 0;
    if (mode === 1) state.terminalLines[state.terminalRow] = line.slice(state.terminalCol);
    else if (mode === 2) state.terminalLines[state.terminalRow] = "";
    else state.terminalLines[state.terminalRow] = line.slice(0, state.terminalCol);
  } else if (final === "J") {
    const mode = nums[0] || 0;
    if (mode === 2) { state.terminalLines = [""]; state.terminalRow = 0; state.terminalCol = 0; }
    else { ensureTerminalLine(state.terminalRow); state.terminalLines[state.terminalRow] = (state.terminalLines[state.terminalRow] || "").slice(0, state.terminalCol); state.terminalLines = state.terminalLines.slice(0, state.terminalRow + 1); }
  }
}

function appendTerminalChar(ch) {
  ensureTerminalLine(state.terminalRow);
  if (ch === "\r") { state.terminalCol = 0; return; }
  if (ch === "\n") { state.terminalRow += 1; state.terminalCol = 0; ensureTerminalLine(state.terminalRow); return; }
  if (ch === "\b" || ch === "\x7f") { state.terminalCol = Math.max(0, state.terminalCol - 1); return; }
  if (ch === "\t") { const spaces = 8 - (state.terminalCol % 8); for (let i = 0; i < spaces; i += 1) appendTerminalChar(" "); return; }
  if (ch < " ") return;
  const line = state.terminalLines[state.terminalRow] || "";
  const padded = state.terminalCol > line.length ? `${line}${" ".repeat(state.terminalCol - line.length)}` : line;
  state.terminalLines[state.terminalRow] = `${padded.slice(0, state.terminalCol)}${ch}${padded.slice(state.terminalCol + 1)}`;
  state.terminalCol += 1;
}

function renderTerminalBuffer() {
  const screen = $("#terminalScreen");
  if (!screen) return;
  if (state.terminalLines.length > 2400) {
    const drop = state.terminalLines.length - 1800;
    state.terminalLines.splice(0, drop);
    state.terminalRow = Math.max(0, state.terminalRow - drop);
  }
  screen.textContent = state.terminalLines.join("\n");
  screen.scrollTop = screen.scrollHeight;
}

function appendTerminal(raw) {
  const text = String(raw);
  for (let i = 0; i < text.length; i += 1) {
    const ch = text[i];
    if (ch === "\x1b") {
      const next = text[i + 1];
      if (next === "[") {
        let j = i + 2;
        while (j < text.length && !/[@-~]/.test(text[j])) j += 1;
        if (j < text.length) { applyTerminalCSI(text.slice(i + 2, j + 1)); i = j; continue; }
      } else if (next === "]") {
        let j = i + 2;
        while (j < text.length && text[j] !== "\x07") { if (text[j] === "\x1b" && text[j + 1] === "\\") { j += 1; break; } j += 1; }
        i = j;
        continue;
      } else { i += 1; continue; }
    }
    appendTerminalChar(ch);
  }
  renderTerminalBuffer();
}

function resetTerminalBuffer(initialText = "") {
  state.terminalLines = [""];
  state.terminalRow = 0;
  state.terminalCol = 0;
  const screen = $("#terminalScreen");
  if (screen) screen.textContent = "";
  if (initialText) appendTerminal(initialText);
}

function sendTerminalRaw(value) {
  const socket = state.terminalSocket;
  if (!socket || !state.terminalConnected || socket.readyState !== WebSocket.OPEN) return false;
  socket.send(value);
  return true;
}

function sendTerminalInput() {
  const input = $("#terminalInput");
  if (!input) return;
  const value = input.value;
  if (!value) return;
  if (sendTerminalRaw(value.endsWith("\n") ? value : `${value}\r`)) input.value = "";
}

function connectTerminal() {
  const target = $("#terminalTarget").value;
  if (!target) { toast(uiText("请先选择容器", "Select a container first")); return; }
  const container = state.containers.find((item) => item.name === target);
  if (container && !container.running) { toast(uiText("请先启动容器", "Start the container first")); return; }
  if (state.terminalSocket) disconnectTerminal();
  const user = $("#terminalUser").value.trim() || "root";
  resetTerminalBuffer(uiText(`连接 ${target} (${user})...\n`, `Connecting to ${target} (${user})...\n`));
  state.terminalTarget = target;
  state.terminalConnected = false;
  const socket = new WebSocket(terminalURL(target, user));
  socket.binaryType = "arraybuffer";
  state.terminalSocket = socket;
  updateTerminalControls();
  socket.onopen = () => { state.terminalConnected = true; appendTerminal(uiText("已连接。\n", "Connected.\n")); updateTerminalControls(); $("#terminalScreen")?.focus(); };
  socket.onmessage = async (event) => {
    if (event.data instanceof Blob) appendTerminal(await event.data.text());
    else if (event.data instanceof ArrayBuffer) appendTerminal(new TextDecoder().decode(event.data));
    else appendTerminal(event.data);
  };
  socket.onerror = () => toast(uiText("终端连接错误", "Terminal connection error"));
  socket.onclose = (event) => {
    if (state.terminalSocket === socket) { state.terminalSocket = null; state.terminalConnected = false; }
    appendTerminal(uiText(`\n[连接已关闭${event.reason ? `: ${event.reason}` : ""}]\n`, `\n[Connection closed${event.reason ? `: ${event.reason}` : ""}]\n`));
    updateTerminalControls();
  };
}

function disconnectTerminal() {
  const socket = state.terminalSocket;
  if (!socket) return;
  state.terminalSocket = null;
  state.terminalConnected = false;
  socket.close(1000, "client disconnect");
  updateTerminalControls();
}

function clearTerminal() {
  resetTerminalBuffer();
  $("#terminalScreen")?.focus();
}

function handleTerminalKey(event) {
  if (!state.terminalConnected) return;
  if (event.metaKey || event.altKey) return;
  let value = "";
  if (event.ctrlKey && event.key.length === 1) {
    const code = event.key.toUpperCase().charCodeAt(0) - 64;
    if (code >= 0 && code <= 31) value = String.fromCharCode(code);
  } else {
    const special = { Enter: "\r", Backspace: "\x7f", Tab: "\t", Escape: "\x1b", ArrowUp: "\x1b[A", ArrowDown: "\x1b[B", ArrowRight: "\x1b[C", ArrowLeft: "\x1b[D", Home: "\x1b[H", End: "\x1b[F", Delete: "\x1b[3~", PageUp: "\x1b[5~", PageDown: "\x1b[6~" };
    value = special[event.key] || (event.key.length === 1 ? event.key : "");
  }
  if (value && sendTerminalRaw(value)) event.preventDefault();
}

async function createContainer(event) {
  event.preventDefault();
  const validationErrors = updateCreateFormValidation();
  if (validationErrors.length) {
    const first = $(validationErrors[0].selector);
    first?.focus({ preventScroll: true });
    first?.closest(".create-modal-body")?.scrollTo({ top: Math.max(0, first.offsetTop - 24), behavior: "smooth" });
    return;
  }
  const source = document.querySelector('input[name="createSource"]:checked')?.value || "local";
  const cloudAsset = source === "cloud" ? selectedCreateCloudAsset() : null;
  if (source === "cloud" && !cloudAsset?.downloadUrl) {
    toast(uiText("请选择可用的云端镜像", "Select an available cloud image"));
    return;
  }
  const netMode = $("#createNetMode").value;
  const rootfsPath = source === "local" ? $("#createLocalRootfs").value : "";
  const useSparseImage = Boolean($("#createUseSparseImage")?.checked);
  const storageMode = useSparseImage ? "image" : "directory";
  const imageSize = Number($("#createImageSize")?.value || 8);
  const cloudInitSupported = createTemplateSupportsCloudInit(source === "cloud" ? cloudAsset : selectedCreateLocalRootfs(), source);
  const cloudInitEnabled = cloudInitSupported ? Boolean($("#createCloudInitEnabled")?.checked) : undefined;
  const cloudInitUserData = cloudInitEnabled ? createCloudInitUserDataResult() : { value: "" };
  if (cloudInitUserData.error) {
    toast(cloudInitUserData.error);
    return;
  }
  const cloudInitPasswordNotice = cloudInitEnabled ? createCloudInitPasswordNotice() : null;
  const payload = {
    name: $("#createName").value.trim(),
    hostname: $("#createHostname").value.trim(),
    rootfsPath,
    rootfsSource: source,
    cloudRootfsUrl: cloudAsset?.downloadUrl || "",
    cloudInitEnabled,
    cloudInitUserData: cloudInitEnabled ? cloudInitUserData.value : "",
    cloudInitNetworkConfig: cloudInitEnabled ? $("#createCloudInitNetworkConfig")?.value || "" : "",
    rootfsStorageMode: storageMode,
    storageMode,
    useSparseImage,
    rootfsImageSizeGB: useSparseImage ? imageSize : 0,
    imageSizeGB: useSparseImage ? imageSize : 0,
    netMode,
    dnsServers: $("#createDns").value.trim(),
    portForwards: netMode === "nat" ? normalizeListInput($("#createPorts").value) : "",
    staticNatIp: netMode === "nat" ? staticNATIPValue("create") : "",
    gatewayContainer: netMode === "gateway" ? $("#createGatewayContainer").value.trim() : "",
    gatewayNet: netMode === "gateway" ? $("#createGatewayNet").value.trim() : "",
    gatewayLanIfname: netMode === "gateway" ? $("#createGatewayIface").value.trim() : "",
    gatewayBridge: netMode === "gateway" ? $("#createGatewayBridge").value.trim() : "",
    privilegedMode: privilegedModeValue("create"),
    bindMounts: normalizeListInput($("#createBinds").value),
    customInit: $("#createInit").value.trim(),
    tx11ExtraFlags: $("#createTermuxX11").checked ? $("#createTx11ExtraFlags").value.trim() : "",
    virglExtraFlags: $("#createVirgl").checked ? $("#createVirglExtraFlags").value.trim() : "",
    memoryLimit: $("#createMemoryLimit").value.trim(),
    pidsLimit: $("#createPidsLimit").value.trim(),
    ...cpuLimitPayload($("#createCpus").value),
    env: $("#createEnv").value,
    start: $("#createStart").checked,
    androidStorage: supportsAndroidStorage() && $("#createAndroidStorage").checked,
    hwAccess: $("#createHwAccess").checked,
    gpuMode: $("#createGpu").checked,
    termuxX11: $("#createTermuxX11").checked,
    virgl: $("#createVirgl").checked,
    pulseAudio: $("#createPulse").checked,
    selinuxPermissive: $("#createSelinuxPermissive").checked,
    allowUserns: $("#createAllowUserns").checked,
    volatileMode: $("#createVolatile").checked,
    runAtBoot: $("#createRunAtBoot").checked,
    forceCgroupV1: $("#createForceCgroupV1").checked,
    disableIPv6: modeForcesDisableIPv6(netMode) || $("#createDisableIpv6").checked,
    blockNestedNamespaces: $("#createBlockNestedNS").checked && !privilegedDisablesDeadlock(privilegedModeValue("create")),
  };
  setBusy(true);
  try {
    const data = await api("/api/containers", { method: "POST", body: JSON.stringify(payload) });
    hideCreateModal();
    if (data.task) state.tasks[data.task.id] = data.task;
    trackTask(data.taskId, async () => {
      await refreshAll();
      if (cloudInitPasswordNotice) {
        toast(uiText(`${payload.name} 创建完成。${cloudInitPasswordNotice.username} 的随机密码：${cloudInitPasswordNotice.password}`, `${payload.name} created. Random password for ${cloudInitPasswordNotice.username}: ${cloudInitPasswordNotice.password}`), 12000);
      } else {
        toast(uiText(`${payload.name} 创建完成`, `${payload.name} created`));
      }
    });
    openTaskPanel();
    toast(uiText(`已开始创建 ${payload.name}`, `Creation started: ${payload.name}`));
  } catch (err) {
    toast(err.message);
  } finally {
    setBusy(false);
  }
}

function clearCreateTemplateSelection() {
  ["#createLocalRootfs", "#createCloudRootfs", "#createLocalRootfsSearch", "#createCloudRootfsSearch", "#createCloudSourceFilter"].forEach((selector) => {
    const input = $(selector);
    if (input) input.value = "";
  });
}

function setCreateTemplateSelectionLocked(locked) {
  $("#createTemplatePicker")?.classList.toggle("template-selection-locked", Boolean(locked));
}

function showCreateModal(options = {}) {
  const requestedSource = options.source === "cloud" ? "cloud" : "local";
  const directLocalRootfsPath = String(options.localRootfsPath || "").trim();
  setCreateTemplateSelectionLocked(false);
  $("#createModal").classList.remove("hidden");
  resetCreateCloudInitFields();
  renderRuntimeVersions();
  $("#createCloudTask").textContent = uiText("选择镜像后直接创建，后台会下载并继续创建容器。", "After you select an image, it will download in the background and then create the container.");
  clearCreateTemplateSelection();
  const sourceInput = document.querySelector(`input[name="createSource"][value="${requestedSource}"]`);
  if (sourceInput) sourceInput.checked = true;
  updateStaticNATIP("create");
  updateCreateSourceUI();
  updateCreateStorageUI();
  updateNetworkModeFields();
  updateGraphicsFlagFields("create");
  renderCreateLocalOptions();
  renderCreateCloudOptions();
  let directSelection = false;
  if (directLocalRootfsPath) {
    const localSelect = $("#createLocalRootfs");
    if (localSelect && Array.from(localSelect.options).some((option) => option.value === directLocalRootfsPath)) {
      localSelect.value = directLocalRootfsPath;
      handleCreateTemplateControlChange("local");
      directSelection = true;
    } else {
      toast(uiText("所选本地模板已不存在，请重新选择", "The selected local template no longer exists. Select another template."));
    }
  }
  setCreateTemplateSelectionLocked(directSelection);
  updateDeadlockControls("create");
  updateUserNamespaceControls("create");
  updateCreateFormValidation();
  setTimeout(() => {
    if (directSelection) {
      $("#createName")?.focus();
      return;
    }
    (requestedSource === "cloud" ? $("#createCloudRootfsSearch") : $("#createLocalRootfsSearch"))?.focus();
  }, 0);
}

function renderRuntimeVersions() {
  const data = state.status || {};
  const loading = uiText("读取中", "Loading");
  const webVersion = data.webVersion || loading;
  const headerVersion = $("#webVersionLabel");
  if (headerVersion) {
    headerVersion.textContent = formatWebVersionLabel(webVersion);
    headerVersion.title = `WebUI ${webVersion}`;
  }
  const node = $("#createVersionHint");
  if (!node) return;
  node.textContent = uiText(
    `WebUI: ${webVersion} · 当前核心: ${data.coreVersion || loading} · 适配核心: ${data.supportedCoreVersion || loading}`,
    `WebUI: ${webVersion} · Current Core: ${data.coreVersion || loading} · Supported Core: ${data.supportedCoreVersion || loading}`,
  );
}

function formatWebVersionLabel(rawVersion) {
  const version = String(rawVersion || uiText("读取中", "Loading")).trim();
  const buildMatch = version.match(/^(.+?)\+build\.(\d{8})T(\d{6})\d*Z$/);
  if (!buildMatch) return `WebUI ${version}`;

  const [, sourceVersion, date, time] = buildMatch;
  const dirty = /(?:^|[-.])dirty(?:$|[-.])/.test(sourceVersion);
  const sourceLabel = sourceVersion.replace(/(?:[-.])dirty(?:$|[-.])/, "").slice(0, 14) || "dev";
  const timestamp = `${date.slice(4, 6)}-${date.slice(6, 8)} ${time.slice(0, 2)}:${time.slice(2, 4)}:${time.slice(4, 6)}`;
  return `WebUI ${sourceLabel}${dirty ? "*" : ""} · ${timestamp}`;
}

function hideCreateModal() {
  $("#createModal").classList.add("hidden");
}

async function loadSystemSettings(showBusy = true) {
  if (showBusy) setBusy(true);
  const initialLoad = !state.systemSettingsLoaded;
  const languageSaveVersion = state.uiLanguageSaveVersion;
  const languageSaveCompletedVersion = state.uiLanguageSaveCompletedVersion;
  const settingsWriteVersion = state.settingsWriteVersion;
  const settingsWriteCompletedVersion = state.settingsWriteCompletedVersion;
  try {
    const data = (await api("/api/settings")) || {};
    if (
      state.uiLanguageSavePending
      || languageSaveVersion !== state.uiLanguageSaveVersion
      || languageSaveCompletedVersion !== state.uiLanguageSaveCompletedVersion
      || state.settingsWritePending
      || settingsWriteVersion !== state.settingsWriteVersion
      || settingsWriteCompletedVersion !== state.settingsWriteCompletedVersion
    ) {
      if (initialLoad && !state.systemSettingsSaving) {
        const selectedLanguage = state.pendingInitialUILanguage
          || window.DS_I18N?.getLocale?.()
          || state.systemSettings.uiLanguage
          || data.uiLanguage
          || "zh-CN";
        const settings = {
          ...data,
          uiLanguage: selectedLanguage,
          uiLanguageConfigured: true,
        };
        state.systemSettings = settings;
        state.rootfsRepositories = data.rootfsRepositories || state.rootfsRepositories || [];
        state.systemSettingsLoaded = true;
        renderSystemSettings(settings);
      }
      return state.systemSettings;
    }
    state.systemSettings = data || {};
    state.rootfsRepositories = data.rootfsRepositories || state.rootfsRepositories || [];
    state.systemSettingsLoaded = true;
    if (data?.uiLanguageConfigured) {
      state.confirmedUILanguage = data.uiLanguage || state.confirmedUILanguage || "zh-CN";
      window.DS_UI_LANGUAGE_DEFAULT = state.confirmedUILanguage;
      window.DS_UI_LANGUAGE_CONFIGURED = true;
      window.DS_I18N?.setInitialLocaleSetupRequired?.(false);
    }
    renderSystemSettings(data || {});
  } catch (err) {
    const status = $("#settingsStatus");
    if (status) status.textContent = err.message;
    toast(err.message);
  } finally {
    if (showBusy) setBusy(false);
  }
}

function setInputValue(selector, value) {
  const node = $(selector);
  if (node) node.value = value ?? "";
}

function setChecked(selector, value) {
  const node = $(selector);
  if (node) node.checked = Boolean(value);
}

function configuredUILanguage() {
  return state.confirmedUILanguage || state.systemSettings.uiLanguage || window.DS_UI_LANGUAGE_DEFAULT || "zh-CN";
}

function beginSettingsWrite() {
  const version = state.settingsWriteVersion + 1;
  state.settingsWriteVersion = version;
  state.settingsWritePending = true;
  return version;
}

function completeSettingsWrite(version) {
  if (version !== state.settingsWriteVersion) return;
  state.settingsWriteCompletedVersion = version;
  state.settingsWritePending = false;
}

function failSettingsWrite(version) {
  if (version === state.settingsWriteVersion) state.settingsWritePending = false;
}

function enqueueSettingsSave(save) {
  const queuedSave = state.settingsSaveQueue.then(save, save);
  state.settingsSaveQueue = queuedSave.catch(() => {});
  return queuedSave;
}

async function persistUILanguageConfiguration(language) {
  const requested = language === "en" ? "en" : "zh-CN";
  const version = state.uiLanguageSaveVersion + 1;
  const settingsWriteVersion = beginSettingsWrite();
  state.uiLanguageSaveVersion = version;
  state.uiLanguageSavePending = true;
  const save = async () => {
    const data = await api("/api/settings/ui-language", {
      method: "PUT",
      body: JSON.stringify({ uiLanguage: requested }),
    });
    const savedLanguage = data.uiLanguage || requested;
    window.DS_UI_LANGUAGE_DEFAULT = savedLanguage;
    window.DS_UI_LANGUAGE_CONFIGURED = true;
    state.confirmedUILanguage = savedLanguage;
    state.systemSettings = {
      ...state.systemSettings,
      uiLanguage: savedLanguage,
      uiLanguageConfigured: true,
      rootfsRepositories: data.rootfsRepositories || state.rootfsRepositories,
    };
    if (Array.isArray(data.rootfsRepositories)) {
      state.rootfsRepositories = data.rootfsRepositories;
      state.rootfsAssets = [];
      state.rootfsErrors = [];
      state.rootfsAssetsLoaded = false;
      state.rootfsAssetsLoadedAt = 0;
      state.rootfsAssetsArchitecture = "";
      renderSettingsRepositories(data.rootfsRepositories);
      renderRepositories();
    }
    if (version === state.uiLanguageSaveVersion) {
      state.pendingInitialUILanguage = "";
      state.uiLanguageSaveCompletedVersion = version;
      state.uiLanguageSavePending = false;
      window.DS_I18N?.setInitialLocaleSetupRequired?.(false);
      setInputValue("#settingsUILanguage", savedLanguage);
    }
    completeSettingsWrite(settingsWriteVersion);
    return data;
  };
  const queuedSave = enqueueSettingsSave(save);
  try {
    return await queuedSave;
  } catch (err) {
    failSettingsWrite(settingsWriteVersion);
    if (version === state.uiLanguageSaveVersion) state.uiLanguageSavePending = false;
    throw err;
  }
}

async function updateSettingsUILanguage(language) {
  window.DS_I18N?.setLocale?.(language, { reload: false });
  const save = persistUILanguageConfiguration(language);
  const version = state.uiLanguageSaveVersion;
  try {
    await save;
    if (version === state.uiLanguageSaveVersion) toast(t("settings.uiLanguageSaved"));
  } catch (err) {
    if (version !== state.uiLanguageSaveVersion) return;
    const rollbackLanguage = configuredUILanguage();
    window.DS_I18N?.setLocale?.(rollbackLanguage, { reload: false });
    setInputValue("#settingsUILanguage", rollbackLanguage);
    throw err;
  }
}

async function savePendingInitialUILanguage() {
  const language = state.pendingInitialUILanguage;
  if (!language || !state.authenticated) return;
  if (state.initialUILanguageSavePromise) return state.initialUILanguageSavePromise;
  const save = (async () => {
    try {
      await persistUILanguageConfiguration(language);
    } catch (err) {
      window.DS_I18N?.setInitialLocaleSetupRequired?.(true);
      throw err;
    }
  })();
  state.initialUILanguageSavePromise = save;
  try {
    return await save;
  } finally {
    if (state.initialUILanguageSavePromise === save) state.initialUILanguageSavePromise = null;
  }
}

function updateSettingsModeUI() {
  const mode = $("#settingsMode")?.value || "local";
  const host = $("#settingsHost");
  if (!host) return;
  if (mode === "local") {
    host.value = "127.0.0.1";
    host.disabled = true;
  } else {
    host.disabled = false;
    if (!host.value || host.value === "127.0.0.1") host.value = "0.0.0.0";
  }
}

function overviewPowerEnabled(settings = state.systemSettings) {
  return settings?.overviewPowerEnabled !== false;
}

function batteryMonitoringEnabled(settings = state.systemSettings) {
  return settings?.batteryMonitoringEnabled !== false;
}

function renderBatteryMonitoringDisabled() {
  renderBatteryPower({ message: t("battery.monitoringDisabled") });
  const live = $("#batteryLiveOverview");
  if (live) live.innerHTML = `<div class="empty-state">${escapeHTML(t("battery.monitoringDisabled"))}</div>`;
}

function updateBatteryFeatureUI(settings = state.systemSettings) {
  const overviewPower = overviewPowerEnabled(settings);
  const monitoring = batteryMonitoringEnabled(settings);
  $("#overviewPowerMetric")?.classList.toggle("hidden", !overviewPower || !monitoring);
  $$('[data-battery-monitoring-nav]').forEach((button) => {
    button.classList.toggle("hidden", !monitoring);
    button.disabled = !monitoring;
    button.setAttribute("aria-hidden", String(!monitoring));
  });
  $("#batteryView")?.classList.toggle("hidden", !monitoring);
  [
    "#settingsBatteryStatsSampleSeconds",
    "#settingsBatteryStatsWriteMinutes",
    "#settingsBatteryStatsRetentionDays",
    "#settingsBatterySeriesCells",
    "#settingsBatteryDirectPower",
  ].forEach((selector) => {
    const input = $(selector);
    if (!input) return;
    input.disabled = !monitoring;
    input.closest("label")?.classList.toggle("disabled", !monitoring);
  });
  if (!monitoring) {
    state.batteryPower = { message: t("battery.monitoringDisabled") };
    renderBatteryMonitoringDisabled();
    if (state.currentView === "battery") switchView("overview", { silentBatteryRedirect: true });
  }
}

function updateBatterySettingsFormUI() {
  const monitoring = Boolean($("#settingsBatteryMonitoringEnabled")?.checked);
  [
    "#settingsBatteryStatsSampleSeconds",
    "#settingsBatteryStatsWriteMinutes",
    "#settingsBatteryStatsRetentionDays",
    "#settingsBatterySeriesCells",
    "#settingsBatteryDirectPower",
  ].forEach((selector) => {
    const input = $(selector);
    if (!input) return;
    input.disabled = !monitoring;
    input.closest("label")?.classList.toggle("disabled", !monitoring);
  });
}

function renderSystemSettings(data = state.systemSettings || {}) {
  if (!$("#settingsView")) return;
  data = {
    ...data,
    overviewPowerEnabled: data.overviewPowerEnabled !== false,
    batteryMonitoringEnabled: data.batteryMonitoringEnabled !== false,
  };
  state.systemSettings = data;
  setInputValue("#settingsMode", data.mode || "local");
  setInputValue("#settingsHost", data.host || "127.0.0.1");
  updateSettingsModeUI();
  setInputValue("#settingsPort", data.port || 9090);
  setInputValue("#settingsOverviewRefreshSeconds", data.overviewRefreshSeconds || DEFAULT_OVERVIEW_REFRESH_SECONDS);
  setInputValue("#settingsBatteryStatsSampleSeconds", data.batteryStatsSampleSeconds || DEFAULT_BATTERY_STATS_SAMPLE_SECONDS);
  setInputValue("#settingsBatteryStatsWriteMinutes", data.batteryStatsWriteMinutes || DEFAULT_BATTERY_STATS_WRITE_MINUTES);
  setInputValue("#settingsBatteryStatsRetentionDays", data.batteryStatsRetentionDays || DEFAULT_BATTERY_STATS_RETENTION_DAYS);
  setInputValue("#settingsBatterySeriesCells", data.batterySeriesCells ?? 0);
	setInputValue("#settingsUILanguage", data.uiLanguage || window.DS_I18N?.getDefaultLocale?.() || "zh-CN");
  setInputValue("#settingsAuthToken", data.authToken || "");
  setInputValue("#settingsDroidspacesPath", data.droidspacesPath || "");
  setInputValue("#settingsCorePath", data.corePath || "");
  setInputValue("#settingsTemplateImageRoot", data.templateImageRoot || "");
  setInputValue("#settingsWorkspace", data.workspace || "");
  setInputValue("#settingsConfigPath", data.configPath || "");
  setInputValue("#settingsDefaultNatThirdOctet", data.defaultNatThirdOctet || DEFAULT_NAT_THIRD_OCTET);
  setInputValue("#settingsDefaultNatCIDR", natAddressPoolText());
  updateNATPrefixLabels();
  setInputValue("#settingsNatGatewayIP", data.natGatewayIP || "172.28.0.1");
  setChecked("#settingsSocketdEnabled", data.socketdEnabled);
  setChecked("#settingsRootfsSkipTLSVerify", data.rootfsSkipTLSVerify);
	setChecked("#settingsNestedAndroidNatCompat", data.nestedAndroidNatCompat);
  setChecked("#settingsOverviewPowerEnabled", data.overviewPowerEnabled);
  setChecked("#settingsBatteryMonitoringEnabled", data.batteryMonitoringEnabled);
  setChecked("#settingsBatteryDirectPower", data.batteryDirectPowerSupported);
  const integration = data.integration || state.diagnostics.settings || {};
  setChecked("#settingsDaemonMode", integration.daemonMode);
  setChecked("#settingsSymlinkEnabled", integration.symlinkEnabled);
  renderSettingsIntegration(integration);
  renderSettingsRepositories(data.rootfsRepositories || state.rootfsRepositories || []);
  renderCoreUpdate();
  updateNetworkModeFields();
  updateBatteryFeatureUI(data);
  updateBatterySettingsFormUI();
  const status = $("#settingsStatus");
  if (status) {
    if (data.saved) status.textContent = data.restartRequired ? t("settings.savedRestartRequired") : t("settings.saved");
    else status.textContent = data.configPath ? t("settings.configPath", { path: data.configPath }) : t("settings.noConfigPath");
  }
}

function renderCoreUpdate(data = state.coreUpdate) {
  const node = $("#coreUpdateStatus");
  const action = $("#coreUpdateBtn");
  if (!node || !action) return;
  if (!data) {
    node.textContent = t("settings.notChecked");
    action.textContent = t("settings.downloadUpdate");
    return;
  }
  const current = data.currentVersion || state.status?.coreVersion || t("settings.unknown");
  const latest = data.latestVersion || t("settings.notFetched");
  const asset = data.assetName || data.asset?.name || "-";
  const architecture = data.architecture || data.arch || "-";
  const source = data.source || t("settings.officialRelease");
  const updateAvailable = data.updateAvailable;
  const stateText = data.status === "updating" ? t("settings.updating") : updateAvailable === false ? t("settings.upToDate") : updateAvailable === true ? t("settings.updateAvailable") : (data.message || t("settings.checkComplete"));
  const rows = [
    [t("settings.currentVersion"), current],
    [t("settings.latestVersion"), latest],
    [t("settings.status"), stateText],
    [t("settings.architecture"), architecture],
    [t("settings.releaseAsset"), asset],
    [t("settings.source"), source],
  ];
  node.innerHTML = rows.map(([key, value]) => `<div class="summary-row"><span>${escapeHTML(key)}</span><strong>${escapeHTML(String(value || "-"))}</strong></div>`).join("");
  action.textContent = updateAvailable === false ? t("settings.reinstall") : t("settings.downloadUpdate");
}

async function checkCoreUpdate(showBusy = true) {
  if (showBusy) setBusy(true);
  try {
    const data = await api("/api/core/update?refresh=1");
    state.coreUpdate = data;
    renderCoreUpdate();
    await loadStatus(true).catch(() => {});
    return data;
  } finally {
    if (showBusy) setBusy(false);
  }
}

async function startCoreUpdate() {
  const checked = state.coreUpdate || await checkCoreUpdate(false);
  const target = checked.latestVersion || t("settings.latestOfficialVersion");
  if (!window.confirm(t("settings.updateConfirm", { target }))) return;
  setBusy(true);
  try {
    const data = await api("/api/core/update", { method: "POST", body: JSON.stringify({}) });
    state.coreUpdate = { ...(state.coreUpdate || {}), status: "updating" };
    renderCoreUpdate();
    if (data.task) state.tasks[data.task.id] = data.task;
    trackTask(data.taskId, async () => {
      await Promise.allSettled([loadStatus(true), checkCoreUpdate(false), loadTasks()]);
      toast(t("settings.updateComplete"));
    });
    openTaskPanel();
  } finally {
    setBusy(false);
  }
}

function renderSettingsIntegration(data = {}) {
  const node = $("#settingsIntegrationStatus");
  if (!node) return;
  const rows = [
    ["Daemon marker", data.daemonModeFile || "-"],
    ["Symlink", data.symlinkPath || "-"],
    ["Symlink target", data.symlinkTarget || "-"],
    ["Droidspaces", data.droidspacesPath || state.systemSettings.droidspacesPath || "-"],
  ];
  node.innerHTML = rows.map(([key, value]) => `<div class="summary-row"><span>${escapeHTML(key)}</span><strong class="mono">${escapeHTML(value)}</strong></div>`).join("");
}

function rootfsRepositoryRow(repository, index, settings = false) {
  const info = rootfsRepositoryInfo(repository);
  const nameAttribute = settings ? `data-settings-repo-name="${index}"` : `data-repo-name="${index}"`;
  const urlAttribute = settings ? `data-settings-repo-url="${index}"` : `data-repo-url="${index}"`;
  const removeAttribute = settings ? `data-settings-repo-remove="${index}"` : `data-repo-remove="${index}"`;
  const repositoryHelp = uiText("可填写 rootfs.json、lxc-image 镜像站或南京大学镜像站", "Use a rootfs.json URL, an lxc-image mirror, or the Nanjing University mirror");
  const repositoryPlaceholder = uiText("rootfs.json 或 https://images.linuxcontainers.org/", "rootfs.json or https://images.linuxcontainers.org/");
  return `<div class="repo-row"><input ${nameAttribute} type="text" placeholder="${uiText("名称", "Name")}" value="${escapeHTML(info.name)}" /><input ${urlAttribute} type="url" placeholder="${escapeHTML(repositoryPlaceholder)}" title="${escapeHTML(repositoryHelp)}" value="${escapeHTML(info.url)}" /><button class="icon-btn danger" type="button" ${removeAttribute} title="${uiText("删除仓库", "Delete Repository")}" aria-label="${uiText("删除仓库", "Delete Repository")}">×</button></div>`;
}

function isLinuxContainersRepositoryEntry(repository) {
  const url = rootfsRepositoryURL(repository);
  return isLinuxContainersOfficialRepositoryURL(url)
    || isLinuxContainersCNMirrorRepositoryURL(url);
}

function hasLinuxContainersCNMirrorRepository(repos) {
  return safeArray(repos).some((repo) => isLinuxContainersCNMirrorRepositoryURL(rootfsRepositoryURL(repo)));
}

// Keep a single lxc-image repository. Earlier WebUI versions added the
// Nanjing mirror as a second row, so normalize those saved settings on render.
function setLinuxContainersMirrorRepository(repos, enabled, addWhenMissing = false) {
  const items = [];
  let replaced = false;
  safeArray(repos).forEach((repository) => {
    const item = { name: repository?.name || repository?.Name || "", url: repository?.url || repository?.URL || "" };
    if (!isLinuxContainersRepositoryEntry(item)) {
      items.push(item);
      return;
    }
    if (replaced) return;
    items.push({
      name: LINUX_CONTAINERS_NAME,
      url: enabled ? LINUX_CONTAINERS_CN_MIRROR_URL : LINUX_CONTAINERS_OFFICIAL_URL,
    });
    replaced = true;
  });
  if (!replaced && addWhenMissing) items.push({ name: LINUX_CONTAINERS_NAME, url: enabled ? LINUX_CONTAINERS_CN_MIRROR_URL : LINUX_CONTAINERS_OFFICIAL_URL });
  return items;
}

function renderSettingsRepositoryPresets(repos) {
  const enabledInput = $("#settingsNjuMirrorEnabled");
  const status = $("#settingsNjuMirrorRepoStatus");
  if (!enabledInput || !status) return;
  const enabled = hasLinuxContainersCNMirrorRepository(repos);
  enabledInput.checked = enabled;
  status.textContent = enabled ? t("settings.njuMirror") : t("settings.officialSource");
  status.classList.toggle("running", enabled);
  status.classList.toggle("stopped", !enabled);
}

function renderSettingsRepositories(repos) {
  const wrap = $("#settingsRepoRows");
  if (!wrap) return;
  const rawItems = (repos && repos.length ? repos : [{ name: "Droidspaces Official", url: "" }]).map((repo) => ({ name: repo.name || repo.Name || "", url: repo.url || repo.URL || "" }));
  const items = setLinuxContainersMirrorRepository(rawItems, hasLinuxContainersCNMirrorRepository(rawItems));
  state.systemSettings.rootfsRepositories = items;
  wrap.innerHTML = items.map((repo, index) => rootfsRepositoryRow(repo, index, true)).join("");
  renderSettingsRepositoryPresets(items);
}

function collectSettingsRepositories() {
  const items = $$('[data-settings-repo-url]').map((input) => {
    const index = input.dataset.settingsRepoUrl;
    return { name: $(`[data-settings-repo-name="${index}"]`)?.value.trim() || "", url: input.value.trim() };
  }).filter((repo) => repo.url);
  return setLinuxContainersMirrorRepository(items, Boolean($("#settingsNjuMirrorEnabled")?.checked));
}

function collectSystemSettings() {
  const mode = $("#settingsMode")?.value || "local";
  return {
    mode,
    host: mode === "local" ? "127.0.0.1" : ($("#settingsHost")?.value.trim() || ""),
    port: Number($("#settingsPort")?.value || 0),
    overviewRefreshSeconds: Number($("#settingsOverviewRefreshSeconds")?.value || DEFAULT_OVERVIEW_REFRESH_SECONDS),
    batteryStatsSampleSeconds: Number($("#settingsBatteryStatsSampleSeconds")?.value || DEFAULT_BATTERY_STATS_SAMPLE_SECONDS),
    batteryStatsWriteMinutes: Number($("#settingsBatteryStatsWriteMinutes")?.value || DEFAULT_BATTERY_STATS_WRITE_MINUTES),
    batteryStatsRetentionDays: Number($("#settingsBatteryStatsRetentionDays")?.value || DEFAULT_BATTERY_STATS_RETENTION_DAYS),
    authToken: $("#settingsAuthToken")?.value.trim() || "",
    uiLanguage: $("#settingsUILanguage")?.value || "zh-CN",
    droidspacesPath: $("#settingsDroidspacesPath")?.value.trim() || "",
    corePath: $("#settingsCorePath")?.value.trim() || "",
    // imageRoot remains a backend compatibility setting. It has no editable UI
    // because downloaded templates now use templateImageRoot exclusively.
    imageRoot: state.systemSettings.imageRoot || "",
    templateImageRoot: $("#settingsTemplateImageRoot")?.value.trim() || "",
    workspace: $("#settingsWorkspace")?.value.trim() || "",
    socketdEnabled: $("#settingsSocketdEnabled")?.checked || false,
    rootfsSkipTLSVerify: $("#settingsRootfsSkipTLSVerify")?.checked || false,
    overviewPowerEnabled: $("#settingsOverviewPowerEnabled")?.checked !== false,
    batteryMonitoringEnabled: $("#settingsBatteryMonitoringEnabled")?.checked !== false,
    batteryDirectPowerSupported: $("#settingsBatteryDirectPower")?.checked || false,
    // Retain this legacy setting when saving unrelated system settings. Detailed
    // battery information now lives in Battery Monitoring instead of the home card.
    batteryDetailEnabled: state.systemSettings.batteryDetailEnabled !== false,
    batterySeriesCells: Number($("#settingsBatterySeriesCells")?.value || 0),
    defaultNatThirdOctet: Number($("#settingsDefaultNatThirdOctet")?.value || DEFAULT_NAT_THIRD_OCTET),
    defaultNatCIDR: state.systemSettings.defaultNatCIDR || state.networkSettings.defaultNatCIDR || "172.28.0.0/16",
	nestedAndroidNatCompat: $("#settingsNestedAndroidNatCompat")?.checked || false,
    rootfsRepositories: collectSettingsRepositories(),
  };
}

async function saveSystemSettingsFromForm() {
  if (state.systemSettingsSaving) return;
  const payload = collectSystemSettings();
  if (!payload.rootfsRepositories.length) {
    throw new Error(uiText("至少保留一个云端镜像仓库", "Keep at least one cloud image repository"));
  }
  const settingsWriteVersion = beginSettingsWrite();
  state.systemSettingsSaving = true;
  setBusy(true);
  try {
    const data = await enqueueSettingsSave(() => api("/api/settings", { method: "PUT", body: JSON.stringify(payload) }));
    const savedLanguage = data.uiLanguage || payload.uiLanguage;
    window.DS_UI_LANGUAGE_DEFAULT = savedLanguage;
    window.DS_UI_LANGUAGE_CONFIGURED = true;
    state.confirmedUILanguage = savedLanguage;
    state.pendingInitialUILanguage = "";
    window.DS_I18N?.setInitialLocaleSetupRequired?.(false);
    state.systemSettings = data;
    state.rootfsRepositories = data.rootfsRepositories || payload.rootfsRepositories;
    state.rootfsAssets = [];
    state.rootfsErrors = [];
    state.rootfsAssetsLoaded = false;
    state.rootfsAssetsLoadedAt = 0;
    state.rootfsAssetsArchitecture = "";
    state.networkSettings = { ...state.networkSettings, defaultNatCIDR: data.defaultNatCIDR || payload.defaultNatCIDR, defaultNatThirdOctet: data.defaultNatThirdOctet || payload.defaultNatThirdOctet, natGatewayIP: data.natGatewayIP || "172.28.0.1" };
    setAuthToken(payload.authToken);
    renderSystemSettings(data);
    updateNATPrefixLabels();
    restartOverviewRefreshTimer();
    renderRepositories();
    renderNetworkSettings();
    completeSettingsWrite(settingsWriteVersion);
    toast(data.restartRequired ? uiText("设置已保存，监听变更重启后生效", "Settings saved. Listen changes take effect after restart.") : uiText("系统设置已保存", "System settings saved"));
    await loadStatus().catch(() => {});
  } catch (err) {
    failSettingsWrite(settingsWriteVersion);
    throw err;
  } finally {
    state.systemSettingsSaving = false;
    setBusy(false);
  }
}

function rootfsAssetsMemoryCacheFresh(arch) {
  const age = Date.now() - Number(state.rootfsAssetsLoadedAt || 0);
  return state.rootfsAssetsLoaded
    && state.rootfsAssetsArchitecture === arch
    && age >= 0
    && age < ROOTFS_ASSET_MEMORY_CACHE_MS;
}

async function loadRootfsAssets({ forceRefresh = false } = {}) {
  const arch = $("#rootfsArch")?.value || "";
  if (state.rootfsLoading) return;
  if (!forceRefresh && rootfsAssetsMemoryCacheFresh(arch)) {
    renderRootfsAssets(state.rootfsAssets, state.rootfsErrors);
    return;
  }

  setRootfsLoading(true);
  const list = $("#rootfsList");
  list.innerHTML = `<div class="empty-state">${uiText("加载中", "Loading")}</div>`;
  try {
    const params = new URLSearchParams();
    if (arch) params.set("arch", arch);
    if (forceRefresh) params.set("refresh", "1");
    const query = params.toString();
    const data = await api(`/api/rootfs${query ? `?${query}` : ""}`);
    state.rootfsAssets = data.assets || [];
    state.rootfsErrors = data.errors || [];
    state.rootfsAssetsLoaded = state.rootfsAssets.length > 0 || state.rootfsErrors.length === 0;
    state.rootfsAssetsLoadedAt = state.rootfsAssetsLoaded ? Date.now() : 0;
    state.rootfsAssetsArchitecture = arch;
    state.rootfsRepositories = data.repositories || state.rootfsRepositories || [];
    renderRepositories();
    renderRootfsAssets(state.rootfsAssets, state.rootfsErrors);
    renderCreateCloudOptions();
  } catch (err) {
    state.rootfsAssetsLoaded = false;
    state.rootfsAssetsLoadedAt = 0;
    state.rootfsErrors = [err.message];
    list.innerHTML = `<div class="empty-state">${escapeHTML(err.message)}</div>`;
    toast(err.message);
  } finally {
    setRootfsLoading(false);
  }
}

function renderRepositories() {
  const wrap = $("#repoRows");
  if (!wrap) return;
  const repos = state.rootfsRepositories.length ? state.rootfsRepositories : [{ name: "Droidspaces Official", url: "" }];
  wrap.innerHTML = repos.map((repo, index) => rootfsRepositoryRow(repo, index)).join("");
}

function setRootfsRepositoryEditorOpen(open) {
  state.rootfsRepositoryEditorOpen = Boolean(open);
  $("#repoEditorPanel")?.classList.toggle("hidden", !state.rootfsRepositoryEditorOpen);
  $("#repoManageBtn")?.setAttribute("aria-expanded", String(state.rootfsRepositoryEditorOpen));
  if (state.rootfsRepositoryEditorOpen) renderRepositories();
}

async function saveRepositories() {
  const repos = $$("[data-repo-url]").map((input) => {
    const index = input.dataset.repoUrl;
    return { name: $(`[data-repo-name="${index}"]`)?.value.trim() || "", url: input.value.trim() };
  }).filter((repo) => repo.url);
  const data = await api("/api/rootfs/repositories", { method: "PUT", body: JSON.stringify({ repositories: repos }) });
  state.rootfsRepositories = data.repositories || repos;
  state.rootfsAssetsLoaded = false;
  state.rootfsAssetsLoadedAt = 0;
  renderRepositories();
  toast(uiText("仓库已保存", "Repositories saved"));
  await loadRootfsAssets({ forceRefresh: true });
}

function rootfsAssetSources(assets) {
  return [...new Set(assets.map(rootfsAssetSource))].sort((left, right) => {
    const leftLinuxContainers = isLinuxContainersSource(left);
    const rightLinuxContainers = isLinuxContainersSource(right);
    if (leftLinuxContainers !== rightLinuxContainers) return leftLinuxContainers ? -1 : 1;
    return left.localeCompare(right, "zh-CN");
  });
}

function rootfsRemoteSearchText(asset) {
  return rootfsSystemVersion(asset).toLocaleLowerCase("zh-CN");
}

function filteredRootfsAssets(assets) {
  const sourceFiltered = state.rootfsSourceFilter
    ? assets.filter((asset) => rootfsAssetSource(asset) === state.rootfsSourceFilter)
    : assets;
  const query = String($("#rootfsRemoteSearch")?.value || "").trim().toLocaleLowerCase("zh-CN");
  if (!query) return sourceFiltered;
  const terms = query.split(/\s+/).filter(Boolean);
  return sourceFiltered.filter((asset) => {
    const text = rootfsRemoteSearchText(asset);
    return terms.every((term) => text.includes(term));
  });
}

function rootfsSourceBadgeClass(source) {
  return isLinuxContainersSource(source) ? "lxc-image" : "";
}

function renderRootfsSourceFilter(assets) {
  const select = $("#rootfsSourceFilter");
  if (!select) return;
  const sources = rootfsAssetSources(assets);
  if (state.rootfsSourceFilter && !sources.includes(state.rootfsSourceFilter)) state.rootfsSourceFilter = "";
  select.innerHTML = [`<option value="">${uiText(`全部镜像（${assets.length}）`, `All Images (${assets.length})`)}</option>`, ...sources.map((source) => {
    const count = assets.filter((asset) => rootfsAssetSource(asset) === source).length;
    return `<option value="${escapeHTML(source)}">${escapeHTML(source)}${uiText(`（${count}）`, ` (${count})`)}</option>`;
  })].join("");
  select.value = state.rootfsSourceFilter;
  select.disabled = sources.length === 0;
}

function renderRootfsAssets(assets, errors = []) {
  const list = $("#rootfsList");
  const visibleAssets = cloudRootfsAssetsForSelection(assets);
  renderRootfsSourceFilter(visibleAssets);
  const filteredAssets = filteredRootfsAssets(visibleAssets);
  if (!filteredAssets.length) {
    const hasSearch = Boolean(String($("#rootfsRemoteSearch")?.value || "").trim());
    const emptyText = visibleAssets.length
      ? (hasSearch ? uiText("没有匹配的云端镜像", "No matching cloud images") : uiText("当前来源没有可用镜像", "No images are available from this source"))
      : (errors.length ? escapeHTML(errors.join("\n")) : uiText("暂无可用镜像", "No cloud images available"));
    list.innerHTML = `<div class="empty-state">${emptyText}</div>`;
    return;
  }
  list.innerHTML = filteredAssets.map((asset) => {
    const encoded = encodeURIComponent(JSON.stringify(asset));
    const source = rootfsAssetSource(asset);
    const title = rootfsDisplayName(asset);
    const variant = rootfsCloudVariant(asset);
    const buildDate = formatRootfsBuildDate(asset.buildDate);
    const description = rootfsAssetDescription(asset);
    const descriptionMarkup = description ? `<p class="rootfs-desc rootfs-cloud-description" title="${escapeHTML(description)}">${escapeHTML(description)}</p>` : "";
    return `<article class="rootfs-item rootfs-cloud-item"><div class="rootfs-cloud-primary"><div class="rootfs-template-title">${rootfsDistroIcon(asset)}<h3>${escapeHTML(title)}</h3></div><span class="badge rootfs-cloud-chip" title="${uiText("架构", "Architecture")}">${escapeHTML(asset.architecture || "-")}</span><span class="badge rootfs-cloud-chip" title="${uiText("变体", "Variant")}">${escapeHTML(variant)}</span></div>${descriptionMarkup}<div class="rootfs-cloud-source-row"><span class="rootfs-cloud-label">${uiText("镜像来源", "Image Source")}</span><span class="badge rootfs-source-badge ${rootfsSourceBadgeClass(source)}">${escapeHTML(source)}</span>${rootfsSupportBadge(asset)}</div><div class="rootfs-cloud-footer"><span class="rootfs-cloud-stat"><small>${uiText("系统包大小", "Package Size")}</small><strong>${fmtSize(asset.sizeBytes)}</strong></span><span class="rootfs-cloud-stat"><small>${uiText("系统包日期时间", "Package Date")}</small><strong>${escapeHTML(buildDate)}</strong></span><button class="text-btn primary" data-rootfs="${encoded}">${uiText("下载", "Download")}</button></div></article>`;
  }).join("");
  if (errors.length) toast(errors.join("\n"));
}

function setDownloadSubmissionState(button, submittingLabel = uiText("正在提交...", "Submitting...")) {
  if (!button) return () => {};
  const original = {
    disabled: button.disabled,
    label: button.textContent,
    ariaBusy: button.getAttribute("aria-busy"),
  };
  button.disabled = true;
  button.textContent = submittingLabel;
  button.setAttribute("aria-busy", "true");
  return () => {
    button.disabled = original.disabled;
    button.textContent = original.label;
    if (original.ariaBusy === null) button.removeAttribute("aria-busy");
    else button.setAttribute("aria-busy", original.ariaBusy);
  };
}

async function downloadRootfs(asset, button) {
  if (button?.disabled) return;
  const restoreButton = setDownloadSubmissionState(button);
  toast(uiText("正在校验镜像来源并提交下载任务...", "Validating the image source and submitting the download..."), 8000);
  setBusy(true);
  try {
    const data = await api("/api/rootfs/download", { method: "POST", body: JSON.stringify(asset) });
    trackTask(data.taskId);
    const completed = String(data.task?.status || "").toLowerCase() === "done";
    if (!completed) openTaskPanel();
    toast(completed ? uiText("已复用已下载的镜像", "Using the downloaded image") : data.shared ? uiText("已复用正在进行的下载", "Using the in-progress download") : uiText("已开始下载", "Download started"));
  } catch (err) {
    toast(err.message);
  } finally {
    restoreButton();
    setBusy(false);
  }
}

async function uploadLocalRootfs() {
  const input = $("#rootfsUploadFile");
  const file = input?.files?.[0];
  if (!file) { toast(uiText("请选择要上传的镜像模板", "Select an image template to upload")); return; }
  const form = new FormData();
  form.append("file", file);
  setBusy(true);
  try {
    const token = getAuthToken();
    const response = await fetch("/api/rootfs/local/upload", { method: "POST", headers: token ? { Authorization: `Bearer ${token}` } : {}, body: form });
    const data = await response.json().catch(() => ({}));
    if (!response.ok) throw new Error(data.error || `HTTP ${response.status}`);
    input.value = "";
    toast(uiText("模板已上传", "Template uploaded"));
    await loadLocalRootfs();
  } catch (err) {
    toast(err.message);
  } finally {
    setBusy(false);
  }
}

async function loadLocalRootfs() {
  try {
    const data = await api("/api/rootfs/local");
    state.localRootfs = data.items || [];
    $("#localRootfsCount").textContent = state.localRootfs.length;
    renderLocalRootfs();
    renderCreateLocalOptions();
  } catch (err) {
    const message = `<div class="empty-state">${escapeHTML(err.message)}</div>`;
    $("#localRootfsList").innerHTML = message;
    $("#backupRootfsList").innerHTML = message;
  }
}

function renderLocalRootfs() {
  renderRootfsItems("#localRootfsList", state.localRootfs.filter((item) => item.kind !== "backup"), uiText("暂无本地模板", "No local templates"));
  renderRootfsItems("#backupRootfsList", state.localRootfs.filter((item) => item.kind === "backup"), uiText("暂无备份导出", "No exported backups"));
}

function renderRootfsItems(selector, items, emptyText) {
  const list = $(selector);
  if (!list) return;
  if (!items.length) {
    list.innerHTML = `<div class="empty-state">${escapeHTML(emptyText)}</div>`;
    return;
  }
  list.innerHTML = items.map((item) => {
    const title = rootfsSystemVersion(item);
    const architecture = localRootfsArchitecture(item);
    const variant = localRootfsVariant(item);
    const canDownload = item.kind === "archive" || item.kind === "backup" || item.kind === "image";
    const downloadURL = `/api/rootfs/local/download?path=${encodeURIComponent(item.path)}`;
    const download = canDownload ? `<button class="text-btn" data-download-url="${escapeHTML(downloadURL)}" data-download-name="${escapeHTML(title)}">${uiText("下载", "Download")}</button>` : "";
    const remove = canDownload ? `<button class="text-btn danger" data-delete-rootfs="${escapeHTML(item.path)}" data-delete-rootfs-name="${escapeHTML(title)}">${uiText("删除", "Delete")}</button>` : "";
    const create = item.kind === "backup" ? "" : `<button class="text-btn primary rootfs-local-create" data-use-local-rootfs="${escapeHTML(item.path)}">${uiText("创建容器", "Create Container")}</button>`;
    const source = localRootfsSource(item);
    const actions = download || remove ? `<div class="rootfs-local-actions">${download}${remove}</div>` : "<span></span>";
    return `<article class="rootfs-item rootfs-local-item"><div class="rootfs-cloud-primary"><div class="rootfs-template-title">${rootfsDistroIcon(item)}<h3>${escapeHTML(title)}</h3></div><span class="badge rootfs-cloud-chip" title="${uiText("架构", "Architecture")}">${escapeHTML(architecture)}</span><span class="badge rootfs-cloud-chip" title="${uiText("变体", "Variant")}">${escapeHTML(variant)}</span></div><div class="rootfs-cloud-source-row"><span class="rootfs-cloud-label">${uiText("镜像来源", "Image Source")}</span><span class="badge rootfs-local-source">${escapeHTML(source)}</span>${localRootfsSupportBadge(item)}${create}</div><div class="rootfs-cloud-footer"><span class="rootfs-cloud-stat"><small>${uiText("系统包大小", "Package Size")}</small><strong>${fmtSize(item.size)}</strong></span><span class="rootfs-cloud-stat"><small>${uiText("系统包日期时间", "Package Date")}</small><strong>${fmtTime(item.modified)}</strong></span>${actions}</div></article>`;
  }).join("");
}

function kindText(kind) {
  const labels = { directory: uiText("目录", "Directory"), image: uiText("镜像", "Image"), archive: uiText("压缩包", "Archive"), backup: uiText("备份", "Backup") };
  return labels[kind] || kind || uiText("未知", "Unknown");
}

function createTemplateSource() {
  return document.querySelector('input[name="createSource"]:checked')?.value || "local";
}

function selectedCreateLocalRootfs() {
  const path = $("#createLocalRootfs")?.value || "";
  return state.localRootfs.find((item) => item.path === path && item.kind !== "backup") || null;
}

function localRootfsHasOfficialSupport(item) {
  const source = localRootfsSource(item).toLowerCase();
  return source.includes("droidspaces") && source.includes("official");
}

function createTemplateVariantInfo(variant) {
  const value = String(variant || "").trim() || uiText("标准", "Standard");
  switch (value.toLowerCase()) {
    case "default":
      return { value, label: uiText("default · 标准", "default · Standard"), title: uiText("default：常规容器根文件系统", "default: regular container root filesystem") };
    case "cloud":
      return { value, label: "cloud · cloud-init", title: uiText("cloud：包含 cloud-init 的云初始化镜像", "cloud: cloud-init image") };
    case "tinycloud":
      return { value, label: "tinycloud · Incus", title: uiText("tinycloud：面向 Incus 的轻量初始化镜像", "tinycloud: lightweight Incus initialization image") };
    default:
      return { value, label: value, title: uiText(`变体：${value}`, `Variant: ${value}`) };
  }
}

function createTemplateDetails(item, source) {
  const cloud = source === "cloud";
  const variant = cloud ? rootfsCloudVariant(item) : localRootfsVariant(item);
  return {
    title: rootfsSystemVersion(item),
    architecture: String(cloud ? (item?.architecture || "-") : localRootfsArchitecture(item)).trim() || "-",
    variant: createTemplateVariantInfo(variant),
    repository: cloud ? rootfsAssetSource(item) : localRootfsSource(item),
    official: cloud ? rootfsHasOfficialSupport(item) : localRootfsHasOfficialSupport(item),
    size: cloud ? fmtSize(item?.sizeBytes) : fmtSize(item?.size),
    buildDate: cloud ? formatRootfsBuildDate(item?.buildDate) : fmtTime(item?.modified),
  };
}

function createTemplateSupportsCloudInit(item, source = createTemplateSource()) {
  if (!item) return false;
  const variant = source === "cloud" ? rootfsCloudVariant(item) : localRootfsVariant(item);
  return String(variant || "").trim().toLowerCase() === "cloud";
}

function createCloudInitUserDataMode() {
  return document.querySelector('input[name="createCloudInitUserDataMode"]:checked')?.value || "guided";
}

function cloudInitStringList(value) {
  return String(value || "").split(/\r?\n/).map((item) => item.trim()).filter(Boolean);
}

function cloudInitPackageList(value) {
  return String(value || "").split(/[\n,]+/).map((item) => item.trim()).filter(Boolean);
}

function cloudInitYAMLString(value) {
  return JSON.stringify(String(value));
}

function generateCloudInitRandomPassword(length = CLOUD_INIT_RANDOM_PASSWORD_LENGTH) {
  const alphabet = CLOUD_INIT_RANDOM_PASSWORD_ALPHABET;
  const limit = 256 - (256 % alphabet.length);
  let password = "";
  while (password.length < length) {
    const bytes = new Uint8Array(Math.max(16, length * 2));
    if (globalThis.crypto?.getRandomValues) {
      globalThis.crypto.getRandomValues(bytes);
    } else {
      for (let index = 0; index < bytes.length; index += 1) bytes[index] = Math.floor(Math.random() * 256);
    }
    for (const byte of bytes) {
      if (byte >= limit) continue;
      password += alphabet[byte % alphabet.length];
      if (password.length === length) break;
    }
  }
  return password;
}

function updateCreateCloudInitPasswordVisibility() {
  const password = $("#createCloudInitPassword");
  const control = $("#createCloudInitPasswordVisibility");
  if (!password || !control) return;
  const visible = password.type === "text";
  control.title = t(visible ? "create.hidePassword" : "create.showPassword");
  control.setAttribute("aria-label", control.title);
  control.setAttribute("aria-pressed", String(visible));
}

function setCreateCloudInitGeneratedPassword() {
  const password = $("#createCloudInitPassword");
  if (!password) return;
  password.value = generateCloudInitRandomPassword();
  password.dataset.generated = "true";
  password.type = "password";
  updateCreateCloudInitPasswordVisibility();
}

function resetCreateCloudInitFields() {
  const panel = $("#createCloudInitField");
  const enabled = $("#createCloudInitEnabled");
  const username = $("#createCloudInitUsername");
  const sshEnabled = $("#createCloudInitSSHEnabled");
  const sshPort = $("#createCloudInitSSHPort");
  const sshPortField = $("#createCloudInitSSHPortField");
  const rootSSH = $("#createCloudInitRootSSH");
  const sudo = $("#createCloudInitSudo");
  const sshKeys = $("#createCloudInitSSHKeys");
  const packages = $("#createCloudInitPackages");
  const commands = $("#createCloudInitCommands");
  const userData = $("#createCloudInitUserData");
  const networkConfig = $("#createCloudInitNetworkConfig");
  if (panel) panel.dataset.templateKey = "";
  if (enabled) enabled.checked = true;
  if (username) username.value = "root";
  setCreateCloudInitGeneratedPassword();
  if (sshEnabled) {
    sshEnabled.checked = false;
    sshEnabled.setAttribute("aria-expanded", "false");
  }
  if (sshPort) {
    sshPort.value = "22";
    sshPort.disabled = true;
  }
  if (sshPortField) sshPortField.classList.add("hidden");
  if (rootSSH) rootSSH.checked = false;
  if (sudo) sudo.checked = true;
  if (sshKeys) sshKeys.value = "";
  if (packages) packages.value = "";
  if (commands) commands.value = "";
  if (userData) {
    userData.value = "";
    userData.dataset.advancedValue = "";
    userData.dataset.mode = "guided";
  }
  if (networkConfig) networkConfig.value = "";
  const guidedMode = document.querySelector('input[name="createCloudInitUserDataMode"][value="guided"]');
  if (guidedMode) guidedMode.checked = true;
}

function createCloudInitPasswordNotice() {
  if (createCloudInitUserDataMode() !== "guided") return null;
  const username = String($("#createCloudInitUsername")?.value || "").trim();
  const password = $("#createCloudInitPassword");
  if (!username || !password?.value || password.dataset.generated !== "true") return null;
  return { username, password: password.value };
}

function validateCloudInitUserData(value) {
  if (String(value).includes("\0")) return { error: uiText("用户数据不能包含 NUL 字符", "User data cannot contain NUL characters") };
  if (new TextEncoder().encode(String(value)).length > CLOUD_INIT_MAX_DOCUMENT_BYTES) {
    return { error: uiText("用户数据不能超过 64 KiB", "User data cannot exceed 64 KiB") };
  }
  return { value: String(value) };
}

function cloudInitSSHPortResult() {
  const raw = String($("#createCloudInitSSHPort")?.value || "").trim();
  if (!/^\d+$/.test(raw)) return { error: uiText("SSH 端口必须是 1 到 65535 之间的整数", "SSH port must be an integer from 1 to 65535") };
  const value = Number(raw);
  if (!Number.isSafeInteger(value) || value < 1 || value > 65535) {
    return { error: uiText("SSH 端口必须是 1 到 65535 之间的整数", "SSH port must be an integer from 1 to 65535") };
  }
  return { value };
}

function createCloudInitSSHSettings(username, password) {
  const rootSSH = Boolean($("#createCloudInitRootSSH")?.checked);
  const enabled = Boolean($("#createCloudInitSSHEnabled")?.checked) || rootSSH;
  if (!enabled) return { enabled: false };
  if (rootSSH && username !== "root") {
    return { error: uiText("root 用户 SSH 远程管理需要将初始化用户名设为 root", "Root SSH remote access requires the initial username to be root") };
  }
  if (rootSSH && !password) return { error: uiText("启用 root 用户 SSH 远程管理需要设置 root 登录密码", "Root SSH remote access requires a root login password") };
  const port = cloudInitSSHPortResult();
  if (port.error) return port;
  return {
    enabled: true,
    port: port.value,
    passwordAuthentication: Boolean(password),
    rootPasswordLogin: rootSSH,
  };
}

function createCloudInitSSHServiceCommand(settings) {
  const sshConfigLines = [`Port ${settings.port}`];
  if (settings.passwordAuthentication) sshConfigLines.push("PasswordAuthentication yes");
  if (settings.rootPasswordLogin) sshConfigLines.push("PermitRootLogin yes");
  const configValues = sshConfigLines.map((line) => cloudInitYAMLString(line)).join(" ");
  const installServer = "if command -v apt-get >/dev/null 2>&1; then export DEBIAN_FRONTEND=noninteractive; apt-get update && apt-get install -y openssh-server; "
    + "elif command -v dnf >/dev/null 2>&1; then dnf install -y openssh-server; "
    + "elif command -v yum >/dev/null 2>&1; then yum install -y openssh-server; "
    + "elif command -v apk >/dev/null 2>&1; then apk add --no-cache openssh; "
    + "elif command -v pacman >/dev/null 2>&1; then pacman -Sy --noconfirm openssh; "
    + "elif command -v zypper >/dev/null 2>&1; then zypper --non-interactive install openssh; "
    + "elif command -v xbps-install >/dev/null 2>&1; then xbps-install -Sy openssh; "
    + "elif command -v emerge >/dev/null 2>&1; then emerge --noreplace net-misc/openssh; "
    + "else exit 1; fi";
  // Debian-family images may use systemd socket activation. Restarting only
  // ssh.service leaves the socket listening on its old port, so reload the
  // manager and restart whichever socket unit is available before falling back
  // to a traditional daemon service.
  const enableService = "if [ \"$(cat /proc/1/comm 2>/dev/null)\" = systemd ] && command -v systemctl >/dev/null 2>&1 && systemctl daemon-reload >/dev/null 2>&1; then "
    + "systemctl stop ssh.service sshd.service ssh.socket sshd.socket >/dev/null 2>&1 || true; "
    + "systemctl daemon-reload; "
    + "if systemctl restart ssh.socket >/dev/null 2>&1; then systemctl enable ssh.socket >/dev/null 2>&1 || true; "
    + "elif systemctl restart sshd.socket >/dev/null 2>&1; then systemctl enable sshd.socket >/dev/null 2>&1 || true; "
    + "elif systemctl enable --now ssh.service >/dev/null 2>&1; then systemctl restart ssh.service; "
    + "elif systemctl enable --now sshd.service >/dev/null 2>&1; then systemctl restart sshd.service; "
    + "else exit 1; fi; "
    + "elif command -v rc-service >/dev/null 2>&1; then "
    + "if command -v rc-update >/dev/null 2>&1; then rc-update add sshd default >/dev/null 2>&1 || rc-update add ssh default >/dev/null 2>&1 || true; fi; "
    + "if rc-service sshd restart >/dev/null 2>&1 || rc-service sshd start >/dev/null 2>&1 || rc-service ssh restart >/dev/null 2>&1 || rc-service ssh start >/dev/null 2>&1; then :; else exit 1; fi; "
    + "elif command -v service >/dev/null 2>&1; then "
    + "if service ssh restart >/dev/null 2>&1 || service sshd restart >/dev/null 2>&1 || service ssh start >/dev/null 2>&1 || service sshd start >/dev/null 2>&1; then :; else exit 1; fi; "
    + "else exit 1; fi";
  return [
    installServer,
    "mkdir -p /etc/ssh/sshd_config.d",
    "test -f /etc/ssh/sshd_config || : > /etc/ssh/sshd_config",
    `printf '%s\\n' ${configValues} > /etc/ssh/sshd_config.d/00-droidspaces-remote-management.conf`,
    "(grep -Fqx 'Include /etc/ssh/sshd_config.d/*.conf' /etc/ssh/sshd_config || sed -i '1i Include /etc/ssh/sshd_config.d/*.conf' /etc/ssh/sshd_config)",
    "if command -v ssh-keygen >/dev/null 2>&1; then ssh-keygen -A || true; fi",
    "if command -v sshd >/dev/null 2>&1; then sshd -t; fi",
    enableService,
  ].join(" && ");
}

function createCloudInitUserDataResult() {
  const mode = createCloudInitUserDataMode();
  const field = $("#createCloudInitUserData");
  if (mode === "advanced") return validateCloudInitUserData(field?.value || "");

  const username = String($("#createCloudInitUsername")?.value || "").trim();
  const password = String($("#createCloudInitPassword")?.value || "");
  const sshKeys = cloudInitStringList($("#createCloudInitSSHKeys")?.value);
  const packages = cloudInitPackageList($("#createCloudInitPackages")?.value);
  const commands = cloudInitStringList($("#createCloudInitCommands")?.value);
  if ((!username && (password || sshKeys.length)) || (username && !/^[a-z_][a-z0-9_-]*$/i.test(username))) {
    return { error: username ? uiText("用户名仅可使用字母、数字、下划线和连字符", "Username may contain only letters, numbers, underscores, and hyphens") : uiText("设置密码或 SSH 公钥时需要填写用户名", "A username is required when setting a password or SSH public keys") };
  }

  const sshSettings = createCloudInitSSHSettings(username, password);
  if (sshSettings.error) return sshSettings;
  if (sshSettings.enabled) {
    const sshServiceCommand = createCloudInitSSHServiceCommand(sshSettings);
    if (!commands.includes(sshServiceCommand)) commands.unshift(sshServiceCommand);
  }

  const hasCustomSettings = Boolean(username || packages.length || commands.length || sshSettings.enabled);
  if (!hasCustomSettings) {
    if (field) field.value = "";
    return { value: "" };
  }

  const hostname = String($("#createHostname")?.value || "").trim() || String($("#createName")?.value || "").trim();
  const lines = ["#cloud-config"];
  if (hostname) lines.push(`hostname: ${cloudInitYAMLString(hostname)}`, "manage_etc_hosts: true", "preserve_hostname: false");
  if (username) {
    lines.push("users:", "  - default", `  - name: ${cloudInitYAMLString(username)}`);
    if (username.toLowerCase() !== "root" && $("#createCloudInitSudo")?.checked) lines.push(`    sudo: ${cloudInitYAMLString("ALL=(ALL) NOPASSWD:ALL")}`);
    if (password) lines.push("    lock_passwd: false", `    plain_text_passwd: ${cloudInitYAMLString(password)}`);
    if (sshKeys.length) {
      lines.push("    ssh_authorized_keys:");
      sshKeys.forEach((key) => lines.push(`      - ${cloudInitYAMLString(key)}`));
    }
  }
  if (sshSettings.passwordAuthentication) lines.push("ssh_pwauth: true");
  if (packages.length) {
    lines.push("package_update: true", "packages:");
    packages.forEach((pkg) => lines.push(`  - ${cloudInitYAMLString(pkg)}`));
  }
  if (commands.length) {
    lines.push("runcmd:");
    commands.forEach((command) => lines.push(`  - ${cloudInitYAMLString(command)}`));
  }

  const result = `${lines.join("\n")}\n`;
  const validated = validateCloudInitUserData(result);
  if (!validated.error && field) field.value = validated.value;
  return validated;
}

function updateCreateCloudInitUserDataSummary() {
  const summary = $("#createCloudInitUserDataSummary");
  if (!summary) return;
  const enabledInput = $("#createCloudInitEnabled");
  if (!enabledInput?.checked || enabledInput.disabled) {
    summary.textContent = uiText("cloud-init 已关闭，创建时不会应用用户数据或网络配置。", "cloud-init is disabled, so user data and network configuration will not be applied.");
    return;
  }
  if (createCloudInitUserDataMode() === "advanced") {
    summary.textContent = $("#createCloudInitUserData")?.value.trim() ? uiText("将使用高级 YAML 覆盖引导配置。", "Advanced YAML will override the guided configuration.") : uiText("高级 YAML 未填写，将使用容器主机名完成初始化。", "No advanced YAML is configured; the container hostname will be used for initialization.");
    return;
  }

  const result = createCloudInitUserDataResult();
  if (result.error) {
    summary.textContent = result.error;
    return;
  }
  const items = [];
  const username = String($("#createCloudInitUsername")?.value || "").trim();
  if (username) items.push(uiText(`用户 ${username}`, `User ${username}`));
  const sshKeyCount = cloudInitStringList($("#createCloudInitSSHKeys")?.value).length;
  if (sshKeyCount) items.push(uiText(`${sshKeyCount} 条 SSH 公钥`, `${sshKeyCount} SSH public key${sshKeyCount === 1 ? "" : "s"}`));
  const packageCount = cloudInitPackageList($("#createCloudInitPackages")?.value).length;
  if (packageCount) items.push(uiText(`${packageCount} 个软件包`, `${packageCount} package${packageCount === 1 ? "" : "s"}`));
  const commandCount = cloudInitStringList($("#createCloudInitCommands")?.value).length;
  if (commandCount) items.push(uiText(`${commandCount} 条启动命令`, `${commandCount} startup command${commandCount === 1 ? "" : "s"}`));
  const sshEnabled = Boolean($("#createCloudInitSSHEnabled")?.checked);
  const rootSSH = Boolean($("#createCloudInitRootSSH")?.checked);
  if (sshEnabled) {
    const port = String($("#createCloudInitSSHPort")?.value || "22").trim() || "22";
    items.push(uiText(`SSH 远程管理（端口 ${port}）`, `SSH remote access (port ${port})`));
  }
  if (rootSSH && username === "root" && $("#createCloudInitPassword")?.value) {
    items.push(uiText("root SSH 密码登录", "Root SSH password login"));
  }
  summary.textContent = items.length ? uiText(`将配置${items.join("、")}。`, `Will configure ${items.join(", ")}.`) : uiText("未设置额外用户数据，将使用容器主机名完成初始化。", "No additional user data is configured; the container hostname will be used for initialization.");
}

function updateCreateCloudInitSSHControls(enabled, advanced) {
  const sshEnabled = $("#createCloudInitSSHEnabled");
  const sshPort = $("#createCloudInitSSHPort");
  const sshPortField = $("#createCloudInitSSHPortField");
  const rootSSH = $("#createCloudInitRootSSH");
  if (!sshEnabled || !sshPort || !sshPortField || !rootSSH) return;

  const formEnabled = enabled && !advanced;
  const username = String($("#createCloudInitUsername")?.value || "").trim();
  const hasRootPassword = username === "root" && Boolean($("#createCloudInitPassword")?.value);
  if (!hasRootPassword && rootSSH.checked) rootSSH.checked = false;
  if (rootSSH.checked) sshEnabled.checked = true;

  const remoteManagementEnabled = Boolean(sshEnabled.checked);
  const showPort = formEnabled && remoteManagementEnabled;
  sshEnabled.disabled = !formEnabled;
  sshPortField.classList.toggle("hidden", !showPort);
  sshPort.disabled = !showPort;
  rootSSH.disabled = !formEnabled || !hasRootPassword;
  sshEnabled.setAttribute("aria-expanded", String(showPort));
}

function updateCreateCloudInitUserDataUI() {
  const guided = $("#createCloudInitGuidedSettings");
  const advancedField = $("#createCloudInitAdvancedField");
  const advancedInput = $("#createCloudInitUserData");
  const enabledInput = $("#createCloudInitEnabled");
  if (!guided || !advancedField || !advancedInput || !enabledInput) return;

  const advanced = createCloudInitUserDataMode() === "advanced";
  const previousMode = advancedInput.dataset.mode || "guided";
  if (!advanced && previousMode === "advanced") {
    advancedInput.dataset.advancedValue = advancedInput.value;
  } else if (advanced && previousMode !== "advanced" && advancedInput.dataset.advancedValue !== undefined) {
    advancedInput.value = advancedInput.dataset.advancedValue;
  }
  advancedInput.dataset.mode = advanced ? "advanced" : "guided";
  if (advanced) advancedInput.dataset.advancedValue = advancedInput.value;

  const enabled = enabledInput.checked && !enabledInput.disabled;
  guided.classList.toggle("hidden", !enabled || advanced);
  advancedField.classList.toggle("hidden", !enabled || !advanced);
  $$('input[name="createCloudInitUserDataMode"]').forEach((input) => { input.disabled = !enabled; });
  guided.querySelectorAll("input, textarea, select, button").forEach((input) => { input.disabled = !enabled || advanced; });
  advancedInput.disabled = !enabled || !advanced;
  updateCreateCloudInitSSHControls(enabled, advanced);
  updateCreateCloudInitUserDataSummary();
  updateCreateFormValidation();
}

function createFormValidationError(selector, message, errors) {
  setCreateFieldError(selector, message);
  if (message) errors.push({ selector, message });
}

function updateCreateFormValidation() {
  const form = $("#createForm");
  if (!form) return [];
  const errors = [];
  const name = String($("#createName")?.value || "").trim();
  const nameHasNUL = name.includes(String.fromCharCode(0));
  if (!name) createFormValidationError("#createName", uiText("容器名称不能为空", "Container name is required"), errors);
  else if (name.length > 255 || /[\/\r\n]/.test(name) || nameHasNUL) createFormValidationError("#createName", uiText("名称不能超过 255 个字符，且不能包含 /、换行或 NUL", "Name cannot exceed 255 characters or contain /, line breaks, or NUL"), errors);
  else if (state.containers.some((container) => String(container.name || "").replaceAll(" ", "-") === name.replaceAll(" ", "-"))) createFormValidationError("#createName", uiText("该容器名称已经存在", "A container with this name already exists"), errors);
  else createFormValidationError("#createName", "", errors);

  const hostname = String($("#createHostname")?.value || "").trim();
  createFormValidationError("#createHostname", /[\r\n]/.test(hostname) || hostname.includes(String.fromCharCode(0)) ? uiText("主机名不能包含换行或 NUL 字符", "Hostname cannot contain line breaks or NUL characters") : "", errors);

  const source = createTemplateSource();
  const cloudAsset = source === "cloud" ? selectedCreateCloudAsset() : null;
  const localAsset = source === "local" ? selectedCreateLocalRootfs() : null;
  if (source === "cloud") {
    createFormValidationError("#createLocalRootfs", "", errors);
    createFormValidationError("#createCloudRootfs", cloudAsset?.downloadUrl ? "" : uiText("请选择可用的云端镜像", "Select an available cloud image"), errors);
  } else {
    createFormValidationError("#createCloudRootfs", "", errors);
    createFormValidationError("#createLocalRootfs", localAsset?.path ? "" : uiText("请选择本地模板", "Select a local template"), errors);
  }

  const imageMode = Boolean($("#createUseSparseImage")?.checked);
  const imageSize = Number($("#createImageSize")?.value || 0);
  createFormValidationError("#createImageSize", !imageMode || (Number.isInteger(imageSize) && imageSize >= 4 && imageSize <= 512) ? "" : uiText("镜像大小必须是 4 到 512 GB 的整数", "Image size must be an integer from 4 to 512 GB"), errors);

  const netMode = String($("#createNetMode")?.value || "").trim().toLowerCase();
  createFormValidationError("#createNetMode", ["host", "nat", "none", "gateway"].includes(netMode) ? "" : uiText("网络模式必须是 host、nat、none 或 gateway", "Network mode must be host, nat, none, or gateway"), errors);
  if (netMode === "nat") {
    const natOctet = String($("#createStaticNatOctet4")?.value || "").trim();
    const natIP = natOctet ? staticNATIPValue("create") : "";
    const natConflict = natIP && state.containers.some((container) => String(container.natIp || container.staticNatIp || "") === natIP);
    createFormValidationError("#createStaticNatOctet4", !staticNATOctetValid(natOctet, 1, 254) ? uiText("NAT 静态 IP 第 4 字节必须是 1 到 254", "The fourth octet of the static NAT IP must be from 1 to 254") : (natConflict ? uiText("该 NAT 静态 IP 已被其他容器使用", "This static NAT IP is already used by another container") : ""), errors);
    createFormValidationError("#createPorts", validateCreatePortForwards($("#createPorts")?.value) || validateCreatePortForwardAvailability($("#createPorts")?.value), errors);
  } else {
    ["#createStaticNatOctet4", "#createPorts"].forEach((selector) => createFormValidationError(selector, "", errors));
  }

  if (netMode === "gateway") {
    const gateway = String($("#createGatewayContainer")?.value || "").trim();
    const exists = state.containers.some((container) => String(container.name || "") === gateway);
    createFormValidationError("#createGatewayContainer", !gateway ? uiText("网关容器不能为空", "Gateway container is required") : (gateway === name ? uiText("网关容器不能是当前容器", "Gateway container cannot be the current container") : (!exists ? uiText("网关容器尚未安装", "Gateway container is not installed") : "")), errors);
    [["#createGatewayNet", uiText("网关网络名", "Gateway network"), 0], ["#createGatewayIface", uiText("网关内接口", "Gateway interface"), 15], ["#createGatewayBridge", uiText("宿主桥接名", "Host bridge"), 15]].forEach(([selector, label, max]) => {
      const value = String($(selector)?.value || "").trim();
      const invalid = value && (!/^[A-Za-z0-9_-]+$/.test(value) || (max > 0 && value.length > max));
      createFormValidationError(selector, invalid ? uiText(`${label}只能使用字母、数字、下划线和连字符${max > 0 ? `，最长 ${max} 个字符` : ""}`, `${label} may contain only letters, numbers, underscores, and hyphens${max > 0 ? `, up to ${max} characters` : ""}`) : "", errors);
    });
  } else {
    ["#createGatewayContainer", "#createGatewayNet", "#createGatewayIface", "#createGatewayBridge"].forEach((selector) => createFormValidationError(selector, "", errors));
  }

  createFormValidationError("#createMemoryLimit", validateCreateMemoryLimit($("#createMemoryLimit")?.value), errors);
  createFormValidationError("#createCpus", validateCreateCPU($("#createCpus")?.value), errors);
  createFormValidationError("#createPidsLimit", validateCreatePids($("#createPidsLimit")?.value), errors);
  createFormValidationError("#createDns", validateCreateSafeText($("#createDns")?.value, "DNS"), errors);
  createFormValidationError("#createBinds", validateCreateSafeText($("#createBinds")?.value, uiText("绑定挂载", "Bind mounts"), true) || validateCreateBinds($("#createBinds")?.value), errors);
  createFormValidationError("#createInit", validateCreateSafeText($("#createInit")?.value, "Init") || validateCreateInit($("#createInit")?.value), errors);
  createFormValidationError("#createTx11ExtraFlags", $("#createTermuxX11")?.checked ? validateCreateSafeText($("#createTx11ExtraFlags")?.value, uiText("X11 额外参数", "Extra X11 flags")) : "", errors);
  createFormValidationError("#createVirglExtraFlags", $("#createVirgl")?.checked ? validateCreateSafeText($("#createVirglExtraFlags")?.value, uiText("VirGL 额外参数", "Extra VirGL flags")) : "", errors);
  createFormValidationError("#createEnv", validateCreateSafeText($("#createEnv")?.value, uiText("环境变量", "Environment variables"), true), errors);

  const cloudInitSupported = createTemplateSupportsCloudInit(source === "cloud" ? cloudAsset : localAsset, source);
  const cloudInitEnabled = cloudInitSupported && Boolean($("#createCloudInitEnabled")?.checked);
  if (cloudInitEnabled) {
    const result = createCloudInitUserDataResult();
    createFormValidationError(createCloudInitUserDataMode() === "advanced" ? "#createCloudInitUserData" : "#createCloudInitUsername", result.error || "", errors);
    const network = String($("#createCloudInitNetworkConfig")?.value || "");
    createFormValidationError("#createCloudInitNetworkConfig", network.includes(String.fromCharCode(0)) ? uiText("网络定义不能包含 NUL 字符", "Network definition cannot contain NUL characters") : (new TextEncoder().encode(network).length > CLOUD_INIT_MAX_DOCUMENT_BYTES ? uiText("网络定义不能超过 64 KiB", "Network definition cannot exceed 64 KiB") : ""), errors);
  } else {
    ["#createCloudInitUsername", "#createCloudInitPassword", "#createCloudInitSSHPort", "#createCloudInitUserData", "#createCloudInitNetworkConfig"].forEach((selector) => createFormValidationError(selector, "", errors));
  }

  const summary = $("#createValidationSummary");
  if (summary) {
    summary.textContent = errors[0]?.message || "";
    summary.classList.toggle("hidden", errors.length === 0);
  }
  const submit = form.querySelector('button[type="submit"]');
  if (submit) submit.disabled = Boolean(errors.length) || state.busy;
  return errors;
}

function updateCreateCloudInitUI() {
  const panel = $("#createCloudInitField");
  const enabledInput = $("#createCloudInitEnabled");
  const settings = $("#createCloudInitSettings");
  if (!panel || !enabledInput || !settings) return;

  const source = createTemplateSource();
  const item = source === "cloud" ? selectedCreateCloudAsset() : selectedCreateLocalRootfs();
  const supported = createTemplateSupportsCloudInit(item, source);
  panel.classList.toggle("hidden", !supported);
  if (!supported) {
    panel.dataset.templateKey = "";
    settings.classList.add("hidden");
    settings.querySelectorAll("input, textarea, select, button").forEach((input) => { input.disabled = true; });
    enabledInput.disabled = true;
    updateCreateCloudInitUserDataUI();
    return;
  }

  const value = source === "cloud" ? rootfsAssetDownloadURL(item) : String(item?.path || "");
  const key = `${source}:${value}`;
  if (panel.dataset.templateKey !== key) enabledInput.checked = true;
  panel.dataset.templateKey = key;
  enabledInput.disabled = false;
  const enabled = enabledInput.checked;
  panel.classList.toggle("is-disabled", !enabled);
  settings.classList.toggle("hidden", !enabled);
  settings.querySelectorAll("input, textarea, select, button").forEach((input) => { input.disabled = !enabled; });
  updateCreateCloudInitUserDataUI();
  updateCreateFormValidation();
}

function createTemplateSupportBadge(official) {
  if (official) return `<span class="badge template-picker-support official">${uiText("官方支持", "Officially Supported")}</span>`;
  return rootfsUnsupportedBadge();
}

function createTemplateSearchText(item, source) {
  void source;
  return rootfsSystemVersion(item).toLocaleLowerCase("zh-CN");
}

function createTemplateMatches(item, source, query) {
  const normalized = String(query || "").trim().toLocaleLowerCase("zh-CN");
  return !normalized || createTemplateSearchText(item, source).includes(normalized);
}

function createTemplatePickerCard(item, source, value, selected) {
  const details = createTemplateDetails(item, source);
  const supportLabel = details.official ? uiText("Droidspaces 官方支持", "Officially Supported by Droidspaces") : uiText("非官方支持系统", "Unofficial System");
  const sourceClass = source === "cloud" ? `rootfs-source-badge ${rootfsSourceBadgeClass(details.repository)}` : "rootfs-local-source";
  const description = source === "cloud" ? rootfsAssetDescription(item) : "";
  const descriptionMarkup = description ? `<span class="template-picker-card-description" title="${escapeHTML(description)}">${escapeHTML(description)}</span>` : "";
  const label = [details.title, details.architecture, details.variant.label, details.repository, description, supportLabel].filter(Boolean).join(uiText("，", ", "));
  return `<button class="template-picker-card${selected ? " is-selected" : ""}" type="button" aria-pressed="${selected}" aria-label="${escapeHTML(label)}" data-create-template-kind="${source}" data-create-template-value="${escapeHTML(String(value))}"><span class="template-picker-card-primary"><span class="template-picker-card-title">${rootfsDistroIcon(item, "template-picker-distro-icon")}<strong>${escapeHTML(details.title)}</strong></span>${selected ? `<span class="template-picker-selected-mark">${uiText("已选", "Selected")}</span>` : ""}</span><span class="template-picker-card-tags"><span class="badge" title="${uiText("架构", "Architecture")}">${escapeHTML(details.architecture)}</span><span class="badge" title="${escapeHTML(details.variant.title)}">${escapeHTML(details.variant.label)}</span></span>${descriptionMarkup}<span class="template-picker-card-source"><span>${uiText("镜像来源", "Image Source")}</span><strong class="badge ${sourceClass}" title="${escapeHTML(details.repository)}">${escapeHTML(details.repository)}</strong>${createTemplateSupportBadge(details.official)}</span><span class="template-picker-card-stats"><span><small>${uiText("系统包大小", "Package Size")}</small><strong>${escapeHTML(details.size)}</strong></span><span><small>${uiText("系统包日期时间", "Package Date")}</small><strong>${escapeHTML(details.buildDate)}</strong></span></span></button>`;
}

function setCreateTemplatePickerCount(source, count, total, query) {
  if (createTemplateSource() !== source) return;
  const node = $("#createTemplatePickerCount");
  if (!node) return;
  const label = source === "cloud" ? uiText("云端镜像", "Cloud Images") : uiText("本地模板", "Local Templates");
  node.textContent = query && count !== total ? `${count} / ${total} ${label}` : `${count} ${label}`;
}

function renderCreateTemplateSelection() {
  const node = $("#createTemplateSelection");
  if (!node) return;
  const source = createTemplateSource();
  const item = source === "cloud" ? selectedCreateCloudAsset() : selectedCreateLocalRootfs();
  if (!item) {
    const empty = source === "cloud" && state.rootfsLoading ? uiText("正在读取云端镜像", "Loading cloud images") : uiText("尚未选择模板", "No template selected");
    node.innerHTML = `<span class="template-picker-selection-label">${uiText("当前选择", "Current Selection")}</span><span class="template-picker-selection-empty">${escapeHTML(empty)}</span>`;
    return;
  }
  const details = createTemplateDetails(item, source);
  const origin = source === "cloud" ? uiText("云端镜像", "Cloud Image") : uiText("本地模板", "Local Template");
  node.innerHTML = `<span class="template-picker-selection-label">${uiText("当前选择", "Current Selection")}</span><span class="template-picker-selection-main">${rootfsDistroIcon(item, "template-picker-selection-icon")}<span><strong>${escapeHTML(details.title)}</strong><small>${escapeHTML(details.architecture)} · ${escapeHTML(details.variant.label)} · ${escapeHTML(details.repository)}</small></span></span><span class="template-picker-selection-origin">${escapeHTML(origin)}</span>`;
}

function renderCreateLocalTemplatePicker() {
  const list = $("#createLocalRootfsList");
  if (!list) return;
  const query = $("#createLocalRootfsSearch")?.value || "";
  const allItems = state.localRootfs.filter((item) => item.kind !== "backup");
  const items = allItems.filter((item) => createTemplateMatches(item, "local", query));
  const selectedPath = $("#createLocalRootfs")?.value || "";
  if (!items.length) {
    list.innerHTML = `<div class="empty-state">${allItems.length ? uiText("没有匹配的本地模板", "No matching local templates") : uiText("暂无本地模板", "No local templates")}</div>`;
  } else {
    list.innerHTML = items.map((item) => createTemplatePickerCard(item, "local", item.path, item.path === selectedPath)).join("");
  }
  setCreateTemplatePickerCount("local", items.length, allItems.length, query);
  if (createTemplateSource() === "local") renderCreateTemplateSelection();
}

function renderCreateCloudSourceFilter() {
  const select = $("#createCloudSourceFilter");
  if (!select) return;
  const selected = select.value;
  const assets = cloudRootfsAssetsForSelection(state.rootfsAssets);
  const sources = rootfsAssetSources(assets);
  select.innerHTML = [`<option value="">${uiText("全部来源", "All Sources")} (${assets.length})</option>`, ...sources.map((source) => `<option value="${escapeHTML(source)}">${escapeHTML(source)} (${assets.filter((asset) => rootfsAssetSource(asset) === source).length})</option>`)].join("");
  if (sources.includes(selected)) select.value = selected;
  select.disabled = state.rootfsLoading || sources.length <= 1;
}

function renderCreateCloudTemplatePicker() {
  const list = $("#createCloudRootfsList");
  if (!list) return;
  renderCreateCloudSourceFilter();
  const query = $("#createCloudRootfsSearch")?.value || "";
  const selectedSource = $("#createCloudSourceFilter")?.value || "";
  const visibleAssets = cloudRootfsAssetsForSelection(state.rootfsAssets);
  const allItems = visibleAssets.filter((item) => !selectedSource || rootfsAssetSource(item) === selectedSource);
  const items = allItems.filter((item) => createTemplateMatches(item, "cloud", query));
  const selectedIndex = $("#createCloudRootfs")?.value || "";
  list.setAttribute("aria-busy", String(state.rootfsLoading));
  if (!items.length) {
    const error = state.rootfsErrors[0];
    const message = state.rootfsLoading
      ? uiText("正在读取云端镜像", "Loading cloud images")
      : (error ? uiText(`云端镜像暂不可用：${error}`, `Cloud images are unavailable: ${error}`) : (visibleAssets.length ? uiText("没有匹配的云端镜像", "No matching cloud images") : uiText("暂无可用云端镜像", "No cloud images available")));
    list.innerHTML = `<div class="empty-state">${escapeHTML(message)}</div>`;
  } else {
    list.innerHTML = items.map((item) => {
      const index = state.rootfsAssets.indexOf(item);
      return createTemplatePickerCard(item, "cloud", index, String(index) === selectedIndex);
    }).join("");
  }
  setCreateTemplatePickerCount("cloud", items.length, allItems.length, query);
  if (createTemplateSource() === "cloud") renderCreateTemplateSelection();
}

function renderCreateTemplatePicker() {
  if (createTemplateSource() === "cloud") renderCreateCloudTemplatePicker();
  else renderCreateLocalTemplatePicker();
  updateCreateCloudInitUI();
}

function renderCreateLocalOptions() {
  const select = $("#createLocalRootfs");
  if (!select) return;
  const selectedPath = select.value;
  const items = state.localRootfs.filter((item) => item.kind !== "backup");
  if (!items.length) {
    select.innerHTML = `<option value="">${uiText("暂无本地模板", "No local templates")}</option>`;
    renderCreateLocalTemplatePicker();
    return;
  }
  select.innerHTML = [
    `<option value="">${uiText("请选择本地模板", "Select a local template")}</option>`,
    ...items.map((item) => `<option value="${escapeHTML(item.path)}">${escapeHTML(rootfsDisplayName(item))} (${escapeHTML(kindText(item.kind))} · ${escapeHTML(localRootfsSource(item))})</option>`),
  ].join("");
  if (selectedPath && items.some((item) => item.path === selectedPath)) {
    select.value = selectedPath;
  }
  renderCreateLocalTemplatePicker();
  updateCreateCloudInitUI();
}

function renderCreateCloudOptions() {
  const select = $("#createCloudRootfs");
  if (!select) return;
  const selectedURL = rootfsAssetDownloadURL(selectedCreateCloudAsset());
  const assets = cloudRootfsAssetsForSelection(state.rootfsAssets);
  if (!assets.length) {
    select.innerHTML = `<option value="">${uiText("请先刷新云端列表", "Refresh the cloud image list first")}</option>`;
    renderCreateCloudSourceHint();
    renderCreateCloudTemplatePicker();
    return;
  }
  select.innerHTML = [`<option value="">${uiText("请选择云端镜像", "Select a cloud image")}</option>`, ...rootfsAssetSources(assets).map((source) => {
    const options = state.rootfsAssets
      .map((asset, index) => ({ asset, index }))
      .filter(({ asset }) => !isTinyCloudRootfsAsset(asset) && rootfsAssetSource(asset) === source)
      .map(({ asset, index }) => `<option value="${index}">${escapeHTML(rootfsAssetOptionLabel(asset))}</option>`)
      .join("");
    return `<optgroup label="${escapeHTML(source)}">${options}</optgroup>`;
  })].join("");
  const selectedIndex = state.rootfsAssets.findIndex((asset) => rootfsAssetDownloadURL(asset) === selectedURL);
  if (selectedIndex >= 0) select.value = String(selectedIndex);
  renderCreateCloudSourceHint();
  renderCreateCloudTemplatePicker();
  updateCreateCloudInitUI();
}

function selectedCreateCloudAsset() {
  const rawIndex = $("#createCloudRootfs")?.value;
  if (rawIndex === undefined || rawIndex === null || rawIndex === "") return null;
  const index = Number.parseInt(rawIndex, 10);
  if (!Number.isInteger(index) || index < 0) return null;
  const asset = state.rootfsAssets[index] || null;
  return isTinyCloudRootfsAsset(asset) ? null : asset;
}

function renderCreateCloudSourceHint() {
  const hint = $("#createCloudSourceHint");
  if (!hint) return;
  const asset = selectedCreateCloudAsset();
  if (!asset) {
    hint.textContent = uiText("来源：等待选择", "Source: awaiting selection");
    hint.removeAttribute("title");
    return;
  }
  const source = rootfsAssetSource(asset);
  const variant = rootfsAssetVariant(asset);
  const architecture = String(asset.architecture || "-").trim() || "-";
  hint.textContent = uiText(`来源：${source}${variant ? ` · ${variant}` : ""}`, `Source: ${source}${variant ? ` · ${variant}` : ""}`);
  hint.title = `${rootfsDisplayName(asset)} · ${architecture} · ${source}${variant ? ` · ${variant}` : ""}`;
}

function handleCreateTemplateControlChange(source) {
  if (source === "cloud") {
    renderCreateCloudSourceHint();
    $("#createCloudTask").textContent = uiText("选择镜像后直接创建，后台会下载并继续创建容器。", "After you select an image, it will download in the background and then create the container.");
  }
  renderCreateTemplatePicker();
}

function preventTemplateSearchSubmit(event) {
  if (event.key === "Enter") event.preventDefault();
}

function openTaskPanel() {
  setMenuOpen(false);
  setTaskOverviewOpen(false);
  setTaskFloatOpen(true);
  renderTasks();
}

function setTaskOverviewOpen(open) {
  const panel = $("#taskOverviewFloat");
  if (!panel) return;
  panel.classList.toggle("hidden", !open);
  $("#taskStatusBtn")?.setAttribute("aria-expanded", String(open));
}

function normalizeTaskSummary(summary, tasks) {
  if (summary && typeof summary === "object") {
    return {
      total: Number(summary.total) || 0,
      active: Number(summary.active) || 0,
      pending: Number(summary.pending) || 0,
      running: Number(summary.running) || 0,
      done: Number(summary.done) || 0,
      error: Number(summary.error) || 0,
      cancelled: Number(summary.cancelled) || 0,
      byKind: summary.byKind && typeof summary.byKind === "object" ? summary.byKind : {},
    };
  }
  const result = { total: 0, active: 0, pending: 0, running: 0, done: 0, error: 0, cancelled: 0, byKind: {} };
  (tasks || []).forEach((task) => {
    result.total += 1;
    const kind = String(task?.kind || "other");
    result.byKind[kind] = (result.byKind[kind] || 0) + 1;
    const status = String(task?.status || "").toLowerCase();
    if (status === "pending") { result.pending += 1; result.active += 1; }
    else if (status === "running") { result.running += 1; result.active += 1; }
    else if (status === "done") result.done += 1;
    else if (status === "cancelled" || status === "canceled") result.cancelled += 1;
    else result.error += 1;
  });
  return result;
}

function setTaskFloatOpen(open) {
  const panel = $("#taskFloat");
  if (!panel) return;
  panel.classList.toggle("hidden", !open);
  $("#taskOutputBtn")?.setAttribute("aria-expanded", String(open));
}

function trackTask(taskId, onDone) {
  if (!taskId) return;
  const previous = state.tasks[taskId] || {};
  state.tasks[taskId] = {
    ...previous,
    id: taskId,
    onDone: onDone || previous.onDone,
    status: previous.status || "pending",
  };
  renderTasks();
  pollTask(taskId);
}

async function settleTerminalTask(taskId, task, previous = state.tasks[taskId] || {}) {
  const id = String(taskId || "").trim();
  if (!id || terminalTaskSettles.has(id)) return;

  terminalTaskSettles.add(id);
  const status = String(task?.status || "").toLowerCase();
  const action = containerOperationAction(task?.kind);
  const cleared = action ? clearContainerTask(task?.name, id) : false;
  if (cleared) refreshContainerOperationUI();
  delete state.tasks[id];
  renderTasks();

  try {
    if (status === "done" && typeof previous.onDone === "function") {
      await previous.onDone(task);
    } else if (status !== "done" && previous.id && task?.error) {
      toast(task.error);
    }
  } catch (err) {
    toast(err.message);
  } finally {
    terminalTaskSettles.delete(id);
    renderTasks();
  }
}

async function pollTask(taskId) {
  try {
    const task = await api(`/api/tasks/${encodeURIComponent(taskId)}`);
    const previous = state.tasks[taskId] || {};
    const status = String(task.status || "").toLowerCase();
    if (!taskIsActive(task)) {
      await settleTerminalTask(taskId, task, previous);
      await Promise.allSettled([status === "done" ? loadLocalRootfs() : Promise.resolve(), loadTasks()]);
      return;
    }
    state.tasks[taskId] = { ...previous, ...task, onDone: previous.onDone };
    renderTasks();
    setTimeout(() => pollTask(taskId), 900);
  } catch (err) {
    toast(err.message);
  }
}

function sortedTasks() {
  return Object.values(state.tasks).sort((a, b) => (b.updatedAt || b.startedAt || 0) - (a.updatedAt || a.startedAt || 0));
}

function renderTasks() {
  const tasks = sortedTasks().filter(taskShouldRender);
  renderTaskList("#taskFloatList", tasks, uiText("暂无任务输出", "No task output"));
  const active = tasks.filter(taskIsActive).length;
  const node = $("#activeTaskCount");
  if (node) node.textContent = active;
  const outputButton = $("#taskOutputBtn");
  if (outputButton) outputButton.textContent = active ? uiText(`任务输出 ${active}`, `Task Output ${active}`) : uiText("任务输出", "Task Output");
  const status = $("#taskFloatStatus");
  if (status) status.textContent = tasks.length
    ? uiText(`${active} 个运行中 / ${tasks.length} 个任务`, `${active} running / ${tasks.length} tasks`)
    : uiText("暂无任务", "No tasks");
  renderTaskOverview(tasks);
  if (!tasks.length) setTaskFloatOpen(false);
}

function renderTaskOverview(activeTasks = sortedTasks().filter(taskShouldRender)) {
  const summary = normalizeTaskSummary(state.taskSummary, []);
  const summaryNode = $("#taskOverviewSummary");
  if (summaryNode) {
    const counts = [
      [uiText("总计", "Total"), summary.total],
      [uiText("排队", "Queued"), summary.pending],
      [uiText("运行中", "Running"), summary.running],
      [uiText("已完成", "Completed"), summary.done],
      [uiText("失败", "Failed"), summary.error],
      [uiText("已取消", "Cancelled"), summary.cancelled],
    ];
    summaryNode.innerHTML = counts.map(([label, count]) => `<div><strong>${Number(count) || 0}</strong><span>${label}</span></div>`).join("");
  }
  const status = $("#taskOverviewStatus");
  if (status) status.textContent = uiText(
    `本次服务运行累计 ${summary.total || 0} 个任务，当前 ${summary.active || activeTasks.length || 0} 个活跃`,
    `${summary.total || 0} tasks this service run; ${summary.active || activeTasks.length || 0} active`
  );
  const list = $("#taskOverviewList");
  if (!list) return;
  if (!activeTasks.length) {
    list.innerHTML = `<div class="empty-state">${uiText("暂无运行中的后台任务", "No active background tasks")}</div>`;
    return;
  }
  list.innerHTML = activeTasks.map((task) => {
    const percent = Math.max(0, Math.min(100, Number(task.percent) || 0));
    return `<div class="task-overview-item"><div><strong>${escapeHTML(task.name || task.id)}</strong><span>${escapeHTML(taskLabel(task.kind))} · ${escapeHTML(task.status || "pending")}</span></div><span>${percent}%</span></div>`;
  }).join("");
}

function renderTaskList(selector, tasks, emptyText) {
  const list = $(selector);
  if (!list) return;
  if (!tasks.length) {
    list.innerHTML = `<div class="empty-state">${escapeHTML(emptyText)}</div>`;
    return;
  }
  list.innerHTML = tasks.map((task) => {
    const pct = task.percent || 0;
    const status = task.status || "pending";
    const link = task.url ? `<button class="text-btn" data-download-url="${escapeHTML(task.url)}" data-download-name="${escapeHTML(task.name || task.kind || task.id)}">${uiText("下载", "Download")}</button>` : "";
    const backupNote = task.willStopContainer ? `<span>${task.stoppedContainer ? uiText("已停止容器", "Container stopped") : uiText("将停止容器", "Container will stop")}${task.restoredContainer ? uiText(" · 已恢复启动", " · restarted") : task.restoreError ? uiText(" · 恢复失败", " · restart failed") : task.restoreAfterBackup ? uiText(" · 将恢复启动", " · will restart") : ""}</span>` : "";
    const logLines = Array.isArray(task.log) ? task.log.slice(-80).join("\n") : (task.output || "");
    const log = logLines ? `<pre class="task-log">${escapeHTML(logLines)}</pre>` : "";
    return `<div class="task-item"><div><strong>${escapeHTML(task.name || task.id)}</strong><span>${escapeHTML(taskLabel(task.kind))} · ${escapeHTML(status)} · ${pct}%</span>${backupNote}${task.error ? `<div class="task-error">${escapeHTML(task.error)}</div>` : ""}${task.restoreError ? `<div class="task-error">${escapeHTML(task.restoreError)}</div>` : ""}${task.path ? `<span class="mono muted">${escapeHTML(task.path)}</span>` : ""}${log}</div><progress max="100" value="${pct}"></progress>${link}</div>`;
  }).join("");
  if (selector === "#taskFloatList") {
    list.querySelectorAll(".task-log").forEach((log) => { log.scrollTop = log.scrollHeight; });
  }
}

async function downloadFile(url, fallbackName = "download") {
  setBusy(true);
  try {
    const response = await fetch(url, { headers: authHeaders() });
    if (!response.ok) {
      const data = await response.json().catch(() => ({}));
      throw new Error(data.error || `HTTP ${response.status}`);
    }
    const blob = await response.blob();
    const objectURL = URL.createObjectURL(blob);
    const link = document.createElement("a");
    link.href = objectURL;
    link.download = filenameFromDisposition(response.headers.get("Content-Disposition")) || fallbackName || "download";
    document.body.appendChild(link);
    link.click();
    link.remove();
    URL.revokeObjectURL(objectURL);
  } finally {
    setBusy(false);
  }
}

function filenameFromDisposition(value) {
  const match = /filename\*?=(?:UTF-8''|")?([^";]+)/i.exec(value || "");
  return match ? decodeURIComponent(match[1].trim()) : "";
}

async function deleteLocalRootfs(path, name) {
  if (!confirm(uiText(`删除 ${name || path}？此操作会移除该镜像/备份文件。`, `Delete ${name || path}? This removes the image or backup file.`))) return;
  setBusy(true);
  try {
    await api(`/api/rootfs/local/delete?path=${encodeURIComponent(path)}`, { method: "DELETE" });
    toast(uiText("已删除镜像文件", "Image file deleted"));
    await loadLocalRootfs();
  } catch (err) {
    toast(err.message);
  } finally {
    setBusy(false);
  }
}

async function openConfigModal(name) {
  setBusy(true);
  try {
    const data = await api(`/api/containers/${encodeURIComponent(name)}`);
    state.configTarget = data.name || name;
    $("#configName").value = state.configTarget;
    $("#configTitle").textContent = uiText(`修改运行配置 · ${state.configTarget}`, `Edit Runtime Configuration · ${state.configTarget}`);
    $("#configSubtitle").textContent = data.running ? uiText("保存时会先停止容器，并按选项恢复启动", "The container will stop before saving and restart according to the selected option") : uiText("保存后会写入 container.config", "Changes will be written to container.config when saved");
    $("#configHostname").value = data.hostname || "";
    $("#configNetMode").value = data.netMode || "nat";
    setStaticNATIP("config", data.staticNatIp || data.natIp || "");
    $("#configDns").value = data.dnsServers || "";
    $("#configPorts").value = portLines(data.ports);
    $("#configGatewayContainer").value = data.gatewayContainer || data.configValues?.gateway_container || "";
    $("#configGatewayNet").value = data.gatewayNet || data.configValues?.gateway_net || "";
    $("#configGatewayIface").value = data.gatewayLanIfname || data.configValues?.gateway_lan_ifname || "";
    $("#configGatewayBridge").value = data.gatewayBridge || data.configValues?.gateway_bridge || "";
    $("#configBinds").value = safeArray(data.binds).map((bind) => `${bind.source}:${bind.destination}${bind.readOnly ? ":ro" : ""}`).join("\n");
    $("#configTx11ExtraFlags").value = data.tx11ExtraFlags || data.tx11_extra_flags || data.configValues?.tx11_extra_flags || "";
    $("#configVirglExtraFlags").value = data.virglExtraFlags || data.virgl_extra_flags || data.configValues?.virgl_extra_flags || "";
    $("#configMemoryLimit").value = memoryLimitInputValue(data.memoryLimit || data.configValues?.memory_limit || "");
    $("#configCpus").value = resourceInputValue(data.cpus || cpusFromQuota(data.cpuQuota || data.configValues?.cpu_quota, data.cpuPeriod || data.configValues?.cpu_period));
    $("#configPidsLimit").value = resourceInputValue(data.pidsLimit || data.configValues?.pids_limit || "");
    $("#configInit").value = data.customInit || "";
    $("#configRestore").checked = true;
    $("#configDisableIpv6").checked = Boolean(data.disableIPv6);
    delete $("#configDisableIpv6").dataset.userChecked;
    $("#configAndroidStorage").checked = Boolean(data.androidStorage);
    $("#configHwAccess").checked = Boolean(data.hwAccess);
    $("#configGpu").checked = Boolean(data.gpuMode);
    $("#configTermuxX11").checked = Boolean(data.termuxX11);
    $("#configVirgl").checked = Boolean(data.virgl);
    $("#configPulse").checked = Boolean(data.pulseAudio);
    $("#configSelinuxPermissive").checked = Boolean(data.selinuxPermissive);
    $("#configAllowUserns").checked = Boolean(data.allowUserns || data.configValues?.allow_userns === "1");
    $("#configVolatile").checked = Boolean(data.volatileMode);
    $("#configRunAtBoot").checked = Boolean(data.runAtBoot || data.configValues?.run_at_boot === "1");
    $("#configForceCgroupV1").checked = Boolean(data.forceCgroupV1);
    setPrivilegedModeValue("config", data.privilegedMode || data.configValues?.privileged || "");
    $("#configBlockNestedNS").checked = Boolean(data.blockNestedNamespaces) && !privilegedDisablesDeadlock(privilegedModeValue("config"));
    updateDeadlockControls("config");
    updateUserNamespaceControls("config");
    updateGraphicsFlagFields("config");
    $("#configEnv").value = safeArray(data.env).map((env) => `${env.key}=${env.value}`).join("\n");
    updateNetworkModeFields();
    setConfigSaveStatus("");
    $("#configModal").classList.remove("hidden");
  } catch (err) {
    toast(err.message);
  } finally {
    setBusy(false);
  }
}

function setConfigSaveStatus(message = "") {
  const node = $("#configSaveStatus");
  if (!node) return;
  const text = String(message || "").trim();
  node.textContent = text;
  node.classList.toggle("hidden", !text);
}

function hideConfigModal(force = false) {
  if (state.configSaveInProgress && !force) return;
  $("#configModal")?.classList.add("hidden");
  state.configTarget = "";
  setConfigSaveStatus("");
}

function setConfigSaveInProgress(value) {
  const saving = Boolean(value);
  state.configSaveInProgress = saving;
  const form = $("#configForm");
  if (!form) return;
  form.classList.toggle("is-saving", saving);
  form.querySelectorAll("input, select, textarea").forEach((field) => {
    if (saving) {
      field.dataset.configSaveWasDisabled = field.disabled ? "1" : "0";
      field.disabled = true;
      return;
    }
    if (field.dataset.configSaveWasDisabled !== undefined) {
      field.disabled = field.dataset.configSaveWasDisabled === "1";
      delete field.dataset.configSaveWasDisabled;
    }
  });
  if (!saving) updateNetworkModeFields();
}

function waitForConfigRestart(name, timeoutMs = 90000) {
  const deadline = Date.now() + timeoutMs;
  const poll = async () => {
    let lastError = null;
    while (Date.now() < deadline) {
      try {
        const data = await api(`/api/containers/${encodeURIComponent(name)}`);
        if (data.running) return data;
      } catch (err) {
        lastError = err;
      }
      await new Promise((resolve) => setTimeout(resolve, 900));
    }
    const suffix = lastError?.message ? `：${lastError.message}` : "";
    throw new Error(uiText(`配置已保存，但容器未在 90 秒内恢复启动${suffix}`, `Configuration was saved, but the container did not restart within 90 seconds${suffix}`));
  };
  return poll();
}

async function submitConfig(event) {
  event.preventDefault();
  if (state.configSaveInProgress) return;
  const name = $("#configName").value || state.configTarget;
  if (!name) return;
  const running = state.containers.find((container) => container.name === name)?.running || state.selectedDetail?.name === name && state.selectedDetail?.running;
  if (running && !confirm(uiText(`保存 ${name} 的配置会先停止当前容器，完成后按选项恢复启动。继续？`, `Saving ${name} will stop the current container and restart it according to the selected option. Continue?`))) return;
  const netMode = $("#configNetMode").value;
  const payload = {
    hostname: $("#configHostname").value.trim(),
    netMode,
    dnsServers: $("#configDns").value.trim(),
    portForwards: netMode === "nat" ? normalizeListInput($("#configPorts").value) : "",
    staticNatIp: netMode === "nat" ? staticNATIPValue("config") : "",
    gatewayContainer: netMode === "gateway" ? $("#configGatewayContainer").value.trim() : "",
    gatewayNet: netMode === "gateway" ? $("#configGatewayNet").value.trim() : "",
    gatewayLanIfname: netMode === "gateway" ? $("#configGatewayIface").value.trim() : "",
    gatewayBridge: netMode === "gateway" ? $("#configGatewayBridge").value.trim() : "",
    privilegedMode: privilegedModeValue("config"),
    bindMounts: normalizeListInput($("#configBinds").value),
    customInit: $("#configInit").value.trim(),
    tx11ExtraFlags: $("#configTermuxX11").checked ? $("#configTx11ExtraFlags").value.trim() : "",
    virglExtraFlags: $("#configVirgl").checked ? $("#configVirglExtraFlags").value.trim() : "",
    memoryLimit: $("#configMemoryLimit").value.trim(),
    pidsLimit: $("#configPidsLimit").value.trim(),
    ...cpuLimitPayload($("#configCpus").value),
    restoreAfterUpdate: $("#configRestore").checked,
    env: $("#configEnv").value,
    androidStorage: supportsAndroidStorage() && $("#configAndroidStorage").checked,
    hwAccess: $("#configHwAccess").checked,
    gpuMode: $("#configGpu").checked,
    termuxX11: $("#configTermuxX11").checked,
    virgl: $("#configVirgl").checked,
    pulseAudio: $("#configPulse").checked,
    selinuxPermissive: $("#configSelinuxPermissive").checked,
    allowUserns: $("#configAllowUserns").checked,
    volatileMode: $("#configVolatile").checked,
    runAtBoot: $("#configRunAtBoot").checked,
    forceCgroupV1: $("#configForceCgroupV1").checked,
    disableIPv6: modeForcesDisableIPv6(netMode) || $("#configDisableIpv6").checked,
    blockNestedNamespaces: $("#configBlockNestedNS").checked && !privilegedDisablesDeadlock(privilegedModeValue("config")),
  };
  let configSaved = false;
  setConfigSaveInProgress(true);
  setConfigSaveStatus(running && payload.restoreAfterUpdate ? uiText("正在保存配置并等待容器恢复启动...", "Saving configuration and waiting for the container to restart...") : uiText("正在保存配置...", "Saving configuration..."));
  setBusy(true);
  try {
    const data = await api(`/api/containers/${encodeURIComponent(name)}/config`, { method: "PATCH", body: JSON.stringify(payload) });
    configSaved = true;
    if (data.restoreError) {
      setConfigSaveStatus(uiText("配置已保存，但容器恢复启动失败。请从容器详情手动启动。", "Configuration was saved, but the container failed to restart. Start it manually from container details."));
      toast(uiText(`恢复启动失败：${data.restoreError}`, `Restart failed: ${data.restoreError}`));
      return;
    }
    if (data.stopped && payload.restoreAfterUpdate) {
      if (!data.restarted) {
        setConfigSaveStatus(uiText("配置已保存，但未能确认容器已恢复启动。", "Configuration was saved, but the container restart could not be confirmed."));
        toast(uiText("配置已保存，但容器未恢复启动", "Configuration was saved, but the container did not restart"));
        return;
      }
      setConfigSaveStatus(uiText("配置已保存，正在确认容器已恢复启动...", "Configuration saved. Confirming that the container restarted..."));
      await waitForConfigRestart(name);
    }
    await refreshAll();
    if (state.selected === name) await inspect(name, false, false);
    hideConfigModal(true);
    toast(data.restarted ? uiText("配置已保存并恢复启动", "Configuration saved and container restarted") : uiText("配置已保存", "Configuration saved"));
  } catch (err) {
    const message = configSaved
      ? uiText(`配置已保存，但无法确认容器已恢复启动：${err.message}`, `Configuration was saved, but the container restart could not be confirmed: ${err.message}`)
      : uiText(`保存未完成：${err.message}`, `Save did not complete: ${err.message}`);
    setConfigSaveStatus(message);
    toast(message);
  } finally {
    setConfigSaveInProgress(false);
    setBusy(false);
  }
}

async function exportContainer(name, asTemplate) {
  const action = asTemplate ? "template" : "export";
  const container = state.containers.find((item) => item.name === name) || (state.selectedDetail?.name === name ? state.selectedDetail : null);
  if (container?.running && !confirm(uiText(`${asTemplate ? "转换为模板" : "备份"}会先停止容器 ${name}，完成或失败后会尽量恢复启动。继续？`, `${asTemplate ? "Converting to a template" : "Backing up"} will stop container ${name} and attempt to restore it after completion or failure. Continue?`))) return;
  setBusy(true);
  try {
    const data = await api(`/api/containers/${encodeURIComponent(name)}/${action}`, { method: "POST" });
    trackTask(data.taskId);
    openTaskPanel();
    toast(data.willStopContainer ? uiText("任务已开始：会停止容器并在结束后恢复", "Task started: the container will stop and be restored afterward") : (asTemplate ? uiText("已开始转换为模板", "Template conversion started") : uiText("已开始打包备份", "Backup packaging started")));
  } catch (err) {
    toast(err.message);
  } finally {
    setBusy(false);
  }
}

async function loadNetworkSettings() {
  try {
    const data = await api("/api/network/settings");
    state.networkSettings = { ...state.networkSettings, ...data };
    renderNetworkSettings();
  } catch (err) {
    // Older builds may not expose settings; keep defaults visible.
    renderNetworkSettings();
  }
}

function renderNetworkSettings() {
  const data = state.networkSettings || {};
  updateNATPrefixLabels();
  if ($("#natGatewayIP")) $("#natGatewayIP").value = data.natGatewayIP || "172.28.0.1";
  const upstreamMode = data.upstreamMode === "core-auto-detect" || !data.upstreamMode
    ? t("network.autoDetected")
    : String(data.upstreamMode);
  ["#natUpstreamMode", "#settingsNatUpstreamMode"].forEach((selector) => {
    const input = $(selector);
    if (input) input.value = upstreamMode;
  });
}

function renderNetwork() {
  const summary = $("#networkSummary");
  const rows = $("#networkRows");
  if (summary) {
    const counts = state.containers.reduce((acc, item) => {
      const key = item.netMode || "unknown";
      acc[key] = (acc[key] || 0) + 1;
      return acc;
    }, {});
    summary.innerHTML = ["host", "nat", "gateway", "none", "unknown"].filter((key) => counts[key]).map((key) => `<div class="metric"><span class="metric-label">${escapeHTML(key)}</span><strong>${counts[key]}</strong></div>`).join("") || `<div class="metric"><span class="metric-label">${uiText("网络模式", "Network Modes")}</span><strong>0</strong></div>`;
  }
  if (!rows) return;
  if (!state.containers.length) {
    rows.innerHTML = `<tr><td colspan="6" class="empty">${uiText("暂无容器", "No containers")}</td></tr>`;
    return;
  }
  rows.innerHTML = state.containers.map((container) => `<tr><td><strong>${escapeHTML(container.name)}</strong></td><td>${statusBadge(container)}</td><td>${escapeHTML(container.netMode || "-")}</td><td class="mono">${escapeHTML(container.natIp || "-")}</td><td class="mono path-cell">${escapeHTML(portText(container.ports))}</td><td class="mono">${escapeHTML(container.dnsServers || uiText("详情中查看", "See details"))}</td></tr>`).join("");
}

function renderSecurity() {
  const summary = $("#securitySummary");
  const findings = $("#securityFindings");
  const status = state.status || {};
  if (summary) {
    summary.innerHTML = [
      [uiText("授权", "Authorization"), status.authEnabled ? uiText("已启用", "Enabled") : uiText("未启用", "Disabled")],
      [uiText("监听模式", "Listen Mode"), status.mode || "-"],
      [uiText("镜像下载 TLS 校验", "Image Download TLS Verification"), status.rootfsSkipTLSVerify ? uiText("已跳过", "Skipped") : uiText("启用", "Enabled")],
      ["WebSocket Origin", "同源或本机回环"],
      ["socketd", status.socketdEnabled ? uiText("启用", "Enabled") : uiText("禁用", "Disabled")],
    ].map(([k, v]) => `<div class="summary-row"><span>${k}</span><strong>${escapeHTML(v)}</strong></div>`).join("");
  }
  if (!findings) return;
  const items = [];
  if (!status.authEnabled) items.push([uiText("高", "High"), uiText("WebUI 未启用 authToken", "WebUI authToken is not enabled"), uiText("public 或局域网访问时必须配置授权密钥", "Configure an authorization token for public or LAN access")]);
  if (status.mode === "public" && !status.authEnabled) items.push([uiText("高", "High"), uiText("public 模式未启用授权", "Public mode has no authorization"), uiText("外部网络可直接访问 API", "External networks can access the API directly")]);
  if (status.rootfsSkipTLSVerify) items.push([uiText("中", "Medium"), uiText("镜像下载跳过 TLS 校验", "Image downloads skip TLS verification"), uiText("仅在 Android 证书链不可用时临时使用", "Use only temporarily when Android certificate chains are unavailable")]);
  state.containers.forEach((container) => {
    if ((container.ports || []).length) items.push([uiText("中", "Medium"), uiText(`${container.name} 暴露端口`, `${container.name} exposes ports`), portText(container.ports)]);
    if (container.netMode === "host") items.push([uiText("低", "Low"), uiText(`${container.name} 使用 host 网络`, `${container.name} uses host networking`), uiText("容器与设备共享网络命名空间", "The container shares the device network namespace")]);
  });
  const detail = state.selectedDetail;
  if (detail) {
    if (detail.selinuxPermissive) items.push([uiText("高", "High"), uiText(`${detail.name} SELinux 宽容`, `${detail.name} is SELinux permissive`), uiText("降低强制访问控制保护", "This reduces mandatory access control protection")]);
    if (detail.hwAccess) items.push([uiText("中", "Medium"), uiText(`${detail.name} 开启硬件访问`, `${detail.name} has hardware access`), uiText("确认容器内进程可信", "Confirm that processes in the container are trusted")]);
    if (supportsAndroidStorage() && detail.androidStorage) items.push([uiText("中", "Medium"), uiText(`${detail.name} 挂载 Android 存储`, `${detail.name} mounts Android storage`), uiText("注意外部存储数据暴露范围", "Review external storage data exposure")]);
    safeArray(detail.binds).filter((bind) => !bind.readOnly).forEach((bind) => items.push([uiText("中", "Medium"), uiText(`${detail.name} 读写绑定挂载`, `${detail.name} has a read-write bind mount`), `${bind.source} → ${bind.destination}`]));
  }
  if (!items.length) {
    findings.innerHTML = `<div class="empty-state">${uiText("未发现明显风险", "No obvious risks found")}</div>`;
    return;
  }
  findings.innerHTML = items.map(([level, title, desc]) => `<div class="finding"><span class="severity ${level === uiText("高", "High") ? "high" : level === uiText("中", "Medium") ? "medium" : "low"}">${level}</span><div><strong>${escapeHTML(title)}</strong><p>${escapeHTML(desc)}</p></div></div>`).join("");
}

function renderEvents() {
  renderEventList("#eventsList", state.events, uiText("暂无事件", "No events"));
  renderEventList("#overviewEvents", state.events.slice(-5), uiText("暂无事件", "No events"));
}

function renderEventList(selector, events, emptyText) {
  const node = $(selector);
  if (!node) return;
  if (!events.length) {
    node.innerHTML = `<div class="empty-state">${escapeHTML(emptyText)}</div>`;
    return;
  }
  node.innerHTML = events.slice().reverse().map((event) => `<div class="event"><div class="event-time">${event.time ? new Date(event.time * 1000).toLocaleTimeString(uiLocale()) : "-"}</div><div class="event-main"><strong>${escapeHTML(event.action || event.type || "-")}</strong><span>${escapeHTML(event.actorName || event.actorId || "-")}</span></div></div>`).join("");
}

async function loadBatteryPower() {
  if (!batteryMonitoringEnabled()) {
    const data = { message: uiText("电池监控已关闭", "Battery monitoring is disabled") };
    state.batteryPower = data;
    renderBatteryPower(data);
    return data;
  }
  const hours = Number($("#batteryPowerHours")?.value || 24);
  const data = await api(`/api/battery/power?hours=${encodeURIComponent(hours)}`);
  state.batteryPower = data;
  renderBatteryPower(data);
  return data;
}

async function refreshBatteryMonitoring() {
  if (!batteryMonitoringEnabled()) {
    renderBatteryMonitoringDisabled();
    return;
  }
  await Promise.all([loadHost(), loadBatteryPower()]);
}

function renderBatteryPower(data = state.batteryPower) {
  const summary = $("#batteryPowerSummary");
  const batteryBins = $("#batteryPowerBins");
  const inputBins = $("#inputPowerBins");
  const chart = $("#batteryPowerChart");
  if (!summary || !batteryBins || !inputBins || !chart) return;
  if (!data || data.message) {
    summary.innerHTML = `<div class="empty-state">${escapeHTML(data?.message || uiText("暂无电池功率样本", "No battery power samples"))}</div>`;
  } else {
    summary.innerHTML = [
      [uiText("历史样本", "Historical Samples"), `${data.sampleCount || 0}`],
      [uiText("放电样本", "Discharge Samples"), data.dischargeSampleCount ? uiText(`${data.dischargeSampleCount} 条`, `${data.dischargeSampleCount}`) : "-"],
      [uiText("平均放电", "Average Discharge"), data.dischargeSampleCount ? `${fmtCompactNumber(data.avgDischargeW, 2)} W` : "-"],
      [uiText("峰值放电", "Peak Discharge"), data.maxDischargeW ? `${fmtCompactNumber(data.maxDischargeW, 2)} W` : "-"],
      [uiText("充电样本", "Charge Samples"), data.chargeSampleCount ? uiText(`${data.chargeSampleCount} 条`, `${data.chargeSampleCount}`) : "-"],
      [uiText("平均充电", "Average Charge"), data.chargeSampleCount ? `${fmtCompactNumber(data.avgChargeW, 2)} W` : "-"],
      [uiText("峰值充电", "Peak Charge"), data.maxChargeW ? `${fmtCompactNumber(data.maxChargeW, 2)} W` : "-"],
      [uiText("平均输入", "Average Input"), data.inputSampleCount ? `${fmtCompactNumber(data.avgInputW, 2)} W` : "-"],
      [uiText("峰值输入", "Peak Input"), data.maxInputW ? `${fmtCompactNumber(data.maxInputW, 2)} W` : "-"],
    ].map(([label, value]) => `<div class="metric small"><span class="metric-label">${escapeHTML(label)}</span><strong>${escapeHTML(value)}</strong></div>`).join("");
  }
  batteryBins.innerHTML = renderPowerBins(data?.batteryBins || [], data?.dischargeSampleCount || 0);
  inputBins.innerHTML = renderPowerBins(data?.inputBins || [], data?.inputSampleCount || 0);
  chart.innerHTML = renderPowerChart(data?.chartSamples || data?.recentSamples || []);
}

function renderPowerChart(samples) {
  const rows = safeArray(samples)
    .map((sample) => ({
      time: Number(sample.time || 0),
      batteryW: Number(sample.batteryW),
      inputW: Number(sample.inputW),
      hasBattery: Boolean(sample.hasBattery),
      hasInput: Boolean(sample.hasInput),
    }))
    .filter((sample) => sample.time > 0)
    .sort((a, b) => a.time - b.time);
  const batteryValues = rows
    .filter((sample) => sample.hasBattery && Number.isFinite(sample.batteryW))
    .map((sample) => sample.batteryW);
  const inputValues = rows
    .filter((sample) => sample.hasInput && Number.isFinite(sample.inputW))
    .map((sample) => Math.max(0, sample.inputW));
  const magnitude = Math.max(0, ...batteryValues.map((value) => Math.abs(value)), ...inputValues);
  if (!rows.length || magnitude <= 0) return `<div class="empty-state">${uiText("暂无曲线数据", "No chart data")}</div>`;

  const width = 720;
  const height = 260;
  const pad = { top: 18, right: 18, bottom: 38, left: 54 };
  const plotW = width - pad.left - pad.right;
  const plotH = height - pad.top - pad.bottom;
  const minTime = rows[0].time;
  const maxTime = rows[rows.length - 1].time;
  const timeSpan = Math.max(1, maxTime - minTime);
  const hasNegativeBattery = batteryValues.some((value) => value < 0);
  const maxScale = niceChartMax(magnitude);
  const minScale = hasNegativeBattery ? -maxScale : 0;
  const valueRange = Math.max(1, maxScale - minScale);
  const xFor = (time) => pad.left + ((time - minTime) / timeSpan) * plotW;
  const yFor = (value) => pad.top + ((maxScale - Math.max(minScale, Math.min(maxScale, value))) / valueRange) * plotH;
  const buildSeries = (valueFor) => rows
    .map((sample) => {
      const value = valueFor(sample);
      if (!Number.isFinite(value)) return null;
      return { x: xFor(sample.time), y: yFor(value), value };
    })
    .filter(Boolean);
  const batterySeries = buildSeries((sample) => sample.hasBattery ? sample.batteryW : NaN);
  const inputSeries = buildSeries((sample) => sample.hasInput ? Math.max(0, sample.inputW) : NaN);
  if (batterySeries.length + inputSeries.length <= 0) return `<div class="empty-state">${uiText("暂无曲线数据", "No chart data")}</div>`;

  const linePath = (series) => series.map((point, index) => `${index ? "L" : "M"}${point.x.toFixed(1)},${point.y.toFixed(1)}`).join(" ");
  const marker = (series, cls) => {
    const point = series[series.length - 1];
    return point ? `<circle class="power-chart-dot ${cls}" cx="${point.x.toFixed(1)}" cy="${point.y.toFixed(1)}" r="4"></circle>` : "";
  };
  const ticks = minScale < 0
    ? [minScale, minScale / 2, 0, maxScale / 2, maxScale]
    : [0, maxScale * 0.25, maxScale * 0.5, maxScale * 0.75, maxScale];
  const grid = ticks.map((value) => {
    const y = yFor(value);
    const label = `${fmtCompactNumber(value, maxScale >= 10 ? 1 : 2)} W`;
    const cls = Math.abs(value) < 0.0001 ? "power-chart-grid zero" : "power-chart-grid";
    return `<line class="${cls}" x1="${pad.left}" y1="${y.toFixed(1)}" x2="${width - pad.right}" y2="${y.toFixed(1)}"></line><text class="power-chart-label" x="${pad.left - 8}" y="${(y + 4).toFixed(1)}" text-anchor="end">${escapeHTML(label)}</text>`;
  }).join("");
  const batteryLatest = batterySeries[batterySeries.length - 1]?.value;
  const inputLatest = inputSeries[inputSeries.length - 1]?.value;
  const latest = [
    Number.isFinite(batteryLatest) ? uiText(`电池 ${fmtCompactNumber(batteryLatest, 2)} W`, `Battery ${fmtCompactNumber(batteryLatest, 2)} W`) : "",
    Number.isFinite(inputLatest) ? uiText(`输入 ${fmtCompactNumber(inputLatest, 2)} W`, `Input ${fmtCompactNumber(inputLatest, 2)} W`) : "",
  ].filter(Boolean).join(" / ");
  const batteryPath = linePath(batterySeries);
  const inputPath = linePath(inputSeries);
  return `<div class="power-chart-head"><div class="power-chart-legend"><span class="battery">${uiText("电池净功率（正：充电，负：放电）", "Battery net power (positive: charging, negative: discharging)")}</span><span class="input">${uiText("外部输入", "External Input")}</span></div><strong>${escapeHTML(latest || "-")}</strong></div>
    <svg class="power-chart-svg" viewBox="0 0 ${width} ${height}" role="img" aria-label="${uiText("电池净功率曲线，正值表示充电，负值表示放电；同时显示外部输入功率", "Battery net power chart. Positive values charge and negative values discharge; external input is also shown.")}">
      ${grid}
      <line class="power-chart-axis" x1="${pad.left}" y1="${pad.top}" x2="${pad.left}" y2="${height - pad.bottom}"></line>
      <line class="power-chart-axis" x1="${pad.left}" y1="${height - pad.bottom}" x2="${width - pad.right}" y2="${height - pad.bottom}"></line>
      <text class="power-chart-label" x="${pad.left}" y="${height - 12}" text-anchor="start">${escapeHTML(formatChartTime(minTime))}</text>
      <text class="power-chart-label" x="${width - pad.right}" y="${height - 12}" text-anchor="end">${escapeHTML(formatChartTime(maxTime))}</text>
      ${batteryPath ? `<path class="power-chart-line battery" d="${batteryPath}"></path>` : ""}
      ${inputPath ? `<path class="power-chart-line input" d="${inputPath}"></path>` : ""}
      ${marker(batterySeries, "battery")}
      ${marker(inputSeries, "input")}
    </svg>`;
}

function niceChartMax(value) {
  const n = Math.max(1, Number(value) || 1);
  const exponent = Math.floor(Math.log10(n));
  const base = Math.pow(10, exponent);
  const scaled = n / base;
  const step = scaled <= 1 ? 1 : scaled <= 2 ? 2 : scaled <= 5 ? 5 : 10;
  return step * base;
}

function formatChartTime(seconds) {
  const date = new Date(seconds * 1000);
  return new Intl.DateTimeFormat(uiLocale(), { month: "2-digit", day: "2-digit", hour: "2-digit", minute: "2-digit", hour12: false }).format(date);
}

function renderPowerBins(bins, total) {
  if (!bins.length || total <= 0) return `<div class="empty-state">${uiText("暂无区间数据", "No range data")}</div>`;
  return bins.map((bin) => {
    const percent = Math.max(0, Math.min(100, Number(bin.percent || 0)));
    return `<div class="power-bin"><div class="power-bin-head"><span>${escapeHTML(bin.label)}</span><strong>${uiText(`${bin.count || 0} 次 · ${fmtCompactNumber(percent, 1)}%`, `${bin.count || 0} · ${fmtCompactNumber(percent, 1)}%`)}</strong></div><div class="power-bin-track"><span style="width:${percent}%"></span></div></div>`;
  }).join("");
}

function switchView(view, options = {}) {
  if (view === "battery" && !batteryMonitoringEnabled()) {
    if (!options.silentBatteryRedirect) toast(t("battery.monitoringDisabledRedirect"));
    view = "overview";
  }
  if (!options.keepMenuOpen) setMenuOpen(false);
  if (view !== "rootfs") setRootfsRepositoryEditorOpen(false);
  state.currentView = view;
  $$(".nav-item").forEach((button) => button.classList.toggle("active", button.dataset.view === view || (button.dataset.view === "containers" && (view === "detail" || view === "terminal"))));
  $$(".sub-nav[data-view]").forEach((button) => button.classList.toggle("active", button.dataset.view === view));
  collapseMenuGroups();
  document.activeElement?.blur?.();
  $$(".view-panel").forEach((panel) => panel.classList.remove("active"));
  $(`#${view}View`)?.classList.add("active");
  const titles = {
    overview: ["view.overview.title", "view.overview.subtitle"],
    containers: ["view.containers.title", "view.containers.subtitle"],
    detail: ["view.detail.title", "view.detail.subtitle"],
    terminal: ["view.terminal.title", "view.terminal.subtitle"],
    rootfs: ["view.rootfs.title", ""],
    network: ["view.network.title", "view.network.subtitle"],
    security: ["view.security.title", "view.security.subtitle"],
    diagnostics: ["view.diagnostics.title", "view.diagnostics.subtitle"],
    battery: ["view.battery.title", "view.battery.subtitle"],
    settings: ["view.settings.title", "view.settings.subtitle"],
  };
  const [titleKey, subtitleKey] = titles[view] || ["document.title", ""];
  $("#viewTitle").textContent = t(titleKey);
  $("#viewSubtitle").textContent = subtitleKey ? t(subtitleKey) : "";
  if (view === "rootfs" && !options.skipRootfsLoad) {
    refreshCurrentRootfsTab().catch((err) => toast(err.message));
  }
  if (view === "network") { loadNetworkSettings().catch((err) => toast(err.message)); renderNetwork(); }
  if (view === "security") renderSecurity();
  if (view === "diagnostics") {
    renderBackendDiagnostics();
    loadBackendDiagnostics().catch((err) => toast(err.message));
    loadHost().catch((err) => toast(err.message));
    renderWebUILog();
    loadWebUILog();
  }
  if (view === "battery") {
    renderDeviceMetrics();
    refreshBatteryMonitoring().catch((err) => toast(err.message));
  }
  if (view === "settings") loadSystemSettings().catch((err) => toast(err.message));
  if (view === "overview") refreshOverviewMetrics();
  updateWebUILogPolling();
}

function refreshCurrentRootfsTab(forceRefresh = false) {
  if (state.rootfsTab === "remote") return loadRootfsAssets({ forceRefresh });
  return loadLocalRootfs();
}

function switchRootfsTab(tab) {
  if (!["local", "backups", "remote"].includes(tab)) return;
  state.rootfsTab = tab;
  if (tab !== "remote") setRootfsRepositoryEditorOpen(false);
  $$("[data-rootfs-tab]").forEach((button) => {
    const active = button.dataset.rootfsTab === tab;
    button.classList.toggle("active", active);
    button.setAttribute("aria-selected", active ? "true" : "false");
  });
  const panels = { local: "#rootfsLocalPanel", backups: "#rootfsBackupsPanel", remote: "#rootfsRemotePanel" };
  Object.values(panels).forEach((selector) => $(selector)?.classList.remove("active"));
  $(panels[tab])?.classList.add("active");
  refreshCurrentRootfsTab().catch((err) => toast(err.message));
}

function setContainerFilter(filter) {
  state.containerFilter = filter;
  $$("[data-container-filter]").forEach((button) => {
    const active = button.dataset.containerFilter === filter;
    button.classList.toggle("active", active);
    if (button.classList.contains("segmented-option")) button.setAttribute("aria-pressed", active ? "true" : "false");
  });
  switchView("containers");
  renderContainers();
}

function updateCreateSourceUI() {
  const source = createTemplateSource();
  $("#localSourceField").classList.toggle("hidden", source !== "local");
  $("#cloudSourceField").classList.toggle("hidden", source !== "cloud");
  $("#createTemplatePicker")?.setAttribute("data-source", source);
  renderCreateTemplatePicker();
  if (source === "cloud" && !state.rootfsAssetsLoaded) loadRootfsAssets().catch((err) => toast(err.message));
}

function privilegedDisablesDeadlock(value) {
  const normalized = String(value || "").toLowerCase();
  return normalized === "full" || normalized.split(/[ ,]+/).includes("noseccomp");
}

function privilegedModeInputs(prefix) {
  return $$(`#${prefix}PrivilegedMode input[type="checkbox"]`);
}

function privilegedFullInput(prefix) {
  return privilegedModeInputs(prefix).find((input) => input.value === "full");
}

function privilegedModeValue(prefix) {
  const full = privilegedFullInput(prefix);
  if (full?.checked) return "full";
  const checked = privilegedModeInputs(prefix).filter((input) => input.checked).map((input) => input.value);
  return checked.join(",");
}

function setPrivilegedModeValue(prefix, value) {
  const parts = String(value || "").toLowerCase().split(/[,\s]+/).filter(Boolean);
  const useFull = parts.includes("full");
  privilegedModeInputs(prefix).forEach((input) => {
    input.checked = useFull || parts.includes(input.value);
    input.disabled = useFull && input.value !== "full";
  });
  updateDeadlockControls(prefix);
  updateUserNamespaceControls(prefix);
}

function confirmPrivilegedMode() {
  const acknowledgement = t("security.privilegedAcknowledgement");
  return window.prompt(t("security.privilegedPrompt", { acknowledgement })) === acknowledgement;
}

function handlePrivilegedModeChange(prefix, changedInput) {
  const inputs = privilegedModeInputs(prefix);
  const alreadyEnabled = inputs.some((input) => input !== changedInput && input.checked);
  if (changedInput?.checked && !alreadyEnabled && !confirmPrivilegedMode()) {
    changedInput.checked = false;
    toast(t("security.privilegedAcknowledgementRequired", { acknowledgement: t("security.privilegedAcknowledgement") }));
    updateDeadlockControls(prefix);
    updateUserNamespaceControls(prefix);
    return;
  }
  if (changedInput?.value === "full") {
    inputs.forEach((input) => {
      if (input === changedInput) return;
      input.checked = changedInput.checked;
      input.disabled = changedInput.checked;
    });
  } else if (changedInput?.checked && !inputs.filter((input) => input.value !== "full").some((input) => !input.checked)) {
    const full = privilegedFullInput(prefix);
    if (full) {
      full.checked = true;
      inputs.forEach((input) => {
        if (input.value !== "full") input.disabled = true;
      });
    }
  }
  updateDeadlockControls(prefix);
  updateUserNamespaceControls(prefix);
}

function updateDeadlockControls(prefix) {
  const checkbox = $(`#${prefix}BlockNestedNS`);
  if (!checkbox) return;
  const disabled = privilegedDisablesDeadlock(privilegedModeValue(prefix));
  checkbox.disabled = disabled;
  if (disabled) checkbox.checked = false;
}

function updateUserNamespaceControls(prefix) {
  const checkbox = $(`#${prefix}AllowUserns`);
  if (!checkbox) return;
  const forced = privilegedDisablesDeadlock(privilegedModeValue(prefix));
  checkbox.disabled = forced;
  if (forced) checkbox.checked = true;
}

async function downloadSelectedCloudForCreate() {
  const asset = selectedCreateCloudAsset();
  if (!asset) { toast(uiText("请先选择云端镜像", "Select a cloud image first")); return; }
  const button = $("#createCloudDownloadBtn");
  const restoreButton = setDownloadSubmissionState(button, uiText("正在提交...", "Submitting..."));
  $("#createCloudTask").textContent = uiText("正在校验镜像来源并提交预下载任务...", "Verifying the image source and submitting the pre-download task...");
  try {
    const data = await api("/api/rootfs/download", { method: "POST", body: JSON.stringify(asset) });
    trackTask(data.taskId, (task) => {
      $("#createCloudTask").textContent = uiText(`已下载到本地：${task.path || ""}。可切换到本地模板后创建。`, `Downloaded locally: ${task.path || ""}. Switch to Local Template to create the container.`);
    });
    openTaskPanel();
    $("#createCloudTask").textContent = data.shared
      ? uiText("已加入正在进行的预下载任务。", "Joined the pre-download task already in progress.")
      : uiText("预下载任务已提交；也可以直接创建，让后台自动下载并创建。", "Pre-download task submitted. You can also create directly and let the backend download the image automatically.");
  } catch (err) {
    $("#createCloudTask").textContent = uiText(`预下载提交失败：${err.message}`, `Pre-download submission failed: ${err.message}`);
    throw err;
  } finally {
    restoreButton();
  }
}

function resetMenuInteractionState() {
  const active = document.activeElement;
  if (active?.closest?.("#menuPanel, #menuToggle")) active.blur();
}

function setMenuOpen(open) {
  const panel = $("#menuPanel");
  const toggle = $("#menuToggle");
  if (!panel || !toggle) return;
  panel.classList.toggle("hidden", !open);
  toggle.setAttribute("aria-expanded", open ? "true" : "false");
  panel.classList.toggle("menu-open", open);
  if (open) {
    setTaskOverviewOpen(false);
    setTaskFloatOpen(false);
    positionMenuPanel();
  }
  if (!open) {
    panel.style.removeProperty("top");
    panel.style.removeProperty("max-height");
    collapseMenuGroups();
    resetMenuInteractionState();
  }
}

function positionMenuPanel() {
  const panel = $("#menuPanel");
  const topbar = $(".topbar");
  if (!panel || !topbar) return;
  const top = Math.max(0, Math.ceil(topbar.getBoundingClientRect().bottom + 8));
  const viewportHeight = window.visualViewport?.height || window.innerHeight;
  panel.style.top = `${top}px`;
  panel.style.maxHeight = `${Math.max(160, Math.floor(viewportHeight - top - 12))}px`;
}

function toggleMenu() {
  const panel = $("#menuPanel");
  setMenuOpen(panel?.classList.contains("hidden"));
}

function setMenuGroupOpen(group, open) {
  const wrapper = $(`[data-menu-group="${group}"]`);
  if (!wrapper) return;
  wrapper.classList.toggle("open", open);
  wrapper.querySelector(".nav-submenu")?.classList.toggle("hidden", !open);
  wrapper.querySelector("[data-menu-toggle]")?.setAttribute("aria-expanded", open ? "true" : "false");
}

function collapseMenuGroups() {
  $$('[data-menu-group]').forEach((group) => setMenuGroupOpen(group.dataset.menuGroup, false));
}


async function runCLI(command) {
  setBusy(true);
  $("#cliOutput").textContent = uiText("执行中", "Running");
  try {
    const data = await api("/api/cli", { method: "POST", body: JSON.stringify({ command }) });
    $("#cliOutput").textContent = `$ droidspaces ${data.command}\nexit=${data.exitCode}\n\n${data.output || ""}`;
    if (command === "scan") await refreshAll();
  } catch (err) {
    $("#cliOutput").textContent = err.message;
    toast(err.message);
  } finally {
    setBusy(false);
  }
}

function escapeHTML(value) {
  return String(value)
    .replaceAll("&", "&amp;")
    .replaceAll("<", "&lt;")
    .replaceAll(">", "&gt;")
    .replaceAll('"', "&quot;")
    .replaceAll("'", "&#039;");
}

document.addEventListener("click", (event) => {
  const button = event.target.closest("button");
  if (!button) return;

  if (button.id === "menuToggle") {
    toggleMenu();
    return;
  }
  if (button.dataset.createTemplateKind) {
    const source = button.dataset.createTemplateKind;
    const select = source === "cloud" ? $("#createCloudRootfs") : $("#createLocalRootfs");
    const value = button.dataset.createTemplateValue || "";
    if (!select || !Array.from(select.options).some((option) => option.value === value)) return;
    select.value = value;
    select.dispatchEvent(new Event("change", { bubbles: true }));
    return;
  }
  if (button.dataset.menuToggle) {
    const group = button.dataset.menuToggle;
    const wrapper = button.closest("[data-menu-group]");
    const willOpen = !wrapper?.classList.contains("open");
    if (button.dataset.view) switchView(button.dataset.view, { keepMenuOpen: true });
    setMenuGroupOpen(group, willOpen);
    return;
  }
  if (button.dataset.view) {
    switchView(button.dataset.view);
    setMenuOpen(false);
    return;
  }
  if (button.dataset.containerFilter) {
    setContainerFilter(button.dataset.containerFilter);
    return;
  }
  if (button.id === "taskOutputBtn") {
    setMenuOpen(false);
    setTaskOverviewOpen(false);
    setTaskFloatOpen($("#taskFloat")?.classList.contains("hidden"));
    if (!$("#taskFloat")?.classList.contains("hidden")) loadTasks().catch((err) => toast(err.message));
    return;
  }
  if (button.id === "taskStatusBtn") {
    setMenuOpen(false);
    setTaskFloatOpen(false);
    setTaskOverviewOpen($("#taskOverviewFloat")?.classList.contains("hidden"));
    if (!$("#taskOverviewFloat")?.classList.contains("hidden")) loadTasks().catch((err) => toast(err.message));
    return;
  }
  if (button.id === "taskFloatCloseBtn") {
    setTaskFloatOpen(false);
    return;
  }
  if (button.id === "taskFloatRefreshBtn") {
    loadTasks().catch((err) => toast(err.message));
    return;
  }
  if (button.id === "taskOverviewCloseBtn") {
    setTaskOverviewOpen(false);
    return;
  }
  if (button.id === "taskOverviewRefreshBtn") {
    loadTasks().catch((err) => toast(err.message));
    return;
  }
  // Navigation and menu controls remain usable while a refresh or unrelated
  // request is busy; action controls below retain the existing busy guard.
  if (state.busy) return;

  if (button.id === "bootPriorityBtn") {
    showBootPriorityModal();
    return;
  }
  if (button.id === "bootPriorityCloseBtn" || button.id === "bootPriorityCancelBtn") {
    hideBootPriorityModal();
    return;
  }
  if (button.id === "bootPrioritySaveBtn") {
    submitBootPriority();
    return;
  }
  if (button.dataset.bootPriorityMove) {
    moveBootPriority(Number(button.dataset.bootPriorityIndex), button.dataset.bootPriorityMove);
    return;
  }
  if (button.dataset.serviceFilter) {
    state.serviceFilter = button.dataset.serviceFilter;
    state.serviceSearch = "";
    if (state.selected && state.containerServices[state.selected]) renderDetailServices(state.selected, state.containerServices[state.selected]);
    return;
  }
  if (button.dataset.rootfsTab) {
    switchView("rootfs", { skipRootfsLoad: true, keepMenuOpen: false });
    switchRootfsTab(button.dataset.rootfsTab);
    setMenuOpen(false);
    return;
  }
  if (button.dataset.repoRemove !== undefined) {
    const index = Number(button.dataset.repoRemove);
    state.rootfsRepositories.splice(index, 1);
    renderRepositories();
    return;
  }

  if (button.id === "repoManageBtn") {
    setRootfsRepositoryEditorOpen(!state.rootfsRepositoryEditorOpen);
    return;
  }
  if (button.id === "repoEditorCloseBtn") {
    setRootfsRepositoryEditorOpen(false);
    return;
  }

  const action = button.dataset.action;
  const encodedName = button.dataset.name;
  if (action && encodedName) {
    const name = decodeURIComponent(encodedName);
    if (action === "inspect") inspect(name).catch((err) => toast(err.message));
    else if (action === "delete") deleteContainer(name);
    else if (action === "config") openConfigModal(name);
    else if (action === "export") exportContainer(name, false);
    else if (action === "template") exportContainer(name, true);
    else if (action === "terminal") { selectTerminal(name); switchView("terminal"); connectTerminal(); }
    else runLifecycle(name, action);
    return;
  }

  if (button.dataset.rootfs) {
    try { downloadRootfs(JSON.parse(decodeURIComponent(button.dataset.rootfs)), button); }
    catch (err) { toast(err.message); }
    return;
  }

  if (button.dataset.downloadUrl) {
    downloadFile(button.dataset.downloadUrl, button.dataset.downloadName || "download").catch((err) => toast(err.message));
    return;
  }

  if (button.dataset.deleteRootfs) {
    deleteLocalRootfs(button.dataset.deleteRootfs, button.dataset.deleteRootfsName || uiText("镜像", "Image"));
    return;
  }

  if (button.dataset.useLocalRootfs) {
    showCreateModal({ source: "local", localRootfsPath: button.dataset.useLocalRootfs });
    return;
  }

  if (button.id === "repoAddBtn") {
    state.rootfsRepositories.push({ name: "", url: "" });
    renderRepositories();
    return;
  }
  if (button.id === "repoSaveBtn") {
    saveRepositories().then(() => setRootfsRepositoryEditorOpen(false)).catch((err) => toast(err.message));
    return;
  }
  if (button.id === "settingsRepoAddBtn") {
    const repos = collectSettingsRepositories();
    repos.push({ name: "", url: "" });
    renderSettingsRepositories(repos);
    return;
  }
  if (button.id === "settingsSaveBtn") {
    saveSystemSettingsFromForm().catch((err) => toast(err.message));
    return;
  }
  if (button.id === "coreUpdateCheckBtn") {
    checkCoreUpdate().catch((err) => toast(err.message));
    return;
  }
  if (button.id === "coreUpdateBtn") {
    startCoreUpdate().catch((err) => toast(err.message));
    return;
  }
  if (button.dataset.settingsRepoRemove !== undefined) {
    const removeIndex = Number(button.dataset.settingsRepoRemove);
    const repos = collectSettingsRepositories().filter((_, index) => index !== removeIndex);
    renderSettingsRepositories(repos.length ? repos : [{ name: "", url: "" }]);
    return;
  }
  if (button.id === "rootfsUploadBtn") {
    uploadLocalRootfs();
    return;
  }
  if (button.dataset.detailTab) {
    switchDetailTab(button.dataset.detailTab);
    return;
  }
  if (button.dataset.detailLoad) {
    const name = decodeURIComponent(button.dataset.name || state.selected || "");
    if (!name) { toast(uiText("请先选择容器", "Select a container first")); return; }
    if (button.dataset.detailLoad === "users") loadDetailUsers(name).catch((err) => toast(err.message));
    else if (button.dataset.detailLoad === "services") loadDetailServices(name).catch((err) => toast(err.message));
    else if (button.dataset.detailLoad === "diagnostics") loadDetailDiagnostics().catch((err) => toast(err.message));
    return;
  }
  if (button.dataset.serviceAction) {
    const name = decodeURIComponent(button.dataset.name || state.selected || "");
    setBusy(true);
    postServiceAction(name, button.dataset.serviceManager, button.dataset.serviceName, button.dataset.serviceAction)
      .then((data) => { toast(uiText("服务操作已提交", "Service action submitted")); renderDiagnosticsOutput(data, "#detailServiceOutput"); return loadDetailServices(name); })
      .catch((err) => toast(err.message))
      .finally(() => setBusy(false));
    return;
  }
  if (button.dataset.systemdInspect) {
    const name = decodeURIComponent(button.dataset.name || state.selected || "");
    openSystemdUnitInspector(name, button.dataset.serviceName || "");
    return;
  }
  if (button.dataset.systemdInspectorClose) {
    state.systemdUnit = null;
    renderSystemdUnitInspector();
    return;
  }
  if (button.dataset.systemdOverrideSave) {
    persistSystemdOverride(false);
    return;
  }
  if (button.dataset.systemdOverrideDelete) {
    persistSystemdOverride(true);
    return;
  }
  if (button.dataset.sparseAction) {
    runSparseAction(decodeURIComponent(button.dataset.name || state.selected || ""), button.dataset.sparseAction);
    return;
  }
  if (button.dataset.terminalUser !== undefined) {
    openTerminalAsUser(decodeURIComponent(button.dataset.name || state.selected || ""), decodeURIComponent(button.dataset.terminalUser || "root"));
    return;
  }
  if (button.dataset.networkDiagnose !== undefined) {
    runContainerNetworkDiagnostics(decodeURIComponent(button.dataset.networkDiagnose || state.selected || ""));
    return;
  }
  if (button.dataset.diagnosticsAction) {
    const selector = button.closest("#detailBody") ? "#detailDiagnosticsOutput" : (button.closest("#settingsView") ? "#settingsIntegrationStatus" : "#diagnosticsOutput");
    runDiagnosticsAction(button.dataset.diagnosticsAction, selector);
    return;
  }
  if (button.dataset.copyText) {
    copyText(button.dataset.copyText);
    return;
  }
  if (button.dataset.cli) runCLI(button.dataset.cli);
});

document.addEventListener("change", (event) => {
  const input = event.target.closest?.("[data-diag-toggle]");
  if (!input) return;
  const inSettings = Boolean(input.closest("#settingsView"));
  const selector = input.closest("#detailDiagnosticsOutput") ? "#detailDiagnosticsOutput" : (inSettings ? "#settingsIntegrationStatus" : "#diagnosticsOutput");
  const payload = { [input.dataset.diagToggle]: input.checked };
  setBusy(true);
  saveDiagnosticsSettings(payload)
    .then((data) => { if (inSettings) { state.systemSettings.integration = data; renderSettingsIntegration(data); } else renderDiagnosticsSettings(data, selector); toast(uiText("诊断设置已保存", "Diagnostic settings saved")); })
    .catch((err) => { input.checked = !input.checked; if (inSettings) { const node = $("#settingsIntegrationStatus"); if (node) node.textContent = err.message; } else renderDiagnosticsOutput(err.message, selector); toast(err.message); })
    .finally(() => setBusy(false));
});

document.addEventListener("input", (event) => {
  if (event.target.closest?.("#settingsDefaultNatThirdOctet")) {
    cleanStaticNATOctetInput(event.target);
    state.systemSettings.defaultNatThirdOctet = Number(event.target.value || DEFAULT_NAT_THIRD_OCTET);
    updateNATPrefixLabels();
    updateStaticNATIP("create");
    updateStaticNATIP("config");
    return;
  }
  const natOctet = event.target.closest?.("[data-static-nat-prefix]");
  if (natOctet) {
    cleanStaticNATOctetInput(natOctet);
    updateStaticNATIP(natOctet.dataset.staticNatPrefix);
    if (natOctet.dataset.staticNatPrefix === "create") updateCreateFormValidation();
    return;
  }
  const input = event.target.closest?.("#detailServiceSearch");
  if (!input) return;
  state.serviceSearch = input.value || "";
  if (state.selected && state.containerServices[state.selected]) renderDetailServices(state.selected, state.containerServices[state.selected]);
});

document.addEventListener("click", (event) => {
  if (event.target.closest("#menuToggle") || event.target.closest("#menuPanel")) return;
  setMenuOpen(false);
});

document.addEventListener("keydown", (event) => {
  if (event.key === "Escape") setMenuOpen(false);
});

function repositionOpenMenu() {
  if (!$("#menuPanel")?.classList.contains("hidden")) positionMenuPanel();
}

window.addEventListener("resize", repositionOpenMenu);
window.visualViewport?.addEventListener("resize", repositionOpenMenu);
document.addEventListener("visibilitychange", () => {
  updateWebUILogPolling();
  if (!document.hidden && state.webuiLogAutoFollow && state.currentView === "diagnostics") loadWebUILog();
});

function bindUI() {
  $("#refreshBtn").addEventListener("click", refreshAll);
  $("#sidebarRefreshBtn")?.addEventListener("click", refreshAll);
  $("#backendDiagnosticsRefreshBtn")?.addEventListener("click", () => refreshBackendDiagnostics().catch((err) => toast(err.message)));
  $("#webuiLogRefreshBtn")?.addEventListener("click", () => loadWebUILog());
  $("#webuiLogTail")?.addEventListener("change", (event) => {
    state.webuiLogTail = normalizedWebUILogTail(event.target.value);
    event.target.value = String(state.webuiLogTail);
    renderWebUILog();
    loadWebUILog();
  });
  $("#webuiLogFollow")?.addEventListener("change", (event) => {
    state.webuiLogAutoFollow = event.target.checked;
    renderWebUILog();
    updateWebUILogPolling();
    if (state.webuiLogAutoFollow) loadWebUILog();
  });
  $("#rootfsRefreshBtn").addEventListener("click", () => refreshCurrentRootfsTab(true).catch((err) => toast(err.message)));
  $("#rootfsArch")?.addEventListener("change", () => {
    state.rootfsAssetsLoaded = false;
    state.rootfsAssetsLoadedAt = 0;
    if (state.rootfsTab === "remote") loadRootfsAssets().catch((err) => toast(err.message));
  });
  $("#hostRefreshBtn").addEventListener("click", () => loadHost().catch((err) => toast(err.message)));
  $("#eventsBtn").addEventListener("click", () => loadEvents().catch((err) => toast(err.message)));
  $("#batteryPowerRefreshBtn")?.addEventListener("click", () => refreshBatteryMonitoring().catch((err) => toast(err.message)));
  $("#batteryPowerHours")?.addEventListener("change", () => refreshBatteryMonitoring().catch((err) => toast(err.message)));
  $("#createBtn").addEventListener("click", showCreateModal);
  $("#createCloseBtn").addEventListener("click", hideCreateModal);
  $("#createCancelBtn").addEventListener("click", hideCreateModal);
  $("#createForm").addEventListener("submit", createContainer);
  $("#createForm").addEventListener("input", (event) => {
    if (event.target.matches("input, textarea, select")) updateCreateFormValidation();
  });
  $("#createForm").addEventListener("change", (event) => {
    if (event.target.matches("input, textarea, select")) updateCreateFormValidation();
  });
  $("#configCloseBtn").addEventListener("click", hideConfigModal);
  $("#configCancelBtn").addEventListener("click", hideConfigModal);
  $("#configForm").addEventListener("submit", submitConfig);
  $("#createNetMode").addEventListener("change", updateNetworkModeFields);
  $("#createUseSparseImage")?.addEventListener("change", updateCreateStorageUI);
  $("#createCloudInitEnabled")?.addEventListener("change", updateCreateCloudInitUI);
  $$('input[name="createCloudInitUserDataMode"]').forEach((input) => input.addEventListener("change", updateCreateCloudInitUserDataUI));
  ["#createCloudInitUsername", "#createCloudInitSSHKeys", "#createCloudInitPackages", "#createCloudInitCommands", "#createCloudInitUserData"].forEach((selector) => {
    $(selector)?.addEventListener("input", updateCreateCloudInitUserDataUI);
  });
  $("#createCloudInitPassword")?.addEventListener("input", () => {
    $("#createCloudInitPassword").dataset.generated = "false";
    updateCreateCloudInitUserDataUI();
  });
  $("#createCloudInitPasswordVisibility")?.addEventListener("click", () => {
    const password = $("#createCloudInitPassword");
    if (!password) return;
    password.type = password.type === "password" ? "text" : "password";
    updateCreateCloudInitPasswordVisibility();
  });
  $("#createCloudInitPasswordRegenerate")?.addEventListener("click", () => {
    setCreateCloudInitGeneratedPassword();
    updateCreateCloudInitUserDataUI();
  });
  $("#createCloudInitSSHEnabled")?.addEventListener("change", () => {
    const rootSSH = $("#createCloudInitRootSSH");
    if (!$("#createCloudInitSSHEnabled")?.checked && rootSSH) rootSSH.checked = false;
    updateCreateCloudInitUserDataUI();
  });
  $("#createCloudInitRootSSH")?.addEventListener("change", () => {
    const sshEnabled = $("#createCloudInitSSHEnabled");
    if ($("#createCloudInitRootSSH")?.checked && sshEnabled) sshEnabled.checked = true;
    updateCreateCloudInitUserDataUI();
  });
  ["#createCloudInitSudo", "#createCloudInitSSHPort"].forEach((selector) => {
    $(selector)?.addEventListener("change", updateCreateCloudInitUserDataUI);
  });
  $("#createCloudInitSSHPort")?.addEventListener("input", updateCreateCloudInitUserDataUI);
  $("#createTermuxX11")?.addEventListener("change", () => updateGraphicsFlagFields("create"));
  $("#createVirgl")?.addEventListener("change", () => updateGraphicsFlagFields("create"));
  privilegedModeInputs("create").forEach((input) => input.addEventListener("change", () => handlePrivilegedModeChange("create", input)));
  $("#configNetMode").addEventListener("change", updateNetworkModeFields);
  $("#settingsMode")?.addEventListener("change", updateSettingsModeUI);
  $("#settingsUILanguage")?.addEventListener("change", (event) => {
    updateSettingsUILanguage(event.currentTarget.value).catch((err) => toast(err.message));
  });
  $("#settingsBatteryMonitoringEnabled")?.addEventListener("change", updateBatterySettingsFormUI);
  $("#settingsNjuMirrorEnabled")?.addEventListener("change", (event) => {
  const enabled = event.currentTarget.checked;
  const repositories = setLinuxContainersMirrorRepository(collectSettingsRepositories(), enabled, true);
  renderSettingsRepositories(repositories);
  toast(enabled ? uiText("已切换为南京大学 lxc-image 下载源，保存设置后生效", "Switched to the Nanjing University lxc-image source. Save settings to apply it.") : uiText("已切换为 lxc-image 官方下载源，保存设置后生效", "Switched to the official lxc-image source. Save settings to apply it."));
  });
  $("#configTermuxX11")?.addEventListener("change", () => updateGraphicsFlagFields("config"));
  $("#configVirgl")?.addEventListener("change", () => updateGraphicsFlagFields("config"));
  privilegedModeInputs("config").forEach((input) => input.addEventListener("change", () => handlePrivilegedModeChange("config", input)));
  $$('input[name="createSource"]').forEach((input) => input.addEventListener("change", updateCreateSourceUI));
  $("#createCloudDownloadBtn").addEventListener("click", () => downloadSelectedCloudForCreate().catch((err) => toast(err.message)));
  $("#createCloudPickerRefreshBtn")?.addEventListener("click", () => loadRootfsAssets({ forceRefresh: true }).catch((err) => toast(err.message)));
  $("#createLocalRootfs")?.addEventListener("change", () => handleCreateTemplateControlChange("local"));
  $("#createCloudRootfs")?.addEventListener("change", () => handleCreateTemplateControlChange("cloud"));
  $("#createLocalRootfsSearch")?.addEventListener("input", renderCreateLocalTemplatePicker);
  $("#createCloudRootfsSearch")?.addEventListener("input", renderCreateCloudTemplatePicker);
  $("#createLocalRootfsSearch")?.addEventListener("keydown", preventTemplateSearchSubmit);
  $("#createCloudRootfsSearch")?.addEventListener("keydown", preventTemplateSearchSubmit);
  $("#createCloudSourceFilter")?.addEventListener("change", renderCreateCloudTemplatePicker);
  $("#rootfsSourceFilter")?.addEventListener("change", (event) => {
    state.rootfsSourceFilter = event.target.value || "";
    renderRootfsAssets(state.rootfsAssets, []);
  });
  $("#rootfsRemoteSearch")?.addEventListener("input", () => renderRootfsAssets(state.rootfsAssets, []));
  $("#rootfsRemoteSearch")?.addEventListener("keydown", preventTemplateSearchSubmit);
  $("#terminalConnectBtn").addEventListener("click", connectTerminal);
  $("#terminalDisconnectBtn").addEventListener("click", disconnectTerminal);
  $("#terminalClearBtn").addEventListener("click", clearTerminal);
  $("#terminalSendBtn").addEventListener("click", sendTerminalInput);
  $("#terminalTarget").addEventListener("change", (event) => { state.terminalTarget = event.target.value; loadTerminalUsers(state.terminalTarget).catch((err) => toast(err.message)); });
  $("#terminalUserSelect").addEventListener("change", (event) => { if (event.target.value !== "__manual") $("#terminalUser").value = event.target.value; });
  $("#terminalScreen").addEventListener("keydown", handleTerminalKey);
  $("#terminalScreen").addEventListener("paste", async (event) => {
    if (!state.terminalConnected) return;
    const text = event.clipboardData?.getData("text") || "";
    if (text && sendTerminalRaw(text)) event.preventDefault();
  });
  $("#terminalInput").addEventListener("keydown", (event) => {
    if (event.ctrlKey && event.key.toLowerCase() === "c") { if (sendTerminalRaw("\x03")) event.preventDefault(); return; }
    if (event.ctrlKey && event.key.toLowerCase() === "d") { if (sendTerminalRaw("\x04")) event.preventDefault(); return; }
    if (event.key === "Enter" && !event.shiftKey) { event.preventDefault(); sendTerminalInput(); }
  });
  $("#filterInput").addEventListener("input", renderContainers);
  $("#detailRefreshBtn").addEventListener("click", () => {
    if (!state.selected) { toast(uiText("请先选择容器", "Select a container first")); return; }
    inspect(state.selected, true, false).catch((err) => toast(err.message));
  });
  $("#detailTerminalBtn").addEventListener("click", () => {
    if (!state.selected) { toast(uiText("请先选择容器", "Select a container first")); return; }
    selectTerminal(state.selected);
    switchView("terminal");
    connectTerminal();
  });
  $("#loginForm").addEventListener("submit", async (event) => {
    event.preventDefault();
    const token = $("#loginToken").value.trim();
    try { await loginWithToken(token); }
    catch (err) { showLogin(err.message); }
  });
}

async function boot() {
  bindUI();
  applyLocalizedDynamicLabels();
  updateNetworkModeFields();
  updateCreateStorageUI();
  updateGraphicsFlagFields("create");
  updateGraphicsFlagFields("config");
  updateDeadlockControls("create");
  updateDeadlockControls("config");
  updateUserNamespaceControls("create");
  updateUserNamespaceControls("config");
  renderNetworkSettings();
  if (window.DS_AUTH_REQUIRED) {
    const token = getAuthToken();
    if (!token) { showLogin(); return; }
    try { await loginWithToken(token); }
    catch (err) { showLogin(err.message); }
  } else {
    state.authenticated = true;
    hideLogin();
    await loadSystemSettings(false).catch(() => {});
    await savePendingInitialUILanguage().catch((err) => toast(err.message));
    await refreshAll();
  }
  restartOverviewRefreshTimer();
  setInterval(() => {
    if (state.authenticated && !document.hidden) refreshContainerRuntimeBadges();
  }, 1000);
  setInterval(() => {
    if (state.busy || !state.authenticated) return;
    if (state.currentView === "detail" && state.selected) inspect(state.selected, false, false).catch((err) => toast(err.message));
    else if (state.currentView !== "overview") refreshAll();
  }, BACKGROUND_REFRESH_MS);
}

window.addEventListener("droidspaceslocalechange", () => {
  applyLocalizedDynamicLabels();
  renderDeviceMetrics();
  renderOverviewContainers();
  renderBackendSummary();
  renderBackendDiagnostics();
  renderHost();
  renderContainers();
  if (state.selectedDetail) renderDetail(state.selectedDetail);
  renderNetwork();
  renderSecurity();
  renderEvents();
  renderTasks();
  renderRuntimeVersions();
  renderNetworkSettings();
  renderWebUILog();
  renderRootfsAssets(state.rootfsAssets, state.rootfsErrors || []);
  renderLocalRootfs();
  renderCreateTemplatePicker();
  updateCreateFormValidation();
  if (state.currentView === "settings") {
    $("#viewTitle").textContent = t("view.settings.title");
    $("#viewSubtitle").textContent = t("view.settings.subtitle");
  } else {
    switchView(state.currentView, { keepMenuOpen: true, skipRootfsLoad: true, silentBatteryRedirect: true });
  }
});

window.addEventListener("droidspacesinitiallocalechoice", (event) => {
  const language = event.detail?.locale;
  if (language !== "zh-CN" && language !== "en") return;
  state.pendingInitialUILanguage = language;
  if (state.authenticated) {
    savePendingInitialUILanguage().catch((err) => toast(err.message));
  }
});

boot();
