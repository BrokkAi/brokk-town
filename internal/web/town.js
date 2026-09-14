export const positions = {
  bug: [180, 172],
  issue: [555, 172],
  review: [930, 172],
  hall: [180, 446],
  repo: [555, 446],
  release: [930, 446],
  outside: [-50, 330],
};
export const houseNames = {
  bug: "BUG BOT",
  issue: "ISSUE BOT",
  review: "REVIEW BOT",
  release: "RELEASE BOT",
  repo: "REPO BOT",
  hall: "TOWN HALL",
};
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
    (task.kind !== "issue" || task.issue_job?.retry_eligible)
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
  const a = positions[from] || positions.outside,
    b = to === "outside" ? [1170, 550] : positions[to] || positions.hall,
    road = from === "release" || to === "release" ? 540 : 330;
  const points = [
      [a[0], a[1] + 80],
      [a[0], road],
      [b[0], road],
      [b[0], b[1] + 80],
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
        direction: b[0] - a[0],
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
