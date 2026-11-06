import test from "node:test";
import assert from "node:assert/strict";
import { account, authHeaders, readSession, signInError, whoLabel } from "./account.js";

test("a signed-in browser sends no access key", () => {
  assert.deepEqual(authHeaders(""), {});
  assert.deepEqual(authHeaders("abc"), { Authorization: "Bearer abc" });
});

test("a failed sign-in's message is read from the address", () => {
  assert.equal(signInError("#signin-error=Sign-in%20was%20cancelled."), "Sign-in was cancelled.");
  assert.equal(signInError("#token=abc"), "");
  assert.equal(signInError(""), "");
});

test("an unreachable or failing session endpoint offers no providers", async () => {
  const none = { providers: [], user: null };
  assert.deepEqual(await readSession(async () => { throw new Error("offline"); }), none);
  assert.deepEqual(await readSession(async () => ({ ok: false })), none);
  const session = { providers: ["github"], user: null };
  assert.deepEqual(await readSession(async () => ({ ok: true, json: async () => session })), session);
});

test("questions and changes name who made them", () => {
  assert.equal(whoLabel("Ada (@ada)"), "Ada (@ada)");
  assert.equal(whoLabel(""), "Operator");
});

function dialogs() {
  const elements = {};
  const $ = (selector) => (elements[selector] ||= {
    hidden: false, open: false, textContent: "",
    showModal() { this.open = true; },
    addEventListener() {},
  });
  return { $, elements };
}

test("a refused access key explains how to get a new one; everyone else signs in", () => {
  const { $, elements } = dialogs();
  const signIn = account({ $, getSession: () => ({ providers: ["github"], user: null }), getToken: () => "" });
  signIn.showSignIn("", true);
  assert.equal(elements["#connect-dialog"].open, true);
  assert.equal(elements["#signin-dialog"]?.open ?? false, false);
  signIn.showSignIn("Your sign-in expired. Sign in again.");
  assert.equal(elements["#signin-dialog"].open, true);
  assert.equal(elements["#signin-github"].hidden, false);
  assert.equal(elements["#signin-google"].hidden, true);
  assert.equal(elements["#signin-error"].textContent, "Your sign-in expired. Sign in again.");
});

test("a Town that named no providers says to reload", () => {
  const { $, elements } = dialogs();
  account({ $, getSession: () => ({ providers: [], user: null }), getToken: () => "" }).showSignIn();
  assert.match(elements["#signin-error"].textContent, /Reload the page/);
});
