import test, { beforeEach, afterEach } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { management } from "./manage.js";

// A small DOM fixture exercises the actual form handlers and async API boundary.
// IDs and form membership come from the shipped page to catch markup drift.
class Element {
  constructor(tagName, value = "") {
    this.tagName = tagName;
    this.value = value;
    this.children = [];
    this.listeners = {};
    this.disabled = false;
    this.hidden = false;
    this.textContent = "";
    this.open = false;
  }
  append(...children) { this.children.push(...children); }
  replaceChildren(...children) { this.children = children; }
  get options() { return this.children.flatMap((c) => c.tagName === "optgroup" ? c.children : [c]); }
  addEventListener(name, callback) { (this.listeners[name] ||= []).push(callback); }
  dispatchEvent(event) { this.listeners[event.type]?.forEach((fn) => fn(event)); }
  showModal() { this.open = true; }
  close() { this.open = false; this.dispatchEvent({ type: "close" }); }
  querySelectorAll(selector) { return this.children.filter((e) => selector.split(",").includes(e.tagName)); }
  reset() { this.children.forEach((e) => { e.value = ""; }); }
}
const html = readFileSync(new URL("./index.html", import.meta.url), "utf8");
let elements;
beforeEach(() => {
  elements = Object.fromEntries([...html.matchAll(/<([a-z][a-z0-9]*)\b[^>]*\bid="([^"]+)"[^>]*>/gs)]
    .map(([, tag, id]) => [id, new Element(tag)]));
  for (const [, id, body] of html.matchAll(/<form id="([^"]+)">([\s\S]*?)<\/form>/g)) {
    elements[id].children = [...body.matchAll(/\bid="([^"]+)"/g)].map(([, child]) => elements[child]);
  }
  for (const role of ["", "bug", "feature", "issue", "review", "release", "simplifier", "repo"])
    elements["agent-role"].append(new Element("option", role));
  const document = new Element("document");
  document.querySelector = (selector) => {
    assert.ok(elements[selector.slice(1)], `Missing page element: ${selector}`);
    return elements[selector.slice(1)];
  };
  document.querySelectorAll = () => [];
  document.createElement = (tag) => new Element(tag);
  globalThis.document = document;
  globalThis.Option = class extends Element {
    constructor(text, value) { super("option", value); this.textContent = text; }
  };
});
afterEach(() => {
  delete globalThis.document;
  delete globalThis.Option;
});
const tick = () => new Promise((resolve) => setImmediate(resolve));
const catalog = {
  demo: true,
  agents: ["codex-acp", "claude-code", "opencode"].map((id) => ({
    id, name: id, source: "registry", available: true, version: "2.0",
  })),
};
function fixture(extraAPI, executionOptions = { configured: false, targets: [], profiles: [] }) {
  const town = {
    id: "acme/project",
    config: {
      repo: "acme/project", harness: "codex-acp", model: "default-model",
      effort: "medium", harness_version: "1.0", bot_agents: {}, merge_policy: "bot", simplifier_mode: "suggest",
    },
  };
  const overrides = {
    review: { harness: "claude-code", model: "review-model", effort: "high", harness_version: "1.5" },
  };
  const syncProfiles = () => {
    for (const role of ["bug", "feature", "issue", "review", "release", "simplifier", "repo"])
      town.config.bot_agents[role] = overrides[role]
        ? { ...overrides[role], inherited: false }
        : { ...town.config, bot_agents: undefined, inherited: true };
  };
  syncProfiles();
  const calls = [];
  const api = async (url, body, signal) => {
    calls.push({ url, body, signal });
    if (url === "/api/harnesses") return catalog;
    if (url === "/api/execution-options") return executionOptions;
    if (url === "/api/execution") {
      if (extraAPI) await extraAPI(url, body, signal);
      if (!body.role) town.config.execution = body.selection;
      else {
        town.config.bot_execution ||= {};
        if (body.selection === null) delete town.config.bot_execution[body.role];
        else town.config.bot_execution[body.role] = body.selection;
      }
      return {};
    }
    if (url === "/api/settings") {
      if (extraAPI) await extraAPI(url, body, signal);
      const { role, agent, merge_policy, simplifier_mode } = body;
      if (merge_policy) town.config.merge_policy = merge_policy;
      if (simplifier_mode) town.config.simplifier_mode = simplifier_mode;
      if (agent.inherit) delete overrides[role];
      else {
        const target = role ? (overrides[role] ||= {}) : town.config;
        Object.assign(target, agent, { harness_version: agent.version || target.harness_version });
      }
      syncProfiles();
      return {};
    }
    if (extraAPI) return extraAPI(url, body, signal);
    return { models: [], efforts: [] };
  };
  management({ api, getTown: () => town, getState: () => ({ towns: { [town.id]: town } }), refresh: async () => {} });
  return { town, calls, saves: () => calls.filter((c) => c.url === "/api/settings") };
}
async function open(role = "") {
  document.dispatchEvent({ type: "open-settings", detail: { role } });
  await tick();
}
function select(role) {
  elements["agent-role"].value = role;
  elements["agent-role"].onchange();
}
function edit(id, value, event = "oninput") {
  elements[id].value = value;
  elements[id][event]();
}
async function save() {
  await elements["settings-form"].onsubmit({
    preventDefault() {}, target: elements["settings-form"], submitter: elements["save-agent"],
  });
}

