import test from "node:test";
import assert from "node:assert/strict";
import {
  rooms,
  posts,
  roomFor,
  lookFor,
  placeCases,
  route,
  changes,
  originOf,
  markFor,
  frames,
  canStage,
  play,
  sceneArt,
  suspectArt,
  at,
} from "./scene.js";
import { unitOrder } from "./precinct.js";

const issue = (number, extra) => ({ id: `issue:${number}`, kind: "issue", number, title: `Case ${number}`, ...extra });

test("every unit has a room in the building and a post to stand at", () => {
  const staffed = Object.values(rooms).flatMap((room) => room.roles);
  assert.deepEqual([...staffed].sort(), [...unitOrder].sort(), "each unit works in exactly one room");
  const where = posts();
  for (const role of unitOrder) assert.equal(where[role].length, 2, `${role} has a post`);
  const off = posts({ bug: "quiet", hall: "quiet" });
  assert.notDeepEqual(off.bug, where.bug, "an officer in quiet hours leaves the post");
  assert.notDeepEqual(off.bug, off.hall, "and takes a seat of its own on the break room couch");
  assert.deepEqual(off.review, where.review, "everyone else stays at the post");
});

test("a case stands in its unit's room, the tank if it was thrown out, or nowhere once it left", () => {
  assert.equal(roomFor(issue(1, { house: "bug", stage: "open" })), "squadroom");
  assert.equal(roomFor(issue(2, { house: "simplifier", stage: "simplifying" })), "magistrate");
  assert.equal(roomFor(issue(3, { house: "hall", stage: "awaiting_mayor", mayoral_decision: "pending" })), "judge");
  assert.equal(roomFor(issue(4, { house: "issue", stage: "queued" })), "rehab");
  assert.equal(roomFor({ id: "pr:5", kind: "pr", number: 5, house: "review", stage: "queued" }), "forensics");
  assert.equal(roomFor({ id: "commit:c", kind: "commit", house: "release", stage: "unreleased" }), "release");
  assert.equal(roomFor(issue(6, { house: "hall", stage: "declined", mayoral_decision: "declined" })), "holding");
  assert.equal(roomFor(issue(7, { house: "hall", stage: "closed", mayoral_decision: "declined" })), "holding", "closed dismissals stay booked");
  assert.equal(roomFor(issue(8, { house: "issue", stage: "implemented" })), null);
  assert.equal(roomFor({ id: "pr:9", kind: "pr", number: 9, house: "review", stage: "merged" }), null);
  assert.equal(roomFor(undefined), null);
});

test("slop looks like slop until it is cuffed or reformed", () => {
  assert.equal(lookFor(issue(1, { house: "bug" }), "open"), "slop");
  assert.equal(lookFor(issue(1, { house: "bug" }), "blocked"), "stuck");
  assert.equal(lookFor(issue(1, { house: "hall", stage: "declined" }), "open"), "cuffed");
  assert.equal(lookFor({ id: "commit:c", house: "release" }, "open"), "reformed", "what reaches Release is reformed");
  assert.match(suspectArt("stuck"), /class="alert"/);
  assert.match(suspectArt("cuffed"), /class="cuffs"/);
  assert.match(suspectArt("reformed"), /class="reformed"/);
});

test("each room stands as many suspects as it has spots, stuck first, then the newest arrivals", () => {
  const tasks = {};
  for (let n = 1; n <= 8; n++) tasks[`issue:${n}`] = issue(n, { house: "issue", stage: "queued", updated: `2026-09-0${n}T00:00:00Z` });
  tasks["issue:8"].blocked = true;
  tasks["issue:1"].updated = "2026-09-30T00:00:00Z";
  tasks["issue:20"] = issue(20, { house: "hall", stage: "declined", mayoral_decision: "declined", updated: "2026-09-01T00:00:00Z" });
  tasks["issue:21"] = issue(21, { house: "hall", stage: "declined", mayoral_decision: "declined", updated: "2026-09-03T00:00:00Z" });
  tasks["issue:30"] = issue(30, { house: "issue", stage: "implemented" });
  const { placed, overflow, counts } = placeCases({ id: "acme/x", tasks, workers: {} });
  const rehab = placed.filter((p) => p.room === "rehab");
  assert.equal(rehab.length, rooms.rehab.slots.length);
  assert.equal(rehab[0].id, "issue:8", "a stuck case stands at the front");
  assert.equal(rehab[0].look, "stuck");
  assert.deepEqual(rehab.slice(1).map((p) => p.id), ["issue:1", "issue:7", "issue:6", "issue:5", "issue:4"], "then the newest arrivals, so a case walking into a full room is seen");
  assert.equal(overflow.rehab, 8 - rooms.rehab.slots.length, "the rest are counted");
  assert.equal(counts.rehab, 8);
  assert.deepEqual(placed.filter((p) => p.room === "holding").map((p) => p.id), ["issue:21", "issue:20"]);
  assert.equal(counts.holding, 2);
  assert.ok(!placed.some((p) => p.id === "issue:30"), "settled work is not in the building");
  for (const p of placed) assert.equal(p.y, rooms[p.room].floor, "everyone stands on their room's floor");
  assert.deepEqual(placeCases(undefined).placed, []);
});

