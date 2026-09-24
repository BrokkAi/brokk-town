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
    // Scrollable list containers are the non-button descendants needed by
    // this shim. Keep them shallow: app.js only needs their identity, focus,
    // and scroll offsets to exercise redraw preservation.
    const lists = /<div\b([^>]*(?:\bdata-board-list="[^"]+"|class="house-task-queue")[^>]*)>[\s\S]*?<\/div>/gi;
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
    // A themed label carries the key its string is swapped by; the fixture
    // keeps it so a skin test can assert on what the page actually shows.
    const skinKey = opening.match(/data-skin-text="([^"]+)"/)?.[1];
    if (skinKey) element.dataset.skinText = skinKey;
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
  towns: {
    "acme/project": {
      id: "acme/project",
      config: { repo: "acme/project", branch: "main", bot_agents: { review: { harness: "claude-acp", model: "claude-opus-5", effort: "high", inherited: false }, repo: { harness: "codex-acp", model: "repo-repair-model", effort: "low", inherited: false } }, harness: "codex-acp", model: "m", effort: "medium" },
      workers: {
        issue: { role: "issue", status: "working", enabled: true, task: "Implementing", logs: [], agent: { harness: "codex-acp", model: "m", effort: "medium" } },
        review: { role: "review", status: "waiting", enabled: true, next: "0001-01-01T00:00:00Z", logs: [] },
        repo: { role: "repo", status: "paused", enabled: true, logs: [] },
        simplifier: { role: "simplifier", status: "waiting", enabled: true, logs: [] },
        hall: { role: "hall", status: "waiting", enabled: true, task: "Ready when you are", logs: [] },
      },
      bulletins: [
        { at: "2026-09-21T12:00:00Z", since: "2026-09-21T06:00:00Z", until: "2026-09-21T12:00:00Z", title: "Exports you can trust", summary: "Exports keep your filters and can be downloaded as CSV.", pulls: [12, 14], items: [{ kind: "fix", title: "Exports keep the active filter", detail: "An export no longer drops the filter you were viewing.", pulls: [12], issues: [9] }, { kind: "feature", title: "Download reports as CSV", pulls: [14] }] },
      ],
      tasks: {
        "pr:1": { id: "pr:1", kind: "pr", number: 1, title: "Review this change", house: "review", stage: "awaiting_author" },
        "issue:2": { id: "issue:2", kind: "issue", number: 2, title: "Queue this change", house: "issue", stage: "queued" },
        "source:done": { id: "source:done", kind: "source", title: "Checked off in Slack", house: "issue", stage: "complete", source: { eligible: false } },
        "issue:3": { id: "issue:3", kind: "issue", number: 3, title: "Outside request", house: "hall", stage: "awaiting_mayor", external: true, mayoral_decision: "pending", simplification: { mode: "suggest", decision: "decline", summary: "Low value", detail: "The request adds a second registry for one caller." } },
        "pr:4": { id: "pr:4", kind: "pr", number: 4, title: "Speculative matrix", house: "hall", stage: "declined", external: true, simplification: { mode: "auto", decision: "decline", detail: "No callers." } },
      },
      intents: {}, reports: [], events: [],
    },
  },
  events: [],
};

const baseState = state;