const executionCatalog = {
  configured: true, stale: false, fetched: "2026-09-26T12:00:00Z",
  targets: [
    { id: "localhost", kind: "local-bare", availability: "ready" },
    { id: "builder", kind: "ssh-bare", availability: "unavailable", unavailable_reason: "Start the build host." },
  ],
  profiles: [{ id: "coder", harness: "codex" }],
  default: { target_id: "localhost", profile_id: "coder" },
};

test("execution selection stores IDs independently of unsaved agent edits", async () => {
  const app = fixture(null, executionCatalog);
  await open("review");
  edit("model-input", "unsaved-model");
  edit("execution-mode", "mjolnir", "onchange");
  edit("execution-target", "builder", "onchange");
  assert.match(elements["execution-detail"].textContent, /Start the build host/);
  assert.match(elements["execution-note"].textContent, /holds agent work/);
  assert.equal(elements["execution-profile-field"].hidden, true);
  await elements["save-execution"].onclick();
  const call = app.calls.find((c) => c.url === "/api/execution");
  assert.deepEqual(call.body, { town: app.town.id, role: "review", selection: { target_id: "builder", profile_id: "coder" } });
  assert.equal(elements["model-input"].value, "unsaved-model");
  assert.equal(app.saves().length, 0);
  await save();
  assert.equal(app.town.config.bot_execution.review.target_id, "builder");
  edit("execution-mode", "local", "onchange");
  await elements["save-execution"].onclick();
  assert.deepEqual(app.town.config.bot_execution.review, { target_id: "", profile_id: "" });
  edit("execution-mode", "inherit", "onchange");
  await elements["save-execution"].onclick();
  assert.equal(app.town.config.bot_execution.review, undefined);
  assert.equal(app.town.config.bot_agents.review.model, "unsaved-model");
});

test("one local target and one profile add no picker noise", async () => {
  fixture(null, { ...executionCatalog, targets: [executionCatalog.targets[0]] });
  await open();
  assert.equal(elements["execution-settings"].hidden, true);
});

test("stale and missing saved targets remain visible with an actionable error", async () => {
  const app = fixture(null, { ...executionCatalog, stale: true, error: "Cannot reach Mjolnir; start its daemon.", targets: [] });
  app.town.config.execution = { target_id: "missing", profile_id: "coder" };
  await open("issue");
  assert.equal(elements["execution-settings"].hidden, false);
  assert.match(elements["execution-status"].textContent, /Cannot reach Mjolnir/);
  assert.equal(elements["execution-target"].value, "missing");
  assert.match(elements["execution-detail"].textContent, /missing from catalog/);
  assert.equal(elements["execution-mode"].value, "inherit");
  assert.equal(app.calls.some((c) => c.url === "/api/execution"), false);
});

test("an execution save from an old role cannot overwrite the current form", async () => {
  let finish;
  fixture((url) => url === "/api/execution" ? new Promise((resolve) => { finish = resolve; }) : undefined, executionCatalog);
  await open("review");
  edit("execution-mode", "mjolnir", "onchange");
  const pending = elements["save-execution"].onclick();
  select("issue");
  finish();
  await pending;
  assert.equal(elements["execution-mode"].value, "inherit");
  assert.equal(elements["execution-result"].textContent, "");
});

