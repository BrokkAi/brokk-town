import test from "node:test";
import assert from "node:assert/strict";
import {
  bots,
  botOrder,
  isBot,
  botName,
  botLabel,
  statusText,
  statusLight,
  taskRef,
  cargoRef,
  eventRoute,
  eventActor,
  pipeline,
} from "./bots.js";
import { botNames } from "./town.js";

test("every role the API knows has one pipeline stage", () => {
  assert.deepEqual([...botOrder].sort(), Object.keys(botNames).sort());
  assert.deepEqual(Object.keys(bots).sort(), [...botOrder].sort());
  assert.equal(botLabel("hall"), "Triage");
  assert.equal(botName("hall"), "Mayor Bot");
  assert.equal(botLabel("issue"), "Implementation");
  assert.equal(botName("outside"), "External");
  assert.equal(botName("operator"), "You");
  assert.equal(isBot("outside"), false);
  assert.equal(isBot("toString"), false, "only own role keys count");
});

test("activity routes name both ends and other events by kind", () => {
  assert.equal(eventRoute({ kind: "delivery", from: "outside", to: "hall" }), "External → Triage");
  assert.equal(eventRoute({ kind: "delivery", from: "issue", to: "review" }), "Implementation → Review");
  assert.equal(eventRoute({ kind: "delivery", from: "operator", to: "issue" }), "You → Implementation");
  assert.equal(eventRoute({ kind: "control" }), "Control");
  assert.equal(eventRoute({ kind: "uncertain_write" }), "Uncertain write");
});

test("an event names the signed-in person who acted", () => {
  assert.equal(eventActor({ kind: "control" }, "alice"), "");
  assert.equal(eventActor({ kind: "control", actor: "Alice" }, "alice"), "You");
  assert.equal(eventActor({ kind: "decision", actor: "bob" }, "alice"), "bob");
  assert.equal(eventActor({ kind: "decision", actor: "bob" }, ""), "bob");
});

test("tasks read by number, pull request or commit", () => {
  assert.equal(taskRef({ kind: "pr", number: 7 }), "PR 7");
  assert.equal(taskRef({ kind: "issue", number: 12 }), "#12");
  assert.equal(taskRef({ kind: "commit", head: "abcdef123456" }), "abcdef1");
  assert.equal(cargoRef("pr:7"), "PR 7");
  assert.equal(cargoRef("issue:12"), "#12");
  assert.equal(cargoRef("commit:abcdef123456"), "abcdef1");
});

test("worker status reads plainly and maps to an indicator", () => {
  assert.equal(statusText("working"), "Working");
  assert.equal(statusText("waiting"), "Idle");
  assert.equal(statusText("paused"), "Paused");
  assert.equal(statusText("recovery_required"), "Recovery required");
  assert.equal(statusText(""), "Paused");
  assert.equal(statusLight("working"), "active");
  assert.equal(statusLight("failed"), "blocked");
  assert.equal(statusLight("quiet"), "quiet");
  assert.equal(statusLight("paused"), "waiting");
});

test("the pipeline lists every bot in order and counts triage decisions", () => {
  const town = {
    workers: { hall: { role: "hall", status: "waiting", enabled: true } },
    tasks: {
      "issue:1": { id: "issue:1", kind: "issue", number: 1, house: "hall", stage: "awaiting_mayor", mayoral_decision: "pending" },
      "issue:2": { id: "issue:2", kind: "issue", number: 2, house: "hall", stage: "awaiting_mayor", mayoral_decision: "pending", blocked: true },
    },
  };
  const rows = pipeline(town);
  assert.deepEqual(rows.map((row) => row.role), botOrder);
  const triage = rows.find((row) => row.role === "hall");
  assert.equal(triage.decisions, 1, "a blocked task is not a decision waiting");
  assert.equal(triage.bot, "Mayor Bot");
  assert.equal(triage.label, "Triage");
});
