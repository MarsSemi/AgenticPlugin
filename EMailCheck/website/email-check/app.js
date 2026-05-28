const state = {
  accounts: [],
  accountChecks: {},
  selectedAccount: "",
  editingAccountID: "",
	  selectedMessage: null,
	  messages: [],
	  autoChecks: [],
	  selectedAutoTaskID: "",
	  activeModule: "accountModule",
  activeDetailTab: "content",
  readState: {},
  activeMessageToken: 0,
};

const $ = (id) => document.getElementById(id);
const moduleOrderStorageKey = "email-check.module-order.v1";
const moduleNav = document.querySelector(".module-nav");
const logEl = $("log");
const serviceStatus = $("serviceStatus");
const accountTableBody = $("accountTableBody");
const mailAccountSelect = $("mailAccountSelect");
const accountForm = $("accountForm");
const accountStatus = $("accountStatus");
const accountCheckMessage = $("accountCheckMessage");
const saveAccountBtn = $("saveAccountBtn");
const messageList = $("messageList");
const messageListTitle = $("messageListTitle");
const markAllReadBtn = $("markAllReadBtn");
const messageDetail = $("messageDetail");
const replyForm = $("replyForm");
const aiReplyBtn = $("aiReplyBtn");
const aiReplyDialog = $("aiReplyDialog");
const aiReplyStatus = $("aiReplyStatus");
const cancelAiReplyBtn = $("cancelAiReplyBtn");
const autoTaskList = $("autoTaskList");
const autoTaskListTitle = $("autoTaskListTitle");
const autoTaskResult = $("autoTaskResult");
const autoTaskDialog = $("autoTaskDialog");
const autoTaskForm = $("autoTaskForm");
const autoTaskStatus = $("autoTaskStatus");
const saveAutoTaskBtn = $("saveAutoTaskBtn");
const accountFields = accountForm.elements;
const autoTaskFields = autoTaskForm.elements;
let accountStatusTimer = null;
let accountCheckMessageTimer = null;
let autoTaskStatusTimer = null;
let markReadTimer = null;
let aiReplyAbortController = null;
let draggedModuleTab = null;

function log(message, data) {
  if (window.console?.debug) {
    console.debug("[email-check]", message, data || "");
  }
}

async function api(path, options = {}) {
  const normalized = normalizePath(path);
  const method = options.method || "GET";
  const body = requestBody(options.body);
  const requestOptions = jsonRequestOptions({ ...options, method, body });
  if (typeof window.AgenticTalkAPI?.apiFetch === "function") {
    const result = await window.AgenticTalkAPI.apiFetch(pluginGatewayPath(normalized), requestOptions);
    return decodeAPIResult(result, "外掛 API 沒有回傳有效結果");
  }
  return rawJSON(pluginGatewayPath(normalized), requestOptions);
}

function normalizePath(path) {
  return String(path || "").startsWith("/") ? String(path || "") : `/${path || ""}`;
}

function pluginGatewayPath(path) {
  return `/api/plugin/email-check${normalizePath(path)}`;
}

function jsonRequestOptions(options = {}) {
  return {
    ...options,
    credentials: options.credentials || "same-origin",
    headers: {
      "Content-Type": "application/json",
      ...authHeadersFromCookie(),
      ...(options.headers || {}),
    },
  };
}

async function decodeAPIResult(result, invalidMessage) {
  if (typeof Response !== "undefined" && result instanceof Response) {
    const text = await result.text();
    let data = {};
    if (text) {
      try {
        data = JSON.parse(text);
      } catch {
        data = { success: false, error: text };
      }
    }
    if (!result.ok && !data.error) {
      data.error = `HTTP ${result.status}`;
    }
    return data;
  }
  if (!result || typeof result !== "object") {
    throw new Error(invalidMessage);
  }
  return result;
}

async function rawJSON(path, options = {}) {
  let response;
  try {
    response = await fetch(path, {
      credentials: "same-origin",
      ...options,
    });
  } catch (error) {
    throw new Error(`無法連線 EMAIL 外掛服務：${error.message || error}`);
  }
  const text = await response.text();
  let data = {};
  if (text) {
    try {
      data = JSON.parse(text);
    } catch {
      data = { success: false, error: text };
    }
  }
  if (!response.ok) {
    data.status = response.status;
    data.error = data.error || `HTTP ${response.status}`;
  }
  return data;
}

async function pluginControl(path, options = {}) {
  const normalized = normalizePath(path);
  const method = options.method || "GET";
  const body = requestBody(options.body);
  const requestOptions = jsonRequestOptions({ ...options, method, body });
  if (typeof window.AgenticTalkAPI?.apiFetch === "function") {
    const result = await window.AgenticTalkAPI.apiFetch(`/api/plugin/email-check/_plugin${normalized}`, requestOptions);
    return decodeAPIResult(result, "外掛控制 API 沒有回傳有效結果");
  }
  const gateway = await rawJSON(`/api/plugin/email-check/_plugin${normalized}`, requestOptions);
  if (gateway.success || gateway.status !== 404) {
    return gateway;
  }
  const command = normalized.replace(/^\/+/, "") || "status";
  return rawJSON(`/api/email-check/plugin/${command}`, requestOptions);
}

function requestBody(value) {
  if (value === undefined || value === null) return undefined;
  if (typeof FormData !== "undefined" && value instanceof FormData) return value;
  if (typeof value === "string") return value;
  return JSON.stringify(value);
}

function authHeadersFromCookie() {
  const token = readCookieToken();
  if (!token) return {};
  const value = token.toLowerCase().startsWith("bearer ") ? token : `Bearer ${token}`;
  return {
    Authentication: value,
    Authorization: value,
  };
}

function readCookieToken() {
  const names = ["agentic_auth_token", "auth_token", "authToken", "token", "Authentication", "Authorization"];
  const cookies = String(document.cookie || "").split(";").map((part) => part.trim()).filter(Boolean);
  for (const name of names) {
    const encodedPrefix = `${encodeURIComponent(name)}=`;
    const plainPrefix = `${name}=`;
    const found = cookies.find((cookie) => cookie.startsWith(encodedPrefix) || cookie.startsWith(plainPrefix));
    if (!found) continue;
    const raw = found.slice(found.indexOf("=") + 1);
    try {
      return decodeURIComponent(raw).trim();
    } catch {
      return raw.trim();
    }
  }
  return "";
}

function setModule(moduleID) {
  state.activeModule = moduleID;
  document.querySelectorAll(".module").forEach((module) => {
    module.classList.toggle("active", module.id === moduleID);
  });
  document.querySelectorAll("[data-module-target]").forEach((button) => {
    button.classList.toggle("active", button.dataset.moduleTarget === moduleID);
  });
}

function initializeModuleOrdering() {
  applyStoredModuleOrder();
  bindModuleDragSorting();
}

function moduleTabs() {
  return [...document.querySelectorAll("[data-module-target]")];
}

