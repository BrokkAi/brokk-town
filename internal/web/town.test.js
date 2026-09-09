import test from "node:test";
import assert from "node:assert/strict";
import {
  positions,
  routePosition,
  visibleEvents,
  queueFor,
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
