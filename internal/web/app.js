import {
  positions,
  houseNames,
  houseAuthority,
  houseShortcuts,
  routePosition,
  queueFor,
  issueJobDetails,
  taskRetryEligible,
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
  townControls,
  decisionReason,
  inbox,
  attentionGuidance,
  ago,
  profileSummary,
} from "./town.js";
import { management } from "./manage.js";
import { landscape, drawWorking, easeDelivery } from "./scenery.js";
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
  sessionStorage.getItem("brokk-town-token") ||
  "";
if (token) {
  sessionStorage.setItem("brokk-town-token", token);
  history.replaceState(null, "", location.pathname);
}
let overview = (() => {
  try { return localStorage.getItem("brokk-town-scope") !== "town"; }
  catch { return true; }
})();
function saveScope() {
  try { localStorage.setItem("brokk-town-scope", overview ? "all" : "town"); }
  catch {}
}
let viewMode = (() => {
  try {
    return normalizeView(localStorage.getItem("brokk-town-view"));
  } catch {
    return "town";
  }
})();
let state = null,
  selectedTown = (() => {
    try {
      return localStorage.getItem("brokk-town-selected") || "";
    } catch {
      return "";
    }
  })(),
  selectedHouse = "hall",
  selectedTask = "",
  sequence = null,
  moving = [],
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
const canvas = $("#world"),
  ctx = canvas.getContext("2d"),
  buildings = new Image(),
  actors = new Image(),
  simplifierClarifier = new Image(),
  featureStudy = new Image(),
  featureReader = new Image();
buildings.src = "/assets/buildings-atlas.png";
actors.src = "/assets/actors-atlas.png";
simplifierClarifier.src = "/assets/simplifier-clarifier.png";
featureStudy.src = "/assets/feature-study.png";
featureReader.src = "/assets/feature-reader.png";
const crops = [
  [0, 0, 512, 512],
  [512, 0, 512, 480],
  [1024, 0, 512, 512],
  [0, 512, 512, 512],
  [512, 480, 512, 544],
  [1024, 512, 512, 512],
];
let field = null,
  fieldTown = null;
const actorCrops = [
  [116, 145, 333, 296],
  [86, 145, 311, 293],
  [175, 145, 213, 296],
  [49, 77, 428, 327],
  [62, 95, 434, 311],
  [163, 110, 232, 306],
];
const indices = {
  bug: 0, issue: 1, review: 2, release: 3, repo: 4, hall: 5, feature: 6,
};
const standaloneBuildings = { feature: featureStudy, simplifier: simplifierClarifier };
// A house stands on the map whether its art comes from the shared atlas or
// from its own sprite, so every lookup asks whether the role has a house at
// all rather than which sheet it was painted on.
const isHouse = (role) =>
  indices[role] !== undefined || standaloneBuildings[role] !== undefined;
