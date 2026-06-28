const state = {
  containers: [],
  selected: "",
  busy: false,
  lastEventSince: 0,
  authenticated: !window.DS_AUTH_REQUIRED,
  status: {},
  terminalSocket: null,
  terminalConnected: false,
  terminalTarget: "",
  terminalLines: [""],
  terminalRow: 0,
  terminalCol: 0,
  currentView: "overview",
  rootfsAssets: [],
  localRootfs: [],
  tasks: {},
  createCloudTaskId: "",
};

const $ = (selector) => document.querySelector(selector);
const $$ = (selector) => Array.from(document.querySelectorAll(selector));

function getAuthToken() {
  return localStorage.getItem("DS_WEBUI_AUTH_TOKEN") || "";
}

function setAuthToken(token) {
  if (token) localStorage.setItem("DS_WEBUI_AUTH_TOKEN", token);
  else localStorage.removeItem("DS_WEBUI_AUTH_TOKEN");
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
      showLogin("授权失败，请重新输入 token");
    }
    throw new Error(data.error || `HTTP ${response.status}`);
  }
  return data;
}

async function loginWithToken(token) {
  setAuthToken(token);
  const response = await fetch("/api/login", {
    method: "POST",
    headers: authHeaders(),
  });
  const data = await response.json().catch(() => ({}));
  if (!response.ok) {
    setAuthToken("");
    throw new Error(data.error || "登录失败");
  }
  state.authenticated = true;
  hideLogin();
  await refreshAll();
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
  $$("#refreshBtn, #rootfsRefreshBtn, .row-actions button, .tool-buttons button, .modal-actions button").forEach((button) => {
    button.disabled = value;
  });
  updateTerminalControls();
}

function toast(message) {
  const node = $("#toast");
  node.textContent = message;
  node.classList.remove("hidden");
  clearTimeout(node._timer);
  node._timer = setTimeout(() => node.classList.add("hidden"), 3600);
}

function fmtTime(unix) {
  if (!unix) return "未知";
  return new Date(unix * 1000).toLocaleString();
}

function fmtBytes(value) {
  if (!value) return "无限制";
  const units = ["B", "KiB", "MiB", "GiB", "TiB"];
  let n = Number(value);
  let i = 0;
  while (n >= 1024 && i < units.length - 1) {
    n /= 1024;
    i += 1;
  }
  return `${n.toFixed(i === 0 ? 0 : 1)} ${units[i]}`;
}

function fmtSize(bytes) {
  if (!bytes) return "未知大小";
  const units = ["B", "KiB", "MiB", "GiB"];
  let n = Number(bytes);
  let i = 0;
  while (n >= 1024 && i < units.length - 1) {
    n /= 1024;
    i += 1;
  }
  return `${n.toFixed(i === 0 ? 0 : 1)} ${units[i]}`;
}

function portText(ports) {
  if (!ports || ports.length === 0) return "无";
  return ports
    .map((p) => {
      const host = p.hostPortEnd ? `${p.hostPort}-${p.hostPortEnd}` : p.hostPort;
      const cont = p.containerPortEnd
        ? `${p.containerPort}-${p.containerPortEnd}`
        : p.containerPort;
      return `${host}:${cont}/${p.protocol}`;
    })
    .join(", ");
}

async function loadStatus() {
  const data = await api("/api/status");
  state.status = data;
  const backendLabels = {
    ready: "就绪",
    unreachable: "不可达",
    "cli-fallback": "CLI 兜底",
    "workspace-fallback": "工作区兜底",
  };
  $("#backendState").textContent = backendLabels[data.backend] || data.backend || "未知";
  const info = data.info || {};
  $("#totalContainers").textContent = info.containersTotal ?? 0;
  $("#runningContainers").textContent = info.containersRunning ?? 0;
  $("#stoppedContainers").textContent = info.containersStopped ?? 0;
  if (data.backendError) toast(`后端提示：${data.backendError}`);
}

async function loadContainers() {
  const data = await api(`/api/containers?all=1`);
  state.containers = data.containers || [];
  if ((data.source === "workspace" || data.source === "cli") && data.backendError) {
    toast(data.source === "cli" ? "已使用 Droidspaces CLI 兜底读取容器状态" : "已使用工作区兜底读取容器状态");
  }
  renderContainers();
  renderTerminalTargets();
}