test("bot drafts keep independent harnesses, models, effort and pinned versions", async () => {
  const app = fixture();
  await open("review");
  assert.equal(elements["harness-input"].value, "claude-code");
  assert.match(elements["agent-profile-status"].textContent, /Custom/);
  edit("model-input", "next-review-model");
  edit("effort-input", "xhigh");
  select("issue");
  assert.equal(elements["harness-input"].value, "codex-acp");
  assert.match(elements["agent-profile-status"].textContent, /Uses town defaults/);
  edit("model-input", "issue-model");
  edit("effort-input", "xhigh");
  select("review");
  assert.equal(elements["model-input"].value, "next-review-model");
  assert.equal(elements["effort-input"].value, "xhigh");
  await save();
  assert.deepEqual(app.saves()[0].body, {
    town: "acme/project", role: "review",
    agent: { harness: "claude-code", model: "next-review-model", effort: "xhigh", version: "1.5" },
    merge_policy: "bot",
    simplifier_mode: "suggest",
    review_close_severity: "P2",
    quiet_hours: { windows: null },
  });
  assert.equal(elements["settings-dialog"].open, true);
  assert.match(elements["settings-success"].textContent, /Review Bot saved/);
  select("issue");
  assert.equal(elements["model-input"].value, "issue-model");
  assert.match(elements["agent-profile-status"].textContent, /unsaved/);
  await save();
  assert.equal(app.saves()[1].body.role, "issue");
  assert.equal(app.saves()[1].body.agent.harness, "codex-acp");
  assert.equal(app.saves()[1].body.agent.version, "1.0");
});


test("quiet hours follow the default, opt out, or save the town's own windows", async () => {
  const app = fixture();
  await open();
  assert.equal(elements["settings-quiet-mode"].value, "default");
  assert.equal(elements["settings-quiet-hours"].hidden, true);
  edit("settings-quiet-mode", "own", "onchange");
  assert.equal(elements["settings-quiet-hours"].hidden, false);
  elements["settings-quiet-hours"].value = "weekdays 18:00-08:00; weekends 00:00-24:00";
  await save();
  assert.deepEqual(app.saves()[0].body.quiet_hours, {
    windows: [
      { days: ["mon", "tue", "wed", "thu", "fri"], start: "18:00", end: "08:00" },
      { days: ["sat", "sun"], start: "00:00", end: "24:00" },
    ],
  });
  elements["settings-quiet-hours"].value = "mon 25:00-01:00";
  await save();
  assert.equal(app.saves().length, 1, "an invalid window never reaches the service");
  assert.match(elements["settings-error"].textContent, /start "25:00" must be HH:MM/);
  edit("settings-quiet-mode", "none", "onchange");
  await save();
  assert.deepEqual(app.saves()[1].body.quiet_hours, { windows: [] });
  // A town that saved its own windows reopens showing them.
  app.town.config.quiet_hours = [{ days: ["sat", "sun"], start: "00:00", end: "24:00" }];
  await open();
  assert.equal(elements["settings-quiet-mode"].value, "own");
  assert.equal(elements["settings-quiet-hours"].value, "sat,sun 00:00-24:00");
});

test("Repo Bot exposes and saves its repair agent profile", async () => {
  const app = fixture();
  await open("repo");
  assert.match(elements["agent-profile-status"].textContent, /Uses town defaults/);
  assert.match(elements["agent-profile-note"].textContent, /repairing a failing branch/);
  edit("model-input", "repo-repair-model");
  edit("effort-input", "high");
  await save();
  assert.equal(app.saves()[0].body.role, "repo");
  assert.equal(app.saves()[0].body.agent.model, "repo-repair-model");
  assert.equal(app.saves()[0].body.agent.effort, "high");
});


test("restoring defaults waits for save and follows subsequent town default changes", async () => {
  const app = fixture();
  await open("review");
  elements["inherit-agent"].onclick();
  assert.equal(app.saves().length, 0);
  assert.equal(elements["harness-input"].value, "codex-acp");
  assert.match(elements["agent-profile-status"].textContent, /unsaved/);
  for (const id of ["harness-input", "model-input", "effort-input", "command-input", "update-harness", "load-choices"])
    assert.equal(elements[id].disabled, true, `${id} waits for reset to save`);
  assert.match(elements["agent-profile-note"].textContent, /Save to use town defaults before customizing/);
  select("issue");
  assert.equal(elements["model-input"].disabled, false);
  select("review");
  assert.equal(elements["model-input"].disabled, true);
  await save();
  assert.deepEqual(app.saves()[0].body.agent, { inherit: true });
  assert.equal(elements["model-input"].disabled, false);
  select("");
  edit("model-input", "new-default");
  await save();
  assert.equal(app.saves()[1].body.role, "");
  select("review");
  assert.equal(elements["model-input"].value, "new-default");
  assert.match(elements["agent-profile-status"].textContent, /Uses town defaults/);
  await save();
  assert.deepEqual(app.saves()[2].body.agent, { inherit: true });
});

