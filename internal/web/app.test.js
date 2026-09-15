import test from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";

// This is intentionally a small browser shim rather than a second renderer.
// app.js is imported unchanged, so these checks exercise its real event
// handlers and redraw path without adding a DOM dependency to the project.
class Element {
  constructor(tagName = "div", id = "") {
    this.tagName = tagName.toUpperCase();
    this.id = id;
    this.value = "";
    this.children = [];
    this.listeners = {};
    this.dataset = {};
    this.className = "";
    this.classList = {
      add: (...names) => {
        const classes = this._classes();
        names.forEach((name) => classes.add(name));
        this.className = [...classes].join(" ");
      },
      remove: (...names) => {
        const classes = this._classes();
        names.forEach((name) => classes.delete(name));
        this.className = [...classes].join(" ");
      },
      toggle: (name, force) => {
        const classes = this._classes();
        const next = force === undefined ? !classes.has(name) : force;
        next ? classes.add(name) : classes.delete(name);
        this.className = [...classes].join(" ");
        return next;
      },
      contains: (name) => this._classes().has(name),
    };
    this.hidden = false;
    this.disabled = false;
    this.open = false;
    this.textContent = "";
    this.innerText = "";
    this.onclick = null;
  }
  _classes() {
    return new Set(this.className.split(/\s+/).filter(Boolean));
  }
  setAttribute(name, value) {
    if (name === "class") this.className = String(value);
    else if (name === "id") this.id = String(value);
    else this[name] = String(value);
  }
  getAttribute(name) {
    if (name === "class") return this.className;
    if (name === "id") return this.id;
    return this[name];
  }
  append(...children) {
    this.children.push(...children);
  }
  replaceChildren(...children) {
    this.children = children;
  }
  addEventListener(name, callback) {
    (this.listeners[name] ||= []).push(callback);
  }
  dispatchEvent(event) {
    event.target ||= this;
    for (const callback of this.listeners[event.type] || []) callback(event);
    this[`on${event.type}`]?.(event);
  }
  click() {
    this.dispatchEvent({ type: "click" });
  }
  showModal() {
    this.open = true;
  }
  close() {
    this.open = false;
    this.dispatchEvent({ type: "close" });
  }
  focus() {
    globalThis.document.activeElement = this;
  }
  reset() {
    for (const child of this.children) child.value = "";
  }
  matches(selector) {
    return selector.split(",").some((part) => {
      part = part.trim();
      if (part === "input" || part === "select" || part === "textarea")
        return this.tagName.toLowerCase() === part;
      if (part.startsWith("#")) return this.id === part.slice(1);
      if (part.startsWith(".")) return this._classes().has(part.slice(1));
      return false;
    });
  }
  closest(selector) {
    return this._surface && selector.split(",").some((part) => part.trim() === `#${this._surface.id}`)
      ? this._surface
      : null;
  }
  querySelector(selector) {
    if (selector.startsWith("#") && this._html) {
      const id = selector.slice(1);
      const match = this._html.match(new RegExp(`<([a-z]+)[^>]*\\bid="${id}"[^>]*>([\\s\\S]*?)</\\1>`, "i"));
      if (match) {
        const child = new Element(match[1], id);
        child.textContent = match[2].replace(/<[^>]*>/g, "").trim();
        return child;
      }
    }
    return this.querySelectorAll(selector)[0] || null;
  }
  querySelectorAll(selector) {
    const selectors = selector.split(",").map((part) => part.trim());
    const all = [];
    const visit = (element) => {
      for (const child of element.children) {
        if (selectors.some((part) => matches(child, part))) all.push(child);
        visit(child);
      }
    };
    visit(this);
    return all;
  }
  set innerHTML(html) {
    this._html = html;
    this.textContent = String(html).replace(/<[^>]*>/g, "").replace(/&[a-z]+;/g, " ");
    this.children = [];
    const tags = /<button\b([^>]*)>([\s\S]*?)<\/button>/gi;
    for (const match of html.matchAll(tags)) {
      const button = new Element("button");
      const attrs = match[1];
      for (const attr of attrs.matchAll(/([\w-]+)="([^"]*)"/g)) {
        const [, name, value] = attr;
        if (name === "class") button.className = value;
        else if (name === "id") button.id = value;
        else if (name.startsWith("data-")) button.dataset[name.slice(5).replaceAll(/-([a-z])/g, (_, c) => c.toUpperCase())] = value;
      }
      button.textContent = match[2].replace(/<[^>]*>/g, "").trim();
      button._surface = this;
      this.children.push(button);
    }
    // Board list containers are the only non-button descendants needed by
    // this shim. Keep them shallow: app.js only needs their identity, focus,
    // and scroll offsets to exercise redraw preservation.
    const lists = /<div\b([^>]*\bdata-board-list="[^"]+"[^>]*)>[\s\S]*?<\/div>/gi;
    for (const match of html.matchAll(lists)) {
      const list = new Element("div");
      for (const attr of match[1].matchAll(/([\w-]+)="([^"]*)"/g)) {
        const [, name, value] = attr;
        if (name === "class") list.className = value;
        else if (name === "id") list.id = value;
        else if (name.startsWith("data-")) list.dataset[name.slice(5).replaceAll(/-([a-z])/g, (_, c) => c.toUpperCase())] = value;
      }
      list._surface = this;
      this.children.push(list);
    }
  }
  get innerHTML() {
    return this._html || "";
  }
  get options() {
    return this.children;
  }
  getContext() {
    return {
      drawImage() {}, fill() {}, beginPath() {}, ellipse() {}, fillRect() {},
      fillText() {},
    };
  }
}

