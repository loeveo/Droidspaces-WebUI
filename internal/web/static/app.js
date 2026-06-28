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
  $("#workspacePath").textContent = data.workspace || "未知工作区";
  const info = data.info || {};
  $("#totalContainers").textContent = info.containersTotal ?? 0;
  $("#runningContainers").textContent = info.containersRunning ?? 0;
  $("#stoppedContainers").textContent = info.containersStopped ?? 0;
  if (data.backendError) toast(`后端提示：${data.backendError}`);
}

async function loadContainers() {
  const includeAll = $("#includeStopped").checked ? "1" : "0";
  const data = await api(`/api/containers?all=${includeAll}`);
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
  const rows = state.containers.filter((container) => {
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
          <td><div class="row-actions">${action}<button class="icon-btn" title="重启" aria-label="重启" data-action="restart" data-name="${encoded}">↻</button><button class="icon-btn" title="详情" aria-label="详情" data-action="inspect" data-name="${encoded}">ⓘ</button><button class="icon-btn" title="终端" aria-label="终端" data-action="terminal" data-name="${encoded}">⌁</button><button class="icon-btn danger" title="删除" aria-label="删除" data-action="delete" data-name="${encoded}">×</button></div></td>
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
  $("#detailEmpty").classList.add("hidden");
  $("#detailBody").classList.remove("hidden");
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

function appendTerminal(raw) {
  const screen = $("#terminalScreen");
  if (!screen) return;
  let text = String(raw)
    .replace(/\r\n/g, "\n")
    .replace(/\r/g, "")
    .replace(/\x1b\[[0-9;?]*[ -/]*[@-~]/g, "");
  if (!screen.dataset.active) {
    screen.textContent = "";
    screen.dataset.active = "1";
  }
  for (const ch of text) {
    if (ch === "\b" || ch === "\x7f") {
      screen.textContent = screen.textContent.slice(0, -1);
    } else if (ch !== "\x00") {
      screen.textContent += ch;
    }
  }
  if (screen.textContent.length > 200000) {
    screen.textContent = screen.textContent.slice(-160000);
  }
  screen.scrollTop = screen.scrollHeight;
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
  const screen = $("#terminalScreen");
  screen.dataset.active = "1";
  screen.textContent = `连接 ${target} (${user})...\n`;
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
  screen.dataset.active = "1";
  screen.textContent = "";
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
  const payload = {
    name: $("#createName").value.trim(),
    hostname: $("#createHostname").value.trim(),
    rootfsPath: $("#createRootfs").value.trim(),
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
  $("#createName").focus();
}

function hideCreateModal() {
  $("#createModal").classList.add("hidden");
}

function closeDetail() {
  state.selected = "";
  $("#detailTitle").textContent = "详情";
  $("#detailBody").classList.add("hidden");
  $("#detailEmpty").classList.remove("hidden");
}

async function loadRootfsAssets() {
  setBusy(true);
  const list = $("#rootfsList");
  list.innerHTML = `<div class="empty-state">加载中</div>`;
  try {
    const arch = $("#rootfsArch").value;
    const data = await api(`/api/rootfs${arch ? `?arch=${encodeURIComponent(arch)}` : ""}`);
    $("#rootfsMeta").textContent = `模板目录：${data.templateImageRoot || "-"}`;
    renderRootfsAssets(data.assets || [], data.errors || []);
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
    toast(`已下载到 ${data.path}`);
  } catch (err) {
    toast(err.message);
  } finally {
    setBusy(false);
  }
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
  $("#includeStopped").addEventListener("change", refreshAll);
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
