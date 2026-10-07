import test from "node:test";
import assert from "node:assert/strict";
import { robotCop, slopBlob, reformedBlob, inCustody, cruiser, bus } from "./animation.js";
import { unitOrder } from "./precinct.js";

test("every unit's robot cop shows its state: working its prop, asleep, or stuck and waving for help", () => {
  for (const role of unitOrder) {
    for (const light of ["active", "waiting", "blocked", "quiet"]) {
      const svg = robotCop(role, light);
      assert.match(svg, new RegExp(`^<svg class="cop cop-${light}[ "]`), `${role} ${light} draws its state`);
      assert.match(svg, /aria-hidden="true"/, "the officer is decoration, never a control");
    }
  }
  assert.match(robotCop("bug", "quiet"), /class="zzz"/, "quiet hours put the officer to sleep");
  assert.match(robotCop("bug", "blocked"), /class="help"/, "a stuck unit waves for help");
  assert.doesNotMatch(robotCop("bug", "active"), /class="zzz"|class="help"/);
  assert.match(robotCop("repo", "active"), /class="beam"/, "Patrol on a case sweeps a flashlight");
  assert.doesNotMatch(robotCop("repo", "waiting"), /class="beam"/, "Patrol standing by has a donut instead");
  assert.match(robotCop("bug", "active"), /prop-sweep/, "the Bug Detective works a magnifier");
  assert.match(robotCop("feature", "active"), /prop-tilt/, "the Feature Detective scans with binoculars");
  assert.match(robotCop("issue", "active"), /class="pen"/, "the Caseworker writes up the case");
  assert.match(robotCop("review", "active"), /class="bubbles"/, "Forensics runs a test tube");
});

test("the judges sit in robes and wigs; the officers wear caps", () => {
  for (const role of ["hall", "simplifier"]) {
    assert.match(robotCop(role, "active"), /^<svg class="cop cop-active judge"/, `${role} is a judge`);
    assert.match(robotCop(role, "active"), /class="wig"/);
  }
  assert.match(robotCop("hall", "active"), /prop-gavel/, "the Probation Judge has a gavel");
  assert.match(robotCop("simplifier", "active"), /prop-scales/, "the Magistrate weighs the evidence");
  for (const role of ["repo", "bug", "feature", "issue", "review", "release"])
    assert.doesNotMatch(robotCop(role, "active"), /judge|class="wig"/, `${role} is in uniform`);
});

test("slop gets cuffs only when caught, and reformed slop is slop no more", () => {
  assert.doesNotMatch(slopBlob(), /class="cuffs"/);
  assert.match(slopBlob({ cuffed: true }), /class="cuffs"/);
  assert.match(reformedBlob(), /^<svg class="reformed"/);
  assert.doesNotMatch(reformedBlob(), /class="goo"|class="cuffs"/, "the reformed are not goo");
  assert.match(cruiser(), /class="bar-red"/, "a squad car runs its lights");
  assert.match(bus(), /RELEASE EXPRESS/, "the release bus says where it goes");
  assert.match(bus(), /class="wheel"/);
});

test("custody is every case a judge threw out", () => {
  assert.equal(inCustody({ stage: "declined" }), true);
  assert.equal(inCustody({ stage: "closed", mayoral_decision: "declined" }), true, "a closed dismissal stays booked");
  assert.equal(inCustody({ stage: "queued" }), false, "open work is not in custody");
  assert.equal(inCustody({ stage: "awaiting_mayor", mayoral_decision: "pending" }), false);
  assert.equal(inCustody(undefined), false);
});
