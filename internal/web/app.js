import { historyPanel } from "./history.js";

import { guidePanel } from "./guide.js";
import { storagePanel } from "./storage.js";
import {
  houseAuthority,
  queueFor,
  settled,
  issueJobDetails,
  taskRetryEligible,
  taskSnoozable,
  snoozeLabel,
  snoozeRequest,
  defaultSnoozeUntil,
  localInputValue,
  visibleEvents,
  outcomeReport,
  safeURL,
  townSummary,
  taskStatuses,
  projectTask,
  projectState,
  projectTown,
  projectWorker,
  houseWorkload,
  boardColumns,
  boardColumn,
  normalizeView,
  focusIdentity,
  focusMatches,
  scheduleLabel,
  branchHealthNote,
  workerControls,
  repositoryStatus,
  townControls,
  decisionReason,
  inbox,
  attentionGuidance,
  ago,
  profileSummary,
  quietNote,
  parseQuietHours,
  formatQuietHours,
  reconnectDelay,
} from "./town.js";
import { attentionSettings } from "./attention.js";
import { management } from "./manage.js";
import {
  units,
  unitOrder,
  isUnit,
  callsign,
  unitName,
  unitBot,
  dutyStatus,
  dutyLight,
  caseNumber,
  radioRoute,
  caseFlow,
  roster,
} from "./precinct.js";
import { robotCop, slopBlob, custody, newArrests, canStage, patrolRun, arrest, alarm } from "./animation.js";
const $ = (s) => document.querySelector(s),
  esc = (value) =>
    String(value ?? "").replace(
      /[&<>"']/g,
      (c) =>
        ({
          "&": "&amp;",
          "<": "&lt;",
          ">": "&gt;",
          '"': "&quot;",
          "'": "&#39;",
        })[c],
    );
let token =
  new URLSearchParams(location.hash.slice(1)).get("token") ||
  sessionStorage.getItem("slopcop-squad-token") ||
  "";

if (token) {
  sessionStorage.setItem("slopcop-squad-token", token);
  history.replaceState(null, "", location.pathname);
}
let overview = (() => {
  try { return localStorage.getItem("slopcop-squad-scope") !== "town"; }
  catch { return true; }
})();
function saveScope() {
  try { localStorage.setItem("slopcop-squad-scope", overview ? "all" : "town"); }
  catch {}
}
let viewMode = (() => {
  try {
    return normalizeView(localStorage.getItem("slopcop-squad-view"));
  } catch {
    return "town";
  }
})();
let state = null,
  selectedTown = (() => {
    try {
      return localStorage.getItem("slopcop-squad-selected") || "";
    } catch {
      return "";
    }
  })(),
  selectedHouse = "hall",
  selectedTask = "",
  sequence = null,
  transfers = [],
  arrests = [],
  motion = !matchMedia("(prefers-reduced-motion: reduce)").matches,
  streamAbort = null,
  servedVersion = "";
let liveDetail = null;
let liveDetailEpoch = 0;
let outcomeDays = 7;
function clearLiveDetail() {
  liveDetail = null;
  liveDetailEpoch++;
}
async function fetchLiveDetail(townId, taskId) {
  const key = `${townId}\n${taskId}`;
  const epoch = liveDetailEpoch;
  try {
    const detail = await api("/api/task-detail", { town: townId, task: taskId });
    if (epoch !== liveDetailEpoch) return;
    liveDetail = { key, status: "ready", data: detail };
  } catch (error) {
    if (epoch !== liveDetailEpoch) return;
    liveDetail = { key, status: "failed", error: error.message };
  }
  if (selectedTown === townId && selectedTask === taskId) renderInspection();
}
// A write that gets no answer is not a failed write: Town may have applied it
// before the connection stalled. The message says so rather than inviting a
// blind retry.
const writeTimeoutMs = 30000;
const writeTimeoutMessage = `The Squad did not answer within ${writeTimeoutMs / 1000} seconds. The request may still have been applied; check its state before trying again.`;
async function api(path, body, signal) {
  let response;
  try {
    response = await fetch(path, {
      method: body ? "POST" : "GET",
      headers: {
        Authorization: `Bearer ${token}`,
        ...(body ? { "Content-Type": "application/json" } : {}),
      },
      body: body ? JSON.stringify(body) : undefined,
      signal,
    });
  } catch (error) {
    if (signal?.aborted && signal.reason?.name === "TimeoutError") throw new Error(writeTimeoutMessage);
    throw error;
  }
  if (!response.ok) {
    const error = await response
      .json()
      .catch(() => ({ error: response.statusText }));
    const failure = new Error(error.error || response.statusText);
    failure.status = response.status;
    throw failure;
  }
  return response.json();
}

async function exportOutcomes(days) {
  const query = new URLSearchParams({ town: selectedTown, format: "csv" });
  query.set("from", (days > 0 ? new Date(Date.now() - days * 86400000) : new Date(0)).toISOString());
  const response = await fetch(`/api/outcomes?${query}`, { headers: { Authorization: `Bearer ${token}` } });
  if (!response.ok) throw new Error((await response.json().catch(() => ({}))).error || response.statusText);
  const href = URL.createObjectURL(await response.blob());
  const link = document.createElement("a");
  link.href = href;
  link.download = `${selectedTown.replace("/", "-")}-outcomes-${days > 0 ? `${days}d` : "all"}.csv`;
  link.click();
  URL.revokeObjectURL(href);
}
function town() {
  return state?.towns[selectedTown];
}
async function refreshState() {
  receive(await api("/api/state"));
}
const attention = attentionSettings({ api, getState: () => state, refresh: refreshState });
const renderManagement = management({
  api,
  getTown: town,
  getState: () => state,
  refresh: refreshState,
});
function showError(message) {
  $("#error").textContent = message;
  $("#error").hidden = !message;
}
// The stream reconnects with capped, jittered exponential backoff. A hidden
// tab schedules nothing: the retry waits until the page is visible again, and
// becoming visible retries at once instead of finishing a long backoff.
let reconnectAttempt = 0,
  reconnectTimer = 0,
  reconnectHeld = false,
  renderHeld = false;
function scheduleReconnect() {
  clearTimeout(reconnectTimer);
  reconnectTimer = 0;
  if (document.hidden) {
    reconnectHeld = true;
    return;
  }
  reconnectTimer = setTimeout(() => {
    reconnectTimer = 0;
    if (document.hidden) {
      reconnectHeld = true;
      return;
    }
    connect();
  }, reconnectDelay(reconnectAttempt++));
}
document.addEventListener("visibilitychange", () => {
  if (document.hidden) return;
  if (renderHeld && state) {
    renderHeld = false;
    render();
  }
  if (!reconnectHeld && !reconnectTimer) return;
  clearTimeout(reconnectTimer);
  reconnectTimer = 0;
  reconnectHeld = false;
  reconnectAttempt = 0;
  connect();
});
async function connect() {
  if (!token) {
    $("#connect-dialog").showModal();
    return;
  }
  clearTimeout(reconnectTimer);
  reconnectTimer = 0;
  reconnectHeld = false;
  streamAbort?.abort();
  streamAbort = new AbortController();
  try {
    const response = await fetch("/api/events", {
      headers: { Authorization: `Bearer ${token}` },
      signal: streamAbort.signal,
    });
    if (!response.ok) {
      if (response.status === 401) {
        $("#connect-dialog").showModal();
        return;
      }
      throw new Error("SlopCop Squad is unavailable");
    }
    $("#connection").textContent = "Connected";
    $("#connection-dot").className = "dot active";
    let buffer = "";
    const reader = response.body.getReader(),
      decoder = new TextDecoder();
    for (;;) {
      const { value, done } = await reader.read();
      if (done) throw new Error("Connection closed");
      buffer += decoder.decode(value, { stream: true });
      let end;
      while ((end = buffer.indexOf("\n\n")) >= 0) {
        const message = buffer.slice(0, end);
        buffer = buffer.slice(end + 2);
        const data = message.split("\n").find((l) => l.startsWith("data: "));
        if (data) {
          // A frame, not just an accepted request, proves the stream works.
          reconnectAttempt = 0;
          receive(JSON.parse(data.slice(6)));
        }
      }
    }
  } catch (error) {
    if (error.name === "AbortError") return;
    $("#connection").textContent = "Reconnecting";
    $("#connection-dot").className = "dot";
    scheduleReconnect();
  }
}
function receive(next) {
  if (sequence !== null && next.seq >= sequence) {
    for (const event of visibleEvents(next.events, sequence, selectedTown)) {
      if ((event.kind === "delivery" || event.kind === "error") && viewMode === "town" && !overview && motion && !document.hidden)
        transfers.push(event);
    }
    // A case that entered custody since the last snapshot gets its arrest.
    if (viewMode === "town" && !overview && motion && !document.hidden)
      arrests.push(...newArrests(state?.towns?.[selectedTown], next.towns?.[selectedTown]));
  } else {
    transfers = [];
    arrests = [];
  }
  transfers = transfers.slice(-6);
  arrests = arrests.slice(-3);
  sequence = next.seq;
  state = next;
  // The page's assets belong to the binary that served them. When a restart
  // brings a different version, reload so the UI matches the API again.
  if (next.version) {
    if (servedVersion && next.version !== servedVersion) {
      location.reload();
      return;
    }
    servedVersion = next.version;
  }
  const serviceVersion = typeof state.version === "string" ? state.version.trim() : "";
  $("#town-version").textContent = serviceVersion;
  $("#town-version").hidden = !serviceVersion;
  $("#town-version").title = serviceVersion;
  $("#help-version").textContent = serviceVersion ? `SlopCop Squad ${serviceVersion}` : "";
  if (!state.towns[selectedTown]) {
    selectedTown = Object.keys(state.towns)[0] || "";
    selectedTask = "";
  }
  if (selectedTask && !state.towns[selectedTown]?.tasks[selectedTask]) selectedTask = "";
  // A hidden tab keeps the newest snapshot and paints it when shown again.
  if (document.hidden) {
    renderHeld = true;
    return;
  }
  render();
}

function capacityInfo() {
  const capacity = state?.capacity || {};
  const service = state?.service_config || {};
  const active = Number(capacity.active ?? 0);
  const limit = Number(capacity.limit ?? service.max_workers ?? 4);
  return {
    active: Number.isFinite(active) ? active : 0,
    limit: Number.isFinite(limit) && limit > 0 ? limit : 4,
  };
}

// budgetBlock reports what the accounting period measured. Absent telemetry is
// spelled "not reported" everywhere: a town whose agents never sent usage has
// not spent nothing, Town simply does not know.
// policyBlock states what this unit's filters admit, and how much of the
// repository's inventory they hold back. A filtered item stays listed and
// marked: an operator has to be able to see what their own filter excluded.
function policyBlock(t, house) {
  const policy = (t?.config?.work_policies || []).find((p) => p.role === house);
  const held = Object.values(t?.tasks || {}).filter(
    (task) => task.policy_excluded && task.house === house,
  ).length;
  if (!policy) {
    return held
      ? `<p class="muted">${held} item${held === 1 ? "" : "s"} in this precinct are held back by another unit's filter.</p>`
      : "";
  }
  const excluded = held
    ? ` ${held} inventory item${held === 1 ? " is" : "s are"} held back by it.`
    : " Nothing in the current inventory is held back by it.";
  return `<h3>WORK POLICY</h3><p class="queue-summary">${esc(policy.summary)}${esc(excluded)}</p>`;
}

// quietBlock says whether quiet hours hold this town's new work, and where the
// schedule comes from. It is a scheduled pause, not a failure.
function quietBlock(t) {
  const note = quietNote(t);
  const held = t?.quiet_hours?.active;
  const body = note
    ? `<p class="${held ? "quiet-held" : "muted"}">${esc(note)}</p>`
    : '<p class="muted">No quiet hours. Set weekly windows in Precinct settings, or a service default under Squad settings.</p>';
  return `<h2>Quiet hours</h2>${body}`;
}
function budgetBlock(t) {
  const budget = t?.budget;
  if (!budget) return "";
  const minutes = Math.round(Number(budget.agent_seconds || 0) / 60);
  const attempts = budget.max_attempts
    ? `${budget.attempts} of ${budget.max_attempts} agent attempts`
    : `${budget.attempts} agent attempts`;
  const time = budget.max_agent_minutes
    ? `${minutes} of ${budget.max_agent_minutes} agent minutes`
    : `${minutes} agent minutes`;
  const untimed = budget.untimed
    ? ` · ${budget.untimed} attempt${budget.untimed === 1 ? "" : "s"} reported no elapsed time`
    : "";
  const usage =
    budget.usage == null
      ? "not reported"
      : `${budget.usage.input_tokens} in / ${budget.usage.output_tokens} out`;
  const cost = budget.cost_usd == null ? "not reported" : `$${budget.cost_usd}`;
  const resets = budget.to ? new Date(budget.to).toLocaleString() : "";
  const held = budget.exhausted
    ? `<p class="budget-held">${esc(budget.reason || "Budget reached. New agent work is held.")}</p>`
    : "";
  const ceiling = budget.configured
    ? ""
    : '<p class="muted">No budget set for this precinct. Agent work is bounded only by worker capacity and the per-task attempt limits.</p>';
  return `<h2>Agent budget</h2>${held}<p class="muted">This ${esc(budget.period)} (resets ${esc(resets)}): ${esc(attempts)} · ${esc(time)}${esc(untimed)}.</p><p class="muted">Tokens ${esc(usage)} · cost ${esc(cost)}. ${esc(budget.advice || "")}</p>${ceiling}`;
}

function selectView(mode) {
  mode = normalizeView(mode);
  viewMode = mode;
  transfers = [];
  try {
    localStorage.setItem("slopcop-squad-view", mode);
  } catch {
    /* Private browsing may disable persistence; the in-memory selector remains usable. */
  }
  render();
}

function renderViewSwitcher() {
  document.querySelectorAll("#view-switcher [data-view]").forEach((button) => {
    const selected = button.dataset.view === viewMode;
    button.tabIndex = selected ? 0 : -1;
    button.setAttribute("aria-selected", String(selected));
    button.classList.toggle("selected", selected);
  });
}

function renderCapacity() {
  const { active, limit } = capacityInfo();
  $("#capacity-summary").textContent = `${active}/${limit} on duty`;
  $("#capacity-current").textContent = `${active} active · limit ${limit}`;
  if (!$("#capacity-dialog").open)
    $("#capacity-input").value = String(limit);
}

// Every surface spells a dispatch profile the same way: harness, model and
// effort as three chips, dimmed when the unit simply inherits town defaults
// and outlined when an operator chose it for that unit.
function summaryChips(p, extra = "") {
  return `<span class="profile ${p.inherited ? "inherited" : "own"}${p.live ? " live" : ""}${extra ? ` ${extra}` : ""}" title="${esc(p.title)}"><b class="profile-chip harness">${esc(p.harness)}</b><b class="profile-chip model">${esc(p.model)}</b><b class="profile-chip effort rank-${esc(p.rank)}">${esc(p.effort)}</b></span>`;
}
function profileChips(profile, role = "", extra = "") {
  return summaryChips(profileSummary(profile), extra);
}
function queuedProfileText(task) {
  if (settled(task)) return "";
  return `<span class="profile-lead">${task.profile?.source === "active" ? "Running profile" : "Next profile"}</span>${profileChips(task.profile, task.house)}`;
}
function captureBoardViewport(board) {
  const townColumns = new Map(
    [...board.querySelectorAll("[data-board-columns]")].map((columns) => [
      columns.dataset.boardColumns,
      { left: columns.scrollLeft, top: columns.scrollTop },
    ]),
  );
  const taskLists = new Map(
    [...board.querySelectorAll("[data-board-list]")].map((list) => [
      list.dataset.boardList,
      { left: list.scrollLeft, top: list.scrollTop },
    ]),
  );
  return {
    townColumns,
    taskLists,
    focusedList: document.activeElement?.dataset?.boardList || "",
  };
}
function restoreBoardViewport(board, viewport) {
  board.querySelectorAll("[data-board-columns]").forEach((columns) => {
    const position = viewport.townColumns.get(columns.dataset.boardColumns);
    if (position) {
      columns.scrollLeft = position.left;
      columns.scrollTop = position.top;
    }
  });
  board.querySelectorAll("[data-board-list]").forEach((list) => {
    const position = viewport.taskLists.get(list.dataset.boardList);
    if (position) {
      list.scrollLeft = position.left;
      list.scrollTop = position.top;
    }
  });
  if (viewport.focusedList) {
    [...board.querySelectorAll("[data-board-list]")]
      .find((list) => list.dataset.boardList === viewport.focusedList)
      ?.focus({ preventScroll: true });
  }
}
// Board and Compact list units in roster order, the way a case moves.
function inRosterOrder(workers) {
  return [...workers].sort((a, b) => unitOrder.indexOf(a.role) - unitOrder.indexOf(b.role));
}
function workloadText(counts) {
  return `${counts.active} active · ${counts.waiting} waiting · ${counts.blocked} blocked`;
}
function workloadChips(counts) {
  return `<span class="workload-counts" title="Active items · waiting items · blocked items"><span class="workload-active">${counts.active} active</span><span>${counts.waiting} waiting</span><span class="${counts.blocked ? "workload-blocked" : ""}">${counts.blocked} blocked</span></span>`;
}
function renderBoard() {
  const board = $("#board");
  const viewport = captureBoardViewport(board);
  const projection = projectState(state, selectedTown);
  const towns = overview
    ? projection.towns
    : projection.towns.filter((item) => item.town?.id === selectedTown);
  board.innerHTML = towns.length
    ? towns
        .map((item) => {
          const townName = item.town?.config?.repo || item.town?.id || "Town";
          const workerCards = inRosterOrder(item.workers)
            .map((worker) => ({ worker, counts: houseWorkload(item.town, worker.role) }))
            .filter(({ worker, counts }) => worker.active || worker.status === "failed" || counts.waiting || counts.blocked)
            .map(({ worker, counts }) => `<button class="board-worker ${counts.blocked || worker.status === "failed" ? "failed" : ""}" data-board-town="${esc(item.town.id)}" data-board-house="${esc(worker.role)}"><strong>${esc(callsign(worker.role))} ${esc(unitName(worker.role))} · ${esc(dutyStatus(worker.status))}</strong>${workloadChips(counts)}<small>${profileChips(worker.profile, worker.role)}</small></button>`)
            .join("");
          const cards = boardColumns
            .map((column) => {
              const tasks = item.tasks.filter((task) => boardColumn(task) === column.id);
              if (!tasks.length) return "";
              const classes = tasks.map((task) => task.statusClass).join(" ");
              const listKey = `${item.town.id}:${column.id}`;
              return `<div class="board-column status-${esc(column.id)} ${esc(classes)}"><h3>${esc(column.label)} <span>${tasks.length}</span></h3><div class="board-column-list" data-board-list="${esc(listKey)}" role="region" tabindex="0" aria-label="${esc(`${townName} ${column.label} tasks`)}">${tasks
                .map(
                  (task) =>
                    `<button class="board-task" data-board-town="${esc(item.town.id)}" data-board-task="${esc(task.id)}" data-board-house="${esc(task.house || "hall")}"><strong>${esc(task.title || task.id)}</strong><small><span class="status-chip status-${esc(task.statusClass)}">${esc(task.statusLabel)}</span> · ${esc(task.house ? unitName(task.house) : "town")}${task.number ? ` · #${task.number}` : ""}</small><small>${queuedProfileText(task)}</small></button>`,
                )
                .join("")}</div></div>`;
            })
            .join("");
          return `<article class="board-town"><header><div><span class="eyebrow">${esc(townName.split("/")[0] || "TOWN")}</span><h2>${esc(townName.split("/").slice(1).join("/") || townName)}</h2></div><button class="quiet board-visit" data-board-visit="${esc(item.town.id)}">Open precinct →</button></header>${workerCards ? `<div class="board-workers"><h3>Units</h3>${workerCards}</div>` : ""}<div class="board-columns" data-board-columns="${esc(item.town.id)}">${cards || '<p class="muted">No cases on file yet.</p>'}</div></article>`;
        })
        .join("")
    : '<div class="overview-empty"><h2>No precincts yet.</h2><p>Add a repository with New precinct to start tracking cases.</p></div>';
  board
    .querySelectorAll("[data-board-visit]")
    .forEach((button) => (button.onclick = () => { selectView("town"); selectTown(button.dataset.boardVisit); }));
  board
    .querySelectorAll("[data-board-task], [data-board-house]")
    .forEach(
      (button) =>
        (button.onclick = () => {
          inspectOperation(button.dataset.boardTown, button.dataset.boardHouse, button.dataset.boardTask);
        }),
    );
  restoreBoardViewport(board, viewport);
}

function renderCompact() {
  const projection = projectState(state, selectedTown);
  const towns = overview
    ? projection.towns
    : projection.towns.filter((item) => item.town?.id === selectedTown);
  $("#compact").innerHTML = towns
    .map((item) => {
      const repo = item.town?.config?.repo || item.town?.id || "Town";
      return `<article class="compact-town"><div class="compact-title"><h2>${esc(repo)}</h2><span>${item.active} on a case · ${item.attention} stuck</span></div><div class="compact-workers">${inRosterOrder(item.workers)
        .map(
          (worker) =>
            `<button class="compact-worker" data-compact-town="${esc(item.town.id)}" data-compact-house="${esc(worker.role)}"><i class="dot ${worker.active ? "active" : dutyLight(worker.status)}"></i><strong>${esc(callsign(worker.role))} ${esc(unitName(worker.role))}</strong><small>${esc(dutyStatus(worker.status))}</small><small>${profileChips(worker.profile, worker.role)}</small><small>${esc(scheduleLabel(worker))}</small></button>`,
        )
        .join("")}</div><div class="compact-tasks">${item.tasks
        .map(
          (task) =>
            `<button class="compact-task status-${esc(task.statusClass)}" data-compact-town="${esc(item.town.id)}" data-compact-house="${esc(task.house || "hall")}" data-compact-task="${esc(task.id)}"><span>${esc(task.statusLabel)}</span><strong>${esc(task.title || task.id)}</strong><small>${queuedProfileText(task)}</small></button>`,
        )
        .join("") || '<span class="muted">No open cases.</span>'}</div></article>`;
    })
    .join("") || '<div class="overview-empty"><h2>No precincts yet.</h2></div>';
  $("#compact")
    .querySelectorAll("[data-compact-house]")
    .forEach(
      (button) =>
        (button.onclick = () => {
          inspectOperation(button.dataset.compactTown, button.dataset.compactHouse, button.dataset.compactTask);
        }),
    );
}
function inspectOperation(id, role, task = "") {
  if (!state?.towns[id]) return;
  selectedTown = id;
  selectedHouse = role;
  selectedTask = task;
  $("#inspector").classList.add("open");
  render();
  $("#close-inspector").focus();
}
function closeInspector() {
  clearLiveDetail();
  $("#inspector").classList.remove("open");
  if (state) renderOverview();
}
function chooseHouse(role) {
  overview = false;
  saveScope();
  selectedHouse = role;
  selectedTask = "";
  clearLiveDetail();
  $("#inspector").classList.add("open");
  render();
}
storagePanel({ api, getTown: town });
historyPanel({ api, getTown: town });

const guideUI = guidePanel({api, getTown:town, getState:()=>state, onOpen:()=>chooseHouse("hall")});

function renderTownControls(t) {
 $("#town-storage").disabled = !t;
 $("#town-history").disabled = !t;
 $("#town-history").textContent = t?.archived_tasks ? `Case archive (${t.archived_tasks})` : "Case archive";
  const toggle = $("#town-toggle"),
    pauseAll = $("#pause-all"),
    chip = $("#town-state"),
    detail = $("#town-wake-detail");
  toggle.disabled = !t;
  pauseAll.disabled = !t;
  if (!t) {
    chip.hidden = true;
    pauseAll.hidden = true;
    toggle.textContent = "▶ Start patrol";
    toggle.dataset.action = "start";
    detail.hidden = true;
    return;
  }
  const controls = townControls(t);
  chip.hidden = false;
  chip.textContent = controls.status;
  chip.className = `status-chip status-${controls.statusClass}`;
  toggle.textContent = controls.primary.label;
  toggle.dataset.action = controls.primary.action;
  toggle.classList.toggle("primary", controls.primary.action === "start");
  toggle.title = controls.detail || controls.primary.label;
  detail.textContent = controls.detail || "";
  detail.hidden = !controls.detail;
  pauseAll.hidden = !controls.secondary;
  if (controls.secondary)
    pauseAll.textContent = controls.secondary.label;
  const townBusy = pendingWrites.has(commandKey(t.id, "all", "", ""));
  toggle.disabled = townBusy;
  pauseAll.disabled = townBusy;
  toggle.setAttribute("aria-busy", String(toggle.disabled));
  pauseAll.setAttribute("aria-busy", String(pauseAll.disabled));
}
function render() {
  const focus = focusIdentity(document.activeElement);
  const t = town();
  renderViewSwitcher();
  renderCapacity();
  $("#mode").hidden = !state.demo;
  $("#demo-note").hidden = !state.demo;
  $("#empty").hidden = !!t;
  renderTownControls(t);
  guideUI.render();
  $("#repo-owner").textContent = t ? t.config.repo.split("/")[0] : "SLOPCOP SQUAD";
  $("#town-name").textContent = t
    ? t.config.repo.split("/")[1]
    : "No precinct open";
  const needs = inbox(state);
  const townNeeds = (id) => needs.towns[id] || { decisions: 0, attention: 0 };
  $("#town-meta").textContent = t
    ? `${t.config.branch || "Reading repository…"} · ${Object.values(t.workers).filter((w) => w.status === "working").length} on a case · ${townNeeds(t.id).decisions} awaiting ruling · ${townNeeds(t.id).attention} stuck`
    : "Put a repository under watch to open its precinct.";
  $("#towns").innerHTML = Object.values(state.towns)
    .map((item) => {
      const counts = townNeeds(item.id);
      const flags = [
        counts.decisions ? `<span class="town-flag decide">${counts.decisions} to rule on</span>` : "",
        counts.attention ? `<span class="town-flag attention">${counts.attention} stuck</span>` : "",
      ].join("");
      return `<button class="town-link ${item.id === selectedTown ? "selected" : ""}" data-town="${esc(item.id)}"><strong>${esc(item.config.repo.split("/")[1])}</strong><small>${esc(item.config.repo.split("/")[0])} · ${Object.values(item.workers).filter((w) => w.enabled).length} on duty</small>${flags ? `<span class="town-flags">${flags}</span>` : ""}</button>`;
    })
    .join("");
  $("#towns")
    .querySelectorAll("button")
    .forEach(
      (button) =>
        (button.onclick = () => {
          selectTown(button.dataset.town);
        }),
    );
  renderOverview();
  renderBoard();
  renderCompact();
  showError(t?.error || "");
  renderPrecinct();
  renderInspection();
  renderJournal();
  renderManagement();
  renderInbox(needs);
  restoreFocus(focus);
  stageScenes();
}
function restoreFocus(focus) {
  if (!focus) return;
  const root = document.querySelector(`#${focus.surface}`);
  const restored = [...(root?.querySelectorAll("button, a, summary") || [])].find((button) =>
    focusMatches(button, focus),
  );
  restored?.focus({ preventScroll: true });
}
function selectTown(id) {
  if (!state?.towns[id]) throw new Error("Unknown town");
  selectedTown = id;
  try {
    localStorage.setItem("slopcop-squad-selected", id);
  } catch {
    /* Keep the selection for this session when persistence is unavailable. */
  }
  selectedTask = "";
  clearLiveDetail();
  transfers = [];
  overview = false;
  saveScope();
  render();
}
function renderOverview() {
  const operations = viewMode !== "town";
  $("#overview").hidden = !overview || operations;
  $("#board").hidden = viewMode !== "board";
  $("#compact").hidden = viewMode !== "compact";
  $(".precinct").hidden = overview || operations;
  $(".activity").hidden = overview || operations;
  $("#inspector").hidden = operations ? !$("#inspector").classList.contains("open") : overview;
  $(".town-controls").hidden = overview;
  $("#all-towns").classList.toggle("selected", overview);
  if (!overview) return;
  $("#repo-owner").textContent = "CITYWIDE";
  $("#town-name").textContent = "All precincts. One force.";
  const townCount = Object.keys(state.towns).length;
  $("#town-meta").textContent = `${townCount} precinct${townCount === 1 ? "" : "s"} · independent units, caseloads and releases`;
  $("#overview").innerHTML =
    Object.values(state.towns)
      .map((t) => {
        const stats = townSummary(t),
          spread = projectTown(t).profiles,
          stuck = stats.blocked + stats.failed;
        return `<button class="town-card" data-visit="${esc(t.id)}"><span class="eyebrow">${esc(t.config.repo.split("/")[0])}</span><h2>${esc(t.config.repo.split("/")[1])}</h2>${spread.distinct.length === 1 ? summaryChips(spread.distinct[0]) : `<span class="profile mixed" title="${esc(spread.distinct.map((p) => p.text).join("\n"))}">${esc(spread.label)}${spread.overrides ? ` · ${spread.overrides} custom` : ""}</span>`}<div class="town-stats"><span><strong>${stats.busy}</strong> on a case</span><span><strong>${stats.queued}</strong> open cases</span><span class="${stats.decisions ? "stat-decide" : ""}"><strong>${stats.decisions}</strong> awaiting ruling</span><span class="${stuck ? "stat-stuck" : ""}"><strong>${stuck}</strong> stuck</span></div><p>${esc(repositoryStatus(t))}</p><small>${esc(stats.release)} · Open precinct →</small></button>`;
      })
      .join("") ||
    `<div class="overview-empty"><h2>No precincts yet.</h2><p>Add one with New precinct above. Each repository gets its own precinct and its own units.</p></div>`;
  $("#overview")
    .querySelectorAll("[data-visit]")
    .forEach((b) => (b.onclick = () => selectTown(b.dataset.visit)));
}
$("#all-towns").onclick = () => {
  overview = true;
  saveScope();
  transfers = [];
  if (state) render();
};

document.querySelectorAll("#view-switcher [data-view]").forEach((button) => {
  button.onclick = () => selectView(button.dataset.view);
  button.onkeydown = (event) => {
    const modes = ["town", "board", "compact"];
    let index = modes.indexOf(button.dataset.view);
    if (event.key === "ArrowRight") index = (index + 1) % modes.length;
    else if (event.key === "ArrowLeft") index = (index + modes.length - 1) % modes.length;
    else if (event.key === "Home") index = 0;
    else if (event.key === "End") index = modes.length - 1;
    else return;
    event.preventDefault();
    selectView(modes[index]);
    document.querySelector(`#view-switcher [data-view="${modes[index]}"]`).focus();
  };
});

$("#capacity-settings").onclick = () => {
  attention.open();
  renderCapacity();
  $("#capacity-error").textContent = "";
  $("#service-quiet-error").textContent = "";
  $("#service-quiet-success").textContent = "";
  $("#service-quiet-input").value = formatQuietHours(state?.service_config?.quiet_hours);
  $("#capacity-dialog").showModal();
};
$("#service-quiet-form").onsubmit = async (event) => {
  event.preventDefault();
  let windows;
  try {
    windows = parseQuietHours($("#service-quiet-input").value);
  } catch (error) {
    $("#service-quiet-error").textContent = error.message;
    return;
  }
  const submit = event.submitter;
  submit.disabled = true;
  $("#service-quiet-error").textContent = "";
  $("#service-quiet-success").textContent = "";
  try {
    await api("/api/quiet-hours", { windows });
    $("#service-quiet-success").textContent = windows.length ? "Quiet hours saved." : "Service default removed.";
    await refreshState();
  } catch (error) {
    $("#service-quiet-error").textContent = error.message;
  } finally {
    submit.disabled = false;
  }
};
$("#cancel-capacity").onclick = () => $("#capacity-dialog").close();
$("#capacity-form").onsubmit = async (event) => {
  event.preventDefault();
  const value = Number($("#capacity-input").value);
  if (!Number.isInteger(value) || value < 1 || value > 64) {
    $("#capacity-error").textContent = "Capacity must be an integer from 1 to 64.";
    return;
  }
  const submit = event.submitter;
  submit.disabled = true;
  $("#capacity-error").textContent = "";
  try {
    await api("/api/capacity", { max_workers: value });
    $("#capacity-dialog").close();
    await refreshState();
  } catch (error) {
    $("#capacity-error").textContent = error.message;
  } finally {
    submit.disabled = false;
  }
};
// simplifierDeclined marks work the Slop Squad dismissed in auto mode. The
// dismissal is final unless the court admits it anyway.
function simplifierDeclined(task) {
  return task.house === "hall" && task.stage === "declined" && !task.mayoral_decision && task.simplification?.mode === "auto" && task.simplification?.decision === "decline";
}
// The Precinct view draws only when its markup changed: the snapshot stream
// redraws on every event, and rewriting identical markup would drop hover and
// restart every officer's animation mid-gesture.
let rosterMarkup = "",
  flowMarkup = "",
  tankMarkup = "";
// A perp just booked into the tank drops in once; the mark expires so a later
// redraw shows it settled.
const freshPerps = new Map();
const busyGuide = (turn) => ["queued", "gathering", "answering"].includes(turn?.status);
function renderPrecinct() {
  const t = town();
  // The Desk Sergeant's open question radios the units it asks about.
  const asked = new Set(t?.guide?.turns?.find(busyGuide)?.houses || []);
  const markup = t
    ? roster(t)
        .map((unit) => {
          const profile = unit.role === "hall" ? null : profileSummary(unit.worker.profile);
          const load = unit.role === "hall"
            ? `<span class="rulings">${unit.rulings} awaiting ruling</span>${workloadChips(unit.counts)}`
            : `${profileChips(unit.worker.profile, unit.role)}${workloadChips(unit.counts)}`;
          const summary = `${unit.name}, ${unit.status}, ${unit.role === "hall" ? `${unit.rulings} awaiting ruling, ` : ""}${workloadText(unit.counts)}${profile ? `, ${profile.text}` : ""}`;
          return `<button class="unit light-${unit.light}${selectedHouse === unit.role ? " selected" : ""}${asked.has(unit.role) ? " radioed" : ""}" data-house="${unit.role}" aria-label="Open ${esc(summary)}" title="${esc(`${unit.callsign} · ${unit.name} · ${unit.bot}\n${unit.duty}`)}${profile ? `\n${esc(profile.title)}` : ""}" aria-keyshortcuts="${unitOrder.indexOf(unit.role) + 1}"><span class="unit-sign">${robotCop(unit.role, unit.light)}</span><span class="unit-name"><span class="unit-callsign">${unit.callsign}</span><strong>${esc(unit.name)}</strong><small>${esc(unit.bot)}</small></span><span class="unit-duty"><i class="dot ${unit.light}"></i>${esc(unit.status)}</span><span class="unit-load">${load}</span></button>`;
        })
        .join("")
    : "";
  const list = $("#roster");
  if (markup !== rosterMarkup || !list.children?.length) {
    rosterMarkup = markup;
    list.innerHTML = markup;
    list.querySelectorAll("button").forEach((b) => (b.onclick = () => chooseHouse(b.dataset.house)));
  }
  renderCaseFlow(t);
  renderTank(t);
}
// openCase opens a case file from anywhere a case shows up: a lane, the tank,
// a squad car on the street.
function openCase(id, house = "") {
  const task = town()?.tasks[id];
  if (!task) return;
  selectedHouse = isUnit(task.house) ? task.house : isUnit(house) ? house : "hall";
  selectedTask = task.id;
  clearLiveDetail();
  $("#inspector").classList.add("open");
  render();
}
function renderCaseFlow(t) {
  const markup = t
    ? caseFlow(t)
        .map((lane) => {
          const cases = lane.cases
            .map((task) => `<button class="case status-${esc(task.statusClass)}" data-task="${esc(task.id)}" title="${esc(`${caseNumber(task)} · ${task.title || task.id}\n${unitName(task.house)} · ${task.statusLabel}`)}"><span class="case-number">${esc(caseNumber(task))}</span><span class="case-stamp">${esc(task.statusLabel)}</span><span class="case-title">${esc(task.title || task.id)}</span></button>`)
            .join("");
          const more = lane.more
            ? `<button class="case-more" data-house="${esc(lane.moreHouse)}">+${lane.more} more in ${esc(unitName(lane.moreHouse))}</button>`
            : "";
          return `<section class="lane${lane.urgent ? " urgent" : ""}" data-lane="${lane.id}" aria-label="${esc(`${lane.label}: ${lane.total} open case${lane.total === 1 ? "" : "s"}`)}"><div class="lane-head"><strong>${esc(lane.label)}<span class="lane-count${lane.total ? "" : " zero"}">${lane.total}</span></strong><small>${lane.roles.map(callsign).join(" · ")}</small></div><div class="lane-cases">${cases || '<p class="lane-empty">No open cases.</p>'}${more}</div></section>`;
        })
        .join("")
    : "";
  const flow = $("#caseflow");
  if (markup !== flowMarkup || !flow.children?.length) {
    flowMarkup = markup;
    flow.innerHTML = markup;
    flow.querySelectorAll("[data-task]").forEach((b) => (b.onclick = () => openCase(b.dataset.task)));
    flow.querySelectorAll(".case-more").forEach((b) => (b.onclick = () => chooseHouse(b.dataset.house)));
  }
  flow.hidden = !t;
}
// The Slop Tank holds every case the squad threw out, each one a cuffed blob
// with its case number on the placard.
function renderTank(t) {
  const tank = $("#tank"),
    count = $("#tank-count");
  if (!tank) return;
  const now = Date.now();
  const held = custody(t);
  const markup = held.perps
    .map((task) => `<button class="perp${(freshPerps.get(task.id) || 0) > now ? " fresh" : ""}" data-task="${esc(task.id)}" title="${esc(`${caseNumber(task)} · ${task.title || task.id}\nDismissed${task.simplification?.mode === "auto" ? " by the Slop Squad" : " by the court"}`)}">${slopBlob({ cuffed: true })}<span class="placard">${esc(caseNumber(task))}</span></button>`)
    .join("") || '<p class="tank-empty">Cell\'s empty. Nobody has been booked yet.</p>';
  if (markup !== tankMarkup || !tank.children?.length) {
    tankMarkup = markup;
    tank.innerHTML = markup;
    tank.querySelectorAll("[data-task]").forEach((b) => (b.onclick = () => openCase(b.dataset.task, "hall")));
  }
  if (count) count.textContent = held.total ? `${held.total} in custody${held.total > held.perps.length ? ` · newest ${held.perps.length} shown` : ""}` : "";
  tank.hidden = !t;
}
// stageScenes plays what the last snapshot committed: a squad car for each
// transfer, and an arrest for each case newly thrown out. It only decorates;
// with Motion off, a reduced-motion preference, or no layout, it does nothing.
function stageScenes() {
  const runs = transfers,
    busts = arrests;
  transfers = [];
  arrests = [];
  if ((!runs.length && !busts.length) || !motion || overview || viewMode !== "town" || document.hidden) return;
  const ctx = {
    frame: $(".flow-frame"),
    flow: $("#caseflow"),
    street: $(".street"),
    layer: $("#transfers"),
    tank: $("#tank"),
    open: (id, house) => openCase(id, house),
    booked: (id) => {
      freshPerps.set(id, Date.now() + 1500);
      tankMarkup = "";
      renderTank(town());
    },
  };
  if (!canStage(ctx)) return;
  runs.filter((event) => event.town === selectedTown).forEach((event, i) => setTimeout(() => (event.kind === "error" ? alarm(ctx, event) : patrolRun(ctx, event)), i * 450));
  busts.forEach((id, i) => {
    const task = town()?.tasks[id];
    if (task) setTimeout(() => arrest(ctx, task), 600 + i * 1800);
  });
}
// The snapshot stream re-renders the inspector on every event. Rewriting
// identical markup tears the panel down mid-read and drops the reader back at
// the top, so the panel only swaps when its content actually changed, and a
// swap within the same selection keeps every scroll position it had.
let inspectionSignature = "";
function inspectionFrames(out) {
  const frames = [];
  for (let el = out; el; el = el.parentElement)
    frames.push([el, el.scrollTop, el.scrollLeft]);
  if (document.scrollingElement)
    frames.push([
      document.scrollingElement,
      document.scrollingElement.scrollTop,
      document.scrollingElement.scrollLeft,
    ]);
  return frames;
}
function writeInspection(out, html) {
  const key = `${selectedTown}\u0000${selectedHouse}\u0000${selectedTask}`;
  const signature = `${key}\u0000${html}`;
  if (signature === inspectionSignature) return false;
  const sameSelection = inspectionSignature.startsWith(`${key}\u0000`);
  const frames = sameSelection ? inspectionFrames(out) : [];
  const queueScroll = sameSelection ? out.querySelector(".house-task-queue")?.scrollTop : undefined;
  inspectionSignature = signature;
  out.innerHTML = html;
  const queue = out.querySelector(".house-task-queue");
  if (queue && queueScroll !== undefined) queue.scrollTop = queueScroll;
  for (const [el, top, left] of frames) {
    if (el.scrollTop !== top) el.scrollTop = top;
    if (el.scrollLeft !== left) el.scrollLeft = left;
  }
  return true;
}
function diagnosticBlock(report, role) {
  if (!report) return '<p class="muted">No saved diagnostic. Check setup to read command availability and GitHub access.</p>';
  const checks = (report.checks || []).filter((check) => !role || !check.role || check.role === role);
  return `<p class="muted">Checked ${esc(new Date(report.at).toLocaleString())}. Run again after changing settings.${report.head ? ` Head <code>${esc(report.head)}</code> · base <code>${esc(report.base)}</code>.` : ""}</p>${checks.map((check) => `<p><strong>${esc(check.status)} · ${esc(check.code.replaceAll("_", " "))}</strong><br>${esc(check.detail)}${check.action ? `<br>Next: ${esc(check.action)}` : ""}${safeURL(check.url) ? `<br><a href="${esc(check.url)}" target="_blank" rel="noopener noreferrer">Open on GitHub ↗</a>` : ""}</p>`).join("")}`;
}
function diagnosticSummary(report) {
  return (report?.checks || []).map((check) => `${check.detail} ${check.action || ""}`.trim()).join(" ");
}
function setupBlock(t, role) {
  return `<section class="setup-diagnostics"><h3>Setup diagnostics</h3><p class="muted">Checks saved settings without starting agents or running verification commands.</p><button id="check-setup" type="button"${busy(writeKey("diagnostics", t.id))}>Check setup</button>${diagnosticBlock(t.diagnostics, role)}</section>`;
}
function bindSetup(out, townId) {
  const button = out.querySelector("#check-setup");
  if (button) button.onclick = () => trackWrite(writeKey("diagnostics", townId), async (signal) => {
    try {
      await api("/api/diagnostics", { town: townId }, signal);
      await refreshState();
      showError("");
    } catch (error) { showError(error.message); }
  });
}

function workerButtons(controls, role) {
  const button = (action, label, primary) => {
    const pending = busy(commandKey(selectedTown, role, action, ""));
    return `<button${primary ? ' class="primary"' : ""} data-action="${action}"${pending || (controls[action] ? "" : " disabled")}>${label}</button>`;
  };
  return `<div class="inspector-actions">${button("start", "▶ Deploy", true)}${button("pause", "Ⅱ Stand down")}${button("stop", "■ Stop now")}</div>`;
}
function renderInspection() {
  const t = town(),
    out = $("#inspection"),
    houseLabel = unitName(selectedHouse),
    unit = units[selectedHouse];
  $("#inspector-town").textContent = t?.config.repo || "UNIT FILE";
  if (!t) {
    writeInspection(
      out,
      '<h2>Pick a unit</h2><p class="muted">Put a repository under watch to open its first precinct.</p>',
    );
    return;
  }
  if (selectedTask && t.tasks[selectedTask]) {
    const task = t.tasks[selectedTask];
    const projected = projectTask(t, task);
    const source = task.source,
      provenance = source?.provenance,
      sourceSummary = source ? `<p><strong>${esc(source.identity.provider)}</strong> via ${esc(source.identity.funnel)} · ${source.eligible ? "eligible" : "not eligible"} · priority ${esc(source.priority.policy)}${provenance?.external_state ? ` · source state ${esc(provenance.external_state)}` : ""}</p><p>Last observed ${provenance?.observed_at ? esc(new Date(provenance.observed_at).toLocaleString()) : "unknown"}${provenance?.revision ? ` · revision <code>${esc(String(provenance.revision).slice(0, 12))}</code>` : ""}</p>${source.last_outcome?.kind && source.last_outcome.kind !== "complete" ? `<p class="uncertainty-note">${esc(source.last_outcome.kind.replaceAll("_", " "))}: ${esc(source.last_outcome.detail || "Source coverage is incomplete")}</p>` : ""}` : "";
    const taskBusy = (action, role) => busy(commandKey(selectedTown, role, action, selectedTask));
    const mayorActions = simplifierDeclined(task)
      ? `<div class="inspector-actions"><button id="admit-task" class="primary"${taskBusy("admit", "hall")}>Admit anyway</button></div><p class="muted">The Slop Squad dismissed this. Admitting overrules it and sends it to ${task.kind === "issue" ? "the Task Force" : "Forensics"}.</p>`
      : task.mayoral_decision !== "pending"
      ? ""
        : `<div class="inspector-actions"><button id="admit-task" class="primary"${taskBusy("admit", "hall")}>${task.audit?.verdict === "changes_needed" ? "Review again" : "Admit the case"}</button><button id="decline-task" class="danger"${taskBusy("decline", "hall")}>Dismiss</button></div><p class="muted">Nothing acts on this ${task.audit?.verdict === "changes_needed" ? "review outcome" : "arrival"} until it is ruled on.</p>`;
    const isMayor = task.mayoral_decision === "pending";
    const liveCapable = isMayor && (task.kind === "issue" || task.kind === "pr") && task.number > 0;
    const kindLabel = task.kind === "pr" ? "PR" : task.kind === "issue" ? "Issue" : task.kind;
    const mayorMeta = liveCapable
      ? `<p><strong>${esc(kindLabel)} #${task.number}</strong> · ${esc(decisionReason(task))}${task.updated ? ` · updated ${esc(new Date(task.updated).toLocaleString())}` : ""}</p><p class="muted">Admitting sends this to ${task.kind === "issue" ? "the Task Force" : "Forensics"}.</p>`
      : "";
    let liveBlock = "";
    if (liveCapable) {
      const key = `${selectedTown}\n${selectedTask}`;
      if (!liveDetail || liveDetail.key !== key) {
        liveDetail = { key, status: state.demo ? "demo" : "loading" };
        if (!state.demo) fetchLiveDetail(selectedTown, selectedTask);
      }
      if (liveDetail.status === "loading") {
        liveBlock = `<p class="muted">Loading live details from GitHub…</p>`;
      } else if (liveDetail.status === "demo") {
        liveBlock = `<p class="muted">Training exercise: simulated arrival, no live source.</p>`;
      } else if (liveDetail.status === "failed") {
        liveBlock = `<p class="muted">Live details unavailable${liveDetail.error ? `: ${esc(liveDetail.error)}` : ""}. The summary above is current as of the last sync.</p>`;
      } else {
        const d = liveDetail.data;
        const comments = d.kind === "pr" && d.review_comments
          ? `${d.comments} comments · ${d.review_comments} review comments`
          : `${d.comments} comment${d.comments === 1 ? "" : "s"}`;
        liveBlock = `<p class="muted">${d.author ? `Opened by ${esc(d.author)} · ` : ""}${esc(comments)} · ${esc(d.state)} · updated ${esc(new Date(d.updated_at).toLocaleString())}</p>${d.body ? `<pre>${esc(d.body)}</pre>${d.truncated ? `<p class="muted">Truncated here — the rest is at the source link above.</p>` : ""}` : `<p class="muted">No description provided at the source.</p>`}`;
      }
    }
    const simplifier = task.simplification
      ? `<section class="advisor-note"><strong>Slop Squad · ${esc(task.simplification.mode)} mode · ${esc(task.simplification.decision === "decline" ? "dismiss" : task.simplification.decision)}</strong>${task.simplification.summary ? `<p>${esc(task.simplification.summary)}</p>` : ""}<p>${esc(task.simplification.detail)}</p></section>`
      : "";
    const detailText = task.merge_wait && task.stage === "ready" && (task.detail || "").trim() === diagnosticSummary(task.merge_wait) ? "" : task.detail || (liveCapable ? "" : "Following the case.");
    const snooze = projected.snooze;
    const snoozeBlock = snooze
      ? `<section class="snooze-note" aria-label="Snooze"><strong>${esc(snoozeLabel(task))}</strong>${snooze.reason ? `<p>${esc(snooze.reason)}</p>` : ""}<p class="muted">Resumes ${esc(snooze.until.toLocaleString())} without any action. Its unit keeps working the rest of the caseload; no agent starts and no merge happens for this case until then.</p><div class="inspector-actions"><button id="snooze-task" type="button">Change snooze…</button><button id="clear-snooze" type="button"${taskBusy("undefer", selectedHouse)}>Resume now</button></div></section>`
      : taskSnoozable(task)
        ? `<div class="inspector-actions"><button id="snooze-task" type="button">Snooze…</button></div>`
        : "";
    if (!writeInspection(out, `<button id="back-house" class="quiet">← ${esc(houseLabel)}</button><h2>${esc(task.title)}</h2><div class="status-line status-${esc(projected.statusClass)}"><span class="status-chip">${esc(projected.statusLabel)}</span> · ${esc(task.stage)}${task.external ? " · external arrival" : ""}</div>${mayorActions}${snoozeBlock}<div class="task-detail">${safeURL(task.url) ? `<a href="${esc(task.url)}" target="_blank" rel="noopener noreferrer">Open at source ↗</a>` : ""}${mayorMeta}${simplifier}${sourceSummary}${task.merge_wait && task.stage === "ready" ? `<section><h3>Last merge check</h3>${diagnosticBlock(task.merge_wait)}</section>` : ""}${detailText ? `<p>${esc(detailText)}</p>` : ""}${issueJobDetails(task).map((detail) => `<p>${esc(detail)}</p>`).join("")}${task.head ? `<p>Revision <code>${esc(task.head.slice(0, 10))}</code> · repair round ${task.cycles}</p>` : ""}${liveBlock}${task.audit ? `<h3>${esc(task.audit.verdict.replaceAll("_", " "))}</h3><p>${esc(task.audit.summary)}</p>${task.audit.findings.map((f) => `<p><strong>${esc(f.state)}</strong> ${esc(f.detail)}</p>`).join("")}` : ""}${projected.intent?.detail ? `<p class="uncertainty-note">${esc(projected.intent.detail)}</p>` : ""}</div>${taskRetryEligible(task) || projected.status === "uncertain_write" || projected.status === "inconclusive" ? `<button id="retry-task" class="primary"${taskBusy("retry", selectedHouse)}>Reconcile and retry</button>${snooze ? '<p class="muted">Retry clears the block but keeps the snooze: the case still waits for its resume time unless you choose Resume now.</p>' : ""}` : ""}`))
      return;
    $("#back-house").onclick = () => {
      selectedTask = "";
      clearLiveDetail();
      renderInspection();
    };
    if ($("#retry-task"))
      $("#retry-task").onclick = () =>
        command("retry", selectedHouse, selectedTask);
    const decisionButtons = [...out.querySelectorAll("button")];
    const snoozeButton = decisionButtons.find((button) => button.id === "snooze-task"),
      resumeButton = decisionButtons.find((button) => button.id === "clear-snooze");
    if (snoozeButton) snoozeButton.onclick = () => openSnooze(task);
    if (resumeButton) resumeButton.onclick = () => command("undefer", selectedHouse, selectedTask);
    const admit = decisionButtons.find((button) => button.id === "admit-task"),
      decline = decisionButtons.find((button) => button.id === "decline-task");
    if (admit)
      admit.onclick = () => {
        if (simplifierDeclined(task) && !globalThis.confirm(`Admit ${task.kind === "pr" ? "PR" : "issue"} #${task.number} over the Slop Squad's dismissal? The Squad will act on it, and the dismissal cannot be restored.`)) return;
        return command("admit", "hall", selectedTask);
      };
    if (decline) decline.onclick = () => command("decline", "hall", selectedTask);
    return;
  }
  if (selectedHouse === "hall") {
    const decisions = queueFor(t, "hall").filter((task) => task.mayoral_decision === "pending");
    // Newest first and capped, like the other Courthouse feeds.
    const overrulable = Object.values(t.tasks || {}).filter(simplifierDeclined).sort((a, b) => (b.number ?? 0) - (a.number ?? 0));
    const overrulableBlock = overrulable.length
      ? `<h3>Dismissed by the Slop Squad</h3>${overrulable.slice(0, 20).map((task) => `<button class="task-card" data-task="${esc(task.id)}"><strong>${esc(task.title)}</strong><small>${esc(task.kind)}${task.number > 0 ? ` #${task.number}` : ""} · you can admit it anyway</small></button>`).join("")}${overrulable.length > 20 ? `<p class="muted">${overrulable.length - 20} older dismissals are not shown.</p>` : ""}`
      : "";
    const outcomes = outcomeReport(t.outcomes || [], outcomeDays > 0 ? new Date(Date.now() - outcomeDays * 86400000) : new Date(0));
    const metric = (value, label) => `<span><strong>${value}</strong>${label}</span>`;
    const outcomeRows = outcomes.records.slice().reverse().slice(0, 50).map((record) => {
      const unknown = record.elapsed_ms == null ? "elapsed unknown" : `${Math.round(record.elapsed_ms / 1000)}s`;
      const judgment = record.judgment ? `${record.judgment.value.replaceAll("_", " ")}: ${record.judgment.explanation}` : "unjudged";
      const judging = busy(writeKey("judgment", selectedTown, record.id));
      const judge = record.kind === "finding_filed" ? `<span class="judgment-actions"><button data-judgment="useful" data-outcome="${esc(record.id)}"${judging}>Useful</button><button data-judgment="false_positive" data-outcome="${esc(record.id)}"${judging}>False positive</button></span>` : "";
      const title = esc(record.kind.replaceAll("_", " "));
      const linkedTitle = safeURL(record.url) ? `<a href="${esc(record.url)}" target="_blank" rel="noopener noreferrer">${title}</a>` : title;
      const provenance = [record.role, record.task_id || "run-wide", record.revision ? `revision ${record.revision.slice(0, 12)}` : "revision unknown"].filter(Boolean).join(" · ");
      return `<article class="outcome-row"><time>${esc(new Date(record.at).toLocaleString())}</time><strong>${linkedTitle}</strong>${record.detail ? `<p>${esc(record.detail)}</p>` : ""}<p>${esc(record.status)} · ${esc(provenance)} · ${esc(unknown)} · usage ${record.usage == null ? "unknown" : esc(`${record.usage.input_tokens} in / ${record.usage.output_tokens} out`)} · cost ${record.cost_usd == null ? "unknown" : esc(`$${record.cost_usd}`)}</p>${record.kind === "finding_filed" ? `<p>Usefulness: ${esc(judgment)}</p>${judge}` : ""}</article>`;
    }).join("");
    const mayor = t.workers?.hall,
      projectedMayor = projectWorker(t, "hall", mayor),
      mayorControls = workerControls(mayor);
    const mayorBlock = `<div class="status-line"><i class="dot ${projectedMayor.active ? "active" : dutyLight(projectedMayor.status)}"></i>Judge Bot ${esc(dutyStatus(projectedMayor.status))}${mayor?.next && Date.parse(mayor.next) > Date.now() ? ` · next check ${new Date(mayor.next).toLocaleTimeString()}` : ""}</div>${workerButtons(mayorControls, "hall")}<p class="muted">${mayor?.enabled ? "Judge Bot rules on each arrival below as it comes in, with its reason kept on the case, and writes the blotter when work merges." : "Deploy Judge Bot to have it rule on arrivals and keep the blotter. Until then, rulings wait here for you."}</p>${mayor?.task && mayor.task !== "Ready when you are" ? `<p class="muted">${esc(mayor.task)}</p>` : ""}${mayor?.error ? `<p class="muted">${esc(mayor.error)}</p>` : ""}`;
    const bulletinRows = (t.bulletins || []).slice().reverse().slice(0, 20).map((b) => `<article class="bulletin"><h4>${esc(b.title)}</h4><p class="muted">${esc(new Date(b.since).toLocaleString())} – ${esc(new Date(b.until).toLocaleString())} · ${(b.pulls || []).length} merged</p><p>${esc(b.summary)}</p>${(b.items || []).map((item) => `<div class="bulletin-item"><span class="status-chip status-${esc(item.kind)}">${esc(item.kind)}</span> <strong>${esc(item.title)}</strong>${item.detail ? `<p>${esc(item.detail)}</p>` : ""}<small>${(item.pulls || []).map((n) => `PR #${n}`).join(", ")}${(item.issues || []).length ? ` · ${item.issues.map((n) => `issue #${n}`).join(", ")}` : ""}</small></div>`).join("")}</article>`).join("");
    if (!writeInspection(out, `<p class="worker-type">${esc(`${unit.callsign} · ${unit.tagline}`)}</p><h2>${esc(unit.name)}</h2><p class="unit-bot">Judge Bot presiding</p>${mayorBlock}<p class="muted">${mayor?.enabled ? "Judge Bot rules on civilian reports and unit proposals as they arrive; anything it cannot rule on waits here for you." : "Civilian reports and unit proposals wait for your ruling."}</p>${decisions.map((task) => `<button class="task-card" data-task="${esc(task.id)}"><strong>${esc(task.title)}</strong><small>${esc(task.kind)}${task.number > 0 ? ` #${task.number}` : ""} · awaiting ${mayor?.enabled ? "a ruling" : "your ruling"}</small></button>`).join("") || '<p class="muted">Nothing awaits a ruling.</p>'}${overrulableBlock}<h2>The blotter</h2><p class="muted">Judge Bot's public record for the people who use this software: features gained and bugs fixed, from the pull requests that merged.</p>${bulletinRows || '<p class="muted">The blotter is empty. Judge Bot writes an entry after work merges, at most every few hours.</p>'}${quietBlock(t)}${budgetBlock(t)}${setupBlock(t, "hall")}<h2>Clearance stats</h2><div class="outcome-period"><span>Period</span>${[1, 7, 30, 0].map((days) => `<button data-outcome-days="${days}"${days === outcomeDays ? ' class="primary"' : ""}>${days === 0 ? "All" : `${days}d`}</button>`).join("")}<button data-export-outcomes="${outcomeDays}">Export CSV</button></div><div class="outcome-metrics">${metric(outcomes.summary.attempts, "attempts")}${metric(outcomes.summary.findings, "findings")}${metric(outcomes.summary.submitted, "PRs submitted")}${metric(outcomes.summary.merged, "merges")}${metric(outcomes.summary.repairs, "repairs")}${metric(outcomes.summary.blocked, "blocked / abandoned")}${metric(outcomes.summary.releases, "releases")}</div><p class="muted">Finding verdicts: ${outcomes.summary.useful} useful · ${outcomes.summary.falsePositives} false positive · ${outcomes.summary.unjudged} unjudged. Submitted PRs count as artifacts; only repository-confirmed merges count as accepted fixes.</p>${outcomeRows || '<p class="muted">No outcome records in this period.</p>'}<h2>Patrol reports</h2>${
      t.reports
        .slice()
        .reverse()
        .map(
          (r) =>
            `<article class="report"><time>${new Date(r.at).toLocaleTimeString()}</time><h4>${esc(r.title)}</h4><p>${esc(r.body)}</p></article>`,
        )
        .join("") ||
      '<p class="muted">The first report comes in after Patrol\'s first pass.</p>'
    }`))
      return;
    bindSetup(out, t.id);
    out.querySelectorAll("[data-action]").forEach((b) => (b.onclick = () => command(b.dataset.action, "hall")));
    out.querySelectorAll("[data-task]").forEach((button) => {
      button.onclick = () => { selectedTask = button.dataset.task; renderInspection(); };
    });
    out.querySelectorAll("[data-outcome-days]").forEach((button) => {
      button.onclick = () => { outcomeDays = Number(button.dataset.outcomeDays); renderInspection(); };
    });
    out.querySelectorAll("[data-export-outcomes]").forEach((button) => {
      button.onclick = () => exportOutcomes(Number(button.dataset.exportOutcomes)).catch((error) => showError(error.message));
    });
    out.querySelectorAll("[data-judgment]").forEach((button) => {
      button.onclick = async () => {
        const explanation = globalThis.prompt("Explain this usefulness judgment:");
        if (!explanation?.trim()) return;
        const townId = selectedTown;
        await trackWrite(writeKey("judgment", townId, button.dataset.outcome), async (signal) => {
          try {
            await api("/api/outcomes/judgment", { town: townId, outcome: button.dataset.outcome, value: button.dataset.judgment, explanation: explanation.trim() }, signal);
            await refreshState();
          } catch (error) { showError(error.message); }
        });
      };
    });
    return;
  }
  const w = t.workers[selectedHouse],
    projectedWorker = projectWorker(t, selectedHouse, w),
    queue = queueFor(t, selectedHouse);
  if (!w) return;
  const projectedQueue = new Map(queue.map((task) => [task.id, projectTask(t, task)]));
  const queueSummary = [
    [queue.filter((task) => task.kind === "issue").length, "issue", "issues"],
    [queue.filter((task) => task.kind === "pr").length, "pull request", "pull requests"],
    [queue.filter((task) => !["issue", "pr"].includes(task.kind)).length, "other item", "other items"],
    [queue.filter((task) => task.blocked).length, "blocked", "blocked"],
    [queue.filter((task) => projectedQueue.get(task.id).snooze).length, "snoozed", "snoozed"],
  ].filter(([count]) => count > 0)
    .map(([count, singular, plural]) => `${count} ${count === 1 ? singular : plural}`).join(" · ");
  const agent = projectedWorker.profile;
  const releaseBlocked = selectedHouse === "release" && t.config.merge_policy === "manual";
  const controls = workerControls(w, releaseBlocked);
  const funnelDetails = selectedHouse === "issue" || selectedHouse === "repo"
    ? Object.values(t.funnel_syncs || {}).map((sync) => `<p class="muted"><strong>${esc(sync.funnel)}</strong> · ${esc(sync.provider)} · ${esc((sync.outcome?.kind || "incomplete").replaceAll("_", " "))}${sync.last_sync ? ` · ${esc(new Date(sync.last_sync).toLocaleString())}` : ""}${sync.outcome?.detail ? `<br>${esc(sync.outcome.detail)}` : ""}</p>`).join("")
    : "";
  const healthNote = selectedHouse === "repo" ? branchHealthNote(t.health) : "";
  const healthDetails = healthNote ? `<p class="muted">${esc(healthNote)}</p>` : "";
  const agentDetails = `<div class="agent-card"><h3>${projectedWorker.active ? "RUNNING NOW" : "NEXT RUN"}</h3><dl class="agent-profile"><dt>Harness</dt><dd>${esc(agent.harness || "codex-acp")}${agent.harness_version ? ` <span class="muted">${esc(agent.harness_version)}</span>` : ""}</dd><dt>Model</dt><dd>${agent.model ? esc(agent.model) : '<span class="muted">harness default</span>'}</dd><dt>Effort</dt><dd>${agent.effort ? esc(agent.effort) : '<span class="muted">harness default</span>'}</dd></dl><p class="muted">${selectedHouse === "repo" ? "Inventory runs without an agent. The configured agent starts only to repair a failing branch." : agent.source === "active" ? "Captured when this run was dispatched" : agent.inherited === false ? "Set for this unit only" : "Inherited from this precinct's defaults"}</p><button id="configure-agent" type="button">Configure agent</button></div>`;
  if (!writeInspection(out, `<p class="worker-type">${esc(`${unit.callsign} · ${unit.tagline}`)}</p><h2>${esc(houseLabel)}</h2><p class="unit-bot">${esc(unitBot(selectedHouse))} · ${esc(unit.duty)}</p><div class="status-line"><i class="dot ${projectedWorker.active ? "active" : dutyLight(projectedWorker.status)}"></i>${esc(dutyStatus(projectedWorker.status))}${w.next && Date.parse(w.next) > Date.now() ? ` · next check ${new Date(w.next).toLocaleTimeString()}` : ""}</div>${projectedWorker.status === "quiet" ? `<p class="quiet-held">${esc(quietNote(t))}</p>` : ""}${workerButtons(controls, selectedHouse)}${workloadChips(houseWorkload(t, selectedHouse))}${policyBlock(t, selectedHouse)}<h3>CASELOAD · ${queue.length}</h3><p class="queue-summary">${esc(queueSummary)}</p><div class="house-task-queue">${

    queue
      .map(
        (task) =>
          `<button class="task-card${task.policy_excluded ? " filtered" : ""}" data-task="${esc(task.id)}"><strong>${esc(task.title)}</strong><small>${esc(projectedQueue.get(task.id).snooze ? snoozeLabel(task) : projectedQueue.get(task.id).statusLabel)}${task.external ? " · external" : ""}${task.blocked ? " · stuck" : ""}${task.policy_excluded ? " · held by this unit\u2019s filter" : ""}</small></button>`,
      )
      .join("") || '<p class="muted">No open cases.</p>'
  }</div><h3>LAST REPORTED</h3><p class="muted">${esc(w.task || (selectedHouse === "feature" ? "Looks for useful new capabilities by studying this repository" : selectedHouse === "simplifier" ? "Screens arrivals and researches lower-complexity alternatives" : "Standing by for a case"))}</p><p class="authority"><strong>Jurisdiction:</strong> ${esc(houseAuthority(selectedHouse, t.config.merge_policy))}</p>${w.error ? `<p class="muted">${esc(w.error)}</p>` : ""}${agentDetails}${healthDetails}${funnelDetails}${setupBlock(t, selectedHouse)}<h3>UNIT LOG</h3><div class="worker-logs">${
    esc(
      (w.logs || [])
        .slice(-35)
        .map((l) => `${new Date(l.at).toLocaleTimeString()}  ${l.text}`)
        .join("\n"),
    ) || "Nothing on the log yet."
  }</div>`))
    return;
  bindSetup(out, t.id);
  const configure = out.querySelector("#configure-agent");
  if (configure) configure.onclick = () => document.dispatchEvent(
    new CustomEvent("open-settings", { detail: { role: selectedHouse } }),
  );
  out
    .querySelectorAll("[data-action]")
    .forEach(
      (b) => (b.onclick = () => command(b.dataset.action, selectedHouse)),
    );
  out.querySelectorAll("[data-task]").forEach(
    (b) =>
      (b.onclick = () => {
        selectedTask = b.dataset.task;
        renderInspection();
      }),
  );
}
function renderJournal() {
  const events = (state?.events || [])
    .filter((e) => e.town === selectedTown)
    .slice(-30)
    .reverse();
  $("#activity-count").textContent = `· ${events.length}`;
  $("#journal").innerHTML =
    events
      .map(
        (e) =>
          `<li><time>${new Date(e.at).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit", second: "2-digit" })}</time><span class="event-route">${esc(radioRoute(e))}</span><button data-cargo="${esc(e.cargo || "")}" data-house="${esc(e.to || "hall")}">${esc(e.title)}</button></li>`,
      )
      .join("") ||
    '<li class="muted">Quiet on the radio. Traffic shows up here as cases move.</li>';
  $("#journal")
    .querySelectorAll("button")
    .forEach(
      (b) =>
        (b.onclick = () => {
          selectedHouse = isUnit(b.dataset.house) ? b.dataset.house : "hall";
          selectedTask = b.dataset.cargo;
          $("#inspector").classList.add("open");
          renderInspection();
        }),
    );
}
// The snooze dialog edits one task's resume time and reason. It remembers the
// town and task it opened for, so a snapshot that moves the selection while it
// is open cannot redirect the request to another task.
let snoozeTarget = null;
function openSnooze(task) {
  snoozeTarget = { town: selectedTown, house: selectedHouse, task: task.id };
  $("#snooze-title").textContent = task.title || task.id;
  const current = Date.parse(task.deferred_until || "");
  $("#snooze-until").value = Number.isFinite(current) && current > Date.now()
    ? localInputValue(new Date(current))
    : defaultSnoozeUntil();
  $("#snooze-reason").value = task.defer_reason || "";
  $("#snooze-error").textContent = "";
  $("#snooze-dialog").showModal();
}
$("#cancel-snooze").onclick = () => $("#snooze-dialog").close();
$("#snooze-form").onsubmit = async (event) => {
  event.preventDefault();
  if (!snoozeTarget) return;
  const request = snoozeRequest($("#snooze-until").value, $("#snooze-reason").value);
  if (request.error) {
    $("#snooze-error").textContent = request.error;
    return;
  }
  const submit = event.submitter;
  submit.disabled = true;
  $("#snooze-error").textContent = "";
  try {
    await api("/api/control", { town: snoozeTarget.town, role: snoozeTarget.house || "all", action: "defer", task: snoozeTarget.task, until: request.until, reason: request.reason });
    $("#snooze-dialog").close();
    showError("");
  } catch (error) {
    $("#snooze-error").textContent = error.message;
  } finally {
    submit.disabled = false;
  }
};
// A write in flight is part of what the page shows, not a property of one
// element: snapshots rebuild the controls while the request is out, so the
// markup itself carries the disabled state until the request settles.
const pendingWrites = new Set();
const writeKey = (...parts) => parts.join("\u0000");
function busy(key) {
  return pendingWrites.has(key) ? ' disabled aria-busy="true"' : "";
}
// Each tracked write gets a deadline. Its request is aborted then, and the
// control is released even if something after the request (a state refresh)
// is still stuck, so one lost response never disables a button until reload.
async function trackWrite(key, write, report = showError) {
  if (pendingWrites.has(key)) return;
  const focus = focusIdentity(document.activeElement);
  const signal = AbortSignal.timeout(writeTimeoutMs);
  const expired = new Promise((resolve) =>
    signal.addEventListener("abort", () => resolve(expiredWrite), { once: true }),
  );
  pendingWrites.add(key);
  if (state) render();
  try {
    const result = await Promise.race([write(signal), expired]);
    if (result === expiredWrite) report(writeTimeoutMessage);
    return result === expiredWrite ? undefined : result;
  } finally {
    pendingWrites.delete(key);
    if (state) {
      render();
      // Disabling the pressed button dropped focus; give it back.
      if (!document.activeElement || document.activeElement === document.body) restoreFocus(focus);
    }
  }
}
const expiredWrite = Symbol("expired write");
// Which pending write a command belongs to. Admit and decline are one decision
// per task, and the header's wake and pause act on the whole town, so each
// pair shares a key and blocks its partner while either is out.
function commandKey(townId, role, action, task) {
  if (role === "hall" && task && (action === "admit" || action === "decline"))
    return writeKey("decide", townId, task);
  if (role === "all" && !task) return writeKey("town", townId);
  return writeKey("control", townId, role, action, task);
}
async function command(action, role = "all", task = "", townId = selectedTown) {
  return trackWrite(commandKey(townId, role, action, task), async (signal) => {
    try {
      await api("/api/control", { town: townId, role, action, task }, signal);
      showError("");
    } catch (e) {
      showError(e.message);
    }
  });
}
// The inbox lists what waits on a ruling in every town and points at the
// exact unit where the ruling or retry lives.  It re-renders on every
// snapshot so a decision made elsewhere disappears without a reload.
function inboxLabel(item) {
  const kind = item.kind === "pr" ? "PR" : item.kind === "issue" ? "Issue" : item.kind;
  return [item.number > 0 ? `${kind} #${item.number}` : kind, ago(item.updated)].filter(Boolean);
}
function inboxGroups(items, card) {
  const groups = new Map();
  for (const item of items) {
    if (!groups.has(item.repo)) groups.set(item.repo, []);
    groups.get(item.repo).push(item);
  }
  return [...groups]
    .map(([repo, rows]) => `<section class="inbox-town"><h3>${esc(repo)}</h3>${rows.map(card).join("")}</section>`)
    .join("");
}
function renderInbox(needs = inbox(state)) {
  const count = $("#inbox-count"),
    toggle = $("#inbox-toggle");
  count.textContent = String(needs.total);
  count.hidden = !needs.total;
  count.classList.toggle("decisions", needs.decisions.length > 0);
  const summary = needs.total
    ? `${needs.decisions.length} awaiting your ruling · ${needs.attention.length} stuck`
    : "Nothing needs you right now";
  toggle.title = `${summary} · across every precinct`;
  toggle.setAttribute("aria-label", `Needs you: ${summary}`);
  const openButton = (item, label, primary) =>
    `<button class="${primary ? "primary" : ""}" data-inbox-key="open:${esc(item.task || item.house)}" data-inbox-town="${esc(item.town)}" data-inbox-house="${esc(item.house)}" data-inbox-task="${esc(item.task)}" data-inbox-open="1">${label}</button>`;
  // One decision per task at a time, whichever of its two buttons was pressed.
  const deciding = (item) => busy(commandKey(item.town, "hall", "admit", item.task));
  const decisionCard = (item) =>
    `<article class="inbox-item decide"><div><strong>${esc(item.title)}</strong><small>${esc([...inboxLabel(item), item.reason].join(" · "))} · admitting sends it to ${item.kind === "issue" ? "the Task Force" : "Forensics"}</small></div><div class="inbox-actions">${openButton(item, "Open in Courthouse", true)}<button data-inbox-key="admit:${esc(item.task)}" data-inbox-town="${esc(item.town)}" data-inbox-task="${esc(item.task)}" data-inbox-decide="admit"${deciding(item)}>${item.reviewAgain ? "Review again" : "Admit"}</button><button class="danger" data-inbox-key="decline:${esc(item.task)}" data-inbox-town="${esc(item.town)}" data-inbox-task="${esc(item.task)}" data-inbox-decide="decline"${deciding(item)}>Dismiss</button></div></article>`;
  const expanded = new Set([...$("#inbox-list").querySelectorAll("[data-inbox-detail]")]
    .filter((detail) => detail.open).map((detail) => detail.dataset.inboxDetail));
  const attentionCard = (item) => {
    const guidance = attentionGuidance(item);
    const configure = guidance.configure
      ? `<button class="primary" data-inbox-key="configure:${esc(item.house)}" data-inbox-town="${esc(item.town)}" data-inbox-house="${esc(item.house)}" data-inbox-configure="1">Configure agent</button>` : "";
    const workflow = guidance.workflow
      ? `<a class="primary" data-inbox-key="workflow:${esc(item.house)}" data-inbox-town="${esc(item.town)}" href="${esc(guidance.workflow)}" target="_blank" rel="noopener noreferrer">View failed workflow ↗</a>` : "";
    const retry = guidance.retryRelease
      ? `<button data-inbox-key="retry:release" data-inbox-town="${esc(item.town)}" data-inbox-retry="1">Retry release…</button>` : "";
    return `<article class="inbox-item attention"><div class="inbox-context"><strong>${esc(guidance.title)}</strong><small><span class="status-chip status-${esc(item.statusClass)}">${esc(item.statusLabel)}</span> · ${esc([isUnit(item.house) ? unitName(item.house) : item.house, ...inboxLabel(item)].join(" · "))}</small><p>${esc(guidance.summary)}</p><p class="inbox-next">${esc(guidance.next)}</p>${item.detail ? `<details data-inbox-detail="${esc(JSON.stringify([item.town, item.task || item.house]))}"><summary data-inbox-key="details:${esc(item.task || item.house)}" data-inbox-town="${esc(item.town)}">Technical details</summary><small>${esc(item.detail)}</small></details>` : ""}</div><div class="inbox-actions">${configure}${workflow}${retry}${openButton(item, item.task ? "Open case" : "Open unit &amp; log", !configure && !workflow)}</div></article>`;
  };
  $("#inbox-list").innerHTML =
    (needs.decisions.length
      ? `<h2>Awaiting your ruling <span class="count">${needs.decisions.length}</span></h2>${inboxGroups(needs.decisions, decisionCard)}`
      : "") +
    (needs.attention.length
      ? `<h2>Stuck <span class="count">${needs.attention.length}</span></h2>${inboxGroups(needs.attention, attentionCard)}`
      : "") ||
    '<p class="muted">Nothing needs you right now. New rulings and stuck cases from every precinct show up here.</p>';
  $("#inbox-list").querySelectorAll("[data-inbox-detail]").forEach((detail) => {
    detail.open = expanded.has(detail.dataset.inboxDetail);
  });
  $("#inbox-list").querySelectorAll("[data-inbox-retry]").forEach((button) => {
    button.disabled = inboxRetries.has(button.dataset.inboxTown);
    button.onclick = () => retryReleaseFromInbox(button.dataset.inboxTown);
  });
  $("#inbox-list").querySelectorAll("[data-inbox-configure]").forEach((button) => {
    button.onclick = () => {
      openInboxItem(button.dataset.inboxTown, button.dataset.inboxHouse, "");
      document.dispatchEvent(new CustomEvent("open-settings", { detail: { role: button.dataset.inboxHouse } }));
    };
  });
  $("#inbox-list")
    .querySelectorAll("[data-inbox-open]")
    .forEach((button) => (button.onclick = () => openInboxItem(button.dataset.inboxTown, button.dataset.inboxHouse, button.dataset.inboxTask)));
  $("#inbox-list")
    .querySelectorAll("[data-inbox-decide]")
    .forEach((button) => (button.onclick = () => decideFromInbox(button.dataset.inboxTown, button.dataset.inboxTask, button.dataset.inboxDecide)));
}
const inboxRetries = new Set();
async function retryReleaseFromInbox(id) {
  if (inboxRetries.has(id)) return;
  const repo = state?.towns[id]?.config?.repo || id;
  if (!globalThis.confirm(`Retry Release Bot for ${repo}? This clears its saved attempt limit and resumes release work. It may publish a release. Continue only after reviewing the previous failure.`)) return;
  inboxRetries.add(id);
  $("#inbox-error").textContent = "";
  renderInbox();
  try {
    await api("/api/control", { town: id, role: "release", action: "retry" });
    await refreshState();
  } catch (error) {
    $("#inbox-error").textContent = error.message;
  } finally {
    inboxRetries.delete(id);
    renderInbox();
  }
}
function openInboxItem(id, house, task) {
  if (!state?.towns[id]) return;
  $("#inbox-dialog").close();
  selectTown(id);
  inspectOperation(id, isUnit(house) ? house : "hall", task);
}
async function decideFromInbox(id, task, action) {
  const key = commandKey(id, "hall", action, task);
  if (pendingWrites.has(key)) return;
  $("#inbox-error").textContent = "";
  const report = (message) => ($("#inbox-error").textContent = message);
  await trackWrite(key, async (signal) => {
    try {
      await api("/api/control", { town: id, role: "hall", action, task }, signal);
      await refreshState();
    } catch (error) {
      report(error.message);
    }
  }, report);
}
function openInbox() {
  if (!state) return;
  $("#inbox-error").textContent = "";
  renderInbox();
  $("#inbox-dialog").showModal();
}
$("#inbox-toggle").onclick = openInbox;
$("#close-inbox").onclick = () => $("#inbox-dialog").close();
$("#town-toggle").onclick = () => command($("#town-toggle").dataset.action || "start");
$("#pause-all").onclick = () => command("pause");
$("#close-inspector").onclick = closeInspector;
for (const id of ["new-town", "empty-add"])
  $("#" + id).onclick = () => {
    $("#add-error").textContent = "";
    $("#add-dialog").showModal();
  };
$("#cancel-add").onclick = () => $("#add-dialog").close();
$("#add-form").onsubmit = async (e) => {
  e.preventDefault();
  const button = e.submitter;
  button.disabled = true;
  try {
    const result = await api("/api/towns", {
      repo: $("#repo-input").value.trim(),
      merge_policy: $("#merge-policy").value,
    });
    selectedTown = result.id;
    overview = false;
    $("#add-dialog").close();
    $("#repo-input").value = "";
  } catch (error) {
    $("#add-error").textContent = error.message;
  } finally {
    button.disabled = false;
  }
};
function motionUI() {
  document.body?.classList?.toggle("still", !motion);
  $("#motion").textContent = motion ? "Motion on" : "Motion off";
  $("#motion").setAttribute("aria-pressed", String(motion));
}
$("#motion").onclick = () => {
  motion = !motion;
  if (!motion) {
    transfers = [];
    arrests = [];
  }
  motionUI();
};
motionUI();
matchMedia("(prefers-reduced-motion: reduce)").addEventListener(
  "change",
  (e) => {
    motion = !e.matches;
    transfers = [];
    arrests = [];
    motionUI();
  },
);
$("#help").onclick = () => $("#help-dialog").showModal();
$("#close-help").onclick = () => $("#help-dialog").close();
document.addEventListener("keydown", (e) => {
  if (e.target.matches("input,select,textarea") || $("dialog[open]") || e.metaKey || e.ctrlKey || e.altKey) return;
  if (e.key === "0") {
    $("#all-towns").click();
    return;
  }
  if (e.key === "?") $("#help-dialog").showModal();
  if (e.key.toLowerCase() === "i") openInbox();
  if (e.key.toLowerCase() === "p") selectView("town");
  if (e.key.toLowerCase() === "b") selectView("board");
  if (e.key.toLowerCase() === "c") selectView("compact");
  if (e.key === "Escape") closeInspector();
  if (/^[1-8]$/.test(e.key)) chooseHouse(unitOrder[Number(e.key) - 1]);
});
setInterval(() => {
  $("#clock").textContent = new Date().toLocaleTimeString([], {
    hour: "2-digit",
    minute: "2-digit",
  });
}, 1000);
connect();

// Read and navigation tools mirror the visible multi-town controls. Worker
// mutation remains an explicit action through the operator controls.
import { registerTownTools } from "./tools.js";
registerTownTools(document.modelContext, () => state, selectTown, chooseHouse);
