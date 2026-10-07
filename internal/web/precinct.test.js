import test from "node:test";
import assert from "node:assert/strict";
import {
  units,
  unitOrder,
  lanes,
  isUnit,
  callsign,
  unitName,
  unitBot,
  laneOf,
  dutyStatus,
  dutyLight,
  caseNumber,
  cargoLabel,
  radioRoute,
  caseFlow,
  roster,
} from "./precinct.js";
import { botNames } from "./town.js";

test("every bot staffs exactly one unit, in one lane, with a unique callsign", () => {
  assert.deepEqual([...unitOrder].sort(), Object.keys(botNames).sort(), "the roster covers every role the API knows");
  assert.deepEqual(Object.keys(units).sort(), [...unitOrder].sort());
  const signs = unitOrder.map(callsign);
  assert.equal(new Set(signs).size, signs.length, "callsigns are unique");
  for (const sign of signs) assert.match(sign, /^[A-Z]{3}$/, "a callsign is three capitals");
  assert.deepEqual(lanes.flatMap((lane) => lane.roles).sort(), [...unitOrder].sort(), "each unit sits in exactly one lane");
  for (const role of unitOrder) assert.ok(lanes.find((lane) => lane.id === laneOf(role)).roles.includes(role));
  assert.equal(unitName("hall"), "Courthouse");
  assert.equal(unitBot("hall"), "Judge Bot");
  assert.equal(unitName("simplifier"), "Slop Squad");
  assert.equal(isUnit("outside"), false);
  assert.equal(isUnit("toString"), false, "only own unit keys count");
});

test("the radio reads transfers by callsign and cases by number", () => {
  assert.equal(radioRoute({ kind: "delivery", from: "outside", to: "hall" }), "CIV → CRT");
  assert.equal(radioRoute({ kind: "delivery", from: "issue", to: "review" }), "TSK → LAB");
  assert.equal(radioRoute({ kind: "delivery", from: "operator", to: "issue" }), "CPT → TSK");
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

test("case flow lays open cases into lanes, urgent first, and caps each lane", () => {
  const tasks = {
    "issue:1": { id: "issue:1", kind: "issue", number: 1, title: "Lead", house: "bug", stage: "open" },
    "issue:2": { id: "issue:2", kind: "issue", number: 2, title: "Screen me", house: "simplifier", stage: "simplifying" },
    "issue:3": { id: "issue:3", kind: "issue", number: 3, title: "Rule on me", house: "hall", stage: "awaiting_mayor", mayoral_decision: "pending" },
    "issue:4": { id: "issue:4", kind: "issue", number: 4, title: "Dismissed", house: "hall", stage: "declined", mayoral_decision: "declined" },
    "pr:9": { id: "pr:9", kind: "pr", number: 9, title: "Stuck", house: "review", stage: "queued", blocked: true },
    "commit:c": { id: "commit:c", kind: "commit", title: "Ship me", house: "release", stage: "unreleased", head: "c".repeat(40) },
  };
  for (let n = 20; n < 28; n++)
    tasks[`issue:${n}`] = { id: `issue:${n}`, kind: "issue", number: n, title: `Work ${n}`, house: "issue", stage: "queued" };
  for (let n = 30; n < 32; n++)
    tasks[`pr:${n}`] = { id: `pr:${n}`, kind: "pr", number: n, title: `Exam ${n}`, house: "review", stage: "queued" };
  const town = { id: "acme/x", tasks, workers: {} };
  const flow = Object.fromEntries(caseFlow(town).map((lane) => [lane.id, lane]));
  assert.deepEqual(Object.keys(flow), ["leads", "screening", "court", "taskforce", "forensics", "release"], "lanes run from leads to release");
  assert.deepEqual(flow.leads.cases.map((task) => task.id), ["issue:1"]);
  assert.deepEqual(flow.screening.cases.map((task) => task.id), ["issue:2"]);
  assert.deepEqual(flow.court.cases.map((task) => task.id), ["issue:3"], "a dismissed case leaves the flow");
  assert.equal(flow.forensics.cases[0].id, "pr:9", "a stuck case leads its lane");
  assert.equal(flow.forensics.urgent, 1);
  assert.equal(flow.taskforce.total, 8);
  assert.equal(flow.taskforce.cases.length, 6, "a lane shows at most six cases");
  assert.equal(flow.taskforce.more, 2);
  assert.equal(flow.taskforce.moreHouse, "issue", "the rest of a lane opens in the unit holding it");
  assert.deepEqual(flow.release.cases.map((task) => task.id), ["commit:c"]);
  assert.equal(caseFlow(undefined).every((lane) => lane.total === 0), true, "no town means empty lanes");
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