function renderContainers() {
  const tbody = $("#containerRows");
  const filter = $("#filterInput").value.trim().toLowerCase();
  const stateFilter = $("#containerStateFilter")?.value || "all";
  const rows = state.containers.filter((container) => {
    if (stateFilter === "running" && !container.running) return false;
    if (stateFilter === "stopped" && container.running) return false;
    const haystack = [container.name, container.netMode, container.hostname, container.natIp]
      .join(" ")
      .toLowerCase();
    return haystack.includes(filter);
  });

  if (rows.length === 0) {
    tbody.innerHTML = `<tr><td colspan="5" class="empty">无匹配容器</td></tr>`;
    return;
  }

  tbody.innerHTML = rows
    .map((container) => {
      const encoded = encodeURIComponent(container.name);
      const statusClass = container.running ? "running" : "stopped";
      const statusText = container.running ? "运行中" : "已停止";
      const pid = container.pid || "-";
      const action = container.running
        ? `<button class="icon-btn danger" title="停止" aria-label="停止" data-action="stop" data-name="${encoded}">■</button>`
        : `<button class="icon-btn primary" title="启动" aria-label="启动" data-action="start" data-name="${encoded}">▶</button>`;
      return `
        <tr data-name="${encoded}">
          <td><div class="name-cell"><span class="state-dot ${container.running ? "running" : ""}"></span><strong>${escapeHTML(container.name)}</strong></div></td>
          <td><span class="badge ${statusClass}">${statusText}</span></td>
          <td class="mono">${pid}</td>
          <td>${escapeHTML(container.netMode || "-")}</td>
          <td><div class="row-actions">${action}<button class="icon-btn" title="重启" aria-label="重启" data-action="restart" data-name="${encoded}">↻</button><button class="icon-btn" title="详细参数" aria-label="详细参数" data-action="inspect" data-name="${encoded}">ⓘ</button><button class="icon-btn" title="终端" aria-label="终端" data-action="terminal" data-name="${encoded}">⌁</button><button class="icon-btn" title="打包备份" aria-label="打包备份" data-action="export" data-name="${encoded}">⇩</button><button class="icon-btn" title="转为模板" aria-label="转为模板" data-action="template" data-name="${encoded}">▣</button><button class="icon-btn danger" title="删除" aria-label="删除" data-action="delete" data-name="${encoded}">×</button></div></td>
        </tr>`;
    })
    .join("");
}

async function refreshAll() {
  if (window.DS_AUTH_REQUIRED && !state.authenticated) return;
  setBusy(true);
  try {
    await loadStatus();
    await loadContainers();
    await loadLocalRootfs();
    if (state.selected) await inspect(state.selected, false);
    await loadEvents();
  } catch (err) {
    toast(err.message);
  } finally {
    setBusy(false);
  }
}

async function inspect(name, showToast = true) {
  const data = await api(`/api/containers/${encodeURIComponent(name)}`);
  state.selected = name;
  $("#detailTitle").textContent = data.name || name;
  $("#detailPanel").classList.remove("hidden");
  $("#detailBody").innerHTML = renderDetail(data);
  if (showToast) toast("已加载详情");
}

