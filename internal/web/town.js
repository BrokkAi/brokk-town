export const positions = {
  bug: [142, 172],
  feature: [420, 172],
  issue: [700, 172],
  review: [978, 172],
  hall: [180, 446],
  repo: [555, 446],
  release: [930, 446],
  outside: [-50, 330],
};
export const houseNames = {
  bug: "BUG BOT",
  feature: "FEATURE BOT",
  issue: "ISSUE BOT",
  review: "REVIEW BOT",
  release: "RELEASE BOT",
  repo: "REPO BOT",
  hall: "TOWN HALL",
};
// Preserve established shortcuts and append the new study on key 7.
export const houseShortcuts = [
  "bug",
  "issue",
  "review",
  "release",
  "repo",
  "hall",
  "feature",
];
export const roadLevels = { upper: 330, lower: 540 };
export const roadEdges = [40, 1080];
export function houseDoor(role) {
  const [x, y] = positions[role] || positions.hall;
  return [x, role === "outside" ? roadLevels.upper : y + 80];
}
export function houseRoad(role) {
  return role === "outside" || houseDoor(role)[1] < roadLevels.upper
    ? roadLevels.upper
    : roadLevels.lower;
}
export const roadSegments = [
  [
    [-50, roadLevels.upper],
    [1170, roadLevels.upper],
  ],
  [
    [-50, roadLevels.lower],
    [1170, roadLevels.lower],
  ],
  ...roadEdges.map((x) => [
    [x, roadLevels.upper],
    [x, roadLevels.lower],
  ]),
  ...Object.keys(houseNames).map((role) => [
    houseDoor(role),
    [positions[role][0], houseRoad(role)],
  ]),
];
export function queueFor(town, role) {
  return Object.values(town.tasks || {})
    .filter(
      (t) =>
        t.house === role &&
        !["closed", "merged", "shipped", "implemented"].includes(t.stage),
    )
    .sort(
      (a, b) =>
        Number(!!b.blocked) - Number(!!a.blocked) ||
        (a.number ?? 0) - (b.number ?? 0),
    );
}
export function visibleEvents(events, after, town) {
  return (events || []).filter((e) => e.seq > after && e.town === town);
}
export function safeURL(value) {
  try {
    const u = new URL(value);
    return (
      u.protocol === "https:" &&
      u.hostname === "github.com" &&
      !u.username &&
      !u.password &&
      !u.port
    );
  } catch {
    return false;
  }
}
export function routePosition(from, to, progress) {
  const source = Object.hasOwn(positions, from) ? from : "outside",
    target = Object.hasOwn(positions, to) ? to : "hall",
    a = houseDoor(source),
    b = target === "outside" ? [1170, roadLevels.lower] : houseDoor(target),
    aRoad = houseRoad(source),
    bRoad = target === "outside" ? roadLevels.lower : houseRoad(target),
    edge = roadEdges.reduce((best, x) =>
      Math.abs(a[0] - x) + Math.abs(b[0] - x) <
      Math.abs(a[0] - best) + Math.abs(b[0] - best)
        ? x
        : best,
    );
  // Use side lanes between rows so couriers never cross through a house.
  const points = [
      a,
      [a[0], aRoad],
      ...(aRoad === bRoad
        ? []
        : [[edge, aRoad], [edge, bRoad]]),
      [b[0], bRoad],
      b,
    ],
    lengths = points
      .slice(1)
      .map((p, i) => Math.hypot(p[0] - points[i][0], p[1] - points[i][1]));
  let distance =
    Math.min(1, Math.max(0, progress)) * lengths.reduce((a, b) => a + b, 0);
  for (let i = 0; i < lengths.length; i++) {
    if (distance <= lengths[i] || i === lengths.length - 1) {
      const f = lengths[i] ? distance / lengths[i] : 0;
      return {
        x: points[i][0] + (points[i + 1][0] - points[i][0]) * f,
        y: points[i][1] + (points[i + 1][1] - points[i][1]) * f,
        direction: points[i + 1][0] - points[i][0] || b[0] - a[0],
      };
    }
    distance -= lengths[i];
  }
  return { x: b[0], y: b[1], direction: 1 };
}

export function townSummary(town) {
  const tasks = Object.values(town.tasks || {}),
    workers = Object.values(town.workers || {});
  return {
    busy: workers.filter(
      (w) => w.status === "working" || w.status === "pausing",
    ).length,
    blocked: tasks.filter((t) => t.blocked).length,
    failed: workers.filter((w) => w.status === "failed").length,
    queued: tasks.filter(
      (t) => !["closed", "merged", "shipped", "implemented"].includes(t.stage),
    ).length,
    release: town.last_release || "No releases yet",
  };
}

