export const positions = {
  bug: [118, 172],
  simplifier: [354, 172],
  issue: [590, 172],
  review: [826, 172],
  hall: [118, 446],
  repo: [354, 446],
  release: [590, 446],
  feature: [826, 446],
  outside: [-50, 330],
};
export const houseNames = {
  bug: "BUG BOT",
  simplifier: "SIMPLIFIER BOT",
  feature: "FEATURE BOT",
  issue: "ISSUE BOT",
  review: "REVIEW BOT",
  release: "RELEASE BOT",
  repo: "REPO BOT",
  hall: "TOWN HALL",
};
export const houseAuthorities = {
  bug: "May inspect repository content and file GitHub bug issues.",
  simplifier: "May inspect repository content, file simplification issues, and in auto mode recommend that Town decline or close low-value complex issues.",
  feature: "May inspect repository content and propose or file GitHub feature issues.",
  issue: "May claim issues, create pull requests, and push repairs to Town-owned branches.",
  review: "May post pull request reviews and findings and merge eligible pull requests when merge policy permits; it does not edit contributor branches.",
  release: "May create and merge release-preparation pull requests and publish releases and packages.",
  repo: "Inventories repository state, and repairs the branch it covers when its checks fail.",
};

export function houseAuthority(role, mergePolicy = "bot") {
  if (role === "release" && mergePolicy === "manual")
    return `${houseAuthorities.release} Paused while every merge is manual.`;
  return houseAuthorities[role] || "";
}
// Preserve established shortcuts and append the new study on key 7.
export const houseShortcuts = [
  "bug",
  "issue",
  "review",
  "release",
  "repo",
  "hall",
  "feature",
  "simplifier",
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
  simplifying: { label: "Awaiting Simplifier", className: "queued" },
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
  { id: "simplifier", label: "Simplifier queue" },
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
// Start re-enables a stopped worker and lets the operator retry a failed one
// before its backoff expires. An enabled worker that is working, pausing, or
// has an active agent is already running; waiting without an agent means its
// poll interval is scheduled. Pause and Stop apply to an enabled worker, and
// Pause is redundant once a pause is already in flight.
export function workerControls(worker, blocked = false) {
  const status = normalized(worker?.status);
  const enabled = !!worker?.enabled;
  const active = activeWorkerStatuses.has(status) || !!worker?.agent;
  return {
    start: !blocked && (!enabled || status === "failed"),
    pause: enabled && status !== "pausing",
    stop: enabled || active,
  };
}

// Town-wide wake state for the header controls. Repo-bot is excluded: it is
// always enabled and never an agent, so it says nothing about whether the
// operator has authorized real work.
export function townControls(town) {
  const manual = town?.config?.merge_policy === "manual";
  const agents = Object.values(town?.workers || {}).filter(
    (w) => w.role !== "repo" && !(manual && w.role === "release"),
  );
  const awake = agents.filter((w) => w.enabled).length;
  const names = manual
    ? "Bug Bot, Feature Bot, Issue Bot, Review Bot, and Simplifier Bot; Release Bot stays paused while every merge is manual"
    : "Bug Bot, Feature Bot, Issue Bot, Review Bot, Simplifier Bot, and Release Bot";
  if (!awake)
    return { status: "Paused", statusClass: "paused", primary: { action: "start", label: `▶ Wake the town (${agents.length})` }, secondary: null, detail: `Starts ${names}. Repo Bot already watches the repository.` };
  if (awake === agents.length)
    return { status: `Awake · ${awake} agent${awake === 1 ? "" : "s"}`, statusClass: "awake", primary: { action: "pause", label: "Ⅱ Pause the town" }, secondary: null, detail: "" };
  return {
    status: `Partly awake · ${awake} of ${agents.length}`,
    statusClass: "partial",
    primary: { action: "start", label: "▶ Wake the rest" },
    secondary: { action: "pause", label: "Ⅱ Pause all" },
    detail: `Starts the remaining available workers: ${names}. Repo Bot already watches the repository.`,
  };
}

// branchHealthNote is what the watchtower reports about the branch it covers.
// A branch nothing is wrong with says so in one line; a failing one names the
// checks, and a repair names the commit that was published.
export function branchHealthNote(health) {
  if (!health || !health.state) return "";
  const failing = (health.failing || []).join(", ");
  const detail = health.detail ? ` ${health.detail}` : "";
  switch (health.state) {
    case "red":
      return `Branch checks are failing: ${failing}.${detail}`;
    case "repaired":
      return `Published a repair for ${failing} as ${(health.pushed || "").slice(0, 8)}.`;
    case "unrepairable":
      return `Branch checks are failing and Repo Bot cannot repair them.${detail}`;
    case "pending":
      return "Branch checks are still running.";
    case "unreported":
      return "The branch reports no checks.";
    default:
      return "Branch checks are passing.";
  }
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

// A profile only reads at a glance when it is short, so the registry decoration
// (vendor prefix, -acp suffix, provider path) is dropped and the part an
// operator actually chose is kept.
const harnessNames = {
  "codex-acp": "codex",
  "claude-acp": "claude",
  "brokkai/anvil": "anvil",
  "brokkai/muse-acp": "muse",
  "foundev/draupnir": "draupnir",
};
export function harnessLabel(harness) {
  const id = String(harness ?? "").trim().toLowerCase();
  if (!id) return "codex";
  const trimmed = id.replace(/\/+$/, "");
  return (
    harnessNames[id] || trimmed.split("/").at(-1).replace(/-acp$/, "") || id
  );
}
export function modelLabel(model) {
  const id = String(model ?? "").trim();
  return (id && id.split("/").at(-1)) || "default";
}
export function effortLabel(effort) {
  const id = String(effort ?? "").trim();
  return id ? id.replaceAll("_", " ") : "default";
}
// Effort names differ per harness, so unrecognized values keep their own shade
// rather than being forced onto a scale the harness never advertised.
const effortRanks = {
  minimal: "low",
  low: "low",
  medium: "medium",
  standard: "medium",
  high: "high",
  xhigh: "max",
  "x-high": "max",
  max: "max",
};
export function effortRank(effort) {
  const id = String(effort ?? "").trim().toLowerCase().replaceAll("_", "-");
  if (!id) return "default";
  return effortRanks[id] || "custom";
}

// One description of a dispatch profile for every surface: the village label,
// the operations views, the inspector and the tooltip all say the same thing.
export function profileSummary(profile) {
  const harness = harnessLabel(profile?.harness);
  const model = modelLabel(profile?.model);
  const effort = effortLabel(profile?.effort);
  const inherited = profile?.inherited !== false;
  const live = profile?.source === "active";
  const version = profile?.harness_version || "";
  return {
    harness,
    model,
    effort,
    rank: effortRank(profile?.effort),
    inherited,
    live,
    version,
    text: `${harness} · ${model} · ${effort}`,
    title: [
      `${live ? "Running now" : "Next run"}: ${profile?.harness || "codex-acp"}${version ? ` ${version}` : ""}`,
      `Model: ${profile?.model || "harness default"}`,
      `Effort: ${profile?.effort || "harness default"}`,
      inherited
        ? "Inherited from this town's defaults"
        : "Set for this house only",
    ].join("\n"),
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
  const worker = town?.workers?.[task?.house];
  const run = worker?.run;
  if ((workerIsActive(worker) || worker?.agent) && run &&
      ((task?.kind === "issue" && task.number > 0 && run.issue === task.number) ||
       (task?.kind === "pr" && task.number > 0 && run.pr === task.number)))
    return "working";
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
  if (status === "simplifying") return "simplifier";
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
    profile: workerProfile(town, task?.house, status === "working" ? worker : null),
    intent: intentFor(town, task),
  };
}

// Active, waiting and blocked count assigned work items.
// Only an exact persisted run target moves a queued item into active work.
export function houseWorkload(town, role) {
  const tasks = queueFor(town || {}, role);
  const statuses = tasks.map((task) => taskStatus(town, task));
  const blocked = statuses.filter((status) => attentionStatuses.includes(status)).length;
  const working = statuses.filter((status) => status === "working").length;
  return {
    active: working,
    waiting: tasks.length - blocked - working,
    blocked,
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

// The overview answers "is this town running one profile or several?" before
// anyone visits it.  Repo-bot has no agent, so it never counts as a profile.
export function profileSpread(workers) {
  const agents = workers.filter((worker) => worker.role !== "repo" && worker.role !== "hall");
  const distinct = [];
  for (const worker of agents) {
    const summary = profileSummary(worker.profile);
    if (!distinct.some((other) => other.text === summary.text)) distinct.push(summary);
  }
  return {
    distinct,
    overrides: agents.filter((worker) => worker.profile?.inherited === false).length,
    label:
      distinct.length === 0
        ? ""
        : distinct.length === 1
          ? distinct[0].text
          : `${distinct.length} profiles`,
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
        simplifying: 6,
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
    profiles: profileSpread(workers),
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