test("choices are scoped to the selected bot and stale choices cannot cross profiles", async () => {
  const pending = [];
  const app = fixture((url, body, signal) => new Promise((resolve) => pending.push({ body, signal, resolve })));
  await open("review");
  const first = elements["load-choices"].onclick();
  assert.equal(pending[0].body.role, "review");
  select("release");
  assert.equal(pending[0].signal.aborted, true);
  edit("harness-input", "opencode", "onchange");
  const second = elements["load-choices"].onclick();
  assert.equal(pending[1].body.role, "release");
  assert.equal(pending[1].body.agent.harness, "opencode");
  pending[1].resolve({ models: [{ value: "provider/release-model", name: "Release model" }], efforts: [] });
  await second;
  pending[0].resolve({ models: [{ value: "wrong-review-model", name: "Wrong model" }], efforts: [] });
  await first;
  assert.deepEqual(elements["model-choices"].children.map((o) => o.value), ["provider/release-model"]);
  assert.equal(app.saves().length, 0);
});

test("invalid custom command survives switching and prevents only its profile save", async () => {
  const app = fixture();
  await open("release");
  edit("harness-input", "custom", "onchange");
  edit("command-input", "[broken command");
  select("issue");
  edit("model-input", "issue-model");
  await save();
  select("release");
  assert.equal(elements["command-input"].value, "[broken command");
  await save();
  assert.equal(app.saves().length, 1);
  assert.equal(app.saves()[0].body.role, "issue");
  assert.ok(elements["settings-error"].textContent);
  assert.equal(elements["save-agent"].disabled, false);
  elements["settings-dialog"].close();
  await open("release");
  assert.equal(elements["command-input"].value, "");
  assert.equal(elements["harness-input"].value, "codex-acp");
});

test("saving during choice discovery cancels it and leaves discovery available", async () => {
  let resolveChoices, signal;
  fixture((url, body, nextSignal) => {
    if (url !== "/api/choices") return;
    signal = nextSignal;
    return new Promise((resolve) => { resolveChoices = resolve; });
  });
  await open("review");
  const choices = elements["load-choices"].onclick();
  assert.equal(elements["load-choices"].disabled, true);
  await save();
  assert.equal(signal.aborted, true);
  assert.equal(elements["load-choices"].disabled, false);
  resolveChoices({ models: [{ value: "stale", name: "Stale" }], efforts: [] });
  await choices;
  assert.equal(elements["model-choices"].children.length, 0);
  assert.equal(elements["load-choices"].disabled, false);
});

test("a failed save from a closed dialog cannot affect a reopened profile", async () => {
  let rejectSave;
  fixture((url) => {
    if (url === "/api/settings") return new Promise((_, reject) => { rejectSave = reject; });
  });
  await open("review");
  const saving = save();
  assert.equal(elements["save-agent"].disabled, true);
  elements["settings-dialog"].close();
  await open("issue");
  assert.equal(elements["save-agent"].disabled, false);
  rejectSave(new Error("Review save failed"));
  await saving;
  assert.equal(elements["agent-role"].value, "issue");
  assert.equal(elements["settings-error"].textContent, "");
  assert.equal(elements["model-input"].disabled, false);
});

test("a receipt check stays disabled across redraws and is released when it times out", async () => {
  const deadlines = [];
  const timeout = AbortSignal.timeout;
  AbortSignal.timeout = (ms) => {
    const controller = new AbortController();
    deadlines.push({ ms, controller });
    return controller.signal;
  };
  try {
    const checks = [];
    const { town } = fixture(async (url, body, signal) => {
      if (url !== "/api/requests/check") return {};
      checks.push({ body, signal });
      return new Promise((_, reject) => signal.addEventListener("abort", () => reject(new Error("no answer"))));
    });
    town.requests = { r1: { id: "r1", title: "Flaky export", kind: "bug", status: "uncertain", created: "2026-09-01T00:00:00Z" } };
    // The page's request history holds one button per uncertain submission.
    const history = elements["request-history"];
    Object.defineProperty(history, "innerHTML", {
      set(html) {
        this._html = html;
        this.children = [...html.matchAll(/data-recheck="([^"]+)"([^>]*)>/g)].map(([, id, rest]) =>
          Object.assign(new Element("button"), { dataset: { recheck: id }, disabled: /\bdisabled\b/.test(rest) }));
      },
      get() { return this._html; },
    });
    history.querySelectorAll = () => history.children;
    elements["new-request"].onclick();
    history.children[0].onclick();
    assert.equal(checks.length, 1);
    assert.equal(deadlines.at(-1).ms, 30000, "the check has a deadline");
    assert.equal(checks[0].signal, deadlines.at(-1).controller.signal);
    assert.equal(history.children[0].disabled, true, "the redrawn button stays disabled while the check is out");
    history.children[0].onclick();
    assert.equal(checks.length, 1, "a second press sends nothing");
    deadlines.at(-1).controller.abort();
    await tick();
    assert.equal(history.children[0].disabled, false, "the deadline releases the button");
    assert.equal(elements["request-error"].textContent, "no answer");
  } finally {
    AbortSignal.timeout = timeout;
  }
});
