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
        !["complete", "closed", "merged", "shipped", "implemented", "declined"].includes(t.stage),
    )
    .sort(
      (a, b) =>
        Number(!!b.blocked) - Number(!!a.blocked) ||
        (a.number ?? 0) - (b.number ?? 0),
    );
}
export function issueJobDetails(task) {
  const job = task.issue_job;
  if (task.kind !== "issue" || !job) return [];
  const details = [
    `Issue-bot: ${job.status.replaceAll("_", " ")} · ${task.attempts || 0} attempts`,
  ];
  for (const detail of [job.last_error, job.result_detail, job.retry_detail]) {
    if (detail && detail !== task.detail && !details.includes(detail))
      details.push(detail);
  }
  return details;
}
export function taskRetryEligible(task) {
  return !!(
    task.blocked &&
    (task.kind !== "issue" || !task.issue_job || task.issue_job.retry_eligible)
  );
}
export function visibleEvents(events, after, town) {
  return (events || []).filter((e) => e.seq > after && e.town === town);
}

export function outcomeReport(records = [], since = new Date(0)) {
  const selected = records
    .filter((record) => new Date(record.at) >= since)
    .sort((a, b) => new Date(a.at) - new Date(b.at) || String(a.id).localeCompare(String(b.id)));
  const summary = {
    attempts: 0, findings: 0, submitted: 0, merged: 0, repairs: 0,
    blocked: 0, releases: 0, useful: 0, falsePositives: 0, unjudged: 0,
  };
  for (const record of selected) {
    if (record.kind === "worker_attempt") summary.attempts++;
    if (record.kind === "finding_filed") {
      summary.findings++;
      if (!record.judgment) summary.unjudged++;
      else if (record.judgment.value === "useful") summary.useful++;
      else if (record.judgment.value === "false_positive") summary.falsePositives++;
    }
    if (record.kind === "implementation_pr") summary.submitted++;
    if (record.kind === "merge") summary.merged++;
    if (record.kind === "repair_round") summary.repairs++;
    if (record.kind === "blocked" || record.kind === "abandoned") summary.blocked++;
    if (record.kind === "release") summary.releases++;
  }
  return { records: selected, summary };
}
export function safeURL(value) {
  try {
    const u = new URL(value);
    return (
      u.protocol === "https:" &&
      Boolean(u.hostname) &&
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
      (t) => !["complete", "closed", "merged", "shipped", "implemented", "declined"].includes(t.stage),
    ).length,
    decisions: tasks.filter((t) => t.mayoral_decision === "pending").length,
    release: town.last_release || "No releases yet",
  };
}

// Why a task waits at Town Hall, in the Mayor's words.  Shared by the
// inspector and the cross-town inbox so both explain an arrival the same way.
export function decisionReason(task) {
  if (task?.audit?.verdict === "changes_needed") return "Town review asked for changes";
  if (task?.external) return "outside arrival";
  return "proposed inside Town";
}

// Task statuses that stop work until an operator looks.  The board's Blocked
// column, the operations attention count, and the inbox all use this one list.
export const attentionStatuses = ["blocked", "failed", "inconclusive", "uncertain_write"];

// Everything across every town that waits on a person: pending Mayoral
// decisions and stuck work.  Oldest first, so the longest wait surfaces on top.
// Each item names the town and house to open so the caller can navigate
// straight to the place where the decision or retry lives.
export function inbox(state) {
  const decisions = [],
    attention = [],
    towns = {};
  for (const town of Object.values(state?.towns || {})) {
    if (!town?.id) continue;
    const repo = town.config?.repo || town.id;
    const counts = { decisions: 0, attention: 0 };
    towns[town.id] = counts;
    const base = (task) => ({
      town: town.id,
      repo,
      task: task.id,
      house: task.house || "hall",
      title: task.title || task.id,
      kind: task.kind || "",
      number: task.number || 0,
      updated: task.updated || "",
    });
    for (const task of Object.values(town.tasks || {})) {
      if (!task) continue;
      if (task.mayoral_decision === "pending") {
        counts.decisions++;
        decisions.push({
          ...base(task),
          external: !!task.external,
          reason: decisionReason(task),
          reviewAgain: task.audit?.verdict === "changes_needed",
        });
        continue;
      }
      const projected = projectTask(town, task);
      if (!attentionStatuses.includes(projected.status)) continue;
      counts.attention++;
      attention.push({
        ...base(task),
        status: projected.status,
        statusLabel: projected.statusLabel,
        statusClass: projected.statusClass,
        detail: task.detail || projected.intent?.detail || "",
      });
    }
    for (const [role, worker] of Object.entries(town.workers || {})) {
      if (normalized(worker?.status) !== "failed") continue;
      counts.attention++;
      attention.push({
        town: town.id,
        repo,
        task: "",
        house: role,
        title: `${houseNames[role] || role} failed`,
        kind: "worker",
        number: 0,
        updated: worker.updated || "",
        status: "failed",
        statusLabel: taskStatuses.failed.label,
        statusClass: taskStatuses.failed.className,
        detail: worker.error || "",
      });
    }
  }
  const oldestFirst = (a, b) =>
    (Date.parse(a.updated) || 0) - (Date.parse(b.updated) || 0) ||
    a.repo.localeCompare(b.repo) ||
    a.title.localeCompare(b.title);
  decisions.sort(oldestFirst);
  attention.sort(oldestFirst);
  return { decisions, attention, towns, total: decisions.length + attention.length };
}

