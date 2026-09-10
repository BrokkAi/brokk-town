import test from "node:test";
import assert from "node:assert/strict";
import { seededRandom, workerPose, easeDelivery } from "./scenery.js";
import { routePosition } from "./town.js";

test("reduced motion freezes workers at every phase", () => {
  for (const role of ["bug", "issue", "review", "release", "repo"]) {
    assert.deepEqual(
      workerPose(role, 0, false),
      workerPose(role, 15930, false),
    );
    assert.notDeepEqual(
      workerPose(role, 0, true),
      workerPose(role, 1513, true),
    );
  }
});
test("eased deliveries keep endpoints and move monotonically on their route", () => {
  assert.equal(easeDelivery(-1), 0);
  assert.equal(easeDelivery(2), 1);
  let prev = 0;
  for (let i = 0; i <= 100; i++) {
    const p = easeDelivery(i / 100);
    assert.ok(p >= prev && p <= 1);
    prev = p;
  }
  for (const p of [0, 1])
    assert.deepEqual(
      routePosition("issue", "review", p),
      routePosition("issue", "review", easeDelivery(p)),
    );
});
test("town landscaping stays stable across frames and differs by repository", () => {
  const a = seededRandom("acme/a"),
    b = seededRandom("acme/a"),
    c = seededRandom("acme/b");
  const values = Array.from({ length: 50 }, () => a());
  assert.deepEqual(
    values,
    Array.from({ length: 50 }, () => b()),
  );
  assert.notDeepEqual(
    values,
    Array.from({ length: 50 }, () => c()),
  );
});