function matches(element, selector) {
  if (selector === "button") return element.tagName === "BUTTON";
  if (selector.startsWith("#")) return element.id === selector.slice(1);
  if (selector.startsWith(".")) return element._classes().has(selector.slice(1));
  const attr = selector.match(/^\[data-([\w-]+)(?:="([^"]*)")?\]$/);
  if (attr) {
    const key = attr[1].replaceAll(/-([a-z])/g, (_, c) => c.toUpperCase());
    return Object.hasOwn(element.dataset, key) && (!attr[2] || element.dataset[key] === attr[2]);
  }
  return element.matches(selector);
}

function installFixture() {
  const html = readFileSync(new URL("./index.html", import.meta.url), "utf8");
  const elements = {};
  for (const [, id] of html.matchAll(/\bid="([^"]+)"/g)) {
    const element = new Element("div", id);
    const at = html.indexOf(`id="${id}"`);
    const opening = html.slice(html.lastIndexOf("<", at), html.indexOf(">", at) + 1);
    element.tagName = opening.match(/^<([a-z]+)/i)?.[1]?.toUpperCase() || "DIV";
    element.className = opening.match(/class="([^"]*)"/)?.[1] || "";
    elements[id] = element;
  }
  // The inspector heading spans a line break in the source HTML; retain it as
  // a concrete node even if a future fixture parser is stricter.
  elements["inspector-town"] ||= new Element("span", "inspector-town");
  // Static view buttons are the only controls not represented by an ID.
  const viewSwitcher = elements["view-switcher"];
  for (const mode of ["town", "board", "compact"]) {
    const button = new Element("button");
    button.dataset.view = mode;
    button._surface = viewSwitcher;
    viewSwitcher.children.push(button);
  }
  for (const [, id, body] of html.matchAll(/<form id="([^"]+)">([\s\S]*?)<\/form>/g)) {
    elements[id].children = [...body.matchAll(/\bid="([^"]+)"/g)].map(([, child]) => elements[child]).filter(Boolean);
  }
  for (const role of ["", "bug", "feature", "issue", "review", "release"])
    elements["agent-role"].append(new Element("option", role));
  const document = new Element("document");
  document.hidden = false;
  document.activeElement = document;
  document.querySelector = (selector) => {
    if (selector.startsWith("#")) {
      const existing = elements[selector.slice(1)];
      if (existing) return existing;
      return Object.values(elements).map((element) => element.querySelector(selector)).find(Boolean) || null;
    }
    if (selector === ".world") return elements.world;
    if (selector === ".activity") return elements.journal?.parent || elements.activity || (elements.journal && (elements.journal.parent = new Element("section", "activity")));
    if (selector === ".town-controls") return elements["town-toggle"]?.parent || elements["town-toggle"];
    return Object.values(elements).find((element) => element.matches(selector)) || Object.values(elements).map((element) => element.querySelector(selector)).find(Boolean) || null;
  };
  document.querySelectorAll = (selector) => {
    if (selector === "#view-switcher [data-view]") return viewSwitcher.children;
    if (selector === "[data-close]") return [];
    return Object.values(elements).filter((element) => matches(element, selector));
  };
  document.createElement = (tag) => new Element(tag);
  document.addEventListener = (name, callback) => (document.listeners[name] ||= []).push(callback);
  document.dispatchEvent = (event) => {
    for (const callback of document.listeners[event.type] || []) callback(event);
  };
  globalThis.document = document;
  globalThis.Option = class extends Element {
    constructor(text, value) { super("option", value); this.textContent = text; }
  };
  globalThis.Image = class { constructor() { this.complete = true; this.naturalWidth = 1; } };
  globalThis.matchMedia = () => ({ matches: false, addEventListener() {} });
  globalThis.requestAnimationFrame = () => 0;
  globalThis.setInterval = () => 0;
  globalThis.setTimeout = () => 0;
  globalThis.localStorage = { getItem: () => null, setItem() {} };
  globalThis.sessionStorage = { getItem: () => null, setItem() {} };
  globalThis.location = { hash: "#token=test-key", pathname: "/" };
  globalThis.history = { replaceState() {} };
  globalThis.confirm = () => true;
  return elements;
}