function renderDetail(data) {
  const flags = [
    ["前台", data.foreground],
    ["易失", data.volatileMode],
    ["cgroup v1", data.forceCgroupV1],
    ["禁用 IPv6", data.disableIPv6],
    ["Android 存储", data.androidStorage],
    ["SELinux 宽容", data.selinuxPermissive],
    ["硬件访问", data.hwAccess],
    ["GPU", data.gpuMode],
    ["Termux X11", data.termuxX11],
    ["阻止嵌套 NS", data.blockNestedNamespaces],
    ["镜像挂载", data.isImageMount],
  ];
  const binds = data.binds?.length
    ? data.binds.map((bind) => `<div class="kv"><span>${bind.readOnly ? "只读" : "读写"}</span><span class="mono">${escapeHTML(bind.source)} → ${escapeHTML(bind.destination)}</span></div>`).join("")
    : `<div class="empty-state">无绑定挂载</div>`;
  return `
    <div class="kv"><span>状态</span><span>${data.running ? "运行中" : "已停止"}</span></div>
    <div class="kv"><span>PID</span><span class="mono">${data.pid || "-"}</span></div>
    <div class="kv"><span>UUID</span><span class="mono">${escapeHTML(data.uuid || "-")}</span></div>
    <div class="kv"><span>RootFS</span><span class="mono">${escapeHTML(data.rootfsPath || "-")}</span></div>
    <div class="kv"><span>镜像</span><span class="mono">${escapeHTML(data.imageRef || "-")}</span></div>
    <div class="kv"><span>主机名</span><span>${escapeHTML(data.hostname || "-")}</span></div>
    <div class="kv"><span>网络</span><span>${escapeHTML(data.netMode || "-")} ${data.natIp ? `(${escapeHTML(data.natIp)})` : ""}</span></div>
    <div class="kv"><span>Init</span><span class="mono">${escapeHTML(data.customInit || "/sbin/init")}</span></div>
    <div class="kv"><span>DNS</span><span class="mono">${escapeHTML(data.dnsServers || "-")}</span></div>
    <div class="kv"><span>端口</span><span class="mono">${escapeHTML(portText(data.ports))}</span></div>
    <div class="kv"><span>启动时间</span><span>${fmtTime(data.startedAt)}</span></div>
    <div class="kv"><span>内存</span><span>${fmtBytes(data.memoryLimit)}</span></div>
    <div class="kv"><span>CPU</span><span>${data.cpuQuota ? `${data.cpuQuota}/${data.cpuPeriod || "默认"}` : "无限制"}</span></div>
    <div class="kv"><span>PIDs</span><span>${data.pidsLimit || "无限制"}</span></div>
    <div class="kv"><span>来源</span><span>${escapeHTML(data.source || "-")}</span></div>
    <div class="flag-grid">${flags.map(([label, value]) => `<div class="flag"><span>${label}</span><strong>${value ? "开" : "关"}</strong></div>`).join("")}</div>
    <h2>绑定挂载</h2>${binds}${data.rawOutput ? `<h2>CLI 信息</h2><pre class="mini-output">${escapeHTML(data.rawOutput)}</pre>` : ""}`;
}

async function runLifecycle(name, action) {
  setBusy(true);
  try {
    const data = await api(`/api/containers/${encodeURIComponent(name)}/${action}`, { method: "POST" });
    const labels = { start: "启动", stop: "停止", restart: "重启" };
    toast(`${labels[action]}已提交 (${data.source || "后端"})`);
    if (data.output) $("#cliOutput").textContent = data.output;
    await refreshAll();
  } catch (err) {
    toast(err.message);
  } finally {
    setBusy(false);
  }
}

async function deleteContainer(name) {
  if (!confirm(`删除容器 ${name}？这会删除该容器目录及其中数据。`)) return;
  setBusy(true);
  try {
    const data = await api(`/api/containers/${encodeURIComponent(name)}`, { method: "DELETE" });
    toast(`已删除 ${name}`);
    $("#cliOutput").textContent = data.deleted || "已删除";
    if (state.selected === name) closeDetail();
    await refreshAll();
  } catch (err) {
    toast(err.message);
  } finally {
    setBusy(false);
  }
}

function selectTerminal(name) {
  $("#terminalPanel").classList.remove("hidden");
  $("#terminalTarget").value = name;
  $("#terminalScreen")?.focus();
  updateTerminalControls();
}