test("routes keep to the floors, the elevator, the skybridge and the court stairs", () => {
  const spot = (room, i = 0) => ({ x: rooms[room].slots[i], y: rooms[room].floor });
  const ys = (points) => new Set(points.map((p) => p.y));
  // Same floor: one straight walk.
  assert.equal(route("frontdesk", spot("frontdesk"), "release", spot("release")).length, 2);
  // Every change of floor happens in the elevator shaft or on the court stairs.
  for (const [a, b] of [["squadroom", "magistrate"], ["magistrate", "judge"], ["judge", "rehab"], ["rehab", "forensics"], ["forensics", "rehab"], ["judge", "holding"]]) {
    const points = route(a, spot(a), b, spot(b));
    assert.deepEqual(points[0], spot(a));
    assert.deepEqual(points.at(-1), spot(b), `${a} → ${b} ends at the spot`);
    for (let i = 1; i < points.length; i++) {
      const [p, q] = [points[i - 1], points[i]];
      assert.ok(p.x === q.x || p.y === q.y, `${a} → ${b} moves along a floor or a shaft, never diagonally`);
      if (p.y !== q.y) assert.ok([416, 1158, 794, 34].includes(p.x), `${a} → ${b} changes level at the lift, the stairs or a door`);
    }
  }
  // The courtrooms share their own staircase.
  const stairs = route("magistrate", spot("magistrate"), "judge", spot("judge"));
  assert.ok(stairs.some((p) => p.x === 1158));
  assert.ok(!stairs.some((p) => p.x === 416), "court to court never goes through the precinct");
  // Out to the street leaves by the exit door.
  const out = route("release", spot("release"), "bus");
  assert.ok(out.some((p) => p.x === 794 && p.y === 632), "through the exit door");
  assert.ok(ys(out).has(690), "onto the sidewalk");
});

test("between two snapshots a case moves, arrives, or leaves, and its exit says why", () => {
  const spotted = (room) => ({ room, x: rooms[room].slots[0] ?? 0, y: rooms[room].floor, look: "slop" });
  const before = new Map([
    ["issue:1", spotted("judge")],
    ["issue:2", spotted("rehab")],
    ["pr:3", spotted("forensics")],
    ["commit:c", spotted("release")],
    ["issue:4", spotted("frontdesk")],
    ["issue:5", spotted("squadroom")],
    ["issue:6", spotted("judge")],
  ]);
  const after = new Map([
    ["issue:1", spotted("holding")],
    ["issue:6", { ...spotted("judge"), x: rooms.judge.slots[2] }],
    ["issue:9", spotted("magistrate")],
  ]);
  const town = {
    tasks: {
      "issue:1": issue(1, { house: "hall", stage: "declined" }),
      "issue:2": issue(2, { house: "issue", stage: "implemented" }),
      "pr:3": { id: "pr:3", kind: "pr", house: "review", stage: "merged" },
      "commit:c": { id: "commit:c", kind: "commit", house: "release", stage: "shipped" },
      "issue:4": issue(4, { house: "repo", stage: "complete" }),
      "issue:5": issue(5, { house: "bug", stage: "closed" }),
    },
  };
  const diff = changes(before, after, town);
  assert.deepEqual(diff.moved.map((m) => [m.id, m.from.room, m.to.room]), [["issue:1", "judge", "holding"], ["issue:6", "judge", "judge"]], "a case shuffles along the line when its room reorders");
  assert.deepEqual(diff.arrived.map((a) => [a.id, a.room]), [["issue:9", "magistrate"]], "an arrival carries its case so the scene can find it");
  assert.deepEqual(Object.fromEntries(diff.gone.map((g) => [g.id, g.reason])), {
    "issue:2": "continued",
    "pr:3": "rehabilitated",
    "commit:c": "shipped",
    "issue:4": "done",
    "issue:5": "closed",
  });
  assert.deepEqual(changes(after, after, town), { moved: [], arrived: [], gone: [] });
});

