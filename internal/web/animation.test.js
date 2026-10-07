import test from "node:test";
import assert from "node:assert/strict";
import {
  alarm,
  robotCop,
  slopBlob,
  cruiser,
  stampFor,
  isCitation,
  inCustody,
  custody,
  newArrests,
  canStage,
  patrolRun,
  arrest,
} from "./animation.js";
import { unitOrder } from "./precinct.js";

test("every unit's robot cop shows its state: working its prop, asleep, or stuck and waving for help", () => {
  for (const role of unitOrder) {
    for (const light of ["active", "waiting", "blocked", "quiet"]) {
      const svg = robotCop(role, light);
      assert.match(svg, new RegExp(`^<svg class="cop cop-${light}"`), `${role} ${light} draws its state`);
      assert.match(svg, /aria-hidden="true"/, "the officer is decoration, never a control");
    }
  }
  assert.match(robotCop("bug", "quiet"), /class="zzz"/, "quiet hours put the officer to sleep");
  assert.match(robotCop("bug", "blocked"), /class="help"/, "a stuck unit waves for help");
  assert.doesNotMatch(robotCop("bug", "active"), /class="zzz"|class="help"/);
  assert.match(robotCop("repo", "active"), /class="beam"/, "Patrol on a case sweeps a flashlight");
  assert.doesNotMatch(robotCop("repo", "waiting"), /class="beam"/, "Patrol standing by has a donut instead");
  assert.match(robotCop("hall", "active"), /prop-gavel/, "the judge has a gavel");
  assert.match(robotCop("simplifier", "active"), /prop-mop/, "the Slop Squad mops");
});

test("the slop gets cuffs only when caught, and the paddy wagon has a suspect at the window", () => {
  assert.doesNotMatch(slopBlob(), /class="cuffs"/);
  assert.match(slopBlob({ cuffed: true }), /class="cuffs"/);
  assert.match(cruiser(), /class="bar-red"/, "a squad car runs its lights");
  assert.doesNotMatch(cruiser(), /class="inmate"/);
  assert.match(cruiser({ wagon: true }), /class="inmate"/);
});

test("each transfer earns the stamp that says what it meant", () => {
  const delivery = (from, to) => ({ kind: "delivery", from, to });
  assert.deepEqual(stampFor(delivery("review", "release")), { text: "CLEARED", tone: "green" });
  assert.deepEqual(stampFor(delivery("release", "outside")), { text: "RELEASED", tone: "green" });
  assert.deepEqual(stampFor(delivery("review", "issue")), { text: "SENT BACK", tone: "orange" });
  assert.deepEqual(stampFor(delivery("hall", "issue")), { text: "ADMITTED", tone: "green" });
  assert.deepEqual(stampFor(delivery("outside", "hall")), { text: "ON THE DOCKET", tone: "yellow" });
  assert.deepEqual(stampFor(delivery("outside", "simplifier")), { text: "BOOKED", tone: "yellow" });
  assert.deepEqual(stampFor(delivery("bug", "issue")), { text: "ASSIGNED", tone: "blue" });
  assert.deepEqual(stampFor(delivery("issue", "review")), { text: "EVIDENCE IN", tone: "blue" });
  assert.deepEqual(stampFor({ kind: "error", from: "repo", to: "repo" }), { text: "STUCK", tone: "red" });
  assert.equal(stampFor({ kind: "control" }), null, "operator traffic gets no stamp");
  assert.equal(isCitation(delivery("review", "issue")), true, "findings sent back are a citation");
  assert.equal(isCitation(delivery("issue", "review")), false);
});

test("the tank holds every dismissed case, newest booking first", () => {
  const town = {
    tasks: {
      "issue:1": { id: "issue:1", kind: "issue", number: 1, stage: "declined", mayoral_decision: "declined", updated: "2026-09-01T00:00:00Z" },
      "pr:2": { id: "pr:2", kind: "pr", number: 2, stage: "closed", mayoral_decision: "declined", updated: "2026-09-03T00:00:00Z" },
      "issue:3": { id: "issue:3", kind: "issue", number: 3, stage: "declined", simplification: { mode: "auto", decision: "decline" }, updated: "2026-09-02T00:00:00Z" },
      "issue:4": { id: "issue:4", kind: "issue", number: 4, stage: "queued" },
      "issue:5": { id: "issue:5", kind: "issue", number: 5, stage: "awaiting_mayor", mayoral_decision: "pending" },
    },
  };
  assert.equal(inCustody(town.tasks["issue:4"]), false, "open work is not in custody");
  assert.equal(inCustody(undefined), false);
  const held = custody(town);
  assert.equal(held.total, 3);
  assert.deepEqual(held.perps.map((task) => task.id), ["pr:2", "issue:3", "issue:1"]);
  assert.equal(custody(town, 2).perps.length, 2, "the tank shows a capped line-up");
  assert.equal(custody(town, 2).total, 3, "but counts everyone");
  assert.equal(custody(undefined).total, 0);
});

test("an arrest is a case that entered custody between two committed snapshots", () => {
  const before = { tasks: { "issue:1": { id: "issue:1", stage: "awaiting_mayor", mayoral_decision: "pending" }, "issue:2": { id: "issue:2", stage: "declined", mayoral_decision: "declined" } } };
  const after = structuredClone(before);
  after.tasks["issue:1"] = { id: "issue:1", stage: "declined", mayoral_decision: "declined" };
  after.tasks["issue:3"] = { id: "issue:3", stage: "declined", simplification: { mode: "auto", decision: "decline" } };
  assert.deepEqual(newArrests(before, after).sort(), ["issue:1", "issue:3"], "already-booked slop is not arrested twice");
  assert.deepEqual(newArrests(undefined, after), [], "a first snapshot books nobody");
  assert.deepEqual(newArrests(after, after), []);
});

test("scenes need a real layout; without one they do nothing and never throw", async () => {
  assert.equal(canStage(undefined), false);
  assert.equal(canStage({ frame: {}, layer: {} }), false);
  await patrolRun({}, { kind: "delivery", from: "issue", to: "review", cargo: "pr:1" });
  await arrest({}, { id: "issue:1", house: "hall" });
  alarm({}, { kind: "error", from: "repo", to: "repo" });
});
