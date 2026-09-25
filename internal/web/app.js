import {
  positions,
  houseNames,
  houseAuthority,
  houseShortcuts,
  routePosition,
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
import { management } from "./manage.js";
import { easeDelivery } from "./scenery.js";
import { factions, skinFor, normalizeSkin } from "./skins.js";
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

// The theme is a look, not a lever: switching it repaints the same snapshot
// and never opens a socket, sends a command, or writes to GitHub. The choice
// lives in this browser only, and ?skin=frontline makes a link that opens the
// war map for someone else.
let skin = (() => {
  let requested = "";
  try {
    requested = new URLSearchParams(location.search || "").get("skin") || "";
  } catch {
    requested = "";
  }
  try {
    return normalizeSkin(requested || localStorage.getItem("brokk-town-skin"));
  } catch {
    return normalizeSkin(requested);
  }
})();
let activeSkin = skinFor(skin);
const factionOverrides = (() => {
  try {
    const stored = JSON.parse(localStorage.getItem("brokk-town-factions") || "{}");
    return stored && typeof stored === "object" ? stored : {};
  } catch {
    return {};
  }
})();
function saveFactionOverride(townId, faction) {
  if (faction) factionOverrides[townId] = faction;
  else delete factionOverrides[townId];
  try {
    localStorage.setItem("brokk-town-factions", JSON.stringify(factionOverrides));
  } catch {
    /* Private browsing keeps the choice for this session only. */
  }
}
// Each base's automatic race comes from its repository. Different bases may
// share one of the three races; a browser choice changes only that base.
function currentFaction(townId = selectedTown) {
  return activeSkin.faction(townId, factionOverrides[townId]);
}
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
// A write that gets no answer is not a failed write: Town may have applied it
// before the connection stalled. The message says so rather than inviting a
// blind retry.
const writeTimeoutMs = 30000;
const writeTimeoutMessage = `Town did not answer within ${writeTimeoutMs / 1000} seconds. The request may still have been applied; check its state before trying again.`;
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
  $("#town-version").title = serviceVersion;
  $("#help-version").textContent = serviceVersion ? `Brokk Town ${serviceVersion}` : "";
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
// policyBlock states what this house's filters admit, and how much of the
// repository's inventory they hold back. A filtered item stays listed and
// marked: an operator has to be able to see what their own filter excluded.
function policyBlock(t, house) {
  const policy = (t?.config?.work_policies || []).find((p) => p.role === house);
  const held = Object.values(t?.tasks || {}).filter(
    (task) => task.policy_excluded && task.house === house,
  ).length;
  if (!policy) {
    return held
      ? `<p class="muted">${held} item${held === 1 ? "" : "s"} in this town are held back by another house's filter.</p>`
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
    : '<p class="muted">No quiet hours. Set weekly windows in Town settings, or a service default under Capacity.</p>';
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
    : '<p class="muted">No budget set for this town. Agent work is bounded only by worker capacity and the per-task attempt limits.</p>';
  return `<h2>Agent budget</h2>${held}<p class="muted">This ${esc(budget.period)} (resets ${esc(resets)}): ${esc(attempts)} · ${esc(time)}${esc(untimed)}.</p><p class="muted">Tokens ${esc(usage)} · cost ${esc(cost)}. ${esc(budget.advice || "")}</p>${ceiling}`;
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

// applySkin paints the chrome a skin supplies: body attribute for the
// stylesheet, every data-skin-text label, and the canvas description. It
// touches no town state, so it is safe to call on any snapshot or redraw.
function applySkin() {
  activeSkin = skinFor(skin);
  activeSkin.prepare?.();
  if (document.body) document.body.dataset.skin = activeSkin.id;
  document.querySelectorAll("[data-skin-text]").forEach((element) => {
    const key = element.dataset?.skinText,
      value = key ? activeSkin.text[key] : undefined;
    if (value !== undefined) element.textContent = value;
  });
  const button = $("#skin");
  if (button) {
    button.textContent = activeSkin.switchLabel;
    button.title = activeSkin.switchTitle;
    button.dataset.skin = activeSkin.id;
    button.setAttribute(
      "aria-label",
      `${activeSkin.switchLabel}. ${activeSkin.switchTitle}`,
    );
  }
  const note = $("#skin-note");
  if (note) {
    note.textContent = activeSkin.note;
    note.hidden = !activeSkin.note;
  }
  if (canvas) canvas.setAttribute("aria-label", activeSkin.text["world-aria"]);
  // The cached backdrop belongs to the base and its banner, so it is dropped
  // whenever either could have changed.
  field = null;
  fieldTown = null;
}

function selectSkin(next) {
  skin = normalizeSkin(next);
  try {
    localStorage.setItem("brokk-town-skin", skin);
  } catch {
    /* Private browsing keeps the theme for this session only. */
  }
  moving = [];
  applySkin();
  if (state) render();
}

// A base flies one faction. The picker shows what the repository derives and
// lets a player rename the banner without changing anything Town does.
function renderFactionPicker(t) {
  const picker = $("#faction-picker"),
    select = $("#faction-select");
  if (!picker || !select) return;
  picker.hidden = !activeSkin.supportsFactions || !t;
  if (picker.hidden) return;
  const options = [
    { value: "", label: `Automatic · ${activeSkin.factionLabel(activeSkin.faction(t.id))}` },
    ...factions.map((f) => ({ value: f.id, label: `${f.name} · ${f.species}` })),
  ];
  const signature = options.map((o) => `${o.value}|${o.label}`).join("\n");
  if (select.dataset.options !== signature) {
    select.dataset.options = signature;
    select.replaceChildren(
      ...options.map((o) => {
        const option = document.createElement("option");
        option.value = o.value;
        option.textContent = o.label;
        return option;
      }),
    );
  }
  const override = factionOverrides[t.id];
  select.value = factions.some((f) => f.id === override) ? override : "";
  select.onchange = () => {
    saveFactionOverride(t.id, select.value);
    applySkin();
    render();
  };
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
            `<button class="compact-worker" data-compact-town="${esc(item.town.id)}" data-compact-house="${esc(worker.role)}"><i class="dot ${worker.active ? "active" : worker.status === "failed" ? "blocked" : worker.status === "quiet" ? "quiet" : "waiting"}"></i><strong>${esc(worker.role)}</strong><small>${esc(worker.status)}</small><small>${profileChips(worker.profile, worker.role)}</small><small>${esc(scheduleLabel(worker))}</small></button>`,
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
    toggle.textContent = activeSkin.rewrite("▶ Wake the town");
    toggle.dataset.action = "start";
    detail.hidden = true;
    return;
  }
  const controls = townControls(t);
  chip.hidden = false;
  chip.textContent = activeSkin.rewrite(controls.status);
  chip.className = `status-chip status-${controls.statusClass}`;
  toggle.textContent = activeSkin.rewrite(controls.primary.label);
  toggle.dataset.action = controls.primary.action;
  toggle.classList.toggle("primary", controls.primary.action === "start");
  toggle.title = activeSkin.rewrite(controls.detail || controls.primary.label);
  detail.textContent = activeSkin.rewrite(controls.detail || "");
  detail.hidden = !controls.detail;
  pauseAll.hidden = !controls.secondary;
  if (controls.secondary)
    pauseAll.textContent = activeSkin.rewrite(controls.secondary.label);
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
  renderFactionPicker(t);
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
    ? activeSkin.meta(
        t.id,
        `${t.config.branch || "Reading repository…"} · ${Object.values(t.workers).filter((w) => w.status === "working").length} agents at work · ${townNeeds(t.id).decisions} ${activeSkin.stats.decisions} · ${townNeeds(t.id).attention} ${activeSkin.stats.attention}`,
        currentFaction(t.id),
      )
    : activeSkin.text["empty-meta"];
  $("#towns").innerHTML = Object.values(state.towns)
    .map((item) => {
      const counts = townNeeds(item.id);
      const faction = activeSkin.supportsFactions ? currentFaction(item.id) : "";
      const flags = [
        counts.decisions ? `<span class="town-flag decide">${counts.decisions} to decide</span>` : "",
        counts.attention ? `<span class="town-flag attention">${counts.attention} attention</span>` : "",
      ].join("");
      return `<button class="town-link ${item.id === selectedTown ? "selected" : ""}" data-town="${esc(item.id)}"${faction ? ` data-faction="${esc(faction)}"` : ""}><strong>▧ ${esc(item.config.repo.split("/")[1])}</strong><small>${esc(item.config.repo.split("/")[0])} · ${faction ? `${esc(activeSkin.factionLabel(faction))} · ` : ""}${Object.values(item.workers).filter((w) => w.enabled).length} awake</small>${flags ? `<span class="town-flags">${flags}</span>` : ""}</button>`;
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
  restoreFocus(focus);
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
  $("#repo-owner").textContent = activeSkin.text["overview-owner"];
  $("#town-name").textContent = activeSkin.text["overview-title"];
  const townCount = Object.keys(state.towns).length;
  $("#town-meta").textContent = `${townCount} ${activeSkin.text[townCount === 1 ? "unit-repository" : "unit-repositories"]} · independent workers, queues, and releases`;
  $("#overview").innerHTML =
    Object.values(state.towns)
      .map((t) => {
        const stats = townSummary(t),
          spread = projectTown(t).profiles,
          faction = currentFaction(t.id),
          badge = activeSkin.supportsFactions
            ? `<span class="faction-badge">${esc(activeSkin.factionLabel(faction))}</span>` : "",
          art = activeSkin.supportsFactions
            ? `<div class="town-card-houses frontline-card-art" aria-hidden="true"><span class="base-mini hall"></span><span class="base-mini bug"></span><span class="base-mini release"></span></div>`
            : `<div class="town-card-houses" aria-hidden="true"></div>`;
        return `<button class="town-card" data-visit="${esc(t.id)}"${faction ? ` data-faction="${esc(faction)}"` : ""}><span class="eyebrow">${esc(t.config.repo.split("/")[0])}</span><h2>${esc(t.config.repo.split("/")[1])}</h2>${badge}${spread.distinct.length === 1 ? summaryChips(spread.distinct[0]) : `<span class="profile mixed" title="${esc(spread.distinct.map((p) => p.text).join("\n"))}">${esc(spread.label)}${spread.overrides ? ` · ${spread.overrides} custom` : ""}</span>`}${art}<div class="town-stats"><span><strong>${stats.busy}</strong> ${esc(activeSkin.stats.working)}</span><span><strong>${stats.queued}</strong> ${esc(activeSkin.stats.queued)}</span><span class="${stats.decisions ? "stat-decide" : ""}"><strong>${stats.decisions}</strong> ${esc(activeSkin.stats.decisions)}</span><span><strong>${stats.blocked + stats.failed}</strong> ${esc(activeSkin.stats.attention)}</span></div><p>${esc(repositoryStatus(t))}</p><small>${esc(stats.release)} · ${esc(activeSkin.visit)}</small></button>`;
      })
      .join("") ||
    `<div class="overview-empty"><h2>Your world starts with one repository.</h2><p>${esc(activeSkin.text["overview-empty-body"])}</p></div>`;
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
function pendingDecisions(t) {
  return queueFor(t || {}, "hall").filter((task) => task.mayoral_decision === "pending" && !task.blocked).length;
}
// simplifierDeclined marks work Simplifier declined in auto mode. The decline
// is final unless the Mayor admits it anyway.
function simplifierDeclined(task) {
  return task.house === "hall" && task.stage === "declined" && !task.mayoral_decision && task.simplification?.mode === "auto" && task.simplification?.decision === "decline";
}
function renderHouses() {
  const t = town(),
    faction = currentFaction(t?.id);
  $("#houses").innerHTML = Object.entries(positions)
    .filter(([role]) => isHouse(role))
    .map(([role, [x, y]]) => {
      const w = t?.workers[role],
        worker = projectWorker(t, role, w),
        counts = houseWorkload(t, role),
        status = role === "hall" && worker.status !== "working" ? `${pendingDecisions(t)} to decide` : worker.status,
        houseLabel = activeSkin.houseLabel(role, faction),
        dot =
          status === "working" || status === "pausing"
            ? "active"
            : status === "blocked" || status === "failed"
              ? "blocked"
              : status === "quiet"
                ? "quiet"
                : "waiting";
      // The label has room for two lines above the road, and the second one
      // now carries the dispatch profile: the status word moves onto the dot
      // the legend already explains, and into the label's tooltip.
      const words = status.replaceAll("_", " "),
        profile = role === "hall" ? null : profileSummary(worker.profile),
        agentLabel = role === "hall" ? "" : profile.text,
        name = `<i class="dot ${dot}"></i><span class="house-name">${activeSkin.houseLabelMarkup(role, faction)}</span>`;
      return `<button class="house ${selectedHouse === role ? "selected" : ""}" style="left:${x / 11.2}%;top:${y / 6.8}%;" data-house="${role}" aria-label="Visit ${esc(houseLabel)}, ${esc(words)}, ${workloadText(counts)}${agentLabel ? `, ${agentLabel}` : ""}" title="${esc(houseLabel)} · ${esc(words)} · ${workloadText(counts)}${profile ? `\n${esc(profile.title)}` : ""}" aria-keyshortcuts="${houseShortcuts.indexOf(role) + 1}"><span class="house-label"><strong>${name}</strong>${role === "hall" ? `<small class="hall-count">${esc(words)}</small>` : `${profileChips(worker.profile, role)}<span class="house-counts" title="${workloadText(counts)}" aria-label="${workloadText(counts)}"><span class="workload-active" title="Active items">${counts.active}</span> / <span title="Waiting items">${counts.waiting}</span> / <span class="workload-blocked" title="Blocked items">${counts.blocked}</span></span>`}</span></button>`;
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
  return `<div class="inspector-actions">${button("start", "▶ Start", true)}${button("pause", "Ⅱ Pause")}${button("stop", "■ Stop")}</div>`;
}
function renderInspection() {
  const t = town(),
    out = $("#inspection"),
    faction = currentFaction(t?.id),
    houseLabel = activeSkin.houseLabel(selectedHouse, faction);
  $("#inspector-town").textContent =
    t?.config.repo || activeSkin.text["inspector-empty-title"];
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
    const taskBusy = (action, role) => busy(commandKey(selectedTown, role, action, selectedTask));
    const mayorActions = simplifierDeclined(task)
      ? `<div class="inspector-actions"><button id="admit-task" class="primary"${taskBusy("admit", "hall")}>Admit anyway</button></div><p class="muted">Simplifier declined this. Admitting overrules it and sends it to ${task.kind === "issue" ? "Issue Bot" : "Review Bot"}.</p>`
      : task.mayoral_decision !== "pending"
      ? ""
        : `<div class="inspector-actions"><button id="admit-task" class="primary"${taskBusy("admit", "hall")}>${task.audit?.verdict === "changes_needed" ? "Review again" : "Admit to town"}</button><button id="decline-task" class="danger"${taskBusy("decline", "hall")}>Decline</button></div><p class="muted">Nothing will act on this ${task.audit?.verdict === "changes_needed" ? "review outcome" : "arrival"} until you decide.</p>`;
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
    const detailText = task.merge_wait && task.stage === "ready" && (task.detail || "").trim() === diagnosticSummary(task.merge_wait) ? "" : task.detail || (liveCapable ? "" : "Following the next step through town.");
    const snooze = projected.snooze;
    const snoozeBlock = snooze
      ? `<section class="snooze-note" aria-label="Snooze"><strong>${esc(snoozeLabel(task))}</strong>${snooze.reason ? `<p>${esc(snooze.reason)}</p>` : ""}<p class="muted">Resumes ${esc(snooze.until.toLocaleString())} without any action. Its house keeps working the rest of the queue; no agent starts and no merge happens for this task until then.</p><div class="inspector-actions"><button id="snooze-task" type="button">Change snooze…</button><button id="clear-snooze" type="button"${taskBusy("undefer", selectedHouse)}>Resume now</button></div></section>`
      : taskSnoozable(task)
        ? `<div class="inspector-actions"><button id="snooze-task" type="button">Snooze…</button></div>`
        : "";
    if (!writeInspection(out, `<button id="back-house" class="quiet">← ${esc(houseLabel)}</button><h2>${esc(task.title)}</h2><div class="status-line status-${esc(projected.statusClass)}"><span class="status-chip">${esc(projected.statusLabel)}</span> · ${esc(task.stage)}${task.external ? " · external arrival" : ""}</div>${mayorActions}${snoozeBlock}<div class="task-detail">${safeURL(task.url) ? `<a href="${esc(task.url)}" target="_blank" rel="noopener noreferrer">Open at source ↗</a>` : ""}${mayorMeta}${simplifier}${sourceSummary}${task.merge_wait && task.stage === "ready" ? `<section><h3>Last merge check</h3>${diagnosticBlock(task.merge_wait)}</section>` : ""}${detailText ? `<p>${esc(detailText)}</p>` : ""}${issueJobDetails(task).map((detail) => `<p>${esc(detail)}</p>`).join("")}${task.head ? `<p>Revision <code>${esc(task.head.slice(0, 10))}</code> · repair round ${task.cycles}</p>` : ""}${liveBlock}${task.audit ? `<h3>${esc(task.audit.verdict.replaceAll("_", " "))}</h3><p>${esc(task.audit.summary)}</p>${task.audit.findings.map((f) => `<p><strong>${esc(f.state)}</strong> ${esc(f.detail)}</p>`).join("")}` : ""}${projected.intent?.detail ? `<p class="uncertainty-note">${esc(projected.intent.detail)}</p>` : ""}</div>${taskRetryEligible(task) || projected.status === "uncertain_write" || projected.status === "inconclusive" ? `<button id="retry-task" class="primary"${taskBusy("retry", selectedHouse)}>Reconcile and retry</button>${snooze ? '<p class="muted">Retry clears the block but keeps the snooze: the task still waits for its resume time unless you choose Resume now.</p>' : ""}` : ""}`))
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
        if (simplifierDeclined(task) && !globalThis.confirm(`Admit ${task.kind === "pr" ? "PR" : "issue"} #${task.number} over Simplifier's decline? Town will act on it, and the decline cannot be restored.`)) return;
        return command("admit", "hall", selectedTask);
      };
    if (decline) decline.onclick = () => command("decline", "hall", selectedTask);
    return;
  }
  if (selectedHouse === "hall") {
    const decisions = queueFor(t, "hall").filter((task) => task.mayoral_decision === "pending");
    // Newest first and capped, like the other Town Hall feeds.
    const overrulable = Object.values(t.tasks || {}).filter(simplifierDeclined).sort((a, b) => (b.number ?? 0) - (a.number ?? 0));
    const overrulableBlock = overrulable.length
      ? `<h3>Declined by Simplifier</h3>${overrulable.slice(0, 20).map((task) => `<button class="task-card" data-task="${esc(task.id)}"><strong>${esc(task.title)}</strong><small>${esc(task.kind)}${task.number > 0 ? ` #${task.number}` : ""} · you can admit it anyway</small></button>`).join("")}${overrulable.length > 20 ? `<p class="muted">${overrulable.length - 20} older declined items are not shown.</p>` : ""}`
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
    const mayorBlock = `<div class="status-line"><i class="dot ${projectedMayor.active ? "active" : projectedMayor.status === "failed" ? "blocked" : projectedMayor.status === "quiet" ? "quiet" : "waiting"}"></i>Mayor Bot ${esc(projectedMayor.status === "quiet" ? "quiet hours" : projectedMayor.status)}${mayor?.next && Date.parse(mayor.next) > Date.now() ? ` · next check ${new Date(mayor.next).toLocaleTimeString()}` : ""}</div>${workerButtons(mayorControls, "hall")}<p class="muted">${mayor?.enabled ? "Mayor Bot judges each arrival below as it comes in, with its reason kept on the task, and writes the bulletin when work merges." : "Start Mayor Bot to have it judge arrivals for you and write the bulletin. Until then, decisions wait here for you."}</p>${mayor?.task && mayor.task !== "Ready when you are" ? `<p class="muted">${esc(mayor.task)}</p>` : ""}${mayor?.error ? `<p class="muted">${esc(mayor.error)}</p>` : ""}`;
    const bulletinRows = (t.bulletins || []).slice().reverse().slice(0, 20).map((b) => `<article class="bulletin"><h4>${esc(b.title)}</h4><p class="muted">${esc(new Date(b.since).toLocaleString())} – ${esc(new Date(b.until).toLocaleString())} · ${(b.pulls || []).length} merged</p><p>${esc(b.summary)}</p>${(b.items || []).map((item) => `<div class="bulletin-item"><span class="status-chip status-${esc(item.kind)}">${esc(item.kind)}</span> <strong>${esc(item.title)}</strong>${item.detail ? `<p>${esc(item.detail)}</p>` : ""}<small>${(item.pulls || []).map((n) => `PR #${n}`).join(", ")}${(item.issues || []).length ? ` · ${item.issues.map((n) => `issue #${n}`).join(", ")}` : ""}</small></div>`).join("")}</article>`).join("");
    if (!writeInspection(out, `<p class="worker-type">${esc(activeSkin.houseTagline("hall", faction))}</p><h2>${esc(activeSkin.text["hall-title"])}</h2>${mayorBlock}<p class="muted">${mayor?.enabled ? "Outside work and proposed features are judged by Mayor Bot as they arrive; anything it cannot judge waits here for you." : "Outside work and proposed features wait for your clearance."}</p>${decisions.map((task) => `<button class="task-card" data-task="${esc(task.id)}"><strong>${esc(task.title)}</strong><small>${esc(task.kind)}${task.number > 0 ? ` #${task.number}` : ""} · awaiting ${mayor?.enabled ? "a decision" : "your decision"}</small></button>`).join("") || '<p class="muted">No arrivals need a decision.</p>'}${overrulableBlock}<h2>What changed</h2><p class="muted">Mayor Bot's bulletin for the people who use this software: features gained and bugs fixed, from the pull requests that merged.</p>${bulletinRows || '<p class="muted">No bulletin yet. Mayor Bot writes one after work merges, at most every few hours.</p>'}${quietBlock(t)}${budgetBlock(t)}${setupBlock(t, "hall")}<h2>Automation outcomes</h2><div class="outcome-period"><span>Period</span>${[1, 7, 30, 0].map((days) => `<button data-outcome-days="${days}"${days === outcomeDays ? ' class="primary"' : ""}>${days === 0 ? "All" : `${days}d`}</button>`).join("")}<button data-export-outcomes="${outcomeDays}">Export CSV</button></div><div class="outcome-metrics">${metric(outcomes.summary.attempts, "attempts")}${metric(outcomes.summary.findings, "findings")}${metric(outcomes.summary.submitted, "PRs submitted")}${metric(outcomes.summary.merged, "merges")}${metric(outcomes.summary.repairs, "repairs")}${metric(outcomes.summary.blocked, "blocked / abandoned")}${metric(outcomes.summary.releases, "releases")}</div><p class="muted">Finding judgments: ${outcomes.summary.useful} useful · ${outcomes.summary.falsePositives} false positive · ${outcomes.summary.unjudged} unjudged. Submitted PRs count as artifacts; only repository-confirmed merges count as accepted fixes.</p>${outcomeRows || '<p class="muted">No outcome records in this period.</p>'}<h2>News from repo-bot</h2>${
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
  const agentDetails = `<div class="agent-card"><h3>${projectedWorker.active ? "RUNNING NOW" : "NEXT RUN"}</h3><dl class="agent-profile"><dt>Harness</dt><dd>${esc(agent.harness || "codex-acp")}${agent.harness_version ? ` <span class="muted">${esc(agent.harness_version)}</span>` : ""}</dd><dt>Model</dt><dd>${agent.model ? esc(agent.model) : '<span class="muted">harness default</span>'}</dd><dt>Effort</dt><dd>${agent.effort ? esc(agent.effort) : '<span class="muted">harness default</span>'}</dd></dl><p class="muted">${selectedHouse === "repo" ? "Inventory runs without an agent. The configured agent starts only to repair a failing branch." : agent.source === "active" ? "Captured when this run was dispatched" : agent.inherited === false ? "Set for this house only" : "Inherited from this town's defaults"}</p><button id="configure-agent" type="button">Configure agent</button></div>`;
  if (!writeInspection(out, `<p class="worker-type">${esc(activeSkin.houseTagline(selectedHouse, faction))}</p><h2>${esc(houseLabel)}</h2><div class="status-line"><i class="dot ${projectedWorker.active ? "active" : projectedWorker.status === "failed" ? "blocked" : projectedWorker.status === "quiet" ? "quiet" : "waiting"}"></i>${esc(projectedWorker.status === "quiet" ? "quiet hours" : projectedWorker.status)}${w.next && Date.parse(w.next) > Date.now() ? ` · next check ${new Date(w.next).toLocaleTimeString()}` : ""}</div>${projectedWorker.status === "quiet" ? `<p class="quiet-held">${esc(quietNote(t))}</p>` : ""}${workerButtons(controls, selectedHouse)}${workloadChips(houseWorkload(t, selectedHouse))}${policyBlock(t, selectedHouse)}<h3>${esc(activeSkin.queueHeading(queue.length))}</h3><p class="queue-summary">${esc(queueSummary)}</p><div class="house-task-queue">${

    queue
      .map(
        (task) =>
          `<button class="task-card${task.policy_excluded ? " filtered" : ""}" data-task="${esc(task.id)}"><strong>${esc(task.title)}</strong><small>${esc(projectedQueue.get(task.id).snooze ? snoozeLabel(task) : projectedQueue.get(task.id).statusLabel)}${task.external ? " · external" : ""}${task.blocked ? " · needs attention" : ""}${task.policy_excluded ? " · held by this house\u2019s filter" : ""}</small></button>`,
      )
      .join("") || '<p class="muted">Nothing waiting at the door.</p>'
  }</div><h3>LATEST ACTIVITY</h3><p class="muted">${esc(w.task || (selectedHouse === "feature" ? "Finds useful new features by studying this repository" : selectedHouse === "simplifier" ? "Reviews arrivals and researches lower-complexity alternatives" : "Waiting for work"))}</p><p class="authority"><strong>Authority:</strong> ${esc(houseAuthority(selectedHouse, t.config.merge_policy))}</p>${w.error ? `<p class="muted">${esc(w.error)}</p>` : ""}${agentDetails}${healthDetails}${funnelDetails}${setupBlock(t, selectedHouse)}<h3>WORKBENCH LOG</h3><div class="worker-logs">${
    esc(
      (w.logs || [])
        .slice(-35)
        .map((l) => `${new Date(l.at).toLocaleTimeString()}  ${l.text}`)
        .join("\n"),
    ) || "No activity yet."
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
  const faction = currentFaction(),
    events = (state?.events || [])
    .filter((e) => e.town === selectedTown)
    .slice(-30)
    .reverse();
  $("#activity-count").textContent = `· ${events.length}`;
  $("#journal").innerHTML =
    events
      .map(
        (e) =>
          `<li><time>${new Date(e.at).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit", second: "2-digit" })}</time><span class="event-route">${esc(e.kind === "delivery" ? activeSkin.routeLabel(e, faction) : activeSkin.rewrite(e.kind))}</span><button data-cargo="${esc(e.cargo || "")}" data-house="${esc(e.to || "hall")}">${esc(e.title)}</button></li>`,
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
  // One decision per task at a time, whichever of its two buttons was pressed.
  const deciding = (item) => busy(commandKey(item.town, "hall", "admit", item.task));
  const decisionCard = (item) =>
    `<article class="inbox-item decide"><div><strong>${esc(item.title)}</strong><small>${esc([...inboxLabel(item), item.reason].join(" · "))} · admitting sends it to ${item.kind === "issue" ? "Issue Bot" : "Review Bot"}</small></div><div class="inbox-actions">${openButton(item, "Open in Town Hall", true)}<button data-inbox-key="admit:${esc(item.task)}" data-inbox-town="${esc(item.town)}" data-inbox-task="${esc(item.task)}" data-inbox-decide="admit"${deciding(item)}>${item.reviewAgain ? "Review again" : "Admit"}</button><button class="danger" data-inbox-key="decline:${esc(item.task)}" data-inbox-town="${esc(item.town)}" data-inbox-task="${esc(item.task)}" data-inbox-decide="decline"${deciding(item)}>Decline</button></div></article>`;
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
  const t = town(),
    faction = currentFaction(),
    // The backdrop belongs to a base and its banner: a new town, a new theme
    // or a new faction all require a repaint.
    key = `${activeSkin.id}\u0000${selectedTown}\u0000${faction || ""}`;
  if (!field || fieldTown !== key) {
    field = activeSkin.landscape(selectedTown, faction);
    fieldTown = key;
  }
  ctx.drawImage(field, 0, 0);
  for (const [role, [x, y]] of Object.entries(positions)) {
    if (!isHouse(role)) continue;
    if (role === selectedHouse) {
      ctx.fillStyle = "#b0e98115";
      ctx.beginPath();
      ctx.ellipse(x, y + 72, 118, 28, 0, 0, Math.PI * 2);
      ctx.fill();
    }
    activeSkin.paintInstallation(ctx, { role, x, y, faction, now, motion }, () => {
      if (standaloneBuildings[role]) singleSprite(standaloneBuildings[role], x, y, 235);
      else sprite(buildings, indices[role], x, y, role === "repo" ? 220 : 235);
    });
    const w = t?.workers[role];
    const working = w?.status === "working" || w?.status === "pausing";
    if (working || activeSkin.showIdleOccupants) {
      let threat = null;
      if (motion && activeSkin.showIdleOccupants)
        for (const m of moving) {
          const elapsed = now - m.start;
          if (m.town !== selectedTown || m.to !== role ||
              elapsed < activeSkin.travelMs * 0.68 ||
              elapsed >= activeSkin.travelMs + activeSkin.impactMs) continue;
          const p = routePosition(m.from, m.to,
            easeDelivery(Math.min(1, elapsed / activeSkin.travelMs)));
          if (Math.hypot(p.x - x, p.y - (y + 60)) < 190) threat = p;
        }
      activeSkin.paintOccupants(ctx, { role, x, y, faction, now, motion, working, threat }, (index, px = 0, py = 0, size = 52) =>
        index === "feature"
          ? singleSprite(featureReader, px, py, 58)
          : sprite(actors, index, px, py, size),
      );
    }
  }

  const travel = activeSkin.travelMs;
  moving = moving.filter((m) => now - m.start < travel + activeSkin.impactMs);
  if (motion)
    for (const m of moving) {
      if (m.town !== selectedTown) continue;
      const elapsed = now - m.start,
        progress = Math.min(1, elapsed / travel),
        p = routePosition(
          m.from,
          m.to,
          easeDelivery(progress),
        ),
        right = p.direction >= 0,
        strikeFaction = activeSkin.strikeFaction(m, faction),
        strike = {
          x: p.x,
          y: p.y,
          from: m.from,
          to: m.to,
          cargo: m.cargo,
          now,
          progress,
          direction: right ? 1 : -1,
          faction: strikeFaction,
          motion,
        };
      if (elapsed <= travel)
        activeSkin.drawStrike(ctx, strike, (index, px, py, size) =>
          sprite(actors, index, px, py, size),
        );
      else if (activeSkin.impactMs)
        activeSkin.drawImpact(ctx, {
          x: p.x,
          y: p.y,
          faction: strikeFaction,
          age: (elapsed - travel) / activeSkin.impactMs,
        });
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
  $("#motion").setAttribute("aria-pressed", String(motion));
}
$("#motion").onclick = () => {
  motion = !motion;
  if (!motion) moving = [];
  motionUI();
};
motionUI();
$("#skin").onclick = () =>
  selectSkin(activeSkin.id === "frontline" ? "town" : "frontline");
applySkin();
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
      easeDelivery(Math.min(1, (performance.now() - m.start) / activeSkin.travelMs)),
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