test("an arrival comes from whoever last handed it over; civilians come in off the street", () => {
  const events = [
    { kind: "delivery", from: "outside", to: "simplifier", cargo: "issue:1" },
    { kind: "delivery", from: "issue", to: "review", cargo: "pr:2" },
    { kind: "delivery", from: "simplifier", to: "hall", cargo: "issue:1" },
    { kind: "control", cargo: "issue:3" },
  ];
  assert.equal(originOf("issue:1", events), "magistrate", "the latest handover counts");
  assert.equal(originOf("pr:2", events), "rehab");
  assert.equal(originOf("issue:3", events), null);
  assert.equal(originOf("issue:1", [events[0]]), "street");
  assert.equal(originOf("issue:1"), null);
});

test("each walk ends with the stamp that says what it meant", () => {
  assert.deepEqual(markFor("judge", "holding"), { text: "LOCKED UP", tone: "red" });
  assert.deepEqual(markFor("street", "magistrate"), { text: "ARRAIGNED", tone: "yellow" });
  assert.deepEqual(markFor("magistrate", "judge"), { text: "ON THE DOCKET", tone: "yellow" });
  assert.deepEqual(markFor("judge", "rehab"), { text: "PROBATION", tone: "green" });
  assert.deepEqual(markFor("judge", "forensics"), { text: "SET FREE", tone: "green" });
  assert.deepEqual(markFor("forensics", "rehab"), { text: "SENT BACK", tone: "orange" });
  assert.deepEqual(markFor("rehab", "forensics"), { text: "EVIDENCE IN", tone: "blue" });
  assert.deepEqual(markFor("squadroom", "rehab"), { text: "ASSIGNED", tone: "blue" });
  assert.deepEqual(markFor("street", "frontdesk"), { text: "BOOKED", tone: "yellow" });
});

test("a walk is timed by its length, with the elevator faster than the feet", () => {
  const flat = frames([{ x: 0, y: 0 }, { x: 170, y: 0 }]);
  assert.equal(Math.round(flat.duration), 1000);
  assert.deepEqual(flat.keyframes.map((k) => k.offset), [0, 1]);
  assert.equal(flat.keyframes[1].left, at(170, 0).match(/left:([^;]+)/)[1]);
  const lift = frames([{ x: 0, y: 0 }, { x: 0, y: 240 }]);
  assert.equal(Math.round(lift.duration), 1000);
  const both = frames([{ x: 0, y: 0 }, { x: 170, y: 0 }, { x: 170, y: 240 }]);
  assert.ok(Math.abs(both.keyframes[1].offset - 0.5) < 1e-9, "a walk and a ride of equal time split the trip");
  assert.equal(frames([{ x: 5, y: 5 }]).keyframes.length, 1, "standing still is one frame");
});

test("the building is drawn from the lights, and scenes without a real layout do nothing", async () => {
  const art = sceneArt({ repo: "acme/<x>", lights: { hall: "active", simplifier: "quiet", bug: "blocked" } });
  assert.match(art, /^<svg class="scene-svg"/);
  assert.match(art, /SLOPCOP SQUAD/);
  assert.match(art, /acme\/&lt;x&gt;/, "the repository name is escaped");
  for (const room of Object.values(rooms)) assert.ok(art.includes(room.sign), `${room.sign} is signed`);
  assert.match(art, /court-lamp in-session/, "an active Probation Judge is in session");
  assert.equal((art.match(/in-session/g) || []).length, 1, "a sleeping Magistrate is not");
  assert.match(art, /class="cop cop-blocked"/);
  assert.match(art, /class="cop cop-quiet judge"/);
  assert.equal(canStage(undefined), false);
  assert.equal(canStage({ fx: {} }), false);
  await play({ fx: {}, find: () => null }, { moved: [{ id: "x" }], arrived: [], gone: [{ id: "y", reason: "shipped" }] });
});
