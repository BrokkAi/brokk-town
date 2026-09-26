import test, { beforeEach, afterEach } from "node:test";
import assert from "node:assert/strict";
import { attentionSettings } from "./attention.js";

let fields;
beforeEach(() => {
  fields = Object.fromEntries(["attention-hook-form", "attention-hook-enabled", "attention-hook-command", "attention-hook-status", "attention-hook-error", "save-attention-hook", "capacity-dialog"].map((id) => [id, { value: "", checked: false, disabled: false, listeners: {}, addEventListener(name, fn) { this.listeners[name] = fn; } }]));
  globalThis.document = { querySelector: (id) => fields[id.slice(1)] };
});
afterEach(() => { delete globalThis.document; });
const submit = () => fields["attention-hook-form"].onsubmit({ preventDefault() {} });

test("attention form keeps saved commands private and edits only on submission", async () => {
  const calls = [];
  const state = { service_config: { attention_hook: { enabled: false, configured: true } } };
  const control = attentionSettings({ getState: () => state, refresh: async () => {}, api: async (url, body) => { calls.push({url, body}); return {enabled: body.enabled, configured: true}; } });
  control.open();
  assert.equal(calls.length, 0);
  assert.equal(fields["attention-hook-command"].value, "");
  assert.match(fields["attention-hook-status"].textContent, /private command saved/);
  fields["attention-hook-enabled"].checked = true;
  await submit();
  assert.deepEqual(calls[0], {url: "/api/attention-hook", body: {enabled: true}});
  fields["attention-hook-command"].value = '["notify", "private-argument"]';
  await submit();
  assert.deepEqual(calls[1].body.command, ["notify", "private-argument"]);
  assert.equal(fields["attention-hook-command"].value, "");
  assert.doesNotMatch(fields["attention-hook-status"].textContent, /private-argument/);
  fields["attention-hook-command"].value = "private-bad-json";
  await submit();
  assert.equal(calls.length, 2);
  assert.doesNotMatch(fields["attention-hook-error"].textContent, /private-bad-json/);
  fields["capacity-dialog"].listeners.close();
  assert.equal(fields["attention-hook-command"].value, "");
});

test("late attention save cannot override a reopened dialog and duplicates are held", async () => {
  let finish, calls = 0;
  const control = attentionSettings({ getState: () => ({demo: true}), refresh: async () => {}, api: async () => { calls++; return new Promise((resolve) => { finish = resolve; }); } });
  control.open();
  fields["attention-hook-command"].value = '["notify"]';
  const pending = submit();
  await submit();
  assert.equal(calls, 1);
  assert.equal(fields["save-attention-hook"].disabled, true);
  fields["capacity-dialog"].listeners.close();
  control.open();
  finish({enabled: true, configured: true});
  await pending;
  assert.equal(fields["save-attention-hook"].disabled, false);
  assert.match(fields["attention-hook-status"].textContent, /Disabled.*demo never runs hooks/);
});