function applyStoredModuleOrder() {
  if (!moduleNav) return;
  let order = [];
  try {
    order = JSON.parse(localStorage.getItem(moduleOrderStorageKey) || "[]");
  } catch {
    order = [];
  }
  if (!Array.isArray(order) || !order.length) return;
  const tabs = moduleTabs();
  const byID = new Map(tabs.map((tab) => [tab.dataset.moduleTarget, tab]));
  for (const moduleID of order) {
    const tab = byID.get(moduleID);
    if (tab) moduleNav.append(tab);
  }
  for (const tab of tabs) {
    if (!order.includes(tab.dataset.moduleTarget)) {
      moduleNav.append(tab);
    }
  }
}

function bindModuleDragSorting() {
  if (!moduleNav) return;
  moduleTabs().forEach((tab) => {
    tab.draggable = true;
    tab.addEventListener("dragstart", (event) => {
      draggedModuleTab = tab;
      tab.classList.add("dragging");
      moduleNav.classList.add("drag-sorting");
      event.dataTransfer.effectAllowed = "move";
      event.dataTransfer.setData("text/plain", tab.dataset.moduleTarget || "");
    });
    tab.addEventListener("dragend", () => finishModuleDrag());
  });
  moduleNav.addEventListener("dragover", (event) => {
    if (!draggedModuleTab) return;
    event.preventDefault();
    const target = event.target.closest("[data-module-target]");
    if (!target || target === draggedModuleTab || !moduleNav.contains(target)) return;
    const rect = target.getBoundingClientRect();
    const before = event.clientY < rect.top + rect.height / 2;
    moduleNav.insertBefore(draggedModuleTab, before ? target : target.nextSibling);
  });
  moduleNav.addEventListener("drop", (event) => {
    if (!draggedModuleTab) return;
    event.preventDefault();
    persistModuleOrder();
    finishModuleDrag();
  });
}

function finishModuleDrag() {
  if (draggedModuleTab) {
    persistModuleOrder();
    draggedModuleTab.classList.remove("dragging");
  }
  draggedModuleTab = null;
  moduleNav?.classList.remove("drag-sorting");
}

function persistModuleOrder() {
  try {
    const order = moduleTabs().map((tab) => tab.dataset.moduleTarget).filter(Boolean);
    localStorage.setItem(moduleOrderStorageKey, JSON.stringify(order));
  } catch (error) {
    log(`儲存功能排序失敗：${error.message || error}`);
  }
}

async function loadAccounts() {
  const result = await api("/api/email-check/accounts");
  if (!result.success) throw new Error(result.error || "讀取帳號失敗");
  state.accounts = result.accounts || [];
  if (state.selectedAccount && !state.accounts.some((account) => account.id === state.selectedAccount)) {
    state.selectedAccount = "";
    state.editingAccountID = "";
  }
	  renderAccounts();
	  populateAutoTaskAccountOptions();
	  if (!state.selectedAccount && state.accounts.length) {
    state.selectedAccount = state.accounts[0].id;
    state.editingAccountID = state.selectedAccount;
  }
  syncAccountSelection();
  fillAccountForm(currentAccount());
	  updateAccountControls();
	}

async function loadAutoChecks() {
  const result = await api("/api/email-check/auto-checks");
  if (!result.success) throw new Error(result.error || "讀取自動檢測任務失敗");
  state.autoChecks = result.tasks || [];
  if (state.selectedAutoTaskID && !state.autoChecks.some((task) => task.id === state.selectedAutoTaskID)) {
    state.selectedAutoTaskID = "";
  }
  renderAutoChecks();
}

function populateAutoTaskAccountOptions() {
  const select = autoTaskFields.account_id;
  if (!select) return;
  const current = select.value || state.selectedAccount;
  select.innerHTML = "";
  for (const account of state.accounts) {
    const option = document.createElement("option");
    option.value = account.id;
    option.textContent = account.name || account.email || account.id;
    select.append(option);
  }
  select.value = state.accounts.some((account) => account.id === current) ? current : (state.selectedAccount || state.accounts[0]?.id || "");
}

function renderAccounts() {
  accountTableBody.innerHTML = "";
  mailAccountSelect.innerHTML = "";
  if (!state.accounts.length) {
    const row = document.createElement("tr");
    row.className = "empty-row";
    row.innerHTML = `<td colspan="3">尚未建立 EMAIL 帳號</td>`;
    accountTableBody.append(row);
  }
  for (const account of state.accounts) {
    const label = account.name || account.email || account.id;
    const check = accountCheckFor(account);
    const statusIcon = accountStatusIcon(check.status, check.message);
    const row = document.createElement("tr");
    row.dataset.accountId = account.id;
    row.tabIndex = 0;
    row.innerHTML = `
      <td title="${escapeHTML(check.message)}">${statusIcon}</td>
      <td title="${escapeHTML(account.name || account.email || "")}">${escapeHTML(account.name || account.email || "")}</td>
      <td title="${escapeHTML(account.email || "")}">${escapeHTML(account.email || "")}</td>
    `;
    row.addEventListener("click", () => selectAccount(account.id, true));
    row.addEventListener("keydown", (event) => {
      if (event.key === "Enter" || event.key === " ") {
        event.preventDefault();
        selectAccount(account.id, true);
      }
    });
    accountTableBody.append(row);

    const mailOption = document.createElement("option");
    mailOption.value = account.id;
    mailOption.textContent = label;
    mailAccountSelect.append(mailOption);
  }
}

function accountCheckFor(account) {
  if (!account) {
    return { status: "unknown", message: "尚未登入檢查" };
  }
  const live = state.accountChecks[account.id];
  if (live) return live;
  const persisted = persistedAccountCheck(account.last_check);
  if (persisted) return persisted;
  return {
    status: "unknown",
    message: account.enabled ? "尚未登入檢查" : "帳號已停用，尚未登入檢查",
  };
}

function persistedAccountCheck(lastCheck) {
  const status = String(lastCheck?.status || "").toLowerCase();
  if (status !== "ok" && status !== "error") return null;
  const message = lastCheck.message || (status === "ok" ? "上次登入檢查成功" : "上次登入檢查失敗");
  const checkedAt = lastCheck.checked_at ? new Date(lastCheck.checked_at) : null;
  const checkedAtText = checkedAt && !Number.isNaN(checkedAt.getTime()) ? checkedAt.toLocaleString() : "";
  return {
    status,
    message: checkedAtText ? `${message}（${checkedAtText}）` : message,
  };
}

function syncAccountSelection() {
  mailAccountSelect.value = state.selectedAccount;
  accountTableBody.querySelectorAll("tr[data-account-id]").forEach((row) => {
    row.classList.toggle("active", row.dataset.accountId === state.selectedAccount);
  });
  renderAccountCheckMessage();
}

function selectAccount(accountID, fillForm) {
  state.selectedAccount = accountID;
  state.editingAccountID = accountID;
  syncAccountSelection();
  if (fillForm) {
    fillAccountForm(currentAccount());
  }
  updateAccountControls();
}

function currentAccount() {
  return state.accounts.find((account) => account.id === state.selectedAccount) || null;
}

