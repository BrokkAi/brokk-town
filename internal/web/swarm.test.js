import test from "node:test";
import assert from "node:assert/strict";
import {
  MAX_PACK,
  MAX_BURROWS,
  STRIKE,
  raidKind,
  packSize,
  raidPhase,
  packLayout,
  creaturePosition,
  deliveryPoint,
  burrowLayout,
  strike,
  lift,
  LIFT,
} from "./swarm.js";
import { skins, skinIds, skinHooks, normalizeSkin, nextSkin } from "./skins.js";
import { houseDoor, routePosition, roadLevels } from "./town.js";

test("every skin exposes the same hooks and the village stays the default", () => {
  for (const id of skinIds) {
    for (const hook of skinHooks)
      assert.equal(typeof skins[id][hook], "function", `${id}.${hook}`);
    assert.ok(skins[id].duration > 0);
    assert.equal(skins[id].id, id);
  }
  assert.equal(normalizeSkin(undefined), "village");
  assert.equal(normalizeSkin("swarm"), "swarm");
  assert.equal(normalizeSkin("starcraft"), "village");
  assert.equal(nextSkin("village"), "swarm");
  assert.equal(nextSkin("swarm"), "village");
});

test("raids read their kind and size from the committed delivery alone", () => {
  assert.equal(raidKind({ from: "hall", to: "issue" }), "airdrop");
  assert.equal(raidKind({ from: "release", to: "outside" }), "sortie");
  assert.equal(raidKind({ from: "outside", to: "hall" }), "incursion");
  assert.equal(raidKind({ from: "issue", to: "review" }), "raid");
  assert.equal(packSize({ from: "hall", to: "issue", cargo: "issue:1" }), 0, "orders are not a swarm");
  const issue = packSize({ from: "bug", to: "issue", cargo: "issue:1" }),
    pr = packSize({ from: "issue", to: "review", cargo: "pr:1" }),
    external = packSize({ from: "outside", to: "review", cargo: "pr:1" });
  assert.ok(issue < pr && pr < external, "bigger work brings a bigger pack");
  assert.ok(external <= MAX_PACK);
  assert.ok(packSize({ from: "release", to: "outside", cargo: "release:v1" }) > packSize({ from: "hall", to: "outside", cargo: "issue:3" }));
  const m = { from: "issue", to: "review", cargo: "pr:9" };
  assert.equal(packLayout(m).length, packSize(m));
  assert.deepEqual(packLayout({ ...m }), packLayout({ ...m }), "the same cargo always brings the same pack");
  assert.notDeepEqual(packLayout({ ...m, cargo: "pr:10" }), packLayout(m));
});

test("a raid approaches on the road, strikes the gate, then withdraws and fades", () => {
  const m = { from: "issue", to: "review", cargo: "pr:4" },
    gate = houseDoor("review");
  assert.equal(raidPhase(0).stage, "approach");
  assert.equal(raidPhase(STRIKE[0]).stage, "strike");
  assert.equal(raidPhase(STRIKE[1]).stage, "withdraw");
  assert.equal(raidPhase(2).stage, "withdraw");
  const start = creaturePosition(m, 0, 0),
    door = houseDoor("issue");
  assert.ok(Math.hypot(start.x - door[0], start.y - door[1]) < 30, "the leader sets out from its own gate");
  const arrived = creaturePosition(m, 0, STRIKE[0] - 0.001);
  assert.ok(Math.hypot(arrived.x - gate[0], arrived.y - gate[1]) < 30, "the leader reaches the target gate");
  for (let i = 0; i < packSize(m); i++) {
    const p = creaturePosition(m, i, (STRIKE[0] + STRIKE[1]) / 2, 500, true);
    assert.ok(Math.abs(p.x - gate[0]) < 80 && Math.abs(p.y - gate[1]) < 60, "the pack boils around the gate");
    assert.equal(p.frenzy, 1);
  }
  const leaving = creaturePosition(m, 0, 0.9),
    gone = creaturePosition(m, 0, 1);
  assert.ok(leaving.alpha < 1 && gone.alpha === 0, "the pack fades as it withdraws");
  assert.deepEqual(creaturePosition(m, 2, 0.3, 0, false), creaturePosition(m, 2, 0.3, 9000, false), "reduced motion removes the clock");
  assert.equal(strike(m, 0.3), 0);
  assert.ok(strike(m, STRIKE[0]) > strike(m, STRIKE[1] - 0.01) && strike(m, STRIKE[1] - 0.01) > 0, "impact is hardest on arrival");
  assert.equal(strike(m, 0.9), 0);
  assert.equal(strike({ from: "hall", to: "issue" }, STRIKE[0]), 0, "orders never strike");
});

test("a horde cuts across the lawns above the upper road, where labels cannot hide it", () => {
  const m = { from: "bug", to: "review", cargo: "issue:5" };
  const onRoad = routePosition("bug", "review", 0.5);
  assert.equal(onRoad.y, roadLevels.upper, "the midpoint of this route is on the upper road");
  assert.ok(Math.abs(lift(onRoad)) === LIFT);
  assert.equal(lift({ x: 0, y: roadLevels.lower }), 0, "the lower road is not covered by labels");
  assert.equal(lift({ x: 0, y: roadLevels.upper - 40 }), 0, "the lift fades out along a side path");
  let previous = null;
  for (let p = 0; p <= 1; p += 0.01) {
    const q = deliveryPoint(m, p * STRIKE[0]);
    if (previous) assert.ok(Math.hypot(q.x - previous.x, q.y - previous.y) < 40, "the leader never jumps");
    previous = q;
  }
});

test("hit testing follows the leader and then the gate", () => {
  const m = { from: "bug", to: "issue", cargo: "issue:2" };
  const early = deliveryPoint(m, 0.2),
    onRoad = routePosition("bug", "issue", 0.5);
  assert.ok(early.x !== undefined && early.y !== undefined);
  assert.ok(Math.hypot(early.x - onRoad.x, early.y - onRoad.y) < 400);
  const [gx, gy] = houseDoor("issue");
  assert.deepEqual([deliveryPoint(m, 0.7).x, deliveryPoint(m, 0.7).y], [gx, gy]);
  const sortie = deliveryPoint({ from: "release", to: "outside", cargo: "release:v1" }, 1);
  assert.deepEqual([sortie.x, sortie.y], [routePosition("release", "outside", 1).x, routePosition("release", "outside", 1).y]);
});

test("burrows are the house queue, capped so a backlog stays legible", () => {
  assert.deepEqual(burrowLayout({}), []);
  assert.deepEqual(burrowLayout(undefined), []);
  const busy = burrowLayout({ active: 3, waiting: 20, blocked: 2 });
  assert.equal(busy.length, MAX_BURROWS);
  assert.equal(busy.filter((b) => b.kind === "active").length, 1, "one item is worked at a time");
  assert.equal(busy.filter((b) => b.kind === "blocked").length, 2, "blocked burrows keep their place before waiting ones");
  const lone = burrowLayout({ waiting: 1 });
  assert.equal(lone.length, 1);
  assert.equal(lone[0].kind, "waiting");
});
