import test from "node:test";
import assert from "node:assert/strict";
import {
  factionIds,
  factions,
  factionFor,
  factionOf,
  factionName,
  rivalFaction,
  installationRoles,
  installationName,
  installationShortName,
  installationTagline,
  sectorLabel,
  targetLabel,
  frontlineWords,
  frontlineLandscape,
  paintInstallation,
  drawGarrison,
  drawStrike,
  drawImpact,
} from "./frontline.js";
import { skins, skinIds, normalizeSkin, skinFor, skinTextKeys } from "./skins.js";

// A recording context stands in for the canvas: the art modules only ever
// call draw primitives, so this counts and replays them without a browser.
function recording() {
  const ops = [];
  const gradient = { addColorStop() {} };
  const state = {};
  const round = (value) =>
    typeof value === "number" ? Number(value.toFixed(3)) : value;
  const ctx = new Proxy(state, {
    get(object, property) {
      if (property === "ops") return ops;
      if (Object.hasOwn(object, property)) return object[property];
      if (property === "createLinearGradient" || property === "createRadialGradient")
        return (...args) => {
          ops.push([property, ...args]);
          return gradient;
        };
      return (...args) => ops.push([String(property), ...args.map(round)]);
    },
    set(object, property, value) {
      ops.push([`:${String(property)}`, value]);
      object[property] = value;
      return true;
    },
  });
  return { ctx, ops };
}

function installCanvas() {
  const { ctx, ops } = recording();
  globalThis.document = {
    createElement: () => ({ width: 0, height: 0, getContext: () => ctx }),
  };
  return ops;
}

test("a base keeps one army across sessions and every army is reachable", () => {
  assert.deepEqual(
    Array.from({ length: 5 }, () => factionFor("BrokkAi/brokk-town")),
    Array(5).fill(factionFor("BrokkAi/brokk-town")),
    "the derived faction never changes for one repository",
  );
  const seen = new Set(
    Array.from({ length: 120 }, (_, i) => factionFor(`acme/repo-${i}`)),
  );
  assert.deepEqual(
    [...seen].sort(),
    [...factionIds].sort(),
    "all three armies are reachable, so no theme is dead weight",
  );
  assert.equal(factionOf("acme/repo-1", "hive"), "hive", "a chosen banner wins");
  assert.equal(
    factionOf("acme/repo-1", "romulans"),
    factionFor("acme/repo-1"),
    "an unknown saved value falls back to the derived faction",
  );
  // A raid arriving from off-map belongs to another army than the base it hits.
  for (const faction of factionIds)
    assert.notEqual(rivalFaction(faction), faction, `${faction} raids as someone else`);
  assert.equal(new Set(factionIds.map(rivalFaction)).size, factionIds.length);
});

test("every army names all eight installations and shortens them for the journal", () => {
  assert.equal(installationRoles.length, 8);
  for (const faction of factionIds) {
    const names = new Set();
    for (const role of installationRoles) {
      const name = installationName(role, faction),
        short = installationShortName(role, faction),
        tagline = installationTagline(role, faction);
      assert.ok(name && short && tagline, `${faction}/${role} is fully named`);
      assert.ok(!names.has(name), `${faction} names each installation apart`);
      names.add(name);
    }
    assert.equal(names.size, 8);
  }
  // The three armies must read differently, or the theme says nothing.
  for (const role of installationRoles) {
    const named = new Set(factionIds.map((faction) => installationName(role, faction)));
    assert.equal(named.size, 3, `${role} has one name per army`);
  }
  assert.match(sectorLabel("acme/project", "hive"), /^Hive base · sector \d+$/);
  assert.equal(factionName("ascendancy"), "Ascendancy");
  assert.equal(factions.length, 3);
  assert.ok(factions.every((faction) => faction.id && faction.species));
});

test("a target marker carries the real issue or pull request number", () => {
  assert.equal(targetLabel("issue:123"), "▸ #123");
  assert.equal(targetLabel("pr:7"), "▸ PR #7");
  assert.equal(targetLabel(""), "▸ strike");
  assert.equal(targetLabel(undefined), "▸ strike");
});

test("the war theme renames the town's nouns and the town theme leaves them alone", () => {
  assert.equal(frontlineWords("Wake the town"), "Wake the base");
  assert.equal(frontlineWords("Town is paused"), "Base is paused");
  assert.equal(frontlineWords("A house and its deliveries"), "A structure and its strikes");
  assert.equal(frontlineWords("Waiting for work"), "Waiting for work");
  assert.equal(frontlineWords(""), "");
  assert.equal(skins.town.rewrite("Wake the town"), "Wake the town");
  assert.equal(skins.frontline.rewrite("Wake the town"), "Wake the base");
});