function fillAccountForm(account) {
  setAccountStatus("");
  const data = account || {
    id: "",
    name: "",
    email: "",
    from_name: "",
    username: "",
    default_folder: "INBOX",
    enabled: true,
    incoming_protocol: "imap",
    imap: { host: "", port: 993, tls: true, start_tls: false },
    pop3: { host: "", port: 995, tls: true, start_tls: false },
    smtp: { host: "", port: 465, tls: true, start_tls: false },
  };
  const protocol = incomingProtocol(data);
  const incoming = incomingServer(data, protocol);
  accountFields.id.value = data.id || "";
  accountFields.id.readOnly = Boolean(account?.id);
  accountFields.name.value = data.name || "";
  accountFields.email.value = data.email || "";
  accountFields.from_name.value = data.from_name || "";
  accountFields.username.value = data.username || "";
  accountFields.password.value = "";
  accountFields.default_folder.value = data.default_folder || "INBOX";
  accountFields.enabled.checked = Boolean(data.enabled);
  accountFields.incoming_protocol.value = protocol;
  accountFields.imap_host.value = incoming.host || "";
  accountFields.imap_port.value = incoming.port || incomingDefaultPort(protocol);
  accountFields.imap_tls.checked = incoming.tls !== false;
  accountFields.imap_start_tls.checked = Boolean(incoming.start_tls);
  accountFields.smtp_host.value = data.smtp?.host || "";
  accountFields.smtp_port.value = data.smtp?.port || 465;
  accountFields.smtp_tls.checked = data.smtp?.tls !== false;
  accountFields.smtp_start_tls.checked = Boolean(data.smtp?.start_tls);
  updateIncomingProtocolControls();
  updateAccountControls();
}

function formAccountPayload() {
  const account = currentAccount();
  const protocol = incomingProtocol({ incoming_protocol: accountFields.incoming_protocol.value });
  const incoming = {
    host: accountFields.imap_host.value.trim(),
    port: Number(accountFields.imap_port.value || incomingDefaultPort(protocol)),
    tls: accountFields.imap_tls.checked,
    start_tls: accountFields.imap_start_tls.checked,
  };
  const imap = { ...(account?.imap || {}) };
  const pop3 = { ...(account?.pop3 || {}) };
  if (protocol === "pop3") {
    Object.assign(pop3, incoming);
  } else {
    Object.assign(imap, incoming);
  }
  return {
    id: accountFields.id.value.trim(),
    name: accountFields.name.value.trim(),
    email: accountFields.email.value.trim(),
    from_name: accountFields.from_name.value.trim(),
    username: accountFields.username.value.trim(),
    password: accountFields.password.value,
    default_folder: accountFields.default_folder.value.trim() || "INBOX",
    enabled: accountFields.enabled.checked,
    incoming_protocol: protocol,
    imap,
    pop3,
    smtp: {
      host: accountFields.smtp_host.value.trim(),
      port: Number(accountFields.smtp_port.value || 465),
      tls: accountFields.smtp_tls.checked,
      start_tls: accountFields.smtp_start_tls.checked,
    },
  };
}

function incomingProtocol(account) {
  return String(account?.incoming_protocol || "imap").toLowerCase() === "pop3" ? "pop3" : "imap";
}

function incomingServer(account, protocol) {
  const server = protocol === "pop3" ? account?.pop3 : account?.imap;
  return server || { host: "", port: incomingDefaultPort(protocol), tls: true, start_tls: false };
}

function incomingDefaultPort(protocol) {
  return protocol === "pop3" ? 995 : 993;
}

function updateIncomingProtocolControls() {
  const protocol = incomingProtocol({ incoming_protocol: accountFields.incoming_protocol.value });
  const label = protocol.toUpperCase();
  $("incomingHostLabel").textContent = `${label} Host`;
  $("incomingPortLabel").textContent = `${label} Port`;
  $("incomingTlsLabel").textContent = `${label} TLS`;
  $("incomingStartTlsLabel").textContent = `${label} STARTTLS`;
  accountFields.imap_host.placeholder = protocol === "pop3" ? "pop.example.com" : "imap.example.com";
  if (!accountFields.imap_port.value) {
    accountFields.imap_port.value = incomingDefaultPort(protocol);
  }
}

function validateAccountPayload(payload) {
  const missing = [];
  if (!payload.email) missing.push("Email");
  if (!payload.username) missing.push("登入帳號");
  if (payload.incoming_protocol === "pop3") {
    if (!payload.pop3.host) missing.push("POP3 Host");
  } else if (!payload.imap.host) {
    missing.push("IMAP Host");
  }
  if (missing.length) {
    throw new Error(`請填寫必要欄位：${missing.join("、")}`);
  }
}

async function saveAccount(event) {
  event.preventDefault();
  setSaving(true);
  try {
    const payload = formAccountPayload();
    validateAccountPayload(payload);
    const editingID = state.editingAccountID;
    const path = editingID ? `/api/email-check/accounts/${encodeURIComponent(editingID)}` : "/api/email-check/accounts";
    setAccountStatus("儲存中...", "info");
    const result = await api(path, { method: path.endsWith("/accounts") ? "POST" : "PUT", body: payload });
    if (!result.success) throw new Error(result.error || "儲存帳號失敗");
    state.selectedAccount = result.account.id;
    state.editingAccountID = result.account.id;
    await loadAccounts();
    setAccountStatus(result.persisted ? `已永久儲存至 ${result.config_path}` : "帳號已儲存", "success");
    log(result.persisted ? "帳號已永久儲存" : "帳號已儲存", result);
  } finally {
    setSaving(false);
  }
}

async function loginCheck() {
  const account = currentAccount();
  if (!account) {
    log("請先選擇或建立 EMAIL 帳號");
    return;
  }
  const payload = formAccountPayload();
  validateAccountPayload(payload);
  const passwordProvided = accountFields.password.value.length > 0;
  const passwordSourceLabel = passwordProvided ? "表單密碼" : "已儲存密碼";
  setAccountCheck(account.id, "checking", `登入檢查中... 使用${passwordSourceLabel}`);
  const result = await api("/api/email-check/login", {
    method: "POST",
    body: { account_id: account.id, account: payload, persist_on_success: true, password_provided: passwordProvided },
  });
  if (result.success) {
    const savedID = result.account?.id || account.id;
    if (result.persisted && result.account) {
      state.selectedAccount = result.account.id;
      state.editingAccountID = result.account.id;
      await loadAccounts();
      setAccountStatus(`登入檢查成功，已永久儲存至 ${result.config_path}`, "success");
    }
    setAccountCheck(savedID, "ok", result.persisted ? `登入檢查成功，已永久儲存至 ${result.config_path}` : "登入檢查成功");
  } else {
    setAccountCheck(account.id, "error", loginErrorMessage(result));
  }
  log(result.success ? "登入檢查成功" : "登入檢查失敗", result.result || result);
}

function setAccountCheck(accountID, status, message) {
  if (!accountID) return;
  state.accountChecks[accountID] = { status, message };
  renderAccounts();
  syncAccountSelection();
  renderAccountCheckMessage();
  if (accountCheckMessageTimer) {
    clearTimeout(accountCheckMessageTimer);
    accountCheckMessageTimer = null;
  }
  if (status === "ok") {
    accountCheckMessageTimer = window.setTimeout(() => {
      const current = state.accountChecks[accountID];
      if (current?.status === "ok") {
        current.message = "";
        renderAccountCheckMessage();
      }
    }, 3000);
  }
}

