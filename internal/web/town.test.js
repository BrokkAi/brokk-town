import test from "node:test";
import assert from "node:assert/strict";
import {
  positions,
  routePosition,
  visibleEvents,
  queueFor,
  issueJobDetails,
  taskRetryEligible,
  safeURL,
  townSummary,
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
test("task links only open normal GitHub URLs", () => {
  assert.equal(safeURL("https://github.com/acme/a/pull/3"), true);
  for (const url of [
    "javascript:alert(1)",
    "https://evil.test",
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
test("retry is limited to eligible issues while preserving PR recovery", () => {
  assert.equal(taskRetryEligible({ kind: "issue", blocked: true }), false);
  assert.equal(taskRetryEligible({ kind: "pr", blocked: true }), true);
  assert.equal(taskRetryEligible({ kind: "pr", blocked: false }), false);
  assert.deepEqual(issueJobDetails({ kind: "pr" }), []);
  assert.deepEqual(issueJobDetails({ kind: "issue" }), []);
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
  assert.throws(() => visit.execute({ town: "acme/no", house: "review" }));
  assert.throws(() => visit.execute({ town: "acme/a", house: "toString" }));
  assert.deepEqual(calls, ["acme/a", "review"]);
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
