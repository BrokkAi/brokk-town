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
    if (selector === ".town-controls") return elements["start-all"]?.parent || elements["start-all"];
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
  return elements;
}

const state = {
  seq: 1,
  demo: true,
  capacity: { active: 1, limit: 4 },
  service_config: { max_workers: 4 },
  towns: {
    "acme/project": {
      id: "acme/project",
      config: { repo: "acme/project", branch: "main", bot_agents: {}, harness: "codex-acp", model: "m", effort: "medium" },
      workers: {
        issue: { role: "issue", status: "working", enabled: true, task: "Implementing", logs: [], agent: { harness: "codex-acp", model: "m", effort: "medium" } },
        review: { role: "review", status: "waiting", enabled: true, next: "0001-01-01T00:00:00Z", logs: [] },
        repo: { role: "repo", status: "paused", enabled: true, logs: [] },
      },
      tasks: {
        "pr:1": { id: "pr:1", kind: "pr", number: 1, title: "Review this change", house: "review", stage: "awaiting_author" },
      },
      intents: {}, reports: [], events: [],
    },
  },
  events: [],
};

test("app handlers render views, inspect work, preserve focused capacity input, and retain keyboard control", async () => {
  const elements = installFixture();
  const requests = [];
  let releaseSecond;
  const messages = [
    `data: ${JSON.stringify(state)}\n\n`,
    new Promise((resolve) => { releaseSecond = () => resolve(`data: ${JSON.stringify({ ...state, seq: 2, capacity: { active: 2, limit: 7 } })}\n\n`); }),
  ];
  globalThis.fetch = async (url, options = {}) => {
    requests.push({ url, options });
    if (url === "/api/events") {
      let index = 0;
      return { ok: true, body: { getReader: () => ({ read: async () => index < messages.length ? { value: new TextEncoder().encode(await messages[index++]), done: false } : { done: true } }) } };
    }
    if (url === "/api/capacity") return { ok: true, json: async () => state.capacity };
    if (url === "/api/state") return { ok: true, json: async () => state };
    if (url === "/api/harnesses") return { ok: true, json: async () => ({ demo: true, agents: [] }) };
    return { ok: true, json: async () => ({}) };
  };
  await import(`./app.js?dom-test=${Date.now()}`);
  for (let i = 0; i < 5; i++) await new Promise((resolve) => setImmediate(resolve));
  assert.equal(elements["capacity-summary"].textContent, "1/4 workers");

  const views = document.querySelectorAll("#view-switcher [data-view]");
  views.find((button) => button.dataset.view === "board").onclick();
  assert.equal(elements.board.hidden, false);
  assert.equal(elements.world.hidden, true);
  assert.equal(elements.compact.hidden, true);
  const boardTask = elements.board.querySelector("[data-board-task]");
  assert.ok(boardTask, "board renders a task card from the snapshot");
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

  elements["capacity-settings"].onclick();
  assert.equal(elements["capacity-dialog"].open, true);
  elements["capacity-input"].focus();
  elements["capacity-input"].value = "9";
  releaseSecond();
  await new Promise((resolve) => setImmediate(resolve));
  assert.equal(elements["capacity-input"].value, "9", "focused draft survives SSE redraw");
  await elements["capacity-form"].onsubmit({
    preventDefault() {},
    submitter: new Element("button"),
  });
  assert.equal(requests.some((request) => request.url === "/api/capacity"), true);
  assert.equal(elements["capacity-dialog"].open, false);
});

test("responsive and reduced-motion contracts remain shipped in the stylesheet", () => {
  const css = readFileSync(new URL("./style.css", import.meta.url), "utf8");
  assert.match(css, /@media\s*\(max-width:\s*760px\)/);
  assert.match(css, /@media\s*\(prefers-reduced-motion:\s*reduce\)/);
  assert.match(css, /\.view-switcher/);
  assert.match(css, /\.operations-board/);
});
