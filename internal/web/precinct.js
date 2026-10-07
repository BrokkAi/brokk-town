import { botNames, houseWorkload, projectTask, projectWorker, queueFor } from "./town.js";

// SlopCop Squad presents each repository as a precinct and its bots as the
// units that work it. This module owns that vocabulary and the shape of
// the Precinct view: who is on the roster, which lane a case sits in, and how
// the radio spells a transfer. It reads the committed snapshot only; every
// command still goes through app.js to the service.

// The role keys are the API's and never change. A unit is what the operator
// sees: its name, its radio callsign and one line on what it does.
export const units = {
  repo: { callsign: "PTL", name: "Patrol", tagline: "Walks the beat", lane: "leads", duty: "Inventories the repository on every pass and repairs the branch it covers when its checks fail." },
  bug: { callsign: "DET", name: "Detectives", tagline: "Investigations", lane: "leads", duty: "Investigates the code and files new, non-duplicate bug reports." },
  feature: { callsign: "INT", name: "Intel", tagline: "Leads & proposals", lane: "leads", duty: "Researches valuable new capabilities and files concrete proposals." },
  simplifier: { callsign: "SLP", name: "Slop Squad", tagline: "Screens for slop", lane: "screening", duty: "Screens every arrival for slop: disproportionate complexity and low value." },
  hall: { callsign: "CRT", name: "Courthouse", tagline: "Rulings & the blotter", lane: "court", duty: "Rules on every arrival and keeps the blotter of features gained and bugs fixed." },
  issue: { callsign: "TSK", name: "Task Force", tagline: "Works the case", lane: "taskforce", duty: "Takes admitted cases, implements them, and opens or repairs pull requests." },
  review: { callsign: "LAB", name: "Forensics", tagline: "Examines the evidence", lane: "forensics", duty: "Examines each exact revision, certifies findings, and merges when policy allows." },
  release: { callsign: "REL", name: "Release", tagline: "Processing & release", lane: "release", duty: "Batches what merged and ships a verified release." },
};

// Roster order follows a case through the precinct, and keys 1–8 follow it.
export const unitOrder = ["repo", "bug", "feature", "simplifier", "hall", "issue", "review", "release"];

// A case moves left to right: leads come in, the Slop Squad screens them, the
// court rules, the Task Force works them, Forensics examines the evidence and
// Release ships what merged.
export const lanes = [
  { id: "leads", label: "Leads", roles: ["repo", "bug", "feature"] },
  { id: "screening", label: "Screening", roles: ["simplifier"] },
  { id: "court", label: "Court", roles: ["hall"] },
  { id: "taskforce", label: "Task Force", roles: ["issue"] },
  { id: "forensics", label: "Forensics", roles: ["review"] },
  { id: "release", label: "Release", roles: ["release"] },
];

// Parties on the radio that are not units: a civilian report from outside the
// precinct, and the captain, which is you.
const parties = { outside: "CIV", operator: "CPT" };

export function isUnit(role) {
  return Object.hasOwn(units, role);
}
export function callsign(role) {
  return units[role]?.callsign || parties[role] || (String(role || "").slice(0, 3).toUpperCase() || "---");
}
export function unitName(role) {
  return units[role]?.name || "Unit";
}
// The bot staffing a unit, for the settings and failures that name it.
export function unitBot(role) {
  return botNames[role] || "";
}
export function laneOf(role) {
  return units[role]?.lane || "leads";
}

function normalized(value) {
  return String(value ?? "").trim().toLowerCase().replaceAll(" ", "_");
}

// A unit's state in the precinct's own words. Statuses the precinct has no
// word for read as the service spelled them.
const dutyWords = {
  working: "on a case",
  pausing: "standing down",
  waiting: "standing by",
  paused: "stood down",
  failed: "stuck",
  blocked: "stuck",
  quiet: "quiet hours",
};
export function dutyStatus(status) {
  const key = normalized(status);
  return dutyWords[key] || key.replaceAll("_", " ") || "stood down";
}

// The light a unit shows: on a case, standing by, stuck, or quiet.
export function dutyLight(status) {
  const key = normalized(status);
  if (key === "working" || key === "pausing") return "active";
  if (key === "failed" || key === "blocked") return "blocked";
  if (key === "quiet") return "quiet";
  return "waiting";
}

// A case is identified the way an operator would say it on the radio.
export function caseNumber(task) {
  const number = Number(task?.number) || 0;
  if (task?.kind === "pr" && number > 0) return `PR ${number}`;
  if (number > 0) return `#${number}`;
  if (task?.kind === "commit" && task.head) return String(task.head).slice(0, 7);
  return String(task?.kind || "case").toUpperCase();
}

// A transfer's cargo is a task ID; the radio reads it as a case.
export function cargoLabel(cargo) {
  const [kind, id] = String(cargo || "").split(":");
  if (kind === "pr" && id) return `PR ${id}`;
  if (kind === "issue" && id) return `#${id}`;
  if (kind === "commit" && id) return id.slice(0, 7);
  return String(cargo || "case").slice(0, 12);
}

// One radio line's route: a transfer names both ends by callsign; any other
// traffic names its kind.
export function radioRoute(event) {
  if (event?.kind === "delivery") return `${callsign(event.from)} → ${callsign(event.to)}`;
  return String(event?.kind || "radio").replaceAll("_", " ").toUpperCase();
}

const laneUrgency = { blocked: 0, uncertain_write: 0, inconclusive: 0, failed: 0, working: 1 };

// caseFlow lays the town's open cases into the lanes. Each lane keeps the
// cases that need someone first, then those being worked, then the rest by
// number, and shows at most limit of them.
export function caseFlow(town, limit = 6) {
  return lanes.map((lane) => {
    const cases = lane.roles
      .flatMap((role) => queueFor(town || {}, role))
      .map((task) => projectTask(town, task))
      .sort((a, b) =>
        (laneUrgency[a.status] ?? 2) - (laneUrgency[b.status] ?? 2) ||
        (a.number ?? 0) - (b.number ?? 0) ||
        String(a.id).localeCompare(String(b.id)),
      );
    return {
      ...lane,
      total: cases.length,
      urgent: cases.filter((task) => laneUrgency[task.status] === 0).length,
      cases: cases.slice(0, limit),
      more: Math.max(0, cases.length - limit),
      // Past the limit, the rest of the lane is read in the unit holding the
      // first case that did not fit.
      moreHouse: cases[limit]?.house || lane.roles[0],
    };
  });
}

// roster is every unit in order with what it is doing now and its caseload.
// The Courthouse is measured in rulings: what waits on it is a decision.
export function roster(town) {
  return unitOrder.map((role) => {
    const worker = projectWorker(town, role, town?.workers?.[role]);
    const counts = houseWorkload(town, role);
    const rulings = role === "hall"
      ? queueFor(town || {}, "hall").filter((task) => task.mayoral_decision === "pending" && !task.blocked).length
      : 0;
    return {
      role,
      ...units[role],
      bot: botNames[role],
      worker,
      counts,
      rulings,
      light: dutyLight(worker.status),
      status: dutyStatus(worker.status),
    };
  });
}