test("app handlers render views, inspect work, preserve focused capacity input, and retain keyboard control", async () => {
  const state = structuredClone(baseState);
  Object.assign(state.towns["acme/project"].tasks, {
        "issue:70": { id: "issue:70", kind: "issue", number: 70, title: "Waiting intake issue", house: "simplifier", stage: "simplifying" },
        "pr:74": { id: "pr:74", kind: "pr", number: 74, title: "Waiting intake PR", house: "simplifier", stage: "simplifying", blocked: true },
  });
  const elements = installFixture();
  const requests = [];
  const createdLinks = [];
  let releaseSecond, releaseIntake, releaseThird;
  const messages = [
    `data: ${JSON.stringify(state)}\n\n`,
    new Promise((resolve) => { releaseSecond = () => resolve(`data: ${JSON.stringify({ ...state, seq: 2, capacity: { active: 2, limit: 7 } })}\n\n`); }),
    new Promise((resolve) => { releaseIntake = () => resolve(`data: ${JSON.stringify({ ...state, seq: 3 })}\n\n`); }),
    new Promise((resolve) => { releaseThird = () => resolve(`data: ${JSON.stringify({ ...state, seq: 4, capacity: { active: 3, limit: 8 } })}\n\n`); }),
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

  const views = document.querySelectorAll("#view-switcher [data-view]");
  views.find((button) => button.dataset.view === "board").onclick();
  assert.equal(elements.board.hidden, false);
  assert.equal(elements.world.hidden, true);
  assert.equal(elements.compact.hidden, true);
  const boardLists = elements.board.querySelectorAll("[data-board-list]");
  assert.equal(boardLists.length, 6, "board exposes each populated column as its own list");
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
  assert.doesNotMatch(doneTask.textContent, /Next profile/, "done work does not advertise another dispatch");
  for (const part of ["profile-chip harness", "profile-chip model", "profile-chip effort"])
    assert.ok(
      elements.board.innerHTML.includes(part),
      `board names every part of a dispatch profile (${part})`,
    );
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
  assert.equal(elements["town-state"].textContent, "Awake · 4 agents");
  assert.equal(elements["town-toggle"].textContent, "Ⅱ Pause the town", "toggle offers the action that changes state");
  assert.equal(elements["town-toggle"].classList.contains("primary"), false);
  assert.equal(elements["pause-all"].hidden, true, "no separate pause-all when every agent is awake");
  const houseLabel = (role) =>
    elements.houses.querySelectorAll("[data-house]").find((button) => button.dataset.house === role).textContent;
  assert.match(houseLabel("review"), /claude/, "the village names each house's harness");
  assert.match(houseLabel("review"), /claude-opus-5/, "the village names each house's model");
  assert.match(houseLabel("review"), /high/, "the village names each house's effort");
  assert.match(houseLabel("issue"), /codex/, "a house inheriting town defaults still shows them");
  assert.match(houseLabel("issue"), /medium/, "a house inheriting town defaults still shows its effort");
  assert.match(houseLabel("repo"), /repo-repair-model/, "the watchtower names its repair profile");
  assert.match(houseLabel("simplifier"), /SIMPLIFIER/, "a house painted from its own sprite still gets a label");
  assert.match(houseLabel("simplifier"), /codex/, "the clarifier names the harness it inherits");
  assert.ok(
    elements.houses.innerHTML.includes('class="profile own"'),
    "a house with its own profile is distinguished from an inheriting one",
  );
  assert.ok(elements.houses.innerHTML.includes('class="profile inherited'), "inherited profiles are marked as inherited");
  document.dispatchEvent({ type: "keydown", key: "8", target: new Element("div") });
  assert.match(elements.inspection.textContent, /THE CLARIFIER/, "the eighth shortcut visits Simplifier Bot");
  document.dispatchEvent({ type: "keydown", key: "t", target: new Element("div") });
  assert.equal(elements.houses.innerHTML.includes("workload-counts"), false, "houses use the compact count line");
  assert.match(houseLabel("simplifier"), /0 \/ 1 \/ 1/);
  assert.match(elements.houses.innerHTML, /class="house-counts" title="0 active · 1 waiting · 1 blocked"/);
  assert.match(houseLabel("hall"), /1 to decide/);
  assert.match(elements.board.textContent, /simplifier · waiting.*0 active.*1 waiting.*1 blocked/s);
  assert.match(elements.inspection.textContent, /1 issue · 1 pull request · 1 blocked/);
  const intakeScroll = elements.inspection.querySelector(".house-task-queue");
  intakeScroll.scrollTop = 127;
  for (let number = 100; number < 145; number++) {
    state.towns["acme/project"].tasks[`issue:${number}`] = { id: `issue:${number}`, kind: "issue", number, title: `Intake ${number}`, house: "simplifier", stage: "simplifying" };
  }
  releaseIntake();
  await new Promise((resolve) => setImmediate(resolve));
  assert.equal(elements.inspection.querySelector(".house-task-queue").scrollTop, 127, "queue scroll survives changed inspector markup");
  assert.ok(elements.inspection.querySelectorAll("[data-task]").some((button) => button.dataset.task === "issue:144"), "backlog beyond forty items remains accessible");

  assert.ok(elements.inspection.innerHTML.indexOf('data-task="issue:70"') < elements.inspection.innerHTML.indexOf('class="agent-card"'), "intake is visible before agent configuration");
  assert.match(elements.board.textContent, /Simplifier queue/);
  assert.match(elements.board.textContent, /Awaiting Simplifier/);

  elements.houses.querySelectorAll("[data-house]").find((button) => button.dataset.house === "issue").onclick();
  assert.match(elements.inspection.textContent, /Authority:.*create pull requests/, "house controls explain their write authority");
  assert.match(elements.inspection.textContent, /Harness.*codex-acp/s, "the inspector spells the harness out in full");
  assert.match(elements.inspection.textContent, /Effort.*medium/s, "the inspector spells the effort out in full");
  elements.houses.querySelectorAll("[data-house]").find((button) => button.dataset.house === "repo").onclick();
  assert.match(elements.inspection.textContent, /Configure agent/, "the watchtower exposes its repair profile");
  assert.match(elements.inspection.textContent, /Inventory runs without an agent/, "the watchtower explains when its profile is used");
  // Arriving from another house, so the clarifier's own label is what opens it.
  elements.houses.querySelectorAll("[data-house]").find((button) => button.dataset.house === "simplifier").onclick();
  assert.match(elements.inspection.textContent, /Authority:.*file simplification issues/, "the clarifier's label opens the clarifier");
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
  assert.match(elements.inspection.textContent, /Simplifier Bot · suggest mode · decline/);
  assert.match(elements.inspection.textContent, /second registry for one caller/);
  assert.match(elements.inspection.textContent, /Demo town/, "demo explains why no live body follows");
  assert.equal(requests.some((request) => request.url === "/api/task-detail"), false, "demo never fetches live details");
  await elements.inspection.querySelectorAll("button").find((button) => button.id === "admit-task").onclick();
  assert.equal(requests.some((request) => request.url === "/api/control" && request.options.body.includes('"action":"admit"')), true);
  elements.houses.querySelectorAll("[data-house]").find((button) => button.dataset.house === "hall").onclick();
  const declined = elements.inspection.querySelectorAll("[data-task]").find((button) => button.dataset.task === "pr:4");
  assert.ok(declined, "Town Hall lists work Simplifier declined");
  declined.onclick();
  assert.match(elements.inspection.textContent, /Simplifier declined this/, "the overrule explains itself");
  globalThis.confirm = () => false;
  await elements.inspection.querySelectorAll("button").find((button) => button.id === "admit-task").onclick();
  assert.equal(requests.some((request) => request.url === "/api/control" && request.options.body.includes('"task":"pr:4"')), false, "refusing the confirmation sends nothing");
  globalThis.confirm = (message) => { assert.match(message, /over Simplifier's decline/); return true; };
  await elements.inspection.querySelectorAll("button").find((button) => button.id === "admit-task").onclick();
  globalThis.confirm = () => true;
  assert.equal(requests.some((request) => request.url === "/api/control" && request.options.body.includes('"action":"admit"') && request.options.body.includes('"task":"pr:4"')), true, "admitting anyway uses the Mayoral decision command");
  elements.houses.querySelectorAll("[data-house]").find((button) => button.dataset.house === "hall").onclick();
  elements.houses.querySelectorAll("[data-house]").find((button) => button.dataset.house === "hall").onclick();
  assert.match(elements.inspection.textContent, /Mayor Bot waiting/, "Town Hall shows Mayor Bot's status");
  assert.match(elements.inspection.textContent, /What changed/, "Town Hall carries the bulletin feed");
  assert.match(elements.inspection.textContent, /Exports you can trust/, "the latest bulletin title is shown");
  assert.match(elements.inspection.textContent, /Download reports as CSV/, "bulletin items are listed");
  assert.match(elements.inspection.textContent, /PR #14/, "bulletin items cite their pull requests");
  const pauseMayor = elements.inspection.querySelectorAll("button").find((button) => button.dataset.action === "pause");
  assert.ok(pauseMayor, "Town Hall offers to pause Mayor Bot");
  await pauseMayor.onclick();
  assert.equal(requests.some((request) => request.url === "/api/control" && request.options.body.includes('"action":"pause"') && request.options.body.includes('"role":"hall"')), true, "pausing Mayor Bot is a house control");

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
    config: { repo: "beta/tools", branch: "main", bot_agents: {}, harness: "codex-acp", model: "", effort: "" },
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

  assert.equal(elements["inbox-count"].textContent, "3", "header badge counts decisions and stuck work across towns");
  assert.equal(elements["inbox-count"].hidden, false);
  assert.ok(elements["inbox-count"].classList.contains("decisions"), "badge highlights pending Mayoral decisions");
  assert.match(elements["inbox-toggle"].getAttribute("aria-label"), /2 awaiting your decision · 1 need attention/);
  const betaLink = elements.towns.querySelectorAll("[data-town]").find((button) => button.dataset.town === "beta/tools");
  assert.match(betaLink.textContent, /1 to decide/, "sidebar shows each town's pending decisions");
  assert.match(betaLink.textContent, /1 attention/, "sidebar shows each town's stuck work");
  assert.match(elements.overview.textContent, /to decide/, "overview cards show decisions waiting");

  document.dispatchEvent({ type: "keydown", key: "i", target: new Element("div") });
  assert.equal(elements["inbox-dialog"].open, true, "keyboard shortcut opens the inbox");
  const opens = elements["inbox-list"].querySelectorAll("[data-inbox-open]");
  assert.deepEqual(
    opens.map((button) => [button.dataset.inboxTown, button.dataset.inboxHouse, button.dataset.inboxTask]),
    [["acme/project", "hall", "issue:3"], ["beta/tools", "hall", "pr:12"], ["beta/tools", "review", ""]],
    "decisions from every town lead (an unknown age counts as the longest wait), then stuck work names the house to inspect",
  );
  assert.match(elements["inbox-list"].textContent, /outside arrival/);
  assert.match(elements["inbox-list"].textContent, /Review Bot/);
  assert.match(elements["inbox-list"].textContent, /gh exploded/, "failed workers carry their error");

  opens[1].onclick();
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

  other.workers.feature = { role: "feature", enabled: true, status: "failed", error: "Muse ACP is not on the service PATH.", logs: [] };
  other.workers.release = { role: "release", enabled: true, status: "failed", error: "release retry budget exhausted; last failure: https://github.com/beta/tools/actions/runs/123: failure", logs: [] };
  elements["inbox-dialog"].close();
  elements["inbox-toggle"].onclick();
  assert.match(elements["inbox-list"].textContent, /Technical details/);
  assert.match(elements["inbox-list"].textContent, /earlier attempt/);
  assert.match(elements["inbox-list"].innerHTML, /href="https:\/\/github\.com\/beta\/tools\/actions\/runs\/123"/, "failed workflow is a direct link");
  const configure = elements["inbox-list"].querySelectorAll("[data-inbox-configure]")[0];
  configure.onclick();
  assert.equal(elements["settings-dialog"].open, true);
  assert.equal(elements["agent-role"].value, "feature", "opens the failed bot's agent settings");
  assert.equal(elements["inspector-town"].textContent, "beta/tools");
  elements["settings-dialog"].close();
  elements["inbox-toggle"].onclick();
  const retry = elements["inbox-list"].querySelectorAll("[data-inbox-retry]")[0];
  const before = requests.filter((r) => r.url === "/api/control").length;
  globalThis.confirm = () => false;
  await retry.onclick();
  assert.equal(requests.filter((r) => r.url === "/api/control").length, before, "canceling sends no command");
  globalThis.confirm = (message) => { assert.match(message, /may publish a release/); return true; };
  await retry.onclick();
  assert.deepEqual(JSON.parse(requests.filter((r) => r.url === "/api/control").at(-1).options.body), { town: "beta/tools", role: "release", action: "retry" });
  const successfulFetch = globalThis.fetch;
  globalThis.fetch = async (url, options) => url === "/api/control"
    ? { ok: false, json: async () => ({ error: "Release retry is unavailable" }) }
    : successfulFetch(url, options);
  await elements["inbox-list"].querySelectorAll("[data-inbox-retry]")[0].onclick();
  assert.equal(elements["inbox-error"].textContent, "Release retry is unavailable", "a rejected retry stays visible in the inbox");
  other.config.merge_policy = "manual";
  elements["inbox-dialog"].close();
  elements["inbox-toggle"].onclick();
  assert.equal(elements["inbox-list"].querySelectorAll("[data-inbox-retry]").length, 0, "manual release policy does not offer automation retry");

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


test("responsive and reduced-motion contracts remain shipped in the stylesheet", () => {
  const css = readFileSync(new URL("./style.css", import.meta.url), "utf8");
  assert.match(css, /@media\s*\(max-width:\s*760px\)/);
  assert.match(css, /@media\s*\(prefers-reduced-motion:\s*reduce\)/);
  assert.match(css, /\.view-switcher/);
  assert.match(css, /\.operations-board/);
  // Effort is an ordinal, so each level has to be distinguishable at a glance.
  for (const rank of ["default", "low", "medium", "high", "max", "custom"])
    assert.match(css, new RegExp(`\\.profile-chip\\.rank-${rank}\\s*\\{`), `effort rank ${rank} has no shade`);
  assert.match(css, /\.profile\.own\s+\.profile-chip/, "a profile set for one house is marked apart from an inherited one");
});

// openTownHall renders one snapshot and opens the Town Hall inspector. The
// client keeps its own copy of a delivered snapshot, so a test that needs
// different town state installs a fresh fixture rather than mutating this one.
async function openTownHall(budget, adjust) {
  return (await openTownHallPanel(budget, adjust)).textContent;
}
async function openTownHallPanel(budget, adjust = () => {}) {
  const elements = installFixture();
  const live = structuredClone(state);
  live.seq = 1;
  live.demo = false;
  live.towns["acme/project"].budget = budget;
  adjust(live.towns["acme/project"]);
  const message = `data: ${JSON.stringify(live)}\n\n`;
  let served = false;
  globalThis.fetch = async (url) => {
    if (url === "/api/events")
      return { ok: true, body: { getReader: () => ({ read: async () => (!served ? ((served = true), { value: new TextEncoder().encode(message), done: false }) : { done: true }) }) } };
    if (url === "/api/state") return { ok: true, json: async () => live };
    if (url === "/api/harnesses") return { ok: true, json: async () => ({ demo: false, agents: [] }) };
    return { ok: true, json: async () => ({}) };
  };
  await import(`./app.js?budget=${Date.now()}-${Math.random()}`);
  for (let i = 0; i < 10; i++) await new Promise((resolve) => setImmediate(resolve));
  elements.towns.querySelectorAll("[data-town]")[0].onclick();
  elements.houses.querySelectorAll("[data-house]").find((button) => button.dataset.house === "hall").onclick();
  return elements.inspection;
}

test("an exhausted agent budget explains itself and never renders absent telemetry as zero", async () => {
  const text = await openTownHall({
    configured: true, period: "day",
    from: "2026-09-22T00:00:00Z", to: "2026-09-23T00:00:00Z",
    attempts: 12, max_attempts: 12, agent_seconds: 300, max_agent_minutes: 0,
    untimed: 2, exhausted: true,
    reason: "Budget reached: 12 of 12 agent attempts this day. New agent work resumes at 00:00 on 23 Sep.",
    advice: "Town cannot cap token or dollar spend: no bundled agent harness reports usage back through the worker protocol.",
    usage: null, cost_usd: null,
  });
  assert.match(text, /Agent budget/, "Town Hall shows the budget");
  assert.match(text, /12 of 12 agent attempts/, "the ceiling is shown against the spend");
  assert.match(text, /5 agent minutes/, "measured agent time is shown in minutes");
  assert.match(text, /2 attempts reported no elapsed time/, "unmeasured attempts are called out");
  assert.match(text, /Tokens not reported/, "missing usage reads as unreported");
  assert.match(text, /cost not reported/, "missing cost reads as unreported");
  assert.doesNotMatch(text, /\$0/, "absent cost is never rendered as zero dollars");
  assert.match(text, /no bundled agent harness reports usage/, "the telemetry limitation is stated");
  assert.match(text, /New agent work resumes/, "an exhausted budget explains when work resumes");
});

test("a town with no budget still reports the agent spend it measured", async () => {
  const text = await openTownHall({
    configured: false, period: "day", from: "2026-09-22T00:00:00Z", to: "2026-09-23T00:00:00Z",
    attempts: 3, agent_seconds: 0, untimed: 0, exhausted: false,
    advice: "Town cannot cap token or dollar spend.", usage: null, cost_usd: null,
  });
  assert.match(text, /3 agent attempts/, "spend is reported without a budget");
  assert.match(text, /No budget set for this town/, "an absent budget is stated plainly");
  assert.doesNotMatch(text, /New agent work resumes/, "an unlimited town is not described as held");
});

test("Town Hall shows quiet hours as a scheduled pause, with Mayor Bot's quiet dot", async () => {
  const panel = await openTownHallPanel(null, (town) => {
    town.quiet_hours = { source: "service", active: true, until: "2026-09-22T08:00:00Z", windows: [], reason: "" };
    town.workers.hall = { ...(town.workers.hall || {}), role: "hall", enabled: true, status: "quiet" };
  });
  assert.match(panel.innerHTML, /<h2>Quiet hours<\/h2><p class="quiet-held">/, "Town Hall explains the hold");
  assert.match(panel.textContent, /Quiet hours until .*service default.*running work finishes/);
  assert.match(panel.innerHTML, /dot quiet"><\/i>Mayor Bot quiet hours/, "Mayor Bot's dot and status read as quiet");
});

test("the world legend explains the quiet dot, and the quiet windows input is labelled", () => {
  const html = readFileSync(new URL("./index.html", import.meta.url), "utf8");
  assert.match(html, /<i class="dot quiet"><\/i\s*><span id="legend-quiet" data-skin-text="legend-quiet"/);
  assert.match(html, /id="settings-quiet-hours"[^>]*aria-label="[^"]+"/, "the town's quiet windows input is labelled");
});

test("a house shows its work policy and marks the inventory that policy holds back", async () => {
  const elements = installFixture();
  const live = structuredClone(state);
  live.seq = 1;
  live.demo = false;
  const t = live.towns["acme/project"];
  t.config.work_policies = [
    { role: "issue", labels: ["agent-ready"], summary: "Takes work labelled agent-ready." },
  ];
  t.tasks["issue:2"].policy_excluded = true;
  t.tasks["issue:5"] = { id: "issue:5", kind: "issue", number: 5, title: "Eligible work", house: "issue", stage: "queued", labels: ["agent-ready"] };
  const message = `data: ${JSON.stringify(live)}\n\n`;
  let served = false;
  globalThis.fetch = async (url) => {
    if (url === "/api/events")
      return { ok: true, body: { getReader: () => ({ read: async () => (!served ? ((served = true), { value: new TextEncoder().encode(message), done: false }) : { done: true }) }) } };
    if (url === "/api/state") return { ok: true, json: async () => live };
    if (url === "/api/harnesses") return { ok: true, json: async () => ({ demo: false, agents: [] }) };
    return { ok: true, json: async () => ({}) };
  };
  await import(`./app.js?policy=${Date.now()}-${Math.random()}`);
  for (let i = 0; i < 10; i++) await new Promise((resolve) => setImmediate(resolve));
  elements.towns.querySelectorAll("[data-town]")[0].onclick();
  elements.houses.querySelectorAll("[data-house]").find((button) => button.dataset.house === "issue").onclick();
  const text = elements.inspection.textContent;
  assert.match(text, /WORK POLICY/, "the house states its policy");
  assert.match(text, /Takes work labelled agent-ready/, "the policy is spelled out");
  assert.match(text, /1 inventory item is held back/, "the held-back count is reported");
  const held = elements.inspection.querySelectorAll("[data-task]").find((button) => button.dataset.task === "issue:2");
  assert.ok(held, "filtered work stays listed rather than vanishing");
  assert.match(held.textContent, /held by this house/, "filtered work says why it is not eligible");
  assert.equal(held.className.includes("filtered"), true, "filtered work is marked apart");
  const eligible = elements.inspection.querySelectorAll("[data-task]").find((button) => button.dataset.task === "issue:5");
  assert.ok(eligible, "eligible work is still queued");
  assert.doesNotMatch(eligible.textContent, /held by this house/, "eligible work is not marked as filtered");
});

// The Frontline skin is a theme: it repaints labels and the map, keeps a
// per-browser choice, and never edits the snapshot it is drawn from.
async function openFrontline({ search = "", extraTowns = [] } = {}) {
  const elements = installFixture();
  document.body = document.createElement("body");
  const stored = new Map();
  globalThis.localStorage = {
    getItem: (key) => (stored.has(key) ? stored.get(key) : null),
    setItem: (key, value) => stored.set(key, String(value)),
  };
  // The token decides whether the client streams at all, so the fixture keeps
  // the one installFixture hands to the other tests.
  globalThis.location = { hash: "#token=test-key", pathname: "/", search };
  const live = structuredClone(baseState);
  live.demo = false;
  for (const id of extraTowns) {
    const town = structuredClone(live.towns["acme/project"]);
    town.id = id;
    town.config.repo = id;
    live.towns[id] = town;
  }
  const message = `data: ${JSON.stringify(live)}\n\n`;
  let served = false;
  globalThis.fetch = async (url) => {
    if (url === "/api/events")
      return { ok: true, body: { getReader: () => ({ read: async () => (!served ? ((served = true), { value: new TextEncoder().encode(message), done: false }) : { done: true }) }) } };
    if (url === "/api/state") return { ok: true, json: async () => live };
    if (url === "/api/harnesses") return { ok: true, json: async () => ({ demo: false, agents: [] }) };
    return { ok: true, json: async () => ({}) };
  };
  await import(`./app.js?frontline=${search}-${Date.now()}-${Math.random()}`);
  for (let i = 0; i < 10; i++) await new Promise((resolve) => setImmediate(resolve));
  elements.towns.querySelectorAll("[data-town]")[0].onclick();
  return { elements, stored, live };
}

test("the frontline skin relabels the town, holds its own faction, and leaves state alone", async () => {
  const { elements, stored, live } = await openFrontline();
  assert.equal(document.body.dataset.skin, "town");
  assert.equal(elements["legend-active"].textContent, "Working");
  assert.equal(elements["activity-heading"].textContent, "Along the way");
  assert.equal(elements["faction-picker"].hidden, true, "the town skin has no armies");
  assert.equal(elements["skin-note"].hidden, true);

  const before = JSON.stringify(live);
  elements.skin.click();
  assert.equal(document.body.dataset.skin, "frontline");
  assert.equal(stored.get("brokk-town-skin"), "frontline", "the chosen theme is remembered");
  assert.equal(elements["legend-active"].textContent, "Engaged");
  assert.equal(elements["legend-blocked"].textContent, "Needs support");
  assert.equal(elements["activity-heading"].textContent, "Along the front");
  assert.equal(elements["all-towns"].textContent, "▧ All bases");
  assert.equal(elements["clock-eyebrow"].textContent, "CAMPAIGN CLOCK");
  assert.match(elements["clock-note"].textContent, /strike/);
  assert.equal(elements["faction-picker"].hidden, false, "a base flies an army");
  assert.equal(elements["skin-note"].hidden, false);
  assert.match(elements["skin-note"].textContent, /nothing here writes to GitHub/);
  assert.match(elements["town-meta"].textContent, /base · sector \d+/);

  const options = elements["faction-select"].children;
  assert.equal(options.length, 4, "auto plus one option per army");
  assert.match(options[0].textContent, /^Automatic · /);

  // A base names its own structures; the town's names are gone.
  elements.houses.querySelectorAll("[data-house]").find((button) => button.dataset.house === "issue").onclick();
  assert.match(elements.inspection.textContent, /Muster Yard|Weaver Ring|Brood Vault/);
  assert.doesNotMatch(elements.inspection.textContent, /THE WORKSHOP/);

  elements["faction-select"].value = "hive";
  elements["faction-select"].onchange();
  assert.equal(
    stored.get("brokk-town-factions"),
    JSON.stringify({ "acme/project": "hive" }),
    "the banner a player picks is saved",
  );
  assert.match(elements["town-meta"].textContent, /Hive base · sector \d+/);
  assert.equal(JSON.stringify(live), before, "the theme never edits town state");

  elements.skin.click();
  assert.equal(document.body.dataset.skin, "town");
  assert.equal(elements["legend-active"].textContent, "Working");
  assert.equal(elements["faction-picker"].hidden, true);
  assert.match(elements.inspection.textContent, /THE WORKSHOP/, "the town names come back");
});

test("?skin=frontline opens the war map for whoever the link is sent to", async () => {
  const { elements } = await openFrontline({ search: "?skin=frontline" });
  assert.equal(document.body.dataset.skin, "frontline");
  assert.equal(elements["activity-eyebrow"].textContent, "LIVE FIELD JOURNAL");
  assert.equal(elements["skin"].textContent, "Theme: Frontline");
  assert.equal(elements["skin"].title, "Switch back to the Brokk Town neighbourhood");
});

test("Frontline overview shows each base's race art even when races repeat", async () => {
  const { elements } = await openFrontline({ search: "?skin=frontline",
    extraTowns: ["acme/repo-3", "acme/repo-5"] });
  const links = elements.towns.querySelectorAll("[data-town]");
  assert.equal(new Set(links.map((link) => link.dataset.faction)).size, 2);
  assert.equal(links.filter((link) => link.dataset.faction === "vanguard").length, 2);
  elements["all-towns"].click();
  const cards = elements.overview.querySelectorAll("[data-visit]");
  assert.equal(new Set(cards.map((card) => card.dataset.faction)).size, 2);
  assert.equal((elements.overview.innerHTML.match(/town-card-houses frontline-card-art/g) || []).length, 3);
  for (const label of ["Vanguard", "Hive"])
    assert.match(elements.overview.textContent, new RegExp(label));
});

test("a faction choice survives blocked browser storage for the session", async () => {
  const { elements } = await openFrontline({ search: "?skin=frontline" });
  let reads = 0;
  localStorage.getItem = () => { reads++; return null; };
  localStorage.setItem = () => { throw new Error("storage unavailable"); };

  elements["faction-select"].value = "hive";
  elements["faction-select"].onchange();
  assert.equal(elements["faction-select"].value, "hive");
  assert.match(elements["town-meta"].textContent, /Hive base/);

  elements.skin.click();
  elements.skin.click();
  assert.equal(elements["faction-select"].value, "hive", "the choice lasts through redraws");
  assert.equal(reads, 0, "redraws do not read browser storage");
});

test("the task inspector snoozes a task and resumes a snoozed one", async () => {
  const elements = installFixture();
  const requests = [];
  const live = structuredClone({ ...state, seq: 1, demo: false });
  live.towns["acme/project"].tasks["issue:5"] = { id: "issue:5", kind: "issue", number: 5, title: "Wait for the vendor", house: "issue", stage: "queued", deferred_until: "2999-01-01T09:00:00Z", defer_reason: "vendor API not released" };
  const message = `data: ${JSON.stringify(live)}\n\n`;
  let served = false;
  globalThis.fetch = async (url, options = {}) => {
    requests.push({ url, options });
    if (url === "/api/events") {
      return { ok: true, body: { getReader: () => ({ read: async () => !served ? (served = true, { value: new TextEncoder().encode(message), done: false }) : { done: true } }) } };
    }
    if (url === "/api/state") return { ok: true, json: async () => live };
    if (url === "/api/harnesses") return { ok: true, json: async () => ({ demo: false, agents: [] }) };
    return { ok: true, json: async () => ({ ok: true }) };
  };
  await import(`./app.js?snooze=${Date.now()}-${Math.random()}`);
  for (let i = 0; i < 10; i++) await new Promise((resolve) => setImmediate(resolve));
  elements.towns.querySelectorAll("[data-town]")[0].onclick();
  elements.houses.querySelectorAll("[data-house]").find((button) => button.dataset.house === "issue").onclick();
  assert.match(elements.inspection.textContent, /1 snoozed/, "the house queue counts snoozed work");
  const snoozedCard = elements.inspection.querySelectorAll("[data-task]").find((button) => button.dataset.task === "issue:5");
  assert.match(snoozedCard.textContent, /Snoozed until/, "the queue card says when the task resumes");

  elements.inspection.querySelectorAll("[data-task]").find((button) => button.dataset.task === "issue:2").onclick();
  const snooze = elements.inspection.querySelectorAll("button").find((button) => button.id === "snooze-task");
  assert.ok(snooze, "a queued task offers a snooze");
  snooze.onclick();
  assert.equal(elements["snooze-dialog"].open, true);
  assert.ok(elements["snooze-until"].value, "the dialog suggests a resume time");
  elements["snooze-until"].value = "2020-01-01T09:00";
  await elements["snooze-form"].onsubmit({ preventDefault() {}, submitter: new Element("button") });
  assert.match(elements["snooze-error"].textContent, /future/, "a past time is refused before it is sent");
  assert.equal(requests.some((request) => request.url === "/api/control"), false);
  const soon = new Date(Date.now() + 2 * 86400000);
  const pad = (n) => String(n).padStart(2, "0");
  const local = `${soon.getFullYear()}-${pad(soon.getMonth() + 1)}-${pad(soon.getDate())}T09:30`;
  elements["snooze-until"].value = local;
  elements["snooze-reason"].value = "  after the release  ";
  await elements["snooze-form"].onsubmit({ preventDefault() {}, submitter: new Element("button") });
  const sent = requests.find((request) => request.url === "/api/control");
  assert.ok(sent, "the snooze is sent through the control API");
  const body = JSON.parse(sent.options.body);
  assert.equal(body.action, "defer");
  assert.equal(body.town, "acme/project");
  assert.equal(body.task, "issue:2");
  assert.equal(body.reason, "after the release");
  assert.equal(body.until, new Date(local).toISOString().replace(/\.\d{3}Z$/, "Z"));
  assert.equal(elements["snooze-dialog"].open, false);

  elements.houses.querySelectorAll("[data-house]").find((button) => button.dataset.house === "issue").onclick();
  elements.inspection.querySelectorAll("[data-task]").find((button) => button.dataset.task === "issue:5").onclick();
  assert.match(elements.inspection.textContent, /Snoozed until/);
  assert.match(elements.inspection.textContent, /vendor API not released/);
  assert.match(elements.inspection.textContent, /Resumes .* without any action/);
  await elements.inspection.querySelectorAll("button").find((button) => button.id === "clear-snooze").onclick();
  const cleared = JSON.parse(requests.filter((request) => request.url === "/api/control").at(-1).options.body);
  assert.equal(cleared.action, "undefer");
  assert.equal(cleared.task, "issue:5");
});

test("every dialog is named by its heading, and the motion toggle reports its real state", async () => {
  const html = readFileSync(new URL("./index.html", import.meta.url), "utf8");
  const dialogs = [...html.matchAll(/<dialog\b([^>]*)>/g)];
  assert.ok(dialogs.length >= 9);
  for (const [, attrs] of dialogs) {
    const id = attrs.match(/\bid="([^"]+)"/)?.[1];
    const label = attrs.match(/aria-labelledby="([^"]+)"/)?.[1];
    assert.ok(label, `${id} has aria-labelledby`);
    assert.match(html, new RegExp(`<h2 id="${label}"`), `${id} is labelled by a heading that exists`);
  }
  assert.match(html, /id="motion"[^>]*aria-pressed="true"[^>]*>Motion on/, "the shipped markup says pressed while motion is on");

  const elements = installFixture();
  globalThis.fetch = async () => ({ ok: false, status: 503, json: async () => ({}) });
  await import(`./app.js?motion=${Date.now()}-${Math.random()}`);
  assert.equal(elements.motion.textContent, "Motion on");
  assert.equal(elements.motion["aria-pressed"], "true");
  elements.motion.onclick();
  assert.equal(elements.motion.textContent, "Motion off");
  assert.equal(elements.motion["aria-pressed"], "false");
});

test("the event stream backs off exponentially, waits while hidden, and retries when shown", async () => {
  const elements = installFixture();
  const timers = [];
  globalThis.setTimeout = (callback, delay) => {
    timers.push({ callback, delay });
    return timers.length;
  };
  let attempts = 0;
  let serve = false;
  const snapshot = `data: ${JSON.stringify(state)}\n\n`;
  globalThis.fetch = async (url) => {
    if (url !== "/api/events") return { ok: true, json: async () => ({}) };
    attempts++;
    if (!serve) return { ok: false, status: 503, json: async () => ({}) };
    let sent = false;
    return { ok: true, body: { getReader: () => ({ read: async () => (!sent ? ((sent = true), { value: new TextEncoder().encode(snapshot), done: false }) : new Promise(() => {})) }) } };
  };
  const flush = async () => { for (let i = 0; i < 5; i++) await new Promise((resolve) => setImmediate(resolve)); };
  try {
    await import(`./app.js?backoff=${Date.now()}-${Math.random()}`);
    await flush();
    assert.equal(attempts, 1);
    assert.equal(elements.connection.textContent, "Reconnecting");
    const delays = [];
    for (let i = 0; i < 8; i++) {
      const timer = timers.at(-1);
      delays.push(timer.delay);
      timer.callback();
      await flush();
    }
    assert.equal(attempts, 9, "each timer makes exactly one attempt");
    delays.forEach((delay, attempt) => {
      const ceiling = Math.min(30000, 1000 * 2 ** attempt);
      assert.ok(delay >= ceiling / 2 && delay <= ceiling, `attempt ${attempt} waits ${delay}ms within [${ceiling / 2}, ${ceiling}]`);
    });
    assert.ok(delays.at(-1) >= 15000 && delays.at(-1) <= 30000, "the wait is capped at thirty seconds");

    // A hidden tab lets the pending timer lapse without connecting.
    const pending = timers.length;
    document.hidden = true;
    timers.at(-1).callback();
    await flush();
    assert.equal(attempts, 9, "no attempt while hidden");
    assert.equal(timers.length, pending, "no new timer while hidden");

    // Showing the tab retries at once, and the stream paints its snapshot.
    serve = true;
    document.hidden = false;
    document.dispatchEvent({ type: "visibilitychange" });
    await flush();
    assert.equal(attempts, 10, "becoming visible reconnects immediately");
    assert.equal(elements.connection.textContent, "Connected");
    assert.match(elements.towns.innerHTML, /data-town="acme\/project"/);
  } finally {
    globalThis.setTimeout = () => 0;
    document.hidden = false;
  }
});

test("a hidden tab keeps the newest snapshot and paints it when shown", async () => {
  const elements = installFixture();
  document.hidden = true;
  let served = false;
  const message = `data: ${JSON.stringify(state)}\n\n`;
  globalThis.fetch = async (url) => {
    if (url === "/api/events")
      return { ok: true, body: { getReader: () => ({ read: async () => (!served ? ((served = true), { value: new TextEncoder().encode(message), done: false }) : new Promise(() => {})) }) } };
    return { ok: true, json: async () => ({}) };
  };
  await import(`./app.js?hidden=${Date.now()}-${Math.random()}`);
  for (let i = 0; i < 5; i++) await new Promise((resolve) => setImmediate(resolve));
  assert.equal(elements.towns.innerHTML, "", "nothing is painted while hidden");
  document.hidden = false;
  document.dispatchEvent({ type: "visibilitychange" });
  assert.match(elements.towns.innerHTML, /data-town="acme\/project"/);
});

test("a write in flight stays disabled across snapshot redraws and focus returns after it settles", async () => {
  const elements = installFixture();
  const live = structuredClone(state);
  live.demo = false;
  let releaseSnapshot;
  const messages = [
    `data: ${JSON.stringify(live)}\n\n`,
    new Promise((resolve) => { releaseSnapshot = () => resolve(`data: ${JSON.stringify({ ...live, seq: 2 })}\n\n`); }),
  ];
  const controls = [];
  globalThis.fetch = async (url, options = {}) => {
    if (url === "/api/events") {
      let index = 0;
      return { ok: true, body: { getReader: () => ({ read: async () => (index < messages.length ? { value: new TextEncoder().encode(await messages[index++]), done: false } : new Promise(() => {})) }) } };
    }
    if (url === "/api/control")
      return new Promise((resolve) => controls.push({ body: JSON.parse(options.body), resolve: () => resolve({ ok: true, json: async () => ({}) }) }));
    if (url === "/api/state") return { ok: true, json: async () => live };
    if (url === "/api/task-detail") return { ok: false, json: async () => ({ error: "offline" }) };
    return { ok: true, json: async () => ({}) };
  };
  const flush = async () => { for (let i = 0; i < 10; i++) await new Promise((resolve) => setImmediate(resolve)); };
  await import(`./app.js?inflight=${Date.now()}-${Math.random()}`);
  await flush();
  elements.towns.querySelectorAll("[data-town]")[0].onclick();
  elements.houses.querySelectorAll("[data-house]").find((button) => button.dataset.house === "issue").onclick();

  const pause = elements.inspection.querySelectorAll("[data-action]").find((button) => button.dataset.action === "pause");
  pause.focus();
  const sent = pause.onclick();
  assert.equal(controls.length, 1);
  assert.deepEqual(controls[0].body, { town: "acme/project", role: "issue", action: "pause", task: "" });
  assert.match(elements.inspection.innerHTML, /data-action="pause" disabled aria-busy="true"/);
  pause.onclick();
  assert.equal(controls.length, 1, "a second press while in flight sends nothing");

  // A snapshot rebuilds the inspector while the request is out.
  releaseSnapshot();
  await flush();
  assert.match(elements.inspection.innerHTML, /data-action="pause" disabled aria-busy="true"/, "the redraw keeps the pending button disabled");
  assert.doesNotMatch(elements.inspection.innerHTML, /data-action="stop" disabled/, "other controls stay usable");

  document.activeElement = document.body;
  controls[0].resolve();
  await sent;
  assert.doesNotMatch(elements.inspection.innerHTML, /aria-busy/, "the control returns once the write settles");
  assert.equal(document.activeElement.dataset.action, "pause", "focus returns to the control that was pressed");

  // The inbox holds both decision buttons for a task while one is in flight.
  const admit = elements["inbox-list"].querySelectorAll("[data-inbox-decide]").find((button) => button.dataset.inboxDecide === "admit");
  const deciding = admit.onclick();
  assert.equal(controls.length, 2);
  assert.match(elements["inbox-list"].innerHTML, /data-inbox-decide="admit" disabled aria-busy="true"/);
  assert.match(elements["inbox-list"].innerHTML, /data-inbox-decide="decline" disabled aria-busy="true"/);
  elements["inbox-list"].querySelectorAll("[data-inbox-decide]").find((button) => button.dataset.inboxDecide === "decline").onclick();
  assert.equal(controls.length, 2, "declining while admitting sends nothing");
  controls[1].resolve();
  await deciding;
  assert.doesNotMatch(elements["inbox-list"].innerHTML, /aria-busy/);
});

test("a write that never answers is released at its deadline, and paired controls block each other", async () => {
  const elements = installFixture();
  const live = structuredClone(state);
  live.demo = false;
  const deadlines = [];
  const timeout = AbortSignal.timeout;
  AbortSignal.timeout = (ms) => {
    const controller = new AbortController();
    deadlines.push({ ms, controller });
    return controller.signal;
  };
  const controls = [];
  globalThis.fetch = async (url, options = {}) => {
    if (url === "/api/events") {
      let sent = false;
      return { ok: true, body: { getReader: () => ({ read: async () => (!sent ? ((sent = true), { value: new TextEncoder().encode(`data: ${JSON.stringify(live)}\n\n`), done: false }) : new Promise(() => {})) }) } };
    }
    if (url === "/api/control") {
      const body = JSON.parse(options.body);
      controls.push({ body, signal: options.signal });
      // The inbox decision's request ignores its signal, as a stuck refresh
      // would: the deadline must release the control regardless.
      if (body.action === "decline") return new Promise(() => {});
      return new Promise((_, reject) => options.signal.addEventListener("abort", () => reject(options.signal.reason)));
    }
    if (url === "/api/state") return { ok: true, json: async () => live };
    if (url === "/api/task-detail") return { ok: false, json: async () => ({ error: "offline" }) };
    return { ok: true, json: async () => ({}) };
  };
  const flush = async () => { for (let i = 0; i < 10; i++) await new Promise((resolve) => setImmediate(resolve)); };
  try {
    await import(`./app.js?deadline=${Date.now()}-${Math.random()}`);
    await flush();
    elements.towns.querySelectorAll("[data-town]")[0].onclick();

    // Town-wide wake and pause share one pending write for the town.
    const waking = elements["town-toggle"].onclick();
    assert.equal(controls.at(-1).body.role, "all");
    assert.equal(elements["town-toggle"].disabled, true);
    assert.equal(elements["pause-all"].disabled, true, "pause waits for the wake to settle");
    elements["pause-all"].onclick();
    assert.equal(controls.length, 1, "pausing the town while it wakes sends nothing");
    assert.equal(deadlines.at(-1).ms, 30000);
    deadlines.at(-1).controller.abort(new DOMException("deadline", "TimeoutError"));
    await waking;
    assert.equal(elements["town-toggle"].disabled, false, "the deadline releases the town controls");
    assert.match(elements.error.textContent, /may still have been applied/, "a lost answer is reported as uncertain, not failed");

    // The inspector's Admit and Decline are one decision.
    elements.houses.querySelectorAll("[data-house]").find((button) => button.dataset.house === "hall").onclick();
    elements.inspection.querySelectorAll("[data-task]").find((button) => button.dataset.task === "issue:3").onclick();
    const find = (id) => elements.inspection.querySelectorAll("button").find((button) => button.id === id);
    const admitting = find("admit-task").onclick();
    assert.match(elements.inspection.innerHTML, /id="admit-task" class="primary" disabled aria-busy="true"/);
    assert.match(elements.inspection.innerHTML, /id="decline-task" class="danger" disabled aria-busy="true"/);
    const before = controls.length;
    find("decline-task").onclick();
    assert.equal(controls.length, before, "declining while admitting sends nothing");
    deadlines.at(-1).controller.abort(new DOMException("deadline", "TimeoutError"));
    await admitting;
    assert.doesNotMatch(elements.inspection.innerHTML, /aria-busy/);

    // A request that ignores its signal is still released at the deadline.
    const declining = elements["inbox-list"].querySelectorAll("[data-inbox-decide]").find((button) => button.dataset.inboxDecide === "decline").onclick();
    assert.match(elements["inbox-list"].innerHTML, /data-inbox-decide="decline" disabled/);
    deadlines.at(-1).controller.abort(new DOMException("deadline", "TimeoutError"));
    await declining;
    assert.doesNotMatch(elements["inbox-list"].innerHTML, /aria-busy/);
    assert.match(elements["inbox-error"].textContent, /may still have been applied/);
  } finally {
    AbortSignal.timeout = timeout;
  }
});
