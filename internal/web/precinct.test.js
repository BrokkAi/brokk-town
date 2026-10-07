import test from "node:test";
import assert from "node:assert/strict";
import {
  units,
  unitOrder,
  isUnit,
  callsign,
  unitName,
  unitBot,
  dutyStatus,
  dutyLight,
  caseNumber,
  cargoLabel,
  radioRoute,
  roster,
} from "./precinct.js";
import { botNames } from "./town.js";

test("every bot staffs exactly one unit with a unique callsign", () => {
  assert.deepEqual([...unitOrder].sort(), Object.keys(botNames).sort(), "the roster covers every role the API knows");
  assert.deepEqual(Object.keys(units).sort(), [...unitOrder].sort());
  const signs = unitOrder.map(callsign);
  assert.equal(new Set(signs).size, signs.length, "callsigns are unique");
  for (const sign of signs) assert.match(sign, /^[A-Z]{3}$/, "a callsign is three capitals");
  assert.equal(unitName("hall"), "Probation Judge");
  assert.equal(unitBot("hall"), "Judge Bot");
  assert.equal(unitName("simplifier"), "Magistrate");
  assert.equal(unitName("issue"), "Caseworker");
  assert.equal(unitName("bug"), "Bug Detective");
  assert.equal(unitName("feature"), "Feature Detective");
  assert.equal(isUnit("outside"), false);
  assert.equal(isUnit("toString"), false, "only own unit keys count");
});

test("the radio reads transfers by callsign and cases by number", () => {
  assert.equal(radioRoute({ kind: "delivery", from: "outside", to: "hall" }), "CIV → JDG");
  assert.equal(radioRoute({ kind: "delivery", from: "issue", to: "review" }), "CWK → LAB");
  assert.equal(radioRoute({ kind: "delivery", from: "operator", to: "issue" }), "CPT → CWK");
  assert.equal(radioRoute({ kind: "control" }), "CONTROL");
  assert.equal(radioRoute({ kind: "uncertain_write" }), "UNCERTAIN WRITE");
  assert.equal(cargoLabel("issue:704"), "#704");
  assert.equal(cargoLabel("pr:12"), "PR 12");
  assert.equal(cargoLabel("commit:abcdef1234567"), "abcdef1");
  assert.equal(cargoLabel(""), "case");
  assert.equal(caseNumber({ kind: "pr", number: 12 }), "PR 12");
  assert.equal(caseNumber({ kind: "issue", number: 7 }), "#7");
  assert.equal(caseNumber({ kind: "commit", head: "0123456789" }), "0123456");
  assert.equal(caseNumber({ kind: "source" }), "SOURCE");
});

test("a unit's duty reads in the precinct's words and lights the matching dot", () => {
  assert.equal(dutyStatus("working"), "on a case");
  assert.equal(dutyStatus("waiting"), "standing by");
  assert.equal(dutyStatus("paused"), "stood down");
  assert.equal(dutyStatus("failed"), "stuck");
  assert.equal(dutyStatus("quiet"), "quiet hours");
  assert.equal(dutyStatus("recovery_hold"), "recovery hold", "an unfamiliar status reads as the service spelled it");
  assert.equal(dutyStatus(""), "stood down");
  assert.equal(dutyLight("pausing"), "active");
  assert.equal(dutyLight("failed"), "blocked");
  assert.equal(dutyLight("quiet"), "quiet");
  assert.equal(dutyLight("paused"), "waiting");
});

test("the roster names every unit in order with its duty and caseload", () => {
  const town = {
    id: "acme/x",
    config: {},
    tasks: {
      "issue:3": { id: "issue:3", kind: "issue", number: 3, house: "hall", stage: "awaiting_mayor", mayoral_decision: "pending" },
      "issue:5": { id: "issue:5", kind: "issue", number: 5, house: "hall", stage: "awaiting_mayor", mayoral_decision: "pending", blocked: true },
      "issue:6": { id: "issue:6", kind: "issue", number: 6, house: "issue", stage: "queued" },
    },
    workers: {
      issue: { role: "issue", status: "working", enabled: true },
      hall: { role: "hall", status: "quiet", enabled: true },
    },
  };
  const units = roster(town);
  assert.deepEqual(units.map((unit) => unit.role), unitOrder);
  const byRole = Object.fromEntries(units.map((unit) => [unit.role, unit]));
  assert.equal(byRole.issue.status, "on a case");
  assert.equal(byRole.issue.light, "active");
  assert.equal(byRole.issue.counts.waiting, 1);
  assert.equal(byRole.hall.rulings, 1, "a blocked ruling is not one waiting on the court");
  assert.equal(byRole.hall.light, "quiet");
  assert.equal(byRole.hall.bot, "Judge Bot");
  assert.equal(byRole.repo.status, "stood down", "a unit with no worker reads as stood down");
});