function accountStatusIcon(status, message) {
  const map = {
    ok: ["fa-circle-check", "ok", "連線正常"],
    error: ["fa-circle-xmark", "error", "連線失敗"],
    checking: ["fa-circle-notch fa-spin", "checking", "檢查中"],
    unknown: ["fa-circle-question", "unknown", "尚未檢查"],
  };
  const [icon, className, label] = map[status] || map.unknown;
  return `<i class="fa-solid ${icon} account-status-icon ${className}" aria-hidden="true"></i><span class="sr-only">${escapeHTML(message || label)}</span>`;
}

function loginErrorMessage(result) {
  const incoming = result?.result?.incoming || result?.result?.imap;
  const message = incoming?.error || result?.result?.smtp?.error || result?.error || "登入檢查失敗";
  const attempts = incoming?.attempts || [];
  const passwordSource = result?.result?.diagnostics?.password_source;
  const passwordSourceLabel = passwordSource === "form" ? "表單密碼" : "已儲存密碼";
  if (message === "account.password is required") {
    return "請輸入密碼 / App Password 後再登入檢查。";
  }
  if (message.includes("mismatched password")) {
    const attempted = attempts.map((attempt) => attempt.username).filter(Boolean).join("、");
    return `伺服器回報密碼不相符；本次使用${passwordSourceLabel}${attempted ? `，登入帳號 ${attempted}` : ""}。請重新輸入密碼 / App Password 後再登入檢查。`;
  }
  if (attempts.length) {
    const attempted = attempts.map((attempt) => attempt.username).filter(Boolean).join("、");
    if (message.includes("server closed connection after Password prompt")) {
      return `伺服器在密碼驗證後關閉連線；登入帳號 ${attempted}。請確認登入帳號、密碼 / App Password 或 IMAP 權限。`;
    }
    return `${message}；登入帳號 ${attempted}`;
  }
  if (message.includes("server closed connection after Password prompt")) {
    return "伺服器在密碼驗證後關閉連線，請確認登入帳號、密碼 / App Password 或 IMAP 權限。";
  }
  return message;
}

function renderAccountCheckMessage() {
  if (!accountCheckMessage) return;
  const check = state.accountChecks[state.selectedAccount];
  if (!check || check.status === "unknown") {
    accountCheckMessage.textContent = "";
    accountCheckMessage.className = "inline-status";
    return;
  }
  if (check.status === "ok" && !check.message) {
    accountCheckMessage.textContent = "";
    accountCheckMessage.className = "inline-status";
    return;
  }
  const label = {
    ok: "登入檢查成功",
    error: "登入檢查失敗",
    checking: "登入檢查中...",
  }[check.status] || "登入檢查";
  accountCheckMessage.textContent = `${label}${check.message ? `：${check.message}` : ""}`;
  accountCheckMessage.className = `inline-status ${check.status}`;
}

function renderAutoChecks() {
  autoTaskList.innerHTML = "";
  autoTaskListTitle.textContent = `檢測任務 ${state.autoChecks.length}`;
  if (!state.autoChecks.length) {
    autoTaskList.innerHTML = `<div class="auto-empty">尚未建立檢測任務</div>`;
    autoTaskResult.className = "auto-result-empty";
    autoTaskResult.textContent = "尚未選取任務";
    return;
  }
  if (!state.selectedAutoTaskID) {
    state.selectedAutoTaskID = state.autoChecks[0].id;
  }
  for (const task of state.autoChecks) {
    const account = accountByID(task.account_id);
    const result = task.last_result || {};
    const item = document.createElement("article");
    item.className = "auto-task-card";
    item.dataset.taskId = task.id;
    item.classList.toggle("active", task.id === state.selectedAutoTaskID);
    item.innerHTML = `
      <div class="auto-task-head">
        <div>
          <strong>${escapeHTML(task.name || "未命名任務")}</strong>
          <div class="meta">${escapeHTML(account?.name || account?.email || task.account_id || "")} · 每 ${Number(task.interval_minutes || 60)} 分鐘</div>
        </div>
        ${autoTaskStatusPill(task)}
      </div>
      <div class="auto-task-meta">
        <span>${task.enabled ? "啟用" : "停用"}</span>
        <span>${escapeHTML(task.folder || "INBOX")}</span>
        <span>${Number(task.since_days || 7)} 天</span>
        <span>${task.unread_only ? "未讀" : "全部"}</span>
      </div>
      <div class="auto-task-run">${escapeHTML(result.message || "尚未執行")}</div>
      <div class="auto-task-actions">
        <button type="button" data-action="run"><i class="fa-solid fa-play" aria-hidden="true"></i><span>執行</span></button>
        <button type="button" class="ghost-button" data-action="edit"><i class="fa-solid fa-pen" aria-hidden="true"></i><span>編輯</span></button>
        <button type="button" class="danger" data-action="delete"><i class="fa-solid fa-trash" aria-hidden="true"></i><span>刪除</span></button>
      </div>
    `;
    item.addEventListener("click", () => selectAutoTask(task.id));
    item.querySelector('[data-action="run"]').addEventListener("click", (event) => {
      event.stopPropagation();
      runAutoTask(task.id).catch(handleError);
    });
    item.querySelector('[data-action="edit"]').addEventListener("click", (event) => {
      event.stopPropagation();
      openAutoTaskDialog(task);
    });
    item.querySelector('[data-action="delete"]').addEventListener("click", (event) => {
      event.stopPropagation();
      deleteAutoTask(task.id).catch(handleError);
    });
    autoTaskList.append(item);
  }
  renderAutoTaskResult(currentAutoTask());
}

function autoTaskStatusPill(task) {
  const result = task.last_result || {};
  const status = String(result.status || "").toLowerCase();
  if (status === "ok") {
    return `<span class="status-pill mini ready"><i class="fa-solid fa-circle-check" aria-hidden="true"></i><span>正常</span></span>`;
  }
  if (status === "error") {
    return `<span class="status-pill mini error"><i class="fa-solid fa-circle-xmark" aria-hidden="true"></i><span>錯誤</span></span>`;
  }
  return `<span class="status-pill mini checking"><i class="fa-solid fa-circle-question" aria-hidden="true"></i><span>未執行</span></span>`;
}

function selectAutoTask(taskID) {
  state.selectedAutoTaskID = taskID;
  renderAutoChecks();
}

function currentAutoTask() {
  return state.autoChecks.find((task) => task.id === state.selectedAutoTaskID) || null;
}

function accountByID(id) {
  return state.accounts.find((account) => account.id === id) || null;
}

