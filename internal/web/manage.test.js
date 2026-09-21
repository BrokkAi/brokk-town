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
  elements = Object.fromEntries([...html.matchAll(/<([a-z]+)\b[^>]*\bid="([^"]+)"[^>]*>/gs)]
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
function fixture(extraAPI) {
  const town = {
    id: "acme/project",
    config: {
      repo: "acme/project", harness: "codex-acp", model: "default-model",
      effort: "medium", harness_version: "1.0", bot_agents: {}, merge_policy: "bot", simplifier_mode: "suggest",
      bot_versions: { bug: "0.3.1", feature: "0.1.1", issue: "0.5.2", review: "0.2.1", release: "0.5.1", simplifier: "0.1.0", repo: "0.1.0" },
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
    if (url === "/api/settings") {
      if (extraAPI) await extraAPI(url, body, signal);
      const { role, agent, merge_policy, simplifier_mode, auto_update_bots, auto_mayor, bot_version } = body;
      if (merge_policy) town.config.merge_policy = merge_policy;
      if (auto_mayor !== undefined) town.config.auto_mayor = auto_mayor;
      if (simplifier_mode) town.config.simplifier_mode = simplifier_mode;
      if (auto_update_bots !== undefined) town.config.auto_update_bots = auto_update_bots;
      if (bot_version) town.config.bot_versions[role] = bot_version;
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
    auto_update_bots: false,
    auto_mayor: false,
    bot_version: "0.2.1",
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

test("bot updates are explicit and save a new exact pin", async () => {
  const app = fixture((url) => {
    if (url === "/api/bot-versions") return { issue: "0.6.0" };
  });
  await open("issue");
  assert.match(elements["bot-version-status"].textContent, /0\.5\.2/);
  await elements["check-bot-version"].onclick();
  assert.match(elements["bot-version-status"].textContent, /latest stable is 0\.6\.0/);
  elements["use-bot-version"].onclick();
  assert.match(elements["bot-version-status"].textContent, /will be pinned to 0\.6\.0/);
  await save();
  assert.equal(app.saves()[0].body.bot_version, "0.6.0");
  assert.equal(app.town.config.bot_versions.issue, "0.6.0");
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

test("automatic bot updates are off until the Mayor saves the town setting", async () => {
  const app = fixture();
  await open();
  assert.equal(elements["settings-auto-update-bots"].checked, false);
  elements["settings-auto-update-bots"].checked = true;
  await save();
  assert.equal(app.saves()[0].body.auto_update_bots, true);
  assert.equal(app.town.config.auto_update_bots, true);
  await open();
  assert.equal(elements["settings-auto-update-bots"].checked, true, "the saved setting is shown on reopen");
});

test("Auto-Mayor is off until the Mayor saves the town setting", async () => {
  const app = fixture();
  await open();
  assert.equal(elements["settings-auto-mayor"].checked, false);
  elements["settings-auto-mayor"].checked = true;
  await save();
  assert.equal(app.saves()[0].body.auto_mayor, true);
  assert.equal(app.town.config.auto_mayor, true);
  await open();
  assert.equal(elements["settings-auto-mayor"].checked, true, "the saved setting is shown on reopen");
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
