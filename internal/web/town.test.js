import test from "node:test";
import assert from "node:assert/strict";
import {
  positions,
  houseNames,
  houseShortcuts,
  roadSegments,
  routePosition,
  visibleEvents,
  queueFor,
  safeURL,
  townSummary,
  taskStatus,
  projectTask,
  projectTown,
  projectState,
  workerProfile,
  boardColumn,
  normalizeView,
  focusIdentity,
  focusMatches,
  scheduleLabel,
} from "./town.js";
import { registerTownTools } from "./tools.js";
test("deliveries resume after cursor and stay in their repository", () => {
  const events = [
    { seq: 1, town: "a" },
    { seq: 2, town: "b" },
    { seq: 3, town: "a" },
  ];
  assert.deepEqual(visibleEvents(events, 1, "a"), [events[2]]);
  assert.deepEqual(visibleEvents(events, 3, "a"), []);
});
test("all delivery routes have finite continuous endpoints", () => {
  for (const [from, to] of [
    ["bug", "issue"],
    ["feature", "issue"],
    ["feature", "hall"],
    ["outside", "feature"],
    ["issue", "review"],
    ["review", "issue"],
    ["review", "release"],
    ["release", "outside"],
    ["outside", "issue"],
    ["outside", "review"],
  ]) {
    const a = routePosition(from, to, 0),
      b = routePosition(from, to, 1);
    assert.equal(a.x, positions[from][0]);
    assert.equal(b.x, to === "outside" ? 1170 : positions[to][0]);
    let prev = a;
    for (let i = 1; i <= 100; i++) {
      const p = routePosition(from, to, i / 100);
      assert.ok(Number.isFinite(p.x) && Number.isFinite(p.y));
      assert.ok(Math.hypot(p.x - prev.x, p.y - prev.y) < 40);
      prev = p;
    }
    assert.deepEqual(routePosition(from, to, -1), a);
    assert.deepEqual(routePosition(from, to, 2), b);
  }
});
test("all seven houses fit and feature deliveries follow the painted roads", () => {
  assert.equal(Object.keys(houseNames).length, 7);
  assert.equal(houseNames.feature, "FEATURE BOT");
  assert.equal(houseShortcuts[5], "hall");
  assert.equal(houseShortcuts[6], "feature");
  const houses = Object.keys(houseNames);
  for (const role of houses) {
    const [x, y] = positions[role];
    assert.ok(x >= 118 && x <= 1002 && y >= 118 && y <= 562);
    for (const other of houses.filter((other) => other !== role)) {
      const [ox, oy] = positions[other];
      assert.ok(Math.abs(x - ox) >= 236 || Math.abs(y - oy) >= 236);
    }
  }
  for (const from of [...houses, "outside"]) {
    for (const to of [...houses, "outside"]) {
      for (let i = 0; i <= 100; i++) {
        const { x, y } = routePosition(from, to, i / 100);
        assert.ok(
          roadSegments.some(([a, b]) =>
            x >= Math.min(a[0], b[0]) - 1e-6 &&
            x <= Math.max(a[0], b[0]) + 1e-6 &&
            y >= Math.min(a[1], b[1]) - 1e-6 &&
            y <= Math.max(a[1], b[1]) + 1e-6,
          ),
          `${from} → ${to} left the road at ${x}, ${y}`,
        );
      }
    }
  }
});
test("queue and overview report blocked and waiting work", () => {
  const t = {
    tasks: {
      a: { house: "issue", stage: "fixes", blocked: true, number: 2 },
      b: { house: "issue", stage: "queued", number: 1 },
      c: { house: "release", stage: "shipped" },
    },
    workers: { bug: { status: "working" }, review: { status: "failed" } },
    last_release: "v1",
  };
  assert.deepEqual(
    queueFor(t, "issue").map((t) => t.number),
    [2, 1],
  );
  assert.deepEqual(townSummary(t), {
    busy: 1,
    blocked: 1,
    failed: 1,
    queued: 2,
    release: "v1",
  });
});
test("task links only open normal provider HTTPS URLs", () => {
  assert.equal(safeURL("https://github.com/acme/a/pull/3"), true);
  assert.equal(safeURL("https://app.slack.com/client/T1/C1/thread-2"), true);
  assert.equal(safeURL("https://linear.app/acme/issue/ABC-1"), true);
  for (const url of [
    "javascript:alert(1)",
    "https://github.com@evil.test",
    "https://evil@github.com",
    "https://github.com:444/a",
    "http://github.com",
  ])
    assert.equal(safeURL(url), false);
});
test("operations projection preserves distinct persisted task outcomes", () => {
  const town = {
    id: "acme/project",
    config: {
      repo: "acme/project",
      harness: "codex-acp",
      model: "queued-model",
      effort: "medium",
      bot_agents: {
        issue: { harness: "claude", model: "issue-model", effort: "high", inherited: false },
      },
    },
    workers: {
      issue: { role: "issue", status: "working", agent: { harness: "codex", harness_version: "9", model: "active-model", effort: "xhigh", inherited: false } },
      review: { role: "review", status: "waiting" },
    },
    intents: { 5: { status: "uncertain" } },
    tasks: {
      blocked: { id: "blocked", title: "Blocked", house: "issue", stage: "fixes", blocked: true },
      failed: { id: "failed", title: "Failed", house: "issue", stage: "failed" },
      inconclusive: { id: "inconclusive", title: "Inconclusive", house: "review", stage: "inconclusive" },
      uncertain: { id: "uncertain", kind: "pr", title: "Uncertain", house: "issue", stage: "queued", number: 5 },
      github: { id: "github", title: "GitHub", house: "review", stage: "awaiting_author" },
      work: { id: "work", title: "Working", house: "issue", stage: "fixes" },
    },
  };
  assert.equal(taskStatus(town, town.tasks.blocked), "blocked");
  assert.equal(taskStatus(town, town.tasks.failed), "failed");
  assert.equal(taskStatus(town, town.tasks.inconclusive), "inconclusive");
  assert.equal(taskStatus(town, town.tasks.uncertain), "uncertain_write");
  assert.equal(taskStatus(town, town.tasks.github), "waiting_github");
  assert.equal(taskStatus(town, town.tasks.work), "queued");
  assert.equal(workerProfile(town, "issue", town.workers.issue).model, "active-model");
  assert.equal(workerProfile(town, "review", town.workers.review).model, "queued-model");
  const projection = projectTown(town);
  assert.equal(projection.attention, 4);
  assert.deepEqual(
    projection.tasks.map((task) => task.status),
    ["blocked", "uncertain_write", "inconclusive", "failed", "queued", "waiting_github"],
  );
  assert.equal(projectState({ towns: { [town.id]: town } }, town.id).selected.town, town);
  assert.equal(projectTask(town, town.tasks.github).statusLabel, "Waiting on GitHub");
  assert.equal(boardColumn(projectTask(town, { ...town.tasks.github, stage: "shipped" })), "shipped");
  assert.equal(boardColumn(projectTask(town, { ...town.tasks.github, stage: "merged" })), "completed");
  assert.equal(boardColumn(projectTask(town, { ...town.tasks.github, stage: "mystery" })), "open");
});
test("view and focus projections tolerate reconnect redraws", () => {
  assert.equal(normalizeView("board"), "board");
  assert.equal(normalizeView("broken"), "town");
  const boardButton = {
    dataset: { boardTown: "acme/project", boardTask: "pr:1" },
    closest: () => ({ id: "board" }),
  };
  const identity = focusIdentity(boardButton);
  assert.deepEqual(identity, { surface: "board", key: "pr:1", town: "acme/project" });
  assert.equal(focusMatches({ dataset: { boardTown: "acme/project", boardTask: "pr:1" } }, identity), true);
  assert.equal(focusMatches({ dataset: { boardTown: "acme/other", boardTask: "pr:1" } }, identity), false);
});
test("schedule labels distinguish disabled, active, due, and future workers", () => {
  assert.equal(scheduleLabel({ enabled: false, next: "0001-01-01T00:00:00Z" }), "Paused");
  assert.equal(scheduleLabel({ enabled: true, agent: { model: "m" } }), "After current run");
  assert.equal(scheduleLabel({ enabled: true, next: "0001-01-01T00:00:00Z" }), "Due now");
  assert.match(scheduleLabel({ enabled: true, next: "2999-01-01T00:00:00Z" }), /^Next/);
});
test("optional tools validate input and use the visible navigation", async () => {
  const registered = new Map(),
    calls = [];
  const state = {
    demo: true,
    towns: { "acme/a": { id: "acme/a", tasks: {}, workers: {} } },
  };
  const cleanup = registerTownTools(
    {
      registerTool(tool, options) {
        registered.set(tool.name, { ...tool, options });
      },
    },
    () => state,
    (id) => calls.push(id),
    (house) => calls.push(house),
  );
  assert.equal(registered.size, 2);
  const list = registered.get("list_towns");
  assert.equal(list.annotations.readOnlyHint, true);
  assert.equal(list.execute({}).towns[0].id, "acme/a");
  assert.throws(() => list.execute({ extra: 1 }));
  const visit = registered.get("visit_town_house");
  assert.deepEqual(visit.execute({ town: "acme/a", house: "review" }), {
    town: "acme/a",
    house: "review",
  });
  assert.deepEqual(calls, ["acme/a", "review"]);
  assert.ok(visit.inputSchema.properties.house.enum.includes("feature"));
  assert.deepEqual(visit.execute({ town: "acme/a", house: "feature" }), {
    town: "acme/a",
    house: "feature",
  });
  assert.throws(() => visit.execute({ town: "acme/no", house: "review" }));
  assert.throws(() => visit.execute({ town: "acme/a", house: "toString" }));
  assert.deepEqual(calls, ["acme/a", "review", "acme/a", "feature"]);
  cleanup();
  assert.equal(list.options.signal.aborted, true);
  assert.doesNotThrow(() =>
    registerTownTools(
      undefined,
      () => state,
      () => {},
      () => {},
    ),
  );
});