function renderAutoTaskResult(task) {
  if (!task) {
    autoTaskResult.className = "auto-result-empty";
    autoTaskResult.textContent = "尚未選取任務";
    return;
  }
  const result = task.last_result || {};
  const matched = result.matched_uids || [];
  autoTaskResult.className = "auto-result-body";
  autoTaskResult.innerHTML = `
    <div class="auto-result-title">
      <strong>${escapeHTML(task.name || "未命名任務")}</strong>
      ${autoTaskStatusPill(task)}
    </div>
    <dl class="result-grid">
      <div><dt>帳號</dt><dd>${escapeHTML(accountByID(task.account_id)?.email || task.account_id || "")}</dd></div>
      <div><dt>下次執行</dt><dd>${escapeHTML(formatDateTime(task.next_run) || "未排程")}</dd></div>
      <div><dt>上次執行</dt><dd>${escapeHTML(formatDateTime(task.last_run) || "尚未執行")}</dd></div>
      <div><dt>符合信件</dt><dd>${Number(result.matched_count || 0)} / ${Number(result.scanned_count || 0)}</dd></div>
      <div><dt>LINE</dt><dd>${escapeHTML(lineStatusText(task, result))}</dd></div>
    </dl>
    <div class="result-message">${escapeHTML(result.message || "尚未執行")}</div>
    <div class="result-uids">${matched.length ? matched.map((uid) => `<code>${escapeHTML(uid)}</code>`).join("") : ""}</div>
  `;
}

function lineStatusText(task, result) {
  if (!task.line?.enabled) return "未啟用";
  if (result.line_status === "reserved") return "已預留，尚未實作";
  return result.line_status || "已預留";
}

function formatDateTime(value) {
  if (!value) return "";
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return value;
  return date.toLocaleString();
}

function openAutoTaskDialog(task = null) {
  populateAutoTaskAccountOptions();
  setAutoTaskStatus("");
  const data = task || {
    id: "",
    name: "",
    enabled: true,
    account_id: state.selectedAccount || state.accounts[0]?.id || "",
    folder: "INBOX",
    unread_only: true,
    since_days: 7,
    interval_minutes: 60,
    subject_keywords: [],
    from_keywords: [],
    body_keywords: [],
    prompt: "",
    line: { enabled: false, room_id: "" },
  };
  autoTaskFields.id.value = data.id || "";
  autoTaskFields.account_id.value = data.account_id || "";
  autoTaskFields.name.value = data.name || "";
  autoTaskFields.interval_minutes.value = data.interval_minutes || 60;
  autoTaskFields.folder.value = data.folder || "INBOX";
  autoTaskFields.since_days.value = data.since_days || 7;
  autoTaskFields.enabled.checked = data.enabled !== false;
  autoTaskFields.unread_only.checked = data.unread_only !== false;
  autoTaskFields.subject_keywords.value = keywordText(data.subject_keywords);
  autoTaskFields.from_keywords.value = keywordText(data.from_keywords);
  autoTaskFields.body_keywords.value = keywordText(data.body_keywords);
  autoTaskFields.prompt.value = data.prompt || "";
  autoTaskFields.line_room_id.value = data.line?.room_id || "";
  autoTaskFields.line_enabled.checked = Boolean(data.line?.enabled);
  if (typeof autoTaskDialog.showModal === "function" && !autoTaskDialog.open) {
    autoTaskDialog.showModal();
  } else {
    autoTaskDialog.setAttribute("open", "");
  }
}

function closeAutoTaskDialog() {
  if (autoTaskDialog.open && typeof autoTaskDialog.close === "function") {
    autoTaskDialog.close();
  } else {
    autoTaskDialog.removeAttribute("open");
  }
}

function keywordText(values) {
  return (values || []).join("\n");
}

function parseKeywordText(value) {
  return String(value || "")
    .split(/[\n,，\t]/)
    .map((part) => part.trim())
    .filter(Boolean);
}

function autoTaskPayload() {
  return {
    id: autoTaskFields.id.value.trim(),
    account_id: autoTaskFields.account_id.value,
    name: autoTaskFields.name.value.trim(),
    enabled: autoTaskFields.enabled.checked,
    folder: autoTaskFields.folder.value.trim() || "INBOX",
    unread_only: autoTaskFields.unread_only.checked,
    since_days: Number(autoTaskFields.since_days.value || 7),
    interval_minutes: Number(autoTaskFields.interval_minutes.value || 60),
    subject_keywords: parseKeywordText(autoTaskFields.subject_keywords.value),
    from_keywords: parseKeywordText(autoTaskFields.from_keywords.value),
    body_keywords: parseKeywordText(autoTaskFields.body_keywords.value),
    prompt: autoTaskFields.prompt.value.trim(),
    line: {
      enabled: autoTaskFields.line_enabled.checked,
      room_id: autoTaskFields.line_room_id.value.trim(),
    },
  };
}

async function saveAutoTask(event) {
  event.preventDefault();
  const payload = autoTaskPayload();
  if (!payload.account_id) throw new Error("請先建立或選擇 EMAIL 帳號");
  if (!payload.name) throw new Error("請填寫任務名稱");
  saveAutoTaskBtn.disabled = true;
  setAutoTaskStatus("儲存中...", "info");
  try {
    const editingID = payload.id;
    const path = editingID ? `/api/email-check/auto-checks/${encodeURIComponent(editingID)}` : "/api/email-check/auto-checks";
    const result = await api(path, { method: editingID ? "PUT" : "POST", body: payload });
    if (!result.success) throw new Error(result.error || "儲存檢測任務失敗");
    state.selectedAutoTaskID = result.task?.id || editingID;
    await loadAutoChecks();
    setAutoTaskStatus(`已永久儲存至 ${result.config_path}`, "success");
    window.setTimeout(closeAutoTaskDialog, 350);
  } finally {
    saveAutoTaskBtn.disabled = false;
  }
}

async function runAutoTask(taskID) {
  const result = await api(`/api/email-check/auto-checks/${encodeURIComponent(taskID)}/run`, { method: "POST", body: {} });
  if (!result.success) throw new Error(result.error || "執行檢測任務失敗");
  state.selectedAutoTaskID = result.task?.id || taskID;
  await loadAutoChecks();
  log("自動檢測任務已執行", result.result);
}

async function deleteAutoTask(taskID) {
  const task = state.autoChecks.find((item) => item.id === taskID);
  const confirmed = window.confirm(`確認刪除檢測任務「${task?.name || taskID}」？`);
  if (!confirmed) return;
  const result = await api(`/api/email-check/auto-checks/${encodeURIComponent(taskID)}`, { method: "DELETE" });
  if (!result.success) throw new Error(result.error || "刪除檢測任務失敗");
  state.selectedAutoTaskID = "";
  await loadAutoChecks();
}

function setAutoTaskStatus(message, type = "info") {
  autoTaskStatus.textContent = message || "";
  autoTaskStatus.className = message ? `form-status show ${type}` : "form-status";
  if (autoTaskStatusTimer) {
    clearTimeout(autoTaskStatusTimer);
    autoTaskStatusTimer = null;
  }
  if (message && type === "success") {
    autoTaskStatusTimer = window.setTimeout(() => {
      if (autoTaskStatus.classList.contains("success")) {
        setAutoTaskStatus("");
      }
    }, 3000);
  }
}

function listRequest(unreadOnly) {
  return {
    account_id: state.selectedAccount,
    folder: $("folderInput").value.trim() || "INBOX",
    since_days: Number($("sinceDaysInput").value || 7),
    unread_only: unreadOnly ?? $("unreadOnlyInput").checked,
    include_body: false,
  };
}