function renderTerminalTargets() {
  const select = $("#terminalTarget");
  if (!select) return;
  const current = select.value;
  select.innerHTML = state.containers
    .map((container) => `<option value="${escapeHTML(container.name)}">${escapeHTML(container.name)}${container.running ? "" : " (停止)"}</option>`)
    .join("");
  if (state.containers.some((container) => container.name === current)) {
    select.value = current;
  } else if (state.containers.length > 0) {
    select.value = state.containers[0].name;
  }
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
  if (connectBtn) connectBtn.disabled = state.busy || connected || connecting;
  if (disconnectBtn) disconnectBtn.disabled = !socket;
  if (sendBtn) sendBtn.disabled = !connected;
  if (input) input.disabled = !connected;
  if (target) target.disabled = connected || connecting;
  if (user) user.disabled = connected || connecting;
  if (connecting) terminalStatus("连接中");
  else if (connected) terminalStatus(`已连接 ${state.terminalTarget}`, true);
  else terminalStatus("未连接");
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
  const nums = body
    .split(";")
    .filter(Boolean)
    .map((part) => Number(part))
    .map((value) => (Number.isFinite(value) && value > 0 ? value : 1));
  const first = nums[0] || 1;
  if (final === "A") state.terminalRow = Math.max(0, state.terminalRow - first);
  else if (final === "B") {
    state.terminalRow += first;
    ensureTerminalLine(state.terminalRow);
  } else if (final === "C") state.terminalCol += first;
  else if (final === "D") state.terminalCol = Math.max(0, state.terminalCol - first);
  else if (final === "G") state.terminalCol = Math.max(0, first - 1);
  else if (final === "H" || final === "f") {
    state.terminalRow = Math.max(0, (nums[0] || 1) - 1);
    state.terminalCol = Math.max(0, (nums[1] || 1) - 1);
    ensureTerminalLine(state.terminalRow);
  } else if (final === "K") {
    ensureTerminalLine(state.terminalRow);
    const line = state.terminalLines[state.terminalRow] || "";
    const mode = nums[0] || 0;
    if (mode === 1) state.terminalLines[state.terminalRow] = line.slice(state.terminalCol);
    else if (mode === 2) state.terminalLines[state.terminalRow] = "";
    else state.terminalLines[state.terminalRow] = line.slice(0, state.terminalCol);
  } else if (final === "J") {
    const mode = nums[0] || 0;
    if (mode === 2) {
      state.terminalLines = [""];
      state.terminalRow = 0;
      state.terminalCol = 0;
    } else {
      ensureTerminalLine(state.terminalRow);
      state.terminalLines[state.terminalRow] = (state.terminalLines[state.terminalRow] || "").slice(0, state.terminalCol);
      state.terminalLines = state.terminalLines.slice(0, state.terminalRow + 1);
    }
  }
}

function appendTerminalChar(ch) {
  ensureTerminalLine(state.terminalRow);
  if (ch === "\r") {
    state.terminalCol = 0;
    return;
  }
  if (ch === "\n") {
    state.terminalRow += 1;
    state.terminalCol = 0;
    ensureTerminalLine(state.terminalRow);
    return;
  }
  if (ch === "\b" || ch === "\x7f") {
    state.terminalCol = Math.max(0, state.terminalCol - 1);
    return;
  }
  if (ch === "\t") {
    const spaces = 8 - (state.terminalCol % 8);
    for (let i = 0; i < spaces; i += 1) appendTerminalChar(" ");
    return;
  }
  if (ch < " ") return;
  const line = state.terminalLines[state.terminalRow] || "";
  const padded = state.terminalCol > line.length ? `${line}${" ".repeat(state.terminalCol - line.length)}` : line;
  state.terminalLines[state.terminalRow] = `${padded.slice(0, state.terminalCol)}${ch}${padded.slice(state.terminalCol + 1)}`;
  state.terminalCol += 1;
}

function renderTerminalBuffer() {
  const screen = $("#terminalScreen");
  if (!screen) return;
  if (!screen.dataset.active) {
    screen.textContent = "";
    screen.dataset.active = "1";
  }
  if (state.terminalLines.length > 2000) {
    const drop = state.terminalLines.length - 1600;
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
        if (j < text.length) {
          applyTerminalCSI(text.slice(i + 2, j + 1));
          i = j;
          continue;
        }
      } else if (next === "]") {
        let j = i + 2;
        while (j < text.length && text[j] !== "\x07") {
          if (text[j] === "\x1b" && text[j + 1] === "\\") {
            j += 1;
            break;
          }
          j += 1;
        }
        i = j;
        continue;
      } else {
        i += 1;
        continue;
      }
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
  if (screen) {
    screen.dataset.active = "1";
    screen.textContent = "";
  }
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
  if (sendTerminalRaw(value.endsWith("\n") ? value : `${value}\r`)) {
    input.value = "";
  }
}