const state = {
  seq: 1,
  demo: true,
  version: "v0.1.2",
  capacity: { active: 1, limit: 4 },
  service_config: { max_workers: 4 },
  update: { current: "1.0.0", latest: "1.1.0", command: "npm install -g @brokkai/brokk-town@1.1.0" },
  towns: {
    "acme/project": {
      id: "acme/project",
      config: { repo: "acme/project", branch: "main", bot_agents: {}, bot_versions: { bug: "0.3.1", feature: "0.1.1", issue: "0.5.2", review: "0.2.1", release: "0.5.1" }, harness: "codex-acp", model: "m", effort: "medium" },
      workers: {
        issue: { role: "issue", status: "working", enabled: true, task: "Implementing", logs: [], agent: { harness: "codex-acp", model: "m", effort: "medium" } },
        review: { role: "review", status: "waiting", enabled: true, next: "0001-01-01T00:00:00Z", logs: [] },
        repo: { role: "repo", status: "paused", enabled: true, logs: [] },
      },
      tasks: {
        "pr:1": { id: "pr:1", kind: "pr", number: 1, title: "Review this change", house: "review", stage: "awaiting_author" },
        "issue:2": { id: "issue:2", kind: "issue", number: 2, title: "Queue this change", house: "issue", stage: "queued" },
        "source:done": { id: "source:done", kind: "source", title: "Checked off in Slack", house: "issue", stage: "complete", source: { eligible: false } },
        "issue:3": { id: "issue:3", kind: "issue", number: 3, title: "Outside request", house: "hall", stage: "awaiting_mayor", external: true, mayoral_decision: "pending" },
        "upgrade:feature": { id: "upgrade:feature", kind: "upgrade", title: "Feature Bot 0.1.2 is available (pinned 0.1.1)", house: "hall", stage: "awaiting_mayor", mayoral_decision: "pending", upgrade: { role: "feature", from: "0.1.1", to: "0.1.2" } },
      },
      intents: {}, reports: [], events: [],
    },
  },
  events: [],
};