// These projections deliberately read the persisted snapshot only.  They are
// presentation data: commands and scheduling remain owned by the service.
export const taskStatuses = {
  unknown: { label: "Unknown stage", className: "unknown" },
  open: { label: "Open", className: "open" },
  draft: { label: "Draft", className: "draft" },
  working: { label: "Working", className: "working" },
  queued: { label: "Queued", className: "queued" },
  waiting_github: { label: "Waiting on GitHub", className: "waiting-github" },
  ready: { label: "Ready", className: "ready" },
  blocked: { label: "Blocked", className: "blocked" },
  failed: { label: "Failed", className: "failed" },
  inconclusive: { label: "Inconclusive", className: "inconclusive" },
  uncertain_write: { label: "Uncertain write", className: "uncertain-write" },
  unreleased: { label: "Unreleased", className: "unreleased" },
  implemented: { label: "Implemented", className: "implemented" },
  closed: { label: "Closed", className: "closed" },
  merged: { label: "Merged", className: "merged" },
  shipped: { label: "Shipped", className: "shipped" },
};
export const boardColumns = [
  { id: "open", label: "Open" },
  { id: "queued", label: "Queued" },
  { id: "in_progress", label: "In Progress" },
  { id: "blocked", label: "Blocked" },
  { id: "review", label: "Review" },
  { id: "ready", label: "Ready" },
  { id: "shipped", label: "Shipped" },
  { id: "completed", label: "Completed" },
];
export const viewModes = ["town", "board", "compact"];

export function normalizeView(value) {
  return viewModes.includes(value) ? value : "town";
}

export function focusIdentity(target) {
  if (!target) return null;
  const surface = target.closest?.("#towns, #houses, #journal, #board, #compact")?.id || "";
  const dataset = target.dataset || {};
  const key = dataset.town || dataset.house || dataset.task || dataset.cargo || dataset.boardTask || dataset.boardHouse || dataset.compactTask || dataset.compactHouse || "";
  const town = dataset.town || dataset.boardTown || dataset.compactTown || "";
  return surface && key ? { surface, key, town } : null;
}

export function focusMatches(target, identity) {
  if (!identity || !target) return false;
  const dataset = target.dataset || {};
  const key = dataset.town || dataset.house || dataset.task || dataset.cargo || dataset.boardTask || dataset.boardHouse || dataset.compactTask || dataset.compactHouse || "";
  const town = dataset.town || dataset.boardTown || dataset.compactTown || "";
  return key === identity.key && (!identity.town || town === identity.town);
}

export function scheduleLabel(worker, now = Date.now()) {
  if (!worker?.enabled) return "Paused";
  if (worker?.agent) return "After current run";
  const next = Date.parse(worker?.next || "");
  if (!Number.isFinite(next)) return "Waiting for assignment";
  if (next <= now) return "Due now";
  return `Next ${new Date(next).toLocaleTimeString()}`;
}

const terminalStages = new Set([
  "closed",
  "merged",
  "shipped",
  "implemented",
]);
const githubWaitingStages = new Set([
  "awaiting_author",
  "locked",
  "checks",
]);
const activeWorkerStatuses = new Set(["working", "pausing"]);
const activeStages = new Set(["working"]);

function normalized(value) {
  return String(value ?? "").trim().toLowerCase().replaceAll(" ", "_");
}

function intentFor(town, task) {
  if (task?.kind !== "pr") return null;
  const intents = town?.intents || {};
  const number = task?.number;
  if (number === undefined || number === null) return null;
  return intents[number] || intents[String(number)] || null;
}

export function workerIsActive(worker) {
  return activeWorkerStatuses.has(normalized(worker?.status));
}

export function workerProfile(town, role, worker = null) {
  // The captured profile remains authoritative while a dispatch is winding
  // down, even if Control(stop) has already changed the visible status.
  const active = !!worker?.agent;
  // A running worker owns the profile captured at dispatch.  A queued worker
  // uses the current public bot profile and therefore reflects future work.
  const configured = town?.config?.bot_agents?.[role] || town?.config || {};
  const captured = active && worker?.agent ? worker.agent : configured;
  return {
    harness: captured?.harness || "codex-acp",
    harness_version:
      captured?.harness_version || captured?.harnessVersion || "",
    model: captured?.model || "",
    effort: captured?.effort || "",
    inherited:
      captured?.inherited !== undefined
        ? captured.inherited
        : !town?.config?.bot_agents?.[role],
    source: active && worker?.agent ? "active" : "queued",
  };
}