// Short relative age for inbox rows.  Go's zero time and unparseable values
// render as nothing rather than as an absurd number of days.
export function ago(value, now = Date.now()) {
  const at = Date.parse(value || "");
  if (!Number.isFinite(at) || at < Date.UTC(2000, 0, 1)) return "";
  const seconds = Math.max(0, Math.round((now - at) / 1000));
  if (seconds < 60) return "just now";
  const minutes = Math.round(seconds / 60);
  if (minutes < 60) return `${minutes}m ago`;
  const hours = Math.round(minutes / 60);
  if (hours < 48) return `${hours}h ago`;
  return `${Math.round(hours / 24)}d ago`;
}

// These projections deliberately read the persisted snapshot only.  They are
// presentation data: commands and scheduling remain owned by the service.
export const taskStatuses = {
  unknown: { label: "Unknown stage", className: "unknown" },
  open: { label: "Open", className: "open" },
  draft: { label: "Draft", className: "draft" },
  working: { label: "Working", className: "working" },
  queued: { label: "Queued", className: "queued" },
  awaiting_mayor: { label: "Mayoral decision", className: "waiting-github" },
  declined: { label: "Declined by Mayor", className: "closed" },
  delayed: { label: "Delayed by Mayor", className: "waiting-github" },
  waiting_github: { label: "Waiting on GitHub", className: "waiting-github" },
  ready: { label: "Ready", className: "ready" },
  blocked: { label: "Blocked", className: "blocked" },
  failed: { label: "Failed", className: "failed" },
  inconclusive: { label: "Inconclusive", className: "inconclusive" },
  uncertain_write: { label: "Uncertain write", className: "uncertain-write" },
  unreleased: { label: "Unreleased", className: "unreleased" },
  implemented: { label: "Implemented", className: "implemented" },
  complete: { label: "Done", className: "complete" },
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
  const surface = target.closest?.("#towns, #houses, #journal, #board, #compact, #inbox-list")?.id || "";
  const dataset = target.dataset || {};
  const key = dataset.town || dataset.house || dataset.task || dataset.cargo || dataset.boardTask || dataset.boardHouse || dataset.compactTask || dataset.compactHouse || dataset.inboxKey || "";
  const town = dataset.town || dataset.boardTown || dataset.compactTown || dataset.inboxTown || "";
  return surface && key ? { surface, key, town } : null;
}

export function focusMatches(target, identity) {
  if (!identity || !target) return false;
  const dataset = target.dataset || {};
  const key = dataset.town || dataset.house || dataset.task || dataset.cargo || dataset.boardTask || dataset.boardHouse || dataset.compactTask || dataset.compactHouse || dataset.inboxKey || "";
  const town = dataset.town || dataset.boardTown || dataset.compactTown || dataset.inboxTown || "";
  return key === identity.key && (!identity.town || town === identity.town);
}

// Which operator controls make sense for a worker in its current state.
// Start re-enables a paused or failed worker (or runs a waiting one now);
// Pause and Stop only apply to an enabled worker; Pause is redundant once a
// pause is already in flight.
export function workerControls(worker) {
  const status = normalized(worker?.status);
  const enabled = !!worker?.enabled;
  const active = activeWorkerStatuses.has(status) || !!worker?.agent;
  return {
    start: !enabled || !active,
    pause: enabled && status !== "pausing",
    stop: enabled || active,
  };
}

// Town-wide wake state for the header controls. Repo-bot is excluded: it is
// always enabled and never an agent, so it says nothing about whether the
// operator has authorized real work.
export function townControls(town) {
  const agents = Object.values(town?.workers || {}).filter((w) => w.role !== "repo");
  const awake = agents.filter((w) => w.enabled).length;
  if (!awake)
    return { status: "Paused", statusClass: "paused", primary: { action: "start", label: "▶ Wake the town" }, secondary: null };
  if (awake === agents.length)
    return { status: `Awake · ${awake} agent${awake === 1 ? "" : "s"}`, statusClass: "awake", primary: { action: "pause", label: "Ⅱ Pause the town" }, secondary: null };
  return {
    status: `Partly awake · ${awake} of ${agents.length}`,
    statusClass: "partial",
    primary: { action: "start", label: "▶ Wake the rest" },
    secondary: { action: "pause", label: "Ⅱ Pause all" },
  };
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
  "complete",
  "closed",
  "merged",
  "shipped",
  "implemented",
  "declined",
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
  if (["complete", "merged", "closed", "implemented", "declined"].includes(status)) return "completed";
  if (status === "unreleased") return "ready";
  if (attentionStatuses.includes(status)) return "blocked";
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
    attention: tasks.filter((task) => attentionStatuses.includes(task.status)).length +
      workers.filter((worker) => worker.status === "failed").length,
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
