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