test("the map is seeded per base and faction and never drifts", () => {
  const ops = installCanvas();
  frontlineLandscape("acme/project", "hive");
  const first = ops.splice(0).length;
  frontlineLandscape("acme/project", "hive");
  const second = ops.splice(0).length;
  assert.equal(first, second, "one base repaints the same ground");
  assert.ok(first > 400, "the map is painted, not stubbed");

  frontlineLandscape("acme/project", "hive");
  const hive = ops.splice(0);
  frontlineLandscape("acme/project", "vanguard");
  const vanguard = ops.splice(0);
  frontlineLandscape("acme/other", "hive");
  const other = ops.splice(0);
  assert.notDeepEqual(hive, vanguard, "a different banner repaints the ground");
  assert.notDeepEqual(hive, other, "a different repository gets different terrain");
});

test("installations and garrisons draw for every army without a browser", () => {
  const { ctx, ops } = recording();
  for (const faction of factionIds)
    for (const role of installationRoles) {
      ops.length = 0;
      paintInstallation(ctx, { role, x: 300, y: 200, faction, now: 1200, motion: true });
      assert.ok(ops.length > 20, `${faction}/${role} draws a structure`);
      ops.length = 0;
      drawGarrison(ctx, { role, x: 300, y: 200, faction, now: 1200, motion: true });
      assert.ok(ops.length > 20, `${faction}/${role} draws a garrison`);
    }
});

test("a strike frame names its target and repeats exactly for the same event", () => {
  const { ctx, ops } = recording();
  const strike = {
    x: 420,
    y: 330,
    cargo: "issue:123",
    faction: "vanguard",
    now: 2000,
    progress: 0.42,
    direction: 1,
    motion: true,
  };
  drawStrike(ctx, strike);
  const first = ops.splice(0);
  drawStrike(ctx, strike);
  assert.deepEqual(ops.splice(0), first, "the same event paints the same frame");
  assert.ok(
    first.some((op) => op[0] === "fillText" && op[1] === "▸ #123"),
    "the strike carries the identifier a reader can look up",
  );
  ops.length = 0;
  drawImpact(ctx, { x: 420, y: 330, faction: "vanguard", age: 0.4 });
  assert.ok(ops.length > 15, "the impact burst paints");
});

test("both skins answer the same surface and every label the page asks for", () => {
  const surface = [
    "landscape",
    "paintInstallation",
    "paintOccupants",
    "drawStrike",
    "drawImpact",
    "rewrite",
    "strikeFaction",
    "houseLabel",
    "houseLabelMarkup",
    "houseTagline",
    "routeLabel",
    "queueHeading",
    "meta",
    "faction",
    "factionLabel",
  ];
  for (const id of skinIds) {
    const skin = skins[id];
    for (const method of surface)
      assert.equal(typeof skin[method], "function", `${id}.${method} is callable`);
    for (const key of skinTextKeys)
      assert.ok(skin.text[key], `${id} labels ${key}`);
    assert.equal(typeof skin.travelMs, "number");
    assert.equal(typeof skin.impactMs, "number");
    assert.ok(skin.switchLabel && skin.switchTitle && skin.visit);
  }
  assert.equal(normalizeSkin("frontline"), "frontline");
  assert.equal(normalizeSkin("mystery"), "town");
  assert.equal(normalizeSkin(undefined), "town");
  assert.equal(skinFor("nope").id, "town");
  assert.equal(skinFor("frontline").id, "frontline");
  assert.notEqual(
    skins.frontline.houseLabel("bug", "hive"),
    skins.town.houseLabel("bug"),
  );
  assert.match(skins.town.houseLabelMarkup("bug"), /bot-suffix/);
  assert.equal(skins.town.houseLabelMarkup("bug"), skins.town.houseLabelMarkup("bug"));
  assert.equal(skins.town.faction("acme/project"), null, "the town skin has no armies");
  assert.equal(skins.town.strikeFaction({ from: "outside" }), null);
  assert.equal(
    skins.frontline.strikeFaction({ from: "outside" }, "hive"),
    rivalFaction("hive"),
    "an incoming raid flies someone else's banner",
  );
  assert.equal(
    skins.frontline.strikeFaction({ from: "bug", to: "issue" }, "hive"),
    "hive",
    "an internal strike flies the base's banner",
  );
  assert.equal(skins.frontline.meta("acme/project", "main", "hive"), `${sectorLabel("acme/project", "hive")} · main`);
  assert.equal(skins.town.meta("acme/project", "main", null), "main");
  assert.match(skins.frontline.routeLabel({ from: "bug", to: "issue" }, "vanguard"), /⚔ .+ → /);
  assert.equal(skins.town.routeLabel({ from: "bug", to: "issue" }), "bug → issue");
});