test("app handlers render views, inspect work, preserve focused capacity input, and retain keyboard control", async () => {
  const elements = installFixture();
  const requests = [];
  const createdLinks = [];
  let releaseSecond, releaseThird;
  const messages = [
    `data: ${JSON.stringify(state)}\n\n`,
    new Promise((resolve) => { releaseSecond = () => resolve(`data: ${JSON.stringify({ ...state, seq: 2, capacity: { active: 2, limit: 7 } })}\n\n`); }),
    new Promise((resolve) => { releaseThird = () => resolve(`data: ${JSON.stringify({ ...state, seq: 3, capacity: { active: 3, limit: 8 } })}\n\n`); }),
  ];
  document.createElement = (tag) => {
    const element = new Element(tag);
    if (tag === "a") createdLinks.push(element);
    return element;
  };
  globalThis.URL.createObjectURL = () => "blob:outcomes";
  globalThis.URL.revokeObjectURL = () => {};
  globalThis.fetch = async (url, options = {}) => {
    requests.push({ url, options });
    if (url === "/api/events") {
      let index = 0;
      return { ok: true, body: { getReader: () => ({ read: async () => index < messages.length ? { value: new TextEncoder().encode(await messages[index++]), done: false } : { done: true } }) } };
    }
    if (url === "/api/capacity") return { ok: true, json: async () => state.capacity };
    if (url === "/api/update") return { ok: true, json: async () => ({ ok: true, restart_required: true }) };
    if (url === "/api/state") return { ok: true, json: async () => state };
    if (url === "/api/harnesses") return { ok: true, json: async () => ({ demo: true, agents: [] }) };
    if (url.startsWith("/api/outcomes")) return { ok: true, blob: async () => ({}) };
    return { ok: true, json: async () => ({}) };
  };
  await import(`./app.js?dom-test=${Date.now()}`);
  for (let i = 0; i < 5; i++) await new Promise((resolve) => setImmediate(resolve));
  assert.equal(elements["capacity-summary"].textContent, "1/4 workers");
  assert.equal(elements["town-version"].textContent, "v0.1.2");
  assert.equal(elements["help-version"].textContent, "Brokk Town v0.1.2");
  assert.equal(elements["update-notice"].textContent, "Upgrade Town to 1.1.0");
  await elements["update-notice"].onclick();
  assert.equal(requests.some((request) => request.url === "/api/update"), true);
  assert.match(elements["update-notice"].textContent, /installed/);

  const views = document.querySelectorAll("#view-switcher [data-view]");
  views.find((button) => button.dataset.view === "board").onclick();
  assert.equal(elements.board.hidden, false);
  assert.equal(elements.world.hidden, true);
  assert.equal(elements.compact.hidden, true);
  const boardLists = elements.board.querySelectorAll("[data-board-list]");
  assert.equal(boardLists.length, 4, "board exposes each populated column as its own list");
  const queuedList = boardLists.find((list) => list.dataset.boardList.endsWith(":queued"));
  const reviewList = boardLists.find((list) => list.dataset.boardList.endsWith(":review"));
  queuedList.scrollTop = 113;
  reviewList.scrollTop = 29;
  queuedList.focus();
  releaseSecond();
  await new Promise((resolve) => setImmediate(resolve));
  const redrawnLists = elements.board.querySelectorAll("[data-board-list]");
  assert.equal(redrawnLists.find((list) => list.dataset.boardList.endsWith(":queued")).scrollTop, 113, "queued list scroll survives SSE redraw");
  assert.equal(redrawnLists.find((list) => list.dataset.boardList.endsWith(":review")).scrollTop, 29, "review list keeps its independent scroll position");
  assert.equal(document.activeElement.dataset.boardList, queuedList.dataset.boardList, "focused board list survives SSE redraw");
  const boardTask = elements.board
    .querySelectorAll("[data-board-task]")
    .find((task) => task.dataset.boardTask === "pr:1");
  assert.ok(boardTask, "board renders a task card from the snapshot");
  const doneTask = elements.board
    .querySelectorAll("[data-board-task]")
    .find((task) => task.dataset.boardTask === "source:done");
  assert.ok(doneTask, "board renders source-observed done work");
  assert.doesNotMatch(doneTask.textContent, /Next profile:/, "done work does not advertise another dispatch");
  boardTask.onclick();
  assert.equal(elements.inspector.classList.contains("open"), true);
  assert.equal(elements.inspector.hidden, false, "board inspection is visible");
  assert.match(elements.inspection.textContent, /Review this change/);

  views.find((button) => button.dataset.view === "compact").onclick();
  assert.equal(elements.compact.hidden, false);
  assert.equal(elements.board.hidden, true);
  document.dispatchEvent({ type: "keydown", key: "t", target: new Element("div") });
  assert.equal(elements.world.hidden, true, "Town view preserves all-town overview until a town is selected");
  document.dispatchEvent({ type: "keydown", key: "b", target: new Element("div") });
  assert.equal(elements.board.hidden, false, "keyboard shortcut opens Board");
  document.dispatchEvent({ type: "keydown", key: "c", target: new Element("div") });
  assert.equal(elements.compact.hidden, false, "keyboard shortcut opens Compact");

  elements.towns.querySelectorAll("[data-town]")[0].onclick();
  assert.equal(elements["town-state"].hidden, false, "header shows the town's wake state");
  assert.equal(elements["town-state"].textContent, "Awake · 2 agents");
  assert.equal(elements["town-toggle"].textContent, "Ⅱ Pause the town", "toggle offers the action that changes state");
  assert.equal(elements["town-toggle"].classList.contains("primary"), false);
  assert.equal(elements["pause-all"].hidden, true, "no separate pause-all when every agent is awake");
  elements.houses.querySelectorAll("[data-house]").find((button) => button.dataset.house === "issue").onclick();
  assert.match(elements.inspection.textContent, /Authority:.*create pull requests/, "house controls explain their write authority");
  elements.houses.querySelectorAll("[data-house]").find((button) => button.dataset.house === "hall").onclick();
  elements.inspection.querySelectorAll("[data-outcome-days]").find((button) => button.dataset.outcomeDays === "0").onclick();
  await elements.inspection.querySelectorAll("[data-export-outcomes]")[0].onclick();
  const outcomeExport = requests.find((request) => request.url.startsWith("/api/outcomes"));
  assert.equal(new URL(`https://town.local${outcomeExport.url}`).searchParams.get("from"), "1970-01-01T00:00:00.000Z", "All exports do not fall back to the server's seven-day default");
  assert.equal(createdLinks.at(-1).download, "acme-project-outcomes-all.csv");
  await elements["town-toggle"].onclick();
  assert.equal(requests.some((request) => request.url === "/api/control" && request.options.body.includes('"action":"pause"') && request.options.body.includes('"role":"all"')), true);
  elements.houses.querySelectorAll("[data-house]").find((button) => button.dataset.house === "hall").onclick();
  const decision = elements.inspection.querySelectorAll("[data-task]").find((button) => button.dataset.task === "issue:3");
  assert.ok(decision, "Town Hall shows work awaiting the Mayor");
  decision.onclick();
  assert.match(decision.textContent, /issue #3/, "Town Hall card carries the source number");
  assert.match(elements.inspection.textContent, /Issue #3/);
  assert.match(elements.inspection.textContent, /Issue Bot/, "decision names its destination");
  assert.match(elements.inspection.textContent, /Demo town/, "demo explains why no live body follows");
  assert.equal(requests.some((request) => request.url === "/api/task-detail"), false, "demo never fetches live details");
  await elements.inspection.querySelectorAll("button").find((button) => button.id === "admit-task").onclick();
  assert.equal(requests.some((request) => request.url === "/api/control" && request.options.body.includes('"action":"admit"')), true);
  elements.houses.querySelectorAll("[data-house]").find((button) => button.dataset.house === "hall").onclick();
  const upgrade = elements.inspection.querySelectorAll("[data-task]").find((button) => button.dataset.task === "upgrade:feature");
  assert.ok(upgrade, "Town Hall shows a bot update awaiting the Mayor");
  assert.match(upgrade.innerHTML || upgrade.textContent || "", /bot update/);
  upgrade.onclick();
  const upgradeButtons = elements.inspection.querySelectorAll("button");
  assert.ok(upgradeButtons.find((button) => button.id === "admit-task"), "a bot update can be approved");
  assert.ok(upgradeButtons.find((button) => button.id === "decline-task"), "a bot update can be declined");
  await upgradeButtons.find((button) => button.id === "delay-task").onclick();
  assert.equal(requests.some((request) => request.url === "/api/control" && request.options.body.includes('"action":"delay"') && request.options.body.includes('"task":"upgrade:feature"')), true);

  elements["capacity-settings"].onclick();
  assert.equal(elements["capacity-dialog"].open, true);
  elements["capacity-input"].focus();
  elements["capacity-input"].value = "9";
  releaseThird();
  await new Promise((resolve) => setImmediate(resolve));
  assert.equal(elements["capacity-input"].value, "9", "focused draft survives SSE redraw");
  await elements["capacity-form"].onsubmit({
    preventDefault() {},
    submitter: new Element("button"),
  });
  assert.equal(requests.some((request) => request.url === "/api/capacity"), true);
  assert.equal(elements["capacity-dialog"].open, false);
});

async function openMayoralDecision(taskDetail) {
  const elements = installFixture();
  const requests = [];
  const live = { ...state, seq: 1, demo: false };
  const message = `data: ${JSON.stringify(live)}\n\n`;
  let served = false;
  globalThis.fetch = async (url, options = {}) => {
    requests.push({ url, options });
    if (url === "/api/events") {
      return { ok: true, body: { getReader: () => ({ read: async () => !served ? (served = true, { value: new TextEncoder().encode(message), done: false }) : { done: true } }) } };
    }
    if (url === "/api/task-detail") return taskDetail(url, options);
    if (url === "/api/state") return { ok: true, json: async () => live };
    if (url === "/api/harnesses") return { ok: true, json: async () => ({ demo: false, agents: [] }) };
    return { ok: true, json: async () => ({}) };
  };
  await import(`./app.js?live-detail=${Date.now()}-${Math.random()}`);
  for (let i = 0; i < 10; i++) await new Promise((resolve) => setImmediate(resolve));
  elements.towns.querySelectorAll("[data-town]")[0].onclick();
  elements.houses.querySelectorAll("[data-house]").find((button) => button.dataset.house === "hall").onclick();
  elements.inspection.querySelectorAll("[data-task]").find((button) => button.dataset.task === "issue:3").onclick();
  for (let i = 0; i < 10; i++) await new Promise((resolve) => setImmediate(resolve));
  return { elements, requests };
}

test("mayoral inspection loads live GitHub details on open", async () => {
  const { elements, requests } = await openMayoralDecision(async () => ({
    ok: true,
    json: async () => ({
      kind: "issue", number: 3, title: "Outside request",
      body: "Here is why this matters.", author: "octo", comments: 4,
      state: "open", updated_at: "2026-09-01T10:00:00Z", url: "https://example.com/i/3",
    }),
  }));
  const detailCall = requests.find((request) => request.url === "/api/task-detail");
  assert.ok(detailCall, "opening the card fetches live details");
  assert.deepEqual(JSON.parse(detailCall.options.body), { town: "acme/project", task: "issue:3" });
  assert.match(elements.inspection.textContent, /Here is why this matters/);
  assert.match(elements.inspection.textContent, /octo/);
  assert.match(elements.inspection.textContent, /4 comments/);
  assert.match(elements.inspection.textContent, /Issue #3/);
  assert.match(elements.inspection.textContent, /Issue Bot/);
});

test("mayoral inspection stays decidable when live details fail", async () => {
  const { elements, requests } = await openMayoralDecision(async () => ({
    ok: false, json: async () => ({ error: "gh exploded" }),
  }));
  assert.ok(requests.some((request) => request.url === "/api/task-detail"));
  assert.match(elements.inspection.textContent, /Live details unavailable/);
  assert.match(elements.inspection.textContent, /gh exploded/);
  assert.match(elements.inspection.textContent, /Admit to town/, "the decision stays available");
});

test("an open decision keeps its markup across snapshots and puts the decision above the body", async () => {
  const elements = installFixture();
  const live = { ...state, seq: 1, demo: false };
  let releaseSecond;
  const messages = [
    `data: ${JSON.stringify(live)}\n\n`,
    new Promise((resolve) => {
      releaseSecond = () => resolve(`data: ${JSON.stringify({ ...live, seq: 2 })}\n\n`);
    }),
  ];
  globalThis.fetch = async (url) => {
    if (url === "/api/events") {
      let index = 0;
      return { ok: true, body: { getReader: () => ({ read: async () => index < messages.length ? { value: new TextEncoder().encode(await messages[index++]), done: false } : { done: true } }) } };
    }
    if (url === "/api/task-detail")
      return {
        ok: true,
        json: async () => ({
          kind: "issue", number: 3, title: "Outside request",
          body: "A long body the Mayor reads while the stream keeps ticking.",
          author: "octo", comments: 4, state: "open",
          updated_at: "2026-09-01T10:00:00Z", url: "https://example.com/i/3",
        }),
      };
    if (url === "/api/state") return { ok: true, json: async () => live };
    if (url === "/api/harnesses") return { ok: true, json: async () => ({ demo: false, agents: [] }) };
    return { ok: true, json: async () => ({}) };
  };
  await import(`./app.js?inspection-stability=${Date.now()}-${Math.random()}`);
  for (let i = 0; i < 10; i++) await new Promise((resolve) => setImmediate(resolve));
  elements.towns.querySelectorAll("[data-town]")[0].onclick();
  elements.houses.querySelectorAll("[data-house]").find((button) => button.dataset.house === "hall").onclick();
  elements.inspection.querySelectorAll("[data-task]").find((button) => button.dataset.task === "issue:3").onclick();
  for (let i = 0; i < 10; i++) await new Promise((resolve) => setImmediate(resolve));

  const html = elements.inspection.innerHTML;
  assert.ok(
    html.indexOf('class="inspector-actions"') < html.indexOf('class="task-detail"'),
    "the decision sits above a body that grows when live details arrive",
  );
  // The panel is only rebuilt when its content changes; an unchanged snapshot
  // must leave the reader's own DOM — and so their scroll position — in place.
  elements.inspection.innerHTML = `${html}<!--kept-->`;
  releaseSecond();
  for (let i = 0; i < 10; i++) await new Promise((resolve) => setImmediate(resolve));
  assert.ok(
    elements.inspection.innerHTML.includes("<!--kept-->"),
    "an identical snapshot leaves the open decision untouched",
  );
  assert.match(elements.inspection.textContent, /A long body the Mayor reads/);
});

test("the inbox lists every town's decisions, opens the right Town Hall, and decides in place", async () => {
  const elements = installFixture();
  const requests = [];
  const other = {
    id: "beta/tools",
    config: { repo: "beta/tools", branch: "main", bot_agents: {}, bot_versions: {}, harness: "codex-acp", model: "", effort: "" },
    workers: {
      review: { role: "review", status: "failed", enabled: true, error: "gh exploded", logs: [], updated: "2026-09-15T08:00:00Z" },
      repo: { role: "repo", status: "paused", enabled: true, logs: [] },
    },
    tasks: {
      "pr:12": { id: "pr:12", kind: "pr", number: 12, title: "Contributor PR for beta", house: "hall", stage: "awaiting_mayor", external: true, mayoral_decision: "pending", updated: "2026-09-10T08:00:00Z" },
    },
    intents: {}, reports: [], events: [],
  };
  const twoTowns = { ...state, seq: 1, towns: { ...state.towns, "beta/tools": other } };
  let served = false;
  globalThis.fetch = async (url, options = {}) => {
    requests.push({ url, options });
    if (url === "/api/events") {
      return { ok: true, body: { getReader: () => ({ read: async () => !served ? (served = true, { value: new TextEncoder().encode(`data: ${JSON.stringify(twoTowns)}\n\n`), done: false }) : { done: true } }) } };
    }
    if (url === "/api/state") return { ok: true, json: async () => twoTowns };
    if (url === "/api/harnesses") return { ok: true, json: async () => ({ demo: true, agents: [] }) };
    return { ok: true, json: async () => ({}) };
  };
  await import(`./app.js?inbox-test=${Date.now()}`);
  for (let i = 0; i < 10; i++) await new Promise((resolve) => setImmediate(resolve));

  assert.equal(elements["inbox-count"].textContent, "4", "header badge counts decisions and stuck work across towns");
  assert.equal(elements["inbox-count"].hidden, false);
  assert.ok(elements["inbox-count"].classList.contains("decisions"), "badge highlights pending Mayoral decisions");
  assert.match(elements["inbox-toggle"].getAttribute("aria-label"), /3 awaiting your decision · 1 need attention/);
  const betaLink = elements.towns.querySelectorAll("[data-town]").find((button) => button.dataset.town === "beta/tools");
  assert.match(betaLink.textContent, /1 to decide/, "sidebar shows each town's pending decisions");
  assert.match(betaLink.textContent, /1 attention/, "sidebar shows each town's stuck work");
  assert.match(elements.overview.textContent, /to decide/, "overview cards show decisions waiting");

  document.dispatchEvent({ type: "keydown", key: "i", target: new Element("div") });
  assert.equal(elements["inbox-dialog"].open, true, "keyboard shortcut opens the inbox");
  const opens = elements["inbox-list"].querySelectorAll("[data-inbox-open]");
  assert.deepEqual(
    opens.map((button) => [button.dataset.inboxTown, button.dataset.inboxHouse, button.dataset.inboxTask]),
    [["acme/project", "hall", "upgrade:feature"], ["acme/project", "hall", "issue:3"], ["beta/tools", "hall", "pr:12"], ["beta/tools", "review", ""]],
    "decisions from every town lead (an unknown age counts as the longest wait), then stuck work names the house to inspect",
  );
  assert.match(elements["inbox-list"].textContent, /outside arrival/);
  assert.match(elements["inbox-list"].textContent, /Review Bot/);
  assert.match(elements["inbox-list"].textContent, /gh exploded/, "failed workers carry their error");

  opens[2].onclick();
  assert.equal(elements["inbox-dialog"].open, false, "opening an item closes the inbox");
  assert.equal(elements.inspector.classList.contains("open"), true);
  assert.equal(elements.overview.hidden, true, "opening an item leaves the all-towns overview");
  assert.equal(elements["inspector-town"].textContent, "beta/tools", "the inspector switches to the item's town");
  assert.match(elements.inspection.textContent, /Contributor PR for beta/);
  assert.match(elements.inspection.textContent, /PR #12/);
  assert.ok(elements.inspection.querySelectorAll("button").find((button) => button.id === "admit-task"), "the decision is right there");

  elements["inbox-toggle"].onclick();
  const decline = elements["inbox-list"].querySelectorAll("[data-inbox-decide]").find((button) => button.dataset.inboxTask === "issue:3" && button.dataset.inboxDecide === "decline");
  assert.ok(decline, "decisions can be made from the inbox");
  await decline.onclick();
  const control = requests.find((request) => request.url === "/api/control" && request.options.body.includes('"action":"decline"'));
  assert.ok(control, "declining from the inbox sends the control command");
  assert.deepEqual(JSON.parse(control.options.body), { town: "acme/project", role: "hall", action: "decline", task: "issue:3" }, "the command targets the item's own town, not the selected one");
  assert.equal(elements["inbox-error"].textContent, "");
});

test("reopening a Mayoral card refreshes live details and retries failures", async () => {
  let calls = 0;
  const { elements, requests } = await openMayoralDecision(async () => {
    calls++;
    if (calls === 1) return { ok: false, json: async () => ({ error: "temporary failure" }) };
    return { ok: true, json: async () => ({
      kind: "issue", number: 3, title: "Outside request", body: "Fresh detail",
      author: "octo", comments: 5, state: "open",
      updated_at: "2026-09-01T10:00:00Z", url: "https://example.com/i/3",
    }) };
  });
  assert.match(elements.inspection.textContent, /Live details unavailable/);
  elements.houses.querySelectorAll("[data-house]").find((button) => button.dataset.house === "hall").onclick();
  elements.inspection.querySelectorAll("[data-task]").find((button) => button.dataset.task === "issue:3").onclick();
  for (let i = 0; i < 10; i++) await new Promise((resolve) => setImmediate(resolve));
  assert.equal(requests.filter((request) => request.url === "/api/task-detail").length, 2);
  assert.match(elements.inspection.textContent, /Fresh detail/);
  assert.match(elements.inspection.textContent, /5 comments/);
});

test("the page follows a restarted service: managed upgrades report restarting and a new version reloads the assets", async () => {
  const elements = installFixture();
  let reloads = 0;
  globalThis.location.reload = () => { reloads++; };
  let releaseSecond;
  const messages = [
    `data: ${JSON.stringify({ ...state, version: "1.0.0" })}\n\n`,
    new Promise((resolve) => { releaseSecond = () => resolve(`data: ${JSON.stringify({ ...state, seq: 2, version: "1.1.0" })}\n\n`); }),
  ];
  globalThis.fetch = async (url) => {
    if (url === "/api/events") {
      let index = 0;
      return { ok: true, body: { getReader: () => ({ read: async () => index < messages.length ? { value: new TextEncoder().encode(await messages[index++]), done: false } : { done: true } }) } };
    }
    if (url === "/api/update") return { ok: true, json: async () => ({ ok: true, restarting: true }) };
    if (url === "/api/harnesses") return { ok: true, json: async () => ({ demo: true, agents: [] }) };
    return { ok: true, json: async () => ({}) };
  };
  await import(`./app.js?reload-test=${Date.now()}`);
  for (let i = 0; i < 5; i++) await new Promise((resolve) => setImmediate(resolve));
  await elements["update-notice"].onclick();
  assert.match(elements["update-notice"].textContent, /restarting/);
  assert.equal(reloads, 0, "the same version never reloads");
  releaseSecond();
  for (let i = 0; i < 5; i++) await new Promise((resolve) => setImmediate(resolve));
  assert.equal(reloads, 1, "a different service version reloads the page once");
});

test("responsive and reduced-motion contracts remain shipped in the stylesheet", () => {
  const css = readFileSync(new URL("./style.css", import.meta.url), "utf8");
  assert.match(css, /@media\s*\(max-width:\s*760px\)/);
  assert.match(css, /@media\s*\(prefers-reduced-motion:\s*reduce\)/);
  assert.match(css, /\.view-switcher/);
  assert.match(css, /\.operations-board/);
});
