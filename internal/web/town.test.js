import test from "node:test";
import assert from "node:assert/strict";
import {
  positions,
  houseNames,
  houseAuthority,
  houseShortcuts,
  roadSegments,
  routePosition,
  visibleEvents,
  queueFor,
  issueJobDetails,
  taskRetryEligible,
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
  workerControls,
  townControls,
  inbox,
  ago,
  decisionReason,
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
      d: { house: "issue", stage: "complete" },
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
    decisions: 0,
    release: "v1",
  });
});
test("inbox gathers decisions and stuck work from every town, longest wait first", () => {
  const state = {
    towns: {
      "acme/later": {
        id: "acme/later",
        config: { repo: "acme/later" },
        workers: { review: { status: "failed", error: "gh exploded", updated: "2026-09-15T08:00:00Z" } },
        tasks: {
          "pr:9": { id: "pr:9", kind: "pr", number: 9, title: "Newer contributor PR", house: "hall", stage: "awaiting_mayor", mayoral_decision: "pending", external: true, updated: "2026-09-15T09:00:00Z" },
          "pr:4": { id: "pr:4", kind: "pr", number: 4, title: "Merge looked uncertain", house: "release", stage: "ready", updated: "2026-09-14T10:00:00Z" },
          "issue:5": { id: "issue:5", kind: "issue", number: 5, title: "Already admitted", house: "issue", stage: "queued", mayoral_decision: "admitted", updated: "2026-09-15T09:30:00Z" },
        },
        intents: { 4: { status: "uncertain", detail: "Merge response was lost" } },
      },
      "acme/earlier": {
        id: "acme/earlier",
        config: { repo: "acme/earlier" },
        workers: { issue: { status: "working" } },
        tasks: {
          "issue:7": { id: "issue:7", kind: "issue", number: 7, title: "Older proposal", house: "hall", stage: "awaiting_mayor", mayoral_decision: "pending", updated: "2026-09-13T12:00:00Z" },
          "pr:8": { id: "pr:8", kind: "pr", number: 8, title: "Review asked for changes", house: "hall", stage: "awaiting_mayor", mayoral_decision: "pending", external: true, audit: { verdict: "changes_needed", summary: "", findings: [] }, updated: "2026-09-14T12:00:00Z" },
          "issue:1": { id: "issue:1", kind: "issue", number: 1, title: "Stuck repair", house: "issue", stage: "fixes", blocked: true, updated: "2026-09-12T12:00:00Z" },
          "issue:2": { id: "issue:2", kind: "issue", number: 2, title: "Declined earlier", house: "hall", stage: "declined", mayoral_decision: "declined", blocked: true },
        },
      },
    },
  };
  const needs = inbox(state);
  assert.deepEqual(
    needs.decisions.map((item) => [item.town, item.task, item.reason, item.reviewAgain]),
    [
      ["acme/earlier", "issue:7", "proposed inside Town", false],
      ["acme/earlier", "pr:8", "Town review asked for changes", true],
      ["acme/later", "pr:9", "outside arrival", false],
    ],
    "decisions come from every town, oldest first, and skip admitted or declined work",
  );
  assert.deepEqual(
    needs.attention.map((item) => [item.town, item.house, item.task, item.status]),
    [
      ["acme/earlier", "issue", "issue:1", "blocked"],
      ["acme/later", "release", "pr:4", "uncertain_write"],
      ["acme/later", "review", "", "failed"],
    ],
    "stuck tasks and failed workers each point at the house to inspect",
  );
  assert.equal(needs.attention[1].detail, "Merge response was lost");
  assert.equal(needs.attention[2].detail, "gh exploded");
  assert.deepEqual(needs.towns, {
    "acme/later": { decisions: 1, attention: 2 },
    "acme/earlier": { decisions: 2, attention: 1 },
  });
  assert.equal(needs.total, 6);
  assert.deepEqual(inbox(null), { decisions: [], attention: [], towns: {}, total: 0 });
  assert.equal(decisionReason({ external: true, audit: { verdict: "changes_needed" } }), "Town review asked for changes");
  const now = Date.parse("2026-09-15T12:00:00Z");
  assert.equal(ago("2026-09-15T11:59:40Z", now), "just now");
  assert.equal(ago("2026-09-15T11:35:00Z", now), "25m ago");
  assert.equal(ago("2026-09-15T02:00:00Z", now), "10h ago");
  assert.equal(ago("2026-09-10T12:00:00Z", now), "5d ago");
  assert.equal(ago("0001-01-01T00:00:00Z", now), "", "Go's zero time is not an age");
  assert.equal(ago("", now), "");
  assert.deepEqual(
    focusIdentity({ closest: () => ({ id: "inbox-list" }), dataset: { inboxKey: "admit:pr:8", inboxTown: "acme/earlier" } }),
    { surface: "inbox-list", key: "admit:pr:8", town: "acme/earlier" },
    "inbox buttons keep focus across snapshot redraws",
  );
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
test("blocked issue jobs expose attention, attempts, and recovery guidance", () => {
  const blocked = {
    kind: "issue",
    house: "issue",
    number: 7,
    stage: "blocked",
    blocked: true,
    attempts: 2,
    detail: "The feature needs an API contract.",
    issue_job: {
      status: "blocked",
      result_detail: "The feature needs an API contract.",
      last_error: "Agent returned blocked.",
      retry_eligible: true,
      retry_detail: "Clarify the API contract on GitHub, then retry this issue.",
    },
  };
  const t = {
    tasks: {
      pending: { kind: "issue", house: "issue", number: 1, stage: "queued" },
      blocked,
    },
  };
  assert.equal(townSummary(t).blocked, 1);
  assert.deepEqual(queueFor(t, "issue").map((task) => task.number), [7, 1]);
  assert.deepEqual(issueJobDetails(blocked), [
    "Issue-bot: blocked · 2 attempts",
    "Agent returned blocked.",
    "Clarify the API contract on GitHub, then retry this issue.",
  ]);
  assert.equal(taskRetryEligible(blocked), true);

  blocked.issue_job.retry_eligible = false;
  blocked.issue_job.retry_detail = "Wait for the active issue worker to finish.";
  assert.equal(taskRetryEligible(blocked), false);
  assert.ok(issueJobDetails(blocked).includes(blocked.issue_job.retry_detail));

  blocked.blocked = false;
  blocked.stage = "queued";
  blocked.issue_job = { status: "pending", retry_eligible: false };
  blocked.detail = "Waiting for issue-bot.";
  assert.equal(townSummary(t).blocked, 0);
  assert.equal(taskRetryEligible(blocked), false);
  assert.deepEqual(issueJobDetails(blocked), ["Issue-bot: pending · 2 attempts"]);

  blocked.stage = "implemented";
  blocked.issue_job.status = "submitted";
  assert.equal(townSummary(t).queued, 1);
  assert.deepEqual(queueFor(t, "issue").map((task) => task.number), [1]);
});
test("retry preserves funnel and PR recovery while respecting durable issue jobs", () => {
  assert.equal(taskRetryEligible({ kind: "issue", blocked: true }), true);
  assert.equal(taskRetryEligible({ kind: "issue", blocked: true, issue_job: { retry_eligible: false } }), false);
  assert.equal(taskRetryEligible({ kind: "pr", blocked: true }), true);
  assert.equal(taskRetryEligible({ kind: "pr", blocked: false }), false);
  assert.deepEqual(issueJobDetails({ kind: "pr" }), []);
  assert.deepEqual(issueJobDetails({ kind: "issue" }), []);
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
      done: { id: "done", title: "Done", house: "issue", stage: "complete" },
    },
  };
  assert.equal(taskStatus(town, town.tasks.blocked), "blocked");
  assert.equal(taskStatus(town, town.tasks.failed), "failed");
  assert.equal(taskStatus(town, town.tasks.inconclusive), "inconclusive");
  assert.equal(taskStatus(town, town.tasks.uncertain), "uncertain_write");
  assert.equal(taskStatus(town, town.tasks.github), "waiting_github");
  assert.equal(taskStatus(town, town.tasks.work), "queued");
  assert.equal(taskStatus(town, town.tasks.done), "complete");
  assert.equal(queueFor(town, "issue").some((task) => task.id === "done"), false);
  assert.equal(workerProfile(town, "issue", town.workers.issue).model, "active-model");
  assert.equal(workerProfile(town, "review", town.workers.review).model, "queued-model");
  const projection = projectTown(town);
  assert.equal(projection.attention, 4);
  assert.deepEqual(
    projection.tasks.map((task) => task.status),
    ["blocked", "uncertain_write", "inconclusive", "failed", "queued", "waiting_github", "complete"],
  );
  assert.equal(projectState({ towns: { [town.id]: town } }, town.id).selected.town, town);
  assert.equal(projectTask(town, town.tasks.github).statusLabel, "Waiting on GitHub");
  assert.equal(boardColumn(projectTask(town, { ...town.tasks.github, stage: "shipped" })), "shipped");
  assert.equal(boardColumn(projectTask(town, { ...town.tasks.github, stage: "merged" })), "completed");
  assert.equal(boardColumn(projectTask(town, town.tasks.done)), "completed");
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
test("worker controls only offer actions that change the worker's state", () => {
  assert.deepEqual(workerControls({ enabled: true, status: "working" }), { start: false, pause: true, stop: true });
  assert.deepEqual(workerControls({ enabled: true, status: "waiting", agent: { model: "m" } }), { start: false, pause: true, stop: true });
  assert.deepEqual(workerControls({ enabled: true, status: "pausing" }), { start: false, pause: false, stop: true });
  assert.deepEqual(workerControls({ enabled: true, status: "waiting" }), { start: true, pause: true, stop: true });
  assert.deepEqual(workerControls({ enabled: true, status: "failed" }), { start: true, pause: true, stop: true });
  assert.deepEqual(workerControls({ enabled: false, status: "paused" }), { start: true, pause: false, stop: false });
  assert.deepEqual(workerControls(undefined), { start: true, pause: false, stop: false });
  assert.deepEqual(workerControls({ enabled: false, status: "paused" }, true), { start: false, pause: false, stop: false });
  assert.match(houseAuthority("release", "manual"), /release-preparation pull requests.*Paused/);
  assert.match(houseAuthority("repo"), /^Read-only:/);
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

test("townControls reports the town's wake state and offers the matching action", () => {
  const agents = (enabled) => Object.fromEntries(
    ["bug", "feature", "issue", "review", "release"].map((role, i) => [role, { role, enabled: enabled[i] }]),
  );
  const paused = townControls({ workers: { ...agents([false, false, false, false, false]), repo: { role: "repo", enabled: true } } });
  assert.equal(paused.status, "Paused", "repo-bot being enabled does not count as awake");
  assert.deepEqual(paused.primary, { action: "start", label: "▶ Wake the town (5)" });
  assert.match(paused.detail, /Bug Bot.*Feature Bot.*Issue Bot.*Review Bot.*Release Bot/);
  assert.equal(paused.secondary, null);
  const awake = townControls({ workers: agents([true, true, true, true, true]) });
  assert.equal(awake.status, "Awake · 5 agents");
  assert.deepEqual(awake.primary, { action: "pause", label: "Ⅱ Pause the town" });
  assert.equal(awake.secondary, null);
  const partial = townControls({ workers: agents([true, false, true, false, false]) });
  assert.equal(partial.status, "Partly awake · 2 of 5");
  assert.deepEqual(partial.primary, { action: "start", label: "▶ Wake the rest" });
  assert.deepEqual(partial.secondary, { action: "pause", label: "Ⅱ Pause all" });
  const manual = townControls({
    config: { merge_policy: "manual" },
    workers: { ...agents([false, false, false, false, false]), repo: { role: "repo", enabled: true } },
  });
  assert.deepEqual(manual.primary, { action: "start", label: "▶ Wake the town (4)" });
  assert.match(manual.detail, /Release Bot stays paused while every merge is manual/);
  assert.equal(townControls(null).status, "Paused");
});
