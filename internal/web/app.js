import {
  positions,
  houseNames,
  houseShortcuts,
  routePosition,
  queueFor,
  visibleEvents,
  safeURL,
  townSummary,
  taskStatuses,
  projectTask,
  projectState,
  projectWorker,
  boardColumns,
  boardColumn,
  normalizeView,
  focusIdentity,
  focusMatches,
  scheduleLabel,
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
const canvas = $("#world"),
  ctx = canvas.getContext("2d"),
  buildings = new Image(),
  actors = new Image(),
  featureStudy = new Image(),
  featureReader = new Image();
buildings.src = "/assets/buildings-atlas.png";
actors.src = "/assets/actors-atlas.png";
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
  const update = $("#update-notice");
  if (state.update) {
    update.textContent = `Upgrade Town to ${state.update.latest}`;
    update.title = state.update.command;
    update.hidden = false;
    update.onclick = async () => {
      if (!confirm(`Upgrade Brokk Town to ${state.update.latest}?\n\nTown installs that exact version and restarts itself; this page reloads when it is back.`)) return;
      update.disabled = true;
      update.textContent = "Upgrading Town…";
      try {
        const result = await api("/api/update", {});
        if (result.restarting) {
          update.textContent = `Town ${state.update.latest} installed · restarting`;
          update.title = "The service restarts itself; this page reloads when it is back";
        } else {
          update.textContent = `Town ${state.update.latest} installed · restart service`;
          update.title = "Restart bt serve to use the installed version";
        }
      } catch (error) {
        update.disabled = false;
        update.textContent = `Upgrade failed · try again`;
        update.title = error.message;
      }
    };
  } else {
    update.hidden = true;
  }
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

function queuedProfileText(task) {
  if (["complete", "closed", "merged", "shipped", "implemented", "declined"].includes(task.stage)) return "";
  return `Next profile: ${task.profile.harness} · ${task.profile.model || "default model"} · ${task.profile.effort || "default effort"}`;
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
            .filter((worker) => worker.active || worker.status === "failed" || worker.status === "pausing")
            .map((worker) => `<button class="board-worker ${worker.status === "failed" ? "failed" : ""}" data-board-town="${esc(item.town.id)}" data-board-house="${esc(worker.role)}"><strong>${esc(worker.role)} · ${esc(worker.status)}</strong><small>${esc(worker.profile.harness)} · ${esc(worker.profile.model || "default")} · ${esc(worker.profile.effort || "default")}</small></button>`)
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
                    `<button class="board-task" data-board-town="${esc(item.town.id)}" data-board-task="${esc(task.id)}" data-board-house="${esc(task.house || "hall")}"><strong>${esc(task.title || task.id)}</strong><small><span class="status-chip status-${esc(task.statusClass)}">${esc(task.statusLabel)}</span> · ${esc(task.house || "town")}${task.number ? ` · #${task.number}` : ""}</small><small>${esc(queuedProfileText(task))}</small></button>`,
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
            `<button class="compact-worker" data-compact-town="${esc(item.town.id)}" data-compact-house="${esc(worker.role)}"><i class="dot ${worker.active ? "active" : worker.status === "failed" ? "blocked" : "waiting"}"></i><strong>${esc(worker.role)}</strong><small>${esc(worker.status)} · ${worker.role === "repo" ? "No agent" : `${esc(worker.profile.harness)}${worker.profile.harness_version ? ` ${esc(worker.profile.harness_version)}` : ""} · ${esc(worker.profile.model || "default")} · ${esc(worker.profile.effort || "default")}`}</small><small>${esc(scheduleLabel(worker))}</small></button>`,
        )
        .join("")}</div><div class="compact-tasks">${item.tasks
        .map(
          (task) =>
            `<button class="compact-task status-${esc(task.statusClass)}" data-compact-town="${esc(item.town.id)}" data-compact-house="${esc(task.house || "hall")}" data-compact-task="${esc(task.id)}"><span>${esc(task.statusLabel)}</span><strong>${esc(task.title || task.id)}</strong><small>${esc(queuedProfileText(task))}</small></button>`,
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
  $("#inspector").classList.remove("open");
  if (state) renderOverview();
}
function chooseHouse(role) {
  overview = false;
  saveScope();
  selectedHouse = role;
  selectedTask = "";
  $("#inspector").classList.add("open");
  render();
}
function render() {
  const focus = focusIdentity(document.activeElement);
  const t = town();
  renderViewSwitcher();
  renderCapacity();
  $("#mode").hidden = !state.demo;
  $("#demo-note").hidden = !state.demo;
  $("#empty").hidden = !!t;
  $("#start-all").disabled = !t;
  $("#pause-all").disabled = !t;
  $("#repo-owner").textContent = t ? t.config.repo.split("/")[0] : "WELCOME TO";
  $("#town-name").textContent = t
    ? t.config.repo.split("/")[1]
    : "Your next little town";
  $("#town-meta").textContent = t
    ? `${t.config.branch || "Reading repository…"} · ${Object.values(t.workers).filter((w) => w.status === "working").length} agents at work · ${Object.values(t.tasks).filter((task) => task.blocked).length} need attention`
    : "Connect a repository to bring its agents together.";
  $("#towns").innerHTML = Object.values(state.towns)
    .map(
      (item) =>
        `<button class="town-link ${item.id === selectedTown ? "selected" : ""}" data-town="${esc(item.id)}"><strong>▧ ${esc(item.config.repo.split("/")[1])}</strong><small>${esc(item.config.repo.split("/")[0])} · ${Object.values(item.workers).filter((w) => w.enabled).length} awake</small></button>`,
    )
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
  if (focus) {
    const root = document.querySelector(`#${focus.surface}`);
    const restored = [...(root?.querySelectorAll("button") || [])].find((button) =>
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
        const stats = townSummary(t);
        return `<button class="town-card" data-visit="${esc(t.id)}"><span class="eyebrow">${esc(t.config.repo.split("/")[0])}</span><h2>${esc(t.config.repo.split("/")[1])}</h2><div class="town-card-houses" aria-hidden="true"></div><div class="town-stats"><span><strong>${stats.busy}</strong> working</span><span><strong>${stats.queued}</strong> at the doors</span><span><strong>${stats.blocked + stats.failed}</strong> need attention</span></div><p>${esc(t.error || t.reports.at(-1)?.title || "Repo-bot is taking the first inventory")}</p><small>${esc(stats.release)} · Visit town →</small></button>`;
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
function renderHouses() {
  const t = town();
  $("#houses").innerHTML = Object.entries(positions)
    .filter(([role]) => indices[role] !== undefined)
    .map(([role, [x, y]]) => {
      const w = t?.workers[role],
        worker = projectWorker(t, role, w),
        count = t ? queueFor(t, role).length : 0,
        status = role === "hall" ? "Town reports" : worker.status,
        dot =
          status === "working" || status === "pausing"
            ? "active"
            : status === "blocked" || status === "failed"
              ? "blocked"
              : "waiting";
      return `<button class="house ${selectedHouse === role ? "selected" : ""}" style="left:${x / 11.2}%;top:${y / 6.8}%;" data-house="${role}" aria-label="Visit ${houseNames[role]}" title="${houseNames[role]} · ${esc(status.replaceAll("_", " "))}" aria-keyshortcuts="${houseShortcuts.indexOf(role) + 1}"><span class="house-label"><strong>${houseNames[role].replace(" BOT", '<span class="bot-suffix"> BOT</span>')}${count ? `<span class="count">${count}</span>` : ""}</strong><small><i class="dot ${dot}"></i>${esc(status.replaceAll("_", " "))}</small></span></button>`;
    })
    .join("");
  $("#houses")
    .querySelectorAll("button")
    .forEach((b) => (b.onclick = () => chooseHouse(b.dataset.house)));
}
function renderInspection() {
  const t = town(),
    out = $("#inspection");
  $("#inspector-town").textContent = t?.config.repo || "House inspector";
  if (!t) {
    out.innerHTML =
      '<h2>Take a look around</h2><p class="muted">Add a repository to establish the first town.</p>';
    return;
  }
  if (selectedTask && t.tasks[selectedTask]) {
    const task = t.tasks[selectedTask];
    const projected = projectTask(t, task);
    const source = task.source,
      provenance = source?.provenance,
      sourceSummary = source ? `<p><strong>${esc(source.identity.provider)}</strong> via ${esc(source.identity.funnel)} · ${source.eligible ? "eligible" : "not eligible"} · priority ${esc(source.priority.policy)}${provenance?.external_state ? ` · source state ${esc(provenance.external_state)}` : ""}</p><p>Last observed ${provenance?.observed_at ? esc(new Date(provenance.observed_at).toLocaleString()) : "unknown"}${provenance?.revision ? ` · revision <code>${esc(String(provenance.revision).slice(0, 12))}</code>` : ""}</p>${source.last_outcome?.kind && source.last_outcome.kind !== "complete" ? `<p class="uncertainty-note">${esc(source.last_outcome.kind.replaceAll("_", " "))}: ${esc(source.last_outcome.detail || "Source coverage is incomplete")}</p>` : ""}` : "";
    const mayorActions = task.mayoral_decision === "pending"
      ? `<div class="inspector-actions"><button id="admit-task" class="primary">${task.audit?.verdict === "changes_needed" ? "Review again" : "Admit to town"}</button><button id="decline-task" class="danger">Decline</button></div><p class="muted">Nothing will act on this ${task.audit?.verdict === "changes_needed" ? "review outcome" : "arrival"} until you decide.</p>`
      : "";
    out.innerHTML = `<button id="back-house" class="quiet">← ${houseNames[selectedHouse] || "House"}</button><h2>${esc(task.title)}</h2><div class="status-line status-${esc(projected.statusClass)}"><span class="status-chip">${esc(projected.statusLabel)}</span> · ${esc(task.stage)}${task.external ? " · external arrival" : ""}</div><div class="task-detail">${safeURL(task.url) ? `<a href="${esc(task.url)}" target="_blank" rel="noopener noreferrer">Open at source ↗</a>` : ""}${sourceSummary}<p>${esc(task.detail || "Following the next step through town.")}</p>${task.head ? `<p>Revision <code>${esc(task.head.slice(0, 10))}</code> · repair round ${task.cycles}</p>` : ""}${task.audit ? `<h3>${esc(task.audit.verdict.replaceAll("_", " "))}</h3><p>${esc(task.audit.summary)}</p>${task.audit.findings.map((f) => `<p><strong>${esc(f.state)}</strong> ${esc(f.detail)}</p>`).join("")}` : ""}${projected.intent?.detail ? `<p class="uncertainty-note">${esc(projected.intent.detail)}</p>` : ""}</div>${mayorActions}${task.blocked || projected.status === "uncertain_write" || projected.status === "inconclusive" ? '<button id="retry-task" class="primary">Reconcile and retry</button>' : ""}`;
    $("#back-house").onclick = () => {
      selectedTask = "";
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
    out.innerHTML = `<p class="worker-type">THE TOWN HALL</p><h2>Mayoral decisions</h2><p class="muted">Outside work and proposed features wait for your clearance.</p>${decisions.map((task) => `<button class="task-card" data-task="${esc(task.id)}"><strong>${esc(task.title)}</strong><small>${esc(task.kind)} · awaiting your decision</small></button>`).join("") || '<p class="muted">No arrivals need your decision.</p>'}<h2>News from repo-bot</h2>${
      t.reports
        .slice()
        .reverse()
        .map(
          (r) =>
            `<article class="report"><time>${new Date(r.at).toLocaleTimeString()}</time><h4>${esc(r.title)}</h4><p>${esc(r.body)}</p></article>`,
        )
        .join("") ||
      '<p class="muted">The first report will arrive after the repository check.</p>'
    }`;
    out.querySelectorAll("[data-task]").forEach((button) => {
      button.onclick = () => { selectedTask = button.dataset.task; renderInspection(); };
    });
    return;
  }
  const w = t.workers[selectedHouse],
    projectedWorker = projectWorker(t, selectedHouse, w),
    queue = queueFor(t, selectedHouse);
  if (!w) return;
  const agent = projectedWorker.profile;
  const funnelDetails = selectedHouse === "issue" || selectedHouse === "repo"
    ? Object.values(t.funnel_syncs || {}).map((sync) => `<p class="muted"><strong>${esc(sync.funnel)}</strong> · ${esc(sync.provider)} · ${esc((sync.outcome?.kind || "incomplete").replaceAll("_", " "))}${sync.last_sync ? ` · ${esc(new Date(sync.last_sync).toLocaleString())}` : ""}${sync.outcome?.detail ? `<br>${esc(sync.outcome.detail)}` : ""}</p>`).join("")
    : "";
  const agentDetails = selectedHouse === "repo"
    ? '<p class="muted">Reports repository state without an agent.</p>'
    : `<p class="muted">${projectedWorker.active ? "Active dispatch" : "Next run"}: ${esc(agent.harness || "codex-acp")}${agent.harness_version ? ` ${esc(agent.harness_version)}` : ""} · ${esc(agent.model || "Default model")} · ${esc(agent.effort || "Default effort")}<br>${agent.source === "active" ? "Captured for this run" : agent.inherited === false ? "Own queued bot profile" : "Town defaults for queued work"}</p><button id="configure-agent" type="button">Configure agent</button>`;
  out.innerHTML = `<p class="worker-type">${{ bug: "THE GREENHOUSE", feature: "THE STUDY", issue: "THE WORKSHOP", review: "THE OBSERVATORY", release: "THE SHIPPING DEPOT", repo: "THE WATCHTOWER" }[selectedHouse]}</p><h2>${houseNames[selectedHouse]}</h2><p class="muted">${esc(w.task || (selectedHouse === "feature" ? "Finds useful new features by studying this repository" : "Waiting for work"))}</p><div class="status-line"><i class="dot ${projectedWorker.active ? "active" : projectedWorker.status === "failed" ? "blocked" : "waiting"}"></i>${esc(projectedWorker.status)}${w.next && Date.parse(w.next) > Date.now() ? ` · next check ${new Date(w.next).toLocaleTimeString()}` : ""}</div><div class="inspector-actions"><button class="primary" data-action="start">▶ Start</button><button data-action="pause">Ⅱ Pause</button><button data-action="stop">■ Stop</button></div>${agentDetails}${funnelDetails}${w.error ? `<p class="muted">${esc(w.error)}</p>` : ""}<h3>AT THE DOOR · ${queue.length}</h3>${
    queue
      .slice(0, 40)
      .map(
        (task) =>
          `<button class="task-card" data-task="${esc(task.id)}"><strong>${esc(task.title)}</strong><small>${esc(task.stage.replaceAll("_", " "))}${task.external ? " · external" : ""}${task.blocked ? " · needs attention" : ""}</small></button>`,
      )
      .join("") || '<p class="muted">Nothing waiting at the door.</p>'
  }<h3>WORKBENCH LOG</h3><div class="worker-logs">${
    esc(
      (w.logs || [])
        .slice(-35)
        .map((l) => `${new Date(l.at).toLocaleTimeString()}  ${l.text}`)
        .join("\n"),
    ) || "No activity yet."
  }</div>`;
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
          selectedHouse =
            indices[b.dataset.house] !== undefined ? b.dataset.house : "hall";
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
    if (indices[role] === undefined) continue;
    if (role === selectedHouse) {
      ctx.fillStyle = "#b0e98115";
      ctx.beginPath();
      ctx.ellipse(x, y + 72, 118, 28, 0, 0, Math.PI * 2);
      ctx.fill();
    }
    if (role === "feature") singleSprite(featureStudy, x, y, 235);
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
$("#start-all").onclick = () => command("start");
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
$("#connect-form").onsubmit = (e) => {
  e.preventDefault();
  token = $("#token-input").value.trim();
  sessionStorage.setItem("brokk-town-token", token);
  $("#connect-dialog").close();
  connect();
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
  if (e.key.toLowerCase() === "t") selectView("town");
  if (e.key.toLowerCase() === "b") selectView("board");
  if (e.key.toLowerCase() === "c") selectView("compact");
  if (e.key === "Escape") closeInspector();
  if (/^[1-7]$/.test(e.key)) chooseHouse(houseShortcuts[Number(e.key) - 1]);
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
      selectedHouse = indices[m.to] !== undefined ? m.to : "hall";
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
