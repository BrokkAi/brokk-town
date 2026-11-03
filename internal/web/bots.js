import { botNames, houseWorkload, projectWorker, queueFor } from "./town.js";

// The eight bots in pipeline order, with the label and one-line description the
// browser shows for each. The role keys are the API's and never change. This
// module reads the committed snapshot only; every command goes through app.js.
export const bots = {
  repo: { label: "Repository", summary: "Scans the repository on every pass, records new arrivals, and repairs the covered branch when its checks fail." },
  bug: { label: "Bug discovery", summary: "Investigates the code and files new, non-duplicate bug reports." },
  feature: { label: "Feature discovery", summary: "Researches valuable new capabilities and files concrete proposals." },
  simplifier: { label: "Simplification", summary: "Reviews every arrival for disproportionate complexity and low value before it is triaged." },
  hall: { label: "Triage", summary: "Decides whether each arrival is accepted or declined, and writes the status report of features added and bugs fixed." },
  issue: { label: "Implementation", summary: "Implements accepted issues and opens or repairs the pull request." },
  review: { label: "Review", summary: "Reviews each exact revision, certifies findings, and approves or requests changes; merges when policy allows." },
  release: { label: "Release", summary: "Batches merged work and publishes a verified release." },
};

// Pipeline order follows a task through Town, and keys 1–8 follow it.
export const botOrder = ["repo", "bug", "feature", "simplifier", "hall", "issue", "review", "release"];

// Event endpoints that are not bots.
const parties = { outside: "External", operator: "You" };

export function isBot(role) {
  return Object.hasOwn(bots, role);
}
// botName is the name a bot ships under, or the party for a non-bot endpoint.
export function botName(role) {
  return botNames[role] || parties[role] || "Town";
}
// botLabel is the pipeline stage a bot owns.
export function botLabel(role) {
  return bots[role]?.label || "Town";
}

function normalized(value) {
  return String(value ?? "").trim().toLowerCase().replaceAll(" ", "_");
}

const statusWords = {
  working: "Working",
  pausing: "Pausing",
  waiting: "Idle",
  paused: "Paused",
  failed: "Failed",
  blocked: "Blocked",
  quiet: "Quiet hours",
};
// statusText is a worker status as the browser spells it.
export function statusText(status) {
  const key = normalized(status);
  if (statusWords[key]) return statusWords[key];
  const words = key.replaceAll("_", " ");
  return words ? words[0].toUpperCase() + words.slice(1) : "Paused";
}

// statusLight is the indicator a bot shows: active, waiting, blocked or quiet.
export function statusLight(status) {
  const key = normalized(status);
  if (key === "working" || key === "pausing") return "active";
  if (key === "failed" || key === "blocked") return "blocked";
  if (key === "quiet") return "quiet";
  return "waiting";
}

// taskRef is the short reference for a task: #12, PR 7 or a commit prefix.
export function taskRef(task) {
  const number = Number(task?.number) || 0;
  if (task?.kind === "pr" && number > 0) return `PR ${number}`;
  if (number > 0) return `#${number}`;
  if (task?.kind === "commit" && task.head) return String(task.head).slice(0, 7);
  return String(task?.kind || "task").toUpperCase();
}

// cargoRef reads a transfer's task ID the same way.
export function cargoRef(cargo) {
  const [kind, id] = String(cargo || "").split(":");
  if (kind === "pr" && id) return `PR ${id}`;
  if (kind === "issue" && id) return `#${id}`;
  if (kind === "commit" && id) return id.slice(0, 7);
  return String(cargo || "task").slice(0, 12);
}

// endpoint names one end of a transfer: the stage for a bot, else the party.
function endpoint(role) {
  return isBot(role) ? bots[role].label : botName(role);
}

// eventRoute is an activity line's route: a transfer names both ends; any
// other event names its kind.
export function eventRoute(event) {
  if (event?.kind === "delivery") return `${endpoint(event.from)} → ${endpoint(event.to)}`;
  const words = String(event?.kind || "event").replaceAll("_", " ");
  return words[0].toUpperCase() + words.slice(1);
}

// eventActor names the person whose action an event records: "You" for the
// viewer's own, the GitHub login for anyone else's, and "" when the event names
// no one, as for bots and for the local access key.
export function eventActor(event, me) {
  const actor = String(event?.actor || "");
  if (!actor) return "";
  return me && actor.toLowerCase() === String(me).toLowerCase() ? "You" : actor;
}

// pipeline is every bot in order with what it is doing now and its workload.
// Triage is measured in decisions: what waits on it is a decision.
export function pipeline(town) {
  return botOrder.map((role) => {
    const worker = projectWorker(town, role, town?.workers?.[role]);
    const counts = houseWorkload(town, role);
    const decisions = role === "hall"
      ? queueFor(town || {}, "hall").filter((task) => task.mayoral_decision === "pending" && !task.blocked).length
      : 0;
    return {
      role,
      ...bots[role],
      bot: botNames[role],
      worker,
      counts,
      decisions,
      light: statusLight(worker.status),
      status: statusText(worker.status),
    };
  });
}