async function readMessages(unreadOnly) {
  if (!state.selectedAccount) {
    log("請先選擇 EMAIL 帳號");
    return;
  }
  const result = await api(unreadOnly ? "/api/email-check/check" : "/api/email-check/messages", {
    method: "POST",
    body: listRequest(unreadOnly),
  });
  if (!result.success) throw new Error(result.error || "讀取信件失敗");
  renderMessages(result.messages || []);
  log(unreadOnly ? `未讀信件 ${result.unread_count || 0} 封` : `讀取 ${result.count || 0} 封信`, result);
}

function renderMessages(messages) {
  cancelMarkReadTimer();
  state.messages = messages || [];
  messageList.innerHTML = "";
  state.selectedMessage = null;
  setDetailTab("content");
  messageDetail.className = "detail-empty";
  messageDetail.textContent = state.messages.length ? "請選取信件" : "沒有符合條件的信件";
  for (const message of state.messages) {
    const item = document.createElement("button");
    item.type = "button";
    item.className = "message-item";
    item.dataset.uid = message.uid || "";
    setMessageItemReadState(item, messageIsRead(message));
    item.innerHTML = `
      <span class="message-item-head">
        <strong>${escapeHTML(message.subject || "(無主旨)")}</strong>
        ${messageReadPill(message)}
      </span>
      <span class="meta">${escapeHTML(message.from || "")}</span>
      <span class="meta">${escapeHTML(message.date || "")} · UID ${escapeHTML(message.uid || "")}</span>
      <span>${escapeHTML(message.text_preview || "")}</span>
    `;
    item.addEventListener("click", () => loadMessage(message.uid, item, message).catch(handleMessageLoadError));
    messageList.append(item);
  }
  updateMessageListSummary();
}

async function loadMessage(uid, item, summaryMessage) {
  if (!uid) throw new Error("信件 UID 不存在，無法讀取內容");
  cancelMarkReadTimer();
  const token = ++state.activeMessageToken;
  [...messageList.querySelectorAll(".message-item")].forEach((node) => node.classList.remove("active"));
  item.classList.add("active");
  state.selectedMessage = summaryMessage || null;
  setDetailTab("content");
  messageDetail.className = "detail-empty";
  messageDetail.textContent = "讀取信件內容中...";
  const result = await api("/api/email-check/messages", {
    method: "POST",
    body: {
      ...listRequest(false),
      uids: [uid],
      include_body: true,
      mark_seen: false,
    },
  });
  if (!result.success) throw new Error(result.error || "讀取信件內容失敗");
  const message = result.message || result.messages?.[0];
  if (!message) throw new Error("找不到信件內容");
  state.selectedMessage = message;
  renderDetail(message);
  scheduleMarkRead(uid, item, token);
}

function handleMessageLoadError(error) {
  const message = error.message || String(error);
  setDetailTab("content");
  messageDetail.className = "detail-empty";
  messageDetail.textContent = `讀取信件內容失敗：${message}`;
  log(`讀取信件內容失敗：${message}`);
}

function messageReadPill(message) {
  const read = messageIsRead(message);
  return `<span class="read-pill ${read ? "read" : "unread"}" data-read-pill>${read ? "已讀" : "未讀"}</span>`;
}

function messageIsRead(message) {
  if (!message) return false;
  const key = messageReadKey(message.uid);
  if (key && state.readState[key] === true) return true;
  return (message.flags || []).some((flag) => {
    const normalized = String(flag || "").trim().toLowerCase();
    return normalized === "\\seen" || normalized === "seen";
  });
}

function messageReadKey(uid) {
  uid = String(uid || "").trim();
  if (!uid || !state.selectedAccount) return "";
  const folder = $("folderInput")?.value?.trim() || "INBOX";
  return `${state.selectedAccount}|${folder}|${uid}`;
}

function setMessageItemReadState(item, read) {
  if (!item) return;
  item.classList.toggle("read", read);
  item.classList.toggle("unread", !read);
  const pill = item.querySelector("[data-read-pill]");
  if (pill) {
    pill.textContent = read ? "已讀" : "未讀";
    pill.className = `read-pill ${read ? "read" : "unread"}`;
  }
}

function cancelMarkReadTimer() {
  if (markReadTimer) {
    clearTimeout(markReadTimer);
    markReadTimer = null;
  }
}

function scheduleMarkRead(uid, item, token) {
  cancelMarkReadTimer();
  markReadTimer = window.setTimeout(() => {
    markReadTimer = null;
    if (token !== state.activeMessageToken || state.selectedMessage?.uid !== uid) return;
    markMessageRead(uid, item).catch((error) => {
      log(`標記已讀失敗：${error.message || error}`);
    });
  }, 5000);
}

async function markMessageRead(uid, item) {
  const key = messageReadKey(uid);
  if (key && state.readState[key] === true) return;
  const result = await api("/api/email-check/messages", {
    method: "POST",
    body: {
      account_id: state.selectedAccount,
      folder: $("folderInput").value.trim() || "INBOX",
      uids: [uid],
      include_body: false,
      mark_seen: true,
    },
  });
  if (!result.success) throw new Error(result.error || "標記已讀失敗");
  if (key) state.readState[key] = true;
  if (state.selectedMessage?.uid === uid) {
    const flags = new Set([...(state.selectedMessage.flags || []), "\\Seen"]);
    state.selectedMessage.flags = [...flags];
  }
  updateStoredMessageFlags([uid]);
  setMessageItemReadState(item || messageList.querySelector(`[data-uid="${cssEscape(uid)}"]`), true);
  updateMessageListSummary();
}

async function markAllMessagesRead() {
  const unread = state.messages.filter((message) => !messageIsRead(message) && message.uid);
  if (!unread.length) return;
  cancelMarkReadTimer();
  const uids = unread.map((message) => message.uid);
  markAllReadBtn.disabled = true;
  const result = await api("/api/email-check/messages", {
    method: "POST",
    body: {
      account_id: state.selectedAccount,
      folder: $("folderInput").value.trim() || "INBOX",
      uids,
      include_body: false,
      mark_seen: true,
    },
  });
  if (!result.success) throw new Error(result.error || "全部已讀失敗");
  updateStoredMessageFlags(uids);
  for (const uid of uids) {
    setMessageItemReadState(messageList.querySelector(`[data-uid="${cssEscape(uid)}"]`), true);
  }
  updateMessageListSummary();
}

function updateStoredMessageFlags(uids) {
  const uidSet = new Set(uids.map((uid) => String(uid || "")));
  for (const message of state.messages) {
    if (!uidSet.has(String(message.uid || ""))) continue;
    const key = messageReadKey(message.uid);
    if (key) state.readState[key] = true;
    const flags = new Set([...(message.flags || []), "\\Seen"]);
    message.flags = [...flags];
  }
  if (state.selectedMessage && uidSet.has(String(state.selectedMessage.uid || ""))) {
    const flags = new Set([...(state.selectedMessage.flags || []), "\\Seen"]);
    state.selectedMessage.flags = [...flags];
  }
}