export function taskStatus(town, task) {
  const stage = normalized(task?.stage);
  const intent = intentFor(town, task);
  const intentStatus = normalized(intent?.status);
  // A confirmed terminal observation outranks stale review/write metadata.
  if (terminalStages.has(stage)) return stage;
  // Preserve the strongest observable uncertainty.  A blocked flag remains
  // available on the projection, while the status explains why it is blocked.
  if (
    ["uncertain", "uncertain_write", "uncertain-write"].includes(stage) ||
    ["uncertain", "uncertain_write", "uncertain-write"].includes(intentStatus)
  )
    return "uncertain_write";
  if (
    stage === "inconclusive" ||
    normalized(task?.audit?.verdict) === "inconclusive"
  )
    return "inconclusive";
  if (task?.blocked || stage === "blocked") return "blocked";
  if (stage === "failed") return "failed";
  if (stage === "ready") return "ready";
  if (stage === "draft") return "draft";
  if (stage === "open") return "open";
  if (githubWaitingStages.has(stage)) return "waiting_github";
  if (activeStages.has(stage)) return "working";
  if (stage === "queued" || stage === "fixes") return "queued";
  if (stage === "unreleased") return "unreleased";
  if (Object.hasOwn(taskStatuses, stage)) return stage;
  return "unknown";
}

export function boardColumn(task) {
  const stage = normalized(task?.stage);
  const status = task?.status || stage;
  if (status === "shipped") return "shipped";
  if (["merged", "closed", "implemented"].includes(status)) return "completed";
  if (status === "unreleased") return "ready";
  if (["blocked", "failed", "inconclusive", "uncertain_write"].includes(status)) return "blocked";
  if (status === "unknown") return "open";
  if (status === "ready") return "ready";
  if (["waiting_github", "review"].includes(status) || ["awaiting_author", "checks"].includes(stage)) return "review";
  if (status === "working") return "in_progress";
  if (stage === "queued" || stage === "fixes") return "queued";
  if (["draft", "open"].includes(stage)) return "open";
  return "open";
}

export function projectTask(town, task) {
  const status = taskStatus(town, task);
  const worker = town?.workers?.[task?.house];
  return {
    ...task,
    status,
    statusLabel: taskStatuses[status]?.label || status,
    statusClass: taskStatuses[status]?.className || status,
    blocked: !!task?.blocked,
    workerStatus: normalized(worker?.status) || "paused",
    // A queued task must use the configured profile even while its house has
    // another task running with a captured profile.
    profile: workerProfile(town, task?.house),
    intent: intentFor(town, task),
  };
}

export function projectWorker(town, role, worker = town?.workers?.[role]) {
  const current = worker || { role, status: "paused", enabled: false };
  const profile = workerProfile(town, role, current);
  return {
    ...current,
    role,
    status: normalized(current.status) || "paused",
    active: workerIsActive(current) || !!current.agent,
    profile,
  };
}

export function projectTown(town) {
  const tasks = Object.values(town?.tasks || {})
    .filter(Boolean)
    .map((task) => projectTask(town, task))
    .sort((a, b) => {
      const statusOrder = {
        blocked: 0,
        uncertain_write: 1,
        inconclusive: 2,
        failed: 3,
        unknown: 4,
        working: 5,
        queued: 6,
        waiting_github: 7,
        ready: 8,
        unreleased: 9,
        implemented: 10,
        merged: 11,
        shipped: 12,
        closed: 13,
      };
      return (
        (statusOrder[a.status] ?? 99) - (statusOrder[b.status] ?? 99) ||
        String(a.house || "").localeCompare(String(b.house || "")) ||
        (a.number ?? 0) - (b.number ?? 0) ||
        String(a.id || "").localeCompare(String(b.id || ""))
      );
    });
  const workers = Object.keys(town?.workers || {})
    .map((role) => projectWorker(town, role))
    .sort((a, b) => String(a.role).localeCompare(String(b.role)));
  const counts = Object.fromEntries(
    Object.keys(taskStatuses).map((status) => [
      status,
      tasks.filter((task) => task.status === status).length,
    ]),
  );
  return {
    town,
    workers,
    tasks,
    counts,
    active: workers.filter((worker) => worker.active).length,
    failedWorkers: workers.filter((worker) => worker.status === "failed").length,
    attention: tasks.filter((task) =>
      ["blocked", "failed", "inconclusive", "uncertain_write"].includes(
        task.status,
      ),
    ).length + workers.filter((worker) => worker.status === "failed").length,
  };
}

export function projectState(state, selectedTown = "") {
  const towns = Object.values(state?.towns || {}).map(projectTown);
  const selected = towns.find((item) => item.town?.id === selectedTown);
  return {
    state,
    towns,
    selected,
    active: towns.reduce((sum, town) => sum + town.active, 0),
    attention: towns.reduce((sum, town) => sum + town.attention, 0),
  };
}
