import test from "node:test";
import assert from "node:assert/strict";
import { executionControls } from "./execution.js";

test("runtime selection saves the displayed role and ignores late responses after navigation", async () => {
  const elements = new Map();
  globalThis.document = { querySelector(id) {
    if (!elements.has(id)) elements.set(id, { value: "", replaceChildren() {} });
    return elements.get(id);
  } };
  globalThis.Option = class { constructor(label, value) { this.label = label; this.value = value; } };
  const element = (id) => document.querySelector(`#${id}`);
  const selected = { target_id: "builder", profile_id: "coder" };
  const config = { execution: selected };
  const requests = [];
  let finish, changed = 0;
  const controls = executionControls({
    getConfig: () => config,
    refresh: async () => {},
    onSaved: () => { changed++; },
    api: async (path, body) => {
      if (path === "/api/execution-options") return { configured: true, targets: [{ id: "builder" }], profiles: [{ id: "coder" }] };
      requests.push({ path, body });
      await new Promise((resolve) => { finish = resolve; });
      config.execution_runtimes = { pin: { source: selected, runtime: { harness: "codex", components: [{ name: "provider_cli", version: "1.2.3" }] } } };
      return { ok: true };
    },
  });
  controls.show("acme/app", "review", config);
  await Promise.resolve();
  assert.equal(element("execution-runtime-field").hidden, false);
  assert.match(element("execution-runtime-detail").textContent, /No runtime selected/);
  element("execution-runtime-session").value = " discovery ";
  element("execution-runtime-session").oninput();
  const save = element("save-execution-runtime").onclick();
  assert.equal(element("save-execution-runtime").disabled, true);
  finish();
  await save;
  assert.deepEqual(requests[0], { path: "/api/execution-runtime", body: { town: "acme/app", role: "review", session_id: "discovery" } });
  assert.match(element("execution-runtime-detail").textContent, /provider_cli 1.2.3/);
  assert.equal(changed, 1);

  const lateSave = element("save-execution-runtime").onclick();
  controls.show("acme/other", "issue", config);
  finish();
  await lateSave;
  assert.equal(changed, 1);
  assert.equal(element("execution-result").textContent, "");
  const originalTimeout = AbortSignal.timeout, deadline = new AbortController();
  AbortSignal.timeout = () => deadline.signal;
  try {
    element("execution-runtime-session").value = "discovery";
    const timedSave = element("save-execution-runtime").onclick();
    deadline.abort();
    await timedSave;
    assert.match(element("execution-result").textContent, /may still have been applied/);
    assert.equal(element("save-execution-runtime").disabled, false);
    finish();
    await Promise.resolve();
    await Promise.resolve();
    assert.equal(changed, 1);
  } finally {
    AbortSignal.timeout = originalTimeout;
  }
  controls.close();
});