async function api(path, body, signal) {
  const response = await fetch(path, {
    method: body ? "POST" : "GET",
    headers: {
      Authorization: `Bearer ${token}`,
      ...(body ? { "Content-Type": "application/json" } : {}),
    },
    body: body ? JSON.stringify(body) : undefined,
    signal,
  });
  if (!response.ok) {
    const error = await response
      .json()
      .catch(() => ({ error: response.statusText }));
    throw new Error(error.error || response.statusText);
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
async function connect() {
  if (!token) {
    $("#connect-dialog").showModal();
    return;
  }
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
      throw new Error("Town service unavailable");
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
        if (data) receive(JSON.parse(data.slice(6)));
      }
    }
  } catch (error) {
    if (error.name === "AbortError") return;
    $("#connection").textContent = "Reconnecting";
    $("#connection-dot").className = "dot";
    setTimeout(connect, 2000);
  }
}
function receive(next) {
  if (sequence !== null && next.seq >= sequence) {
    for (const event of visibleEvents(next.events, sequence, selectedTown)) {
      if (event.kind === "delivery" && viewMode === "town" && !overview && motion && !document.hidden)
        moving.push({ ...event, start: performance.now() });
    }
  } else {
    moving = [];
  }
  moving = moving.slice(-24);
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
  $("#help-version").textContent = serviceVersion ? `Brokk Town ${serviceVersion}` : "";
  if (!state.towns[selectedTown]) {
    selectedTown = Object.keys(state.towns)[0] || "";
    selectedTask = "";
  }
  if (selectedTask && !state.towns[selectedTown]?.tasks[selectedTask]) selectedTask = "";
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

function selectView(mode) {
  mode = normalizeView(mode);
  viewMode = mode;
  moving = [];
  try {
    localStorage.setItem("brokk-town-view", mode);
  } catch {
    /* Private browsing may disable persistence; the in-memory selector remains usable. */
  }
  render();
}

function renderViewSwitcher() {
  $("#operations-mode").hidden = viewMode === "town";
  document.querySelectorAll("#view-switcher [data-view]").forEach((button) => {
    const selected = button.dataset.view === viewMode;
    button.tabIndex = selected ? 0 : -1;
    button.setAttribute("aria-selected", String(selected));
    button.classList.toggle("selected", selected);
  });
}

function renderCapacity() {
  const { active, limit } = capacityInfo();
  $("#capacity-summary").textContent = `${active}/${limit} workers`;
  $("#capacity-current").textContent = `${active} active · limit ${limit}`;
  if (!$("#capacity-dialog").open)
    $("#capacity-input").value = String(limit);
}

// Every surface spells a dispatch profile the same way: harness, model and
// effort as three chips, dimmed when the house simply inherits town defaults
// and outlined when an operator chose it for that house.
function summaryChips(p, extra = "") {
  return `<span class="profile ${p.inherited ? "inherited" : "own"}${p.live ? " live" : ""}${extra ? ` ${extra}` : ""}" title="${esc(p.title)}"><b class="profile-chip harness">${esc(p.harness)}</b><b class="profile-chip model">${esc(p.model)}</b><b class="profile-chip effort rank-${esc(p.rank)}">${esc(p.effort)}</b></span>`;
}
function profileChips(profile, role = "", extra = "") {
  return summaryChips(profileSummary(profile), extra);
}
function queuedProfileText(task) {
  if (["complete", "closed", "merged", "shipped", "implemented", "declined"].includes(task.stage)) return "";
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
          const workerCards = item.workers
            .map((worker) => ({ worker, counts: houseWorkload(item.town, worker.role) }))
            .filter(({ worker, counts }) => worker.active || worker.status === "failed" || counts.waiting || counts.blocked)
            .map(({ worker, counts }) => `<button class="board-worker ${counts.blocked || worker.status === "failed" ? "failed" : ""}" data-board-town="${esc(item.town.id)}" data-board-house="${esc(worker.role)}"><strong>${esc(worker.role)} · ${esc(worker.status)}</strong>${workloadChips(counts)}<small>${profileChips(worker.profile, worker.role)}</small></button>`)
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
                    `<button class="board-task" data-board-town="${esc(item.town.id)}" data-board-task="${esc(task.id)}" data-board-house="${esc(task.house || "hall")}"><strong>${esc(task.title || task.id)}</strong><small><span class="status-chip status-${esc(task.statusClass)}">${esc(task.statusLabel)}</span> · ${esc(task.house || "town")}${task.number ? ` · #${task.number}` : ""}</small><small>${queuedProfileText(task)}</small></button>`,
                )
                .join("")}</div></div>`;
            })
            .join("");
          return `<article class="board-town"><header><div><span class="eyebrow">${esc(townName.split("/")[0] || "TOWN")}</span><h2>${esc(townName.split("/").slice(1).join("/") || townName)}</h2></div><button class="quiet board-visit" data-board-visit="${esc(item.town.id)}">Open town →</button></header>${workerCards ? `<div class="board-workers"><h3>Workers</h3>${workerCards}</div>` : ""}<div class="board-columns" data-board-columns="${esc(item.town.id)}">${cards || '<p class="muted">No work recorded yet.</p>'}</div></article>`;
        })
        .join("")
    : '<div class="overview-empty"><h2>No towns to show.</h2><p>Add a repository to start tracking operations.</p></div>';
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
      return `<article class="compact-town"><div class="compact-title"><h2>${esc(repo)}</h2><span>${item.active} active · ${item.attention} attention</span></div><div class="compact-workers">${item.workers
        .map(
          (worker) =>
            `<button class="compact-worker" data-compact-town="${esc(item.town.id)}" data-compact-house="${esc(worker.role)}"><i class="dot ${worker.active ? "active" : worker.status === "failed" ? "blocked" : "waiting"}"></i><strong>${esc(worker.role)}</strong><small>${esc(worker.status)}</small><small>${profileChips(worker.profile, worker.role)}</small><small>${esc(scheduleLabel(worker))}</small></button>`,
        )
        .join("")}</div><div class="compact-tasks">${item.tasks
        .map(
          (task) =>
            `<button class="compact-task status-${esc(task.statusClass)}" data-compact-town="${esc(item.town.id)}" data-compact-house="${esc(task.house || "hall")}" data-compact-task="${esc(task.id)}"><span>${esc(task.statusLabel)}</span><strong>${esc(task.title || task.id)}</strong><small>${queuedProfileText(task)}</small></button>`,
        )
        .join("") || '<span class="muted">No open work.</span>'}</div></article>`;
    })
    .join("") || '<div class="overview-empty"><h2>No towns to show.</h2></div>';
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
function renderTownControls(t) {
  const toggle = $("#town-toggle"),
    pauseAll = $("#pause-all"),
    chip = $("#town-state"),
    detail = $("#town-wake-detail");
  toggle.disabled = !t;
  pauseAll.disabled = !t;
  if (!t) {
    chip.hidden = true;
    pauseAll.hidden = true;
    toggle.textContent = "▶ Wake the town";
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
  if (controls.secondary) pauseAll.textContent = controls.secondary.label;
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
  $("#repo-owner").textContent = t ? t.config.repo.split("/")[0] : "WELCOME TO";
  $("#town-name").textContent = t
    ? t.config.repo.split("/")[1]
    : "Your next little town";
  const needs = inbox(state);
  const townNeeds = (id) => needs.towns[id] || { decisions: 0, attention: 0 };
  $("#town-meta").textContent = t
    ? `${t.config.branch || "Reading repository…"} · ${Object.values(t.workers).filter((w) => w.status === "working").length} agents at work · ${townNeeds(t.id).decisions} await your decision · ${townNeeds(t.id).attention} need attention`
    : "Connect a repository to bring its agents together.";
  $("#towns").innerHTML = Object.values(state.towns)
    .map((item) => {
      const counts = townNeeds(item.id);
      const flags = [
        counts.decisions ? `<span class="town-flag decide">${counts.decisions} to decide</span>` : "",
        counts.attention ? `<span class="town-flag attention">${counts.attention} attention</span>` : "",
      ].join("");
      return `<button class="town-link ${item.id === selectedTown ? "selected" : ""}" data-town="${esc(item.id)}"><strong>▧ ${esc(item.config.repo.split("/")[1])}</strong><small>${esc(item.config.repo.split("/")[0])} · ${Object.values(item.workers).filter((w) => w.enabled).length} awake</small>${flags ? `<span class="town-flags">${flags}</span>` : ""}</button>`;
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
  renderHouses();
  renderInspection();
  renderJournal();
  renderManagement();
  renderInbox(needs);
  if (focus) {
    const root = document.querySelector(`#${focus.surface}`);
    const restored = [...(root?.querySelectorAll("button, a, summary") || [])].find((button) =>
      focusMatches(button, focus),
    );
    restored?.focus({ preventScroll: true });
  }
}
function selectTown(id) {
  if (!state?.towns[id]) throw new Error("Unknown town");
  selectedTown = id;
  try {
    localStorage.setItem("brokk-town-selected", id);
  } catch {
    /* Keep the selection for this session when persistence is unavailable. */
  }
  selectedTask = "";
  clearLiveDetail();
  moving = [];
  overview = false;
  saveScope();
  render();
}
function renderOverview() {
  const operations = viewMode !== "town";
  $("#overview").hidden = !overview || operations;
  $("#board").hidden = viewMode !== "board";
  $("#compact").hidden = viewMode !== "compact";
  $(".world").hidden = overview || operations;
  $(".activity").hidden = overview || operations;
  $("#inspector").hidden = operations ? !$("#inspector").classList.contains("open") : overview;
  $(".town-controls").hidden = overview;
  $("#all-towns").classList.toggle("selected", overview);
  if (!overview) return;
  $("#repo-owner").textContent = "YOUR LOCAL WORLD";
  $("#town-name").textContent = "Every town, together.";
  $("#town-meta").textContent =
    `${Object.keys(state.towns).length} repositories · independent workers, queues, and releases`;
  $("#overview").innerHTML =
    Object.values(state.towns)
      .map((t) => {
        const stats = townSummary(t),
          spread = projectTown(t).profiles;
        return `<button class="town-card" data-visit="${esc(t.id)}"><span class="eyebrow">${esc(t.config.repo.split("/")[0])}</span><h2>${esc(t.config.repo.split("/")[1])}</h2>${spread.distinct.length === 1 ? summaryChips(spread.distinct[0]) : `<span class="profile mixed" title="${esc(spread.distinct.map((p) => p.text).join("\n"))}">${esc(spread.label)}${spread.overrides ? ` · ${spread.overrides} custom` : ""}</span>`}<div class="town-card-houses" aria-hidden="true"></div><div class="town-stats"><span><strong>${stats.busy}</strong> working</span><span><strong>${stats.queued}</strong> at the doors</span><span class="${stats.decisions ? "stat-decide" : ""}"><strong>${stats.decisions}</strong> to decide</span><span><strong>${stats.blocked + stats.failed}</strong> need attention</span></div><p>${esc(t.error || t.reports.at(-1)?.title || "Repo-bot is taking the first inventory")}</p><small>${esc(stats.release)} · Visit town →</small></button>`;
      })
      .join("") ||
    '<div class="overview-empty"><h2>Your world starts with one repository.</h2><p>Add a town using New town above. Each repository gets its own team of agents.</p></div>';
  $("#overview")
    .querySelectorAll("[data-visit]")
    .forEach((b) => (b.onclick = () => selectTown(b.dataset.visit)));
}
$("#all-towns").onclick = () => {
  overview = true;
  saveScope();
  moving = [];
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
  renderCapacity();
  $("#capacity-error").textContent = "";
  $("#capacity-dialog").showModal();
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
function pendingDecisions(t) {
  return queueFor(t || {}, "hall").filter((task) => task.mayoral_decision === "pending").length;
}
function renderHouses() {
  const t = town();
  $("#houses").innerHTML = Object.entries(positions)
    .filter(([role]) => isHouse(role))
    .map(([role, [x, y]]) => {
      const w = t?.workers[role],
        worker = projectWorker(t, role, w),
        counts = houseWorkload(t, role),
        status = role === "hall" && worker.status !== "working" ? `${pendingDecisions(t)} to decide` : worker.status,
        dot =
          status === "working" || status === "pausing"
            ? "active"
            : status === "blocked" || status === "failed"
              ? "blocked"
              : "waiting";
      // The label has room for two lines above the road, and the second one
      // now carries the dispatch profile: the status word moves onto the dot
      // the legend already explains, and into the label's tooltip.
      const words = status.replaceAll("_", " "),
        profile = role === "hall" ? null : profileSummary(worker.profile),
        agentLabel = role === "hall" ? "" : profile.text,
        name = `<i class="dot ${dot}"></i><span class="house-name">${houseNames[role].replace(" BOT", '<span class="bot-suffix"> BOT</span>')}</span>`;
      return `<button class="house ${selectedHouse === role ? "selected" : ""}" style="left:${x / 11.2}%;top:${y / 6.8}%;" data-house="${role}" aria-label="Visit ${houseNames[role]}, ${esc(words)}, ${workloadText(counts)}${agentLabel ? `, ${agentLabel}` : ""}" title="${houseNames[role]} · ${esc(words)} · ${workloadText(counts)}${profile ? `\n${esc(profile.title)}` : ""}" aria-keyshortcuts="${houseShortcuts.indexOf(role) + 1}"><span class="house-label"><strong>${name}</strong>${role === "hall" ? `<small class="hall-count">${esc(words)}</small>` : `${profileChips(worker.profile, role)}<span class="house-counts" title="${workloadText(counts)}" aria-label="${workloadText(counts)}"><span class="workload-active" title="Active items">${counts.active}</span> / <span title="Waiting items">${counts.waiting}</span> / <span class="workload-blocked" title="Blocked items">${counts.blocked}</span></span>`}</span></button>`;
    })
    .join("");
  $("#houses")
    .querySelectorAll("button")
    .forEach((b) => (b.onclick = () => chooseHouse(b.dataset.house)));
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
function renderInspection() {
  const t = town(),
    out = $("#inspection");
  $("#inspector-town").textContent = t?.config.repo || "House inspector";
  if (!t) {
    writeInspection(
      out,
      '<h2>Take a look around</h2><p class="muted">Add a repository to establish the first town.</p>',
    );
    return;
  }
  if (selectedTask && t.tasks[selectedTask]) {
    const task = t.tasks[selectedTask];
    const projected = projectTask(t, task);
    const source = task.source,
      provenance = source?.provenance,
      sourceSummary = source ? `<p><strong>${esc(source.identity.provider)}</strong> via ${esc(source.identity.funnel)} · ${source.eligible ? "eligible" : "not eligible"} · priority ${esc(source.priority.policy)}${provenance?.external_state ? ` · source state ${esc(provenance.external_state)}` : ""}</p><p>Last observed ${provenance?.observed_at ? esc(new Date(provenance.observed_at).toLocaleString()) : "unknown"}${provenance?.revision ? ` · revision <code>${esc(String(provenance.revision).slice(0, 12))}</code>` : ""}</p>${source.last_outcome?.kind && source.last_outcome.kind !== "complete" ? `<p class="uncertainty-note">${esc(source.last_outcome.kind.replaceAll("_", " "))}: ${esc(source.last_outcome.detail || "Source coverage is incomplete")}</p>` : ""}` : "";
    const mayorActions = task.mayoral_decision !== "pending"
      ? ""
        : `<div class="inspector-actions"><button id="admit-task" class="primary">${task.audit?.verdict === "changes_needed" ? "Review again" : "Admit to town"}</button><button id="decline-task" class="danger">Decline</button></div><p class="muted">Nothing will act on this ${task.audit?.verdict === "changes_needed" ? "review outcome" : "arrival"} until you decide.</p>`;
    const isMayor = task.mayoral_decision === "pending";
    const liveCapable = isMayor && (task.kind === "issue" || task.kind === "pr") && task.number > 0;
    const kindLabel = task.kind === "pr" ? "PR" : task.kind === "issue" ? "Issue" : task.kind;
    const mayorMeta = liveCapable
      ? `<p><strong>${esc(kindLabel)} #${task.number}</strong> · ${esc(decisionReason(task))}${task.updated ? ` · updated ${esc(new Date(task.updated).toLocaleString())}` : ""}</p><p class="muted">Admitting sends this to ${task.kind === "issue" ? "Issue Bot" : "Review Bot"}.</p>`
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
        liveBlock = `<p class="muted">Demo town: simulated arrival, no live source.</p>`;
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
      ? `<section class="advisor-note"><strong>Simplifier Bot · ${esc(task.simplification.mode)} mode · ${esc(task.simplification.decision)}</strong>${task.simplification.summary ? `<p>${esc(task.simplification.summary)}</p>` : ""}<p>${esc(task.simplification.detail)}</p></section>`
      : "";
    const detailText = task.detail || (liveCapable ? "" : "Following the next step through town.");
    if (!writeInspection(out, `<button id="back-house" class="quiet">← ${houseNames[selectedHouse] || "House"}</button><h2>${esc(task.title)}</h2><div class="status-line status-${esc(projected.statusClass)}"><span class="status-chip">${esc(projected.statusLabel)}</span> · ${esc(task.stage)}${task.external ? " · external arrival" : ""}</div>${mayorActions}<div class="task-detail">${safeURL(task.url) ? `<a href="${esc(task.url)}" target="_blank" rel="noopener noreferrer">Open at source ↗</a>` : ""}${mayorMeta}${simplifier}${sourceSummary}${detailText ? `<p>${esc(detailText)}</p>` : ""}${issueJobDetails(task).map((detail) => `<p>${esc(detail)}</p>`).join("")}${task.head ? `<p>Revision <code>${esc(task.head.slice(0, 10))}</code> · repair round ${task.cycles}</p>` : ""}${liveBlock}${task.audit ? `<h3>${esc(task.audit.verdict.replaceAll("_", " "))}</h3><p>${esc(task.audit.summary)}</p>${task.audit.findings.map((f) => `<p><strong>${esc(f.state)}</strong> ${esc(f.detail)}</p>`).join("")}` : ""}${projected.intent?.detail ? `<p class="uncertainty-note">${esc(projected.intent.detail)}</p>` : ""}</div>${taskRetryEligible(task) || projected.status === "uncertain_write" || projected.status === "inconclusive" ? '<button id="retry-task" class="primary">Reconcile and retry</button>' : ""}`))
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
    const admit = decisionButtons.find((button) => button.id === "admit-task"),
      decline = decisionButtons.find((button) => button.id === "decline-task");
    if (admit) admit.onclick = () => command("admit", "hall", selectedTask);
    if (decline) decline.onclick = () => command("decline", "hall", selectedTask);
    return;
  }
  if (selectedHouse === "hall") {
    const decisions = queueFor(t, "hall").filter((task) => task.mayoral_decision === "pending");
    const outcomes = outcomeReport(t.outcomes || [], outcomeDays > 0 ? new Date(Date.now() - outcomeDays * 86400000) : new Date(0));
    const metric = (value, label) => `<span><strong>${value}</strong>${label}</span>`;
    const outcomeRows = outcomes.records.slice().reverse().slice(0, 50).map((record) => {
      const unknown = record.elapsed_ms == null ? "elapsed unknown" : `${Math.round(record.elapsed_ms / 1000)}s`;
      const judgment = record.judgment ? `${record.judgment.value.replaceAll("_", " ")}: ${record.judgment.explanation}` : "unjudged";
      const judge = record.kind === "finding_filed" ? `<span class="judgment-actions"><button data-judgment="useful" data-outcome="${esc(record.id)}">Useful</button><button data-judgment="false_positive" data-outcome="${esc(record.id)}">False positive</button></span>` : "";
      const title = esc(record.kind.replaceAll("_", " "));
      const linkedTitle = safeURL(record.url) ? `<a href="${esc(record.url)}" target="_blank" rel="noopener noreferrer">${title}</a>` : title;
      const provenance = [record.role, record.task_id || "run-wide", record.revision ? `revision ${record.revision.slice(0, 12)}` : "revision unknown"].filter(Boolean).join(" · ");
      return `<article class="outcome-row"><time>${esc(new Date(record.at).toLocaleString())}</time><strong>${linkedTitle}</strong>${record.detail ? `<p>${esc(record.detail)}</p>` : ""}<p>${esc(record.status)} · ${esc(provenance)} · ${esc(unknown)} · usage ${record.usage == null ? "unknown" : esc(`${record.usage.input_tokens} in / ${record.usage.output_tokens} out`)} · cost ${record.cost_usd == null ? "unknown" : esc(`$${record.cost_usd}`)}</p>${record.kind === "finding_filed" ? `<p>Usefulness: ${esc(judgment)}</p>${judge}` : ""}</article>`;
    }).join("");
    const mayor = t.workers?.hall,
      projectedMayor = projectWorker(t, "hall", mayor),
      mayorControls = workerControls(mayor);
    const mayorBlock = `<div class="status-line"><i class="dot ${projectedMayor.active ? "active" : projectedMayor.status === "failed" ? "blocked" : "waiting"}"></i>Mayor Bot ${esc(projectedMayor.status)}${mayor?.next && Date.parse(mayor.next) > Date.now() ? ` · next check ${new Date(mayor.next).toLocaleTimeString()}` : ""}</div><div class="inspector-actions"><button class="primary" data-action="start"${mayorControls.start ? "" : " disabled"}>▶ Start</button><button data-action="pause"${mayorControls.pause ? "" : " disabled"}>Ⅱ Pause</button><button data-action="stop"${mayorControls.stop ? "" : " disabled"}>■ Stop</button></div><p class="muted">${mayor?.enabled ? "Mayor Bot judges each arrival below as it comes in, with its reason kept on the task, and writes the bulletin when work merges." : "Start Mayor Bot to have it judge arrivals for you and write the bulletin. Until then, decisions wait here for you."}</p>${mayor?.task && mayor.task !== "Ready when you are" ? `<p class="muted">${esc(mayor.task)}</p>` : ""}${mayor?.error ? `<p class="muted">${esc(mayor.error)}</p>` : ""}`;
    const bulletinRows = (t.bulletins || []).slice().reverse().slice(0, 20).map((b) => `<article class="bulletin"><h4>${esc(b.title)}</h4><p class="muted">${esc(new Date(b.since).toLocaleString())} – ${esc(new Date(b.until).toLocaleString())} · ${(b.pulls || []).length} merged</p><p>${esc(b.summary)}</p>${(b.items || []).map((item) => `<div class="bulletin-item"><span class="status-chip status-${esc(item.kind)}">${esc(item.kind)}</span> <strong>${esc(item.title)}</strong>${item.detail ? `<p>${esc(item.detail)}</p>` : ""}<small>${(item.pulls || []).map((n) => `PR #${n}`).join(", ")}${(item.issues || []).length ? ` · ${item.issues.map((n) => `issue #${n}`).join(", ")}` : ""}</small></div>`).join("")}</article>`).join("");
    if (!writeInspection(out, `<p class="worker-type">THE TOWN HALL</p><h2>Mayoral decisions</h2>${mayorBlock}<p class="muted">${mayor?.enabled ? "Outside work and proposed features are judged by Mayor Bot as they arrive; anything it cannot judge waits here for you." : "Outside work and proposed features wait for your clearance."}</p>${decisions.map((task) => `<button class="task-card" data-task="${esc(task.id)}"><strong>${esc(task.title)}</strong><small>${esc(task.kind)}${task.number > 0 ? ` #${task.number}` : ""} · awaiting ${mayor?.enabled ? "a decision" : "your decision"}</small></button>`).join("") || '<p class="muted">No arrivals need a decision.</p>'}<h2>What changed</h2><p class="muted">Mayor Bot's bulletin for the people who use this software: features gained and bugs fixed, from the pull requests that merged.</p>${bulletinRows || '<p class="muted">No bulletin yet. Mayor Bot writes one after work merges, at most every few hours.</p>'}<h2>Automation outcomes</h2><div class="outcome-period"><span>Period</span>${[1, 7, 30, 0].map((days) => `<button data-outcome-days="${days}"${days === outcomeDays ? ' class="primary"' : ""}>${days === 0 ? "All" : `${days}d`}</button>`).join("")}<button data-export-outcomes="${outcomeDays}">Export CSV</button></div><div class="outcome-metrics">${metric(outcomes.summary.attempts, "attempts")}${metric(outcomes.summary.findings, "findings")}${metric(outcomes.summary.submitted, "PRs submitted")}${metric(outcomes.summary.merged, "merges")}${metric(outcomes.summary.repairs, "repairs")}${metric(outcomes.summary.blocked, "blocked / abandoned")}${metric(outcomes.summary.releases, "releases")}</div><p class="muted">Finding judgments: ${outcomes.summary.useful} useful · ${outcomes.summary.falsePositives} false positive · ${outcomes.summary.unjudged} unjudged. Submitted PRs count as artifacts; only repository-confirmed merges count as accepted fixes.</p>${outcomeRows || '<p class="muted">No outcome records in this period.</p>'}<h2>News from repo-bot</h2>${
      t.reports
        .slice()
        .reverse()
        .map(
          (r) =>
            `<article class="report"><time>${new Date(r.at).toLocaleTimeString()}</time><h4>${esc(r.title)}</h4><p>${esc(r.body)}</p></article>`,
        )
        .join("") ||
      '<p class="muted">The first report will arrive after the repository check.</p>'
    }`))
      return;
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
        try {
          await api("/api/outcomes/judgment", { town: selectedTown, outcome: button.dataset.outcome, value: button.dataset.judgment, explanation: explanation.trim() });
          await refreshState();
        } catch (error) { showError(error.message); }
      };
    });
    return;
  }
  const w = t.workers[selectedHouse],
    projectedWorker = projectWorker(t, selectedHouse, w),
    queue = queueFor(t, selectedHouse);
  if (!w) return;
  const queueSummary = [
    [queue.filter((task) => task.kind === "issue").length, "issue", "issues"],
    [queue.filter((task) => task.kind === "pr").length, "pull request", "pull requests"],
    [queue.filter((task) => !["issue", "pr"].includes(task.kind)).length, "other item", "other items"],
    [queue.filter((task) => task.blocked).length, "blocked", "blocked"],
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
  const agentDetails = `<div class="agent-card"><h3>${projectedWorker.active ? "RUNNING NOW" : "NEXT RUN"}</h3><dl class="agent-profile"><dt>Harness</dt><dd>${esc(agent.harness || "codex-acp")}${agent.harness_version ? ` <span class="muted">${esc(agent.harness_version)}</span>` : ""}</dd><dt>Model</dt><dd>${agent.model ? esc(agent.model) : '<span class="muted">harness default</span>'}</dd><dt>Effort</dt><dd>${agent.effort ? esc(agent.effort) : '<span class="muted">harness default</span>'}</dd></dl><p class="muted">${selectedHouse === "repo" ? "Inventory runs without an agent. The configured agent starts only to repair a failing branch." : agent.source === "active" ? "Captured when this run was dispatched" : agent.inherited === false ? "Set for this house only" : "Inherited from this town's defaults"}</p><button id="configure-agent" type="button">Configure agent</button></div>`;
  if (!writeInspection(out, `<p class="worker-type">${{ bug: "THE GREENHOUSE", simplifier: "THE CLARIFIER", feature: "THE STUDY", issue: "THE WORKSHOP", review: "THE OBSERVATORY", release: "THE SHIPPING DEPOT", repo: "THE WATCHTOWER" }[selectedHouse]}</p><h2>${houseNames[selectedHouse]}</h2><div class="status-line"><i class="dot ${projectedWorker.active ? "active" : projectedWorker.status === "failed" ? "blocked" : "waiting"}"></i>${esc(projectedWorker.status)}${w.next && Date.parse(w.next) > Date.now() ? ` · next check ${new Date(w.next).toLocaleTimeString()}` : ""}</div><div class="inspector-actions"><button class="primary" data-action="start"${controls.start ? "" : " disabled"}>▶ Start</button><button data-action="pause"${controls.pause ? "" : " disabled"}>Ⅱ Pause</button><button data-action="stop"${controls.stop ? "" : " disabled"}>■ Stop</button></div>${workloadChips(houseWorkload(t, selectedHouse))}<h3>BOT QUEUE · ${queue.length}</h3><p class="queue-summary">${esc(queueSummary)}</p><div class="house-task-queue">${

    queue
      .map(
        (task) =>
          `<button class="task-card" data-task="${esc(task.id)}"><strong>${esc(task.title)}</strong><small>${esc(projectTask(t, task).statusLabel)}${task.external ? " · external" : ""}${task.blocked ? " · needs attention" : ""}</small></button>`,
      )
      .join("") || '<p class="muted">Nothing waiting at the door.</p>'
  }</div><h3>LATEST ACTIVITY</h3><p class="muted">${esc(w.task || (selectedHouse === "feature" ? "Finds useful new features by studying this repository" : selectedHouse === "simplifier" ? "Reviews arrivals and researches lower-complexity alternatives" : "Waiting for work"))}</p><p class="authority"><strong>Authority:</strong> ${esc(houseAuthority(selectedHouse, t.config.merge_policy))}</p>${w.error ? `<p class="muted">${esc(w.error)}</p>` : ""}${agentDetails}${healthDetails}${funnelDetails}<h3>WORKBENCH LOG</h3><div class="worker-logs">${
    esc(
      (w.logs || [])
        .slice(-35)
        .map((l) => `${new Date(l.at).toLocaleTimeString()}  ${l.text}`)
        .join("\n"),
    ) || "No activity yet."
  }</div>`))
    return;
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
          `<li><time>${new Date(e.at).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit", second: "2-digit" })}</time><span class="event-route">${esc(e.kind === "delivery" ? `${e.from} → ${e.to}` : e.kind)}</span><button data-cargo="${esc(e.cargo || "")}" data-house="${esc(e.to || "hall")}">${esc(e.title)}</button></li>`,
      )
      .join("") ||
    '<li class="muted">The journal will fill as work moves through town.</li>';
  $("#journal")
    .querySelectorAll("button")
    .forEach(
      (b) =>
        (b.onclick = () => {
          selectedHouse = isHouse(b.dataset.house) ? b.dataset.house : "hall";
          selectedTask = b.dataset.cargo;
          $("#inspector").classList.add("open");
          renderInspection();
        }),
    );
}
async function command(action, role = "all", task = "") {
  try {
    await api("/api/control", { town: selectedTown, role, action, task });
    showError("");
  } catch (e) {
    showError(e.message);
  }
}
// The inbox lists what waits on the Mayor in every town and points at the
// exact house where the decision or retry lives.  It re-renders on every
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
    ? `${needs.decisions.length} awaiting your decision · ${needs.attention.length} need attention`
    : "Nothing needs you right now";
  toggle.title = `${summary} · across every town`;
  toggle.setAttribute("aria-label", `Needs you: ${summary}`);
  const openButton = (item, label, primary) =>
    `<button class="${primary ? "primary" : ""}" data-inbox-key="open:${esc(item.task || item.house)}" data-inbox-town="${esc(item.town)}" data-inbox-house="${esc(item.house)}" data-inbox-task="${esc(item.task)}" data-inbox-open="1">${label}</button>`;
  const decisionCard = (item) =>
    `<article class="inbox-item decide"><div><strong>${esc(item.title)}</strong><small>${esc([...inboxLabel(item), item.reason].join(" · "))} · admitting sends it to ${item.kind === "issue" ? "Issue Bot" : "Review Bot"}</small></div><div class="inbox-actions">${openButton(item, "Open in Town Hall", true)}<button data-inbox-key="admit:${esc(item.task)}" data-inbox-town="${esc(item.town)}" data-inbox-task="${esc(item.task)}" data-inbox-decide="admit">${item.reviewAgain ? "Review again" : "Admit"}</button><button class="danger" data-inbox-key="decline:${esc(item.task)}" data-inbox-town="${esc(item.town)}" data-inbox-task="${esc(item.task)}" data-inbox-decide="decline">Decline</button></div></article>`;
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
    return `<article class="inbox-item attention"><div class="inbox-context"><strong>${esc(guidance.title)}</strong><small><span class="status-chip status-${esc(item.statusClass)}">${esc(item.statusLabel)}</span> · ${esc([houseNames[item.house] || item.house, ...inboxLabel(item)].join(" · "))}</small><p>${esc(guidance.summary)}</p><p class="inbox-next">${esc(guidance.next)}</p>${item.detail ? `<details data-inbox-detail="${esc(JSON.stringify([item.town, item.task || item.house]))}"><summary data-inbox-key="details:${esc(item.task || item.house)}" data-inbox-town="${esc(item.town)}">Technical details</summary><small>${esc(item.detail)}</small></details>` : ""}</div><div class="inbox-actions">${configure}${workflow}${retry}${openButton(item, item.task ? "View task" : "View bot &amp; logs", !configure && !workflow)}</div></article>`;
  };
  $("#inbox-list").innerHTML =
    (needs.decisions.length
      ? `<h2>Awaiting your decision <span class="count">${needs.decisions.length}</span></h2>${inboxGroups(needs.decisions, decisionCard)}`
      : "") +
    (needs.attention.length
      ? `<h2>Needs attention <span class="count">${needs.attention.length}</span></h2>${inboxGroups(needs.attention, attentionCard)}`
      : "") ||
    '<p class="muted">Nothing needs you right now. New arrivals and stuck work will appear here from every town.</p>';
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
  inspectOperation(id, isHouse(house) ? house : "hall", task);
}
async function decideFromInbox(id, task, action) {
  $("#inbox-error").textContent = "";
  try {
    await api("/api/control", { town: id, role: "hall", action, task });
    await refreshState();
  } catch (error) {
    $("#inbox-error").textContent = error.message;
  }
}
function openInbox() {
  if (!state) return;
  $("#inbox-error").textContent = "";
  renderInbox();
  $("#inbox-dialog").showModal();
}
$("#inbox-toggle").onclick = openInbox;
$("#close-inbox").onclick = () => $("#inbox-dialog").close();
function sprite(image, index, x, y, size) {
  if (!image.complete || !image.naturalWidth) return;
  const crop =
    image === buildings
      ? crops[index]
      : [
          (index % 3) * 512 + actorCrops[index][0],
          Math.floor(index / 3) * 512 + actorCrops[index][1],
          ...actorCrops[index].slice(2),
        ];
  const width = image === buildings ? size : (size * crop[2]) / crop[3];
  ctx.drawImage(image, ...crop, x - width / 2, y - size / 2, width, size);
}
function singleSprite(image, x, y, height) {
  if (!image.complete || !image.naturalWidth) return;
  const width = (height * image.naturalWidth) / image.naturalHeight;
  ctx.drawImage(image, x - width / 2, y - height / 2, width, height);
}
function draw(now) {
  if (overview || viewMode !== "town" || document.hidden) {
    requestAnimationFrame(draw);
    return;
  }
  if (!field || fieldTown !== selectedTown) {
    field = landscape(selectedTown);
    fieldTown = selectedTown;
  }
  ctx.drawImage(field, 0, 0);
  const t = town();
  for (const [role, [x, y]] of Object.entries(positions)) {
    if (!isHouse(role)) continue;
    if (role === selectedHouse) {
      ctx.fillStyle = "#b0e98115";
      ctx.beginPath();
      ctx.ellipse(x, y + 72, 118, 28, 0, 0, Math.PI * 2);
      ctx.fill();
    }
    if (standaloneBuildings[role]) singleSprite(standaloneBuildings[role], x, y, 235);
    else sprite(buildings, indices[role], x, y, role === "repo" ? 220 : 235);
    const w = t?.workers[role];
    if (w?.status === "working" || w?.status === "pausing") {
      drawWorking(ctx, role, x, y, now, motion, (index) =>
        index === "feature"
          ? singleSprite(featureReader, 0, 0, 58)
          : sprite(actors, index, 0, 0, 52),
      );
    }
  }

  moving = moving.filter((m) => now - m.start < 6500);
  if (motion)
    for (const m of moving) {
      if (m.town !== selectedTown) continue;
      const p = routePosition(
          m.from,
          m.to,
          easeDelivery((now - m.start) / 6500),
        ),
        right = p.direction >= 0,
        truck = m.from === "outside" || m.to === "outside";
      ctx.fillStyle = "#192b2270";
      ctx.beginPath();
      ctx.ellipse(p.x + 6, p.y + 25, truck ? 38 : 23, 7, 0, 0, Math.PI * 2);
      ctx.fill();
      for (let i = 0; i < 3; i++) {
        const dust = (now / 500 + i / 3) % 1;
        ctx.fillStyle = `rgba(219,202,155,${(1 - dust) * 0.3})`;
        ctx.beginPath();
        ctx.ellipse(
          p.x - (right ? 1 : -1) * (20 + dust * 25),
          p.y + 22 - dust * 5,
          2 + dust * 5,
          2 + dust * 2,
          0,
          0,
          Math.PI * 2,
        );
        ctx.fill();
      }
      sprite(
        actors,
        truck ? (right ? 3 : 4) : right ? 0 : 1,
        p.x,
        p.y + Math.sin(now / 100) * 1.3,
        truck ? 57 : 58,
      );
      ctx.fillStyle = "#0b1a10";
      ctx.fillRect(p.x - 34, p.y - 48, 68, 19);
      ctx.fillStyle = "#dbe6cc";
      ctx.font = "11px monospace";
      ctx.textAlign = "center";
      ctx.fillText(
        m.cargo?.replace("issue:", "#").replace("pr:", "PR #").slice(0, 10) ||
          "cargo",
        p.x,
        p.y - 35,
      );
    }
  requestAnimationFrame(draw);
}
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
  $("#motion").textContent = motion ? "Motion on" : "Motion off";
  $("#motion").setAttribute("aria-pressed", String(!motion));
}
$("#motion").onclick = () => {
  motion = !motion;
  if (!motion) moving = [];
  motionUI();
};
motionUI();
matchMedia("(prefers-reduced-motion: reduce)").addEventListener(
  "change",
  (e) => {
    motion = !e.matches;
    moving = [];
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
  if (e.key.toLowerCase() === "t") selectView("town");
  if (e.key.toLowerCase() === "b") selectView("board");
  if (e.key.toLowerCase() === "c") selectView("compact");
  if (e.key === "Escape") closeInspector();
  if (/^[1-9]$/.test(e.key) && houseShortcuts[Number(e.key) - 1])
    chooseHouse(houseShortcuts[Number(e.key) - 1]);
});
canvas.onclick = (e) => {
  const r = canvas.getBoundingClientRect(),
    x = ((e.clientX - r.left) * 1120) / r.width,
    y = ((e.clientY - r.top) * 680) / r.height;
  for (const m of moving) {
    const p = routePosition(
      m.from,
      m.to,
      easeDelivery((performance.now() - m.start) / 6500),
    );
    if (Math.hypot(x - p.x, y - p.y) < 50) {
      selectedTask = m.cargo;
      selectedHouse = isHouse(m.to) ? m.to : "hall";
      $("#inspector").classList.add("open");
      renderInspection();
      return;
    }
  }
};
setInterval(() => {
  $("#clock").textContent = new Date().toLocaleTimeString([], {
    hour: "2-digit",
    minute: "2-digit",
  });
}, 1000);
requestAnimationFrame(draw);
connect();

// Read and navigation tools mirror the visible multi-town controls. Worker
// mutation remains an explicit action through the operator controls.
import { registerTownTools } from "./tools.js";
registerTownTools(document.modelContext, () => state, selectTown, chooseHouse);