function updateMessageListSummary() {
  const total = state.messages.length;
  const unread = state.messages.filter((message) => !messageIsRead(message)).length;
  messageListTitle.textContent = `信件 ${unread}/${total}`;
  markAllReadBtn.disabled = !total || unread === 0 || !state.selectedAccount;
}

function cssEscape(value) {
  if (window.CSS?.escape) return window.CSS.escape(String(value));
  return String(value).replaceAll("\\", "\\\\").replaceAll('"', '\\"');
}

function setDetailTab(tabID) {
  state.activeDetailTab = tabID === "reply" ? "reply" : "content";
  document.querySelectorAll("[data-detail-tab]").forEach((button) => {
    const active = button.dataset.detailTab === state.activeDetailTab;
    button.classList.toggle("active", active);
    button.setAttribute("aria-selected", active ? "true" : "false");
  });
  document.querySelectorAll(".detail-panel").forEach((panel) => {
    const active = panel.id === `${state.activeDetailTab}Panel`;
    panel.classList.toggle("active", active);
    panel.hidden = !active;
  });
}

function renderDetail(message) {
  replyForm.elements.subject.value = message.subject?.toLowerCase().startsWith("re:") ? message.subject : `Re: ${message.subject || ""}`;
  replyForm.elements.to.value = message.reply_to || message.from || "";
  messageDetail.className = "detail-body";
  messageDetail.innerHTML = `
    <div class="detail-subject"><strong>${escapeHTML(message.subject || "(無主旨)")}</strong></div>
    <div class="meta">From: ${escapeHTML(message.from || "")}</div>
    <div class="meta">Date: ${escapeHTML(message.date || "")}</div>
    <hr>
    <div>${escapeHTML(message.text_body || message.text_preview || "")}</div>
  `;
}

async function generateReplyDraft() {
  if (!state.selectedMessage) throw new Error("尚未選取信件");
  const textField = replyForm.elements.text;
  const currentText = String(textField.value || "").trim();
  aiReplyAbortController?.abort();
  aiReplyAbortController = new AbortController();
  aiReplyBtn.disabled = true;
  aiReplyBtn.classList.add("loading");
  openAiReplyDialog(currentText ? "正在潤飾回覆草稿..." : "正在產生回覆草稿...");
  try {
    const draft = await fetchChatReplyDraft(state.selectedMessage, currentText, aiReplyAbortController.signal);
    if (!draft) throw new Error("CHAT 未回傳回覆內容");
    textField.value = draft;
    setDetailTab("reply");
    textField.focus();
  } catch (error) {
    if (isAbortError(error)) {
      log("已取消 CHAT 回覆生成");
      return;
    }
    throw error;
  } finally {
    aiReplyAbortController = null;
    aiReplyBtn.disabled = false;
    aiReplyBtn.classList.remove("loading");
    closeAiReplyDialog();
  }
}

function openAiReplyDialog(statusText) {
  aiReplyStatus.textContent = statusText;
  if (typeof aiReplyDialog.showModal === "function" && !aiReplyDialog.open) {
    aiReplyDialog.showModal();
  } else {
    aiReplyDialog.setAttribute("open", "");
  }
}

function closeAiReplyDialog() {
  if (aiReplyDialog.open && typeof aiReplyDialog.close === "function") {
    aiReplyDialog.close();
  } else {
    aiReplyDialog.removeAttribute("open");
  }
}

function cancelAiReply() {
  aiReplyStatus.textContent = "正在取消...";
  aiReplyAbortController?.abort();
}

function isAbortError(error) {
  return error?.name === "AbortError" || String(error?.message || error).includes("abort");
}

async function fetchChatReplyDraft(message, currentText, signal) {
  const mode = currentText ? "polish" : "create";
  const system = [
    "你是專業 EMAIL 回覆助理，全程使用繁體中文。",
    "只輸出可直接貼入回覆內容的正文，不要 Markdown，不要標題，不要解釋。",
    "語氣保持禮貌、清楚、簡潔；不要編造未提供的事實、承諾或附件。",
    "若已有草稿，請保留原意並修飾語氣、結構與錯字。",
  ].join("\n");
  const sourceBody = message.text_body || message.text_preview || "";
  const userMessage = [
    `任務：${mode === "polish" ? "潤飾下方既有 EMAIL 回覆草稿" : "根據原始信件產生 EMAIL 回覆草稿"}`,
    `原信主旨：${message.subject || ""}`,
    `原信寄件者：${message.from || ""}`,
    `回覆收件者：${replyForm.elements.to.value || ""}`,
    `回覆主旨：${replyForm.elements.subject.value || ""}`,
    "",
    "原始信件內容：",
    sourceBody.slice(0, 8000),
    "",
    "目前回覆草稿：",
    currentText || "(尚無草稿)",
  ].join("\n");
  return fetchDirectStreamText({
    system,
    message: userMessage,
    sessionID: `email-reply-${state.selectedAccount || "default"}`,
    signal,
  });
}

async function fetchDirectStreamText({ system, message, sessionID, signal }) {
  const response = await fetch("/talk/ask/direct_stream", {
    method: "POST",
    headers: {
      "Content-Type": "application/json",
      ...(window.AgenticTalkAPI?.authHeaders?.() || authHeadersFromCookie()),
    },
    body: JSON.stringify({
      session_id: sessionID,
      provider_id: "chat",
      system,
      message,
      thinking_mode: "light",
      permission_profile: "default",
      locale_mode: "fixed",
      locale: "zh-TW",
      lang: "zh-TW",
      language: "zh-TW",
      allow_shell: false,
      plan_execute: false,
      use_memory: false,
      debug: false,
      step_reasoning_stream: false,
    }),
    signal,
  });
  if (!response.ok || !response.body) throw new Error("CHAT 串流呼叫失敗");
  const reader = response.body.getReader();
  const decoder = new TextDecoder("utf-8");
  let buffer = "";
  let content = "";
  while (true) {
    const { value, done } = await reader.read();
    if (done) break;
    buffer += decoder.decode(value, { stream: true }).replaceAll("\r\n", "\n");
    while (buffer.includes("\n\n")) {
      const splitIndex = buffer.indexOf("\n\n");
      const chunk = buffer.slice(0, splitIndex);
      buffer = buffer.slice(splitIndex + 2);
      const { eventName, payload } = parseSSEChunk(chunk);
      if (eventName === "delta") {
        content += payload.delta || "";
      } else if (eventName === "done") {
        content = payload.assistant_message?.content || content;
      } else if (eventName === "error") {
        throw new Error(payload.error || "CHAT 回應失敗");
      }
    }
  }
  return content.trim().replace(/^```(?:text)?\s*/i, "").replace(/```$/i, "").trim();
}

function parseSSEChunk(chunk) {
  let eventName = "message";
  const data = [];
  for (const line of String(chunk || "").split("\n")) {
    if (line.startsWith("event:")) eventName = line.slice(6).trim();
    if (line.startsWith("data:")) data.push(line.slice(5).trimStart());
  }
  const text = data.join("\n").trim();
  if (!text) return { eventName, payload: {} };
  try {
    return { eventName, payload: JSON.parse(text) };
  } catch {
    return { eventName, payload: { delta: text } };
  }
}

