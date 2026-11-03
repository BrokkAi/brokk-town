import test from "node:test";
import assert from "node:assert/strict";
import { authHeaders, readSession, signInError, whoLabel } from "./account.js";

test("a signed-in browser sends no access key", () => {
  assert.deepEqual(authHeaders(""), {});
  assert.deepEqual(authHeaders("abc"), { Authorization: "Bearer abc" });
});

test("a failed sign-in's message is read from the address", () => {
  assert.equal(signInError("#signin-error=Sign-in%20was%20cancelled."), "Sign-in was cancelled.");
  assert.equal(signInError("#token=abc"), "");
  assert.equal(signInError(""), "");
});

test("an unreachable or failing session endpoint means a local Town", async () => {
  assert.deepEqual(await readSession(async () => { throw new Error("offline"); }), { mode: "local" });
  assert.deepEqual(await readSession(async () => ({ ok: false })), { mode: "local" });
  const shared = { mode: "multi-user", providers: ["github"], user: null };
  assert.deepEqual(await readSession(async () => ({ ok: true, json: async () => shared })), shared);
});

test("questions and changes name who made them in a shared Town", () => {
  assert.equal(whoLabel("Ada (@ada)", { shared: true }), "Ada (@ada)");
  assert.equal(whoLabel("", { shared: true }), "Operator");
  assert.equal(whoLabel(""), "You");
});