function connectTerminal() {
  const target = $("#terminalTarget").value;
  if (!target) {
    toast("请先选择容器");
    return;
  }
  const container = state.containers.find((item) => item.name === target);
  if (container && !container.running) {
    toast("请先启动容器");
    return;
  }
  if (state.terminalSocket) {
    disconnectTerminal();
  }
  const user = $("#terminalUser").value.trim() || "root";
  resetTerminalBuffer(`连接 ${target} (${user})...\n`);
  state.terminalTarget = target;
  state.terminalConnected = false;
  const socket = new WebSocket(terminalURL(target, user));
  socket.binaryType = "arraybuffer";
  state.terminalSocket = socket;
  updateTerminalControls();

  socket.onopen = () => {
    state.terminalConnected = true;
    appendTerminal("已连接。\n");
    updateTerminalControls();
    $("#terminalScreen")?.focus();
  };
  socket.onmessage = async (event) => {
    if (event.data instanceof Blob) {
      appendTerminal(await event.data.text());
    } else if (event.data instanceof ArrayBuffer) {
      appendTerminal(new TextDecoder().decode(event.data));
    } else {
      appendTerminal(event.data);
    }
  };
  socket.onerror = () => {
    toast("终端连接错误");
  };
  socket.onclose = (event) => {
    if (state.terminalSocket === socket) {
      state.terminalSocket = null;
      state.terminalConnected = false;
    }
    appendTerminal(`\n[连接已关闭${event.reason ? `: ${event.reason}` : ""}]\n`);
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
  const screen = $("#terminalScreen");
  if (!screen) return;
  resetTerminalBuffer();
  screen.focus();
}

function handleTerminalKey(event) {
  if (!state.terminalConnected) return;
  if (event.metaKey || event.altKey) return;
  let value = "";
  if (event.ctrlKey && event.key.length === 1) {
    const code = event.key.toUpperCase().charCodeAt(0) - 64;
    if (code >= 0 && code <= 31) value = String.fromCharCode(code);
  } else {
    const special = {
      Enter: "\r",
      Backspace: "\x7f",
      Tab: "\t",
      Escape: "\x1b",
      ArrowUp: "\x1b[A",
      ArrowDown: "\x1b[B",
      ArrowRight: "\x1b[C",
      ArrowLeft: "\x1b[D",
      Home: "\x1b[H",
      End: "\x1b[F",
      Delete: "\x1b[3~",
      PageUp: "\x1b[5~",
      PageDown: "\x1b[6~",
    };
    value = special[event.key] || (event.key.length === 1 ? event.key : "");
  }
  if (value && sendTerminalRaw(value)) {
    event.preventDefault();
  }
}

async function createContainer(event) {
  event.preventDefault();
  const source = document.querySelector('input[name="createSource"]:checked')?.value || "local";
  const rootfsPath = source === "local" ? $("#createLocalRootfs").value : source === "direct" ? $("#createRootfs").value.trim() : "";
  const payload = {
    name: $("#createName").value.trim(),
    hostname: $("#createHostname").value.trim(),
    rootfsPath,
    rootfsSource: source,
    rootfsTaskId: source === "cloud" ? state.createCloudTaskId : "",
    netMode: $("#createNetMode").value,
    dnsServers: $("#createDns").value.trim(),
    portForwards: $("#createPorts").value.trim(),
    bindMounts: $("#createBinds").value.trim(),
    customInit: $("#createInit").value.trim(),
    env: $("#createEnv").value,
    start: $("#createStart").checked,
    androidStorage: $("#createAndroidStorage").checked,
    gpuMode: $("#createGpu").checked,
    termuxX11: $("#createTermuxX11").checked,
    pulseAudio: $("#createPulse").checked,
    volatileMode: $("#createVolatile").checked,
    disableIPv6: $("#createDisableIpv6").checked,
  };
  setBusy(true);
  try {
    const data = await api("/api/containers", { method: "POST", body: JSON.stringify(payload) });
    hideCreateModal();
    toast(`已创建 ${data.name}`);
    $("#cliOutput").textContent = data.startOutput || data.configPath || "已创建";
    await refreshAll();
  } catch (err) {
    toast(err.message);
  } finally {
    setBusy(false);
  }
}

function showCreateModal() {
  $("#createModal").classList.remove("hidden");
  state.createCloudTaskId = "";
  $("#createCloudTask").textContent = "";
  updateCreateSourceUI();
  renderCreateLocalOptions();
  renderCreateCloudOptions();
  $("#createName").focus();
}

function hideCreateModal() {
  $("#createModal").classList.add("hidden");
}

function closeDetail() {
  state.selected = "";
  $("#detailTitle").textContent = "详情";
  $("#detailBody").innerHTML = "";
  $("#detailPanel").classList.add("hidden");
}

async function loadRootfsAssets() {
  setBusy(true);
  const list = $("#rootfsList");
  list.innerHTML = `<div class="empty-state">加载中</div>`;
  try {
    const arch = $("#rootfsArch").value;
    const data = await api(`/api/rootfs${arch ? `?arch=${encodeURIComponent(arch)}` : ""}`);
    state.rootfsAssets = data.assets || [];
    renderRootfsAssets(state.rootfsAssets, data.errors || []);
    renderCreateCloudOptions();
  } catch (err) {
    list.innerHTML = `<div class="empty-state">${escapeHTML(err.message)}</div>`;
    toast(err.message);
  } finally {
    setBusy(false);
  }
}

function renderRootfsAssets(assets, errors) {
  const list = $("#rootfsList");
  if (!assets.length) {
    list.innerHTML = `<div class="empty-state">${errors.length ? escapeHTML(errors.join("\n")) : "暂无可用 RootFS"}</div>`;
    return;
  }
  list.innerHTML = assets
    .map((asset, index) => {
      const encoded = encodeURIComponent(JSON.stringify(asset));
      return `<div class="rootfs-item"><h3>${escapeHTML(asset.name || "未命名")}</h3><div class="rootfs-desc">${escapeHTML(asset.description || "无描述")}</div><div class="mono muted">${escapeHTML(asset.architecture || "-")} · ${fmtSize(asset.sizeBytes)} · ${escapeHTML(asset.buildDate || "-")}</div><div class="mono muted">${escapeHTML(asset.sourceRepoName || "-")}</div><div class="rootfs-foot"><span class="mono muted">${escapeHTML(asset.uniqueFilename || `rootfs-${index}`)}</span><button class="text-btn primary" data-rootfs="${encoded}">下载</button></div></div>`;
    })
    .join("");
  if (errors.length) toast(errors.join("\n"));
}

async function downloadRootfs(asset) {
  setBusy(true);
  try {
    const data = await api("/api/rootfs/download", { method: "POST", body: JSON.stringify(asset) });
    trackTask(data.taskId);
    toast("已开始下载");
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
    $("#localRootfsList").innerHTML = `<div class="empty-state">${escapeHTML(err.message)}</div>`;
  }
}

function renderLocalRootfs() {
  const list = $("#localRootfsList");
  if (!state.localRootfs.length) {
    list.innerHTML = `<div class="empty-state">暂无本地模板</div>`;
    return;
  }
  list.innerHTML = state.localRootfs.map((item) => {
    const canDownload = item.kind === "archive" || item.kind === "backup" || item.kind === "image";
    const downloadURL = `/api/rootfs/local/download?path=${encodeURIComponent(item.path)}`;
    const download = canDownload ? `<button class="text-btn" data-download-url="${escapeHTML(downloadURL)}" data-download-name="${escapeHTML(item.name)}">下载</button>` : "";
    return `<div class="rootfs-item compact-item"><h3>${escapeHTML(item.name)}</h3><div class="mono muted">${escapeHTML(kindText(item.kind))} · ${fmtSize(item.size)}</div><div class="mono muted path-line">${escapeHTML(item.path)}</div><div class="rootfs-foot"><button class="text-btn" data-use-local-rootfs="${escapeHTML(item.path)}">用于新建</button>${download}</div></div>`;
  }).join("");
}

function kindText(kind) {
  const labels = { directory: "目录", image: "镜像", archive: "压缩包", backup: "备份" };
  return labels[kind] || kind || "未知";
}

function renderCreateLocalOptions() {
  const select = $("#createLocalRootfs");
  if (!select) return;
  if (!state.localRootfs.length) {
    select.innerHTML = `<option value="">暂无本地模板</option>`;
    return;
  }
  select.innerHTML = state.localRootfs.map((item) => `<option value="${escapeHTML(item.path)}">${escapeHTML(item.name)} (${escapeHTML(kindText(item.kind))})</option>`).join("");
}

function renderCreateCloudOptions() {
  const select = $("#createCloudRootfs");
  if (!select) return;
  select.innerHTML = state.rootfsAssets.map((asset, index) => `<option value="${index}">${escapeHTML(asset.name || "未命名")} · ${escapeHTML(asset.architecture || "-")}</option>`).join("");
}

function trackTask(taskId, onDone) {
  if (!taskId) return;
  state.tasks[taskId] = { id: taskId, onDone };
  renderTasks();
  pollTask(taskId);
}

async function pollTask(taskId) {
  try {
    const task = await api(`/api/tasks/${encodeURIComponent(taskId)}`);
    state.tasks[taskId] = { ...(state.tasks[taskId] || {}), ...task };
    renderTasks();
    if (task.status === "done") {
      if (typeof state.tasks[taskId].onDone === "function") state.tasks[taskId].onDone(task);
      await loadLocalRootfs();
      return;
    }
    if (task.status === "error") return;
    setTimeout(() => pollTask(taskId), 800);
  } catch (err) {
    toast(err.message);
  }
}

function renderTasks() {
  const list = $("#taskList");
  if (!list) return;
  const tasks = Object.values(state.tasks);
  if (!tasks.length) {
    list.innerHTML = "";
    return;
  }
  list.innerHTML = tasks.map((task) => {
    const pct = task.percent || 0;
    const status = task.status || "pending";
    const link = task.url ? `<button class="text-btn" data-download-url="${escapeHTML(task.url)}" data-download-name="${escapeHTML(task.name || task.kind || task.id)}">下载</button>` : "";
    return `<div class="task-item"><div><strong>${escapeHTML(task.name || task.kind || task.id)}</strong><span>${escapeHTML(status)} ${pct}%</span>${task.error ? `<div class="task-error">${escapeHTML(task.error)}</div>` : ""}</div><progress max="100" value="${pct}"></progress>${link}</div>`;
  }).join("");
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

async function exportContainer(name, asTemplate) {
  const action = asTemplate ? "template" : "export";
  setBusy(true);
  try {
    const data = await api(`/api/containers/${encodeURIComponent(name)}/${action}`, { method: "POST" });
    trackTask(data.taskId);
    switchView("rootfs");
    toast(asTemplate ? "已开始转换为模板" : "已开始打包备份");
  } catch (err) {
    toast(err.message);
  } finally {
    setBusy(false);
  }
}

function switchView(view) {
  state.currentView = view;
  $$(".nav-item").forEach((button) => button.classList.toggle("active", button.dataset.view === view));
  $$(".view-panel").forEach((panel) => panel.classList.remove("active"));
  $(`#${view}View`)?.classList.add("active");
  const titles = { overview: "概览", containers: "容器列表", rootfs: "RootFS 管理", diagnostics: "诊断事件" };
  $("#viewTitle").textContent = titles[view] || "Droidspaces";
  if (view === "rootfs") {
    loadLocalRootfs().catch((err) => toast(err.message));
    if (!state.rootfsAssets.length) loadRootfsAssets().catch((err) => toast(err.message));
  }
}

function updateCreateSourceUI() {
  const source = document.querySelector('input[name="createSource"]:checked')?.value || "local";
  $("#localSourceField").classList.toggle("hidden", source !== "local");
  $("#cloudSourceField").classList.toggle("hidden", source !== "cloud");
  $("#directSourceField").classList.toggle("hidden", source !== "direct");
  if (source === "cloud" && !state.rootfsAssets.length) {
    loadRootfsAssets().catch((err) => toast(err.message));
  }
}

async function downloadSelectedCloudForCreate() {
  const idx = Number($("#createCloudRootfs").value);
  const asset = state.rootfsAssets[idx];
  if (!asset) {
    toast("请先选择云端镜像");
    return;
  }
  const data = await api("/api/rootfs/download", { method: "POST", body: JSON.stringify(asset) });
  state.createCloudTaskId = data.taskId;
  trackTask(data.taskId, (task) => {
    $("#createCloudTask").textContent = `已下载：${task.path || ""}`;
  });
  $("#createCloudTask").textContent = "下载中，完成后可直接创建";
}

async function runCLI(command) {
  setBusy(true);
  $("#cliOutput").textContent = "执行中";
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

async function loadEvents() {
  const data = await api(`/api/events?since=${state.lastEventSince}`);
  const events = data.events || [];
  if (events.length > 0) state.lastEventSince = Math.max(...events.map((event) => event.time || 0));
  if (data.backendError) toast(`事件后端提示：${data.backendError}`);
  renderEvents(events);
}

function renderEvents(events) {
  const node = $("#eventsList");
  if (!events.length) {
    node.innerHTML = `<div class="empty-state">暂无事件</div>`;
    return;
  }
  node.innerHTML = events
    .slice()
    .reverse()
    .map((event) => `<div class="event"><div class="event-time">${event.time ? new Date(event.time * 1000).toLocaleTimeString() : "-"}</div><div class="event-main"><strong>${escapeHTML(event.action || "-")}</strong><span>${escapeHTML(event.actorName || event.actorId || "-")}</span></div></div>`)
    .join("");
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
  if (!button || state.busy) return;

  const action = button.dataset.action;
  const encodedName = button.dataset.name;
  if (action && encodedName) {
    const name = decodeURIComponent(encodedName);
    if (action === "inspect") inspect(name).catch((err) => toast(err.message));
    else if (action === "delete") deleteContainer(name);
    else if (action === "export") exportContainer(name, false);
    else if (action === "template") exportContainer(name, true);
    else if (action === "terminal") {
      selectTerminal(name);
      connectTerminal();
    } else runLifecycle(name, action);
    return;
  }

  if (button.dataset.rootfs) {
    try {
      downloadRootfs(JSON.parse(decodeURIComponent(button.dataset.rootfs)));
    } catch (err) {
      toast(err.message);
    }
    return;
  }

  if (button.dataset.downloadUrl) {
    downloadFile(button.dataset.downloadUrl, button.dataset.downloadName || "download").catch((err) => toast(err.message));
    return;
  }

  if (button.dataset.useLocalRootfs) {
    showCreateModal();
    document.querySelector('input[name="createSource"][value="local"]').checked = true;
    updateCreateSourceUI();
    $("#createLocalRootfs").value = button.dataset.useLocalRootfs;
    return;
  }

  if (button.dataset.cli) runCLI(button.dataset.cli);
});

function bindUI() {
  $("#refreshBtn").addEventListener("click", refreshAll);
  $("#rootfsRefreshBtn").addEventListener("click", loadRootfsAssets);
  $("#eventsBtn").addEventListener("click", () => loadEvents().catch((err) => toast(err.message)));
  $("#createBtn").addEventListener("click", showCreateModal);
  $("#createCloseBtn").addEventListener("click", hideCreateModal);
  $("#createCancelBtn").addEventListener("click", hideCreateModal);
  $("#createForm").addEventListener("submit", createContainer);
  $$(".nav-item").forEach((button) => button.addEventListener("click", () => switchView(button.dataset.view)));
  $$('input[name="createSource"]').forEach((input) => input.addEventListener("change", updateCreateSourceUI));
  $("#createCloudDownloadBtn").addEventListener("click", () => downloadSelectedCloudForCreate().catch((err) => toast(err.message)));
  $("#createCloudRootfs").addEventListener("change", () => {
    state.createCloudTaskId = "";
    $("#createCloudTask").textContent = "";
  });
  $("#terminalConnectBtn").addEventListener("click", connectTerminal);
  $("#terminalDisconnectBtn").addEventListener("click", disconnectTerminal);
  $("#terminalClearBtn").addEventListener("click", clearTerminal);
  $("#terminalSendBtn").addEventListener("click", sendTerminalInput);
  $("#terminalScreen").addEventListener("keydown", handleTerminalKey);
  $("#terminalScreen").addEventListener("paste", async (event) => {
    if (!state.terminalConnected) return;
    const text = event.clipboardData?.getData("text") || "";
    if (text && sendTerminalRaw(text)) event.preventDefault();
  });
  $("#terminalInput").addEventListener("keydown", (event) => {
    if (event.ctrlKey && event.key.toLowerCase() === "c") {
      if (sendTerminalRaw("\x03")) event.preventDefault();
      return;
    }
    if (event.ctrlKey && event.key.toLowerCase() === "d") {
      if (sendTerminalRaw("\x04")) event.preventDefault();
      return;
    }
    if (event.key === "Enter" && !event.shiftKey) {
      event.preventDefault();
      sendTerminalInput();
    }
  });
  $("#containerStateFilter").addEventListener("change", renderContainers);
  $("#filterInput").addEventListener("input", renderContainers);
  $("#closeDetailBtn").addEventListener("click", closeDetail);
  $("#loginForm").addEventListener("submit", async (event) => {
    event.preventDefault();
    const token = $("#loginToken").value.trim();
    try {
      await loginWithToken(token);
    } catch (err) {
      showLogin(err.message);
    }
  });
}

async function boot() {
  bindUI();
  if (window.DS_AUTH_REQUIRED) {
    const token = getAuthToken();
    if (!token) {
      showLogin();
      return;
    }
    try {
      await loginWithToken(token);
    } catch (err) {
      showLogin(err.message);
    }
  } else {
    state.authenticated = true;
    hideLogin();
    await refreshAll();
  }
  setInterval(() => {
    if (!state.busy && state.authenticated) refreshAll();
  }, 15000);
}

boot();