async function sendReply(event) {
  event.preventDefault();
  if (!state.selectedMessage) throw new Error("尚未選取信件");
  const result = await api("/api/email-check/reply", {
    method: "POST",
    body: {
      account_id: state.selectedAccount,
      folder: $("folderInput").value.trim() || "INBOX",
      uid: state.selectedMessage.uid,
      to: replyForm.elements.to.value.split(",").map((value) => value.trim()).filter(Boolean),
      subject: replyForm.elements.subject.value.trim(),
      text: replyForm.elements.text.value,
    },
  });
  if (!result.success) throw new Error(result.error || "回覆失敗");
  replyForm.elements.text.value = "";
  log("回覆已送出", result.sent);
}

function escapeHTML(value) {
  return String(value ?? "")
    .replaceAll("&", "&amp;")
    .replaceAll("<", "&lt;")
    .replaceAll(">", "&gt;")
    .replaceAll('"', "&quot;");
}

function bind() {
  document.querySelectorAll("[data-module-target]").forEach((button) => {
    button.addEventListener("click", () => setModule(button.dataset.moduleTarget));
  });
  document.querySelectorAll("[data-detail-tab]").forEach((button) => {
    button.addEventListener("click", () => setDetailTab(button.dataset.detailTab));
  });
  $("newAccountBtn").addEventListener("click", () => {
    startNewAccount();
  });
  mailAccountSelect.addEventListener("change", () => {
    selectAccount(mailAccountSelect.value, false);
  });
  accountFields.incoming_protocol.addEventListener("change", () => {
    const protocol = incomingProtocol({ incoming_protocol: accountFields.incoming_protocol.value });
    const server = incomingServer(currentAccount(), protocol);
    accountFields.imap_host.value = server.host || "";
    accountFields.imap_port.value = server.port || incomingDefaultPort(protocol);
    accountFields.imap_tls.checked = server.tls !== false;
    accountFields.imap_start_tls.checked = Boolean(server.start_tls);
    updateIncomingProtocolControls();
  });
  accountForm.addEventListener("submit", (event) => saveAccount(event).catch(handleError));
  $("loginBtn").addEventListener("click", () => loginCheck().catch(handleError));
	  $("deleteAccountBtn").addEventListener("click", () => deleteAccount().catch(handleError));
	  $("checkBtn").addEventListener("click", () => readMessages().catch(handleError));
	  $("newAutoTaskBtn").addEventListener("click", () => openAutoTaskDialog());
	  $("closeAutoTaskDialogBtn").addEventListener("click", closeAutoTaskDialog);
	  $("cancelAutoTaskBtn").addEventListener("click", closeAutoTaskDialog);
	  autoTaskForm.addEventListener("submit", (event) => saveAutoTask(event).catch(handleError));
	  markAllReadBtn.addEventListener("click", () => markAllMessagesRead().catch(handleError));
  aiReplyBtn.addEventListener("click", () => generateReplyDraft().catch(handleError));
  cancelAiReplyBtn.addEventListener("click", cancelAiReply);
  aiReplyDialog.addEventListener("cancel", (event) => {
    event.preventDefault();
    cancelAiReply();
  });
  replyForm.addEventListener("submit", (event) => sendReply(event).catch(handleError));
}

async function deleteAccount() {
  const account = currentAccount();
  if (!account) {
    log("請先選擇要刪除的 EMAIL 帳號");
    return;
  }
  const confirmed = window.confirm(`確認刪除 EMAIL 帳號「${account.name || account.email || account.id}」？`);
  if (!confirmed) return;
  const result = await api(`/api/email-check/accounts/${encodeURIComponent(account.id)}`, { method: "DELETE" });
  if (!result.success) throw new Error(result.error || "刪除帳號失敗");
  state.selectedAccount = "";
  state.editingAccountID = "";
  await loadAccounts();
  log(result.persisted ? "帳號已刪除並永久儲存" : "帳號已刪除", result);
}

function startNewAccount() {
  state.selectedAccount = "";
  state.editingAccountID = "";
  syncAccountSelection();
  fillAccountForm(null);
  accountFields.id.readOnly = false;
  accountFields.email.focus();
  updateAccountControls();
  setAccountStatus("請填寫帳號資訊後按儲存。", "info");
  log("已切換為新增 EMAIL 帳號");
}

function updateAccountControls() {
  const hasSelection = Boolean(currentAccount());
  $("loginBtn").disabled = !hasSelection;
  $("deleteAccountBtn").disabled = !hasSelection;
	  $("checkBtn").disabled = !state.selectedAccount;
	  $("newAutoTaskBtn").disabled = !state.accounts.length;
	  updateMessageListSummary();
	}

function handleError(error) {
  const message = error.message || String(error);
	  if (state.activeModule === "accountModule") {
	    setAccountStatus(message, "error");
	  } else if (state.activeModule === "autoCheckModule") {
	    setAutoTaskStatus(message, "error");
	  }
  log(message);
}

function setSaving(saving) {
  saveAccountBtn.disabled = saving;
  saveAccountBtn.textContent = saving ? "儲存中..." : "儲存帳號";
}

function setAccountStatus(message, type = "info") {
  accountStatus.textContent = message || "";
  accountStatus.className = message ? `form-status show ${type}` : "form-status";
  if (accountStatusTimer) {
    clearTimeout(accountStatusTimer);
    accountStatusTimer = null;
  }
  if (message && type === "success") {
    accountStatusTimer = window.setTimeout(() => {
      if (accountStatus.classList.contains("success")) {
        setAccountStatus("");
      }
    }, 3000);
  }
}

async function initializePage() {
  try {
	    await ensureServiceReady();
	    await loadAccounts();
	    await loadAutoChecks();
	  } catch (error) {
    setServiceStatus("error", "服務錯誤");
    handleError(error);
  }
}

async function ensureServiceReady() {
  setServiceStatus("checking", "檢查中");
  let status = null;
  try {
    status = await pluginControl("/status", { method: "GET", cache: "no-store" });
  } catch (error) {
    log(`SERVICE 狀態檢查失敗，嘗試啟動：${error.message || error}`);
  }
  if (status?.success && status?.plugin?.loaded) {
    setServiceStatus("ready", "已啟動");
    return status;
  }

  setServiceStatus("starting", "啟動中");
  const load = await pluginControl("/load", { method: "POST", body: {} });
  if (!load?.success) {
    const error = load?.error || "SERVICE 啟動失敗";
    setServiceStatus("error", "服務錯誤");
    throw new Error(error);
  }
  setServiceStatus("ready", "已啟動");
  return load;
}

function setServiceStatus(type, text) {
  const icons = {
    checking: "fa-circle-notch fa-spin",
    starting: "fa-circle-notch fa-spin",
    ready: "fa-circle-check",
    error: "fa-circle-xmark",
  };
  serviceStatus.innerHTML = `<i class="fa-solid ${icons[type] || "fa-circle-info"}" aria-hidden="true"></i><span>${escapeHTML(text)}</span>`;
  serviceStatus.className = `status-pill ${type}`;
  serviceStatus.title = text;
}

initializeModuleOrdering();
bind();
setModule(state.activeModule);
initializePage();
