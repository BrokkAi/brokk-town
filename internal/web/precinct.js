import { botNames, houseWorkload, projectWorker, queueFor } from "./town.js";

// SlopCop Squad presents each repository as a precinct and its bots as the
// units that work it. This module owns that vocabulary: who is on the roster,
// what each unit is called and how the radio spells a transfer. It reads the
// committed snapshot only; every command still goes through app.js.

// The role keys are the API's and never change. A unit is what the operator
// sees: its name, its radio callsign and one line on what it does. Detectives
// find new slop; the Magistrate and the Probation Judge decide whether it goes
// to the Slop Tank or gets a chance; the Caseworker rehabilitates it and
// Forensics checks the work; Release ships the reformed out.
export const units = {
  repo: { callsign: "PTL", name: "Patrol", tagline: "Walks the beat", duty: "Inventories the repository on every pass, books what comes in, and repairs the branch it covers when its checks fail." },
  bug: { callsign: "BUG", name: "Bug Detective", tagline: "Finds new slop", duty: "Investigates the code and files new, non-duplicate bug reports." },
  feature: { callsign: "FTR", name: "Feature Detective", tagline: "Finds new slop", duty: "Researches new capabilities and files concrete proposals, which are slop until a judge says otherwise." },
  simplifier: { callsign: "MAG", name: "Magistrate", tagline: "Arraignment", duty: "Arraigns every arrival: disproportionate complexity and low value go to the Slop Tank, the rest go free to the next hearing." },
  hall: { callsign: "JDG", name: "Probation Judge", tagline: "Probation or the tank", duty: "Rules on every arrival, probation with the Caseworker or the Slop Tank, and keeps the blotter of features gained and bugs fixed." },
  issue: { callsign: "CWK", name: "Caseworker", tagline: "Rehabilitates slop", duty: "Works each case on probation: implements it and opens or repairs the pull request." },
  review: { callsign: "LAB", name: "Forensics", tagline: "Examines the evidence", duty: "Examines each exact revision, certifies findings, and clears the case or sends it back; merges when policy allows." },
  release: { callsign: "REL", name: "Release", tagline: "Ships the reformed", duty: "Batches what merged and ships a verified release." },
};

// Roster order follows a case through the precinct, and keys 1–8 follow it.
export const unitOrder = ["repo", "bug", "feature", "simplifier", "hall", "issue", "review", "release"];

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

// roster is every unit in order with what it is doing now and its caseload.
// The Probation Judge is measured in rulings: what waits on it is a decision.
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
